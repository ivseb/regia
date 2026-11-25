package finance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Import CSV delle voci di costo (demand.md §10): profili di mappatura salvati, prova senza
// salvare con il report degli errori riga per riga, conferma dello stesso file (sha256) e
// annullamento. Reimportare lo stesso file non duplica: ogni riga ha un riferimento
// (colonna mappata su source_ref, oppure l'impronta dei suoi dati) e le righe già presenti
// sono riconosciute come duplicati.

// Campi di destinazione di una colonna.
var importFields = map[string]string{
	"element":       "elemento (chiave, es. DEM-12)",
	"entry_date":    "data",
	"amount":        "importo",
	"currency":      "valuta",
	"category":      "categoria (codice o nome)",
	"kind":          "tipo (consuntivo/impegnato)",
	"source_ref":    "riferimento (numero del documento)",
	"description":   "descrizione",
	"supplier":      "fornitore",
	"org_unit":      "centro di costo",
	"capitalizable": "capitalizzabile (sì/no)",
}

// ImportFields sono i campi a cui si può mappare una colonna, con l'etichetta.
func ImportFields() map[string]string { return importFields }

// Mapping è la mappatura di un profilo: colonne del file → campi, formati e valori di default.
type Mapping struct {
	Columns    map[string]string `json:"columns"`     // intestazione → campo
	Delimiter  string            `json:"delimiter"`   // "," ";" "\t"; vuoto = automatico
	DateFormat string            `json:"date_format"` // AAAA-MM-GG | GG/MM/AAAA | GG.MM.AAAA | MM/GG/AAAA
	Decimal    string            `json:"decimal"`     // "," (default) o "."
	Defaults   map[string]string `json:"defaults"`    // campo → valore se la colonna manca o è vuota
}

var dateLayouts = map[string]string{"": "2006-01-02", "AAAA-MM-GG": "2006-01-02", "GG/MM/AAAA": "02/01/2006",
	"GG.MM.AAAA": "02.01.2006", "MM/GG/AAAA": "01/02/2006", "GG-MM-AAAA": "02-01-2006"}

func (m *Mapping) validate() error {
	if len(m.Columns) == 0 {
		return engine.InvalidCode("validation.finance.mapping_no_columns", "mapping.columns", "mappa almeno una colonna")
	}
	seen := map[string]bool{}
	for col, f := range m.Columns {
		if _, ok := importFields[f]; !ok {
			return engine.InvalidValue("mapping.columns", "campo %q sconosciuto per la colonna %q", f, col)
		}
		if seen[f] {
			return engine.InvalidParams("validation.finance.mapping_column_twice", "mapping.columns", map[string]any{"field": f}, "il campo %q è mappato due volte", f)
		}
		seen[f] = true
	}
	for f := range m.Defaults {
		if _, ok := importFields[f]; !ok {
			return engine.InvalidValue("mapping.defaults", "campo %q sconosciuto", f)
		}
	}
	for _, need := range []string{"entry_date", "amount"} {
		if !seen[need] {
			return engine.Required("mapping.columns", "manca la colonna per il campo %q", importFields[need])
		}
	}
	if !seen["element"] && m.Defaults["element"] == "" {
		return engine.Required("mapping.columns", "serve la colonna dell'elemento o un elemento di default")
	}
	if !seen["category"] && m.Defaults["category"] == "" {
		return engine.Required("mapping.columns", "serve la colonna della categoria o una categoria di default")
	}
	if _, ok := dateLayouts[m.DateFormat]; !ok {
		return engine.InvalidValue("mapping.date_format", "formato di data non ammesso")
	}
	if m.Decimal != "" && m.Decimal != "," && m.Decimal != "." {
		return engine.InvalidValue("mapping.decimal", "separatore decimale non ammesso")
	}
	if len([]rune(m.Delimiter)) > 1 {
		return engine.InvalidCode("validation.finance.mapping_delimiter", "mapping.delimiter", "il separatore è un solo carattere")
	}
	return nil
}

