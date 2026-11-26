package projects

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/planning"
	"regia/internal/platform/calendar"
)

// Avanzamento e numeri del progetto (progetti.md §3, «Avanzamento e numeri del progetto»).
//
// Avanzamento di una voce:
//   - work package agile: story chiuse degli epic del team (create_epic); senza epic, la
//     percentuale dichiarata;
//   - work package a cascata: media delle attività pesata sulle stime (pari peso senza stime);
//     senza attività, la percentuale dichiarata;
//   - work package del fornitore: l'ultimo SAL accettato (percentuale complessiva, oppure importi
//     accettati sul valore del contratto); senza SAL, i deliverable accettati sul totale; poi la
//     percentuale dichiarata;
//   - attività: la percentuale dichiarata, storicizzata in ext_plan_progress;
//   - una voce chiusa vale 100, una annullata non conta.
//
// Avanzamento del progetto: media dei work package pesata sul peso impostato a mano, altrimenti
// sulla stima in giorni; un work package senza stima pesa quanto la media delle stime degli
// altri (1 se nessuno ha una stima). Con il metodo «equal» pesano tutti uguale.

func computeItemProgress(p *Plan) {
	activities := map[string][]*Item{}
	for i := range p.Items {
		it := &p.Items[i]
		if it.Kind == TypeActivity && it.ParentID != nil {
			activities[*it.ParentID] = append(activities[*it.ParentID], it)
		}
	}
	for i := range p.Items {
		it := &p.Items[i]
		switch {
		case it.StateCategory == "done":
			it.Progress, it.ProgressFrom = 100, "state"
		case it.StateCategory == "cancelled":
			it.Progress, it.ProgressFrom = 0, "state"
		case it.Kind == TypeMilestone:
			it.Progress, it.ProgressFrom = 0, "state"
		case it.Kind == TypeActivity:
			it.Progress, it.ProgressFrom = valueOr(it.declared, 0), "declared"
		}
	}
	for i := range p.Items {
		it := &p.Items[i]
		if it.Kind != TypeWorkPackage || it.StateCategory == "done" || it.StateCategory == "cancelled" {
			continue
		}
		mode := deref(it.Mode)
		switch {
		case mode == ModeAgile && it.epicProgress != nil && it.epicProgress.Total > 0:
			it.Progress, it.ProgressFrom = it.epicProgress.Percent, "epics"
		case mode == ModeWaterfall && len(live(activities[it.ID])) > 0:
			acts := live(activities[it.ID])
			ws := make([]float64, len(acts))
			vs := make([]float64, len(acts))
			for j, a := range acts {
				ws[j] = valueOr(a.EffortDays, -1)
				vs[j] = a.Progress
			}
			it.Progress, it.ProgressFrom = weighted(vs, fillWeights(ws, p.Method == "equal")), "activities"
		case mode == ModeSupplier && it.claimProgress != nil:
			it.Progress, it.ProgressFrom = *it.claimProgress, "claims"
		case mode == ModeSupplier && it.DeliverablesAccepted > 0:
			it.Progress, it.ProgressFrom = float64(it.DeliverablesAccepted)*100/float64(it.Deliverables), "deliverables"
		case it.declared != nil:
			it.Progress, it.ProgressFrom = *it.declared, "declared"
		default:
			it.Progress, it.ProgressFrom = 0, "none"
		}
		it.Progress = round1(it.Progress)
	}
}

func live(items []*Item) []*Item {
	out := items[:0:0]
	for _, it := range items {
		if it.StateCategory != "cancelled" {
			out = append(out, it)
		}
	}
	return out
}

func valueOr(v *float64, d float64) float64 {
	if v == nil {
		return d
	}
	return *v
}

