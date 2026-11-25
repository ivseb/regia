package finance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Stanziamenti a cascata (budget.md §2): periodi, nodi dell'albero, registro dei movimenti
// immutabile e numeri di ogni nodo. L'importo di un nodo non si scrive mai: è la somma dei
// suoi movimenti. La regola «la somma dei figli non supera il padre» è applicata qui, con il
// periodo bloccato (serializzazione per albero), e ripetuta dal database come rete di
// sicurezza (trigger a fine transazione).

// Tipi di movimento.
const (
	MoveInitial  = "initial"  // plafond della radice
	MoveAllocate = "allocate" // ripartizione a un figlio
	MoveTransfer = "transfer" // trasferimento tra nodi
	MoveReturn   = "return"   // restituzione al padre
	MoveCarry    = "carry"    // riporto al periodo successivo
	MoveLapse    = "lapse"    // decadenza
	MoveAdjust   = "adjust"   // rettifica
)

// ---------------------------------------------------------------------
// Impostazioni del tenant (budget.md §7)
// ---------------------------------------------------------------------

type BudgetSettings struct {
	PeriodGranularity      string `json:"period_granularity"` // year | semester | quarter | month
	EndPolicy              string `json:"end_policy"`         // return | carry | lapse
	TransferThresholdCents *int64 `json:"transfer_threshold_cents"`
	TransferBodyKind       string `json:"transfer_body_kind"`
	FreeAllocation         bool   `json:"free_allocation"`
	WarnDays               int    `json:"warn_days"`
}

func GetBudgetSettings(ctx context.Context, tx pgx.Tx) (BudgetSettings, error) {
	st := BudgetSettings{PeriodGranularity: "year", EndPolicy: "return", TransferBodyKind: "investment_committee", FreeAllocation: true, WarnDays: 30}
	err := tx.QueryRow(ctx, `select period_granularity, end_policy, transfer_threshold_cents, transfer_body_kind, free_allocation, warn_days
		from finance_settings`).Scan(&st.PeriodGranularity, &st.EndPolicy, &st.TransferThresholdCents, &st.TransferBodyKind, &st.FreeAllocation, &st.WarnDays)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return st, err
}

type BudgetSettingsInput struct {
	PeriodGranularity      *string `json:"period_granularity"`
	EndPolicy              *string `json:"end_policy"`
	TransferThresholdCents *int64  `json:"transfer_threshold_cents"`
	ClearThreshold         bool    `json:"clear_threshold"`
	TransferBodyKind       *string `json:"transfer_body_kind"`
	FreeAllocation         *bool   `json:"free_allocation"`
	WarnDays               *int    `json:"warn_days"`
}

var (
	granularities = map[string]bool{"year": true, "semester": true, "quarter": true, "month": true}
	endPolicies   = map[string]bool{"return": true, "carry": true, "lapse": true}
	bodyKinds     = map[string]bool{"investment_committee": true, "steering": true, "architecture_board": true, "cab": true, "other": true}
)

func (s *Service) UpdateBudgetSettings(ctx context.Context, tx pgx.Tx, a engine.Actor, in BudgetSettingsInput) (BudgetSettings, error) {
	if err := requireWrite(a); err != nil {
		return BudgetSettings{}, err
	}
	st, err := GetBudgetSettings(ctx, tx)
	if err != nil {
		return st, err
	}
	if in.PeriodGranularity != nil {
		if !granularities[*in.PeriodGranularity] {
			return st, engine.InvalidValue("period_granularity", "valore non ammesso (year, semester, quarter, month)")
		}
		st.PeriodGranularity = *in.PeriodGranularity
	}
	if in.EndPolicy != nil {
		if !endPolicies[*in.EndPolicy] {
			return st, engine.InvalidValue("end_policy", "valore non ammesso (return, carry, lapse)")
		}
		st.EndPolicy = *in.EndPolicy
	}
	if in.TransferThresholdCents != nil {
		if *in.TransferThresholdCents <= 0 {
			return st, engine.InvalidCode("validation.finance.threshold_not_positive", "transfer_threshold_cents", "la soglia deve essere positiva (per toglierla: clear_threshold)")
		}
		st.TransferThresholdCents = in.TransferThresholdCents
	}
	if in.ClearThreshold {
		st.TransferThresholdCents = nil
	}
	if in.TransferBodyKind != nil {
		if !bodyKinds[*in.TransferBodyKind] {
			return st, engine.InvalidValue("transfer_body_kind", "tipo di organo non ammesso")
		}
		st.TransferBodyKind = *in.TransferBodyKind
	}
	if in.FreeAllocation != nil {
		st.FreeAllocation = *in.FreeAllocation
	}
	if in.WarnDays != nil {
		if *in.WarnDays < 0 || *in.WarnDays > 365 {
			return st, engine.OutOfRange("warn_days", 0, 365, "da 0 a 365 giorni")
		}
		st.WarnDays = *in.WarnDays
	}
	if _, err := tx.Exec(ctx, `insert into finance_settings (tenant_id, period_granularity, end_policy, transfer_threshold_cents, transfer_body_kind, free_allocation, warn_days, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, now())
		on conflict (tenant_id) do update set period_granularity = excluded.period_granularity, end_policy = excluded.end_policy,
			transfer_threshold_cents = excluded.transfer_threshold_cents, transfer_body_kind = excluded.transfer_body_kind,
			free_allocation = excluded.free_allocation, warn_days = excluded.warn_days, updated_by = excluded.updated_by, updated_at = now()`,
		st.PeriodGranularity, st.EndPolicy, st.TransferThresholdCents, st.TransferBodyKind, st.FreeAllocation, st.WarnDays, nullable(a.MemberID)); err != nil {
		return st, err
	}
	return st, s.aud(ctx, tx, a, "budget.settings", "finance_settings", "", map[string]any{"period_granularity": st.PeriodGranularity,
		"end_policy": st.EndPolicy, "transfer_threshold_cents": st.TransferThresholdCents, "transfer_body_kind": st.TransferBodyKind,
		"free_allocation": st.FreeAllocation, "warn_days": st.WarnDays})
}

// ---------------------------------------------------------------------
// Periodi
// ---------------------------------------------------------------------

type Period struct {
	ID           string     `json:"id"`
	FiscalYearID string     `json:"fiscal_year_id"`
	FiscalYear   string     `json:"fiscal_year"`
	Name         string     `json:"name"`
	Ordinal      int        `json:"ordinal"`
	Start        string     `json:"start_date"`
	End          string     `json:"end_date"`
	Status       string     `json:"status"` // planning | open | closing | closed
	ClosedAt     *time.Time `json:"closed_at"`
	DaysToClose  *int       `json:"days_to_close"` // null se il periodo è chiuso o già finito
	RootID       *string    `json:"root_id"`       // visibile solo alla finanza
	start, end   time.Time
}

const periodSelect = `select p.id, p.fiscal_year_id, f.name, p.name, p.ordinal, p.start_date, p.end_date, p.status, p.closed_at,
	(select a.id from budget_allocations a where a.period_id = p.id and a.kind = 'root')
	from budget_periods p join fiscal_years f on f.tenant_id = p.tenant_id and f.id = p.fiscal_year_id`

func scanPeriods(rows pgx.Rows, today time.Time) ([]Period, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Period, error) {
		var p Period
		err := r.Scan(&p.ID, &p.FiscalYearID, &p.FiscalYear, &p.Name, &p.Ordinal, &p.start, &p.end, &p.Status, &p.ClosedAt, &p.RootID)
		p.Start, p.End = p.start.Format(time.DateOnly), p.end.Format(time.DateOnly)
		if p.Status != "closed" && !p.end.Before(today) {
			d := int(p.end.Sub(today).Hours() / 24)
			p.DaysToClose = &d
		}
		return p, err
	})
	if out == nil {
		out = []Period{}
	}
	return out, err
}

