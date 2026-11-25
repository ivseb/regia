package demand

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Column raccoglie i dati della domanda che la lista delle richieste mostra come colonne
// (unità, sponsor, stima, scadenza normativa, punteggio, giorni nella fase corrente).
type Column struct {
	Unit               *string    `json:"unit"`
	Sponsor            *string    `json:"sponsor"`
	EstimateLowCents   *int64     `json:"estimate_low_cents"`
	EstimateHighCents  *int64     `json:"estimate_high_cents"`
	RegulatoryDeadline *time.Time `json:"regulatory_deadline"`
	IsMandatory        bool       `json:"is_mandatory"`
	Score              *float64   `json:"score"`
	DaysInState        int        `json:"days_in_state"`
}

// Columns restituisce le colonne della domanda per gli elementi indicati (per id). Gli
// elementi che non sono richieste non compaiono. Vale la visibilità della sessione (RLS).
func (s *Service) Columns(ctx context.Context, tx pgx.Tx, ids []string) (map[string]Column, error) {
	out := map[string]Column{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select d.element_id::text, u.name,
		       (select coalesce(m.display_name, g.name) from stakeholders sh
		          join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'sponsor'
		          left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		          left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		          where sh.element_id = d.element_id order by 1 limit 1),
		       d.estimate_low_cents, d.estimate_high_cents, d.regulatory_deadline, d.is_mandatory, d.score,
		       greatest(0, floor(extract(epoch from now() - coalesce(
		          (select max(a.created_at) from activities a where a.element_id = d.element_id and a.kind in ('state', 'created')), e.created_at)) / 86400))::int
		from ext_demand d
		join elements e on e.tenant_id = d.tenant_id and e.id = d.element_id
		left join org_units u on u.tenant_id = d.tenant_id and u.id = d.requesting_unit_id
		where d.element_id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c Column
		if err := rows.Scan(&id, &c.Unit, &c.Sponsor, &c.EstimateLowCents, &c.EstimateHighCents, &c.RegulatoryDeadline, &c.IsMandatory, &c.Score, &c.DaysInState); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}
