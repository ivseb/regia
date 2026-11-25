package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// Editor visuale dei processi (sdlc.md §10, "Editor visuale dei processi").
// Qui ci sono i metadati che servono all'editor (ruoli, gruppi, controlli, politiche
// di approvazione, fatti disponibili, tipi di azione delle regole), il catalogo per
// creare un processo (tipi e pacchetti di partenza) e la validazione senza salvataggio.
// I moduli descrivono i loro fatti, le loro azioni e i loro pacchetti con
// DescribeFacts, DescribeAction e Starter, alla registrazione.

// FactInfo descrive un fatto usabile nelle condizioni.
type FactInfo struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`              // text | number | bool | date | presence
	Example string   `json:"example,omitempty"` // valore di esempio (da un elemento del tipo, se c'è)
	Values  []string `json:"values,omitempty"`  // valori suggeriti
	Source  string   `json:"source"`            // base | modulo | campo | ruolo | elemento
	// Advanced: dato fuori contesto (es. un ruolo che nessun elemento del tipo usa). L'editor lo
	// mostra solo in modalità «avanzato».
	Advanced bool `json:"advanced,omitempty"`
}

// ActionField è un parametro di un tipo di azione delle regole.
type ActionField struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // role | group | member | phase | bool | text
	Required bool   `json:"required,omitempty"`
	Help     string `json:"help,omitempty"`
}

// ActionSchema descrive un tipo di azione delle regole per l'editor. Un tipo che
// l'editor non conosce si modifica con un modulo generico chiave/valore.
type ActionSchema struct {
	Kind        string        `json:"kind"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Fields      []ActionField `json:"fields"`
	// OneOf: parametri alternativi, ne va indicato esattamente uno (es. gruppo oppure membro).
	OneOf []string `json:"one_of,omitempty"`
}

// assignRole è l'unico tipo di azione del motore (vedi runAction).
var assignRole = ActionSchema{
	Kind: "assign_role", Name: "Assegna un ruolo",
	Description: "Assegna un ruolo sull'elemento a un gruppo o a un membro.",
	Fields: []ActionField{
		{Key: "role", Name: "Ruolo", Kind: "role", Required: true},
		{Key: "group", Name: "Gruppo", Kind: "group"},
		{Key: "member", Name: "Membro", Kind: "member"},
		{Key: "replace", Name: "Sostituisce i titolari attuali", Kind: "bool",
			Help: "Se attivo, chi ha già il ruolo lo perde; altrimenti si aggiunge."},
	},
	OneOf: []string{"group", "member"},
}

// editorRegistry raccoglie le descrizioni registrate dai moduli all'avvio.
type editorRegistry struct {
	mu       sync.RWMutex
	facts    map[string][]FactInfo // per chiave del tipo
	actions  []ActionSchema
	starters []Bundle
}

// DescribeFacts dà nome, tipo e valori suggeriti ai fatti che un modulo fornisce per un
// tipo di elemento (vedi Facts). Servono all'editor anche quando non c'è ancora
// nessun elemento del tipo da cui ricavarli.
func (s *Service) DescribeFacts(typeKey string, facts ...FactInfo) {
	r := &s.editor
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.facts == nil {
		r.facts = map[string][]FactInfo{}
	}
	for _, f := range facts {
		f.Key = strings.ToLower(f.Key)
		if f.Source == "" {
			f.Source = "modulo"
		}
		if f.Kind == "" {
			f.Kind = "text"
		}
		r.facts[typeKey] = append(r.facts[typeKey], f)
	}
}

// DescribeAction registra lo schema di un tipo di azione delle regole (per l'editor).
// Uno schema con lo stesso tipo sostituisce il precedente.
func (s *Service) DescribeAction(a ActionSchema) {
	r := &s.editor
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = slices.DeleteFunc(r.actions, func(x ActionSchema) bool { return x.Kind == a.Kind })
	r.actions = append(r.actions, a)
}