func today() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// Periods elenca i periodi di un esercizio (o di tutti), in ordine di data.
func (s *Service) Periods(ctx context.Context, tx pgx.Tx, fyID string) ([]Period, error) {
	rows, err := tx.Query(ctx, periodSelect+` where ($1 = '' or p.fiscal_year_id::text = $1) order by p.start_date`, fyID)
	if err != nil {
		return nil, err
	}
	return scanPeriods(rows, today())
}

func (s *Service) period(ctx context.Context, tx pgx.Tx, id string) (*Period, error) {
	rows, err := tx.Query(ctx, periodSelect+` where p.id::text = $1`, id)
	if err != nil {
		return nil, err
	}
	ps, err := scanPeriods(rows, today())
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, engine.ErrNotFound
	}
	return &ps[0], nil
}

var monthNames = []string{"gen", "feb", "mar", "apr", "mag", "giu", "lug", "ago", "set", "ott", "nov", "dic"}

// splitFiscalYear divide l'esercizio nei periodi della granularità: l'ultimo termina con
// l'esercizio anche se è più corto.
func splitFiscalYear(name string, start, end time.Time, gran string) []Period {
	step := map[string]int{"year": 0, "semester": 6, "quarter": 3, "month": 1}[gran]
	var out []Period
	if step == 0 {
		return []Period{{Name: name, Ordinal: 1, start: start, end: end}}
	}
	label := map[string]string{"semester": "S", "quarter": "Q"}[gran]
	from := start
	for i := 1; !from.After(end); i++ {
		to := from.AddDate(0, step, 0).AddDate(0, 0, -1)
		if to.After(end) {
			to = end
		}
		n := fmt.Sprintf("%s%d %s", label, i, name)
		if gran == "month" {
			n = fmt.Sprintf("%s %d", monthNames[from.Month()-1], from.Year())
		}
		out = append(out, Period{Name: n, Ordinal: i, start: from, end: to})
		from = to.AddDate(0, 0, 1)
	}
	return out
}

// EnsurePeriods genera i periodi di un esercizio con la granularità indicata (quella del
// tenant se vuota). Se l'esercizio ha già periodi con la stessa suddivisione non fa nulla;
// se la suddivisione cambia li rigenera solo quando nessuno ha stanziamenti.
func (s *Service) EnsurePeriods(ctx context.Context, tx pgx.Tx, a engine.Actor, fyID, gran string) ([]Period, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	return s.ensurePeriods(ctx, tx, a, fyID, gran)
}

func (s *Service) ensurePeriods(ctx context.Context, tx pgx.Tx, a engine.Actor, fyID, gran string) ([]Period, error) {
	if gran == "" {
		st, err := GetBudgetSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		gran = st.PeriodGranularity
	}
	if !granularities[gran] {
		return nil, engine.InvalidValue("granularity", "valore non ammesso (year, semester, quarter, month)")
	}
	var name, status string
	var start, end time.Time
	if err := tx.QueryRow(ctx, "select name, start_date, end_date, status from fiscal_years where id::text = $1 for update", fyID).Scan(&name, &start, &end, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("fiscal_year_id", "fiscal_year", "esercizio inesistente")
		}
		return nil, err
	}
	want := splitFiscalYear(name, start, end, gran)
	have, err := s.Periods(ctx, tx, fyID)
	if err != nil {
		return nil, err
	}
	same := len(have) == len(want)
	for i := 0; same && i < len(have); i++ {
		same = have[i].start.Equal(want[i].start) && have[i].end.Equal(want[i].end)
	}
	if same {
		return have, nil
	}
	if len(have) > 0 {
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from budget_allocations a join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
			where p.fiscal_year_id::text = $1 and (a.kind <> 'root' or exists (select 1 from budget_movements m where m.allocation_id = a.id)))`, fyID).Scan(&used); err != nil {
			return nil, err
		}
		if used {
			return nil, engine.InvalidCode("validation.finance.granularity_has_allocations", "granularity", "l'esercizio ha già stanziamenti: la suddivisione in periodi non si cambia")
		}
		var hasRootMoves bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from budget_movements m join budget_periods p on p.tenant_id = m.tenant_id and p.id = m.period_id
			where p.fiscal_year_id::text = $1)`, fyID).Scan(&hasRootMoves); err != nil {
			return nil, err
		}
		if hasRootMoves {
			return nil, engine.InvalidCode("validation.finance.granularity_plafond_set", "granularity", "il plafond è già stato registrato: la suddivisione in periodi non si cambia")
		}
		if _, err := tx.Exec(ctx, `delete from budget_allocations where period_id in (select id from budget_periods where fiscal_year_id::text = $1)`, fyID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "delete from budget_periods where fiscal_year_id::text = $1", fyID); err != nil {
			return nil, err
		}
	}
	now := today()
	for _, p := range want {
		st := "planning"
		switch {
		case status == "closed":
			st = "closed"
		case status == "open" && !p.start.After(now):
			st = "open"
		}
		if _, err := tx.Exec(ctx, `insert into budget_periods (tenant_id, id, fiscal_year_id, name, ordinal, start_date, end_date, status)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`, db.NewID(), fyID, p.Name, p.Ordinal, p.start, p.end, st); err != nil {
			return nil, err
		}
	}
	if err := s.aud(ctx, tx, a, "budget.periods", "fiscal_year", fyID, map[string]any{"granularity": gran, "periods": len(want)}); err != nil {
		return nil, err
	}
	return s.Periods(ctx, tx, fyID)
}

// OpenPeriod apre un periodo in pianificazione.
func (s *Service) OpenPeriod(ctx context.Context, tx pgx.Tx, a engine.Actor, id string) (*Period, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	p, err := s.lockPeriod(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Status != "planning" {
		return nil, engine.InvalidCode("validation.finance.period_not_planning", "status", "il periodo non è in pianificazione")
	}
	if _, err := tx.Exec(ctx, "update budget_periods set status = 'open' where id = $1", id); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "budget.period.open", "budget_period", id, nil); err != nil {
		return nil, err
	}
	return s.period(ctx, tx, id)
}

// lockPeriod blocca il periodo (serializza chi scrive sullo stesso albero) e lo restituisce.
func (s *Service) lockPeriod(ctx context.Context, tx pgx.Tx, id string) (*Period, error) {
	var ok bool
	if err := tx.QueryRow(ctx, "select true from budget_periods where id::text = $1 for update", id).Scan(&ok); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	return s.period(ctx, tx, id)
}

// ---------------------------------------------------------------------
// Nodi e numeri
// ---------------------------------------------------------------------

// Numbers sono i numeri di un nodo, calcolati sempre dalla stessa funzione (computeNumbers).
//
//   - stanziato: somma dei movimenti del nodo;
//   - ripartito: somma dello stanziato dei figli; riserva = stanziato - ripartito;
//   - impegnato: per un elemento, il maggiore tra budget approvato collegato, ordini aperti e speso;
//     per un'unità, la somma dei figli;
//   - speso: consuntivo da cost_rollups nei mesi del periodo (elemento e suoi figli); per
//     un'unità, la somma dei figli;
//   - residuo: stanziato - impegnato (negativo = sforamento);
//   - forecast a fine periodo: la previsione pubblicata, altrimenti lo speso proiettato
//     linearmente sul periodo, mai sotto l'impegnato; per un'unità, la somma dei figli.
type Numbers struct {
	AllocatedCents   int64 `json:"allocated_cents"`
	DistributedCents int64 `json:"distributed_cents"`
	ReserveCents     int64 `json:"reserve_cents"`
	CommittedCents   int64 `json:"committed_cents"`
	OrdersCents      int64 `json:"orders_cents"`
	SpentCents       int64 `json:"spent_cents"`
	ResidualCents    int64 `json:"residual_cents"`
	ForecastCents    int64 `json:"forecast_cents"`
	// own: impegnato proprio del nodo (budget collegati, ordini, speso), senza i figli
	ownBudget, ownOrders, ownSpent, ownForecast int64
}

