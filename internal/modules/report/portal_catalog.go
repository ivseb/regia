package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Catalogo del portale (replica/usabilita/lavoro.md §1). Ogni voce è una carta che il richiedente sceglie e
// crea un elemento di un tipo con il suo modulo: passi, campi scelti tra quelli del tipo (con etichetta, aiuto,
// obbligo e condizione di visibilità per voce) e regole di instradamento «se… allora assegna…».
// Senza voci salvate il catalogo è quello di prima: una voce per ogni tipo esposto, con il modulo di sempre.
// Il modulo si modifica in una bozza e si pubblica; nome, attivazione, gruppo e ordine valgono subito.

// FormField è un campo del modulo: uno del tipo (key) o un dato della domanda (std). Le parti vuote seguono il campo.
type FormField struct {
	Key         string `json:"key"`
	Std         bool   `json:"std,omitempty"`
	Label       string `json:"label,omitempty"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required,omitempty"`
	VisibleWhen string `json:"visible_when,omitempty"`
}

// FormStep è un passo del wizard. Preset «context» o «details»: il titolo vuoto prende quello di prodotto.
type FormStep struct {
	Preset string      `json:"preset,omitempty"`
	Title  string      `json:"title,omitempty"`
	Intro  string      `json:"intro,omitempty"`
	Fields []FormField `json:"fields"`
}

// Route: «se [when] allora assegna il ruolo a un team (il suo referente) o a un gruppo, con priorità ed etichetta».
type Route struct {
	When     string `json:"when,omitempty"`
	Role     string `json:"role,omitempty"`
	TeamID   string `json:"team_id,omitempty"`
	GroupID  string `json:"group_id,omitempty"`
	Priority string `json:"priority,omitempty"` // low | medium | high
	Label    string `json:"label,omitempty"`
}

type EntryForm struct {
	Intro      string     `json:"intro,omitempty"`
	TitleLabel string     `json:"title_label,omitempty"`
	DescLabel  string     `json:"desc_label,omitempty"`
	Steps      []FormStep `json:"steps"`
	Routes     []Route    `json:"routes"`
}

type Entry struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Icon        string     `json:"icon"`
	Type        string     `json:"type"`
	Position    int        `json:"position"`
	Active      bool       `json:"active"`
	GroupID     string     `json:"group_id"`
	Form        EntryForm  `json:"form"`
	Draft       *EntryForm `json:"draft"`
	PublishedAt *time.Time `json:"published_at"`
}

const maxSteps, maxRoutes = 8, 20

var priorityTags = map[string]string{"low": "Priorità bassa", "medium": "Priorità media", "high": "Priorità alta"}

func normalizeForm(f *EntryForm) {
	if f.Steps == nil {
		f.Steps = []FormStep{}
	}
	for i := range f.Steps {
		if f.Steps[i].Fields == nil {
			f.Steps[i].Fields = []FormField{}
		}
	}
	if f.Routes == nil {
		f.Routes = []Route{}
	}
}

// defaultForm è il modulo di sempre: «Tempi e contesto» con i dati della domanda e «Altri dettagli» con i campi.
func defaultForm(pt PortalType) EntryForm {
	f := EntryForm{Steps: []FormStep{}, Routes: []Route{}}
	if len(pt.Standard) > 0 {
		st := FormStep{Preset: "context", Fields: []FormField{}}
		for _, k := range pt.Standard {
			st.Fields = append(st.Fields, FormField{Key: k, Std: true})
		}
		f.Steps = append(f.Steps, st)
	}
	if len(pt.Fields) > 0 {
		st := FormStep{Preset: "details", Fields: []FormField{}}
		for _, k := range pt.Fields {
			st.Fields = append(st.Fields, FormField{Key: k})
		}
		f.Steps = append(f.Steps, st)
	}
	return f
}

// portalType: i campi e i dati della domanda che il modulo pubblicato chiede.
func (e *Entry) portalType() PortalType {
	pt := PortalType{Type: e.Type, Label: e.Label, Description: e.Description, Fields: []string{}, Standard: []string{}}
	for _, st := range e.Form.Steps {
		for _, f := range st.Fields {
			if f.Std {
				pt.Standard = append(pt.Standard, f.Key)
			} else {
				pt.Fields = append(pt.Fields, f.Key)
			}
		}
	}
	return pt
}

