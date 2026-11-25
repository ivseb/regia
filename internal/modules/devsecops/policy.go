package devsecops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/i18n"
)

// Politica centrale della pipeline (replica/devsecops.md §3.3, fase 3): quali controlli sono
// obbligatori, e in quale modalità (off, warn, block), per ogni classe di applicazione. La
// classe dipende da criticità, funzione critica DORA, classificazione dei dati ed esposizione
// dell'applicazione. La definizione è un documento JSON versionato (tabelle pipeline_policy e
// pipeline_policy_versions, migrazione 0034): ogni salvataggio con una definizione diversa è
// una nuova versione. Si modifica da Configurazione → DevSecOps → Politica della pipeline.

// PolicyFormat identifica il formato della definizione.
const PolicyFormat = "regia.pipeline-policy/v1"

// Modalità di un controllo, dalla più debole alla più forte.
var modeRank = map[string]int{"off": 0, "warn": 1, "block": 2}

// Control è un controllo del catalogo della politica. Aliases sono i nomi con cui le pipeline
// lo riportano nei check_runs (es. il controllo «sast» arriva come «semgrep» o «codeql»).
type Control struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Help    string   `json:"help,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// Class è una classe di applicazione. Le condizioni sono in AND tra loro; dentro una
// condizione i valori sono in OR; una condizione vuota vale per tutte le applicazioni. La
// prima classe che corrisponde decide.
type Class struct {
	Key              string            `json:"key"`
	Name             string            `json:"name"`
	Criticality      []string          `json:"criticality,omitempty"`
	CriticalFunction string            `json:"critical_function,omitempty"` // "" | yes | no
	Data             []string          `json:"data,omitempty"`
	Exposure         []string          `json:"exposure,omitempty"`
	Modes            map[string]string `json:"modes"`
}

// PolicyDef è la definizione di una versione della politica.
type PolicyDef struct {
	Format string `json:"format"`
	// ExposureField è la chiave del campo personalizzato dell'applicazione con l'esposizione
	// (es. «interna», «internet»); vuoto = l'esposizione non è una condizione.
	ExposureField string    `json:"exposure_field,omitempty"`
	Controls      []Control `json:"controls"`
	Classes       []Class   `json:"classes"`
	// Fallback vale per le applicazioni che nessuna classe riconosce.
	Fallback Class `json:"fallback"`
}

var (
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	classKey  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	critLevel = []string{"low", "medium", "high", "critical"}
)

// DefaultPolicy è la politica di partenza proposta nell'editor: valori ragionevoli, che ogni
// organizzazione adatta.
func DefaultPolicy() PolicyDef {
	return PolicyDef{
		Format: PolicyFormat,
		Controls: []Control{
			{Key: "test", Name: "Test automatici", Help: "Esegue i test dell'applicazione a ogni modifica.", Aliases: []string{"tests", "unit", "unit_tests"}},
			{Key: "lint", Name: "Qualità del codice", Help: "Controlli di stile e qualità statica.", Aliases: []string{"linter", "format"}},
			{Key: "secrets", Name: "Segreti nel codice", Help: "Cerca chiavi e password committate per errore.", Aliases: []string{"secret_scan", "gitleaks"}},
			{Key: "dependencies", Name: "Dipendenze vulnerabili", Help: "Analisi delle dipendenze (SCA) e delle CVE note.", Aliases: []string{"cves", "sca", "dependency_scan", "grype", "osv"}},
			{Key: "sast", Name: "Analisi statica della sicurezza", Help: "SAST sul codice sorgente.", Aliases: []string{"semgrep", "codeql", "sonar"}},
			{Key: "image", Name: "Immagine del container", Help: "Scansione dell'immagine prima della pubblicazione.", Aliases: []string{"container", "trivy"}},
		},
		Classes: []Class{
			{Key: "funzione_critica", Name: "Funzione critica DORA", CriticalFunction: "yes",
				Modes: map[string]string{"test": "block", "lint": "warn", "secrets": "block", "dependencies": "block", "sast": "block", "image": "block"}},
			{Key: "criticita_alta", Name: "Criticità alta", Criticality: []string{"high", "critical"},
				Modes: map[string]string{"test": "block", "lint": "warn", "secrets": "block", "dependencies": "block", "sast": "warn", "image": "warn"}},
		},
		Fallback: Class{Key: "altre", Name: "Tutte le altre",
			Modes: map[string]string{"test": "warn", "lint": "warn", "secrets": "block", "dependencies": "warn", "sast": "off", "image": "off"}},
	}
}

// DefaultPolicyIn è la politica di partenza con nomi e spiegazioni nella lingua indicata.
func DefaultPolicyIn(lang string) PolicyDef {
	d := DefaultPolicy()
	if lang == i18n.Default {
		return d
	}
	for i := range d.Controls {
		d.Controls[i].Name, d.Controls[i].Help = i18n.Name(lang, d.Controls[i].Name), i18n.Name(lang, d.Controls[i].Help)
	}
	for i := range d.Classes {
		d.Classes[i].Name = i18n.Name(lang, d.Classes[i].Name)
	}
	d.Fallback.Name = i18n.Name(lang, d.Fallback.Name)
	return d
}

// PolicyProblem è un errore di validazione, con il punto della definizione.
type PolicyProblem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func cleanList(in []string, lower bool) []string {
	out := []string{}
	for _, v := range in {
		v = clip(v, 80)
		if lower {
			v = strings.ToLower(v)
		}
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// Normalize pulisce la definizione e restituisce i problemi trovati (vuoto = valida). Ogni
// classe ha una modalità per ogni controllo (assente = off).
func (d *PolicyDef) Normalize() []PolicyProblem {
	var ps []PolicyProblem
	add := func(path, msg string, a ...any) {
		ps = append(ps, PolicyProblem{Path: path, Message: fmt.Sprintf(msg, a...)})
	}
	d.Format = PolicyFormat
	d.ExposureField = strings.TrimSpace(d.ExposureField)
	if len(d.Controls) == 0 || len(d.Controls) > 30 {
		add("controls", "servono da 1 a 30 controlli")
	}
	keys := map[string]bool{}
	names := map[string]string{} // nome di un controllo (chiave o alias) → controllo
	for i := range d.Controls {
		c := &d.Controls[i]
		c.Key, c.Name, c.Help = strings.ToLower(strings.TrimSpace(c.Key)), clip(c.Name, 80), clip(c.Help, 300)
		w := fmt.Sprintf("controls[%d]", i)
		switch {
		case !keyRe.MatchString(c.Key):
			add(w+".key", "la chiave va da 1 a 40 caratteri: lettere minuscole, cifre e «_», cominciando da una lettera")
			continue
		case keys[c.Key]:
			add(w+".key", "la chiave %q è ripetuta", c.Key)
			continue
		case c.Name == "":
			add(w+".name", "serve il nome")
		}
		keys[c.Key] = true
		c.Aliases = cleanList(c.Aliases, true)
		if len(c.Aliases) > 12 {
			add(w+".aliases", "al massimo 12 nomi alternativi")
		}
		for _, n := range append([]string{c.Key}, c.Aliases...) {
			if prev, ok := names[n]; ok && prev != c.Key {
				add(w+".aliases", "il nome %q è già di %q", n, prev)
			}
			names[n] = c.Key
		}
	}
	if d.ExposureField != "" && !keyRe.MatchString(d.ExposureField) {
		add("exposure_field", "chiave del campo non valida")
	}
	check := func(c *Class, w string, fallback bool) {
		c.Key, c.Name = strings.ToLower(strings.TrimSpace(c.Key)), clip(c.Name, 80)
		if !classKey.MatchString(c.Key) {
			add(w+".key", "la chiave della classe non è valida")
		}
		if c.Name == "" {
			add(w+".name", "serve il nome della classe")
		}
		if fallback {
			c.Criticality, c.CriticalFunction, c.Data, c.Exposure = nil, "", nil, nil
		} else {
			c.Criticality = cleanList(c.Criticality, true)
			for _, v := range c.Criticality {
				if !slices.Contains(critLevel, v) {
					add(w+".criticality", "criticità sconosciuta: %q", v)
				}
			}
			switch c.CriticalFunction = strings.ToLower(strings.TrimSpace(c.CriticalFunction)); c.CriticalFunction {
			case "", "yes", "no":
			default:
				add(w+".critical_function", "ammessi yes, no o vuoto")
			}
			c.Data, c.Exposure = cleanList(c.Data, true), cleanList(c.Exposure, true)
			if len(c.Data) > 20 || len(c.Exposure) > 20 {
				add(w, "al massimo 20 valori per condizione")
			}
		}
		modes := map[string]string{}
		for k, m := range c.Modes {
			if !keys[k] {
				add(w+".modes."+k, "controllo sconosciuto")
				continue
			}
			if _, ok := modeRank[m]; !ok {
				add(w+".modes."+k, "modalità non ammessa (off, warn, block)")
				continue
			}
			modes[k] = m
		}
		for k := range keys {
			if modes[k] == "" {
				modes[k] = "off"
			}
		}
		c.Modes = modes
	}
	if len(d.Classes) > 12 {
		add("classes", "al massimo 12 classi")
	}
	seen := map[string]bool{}
	for i := range d.Classes {
		check(&d.Classes[i], fmt.Sprintf("classes[%d]", i), false)
		if seen[d.Classes[i].Key] {
			add(fmt.Sprintf("classes[%d].key", i), "la chiave %q è ripetuta", d.Classes[i].Key)
		}
		seen[d.Classes[i].Key] = true
	}
	if d.Fallback.Key == "" {
		d.Fallback.Key = "altre"
	}
	if d.Fallback.Name == "" {
		d.Fallback.Name = "Tutte le altre"
	}
	check(&d.Fallback, "fallback", true)
	if seen[d.Fallback.Key] {
		add("fallback.key", "la chiave %q è già di una classe", d.Fallback.Key)
	}
	return ps
}

// canonical è la forma con cui si confrontano due definizioni.
func (d *PolicyDef) canonical() []byte {
	b, _ := json.Marshal(d)
	return b
}

// controlIndex traduce il nome di un controllo riportato da una pipeline (chiave o alias)
// nella chiave del controllo del catalogo.
func (d *PolicyDef) controlIndex() map[string]string {
	idx := map[string]string{}
	for _, c := range d.Controls {
		for _, a := range c.Aliases {
			idx[a] = c.Key
		}
	}
	for _, c := range d.Controls {
		idx[c.Key] = c.Key
	}
	return idx
}

// AppAttrs sono gli attributi dell'applicazione che decidono la classe.
type AppAttrs struct {
	Criticality      string
	CriticalFunction bool
	Data             string
	Exposure         string
}

func has(list []string, v string) bool { return slices.Contains(list, strings.ToLower(v)) }

// ClassOf restituisce la classe di un'applicazione: la prima che corrisponde, altrimenti la
// classe di ripiego.
func (d *PolicyDef) ClassOf(a AppAttrs) *Class {
	for i := range d.Classes {
		c := &d.Classes[i]
		if len(c.Criticality) > 0 && !has(c.Criticality, a.Criticality) {
			continue
		}
		if c.CriticalFunction == "yes" && !a.CriticalFunction || c.CriticalFunction == "no" && a.CriticalFunction {
			continue
		}
		if len(c.Data) > 0 && !has(c.Data, a.Data) {
			continue
		}
		if len(c.Exposure) > 0 && !has(c.Exposure, a.Exposure) {
			continue
		}
		return c
	}
	return &d.Fallback
}

// ---------------------------------------------------------------------------
// Versioni

// PolicyVersion è una versione nello storico.
type PolicyVersion struct {
	Version   int        `json:"version"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy *string    `json:"created_by"`
	Def       *PolicyDef `json:"definition,omitempty"`
}

