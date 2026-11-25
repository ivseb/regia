package importer

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Il piano è l'import calcolato in memoria contro i dati attuali del tenant: per ogni issue
// tipo, fase, persone, campi, genitore e sprint già risolti, con gli errori e gli avvisi
// riga per riga. Serve all'anteprima (che non salva niente) e all'esecuzione, che lo
// scrive a lotti. Gli oggetti da creare (spazio, campi, opzioni, membro «importato»,
// sprint) sono elencati qui e creati da prepare, dentro la transazione che scrive.

// RowError è un problema di una riga del file (o di una issue dell'API).
type RowError struct {
	Line    int    `json:"line,omitempty"`
	Key     string `json:"key,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Level   string `json:"level"` // error | warning
}

type typeTarget struct {
	info    *engine.TypeInfo
	initial engine.State
	defs    []engine.FieldDef
}

type fieldMeta struct {
	id, key, name, dataType string
	multi                   bool
	optionSetID             *string
	options                 map[string]string // nome in minuscolo → id
	optionNames             map[string]string // nome in minuscolo → nome com'è scritto
	newOptions              []string          // opzioni da creare (nomi)
	archived, isNew         bool
	hasValues               bool
	types                   map[string]bool // tipi a cui è associato
	hoursPerDay             float64
}

type pComment struct {
	key     string
	author  *string // membro; nil = membro «importato»
	created time.Time
	body    string
}

type pWorklog struct {
	key     string
	member  *string // nil = membro «importato»
	day     time.Time
	minutes int
	text    string
}

type pLink struct {
	relType           string
	source, target    int    // indici del piano (-1 = elemento esistente)
	sourceID, target2 string // id degli elementi esistenti
}

type pIssue struct {
	src      *Issue
	id       string
	existing bool
	version  int
	typ      string
	state    engine.State
	number   int64
	title    string
	body     string
	assignee *string
	reporter *string
	created  time.Time
	updated  time.Time
	resolved *time.Time
	start    *time.Time
	due      *time.Time
	parentID *string
	parent   int // indice del genitore nel piano, -1
	depth    int
	sprint   int              // indice dello sprint, -1
	raw      map[string][]any // valori dei campi per chiave, prima di risolvere le opzioni
	fields   map[string]any
	comments []pComment
	worklogs []pWorklog
	atts     []Attachment // da scaricare (solo API)
	userNote map[string]string
}

type pSprint struct {
	name     string
	key      string // chiave d'origine
	id       string
	existing bool
	state    string // planned | active | closed
	start    *time.Time
	end      *time.Time
	goal     string
}

type plan struct {
	runID    string
	byImport map[string]bool // oggetti creati da un import (campi, spazi): si riusano
	m        *Mapping
	ds       *Dataset
	system   string
	loc      *time.Location
	dateFmt  string
	actor    engine.Actor
	issues   []*pIssue // in ordine di profondità: i genitori prima dei figli
	sprints  []*pSprint
	links    []pLink
	errors   []RowError
	warnings []string
	types    map[string]*typeTarget
	fields   map[string]*fieldMeta // per chiave
	space    struct {
		id, key, name string
		isNew         bool
		team          string
	}
	keepNumbers   bool
	needHolder    bool
	holderName    string
	holderID      string
	priorityField string
	labelsField   string
	originField   string
	estimateField string
	members       map[string]memberRow
	skipped       int
	unmapped      map[string]int // stati senza fase: "tipo/stato" → elementi
	relTypes      map[string]string
	counts        map[string]int // per tipo
	created       int
	updated       int
	attLinks      int
}

type memberRow struct {
	id, name, email, status string
}

const (
	maxErrorsKept  = 1000
	originFieldKey = "origine"
)

func (p *plan) addError(is *Issue, field, format string, args ...any) {
	if len(p.errors) < maxErrorsKept {
		p.errors = append(p.errors, RowError{Line: is.Line, Key: is.Key, Field: field, Message: fmt.Sprintf(format, args...), Level: "error"})
	}
}

func (p *plan) addWarning(is *Issue, field, format string, args ...any) {
	if len(p.errors) < maxErrorsKept {
		p.errors = append(p.errors, RowError{Line: is.Line, Key: is.Key, Field: field, Message: fmt.Sprintf(format, args...), Level: "warning"})
	}
}

func (p *plan) errorCount() (errs, warns int) {
	for _, e := range p.errors {
		if e.Level == "error" {
			errs++
		} else {
			warns++
		}
	}
	return
}

// buildPlan calcola il piano. Non scrive nulla.
func buildPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, ds *Dataset, m *Mapping, runID string) (*plan, error) {
	issues, _, _ := issuesOf(ds, m.Columns)
	p := &plan{runID: runID, byImport: map[string]bool{}, m: m, ds: ds, system: ds.System, loc: m.location(), dateFmt: m.DateFormat, actor: actor,
		types: map[string]*typeTarget{}, fields: map[string]*fieldMeta{}, members: map[string]memberRow{},
		unmapped: map[string]int{}, relTypes: map[string]string{}, counts: map[string]int{}}
	if p.dateFmt == "" {
		p.dateFmt = "auto"
	}
	p.holderName = "Importato da " + map[string]string{"jira": "Jira", "youtrack": "YouTrack"}[ds.System]
	if p.holderName == "Importato da " {
		p.holderName = "Importato da CSV"
	}
	if err := p.loadTenant(ctx, tx); err != nil {
		return nil, err
	}
	if err := p.resolveSpace(ctx, tx); err != nil {
		return nil, err
	}
	if err := p.resolveFields(); err != nil {
		return nil, err
	}
	// Ultima occorrenza di ogni chiave.
	last := map[string]int{}
	for i, is := range issues {
		last[strings.ToUpper(is.Key)] = i
	}
	byKey := map[string]int{}
	byID := map[string]int{}
	var keys []string
	for i := range issues {
		is := &issues[i]
		if last[strings.ToUpper(is.Key)] != i {
			p.addWarning(is, "key", "chiave ripetuta più avanti nel file: questa riga si salta")
			continue
		}
		pi := p.issue(is)
		if pi == nil {
			continue
		}
		byKey[strings.ToUpper(is.Key)] = len(p.issues)
		if is.ID != "" {
			byID[is.ID] = len(p.issues)
		}
		keys = append(keys, is.Key)
		p.issues = append(p.issues, pi)
	}
	if err := p.resolveExisting(ctx, tx, keys); err != nil {
		return nil, err
	}
	if err := p.resolveSprints(ctx, tx); err != nil {
		return nil, err
	}
	if err := p.resolveParents(ctx, tx, byKey, byID); err != nil {
		return nil, err
	}
	if err := p.resolveLinks(ctx, tx, byKey); err != nil {
		return nil, err
	}
	if err := p.resolveNumbers(ctx, tx); err != nil {
		return nil, err
	}
	p.order()
	for _, pi := range p.issues {
		for k := range pi.raw {
			if f := p.fields[k]; f != nil {
				f.hasValues = true
			}
		}
		p.counts[pi.typ]++
		if pi.existing {
			p.updated++
		} else {
			p.created++
		}
	}
	for k, n := range p.unmapped {
		t, st, _ := strings.Cut(k, "/")
		p.warnings = append(p.warnings, fmt.Sprintf("%d elementi %s con lo stato «%s» senza fase corrispondente: vanno nella fase iniziale", n, t, st))
	}
	sort.Strings(p.warnings)
	return p, nil
}

func (p *plan) loadTenant(ctx context.Context, tx pgx.Tx) error {
	for _, t := range WorkTypes {
		ti, err := engine.LoadType(ctx, tx, t)
		if err != nil {
			var ve *engine.ValidationError
			if errors.As(err, &ve) {
				return engine.InvalidCode("validation.importer.work_not_configured", "work", "il work management non è configurato: esegui «regia tenant provision»")
			}
			return err
		}
		tt := &typeTarget{info: ti}
		if ti.Workflow != nil && ti.Workflow.Initial != "" {
			for _, s := range ti.States {
				if strings.EqualFold(s.Name, ti.Workflow.Initial) {
					tt.initial = s
				}
			}
		}
		if tt.initial.ID == "" && len(ti.States) > 0 {
			tt.initial = ti.States[0]
		}
		p.types[t] = tt
	}
	rows, err := tx.Query(ctx, `select distinct target_id::text from import_created where kind in ('field', 'space')`)
	if err != nil {
		return err
	}
	var cid string
	if _, err := pgx.ForEachRow(rows, []any{&cid}, func() error { p.byImport[cid] = true; return nil }); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `select id::text, display_name, coalesce(lower(email), ''), status from members`)
	if err != nil {
		return err
	}
	var mr memberRow
	if _, err := pgx.ForEachRow(rows, []any{&mr.id, &mr.name, &mr.email, &mr.status}, func() error {
		p.members[mr.id] = mr
		if mr.name == p.holderName && mr.email == "" {
			p.holderID = mr.id
		}
		return nil
	}); err != nil {
		return err
	}
	if p.holderID != "" {
		var service bool
		if err := tx.QueryRow(ctx, `select member_type = 'service' and account_id is null from members where id = $1`, p.holderID).Scan(&service); err != nil || !service {
			p.holderID = ""
		}
	}
	rows, err = tx.Query(ctx, `select f.id::text, f.key, f.name, f.data_type, f.is_multi, f.option_set_id::text, f.archived,
		coalesce((f.settings->>'hours_per_day')::float8, 8),
		coalesce((select array_agg(t.key) from type_fields tf join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id
		          where tf.field_id = f.id and tf.space_id is null), '{}')
		from field_defs f`)
	if err != nil {
		return err
	}
	var f fieldMeta
	var types []string
	if _, err := pgx.ForEachRow(rows, []any{&f.id, &f.key, &f.name, &f.dataType, &f.multi, &f.optionSetID, &f.archived, &f.hoursPerDay, &types}, func() error {
		fm := f
		fm.types = map[string]bool{}
		for _, t := range types {
			fm.types[t] = true
		}
		fm.options, fm.optionNames = map[string]string{}, map[string]string{}
		p.fields[fm.key] = &fm
		return nil
	}); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `select f.key, o.id::text, o.name from options o join field_defs f on f.tenant_id = o.tenant_id and f.option_set_id = o.option_set_id
		where not o.archived`)
	if err != nil {
		return err
	}
	var fk, oid, oname string
	if _, err := pgx.ForEachRow(rows, []any{&fk, &oid, &oname}, func() error {
		if fm := p.fields[fk]; fm != nil {
			fm.options[strings.ToLower(oname)] = oid
			fm.optionNames[strings.ToLower(oname)] = oname
		}
		return nil
	}); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `select key, id::text from relation_types`)
	if err != nil {
		return err
	}
	var rk, rid string
	_, err = pgx.ForEachRow(rows, []any{&rk, &rid}, func() error { p.relTypes[rk] = rid; return nil })
	return err
}

func (p *plan) resolveSpace(ctx context.Context, tx pgx.Tx) error {
	key := strings.ToUpper(strings.TrimSpace(p.m.Space.Key))
	var id, name string
	err := tx.QueryRow(ctx, `select id::text, name from spaces where key = $1`, key).Scan(&id, &name)
	switch {
	case p.m.Space.Mode == "existing":
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.NotFound("space", "space", "lo spazio %s non esiste", key)
		}
		if err != nil {
			return err
		}
		var allowed int
		if err := tx.QueryRow(ctx, `select count(*) from space_types st join element_types t on t.tenant_id = st.tenant_id and t.id = st.type_id
			where st.space_id = $1 and t.key = any($2)`, id, WorkTypes).Scan(&allowed); err != nil {
			return err
		}
		if allowed < len(WorkTypes) {
			return engine.InvalidParams("validation.importer.space_not_team", "space", map[string]any{"key": key}, "lo spazio %s non è uno spazio di team: scegli lo spazio di un team o creane uno nuovo", key)
		}
		p.space.id, p.space.key, p.space.name = id, key, name
	default:
		if err == nil && p.byImport[id] {
			// Spazio creato da un import precedente (o da questo, interrotto): si riusa.
			p.space.id, p.space.key, p.space.name = id, key, name
			p.keepNumbers = p.m.Space.KeepNumbers
			return nil
		}
		if err == nil {
			return engine.Duplicate("space", "la chiave %s è già usata dallo spazio «%s»: scegli un'altra chiave o importa in quello spazio", key, name)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var teamUsed bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from teams where lower(name) = lower($1))`, strings.TrimSpace(p.m.Space.TeamName)).Scan(&teamUsed); err != nil {
			return err
		}
		if teamUsed {
			return engine.Duplicate("space", "esiste già un team «%s»: scegli il suo spazio o un altro nome", strings.TrimSpace(p.m.Space.TeamName))
		}
		p.space.id, p.space.key, p.space.name, p.space.isNew, p.space.team = db.NewID(), key, strings.TrimSpace(p.m.Space.TeamName), true, strings.TrimSpace(p.m.Space.TeamName)
		p.keepNumbers = p.m.Space.KeepNumbers
	}
	return nil
}

