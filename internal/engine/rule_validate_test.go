package engine

import (
	"errors"
	"testing"
)

// Le regole non valide producono problemi con un code specifico, che la pubblicazione restituisce.
func TestRuleProblemCodes(t *testing.T) {
	w := &Workflow{
		Initial:     "Nuova",
		Phases:      []Phase{{Name: "Nuova", Category: "todo"}, {Name: "Fine", Category: "done"}},
		Transitions: []Transition{{From: []string{"Nuova"}, To: "Fine"}},
		Rules: []Rule{
			{Key: "Maiuscola", Name: "x", On: "create", Actions: []Action{{Kind: "assign_role", Role: "r", Group: "g"}}},
			{Key: "a", Name: "x", On: "enter:Inesistente", When: "((", Actions: []Action{{Kind: "assign_role"}}},
			{Key: "b", Name: "x", On: "boh"},
			{Key: "b", Name: "x", On: "create", Actions: []Action{{Kind: ""}}},
		},
	}
	got := map[string]bool{}
	for _, p := range Validate(w) {
		if p.Code != "" {
			got[p.Code] = true
		}
	}
	for _, c := range []string{"key_invalid", "phase_unknown", "condition_invalid", "role_required", "target_required", "event_invalid", "no_actions", "key_duplicate", "kind_required"} {
		if !got["validation.rule."+c] {
			t.Errorf("manca il code validation.rule.%s (trovati %v)", c, got)
		}
	}
	err := RuleCodeError(Validate(w))
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code == "" {
		t.Fatalf("errore senza code: %#v", err)
	}
}
