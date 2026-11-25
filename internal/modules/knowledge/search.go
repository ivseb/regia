package knowledge

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Ricerca full-text dentro uno spazio, con estratti evidenziati. Le corrispondenze arrivano
// dall'indice GIN tramite fts_match e fts_match_prefix (sotto RLS non si usa @@ direttamente,
// migrazione 0006): parole intere con la radice italiana e prefissi mentre si scrive. Gli
// estratti li calcola ts_headline sulle sole pagine trovate.

// Segment è un pezzo di estratto; Hit = parola cercata.
type Segment struct {
	Text string `json:"t"`
	Hit  bool   `json:"hit,omitempty"`
}

// SearchHit è una pagina trovata.
type SearchHit struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Excerpt   []Segment `json:"excerpt"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	hlStart = "\x02"
	hlStop  = "\x03"
)

// prefixQuery costruisce "parola:* & altra:*" (lettere e cifre, al massimo 8 parole).
func prefixQuery(q string) string {
	var parts []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(parts) == 8 {
			break
		}
		parts = append(parts, w+":*")
	}
	return strings.Join(parts, " & ")
}

// Search cerca nelle pagine dello spazio (titolo e testo).
func (s *Service) Search(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey, q string) ([]SearchHit, error) {
	sp, err := s.readableSpace(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	out := []SearchHit{}
	q = strings.TrimSpace(q)
	prefix := prefixQuery(q)
	if len([]rune(q)) < 2 || prefix == "" {
		return out, nil
	}
	var typeID string
	if err := tx.QueryRow(ctx, `select id::text from element_types where key = $1`, TypePage).Scan(&typeID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select x::text from fts_match($1, 500, array[$3::uuid], null, array[$4::uuid], null) as x
		union
		select y::text from fts_match_prefix('', $2, 500, array[$3::uuid], null, array[$4::uuid], null) as y`,
		q, prefix, typeID, sp.ID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return out, err
	}
	opts := "StartSel=" + hlStart + ", StopSel=" + hlStop + ", MaxWords=28, MinWords=10, MaxFragments=2, FragmentDelimiter=" + " … "
	rows, err = tx.Query(ctx, `
		select s.key || '-' || e.number, e.title, coalesce(o.name, ''),
		       ts_headline('simple', left(e.body_text, 100000), to_tsquery('simple', $2), $3), e.updated_at
		from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.id = any($1::uuid[]) and e.deleted_at is null
		order by ts_rank(e.search, websearch_to_tsquery('simple', unaccent($4)) || to_tsquery('simple', unaccent($2))) desc, e.updated_at desc
		limit 50`, ids, prefix, opts, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		var hl string
		if err := rows.Scan(&h.Key, &h.Title, &h.State, &hl, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Excerpt = segments(hl)
		out = append(out, h)
	}
	return out, rows.Err()
}

// segments divide l'estratto di ts_headline nei pezzi evidenziati e no.
func segments(hl string) []Segment {
	var out []Segment
	for hl != "" {
		i := strings.Index(hl, hlStart)
		if i < 0 {
			out = append(out, Segment{Text: hl})
			break
		}
		if i > 0 {
			out = append(out, Segment{Text: hl[:i]})
		}
		hl = hl[i+len(hlStart):]
		j := strings.Index(hl, hlStop)
		if j < 0 {
			out = append(out, Segment{Text: hl, Hit: true})
			break
		}
		out = append(out, Segment{Text: hl[:j], Hit: true})
		hl = hl[j+len(hlStop):]
	}
	if out == nil {
		out = []Segment{}
	}
	return out
}
