package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Stati della seduta.
const (
	Planned   = "planned"   // pianificata
	Convened  = "convened"  // convocata
	Held      = "held"      // svolta
	Minuted   = "minuted"   // verbalizzata
	Cancelled = "cancelled" // annullata
)

// StatusNames sono le etichette italiane degli stati.
var StatusNames = map[string]string{Planned: "pianificata", Convened: "convocata", Held: "svolta", Minuted: "verbalizzata", Cancelled: "annullata"}

// Outcomes sono le decisioni ammesse su un punto dell'ordine del giorno.
var Outcomes = map[string]string{
	"approved":                 "approvata",
	"approved_with_conditions": "approvata con condizioni",
	"rejected":                 "respinta",
	"deferred":                 "rinviata",
	"informational":            "presa d'atto",
}

// Summary è una seduta nel calendario.
type Summary struct {
	ID          string    `json:"id"`
	BodyID      string    `json:"body_id"`
	Body        string    `json:"body"`
	BodyKind    string    `json:"body_kind"`
	Title       string    `json:"title"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Location    string    `json:"location"`
	Link        string    `json:"link"`
	Status      string    `json:"status"`
	Items       int       `json:"items"`
	Decided     int       `json:"decided"`
}

// Meetings elenca le sedute tra from e to (estremi facoltativi), eventualmente di un organo.
func (s *Service) Meetings(ctx context.Context, tx pgx.Tx, actor engine.Actor, from, to *time.Time, bodyID string) ([]Summary, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `
		select m.id, b.id, b.name, b.kind, m.title, m.scheduled_at, m.location, m.link, m.status,
		       (select count(*) from meeting_items i where i.tenant_id = m.tenant_id and i.meeting_id = m.id),
		       (select count(*) from meeting_items i where i.tenant_id = m.tenant_id and i.meeting_id = m.id and i.outcome is not null)
		from meetings m join governance_bodies b on b.tenant_id = m.tenant_id and b.id = m.body_id
		where ($1::timestamptz is null or m.scheduled_at >= $1) and ($2::timestamptz is null or m.scheduled_at < $2)
		  and ($3 = '' or m.body_id::text = $3)
		order by m.scheduled_at, m.id limit 500`, from, to, bodyID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Summary])
	if out == nil {
		out = []Summary{}
	}
	return out, err
}

// Item è un punto dell'ordine del giorno.
type Item struct {
	Ordinal       int        `json:"ordinal"`
	ElementID     string     `json:"element_id"`
	Key           string     `json:"key"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	StateCategory string     `json:"state_category"`
	IsMandatory   bool       `json:"is_mandatory"`
	AmountCents   *int64     `json:"amount_cents"`
	Score         *float64   `json:"score"`
	Proposed      bool       `json:"proposed"`    // proposto dal sistema
	StepActive    bool       `json:"step_active"` // c'è un passo di approvazione attivo dell'organo
	Outcome       *string    `json:"outcome"`
	Note          *string    `json:"note"`
	Conditions    *string    `json:"conditions"`
	DecidedBy     *string    `json:"decided_by"`
	DecidedAt     *time.Time `json:"decided_at"`
}

