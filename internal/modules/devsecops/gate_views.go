package devsecops

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// ---------------------------------------------------------------------------
// Configurazione → Gate di rilascio

// GateConfig è tutto ciò che serve alla pagina di configurazione.
type GateConfig struct {
	Policies     []GatePolicy    `json:"policies"`
	Environments []EnvView       `json:"environments"`
	Phases       []PhaseOption   `json:"request_phases"`
	Approvers    []GateApprover  `json:"approvers"`
	Roles        []NamedKey      `json:"roles"`
	Evidence     *EvidenceConfig `json:"evidence"`
	Defaults     []GateRule      `json:"default_rules"`
	// Fatti per le condizioni libere, per tipo (application, business_request).
	Facts   map[string][]engine.FactInfo `json:"facts"`
	Samples map[string]string            `json:"samples"`
	CanEdit bool                         `json:"can_edit"`
}

// PhaseOption è una fase della richiesta in cui il change può dirsi approvato.
type PhaseOption struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

// NamedKey è una chiave con il suo nome.
type NamedKey struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// GateConfig legge la configurazione del gate.
func (s *Service) GateConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*GateConfig, error) {
	if !actor.Has(PermRead) && !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	c := &GateConfig{CanEdit: CanConfigure(actor), Defaults: DefaultGateRules(true), Facts: map[string][]engine.FactInfo{}, Samples: map[string]string{}}
	var err error
	for _, typ := range []string{"application", "business_request"} {
		var sample string
		if c.Facts[typ], sample, err = s.eng.TypeFacts(ctx, tx, actor, typ); err != nil {
			return nil, err
		}
		c.Samples[typ] = sample
	}
	if c.Policies, err = loadPolicies(ctx, tx); err != nil {
		return nil, err
	}
	if c.Approvers, err = loadApprovers(ctx, tx); err != nil {
		return nil, err
	}
	if c.Evidence, err = s.evidenceConfig(ctx, tx); err != nil {
		return nil, err
	}
	envs, err := tx.Query(ctx, "select id::text, name, is_production, ordinal from environments order by ordinal, name")
	if err != nil {
		return nil, err
	}
	c.Environments = []EnvView{}
	for envs.Next() {
		var e EnvView
		if err := envs.Scan(&e.ID, &e.Name, &e.Production, &e.Ordinal); err != nil {
			envs.Close()
			return nil, err
		}
		c.Environments = append(c.Environments, e)
	}
	if err := envs.Err(); err != nil {
		return nil, err
	}
	// Le fasi in cui si trovano le richieste (in ordine) e quelle degli stati del loro insieme di valori.
	ph, err := tx.Query(ctx, `select o.name, coalesce(o.category, ''), min(o.ordinal) from options o
		where o.category is not null and not o.archived and o.option_set_id in (
			select o2.option_set_id from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'business_request'
			join options o2 on o2.tenant_id = e.tenant_id and o2.id = e.state_id)
		group by 1, 2 order by 3, 1`)
	if err != nil {
		return nil, err
	}
	c.Phases = []PhaseOption{}
	for ph.Next() {
		var p PhaseOption
		var ord int
		if err := ph.Scan(&p.Name, &p.Category, &ord); err != nil {
			ph.Close()
			return nil, err
		}
		c.Phases = append(c.Phases, p)
	}
	if err := ph.Err(); err != nil {
		return nil, err
	}
	roles, err := tx.Query(ctx, "select key, name from stakeholder_roles order by name")
	if err != nil {
		return nil, err
	}
	c.Roles = []NamedKey{}
	for roles.Next() {
		var r NamedKey
		if err := roles.Scan(&r.Key, &r.Name); err != nil {
			roles.Close()
			return nil, err
		}
		c.Roles = append(c.Roles, r)
	}
	return c, roles.Err()
}

// ---------------------------------------------------------------------------
// Decisioni

// DecisionView è una decisione registrata.
type DecisionView struct {
	ID          string        `json:"id"`
	Application GateRef       `json:"application"`
	Request     *GateRef      `json:"request,omitempty"`
	Service     string        `json:"service,omitempty"`
	Environment string        `json:"environment"`
	Version     string        `json:"version,omitempty"`
	Commit      string        `json:"commit,omitempty"`
	Change      string        `json:"change,omitempty"`
	Result      string        `json:"result"`
	RawResult   string        `json:"raw_result"`
	Mode        string        `json:"mode"`
	Overridden  bool          `json:"overridden"`
	Reasons     []GateReason  `json:"reasons"`
	Actor       string        `json:"actor"`
	At          time.Time     `json:"at"`
	CanOverride bool          `json:"can_override"`
	Override    *OverrideView `json:"override,omitempty"`
}