// ---------------------------------------------------------------------
// Profili
// ---------------------------------------------------------------------

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Target    string    `json:"target"`
	Mapping   Mapping   `json:"mapping"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Service) Profiles(ctx context.Context, tx pgx.Tx) ([]Profile, error) {
	rows, err := tx.Query(ctx, "select id, name, target, mapping, updated_at from import_profiles where target = 'cost_entries' order by lower(name)")
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Profile, error) {
		var p Profile
		var raw []byte
		if err := r.Scan(&p.ID, &p.Name, &p.Target, &raw, &p.UpdatedAt); err != nil {
			return p, err
		}
		return p, json.Unmarshal(raw, &p.Mapping)
	})
	if out == nil {
		out = []Profile{}
	}
	return out, err
}

func (s *Service) profile(ctx context.Context, tx pgx.Tx, id string) (*Profile, error) {
	var p Profile
	var raw []byte
	err := tx.QueryRow(ctx, "select id, name, target, mapping, updated_at from import_profiles where id::text = $1", id).
		Scan(&p.ID, &p.Name, &p.Target, &raw, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.NotFound("profile_id", "profile", "profilo inesistente")
	}
	if err != nil {
		return nil, err
	}
	return &p, json.Unmarshal(raw, &p.Mapping)
}

type ProfileInput struct {
	Name    string  `json:"name"`
	Mapping Mapping `json:"mapping"`
}

// SaveProfile crea (id vuoto) o sostituisce un profilo di import.
func (s *Service) SaveProfile(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ProfileInput) (*Profile, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, engine.Required("name", "serve un nome")
	}
	if err := in.Mapping.validate(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(in.Mapping)
	action := "import_profile.update"
	var err error
	if id == "" {
		id, action = db.NewID(), "import_profile.create"
		_, err = tx.Exec(ctx, `insert into import_profiles (tenant_id, id, name, target, mapping, created_by) values (app_tenant(), $1, $2, 'cost_entries', $3, $4)`,
			id, in.Name, raw, nullable(actor.MemberID))
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `update import_profiles set name = $2, mapping = $3, updated_at = now() where id::text = $1`, id, in.Name, raw)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, engine.ErrNotFound
		}
	}
	if err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("name", "esiste già un profilo con questo nome")
		}
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "import_profile", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.profile(ctx, tx, id)
}

func (s *Service) DeleteProfile(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireWrite(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from import_profiles where id::text = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audit.Append(ctx, tx, audit.Event{Action: "import_profile.delete", EntityType: "import_profile", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

// ---------------------------------------------------------------------
// Analisi del file
// ---------------------------------------------------------------------

// RowError è un errore di una riga (la riga 1 è l'intestazione).
type RowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// PreviewRow è una riga valida come verrà importata.
type PreviewRow struct {
	Row             int    `json:"row"`
	ElementKey      string `json:"element_key"`
	EntryDate       string `json:"entry_date"`
	AmountCents     int64  `json:"amount_cents"`
	Currency        string `json:"currency"`
	AmountBaseCents int64  `json:"amount_base_cents"`
	Category        string `json:"category"`
	Kind            string `json:"kind"`
	Description     string `json:"description"`
	SourceRef       string `json:"source_ref"`
	Duplicate       bool   `json:"duplicate"`
}

type parsedRow struct {
	PreviewRow
	elementID, categoryID string
	orgUnitID             *string
	supplier              *string
	fxRate                string
	date                  time.Time
	capitalizable         bool
}

type analysis struct {
	total, duplicates int
	rows              []parsedRow // valide, duplicati compresi
	errors            []RowError
}

const maxImportRows = 50_000

func (s *Service) analyze(ctx context.Context, tx pgx.Tx, actor engine.Actor, m Mapping, content string) (*analysis, error) {
	content = strings.TrimPrefix(content, string(rune(0xFEFF)))
	delim := m.Delimiter
	if delim == "" {
		first, _, _ := strings.Cut(content, "\n")
		delim = ","
		if strings.Count(first, ";") > strings.Count(first, ",") {
			delim = ";"
		} else if strings.Count(first, "\t") > strings.Count(first, ",") {
			delim = "\t"
		}
	}
	rd := csv.NewReader(strings.NewReader(content))
	rd.Comma = []rune(delim)[0]
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	header, err := rd.Read()
	if err != nil {
		return nil, engine.InvalidCode("validation.finance.csv_unreadable", "csv", "file vuoto o non leggibile come CSV")
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	fieldCol := map[string]int{}
	for h, f := range m.Columns {
		i, ok := col[strings.ToLower(strings.TrimSpace(h))]
		if !ok {
			return nil, engine.InvalidParams("validation.finance.mapping_column_missing", "mapping.columns", map[string]any{"column": h}, "nel file manca la colonna %q", h)
		}
		fieldCol[f] = i
	}
	base, err := BaseCurrency(ctx, tx)
	if err != nil {
		return nil, err
	}
	// Tabelle di riferimento caricate una volta.
	type refRow struct {
		ID, Name string
		Code     *string
	}
	rows, err := tx.Query(ctx, "select id, name, code from cost_categories where active")
	if err != nil {
		return nil, err
	}
	catRows, err := pgx.CollectRows(rows, pgx.RowToStructByPos[refRow])
	if err != nil {
		return nil, err
	}
	cats := map[string][2]string{} // codice o nome minuscolo → id, nome
	for _, c := range catRows {
		cats[strings.ToLower(c.Name)] = [2]string{c.ID, c.Name}
		if c.Code != nil {
			cats[strings.ToLower(*c.Code)] = [2]string{c.ID, c.Name}
		}
	}
	rows, err = tx.Query(ctx, "select id, name, null::text from org_units")
	if err != nil {
		return nil, err
	}
	unitRows, err := pgx.CollectRows(rows, pgx.RowToStructByPos[refRow])
	if err != nil {
		return nil, err
	}
	units := map[string]string{}
	for _, u := range unitRows {
		units[strings.ToLower(u.Name)] = u.ID
	}
	elements := map[string]*engine.Element{}
	fx := map[string]string{} // valuta|data → errore o ""

	a := &analysis{}
	refs := map[string]bool{}
	layout := dateLayouts[m.DateFormat]
	for line := 2; ; line++ {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			a.errors = append(a.errors, RowError{Row: line, Message: "riga non leggibile: " + err.Error()})
			a.total++
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue // riga vuota
		}
		a.total++
		if a.total > maxImportRows {
			return nil, engine.InvalidParams("validation.finance.csv_too_many_rows", "csv", map[string]any{"max": maxImportRows}, "al massimo %d righe per file", maxImportRows)
		}
		get := func(f string) string {
			if i, ok := fieldCol[f]; ok && i < len(rec) {
				if v := strings.TrimSpace(rec[i]); v != "" {
					return v
				}
			}
			return strings.TrimSpace(m.Defaults[f])
		}
		fail := func(field, msg string) {
			a.errors = append(a.errors, RowError{Row: line, Field: field, Message: msg})
		}
		var p parsedRow
		p.Row = line
		ok := true
		// Elemento
		key := strings.ToUpper(get("element"))
		el, seen := elements[key]
		if !seen {
			if e, err := s.eng.Get(ctx, tx, actor, key); err == nil {
				el = e
			} else if !errors.Is(err, engine.ErrNotFound) {
				return nil, err
			}
			elements[key] = el
		}
		if el == nil {
			fail("element", fmt.Sprintf("elemento %q inesistente o non visibile", key))
			ok = false
		} else {
			p.elementID, p.ElementKey = el.ID, el.Key
		}
		// Data
		if d, err := time.Parse(layout, get("entry_date")); err != nil {
			fail("entry_date", fmt.Sprintf("data %q non valida (formato %s)", get("entry_date"), dateFormatName(m.DateFormat)))
			ok = false
		} else {
			p.date, p.EntryDate = d, d.Format(time.DateOnly)
		}
		// Importo
		if c, err := parseAmount(get("amount"), m.Decimal); err != nil {
			fail("amount", err.Error())
			ok = false
		} else {
			p.AmountCents = c
		}
		// Valuta
		if cur, err := normCurrency(get("currency"), base); err != nil {
			fail("currency", "valuta non valida")
			ok = false
		} else {
			p.Currency = cur
		}
		// Categoria
		if c, found := cats[strings.ToLower(get("category"))]; found {
			p.categoryID, p.Category = c[0], c[1]
		} else {
			fail("category", fmt.Sprintf("categoria %q inesistente", get("category")))
			ok = false
		}
		// Tipo
		switch strings.ToLower(get("kind")) {
		case "", "actual", "consuntivo", "fattura", "speso":
			p.Kind = "actual"
		case "committed", "impegnato", "impegno", "ordine":
			p.Kind = "committed"
		default:
			fail("kind", fmt.Sprintf("tipo %q non riconosciuto (consuntivo o impegnato)", get("kind")))
			ok = false
		}
		if u := get("org_unit"); u != "" {
			if id, found := units[strings.ToLower(u)]; found {
				p.orgUnitID = &id
			} else {
				fail("org_unit", fmt.Sprintf("centro di costo %q inesistente", u))
				ok = false
			}
		}
		switch strings.ToLower(get("capitalizable")) {
		case "", "no", "n", "false", "0":
		case "si", "sì", "s", "yes", "y", "true", "1":
			p.capitalizable = true
		default:
			fail("capitalizable", "valore non riconosciuto (sì o no)")
			ok = false
		}
		p.Description = get("description")
		if v := get("supplier"); v != "" {
			p.supplier = &v
		}
		if !ok {
			continue
		}
		// Conversione: un controllo per valuta e data.
		k := p.Currency + "|" + p.EntryDate
		if msg, done := fx[k]; done && msg != "" {
			fail("currency", msg)
			continue
		}
		base2, rate, err := convert(ctx, tx, p.AmountCents, p.Currency, base, p.date)
		if errors.Is(err, ErrNoRate) {
			fx[k] = err.Error()
			fail("currency", err.Error())
			continue
		}
		if err != nil {
			return nil, err
		}
		fx[k] = ""
		p.AmountBaseCents, p.fxRate = base2, rate
		// Riferimento per l'idempotenza.
		if ref := get("source_ref"); ref != "" {
			p.SourceRef = "ref:" + ref
		} else {
			sum := sha256.Sum256([]byte(strings.Join([]string{p.elementID, p.EntryDate, fmt.Sprint(p.AmountCents), p.Currency,
				p.categoryID, p.Kind, p.Description, get("supplier")}, "\x1f")))
			p.SourceRef = "sha:" + hex.EncodeToString(sum[:16])
		}
		if refs[p.SourceRef] {
			p.Duplicate = true
		}
		refs[p.SourceRef] = true
		a.rows = append(a.rows, p)
	}
	// Righe già importate (anche da file precedenti).
	if len(a.rows) > 0 {
		all := make([]string, 0, len(refs))
		for r := range refs {
			all = append(all, r)
		}
		rows, err := tx.Query(ctx, "select source_ref from cost_entries where source = 'import' and source_ref = any($1)", all)
		if err != nil {
			return nil, err
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		existing := map[string]bool{}
		for _, r := range found {
			existing[r] = true
		}
		for i := range a.rows {
			if existing[a.rows[i].SourceRef] {
				a.rows[i].Duplicate = true
			}
		}
	}
	for _, r := range a.rows {
		if r.Duplicate {
			a.duplicates++
		}
	}
	return a, nil
}

func dateFormatName(f string) string {
	if f == "" {
		return "AAAA-MM-GG"
	}
	return f
}

// parseAmount legge un importo con il separatore decimale indicato ("," di default) e
// restituisce i centesimi. Ammessi i separatori delle migliaia, il segno e le parentesi.
func parseAmount(v, decimal string) (int64, error) {
	if decimal == "" {
		decimal = ","
	}
	orig := v
	v = strings.NewReplacer(" ", "", " ", "", "€", "", "'", "").Replace(v)
	neg := false
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		neg, v = true, v[1:len(v)-1]
	}
	if decimal == "," {
		v = strings.ReplaceAll(v, ".", "")
		v = strings.ReplaceAll(v, ",", ".")
	} else {
		v = strings.ReplaceAll(v, ",", "")
	}
	r, ok := new(big.Rat).SetString(v)
	if v == "" || !ok {
		return 0, fmt.Errorf("importo %q non valido", orig)
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() {
		// Oltre i centesimi si arrotonda (mezzo lontano da zero).
		f, _ := r.Float64()
		if f < 0 {
			f -= 0.5
		} else {
			f += 0.5
		}
		r = new(big.Rat).SetInt64(int64(f))
	}
	if !r.Num().IsInt64() {
		return 0, fmt.Errorf("importo %q troppo grande", orig)
	}
	c := r.Num().Int64()
	if neg {
		c = -c
	}
	if c == 0 {
		return 0, fmt.Errorf("importo %q nullo", orig)
	}
	return c, nil
}

// ---------------------------------------------------------------------
// Esecuzioni: prova, conferma, annullamento
// ---------------------------------------------------------------------

type Run struct {
	ID         string       `json:"id"`
	ProfileID  *string      `json:"profile_id"`
	Profile    *string      `json:"profile"`
	FileName   string       `json:"file_name"`
	Status     string       `json:"status"` // preview | done | rolled_back | failed
	RowsTotal  int          `json:"rows_total"`
	RowsOK     int          `json:"rows_ok"`
	RowsError  int          `json:"rows_error"`
	RowsDup    int          `json:"rows_duplicate"`
	Errors     []RowError   `json:"errors"`
	Preview    []PreviewRow `json:"preview"`
	DryRun     bool         `json:"dry_run"`
	CreatedBy  *string      `json:"created_by"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at"`
}

