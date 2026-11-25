package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Sections sono i testi del report (markdown).
type Sections struct {
	Summary   string            `json:"summary"`   // sintesi
	Done      string            `json:"done"`      // attività fatte nel periodo
	Next      string            `json:"next"`      // prossime attività
	Risks     string            `json:"risks"`     // rischi e problemi
	Decisions string            `json:"decisions"` // richieste di decisione
	Notes     map[string]string `json:"notes"`     // nota per semaforo: schedule, cost, scope, risk
}

// Report è uno status report.
type Report struct {
	ID             string     `json:"id"`
	ElementID      string     `json:"element_id"`
	ElementKey     string     `json:"element_key"`
	ElementTitle   string     `json:"element_title"`
	PeriodStart    *string    `json:"period_start"`
	PeriodEnd      string     `json:"period_end"`
	Status         string     `json:"status"` // draft | submitted
	Overall        string     `json:"overall"`
	Schedule       string     `json:"schedule"`
	Cost           string     `json:"cost"`
	Scope          string     `json:"scope"`
	Risk           string     `json:"risk"`
	Sections       Sections   `json:"sections"`
	Data           Snapshot   `json:"data"`
	AuthorID       *string    `json:"author_id"`
	Author         *string    `json:"author"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	AcknowledgedBy *string    `json:"acknowledged_by"`
	Acknowledger   *string    `json:"acknowledged_by_name"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// PrevID è il report inviato precedente (per il confronto).
	PrevID         *string `json:"prev_id"`
	CanEdit        bool    `json:"can_edit"`
	CanAcknowledge bool    `json:"can_acknowledge"`
}

const selectReport = `
	select r.id, r.element_id, coalesce(sp.key || '-' || e.number, ''), e.title, r.period_start, r.period_end, r.status,
	       r.overall, coalesce(r.schedule, 'green'), coalesce(r.cost, 'green'), coalesce(r.scope, 'green'), coalesce(r.risk, 'green'),
	       r.summary, r.data, r.author_id, au.display_name, r.submitted_at, r.acknowledged_by, ak.display_name, r.acknowledged_at,
	       r.version, r.created_at, r.updated_at,
	       (select p.id from status_reports p where p.element_id = r.element_id and p.status = 'submitted' and p.period_end < r.period_end
	        order by p.period_end desc limit 1)
	from status_reports r
	join elements e on e.tenant_id = r.tenant_id and e.id = r.element_id
	join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
	left join members au on au.tenant_id = r.tenant_id and au.id = r.author_id
	left join members ak on ak.tenant_id = r.tenant_id and ak.id = r.acknowledged_by`

func scanReport(row pgx.Row) (*Report, error) {
	r := &Report{}
	var ps *time.Time
	var pe time.Time
	var summary, data []byte
	if err := row.Scan(&r.ID, &r.ElementID, &r.ElementKey, &r.ElementTitle, &ps, &pe, &r.Status, &r.Overall, &r.Schedule, &r.Cost,
		&r.Scope, &r.Risk, &summary, &data, &r.AuthorID, &r.Author, &r.SubmittedAt, &r.AcknowledgedBy, &r.Acknowledger, &r.AcknowledgedAt,
		&r.Version, &r.CreatedAt, &r.UpdatedAt, &r.PrevID); err != nil {
		return nil, err
	}
	r.PeriodStart, r.PeriodEnd = dayPtr(ps), day(pe)
	if len(summary) > 0 {
		if err := json.Unmarshal(summary, &r.Sections); err != nil {
			return nil, err
		}
	}
	if r.Sections.Notes == nil {
		r.Sections.Notes = map[string]string{}
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &r.Data); err != nil {
			return nil, err
		}
	}
	if r.Data.ReviewsLate == nil {
		r.Data.ReviewsLate = []LateReview{}
	}
	if r.Data.ImpactsBlocked == nil {
		r.Data.ImpactsBlocked = []BlockedImpact{}
	}
	if r.Data.Schedule.Late == nil {
		r.Data.Schedule.Late = []TaskRef{}
	}
	return r, nil
}

