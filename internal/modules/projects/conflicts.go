package projects

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
)

// Componenti condivisi (passo 4, progetti.md §3 «Componenti condivisi: chi governa»): quando
// due progetti pianificano lavoro sulla stessa applicazione nello stesso periodo, il piano lo
// mostra sul work package («anche il progetto X modifica IDP fino al 30/04») e il team owner
// dell'applicazione riceve un avviso, una volta per coppia di work package finché il conflitto dura.
//
// Contano i work package aperti (né chiusi né annullati) con date, di progetti aperti.

// Conflict è un altro work package che tocca la stessa applicazione nello stesso periodo.
type Conflict struct {
	ItemID        string  `json:"item_id"` // il work package di questo progetto
	ApplicationID string  `json:"application_id"`
	Application   string  `json:"application"`
	Project       *Ref    `json:"project"`      // nil se l'attore non legge l'altro progetto
	WorkPackage   *Ref    `json:"work_package"` // nil se l'attore non legge l'altro work package
	Start         *string `json:"start"`        // periodo dell'altro work package
	End           *string `json:"end"`
	// Restricted: l'altro progetto non è leggibile («un altro progetto»).
	Restricted bool `json:"restricted,omitempty"`
	otherID    string
	ownerTeam  *string
}

const conflictsSQL = `
	select w.element_id::text, ap.id::text, ap.title, o.element_id::text, os.key || '-' || oe.number, oe.title,
	       op.id::text, ops.key || '-' || op.number, op.title, oe.start_at, oe.due_at, xa.owner_team_id::text
	from ext_work_package w
	join elements we on we.tenant_id = w.tenant_id and we.id = w.element_id and we.deleted_at is null
	left join options wo on wo.tenant_id = we.tenant_id and wo.id = we.state_id
	join elements ap on ap.tenant_id = w.tenant_id and ap.id = w.application_id and ap.deleted_at is null
	left join ext_application xa on xa.tenant_id = ap.tenant_id and xa.element_id = ap.id
	join ext_work_package o on o.tenant_id = w.tenant_id and o.application_id = w.application_id and o.project_id <> w.project_id
	join elements oe on oe.tenant_id = o.tenant_id and oe.id = o.element_id and oe.deleted_at is null
	left join options oo on oo.tenant_id = oe.tenant_id and oo.id = oe.state_id
	join spaces os on os.tenant_id = oe.tenant_id and os.id = oe.space_id
	join elements op on op.tenant_id = o.tenant_id and op.id = o.project_id and op.deleted_at is null
	left join options opo on opo.tenant_id = op.tenant_id and opo.id = op.state_id
	join spaces ops on ops.tenant_id = op.tenant_id and ops.id = op.space_id
	where w.project_id = $1
	  and coalesce(wo.category, '') not in ('done', 'cancelled')
	  and coalesce(oo.category, '') not in ('done', 'cancelled')
	  and coalesce(opo.category, '') not in ('done', 'cancelled')
	  and we.start_at is not null and we.due_at is not null and oe.start_at is not null and oe.due_at is not null
	  and we.start_at <= oe.due_at and oe.start_at <= we.due_at
	order by oe.due_at, oe.number`

// conflictsOf restituisce i conflitti dei work package del progetto, senza limiti di visibilità.
func conflictsOf(ctx context.Context, tx pgx.Tx, projectID string) ([]Conflict, error) {
	rows, err := tx.Query(ctx, conflictsSQL, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Conflict, error) {
		var c Conflict
		var wp, pj Ref
		var start, end *time.Time
		err := r.Scan(&c.ItemID, &c.ApplicationID, &c.Application, &wp.ID, &wp.Key, &wp.Title, &pj.ID, &pj.Key, &pj.Title, &start, &end, &c.ownerTeam)
		c.otherID = wp.ID
		c.WorkPackage, c.Project = &wp, &pj
		c.Start, c.End = dateStr(start), dateStr(end)
		return c, err
	})
}

