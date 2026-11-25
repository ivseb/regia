package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Campi del linguaggio di query: di sistema (colonne di elements e tabelle del motore),
// offerti dai moduli (QueryField) e personalizzati (field_defs, per chiave o alias).
// I nomi di sistema hanno la precedenza su quelli personalizzati.

// Tipi di valore dei campi di query.
const (
	qkType    = "type"    // tipo di elemento
	qkState   = "state"   // fase del processo
	qkSpace   = "space"   // spazio
	qkMember  = "member"  // persona (anche "me")
	qkKey     = "key"     // chiave leggibile DEM-12
	qkNumber  = "number"  // numero
	qkDate    = "date"    // data
	qkElement = "element" // riferimento a un elemento (genitore)
	qkWatch   = "watch"   // elementi seguiti da una persona
	qkText    = "text"    // testo
	qkBool    = "bool"    // sì / no
	qkEnum    = "enum"    // opzione di un elenco
	qkRef     = "ref"     // riferimento di un modulo (team, applicazione, asset, richiesta)
	qkGroup   = "group"   // gruppo (campi personalizzati)
)

// QueryField è un campo di query offerto da un modulo. Il motore compone gli SQL con i
// filtri e la strategia di prestazioni (filtri risolti in id o predicati sull'indice).
type QueryField struct {
	Key   string   // chiave canonica, es. "team"
	Names []string // nomi accettati (italiano e inglese); il primo è quello proposto
	Label string   // descrizione per l'autocompletamento
	Kind  string   // "ref" (valori risolti in id) | "date"
	// Source è una sottoquery con le colonne (element_id, v): per ogni elemento i valori
	// del campo. v è un uuid per "ref", un timestamptz (o date, con DateOnly) per "date".
	// Deve usare colonne indicizzate su element_id e su v: le condizioni si spingono
	// dentro la sottoquery (anche dentro i rami di una union all).
	Source string
	// DateOnly: v è di tipo date (le date si confrontano come giorni, con l'indice).
	DateOnly bool
	// RefType: i valori sono elementi di questo tipo, per chiave (APP-3).
	RefType string
	// RefTitles: anche per titolo esatto (solo per tipi con pochi elementi: si scorre il tipo).
	RefTitles bool
	// Lookup risolve i nomi di oggetti che non sono elementi (o codici alternativi):
	// "select id::text, nome from ... where lower(nome) = any($1)" ($1 in minuscolo).
	Lookup string
	// Suggest propone valori: "select valore, dettaglio from ... where valore ilike $1 order by ... limit $2".
	Suggest string
	// Types: i tipi di elemento a cui il campo si applica (vuoto = tutti). Il filtro visuale
	// propone in cima i campi del tipo della lista.
	Types []string
}

// QueryField registra un campo di query di un modulo.
func (s *Service) QueryField(f QueryField) { s.queryFields = append(s.queryFields, f) }

type sysField struct {
	key, label string
	names      []string
	kind       string
	col        string // colonna di elements
	nullable   bool
	sortable   bool
	asc        bool // direzione predefinita dell'ordinamento
	indexed    bool // il filtro si risolve in id con un indice
}

