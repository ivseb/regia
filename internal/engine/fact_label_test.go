package engine

import "testing"

func TestFactLabel(t *testing.T) {
	for in, want := range map[string]string{
		"figli_aperti":                    "Figli aperti",
		"valutazioni_obbligatorie_aperte": "Valutazioni obbligatorie aperte",
		"stima_max":                       "Stima max",
	} {
		if got := factLabel(in); got != want {
			t.Errorf("factLabel(%q) = %q, voglio %q", in, got, want)
		}
	}
}
