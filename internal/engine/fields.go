package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Campi personalizzati (sdlc.md §10, "Campi personalizzati e pagina di dettaglio").
//
// Forma salvata in elements.fields (fonte di verità), per tipo di dato:
//
//	string, text, url   testo (url: http o https, verificato)
//	integer, decimal    numero
//	duration            minuti interi (in ingresso anche "2g 3h 30m")
//	money               {"amount": 1200.5, "currency": "EUR"}
//	boolean             vero o falso
//	date                "AAAA-MM-GG"; datetime: RFC 3339 in UTC
//	enum, version       id dell'opzione (in ingresso anche il nome)
//	member, group       id del membro o del gruppo (gruppo anche per nome)
//	element             id dell'elemento (in ingresso anche la chiave DEM-12)
//
// I campi multipli salvano un elenco senza doppioni. L'indice tipizzato element_values
// (una riga per valore) si riscrive nella stessa transazione: v_num per numeri, importi,
// durate e booleani, v_ts per date, v_ref per opzioni, persone, gruppi ed elementi, v_text
// per i testi.

// FieldSettings sono i vincoli del campo (field_defs.settings).
type FieldSettings struct {
	// Types: per "element", chiavi dei tipi di elemento ammessi (vuoto = tutti).
	Types []string `json:"types,omitempty"`
	// Group: per "member", solo i membri di questo gruppo (per nome).
	Group string `json:"group,omitempty"`
	// Groups: per "group", gruppi ammessi (per nome; vuoto = tutti).
	Groups []string `json:"groups,omitempty"`
	// Currency: per "money", valuta proposta (ISO 4217); vuota = valuta base del tenant.
	Currency string `json:"currency,omitempty"`
	// Min e Max: limiti per numeri, importi e durate (in minuti).
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// HoursPerDay: per "duration", ore di una giornata lavorativa (default 8).
	HoursPerDay float64 `json:"hours_per_day,omitempty"`
}

// FieldDef è un campo personalizzato associato a un tipo, con le regole dell'associazione.
type FieldDef struct {
	ID          string
	Key         string
	Name        string
	DataType    string
	Multi       bool
	OptionSetID *string
	Required    bool
	Settings    FieldSettings
	Default     any
	Help        string
	Archived    bool
	Section     string
	VisibleWhen string
	// RequiredFrom: fase da cui il campo è obbligatorio (gate implicito sui passaggi).
	RequiredFrom string
	Ordinal      int
}

const fieldDefColumns = `f.id, f.key, f.name, f.data_type, f.is_multi, f.option_set_id, tf.required,
	f.settings, f.default_value, f.help, f.archived, tf.section, tf.visible_when, tf.required_from, tf.ordinal`

func scanFieldDef(row pgx.CollectableRow) (FieldDef, error) {
	var d FieldDef
	var settings, def []byte
	err := row.Scan(&d.ID, &d.Key, &d.Name, &d.DataType, &d.Multi, &d.OptionSetID, &d.Required,
		&settings, &def, &d.Help, &d.Archived, &d.Section, &d.VisibleWhen, &d.RequiredFrom, &d.Ordinal)
	if err != nil {
		return d, err
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &d.Settings)
	}
	if len(def) > 0 {
		_ = json.Unmarshal(def, &d.Default)
	}
	return d, nil
}

func loadTypeFields(ctx context.Context, tx pgx.Tx, typeID string) ([]FieldDef, error) {
	rows, err := tx.Query(ctx, `
		select `+fieldDefColumns+`
		from type_fields tf join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
		where tf.type_id = $1 and tf.space_id is null
		order by tf.ordinal, f.name`, typeID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanFieldDef)
}

// valueRow è una riga dell'indice tipizzato element_values.
type valueRow struct {
	fieldID string
	ord     int
	text    *string
	num     *float64
	ts      *time.Time
	ref     *string
}

