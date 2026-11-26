package work

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckDayOpen dice se le ore di una persona in un giorno si possono ancora registrare:
// no se il periodo è chiuso o se la settimana del timesheet è inviata o approvata (stessa
// regola della registrazione normale). Lo usa l'import CSV delle ore.
func CheckDayOpen(ctx context.Context, tx pgx.Tx, memberID string, day time.Time) error {
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return err
	}
	return checkOpen(ctx, tx, st, memberID, day)
}
