package itest

import (
	"context"
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
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Procedura «Coinvolgi un team»: l'anteprima non scrive, la conferma scrive tipo di valutazione,
// questionario, regola e nuova versione del processo; la regola scatta davvero sulla richiesta
// (apre il parere e crea l'impatto per il team) e le richieste già avviate restano sulla loro versione.
func TestCoinvolgiUnTeam(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("coi-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Coinvolgi", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision}}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]tenant.MemberInput{
		"req": {MemberType: "requester", Roles: []string{"requester"}},
		"dm":  {Roles: []string{"editor"}, Groups: []string{"Demand management"}},
	} {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Reviews: rev,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin, req, man := login(t, srv.URL, "admin@"+slug+".test", testPassword), login(t, srv.URL, "req@"+slug+".test", testPassword), login(t, srv.URL, "dm@"+slug+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	versions := func() int {
		return len(list(admin.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)["versions"]))
	}
	nTypes := func() int { return len(list(admin.do("GET", "/api/v1/review-config", nil, http.StatusOK)["types"])) }
	hasTeam := func(name string) bool {
		for _, x := range list(admin.do("GET", "/api/v1/teams", nil, http.StatusOK)["items"]) {
			if obj(x)["name"] == name {
				return true
			}
		}
		return false
	}

	// Un'applicazione e un asset logico, e una richiesta che lo cita, avviata prima della procedura.
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"PAY;Pagamenti;alta;no;finanziari;Payments;Area clienti\n"}, http.StatusOK)
	var assetID string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/assets", nil, http.StatusOK)["items"]) {
		assetID = obj(a)["id"].(string)
	}
	mk := func(title string) (string, string) {
		br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": title}, http.StatusCreated)
		req.do("POST", "/api/v1/demand/requests/"+br["key"].(string)+"/assets", map[string]any{"asset_id": assetID}, http.StatusNoContent)
		return br["key"].(string), br["id"].(string)
	}
	_, oldID := mk("Richiesta prima della regola")

	// Solo l'amministratore; niente da fare = errore; fase inesistente = errore.
	body := map[string]any{"new_team": "Cloud broker", "process": "demand_standard", "phase": "Valutazione", "review": true, "work": true}
	man.do("POST", "/api/v1/coinvolgi/anteprima", body, http.StatusForbidden)
	admin.do("POST", "/api/v1/coinvolgi/anteprima", map[string]any{"new_team": "X", "process": "demand_standard"}, http.StatusUnprocessableEntity)
	admin.do("POST", "/api/v1/coinvolgi/anteprima", map[string]any{"new_team": "X", "process": "demand_standard", "phase": "Inesistente", "review": true}, http.StatusUnprocessableEntity)

	// L'anteprima descrive tutto e non scrive niente (nemmeno il team nuovo).
	v0, ty0 := versions(), nTypes()
	pre := admin.do("POST", "/api/v1/coinvolgi/anteprima", body, http.StatusOK)
	if pre["applied"] != false || pre["team_new"] != true || obj(pre["review"])["type_key"] != "parere_cloud_broker" || pre["version"].(float64) != float64(v0+1) {
		t.Fatalf("anteprima: %v", pre)
	}
	if versions() != v0 || nTypes() != ty0 || hasTeam("Cloud broker") {
		t.Fatalf("l'anteprima ha scritto: versioni %d→%d, tipi %d→%d", v0, versions(), ty0, nTypes())
	}

	// La conferma scrive.
	done := admin.do("POST", "/api/v1/coinvolgi", body, http.StatusOK)
	if done["applied"] != true || versions() != v0+1 || nTypes() != ty0+1 || !hasTeam("Cloud broker") {
		t.Fatalf("conferma: %v", done)
	}
	proc := admin.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	if r := jsonString(obj(proc["current"])["rules"]); !strings.Contains(r, "coinvolgi_cloud_broker") || !strings.Contains(r, "create_impact") || !strings.Contains(r, "parere_cloud_broker") {
		t.Fatalf("regola non scritta: %s", r)
	}
	cfg := admin.do("GET", "/api/v1/review-config", nil, http.StatusOK)
	if s := jsonString(cfg["triggers"]); !strings.Contains(s, "parere_cloud_broker") {
		t.Fatalf("il tipo non compare tra le regole che aprono le valutazioni: %s", s)
	}

	// La richiesta già avviata resta sulla sua versione: niente parere né impatto.
	req.do("POST", "/api/v1/elements/"+oldID+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)

	// Una richiesta nuova entra in «Valutazione»: si apre il parere e nasce l'impatto per il team.
	key, id := mk("Migrazione al cloud")
	for _, to := range []string{"Inviata", "Triage", "Valutazione"} {
		u := man
		if to == "Inviata" {
			u = req
		}
		u.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	if got := jsonString(admin.do("GET", "/api/v1/elements/"+key+"/reviews", nil, http.StatusOK)); !strings.Contains(got, "parere_cloud_broker") {
		t.Fatalf("parere non aperto: %s", got)
	}
	an := admin.do("GET", "/api/v1/demand/requests/"+key+"/analysis", nil, http.StatusOK)
	if imps := list(an["impacts"]); len(imps) != 1 || obj(imps[0])["team"] != "Cloud broker" {
		t.Fatalf("impatto per il team non creato: %v", an["impacts"])
	}

	// Una bozza aperta nel processo blocca la procedura (non si pubblica il lavoro di qualcun altro).
	admin.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": obj(proc["current"])}, http.StatusOK)
	admin.do("POST", "/api/v1/coinvolgi", map[string]any{"new_team": "Altro", "process": "demand_standard", "review": true}, http.StatusUnprocessableEntity)
}