var sysFields = []sysField{
	{key: "type", names: []string{"tipo", "type"}, label: "Tipo di elemento", kind: qkType, col: "e.type_id"},
	{key: "state", names: []string{"fase", "stato", "state", "status", "phase"}, label: "Fase del processo (anche aperta, chiusa)", kind: qkState, col: "e.state_id", nullable: true, sortable: true, asc: true},
	{key: "space", names: []string{"spazio", "space", "progetto", "project"}, label: "Spazio", kind: qkSpace, col: "e.space_id"},
	{key: "key", names: []string{"chiave", "key"}, label: "Chiave, es. DEM-12 o DEM-1..DEM-50", kind: qkKey, col: "e.number", sortable: true, indexed: true},
	{key: "number", names: []string{"numero", "number", "id"}, label: "Numero nello spazio", kind: qkNumber, col: "e.number", sortable: true, indexed: true},
	{key: "title", names: []string{"titolo", "title"}, label: "Titolo (solo per ordinare: per cercare usa il testo libero)", kind: qkText, col: "e.title", sortable: true, asc: true},
	{key: "assignee", names: []string{"assegnatario", "assegnato", "assignee"}, label: "Assegnatario (anche me)", kind: qkMember, col: "e.assignee_id", nullable: true, indexed: true},
	{key: "reporter", names: []string{"segnalatore", "autore", "reporter", "author"}, label: "Chi l'ha aperta (anche me)", kind: qkMember, col: "e.reporter_id", nullable: true, indexed: true},
	{key: "parent", names: []string{"genitore", "parent"}, label: "Elemento genitore, es. DEM-12", kind: qkElement, col: "e.parent_id", nullable: true, indexed: true},
	{key: "updated", names: []string{"aggiornata", "aggiornato", "aggiornamento", "updated"}, label: "Ultimo aggiornamento", kind: qkDate, col: "e.updated_at", sortable: true, indexed: true},
	{key: "created", names: []string{"creata", "creato", "creazione", "created"}, label: "Creazione", kind: qkDate, col: "e.created_at", sortable: true, indexed: true},
	{key: "resolved", names: []string{"risolta", "risolto", "chiusura", "resolved"}, label: "Chiusura", kind: qkDate, col: "e.resolved_at", nullable: true, sortable: true, indexed: true},
	{key: "start", names: []string{"inizio", "start"}, label: "Inizio pianificato", kind: qkDate, col: "e.start_at", nullable: true, sortable: true, asc: true, indexed: true},
	{key: "end", names: []string{"fine", "end"}, label: "Fine pianificata", kind: qkDate, col: "e.due_at", nullable: true, sortable: true, asc: true, indexed: true},
	{key: "watched", names: []string{"osservati", "osservata", "osservato", "seguiti", "seguita", "seguito", "watched", "watching"}, label: "Elementi che una persona segue (osservati: me)", kind: qkWatch},
}

// customField è un campo personalizzato del tenant.
type customField struct {
	ID, Key, Name, DataType string
	OptionSetID             *string
	Aliases                 []string
	Multi                   bool
	Settings                FieldSettings
}

// qkind dice come si filtra un campo personalizzato; "" = non filtrabile.
func (c customField) qkind() string {
	switch c.DataType {
	case "string", "text", "url":
		return qkText
	case "integer", "decimal", "money", "duration":
		return qkNumber
	case "boolean":
		return qkBool
	case "date", "datetime":
		return qkDate
	case "enum", "state", "version":
		return qkEnum
	case "member":
		return qkMember
	case "group":
		return qkGroup
	case "element":
		return qkElement
	}
	return ""
}

// col è la colonna di element_values con il valore.
func (c customField) col() string {
	switch c.qkind() {
	case qkNumber, qkBool:
		return "v_num"
	case qkDate:
		return "v_ts"
	case qkEnum, qkMember, qkGroup, qkElement:
		return "v_ref"
	}
	return "v_text"
}

// qfield è un campo risolto per nome.
type qfield struct {
	key    string // canonico
	name   string // nome principale da proporre
	label  string
	kind   string
	sys    *sysField
	mod    *QueryField
	custom *customField
}

func (f *qfield) sortable() bool {
	switch {
	case f.sys != nil:
		return f.sys.sortable
	case f.custom != nil:
		k := f.custom.qkind()
		return k == qkNumber || k == qkDate || k == qkText || k == qkBool || k == qkEnum
	}
	return false
}

func (f *qfield) nullable() bool { return f.sys == nil || f.sys.nullable }

// catalog sono i campi disponibili nel tenant, per nome in minuscolo.
type catalog struct {
	fields []*qfield // nell'ordine di proposta: sistema, moduli, personalizzati
	byName map[string]*qfield
}

func (c *catalog) lookup(name string) *qfield {
	return c.byName[strings.ToLower(strings.TrimSpace(name))]
}

