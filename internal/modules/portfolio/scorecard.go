// Package portfolio è la valutazione e la priorità del portafoglio (demand.md §6; piano,
// passo 8): scorecard configurabili e versionate per tenant, punteggio pesato delle
// richieste e graduatoria con obblighi normativi in testa, linea di budget e ordine manuale.
package portfolio

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Service è il modulo; usa il motore per i fatti delle richieste (valori proposti).
type Service struct {
	eng *engine.Service
}

func New(eng *engine.Service) *Service { return &Service{eng: eng} }

// Definition è il contenuto di una versione della scorecard.
type Definition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Method      string      `json:"method"`       // per ora solo "weighted"
	ScorerRoles []string    `json:"scorer_roles"` // ruoli sulla richiesta che possono votare
	Criteria    []Criterion `json:"criteria"`
}

// Criterion è un criterio della scorecard.
type Criterion struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Weight      float64 `json:"weight"`
	// Direction: higher_better (più alto è meglio) o lower_better (più alto pesa contro, es. rischio).
	Direction string  `json:"direction"`
	Scale     []Level `json:"scale"`
	// Suggest: valore proposto dalla prima condizione vera sui fatti della richiesta.
	Suggest []Suggestion `json:"suggest,omitempty"`
	// SuggestFact: in alternativa, il valore proposto è quello di un fatto, se è un valore della scala.
	SuggestFact string `json:"suggest_fact,omitempty"`
}

type Level struct {
	Value float64 `json:"value"`
	Label string  `json:"label"`
}

type Suggestion struct {
	When  string  `json:"when"`
	Value float64 `json:"value"`
}

func (c Criterion) bounds() (lo, hi float64) { return c.Scale[0].Value, c.Scale[len(c.Scale)-1].Value }

func (c Criterion) level(v float64) (Level, bool) {
	for _, l := range c.Scale {
		if l.Value == v {
			return l, true
		}
	}
	return Level{}, false
}

// normalized porta il voto in [0, 1] secondo la direzione del criterio.
func (c Criterion) normalized(v float64) float64 {
	lo, hi := c.bounds()
	n := (v - lo) / (hi - lo)
	if c.Direction == "lower_better" {
		n = 1 - n
	}
	return n
}

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Problem è un errore di validazione della definizione, con il percorso del campo.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (d *Definition) validate() []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		add("name", "il nome è obbligatorio")
	}
	if d.Method == "" {
		d.Method = "weighted"
	}
	if d.Method != "weighted" {
		add("method", "metodo non ammesso: per ora solo «weighted» (punteggio pesato)")
	}
	if len(d.ScorerRoles) == 0 {
		add("scorer_roles", "indica almeno un ruolo che può votare")
	}
	if len(d.Criteria) == 0 {
		add("criteria", "serve almeno un criterio")
	}
	seen := map[string]bool{}
	for i := range d.Criteria {
		c := &d.Criteria[i]
		p := fmt.Sprintf("criteria[%d]", i)
		c.Name = strings.TrimSpace(c.Name)
		if !keyRe.MatchString(c.Key) {
			add(p+".key", "chiave non valida (minuscole, cifre e _)")
		} else if seen[c.Key] {
			add(p+".key", "chiave %q ripetuta", c.Key)
		}
		seen[c.Key] = true
		if c.Name == "" {
			add(p+".name", "il nome è obbligatorio")
		}
		if !(c.Weight > 0) || math.IsInf(c.Weight, 0) {
			add(p+".weight", "il peso deve essere maggiore di zero")
		}
		if c.Direction == "" {
			c.Direction = "higher_better"
		}
		if c.Direction != "higher_better" && c.Direction != "lower_better" {
			add(p+".direction", "direzione non ammessa (higher_better, lower_better)")
		}
		if len(c.Scale) < 2 {
			add(p+".scale", "la scala ha almeno due livelli")
		}
		for j, l := range c.Scale {
			if strings.TrimSpace(l.Label) == "" {
				add(fmt.Sprintf("%s.scale[%d].label", p, j), "ogni livello ha un'etichetta")
			}
			if j > 0 && l.Value <= c.Scale[j-1].Value {
				add(fmt.Sprintf("%s.scale[%d].value", p, j), "i valori della scala vanno in ordine crescente, senza ripetizioni")
			}
		}
		for j, sg := range c.Suggest {
			if _, err := engine.ParseCondition(sg.When); err != nil {
				add(fmt.Sprintf("%s.suggest[%d].when", p, j), "condizione non valida: %v", err)
			}
			if len(c.Scale) >= 2 {
				if _, ok := c.level(sg.Value); !ok {
					add(fmt.Sprintf("%s.suggest[%d].value", p, j), "il valore proposto non è un livello della scala")
				}
			}
		}
		if c.SuggestFact != "" && !keyRe.MatchString(strings.ReplaceAll(c.SuggestFact, ".", "_")) {
			add(p+".suggest_fact", "nome di fatto non valido")
		}
	}
	return ps
}

