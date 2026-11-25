package grc

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// AuditHome è la pagina iniziale dell'auditor: gli incarichi, quello corrente con i controlli,
// i rilievi e le richieste aperte.
type AuditHome struct {
	Engagements []Engagement      `json:"engagements"`
	Current     *EngagementDetail `json:"current"`
	Findings    []Finding         `json:"findings"`
	Requests    []Request         `json:"requests"`
	Mine        []Request         `json:"mine"`
	CanAudit    bool              `json:"can_audit"`
}

// Home compone la pagina iniziale; engagement vuoto = il primo incarico (aperti per primi).
func (s *Service) Home(ctx context.Context, tx pgx.Tx, actor engine.Actor, engagement string) (*AuditHome, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	h := &AuditHome{CanAudit: actor.Has(PermAudit), Findings: []Finding{}, Requests: []Request{}}
	var err error
	if h.Engagements, err = s.Engagements(ctx, tx, actor); err != nil {
		return nil, err
	}
	cur := engagement
	if cur == "" && len(h.Engagements) > 0 {
		cur = h.Engagements[0].ID
	}
	if cur != "" {
		if h.Current, err = s.EngagementDetail(ctx, tx, actor, cur); err != nil {
			return nil, err
		}
		if h.Findings, err = s.Findings(ctx, tx, actor, cur); err != nil {
			return nil, err
		}
		if h.Requests, err = s.EngagementRequests(ctx, tx, actor, cur); err != nil {
			return nil, err
		}
	}
	if h.Mine, err = s.MyRequests(ctx, tx, actor); err != nil {
		return nil, err
	}
	return h, nil
}
