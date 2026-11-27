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

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/projects"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Progetti, passo 6 (replica/progetti.md §3, §4, §6): l'applicazione con i progetti che la toccano
// e i conflitti, le dipendenze tra progetti con il semaforo e l'avviso al PM quando la milestone
// slitta, la mappa dei semafori del portafoglio, il work package del fornitore collegato a un
// contratto del registro ICT, il riepilogo degli status report del progetto sulla richiesta.
func TestProjectStep6(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("pr6-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, work.Provision, risk.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Passo 6", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"pm", "editor"}, {"pm2", "editor"}, {"guest", ""}, {"viewer", "viewer"}} {
		var roles []string
		if m.roles != "" {
			roles = strings.Split(m.roles, ",")
		}
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.email,
			Password: testPassword, Roles: roles}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	pm := login(t, srv.URL, "pm@"+slug+".test", testPassword)
	pm2 := login(t, srv.URL, "pm2@"+slug+".test", testPassword)
	guest := login(t, srv.URL, "guest@"+slug+".test", testPassword)
	viewer := login(t, srv.URL, "viewer@"+slug+".test", testPassword)
	run := func(fn func(tx pgx.Tx) error) error { return d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn) }
	sysAdmin := engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}
	role := func(el, key, member string) {
		t.Helper()
		if err := run(func(tx pgx.Tx) error { return engine.SetStakeholder(ctx, tx, sysAdmin, el, key, member, "", false) }); err != nil {
			t.Fatal(err)
		}
	}

	// --- Applicazione e progetti nel tempo: due progetti modificano IDP in periodi sovrapposti.
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome\nIDP,Identity provider\n"}, http.StatusOK)
	var idp, idpKey string
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		if m := a.(map[string]any); m["code"] == "IDP" {
			idp, idpKey = m["id"].(string), m["key"].(string)
		}
	}
	if idp == "" {
		t.Fatal("applicazione IDP non creata")
	}
	pa := pm.do("POST", "/api/v1/projects", map[string]any{"title": "MFA canali", "template": "agile", "no_phases": true}, http.StatusCreated)
	ak, aid := pa["key"].(string), pa["id"].(string)
	wpA := pm.do("POST", "/api/v1/projects/"+ak+"/items", map[string]any{"kind": "work_package", "title": "IDP: secondo fattore", "delivery_mode": "supplier",
		"application_id": idp, "start": "2031-03-03", "end": "2031-03-28"}, http.StatusCreated)
	msA := pm.do("POST", "/api/v1/projects/"+ak+"/items", map[string]any{"kind": "milestone", "title": "IDP pronto", "date": "2031-03-28"}, http.StatusCreated)
	pb := pm2.do("POST", "/api/v1/projects", map[string]any{"title": "Portale clienti", "template": "agile", "no_phases": true}, http.StatusCreated)
	bk, bid := pb["key"].(string), pb["id"].(string)
	wpB := pm2.do("POST", "/api/v1/projects/"+bk+"/items", map[string]any{"kind": "work_package", "title": "Login del portale", "delivery_mode": "agile",
		"application_id": idp, "start": "2031-03-17", "end": "2031-04-11"}, http.StatusCreated)

	apv := admin.do("GET", "/api/v1/projects/by-application?app="+idpKey, nil, http.StatusOK)
	if its := apv["items"].([]any); len(its) != 2 {
		t.Fatalf("work package sull'applicazione: %v", its)
	}
	cs := apv["conflicts"].([]any)
	if len(cs) != 1 || cs[0].(map[string]any)["from"] != "2031-03-17" || cs[0].(map[string]any)["until"] != "2031-03-28" {
		t.Fatalf("conflitti sull'applicazione: %v", cs)
	}
	guest.do("GET", "/api/v1/projects/by-application?app="+idpKey, nil, http.StatusNotFound)
	// L'ospite legge l'applicazione e il progetto B, non il progetto A: il suo work package resta senza titolo.
	role(idp, "sponsor", guest.member)
	role(bid, "sponsor", guest.member)
	gv := guest.do("GET", "/api/v1/projects/by-application?app="+idpKey, nil, http.StatusOK)
	for _, it := range gv["items"].([]any) {
		m := it.(map[string]any)
		if m["id"] == wpA["id"] && (m["project"] != nil || m["work_package"] != nil || m["restricted"] != true || m["start"] != "2031-03-03") {
			t.Fatalf("work package del progetto non leggibile: %v", m)
		}
		if m["id"] == wpB["id"] && m["project"].(map[string]any)["key"] != bk {
			t.Fatalf("work package del progetto leggibile: %v", m)
		}
	}

	// --- Dipendenze tra progetti: il login del portale ha bisogno dell'IDP pronto entro il 31/03.
	plan := pm2.do("POST", "/api/v1/projects/"+bk+"/dependencies", map[string]any{"milestone_id": msA["key"], "item_id": wpB["key"],
		"need_by": "2031-03-31", "note": "serve il secondo fattore"}, http.StatusCreated)
	ext := plan["external"].([]any)
	if len(ext) != 1 || ext[0].(map[string]any)["late_days"] != 0.0 || ext[0].(map[string]any)["milestone_project"].(map[string]any)["key"] != ak {
		t.Fatalf("dipendenza creata: %v", ext)
	}
	depID := ext[0].(map[string]any)["id"].(string)
	pm2.do("POST", "/api/v1/projects/"+bk+"/dependencies", map[string]any{"milestone_id": msA["id"], "item_id": wpB["id"], "need_by": "2031-03-31"}, http.StatusUnprocessableEntity)
	pm.do("POST", "/api/v1/projects/"+ak+"/dependencies", map[string]any{"milestone_id": msA["id"], "need_by": "2031-03-31"}, http.StatusUnprocessableEntity)
	pm2.do("POST", "/api/v1/projects/"+bk+"/dependencies", map[string]any{"milestone_id": msA["id"]}, http.StatusUnprocessableEntity)
	guest.do("POST", "/api/v1/projects/"+bk+"/dependencies", map[string]any{"milestone_id": msA["id"], "need_by": "2031-04-30"}, http.StatusForbidden)
	cands := pm2.do("GET", "/api/v1/projects/"+bk+"/dependency-candidates", nil, http.StatusOK)["items"].([]any)
	if len(cands) != 1 || cands[0].(map[string]any)["milestone"].(map[string]any)["id"] != msA["id"] {
		t.Fatalf("milestone candidate: %v", cands)
	}
	if deps := pm.do("GET", "/api/v1/projects/"+ak+"/plan", nil, http.StatusOK)["dependents"].([]any); len(deps) != 1 ||
		deps[0].(map[string]any)["project"].(map[string]any)["key"] != bk {
		t.Fatalf("dipendenti sul piano del progetto A: %v", deps)
	}
	notices := func() int {
		t.Helper()
		var n int
		if err := run(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "select count(*) from notifications where kind = 'project' and payload->>'event' = 'dependency_late' and member_id = $1", pm2.member).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	numbersB := func() map[string]any {
		return pm2.do("GET", "/api/v1/projects/"+bk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	}
	if n := numbersB(); len(n["external_late"].([]any)) != 0 {
		t.Fatalf("dipendenza in tempo segnata in ritardo: %v", n["external_late"])
	}
	// La milestone slitta al 15/04: oltre la data che serve. Semaforo giallo e un avviso al PM di B.
	pm.do("PUT", "/api/v1/plan-items/"+msA["id"].(string)+"/schedule", map[string]any{"start": "2031-04-08", "end": "2031-04-08"}, http.StatusOK)
	n := numbersB()
	if el := n["external_late"].([]any); len(el) != 1 || el[0].(map[string]any)["late_days"] != 6.0 {
		t.Fatalf("dipendenza in ritardo: %v", n["external_late"])
	}
	if !strings.Contains(strings.Join(toStrings(n["reasons"]), ","), "external_late") || n["signal"] == "green" {
		t.Fatalf("semaforo con la dipendenza in ritardo: %v %v", n["signal"], n["reasons"])
	}
	if c := notices(); c != 1 {
		t.Fatalf("avvisi al PM dopo lo slittamento: %d", c)
	}
	// Uno spostamento che lascia la data uguale non riavvisa; un nuovo slittamento sì.
	pm.do("PUT", "/api/v1/plan-items/"+wpA["id"].(string)+"/schedule", map[string]any{"start": "2031-03-04", "end": "2031-03-28"}, http.StatusOK)
	if c := notices(); c != 1 {
		t.Fatalf("avvisi ripetuti: %d", c)
	}
	pm.do("PUT", "/api/v1/plan-items/"+msA["id"].(string)+"/schedule", map[string]any{"start": "2031-04-10", "end": "2031-04-10"}, http.StatusOK)
	if c := notices(); c != 2 {
		t.Fatalf("avvisi dopo il secondo slittamento: %d", c)
	}
	// L'ospite legge il progetto B ma non A: la milestone resta senza titolo né progetto.
	gp := guest.do("GET", "/api/v1/projects/"+bk+"/plan", nil, http.StatusOK)
	if g := gp["external"].([]any)[0].(map[string]any); g["milestone"] != nil || g["milestone_project"] != nil || g["restricted"] != true || g["milestone_date"] != "2031-04-10" {
		t.Fatalf("dipendenza vista dall'ospite: %v", g)
	}

	// --- Mappa dei semafori del portafoglio.
	pf := admin.do("GET", "/api/v1/projects/portfolio", nil, http.StatusOK)
	var rowB map[string]any
	for _, it := range pf["projects"].([]any) {
		if m := it.(map[string]any); m["key"] == bk {
			rowB = m
		}
	}
	if rowB == nil {
		t.Fatalf("progetto B assente dalla mappa: %v", pf)
	}
	sg := rowB["signals"].(map[string]any)
	if sg["schedule"] != "amber" || sg["conflicts"] != "amber" || sg["scope"] != "none" || len(rowB["external"].([]any)) != 1 {
		t.Fatalf("semafori del progetto B: %v", rowB)
	}
	// Come l'elenco dei progetti, la mappa chiede la lettura degli elementi.
	guest.do("GET", "/api/v1/projects/portfolio", nil, http.StatusForbidden)
	// La dipendenza si toglie: torna verde.
	pm2.do("DELETE", "/api/v1/projects/"+bk+"/dependencies/"+depID, nil, http.StatusOK)
	if n := numbersB(); len(n["external_late"].([]any)) != 0 || strings.Contains(strings.Join(toStrings(n["reasons"]), ","), "external_late") {
		t.Fatalf("dipendenza tolta: %v", n)
	}

	// --- Contratto del fornitore dal registro ICT.
	prov1 := admin.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Fornitore IDP"}, http.StatusCreated)
	prov2 := admin.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Altro fornitore"}, http.StatusCreated)
	pk1 := prov1["key"].(string)
	pk2 := prov2["key"].(string)
	c1 := admin.do("POST", "/api/v1/ict/providers/"+pk1+"/contracts", map[string]any{"reference": "CTR-IDP-1", "annual_cost_cents": 400_000, "currency": "EUR"}, http.StatusCreated)
	c2 := admin.do("POST", "/api/v1/ict/providers/"+pk2+"/contracts", map[string]any{"reference": "CTR-ALTRO"}, http.StatusCreated)
	wpID := wpA["id"].(string)
	if opts := pm.do("GET", "/api/v1/plan-items/"+wpID+"/contracts", nil, http.StatusOK)["items"].([]any); len(opts) != 2 {
		t.Fatalf("contratti collegabili senza fornitore: %v", opts)
	}
	guest.do("GET", "/api/v1/plan-items/"+wpID+"/contracts", nil, http.StatusNotFound)
	sv := pm.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contract_id": c1["id"]}, http.StatusOK)
	con := sv["contract"].(map[string]any)
	if con["ref"] != "CTR-IDP-1" || i64(con["amount_cents"]) != 400_000 || con["own_ref"] != nil || con["linked"].(map[string]any)["reference"] != "CTR-IDP-1" ||
		sv["supplier"] != "Fornitore IDP" {
		t.Fatalf("contratto collegato: %v / %v", sv["contract"], sv["supplier"])
	}
	if opts := pm.do("GET", "/api/v1/plan-items/"+wpID+"/contracts", nil, http.StatusOK)["items"].([]any); len(opts) != 1 {
		t.Fatalf("contratti collegabili del fornitore: %v", opts)
	}
	pm.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contract_id": c2["id"]}, http.StatusUnprocessableEntity)
	// Il SAL a importo si misura sul valore del contratto collegato.
	sv = pm.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"amount_cents": 100_000, "accept": true}, http.StatusCreated)
	if sv["progress"] != 25.0 {
		t.Fatalf("avanzamento dal SAL sul contratto collegato: %v", sv["progress"])
	}
	// I dati scritti sul work package restano e valgono loro; scollegare non li tocca.
	sv = pm.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contract_ref": "ODA-7", "contract_amount_cents": 200_000}, http.StatusOK)
	if con = sv["contract"].(map[string]any); con["ref"] != "ODA-7" || i64(con["amount_cents"]) != 200_000 || con["linked"] == nil || sv["progress"] != 50.0 {
		t.Fatalf("contratto con dati propri: %v %v", con, sv["progress"])
	}
	sv = pm.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contract_id": ""}, http.StatusOK)
	if con = sv["contract"].(map[string]any); con["linked"] != nil || con["ref"] != "ODA-7" {
		t.Fatalf("contratto scollegato: %v", con)
	}

	// --- Riepilogo sulla richiesta confluita: rimanda agli status report del progetto.
	br := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Secondo fattore per i clienti"}, http.StatusCreated)
	rk := br["key"].(string)
	admin.do("POST", "/api/v1/demand/requests/"+rk+"/status-reports", map[string]any{}, http.StatusCreated)
	if err := run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update status_reports set status = 'submitted', submitted_at = now() where element_id = $1", br["id"])
		if err == nil {
			_, err = tx.Exec(ctx, "insert into project_requests (tenant_id, project_id, request_id) values (app_tenant(), $1, $2)", aid, br["id"])
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rl := admin.do("GET", "/api/v1/demand/requests/"+rk+"/status-reports", nil, http.StatusOK)
	if p, _ := rl["project"].(map[string]any); p == nil || p["key"] != ak || p["latest"] != nil || p["submitted"] != 0.0 || len(rl["items"].([]any)) != 1 {
		t.Fatalf("riepilogo senza report del progetto: %v", rl)
	}
	admin.do("POST", "/api/v1/demand/requests/"+rk+"/status-reports", map[string]any{}, http.StatusUnprocessableEntity)
	sr := pm.do("POST", "/api/v1/projects/"+ak+"/status-reports", map[string]any{}, http.StatusCreated)
	pm.do("POST", "/api/v1/project-reports/"+sr["id"].(string)+"/submit", nil, http.StatusOK)
	rl = admin.do("GET", "/api/v1/demand/requests/"+rk+"/status-reports", nil, http.StatusOK)
	if p := rl["project"].(map[string]any); p["submitted"] != 1.0 || p["latest"].(map[string]any)["id"] != sr["id"] {
		t.Fatalf("riepilogo con il report del progetto: %v", p)
	}
	// Chi legge la richiesta ma non il progetto (lo spazio dei progetti diventa riservato) sa solo che è confluita.
	if v := viewer.do("GET", "/api/v1/demand/requests/"+rk+"/status-reports", nil, http.StatusOK)["project"].(map[string]any); v["key"] != ak {
		t.Fatalf("riepilogo per chi legge il progetto: %v", v)
	}
	if err := run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update spaces set settings = settings || '{"access":"restricted"}' where key = 'PRJ'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	gl := viewer.do("GET", "/api/v1/demand/requests/"+rk+"/status-reports", nil, http.StatusOK)
	if p := gl["project"].(map[string]any); p["restricted"] != true || p["key"] != nil || p["latest"] != nil {
		t.Fatalf("riepilogo per chi non legge il progetto: %v", p)
	}
}
