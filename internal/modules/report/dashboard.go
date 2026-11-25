package report

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/finance"
	"regia/internal/platform/rbac"
)

// Dashboard di portafoglio (demand.md §11, §14 D12). Ogni numero ha la query del
// linguaggio di ricerca che riproduce lo stesso insieme: la lista filtrata è la prova
// del numero. Le aggregazioni girano in SQL; l'API tiene una cache breve per tenant.

// Filter: unità richiedente (nome), driver, periodo di creazione (AAAA-MM-GG, estremi inclusi).
type Filter struct {
	Unit   string `json:"unit"`
	Driver string `json:"driver"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// Key identifica il filtro nella cache.
func (f Filter) Key() string { return f.Unit + "|" + f.Driver + "|" + f.From + "|" + f.To }

type Number struct {
	Count int    `json:"count"`
	Query string `json:"query"`
}

type PhaseStat struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	OffPath  bool   `json:"off_path"`
	Count    int    `json:"count"`
	Query    string `json:"query"`
	// AvgDays: tempo medio di attraversamento (permanenze concluse); Stays quante.
	AvgDays *float64 `json:"avg_days"`
	Stays   int      `json:"stays"`
	// WaitDays: da quanto, in media, le richieste ora nella fase vi si trovano.
	WaitDays *float64 `json:"wait_days"`
}

type IndicatorStat struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	More  bool   `json:"more"` // il conteggio si è fermato al limite
	Query string `json:"query"`
}

type FinanceStat struct {
	Currency       string   `json:"currency"`
	ApprovedCents  int64    `json:"approved_cents"`
	ActualCents    int64    `json:"actual_cents"`
	CommittedCents int64    `json:"committed_cents"`
	ConsumptionPct *float64 `json:"consumption_pct"`
	BudgetQuery    string   `json:"budget_query"`
	CostQuery      string   `json:"cost_query"`
	WithBudget     int      `json:"with_budget"`
	WithCosts      int      `json:"with_costs"`
	// Dettaglio, solo con finance.read.
	CanSeeDetails bool             `json:"can_see_details"`
	ByRequest     []RequestFinance `json:"by_request,omitempty"`
	ByCategory    []CategoryCost   `json:"by_category,omitempty"`
}

type RequestFinance struct {
	Key            string   `json:"key"`
	Title          string   `json:"title"`
	ApprovedCents  int64    `json:"approved_cents"`
	ActualCents    int64    `json:"actual_cents"`
	CommittedCents int64    `json:"committed_cents"`
	ConsumptionPct *float64 `json:"consumption_pct"`
}

type CategoryCost struct {
	Category       string `json:"category"`
	Nature         string `json:"nature"`
	ActualCents    int64  `json:"actual_cents"`
	CommittedCents int64  `json:"committed_cents"`
}

type TeamCount struct {
	Team  string `json:"team"`
	Count int    `json:"count"`
	Query string `json:"query"`
}

type TeamLoadStat struct {
	Team         string    `json:"team"`
	CapacityDays float64   `json:"capacity_days"`
	LoadDays     float64   `json:"load_days"`
	PeakPct      float64   `json:"peak_pct"`
	OverWeeks    int       `json:"over_weeks"`
	Weeks        []float64 `json:"weeks"` // carico/capacità per settimana (%)
	Query        string    `json:"query"`
}

type Slice struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Query string `json:"query"`
}

type RAGStat struct {
	Green   Number    `json:"green"`
	Amber   Number    `json:"amber"`
	Red     Number    `json:"red"`
	Overdue Number    `json:"overdue"` // report attesi e non arrivati
	Items   []RAGItem `json:"items"`   // gli ultimi report, prima i rossi
}

type RAGItem struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Overall   string `json:"overall"`
	Schedule  string `json:"schedule"`
	Cost      string `json:"cost"`
	Scope     string `json:"scope"`
	Risk      string `json:"risk"`
	PeriodEnd string `json:"period_end"`
	Overdue   bool   `json:"overdue"`
}

type Dashboard struct {
	AsOf             string          `json:"as_of"`
	Filter           Filter          `json:"filter"`
	BaseQuery        string          `json:"base_query"`
	Total            Number          `json:"total"`
	Open             Number          `json:"open"`
	Phases           []PhaseStat     `json:"phases"`
	LateDesired      Number          `json:"late_desired"`
	LateDeadline     Number          `json:"late_deadline"`
	DeadlineSoon     Number          `json:"deadline_soon"` // scadenza normativa entro 90 giorni
	Mandatory        Number          `json:"mandatory"`
	Regulatory       []IndicatorStat `json:"regulatory"`
	Finance          FinanceStat     `json:"finance"`
	ReviewsLate      []TeamCount     `json:"reviews_late"`
	ReviewsLateTotal Number          `json:"reviews_late_total"`
	TeamLoad         []TeamLoadStat  `json:"team_load"`
	LoadFrom         string          `json:"load_from"`
	LoadTo           string          `json:"load_to"`
	ByDriver         []Slice         `json:"by_driver"`
	ByUnit           []Slice         `json:"by_unit"`
	RAG              RAGStat         `json:"rag"`
	Units            []Slice         `json:"units"` // unità per il filtro
}

// indicatorLimit: oltre, il conteggio di un indicatore normativo si ferma (è una query libera).
const indicatorLimit = 1000

// Portfolio calcola il cruscotto. Serve element.read; il dettaglio finanziario solo con finance.read.
func (s *Service) Portfolio(ctx context.Context, tx pgx.Tx, actor engine.Actor, f Filter) (*Dashboard, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	c, err := s.clock(ctx, tx)
	if err != nil {
		return nil, err
	}
	d := &Dashboard{AsOf: day(c.today), Filter: f}
	// --- Filtri e query di base.
	base := "tipo: " + requestType
	var unit, driver *string
	var from, to *time.Time
	if f.Unit = strings.TrimSpace(f.Unit); f.Unit != "" {
		var name string
		err := tx.QueryRow(ctx, `select name from org_units where lower(name) = lower($1) or id::text = $1 limit 1`, f.Unit).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("unit", "", "unità «%s» inesistente", f.Unit)
		}
		if err != nil {
			return nil, err
		}
		f.Unit, unit = name, &name
		base += " unita: " + quote(name)
	}
	if f.Driver != "" {
		w, ok := driverWord[f.Driver]
		if !ok {
			return nil, engine.InvalidValue("driver", "driver non valido (strategic, regulatory, operational, efficiency, risk)")
		}
		driver = &f.Driver
		base += " driver: " + w
	}
	if f.From != "" || f.To != "" {
		var a, b string
		if f.From != "" {
			t, err := parseDay("from", f.From)
			if err != nil {
				return nil, err
			}
			x := c.startOf(t)
			from, a = &x, day(t)
		}
		if f.To != "" {
			t, err := parseDay("to", f.To)
			if err != nil {
				return nil, err
			}
			x := c.startOf(t.AddDate(0, 0, 1))
			to, b = &x, day(t)
		}
		if from != nil && to != nil && !from.Before(*to) {
			return nil, engine.InvalidCode("validation.report.period_ends_before_start", "from", "il periodo finisce prima di cominciare")
		}
		base += " creata: " + a + ".." + b
	}
	d.Filter, d.BaseQuery = f, base
	q := func(extra string) string { return base + " " + extra }

	// Il perimetro: le richieste che rispettano i filtri (la stessa logica della query).
	// Il perimetro si calcola una volta sola in una tabella temporanea della transazione: le
	// ~15 query del cruscotto la rileggono invece di ripetere il join su tutte le richieste
	// (con 120.000 richieste: da 1,2 s a meno di 0,4 s, benchmark 2026-11-24).
	args := []any{unit, driver, from, to}
	if _, err := tx.Exec(ctx, `create temp table dash_req (
			id uuid, number bigint, title text, state_id uuid, created_at timestamptz, resolved_at timestamptz,
			space_key text, driver text, requesting_unit_id uuid, desired_date date, regulatory_deadline date, mandatory boolean
		) on commit drop`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into dash_req
		select e.id, e.number, e.title, e.state_id, e.created_at, e.resolved_at, sp.key,
		       d.driver, d.requesting_unit_id, d.desired_date, d.regulatory_deadline, coalesce(d.is_mandatory, false)
		from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'business_request'
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join ext_demand d on d.tenant_id = e.tenant_id and d.element_id = e.id
		where e.deleted_at is null and not e.is_draft
		  and ($1::text is null or d.requesting_unit_id in (select u.id from org_units u where lower(u.name) = lower($1)))
		  and ($2::text is null or d.driver = $2)
		  and ($3::timestamptz is null or e.created_at >= $3)
		  and ($4::timestamptz is null or e.created_at < $4)`, args...); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `create index on dash_req (id)`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `analyze dash_req`); err != nil {
		return nil, err
	}
	// Le query leggono il perimetro dalla tabella; i parametri $1-$4 restano nel testo
	// (con un'espressione sempre vera) perché ogni query li riceve in blocco.
	reqCTE := `with req as not materialized (select * from dash_req
		where ($1::text is null or true) and ($2::text is null or true) and ($3::timestamptz is null or true) and ($4::timestamptz is null or true))`
	withArgs := func(extra ...any) []any { return append(append([]any{}, args...), extra...) }

	// --- Conteggi semplici.
	var total, open, lateDesired, lateDeadline, soon, mandatory int
	if err := tx.QueryRow(ctx, reqCTE+`
		select count(*), count(*) filter (where resolved_at is null),
		       count(*) filter (where resolved_at is null and desired_date < $5),
		       count(*) filter (where resolved_at is null and regulatory_deadline < $5),
		       count(*) filter (where resolved_at is null and regulatory_deadline >= $5 and regulatory_deadline <= $6),
		       count(*) filter (where resolved_at is null and mandatory)
		from req`, withArgs(c.today, c.today.AddDate(0, 0, 90))...).Scan(&total, &open, &lateDesired, &lateDeadline, &soon, &mandatory); err != nil {
		return nil, err
	}
	d.Total = Number{total, base}
	d.Open = Number{open, q("fase: aperta")}
	d.LateDesired = Number{lateDesired, q("fase: aperta desiderata: <oggi")}
	d.LateDeadline = Number{lateDeadline, q("fase: aperta scadenza: <oggi")}
	d.DeadlineSoon = Number{soon, q("fase: aperta scadenza: oggi..+90g")}
	d.Mandatory = Number{mandatory, q("fase: aperta obbligatoria: sì")}

	// --- Fasi: quante richieste per fase, permanenza media conclusa e attesa attuale.
	if err := s.phases(ctx, tx, d, reqCTE, args, q); err != nil {
		return nil, err
	}

	// --- Indicatori normativi: query libere del tenant, contate con il motore di ricerca
	// (così il numero è per costruzione quello della lista).
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	d.Regulatory = []IndicatorStat{}
	for _, ind := range st.Regulatory {
		query := q("fase: aperta " + ind.Query)
		n, more, err := s.count(ctx, tx, actor, query, indicatorLimit)
		if err != nil {
			var qe *engine.QueryError
			if errors.As(err, &qe) {
				continue // indicatore non più valido (campo rinominato): si salta
			}
			return nil, err
		}
		d.Regulatory = append(d.Regulatory, IndicatorStat{Label: ind.Label, Count: n, More: more, Query: query})
	}

	// --- Budget e costi (aggregati).
	if err := s.finance(ctx, tx, actor, d, reqCTE, args, q); err != nil {
		return nil, err
	}

	// --- Distribuzione delle richieste aperte per driver e per unità.
	d.ByDriver = []Slice{}
	rows, err := tx.Query(ctx, reqCTE+` select coalesce(driver, ''), count(*) from req where resolved_at is null group by 1 order by 2 desc, 1`, args...)
	if err != nil {
		return nil, err
	}
	var key string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&key, &n}, func() error {
		sl := Slice{Key: key, Count: n}
		if key == "" {
			sl.Label, sl.Query = "Senza driver", q("fase: aperta driver: vuoto")
		} else {
			sl.Label, sl.Query = driverLabel[key], q("fase: aperta driver: "+driverWord[key])
		}
		d.ByDriver = append(d.ByDriver, sl)
		return nil
	}); err != nil {
		return nil, err
	}
	d.ByUnit = []Slice{}
	rows, err = tx.Query(ctx, reqCTE+` select coalesce(u.name, ''), count(*) from req
		left join org_units u on u.tenant_id = app_tenant() and u.id = req.requesting_unit_id
		where req.resolved_at is null group by 1 order by 2 desc, 1`, args...)
	if err != nil {
		return nil, err
	}
	if _, err := pgx.ForEachRow(rows, []any{&key, &n}, func() error {
		sl := Slice{Key: key, Label: key, Count: n, Query: q("fase: aperta unita: " + quote(key))}
		if key == "" {
			sl.Label, sl.Query = "Senza unità", q("fase: aperta unita: vuoto")
		}
		d.ByUnit = append(d.ByUnit, sl)
		return nil
	}); err != nil {
		return nil, err
	}
	d.Units = []Slice{}
	rows, err = tx.Query(ctx, `select id::text, name from org_units where not archived order by name`)
	if err != nil {
		return nil, err
	}
	var id, name string
	if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error {
		d.Units = append(d.Units, Slice{Key: id, Label: name})
		return nil
	}); err != nil {
		return nil, err
	}

	// --- Semafori degli ultimi status report (richieste aperte nel perimetro).
	if err := s.rag(ctx, tx, d, reqCTE, args, q, c.today); err != nil {
		return nil, err
	}

	// --- Valutazioni in ritardo per team valutatore (tutto il portafoglio).
	d.ReviewsLate = []TeamCount{}
	rows, err = tx.Query(ctx, `select rt.name, count(*) from ext_review x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null and not e.is_draft
		join review_teams rt on rt.tenant_id = x.tenant_id and rt.id = x.team_id
		where x.stage in ('submitted', 'exam') and x.due_on < $1
		group by rt.name order by 2 desc, 1`, c.today)
	if err != nil {
		return nil, err
	}
	lateTotal := 0
	if _, err := pgx.ForEachRow(rows, []any{&name, &n}, func() error {
		d.ReviewsLate = append(d.ReviewsLate, TeamCount{Team: name, Count: n,
			Query: "tipo: " + reviewType + " valutatore: " + quote(name) + " scadenza_sla: <oggi"})
		lateTotal += n
		return nil
	}); err != nil {
		return nil, err
	}
	d.ReviewsLateTotal = Number{lateTotal, "tipo: " + reviewType + " scadenza_sla: <oggi"}

	// --- Carico dei team nelle prossime otto settimane (tutto il portafoglio).
	lf, lt := c.today, c.today.AddDate(0, 0, 7*8-1)
	loads, err := s.plan.Load(ctx, tx, actor, day(lf), day(lt))
	if err != nil {
		return nil, err
	}
	d.LoadFrom, d.LoadTo = day(lf), day(lt)
	d.TeamLoad = []TeamLoadStat{}
	for _, t := range loads {
		ts := TeamLoadStat{Team: t.Name, Weeks: []float64{}, Query: "tipo: " + impactType + " team: " + quote(t.Name) + " fase: aperta"}
		for _, w := range t.Weeks {
			ts.CapacityDays += w.Capacity
			ts.LoadDays += w.Load
			pct := 0.0
			if w.Capacity > 0 {
				pct = math.Round(w.Load * 100 / w.Capacity)
			}
			ts.Weeks = append(ts.Weeks, pct)
			ts.PeakPct = math.Max(ts.PeakPct, pct)
			if w.Load > w.Capacity {
				ts.OverWeeks++
			}
		}
		ts.CapacityDays, ts.LoadDays = round1(ts.CapacityDays), round1(ts.LoadDays)
		d.TeamLoad = append(d.TeamLoad, ts)
	}
	sort.SliceStable(d.TeamLoad, func(i, j int) bool { return d.TeamLoad[i].PeakPct > d.TeamLoad[j].PeakPct })
	return d, nil
}