type Node struct {
	ID              string  `json:"id"`
	PeriodID        string  `json:"period_id"`
	ParentID        *string `json:"parent_id"`
	Kind            string  `json:"kind"` // root | unit | element
	OrgUnitID       *string `json:"org_unit_id"`
	ElementID       *string `json:"element_id"`
	Label           string  `json:"label"`
	UnitCode        *string `json:"unit_code"`
	ManagerName     *string `json:"manager_name"`
	Nature          *string `json:"nature"`
	FundingSourceID *string `json:"funding_source_id"`
	CostCategoryID  *string `json:"cost_category_id"`
	EndPolicy       *string `json:"end_policy"`
	Note            string  `json:"note"`
	Archived        bool    `json:"archived"`
	Depth           int     `json:"depth"`
	Children        int     `json:"children"`
	Numbers
	// CanAllocate: l'attore può ripartire a un figlio; CanEdit: può cambiare l'importo del nodo.
	CanAllocate bool `json:"can_allocate"`
	CanEdit     bool `json:"can_edit"`
	// Budgets: budget di progetto che attingono dal nodo (solo per i nodi di un elemento).
	Budgets []NodeBudget `json:"budgets,omitempty"`
}

type NodeBudget struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Version    int    `json:"version"`
	Status     string `json:"status"`
	TotalCents int64  `json:"total_cents"`
	// ElementKey e ElementTitle: l'elemento del budget, per il link; CommittedCents: ciò che il budget impegna sul nodo
	// (il totale se è approvato e non è una previsione, altrimenti zero).
	ElementKey     string `json:"element_key"`
	ElementTitle   string `json:"element_title"`
	CommittedCents int64  `json:"committed_cents"`
}

const effLine = `(l.amount_cents
	+ coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = l.id and t.status = 'approved'), 0)
	- coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status = 'approved'), 0))`

// loadNodes legge i nodi di un periodo visibili all'attore (la RLS filtra), con lo stanziato.
func (s *Service) loadNodes(ctx context.Context, tx pgx.Tx, periodID string) ([]*Node, error) {
	rows, err := tx.Query(ctx, `
		select a.id, a.period_id, a.parent_id, a.kind, a.org_unit_id, a.element_id,
		       coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, '') || ' ' || coalesce(e.title, ''), ' '), a.name, ''),
		       u.code, mg.display_name, a.nature, a.funding_source_id, a.cost_category_id, a.end_policy, a.note, a.archived,
		       coalesce((select sum(m.amount_cents) from budget_movements m where m.allocation_id = a.id), 0)
		from budget_allocations a
		left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
		left join members mg on mg.tenant_id = u.tenant_id and mg.id = u.manager_id
		left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where a.period_id::text = $1`, periodID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Node, error) {
		n := &Node{}
		err := r.Scan(&n.ID, &n.PeriodID, &n.ParentID, &n.Kind, &n.OrgUnitID, &n.ElementID, &n.Label, &n.UnitCode, &n.ManagerName,
			&n.Nature, &n.FundingSourceID, &n.CostCategoryID, &n.EndPolicy, &n.Note, &n.Archived, &n.AllocatedCents)
		if n.Label == "" && n.Kind == "root" {
			n.Label = "Plafond"
		}
		return n, err
	})
}

