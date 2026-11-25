package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Ricerca con il linguaggio di query: sintassi in query_lang.go, campi in
// query_fields.go, piano e strategie di prestazione in query_plan.go,
// autocompletamento in query_suggest.go.

// Page è una pagina di risultati con il cursore per la successiva.
type Page struct {
	Items []*Element `json:"items"`
	Next  string     `json:"next,omitempty"`
	// Labels: etichette degli elementi e dei gruppi citati dai campi personalizzati degli
	// elementi della pagina (colonne della tabella), per id.
	Labels map[string]RefLabel `json:"labels,omitempty"`
}

// cursor: gruppo dell'ordinamento, valore di ordinamento e id dell'ultimo elemento.
type cursor struct {
	B  int    `json:"b,omitempty"`
	V  string `json:"v"`
	ID string `json:"id"`
}

// Search esegue la query rispettando la visibilità dell'attore. Paginazione a cursore
// (keyset): costo costante anche nelle pagine lontane.
func (s *Service) Search(ctx context.Context, tx pgx.Tx, actor Actor, q string, limit int, after string) (Page, error) {
	pg, err := s.search(ctx, tx, actor, q, limit, after)
	if err != nil || after != "" {
		return pg, err
	}
	// Una sola parola che è il codice di catalogo di un'applicazione o di una capability
	// (APP-MOB) trova l'elemento, come la chiave (ARC-8): in testa ai risultati.
	if pq, perr := Parse(q); perr == nil && len(pq.Clauses) == 0 && len(pq.Text) == 1 && !pq.Text[0].Quoted {
		if id := s.catalogCodeID(ctx, tx, pq.Text[0].Text); id != "" {
			for _, it := range pg.Items {
				if it.ID == id {
					return pg, nil
				}
			}
			if el, gerr := s.Get(ctx, tx, actor, id); gerr == nil {
				pg.Items = append([]*Element{el}, pg.Items...)
			}
		}
	}
	return pg, nil
}

func (s *Service) search(ctx context.Context, tx pgx.Tx, actor Actor, q string, limit int, after string) (Page, error) {
	pq, err := Parse(q)
	if err != nil {
		return Page{}, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var cur *cursor
	if after != "" {
		c, err := decodeCursor(after)
		if err != nil {
			return Page{}, InvalidValue("cursor", "cursore non valido")
		}
		cur = &c
	}
	pl, err := s.plan(ctx, tx, actor, pq)
	if err != nil {
		return Page{}, err
	}
	if cur != nil && cur.B >= len(pl.buckets) {
		return Page{}, InvalidValue("cursor", "cursore non valido per questa query")
	}
	empty := Page{Items: []*Element{}}
	// Filtri selettivi risolti in id (nil = nessun vincolo).
	ids, err := pl.resolve(ctx, tx)
	if errors.Is(err, errNoMatch) {
		return empty, nil
	}
	if err != nil {
		return Page{}, err
	}
	mode, err := s.textStrategy(ctx, tx, pl)
	if err != nil {
		return Page{}, err
	}
	if mode.ids != nil {
		if ids = intersect(ids, mode.ids); len(ids) == 0 {
			return empty, nil
		}
	}
	return s.runBuckets(ctx, tx, actor, pl, ids, mode.mode, cur, limit)
}

type textChoice struct {
	mode textMode
	ids  []string // per textIDs: le corrispondenze trovate con l'indice GIN
}

// textStrategy sceglie come cercare il testo libero. Con la RLS l'indice GIN non è
// utilizzabile nella query normale (migrazione 0006), quindi si sceglie la strategia in
// base alla frequenza stimata del termine (fts_frequency, migrazione 0007):
//   - termine frequente (>= FTSDenseFrequency): si scorre l'indice ordinato con il
//     filtro full-text; la pagina si riempie dopo poche centinaia di righe;
//   - termine raro: fts_match usa l'indice GIN sul solo tenant corrente, già combinato
//     con tipo, stato e spazio, e la pagina si costruisce su quegli id. Se gli id sono
//     troppi (stima sbagliata) si torna allo scorrimento.
//
// I prefissi ("regis*", migrazione 0015) non hanno statistiche: con almeno tre lettere
// si prova l'indice GIN (fts_match_prefix), altrimenti si scorre l'indice ordinato.
func (s *Service) textStrategy(ctx context.Context, tx pgx.Tx, pl *plan) (textChoice, error) {
	if pl.key != nil || (pl.text == "" && pl.prefix == "") {
		return textChoice{mode: textNone}, nil
	}
	var rows pgx.Rows
	var err error
	if pl.prefix == "" {
		var freq float64
		if err := tx.QueryRow(ctx, "select fts_frequency($1)", pl.text).Scan(&freq); err != nil {
			return textChoice{}, err
		}
		if freq >= FTSDenseFrequency {
			return textChoice{mode: textWalk}, nil
		}
		rows, err = tx.Query(ctx, `select fts_match($1, $2, $3, $4, $5, $6)`,
			pl.text, ftsMaxIDs, nilIfEmpty(pl.ftsTypes), nilIfEmpty(pl.ftsStates), nilIfEmpty(pl.ftsSpaces), pl.ftsOpen)
	} else {
		if shortestPrefix(pl.prefix) < 3 {
			return textChoice{mode: textWalk}, nil
		}
		rows, err = tx.Query(ctx, `select fts_match_prefix($1, $2, $3, $4, $5, $6, $7)`,
			pl.text, pl.prefix, ftsMaxIDs, nilIfEmpty(pl.ftsTypes), nilIfEmpty(pl.ftsStates), nilIfEmpty(pl.ftsSpaces), pl.ftsOpen)
	}
	if err != nil {
		return textChoice{}, err
	}
	matches, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return textChoice{}, err
	}
	if len(matches) >= ftsMaxIDs {
		return textChoice{mode: textWalk}, nil
	}
	if matches == nil {
		matches = []string{}
	}
	return textChoice{mode: textIDs, ids: matches}, nil
}

