package work

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Membri dei team, capacità e assenze (piano, passo 18; replica/work.md «Tempo e team»).
// La capacità di un team è la somma, per ogni membro, delle ore a settimana (quelle indicate
// nel team, altrimenti quelle della persona) per la quota di allocazione. Si riflette in
// teams.weekly_capacity_hours, che il carico dei team (planning.Load) e la scheda Team in
// Architettura già leggono; le assenze riducono le singole settimane del carico.

// TeamMember è una persona in un team.
type TeamMember struct {
	MemberID    string   `json:"member_id"`
	Name        string   `json:"name"`
	Email       *string  `json:"email"`
	Lead        bool     `json:"lead"`
	Role        *string  `json:"role"` // funzione (backend, analista…), libera
	Allocation  int      `json:"allocation_pct"`
	WeeklyHours *float64 `json:"weekly_hours"` // ore nel team; null = quelle della persona
	PersonHours float64  `json:"person_hours"` // ore a settimana della persona
	Hours       float64  `json:"hours"`        // ore effettive nel team: ore × allocazione
	AbsentNow   bool     `json:"absent_now"`
}

// TeamView è un team con le sue persone.
type TeamView struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	LeadID    *string      `json:"lead_id"`
	Lead      *string      `json:"lead"`
	SpaceKey  *string      `json:"space_key"`
	Members   []TeamMember `json:"members"`
	Hours     float64      `json:"hours"`         // capacità a settimana
	Days      float64      `json:"capacity_days"` // la stessa in giorni di otto ore
	CanManage bool         `json:"can_manage"`
}

func canManageTeams(actor engine.Actor) bool {
	return actor.Has("architecture.write") || actor.Has("tenant.admin")
}