// resolveFields controlla i campi di destinazione e prepara quelli nuovi.
func (p *plan) resolveFields() error {
	ensureNew := func(key, name, dataType string, multi bool) (*fieldMeta, error) {
		if f, ok := p.fields[key]; ok {
			if f.isNew || (p.byImport[f.id] && f.dataType == dataType && !f.archived) {
				return f, nil // campo creato da un import precedente: si riusa
			}
			return nil, engine.Duplicate("fields", "la chiave %q è già usata dal campo «%s»: scegli «campo esistente» o un'altra chiave", key, f.name)
		}
		f := &fieldMeta{id: db.NewID(), key: key, name: strings.TrimSpace(name), dataType: dataType, multi: multi, isNew: true,
			options: map[string]string{}, optionNames: map[string]string{}, types: map[string]bool{}, hoursPerDay: 8}
		p.fields[key] = f
		return f, nil
	}
	check := func(section, src string, t FieldTarget) (string, error) {
		switch t.Mode {
		case "existing":
			f, ok := p.fields[t.Key]
			if !ok {
				return "", engine.NotFound(section, "", "%s: il campo %q non esiste", src, t.Key)
			}
			if f.archived {
				return "", engine.InvalidParams("validation.importer.field_archived", section, map[string]any{"source": src, "field": f.name}, "%s: il campo «%s» è archiviato", src, f.name)
			}
			switch f.dataType {
			case "money", "group", "element", "version", "state":
				return "", engine.InvalidParams("validation.importer.field_type_unsupported", section, map[string]any{"source": src, "type": f.dataType}, "%s: i campi di tipo %s non si importano", src, f.dataType)
			}
			return f.key, nil
		case "new":
			f, err := ensureNew(t.Key, t.Name, t.DataType, t.Multi)
			if err != nil {
				return "", err
			}
			return f.key, nil
		}
		return "", nil
	}
	if f, ok := p.fields[originFieldKey]; ok {
		if f.dataType != "string" {
			return engine.Duplicate("fields", "il campo «origine» esiste già con un tipo diverso da testo: rinominane la chiave")
		}
	} else {
		ensureNew(originFieldKey, "Origine", "string", false) //nolint:errcheck // non può fallire: la chiave è libera
	}
	p.originField = originFieldKey
	if f, ok := p.fields["stima"]; ok && f.dataType == "duration" && !f.archived {
		p.estimateField = "stima"
	}
	for src, t := range p.m.Fields {
		if _, err := check("fields", src, t); err != nil {
			return err
		}
	}
	var err error
	if p.m.Labels.Mode != "" && p.m.Labels.Mode != "ignore" {
		t := p.m.Labels
		if t.Mode == "new" {
			t.Multi = true
		}
		if p.labelsField, err = check("labels", "etichette", t); err != nil {
			return err
		}
		if f := p.fields[p.labelsField]; f != nil && f.dataType != "enum" && f.dataType != "string" {
			return engine.InvalidCode("validation.importer.labels_field", "labels", "le etichette vanno in un campo a scelta o di testo")
		}
	}
	if k := p.m.Priority.Field; k != "" {
		f, ok := p.fields[k]
		if !ok || f.dataType != "enum" || f.archived {
			return engine.InvalidCode("validation.importer.priority_field", "priority", "la priorità va in un campo a scelta esistente (es. Priorità)")
		}
		p.priorityField = k
	}
	return nil
}