// count conta i risultati di una query con il motore di ricerca, fino a limit.
func (s *Service) count(ctx context.Context, tx pgx.Tx, actor engine.Actor, query string, limit int) (int, bool, error) {
	n, cursor := 0, ""
	for {
		page, err := s.eng.Search(ctx, tx, actor, query, 200, cursor)
		if err != nil {
			return 0, false, err
		}
		n += len(page.Items)
		if page.Next == "" {
			return n, false, nil
		}
		if n >= limit {
			return n, true, nil
		}
		cursor = page.Next
	}
}

func (s *Service) phases(ctx context.Context, tx pgx.Tx, d *Dashboard, reqCTE string, args []any, q func(string) string) error {
	t, err := engine.LoadType(ctx, tx, requestType)
	if err != nil {
		return err
	}
	type acc struct {
		count, stays int
		avg, wait    *float64
	}
	by := map[string]*acc{}
	get := func(name string) *acc {
		if by[name] == nil {
			by[name] = &acc{}
		}
		return by[name]
	}
	// Quante per fase e da quanto si trovano lì (ultimo cambio di fase o creazione).
	rows, err := tx.Query(ctx, reqCTE+`
		select o.name, count(*),
		       avg(extract(epoch from (now() - coalesce(
		           (select max(a.created_at) from activities a where a.element_id = req.id and a.kind = 'state'), req.created_at))) / 86400)
		         filter (where req.resolved_at is null)
		from req join options o on o.tenant_id = app_tenant() and o.id = req.state_id
		group by o.name`, args...)
	if err != nil {
		return err
	}
	var name string
	var n int
	var wait *float64
	if _, err := pgx.ForEachRow(rows, []any{&name, &n, &wait}, func() error {
		a := get(name)
		a.count = n
		if wait != nil {
			v := round1(*wait)
			a.wait = &v
		}
		return nil
	}); err != nil {
		return err
	}
	// Permanenze concluse: ogni cambio di fase chiude la permanenza nella fase di partenza,
	// iniziata con il cambio precedente (o con la creazione).
	rows, err = tx.Query(ctx, reqCTE+`,
		mv as (
			select a.element_id, a.created_at as at, a.removed->>'name' as phase,
			       lag(a.created_at) over (partition by a.element_id order by a.created_at, a.id) as prev
			from activities a join req on req.id = a.element_id
			where a.kind = 'state'
		)
		select mv.phase, count(*), avg(extract(epoch from (mv.at - coalesce(mv.prev, req.created_at))) / 86400)
		from mv join req on req.id = mv.element_id
		where mv.phase is not null
		group by mv.phase`, args...)
	if err != nil {
		return err
	}
	var avg *float64
	if _, err := pgx.ForEachRow(rows, []any{&name, &n, &avg}, func() error {
		a := get(name)
		a.stays = n
		if avg != nil {
			v := round1(*avg)
			a.avg = &v
		}
		return nil
	}); err != nil {
		return err
	}
	d.Phases = []PhaseStat{}
	for _, st := range t.States {
		a := get(st.Name)
		d.Phases = append(d.Phases, PhaseStat{Name: st.Name, Category: st.Category, OffPath: st.OffPath, Count: a.count,
			Query: q("fase: " + quote(st.Name)), AvgDays: a.avg, Stays: a.stays, WaitDays: a.wait})
	}
	return nil
}