// Proposal è una richiesta che attende una decisione dell'organo e non è ancora in calendario.
type Proposal struct {
	ElementID   string    `json:"element_id"`
	Key         string    `json:"key"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	IsMandatory bool      `json:"is_mandatory"`
	AmountCents *int64    `json:"amount_cents"`
	Score       *float64  `json:"score"`
	RequestedAt time.Time `json:"requested_at"`
}

// Attendee è un membro dell'organo alla seduta.
type Attendee struct {
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
	Voting   bool   `json:"voting"`
	Present  bool   `json:"present"`
}

// Quorum: votanti presenti rispetto al richiesto.
type Quorum struct {
	Required int  `json:"required"`
	Present  int  `json:"present"`
	Voting   int  `json:"voting"`
	Reached  bool `json:"reached"`
}

// Meeting è il dettaglio della seduta.
type Meeting struct {
	ID                string     `json:"id"`
	BodyID            string     `json:"body_id"`
	Body              string     `json:"body"`
	BodyKind          string     `json:"body_kind"`
	Title             string     `json:"title"`
	ScheduledAt       time.Time  `json:"scheduled_at"`
	Location          string     `json:"location"`
	Link              string     `json:"link"`
	Status            string     `json:"status"`
	ConvenedAt        *time.Time `json:"convened_at"`
	HeldAt            *time.Time `json:"held_at"`
	ChairID           *string    `json:"chair_id"`
	Chair             *string    `json:"chair"`
	SecretaryID       *string    `json:"secretary_id"`
	Secretary         *string    `json:"secretary"`
	Items             []Item     `json:"items"`
	Proposals         []Proposal `json:"proposals"`
	Attendance        []Attendee `json:"attendance"`
	Quorum            Quorum     `json:"quorum"`
	Minutes           *string    `json:"minutes"`       // testo salvato (bozza o verbale approvato)
	MinutesDraft      string     `json:"minutes_draft"` // bozza generata dalle decisioni
	MinutesApprovedAt *time.Time `json:"minutes_approved_at"`
	MinutesApprovedBy *string    `json:"minutes_approved_by"`
	MinutesHash       *string    `json:"minutes_hash"`
	CanManage         bool       `json:"can_manage"`  // presidente, segretario o governo del portafoglio
	CanDecide         bool       `json:"can_decide"`  // presidente o segretario
	CanApprove        bool       `json:"can_approve"` // presidente: approva il verbale
	quorumSetting     *int
	localTime         time.Time
}

// head legge la seduta con l'organo e i ruoli dell'attore.
func (s *Service) head(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, lock bool) (*Meeting, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	q := `select m.id, b.id, b.name, b.kind, m.title, m.scheduled_at, m.location, m.link, m.status, m.convened_at, m.held_at,
		       b.chair_id, c.display_name, b.secretary_id, sec.display_name, m.minutes_text, m.minutes_approved_at, ma.display_name,
		       m.minutes_hash, b.quorum, (m.scheduled_at at time zone coalesce((select t.default_timezone from tenants t where t.id = m.tenant_id), 'Europe/Rome'))
		from meetings m join governance_bodies b on b.tenant_id = m.tenant_id and b.id = m.body_id
		left join members c on c.tenant_id = b.tenant_id and c.id = b.chair_id
		left join members sec on sec.tenant_id = b.tenant_id and sec.id = b.secretary_id
		left join members ma on ma.tenant_id = m.tenant_id and ma.id = m.minutes_approved_by
		where m.id = $1`
	if lock {
		q += " for update of m"
	}
	m := &Meeting{}
	err := tx.QueryRow(ctx, q, id).Scan(&m.ID, &m.BodyID, &m.Body, &m.BodyKind, &m.Title, &m.ScheduledAt, &m.Location, &m.Link, &m.Status,
		&m.ConvenedAt, &m.HeldAt, &m.ChairID, &m.Chair, &m.SecretaryID, &m.Secretary, &m.Minutes, &m.MinutesApprovedAt, &m.MinutesApprovedBy,
		&m.MinutesHash, &m.quorumSetting, &m.localTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	officer := (m.ChairID != nil && *m.ChairID == actor.MemberID) || (m.SecretaryID != nil && *m.SecretaryID == actor.MemberID)
	m.CanDecide = officer
	m.CanManage = officer || actor.Has(rbac.PortfolioAdmin)
	m.CanApprove = m.ChairID != nil && *m.ChairID == actor.MemberID
	return m, nil
}

// Meeting restituisce il dettaglio: ordine del giorno, proposte, presenze, quorum, verbale.
func (s *Service) Meeting(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, false)
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, tx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) fill(ctx context.Context, tx pgx.Tx, m *Meeting) error {
	rows, err := tx.Query(ctx, `
		select i.ordinal, e.id, coalesce(sp.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		       coalesce(d.is_mandatory, false), coalesce(
		         (select rq.amount_cents from approval_requests rq where rq.tenant_id = e.tenant_id and rq.element_id = e.id
		            order by rq.requested_at desc limit 1), d.estimate_high_cents),
		       d.score, i.proposed,
		       exists (select 1 from approval_step_assignees a
		         join approval_steps st on st.tenant_id = a.tenant_id and st.id = a.step_id and st.decision = 'pending'
		         join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id and rq.status = 'pending'
		         where a.tenant_id = e.tenant_id and a.body_id = $2 and rq.element_id = e.id),
		       i.outcome, i.note, i.conditions, dm.display_name, i.decided_at
		from meeting_items i
		join elements e on e.tenant_id = i.tenant_id and e.id = i.element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join ext_demand d on d.tenant_id = e.tenant_id and d.element_id = e.id
		left join members dm on dm.tenant_id = i.tenant_id and dm.id = i.decided_by
		where i.meeting_id = $1 order by i.ordinal, e.number`, m.ID, m.BodyID)
	if err != nil {
		return err
	}
	if m.Items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Item]); err != nil {
		return err
	}
	if m.Items == nil {
		m.Items = []Item{}
	}
	m.Proposals = []Proposal{}
	if m.Status != Minuted && m.Status != Cancelled {
		if m.Proposals, err = proposals(ctx, tx, m.BodyID, m.ID); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `select a.member_id, mb.display_name, a.voting, a.present from meeting_attendance a
		join members mb on mb.tenant_id = a.tenant_id and mb.id = a.member_id
		where a.meeting_id = $1 order by mb.display_name`, m.ID)
	if err != nil {
		return err
	}
	if m.Attendance, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Attendee]); err != nil {
		return err
	}
	if m.Attendance == nil {
		m.Attendance = []Attendee{}
	}
	for _, a := range m.Attendance {
		if a.Voting {
			m.Quorum.Voting++
			if a.Present {
				m.Quorum.Present++
			}
		}
	}
	m.Quorum.Required = quorumRequired(m.quorumSetting, m.Quorum.Voting)
	m.Quorum.Reached = m.Quorum.Voting > 0 && m.Quorum.Present >= m.Quorum.Required
	m.MinutesDraft = draftMinutes(m)
	return nil
}

// proposals: richieste con un passo di approvazione attivo assegnato all'organo, non
// ancora all'ordine del giorno di questa seduta né in attesa in un'altra seduta aperta.
func proposals(ctx context.Context, tx pgx.Tx, bodyID, meetingID string) ([]Proposal, error) {
	rows, err := tx.Query(ctx, `
		select e.id, coalesce(sp.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), coalesce(d.is_mandatory, false),
		       rq.amount_cents, d.score, rq.requested_at
		from approval_step_assignees a
		join approval_steps st on st.tenant_id = a.tenant_id and st.id = a.step_id and st.decision = 'pending'
		join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id and rq.status = 'pending'
		join elements e on e.tenant_id = rq.tenant_id and e.id = rq.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join ext_demand d on d.tenant_id = e.tenant_id and d.element_id = e.id
		where a.body_id = $1
		  and not exists (select 1 from meeting_items i where i.tenant_id = e.tenant_id and i.meeting_id = $2 and i.element_id = e.id)
		  and not exists (select 1 from meeting_items i join meetings om on om.tenant_id = i.tenant_id and om.id = i.meeting_id
		                  where i.tenant_id = e.tenant_id and i.element_id = e.id and i.outcome is null and om.id <> $2
		                    and om.status in ('planned','convened','held'))
		order by coalesce(d.is_mandatory, false) desc, d.score desc nulls last, rq.requested_at`, bodyID, meetingID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Proposal])
	if out == nil {
		out = []Proposal{}
	}
	return out, err
}

// MeetingInput crea o modifica una seduta.
type MeetingInput struct {
	BodyID      string `json:"body_id"`
	Title       string `json:"title"`
	ScheduledAt string `json:"scheduled_at"` // RFC 3339
	Location    string `json:"location"`
	Link        string `json:"link"`
	// Propose (default sì): alla creazione l'ordine del giorno parte dalle richieste in
	// attesa di una decisione dell'organo.
	Propose *bool `json:"propose"`
}

func (in *MeetingInput) parse() (time.Time, error) {
	in.Title, in.Location, in.Link = strings.TrimSpace(in.Title), strings.TrimSpace(in.Location), strings.TrimSpace(in.Link)
	t, err := time.Parse(time.RFC3339, in.ScheduledAt)
	if err != nil {
		return t, engine.InvalidCode("validation.governance.datetime_invalid", "scheduled_at", "data e ora non valide (RFC 3339, es. 2027-01-12T15:00:00+01:00)")
	}
	if in.Link != "" && !strings.HasPrefix(in.Link, "https://") && !strings.HasPrefix(in.Link, "http://") {
		return t, engine.InvalidCode("validation.governance.link_https", "link", "il collegamento deve iniziare con https://")
	}
	if len(in.Title) > 200 || len(in.Location) > 200 || len(in.Link) > 500 {
		return t, engine.InvalidCode("validation.governance.text_too_long", "title", "testo troppo lungo")
	}
	return t, nil
}

// CreateMeeting pianifica una seduta (presidente, segretario o governo del portafoglio)
// con la fotografia dei membri per le presenze e, di default, l'ordine del giorno proposto.
func (s *Service) CreateMeeting(ctx context.Context, tx pgx.Tx, actor engine.Actor, in MeetingInput) (*Meeting, error) {
	at, err := in.parse()
	if err != nil {
		return nil, err
	}
	b, err := s.body(ctx, tx, actor, in.BodyID)
	if err != nil {
		if errors.Is(err, engine.ErrNotFound) {
			return nil, engine.NotFound("body_id", "body", "organo inesistente")
		}
		return nil, err
	}
	if !b.CanManage {
		return nil, engine.ErrForbidden
	}
	if b.Archived {
		return nil, engine.InvalidCode("validation.governance.body_archived", "body_id", "l'organo è archiviato")
	}
	if in.Title == "" {
		in.Title = "Seduta del " + b.Name
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into meetings (tenant_id, id, body_id, scheduled_at, status, title, location, link, created_by)
		values (app_tenant(), $1, $2, $3, 'planned', $4, $5, $6, $7)`, id, b.ID, at, in.Title, in.Location, in.Link, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into meeting_attendance (tenant_id, meeting_id, member_id, voting, present)
		select app_tenant(), $1, e.member_id, e.voting, false from body_effective_members($2) e
		join members m on m.id = e.member_id and m.status = 'active'`, id, b.ID); err != nil {
		return nil, err
	}
	added := 0
	if in.Propose == nil || *in.Propose {
		ps, err := proposals(ctx, tx, b.ID, id)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if err := s.addItem(ctx, tx, actor, id, b.Name, at, p.ElementID, true); err != nil {
				return nil, err
			}
			added++
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.create", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"body_id": b.ID, "scheduled_at": at, "proposed_items": added}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// UpdateMeeting modifica titolo, data, luogo e collegamento finché la seduta non è svolta.
func (s *Service) UpdateMeeting(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in MeetingInput) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanManage {
		return nil, engine.ErrForbidden
	}
	if m.Status != Planned && m.Status != Convened {
		return nil, engine.InvalidParams("validation.governance.meeting_status_locked", "status", map[string]any{"status": StatusNames[m.Status]}, "la seduta è %s: data e luogo non si cambiano più", StatusNames[m.Status])
	}
	at, err := in.parse()
	if err != nil {
		return nil, err
	}
	if in.Title == "" {
		in.Title = m.Title
	}
	if _, err := tx.Exec(ctx, `update meetings set scheduled_at = $2, title = $3, location = $4, link = $5 where id = $1`,
		id, at, in.Title, in.Location, in.Link); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.update", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"scheduled_at": at}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// transitions ammessi tra gli stati della seduta (verbalizzata solo approvando il verbale).
var transitions = map[string][]string{
	Planned:  {Convened, Held, Cancelled},
	Convened: {Held, Cancelled},
}

// SetStatus convoca, segna come svolta o annulla la seduta.
func (s *Service) SetStatus(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, to string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanManage {
		return nil, engine.ErrForbidden
	}
	if to == Minuted {
		return nil, engine.InvalidCode("validation.governance.minutes_via_approval", "status", "la seduta diventa verbalizzata approvando il verbale")
	}
	if !slices.Contains(transitions[m.Status], to) {
		return nil, engine.InvalidValue("status", "passaggio non ammesso: da %s a %s", StatusNames[m.Status], or(StatusNames[to], to))
	}
	if m.Status == Planned && (to == Convened || to == Held) {
		// I membri effettivi si ricalcolano alla convocazione: i gruppi possono essere cambiati
		// dopo la creazione della seduta. Le presenze già segnate restano.
		if err := refreshAttendance(ctx, tx, id, m.BodyID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `update meetings set status = $2,
		convened_at = case when $2 = 'convened' then now() else convened_at end,
		held_at = case when $2 = 'held' then now() else held_at end where id = $1`, id, to); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.status", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"from": m.Status, "to": to}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// refreshAttendance riallinea l'elenco dei presenti ai membri effettivi dell'organo: aggiunge
// chi è entrato, toglie chi è uscito (se non era segnato presente) e aggiorna il diritto di voto.
func refreshAttendance(ctx context.Context, tx pgx.Tx, meetingID, bodyID string) error {
	if _, err := tx.Exec(ctx, `insert into meeting_attendance (tenant_id, meeting_id, member_id, voting, present)
		select app_tenant(), $1, e.member_id, e.voting, false from body_effective_members($2) e
		join members m on m.id = e.member_id and m.status = 'active'
		on conflict (tenant_id, meeting_id, member_id) do update set voting = excluded.voting`, meetingID, bodyID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from meeting_attendance a where a.meeting_id = $1 and not a.present
		and a.member_id not in (select e.member_id from body_effective_members($2) e)`, meetingID, bodyID)
	return err
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) addItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, meetingID, body string, at time.Time, elementID string, proposed bool) error {
	if _, err := tx.Exec(ctx, `insert into meeting_items (tenant_id, meeting_id, element_id, ordinal, proposed)
		values (app_tenant(), $1, $2, coalesce((select max(ordinal) from meeting_items where meeting_id = $1), 0) + 1, $3)`,
		meetingID, elementID, proposed); err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, elementID, actor.MemberID, "meeting", "agenda",
		map[string]any{"meeting_id": meetingID, "body": body, "scheduled_at": at}, nil)
}

