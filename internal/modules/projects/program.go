package projects

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Programmi (passo 4, progetti.md §3 «Programma», §6 «Programma e portafoglio»): il programma
// raggruppa progetti con un obiettivo comune (ext_project.program_id). I suoi numeri sono quelli
// di computeNumbers sulle voci di tutti i suoi progetti, messe in un solo piano: la stessa
// funzione dei progetti, quindi gli stessi conti ovunque. La roadmap è l'elenco dei progetti con
// date, baseline e milestone.
//
// Si vedono solo i progetti che l'attore legge, con i limiti del singolo elemento come il piano.

// RoleProgramManager: chi governa il programma (progetti.md §5): mette i progetti nel programma.
const RoleProgramManager = "program_manager"

// Milestone è una milestone nella roadmap del programma.
type RoadmapMilestone struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Title         string  `json:"title"`
	Date          *string `json:"date"`
	BaselineDate  *string `json:"baseline_date"`
	StateCategory string  `json:"state_category"`
	Contractual   bool    `json:"contractual"`
	DaysLate      int     `json:"days_late"`
	Restricted    bool    `json:"restricted,omitempty"`
}

// ProgramProject è un progetto del programma con i suoi numeri e le sue milestone.
type ProgramProject struct {
	ID            string             `json:"id"`
	Key           string             `json:"key"`
	Title         string             `json:"title"`
	State         string             `json:"state"`
	StateCategory string             `json:"state_category"`
	PM            *string            `json:"project_manager"`
	Progress      float64            `json:"progress"`
	Signal        string             `json:"signal"`
	Reasons       []string           `json:"reasons"`
	Start         *string            `json:"start"`
	End           *string            `json:"end"`
	BaselineStart *string            `json:"baseline_start"`
	BaselineEnd   *string            `json:"baseline_end"`
	VarianceDays  *int               `json:"variance_days"`
	WorkPackages  int                `json:"work_packages"`
	Late          int                `json:"late"`
	Conflicts     int                `json:"conflicts"`
	ClaimsPending int                `json:"claims_pending"`
	Milestones    []RoadmapMilestone `json:"milestones"`
	// Budget del progetto (passo 5), solo a chi lo vede; senza l'elenco delle versioni.
	Budget *Budget `json:"budget"`
}

// ProgramView è la pagina del programma.
type ProgramView struct {
	Program  Ref              `json:"program"`
	State    string           `json:"state"`
	Numbers  Numbers          `json:"numbers"`
	Projects []ProgramProject `json:"projects"`
	// CanEdit: può mettere o togliere progetti dal programma.
	CanEdit bool `json:"can_edit"`
}

// ProgramSummary è un programma nell'elenco.
type ProgramSummary struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	StateCategory string   `json:"state_category"`
	Projects      int      `json:"projects"`
	Progress      float64  `json:"progress"`
	Signal        string   `json:"signal"`
	Start         *string  `json:"start"`
	End           *string  `json:"end"`
	NextMilestone *ItemRef `json:"next_milestone"`
	Late          int      `json:"late"`
}

var signalRank = map[string]int{"none": 0, "green": 1, "amber": 2, "red": 3}

func canEditProgram(ctx context.Context, tx pgx.Tx, actor engine.Actor, programID string) (bool, error) {
	if actor.Has("element.write") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, programID, actor.MemberID, []string{RoleProgramManager, "pmo"})
}

