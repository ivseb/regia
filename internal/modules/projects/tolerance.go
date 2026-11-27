package projects

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Tolleranze del singolo progetto (progetti.md §8, passo 5). Il tenant ha le sue in
// Configurazione → Progetti; un progetto può averne di proprie, per esempio più strette per un
// progetto normativo. Le cambia chi governa il progetto, non chi lo pianifica: il PMO e lo sponsor
// del progetto, il governo del portafoglio e l'amministratore. Il PM le vede ma non le allarga da sé.

// toleranceRoles cambiano le tolleranze del progetto.
var toleranceRoles = []string{"pmo", "sponsor"}

func canEditTolerance(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) (bool, error) {
	if actor.Has("tenant.admin") || actor.Has("portfolio.admin") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, projectID, actor.MemberID, toleranceRoles)
}

// ToleranceInput imposta le tolleranze del progetto: un valore nullo torna a quella del tenant.
type ToleranceInput struct {
	Days      *int     `json:"days"`
	BudgetPct *float64 `json:"budget_pct"`
}

// SetTolerance salva le tolleranze del progetto e ricontrolla il piano: con tolleranze più strette
// la change request può nascere subito.
func (s *Service) SetTolerance(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ToleranceInput) (*Plan, error) {
	proj, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canEditTolerance(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if in.Days != nil && (*in.Days < 0 || *in.Days > 365) {
		return nil, engine.InvalidCode("validation.projects.tolerance_days", "days", "la tolleranza va da 0 a 365 giorni lavorativi")
	}
	if in.BudgetPct != nil && (math.IsNaN(*in.BudgetPct) || *in.BudgetPct < 0 || *in.BudgetPct > 100) {
		return nil, engine.InvalidCode("validation.projects.tolerance_budget", "budget_pct", "la tolleranza sul budget va da 0 a 100%%")
	}
	tag, err := tx.Exec(ctx, "update ext_project set tolerance_days = $2, tolerance_budget_pct = $3 where element_id = $1", proj.ID, in.Days, in.BudgetPct)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into ext_project (tenant_id, element_id, tolerance_days, tolerance_budget_pct)
			values (app_tenant(), $1, $2, $3)`, proj.ID, in.Days, in.BudgetPct); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "plan", "tolerance", toMap(in), nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.tolerance", EntityType: TypeProject, EntityID: proj.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: toMap(in)}); err != nil {
		return nil, err
	}
	if err := s.checkTolerances(ctx, tx, actor, proj.ID); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, tx, actor, proj.ID)
}
