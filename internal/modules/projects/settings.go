package projects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Settings sono le impostazioni del tenant per i progetti (progetti.md §8).
type Settings struct {
	DefaultTemplate string `json:"default_template"`
	AutoFromRequest bool   `json:"auto_from_request"`
	ProgramsEnabled bool   `json:"programs_enabled"`
	ProgressMethod  string `json:"progress_method"` // weighted_estimate | equal
	// Tolleranze oltre cui una modifica del piano apre una change request: giorni lavorativi
	// su una milestone o sulla fine del progetto, percentuale sul budget.
	ToleranceDays      int     `json:"tolerance_days"`
	ToleranceBudgetPct float64 `json:"tolerance_budget_pct"`
	Roles              []Role  `json:"roles"`
	// AvailableRoles: i ruoli per oggetto del tenant che si possono aggiungere al progetto.
	AvailableRoles []RoleRef `json:"available_roles"`
	CanEdit        bool      `json:"can_edit"`
}

// Role è un ruolo del progetto: un ruolo per oggetto (stakeholder_roles) con le sue regole.
type Role struct {
	Key                string `json:"key"`
	Name               string `json:"name"`
	Required           bool   `json:"required"`
	InheritFromRequest bool   `json:"inherit_from_request"`
}

type RoleRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// GetSettings legge le impostazioni (i predefiniti se il tenant non le ha ancora salvate).
func GetSettings(ctx context.Context, tx pgx.Tx) (*Settings, error) {
	st := &Settings{DefaultTemplate: "mixed", AutoFromRequest: true, ProgressMethod: "weighted_estimate", ToleranceDays: 10, ToleranceBudgetPct: 5}
	err := tx.QueryRow(ctx, `select default_template, auto_from_request, programs_enabled, progress_method, tolerance_days, tolerance_budget_pct::float8
		from project_settings`).
		Scan(&st.DefaultTemplate, &st.AutoFromRequest, &st.ProgramsEnabled, &st.ProgressMethod, &st.ToleranceDays, &st.ToleranceBudgetPct)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select r.key, r.name, pr.required, pr.inherit_from_request from project_roles pr
		join stakeholder_roles r on r.tenant_id = pr.tenant_id and r.id = pr.role_id order by pr.ordinal, r.name`)
	if err != nil {
		return nil, err
	}
	if st.Roles, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Role]); err != nil {
		return nil, err
	}
	return st, nil
}

// SettingsFor aggiunge i ruoli disponibili e se l'attore può modificarle.
func (s *Service) SettingsFor(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Settings, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	st.CanEdit = actor.Has("tenant.admin")
	rows, err := tx.Query(ctx, `select key, name from stakeholder_roles order by name`)
	if err != nil {
		return nil, err
	}
	st.AvailableRoles, err = pgx.CollectRows(rows, pgx.RowToStructByPos[RoleRef])
	return st, err
}

// SettingsInput sostituisce le impostazioni; Roles, se indicato, sostituisce i ruoli del progetto.
type SettingsInput struct {
	DefaultTemplate    *string  `json:"default_template"`
	AutoFromRequest    *bool    `json:"auto_from_request"`
	ProgramsEnabled    *bool    `json:"programs_enabled"`
	ProgressMethod     *string  `json:"progress_method"`
	ToleranceDays      *int     `json:"tolerance_days"`
	ToleranceBudgetPct *float64 `json:"tolerance_budget_pct"`
	Roles              []Role   `json:"roles"`
}

func (s *Service) SaveSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (*Settings, error) {
	if !actor.Has("tenant.admin") {
		return nil, engine.ErrForbidden
	}
	if in.DefaultTemplate != nil && !slices.Contains(Templates, *in.DefaultTemplate) {
		return nil, engine.InvalidCode("validation.projects.template", "default_template", "modello non ammesso (agile, waterfall, mixed, contract)")
	}
	if in.ProgressMethod != nil && *in.ProgressMethod != "weighted_estimate" && *in.ProgressMethod != "equal" {
		return nil, engine.InvalidValue("progress_method", "metodo di avanzamento non ammesso (weighted_estimate, equal)")
	}
	if in.ToleranceDays != nil && (*in.ToleranceDays < 0 || *in.ToleranceDays > 365) {
		return nil, engine.InvalidCode("validation.projects.tolerance_days", "tolerance_days", "la tolleranza va da 0 a 365 giorni lavorativi")
	}
	if in.ToleranceBudgetPct != nil && (math.IsNaN(*in.ToleranceBudgetPct) || *in.ToleranceBudgetPct < 0 || *in.ToleranceBudgetPct > 100) {
		return nil, engine.InvalidCode("validation.projects.tolerance_budget", "tolerance_budget_pct", "la tolleranza sul budget va da 0 a 100%%")
	}
	if _, err := tx.Exec(ctx, `insert into project_settings (tenant_id, default_template, auto_from_request, programs_enabled, progress_method,
		tolerance_days, tolerance_budget_pct, updated_by)
		values (app_tenant(), coalesce($1, 'mixed'), coalesce($2, true), coalesce($3, false), coalesce($4, 'weighted_estimate'),
		  coalesce($6, 10), coalesce($7, 5), $5)
		on conflict (tenant_id) do update set
		  default_template = coalesce($1, project_settings.default_template),
		  auto_from_request = coalesce($2, project_settings.auto_from_request),
		  programs_enabled = coalesce($3, project_settings.programs_enabled),
		  progress_method = coalesce($4, project_settings.progress_method),
		  tolerance_days = coalesce($6, project_settings.tolerance_days),
		  tolerance_budget_pct = coalesce($7, project_settings.tolerance_budget_pct),
		  updated_by = $5, updated_at = now()`,
		in.DefaultTemplate, in.AutoFromRequest, in.ProgramsEnabled, in.ProgressMethod, nullable(actor.MemberID),
		in.ToleranceDays, in.ToleranceBudgetPct); err != nil {
		return nil, err
	}
	if in.Roles != nil {
		if _, err := tx.Exec(ctx, "delete from project_roles"); err != nil {
			return nil, err
		}
		for i, r := range in.Roles {
			tag, err := tx.Exec(ctx, `insert into project_roles (tenant_id, role_id, required, inherit_from_request, ordinal)
				select app_tenant(), id, $2, $3, $4 from stakeholder_roles where key = $1 on conflict do nothing`,
				r.Key, r.Required, r.InheritFromRequest, i)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				return nil, engine.InvalidParams("validation.role_not_found", "roles", map[string]any{"role": r.Key}, "ruolo %q inesistente", r.Key)
			}
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "projects.settings", EntityType: "project_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: toMap(in)}); err != nil {
		return nil, err
	}
	return s.SettingsFor(ctx, tx, actor)
}

// toMap trasforma un input nel contenuto dell'evento di audit.
func toMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
