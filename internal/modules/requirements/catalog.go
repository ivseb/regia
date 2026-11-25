package requirements

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Cataloghi di requisiti (sdlc.md §2, §6): voci con testo, categoria e criterio di
// verifica, e baseline per classe di applicazione decise da una condizione (per esempio
// "criticita: alta, critica" → baseline di sicurezza alta). Ogni catalogo è versionato
// come i modelli di questionario: una bozza, una versione corrente, le pubblicate immutabili.
// I requisiti creati da una voce ricordano catalogo, versione e baseline.

// Definition è il contenuto di una versione del catalogo.
type Definition struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Items       []Item     `json:"items"`
	Baselines   []Baseline `json:"baselines"`
}

// Item è una voce del catalogo.
type Item struct {
	Key          string       `json:"key"`
	Title        string       `json:"title"`
	Text         string       `json:"text,omitempty"`
	Category     string       `json:"category,omitempty"`
	Verification Verification `json:"verification"`
	// Regulatory: requisiti normativi a cui la voce risponde, come "chiave:riferimento"
	// (es. "dora:Art. 9", "iso27001:A.8.24"). La verifica del requisito nato dalla voce vale
	// come evidenza dei controlli che coprono quei requisiti (modulo grc, piano passo 20).
	Regulatory []string `json:"regulatory,omitempty"`
}

// Verification è il criterio di verifica di una voce: chi verifica e come.
type Verification struct {
	Method    string `json:"method"`              // manual | evidence
	Check     string `json:"check,omitempty"`     // controllo automatico (devsecops.md §3.5), per method = evidence
	Criterion string `json:"criterion,omitempty"` // come si verifica, in parole
	Team      string `json:"team,omitempty"`      // chiave del team valutatore che verifica
	Role      string `json:"role,omitempty"`      // oppure: ruolo sulla richiesta o sull'impatto
}

// Baseline è un insieme di voci che si applica quando la condizione è vera: sui fatti di
// ogni impatto (scope application) o della richiesta (scope project). Vuota = sempre.
type Baseline struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scope       string   `json:"scope"` // application | project
	When        string   `json:"when,omitempty"`
	Items       []string `json:"items"`
}

var (
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	itemKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
	checkRe   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,62}$`)
	// regRe: riferimento normativo "chiave:riferimento" (modulo grc, grc_requirement_links).
	regRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}:[^\s:][^:]{0,63}$`)
)

// InvalidDefinition riporta i problemi di una definizione non valida.
type InvalidDefinition struct{ Problems []engine.Problem }

func (e *InvalidDefinition) Error() string {
	for _, p := range e.Problems {
		if p.Level == "error" {
			return "catalogo di requisiti non valido: " + p.Path + ": " + p.Msg
		}
	}
	return "catalogo di requisiti non valido"
}

// DecodeDefinition legge una definizione rifiutando i campi sconosciuti.
func DecodeDefinition(raw []byte) (*Definition, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	d := &Definition{}
	if err := dec.Decode(d); err != nil {
		return nil, engine.InvalidValue("definition", "JSON non valido: %v", err)
	}
	return d, nil
}

func (v Verification) validate(path string, add func(path, msg string)) {
	switch v.Method {
	case "manual", "evidence":
	default:
		add(path+".method", "metodo di verifica non ammesso (manual, evidence)")
	}
	if v.Method == "evidence" && !checkRe.MatchString(v.Check) {
		add(path+".check", "per la verifica da evidenza indica la chiave del controllo (es. sca_block)")
	}
	if v.Method == "manual" && v.Check != "" {
		add(path+".check", "il controllo vale solo per la verifica da evidenza")
	}
	if (v.Team == "") == (v.Role == "") {
		add(path, "indica chi verifica: un team valutatore (team) oppure un ruolo (role)")
	}
	if len(v.Criterion) > 2000 {
		add(path+".criterion", "criterio troppo lungo (massimo 2000 caratteri)")
	}
}

