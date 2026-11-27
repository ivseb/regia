package projects

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Applicazione e progetti nel tempo (passo 6, progetti.md §6 «Componente (Architettura)»): la
// scheda dell'applicazione elenca i work package dei progetti che la toccano, con periodo, fase e
// team, e i conflitti (due progetti aperti sulla stessa applicazione nello stesso periodo, come al
// passo 4). Le voci che l'attore non legge restano con le date, senza titolo né team.

// AppWork è un work package che tocca l'applicazione.
type AppWork struct {
	ID            string  `json:"id"`
	WorkPackage   *Ref    `json:"work_package"` // nil se non leggibile
	Project       *Ref    `json:"project"`      // nil se non leggibile
	Phase         *string `json:"phase"`
	Mode          *string `json:"delivery_mode"`
	Team          *string `json:"team"`
	Start         *string `json:"start"`
	End           *string `json:"end"`
	State         string  `json:"state"`
	StateCategory string  `json:"state_category"`
	// Open: il work package e il suo progetto sono aperti (contano per i conflitti).
	Open       bool `json:"open"`
	Restricted bool `json:"restricted,omitempty"`
	projectID  string
}

// AppConflict: due work package di progetti diversi sovrapposti sull'applicazione.
type AppConflict struct {
	A     string  `json:"a"` // id dei work package (a < b)
	B     string  `json:"b"`
	From  *string `json:"from"` // periodo sovrapposto
	Until *string `json:"until"`
}

// AppProjects è la parte «progetti» della scheda dell'applicazione.
type AppProjects struct {
	Application Ref           `json:"application"`
	Items       []AppWork     `json:"items"`
	Conflicts   []AppConflict `json:"conflicts"`
}

// ApplicationProjects restituisce i work package dei progetti sull'applicazione e i conflitti.
func (s *Service) ApplicationProjects(ctx context.Context, tx pgx.Tx, actor engine.Actor, appRef string) (*AppProjects, error) {
	app, err := s.eng.Get(ctx, tx, actor, appRef)
	if err != nil {
		return nil, err
	}
	if app.TypeKey != "application" {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, `select w.element_id::text, ws.key || '-' || we.number, we.title, w.project_id::text, ps.key || '-' || pe.number, pe.title,
		coalesce(wo.name, ''), coalesce(wo.category, ''), coalesce(po.category, ''), ph.name, w.delivery_mode, t.name, we.start_at, we.due_at
		from ext_work_package w
		join elements we on we.tenant_id = w.tenant_id and we.id = w.element_id and we.deleted_at is null
		join spaces ws on ws.tenant_id = we.tenant_id and ws.id = we.space_id
		left join options wo on wo.tenant_id = we.tenant_id and wo.id = we.state_id
		join elements pe on pe.tenant_id = w.tenant_id and pe.id = w.project_id and pe.deleted_at is null and not pe.is_draft
		join spaces ps on ps.tenant_id = pe.tenant_id and ps.id = pe.space_id
		left join options po on po.tenant_id = pe.tenant_id and po.id = pe.state_id
		left join plan_phases ph on ph.tenant_id = w.tenant_id and ph.id = w.phase_id
		left join teams t on t.tenant_id = w.tenant_id and t.id = w.team_id
		where w.application_id = $1 and coalesce(wo.category, '') <> 'cancelled' and coalesce(po.category, '') <> 'cancelled'
		order by we.start_at nulls last, pe.number, we.number limit 500`, app.ID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AppWork, error) {
		var a AppWork
		var wp, pj Ref
		var pcat string
		var start, end *time.Time
		err := r.Scan(&a.ID, &wp.Key, &wp.Title, &a.projectID, &pj.Key, &pj.Title, &a.State, &a.StateCategory, &pcat,
			&a.Phase, &a.Mode, &a.Team, &start, &end)
		wp.ID, pj.ID = a.ID, a.projectID
		a.WorkPackage, a.Project = &wp, &pj
		a.Start, a.End = dateStr(start), dateStr(end)
		a.Open = a.StateCategory != "done" && pcat != "done"
		return a, err
	})
	if err != nil {
		return nil, err
	}
	out := &AppProjects{Application: Ref{ID: app.ID, Key: app.Key, Title: app.Title}, Items: items, Conflicts: appConflicts(items)}
	rd := s.reader(actor)
	for i := range out.Items {
		a := &out.Items[i]
		ok, err := rd.can(ctx, tx, a.projectID)
		if err != nil {
			return nil, err
		}
		if !ok {
			a.Project, a.WorkPackage, a.Team, a.Phase, a.Restricted = nil, nil, nil, nil, true
			continue
		}
		if ok, err = rd.can(ctx, tx, a.ID); err != nil {
			return nil, err
		} else if !ok {
			a.WorkPackage, a.Team, a.Restricted = nil, nil, true
		}
	}
	return out, nil
}

// appConflicts: coppie di work package aperti, con date, di progetti diversi, sovrapposti.
func appConflicts(items []AppWork) []AppConflict {
	out := []AppConflict{}
	for i := range items {
		a := &items[i]
		if !a.Open || a.Start == nil || a.End == nil {
			continue
		}
		for j := i + 1; j < len(items); j++ {
			b := &items[j]
			if !b.Open || b.Start == nil || b.End == nil || b.projectID == a.projectID {
				continue
			}
			if *a.Start > *b.End || *b.Start > *a.End {
				continue
			}
			from, until := a.Start, a.End
			if *b.Start > *from {
				from = b.Start
			}
			if *b.End < *until {
				until = b.End
			}
			x, y := a.ID, b.ID
			if y < x {
				x, y = y, x
			}
			out = append(out, AppConflict{A: x, B: y, From: from, Until: until})
		}
	}
	return out
}
