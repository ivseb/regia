package privacy

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// breachSoonHours: una violazione da notificare con meno di queste ore alla scadenza è «vicina».
const breachSoonHours = 24

// HomeCounts sono i numeri della home del DPO.
type HomeCounts struct {
	Treatments    int `json:"treatments"`
	NoBasis       int `json:"no_basis"`
	DPIARequired  int `json:"dpia_required"`
	DPIAMissing   int `json:"dpia_missing"`
	DPIAInReview  int `json:"dpia_in_review"`
	DPIAApproved  int `json:"dpia_approved"`
	OpenBreaches  int `json:"open_breaches"`
	SoonBreaches  int `json:"soon_breaches"`
	UnlinkedApps  int `json:"unlinked_apps"`
	TransfersNoEU int `json:"transfers_extra_eu"`
}

// Home è la home del DPO: ciò che chiede attenzione, con gli elementi (non solo i numeri).
type Home struct {
	Counts      HomeCounts  `json:"counts"`
	NoBasis     []Treatment `json:"no_basis"`
	DPIAMissing []Treatment `json:"dpia_missing"`
	InReview    []DPIAItem  `json:"in_review"`
	Breaches    []Breach    `json:"breaches"`
	Unlinked    []AppRef    `json:"unlinked"`
}

// Home compone la home del DPO (privacy.read).
func (s *Service) Home(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Home, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	h := &Home{NoBasis: []Treatment{}, DPIAMissing: []Treatment{}, InReview: []DPIAItem{}, Breaches: []Breach{}}
	ts, err := s.Treatments(ctx, tx, actor, false)
	if err != nil {
		return nil, err
	}
	h.Counts.Treatments = len(ts)
	for _, t := range ts {
		if t.LegalBasis == "" {
			h.Counts.NoBasis++
			h.NoBasis = append(h.NoBasis, t)
		}
		if t.DPIARequired {
			h.Counts.DPIARequired++
			if t.DPIA == nil {
				h.Counts.DPIAMissing++
				h.DPIAMissing = append(h.DPIAMissing, t)
			}
		}
		if t.TransfersExtraEU {
			h.Counts.TransfersNoEU++
		}
	}
	ds, err := s.DPIAs(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		switch d.Status {
		case "in_review":
			h.Counts.DPIAInReview++
			h.InReview = append(h.InReview, d)
		case "approved":
			h.Counts.DPIAApproved++
		}
	}
	bs, err := s.Breaches(ctx, tx, actor, false)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if b.Status != "open" && b.Status != "overdue" {
			continue
		}
		h.Counts.OpenBreaches++
		if b.HoursLeft < breachSoonHours {
			h.Counts.SoonBreaches++
		}
		if len(h.Breaches) < 10 {
			h.Breaches = append(h.Breaches, b)
		}
	}
	apps, linked, err := catalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	un := unlinkedOf(apps, linked)
	h.Counts.UnlinkedApps = len(un)
	if len(un) > 10 {
		un = un[:10]
	}
	h.Unlinked = un
	return h, nil
}