// issue risolve tipo, fase, date, persone e campi di una issue.
func (p *plan) issue(is *Issue) *pIssue {
	for _, pr := range is.Problems {
		p.addWarning(is, "", "%s", pr)
	}
	src := is.Type
	if src == "" {
		src = "(senza tipo)"
	}
	typ, ok := p.m.Types[src]
	if !ok {
		typ = defaultType(src)
	}
	if typ == "" {
		p.skipped++
		return nil
	}
	tt := p.types[typ]
	if tt == nil {
		p.addError(is, "type", "tipo %q sconosciuto", typ)
		return nil
	}
	is.Summary, is.Description = clean(is.Summary), clean(is.Description)
	for i := range is.Comments {
		is.Comments[i].Body = clean(is.Comments[i].Body)
	}
	for k, vals := range is.Custom {
		for i := range vals {
			vals[i] = clean(vals[i])
		}
		is.Custom[k] = vals
	}
	title := strings.TrimSpace(strings.ReplaceAll(is.Summary, "\n", " "))
	if title == "" {
		p.addError(is, "summary", "manca il titolo")
		return nil
	}
	if utf8.RuneCountInString(title) > 500 {
		title = string([]rune(title)[:497]) + "…"
		p.addWarning(is, "summary", "titolo troppo lungo: accorciato a 500 caratteri")
	}
	pi := &pIssue{src: is, id: db.NewID(), typ: typ, title: title, parent: -1, sprint: -1, raw: map[string][]any{}, userNote: map[string]string{}}
	// Fase.
	st := is.Status
	if st == "" {
		st = "(senza stato)"
	}
	phase := p.m.States[typ][st]
	if phase == "" {
		phase = defaultPhase(tt, st, is.StatusCat)
	}
	found := false
	for _, s := range tt.info.States {
		if strings.EqualFold(s.Name, phase) {
			pi.state, found = s, true
			break
		}
	}
	if !found {
		pi.state = tt.initial
		p.unmapped[typ+"/"+st]++
	}
	// Date.
	now := time.Now().UTC()
	date := func(field, v string) *time.Time {
		if strings.TrimSpace(v) == "" {
			return nil
		}
		t, err := parseDate(v, p.dateFmt, p.loc)
		if err != nil {
			p.addWarning(is, field, "data %q non riconosciuta: scegli il formato delle date nella mappatura", short(v))
			return nil
		}
		return &t
	}
	if c := date("created", is.Created); c != nil {
		pi.created = *c
	} else {
		pi.created = now
	}
	if u := date("updated", is.Updated); u != nil && !u.Before(pi.created) {
		pi.updated = *u
	} else {
		pi.updated = pi.created
	}
	if pi.state.Category == "done" || pi.state.Category == "cancelled" {
		if r := date("resolved", is.Resolved); r != nil {
			pi.resolved = r
		} else {
			r := pi.updated
			pi.resolved = &r
		}
	}
	if d := date("due", is.Due); d != nil {
		x := dayOnly(*d, p.loc)
		pi.due = &x
	}
	if s := date("start", is.Start); s != nil {
		x := dayOnly(*s, p.loc)
		pi.start = &x
	}
	// Persone.
	pi.assignee = p.member(is.Assignee, pi, "assignee")
	pi.reporter = p.member(is.Reporter, pi, "reporter")
	// Testo: descrizione e allegati come link (dall'API si scaricano, se richiesto).
	body := is.Description
	if p.system == "jira" {
		body = wikiToMarkdown(body)
	}
	var links []string
	for _, a := range is.Attachments {
		if p.ds.Format == "api" && p.ds.canDownload && p.m.Include.Attachments && a.URL != "" {
			pi.atts = append(pi.atts, a)
			continue
		}
		if a.URL != "" {
			name := a.Name
			if name == "" {
				name = a.URL
			}
			links = append(links, fmt.Sprintf("- [%s](%s)", strings.ReplaceAll(name, "]", ""), a.URL))
			p.attLinks++
		}
	}
	if len(links) > 0 {
		if body != "" {
			body += "\n\n"
		}
		body += "**Allegati nell'origine**\n\n" + strings.Join(links, "\n")
	}
	if utf8.RuneCountInString(body) > 200_000 {
		body = string([]rune(body)[:200_000])
		p.addWarning(is, "description", "descrizione troppo lunga: accorciata a 200.000 caratteri")
	}
	pi.body = body
	// Campi.
	p.setRaw(pi, p.originField, is.Key)
	if p.priorityField != "" && is.Priority != "" {
		name := p.m.Priority.Values[is.Priority]
		if name == "" {
			name = defaultPriority(p.fields[p.priorityField], is.Priority)
		}
		p.setRaw(pi, p.priorityField, p.optionName(p.fields[p.priorityField], name))
	}
	if p.labelsField != "" {
		f := p.fields[p.labelsField]
		for _, l := range is.Labels {
			if f.dataType == "enum" {
				p.setRaw(pi, f.key, p.optionName(f, l))
			} else {
				p.setRaw(pi, f.key, l)
			}
		}
	}
	if p.estimateField != "" && is.Estimate != nil && *is.Estimate > 0 {
		p.setRaw(pi, p.estimateField, math.Round(*is.Estimate))
	}
	for name, vals := range is.Custom {
		t, ok := p.m.Fields[name]
		if !ok || t.Mode == "" || t.Mode == "ignore" {
			continue
		}
		f := p.fields[t.Key]
		if f == nil {
			continue
		}
		for _, v := range vals {
			cv, err := p.convert(f, v)
			if err != nil {
				p.addWarning(is, name, "%v: il valore si salta", err)
				continue
			}
			if cv != nil {
				p.setRaw(pi, f.key, cv)
			}
		}
	}
	// Commenti e ore.
	if p.m.Include.Comments {
		for i, c := range is.Comments {
			body := strings.TrimSpace(c.Body)
			if body == "" {
				continue
			}
			if p.system == "jira" {
				body = wikiToMarkdown(body)
			}
			pc := pComment{key: commentKey(is.Key, c, i), body: body}
			if t := date("comment", c.Created); t != nil {
				pc.created = *t
			} else {
				pc.created = pi.created
			}
			if mid := p.memberOnly(c.Author); mid != nil {
				pc.author = mid
			} else {
				p.needHolder = true
				name := "sconosciuto"
				if c.Author != nil && c.Author.Name != "" {
					name = c.Author.Name
				}
				pc.body = "_Importato da " + name + "_\n\n" + body
			}
			if utf8.RuneCountInString(pc.body) > 50_000 {
				pc.body = string([]rune(pc.body)[:50_000])
			}
			pi.comments = append(pi.comments, pc)
		}
	}
	if p.m.Include.Worklogs {
		for i, w := range is.Worklogs {
			mins := int(math.Round(w.Minutes))
			if mins <= 0 {
				continue
			}
			pw := pWorklog{key: worklogKey(is.Key, w, i), minutes: mins, text: w.Comment}
			if t := date("worklog", w.Started); t != nil {
				pw.day = dayOnly(*t, p.loc)
			} else {
				pw.day = dayOnly(pi.created, p.loc)
			}
			pw.member = p.memberOnly(w.Author)
			if pw.member == nil {
				p.needHolder = true
				if w.Author != nil && w.Author.Name != "" {
					pw.text = strings.TrimSpace("Importato da " + w.Author.Name + ". " + pw.text)
				}
			}
			pi.worklogs = append(pi.worklogs, pw)
		}
	}
	return pi
}

