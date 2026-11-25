package devsecops

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Job periodico delle vulnerabilità: ricalcola le scadenze (la criticità o l'esposizione di
// un'applicazione possono essere cambiate), chiude le eccezioni scadute e fa scattare i passi
// di escalation della politica. Un avviso parte una volta sola per vulnerabilità e per passo;
// se una vulnerabilità ha già superato più passi, parte solo l'ultimo. Le vulnerabilità di
// una stessa applicazione e di uno stesso team viaggiano in una sola notifica.

// VulnReport conta gli effetti di un giro del job.
type VulnReport struct {
	Recomputed        int `json:"recomputed"`
	ExpiredExceptions int `json:"expired_exceptions"`
	ExceptionNotices  int `json:"exception_notices"`
	Notifications     int `json:"notifications"`
	Findings          int `json:"findings"`
	NoRecipients      int `json:"no_recipients"`
}

func (r *VulnReport) add(o VulnReport) {
	r.Recomputed += o.Recomputed
	r.ExpiredExceptions += o.ExpiredExceptions
	r.ExceptionNotices += o.ExceptionNotices
	r.Notifications += o.Notifications
	r.Findings += o.Findings
	r.NoRecipients += o.NoRecipients
}

func (r VulnReport) total() int {
	return r.Recomputed + r.ExpiredExceptions + r.ExceptionNotices + r.Notifications
}

// RunVulnerabilities esegue il job a intervalli finché ctx non è chiuso.
func (s *Service) RunVulnerabilities(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	run := func() {
		rep, err := s.CheckVulnerabilities(ctx, d)
		if err != nil {
			log.Warn("scadenze e escalation delle vulnerabilità", "errore", err)
		} else if rep.total() > 0 {
			log.Info("scadenze e escalation delle vulnerabilità", "ricalcolate", rep.Recomputed, "eccezioni_scadute", rep.ExpiredExceptions,
				"avvisi_eccezioni", rep.ExceptionNotices, "notifiche", rep.Notifications, "vulnerabilita", rep.Findings, "senza_destinatari", rep.NoRecipients)
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

// CheckVulnerabilities controlla tutti i tenant (l'elenco passa dal ruolo di piattaforma, il
// resto gira sotto RLS).
func (s *Service) CheckVulnerabilities(ctx context.Context, d *db.DB) (VulnReport, error) {
	rows, err := d.Owner.Query(ctx, `select tenant_id::text from findings where stage in ('open','taken','in_progress','exception')
		union select tenant_id::text from vuln_exceptions where status in ('pending','approved')`)
	if err != nil {
		return VulnReport{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return VulnReport{}, err
	}
	var total VulnReport
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			rep, err := s.CheckVulnerabilitiesTenant(ctx, tx)
			total.add(rep)
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckVulnerabilitiesTenant esegue il giro per un tenant.
func (s *Service) CheckVulnerabilitiesTenant(ctx context.Context, tx pgx.Tx) (VulnReport, error) {
	var rep VulnReport
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return rep, err
	}
	if rep.Recomputed, err = s.recomputeDue(ctx, tx, pol, ""); err != nil {
		return rep, err
	}
	if rep.ExpiredExceptions, rep.ExceptionNotices, err = s.ExpireExceptions(ctx, tx, pol); err != nil {
		return rep, err
	}
	// Un'eccezione scaduta rimette in gioco delle vulnerabilità: scadenze da ricalcolare.
	if rep.ExpiredExceptions > 0 {
		n, err := s.recomputeDue(ctx, tx, pol, "")
		if err != nil {
			return rep, err
		}
		rep.Recomputed += n
	}
	if err := s.escalate(ctx, tx, pol, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

type escGroup struct {
	app, team string
	ids       []string
	assignees []string
	critical  int
	high      int
	first     time.Time // la scadenza più vicina
	oldest    time.Time // la più vecchia già superata (zero se nessuna)
}

func (s *Service) escalate(ctx context.Context, tx pgx.Tx, pol VulnPolicy, rep *VulnReport) error {
	now := s.now()
	for idx := len(pol.Escalation) - 1; idx >= 0; idx-- {
		st := pol.Escalation[idx]
		rows, err := tx.Query(ctx, `select id::text, application_element_id::text, coalesce(team_id::text, ''), coalesce(assignee_id::text, ''), severity, due_at
			from findings
			where stage in `+openStages+` and due_at is not null and exception_id is null and application_element_id is not null
			  and notice_level < $1 and due_at + $2::int * interval '1 day' <= $3
			order by due_at for update skip locked`, idx+1, st.Days, now)
		if err != nil {
			return err
		}
		groups := map[[2]string]*escGroup{}
		var order [][2]string
		for rows.Next() {
			var id, app, team, assignee, sev string
			var due time.Time
			if err := rows.Scan(&id, &app, &team, &assignee, &sev, &due); err != nil {
				rows.Close()
				return err
			}
			k := [2]string{app, team}
			g := groups[k]
			if g == nil {
				g = &escGroup{app: app, team: team, first: due}
				groups[k] = g
				order = append(order, k)
			}
			g.ids = append(g.ids, id)
			if assignee != "" && !slices.Contains(g.assignees, assignee) {
				g.assignees = append(g.assignees, assignee)
			}
			switch sev {
			case "critical":
				g.critical++
			case "high":
				g.high++
			}
			if due.Before(g.first) {
				g.first = due
			}
			if due.Before(now) && (g.oldest.IsZero() || due.Before(g.oldest)) {
				g.oldest = due
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, k := range order {
			g := groups[k]
			if _, err := tx.Exec(ctx, "update findings set notice_level = $2 where id = any($1::uuid[])", g.ids, idx+1); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into vuln_events (tenant_id, finding_id, at, kind, note)
				select app_tenant(), unnest($1::uuid[]), $2, 'escalation', $3`, g.ids, now, st.Name); err != nil {
				return err
			}
			members, groupIDs, err := s.escalationRecipients(ctx, tx, st, g)
			if err != nil {
				return err
			}
			rep.Findings += len(g.ids)
			if len(members) == 0 && len(groupIDs) == 0 {
				rep.NoRecipients++
				continue
			}
			payload := map[string]any{"count": len(g.ids), "critical": g.critical, "high": g.high, "step": st.Name, "level": idx + 1}
			if st.Days < 0 {
				payload["event"] = "vuln_due_soon"
				payload["due"] = g.first.Format("02/01/2006")
			} else {
				payload["event"] = "vuln_overdue"
				payload["days"] = 0
				if !g.oldest.IsZero() {
					payload["days"] = int(now.Sub(g.oldest).Hours() / 24)
				}
			}
			if err := s.notifyVuln(ctx, tx, g.app, "", members, groupIDs, payload); err != nil {
				return err
			}
			rep.Notifications++
		}
	}
	return nil
}

// escalationRecipients risolve i destinatari di un passo per un gruppo di vulnerabilità.
func (s *Service) escalationRecipients(ctx context.Context, tx pgx.Tx, st EscStep, g *escGroup) (members, groups []string, err error) {
	add := func(ids []string) {
		for _, id := range ids {
			if id != "" && !slices.Contains(members, id) {
				members = append(members, id)
			}
		}
	}
	q := func(sql string, args ...any) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		add(ids)
		return nil
	}
	for _, to := range st.To {
		switch {
		case to == "assignee":
			add(g.assignees)
		case to == "team" && g.team != "":
			err = q(`select member_id::text from team_members where team_id = $1 and (valid_to is null or valid_to >= current_date)`, g.team)
		case to == "team_lead" && g.team != "":
			err = q(`select lead_id::text from teams where id = $1 and lead_id is not null`, g.team)
		case to == "unit_manager" && g.team != "":
			err = q(`select ou.manager_id::text from teams t join org_units ou on ou.tenant_id = t.tenant_id and ou.id = t.org_unit_id
				where t.id = $1 and ou.manager_id is not null`, g.team)
		case to == "parent_manager" && g.team != "":
			err = q(`select pu.manager_id::text from teams t
				join org_units ou on ou.tenant_id = t.tenant_id and ou.id = t.org_unit_id
				join org_units pu on pu.tenant_id = ou.tenant_id and pu.id = ou.parent_id
				where t.id = $1 and pu.manager_id is not null`, g.team)
		case strings.HasPrefix(to, "group:"):
			gid := strings.TrimPrefix(to, "group:")
			if !slices.Contains(groups, gid) {
				groups = append(groups, gid)
			}
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return members, groups, nil
}