func (s *Service) storedEntries(ctx context.Context, tx pgx.Tx) ([]Entry, bool, error) {
	rows, err := tx.Query(ctx, `select id::text, key, label, description, category, icon, type_key, position, active,
		coalesce(group_id::text, ''), form, draft, published_at from portal_entries order by position, lower(label), key`)
	if err != nil {
		return nil, false, err
	}
	out := []Entry{}
	var e Entry
	var form, draft []byte
	if _, err := pgx.ForEachRow(rows, []any{&e.ID, &e.Key, &e.Label, &e.Description, &e.Category, &e.Icon, &e.Type, &e.Position, &e.Active,
		&e.GroupID, &form, &draft, &e.PublishedAt}, func() error {
		x := e
		x.Form, x.Draft = EntryForm{}, nil
		if len(form) > 0 {
			if err := json.Unmarshal(form, &x.Form); err != nil {
				return err
			}
		}
		normalizeForm(&x.Form)
		if len(draft) > 0 && string(draft) != "null" {
			d := EntryForm{}
			if err := json.Unmarshal(draft, &d); err != nil {
				return err
			}
			normalizeForm(&d)
			x.Draft = &d
		}
		out = append(out, x)
		return nil
	}); err != nil {
		return nil, false, err
	}
	return out, len(out) > 0, nil
}

