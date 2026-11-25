package engine

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Tag è un'etichetta (tag) di un elemento, in forma compatta per le liste.
type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// TagsOf dà le etichette degli elementi indicati con una sola query aggregata (nessun N+1): quelle
// condivise del tenant e le personali di chi guarda. Le righe di element_tags si leggono dalla
// chiave primaria (tenant, elemento, etichetta), quindi il costo segue la pagina e non lo spazio.
func TagsOf(ctx context.Context, tx pgx.Tx, memberID string, ids []string) (map[string][]Tag, error) {
	out := map[string][]Tag{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select et.element_id::text, t.id::text, t.name, coalesce(t.color, '')
		from element_tags et
		join tags t on t.tenant_id = et.tenant_id and t.id = et.tag_id
		where et.element_id = any($1::uuid[]) and (t.owner_id is null or t.owner_id::text = $2)
		order by lower(t.name), t.id`, ids, memberID)
	if err != nil {
		return nil, err
	}
	var el string
	var tg Tag
	_, err = pgx.ForEachRow(rows, []any{&el, &tg.ID, &tg.Name, &tg.Color}, func() error {
		out[el] = append(out[el], tg)
		return nil
	})
	return out, err
}

// AttachTags completa le etichette degli elementi di una pagina di risultati.
func AttachTags(ctx context.Context, tx pgx.Tx, memberID string, items []*Element) error {
	ids := make([]string, len(items))
	for i, el := range items {
		ids[i] = el.ID
	}
	m, err := TagsOf(ctx, tx, memberID, ids)
	if err != nil {
		return err
	}
	for _, el := range items {
		el.Tags = m[el.ID]
	}
	return nil
}
