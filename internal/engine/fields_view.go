package engine

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// Viste dei campi personalizzati per il dettaglio e per i form, e fatti delle condizioni.

// OptionView è un'opzione di un insieme di valori. Color è un token del tema
// (accent, ok, flag, danger, muted, fg) o vuoto: l'interfaccia lo rende con le variabili
// del tema scelto, mai con un colore fisso.
type OptionView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color,omitempty"`
	Archived bool   `json:"archived,omitempty"`
	// Labels: nome per lingua (solo visualizzazione; il valore resta Name).
	Labels map[string]string `json:"labels,omitempty"`
}

// RefLabel è l'etichetta di un riferimento (persona, gruppo, elemento).
type RefLabel struct {
	Label string `json:"label"`
	Key   string `json:"key,omitempty"`  // elementi: DEM-12
	Type  string `json:"type,omitempty"` // elementi: chiave del tipo
}

// FieldView è un campo del tipo come lo vede l'interfaccia: definizione, regole
// dell'associazione e, nel dettaglio di un elemento, valore e stato.
type FieldView struct {
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	DataType     string        `json:"data_type"`
	Multi        bool          `json:"multi"`
	Help         string        `json:"help,omitempty"`
	Section      string        `json:"section"`
	Ordinal      int           `json:"ordinal"`
	Required     bool          `json:"required"`
	RequiredFrom string        `json:"required_from,omitempty"`
	VisibleWhen  string        `json:"visible_when,omitempty"`
	Archived     bool          `json:"archived,omitempty"`
	Settings     FieldSettings `json:"settings"`
	Default      any           `json:"default,omitempty"`
	Options      []OptionView  `json:"options,omitempty"`
	// Solo nel dettaglio di un elemento.
	Value       any                 `json:"value,omitempty"`
	Visible     bool                `json:"visible"`
	RequiredNow bool                `json:"required_now"`
	Labels      map[string]RefLabel `json:"labels,omitempty"`
}

func viewOf(d FieldDef) FieldView {
	return FieldView{Key: d.Key, Name: d.Name, DataType: d.DataType, Multi: d.Multi, Help: d.Help, Section: d.Section,
		Ordinal: d.Ordinal, Required: d.Required, RequiredFrom: d.RequiredFrom, VisibleWhen: d.VisibleWhen,
		Archived: d.Archived, Settings: d.Settings, Default: d.Default, Visible: true}
}