// clean toglie ciò che Postgres non accetta in un testo (NUL, UTF-8 non valido).
func clean(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return s
}

func (p *plan) setRaw(pi *pIssue, key string, v any) {
	if key == "" || v == nil {
		return
	}
	f := p.fields[key]
	if f == nil {
		return
	}
	if !f.multi {
		pi.raw[key] = []any{v}
		return
	}
	for _, x := range pi.raw[key] {
		if fmt.Sprint(x) == fmt.Sprint(v) {
			return
		}
	}
	if len(pi.raw[key]) < 200 {
		pi.raw[key] = append(pi.raw[key], v)
	}
}

// optionName restituisce il nome dell'opzione (esistente, o da creare se manca).
func (p *plan) optionName(f *fieldMeta, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || f == nil {
		return ""
	}
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	low := strings.ToLower(name)
	if n, ok := f.optionNames[low]; ok {
		return n
	}
	f.optionNames[low] = name
	f.newOptions = append(f.newOptions, name)
	return name
}

// member risolve una persona come assegnatario o segnalatore.
func (p *plan) member(u *UserRef, pi *pIssue, role string) *string {
	if u == nil || u.Ref == "" {
		return nil
	}
	t, ok := p.m.Users[u.Ref]
	if !ok {
		t = UserTarget{Mode: "none"}
	}
	switch t.Mode {
	case "member":
		if mr, ok := p.members[t.MemberID]; ok && mr.status != "anonymized" {
			id := mr.id
			return &id
		}
		p.addWarning(pi.src, role, "il membro scelto per %s non esiste più", u.Name)
	case "placeholder":
		p.needHolder = true
		pi.userNote[role] = u.Name
		return &p.holderID // l'id si conosce dopo prepare: si legge allora
	}
	pi.userNote[role] = u.Name
	return nil
}

