package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Catalogo dell'assistente: i tipi di elemento del tenant con le loro fasi, raggruppate per
// categoria. Va nelle istruzioni (uguale fra le domande dello stesso tenant, quindi resta nella
// cache del prompt) così il modello scrive la query giusta al primo colpo invece di provare
// tipi e fasi a tentativi, un giro dopo l'altro. Niente conteggi: direbbero quanti elementi ci
// sono anche fra quelli che la persona non può vedere.

// categoryNames: le categorie delle fasi come le legge il modello.
var categoryNames = []struct{ Key, Name string }{
	{"todo", "da fare"},
	{"in_progress", "in corso"},
	{"done", "chiuse"},
	{"cancelled", "annullate"},
}

// maxCatalogTypes: oltre, il catalogo si tronca (la query resta possibile con «cerca»).
const maxCatalogTypes = 80

func assistantCatalog(ctx context.Context, tx pgx.Tx) (string, error) {
	rows, err := tx.Query(ctx, `select t.key, t.name, coalesce(o.category, 'todo'), o.name
		from element_types t
		left join options o on o.tenant_id = t.tenant_id and o.option_set_id = t.state_set_id and not o.archived
		where t.archived_at is null
		order by t.key, o.ordinal, lower(o.name)`)
	if err != nil {
		return "", err
	}
	type typeRow struct {
		key, name string
		phases    map[string][]string
	}
	var types []*typeRow
	for rows.Next() {
		var key, name, cat string
		var phase *string
		if err := rows.Scan(&key, &name, &cat, &phase); err != nil {
			rows.Close()
			return "", err
		}
		if len(types) == 0 || types[len(types)-1].key != key {
			types = append(types, &typeRow{key: key, name: name, phases: map[string][]string{}})
		}
		if phase != nil {
			t := types[len(types)-1]
			t.phases[cat] = append(t.phases[cat], *phase)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Tipi di elemento (chiave per «tipo:», nome) e fasi per categoria:\n")
	for i, t := range types {
		if i == maxCatalogTypes {
			fmt.Fprintf(&b, "(altri %d tipi)\n", len(types)-i)
			break
		}
		fmt.Fprintf(&b, "- %s (%s)", t.key, t.name)
		var parts []string
		for _, c := range categoryNames {
			if ps := t.phases[c.Key]; len(ps) > 0 {
				parts = append(parts, c.Name+": "+strings.Join(ps, ", "))
			}
		}
		if len(parts) > 0 {
			b.WriteString(" — " + strings.Join(parts, "; "))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
