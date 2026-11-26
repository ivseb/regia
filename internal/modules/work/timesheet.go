package work

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
	"regia/internal/platform/db"
)

// Timesheet settimanale (piano, passo 18; replica/work.md «Tempo e team»): una griglia
// giorni × elementi sopra le stesse work_logs delle ore rapide. Una cella è la somma delle
// ore della persona su quell'elemento in quel giorno; scriverla aggiorna o crea le
// registrazioni. Invio e approvazione del responsabile sono facoltativi, per tenant; un
// periodo chiuso non si scrive più, né dal timesheet né dalle ore rapide.

// TimeSettings sono le regole del tempo del tenant.
type TimeSettings struct {
	RequireApproval bool    `json:"require_approval"`
	ApproverMode    string  `json:"approver_mode"` // team_lead | member | admin
	ApproverID      *string `json:"approver_id"`
	Approver        *string `json:"approver"`
	LockedUntil     *string `json:"locked_until"`
	MaxDayMinutes   int     `json:"max_day_minutes"`
	CanEdit         bool    `json:"can_edit"`
}

func loadTimeSettings(ctx context.Context, tx pgx.Tx) (TimeSettings, error) {
	st := TimeSettings{ApproverMode: "team_lead", MaxDayMinutes: 960}
	err := tx.QueryRow(ctx, `select require_approval, approver_mode, approver_member_id::text,
		(select display_name from members m where m.id = s.approver_member_id), to_char(locked_until, 'YYYY-MM-DD'), max_day_minutes
		from time_settings s`).Scan(&st.RequireApproval, &st.ApproverMode, &st.ApproverID, &st.Approver, &st.LockedUntil, &st.MaxDayMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	return st, err
}

func canAdminTime(actor engine.Actor) bool {
	return actor.Has("tenant.admin") || actor.Has("finance.write")
}

// TimeSettings restituisce le regole del tempo.
func (s *Service) TimeSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*TimeSettings, error) {
	st, err := loadTimeSettings(ctx, tx)
	st.CanEdit = canAdminTime(actor)
	return &st, err
}

// TimeSettingsInput: i campi non indicati restano come sono.
type TimeSettingsInput struct {
	RequireApproval *bool   `json:"require_approval"`
	ApproverMode    *string `json:"approver_mode"`
	ApproverID      *string `json:"approver_id"`
	LockedUntil     *string `json:"locked_until"` // "" = nessun blocco
	MaxDayMinutes   *int    `json:"max_day_minutes"`
}

// SaveTimeSettings salva le regole (tenant.admin o finance.write).
func (s *Service) SaveTimeSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TimeSettingsInput) (*TimeSettings, error) {
	if !canAdminTime(actor) {
		return nil, engine.ErrForbidden
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	if in.RequireApproval != nil {
		st.RequireApproval = *in.RequireApproval
	}
	if in.ApproverMode != nil {
		if !slices.Contains([]string{"team_lead", "member", "admin"}, *in.ApproverMode) {
			return nil, engine.InvalidCode("validation.work.approver_mode", "approver_mode", "chi approva: il responsabile del team, una persona precisa o un amministratore")
		}
		st.ApproverMode = *in.ApproverMode
	}
	if in.ApproverID != nil {
		v := strings.TrimSpace(*in.ApproverID)
		st.ApproverID = nil
		if v != "" {
			st.ApproverID = &v
		}
	}
	if st.ApproverMode == "member" && st.ApproverID == nil {
		return nil, engine.Required("approver_id", "scegli la persona che approva")
	}
	if in.LockedUntil != nil {
		v := strings.TrimSpace(*in.LockedUntil)
		if v == "" {
			st.LockedUntil = nil
		} else {
			if _, err := time.Parse(time.DateOnly, v); err != nil {
				return nil, engine.InvalidCode("validation.date_invalid", "locked_until", "data non valida (AAAA-MM-GG)")
			}
			st.LockedUntil = &v
		}
	}
	if in.MaxDayMinutes != nil {
		if *in.MaxDayMinutes < 60 || *in.MaxDayMinutes > 1440 {
			return nil, engine.OutOfRange("max_day_minutes", 1, 24, "il massimo giornaliero è tra 1 e 24 ore")
		}
		st.MaxDayMinutes = *in.MaxDayMinutes
	}
	if _, err := tx.Exec(ctx, `insert into time_settings (tenant_id, require_approval, approver_mode, approver_member_id, locked_until, max_day_minutes, updated_by)
		values (app_tenant(), $1, $2, $3, $4::date, $5, $6)
		on conflict (tenant_id) do update set require_approval = excluded.require_approval, approver_mode = excluded.approver_mode,
		  approver_member_id = excluded.approver_member_id, locked_until = excluded.locked_until, max_day_minutes = excluded.max_day_minutes,
		  updated_by = excluded.updated_by, updated_at = now()`,
		st.RequireApproval, st.ApproverMode, st.ApproverID, st.LockedUntil, st.MaxDayMinutes, nullable(actor.MemberID)); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "approver_id", "persona inesistente")
		}
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "time.settings", EntityType: "tenant", EntityID: "",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"require_approval": st.RequireApproval, "approver_mode": st.ApproverMode, "locked_until": st.LockedUntil}}); err != nil {
		return nil, err
	}
	return s.TimeSettings(ctx, tx, actor)
}

