package projects

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

// Baseline del piano (progetti.md §3 «Baseline e variazioni», passo 3).
//
// Una baseline è una fotografia immutabile del piano: date e stima di ogni voce, inviluppo del
// progetto e budget delle richieste confluite. Le tabelle non si modificano né si cancellano
// (un trigger lo impedisce); la baseline in vigore è quella con il numero più alto. La prima la
// fissa chi pianifica il progetto, le successive nascono solo da una change request approvata.

// BaselineItem è una voce come era nella baseline.
type BaselineItem struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Key        string   `json:"key"`
	Title      string   `json:"title"`
	PhaseID    *string  `json:"phase_id"`
	Phase      *string  `json:"phase"`
	Start      *string  `json:"start"`
	End        *string  `json:"end"`
	EffortDays *float64 `json:"estimate_effort_days"`
	Restricted bool     `json:"restricted,omitempty"`
}

// Baseline è una versione del piano.
type Baseline struct {
	BaselineHead
	Note          string `json:"note"`
	ChangeRequest *Ref   `json:"change_request"`
	// Budget: nil se l'attore non vede il budget del progetto.
	Budget *Budget        `json:"budget"`
	Items  []BaselineItem `json:"items,omitempty"`
	// Current: è la baseline in vigore.
	Current bool `json:"current"`
}

// snapshot è il contenuto di una baseline (o del piano proposto da una change request).
type snapshot struct {
	Start  *string        `json:"start"`
	End    *string        `json:"end"`
	Budget *Budget        `json:"budget"`
	Items  []BaselineItem `json:"items"`
}

// snapshotOf fotografa il piano già caricato (senza limiti di visibilità: la baseline è del
// progetto, non di chi la fissa). Le voci annullate non entrano.
func snapshotOf(p *Plan) snapshot {
	sn := snapshot{Items: []BaselineItem{}}
	phases := map[string]string{}
	for _, ph := range p.Phases {
		phases[ph.ID] = ph.Name
	}
	for _, it := range p.Items {
		if it.StateCategory == "cancelled" {
			continue
		}
		bi := BaselineItem{ID: it.ID, Kind: it.Kind, Key: it.Key, Title: it.Title, PhaseID: it.PhaseID, Start: it.Start, End: it.End, EffortDays: it.EffortDays}
		if it.PhaseID != nil {
			if n, ok := phases[*it.PhaseID]; ok {
				bi.Phase = &n
			}
		}
		sn.Items = append(sn.Items, bi)
		if it.Start != nil && (sn.Start == nil || *it.Start < *sn.Start) {
			sn.Start = it.Start
		}
		if it.End != nil && (sn.End == nil || *it.End > *sn.End) {
			sn.End = it.End
		}
	}
	return sn
}

// budgetSnapshot legge il budget aggregato delle richieste del progetto, senza i limiti
// dell'attore: entra nella baseline così com'è.
func (s *Service) budgetSnapshot(ctx context.Context, tx pgx.Tx, p *Plan) (*Budget, error) {
	if s.Budget == nil {
		return nil, nil
	}
	ids := make([]string, len(p.Project.Requests))
	for i, r := range p.Project.Requests {
		ids[i] = r.ID
	}
	b, err := s.Budget(ctx, tx, engine.Actor{Permissions: []string{"*"}}, p.Project.ID, ids)
	if b != nil {
		b.Budgets = nil // la baseline tiene solo gli importi
	}
	return b, err
}

