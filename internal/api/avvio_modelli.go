package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/grc"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/tenant"
)

// Modelli per tipo di azienda. Un modello non duplica nulla: riusa le Provision dei moduli (le stesse
// di «regia tenant provision»), gli starter dei cataloghi normativi (grc/starter) e accende i
// moduli che servono. L'unica cosa che porta di suo sono gli organi di governo tipici del settore.
//
// I testi normativi dei cataloghi di partenza non sono verificati da una persona: l'anteprima e
// l'elenco di ciò che è applicato li segnano «da verificare» finché un amministratore non conferma.

type avvioBody struct {
	Key, Kind, It, En string
}

type avvioModel struct {
	Key       string
	Available bool
	ModulesOn []string
	Catalogs  []string
	Bodies    []avvioBody
}

// avvioCatalogo: il primo modello è banca/assicurazione; gli altri hanno la struttura ma sono «in arrivo».
var avvioCatalogo = []avvioModel{
	{
		Key: "banca_assicurazione", Available: true,
		ModulesOn: []string{"demand", "work", "knowledge", "devsecops", "grc", "architecture"},
		Catalogs:  []string{"dora", "nis2"},
		Bodies: []avvioBody{
			{"rischi_ict", "other", "Comitato rischi ICT", "ICT risk committee"},
			{"sicurezza_info", "other", "Comitato sicurezza delle informazioni", "Information security committee"},
			{"architetture", "architecture_board", "Comitato architetture", "Architecture board"},
			{"modifiche_cab", "cab", "Comitato modifiche (CAB)", "Change advisory board (CAB)"},
		},
	},
	{Key: "software_house"},
	{Key: "pmi"},
	{Key: "pa"},
	{Key: "consulente"},
}

func avvioModelByKey(key string) (avvioModel, bool) {
	for _, m := range avvioCatalogo {
		if m.Key == key {
			return m, true
		}
	}
	return avvioModel{}, false
}

type provisionFn = func(ctx context.Context, tx pgx.Tx, adminMemberID string) error

// avvioProvisioners sono le Provision dei moduli, nello stesso ordine di cmd/regia/tenant.go.
func avvioProvisioners() []provisionFn {
	return []provisionFn{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision, reviews.Provision,
		work.Provision, requirements.Provision, risk.Provision, grc.Provision, knowledge.Provision}
}

func (b avvioBody) name(lang string) string {
	if lang == i18n.En {
		return b.En
	}
	return b.It
}

// ---------------------------------------------------------------------
// Fotografia del tenant, per dire cosa un modello crea e cosa c'è già

type avvioSnap struct {
	types   map[string]string // chiave → nome
	procs   map[string]string
	bodies  map[string]bool // nome
	fws     map[string]bool // chiave
	modules map[string]bool // moduli spenti
}

func takeSnap(ctx context.Context, tx pgx.Tx) (*avvioSnap, error) {
	sn := &avvioSnap{types: map[string]string{}, procs: map[string]string{}, bodies: map[string]bool{}, fws: map[string]bool{}, modules: map[string]bool{}}
	pairs := func(sql string, into map[string]string) error {
		rows, err := tx.Query(ctx, sql)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, n string
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			into[k] = n
		}
		return rows.Err()
	}
	if err := pairs(`select key, name from element_types`, sn.types); err != nil {
		return nil, err
	}
	if err := pairs(`select key, name from process_definitions`, sn.procs); err != nil {
		return nil, err
	}
	names := map[string]string{}
	if err := pairs(`select name, name from governance_bodies`, names); err != nil {
		return nil, err
	}
	for n := range names {
		sn.bodies[n] = true
	}
	fw := map[string]string{}
	if err := pairs(`select key, name from frameworks where owner_tenant_id = app_tenant()`, fw); err != nil {
		return nil, err
	}
	for k := range fw {
		sn.fws[k] = true
	}
	off := map[string]string{}
	if err := pairs(`select module, module from tenant_modules where not enabled`, off); err != nil {
		return nil, err
	}
	for k := range off {
		sn.modules[k] = true
	}
	return sn, nil
}

