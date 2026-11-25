package architecture

// Import ed export del catalogo esteso (replica/architettura-ea.md §7): capability e
// dipendenze in CSV (sul modello dell'import delle applicazioni: prova senza salvare,
// savepoint per riga) e l'intero catalogo in JSON (formato regia.ea/v1).
//
// CSV delle capability:  codice; nome; padre; criticita; descrizione; applicazioni; asset
//   "padre" è il codice o il nome della capability che la contiene; "applicazioni" e
//   "asset" sono codici o nomi separati da "|". La capability si trova per codice, se
//   c'è, altrimenti per nome.
// CSV delle dipendenze: origine; destinazione; tipo; protocollo; criticita; dati; classificazione
//   origine e destinazione sono chiavi, codici o nomi; tipo è la chiave o il nome diretto
//   ("chiama le API di"). Una dipendenza già presente si aggiorna negli attributi.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

const EAFormat = "regia.ea/v1"

var capHeaders = map[string]string{
	"codice": "code", "code": "code",
	"nome": "name", "name": "name", "capability": "name",
	"padre": "parent", "parent": "parent", "genitore": "parent",
	"criticita": "criticality", "criticità": "criticality", "criticality": "criticality", "importanza": "criticality",
	"descrizione": "description", "description": "description",
	"applicazioni": "apps", "applications": "apps", "supportata_da": "apps",
	"asset": "assets", "assets": "assets", "asset_logici": "assets",
	"posizione": "position", "position": "position",
}

var depHeaders = map[string]string{
	"origine": "source", "source": "source", "da": "source", "from": "source",
	"destinazione": "target", "target": "target", "a": "target", "to": "target",
	"tipo": "type", "type": "type", "relazione": "type",
	"protocollo": "protocol", "protocol": "protocol",
	"criticita": "criticality", "criticità": "criticality", "criticality": "criticality",
	"dati": "data", "data": "data", "dati_scambiati": "data",
	"classificazione": "classification", "classification": "classification", "classificazione_dati": "classification",
}

type csvRows struct {
	cols map[string]int
	recs [][]string
	line []int
}

func readCSV(data string, aliases map[string]string, required ...string) (*csvRows, error) {
	data = strings.TrimPrefix(data, bom)
	first, _, _ := strings.Cut(data, "\n")
	r := csv.NewReader(strings.NewReader(data))
	if strings.Count(first, ";") >= strings.Count(first, ",") {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, engine.InvalidParams("validation.architecture.csv_unreadable", "csv", map[string]any{"detail": err}, "file vuoto o non leggibile: %v", err)
	}
	out := &csvRows{cols: map[string]int{}}
	for i, h := range header {
		key, ok := aliases[strings.ToLower(strings.TrimSpace(h))]
		if !ok {
			return nil, engine.InvalidValue("csv", "colonna %q non riconosciuta", h)
		}
		out.cols[key] = i
	}
	for _, k := range required {
		if _, ok := out.cols[k]; !ok {
			return nil, engine.Required("csv", "manca la colonna %q", k)
		}
	}
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return nil, engine.InvalidParams("validation.architecture.csv_row", "csv", map[string]any{"row": line, "detail": err}, "riga %d: %v", line, err)
		}
		if strings.TrimSpace(strings.Join(rec, "")) == "" {
			continue
		}
		if len(out.recs) >= maxImportRows {
			return nil, engine.InvalidParams("validation.architecture.import_too_many_rows", "csv", map[string]any{"max": maxImportRows}, "troppe righe (massimo %d per import)", maxImportRows)
		}
		out.recs = append(out.recs, rec)
		out.line = append(out.line, line)
	}
	return out, nil
}

func (c *csvRows) getter(i int) func(string) string {
	rec := c.recs[i]
	return func(k string) string {
		if j, ok := c.cols[k]; ok && j < len(rec) {
			return strings.TrimSpace(rec[j])
		}
		return ""
	}
}