// Teams elenca i team con le persone, per tutti quelli che leggono.
func (s *Service) Teams(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]TeamView, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `select t.id::text, t.name, t.lead_id::text, l.display_name, sp.key
		from teams t left join members l on l.tenant_id = t.tenant_id and l.id = t.lead_id
		left join spaces sp on sp.tenant_id = t.tenant_id and sp.id = t.delivery_space_id
		where not t.archived order by t.name`)
	if err != nil {
		return nil, err
	}
	teams, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamView, error) {
		var t TeamView
		t.Members = []TeamMember{}
		return t, r.Scan(&t.ID, &t.Name, &t.LeadID, &t.Lead, &t.SpaceKey)
	})
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i := range teams {
		idx[teams[i].ID] = i
		teams[i].CanManage = canManageTeams(actor) || deref(teams[i].LeadID) == actor.MemberID
	}
	mrows, err := tx.Query(ctx, `select tm.team_id::text, m.id::text, m.display_name, m.email, tm.role_key, tm.allocation_pct,
		tm.weekly_hours::float8, m.weekly_hours::float8,
		exists (select 1 from member_absences a where a.member_id = m.id and current_date between a.from_date and a.to_date and a.hours_per_day is null)
		from team_members tm join members m on m.tenant_id = tm.tenant_id and m.id = tm.member_id
		where m.status <> 'anonymized' order by m.display_name`)
	if err != nil {
		return nil, err
	}
	var teamID string
	var tm TeamMember
	if _, err := pgx.ForEachRow(mrows, []any{&teamID, &tm.MemberID, &tm.Name, &tm.Email, &tm.Role, &tm.Allocation, &tm.WeeklyHours, &tm.PersonHours, &tm.AbsentNow}, func() error {
		i, ok := idx[teamID]
		if !ok {
			return nil
		}
		tm.Lead = deref(teams[i].LeadID) == tm.MemberID
		base := tm.PersonHours
		if tm.WeeklyHours != nil {
			base = *tm.WeeklyHours
		}
		tm.Hours = base * float64(tm.Allocation) / 100
		teams[i].Members = append(teams[i].Members, tm)
		teams[i].Hours += tm.Hours
		return nil
	}); err != nil {
		return nil, err
	}
	for i := range teams {
		teams[i].Days = teams[i].Hours / 8
		// Il responsabile viene prima, poi in ordine alfabetico.
		slices.SortStableFunc(teams[i].Members, func(a, b TeamMember) int {
			if a.Lead != b.Lead {
				if a.Lead {
					return -1
				}
				return 1
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
	}
	return teams, nil
}

// canManageTeam: chi gestisce i team o il responsabile di quel team.
func canManageTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, teamID string) (bool, error) {
	if canManageTeams(actor) {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, "select exists (select 1 from teams where id::text = $1 and lead_id = $2::uuid)", teamID, actor.MemberID).Scan(&ok)
	return ok, err
}

// TeamMemberInput aggiunge o aggiorna una persona nel team.
type TeamMemberInput struct {
	MemberID    string   `json:"member_id"`
	Lead        bool     `json:"lead"`
	Role        string   `json:"role"`
	Allocation  *int     `json:"allocation_pct"`
	WeeklyHours *float64 `json:"weekly_hours"` // null = quelle della persona
	ClearHours  bool     `json:"clear_hours"`  // torna alle ore della persona
}

// SaveTeamMember aggiunge la persona al team o ne cambia ruolo, allocazione e ore. Chi è
// indicato come responsabile diventa il lead del team (uno solo).
func (s *Service) SaveTeamMember(ctx context.Context, tx pgx.Tx, actor engine.Actor, teamID string, in TeamMemberInput) error {
	if ok, err := canManageTeam(ctx, tx, actor, teamID); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	if in.Allocation != nil && (*in.Allocation < 0 || *in.Allocation > 100) {
		return engine.OutOfRange("allocation_pct", 0, 100, "l'allocazione è tra 0 e 100")
	}
	if in.WeeklyHours != nil && (*in.WeeklyHours < 0 || *in.WeeklyHours > 80) {
		return engine.OutOfRange("weekly_hours", 0, 80, "le ore a settimana sono tra 0 e 80")
	}
	if len(strings.TrimSpace(in.Role)) > 60 {
		return engine.TooLong("role", 60, "ruolo troppo lungo (massimo 60 caratteri)")
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status in ('active', 'invited') and member_type <> 'service')", in.MemberID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound("member_id", "member", "persona inesistente o non attiva")
	}
	if err := tx.QueryRow(ctx, "select exists (select 1 from teams where id::text = $1 and not archived)", teamID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.ErrNotFound
	}
	pct := 100
	if in.Allocation != nil {
		pct = *in.Allocation
	}
	if _, err := tx.Exec(ctx, `insert into team_members (tenant_id, team_id, member_id, role_key, allocation_pct, weekly_hours)
		values (app_tenant(), $1::uuid, $2::uuid, $3, $4, $5)
		on conflict (tenant_id, team_id, member_id) do update set
		  role_key = case when $6 then excluded.role_key else team_members.role_key end,
		  allocation_pct = case when $7 then excluded.allocation_pct else team_members.allocation_pct end,
		  weekly_hours = case when $9 then null when $8 then excluded.weekly_hours else team_members.weekly_hours end`,
		teamID, in.MemberID, nullable(strings.TrimSpace(in.Role)), pct, in.WeeklyHours,
		strings.TrimSpace(in.Role) != "", in.Allocation != nil, in.WeeklyHours != nil, in.ClearHours); err != nil {
		return err
	}
	if in.Lead {
		if _, err := tx.Exec(ctx, "update teams set lead_id = $2::uuid where id::text = $1", teamID, in.MemberID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, "update teams set lead_id = null where id::text = $1 and lead_id = $2::uuid", teamID, in.MemberID); err != nil {
		return err
	}
	if err := syncTeamCapacity(ctx, tx, teamID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "team.member", EntityType: "team", EntityID: teamID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"member_id": in.MemberID, "lead": in.Lead, "allocation_pct": pct}})
}