// forest ordina i nodi in visita (padre prima dei figli, figli per etichetta) e ne imposta
// profondità e numero di figli. I nodi il cui padre non è visibile sono radici della vista.
func forest(nodes []*Node) (ordered []*Node, children map[string][]*Node) {
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	children = map[string][]*Node{}
	var roots []*Node
	for _, n := range nodes {
		if n.ParentID != nil && byID[*n.ParentID] != nil {
			children[*n.ParentID] = append(children[*n.ParentID], n)
		} else {
			roots = append(roots, n)
		}
	}
	less := func(l []*Node) {
		sort.SliceStable(l, func(i, j int) bool {
			if l[i].Kind != l[j].Kind {
				return l[i].Kind == "unit" // le unità prima degli elementi
			}
			return strings.ToLower(l[i].Label) < strings.ToLower(l[j].Label)
		})
	}
	less(roots)
	var walk func(n *Node, d int)
	walk = func(n *Node, d int) {
		n.Depth = d
		ordered = append(ordered, n)
		kids := children[n.ID]
		less(kids)
		n.Children = len(kids)
		for _, c := range kids {
			walk(c, d+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return ordered, children
}

// computeNumbers è l'unica funzione che calcola i numeri dei nodi di un periodo
// (budget.md §2): la usano l'albero, l'anteprima della chiusura, i controlli di impegno e
// gli avvisi. I nodi devono arrivare da loadNodes.
func (s *Service) computeNumbers(ctx context.Context, tx pgx.Tx, p *Period, nodes []*Node) (map[string][]*Node, error) {
	ordered, children := forest(nodes)
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	// Budget di progetto collegati: approvati correnti e previsioni, nei mesi del periodo.
	rows, err := tx.Query(ctx, `
		select b.allocation_id,
		       coalesce(sum(`+effLine+`) filter (where b.kind <> 'forecast'), 0),
		       coalesce(sum(`+effLine+`) filter (where b.kind = 'forecast'), 0)
		from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
		where b.allocation_id in (select id from budget_allocations where period_id = $1) and b.status = 'approved'
		  and (l.period is null or (l.period >= date_trunc('month', $2::date)::date and l.period <= $3))
		group by b.allocation_id`, p.ID, p.start, p.end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var bud, fc int64
		if err := rows.Scan(&id, &bud, &fc); err != nil {
			rows.Close()
			return nil, err
		}
		if n := byID[id]; n != nil {
			n.ownBudget, n.ownForecast = bud, fc
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Consuntivo e ordini: aggregati per mese, mai le singole voci.
	rows, err = tx.Query(ctx, `
		select a.id, coalesce(sum(r.amount_base_cents) filter (where r.kind = 'actual'), 0),
		       coalesce(sum(r.amount_base_cents) filter (where r.kind = 'committed'), 0)
		from budget_allocations a
		join lateral finance_family(a.element_id) f(id) on true
		join cost_rollups r on r.element_id = f.id and r.period >= date_trunc('month', $2::date)::date and r.period <= $3
		where a.period_id = $1 and a.kind = 'element' group by a.id`, p.ID, p.start, p.end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var act, ord int64
		if err := rows.Scan(&id, &act, &ord); err != nil {
			rows.Close()
			return nil, err
		}
		if n := byID[id]; n != nil {
			n.ownSpent, n.ownOrders = act, ord
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := today()
	total := p.end.Sub(p.start).Hours()/24 + 1
	elapsed := now.Sub(p.start).Hours()/24 + 1
	var calc func(n *Node)
	calc = func(n *Node) {
		var kidsAlloc, kidsCommitted, kidsSpent, kidsForecast, kidsOrders int64
		for _, c := range children[n.ID] {
			calc(c)
			kidsAlloc += c.AllocatedCents
			kidsCommitted += c.CommittedCents
			kidsSpent += c.SpentCents
			kidsForecast += c.ForecastCents
			kidsOrders += c.OrdersCents
		}
		n.DistributedCents = kidsAlloc
		n.ReserveCents = n.AllocatedCents - kidsAlloc
		if n.Kind == "element" {
			n.SpentCents, n.OrdersCents = n.ownSpent, n.ownOrders
			n.CommittedCents = max(n.ownBudget, n.ownOrders, n.ownSpent)
			proj := n.ownSpent
			if elapsed >= 1 && elapsed < total {
				proj = int64(float64(n.ownSpent) * total / elapsed)
			}
			fc := proj
			if n.ownForecast > 0 {
				fc = n.ownForecast
			}
			n.ForecastCents = max(fc, n.CommittedCents)
		} else {
			n.SpentCents, n.OrdersCents, n.CommittedCents, n.ForecastCents = kidsSpent, kidsOrders, kidsCommitted, kidsForecast
		}
		n.ResidualCents = n.AllocatedCents - n.CommittedCents
	}
	for _, n := range ordered {
		if n.ParentID == nil || byID[*n.ParentID] == nil {
			calc(n)
		}
	}
	return children, nil
}

// permissions: le unità che l'attore gestisce (responsabile) e se la ripartizione libera è attiva.
type actorScope struct {
	finance bool
	free    bool
	managed map[string]bool
}

func (s *Service) scope(ctx context.Context, tx pgx.Tx, a engine.Actor) (*actorScope, error) {
	st, err := GetBudgetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	sc := &actorScope{finance: a.Has("finance.write"), free: st.FreeAllocation, managed: map[string]bool{}}
	if a.MemberID == "" {
		return sc, nil
	}
	rows, err := tx.Query(ctx, "select managed_org_units($1::uuid)", a.MemberID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		sc.managed[id] = true
	}
	return sc, nil
}

// canAllocateUnder: l'attore può creare o cambiare i figli del nodo n.
func (sc *actorScope) canAllocateUnder(n *Node) bool {
	if sc.finance {
		return true
	}
	return sc.free && n.Kind == "unit" && n.OrgUnitID != nil && sc.managed[*n.OrgUnitID]
}

// Tree restituisce i nodi del periodo visibili all'attore, in ordine di visita, con i numeri
// e i permessi di scrittura.
func (s *Service) Tree(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string) ([]*Node, error) {
	p, err := s.period(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	if _, err := s.computeNumbers(ctx, tx, p, nodes); err != nil {
		return nil, err
	}
	ordered, _ := forest(nodes)
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	writable := p.Status == "planning" || p.Status == "open" || (p.Status == "closing" && sc.finance)
	for _, n := range ordered {
		if !writable {
			continue
		}
		n.CanAllocate = n.Kind != "element" && sc.canAllocateUnder(n)
		if n.Kind == "root" {
			n.CanEdit = sc.finance
		} else if n.ParentID != nil && byID[*n.ParentID] != nil {
			n.CanEdit = sc.canAllocateUnder(byID[*n.ParentID])
		} else {
			n.CanEdit = sc.finance
		}
	}
	// Budget collegati ai nodi di un elemento.
	if err := s.attachBudgets(ctx, tx, p, ordered); err != nil {
		return nil, err
	}
	if ordered == nil {
		ordered = []*Node{}
	}
	return ordered, nil
}

func (s *Service) attachBudgets(ctx context.Context, tx pgx.Tx, p *Period, nodes []*Node) error {
	rows, err := tx.Query(ctx, `select b.allocation_id, b.id, b.kind, b.version, b.status,
		coalesce((select sum(`+effLine+`) from budget_lines l where l.budget_id = b.id
		          and (l.period is null or (l.period >= date_trunc('month', $2::date)::date and l.period <= $3))), 0),
		coalesce(sp.key || '-' || e.number, ''), coalesce(e.title, '')
		from budgets b
		left join elements e on e.tenant_id = b.tenant_id and e.id = b.element_id
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where b.allocation_id in (select id from budget_allocations where period_id = $1) and b.status <> 'superseded'
		order by b.version`, p.ID, p.start, p.end)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for rows.Next() {
		var id string
		var nb NodeBudget
		if err := rows.Scan(&id, &nb.ID, &nb.Kind, &nb.Version, &nb.Status, &nb.TotalCents, &nb.ElementKey, &nb.ElementTitle); err != nil {
			return err
		}
		if nb.Status == "approved" && nb.Kind != "forecast" {
			nb.CommittedCents = nb.TotalCents
		}
		if n := byID[id]; n != nil {
			n.Budgets = append(n.Budgets, nb)
		}
	}
	return rows.Err()
}

func (s *Service) node(ctx context.Context, tx pgx.Tx, a engine.Actor, id string) (*Node, error) {
	var periodID string
	if err := tx.QueryRow(ctx, "select period_id from budget_allocations where id::text = $1", id).Scan(&periodID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	tree, err := s.Tree(ctx, tx, a, periodID)
	if err != nil {
		return nil, err
	}
	for _, n := range tree {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, engine.ErrNotFound
}

// Node restituisce un nodo con i suoi numeri.
func (s *Service) Node(ctx context.Context, tx pgx.Tx, a engine.Actor, id string) (*Node, error) {
	return s.node(ctx, tx, a, id)
}

// ---------------------------------------------------------------------
// Movimenti
// ---------------------------------------------------------------------

type Movement struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	AmountCents  int64     `json:"amount_cents"`
	CounterpartI *string   `json:"counterpart_id"`
	Counterpart  *string   `json:"counterpart"`
	BatchID      string    `json:"batch_id"`
	ClosureID    *string   `json:"closure_id"`
	TransferID   *string   `json:"transfer_id"`
	ApprovalID   *string   `json:"approval_request_id"`
	Reason       string    `json:"reason"`
	By           *string   `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	// BalanceCents: stanziato del nodo dopo il movimento.
	BalanceCents int64 `json:"balance_cents"`
}

// Movements è la storia di un nodo, dal più vecchio, con il saldo progressivo.
func (s *Service) Movements(ctx context.Context, tx pgx.Tx, id string) ([]Movement, error) {
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from budget_allocations where id::text = $1)", id).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, `
		select m.id, m.kind, m.amount_cents, m.counterpart_id,
		       (select coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, '') || ' ' || coalesce(e.title, ''), ' '), c.name)
		        from budget_allocations c
		        left join org_units u on u.tenant_id = c.tenant_id and u.id = c.org_unit_id
		        left join elements e on e.tenant_id = c.tenant_id and e.id = c.element_id
		        left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		        where c.id = m.counterpart_id),
		       m.batch_id, m.closure_id, m.transfer_id, m.approval_request_id, m.reason, mm.display_name, m.created_at,
		       (sum(m.amount_cents) over (order by m.created_at, m.id))::bigint
		from budget_movements m left join members mm on mm.tenant_id = m.tenant_id and mm.id = m.created_by
		where m.allocation_id::text = $1 order by m.created_at, m.id`, id)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Movement])
	if out == nil {
		out = []Movement{}
	}
	return out, err
}

type move struct {
	alloc, counterpart, closure, transfer, approval string
	kind                                            string
	amount                                          int64
	batch, reason                                   string
}

func (s *Service) addMove(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string, m move) error {
	if m.amount == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into budget_movements (tenant_id, id, period_id, allocation_id, kind, amount_cents, counterpart_id, batch_id,
		closure_id, transfer_id, approval_request_id, reason, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		db.NewID(), periodID, m.alloc, m.kind, m.amount, nullable(m.counterpart), m.batch, nullable(m.closure), nullable(m.transfer),
		nullable(m.approval), strings.TrimSpace(m.reason), nullable(a.MemberID))
	return err
}

// verify controlla la regola dell'albero sui nodi toccati: importo non negativo, figli entro
// il nodo, nodo entro il padre. Gira dopo le scritture, con il periodo bloccato.
func (s *Service) verify(ctx context.Context, tx pgx.Tx, ids ...string) error {
	// Il controllo guarda l'intero albero, anche i nodi che chi scrive non vede (il padre del
	// suo nodo, per esempio): è un controllo di integrità, non una lettura per l'utente.
	return withFinance(ctx, tx, func() error { return s.verifyAll(ctx, tx, ids...) })
}

func (s *Service) verifyAll(ctx context.Context, tx pgx.Tx, ids ...string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var label string
		var amount, kids int64
		var parent *string
		var parentLabel string
		var parentAmount, parentKids int64
		err := tx.QueryRow(ctx, `
			select coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, ''), ''), a.name, 'Plafond'), a.parent_id,
			       coalesce((select sum(m.amount_cents) from budget_movements m where m.allocation_id = a.id), 0),
			       coalesce((select sum(m.amount_cents) from budget_movements m join budget_allocations c on c.tenant_id = m.tenant_id and c.id = m.allocation_id
			                 where c.parent_id = a.id), 0)
			from budget_allocations a
			left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
			left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where a.id = $1`, id).Scan(&label, &parent, &amount, &kids)
		if err != nil {
			return err
		}
		if amount < 0 {
			return engine.InvalidParams("validation.finance.allocation_negative", "amount_cents", map[string]any{"label": label}, "lo stanziamento di «%s» non può diventare negativo", label)
		}
		if kids > amount {
			return engine.InvalidParams("validation.finance.children_exceed", "amount_cents", map[string]any{"label": label, "children": money(kids), "amount": money(amount)}, "«%s»: i figli (%s) supererebbero lo stanziamento (%s)", label, money(kids), money(amount))
		}
		if parent != nil {
			if err := tx.QueryRow(ctx, `
				select coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, ''), ''), a.name, 'Plafond'),
				       coalesce((select sum(m.amount_cents) from budget_movements m where m.allocation_id = a.id), 0),
				       coalesce((select sum(m.amount_cents) from budget_movements m join budget_allocations c on c.tenant_id = m.tenant_id and c.id = m.allocation_id
				                 where c.parent_id = a.id), 0)
				from budget_allocations a
				left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
				left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
				left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
				where a.id = $1`, *parent).Scan(&parentLabel, &parentAmount, &parentKids); err != nil {
				return err
			}
			if parentKids > parentAmount {
				return engine.InvalidParams("validation.finance.reserve_exceeded", "amount_cents", map[string]any{"excess": money(parentKids - parentAmount), "label": parentLabel, "allocated": money(parentAmount), "split": money(parentKids)}, "supera di %s la riserva di «%s» (stanziato %s, già ripartito %s)",
					money(parentKids-parentAmount), parentLabel, money(parentAmount), money(parentKids))
			}
		}
	}
	return nil
}

// writablePeriod blocca il periodo e verifica che si possa scrivere: in pianificazione o
// aperto per chi ha i permessi; in chiusura solo per la finanza.
func (s *Service) writablePeriod(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string) (*Period, error) {
	p, err := s.lockPeriod(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	switch p.Status {
	case "planning", "open":
	case "closing":
		if !a.Has("finance.write") {
			return nil, engine.InvalidCode("validation.finance.period_closing_finance_only", "period_id", "il periodo è in chiusura: gli stanziamenti li modifica solo la finanza")
		}
	default:
		return nil, engine.InvalidCode("validation.finance.period_closed", "period_id", "il periodo è chiuso")
	}
	return p, nil
}

// ---------------------------------------------------------------------
// Operazioni sui nodi
// ---------------------------------------------------------------------

// RootInput crea la radice (plafond) di un periodo.
type RootInput struct {
	PeriodID    string `json:"period_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
}

