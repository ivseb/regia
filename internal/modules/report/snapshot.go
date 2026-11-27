package report

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/calendar"
)

// Snapshot è la fotografia dei dati con cui si precompila uno status report. Resta nel
// report (status_reports.data): il confronto tra due report usa i numeri di allora.
type Snapshot struct {
	AsOf           string          `json:"as_of"`
	Phase          string          `json:"phase"`
	Schedule       ScheduleData    `json:"schedule"`
	Cost           CostData        `json:"cost"`
	ReviewsLate    []LateReview    `json:"reviews_late"`
	ImpactsBlocked []BlockedImpact `json:"impacts_blocked"`
	Suggested      Suggested       `json:"suggested"`
	// Project: i numeri del progetto (solo nei report del progetto, passo 5).
	Project *ProjectData `json:"project,omitempty"`
}

// ScheduleData: avanzamento dal Gantt. L'atteso pesa ogni impatto per la sua stima e per
// la parte di finestra già trascorsa; il reale conta il lavoro degli impatti rilasciati.
type ScheduleData struct {
	Start          *string   `json:"start"`
	End            *string   `json:"end"`
	DesiredDate    *string   `json:"desired_date"`
	Deadline       *string   `json:"regulatory_deadline"`
	Tasks          int       `json:"tasks"`
	TasksDone      int       `json:"tasks_done"`
	TasksScheduled int       `json:"tasks_scheduled"`
	TasksLate      int       `json:"tasks_late"`
	PlannedPct     *float64  `json:"planned_pct"`
	ActualPct      *float64  `json:"actual_pct"`
	Late           []TaskRef `json:"late"`
}

type TaskRef struct {
	// ID: la voce, per togliere il titolo a chi non la legge (report del progetto).
	ID          string  `json:"id,omitempty"`
	Key         string  `json:"key"`
	Application string  `json:"application"`
	Team        *string `json:"team"`
	End         *string `json:"end"`
}

// CostData: solo aggregati (gli stessi dei totali della richiesta) ed earned value.
type CostData struct {
	Currency       string   `json:"currency"`
	ApprovedCents  *int64   `json:"approved_cents"`
	ActualCents    int64    `json:"actual_cents"`
	CommittedCents int64    `json:"committed_cents"`
	ForecastCents  *int64   `json:"forecast_cents"`
	ConsumptionPct *float64 `json:"consumption_pct"`
	CPI            *float64 `json:"cpi"`
	SPI            *float64 `json:"spi"`
	EVAsOf         *string  `json:"ev_as_of"`
	// Hidden: chi legge non vede il budget del progetto (gli importi sono tolti).
	Hidden bool `json:"hidden,omitempty"`
}

type LateReview struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Team      string `json:"team"`
	Due       string `json:"due"`
	LateDays  int    `json:"late_days"` // giorni lavorativi
	Mandatory bool   `json:"mandatory"`
}

type BlockedImpact struct {
	Key         string `json:"key"`
	Application string `json:"application"`
	Reason      string `json:"reason"`
}

// Suggested sono i semafori proposti dai dati, con i motivi; il PM li corregge.
type Suggested struct {
	Overall  string              `json:"overall"`
	Schedule string              `json:"schedule"`
	Cost     string              `json:"cost"`
	Scope    string              `json:"scope"`
	Risk     string              `json:"risk"`
	Reasons  map[string][]string `json:"reasons"`
}

var ragRank = map[string]int{"green": 0, "amber": 1, "red": 2}