// loadPortal: la configurazione del portale con i tipi delle voci e il catalogo (salvato, o quello di sempre).
func (s *Service) loadPortal(ctx context.Context, tx pgx.Tx) (*PortalSettings, []Entry, error) {
	ps, err := s.loadPortalSettings(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	entries, stored, err := s.storedEntries(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if !stored {
		for i, t := range ps.Types {
			entries = append(entries, Entry{Key: t.Type, Label: t.Label, Description: t.Description, Icon: "doc", Type: t.Type,
				Position: i, Active: true, Form: defaultForm(t)})
		}
		return ps, entries, nil
	}
	for i := range entries {
		e := &entries[i]
		pt := e.portalType()
		j := slices.IndexFunc(ps.Types, func(t PortalType) bool { return t.Type == e.Type })
		if j < 0 {
			ps.Types = append(ps.Types, pt)
			continue
		}
		for _, k := range pt.Fields {
			if !slices.Contains(ps.Types[j].Fields, k) {
				ps.Types[j].Fields = append(ps.Types[j].Fields, k)
			}
		}
		for _, k := range pt.Standard {
			if !slices.Contains(ps.Types[j].Standard, k) {
				ps.Types[j].Standard = append(ps.Types[j].Standard, k)
			}
		}
	}
	return ps, entries, nil
}

// entryFor trova la voce attiva e visibile alla persona (nil se non c'è).
func (s *Service) entryFor(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Entry, []Entry, error) {
	_, entries, err := s.loadPortal(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	for i := range entries {
		e := &entries[i]
		if e.Key != key || !e.Active {
			continue
		}
		if e.GroupID != "" {
			ok, err := s.inGroup(ctx, tx, actor, e.GroupID)
			if err != nil || !ok {
				return nil, entries, err
			}
		}
		return e, entries, nil
	}
	return nil, entries, nil
}

func (s *Service) inGroup(ctx context.Context, tx pgx.Tx, actor engine.Actor, groupID string) (bool, error) {
	var in bool
	err := tx.QueryRow(ctx, `select $2::uuid in (select member_groups($1))`, actor.MemberID, groupID).Scan(&in)
	return in, err
}

// ---------------------------------------------------------------------------
// Vista del richiedente

// StepField è un campo di un passo come lo vede il richiedente. Etichetta vuota per un dato della domanda: vale quella di prodotto.
type StepField struct {
	Key         string `json:"key"`
	Std         bool   `json:"std,omitempty"`
	Label       string `json:"label"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required,omitempty"`
	VisibleWhen string `json:"visible_when,omitempty"`
}

type StepView struct {
	Preset string      `json:"preset,omitempty"`
	Title  string      `json:"title,omitempty"`
	Intro  string      `json:"intro,omitempty"`
	Fields []StepField `json:"fields"`
}

// EntryView è una voce con il modulo risolto: i campi del tipo con etichetta, aiuto, obbligo e condizione della voce.
type EntryView struct {
	Key         string             `json:"key"`
	Label       string             `json:"label"`
	Description string             `json:"description"`
	Category    string             `json:"category"`
	Icon        string             `json:"icon"`
	Type        string             `json:"type"`
	Intro       string             `json:"intro,omitempty"`
	TitleLabel  string             `json:"title_label,omitempty"`
	DescLabel   string             `json:"desc_label,omitempty"`
	Steps       []StepView         `json:"steps"`
	Fields      []engine.FieldView `json:"fields"`
}

func (s *Service) entryView(ctx context.Context, tx pgx.Tx, e Entry, f EntryForm) (*EntryView, error) {
	v := &EntryView{Key: e.Key, Label: e.Label, Description: e.Description, Category: e.Category, Icon: e.Icon, Type: e.Type,
		Intro: f.Intro, TitleLabel: f.TitleLabel, DescLabel: f.DescLabel, Steps: []StepView{}, Fields: []engine.FieldView{}}
	views, err := s.eng.TypeFields(ctx, tx, e.Type)
	if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return nil, err
	}
	for _, st := range f.Steps {
		sv := StepView{Preset: st.Preset, Title: st.Title, Intro: st.Intro, Fields: []StepField{}}
		for _, ff := range st.Fields {
			sf := StepField{Key: ff.Key, Std: ff.Std, Label: ff.Label, Help: ff.Help, Required: ff.Required, VisibleWhen: ff.VisibleWhen}
			if !ff.Std {
				i := slices.IndexFunc(views, func(x engine.FieldView) bool { return x.Key == ff.Key && !x.Archived })
				if i < 0 {
					continue
				}
				fv := views[i]
				if ff.Label != "" {
					fv.Name = ff.Label
				}
				if ff.Help != "" {
					fv.Help = ff.Help
				}
				if ff.Required {
					fv.Required, fv.RequiredNow = true, true
				}
				if ff.VisibleWhen != "" {
					fv.VisibleWhen = ff.VisibleWhen
				}
				sf.Label = fv.Name
				sf.Required = fv.Required
				sf.VisibleWhen = fv.VisibleWhen
				v.Fields = append(v.Fields, fv)
			}
			sv.Fields = append(sv.Fields, sf)
		}
		v.Steps = append(v.Steps, sv)
	}
	return v, nil
}

// draftFacts: i fatti per le condizioni dei campi, come li costruisce il modulo (nomi delle opzioni, tipo, motivazione).
func draftFacts(views []engine.FieldView, d Draft) engine.Facts {
	f := engine.Facts{}
	f.Set("tipo", d.Type)
	if v, ok := d.Data["driver"].(string); ok && v != "" {
		f.Set("driver", v)
	}
	b, _ := d.Data["is_mandatory"].(bool)
	f.Set("obbligatoria", b)
	raw, _ := d.Data["fields"].(map[string]any)
	for _, fv := range views {
		val, ok := raw[fv.Key]
		if !ok || val == nil || val == "" {
			continue
		}
		list, isList := val.([]any)
		if !isList {
			list = []any{val}
		}
		for _, x := range list {
			if fv.DataType == "enum" || fv.DataType == "version" {
				name := fmt.Sprint(x)
				for _, o := range fv.Options {
					if o.ID == name {
						name = o.Name
					}
				}
				f.Set(fv.Key, name)
			} else {
				f.Set(fv.Key, x)
			}
		}
	}
	return f
}

func emptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// checkEntryData: i campi della bozza devono essere quelli del modulo; quelli nascosti dalla condizione si scartano
// e i visibili obbligatori vanno compilati. Restituisce i campi da salvare nell'elemento.
func (s *Service) checkEntryData(ctx context.Context, tx pgx.Tx, e *Entry, d Draft) (map[string]any, error) {
	views, err := s.eng.TypeFields(ctx, tx, e.Type)
	if err != nil && !errors.Is(err, engine.ErrNotFound) {
		return nil, err
	}
	facts := draftFacts(views, d)
	raw, _ := d.Data["fields"].(map[string]any)
	planned := map[string]FormField{}
	var order []FormField
	for _, st := range e.Form.Steps {
		for _, f := range st.Fields {
			if !f.Std {
				planned[f.Key] = f
			}
			order = append(order, f)
		}
	}
	visible := func(f FormField, base string) bool {
		cond := f.VisibleWhen
		if cond == "" {
			cond = base
		}
		if cond == "" {
			return true
		}
		c, err := engine.ParseCondition(cond)
		return err != nil || c.Eval(facts)
	}
	out := map[string]any{}
	for k, v := range raw {
		f, ok := planned[k]
		if !ok {
			return nil, engine.InvalidParams("validation.report.portal_field_not_planned", "fields", map[string]any{"field": k}, "il campo «%s» non è previsto nel portale", k)
		}
		base := ""
		if i := slices.IndexFunc(views, func(x engine.FieldView) bool { return x.Key == k }); i >= 0 {
			base = views[i].VisibleWhen
		}
		if !visible(f, base) {
			continue
		}
		if !emptyValue(v) {
			out[k] = v
		}
	}
	for _, f := range order {
		if !f.Required || !visible(f, "") {
			continue
		}
		var v any
		if f.Std {
			v = d.Data[f.Key]
			if b, ok := v.(bool); ok && !b {
				v = nil
			}
		} else {
			v = out[f.Key]
		}
		if emptyValue(v) {
			label := f.Label
			if label == "" {
				label = f.Key
				if i := slices.IndexFunc(views, func(x engine.FieldView) bool { return x.Key == f.Key }); i >= 0 {
					label = views[i].Name
				}
			}
			return nil, engine.InvalidParams("validation.portal.field_required", "fields", map[string]any{"field": label}, "il campo «%s» è obbligatorio", label)
		}
	}
	return out, nil
}

// route applica le regole della voce all'elemento appena creato: tutte quelle la cui condizione è vera.
func (s *Service) route(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, routes []Route) error {
	if len(routes) == 0 {
		return nil
	}
	facts, err := s.eng.ElementFacts(ctx, tx, el)
	if err != nil {
		return err
	}
	for _, r := range routes {
		c, err := engine.ParseCondition(r.When)
		if err != nil || !c.Eval(facts) {
			continue
		}
		note := []string{}
		if r.Role != "" {
			member, group := "", r.GroupID
			if r.TeamID != "" {
				var lead *string
				if err := tx.QueryRow(ctx, `select lead_id::text from teams where id = $1 and not archived`, r.TeamID).Scan(&lead); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if lead != nil {
					member = *lead
				}
			}
			if member == "" && group == "" {
				note = append(note, "nessun referente")
			} else if err := engine.SetStakeholder(ctx, tx, actor, el.ID, r.Role, member, group, false); err != nil {
				return err
			}
			note = append(note, r.Role)
		}
		for _, name := range []string{priorityTags[r.Priority], strings.TrimSpace(r.Label)} {
			if name == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `insert into tags (tenant_id, name) values (app_tenant(), $1) on conflict do nothing`, name); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into element_tags (tenant_id, element_id, tag_id)
				select app_tenant(), $1, id from tags where owner_id is null and lower(name) = lower($2) on conflict do nothing`, el.ID, name); err != nil {
				return err
			}
			note = append(note, name)
		}
		if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "rule", "portal_routing",
			map[string]any{"rule": "Instradamento del portale", "action": "assign_role", "note": strings.Join(note, ", ")}, nil); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Configurazione del catalogo

type CatalogConfig struct {
	Entries    []Entry         `json:"entries"`
	Stored     bool            `json:"stored"`
	Types      []AvailableType `json:"types"`
	Standard   []NamedField    `json:"standard_fields"`
	Roles      []NamedField    `json:"roles"`
	Teams      []NamedField    `json:"teams"`
	Groups     []NamedField    `json:"groups"`
	Priorities []string        `json:"priorities"`
}

func (s *Service) namedList(ctx context.Context, tx pgx.Tx, q string) ([]NamedField, error) {
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[NamedField])
	if out == nil {
		out = []NamedField{}
	}
	return out, err
}

func (s *Service) CatalogConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*CatalogConfig, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	_, entries, err := s.loadPortal(ctx, tx)
	if err != nil {
		return nil, err
	}
	_, stored, err := s.storedEntries(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &CatalogConfig{Entries: entries, Stored: stored, Types: []AvailableType{}, Standard: []NamedField{}, Priorities: []string{"low", "medium", "high"}}
	rows, err := tx.Query(ctx, `select t.key, t.name from element_types t
		where t.module in ('strategy', 'core', 'grc') and t.archived_at is null and exists (select 1 from space_types st where st.type_id = t.id)
		order by t.name`)
	if err != nil {
		return nil, err
	}
	var k, n string
	var types [][2]string
	if _, err := pgx.ForEachRow(rows, []any{&k, &n}, func() error { types = append(types, [2]string{k, n}); return nil }); err != nil {
		return nil, err
	}
	for _, kn := range types {
		fields, err := s.typeFieldKeys(ctx, tx, kn[0])
		if err != nil {
			return nil, err
		}
		out.Types = append(out.Types, AvailableType{Key: kn[0], Name: kn[1], Fields: fields})
	}
	for _, f := range StandardFields {
		out.Standard = append(out.Standard, NamedField{Key: f.Key, Name: f.Label})
	}
	if out.Roles, err = s.namedList(ctx, tx, `select key, name from stakeholder_roles order by name`); err != nil {
		return nil, err
	}
	if out.Teams, err = s.namedList(ctx, tx, `select id::text, name from teams where not archived order by lower(name)`); err != nil {
		return nil, err
	}
	if out.Groups, err = s.namedList(ctx, tx, `select id::text, name from groups where system_kind is null order by lower(name)`); err != nil {
		return nil, err
	}
	return out, nil
}

// ensureStored salva il catalogo di sempre alla prima modifica, così le voci non spariscono.
func (s *Service) ensureStored(ctx context.Context, tx pgx.Tx, actor engine.Actor) error {
	_, stored, err := s.storedEntries(ctx, tx)
	if err != nil || stored {
		return err
	}
	_, entries, err := s.loadPortal(ctx, tx)
	if err != nil {
		return err
	}
	for i, e := range entries {
		form, _ := json.Marshal(e.Form)
		if _, err := tx.Exec(ctx, `insert into portal_entries (tenant_id, id, key, label, description, icon, type_key, position, active, form, published_at, updated_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, true, $8, now(), $9)`,
			db.NewID(), e.Key, e.Label, e.Description, e.Icon, e.Type, i, form, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	return nil
}

type EntryInput struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
	Type        string `json:"type"`
}

func slug(s string) string {
	repl := strings.NewReplacer("à", "a", "è", "e", "é", "e", "ì", "i", "ò", "o", "ù", "u")
	var b strings.Builder
	dash := true
	for _, r := range repl.Replace(strings.ToLower(s)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || !unicode.IsLetter(rune(out[0])) {
		out = "voce-" + out
	}
	out = strings.Trim(out, "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func cleanMeta(label, description, category, icon string) (string, string, string, string, error) {
	label, description, category, icon = strings.TrimSpace(label), strings.TrimSpace(description), strings.TrimSpace(category), strings.TrimSpace(icon)
	if label == "" || len(label) > 80 || len(description) > 400 || len(category) > 60 || len(icon) > 30 {
		return "", "", "", "", engine.InvalidCode("validation.portal.entry_text_limits", "label", "voce: nome da 1 a 80 caratteri, descrizione fino a 400, categoria fino a 60")
	}
	if icon == "" {
		icon = "doc"
	}
	return label, description, category, icon, nil
}

// CreateEntry aggiunge una voce al catalogo, con il modulo di sempre per il suo tipo.
func (s *Service) CreateEntry(ctx context.Context, tx pgx.Tx, actor engine.Actor, in EntryInput) (*Entry, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	label, desc, cat, icon, err := cleanMeta(in.Label, in.Description, in.Category, in.Icon)
	if err != nil {
		return nil, err
	}
	var typeName string
	if err := tx.QueryRow(ctx, `select name from element_types where key = $1 and archived_at is null`, in.Type).Scan(&typeName); errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.NotFound("type", "", "tipo «%s» inesistente", in.Type)
	} else if err != nil {
		return nil, err
	}
	if err := s.ensureStored(ctx, tx, actor); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = slug(label)
	}
	base := key
	for n := 2; ; n++ {
		var taken bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from portal_entries where key = $1)`, key).Scan(&taken); err != nil {
			return nil, err
		}
		if !taken {
			break
		}
		if in.Key != "" {
			return nil, engine.InvalidParams("validation.portal.entry_key_taken", "key", map[string]any{"key": key}, "la voce «%s» esiste già", key)
		}
		key = fmt.Sprintf("%s-%d", base, n)
	}
	fields, err := s.typeFieldKeys(ctx, tx, in.Type)
	if err != nil {
		return nil, err
	}
	pt := PortalType{Type: in.Type, Fields: []string{}, Standard: []string{}}
	for _, f := range fields {
		pt.Fields = append(pt.Fields, f.Key)
	}
	if in.Type == requestType {
		for _, f := range StandardFields {
			pt.Standard = append(pt.Standard, f.Key)
		}
	}
	form, _ := json.Marshal(defaultForm(pt))
	if _, err := tx.Exec(ctx, `insert into portal_entries (tenant_id, id, key, label, description, category, icon, type_key, position, form, published_at, updated_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, (select coalesce(max(position), -1) + 1 from portal_entries), $8, now(), $9)`,
		db.NewID(), key, label, desc, cat, icon, in.Type, form, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update element_types set portal_enabled = true, updated_at = now() where key = $1 and not portal_enabled`, in.Type); err != nil {
		return nil, err
	}
	if err := s.auditEntry(ctx, tx, actor, "portal.entry.create", key); err != nil {
		return nil, err
	}
	return s.oneEntry(ctx, tx, key)
}

func (s *Service) auditEntry(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, key string) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "portal_entry", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"entry": key}})
}

func (s *Service) oneEntry(ctx context.Context, tx pgx.Tx, key string) (*Entry, error) {
	_, entries, err := s.loadPortal(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Key == key {
			return &entries[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

// EntryPatch: nome, descrizione, categoria, icona, attivazione e gruppo valgono subito; il modulo va in bozza.
type EntryPatch struct {
	Label       *string    `json:"label"`
	Description *string    `json:"description"`
	Category    *string    `json:"category"`
	Icon        *string    `json:"icon"`
	Active      *bool      `json:"active"`
	GroupID     *string    `json:"group_id"`
	Form        *EntryForm `json:"form"`
}

func (s *Service) UpdateEntry(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, in EntryPatch) (*Entry, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	if err := s.ensureStored(ctx, tx, actor); err != nil {
		return nil, err
	}
	cur, err := s.oneEntry(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	str := func(p *string, def string) string {
		if p != nil {
			return *p
		}
		return def
	}
	label, desc, cat, icon, err := cleanMeta(str(in.Label, cur.Label), str(in.Description, cur.Description), str(in.Category, cur.Category), str(in.Icon, cur.Icon))
	if err != nil {
		return nil, err
	}
	active := cur.Active
	if in.Active != nil {
		active = *in.Active
	}
	group := cur.GroupID
	if in.GroupID != nil {
		group = strings.TrimSpace(*in.GroupID)
		if group != "" {
			if !uuidOK(group) {
				return nil, engine.InvalidCode("validation.portal.group_unknown", "group_id", "il gruppo scelto non esiste")
			}
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from groups where id = $1)`, group).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				return nil, engine.InvalidCode("validation.portal.group_unknown", "group_id", "il gruppo scelto non esiste")
			}
		}
	}
	var draft any
	if in.Form != nil {
		f, err := s.cleanForm(ctx, tx, cur.Type, *in.Form)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(f)
		draft = raw
	}
	if _, err := tx.Exec(ctx, `update portal_entries set label = $2, description = $3, category = $4, icon = $5, active = $6,
		group_id = nullif($7, '')::uuid, draft = coalesce($8::jsonb, draft), updated_by = $9, updated_at = now() where key = $1`,
		key, label, desc, cat, icon, active, group, draft, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := s.auditEntry(ctx, tx, actor, "portal.entry.update", key); err != nil {
		return nil, err
	}
	return s.oneEntry(ctx, tx, key)
}