// EnsureRoot crea la radice del periodo se manca e, se indicato, ne imposta il plafond.
func (s *Service) EnsureRoot(ctx context.Context, tx pgx.Tx, a engine.Actor, in RootInput) (*Node, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	if in.AmountCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "amount_cents", "l'importo non può essere negativo")
	}
	p, err := s.writablePeriod(ctx, tx, a, in.PeriodID)
	if err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, "select id from budget_allocations where period_id = $1 and kind = 'root'", p.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into budget_allocations (tenant_id, id, period_id, kind, name, created_by)
			values (app_tenant(), $1, $2, 'root', $3, $4)`, id, p.ID, "Plafond "+p.Name, nullable(a.MemberID)); err != nil {
			return nil, err
		}
		if err := s.aud(ctx, tx, a, "budget.root.create", "budget_allocation", id, map[string]any{"period_id": p.ID}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if in.AmountCents > 0 || in.Reason != "" {
		if _, err := s.setAmount(ctx, tx, a, id, in.AmountCents, in.Reason); err != nil {
			return nil, err
		}
	}
	return s.node(ctx, tx, a, id)
}

// NodeInput crea un nodo con il suo stanziamento iniziale.
type NodeInput struct {
	PeriodID        string  `json:"period_id"`
	ParentID        string  `json:"parent_id"`
	OrgUnitID       *string `json:"org_unit_id"`
	ElementID       *string `json:"element_id"`
	Nature          *string `json:"nature"` // capex | opex
	FundingSourceID *string `json:"funding_source_id"`
	CostCategoryID  *string `json:"cost_category_id"`
	AmountCents     int64   `json:"amount_cents"`
	Note            string  `json:"note"`
	Reason          string  `json:"reason"`
}

func (s *Service) allocated(ctx context.Context, tx pgx.Tx, id string) (int64, error) {
	var v int64
	err := tx.QueryRow(ctx, "select coalesce(sum(amount_cents), 0) from budget_movements where allocation_id::text = $1", id).Scan(&v)
	return v, err
}

// parentFor carica il padre e verifica che l'attore possa ripartire sotto di lui.
func (s *Service) parentFor(ctx context.Context, tx pgx.Tx, a engine.Actor, sc *actorScope, id, periodID string) (*Node, error) {
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.ID == id {
			if n.Kind == "element" {
				return nil, engine.InvalidCode("validation.finance.item_not_allocatable", "parent_id", "un elemento non si ripartisce ulteriormente")
			}
			if !sc.canAllocateUnder(n) {
				return nil, engine.ErrForbidden
			}
			return n, nil
		}
	}
	if !sc.finance {
		return nil, engine.ErrForbidden // il padre c'è ma chi scrive non lo vede: non gli compete
	}
	return nil, engine.NotFound("parent_id", "", "nodo padre inesistente in questo periodo")
}

// CreateNode crea un nodo sotto un padre e ci mette lo stanziamento iniziale (ripartizione dal padre).
func (s *Service) CreateNode(ctx context.Context, tx pgx.Tx, a engine.Actor, in NodeInput) (*Node, error) {
	if in.AmountCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "amount_cents", "l'importo non può essere negativo")
	}
	if (in.OrgUnitID == nil || *in.OrgUnitID == "") == (in.ElementID == nil || *in.ElementID == "") {
		return nil, engine.Required("org_unit_id", "indica un'unità oppure un elemento")
	}
	if in.Nature != nil && *in.Nature != "" && *in.Nature != "capex" && *in.Nature != "opex" {
		return nil, engine.InvalidValue("nature", "natura non ammessa (capex, opex)")
	}
	if _, err := s.writablePeriod(ctx, tx, a, in.PeriodID); err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	parent, err := s.parentFor(ctx, tx, a, sc, in.ParentID, in.PeriodID)
	if err != nil {
		return nil, err
	}
	id, err := s.insertNode(ctx, tx, a, parent, in)
	if err != nil {
		return nil, err
	}
	batch := db.NewID()
	if err := s.addMove(ctx, tx, a, in.PeriodID, move{alloc: id, counterpart: parent.ID, kind: MoveAllocate, amount: in.AmountCents, batch: batch, reason: in.Reason}); err != nil {
		return nil, err
	}
	if err := s.verify(ctx, tx, id, parent.ID); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "budget.node.create", "budget_allocation", id, map[string]any{"period_id": in.PeriodID, "parent_id": parent.ID,
		"org_unit_id": in.OrgUnitID, "element_id": in.ElementID, "amount_cents": in.AmountCents}); err != nil {
		return nil, err
	}
	return s.node(ctx, tx, a, id)
}

// insertNode scrive il nodo (senza movimenti) controllando che il destinatario sia coerente
// con l'organizzazione: un'unità si ripartisce sotto la radice o sotto una sua unità superiore.
func (s *Service) insertNode(ctx context.Context, tx pgx.Tx, a engine.Actor, parent *Node, in NodeInput) (string, error) {
	kind := "unit"
	var unit, el any
	if in.ElementID != nil && *in.ElementID != "" {
		kind = "element"
		el = *in.ElementID
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from elements where id::text = $1 and deleted_at is null and not is_draft)", *in.ElementID).Scan(&ok); err != nil {
			return "", err
		}
		if !ok {
			return "", engine.NotFound("element_id", "element", "elemento inesistente")
		}
	} else {
		unit = *in.OrgUnitID
		var archived bool
		if err := tx.QueryRow(ctx, "select archived from org_units where id::text = $1", *in.OrgUnitID).Scan(&archived); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", engine.NotFound("org_unit_id", "unit", "unità inesistente")
			}
			return "", err
		}
		if archived {
			return "", engine.InvalidCode("validation.finance.unit_archived", "org_unit_id", "l'unità è archiviata")
		}
		if parent.Kind == "unit" {
			var below bool
			if err := tx.QueryRow(ctx, `select $2::uuid in (select org_subtree($1::uuid)) and $1::uuid <> $2::uuid`, *parent.OrgUnitID, *in.OrgUnitID).Scan(&below); err != nil {
				return "", err
			}
			if !below {
				return "", engine.InvalidParams("validation.finance.unit_not_under", "org_unit_id", map[string]any{"label": parent.Label}, "l'unità non sta sotto «%s» nell'organizzazione", parent.Label)
			}
		}
	}
	id := db.NewID()
	_, err := tx.Exec(ctx, `insert into budget_allocations (tenant_id, id, period_id, parent_id, kind, org_unit_id, element_id, nature, funding_source_id, cost_category_id, note, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, in.PeriodID, parent.ID, kind, unit, el, nullablePtr(in.Nature), nullablePtr(in.FundingSourceID), nullablePtr(in.CostCategoryID),
		strings.TrimSpace(in.Note), nullable(a.MemberID))
	if err != nil {
		if isUnique(err) {
			return "", engine.Duplicate("org_unit_id", "esiste già un nodo per questo destinatario, natura e fonte nel periodo")
		}
		if strings.Contains(err.Error(), "23503") {
			return "", engine.NotFound("funding_source_id", "", "fonte o categoria inesistente")
		}
		return "", err
	}
	return id, nil
}