// RemoveTeamMember toglie la persona dal team (e dal ruolo di responsabile).
func (s *Service) RemoveTeamMember(ctx context.Context, tx pgx.Tx, actor engine.Actor, teamID, memberID string) error {
	if ok, err := canManageTeam(ctx, tx, actor, teamID); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	tag, err := tx.Exec(ctx, "delete from team_members where team_id::text = $1 and member_id::text = $2", teamID, memberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	if _, err := tx.Exec(ctx, "update teams set lead_id = null where id::text = $1 and lead_id::text = $2", teamID, memberID); err != nil {
		return err
	}
	if err := syncTeamCapacity(ctx, tx, teamID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "team.member.remove", EntityType: "team", EntityID: teamID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"member_id": memberID}})
}

// syncTeamCapacity allinea la capacità del team alle sue persone. Senza persone resta
// quella indicata a mano.
func syncTeamCapacity(ctx context.Context, tx pgx.Tx, teamID string) error {
	_, err := tx.Exec(ctx, `update teams t set weekly_capacity_hours = x.h from (
		select coalesce(sum(coalesce(tm.weekly_hours, m.weekly_hours) * tm.allocation_pct / 100.0), 0) as h, count(*) as n
		from team_members tm join members m on m.tenant_id = tm.tenant_id and m.id = tm.member_id where tm.team_id::text = $1) x
		where t.id::text = $1 and x.n > 0`, teamID)
	return err
}

// ---------------------------------------------------------------------------
// Assenze

// Absence è un'assenza semplice.
type Absence struct {
	ID          string   `json:"id"`
	MemberID    string   `json:"member_id"`
	Member      string   `json:"member"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Kind        string   `json:"kind"`
	HoursPerDay *float64 `json:"hours_per_day"` // null = tutta la giornata
	Note        *string  `json:"note"`
}

// AbsenceInput: giorni interi, o poche ore al giorno.
type AbsenceInput struct {
	MemberID    string   `json:"member_id"` // vuoto = chi scrive
	From        string   `json:"from"`
	To          string   `json:"to"`
	Kind        string   `json:"kind"`
	HoursPerDay *float64 `json:"hours_per_day"`
	Note        string   `json:"note"`
}

func absencesOf(ctx context.Context, tx pgx.Tx, memberID string, from, to time.Time) ([]Absence, error) {
	rows, err := tx.Query(ctx, `select a.id::text, a.member_id::text, m.display_name, to_char(a.from_date, 'YYYY-MM-DD'), to_char(a.to_date, 'YYYY-MM-DD'),
		a.kind, a.hours_per_day::float8, a.note
		from member_absences a join members m on m.tenant_id = a.tenant_id and m.id = a.member_id
		where a.member_id::text = $1 and a.to_date >= $2::date and a.from_date <= $3::date order by a.from_date`,
		memberID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Absence])
	if out == nil {
		out = []Absence{}
	}
	return out, err
}

// absenceMinutes: i minuti di assenza del giorno d (al massimo quelli di una giornata).
func absenceMinutes(abs []Absence, d time.Time, perDay int) int {
	ds := d.Format(time.DateOnly)
	total := 0
	for _, a := range abs {
		if ds < a.From || ds > a.To {
			continue
		}
		if a.HoursPerDay == nil {
			total = perDay
			break
		}
		total += int(*a.HoursPerDay * 60)
	}
	return min(total, perDay)
}

// ListAbsences elenca le assenze di una persona (o di tutti, per chi gestisce i team) nel periodo.
func (s *Service) ListAbsences(ctx context.Context, tx pgx.Tx, actor engine.Actor, memberID, fromS, toS string) ([]Absence, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	from, err := time.Parse(time.DateOnly, fromS)
	if err != nil {
		from = time.Now().AddDate(0, 0, -7)
	}
	to, err := time.Parse(time.DateOnly, toS)
	if err != nil {
		to = from.AddDate(0, 3, 0)
	}
	where, args := "", []any{from.Format(time.DateOnly), to.Format(time.DateOnly)}
	if memberID != "" {
		where, args = " and a.member_id::text = $3", append(args, memberID)
	}
	rows, err := tx.Query(ctx, `select a.id::text, a.member_id::text, m.display_name, to_char(a.from_date, 'YYYY-MM-DD'), to_char(a.to_date, 'YYYY-MM-DD'),
		a.kind, a.hours_per_day::float8, a.note
		from member_absences a join members m on m.tenant_id = a.tenant_id and m.id = a.member_id
		where a.to_date >= $1::date and a.from_date <= $2::date`+where+` order by a.from_date, m.display_name limit 500`, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Absence])
	if out == nil {
		out = []Absence{}
	}
	return out, err
}

// canEditAbsences: la persona stessa, chi gestisce i team, il responsabile di un suo team.
func canEditAbsences(ctx context.Context, tx pgx.Tx, actor engine.Actor, memberID string) (bool, error) {
	if memberID == actor.MemberID || canManageTeams(actor) {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		where tm.member_id::text = $1 and t.lead_id = $2::uuid)`, memberID, actor.MemberID).Scan(&ok)
	return ok, err
}