// ErrInvalidDefinition accompagna i problemi di validazione.
type InvalidDefinition struct{ Problems []Problem }

func (e *InvalidDefinition) Error() string {
	if len(e.Problems) == 0 {
		return "definizione della scorecard non valida"
	}
	return "definizione della scorecard non valida: " + e.Problems[0].Path + ": " + e.Problems[0].Message
}

// Version è una versione della scorecard.
type Version struct {
	ID          string      `json:"id"`
	Key         string      `json:"key"`
	Version     int         `json:"version"`
	Status      string      `json:"status"` // draft | published | superseded
	Type        *string     `json:"type"`   // chiave del tipo valutato
	Notes       string      `json:"notes"`
	CreatedAt   time.Time   `json:"created_at"`
	PublishedAt *time.Time  `json:"published_at"`
	PublishedBy *string     `json:"published_by"`
	Results     int         `json:"results"` // punteggi dati con questa versione
	Definition  *Definition `json:"definition,omitempty"`
}

const selectVersion = `
	select s.id, s.key, s.version, s.status, t.key, s.notes, s.created_at, s.published_at, pm.display_name,
	       (select count(*) from scorecard_results r where r.tenant_id = s.tenant_id and r.scorecard_id = s.id),
	       s.name, s.description, s.method, s.scorer_roles, s.criteria
	from scorecards s
	left join element_types t on t.tenant_id = s.tenant_id and t.id = s.type_id
	left join members pm on pm.tenant_id = s.tenant_id and pm.id = s.published_by`

func scanVersion(row pgx.Row) (*Version, error) {
	v := &Version{Definition: &Definition{}}
	var criteria []byte
	err := row.Scan(&v.ID, &v.Key, &v.Version, &v.Status, &v.Type, &v.Notes, &v.CreatedAt, &v.PublishedAt, &v.PublishedBy, &v.Results,
		&v.Definition.Name, &v.Definition.Description, &v.Definition.Method, &v.Definition.ScorerRoles, &criteria)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(criteria, &v.Definition.Criteria); err != nil {
		return nil, err
	}
	return v, nil
}

func collectVersions(rows pgx.Rows) ([]*Version, error) {
	defer rows.Close()
	var out []*Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Scorecard è una scorecard con le sue versioni.
type Scorecard struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	Type     *string    `json:"type"`
	Current  *Version   `json:"current"`
	Draft    *Version   `json:"draft"`
	Versions []*Version `json:"versions"` // dalla più recente, senza definizione
	CanEdit  bool       `json:"can_edit"`
}

// Scorecards elenca le scorecard del tenant.
func (s *Service) Scorecards(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Scorecard, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, "select distinct key from scorecards order by key")
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []Scorecard{}
	for _, k := range keys {
		sc, err := s.Scorecard(ctx, tx, actor, k)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, nil
}

// Scorecard restituisce una scorecard con versione corrente, bozza e storia delle versioni.
func (s *Service) Scorecard(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Scorecard, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, selectVersion+` where s.key = $1 order by s.version desc`, key)
	if err != nil {
		return nil, err
	}
	vs, err := collectVersions(rows)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, engine.ErrNotFound
	}
	sc := &Scorecard{Key: key, Type: vs[0].Type, CanEdit: actor.Has(rbac.PortfolioAdmin), Versions: []*Version{}}
	for _, v := range vs {
		switch v.Status {
		case "published":
			sc.Current = v
		case "draft":
			sc.Draft = v
		}
		light := *v
		light.Definition = nil
		sc.Versions = append(sc.Versions, &light)
	}
	if sc.Current != nil {
		sc.Name = sc.Current.Definition.Name
	} else {
		sc.Name = vs[0].Definition.Name
	}
	return sc, nil
}

