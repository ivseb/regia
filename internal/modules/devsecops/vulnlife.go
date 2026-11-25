package devsecops

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Ciclo di vita delle vulnerabilità (replica/devsecops.md §3.2). Gli stadi:
//
//	open         aperta: nessuno se ne è ancora occupato
//	taken        presa in carico da una persona
//	in_progress  in correzione
//	exception    eccezione approvata (accettazione del rischio, con scadenza)
//	resolved     risolta dalla persona; si chiude da sola quando sparisce dai report
//	closed       chiusa automaticamente: non compare più nei report della versione in produzione
//	false_positive
//
// «status» dei finding dice se la vulnerabilità c'è ancora nei report (open/fixed) o se una
// persona ha deciso (accepted, false_positive); la chiusura e la riapertura automatiche
// seguono quel campo.

// StageNames sono i nomi degli stadi per l'interfaccia e le notifiche.
var StageNames = map[string]string{
	"open": "aperta", "taken": "presa in carico", "in_progress": "in correzione", "exception": "eccezione",
	"resolved": "risolta", "closed": "chiusa automaticamente", "false_positive": "falso positivo",
}

func canAct(a engine.Actor) bool { return a.Has("element.write") || CanConfigure(a) }

// syncVulnLifecycle allinea stadi, scadenze e team dei finding di un servizio dopo che i
// report ne hanno cambiato la presenza. Si chiama alla fine di refreshFindings.
func (s *Service) syncVulnLifecycle(ctx context.Context, tx pgx.Tx, serviceID string) error {
	now := s.now()
	// Spariti dai report della versione in produzione: chiusi da soli.
	if _, err := tx.Exec(ctx, `
		with old as (select id, stage from findings where service_id = $1 and status = 'fixed'
		               and stage in ('open','taken','in_progress','resolved') for update),
		upd as (update findings f set stage = 'closed', closed_at = coalesce(f.closed_at, $2), closed_reason = 'auto', exception_id = null
		        from old where f.id = old.id returning f.id, old.stage as from_stage)
		insert into vuln_events (tenant_id, finding_id, at, kind, from_stage, to_stage, note)
		select app_tenant(), id, $2, 'closed_auto', from_stage, 'closed', 'Non compare più nei report della versione in produzione' from upd`,
		serviceID, now); err != nil {
		return err
	}
	// Ricomparsi dopo la chiusura automatica: di nuovo aperti, con l'escalation da capo.
	if _, err := tx.Exec(ctx, `
		with old as (select id, stage from findings where service_id = $1 and status = 'open' and stage = 'closed' for update),
		upd as (update findings f set stage = 'open', closed_at = null, closed_reason = null, notice_level = 0
		        from old where f.id = old.id returning f.id, old.stage as from_stage)
		insert into vuln_events (tenant_id, finding_id, at, kind, from_stage, to_stage, note)
		select app_tenant(), id, $2, 'reopened_auto', from_stage, 'open', 'È ricomparsa nei report della versione in produzione' from upd`,
		serviceID, now); err != nil {
		return err
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return err
	}
	_, err = s.recomputeDue(ctx, tx, pol, serviceID)
	return err
}

// ---------------------------------------------------------------------------
// Azioni sulle vulnerabilità (anche in blocco)

// BulkInput è un'azione su una o più vulnerabilità.
type BulkInput struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"` // take, assign, start, resolve, reopen, false_positive
	// Team e Assignee valgono per «assign»: Team "auto" torna al team dell'applicazione.
	Team     string `json:"team"`
	Assignee string `json:"assignee"`
	Note     string `json:"note"`
}

// BulkResult dice quante vulnerabilità sono cambiate e quante no, con il motivo.
type BulkResult struct {
	Changed int      `json:"changed"`
	Skipped int      `json:"skipped"`
	Notes   []string `json:"notes"`
}

const maxBulk = 1000

