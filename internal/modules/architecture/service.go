package architecture

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// PermWrite è il permesso di gestire il catalogo (asset, applicazioni, team, composizione).
const PermWrite = "architecture.write"

// Ruoli sulla richiesta che possono fare l'analisi tecnica (impatti e asset citati).
var analystRoles = []string{"demand_manager", "pmo", "project_manager", "architect", "technical_analyst"}

type Service struct {
	eng *engine.Service
}

// Register collega il modulo al motore: fatti per le condizioni di processo.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng}
	eng.Facts(RequestType, s.requestFacts)
	eng.Facts(ImpactType, s.impactFacts)
	eng.Facts(AppType, s.appFacts)
	eng.Action(ActionCreateImpact, s.actionCreateImpact, s.checkCreateImpact)
	registerImpactAction(eng)
	registerQueryFields(eng) // team:, applicazione:, asset:, richiesta: (query.go)
	describeFacts(eng)
	for _, f := range []string{"logical_asset_standard.json", "application_standard.json", "impact_standard.json"} {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			panic(err)
		}
		eng.Starter(raw)
	}
	// Architettura enterprise: fatti, descrizioni e processo delle capability (ea_facts.go).
	registerEA(s, eng)
	return s
}

var critNames = map[string]string{"low": "bassa", "medium": "media", "high": "alta", "critical": "critica"}
var critRank = map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}

// normCriticality accetta i valori in italiano o in inglese.
func normCriticality(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for en, it := range critNames {
		if s == en || s == it {
			return en, true
		}
	}
	return "", s == ""
}

var changeKinds = map[string]string{"new": "nuova", "change": "modifica", "config": "configurazione", "decommission": "dismissione", "none": "nessuna"}

// ---------------------------------------------------------------------------
// Team

type Team struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	LeadID *string `json:"lead_id"`
	Lead   *string `json:"lead"`
	Apps   int     `json:"applications"`
	// Capacity: giorni/persona a settimana (null = 5, una persona a tempo pieno).
	Capacity *float64 `json:"capacity_days_week"`
}

func (s *Service) Teams(ctx context.Context, tx pgx.Tx) ([]Team, error) {
	return s.TeamsSearch(ctx, tx, "", 0, []string{})
}

// TeamsSearch: come Teams, con ricerca nel nome (q già resa letterale per ilike), massimo di righe
// (0 = tutte) e restrizione a certi identificativi.
func (s *Service) TeamsSearch(ctx context.Context, tx pgx.Tx, q string, limit int, ids []string) ([]Team, error) {
	if ids == nil {
		ids = []string{} // NULL escluderebbe ogni riga
	}
	rows, err := tx.Query(ctx, `select t.id, t.name, t.lead_id, m.display_name,
		(select count(*) from ext_application a where a.owner_team_id = t.id),
		t.weekly_capacity_hours::float8 / 8
		from teams t left join members m on m.tenant_id = t.tenant_id and m.id = t.lead_id
		where not t.archived and ($1 = '' or t.name ilike '%' || $1 || '%') and (cardinality($2::text[]) = 0 or t.id::text = any($2))
		order by t.name, t.id limit nullif($3, 0)`, q, ids, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Team])
}

type TeamInput struct {
	Name     string   `json:"name"`
	LeadID   string   `json:"lead_id"`
	Capacity *float64 `json:"capacity_days_week"`
}

func (s *Service) CreateTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TeamInput) (string, error) {
	if !actor.Has(PermWrite) && !actor.Has("tenant.admin") {
		return "", engine.ErrForbidden
	}
	return createTeam(ctx, tx, actor, in)
}

func createTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TeamInput) (string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return "", engine.TooLong("name", 120, "nome del team obbligatorio (massimo 120 caratteri)")
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from teams where lower(name) = lower($1))", name).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "", engine.Duplicate("name", "esiste già un team %q", name)
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into teams (tenant_id, id, name, lead_id) values (app_tenant(), $1, $2, $3)`, id, name, nullable(in.LeadID)); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return "", engine.InvalidCode("validation.member_not_found", "lead_id", "membro inesistente")
		}
		return "", err
	}
	return id, audit.Append(ctx, tx, audit.Event{Action: "team.create", EntityType: "team", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"name": name}})
}

func (s *Service) UpdateTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in TeamInput) error {
	if !actor.Has(PermWrite) && !actor.Has("tenant.admin") {
		return engine.ErrForbidden
	}
	if in.Capacity != nil && (*in.Capacity < 0 || *in.Capacity > 1000) {
		return engine.OutOfRange("capacity_days_week", 0, 1000, "capacità fuori scala (0-1000 giorni/persona a settimana)")
	}
	tag, err := tx.Exec(ctx, `update teams set name = coalesce(nullif(trim($2), ''), name), lead_id = $3,
		weekly_capacity_hours = case when $4::float8 is null then weekly_capacity_hours else $4::float8 * 8 end where id = $1`,
		id, in.Name, nullable(in.LeadID), in.Capacity)
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return engine.InvalidCode("validation.member_not_found", "lead_id", "membro inesistente")
		}
		if strings.Contains(err.Error(), "teams_name_uq") {
			return engine.Duplicate("name", "esiste già un team con questo nome")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audit.Append(ctx, tx, audit.Event{Action: "team.update", EntityType: "team", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

func teamByName(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id from teams where lower(name) = lower($1) and not archived", strings.TrimSpace(name)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ---------------------------------------------------------------------------
// Applicazioni

type Application struct {
	ID                 string   `json:"id"`
	Key                string   `json:"key"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	State              string   `json:"state"`
	StateCategory      string   `json:"state_category"`
	Code               *string  `json:"code"`
	Criticality        *string  `json:"criticality"`
	CriticalFunction   bool     `json:"supports_critical_function"`
	DataClassification *string  `json:"data_classification"`
	Hosting            *string  `json:"hosting"`
	RTOMinutes         *int     `json:"rto_minutes"`
	RPOMinutes         *int     `json:"rpo_minutes"`
	TeamID             *string  `json:"team_id"`
	Team               *string  `json:"team"`
	Assets             []string `json:"assets"`
}

const selectApp = `
	select e.id, s.key || '-' || e.number, e.title, e.body_text, coalesce(o.name, ''), coalesce(o.category, ''),
	       a.code, a.criticality, a.supports_critical_function, a.data_classification, a.hosting, a.rto_minutes, a.rpo_minutes,
	       a.owner_team_id, t.name,
	       coalesce((select array_agg(ae.title order by ae.title) from relations r
	                 join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key in ('composed_of', 'uses')
	                 join elements ae on ae.tenant_id = r.tenant_id and ae.id = r.source_id and ae.deleted_at is null
	                 where r.target_id = e.id), '{}')
	from elements e
	join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
	where e.deleted_at is null`

func scanApps(rows pgx.Rows) ([]Application, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Application, error) {
		var a Application
		err := r.Scan(&a.ID, &a.Key, &a.Name, &a.Description, &a.State, &a.StateCategory, &a.Code, &a.Criticality, &a.CriticalFunction,
			&a.DataClassification, &a.Hosting, &a.RTOMinutes, &a.RPOMinutes, &a.TeamID, &a.Team, &a.Assets)
		return a, err
	})
}

// Applications elenca il catalogo; q filtra per nome o codice.
func (s *Service) Applications(ctx context.Context, tx pgx.Tx, q string) ([]Application, error) {
	return s.ApplicationsSearch(ctx, tx, q, 500, []string{})
}

// ApplicationsSearch: come Applications, con massimo di righe (0 = tutte) e restrizione a certi
// identificativi (per leggere i nomi delle voci già scelte in un selettore).
func (s *Service) ApplicationsSearch(ctx context.Context, tx pgx.Tx, q string, limit int, ids []string) ([]Application, error) {
	if ids == nil {
		ids = []string{} // NULL escluderebbe ogni riga
	}
	rows, err := tx.Query(ctx, selectApp+` and ($1 = '' or e.title ilike '%' || $1 || '%' or a.code ilike $1 || '%' or (s.key || '-' || e.number) ilike $1)
		and (cardinality($2::text[]) = 0 or e.id::text = any($2))
		order by e.title, e.id limit nullif($3, 0)`, strings.TrimSpace(q), ids, limit)
	if err != nil {
		return nil, err
	}
	return scanApps(rows)
}