// request: la richiesta letta dall'attore. Gli status report sono interni: servono i
// permessi di lettura del tenant (il richiedente del portale non li vede).
func (s *Service) request(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrNotFound
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != requestType {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// editorRoles scrivono i report della richiesta (oltre a chi ha element.write).
var editorRoles = []string{"project_manager", "demand_manager", "pmo"}

func (s *Service) canEdit(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) (bool, error) {
	if actor.Has(rbac.ElementWrite) {
		return true, nil
	}
	return engine.HasRole(ctx, tx, elementID, actor.MemberID, editorRoles)
}

// canAcknowledge: sponsor e PMO della richiesta, o il governo del portafoglio, prendono visione.
func (s *Service) canAcknowledge(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) (bool, error) {
	if actor.Has(rbac.PortfolioAdmin) {
		return true, nil
	}
	return engine.HasRole(ctx, tx, elementID, actor.MemberID, []string{"sponsor", "pmo"})
}

// Schedule è la periodicità della richiesta con la prossima scadenza.
type Schedule struct {
	Frequency        *string  `json:"frequency"` // null = quella del tenant
	EffectiveFreq    string   `json:"effective_frequency"`
	NextDue          *string  `json:"next_due"`
	Overdue          bool     `json:"overdue"`
	Expected         bool     `json:"expected"` // la richiesta è in una fase in cui il report è atteso
	LastOverall      *string  `json:"last_overall"`
	LastPeriodEnd    *string  `json:"last_period_end"`
	TenantFrequency  string   `json:"tenant_frequency"`
	ReminderDays     int      `json:"reminder_days"`
	ExpectedInPhases []string `json:"expected_in_phases"`
}

func (s *Service) schedule(ctx context.Context, tx pgx.Tx, el *engine.Element, st Settings, today time.Time) (Schedule, error) {
	sc := Schedule{TenantFrequency: st.Frequency, EffectiveFreq: st.Frequency, ReminderDays: st.ReminderDays, ExpectedInPhases: st.Phases}
	var next, last *time.Time
	err := tx.QueryRow(ctx, `select frequency, next_due, last_overall, last_period_end from status_report_schedules where element_id = $1`, el.ID).
		Scan(&sc.Frequency, &next, &sc.LastOverall, &last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return sc, err
	}
	if sc.Frequency != nil {
		sc.EffectiveFreq = *sc.Frequency
	}
	sc.NextDue, sc.LastPeriodEnd = dayPtr(next), dayPtr(last)
	for _, p := range st.Phases {
		if p == el.State {
			sc.Expected = el.ResolvedAt == nil
		}
	}
	sc.Overdue = sc.Expected && next != nil && next.Before(today)
	return sc, nil
}

// List è lo storico dei report della richiesta (dal più recente) con la periodicità.
type List struct {
	Items    []*Report `json:"items"`
	Schedule Schedule  `json:"schedule"`
	CanEdit  bool      `json:"can_edit"`
}

func (s *Service) List(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*List, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &List{Items: []*Report{}}
	if out.CanEdit, err = s.canEdit(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	// Le bozze le vede solo chi può scriverle.
	rows, err := tx.Query(ctx, selectReport+` where r.element_id = $1 and (r.status = 'submitted' or $2) order by r.period_end desc`, el.ID, out.CanEdit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ack, err := s.canAcknowledge(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range out.Items {
		r.CanEdit = out.CanEdit && r.Status == "draft"
		r.CanAcknowledge = ack && r.Status == "submitted" && r.AcknowledgedAt == nil && (r.AuthorID == nil || *r.AuthorID != actor.MemberID)
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	c, err := s.clock(ctx, tx)
	if err != nil {
		return nil, err
	}
	out.Schedule, err = s.schedule(ctx, tx, el, st, c.today)
	return out, err
}

// Get legge un report (le bozze solo per chi può scriverle).
func (s *Service) Get(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Report, error) {
	if !uuidOK(id) {
		return nil, engine.ErrNotFound
	}
	r, err := scanReport(tx.QueryRow(ctx, selectReport+` where r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.request(ctx, tx, actor, r.ElementID); err != nil {
		return nil, err
	}
	edit, err := s.canEdit(ctx, tx, actor, r.ElementID)
	if err != nil {
		return nil, err
	}
	if r.Status == "draft" && !edit {
		return nil, engine.ErrNotFound
	}
	r.CanEdit = edit && r.Status == "draft"
	ack, err := s.canAcknowledge(ctx, tx, actor, r.ElementID)
	if err != nil {
		return nil, err
	}
	r.CanAcknowledge = ack && r.Status == "submitted" && r.AcknowledgedAt == nil && (r.AuthorID == nil || *r.AuthorID != actor.MemberID)
	return r, nil
}

func uuidOK(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
	}
	return true
}

// Prefill è la bozza proposta dai dati, senza salvarla.
type Prefill struct {
	PeriodStart string   `json:"period_start"`
	PeriodEnd   string   `json:"period_end"`
	Sections    Sections `json:"sections"`
	Data        Snapshot `json:"data"`
}

// period: il periodo del nuovo report, dal giorno dopo l'ultimo inviato.
func (s *Service) period(ctx context.Context, tx pgx.Tx, el *engine.Element, end time.Time, freq string) (time.Time, error) {
	var last *time.Time
	if err := tx.QueryRow(ctx, `select max(period_end) from status_reports where element_id = $1 and status = 'submitted' and period_end < $2`,
		el.ID, end).Scan(&last); err != nil {
		return time.Time{}, err
	}
	if last != nil {
		return last.AddDate(0, 0, 1), nil
	}
	switch freq {
	case "weekly":
		return end.AddDate(0, 0, -6), nil
	case "monthly":
		return end.AddDate(0, -1, 1), nil
	default:
		return end.AddDate(0, 0, -13), nil
	}
}

func (s *Service) prefill(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, endS string) (*Prefill, error) {
	c, err := s.clock(ctx, tx)
	if err != nil {
		return nil, err
	}
	end := c.today
	if endS != "" {
		if end, err = parseDay("period_end", endS); err != nil {
			return nil, err
		}
	} else {
		// Se oggi c'è già un report, il nuovo chiude il giorno dopo l'ultimo.
		var last *time.Time
		if err := tx.QueryRow(ctx, `select max(period_end) from status_reports where element_id = $1`, el.ID).Scan(&last); err != nil {
			return nil, err
		}
		if last != nil && !last.Before(end) {
			end = last.AddDate(0, 0, 1)
		}
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	sc, err := s.schedule(ctx, tx, el, st, c.today)
	if err != nil {
		return nil, err
	}
	start, err := s.period(ctx, tx, el, end, sc.EffectiveFreq)
	if err != nil {
		return nil, err
	}
	sn, err := s.snapshot(ctx, tx, actor, el, end)
	if err != nil {
		return nil, err
	}
	sec, err := s.prefillText(ctx, tx, el, sn, start, end, sc.EffectiveFreq)
	if err != nil {
		return nil, err
	}
	sec.Notes = map[string]string{}
	for dim, why := range sn.Suggested.Reasons {
		sec.Notes[dim] = strings.Join(why, "; ")
	}
	return &Prefill{PeriodStart: day(start), PeriodEnd: day(end), Sections: sec, Data: *sn}, nil
}

// Preview restituisce la precompilazione senza salvare.
func (s *Service) Preview(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, end string) (*Prefill, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	return s.prefill(ctx, tx, actor, el, end)
}

// Create apre la bozza del report del periodo, precompilata dai dati.
func (s *Service) Create(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, end string) (*Report, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canEdit(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	var drafts int
	if err := tx.QueryRow(ctx, `select count(*) from status_reports where element_id = $1 and status = 'draft'`, el.ID).Scan(&drafts); err != nil {
		return nil, err
	}
	if drafts > 0 {
		return nil, engine.Duplicate("status", "c'è già una bozza: completala o eliminala")
	}
	p, err := s.prefill(ctx, tx, actor, el, end)
	if err != nil {
		return nil, err
	}
	var dup bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from status_reports where element_id = $1 and period_end = $2)`, el.ID, p.PeriodEnd).Scan(&dup); err != nil {
		return nil, err
	}
	if dup {
		return nil, engine.Duplicate("period_end", "esiste già un report con fine periodo %s", p.PeriodEnd)
	}
	id := db.NewID()
	sum, _ := json.Marshal(p.Sections)
	data, _ := json.Marshal(p.Data)
	g := p.Data.Suggested
	if _, err := tx.Exec(ctx, `insert into status_reports (tenant_id, id, element_id, period_start, period_end, status, overall, schedule, cost, scope, risk,
		summary, data, author_id) values (app_tenant(), $1, $2, $3, $4, 'draft', $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, el.ID, p.PeriodStart, p.PeriodEnd, g.Overall, g.Schedule, g.Cost, g.Scope, g.Risk, sum, data, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "status_report.create", EntityType: "status_report", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element": el.ID, "period_end": p.PeriodEnd}}); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, actor, id)
}

// Input è la modifica di una bozza: solo i campi presenti cambiano.
type Input struct {
	Version     int       `json:"version"`
	PeriodStart *string   `json:"period_start"`
	PeriodEnd   *string   `json:"period_end"`
	Overall     *string   `json:"overall"`
	Schedule    *string   `json:"schedule"`
	Cost        *string   `json:"cost"`
	Scope       *string   `json:"scope"`
	Risk        *string   `json:"risk"`
	Sections    *Sections `json:"sections"`
}

func validRAG(field string, v *string) error {
	if v != nil {
		if _, ok := ragRank[*v]; !ok {
			return engine.InvalidValue(field, "semaforo non valido (green, amber, red)")
		}
	}
	return nil
}

// lockDraft legge e blocca la bozza per la scrittura.
func (s *Service) lockDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Report, error) {
	r, err := s.Get(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if !r.CanEdit {
		if r.Status != "draft" {
			return nil, engine.InvalidCode("validation.report.status_sent", "status", "il report è già stato inviato e non si modifica")
		}
		return nil, engine.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `select 1 from status_reports where id = $1 for update`, id); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) Update(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in Input) (*Report, error) {
	r, err := s.lockDraft(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if in.Version != r.Version {
		return nil, engine.ErrConflict
	}
	for f, v := range map[string]*string{"overall": in.Overall, "schedule": in.Schedule, "cost": in.Cost, "scope": in.Scope, "risk": in.Risk} {
		if err := validRAG(f, v); err != nil {
			return nil, err
		}
	}
	start, end := r.PeriodStart, r.PeriodEnd
	if in.PeriodEnd != nil {
		t, err := parseDay("period_end", *in.PeriodEnd)
		if err != nil {
			return nil, err
		}
		end = day(t)
	}
	if in.PeriodStart != nil {
		if *in.PeriodStart == "" {
			start = nil
		} else {
			t, err := parseDay("period_start", *in.PeriodStart)
			if err != nil {
				return nil, err
			}
			v := day(t)
			start = &v
		}
	}
	if start != nil && *start > end {
		return nil, engine.InvalidCode("validation.report.status_period_inverted", "period_start", "l'inizio del periodo è dopo la fine")
	}
	sec := r.Sections
	if in.Sections != nil {
		sec = *in.Sections
		if sec.Notes == nil {
			sec.Notes = map[string]string{}
		}
		for _, t := range []string{sec.Summary, sec.Done, sec.Next, sec.Risks, sec.Decisions} {
			if len(t) > 20000 {
				return nil, engine.TooLong("sections", 20000, "testo troppo lungo (massimo 20.000 caratteri per sezione)")
			}
		}
	}
	pick := func(v *string, cur string) string {
		if v != nil {
			return *v
		}
		return cur
	}
	sum, _ := json.Marshal(sec)
	_, err = tx.Exec(ctx, `update status_reports set period_start = $2, period_end = $3, overall = $4, schedule = $5, cost = $6, scope = $7, risk = $8,
		summary = $9, version = version + 1, updated_at = now() where id = $1`,
		id, start, end, pick(in.Overall, r.Overall), pick(in.Schedule, r.Schedule), pick(in.Cost, r.Cost), pick(in.Scope, r.Scope),
		pick(in.Risk, r.Risk), sum)
	if err != nil {
		if strings.Contains(err.Error(), "status_reports_tenant_id_element_id_period_end_key") || strings.Contains(err.Error(), "duplicate key") {
			return nil, engine.Duplicate("period_end", "esiste già un report con fine periodo %s", end)
		}
		return nil, err
	}
	return s.Get(ctx, tx, actor, id)
}

// Refresh ricalcola la fotografia dei dati della bozza (i testi restano).
func (s *Service) Refresh(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Report, error) {
	r, err := s.lockDraft(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	el, err := s.request(ctx, tx, actor, r.ElementID)
	if err != nil {
		return nil, err
	}
	end, _ := parseDay("period_end", r.PeriodEnd)
	sn, err := s.snapshot(ctx, tx, actor, el, end)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(sn)
	if _, err := tx.Exec(ctx, `update status_reports set data = $2, version = version + 1, updated_at = now() where id = $1`, id, data); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, actor, id)
}

// Delete elimina una bozza.
func (s *Service) Delete(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	r, err := s.lockDraft(ctx, tx, actor, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from status_reports where id = $1 and status = 'draft'`, id); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "status_report.delete", EntityType: "status_report", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element": r.ElementID}})
}

// Submit invia il report: da qui è immutabile. Aggiorna la periodicità (prossima
// scadenza) e avvisa chi segue la richiesta (sponsor, PMO, osservatori).
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Report, error) {
	r, err := s.lockDraft(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Sections.Summary) == "" {
		return nil, engine.Required("sections", "scrivi la sintesi prima di inviare")
	}
	if _, err := tx.Exec(ctx, `update status_reports set status = 'submitted', submitted_at = now(), author_id = coalesce(author_id, $2),
		version = version + 1, updated_at = now() where id = $1`, id, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	var freq *string
	if err := tx.QueryRow(ctx, `select frequency from status_report_schedules where element_id = $1`, r.ElementID).Scan(&freq); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	eff := st.Frequency
	if freq != nil {
		eff = *freq
	}
	end, _ := parseDay("period_end", r.PeriodEnd)
	var next any
	if eff != "off" {
		next = day(addPeriod(end, eff))
	}
	// La scadenza avanza solo se il report è il più recente.
	if _, err := tx.Exec(ctx, `insert into status_report_schedules (tenant_id, element_id, next_due, last_report_id, last_overall, last_period_end, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, now())
		on conflict (tenant_id, element_id) do update set
			next_due = case when status_report_schedules.last_period_end is null or excluded.last_period_end >= status_report_schedules.last_period_end
			                then excluded.next_due else status_report_schedules.next_due end,
			last_report_id = case when status_report_schedules.last_period_end is null or excluded.last_period_end >= status_report_schedules.last_period_end
			                then excluded.last_report_id else status_report_schedules.last_report_id end,
			last_overall = case when status_report_schedules.last_period_end is null or excluded.last_period_end >= status_report_schedules.last_period_end
			                then excluded.last_overall else status_report_schedules.last_overall end,
			last_period_end = greatest(status_report_schedules.last_period_end, excluded.last_period_end),
			reminded_for = null, updated_by = excluded.updated_by, updated_at = now()`,
		r.ElementID, next, id, r.Overall, r.PeriodEnd, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, r.ElementID, actor.MemberID, "report", "submitted",
		map[string]any{"overall": r.Overall, "period_end": r.PeriodEnd, "report": id}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "status_report.submit", EntityType: "status_report", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element": r.ElementID, "period_end": r.PeriodEnd, "overall": r.Overall}}); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReport, ElementID: r.ElementID, ActorID: actor.MemberID, Followers: true,
		Payload: map[string]any{"event": "submitted", "overall": r.Overall, "period_end": r.PeriodEnd}}); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, actor, id)
}

// Acknowledge: lo sponsor (o il PMO) prende visione del report inviato.
func (s *Service) Acknowledge(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Report, error) {
	r, err := s.Get(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if !r.CanAcknowledge {
		if r.Status != "submitted" || r.AcknowledgedAt != nil {
			return nil, engine.InvalidCode("validation.report.status_not_awaiting", "status", "il report non è in attesa di presa visione")
		}
		return nil, engine.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `update status_reports set acknowledged_by = $2, acknowledged_at = now() where id = $1`, id, actor.MemberID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "status_report.acknowledge", EntityType: "status_report", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element": r.ElementID}}); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, actor, id)
}

// ScheduleInput: periodicità della richiesta ("" = quella del tenant) e prossima scadenza.
type ScheduleInput struct {
	Frequency *string `json:"frequency"`
	NextDue   *string `json:"next_due"`
}

func (s *Service) SetSchedule(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ScheduleInput) (*List, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canEdit(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	var freq any
	if in.Frequency != nil && *in.Frequency != "" {
		if _, ok := frequencies[*in.Frequency]; !ok {
			return nil, engine.InvalidValue("frequency", "periodicità non ammessa (weekly, biweekly, monthly, off)")
		}
		freq = *in.Frequency
	}
	var next any
	if in.NextDue != nil && *in.NextDue != "" {
		t, err := parseDay("next_due", *in.NextDue)
		if err != nil {
			return nil, err
		}
		next = day(t)
	}
	if freq == "off" {
		next = nil
	}
	if _, err := tx.Exec(ctx, `insert into status_report_schedules (tenant_id, element_id, frequency, next_due, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, now())
		on conflict (tenant_id, element_id) do update set frequency = excluded.frequency,
			next_due = case when $5 then excluded.next_due else status_report_schedules.next_due end,
			reminded_for = null, updated_by = excluded.updated_by, updated_at = now()`,
		el.ID, freq, next, nullable(actor.MemberID), in.NextDue != nil || freq == "off"); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "status_report.schedule", EntityType: requestType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"frequency": freq, "next_due": next}}); err != nil {
		return nil, err
	}
	return s.List(ctx, tx, actor, el.ID)
}

// ---------------------------------------------------------------------------
// Confronto tra due report

type Change struct {
	Field string `json:"field"`
	Label string `json:"label"`
	From  any    `json:"from"`
	To    any    `json:"to"`
	// Trend: better, worse, same (per i semafori e i numeri con un verso).
	Trend string `json:"trend"`
}

type Comparison struct {
	From    *Report  `json:"from"`
	To      *Report  `json:"to"`
	Changes []Change `json:"changes"`
}

var dimLabels = []struct{ key, label string }{
	{"overall", "Stato generale"}, {"schedule", "Tempi"}, {"cost", "Costi"}, {"scope", "Perimetro"}, {"risk", "Rischi"},
}

// Compare confronta il report id con other (di solito il precedente).
func (s *Service) Compare(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, other string) (*Comparison, error) {
	b, err := s.Get(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if other == "" {
		if b.PrevID == nil {
			return nil, engine.InvalidCode("validation.report.status_no_previous", "with", "non c'è un report precedente da confrontare")
		}
		other = *b.PrevID
	}
	a, err := s.Get(ctx, tx, actor, other)
	if err != nil {
		return nil, err
	}
	if a.ElementID != b.ElementID {
		return nil, engine.InvalidCode("validation.report.status_other_requests", "with", "i report sono di richieste diverse")
	}
	if a.PeriodEnd > b.PeriodEnd {
		a, b = b, a
	}
	return &Comparison{From: a, To: b, Changes: changes(a, b)}, nil
}

func changes(a, b *Report) []Change {
	out := []Change{}
	rag := func(r *Report, k string) string {
		switch k {
		case "overall":
			return r.Overall
		case "schedule":
			return r.Schedule
		case "cost":
			return r.Cost
		case "scope":
			return r.Scope
		}
		return r.Risk
	}
	for _, d := range dimLabels {
		x, y := rag(a, d.key), rag(b, d.key)
		trend := "same"
		if ragRank[y] < ragRank[x] {
			trend = "better"
		} else if ragRank[y] > ragRank[x] {
			trend = "worse"
		}
		out = append(out, Change{Field: d.key, Label: d.label, From: x, To: y, Trend: trend})
	}
	num := func(field, label string, x, y *float64, higherBetter bool) {
		if x == nil && y == nil {
			return
		}
		trend := "same"
		if x != nil && y != nil && *x != *y {
			if (*y > *x) == higherBetter {
				trend = "better"
			} else {
				trend = "worse"
			}
		}
		out = append(out, Change{Field: field, Label: label, From: x, To: y, Trend: trend})
	}
	f := func(v int) *float64 { x := float64(v); return &x }
	fc := func(v *int64) *float64 {
		if v == nil {
			return nil
		}
		x := float64(*v)
		return &x
	}
	num("actual_pct", "Lavoro completato (%)", a.Data.Schedule.ActualPct, b.Data.Schedule.ActualPct, true)
	num("planned_pct", "Avanzamento atteso (%)", a.Data.Schedule.PlannedPct, b.Data.Schedule.PlannedPct, true)
	num("tasks_late", "Impatti in ritardo", f(a.Data.Schedule.TasksLate), f(b.Data.Schedule.TasksLate), false)
	num("consumption_pct", "Consumo del budget (%)", a.Data.Cost.ConsumptionPct, b.Data.Cost.ConsumptionPct, false)
	num("actual_cents", "Consuntivo", fc(&a.Data.Cost.ActualCents), fc(&b.Data.Cost.ActualCents), false)
	num("approved_cents", "Budget approvato", fc(a.Data.Cost.ApprovedCents), fc(b.Data.Cost.ApprovedCents), true)
	num("cpi", "CPI", a.Data.Cost.CPI, b.Data.Cost.CPI, true)
	num("spi", "SPI", a.Data.Cost.SPI, b.Data.Cost.SPI, true)
	num("reviews_late", "Valutazioni in ritardo", f(len(a.Data.ReviewsLate)), f(len(b.Data.ReviewsLate)), false)
	num("impacts_blocked", "Impatti bloccati", f(len(a.Data.ImpactsBlocked)), f(len(b.Data.ImpactsBlocked)), false)
	return out
}

// ---------------------------------------------------------------------------
// Export in Markdown

var ragIT = map[string]string{"green": "verde", "amber": "giallo", "red": "rosso"}

func euros(c int64, cur string) string {
	v := float64(c) / 100
	s := fmt.Sprintf("%.0f", v)
	// separatore delle migliaia all'italiana
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(ch)
	}
	out := b.String()
	if neg {
		out = "-" + out
	}
	if cur == "EUR" || cur == "" {
		return out + " €"
	}
	return out + " " + cur
}

// Markdown è il report in testo, per incollarlo in un'email o in un wiki.
func Markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Status report %s · %s\n\n", r.ElementKey, r.ElementTitle)
	period := r.PeriodEnd
	if r.PeriodStart != nil {
		period = *r.PeriodStart + " → " + r.PeriodEnd
	}
	fmt.Fprintf(&b, "Periodo: %s  \n", period)
	if r.Status == "draft" {
		b.WriteString("Stato: **bozza**  \n")
	} else if r.SubmittedAt != nil {
		who := ""
		if r.Author != nil {
			who = " da " + *r.Author
		}
		fmt.Fprintf(&b, "Inviato il %s%s  \n", r.SubmittedAt.Format("02/01/2006"), who)
	}
	if r.Data.Phase != "" {
		fmt.Fprintf(&b, "Fase: %s\n", r.Data.Phase)
	}
	b.WriteString("\n## Semafori\n\n| ambito | stato | nota |\n| --- | --- | --- |\n")
	for _, d := range dimLabels {
		v := map[string]string{"overall": r.Overall, "schedule": r.Schedule, "cost": r.Cost, "scope": r.Scope, "risk": r.Risk}[d.key]
		note := strings.ReplaceAll(r.Sections.Notes[d.key], "\n", " ")
		fmt.Fprintf(&b, "| %s | %s | %s |\n", d.label, strings.ToUpper(ragIT[v]), strings.ReplaceAll(note, "|", "/"))
	}
	sec := func(title, body string) {
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		if strings.TrimSpace(body) == "" {
			b.WriteString("—\n")
			return
		}
		b.WriteString(strings.TrimSpace(body) + "\n")
	}
	sec("Sintesi", r.Sections.Summary)
	sec("Attività svolte", r.Sections.Done)
	sec("Prossime attività", r.Sections.Next)
	sec("Rischi e problemi", r.Sections.Risks)
	sec("Richieste di decisione", r.Sections.Decisions)

	d := r.Data
	b.WriteString("\n## Dati al " + d.AsOf + "\n\n")
	if d.Schedule.ActualPct != nil {
		fmt.Fprintf(&b, "- Lavoro completato: %.0f%% (atteso %.0f%%)\n", *d.Schedule.ActualPct, *d.Schedule.PlannedPct)
	}
	fmt.Fprintf(&b, "- Impatti: %d, rilasciati %d, in ritardo %d\n", d.Schedule.Tasks, d.Schedule.TasksDone, d.Schedule.TasksLate)
	if d.Schedule.End != nil {
		fmt.Fprintf(&b, "- Fine pianificata: %s\n", *d.Schedule.End)
	}
	if d.Schedule.Deadline != nil {
		fmt.Fprintf(&b, "- Scadenza normativa: %s\n", *d.Schedule.Deadline)
	}
	if d.Cost.ApprovedCents != nil {
		fmt.Fprintf(&b, "- Budget approvato: %s\n", euros(*d.Cost.ApprovedCents, d.Cost.Currency))
	}
	fmt.Fprintf(&b, "- Consuntivo: %s; impegnato: %s\n", euros(d.Cost.ActualCents, d.Cost.Currency), euros(d.Cost.CommittedCents, d.Cost.Currency))
	if d.Cost.ConsumptionPct != nil {
		fmt.Fprintf(&b, "- Consumo del budget: %.0f%%\n", *d.Cost.ConsumptionPct)
	}
	if d.Cost.CPI != nil || d.Cost.SPI != nil {
		f := func(v *float64) string {
			if v == nil {
				return "—"
			}
			return fmt.Sprintf("%.2f", *v)
		}
		fmt.Fprintf(&b, "- Earned value: CPI %s, SPI %s\n", f(d.Cost.CPI), f(d.Cost.SPI))
	}
	fmt.Fprintf(&b, "- Valutazioni in ritardo: %d; impatti bloccati: %d\n", len(d.ReviewsLate), len(d.ImpactsBlocked))
	if r.AcknowledgedAt != nil && r.Acknowledger != nil {
		fmt.Fprintf(&b, "\nPresa visione: %s, %s\n", *r.Acknowledger, r.AcknowledgedAt.Format("02/01/2006"))
	}
	return b.String()
}
