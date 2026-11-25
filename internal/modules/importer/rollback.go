package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Annullamento: si elimina ciò che l'import ha creato, se dopo nessuno l'ha toccato.
//   - Un elemento creato dall'import resta se è cambiato dopo (versione diversa da quella
//     scritta), se un import successivo l'ha aggiornato, o se ha commenti, ore o allegati
//     aggiunti in Regia.
//   - Commenti e ore dell'import si eliminano anche dagli elementi che restano (salvo i
//     commenti modificati).
//   - Campi, opzioni, tipi di relazione, membro «importato», spazio e team si eliminano solo
//     se nessuno li usa più.
// Gli aggiornamenti fatti a elementi che esistevano già non si annullano.

type RollbackReport struct {
	Deleted      int  `json:"deleted"`
	Kept         int  `json:"kept"`
	KeptModified int  `json:"kept_modified"`
	KeptUpdated  int  `json:"kept_updated"` // aggiornati da un import successivo
	Comments     int  `json:"comments"`
	Worklogs     int  `json:"worklogs"`
	Relations    int  `json:"relations"`
	Fields       int  `json:"fields"`
	Options      int  `json:"options"`
	SpaceDeleted bool `json:"space_deleted"`
	NotReverted  int  `json:"not_reverted"` // elementi esistenti aggiornati: restano come li ha lasciati l'import
}

// Rollback avvia l'annullamento in background.
func (s *Service) Rollback(ctx context.Context, tenantID string, actor engine.Actor, runID string) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var out *Run
	err := s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		status, _, hb, err := lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		interrupted := status == "running" && (hb == nil || time.Since(*hb) > staleAfter)
		if status != "done" && status != "failed" && !interrupted {
			return engine.InvalidCode("validation.importer.rollback_status", "status", "si annullano solo gli import completati o interrotti")
		}
		var running int
		if err := tx.QueryRow(ctx, `select count(*) from import_runs where target = $1 and status = 'running' and id <> $2`, Target, runID).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return engine.InvalidCode("validation.importer.import_running", "status", "c'è un import in corso: aspetta che finisca")
		}
		if _, err := tx.Exec(ctx, `update import_runs set status = 'running', heartbeat_at = now(), progress = $2 where id = $1`,
			runID, jsonb(Progress{Phase: "annullamento"})); err != nil {
			return err
		}
		out, err = s.run(ctx, tx, runID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.rollback(s.ctx, tenantID, runID, actor); err != nil && s.ctx.Err() == nil {
			s.log.Error("annullamento dell'import fallito", "run", runID, "errore", err)
			_ = s.db.InTenant(context.Background(), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
				_, err := tx.Exec(context.Background(), `update import_runs set status = 'failed', progress = progress || jsonb_build_object('message', $2::text)
					where id = $1`, runID, "annullamento non riuscito: "+err.Error())
				return err
			})
		}
	}()
	return out, nil
}