// writeBaseline scrive la nuova baseline (numero successivo) e aggiorna la data di baseline
// delle milestone. crID è la change request approvata da cui nasce ("" per la prima).
func writeBaseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID, crID, note string, sn snapshot) (string, int, error) {
	var number int
	if err := tx.QueryRow(ctx, "select coalesce(max(number), 0) + 1 from plan_baselines where project_id = $1", projectID).Scan(&number); err != nil {
		return "", 0, err
	}
	var cur, approved, forecast any
	if sn.Budget != nil {
		cur, approved, forecast = sn.Budget.Currency, sn.Budget.ApprovedCents, sn.Budget.ForecastCents
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into plan_baselines (tenant_id, project_id, number, note, change_request_id, start_date, end_date,
		currency, approved_cents, forecast_cents, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5::date, $6::date, $7, $8, $9, $10) returning id::text`,
		projectID, number, strings.TrimSpace(note), nullable(crID), sn.Start, sn.End, cur, approved, forecast, nullable(actor.MemberID)).Scan(&id); err != nil {
		return "", 0, err
	}
	for _, it := range sn.Items {
		if _, err := tx.Exec(ctx, `insert into plan_baseline_items (tenant_id, baseline_id, item_id, kind, key, title, phase_id, phase_name,
			start_date, end_date, estimate_effort_days)
			select app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8::date, $9::date, $10
			where exists (select 1 from elements where id = $2)`,
			id, it.ID, it.Kind, it.Key, it.Title, it.PhaseID, it.Phase, it.Start, it.End, it.EffortDays); err != nil {
			return "", 0, err
		}
	}
	// La data di baseline della milestone (ext_milestone.baseline_date, prevista dallo schema).
	if _, err := tx.Exec(ctx, `update ext_milestone m set baseline_date = bi.end_date from plan_baseline_items bi
		where bi.baseline_id = $1 and bi.item_id = m.element_id and m.project_id = $2`, id, projectID); err != nil {
		return "", 0, err
	}
	if err := engine.AddActivity(ctx, tx, projectID, actor.MemberID, "plan", "baseline", map[string]any{"number": number}, nil); err != nil {
		return "", 0, err
	}
	return id, number, audit.Append(ctx, tx, audit.Event{Action: "plan.baseline", EntityType: TypeProject, EntityID: projectID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"baseline_id": id, "number": number, "change_request_id": crID, "items": len(sn.Items)}})
}

// loadBaseline legge la baseline in vigore del piano e le date delle voci in baseline.
func loadBaseline(ctx context.Context, tx pgx.Tx, p *Plan) error {
	b := &BaselineHead{}
	var start, end *time.Time
	var cur *string
	var approved, forecast *int64
	err := tx.QueryRow(ctx, `select b.id::text, b.number, b.start_date::timestamptz, b.end_date::timestamptz,
		to_char(b.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), m.display_name, b.currency, b.approved_cents, b.forecast_cents
		from plan_baselines b left join members m on m.tenant_id = b.tenant_id and m.id = b.created_by
		where b.project_id = $1 order by b.number desc limit 1`, p.Project.ID).
		Scan(&b.ID, &b.Number, &start, &end, &b.CreatedAt, &b.CreatedBy, &cur, &approved, &forecast)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	b.Start, b.End = dateStr(start), dateStr(end)
	if cur != nil {
		b.budget = &Budget{Currency: *cur, ApprovedCents: approved, ForecastCents: forecast}
	}
	p.Baseline = b
	rows, err := tx.Query(ctx, `select item_id::text, start_date::timestamptz, end_date::timestamptz, kind, key, title
		from plan_baseline_items where baseline_id = $1 order by end_date nulls last, key`, b.ID)
	if err != nil {
		return err
	}
	type bd struct{ s, e *string }
	dates := map[string]bd{}
	for rows.Next() {
		var id, kind, key, title string
		var s, e *time.Time
		if err := rows.Scan(&id, &s, &e, &kind, &key, &title); err != nil {
			rows.Close()
			return err
		}
		dates[id] = bd{dateStr(s), dateStr(e)}
		if kind == TypeWorkPackage || kind == TypeMilestone {
			b.scope = append(b.scope, BaselineItem{ID: id, Kind: kind, Key: key, Title: title, End: dateStr(e)})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range p.Items {
		it := &p.Items[i]
		if d, ok := dates[it.ID]; ok {
			it.InBaseline, it.BaselineStart, it.BaselineEnd = true, d.s, d.e
		}
	}
	return nil
}

// shift è lo scostamento in giorni lavorativi tra la data di baseline e quella prevista:
// positivo se la prevista è più tardi.
func shift(base, cur time.Time) int {
	switch {
	case cur.After(base):
		return calendar.Workdays(base.AddDate(0, 0, 1), cur)
	case cur.Before(base):
		return -calendar.Workdays(cur.AddDate(0, 0, 1), base)
	}
	return 0
}

// computeItemVariance: scostamento della fine di ogni voce dalla baseline.
func computeItemVariance(p *Plan) {
	for i := range p.Items {
		it := &p.Items[i]
		b, e := parse(it.BaselineEnd), parse(it.End)
		if b == nil || e == nil {
			continue
		}
		d := shift(*b, *e)
		it.VarianceDays = &d
	}
}

// CreateBaseline fissa la prima baseline del piano. Le successive nascono solo da una change
// request approvata: qui si rifiutano.
func (s *Service) CreateBaseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, note string) (*Baseline, error) {
	proj, err := s.planProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if len(note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	// Una sola richiesta alla volta: la riga del progetto fa da serratura.
	if _, err := tx.Exec(ctx, "select 1 from elements where id = $1 for update", proj.ID); err != nil {
		return nil, err
	}
	var has bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from plan_baselines where project_id = $1)", proj.ID).Scan(&has); err != nil {
		return nil, err
	}
	if has {
		return nil, engine.InvalidCode("validation.projects.baseline_exists", "baseline", "il piano ha già una baseline: per cambiarla serve una change request")
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	sn := snapshotOf(p)
	if len(sn.Items) == 0 {
		return nil, engine.InvalidCode("validation.projects.baseline_empty", "baseline", "il piano è vuoto: aggiungi almeno una voce prima di fissare la baseline")
	}
	if sn.Budget, err = s.budgetSnapshot(ctx, tx, p); err != nil {
		return nil, err
	}
	id, _, err := writeBaseline(ctx, tx, actor, proj.ID, "", note, sn)
	if err != nil {
		return nil, err
	}
	return s.baseline(ctx, tx, actor, proj, id)
}

// Baselines elenca le baseline del progetto, dalla più recente, senza le voci.
func (s *Service) Baselines(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]Baseline, error) {
	proj, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select id::text from plan_baselines where project_id = $1 order by number desc`, proj.ID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]Baseline, 0, len(ids))
	for i, id := range ids {
		b, err := s.readBaseline(ctx, tx, actor, proj, id, false)
		if err != nil {
			return nil, err
		}
		b.Current = i == 0
		out = append(out, *b)
	}
	return out, nil
}