// shortestPrefix: lunghezza (in lettere) del prefisso più corto di una tsquery "a:* & b:*".
func shortestPrefix(tsq string) int {
	n := 1 << 30
	for _, p := range splitAnd(tsq) {
		if l := len([]rune(p)) - 2; l < n {
			n = l
		}
	}
	return n
}

func splitAnd(tsq string) []string {
	var out []string
	cur := ""
	for _, r := range tsq {
		switch r {
		case ' ':
		case '&':
			out = append(out, cur)
			cur = ""
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// runBuckets legge la pagina scorrendo i gruppi dell'ordinamento a partire dal cursore.
func (s *Service) runBuckets(ctx context.Context, tx pgx.Tx, actor Actor, pl *plan, ids []string, mode textMode, cur *cursor, limit int) (Page, error) {
	type mark struct {
		b int
		v string
	}
	items := []*Element{}
	var marks []mark
	start := 0
	if cur != nil {
		start = cur.B
	}
	for bi := start; bi < len(pl.buckets) && len(items) <= limit; bi++ {
		bk := &pl.buckets[bi]
		bids := ids
		if bk.extra != nil && bk.extra.res != nil {
			r, dense, err := resolveIDs(ctx, tx, bk.extra)
			if err != nil {
				return Page{}, err
			}
			if !dense {
				if bids = intersect(bids, r); len(bids) == 0 {
					continue
				}
			}
		}
		var c *cursor
		if cur != nil && bi == start {
			c = cur
		}
		sql, args := buildBucket(pl, bk, actor, bids, mode, c, limit+1-len(items))
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return Page{}, err
		}
		for rows.Next() {
			var v string
			el, err := scanElementWith(rows, &v)
			if err != nil {
				rows.Close()
				return Page{}, err
			}
			items = append(items, el)
			marks = append(marks, mark{b: bi, v: v})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Page{}, err
		}
	}
	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		m := marks[limit-1]
		page.Next = encodeCursor(cursor{B: m.b, V: m.v, ID: page.Items[limit-1].ID})
	}
	return page, nil
}

func nilIfEmpty(ids []string) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// FTSDenseFrequency è la frequenza (quota di elementi che contengono il termine) oltre
// la quale conviene scorrere l'indice ordinato invece di leggere l'indice GIN. Con 0,5%
// lo scorrimento esamina in media 51/0,005 ≈ 10.000 righe nel caso peggiore.
// Regolabile per i test: 0 = sempre scorrimento, >1 = sempre indice GIN.
var FTSDenseFrequency = 0.005

const (
	// ftsMaxIDs: massimo di corrispondenze lette dall'indice GIN e rilette per id.
	// Raggiunto il massimo, le corrispondenze sono dense nel filtro: si scorre l'indice
	// ordinato senza limite di tempo, sapendo che la pagina si riempie presto.
	ftsMaxIDs = 2000
)

type textMode int

const (
	textNone textMode = iota
	textWalk          // filtro "search @@" valutato scorrendo l'indice ordinato
	textIDs           // filtro sugli id trovati da fts_match (intersecati con gli altri id)
)

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, error) {
	var c cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if !uuidRe.MatchString(c.ID) || c.B < 0 {
		return c, fmt.Errorf("id non valido")
	}
	return c, nil
}
