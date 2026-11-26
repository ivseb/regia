package projects

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Summary è un progetto nell'elenco, con i numeri principali (gli stessi della Panoramica).
type Summary struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	StateCategory string   `json:"state_category"`
	Template      string   `json:"template"`
	PM            *string  `json:"project_manager"`
	Requests      int      `json:"requests"`
	Progress      float64  `json:"progress"`
	Signal        string   `json:"signal"`
	Start         *string  `json:"start"`
	End           *string  `json:"end"`
	WorkPackages  int      `json:"work_packages"`
	NextMilestone *ItemRef `json:"next_milestone"`
	Late          int      `json:"late"`
}

// List elenca i progetti aperti (o tutti), i più recenti prima.
func (s *Service) List(ctx context.Context, tx pgx.Tx, actor engine.Actor, includeClosed bool) ([]Summary, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	q := `select e.id::text from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'project'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft`
	if !includeClosed {
		q += ` and coalesce(o.category, '') not in ('done', 'cancelled')`
	}
	rows, err := tx.Query(ctx, q+` order by e.created_at desc limit 200`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(ids))
	rd := s.reader(actor)
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			continue // non visibile all'attore
		}
		p, err := s.loadPlan(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		n, err := s.numbersOf(ctx, tx, actor, p, false)
		if err != nil {
			return nil, err
		}
		// Nell'elenco serve solo il titolo della prossima milestone: si controlla quella, non tutto il piano.
		if nm := n.NextMilestone; nm != nil {
			ok, err := rd.can(ctx, tx, nm.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				nm.Title, nm.Team, nm.Restricted = "", nil, true
			}
		}
		sm := Summary{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, StateCategory: el.StateCategory,
			Template: p.Project.Template, Requests: len(p.Project.Requests), Progress: n.Progress, Signal: n.Signal,
			Start: n.Start, End: n.End, WorkPackages: n.WorkPackages, NextMilestone: n.NextMilestone, Late: len(n.Late)}
		if sm.Start == nil {
			sm.Start, sm.End = p.Project.Start, p.Project.End
		}
		_ = tx.QueryRow(ctx, `select m.display_name from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'project_manager'
			join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
			where sh.element_id = $1 order by sh.created_at limit 1`, el.ID).Scan(&sm.PM)
		out = append(out, sm)
	}
	return out, nil
}

// ProjectOfRequest restituisce il progetto in cui è confluita la richiesta (nil se non c'è).
func (s *Service) ProjectOfRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestRef string) (*Ref, bool, error) {
	req, err := s.eng.Get(ctx, tx, actor, requestRef)
	if err != nil {
		return nil, false, err
	}
	var r Ref
	err = tx.QueryRow(ctx, `select p.id::text, s.key || '-' || p.number, p.title from project_requests pr
		join elements p on p.tenant_id = pr.tenant_id and p.id = pr.project_id and p.deleted_at is null
		join spaces s on s.tenant_id = p.tenant_id and s.id = p.space_id
		where pr.request_id = $1`, req.ID).Scan(&r.ID, &r.Key, &r.Title)
	if err == pgx.ErrNoRows {
		ok := false
		if req.TypeKey == RequestType {
			if ok, err = requestApproved(ctx, tx, req); err != nil {
				return nil, false, err
			}
		}
		return nil, ok, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &r, true, nil
}
