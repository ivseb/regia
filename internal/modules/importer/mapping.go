package importer

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"regia/internal/engine"
)

// Mapping è la mappatura di un import: da cosa c'è nell'origine a cosa c'è in Regia.
// Si salva sull'esecuzione e, se l'utente vuole, come profilo (import_profiles, target
// work_items) da riusare con il prossimo export dello stesso strumento.
type Mapping struct {
	// Columns: ruolo di ogni colonna del CSV, nell'ordine dell'intestazione (solo CSV).
	Columns    []string    `json:"columns,omitempty"`
	DateFormat string      `json:"date_format"` // auto | iso | jira | dmy | dmy_dot | mdy | dmony
	Timezone   string      `json:"timezone,omitempty"`
	Space      SpaceTarget `json:"space"`
	// Types: tipo d'origine → epic | story | task | bug; "" = non importare.
	Types map[string]string `json:"types"`
	// States: tipo di Regia → stato d'origine → fase del processo del tipo.
	States map[string]map[string]string `json:"states"`
	Users  map[string]UserTarget        `json:"users"`
	// Fields: campo personalizzato d'origine → campo di Regia.
	Fields   map[string]FieldTarget  `json:"fields"`
	Priority ChoiceTarget            `json:"priority"`
	Labels   FieldTarget             `json:"labels"`
	Sprints  map[string]SprintTarget `json:"sprints"`
	// Links: tipo di collegamento d'origine → chiave del tipo di relazione; "" = non importare.
	Links   map[string]string `json:"links"`
	Include Include           `json:"include"`
}

// SpaceTarget: dove vanno gli elementi.
type SpaceTarget struct {
	Mode     string `json:"mode"` // existing | new
	Key      string `json:"key"`
	TeamName string `json:"team_name,omitempty"` // solo new: il team di cui è lo spazio
	// KeepNumbers: PROJ-123 resta PROJ-123 (spazio nuovo con la chiave del progetto).
	KeepNumbers bool `json:"keep_numbers,omitempty"`
}

// UserTarget: a chi corrisponde una persona dell'origine.
type UserTarget struct {
	Mode     string `json:"mode"` // member | placeholder | none
	MemberID string `json:"member_id,omitempty"`
}

// FieldTarget: dove va un campo d'origine.
type FieldTarget struct {
	Mode     string `json:"mode"` // existing | new | ignore
	Key      string `json:"key,omitempty"`
	Name     string `json:"name,omitempty"`
	DataType string `json:"data_type,omitempty"`
	Multi    bool   `json:"multi,omitempty"`
}

// ChoiceTarget: priorità → un campo a scelta; Values: valore d'origine → opzione
// (vuoto = si crea l'opzione con lo stesso nome).
type ChoiceTarget struct {
	Field  string            `json:"field"`
	Values map[string]string `json:"values"`
}

// SprintTarget: date e stato di uno sprint (dal CSV non arrivano: si scrivono qui).
type SprintTarget struct {
	Start string `json:"start,omitempty"` // AAAA-MM-GG
	End   string `json:"end,omitempty"`
	State string `json:"state"` // planned | active | closed
	Skip  bool   `json:"skip,omitempty"`
}

// Include: cosa si porta oltre agli elementi.
type Include struct {
	Comments    bool `json:"comments"`
	Worklogs    bool `json:"worklogs"`
	Attachments bool `json:"attachments"`
	Links       bool `json:"links"`
	Sprints     bool `json:"sprints"`
}

// WorkTypes sono i tipi di Regia di destinazione.
var WorkTypes = []string{"epic", "story", "task", "bug"}

// Problem è un errore della mappatura, da mostrare accanto alla sezione.
type Problem struct {
	Section string `json:"section"`
	Item    string `json:"item,omitempty"`
	Message string `json:"message"`
}

var fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var spaceKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)
var dayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// creatable: tipi di dato dei campi creati dall'import.
var creatable = []string{"string", "text", "integer", "decimal", "boolean", "date", "datetime", "duration", "url", "enum", "member"}

