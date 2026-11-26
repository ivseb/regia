// Package planning è la pianificazione nel tempo: piano di una richiesta (impatti con
// date e dipendenze), spostamenti a cascata, piano automatico, roadmap di portafoglio
// e carico dei team. Le date stanno in elements.start_at / due_at (giorni interi);
// i conti usano i giorni lavorativi (internal/platform/calendar).
package planning

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

const (
	requestType = "business_request"
	impactType  = "impact"
	// defaultDurationDays è la durata proposta per un impatto senza stima.
	defaultDurationDays = 5
	// defaultCapacity: giorni/persona a settimana di un team senza capacità indicata.
	defaultCapacity = 5.0
)

var analystRoles = []string{"demand_manager", "pmo", "project_manager", "architect", "technical_analyst"}

type Service struct {
	eng *engine.Service
}

func New(eng *engine.Service) *Service { return &Service{eng: eng} }

// Task è un impatto nel piano.
type Task struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Application   string   `json:"application"`
	TeamID        *string  `json:"team_id"`
	Team          *string  `json:"team"`
	State         string   `json:"state"`
	StateCategory string   `json:"state_category"`
	Start         *string  `json:"start"`
	End           *string  `json:"end"`
	EffortDays    *float64 `json:"estimate_effort_days"`
	DependsOn     []string `json:"depends_on"` // id degli impatti da cui dipende
	Capacity      float64  `json:"team_capacity"`
	// Deps: dipendenze tipizzate (fs, ss, ff con ritardo) delle voci di un progetto; se nil
	// valgono DependsOn come fine→inizio. Milestone: voce di durata zero.
	Deps      []Dep `json:"deps,omitempty"`
	Milestone bool  `json:"-"`
}

// RequestWindow è la richiesta con la sua finestra e le scadenze.
type RequestWindow struct {
	ID                 string  `json:"id"`
	Key                string  `json:"key"`
	Title              string  `json:"title"`
	State              string  `json:"state"`
	StateCategory      string  `json:"state_category"`
	Start              *string `json:"start"`
	End                *string `json:"end"`
	DesiredDate        *string `json:"desired_date"`
	RegulatoryDeadline *string `json:"regulatory_deadline"`
	Mandatory          bool    `json:"is_mandatory"`
	Driver             *string `json:"driver"`
	Impacts            int     `json:"impacts"`
	Scheduled          int     `json:"scheduled"`
	PhaseIndex         int     `json:"phase_index"`
	PhaseCount         int     `json:"phase_count"`
}

// Plan è il piano di una richiesta.
type Plan struct {
	Request RequestWindow `json:"request"`
	Tasks   []Task        `json:"tasks"`
	CanEdit bool          `json:"can_edit"`
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.DateOnly)
	return &s
}

func (s *Service) request(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != requestType {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

func (s *Service) canPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID string) (bool, error) {
	if actor.Has("element.write") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, requestID, actor.MemberID, analystRoles)
}

const windowSelect = `
	select r.id, s.key || '-' || r.number, r.title, coalesce(o.name, ''), coalesce(o.category, ''),
	       r.start_at, r.due_at, d.desired_date, d.regulatory_deadline, coalesce(d.is_mandatory, false), d.driver,
	       (select count(*) from ext_impact x join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.element_id
	          and ie.deleted_at is null where x.request_id = r.id),
	       (select count(*) from ext_impact x join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.element_id
	          and ie.deleted_at is null and ie.start_at is not null where x.request_id = r.id),
	       coalesce(o.ordinal, 0)
	from elements r
	join spaces s on s.tenant_id = r.tenant_id and s.id = r.space_id
	join element_types t on t.tenant_id = r.tenant_id and t.id = r.type_id and t.key = 'business_request'
	left join options o on o.tenant_id = r.tenant_id and o.id = r.state_id
	left join ext_demand d on d.tenant_id = r.tenant_id and d.element_id = r.id
	where r.deleted_at is null and not r.is_draft`

func scanWindows(rows pgx.Rows) ([]RequestWindow, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestWindow, error) {
		var w RequestWindow
		var start, end, desired, deadline *time.Time
		err := r.Scan(&w.ID, &w.Key, &w.Title, &w.State, &w.StateCategory, &start, &end, &desired, &deadline,
			&w.Mandatory, &w.Driver, &w.Impacts, &w.Scheduled, &w.PhaseIndex)
		w.Start, w.End, w.DesiredDate, w.RegulatoryDeadline = dateStr(start), dateStr(end), dateStr(desired), dateStr(deadline)
		return w, err
	})
}