func (s *Service) Application(ctx context.Context, tx pgx.Tx, id string) (*Application, error) {
	rows, err := tx.Query(ctx, selectApp+` and e.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanApps(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

type ApplicationInput struct {
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	Code               *string `json:"code"`
	Criticality        *string `json:"criticality"`
	CriticalFunction   *bool   `json:"supports_critical_function"`
	DataClassification *string `json:"data_classification"`
	Hosting            *string `json:"hosting"`
	RTOMinutes         *int    `json:"rto_minutes"`
	RPOMinutes         *int    `json:"rpo_minutes"`
	TeamID             *string `json:"team_id"`
}

func (in *ApplicationInput) validate() error {
	if in.Criticality != nil {
		c, ok := normCriticality(*in.Criticality)
		if !ok {
			return engine.InvalidValue("criticality", "valore non ammesso (bassa, media, alta, critica)")
		}
		in.Criticality = &c
	}
	if in.Code != nil {
		c := strings.TrimSpace(*in.Code)
		if len(c) > 60 {
			return engine.TooLong("code", 60, "codice troppo lungo (massimo 60 caratteri)")
		}
		in.Code = &c
	}
	for name, v := range map[string]*int{"rto_minutes": in.RTOMinutes, "rpo_minutes": in.RPOMinutes} {
		if v != nil && *v < 0 {
			return engine.InvalidCode("validation.not_negative", name, "non può essere negativo")
		}
	}
	return nil
}

func (s *Service) CreateApplication(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ApplicationInput) (*Application, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if in.Name == nil {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: AppType, Title: *in.Name, BodyText: desc, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_application (tenant_id, element_id) values (app_tenant(), $1)`, el.ID); err != nil {
		return nil, err
	}
	if err := writeApp(ctx, tx, el.ID, in); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	return s.Application(ctx, tx, el.ID)
}

func writeApp(ctx context.Context, tx pgx.Tx, id string, in ApplicationInput) error {
	var code any
	if in.Code != nil {
		code = *in.Code
		if *in.Code == "" {
			code = nil
		}
	}
	_, err := tx.Exec(ctx, `update ext_application set
		code = case when $11 then $2 else code end,
		criticality = coalesce($3, criticality),
		supports_critical_function = coalesce($4, supports_critical_function),
		data_classification = coalesce($5, data_classification),
		hosting = coalesce($6, hosting),
		rto_minutes = coalesce($7, rto_minutes),
		rpo_minutes = coalesce($8, rpo_minutes),
		owner_team_id = case when $10 then $9::uuid else owner_team_id end
		where element_id = $1`,
		id, code, in.Criticality, in.CriticalFunction, in.DataClassification, in.Hosting, in.RTOMinutes, in.RPOMinutes,
		nullable(deref(in.TeamID)), in.TeamID != nil, in.Code != nil)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "ext_application_code_uq"):
			return engine.Duplicate("code", "codice già usato da un'altra applicazione")
		case strings.Contains(err.Error(), "foreign key"):
			return engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
	}
	return err
}

func (s *Service) UpdateApplication(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ApplicationInput) (*Application, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	app, err := s.Application(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, engine.ErrNotFound
	}
	if in.Name != nil || in.Description != nil {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			return nil, err
		}
		if _, err := s.eng.Update(ctx, tx, actor, id, engine.UpdateInput{Version: el.Version, Title: in.Name, BodyText: in.Description}); err != nil {
			return nil, err
		}
	}
	if err := writeApp(ctx, tx, id, in); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "field", "application", in, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "application.update", EntityType: AppType, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.Application(ctx, tx, id)
}

// ---------------------------------------------------------------------------
// Asset logici e composizione

type Component struct {
	ApplicationID string  `json:"application_id"`
	Key           string  `json:"key"`
	Code          *string `json:"code"` // codice di catalogo (APP-MOB): in evidenza nelle viste
	Name          string  `json:"name"`
	Relation      string  `json:"relation"` // composed_of | uses
	Team          *string `json:"team"`
	Criticality   *string `json:"criticality"`
}

type Asset struct {
	ID          string      `json:"id"`
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Kind        string      `json:"kind"`
	State       string      `json:"state"`
	OwnerUnitID *string     `json:"owner_unit_id"`
	OwnerUnit   *string     `json:"owner_unit"`
	Components  []Component `json:"components"`
}