// Validate controlla la definizione senza il database.
func (d *Definition) Validate() []engine.Problem {
	ps := []engine.Problem{}
	add := func(path, msg string) { ps = append(ps, engine.Problem{Level: "error", Path: path, Msg: msg}) }
	if strings.TrimSpace(d.Name) == "" {
		add("name", "il nome del catalogo è obbligatorio")
	}
	if len(d.Items) == 0 {
		add("items", "il catalogo non ha voci")
	}
	seen := map[string]bool{}
	for i, it := range d.Items {
		p := fmt.Sprintf("items[%d]", i)
		if !itemKeyRe.MatchString(it.Key) {
			add(p+".key", "chiave non valida (lettere, cifre, - _ ., al massimo 32)")
		} else if seen[strings.ToUpper(it.Key)] {
			add(p+".key", fmt.Sprintf("chiave %q ripetuta", it.Key))
		}
		seen[strings.ToUpper(it.Key)] = true
		if t := strings.TrimSpace(it.Title); t == "" || len(t) > 500 {
			add(p+".title", "titolo obbligatorio (massimo 500 caratteri)")
		}
		if len(it.Text) > 10000 {
			add(p+".text", "testo troppo lungo (massimo 10000 caratteri)")
		}
		it.Verification.validate(p+".verification", add)
		for j, ref := range it.Regulatory {
			if !regRe.MatchString(ref) {
				add(fmt.Sprintf("%s.regulatory[%d]", p, j), "riferimento normativo non valido: chiave:riferimento (es. dora:Art. 9)")
			}
		}
	}
	bseen := map[string]bool{}
	for i, b := range d.Baselines {
		p := fmt.Sprintf("baselines[%d]", i)
		if !keyRe.MatchString(b.Key) {
			add(p+".key", "chiave non valida (minuscole, cifre e _)")
		} else if bseen[b.Key] {
			add(p+".key", fmt.Sprintf("chiave %q ripetuta", b.Key))
		}
		bseen[b.Key] = true
		if strings.TrimSpace(b.Name) == "" {
			add(p+".name", "il nome della baseline è obbligatorio")
		}
		if b.Scope != "application" && b.Scope != "project" {
			add(p+".scope", "ambito non ammesso (application, project)")
		}
		if b.When != "" {
			if _, err := engine.ParseCondition(b.When); err != nil {
				add(p+".when", "condizione non valida: "+err.Error())
			}
		}
		if len(b.Items) == 0 {
			add(p+".items", "la baseline non ha voci")
		}
		for j, k := range b.Items {
			if !seen[strings.ToUpper(k)] {
				add(fmt.Sprintf("%s.items[%d]", p, j), fmt.Sprintf("voce %q inesistente nel catalogo", k))
			}
		}
	}
	return ps
}

// checkRefs controlla i riferimenti ai dati del tenant: team valutatori e ruoli.
func (d *Definition) checkRefs(ctx context.Context, tx pgx.Tx) ([]engine.Problem, error) {
	ps := []engine.Problem{}
	for i, it := range d.Items {
		p := fmt.Sprintf("items[%d].verification", i)
		if it.Verification.Team != "" {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from review_teams where key = $1 and not archived)", it.Verification.Team).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				ps = append(ps, engine.Problem{Level: "error", Path: p + ".team", Msg: fmt.Sprintf("team valutatore %q inesistente", it.Verification.Team)})
			}
		}
		if it.Verification.Role != "" {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", it.Verification.Role).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				ps = append(ps, engine.Problem{Level: "error", Path: p + ".role", Msg: fmt.Sprintf("ruolo %q inesistente", it.Verification.Role)})
			}
		}
	}
	return ps, nil
}

func hasErrors(ps []engine.Problem) bool {
	return slices.ContainsFunc(ps, func(p engine.Problem) bool { return p.Level == "error" })
}

func (d *Definition) item(key string) *Item {
	for i := range d.Items {
		if strings.EqualFold(d.Items[i].Key, key) {
			return &d.Items[i]
		}
	}
	return nil
}

