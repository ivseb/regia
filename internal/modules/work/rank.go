package work

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Ordinamento manuale con indice frazionario (elements.rank): stringhe di cifre in base 36
// confrontate byte per byte (collate "C"). Tra due chiavi qualsiasi ce n'è sempre un'altra,
// quindi spostare una card scrive una sola riga. Le chiavi non finiscono mai con "0".

const rankDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

// maxRankLen: oltre questa lunghezza (molti inserimenti nello stesso punto) si ridistribuiscono
// le chiavi dello spazio a passo costante.
const maxRankLen = 40

func digitOf(c byte) int { return strings.IndexByte(rankDigits, c) }

// rankBetween restituisce una chiave strettamente compresa tra a e b. a vuota = inizio,
// b vuota = fine. Con a >= b (dati incoerenti) restituisce una chiave subito dopo a.
func rankBetween(a, b string) string {
	if b != "" && a >= b {
		b = ""
	}
	return midpoint(a, b)
}

// midpoint: algoritmo delle chiavi frazionarie (come le cifre dopo la virgola).
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

// spreadRanks genera n chiavi crescenti a passo costante, tutte maggiori di after (vuota =
// dall'inizio) e senza limite superiore: servono a riempire o ridistribuire uno spazio.
func spreadRanks(after string, n int) []string {
	if n <= 0 {
		return nil
	}
	width := 1
	for math.Pow(36, float64(width)) < float64(n+1)*4 {
		width++
	}
	total := math.Pow(36, float64(width))
	step := total / float64(n+1)
	out := make([]string, n)
	for i := range n {
		v := int64(step * float64(i+1))
		s := strings.Repeat("0", width)
		buf := []byte(s)
		for p := width - 1; p >= 0 && v > 0; p-- {
			buf[p] = rankDigits[v%36]
			v /= 36
		}
		// La "i" finale evita lo zero in coda e lascia spazio per inserire prima e dopo.
		out[i] = after + string(buf) + "i"
	}
	return out
}

// fillRanks dà una chiave agli elementi dello spazio che non ce l'hanno (creati da altre
// parti dell'applicazione), in coda e nell'ordine di numero.
func fillRanks(ctx context.Context, tx pgx.Tx, spaceID string) error {
	rows, err := tx.Query(ctx, `select id::text from elements where space_id = $1 and rank is null and deleted_at is null and not is_draft
		order by number`, spaceID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return err
	}
	var last *string
	if err := tx.QueryRow(ctx, `select max(rank collate "C") from elements where space_id = $1 and deleted_at is null`, spaceID).Scan(&last); err != nil {
		return err
	}
	keys := spreadRanks(deref(last), len(ids))
	_, err = tx.Exec(ctx, `update elements e set rank = k.rank from unnest($1::uuid[], $2::text[]) as k(id, rank) where e.id = k.id`, ids, keys)
	return err
}

// rebalance ridistribuisce tutte le chiavi dello spazio a passo costante, mantenendo l'ordine.
func rebalance(ctx context.Context, tx pgx.Tx, spaceID string) error {
	rows, err := tx.Query(ctx, `select id::text from elements where space_id = $1 and deleted_at is null and not is_draft
		order by rank collate "C" nulls last, number`, spaceID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	keys := spreadRanks("", len(ids))
	_, err = tx.Exec(ctx, `update elements e set rank = k.rank from unnest($1::uuid[], $2::text[]) as k(id, rank) where e.id = k.id`, ids, keys)
	return err
}

// lastRank è la chiave per un elemento nuovo in fondo allo spazio.
func lastRank(ctx context.Context, tx pgx.Tx, spaceID string) (string, error) {
	var last *string
	if err := tx.QueryRow(ctx, `select max(rank collate "C") from elements where space_id = $1 and deleted_at is null`, spaceID).Scan(&last); err != nil {
		return "", err
	}
	return rankBetween(deref(last), ""), nil
}

// rankOf legge la chiave di un elemento dello spazio ("" se non c'è).
func rankOf(ctx context.Context, tx pgx.Tx, spaceID, id string) (string, bool, error) {
	var r *string
	err := tx.QueryRow(ctx, `select rank from elements where id::text = $1 and space_id = $2 and deleted_at is null`, id, spaceID).Scan(&r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return deref(r), true, err
}

// placeBetween calcola la chiave di el tra after e before (id di elementi dello stesso
// spazio, vuoti = inizio o fine) e la scrive. Gli elementi della board e del backlog sono
// filtrati: i vicini visti dall'utente non sono per forza consecutivi nello spazio, ma la
// chiave nuova sta comunque tra i due, quindi l'ordine visto resta quello voluto.
func placeBetween(ctx context.Context, tx pgx.Tx, spaceID, elementID, after, before string) (string, error) {
	if err := fillRanks(ctx, tx, spaceID); err != nil {
		return "", err
	}
	var a, b string
	if after != "" {
		r, ok, err := rankOf(ctx, tx, spaceID, after)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", engine.NotFound("after", "element", "elemento di riferimento non trovato nello spazio")
		}
		a = r
	}
	if before != "" {
		r, ok, err := rankOf(ctx, tx, spaceID, before)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", engine.NotFound("before", "element", "elemento di riferimento non trovato nello spazio")
		}
		b = r
	}
	switch {
	case after != "" && before == "":
		// In fondo alla lista vista: subito dopo "after" nello spazio (prima del successivo).
		var next *string
		if err := tx.QueryRow(ctx, `select min(rank collate "C") from elements where space_id = $1 and deleted_at is null
			and rank collate "C" > $2 and id::text <> $3`, spaceID, a, elementID).Scan(&next); err != nil {
			return "", err
		}
		b = deref(next)
	case after == "" && before != "":
		var prev *string
		if err := tx.QueryRow(ctx, `select max(rank collate "C") from elements where space_id = $1 and deleted_at is null
			and rank collate "C" < $2 and id::text <> $3`, spaceID, b, elementID).Scan(&prev); err != nil {
			return "", err
		}
		a = deref(prev)
	case after == "" && before == "":
		r, err := lastRank(ctx, tx, spaceID)
		if err != nil {
			return "", err
		}
		a, b = "", ""
		if _, err := tx.Exec(ctx, "update elements set rank = $2 where id::text = $1", elementID, r); err != nil {
			return "", err
		}
		return r, nil
	}
	if b != "" && a >= b {
		// Vicini incoerenti (filtri diversi): dopo "after".
		var next *string
		if err := tx.QueryRow(ctx, `select min(rank collate "C") from elements where space_id = $1 and deleted_at is null
			and rank collate "C" > $2 and id::text <> $3`, spaceID, a, elementID).Scan(&next); err != nil {
			return "", err
		}
		b = deref(next)
	}
	r := rankBetween(a, b)
	if len(r) > maxRankLen {
		if err := rebalance(ctx, tx, spaceID); err != nil {
			return "", err
		}
		return placeBetween(ctx, tx, spaceID, elementID, after, before)
	}
	tag, err := tx.Exec(ctx, "update elements set rank = $3 where id::text = $1 and space_id = $2", elementID, spaceID, r)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("elemento %s non nello spazio", elementID)
	}
	return r, nil
}