// ActionSchemas elenca i tipi di azione: assign_role e poi quelli registrati dai moduli.
func (s *Service) ActionSchemas() []ActionSchema {
	r := &s.editor
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []ActionSchema{assignRole}
	described := map[string]bool{assignRole.Kind: true}
	for _, a := range r.actions {
		if !described[a.Kind] {
			described[a.Kind] = true
			out = append(out, a)
		}
	}
	// I tipi registrati da un modulo senza descrizione si modificano con il modulo generico.
	for k := range s.hooks.Actions {
		if !described[k] {
			out = append(out, ActionSchema{Kind: k, Name: k, Fields: []ActionField{}})
		}
	}
	slices.SortStableFunc(out[1:], func(a, b ActionSchema) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Starter registra un pacchetto di partenza (formato di export) offerto alla creazione
// di un processo. I pacchetti sono inclusi nel binario: uno non valido è un errore di
// programmazione e ferma l'avvio.
func (s *Service) Starter(raw []byte) {
	b, err := ParseBundle(raw)
	if err != nil {
		panic("pacchetto di partenza non valido: " + err.Error())
	}
	r := &s.editor
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starters = slices.DeleteFunc(r.starters, func(x Bundle) bool { return x.Key == b.Key })
	r.starters = append(r.starters, *b)
}

// NamedKey è una coppia chiave e nome (ruoli, gruppi, categorie).
type NamedKey struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// PolicyInfo è una politica di approvazione attiva, per fase.
type PolicyInfo struct {
	Name     string            `json:"name"`
	Stage    string            `json:"stage"`
	Type     string            `json:"type,omitempty"` // vuoto = vale per tutti i tipi
	Outcomes map[string]string `json:"outcomes"`       // approve | reject | defer → fase di arrivo
}

// ProcessMeta sono i dati di riferimento per l'editor di un processo.
type ProcessMeta struct {
	Type     NamedKey       `json:"type"`
	Module   string         `json:"module"`
	Roles    []NamedKey     `json:"roles"`
	Groups   []NamedKey     `json:"groups"` // key = id del gruppo; nelle regole si usa il nome
	Checks   []string       `json:"checks"`
	Policies []PolicyInfo   `json:"policies"`
	Facts    []FactInfo     `json:"facts"`
	Sample   string         `json:"sample,omitempty"` // elemento da cui vengono gli esempi
	Actions  []ActionSchema `json:"actions"`
}

var phaseCategoryNames = []NamedKey{{"todo", "da iniziare"}, {"in_progress", "in corso"}, {"done", "conclusa"}, {"cancelled", "annullata"}}

// ProcessMeta restituisce i metadati per l'editor del processo indicato.
func (s *Service) ProcessMeta(ctx context.Context, tx pgx.Tx, actor Actor, ref string) (*ProcessMeta, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	_, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	t, err := loadTypeByID(ctx, tx, typeID)
	if err != nil {
		return nil, err
	}
	m := &ProcessMeta{Type: NamedKey{t.Key, t.Name}, Module: t.Module, Checks: s.GuardNames(), Actions: localizeActions(ctx, s.ActionSchemas())}
	collect := func(q string, args ...any) ([]NamedKey, error) {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToStructByPos[NamedKey])
	}
	if m.Roles, err = collect(`select key, name from stakeholder_roles order by name`); err != nil {
		return nil, err
	}
	if m.Groups, err = collect(`select id::text, name from groups order by lower(name)`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select p.name, p.stage, coalesce(t.key, ''), p.rules
		from approval_policies p left join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		where p.active and (p.type_id = $1 or p.type_id is null)
		order by lower(p.stage), p.type_id nulls last, p.name`, typeID)
	if err != nil {
		return nil, err
	}
	m.Policies = []PolicyInfo{}
	var pi PolicyInfo
	var raw []byte
	if _, err := pgx.ForEachRow(rows, []any{&pi.Name, &pi.Stage, &pi.Type, &raw}, func() error {
		var body struct {
			Outcomes map[string]string `json:"outcomes"`
		}
		_ = json.Unmarshal(raw, &body) // politica illeggibile: si mostra senza esiti
		p := pi
		p.Outcomes = body.Outcomes
		if p.Outcomes == nil {
			p.Outcomes = map[string]string{}
		}
		m.Policies = append(m.Policies, p)
		return nil
	}); err != nil {
		return nil, err
	}
	if m.Facts, m.Sample, err = s.availableFacts(ctx, tx, actor, t, m.Roles); err != nil {
		return nil, err
	}
	return m, nil
}

// availableFacts elenca i fatti del tipo: quelli di base, quelli descritti dai moduli,
// i campi personalizzati, i ruoli e, se esiste un elemento del tipo, i suoi fatti con
// i valori come esempio (così compaiono anche i fatti che nessuno ha descritto).
func (s *Service) availableFacts(ctx context.Context, tx pgx.Tx, actor Actor, t *TypeInfo, roles []NamedKey) ([]FactInfo, string, error) {
	out, sample, err := s.availableFactsIt(ctx, tx, actor, t, roles)
	return localizeFacts(ctx, out), sample, err
}

// availableFactsIt elenca i fatti con i nomi nella lingua dei moduli (italiano); availableFacts
// li porta nella lingua di chi chiama.
func (s *Service) availableFactsIt(ctx context.Context, tx pgx.Tx, actor Actor, t *TypeInfo, roles []NamedKey) ([]FactInfo, string, error) {
	out := []FactInfo{}
	idx := map[string]int{}
	add := func(f FactInfo) {
		if i, ok := idx[f.Key]; ok {
			// Già noto: si completano solo i dati mancanti.
			if out[i].Example == "" {
				out[i].Example = f.Example
			}
			if len(out[i].Values) == 0 {
				out[i].Values = f.Values
			}
			return
		}
		idx[f.Key] = len(out)
		out = append(out, f)
	}

	phases := make([]string, 0, len(t.options))
	for _, o := range t.options {
		phases = append(phases, o.Name)
	}
	cats := make([]string, 0, len(phaseCategoryNames))
	for _, c := range phaseCategoryNames {
		cats = append(cats, c.Key)
	}
	rows, err := tx.Query(ctx, `select s.key from space_types st join spaces s on s.tenant_id = st.tenant_id and s.id = st.space_id
		where st.type_id = $1 order by s.key`, t.ID)
	if err != nil {
		return nil, "", err
	}
	spaces, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, "", err
	}
	add(FactInfo{Key: "tipo", Name: "Tipo di elemento", Kind: "text", Values: []string{t.Key, t.Name}, Source: "base"})
	add(FactInfo{Key: "stato", Name: "Fase attuale", Kind: "text", Values: phases, Source: "base"})
	add(FactInfo{Key: "categoria", Name: "Categoria della fase", Kind: "text", Values: cats, Source: "base"})
	add(FactInfo{Key: "spazio", Name: "Spazio", Kind: "text", Values: spaces, Source: "base"})

	s.editor.mu.RLock()
	described := slices.Clone(s.editor.facts[t.Key])
	s.editor.mu.RUnlock()
	for _, f := range described {
		add(f)
	}

	defs, err := loadTypeFields(ctx, tx, t.ID)
	if err != nil {
		return nil, "", err
	}
	for _, d := range defs {
		f := FactInfo{Key: strings.ToLower(d.Key), Name: d.Name, Kind: fieldKind(d.DataType), Source: "campo"}
		if d.DataType == "enum" && d.OptionSetID != nil {
			rows, err := tx.Query(ctx, `select name from options where option_set_id = $1 and not archived order by ordinal`, *d.OptionSetID)
			if err != nil {
				return nil, "", err
			}
			if f.Values, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
				return nil, "", err
			}
		}
		add(f)
	}
	// Due ruoli con lo stesso nome (es. «Compliance») si distinguono con la chiave.
	byName := map[string]int{}
	for _, r := range roles {
		byName[strings.ToLower(r.Name)]++
	}
	// Nel contesto del tipo stanno i ruoli che qualche suo elemento ha già assegnato. Se nessuno è
	// in uso (tenant nuovo) non si nasconde nulla: sarebbe un menu vuoto.
	usedRows, err := tx.Query(ctx, `select distinct r.key from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		join elements e on e.tenant_id = sh.tenant_id and e.id = sh.element_id
		where e.type_id = $1 and e.deleted_at is null`, t.ID)
	if err != nil {
		return nil, "", err
	}
	usedKeys, err := pgx.CollectRows(usedRows, pgx.RowTo[string])
	if err != nil {
		return nil, "", err
	}
	used := map[string]bool{}
	for _, k := range usedKeys {
		used[k] = true
	}
	for _, r := range roles {
		name := "Ruolo " + r.Name
		if byName[strings.ToLower(r.Name)] > 1 {
			name += " (" + r.Key + ")"
		}
		add(FactInfo{Key: "ruolo." + r.Key, Name: name, Kind: "presence", Source: "ruolo", Advanced: len(used) > 0 && !used[r.Key]})
	}

	// Esempi da un elemento del tipo: il più recente che chi configura può leggere.
	var sampleID string
	err = tx.QueryRow(ctx, `select id from elements where type_id = $1 and deleted_at is null and not is_draft
		order by updated_at desc limit 1`, t.ID).Scan(&sampleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	el, err := s.Get(ctx, tx, actor, sampleID)
	if errors.Is(err, ErrNotFound) {
		return out, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	facts, err := s.ElementFacts(ctx, tx, el)
	if err != nil {
		return nil, "", err
	}
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		vals := facts[k]
		if len(vals) == 0 {
			continue
		}
		if strings.HasPrefix(k, "ruolo.") {
			continue // i ruoli sono già elencati sopra, con il loro nome
		}
		f := FactInfo{Key: k, Name: factLabel(k), Kind: valueKind(vals[0]), Example: exampleOf(vals), Source: "elemento"}
		if f.Kind == "text" {
			f.Values = textValues(vals)
		}
		add(f)
	}
	return out, el.Key, nil
}

// factLabel dà un nome leggibile a un fatto che nessun modulo ha descritto: la chiave senza
// trattini bassi, con la prima lettera maiuscola («figli_aperti» diventa «Figli aperti»).
func factLabel(key string) string {
	t := strings.ReplaceAll(key, "_", " ")
	if t == "" {
		return key
	}
	r := []rune(t)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

func fieldKind(dataType string) string {
	switch dataType {
	case "integer", "duration", "decimal", "money":
		return "number"
	case "boolean":
		return "bool"
	case "date":
		return "date"
	}
	return "text"
}

func valueKind(v any) string {
	switch x := v.(type) {
	case float64:
		return "number"
	case bool:
		return "bool"
	case string:
		if len(x) == 10 {
			if _, err := time.Parse(time.DateOnly, x); err == nil {
				return "date"
			}
		}
	}
	return "text"
}

func formatFact(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "sì"
		}
		return "no"
	case string:
		return x
	}
	return ""
}

// exampleOf mostra i valori di un fatto, accorciati.
func exampleOf(vals []any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		if s := formatFact(v); s != "" {
			parts = append(parts, s)
		}
	}
	s := strings.Join(parts, ", ")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}
	return s
}

func textValues(vals []any) []string {
	out := []string{}
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" && !slices.Contains(out, s) && len(out) < 20 {
			out = append(out, s)
		}
	}
	return out
}

// CatalogType è un tipo di elemento per cui si può creare un processo.
type CatalogType struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Module    string `json:"module"`
	Processes int    `json:"processes"`
}

// ProcessCatalog serve alla creazione di un processo: i tipi e i pacchetti di partenza.
type ProcessCatalog struct {
	Types    []CatalogType `json:"types"`
	Starters []Bundle      `json:"starters"`
}

// Catalog elenca i tipi di elemento e i pacchetti di partenza registrati dai moduli.
func (s *Service) Catalog(ctx context.Context, tx pgx.Tx, actor Actor) (*ProcessCatalog, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select t.key, t.name, t.module, (select count(*) from process_definitions p where p.type_id = t.id and not p.archived)
		from element_types t order by t.name`)
	if err != nil {
		return nil, err
	}
	c := &ProcessCatalog{}
	if c.Types, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogType]); err != nil {
		return nil, err
	}
	s.editor.mu.RLock()
	c.Starters = slices.Clone(s.editor.starters)
	s.editor.mu.RUnlock()
	if c.Starters == nil {
		c.Starters = []Bundle{}
	}
	if lang := i18n.Lang(ctx); lang != i18n.Default {
		for i := range c.Starters {
			c.Starters[i].Name = i18n.Name(lang, c.Starters[i].Name)
		}
	}
	slices.SortFunc(c.Starters, func(a, b Bundle) int { return strings.Compare(a.Name, b.Name) })
	return c, nil
}