// PolicyState è la politica corrente con lo storico e ciò che serve a modificarla.
type PolicyState struct {
	Configured bool            `json:"configured"` // false: nessuna versione salvata, la definizione è quella di partenza
	Version    int             `json:"version"`
	Definition PolicyDef       `json:"definition"`
	Versions   []PolicyVersion `json:"versions"`
	Observed   Observed        `json:"observed"`
	CanEdit    bool            `json:"can_edit"`
	Fields     []FieldOption   `json:"fields"`
}

// Observed sono i valori che compaiono davvero nel catalogo e nei check_runs: suggeriscono
// i valori delle condizioni e i nomi dei controlli.
type Observed struct {
	Data     []string `json:"data"`
	Exposure []string `json:"exposure"`
	Checks   []string `json:"checks"`
}

// LoadPolicy legge la politica corrente (nil se non ne è stata salvata nessuna).
func LoadPolicy(ctx context.Context, tx pgx.Tx) (*PolicyDef, int, error) {
	var raw []byte
	var v int
	err := tx.QueryRow(ctx, `select v.definition, p.current_version from pipeline_policy p
		join pipeline_policy_versions v on v.tenant_id = p.tenant_id and v.version = p.current_version`).Scan(&raw, &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var d PolicyDef
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, 0, err
	}
	return &d, v, nil
}