func (s *Service) Assets(ctx context.Context, tx pgx.Tx) ([]Asset, error) {
	rows, err := tx.Query(ctx, `
		select e.id, s.key || '-' || e.number, e.title, e.body_text, x.kind, coalesce(o.name, ''), x.owner_unit_id, u.name
		from elements e
		join ext_logical_asset x on x.tenant_id = e.tenant_id and x.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join org_units u on u.tenant_id = x.tenant_id and u.id = x.owner_unit_id
		where e.deleted_at is null order by e.title`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Asset, error) {
		var a Asset
		err := r.Scan(&a.ID, &a.Key, &a.Name, &a.Description, &a.Kind, &a.State, &a.OwnerUnitID, &a.OwnerUnit)
		a.Components = []Component{}
		return a, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	idx := map[string]int{}
	ids := make([]string, len(list))
	for i, a := range list {
		idx[a.ID], ids[i] = i, a.ID
	}
	crows, err := tx.Query(ctx, `
		select r.source_id, e.id, s.key || '-' || e.number, a.code, e.title, rt.key, t.name, a.criticality
		from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key in ('composed_of', 'uses')
		join elements e on e.tenant_id = r.tenant_id and e.id = r.target_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where r.source_id = any($1::uuid[]) order by rt.key, e.title`, ids)
	if err != nil {
		return nil, err
	}
	var src string
	var c Component
	_, err = pgx.ForEachRow(crows, []any{&src, &c.ApplicationID, &c.Key, &c.Code, &c.Name, &c.Relation, &c.Team, &c.Criticality}, func() error {
		list[idx[src]].Components = append(list[idx[src]].Components, c)
		return nil
	})
	return list, err
}

type AssetInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	OwnerUnitID string `json:"owner_unit_id"`
}

var assetKinds = map[string]bool{"product": true, "service": true, "channel": true, "capability": true, "other": true}

func (s *Service) CreateAsset(ctx context.Context, tx pgx.Tx, actor engine.Actor, in AssetInput) (string, error) {
	if !actor.Has(PermWrite) {
		return "", engine.ErrForbidden
	}
	return s.createAsset(ctx, tx, actor, in)
}

func (s *Service) createAsset(ctx context.Context, tx pgx.Tx, actor engine.Actor, in AssetInput) (string, error) {
	if in.Kind == "" {
		in.Kind = "product"
	}
	if !assetKinds[in.Kind] {
		return "", engine.InvalidValue("kind", "tipo non ammesso (product, service, channel, capability, other)")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: AssetType, Title: in.Name, BodyText: in.Description, DeferStart: true})
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into ext_logical_asset (tenant_id, element_id, kind, owner_unit_id) values (app_tenant(), $1, $2, $3)`,
		el.ID, in.Kind, nullable(in.OwnerUnitID)); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return "", engine.InvalidCode("validation.org_unit_not_found", "owner_unit_id", "unità organizzativa inesistente")
		}
		return "", err
	}
	_, err = s.eng.StartProcess(ctx, tx, actor, el)
	return el.ID, err
}

func assetByName(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `select e.id from elements e join ext_logical_asset x on x.tenant_id = e.tenant_id and x.element_id = e.id
		where lower(e.title) = lower($1) and e.deleted_at is null limit 1`, strings.TrimSpace(name)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// relate crea una relazione (idempotente) e la registra nella storia di entrambi gli elementi.
func relate(ctx context.Context, tx pgx.Tx, actor engine.Actor, relKey, sourceID, targetID string) (bool, error) {
	tag, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), $1, rt.id, $3, $4, $5 from relation_types rt where rt.key = $2
		on conflict do nothing`, db.NewID(), relKey, sourceID, targetID, nullable(actor.MemberID))
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return false, engine.ErrNotFound
		}
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := engine.AddActivity(ctx, tx, sourceID, actor.MemberID, "relation", relKey, map[string]any{"target": targetID}, nil); err != nil {
		return false, err
	}
	return true, engine.AddActivity(ctx, tx, targetID, actor.MemberID, "relation", relKey+".inverse", map[string]any{"source": sourceID}, nil)
}

