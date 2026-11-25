// Package architecture è la parte minima dell'architettura enterprise che serve al
// ciclo di vita della richiesta (sdlc.md §3-4): asset logici, applicazioni con il team
// owner, composizione, e impatti di una business request sulle applicazioni.
package architecture

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

const (
	SpaceKey     = "ARC"
	AssetType    = "logical_asset"
	AppType      = "application"
	ImpactType   = "impact"
	RequestType  = "business_request" // tipo del modulo demand
	RequestSpace = "DEM"

	RelComposedOf = "composed_of"
	RelUses       = "uses"
	RelConcerns   = "concerns"
	RelDependsOn  = "depends_on"
)

//go:embed starter/*.json
var starters embed.FS

var types = []struct{ key, name, module, ext, space string }{
	{AssetType, "Asset logico", "architecture", "logical_asset", SpaceKey},
	{AppType, "Applicazione", "architecture", "application", SpaceKey},
	{ImpactType, "Impatto", "strategy", "impact", RequestSpace},
}

var relationTypes = []struct{ key, out, in string }{
	{RelComposedOf, "è composto da", "fa parte di"},
	{RelUses, "usa", "è usata da"},
	{RelConcerns, "riguarda", "è citato da"},
	{RelDependsOn, "dipende da", "è prerequisito di"},
}

var roles = []struct{ key, name string }{
	{"technical_analyst", "Analista tecnico"},
	{"team_lead", "Referente del team"},
	{"architect", "Architetto"},
}

// Provision configura il modulo in un tenant. È idempotente e va eseguito dopo quello
// del demand (gli impatti stanno nello spazio delle richieste).
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var spaceID string
	err := tx.QueryRow(ctx, "select id from spaces where key = $1", SpaceKey).Scan(&spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		spaceID = db.NewID()
		err = exec(`insert into spaces (tenant_id, id, key, name, kind, lead_id) values (app_tenant(), $1, $2, 'Architettura', 'architecture', $3)`,
			spaceID, SpaceKey, adminMemberID)
	}
	if err != nil {
		return err
	}
	for _, t := range types {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, $3, $4, $5, true)`,
				typeID, t.key, t.name, t.module, t.ext)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, t.space, typeID); err != nil {
			return err
		}
	}
	for _, r := range relationTypes {
		if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
			values (app_tenant(), $1, $2, $3, $4, true) on conflict do nothing`, db.NewID(), r.key, r.out, r.in); err != nil {
			return err
		}
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	// Il gruppo Demand management serve alla regola del pacchetto degli impatti.
	if err := exec(`insert into groups (tenant_id, id, name) values (app_tenant(), $1, 'Demand management') on conflict do nothing`, db.NewID()); err != nil {
		return err
	}
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	for _, f := range []string{"logical_asset_standard.json", "application_standard.json", "impact_standard.json"} {
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
		if _, problems, err := engine.New().Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("pacchetto %s: %w %v", f, err, problems)
		}
	}
	// Architettura enterprise: capability, tipi di dipendenza, impostazioni (ea.go).
	return provisionEA(ctx, tx, adminMemberID)
}