func (p *plan) memberOnly(u *UserRef) *string {
	if u == nil || u.Ref == "" {
		return nil
	}
	if t, ok := p.m.Users[u.Ref]; ok && t.Mode == "member" {
		if mr, ok := p.members[t.MemberID]; ok && mr.status != "anonymized" {
			id := mr.id
			return &id
		}
	}
	return nil
}

func commentKey(issue string, c Comment, i int) string {
	if c.ID != "" {
		return issue + "#c:" + c.ID
	}
	return issue + "#c:" + digest(c.Created, refOf(c.Author), c.Body, strconv.Itoa(i))
}

func worklogKey(issue string, w Worklog, i int) string {
	if w.ID != "" {
		return issue + "#w:" + w.ID
	}
	return issue + "#w:" + digest(w.Started, refOf(w.Author), strconv.FormatFloat(w.Minutes, 'f', 2, 64), w.Comment, strconv.Itoa(i))
}

func refOf(u *UserRef) string {
	if u == nil {
		return ""
	}
	return u.Ref
}

func digest(parts ...string) string {
	h := sha1.New()
	for _, s := range parts {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// convert legge un valore per il tipo di dato del campo di destinazione.
func (p *plan) convert(f *fieldMeta, v string) (any, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	switch f.dataType {
	case "string":
		if utf8.RuneCountInString(v) > 1000 {
			v = string([]rune(v)[:1000])
		}
		return v, nil
	case "text":
		if utf8.RuneCountInString(v) > 100_000 {
			v = string([]rune(v)[:100_000])
		}
		return v, nil
	case "url":
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(v, " \t\n") || len(v) > 2000 {
			return nil, fmt.Errorf("«%s» non è un indirizzo web", short(v))
		}
		return v, nil
	case "integer", "decimal":
		n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("«%s» non è un numero", short(v))
		}
		if f.dataType == "integer" {
			n = math.Round(n)
		}
		return n, nil
	case "boolean":
		switch strings.ToLower(v) {
		case "sì", "si", "yes", "true", "1", "x", "vero":
			return true, nil
		case "no", "false", "0", "falso":
			return false, nil
		}
		return nil, fmt.Errorf("«%s» non è sì o no", short(v))
	case "date", "datetime":
		t, err := parseDate(v, p.dateFmt, p.loc)
		if err != nil {
			return nil, fmt.Errorf("«%s» non è una data", short(v))
		}
		if f.dataType == "date" {
			return dayOnly(t, p.loc).Format(time.DateOnly), nil
		}
		return t.UTC().Format(time.RFC3339), nil
	case "duration":
		mins, err := parseDurationText(v)
		if err != nil || mins < 0 {
			return nil, fmt.Errorf("«%s» non è una durata", short(v))
		}
		return math.Round(mins), nil
	case "enum":
		return p.optionName(f, v), nil
	case "member":
		for ref, t := range p.m.Users {
			if t.Mode != "member" {
				continue
			}
			if strings.EqualFold(ref, v) {
				return t.MemberID, nil
			}
		}
		for _, mr := range p.members {
			if strings.EqualFold(mr.name, v) || (mr.email != "" && strings.EqualFold(mr.email, v)) {
				return mr.id, nil
			}
		}
		return nil, fmt.Errorf("persona «%s» senza corrispondenza", short(v))
	}
	return nil, fmt.Errorf("tipo di campo %s non importabile", f.dataType)
}

// resolveExisting: le issue già importate (stessa chiave d'origine) si aggiornano.
func (p *plan) resolveExisting(ctx context.Context, tx pgx.Tx, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `select o.source_key, e.id::text, e.version, t.key from import_origins o
		join elements e on e.tenant_id = o.tenant_id and e.id = o.target_id and e.deleted_at is null
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where o.system = $1 and o.kind = 'element' and o.source_key = any($2)`, p.system, keys)
	if err != nil {
		return err
	}
	type ex struct {
		id      string
		version int
		typ     string
	}
	found := map[string]ex{}
	var k string
	var x ex
	if _, err := pgx.ForEachRow(rows, []any{&k, &x.id, &x.version, &x.typ}, func() error { found[k] = x; return nil }); err != nil {
		return err
	}
	for _, pi := range p.issues {
		e, ok := found[pi.src.Key]
		if !ok {
			continue
		}
		pi.id, pi.existing, pi.version = e.id, true, e.version
		if e.typ != pi.typ {
			// Il tipo non cambia su un elemento esistente: si resta sul suo processo.
			p.addWarning(pi.src, "type", "già importato come %s: il tipo resta quello", e.typ)
			pi.typ = e.typ
			if tt := p.types[e.typ]; tt != nil {
				found := false
				for _, s := range tt.info.States {
					if strings.EqualFold(s.Name, pi.state.Name) {
						pi.state, found = s, true
					}
				}
				if !found {
					pi.state = tt.initial
				}
			}
		}
	}
	// Commenti e ore già importati non si ripetono.
	var ckeys, wkeys []string
	for _, pi := range p.issues {
		if !pi.existing {
			continue
		}
		for _, c := range pi.comments {
			ckeys = append(ckeys, c.key)
		}
		for _, w := range pi.worklogs {
			wkeys = append(wkeys, w.key)
		}
	}
	seen := map[string]bool{}
	for kind, list := range map[string][]string{"comment": ckeys, "work_log": wkeys} {
		if len(list) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, `select source_key from import_origins where system = $1 and kind = $2 and source_key = any($3)`, p.system, kind, list)
		if err != nil {
			return err
		}
		var sk string
		if _, err := pgx.ForEachRow(rows, []any{&sk}, func() error { seen[sk] = true; return nil }); err != nil {
			return err
		}
	}
	for _, pi := range p.issues {
		if !pi.existing {
			continue
		}
		cs := pi.comments[:0]
		for _, c := range pi.comments {
			if !seen[c.key] {
				cs = append(cs, c)
			}
		}
		pi.comments = cs
		ws := pi.worklogs[:0]
		for _, w := range pi.worklogs {
			if !seen[w.key] {
				ws = append(ws, w)
			}
		}
		pi.worklogs = ws
	}
	return nil
}