// SetAmount cambia lo stanziamento di un nodo con un movimento di rettifica (o iniziale per la
// radice) di pari differenza. Chi non è finanza lo fa solo sui figli di un'unità che gestisce.
func (s *Service) SetAmount(ctx context.Context, tx pgx.Tx, a engine.Actor, id string, amount int64, reason string) (*Node, error) {
	if _, err := s.setAmount(ctx, tx, a, id, amount, reason); err != nil {
		return nil, err
	}
	return s.node(ctx, tx, a, id)
}

func (s *Service) setAmount(ctx context.Context, tx pgx.Tx, a engine.Actor, id string, amount int64, reason string) (int64, error) {
	if amount < 0 {
		return 0, engine.InvalidCode("validation.not_negative", "amount_cents", "l'importo non può essere negativo")
	}
	var periodID, kind string
	var parentID *string
	if err := tx.QueryRow(ctx, "select period_id, kind, parent_id from budget_allocations where id::text = $1", id).Scan(&periodID, &kind, &parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, engine.ErrNotFound
		}
		return 0, err
	}
	p, err := s.writablePeriod(ctx, tx, a, periodID)
	if err != nil {
		return 0, err
	}
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return 0, err
	}
	var parent *Node
	if kind == "root" {
		if !sc.finance {
			return 0, engine.ErrForbidden
		}
	} else {
		if parent, err = s.parentFor(ctx, tx, a, sc, *parentID, periodID); err != nil {
			return 0, err
		}
	}
	cur, err := s.allocated(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	delta := amount - cur
	if delta == 0 {
		return 0, nil
	}
	if delta < 0 && kind == "element" {
		// Non si ritira ciò che è già impegnato.
		nodes, err := s.loadNodes(ctx, tx, periodID)
		if err != nil {
			return 0, err
		}
		if _, err := s.computeNumbers(ctx, tx, p, nodes); err != nil {
			return 0, err
		}
		for _, n := range nodes {
			if n.ID == id && amount < n.CommittedCents {
				return 0, engine.InvalidParams("validation.finance.committed_floor", "amount_cents", map[string]any{"amount": money(n.CommittedCents)}, "sono già impegnati %s: lo stanziamento non può scendere sotto", money(n.CommittedCents))
			}
		}
	}
	mk, cp := MoveAdjust, ""
	if kind == "root" {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from budget_movements where allocation_id::text = $1", id).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			mk = MoveInitial
		}
	} else {
		cp = parent.ID
		if cur == 0 && delta > 0 {
			mk = MoveAllocate
		}
	}
	if err := s.addMove(ctx, tx, a, periodID, move{alloc: id, counterpart: cp, kind: mk, amount: delta, batch: db.NewID(), reason: reason}); err != nil {
		return 0, err
	}
	toCheck := []string{id}
	if parentID != nil {
		toCheck = append(toCheck, *parentID)
	}
	if err := s.verify(ctx, tx, toCheck...); err != nil {
		return 0, err
	}
	if kind == "root" {
		if err := s.syncEnvelope(ctx, tx, p.FiscalYearID); err != nil {
			return 0, err
		}
	}
	return delta, s.aud(ctx, tx, a, "budget.node.amount", "budget_allocation", id, map[string]any{"from_cents": cur, "to_cents": amount, "kind": mk})
}

// syncEnvelope tiene il vecchio plafond dell'esercizio (envelope_cents, usato dalla
// graduatoria) uguale alla somma delle radici dei suoi periodi.
func (s *Service) syncEnvelope(ctx context.Context, tx pgx.Tx, fyID string) error {
	_, err := tx.Exec(ctx, `update fiscal_years set envelope_cents = (
		select coalesce(sum(m.amount_cents), 0) from budget_movements m join budget_allocations a on a.tenant_id = m.tenant_id and a.id = m.allocation_id
		join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
		where a.kind = 'root' and p.fiscal_year_id = fiscal_years.id)
		where id = $1 and exists (select 1 from budget_allocations a join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
		                          where a.kind = 'root' and p.fiscal_year_id = fiscal_years.id)`, fyID)
	return err
}

