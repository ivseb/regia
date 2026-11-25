package risk

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Scadenze del modulo, controllate da un job: contratti ICT in scadenza (preavviso del
// contratto più quello del tenant), revisioni e trattamenti dei rischi, accettazioni scadute
// (il rischio torna da valutare), notifiche normative vicine alla scadenza o scadute. Ogni
// avviso parte una volta sola.

// DeadlineReport conta gli avvisi di un giro del job.
type DeadlineReport struct {
	Contracts     int `json:"contracts"`
	Reviews       int `json:"reviews"`
	Treatments    int `json:"treatments"`
	Expired       int `json:"expired_acceptances"`
	Notifications int `json:"notifications"`
	Overdue       int `json:"overdue_notifications"`
}

func (r *DeadlineReport) add(o DeadlineReport) {
	r.Contracts += o.Contracts
	r.Reviews += o.Reviews
	r.Treatments += o.Treatments
	r.Expired += o.Expired
	r.Notifications += o.Notifications
	r.Overdue += o.Overdue
}

func (r DeadlineReport) total() int {
	return r.Contracts + r.Reviews + r.Treatments + r.Expired + r.Notifications + r.Overdue
}

// RunDeadlines esegue il job a intervalli finché ctx non è chiuso.
func (s *Service) RunDeadlines(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	run := func() {
		rep, err := s.CheckDeadlines(ctx, d)
		if err != nil {
			log.Warn("scadenze di rischi, contratti e notifiche", "errore", err)
		} else if rep.total() > 0 {
			log.Info("scadenze di rischi, contratti e notifiche", "contratti", rep.Contracts, "revisioni", rep.Reviews,
				"trattamenti", rep.Treatments, "accettazioni_scadute", rep.Expired, "notifiche", rep.Notifications, "notifiche_scadute", rep.Overdue)
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

// CheckDeadlines controlla le scadenze di tutti i tenant (l'elenco dei tenant passa dal ruolo
// di piattaforma, il resto gira sotto RLS).
func (s *Service) CheckDeadlines(ctx context.Context, d *db.DB) (DeadlineReport, error) {
	rows, err := d.Owner.Query(ctx, `select tenant_id::text from ext_risk union select tenant_id::text from ict_contracts
		union select tenant_id::text from regulatory_notifications where status in ('pending', 'draft')`)
	if err != nil {
		return DeadlineReport{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return DeadlineReport{}, err
	}
	var total DeadlineReport
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			rep, err := s.CheckDeadlinesTenant(ctx, tx)
			total.add(rep)
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckDeadlinesTenant controlla le scadenze di un tenant.
func (s *Service) CheckDeadlinesTenant(ctx context.Context, tx pgx.Tx) (DeadlineReport, error) {
	var rep DeadlineReport
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return rep, err
	}
	today := s.today()
	sys := engine.Actor{Permissions: []string{rbac.ElementRead}}

	// Contratti in scadenza.
	rows, err := tx.Query(ctx, selectContract+` where c.end_date is not null and c.end_date >= $1 and c.expiry_notice_sent_at is null
		and c.end_date <= $1 + (coalesce(c.notice_days, 0) + $2) * interval '1 day' for update of c skip locked`, today, st.ContractNoticeDays)
	if err != nil {
		return rep, err
	}
	contracts, err := s.scanContracts(ctx, tx, rows, st.ContractNoticeDays)
	if err != nil {
		return rep, err
	}
	for _, c := range contracts {
		if _, err := tx.Exec(ctx, "update ict_contracts set expiry_notice_sent_at = now() where id = $1", c.ID); err != nil {
			return rep, err
		}
		if err := s.notifyContract(ctx, tx, c); err != nil {
			return rep, err
		}
		rep.Contracts++
	}

	// Revisioni e trattamenti dei rischi aperti.
	type due struct {
		id, kind string
		at       time.Time
	}
	var dues []due
	drows, err := tx.Query(ctx, `select x.element_id::text, 'review', x.review_due from ext_risk x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.review_due is not null and x.review_notice_sent_at is null and x.review_due <= $1::date + $2 * interval '1 day'
		  and not (x.acceptance_stage = 'accepted' and x.accepted_until < $1)
		  and coalesce(o.category, '') not in ('done', 'cancelled')
		union all
		select x.element_id::text, 'treatment', x.treatment_due from ext_risk x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.treatment_due is not null and x.treatment_notice_sent_at is null and x.treatment is distinct from 'accept'
		  and x.treatment_due <= $1::date + $2 * interval '1 day' and coalesce(o.category, '') = 'in_progress'`, today, st.ReviewNoticeDays)
	if err != nil {
		return rep, err
	}
	var id, kind string
	var at time.Time
	if _, err := pgx.ForEachRow(drows, []any{&id, &kind, &at}, func() error {
		dues = append(dues, due{id, kind, at})
		return nil
	}); err != nil {
		return rep, err
	}
	for _, d := range dues {
		col, event := "review_notice_sent_at", "risk_review_due"
		if d.kind == "treatment" {
			col, event = "treatment_notice_sent_at", "risk_treatment_due"
		}
		if _, err := tx.Exec(ctx, "update ext_risk set "+col+" = now() where element_id = $1", d.id); err != nil {
			return rep, err
		}
		if err := s.notifyRisk(ctx, tx, sys, d.id, map[string]any{"event": event, "due": d.at.Format(time.DateOnly)}); err != nil {
			return rep, err
		}
		if d.kind == "treatment" {
			rep.Treatments++
		} else {
			rep.Reviews++
		}
	}

	// Accettazioni scadute: il rischio torna da valutare.
	arows, err := tx.Query(ctx, `select element_id::text from ext_risk where acceptance_stage = 'accepted' and accepted_until < $1`, today)
	if err != nil {
		return rep, err
	}
	expired, err := pgx.CollectRows(arows, pgx.RowTo[string])
	if err != nil {
		return rep, err
	}
	for _, id := range expired {
		el, err := s.eng.Get(ctx, tx, reader(""), id)
		if err != nil {
			return rep, err
		}
		phase, err := s.phaseFor(ctx, tx, el, ActionAcceptance, "reopened")
		if err != nil {
			return rep, err
		}
		if phase == "" {
			if _, err := tx.Exec(ctx, "update ext_risk set acceptance_stage = 'expired' where element_id = $1", id); err != nil {
				return rep, err
			}
		} else if _, err := s.eng.Transition(ctx, tx, sys, id, phase, "Accettazione scaduta: il rischio va rivalutato", engine.TransitionOpts{System: true}); err != nil {
			return rep, err
		}
		if err := s.notifyRisk(ctx, tx, sys, id, map[string]any{"event": "risk_acceptance_expired", "due": today.Format(time.DateOnly)}); err != nil {
			return rep, err
		}
		rep.Expired++
	}

	// Notifiche normative: preavviso e scadenza passata.
	now := s.now()
	nrows, err := tx.Query(ctx, selectNotification+` where n.status in ('pending', 'draft') and n.due_at is not null
		and ((n.reminder_sent_at is null and n.due_at <= $1) or (n.overdue_sent_at is null and n.due_at < $2)) for update of n skip locked`,
		now.Add(time.Duration(st.NotificationWarningHours)*time.Hour), now)
	if err != nil {
		return rep, err
	}
	notices, err := scanNotifications(nrows, now)
	if err != nil {
		return rep, err
	}
	for _, n := range notices {
		overdue := n.DueAt.Before(now)
		event := "notification_due"
		if overdue {
			event = "notification_overdue"
		}
		// Una notifica già scaduta non riceve anche il preavviso.
		if _, err := tx.Exec(ctx, `update regulatory_notifications set reminder_sent_at = coalesce(reminder_sent_at, now()),
			overdue_sent_at = case when $2 then now() else overdue_sent_at end where id = $1`, n.ID, overdue); err != nil {
			return rep, err
		}
		if err := s.notifyIncident(ctx, tx, sys, n.IncidentID, map[string]any{"event": event, "scheme": n.SchemeName, "stage": n.StageName,
			"due": n.DueAt.In(rome).Format("02/01/2006 15:04")}); err != nil {
			return rep, err
		}
		if overdue {
			rep.Overdue++
		} else {
			rep.Notifications++
		}
	}
	return rep, nil
}

// rome è il fuso degli orari nei testi delle notifiche (UTC se il sistema non lo conosce).
var rome = func() *time.Location {
	l, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.UTC
	}
	return l
}()