// Policy restituisce la politica corrente (o quella di partenza) con lo storico.
func (s *Service) Policy(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*PolicyState, error) {
	if !actor.Has(PermRead) && !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	st := &PolicyState{CanEdit: CanConfigure(actor), Versions: []PolicyVersion{}}
	def, v, err := LoadPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	if def == nil {
		d := DefaultPolicyIn(i18n.Lang(ctx))
		def = &d
	} else {
		st.Configured, st.Version = true, v
	}
	st.Definition = *def
	rows, err := tx.Query(ctx, `select v.version, v.note, v.created_at, m.display_name from pipeline_policy_versions v
		left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by order by v.version desc limit 100`)
	if err != nil {
		return nil, err
	}
	if st.Versions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PolicyVersion, error) {
		var pv PolicyVersion
		err := r.Scan(&pv.Version, &pv.Note, &pv.CreatedAt, &pv.CreatedBy)
		return pv, err
	}); err != nil {
		return nil, err
	}
	if st.Observed, err = observed(ctx, tx, def.ExposureField); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select f.key, f.name from field_defs f
		join type_fields tf on tf.tenant_id = f.tenant_id and tf.field_id = f.id
		join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id and t.key = 'application'
		where f.data_type in ('string', 'enum') group by f.key, f.name order by f.name`)
	if err != nil {
		return nil, err
	}
	if st.Fields, err = pgx.CollectRows(rows, pgx.RowToStructByPos[FieldOption]); err != nil {
		return nil, err
	}
	return st, nil
}

func observed(ctx context.Context, tx pgx.Tx, exposureField string) (Observed, error) {
	o := Observed{Data: []string{}, Exposure: []string{}, Checks: []string{}}
	rows, err := tx.Query(ctx, `select distinct lower(data_classification) from ext_application where data_classification is not null and data_classification <> '' order by 1 limit 50`)
	if err != nil {
		return o, err
	}
	if o.Data, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return o, err
	}
	if exposureField != "" {
		rows, err = tx.Query(ctx, `select distinct lower(fields ->> $1) from elements where deleted_at is null and fields ->> $1 is not null order by 1 limit 50`, exposureField)
		if err != nil {
			return o, err
		}
		if o.Exposure, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return o, err
		}
	}
	rows, err = tx.Query(ctx, `select distinct c ->> 'name' from (select checks from check_runs order by at desc limit 500) r, jsonb_array_elements(r.checks) c
		where c ->> 'name' is not null order by 1 limit 100`)
	if err != nil {
		return o, err
	}
	o.Checks, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return o, err
}

// PolicyInput è un salvataggio della politica.
type PolicyInput struct {
	Definition PolicyDef `json:"definition"`
	Note       string    `json:"note"`
}

// ValidationError porta i problemi di una definizione.
type ValidationError struct{ Problems []PolicyProblem }

func (e *ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "politica non valida"
	}
	return "politica non valida: " + e.Problems[0].Path + ": " + e.Problems[0].Message
}

// checkExposureField verifica che il campo dell'esposizione esista.
func checkExposureField(ctx context.Context, tx pgx.Tx, def *PolicyDef) error {
	if def.ExposureField == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from field_defs where key = $1)", def.ExposureField).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound("exposure_field", "field", "campo personalizzato inesistente: %s", def.ExposureField)
	}
	return nil
}

// SavePolicy salva una nuova versione se la definizione è diversa dalla corrente.
func (s *Service) SavePolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, in PolicyInput) (*PolicyState, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	def := in.Definition
	if ps := def.Normalize(); len(ps) > 0 {
		return nil, &ValidationError{Problems: ps}
	}
	if err := checkExposureField(ctx, tx, &def); err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "pipeline-policy"); err != nil {
		return nil, err
	}
	cur, v, err := LoadPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	if cur == nil || !bytes.Equal(cur.canonical(), def.canonical()) {
		raw, _ := json.Marshal(def)
		v++
		if _, err := tx.Exec(ctx, `insert into pipeline_policy_versions (tenant_id, version, definition, note, created_by) values (app_tenant(), $1, $2, $3, $4)`,
			v, raw, clip(in.Note, 500), nullable(actor.MemberID)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into pipeline_policy (tenant_id, current_version) values (app_tenant(), $1)
			on conflict (tenant_id) do update set current_version = excluded.current_version, updated_at = now()`, v); err != nil {
			return nil, err
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.policy.save", "pipeline_policy", "", map[string]any{"version": v, "controls": len(def.Controls), "classes": len(def.Classes) + 1}); err != nil {
			return nil, err
		}
	}
	return s.Policy(ctx, tx, actor)
}