func requireAdmin(a engine.Actor) error {
	if !a.Has(rbac.ProcessAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

func requireRead(a engine.Actor) error {
	if !a.Has(rbac.ElementRead) {
		return engine.ErrForbidden
	}
	return nil
}

// ---------------------------------------------------------------------------
// Versioni

// CatalogVersion è una versione di un catalogo.
type CatalogVersion struct {
	ID          string      `json:"id"`
	Key         string      `json:"key"`
	Version     int         `json:"version"`
	Status      string      `json:"status"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Definition  *Definition `json:"definition"`
	Notes       string      `json:"notes"`
	CreatedAt   time.Time   `json:"created_at"`
	PublishedAt *time.Time  `json:"published_at"`
	PublishedBy *string     `json:"published_by"`
}

const selectCatalog = `
	select c.id::text, c.key, c.version, c.status, c.name, c.description, c.definition, c.notes, c.created_at, c.published_at, m.display_name
	from requirement_catalogs c left join members m on m.tenant_id = c.tenant_id and m.id = c.published_by`

func scanCatalogs(rows pgx.Rows) ([]*CatalogVersion, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*CatalogVersion, error) {
		v := &CatalogVersion{}
		var raw []byte
		if err := r.Scan(&v.ID, &v.Key, &v.Version, &v.Status, &v.Name, &v.Description, &raw, &v.Notes, &v.CreatedAt, &v.PublishedAt, &v.PublishedBy); err != nil {
			return nil, err
		}
		v.Definition = &Definition{}
		return v, json.Unmarshal(raw, v.Definition)
	})
}

// CatalogSummary è un catalogo nell'elenco.
type CatalogSummary struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Version     *int       `json:"version"`
	PublishedAt *time.Time `json:"published_at"`
	Items       int        `json:"items"`
	Baselines   []Baseline `json:"baselines"`
	HasDraft    bool       `json:"has_draft"`
	DraftNumber *int       `json:"draft_version"`
}

// Catalogs elenca i cataloghi con la versione corrente e l'eventuale bozza.
func (s *Service) Catalogs(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]CatalogSummary, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectCatalog+` where c.status in ('draft','published') order by c.key, c.version`)
	if err != nil {
		return nil, err
	}
	vs, err := scanCatalogs(rows)
	if err != nil {
		return nil, err
	}
	out := []CatalogSummary{}
	idx := map[string]int{}
	for _, v := range vs {
		i, ok := idx[v.Key]
		if !ok {
			i = len(out)
			idx[v.Key] = i
			out = append(out, CatalogSummary{Key: v.Key, Name: v.Name, Description: v.Description, Baselines: []Baseline{}})
		}
		c := &out[i]
		if v.Status == "draft" {
			c.HasDraft, c.DraftNumber = true, &v.Version
			if c.Version == nil {
				c.Name, c.Description = v.Name, v.Description
			}
			continue
		}
		c.Version, c.PublishedAt = &v.Version, v.PublishedAt
		c.Name, c.Description = v.Name, v.Description
		c.Items, c.Baselines = len(v.Definition.Items), v.Definition.Baselines
	}
	return out, nil
}

// Catalog è un catalogo con la versione corrente, la bozza e la storia delle versioni.
type Catalog struct {
	Key           string            `json:"key"`
	Current       *CatalogVersion   `json:"current"`
	Draft         *CatalogVersion   `json:"draft"`
	DraftProblems []engine.Problem  `json:"draft_problems"`
	Versions      []*CatalogVersion `json:"versions"`
	Requirements  int               `json:"requirements"` // requisiti nati dal catalogo
}

// GetCatalog restituisce un catalogo.
func (s *Service) GetCatalog(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Catalog, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectCatalog+` where c.key = $1 order by c.version desc`, key)
	if err != nil {
		return nil, err
	}
	vs, err := scanCatalogs(rows)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, engine.ErrNotFound
	}
	c := &Catalog{Key: key, Versions: []*CatalogVersion{}, DraftProblems: []engine.Problem{}}
	for _, v := range vs {
		switch v.Status {
		case "draft":
			c.Draft = v
			c.DraftProblems = v.Definition.Validate()
			refs, err := v.Definition.checkRefs(ctx, tx)
			if err != nil {
				return nil, err
			}
			c.DraftProblems = append(c.DraftProblems, refs...)
		case "published":
			c.Current = v
		}
		if v.Status != "draft" {
			c.Versions = append(c.Versions, &CatalogVersion{ID: v.ID, Key: v.Key, Version: v.Version, Status: v.Status, Name: v.Name,
				Notes: v.Notes, CreatedAt: v.CreatedAt, PublishedAt: v.PublishedAt, PublishedBy: v.PublishedBy})
		}
	}
	if err := tx.QueryRow(ctx, "select count(*)::int from ext_requirement where catalog_key = $1", key).Scan(&c.Requirements); err != nil {
		return nil, err
	}
	return c, nil
}

// ValidateDefinition controlla una definizione (anche i riferimenti) senza salvarla.
func (s *Service) ValidateDefinition(ctx context.Context, tx pgx.Tx, actor engine.Actor, raw []byte) ([]engine.Problem, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	d, err := DecodeDefinition(raw)
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return []engine.Problem{{Level: "error", Path: "definition", Msg: ve.Msg}}, nil
		}
		return nil, err
	}
	ps := d.Validate()
	refs, err := d.checkRefs(ctx, tx)
	return append(ps, refs...), err
}

// SaveDraft salva la bozza del catalogo (la crea se non c'è). La bozza può avere errori:
// li ferma la pubblicazione.
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, raw []byte, notes string) (*Catalog, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if !keyRe.MatchString(key) {
		return nil, engine.InvalidValue("key", "chiave del catalogo non valida (minuscole, cifre e _)")
	}
	d, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	if err := saveDraft(ctx, tx, actor, key, d, notes); err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, tx, actor, key)
}

func saveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, d *Definition, notes string) error {
	def, _ := json.Marshal(d)
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = key
	}
	tag, err := tx.Exec(ctx, `update requirement_catalogs set definition = $2, name = $3, description = $4, notes = $5, created_by = $6, created_at = now()
		where key = $1 and status = 'draft'`, key, def, name, d.Description, notes, nullable(actor.MemberID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into requirement_catalogs (tenant_id, id, key, version, status, name, description, definition, notes, created_by)
			values (app_tenant(), $1, $2, (select coalesce(max(version), 0) + 1 from requirement_catalogs where key = $2), 'draft', $3, $4, $5, $6, $7)`,
			db.NewID(), key, name, d.Description, def, notes, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	return audited(ctx, tx, actor, "requirement_catalog.draft", "requirement_catalog", "", map[string]any{"key": key})
}

// DiscardDraft scarta la bozza.
func (s *Service) DiscardDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from requirement_catalogs where key = $1 and status = 'draft'", key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audited(ctx, tx, actor, "requirement_catalog.discard", "requirement_catalog", "", map[string]any{"key": key})
}

// Publish pubblica la bozza: le baseline applicate da ora usano questa versione; i
// requisiti già creati restano con la loro.
func (s *Service) Publish(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, notes string) (*Catalog, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var id string
	var version int
	var raw []byte
	err := tx.QueryRow(ctx, "select id, version, definition from requirement_catalogs where key = $1 and status = 'draft' for update", key).Scan(&id, &version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidCode("validation.no_draft", "draft", "non c'è una bozza da pubblicare")
	}
	if err != nil {
		return nil, err
	}
	d, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	ps := d.Validate()
	refs, err := d.checkRefs(ctx, tx)
	if err != nil {
		return nil, err
	}
	if ps = append(ps, refs...); hasErrors(ps) {
		return nil, &InvalidDefinition{Problems: ps}
	}
	if _, err := tx.Exec(ctx, "update requirement_catalogs set status = 'superseded' where key = $1 and status = 'published'", key); err != nil {
		return nil, err
	}
	if notes != "" {
		if _, err := tx.Exec(ctx, "update requirement_catalogs set notes = $2 where id = $1", id, notes); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update requirement_catalogs set status = 'published', published_by = $2, published_at = now() where id = $1",
		id, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "requirement_catalog.publish", "requirement_catalog", id, map[string]any{"key": key, "version": version}); err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, tx, actor, key)
}

