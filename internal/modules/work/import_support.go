package work

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Funzioni esposte per l'import di progetti da altri strumenti (internal/modules/importer).

// FillRanks mette in fondo all'ordine manuale dello spazio gli elementi senza posizione,
// nell'ordine del numero (come alla creazione dalla board).
func FillRanks(ctx context.Context, tx pgx.Tx, spaceID string) error {
	return fillRanks(ctx, tx, spaceID)
}

// TypeKeys sono i tipi del work management ammessi negli spazi dei team.
func TypeKeys() []string { return typeKeys() }
