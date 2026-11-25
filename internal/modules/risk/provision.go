package risk

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/db"
)

//go:embed starter/*.json
var starters embed.FS

var processFiles = []string{"risk_standard.json", "incident_standard.json", "ict_provider_standard.json"}

var spaces = []struct{ key, name, kind string }{
	{RiskSpace, "Rischi ICT", "compliance"},
	{IncidentSpace, "Incidenti", "service"},
	{ProviderSpace, "Fornitori ICT", "compliance"},
}

var types = []struct{ key, name, ext, space string }{
	{RiskType, "Rischio ICT", "risk", RiskSpace},
	{IncidentType, "Incidente", "incident", IncidentSpace},
	{ProviderType, "Fornitore ICT", "ict_provider", ProviderSpace},
}

var roles = []struct{ key, name string }{
	{RoleRiskOwner, "Titolare del rischio"},
	{RoleRiskCommittee, "Comitato rischi"},
	{RoleIncidentLead, "Responsabile dell'incidente"},
	{RoleIncidentTeam, "Gestione incidenti"},
	{RoleProviderOwner, "Referente del fornitore"},
}

var relationTypes = []struct{ key, out, in string }{
	{RelRiskOf, "riguarda", "è esposto al rischio"},
	{RelMitigates, "mitiga", "è mitigato da"},
	{RelProvidedBy, "è fornita da", "fornisce"},
	{RelImpacts, "ha impattato", "è stata impattata da"},
	{RelMaterializes, "concretizza il rischio", "si è concretizzato in"},
	{RelCorrective, "azione correttiva di", "ha l'azione correttiva"},
}

// Provision configura il modulo in un tenant. È idempotente e va eseguito dopo
// architecture (applicazioni e asset) e, per le azioni correttive, dopo work:
//   - spazi RIS, INC e FOR con i tipi risk, incident e ict_provider;
//   - ruoli sugli elementi, gruppi "Risk management" e "Incident management", relazioni;
//   - il task ammesso nello spazio degli incidenti (azioni correttive senza team);
//   - processi dei pacchetti di partenza;
//   - la prima volta, il pacchetto di configurazione (matrice 5×5, impostazioni, politica di
//     accettazione, schemi DORA e NIS2).
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	for _, sp := range spaces {
		if err := exec(`insert into spaces (tenant_id, id, key, name, kind, lead_id) values (app_tenant(), $1, $2, $3, $4, $5)
			on conflict (tenant_id, key) do nothing`, db.NewID(), sp.key, sp.name, sp.kind, adminMemberID); err != nil {
			return err
		}
	}
	for _, t := range types {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, $3, 'grc', $4, true)`,
				typeID, t.key, t.name, t.ext)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, t.space, typeID); err != nil {
			return err
		}
	}
	// Le azioni correttive senza team stanno nello spazio degli incidenti (se il work
	// management c'è: altrimenti il tipo task arriva con il suo provisioning successivo).
	if err := exec(`insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), s.id, t.id from spaces s, element_types t where s.key = $1 and t.key = $2 on conflict do nothing`,
		IncidentSpace, TaskType); err != nil {
		return err
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	for _, g := range []string{GroupRisk, GroupIncident} {
		if err := exec(`insert into groups (tenant_id, id, name) values (app_tenant(), $1, $2) on conflict do nothing`, db.NewID(), g); err != nil {
			return err
		}
	}
	for _, r := range relationTypes {
		if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
			values (app_tenant(), $1, $2, $3, $4, true) on conflict do nothing`, db.NewID(), r.key, r.out, r.in); err != nil {
			return err
		}
	}
	// Un motore con le azioni di questo modulo (e quelle già pubblicate da altri, come
	// segnaposto) per validare i processi di partenza.
	eng := engine.New()
	ap := approval.Register(eng)
	s := Register(eng, ap)
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
		known := eng.ActionKinds()
		for _, r := range b.Definition.Rules {
			for _, a := range r.Actions {
				if !slices.Contains(known, a.Kind) {
					eng.Action(a.Kind, func(context.Context, pgx.Tx, engine.Actor, *engine.Element, engine.Action) (string, error) {
						return "", engine.ErrNoEffect
					})
					known = append(known, a.Kind)
				}
			}
		}
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("processo %s: %w %v", f, err, problems)
		}
	}
	var configured bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from risk_matrices) or exists (select 1 from incident_schemes)").Scan(&configured); err != nil {
		return err
	}
	if configured {
		return nil
	}
	raw, err := starters.ReadFile("starter/rischi_standard.json")
	if err != nil {
		return err
	}
	p, err := ParsePackage(raw)
	if err != nil {
		return err
	}
	if _, err := s.Import(ctx, tx, actor, p); err != nil {
		return fmt.Errorf("pacchetto dei rischi: %w", err)
	}
	return nil
}
