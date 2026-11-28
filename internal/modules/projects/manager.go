package projects

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// ManagerRole è il ruolo del responsabile per il tipo: project manager sui progetti, program
// manager sui programmi; vuoto per gli altri tipi.
func ManagerRole(typeKey string) string {
	switch typeKey {
	case TypeProject:
		return "project_manager"
	case TypeProgram:
		return RoleProgramManager
	}
	return ""
}

// IsPlanItem dice se l'elemento è una voce di piano di un progetto (work package, attività,
// milestone): l'assegnatario si cambia con UpdateItem.
func (s *Service) IsPlanItem(ctx context.Context, tx pgx.Tx, elementID string) (bool, error) {
	_, _, err := itemProject(ctx, tx, elementID)
	if errors.Is(err, engine.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CanAssignItem dice se l'attore può cambiare l'assegnatario della voce di piano: chi
// pianifica il progetto, come in UpdateItem.
func (s *Service) CanAssignItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) (bool, error) {
	projectID, _, err := itemProject(ctx, tx, elementID)
	if err != nil {
		return false, err
	}
	return canPlan(ctx, tx, actor, projectID)
}

// CanSetManager dice se l'attore può cambiare il responsabile: chi pianifica il progetto
// (element.write, project manager o PMO) o chi modifica il programma.
func (s *Service) CanSetManager(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element) (bool, error) {
	switch el.TypeKey {
	case TypeProject:
		return canPlan(ctx, tx, actor, el.ID)
	case TypeProgram:
		return canEditProgram(ctx, tx, actor, el.ID)
	}
	return false, nil
}

// SetManager sostituisce il responsabile del progetto o del programma con un membro attivo.
// Version 0 = nessun controllo della versione.
func (s *Service) SetManager(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, memberID string, version int) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	role := ManagerRole(el.TypeKey)
	if role == "" {
		return nil, engine.InvalidCode("validation.projects.manager_type", "element", "il responsabile si cambia solo su progetti e programmi")
	}
	if version != 0 && version != el.Version {
		return nil, engine.ErrConflict
	}
	if ok, err := s.CanSetManager(ctx, tx, actor, el); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if memberID == "" {
		return nil, engine.InvalidCode("validation.projects.manager_required", "member_id", "il responsabile è obbligatorio")
	}
	if err := engine.SetStakeholder(ctx, tx, actor, el.ID, role, memberID, "", true); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "projects.manager", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"role": role, "member_id": memberID}}); err != nil {
		return nil, err
	}
	return el, nil
}
