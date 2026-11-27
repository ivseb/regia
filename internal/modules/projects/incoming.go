package projects

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
)

// «In arrivo dai progetti» (progetti.md §6 «Team», passo 5): il team vede il lavoro che i progetti
// hanno pianificato su di lui nelle prossime settimane, prima che diventi lavoro sulla board. Sono i
// work package con il suo team e le attività di quei work package, aperti, di progetti aperti, con
// date che toccano la finestra. Il work package ancora da accettare è «proposto»; quello agile già
// accettato porta l'epic sulla board. Le voci che l'attore non legge non compaiono: si contano.

// IncomingItem è una voce pianificata sul team.
type IncomingItem struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	Kind          string   `json:"kind"` // work_package | plan_activity
	Project       Ref      `json:"project"`
	Phase         *string  `json:"phase"`
	WorkPackage   *Ref     `json:"work_package"` // dell'attività
	Mode          *string  `json:"delivery_mode"`
	Team          string   `json:"team"`
	State         string   `json:"state"`
	StateCategory string   `json:"state_category"`
	Start         *string  `json:"start"`
	End           *string  `json:"end"`
	EffortDays    *float64 `json:"estimate_effort_days"`
	// Proposed: il work package aspetta che il team lo accetti (fase da fare).
	Proposed bool  `json:"proposed"`
	Epics    []Ref `json:"epics"`
}

// Incoming è la vista del team.
type Incoming struct {
	Teams []Ref          `json:"teams"`
	From  string         `json:"from"`
	To    string         `json:"to"`
	Weeks int            `json:"weeks"`
	Items []IncomingItem `json:"items"`
	// Restricted: voci pianificate sul team che l'attore non può leggere.
	Restricted int `json:"restricted"`
}

const incomingSQL = `
	with tm as (
		select id, name from teams
		where ($1 <> '' and id::text = $1) or ($2 <> '' and delivery_space_id = (select id from spaces where key = $2))),
	items as (
		select w.element_id id, 'work_package' kind, w.project_id, w.phase_id, null::uuid wp_id, w.delivery_mode mode,
		       w.estimate_effort_days eff, tm.name team
		from ext_work_package w join tm on tm.id = w.team_id
		union all
		select a.element_id, 'plan_activity', a.project_id, w.phase_id, a.work_package_id, w.delivery_mode, a.estimate_effort_days, tm.name
		from ext_plan_activity a
		join ext_work_package w on w.tenant_id = a.tenant_id and w.element_id = a.work_package_id
		join tm on tm.id = w.team_id)
	select i.id::text, sp.key || '-' || e.number, e.title, i.kind, p.id::text, pp.key || '-' || p.number, p.title, ph.name,
	       i.wp_id::text, wps.key || '-' || wp.number, wp.title, i.mode, i.team,
	       coalesce(o.name, ''), coalesce(o.category, ''), e.start_at, e.due_at, i.eff::float8
	from items i
	join elements e on e.tenant_id = app_tenant() and e.id = i.id and e.deleted_at is null
	join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join elements p on p.tenant_id = e.tenant_id and p.id = i.project_id and p.deleted_at is null
	join spaces pp on pp.tenant_id = p.tenant_id and pp.id = p.space_id
	left join options po on po.tenant_id = p.tenant_id and po.id = p.state_id
	left join plan_phases ph on ph.tenant_id = e.tenant_id and ph.id = i.phase_id
	left join elements wp on wp.tenant_id = e.tenant_id and wp.id = i.wp_id
	left join spaces wps on wps.tenant_id = wp.tenant_id and wps.id = wp.space_id
	where coalesce(o.category, '') not in ('done', 'cancelled') and coalesce(po.category, '') not in ('done', 'cancelled')
	  and coalesce(e.start_at, e.due_at)::date <= $4::date and coalesce(e.due_at, e.start_at)::date >= $3::date
	order by coalesce(e.start_at, e.due_at), pp.key, p.number, i.kind = 'plan_activity', e.number`

// IncomingInput sceglie il team (per id o per spazio di consegna) e la finestra in settimane.
type IncomingInput struct {
	Team  string
	Space string
	Weeks int
}

// IncomingWork restituisce il lavoro dei progetti pianificato sul team nelle prossime settimane.
func (s *Service) IncomingWork(ctx context.Context, tx pgx.Tx, actor engine.Actor, in IncomingInput) (*Incoming, error) {
	if in.Team == "" && in.Space == "" {
		return nil, engine.Required("team", "indica il team o lo spazio del team")
	}
	if in.Weeks <= 0 {
		in.Weeks = 8
	}
	if in.Weeks > 52 {
		in.Weeks = 52
	}
	from := today()
	to := from.AddDate(0, 0, 7*in.Weeks)
	out := &Incoming{Teams: []Ref{}, Items: []IncomingItem{}, Weeks: in.Weeks, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}
	rows, err := tx.Query(ctx, `select id::text, '', name from teams
		where ($1 <> '' and id::text = $1) or ($2 <> '' and delivery_space_id = (select id from spaces where key = $2)) order by name`, in.Team, in.Space)
	if err != nil {
		return nil, err
	}
	if out.Teams, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Ref]); err != nil {
		return nil, err
	}
	if len(out.Teams) == 0 {
		return out, nil
	}
	rows, err = tx.Query(ctx, incomingSQL, in.Team, in.Space, out.From, out.To)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (IncomingItem, error) {
		var it IncomingItem
		var start, end *time.Time
		var wpID, wpKey, wpTitle *string
		err := r.Scan(&it.ID, &it.Key, &it.Title, &it.Kind, &it.Project.ID, &it.Project.Key, &it.Project.Title, &it.Phase,
			&wpID, &wpKey, &wpTitle, &it.Mode, &it.Team, &it.State, &it.StateCategory, &start, &end, &it.EffortDays)
		if err != nil {
			return it, err
		}
		it.Start, it.End = dateStr(start), dateStr(end)
		if wpID != nil && wpKey != nil {
			it.WorkPackage = &Ref{ID: *wpID, Key: *wpKey, Title: deref(wpTitle)}
		}
		it.Proposed = it.Kind == TypeWorkPackage && it.StateCategory == "todo"
		it.Epics = []Ref{}
		return it, nil
	})
	if err != nil {
		return nil, err
	}
	rd := s.reader(actor)
	for _, it := range items {
		ok, err := rd.can(ctx, tx, it.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			out.Restricted++
			continue
		}
		if ok, err := rd.can(ctx, tx, it.Project.ID); err != nil {
			return nil, err
		} else if !ok {
			it.Project.Title = ""
		}
		if it.WorkPackage != nil {
			if ok, err := rd.can(ctx, tx, it.WorkPackage.ID); err != nil {
				return nil, err
			} else if !ok {
				it.WorkPackage.Title = ""
			}
		}
		if it.Kind == TypeWorkPackage && deref(it.Mode) == ModeAgile {
			ids, err := work.EpicsOfWorkPackage(ctx, tx, it.ID)
			if err != nil {
				return nil, err
			}
			if len(ids) > 0 {
				rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title from elements e
					join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = any($1::uuid[]) order by s.key, e.number`, ids)
				if err != nil {
					return nil, err
				}
				epics, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Ref])
				if err != nil {
					return nil, err
				}
				for _, e := range epics {
					if ok, err := rd.can(ctx, tx, e.ID); err != nil {
						return nil, err
					} else if ok {
						it.Epics = append(it.Epics, e)
					}
				}
			}
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}