// optionsOf legge le opzioni (anche archiviate) degli insiemi indicati.
func optionsOf(ctx context.Context, tx pgx.Tx, setIDs []string) (map[string][]OptionView, error) {
	out := map[string][]OptionView{}
	if len(setIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select option_set_id::text, id::text, name, coalesce(color, ''), archived, labels from options
		where option_set_id = any($1::uuid[]) order by option_set_id, ordinal, lower(name)`, setIDs)
	if err != nil {
		return nil, err
	}
	var set string
	var o OptionView
	var lb []byte
	_, err = pgx.ForEachRow(rows, []any{&set, &o.ID, &o.Name, &o.Color, &o.Archived, &lb}, func() error {
		o.Labels = parseLabels(lb)
		out[set] = append(out[set], o)
		return nil
	})
	return out, err
}

func withOptions(ctx context.Context, tx pgx.Tx, defs []FieldDef, views []FieldView) error {
	var sets []string
	for _, d := range defs {
		if d.OptionSetID != nil {
			sets = append(sets, *d.OptionSetID)
		}
	}
	opts, err := optionsOf(ctx, tx, sets)
	if err != nil {
		return err
	}
	for i, d := range defs {
		if d.OptionSetID != nil {
			views[i].Options = opts[*d.OptionSetID]
		}
	}
	return nil
}

// TypeFields elenca i campi del tipo per i form di creazione (senza gli archiviati).
func (s *Service) TypeFields(ctx context.Context, tx pgx.Tx, typeKey string) ([]FieldView, error) {
	t, err := LoadType(ctx, tx, typeKey)
	if err != nil {
		return nil, err
	}
	all, err := loadTypeFields(ctx, tx, t.ID)
	if err != nil {
		return nil, err
	}
	defs := make([]FieldDef, 0, len(all))
	for _, d := range all {
		if !d.Archived {
			defs = append(defs, d)
		}
	}
	views := make([]FieldView, len(defs))
	for i, d := range defs {
		views[i] = viewOf(d)
		views[i].RequiredNow = d.Required && d.VisibleWhen == ""
	}
	if err := withOptions(ctx, tx, defs, views); err != nil {
		return nil, err
	}
	// Le etichette dei valori predefiniti (persone, gruppi, elementi) per mostrarli nel form.
	labels, err := refLabels(ctx, tx, defs, func(d FieldDef) any { return d.Default })
	if err != nil {
		return nil, err
	}
	for i, d := range defs {
		views[i].Labels = labels[d.Key]
	}
	return views, nil
}

// FieldViews restituisce i campi dell'elemento con valore, visibilità e obbligatorietà
// nella fase attuale. I campi archiviati compaiono solo se hanno un valore (in sola lettura).
func (s *Service) FieldViews(ctx context.Context, tx pgx.Tx, el *Element) ([]FieldView, error) {
	all, err := loadTypeFields(ctx, tx, el.TypeID)
	if err != nil {
		return nil, err
	}
	defs := make([]FieldDef, 0, len(all))
	for _, d := range all {
		if !d.Archived || !isEmptyValue(el.Fields[d.Key]) {
			defs = append(defs, d)
		}
	}
	views := make([]FieldView, len(defs))
	if len(defs) == 0 {
		return views, nil
	}
	var states []State
	var facts Facts
	for _, d := range defs {
		if (d.Required || d.RequiredFrom != "") && states == nil {
			fl, err := s.flowOf(ctx, tx, el)
			if err != nil {
				return nil, err
			}
			states = fl.states
			if states == nil {
				states = []State{}
			}
		}
		if d.VisibleWhen != "" && facts == nil {
			if facts, err = s.ElementFacts(ctx, tx, el); err != nil {
				return nil, err
			}
		}
	}
	for i, d := range defs {
		v := viewOf(d)
		v.Value = el.Fields[d.Key]
		v.Visible = d.visibleWith(facts)
		v.RequiredNow = v.Visible && (d.Required || d.RequiredFrom != "") && d.requiredIn(states, el.State)
		views[i] = v
	}
	if err := withOptions(ctx, tx, defs, views); err != nil {
		return nil, err
	}
	labels, err := refLabels(ctx, tx, defs, func(d FieldDef) any { return el.Fields[d.Key] })
	if err != nil {
		return nil, err
	}
	for i, d := range defs {
		views[i].Labels = labels[d.Key]
	}
	return views, nil
}

// refLabels risolve i riferimenti (persone, gruppi, elementi) dei valori in etichette,
// con una query per genere: campo → id → etichetta.
func refLabels(ctx context.Context, tx pgx.Tx, defs []FieldDef, value func(FieldDef) any) (map[string]map[string]RefLabel, error) {
	ids := map[string][]string{} // genere → id
	for _, d := range defs {
		switch d.DataType {
		case "member", "group", "element":
			ids[d.DataType] = append(ids[d.DataType], stringsOf(value(d))...)
		}
	}
	found := map[string]RefLabel{}
	queries := map[string]string{
		"member": `select id::text, display_name, '', '' from members where id::text = any($1)`,
		"group":  `select id::text, name, '', '' from groups where id::text = any($1)`,
		"element": `select e.id::text, e.title, coalesce(sp.key || '-' || e.number, ''), t.key from elements e
			join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			where e.id::text = any($1) and e.deleted_at is null`,
	}
	for kind, list := range ids {
		if len(list) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, queries[kind], list)
		if err != nil {
			return nil, err
		}
		var id string
		var l RefLabel
		if _, err := pgx.ForEachRow(rows, []any{&id, &l.Label, &l.Key, &l.Type}, func() error {
			found[id] = l
			return nil
		}); err != nil {
			return nil, err
		}
	}
	out := map[string]map[string]RefLabel{}
	for _, d := range defs {
		switch d.DataType {
		case "member", "group", "element":
			for _, id := range stringsOf(value(d)) {
				if l, ok := found[id]; ok {
					if out[d.Key] == nil {
						out[d.Key] = map[string]RefLabel{}
					}
					out[d.Key][id] = l
				}
			}
		}
	}
	return out, nil
}

func stringsOf(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// fieldFacts aggiunge ai fatti i valori dei campi personalizzati, in forma leggibile:
// opzioni, persone e gruppi per nome, elementi per chiave e titolo, importi in unità.
func fieldFacts(ctx context.Context, tx pgx.Tx, defs []FieldDef, fields map[string]any, f Facts) error {
	var refDefs []FieldDef
	for _, d := range defs {
		v, ok := fields[d.Key]
		if !ok || v == nil {
			continue
		}
		vals, isList := v.([]any)
		if !isList {
			vals = []any{v}
		}
		switch d.DataType {
		case "enum", "version", "state", "member", "group", "element":
			refDefs = append(refDefs, d)
		case "money":
			var amounts, currencies []any
			for _, x := range vals {
				if m, ok := x.(map[string]any); ok {
					amounts = append(amounts, m["amount"])
					currencies = append(currencies, m["currency"])
				}
			}
			f.Set(d.Key, amounts...)
			f.Set(d.Key+".valuta", currencies...)
		default:
			f.Set(d.Key, vals...)
		}
	}
	if len(refDefs) == 0 {
		return nil
	}
	labels, err := refLabels(ctx, tx, refDefs, func(d FieldDef) any { return fields[d.Key] })
	if err != nil {
		return err
	}
	var sets []string
	for _, d := range refDefs {
		if d.OptionSetID != nil {
			sets = append(sets, *d.OptionSetID)
		}
	}
	opts, err := optionsOf(ctx, tx, sets)
	if err != nil {
		return err
	}
	for _, d := range refDefs {
		var out []any
		for _, id := range stringsOf(fields[d.Key]) {
			switch d.DataType {
			case "enum", "version", "state":
				if d.OptionSetID != nil {
					for _, o := range opts[*d.OptionSetID] {
						if o.ID == id {
							out = append(out, o.Name)
						}
					}
				}
			case "element":
				if l, ok := labels[d.Key][id]; ok {
					out = append(out, l.Key, l.Label)
				}
			default:
				if l, ok := labels[d.Key][id]; ok {
					out = append(out, l.Label)
				}
			}
		}
		f.Set(d.Key, out...)
	}
	return nil
}

// PageLabels risolve i riferimenti a elementi e gruppi dei campi personalizzati degli
// elementi di una pagina di risultati: due query al massimo, solo se ce ne sono.
func PageLabels(ctx context.Context, tx pgx.Tx, items []*Element) (map[string]RefLabel, error) {
	if len(items) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `select key, data_type from field_defs where data_type in ('element', 'group')`)
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	var k, dt string
	if _, err := pgx.ForEachRow(rows, []any{&k, &dt}, func() error {
		kinds[k] = dt
		return nil
	}); err != nil {
		return nil, err
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	var defs []FieldDef
	fields := map[string]any{}
	n := 0
	for _, el := range items {
		for key, kind := range kinds {
			if v, ok := el.Fields[key]; ok {
				n++
				fk := key + "#" + strconv.Itoa(n)
				defs = append(defs, FieldDef{Key: fk, DataType: kind})
				fields[fk] = v
			}
		}
	}
	if len(defs) == 0 {
		return nil, nil
	}
	labels, err := refLabels(ctx, tx, defs, func(d FieldDef) any { return fields[d.Key] })
	if err != nil {
		return nil, err
	}
	out := map[string]RefLabel{}
	for _, m := range labels {
		for id, l := range m {
			out[id] = l
		}
	}
	return out, nil
}