// NodePatch cambia i dati di un nodo che non sono importi.
type NodePatch struct {
	EndPolicy      *string `json:"end_policy"` // "" = come il tenant
	Note           *string `json:"note"`
	Archived       *bool   `json:"archived"`
	CostCategoryID *string `json:"cost_category_id"`
}

func (s *Service) PatchNode(ctx context.Context, tx pgx.Tx, a engine.Actor, id string, in NodePatch) (*Node, error) {
	var periodID string
	var parentID *string
	if err := tx.QueryRow(ctx, "select period_id, parent_id from budget_allocations where id::text = $1", id).Scan(&periodID, &parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if _, err := s.writablePeriod(ctx, tx, a, periodID); err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	if !sc.finance {
		if parentID == nil {
			return nil, engine.ErrForbidden
		}
		if _, err := s.parentFor(ctx, tx, a, sc, *parentID, periodID); err != nil {
			return nil, err
		}
		if in.EndPolicy != nil {
			return nil, engine.ErrForbidden // la politica di fine periodo la decide la finanza
		}
	}
	if in.EndPolicy != nil && *in.EndPolicy != "" && !endPolicies[*in.EndPolicy] {
		return nil, engine.InvalidValue("end_policy", "valore non ammesso (return, carry, lapse)")
	}
	if in.Archived != nil && *in.Archived {
		n, err := s.node(ctx, tx, a, id)
		if err != nil {
			return nil, err
		}
		var kids, budgets int
		if err := tx.QueryRow(ctx, `select (select count(*) from budget_allocations where parent_id = $1 and not archived),
			(select count(*) from budgets where allocation_id = $1 and status <> 'superseded')`, id).Scan(&kids, &budgets); err != nil {
			return nil, err
		}
		switch {
		case n.Kind == "root":
			return nil, engine.InvalidCode("validation.finance.root_not_archivable", "archived", "la radice non si archivia")
		case n.AllocatedCents != 0:
			return nil, engine.InvalidParams("validation.finance.node_has_allocated", "archived", map[string]any{"amount": money(n.AllocatedCents)}, "il nodo ha ancora %s stanziati: azzera lo stanziamento prima di archiviarlo", money(n.AllocatedCents))
		case kids > 0:
			return nil, engine.InvalidParams("validation.finance.node_has_children", "archived", map[string]any{"n": kids}, "archivia prima i nodi sotto (%d)", kids)
		case budgets > 0:
			return nil, engine.InvalidCode("validation.finance.node_has_budgets", "archived", "ci sono budget di progetto che attingono dal nodo")
		}
	}
	set := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.EndPolicy != nil {
		add("end_policy", nullablePtr(in.EndPolicy))
	}
	if in.Note != nil {
		add("note", strings.TrimSpace(*in.Note))
	}
	if in.Archived != nil {
		add("archived", *in.Archived)
	}
	if in.CostCategoryID != nil {
		add("cost_category_id", nullablePtr(in.CostCategoryID))
	}
	if len(set) > 0 {
		if _, err := tx.Exec(ctx, "update budget_allocations set "+strings.Join(set, ", ")+" where id::text = $1", args...); err != nil {
			if strings.Contains(err.Error(), "23503") {
				return nil, engine.NotFound("cost_category_id", "", "categoria inesistente")
			}
			return nil, err
		}
		if err := s.aud(ctx, tx, a, "budget.node.update", "budget_allocation", id, map[string]any{"fields": len(set)}); err != nil {
			return nil, err
		}
	}
	return s.node(ctx, tx, a, id)
}

// ---------------------------------------------------------------------
// Collegamento con i budget di progetto
// ---------------------------------------------------------------------

// withFinance esegue fn con la visibilità della finanza: serve a chi decide un'approvazione
// (sponsor, organo) e non vede gli stanziamenti ma deve poterli impegnare o spostare.
// L'azione è già autorizzata dall'approvazione; il valore precedente viene ripristinato.
func withFinance(ctx context.Context, tx pgx.Tx, fn func() error) error {
	var prev string
	if err := tx.QueryRow(ctx, "select coalesce(current_setting('app.finance', true), '')").Scan(&prev); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "select set_config('app.finance', 'on', true)"); err != nil {
		return err
	}
	err := fn()
	if _, e2 := tx.Exec(ctx, "select set_config('app.finance', $1, true)", prev); e2 != nil && err == nil {
		err = e2
	}
	return err
}

// commitCheck verifica che il budget (nel totale indicato) stia nello stanziamento da cui
// attinge, insieme agli altri budget approvati sullo stesso nodo. Chiamata all'invio e
// all'approvazione, con il periodo bloccato.
func (s *Service) commitCheck(ctx context.Context, tx pgx.Tx, budgetID, allocID string) error {
	return withFinance(ctx, tx, func() error {
		var periodID string
		var ps, pe time.Time
		var label string
		if err := tx.QueryRow(ctx, `select p.id, p.start_date, p.end_date, coalesce(nullif(coalesce(sp.key || '-' || e.number, ''), ''), a.name)
			from budget_allocations a join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
			left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where a.id = $1`, allocID).Scan(&periodID, &ps, &pe, &label); err != nil {
			return engine.NotFound("allocation_id", "", "stanziamento inesistente")
		}
		if _, err := s.lockPeriod(ctx, tx, periodID); err != nil {
			return err
		}
		allocated, err := s.allocated(ctx, tx, allocID)
		if err != nil {
			return err
		}
		var others, mine int64
		if err := tx.QueryRow(ctx, `select
			coalesce((select sum(`+effLine+`) from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
			          where b.allocation_id = $1 and b.status = 'approved' and b.kind <> 'forecast' and b.id <> $2
			            and not exists (select 1 from budgets me where me.id = $2 and me.element_id = b.element_id and me.fiscal_year_id = b.fiscal_year_id)
			            and (l.period is null or (l.period >= date_trunc('month', $3::date)::date and l.period <= $4))), 0),
			coalesce((select sum(l.amount_cents) from budget_lines l where l.budget_id = $2
			          and (l.period is null or (l.period >= date_trunc('month', $3::date)::date and l.period <= $4))), 0)`,
			allocID, budgetID, ps, pe).Scan(&others, &mine); err != nil {
			return err
		}
		var forecast bool
		if err := tx.QueryRow(ctx, "select kind = 'forecast' from budgets where id = $1", budgetID).Scan(&forecast); err != nil {
			return err
		}
		if forecast {
			return nil // la previsione non impegna
		}
		if others+mine > allocated {
			return engine.InvalidParams("validation.finance.budget_exceeds_allocation", "allocation_id", map[string]any{"budget": money(mine), "label": label, "available": money(max(allocated-others, 0)), "allocated": money(allocated)}, "il budget (%s) supera lo stanziamento di «%s»: disponibili %s su %s",
				money(mine), label, money(max(allocated-others, 0)), money(allocated))
		}
		return nil
	})
}