func (s *Service) finance(ctx context.Context, tx pgx.Tx, actor engine.Actor, d *Dashboard, reqCTE string, args []any, q func(string) string) error {
	st, err := finance.GetSettings(ctx, tx)
	if err != nil {
		return err
	}
	fs := FinanceStat{Currency: st.BaseCurrency, BudgetQuery: q("budget_approvato: presente"), CostQuery: q("con_costi: presente"), CanSeeDetails: finance.CanRead(actor)}
	// Per ogni richiesta: budget approvato (versioni correnti con i trasferimenti approvati),
	// consuntivo e impegnato degli aggregati della richiesta, dei suoi impatti e dei figli.
	const perRequest = `,
		fam as (
			select id as rid, id as eid from req
			union select x.request_id, x.element_id from ext_impact x join req on req.id = x.request_id
			union select e.parent_id, e.id from elements e join req on req.id = e.parent_id where e.deleted_at is null
		),
		cost as (
			select fam.rid, coalesce(sum(r.amount_base_cents) filter (where r.kind = 'actual'), 0)::bigint as actual,
			       coalesce(sum(r.amount_base_cents) filter (where r.kind = 'committed'), 0)::bigint as committed
			from cost_rollups r join fam on fam.eid = r.element_id group by fam.rid
		),
		bl as (
			select b.element_id as rid, l.id as line_id, l.amount_cents
			from budgets b join req on req.id = b.element_id
			join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
			where b.status = 'approved' and b.kind <> 'forecast'
		),
		bud as (
			select bl.rid, sum(bl.amount_cents
				+ coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = bl.line_id and t.status = 'approved'), 0)
				- coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = bl.line_id and t.status = 'approved'), 0))::bigint as approved
			from bl group by bl.rid
		),
		per as (
			select req.id, req.space_key || '-' || req.number as key, req.title,
			       bud.approved, coalesce(cost.actual, 0) as actual, coalesce(cost.committed, 0) as committed
			from req left join bud on bud.rid = req.id left join cost on cost.rid = req.id
		)`
	if err := tx.QueryRow(ctx, reqCTE+perRequest+`
		select coalesce(sum(approved), 0)::bigint, coalesce(sum(actual), 0)::bigint, coalesce(sum(committed), 0)::bigint,
		       count(*) filter (where approved is not null),
		       count(*) filter (where id in (select rid from fam join cost_rollups r on r.element_id = fam.eid))
		from per`, args...).Scan(&fs.ApprovedCents, &fs.ActualCents, &fs.CommittedCents, &fs.WithBudget, &fs.WithCosts); err != nil {
		return err
	}
	if fs.ApprovedCents > 0 {
		v := round1(float64(fs.ActualCents) * 100 / float64(fs.ApprovedCents))
		fs.ConsumptionPct = &v
	}
	if fs.CanSeeDetails {
		rows, err := tx.Query(ctx, reqCTE+perRequest+`
			select key, title, coalesce(approved, 0), actual, committed from per
			where approved is not null or actual <> 0 or committed <> 0
			order by actual desc, coalesce(approved, 0) desc, key limit 15`, args...)
		if err != nil {
			return err
		}
		fs.ByRequest, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestFinance, error) {
			var x RequestFinance
			err := r.Scan(&x.Key, &x.Title, &x.ApprovedCents, &x.ActualCents, &x.CommittedCents)
			if x.ApprovedCents > 0 {
				v := round1(float64(x.ActualCents) * 100 / float64(x.ApprovedCents))
				x.ConsumptionPct = &v
			}
			return x, err
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, reqCTE+`,
			fam as (
				select id as rid, id as eid from req
				union select x.request_id, x.element_id from ext_impact x join req on req.id = x.request_id
				union select e.parent_id, e.id from elements e join req on req.id = e.parent_id where e.deleted_at is null
			)
			select c.name, c.nature, coalesce(sum(r.amount_base_cents) filter (where r.kind = 'actual'), 0)::bigint,
			       coalesce(sum(r.amount_base_cents) filter (where r.kind = 'committed'), 0)::bigint
			from cost_rollups r join (select distinct eid from fam) f on f.eid = r.element_id
			join cost_categories c on c.tenant_id = r.tenant_id and c.id = r.cost_category_id
			group by c.name, c.nature order by 3 desc, 1`, args...)
		if err != nil {
			return err
		}
		fs.ByCategory, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CategoryCost])
		if err != nil {
			return err
		}
		if fs.ByRequest == nil {
			fs.ByRequest = []RequestFinance{}
		}
		if fs.ByCategory == nil {
			fs.ByCategory = []CategoryCost{}
		}
	}
	d.Finance = fs
	return nil
}