// isEmptyValue: nil, testo vuoto o elenco vuoto valgono "nessun valore".
func isEmptyValue(v any) bool {
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

// normalize valida i valori in ingresso e restituisce la forma salvata in fields
// (opzioni e membri per id) più le righe dell'indice tipizzato.
// partial = true: aggiornamento, i campi assenti non si toccano e i required non si verificano.
func normalize(ctx context.Context, tx pgx.Tx, defs []FieldDef, current, in map[string]any, partial bool) (map[string]any, []valueRow, error) {
	byKey := make(map[string]FieldDef, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}
	out := map[string]any{}
	for k, v := range current {
		out[k] = v
	}
	nc := &normCtx{}
	for k, v := range in {
		d, ok := byKey[k]
		if !ok {
			return nil, nil, NotFound(k, "field", "campo inesistente per questo tipo")
		}
		if d.Archived {
			return nil, nil, invalid(k, "il campo «%s» è archiviato: non si modifica più", d.Name)
		}
		if isEmptyValue(v) {
			delete(out, k)
			continue
		}
		vals := []any{v}
		if d.Multi {
			arr, ok := v.([]any)
			if !ok {
				arr = []any{v} // un solo valore per un campo multiplo
			}
			vals = arr
			if len(vals) > 200 {
				return nil, nil, invalid(k, "troppi valori (massimo 200)")
			}
		} else if _, isList := v.([]any); isList {
			return nil, nil, invalid(k, "il campo «%s» accetta un solo valore", d.Name)
		}
		stored := make([]any, 0, len(vals))
		seen := map[string]bool{}
		for _, one := range vals {
			if isEmptyValue(one) {
				continue
			}
			sv, err := nc.normalizeOne(ctx, tx, d, one)
			if err != nil {
				return nil, nil, err
			}
			if sig := fmt.Sprint(sv); !seen[sig] {
				seen[sig] = true
				stored = append(stored, sv)
			}
		}
		switch {
		case len(stored) == 0:
			delete(out, k)
		case d.Multi:
			out[k] = stored
		default:
			out[k] = stored[0]
		}
	}
	if !partial {
		// Valori predefiniti per i campi non compilati.
		for _, d := range defs {
			if _, ok := out[d.Key]; ok || d.Archived || isEmptyValue(d.Default) {
				continue
			}
			if _, given := in[d.Key]; given {
				continue // svuotato di proposito
			}
			out[d.Key] = d.Default
		}
		// Gli obbligatori con visibilità condizionale si verificano sui passaggi di fase.
		for _, d := range defs {
			if _, ok := out[d.Key]; d.Required && !ok && !d.Archived && d.VisibleWhen == "" {
				return nil, nil, Required(d.Key, "campo obbligatorio: %s", d.Name)
			}
		}
	}
	return out, indexRows(defs, out), nil
}

// indexRows calcola le righe dell'indice per i campi associati al tipo.
func indexRows(defs []FieldDef, fields map[string]any) []valueRow {
	var rows []valueRow
	for _, d := range defs {
		v, ok := fields[d.Key]
		if !ok || v == nil {
			continue
		}
		vals, isList := v.([]any)
		if !isList {
			vals = []any{v}
		}
		for i, one := range vals {
			if r, ok := indexRow(d, i, one); ok {
				rows = append(rows, r)
			}
		}
	}
	return rows
}

// normCtx tiene i dati letti una volta per richiesta (valuta base del tenant).
type normCtx struct {
	baseCurrency string
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func (nc *normCtx) normalizeOne(ctx context.Context, tx pgx.Tx, d FieldDef, v any) (any, error) {
	switch d.DataType {
	case "string", "text":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve un testo")
		}
		max := 1000
		if d.DataType == "text" {
			max = 100_000
		}
		if utf8.RuneCountInString(s) > max {
			return nil, invalid(d.Key, "testo troppo lungo (massimo %d caratteri)", max)
		}
		return strings.TrimSpace(s), nil
	case "url":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve un indirizzo web")
		}
		s = strings.TrimSpace(s)
		if len(s) > 2000 {
			return nil, TooLong(d.Key, 2000, "indirizzo troppo lungo (massimo 2000 caratteri)")
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(s, " \t\n") {
			return nil, InvalidValue(d.Key, "indirizzo web non valido: scrivilo per intero, con http:// o https://")
		}
		return s, nil
	case "integer", "decimal":
		f, err := number(v)
		if err != nil {
			return nil, Required(d.Key, "serve un numero")
		}
		if d.DataType == "integer" && f != math.Trunc(f) {
			return nil, Required(d.Key, "serve un numero intero")
		}
		if err := d.inRange(f); err != nil {
			return nil, err
		}
		return f, nil
	case "duration":
		var mins float64
		switch x := v.(type) {
		case string:
			m, err := ParseDuration(x, d.Settings.hoursPerDay())
			if err != nil {
				return nil, invalid(d.Key, "%v", err)
			}
			mins = m
		default:
			f, err := number(v)
			if err != nil || f != math.Trunc(f) {
				return nil, Required(d.Key, "serve una durata in minuti o come «2g 3h 30m»")
			}
			mins = f
		}
		if mins < 0 {
			return nil, InvalidCode("validation.not_negative", d.Key, "la durata non può essere negativa")
		}
		if err := d.inRange(mins); err != nil {
			return nil, err
		}
		return mins, nil
	case "money":
		var amount float64
		currency := ""
		switch x := v.(type) {
		case map[string]any:
			f, err := number(x["amount"])
			if err != nil {
				return nil, Required(d.Key, "serve un importo")
			}
			amount = f
			if c, ok := x["currency"].(string); ok {
				currency = strings.ToUpper(strings.TrimSpace(c))
			}
		default:
			f, err := number(v)
			if err != nil {
				return nil, Required(d.Key, "serve un importo")
			}
			amount = f
		}
		if currency == "" {
			currency = d.Settings.Currency
		}
		if currency == "" {
			if nc.baseCurrency == "" {
				if err := tx.QueryRow(ctx, "select base_currency from tenants where id = app_tenant()").Scan(&nc.baseCurrency); err != nil {
					return nil, err
				}
			}
			currency = nc.baseCurrency
		}
		if !currencyRe.MatchString(currency) {
			return nil, InvalidValue(d.Key, "valuta non valida: codice ISO 4217 di tre lettere (es. EUR)")
		}
		amount = math.Round(amount*100) / 100
		if math.Abs(amount) >= 1e13 {
			return nil, invalid(d.Key, "importo fuori scala")
		}
		if err := d.inRange(amount); err != nil {
			return nil, err
		}
		return map[string]any{"amount": amount, "currency": currency}, nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, Required(d.Key, "serve vero o falso")
		}
		return b, nil
	case "date":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve una data AAAA-MM-GG")
		}
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return nil, DateInvalid(d.Key, "data non valida, formato AAAA-MM-GG")
		}
		return s, nil
	case "datetime":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve una data e ora")
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
		if err != nil {
			return nil, invalid(d.Key, "data e ora non valide: formato RFC 3339, es. 2026-11-24T10:30:00+01:00")
		}
		return t.UTC().Format(time.RFC3339), nil
	case "enum", "version":
		s, ok := v.(string)
		if !ok || d.OptionSetID == nil {
			return nil, Required(d.Key, "serve il nome di un'opzione")
		}
		var id string
		err := tx.QueryRow(ctx, `select id from options where option_set_id = $1 and not archived
			and (id::text = $2 or lower(name) = lower($2))`, *d.OptionSetID, strings.TrimSpace(s)).Scan(&id)
		if err != nil {
			return nil, NotFound(d.Key, "", "opzione %q inesistente", s)
		}
		return id, nil
	case "member":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve l'id di un membro")
		}
		var exists, inGroup bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from members where id::text = $1 and status = 'active'),
			$2 = '' or exists (select 1 from groups g, lateral group_effective_members(g.id) gm(member_id)
				where gm.member_id::text = $1 and lower(g.name) = lower($2))`, s, d.Settings.Group).Scan(&exists, &inGroup); err != nil {
			return nil, err
		}
		if !exists {
			return nil, invalidCode("validation.member_not_found", d.Key, "membro inesistente")
		}
		if !inGroup {
			return nil, Required(d.Key, "per «%s» serve un membro del gruppo %s", d.Name, d.Settings.Group)
		}
		return s, nil
	case "group":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve un gruppo")
		}
		var id, name string
		err := tx.QueryRow(ctx, `select id::text, name from groups where id::text = $1 or lower(name) = lower($1)
			order by (id::text = $1) desc limit 1`, strings.TrimSpace(s)).Scan(&id, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, NotFound(d.Key, "group", "gruppo %q inesistente", s)
		}
		if err != nil {
			return nil, err
		}
		if len(d.Settings.Groups) > 0 && !slices.ContainsFunc(d.Settings.Groups, func(g string) bool { return strings.EqualFold(g, name) }) {
			return nil, invalid(d.Key, "per «%s» sono ammessi solo i gruppi %s", d.Name, strings.Join(d.Settings.Groups, ", "))
		}
		return id, nil
	case "element":
		s, ok := v.(string)
		if !ok {
			return nil, Required(d.Key, "serve la chiave di un elemento, es. DEM-12")
		}
		s = strings.TrimSpace(s)
		var id, typeKey string
		var err error
		if m := keyRe.FindStringSubmatch(strings.ToUpper(s)); m != nil {
			n, _ := strconv.ParseInt(m[2], 10, 64)
			err = tx.QueryRow(ctx, `select e.id::text, t.key from elements e
				join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
				join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
				where sp.key = $1 and e.number = $2 and e.deleted_at is null and not e.is_draft`, m[1], n).Scan(&id, &typeKey)
		} else if uuidRe.MatchString(strings.ToLower(s)) {
			err = tx.QueryRow(ctx, `select e.id::text, t.key from elements e
				join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
				where e.id = $1 and e.deleted_at is null and not e.is_draft`, strings.ToLower(s)).Scan(&id, &typeKey)
		} else {
			return nil, invalid(d.Key, "«%s» non è una chiave: scrivi ad esempio DEM-12", s)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, NotFound(d.Key, "element", "elemento %s inesistente", s)
		}
		if err != nil {
			return nil, err
		}
		if len(d.Settings.Types) > 0 && !slices.Contains(d.Settings.Types, typeKey) {
			return nil, invalid(d.Key, "per «%s» sono ammessi solo elementi di tipo %s", d.Name, strings.Join(d.Settings.Types, ", "))
		}
		return id, nil
	default:
		return nil, invalid(d.Key, "tipo di campo %q non supportato per i valori degli elementi", d.DataType)
	}
}

func (d FieldDef) inRange(f float64) error {
	if d.Settings.Min != nil && f < *d.Settings.Min {
		return invalid(d.Key, "il valore minimo è %s", fmtNum(*d.Settings.Min))
	}
	if d.Settings.Max != nil && f > *d.Settings.Max {
		return invalid(d.Key, "il valore massimo è %s", fmtNum(*d.Settings.Max))
	}
	return nil
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func (s FieldSettings) hoursPerDay() float64 {
	if s.HoursPerDay > 0 && s.HoursPerDay <= 24 {
		return s.HoursPerDay
	}
	return 8
}

// number accetta un numero JSON o un testo numerico (con la virgola decimale italiana).
func number(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, errors.New("numero non valido")
		}
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		s := strings.TrimSpace(x)
		if strings.Contains(s, ",") && !strings.Contains(s, ".") {
			s = strings.ReplaceAll(s, ",", ".")
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, errors.New("numero non valido")
		}
		return f, nil
	}
	return 0, errors.New("numero non valido")
}

var durSplitRe = regexp.MustCompile(`([a-z])\s*(\d)`)

var durPartRe = regexp.MustCompile(`(?i)^(\d+(?:[.,]\d+)?)\s*(settimane|settimana|sett|w|giorni|giorno|gg|g|d|ore|ora|h|minuti|minuto|min|m)$`)

// ParseDuration legge una durata come «1s 2g 3h 30m» (settimane di 5 giorni, giorni di
// hoursPerDay ore) o un numero di minuti. Restituisce i minuti.
func ParseDuration(s string, hoursPerDay float64) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("durata vuota")
	}
	if f, err := number(s); err == nil {
		return math.Round(f), nil
	}
	// "2g3h" senza spazi: si separano le parti.
	s = durSplitRe.ReplaceAllString(s, "$1 $2")
	total := 0.0
	for _, p := range strings.Fields(s) {
		m := durPartRe.FindStringSubmatch(p)
		if m == nil {
			return 0, fmt.Errorf("durata non valida: «%s» (usa ad esempio 2g 3h 30m)", p)
		}
		n, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		switch u := m[2]; {
		case strings.HasPrefix(u, "sett") || u == "w":
			total += n * 5 * hoursPerDay * 60
		case strings.HasPrefix(u, "g") || u == "d":
			total += n * hoursPerDay * 60
		case strings.HasPrefix(u, "or") || u == "h":
			total += n * 60
		default:
			total += n
		}
	}
	return math.Round(total), nil
}

func indexRow(d FieldDef, ord int, v any) (valueRow, bool) {
	r := valueRow{fieldID: d.ID, ord: ord}
	switch d.DataType {
	case "integer", "duration", "decimal":
		f, err := number(v)
		if err != nil {
			return r, false
		}
		r.num = &f
	case "money":
		m, ok := v.(map[string]any)
		if !ok {
			return r, false
		}
		f, err := number(m["amount"])
		if err != nil {
			return r, false
		}
		r.num = &f
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return r, false
		}
		f := 0.0
		if b {
			f = 1
		}
		r.num = &f
	case "date":
		s, _ := v.(string)
		t, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return r, false
		}
		r.ts = &t
	case "datetime":
		s, _ := v.(string)
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return r, false
		}
		r.ts = &t
	case "enum", "version", "state", "member", "group", "element":
		s, ok := v.(string)
		if !ok || !uuidRe.MatchString(s) {
			return r, false
		}
		r.ref = &s
	default:
		s := fmt.Sprint(v)
		if d.DataType == "text" && utf8.RuneCountInString(s) > 500 {
			// L'indice serve a filtrare e ordinare: per i testi lunghi basta l'inizio.
			s = string([]rune(s)[:500])
		}
		r.text = &s
	}
	return r, true
}

// syncValues riscrive l'indice tipizzato dell'elemento per i campi del tipo (stessa
// transazione di fields). I valori dei campi tolti dal tipo restano, come in fields.
func syncValues(ctx context.Context, tx pgx.Tx, elementID string, defs []FieldDef, rows []valueRow) error {
	ids := make([]string, len(defs))
	for i, d := range defs {
		ids[i] = d.ID
	}
	if _, err := tx.Exec(ctx, "delete from element_values where element_id = $1 and field_id = any($2::uuid[])", elementID, ids); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, r := range rows {
		b.Queue(`insert into element_values (tenant_id, element_id, field_id, ord, v_text, v_num, v_ts, v_ref)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`, elementID, r.fieldID, r.ord, r.text, r.num, r.ts, r.ref)
	}
	return tx.SendBatch(ctx, b).Close()
}

// ---------------------------------------------------------------------
// Obbligatorietà per fase e visibilità condizionale

// mainPathIndex è la posizione della fase nel percorso principale (-1 = fuori percorso o inesistente).
func mainPathIndex(states []State, name string) int {
	i := 0
	for _, s := range states {
		if s.OffPath {
			continue
		}
		if strings.EqualFold(s.Name, name) {
			return i
		}
		i++
	}
	return -1
}

// requiredIn dice se il campo è obbligatorio quando l'elemento sta nella fase indicata.
// Le fasi fuori percorso (sospensioni, esiti negativi) non chiedono dati.
func (d FieldDef) requiredIn(states []State, phase string) bool {
	if d.Archived {
		return false
	}
	if len(states) == 0 {
		return d.Required
	}
	idx := mainPathIndex(states, phase)
	if idx < 0 {
		return false
	}
	if d.Required {
		return true
	}
	if d.RequiredFrom == "" {
		return false
	}
	from := mainPathIndex(states, d.RequiredFrom)
	return from >= 0 && idx >= from
}

// visibleWith valuta la condizione di visibilità (vuota = sempre visibile). Una condizione
// illeggibile (non dovrebbe accadere: si valida al salvataggio) lascia il campo visibile.
func (d FieldDef) visibleWith(f Facts) bool {
	if d.VisibleWhen == "" {
		return true
	}
	c, err := ParseCondition(d.VisibleWhen)
	if err != nil {
		return true
	}
	return c.Eval(f)
}

// factsInPhase copia i fatti con la fase sostituita (per valutare la visibilità nella fase di arrivo).
func factsInPhase(f Facts, phase State) Facts {
	out := make(Facts, len(f)+3)
	for k, v := range f {
		out[k] = v
	}
	out.Set("stato", phase.Name)
	out.Set("fase", phase.Name)
	out.Set("categoria", phase.Category)
	return out
}

// missingForPhase elenca i nomi dei campi obbligatori (e visibili) ancora vuoti per entrare
// nella fase indicata: è il gate implicito dei campi personalizzati.
func (s *Service) missingForPhase(ctx context.Context, tx pgx.Tx, el *Element, states []State, target State, lf *lazyFacts) ([]string, error) {
	defs, err := lf.fieldDefs(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []string
	var facts Facts
	for _, d := range defs {
		if !d.requiredIn(states, target.Name) || !isEmptyValue(el.Fields[d.Key]) {
			continue
		}
		if d.VisibleWhen != "" {
			if facts == nil {
				f, err := lf.get(ctx, tx)
				if err != nil {
					return nil, err
				}
				facts = factsInPhase(f, target)
			}
			if !d.visibleWith(facts) {
				continue
			}
		}
		out = append(out, d.Name)
	}
	return out, nil
}

// requiredMessage è il motivo del blocco per i campi mancanti.
func requiredMessage(phase string, names []string) string {
	return fmt.Sprintf("per passare a «%s» compila: %s", phase, strings.Join(names, ", "))
}