func (s *Service) program(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != TypeProgram {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// programNumbers carica i progetti leggibili del programma e calcola i numeri aggregati: le voci
// di tutti i progetti in un solo piano per computeNumbers; il semaforo è il peggiore tra quello
// aggregato e quelli dei progetti (che vedono anche baseline e budget).
func (s *Service) programNumbers(ctx context.Context, tx pgx.Tx, actor engine.Actor, programID string, withBudget bool) (Numbers, []ProgramProject, error) {
	rows, err := tx.Query(ctx, `select x.element_id::text from ext_project x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null and not e.is_draft
		where x.program_id = $1 order by e.start_at nulls last, e.number`, programID)
	if err != nil {
		return Numbers{}, nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Numbers{}, nil, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return Numbers{}, nil, err
	}
	merged := &Plan{Items: []Item{}, Phases: []Phase{}, Method: st.ProgressMethod,
		Tolerance: Tolerance{Days: st.ToleranceDays, BudgetPct: st.ToleranceBudgetPct}}
	merged.Project.ID = programID
	out := []ProgramProject{}
	var budgets []*Budget
	rd := s.reader(actor)
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			if errors.Is(err, engine.ErrNotFound) || errors.Is(err, engine.ErrForbidden) {
				continue // progetto che l'attore non vede
			}
			return Numbers{}, nil, err
		}
		p, err := s.loadPlan(ctx, tx, el)
		if err != nil {
			return Numbers{}, nil, err
		}
		if err := s.limitPlan(ctx, tx, actor, p, rd); err != nil {
			return Numbers{}, nil, err
		}
		n, err := s.numbersOf(ctx, tx, actor, p, withBudget)
		if err != nil {
			return Numbers{}, nil, err
		}
		merged.Items = append(merged.Items, p.Items...)
		if n.Budget != nil {
			budgets = append(budgets, n.Budget)
		}
		pp := ProgramProject{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, StateCategory: el.StateCategory,
			Progress: n.Progress, Signal: n.Signal, Reasons: n.Reasons, Start: n.Start, End: n.End, BaselineStart: n.BaselineStart,
			BaselineEnd: n.BaselineEnd, VarianceDays: n.VarianceDays, WorkPackages: n.WorkPackages, Late: len(n.Late),
			Conflicts: len(n.Conflicts), ClaimsPending: n.ClaimsPending, Milestones: []RoadmapMilestone{}}
		if n.Budget != nil {
			b := *n.Budget
			b.Budgets = nil
			pp.Budget = &b
		}
		if pp.Start == nil {
			pp.Start, pp.End = p.Project.Start, p.Project.End
		}
		late := map[string]int{}
		for _, l := range n.Late {
			late[l.ID] = l.DaysLate
		}
		for _, it := range p.Items {
			if it.Kind != TypeMilestone || it.StateCategory == "cancelled" {
				continue
			}
			pp.Milestones = append(pp.Milestones, RoadmapMilestone{ID: it.ID, Key: it.Key, Title: it.Title, Date: it.End,
				BaselineDate: it.BaselineEnd, StateCategory: it.StateCategory, Contractual: it.Contractual, DaysLate: late[it.ID], Restricted: it.Restricted})
		}
		_ = tx.QueryRow(ctx, `select m.display_name from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'project_manager'
			join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
			where sh.element_id = $1 order by sh.created_at limit 1`, el.ID).Scan(&pp.PM)
		out = append(out, pp)
	}
	agg := computeNumbers(merged, today())
	agg.Budget = sumBudgets(budgets)
	for _, pp := range out {
		if signalRank[pp.Signal] > signalRank[agg.Signal] {
			agg.Signal = pp.Signal
		}
		for _, r := range pp.Reasons {
			if !strings.Contains(","+strings.Join(agg.Reasons, ",")+",", ","+r+",") {
				agg.Reasons = append(agg.Reasons, r)
			}
		}
	}
	return agg, out, nil
}

// sumBudgets somma i budget dei progetti nella stessa valuta (nil se mancano o le valute differiscono).
func sumBudgets(bs []*Budget) *Budget {
	if len(bs) == 0 {
		return nil
	}
	out := &Budget{Currency: bs[0].Currency}
	add := func(dst **int64, v *int64) {
		if v == nil {
			return
		}
		t := *v
		if *dst != nil {
			t += **dst
		}
		*dst = &t
	}
	for _, b := range bs {
		if b.Currency != out.Currency {
			return nil
		}
		add(&out.ApprovedCents, b.ApprovedCents)
		add(&out.ForecastCents, b.ForecastCents)
		out.SpentCents += b.SpentCents
		out.CommittedCents += b.CommittedCents
	}
	return out
}