// AddItem iscrive una richiesta all'ordine del giorno.
func (s *Service) AddItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, ref string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanManage {
		return nil, engine.ErrForbidden
	}
	if m.Status == Minuted || m.Status == Cancelled {
		return nil, engine.InvalidParams("validation.governance.agenda_closed", "status", map[string]any{"status": StatusNames[m.Status]}, "la seduta è %s: l'ordine del giorno è chiuso", StatusNames[m.Status])
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from meeting_items where meeting_id = $1 and element_id = $2)", id, el.ID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, engine.InvalidParams("validation.governance.agenda_item_present", "element", map[string]any{"key": el.Key}, "%s è già all'ordine del giorno", el.Key)
	}
	if err := s.addItem(ctx, tx, actor, id, m.Body, m.ScheduledAt, el.ID, false); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.item_add", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": el.ID}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// RemoveItem toglie un punto non ancora deciso.
func (s *Service) RemoveItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, ref string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanManage {
		return nil, engine.ErrForbidden
	}
	if m.Status == Minuted {
		return nil, engine.InvalidCode("validation.governance.agenda_minuted", "status", "la seduta è verbalizzata: l'ordine del giorno non cambia più")
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, "delete from meeting_items where meeting_id = $1 and element_id = $2 and outcome is null", id, el.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidParams("validation.governance.agenda_item_missing", "element", map[string]any{"key": el.Key}, "%s non è all'ordine del giorno o è già stato deciso", el.Key)
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.item_remove", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": el.ID}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// Presence è la presenza di un membro.
type Presence struct {
	MemberID string `json:"member_id"`
	Present  bool   `json:"present"`
}

