package reviews

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Trigger è una regola di processo che apre un tipo di valutazione: «Quando si apre».
type Trigger struct {
	ReviewType  string `json:"review_type"`           // chiave del tipo di valutazione (ref dell'azione)
	ProcessKey  string `json:"process_key"`           // processo che contiene la regola
	ProcessName string `json:"process_name"`          // nome del processo
	ItemType    string `json:"item_type"`             // nome del tipo di elemento del processo
	RuleKey     string `json:"rule_key"`              // regola
	RuleName    string `json:"rule_name"`             // nome della regola
	On          string `json:"on"`                    // evento: create, enter:<fase>, exit:<fase>
	When        string `json:"when,omitempty"`        // condizione della regola
	ActionWhen  string `json:"action_when,omitempty"` // condizione propria dell'azione
}

// Triggers elenca le regole dei processi pubblicati (non archiviati) con un'azione open_review,
// per mostrare nella scheda del tipo di valutazione quando si apre.
func (s *Service) Triggers(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Trigger, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select p.key, p.name, et.name, v.definition
		from process_definitions p
		join process_versions v on v.process_id = p.id and v.status = 'published'
		join element_types et on et.id = p.type_id
		where not p.archived order by p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		var pk, pn, tn string
		var raw []byte
		if err := rows.Scan(&pk, &pn, &tn, &raw); err != nil {
			return nil, err
		}
		var def struct {
			Rules []engine.Rule `json:"rules"`
		}
		if json.Unmarshal(raw, &def) != nil {
			continue
		}
		for _, r := range def.Rules {
			for _, a := range r.Actions {
				if a.Kind == ActionOpen && a.Ref != "" {
					out = append(out, Trigger{ReviewType: a.Ref, ProcessKey: pk, ProcessName: pn, ItemType: tn,
						RuleKey: r.Key, RuleName: r.Name, On: r.On, When: r.When, ActionWhen: a.When})
				}
			}
		}
	}
	return out, rows.Err()
}
