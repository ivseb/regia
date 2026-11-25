package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Provision configura il modulo in un tenant. È idempotente:
//   - tipo "page" (modulo knowledge), ammesso negli spazi di documentazione esistenti;
//   - ruoli "Revisore del documento" e "Approvatore del documento";
//   - relazione "documenta" (pagina → elemento);
//   - politica di approvazione della fase "In revisione": prima i revisori, poi gli
//     approvatori; esiti Approvata, Bozza (respinta o rinviata);
//   - processo "Documento controllato" dal pacchetto di partenza;
//   - template di partenza per tutti gli spazi: runbook, ADR, policy, verbale, guida.
//
// Gli spazi si creano dall'interfaccia (Documenti → Nuovo spazio) o con l'API.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var typeID string
	err := tx.QueryRow(ctx, "select id from element_types where key = $1", TypePage).Scan(&typeID)
	if errors.Is(err, pgx.ErrNoRows) {
		typeID = db.NewID()
		err = exec(`insert into element_types (tenant_id, id, key, name, module, is_system)
			values (app_tenant(), $1, $2, 'Documento', 'knowledge', true)`, typeID, TypePage)
	}
	if err != nil {
		return err
	}
	for _, r := range []struct{ key, name string }{{RoleReviewer, "Revisore del documento"}, {RoleApprover, "Approvatore del documento"}} {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3)
			on conflict do nothing`, db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
		values (app_tenant(), $1, $2, 'documenta', 'documentato da', true) on conflict do nothing`, db.NewID(), RelDocuments); err != nil {
		return err
	}
	if err := exec(`insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), s.id, $1 from spaces s where s.kind = $2 on conflict do nothing`, typeID, SpaceKind); err != nil {
		return err
	}
	var hasPolicy bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from approval_policies where type_id = $1 and lower(stage) = lower($2))`,
		typeID, ReviewPhase).Scan(&hasPolicy); err != nil {
		return err
	}
	if !hasPolicy {
		policy := map[string]any{
			"outcomes": map[string]string{"approve": "Approvata", "reject": "Bozza", "defer": "Bozza"},
			"rules": []map[string]any{{"when": map[string]any{}, "steps": []map[string]any{
				{"approvers": []map[string]string{{"role": RoleReviewer}}},
				{"approvers": []map[string]string{{"role": RoleApprover}}},
			}}},
		}
		raw, _ := json.Marshal(policy)
		if err := exec(`insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
			values (app_tenant(), $1, 'Approvazione dei documenti controllati', $2, $3, $4)`, db.NewID(), typeID, ReviewPhase, raw); err != nil {
			return err
		}
	}
	var hasProcess bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions where type_id = $1)`, typeID).Scan(&hasProcess); err != nil {
		return err
	}
	if !hasProcess {
		raw, err := starters.ReadFile("starter/page_controlled.json")
		if err != nil {
			return err
		}
		b, err := engine.ParseBundle(raw)
		if err != nil {
			return err
		}
		eng := engine.New()
		actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("processo dei documenti controllati: %w %v", err, problems)
		}
	}
	for i, t := range starterTemplates {
		if err := exec(`insert into kb_templates (tenant_id, id, space_id, key, name, description, body, controlled, review_months, ordinal)
			select app_tenant(), $1, null, $2, $3, $4, $5, $6, $7, $8
			where not exists (select 1 from kb_templates where space_id is null and key = $2)`,
			db.NewID(), t.Key, t.Name, t.Description, t.Body, t.Controlled, t.ReviewMonths, (i+1)*10); err != nil {
			return err
		}
	}
	return nil
}