// resolveSprints: gli sprint usati dalle issue (e quelli letti dall'API), nuovi o già
// importati nello spazio.
func (p *plan) resolveSprints(ctx context.Context, tx pgx.Tx) error {
	if !p.m.Include.Sprints {
		return nil
	}
	info := map[string]SprintInfo{}
	for _, s := range p.ds.Sprints {
		info[s.Name] = s
	}
	idx := map[string]int{}
	// Tutti gli sprint citati si creano (con le loro date: sono la storia del team); ogni
	// elemento è pianificato nell'ultimo dell'elenco, il più recente.
	for _, pi := range p.issues {
		for i := len(pi.src.Sprints) - 1; i >= 0; i-- {
			name := pi.src.Sprints[i]
			t := p.m.Sprints[name]
			if t.Skip {
				continue
			}
			j, ok := idx[name]
			if !ok {
				ps := &pSprint{name: name, key: "sprint:" + p.space.key + ":" + name, id: db.NewID(), state: t.State}
				in := info[name]
				if ps.state == "" {
					ps.state = map[string]string{"future": "planned", "active": "active", "closed": "closed"}[strings.ToLower(in.State)]
				}
				if ps.state == "" {
					ps.state = "planned"
				}
				start, end := t.Start, t.End
				if start == "" {
					start = in.Start
				}
				if end == "" {
					end = in.End
				}
				for k, v := range map[**time.Time]string{&ps.start: start, &ps.end: end} {
					if v == "" {
						continue
					}
					if d, err := parseDate(v, p.dateFmt, p.loc); err == nil {
						x := dayOnly(d, p.loc)
						*k = &x
					}
				}
				ps.goal = in.Goal
				j = len(p.sprints)
				idx[name] = j
				p.sprints = append(p.sprints, ps)
			}
			if pi.sprint < 0 {
				pi.sprint = j
			}
		}
	}
	// Numeri degli sprint in ordine di inizio (poi di nome: «Sprint 2» prima di «Sprint 10»).
	order := make([]int, len(p.sprints))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		sa, sb := p.sprints[order[a]], p.sprints[order[b]]
		if sa.start != nil && sb.start != nil && !sa.start.Equal(*sb.start) {
			return sa.start.Before(*sb.start)
		}
		return naturalLess(sa.name, sb.name)
	})
	pos := make([]int, len(order))
	sorted := make([]*pSprint, len(order))
	for n, old := range order {
		pos[old], sorted[n] = n, p.sprints[old]
	}
	p.sprints = sorted
	for _, pi := range p.issues {
		if pi.sprint >= 0 {
			pi.sprint = pos[pi.sprint]
		}
	}
	if len(p.sprints) == 0 {
		return nil
	}
	keys := make([]string, len(p.sprints))
	for i, s := range p.sprints {
		keys[i] = s.key
	}
	rows, err := tx.Query(ctx, `select o.source_key, e.id::text from import_origins o
		join elements e on e.tenant_id = o.tenant_id and e.id = o.target_id and e.deleted_at is null
		where o.system = $1 and o.kind = 'element' and o.source_key = any($2)`, p.system, keys)
	if err != nil {
		return err
	}
	var k, id string
	if _, err := pgx.ForEachRow(rows, []any{&k, &id}, func() error {
		for _, s := range p.sprints {
			if s.key == k {
				s.id, s.existing = id, true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Un solo sprint attivo per spazio (il gate dei processi lo chiede): gli altri restano pianificati.
	active := 0
	if !p.space.isNew {
		if err := tx.QueryRow(ctx, `select count(*) from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
			join options o on o.tenant_id = e.tenant_id and o.id = e.state_id and o.category = 'in_progress'
			where e.space_id = $1 and e.deleted_at is null and not (e.id = any($2::uuid[]))`, p.space.id, sprintIDs(p.sprints)).Scan(&active); err != nil {
			return err
		}
	}
	for i := len(p.sprints) - 1; i >= 0; i-- {
		s := p.sprints[i]
		if s.state != "active" {
			continue
		}
		if active > 0 {
			s.state = "planned"
			p.warnings = append(p.warnings, fmt.Sprintf("lo sprint «%s» resta pianificato: nello spazio c'è già uno sprint attivo", s.name))
			continue
		}
		active++
	}
	return nil
}

func sprintIDs(list []*pSprint) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s.existing {
			out = append(out, s.id)
		}
	}
	return out
}

// resolveParents: il genitore è nel file (per chiave o id) o un elemento già importato.
func (p *plan) resolveParents(ctx context.Context, tx pgx.Tx, byKey, byID map[string]int) error {
	var missing []string
	for _, pi := range p.issues {
		ref := strings.TrimSpace(pi.src.Parent)
		if ref == "" {
			continue
		}
		if j, ok := byKey[strings.ToUpper(ref)]; ok {
			pi.parent = j
		} else if j, ok := byID[ref]; ok {
			pi.parent = j
		} else {
			missing = append(missing, ref)
		}
	}
	existing := map[string]string{}
	if len(missing) > 0 {
		rows, err := tx.Query(ctx, `select o.source_key, e.id::text from import_origins o
			join elements e on e.tenant_id = o.tenant_id and e.id = o.target_id and e.deleted_at is null
			where o.system = $1 and o.kind = 'element' and o.source_key = any($2)`, p.system, missing)
		if err != nil {
			return err
		}
		var k, id string
		if _, err := pgx.ForEachRow(rows, []any{&k, &id}, func() error { existing[strings.ToUpper(k)] = id; return nil }); err != nil {
			return err
		}
	}
	for i, pi := range p.issues {
		ref := strings.TrimSpace(pi.src.Parent)
		if ref == "" || pi.parent >= 0 {
			if pi.parent == i {
				pi.parent = -1
			}
			continue
		}
		if id, ok := existing[strings.ToUpper(ref)]; ok {
			pi.parentID = &id
			continue
		}
		p.addWarning(pi.src, "parent", "genitore %s non trovato: l'elemento resta senza genitore", ref)
	}
	// Cicli (dati incoerenti): si spezzano.
	for i, pi := range p.issues {
		seen := map[int]bool{i: true}
		j := pi.parent
		for j >= 0 {
			if seen[j] {
				p.addWarning(pi.src, "parent", "gerarchia circolare: l'elemento resta senza genitore")
				pi.parent = -1
				break
			}
			seen[j] = true
			j = p.issues[j].parent
		}
	}
	for _, pi := range p.issues {
		if pi.parent >= 0 {
			id := p.issues[pi.parent].id
			pi.parentID = &id
			if !standardParent(p.issues[pi.parent].typ, pi.typ) {
				p.addWarning(pi.src, "parent", "%s sotto %s: in Regia la gerarchia tipica è epic > story > task", pi.typ, p.issues[pi.parent].typ)
			}
		}
	}
	return nil
}

func standardParent(parent, child string) bool {
	switch child {
	case "story":
		return parent == "epic"
	case "task":
		return parent == "story" || parent == "bug"
	case "bug":
		return parent == "epic" || parent == "story"
	}
	return false
}

// resolveLinks: collegamenti tra issue → relazioni, senza doppioni.
func (p *plan) resolveLinks(ctx context.Context, tx pgx.Tx, byKey map[string]int) error {
	if !p.m.Include.Links {
		return nil
	}
	var external []string
	for _, pi := range p.issues {
		for _, l := range pi.src.Links {
			if _, ok := byKey[strings.ToUpper(l.Target)]; !ok {
				external = append(external, l.Target)
			}
		}
	}
	existing := map[string]string{}
	if len(external) > 0 {
		rows, err := tx.Query(ctx, `select o.source_key, e.id::text from import_origins o
			join elements e on e.tenant_id = o.tenant_id and e.id = o.target_id and e.deleted_at is null
			where o.system = $1 and o.kind = 'element' and o.source_key = any($2)`, p.system, external)
		if err != nil {
			return err
		}
		var k, id string
		if _, err := pgx.ForEachRow(rows, []any{&k, &id}, func() error { existing[strings.ToUpper(k)] = id; return nil }); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	missing := 0
	for i, pi := range p.issues {
		for _, l := range pi.src.Links {
			rel, ok := p.m.Links[l.Type]
			if !ok {
				rel = defaultRelation(l.Type)
			}
			if rel == "" {
				continue
			}
			if _, ok := p.relTypes[rel]; !ok && !builtinRelation(rel) {
				p.addWarning(pi.src, "links", "tipo di relazione %q inesistente: il collegamento si salta", rel)
				continue
			}
			other := -1
			otherID := ""
			if j, ok := byKey[strings.ToUpper(l.Target)]; ok {
				other = j
			} else if id, ok := existing[strings.ToUpper(l.Target)]; ok {
				otherID = id
			} else {
				missing++
				continue
			}
			if other == i {
				continue
			}
			lk := pLink{relType: rel, source: i, target: other, target2: otherID}
			if !l.Out {
				lk = pLink{relType: rel, source: other, sourceID: otherID, target: i}
			}
			a, b := p.linkEnd(lk.source, lk.sourceID), p.linkEnd(lk.target, lk.target2)
			sig := rel + "|" + a + "|" + b
			if rel == "relates" && b < a {
				sig = rel + "|" + b + "|" + a
			}
			if seen[sig] {
				continue
			}
			seen[sig] = true
			p.links = append(p.links, lk)
		}
	}
	if missing > 0 {
		p.warnings = append(p.warnings, fmt.Sprintf("%d collegamenti verso elementi che non sono nel file né già importati: si saltano", missing))
	}
	return nil
}

func (p *plan) linkEnd(idx int, id string) string {
	if idx >= 0 {
		return p.issues[idx].id
	}
	return id
}

// resolveNumbers: con la numerazione d'origine il numero viene dalla chiave (PROJ-123).
func (p *plan) resolveNumbers(ctx context.Context, tx pgx.Tx) error {
	if !p.keepNumbers {
		return nil
	}
	used := map[int64]string{}
	if !p.space.isNew {
		rows, err := tx.Query(ctx, `select number, coalesce(o.source_key, '') from elements e
			left join import_origins o on o.tenant_id = e.tenant_id and o.target_id = e.id and o.kind = 'element'
			where e.space_id = $1 and e.number is not null`, p.space.id)
		if err != nil {
			return err
		}
		var n int64
		var k string
		if _, err := pgx.ForEachRow(rows, []any{&n, &k}, func() error {
			if k == "" {
				k = p.space.key + "-" + strconv.FormatInt(n, 10)
			}
			used[n] = k
			return nil
		}); err != nil {
			return err
		}
	}
	for _, pi := range p.issues {
		if pi.existing {
			continue
		}
		m := keyLikeRe.FindStringSubmatch(pi.src.Key)
		if m == nil || !strings.EqualFold(m[1], p.space.key) {
			continue
		}
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		if prev, dup := used[n]; dup {
			p.addWarning(pi.src, "key", "numero %d già usato da %s: riceve un numero nuovo", n, prev)
			continue
		}
		used[n] = pi.src.Key
		pi.number = n
	}
	return nil
}

// order mette i genitori prima dei figli (la chiave esterna parent_id si controlla a ogni
// istruzione) e, a pari profondità, segue l'ordine del file.
func (p *plan) order() {
	for _, pi := range p.issues {
		d := 0
		for j := pi.parent; j >= 0 && d < 50; j = p.issues[j].parent {
			d++
		}
		pi.depth = d
	}
	idx := make([]int, len(p.issues))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return p.issues[idx[a]].depth < p.issues[idx[b]].depth })
	pos := make([]int, len(idx))
	out := make([]*pIssue, len(idx))
	for newPos, old := range idx {
		pos[old] = newPos
		out[newPos] = p.issues[old]
	}
	for _, pi := range out {
		if pi.parent >= 0 {
			pi.parent = pos[pi.parent]
		}
	}
	for i := range p.links {
		if p.links[i].source >= 0 {
			p.links[i].source = pos[p.links[i].source]
		}
		if p.links[i].target >= 0 {
			p.links[i].target = pos[p.links[i].target]
		}
	}
	p.issues = out
}

