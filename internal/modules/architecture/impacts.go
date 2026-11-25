package architecture

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Impact è l'impatto di una richiesta su una applicazione: il perimetro di un team.
type Impact struct {
	ID             string   `json:"id"`
	Key            string   `json:"key"`
	Title          string   `json:"title"`
	State          string   `json:"state"`
	StateCategory  string   `json:"state_category"`
	RequestID      string   `json:"request_id"`
	RequestKey     string   `json:"request_key"`
	RequestTitle   string   `json:"request_title"`
	ApplicationID  string   `json:"application_id"`
	ApplicationKey string   `json:"application_key"`
	Application    string   `json:"application"`
	Criticality    *string  `json:"criticality"`
	CriticalFunc   bool     `json:"supports_critical_function"`
	TeamID         *string  `json:"team_id"`
	Team           *string  `json:"team"`
	ChangeKind     string   `json:"change_kind"`
	TechnicalNote  string   `json:"technical_note"`
	EffortDays     *float64 `json:"estimate_effort_days"`
	CostCents      *int64   `json:"estimate_cost_cents"`
	DependsOn      []string `json:"depends_on"`
}

const selectImpact = `
	select e.id, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''),
	       x.request_id, rs.key || '-' || r.number, r.title,
	       x.application_id, aps.key || '-' || ap.number, ap.title, a.criticality, coalesce(a.supports_critical_function, false),
	       x.team_id, t.name, x.change_kind, x.technical_note, x.estimate_effort_days::float8, x.estimate_cost_cents,
	       coalesce((select array_agg(ds.key || '-' || d.number order by d.number) from relations rel
	                 join relation_types rt on rt.tenant_id = rel.tenant_id and rt.id = rel.type_id and rt.key = 'depends_on'
	                 join elements d on d.tenant_id = rel.tenant_id and d.id = rel.target_id and d.deleted_at is null
	                 join spaces ds on ds.tenant_id = d.tenant_id and ds.id = d.space_id
	                 where rel.source_id = e.id), '{}')
	from ext_impact x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join elements r on r.tenant_id = x.tenant_id and r.id = x.request_id
	join spaces rs on rs.tenant_id = r.tenant_id and rs.id = r.space_id
	join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
	join spaces aps on aps.tenant_id = ap.tenant_id and aps.id = ap.space_id
	left join ext_application a on a.tenant_id = ap.tenant_id and a.element_id = ap.id
	left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id`

func scanImpacts(rows pgx.Rows) ([]Impact, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Impact, error) {
		var i Impact
		err := r.Scan(&i.ID, &i.Key, &i.Title, &i.State, &i.StateCategory, &i.RequestID, &i.RequestKey, &i.RequestTitle,
			&i.ApplicationID, &i.ApplicationKey, &i.Application, &i.Criticality, &i.CriticalFunc,
			&i.TeamID, &i.Team, &i.ChangeKind, &i.TechnicalNote, &i.EffortDays, &i.CostCents, &i.DependsOn)
		return i, err
	})
}

