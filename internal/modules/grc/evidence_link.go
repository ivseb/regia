package grc

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Collegamento di un'evidenza già esistente a un altro controllo (ondata 5 dell'AI: le proposte
// dell'assistente accettate dalla persona; vale anche a mano). Chi può: grc.admin, oppure il
// responsabile del controllo a cui si collega, come per la creazione di un'evidenza. La firma
// dell'evidenza non cambia (non comprende i controlli): l'evidenza vale anche per il nuovo
// controllo, e il grado di copertura dichiarato resta come informazione per chi verifica.

// LinkInput collega un'evidenza a un controllo.
type LinkInput struct {
	Evidence string `json:"evidence"`
	Control  string `json:"control"`
	// Coverage: full | partial (vuoto = non dichiarata).
	Coverage string `json:"coverage"`
	Note     string `json:"note"`
	// Via: manual | ai (proposta dell'assistente accettata dalla persona).
	Via string `json:"via"`
}

// LinkResult è l'esito del collegamento.
type LinkResult struct {
	EvidenceKey string `json:"evidence"`
	ControlKey  string `json:"control"`
	// Already: il collegamento c'era già (il grado e la nota si aggiornano).
	Already bool `json:"already"`
}

// LinkEvidence collega un'evidenza esistente a un controllo.
func (s *Service) LinkEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, in LinkInput) (*LinkResult, error) {
	evID, err := s.evidenceID(ctx, tx, strings.TrimSpace(in.Evidence))
	if err != nil {
		return nil, engine.NotFound("evidence", "evidence", "evidenza %q inesistente", in.Evidence)
	}
	ctrlID, err := s.controlRef(ctx, tx, strings.TrimSpace(in.Control))
	if err != nil {
		return nil, engine.NotFound("control", "control", "controllo %q inesistente", in.Control)
	}
	if !actor.Has(PermAdmin) {
		ok, err := isOwnerOf(ctx, tx, actor, []string{ctrlID})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.ErrForbidden
		}
	}
	switch in.Coverage {
	case "", "full", "partial":
	default:
		return nil, engine.InvalidValue("coverage", "copertura non ammessa (full, partial)")
	}
	if in.Via == "" {
		in.Via = "manual"
	}
	if in.Via != "manual" && in.Via != "ai" {
		return nil, engine.InvalidValue("via", "origine non ammessa (manual, ai)")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	var status string
	if err := tx.QueryRow(ctx, "select status from ext_evidence where element_id = $1", evID).Scan(&status); err != nil {
		return nil, err
	}
	if status == "rejected" || status == "revoked" {
		return nil, engine.InvalidCode("validation.grc.evidence_not_linkable", "evidence", "un'evidenza respinta o revocata non si collega")
	}
	var existed bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from grc_evidence_controls where evidence_element_id = $1 and control_element_id = $2)`,
		evID, ctrlID).Scan(&existed); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into grc_evidence_controls (tenant_id, evidence_element_id, control_element_id, coverage, note, via, linked_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)
		on conflict (tenant_id, evidence_element_id, control_element_id) do update
		set coverage = excluded.coverage, note = excluded.note, via = excluded.via, linked_by = excluded.linked_by`,
		evID, ctrlID, nullable(in.Coverage), nullable(in.Note), in.Via, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	out := &LinkResult{Already: existed}
	if err := tx.QueryRow(ctx, `select (select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = $1),
		(select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = $2)`,
		evID, ctrlID).Scan(&out.EvidenceKey, &out.ControlKey); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, evID, actor.MemberID, "grc", "collegata", map[string]any{"control": out.ControlKey, "coverage": in.Coverage, "via": in.Via}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.evidence.link", EvidenceType, evID, map[string]any{"control": ctrlID, "coverage": in.Coverage,
		"via": in.Via, "already": existed}); err != nil {
		return nil, err
	}
	return out, nil
}

// LinkCoverage è il grado dichiarato di un collegamento evidenza → controllo.
type LinkCoverage struct {
	Coverage string `json:"coverage,omitempty"`
	Note     string `json:"note,omitempty"`
	Via      string `json:"via"`
}

// CanLink dice se l'attore può collegare evidenze a quel controllo (grc.admin o responsabile).
func (s *Service) CanLink(ctx context.Context, tx pgx.Tx, actor engine.Actor, controlID string) (bool, error) {
	if actor.Has(PermAdmin) {
		return true, nil
	}
	return isOwnerOf(ctx, tx, actor, []string{controlID})
}