// DecisionFilter limita l'elenco.
type DecisionFilter struct {
	Application string
	Request     string
	Result      string
	Limit       int
}

func (s *Service) Decisions(ctx context.Context, tx pgx.Tx, actor engine.Actor, f DecisionFilter) ([]DecisionView, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 20
	}
	q := `select d.id::text, d.application_element_id::text, d.request_element_id::text, coalesce(sv.name, ''), d.environment, coalesce(d.version, ''),
		coalesce(d.commit, ''), coalesce(d.change_number, ''), d.outcome, d.raw_outcome, d.mode, d.overridden, d.reasons, coalesce(m.display_name, 'Pipeline'), d.created_at
		from gate_decisions d left join members m on m.tenant_id = d.tenant_id and m.id = d.actor_member_id
		left join services sv on sv.tenant_id = d.tenant_id and sv.id = d.service_id where true`
	args := []any{}
	if f.Application != "" {
		args = append(args, f.Application)
		q += ` and d.application_element_id::text = $` + itoa(len(args))
	}
	if f.Request != "" {
		args = append(args, f.Request)
		q += ` and d.request_element_id::text = $` + itoa(len(args))
	}
	if f.Result != "" {
		args = append(args, f.Result)
		q += ` and d.outcome = $` + itoa(len(args))
	}
	args = append(args, f.Limit)
	q += ` order by d.created_at desc limit $` + itoa(len(args))
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type raw struct {
		v        DecisionView
		app, req string
		hasReq   bool
		reasons  []byte
	}
	var list []raw
	for rows.Next() {
		var r raw
		var req *string
		if err := rows.Scan(&r.v.ID, &r.app, &req, &r.v.Service, &r.v.Environment, &r.v.Version, &r.v.Commit, &r.v.Change, &r.v.Result,
			&r.v.RawResult, &r.v.Mode, &r.v.Overridden, &r.reasons, &r.v.Actor, &r.v.At); err != nil {
			rows.Close()
			return nil, err
		}
		if req != nil {
			r.req, r.hasReq = *req, true
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs := map[string]GateRef{}
	ref := func(id string) (GateRef, error) {
		if g, ok := refs[id]; ok {
			return g, nil
		}
		g, err := s.elementRefByID(ctx, tx, id)
		refs[id] = g
		return g, err
	}
	// Override per decisione: l'ultimo.
	ids := make([]string, len(list))
	for i, r := range list {
		ids[i] = r.v.ID
	}
	ovs := map[string]OverrideView{}
	if len(ids) > 0 {
		all, err := s.loadOverrides(ctx, tx, "o.decision_id::text = any($1)", ids)
		if err != nil {
			return nil, err
		}
		for i := len(all) - 1; i >= 0; i-- { // le più recenti per ultime
			ovs[all[i].DecisionID] = all[i]
		}
	}
	policies, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]DecisionView, 0, len(list))
	for _, r := range list {
		if r.v.Application, err = ref(r.app); err != nil {
			return nil, err
		}
		if r.hasReq {
			g, err := ref(r.req)
			if err != nil {
				return nil, err
			}
			r.v.Request = &g
		}
		r.v.Reasons = []GateReason{}
		_ = json.Unmarshal(r.reasons, &r.v.Reasons)
		if o, ok := ovs[r.v.ID]; ok {
			oc := o
			r.v.Override = &oc
		}
		pol := policyFor(policies, r.v.Environment)
		r.v.CanOverride = r.v.RawResult == "fail" && r.v.Result == "fail" && pol != nil && pol.OverrideEnabled &&
			(r.v.Override == nil || r.v.Override.Status == "rejected" || r.v.Override.Status == "revoked" || r.v.Override.Status == "expired")
		out = append(out, r.v)
	}
	return out, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------------------
// Pannello dell'applicazione e della richiesta

// GateCell è lo stato di un'applicazione in un ambiente, adesso.
type GateCell struct {
	Application GateRef      `json:"application"`
	Environment string       `json:"environment"`
	Production  bool         `json:"production"`
	Outcome     *GateOutcome `json:"outcome"`
}