// checkOpen: il giorno è scrivibile? No se è nel periodo chiuso o se la sua settimana è
// stata inviata o approvata.
func checkOpen(ctx context.Context, tx pgx.Tx, st TimeSettings, memberID string, day time.Time) error {
	if st.LockedUntil != nil {
		if lock, err := time.Parse(time.DateOnly, *st.LockedUntil); err == nil && !day.After(lock) {
			return engine.InvalidParams("validation.work.period_locked", "date", map[string]any{"date": lock.Format("02/01/2006")}, "il periodo fino al %s è chiuso: non si registrano più ore", lock.Format("02/01/2006"))
		}
	}
	var status string
	err := tx.QueryRow(ctx, "select status from timesheets where member_id = $1 and week_start = $2", memberID, weekStart(day).Format(time.DateOnly)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	switch status {
	case "submitted":
		return engine.InvalidParams("validation.work.week_submitted", "date", map[string]any{"date": day.Format("02/01/2006")}, "la settimana del %s è stata inviata: richiamala per modificarla", day.Format("02/01/2006"))
	case "approved":
		return engine.InvalidParams("validation.work.week_approved", "date", map[string]any{"date": day.Format("02/01/2006")}, "la settimana del %s è approvata: non si modifica più", day.Format("02/01/2006"))
	}
	return nil
}

func weekStart(t time.Time) time.Time {
	t = calendar.Day(t)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}

func parseWeek(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return weekStart(time.Now()), nil
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, engine.DateInvalid("week", "settimana non valida (AAAA-MM-GG)")
	}
	return weekStart(d), nil
}

// ---------------------------------------------------------------------------
// Lettura

// TSDay è un giorno della settimana.
type TSDay struct {
	Date     string `json:"date"`
	Workday  bool   `json:"workday"`
	Holiday  bool   `json:"holiday"`
	Absence  int    `json:"absence"`  // minuti di assenza
	Capacity int    `json:"capacity"` // minuti attesi (ore del giorno meno assenza; 0 nei festivi)
	Locked   bool   `json:"locked"`   // periodo chiuso
	Future   bool   `json:"future"`   // oltre domani: non si registra
	Total    int    `json:"total"`
}

// TSRow è una riga del timesheet: un elemento con i minuti di ogni giorno.
type TSRow struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Type     string  `json:"type"`
	State    string  `json:"state"`
	Category string  `json:"category"`
	Days     [7]int  `json:"days"`
	Total    int     `json:"total"`
	Estimate *int64  `json:"estimate"` // minuti
	Logged   int64   `json:"logged"`   // minuti registrati da tutti, in totale
	Assigned bool    `json:"assigned"` // assegnato a questa persona
	Parent   *string `json:"parent,omitempty"`
}

// TSSuggestion è un elemento da aggiungere alla settimana.
type TSSuggestion struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	State   string `json:"state"`
	Minutes int    `json:"minutes"` // settimana scorsa
}

// Timesheet è la settimana di una persona.
type Timesheet struct {
	MemberID   string         `json:"member_id"`
	Member     string         `json:"member"`
	Mine       bool           `json:"mine"`
	Week       string         `json:"week"` // il lunedì
	Prev       string         `json:"prev"`
	Next       string         `json:"next"`
	Days       []TSDay        `json:"days"`
	Rows       []TSRow        `json:"rows"`
	Total      int            `json:"total"`
	Capacity   int            `json:"capacity"` // minuti attesi nella settimana
	Status     string         `json:"status"`   // draft | submitted | approved | rejected
	Comment    *string        `json:"comment"`
	DecidedBy  *string        `json:"decided_by"`
	SubmitTo   []string       `json:"submit_to"` // chi riceverà la richiesta
	Editable   bool           `json:"editable"`
	CanDecide  bool           `json:"can_decide"`
	Assigned   []TSSuggestion `json:"assigned"`
	Previous   []TSSuggestion `json:"previous"`
	Settings   TimeSettings   `json:"settings"`
	MaxDay     int            `json:"max_day"`
	PrevTotal  int            `json:"prev_total"`
	WeeklyHrs  float64        `json:"weekly_hours"`
	Absences   []Absence      `json:"absences"`
	CanLogHere bool           `json:"can_log"`
}

