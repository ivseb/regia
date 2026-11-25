package finance

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
	"regia/internal/platform/db"
)

// Earned value (demand.md §10, decisione 3): opzionale per progetto (ext_project).
//   - BAC: il budget di baseline indicato, altrimenti il budget approvato corrente.
//   - PV: BAC × la quota di lavoro pianificata alla data, dalle date degli impatti e dalle
//     loro stime in giorni (in mancanza, la durata); senza impatti datati, la finestra
//     della richiesta.
//   - EV: BAC × percentuale di completamento, secondo il metodo: dichiarata
//     (percent_complete), impatti conclusi a peso uguale (milestones, 0/100), impatti
//     pesati con la stima, quelli in corso al 50% (work_packages, 50/50).
//   - AC: il consuntivo dagli aggregati fino al mese della data.
// Una fotografia per mese (ev_snapshots), aggiornata ogni giorno dal job in-process e su
// richiesta; i mesi passati restano come sono.

var pmRoles = []string{"pmo", "project_manager"}

type Snapshot struct {
	PeriodEnd  string    `json:"period_end"`
	BACCents   int64     `json:"bac_cents"`
	PVCents    int64     `json:"pv_cents"`
	EVCents    int64     `json:"ev_cents"`
	ACCents    int64     `json:"ac_cents"`
	Percent    float64   `json:"percent_complete"`
	CPI        *float64  `json:"cpi"`
	SPI        *float64  `json:"spi"`
	EACCents   *int64    `json:"eac_cents"` // BAC / CPI
	VACCents   *int64    `json:"vac_cents"` // BAC − EAC
	Manual     bool      `json:"manual"`
	ComputedAt time.Time `json:"computed_at"`
}

type CurvePoint struct {
	Date    string `json:"date"`
	PVCents int64  `json:"pv_cents"`
}

type Project struct {
	ElementID        string       `json:"element_id"`
	Key              string       `json:"key"`
	Title            string       `json:"title"`
	Enabled          bool         `json:"ev_enabled"`
	Method           *string      `json:"ev_method"`
	PercentComplete  float64      `json:"percent_complete"`
	BaselineBudgetID *string      `json:"baseline_budget_id"`
	BACCents         int64        `json:"bac_cents"`
	PlanStart        *string      `json:"plan_start"`
	PlanEnd          *string      `json:"plan_end"`
	Latest           *Snapshot    `json:"latest"`
	Snapshots        []Snapshot   `json:"snapshots,omitempty"`
	Curve            []CurvePoint `json:"curve,omitempty"`
	CanEdit          bool         `json:"can_edit"`
	Message          string       `json:"message,omitempty"`
}

type evTask struct {
	start, end time.Time
	scheduled  bool
	weight     float64
	category   string
}

type evPlan struct {
	enabled    bool
	method     *string
	pct        float64
	baseline   *string
	bac        int64
	tasks      []evTask
	start, end *time.Time
}