func worst(v ...string) string {
	out := "green"
	for _, x := range v {
		if ragRank[x] > ragRank[out] {
			out = x
		}
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// snapshot raccoglie i dati della richiesta alla data asOf.
func (s *Service) snapshot(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, asOf time.Time) (*Snapshot, error) {
	if el.TypeKey == projectType {
		if s.ProjectSnapshot == nil {
			return nil, engine.ErrNotFound
		}
		sn, err := s.ProjectSnapshot(ctx, tx, actor, el, asOf)
		if err != nil {
			return nil, err
		}
		sn.AsOf, sn.Phase = day(asOf), el.State
		if sn.ReviewsLate == nil {
			sn.ReviewsLate = []LateReview{}
		}
		if sn.ImpactsBlocked == nil {
			sn.ImpactsBlocked = []BlockedImpact{}
		}
		if sn.Schedule.Late == nil {
			sn.Schedule.Late = []TaskRef{}
		}
		sn.Suggested = suggestProject(sn, asOf)
		return sn, nil
	}
	sn := &Snapshot{AsOf: day(asOf), Phase: el.State, ReviewsLate: []LateReview{}, ImpactsBlocked: []BlockedImpact{}}
	// --- Tempi: il piano della richiesta (Gantt).
	plan, err := s.plan.GetPlan(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	rq := plan.Request
	sn.Schedule = ScheduleData{Start: rq.Start, End: rq.End, DesiredDate: rq.DesiredDate, Deadline: rq.RegulatoryDeadline, Late: []TaskRef{}}
	var plannedW, actualW, totalW float64
	for _, t := range plan.Tasks {
		if t.StateCategory == "cancelled" {
			continue
		}
		sn.Schedule.Tasks++
		done := t.StateCategory == "done"
		if done {
			sn.Schedule.TasksDone++
		}
		var st, en time.Time
		dated := t.Start != nil && t.End != nil
		if dated {
			sn.Schedule.TasksScheduled++
			st, _ = calendar.Parse(*t.Start)
			en, _ = calendar.Parse(*t.End)
		}
		w := 1.0
		if t.EffortDays != nil && *t.EffortDays > 0 {
			w = *t.EffortDays
		} else if dated {
			w = math.Max(float64(calendar.Workdays(st, en)), 1)
		}
		totalW += w
		if done {
			actualW += w
		}
		if dated {
			switch {
			case asOf.After(en):
				plannedW += w
				if !done {
					sn.Schedule.TasksLate++
					sn.Schedule.Late = append(sn.Schedule.Late, TaskRef{Key: t.Key, Application: t.Application, Team: t.Team, End: t.End})
				}
			case !asOf.Before(st):
				all := math.Max(float64(calendar.Workdays(st, en)), 1)
				plannedW += w * math.Min(float64(calendar.Workdays(st, asOf))/all, 1)
			}
		}
	}
	if totalW > 0 {
		p, a := round1(plannedW*100/totalW), round1(actualW*100/totalW)
		sn.Schedule.PlannedPct, sn.Schedule.ActualPct = &p, &a
	}

	// --- Costi: i totali aggregati della richiesta ed earned value.
	tot, err := s.fin.RequestTotals(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	sn.Cost = CostData{Currency: tot.Currency, ApprovedCents: tot.ApprovedCents, ActualCents: tot.ActualCents,
		CommittedCents: tot.CommittedCents, ForecastCents: tot.ForecastCents, ConsumptionPct: tot.ConsumptionPct}
	if sn.Cost.ConsumptionPct != nil {
		v := round1(*sn.Cost.ConsumptionPct)
		sn.Cost.ConsumptionPct = &v
	}
	var cpi, spi *float64
	var evAt *time.Time
	err = tx.QueryRow(ctx, `select s.cpi::float8, s.spi::float8, s.period_end from ext_project p
		join ev_snapshots s on s.tenant_id = p.tenant_id and s.element_id = p.element_id
		where p.element_id = $1 and p.ev_enabled and s.period_end <= $2 order by s.period_end desc limit 1`, el.ID, asOf).Scan(&cpi, &spi, &evAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if cpi != nil {
		v := math.Round(*cpi*100) / 100
		sn.Cost.CPI = &v
	}
	if spi != nil {
		v := math.Round(*spi*100) / 100
		sn.Cost.SPI = &v
	}
	sn.Cost.EVAsOf = dayPtr(evAt)

	// --- Valutazioni in ritardo (della richiesta e dei suoi impatti).
	rows, err := tx.Query(ctx, `select coalesce(sp.key || '-' || e.number, ''), e.title, rt.name, x.due_on, x.mandatory
		from ext_review x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join review_teams rt on rt.tenant_id = x.tenant_id and rt.id = x.team_id
		where x.request_id = $1 and x.stage in ('submitted', 'exam') and x.due_on < $2
		order by x.due_on, e.number`, el.ID, asOf)
	if err != nil {
		return nil, err
	}
	var lr LateReview
	var due time.Time
	if _, err := pgx.ForEachRow(rows, []any{&lr.Key, &lr.Title, &lr.Team, &due, &lr.Mandatory}, func() error {
		r := lr
		r.Due = day(due)
		r.LateDays = calendar.Late(due, asOf)
		sn.ReviewsLate = append(sn.ReviewsLate, r)
		return nil
	}); err != nil {
		return nil, err
	}

	// --- Impatti bloccati: attendono un impatto in ritardo, hanno una valutazione
	// obbligatoria in ritardo o un esito negativo.
	rows, err = tx.Query(ctx, `
		with imp as (
			select e.id, coalesce(sp.key || '-' || e.number, '') as key, ap.title as app
			from ext_impact x
			join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
			join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where x.request_id = $1 and coalesce(o.category, '') not in ('done', 'cancelled')
		)
		select imp.key, imp.app, 'attende ' || coalesce(ps.key || '-' || p.number, '') || ', in ritardo dal ' || to_char(p.due_at, 'DD/MM/YYYY')
		from imp
		join relations rel on rel.source_id = imp.id
		join relation_types rt on rt.tenant_id = rel.tenant_id and rt.id = rel.type_id and rt.key = 'depends_on'
		join elements p on p.tenant_id = rel.tenant_id and p.id = rel.target_id and p.deleted_at is null
		join spaces ps on ps.tenant_id = p.tenant_id and ps.id = p.space_id
		left join options po on po.tenant_id = p.tenant_id and po.id = p.state_id
		where coalesce(po.category, '') not in ('done', 'cancelled') and p.due_at < $2
		union all
		select imp.key, imp.app, 'valutazione ' || rt.name || ' in ritardo (scadeva il ' || to_char(x.due_on, 'DD/MM/YYYY') || ')'
		from imp join ext_review x on x.impact_id = imp.id
		join review_teams rt on rt.tenant_id = x.tenant_id and rt.id = x.team_id
		where x.mandatory and x.stage in ('submitted', 'exam') and x.due_on < $2
		union all
		select imp.key, imp.app, 'esito negativo della valutazione ' || rt.name
		from imp join ext_review x on x.impact_id = imp.id
		join review_teams rt on rt.tenant_id = x.tenant_id and rt.id = x.team_id
		where x.stage = 'concluded' and x.outcome_polarity = 'negative' and x.superseded_by is null
		order by 1, 3`, el.ID, asOf)
	if err != nil {
		return nil, err
	}
	var bi BlockedImpact
	if _, err := pgx.ForEachRow(rows, []any{&bi.Key, &bi.Application, &bi.Reason}, func() error {
		sn.ImpactsBlocked = append(sn.ImpactsBlocked, bi)
		return nil
	}); err != nil {
		return nil, err
	}

	sn.Suggested = suggest(sn, asOf)
	return sn, nil
}

// suggest propone i semafori dai dati. Le soglie sono volutamente semplici e dette nei
// motivi: il PM vede perché e corregge.
func suggest(sn *Snapshot, asOf time.Time) Suggested {
	g := Suggested{Reasons: map[string][]string{}}
	add := func(dim, why string) { g.Reasons[dim] = append(g.Reasons[dim], why) }
	sc := sn.Schedule
	parse := func(p *string) *time.Time {
		if p == nil {
			return nil
		}
		t, err := calendar.Parse(*p)
		if err != nil {
			return nil
		}
		return &t
	}
	end, deadline, desired := parse(sc.End), parse(sc.Deadline), parse(sc.DesiredDate)

	// Tempi.
	g.Schedule = "green"
	gap := 0.0
	if sc.PlannedPct != nil && sc.ActualPct != nil {
		gap = *sc.PlannedPct - *sc.ActualPct
	}
	switch {
	case end != nil && deadline != nil && end.After(*deadline):
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("la fine pianificata (%s) supera la scadenza normativa (%s)", it(*end), it(*deadline)))
	case gap > 25:
		g.Schedule = "red"
		add("schedule", fmt.Sprintf("avanzamento %.0f%% contro %.0f%% atteso", *sc.ActualPct, *sc.PlannedPct))
	}
	if g.Schedule != "red" {
		switch {
		case sc.TasksLate > 0:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("%d %s oltre la fine pianificata", sc.TasksLate, plural(sc.TasksLate, "impatto", "impatti")))
		case gap > 10:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("avanzamento %.0f%% contro %.0f%% atteso", *sc.ActualPct, *sc.PlannedPct))
		case end != nil && desired != nil && end.After(*desired):
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("la fine pianificata (%s) supera la data desiderata (%s)", it(*end), it(*desired)))
		case sc.Tasks > sc.TasksScheduled:
			g.Schedule = "amber"
			add("schedule", fmt.Sprintf("%d %s ancora senza date", sc.Tasks-sc.TasksScheduled, plural(sc.Tasks-sc.TasksScheduled, "impatto", "impatti")))
		}
	}

	g.Cost = suggestCost(sn, add)

	// Rischi e problemi.
	g.Risk = "green"
	mandatoryLate := 0
	for _, r := range sn.ReviewsLate {
		if r.Mandatory {
			mandatoryLate++
		}
	}
	negative := 0
	for _, b := range sn.ImpactsBlocked {
		if strings.HasPrefix(b.Reason, "esito negativo") {
			negative++
		}
	}
	soon := deadline != nil && !deadline.After(asOf.AddDate(0, 0, 30))
	switch {
	case mandatoryLate > 0:
		g.Risk = "red"
		add("risk", fmt.Sprintf("%d %s obbligatorie in ritardo", mandatoryLate, plural(mandatoryLate, "valutazione", "valutazioni")))
	case negative > 0:
		g.Risk = "red"
		add("risk", fmt.Sprintf("%d %s con esito negativo", negative, plural(negative, "impatto", "impatti")))
	case soon && g.Schedule != "green":
		g.Risk = "red"
		add("risk", fmt.Sprintf("scadenza normativa vicina (%s) con i tempi non in linea", it(*deadline)))
	case len(sn.ReviewsLate) > 0:
		g.Risk = "amber"
		add("risk", fmt.Sprintf("%d %s in ritardo", len(sn.ReviewsLate), plural(len(sn.ReviewsLate), "valutazione", "valutazioni")))
	case len(sn.ImpactsBlocked) > 0:
		g.Risk = "amber"
		add("risk", fmt.Sprintf("%d %s bloccati", len(sn.ImpactsBlocked), plural(len(sn.ImpactsBlocked), "impatto", "impatti")))
	}
	if len(sn.ImpactsBlocked) > 0 && g.Risk == "red" && mandatoryLate == 0 && negative == 0 {
		add("risk", fmt.Sprintf("%d %s bloccati", len(sn.ImpactsBlocked), plural(len(sn.ImpactsBlocked), "impatto", "impatti")))
	}

	// Perimetro: non si deduce dai dati; resta da confermare.
	g.Scope = "green"
	add("scope", "nessun dato sul perimetro: da confermare")

	g.Overall = worst(g.Schedule, g.Cost, g.Risk, g.Scope)
	return g
}

// suggestCost propone il semaforo dei costi dagli aggregati e dall'earned value (richiesta e progetto).
func suggestCost(sn *Snapshot, add func(dim, why string)) string {
	c := sn.Cost
	sc := sn.Schedule
	cost := "green"
	switch {
	case c.CPI != nil && *c.CPI < 0.9:
		cost = "red"
		add("cost", fmt.Sprintf("CPI %.2f: si spende più del valore prodotto", *c.CPI))
	case c.ConsumptionPct != nil && *c.ConsumptionPct > 100:
		cost = "red"
		add("cost", fmt.Sprintf("consumo del budget al %.0f%%", *c.ConsumptionPct))
	case c.ApprovedCents != nil && c.ForecastCents != nil && float64(*c.ForecastCents) > float64(*c.ApprovedCents)*1.1:
		cost = "red"
		add("cost", "la previsione a finire supera il budget di oltre il 10%")
	case c.CPI != nil && *c.CPI < 0.97:
		cost = "amber"
		add("cost", fmt.Sprintf("CPI %.2f", *c.CPI))
	case c.ApprovedCents == nil && c.ActualCents > 0:
		cost = "amber"
		add("cost", "costi registrati senza un budget approvato")
	case c.ApprovedCents != nil && c.ForecastCents != nil && *c.ForecastCents > *c.ApprovedCents:
		cost = "amber"
		add("cost", "la previsione a finire supera il budget")
	case c.ConsumptionPct != nil && sc.ActualPct != nil && *c.ConsumptionPct > 80 && *c.ConsumptionPct > *sc.ActualPct+15:
		cost = "amber"
		add("cost", fmt.Sprintf("consumo del budget al %.0f%% con il %.0f%% del lavoro completato", *c.ConsumptionPct, *sc.ActualPct))
	}
	return cost
}

func it(t time.Time) string { return t.Format("02/01/2006") }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// prefillText scrive la bozza delle sezioni dai dati: il PM la corregge.
func (s *Service) prefillText(ctx context.Context, tx pgx.Tx, el *engine.Element, sn *Snapshot, from, to time.Time, freq string) (Sections, error) {
	if sn.Project != nil {
		return s.prefillProjectText(ctx, tx, el, sn, from, to, freq)
	}
	var sec Sections
	var b strings.Builder
	fmt.Fprintf(&b, "Fase «%s».", sn.Phase)
	if sn.Schedule.ActualPct != nil {
		fmt.Fprintf(&b, " Lavoro completato %.0f%% (atteso %.0f%%).", *sn.Schedule.ActualPct, *sn.Schedule.PlannedPct)
	}
	if sn.Cost.ConsumptionPct != nil {
		fmt.Fprintf(&b, " Consumo del budget %.0f%%.", *sn.Cost.ConsumptionPct)
	}
	sec.Summary = b.String()

	// Fatte nel periodo: impatti rilasciati e cambi di fase della richiesta.
	b.Reset()
	rows, err := tx.Query(ctx, `
		select '- ' || coalesce(sp.key || '-' || e.number, '') || ' ' || ap.title || ': ' || coalesce(o.name, '')
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.request_id = $1 and o.category = 'done' and e.resolved_at >= $2 and e.resolved_at < $3
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

	// Prossime: impatti aperti che iniziano o finiscono nel prossimo periodo.
	next := addPeriod(to, freq)
	rows, err = tx.Query(ctx, `
		select '- ' || coalesce(sp.key || '-' || e.number, '') || ' ' || ap.title
		       || case when e.start_at > $2 then ': inizio il ' || to_char(e.start_at, 'DD/MM/YYYY')
		               else ': fine prevista il ' || to_char(e.due_at, 'DD/MM/YYYY') end
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.request_id = $1 and coalesce(o.category, '') not in ('done', 'cancelled')
		  and ((e.start_at > $2 and e.start_at <= $3) or (e.due_at > $2 and e.due_at <= $3))
		order by coalesce(e.start_at, e.due_at), e.number`, el.ID, to, next)
	if err != nil {
		return sec, err
	}
	if lines, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return sec, err
	}
	sec.Next = strings.Join(lines, "\n")

	// Rischi e problemi: ciò che i dati segnalano.
	var risks []string
	for _, t := range sn.Schedule.Late {
		end := ""
		if t.End != nil {
			if d, err := calendar.Parse(*t.End); err == nil {
				end = " (fine prevista " + it(d) + ")"
			}
		}
		risks = append(risks, "- "+t.Key+" "+t.Application+": in ritardo"+end)
	}
	for _, r := range sn.ReviewsLate {
		risks = append(risks, fmt.Sprintf("- valutazione %s (%s) in ritardo di %d giorni lavorativi", r.Key, r.Team, r.LateDays))
	}
	for _, b := range sn.ImpactsBlocked {
		risks = append(risks, "- "+b.Key+" "+b.Application+": "+b.Reason)
	}
	sort.Strings(risks)
	sec.Risks = strings.Join(risks, "\n")
	return sec, nil
}