type cachedCatalog struct {
	def     *Definition
	version int
}

var catalogCache sync.Map // id della versione pubblicata → cachedCatalog (immutabile)

// currentCatalog legge la versione corrente di un catalogo; nil se non c'è.
func currentCatalog(ctx context.Context, tx pgx.Tx, key string) (*Definition, int, error) {
	var id string
	err := tx.QueryRow(ctx, "select id::text from requirement_catalogs where key = $1 and status = 'published'", key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if c, ok := catalogCache.Load(id); ok {
		e := c.(cachedCatalog)
		return e.def, e.version, nil
	}
	var raw []byte
	var version int
	if err := tx.QueryRow(ctx, "select definition, version from requirement_catalogs where id = $1", id).Scan(&raw, &version); err != nil {
		return nil, 0, err
	}
	d := &Definition{}
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, 0, err
	}
	catalogCache.Store(id, cachedCatalog{d, version})
	return d, version, nil
}

// ---------------------------------------------------------------------------
// Applicazione delle baseline

func (s *Service) checkBaseline(ctx context.Context, tx pgx.Tx, a engine.Action) ([]string, error) {
	if a.Ref == "" {
		return []string{"indica in \"ref\" la chiave del catalogo"}, nil
	}
	d, _, err := currentCatalog(ctx, tx, a.Ref)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return []string{fmt.Sprintf("catalogo %q inesistente o senza una versione pubblicata", a.Ref)}, nil
	}
	if a.When != "" {
		if _, err := engine.ParseCondition(a.When); err != nil {
			return []string{"condizione dell'azione non valida: " + err.Error()}, nil
		}
	}
	if b := a.Params["baseline"]; b != "" && !slices.ContainsFunc(d.Baselines, func(x Baseline) bool { return x.Key == b }) {
		return []string{fmt.Sprintf("baseline %q inesistente nel catalogo %s", b, a.Ref)}, nil
	}
	return nil, nil
}