// ---------------------------------------------------------------------
// Proposte di default

// defaultType: il tipo di Regia per i nomi comuni dei tipi d'origine.
func defaultType(src string) string {
	s := strings.ToLower(strings.TrimSpace(src))
	switch {
	case s == "epic" || s == "epica" || strings.Contains(s, "epic"):
		return "epic"
	case s == "bug" || strings.Contains(s, "bug") || strings.Contains(s, "defect") || strings.Contains(s, "difetto") || s == "incident" || s == "problema":
		return "bug"
	case strings.Contains(s, "sub-task") || strings.Contains(s, "subtask") || strings.Contains(s, "sottotask") || s == "task" || s == "attività" || s == "compito":
		return "task"
	}
	return "story"
}

// defaultPhase: la fase del tipo con la categoria dedotta dallo stato.
func defaultPhase(tt *typeTarget, status, cat string) string {
	g := guessCategory(status)
	if g == "" {
		switch strings.ToLower(cat) {
		case "to do", "da fare", "new":
			g = "todo"
		case "in progress", "in corso", "indeterminate":
			g = "in_progress"
		case "done", "fatto", "completata":
			g = "done"
		}
	}
	for _, s := range tt.info.States {
		if strings.EqualFold(s.Name, status) {
			return s.Name
		}
	}
	pick := func(cat string, nth int) string {
		i := 0
		for _, s := range tt.info.States {
			if s.Category == cat && !s.OffPath {
				if i == nth {
					return s.Name
				}
				i++
			}
		}
		for _, s := range tt.info.States {
			if s.Category == cat {
				return s.Name
			}
		}
		return ""
	}
	switch g {
	case "todo":
		return pick("todo", 0)
	case "in_progress":
		return pick("in_progress", 0)
	case "review":
		if n := pick("in_progress", 1); n != "" {
			return n
		}
		return pick("in_progress", 0)
	case "verify":
		for _, s := range tt.info.States {
			if strings.EqualFold(s.Name, "Da verificare") {
				return s.Name
			}
		}
		if n := pick("in_progress", 1); n != "" {
			return n
		}
		return pick("in_progress", 0)
	case "done":
		return pick("done", 0)
	case "cancelled":
		return pick("cancelled", 0)
	}
	return ""
}

