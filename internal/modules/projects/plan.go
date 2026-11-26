package projects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/planning"
	"regia/internal/modules/work"
)

// Head è il progetto in testa al piano.
type Head struct {
	ID            string       `json:"id"`
	Key           string       `json:"key"`
	Title         string       `json:"title"`
	State         string       `json:"state"`
	StateCategory string       `json:"state_category"`
	Template      string       `json:"template"`
	Start         *string      `json:"start"`
	End           *string      `json:"end"`
	Requests      []RequestRef `json:"requests"`
	// Target: la scadenza normativa (o, senza, la data desiderata) più vicina delle richieste.
	Target     *string `json:"target"`
	TargetKind string  `json:"target_kind"` // regulatory | desired | ""
	Program    *Ref    `json:"program"`
}

// RequestRef è una richiesta confluita nel progetto.
type RequestRef struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state"`
	// Restricted: richiesta che l'attore non può leggere (senza titolo né stato).
	Restricted bool `json:"restricted,omitempty"`
}

// Ref è un riferimento leggibile.
type Ref struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Phase è una fase del piano, con le date dell'inviluppo delle sue voci.
type Phase struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Ordinal       int     `json:"ordinal"`
	GateEnabled   bool    `json:"gate_enabled"`
	GateStatus    string  `json:"gate_status"` // open | pending | approved | rejected
	GateRequestID *string `json:"gate_request_id"`
	Start         *string `json:"start"`
	End           *string `json:"end"`
	Progress      float64 `json:"progress"`
}

// Item è una voce del piano: work package, attività o milestone.
type Item struct {
	ID            string         `json:"id"`
	Key           string         `json:"key"`
	Title         string         `json:"title"`
	Kind          string         `json:"kind"` // work_package | plan_activity | milestone
	PhaseID       *string        `json:"phase_id"`
	ParentID      *string        `json:"parent_id"` // work package dell'attività (o della milestone)
	State         string         `json:"state"`
	StateCategory string         `json:"state_category"`
	Start         *string        `json:"start"`
	End           *string        `json:"end"`
	Mode          *string        `json:"delivery_mode"`
	TeamID        *string        `json:"team_id"`
	Team          *string        `json:"team"`
	ApplicationID *string        `json:"application_id"`
	Application   *string        `json:"application"`
	SupplierID    *string        `json:"supplier_id"`
	Supplier      *string        `json:"supplier"`
	AssigneeID    *string        `json:"assignee_id"`
	Assignee      *string        `json:"assignee"`
	EffortDays    *float64       `json:"estimate_effort_days"`
	Weight        *float64       `json:"weight"`
	Contractual   bool           `json:"contractual"`
	Impact        *Ref           `json:"impact"`
	Epics         []Ref          `json:"epics"`
	Deps          []planning.Dep `json:"deps"`
	Progress      float64        `json:"progress"`        // 0..100
	ProgressFrom  string         `json:"progress_source"` // epics | activities | declared | state | none
	Capacity      float64        `json:"team_capacity"`
	// Baseline in vigore (passo 3): date della voce nella baseline e scostamento della fine in
	// giorni lavorativi (positivo = più tardi). InBaseline è falso per le voci nate dopo.
	InBaseline    bool    `json:"in_baseline"`
	BaselineStart *string `json:"baseline_start"`
	BaselineEnd   *string `json:"baseline_end"`
	VarianceDays  *int    `json:"variance_days"`
	// Fornitore (passo 4): il contatto del fornitore dichiara i SAL (supplier_updates); SAL da
	// accettare, deliverable non annullati e accettati.
	SupplierUpdates      bool `json:"supplier_updates"`
	ClaimsPending        int  `json:"claims_pending"`
	Deliverables         int  `json:"deliverables"`
	DeliverablesAccepted int  `json:"deliverables_accepted"`
	// Conflicts: altri progetti che toccano la stessa applicazione nello stesso periodo.
	Conflicts []Conflict `json:"conflicts,omitempty"`
	// Restricted: l'attore non può leggere la voce; restano date, avanzamento e dipendenze.
	Restricted    bool           `json:"restricted,omitempty"`
	declared      *float64       // ultima percentuale dichiarata (work package senza epic)
	epicProgress  *work.Progress // avanzamento degli epic (work package agile)
	claimProgress *float64       // avanzamento dai SAL accettati (work package del fornitore)
}

