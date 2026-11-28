package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
)

var starterFiles = []string{"bia_standard.json", "resilience_test_standard.json"}

var elementTypes = []struct{ key, name, ext string }{
	{BIAType, "Analisi d'impatto (BIA)", "bia"},
	{ResTestType, "Test di resilienza", "resilience_test"},
}

// Provision configura il modulo in un tenant (idempotente). Va dopo l'architettura (spazio ARC
// e capability) e la Knowledge (tipo pagina): i tipi sorgente della relazione dei piani si
// leggono dal tenant.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var spaceOK bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from spaces where key = $1)`, SpaceKey).Scan(&spaceOK); err != nil {
		return err
	}
	if !spaceOK {
		return nil // senza architettura la continuità non ha su cosa lavorare
	}
	lang, err := i18n.TenantLang(ctx, tx)
	if err != nil {
		return err
	}
	for _, t := range elementTypes {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, $3, 'architecture', $4, true)`,
				typeID, t.key, i18n.Name(lang, t.name), t.ext)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, SpaceKey, typeID); err != nil {
			return err
		}
	}
	// Piani di continuità: pagina → capability o applicazione; non propaga impatti.
	if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system, impact_direction,
			source_type_ids, target_type_ids)
		values (app_tenant(), $1, $2, $3, $4, true, 'none',
			(select array_agg(id) from element_types where key = $5),
			(select array_agg(id) from element_types where key in ($6, $7)))
		on conflict do nothing`, db.NewID(), RelPlanFor, i18n.Name(lang, "è il piano di continuità di"), i18n.Name(lang, "ha come piano di continuità"),
		PageType, CapabilityType, AppType); err != nil {
		return err
	}
	for _, r := range rbac.SystemRoles {
		if r.Key != "continuity_manager" {
			continue
		}
		if err := exec(`insert into roles (tenant_id, id, key, name, permissions) values (app_tenant(), $1, $2, $3, $4)
			on conflict (tenant_id, key) where key is not null do nothing`, db.NewID(), r.Key, i18n.Name(lang, r.Name), r.Permissions); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(DefaultConfig(lang))
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `insert into continuity_settings (tenant_id, version, config, updated_by) values (app_tenant(), 1, $1, $2)
		on conflict do nothing`, raw, nullable(adminMemberID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err := exec(`insert into continuity_settings_versions (tenant_id, version, config, created_by) values (app_tenant(), 1, $1, $2)
			on conflict do nothing`, raw, nullable(adminMemberID)); err != nil {
			return err
		}
	}
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	// Il motore che importa i pacchetti deve conoscere i controlli dei gate (la validazione li
	// cerca); quelli veri li registra Register sul motore del server.
	imp := engine.New()
	for _, g := range []string{"bia_completa", "esito_test"} {
		imp.Guard(g, func(context.Context, pgx.Tx, *engine.Element) error { return nil })
	}
	for _, f := range starterFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			return err
		}
		b, err := engine.ParseBundle(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var has bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
			where t.key = $1)`, b.Type).Scan(&has); err != nil {
			return err
		}
		if has {
			continue
		}
		if _, problems, err := imp.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("pacchetto %s: %w %v", f, err, problems)
		}
	}
	return nil
}

// Setup prepara il modulo in un tenant esistente (continuity.admin o tenant.admin).
func (s *Service) Setup(ctx context.Context, tx pgx.Tx, actor engine.Actor) (map[string]bool, error) {
	if !can(actor).Admin {
		return nil, engine.ErrForbidden
	}
	var archOK bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from element_types where key = $1)`, CapabilityType).Scan(&archOK); err != nil {
		return nil, err
	}
	if !archOK {
		return nil, engine.InvalidCode("validation.continuity.not_ready", "continuity", "serve prima l'architettura con le capability")
	}
	if err := Provision(ctx, tx, actor.MemberID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.setup", EntityType: "continuity_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return map[string]bool{"ready": true}, nil
}
