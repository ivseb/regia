package finance

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Totals sono i numeri di "Budget e costi" di una richiesta: li vede chiunque veda la
// richiesta, perché sono aggregati (budget per categoria, cost_rollups per mese e
// categoria), mai voci singole, persone o tariffe.
type Totals struct {
	Currency string `json:"currency"`
	// Preventivo: le stime della domanda (nella loro valuta).
	EstimateLowCents  *int64 `json:"estimate_low_cents"`
	EstimateHighCents *int64 `json:"estimate_high_cents"`
	EstimateCurrency  string `json:"estimate_currency"`
	// Budget approvato (versioni correnti, con i trasferimenti), in lavorazione, consuntivo, impegnato.
	ApprovedCents  *int64 `json:"approved_cents"`
	PendingCents   *int64 `json:"pending_cents"` // versione inviata o in bozza
	ActualCents    int64  `json:"actual_cents"`
	CommittedCents int64  `json:"committed_cents"`
	CapexCents     int64  `json:"capex_actual_cents"`
	// Previsione a finire: la previsione pubblicata, altrimenti l'EAC dell'earned value.
	ForecastCents  *int64  `json:"forecast_cents"`
	ForecastSource *string `json:"forecast_source"` // forecast | eac
	// Consumo: consuntivo / budget approvato (in %); soglie di allerta del tenant.
	ConsumptionPct  *float64     `json:"consumption_pct"`
	AlertThresholds []int        `json:"alert_thresholds"`
	ByCategory      []CatTotal   `json:"by_category"`
	ByMonth         []MonthTotal `json:"by_month"`
	Budgets         []BudgetRef  `json:"budgets"`
	EVEnabled       bool         `json:"ev_enabled"`
	// CanSeeDetails: l'attore ha finance.read (voci, tariffe, pagina /finanza).
	CanSeeDetails bool `json:"can_see_details"`
	CanEdit       bool `json:"can_edit"`
}

type CatTotal struct {
	CategoryID     string `json:"cost_category_id"`
	Category       string `json:"category"`
	Nature         string `json:"nature"`
	BudgetCents    int64  `json:"budget_cents"`
	ActualCents    int64  `json:"actual_cents"`
	CommittedCents int64  `json:"committed_cents"`
}

type MonthTotal struct {
	Month          string `json:"month"` // AAAA-MM
	ActualCents    int64  `json:"actual_cents"`
	CommittedCents int64  `json:"committed_cents"`
}

// BudgetRef è la versione corrente di un esercizio (senza righe).
type BudgetRef struct {
	ID         string `json:"id"`
	FiscalYear string `json:"fiscal_year"`
	Kind       string `json:"kind"`
	Version    int    `json:"version"`
	Status     string `json:"status"`
	TotalCents int64  `json:"total_cents"`
}