// CreateAbsence registra un'assenza.
func (s *Service) CreateAbsence(ctx context.Context, tx pgx.Tx, actor engine.Actor, in AbsenceInput) (*Absence, error) {
	if in.MemberID == "" {
		in.MemberID = actor.MemberID
	}
	if ok, err := canEditAbsences(ctx, tx, actor, in.MemberID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	from, err1 := time.Parse(time.DateOnly, in.From)
	to, err2 := time.Parse(time.DateOnly, firstNonEmpty(in.To, in.From))
	if err1 != nil || err2 != nil {
		return nil, engine.DateInvalid("from", "date non valide (AAAA-MM-GG)")
	}
	if to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		return nil, engine.InvalidCode("validation.work.absence_range", "to", "il periodo va dal primo all'ultimo giorno, al massimo un anno")
	}
	kind := firstNonEmpty(in.Kind, "ferie")
	if !slices.Contains([]string{"ferie", "malattia", "permesso", "altro"}, kind) {
		return nil, engine.InvalidCode("validation.work.absence_kind", "kind", "tipo di assenza: ferie, malattia, permesso o altro")
	}
	if in.HoursPerDay != nil && (*in.HoursPerDay <= 0 || *in.HoursPerDay > 24) {
		return nil, engine.OutOfRange("hours_per_day", 0, 24, "le ore al giorno sono tra 0 e 24 (vuoto = tutta la giornata)")
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 300 {
		return nil, engine.TooLong("note", 300, "nota troppo lunga (massimo 300 caratteri)")
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into member_absences (tenant_id, id, member_id, from_date, to_date, kind, hours_per_day, note, created_by)
		values (app_tenant(), $1, $2::uuid, $3::date, $4::date, $5, $6, $7, $8::uuid)`,
		id, in.MemberID, from.Format(time.DateOnly), to.Format(time.DateOnly), kind, in.HoursPerDay, nullable(note), nullable(actor.MemberID)); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "member_id", "persona inesistente")
		}
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "absence.create", EntityType: "member", EntityID: in.MemberID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly), "kind": kind}}); err != nil {
		return nil, err
	}
	var out Absence
	err := tx.QueryRow(ctx, `select a.id::text, a.member_id::text, m.display_name, to_char(a.from_date, 'YYYY-MM-DD'), to_char(a.to_date, 'YYYY-MM-DD'),
		a.kind, a.hours_per_day::float8, a.note from member_absences a join members m on m.tenant_id = a.tenant_id and m.id = a.member_id where a.id = $1`, id).
		Scan(&out.ID, &out.MemberID, &out.Member, &out.From, &out.To, &out.Kind, &out.HoursPerDay, &out.Note)
	return &out, err
}

// DeleteAbsence cancella un'assenza.
func (s *Service) DeleteAbsence(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	var memberID string
	err := tx.QueryRow(ctx, "select member_id::text from member_absences where id::text = $1", id).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if ok, err := canEditAbsences(ctx, tx, actor, memberID); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	if _, err := tx.Exec(ctx, "delete from member_absences where id::text = $1", id); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "absence.delete", EntityType: "member", EntityID: memberID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
