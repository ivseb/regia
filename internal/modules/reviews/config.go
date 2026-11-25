package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// La configurazione delle valutazioni (team, tipi, modelli) la gestisce l'amministratore
// di processo: fa parte dell'SDLC configurabile (sdlc.md §1-2). Leggerla basta element.read.

func requireAdmin(a engine.Actor) error {
	if !a.Has(rbac.ProcessAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

func requireRead(a engine.Actor) error {
	if !a.Has(rbac.ElementRead) {
		return engine.ErrForbidden
	}
	return nil
}

func auditCfg(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

// ---------------------------------------------------------------------------
// Team valutatori

// Team è un team valutatore: un gruppo esistente con responsabile, SLA ed escalation.
type Team struct {
	ID             string  `json:"id"`
	Key            string  `json:"key"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	GroupID        string  `json:"group_id"`
	Group          string  `json:"group"`
	Members        int     `json:"members"`
	LeadID         *string `json:"lead_id"`
	Lead           *string `json:"lead"`
	SLADays        int     `json:"sla_days"`
	ReminderDays   int     `json:"reminder_days"`
	EscalationDays *int    `json:"escalation_days"`
	Archived       bool    `json:"archived"`
}

const selectTeam = `
	select t.id, t.key, t.name, t.description, t.group_id, g.name,
	       (select count(*) from group_members gm where gm.group_id = t.group_id)::int,
	       t.lead_id, m.display_name, t.sla_days, t.reminder_days, t.escalation_days, t.archived
	from review_teams t
	join groups g on g.tenant_id = t.tenant_id and g.id = t.group_id
	left join members m on m.tenant_id = t.tenant_id and m.id = t.lead_id`

func (s *Service) Teams(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Team, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectTeam+` order by t.archived, t.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Team])
}

func (s *Service) team(ctx context.Context, tx pgx.Tx, id string) (*Team, error) {
	rows, err := tx.Query(ctx, selectTeam+` where t.id::text = $1 or t.key = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Team])
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

// TeamInput crea o modifica un team valutatore. Group è il nome di un gruppo esistente
// (in alternativa GroupID). EscalationDays 0 = nessuna escalation.
type TeamInput struct {
	Key            string  `json:"key"`
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	GroupID        *string `json:"group_id"`
	Group          *string `json:"group"`
	LeadID         *string `json:"lead_id"` // "" = nessuno
	SLADays        *int    `json:"sla_days"`
	ReminderDays   *int    `json:"reminder_days"`
	EscalationDays *int    `json:"escalation_days"`
	Archived       *bool   `json:"archived"`
}

func (in *TeamInput) resolveGroup(ctx context.Context, tx pgx.Tx) error {
	if in.Group == nil || in.GroupID != nil {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, "select id from groups where lower(name) = lower($1)", strings.TrimSpace(*in.Group)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.NotFound("group", "group", "gruppo %q inesistente", *in.Group)
	}
	in.GroupID = &id
	return err
}

func (in TeamInput) validate() error {
	if in.Name != nil {
		if n := strings.TrimSpace(*in.Name); n == "" || len(n) > 120 {
			return engine.TooLong("name", 120, "nome del team obbligatorio (massimo 120 caratteri)")
		}
	}
	if in.SLADays != nil && (*in.SLADays < 1 || *in.SLADays > 250) {
		return engine.OutOfRange("sla_days", 1, 250, "lo SLA va da 1 a 250 giorni lavorativi")
	}
	if in.ReminderDays != nil && (*in.ReminderDays < 0 || *in.ReminderDays > 60) {
		return engine.OutOfRange("reminder_days", 0, 60, "il sollecito va da 0 a 60 giorni lavorativi prima della scadenza")
	}
	if in.EscalationDays != nil && (*in.EscalationDays < 0 || *in.EscalationDays > 120) {
		return engine.OutOfRange("escalation_days", 1, 120, "l'escalation va da 1 a 120 giorni lavorativi di ritardo (0 = nessuna)")
	}
	return nil
}

func dbErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "review_teams_key_uq"), strings.Contains(msg, "review_types_key_uq"):
		return engine.Duplicate("key", "chiave già usata")
	case strings.Contains(msg, "review_teams_name_uq"):
		return engine.Duplicate("name", "esiste già un team valutatore con questo nome")
	case strings.Contains(msg, "foreign key"):
		return engine.NotFound("", "", "riferimento inesistente (gruppo, membro o team)")
	}
	return err
}

func (s *Service) CreateTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TeamInput) (*Team, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.createTeam(ctx, tx, actor, in)
}

func (s *Service) createTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TeamInput) (*Team, error) {
	if !keyRe.MatchString(in.Key) {
		return nil, engine.InvalidValue("key", "chiave non valida (minuscole, cifre e _)")
	}
	if in.Name == nil {
		return nil, engine.Required("name", "nome del team obbligatorio")
	}
	if err := in.resolveGroup(ctx, tx); err != nil {
		return nil, err
	}
	if in.GroupID == nil {
		return nil, engine.Required("group_id", "indica il gruppo delle persone del team")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	id := db.NewID()
	sla, rem := 5, 1
	if in.SLADays != nil {
		sla = *in.SLADays
	}
	if in.ReminderDays != nil {
		rem = *in.ReminderDays
	}
	var esc any
	if in.EscalationDays != nil && *in.EscalationDays > 0 {
		esc = *in.EscalationDays
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	lead := ""
	if in.LeadID != nil {
		lead = *in.LeadID
	}
	if _, err := tx.Exec(ctx, `insert into review_teams (tenant_id, id, key, name, description, group_id, lead_id, sla_days, reminder_days, escalation_days)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, in.Key, strings.TrimSpace(*in.Name), desc, *in.GroupID, nullable(lead), sla, rem, esc); err != nil {
		return nil, dbErr(err)
	}
	if err := auditCfg(ctx, tx, actor, "review_team.create", "review_team", id, map[string]any{"key": in.Key}); err != nil {
		return nil, err
	}
	return s.team(ctx, tx, id)
}

func (s *Service) UpdateTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in TeamInput) (*Team, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	t, err := s.team(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := in.resolveGroup(ctx, tx); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	escSet := in.EscalationDays != nil
	var esc any
	if escSet && *in.EscalationDays > 0 {
		esc = *in.EscalationDays
	}
	if _, err := tx.Exec(ctx, `update review_teams set
		name = coalesce(nullif(trim($2), ''), name),
		description = coalesce($3, description),
		group_id = coalesce($4::uuid, group_id),
		lead_id = case when $5 then $6::uuid else lead_id end,
		sla_days = coalesce($7, sla_days),
		reminder_days = coalesce($8, reminder_days),
		escalation_days = case when $9 then $10::int else escalation_days end,
		archived = coalesce($11, archived),
		updated_at = now()
		where id = $1`, t.ID, in.Name, in.Description, in.GroupID, in.LeadID != nil, nullable(deref(in.LeadID)),
		in.SLADays, in.ReminderDays, escSet, esc, in.Archived); err != nil {
		return nil, dbErr(err)
	}
	if err := auditCfg(ctx, tx, actor, "review_team.update", "review_team", t.ID, map[string]any{"key": t.Key}); err != nil {
		return nil, err
	}
	return s.team(ctx, tx, t.ID)
}

// ---------------------------------------------------------------------------
// Tipi di valutazione

// Outcome è un esito ammesso. Polarity: positive (favorevole, senza prescrizioni),
// conditional (servono prescrizioni accettate), negative (non favorevole: blocca il gate).
type Outcome struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Polarity string `json:"polarity"`
}