// Plan è il piano del progetto.
type Plan struct {
	Project Head    `json:"project"`
	Phases  []Phase `json:"phases"`
	Items   []Item  `json:"items"`
	CanEdit bool    `json:"can_edit"`
	// Method: metodo di avanzamento del tenant (weighted_estimate | equal).
	Method string `json:"progress_method"`
	// Baseline in vigore (nil se il piano non ne ha) e tolleranze del progetto.
	Baseline  *BaselineHead `json:"baseline"`
	Tolerance Tolerance     `json:"tolerance"`
}

// BaselineHead è la baseline in vigore, senza le voci.
type BaselineHead struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	Start     *string `json:"start"`
	End       *string `json:"end"`
	CreatedAt string  `json:"created_at"`
	CreatedBy *string `json:"created_by"`
	// Budget della baseline: lo vede solo chi vede il budget del progetto (numbersOf).
	budget *Budget
}

// Tolerance sono le tolleranze del progetto: quelle del tenant, se il progetto non ne ha di sue.
type Tolerance struct {
	Days      int     `json:"days"`
	BudgetPct float64 `json:"budget_pct"`
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.DateOnly)
	return &s
}

func today() time.Time { return time.Now().UTC().Truncate(24 * time.Hour) }

// GetPlan restituisce il piano del progetto.
func (s *Service) GetPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Plan, error) {
	el, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	p, err := s.loadPlan(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	if err := s.limitPlan(ctx, tx, actor, p, nil); err != nil {
		return nil, err
	}
	if p.CanEdit, err = canPlan(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	return p, nil
}

// loadPlanByItem carica il piano del progetto di una voce (nil se non è una voce di piano).
func (s *Service) loadPlanByItem(ctx context.Context, tx pgx.Tx, itemID string) (*Plan, error) {
	var projectID string
	err := tx.QueryRow(ctx, `select coalesce(
		(select project_id from ext_work_package where element_id = $1),
		(select project_id from ext_plan_activity where element_id = $1),
		(select project_id from ext_milestone where element_id = $1))::text`, itemID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) || projectID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	el, err := s.eng.Get(ctx, tx, engine.Actor{Permissions: []string{"element.read"}}, projectID)
	if err != nil {
		return nil, err
	}
	return s.loadPlan(ctx, tx, el)
}

// itemProject restituisce il progetto della voce e il tipo di voce.
func itemProject(ctx context.Context, tx pgx.Tx, itemID string) (string, string, error) {
	var projectID, kind string
	err := tx.QueryRow(ctx, `
		select project_id::text, 'work_package' from ext_work_package where element_id = $1
		union all select project_id::text, 'plan_activity' from ext_plan_activity where element_id = $1
		union all select project_id::text, 'milestone' from ext_milestone where element_id = $1
		limit 1`, itemID).Scan(&projectID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", engine.ErrNotFound
	}
	return projectID, kind, err
}

const itemsSQL = `
	with items as (
		select x.element_id id, 'work_package' kind, x.phase_id, null::uuid parent_id, x.delivery_mode mode, x.team_id,
		       x.application_id, x.supplier_id, x.estimate_effort_days eff, x.weight, x.impact_id, false contractual, null::numeric pct
		from ext_work_package x where x.project_id = $1
		union all
		select a.element_id, 'plan_activity', w.phase_id, a.work_package_id, null, null, null, null, a.estimate_effort_days, null, null, false, a.percent
		from ext_plan_activity a left join ext_work_package w on w.tenant_id = a.tenant_id and w.element_id = a.work_package_id
		where a.project_id = $1
		union all
		select m.element_id, 'milestone', m.phase_id, m.work_package_id, null, null, null, null, null, null, null, m.contractual, null
		from ext_milestone m where m.project_id = $1)
	select i.id::text, sp.key || '-' || e.number, e.title, i.kind, i.phase_id::text, i.parent_id::text,
	       coalesce(o.name, ''), coalesce(o.category, ''), e.start_at, e.due_at, i.mode, i.team_id::text, t.name,
	       i.application_id::text, ap.title, i.supplier_id::text, su.title, e.assignee_id::text, mb.display_name,
	       i.eff::float8, i.weight::float8, i.contractual, i.pct::float8,
	       i.impact_id::text, case when im.id is not null then ims.key || '-' || im.number end, im.title,
	       (select pp.percent::float8 from ext_plan_progress pp where pp.element_id = i.id order by pp.created_at desc limit 1),
	       coalesce((select jsonb_agg(jsonb_build_object('id', rel.target_id::text, 'kind', coalesce(rel.attrs->>'kind', 'fs'),
	                   'lag', coalesce((rel.attrs->>'lag')::int, 0)) order by rel.created_at)
	                 from relations rel join relation_types rt on rt.tenant_id = rel.tenant_id and rt.id = rel.type_id and rt.key = 'depends_on'
	                 where rel.source_id = i.id), '[]'::jsonb),
	       coalesce(t.weekly_capacity_hours::float8 / 8, 0)
	from items i
	join elements e on e.tenant_id = app_tenant() and e.id = i.id and e.deleted_at is null
	join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	left join teams t on t.tenant_id = e.tenant_id and t.id = i.team_id
	left join elements ap on ap.tenant_id = e.tenant_id and ap.id = i.application_id
	left join elements su on su.tenant_id = e.tenant_id and su.id = i.supplier_id
	left join members mb on mb.tenant_id = e.tenant_id and mb.id = e.assignee_id
	left join elements im on im.tenant_id = e.tenant_id and im.id = i.impact_id and im.deleted_at is null
	left join spaces ims on ims.tenant_id = im.tenant_id and ims.id = im.space_id
	order by e.start_at nulls last, e.number`

// loadPlan legge fasi e voci del progetto e calcola l'avanzamento di ogni voce.
func (s *Service) loadPlan(ctx context.Context, tx pgx.Tx, el *engine.Element) (*Plan, error) {
	p := &Plan{Phases: []Phase{}, Items: []Item{}}
	h := &p.Project
	h.ID, h.Key, h.Title, h.State, h.StateCategory = el.ID, el.Key, el.Title, el.State, el.StateCategory
	h.Start, h.End = dateStr(el.StartAt), dateStr(el.DueAt)
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	p.Method = st.ProgressMethod
	p.Tolerance = Tolerance{Days: st.ToleranceDays, BudgetPct: st.ToleranceBudgetPct}
	var programID, programKey, programTitle *string
	var tolDays *int
	var tolBudget *float64
	err = tx.QueryRow(ctx, `select x.template, x.program_id::text, ps.key || '-' || pg.number, pg.title,
		x.tolerance_days, x.tolerance_budget_pct::float8 from ext_project x
		left join elements pg on pg.tenant_id = x.tenant_id and pg.id = x.program_id and pg.deleted_at is null
		left join spaces ps on ps.tenant_id = pg.tenant_id and ps.id = pg.space_id
		where x.element_id = $1`, el.ID).Scan(&h.Template, &programID, &programKey, &programTitle, &tolDays, &tolBudget)
	if tolDays != nil {
		p.Tolerance.Days = *tolDays
	}
	if tolBudget != nil {
		p.Tolerance.BudgetPct = *tolBudget
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.Template = "mixed"
	} else if err != nil {
		return nil, err
	}
	if programID != nil && programKey != nil {
		h.Program = &Ref{ID: *programID, Key: *programKey, Title: deref(programTitle)}
	}
	rows, err := tx.Query(ctx, `select r.id::text, s.key || '-' || r.number, r.title, coalesce(o.name, '')
		from project_requests pr
		join elements r on r.tenant_id = pr.tenant_id and r.id = pr.request_id and r.deleted_at is null
		join spaces s on s.tenant_id = r.tenant_id and s.id = r.space_id
		left join options o on o.tenant_id = r.tenant_id and o.id = r.state_id
		where pr.project_id = $1 order by pr.created_at`, el.ID)
	if err != nil {
		return nil, err
	}
	if h.Requests, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestRef, error) {
		var rq RequestRef
		err := r.Scan(&rq.ID, &rq.Key, &rq.Title, &rq.State)
		return rq, err
	}); err != nil {
		return nil, err
	}
	var reg, desired *time.Time
	if err := tx.QueryRow(ctx, `select min(d.regulatory_deadline)::timestamptz, min(d.desired_date)::timestamptz from project_requests pr
		join ext_demand d on d.tenant_id = pr.tenant_id and d.element_id = pr.request_id where pr.project_id = $1`, el.ID).Scan(&reg, &desired); err != nil {
		return nil, err
	}
	switch {
	case reg != nil:
		h.Target, h.TargetKind = dateStr(reg), "regulatory"
	case desired != nil:
		h.Target, h.TargetKind = dateStr(desired), "desired"
	}
	rows, err = tx.Query(ctx, `select id::text, name, ordinal, gate_enabled, gate_status, gate_request_id::text
		from plan_phases where project_id = $1 order by ordinal, created_at`, el.ID)
	if err != nil {
		return nil, err
	}
	if p.Phases, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Phase, error) {
		var ph Phase
		err := r.Scan(&ph.ID, &ph.Name, &ph.Ordinal, &ph.GateEnabled, &ph.GateStatus, &ph.GateRequestID)
		return ph, err
	}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, itemsSQL, el.ID)
	if err != nil {
		return nil, err
	}
	p.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var it Item
		var start, end *time.Time
		var pct *float64
		var impactID, impactKey, impactTitle *string
		var deps []byte
		err := r.Scan(&it.ID, &it.Key, &it.Title, &it.Kind, &it.PhaseID, &it.ParentID, &it.State, &it.StateCategory,
			&start, &end, &it.Mode, &it.TeamID, &it.Team, &it.ApplicationID, &it.Application, &it.SupplierID, &it.Supplier,
			&it.AssigneeID, &it.Assignee, &it.EffortDays, &it.Weight, &it.Contractual, &pct,
			&impactID, &impactKey, &impactTitle, &it.declared, &deps, &it.Capacity)
		if err != nil {
			return it, err
		}
		it.Start, it.End = dateStr(start), dateStr(end)
		if impactID != nil && impactKey != nil {
			it.Impact = &Ref{ID: *impactID, Key: *impactKey, Title: deref(impactTitle)}
		}
		if pct != nil {
			it.declared = pct // attività: la percentuale corrente
		}
		it.Epics = []Ref{}
		if err := json.Unmarshal(deps, &it.Deps); err != nil {
			return it, err
		}
		return it, nil
	})
	if err != nil {
		return nil, err
	}
	// Epic dei work package agili.
	for i := range p.Items {
		it := &p.Items[i]
		if it.Kind != TypeWorkPackage || it.Mode == nil || *it.Mode != ModeAgile {
			continue
		}
		ids, err := work.EpicsOfWorkPackage(ctx, tx, it.ID)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		pr, err := work.ProgressOf(ctx, tx, ids)
		if err != nil {
			return nil, err
		}
		it.epicProgress = &pr
		rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title from elements e
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = any($1::uuid[]) order by s.key, e.number`, ids)
		if err != nil {
			return nil, err
		}
		if it.Epics, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Ref]); err != nil {
			return nil, err
		}
	}
	if err := loadBaseline(ctx, tx, p); err != nil {
		return nil, err
	}
	if err := loadSupplier(ctx, tx, p); err != nil {
		return nil, err
	}
	if err := loadConflicts(ctx, tx, p); err != nil {
		return nil, err
	}
	computeItemProgress(p)
	computeItemVariance(p)
	computePhases(p)
	return p, nil
}

// tasks traduce le voci per il motore del Gantt.
func (p *Plan) tasks() []planning.Task {
	out := make([]planning.Task, 0, len(p.Items))
	for _, it := range p.Items {
		deps := it.Deps
		if deps == nil {
			deps = []planning.Dep{}
		}
		out = append(out, planning.Task{ID: it.ID, Key: it.Key, StateCategory: it.StateCategory, Start: it.Start, End: it.End,
			EffortDays: it.EffortDays, Capacity: it.Capacity, Deps: deps, Milestone: it.Kind == TypeMilestone, TeamID: it.TeamID, Team: it.Team})
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
