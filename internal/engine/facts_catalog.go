package engine

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// TypeFacts elenca i fatti che un tipo di elemento offre alle condizioni (gli stessi del
// costruttore di condizioni dei processi), con un elemento d'esempio se esiste. Serve ai moduli
// che hanno condizioni proprie, come il gate di rilascio.
func (s *Service) TypeFacts(ctx context.Context, tx pgx.Tx, actor Actor, typeKey string) ([]FactInfo, string, error) {
	var id string
	if err := tx.QueryRow(ctx, "select id::text from element_types where key = $1", typeKey).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []FactInfo{}, "", nil
		}
		return nil, "", err
	}
	t, err := loadTypeByID(ctx, tx, id)
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.Query(ctx, `select key, name from stakeholder_roles order by name`)
	if err != nil {
		return nil, "", err
	}
	roles, err := pgx.CollectRows(rows, pgx.RowToStructByPos[NamedKey])
	if err != nil {
		return nil, "", err
	}
	return s.availableFacts(ctx, tx, actor, t, roles)
}