func (s *Service) tasks(ctx context.Context, tx pgx.Tx, requestID string) ([]Task, error) {
	rows, err := tx.Query(ctx, `
		select e.id, sp.key || '-' || e.number, ap.title, x.team_id, t.name, coalesce(o.name, ''), coalesce(o.category, ''),
		       e.start_at, e.due_at, x.estimate_effort_days::float8,
		       coalesce((select array_agg(rel.target_id::text order by rel.target_id) from relations rel
		                 join relation_types rt on rt.tenant_id = rel.tenant_id and rt.id = rel.type_id and rt.key = 'depends_on'
		                 where rel.source_id = e.id), '{}'),
		       coalesce(t.weekly_capacity_hours::float8 / 8, 0)
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		where x.request_id = $1
		order by e.start_at nulls last, e.number`, requestID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Task, error) {
		var t Task
		var start, end *time.Time
		err := r.Scan(&t.ID, &t.Key, &t.Application, &t.TeamID, &t.Team, &t.State, &t.StateCategory, &start, &end,
			&t.EffortDays, &t.DependsOn, &t.Capacity)
		t.Start, t.End = dateStr(start), dateStr(end)
		if t.Capacity <= 0 {
			t.Capacity = defaultCapacity
		}
		return t, err
	})
}

// GetPlan restituisce il piano della richiesta.
func (s *Service) GetPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Plan, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, windowSelect+` and r.id = $1`, el.ID)
	if err != nil {
		return nil, err
	}
	ws, err := scanWindows(rows)
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, engine.ErrNotFound
	}
	p := &Plan{Request: ws[0]}
	if p.Tasks, err = s.tasks(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	if p.CanEdit, err = s.canPlan(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	return p, nil
}

// Change è uno spostamento applicato (anche a cascata).
type Change struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Start string `json:"start"`
	End   string `json:"end"`
	Cause string `json:"cause"` // moved | cascade | auto
}

// Schedule sposta o ridimensiona un impatto. Con cascade gli impatti che ne dipendono
// (direttamente o no) slittano in avanti quanto serve, mantenendo la loro durata in
// giorni lavorativi. Restituisce tutte le modifiche.
func (s *Service) Schedule(ctx context.Context, tx pgx.Tx, actor engine.Actor, impactID, startS, endS string, cascade bool) ([]Change, error) {
	start, err1 := calendar.Parse(startS)
	end, err2 := calendar.Parse(endS)
	if err1 != nil || err2 != nil {
		return nil, engine.DateInvalid("start", "date non valide, formato AAAA-MM-GG")
	}
	if end.Before(start) {
		return nil, engine.InvalidCode("validation.planning.end_before_start", "end", "la fine è prima dell'inizio")
	}
	if end.Sub(start) > 5*365*24*time.Hour {
		return nil, engine.InvalidCode("validation.planning.duration_too_long", "end", "durata eccessiva (massimo 5 anni)")
	}
	var requestID string
	err := tx.QueryRow(ctx, `select x.request_id from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		where x.element_id::text = $1 and e.deleted_at is null`, impactID).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.request(ctx, tx, actor, requestID); err != nil {
		return nil, err
	}
	ok, err := s.canPlan(ctx, tx, actor, requestID)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Il referente del team pianifica il suo perimetro.
		if ok, err = engine.HasRole(ctx, tx, impactID, actor.MemberID, []string{"team_lead", "technical_analyst"}); err != nil || !ok {
			return nil, firstErr(err, engine.ErrForbidden)
		}
	}
	tasks, err := s.tasks(ctx, tx, requestID)
	if err != nil {
		return nil, err
	}
	g := newGraph(tasks)
	changes := []Change{}
	g.set(impactID, calendar.Day(start), calendar.Day(end))
	changes = append(changes, g.change(impactID, "moved"))
	if cascade {
		changes = append(changes, g.cascade(impactID)...)
	}
	if err := s.save(ctx, tx, actor, requestID, g, changes); err != nil {
		return nil, err
	}
	return changes, nil
}