func unrelate(ctx context.Context, tx pgx.Tx, actor engine.Actor, relKeys []string, sourceID, targetID string) error {
	tag, err := tx.Exec(ctx, `delete from relations r using relation_types rt
		where rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = any($1) and r.source_id = $2 and r.target_id = $3`,
		relKeys, sourceID, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return engine.AddActivity(ctx, tx, sourceID, actor.MemberID, "relation", "removed", nil, map[string]any{"target": targetID})
}

func isType(ctx context.Context, tx pgx.Tx, id, typeKey string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where e.id::text = $1 and t.key = $2 and e.deleted_at is null)`, id, typeKey).Scan(&ok)
	return ok, err
}

// AddComponent collega un'applicazione a un asset logico (composto da / usa).
func (s *Service) AddComponent(ctx context.Context, tx pgx.Tx, actor engine.Actor, assetID, appID, rel string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	if rel == "" {
		rel = RelComposedOf
	}
	if rel != RelComposedOf && rel != RelUses {
		return engine.InvalidValue("relation", "relazione non ammessa (composed_of, uses)")
	}
	if ok, err := isType(ctx, tx, assetID, AssetType); err != nil || !ok {
		return firstErr(err, engine.ErrNotFound)
	}
	if ok, err := isType(ctx, tx, appID, AppType); err != nil || !ok {
		return firstErr(err, engine.NotFound("application_id", "application", "applicazione inesistente"))
	}
	// Una sola relazione tra asset e applicazione: si sostituisce l'altra.
	_, _ = tx.Exec(ctx, `delete from relations r using relation_types rt where rt.tenant_id = r.tenant_id and rt.id = r.type_id
		and rt.key in ('composed_of', 'uses') and rt.key <> $1 and r.source_id = $2 and r.target_id = $3`, rel, assetID, appID)
	_, err := relate(ctx, tx, actor, rel, assetID, appID)
	return err
}

func (s *Service) RemoveComponent(ctx context.Context, tx pgx.Tx, actor engine.Actor, assetID, appID string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	return unrelate(ctx, tx, actor, []string{RelComposedOf, RelUses}, assetID, appID)
}

func firstErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// describeFacts dà nomi e valori suggeriti ai fatti del modulo, per l'editor dei processi.
func describeFacts(eng *engine.Service) {
	crit := []string{"bassa", "media", "alta", "critica"}
	eng.DescribeFacts(RequestType,
		engine.FactInfo{Key: "asset", Name: "Asset citati", Kind: "text"},
		engine.FactInfo{Key: "applicazioni", Name: "Applicazioni impattate", Kind: "text"},
		engine.FactInfo{Key: "team", Name: "Team coinvolti", Kind: "text"},
		engine.FactInfo{Key: "classificazione_dati", Name: "Classificazione dei dati", Kind: "text"},
		engine.FactInfo{Key: "criticita_max", Name: "Criticità massima delle applicazioni", Kind: "text", Values: crit},
		engine.FactInfo{Key: "funzione_critica", Name: "Tocca una funzione critica (DORA)", Kind: "bool"},
		engine.FactInfo{Key: "impatti", Name: "Impatti sulle applicazioni", Kind: "number", Example: "3"},
		engine.FactInfo{Key: "impatti_da_stimare", Name: "Impatti ancora da stimare", Kind: "number", Example: "0"},
	)
	eng.DescribeFacts(ImpactType,
		engine.FactInfo{Key: "applicazione", Name: "Applicazione", Kind: "text"},
		engine.FactInfo{Key: "team", Name: "Team", Kind: "text"},
		engine.FactInfo{Key: "tipo_intervento", Name: "Tipo di intervento", Kind: "text",
			Values: []string{"nuova componente", "modifica", "configurazione", "dismissione", "nessun intervento"}},
		engine.FactInfo{Key: "giornate", Name: "Giornate stimate", Kind: "number"},
		engine.FactInfo{Key: "costo", Name: "Costo (euro)", Kind: "number"},
		engine.FactInfo{Key: "criticita", Name: "Criticità dell'applicazione", Kind: "text", Values: crit},
		engine.FactInfo{Key: "funzione_critica", Name: "Funzione critica (DORA)", Kind: "bool"},
		engine.FactInfo{Key: "richiesta", Name: "Richiesta", Kind: "text"},
	)
	eng.DescribeFacts(AppType,
		engine.FactInfo{Key: "codice", Name: "Codice", Kind: "text"},
		engine.FactInfo{Key: "criticita", Name: "Criticità", Kind: "text", Values: crit},
		engine.FactInfo{Key: "funzione_critica", Name: "Funzione critica (DORA)", Kind: "bool"},
		engine.FactInfo{Key: "classificazione_dati", Name: "Classificazione dei dati", Kind: "text"},
		engine.FactInfo{Key: "hosting", Name: "Hosting", Kind: "text"},
		engine.FactInfo{Key: "team", Name: "Team", Kind: "text"},
		engine.FactInfo{Key: "asset", Name: "Asset", Kind: "text"},
	)
}
