package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/i18n"
	"regia/internal/platform/tenant"
)

// Cosa è applicato e «ripristina il pacchetto di partenza» per singolo oggetto.
//
// I processi di partenza sono quelli registrati dai moduli (engine.Starter): un processo del tenant
// con la stessa chiave è «di partenza». L'originale è la versione con cui il processo è nato dal
// provisioning (baseline); se la versione pubblicata è diversa è «modificato» e si riporta
// all'originale pubblicando una nuova versione (le richieste già avviate restano sulla versione in
// cui sono, come per ogni pubblicazione).

type avvioProcess struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	TypeName string     `json:"type_name"`
	Version  int        `json:"version"`
	State    string     `json:"state"` // original | modified | unknown
	Archived bool       `json:"archived"`
	HasDraft bool       `json:"has_draft"`
	Restored *time.Time `json:"restored_at"`
}

type avvioCatalog struct {
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Starter    string     `json:"starter"`
	Unverified bool       `json:"unverified"`
	VerifiedAt *time.Time `json:"verified_at"`
	VerifiedBy *string    `json:"verified_by"`
}

type avvioAppliedModel struct {
	Key       string    `json:"key"`
	AppliedAt time.Time `json:"applied_at"`
	AppliedBy *string   `json:"applied_by"`
}

type avvioBodyRow struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type avvioApplied struct {
	Models    []avvioAppliedModel `json:"models"`
	Processes []avvioProcess      `json:"processes"`
	Catalogs  []avvioCatalog      `json:"catalogs"`
	Bodies    []avvioBodyRow      `json:"bodies"`
	Modules   []moduleState       `json:"modules"`
}

