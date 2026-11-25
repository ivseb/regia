package engine

import "testing"

func TestConditions(t *testing.T) {
	f := Facts{}
	f.Set("stima_max", 120000.0)
	f.Set("driver", "regulatory", "normativo")
	f.Set("obbligatoria", true)
	f.Set("stato", "Valutazione")
	f.Set("scadenza_normativa", "2026-12-31")
	f.Set("ruolo.sponsor", "Sara Sponsor")
	f.Set("unita", "Area Clienti")

	cases := []struct {
		src  string
		want bool
	}{
		{"", true},
		{"stima_max: >50000", true},
		{"stima_max: > 50000", true},
		{"stima_max: >=120000", true},
		{"stima_max: <100k", false},
		{"stima_max: presente", true},
		{"stima_min: assente", true},
		{"stima_min: presente", false},
		{"driver: normativo", true},
		{"driver: Strategic, normativo", true},
		{"driver: strategic", false},
		{"-driver: strategic", true},
		{"non driver: normativo", false},
		{"obbligatoria: sì", true},
		{"obbligatoria: no", false},
		{"stato: Valutazione ruolo.sponsor: presente", true},
		{"stato: Triage ruolo.sponsor: presente", false},
		{"stato: Triage oppure ruolo.sponsor: presente", true},
		{"stato: Triage or stima_max: <10", false},
		{"scadenza_normativa: <2027-01-01", true},
		{"scadenza_normativa: >2027-01-01", false},
		{`unita: "Area Clienti"`, true},
		{`unita: "area clienti", Altro`, true},
		{"stima_max: 120.000", true},
		{"stima_max: 120000 e driver: normativo", true},
	}
	for _, c := range cases {
		cond, err := ParseCondition(c.src)
		if err != nil {
			t.Errorf("%q: errore %v", c.src, err)
			continue
		}
		if got := cond.Eval(f); got != c.want {
			t.Errorf("%q = %v, atteso %v", c.src, got, c.want)
		}
	}

	for _, bad := range []string{"stima_max", "stima_max:", "oppure stato: X", "stato: X oppure", `stato: "aperta`, "stima_max: >abc", "non", "stato: X Y"} {
		if _, err := ParseCondition(bad); err == nil {
			t.Errorf("%q: atteso un errore", bad)
		}
	}

	if keys := MustCondition("stato: A ruolo.sponsor: presente oppure stato: B").Keys(); len(keys) != 2 {
		t.Errorf("chiavi: %v", keys)
	}
}
