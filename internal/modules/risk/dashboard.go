package risk

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Dashboard è il cruscotto "Rischi e incidenti": heatmap della matrice predefinita, rischi
// oltre la soglia, fornitori critici e contratti in scadenza, incidenti aperti con le
// notifiche normative in scadenza.
type Dashboard struct {
	Heatmap       *Heatmap        `json:"heatmap"`
	OverThreshold []RiskItem      `json:"over_threshold"`
	Overdue       []RiskItem      `json:"overdue"`
	OpenRisks     int             `json:"open_risks"`
	Critical      []Provider      `json:"critical_providers"`
	Providers     int             `json:"providers"`
	Expiring      []Contract      `json:"expiring_contracts"`
	Incidents     []IncidentItem  `json:"open_incidents"`
	Notifications []Notification  `json:"open_notifications"`
	Settings      *Settings       `json:"settings"`
	Can           map[string]bool `json:"can"`
}

// DashboardView calcola il cruscotto (element.read).
func (s *Service) DashboardView(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Dashboard, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	d := &Dashboard{OverThreshold: []RiskItem{}, Overdue: []RiskItem{}, Can: map[string]bool{
		"risk": actor.Has(PermAdmin), "incident": canReport(actor), "manage_incidents": actor.Has(PermIncident)}}
	var err error
	if d.Settings, err = loadSettings(ctx, tx); err != nil {
		return nil, err
	}
	ms, err := s.Matrices(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(ms) > 0 {
		if d.Heatmap, err = s.HeatmapOf(ctx, tx, ms[0]); err != nil {
			return nil, err
		}
	}
	reg, err := s.Risks(ctx, tx, actor, RiskFilter{Phase: "aperti"})
	if err != nil {
		return nil, err
	}
	d.OpenRisks = len(reg.Items)
	for _, it := range reg.Items {
		if it.Residual != nil && it.Residual.Over {
			d.OverThreshold = append(d.OverThreshold, it)
		} else if it.Residual == nil && it.Inherent != nil && it.Inherent.Over {
			// Senza residuo vale l'inerente: un rischio alto non ancora trattato è oltre la soglia.
			d.OverThreshold = append(d.OverThreshold, it)
		}
		if it.Overdue {
			d.Overdue = append(d.Overdue, it)
		}
	}
	provs, err := s.Providers(ctx, tx, actor, "", false)
	if err != nil {
		return nil, err
	}
	d.Providers = len(provs)
	d.Critical = []Provider{}
	for _, p := range provs {
		if p.Critical {
			d.Critical = append(d.Critical, p)
		}
	}
	if d.Expiring, err = s.Contracts(ctx, tx, actor, "", true); err != nil {
		return nil, err
	}
	if d.Incidents, err = s.Incidents(ctx, tx, actor, "", true, false); err != nil {
		return nil, err
	}
	if d.Notifications, err = s.OpenNotifications(ctx, tx); err != nil {
		return nil, err
	}
	return d, nil
}