// Program restituisce la pagina del programma: numeri aggregati, progetti e roadmap.
func (s *Service) Program(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ProgramView, error) {
	el, err := s.program(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	n, projects, err := s.programNumbers(ctx, tx, actor, el.ID, true)
	if err != nil {
		return nil, err
	}
	v := &ProgramView{Program: Ref{ID: el.ID, Key: el.Key, Title: el.Title}, State: el.State, Numbers: n, Projects: projects}
	if v.CanEdit, err = canEditProgram(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	return v, nil
}

// Programs elenca i programmi (aperti, o anche i chiusi) con i numeri aggregati.
func (s *Service) Programs(ctx context.Context, tx pgx.Tx, actor engine.Actor, includeClosed bool) ([]ProgramSummary, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	q := `select e.id::text from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'program'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft`
	if !includeClosed {
		q += ` and coalesce(o.category, '') not in ('done', 'cancelled')`
	}
	rows, err := tx.Query(ctx, q+` order by e.title limit 200`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []ProgramSummary{}
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			continue
		}
		n, projects, err := s.programNumbers(ctx, tx, actor, el.ID, false)
		if err != nil {
			return nil, err
		}
		out = append(out, ProgramSummary{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, StateCategory: el.StateCategory,
			Projects: len(projects), Progress: n.Progress, Signal: n.Signal, Start: n.Start, End: n.End, NextMilestone: n.NextMilestone, Late: len(n.Late)})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out, nil
}

// ProgramInput crea un programma.
type ProgramInput struct {
	Title    string `json:"title"`
	BodyText string `json:"body_text"`
}

// CreateProgram crea un programma (chi può scrivere gli elementi); chi lo crea ne è il program manager.
func (s *Service) CreateProgram(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ProgramInput) (*engine.Element, error) {
	if !actor.Has("element.write") {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeProgram, Title: in.Title,
		BodyText: in.BodyText, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if actor.MemberID != "" {
		if err := engine.SetStakeholder(ctx, tx, actor, el.ID, RoleProgramManager, actor.MemberID, "", false); err != nil {
			return nil, err
		}
	}
	return s.eng.StartProcess(ctx, tx, actor, el)
}

// SetProgram mette il progetto in un programma ("" lo toglie). Serve poter pianificare il
// progetto oppure governare il programma.
func (s *Service) SetProgram(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectRef, programRef string) (*Ref, error) {
	proj, err := s.project(ctx, tx, actor, projectRef)
	if err != nil {
		return nil, err
	}
	var prog *engine.Element
	if strings.TrimSpace(programRef) != "" {
		if prog, err = s.program(ctx, tx, actor, programRef); err != nil {
			if errors.Is(err, engine.ErrNotFound) {
				return nil, engine.NotFound("program", "program", "programma inesistente")
			}
			return nil, err
		}
	}
	ok, err := canPlan(ctx, tx, actor, proj.ID)
	if err != nil {
		return nil, err
	}
	if !ok && prog != nil {
		if ok, err = canEditProgram(ctx, tx, actor, prog.ID); err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	var progID any
	var out *Ref
	if prog != nil {
		progID = prog.ID
		out = &Ref{ID: prog.ID, Key: prog.Key, Title: prog.Title}
	}
	if _, err := tx.Exec(ctx, `insert into ext_project (tenant_id, element_id, program_id) values (app_tenant(), $1, $2)
		on conflict (tenant_id, element_id) do update set program_id = excluded.program_id`, proj.ID, progID); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "plan", "program", map[string]any{"program": progID}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "projects.program", EntityType: TypeProject, EntityID: proj.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"program_id": progID}}); err != nil {
		return nil, err
	}
	return out, nil
}
