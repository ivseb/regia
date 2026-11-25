package itest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Motore di processo configurabile (piano, passo 1): versioni immutabili, gate e
// regole come dati, più processi per tipo, export e import, migrazione guidata.
func TestConfigurableProcess(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("proc-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Processi", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "editor@" + slug + ".test", Name: "Editor",
		Password: testPassword, Roles: []string{"editor"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "proc@" + slug + ".test", Name: "Configuratore",
		Password: testPassword, Roles: []string{"process_admin"}}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	editor := login(t, srv.URL, "editor@"+slug+".test", testPassword)
	conf := login(t, srv.URL, "proc@"+slug+".test", testPassword)

	// Solo chi ha process.admin vede la configurazione (non serve essere amministratore del tenant).
	editor.do("GET", "/api/v1/processes", nil, http.StatusForbidden)
	list := conf.do("GET", "/api/v1/processes", nil, http.StatusOK)["items"].([]any)
	keys := map[string]bool{}
	for _, p := range list {
		keys[p.(map[string]any)["key"].(string)] = true
	}
	for _, k := range []string{"demand_standard", "impact_standard", "application_standard", "logical_asset_standard"} {
		if !keys[k] {
			t.Fatalf("manca il processo %s: %v", k, keys)
		}
	}

	detail := func(key string) map[string]any { return admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK) }
	version := func(key string) (string, float64) {
		p := detail(key)["process"].(map[string]any)
		return p["key"].(string), p["version"].(float64)
	}
	move := func(id, to string, status int) map[string]any {
		return admin.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}

	// Richiesta A sulla versione 1. Le fasi fuori percorso arrivano marcate.
	a := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta normativa", "driver": "regulatory"}, http.StatusCreated)
	aKey, aID := a["key"].(string), a["id"].(string)
	if k, v := version(aKey); k != "demand_standard" || v != 1 {
		t.Fatalf("processo di A: %s v%v", k, v)
	}
	offPath := 0
	for _, ph := range detail(aKey)["phases"].([]any) {
		if ph.(map[string]any)["off_path"] == true {
			offPath++
		}
	}
	if offPath != 3 {
		t.Fatalf("fasi fuori percorso: %d", offPath)
	}
	admin.do("POST", "/api/v1/elements/"+aID+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	move(aID, "Triage", http.StatusOK)
	move(aID, "Valutazione", http.StatusOK)

	// Regola dati: richiesta normativa → ruolo Compliance al gruppo Compliance.
	found := false
	for _, sh := range detail(aKey)["stakeholders"].([]any) {
		m := sh.(map[string]any)
		if m["role"] == "compliance" && m["name"] == "Compliance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("la regola non ha assegnato la Compliance: %v", detail(aKey)["stakeholders"])
	}

	// Gate come condizione: il passaggio si vede ma è bloccato, con il motivo.
	var blocked []any
	for _, tr := range detail(aKey)["transitions"].([]any) {
		m := tr.(map[string]any)
		if m["to"] == "Prioritizzazione" {
			blocked = m["blocked"].([]any)
		}
	}
	if len(blocked) != 1 || !strings.Contains(blocked[0].(string), "stima") {
		t.Fatalf("gate della stima: %v", blocked)
	}
	if r := move(aID, "Prioritizzazione", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "stima") {
		t.Fatalf("messaggio del gate: %v", r)
	}
	admin.do("PATCH", "/api/v1/demand/requests/"+aID, map[string]any{"estimate_high_cents": 8_000_000}, http.StatusNoContent)
	move(aID, "Prioritizzazione", http.StatusOK)

	// Una bozza con errori si salva ma non si pubblica.
	bundle := conf.do("GET", "/api/v1/processes/demand_standard/export", nil, http.StatusOK)
	def := bundle["definition"].(map[string]any)
	bad := cloneJSON(t, def)
	bad["transitions"] = append(bad["transitions"].([]any), map[string]any{"from": []any{"Bozza"}, "to": "Inesistente", "label": "Boh"})
	saved := conf.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": bad}, http.StatusOK)
	if probs, _ := saved["draft_problems"].([]any); !hasError(probs, "Inesistente") {
		t.Fatalf("problemi della bozza: %v", saved["draft_problems"])
	}
	pub := conf.do("POST", "/api/v1/processes/demand_standard/publish", map[string]any{}, http.StatusUnprocessableEntity)
	if probs, _ := pub["problems"].([]any); !hasError(probs, "Inesistente") {
		t.Fatalf("pubblicazione con errori: %v", pub)
	}
	editor.do("DELETE", "/api/v1/processes/demand_standard/draft", nil, http.StatusForbidden)
	conf.do("DELETE", "/api/v1/processes/demand_standard/draft", nil, http.StatusNoContent)
	if _, err := engine.DecodeDefinition([]byte(`{"phases":[],"initial":"x","transitions":[],"colore":"blu"}`)); err == nil {
		t.Fatal("un campo sconosciuto nella definizione deve essere rifiutato")
	}

	// Versione 2: "Prioritizzazione" diventa "Priorità".
	good := cloneJSON(t, def)
	rename := func(v any) any {
		if s, ok := v.(string); ok && s == "Prioritizzazione" {
			return "Priorità"
		}
		return v
	}
	for _, ph := range good["phases"].([]any) {
		m := ph.(map[string]any)
		m["name"] = rename(m["name"])
	}
	for _, tr := range good["transitions"].([]any) {
		m := tr.(map[string]any)
		m["to"] = rename(m["to"])
		from := m["from"].([]any)
		for i := range from {
			from[i] = rename(from[i])
		}
	}
	conf.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": good, "notes": "Priorità"}, http.StatusOK)
	conf.do("POST", "/api/v1/processes/demand_standard/publish", map[string]any{}, http.StatusOK)

	// Le versioni pubblicate non si toccano, nemmeno dal database.
	if _, err := d.Owner.Exec(ctx, `update process_versions set definition = '{}' where status <> 'draft'`); err == nil {
		t.Fatal("una versione pubblicata è stata modificata")
	}

	// B parte sulla v2; A resta sulla v1 finché non si migra.
	b := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta nuova"}, http.StatusCreated)
	if _, v := version(b["key"].(string)); v != 2 {
		t.Fatalf("B dovrebbe essere sulla v2, è sulla v%v", v)
	}
	if _, v := version(aKey); v != 1 {
		t.Fatalf("A dovrebbe restare sulla v1, è sulla v%v", v)
	}

	// Migrazione: A è in una fase che la v2 non ha.
	plan := conf.do("POST", "/api/v1/processes/demand_standard/migration/preview", map[string]any{}, http.StatusOK)
	if plan["blocked"].(float64) != 1 || plan["total"].(float64) != 1 {
		t.Fatalf("anteprima: %v", plan)
	}
	conf.do("POST", "/api/v1/processes/demand_standard/migration", map[string]any{}, http.StatusUnprocessableEntity)
	conf.do("POST", "/api/v1/processes/demand_standard/migration",
		map[string]any{"phase_map": map[string]string{"Prioritizzazione": "Priorità"}}, http.StatusOK)
	if _, v := version(aKey); v != 2 || state(t, admin, aKey) != "Priorità" {
		t.Fatalf("A dopo la migrazione: v%v %s", v, state(t, admin, aKey))
	}

	// Prova delle condizioni sui fatti di un elemento.
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "driver: normativo stima_max: >50k", "element": aKey}, http.StatusOK)
	if chk["ok"] != true || chk["result"] != true {
		t.Fatalf("condizione: %v", chk)
	}
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "driver normativo"}, http.StatusOK); chk["ok"] != false {
		t.Fatalf("condizione non valida accettata: %v", chk)
	}

	// Conteggio dal vivo: quante richieste soddisfano oggi la condizione; i dati del tipo per il costruttore.
	cnt := admin.do("POST", "/api/v1/conditions/count", map[string]any{"condition": "driver: normativo stima_max: >50k", "type": "business_request"}, http.StatusOK)
	if cnt["ok"] != true || cnt["matched"].(float64) < 1 || cnt["total"].(float64) < cnt["matched"].(float64) {
		t.Fatalf("conteggio: %v", cnt)
	}
	if cnt := admin.do("POST", "/api/v1/conditions/count", map[string]any{"condition": "driver normativo", "type": "business_request"}, http.StatusOK); cnt["ok"] != false {
		t.Fatalf("conteggio con condizione non valida: %v", cnt)
	}
	if cnt := admin.do("POST", "/api/v1/conditions/count", map[string]any{"condition": "driver: inesistente_xyz", "type": "business_request"}, http.StatusOK); cnt["matched"].(float64) != 0 {
		t.Fatalf("nessuna richiesta dovrebbe soddisfarla: %v", cnt)
	}
	facts := admin.do("GET", "/api/v1/conditions/facts?type=business_request", nil, http.StatusOK)
	if len(facts["facts"].([]any)) == 0 {
		t.Fatalf("dati del tipo: %v", facts)
	}

	// Import di un secondo processo per lo stesso tipo, con priorità e applicabilità:
	// le richieste piccole seguono un percorso rapido.
	fast := map[string]any{
		"format": "regia.process/v1", "key": "change_rapido", "name": "Change rapido", "type": "business_request",
		"priority": 10, "applies_when": "stima_max: <10000",
		"definition": map[string]any{
			"phases": []any{
				map[string]any{"name": "Bozza", "category": "todo"},
				map[string]any{"name": "In esecuzione", "category": "in_progress"},
				map[string]any{"name": "Chiusa", "category": "done"},
			},
			"initial": "Bozza",
			"transitions": []any{
				map[string]any{"from": []any{"Bozza"}, "to": "In esecuzione", "label": "Avvia"},
				map[string]any{"from": []any{"In esecuzione"}, "to": "Chiusa", "label": "Chiudi"},
			},
		},
	}
	editor.do("POST", "/api/v1/processes/import?publish=true", fast, http.StatusForbidden)
	conf.do("POST", "/api/v1/processes/import?publish=true", fast, http.StatusOK)
	small := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Piccola", "estimate_high_cents": 500_000}, http.StatusCreated)
	if k, _ := version(small["key"].(string)); k != "change_rapido" {
		t.Fatalf("richiesta piccola sul processo %s", k)
	}
	big := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Grande", "estimate_high_cents": 50_000_000}, http.StatusCreated)
	if k, _ := version(big["key"].(string)); k != "demand_standard" {
		t.Fatalf("richiesta grande sul processo %s", k)
	}
	move(small["id"].(string), "In esecuzione", http.StatusOK)

	// Le query per stato vedono anche le fasi nuove.
	page := admin.do("GET", "/api/v1/elements?q="+strings.ReplaceAll("stato: Priorità", " ", "%20"), nil, http.StatusOK)
	if n := len(page["items"].([]any)); n != 1 {
		t.Fatalf("query per fase nuova: %d risultati", n)
	}
}

func cloneJSON(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasError(probs []any, contains string) bool {
	for _, p := range probs {
		m := p.(map[string]any)
		if m["level"] == "error" && strings.Contains(m["message"].(string), contains) {
			return true
		}
	}
	return false
}
