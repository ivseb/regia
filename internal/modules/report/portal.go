package report

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/demand"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Portale del richiedente (demand.md §14 D01, §16 decisione 7). Il richiedente ha
// portal.submit e vede solo le proprie richieste (engine.CanRead: segnalatore o
// stakeholder). Il portale è configurabile per tenant: benvenuto, tipi esposti con i
// campi mostrati, spiegazione delle fasi in parole semplici.

// StandardFields sono i dati della domanda che il portale può chiedere al richiedente.
var StandardFields = []struct{ Key, Label string }{
	{"requesting_unit_id", "Unità richiedente"},
	{"driver", "Motivazione principale"},
	{"is_mandatory", "Obbligo normativo"},
	{"regulatory_deadline", "Scadenza normativa"},
	{"desired_date", "Data desiderata"},
}

func standardKnown(k string) bool {
	for _, f := range StandardFields {
		if f.Key == k {
			return true
		}
	}
	return false
}

// PortalType è un tipo di richiesta esposto nel portale.
type PortalType struct {
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Fields      []string `json:"fields"`   // campi personalizzati mostrati, nell'ordine
	Standard    []string `json:"standard"` // dati della domanda mostrati
}

type PortalSettings struct {
	Welcome    string            `json:"welcome"`
	Types      []PortalType      `json:"types"`
	PhaseTexts map[string]string `json:"phase_texts"`
	// Solo in lettura: tipi che si possono esporre, campi standard, fasi del processo.
	Available []AvailableType `json:"available,omitempty"`
	Standard  []NamedField    `json:"standard_fields,omitempty"`
	Phases    []PhaseText     `json:"phases,omitempty"`
	UpdatedAt *time.Time      `json:"updated_at"`
	CanEdit   bool            `json:"can_edit"`
}

type AvailableType struct {
	Key    string       `json:"key"`
	Name   string       `json:"name"`
	Fields []NamedField `json:"fields"`
}

type NamedField struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type PhaseText struct {
	Name        string `json:"name"`
	Description string `json:"description"` // del processo
	Default     string `json:"default"`     // testo semplice di prodotto
}

// defaultPhaseTexts spiegano le fasi del processo standard in parole semplici.
var defaultPhaseTexts = map[string]string{
	"Bozza":            "La richiesta è ancora una bozza: puoi completarla e inviarla quando è pronta.",
	"Inviata":          "L'abbiamo ricevuta. Il demand management la prenderà in carico a breve.",
	"Triage":           "Stiamo controllando che sia completa e che non ci siano già richieste simili.",
	"Valutazione":      "Stiamo stimando costi, benefici e rischi. Potremmo chiederti qualche chiarimento.",
	"Prioritizzazione": "La stiamo confrontando con le altre richieste per decidere le priorità.",
	"Approvazione":     "È in attesa della decisione dello sponsor e degli organi previsti.",
	"Pianificazione":   "È approvata: stiamo organizzando team, budget e date.",
	"In esecuzione":    "I team ci stanno lavorando.",
	"Chiusa":           "È stata completata e chiusa.",
	"Sospesa":          "È ferma in attesa di una decisione o di una risorsa. Ti avviseremo quando riparte.",
	"Respinta":         "Non è stata approvata: trovi la motivazione nei commenti.",
	"Ritirata":         "È stata ritirata.",
}

func canPortal(a engine.Actor) bool { return a.Has(rbac.PortalSubmit) || a.Has(rbac.ElementWrite) }

func canConfigurePortal(a engine.Actor) bool {
	return a.Has(rbac.TenantAdmin) || a.Has(rbac.PortfolioAdmin)
}

// typeFieldKeys sono i campi personalizzati del tipo, nell'ordine.
func (s *Service) typeFieldKeys(ctx context.Context, tx pgx.Tx, typeKey string) ([]NamedField, error) {
	views, err := s.eng.TypeFields(ctx, tx, typeKey)
	if err != nil {
		return nil, err
	}
	out := []NamedField{}
	for _, v := range views {
		if !v.Archived {
			out = append(out, NamedField{Key: v.Key, Name: v.Name})
		}
	}
	return out, nil
}

