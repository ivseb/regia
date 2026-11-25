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
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Sintesi di scheda (replica/usabilita.md, proposta 2): la richiesta «decisione prima» e la vista 360
// dell'applicazione sono letture aggregate con i permessi e la RLS di chi chiede.
func TestSchedaSintesi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, other := "sch-"+stamp, "sco-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, risk.Provision}
	for _, s := range []string{slug, other} {
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: s, Name: "Sintesi " + s, AdminEmail: "admin@" + s + ".test", AdminPassword: testPassword, Provision: provision,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("viewer", tenant.MemberInput{Roles: []string{"viewer"}})
	add("req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rk := risk.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Risk: rk,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	viewer := login(t, srv.URL, "viewer@"+slug+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)
	stranger := login(t, srv.URL, "admin@"+other+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{
		"csv": "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\nFE-AR;Frontend area riservata;alta;sì;personali;Web;Area riservata\n"}, http.StatusOK)
	var appKey, appID string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		appKey, appID = obj(a)["key"].(string), obj(a)["id"].(string)
	}
	br := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuova area riservata"}, http.StatusCreated)
	reqKey := br["key"].(string)
	admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/impacts", map[string]any{"application_id": appID, "change_kind": "change"}, http.StatusCreated)

	// --- Richiesta: i numeri ci sono, e mancano stima e valutazione.
	s := admin.do("GET", "/api/v1/elements/"+reqKey+"/summary", nil, http.StatusOK)
	if s["key"] != reqKey || int(s["impacts"].(float64)) != 1 {
		t.Fatalf("sintesi della richiesta: %v", s)
	}
	kinds := map[string]bool{}
	for _, m := range list(s["missing"]) {
		kinds[obj(m)["kind"].(string)] = true
	}
	if !kinds["estimate"] || !kinds["score"] {
		t.Fatalf("manca la segnalazione di stima e punteggio: %v", s["missing"])
	}
	if s["requirements"] == nil || s["reviews"] == nil {
		t.Fatalf("blocchi mancanti: %v", s)
	}
	// Una sintesi di richiesta su un'applicazione, e quella 360 su una richiesta, sono errori di validazione.
	admin.do("GET", "/api/v1/elements/"+appKey+"/summary", nil, http.StatusUnprocessableEntity)
	admin.do("GET", "/api/v1/architecture/applications/"+reqKey+"/360", nil, http.StatusUnprocessableEntity)

	// --- Applicazione 360.
	a := admin.do("GET", "/api/v1/architecture/applications/"+appKey+"/360", nil, http.StatusOK)
	if a["key"] != appKey || int(a["requests_open"].(float64)) != 1 || a["deps"] == nil {
		t.Fatalf("360: %v", a)
	}
	if got := obj(list(a["requests"])[0])["request_key"]; got != reqKey {
		t.Fatalf("la richiesta aperta sull'applicazione è %v, attesa %s", got, reqKey)
	}
	// Il codice di catalogo vale come chiave anche qui.
	viewer.do("GET", "/api/v1/architecture/applications/FE-AR/360", nil, http.StatusOK)

	// --- Permessi: un richiedente del portale non legge il catalogo; chi sta in un altro tenant non vede nulla.
	for _, p := range []string{"/api/v1/elements/" + reqKey + "/summary", "/api/v1/architecture/applications/" + appKey + "/360"} {
		if got := req.doRaw("GET", p, nil).status; got != http.StatusForbidden && got != http.StatusNotFound {
			t.Fatalf("%s per il richiedente: %d", p, got)
		}
		stranger.do("GET", p, nil, http.StatusNotFound)
	}
}
