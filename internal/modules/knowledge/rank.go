package knowledge

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Ordinamento manuale delle pagine tra fratelli con indice frazionario (elements.rank, base 36,
// confronto "C"), come board e backlog del work management: spostare una pagina scrive una riga.

const rankDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

func digitOf(c byte) int { return strings.IndexByte(rankDigits, c) }

// rankBetween restituisce una chiave strettamente compresa tra a e b (vuote = estremi).
func rankBetween(a, b string) string {
	if b != "" && a >= b {
		b = ""
	}
	return midpoint(a, b)
}

func midpoint(a, b string) string {
	if b != "" {
		n := 0
		for n < len(b) {
			ca := byte('0')
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			ra := ""
			if n < len(a) {
				ra = a[n:]
			}
			return b[:n] + midpoint(ra, b[n:])
		}
	}
	da := 0
	if a != "" {
		da = digitOf(a[0])
	}
	db := len(rankDigits)
	if b != "" {
		db = digitOf(b[0])
	}
	if db-da > 1 {
		return string(rankDigits[(da+db+1)/2])
	}
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	ra := ""
	if len(a) > 1 {
		ra = a[1:]
	}
	return string(rankDigits[da]) + midpoint(ra, "")
}

// placeAmong calcola il rank della pagina id tra i figli di parentID: dopo after, prima di
// before, oppure in fondo. after e before sono chiavi o id di pagine sorelle.
func placeAmong(ctx context.Context, tx pgx.Tx, spaceID, id, parentID, after, before string) (string, error) {
	sibling := func(ref string) (string, error) {
		var r *string
		err := tx.QueryRow(ctx, `select e.rank from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.space_id = $1 and e.parent_id is not distinct from $2::uuid and e.deleted_at is null and e.id <> $3
			  and (e.id::text = $4 or s.key || '-' || e.number = upper($4))`, spaceID, nullable(parentID), id, ref).Scan(&r)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", engine.InvalidCode("validation.knowledge.position_reference", "position", "la pagina di riferimento non è tra le sorelle")
		}
		return deref(r), err
	}
	neighbour := func(cmp, order, from string) (string, error) {
		var r *string
		err := tx.QueryRow(ctx, `select rank from elements
			where space_id = $1 and parent_id is not distinct from $2::uuid and deleted_at is null and id <> $3
			  and rank is not null and ($4 = '' or rank collate "C" `+cmp+` $4)
			order by rank collate "C" `+order+` limit 1`, spaceID, nullable(parentID), id, from).Scan(&r)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return deref(r), err
	}
	var a, b string
	var err error
	switch {
	case after != "":
		if a, err = sibling(after); err != nil {
			return "", err
		}
		if a != "" {
			b, err = neighbour(">", "asc", a)
		}
	case before != "":
		if b, err = sibling(before); err != nil {
			return "", err
		}
		if b != "" {
			a, err = neighbour("<", "desc", b)
		}
	default:
		a, err = neighbour(">", "desc", "")
	}
	if err != nil {
		return "", err
	}
	return rankBetween(a, b), nil
}
