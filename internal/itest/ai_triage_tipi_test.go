package itest

import (
	"net/http"
	"strings"
	"testing"

	"regia/internal/devtools/fakeai"
)

// Seguito dell'ondata 3 (replica/ai.md, fatto con l'ondata 4): l'analisi alla creazione vale anche
// per il lavoro (task, story, bug) e per le applicazioni: duplicati dello stesso tipo, fra quelli
// che la persona vede.
func TestAITriageAltriTipi(t *testing.T) {
	e := newOndata2(t, "trtipi")
	fake := e.admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": map[string]any{"provider_id": fake["id"], "model": fakeai.ModelDeep}}, http.StatusOK)
	e.admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	task := e.admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "task", "title": "Rinnovo certificato del portale clienti"}, http.StatusCreated)
	r := e.admin.do("POST", "/api/v1/ai/card/triage", map[string]any{"type": "task", "title": "Rinnovare il certificato portale clienti"}, http.StatusOK)
	if !strings.Contains(fmtJSON(r["duplicates"]), task["key"].(string)) {
		t.Fatalf("duplicati del task: %v", r)
	}
	app := e.admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Protocollo documentale elettronico", "code": "PDOC"}, http.StatusCreated)
	r = e.admin.do("POST", "/api/v1/ai/card/triage", map[string]any{"type": "application", "title": "Protocollo elettronico documentale"}, http.StatusOK)
	if !strings.Contains(fmtJSON(r["duplicates"]), app["key"].(string)) {
		t.Fatalf("duplicati dell'applicazione: %v", r)
	}
}
