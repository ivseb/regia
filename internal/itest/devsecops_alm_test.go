package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/devtools/fakealm"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	almconn "regia/internal/modules/devsecops/connectors/alm"
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Connettore ALM/Quality Center in sola lettura, contro il finto ALM (fakealm, http, rete
// interna, con l'intestazione XSRF obbligatoria come da ALM 16.00): configurazione con segreti
// cifrati, prova di connessione (API key, credenziali errate, campo inesistente, versione che
// accetta l'API key solo da oauth2/login), lettura incrementale di test set, istanze,
// esecuzioni e difetti, sessione chiusa alla fine, unità proposte e confermate, test del change
// nella certificazione del rilascio, regola del gate sui test, evidenza GRC, dato personale del
// tester, permessi e isolamento tra tenant.

const almAPI = "/api/v1/devsecops/alm"

func TestAlmConnector(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	slug, slugB := "alm-"+stamp, "almb-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	var tenantID string
	for _, sl := range []string{slug, slugB} {
		ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: sl, Name: "ALM " + sl, AdminEmail: "admin@" + sl + ".test",
			AdminName: "Amministratrice", AdminPassword: testPassword, Provision: provision})
		if err != nil {
			t.Fatal(err)
		}
		if sl == slug {
			tenantID = ten.TenantID
		}
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "lettore@" + slug + ".test", Name: "Lettore",
		Password: testPassword, Roles: []string{"viewer"}}); err != nil {
		t.Fatal(err)
	}

	fake := fakealm.New()
	fake.AddAPIKey("regia-client", "segreto-api-key-di-prova")
	fake.RequireXSRF = true
	rome, _ := time.LoadLocation("Europe/Rome")
	fake.Loc = rome
	now := time.Now()
	fakealm.Demo(fake, now)
	fakeSrv := httptest.NewServer(fake.Handler())
	t.Cleanup(fakeSrv.Close)

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rq := requirements.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	gs := grc.Register(eng)
	box := secretbox.Random()
	as := auth.NewService(d, time.Hour)
	as.SetSecretBox(box)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := almconn.New(almconn.Options{DB: d, Box: box, AllowPrivate: true, Log: log, Timeout: 5 * time.Second})
	srv := api.New(api.Options{DB: d, Auth: as, Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Requirements: rq, Devsecops: dso, GRC: gs,
		Log: log, Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, ALM: svc, FilesDir: t.TempDir()})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(svc.WaitIdle)
	admin := login(t, hs.URL, "admin@"+slug+".test", testPassword)
	reader := login(t, hs.URL, "lettore@"+slug+".test", testPassword)
	adminB := login(t, hs.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	num := func(v any) int { f, _ := v.(float64); return int(f) }

	admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Ordini online", "code": "ORD"}, http.StatusCreated)
	var ordID string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		if obj(a)["code"] == "ORD" {
			ordID = obj(a)["id"].(string)
		}
	}

	// --- Configurazione: validazione, segreti cifrati e mai restituiti.
	base := map[string]any{"name": "ALM collaudo", "base_url": fakeSrv.URL, "auth": "apikey", "client_id": "regia-client",
		"client_secret": "segreto-api-key-di-prova", "private_network": true,
		"projects": []any{map[string]any{"domain": "QUALITA", "project": "Ordini_online"}, map[string]any{"domain": "QUALITA", "project": "Pagamenti"}}}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range base {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	admin.do("POST", almAPI+"/integrations", with("projects", []any{}), http.StatusUnprocessableEntity)
	admin.do("POST", almAPI+"/integrations", with("change_field", "USER 01"), http.StatusUnprocessableEntity)
	admin.do("POST", almAPI+"/integrations", with("change_pattern", "(["), http.StatusUnprocessableEntity)
	admin.do("POST", almAPI+"/integrations", with("app_source", "test_set_field"), http.StatusUnprocessableEntity) // manca il campo
	admin.do("POST", almAPI+"/integrations", with("interval_minutes", 5), http.StatusUnprocessableEntity)
	reader.do("GET", almAPI+"/integrations", nil, http.StatusForbidden)
	id := admin.do("POST", almAPI+"/integrations", base, http.StatusCreated)["id"].(string)
	it := admin.do("GET", almAPI+"/integrations/"+id, nil, http.StatusOK)
	cfg := obj(it["config"])
	if it["has_client_secret"] != true || strings.Contains(jsonString(it), "segreto-api-key") || cfg["base_url"] != fakeSrv.URL+"/qcbin" ||
		cfg["change_source"] != "test_set_field" || cfg["change_field"] != "user-01" || cfg["app_source"] != "project" || cfg["timezone"] != "Europe/Rome" ||
		jsonString(cfg["defect_closed"]) != `["Closed","Rejected"]` {
		t.Fatalf("integrazione salvata: %v", it)
	}

	// --- Prova di connessione.
	tr := admin.do("POST", almAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != true || tr["method"] != "alm-authenticate" || len(list(tr["projects"])) != 2 {
		t.Fatalf("prova di connessione: %v", tr)
	}
	if fake.Open() != 0 {
		t.Fatalf("la prova di connessione ha lasciato %d sessioni aperte", fake.Open())
	}
	upd := func(m map[string]any) {
		admin.do("PATCH", almAPI+"/integrations/"+id, m, http.StatusNoContent)
	}
	upd(with("change_field", "user-99"))
	if tr := admin.do("POST", almAPI+"/integrations/"+id+"/test", nil, http.StatusOK); tr["ok"] != false ||
		!strings.Contains(jsonString(tr["projects"]), "user-99") {
		t.Fatalf("prova con un campo inesistente: %v", tr)
	}
	upd(with("client_secret", "sbagliato"))
	if tr := admin.do("POST", almAPI+"/integrations/"+id+"/test", nil, http.StatusOK); tr["ok"] != false || !strings.Contains(tr["message"].(string), "rifiutato l'API key") {
		t.Fatalf("prova con il segreto sbagliato: %v", tr)
	}
	upd(base)

	// ALM 12.60-14.x: l'API key entra solo da rest/oauth2/login.
	legacy := fakealm.New()
	legacy.AddAPIKey("regia-client", "segreto-api-key-di-prova")
	legacy.OnlyOAuthAPIKeys = true
	legacy.AddProject("QUALITA", "Ordini_online")
	legacySrv := httptest.NewServer(legacy.Handler())
	t.Cleanup(legacySrv.Close)
	lb := with("base_url", legacySrv.URL+"/qcbin/")
	lb["name"], lb["projects"] = "ALM vecchio", []any{map[string]any{"domain": "QUALITA", "project": "Ordini_online"}}
	lid := admin.do("POST", almAPI+"/integrations", lb, http.StatusCreated)["id"].(string)
	if tr := admin.do("POST", almAPI+"/integrations/"+lid+"/test", nil, http.StatusOK); tr["method"] != "oauth2/login" {
		t.Fatalf("prova su ALM 12.60: %v", tr)
	}
	admin.do("DELETE", almAPI+"/integrations/"+lid, nil, http.StatusNoContent)

	// --- Prima sincronizzazione.
	sync := func(full bool) map[string]any {
		t.Helper()
		q := ""
		if full {
			q = "?full=true"
		}
		admin.do("POST", almAPI+"/integrations/"+id+"/sync"+q, nil, http.StatusAccepted)
		svc.WaitIdle()
		return obj(list(admin.do("GET", almAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"])[0])
	}
	run := sync(false)
	st := obj(run["stats"])
	if run["status"] != "ok" || num(st["new_test_sets"]) != 4 || num(st["linked"]) != 4 || num(st["items"]) != 15 || num(st["new_runs"]) != 12 ||
		num(st["defects"]) != 2 || num(st["new_units"]) != 2 {
		t.Fatalf("prima sincronizzazione: %v", run)
	}
	if fake.Open() != 0 {
		t.Fatalf("la sincronizzazione ha lasciato %d sessioni aperte", fake.Open())
	}
	// Il nome del tester è un dato personale: di default non si conserva.
	var testers int
	if err := d.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select count(*) from test_runs where tester is not null").Scan(&testers)
	}); err != nil || testers != 0 {
		t.Fatalf("tester conservati: %d %v", testers, err)
	}
	// Seconda lettura: incrementale e idempotente.
	run = sync(false)
	if st := obj(run["stats"]); num(st["new_test_sets"]) != 0 || num(st["new_runs"]) != 0 {
		t.Fatalf("seconda sincronizzazione: %v", run)
	}
	if q := fake.Queries["runs"]; len(q) == 0 || !strings.HasPrefix(q[len(q)-1], "last-modified[>=") {
		t.Fatalf("filtro incrementale delle esecuzioni: %v", q)
	}

	// --- Unità: un progetto per applicazione, proposta e conferma.
	units := list(admin.do("GET", almAPI+"/integrations/"+id+"/units", nil, http.StatusOK)["items"])
	var ordUnit map[string]any
	for _, u := range units {
		if obj(u)["name"] == "Ordini_online" {
			ordUnit = obj(u)
		}
	}
	if len(units) != 2 || ordUnit == nil || ordUnit["status"] != "suggested" || obj(obj(ordUnit["suggestion"])["application"])["id"] != ordID {
		t.Fatalf("unità proposte: %v", units)
	}
	reader.do("PUT", almAPI+"/integrations/"+id+"/units/"+ordUnit["id"].(string), map[string]any{"status": "confirmed"}, http.StatusForbidden)
	if n := admin.do("POST", almAPI+"/integrations/"+id+"/units/accept", map[string]any{"ids": []string{ordUnit["id"].(string)}}, http.StatusOK); num(n["confirmed"]) != 1 {
		t.Fatalf("conferma delle proposte: %v", n)
	}

	// --- Test set del change, con i numeri per stato.
	sets := list(reader.do("GET", almAPI+"/test-sets?change=chg0045120", nil, http.StatusOK)["items"])
	byName := map[string]map[string]any{}
	for _, s := range sets {
		byName[obj(s)["name"].(string)] = obj(s)
	}
	sys := byName["Sistema - ordini"]
	if len(sets) != 3 || sys == nil || fmt.Sprint(num(obj(sys["counts"])["planned"]), num(obj(sys["counts"])["passed"]), num(obj(sys["counts"])["failed"])) != "5 4 1" ||
		num(sys["open_defects"]) != 1 || sys["cycle"] != "Ciclo 1 - Sistema" || sys["release"] != "R2026.12" || sys["application"] == nil ||
		!strings.HasPrefix(sys["url"].(string), "td://Ordini_online.QUALITA.") || byName["Sistema - pagamenti"]["application"] != nil {
		t.Fatalf("test set del change: %v", sets)
	}

	// --- Certificazione del rilascio: test con origine ALM e difetti aperti.
	ciTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	bearerDo(t, hs.URL, ciTok, "POST", "/api/v1/devsecops/releases", map[string]any{"format": "regia.devsecops/v1",
		"service": map[string]any{"name": "ordini-api", "repository": "acme/ordini-api", "provider": "github", "application": "ORD"},
		"version": "2.5.0", "commit": strings.Repeat("b", 40), "environment": "prod", "change_number": "CHG0045120", "at": now.UTC().Format(time.RFC3339)}, http.StatusAccepted)
	cert := reader.do("GET", "/api/v1/devsecops/changes/CHG0045120/certification", nil, http.StatusOK)
	alm := 0
	for _, r := range list(cert["tests"]) {
		if obj(r)["origin"] == "alm" {
			alm++
		}
	}
	tot := obj(cert["test_totals"])
	def := obj(cert["defects"])
	if alm != 3 || num(tot["planned"]) != 12 || num(tot["executed"]) != 10 || num(tot["passed"]) != 9 || num(tot["failed"]) != 1 ||
		num(tot["blocked"]) != 1 || num(tot["not_run"]) != 1 || num(def["open"]) != 1 || num(obj(def["by_severity"])["high"]) != 1 {
		t.Fatalf("certificazione con i test di ALM: tests=%v totali=%v difetti=%v", cert["tests"], tot, def)
	}

	// --- Gate: regola sui test del change.
	rule := func(params map[string]any) map[string]any {
		return map[string]any{"mode": "block", "override_enabled": false, "override_hours": 24,
			"rules": []any{map[string]any{"kind": "tests", "enabled": true, "level": "block", "params": params}}}
	}
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"min_executed_pct": 150}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"defects_severity": "forse"}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"max_failed": 0, "min_executed_pct": 80, "defects_severity": "high",
		"max_open_defects": 0, "require_results": true}), http.StatusOK)
	gateTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Gate", "scopes": []string{"devsecops.gate"}}, http.StatusCreated)["token"].(string)
	gate := func(change string, want int) (map[string]any, string) {
		t.Helper()
		g := bearerDo(t, hs.URL, gateTok, "POST", "/api/v1/devsecops/gate", map[string]any{"format": "regia.devsecops/v1", "application": "ORD",
			"environment": "prod", "change": change}, want)
		for _, r := range list(g["reasons"]) {
			if obj(r)["kind"] == "tests" {
				return g, obj(r)["message"].(string)
			}
		}
		return g, ""
	}
	if g, msg := gate("CHG0045120", http.StatusConflict); g["result"] != "fail" || !strings.Contains(msg, "1 falliti") || !strings.Contains(msg, "1 difetti aperti") {
		t.Fatalf("gate con un test fallito: %v", g)
	}
	if g, msg := gate("CHG0099999", http.StatusConflict); g["result"] != "fail" || !strings.Contains(msg, "Nessun test per il change CHG0099999") {
		t.Fatalf("gate senza test: %v", g)
	}

	// --- Lettura incrementale: il test fallito è rieseguito e passa, il difetto si chiude.
	later := now.Add(time.Minute)
	fake.Now = func() time.Time { return later }
	fixed := false
	for _, inst := range fakeInstances(fake, "QUALITA", "Ordini_online") {
		if inst["status"] == "Failed" {
			fake.Put("QUALITA", "Ordini_online", "test-instances", map[string]string{"id": inst["id"], "status": "Passed",
				"exec-date": later.In(rome).Format(time.DateOnly), "exec-time": later.In(rome).Format("15:04:05")})
			fake.Put("QUALITA", "Ordini_online", "runs", map[string]string{"name": "Run riesecuzione", "cycle-id": inst["cycle-id"], "testcycl-id": inst["id"],
				"test-id": inst["test-id"], "status": "Passed", "execution-date": later.In(rome).Format(time.DateOnly), "execution-time": later.In(rome).Format("15:04:05")})
			fixed = true
		}
	}
	for _, def := range fakeDefects(fake, "QUALITA", "Ordini_online") {
		if def["status"] == "Open" {
			fake.Put("QUALITA", "Ordini_online", "defects", map[string]string{"id": def["id"], "status": "Closed"})
		}
	}
	if !fixed {
		t.Fatal("nessuna istanza fallita nel finto ALM")
	}
	// Il filtro è «>=» sul cursore: le entità con la stessa ora si rileggono (senza doppioni).
	if run := sync(false); num(obj(run["stats"])["new_runs"]) != 1 || num(obj(run["stats"])["items"]) < 1 || num(obj(run["stats"])["defects"]) < 1 {
		t.Fatalf("sincronizzazione incrementale: %v", run)
	}
	if g, msg := gate("CHG0045120", http.StatusOK); g["result"] != "pass" || !strings.Contains(msg, "eseguiti 10 (83%)") {
		t.Fatalf("gate dopo la riesecuzione: %v / %s", g, msg)
	}

	// --- Evidenza GRC: sorgente «tests», alla generazione della certificazione.
	admin.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusCreated)
	ctl := admin.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Test eseguiti prima del rilascio",
		"requirements": []any{map[string]any{"ref": "dora:Art. 8"}}}, http.StatusCreated)
	admin.do("PUT", "/api/v1/devsecops/gate/evidence-map", map[string]any{"items": []any{map[string]any{"source": "tests", "control": ctl["key"]}}}, http.StatusOK)
	admin.do("POST", "/api/v1/devsecops/changes/CHG0045120/certification", nil, http.StatusCreated)
	found := false
	for _, e := range list(admin.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK)["items"]) {
		if strings.Contains(obj(e)["title"].(string), "Esito dei test del change CHG0045120") {
			found = strings.Contains(jsonString(obj(e)["controls"]), ctl["key"].(string))
		}
	}
	if !found {
		t.Fatalf("evidenza dei test: %v", admin.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK))
	}

	// --- Il tester si conserva solo se chi configura lo chiede.
	keep := with("keep_tester", true)
	upd(keep)
	sync(true)
	if err := d.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select count(*) from test_runs where tester is not null").Scan(&testers)
	}); err != nil || testers == 0 {
		t.Fatalf("tester con keep_tester: %d %v", testers, err)
	}
	upd(base)
	if err := d.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select count(*) from test_runs where tester is not null").Scan(&testers)
	}); err != nil || testers != 0 {
		t.Fatalf("tester dopo aver tolto keep_tester: %d %v", testers, err)
	}

	// --- Isolamento tra tenant e cancellazione.
	adminB.do("GET", almAPI+"/integrations/"+id, nil, http.StatusNotFound)
	if l := list(adminB.do("GET", almAPI+"/test-sets", nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("test set visti da un altro tenant: %v", l)
	}
	admin.do("DELETE", almAPI+"/integrations/"+id, nil, http.StatusNoContent)
	if l := list(admin.do("GET", almAPI+"/test-sets", nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("test set dopo la cancellazione: %v", l)
	}
}

// fakeInstances e fakeDefects leggono le entità del finto ALM (per modificarle nei test).
func fakeInstances(s *fakealm.Server, domain, project string) []map[string]string {
	return s.List(domain, project, "test-instances")
}

func fakeDefects(s *fakealm.Server, domain, project string) []map[string]string {
	return s.List(domain, project, "defects")
}