// check controlla la forma della mappatura (i riferimenti ai dati del tenant li controlla
// il piano). Non modifica nulla.
func (m *Mapping) check(header []string) []Problem {
	var out []Problem
	add := func(section, item, format string, args ...any) {
		out = append(out, Problem{Section: section, Item: item, Message: fmt.Sprintf(format, args...)})
	}
	if header != nil && m.Columns != nil {
		if len(m.Columns) != len(header) {
			add("columns", "", "le colonne della mappatura (%d) non corrispondono al file (%d)", len(m.Columns), len(header))
		}
		summary := false
		for i, r := range m.Columns {
			if !ValidRole(r) {
				name := ""
				if i < len(header) {
					name = header[i]
				}
				add("columns", name, "ruolo %q non valido", r)
			}
			summary = summary || r == RoleSummary
		}
		if !summary {
			add("columns", "", "serve la colonna del titolo")
		}
	}
	if m.DateFormat != "" && m.DateFormat != "auto" && !slices.ContainsFunc(DateFormats, func(f DateFormat) bool { return f.Key == m.DateFormat }) {
		add("date_format", "", "formato di data %q sconosciuto", m.DateFormat)
	}
	if m.Timezone != "" {
		if _, err := time.LoadLocation(m.Timezone); err != nil {
			add("timezone", "", "fuso orario %q sconosciuto", m.Timezone)
		}
	}
	switch m.Space.Mode {
	case "existing":
		if strings.TrimSpace(m.Space.Key) == "" {
			add("space", "", "scegli lo spazio di destinazione")
		}
		if m.Space.KeepNumbers {
			add("space", "", "la numerazione d'origine si conserva solo in uno spazio nuovo")
		}
	case "new":
		if !spaceKeyRe.MatchString(strings.ToUpper(strings.TrimSpace(m.Space.Key))) {
			add("space", "", "chiave dello spazio non valida: lettere maiuscole e cifre, inizia con una lettera, al massimo 20 caratteri")
		}
		if n := strings.TrimSpace(m.Space.TeamName); n == "" || len(n) > 120 {
			add("space", "", "serve il nome del team (al massimo 120 caratteri)")
		}
	default:
		add("space", "", "scegli uno spazio esistente o creane uno nuovo")
	}
	imported := 0
	for src, t := range m.Types {
		if t == "" {
			continue
		}
		imported++
		if !slices.Contains(WorkTypes, t) {
			add("types", src, "tipo di destinazione %q non ammesso (epic, story, task o bug)", t)
		}
	}
	if len(m.Types) > 0 && imported == 0 {
		add("types", "", "nessun tipo da importare")
	}
	for t := range m.States {
		if !slices.Contains(WorkTypes, t) {
			add("states", t, "tipo %q sconosciuto", t)
		}
	}
	for ref, u := range m.Users {
		switch u.Mode {
		case "member":
			if !engine.IsUUID(u.MemberID) {
				add("users", ref, "scegli il membro")
			}
		case "placeholder", "none":
		default:
			add("users", ref, "scelta %q non valida", u.Mode)
		}
	}
	newKeys := map[string]string{}
	checkField := func(section, src string, f FieldTarget) {
		switch f.Mode {
		case "ignore", "":
		case "existing":
			if f.Key == "" {
				add(section, src, "scegli il campo")
			}
		case "new":
			if !fieldKeyRe.MatchString(f.Key) {
				add(section, src, "chiave %q non valida: minuscole senza accenti, cifre e _", f.Key)
			}
			if prev, ok := newKeys[f.Key]; ok {
				add(section, src, "la chiave %q è già usata per %q", f.Key, prev)
			}
			newKeys[f.Key] = src
			if strings.TrimSpace(f.Name) == "" || len(f.Name) > 200 {
				add(section, src, "serve il nome del campo")
			}
			if !slices.Contains(creatable, f.DataType) {
				add(section, src, "tipo di dato %q non ammesso", f.DataType)
			}
			if f.Multi && f.DataType != "enum" && f.DataType != "string" && f.DataType != "member" && f.DataType != "url" {
				add(section, src, "il tipo %s non ammette più valori", f.DataType)
			}
		default:
			add(section, src, "scelta %q non valida", f.Mode)
		}
	}
	for src, f := range m.Fields {
		checkField("fields", src, f)
	}
	checkField("labels", "", m.Labels)
	if m.Labels.Mode == "new" && m.Labels.DataType != "enum" {
		add("labels", "", "le etichette vanno in un campo a scelta")
	}
	for name, sp := range m.Sprints {
		if sp.Skip {
			continue
		}
		if sp.State != "planned" && sp.State != "active" && sp.State != "closed" {
			add("sprints", name, "stato %q non valido (planned, active, closed)", sp.State)
		}
		for _, d := range []string{sp.Start, sp.End} {
			if d != "" && !dayRe.MatchString(d) {
				add("sprints", name, "data %q non valida (AAAA-MM-GG)", d)
			}
		}
		if sp.Start != "" && sp.End != "" && sp.End < sp.Start {
			add("sprints", name, "la fine viene prima dell'inizio")
		}
	}
	return out
}

func (m *Mapping) location() *time.Location {
	if m.Timezone != "" {
		if l, err := time.LoadLocation(m.Timezone); err == nil {
			return l
		}
	}
	l, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.UTC
	}
	return l
}
