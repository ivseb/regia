package engine

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// maxCountElements è quanti elementi si valutano per il conteggio di una condizione: oltre,
// il risultato dice «almeno» (Truncated) senza far attendere chi sta scrivendo la condizione.
const maxCountElements = 300

// ConditionItem è un elemento che soddisfa una condizione, per l'anteprima in parole.
type ConditionItem struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// ConditionCount dice quanti elementi di un tipo soddisfano oggi una condizione.
type ConditionCount struct {
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Matched   int             `json:"matched"`
	Total     int             `json:"total"`
	Truncated bool            `json:"truncated,omitempty"`
	Items     []ConditionItem `json:"items"`
}

// CountCondition valuta la condizione sugli elementi (del tipo dato, o di tutti i tipi se vuoto)
// che la persona può leggere, esclusi cancellati e bozze personali. Una condizione vuota è
// vera per tutti. Con una condizione non valida risponde ok=false e l'errore, senza contare.
func (s *Service) CountCondition(ctx context.Context, tx pgx.Tx, actor Actor, src, typeKey string) (*ConditionCount, error) {
	out := &ConditionCount{Items: []ConditionItem{}}
	c, err := ParseCondition(src)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.OK = true
	rows, err := tx.Query(ctx, `select e.id::text from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where ($1 = '' or t.key = $1) and e.deleted_at is null and not e.is_draft and e.number is not null
		order by e.number desc limit $2`, typeKey, maxCountElements+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(ids) > maxCountElements {
		ids, out.Truncated = ids[:maxCountElements], true
	}
	for _, id := range ids {
		el, err := s.Get(ctx, tx, actor, id)
		if errors.Is(err, ErrNotFound) {
			continue // non leggibile da questa persona
		}
		if err != nil {
			return nil, err
		}
		out.Total++
		f, err := s.ElementFacts(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		if c.Eval(f) {
			out.Matched++
			if len(out.Items) < 5 {
				out.Items = append(out.Items, ConditionItem{Key: el.Key, Title: el.Title})
			}
		}
	}
	return out, nil
}
