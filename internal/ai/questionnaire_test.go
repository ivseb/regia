package ai

import (
	"testing"

	"regia/internal/modules/reviews"
)

// La confidenza la limita Regia: «alta» solo con la stessa domanda confermata sulla stessa
// applicazione con lo stesso valore, o con il fatto della precompilazione; altrimenti al massimo
// «media». Il modello può solo abbassarla.
func TestCapConfidence(t *testing.T) {
	q := reviews.AIQuestion{Question: reviews.Question{Key: "hosting", Type: reviews.QSingle, Prefill: "hosting",
		Options: []reviews.Option{{Value: "saas", Label: "SaaS"}, {Value: "cloud", Label: "Cloud"}}}, PrefillValue: "saas"}
	same := &qSource{src: reviews.AISource{Kind: "answer"}, question: "hosting", value: "cloud"}
	similar := &qSource{src: reviews.AISource{Kind: "similar"}, question: "hosting", value: "cloud"}
	fact := &qSource{src: reviews.AISource{Kind: "fact"}, factKey: "hosting"}
	page := &qSource{src: reviews.AISource{Kind: "page"}}
	for _, c := range []struct {
		v     any
		model string
		cited []*qSource
		want  string
	}{
		{"cloud", "alta", []*qSource{same}, "alta"},
		{"saas", "alta", []*qSource{same}, "media"}, // la risposta confermata dice un'altra cosa
		{"cloud", "alta", []*qSource{similar}, "media"},
		{"cloud", "alta", []*qSource{page}, "media"},
		{"saas", "alta", []*qSource{fact}, "alta"},
		{"cloud", "alta", []*qSource{fact}, "media"},
		{"cloud", "bassa", []*qSource{same}, "bassa"},
	} {
		if got := capConfidence(q, c.v, c.model, c.cited); got != c.want {
			t.Errorf("%v %s %s: %s, atteso %s", c.v, c.model, c.cited[0].src.Kind, got, c.want)
		}
	}
}
