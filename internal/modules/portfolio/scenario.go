package portfolio

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Scenario è una linea di taglio della graduatoria con un nome (migrazione 0059): quante richieste
// stanno sopra la linea e con quale budget. Si confronta con un altro nella cabina di portafoglio.
type Scenario struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Cut         int       `json:"cut"`
	BudgetCents *int64    `json:"budget_cents"`
	ItemKeys    []string  `json:"item_keys"`
	CreatedBy   *string   `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// ScenarioInput è lo scenario da salvare.
type ScenarioInput struct {
	Name        string   `json:"name"`
	Cut         int      `json:"cut"`
	BudgetCents *int64   `json:"budget_cents"`
	ItemKeys    []string `json:"item_keys"`
}

const maxScenarios = 20

// Scenarios elenca gli scenari salvati, i più recenti per ultimi.
func (s *Service) Scenarios(ctx context.Context, tx pgx.Tx) ([]Scenario, error) {
	rows, err := tx.Query(ctx, `select p.id::text, p.name, p.cut, p.budget_cents, p.item_keys, m.display_name, p.created_at
		from portfolio_scenarios p left join members m on m.tenant_id = p.tenant_id and m.id = p.created_by
		order by p.created_at, p.id`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Scenario])
	if out == nil {
		out = []Scenario{}
	}
	return out, err
}

// SaveScenario salva uno scenario (permesso portfolio.admin). Un nome già usato lo sostituisce.
func (s *Service) SaveScenario(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ScenarioInput) (*Scenario, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, engine.InvalidCode("validation.portfolio.scenario_name_required", "name", "serve un nome per lo scenario")
	}
	if utf8.RuneCountInString(in.Name) > 80 {
		return nil, engine.TooLong("name", 80, "nome troppo lungo (massimo 80 caratteri)")
	}
	if in.Cut < 0 || in.Cut > rankLimit {
		return nil, engine.OutOfRange("cut", 0, rankLimit, "linea di taglio fuori dalla graduatoria")
	}
	if in.BudgetCents != nil && *in.BudgetCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "budget_cents", "il budget non può essere negativo")
	}
	if in.ItemKeys == nil {
		in.ItemKeys = []string{}
	}
	var n int
	var exists bool
	if err := tx.QueryRow(ctx, `select count(*), coalesce(bool_or(lower(name) = lower($1)), false) from portfolio_scenarios`, in.Name).Scan(&n, &exists); err != nil {
		return nil, err
	}
	if !exists && n >= maxScenarios {
		return nil, engine.InvalidParams("validation.portfolio.scenario_limit", "name", map[string]any{"max": maxScenarios}, "al massimo %d scenari: eliminane uno", maxScenarios)
	}
	var kept string
	err := tx.QueryRow(ctx, `insert into portfolio_scenarios (tenant_id, id, name, cut, budget_cents, item_keys, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)
		on conflict (tenant_id, lower(name)) do update set name = excluded.name, cut = excluded.cut, budget_cents = excluded.budget_cents,
			item_keys = excluded.item_keys, created_by = excluded.created_by, created_at = now()
		returning id::text`, db.NewID(), in.Name, in.Cut, in.BudgetCents, in.ItemKeys, nullable(actor.MemberID)).Scan(&kept)
	if err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "portfolio.scenario.save", EntityType: "portfolio_scenario", EntityID: kept,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"name": in.Name, "cut": in.Cut, "budget_cents": in.BudgetCents}}); err != nil {
		return nil, err
	}
	list, err := s.Scenarios(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == kept {
			return &list[i], nil
		}
	}
	return nil, errors.New("scenario non trovato dopo il salvataggio")
}

// DeleteScenario elimina uno scenario (permesso portfolio.admin).
func (s *Service) DeleteScenario(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `delete from portfolio_scenarios where id::text = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audit.Append(ctx, tx, audit.Event{Action: "portfolio.scenario.delete", EntityType: "portfolio_scenario", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}
