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
