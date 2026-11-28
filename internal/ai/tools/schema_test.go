package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// Gli schemi degli strumenti sono JSON Schema validi per tutti i fornitori: niente elenchi nulli
// (Gemini rifiuta «required»: null), sempre un oggetto con le proprietà.
func TestSchemiValidi(t *testing.T) {
	for _, tl := range (&Services{}).List() {
		b, err := json.Marshal(tl.Schema)
		if err != nil {
			t.Fatalf("%s: %v", tl.Name, err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("%s: valore nullo nello schema: %s", tl.Name, b)
		}
		if tl.Schema["type"] != "object" || tl.Schema["properties"] == nil {
			t.Errorf("%s: schema non è un oggetto con proprietà: %s", tl.Name, b)
		}
	}
}

// «campi» si accetta come oggetto o come testo JSON; altro è un errore leggibile.
func TestCampiComeTesto(t *testing.T) {
	for in, want := range map[string]int{`{"campi":{"a":1}}`: 1, `{"campi":"{\"a\":1,\"b\":2}"}`: 2, `{"campi":""}`: 0, `{}`: 0} {
		var x updateIn
		if err := decode([]byte(in), &x); err != nil || len(x.Fields) != want {
			t.Errorf("%s: %v %v", in, x.Fields, err)
		}
	}
	var x updateIn
	if err := decode([]byte(`{"campi":"priorità alta"}`), &x); err == nil || !strings.Contains(err.Error(), "oggetto") {
		t.Errorf("testo non JSON: %v", err)
	}
}