// loadConflicts mette i conflitti sui work package del piano.
func loadConflicts(ctx context.Context, tx pgx.Tx, p *Plan) error {
	cs, err := conflictsOf(ctx, tx, p.Project.ID)
	if err != nil {
		return err
	}
	byItem := map[string][]Conflict{}
	for _, c := range cs {
		byItem[c.ItemID] = append(byItem[c.ItemID], c)
	}
	for i := range p.Items {
		it := &p.Items[i]
		if it.Kind == TypeWorkPackage {
			it.Conflicts = byItem[it.ID]
			if it.Conflicts == nil {
				it.Conflicts = []Conflict{}
			}
		}
	}
	return nil
}

// limitConflicts toglie dai conflitti ciò che l'attore non legge: l'altro progetto diventa «un
// altro progetto», l'altro work package perde il titolo. L'applicazione resta: il conflitto è suo.
func limitConflicts(ctx context.Context, tx pgx.Tx, r *reader, cs []Conflict) error {
	for i := range cs {
		c := &cs[i]
		if c.Project != nil {
			ok, err := r.can(ctx, tx, c.Project.ID)
			if err != nil {
				return err
			}
			if !ok {
				c.Project, c.WorkPackage, c.Restricted = nil, nil, true
				continue
			}
		}
		if c.WorkPackage != nil {
			ok, err := r.can(ctx, tx, c.WorkPackage.ID)
			if err != nil {
				return err
			}
			if !ok {
				c.WorkPackage = nil
			}
		}
	}
	return nil
}

// notifyConflicts avvisa il team owner dell'applicazione dei conflitti nuovi del progetto e
// dimentica quelli risolti, così un conflitto che si ripresenta si segnala di nuovo.
func (s *Service) notifyConflicts(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) error {
	cs, err := conflictsOf(ctx, tx, projectID)
	if err != nil {
		return err
	}
	type pair struct{ a, b string }
	live := map[pair]bool{}
	for _, c := range cs {
		a, b := c.ItemID, c.otherID
		if b < a {
			a, b = b, a
		}
		pr := pair{a, b}
		if live[pr] {
			continue
		}
		live[pr] = true
		tag, err := tx.Exec(ctx, `insert into plan_conflicts (tenant_id, work_package_a, work_package_b, application_id)
			values (app_tenant(), $1, $2, $3) on conflict do nothing`, a, b, c.ApplicationID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 || c.ownerTeam == nil {
			continue
		}
		var mine string
		if err := tx.QueryRow(ctx, `select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.id = $1`, projectID).Scan(&mine); err != nil {
			return err
		}
		// Fine del periodo sovrapposto: la prima delle due fini.
		var until string
		if err := tx.QueryRow(ctx, `select to_char(least(a.due_at, b.due_at), 'DD/MM/YYYY') from elements a, elements b where a.id = $1 and b.id = $2`,
			c.ItemID, c.otherID).Scan(&until); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select lead_id::text from teams where id = $1 and lead_id is not null
			union select member_id::text from team_members where team_id = $1`, *c.ownerTeam)
		if err != nil {
			return err
		}
		members, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if len(members) == 0 {
			continue
		}
		if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindProject, ElementID: c.ApplicationID, ActorID: actor.MemberID, Members: members,
			Payload: map[string]any{"event": "conflict", "project": mine, "other": c.Project.Key, "application": c.Application, "until": until}}); err != nil {
			return err
		}
	}
	// Conflitti risolti: le coppie del progetto che non si sovrappongono più.
	rows, err := tx.Query(ctx, `select work_package_a::text, work_package_b::text from plan_conflicts
		where work_package_a in (select element_id from ext_work_package where project_id = $1)
		   or work_package_b in (select element_id from ext_work_package where project_id = $1)`, projectID)
	if err != nil {
		return err
	}
	var gone []pair
	for rows.Next() {
		var pr pair
		if err := rows.Scan(&pr.a, &pr.b); err != nil {
			rows.Close()
			return err
		}
		if !live[pr] {
			gone = append(gone, pr)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, pr := range gone {
		if _, err := tx.Exec(ctx, "delete from plan_conflicts where work_package_a = $1 and work_package_b = $2", pr.a, pr.b); err != nil {
			return err
		}
	}
	return nil
}
