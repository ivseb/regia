package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Status report del progetto (replica/progetti.md §3 «Avanzamento e numeri del progetto», passo 5).
//
// Il report è lo stesso della richiesta (status_reports, bozza → inviato → presa visione, confronto,
// export), ma la fotografia viene dai numeri del progetto, quelli che la Panoramica mostra: una sola
// funzione li calcola (projects.Numbers). Il PM parte da semafori e testi proposti dai numeri, li
// corregge e aggiunge il commento; all'invio lo sponsor (stakeholder del progetto) riceve l'avviso.
//
// Visibilità: la fotografia si scrive con gli occhi dell'autore; chi legge un report vede i titoli
// delle sole voci che può leggere e gli importi solo se vede il budget del progetto.

// ProjectData sono i numeri del progetto fotografati nel report.
type ProjectData struct {
	Signal           string   `json:"signal"`
	Reasons          []string `json:"reasons"`
	Progress         float64  `json:"progress"`
	WorkPackages     int      `json:"work_packages"`
	WorkPackagesDone int      `json:"work_packages_done"`
	Milestones       int      `json:"milestones"`
	MilestonesDone   int      `json:"milestones_done"`
	// Baseline in vigore e scostamenti.
	Baseline          *int     `json:"baseline"`
	BaselineEnd       *string  `json:"baseline_end"`
	VarianceDays      *int     `json:"variance_days"`
	VariancePct       *float64 `json:"variance_pct"`
	BudgetVariancePct *float64 `json:"budget_variance_pct"`
	ToleranceDays     int      `json:"tolerance_days"`
	ToleranceBudget   float64  `json:"tolerance_budget_pct"`
	Target            *string  `json:"target"`
	TargetKind        string   `json:"target_kind"`
	MarginDays        *int     `json:"margin_days"`
	NextMilestone     *ProjRef `json:"next_milestone"`
	// Late: work package e milestone in ritardo; OverTolerance: milestone oltre la tolleranza.
	Late          []ProjRef `json:"late"`
	OverTolerance []ProjRef `json:"over_tolerance"`
	ScopeAdded    []ProjRef `json:"scope_added"`
	ScopeRemoved  []ProjRef `json:"scope_removed"`
	Blocked       int       `json:"blocked"`
	ClaimsPending int       `json:"claims_pending"`
	Conflicts     int       `json:"conflicts"`
	OpenChanges   int       `json:"open_change_requests"`
	Risks         []ProjRef `json:"risks"`
	MissingRoles  []string  `json:"missing_roles"`
}