const runSelect = `select r.id, r.profile_id, p.name, r.file_name, r.status, coalesce(r.rows_total, 0), coalesce(r.rows_ok, 0),
	coalesce(r.rows_error, 0), r.rows_duplicate, r.errors, r.preview, r.dry_run, m.display_name, r.created_at, r.finished_at
	from import_runs r left join import_profiles p on p.tenant_id = r.tenant_id and p.id = r.profile_id
	left join members m on m.tenant_id = r.tenant_id and m.id = r.created_by`

func scanRuns(rows pgx.Rows) ([]Run, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) {
		var x Run
		var errs, prev []byte
		if err := r.Scan(&x.ID, &x.ProfileID, &x.Profile, &x.FileName, &x.Status, &x.RowsTotal, &x.RowsOK, &x.RowsError, &x.RowsDup,
			&errs, &prev, &x.DryRun, &x.CreatedBy, &x.CreatedAt, &x.FinishedAt); err != nil {
			return x, err
		}
		if err := json.Unmarshal(errs, &x.Errors); err != nil {
			return x, err
		}
		return x, json.Unmarshal(prev, &x.Preview)
	})
	if out == nil {
		out = []Run{}
	}
	return out, err
}

func (s *Service) Runs(ctx context.Context, tx pgx.Tx) ([]Run, error) {
	rows, err := tx.Query(ctx, runSelect+` where r.target = 'cost_entries' order by r.created_at desc limit 50`)
	if err != nil {
		return nil, err
	}
	runs, err := scanRuns(rows)
	for i := range runs { // l'elenco resta leggero: anteprima ed errori nel dettaglio
		runs[i].Preview, runs[i].Errors = []PreviewRow{}, []RowError{}
	}
	return runs, err
}

