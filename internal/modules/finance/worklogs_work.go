package finance

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// CostWorkLogs calcola le voci di costo delle ore indicate: le ore registrate dal work
// management (card, dettaglio) diventano costi con lo stesso calcolo delle ore inserite dalla
// pagina Finanza (tariffa della persona o del ruolo alla data, cambio del giorno).
//
// È un passo di sistema, come il worker: chi registra le ore non vede tariffe e costi, e la
// funzione non restituisce nulla di riservato. La transazione va aperta con
// db.TenantOpts{Finance: true}, separata da quella che scrive le ore. Senza la categoria dei
// costi delle ore (modulo non configurato) le ore restano senza costo, come senza tariffa.
func CostWorkLogs(ctx context.Context, tx pgx.Tx, memberID string, ids []string) (RecomputeResult, error) {
	if len(ids) == 0 {
		return RecomputeResult{}, nil
	}
	s := &Service{}
	res, err := s.recompute(ctx, tx, engine.Actor{MemberID: memberID}, workLogScope{ids: ids})
	var ve *engine.ValidationError
	if errors.As(err, &ve) && ve.Field == "labor_category_id" {
		return RecomputeResult{WithoutRate: int64(len(ids))}, nil
	}
	return res, err
}