// PolicyVersionOf legge una versione dello storico.
func (s *Service) PolicyVersionOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, n int) (*PolicyVersion, error) {
	if !actor.Has(PermRead) && !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	var v PolicyVersion
	var raw []byte
	err := tx.QueryRow(ctx, `select v.version, v.note, v.created_at, m.display_name, v.definition from pipeline_policy_versions v
		left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by where v.version = $1`, n).
		Scan(&v.Version, &v.Note, &v.CreatedAt, &v.CreatedBy, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.Def = &PolicyDef{}
	return &v, json.Unmarshal(raw, v.Def)
}

// ---------------------------------------------------------------------------
// Differenze leggibili tra due versioni

// Change è una differenza tra due definizioni, in italiano.
type Change struct {
	Kind string `json:"kind"` // control | class | mode | condition
	Text string `json:"text"`
	// Stricter: la modifica rende la politica più severa (true) o più morbida (false);
	// nil se non è confrontabile.
	Stricter *bool `json:"stricter,omitempty"`
}

var modeIt = map[string]string{"off": "spento", "warn": "avviso", "block": "bloccante"}

func condText(c Class) string {
	var parts []string
	if len(c.Criticality) > 0 {
		parts = append(parts, "criticità "+strings.Join(c.Criticality, " o "))
	}
	switch c.CriticalFunction {
	case "yes":
		parts = append(parts, "funzione critica DORA")
	case "no":
		parts = append(parts, "senza funzione critica DORA")
	}
	if len(c.Data) > 0 {
		parts = append(parts, "dati "+strings.Join(c.Data, " o "))
	}
	if len(c.Exposure) > 0 {
		parts = append(parts, "esposizione "+strings.Join(c.Exposure, " o "))
	}
	if len(parts) == 0 {
		return "tutte le applicazioni"
	}
	return strings.Join(parts, ", ")
}

func sameStrings(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func orOff(m string) string {
	if m == "" {
		return "off"
	}
	return m
}

// DiffPolicy elenca le differenze tra due definizioni (from può essere nil: tutto è nuovo).
func DiffPolicy(from, to *PolicyDef) []Change {
	out := []Change{}
	if to == nil {
		return out
	}
	hasFrom := from != nil
	if from == nil {
		from = &PolicyDef{Format: PolicyFormat}
	}
	ctrlName := func(k string) string {
		for _, c := range to.Controls {
			if c.Key == k {
				return c.Name
			}
		}
		return k
	}
	fromCtl := map[string]Control{}
	for _, c := range from.Controls {
		fromCtl[c.Key] = c
	}
	toCtl := map[string]bool{}
	for _, c := range to.Controls {
		toCtl[c.Key] = true
		if p, ok := fromCtl[c.Key]; !ok {
			out = append(out, Change{Kind: "control", Text: "Nuovo controllo: " + c.Name})
		} else if p.Name != c.Name || !sameStrings(p.Aliases, c.Aliases) {
			out = append(out, Change{Kind: "control", Text: "Controllo modificato: " + c.Name + " (nome o nomi alternativi)"})
		}
	}
	for _, c := range from.Controls {
		if !toCtl[c.Key] {
			out = append(out, Change{Kind: "control", Text: "Controllo tolto: " + c.Name})
		}
	}
	classes := func(d *PolicyDef) []Class {
		if len(d.Controls) == 0 && len(d.Classes) == 0 {
			return nil
		}
		return append(slices.Clone(d.Classes), d.Fallback)
	}
	fromCls := map[string]Class{}
	for _, c := range classes(from) {
		fromCls[c.Key] = c
	}
	toCls := map[string]bool{}
	for _, c := range classes(to) {
		toCls[c.Key] = true
		p, ok := fromCls[c.Key]
		if !ok {
			if hasFrom {
				out = append(out, Change{Kind: "class", Text: "Nuova classe «" + c.Name + "» (" + condText(c) + ")"})
			}
			continue
		}
		if p.Name != c.Name {
			out = append(out, Change{Kind: "class", Text: "Classe «" + p.Name + "» rinominata «" + c.Name + "»"})
		}
		if condText(p) != condText(c) {
			out = append(out, Change{Kind: "condition", Text: "Classe «" + c.Name + "»: da " + condText(p) + " a " + condText(c)})
		}
		for _, k := range to.Controls {
			before, after := orOff(p.Modes[k.Key]), orOff(c.Modes[k.Key])
			if _, existed := fromCtl[k.Key]; !existed || before == after {
				if !existed && after != "off" && hasFrom {
					st := true
					out = append(out, Change{Kind: "mode", Stricter: &st, Text: fmt.Sprintf("Classe «%s», %s: %s", c.Name, ctrlName(k.Key), modeIt[after])})
				}
				continue
			}
			st := modeRank[after] > modeRank[before]
			out = append(out, Change{Kind: "mode", Stricter: &st,
				Text: fmt.Sprintf("Classe «%s», %s: da %s a %s", c.Name, ctrlName(k.Key), modeIt[before], modeIt[after])})
		}
	}
	if hasFrom {
		for _, c := range classes(from) {
			if !toCls[c.Key] {
				out = append(out, Change{Kind: "class", Text: "Classe tolta: «" + c.Name + "»"})
			}
		}
		var order1, order2 []string
		for _, c := range from.Classes {
			if slices.ContainsFunc(to.Classes, func(x Class) bool { return x.Key == c.Key }) {
				order1 = append(order1, c.Key)
			}
		}
		for _, c := range to.Classes {
			if slices.ContainsFunc(from.Classes, func(x Class) bool { return x.Key == c.Key }) {
				order2 = append(order2, c.Key)
			}
		}
		if !slices.Equal(order1, order2) {
			out = append(out, Change{Kind: "class", Text: "L'ordine delle classi è cambiato (decide la prima che corrisponde)"})
		}
		if from.ExposureField != to.ExposureField {
			out = append(out, Change{Kind: "condition", Text: "Campo dell'esposizione: «" + from.ExposureField + "» → «" + to.ExposureField + "»"})
		}
	}
	return out
}

// PolicyDiff confronta due versioni (a = 0: nessuna, tutto è nuovo).
func (s *Service) PolicyDiff(ctx context.Context, tx pgx.Tx, actor engine.Actor, a, b int) ([]Change, error) {
	var from *PolicyDef
	if a > 0 {
		v, err := s.PolicyVersionOf(ctx, tx, actor, a)
		if err != nil {
			return nil, err
		}
		from = v.Def
	}
	v, err := s.PolicyVersionOf(ctx, tx, actor, b)
	if err != nil {
		return nil, err
	}
	return DiffPolicy(from, v.Def), nil
}
