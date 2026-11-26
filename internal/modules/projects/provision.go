package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
	"regia/internal/platform/db"
)

// types sono i tipi del modulo, tutti nello spazio PRJ. deliverable e change_request sono
// solo tipi per ora (passi 3 e 4): niente processo, niente logica.
var types = []struct{ key, name, plural string }{
	{TypeProgram, "Programma", "Programmi"},
	{TypeProject, "Progetto", "Progetti"},
	{TypeWorkPackage, "Work package", "Work package"},
	{TypeActivity, "Attività di piano", "Attività di piano"},
	{TypeMilestone, "Milestone", "Milestone"},
	{TypeDeliverable, "Deliverable", "Deliverable"},
	{TypeChangeRequest, "Change request", "Change request"},
}

// roles sono i ruoli per oggetto che il progetto usa; quelli che esistono già (demand,
// architettura) non si toccano.
var roles = []struct{ key, name string }{
	{"business_owner", "Referente di business"},
	{"project_manager", "Project manager"},
	{"sponsor", "Sponsor"},
	{"pmo", "PMO"},
	{"operations_manager", "Responsabile operations"},
	{"team_lead", "Referente del team"},
}

// defaultProjectRoles: business owner, PM e sponsor obbligatori ed ereditati dalla richiesta;
// PMO facoltativo ed ereditato; responsabile operations facoltativo (progetti.md §8).
var defaultProjectRoles = []struct {
	key               string
	required, inherit bool
}{
	{"business_owner", true, true},
	{"project_manager", true, true},
	{"sponsor", true, true},
	{"pmo", false, true},
	{"operations_manager", false, false},
}

// Provision configura il modulo in un tenant. È idempotente e va eseguito dopo il lavoro
// (la regola dei work package agili usa create_epic). Nei tenant esistenti porta anche le
// richieste approvate nei loro progetti, con gli impatti come work package agili: è la
// migrazione dei dati del passo 1, che si esegue con «regia tenant provision».
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var spaceID string
	err := tx.QueryRow(ctx, "select id from spaces where key = $1", SpaceKey).Scan(&spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		spaceID = db.NewID()
		err = exec(`insert into spaces (tenant_id, id, key, name, kind, lead_id) values (app_tenant(), $1, $2, 'Progetti', 'portfolio', $3)`,
			spaceID, SpaceKey, nullable(adminMemberID))
	}
	if err != nil {
		return err
	}
	for _, t := range types {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, plural_name, module, is_system) values (app_tenant(), $1, $2, $3, $4, 'strategy', true)`,
				typeID, t.key, t.name, t.plural)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id) values (app_tenant(), $1, $2) on conflict do nothing`, spaceID, typeID); err != nil {
			return err
		}
	}
	if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
		values (app_tenant(), $1, 'depends_on', 'dipende da', 'è prerequisito di', true) on conflict do nothing`, db.NewID()); err != nil {
		return err
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	var hasRoles bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from project_roles)").Scan(&hasRoles); err != nil {
		return err
	}
	if !hasRoles {
		for i, r := range defaultProjectRoles {
			if err := exec(`insert into project_roles (tenant_id, role_id, required, inherit_from_request, ordinal)
				select app_tenant(), id, $2, $3, $4 from stakeholder_roles where key = $1 on conflict do nothing`, r.key, r.required, r.inherit, i); err != nil {
				return err
			}
		}
	}
	if err := exec(`insert into project_settings (tenant_id) values (app_tenant()) on conflict do nothing`); err != nil {
		return err
	}
	// Cancelli di fase: decide lo sponsor del progetto (modificabile nelle politiche di approvazione).
	var hasPolicy bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from approval_policies p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		where t.key = $1 and p.stage = $2)`, TypeProject, GatePolicyStage).Scan(&hasPolicy); err != nil {
		return err
	}
	if !hasPolicy {
		rules, _ := json.Marshal(map[string]any{"outcomes": map[string]string{}, "rules": []map[string]any{
			{"when": map[string]any{}, "steps": []any{map[string]any{"approvers": []map[string]string{{"role": "sponsor"}}}}},
		}})
		if err := exec(`insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
			select app_tenant(), $1, 'Cancello di fase del progetto', id, $2, $3 from element_types where key = $4`,
			db.NewID(), GatePolicyStage, rules, TypeProject); err != nil {
			return err
		}
	}
	eng := engine.New()
	work.Register(eng)
	s := Register(eng, nil)
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	for _, f := range processFiles {
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
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("pacchetto %s: %w %v", f, err, problems)
		}
	}
	return s.migrateRequests(ctx, tx, actor)
}

// migrateRequests porta nei progetti le richieste approvate che non ne hanno ancora uno.
func (s *Service) migrateRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor) error {
	rows, err := tx.Query(ctx, `select r.id::text from elements r
		join element_types t on t.tenant_id = r.tenant_id and t.id = r.type_id and t.key = $1
		where r.deleted_at is null and not r.is_draft and not exists (select 1 from project_requests pr where pr.request_id = r.id)
		order by r.number`, RequestType)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		req, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		ok, err := requestApproved(ctx, tx, req)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, _, err := s.fromRequest(ctx, tx, actor, req, ""); err != nil {
			return fmt.Errorf("progetto dalla richiesta %s: %w", req.Key, err)
		}
	}
	return nil
}