// canSeeMember: la persona stessa, chi amministra il tempo o la finanza, il responsabile
// del suo team, chi è stato scelto per approvare.
func canSeeMember(ctx context.Context, tx pgx.Tx, actor engine.Actor, memberID string) (bool, error) {
	if memberID == actor.MemberID || canAdminTime(actor) || actor.Has("finance.read") {
		return true, nil
	}
	if ok, err := isApproverOf(ctx, tx, actor.MemberID, memberID); err != nil || ok {
		return ok, err
	}
	return isRequestApprover(ctx, tx, actor.MemberID, memberID, "")
}

// isApproverOf: chi decide le settimane della persona secondo le regole del tenant.
func isApproverOf(ctx context.Context, tx pgx.Tx, approver, memberID string) (bool, error) {
	if approver == memberID {
		return false, nil
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return false, err
	}
	switch st.ApproverMode {
	case "member":
		return st.ApproverID != nil && *st.ApproverID == approver, nil
	case "admin":
		return false, nil
	}
	var ok bool
	err = tx.QueryRow(ctx, `select exists (select 1 from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		where tm.member_id = $2::uuid and t.lead_id = $1::uuid and not t.archived)`, approver, memberID).Scan(&ok)
	return ok, err
}

// approverNames: chi riceverà l'invio della persona (per mostrarlo prima di inviare).
func approverNames(ctx context.Context, tx pgx.Tx, st TimeSettings, memberID string) ([]string, error) {
	var rows pgx.Rows
	var err error
	switch st.ApproverMode {
	case "member":
		if st.ApproverID == nil || *st.ApproverID == memberID {
			return []string{}, nil
		}
		rows, err = tx.Query(ctx, "select display_name from members where id::text = $1", *st.ApproverID)
	case "admin":
		return []string{"un amministratore"}, nil
	default:
		rows, err = tx.Query(ctx, `select distinct m.display_name from team_members tm
			join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id and not t.archived
			join members m on m.tenant_id = t.tenant_id and m.id = t.lead_id
			where tm.member_id = $1 and t.lead_id <> $1 order by 1`, memberID)
	}
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = []string{"un amministratore"}
	}
	return out, nil
}

