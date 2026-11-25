package itest

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// DevSecOps, fase 5a (metriche DORA): i quattro numeri dai rilasci, dai deploy, dai controlli
// e dagli incidenti collegati; deploy fallito per incidente, hotfix e rollback; punti in
// metric_points per tenant, team e applicazione; soglie e finestra con anteprima; fatti
// dell'applicazione; permessi; isolamento tra tenant.
func TestDevsecopsDora(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, slugB := "dora-"+stamp, "dorab-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, risk.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "DORA", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name string
		in   tenant.MemberInput
	}{
		{"im", tenant.MemberInput{Roles: []string{"incident_manager"}, Groups: []string{"Incident management"}}},
		{"lettore", tenant.MemberInput{Roles: []string{"viewer"}}},
	} {
		m.in.TenantSlug, m.in.Email, m.in.Name, m.in.Password = slug, m.name+"@"+slug+".test", m.name, testPassword
		if _, err := tenant.AddMember(ctx, d, m.in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	rk := risk.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	rev.Now, rq.Now, dso.Now, rk.Now = now, now, now, now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, Devsecops: dso, Risk: rk,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	im := login(t, srv.URL, "im@"+slug+".test", testPassword)
	reader := login(t, srv.URL, "lettore@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	near := func(v any, want float64) bool { f, ok := v.(float64); return ok && math.Abs(f-want) < 0.06 }

	// --- Catalogo: ORD (team Ordini, asset Commercio elettronico) e WEB (stesso asset, nessun rilascio).
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini"}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"ORD;Gestione ordini;critica;sì;personali;Ordini;Commercio elettronico\n" +
		"WEB;Vetrina web;media;no;pubblici;Ordini;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]map[string]any{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)
	}
	ordKey, ordID := apps["ORD"]["key"].(string), apps["ORD"]["id"].(string)
	teamID := obj(list(admin.do("GET", "/api/v1/teams", nil, http.StatusOK)["items"])[0])["id"].(string)

	tok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	push := func(path string, body map[string]any) {
		t.Helper()
		body["format"] = "regia.devsecops/v1"
		bearerDo(t, srv.URL, tok, "POST", "/api/v1/devsecops/"+path, body, http.StatusAccepted)
	}
	svc := map[string]any{"name": "orders-api", "repository": "shop/orders-api", "provider": "github", "application": "ORD"}
	ts := func(day, hour int) string {
		return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	ok := []map[string]any{{"name": "test", "mode": "block", "outcome": "passed"}}
	release := func(version, commit string, checkAt, relAt, title string) {
		push("check-runs", map[string]any{"service": svc, "commit": commit, "run_id": commit, "verdict": "passed", "checks": ok, "at": checkAt})
		push("releases", map[string]any{"service": svc, "version": version, "commit": commit, "environment": "prod", "change_number": "CHG-" + version,
			"change_title": title, "at": relAt, "checks": ok})
	}
	// Novembre: v1 (2,1 giorni), v2 (1,1), v3 (1,0) seguita da un incidente di 4 ore e da un
	// hotfix, v4 (1,0) seguita dal ritorno alla versione precedente.
	release("1.0.0", "c1", ts(2, 8), ts(4, 10), "Prime modifiche")
	release("1.1.0", "c2", ts(9, 8), ts(10, 10), "Carrello")
	release("1.2.0", "c3", ts(16, 10), ts(17, 10), "Pagamento")
	release("1.2.1", "c4", ts(17, 14), ts(17, 15), "Hotfix per il pagamento")
	release("1.3.0", "c5", ts(23, 10), ts(24, 10), "Spedizioni")
	push("deployments", map[string]any{"service": svc, "environment": "prod", "version": "1.2.1", "status": "succeeded", "at": ts(24, 12), "source": "gitops", "external_ref": "argo/orders/1"})

	inc := im.do("POST", "/api/v1/incidents", map[string]any{"title": "Ordini non confermati", "detected_at": ts(17, 14), "impacted": []string{ordKey}}, http.StatusCreated)
	im.do("PATCH", "/api/v1/incidents/"+inc["key"].(string), map[string]any{"resolved_at": ts(17, 18)}, http.StatusOK)
	// Un incidente su un'altra applicazione senza rilasci non cambia la frequenza ma conta nel tempo di ripristino.
	inc2 := im.do("POST", "/api/v1/incidents", map[string]any{"title": "Vetrina lenta", "detected_at": ts(20, 9), "impacted": []string{apps["WEB"]["key"].(string)}}, http.StatusCreated)
	im.do("PATCH", "/api/v1/incidents/"+inc2["key"].(string), map[string]any{"resolved_at": ts(20, 11)}, http.StatusOK)

	// --- Permessi: il ricalcolo e le impostazioni sono del catalogo o dell'amministratore.
	reader.do("POST", "/api/v1/devsecops/dora/recompute", nil, http.StatusForbidden)
	reader.do("PUT", "/api/v1/devsecops/dora/settings", map[string]any{}, http.StatusForbidden)
	if d0 := reader.do("GET", "/api/v1/devsecops/dora?period=month", nil, http.StatusOK); d0["computed"] != false {
		t.Fatalf("prima del calcolo: %v", d0["computed"])
	}
	rep := admin.do("POST", "/api/v1/devsecops/dora/recompute", nil, http.StatusOK)
	if rep["points"].(float64) < 20 {
		t.Fatalf("punti salvati: %v", rep)
	}

	// --- Cruscotto del tenant: novembre è l'ultimo mese concluso.
	dash := reader.do("GET", "/api/v1/devsecops/dora?period=month&compare=team", nil, http.StatusOK)
	metrics := map[string]map[string]any{}
	for _, m := range list(dash["metrics"]) {
		metrics[obj(m)["key"].(string)] = obj(m)
	}
	// 6 deploy riusciti in 30 giorni = 1,4 a settimana; 2 falliti su 6 (v1.2.0: incidente e hotfix, v1.3.0: ritorno).
	if !near(metrics["deploy_frequency"]["value"], 1.4) || metrics["deploy_frequency"]["level"] != "high" {
		t.Fatalf("frequenza: %v", metrics["deploy_frequency"])
	}
	if !near(metrics["lead_time_days"]["value"], 1.0) || metrics["lead_time_days"]["level"] != "elite" {
		t.Fatalf("lead time: %v", metrics["lead_time_days"])
	}
	if !near(metrics["change_failure_rate"]["value"], 33.33) || metrics["change_failure_rate"]["level"] != "low" {
		t.Fatalf("tasso di fallimento: %v", metrics["change_failure_rate"])
	}
	// Mediana tra 4 ore (ordini) e 2 ore (vetrina).
	if !near(metrics["mttr_hours"]["value"], 3.0) || metrics["mttr_hours"]["level"] != "high" {
		t.Fatalf("tempo di ripristino: %v", metrics["mttr_hours"])
	}
	if dash["level"] != "medium" {
		t.Fatalf("livello complessivo: %v", dash["level"])
	}
	if rows := list(dash["compare"]); len(rows) != 1 || obj(rows[0])["name"] != "Ordini" {
		t.Fatalf("confronto per team: %v", dash["compare"])
	}

	// --- Per team e per applicazione: gli stessi numeri; per l'asset anche l'incidente della vetrina.
	team := reader.do("GET", "/api/v1/devsecops/dora?period=month&scope=team&subject="+teamID, nil, http.StatusOK)
	if v := obj(list(team["metrics"])[2])["value"]; !near(v, 33.33) {
		t.Fatalf("team: %v", v)
	}
	app := reader.do("GET", "/api/v1/devsecops/dora?period=week&scope=application&subject="+ordKey, nil, http.StatusOK)
	if len(list(app["metrics"])) != 4 || obj(app["subject"])["id"] != ordID {
		t.Fatalf("applicazione: %v", app["subject"])
	}
	// Settimana del 16-22 novembre: v1.2.0 e v1.2.1; l'incidente e l'hotfix.
	wk := reader.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=application&subject="+ordKey+"&at="+ts(17, 12), nil, http.StatusOK)
	deploys := list(wk["deploys"])
	if len(deploys) != 2 || len(list(wk["incidents"])) != 1 {
		t.Fatalf("approfondimento della settimana: %d deploy, %d incidenti", len(deploys), len(list(wk["incidents"])))
	}
	failed := obj(deploys[1]) // il più vecchio, in fondo
	kinds := map[string]bool{}
	for _, r := range list(failed["reasons"]) {
		kinds[obj(r)["kind"].(string)] = true
	}
	if failed["version"] != "1.2.0" || failed["failed"] != true || !kinds["incident"] || !kinds["hotfix"] {
		t.Fatalf("v1.2.0 doveva fallire per incidente e hotfix: %v", failed)
	}
	if obj(deploys[0])["failed"] == true {
		t.Fatalf("l'hotfix non è un fallimento: %v", deploys[0])
	}
	if obj(failed)["lead_source"] != "check_run" || !near(failed["lead_days"], 1.0) {
		t.Fatalf("lead time dal controllo del commit: %v", failed)
	}
	last := obj(list(reader.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=application&subject="+ordKey+"&at="+ts(25, 12), nil, http.StatusOK)["deploys"])[1])
	if last["version"] != "1.3.0" || obj(list(last["reasons"])[0])["kind"] != "rollback" {
		t.Fatalf("v1.3.0 doveva fallire per il ritorno alla versione precedente: %v", last)
	}

	// --- Fatti sull'applicazione (ultimi 30 giorni dal 1° dicembre).
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": ordKey,
		"condition": "frequenza_rilascio: >1 change_failure_rate: >30 mttr_ore: 4 lead_time_giorni: <2"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti DORA dell'applicazione: %v", chk["facts"])
	}

	// --- Soglie e finestra con anteprima: abbassando l'elite della frequenza e allargando le parole
	// nulla cambia sul salvato finché non si salva; l'anteprima mostra il nuovo livello.
	cur := admin.do("GET", "/api/v1/devsecops/dora/settings", nil, http.StatusOK)
	th := obj(cur["thresholds"])
	th["deploy_frequency"] = map[string]any{"elite": 1.2, "high": 0.5, "medium": 0.1}
	proposed := map[string]any{"failure_window_hours": 24, "hotfix_words": []string{"hotfix"}, "lead_time_start": "auto", "fact_days": 30, "thresholds": th}
	pv := admin.do("POST", "/api/v1/devsecops/dora/preview", map[string]any{"period": "month", "settings": proposed}, http.StatusOK)
	var tenantRow map[string]any
	for _, r := range list(pv["items"]) {
		if obj(r)["scope"] == "tenant" {
			tenantRow = obj(r)
		}
	}
	if tenantRow == nil || obj(tenantRow["current_levels"])["deploy_frequency"] != "high" || obj(tenantRow["proposed_levels"])["deploy_frequency"] != "elite" {
		t.Fatalf("anteprima: %v", tenantRow)
	}
	// Soglie incoerenti: rifiutate.
	bad := map[string]any{"failure_window_hours": 24, "hotfix_words": []string{"hotfix"}, "lead_time_start": "auto", "fact_days": 30,
		"thresholds": map[string]any{"deploy_frequency": map[string]any{"elite": 1, "high": 2, "medium": 3}}}
	admin.do("PUT", "/api/v1/devsecops/dora/settings", bad, http.StatusUnprocessableEntity)
	reader.do("POST", "/api/v1/devsecops/dora/preview", map[string]any{"settings": proposed}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/devsecops/dora/settings", proposed, http.StatusOK)
	dash = reader.do("GET", "/api/v1/devsecops/dora?period=month", nil, http.StatusOK)
	if got := obj(list(dash["metrics"])[0])["level"]; got != "elite" {
		t.Fatalf("livello dopo il salvataggio: %v", got)
	}

	// --- Un altro tenant non vede nulla.
	if b := adminB.do("GET", "/api/v1/devsecops/dora?period=month", nil, http.StatusOK); b["computed"] != false || fmt.Sprint(obj(list(b["metrics"])[0])["value"]) != "<nil>" {
		t.Fatalf("isolamento: %v", b["metrics"])
	}
}
