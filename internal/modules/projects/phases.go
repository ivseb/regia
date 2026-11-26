package projects

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// PhaseInput crea o modifica una fase del piano.
type PhaseInput struct {
	Name        *string `json:"name"`
	Ordinal     *int    `json:"ordinal"`
	GateEnabled *bool   `json:"gate_enabled"`
}

func (s *Service) planProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	p, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, p.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	return p, nil
}

func phaseName(n *string) (string, error) {
	if n == nil {
		return "", engine.Required("name", "il nome della fase è obbligatorio")
	}
	v := strings.TrimSpace(*n)
	if v == "" {
		return "", engine.Required("name", "il nome della fase è obbligatorio")
	}
	if len(v) > 200 {
		return "", engine.TooLong("name", 200, "nome troppo lungo (massimo 200 caratteri)")
	}
	return v, nil
}

// CreatePhase aggiunge una fase in fondo al piano (o nella posizione indicata).
func (s *Service) CreatePhase(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in PhaseInput) (*Phase, error) {
	p, err := s.planProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	name, err := phaseName(in.Name)
	if err != nil {
		return nil, err
	}
	var ord int
	if in.Ordinal != nil {
		ord = *in.Ordinal
		if _, err := tx.Exec(ctx, "update plan_phases set ordinal = ordinal + 1 where project_id = $1 and ordinal >= $2", p.ID, ord); err != nil {
			return nil, err
		}
	} else if err := tx.QueryRow(ctx, "select coalesce(max(ordinal) + 1, 0) from plan_phases where project_id = $1", p.ID).Scan(&ord); err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into plan_phases (tenant_id, project_id, name, ordinal, gate_enabled) values (app_tenant(), $1, $2, $3, $4) returning id::text`,
		p.ID, name, ord, in.GateEnabled != nil && *in.GateEnabled).Scan(&id); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, p.ID, actor.MemberID, "plan", "phase", map[string]any{"phase": name}, nil); err != nil {
		return nil, err
	}
	return s.phase(ctx, tx, p, id)
}

func (s *Service) phase(ctx context.Context, tx pgx.Tx, proj *engine.Element, id string) (*Phase, error) {
	pl, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	for _, ph := range pl.Phases {
		if ph.ID == id {
			return &ph, nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Service) phaseProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, phaseID string) (*engine.Element, error) {
	var projectID string
	if err := tx.QueryRow(ctx, "select project_id::text from plan_phases where id::text = $1", phaseID).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	return s.planProject(ctx, tx, actor, projectID)
}

// UpdatePhase rinomina, sposta o accende/spegne il cancello di una fase.
func (s *Service) UpdatePhase(ctx context.Context, tx pgx.Tx, actor engine.Actor, phaseID string, in PhaseInput) (*Phase, error) {
	p, err := s.phaseProject(ctx, tx, actor, phaseID)
	if err != nil {
		return nil, err
	}
	var name *string
	if in.Name != nil {
		v, err := phaseName(in.Name)
		if err != nil {
			return nil, err
		}
		name = &v
	}
	if in.Ordinal != nil {
		// Spostamento: la fase prende la posizione, le altre scorrono.
		if _, err := tx.Exec(ctx, `with cur as (select ordinal o from plan_phases where id::text = $1)
			update plan_phases set ordinal = case
			  when id::text = $1 then $3
			  when ordinal > (select o from cur) and ordinal <= $3 then ordinal - 1
			  when ordinal < (select o from cur) and ordinal >= $3 then ordinal + 1
			  else ordinal end
			where project_id = $2`, phaseID, p.ID, *in.Ordinal); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `update plan_phases set name = coalesce($2, name), gate_enabled = coalesce($3, gate_enabled),
		gate_status = case when $3 = false and gate_status = 'pending' then 'open' else gate_status end
		where id::text = $1`, phaseID, name, in.GateEnabled); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.phase.update", EntityType: "plan_phase", EntityID: phaseID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: toMap(in)}); err != nil {
		return nil, err
	}
	return s.phase(ctx, tx, p, phaseID)
}

// DeletePhase toglie la fase: le sue voci restano nel piano, senza fase.
func (s *Service) DeletePhase(ctx context.Context, tx pgx.Tx, actor engine.Actor, phaseID string) error {
	p, err := s.phaseProject(ctx, tx, actor, phaseID)
	if err != nil {
		return err
	}
	var name string
	if err := tx.QueryRow(ctx, "delete from plan_phases where id::text = $1 returning name", phaseID).Scan(&name); err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, p.ID, actor.MemberID, "plan", "phase_removed", map[string]any{"phase": name}, nil)
}

// RequestGate chiede l'approvazione del cancello della fase nel motore delle approvazioni
// (politica «Cancello di fase» del tipo project; senza politica decide lo sponsor del progetto).
func (s *Service) RequestGate(ctx context.Context, tx pgx.Tx, actor engine.Actor, phaseID string) (*Phase, error) {
	p, err := s.phaseProject(ctx, tx, actor, phaseID)
	if err != nil {
		return nil, err
	}
	if s.ap == nil {
		return nil, engine.InvalidCode("validation.projects.no_approvals", "gate", "il motore delle approvazioni non è disponibile")
	}
	var name, status string
	var enabled bool
	if err := tx.QueryRow(ctx, "select name, gate_status, gate_enabled from plan_phases where id::text = $1 for update", phaseID).Scan(&name, &status, &enabled); err != nil {
		return nil, err
	}
	if !enabled {
		return nil, engine.InvalidCode("validation.projects.gate_off", "gate", "la fase non ha un cancello di approvazione")
	}
	if status == "pending" || status == "approved" {
		return nil, engine.InvalidParams("validation.projects.gate_status", "gate", map[string]any{"status": status}, "il cancello è già %s", map[string]string{"pending": "in approvazione", "approved": "approvato"}[status])
	}
	label := GatePolicyStage + ": " + name
	reqID, err := s.ap.OpenSubject(ctx, tx, actor, p, SubjectPhaseGate, GatePolicyStage, label, 0)
	if err != nil {
		return nil, err
	}
	if reqID == "" {
		return nil, engine.InvalidCode("validation.projects.gate_no_policy", "gate", "nessuna politica di approvazione per i cancelli di fase: configurala in Approvazioni")
	}
	if _, err := tx.Exec(ctx, "update plan_phases set gate_status = 'pending', gate_request_id = $2 where id::text = $1", phaseID, reqID); err != nil {
		return nil, err
	}
	return s.phase(ctx, tx, p, phaseID)
}

// gateClosed applica l'esito dell'approvazione del cancello: approvato apre la fase
// successiva, respinto la blocca, rimandato torna da chiedere.
func (s *Service) gateClosed(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	status := map[string]string{"approved": "approved", "approved_with_conditions": "approved", "rejected": "rejected", "deferred": "open"}[outcome]
	if status == "" {
		status = "open"
	}
	var projectID, name string
	err := tx.QueryRow(ctx, `update plan_phases set gate_status = $2, closed_at = case when $2 = 'approved' then now() end
		where gate_request_id = $1 returning project_id::text, name`, requestID, status).Scan(&projectID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, projectID, actor.MemberID, "plan", "gate", map[string]any{"phase": name, "outcome": outcome, "comment": comment}, nil)
}