func (s *Service) rag(ctx context.Context, tx pgx.Tx, d *Dashboard, reqCTE string, args []any, q func(string) string, today time.Time) error {
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return err
	}
	var green, amber, red, overdue int
	if err := tx.QueryRow(ctx, reqCTE+`
		select count(*) filter (where sc.last_overall = 'green'), count(*) filter (where sc.last_overall = 'amber'),
		       count(*) filter (where sc.last_overall = 'red'),
		       count(*) filter (where sc.next_due < $5 and o.name = any($6))
		from req join status_report_schedules sc on sc.element_id = req.id
		left join options o on o.tenant_id = app_tenant() and o.id = req.state_id
		where req.resolved_at is null`, append(append([]any{}, args...), today, st.Phases)...).Scan(&green, &amber, &red, &overdue); err != nil {
		return err
	}
	d.RAG = RAGStat{
		Green: Number{green, q("fase: aperta semaforo: verde")}, Amber: Number{amber, q("fase: aperta semaforo: giallo")},
		Red: Number{red, q("fase: aperta semaforo: rosso")}, Items: []RAGItem{},
	}
	// «In ritardo» si limita alle fasi in cui il report è atteso: la query le elenca.
	phases := make([]string, 0, len(st.Phases))
	for _, p := range st.Phases {
		phases = append(phases, quote(p))
	}
	if len(phases) > 0 {
		d.RAG.Overdue = Number{overdue, q("fase: " + strings.Join(phases, ", ") + " prossimo_report: <oggi")}
	}
	rows, err := tx.Query(ctx, reqCTE+`
		select req.space_key || '-' || req.number, req.title, r.overall, coalesce(r.schedule, 'green'), coalesce(r.cost, 'green'),
		       coalesce(r.scope, 'green'), coalesce(r.risk, 'green'), r.period_end, coalesce(sc.next_due < $5 and o.name = any($6), false)
		from req join status_report_schedules sc on sc.element_id = req.id
		join status_reports r on r.tenant_id = sc.tenant_id and r.id = sc.last_report_id
		left join options o on o.tenant_id = app_tenant() and o.id = req.state_id
		where req.resolved_at is null
		order by case r.overall when 'red' then 0 when 'amber' then 1 else 2 end, r.period_end desc limit 30`,
		append(append([]any{}, args...), today, st.Phases)...)
	if err != nil {
		return err
	}
	var it RAGItem
	var pe time.Time
	_, err = pgx.ForEachRow(rows, []any{&it.Key, &it.Title, &it.Overall, &it.Schedule, &it.Cost, &it.Scope, &it.Risk, &pe, &it.Overdue}, func() error {
		x := it
		x.PeriodEnd = day(pe)
		d.RAG.Items = append(d.RAG.Items, x)
		return nil
	})
	return err
}