// GetTimesheet restituisce la settimana di una persona (memberID vuoto = l'attore).
func (s *Service) GetTimesheet(ctx context.Context, tx pgx.Tx, actor engine.Actor, weekS, memberID string) (*Timesheet, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	week, err := parseWeek(weekS)
	if err != nil {
		return nil, err
	}
	if memberID == "" {
		memberID = actor.MemberID
	}
	if ok, err := canSeeMember(ctx, tx, actor, memberID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	st.CanEdit = canAdminTime(actor)
	out := &Timesheet{MemberID: memberID, Mine: memberID == actor.MemberID, Week: week.Format(time.DateOnly),
		Prev: week.AddDate(0, 0, -7).Format(time.DateOnly), Next: week.AddDate(0, 0, 7).Format(time.DateOnly),
		Days: make([]TSDay, 7), Rows: []TSRow{}, Assigned: []TSSuggestion{}, Previous: []TSSuggestion{}, Settings: st,
		MaxDay: st.MaxDayMinutes, Absences: []Absence{}, SubmitTo: []string{}, Status: "draft"}
	if err := tx.QueryRow(ctx, "select display_name, weekly_hours::float8 from members where id = $1", memberID).Scan(&out.Member, &out.WeeklyHrs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	// Giorni, capacità e assenze.
	absences, err := absencesOf(ctx, tx, memberID, week, week.AddDate(0, 0, 6))
	if err != nil {
		return nil, err
	}
	out.Absences = absences
	var lock time.Time
	if st.LockedUntil != nil {
		lock, _ = time.Parse(time.DateOnly, *st.LockedUntil)
	}
	limit := calendar.Day(time.Now()).AddDate(0, 0, 1)
	perDay := out.WeeklyHrs * 60 / 5
	for i := range out.Days {
		d := week.AddDate(0, 0, i)
		day := TSDay{Date: d.Format(time.DateOnly), Workday: d.Weekday() != time.Saturday && d.Weekday() != time.Sunday,
			Holiday: calendar.Holiday(d)}
		day.Locked = !lock.IsZero() && !d.After(lock)
		day.Future = d.After(limit)
		if calendar.Workday(d) {
			day.Absence = absenceMinutes(absences, d, int(perDay))
			day.Capacity = max(int(perDay)-day.Absence, 0)
		}
		out.Days[i] = day
		out.Capacity += day.Capacity
	}
	// Stato della settimana.
	var status string
	var comment *string
	var decidedBy *string
	err = tx.QueryRow(ctx, `select t.status, t.comment, (select display_name from members m where m.id = t.decided_by)
		from timesheets t where t.member_id = $1 and t.week_start = $2`, memberID, out.Week).Scan(&status, &comment, &decidedBy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		out.Status, out.Comment, out.DecidedBy = status, comment, decidedBy
	}
	out.Editable = out.Mine && (out.Status == "draft" || out.Status == "rejected")
	if out.Mine && st.RequireApproval {
		if out.SubmitTo, err = approverNames(ctx, tx, st, memberID); err != nil {
			return nil, err
		}
	}
	if out.Status == "submitted" && !out.Mine {
		if out.CanDecide, err = mayDecideWeek(ctx, tx, actor, memberID, out.Week); err != nil {
			return nil, err
		}
	}
	// Le righe: elementi con ore in settimana.
	rows, err := tx.Query(ctx, `
		select e.id::text, sp.key || '-' || e.number, e.title, t.key, coalesce(o.name, ''), coalesce(o.category, ''),
		       (w.work_date - $2::date), w.minutes, e.assignee_id is not distinct from $1::uuid, e.parent_id::text,
		       case when jsonb_typeof(e.fields->'`+FieldEstimate+`') = 'number' then (e.fields->>'`+FieldEstimate+`')::float8::bigint end,
		       coalesce((select sum(minutes) from work_logs x where x.element_id = e.id), 0)
		from (select element_id, work_date, sum(minutes)::int as minutes from work_logs
		      where member_id = $1 and work_date between $2::date and $2::date + 6 group by element_id, work_date) w
		join elements e on e.tenant_id = app_tenant() and e.id = w.element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		order by sp.key, e.number`, memberID, out.Week)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	var r TSRow
	var dayIdx, mins int
	if _, err := pgx.ForEachRow(rows, []any{&r.ID, &r.Key, &r.Title, &r.Type, &r.State, &r.Category, &dayIdx, &mins, &r.Assigned, &r.Parent, &r.Estimate, &r.Logged}, func() error {
		i, ok := idx[r.ID]
		if !ok {
			i = len(out.Rows)
			idx[r.ID] = i
			out.Rows = append(out.Rows, r)
		}
		if dayIdx >= 0 && dayIdx < 7 {
			out.Rows[i].Days[dayIdx] += mins
			out.Rows[i].Total += mins
			out.Days[dayIdx].Total += mins
			out.Total += mins
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if !out.Mine {
		return out, nil
	}
	// Il lavoro assegnato a me, ancora aperto, che non è già in griglia.
	arows, err := tx.Query(ctx, `
		select e.id::text, sp.key || '-' || e.number, e.title, t.key, coalesce(o.name, '')
		from elements e
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.module = 'work' and t.key <> 'sprint'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.assignee_id = $1 and e.deleted_at is null and not e.is_draft and coalesce(o.category, '') not in ('done', 'cancelled')
		order by (coalesce(o.category, '') = 'in_progress') desc, e.updated_at desc limit 40`, memberID)
	if err != nil {
		return nil, err
	}
	var a TSSuggestion
	if _, err := pgx.ForEachRow(arows, []any{&a.ID, &a.Key, &a.Title, &a.Type, &a.State}, func() error {
		if _, ok := idx[a.ID]; !ok {
			out.Assigned = append(out.Assigned, a)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// Ciò su cui ho lavorato la settimana scorsa.
	prow, err := tx.Query(ctx, `
		select e.id::text, sp.key || '-' || e.number, e.title, t.key, coalesce(o.name, ''), p.minutes
		from (select element_id, sum(minutes)::int as minutes from work_logs
		      where member_id = $1 and work_date between $2::date - 7 and $2::date - 1 group by element_id) p
		join elements e on e.tenant_id = app_tenant() and e.id = p.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		order by p.minutes desc limit 40`, memberID, out.Week)
	if err != nil {
		return nil, err
	}
	if _, err := pgx.ForEachRow(prow, []any{&a.ID, &a.Key, &a.Title, &a.Type, &a.State, &a.Minutes}, func() error {
		out.PrevTotal += a.Minutes
		if _, ok := idx[a.ID]; !ok {
			out.Previous = append(out.Previous, a)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Scrittura delle celle

// CellInput è una cella da impostare: durata scritta a mano ("1h30", "1,5", "0" per svuotare).
type CellInput struct {
	Element  string `json:"element"`
	Date     string `json:"date"`
	Duration string `json:"duration"`
}

// CellsResult: le registrazioni toccate (per il calcolo dei costi) e i totali dei giorni.
type CellsResult struct {
	Logs    []string       `json:"-"`
	Changed int            `json:"changed"`
	Days    map[string]int `json:"days"`
}

// SetCells imposta il totale di una o più celle del timesheet dell'attore, in una transazione.
// Una cella con più registrazioni (ore rapide con note) si aggiusta dalla più recente.
func (s *Service) SetCells(ctx context.Context, tx pgx.Tx, actor engine.Actor, cells []CellInput) (*CellsResult, error) {
	if len(cells) == 0 || len(cells) > 400 {
		return nil, engine.InvalidCode("validation.work.cells_range", "cells", "da 1 a 400 celle per volta")
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	res := &CellsResult{Days: map[string]int{}}
	elCache := map[string]*engine.Element{}
	limit := calendar.Day(time.Now()).AddDate(0, 0, 1)
	dates := map[string]time.Time{}
	for _, c := range cells {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(c.Date))
		if err != nil {
			return nil, engine.InvalidCode("validation.date_invalid", "date", "data non valida (AAAA-MM-GG)")
		}
		if day.After(limit) {
			return nil, engine.InvalidCode("validation.work.hours_in_future", "date", "non si registrano ore nel futuro")
		}
		if err := checkOpen(ctx, tx, st, actor.MemberID, day); err != nil {
			return nil, err
		}
		minutes, err := ParseMinutes(c.Duration)
		if err != nil {
			return nil, engine.InvalidParams("validation.work.duration_invalid", "duration", map[string]any{"detail": err.Error()}, "%s", err.Error())
		}
		if minutes > 24*60 {
			return nil, engine.InvalidCode("validation.work.day_24h", "duration", "al massimo 24 ore in un giorno")
		}
		el := elCache[c.Element]
		if el == nil {
			if el, err = s.eng.Get(ctx, tx, actor, c.Element); err != nil {
				return nil, err
			}
			if ok, err := canLog(ctx, tx, actor, el); err != nil || !ok {
				return nil, firstErr(err, engine.ErrForbidden)
			}
			elCache[c.Element] = el
		}
		ids, changed, err := s.setCell(ctx, tx, actor, el, day, minutes)
		if err != nil {
			return nil, err
		}
		res.Logs = append(res.Logs, ids...)
		if changed {
			res.Changed++
		}
		dates[day.Format(time.DateOnly)] = day
	}
	// Tetto giornaliero: la somma di tutti gli elementi del giorno.
	for ds := range dates {
		var total int
		if err := tx.QueryRow(ctx, "select coalesce(sum(minutes), 0) from work_logs where member_id = $1 and work_date = $2::date", actor.MemberID, ds).Scan(&total); err != nil {
			return nil, err
		}
		if total > st.MaxDayMinutes {
			return nil, engine.InvalidParams("validation.work.day_over_max", "duration", map[string]any{"date": dates[ds].Format("02/01"), "max": hoursText(st.MaxDayMinutes), "total": hoursText(total)}, "il %s supera il massimo di %s ore al giorno (sono %s)", dates[ds].Format("02/01"), hoursText(st.MaxDayMinutes), hoursText(total))
		}
		res.Days[ds] = total
	}
	if res.Changed > 0 {
		if err := audit.Append(ctx, tx, audit.Event{Action: "timesheet.update", EntityType: "member", EntityID: actor.MemberID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"cells": res.Changed}}); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func hoursText(m int) string {
	if m%60 == 0 {
		return fmt.Sprintf("%d", m/60)
	}
	return strings.Replace(fmt.Sprintf("%.1f", float64(m)/60), ".", ",", 1)
}

type logRow struct {
	id      string
	minutes int
}

// setCell porta la cella (persona, elemento, giorno) a minutes. Restituisce le registrazioni
// scritte o aggiornate (da ricalcolare nei costi).
func (s *Service) setCell(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, day time.Time, minutes int) ([]string, bool, error) {
	ds := day.Format(time.DateOnly)
	rows, err := tx.Query(ctx, `select id::text, minutes from work_logs where member_id = $1 and element_id = $2 and work_date = $3::date
		order by created_at desc, id for update`, actor.MemberID, el.ID, ds)
	if err != nil {
		return nil, false, err
	}
	logs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (logRow, error) {
		var l logRow
		return l, r.Scan(&l.id, &l.minutes)
	})
	if err != nil {
		return nil, false, err
	}
	old := 0
	for _, l := range logs {
		old += l.minutes
	}
	if old == minutes {
		return nil, false, nil
	}
	var touched []string
	switch {
	case minutes == 0:
		for _, l := range logs {
			if _, err := tx.Exec(ctx, "delete from work_logs where id::text = $1", l.id); err != nil {
				return nil, false, err
			}
		}
	case minutes > old && len(logs) > 0:
		if _, err := tx.Exec(ctx, "update work_logs set minutes = minutes + $2, updated_at = now() where id::text = $1", logs[0].id, minutes-old); err != nil {
			return nil, false, err
		}
		touched = append(touched, logs[0].id)
	case minutes > old:
		var role *string
		if err := tx.QueryRow(ctx, `select tm.role_key from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
			where t.delivery_space_id = $1 and tm.member_id = $2 limit 1`, el.SpaceID, actor.MemberID).Scan(&role); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, err
		}
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into work_logs (tenant_id, id, element_id, member_id, work_date, minutes, role_key, activity_type, created_by)
			values (app_tenant(), $1, $2, $3, $4::date, $5, $6, 'work', $3)`, id, el.ID, actor.MemberID, ds, minutes, role); err != nil {
			return nil, false, err
		}
		touched = append(touched, id)
	default: // meno di prima: si toglie dalle registrazioni più recenti
		left := old - minutes
		for _, l := range logs {
			if left <= 0 {
				break
			}
			if l.minutes <= left {
				if _, err := tx.Exec(ctx, "delete from work_logs where id::text = $1", l.id); err != nil {
					return nil, false, err
				}
				left -= l.minutes
				continue
			}
			if _, err := tx.Exec(ctx, "update work_logs set minutes = minutes - $2, updated_at = now() where id::text = $1", l.id, left); err != nil {
				return nil, false, err
			}
			touched = append(touched, l.id)
			left = 0
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "time",
		map[string]any{"minutes": minutes, "date": ds, "timesheet": true}, map[string]any{"minutes": old}); err != nil {
		return nil, false, err
	}
	return touched, true, nil
}

// CopyWeek copia le ore della settimana precedente in quella indicata, solo nelle celle
// vuote e nei giorni scrivibili. Restituisce le registrazioni create e il numero di celle.
func (s *Service) CopyWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, weekS string) (*CellsResult, int, error) {
	week, err := parseWeek(weekS)
	if err != nil {
		return nil, 0, err
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `select element_id::text, (work_date - $2::date + 7)::int, sum(minutes)::int
		from work_logs where member_id = $1 and work_date between $2::date - 7 and $2::date - 1 group by element_id, work_date`, actor.MemberID, week.Format(time.DateOnly))
	if err != nil {
		return nil, 0, err
	}
	type src struct {
		el  string
		day int
		min int
	}
	srcs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (src, error) {
		var x src
		return x, r.Scan(&x.el, &x.day, &x.min)
	})
	if err != nil {
		return nil, 0, err
	}
	var cells []CellInput
	limit := calendar.Day(time.Now()).AddDate(0, 0, 1)
	for _, x := range srcs {
		d := week.AddDate(0, 0, x.day)
		if d.After(limit) || checkOpen(ctx, tx, st, actor.MemberID, d) != nil {
			continue
		}
		var has bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from work_logs where member_id = $1 and element_id::text = $2 and work_date = $3::date)",
			actor.MemberID, x.el, d.Format(time.DateOnly)).Scan(&has); err != nil {
			return nil, 0, err
		}
		if has {
			continue
		}
		var gone bool
		if err := tx.QueryRow(ctx, `select not exists (select 1 from elements e left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where e.id::text = $1 and e.deleted_at is null and coalesce(o.category, '') <> 'cancelled')`, x.el).Scan(&gone); err != nil {
			return nil, 0, err
		}
		if gone {
			continue
		}
		cells = append(cells, CellInput{Element: x.el, Date: d.Format(time.DateOnly), Duration: fmt.Sprintf("%dm", x.min)})
	}
	if len(cells) == 0 {
		return &CellsResult{Days: map[string]int{}}, 0, nil
	}
	res, err := s.SetCells(ctx, tx, actor, cells)
	return res, len(cells), err
}

// ---------------------------------------------------------------------------
// Invio e approvazione

// SubmitWeek invia la settimana al responsabile. Senza approvazione richiesta dal tenant
// non c'è nulla da inviare.
func (s *Service) SubmitWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, weekS string) (*Timesheet, error) {
	week, err := parseWeek(weekS)
	if err != nil {
		return nil, err
	}
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !st.RequireApproval {
		return nil, engine.InvalidCode("validation.work.timesheet_no_approval", "week", "in questa organizzazione il timesheet non va approvato")
	}
	var total int
	if err := tx.QueryRow(ctx, "select coalesce(sum(minutes), 0) from work_logs where member_id = $1 and work_date between $2::date and $2::date + 6",
		actor.MemberID, week.Format(time.DateOnly)).Scan(&total); err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, engine.InvalidCode("validation.work.week_empty", "week", "la settimana è vuota: non c'è niente da inviare")
	}
	var cur string
	err = tx.QueryRow(ctx, "select status from timesheets where member_id = $1 and week_start = $2", actor.MemberID, week.Format(time.DateOnly)).Scan(&cur)
	if err == nil && (cur == "submitted" || cur == "approved") {
		return nil, engine.InvalidCode("validation.work.week_already_submitted", "week", "la settimana è già stata inviata")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into timesheets (tenant_id, member_id, week_start, status, minutes)
		values (app_tenant(), $1, $2, 'submitted', $3)
		on conflict (tenant_id, member_id, week_start) do update set status = 'submitted', minutes = excluded.minutes,
		  submitted_at = now(), decided_by = null, decided_at = null, comment = null`, actor.MemberID, week.Format(time.DateOnly), total); err != nil {
		return nil, err
	}
	reqID, err := s.openRequest(ctx, tx, actor.MemberID, week.Format(time.DateOnly), true)
	if err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "timesheet.submit", EntityType: "member", EntityID: actor.MemberID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"week": week.Format(time.DateOnly), "minutes": total, "approval_request_id": reqID}}); err != nil {
		return nil, err
	}
	return s.GetTimesheet(ctx, tx, actor, week.Format(time.DateOnly), "")
}

// WithdrawWeek richiama una settimana inviata e non ancora decisa.
func (s *Service) WithdrawWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, weekS string) (*Timesheet, error) {
	week, err := parseWeek(weekS)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, "delete from timesheets where member_id = $1 and week_start = $2 and status = 'submitted'", actor.MemberID, week.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidCode("validation.work.week_not_pending", "week", "la settimana non è in attesa di approvazione")
	}
	if err := closeRequests(ctx, tx, actor.MemberID, week.Format(time.DateOnly), "cancelled"); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "timesheet.withdraw", EntityType: "member", EntityID: actor.MemberID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"week": week.Format(time.DateOnly)}}); err != nil {
		return nil, err
	}
	return s.GetTimesheet(ctx, tx, actor, week.Format(time.DateOnly), "")
}

// DecideInput: approvare o respingere (con un motivo) la settimana di una persona.
type DecideInput struct {
	Member  string `json:"member"`
	Week    string `json:"week"`
	Approve bool   `json:"approve"`
	Comment string `json:"comment"`
}

// DecideWeek approva o respinge. Chi ha inviato non decide la propria settimana (separazione dei
// compiti). La decisione passa dal motore delle approvazioni, come dall'inbox: stesso passo, stesso
// esito, stesse notifiche.
func (s *Service) DecideWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, in DecideInput) (*Timesheet, error) {
	week, err := parseWeek(in.Week)
	if err != nil {
		return nil, err
	}
	if in.Member == actor.MemberID {
		return nil, engine.InvalidCode("validation.work.own_timesheet", "member", "non puoi decidere il tuo stesso timesheet")
	}
	w := week.Format(time.DateOnly)
	var submitted bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from timesheets where member_id::text = $1 and week_start = $2::date and status = 'submitted')", in.Member, w).Scan(&submitted); err != nil {
		return nil, err
	}
	if !submitted {
		// Niente da decidere: se l'attore non può nemmeno vedere la persona, vale il divieto.
		if ok, err := canSeeMember(ctx, tx, actor, in.Member); err != nil || !ok {
			return nil, firstErr(err, engine.ErrForbidden)
		}
		return nil, engine.InvalidCode("validation.work.week_not_pending", "week", "la settimana non è in attesa di approvazione")
	}
	reqID, stepID, err := pendingStep(ctx, tx, in.Member, w)
	if err != nil {
		return nil, err
	}
	if reqID == "" {
		// Settimana inviata senza richiesta (non dovrebbe succedere dopo la migrazione 0074): si apre ora.
		if _, err := s.openRequest(ctx, tx, in.Member, w, false); err != nil {
			return nil, err
		}
		if reqID, stepID, err = pendingStep(ctx, tx, in.Member, w); err != nil {
			return nil, err
		}
	}
	ok, err := mayDecideWeek(ctx, tx, actor, in.Member, w)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	comment := strings.TrimSpace(in.Comment)
	if !in.Approve && comment == "" {
		return nil, engine.Required("comment", "per respingere indica il motivo")
	}
	status := "approved"
	if !in.Approve {
		status = "rejected"
	}
	if s.ap == nil {
		if err := s.applyDecision(ctx, tx, actor, in.Member, w, status, comment); err != nil {
			return nil, err
		}
		if err := closeRequests(ctx, tx, in.Member, w, status); err != nil {
			return nil, err
		}
		return s.GetTimesheet(ctx, tx, actor, w, in.Member)
	}
	if canAdminTime(actor) {
		// Gli amministratori del tempo decidono sempre, anche se sono diventati tali dopo l'invio.
		if _, err := tx.Exec(ctx, `insert into approval_step_members (tenant_id, step_id, member_id) values (app_tenant(), $1::uuid, $2::uuid)
			on conflict do nothing`, stepID, actor.MemberID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1::uuid)", reqID); err != nil {
			return nil, err
		}
	}
	decision := "approved"
	if !in.Approve {
		decision = "rejected"
	}
	if err := s.ap.Decide(ctx, tx, actor, stepID, approval.DecideInput{Decision: decision, Comment: comment}); err != nil {
		if errors.Is(err, approval.ErrNotPending) {
			return nil, engine.InvalidCode("validation.work.week_not_pending", "week", "la settimana non è in attesa di approvazione")
		}
		return nil, err
	}
	return s.GetTimesheet(ctx, tx, actor, w, in.Member)
}