// SetAttendance registra presenti e assenti (convocata o svolta).
func (s *Service) SetAttendance(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, list []Presence) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanManage {
		return nil, engine.ErrForbidden
	}
	if m.Status != Convened && m.Status != Held && m.Status != Planned {
		return nil, engine.InvalidParams("validation.governance.attendance_locked", "status", map[string]any{"status": StatusNames[m.Status]}, "la seduta è %s: le presenze non si cambiano più", StatusNames[m.Status])
	}
	for _, p := range list {
		tag, err := tx.Exec(ctx, "update meeting_attendance set present = $3 where meeting_id = $1 and member_id::text = $2", id, p.MemberID, p.Present)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, engine.InvalidCode("validation.governance.attendee_not_member", "attendance", "il membro non fa parte dell'organo")
		}
	}
	if err := s.fill(ctx, tx, m); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.attendance", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"present": m.Quorum.Present, "required": m.Quorum.Required, "reached": m.Quorum.Reached}}); err != nil {
		return nil, err
	}
	return m, nil
}

// DecisionInput è la decisione dell'organo su un punto.
type DecisionInput struct {
	Decision   string `json:"decision"` // approved | approved_with_conditions | rejected | deferred | informational
	Note       string `json:"note"`
	Conditions string `json:"conditions"`
}

