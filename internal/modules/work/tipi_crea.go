package work

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CreateType è un tipo che si può creare in uno spazio: l'interfaccia lo propone nel dialogo
// «Nuovo elemento» con i campi che gli sono associati.
type CreateType struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Color   string   `json:"color,omitempty"`
	Parents []string `json:"parents"` // tipi ammessi come genitore
	Points  bool     `json:"points"`  // ha il campo «punti» tra quelli associati
}

// createTypesOf: i tipi indicati (nell'ordine dato) con nome, genitori ammessi e se hanno i punti.
// I nomi nella lingua dell'utente li risolve l'interfaccia (useTypeNames).
func createTypesOf(ctx context.Context, tx pgx.Tx, keys []string) ([]CreateType, error) {
	out := []CreateType{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select t.key, t.name, coalesce(t.color, ''),
		exists (select 1 from type_fields tf join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
		         where tf.type_id = t.id and tf.space_id is null and f.key = $2 and not f.archived)
		from element_types t where t.key = any($1) and t.archived_at is null`, keys, FieldPoints)
	if err != nil {
		return nil, err
	}
	byKey := map[string]CreateType{}
	var c CreateType
	if _, err := pgx.ForEachRow(rows, []any{&c.Key, &c.Name, &c.Color, &c.Points}, func() error {
		c.Parents = parentTypesOf(c.Key)
		if c.Parents == nil {
			c.Parents = []string{}
		}
		byKey[c.Key] = c
		return nil
	}); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if t, ok := byKey[k]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}