// ReopenWeek riapre una settimana approvata (solo chi amministra il tempo): torna in bozza.
func (s *Service) ReopenWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, member, weekS string) error {
	if !canAdminTime(actor) {
		return engine.ErrForbidden
	}
	week, err := parseWeek(weekS)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from timesheets where member_id::text = $1 and week_start = $2", member, week.Format(time.DateOnly))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	if err := closeRequests(ctx, tx, member, week.Format(time.DateOnly), "cancelled"); err != nil {
		return err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "timesheet.reopen", EntityType: "member", EntityID: member,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"week": week.Format(time.DateOnly)}}); err != nil {
		return err
	}
	return notify.TimesheetOutcome(ctx, tx, actor.MemberID, member, "reopened", week.Format(time.DateOnly), "")
}

// PendingWeek è una settimana in attesa della decisione dell'attore.
type PendingWeek struct {
	MemberID    string `json:"member_id"`
	Member      string `json:"member"`
	Week        string `json:"week"`
	Minutes     int    `json:"minutes"`
	SubmittedAt string `json:"submitted_at"`
	Capacity    int    `json:"capacity"`
}

// PendingWeeks elenca le settimane inviate che l'attore può decidere: quelle in cui è tra gli
// approvatori scelti all'invio (direttamente o per delega) o, se amministra il tempo, tutte.
// È lo stesso insieme che il motore mostra in «Le mie approvazioni».
func (s *Service) PendingWeeks(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]PendingWeek, error) {
	rows, err := tx.Query(ctx, `
		select t.member_id::text, m.display_name, to_char(t.week_start, 'YYYY-MM-DD'), t.minutes, to_char(t.submitted_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       (m.weekly_hours * 60)::int
		from timesheets t join members m on m.tenant_id = t.tenant_id and m.id = t.member_id
		where t.status = 'submitted' and t.member_id <> $1::uuid and (
		  $2::boolean or exists (
		    select 1 from approval_requests rq
		    join approval_steps st on st.tenant_id = rq.tenant_id and st.request_id = rq.id and st.decision = 'pending'
		    join approval_step_assignees a on a.tenant_id = st.tenant_id and a.step_id = st.id
		    where rq.subject = 'timesheet' and rq.status = 'pending' and rq.timesheet_member_id = t.member_id and rq.timesheet_week = t.week_start
		      and a.member_id in (select $1::uuid union
		            select d.from_member_id from delegations d where d.to_member_id = $1::uuid and d.scope in ('approvals', 'all')
		              and now() between d.valid_from and d.valid_to)))
		order by t.submitted_at limit 200`, actor.MemberID, canAdminTime(actor))
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingWeek, error) {
		var p PendingWeek
		return p, r.Scan(&p.MemberID, &p.Member, &p.Week, &p.Minutes, &p.SubmittedAt, &p.Capacity)
	})
	if out == nil {
		out = []PendingWeek{}
	}
	return out, err
}
