package projects

import "testing"

// Avanzamento dai SAL accettati: la percentuale dichiarata, oppure gli importi accettati fin lì
// sul valore del contratto; vale l'ultimo SAL che porta un valore.
func TestClaimProgress(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	i := func(v int64) *int64 { return &v }
	contract := i(400_000)
	cases := []struct {
		name     string
		claims   []claimRow
		contract *int64
		want     *float64
	}{
		{"nessun SAL", nil, contract, nil},
		{"percentuale", []claimRow{{Percent: f(25)}, {Percent: f(60)}}, contract, f(60)},
		{"importi sul contratto", []claimRow{{Amount: i(100_000)}, {Amount: i(140_000)}}, contract, f(60)},
		{"misti: l'importo somma anche i SAL a percentuale", []claimRow{{Percent: f(25), Amount: i(100_000)}, {Amount: i(100_000)}}, contract, f(50)},
		{"oltre il contratto si ferma a 100", []claimRow{{Amount: i(500_000)}}, contract, f(100)},
		{"importi senza contratto: nessun valore", []claimRow{{Amount: i(100_000)}}, nil, nil},
		{"l'ultimo senza valore lascia il precedente", []claimRow{{Percent: f(30)}, {Amount: i(10)}}, nil, f(30)},
	}
	for _, c := range cases {
		got := claimProgress(c.claims, c.contract)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%s: %v, atteso %v", c.name, deref64(got), deref64(c.want))
		}
	}
}