// DefaultOutcomes sono gli esiti di default (sdlc.md §4).
var DefaultOutcomes = []Outcome{
	{"favorevole", "Favorevole", "positive"},
	{"favorevole_con_prescrizioni", "Favorevole con prescrizioni", "conditional"},
	{"non_favorevole", "Non favorevole", "negative"},
}

// ReviewType è un tipo di valutazione.
type ReviewType struct {
	ID           string    `json:"id"`
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	TeamID       string    `json:"team_id"`
	Team         string    `json:"team"`
	Scope        string    `json:"scope"` // request | impact
	TemplateKey  string    `json:"template_key"`
	Template     *string   `json:"template"`         // nome della versione corrente
	TemplateVer  *int      `json:"template_version"` // numero della versione corrente
	Mandatory    bool      `json:"mandatory"`
	Outcomes     []Outcome `json:"outcomes"`
	Deciders     string    `json:"deciders"` // team | lead
	OwnerRoles   []string  `json:"owner_roles"`
	Archived     bool      `json:"archived"`
	GroupID      string    `json:"-"`
	LeadID       *string   `json:"-"`
	TeamName     string    `json:"-"`
	TemplateIDCu *string   `json:"-"`
}

const selectType = `
	select rt.id, rt.key, rt.name, rt.description, rt.team_id, t.name, rt.scope, rt.template_key, q.name, q.version,
	       rt.mandatory, rt.outcomes, rt.deciders, rt.owner_roles, rt.archived, t.group_id, t.lead_id, q.id
	from review_types rt
	join review_teams t on t.tenant_id = rt.tenant_id and t.id = rt.team_id
	left join questionnaire_templates q on q.tenant_id = rt.tenant_id and q.key = rt.template_key and q.status = 'published'`

