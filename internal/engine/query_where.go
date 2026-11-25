package engine

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// QueryPredicate compila una query del linguaggio in un predicato SQL su "e" (elements),
// con "s" (spaces) già in join, per le viste dei moduli che leggono un insieme limitato di
// elementi e devono filtrarlo con la stessa barra della query (es. la board di un team).
// args sono gli argomenti già usati dalla query del chiamante: quelli del predicato si
// aggiungono in coda, con la numerazione che prosegue. L'ordinamento della query si ignora
// (la vista ha il suo) e il testo libero si valuta riga per riga: va bene finché l'insieme
// di partenza è piccolo (qualche migliaio di righe, già ristretto dagli indici della vista).
// Query vuota: predicato "true".
func (s *Service) QueryPredicate(ctx context.Context, tx pgx.Tx, actor Actor, q string, args []any) (string, []any, error) {
	pq, err := Parse(q)
	if err != nil {
		return "", nil, err
	}
	pl, err := s.plan(ctx, tx, actor, pq)
	if err != nil {
		return "", nil, err
	}
	sb := &sqlb{args: args}
	if pl.none {
		return "false", sb.args, nil
	}
	where := []string{"true"}
	for i := range pl.conds {
		where = append(where, pl.conds[i].pred(sb))
	}
	if pl.key != nil {
		where = append(where, "(s.key = "+sb.arg(pl.key.space)+" and e.number = "+sb.arg(pl.key.number)+")")
	} else if pl.text != "" || pl.prefix != "" {
		where = append(where, textPred(sb, pl.text, pl.prefix))
	}
	out := "(" + where[0]
	for _, w := range where[1:] {
		out += " and " + w
	}
	return out + ")", sb.args, nil
}