// fillWeights sostituisce i pesi mancanti (< 0) con la media degli altri, o 1 se mancano tutti.
func fillWeights(ws []float64, equal bool) []float64 {
	out := make([]float64, len(ws))
	sum, n := 0.0, 0
	for _, w := range ws {
		if w > 0 {
			sum += w
			n++
		}
	}
	avg := 1.0
	if n > 0 {
		avg = sum / float64(n)
	}
	for i, w := range ws {
		switch {
		case equal:
			out[i] = 1
		case w > 0:
			out[i] = w
		default:
			out[i] = avg
		}
	}
	return out
}

func weighted(vs, ws []float64) float64 {
	num, den := 0.0, 0.0
	for i := range vs {
		num += vs[i] * ws[i]
		den += ws[i]
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// wpWeights sono i pesi dei work package non annullati, nell'ordine di Items.
func wpWeights(p *Plan) ([]*Item, []float64) {
	var wps []*Item
	var ws []float64
	for i := range p.Items {
		it := &p.Items[i]
		if it.Kind != TypeWorkPackage || it.StateCategory == "cancelled" {
			continue
		}
		wps = append(wps, it)
		switch {
		case it.Weight != nil && *it.Weight > 0:
			ws = append(ws, *it.Weight)
		default:
			ws = append(ws, valueOr(it.EffortDays, -1))
		}
	}
	if p.Method == "equal" {
		return wps, fillWeights(ws, true)
	}
	// Il peso manuale vale come stima: la media si fa su tutti i pesi noti.
	return wps, fillWeights(ws, false)
}

// computePhases: inviluppo delle date e avanzamento pesato di ogni fase.
func computePhases(p *Plan) {
	for i := range p.Phases {
		ph := &p.Phases[i]
		var vs, ws []float64
		wps, weights := wpWeights(p)
		for j, it := range wps {
			if it.PhaseID != nil && *it.PhaseID == ph.ID {
				vs = append(vs, it.Progress)
				ws = append(ws, weights[j])
			}
		}
		ph.Progress = round1(weighted(vs, ws))
		for _, it := range p.Items {
			if it.PhaseID == nil || *it.PhaseID != ph.ID || it.Start == nil || it.StateCategory == "cancelled" {
				continue
			}
			if ph.Start == nil || *it.Start < *ph.Start {
				ph.Start = it.Start
			}
			if it.End != nil && (ph.End == nil || *it.End > *ph.End) {
				ph.End = it.End
			}
		}
	}
}

// Numbers sono i numeri del progetto, uguali ovunque (Panoramica, elenco, fatti, status report).
type Numbers struct {
	Progress float64 `json:"progress"`
	// Date di previsione (inviluppo delle voci), di baseline ed effettive.
	Start         *string `json:"start"`
	End           *string `json:"end"`
	BaselineStart *string `json:"baseline_start"`
	BaselineEnd   *string `json:"baseline_end"`
	// Baseline in vigore (numero) e scostamento della fine prevista da quella di baseline, in
	// giorni lavorativi e in percentuale della durata di baseline.
	Baseline     *int     `json:"baseline"`
	VarianceDays *int     `json:"variance_days"`
	VariancePct  *float64 `json:"variance_pct"`
	// Oltre le tolleranze: milestone (DaysLate è lo scostamento), fine del progetto e budget.
	Tolerance        Tolerance `json:"tolerance"`
	OverTolerance    []ItemRef `json:"over_tolerance"`
	EndOverTolerance bool      `json:"end_over_tolerance"`
	// BudgetVariancePct: costo previsto (o approvato) rispetto alla baseline, solo a chi vede il budget.
	BudgetVariancePct   *float64 `json:"budget_variance_pct"`
	BudgetOverTolerance bool     `json:"budget_over_tolerance"`
	ActualStart         *string  `json:"actual_start"`
	ActualEnd           *string  `json:"actual_end"`
	// Target e margine in giorni lavorativi (negativo = oltre la scadenza).
	Target     *string `json:"target"`
	TargetKind string  `json:"target_kind"`
	MarginDays *int    `json:"margin_days"`

	WorkPackages     int        `json:"work_packages"`
	WorkPackagesDone int        `json:"work_packages_done"`
	Milestones       int        `json:"milestones"`
	MilestonesDone   int        `json:"milestones_done"`
	ByMode           []ModeStat `json:"by_mode"`
	NextMilestone    *ItemRef   `json:"next_milestone"`
	Late             []ItemRef  `json:"late"`
	Blocked          []Blocked  `json:"blocked"`
	// Fornitori e componenti condivisi (passo 4): SAL da accettare, deliverable consegnati da
	// accettare e conflitti con altri progetti sulla stessa applicazione.
	ClaimsPending int        `json:"claims_pending"`
	Conflicts     []Conflict `json:"conflicts"`
	// Change request aperte e rischi collegati al progetto o alle sue richieste.
	OpenChangeRequests int       `json:"open_change_requests"`
	Risks              []ItemRef `json:"risks"`
	MissingRoles       []RoleRef `json:"missing_roles"`
	Budget             *Budget   `json:"budget"`
	// Signal: green | amber | red | none (piano vuoto); Reasons sono i codici del perché.
	Signal  string   `json:"signal"`
	Reasons []string `json:"reasons"`
}

// ModeStat: quanti work package per modo di consegna e il loro avanzamento.
type ModeStat struct {
	Mode     string  `json:"mode"`
	Count    int     `json:"count"`
	Progress float64 `json:"progress"`
}

// ItemRef è una voce citata dalla Panoramica.
type ItemRef struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Kind     string  `json:"kind"`
	Date     *string `json:"date"`
	DaysLate int     `json:"days_late"`
	Team     *string `json:"team"`
	Progress float64 `json:"progress"`
	State    string  `json:"state"`
	// Restricted: voce che l'attore non può leggere (senza titolo né team).
	Restricted bool `json:"restricted,omitempty"`
}

// Blocked è una voce che aspetta un predecessore non chiuso (waiting) o che non rispetta
// una dipendenza (violated).
type Blocked struct {
	Item    ItemRef `json:"item"`
	WaitsOn ItemRef `json:"waits_on"`
	Reason  string  `json:"reason"` // waiting | violated
	Kind    string  `json:"dep_kind"`
}

// Budget sono gli importi delle richieste confluite (aggregati, senza voci né tariffe).
type Budget struct {
	Currency       string `json:"currency"`
	ApprovedCents  *int64 `json:"approved_cents"`
	SpentCents     int64  `json:"spent_cents"`
	CommittedCents int64  `json:"committed_cents"`
	ForecastCents  *int64 `json:"forecast_cents"`
}

func ref(it *Item) ItemRef {
	return ItemRef{ID: it.ID, Key: it.Key, Title: it.Title, Kind: it.Kind, Team: it.Team, Progress: it.Progress, State: it.State, Date: it.End, Restricted: it.Restricted}
}

func parse(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := calendar.Parse(*s)
	if err != nil {
		return nil
	}
	return &t
}

// computeNumbers calcola i numeri del piano già caricato: è l'unica funzione che li calcola.
func computeNumbers(p *Plan, now time.Time) Numbers {
	n := Numbers{Late: []ItemRef{}, Blocked: []Blocked{}, Risks: []ItemRef{}, MissingRoles: []RoleRef{}, ByMode: []ModeStat{}, Reasons: []string{},
		OverTolerance: []ItemRef{}, Tolerance: p.Tolerance, Conflicts: []Conflict{}}
	wps, ws := wpWeights(p)
	vs := make([]float64, len(wps))
	byMode := map[string]*struct {
		n      int
		vs, ws []float64
	}{}
	for i, it := range wps {
		vs[i] = it.Progress
		n.WorkPackages++
		if it.StateCategory == "done" {
			n.WorkPackagesDone++
		}
		m := deref(it.Mode)
		if byMode[m] == nil {
			byMode[m] = &struct {
				n      int
				vs, ws []float64
			}{}
		}
		bm := byMode[m]
		bm.n++
		bm.vs, bm.ws = append(bm.vs, it.Progress), append(bm.ws, ws[i])
	}
	n.Progress = round1(weighted(vs, ws))
	for _, m := range Modes {
		if bm := byMode[m]; bm != nil {
			n.ByMode = append(n.ByMode, ModeStat{Mode: m, Count: bm.n, Progress: round1(weighted(bm.vs, bm.ws))})
		}
	}
	byID := map[string]*Item{}
	for i := range p.Items {
		byID[p.Items[i].ID] = &p.Items[i]
	}
	todayS := now.Format(time.DateOnly)
	var started, ended []string
	allDone := len(p.Items) > 0
	for i := range p.Items {
		it := &p.Items[i]
		if it.StateCategory == "cancelled" {
			continue
		}
		if it.Start != nil && (n.Start == nil || *it.Start < *n.Start) {
			n.Start = it.Start
		}
		if it.End != nil && (n.End == nil || *it.End > *n.End) {
			n.End = it.End
		}
		if it.StateCategory != "done" {
			allDone = false
		}
		n.ClaimsPending += it.ClaimsPending
		n.Conflicts = append(n.Conflicts, it.Conflicts...)
		if (it.StateCategory == "in_progress" || it.StateCategory == "done") && it.Start != nil {
			started = append(started, *it.Start)
		}
		if it.StateCategory == "done" && it.End != nil {
			ended = append(ended, *it.End)
		}
		if it.Kind == TypeMilestone {
			n.Milestones++
			if it.StateCategory == "done" {
				n.MilestonesDone++
			} else if it.End != nil && (n.NextMilestone == nil || *it.End < *n.NextMilestone.Date) {
				r := ref(it)
				n.NextMilestone = &r
			}
		}
		// In ritardo: work package e milestone scaduti e non chiusi.
		if (it.Kind == TypeWorkPackage || it.Kind == TypeMilestone) && it.StateCategory != "done" && it.End != nil && *it.End < todayS {
			r := ref(it)
			if d := parse(it.End); d != nil {
				r.DaysLate = calendar.Late(*d, now)
			}
			if r.DaysLate > 0 {
				n.Late = append(n.Late, r)
			}
		}
	}
	if n.NextMilestone != nil {
		if d := parse(n.NextMilestone.Date); d != nil {
			n.NextMilestone.DaysLate = calendar.Late(*d, now)
		}
	}
	if len(started) > 0 {
		sort.Strings(started)
		n.ActualStart = &started[0]
	}
	if allDone && len(ended) > 0 {
		sort.Strings(ended)
		n.ActualEnd = &ended[len(ended)-1]
	}
	sort.SliceStable(n.Late, func(i, j int) bool { return n.Late[i].DaysLate > n.Late[j].DaysLate })
	// Dipendenze bloccate: chi doveva già partire aspetta un predecessore non chiuso; chi ha date
	// che non rispettano una dipendenza (spostato senza cascata).
	sch := planning.NewScheduler(p.tasks())
	for i := range p.Items {
		it := &p.Items[i]
		if it.StateCategory == "done" || it.StateCategory == "cancelled" {
			continue
		}
		violated := map[string]bool{}
		for _, id := range sch.Violated(it.ID) {
			violated[id] = true
		}
		for _, d := range it.Deps {
			pred, ok := byID[d.ID]
			if !ok || pred.StateCategory == "cancelled" {
				continue
			}
			switch {
			case violated[d.ID]:
				n.Blocked = append(n.Blocked, Blocked{Item: ref(it), WaitsOn: ref(pred), Reason: "violated", Kind: d.Kind})
			case pred.StateCategory != "done" && it.Start != nil && *it.Start <= todayS && d.Kind != planning.DepSS:
				n.Blocked = append(n.Blocked, Blocked{Item: ref(it), WaitsOn: ref(pred), Reason: "waiting", Kind: d.Kind})
			}
		}
	}
	n.Target, n.TargetKind = p.Project.Target, p.Project.TargetKind
	if t, e := parse(n.Target), parse(n.End); t != nil && e != nil {
		var m int
		if !e.After(*t) {
			m = calendar.Workdays(e.AddDate(0, 0, 1), *t)
		} else {
			m = -calendar.Workdays(t.AddDate(0, 0, 1), *e)
		}
		n.MarginDays = &m
	}
	// Baseline: scostamento della fine e milestone oltre la tolleranza.
	if b := p.Baseline; b != nil {
		num := b.Number
		n.Baseline, n.BaselineStart, n.BaselineEnd = &num, b.Start, b.End
		if be, e := parse(b.End), parse(n.End); be != nil && e != nil {
			d := shift(*be, *e)
			n.VarianceDays = &d
			if bs := parse(b.Start); bs != nil {
				if dur := calendar.Workdays(*bs, *be); dur > 0 {
					pct := round1(float64(d) * 100 / float64(dur))
					n.VariancePct = &pct
				}
			}
			n.EndOverTolerance = d > p.Tolerance.Days
		}
		for i := range p.Items {
			it := &p.Items[i]
			if it.Kind != TypeMilestone || it.VarianceDays == nil || it.StateCategory == "done" || it.StateCategory == "cancelled" {
				continue
			}
			if *it.VarianceDays > p.Tolerance.Days {
				r := ref(it)
				r.DaysLate = *it.VarianceDays
				n.OverTolerance = append(n.OverTolerance, r)
			}
		}
		sort.SliceStable(n.OverTolerance, func(i, j int) bool { return n.OverTolerance[i].DaysLate > n.OverTolerance[j].DaysLate })
	}
	// Semaforo.
	switch {
	case len(p.Items) == 0:
		n.Signal = "none"
	default:
		n.Signal = "green"
		lateMilestone := false
		for _, l := range n.Late {
			if l.Kind == TypeMilestone {
				lateMilestone = true
			}
		}
		if lateMilestone {
			n.Reasons = append(n.Reasons, "milestone_late")
		}
		if n.MarginDays != nil && *n.MarginDays < 0 {
			n.Reasons = append(n.Reasons, "past_target")
		}
		if len(n.OverTolerance) > 0 || n.EndOverTolerance {
			n.Reasons = append(n.Reasons, "over_tolerance")
		}
		if len(n.Reasons) > 0 {
			n.Signal = "red"
		}
		amber := []string{}
		if len(n.Late) > 0 && !lateMilestone {
			amber = append(amber, "work_package_late")
		}
		if len(n.Blocked) > 0 {
			amber = append(amber, "blocked")
		}
		if len(n.Conflicts) > 0 {
			amber = append(amber, "conflicts")
		}
		if n.Signal == "green" && len(amber) > 0 {
			n.Signal = "amber"
		}
		n.Reasons = append(n.Reasons, amber...)
	}
	return n
}

// Numbers calcola i numeri del progetto: piano, ruoli obbligatori mancanti, change request
// aperte, rischi collegati e budget delle richieste.
func (s *Service) Numbers(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Numbers, *Plan, error) {
	el, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.loadPlan(ctx, tx, el)
	if err != nil {
		return nil, nil, err
	}
	if err := s.limitPlan(ctx, tx, actor, p, nil); err != nil {
		return nil, nil, err
	}
	if p.CanEdit, err = canPlan(ctx, tx, actor, el.ID); err != nil {
		return nil, nil, err
	}
	n, err := s.numbersOf(ctx, tx, actor, p, true)
	return n, p, err
}

func (s *Service) numbersOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Plan, withBudget bool) (*Numbers, error) {
	n := computeNumbers(p, today())
	rows, err := tx.Query(ctx, `select r.key, r.name from project_roles pr
		join stakeholder_roles r on r.tenant_id = pr.tenant_id and r.id = pr.role_id
		where pr.required and not exists (select 1 from stakeholders sh where sh.element_id = $1 and sh.role_id = pr.role_id)
		order by pr.ordinal`, p.Project.ID)
	if err != nil {
		return nil, err
	}
	if n.MissingRoles, err = pgx.CollectRows(rows, pgx.RowToStructByPos[RoleRef]); err != nil {
		return nil, err
	}
	if len(n.MissingRoles) > 0 {
		n.Reasons = append(n.Reasons, "missing_roles")
		if n.Signal == "green" {
			n.Signal = "amber"
		}
	}
	if err := tx.QueryRow(ctx, `select count(*) from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = $2
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.parent_id = $1 and e.deleted_at is null and coalesce(o.category, '') not in ('done', 'cancelled')`,
		p.Project.ID, TypeChangeRequest).Scan(&n.OpenChangeRequests); err != nil {
		return nil, err
	}
	reqIDs := make([]string, len(p.Project.Requests))
	for i, r := range p.Project.Requests {
		reqIDs[i] = r.ID
	}
	rows, err = tx.Query(ctx, `select distinct on (k.id) k.id::text, s.key || '-' || k.number, k.title, coalesce(o.name, '')
		from relations rel
		join elements k on k.tenant_id = rel.tenant_id and k.deleted_at is null
		  and k.id = case when rel.source_id = any($1::uuid[]) then rel.target_id else rel.source_id end
		join element_types t on t.tenant_id = k.tenant_id and t.id = k.type_id and t.key = 'risk'
		join spaces s on s.tenant_id = k.tenant_id and s.id = k.space_id
		left join options o on o.tenant_id = k.tenant_id and o.id = k.state_id
		where (rel.source_id = any($1::uuid[]) or rel.target_id = any($1::uuid[]))
		  and coalesce(o.category, '') not in ('done', 'cancelled')
		order by k.id limit 20`, append([]string{p.Project.ID}, reqIDs...))
	if err != nil {
		return nil, err
	}
	if n.Risks, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemRef, error) {
		ir := ItemRef{Kind: "risk"}
		err := r.Scan(&ir.ID, &ir.Key, &ir.Title, &ir.State)
		return ir, err
	}); err != nil {
		return nil, err
	}
	if n.Risks, err = limitRefs(ctx, tx, s.reader(actor), n.Risks); err != nil {
		return nil, err
	}
	if withBudget && s.Budget != nil && len(reqIDs) > 0 {
		if n.Budget, err = s.Budget(ctx, tx, actor, reqIDs); err != nil {
			return nil, err
		}
	}
	if p.Baseline != nil && n.Budget != nil {
		if pct := budgetVariance(p.Baseline.budget, n.Budget); pct != nil {
			n.BudgetVariancePct = pct
			if *pct > p.Tolerance.BudgetPct {
				n.BudgetOverTolerance = true
				n.Reasons = append(n.Reasons, "budget_over_tolerance")
				n.Signal = "red"
			}
		}
	}
	return &n, nil
}

// cost è il costo di riferimento: il forecast, altrimenti l'approvato.
func cost(b *Budget) *int64 {
	if b == nil {
		return nil
	}
	if b.ForecastCents != nil {
		return b.ForecastCents
	}
	return b.ApprovedCents
}

// budgetVariance: di quanto il costo previsto supera (o resta sotto) quello della baseline, in
// percentuale; nil se uno dei due manca, la valuta è diversa o la baseline è a zero.
func budgetVariance(base, cur *Budget) *float64 {
	b, c := cost(base), cost(cur)
	if b == nil || c == nil || *b <= 0 || base.Currency != cur.Currency {
		return nil
	}
	pct := round1(float64(*c-*b) * 100 / float64(*b))
	return &pct
}