// runModel porta nel tenant ciò che il modello prevede. Serve sia all'applicazione sia all'anteprima
// (che lo esegue in un punto di salvataggio e lo annulla).
func (s *Server) runModel(ctx context.Context, tx pgx.Tx, p *auth.Principal, actor engine.Actor, m avvioModel, lang string) error {
	for _, prov := range avvioProvisioners() {
		if err := prov(ctx, tx, p.MemberID); err != nil {
			return err
		}
	}
	for _, key := range m.Catalogs {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from frameworks where owner_tenant_id = app_tenant() and key = $1)`, key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.grc.ImportStarter(ctx, tx, actor, key, true); err != nil {
			return err
		}
	}
	for _, b := range m.Bodies {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from governance_bodies where name = any($1))`, []string{b.It, b.En}).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into governance_bodies (tenant_id, id, name, kind, chair_id) values (app_tenant(), $1, $2, $3, $4)`, id, b.name(lang), b.Kind, p.MemberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into governance_body_members (tenant_id, body_id, member_id) values (app_tenant(), $1, $2)`, id, p.MemberID); err != nil {
			return err
		}
	}
	for _, mod := range m.ModulesOn {
		if _, err := tx.Exec(ctx, `insert into tenant_modules (tenant_id, module, enabled, updated_by) values (app_tenant(), $1, true, $2)
			on conflict (tenant_id, module) do update set enabled = true, updated_by = excluded.updated_by, updated_at = now()`, mod, p.MemberID); err != nil {
			return err
		}
	}
	return tenant.Localize(ctx, tx, lang)
}

// ---------------------------------------------------------------------
// Anteprima e applicazione

type avvioItem struct {
	Kind  string `json:"kind"` // module | type | process | body | catalog
	Key   string `json:"key"`
	Name  string `json:"name"`
	State string `json:"state"` // new | present
	// Unverified: testo normativo non ancora verificato da una persona.
	Unverified bool `json:"unverified,omitempty"`
	Detail     int  `json:"detail,omitempty"` // requisiti del catalogo
}

type avvioPreview struct {
	Key       string      `json:"key"`
	Available bool        `json:"available"`
	Items     []avvioItem `json:"items"`
	New       int         `json:"new"`
	Present   int         `json:"present"`
	Applied   bool        `json:"applied"`
}

