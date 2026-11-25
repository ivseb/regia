package work

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Board paginata (senza sprint: la board legge tutto lo spazio). Due letture leggere al posto
// della lettura completa di ogni card:
//   1. i conteggi per (tipo, fase) con un solo group by: servono i totali esatti di ogni colonna
//      e le card nascoste (fasi senza colonna), qualunque pagina si chieda;
//   2. la classifica (id, colonna, posizione) con row_number() per colonna nell'ordine della board,
//      senza i join di dettaglio: se ne prendono le prime N card di ogni colonna, oppure le N
//      successive a una card (after) di una colonna sola. Solo per quegli id si leggono i dettagli
//      (padre, epic, figli), quindi il costo dei dettagli segue la pagina e non lo spazio.
// La posizione di una card si trova per id, non per scarto: spostamenti e nuove card tra una pagina
// e l'altra non producono doppioni.

// attachCardTags completa le etichette delle card con una query sola.
func attachCardTags(ctx context.Context, tx pgx.Tx, memberID string, cards []Card) error {
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	tags, err := engine.TagsOf(ctx, tx, memberID, ids)
	if err != nil {
		return err
	}
	for i := range cards {
		cards[i].Tags = tags[cards[i].ID]
	}
	return nil
}

func (s *Service) pagedCards(ctx context.Context, tx pgx.Tx, st *BoardSettings, out *Board, bq BoardQuery, from, where string, args []any,
	counts []int, cats []string, cardSelect string, scan func(pgx.Rows) error) error {
	limit := bq.Limit
	if limit <= 0 {
		limit = BoardPageSize
	}
	limit = min(limit, BoardMaxPage)
	out.PageSize = limit

	// 1. Conteggi per (tipo, fase) → per colonna.
	rows, err := tx.Query(ctx, `select t.key, coalesce(o.name, ''), coalesce(o.category, ''), count(*)::int
		from `+from+`
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where `+where+` group by 1, 2, 3`, args...)
	if err != nil {
		return err
	}
	var tks, sts []string
	var cols []int32
	var tk, name, cat string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&tk, &name, &cat, &n}, func() error {
		col := st.columnOf(tk, name)
		if col < 0 {
			out.Hidden += n
			return nil
		}
		counts[col] += n
		if cats[col] == "" || (cat == "in_progress" && cats[col] == "todo") {
			cats[col] = cat
		}
		if bq.Col == nil || *bq.Col == col {
			tks, sts, cols = append(tks, tk), append(sts, name), append(cols, int32(col))
		}
		return nil
	}); err != nil {
		return err
	}
	if len(tks) == 0 {
		return nil
	}

	// 2. Classifica: (id, colonna, posizione) nell'ordine della board.
	base := len(args)
	a := append(append([]any{}, args...), tks, sts, cols)
	rank := fmt.Sprintf(`cm as (select * from unnest($%d::text[], $%d::text[], $%d::int[]) as x(tk, st, col)),
		ranked as (
			select e.id, cm.col, row_number() over (partition by cm.col order by %s) as rn
			from %s
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			join cm on cm.tk = t.key and cm.st = coalesce(o.name, '')
			where %s)`, base+1, base+2, base+3, boardOrder(st), from, where)
	var sql string
	if bq.Col == nil {
		a = append(a, limit)
		sql = fmt.Sprintf(`with %s select id::text, col, rn from ranked where rn <= $%d order by col, rn`, rank, len(a))
	} else {
		var after any
		if bq.After != "" {
			after = bq.After
		}
		a = append(a, after, limit)
		sql = fmt.Sprintf(`with %s,
			pos as (select rn from ranked where $%d::text is not null and id::text = $%d::text)
			select coalesce(r.id::text, ''), coalesce(r.col, 0), coalesce(r.rn, 0), p.found
			from (select case when $%d::text is null then true else exists (select 1 from pos) end as found,
			             coalesce((select rn from pos), 0) as off) p
			left join ranked r on r.rn > p.off and r.rn <= p.off + $%d
			order by r.rn`, rank, len(a)-1, len(a)-1, len(a)-1, len(a))
	}
	rr, err := tx.Query(ctx, sql, a...)
	if err != nil {
		return err
	}
	var ids []string
	found := true
	var id string
	var col int32
	var rn int64
	if bq.Col == nil {
		_, err = pgx.ForEachRow(rr, []any{&id, &col, &rn}, func() error {
			ids = append(ids, id)
			return nil
		})
	} else {
		_, err = pgx.ForEachRow(rr, []any{&id, &col, &rn, &found}, func() error {
			if id != "" {
				ids = append(ids, id)
			}
			return nil
		})
	}
	if err != nil {
		return err
	}
	if !found {
		return engine.InvalidCode("validation.board.cursor", "after", "la card di partenza non è più in questa colonna: ricarica la board")
	}
	if len(ids) == 0 {
		return nil
	}

	// 3. Dettagli delle sole card della pagina, nell'ordine della classifica.
	dr, err := tx.Query(ctx, fmt.Sprintf(cardSelect, "elements e", "e.id = any($1::uuid[])", "array_position($1::uuid[], e.id)", 2), ids, len(ids))
	if err != nil {
		return err
	}
	if err := scan(dr); err != nil {
		return err
	}
	return nil
}