func (s *Service) actionBaseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if ok, err := typeExists(ctx, tx, TypeKey); err != nil || !ok {
		return "", err
	}
	keys, err := s.applyBaselines(ctx, tx, actor, el, a.Ref, a.Params["baseline"], a.When)
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return ve.Msg, nil
		}
		return "", err
	}
	if len(keys) == 0 {
		return "", engine.ErrNoEffect
	}
	return fmt.Sprintf("catalogo %s: %d requisiti (%s)", a.Ref, len(keys), strings.Join(keys, ", ")), nil
}

// applyBaselines crea i requisiti delle baseline del catalogo che si applicano: quelle di
// applicazione per ogni impatto attivo (o per l'impatto indicato) la cui condizione è vera
// sui fatti dell'impatto, quelle di progetto sulla richiesta. Le voci già presenti sullo
// stesso oggetto non si ripetono. Restituisce le chiavi dei requisiti creati.
func (s *Service) applyBaselines(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, catalog, only, when string) ([]string, error) {
	d, version, err := currentCatalog(ctx, tx, catalog)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, engine.InvalidParams("validation.requirements.catalog_unpublished", "catalog", map[string]any{"catalog": catalog}, "catalogo %s senza una versione pubblicata", catalog)
	}
	sys := reader(actor.MemberID)
	var req *engine.Element
	var impacts []string
	switch el.TypeKey {
	case RequestType:
		req = el
		rows, err := tx.Query(ctx, `select x.element_id::text from ext_impact x
			join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled' order by e.number`, el.ID)
		if err != nil {
			return nil, err
		}
		if impacts, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return nil, err
		}
	case ImpactType:
		if req, err = s.eng.Get(ctx, tx, sys, deref(el.ParentID)); err != nil {
			return nil, err
		}
		impacts = []string{el.ID}
	default:
		return nil, engine.InvalidCode("validation.requirements.baseline_element", "element", "le baseline si applicano a richieste e impatti")
	}
	var extra engine.Condition
	if when != "" {
		if extra, err = engine.ParseCondition(when); err != nil {
			return nil, engine.InvalidValue("when", "condizione non valida: %v", err)
		}
	}
	var created []string
	newFrom := func(b Baseline, it *Item) NewRequirement {
		in := NewRequirement{Title: it.Title, Text: it.Text, Category: it.Category, Origin: "catalog", CatalogKey: catalog,
			CatalogVersion: version, CatalogItemKey: it.Key, BaselineKey: b.Key, Method: it.Verification.Method,
			Check: it.Verification.Check, Criterion: it.Verification.Criterion, VerifierRole: it.Verification.Role}
		return in
	}
	resolveTeam := func(in *NewRequirement, it *Item) error {
		if it.Verification.Team == "" {
			return nil
		}
		err := tx.QueryRow(ctx, "select id::text from review_teams where key = $1", it.Verification.Team).Scan(&in.VerifierTeamID)
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.NotFound("verification", "team", "team valutatore %q inesistente", it.Verification.Team)
		}
		return err
	}
	var reqFacts engine.Facts
	for _, b := range d.Baselines {
		if only != "" && b.Key != only {
			continue
		}
		cond := engine.Condition{}
		if b.When != "" {
			if cond, err = engine.ParseCondition(b.When); err != nil {
				continue // la pubblicazione lo impedisce; per prudenza la baseline non si applica
			}
		}
		if b.Scope == "project" {
			if el.TypeKey != RequestType {
				continue
			}
			if !cond.Empty() {
				if reqFacts == nil {
					if reqFacts, err = s.eng.ElementFacts(ctx, tx, req); err != nil {
						return nil, err
					}
				}
				if !cond.Eval(reqFacts) {
					continue
				}
			}
			for _, k := range b.Items {
				it := d.item(k)
				if it == nil {
					continue
				}
				in := newFrom(b, it)
				if err := resolveTeam(&in, it); err != nil {
					return nil, err
				}
				r, err := s.create(ctx, tx, actor, req, nil, in)
				if err != nil {
					return nil, err
				}
				if r != nil {
					created = append(created, r.Key)
				}
			}
			continue
		}
		for _, id := range impacts {
			imp, err := s.eng.Get(ctx, tx, sys, id)
			if err != nil {
				return nil, err
			}
			if !cond.Empty() || !extra.Empty() {
				f, err := s.eng.ElementFacts(ctx, tx, imp)
				if err != nil {
					return nil, err
				}
				if !cond.Eval(f) || !extra.Eval(f) {
					continue
				}
			}
			for _, k := range b.Items {
				it := d.item(k)
				if it == nil {
					continue
				}
				in := newFrom(b, it)
				if err := resolveTeam(&in, it); err != nil {
					return nil, err
				}
				r, err := s.create(ctx, tx, actor, req, imp, in)
				if err != nil {
					return nil, err
				}
				if r != nil {
					created = append(created, r.Key)
				}
			}
		}
	}
	return created, nil
}