// PublishEntry rende pubblico il modulo in bozza.
func (s *Service) PublishEntry(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Entry, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	tag, err := tx.Exec(ctx, `update portal_entries set form = draft, draft = null, published_at = now(), updated_by = $2, updated_at = now()
		where key = $1 and draft is not null`, key, nullable(actor.MemberID))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.oneEntry(ctx, tx, key); err != nil {
			return nil, err
		}
		return nil, engine.InvalidCode("validation.portal.nothing_to_publish", "form", "non ci sono modifiche da pubblicare")
	}
	if _, err := tx.Exec(ctx, `update element_types set portal_enabled = true, updated_at = now()
		where not portal_enabled and key = (select type_key from portal_entries where key = $1)`, key); err != nil {
		return nil, err
	}
	if err := s.auditEntry(ctx, tx, actor, "portal.entry.publish", key); err != nil {
		return nil, err
	}
	return s.oneEntry(ctx, tx, key)
}

// DiscardEntryDraft scarta le modifiche non pubblicate.
func (s *Service) DiscardEntryDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Entry, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `update portal_entries set draft = null, updated_at = now() where key = $1`, key); err != nil {
		return nil, err
	}
	if err := s.auditEntry(ctx, tx, actor, "portal.entry.discard", key); err != nil {
		return nil, err
	}
	return s.oneEntry(ctx, tx, key)
}

