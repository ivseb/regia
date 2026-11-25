package itest

import (
	"net/http"
	"testing"
)

// Nomi per lingua di opzioni dei campi e fasi: l'etichetta si salva e si rilegge, le lingue non
// supportate e i testi troppo lunghi sono rifiutati, un tenant nato in inglese ha le fasi di partenza
// con l'etichetta inglese, mentre il nome (quello che citano condizioni e processi) non cambia.
func TestOptionAndPhaseLabels(t *testing.T) {
	_, _, admin, _ := newLangEnv(t, "en")

	f := admin.do("POST", "/api/v1/fields", map[string]any{"key": "canale_et", "name": "Canale", "data_type": "enum",
		"options": []map[string]any{{"name": "Telefono", "labels": map[string]any{"en": "Phone"}}, {"name": "Posta"}}}, http.StatusCreated)
	opts := f["options"].([]any)
	first := opts[0].(map[string]any)
	if first["name"] != "Telefono" || first["labels"].(map[string]any)["en"] != "Phone" {
		t.Fatalf("opzione con etichetta: %v", first)
	}
	if _, ok := opts[1].(map[string]any)["labels"]; ok {
		t.Errorf("l'opzione senza etichetta non ha labels: %v", opts[1])
	}

	save := func(labels map[string]any, status int) map[string]any {
		return admin.do("PUT", "/api/v1/fields/canale_et/options", map[string]any{"options": []map[string]any{
			{"id": first["id"], "name": "Telefono", "labels": labels}, {"id": opts[1].(map[string]any)["id"], "name": "Posta", "labels": map[string]any{"en": " Mail  "}},
		}}, status)
	}
	save(map[string]any{"fr": "Téléphone"}, http.StatusUnprocessableEntity)
	save(map[string]any{"en": string(make([]rune, 101))}, http.StatusUnprocessableEntity)
	got := save(map[string]any{"en": "Landline", "it": ""}, http.StatusOK)["options"].([]any)
	if l := got[0].(map[string]any)["labels"].(map[string]any); l["en"] != "Landline" || len(l) != 1 {
		t.Errorf("etichette salvate: %v", l)
	}
	if l := got[1].(map[string]any)["labels"].(map[string]any); l["en"] != "Mail" {
		t.Errorf("spazi normalizzati: %v", l)
	}
	// Svuotando l'etichetta il valore torna a seguire il nome.
	got = save(map[string]any{"en": ""}, http.StatusOK)["options"].([]any)
	if _, ok := got[0].(map[string]any)["labels"]; ok {
		t.Errorf("etichetta svuotata ancora presente: %v", got[0])
	}

	// Le fasi di partenza del tenant in inglese hanno l'etichetta inglese; il nome resta italiano.
	items := admin.do("GET", "/api/v1/state-labels", nil, http.StatusOK)["items"].(map[string]any)
	if len(items) == 0 {
		t.Fatalf("nessuna etichetta delle fasi nel tenant in inglese")
	}
	for name, l := range items {
		if l.(map[string]any)["en"] == "" || l.(map[string]any)["en"] == name {
			t.Errorf("fase %q: etichetta inglese %v", name, l)
		}
	}
}
