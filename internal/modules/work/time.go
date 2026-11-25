package work

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Ore lavorate (time tracking): inserimento rapido dalle card e dal dettaglio. Le ore vanno in
// work_logs, la stessa tabella delle ore inserite dalla Finanza; il costo lo calcola il
// modulo finance (finance.CostWorkLogs) in una transazione di sistema separata, così chi
// registra le ore non vede tariffe né importi.

// TimeInput registra ore su un elemento, per chi le inserisce.
type TimeInput struct {
	Hours    float64 `json:"hours"`
	Duration string  `json:"duration"` // in alternativa: "1h 30m", "2h", "1g"
	Date     string  `json:"date"`     // AAAA-MM-GG; vuota = oggi
	Text     string  `json:"text"`
}

// TimeEntry è una registrazione di ore, senza costi.
type TimeEntry struct {
	ID         string  `json:"id"`
	ElementKey string  `json:"element_key"`
	MemberID   string  `json:"member_id"`
	Member     string  `json:"member"`
	Date       string  `json:"date"`
	Minutes    int     `json:"minutes"`
	Text       *string `json:"text"`
	Mine       bool    `json:"mine"`
}

// TimeSummary sono le ore di un elemento: le sue registrazioni e il totale con i figli.
type TimeSummary struct {
	Entries      []TimeEntry `json:"entries"`
	Minutes      int64       `json:"minutes"`       // sull'elemento
	TotalMinutes int64       `json:"total_minutes"` // con tutta la discendenza (story → task)
	Estimate     *float64    `json:"estimate"`      // minuti stimati (campo "stima")
	CanLog       bool        `json:"can_log"`
}

// LogTime registra le ore dell'attore sull'elemento e restituisce l'id della registrazione
// (da passare a finance.CostWorkLogs).
func (s *Service) LogTime(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in TimeInput) (string, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return "", err
	}
	if ok, err := canLog(ctx, tx, actor, el); err != nil || !ok {
		return "", firstErr(err, engine.ErrForbidden)
	}
	minutes := int(math.Round(in.Hours * 60))
	if d := strings.TrimSpace(in.Duration); d != "" {
		// Un numero da solo è in ore ("2", "1,5"); altrimenti una durata ("1h30", "1h 30m", "1g").
		m, err := ParseMinutes(d)
		if err != nil {
			return "", engine.InvalidParams("validation.work.duration_invalid", "duration", map[string]any{"detail": err.Error()}, "%s", err.Error())
		}
		minutes = m
	}
	if minutes <= 0 || minutes > 24*60 {
		return "", engine.InvalidCode("validation.work.time_entry_range", "hours", "le ore di una registrazione sono tra 1 minuto e 24 ore")
	}
	day := time.Now()
	if strings.TrimSpace(in.Date) != "" {
		if day, err = time.Parse(time.DateOnly, strings.TrimSpace(in.Date)); err != nil {
			return "", engine.InvalidCode("validation.date_invalid", "date", "data non valida (AAAA-MM-GG)")
		}
	}
	if day.After(time.Now().AddDate(0, 0, 1)) {
		return "", engine.InvalidCode("validation.work.hours_in_future", "date", "non si registrano ore nel futuro")
	}
	if st, err := loadTimeSettings(ctx, tx); err != nil {
		return "", err
	} else if err := checkOpen(ctx, tx, st, actor.MemberID, day); err != nil {
		return "", err
	}
	text := strings.TrimSpace(in.Text)
	if len(text) > 1000 {
		return "", engine.TooLong("text", 1000, "nota troppo lunga (massimo 1000 caratteri)")
	}
	// Ruolo con cui si lavora (per la tariffa di ruolo): quello nel team dello spazio.
	var role *string
	if err := tx.QueryRow(ctx, `select tm.role_key from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		where t.delivery_space_id = $1 and tm.member_id = $2 limit 1`, el.SpaceID, actor.MemberID).Scan(&role); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into work_logs (tenant_id, id, element_id, member_id, work_date, minutes, role_key, activity_type, text, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, 'work', $7, $3)`,
		id, el.ID, actor.MemberID, day.Format(time.DateOnly), minutes, role, nullable(text)); err != nil {
		return "", err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "time",
		map[string]any{"minutes": minutes, "date": day.Format(time.DateOnly)}, nil); err != nil {
		return "", err
	}
	return id, audit.Append(ctx, tx, audit.Event{Action: "work_log.create", EntityType: "work_log", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": el.ID, "member_id": actor.MemberID, "minutes": minutes, "source": "work"}})
}

// canLog: chi può scrivere gli elementi, l'assegnatario, i membri del team dello spazio.
func canLog(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element) (bool, error) {
	if (el.AssigneeID != nil && *el.AssigneeID == actor.MemberID) || actor.Has("element.write") {
		return true, nil
	}
	return inTeam(ctx, tx, actor, el.SpaceID)
}

// TimeOf restituisce le ore registrate sull'elemento (ultime 200) e i totali.
func (s *Service) TimeOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*TimeSummary, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &TimeSummary{Entries: []TimeEntry{}}
	if out.CanLog, err = canLog(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select w.id::text, s.key || '-' || e.number, w.member_id::text, m.display_name, to_char(w.work_date, 'YYYY-MM-DD'), w.minutes, w.text
		from work_logs w
		join elements e on e.tenant_id = w.tenant_id and e.id = w.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join members m on m.tenant_id = w.tenant_id and m.id = w.member_id
		where w.element_id = $1 order by w.work_date desc, w.created_at desc limit 200`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Entries, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TimeEntry, error) {
		var t TimeEntry
		err := r.Scan(&t.ID, &t.ElementKey, &t.MemberID, &t.Member, &t.Date, &t.Minutes, &t.Text)
		t.Mine = t.MemberID == actor.MemberID
		return t, err
	}); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `
		with recursive fam(id) as (select $1::uuid union select c.id from elements c join fam f on c.parent_id = f.id where c.deleted_at is null)
		select coalesce((select sum(minutes) from work_logs where element_id = $1), 0),
		       coalesce((select sum(minutes) from work_logs where element_id in (select id from fam)), 0)`, el.ID).
		Scan(&out.Minutes, &out.TotalMinutes); err != nil {
		return nil, err
	}
	if v, ok := el.Fields[FieldEstimate].(float64); ok {
		out.Estimate = &v
	}
	return out, nil
}

// DeleteTime cancella una propria registrazione (con le eventuali voci di costo, in cascata).
func (s *Service) DeleteTime(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	var elementID, memberID string
	var day time.Time
	err := tx.QueryRow(ctx, "select element_id::text, member_id::text, work_date from work_logs where id::text = $1", id).Scan(&elementID, &memberID, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if memberID != actor.MemberID && !actor.Has("finance.write") {
		return engine.ErrForbidden
	}
	if _, err := s.eng.Get(ctx, tx, actor, elementID); err != nil {
		return err
	}
	if st, err := loadTimeSettings(ctx, tx); err != nil {
		return err
	} else if err := checkOpen(ctx, tx, st, memberID, day); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from work_logs where id::text = $1", id); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, elementID, actor.MemberID, "work", "time.removed", nil, map[string]any{"id": id}); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "work_log.delete", EntityType: "work_log", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": elementID}})
}
