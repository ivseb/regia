package engine

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// LinkedElement è un elemento collegato (genitore, figlio o relazione) da mostrare nel dettaglio.
type LinkedElement struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Title         string `json:"title"`
	Type          string `json:"type"`
	TypeName      string `json:"type_name"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
	// Relation: per le relazioni, il nome nel verso dell'elemento (es. "dipende da").
	Relation string `json:"relation,omitempty"`
}

// Links sono i collegamenti di un elemento.
type Links struct {
	Parent    *LinkedElement  `json:"parent"`
	Children  []LinkedElement `json:"children"`
	Relations []LinkedElement `json:"relations"`
}

const linkedColumns = `e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, '')`
const linkedFrom = `
	from elements e
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id`

func scanLinked(r pgx.CollectableRow) (LinkedElement, error) {
	var l LinkedElement
	err := r.Scan(&l.ID, &l.Key, &l.Title, &l.Type, &l.TypeName, &l.State, &l.StateCategory, &l.Relation)
	return l, err
}

// ElementLinks restituisce genitore, figli (al massimo 200) e relazioni nei due versi.
// Chi non ha element.read (richiedente del portale) vede solo ciò che potrebbe aprire.
func (s *Service) ElementLinks(ctx context.Context, tx pgx.Tx, actor Actor, el *Element) (*Links, error) {
	out := &Links{Children: []LinkedElement{}, Relations: []LinkedElement{}}
	// Con spazi riservati o ruoli sugli spazi si controlla ogni collegamento (space_access.go).
	sc, err := s.scopeOf(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	visible := func(list []LinkedElement) ([]LinkedElement, error) {
		if actor.Has("element.read") && len(sc.hidden) == 0 {
			return list, nil
		}
		keep := list[:0]
		for _, l := range list {
			ok, err := s.CanRead(ctx, tx, actor, l.ID)
			if err != nil {
				return nil, err
			}
			if ok {
				keep = append(keep, l)
			}
		}
		return keep, nil
	}
	if el.ParentID != nil {
		rows, err := tx.Query(ctx, `select `+linkedColumns+`, ''`+linkedFrom+` where e.id = $1 and e.deleted_at is null`, *el.ParentID)
		if err != nil {
			return nil, err
		}
		parents, err := pgx.CollectRows(rows, scanLinked)
		if err != nil {
			return nil, err
		}
		if parents, err = visible(parents); err != nil {
			return nil, err
		}
		if len(parents) > 0 {
			out.Parent = &parents[0]
		}
	}
	rows, err := tx.Query(ctx, `select `+linkedColumns+`, ''`+linkedFrom+`
		where e.parent_id = $1 and e.deleted_at is null and not e.is_draft order by t.name, e.number limit 200`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Children, err = pgx.CollectRows(rows, scanLinked); err != nil {
		return nil, err
	}
	if out.Children, err = visible(out.Children); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `
		select `+linkedColumns+`, rt.outward_name`+linkedFrom+`
		join relations r on r.tenant_id = e.tenant_id and r.target_id = e.id
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
		where r.source_id = $1 and e.deleted_at is null
		union all
		select `+linkedColumns+`, rt.inward_name`+linkedFrom+`
		join relations r on r.tenant_id = e.tenant_id and r.source_id = e.id
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
		where r.target_id = $1 and e.deleted_at is null
		limit 200`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Relations, err = pgx.CollectRows(rows, scanLinked); err != nil {
		return nil, err
	}
	if out.Relations, err = visible(out.Relations); err != nil {
		return nil, err
	}
	return out, nil
}