// ImpactView restituisce i dati dell'impatto per il dettaglio dell'elemento (nil se non è un impatto).
func (s *Service) ImpactView(ctx context.Context, tx pgx.Tx, elementID string) (*Impact, error) {
	rows, err := tx.Query(ctx, selectImpact+` where x.element_id = $1`, elementID)
	if err != nil {
		return nil, err
	}
	list, err := scanImpacts(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// CitedAsset è un asset logico citato dalla richiesta.
type CitedAsset struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ProposedApp è un'applicazione suggerita dalla composizione degli asset citati.
type ProposedApp struct {
	ApplicationID string  `json:"application_id"`
	Key           string  `json:"key"`
	Name          string  `json:"name"`
	Via           string  `json:"via"`      // asset da cui arriva
	Relation      string  `json:"relation"` // composed_of | uses
	Team          *string `json:"team"`
}

// Analysis è la situazione dell'analisi tecnica di una richiesta.
type Analysis struct {
	Assets   []CitedAsset  `json:"assets"`
	Proposed []ProposedApp `json:"proposed"`
	Impacts  []Impact      `json:"impacts"`
	CanEdit  bool          `json:"can_edit"`
}

func (s *Service) request(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != RequestType {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// canAnalyse: chi può scrivere gli elementi o ha un ruolo di analisi sulla richiesta.
func (s *Service) canAnalyse(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element) (bool, error) {
	if actor.Has("element.write") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, el.ID, actor.MemberID, analystRoles)
}

// canCite: in più il richiedente e il referente di business possono citare gli asset.
func (s *Service) canCite(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element) (bool, error) {
	if ok, err := s.canAnalyse(ctx, tx, actor, el); err != nil || ok {
		return ok, err
	}
	return engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{"requester", "business_owner"})
}

func (s *Service) Analysis(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Analysis, error) {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &Analysis{Assets: []CitedAsset{}, Proposed: []ProposedApp{}}
	if out.CanEdit, err = s.canAnalyse(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select a.id, s.key || '-' || a.number, a.title from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'concerns'
		join elements a on a.tenant_id = r.tenant_id and a.id = r.target_id and a.deleted_at is null
		join spaces s on s.tenant_id = a.tenant_id and s.id = a.space_id
		where r.source_id = $1 order by a.title`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Assets, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CitedAsset]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `
		select distinct on (ap.id) ap.id, s.key || '-' || ap.number, ap.title, asset.title, rt2.key, t.name
		from relations c
		join relation_types rt on rt.tenant_id = c.tenant_id and rt.id = c.type_id and rt.key = 'concerns'
		join elements asset on asset.tenant_id = c.tenant_id and asset.id = c.target_id
		join relations comp on comp.tenant_id = c.tenant_id and comp.source_id = asset.id
		join relation_types rt2 on rt2.tenant_id = comp.tenant_id and rt2.id = comp.type_id and rt2.key in ('composed_of', 'uses')
		join elements ap on ap.tenant_id = comp.tenant_id and ap.id = comp.target_id and ap.deleted_at is null
		join spaces s on s.tenant_id = ap.tenant_id and s.id = ap.space_id
		left join ext_application a on a.tenant_id = ap.tenant_id and a.element_id = ap.id
		left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where c.source_id = $1
		  and not exists (select 1 from ext_impact x where x.request_id = $1 and x.application_id = ap.id)
		order by ap.id, rt2.key`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Proposed, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ProposedApp]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, selectImpact+` where x.request_id = $1 order by e.number`, el.ID)
	if err != nil {
		return nil, err
	}
	out.Impacts, err = scanImpacts(rows)
	return out, err
}

// CiteAsset collega la richiesta a un asset logico.
func (s *Service) CiteAsset(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, assetID string) error {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return err
	}
	if ok, err := s.canCite(ctx, tx, actor, el); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	if ok, err := isType(ctx, tx, assetID, AssetType); err != nil || !ok {
		return firstErr(err, engine.NotFound("asset_id", "asset", "asset logico inesistente"))
	}
	_, err = relate(ctx, tx, actor, RelConcerns, el.ID, assetID)
	return err
}

func (s *Service) UnciteAsset(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, assetID string) error {
	el, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return err
	}
	if ok, err := s.canCite(ctx, tx, actor, el); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	return unrelate(ctx, tx, actor, []string{RelConcerns}, el.ID, assetID)
}

// ImpactInput crea o modifica un impatto.
type ImpactInput struct {
	ApplicationID string   `json:"application_id"`
	TeamID        *string  `json:"team_id"`
	ChangeKind    *string  `json:"change_kind"`
	TechnicalNote *string  `json:"technical_note"`
	EffortDays    *float64 `json:"estimate_effort_days"`
	CostCents     *int64   `json:"estimate_cost_cents"`
	DependsOn     []string `json:"depends_on"` // chiavi o id di altri impatti della stessa richiesta
}

func (in ImpactInput) validate() error {
	if in.ChangeKind != nil {
		if _, ok := changeKinds[*in.ChangeKind]; !ok {
			return engine.InvalidValue("change_kind", "tipo di intervento non ammesso (new, change, config, decommission, none)")
		}
	}
	if in.EffortDays != nil && *in.EffortDays < 0 {
		return engine.InvalidCode("validation.not_negative", "estimate_effort_days", "non può essere negativa")
	}
	if in.CostCents != nil && *in.CostCents < 0 {
		return engine.InvalidCode("validation.not_negative", "estimate_cost_cents", "non può essere negativo")
	}
	return nil
}

// CreateImpact apre l'impatto della richiesta su una applicazione. Il team è quello
// owner dell'applicazione (se non indicato); il suo referente diventa "Referente del
// team" sull'impatto, chi lo crea "Analista tecnico".
func (s *Service) CreateImpact(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ImpactInput) (*Impact, error) {
	req, err := s.request(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canAnalyse(ctx, tx, actor, req); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	return s.createImpact(ctx, tx, actor, req, in)
}

// createImpact è la creazione vera e propria, senza il controllo dei permessi: la usano
// anche le regole dei processi (azione create_impact), che agiscono per conto del motore.
func (s *Service) createImpact(ctx context.Context, tx pgx.Tx, actor engine.Actor, req *engine.Element, in ImpactInput) (*Impact, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	app, err := s.Application(ctx, tx, in.ApplicationID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, engine.NotFound("application_id", "application", "applicazione inesistente")
	}
	var dup bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		where x.request_id = $1 and x.application_id = $2 and e.deleted_at is null)`, req.ID, app.ID).Scan(&dup); err != nil {
		return nil, err
	}
	if dup {
		return nil, engine.InvalidParams("validation.architecture.impact_exists", "application_id", map[string]any{"application": app.Name}, "la richiesta ha già un impatto su %s", app.Name)
	}
	teamID := app.TeamID
	if in.TeamID != nil {
		teamID = in.TeamID
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: RequestSpace, TypeKey: ImpactType,
		Title: app.Name + " · " + req.Title, ParentID: req.ID, DeferStart: true})
	if err != nil {
		return nil, err
	}
	kind := "change"
	if in.ChangeKind != nil {
		kind = *in.ChangeKind
	}
	note := ""
	if in.TechnicalNote != nil {
		note = *in.TechnicalNote
	}
	if _, err := tx.Exec(ctx, `insert into ext_impact (tenant_id, element_id, request_id, application_id, team_id, change_kind,
		technical_note, estimate_effort_days, estimate_cost_cents) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`,
		el.ID, req.ID, app.ID, nullable(deref(teamID)), kind, note, in.EffortDays, in.CostCents); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
		return nil, err
	}
	if err := engine.SetStakeholder(ctx, tx, actor, el.ID, "technical_analyst", actor.MemberID, "", false); err != nil {
		return nil, err
	}
	if err := setTeamLead(ctx, tx, actor, el.ID, teamID); err != nil {
		return nil, err
	}
	if err := s.setDependencies(ctx, tx, actor, req.ID, el.ID, in.DependsOn); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, req.ID, actor.MemberID, "impact", "created",
		map[string]any{"impact": el.Key, "application": app.Name}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "impact.create", EntityType: ImpactType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"request": req.Key, "application": app.Key}}); err != nil {
		return nil, err
	}
	return s.ImpactView(ctx, tx, el.ID)
}