// BulkAction applica l'azione alle vulnerabilità indicate; quelle in uno stadio che non la
// ammette si saltano e si contano a parte.
func (s *Service) BulkAction(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BulkInput) (*BulkResult, error) {
	if !canAct(actor) {
		return nil, engine.ErrForbidden
	}
	ids := dedupe(in.IDs)
	if len(ids) == 0 {
		return nil, engine.Required("ids", "scegli almeno una vulnerabilità")
	}
	if len(ids) > maxBulk {
		return nil, engine.InvalidParams("validation.devsecops.vuln_bulk_limit", "ids", map[string]any{"max": maxBulk}, "al massimo %d vulnerabilità per volta", maxBulk)
	}
	note := clip(in.Note, 2000)
	res := &BulkResult{Notes: []string{}}
	var total int
	if err := tx.QueryRow(ctx, "select count(*) from findings where id = any($1::uuid[])", ids).Scan(&total); err != nil {
		return nil, err
	}
	if total != len(ids) {
		return nil, engine.ErrNotFound
	}
	openish := []string{"open", "taken", "in_progress"}
	var n int
	var err error
	switch in.Action {
	case "take":
		n, err = s.transition(ctx, tx, actor, ids, []string{"open"}, "take", "taken", note,
			"taken_at = $4, assignee_id = coalesce(f.assignee_id, $5::uuid)", true)
	case "start":
		n, err = s.transition(ctx, tx, actor, ids, []string{"open", "taken"}, "start", "in_progress", note,
			"taken_at = coalesce(f.taken_at, $4), assignee_id = coalesce(f.assignee_id, $5::uuid)", true)
	case "resolve":
		n, err = s.transition(ctx, tx, actor, ids, openish, "resolve", "resolved", note,
			"closed_at = $4, closed_reason = 'manual'", true)
	case "false_positive":
		if note == "" {
			return nil, engine.Required("note", "spiega perché è un falso positivo")
		}
		n, err = s.transition(ctx, tx, actor, ids, append(slices.Clone(openish), "resolved"), "false_positive", "false_positive", note,
			"status = 'false_positive', closed_at = $4, closed_reason = 'false_positive'", true)
	case "reopen":
		n, err = s.transition(ctx, tx, actor, ids, []string{"resolved", "false_positive"}, "reopen", "open", note,
			"status = case when f.status = 'false_positive' then 'open' else f.status end, closed_at = null, closed_reason = null, notice_level = 0", true)
	case "assign":
		n, err = s.assign(ctx, tx, actor, ids, in, note)
	default:
		return nil, engine.InvalidValue("action", "azione non valida")
	}
	if err != nil {
		return nil, err
	}
	res.Changed, res.Skipped = n, len(ids)-n
	if res.Skipped > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d non cambiate: non erano in uno stadio che ammette «%s»", res.Skipped, bulkName[in.Action]))
	}
	if in.Action == "reopen" || in.Action == "assign" {
		// Il tempo ricomincia a scorrere o il team è cambiato: scadenze e team si ricalcolano.
		pol, err := loadVulnPolicy(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err := s.recomputeDue(ctx, tx, pol, ""); err != nil {
			return nil, err
		}
	}
	return res, auditEvent(ctx, tx, actor, "devsecops.vuln_"+in.Action, "finding", "", map[string]any{"count": res.Changed, "skipped": res.Skipped})
}

var bulkName = map[string]string{"take": "prendi in carico", "start": "in correzione", "resolve": "risolta", "false_positive": "falso positivo",
	"reopen": "riapri", "assign": "assegna"}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, id := range in {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// transition porta le vulnerabilità negli stadi `from` allo stadio `to` e ne scrive il
// registro. set è il resto della clausola SET con i parametri $4 (istante) e $5 (membro).
// Quelle con una richiesta di eccezione in corso si lasciano stare.
func (s *Service) transition(ctx context.Context, tx pgx.Tx, actor engine.Actor, ids, from []string, kind, to, note, set string, skipPending bool) (int, error) {
	pending := ""
	if skipPending {
		pending = " and exception_id is null"
	}
	tag, err := tx.Exec(ctx, `
		with old as (select id, stage from findings where id = any($1::uuid[]) and stage = any($2::text[])`+pending+` for update),
		upd as (update findings f set stage = $3, `+set+` from old where f.id = old.id returning f.id, old.stage as from_stage)
		insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
		select app_tenant(), id, $4, $5::uuid, $6, from_stage, $3, $7 from upd`,
		ids, from, to, s.now(), nullable(actor.MemberID), kind, note)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// assign cambia team e persona: un team scelto a mano non segue più l'applicazione.
func (s *Service) assign(ctx context.Context, tx pgx.Tx, actor engine.Actor, ids []string, in BulkInput, note string) (int, error) {
	team, assignee := strings.TrimSpace(in.Team), strings.TrimSpace(in.Assignee)
	if team == "" && assignee == "" {
		return 0, engine.Required("team", "scegli un team o una persona")
	}
	if team != "" && team != "auto" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from teams where id::text = $1 and not archived)", team).Scan(&ok); err != nil {
			return 0, err
		}
		if !ok {
			return 0, engine.InvalidCode("validation.team_not_found", "team", "team inesistente")
		}
	}
	if assignee != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", assignee).Scan(&ok); err != nil {
			return 0, err
		}
		if !ok {
			return 0, engine.InvalidCode("validation.member_not_found", "assignee", "persona inesistente")
		}
	}
	tag, err := tx.Exec(ctx, `
		with old as (select id, stage from findings where id = any($1::uuid[]) and stage in ('open','taken','in_progress') for update),
		upd as (update findings f set
		    team_id = case when $2 = '' then f.team_id when $2 = 'auto' then f.team_id else $2::uuid end,
		    team_manual = case when $2 = '' then f.team_manual else $2 <> 'auto' end,
		    assignee_id = case when $3 = '' then f.assignee_id else $3::uuid end,
		    stage = case when $3 <> '' and f.stage = 'open' then 'taken' else f.stage end,
		    taken_at = case when $3 <> '' then coalesce(f.taken_at, $4) else f.taken_at end,
		    notice_level = 0
		  from old where f.id = old.id returning f.id, old.stage as from_stage, f.stage as to_stage)
		insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
		select app_tenant(), id, $4, $5::uuid, 'assign', from_stage, to_stage, $6 from upd`,
		ids, team, assignee, s.now(), nullable(actor.MemberID), note)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
