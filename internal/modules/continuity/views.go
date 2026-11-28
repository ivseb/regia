package continuity

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// ---------------------------------------------------------------------------
// Riepilogo e «cosa manca»

// Summary sono i numeri del riepilogo.
type Summary struct {
	Issues            int `json:"issues"`
	High              int `json:"high"`
	CriticalFunctions int `json:"critical_functions"`
	BIAApproved       int `json:"bia_approved"`
	BIAMissing        int `json:"bia_missing"`
	TestsDue          int `json:"tests_due"`
	PlansMissing      int `json:"plans_missing"`
}

// Overview è la vista principale della continuità.
type Overview struct {
	Ready   bool    `json:"ready"`
	Summary Summary `json:"summary"`
	Issues  []Issue `json:"issues"`
	Can     Can     `json:"can"`
}

// OverviewFilter restringe le incoerenze.
type OverviewFilter struct {
	Capability string
	Team       string
	Code       string
}

// Overview calcola le incoerenze del tenant.
func (s *Service) Overview(ctx context.Context, tx pgx.Tx, actor engine.Actor, f OverviewFilter) (*Overview, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	ready, err := isReady(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &Overview{Ready: ready, Issues: []Issue{}, Can: can(actor)}
	if !ready {
		return out, nil
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	var capFilter map[string]bool
	if strings.TrimSpace(f.Capability) != "" {
		c, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(f.Capability))
		if err != nil {
			return nil, err
		}
		capFilter = map[string]bool{c.ID: true}
		for _, a := range m.appsOfCap(c.ID) {
			capFilter[a.ID] = true
		}
		for _, id := range m.capOrder { // capability contenute
			if containsStr(m.ancestors(id), c.ID) {
				capFilter[id] = true
			}
		}
	}
	for _, i := range m.allIssues() {
		if f.Code != "" && i.Code != f.Code {
			continue
		}
		if f.Team != "" && (i.Team == nil || i.Team.ID != f.Team) {
			continue
		}
		if capFilter != nil {
			id := ""
			if i.Application != nil {
				id = i.Application.ID
			} else if i.Capability != nil {
				id = i.Capability.ID
			}
			if !capFilter[id] {
				continue
			}
		}
		out.Issues = append(out.Issues, i)
		out.Summary.Issues++
		if i.Severity == "high" {
			out.Summary.High++
		}
		switch i.Code {
		case IssueTestMissing, IssueTestFailed:
			out.Summary.TestsDue++
		case IssuePlanMissing:
			out.Summary.PlansMissing++
		case IssueBIAMissing:
			out.Summary.BIAMissing++
		}
	}
	for _, id := range m.capOrder {
		if bn := m.bias[id]; bn != nil && bn.Approved != nil {
			out.Summary.BIAApproved++
			if bn.Approved.Critical {
				out.Summary.CriticalFunctions++
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Elenco delle BIA per capability

// BIARow è una capability con la sua BIA.
type BIARow struct {
	Capability struct {
		Ref
		Code     *string `json:"code"`
		ParentID *string `json:"parent_id"`
		Depth    int     `json:"depth"`
	} `json:"capability"`
	BIA                  *BIABrief `json:"bia"`
	Applications         int       `json:"applications"`
	CriticalApplications int       `json:"critical_applications"`
}

// BIAList elenca le capability, in albero, con la BIA.
func (s *Service) BIAList(ctx context.Context, tx pgx.Tx, actor engine.Actor) (map[string]any, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	roots := []string{}
	for _, id := range m.capOrder {
		c := m.caps[id]
		if c.Parent != nil {
			if _, ok := m.caps[*c.Parent]; ok {
				children[*c.Parent] = append(children[*c.Parent], id)
				continue
			}
		}
		roots = append(roots, id)
	}
	rows := []BIARow{}
	seen := map[string]bool{}
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if seen[id] || depth > 8 {
			return
		}
		seen[id] = true
		c := m.caps[id]
		var r BIARow
		r.Capability.Ref, r.Capability.Code, r.Capability.ParentID, r.Capability.Depth = c.Ref, c.Code, c.Parent, depth
		r.BIA = m.biaBrief(m.bias[id])
		for _, a := range m.appsOfCap(id) {
			r.Applications++
			if a.Critical {
				r.CriticalApplications++
			}
		}
		rows = append(rows, r)
		for _, ch := range children[id] {
			walk(ch, depth+1)
		}
	}
	for _, id := range roots {
		walk(id, 0)
	}
	return map[string]any{"items": rows, "can": can(actor)}, nil
}

// ---------------------------------------------------------------------------
// Applicazione e capability

// DepRef è una dipendenza diretta con il suo RTO.
type DepRef struct {
	Ref
	Kind   string `json:"kind"`
	RTO    *int   `json:"rto_minutes"`
	Slower bool   `json:"slower"`
}

// AppView è il blocco «Continuità» dell'applicazione.
type AppView struct {
	Application      Ref        `json:"application"`
	Declared         Pair       `json:"declared"`
	Required         ReqPair    `json:"required"`
	CriticalDeclared bool       `json:"critical_declared"`
	CriticalRequired bool       `json:"critical_required"`
	Dependencies     []DepRef   `json:"dependencies"`
	Plans            []PlanRef  `json:"plans"`
	LastTest         *TestBrief `json:"last_test"`
	Issues           []Issue    `json:"issues"`
	Can              Can        `json:"can"`
}

// ReqPair è il richiesto con la BIA di provenienza.
type ReqPair struct {
	RTO    *int `json:"rto"`
	RPO    *int `json:"rpo"`
	Source *Ref `json:"source"`
}

// Application è la continuità di un'applicazione.
func (s *Service) Application(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*AppView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != AppType {
		return nil, engine.InvalidCode("validation.continuity.subject_invalid", "ref", "non è un'applicazione")
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	out := &AppView{Application: Ref{ID: el.ID, Key: el.Key, Title: el.Title}, Dependencies: []DepRef{}, Plans: []PlanRef{}, Issues: []Issue{}, Can: can(actor)}
	a, ok := m.apps[el.ID]
	if !ok { // applicazione dismessa: niente da confrontare
		return out, nil
	}
	r := m.required(a)
	out.Declared = Pair{a.RTO, a.RPO}
	out.Required = ReqPair{RTO: r.RTO, RPO: r.RPO}
	if r.Source != nil {
		out.Required.Source = refOf(r.Source.Ref)
	}
	out.CriticalDeclared, out.CriticalRequired = a.Critical, r.Critical
	eff := effective(a, r)
	slower := func(v *int) bool { return eff != nil && v != nil && *v > *eff }
	for _, d := range m.deps[a.ID] {
		dep := m.apps[d]
		out.Dependencies = append(out.Dependencies, DepRef{Ref: dep.Ref, Kind: "application", RTO: dep.RTO, Slower: slower(dep.RTO)})
	}
	for _, pid := range m.provided[a.ID] {
		p := m.provs[pid]
		out.Dependencies = append(out.Dependencies, DepRef{Ref: p.Ref, Kind: "provider", RTO: p.RTO, Slower: slower(p.RTO)})
	}
	out.Plans = m.planRefs(append([]string{a.ID}, r.Caps...))
	if t := m.lastDone(append([]string{a.ID}, r.Caps...)...); t != nil {
		b := t.brief()
		out.LastTest = &b
	}
	out.Issues = m.appIssues(a)
	sortIssues(out.Issues)
	return out, nil
}

// CapView è il blocco «Continuità» della capability.
type CapView struct {
	Capability   Ref        `json:"capability"`
	BIA          *BIABrief  `json:"bia"`
	Applications []AppBrief `json:"applications"`
	Plans        []PlanRef  `json:"plans"`
	LastTest     *TestBrief `json:"last_test"`
	Issues       []Issue    `json:"issues"`
	Can          Can        `json:"can"`
}

// AppBrief è un'applicazione con i valori dichiarati.
type AppBrief struct {
	Ref
	RTO *int `json:"rto_minutes"`
	RPO *int `json:"rpo_minutes"`
}

// Capability è la continuità di una capability.
func (s *Service) Capability(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*CapView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != CapabilityType {
		return nil, engine.InvalidCode("validation.continuity.not_capability", "ref", "non è una capability")
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	out := &CapView{Capability: Ref{ID: el.ID, Key: el.Key, Title: el.Title}, Applications: []AppBrief{}, Plans: []PlanRef{}, Issues: []Issue{}, Can: can(actor)}
	c, ok := m.caps[el.ID]
	if !ok {
		return out, nil
	}
	out.BIA = m.biaBrief(m.bias[c.ID])
	ids := []string{c.ID}
	for _, a := range m.appsOfCap(c.ID) {
		out.Applications = append(out.Applications, AppBrief{Ref: a.Ref, RTO: a.RTO, RPO: a.RPO})
		ids = append(ids, a.ID)
	}
	out.Plans = m.planRefs(m.ancestors(c.ID))
	if t := m.lastDone(ids...); t != nil {
		b := t.brief()
		out.LastTest = &b
	}
	out.Issues = m.capIssues(c)
	sortIssues(out.Issues)
	return out, nil
}

// ---------------------------------------------------------------------------
// Livello «Continuità» della mappa

// GraphNode sono i valori di un nodo.
type GraphNode struct {
	RTODeclared *int `json:"rto_declared"`
	RTORequired *int `json:"rto_required"`
	Critical    bool `json:"critical"`
}

// GraphEdge è una dipendenza con l'esito del confronto.
type GraphEdge struct {
	Dependent  string `json:"dependent"`
	Dependency string `json:"dependency"`
	Incoherent bool   `json:"incoherent"`
}

// Graph sono i dati del livello «Continuità» della mappa.
func (s *Service) Graph(ctx context.Context, tx pgx.Tx, actor engine.Actor) (map[string]any, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	nodes := map[string]GraphNode{}
	edges := []GraphEdge{}
	for _, id := range m.appOrder {
		a := m.apps[id]
		r := m.required(a)
		nodes[id] = GraphNode{RTODeclared: a.RTO, RTORequired: r.RTO, Critical: a.Critical || r.Critical}
		eff := effective(a, r)
		bad := func(v *int) bool { return eff != nil && v != nil && *v > *eff }
		for _, d := range m.deps[id] {
			edges = append(edges, GraphEdge{Dependent: id, Dependency: d, Incoherent: bad(m.apps[d].RTO)})
		}
		for _, p := range m.provided[id] {
			edges = append(edges, GraphEdge{Dependent: id, Dependency: p, Incoherent: bad(m.provs[p].RTO)})
		}
	}
	for id, p := range m.provs {
		nodes[id] = GraphNode{RTODeclared: p.RTO}
	}
	for _, id := range m.capOrder {
		if bn := m.bias[id]; bn != nil && bn.Approved != nil {
			nodes[id] = GraphNode{RTORequired: bn.Approved.RTO, Critical: bn.Approved.Critical}
		}
	}
	return map[string]any{"nodes": nodes, "edges": edges}, nil
}

// ---------------------------------------------------------------------------
// Piani di continuità

// PlanRef è un piano collegato a un oggetto.
type PlanRef struct {
	Page   Ref `json:"page"`
	Target struct {
		Ref
		Type string `json:"type"`
	} `json:"target"`
	Approved        bool `json:"approved"`
	ApprovedVersion *int `json:"approved_version"`
}

func (m *model) planRefs(ids []string) []PlanRef {
	out := []PlanRef{}
	for _, id := range ids {
		for _, p := range m.plans[id] {
			var r PlanRef
			r.Page, r.Approved, r.ApprovedVersion = p.Page, p.ApprovedVersion != nil, p.ApprovedVersion
			if a, ok := m.apps[id]; ok {
				r.Target.Ref, r.Target.Type = a.Ref, AppType
			} else if c, ok := m.caps[id]; ok {
				r.Target.Ref, r.Target.Type = c.Ref, CapabilityType
			} else {
				r.Target.Ref.ID = id
			}
			out = append(out, r)
		}
	}
	return out
}

// Plans elenca tutti i piani collegati.
func (s *Service) Plans(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]PlanRef, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	ids := append(append([]string{}, m.capOrder...), m.appOrder...)
	return m.planRefs(ids), nil
}

// PlanInput collega una pagina come piano di continuità.
type PlanInput struct {
	Page   string `json:"page"`
	Target string `json:"target"`
}

func (s *Service) planPair(ctx context.Context, tx pgx.Tx, actor engine.Actor, in PlanInput) (*engine.Element, *engine.Element, error) {
	pg, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(in.Page))
	if err != nil || pg.TypeKey != PageType {
		return nil, nil, engine.InvalidCode("validation.continuity.plan_page", "page", "il piano è una pagina della Knowledge")
	}
	t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(in.Target))
	if err != nil || (t.TypeKey != AppType && t.TypeKey != CapabilityType) {
		return nil, nil, engine.InvalidCode("validation.continuity.plan_target", "target", "il piano riguarda una capability o un'applicazione")
	}
	return pg, t, nil
}

// LinkPlan collega il piano (continuity.write).
func (s *Service) LinkPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, in PlanInput) (*PlanRef, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := requireReady(ctx, tx); err != nil {
		return nil, err
	}
	pg, t, err := s.planPair(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), $1, rt.id, $3, $4, $5 from relation_types rt where rt.key = $2
		on conflict do nothing`, db.NewID(), RelPlanFor, pg.ID, t.ID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.plan.link", EntityType: PageType, EntityID: pg.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"target": t.Key}}); err != nil {
		return nil, err
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	for _, p := range m.planRefs([]string{t.ID}) {
		if p.Page.ID == pg.ID {
			return &p, nil
		}
	}
	return nil, engine.NotFound("page", "", "piano non collegato")
}

// UnlinkPlan scollega il piano (continuity.write).
func (s *Service) UnlinkPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, in PlanInput) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	pg, t, err := s.planPair(ctx, tx, actor, in)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from relations r using relation_types rt where rt.tenant_id = r.tenant_id and rt.id = r.type_id
		and rt.key = $1 and r.source_id = $2 and r.target_id = $3`, RelPlanFor, pg.ID, t.ID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "continuity.plan.unlink", EntityType: PageType, EntityID: pg.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"target": t.Key}})
}
