package portfolio

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// La scorecard di partenza non è nel codice: è il pacchetto starter/scorecard_portafoglio.json,
// pubblicato come versione 1 alla creazione del tenant e poi modificabile (nuove versioni).
//
//go:embed starter/scorecard_portafoglio.json
var starterScorecard []byte

// Bundle è il formato di scambio di una scorecard (regia.scorecard/v1).
type Bundle struct {
	Format     string     `json:"format"`
	Key        string     `json:"key"`
	Type       string     `json:"type"`
	Notes      string     `json:"notes"`
	Definition Definition `json:"definition"`
}

// Provision configura il modulo in un tenant: scorecard di partenza e impostazioni della
// graduatoria. È idempotente e va eseguito dopo quello del demand (serve il tipo).
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	var b Bundle
	if err := json.Unmarshal(starterScorecard, &b); err != nil {
		return err
	}
	if b.Format != "regia.scorecard/v1" {
		return fmt.Errorf("scorecard di partenza: formato %q", b.Format)
	}
	if ps := b.Definition.validate(); len(ps) > 0 {
		return fmt.Errorf("scorecard di partenza non valida: %v", ps)
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from scorecards where key = $1)", b.Key).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		var typeID string
		if err := tx.QueryRow(ctx, "select id from element_types where key = $1", b.Type).Scan(&typeID); err != nil {
			return fmt.Errorf("scorecard di partenza: manca il tipo %q (provisioning del demand)", b.Type)
		}
		criteria, _ := json.Marshal(b.Definition.Criteria)
		if _, err := tx.Exec(ctx, `insert into scorecards (tenant_id, id, key, version, status, type_id, name, description, method,
			scorer_roles, criteria, notes, created_by, published_by, published_at)
			values (app_tenant(), $1, $2, 1, 'published', $3, $4, $5, $6, $7, $8, $9, $10, $10, now())`,
			db.NewID(), b.Key, typeID, b.Definition.Name, b.Definition.Description, b.Definition.Method,
			b.Definition.ScorerRoles, criteria, b.Notes, nullable(adminMemberID)); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `insert into portfolio_settings (tenant_id) values (app_tenant()) on conflict do nothing`)
	return err
}
