package itest

import (
	"context"
	"log/slog"
	"math"
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
	"regia/internal/modules/projects"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Progetti (replica/progetti.md, passi 1 e 2): la richiesta approvata diventa il progetto con
// gli impatti come work package agili; il progetto da zero ha fasi, work package agili e a
// cascata, milestone e dipendenze fs/ss/ff con ritardo; il work package agile genera l'epic e
// l'avanzamento sale; la Panoramica ha i numeri giusti; il cancello di fase passa dal motore
// delle approvazioni; i tenant esistenti ricevono i progetti con «tenant provision».
func TestProjects(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("prj-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Progetti", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"sponsor", "editor"}, {"lead", "viewer"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: strings.ToUpper(m.email[:1]) + m.email[1:],
			Password: testPassword, Roles: strings.Split(m.roles, ",")}); err != nil {
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
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)

	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome,team\nBE,Backend,Integrazioni\nFE,Frontend,Web\n"}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		apps[m["code"].(string)] = m["id"].(string)
	}
	var webTeam string
	for _, tm := range admin.do("GET", "/api/v1/teams", nil, http.StatusOK)["items"].([]any) {
		if m := tm.(map[string]any); m["name"] == "Web" {
			webTeam = m["id"].(string)
		}
	}

	// --- 1. Dalla richiesta approvata nasce il progetto ------------------------------------
	br := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "MFA canali digitali", "sponsor_id": sponsor.member,
		"regulatory_deadline": "2027-06-30", "estimate_high_cents": 1000000}, http.StatusCreated)
	reqKey := br["key"].(string)
	be := admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/impacts", map[string]any{"application_id": apps["BE"], "estimate_effort_days": 5}, http.StatusCreated)
	fe := admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/impacts", map[string]any{"application_id": apps["FE"], "estimate_effort_days": 10,
		"depends_on": []string{be["key"].(string)}}, http.StatusCreated)
	admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/plan/auto", map[string]any{"from": "2026-12-01"}, http.StatusOK)
	// Prima dell'approvazione non c'è progetto e non si può creare.
	if got := admin.do("GET", "/api/v1/demand/requests/"+reqKey+"/project", nil, http.StatusOK); got["project"] != nil || got["approved"] != false {
		t.Fatalf("progetto prima dell'approvazione: %v", got)
	}
	admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/project", nil, http.StatusUnprocessableEntity)
	for _, to := range []string{"Inviata", "Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
		admin.do("POST", "/api/v1/elements/"+br["id"].(string)+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	inbox := sponsor.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)
	if len(inbox) != 1 {
		t.Fatalf("inbox dello sponsor: %v", inbox)
	}
	sponsor.do("POST", "/api/v1/approvals/steps/"+inbox[0].(map[string]any)["step_id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if s := state(t, admin, reqKey); s != "Pianificazione" {
		t.Fatalf("stato della richiesta: %s", s)
	}
	got := admin.do("GET", "/api/v1/demand/requests/"+reqKey+"/project", nil, http.StatusOK)
	pr, _ := got["project"].(map[string]any)
	if pr == nil {
		t.Fatalf("la richiesta approvata non è diventata un progetto: %v", got)
	}
	projKey := pr["key"].(string)
	// «Crea il progetto» su una richiesta che ce l'ha già restituisce lo stesso.
	again := admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/project", nil, http.StatusOK)
	if again["created"] != false || again["project"].(map[string]any)["key"] != projKey {
		t.Fatalf("seconda creazione: %v", again)
	}
	plan := admin.do("GET", "/api/v1/projects/"+projKey+"/plan", nil, http.StatusOK)
	if n := len(plan["phases"].([]any)); n != 4 {
		t.Fatalf("fasi del modello misto: %d", n)
	}
	build := plan["phases"].([]any)[1].(map[string]any)
	items := map[string]map[string]any{}
	for _, it := range plan["items"].([]any) {
		m := it.(map[string]any)
		items[m["title"].(string)] = m
	}
	wpBE, wpFE := items["Backend"], items["Frontend"]
	if wpBE == nil || wpFE == nil || len(items) != 2 {
		t.Fatalf("work package dagli impatti: %v", plan["items"])
	}
	if wpFE["delivery_mode"] != "agile" || wpFE["team"] != "Web" || wpFE["phase_id"] != build["id"] || wpFE["impact"].(map[string]any)["key"] != fe["key"] {
		t.Fatalf("work package del frontend: %v", wpFE)
	}
	if wpBE["start"] != "2026-12-01" || wpBE["end"] != "2026-12-07" || wpFE["start"] != "2026-12-09" {
		t.Fatalf("date dagli impatti: %v %v → %v", wpBE["start"], wpBE["end"], wpFE["start"])
	}
	deps := wpFE["deps"].([]any)
	if len(deps) != 1 || deps[0].(map[string]any)["id"] != wpBE["id"] || deps[0].(map[string]any)["kind"] != "fs" {
		t.Fatalf("dipendenze dagli impatti: %v", deps)
	}
	if p := plan["project"].(map[string]any); p["target"] != "2027-06-30" || p["target_kind"] != "regulatory" || len(p["requests"].([]any)) != 1 {
		t.Fatalf("testa del progetto: %v", p)
	}
	// Lo sponsor della richiesta è lo sponsor del progetto (ruolo ereditato).
	pd := admin.do("GET", "/api/v1/elements/"+projKey, nil, http.StatusOK)
	inherited := false
	for _, sh := range pd["stakeholders"].([]any) {
		if m := sh.(map[string]any); m["role"] == "sponsor" && m["member_id"] == sponsor.member {
			inherited = true
		}
	}
	if !inherited {
		t.Fatalf("sponsor non ereditato: %v", pd["stakeholders"])
	}

	// --- 2. Work package agile → epic sulla board del team → l'avanzamento sale ------------
	ep := admin.do("POST", "/api/v1/plan-items/"+wpFE["id"].(string)+"/epic", nil, http.StatusOK)
	space := ep["space_key"].(string)
	for i, title := range []string{"Login con il secondo fattore", "Recupero del dispositivo"} {
		st := admin.do("POST", "/api/v1/work/spaces/"+space+"/items", map[string]any{"type": "story", "title": title, "parent": ep["key"], "assignee_id": admin.member}, http.StatusCreated)
		if i == 0 {
			admin.do("POST", "/api/v1/elements/"+st["id"].(string)+"/transitions", map[string]any{"to": "In corso"}, http.StatusOK)
			admin.do("POST", "/api/v1/elements/"+st["id"].(string)+"/transitions", map[string]any{"to": "Fatto"}, http.StatusOK)
		}
	}
	view := admin.do("GET", "/api/v1/plan-items/"+wpFE["id"].(string), nil, http.StatusOK)
	if view["progress"] != 50.0 || view["progress_source"] != "epics" || len(view["epics"].([]any)) != 1 {
		t.Fatalf("avanzamento dall'epic: %v %v %v", view["progress"], view["progress_source"], view["epics"])
	}
	// Progetto: 50% sul frontend (10 giorni) e 0% sul backend (5 giorni) → 33,3%.
	ov := admin.do("GET", "/api/v1/projects/"+projKey+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["progress"] != 33.3 || ov["work_packages"] != 2.0 {
		t.Fatalf("avanzamento del progetto: %v", ov)
	}

	// --- 3. Progetto da zero: fasi, work package agili e a cascata, milestone, dipendenze ---
	hl := admin.do("POST", "/api/v1/projects", map[string]any{"title": "Home lab", "template": "mixed"}, http.StatusCreated)
	hk := hl["key"].(string)
	hplan := admin.do("GET", "/api/v1/projects/"+hk+"/plan", nil, http.StatusOK)
	phases := hplan["phases"].([]any)
	analysis, realization := phases[0].(map[string]any)["id"].(string), phases[1].(map[string]any)["id"].(string)
	item := func(body map[string]any) map[string]any {
		t.Helper()
		return admin.do("POST", "/api/v1/projects/"+hk+"/items", body, http.StatusCreated)
	}
	design := item(map[string]any{"kind": "work_package", "title": "Analisi e disegno", "delivery_mode": "waterfall", "phase_id": analysis,
		"estimate_effort_days": 8, "start": "2027-01-04", "end": "2027-01-15"})
	req1 := item(map[string]any{"kind": "plan_activity", "title": "Requisiti", "parent_id": design["key"], "estimate_effort_days": 2})
	tech := item(map[string]any{"kind": "plan_activity", "title": "Disegno tecnico", "parent_id": design["id"], "estimate_effort_days": 6})
	done := item(map[string]any{"kind": "milestone", "title": "Disegno approvato", "phase_id": analysis, "date": "2027-01-15",
		"depends_on": []map[string]any{{"ref": design["key"], "kind": "fs"}}})
	app := item(map[string]any{"kind": "work_package", "title": "App", "delivery_mode": "agile", "team_id": webTeam, "phase_id": realization,
		"estimate_effort_days": 8, "start": "2027-01-18", "end": "2027-01-29", "depends_on": []map[string]any{{"ref": done["key"], "kind": "fs", "lag": 0}}})
	idp := item(map[string]any{"kind": "work_package", "title": "IDP", "delivery_mode": "supplier", "phase_id": realization,
		"estimate_effort_days": 4, "start": "2027-01-18", "end": "2027-01-22", "depends_on": []map[string]any{{"ref": app["key"], "kind": "ss", "lag": 2}}})
	if app["phase_id"] != realization || app["team"] != "Web" || idp["delivery_mode"] != "supplier" {
		t.Fatalf("work package creati: %v %v", app, idp)
	}
	// ss con ritardo: l'IDP non può iniziare prima di due giorni lavorativi dopo l'App → 20/01.
	ch := admin.do("PUT", "/api/v1/plan-items/"+app["id"].(string)+"/schedule", map[string]any{"start": "2027-01-18", "end": "2027-01-29"}, http.StatusOK)["changes"].([]any)
	if len(ch) != 2 || ch[1].(map[string]any)["start"] != "2027-01-20" || ch[1].(map[string]any)["end"] != "2027-01-26" {
		t.Fatalf("cascata inizio→inizio con ritardo: %v", ch)
	}
	// Il disegno slitta di una settimana: milestone e App slittano a catena, l'IDP con loro.
	ch = admin.do("PUT", "/api/v1/plan-items/"+design["id"].(string)+"/schedule", map[string]any{"start": "2027-01-11", "end": "2027-01-22"}, http.StatusOK)["changes"].([]any)
	got2 := map[string][2]string{}
	for _, c := range ch {
		m := c.(map[string]any)
		got2[m["key"].(string)] = [2]string{m["start"].(string), m["end"].(string)}
	}
	if got2[done["key"].(string)] != [2]string{"2027-01-22", "2027-01-22"} || got2[app["key"].(string)] != [2]string{"2027-01-25", "2027-02-05"} ||
		got2[idp["key"].(string)] != [2]string{"2027-01-27", "2027-02-02"} {
		t.Fatalf("cascata dal disegno: %v", got2)
	}
	// Fine→fine: la milestone «Collaudo» non chiude prima della fine dell'App.
	coll := item(map[string]any{"kind": "milestone", "title": "Fine collaudo", "date": "2027-01-29"})
	admin.do("PUT", "/api/v1/plan-items/"+coll["id"].(string)+"/dependencies", map[string]any{"depends_on": []map[string]any{{"ref": app["id"], "kind": "ff", "lag": 1}}}, http.StatusOK)
	ch = admin.do("PUT", "/api/v1/plan-items/"+app["id"].(string)+"/schedule", map[string]any{"start": "2027-01-25", "end": "2027-02-05"}, http.StatusOK)["changes"].([]any)
	ok := false
	for _, c := range ch {
		if m := c.(map[string]any); m["key"] == coll["key"] && m["start"] == "2027-02-08" && m["end"] == "2027-02-08" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("cascata fine→fine: %v", ch)
	}
	// Un ciclo si rifiuta.
	admin.do("PUT", "/api/v1/plan-items/"+design["id"].(string)+"/dependencies", map[string]any{"depends_on": []map[string]any{{"ref": idp["key"]}}}, http.StatusUnprocessableEntity)

	// Cascata: le attività dichiarano l'avanzamento, storicizzato; il work package lo pesa sulle stime.
	admin.do("POST", "/api/v1/plan-items/"+req1["id"].(string)+"/progress", map[string]any{"percent": 50}, http.StatusOK)
	admin.do("POST", "/api/v1/plan-items/"+req1["id"].(string)+"/progress", map[string]any{"percent": 100, "note": "fatto"}, http.StatusOK)
	admin.do("POST", "/api/v1/plan-items/"+tech["id"].(string)+"/progress", map[string]any{"percent": 50}, http.StatusOK)
	if h := admin.do("GET", "/api/v1/plan-items/"+req1["id"].(string)+"/progress", nil, http.StatusOK)["items"].([]any); len(h) != 2 || h[0].(map[string]any)["percent"] != 100.0 {
		t.Fatalf("storico dell'avanzamento: %v", h)
	}
	dv := admin.do("GET", "/api/v1/plan-items/"+design["id"].(string), nil, http.StatusOK)
	if dv["progress"] != 62.5 || dv["progress_source"] != "activities" {
		t.Fatalf("avanzamento a cascata: %v %v", dv["progress"], dv["progress_source"])
	}
	// Fornitore: avanzamento dichiarato dal PM.
	admin.do("POST", "/api/v1/plan-items/"+idp["id"].(string)+"/progress", map[string]any{"percent": 25}, http.StatusOK)
	// La milestone non avanza: si raggiunge.
	admin.do("POST", "/api/v1/plan-items/"+done["id"].(string)+"/progress", map[string]any{"percent": 25}, http.StatusUnprocessableEntity)

	// Agile da zero: il team accetta, la regola crea l'epic nello spazio del team, figlio del work package.
	admin.do("POST", "/api/v1/elements/"+app["id"].(string)+"/transitions", map[string]any{"to": "Proposto"}, http.StatusOK)
	lead.do("POST", "/api/v1/elements/"+app["id"].(string)+"/transitions", map[string]any{"to": "Accettato"}, http.StatusOK)
	av := admin.do("GET", "/api/v1/plan-items/"+app["id"].(string), nil, http.StatusOK)
	if len(av["epics"].([]any)) != 1 {
		t.Fatalf("epic del work package agile: %v", av["epics"])
	}
	epic := admin.do("GET", "/api/v1/elements/"+av["epics"].([]any)[0].(map[string]any)["key"].(string), nil, http.StatusOK)["element"].(map[string]any)
	if epic["parent_id"] != app["id"] || epic["space_key"] != space {
		t.Fatalf("epic: genitore %v spazio %v (atteso %s)", epic["parent_id"], epic["space_key"], space)
	}
	// Panoramica: 62,5% (8 gg) + 0% (8 gg, epic senza story) + 25% (4 gg) = 30%; milestone; prossima milestone.
	hov := admin.do("GET", "/api/v1/projects/"+hk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if math.Abs(hov["progress"].(float64)-30) > 0.001 || hov["work_packages"] != 3.0 || hov["milestones"] != 2.0 {
		t.Fatalf("Panoramica: %v", hov)
	}
	if nm := hov["next_milestone"].(map[string]any); nm["key"] != done["key"] {
		t.Fatalf("prossima milestone: %v", nm)
	}
	if len(hov["by_mode"].([]any)) != 3 {
		t.Fatalf("per modo di consegna: %v", hov["by_mode"])
	}

	// --- 4. Cancello di fase: il work package della fase dopo non parte senza approvazione --
	admin.do("PATCH", "/api/v1/plan-phases/"+analysis, map[string]any{"gate_enabled": true}, http.StatusOK)
	idpMove := admin.do("POST", "/api/v1/elements/"+idp["id"].(string)+"/transitions", map[string]any{"to": "In corso"}, http.StatusUnprocessableEntity)
	_ = idpMove
	// Serve lo sponsor del progetto: senza, l'approvazione non si apre.
	admin.do("POST", "/api/v1/plan-phases/"+analysis+"/gate", nil, http.StatusUnprocessableEntity)
	run := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
	}
	run(func(tx pgx.Tx) error {
		return engine.SetStakeholder(ctx, tx, engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}, hl["id"].(string), "sponsor", sponsor.member, "", false)
	})
	gate := admin.do("POST", "/api/v1/plan-phases/"+analysis+"/gate", nil, http.StatusOK)
	if gate["gate_status"] != "pending" {
		t.Fatalf("cancello: %v", gate)
	}
	for _, it := range sponsor.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
		if m := it.(map[string]any); m["element_key"] == hk {
			sponsor.do("POST", "/api/v1/approvals/steps/"+m["step_id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
		}
	}
	hplan = admin.do("GET", "/api/v1/projects/"+hk+"/plan", nil, http.StatusOK)
	if st := hplan["phases"].([]any)[0].(map[string]any)["gate_status"]; st != "approved" {
		t.Fatalf("cancello dopo l'approvazione: %v", st)
	}
	admin.do("POST", "/api/v1/elements/"+idp["id"].(string)+"/transitions", map[string]any{"to": "In corso"}, http.StatusOK)

	// --- 5. Tenant esistente: «tenant provision» porta le richieste approvate nei progetti ---
	admin.do("PUT", "/api/v1/projects/settings", map[string]any{"auto_from_request": false}, http.StatusOK)
	br2 := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta di prima", "sponsor_id": sponsor.member, "estimate_high_cents": 1000000}, http.StatusCreated)
	admin.do("POST", "/api/v1/demand/requests/"+br2["key"].(string)+"/impacts", map[string]any{"application_id": apps["BE"], "estimate_effort_days": 3}, http.StatusCreated)
	for _, to := range []string{"Inviata", "Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
		admin.do("POST", "/api/v1/elements/"+br2["id"].(string)+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	for _, it := range sponsor.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
		if m := it.(map[string]any); m["element_key"] == br2["key"] {
			sponsor.do("POST", "/api/v1/approvals/steps/"+m["step_id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
		}
	}
	if got := admin.do("GET", "/api/v1/demand/requests/"+br2["key"].(string)+"/project", nil, http.StatusOK); got["project"] != nil || got["approved"] != true {
		t.Fatalf("con la nascita automatica spenta: %v", got)
	}
	if err := tenant.Provision(ctx, d, slug, prov); err != nil {
		t.Fatal(err)
	}
	got = admin.do("GET", "/api/v1/demand/requests/"+br2["key"].(string)+"/project", nil, http.StatusOK)
	if got["project"] == nil {
		t.Fatalf("la migrazione non ha creato il progetto: %v", got)
	}
	mp := admin.do("GET", "/api/v1/projects/"+got["project"].(map[string]any)["key"].(string)+"/plan", nil, http.StatusOK)
	if its := mp["items"].([]any); len(its) != 1 || its[0].(map[string]any)["application"] != "Backend" {
		t.Fatalf("work package della migrazione: %v", its)
	}
	// Il primo progetto non si duplica.
	list := admin.do("GET", "/api/v1/projects", nil, http.StatusOK)["items"].([]any)
	if len(list) != 3 {
		t.Fatalf("progetti dopo la migrazione: %d", len(list))
	}
	// Permessi: il referente del team (sola lettura) non modifica il piano.
	lead.do("POST", "/api/v1/projects/"+hk+"/items", map[string]any{"kind": "milestone", "title": "X"}, http.StatusForbidden)
}