// Decide registra la decisione su un punto dell'ordine del giorno. Se la richiesta ha un
// passo di approvazione attivo assegnato all'organo, la decisione lo chiude con
// approval.Service.Decide (attore: il presidente o il segretario), così la richiesta
// avanza da sola secondo la politica. Serve il quorum, salvo per la presa d'atto.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, ref string, in DecisionInput) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanDecide {
		return nil, engine.ErrForbidden
	}
	if m.Status != Held {
		return nil, engine.InvalidParams("validation.governance.decisions_after", "status", map[string]any{"status": StatusNames[m.Status]}, "le decisioni si registrano a seduta svolta (ora è %s)", StatusNames[m.Status])
	}
	if _, ok := Outcomes[in.Decision]; !ok {
		return nil, engine.InvalidValue("decision", "decisione non valida")
	}
	in.Note, in.Conditions = strings.TrimSpace(in.Note), strings.TrimSpace(in.Conditions)
	switch in.Decision {
	case "approved_with_conditions":
		if in.Conditions == "" {
			return nil, engine.Required("conditions", "indica le condizioni dell'approvazione")
		}
	case "rejected", "deferred":
		if in.Note == "" {
			return nil, engine.InvalidCode("validation.reason_required", "note", "serve una motivazione")
		}
	}
	if err := s.fill(ctx, tx, m); err != nil {
		return nil, err
	}
	if in.Decision != "informational" && !m.Quorum.Reached {
		return nil, engine.InvalidParams("validation.governance.quorum_missing", "quorum", map[string]any{"present": m.Quorum.Present, "required": m.Quorum.Required}, "quorum non raggiunto: presenti %d votanti su %d richiesti", m.Quorum.Present, m.Quorum.Required)
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var decided bool
	err = tx.QueryRow(ctx, "select outcome is not null from meeting_items where meeting_id = $1 and element_id = $2 for update", id, el.ID).Scan(&decided)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidParams("validation.governance.agenda_item_absent", "element", map[string]any{"key": el.Key}, "%s non è all'ordine del giorno", el.Key)
	}
	if err != nil {
		return nil, err
	}
	if decided {
		return nil, approval.ErrNotPending
	}
	// Il passo dell'organo sulla richiesta (il primo ancora da decidere).
	var stepID *string
	err = tx.QueryRow(ctx, `select st.id from approval_steps st
		join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id and rq.status = 'pending'
		where rq.element_id = $1 and st.approver_body_id = $2 and st.decision = 'pending'
		order by st.ordinal limit 1`, el.ID, m.BodyID).Scan(&stepID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if stepID != nil && in.Decision != "informational" {
		if err := s.approvals.Decide(ctx, tx, actor, *stepID, approval.DecideInput{
			Decision: in.Decision, Comment: in.Note, Conditions: in.Conditions, MeetingID: id,
		}); err != nil {
			return nil, err
		}
	}
	if in.Decision == "informational" {
		stepID = nil
	}
	if _, err := tx.Exec(ctx, `update meeting_items set outcome = $3, note = $4, conditions = $5, decided_by = $6, decided_at = now(), step_id = $7
		where meeting_id = $1 and element_id = $2`, id, el.ID, in.Decision, nullable(in.Note), nullable(in.Conditions), actor.MemberID, stepID); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "meeting", "decision",
		map[string]any{"meeting_id": id, "body": m.Body, "scheduled_at": m.ScheduledAt, "decision": in.Decision, "note": in.Note, "conditions": in.Conditions}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.meeting.decision", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": el.ID, "decision": in.Decision, "step_id": stepID, "present": m.Quorum.Present, "required": m.Quorum.Required}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// SaveMinutes salva la bozza del verbale (a seduta svolta, prima dell'approvazione).