func loadPlan(ctx context.Context, tx pgx.Tx, elementID string) (*evPlan, error) {
	p := &evPlan{}
	err := tx.QueryRow(ctx, "select ev_enabled, ev_method, percent_complete::float8, baseline_budget_id from ext_project where element_id = $1", elementID).
		Scan(&p.enabled, &p.method, &p.pct, &p.baseline)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// BAC: totale effettivo della baseline indicata, altrimenti dei budget approvati correnti.
	if err := tx.QueryRow(ctx, `select coalesce(sum(l.amount_cents
			+ coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = l.id and t.status = 'approved'), 0)
			- coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status = 'approved'), 0)), 0)::bigint
		from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
		where case when $2::uuid is not null then b.id = $2::uuid else b.element_id = $1 and b.status = 'approved' and b.kind <> 'forecast' end`,
		elementID, p.baseline).Scan(&p.bac); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select e.start_at, e.due_at, x.estimate_effort_days::float8, coalesce(o.category, 'todo')
		from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.request_id = $1`, elementID)
	if err != nil {
		return nil, err
	}
	type row struct {
		Start, End *time.Time
		Days       *float64
		Cat        string
	}
	rs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		t := evTask{category: r.Cat, weight: 1}
		if r.Start != nil && r.End != nil && !r.End.Before(*r.Start) {
			t.start, t.end, t.scheduled = calendar.Day(*r.Start), calendar.Day(*r.End), true
			t.weight = float64(max(calendar.Workdays(t.start, t.end), 1))
		}
		if r.Days != nil && *r.Days > 0 {
			t.weight = *r.Days
		}
		p.tasks = append(p.tasks, t)
	}
	scheduled := 0
	for _, t := range p.tasks {
		if t.scheduled {
			scheduled++
		}
	}
	if scheduled == 0 {
		// Nessun impatto datato: la finestra della richiesta.
		var start, end *time.Time
		if err := tx.QueryRow(ctx, "select start_at, due_at from elements where id = $1", elementID).Scan(&start, &end); err != nil {
			return nil, err
		}
		if start != nil && end != nil && !end.Before(*start) {
			p.tasks = append(p.tasks, evTask{start: calendar.Day(*start), end: calendar.Day(*end), scheduled: true, weight: 1, category: "window"})
		}
	}
	for _, t := range p.tasks {
		if !t.scheduled {
			continue
		}
		if p.start == nil || t.start.Before(*p.start) {
			v := t.start
			p.start = &v
		}
		if p.end == nil || t.end.After(*p.end) {
			v := t.end
			p.end = &v
		}
	}
	return p, nil
}

// planned è la quota di lavoro pianificata conclusa alla data d (0..1).
func (p *evPlan) planned(d time.Time) float64 {
	d = calendar.Day(d)
	var done, total float64
	for _, t := range p.tasks {
		if !t.scheduled {
			continue
		}
		total += t.weight
		switch {
		case d.Before(t.start):
		case !d.Before(t.end):
			done += t.weight
		default:
			all := calendar.Workdays(t.start, t.end)
			if all > 0 {
				done += t.weight * float64(calendar.Workdays(t.start, d)) / float64(all)
			}
		}
	}
	if total == 0 {
		return 0
	}
	return done / total
}

// complete è la percentuale di completamento (0..100) secondo il metodo.
func (p *evPlan) complete() float64 {
	if p.method == nil {
		return p.pct
	}
	switch *p.method {
	case "milestones", "work_packages":
		var done, total float64
		for _, t := range p.tasks {
			if t.category == "cancelled" || t.category == "window" {
				continue
			}
			w := t.weight
			if *p.method == "milestones" {
				w = 1
			}
			total += w
			switch t.category {
			case "done":
				done += w
			case "in_progress":
				if *p.method == "work_packages" {
					done += w / 2
				}
			}
		}
		if total == 0 {
			return 0
		}
		return math.Round(10000*done/total) / 100
	}
	return p.pct
}

func actualTo(ctx context.Context, tx pgx.Tx, elementID string, d time.Time) (int64, error) {
	var ac int64
	err := tx.QueryRow(ctx, `select coalesce(sum(amount_base_cents), 0)::bigint from cost_rollups
		where element_id in (select id from finance_family($1)) and kind = 'actual' and period <= $2`, elementID, monthStart(d)).Scan(&ac)
	return ac, err
}

// computeSnapshot scrive la fotografia del mese di asOf (alla data asOf). pct = nil usa il metodo.
func computeSnapshot(ctx context.Context, tx pgx.Tx, elementID string, asOf time.Time, pct *float64) error {
	p, err := loadPlan(ctx, tx, elementID)
	if err != nil {
		return err
	}
	if !p.enabled {
		return engine.InvalidCode("validation.finance.ev_disabled", "ev_enabled", "l'earned value non è attivo per questo progetto")
	}
	percent := p.complete()
	if pct != nil {
		percent = *pct
	}
	ac, err := actualTo(ctx, tx, elementID, asOf)
	if err != nil {
		return err
	}
	pv := int64(math.Round(float64(p.bac) * p.planned(asOf)))
	ev := int64(math.Round(float64(p.bac) * percent / 100))
	_, err = tx.Exec(ctx, `insert into ev_snapshots (tenant_id, element_id, period_end, bac_cents, pv_cents, ev_cents, ac_cents, percent_complete, manual, computed_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, now())
		on conflict (tenant_id, element_id, period_end) do update set bac_cents = excluded.bac_cents, pv_cents = excluded.pv_cents,
			ev_cents = excluded.ev_cents, ac_cents = excluded.ac_cents, percent_complete = excluded.percent_complete, manual = excluded.manual, computed_at = now()`,
		elementID, monthEnd(asOf), p.bac, pv, ev, ac, percent, pct != nil)
	return err
}

func (s *Service) canEditProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) (bool, error) {
	if actor.Has("finance.write") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, elementID, actor.MemberID, pmRoles)
}

// Project restituisce impostazioni, fotografie e curva S di un progetto.
func (s *Service) Project(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Project, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	p, err := loadPlan(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	out := &Project{ElementID: el.ID, Key: el.Key, Title: el.Title, Enabled: p.enabled, Method: p.method, PercentComplete: p.pct,
		BaselineBudgetID: p.baseline, BACCents: p.bac, PlanStart: dateStr(p.start), PlanEnd: dateStr(p.end)}
	if out.CanEdit, err = s.canEditProject(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	if out.Snapshots, err = snapshots(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	if n := len(out.Snapshots); n > 0 {
		out.Latest = &out.Snapshots[n-1]
	}
	switch {
	case p.bac == 0:
		out.Message = "manca un budget approvato: il BAC è zero"
	case p.start == nil:
		out.Message = "mancano le date pianificate: il PV è zero"
	}
	if p.start != nil && p.end != nil {
		// Un punto per fine mese, più l'inizio e la fine del piano (al massimo 10 anni).
		add := func(d time.Time) {
			out.Curve = append(out.Curve, CurvePoint{Date: d.Format(time.DateOnly), PVCents: int64(math.Round(float64(p.bac) * p.planned(d)))})
		}
		add(p.start.AddDate(0, 0, -1))
		for m := monthEnd(*p.start); m.Before(*p.end) && len(out.Curve) < 121; m = monthEnd(m.AddDate(0, 0, 1)) {
			add(m)
		}
		add(*p.end)
	}
	return out, nil
}

func snapshots(ctx context.Context, tx pgx.Tx, elementID string) ([]Snapshot, error) {
	rows, err := tx.Query(ctx, `select period_end, bac_cents, pv_cents, ev_cents, ac_cents, percent_complete::float8, cpi::float8, spi::float8, manual, computed_at
		from ev_snapshots where element_id = $1 order by period_end`, elementID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Snapshot, error) {
		var s Snapshot
		var d time.Time
		err := r.Scan(&d, &s.BACCents, &s.PVCents, &s.EVCents, &s.ACCents, &s.Percent, &s.CPI, &s.SPI, &s.Manual, &s.ComputedAt)
		s.PeriodEnd = d.Format(time.DateOnly)
		if s.CPI != nil && *s.CPI > 0 {
			eac := int64(math.Round(float64(s.BACCents) / *s.CPI))
			vac := s.BACCents - eac
			s.EACCents, s.VACCents = &eac, &vac
		}
		return s, err
	})
	if out == nil {
		out = []Snapshot{}
	}
	return out, err
}

// ProjectInput attiva o configura l'earned value di un progetto.
type ProjectInput struct {
	Enabled          *bool    `json:"ev_enabled"`
	Method           *string  `json:"ev_method"` // percent_complete | milestones | work_packages
	PercentComplete  *float64 `json:"percent_complete"`
	BaselineBudgetID *string  `json:"baseline_budget_id"` // "" = budget approvato corrente
}

func (s *Service) SaveProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ProjectInput) (*Project, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canEditProject(ctx, tx, actor, el.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	p, err := loadPlan(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	if in.Enabled != nil {
		p.enabled = *in.Enabled
	}
	if in.Method != nil {
		switch *in.Method {
		case "percent_complete", "milestones", "work_packages":
			p.method = in.Method
		default:
			return nil, engine.InvalidValue("ev_method", "metodo non ammesso (percent_complete, milestones, work_packages)")
		}
	}
	if p.enabled && p.method == nil {
		m := "percent_complete"
		p.method = &m
	}
	if in.PercentComplete != nil {
		if *in.PercentComplete < 0 || *in.PercentComplete > 100 {
			return nil, engine.OutOfRange("percent_complete", 0, 100, "percentuale tra 0 e 100")
		}
		p.pct = math.Round(*in.PercentComplete*100) / 100
	}
	if in.BaselineBudgetID != nil {
		p.baseline = nil
		if *in.BaselineBudgetID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from budgets where id::text = $1 and element_id = $2 and status in ('approved','superseded') and kind <> 'forecast')",
				*in.BaselineBudgetID, el.ID).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				return nil, engine.InvalidCode("validation.finance.ev_baseline_invalid", "baseline_budget_id", "la baseline deve essere una versione approvata del budget del progetto")
			}
			p.baseline = in.BaselineBudgetID
		}
	}
	if _, err := tx.Exec(ctx, `insert into ext_project (tenant_id, element_id, ev_enabled, ev_method, baseline_budget_id, percent_complete, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, now())
		on conflict (tenant_id, element_id) do update set ev_enabled = excluded.ev_enabled, ev_method = excluded.ev_method,
			baseline_budget_id = excluded.baseline_budget_id, percent_complete = excluded.percent_complete, updated_by = excluded.updated_by, updated_at = now()`,
		el.ID, p.enabled, p.method, p.baseline, p.pct, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "project.ev", EntityType: "element", EntityID: el.ID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"ev_enabled": p.enabled, "ev_method": p.method,
			"percent_complete": p.pct, "baseline_budget_id": p.baseline}}); err != nil {
		return nil, err
	}
	if p.enabled {
		if err := computeSnapshot(ctx, tx, el.ID, time.Now().UTC(), nil); err != nil {
			return nil, err
		}
	}
	return s.Project(ctx, tx, actor, el.ID)
}

// SnapshotInput: fotografia alla data (default oggi). Per un mese passato si può indicare
// la percentuale di completamento di allora (la fotografia è segnata come manuale).
type SnapshotInput struct {
	AsOf            string   `json:"as_of"`
	PercentComplete *float64 `json:"percent_complete"`
}

func (s *Service) TakeSnapshot(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in SnapshotInput) (*Project, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canEditProject(ctx, tx, actor, el.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	asOf := time.Now().UTC()
	if in.AsOf != "" {
		if asOf, err = parseDate("as_of", in.AsOf); err != nil {
			return nil, err
		}
		if asOf.After(time.Now().UTC()) {
			return nil, engine.InvalidCode("validation.finance.date_not_future", "as_of", "la data non può essere futura")
		}
		// Per un mese chiuso la fotografia è a fine mese.
		if monthEnd(asOf).Before(calendar.Day(time.Now().UTC())) {
			asOf = monthEnd(asOf)
		}
	}
	if in.PercentComplete != nil && (*in.PercentComplete < 0 || *in.PercentComplete > 100) {
		return nil, engine.OutOfRange("percent_complete", 0, 100, "percentuale tra 0 e 100")
	}
	if err := computeSnapshot(ctx, tx, el.ID, asOf, in.PercentComplete); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "project.ev_snapshot", EntityType: "element", EntityID: el.ID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"period_end": monthEnd(asOf).Format(time.DateOnly), "manual": in.PercentComplete != nil}}); err != nil {
		return nil, err
	}
	return s.Project(ctx, tx, actor, el.ID)
}

// Projects elenca i progetti con l'earned value attivo e l'ultima fotografia.
func (s *Service) Projects(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Project, error) {
	rows, err := tx.Query(ctx, `select p.element_id from ext_project p join elements e on e.tenant_id = p.tenant_id and e.id = p.element_id
		where p.ev_enabled and e.deleted_at is null order by e.number limit 200`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, id := range ids {
		p, err := s.Project(ctx, tx, actor, id)
		if errors.Is(err, engine.ErrNotFound) {
			continue // non visibile all'attore
		}
		if err != nil {
			return nil, err
		}
		p.Curve, p.Snapshots = nil, nil
		out = append(out, *p)
	}
	return out, nil
}

// RefreshSnapshots aggiorna la fotografia del mese corrente di tutti i progetti con
// l'earned value attivo, tenant per tenant (ognuno nella sua transazione, con la RLS).
// Le fotografie manuali del mese non si toccano.
func RefreshSnapshots(ctx context.Context, d *db.DB, now time.Time) (int, error) {
	rows, err := d.Owner.Query(ctx, `select tenant_id::text, array_agg(element_id::text) from ext_project where ev_enabled group by tenant_id`)
	if err != nil {
		return 0, err
	}
	type tenantProjects struct {
		Tenant   string
		Elements []string
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tenantProjects])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, tp := range list {
		err := d.InTenant(ctx, tp.Tenant, db.TenantOpts{}, func(tx pgx.Tx) error {
			for _, el := range tp.Elements {
				var manual bool
				if err := tx.QueryRow(ctx, "select coalesce((select manual from ev_snapshots where element_id = $1 and period_end = $2), false)",
					el, monthEnd(now)).Scan(&manual); err != nil {
					return err
				}
				if manual {
					continue
				}
				if err := computeSnapshot(ctx, tx, el, now, nil); err != nil {
					return err
				}
				n++
			}
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// RunDaily è il job giornaliero in-process: aggiorna le fotografie dell'earned value un
// minuto dopo l'avvio e poi ogni 24 ore, finché ctx non è chiuso.
func RunDaily(ctx context.Context, d *db.DB, log *slog.Logger) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := RefreshSnapshots(ctx, d, time.Now().UTC()); err != nil {
				log.Warn("earned value giornaliero", "errore", err)
			} else if n > 0 {
				log.Info("earned value aggiornato", "progetti", n)
			}
			logAlerts(ctx, d, log)
			t.Reset(24 * time.Hour)
		}
	}
}