func scanTypes(rows pgx.Rows) ([]ReviewType, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReviewType, error) {
		var t ReviewType
		var outcomes []byte
		err := r.Scan(&t.ID, &t.Key, &t.Name, &t.Description, &t.TeamID, &t.Team, &t.Scope, &t.TemplateKey, &t.Template, &t.TemplateVer,
			&t.Mandatory, &outcomes, &t.Deciders, &t.OwnerRoles, &t.Archived, &t.GroupID, &t.LeadID, &t.TemplateIDCu)
		if err == nil {
			err = json.Unmarshal(outcomes, &t.Outcomes)
		}
		t.TeamName = t.Team
		return t, err
	})
}

func (s *Service) Types(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]ReviewType, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectType+` order by rt.archived, rt.name`)
	if err != nil {
		return nil, err
	}
	return scanTypes(rows)
}

func (s *Service) reviewType(ctx context.Context, tx pgx.Tx, ref string) (*ReviewType, error) {
	rows, err := tx.Query(ctx, selectType+` where rt.id::text = $1 or rt.key = $1`, ref)
	if err != nil {
		return nil, err
	}
	list, err := scanTypes(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

// TypeInput crea o modifica un tipo di valutazione. Team è la chiave del team (o TeamID).
type TypeInput struct {
	Key         string    `json:"key"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	TeamID      *string   `json:"team_id"`
	Team        *string   `json:"team"`
	Scope       *string   `json:"scope"`
	TemplateKey *string   `json:"template_key"`
	Mandatory   *bool     `json:"mandatory"`
	Outcomes    []Outcome `json:"outcomes"`
	Deciders    *string   `json:"deciders"`
	OwnerRoles  []string  `json:"owner_roles"`
	Archived    *bool     `json:"archived"`
}

func validateOutcomes(os []Outcome) error {
	if len(os) == 0 {
		return engine.Required("outcomes", "serve almeno un esito")
	}
	seen := map[string]bool{}
	pos := false
	for i, o := range os {
		if !keyRe.MatchString(o.Key) {
			return engine.InvalidCode("validation.reviews.outcome_key_invalid", fmt.Sprintf("outcomes[%d].key", i), "chiave non valida (minuscole, cifre e _)")
		}
		if seen[o.Key] {
			return engine.InvalidParams("validation.reviews.outcome_repeated", fmt.Sprintf("outcomes[%d].key", i), map[string]any{"key": o.Key}, "esito %q ripetuto", o.Key)
		}
		seen[o.Key] = true
		if strings.TrimSpace(o.Label) == "" {
			return engine.Required(fmt.Sprintf("outcomes[%d].label", i), "etichetta obbligatoria")
		}
		switch o.Polarity {
		case "positive", "conditional":
			pos = true
		case "negative":
		default:
			return engine.InvalidCode("validation.reviews.polarity_invalid", fmt.Sprintf("outcomes[%d].polarity", i), "polarità non ammessa (positive, conditional, negative)")
		}
	}
	if !pos {
		return engine.Required("outcomes", "serve almeno un esito favorevole")
	}
	return nil
}

func (in *TypeInput) check(ctx context.Context, tx pgx.Tx) error {
	if in.Team != nil && in.TeamID == nil {
		var id string
		err := tx.QueryRow(ctx, "select id from review_teams where key = $1 or lower(name) = lower($1)", *in.Team).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.NotFound("team", "team", "team valutatore %q inesistente", *in.Team)
		}
		if err != nil {
			return err
		}
		in.TeamID = &id
	}
	if in.Name != nil {
		if n := strings.TrimSpace(*in.Name); n == "" || len(n) > 120 {
			return engine.TooLong("name", 120, "nome obbligatorio (massimo 120 caratteri)")
		}
	}
	if in.Scope != nil && *in.Scope != "request" && *in.Scope != "impact" {
		return engine.InvalidValue("scope", "ambito non ammesso (request, impact)")
	}
	if in.Deciders != nil && *in.Deciders != "team" && *in.Deciders != "lead" {
		return engine.InvalidCode("validation.reviews.deciders", "deciders", "chi decide: team oppure lead")
	}
	if in.Outcomes != nil {
		if err := validateOutcomes(in.Outcomes); err != nil {
			return err
		}
	}
	if in.TemplateKey != nil {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from questionnaire_templates where key = $1)", *in.TemplateKey).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.NotFound("template_key", "template", "modello di questionario %q inesistente", *in.TemplateKey)
		}
	}
	for i, r := range in.OwnerRoles {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", r).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.NotFound(fmt.Sprintf("owner_roles[%d]", i), "role", "ruolo %q inesistente", r)
		}
	}
	return nil
}

func defaultOwnerRoles(scope string) []string {
	if scope == "impact" {
		return []string{"team_lead", "technical_analyst", "project_manager", "demand_manager"}
	}
	return []string{"project_manager", "demand_manager"}
}

func (s *Service) CreateType(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TypeInput) (*ReviewType, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.createType(ctx, tx, actor, in)
}

func (s *Service) createType(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TypeInput) (*ReviewType, error) {
	if !keyRe.MatchString(in.Key) {
		return nil, engine.InvalidValue("key", "chiave non valida (minuscole, cifre e _)")
	}
	if in.Name == nil || in.Scope == nil || in.TemplateKey == nil || (in.TeamID == nil && in.Team == nil) {
		return nil, engine.Required("", "servono nome, team, ambito e modello di questionario")
	}
	if err := in.check(ctx, tx); err != nil {
		return nil, err
	}
	if in.Outcomes == nil {
		in.Outcomes = DefaultOutcomes
	}
	if in.OwnerRoles == nil {
		in.OwnerRoles = defaultOwnerRoles(*in.Scope)
	}
	mandatory, deciders, desc := true, "team", ""
	if in.Mandatory != nil {
		mandatory = *in.Mandatory
	}
	if in.Deciders != nil {
		deciders = *in.Deciders
	}
	if in.Description != nil {
		desc = *in.Description
	}
	outcomes, _ := json.Marshal(in.Outcomes)
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into review_types (tenant_id, id, key, name, description, team_id, scope, template_key, mandatory, outcomes, deciders, owner_roles)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, in.Key, strings.TrimSpace(*in.Name), desc, *in.TeamID, *in.Scope, *in.TemplateKey, mandatory, outcomes, deciders, in.OwnerRoles); err != nil {
		return nil, dbErr(err)
	}
	if err := auditCfg(ctx, tx, actor, "review_type.create", "review_type", id, map[string]any{"key": in.Key}); err != nil {
		return nil, err
	}
	return s.reviewType(ctx, tx, id)
}

func (s *Service) UpdateType(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in TypeInput) (*ReviewType, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	t, err := s.reviewType(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := in.check(ctx, tx); err != nil {
		return nil, err
	}
	var outcomes []byte
	if in.Outcomes != nil {
		outcomes, _ = json.Marshal(in.Outcomes)
	}
	if _, err := tx.Exec(ctx, `update review_types set
		name = coalesce(nullif(trim($2), ''), name), description = coalesce($3, description),
		team_id = coalesce($4::uuid, team_id), scope = coalesce($5, scope), template_key = coalesce($6, template_key),
		mandatory = coalesce($7, mandatory), outcomes = coalesce($8::jsonb, outcomes), deciders = coalesce($9, deciders),
		owner_roles = coalesce($10, owner_roles), archived = coalesce($11, archived), updated_at = now()
		where id = $1`, t.ID, in.Name, in.Description, in.TeamID, in.Scope, in.TemplateKey, in.Mandatory, outcomes, in.Deciders,
		in.OwnerRoles, in.Archived); err != nil {
		return nil, dbErr(err)
	}
	// Le valutazioni già aperte conservano il loro esito e la versione del modello.
	if err := auditCfg(ctx, tx, actor, "review_type.update", "review_type", t.ID, map[string]any{"key": t.Key}); err != nil {
		return nil, err
	}
	return s.reviewType(ctx, tx, t.ID)
}

// ---------------------------------------------------------------------------
// Modelli di questionario, versionati come i processi e le scorecard

// TemplateVersion è una versione di un modello.
type TemplateVersion struct {
	ID          string      `json:"id"`
	Key         string      `json:"key"`
	Version     int         `json:"version"`
	Status      string      `json:"status"` // draft | published | superseded
	Name        string      `json:"name"`
	Notes       string      `json:"notes"`
	CreatedAt   time.Time   `json:"created_at"`
	PublishedAt *time.Time  `json:"published_at"`
	PublishedBy *string     `json:"published_by"`
	Reviews     int         `json:"reviews"` // valutazioni legate a questa versione
	Definition  *Definition `json:"definition,omitempty"`
}

const selectTemplate = `
	select q.id, q.key, q.version, q.status, q.name, q.notes, q.created_at, q.published_at, pm.display_name,
	       (select count(*) from ext_review r where r.tenant_id = q.tenant_id and r.template_id = q.id)::int, q.definition
	from questionnaire_templates q
	left join members pm on pm.tenant_id = q.tenant_id and pm.id = q.published_by`

func scanTemplates(rows pgx.Rows) ([]*TemplateVersion, error) {
	defer rows.Close()
	var out []*TemplateVersion
	for rows.Next() {
		v := &TemplateVersion{Definition: &Definition{}}
		var def []byte
		if err := rows.Scan(&v.ID, &v.Key, &v.Version, &v.Status, &v.Name, &v.Notes, &v.CreatedAt, &v.PublishedAt, &v.PublishedBy, &v.Reviews, &def); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(def, v.Definition); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Template è un modello con versione corrente, bozza e storia delle versioni.
type Template struct {
	Key           string             `json:"key"`
	Name          string             `json:"name"`
	Current       *TemplateVersion   `json:"current"`
	Draft         *TemplateVersion   `json:"draft"`
	DraftProblems []Problem          `json:"draft_problems,omitempty"`
	Versions      []*TemplateVersion `json:"versions"` // dalla più recente, senza definizione
	CanEdit       bool               `json:"can_edit"`
}

// Templates elenca i modelli (senza le definizioni).
func (s *Service) Templates(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Template, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectTemplate+` order by q.key, q.version desc`)
	if err != nil {
		return nil, err
	}
	vs, err := scanTemplates(rows)
	if err != nil {
		return nil, err
	}
	out := []Template{}
	for _, v := range vs {
		if len(out) == 0 || out[len(out)-1].Key != v.Key {
			out = append(out, Template{Key: v.Key, Name: v.Name, Versions: []*TemplateVersion{}, CanEdit: actor.Has(rbac.ProcessAdmin)})
		}
		t := &out[len(out)-1]
		light := *v
		light.Definition = nil
		t.Versions = append(t.Versions, &light)
		switch v.Status {
		case "published":
			t.Current, t.Name = &light, v.Name
		case "draft":
			t.Draft = &light
		}
	}
	return out, nil
}

// Template restituisce un modello con le definizioni della versione corrente e della bozza.
func (s *Service) Template(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Template, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectTemplate+` where q.key = $1 order by q.version desc`, key)
	if err != nil {
		return nil, err
	}
	vs, err := scanTemplates(rows)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, engine.ErrNotFound
	}
	t := &Template{Key: key, Name: vs[0].Name, Versions: []*TemplateVersion{}, CanEdit: actor.Has(rbac.ProcessAdmin)}
	for _, v := range vs {
		switch v.Status {
		case "published":
			t.Current, t.Name = v, v.Name
		case "draft":
			t.Draft = v
			t.DraftProblems = v.Definition.Validate()
		}
		light := *v
		light.Definition = nil
		t.Versions = append(t.Versions, &light)
	}
	return t, nil
}

// TemplateVersionByNumber restituisce una versione per numero (per vedere quelle precedenti).
func (s *Service) TemplateVersionByNumber(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, n int) (*TemplateVersion, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectTemplate+` where q.key = $1 and q.version = $2`, key, n)
	if err != nil {
		return nil, err
	}
	vs, err := scanTemplates(rows)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, engine.ErrNotFound
	}
	return vs[0], nil
}

// SaveTemplateDraft crea o sostituisce la bozza del modello (una chiave nuova crea il
// modello). Salva anche con errori, che tornano in DraftProblems: la pubblicazione li rifiuta.
func (s *Service) SaveTemplateDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, raw []byte, notes string) (*Template, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if !keyRe.MatchString(key) {
		return nil, engine.InvalidValue("key", "chiave del modello non valida (minuscole, cifre e _)")
	}
	d, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	if err := saveTemplateDraft(ctx, tx, actor, key, d, notes); err != nil {
		return nil, err
	}
	return s.Template(ctx, tx, actor, key)
}

func saveTemplateDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, d *Definition, notes string) error {
	def, _ := json.Marshal(d)
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = key
	}
	tag, err := tx.Exec(ctx, `update questionnaire_templates set definition = $2, name = $3, description = $4, notes = $5, created_by = $6, created_at = now()
		where key = $1 and status = 'draft'`, key, def, name, d.Description, notes, nullable(actor.MemberID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into questionnaire_templates (tenant_id, id, key, version, status, name, description, definition, notes, created_by)
			values (app_tenant(), $1, $2, (select coalesce(max(version), 0) + 1 from questionnaire_templates where key = $2), 'draft', $3, $4, $5, $6, $7)`,
			db.NewID(), key, name, d.Description, def, notes, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	return auditCfg(ctx, tx, actor, "questionnaire.draft", "questionnaire_template", "", map[string]any{"key": key})
}

// DiscardTemplateDraft scarta la bozza.
func (s *Service) DiscardTemplateDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from questionnaire_templates where key = $1 and status = 'draft'", key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return auditCfg(ctx, tx, actor, "questionnaire.discard", "questionnaire_template", "", map[string]any{"key": key})
}

// PublishTemplate pubblica la bozza: le nuove valutazioni useranno questa versione, quelle
// già aperte restano sulla loro (sdlc.md §5).
func (s *Service) PublishTemplate(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, notes string) (*Template, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var id string
	var version int
	var raw []byte
	err := tx.QueryRow(ctx, "select id, version, definition from questionnaire_templates where key = $1 and status = 'draft' for update", key).Scan(&id, &version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidCode("validation.no_draft", "draft", "non c'è una bozza da pubblicare")
	}
	if err != nil {
		return nil, err
	}
	d, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	if ps := d.Validate(); hasErrors(ps) {
		return nil, &InvalidDefinition{Problems: ps}
	}
	if _, err := tx.Exec(ctx, "update questionnaire_templates set status = 'superseded' where key = $1 and status = 'published'", key); err != nil {
		return nil, err
	}
	if notes != "" {
		if _, err := tx.Exec(ctx, "update questionnaire_templates set notes = $2 where id = $1", id, notes); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update questionnaire_templates set status = 'published', published_by = $2, published_at = now() where id = $1",
		id, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := auditCfg(ctx, tx, actor, "questionnaire.publish", "questionnaire_template", id, map[string]any{"key": key, "version": version}); err != nil {
		return nil, err
	}
	return s.Template(ctx, tx, actor, key)
}

// loadTemplateVersion legge la definizione di una versione. Le versioni pubblicate sono
// immutabili: si tengono in memoria (l'id è unico tra i tenant e arriva da una query
// già filtrata dalla RLS, come per i processi).
func loadTemplateVersion(ctx context.Context, tx pgx.Tx, id string) (*Definition, int, error) {
	if c, ok := templateCache.Load(id); ok {
		e := c.(cachedTemplate)
		return e.def, e.version, nil
	}
	var raw []byte
	var status string
	var version int
	if err := tx.QueryRow(ctx, "select definition, status, version from questionnaire_templates where id = $1", id).Scan(&raw, &status, &version); err != nil {
		return nil, 0, err
	}
	d := &Definition{}
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, 0, err
	}
	if status != "draft" {
		templateCache.Store(id, cachedTemplate{def: d, version: version})
	}
	return d, version, nil
}

func nullable(s string) any {
	if s == "" {
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
