package reviews

import (
	"slices"
	"testing"
)

// Le domande da guardare con attenzione: quelle che cambiano il punteggio (peso > 0 e punti) e
// quelle usate dalle regole di derivazione.
func TestHighlights(t *testing.T) {
	d := scoredDef()
	d.Sections[0].Questions = append(d.Sections[0].Questions, Question{Key: "zero", Label: "Zero", Type: QYesNo, YesScore: f(5), Weight: f(0)})
	d.Derivations = []Derivation{{Key: "log", When: "note: presente", Text: "Scrivere i log"}}
	h := d.Highlights()
	for _, k := range []string{"mfa", "cifratura", "maturita", "controlli", "extra"} {
		if !slices.Contains(h[k], "score") {
			t.Errorf("%s: %v", k, h[k])
		}
	}
	if !slices.Equal(h["note"], []string{"rules"}) || h["zero"] != nil {
		t.Fatalf("evidenze: %v", h)
	}
}