// ProjRef è una voce del progetto citata nel report.
type ProjRef struct {
	ID         string  `json:"id"`
	Key        string  `json:"key"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	Date       *string `json:"date"`
	DaysLate   int     `json:"days_late"`
	Restricted bool    `json:"restricted,omitempty"`
}

// suggestProject propone i semafori dai numeri del progetto: tempi da ritardi, tolleranze e
// avanzamento, costi dal budget del progetto, perimetro dagli scostamenti dalla baseline, rischi da
// rischi aperti, conflitti e ruoli mancanti. Come per la richiesta, i motivi dicono il perché.
func suggestProject(sn *Snapshot, asOf time.Time) Suggested {
	g := Suggested{Reasons: map[string][]string{}}
	add := func(dim, why string) { g.Reasons[dim] = append(g.Reasons[dim], why) }
	pd := sn.Project
	sc := sn.Schedule
	gap := 0.0
	if sc.PlannedPct != nil && sc.ActualPct != nil {
		gap = *sc.PlannedPct - *sc.ActualPct
	}
	lateMs, lateWp := 0, 0
	for _, l := range pd.Late {
		if l.Kind == "milestone" {
			lateMs++
		} else {
			lateWp++
		}
	}

	// Tempi.
	g.Schedule = "green"
	switch {
	case lateMs > 0:
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("%d %s in ritardo", lateMs, plural(lateMs, "milestone", "milestone")))
	case pd.MarginDays != nil && *pd.MarginDays < 0:
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("la fine prevista supera la scadenza di %d giorni lavorativi", -*pd.MarginDays))
	case len(pd.OverTolerance) > 0 || (pd.VarianceDays != nil && *pd.VarianceDays > pd.ToleranceDays):
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("oltre la tolleranza di %d giorni lavorativi sulla baseline", pd.ToleranceDays))
	case gap > 25:
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("avanzamento %.0f%% contro %.0f%% atteso", *sc.ActualPct, *sc.PlannedPct))
	}
	if g.Schedule != "red" {
		switch {
		case lateWp > 0:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("%d work package in ritardo", lateWp))
		case pd.Blocked > 0:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("%d %s bloccate da una dipendenza", pd.Blocked, plural(pd.Blocked, "voce", "voci")))
		case gap > 10:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("avanzamento %.0f%% contro %.0f%% atteso", *sc.ActualPct, *sc.PlannedPct))
		case pd.VarianceDays != nil && *pd.VarianceDays > 0:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("fine prevista %d giorni lavorativi dopo la baseline, dentro la tolleranza", *pd.VarianceDays))
		case sc.Tasks > sc.TasksScheduled:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("%d work package ancora senza date", sc.Tasks-sc.TasksScheduled))
		}
	}

	// Costi: prima la tolleranza sulla baseline, poi le regole della richiesta.
	g.Cost = "green"
	if pd.BudgetVariancePct != nil && *pd.BudgetVariancePct > pd.ToleranceBudget {
		g.Cost = "red"
		add("cost", fmt.Sprintf("costo previsto %+.1f%% sulla baseline, oltre la tolleranza del %.0f%%", *pd.BudgetVariancePct, pd.ToleranceBudget))
	} else {
		g.Cost = suggestCost(sn, add)
	}

	// Perimetro: voci aggiunte o tolte rispetto alla baseline, change request in corso.
	g.Scope = "green"
	changed := len(pd.ScopeAdded) + len(pd.ScopeRemoved)
	switch {
	case changed > 0:
		g.Scope = "amber"
		add("scope", fmt.Sprintf("%d %s aggiunte e %d tolte rispetto alla baseline", len(pd.ScopeAdded), plural(len(pd.ScopeAdded), "voce", "voci"), len(pd.ScopeRemoved)))
	case pd.OpenChanges > 0:
		g.Scope = "amber"
		add("scope", "una change request è in corso")
	case pd.Baseline != nil:
		add("scope", fmt.Sprintf("perimetro uguale alla baseline %d", *pd.Baseline))
	default:
		add("scope", "nessuna baseline: perimetro da confermare")
	}

	// Rischi e problemi.
	g.Risk = "green"
	soon := pd.TargetKind == "regulatory" && pd.Target != nil && func() bool {
		t, err := time.Parse(time.DateOnly, *pd.Target)
		return err == nil && !t.After(asOf.AddDate(0, 0, 30))
	}()
	switch {
	case soon && g.Schedule != "green":
		g.Risk = "red"
		add("risk", "scadenza normativa vicina con i tempi non in linea")
	case len(pd.Risks) > 0:
		g.Risk = "amber"
		add("risk", fmt.Sprintf("%d %s aperti", len(pd.Risks), plural(len(pd.Risks), "rischio", "rischi")))
	case pd.Conflicts > 0:
		g.Risk = "amber"
		add("risk", fmt.Sprintf("%d %s con altri progetti sulla stessa applicazione", pd.Conflicts, plural(pd.Conflicts, "conflitto", "conflitti")))
	case len(pd.MissingRoles) > 0:
		g.Risk = "amber"
		add("risk", "mancano ruoli obbligatori: "+strings.Join(pd.MissingRoles, ", "))
	}
	g.Overall = worst(g.Schedule, g.Cost, g.Risk, g.Scope)
	return g
}

// prefillProjectText scrive la bozza delle sezioni dai numeri del progetto: il PM la corregge.
func (s *Service) prefillProjectText(ctx context.Context, tx pgx.Tx, el *engine.Element, sn *Snapshot, from, to time.Time, freq string) (Sections, error) {
	var sec Sections
	pd := sn.Project
	var b strings.Builder
	fmt.Fprintf(&b, "Fase «%s». Avanzamento %.0f%%", sn.Phase, pd.Progress)
	if sn.Schedule.PlannedPct != nil {
		fmt.Fprintf(&b, " (atteso %.0f%%)", *sn.Schedule.PlannedPct)
	}
	fmt.Fprintf(&b, ", %d su %d work package chiusi.", pd.WorkPackagesDone, pd.WorkPackages)
	if pd.Baseline != nil && pd.VarianceDays != nil {
		fmt.Fprintf(&b, " Fine prevista %+d giorni lavorativi rispetto alla baseline %d.", *pd.VarianceDays, *pd.Baseline)
	}
	if pd.NextMilestone != nil && pd.NextMilestone.Date != nil {
		if d, err := time.Parse(time.DateOnly, *pd.NextMilestone.Date); err == nil {
			fmt.Fprintf(&b, " Prossima milestone: %s %s, il %s.", pd.NextMilestone.Key, pd.NextMilestone.Title, it(d))
		}
	}
	if sn.Cost.ConsumptionPct != nil {
		fmt.Fprintf(&b, " Consumo del budget %.0f%%.", *sn.Cost.ConsumptionPct)
	}
	sec.Summary = b.String()

	// Fatte nel periodo: work package e milestone chiusi, cambi di fase del progetto.
	rows, err := tx.Query(ctx, `
		with v as (
			select element_id id from ext_work_package where project_id = $1
			union all select element_id from ext_milestone where project_id = $1)
		select '- ' || sp.key || '-' || e.number || ' ' || e.title || ': ' || coalesce(o.name, '')
		from v join elements e on e.tenant_id = app_tenant() and e.id = v.id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where o.category = 'done' and e.resolved_at >= $2 and e.resolved_at < $3
		union all
		select '- passaggio a «' || (a.added->>'name') || '» il ' || to_char(a.created_at, 'DD/MM/YYYY')
		from activities a where a.element_id = $1 and a.kind = 'state' and a.created_at >= $2 and a.created_at < $3`,
		el.ID, from, to.AddDate(0, 0, 1))
	if err != nil {
		return sec, err
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return sec, err
	}
	sec.Done = strings.Join(lines, "\n")

	// Prossime: work package e milestone aperti che iniziano o finiscono nel prossimo periodo.
	next := addPeriod(to, freq)
	rows, err = tx.Query(ctx, `
		with v as (
			select element_id id from ext_work_package where project_id = $1
			union all select element_id from ext_milestone where project_id = $1)
		select '- ' || sp.key || '-' || e.number || ' ' || e.title
		       || case when e.start_at > $2 and e.start_at is distinct from e.due_at then ': inizio il ' || to_char(e.start_at, 'DD/MM/YYYY')
		               else ': fine prevista il ' || to_char(e.due_at, 'DD/MM/YYYY') end
		from v join elements e on e.tenant_id = app_tenant() and e.id = v.id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where coalesce(o.category, '') not in ('done', 'cancelled')
		  and ((e.start_at > $2 and e.start_at <= $3) or (e.due_at > $2 and e.due_at <= $3))
		order by coalesce(e.start_at, e.due_at), e.number`, el.ID, to, next)
	if err != nil {
		return sec, err
	}
	if lines, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return sec, err
	}
	sec.Next = strings.Join(lines, "\n")

	// Rischi e problemi: ciò che i numeri segnalano.
	var risks []string
	for _, l := range pd.Late {
		risks = append(risks, fmt.Sprintf("- %s %s: in ritardo di %d giorni lavorativi", l.Key, l.Title, l.DaysLate))
	}
	for _, o := range pd.OverTolerance {
		risks = append(risks, fmt.Sprintf("- %s %s: %+d giorni lavorativi sulla baseline, oltre la tolleranza", o.Key, o.Title, o.DaysLate))
	}
	for _, a := range pd.ScopeAdded {
		risks = append(risks, fmt.Sprintf("- %s %s: aggiunta dopo la baseline", a.Key, a.Title))
	}
	for _, r := range pd.ScopeRemoved {
		risks = append(risks, fmt.Sprintf("- %s %s: tolta rispetto alla baseline", r.Key, r.Title))
	}
	for _, r := range pd.Risks {
		risks = append(risks, fmt.Sprintf("- rischio %s %s", r.Key, r.Title))
	}
	sort.Strings(risks)
	if pd.Blocked > 0 {
		risks = append(risks, fmt.Sprintf("- %d %s bloccate da una dipendenza", pd.Blocked, plural(pd.Blocked, "voce", "voci")))
	}
	if pd.ClaimsPending > 0 {
		risks = append(risks, fmt.Sprintf("- %d SAL da accettare", pd.ClaimsPending))
	}
	if pd.Conflicts > 0 {
		risks = append(risks, fmt.Sprintf("- %d %s con altri progetti", pd.Conflicts, plural(pd.Conflicts, "conflitto", "conflitti")))
	}
	sec.Risks = strings.Join(risks, "\n")
	if pd.OpenChanges > 0 {
		sec.Decisions = fmt.Sprintf("- %d change request in corso da decidere", pd.OpenChanges)
	}
	return sec, nil
}

// redactProject applica al report del progetto i limiti di chi legge: voci non leggibili senza
// titolo, importi tolti a chi non vede il budget del progetto.
func (s *Service) redactProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *Report) error {
	pd := r.Data.Project
	if pd == nil {
		return nil
	}
	seen := map[string]bool{}
	can := func(id string) (bool, error) {
		if id == "" {
			return true, nil
		}
		if ok, done := seen[id]; done {
			return ok, nil
		}
		ok, err := s.eng.CanRead(ctx, tx, actor, id)
		seen[id] = ok
		return ok, err
	}
	fix := func(list []ProjRef) error {
		for i := range list {
			ok, err := can(list[i].ID)
			if err != nil {
				return err
			}
			if !ok {
				list[i].Title, list[i].Restricted = "", true
			}
		}
		return nil
	}
	for _, l := range [][]ProjRef{pd.Late, pd.OverTolerance, pd.ScopeAdded, pd.ScopeRemoved, pd.Risks} {
		if err := fix(l); err != nil {
			return err
		}
	}
	if pd.NextMilestone != nil {
		ok, err := can(pd.NextMilestone.ID)
		if err != nil {
			return err
		}
		if !ok {
			pd.NextMilestone.Title, pd.NextMilestone.Restricted = "", true
		}
	}
	for i := range r.Data.Schedule.Late {
		t := &r.Data.Schedule.Late[i]
		ok, err := can(t.ID)
		if err != nil {
			return err
		}
		if !ok {
			t.Application, t.Team = "", nil
		}
	}
	if s.ProjectBudgetVisible != nil {
		ok, err := s.ProjectBudgetVisible(ctx, tx, actor, r.ElementID)
		if err != nil {
			return err
		}
		if !ok {
			r.Data.Cost = CostData{Currency: r.Data.Cost.Currency, Hidden: true}
			pd.BudgetVariancePct = nil
		}
	}
	return nil
}
