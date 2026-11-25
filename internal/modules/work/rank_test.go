package work

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// Le chiavi frazionarie stanno sempre strettamente tra i vicini e non finiscono con "0".
func TestRankBetween(t *testing.T) {
	check := func(a, b, got string) {
		t.Helper()
		if got <= a || (b != "" && got >= b) || strings.HasSuffix(got, "0") {
			t.Fatalf("rankBetween(%q, %q) = %q", a, b, got)
		}
	}
	for _, c := range [][2]string{{"", ""}, {"", "i"}, {"i", ""}, {"a", "b"}, {"a", "a1"}, {"az", "b"}, {"zz", ""}, {"", "01"}, {"i", "i01"}} {
		check(c[0], c[1], rankBetween(c[0], c[1]))
	}
	// Inserimenti casuali in una lista: l'ordine delle chiavi resta quello della lista.
	r := rand.New(rand.NewPCG(1, 2))
	keys := []string{rankBetween("", "")}
	for range 2000 {
		i := r.IntN(len(keys) + 1)
		a, b := "", ""
		if i > 0 {
			a = keys[i-1]
		}
		if i < len(keys) {
			b = keys[i]
		}
		k := rankBetween(a, b)
		check(a, b, k)
		keys = slices.Insert(keys, i, k)
	}
	if !slices.IsSorted(keys) {
		t.Fatal("chiavi non ordinate")
	}
	// La ridistribuzione genera chiavi ordinate, tutte dopo il prefisso.
	spread := spreadRanks("m", 5000)
	if !slices.IsSorted(spread) || spread[0] <= "m" || len(spread[0]) > 6 {
		t.Fatalf("spreadRanks: %q … %q", spread[0], spread[len(spread)-1])
	}
	if got := ProposeKey("Integrazioni"); got != "INT" {
		t.Fatalf("ProposeKey: %s", got)
	}
}