func (s *Service) SaveMinutes(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, text string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanDecide {
		return nil, engine.ErrForbidden
	}
	if m.Status == Minuted {
		return nil, engine.InvalidCode("validation.governance.minutes_approved_locked", "status", "il verbale è approvato e non si può modificare")
	}
	if m.Status != Held {
		return nil, engine.InvalidCode("validation.governance.minutes_after_meeting", "status", "il verbale si scrive a seduta svolta")
	}
	text = strings.TrimSpace(text)
	if len(text) > 100_000 {
		return nil, engine.InvalidCode("validation.governance.text_too_long", "text", "verbale troppo lungo")
	}
	if _, err := tx.Exec(ctx, "update meetings set minutes_text = $2 where id = $1", id, nullable(text)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.minutes.draft", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"length": len(text)}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}

// ApproveMinutes approva il verbale (il presidente): la seduta diventa verbalizzata e da
// quel momento seduta, punti, presenze e verbale sono immutabili, anche nel database.
// L'impronta SHA-256 del testo va nell'audit.
func (s *Service) ApproveMinutes(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Meeting, error) {
	m, err := s.head(ctx, tx, actor, id, true)
	if err != nil {
		return nil, err
	}
	if !m.CanApprove {
		return nil, engine.ErrForbidden
	}
	if m.Status == Minuted {
		return nil, engine.InvalidCode("validation.governance.minutes_already_approved", "status", "il verbale è già approvato")
	}
	if m.Status != Held {
		return nil, engine.InvalidCode("validation.governance.minutes_approve_after", "status", "il verbale si approva a seduta svolta")
	}
	if err := s.fill(ctx, tx, m); err != nil {
		return nil, err
	}
	text := m.MinutesDraft
	if m.Minutes != nil && strings.TrimSpace(*m.Minutes) != "" {
		text = *m.Minutes
	}
	sum := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(ctx, `update meetings set status = 'minuted', minutes_text = $2, minutes_hash = $3,
		minutes_approved_at = now(), minutes_approved_by = $4 where id = $1`, id, text, hash, actor.MemberID); err != nil {
		return nil, err
	}
	for _, it := range m.Items {
		if it.Outcome == nil {
			continue
		}
		if err := engine.AddActivity(ctx, tx, it.ElementID, actor.MemberID, "meeting", "minutes",
			map[string]any{"meeting_id": id, "body": m.Body, "scheduled_at": m.ScheduledAt}, nil); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.minutes.approve", EntityType: "meeting", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"sha256": hash, "items": len(m.Items), "present": m.Quorum.Present, "quorum_reached": m.Quorum.Reached}}); err != nil {
		return nil, err
	}
	return s.Meeting(ctx, tx, actor, id)
}