// ReorderEntries: l'ordine delle carte nel catalogo.
func (s *Service) ReorderEntries(ctx context.Context, tx pgx.Tx, actor engine.Actor, keys []string) error {
	if !canConfigurePortal(actor) {
		return engine.ErrForbidden
	}
	if err := s.ensureStored(ctx, tx, actor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update portal_entries e set position = k.ord - 1 from unnest($1::text[]) with ordinality as k(key, ord)
		where e.key = k.key`, keys); err != nil {
		return err
	}
	return s.auditEntry(ctx, tx, actor, "portal.entry.reorder", "")
}

// EntryPreview: la voce come la vedrebbe il richiedente con il modulo in modifica, e le unità per il campo «unità».
type EntryPreview struct {
	Entry  EntryView    `json:"entry"`
	Units  []NamedField `json:"units"`
	MyUnit string       `json:"my_unit"`
}

func (s *Service) PreviewEntry(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, form EntryForm) (*EntryPreview, error) {
	if !canConfigurePortal(actor) {
		return nil, engine.ErrForbidden
	}
	cur, err := s.oneEntry(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	f, err := s.cleanForm(ctx, tx, cur.Type, form)
	if err != nil {
		return nil, err
	}
	v, err := s.entryView(ctx, tx, *cur, f)
	if err != nil {
		return nil, err
	}
	units, mine, err := s.unitsFor(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	return &EntryPreview{Entry: *v, Units: units, MyUnit: mine}, nil
}

func formLimit(code string, params map[string]any, msg string, args ...any) error {
	return engine.InvalidParams(code, "form", params, msg, args...)
}

// cleanForm controlla il modulo contro il tipo: campi esistenti e non ripetuti, condizioni valide, regole coerenti.
func (s *Service) cleanForm(ctx context.Context, tx pgx.Tx, typeKey string, in EntryForm) (EntryForm, error) {
	normalizeForm(&in)
	trim := func(v string, max int, what string) (string, error) {
		v = strings.TrimSpace(v)
		if len([]rune(v)) > max {
			return "", formLimit("validation.portal.form_text_limits", map[string]any{"what": what, "max": max}, "«%s»: massimo %d caratteri", what, max)
		}
		return v, nil
	}
	var err error
	if in.Intro, err = trim(in.Intro, 1000, "intro"); err != nil {
		return in, err
	}
	if in.TitleLabel, err = trim(in.TitleLabel, 120, "title_label"); err != nil {
		return in, err
	}
	if in.DescLabel, err = trim(in.DescLabel, 120, "desc_label"); err != nil {
		return in, err
	}
	if len(in.Steps) > maxSteps {
		return in, formLimit("validation.portal.form_too_many_steps", map[string]any{"max": maxSteps}, "al massimo %d passi", maxSteps)
	}
	fields, err := s.typeFieldKeys(ctx, tx, typeKey)
	if err != nil {
		return in, err
	}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.Key] = true
	}
	seen := map[string]bool{}
	for i := range in.Steps {
		st := &in.Steps[i]
		if st.Preset != "" && st.Preset != "context" && st.Preset != "details" {
			st.Preset = ""
		}
		if st.Title, err = trim(st.Title, 80, "title"); err != nil {
			return in, err
		}
		if st.Intro, err = trim(st.Intro, 1000, "intro"); err != nil {
			return in, err
		}
		for j := range st.Fields {
			f := &st.Fields[j]
			id := f.Key
			if f.Std {
				id = "std:" + f.Key
				if typeKey != requestType || !standardKnown(f.Key) {
					return in, formLimit("validation.portal.form_field_unknown", map[string]any{"field": f.Key}, "il dato «%s» non è disponibile per questo tipo", f.Key)
				}
			} else if !known[f.Key] {
				return in, formLimit("validation.portal.form_field_unknown", map[string]any{"field": f.Key}, "il campo «%s» non è del tipo", f.Key)
			}
			if seen[id] {
				return in, formLimit("validation.portal.form_field_repeated", map[string]any{"field": f.Key}, "il campo «%s» è ripetuto", f.Key)
			}
			seen[id] = true
			if f.Label, err = trim(f.Label, 120, "label"); err != nil {
				return in, err
			}
			if f.Help, err = trim(f.Help, 300, "help"); err != nil {
				return in, err
			}
			if f.VisibleWhen = strings.TrimSpace(f.VisibleWhen); f.VisibleWhen != "" {
				if _, err := engine.ParseCondition(f.VisibleWhen); err != nil {
					return in, formLimit("validation.portal.condition_invalid", map[string]any{"field": f.Key}, "condizione del campo «%s» non valida: %v", f.Key, err)
				}
			}
		}
	}
	if len(in.Routes) > maxRoutes {
		return in, formLimit("validation.portal.route_too_many", map[string]any{"max": maxRoutes}, "al massimo %d regole", maxRoutes)
	}
	for i := range in.Routes {
		r := &in.Routes[i]
		r.When, r.Role, r.TeamID, r.GroupID, r.Label = strings.TrimSpace(r.When), strings.TrimSpace(r.Role), strings.TrimSpace(r.TeamID), strings.TrimSpace(r.GroupID), strings.TrimSpace(r.Label)
		if _, err := engine.ParseCondition(r.When); err != nil {
			return in, formLimit("validation.portal.condition_invalid", map[string]any{"field": fmt.Sprint(i + 1)}, "condizione della regola %d non valida: %v", i+1, err)
		}
		if r.Priority != "" && priorityTags[r.Priority] == "" {
			return in, formLimit("validation.portal.route_priority_invalid", map[string]any{"rule": i + 1}, "regola %d: priorità non valida", i+1)
		}
		if len([]rune(r.Label)) > 40 {
			return in, formLimit("validation.portal.form_text_limits", map[string]any{"what": "label", "max": 40}, "l'etichetta iniziale ha al massimo 40 caratteri")
		}
		if (r.Role == "") != (r.TeamID == "" && r.GroupID == "") || (r.TeamID != "" && r.GroupID != "") {
			return in, formLimit("validation.portal.route_target_required", map[string]any{"rule": i + 1}, "regola %d: indica il ruolo e un team oppure un gruppo", i+1)
		}
		if r.Role != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from stakeholder_roles where key = $1)`, r.Role).Scan(&ok); err != nil {
				return in, err
			}
			if !ok {
				return in, formLimit("validation.portal.route_role_unknown", map[string]any{"rule": i + 1}, "regola %d: il ruolo non esiste", i+1)
			}
		}
		for _, t := range [][2]string{{"teams", r.TeamID}, {"groups", r.GroupID}} {
			if t[1] == "" {
				continue
			}
			var ok bool
			if uuidOK(t[1]) {
				if err := tx.QueryRow(ctx, `select exists (select 1 from `+t[0]+` where id = $1)`, t[1]).Scan(&ok); err != nil {
					return in, err
				}
			}
			if !ok {
				return in, formLimit("validation.portal.route_target_unknown", map[string]any{"rule": i + 1}, "regola %d: il team o il gruppo non esiste", i+1)
			}
		}
	}
	return in, nil
}