// CheckDefinition valida una definizione senza salvarla: gli stessi controlli della
// pubblicazione (coerenza interna e riferimenti ai dati del tenant). L'editor la
// chiama a ogni modifica per mostrare i problemi dal vivo.
func (s *Service) CheckDefinition(ctx context.Context, tx pgx.Tx, actor Actor, ref string, raw []byte) ([]Problem, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	_, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	w, err := DecodeDefinition(raw)
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return []Problem{{Level: "error", Path: "definition", Msg: ve.Msg}}, nil
		}
		return nil, err
	}
	ps := Validate(w)
	refs, err := s.validateRefs(ctx, tx, w, typeID)
	if err != nil {
		return nil, err
	}
	return append(ps, refs...), nil
}

// FactsOfType elenca i fatti disponibili per un tipo di elemento (per i costruttori di
// condizioni fuori dall'editor dei processi, come quello dei questionari). Chi legge deve
// poter configurare: i valori d'esempio vengono da un elemento reale.
func (s *Service) FactsOfType(ctx context.Context, tx pgx.Tx, actor Actor, typeKey string) ([]FactInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	t, err := LoadType(ctx, tx, typeKey)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select key, name from stakeholder_roles order by name`)
	if err != nil {
		return nil, err
	}
	roles, err := pgx.CollectRows(rows, pgx.RowToStructByPos[NamedKey])
	if err != nil {
		return nil, err
	}
	out, _, err := s.availableFacts(ctx, tx, actor, t, roles)
	return out, err
}