func (s *Service) rollback(ctx context.Context, tenantID, runID string, actor engine.Actor) error {
	rep := &RollbackReport{}
	inTx := func(fn func(tx pgx.Tx) error) error { return s.db.InTenant(ctx, tenantID, db.TenantOpts{}, fn) }
	var candidates []string
	if err := inTx(func(tx pgx.Tx) error {
		// Elementi creati da questo import: si tengono quelli cambiati dopo.
		rows, err := tx.Query(ctx, `select o.target_id::text,
			case when o.last_run_id <> o.run_id then 'updated'
			     when e.version <> coalesce(o.written_version, 1) then 'modified'
			     when exists (select 1 from comments c where c.element_id = e.id and not exists
			            (select 1 from import_origins x where x.kind = 'comment' and x.target_id = c.id))
			       or exists (select 1 from work_logs w where w.element_id = e.id and not exists
			            (select 1 from import_origins x where x.kind = 'work_log' and x.target_id = w.id))
			       or exists (select 1 from attachments a where a.element_id = e.id and not exists
			            (select 1 from import_created x where x.kind = 'attachment' and x.target_id = a.id)) then 'modified'
			     else '' end
			from import_origins o join elements e on e.tenant_id = o.tenant_id and e.id = o.target_id
			where o.run_id = $1 and o.kind = 'element'`, runID)
		if err != nil {
			return err
		}
		var id, why string
		if _, err := pgx.ForEachRow(rows, []any{&id, &why}, func() error {
			switch why {
			case "updated":
				rep.KeptUpdated++
			case "modified":
				rep.KeptModified++
			default:
				candidates = append(candidates, id)
			}
			return nil
		}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from import_origins where last_run_id = $1 and run_id <> $1 and kind = 'element'`, runID).Scan(&rep.NotReverted)
	}); err != nil {
		return err
	}
	rep.Kept = rep.KeptModified + rep.KeptUpdated
	// I figli prima dei genitori non serve: parent_id diventa null da solo. Si elimina a lotti.
	for i := 0; i < len(candidates); i += 500 {
		batch := candidates[i:min(i+500, len(candidates))]
		if err := inTx(func(tx pgx.Tx) error {
			n, err := deleteElements(ctx, tx, batch)
			rep.Deleted += n
			rep.Kept += len(batch) - n
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `update import_runs set heartbeat_at = now(), progress = $2 where id = $1`, runID,
				jsonb(Progress{Phase: "annullamento", Done: i + len(batch), Total: len(candidates)}))
			return err
		}); err != nil {
			return err
		}
	}
	return inTx(func(tx pgx.Tx) error {
		// Commenti, ore e relazioni dell'import sugli elementi che restano, tranne quelli degli
		// elementi riscritti da un import successivo (che ne ha preso il posto).
		const owned = `not exists (select 1 from import_origins eo where eo.kind = 'element' and eo.target_id = %s and eo.last_run_id <> $1)`
		tag, err := tx.Exec(ctx, `delete from comments c using import_origins o
			where o.run_id = $1 and o.kind = 'comment' and c.id = o.target_id and c.edited_at is null and `+fmt.Sprintf(owned, "c.element_id"), runID)
		if err != nil {
			return err
		}
		rep.Comments = int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, `delete from work_logs w using import_origins o where o.run_id = $1 and o.kind = 'work_log' and w.id = o.target_id
			and `+fmt.Sprintf(owned, "w.element_id"), runID)
		if err != nil {
			return err
		}
		rep.Worklogs = int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, `delete from relations r using import_created c where c.run_id = $1 and c.kind = 'relation' and r.id = c.target_id
			and `+fmt.Sprintf(owned, "r.source_id")+` and `+fmt.Sprintf(owned, "r.target_id"), runID)
		if err != nil {
			return err
		}
		rep.Relations = int(tag.RowsAffected())
		if _, err := tx.Exec(ctx, `update attachments a set removed_at = now() from import_created c
			where c.run_id = $1 and c.kind = 'attachment' and a.id = c.target_id and a.removed_at is null`, runID); err != nil {
			return err
		}
		// Le chiavi d'origine degli oggetti che non ci sono più.
		if _, err := tx.Exec(ctx, `delete from import_origins o where o.run_id = $1 and (
			(o.kind = 'element' and not exists (select 1 from elements e where e.id = o.target_id))
			or (o.kind = 'comment' and not exists (select 1 from comments c where c.id = o.target_id))
			or (o.kind = 'work_log' and not exists (select 1 from work_logs w where w.id = o.target_id)))`, runID); err != nil {
			return err
		}
		// Oggetti di configurazione creati dall'import, se nessuno li usa.
		rows, err := tx.Query(ctx, `select kind, target_id::text from import_created where run_id = $1 and kind in ('option', 'field', 'relation_type', 'member', 'space', 'team')
			order by case kind when 'option' then 1 when 'field' then 2 when 'relation_type' then 3 when 'member' then 4 when 'space' then 5 else 6 end`, runID)
		if err != nil {
			return err
		}
		type obj struct{ Kind, ID string }
		objs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[obj])
		if err != nil {
			return err
		}
		for _, o := range objs {
			ok, err := deleteIfUnused(ctx, tx, o.Kind, o.ID)
			if err != nil {
				return err
			}
			if ok {
				switch o.Kind {
				case "field":
					rep.Fields++
				case "option":
					rep.Options++
				case "space":
					rep.SpaceDeleted = true
				}
			}
		}
		var report Report
		var raw []byte
		if err := tx.QueryRow(ctx, `select coalesce(report, '{}') from import_runs where id = $1`, runID).Scan(&raw); err != nil {
			return err
		}
		_ = json.Unmarshal(raw, &report)
		report.Rollback = rep
		if _, err := tx.Exec(ctx, `update import_runs set status = 'rolled_back', finished_at = now(), report = $2, heartbeat_at = now(),
			progress = $3 where id = $1`, runID, jsonb(report), jsonb(Progress{Phase: "annullato", Done: len(candidates), Total: len(candidates)})); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "import.rollback", EntityType: "import_run", EntityID: runID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"deleted": rep.Deleted, "kept": rep.Kept, "comments": rep.Comments, "worklogs": rep.Worklogs}})
	})
}

// deleteElements elimina gli elementi (con valori, commenti, relazioni e ore). Se un vincolo
// lo impedisce (es. voci di costo registrate a mano) si prova uno per uno e quelli bloccati restano.
func deleteElements(ctx context.Context, tx pgx.Tx, ids []string) (int, error) {
	del := func(tx pgx.Tx, ids []string) (int, error) {
		if _, err := tx.Exec(ctx, `delete from work_logs where element_id = any($1::uuid[])`, ids); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `delete from activities where element_id = any($1::uuid[])`, ids); err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, `delete from elements where id = any($1::uuid[])`, ids)
		return int(tag.RowsAffected()), err
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return 0, err
	}
	n, err := del(sp, ids)
	if err == nil {
		return n, sp.Commit(ctx)
	}
	_ = sp.Rollback(ctx)
	total := 0
	for _, id := range ids {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return total, err
		}
		n, err := del(sp, []string{id})
		if err != nil {
			_ = sp.Rollback(ctx)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// deleteIfUnused elimina un oggetto di configurazione creato dall'import se non è usato.
func deleteIfUnused(ctx context.Context, tx pgx.Tx, kind, id string) (bool, error) {
	var sql string
	switch kind {
	case "option":
		sql = `delete from options o where o.id = $1 and not exists (select 1 from element_values v where v.v_ref = o.id)
			and not exists (select 1 from elements e where e.state_id = o.id)`
	case "field":
		sql = `with gone as (delete from field_defs f where f.id = $1 and not exists (select 1 from element_values v where v.field_id = f.id)
			returning f.option_set_id) delete from option_sets s using gone where s.id = gone.option_set_id`
	case "relation_type":
		sql = `delete from relation_types t where t.id = $1 and not exists (select 1 from relations r where r.type_id = t.id)`
	case "member":
		sql = `delete from members m where m.id = $1 and m.account_id is null
			and not exists (select 1 from elements e where e.assignee_id = m.id or e.reporter_id = m.id)
			and not exists (select 1 from comments c where c.author_id = m.id)`
	case "space":
		sql = `delete from spaces s where s.id = $1 and not exists (select 1 from elements e where e.space_id = s.id)`
	case "team":
		sql = `delete from teams t where t.id = $1 and t.delivery_space_id is null
			and not exists (select 1 from ext_application a where a.owner_team_id = t.id)`
	default:
		return false, errors.New("tipo di oggetto sconosciuto: " + kind)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	tag, err := sp.Exec(ctx, sql, id)
	if err != nil {
		_ = sp.Rollback(ctx)
		return false, nil // usato altrove (vincolo): resta
	}
	if kind == "field" {
		// Con il campo se ne va anche il suo insieme di valori: conta il campo.
		var exists bool
		if err := sp.QueryRow(ctx, `select exists (select 1 from field_defs where id = $1)`, id).Scan(&exists); err != nil {
			_ = sp.Rollback(ctx)
			return false, err
		}
		return !exists, sp.Commit(ctx)
	}
	return tag.RowsAffected() > 0, sp.Commit(ctx)
}
