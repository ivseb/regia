// Package demand è il modulo Demand & Portfolio (replica/demand.md): business request,
// stage-gate, ruoli, approvazioni e dati della domanda. Usa il motore degli elementi.
package demand

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

const (
	SpaceKey = "DEM"
	TypeKey  = "business_request"
	// ApprovalStage è la fase in cui si apre l'approvazione dello stage-gate.
	ApprovalStage = "Approvazione"
)

// Il processo (fasi, passaggi, gate, regole) non è nel codice: è il pacchetto di
// partenza starter/demand_standard.json, importato e pubblicato alla creazione del
// tenant e poi modificabile dalla configurazione dei processi (sdlc.md §1).
//
//go:embed starter/demand_standard.json
var starterProcess []byte

// Ruoli sugli oggetti (demand.md §2).
var roles = []struct{ key, name string }{
	{"requester", "Richiedente"},
	{"business_owner", "Referente di business"},
	{"sponsor", "Sponsor"},
	{"demand_manager", "Demand manager"},
	{"pmo", "PMO"},
	{"project_manager", "Project manager"},
	{"architect", "Architetto"},
	{"finance", "Finanza"},
	{"compliance", "Compliance"},
}

func i64(v int64) *int64 { return &v }

// Soglie di default decise il 2026-11-24 (demand.md §16): fino a 50k lo sponsor,
// fino a 250k anche il PMO, oltre anche il comitato investimenti. Importi in centesimi.
func defaultPolicy() map[string]any {
	sponsor := map[string]any{"approvers": []map[string]string{{"role": "sponsor"}}}
	pmo := map[string]any{"approvers": []map[string]string{{"role": "pmo"}}}
	committee := map[string]any{"approvers": []map[string]string{{"body": "investment_committee"}}}
	return map[string]any{
		"outcomes": map[string]string{"approve": "Pianificazione", "reject": "Respinta", "defer": "Prioritizzazione"},
		"rules": []map[string]any{
			{"when": map[string]any{"amount_lte": i64(5_000_000)}, "steps": []any{sponsor}},
			{"when": map[string]any{"amount_lte": i64(25_000_000)}, "steps": []any{sponsor, pmo}},
			{"when": map[string]any{}, "steps": []any{sponsor, pmo, committee}},
		},
	}
}

// benefitsHelp è l'aiuto del campo "Benefici attesi" (si vede nel form e nel dettaglio).
const benefitsHelp = "Risparmi, ricavi, riduzione del rischio: meglio se misurabili."