// GetBaseline restituisce una baseline con le sue voci.
func (s *Service) GetBaseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Baseline, error) {
	var projectID string
	if err := tx.QueryRow(ctx, "select project_id::text from plan_baselines where id::text = $1", id).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	proj, err := s.project(ctx, tx, actor, projectID)
	if err != nil {
		return nil, err
	}
	return s.baseline(ctx, tx, actor, proj, id)
}

func (s *Service) baseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element, id string) (*Baseline, error) {
	b, err := s.readBaseline(ctx, tx, actor, proj, id, true)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, "select not exists (select 1 from plan_baselines where project_id = $1 and number > $2)", proj.ID, b.Number).Scan(&b.Current)
	return b, err
}

// readBaseline legge la baseline con i limiti di visibilità dell'attore: le voci che non può
// leggere perdono titolo e fase, il budget compare solo a chi vede quello del progetto.
func (s *Service) readBaseline(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element, id string, withItems bool) (*Baseline, error) {
	b := &Baseline{}
	var start, end *time.Time
	var cur *string
	var approved, forecast *int64
	var crID, crKey, crTitle *string
	err := tx.QueryRow(ctx, `select b.id::text, b.number, b.note, b.start_date::timestamptz, b.end_date::timestamptz,
		to_char(b.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), m.display_name, b.currency, b.approved_cents, b.forecast_cents,
		b.change_request_id::text, cs.key || '-' || c.number, c.title
		from plan_baselines b
		left join members m on m.tenant_id = b.tenant_id and m.id = b.created_by
		left join elements c on c.tenant_id = b.tenant_id and c.id = b.change_request_id
		left join spaces cs on cs.tenant_id = c.tenant_id and cs.id = c.space_id
		where b.id::text = $1 and b.project_id = $2`, id, proj.ID).
		Scan(&b.ID, &b.Number, &b.Note, &start, &end, &b.CreatedAt, &b.CreatedBy, &cur, &approved, &forecast, &crID, &crKey, &crTitle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Start, b.End = dateStr(start), dateStr(end)
	r := s.reader(actor)
	if crID != nil && crKey != nil {
		ref := &Ref{ID: *crID, Key: *crKey, Title: deref(crTitle)}
		if ok, err := r.can(ctx, tx, *crID); err != nil {
			return nil, err
		} else if !ok {
			ref.Title = ""
		}
		b.ChangeRequest = ref
	}
	if cur != nil {
		if ok, err := s.budgetVisible(ctx, tx, actor, proj); err != nil {
			return nil, err
		} else if ok {
			b.Budget = &Budget{Currency: *cur, ApprovedCents: approved, ForecastCents: forecast}
		}
	}
	if !withItems {
		return b, nil
	}
	rows, err := tx.Query(ctx, `select item_id::text, kind, key, title, phase_id::text, phase_name, start_date::timestamptz, end_date::timestamptz,
		estimate_effort_days::float8 from plan_baseline_items where baseline_id = $1 order by start_date nulls last, key`, b.ID)
	if err != nil {
		return nil, err
	}
	b.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (BaselineItem, error) {
		var it BaselineItem
		var s, e *time.Time
		err := row.Scan(&it.ID, &it.Kind, &it.Key, &it.Title, &it.PhaseID, &it.Phase, &s, &e, &it.EffortDays)
		it.Start, it.End = dateStr(s), dateStr(e)
		return it, err
	})
	if err != nil {
		return nil, err
	}
	for i := range b.Items {
		it := &b.Items[i]
		ok, err := r.can(ctx, tx, it.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			it.Restricted, it.Title = true, ""
		}
	}
	return b, nil
}

