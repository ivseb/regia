package work

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// I tipi del lavoro vengono dai dati: sono i tipi del modulo «work» (anche quelli creati
// dall'editor), senza lo sprint. Epic e sprint conservano le regole speciali per chiave; tutto
// il resto vale per qualunque tipo del modulo. Un tipo archiviato non è più proposto.

// Segnaposto per workItem: qualunque tipo del lavoro (epic compreso, sprint escluso) o qualunque
// elemento di lavoro (anche senza epic).
const (
	anyWork = "*work"
	anyItem = "*item"
)

// workItemTypes elenca le chiavi dei tipi del modulo work non archiviati, senza lo sprint (e senza
// l'epic se withEpic è falso): prima i tipi di sistema nel loro ordine, poi gli altri per nome.
func workItemTypes(ctx context.Context, tx pgx.Tx, withEpic bool) ([]string, error) {
	rows, err := tx.Query(ctx, `select key from element_types
		where module = 'work' and archived_at is null and key <> $1 and ($2 or key <> $3)
		order by coalesce(array_position($4::text[], key), 99), lower(name), key`,
		TypeSprint, withEpic, TypeEpic, []string{TypeEpic, TypeStory, TypeTask, TypeBug})
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// typeArchived dice se il tipo è archiviato (un tipo inesistente conta come archiviato).
func typeArchived(ctx context.Context, tx pgx.Tx, key string) bool {
	var archived bool
	err := tx.QueryRow(ctx, "select archived_at is not null from element_types where key = $1", key).Scan(&archived)
	return err != nil || archived
}

// parentTypesOf: i tipi che possono essere genitore. Epic non ha genitori; la story sta sotto un
// epic, il task sotto una story o un bug, il bug sotto un epic o una story; un tipo nuovo sotto un epic.
func parentTypesOf(key string) []string {
	switch key {
	case TypeEpic:
		return nil
	case TypeStory:
		return []string{TypeEpic}
	case TypeBug:
		return []string{TypeEpic, TypeStory}
	case TypeTask:
		return []string{TypeStory, TypeBug}
	}
	return []string{TypeEpic}
}

// matchesTypes: l'elemento è di uno dei tipi indicati (chiavi o segnaposto).
func matchesTypes(ctx context.Context, tx pgx.Tx, typeID, key string, types []string) (bool, error) {
	if slices.Contains(types, key) {
		return true, nil
	}
	if !slices.Contains(types, anyWork) && !slices.Contains(types, anyItem) {
		return false, nil
	}
	var module string
	if err := tx.QueryRow(ctx, "select module from element_types where id = $1", typeID).Scan(&module); err != nil {
		return false, err
	}
	if module != "work" || key == TypeSprint {
		return false, nil
	}
	return slices.Contains(types, anyWork) || key != TypeEpic, nil
}

// expandTypes sostituisce i segnaposto con le chiavi correnti, per i messaggi.
func expandTypes(ctx context.Context, tx pgx.Tx, types []string) string {
	out := []string{}
	for _, t := range types {
		switch t {
		case anyWork, anyItem:
			ks, _ := workItemTypes(ctx, tx, t == anyWork)
			out = append(out, ks...)
		default:
			out = append(out, t)
		}
	}
	return strings.Join(out, ", ")
}