func setTeamLead(ctx context.Context, tx pgx.Tx, actor engine.Actor, impactID string, teamID *string) error {
	if teamID == nil || *teamID == "" {
		return nil
	}
	var lead *string
	if err := tx.QueryRow(ctx, "select lead_id from teams where id = $1", *teamID).Scan(&lead); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
		return err
	}
	if lead == nil {
		return nil
	}
	return engine.SetStakeholder(ctx, tx, actor, impactID, "team_lead", *lead, "", true)
}

func (s *Service) setDependencies(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, impactID string, refs []string) error {
	for _, ref := range refs {
		var depID string
		err := tx.QueryRow(ctx, `select e.id from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join ext_impact x on x.tenant_id = e.tenant_id and x.element_id = e.id
			where (e.id::text = $1 or upper(s.key || '-' || e.number) = upper($1)) and x.request_id = $2 and e.deleted_at is null`,
			strings.TrimSpace(ref), requestID).Scan(&depID)
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.InvalidParams("validation.architecture.depends_on_unknown", "depends_on", map[string]any{"ref": ref}, "%q non è un impatto di questa richiesta", ref)
		}
		if err != nil {
			return err
		}
		if depID == impactID {
			return engine.InvalidCode("validation.architecture.depends_on_self", "depends_on", "un impatto non può dipendere da sé stesso")
		}
		if _, err := relate(ctx, tx, actor, RelDependsOn, impactID, depID); err != nil {
			return err
		}
	}
	return nil
}

