package devsecops

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Configurazione: tutto ciò che serve a scegliere nell'editor della politica.

// VulnConfig è la configurazione con le scelte disponibili.
type VulnConfig struct {
	Policy          VulnPolicy       `json:"policy"`
	ExceptionPolicy *ExceptionPolicy `json:"exception_policy"`
	Teams           []IDName         `json:"teams"`
	Groups          []IDName         `json:"groups"`
	Members         []IDName         `json:"members"`
	Roles           []IDName         `json:"roles"`
	Bodies          []IDName         `json:"bodies"`
	Fields          []IDName         `json:"fields"`
	CanEdit         bool             `json:"can_edit"`
}

// VulnConfig legge la configurazione.
func (s *Service) VulnConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*VulnConfig, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	c := &VulnConfig{CanEdit: CanConfigure(actor)}
	var err error
	if c.Policy, err = loadVulnPolicy(ctx, tx); err != nil {
		return nil, err
	}
	if c.ExceptionPolicy, err = s.GetExceptionPolicy(ctx, tx); err != nil {
		return nil, err
	}
	list := func(q string) ([]IDName, error) {
		rows, err := tx.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		out := []IDName{}
		for rows.Next() {
			var x IDName
			if err := rows.Scan(&x.ID, &x.Name); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, x)
		}
		rows.Close()
		return out, rows.Err()
	}
	if c.Teams, err = list("select id::text, name from teams where not archived order by name"); err != nil {
		return nil, err
	}
	if c.Groups, err = list("select id::text, name from groups order by name"); err != nil {
		return nil, err
	}
	if c.Members, err = list("select id::text, display_name from members where status = 'active' order by display_name limit 500"); err != nil {
		return nil, err
	}
	if c.Roles, err = list("select key, name from stakeholder_roles order by name"); err != nil {
		return nil, err
	}
	if c.Bodies, err = list("select distinct on (kind) kind, name from governance_bodies where not archived order by kind, name"); err != nil {
		return nil, err
	}
	if c.Fields, err = list("select key, name from field_defs where data_type in ('boolean','string','enum','text') order by name"); err != nil {
		return nil, err
	}
	return c, nil
}

// RiskChoices elenca i rischi del registro a cui collegare un'eccezione.
func (s *Service) RiskChoices(ctx context.Context, tx pgx.Tx, q string) ([]IDName, error) {
	rows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title from elements e
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'risk'
		where e.deleted_at is null and e.number is not null and ($1 = '' or lower(e.title) like '%' || lower($1) || '%' or sp.key || '-' || e.number = upper($1))
		order by e.number desc limit 100`, strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	out := []IDName{}
	for rows.Next() {
		var x IDName
		if err := rows.Scan(&x.ID, &x.Key, &x.Name); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	rows.Close()
	return out, rows.Err()
}