func (s *Service) catalog(ctx context.Context, tx pgx.Tx) (*catalog, error) {
	c := &catalog{byName: map[string]*qfield{}}
	// soft: nomi secondari dei campi dei moduli (per esempio «tag» per le etichette). Cedono
	// alla chiave di un campo personalizzato del tenant, che altrimenti non si potrebbe più
	// interrogare dopo l'arrivo di un nuovo campo di modulo con lo stesso nome.
	soft := map[string]bool{}
	add := func(f *qfield, names []string) {
		c.fields = append(c.fields, f)
		for i, n := range names {
			n = strings.ToLower(n)
			_, taken := c.byName[n]
			if !taken || (soft[n] && f.custom != nil && i == 0) {
				c.byName[n] = f
				soft[n] = f.mod != nil && i > 0
			}
		}
	}
	for i := range sysFields {
		sf := &sysFields[i]
		add(&qfield{key: sf.key, name: sf.names[0], label: sf.label, kind: sf.kind, sys: sf}, sf.names)
	}
	for i := range s.queryFields {
		mf := &s.queryFields[i]
		add(&qfield{key: mf.Key, name: mf.Names[0], label: mf.Label, kind: mf.Kind, mod: mf}, mf.Names)
	}
	rows, err := tx.Query(ctx, `select id::text, key, name, data_type, option_set_id::text, aliases, is_multi, settings from field_defs order by key`)
	if err != nil {
		return nil, err
	}
	defs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (customField, error) {
		var c customField
		var settings []byte
		err := r.Scan(&c.ID, &c.Key, &c.Name, &c.DataType, &c.OptionSetID, &c.Aliases, &c.Multi, &settings)
		if len(settings) > 0 {
			_ = json.Unmarshal(settings, &c.Settings)
		}
		return c, err
	})
	if err != nil {
		return nil, err
	}
	for i := range defs {
		cf := &defs[i]
		k := cf.qkind()
		if k == "" {
			continue
		}
		names := append([]string{cf.Key}, cf.Aliases...)
		add(&qfield{key: cf.Key, name: cf.Key, label: cf.Name, kind: k, custom: cf}, names)
	}
	return c, nil
}

// FieldInfo descrive un campo per l'interfaccia (colonne della tabella, autocompletamento).
type FieldInfo struct {
	Key      string       `json:"key"`
	Name     string       `json:"name"`  // nome da usare nella query
	Label    string       `json:"label"` // descrizione
	Kind     string       `json:"kind"`
	Source   string       `json:"source"` // system | module | custom
	DataType string       `json:"data_type,omitempty"`
	Sortable bool         `json:"sortable"`
	Multi    bool         `json:"multi,omitempty"`
	Options  []OptionInfo `json:"options,omitempty"`
	// Types: tipi a cui il campo si applica (assente = tutti i tipi).
	Types []string `json:"types,omitempty"`
}

// OptionInfo è un'opzione di un campo a elenco; Color è un token del tema (o vuoto).
type OptionInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// QueryFields elenca i campi interrogabili del tenant, con le opzioni degli elenchi.
func (s *Service) QueryFields(ctx context.Context, tx pgx.Tx) ([]FieldInfo, error) {
	c, err := s.catalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select option_set_id::text, id::text, name, coalesce(color, '') from options where not archived order by option_set_id, ordinal, lower(name)`)
	if err != nil {
		return nil, err
	}
	opts := map[string][]OptionInfo{}
	for rows.Next() {
		var set string
		var o OptionInfo
		if err := rows.Scan(&set, &o.ID, &o.Name, &o.Color); err != nil {
			rows.Close()
			return nil, err
		}
		opts[set] = append(opts[set], o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Tipi a cui è associato ciascun campo personalizzato.
	trows, err := tx.Query(ctx, `select f.key, t.key from type_fields tf
		join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
		join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id
		where tf.space_id is null order by f.key, t.key`)
	if err != nil {
		return nil, err
	}
	fieldTypes := map[string][]string{}
	for trows.Next() {
		var fk, tk string
		if err := trows.Scan(&fk, &tk); err != nil {
			trows.Close()
			return nil, err
		}
		fieldTypes[fk] = append(fieldTypes[fk], tk)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, err
	}
	out := make([]FieldInfo, 0, len(c.fields))
	for _, f := range c.fields {
		fi := FieldInfo{Key: f.key, Name: f.name, Label: f.label, Kind: f.kind, Sortable: f.sortable()}
		switch {
		case f.sys != nil:
			fi.Source = "system"
		case f.mod != nil:
			fi.Source, fi.Types = "module", f.mod.Types
		default:
			fi.Source, fi.DataType, fi.Multi = "custom", f.custom.DataType, f.custom.Multi
			fi.Types = fieldTypes[f.key]
			if f.custom.OptionSetID != nil {
				fi.Options = opts[*f.custom.OptionSetID]
			}
		}
		out = append(out, fi)
	}
	return out, nil
}