func requireAdmin(actor engine.Actor) error {
	if !actor.Has(rbac.PortfolioAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

// validateRoles verifica che i ruoli che votano esistano nel tenant.
func validateRoles(ctx context.Context, tx pgx.Tx, roles []string) []Problem {
	var ps []Problem
	for i, r := range roles {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", r).Scan(&ok); err != nil || !ok {
			ps = append(ps, Problem{Path: fmt.Sprintf("scorer_roles[%d]", i), Message: fmt.Sprintf("ruolo %q inesistente", r)})
		}
	}
	return ps
}

// SaveDraft crea o sostituisce la bozza della scorecard. Una chiave nuova crea una
// nuova scorecard per il tipo indicato (default: business request).
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, def Definition, typeKey, notes string) (*Scorecard, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if !keyRe.MatchString(key) {
		return nil, engine.InvalidValue("key", "chiave della scorecard non valida")
	}
	ps := def.validate()
	ps = append(ps, validateRoles(ctx, tx, def.ScorerRoles)...)
	if len(ps) > 0 {
		return nil, &InvalidDefinition{Problems: ps}
	}
	criteria, _ := json.Marshal(def.Criteria)
	var typeID *string
	var maxVersion int
	err := tx.QueryRow(ctx, `select (array_agg(type_id order by version desc))[1], coalesce(max(version), 0) from scorecards where key = $1`, key).Scan(&typeID, &maxVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if maxVersion == 0 {
		if typeKey == "" {
			typeKey = "business_request"
		}
		var id string
		if err := tx.QueryRow(ctx, "select id from element_types where key = $1", typeKey).Scan(&id); err != nil {
			return nil, engine.NotFound("type", "type", "tipo di elemento %q inesistente", typeKey)
		}
		typeID = &id
	}
	tag, err := tx.Exec(ctx, `update scorecards set name = $2, description = $3, method = $4, scorer_roles = $5, criteria = $6, notes = $7
		where key = $1 and status = 'draft'`, key, def.Name, def.Description, def.Method, def.ScorerRoles, criteria, notes)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into scorecards (tenant_id, id, key, version, status, type_id, name, description, method, scorer_roles, criteria, notes, created_by)
			values (app_tenant(), $1, $2, $3, 'draft', $4, $5, $6, $7, $8, $9, $10, $11)`,
			db.NewID(), key, maxVersion+1, typeID, def.Name, def.Description, def.Method, def.ScorerRoles, criteria, notes, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "scorecard.draft", EntityType: "scorecard", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"key": key, "criteria": len(def.Criteria)}}); err != nil {
		return nil, err
	}
	return s.Scorecard(ctx, tx, actor, key)
}

// DiscardDraft scarta la bozza.
func (s *Service) DiscardDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from scorecards where key = $1 and status = 'draft'", key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audit.Append(ctx, tx, audit.Event{Action: "scorecard.discard", EntityType: "scorecard", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"key": key}})
}

// Publish pubblica la bozza: diventa la versione corrente, la precedente passa a
// "precedente". I punteggi già dati restano legati alla loro versione.
func (s *Service) Publish(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, notes string) (*Scorecard, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var id string
	var version int
	err := tx.QueryRow(ctx, "select id, version from scorecards where key = $1 and status = 'draft' for update", key).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidCode("validation.no_draft", "draft", "non c'è una bozza da pubblicare")
	}
	if err != nil {
		return nil, err
	}
	v, err := scanVersion(tx.QueryRow(ctx, selectVersion+" where s.id = $1", id))
	if err != nil {
		return nil, err
	}
	if ps := append(v.Definition.validate(), validateRoles(ctx, tx, v.Definition.ScorerRoles)...); len(ps) > 0 {
		return nil, &InvalidDefinition{Problems: ps}
	}
	if _, err := tx.Exec(ctx, "update scorecards set status = 'superseded' where key = $1 and status = 'published'", key); err != nil {
		return nil, err
	}
	if notes != "" {
		if _, err := tx.Exec(ctx, "update scorecards set notes = $2 where id = $1", id, notes); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update scorecards set status = 'published', published_by = $2, published_at = now() where id = $1", id, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "scorecard.publish", EntityType: "scorecard", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"key": key, "version": version}}); err != nil {
		return nil, err
	}
	return s.Scorecard(ctx, tx, actor, key)
}

// current è la versione pubblicata della scorecard che valuta il tipo dell'elemento.
func current(ctx context.Context, tx pgx.Tx, typeID string) (*Version, error) {
	v, err := scanVersion(tx.QueryRow(ctx, selectVersion+` where s.status = 'published' and (s.type_id = $1 or s.type_id is null)
		order by s.type_id nulls last, s.key limit 1`, typeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// Vote è il voto di un criterio.
type Vote struct {
	Value float64 `json:"value"`
	Note  string  `json:"note,omitempty"`
}

// Result è un punteggio registrato.
type Result struct {
	ID        string          `json:"id"`
	Version   int             `json:"version"`
	VersionID string          `json:"version_id"`
	Values    map[string]Vote `json:"values"`
	Total     float64         `json:"total"`
	Comment   string          `json:"comment"`
	ScoredBy  *string         `json:"scored_by"`
	ScoredAt  time.Time       `json:"scored_at"`
}

// Proposal è il valore proposto per un criterio, con il motivo.
type Proposal struct {
	Value  float64 `json:"value"`
	Reason string  `json:"reason"`
}

// Evaluation è la sezione "Valutazione" del dettaglio della richiesta.
type Evaluation struct {
	Scorecard *Version `json:"scorecard"` // versione corrente (con definizione)
	CanScore  bool     `json:"can_score"`
	Score     *float64 `json:"score"` // punteggio corrente (ext_demand.score)
	Current   *Result  `json:"current"`
	// CurrentDefinition: la definizione della versione del punteggio corrente, se diversa dalla corrente.
	CurrentDefinition *Definition         `json:"current_definition,omitempty"`
	Stale             bool                `json:"stale"` // punteggio dato con una versione precedente
	Proposals         map[string]Proposal `json:"proposals"`
	History           []Result            `json:"history"`
}

func (s *Service) loadRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from ext_demand where element_id = $1)", el.ID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// mayScore: l'attore ha sulla richiesta uno dei ruoli che la scorecard ammette al voto.
func mayScore(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, v *Version) (bool, error) {
	if v == nil || el.StateCategory == "done" || el.StateCategory == "cancelled" {
		return false, nil
	}
	return engine.HasRole(ctx, tx, el.ID, actor.MemberID, v.Definition.ScorerRoles)
}

// Evaluation restituisce scorecard, punteggio corrente, proposte e storia della richiesta.
func (s *Service) Evaluation(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Evaluation, error) {
	el, err := s.loadRequest(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	v, err := current(ctx, tx, el.TypeID)
	if err != nil {
		return nil, err
	}
	out := &Evaluation{Scorecard: v, Proposals: map[string]Proposal{}, History: []Result{}}
	if out.CanScore, err = mayScore(ctx, tx, actor, el, v); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, "select score from ext_demand where element_id = $1", el.ID).Scan(&out.Score); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select r.id, s.version, s.id, r.values, r.total, r.comment, m.display_name, r.scored_at
		from scorecard_results r
		join scorecards s on s.tenant_id = r.tenant_id and s.id = r.scorecard_id
		left join members m on m.tenant_id = r.tenant_id and m.id = r.scored_by
		where r.element_id = $1 order by r.scored_at desc, r.id limit 50`, el.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		var raw []byte
		if err := rows.Scan(&r.ID, &r.Version, &r.VersionID, &raw, &r.Total, &r.Comment, &r.ScoredBy, &r.ScoredAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r.Values); err != nil {
			return nil, err
		}
		out.History = append(out.History, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.History) > 0 {
		cur := out.History[0]
		out.Current = &cur
		if v == nil || cur.VersionID != v.ID {
			out.Stale = true
			old, err := scanVersion(tx.QueryRow(ctx, selectVersion+" where s.id = $1", cur.VersionID))
			if err != nil {
				return nil, err
			}
			out.CurrentDefinition = old.Definition
		}
	}
	if v != nil {
		facts, err := s.eng.ElementFacts(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		for _, c := range v.Definition.Criteria {
			if p, ok := propose(c, facts); ok {
				out.Proposals[c.Key] = p
			}
		}
	}
	return out, nil
}

// propose calcola il valore proposto di un criterio dai fatti della richiesta.
func propose(c Criterion, f engine.Facts) (Proposal, bool) {
	for _, sg := range c.Suggest {
		cond, err := engine.ParseCondition(sg.When)
		if err != nil || cond.Empty() {
			continue
		}
		if cond.Eval(f) {
			return Proposal{Value: sg.Value, Reason: sg.When}, true
		}
	}
	if c.SuggestFact != "" {
		for _, x := range f[strings.ToLower(c.SuggestFact)] {
			if n, ok := x.(float64); ok {
				if _, in := c.level(n); in {
					return Proposal{Value: n, Reason: c.SuggestFact}, true
				}
			}
		}
	}
	return Proposal{}, false
}

// ScoreInput è una valutazione completa: un voto per ogni criterio della versione corrente.
type ScoreInput struct {
	// VersionID: la versione vista da chi vota; se nel frattempo ne è stata pubblicata
	// un'altra la valutazione è rifiutata, così non si mescolano criteri diversi.
	VersionID string          `json:"version_id"`
	Values    map[string]Vote `json:"values"`
	Comment   string          `json:"comment"`
}

// Total calcola il punteggio pesato da 0 a 100 (una cifra decimale).
func Total(def *Definition, values map[string]Vote) (float64, error) {
	var sum, weights float64
	for _, c := range def.Criteria {
		v, ok := values[c.Key]
		if !ok {
			return 0, engine.Required(c.Key, "manca il voto per «%s»", c.Name)
		}
		if _, in := c.level(v.Value); !in {
			lo, hi := c.bounds()
			return 0, engine.InvalidParams("validation.portfolio.vote_not_level", c.Key, map[string]any{"name": c.Name, "min": lo, "max": hi}, "il voto per «%s» non è un livello della scala (%g-%g)", c.Name, lo, hi)
		}
		sum += c.Weight * c.normalized(v.Value)
		weights += c.Weight
	}
	if weights == 0 {
		return 0, engine.InvalidCode("validation.portfolio.scorecard_no_criteria", "criteria", "scorecard senza criteri")
	}
	return math.Round(1000*sum/weights) / 10, nil
}

// Score registra una valutazione: nuovo risultato nella storia, punteggio corrente in
// ext_demand.score (il fatto "punteggio"), voce nella storia della richiesta e audit.
func (s *Service) Score(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ScoreInput) (*Result, error) {
	el, err := s.loadRequest(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	v, err := current(ctx, tx, el.TypeID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, engine.InvalidCode("validation.portfolio.scorecard_unpublished", "scorecard", "nessuna scorecard pubblicata per questo tipo di richiesta")
	}
	ok, err := mayScore(ctx, tx, actor, el, v)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	if in.VersionID != "" && in.VersionID != v.ID {
		return nil, engine.InvalidParams("validation.portfolio.scorecard_new_version", "version_id", map[string]any{"version": v.Version}, "nel frattempo è stata pubblicata la versione %d della scorecard: ricarica e rivaluta", v.Version)
	}
	for k := range in.Values {
		if !slices.ContainsFunc(v.Definition.Criteria, func(c Criterion) bool { return c.Key == k }) {
			return nil, engine.NotFound(k, "", "criterio inesistente nella versione %d", v.Version)
		}
	}
	for k, vote := range in.Values {
		vote.Note = strings.TrimSpace(vote.Note)
		if len(vote.Note) > 2000 {
			return nil, engine.TooLong(k, 2000, "nota troppo lunga (massimo 2000 caratteri)")
		}
		in.Values[k] = vote
	}
	total, err := Total(v.Definition, in.Values)
	if err != nil {
		return nil, err
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if _, err := tx.Exec(ctx, "select 1 from ext_demand where element_id = $1 for update", el.ID); err != nil {
		return nil, err
	}
	var prev *float64
	if err := tx.QueryRow(ctx, "select score from ext_demand where element_id = $1", el.ID).Scan(&prev); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(in.Values)
	r := &Result{ID: db.NewID(), Version: v.Version, VersionID: v.ID, Values: in.Values, Total: total, Comment: in.Comment}
	if err := tx.QueryRow(ctx, `insert into scorecard_results (tenant_id, id, element_id, scorecard_id, values, total, comment, scored_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7) returning scored_at`,
		r.ID, el.ID, v.ID, raw, total, in.Comment, nullable(actor.MemberID)).Scan(&r.ScoredAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update ext_demand set score = $2, score_scorecard_id = $3, scored_at = $4 where element_id = $1`,
		el.ID, total, v.ID, r.ScoredAt); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, "select display_name from members where id = $1", actor.MemberID).Scan(&r.ScoredBy); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var removed any
	if prev != nil {
		removed = map[string]any{"total": *prev}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "score", v.Key,
		map[string]any{"total": total, "version": v.Version, "comment": in.Comment}, removed); err != nil {
		return nil, err
	}
	return r, audit.Append(ctx, tx, audit.Event{Action: "scorecard.score", EntityType: "business_request", EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"result_id": r.ID, "scorecard_id": v.ID, "version": v.Version, "total": total}})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