// ApplyBaselines applica a mano le baseline di un catalogo a una richiesta o a un impatto
// (chi può creare requisiti). Serve anche per le richieste già oltre la fase della regola.
func (s *Service) ApplyBaselines(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, catalog string) ([]string, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	req := el
	if el.TypeKey == ImpactType {
		if req, err = s.eng.Get(ctx, tx, reader(actor.MemberID), deref(el.ParentID)); err != nil {
			return nil, err
		}
	}
	ok, err := s.canCreate(ctx, tx, actor, req, el)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	keys, err := s.applyBaselines(ctx, tx, actor, el, catalog, "", "")
	if err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, audited(ctx, tx, actor, "requirement.baseline", el.TypeKey, el.ID, map[string]any{"catalog": catalog, "created": len(keys)})
}

// PreviewItem è un'applicazione (o una richiesta) con l'esito della condizione.
type PreviewItem struct {
	ID      string            `json:"id"`
	Key     string            `json:"key"`
	Code    string            `json:"code,omitempty"`
	Title   string            `json:"title"`
	Matched bool              `json:"matched"`
	Facts   map[string]string `json:"facts"`
}

// PreviewCondition mostra su quali applicazioni (scope application) o richieste aperte
// (scope project) una condizione di baseline è vera, con i valori dei fatti usati.
func (s *Service) PreviewCondition(ctx context.Context, tx pgx.Tx, actor engine.Actor, condition, scope string) ([]PreviewItem, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	cond, err := engine.ParseCondition(condition)
	if err != nil {
		return nil, engine.InvalidParams("validation.requirements.condition_error", "condition", map[string]any{"detail": err.Error()}, "%v", err)
	}
	typ := "application"
	if scope == "project" {
		typ = RequestType
	}
	rows, err := tx.Query(ctx, `select e.id::text from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where t.key = $1 and e.deleted_at is null and e.number is not null and coalesce(o.category, '') not in ('done', 'cancelled')
		order by e.number desc limit 200`, typ)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	keys := cond.Keys()
	out := []PreviewItem{}
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, reader(actor.MemberID), id)
		if err != nil {
			return nil, err
		}
		f, err := s.eng.ElementFacts(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		it := PreviewItem{ID: el.ID, Key: el.Key, Title: el.Title, Matched: cond.Eval(f), Facts: map[string]string{}}
		for _, k := range keys {
			vals := f[k]
			parts := make([]string, 0, len(vals))
			for _, v := range vals {
				if v != nil {
					parts = append(parts, fmt.Sprint(v))
				}
			}
			it.Facts[k] = strings.Join(parts, ", ")
		}
		if c, ok := f["codice"]; ok && len(c) > 0 && c[0] != nil {
			it.Code = fmt.Sprint(c[0])
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b PreviewItem) int {
		if a.Matched != b.Matched {
			if a.Matched {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Title, b.Title)
	})
	return out, nil
}
