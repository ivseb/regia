package architecture

import "regia/internal/engine"

// Campi del linguaggio di query offerti dall'architettura (piano, passo 16).
// Ogni sorgente usa colonne indicizzate su element_id e sul valore.
func registerQueryFields(eng *engine.Service) {
	eng.QueryField(engine.QueryField{
		Key: "team", Names: []string{"team", "squadra"},
		Label: "Team: impatti del team, applicazioni di cui è owner, richieste che lo toccano",
		Kind:  "ref", Types: []string{RequestType, ImpactType, AppType},
		Source: `select element_id, team_id as v from ext_impact
			union all select element_id, owner_team_id from ext_application
			union all select request_id, team_id from ext_impact`,
		Lookup:  `select id::text, lower(name) from teams where lower(name) = any($1)`,
		Suggest: `select name, case when archived then 'archiviato' else 'team' end from teams where name ilike $1 order by archived, name limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "application", Names: []string{"applicazione", "applicazioni", "application", "app"},
		Label: "Applicazione (chiave, codice o nome): impatti e richieste che la toccano",
		Kind:  "ref", RefType: AppType, RefTitles: true, Types: []string{RequestType, ImpactType},
		Source: `select element_id, application_id as v from ext_impact
			union all select request_id, application_id from ext_impact`,
		Lookup: `select element_id::text, lower(code) from ext_application where lower(code) = any($1)`,
	})
	eng.QueryField(engine.QueryField{
		Key: "asset", Names: []string{"asset", "asset_logico", "logical_asset"},
		Label: "Asset logico (chiave o nome): richieste che lo citano e applicazioni che lo compongono",
		Kind:  "ref", RefType: AssetType, RefTitles: true, Types: []string{RequestType, AppType},
		Source: `select r.source_id as element_id, r.target_id as v from relations r
			join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = '` + RelConcerns + `'
			union all select r.target_id, r.source_id from relations r
			join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key in ('` + RelComposedOf + `', '` + RelUses + `')`,
	})
	eng.QueryField(engine.QueryField{
		Key: "request", Names: []string{"richiesta", "request"},
		Label: "Impatti di una business request (chiave, es. DEM-12)",
		Kind:  "ref", RefType: RequestType, Types: []string{ImpactType},
		Source: `select element_id, request_id as v from ext_impact`,
	})
}
