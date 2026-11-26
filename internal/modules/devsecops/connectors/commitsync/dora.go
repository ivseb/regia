package commitsync

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// RecomputeFirst ricalcola subito le metriche DORA del tenant se non ce n'è ancora nessuna:
// dopo la prima sincronizzazione riuscita di un connettore la pagina non deve dire «non ancora
// calcolate» fino al giro orario. Un errore è solo un avviso per chi chiama.
func RecomputeFirst(ctx context.Context, d *db.DB, tenantID string, ing any) error {
	rc, ok := ing.(contract.DoraRecomputer)
	if !ok {
		return nil
	}
	return d.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := rc.RecomputeDoraIfEmpty(ctx, tx)
		return err
	})
}
