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
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Ondata 1 del governo (replica/usabilita.md, sezione 4):
//   - il codice di catalogo di un'applicazione (APP-MOB) vale come chiave in link e ricerca;
//   - registrare la risoluzione di un incidente lo porta alla fase di risoluzione se il
//     processo lo consente, altrimenti elenca i passaggi tra cui scegliere;
//   - «causa: fornitore terzo» è la scelta di un fornitore ICT.
func TestOndata1Governo(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("on1-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Ondata 1", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	}); err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("rm", tenant.MemberInput{Roles: []string{"risk_manager"}, Groups: []string{"Risk management"}})
	add("im", tenant.MemberInput{Roles: []string{"incident_manager"}, Groups: []string{"Incident management"}})
	add("viewer", tenant.MemberInput{Roles: []string{"viewer"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	rk := risk.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Work: wk, Risk: rk,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, rm, im, viewer := u("admin"), u("rm"), u("im"), u("viewer")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	eid := func(key string) string {
		return obj(admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"])["id"].(string)
	}

	// --- Il codice di catalogo vale come la chiave dell'elemento.
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"APP-MOB;App mobile;alta;no;personali;;Canali digitali\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	var appKey string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		if obj(a)["code"] == "APP-MOB" {
			appKey = obj(a)["key"].(string)
		}
	}
	if appKey == "" {
		t.Fatal("applicazione APP-MOB non importata")
	}
	for _, ref := range []string{"APP-MOB", "app-mob", appKey} {
		got := obj(viewer.do("GET", "/api/v1/elements/"+ref, nil, http.StatusOK)["element"])
		if got["key"] != appKey {
			t.Fatalf("%s: elemento %v, atteso %s", ref, got["key"], appKey)
		}
	}
	viewer.do("GET", "/api/v1/elements/APP-NONESISTE", nil, http.StatusNotFound)
	found := func(q string) bool {
		for _, it := range list(viewer.do("GET", "/api/v1/elements?q="+q, nil, http.StatusOK)["items"]) {
			if obj(it)["key"] == appKey {
				return true
			}
		}
		return false
	}
	if !found("APP-MOB") || !found("app-mob") || !found(appKey) {
		t.Fatalf("il codice di catalogo non trova l'applicazione in ricerca")
	}

	// --- Risoluzione: dalla fase «Rilevato» il processo non porta a «Risolto»: si sceglie.
	inc := im.do("POST", "/api/v1/incidents", map[string]any{"title": "Accessi lenti all'app", "impacted": []string{appKey}}, http.StatusCreated)
	k1 := inc["key"].(string)
	if st := state(t, im, k1); st != "Rilevato" {
		t.Fatalf("fase iniziale %q", st)
	}
	resolved := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	r1 := im.do("PATCH", "/api/v1/incidents/"+k1, map[string]any{"resolved_at": resolved}, http.StatusOK)
	mv := obj(r1["phase_move"])
	if mv["status"] != "choose" || len(list(mv["options"])) == 0 || !strings.Contains(jsonString(mv["options"]), "Classificato") {
		t.Fatalf("da Rilevato si deve scegliere la fase: %s", jsonString(r1["phase_move"]))
	}
	if st := state(t, im, k1); st != "Rilevato" {
		t.Fatalf("senza passaggio diretto la fase non cambia da sola: %q", st)
	}
	// Una seconda modifica non ripropone la scelta: la risoluzione c'era già.
	if r := im.do("PATCH", "/api/v1/incidents/"+k1, map[string]any{"downtime_minutes": 30}, http.StatusOK); r["phase_move"] != nil {
		t.Fatalf("scelta riproposta: %v", r["phase_move"])
	}

	// --- Da «Classificato» c'è il passaggio a «Risolto»: l'incidente ci va da solo.
	inc2 := im.do("POST", "/api/v1/incidents", map[string]any{"title": "Notifiche push in ritardo", "impacted": []string{appKey}}, http.StatusCreated)
	k2 := inc2["key"].(string)
	im.do("POST", "/api/v1/elements/"+eid(k2)+"/transitions", map[string]any{"to": "Classificato"}, http.StatusOK)
	r2 := im.do("PATCH", "/api/v1/incidents/"+k2, map[string]any{"resolved_at": resolved}, http.StatusOK)
	if mv := obj(r2["phase_move"]); mv["status"] != "moved" || mv["phase"] != "Risolto" {
		t.Fatalf("risoluzione dalla fase Classificato: %s", jsonString(r2["phase_move"]))
	}
	if st := state(t, im, k2); st != "Risolto" {
		t.Fatalf("fase dopo la risoluzione: %q", st)
	}
	resolvedEvents := 0
	for _, e := range list(r2["events"]) {
		if obj(e)["kind"] == "resolved" {
			resolvedEvents++
		}
	}
	if resolvedEvents != 1 {
		t.Fatalf("evento di risoluzione duplicato: %d", resolvedEvents)
	}
	// Chi non ha il permesso non registra la risoluzione.
	viewer.do("PATCH", "/api/v1/incidents/"+k2, map[string]any{"resolved_at": resolved}, http.StatusForbidden)

	// --- Causa: fornitore terzo come scelta del fornitore.
	prov := rm.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Nuvola Calcolo"}, http.StatusCreated)
	pKey := prov["key"].(string)
	im.do("PATCH", "/api/v1/incidents/"+k1, map[string]any{"cause_provider": "FOR-9999"}, http.StatusUnprocessableEntity)
	r3 := im.do("PATCH", "/api/v1/incidents/"+k1, map[string]any{"cause_provider": pKey}, http.StatusOK)
	if r3["cause"] != "provider" || obj(r3["cause_provider"])["key"] != pKey {
		t.Fatalf("causa fornitore: %v %v", r3["cause"], r3["cause_provider"])
	}
	if got := im.do("GET", "/api/v1/incidents/"+k1, nil, http.StatusOK); obj(got["cause_provider"])["title"] != "Nuvola Calcolo" {
		t.Fatalf("fornitore non riletto: %v", got["cause_provider"])
	}
	// Cambiando causa il fornitore si toglie.
	r4 := im.do("PATCH", "/api/v1/incidents/"+k1, map[string]any{"cause": "cyber"}, http.StatusOK)
	if r4["cause_provider"] != nil || r4["cause"] != "cyber" {
		t.Fatalf("fornitore rimasto dopo il cambio di causa: %v", r4["cause_provider"])
	}
}
