package grc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

var types = []struct{ key, name string }{
	{ControlType, "Controllo"},
	{EvidenceType, "Evidenza"},
}

var roles = []struct{ key, name string }{
	{RoleOwner, "Responsabile del controllo"},
	{RoleReviewer, "Compliance"},
}

// Provision configura il modulo in un tenant: spazio GRC, tipi "control" ed "evidence",
// ruoli sul controllo, gruppo Compliance, processo del controllo e impostazioni. Estende
// il ruolo Auditor con grc.read se è ancora quello di sistema. I cataloghi non si caricano
// qui: il tenant sceglie quali pacchetti di partenza importare. È idempotente.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	if err := exec(`insert into spaces (tenant_id, id, key, name, kind, lead_id) values (app_tenant(), $1, $2, 'Conformità', 'compliance', $3)
		on conflict do nothing`, db.NewID(), SpaceKey, adminMemberID); err != nil {
		return err
	}
	for _, t := range types {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, $3, 'grc', $2, true)`,
				typeID, t.key, t.name)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, SpaceKey, typeID); err != nil {
			return err
		}
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	if err := exec(`insert into groups (tenant_id, id, name) values (app_tenant(), $1, $2) on conflict do nothing`, db.NewID(), GroupCompliance); err != nil {
		return err
	}
	if err := exec(`insert into grc_settings (tenant_id) values (app_tenant()) on conflict do nothing`); err != nil {
		return err
	}
	// Auditor: grc.read in più, solo se il ruolo ha ancora i permessi di sistema.
	if err := exec(`update roles set permissions = permissions || array['grc.read'] where key = 'auditor'
		and permissions @> array['element.read','audit.read'] and cardinality(permissions) = 2`); err != nil {
		return err
	}
	raw, err := starters.ReadFile("starter/control_standard.json")
	if err != nil {
		return err
	}
	b, err := engine.ParseBundle(raw)
	if err != nil {
		return fmt.Errorf("control_standard.json: %w", err)
	}
	var has bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		where t.key = $1)`, b.Type).Scan(&has); err != nil {
		return err
	}
	if !has {
		actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
		if _, problems, err := engine.New().Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("processo del controllo: %w %v", err, problems)
		}
	}
	return nil
}

// RunRecompute ricalcola ogni intervallo l'applicabilità delle regole nei tenant che ne
// hanno: i fatti delle applicazioni (criticità, funzione critica, dati) cambiano nel tempo.
func (s *Service) RunRecompute(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func() {
		rows, err := d.Owner.Query(ctx, `select distinct tenant_id::text from grc_applicability_rules where active`)
		if err != nil {
			log.Warn("applicabilità della conformità", "errore", err)
			return
		}
		tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			log.Warn("applicabilità della conformità", "errore", err)
			return
		}
		for _, t := range tenants {
			if err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
				_, err := s.Recompute(ctx, tx, engine.Actor{})
				return err
			}); err != nil {
				log.Warn("applicabilità della conformità", "tenant", t, "errore", err)
			}
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