// UpdateImpact modifica i dati dell'analisi tecnica. Le dipendenze indicate sostituiscono le precedenti.
func (s *Service) UpdateImpact(ctx context.Context, tx pgx.Tx, actor engine.Actor, impactID string, in ImpactInput) (*Impact, error) {
	cur, err := s.ImpactView(ctx, tx, impactID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, engine.ErrNotFound
	}
	req, err := s.request(ctx, tx, actor, cur.RequestID)
	if err != nil {
		return nil, err
	}
	ok, err := s.canAnalyse(ctx, tx, actor, req)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Il referente del team può correggere la stima del suo perimetro.
		if ok, err = engine.HasRole(ctx, tx, impactID, actor.MemberID, []string{"team_lead", "technical_analyst"}); err != nil || !ok {
			return nil, firstErr(err, engine.ErrForbidden)
		}
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update ext_impact set
		team_id = case when $2 then $3::uuid else team_id end,
		change_kind = coalesce($4, change_kind),
		technical_note = coalesce($5, technical_note),
		estimate_effort_days = coalesce($6, estimate_effort_days),
		estimate_cost_cents = coalesce($7, estimate_cost_cents)
		where element_id = $1`, impactID, in.TeamID != nil, nullable(deref(in.TeamID)), in.ChangeKind, in.TechnicalNote, in.EffortDays, in.CostCents); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
		return nil, err
	}
	if in.TeamID != nil {
		if err := setTeamLead(ctx, tx, actor, impactID, in.TeamID); err != nil {
			return nil, err
		}
	}
	if in.DependsOn != nil {
		if _, err := tx.Exec(ctx, `delete from relations r using relation_types rt where rt.tenant_id = r.tenant_id and rt.id = r.type_id
			and rt.key = 'depends_on' and r.source_id = $1`, impactID); err != nil {
			return nil, err
		}
		if err := s.setDependencies(ctx, tx, actor, cur.RequestID, impactID, in.DependsOn); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update elements set version = version + 1, updated_by = $2 where id = $1", impactID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, impactID, actor.MemberID, "field", "impact", in, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "impact.update", EntityType: ImpactType, EntityID: impactID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.ImpactView(ctx, tx, impactID)
}

// ---------------------------------------------------------------------------
// Fatti per le condizioni di processo

// requestFacts: asset citati, applicazioni e team impattati, criticità massima,
// funzione critica DORA, classificazione dei dati, impatti da stimare.
func (s *Service) requestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	rows, err := tx.Query(ctx, `select a.title from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'concerns'
		join elements a on a.tenant_id = r.tenant_id and a.id = r.target_id and a.deleted_at is null
		where r.source_id = $1`, el.ID)
	if err != nil {
		return err
	}
	assets, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	f.Set("asset", toAny(assets)...)
	var apps, teams, classes []string
	var crit *string
	var critical bool
	var total, toEstimate int
	err = tx.QueryRow(ctx, `
		select coalesce(array_agg(distinct ap.title) filter (where ap.title is not null), '{}'),
		       coalesce(array_agg(distinct t.name) filter (where t.name is not null), '{}'),
		       coalesce(array_agg(distinct a.data_classification) filter (where a.data_classification is not null), '{}'),
		       (array_agg(a.criticality order by case a.criticality when 'critical' then 4 when 'high' then 3 when 'medium' then 2 when 'low' then 1 else 0 end desc)
		          filter (where a.criticality is not null))[1],
		       coalesce(bool_or(a.supports_critical_function), false),
		       count(*),
		       count(*) filter (where x.estimate_effort_days is null and coalesce(o.category, '') not in ('done', 'cancelled'))
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join ext_application a on a.tenant_id = ap.tenant_id and a.element_id = ap.id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled'`, el.ID).
		Scan(&apps, &teams, &classes, &crit, &critical, &total, &toEstimate)
	if err != nil {
		return err
	}
	f.Set("applicazioni", toAny(apps)...)
	f.Set("team", toAny(teams)...)
	f.Set("classificazione_dati", toAny(classes)...)
	if crit != nil {
		f.Set("criticita_max", *crit, critNames[*crit])
	}
	f.Set("funzione_critica", critical)
	f.Set("impatti", total)
	f.Set("impatti_da_stimare", toEstimate)
	return nil
}

func (s *Service) impactFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	i, err := s.ImpactView(ctx, tx, el.ID)
	if err != nil || i == nil {
		return err
	}
	f.Set("applicazione", i.Application, i.ApplicationKey)
	f.Set("team", i.Team)
	f.Set("tipo_intervento", i.ChangeKind, changeKinds[i.ChangeKind])
	f.Set("giornate", i.EffortDays)
	if i.CostCents != nil {
		f.Set("costo", float64(*i.CostCents)/100)
	}
	if i.Criticality != nil {
		f.Set("criticita", *i.Criticality, critNames[*i.Criticality])
	}
	f.Set("funzione_critica", i.CriticalFunc)
	f.Set("richiesta", i.RequestKey)
	// Classificazione dei dati dell'applicazione: serve, per esempio, alle precompilazioni
	// dei questionari di valutazione per applicazione.
	var class *string
	if err := tx.QueryRow(ctx, "select data_classification from ext_application where element_id = $1", i.ApplicationID).Scan(&class); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	f.Set("classificazione_dati", class)
	return nil
}

func (s *Service) appFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	a, err := s.Application(ctx, tx, el.ID)
	if err != nil || a == nil {
		return err
	}
	f.Set("codice", a.Code)
	if a.Criticality != nil {
		f.Set("criticita", *a.Criticality, critNames[*a.Criticality])
	}
	f.Set("funzione_critica", a.CriticalFunction)
	f.Set("classificazione_dati", a.DataClassification)
	f.Set("hosting", a.Hosting)
	f.Set("team", a.Team)
	f.Set("asset", toAny(a.Assets)...)
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
