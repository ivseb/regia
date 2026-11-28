package auth

import (
	"slices"
	"strings"
	"testing"
)

func TestIdentityGroups(t *testing.T) {
	cases := []struct {
		name    string
		claims  map[string]any
		claim   string
		want    []string
		present bool
	}{
		{"elenco", map[string]any{"groups": []any{"a", " b ", "", 3}}, "groups", []string{"a", "b"}, true},
		{"stringa singola", map[string]any{"roles": "admin"}, "roles", []string{"admin"}, true},
		{"vuoto ma presente", map[string]any{"groups": []any{}}, "groups", nil, true},
		{"assente", map[string]any{"email": "x"}, "groups", nil, false},
		{"nullo", map[string]any{"groups": nil}, "groups", nil, false},
		{"overage di Entra", map[string]any{"_claim_names": map[string]any{"groups": "src1"}, "groups": []any{"a"}}, "groups", nil, false},
		{"tipo sbagliato", map[string]any{"groups": map[string]any{"a": 1}}, "groups", nil, false},
		{"valore troppo lungo", map[string]any{"groups": []any{strings.Repeat("x", 301), "ok"}}, "groups", []string{"ok"}, true},
	}
	for _, c := range cases {
		got, present := (&Identity{Claims: c.claims}).Groups(c.claim)
		if present != c.present || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, atteso %v %v", c.name, got, present, c.want, c.present)
		}
	}
	many := make([]any, maxClaimGroups+10)
	for i := range many {
		many[i] = "g"
	}
	if got, _ := (&Identity{Claims: map[string]any{"groups": many}}).Groups("groups"); len(got) != maxClaimGroups {
		t.Errorf("tetto dei gruppi: %d", len(got))
	}
	if _, present := (*Identity)(nil).Groups("groups"); present {
		t.Error("identità nulla")
	}
}
