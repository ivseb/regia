package work

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// MyItem è un elemento assegnato a chi guarda.
type MyItem struct {
	ID       string       `json:"id"`
	Key      string       `json:"key"`
	Title    string       `json:"title"`
	Type     string       `json:"type"`
	TypeName string       `json:"type_name"`
	State    string       `json:"state"`
	Category string       `json:"category"`
	Space    string       `json:"space"`
	Priority *string      `json:"priority"` // nome dell'opzione
	Color    *string      `json:"priority_color"`
	Points   *float64     `json:"points"`
	Due      *string      `json:"due"`
	Sprint   *string      `json:"sprint"`
	Updated  string       `json:"updated_at"`
	Tags     []engine.Tag `json:"tags,omitempty"` // etichette
}

// MyWork elenca gli elementi assegnati all'attore in tutti gli spazi (non solo del lavoro dei
// team: anche impatti e valutazioni), aperti più quelli chiusi negli ultimi 7 giorni.
// Il raggruppamento per fase lo fa l'interfaccia, nell'ordine delle categorie.
func (s *Service) MyWork(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]MyItem, error) {
	rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, ''), s.key,
		       po.name, po.color,
		       case when jsonb_typeof(e.fields->'`+FieldPoints+`') = 'number' then (e.fields->>'`+FieldPoints+`')::float8 end,
		       to_char(e.due_at at time zone 'UTC', 'YYYY-MM-DD'),
		       (select ss.key || '-' || sp.number from relations r
		          join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		          join elements sp on sp.tenant_id = r.tenant_id and sp.id = r.target_id
		          join spaces ss on ss.tenant_id = sp.tenant_id and ss.id = sp.space_id
		         where r.source_id = e.id limit 1),
		       to_char(e.updated_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join options po on po.tenant_id = e.tenant_id and po.id::text = e.fields->>'`+FieldPriority+`'
		where e.assignee_id = $1 and e.deleted_at is null and not e.is_draft
		  and (e.resolved_at is null or e.resolved_at > now() - interval '7 days')
		order by case o.category when 'in_progress' then 0 when 'todo' then 1 when 'done' then 2 else 3 end,
		         o.ordinal nulls last, e.rank collate "C" nulls last, e.updated_at desc
		limit 1000`, actor.MemberID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MyItem, error) {
		var m MyItem
		err := r.Scan(&m.ID, &m.Key, &m.Title, &m.Type, &m.TypeName, &m.State, &m.Category, &m.Space, &m.Priority, &m.Color,
			&m.Points, &m.Due, &m.Sprint, &m.Updated)
		return m, err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []MyItem{}
	}
	ids := make([]string, len(out))
	for i, m := range out {
		ids[i] = m.ID
	}
	tags, err := engine.TagsOf(ctx, tx, actor.MemberID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tags[out[i].ID]
	}
	return out, nil
}