// defKey è la definizione di un processo in forma confrontabile: senza le etichette per lingua,
// che sono solo visualizzazione.
func defKey(w *engine.Workflow) string {
	if w == nil {
		return ""
	}
	c := *w
	c.Phases = make([]engine.Phase, len(w.Phases))
	for i, ph := range w.Phases {
		ph.Labels = nil
		c.Phases[i] = ph
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// baseline è il processo com'era alla fine del provisioning: tutte le versioni create dalla stessa
// transazione (creazione del tenant, «tenant provision» o applicazione di un modello) hanno lo stesso
// created_at, perché now() è quello d'inizio transazione. L'ultima di quelle è il pacchetto di partenza
// con le integrazioni dei moduli (gate delle valutazioni e dei requisiti, regole) già dentro: è ciò a
// cui si torna, non il file nudo. 0 = nessuna versione pubblicata.
func baseline(ctx context.Context, tx pgx.Tx, processKey string) (int, error) {
	var n *int
	err := tx.QueryRow(ctx, `select max(v.number) from process_versions v join process_definitions d on d.id = v.process_id
		where d.key = $1 and v.status <> 'draft'
		  and v.created_at = (select min(x.created_at) from process_versions x where x.process_id = d.id)`, processKey).Scan(&n)
	if err != nil || n == nil {
		return 0, err
	}
	return *n, nil
}

// originalOf è la definizione di partenza di un processo (nil se non c'è una versione di riferimento).
func (s *Server) originalOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*engine.Workflow, error) {
	n, err := baseline(ctx, tx, key)
	if err != nil || n == 0 {
		return nil, err
	}
	return s.eng.VersionDefinition(ctx, tx, actor, key, n)
}

func (s *Server) starterBundle(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*engine.Bundle, error) {
	cat, err := s.eng.Catalog(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for i := range cat.Starters {
		if cat.Starters[i].Key == key {
			b := cat.Starters[i]
			return &b, nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Server) avvioApplied(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*avvioApplied, error) {
		return s.readApplied(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) readApplied(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*avvioApplied, error) {
	out := &avvioApplied{Models: []avvioAppliedModel{}, Processes: []avvioProcess{}, Catalogs: []avvioCatalog{}, Bodies: []avvioBodyRow{}}
	rows, err := tx.Query(ctx, `select a.key, a.applied_at, m.display_name from avvio_applicazioni a
		left join members m on m.tenant_id = a.tenant_id and m.id = a.applied_by where a.kind = 'modello' order by a.applied_at`)
	if err != nil {
		return nil, err
	}
	if out.Models, err = pgx.CollectRows(rows, pgx.RowToStructByPos[avvioAppliedModel]); err != nil {
		return nil, err
	}
	if out.Models == nil {
		out.Models = []avvioAppliedModel{}
	}

	cat, err := s.eng.Catalog(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	restored := map[string]time.Time{}
	rr, err := tx.Query(ctx, `select key, restored_at from avvio_applicazioni where kind = 'processo' and restored_at is not null`)
	if err != nil {
		return nil, err
	}
	for rr.Next() {
		var k string
		var at time.Time
		if err := rr.Scan(&k, &at); err != nil {
			rr.Close()
			return nil, err
		}
		restored[k] = at
	}
	rr.Close()
	for _, st := range cat.Starters {
		info, err := s.eng.GetProcess(ctx, tx, actor, st.Key)
		if errors.Is(err, engine.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		row := avvioProcess{Key: st.Key, Name: info.Name, Type: info.TypeKey, TypeName: info.TypeName, State: "modified", Archived: info.Archived, HasDraft: info.Draft != nil}
		for _, v := range info.Versions {
			if v.Status == "published" {
				row.Version = v.Number
			}
		}
		orig, err := s.originalOf(ctx, tx, actor, st.Key)
		if err != nil {
			return nil, err
		}
		switch {
		case orig == nil:
			row.State = "unknown"
		case info.Current != nil && defKey(info.Current) == defKey(orig):
			row.State = "original"
		}
		if at, ok := restored[st.Key]; ok {
			row.Restored = &at
		}
		out.Processes = append(out.Processes, row)
	}

	crows, err := tx.Query(ctx, `select f.key, f.name, f.starter, (a.verified_at is null), a.verified_at, vm.display_name
		from frameworks f
		left join avvio_applicazioni a on a.kind = 'catalogo' and a.key = f.key
		left join members vm on vm.tenant_id = a.tenant_id and vm.id = a.verified_by
		where f.owner_tenant_id = app_tenant() and f.starter is not null order by f.name`)
	if err != nil {
		return nil, err
	}
	if out.Catalogs, err = pgx.CollectRows(crows, pgx.RowToStructByPos[avvioCatalog]); err != nil {
		return nil, err
	}
	if out.Catalogs == nil {
		out.Catalogs = []avvioCatalog{}
	}

	for _, m := range avvioCatalogo {
		for _, b := range m.Bodies {
			var key, name string
			err := tx.QueryRow(ctx, `select a.key, coalesce((select g.name from governance_bodies g where g.name = any($2) limit 1), a.key)
				from avvio_applicazioni a where a.kind = 'organo' and a.key = $1`, b.Key, []string{b.It, b.En}).Scan(&key, &name)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out.Bodies = append(out.Bodies, avvioBodyRow{Key: key, Name: name})
		}
	}
	out.Modules, err = readModules(ctx, tx)
	return out, err
}

// ---------------------------------------------------------------------
// Ripristino di un processo

type avvioRestorePreview struct {
	Key      string           `json:"key"`
	Name     string           `json:"name"`
	Modified bool             `json:"modified"`
	Version  int              `json:"version"`
	HasDraft bool             `json:"has_draft"`
	Elements int              `json:"elements"`
	Current  *engine.Workflow `json:"current"`
	Original *engine.Workflow `json:"original"`
}

func (s *Server) restoreContext(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*avvioRestorePreview, error) {
	if _, err := s.starterBundle(ctx, tx, actor, key); err != nil {
		return nil, err
	}
	info, err := s.eng.GetProcess(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	orig, err := s.originalOf(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if orig == nil {
		return nil, engine.InvalidCode("validation.avvio.restore_invalid", "key", "non c'è una versione di partenza a cui tornare")
	}
	out := &avvioRestorePreview{Key: key, Name: info.Name, HasDraft: info.Draft != nil, Current: info.Current, Original: orig}
	for _, v := range info.Versions {
		if v.Status == "published" {
			out.Version, out.Elements = v.Number, v.Elements
		}
	}
	out.Modified = info.Current == nil || defKey(info.Current) != defKey(orig)
	return out, nil
}

func (s *Server) avvioRestorePreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*avvioRestorePreview, error) {
		return s.restoreContext(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) avvioRestore(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		// FromVersion è la versione che l'anteprima mostrava: se nel frattempo qualcuno ha pubblicato, si ferma.
		FromVersion int `json:"from_version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*avvioApplied, error) {
		ctx := r.Context()
		actor := actorOf(r, p)
		key := r.PathValue("key")
		prev, err := s.restoreContext(ctx, tx, actor, key)
		if err != nil {
			return nil, err
		}
		if in.FromVersion != prev.Version {
			return nil, engine.ErrConflict
		}
		if !prev.Modified {
			return nil, engine.InvalidCode("validation.avvio.nothing_to_restore", "key", "il processo è già quello di partenza")
		}
		raw, err := json.Marshal(prev.Original)
		if err != nil {
			return nil, err
		}
		const notes = "Ripristino del pacchetto di partenza"
		if _, err := s.eng.SaveDraft(ctx, tx, actor, key, raw, notes); err != nil {
			return nil, err
		}
		if _, problems, err := s.eng.Publish(ctx, tx, actor, key, notes); err != nil {
			if engine.HasErrors(problems) {
				return nil, engine.InvalidCode("validation.avvio.restore_invalid", "key", "il pacchetto di partenza non è compatibile con i dati attuali: %s", problems[0].Msg)
			}
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into avvio_applicazioni (tenant_id, model, kind, key, applied_by, restored_by, restored_at)
			values (app_tenant(), 'ripristino', 'processo', $1, $2, $2, now())
			on conflict (tenant_id, kind, key) do update set restored_by = excluded.restored_by, restored_at = now()`, key, p.MemberID); err != nil {
			return nil, err
		}
		lang, err := i18n.TenantLang(ctx, tx)
		if err != nil {
			return nil, err
		}
		if err := tenant.Localize(ctx, tx, lang); err != nil {
			return nil, err
		}
		if err := avvioAudit(ctx, tx, r, p, "avvio.process.restore", map[string]any{"process": key, "from_version": in.FromVersion}); err != nil {
			return nil, err
		}
		return s.readApplied(ctx, tx, actor)
	})
}

// avvioVerify: una persona conferma di aver verificato i testi di un catalogo di partenza.
func (s *Server) avvioVerify(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*avvioApplied, error) {
		ctx := r.Context()
		key := r.PathValue("key")
		var ok bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from frameworks where owner_tenant_id = app_tenant() and key = $1 and starter is not null)`, key).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `insert into avvio_applicazioni (tenant_id, model, kind, key, applied_by, unverified, verified_by, verified_at)
			values (app_tenant(), 'verifica', 'catalogo', $1, $2, false, $2, now())
			on conflict (tenant_id, kind, key) do update set unverified = false, verified_by = excluded.verified_by, verified_at = now()`, key, p.MemberID); err != nil {
			return nil, err
		}
		if err := avvioAudit(ctx, tx, r, p, "avvio.catalog.verify", map[string]any{"catalog": key}); err != nil {
			return nil, err
		}
		return s.readApplied(ctx, tx, actorOf(r, p))
	})
}