// RunDetail è un'esecuzione con gli errori e l'anteprima.
func (s *Service) RunDetail(ctx context.Context, tx pgx.Tx, id string) (*Run, error) {
	return s.run(ctx, tx, id)
}

func (s *Service) run(ctx context.Context, tx pgx.Tx, id string) (*Run, error) {
	rows, err := tx.Query(ctx, runSelect+` where r.id::text = $1`, id)
	if err != nil {
		return nil, err
	}
	runs, err := scanRuns(rows)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, engine.ErrNotFound
	}
	return &runs[0], nil
}

// ImportInput: un file con un profilo salvato, oppure con una mappatura indicata qui
// (prova di una mappatura senza salvarla).
type ImportInput struct {
	ProfileID string   `json:"profile_id"`
	Mapping   *Mapping `json:"mapping"`
	FileName  string   `json:"file_name"`
	CSV       string   `json:"csv"`
}

func fileSHA(content string) []byte {
	sum := sha256.Sum256([]byte(content))
	return sum[:]
}

// Preview analizza il file senza scrivere voci: registra un'esecuzione in stato preview
// con i conteggi, gli errori riga per riga e le prime righe come saranno importate.
func (s *Service) Preview(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ImportInput) (*Run, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	m, profileID, err := s.mappingOf(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	a, err := s.analyze(ctx, tx, actor, m, in.CSV)
	if err != nil {
		return nil, err
	}
	preview := make([]PreviewRow, 0, 20)
	for _, r := range a.rows {
		if len(preview) == 20 {
			break
		}
		preview = append(preview, r.PreviewRow)
	}
	errs := a.errors
	if len(errs) > 200 {
		errs = errs[:200]
	}
	errsJSON, _ := json.Marshal(errs)
	if errs == nil {
		errsJSON = []byte("[]")
	}
	prevJSON, _ := json.Marshal(preview)
	mapJSON, _ := json.Marshal(m)
	name := strings.TrimSpace(in.FileName)
	if name == "" {
		name = "import.csv"
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into import_runs (tenant_id, id, profile_id, target, file_name, file_sha256, status, rows_total, rows_ok, rows_error,
		rows_duplicate, errors, preview, dry_run, mapping, created_by)
		values (app_tenant(), $1, $2, 'cost_entries', $3, $4, 'preview', $5, $6, $7, $8, $9, $10, true, $11, $12)`,
		id, nullable(profileID), name, fileSHA(in.CSV), a.total, len(a.rows)-a.duplicates, len(a.errors), a.duplicates,
		errsJSON, prevJSON, mapJSON, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	return s.run(ctx, tx, id)
}

func (s *Service) mappingOf(ctx context.Context, tx pgx.Tx, in ImportInput) (Mapping, string, error) {
	if len(in.CSV) == 0 {
		return Mapping{}, "", engine.InvalidCode("validation.finance.csv_empty", "csv", "il file è vuoto")
	}
	if in.ProfileID != "" {
		p, err := s.profile(ctx, tx, in.ProfileID)
		if err != nil {
			return Mapping{}, "", err
		}
		return p.Mapping, p.ID, nil
	}
	if in.Mapping == nil {
		return Mapping{}, "", engine.Required("profile_id", "indica un profilo o una mappatura")
	}
	return *in.Mapping, "", in.Mapping.validate()
}

// ConfirmInput conferma un'anteprima con lo stesso file.
type ConfirmInput struct {
	CSV        string `json:"csv"`
	SkipErrors bool   `json:"skip_errors"` // importa le righe valide anche se altre hanno errori
}

// Confirm importa le righe valide e non duplicate del file già provato. Il file deve
// essere identico (sha256) a quello dell'anteprima; l'analisi si ripete perché i dati di
// riferimento possono essere cambiati nel frattempo.
func (s *Service) Confirm(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string, in ConfirmInput) (*Run, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	var status string
	var sha, mapRaw []byte
	err := tx.QueryRow(ctx, "select status, file_sha256, mapping from import_runs where id::text = $1 for update", runID).Scan(&status, &sha, &mapRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "preview" {
		return nil, engine.InvalidCode("validation.finance.import_not_preview", "status", "l'esecuzione non è un'anteprima da confermare")
	}
	if !bytes.Equal(sha, fileSHA(in.CSV)) {
		return nil, engine.InvalidCode("validation.finance.import_file_changed", "csv", "il file è diverso da quello provato: rifai la prova")
	}
	var m Mapping
	if err := json.Unmarshal(mapRaw, &m); err != nil {
		return nil, err
	}
	a, err := s.analyze(ctx, tx, actor, m, in.CSV)
	if err != nil {
		return nil, err
	}
	if len(a.errors) > 0 && !in.SkipErrors {
		return nil, engine.InvalidParams("validation.finance.import_rows_with_errors", "csv", map[string]any{"n": len(a.errors)}, "%d righe con errori: correggile o conferma ignorandole", len(a.errors))
	}
	// Un solo insert per tutte le righe (array per colonna).
	var (
		elems, cats, refs, curs, descs, kinds, rates []string
		units, sups                                  []*string
		dates                                        []time.Time
		amounts, bases                               []int64
		caps                                         []bool
	)
	for _, r := range a.rows {
		if r.Duplicate {
			continue
		}
		elems, cats, refs, curs = append(elems, r.elementID), append(cats, r.categoryID), append(refs, r.SourceRef), append(curs, r.Currency)
		descs, kinds, rates = append(descs, r.Description), append(kinds, r.Kind), append(rates, r.fxRate)
		units, sups, dates = append(units, r.orgUnitID), append(sups, r.supplier), append(dates, r.date)
		amounts, bases, caps = append(amounts, r.AmountCents), append(bases, r.AmountBaseCents), append(caps, r.capitalizable)
	}
	var inserted int64
	if len(elems) > 0 {
		tag, err := tx.Exec(ctx, `insert into cost_entries (tenant_id, id, element_id, cost_category_id, org_unit_id, source, source_ref, entry_date, kind,
			amount_cents, currency, amount_base_cents, fx_rate, capitalizable, description, supplier, import_run_id, created_by)
			select app_tenant(), gen_random_uuid(), e, c, u, 'import', r, d, k, a, cur, b, x::numeric, cap, de, su, $14, $15
			from unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::text[], $5::date[], $6::text[], $7::bigint[], $8::text[], $9::bigint[],
			            $10::text[], $11::bool[], $12::text[], $13::text[]) as t(e, c, u, r, d, k, a, cur, b, x, cap, de, su)
			on conflict (tenant_id, source, source_ref) where source_ref is not null and source <> 'work_log' do nothing`,
			elems, cats, units, refs, dates, kinds, amounts, curs, bases, rates, caps, descs, sups, runID, nullable(actor.MemberID))
		if err != nil {
			return nil, err
		}
		inserted = tag.RowsAffected()
	}
	dups := len(a.rows) - int(inserted)
	errsJSON, _ := json.Marshal(a.errors)
	if a.errors == nil {
		errsJSON = []byte("[]")
	}
	if _, err := tx.Exec(ctx, `update import_runs set status = 'done', dry_run = false, rows_total = $2, rows_ok = $3, rows_error = $4,
		rows_duplicate = $5, errors = $6, finished_at = now() where id = $1`, runID, a.total, inserted, len(a.errors), dups, errsJSON); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "import.confirm", EntityType: "import_run", EntityID: runID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"rows_total": a.total, "rows_ok": inserted,
			"rows_duplicate": dups, "rows_error": len(a.errors), "file_sha256": hex.EncodeToString(sha)}}); err != nil {
		return nil, err
	}
	return s.run(ctx, tx, runID)
}

// Rollback annulla un import: le sue voci si eliminano (gli aggregati si aggiornano).
func (s *Service) Rollback(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string) (*Run, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	var status string
	err := tx.QueryRow(ctx, "select status from import_runs where id::text = $1 for update", runID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "done" {
		return nil, engine.InvalidCode("validation.finance.import_undo_only_completed", "status", "si annullano solo gli import completati")
	}
	tag, err := tx.Exec(ctx, "delete from cost_entries where import_run_id = $1", runID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update import_runs set status = 'rolled_back', finished_at = now() where id = $1", runID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "import.rollback", EntityType: "import_run", EntityID: runID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"deleted": tag.RowsAffected()}}); err != nil {
		return nil, err
	}
	return s.run(ctx, tx, runID)
}