// defaultPriority: priorità comuni di Jira e YouTrack → opzioni di Priorità (Bloccante,
// Alta, Normale, Bassa); senza corrispondenza si crea l'opzione con lo stesso nome.
func defaultPriority(f *fieldMeta, src string) string {
	if f == nil {
		return src
	}
	if n, ok := f.optionNames[strings.ToLower(src)]; ok {
		return n
	}
	s := strings.ToLower(src)
	var want []string
	switch s {
	case "highest", "blocker", "show-stopper", "showstopper", "critical", "urgent", "bloccante", "critica", "urgente":
		want = []string{"bloccante", "alta"}
	case "high", "major", "alta", "important":
		want = []string{"alta"}
	case "medium", "normal", "normale", "media":
		want = []string{"normale", "media"}
	case "low", "lowest", "minor", "trivial", "bassa", "minore":
		want = []string{"bassa"}
	}
	for _, w := range want {
		if n, ok := f.optionNames[w]; ok {
			return n
		}
	}
	return src
}

// defaultRelation: tipi di collegamento comuni → relazioni di Regia.
func defaultRelation(t string) string {
	s := strings.ToLower(t)
	switch {
	case strings.Contains(s, "block") || strings.Contains(s, "depend") || strings.Contains(s, "blocc"):
		return "blocks"
	case strings.Contains(s, "duplic"):
		return "duplicates"
	case strings.Contains(s, "subtask"):
		return ""
	}
	return "relates"
}

// relazioni create dall'import se mancano.
var builtinRelations = []struct {
	key, out, in string
	directed     bool
}{
	{"blocks", "blocca", "è bloccato da", true},
	{"relates", "collegato a", "collegato a", false},
	{"duplicates", "duplica", "è duplicato da", true},
}

func builtinRelation(k string) bool {
	for _, r := range builtinRelations {
		if r.key == k {
			return true
		}
	}
	return false
}