// GatePanel è la scheda «Gate di rilascio» dell'applicazione e della richiesta.
type GatePanel struct {
	Subject      GateSubject    `json:"subject"`
	Cells        []GateCell     `json:"cells"`
	Decisions    []DecisionView `json:"decisions"`
	Overrides    []OverrideView `json:"overrides"`
	Configured   bool           `json:"configured"`
	CanConfigure bool           `json:"can_configure"`
}

// GateSubject è l'elemento di cui si mostra il pannello.
type GateSubject struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// GatePanel calcola lo stato attuale del gate per ogni ambiente con regole. Per
// un'applicazione la richiesta e la versione sono quelle dell'ultima decisione nell'ambiente
// (cioè l'ultima domanda della pipeline); per una richiesta le applicazioni sono quelle dei suoi impatti.
func (s *Service) GatePanel(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*GatePanel, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	p := &GatePanel{Subject: GateSubject{ID: el.ID, Key: el.Key, Title: el.Title, Type: el.TypeKey}, Cells: []GateCell{}, Decisions: []DecisionView{},
		Overrides: []OverrideView{}, CanConfigure: CanConfigure(actor)}
	var appIDs []string
	f := DecisionFilter{Limit: 15}
	switch el.TypeKey {
	case "application":
		appIDs = []string{el.ID}
		f.Application = el.ID
	case "business_request":
		rows, err := tx.Query(ctx, `select distinct application_id::text from ext_impact where request_id = $1 and application_id is not null limit 6`, el.ID)
		if err != nil {
			return nil, err
		}
		if appIDs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return nil, err
		}
		f.Request = el.ID
	default:
		return nil, engine.InvalidCode("validation.devsecops.gate_ref_scope", "ref", "il gate vale per applicazioni e richieste")
	}
	policies, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	p.Configured = len(policies) > 0
	// Ambienti da mostrare: quelli con regole proprie, e (se c'è "*") gli altri esistenti.
	envs := []EnvView{}
	erows, err := tx.Query(ctx, "select id::text, name, is_production, ordinal from environments order by ordinal, name")
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var e EnvView
		if err := erows.Scan(&e.ID, &e.Name, &e.Production, &e.Ordinal); err != nil {
			erows.Close()
			return nil, err
		}
		envs = append(envs, e)
	}
	if err := erows.Err(); err != nil {
		return nil, err
	}
	show := []EnvView{}
	for _, e := range envs {
		if policyFor(policies, e.Name) != nil {
			show = append(show, e)
		}
	}
	for _, pol := range policies {
		if pol.Environment != gateWildcard && !slices.ContainsFunc(show, func(e EnvView) bool { return e.Name == pol.Environment }) {
			show = append(show, EnvView{Name: pol.Environment})
		}
	}
	if len(show) > 8 {
		show = show[:8]
	}
	for _, appID := range appIDs {
		for _, e := range show {
			in := GateInput{Application: appID, Environment: e.Name}
			if el.TypeKey == "business_request" {
				in.Request = el.Key
			} else {
				var reqKey, version string
				err := tx.QueryRow(ctx, `select coalesce(sp.key || '-' || r.number, ''), coalesce(d.version, '') from gate_decisions d
					left join elements r on r.tenant_id = d.tenant_id and r.id = d.request_element_id
					left join spaces sp on sp.tenant_id = r.tenant_id and sp.id = r.space_id
					where d.application_element_id = $1 and d.environment = $2 order by d.created_at desc limit 1`, appID, e.Name).Scan(&reqKey, &version)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return nil, err
				}
				in.Request, in.Version = reqKey, version
			}
			out, err := s.Evaluate(ctx, tx, actor, in, nil)
			if err != nil {
				var ve *engine.ValidationError
				if errors.As(err, &ve) || errors.Is(err, engine.ErrNotFound) {
					continue
				}
				return nil, err
			}
			p.Cells = append(p.Cells, GateCell{Application: out.Application, Environment: e.Name, Production: e.Production, Outcome: out})
		}
	}
	if p.Decisions, err = s.Decisions(ctx, tx, actor, f); err != nil {
		return nil, err
	}
	where, arg := "o.application_element_id::text = $1", el.ID
	if el.TypeKey == "business_request" {
		where = "o.request_element_id::text = $1"
	}
	if p.Overrides, err = s.loadOverrides(ctx, tx, where, arg); err != nil {
		return nil, err
	}
	return p, nil
}
