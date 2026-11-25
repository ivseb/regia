package reviews

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/db"
)

// Lo SLA di una valutazione corre in giorni lavorativi dall'invio al team, è sospeso
// durante i chiarimenti e si ferma all'esito. Il job, nel processo, manda:
//   - il sollecito al team reminder_days giorni lavorativi prima della scadenza;
//   - l'avviso di ritardo al team e a chi compila il primo giorno dopo la scadenza;
//   - l'escalation al responsabile dopo escalation_days giorni lavorativi di ritardo.
// Ogni avviso parte una volta sola (reminded_at, overdue_notified_at, escalated_at).

// SLAReport conta gli avvisi mandati da un giro del job.
type SLAReport struct {
	Reminders   int `json:"reminders"`
	Overdue     int `json:"overdue"`
	Escalations int `json:"escalations"`
}

// RunSLA esegue il job a intervalli finché ctx non è chiuso.
func (s *Service) RunSLA(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	run := func() {
		rep, err := s.CheckSLA(ctx, d)
		if err != nil {
			log.Warn("controllo degli SLA delle valutazioni", "errore", err)
		} else if rep.Reminders+rep.Overdue+rep.Escalations > 0 {
			log.Info("avvisi di SLA delle valutazioni", "solleciti", rep.Reminders, "ritardi", rep.Overdue, "escalation", rep.Escalations)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// CheckSLA controlla le valutazioni con lo SLA in corso in tutti i tenant.
func (s *Service) CheckSLA(ctx context.Context, d *db.DB) (SLAReport, error) {
	// Solo l'elenco dei tenant passa dal ruolo di piattaforma; il resto gira sotto RLS.
	rows, err := d.Owner.Query(ctx, `select distinct tenant_id::text from ext_review
		where stage in ('submitted','exam') and due_on is not null and escalated_at is null`)
	if err != nil {
		return SLAReport{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return SLAReport{}, err
	}
	var total SLAReport
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			rep, err := s.CheckSLATenant(ctx, tx)
			total.Reminders += rep.Reminders
			total.Overdue += rep.Overdue
			total.Escalations += rep.Escalations
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckSLATenant fa il controllo nel tenant della transazione.
func (s *Service) CheckSLATenant(ctx context.Context, tx pgx.Tx) (SLAReport, error) {
	var rep SLAReport
	rows, err := tx.Query(ctx, selectReview+` where x.stage in ('submitted','exam') and x.due_on is not null and x.escalated_at is null
		for update of x skip locked`)
	if err != nil {
		return rep, err
	}
	var list []*reviewRow
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			rows.Close()
			return rep, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	today := s.today()
	for _, r := range list {
		due := *r.DueOn
		late := lateDays(due, today)
		dueStr := due.Format(time.DateOnly)
		base := map[string]any{"team": r.Team, "due": dueStr}
		switch {
		case late == 0 && r.RemindedAt == nil && !addWorkdays(today, r.ReminderDays).Before(due):
			if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindSLA, ElementID: r.ID, Groups: []string{r.GroupID},
				Payload: with(base, "event", "reminder")}); err != nil {
				return rep, err
			}
			if _, err := tx.Exec(ctx, "update ext_review set reminded_at = now() where element_id = $1", r.ID); err != nil {
				return rep, err
			}
			rep.Reminders++
		case late > 0 && r.OverdueNotifiedAt == nil:
			// Al team e a chi segue la valutazione (chi compila, il responsabile).
			if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindSLA, ElementID: r.ID, Groups: []string{r.GroupID},
				Followers: true, Payload: with(base, "event", "overdue")}); err != nil {
				return rep, err
			}
			if _, err := tx.Exec(ctx, "update ext_review set overdue_notified_at = now(), reminded_at = coalesce(reminded_at, now()) where element_id = $1", r.ID); err != nil {
				return rep, err
			}
			if err := engine.AddActivity(ctx, tx, r.ID, "", "sla", "overdue", base, nil); err != nil {
				return rep, err
			}
			rep.Overdue++
		}
		if late > 0 && r.EscalationDays != nil && late >= *r.EscalationDays && r.EscalatedAt == nil {
			if r.LeadID != nil {
				if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindSLA, ElementID: r.ID, Members: []string{*r.LeadID},
					Payload: with(base, "event", "escalation", "late_days", strconv.Itoa(late))}); err != nil {
					return rep, err
				}
			}
			if _, err := tx.Exec(ctx, "update ext_review set escalated_at = now() where element_id = $1", r.ID); err != nil {
				return rep, err
			}
			if err := engine.AddActivity(ctx, tx, r.ID, "", "sla", "escalation", with(base, "late_days", strconv.Itoa(late)), nil); err != nil {
				return rep, err
			}
			rep.Escalations++
		}
	}
	return rep, nil
}

func with(m map[string]any, kv ...string) map[string]any {
	out := make(map[string]any, len(m)+len(kv)/2)
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}