func (s *Server) buildItems(ctx context.Context, tx pgx.Tx, actor engine.Actor, m avvioModel, lang string, before, after *avvioSnap) ([]avvioItem, error) {
	var items []avvioItem
	state := func(was bool) string {
		if was {
			return "present"
		}
		return "new"
	}
	for _, mod := range m.ModulesOn {
		def, ok := moduleByKey(mod)
		if !ok {
			continue
		}
		items = append(items, avvioItem{Kind: "module", Key: mod, Name: def.Name, State: state(!before.modules[mod])})
	}
	lib, err := s.grc.Library(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, key := range m.Catalogs {
		it := avvioItem{Kind: "catalog", Key: key, Name: key, State: state(before.fws[key]), Unverified: true}
		for _, st := range lib.Starters {
			if st.Key == key {
				it.Name, it.Detail = st.Name, st.Items
			}
		}
		items = append(items, it)
	}
	for _, b := range m.Bodies {
		items = append(items, avvioItem{Kind: "body", Key: b.Key, Name: b.name(lang), State: state(before.bodies[b.It] || before.bodies[b.En])})
	}
	cat, err := s.eng.Catalog(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	typeKeys := map[string]bool{}
	for _, st := range cat.Starters {
		name := st.Name
		if n, ok := after.procs[st.Key]; ok {
			name = n
		}
		_, was := before.procs[st.Key]
		items = append(items, avvioItem{Kind: "process", Key: st.Key, Name: name, State: state(was)})
		typeKeys[st.Type] = true
	}
	for k := range after.types {
		if _, was := before.types[k]; !was {
			typeKeys[k] = true
		}
	}
	for k := range typeKeys {
		if name, ok := after.types[k]; ok {
			_, was := before.types[k]
			items = append(items, avvioItem{Kind: "type", Key: k, Name: name, State: state(was)})
		}
	}
	order := map[string]int{"module": 0, "type": 1, "process": 2, "body": 3, "catalog": 4}
	slices.SortStableFunc(items, func(a, b avvioItem) int {
		if a.Kind != b.Kind {
			return order[a.Kind] - order[b.Kind]
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items, nil
}

func countItems(items []avvioItem) (n, present int) {
	for _, it := range items {
		if it.State == "new" {
			n++
		} else {
			present++
		}
	}
	return n, present
}

func (s *Server) avvioModels(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	type row struct {
		Key       string `json:"key"`
		Available bool   `json:"available"`
		Applied   bool   `json:"applied"`
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		rows, err := tx.Query(r.Context(), `select key from avvio_applicazioni where kind = 'modello'`)
		if err != nil {
			return nil, err
		}
		applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		out := make([]row, 0, len(avvioCatalogo))
		for _, m := range avvioCatalogo {
			out = append(out, row{Key: m.Key, Available: m.Available, Applied: slices.Contains(applied, m.Key)})
		}
		return map[string]any{"items": out}, nil
	})
}

func (s *Server) avvioModelOf(w http.ResponseWriter, r *http.Request) (avvioModel, bool) {
	m, ok := avvioModelByKey(r.PathValue("key"))
	if !ok {
		s.engineError(w, r, engine.ErrNotFound)
		return m, false
	}
	if !m.Available {
		s.engineError(w, r, engine.InvalidCode("validation.avvio.model_unavailable", "key", "il modello non è ancora disponibile"))
		return m, false
	}
	return m, true
}

// avvioPreview dice cosa farebbe il modello senza lasciare nulla: lo esegue in un punto di salvataggio
// e lo annulla, così l'elenco è quello vero e non una descrizione scritta a parte.
func (s *Server) avvioPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	m, ok := s.avvioModelOf(w, r)
	if !ok {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*avvioPreview, error) {
		ctx := r.Context()
		actor := actorOf(r, p)
		lang, err := i18n.TenantLang(ctx, tx)
		if err != nil {
			return nil, err
		}
		before, err := takeSnap(ctx, tx)
		if err != nil {
			return nil, err
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = sp.Rollback(ctx) }()
		if err := s.runModel(ctx, sp, p, actor, m, lang); err != nil {
			return nil, err
		}
		after, err := takeSnap(ctx, sp)
		if err != nil {
			return nil, err
		}
		items, err := s.buildItems(ctx, sp, actor, m, lang, before, after)
		if err != nil {
			return nil, err
		}
		out := &avvioPreview{Key: m.Key, Available: true, Items: items}
		out.New, out.Present = countItems(items)
		err = sp.QueryRow(ctx, `select exists (select 1 from avvio_applicazioni where kind = 'modello' and key = $1)`, m.Key).Scan(&out.Applied)
		return out, err
	})
}

func (s *Server) avvioApply(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	m, ok := s.avvioModelOf(w, r)
	if !ok {
		return
	}
	defer s.mods.forget(p.TenantID)
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*avvioPreview, error) {
		ctx := r.Context()
		actor := actorOf(r, p)
		lang, err := i18n.TenantLang(ctx, tx)
		if err != nil {
			return nil, err
		}
		before, err := takeSnap(ctx, tx)
		if err != nil {
			return nil, err
		}
		if err := s.runModel(ctx, tx, p, actor, m, lang); err != nil {
			return nil, err
		}
		after, err := takeSnap(ctx, tx)
		if err != nil {
			return nil, err
		}
		items, err := s.buildItems(ctx, tx, actor, m, lang, before, after)
		if err != nil {
			return nil, err
		}
		rec := func(kind, key string, unverified bool) error {
			_, err := tx.Exec(ctx, `insert into avvio_applicazioni (tenant_id, model, kind, key, applied_by, unverified)
				values (app_tenant(), $1, $2, $3, $4, $5)
				on conflict (tenant_id, kind, key) do update set applied_by = excluded.applied_by, applied_at = now()`, m.Key, kind, key, p.MemberID, unverified)
			return err
		}
		if err := rec("modello", m.Key, false); err != nil {
			return nil, err
		}
		kinds := map[string]string{"process": "processo", "catalog": "catalogo", "body": "organo"}
		for _, it := range items {
			if kind, ok := kinds[it.Kind]; ok && it.State == "new" {
				if err := rec(kind, it.Key, it.Unverified); err != nil {
					return nil, err
				}
			}
		}
		n, present := countItems(items)
		if err := avvioAudit(ctx, tx, r, p, "avvio.model.apply", map[string]any{"model": m.Key, "new": n, "present": present}); err != nil {
			return nil, err
		}
		return &avvioPreview{Key: m.Key, Available: true, Items: items, New: n, Present: present, Applied: true}, nil
	})
}
