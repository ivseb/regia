package report

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/platform/db"
)

// Promemoria degli status report. Per ogni richiesta aperta in una delle fasi in cui il
// report è atteso (impostazioni del tenant):
//   - alla prima volta nasce la periodicità, con la prima scadenza a un periodo da oggi;
//   - reminder_days giorni prima della scadenza parte un promemoria al project manager
//     (o, se manca, al demand manager), una volta sola per scadenza (reminded_for);
//   - l'invio del report sposta la scadenza al periodo successivo (Submit).

// RunReminders esegue il controllo a intervalli finché ctx non è chiuso.
func (s *Service) RunReminders(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func() {
		n, err := s.CheckReminders(ctx, d)
		if err != nil {
			log.Warn("promemoria degli status report", "errore", err)
		} else if n > 0 {
			log.Info("promemoria degli status report inviati", "numero", n)
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

// CheckReminders controlla tutti i tenant. Solo l'elenco passa dal ruolo di piattaforma.
func (s *Service) CheckReminders(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.Owner.Query(ctx, `select id::text from tenants`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			n, err := s.CheckRemindersTenant(ctx, tx)
			total += n
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckRemindersTenant fa il controllo nel tenant della transazione; restituisce i promemoria inviati.
func (s *Service) CheckRemindersTenant(ctx context.Context, tx pgx.Tx) (int, error) {
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return 0, err
	}
	if len(st.Phases) == 0 {
		return 0, nil
	}
	c, err := s.clock(ctx, tx)
	if err != nil {
		return 0, err
	}
	// Richieste nelle fasi attese: chi non ha ancora la periodicità la riceve (se il
	// tenant non ha disattivato i report).
	if st.Frequency != "off" {
		first := day(addPeriod(c.today, st.Frequency))
		if _, err := tx.Exec(ctx, `
			insert into status_report_schedules (tenant_id, element_id, next_due)
			select app_tenant(), e.id, $2::date
			from elements e
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = $3
			join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where e.deleted_at is null and not e.is_draft and e.resolved_at is null and o.name = any($1)
			on conflict (tenant_id, element_id) do nothing`, st.Phases, first, requestType); err != nil {
			return 0, err
		}
	}
	rows, err := tx.Query(ctx, `
		select sc.element_id, sc.next_due
		from status_report_schedules sc
		join elements e on e.tenant_id = sc.tenant_id and e.id = sc.element_id and e.deleted_at is null and e.resolved_at is null
		join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where o.name = any($1) and sc.next_due is not null and coalesce(sc.frequency, $2) <> 'off'
		  and sc.next_due - $3::int <= $4::date and sc.reminded_for is distinct from sc.next_due
		for update of sc skip locked`, st.Phases, st.Frequency, st.ReminderDays, c.today)
	if err != nil {
		return 0, err
	}
	type due struct {
		id  string
		due time.Time
	}
	var list []due
	var x due
	if _, err := pgx.ForEachRow(rows, []any{&x.id, &x.due}, func() error { list = append(list, x); return nil }); err != nil {
		return 0, err
	}
	sent := 0
	for _, it := range list {
		members, groups, err := projectManagers(ctx, tx, it.id)
		if err != nil {
			return sent, err
		}
		event := "due"
		if it.due.Before(c.today) {
			event = "overdue"
		}
		if len(members)+len(groups) > 0 {
			if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReport, ElementID: it.id, Members: members, Groups: groups,
				Payload: map[string]any{"event": event, "due": day(it.due)}}); err != nil {
				return sent, err
			}
			sent++
		}
		if _, err := tx.Exec(ctx, `update status_report_schedules set reminded_for = next_due where element_id = $1`, it.id); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// projectManagers: i project manager della richiesta; se mancano, i demand manager.
func projectManagers(ctx context.Context, tx pgx.Tx, elementID string) (members, groups []string, err error) {
	for _, role := range []string{"project_manager", "demand_manager"} {
		rows, err := tx.Query(ctx, `select sh.member_id::text, sh.group_id::text from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
			where sh.element_id = $1 and r.key = $2`, elementID, role)
		if err != nil {
			return nil, nil, err
		}
		var m, g *string
		if _, err := pgx.ForEachRow(rows, []any{&m, &g}, func() error {
			if m != nil {
				members = append(members, *m)
			}
			if g != nil {
				groups = append(groups, *g)
			}
			return nil
		}); err != nil {
			return nil, nil, err
		}
		if len(members)+len(groups) > 0 {
			return members, groups, nil
		}
	}
	return nil, nil, nil
}