func (s *Service) RequestTotals(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Totals, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	t := &Totals{Currency: st.BaseCurrency, AlertThresholds: st.AlertThresholds, CanSeeDetails: CanRead(actor), CanEdit: actor.Has("finance.write")}
	err = tx.QueryRow(ctx, "select estimate_low_cents, estimate_high_cents, currency from ext_demand where element_id = $1", el.ID).
		Scan(&t.EstimateLowCents, &t.EstimateHighCents, &t.EstimateCurrency)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if t.EstimateCurrency == "" {
		t.EstimateCurrency = st.BaseCurrency
	}
	// Versioni non sostituite: approvate (correnti), in lavorazione, previsione.
	rows, err := tx.Query(ctx, `
		select b.id, f.name, b.kind, b.version, b.status,
		       coalesce((select sum(l.amount_cents) from budget_lines l where l.budget_id = b.id), 0)
		         + coalesce((select sum(t.amount_cents) from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id and l.id = t.to_line_id
		                     where l.budget_id = b.id and t.status = 'approved'), 0)
		         - coalesce((select sum(t.amount_cents) from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id and l.id = t.from_line_id
		                     where l.budget_id = b.id and t.status = 'approved'), 0)
		from budgets b join fiscal_years f on f.tenant_id = b.tenant_id and f.id = b.fiscal_year_id
		where b.element_id = $1 and b.status <> 'superseded'
		order by f.start_date, b.kind = 'forecast', b.version`, el.ID)
	if err != nil {
		return nil, err
	}
	t.Budgets, err = pgx.CollectRows(rows, pgx.RowToStructByPos[BudgetRef])
	if err != nil {
		return nil, err
	}
	var approved, pending, forecast int64
	var hasApproved, hasPending, hasForecast bool
	for _, b := range t.Budgets {
		switch {
		case b.Kind == "forecast" && b.Status == "approved":
			forecast, hasForecast = forecast+b.TotalCents, true
		case b.Kind == "forecast":
		case b.Status == "approved":
			approved, hasApproved = approved+b.TotalCents, true
		default:
			pending, hasPending = pending+b.TotalCents, true
		}
	}
	if hasApproved {
		t.ApprovedCents = &approved
	}
	if hasPending {
		t.PendingCents = &pending
	}
	if hasForecast {
		src := "forecast"
		t.ForecastCents, t.ForecastSource = &forecast, &src
	}
	// Consuntivo e impegnato dagli aggregati (elemento, impatti e figli).
	rows, err = tx.Query(ctx, `
		with cats as (
			select r.cost_category_id, sum(r.amount_base_cents) filter (where r.kind = 'actual') as actual,
			       sum(r.amount_base_cents) filter (where r.kind = 'committed') as committed, 0::bigint as budget
			from cost_rollups r where r.element_id in (select id from finance_family($1))
			group by 1
			union all
			select l.cost_category_id, 0, 0, sum(l.amount_cents)
			from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
			where b.element_id = $1 and b.status = 'approved' and b.kind <> 'forecast'
			group by 1
		)
		select c.id, c.name, c.nature, coalesce(sum(x.budget), 0)::bigint, coalesce(sum(x.actual), 0)::bigint, coalesce(sum(x.committed), 0)::bigint
		from cats x join cost_categories c on c.tenant_id = app_tenant() and c.id = x.cost_category_id
		group by c.id, c.name, c.nature
		having coalesce(sum(x.budget), 0) <> 0 or coalesce(sum(x.actual), 0) <> 0 or coalesce(sum(x.committed), 0) <> 0
		order by c.nature, c.name`, el.ID)
	if err != nil {
		return nil, err
	}
	t.ByCategory, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatTotal])
	if err != nil {
		return nil, err
	}
	for _, c := range t.ByCategory {
		t.ActualCents += c.ActualCents
		t.CommittedCents += c.CommittedCents
	}
	if err := tx.QueryRow(ctx, `select coalesce(sum(capitalizable_base_cents), 0) from cost_rollups
		where element_id in (select id from finance_family($1)) and kind = 'actual'`, el.ID).Scan(&t.CapexCents); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select period, coalesce(sum(amount_base_cents) filter (where kind = 'actual'), 0)::bigint,
		coalesce(sum(amount_base_cents) filter (where kind = 'committed'), 0)::bigint
		from cost_rollups where element_id in (select id from finance_family($1)) group by period order by period`, el.ID)
	if err != nil {
		return nil, err
	}
	t.ByMonth, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (MonthTotal, error) {
		var m MonthTotal
		var p time.Time
		err := r.Scan(&p, &m.ActualCents, &m.CommittedCents)
		m.Month = p.Format("2006-01")
		return m, err
	})
	if err != nil {
		return nil, err
	}
	if t.ApprovedCents != nil && *t.ApprovedCents > 0 {
		v := float64(t.ActualCents) * 100 / float64(*t.ApprovedCents)
		t.ConsumptionPct = &v
	}
	// Earned value: se attivo, l'EAC dell'ultima fotografia fa da previsione (se manca quella pubblicata).
	var evOn bool
	var bac, ev, ac *int64
	err = tx.QueryRow(ctx, `select p.ev_enabled, s.bac_cents, s.ev_cents, s.ac_cents from ext_project p
		left join lateral (select * from ev_snapshots e where e.element_id = p.element_id order by e.period_end desc limit 1) s on true
		where p.element_id = $1`, el.ID).Scan(&evOn, &bac, &ev, &ac)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	t.EVEnabled = evOn
	if evOn && t.ForecastCents == nil && bac != nil && ev != nil && ac != nil && *ev > 0 && *ac > 0 {
		eac := int64(float64(*bac) * float64(*ac) / float64(*ev)) // BAC / CPI
		src := "eac"
		t.ForecastCents, t.ForecastSource = &eac, &src
	}
	if t.ByCategory == nil {
		t.ByCategory = []CatTotal{}
	}
	if t.ByMonth == nil {
		t.ByMonth = []MonthTotal{}
	}
	if t.Budgets == nil {
		t.Budgets = []BudgetRef{}
	}
	return t, nil
}