// loadPortalSettings legge la configurazione; senza riga, i tipi con portal_enabled
// e tutti i loro campi.
func (s *Service) loadPortalSettings(ctx context.Context, tx pgx.Tx) (*PortalSettings, error) {
	ps := &PortalSettings{PhaseTexts: map[string]string{}, Types: []PortalType{}}
	var types, texts []byte
	var at time.Time
	err := tx.QueryRow(ctx, `select welcome, types, phase_texts, updated_at from portal_settings`).Scan(&ps.Welcome, &types, &texts, &at)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		ps.UpdatedAt = &at
		if err := json.Unmarshal(types, &ps.Types); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(texts, &ps.PhaseTexts); err != nil {
			return nil, err
		}
	}
	if ps.UpdatedAt == nil || len(ps.Types) == 0 {
		rows, err := tx.Query(ctx, `select key, name from element_types where portal_enabled order by name`)
		if err != nil {
			return nil, err
		}
		var k, n string
		var keys [][2]string
		if _, err := pgx.ForEachRow(rows, []any{&k, &n}, func() error { keys = append(keys, [2]string{k, n}); return nil }); err != nil {
			return nil, err
		}
		if ps.UpdatedAt == nil {
			for _, kn := range keys {
				fields, err := s.typeFieldKeys(ctx, tx, kn[0])
				if err != nil {
					return nil, err
				}
				pt := PortalType{Type: kn[0], Label: kn[1], Fields: []string{}, Standard: []string{}}
				for _, f := range fields {
					pt.Fields = append(pt.Fields, f.Key)
				}
				if kn[0] == requestType {
					pt.Label = "Nuova richiesta"
					pt.Description = "Un bisogno dell'unità: un nuovo servizio, una modifica, un adeguamento normativo."
					for _, f := range StandardFields {
						pt.Standard = append(pt.Standard, f.Key)
					}
				}
				ps.Types = append(ps.Types, pt)
			}
		}
	}
	return ps, nil
}

// PortalConfig: la configurazione per l'amministratore, con le scelte possibili.
func (s *Service) PortalConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*PortalSettings, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	ps, err := s.loadPortalSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	ps.CanEdit = true
	rows, err := tx.Query(ctx, `select t.key, t.name from element_types t
		where t.module in ('strategy', 'core', 'grc') and exists (select 1 from space_types st where st.type_id = t.id)
		order by t.name`)
	if err != nil {
		return nil, err
	}
	var k, n string
	var types [][2]string
	if _, err := pgx.ForEachRow(rows, []any{&k, &n}, func() error { types = append(types, [2]string{k, n}); return nil }); err != nil {
		return nil, err
	}
	ps.Available = []AvailableType{}
	for _, kn := range types {
		fields, err := s.typeFieldKeys(ctx, tx, kn[0])
		if err != nil {
			return nil, err
		}
		ps.Available = append(ps.Available, AvailableType{Key: kn[0], Name: kn[1], Fields: fields})
	}
	ps.Standard = []NamedField{}
	for _, f := range StandardFields {
		ps.Standard = append(ps.Standard, NamedField{Key: f.Key, Name: f.Label})
	}
	ps.Phases = []PhaseText{}
	if t, err := engine.LoadType(ctx, tx, requestType); err == nil {
		desc, err := phaseDescriptions(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		for _, st := range t.States {
			ps.Phases = append(ps.Phases, PhaseText{Name: st.Name, Description: desc[st.Name], Default: defaultPhaseTexts[st.Name]})
		}
	}
	return ps, nil
}

// phaseDescriptions: descrizioni delle fasi nella versione pubblicata del processo del tipo.
func phaseDescriptions(ctx context.Context, tx pgx.Tx, t *engine.TypeInfo) (map[string]string, error) {
	out := map[string]string{}
	if t.Process == nil {
		return out, nil
	}
	return versionDescriptions(ctx, tx, t.Process.VersionID)
}

