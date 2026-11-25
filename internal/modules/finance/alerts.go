package finance

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/platform/db"
)

// Avvisi di fine periodo (budget.md §3): a N giorni dalla chiusura (impostazione warn_days,
// 30 di default) ogni responsabile di un'unità con residuo non impegnato riceve una notifica
// per quel nodo. Una sola volta per periodo e nodo.

// BudgetAlerts scrive gli avvisi del tenant della transazione (aperta con Finance: la lettura
// dei nodi e dei numeri non dipende da chi la apre). Restituisce le notifiche create.
func (s *Service) BudgetAlerts(ctx context.Context, tx pgx.Tx, now time.Time) (int, error) {
	st, err := GetBudgetSettings(ctx, tx)
	if err != nil {
		return 0, err
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	periods, err := s.Periods(ctx, tx, "")
	if err != nil {
		return 0, err
	}
	sent := 0
	for i := range periods {
		p := &periods[i]
		if p.Status != "open" && p.Status != "closing" {
			continue
		}
		days := int(p.end.Sub(day).Hours() / 24)
		if days < 0 || days > st.WarnDays {
			continue
		}
		nodes, err := s.loadNodes(ctx, tx, p.ID)
		if err != nil {
			return sent, err
		}
		if _, err := s.computeNumbers(ctx, tx, p, nodes); err != nil {
			return sent, err
		}
		for _, n := range nodes {
			if n.Kind != "unit" || n.OrgUnitID == nil || n.ResidualCents <= 0 || n.Archived {
				continue
			}
			rows, err := tx.Query(ctx, `
				select m.id::text from members m
				where m.status = 'active' and (
				  m.id = (select manager_id from org_units where id = $1)
				  or m.id in (select member_id from org_unit_members where org_unit_id = $1 and role = 'manager'))
				  and not exists (select 1 from notifications x where x.member_id = m.id and x.kind = 'budget'
				                  and x.payload->>'event' = 'period_closing' and x.payload->>'node_id' = $2)`, *n.OrgUnitID, n.ID)
			if err != nil {
				return sent, err
			}
			who, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return sent, err
			}
			if len(who) == 0 {
				continue
			}
			got, err := notify.EmitDirect(ctx, tx, notify.KindBudget, who, map[string]any{"event": "period_closing", "period_id": p.ID, "period": p.Name,
				"node_id": n.ID, "node": n.Label, "residual_cents": n.ResidualCents, "days": days})
			if err != nil {
				return sent, err
			}
			sent += len(got)
		}
	}
	return sent, nil
}

// RunBudgetAlerts scrive gli avvisi di tutti i tenant che hanno periodi aperti.
func RunBudgetAlerts(ctx context.Context, d *db.DB, now time.Time) (int, error) {
	rows, err := d.Owner.Query(ctx, "select distinct tenant_id::text from budget_periods where status in ('open','closing')")
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	s := New(nil, nil)
	n := 0
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
			k, err := s.BudgetAlerts(ctx, tx, now)
			n += k
			return err
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func logAlerts(ctx context.Context, d *db.DB, log *slog.Logger) {
	if n, err := RunBudgetAlerts(ctx, d, time.Now().UTC()); err != nil {
		log.Warn("avvisi di fine periodo del budget", "errore", err)
	} else if n > 0 {
		log.Info("avvisi di fine periodo del budget", "notifiche", n)
	}
}
