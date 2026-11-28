package admin

import (
	"slices"
	"testing"
)

func TestMissingPermissions(t *testing.T) {
	cases := []struct {
		have, want, missing []string
	}{
		{[]string{"*"}, []string{"*", "audit.read"}, nil},
		{[]string{"tenant.admin", "element.read"}, []string{"*"}, []string{"*"}},
		{[]string{"tenant.admin", "element.read"}, []string{"element.read"}, nil},
		{[]string{"tenant.admin"}, []string{"finance.read", "audit.read", "audit.read"}, []string{"audit.read", "finance.read"}},
		{[]string{}, []string{}, nil},
	}
	for _, c := range cases {
		if got := missing(c.have, c.want); !slices.Equal(got, c.missing) {
			t.Errorf("missing(%v, %v) = %v, atteso %v", c.have, c.want, got, c.missing)
		}
	}
}
