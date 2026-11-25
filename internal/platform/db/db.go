// Package db apre i pool di connessione e fornisce la transazione "con tenant":
// l'unico modo in cui il codice applicativo tocca i dati di un tenant.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB tiene due pool separati:
//   - App: ruolo applicativo, senza BYPASSRLS; la RLS vale sempre.
//   - Owner: ruolo di piattaforma (migrazioni, creazione tenant, verifica audit, job).
type DB struct {
	App   *pgxpool.Pool
	Owner *pgxpool.Pool
}

func Open(ctx context.Context, ownerURL, appURL string) (*DB, error) {
	owner, err := newPool(ctx, ownerURL, 4)
	if err != nil {
		return nil, fmt.Errorf("pool di piattaforma: %w", err)
	}
	app, err := newPool(ctx, appURL, 0)
	if err != nil {
		owner.Close()
		return nil, fmt.Errorf("pool applicativo: %w", err)
	}
	return &DB{App: app, Owner: owner}, nil
}

func newPool(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	// Descrizioni delle istruzioni preparate per connessione: 512 di default, con le query
	// dinamiche del linguaggio di ricerca e 8 connessioni arrivano a molti MB di heap.
	cfg.ConnConfig.StatementCacheCapacity = 128
	cfg.AfterConnect = registerUUIDStrings
	cfg.ConnConfig.RuntimeParams["application_name"] = "regia"
	// Nessuna query applicativa deve durare più di così (vicini rumorosi).
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "30000"
	return pgxpool.NewWithConfig(ctx, cfg)
}

func (d *DB) Close() {
	d.App.Close()
	d.Owner.Close()
}

// Ping verifica entrambi i pool (readiness).
func (d *DB) Ping(ctx context.Context) error {
	return errors.Join(d.App.Ping(ctx), d.Owner.Ping(ctx))
}

// TenantOpts sono le impostazioni di sicurezza della transazione.
type TenantOpts struct {
	// Finance apre le tabelle riservate a finanza e PMO (tariffe, voci di costo).
	// Va impostato solo dopo il controllo applicativo del permesso.
	Finance bool
	// ReadOnly apre una transazione in sola lettura.
	ReadOnly bool
	// Member è il membro che agisce (app.member_id): la RLS lo usa per la visibilità degli
	// stanziamenti per area. Senza, chi non è finanza non vede nessun importo di stanziamento.
	Member string
}

// ErrNoTenant è restituito quando si prova ad aprire una transazione senza tenant.
var ErrNoTenant = errors.New("nessun tenant nella transazione")

// InTenant esegue fn in una transazione legata al tenant: set_config(..., true)
// vale solo per la transazione, quindi è sicuro con il pool.
func (d *DB) InTenant(ctx context.Context, tenantID string, o TenantOpts, fn func(pgx.Tx) error) error {
	if tenantID == "" {
		return ErrNoTenant
	}
	access := pgx.ReadWrite
	if o.ReadOnly {
		access = pgx.ReadOnly
	}
	return pgx.BeginTxFunc(ctx, d.App, pgx.TxOptions{AccessMode: access}, func(tx pgx.Tx) error {
		finance := "off"
		if o.Finance {
			finance = "on"
		}
		if _, err := tx.Exec(ctx,
			"select set_config('app.tenant_id', $1, true), set_config('app.finance', $2, true), set_config('app.member_id', $3, true)",
			tenantID, finance, o.Member); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Global esegue fn in una transazione del ruolo applicativo senza tenant:
// solo le tabelle globali (account, sessioni) e le funzioni dedicate sono raggiungibili.
func (d *DB) Global(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, d.App, pgx.TxOptions{}, fn)
}