func versionDescriptions(ctx context.Context, tx pgx.Tx, versionID string) (map[string]string, error) {
	out := map[string]string{}
	var raw []byte
	err := tx.QueryRow(ctx, `select definition from process_versions where id = $1`, versionID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var def struct {
		Phases []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"phases"`
	}
	_ = json.Unmarshal(raw, &def)
	for _, p := range def.Phases {
		out[p.Name] = p.Description
	}
	return out, nil
}

type PortalSettingsInput struct {
	Welcome    *string            `json:"welcome"`
	Types      *[]PortalType      `json:"types"`
	PhaseTexts *map[string]string `json:"phase_texts"`
}

// SavePortalConfig salva la configurazione; i tipi esposti diventano portal_enabled.
func (s *Service) SavePortalConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor, in PortalSettingsInput) (*PortalSettings, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	cur, err := s.loadPortalSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	if in.Welcome != nil {
		if len(*in.Welcome) > 4000 {
			return nil, engine.TooLong("welcome", 4000, "testo troppo lungo (massimo 4.000 caratteri)")
		}
		cur.Welcome = strings.TrimSpace(*in.Welcome)
	}
	if in.PhaseTexts != nil {
		clean := map[string]string{}
		for k, v := range *in.PhaseTexts {
			if v = strings.TrimSpace(v); v != "" {
				if len(v) > 500 {
					return nil, engine.TooLong("phase_texts", 500, "fase «%s»: testo troppo lungo (massimo 500 caratteri)", k)
				}
				clean[k] = v
			}
		}
		cur.PhaseTexts = clean
	}
	if in.Types != nil {
		out := []PortalType{}
		seen := map[string]bool{}
		for _, t := range *in.Types {
			if seen[t.Type] {
				return nil, engine.InvalidParams("validation.report.portal_type_repeated", "types", map[string]any{"type": t.Type}, "tipo «%s» ripetuto", t.Type)
			}
			seen[t.Type] = true
			info, err := engine.LoadType(ctx, tx, t.Type)
			if errors.Is(err, engine.ErrNotFound) {
				return nil, engine.NotFound("types", "", "tipo «%s» inesistente", t.Type)
			}
			if err != nil {
				return nil, err
			}
			fields, err := s.typeFieldKeys(ctx, tx, t.Type)
			if err != nil {
				return nil, err
			}
			known := map[string]bool{}
			for _, f := range fields {
				known[f.Key] = true
			}
			t.Label, t.Description = strings.TrimSpace(t.Label), strings.TrimSpace(t.Description)
			if t.Label == "" {
				t.Label = info.Name
			}
			if len(t.Label) > 80 || len(t.Description) > 400 {
				return nil, engine.InvalidParams("validation.report.portal_type_text_limits", "types", map[string]any{"type": t.Type}, "tipo «%s»: nome fino a 80 caratteri, descrizione fino a 400", t.Type)
			}
			if t.Fields == nil {
				t.Fields = []string{}
			}
			for _, f := range t.Fields {
				if !known[f] {
					return nil, engine.InvalidParams("validation.report.portal_field_not_of_type", "types", map[string]any{"field": f, "type": t.Type}, "il campo «%s» non è del tipo «%s»", f, t.Type)
				}
			}
			if t.Standard == nil {
				t.Standard = []string{}
			}
			for _, f := range t.Standard {
				if !standardKnown(f) || t.Type != requestType {
					return nil, engine.InvalidParams("validation.report.portal_data_unavailable", "types", map[string]any{"field": f, "type": t.Type}, "dato «%s» non disponibile per il tipo «%s»", f, t.Type)
				}
			}
			out = append(out, t)
		}
		cur.Types = out
		keys := make([]string, 0, len(out))
		for _, t := range out {
			keys = append(keys, t.Type)
		}
		if entries, _, err := s.storedEntries(ctx, tx); err == nil {
			for _, e := range entries {
				keys = append(keys, e.Type)
			}
		}
		if _, err := tx.Exec(ctx, `update element_types set portal_enabled = (key = any($1)), updated_at = now()
			where portal_enabled <> (key = any($1))`, keys); err != nil {
			return nil, err
		}
	}
	types, _ := json.Marshal(cur.Types)
	texts, _ := json.Marshal(cur.PhaseTexts)
	if _, err := tx.Exec(ctx, `insert into portal_settings (tenant_id, welcome, types, phase_texts, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, now())
		on conflict (tenant_id) do update set welcome = excluded.welcome, types = excluded.types, phase_texts = excluded.phase_texts,
			updated_by = excluded.updated_by, updated_at = now()`, cur.Welcome, types, texts, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "portal.settings.update", EntityType: "portal_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"types": len(cur.Types)}}); err != nil {
		return nil, err
	}
	return s.PortalConfig(ctx, tx, actor)
}

// ---------------------------------------------------------------------------
// Vista del richiedente

// PortalTypeView è un tipo esposto con i campi da compilare.
type PortalTypeView struct {
	Type        string             `json:"type"`
	Label       string             `json:"label"`
	Description string             `json:"description"`
	Fields      []engine.FieldView `json:"fields"`
	Standard    []NamedField       `json:"standard"`
}

type Draft struct {
	ID        string         `json:"id"`
	Entry     string         `json:"entry"` // voce di catalogo (senza catalogo, la chiave del tipo)
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Data      map[string]any `json:"data"`
	Step      int            `json:"step"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// MyRequest è una richiesta nell'elenco «Le mie richieste».
type MyRequest struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	TypeName  string    `json:"type_name"`
	Phase     string    `json:"phase"`
	Category  string    `json:"category"`
	Text      string    `json:"text"` // la fase in parole semplici
	Step      int       `json:"step"` // posizione nel percorso principale (1…), 0 fuori percorso
	Steps     int       `json:"steps"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Portal struct {
	Welcome string           `json:"welcome"`
	Types   []PortalTypeView `json:"types"`
	// Entries: le voci del catalogo che la persona può usare, ciascuna con il suo modulo.
	Entries []EntryView  `json:"entries"`
	Units   []NamedField `json:"units"`
	// MyUnit è l'unità di cui la persona fa parte (org_unit_members): il portale la propone già scelta.
	MyUnit   string      `json:"my_unit"`
	Drafts   []Draft     `json:"drafts"`
	Requests []MyRequest `json:"requests"`
}

// Portal è la pagina del richiedente: benvenuto, tipi, bozze e richieste.
func (s *Service) Portal(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Portal, error) {
	if !canPortal(actor) {
		return nil, engine.ErrForbidden
	}
	ps, entries, err := s.loadPortal(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &Portal{Welcome: ps.Welcome, Types: []PortalTypeView{}, Entries: []EntryView{}, Units: []NamedField{}, Drafts: []Draft{}}
	for _, e := range entries {
		if !e.Active {
			continue
		}
		if e.GroupID != "" {
			var in bool
			if err := tx.QueryRow(ctx, `select $2::uuid in (select member_groups($1))`, actor.MemberID, e.GroupID).Scan(&in); err != nil {
				return nil, err
			}
			if !in {
				continue
			}
		}
		v, err := s.entryView(ctx, tx, e, e.Form)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, *v)
	}
	for _, t := range ps.Types {
		v, err := s.typeView(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out.Types = append(out.Types, *v)
		}
	}
	if out.Units, out.MyUnit, err = s.unitsFor(ctx, tx, actor); err != nil {
		return nil, err
	}
	if out.Drafts, err = s.drafts(ctx, tx, actor); err != nil {
		return nil, err
	}
	out.Requests, err = s.myRequests(ctx, tx, actor, ps)
	return out, err
}

func (s *Service) typeView(ctx context.Context, tx pgx.Tx, t PortalType) (*PortalTypeView, error) {
	views, err := s.eng.TypeFields(ctx, tx, t.Type)
	if errors.Is(err, engine.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := &PortalTypeView{Type: t.Type, Label: t.Label, Description: t.Description, Fields: []engine.FieldView{}, Standard: []NamedField{}}
	for _, key := range t.Fields {
		for _, f := range views {
			if f.Key == key && !f.Archived {
				v.Fields = append(v.Fields, f)
			}
		}
	}
	for _, f := range StandardFields {
		if slices.Contains(t.Standard, f.Key) {
			v.Standard = append(v.Standard, NamedField{Key: f.Key, Name: f.Label})
		}
	}
	return v, nil
}

// mineSQL: le richieste di cui il membro $1 è segnalatore o richiedente.
const mineSQL = `(e.reporter_id = $1 or exists (select 1 from stakeholders sh
	join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'requester'
	where sh.element_id = e.id and sh.member_id = $1))`

func (s *Service) myRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor, ps *PortalSettings) ([]MyRequest, error) {
	rows, err := tx.Query(ctx, `
		select e.id, coalesce(sp.key || '-' || e.number, ''), e.title, t.name, t.key, coalesce(o.name, ''), coalesce(o.category, ''),
		       (select count(*) from comments c where c.element_id = e.id and c.deleted_at is null), e.created_at, e.updated_at
		from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.portal_enabled
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft and `+mineSQL+`
		order by e.updated_at desc limit 200`, actor.MemberID)
	if err != nil {
		return nil, err
	}
	out := []MyRequest{}
	typeKeys := map[string]string{}
	var r MyRequest
	var tk string
	if _, err := pgx.ForEachRow(rows, []any{&r.ID, &r.Key, &r.Title, &r.TypeName, &tk, &r.Phase, &r.Category, &r.Comments, &r.CreatedAt, &r.UpdatedAt}, func() error {
		out = append(out, r)
		typeKeys[r.ID] = tk
		return nil
	}); err != nil {
		return nil, err
	}
	paths := map[string][]string{}
	for i := range out {
		tk := typeKeys[out[i].ID]
		if _, ok := paths[tk]; !ok {
			paths[tk] = nil
			if t, err := engine.LoadType(ctx, tx, tk); err == nil {
				for _, st := range t.States {
					if !st.OffPath {
						paths[tk] = append(paths[tk], st.Name)
					}
				}
			}
		}
		out[i].Steps = len(paths[tk])
		out[i].Step = slices.Index(paths[tk], out[i].Phase) + 1
		out[i].Text = plainText(ps, out[i].Phase, "")
	}
	return out, nil
}

func plainText(ps *PortalSettings, phase, processDesc string) string {
	if t := ps.PhaseTexts[phase]; t != "" {
		return t
	}
	if t := defaultPhaseTexts[phase]; t != "" {
		return t
	}
	return processDesc
}

// PortalRequest è la richiesta vista dal richiedente.
type PortalRequest struct {
	ID          string       `json:"id"`
	Key         string       `json:"key"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	TypeName    string       `json:"type_name"`
	Phase       string       `json:"phase"`
	Category    string       `json:"category"`
	Text        string       `json:"text"`
	Path        []PathStep   `json:"path"`
	NextStep    string       `json:"next_step"`
	FollowedBy  []Person     `json:"followed_by"`
	Data        []LabelValue `json:"data"`
	Actions     []Action     `json:"actions"` // passaggi che il richiedente può fare (invia, ritira)
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type PathStep struct {
	Name  string `json:"name"`
	State string `json:"state"` // done | current | todo
}

type Person struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type LabelValue struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Action struct {
	To              string `json:"to"`
	Label           string `json:"label"`
	CommentRequired bool   `json:"comment_required"`
	Blocked         string `json:"blocked,omitempty"`
}

// PortalRequest: solo le richieste del membro (segnalatore o richiedente).
func (s *Service) PortalRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*PortalRequest, error) {
	if !canPortal(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var mine bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where e.id = $2 and t.portal_enabled and `+mineSQL+`)`, actor.MemberID, el.ID).Scan(&mine); err != nil {
		return nil, err
	}
	if !mine {
		return nil, engine.ErrNotFound
	}
	ps, _, err := s.loadPortal(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &PortalRequest{ID: el.ID, Key: el.Key, Title: el.Title, Description: el.BodyText, TypeName: el.TypeName,
		Phase: el.State, Category: el.StateCategory, CreatedAt: el.CreatedAt, UpdatedAt: el.UpdatedAt,
		Path: []PathStep{}, FollowedBy: []Person{}, Data: []LabelValue{}, Actions: []Action{}}
	phases, _, err := s.eng.Phases(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	desc := map[string]string{}
	if el.ProcessVersionID != nil {
		if desc, err = versionDescriptions(ctx, tx, *el.ProcessVersionID); err != nil {
			return nil, err
		}
	}
	out.Text = plainText(ps, el.State, desc[el.State])
	var main []string
	for _, p := range phases {
		if !p.OffPath {
			main = append(main, p.Name)
		}
	}
	cur := slices.Index(main, el.State)
	for i, name := range main {
		st := "todo"
		switch {
		case cur >= 0 && i < cur:
			st = "done"
		case i == cur:
			st = "current"
		}
		out.Path = append(out.Path, PathStep{Name: name, State: st})
	}
	switch {
	case el.StateCategory == "done" || el.StateCategory == "cancelled":
		out.NextStep = "Nessuno: la richiesta è conclusa."
	case cur == 0 && el.StateCategory == "todo" && len(main) > 1:
		out.NextStep = "Quando è completa, inviala: passerà a «" + main[1] + "»."
	case cur >= 0 && cur+1 < len(main):
		nx := main[cur+1]
		out.NextStep = "«" + nx + "»"
		if t := plainText(ps, nx, desc[nx]); t != "" {
			out.NextStep += ": " + strings.TrimSuffix(lowerFirst(t), ".") + "."
		}
	case cur < 0:
		out.NextStep = "Ti avviseremo quando la richiesta riprende."
	}
	// Chi la segue: demand manager, project manager, PMO (persone o gruppi).
	rows, err := tx.Query(ctx, `select r.name, coalesce(m.display_name, g.name, '')
		from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		where sh.element_id = $1 and r.key in ('demand_manager', 'project_manager', 'pmo', 'business_owner', 'sponsor')
		order by array_position(array['project_manager', 'demand_manager', 'pmo', 'sponsor', 'business_owner'], r.key), 2`, el.ID)
	if err != nil {
		return nil, err
	}
	var p Person
	if _, err := pgx.ForEachRow(rows, []any{&p.Role, &p.Name}, func() error {
		if p.Name != "" {
			out.FollowedBy = append(out.FollowedBy, p)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// I dati inseriti dal richiedente (solo i campi che il portale gli mostra).
	var pt *PortalType
	for i := range ps.Types {
		if ps.Types[i].Type == el.TypeKey {
			pt = &ps.Types[i]
		}
	}
	if pt != nil {
		if el.TypeKey == requestType {
			if err := s.standardValues(ctx, tx, el.ID, pt.Standard, &out.Data); err != nil {
				return nil, err
			}
		}
		views, err := s.eng.FieldViews(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		for _, key := range pt.Fields {
			for _, f := range views {
				if f.Key == key && f.Visible && f.Value != nil {
					out.Data = append(out.Data, LabelValue{Label: f.Name, Value: fieldText(f)})
				}
			}
		}
	}
	// Passaggi che il richiedente può fare da qui (inviare la bozza, ritirarla).
	trs, err := s.eng.Available(ctx, tx, actor, el)
	if err != nil {
		return nil, err
	}
	for _, t := range trs {
		a := Action{To: t.To, Label: t.Label, CommentRequired: t.CommentRequired}
		if len(t.Blocked) > 0 {
			a.Blocked = strings.Join(t.Blocked, "; ")
		}
		for _, g := range t.Gates {
			if !g.Passed && a.Blocked == "" {
				a.Blocked = g.Message
			}
		}
		out.Actions = append(out.Actions, a)
	}
	return out, nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > 1 && r[1] >= 'A' && r[1] <= 'Z' { // sigle (DORA…)
		return s
	}
	return strings.ToLower(string(r[0])) + string(r[1:])
}

func (s *Service) standardValues(ctx context.Context, tx pgx.Tx, elementID string, keys []string, out *[]LabelValue) error {
	var unit, driver *string
	var mandatory bool
	var deadline, desired *time.Time
	err := tx.QueryRow(ctx, `select u.name, d.driver, d.is_mandatory, d.regulatory_deadline, d.desired_date
		from ext_demand d left join org_units u on u.tenant_id = d.tenant_id and u.id = d.requesting_unit_id
		where d.element_id = $1`, elementID).Scan(&unit, &driver, &mandatory, &deadline, &desired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, f := range StandardFields {
		if !slices.Contains(keys, f.Key) {
			continue
		}
		v := ""
		switch f.Key {
		case "requesting_unit_id":
			if unit != nil {
				v = *unit
			}
		case "driver":
			if driver != nil {
				v = driverLabel[*driver]
			}
		case "is_mandatory":
			if mandatory {
				v = "Sì"
			}
		case "regulatory_deadline":
			if deadline != nil {
				v = it(*deadline)
			}
		case "desired_date":
			if desired != nil {
				v = it(*desired)
			}
		}
		if v != "" {
			*out = append(*out, LabelValue{Label: f.Label, Value: v})
		}
	}
	return nil
}

// fieldText rende leggibile il valore di un campo personalizzato.
func fieldText(f engine.FieldView) string {
	label := func(v any) string {
		s, _ := v.(string)
		for _, o := range f.Options {
			if o.ID == s || o.Name == s {
				return o.Name
			}
		}
		if l, ok := f.Labels[s]; ok {
			return l.Label
		}
		switch x := v.(type) {
		case bool:
			if x {
				return "Sì"
			}
			return "No"
		case float64:
			b, _ := json.Marshal(x)
			return string(b)
		case string:
			if t, err := time.Parse(time.DateOnly, x); err == nil {
				return it(t)
			}
			return x
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	if list, ok := f.Value.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, label(v))
		}
		return strings.Join(parts, ", ")
	}
	return label(f.Value)
}

// ---------------------------------------------------------------------------
// Bozze del modulo guidato

func (s *Service) drafts(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Draft, error) {
	rows, err := tx.Query(ctx, `select id, entry_key, type_key, title, data, step, created_at, updated_at from portal_drafts
		where member_id = $1 order by updated_at desc limit 50`, actor.MemberID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanDraft)
}

func scanDraft(r pgx.CollectableRow) (Draft, error) {
	var d Draft
	var raw []byte
	err := r.Scan(&d.ID, &d.Entry, &d.Type, &d.Title, &raw, &d.Step, &d.CreatedAt, &d.UpdatedAt)
	d.Data = map[string]any{}
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &d.Data)
	}
	return d, err
}

type DraftInput struct {
	Entry string         `json:"entry"`
	Type  string         `json:"type"`
	Title string         `json:"title"`
	Data  map[string]any `json:"data"`
	Step  int            `json:"step"`
}

const maxDraftBytes = 64 << 10

// SaveDraft crea (id vuoto) o aggiorna una bozza del membro.
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in DraftInput) (*Draft, error) {
	if !canPortal(actor) {
		return nil, engine.ErrForbidden
	}
	if in.Entry == "" {
		in.Entry = in.Type
	}
	entry, _, err := s.entryFor(ctx, tx, actor, in.Entry)
	if err != nil {
		return nil, err
	}
	if entry == nil || (in.Type != "" && in.Type != entry.Type) {
		return nil, engine.InvalidCode("validation.report.portal_type_unavailable", "type", "tipo di richiesta non disponibile nel portale")
	}
	in.Type = entry.Type
	if len(in.Title) > 500 {
		return nil, engine.TooLong("title", 500, "titolo troppo lungo (massimo 500 caratteri)")
	}
	if in.Step < 0 || in.Step > 20 {
		in.Step = 0
	}
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	raw, _ := json.Marshal(in.Data)
	if len(raw) > maxDraftBytes {
		return nil, engine.InvalidCode("validation.report.portal_draft_too_big", "data", "bozza troppo grande")
	}
	// Una sola bozza per persona e voce di catalogo: chi ricomincia riprende quella che ha già, non ne apre un'altra.
	if id == "" {
		err := tx.QueryRow(ctx, `select id::text from portal_drafts where member_id = $1 and entry_key = $2 order by updated_at desc limit 1`,
			actor.MemberID, in.Entry).Scan(&id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	if id == "" {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from portal_drafts where member_id = $1`, actor.MemberID).Scan(&n); err != nil {
			return nil, err
		}
		if n >= 50 {
			return nil, engine.InvalidCode("validation.report.portal_drafts_limit", "drafts", "hai già 50 bozze: inviane o eliminane qualcuna")
		}
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into portal_drafts (tenant_id, id, member_id, entry_key, type_key, title, data, step) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`,
			id, actor.MemberID, in.Entry, in.Type, strings.TrimSpace(in.Title), raw, in.Step); err != nil {
			return nil, err
		}
	} else {
		if !uuidOK(id) {
			return nil, engine.ErrNotFound
		}
		tag, err := tx.Exec(ctx, `update portal_drafts set entry_key = $3, type_key = $4, title = $5, data = $6, step = $7, updated_at = now()
			where id = $1 and member_id = $2`, id, actor.MemberID, in.Entry, in.Type, strings.TrimSpace(in.Title), raw, in.Step)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, engine.ErrNotFound
		}
	}
	rows, err := tx.Query(ctx, `select id, entry_key, type_key, title, data, step, created_at, updated_at from portal_drafts where id = $1`, id)
	if err != nil {
		return nil, err
	}
	d, err := pgx.CollectExactlyOneRow(rows, scanDraft)
	return &d, err
}

// DeleteDraft elimina una bozza del membro.
func (s *Service) DeleteDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if !canPortal(actor) {
		return engine.ErrForbidden
	}
	if !uuidOK(id) {
		return engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `delete from portal_drafts where id = $1 and member_id = $2`, id, actor.MemberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return nil
}

// SubmitDraft trasforma la bozza in richiesta e la invia (primo passaggio disponibile
// verso una fase non annullata). Se un gate lo impedisce, nulla si salva e la bozza resta.
func (s *Service) SubmitDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, dm *demand.Service, id string) (*engine.Element, error) {
	if !canPortal(actor) {
		return nil, engine.ErrForbidden
	}
	if !uuidOK(id) {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, `select id, entry_key, type_key, title, data, step, created_at, updated_at from portal_drafts
		where id = $1 and member_id = $2 for update`, id, actor.MemberID)
	if err != nil {
		return nil, err
	}
	d, err := pgx.CollectExactlyOneRow(rows, scanDraft)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	entry, _, err := s.entryFor(ctx, tx, actor, d.Entry)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Type != d.Type {
		return nil, engine.InvalidCode("validation.report.portal_type_gone", "type", "tipo di richiesta non più disponibile nel portale")
	}
	if strings.TrimSpace(d.Title) == "" {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	desc, _ := d.Data["description"].(string)
	pt := entry.portalType()
	fields, err := s.checkEntryData(ctx, tx, entry, d)
	if err != nil {
		return nil, err
	}
	var el *engine.Element
	if d.Type == requestType {
		if dm == nil {
			return nil, errors.New("modulo demand non disponibile")
		}
		in := demand.NewRequest{Title: d.Title, Description: desc, Fields: fields}
		str := func(k string) *string {
			if !slices.Contains(pt.Standard, k) {
				return nil
			}
			if v, ok := d.Data[k].(string); ok && strings.TrimSpace(v) != "" {
				v = strings.TrimSpace(v)
				return &v
			}
			return nil
		}
		in.RequestingUnitID = str("requesting_unit_id")
		in.Driver = str("driver")
		in.RegulatoryDeadline = str("regulatory_deadline")
		in.DesiredDate = str("desired_date")
		if slices.Contains(pt.Standard, "is_mandatory") {
			if b, ok := d.Data["is_mandatory"].(bool); ok && b {
				in.IsMandatory = &b
			}
		}
		if el, err = dm.Create(ctx, tx, actor, in); err != nil {
			return nil, err
		}
	} else {
		var space string
		err := tx.QueryRow(ctx, `select s.key from spaces s join space_types st on st.tenant_id = s.tenant_id and st.space_id = s.id
			join element_types t on t.tenant_id = st.tenant_id and t.id = st.type_id
			where t.key = $1 and not s.archived order by s.created_at limit 1`, d.Type).Scan(&space)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidParams("validation.report.portal_type_no_space", "type", map[string]any{"type": d.Type}, "nessuno spazio accoglie il tipo «%s»", d.Type)
		}
		if err != nil {
			return nil, err
		}
		if el, err = s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: space, TypeKey: d.Type, Title: d.Title, BodyText: desc, Fields: fields}); err != nil {
			return nil, err
		}
	}
	// Invio: il primo passaggio disponibile verso una fase non annullata.
	if el.StateCategory == "todo" {
		trs, err := s.eng.Available(ctx, tx, actor, el)
		if err != nil {
			return nil, err
		}
		phases, _, err := s.eng.Phases(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		cat := map[string]string{}
		for _, p := range phases {
			cat[p.Name] = p.Category
		}
		for _, t := range trs {
			if cat[t.To] == "cancelled" || t.CommentRequired {
				continue
			}
			if el, err = s.eng.Transition(ctx, tx, actor, el.ID, t.To, "", engine.TransitionOpts{}); err != nil {
				return nil, err
			}
			break
		}
	}
	if _, err := tx.Exec(ctx, `delete from portal_drafts where id = $1`, id); err != nil {
		return nil, err
	}
	if err := s.route(ctx, tx, actor, el, entry.Form.Routes); err != nil {
		return nil, err
	}
	return el, audit.Append(ctx, tx, audit.Event{Action: "portal.submit", EntityType: d.Type, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"entry": entry.Key}})
}

// SimilarRequest è una richiesta già aperta che somiglia a quella che si sta scrivendo.
type SimilarRequest struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Phase string `json:"phase"`
	Mine  bool   `json:"mine"`
}

// Similar cerca, tra le richieste aperte che la persona può leggere, quelle con parole in comune con il titolo
// (almeno due, o l'unica se il titolo è di una parola): serve al riquadro «Richieste simili già aperte» mentre si
// scrive, per aggiungere un commento invece di duplicare.
func (s *Service) Similar(ctx context.Context, tx pgx.Tx, actor engine.Actor, text string) ([]SimilarRequest, error) {
	if !canPortal(actor) {
		return nil, engine.ErrForbidden
	}
	seen := map[string]bool{}
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 && !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return []SimilarRequest{}, nil
	}
	if len(words) > 8 {
		words = words[:8]
	}
	need := min(2, len(words))
	rows, err := tx.Query(ctx, `
		select coalesce(sp.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), `+mineSQL+`
		from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.portal_enabled
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft and coalesce(o.category, '') not in ('done', 'cancelled')
		  and (`+mineSQL+` or $4::boolean)
		  and (select count(*) from unnest($2::text[]) w where position(w in lower(e.title)) > 0) >= $3
		order by (select count(*) from unnest($2::text[]) w where position(w in lower(e.title)) > 0) desc, e.updated_at desc
		limit 5`, actor.MemberID, words, need, actor.Has(rbac.ElementRead))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[SimilarRequest])
}

// unitsFor: le unità richiedenti e quella della persona. La radice dell'albero (l'azienda intera) non è un'unità
// richiedente: si esclude se ha unità sotto.
func (s *Service) unitsFor(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]NamedField, string, error) {
	rows, err := tx.Query(ctx, `select u.id::text, u.name from org_units u
		where not u.archived and u.kind <> 'cost_center'
		  and not (u.parent_id is null and exists (select 1 from org_units c where c.parent_id = u.id and not c.archived))
		order by u.name`)
	if err != nil {
		return nil, "", err
	}
	units, err := pgx.CollectRows(rows, pgx.RowToStructByPos[NamedField])
	if err != nil {
		return nil, "", err
	}
	if units == nil {
		units = []NamedField{}
	}
	mine := ""
	if actor.MemberID != "" {
		err = tx.QueryRow(ctx, `select m.org_unit_id::text from org_unit_members m
			join org_units u on u.tenant_id = m.tenant_id and u.id = m.org_unit_id and not u.archived and u.kind <> 'cost_center'
			where m.member_id = $1 and not (u.parent_id is null and exists (select 1 from org_units c where c.parent_id = u.id and not c.archived))
			order by (m.role = 'manager') desc, m.created_at limit 1`, actor.MemberID).Scan(&mine)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", err
		}
	}
	return units, mine, nil
}