// ---------------------------------------------------------------------------
// «È questa»: invece di un duplicato, la persona si aggiunge alla richiesta già aperta.

// Support aggiunge la persona come richiedente della richiesta simile e un commento; restituisce la chiave.
func (s *Service) Support(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, text string) (string, error) {
	if !canPortal(actor) {
		return "", engine.ErrForbidden
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 {
		return "", engine.InvalidCode("validation.portal.support_text", "comment", "scrivi una riga per dire che hai la stessa esigenza")
	}
	var id, key string
	var mine bool
	err := tx.QueryRow(ctx, `
		select e.id::text, sp.key || '-' || e.number, `+mineSQL+`
		from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.portal_enabled
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft and coalesce(o.category, '') not in ('done', 'cancelled')
		  and upper(sp.key || '-' || e.number) = upper($2) and (`+mineSQL+` or $3::boolean)`,
		actor.MemberID, ref, actor.Has(rbac.ElementRead)).Scan(&id, &key, &mine)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", engine.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !mine {
		var role bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from stakeholder_roles where key = 'requester')`).Scan(&role); err != nil {
			return "", err
		}
		if role {
			if err := engine.SetStakeholder(ctx, tx, actor, id, "requester", actor.MemberID, "", false); err != nil {
				return "", err
			}
		}
	}
	cid := db.NewID()
	body, _ := json.Marshal(map[string]any{"format": "markdown", "mentions": []any{}})
	if _, err := tx.Exec(ctx, `insert into comments (tenant_id, id, element_id, author_id, body, body_text) values (app_tenant(), $1, $2, $3, $4, $5)`,
		cid, id, actor.MemberID, body, text); err != nil {
		return "", err
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "comment", "added", map[string]any{"comment_id": cid}, nil); err != nil {
		return "", err
	}
	return key, audit.Append(ctx, tx, audit.Event{Action: "portal.support", EntityType: "element", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}