// AutoPlan dà le date agli impatti senza date (o a tutti con replan) rispettando le
// dipendenze. La durata è la stima divisa per la capacità giornaliera del team.
func (s *Service) AutoPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, replan bool, from string) ([]Change, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canPlan(ctx, tx, actor, el.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	base := calendar.NextWorkday(time.Now())
	if el.StartAt != nil && el.StartAt.After(base) {
		base = calendar.NextWorkday(*el.StartAt)
	}
	if from != "" {
		t, err := calendar.Parse(from)
		if err != nil {
			return nil, engine.DateInvalid("from", "data non valida, formato AAAA-MM-GG")
		}
		base = calendar.NextWorkday(t)
	}
	tasks, err := s.tasks(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	g := newGraph(tasks)
	order, err := g.topo()
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	for _, id := range order {
		n := g.nodes[id]
		if n.t.StateCategory == "cancelled" || (n.start != nil && !replan) {
			continue
		}
		start := base
		for _, p := range n.t.DependsOn {
			if pn, ok := g.nodes[p]; ok && pn.end != nil && pn.t.StateCategory != "cancelled" {
				if a := calendar.AfterEnd(*pn.end); a.After(start) {
					start = a
				}
			}
		}
		days := defaultDurationDays
		if n.t.EffortDays != nil && *n.t.EffortDays > 0 {
			days = int(math.Ceil(*n.t.EffortDays / (n.t.Capacity / 5)))
		}
		g.set(id, start, calendar.EndAfter(start, days))
		changes = append(changes, g.change(id, "auto"))
	}
	if err := s.save(ctx, tx, actor, el.ID, g, changes); err != nil {
		return nil, err
	}
	return changes, nil
}

// ShiftRequest sposta l'intera richiesta (finestra e impatti) di delta giorni di calendario;
// senza impatti pianificati imposta la finestra indicata.
func (s *Service) ShiftRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, startS, endS string) ([]Change, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canPlan(ctx, tx, actor, el.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	start, err1 := calendar.Parse(startS)
	end, err2 := calendar.Parse(endS)
	if err1 != nil || err2 != nil || end.Before(start) {
		return nil, engine.DateInvalid("start", "finestra non valida (AAAA-MM-GG, fine dopo l'inizio)")
	}
	tasks, err := s.tasks(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	g := newGraph(tasks)
	changes := []Change{}
	if el.StartAt != nil && g.scheduled() > 0 {
		delta := calendar.Day(start).Sub(calendar.Day(*el.StartAt))
		for _, id := range g.ids {
			n := g.nodes[id]
			if n.start == nil {
				continue
			}
			dur := calendar.Workdays(*n.start, *n.end)
			ns := calendar.NextWorkday(n.start.Add(delta))
			g.set(id, ns, calendar.EndAfter(ns, dur))
			changes = append(changes, g.change(id, "moved"))
		}
		if err := s.save(ctx, tx, actor, el.ID, g, changes); err != nil {
			return nil, err
		}
		return changes, nil
	}
	if _, err := tx.Exec(ctx, `update elements set start_at = $2, due_at = $3, updated_by = $4 where id = $1`,
		el.ID, calendar.Day(start), calendar.Day(end), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "schedule", "window",
		map[string]any{"start": startS, "end": endS}, nil); err != nil {
		return nil, err
	}
	return changes, audit.Append(ctx, tx, audit.Event{Action: "plan.window", EntityType: requestType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"start": startS, "end": endS}})
}

// save scrive le date cambiate, aggiorna la finestra della richiesta (inviluppo degli
// impatti pianificati) e registra storia e audit.
func (s *Service) save(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID string, g *graph, changes []Change) error {
	for _, c := range changes {
		n := g.nodes[c.ID]
		if _, err := tx.Exec(ctx, `update elements set start_at = $2, due_at = $3, version = version + 1, updated_by = $4 where id = $1`,
			c.ID, *n.start, *n.end, nullable(actor.MemberID)); err != nil {
			return err
		}
		if err := engine.AddActivity(ctx, tx, c.ID, actor.MemberID, "schedule", c.Cause,
			map[string]any{"start": c.Start, "end": c.End}, nil); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update elements r set start_at = w.s, due_at = w.e from (
			select min(e.start_at) s, max(e.due_at) e from ext_impact x
			join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null and e.start_at is not null
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled') w
		where r.id = $1 and w.s is not null`, requestID); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	return audit.Append(ctx, tx, audit.Event{Action: "plan.update", EntityType: requestType, EntityID: requestID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"changes": changes}})
}

// Roadmap elenca le richieste aperte (o tutte) con finestra, fasi e scadenze.
func (s *Service) Roadmap(ctx context.Context, tx pgx.Tx, actor engine.Actor, includeClosed bool) ([]RequestWindow, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	q := windowSelect
	if !includeClosed {
		q += ` and coalesce(o.category, '') not in ('done', 'cancelled')`
	}
	rows, err := tx.Query(ctx, q+` order by coalesce(r.start_at, d.desired_date::timestamptz, d.regulatory_deadline::timestamptz, r.created_at), r.number limit 1000`)
	if err != nil {
		return nil, err
	}
	list, err := scanWindows(rows)
	if err != nil {
		return nil, err
	}
	t, err := engine.LoadType(ctx, tx, requestType)
	if err != nil {
		return list, nil
	}
	main := []string{}
	for _, st := range t.States {
		if !st.OffPath {
			main = append(main, st.Name)
		}
	}
	for i := range list {
		list[i].PhaseCount = len(main)
		list[i].PhaseIndex = slices.Index(main, list[i].State)
	}
	return list, nil
}

// TeamLoad è il carico settimanale di un team.
type TeamLoad struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Capacity float64    `json:"capacity_days_week"`
	Weeks    []WeekLoad `json:"weeks"`
}

type WeekLoad struct {
	Week     string  `json:"week"` // lunedì AAAA-MM-GG
	Load     float64 `json:"load_days"`
	Capacity float64 `json:"capacity_days"`
}

func monday(t time.Time) time.Time {
	t = calendar.Day(t)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}

// Load distribuisce la stima di ogni impatto pianificato sui suoi giorni lavorativi e
// la somma per team e settimana, accanto alla capacità della settimana (festività escluse).
func (s *Service) Load(ctx context.Context, tx pgx.Tx, actor engine.Actor, fromS, toS string) ([]TeamLoad, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	from, err1 := calendar.Parse(fromS)
	to, err2 := calendar.Parse(toS)
	if err1 != nil || err2 != nil || to.Before(from) || to.Sub(from) > 3*366*24*time.Hour {
		return nil, engine.DateInvalid("from", "intervallo non valido (AAAA-MM-GG, al massimo 3 anni)")
	}
	from, to = monday(from), monday(to).AddDate(0, 0, 6)
	rows, err := tx.Query(ctx, `select id, name, coalesce(weekly_capacity_hours::float8 / 8, 0) from teams where not archived order by name`)
	if err != nil {
		return nil, err
	}
	teams, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamLoad, error) {
		var t TeamLoad
		err := r.Scan(&t.ID, &t.Name, &t.Capacity)
		if t.Capacity <= 0 {
			t.Capacity = defaultCapacity
		}
		return t, err
	})
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	weeks := []time.Time{}
	for w := from; !w.After(to); w = w.AddDate(0, 0, 7) {
		weeks = append(weeks, w)
	}
	for i := range teams {
		idx[teams[i].ID] = i
		teams[i].Weeks = make([]WeekLoad, len(weeks))
		for j, w := range weeks {
			teams[i].Weeks[j] = WeekLoad{Week: w.Format(time.DateOnly),
				Capacity: math.Round(teams[i].Capacity*float64(calendar.Workdays(w, w.AddDate(0, 0, 4)))/5*10) / 10}
		}
	}
	if err := applyAbsences(ctx, tx, teams, idx, from, to); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `
		select x.team_id, e.start_at, e.due_at, x.estimate_effort_days::float8
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.team_id is not null and e.start_at is not null and e.due_at is not null and x.estimate_effort_days > 0
		  and coalesce(o.category, '') not in ('done', 'cancelled') and e.start_at <= $2 and e.due_at >= $1`, from, to)
	if err != nil {
		return nil, err
	}
	var teamID string
	var st, en time.Time
	var effort float64
	_, err = pgx.ForEachRow(rows, []any{&teamID, &st, &en, &effort}, func() error {
		i, ok := idx[teamID]
		if !ok {
			return nil
		}
		n := calendar.Workdays(st, en)
		if n == 0 {
			return nil
		}
		per := effort / float64(n)
		for d := calendar.Day(st); !d.After(calendar.Day(en)); d = d.AddDate(0, 0, 1) {
			if d.Before(from) || d.After(to) || !calendar.Workday(d) {
				continue
			}
			j := int(monday(d).Sub(from).Hours() / 24 / 7)
			teams[i].Weeks[j].Load += per
		}
		return nil
	})
	for i := range teams {
		for j := range teams[i].Weeks {
			teams[i].Weeks[j].Load = math.Round(teams[i].Weeks[j].Load*10) / 10
		}
	}
	return teams, err
}

func firstErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