type EAImportRow struct {
	Kind    string `json:"kind"` // capability | dependency | technology | relation_type
	Line    int    `json:"line"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // created | updated | error
	Message string `json:"message,omitempty"`
}

type EAImportReport struct {
	DryRun  bool          `json:"dry_run"`
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Errors  int           `json:"errors"`
	Rows    []EAImportRow `json:"rows"`
}

func (rep *EAImportReport) add(row EAImportRow) {
	switch row.Outcome {
	case "created":
		rep.Created++
	case "updated":
		rep.Updated++
	default:
		rep.Errors++
	}
	rep.Rows = append(rep.Rows, row)
}

// inSavepoint esegue fn in un savepoint: un errore di validazione annulla solo la riga.
func inSavepoint(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) (string, error)) (string, string, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	outcome, err := fn(sp)
	if err != nil {
		_ = sp.Rollback(ctx)
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return "error", ve.Error(), nil
		}
		if errors.Is(err, engine.ErrNotFound) {
			return "error", "elemento non trovato", nil
		}
		return "", "", err
	}
	return outcome, "", sp.Commit(ctx)
}

type EAImportInput struct {
	Kind string    `json:"kind"` // capabilities | dependencies (per il CSV)
	CSV  string    `json:"csv"`
	JSON *EABundle `json:"json"`
}

// ImportCatalog importa capability o dipendenze da CSV, oppure un catalogo JSON. Con
// dryRun restituisce l'esito e ErrDryRun, che fa annullare la transazione.
func (s *Service) ImportCatalog(ctx context.Context, tx pgx.Tx, actor engine.Actor, in EAImportInput, dryRun bool) (*EAImportReport, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	rep := &EAImportReport{DryRun: dryRun, Rows: []EAImportRow{}}
	var err error
	switch {
	case in.JSON != nil:
		err = s.importBundle(ctx, tx, actor, in.JSON, rep)
	case in.Kind == "capabilities":
		var rows *csvRows
		if rows, err = readCSV(in.CSV, capHeaders, "name"); err == nil {
			err = s.importCapabilities(ctx, tx, actor, rows, rep)
		}
	case in.Kind == "dependencies":
		var rows *csvRows
		if rows, err = readCSV(in.CSV, depHeaders, "source", "target", "type"); err == nil {
			err = s.importDependencies(ctx, tx, actor, rows, rep)
		}
	default:
		return nil, engine.Required("kind", "indica cosa importare (capabilities, dependencies) oppure un catalogo JSON")
	}
	if err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "architecture.import", "catalog", "", map[string]any{"kind": in.Kind, "created": rep.Created,
		"updated": rep.Updated, "errors": rep.Errors, "dry_run": dryRun}); err != nil {
		return nil, err
	}
	if dryRun {
		return rep, ErrDryRun
	}
	return rep, nil
}

type capRow struct {
	line int
	get  func(string) string
}

func (s *Service) importCapabilities(ctx context.Context, tx pgx.Tx, actor engine.Actor, rows *csvRows, rep *EAImportReport) error {
	pending := make([]capRow, len(rows.recs))
	for i := range rows.recs {
		pending[i] = capRow{rows.line[i], rows.getter(i)}
	}
	// Un padre può comparire più avanti nel file: le righe il cui padre non esiste ancora
	// aspettano il giro successivo, finché qualcosa avanza.
	names := map[string]bool{}
	for _, r := range pending {
		names[strings.ToLower(r.get("name"))], names[strings.ToLower(r.get("code"))] = true, true
	}
	for len(pending) > 0 {
		var wait []capRow
		for _, r := range pending {
			if p := r.get("parent"); p != "" && names[strings.ToLower(p)] {
				id, err := capabilityByCodeOrName(ctx, tx, p)
				if err != nil {
					return err
				}
				if id == "" {
					wait = append(wait, r)
					continue
				}
			}
			if err := s.importCapRow(ctx, tx, actor, r, rep); err != nil {
				return err
			}
		}
		if len(wait) == len(pending) {
			for _, r := range wait {
				rep.add(EAImportRow{Kind: "capability", Line: r.line, Name: r.get("name"), Outcome: "error",
					Message: "padre: la capability «" + r.get("parent") + "» non è stata importata (padre mancante o ciclo)"})
			}
			break
		}
		pending = wait
	}
	return nil
}

func (s *Service) importCapRow(ctx context.Context, tx pgx.Tx, actor engine.Actor, r capRow, rep *EAImportReport) error {
	get := r.get
	outcome, msg, err := inSavepoint(ctx, tx, func(tx pgx.Tx) (string, error) {
		name := get("name")
		if name == "" {
			return "", engine.Required("nome", "manca il nome")
		}
		in := CapabilityInput{Name: &name}
		set := func(k string, dst **string) {
			if v := get(k); v != "" {
				*dst = &v
			}
		}
		set("description", &in.Description)
		set("code", &in.Code)
		set("criticality", &in.Criticality)
		parent := ""
		if p := get("parent"); p != "" {
			id, err := capabilityByCodeOrName(ctx, tx, p)
			if err != nil {
				return "", err
			}
			if id == "" {
				return "", engine.NotFound("padre", "", "capability %q inesistente", p)
			}
			parent = id
		}
		in.ParentID = &parent
		if v := get("position"); v != "" {
			n, err := atoiField("posizione", v)
			if err != nil {
				return "", err
			}
			in.Position = &n
		}
		ref := get("code")
		if ref == "" {
			ref = name
		}
		existing, err := resolveNode(ctx, tx, ref, []string{CapabilityType})
		if err != nil {
			return "", err
		}
		outcome := "updated"
		var id string
		if existing == nil {
			outcome = "created"
			if id, err = s.createCapability(ctx, tx, actor, in); err != nil {
				return "", err
			}
		} else {
			id = existing.ID
			if _, err := s.UpdateCapability(ctx, tx, actor, id, in); err != nil {
				return "", err
			}
		}
		for _, list := range [][2]string{{"apps", AppType}, {"assets", AssetType}} {
			for _, ref := range strings.Split(get(list[0]), "|") {
				ref = strings.TrimSpace(ref)
				if ref == "" {
					continue
				}
				n, err := resolveNode(ctx, tx, ref, []string{list[1]})
				if err != nil {
					return "", err
				}
				if n == nil {
					what := "applicazione"
					if list[1] == AssetType {
						what = "asset logico"
					}
					return "", engine.NotFound(list[0], "", "%s %q inesistente", what, ref)
				}
				if err := s.LinkCapability(ctx, tx, actor, id, n.ID); err != nil {
					return "", err
				}
			}
		}
		return outcome, nil
	})
	if err != nil {
		return err
	}
	rep.add(EAImportRow{Kind: "capability", Line: r.line, Name: get("name"), Outcome: outcome, Message: msg})
	return nil
}

func atoiField(field, v string) (int, error) {
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' || n > 1e8 {
			return 0, engine.InvalidParams("validation.architecture.integer_invalid", field, map[string]any{"value": v}, "%q non è un numero intero", v)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func (s *Service) importDependencies(ctx context.Context, tx pgx.Tx, actor engine.Actor, rows *csvRows, rep *EAImportReport) error {
	for i := range rows.recs {
		get := rows.getter(i)
		attrs := map[string]any{}
		for _, k := range []string{"protocol", "criticality", "data", "classification"} {
			if v := get(k); v != "" {
				attrs[k] = v
			}
		}
		if err := s.importDependency(ctx, tx, actor, rows.line[i], get("source"), get("target"), get("type"), attrs, rep); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) importDependency(ctx context.Context, tx pgx.Tx, actor engine.Actor, line int, source, target, typ string, attrs map[string]any, rep *EAImportReport) error {
	outcome, msg, err := inSavepoint(ctx, tx, func(tx pgx.Tx) (string, error) {
		rt, err := relationType(ctx, tx, typ, catDependency)
		if err != nil {
			return "", err
		}
		if rt == nil {
			return "", engine.NotFound("tipo", "type", "tipo di relazione %q inesistente", typ)
		}
		src, err := resolveNode(ctx, tx, source, nil)
		if err != nil {
			return "", err
		}
		tgt, err := resolveNode(ctx, tx, target, nil)
		if err != nil {
			return "", err
		}
		if src != nil && tgt != nil {
			var id string
			err := tx.QueryRow(ctx, `select id from relations where type_id = $1 and source_id = $2 and target_id = $3`, rt.ID, src.ID, tgt.ID).Scan(&id)
			if err == nil {
				if _, err := s.UpdateRelation(ctx, tx, actor, id, attrs); err != nil {
					return "", err
				}
				return "updated", nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return "", err
			}
		}
		if _, err := s.createRelation(ctx, tx, actor, RelationInput{Type: rt.Key, Source: source, Target: target, Attrs: attrs}, false); err != nil {
			return "", err
		}
		return "created", nil
	})
	if err != nil {
		return err
	}
	rep.add(EAImportRow{Kind: "dependency", Line: line, Name: source + " → " + target, Outcome: outcome, Message: msg})
	return nil
}

// ---------------------------------------------------------------------------
// Export

type EACapability struct {
	Code         string   `json:"code,omitempty"`
	Name         string   `json:"name"`
	Parent       string   `json:"parent,omitempty"`
	Criticality  string   `json:"criticality,omitempty"`
	Description  string   `json:"description,omitempty"`
	Position     int      `json:"position"`
	Applications []string `json:"applications,omitempty"`
	Assets       []string `json:"assets,omitempty"`
}

type EADependency struct {
	Source string         `json:"source"`
	Target string         `json:"target"`
	Type   string         `json:"type"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

type EATechnology struct {
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Vendor       string   `json:"vendor,omitempty"`
	Category     string   `json:"category,omitempty"`
	EndOfSupport string   `json:"end_of_support,omitempty"`
	EndOfLife    string   `json:"end_of_life,omitempty"`
	Applications []string `json:"applications,omitempty"`
}

type EATime struct {
	Application string `json:"application"`
	Rating      string `json:"time_rating"`
	Note        string `json:"note,omitempty"`
}

type EABundle struct {
	Format        string              `json:"format"`
	ExportedAt    string              `json:"exported_at,omitempty"`
	RelationTypes []RelationTypeInput `json:"relation_types,omitempty"`
	Capabilities  []EACapability      `json:"capabilities"`
	Dependencies  []EADependency      `json:"dependencies"`
	Technologies  []EATechnology      `json:"technologies,omitempty"`
	Time          []EATime            `json:"time,omitempty"`
}

// appRef: il codice dell'applicazione se c'è, altrimenti la chiave.
func appRef(code *string, key string) string {
	if code != nil && *code != "" {
		return *code
	}
	return key
}

func (s *Service) ExportCatalog(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*EABundle, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	b := &EABundle{Format: EAFormat, ExportedAt: time.Now().UTC().Format(time.RFC3339), Capabilities: []EACapability{}, Dependencies: []EADependency{}}
	caps, err := capabilities(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	refOf := map[string]string{}
	for _, c := range caps {
		refOf[c.ID] = appRef(c.Code, c.Name)
	}
	for _, c := range caps {
		ec := EACapability{Code: deref(c.Code), Name: c.Name, Criticality: deref(c.Criticality), Description: c.Description, Position: c.Position}
		if c.ParentID != nil {
			ec.Parent = refOf[*c.ParentID]
		}
		for _, a := range c.Apps {
			ec.Applications = append(ec.Applications, appRef(a.Code, a.Key))
		}
		for _, a := range c.Assets {
			ec.Assets = append(ec.Assets, a.Name)
		}
		b.Capabilities = append(b.Capabilities, ec)
	}
	rels, err := s.Relations(ctx, tx, actor, catDependency, "")
	if err != nil {
		return nil, err
	}
	for _, r := range rels {
		d := EADependency{Source: appRef(r.SourceCode, r.SourceKey), Target: appRef(r.TargetCode, r.TargetKey), Type: r.Type}
		if len(r.Attrs) > 0 {
			d.Attrs = r.Attrs
		}
		b.Dependencies = append(b.Dependencies, d)
	}
	types, err := s.RelationTypes(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.System {
			continue
		}
		t := t
		b.RelationTypes = append(b.RelationTypes, RelationTypeInput{Key: t.Key, OutwardName: &t.OutwardName, InwardName: &t.InwardName,
			Category: &t.Category, ImpactDirection: &t.ImpactDirection, SourceTypes: &t.SourceTypes, TargetTypes: &t.TargetTypes, Schema: t.Schema})
	}
	rows, err := tx.Query(ctx, `select t.name, t.version, t.vendor, t.category, coalesce(to_char(t.end_of_support, 'YYYY-MM-DD'), ''),
		coalesce(to_char(t.end_of_life, 'YYYY-MM-DD'), ''),
		coalesce((select array_agg(coalesce(a.code, s.key || '-' || e.number) order by e.title) from application_technologies at
		  join elements e on e.tenant_id = at.tenant_id and e.id = at.application_id and e.deleted_at is null
		  join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		  join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		  where at.technology_id = t.id), '{}')
		from technologies t order by lower(t.name), t.version`)
	if err != nil {
		return nil, err
	}
	if b.Technologies, err = pgx.CollectRows(rows, pgx.RowToStructByPos[EATechnology]); err != nil {
		return nil, err
	}
	trows, err := tx.Query(ctx, `select coalesce(a.code, s.key || '-' || e.number), a.time_rating, a.time_note from ext_application a
		join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where a.time_rating is not null order by e.title`)
	if err != nil {
		return nil, err
	}
	b.Time, err = pgx.CollectRows(trows, pgx.RowToStructByPos[EATime])
	return b, err
}

// ExportCSV restituisce capability o dipendenze nel formato dell'import.
func (s *Service) ExportCSV(ctx context.Context, tx pgx.Tx, actor engine.Actor, what string) ([]byte, error) {
	b, err := s.ExportCatalog(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(bom)
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	switch what {
	case "capabilities":
		_ = w.Write([]string{"codice", "nome", "padre", "criticita", "descrizione", "applicazioni", "asset"})
		for _, c := range b.Capabilities {
			crit := c.Criticality
			if n, ok := critNames[crit]; ok {
				crit = n
			}
			_ = w.Write([]string{c.Code, c.Name, c.Parent, crit, c.Description, strings.Join(c.Applications, "|"), strings.Join(c.Assets, "|")})
		}
	case "dependencies":
		_ = w.Write([]string{"origine", "destinazione", "tipo", "protocollo", "criticita", "dati", "classificazione"})
		for _, d := range b.Dependencies {
			str := func(k string) string {
				if v, ok := d.Attrs[k]; ok {
					if k == "criticality" {
						if n, ok := critNames[v.(string)]; ok {
							return n
						}
					}
					if sv, ok := v.(string); ok {
						return sv
					}
					j, _ := json.Marshal(v)
					return string(j)
				}
				return ""
			}
			_ = w.Write([]string{d.Source, d.Target, d.Type, str("protocol"), str("criticality"), str("data"), str("classification")})
		}
	default:
		return nil, engine.Required("what", "indica capabilities o dependencies")
	}
	w.Flush()
	return buf.Bytes(), nil
}

// importBundle importa un catalogo JSON: tipi di relazione, capability, dipendenze,
// tecnologie e valutazioni TIME, in quest'ordine.
func (s *Service) importBundle(ctx context.Context, tx pgx.Tx, actor engine.Actor, b *EABundle, rep *EAImportReport) error {
	if b.Format != EAFormat {
		return engine.InvalidValue("format", "formato %q non riconosciuto (atteso %s)", b.Format, EAFormat)
	}
	if n := len(b.Capabilities) + len(b.Dependencies) + len(b.Technologies) + len(b.Time); n > maxImportRows*4 {
		return engine.InvalidCode("validation.architecture.catalog_too_big", "json", "catalogo troppo grande")
	}
	for i, t := range b.RelationTypes {
		outcome, msg, err := inSavepoint(ctx, tx, func(tx pgx.Tx) (string, error) {
			if existing, err := relationType(ctx, tx, t.Key); err != nil {
				return "", err
			} else if existing != nil {
				t.Key = existing.Key
				if _, err := s.UpdateRelationType(ctx, tx, actor, existing.Key, t); err != nil {
					return "", err
				}
				return "updated", nil
			}
			if _, err := s.CreateRelationType(ctx, tx, actor, t); err != nil {
				return "", err
			}
			return "created", nil
		})
		if err != nil {
			return err
		}
		rep.add(EAImportRow{Kind: "relation_type", Line: i + 1, Name: t.Key, Outcome: outcome, Message: msg})
	}
	// Le capability passano dallo stesso percorso del CSV.
	rows := &csvRows{cols: map[string]int{"code": 0, "name": 1, "parent": 2, "criticality": 3, "description": 4, "apps": 5, "assets": 6, "position": 7}}
	for i, c := range b.Capabilities {
		rows.recs = append(rows.recs, []string{c.Code, c.Name, c.Parent, c.Criticality, c.Description,
			strings.Join(c.Applications, "|"), strings.Join(c.Assets, "|"), itoaPos(c.Position)})
		rows.line = append(rows.line, i+1)
	}
	if err := s.importCapabilities(ctx, tx, actor, rows, rep); err != nil {
		return err
	}
	for i, d := range b.Dependencies {
		if err := s.importDependency(ctx, tx, actor, i+1, d.Source, d.Target, d.Type, d.Attrs, rep); err != nil {
			return err
		}
	}
	for i, t := range b.Technologies {
		outcome, msg, err := inSavepoint(ctx, tx, func(tx pgx.Tx) (string, error) {
			var id string
			err := tx.QueryRow(ctx, `select id from technologies where lower(name) = lower($1) and lower(version) = lower($2)`, t.Name, t.Version).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", err
			}
			outcome := "updated"
			if id == "" {
				outcome = "created"
			}
			tech, err := s.SaveTechnology(ctx, tx, actor, id, TechnologyInput{Name: &t.Name, Version: &t.Version, Vendor: &t.Vendor, Category: &t.Category,
				EndOfSupport: &t.EndOfSupport, EndOfLife: &t.EndOfLife})
			if err != nil {
				return "", err
			}
			for _, a := range t.Applications {
				n, err := resolveNode(ctx, tx, a, []string{AppType})
				if err != nil {
					return "", err
				}
				if n == nil {
					return "", engine.NotFound("applications", "application", "applicazione %q inesistente", a)
				}
				if _, err := tx.Exec(ctx, `insert into application_technologies (tenant_id, application_id, technology_id)
					values (app_tenant(), $1, $2) on conflict do nothing`, n.ID, tech.ID); err != nil {
					return "", err
				}
			}
			return outcome, nil
		})
		if err != nil {
			return err
		}
		rep.add(EAImportRow{Kind: "technology", Line: i + 1, Name: strings.TrimSpace(t.Name + " " + t.Version), Outcome: outcome, Message: msg})
	}
	for i, t := range b.Time {
		outcome, msg, err := inSavepoint(ctx, tx, func(tx pgx.Tx) (string, error) {
			return "updated", s.SetTime(ctx, tx, actor, t.Application, TimeInput{Rating: t.Rating, Note: t.Note})
		})
		if err != nil {
			return err
		}
		rep.add(EAImportRow{Kind: "time", Line: i + 1, Name: t.Application, Outcome: outcome, Message: msg})
	}
	return nil
}

func itoaPos(n int) string {
	if n <= 0 {
		return ""
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