// ---------------------------------------------------------------------------
// Pannelli scelti da ciascuno (tabella dashboards, una riga per persona).

// Panels sono i pannelli del cruscotto, nell'ordine di default.
var Panels = []struct{ Key, Label string }{
	{"fasi", "Richieste per fase e tempi di attraversamento"},
	{"ritardi", "Richieste in ritardo"},
	{"normativi", "Obblighi normativi"},
	{"budget", "Budget e costi"},
	{"semafori", "Semafori degli status report"},
	{"valutazioni", "Valutazioni in ritardo per team"},
	{"carico", "Carico dei team"},
	{"driver", "Distribuzione per driver"},
	{"unita", "Distribuzione per unità"},
}

type PanelPref struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Visible bool   `json:"visible"`
}

const layoutName = "portafoglio"

// Layout restituisce i pannelli della persona: quelli salvati, poi i nuovi in coda.
func (s *Service) Layout(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]PanelPref, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	var raw []byte
	err := tx.QueryRow(ctx, `select layout from dashboards where owner_id = $1 and name = $2`, actor.MemberID, layoutName).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var saved []PanelPref
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &saved)
	}
	labels := map[string]string{}
	for _, p := range Panels {
		labels[p.Key] = p.Label
	}
	out := []PanelPref{}
	seen := map[string]bool{}
	for _, p := range saved {
		if l, ok := labels[p.Key]; ok && !seen[p.Key] {
			out = append(out, PanelPref{Key: p.Key, Label: l, Visible: p.Visible})
			seen[p.Key] = true
		}
	}
	for _, p := range Panels {
		if !seen[p.Key] {
			out = append(out, PanelPref{Key: p.Key, Label: p.Label, Visible: true})
		}
	}
	return out, nil
}

// SaveLayout salva scelta e ordine dei pannelli della persona.
func (s *Service) SaveLayout(ctx context.Context, tx pgx.Tx, actor engine.Actor, in []PanelPref) ([]PanelPref, error) {
	if !actor.Has(rbac.ElementRead) || actor.MemberID == "" {
		return nil, engine.ErrForbidden
	}
	known := map[string]bool{}
	for _, p := range Panels {
		known[p.Key] = true
	}
	clean := []map[string]any{}
	seen := map[string]bool{}
	for _, p := range in {
		if !known[p.Key] {
			return nil, engine.NotFound("panels", "", "pannello «%s» inesistente", p.Key)
		}
		if seen[p.Key] {
			continue
		}
		seen[p.Key] = true
		clean = append(clean, map[string]any{"key": p.Key, "visible": p.Visible})
	}
	raw, _ := json.Marshal(clean)
	if _, err := tx.Exec(ctx, `insert into dashboards (tenant_id, name, owner_id, layout) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, owner_id, name) where owner_id is not null do update set layout = excluded.layout, updated_at = now()`,
		layoutName, actor.MemberID, raw); err != nil {
		return nil, err
	}
	return s.Layout(ctx, tx, actor)
}