// Provision configura il modulo in un tenant. È idempotente: se il tipo esiste già non fa nulla.
// adminMemberID entra nei gruppi Demand management e PMO e nel comitato, così il tenant
// è utilizzabile subito; l'amministratore li ridistribuisce poi alle persone giuste.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1)", TypeKey).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ensureProcess(ctx, tx, adminMemberID)
	}
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }

	stateSet := db.NewID()
	if err := exec(`insert into option_sets (tenant_id, id, name, kind) values (app_tenant(), $1, 'Ciclo di vita della domanda', 'state')`, stateSet); err != nil {
		return err
	}
	// Le fasi diventano opzioni di questo insieme quando il processo si pubblica.
	typeID := db.NewID()
	if err := exec(`insert into element_types (tenant_id, id, key, name, module, extension, state_set_id, is_system, portal_enabled)
		values (app_tenant(), $1, $2, 'Richiesta', 'strategy', 'demand', $3, true, true)`, typeID, TypeKey, stateSet); err != nil {
		return err
	}

	// Campi personalizzati di esempio: il tenant ne aggiunge altri dalla configurazione.
	urgency := db.NewID()
	if err := exec(`insert into option_sets (tenant_id, id, name) values (app_tenant(), $1, 'Urgenza')`, urgency); err != nil {
		return err
	}
	for i, n := range []string{"Alta", "Media", "Bassa"} {
		if err := exec(`insert into options (tenant_id, id, option_set_id, name, ordinal) values (app_tenant(), $1, $2, $3, $4)`, db.NewID(), urgency, n, i); err != nil {
			return err
		}
	}
	fields := []struct {
		key, name, dataType string
		optionSet           *string
		help                string
	}{
		{"urgenza", "Urgenza", "enum", &urgency, ""},
		{"benefici_attesi", "Benefici attesi", "text", nil, benefitsHelp},
	}
	for i, f := range fields {
		fid := db.NewID()
		if err := exec(`insert into field_defs (tenant_id, id, key, name, data_type, option_set_id, help) values (app_tenant(), $1, $2, $3, $4, $5, $6)`,
			fid, f.key, f.name, f.dataType, f.optionSet, f.help); err != nil {
			return err
		}
		if err := exec(`insert into type_fields (tenant_id, id, type_id, field_id, ordinal) values (app_tenant(), $1, $2, $3, $4)`, db.NewID(), typeID, fid, i); err != nil {
			return err
		}
	}

	spaceID := db.NewID()
	if err := exec(`insert into spaces (tenant_id, id, key, name, kind, lead_id) values (app_tenant(), $1, $2, 'Demand', 'portfolio', $3)`, spaceID, SpaceKey, adminMemberID); err != nil {
		return err
	}
	if err := exec(`insert into space_types (tenant_id, space_id, type_id) values (app_tenant(), $1, $2)`, spaceID, typeID); err != nil {
		return err
	}

	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3)`, db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	for _, g := range []string{"Demand management", "PMO", "Compliance"} {
		gid := db.NewID()
		if err := exec(`insert into groups (tenant_id, id, name) values (app_tenant(), $1, $2)`, gid, g); err != nil {
			return err
		}
		if g == "Compliance" {
			continue // il gruppo nasce vuoto: lo popola l'amministratore
		}
		if err := exec(`insert into group_members (tenant_id, group_id, member_id) values (app_tenant(), $1, $2)`, gid, adminMemberID); err != nil {
			return err
		}
	}
	bodyID := db.NewID()
	if err := exec(`insert into governance_bodies (tenant_id, id, name, kind, chair_id) values (app_tenant(), $1, 'Comitato investimenti', 'investment_committee', $2)`, bodyID, adminMemberID); err != nil {
		return err
	}
	if err := exec(`insert into governance_body_members (tenant_id, body_id, member_id) values (app_tenant(), $1, $2)`, bodyID, adminMemberID); err != nil {
		return err
	}
	pol, _ := json.Marshal(defaultPolicy())
	if err := exec(`insert into approval_policies (tenant_id, id, name, type_id, stage, rules) values (app_tenant(), $1, 'Approvazione dello stage-gate', $2, $3, $4)`,
		db.NewID(), typeID, ApprovalStage, pol); err != nil {
		return err
	}
	return ensureProcess(ctx, tx, adminMemberID)
}

// ensureProcess importa e pubblica il pacchetto di partenza se il tipo non ha ancora
// processi. Per i tenant creati prima del motore di processo (workflow nel codice)
// porta anche le richieste esistenti sulla versione 1 e stacca il vecchio workflow.
func ensureProcess(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	var typeID string
	var has bool
	if err := tx.QueryRow(ctx, `select t.id, exists (select 1 from process_definitions p where p.type_id = t.id)
		from element_types t where t.key = $1`, TypeKey).Scan(&typeID, &has); err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := tx.Exec(ctx, `insert into groups (tenant_id, id, name) values (app_tenant(), $1, 'Compliance') on conflict do nothing`, db.NewID()); err != nil {
		return err
	}
	b, err := engine.ParseBundle(starterProcess)
	if err != nil {
		return err
	}
	// Il provisioning avviene come amministratore del tenant. Il pacchetto usa solo
	// condizioni (nessun controllo dei moduli), quindi basta un motore senza registrazioni.
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	p, problems, err := engine.New().Import(ctx, tx, actor, b, true)
	if err != nil {
		return fmt.Errorf("pacchetto di partenza del demand: %w %v", err, problems)
	}
	var vid string
	if err := tx.QueryRow(ctx, "select id from process_versions where process_id = $1 and status = 'published'", p.ID).Scan(&vid); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update elements set process_version_id = $2 where type_id = $1 and process_version_id is null`, typeID, vid); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update element_types set workflow_id = null where id = $1`, typeID)
	return err
}