// BudgetVisibleTo dice se l'attore vede il budget del progetto (lo usa lo status report).
func (s *Service) BudgetVisibleTo(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) (bool, error) {
	return s.budgetVisible(ctx, tx, actor, &engine.Element{ID: projectID})
}

// budgetRoles vedono il budget del progetto oltre a chi lo pianifica (progetti.md §5): sponsor,
// business owner, PMO, program manager e responsabile operations. Il team e il contatto del
// fornitore no.
var budgetRoles = []string{"sponsor", "business_owner", "pmo", "program_manager", "operations_manager"}

// budgetVisible: l'attore vede il budget del progetto se ha finance.read, se pianifica il progetto,
// se ha sul progetto (o sul suo programma) uno dei ruoli che lo vedono, oppure se legge una delle
// richieste confluite (lo stesso criterio dei totali della richiesta, che sono aggregati).
func (s *Service) budgetVisible(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element) (bool, error) {
	if s.Budget == nil {
		return false, nil
	}
	if actor.Has("finance.read") {
		return true, nil
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || ok {
		return ok, err
	}
	if ok, err := engine.HasRole(ctx, tx, proj.ID, actor.MemberID, budgetRoles); err != nil || ok {
		return ok, err
	}
	var programID *string
	if err := tx.QueryRow(ctx, "select program_id::text from ext_project where element_id = $1", proj.ID).Scan(&programID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if programID != nil {
		if ok, err := engine.HasRole(ctx, tx, *programID, actor.MemberID, []string{"program_manager", "sponsor"}); err != nil || ok {
			return ok, err
		}
	}
	rows, err := tx.Query(ctx, "select request_id::text from project_requests where project_id = $1", proj.ID)
	if err != nil {
		return false, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if ok, err := s.eng.CanRead(ctx, tx, actor, id); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