// SetBudgetAllocation collega un budget di progetto a uno stanziamento (o lo scollega).
func (s *Service) SetBudgetAllocation(ctx context.Context, tx pgx.Tx, a engine.Actor, budgetID string, allocID *string) error {
	if err := requireWrite(a); err != nil {
		return err
	}
	var elementID, fyID, status string
	if err := tx.QueryRow(ctx, "select element_id, fiscal_year_id, status from budgets where id::text = $1 for update", budgetID).Scan(&elementID, &fyID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.ErrNotFound
		}
		return err
	}
	if status == "superseded" {
		return engine.InvalidCode("validation.finance.version_superseded", "status", "la versione è stata sostituita")
	}
	if allocID == nil || *allocID == "" {
		if _, err := tx.Exec(ctx, "update budgets set allocation_id = null where id::text = $1", budgetID); err != nil {
			return err
		}
		return s.aud(ctx, tx, a, "budget.allocation.unlink", "budget", budgetID, nil)
	}
	if err := s.checkAllocationLink(ctx, tx, *allocID, elementID, fyID); err != nil {
		return err
	}
	if status == "approved" {
		if err := s.commitCheck(ctx, tx, budgetID, *allocID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "update budgets set allocation_id = $2 where id::text = $1", budgetID, *allocID); err != nil {
		return err
	}
	return s.aud(ctx, tx, a, "budget.allocation.link", "budget", budgetID, map[string]any{"allocation_id": *allocID})
}

// checkAllocationLink: il nodo è dell'elemento del budget e di un periodo dello stesso esercizio.
func (s *Service) checkAllocationLink(ctx context.Context, tx pgx.Tx, allocID, elementID, fyID string) error {
	var ok bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from budget_allocations a join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
		where a.id::text = $1 and a.kind = 'element' and a.element_id::text = $2 and p.fiscal_year_id::text = $3)`, allocID, elementID, fyID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.InvalidCode("validation.finance.allocation_wrong_node", "allocation_id", "lo stanziamento deve essere un nodo dell'elemento del budget, in un periodo dello stesso esercizio")
	}
	return nil
}

// ElementAllocation: un nodo dell'elemento a cui collegare un budget, con il percorso nell'albero e quanto ne resta.
type ElementAllocation struct {
	ID          string `json:"id"`
	PeriodID    string `json:"period_id"`
	Period      string `json:"period"`
	Allocated   int64  `json:"allocated_cents"`
	FiscalYear  string `json:"fiscal_year"`
	FiscalYearI string `json:"fiscal_year_id"`
	// PeriodStatus, StartDate e EndDate: il periodo, per sapere quali righe del budget cadono nel nodo.
	PeriodStatus string `json:"period_status"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	// Path: i nodi sopra, dalla radice, che chi chiama vede (la RLS taglia gli altri).
	Path []string `json:"path"`
	// Others: impegnato dagli altri budget approvati sul nodo; Available = stanziato - altri (può essere negativo).
	Others    int64 `json:"others_cents"`
	Available int64 `json:"available_cents"`
	// Mine: il totale del budget indicato nel periodo del nodo (0 senza budget); Linked: il budget è già collegato qui.
	Mine   int64 `json:"mine_cents"`
	Linked bool  `json:"linked"`
}

// ElementAllocationsResult: le opzioni di collegamento di un elemento; LinkedHidden dice che il budget è collegato
// a un nodo che chi chiama non vede.
type ElementAllocationsResult struct {
	Items        []ElementAllocation `json:"items"`
	LinkedID     *string             `json:"linked_id"`
	LinkedHidden bool                `json:"linked_hidden"`
}

// ElementAllocationQuery: Element è l'id o la chiave (SPAZIO-N); FiscalYear restringe ai periodi dell'esercizio;
// BudgetID è il budget da collegare (per «il suo» totale, il nodo attuale e l'esclusione dei suoi impegni).
type ElementAllocationQuery struct {
	Element, FiscalYear, BudgetID string
}

// ElementAllocations elenca i nodi di un elemento che chi chiama vede (la RLS filtra), non archiviati e in periodi
// non chiusi (salvo quello già collegato), con percorso, disponibile e quanto ne userebbe il budget.
func (s *Service) ElementAllocations(ctx context.Context, tx pgx.Tx, q ElementAllocationQuery) (*ElementAllocationsResult, error) {
	var elementID string
	if q.Element != "" {
		err := tx.QueryRow(ctx, `select e.id::text from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where e.id::text = $1 or upper(sp.key || '-' || e.number) = upper($1)`, q.Element).Scan(&elementID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	res := &ElementAllocationsResult{Items: []ElementAllocation{}}
	var linked *string
	if q.BudgetID != "" {
		if err := tx.QueryRow(ctx, "select allocation_id::text, element_id::text, fiscal_year_id::text from budgets where id::text = $1", q.BudgetID).Scan(&linked, &elementID, &q.FiscalYear); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, engine.ErrNotFound
			}
			return nil, err
		}
		res.LinkedID = linked
	}
	if elementID == "" {
		return res, nil
	}
	rows, err := tx.Query(ctx, `select a.id::text, p.id::text, p.name, coalesce((select sum(m.amount_cents) from budget_movements m where m.allocation_id = a.id), 0),
			f.name, f.id::text, p.status, p.start_date, p.end_date,
			coalesce((select sum(`+effLine+`) from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
			          where b.allocation_id = a.id and b.status = 'approved' and b.kind <> 'forecast' and b.id::text <> $3
			            and not exists (select 1 from budgets me where me.id::text = $3 and me.element_id = b.element_id and me.fiscal_year_id = b.fiscal_year_id)
			            and (l.period is null or (l.period >= date_trunc('month', p.start_date)::date and l.period <= p.end_date))), 0),
			coalesce((select sum(l.amount_cents) from budget_lines l where l.budget_id::text = $3
			          and (l.period is null or (l.period >= date_trunc('month', p.start_date)::date and l.period <= p.end_date))), 0),
			a.id::text = $4
		from budget_allocations a join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
		join fiscal_years f on f.tenant_id = p.tenant_id and f.id = p.fiscal_year_id
		where a.element_id::text = $1 and ($2 = '' or f.id::text = $2) and (a.id::text = $4 or (not a.archived and p.status <> 'closed'))
		order by p.start_date`, elementID, q.FiscalYear, q.BudgetID, deref(linked))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o ElementAllocation
		var ps, pe time.Time
		if err := rows.Scan(&o.ID, &o.PeriodID, &o.Period, &o.Allocated, &o.FiscalYear, &o.FiscalYearI, &o.PeriodStatus, &ps, &pe, &o.Others, &o.Mine, &o.Linked); err != nil {
			return nil, err
		}
		o.StartDate, o.EndDate = ps.Format("2006-01-02"), pe.Format("2006-01-02")
		o.Available = o.Allocated - o.Others
		res.Items = append(res.Items, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range res.Items {
		path, err := s.nodePath(ctx, tx, res.Items[i].ID)
		if err != nil {
			return nil, err
		}
		res.Items[i].Path = path
	}
	if linked != nil {
		res.LinkedHidden = true
		for _, o := range res.Items {
			if o.ID == *linked {
				res.LinkedHidden = false
			}
		}
	}
	return res, nil
}

// nodePath: i nomi degli antenati del nodo, dalla radice; ci si ferma al primo che la RLS nasconde.
func (s *Service) nodePath(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	rows, err := tx.Query(ctx, `with recursive up as (
			select a.id, a.parent_id, 0 as lvl from budget_allocations a where a.id::text = $1
			union all select p.id, p.parent_id, up.lvl + 1 from up join budget_allocations p on p.id = up.parent_id
		)
		select case when a.kind = 'root' then 'Plafond' else coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, '') || ' ' || coalesce(e.title, ''), ' '), a.name, '') end
		from up join budget_allocations a on a.id = up.id
		left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
		left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where up.lvl > 0 order by up.lvl desc`, id)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if out == nil {
		out = []string{}
	}
	return out, err
}
