package itest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"regia/internal/modules/portfolio"
	"regia/internal/modules/report"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Avanzamento (piano, passi 13 e 14): status report precompilati dai dati, storico e
// confronto, promemoria; cruscotto di portafoglio coerente con le query di destinazione;
// riservatezza dei dati finanziari; portale del richiedente; isolamento tra tenant.
func TestReportDashboardPortal(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, other := "rep-"+stamp, "rpo-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision, reviews.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Avanzamento", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	members := map[string]string{}
	for _, m := range []struct {
		email, typ string
		roles      []string
	}{
		{"req", "requester", []string{"requester"}}, {"curioso", "requester", []string{"requester"}},
		{"pm", "", []string{"viewer"}}, {"sponsor", "", []string{"viewer"}}, {"fin", "", []string{"editor", "finance"}},
		{"lettore", "", []string{"viewer"}},
	} {
		id, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.email,
			Password: testPassword, MemberType: m.typ, Roles: m.roles})
		if err != nil {
			t.Fatal(err)
		}
		members[m.email] = id
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rep := report.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Report: rep,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)
	curioso := login(t, srv.URL, "curioso@"+slug+".test", testPassword)
	pm := login(t, srv.URL, "pm@"+slug+".test", testPassword)
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	fin := login(t, srv.URL, "fin@"+slug+".test", testPassword)
	lettore := login(t, srv.URL, "lettore@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	num := func(v any) float64 { f, _ := v.(float64); return f }
	today := time.Now()
	dayOff := func(n int) string { return today.AddDate(0, 0, n).Format(time.DateOnly) }

	// Quante voci restituisce una query (la «lista filtrata» dietro a ogni numero).
	count := func(u *user, q string) int {
		t.Helper()
		n, cursor := 0, ""
		for {
			p := "/api/v1/elements?limit=200&q=" + url.QueryEscape(q)
			if cursor != "" {
				p += "&cursor=" + url.QueryEscape(cursor)
			}
			page := u.do("GET", p, nil, http.StatusOK)
			n += len(list(page["items"]))
			c, _ := page["next"].(string)
			if c == "" {
				return n
			}
			cursor = c
		}
	}
	coherent := func(u *user, what string, v any) {
		t.Helper()
		m := obj(v)
		q, _ := m["query"].(string)
		if q == "" {
			t.Fatalf("%s: manca la query: %v", what, m)
		}
		if got := count(u, q); got != int(num(m["count"])) {
			t.Fatalf("%s: il cruscotto dice %v, la query «%s» trova %d", what, m["count"], q, got)
		}
	}
	setRole := func(key, role, member string) {
		t.Helper()
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			el, err := eng.Get(ctx, tx, engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}, key)
			if err != nil {
				return err
			}
			return engine.SetStakeholder(ctx, tx, engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}, el.ID, role, member, "", false)
		}); err != nil {
			t.Fatal(err)
		}
	}

	// -----------------------------------------------------------------
	// Dati: unità, tre richieste in fasi diverse, impatti pianificati nel passato.
	// -----------------------------------------------------------------
	uComp := admin.do("POST", "/api/v1/org-units", map[string]any{"name": "Compliance"}, http.StatusCreated)["id"].(string)
	uComm := admin.do("POST", "/api/v1/org-units", map[string]any{"name": "Commerciale"}, http.StatusCreated)["id"].(string)
	ids := map[string]string{}
	newReq := func(title, unit, driver string, mandatory bool, deadline, desired string) string {
		body := map[string]any{"title": title, "sponsor_id": members["sponsor"], "requesting_unit_id": unit, "driver": driver, "is_mandatory": mandatory}
		if deadline != "" {
			body["regulatory_deadline"] = deadline
		}
		if desired != "" {
			body["desired_date"] = desired
		}
		r := admin.do("POST", "/api/v1/demand/requests", body, http.StatusCreated)
		ids[r["key"].(string)] = r["id"].(string)
		return r["key"].(string)
	}
	move := func(key string, phases ...string) {
		for _, p := range phases {
			admin.do("POST", "/api/v1/elements/"+ids[key]+"/transitions", map[string]any{"to": p}, http.StatusOK)
		}
	}
	dora := newReq("Adeguamento DORA: registro dei fornitori", uComp, "regulatory", true, dayOff(-10), dayOff(-20))
	b2b := newReq("Portale clienti B2B", uComm, "strategic", false, "", dayOff(200))
	nis2 := newReq("Notifica degli incidenti NIS2", uComp, "regulatory", true, dayOff(30), "")
	move(dora, "Inviata", "Triage", "Valutazione")
	move(nis2, "Inviata", "Triage")
	move(b2b, "Inviata")

	csv := "codice,nome,team\nBE,Backend,Integrazioni\nFE,Frontend,Web\nRP,Report,Dati\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)["id"].(string)
	}
	be := admin.do("POST", "/api/v1/demand/requests/"+dora+"/impacts", map[string]any{"application_id": apps["BE"], "estimate_effort_days": 5}, http.StatusCreated)
	fe := admin.do("POST", "/api/v1/demand/requests/"+dora+"/impacts", map[string]any{"application_id": apps["FE"], "estimate_effort_days": 10,
		"depends_on": []string{be["key"].(string)}}, http.StatusCreated)
	admin.do("POST", "/api/v1/demand/requests/"+dora+"/impacts", map[string]any{"application_id": apps["RP"]}, http.StatusCreated)
	admin.do("POST", "/api/v1/demand/requests/"+dora+"/plan/auto", map[string]any{"from": dayOff(-90)}, http.StatusOK)

	// Valutazione obbligatoria in ritardo sulla richiesta DORA (scadenza dello SLA passata).
	rv := admin.do("POST", "/api/v1/elements/"+dora+"/reviews", map[string]any{"type": "dpia_preliminare"}, http.StatusCreated)
	if tag, err := d.Owner.Exec(ctx, `update ext_review set stage = 'submitted', mandatory = true, due_on = current_date - 10
		where tenant_id = $1 and element_id = (select e.id from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where e.tenant_id = $1 and s.key || '-' || e.number = $2)`, ten.TenantID, obj(rv["element"])["key"]); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("valutazione in ritardo: %v %v %v", err, tag, rv)
	}

	// Budget approvato (40 k€) e un costo sulla richiesta DORA.
	ov := fin.do("GET", "/api/v1/finance/overview", nil, http.StatusOK)
	cats := map[string]string{}
	for _, c := range list(ov["categories"]) {
		cats[obj(c)["code"].(string)] = obj(c)["id"].(string)
	}
	year := today.Year()
	fy := fin.do("POST", "/api/v1/finance/fiscal-years", map[string]any{"name": "FY", "start_date": time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly),
		"end_date": time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)}, http.StatusCreated)
	bud := fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": dora, "fiscal_year_id": fy["id"],
		"lines": []map[string]any{{"cost_category_id": cats["CONS"], "amount_cents": 4_000_000}}}, http.StatusCreated)
	fin.do("POST", "/api/v1/finance/budgets/"+bud["id"].(string)+"/submit", nil, http.StatusOK)
	for _, rq := range list(sponsor.do("GET", "/api/v1/elements/"+dora, nil, http.StatusOK)["approvals"]) {
		for _, st := range list(obj(rq)["steps"]) {
			if obj(st)["can_decide"] == true {
				sponsor.do("POST", "/api/v1/approvals/steps/"+obj(st)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
			}
		}
	}
	addCost := func(key string, cents int64) {
		fin.do("POST", "/api/v1/finance/costs", map[string]any{"element": key, "cost_category_id": cats["CONS"], "entry_date": dayOff(-3),
			"amount_cents": cents, "description": "Consulenza"}, http.StatusCreated)
	}
	addCost(dora, 1_000_000)

	// -----------------------------------------------------------------
	// Status report: precompilazione, permessi, bozza, invio, storico, confronto.
	// -----------------------------------------------------------------
	setRole(dora, "project_manager", members["pm"])
	pre := pm.do("GET", "/api/v1/demand/requests/"+dora+"/status-reports/prefill?period_end="+dayOff(-7), nil, http.StatusOK)
	data := obj(pre["data"])
	sch, cost := obj(data["schedule"]), obj(data["cost"])
	if num(sch["tasks"]) != 3 || num(sch["tasks_late"]) != 3 || num(sch["planned_pct"]) != 100 || num(sch["actual_pct"]) != 0 {
		t.Fatalf("avanzamento dal Gantt: %v", sch)
	}
	tot := admin.do("GET", "/api/v1/demand/requests/"+dora+"/finance", nil, http.StatusOK)
	if num(cost["approved_cents"]) != 4_000_000 || num(cost["actual_cents"]) != num(tot["actual_cents"]) || num(cost["actual_cents"]) != 1_000_000 {
		t.Fatalf("costi precompilati: %v (totali %v)", cost, tot)
	}
	if len(list(data["reviews_late"])) != 1 || !obj(list(data["reviews_late"])[0])["mandatory"].(bool) {
		t.Fatalf("valutazioni in ritardo: %v", data["reviews_late"])
	}
	blocked := list(data["impacts_blocked"])
	if len(blocked) != 1 || obj(blocked[0])["key"] != fe["key"] || !strings.Contains(obj(blocked[0])["reason"].(string), be["key"].(string)) {
		t.Fatalf("impatti bloccati: %v", blocked)
	}
	sug := obj(data["suggested"])
	if sug["schedule"] != "red" || sug["risk"] != "red" || sug["overall"] != "red" || sug["cost"] != "green" {
		t.Fatalf("semafori proposti: %v", sug)
	}
	if !strings.Contains(obj(pre["sections"])["risks"].(string), "in ritardo") {
		t.Fatalf("rischi precompilati: %v", pre["sections"])
	}
	lettore.do("POST", "/api/v1/demand/requests/"+dora+"/status-reports", map[string]any{}, http.StatusForbidden)
	req.do("GET", "/api/v1/demand/requests/"+dora+"/status-reports", nil, http.StatusNotFound)
	r1 := pm.do("POST", "/api/v1/demand/requests/"+dora+"/status-reports", map[string]any{"period_end": dayOff(-7)}, http.StatusCreated)
	if r1["status"] != "draft" || r1["overall"] != "red" || !r1["can_edit"].(bool) {
		t.Fatalf("bozza: %v", r1)
	}
	id1 := r1["id"].(string)
	pm.do("POST", "/api/v1/demand/requests/"+dora+"/status-reports", map[string]any{}, http.StatusUnprocessableEntity) // c'è già una bozza
	lettore.do("GET", "/api/v1/status-reports/"+id1, nil, http.StatusNotFound)                                         // le bozze non si vedono
	sec := obj(r1["sections"])
	sec["summary"] = ""
	r1 = pm.do("PATCH", "/api/v1/status-reports/"+id1, map[string]any{"version": r1["version"], "sections": sec}, http.StatusOK)
	pm.do("POST", "/api/v1/status-reports/"+id1+"/submit", nil, http.StatusUnprocessableEntity) // sintesi vuota
	pm.do("PATCH", "/api/v1/status-reports/"+id1, map[string]any{"version": 1, "overall": "amber"}, http.StatusConflict)
	pm.do("PATCH", "/api/v1/status-reports/"+id1, map[string]any{"version": r1["version"], "overall": "viola"}, http.StatusUnprocessableEntity)
	sec["summary"] = "Ritardo sul backend: recupero previsto entro fine mese."
	sec["decisions"] = "Approvare una consulenza aggiuntiva."
	r1 = pm.do("PATCH", "/api/v1/status-reports/"+id1, map[string]any{"version": r1["version"], "overall": "amber", "cost": "amber", "sections": sec}, http.StatusOK)
	if r1["overall"] != "amber" || r1["cost"] != "amber" || r1["schedule"] != "red" {
		t.Fatalf("semafori corretti a mano: %v", r1)
	}
	r1 = pm.do("POST", "/api/v1/status-reports/"+id1+"/submit", nil, http.StatusOK)
	if r1["status"] != "submitted" || r1["can_edit"].(bool) {
		t.Fatalf("invio: %v", r1)
	}
	pm.do("PATCH", "/api/v1/status-reports/"+id1, map[string]any{"version": r1["version"], "overall": "green"}, http.StatusUnprocessableEntity)
	if _, err := d.Owner.Exec(ctx, `update status_reports set overall = 'green' where id = $1`, id1); err == nil || !strings.Contains(err.Error(), "inviato") {
		t.Fatalf("un report inviato è immutabile anche nel database: %v", err)
	}
	ls := lettore.do("GET", "/api/v1/demand/requests/"+dora+"/status-reports", nil, http.StatusOK)
	if len(list(ls["items"])) != 1 || obj(ls["schedule"])["next_due"] != dayOff(7) || obj(ls["schedule"])["last_overall"] != "amber" {
		t.Fatalf("storico e periodicità: %v", ls)
	}
	// Chi segue la richiesta (lo sponsor) riceve la notifica dell'invio.
	found := false
	for _, n := range list(sponsor.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"]) {
		if obj(n)["kind"] == "report" && obj(obj(n)["payload"])["event"] == "submitted" {
			found = true
		}
	}
	if !found {
		t.Fatal("lo sponsor non ha la notifica del report")
	}
	lettore.do("POST", "/api/v1/status-reports/"+id1+"/acknowledge", nil, http.StatusForbidden)
	pm.do("POST", "/api/v1/status-reports/"+id1+"/acknowledge", nil, http.StatusForbidden) // l'autore non prende visione
	if a := sponsor.do("POST", "/api/v1/status-reports/"+id1+"/acknowledge", nil, http.StatusOK); a["acknowledged_by"] != members["sponsor"] {
		t.Fatalf("presa visione: %v", a)
	}
	sponsor.do("POST", "/api/v1/status-reports/"+id1+"/acknowledge", nil, http.StatusUnprocessableEntity)

	// Nuovi costi: il secondo report li vede, il primo conserva la sua fotografia.
	addCost(dora, 500_000)
	r2 := pm.do("POST", "/api/v1/demand/requests/"+dora+"/status-reports", map[string]any{}, http.StatusCreated)
	if r2["prev_id"] != id1 || r2["period_start"] != dayOff(-6) {
		t.Fatalf("secondo report: precedente %v, inizio %v", r2["prev_id"], r2["period_start"])
	}
	if c := num(obj(obj(pm.do("GET", "/api/v1/status-reports/"+id1, nil, http.StatusOK)["data"])["cost"])["actual_cents"]); c != 1_000_000 {
		t.Fatalf("la fotografia del primo report è cambiata: %v", c)
	}
	sec2 := obj(r2["sections"])
	sec2["summary"] = "Backend in recupero."
	r2 = pm.do("PATCH", "/api/v1/status-reports/"+r2["id"].(string), map[string]any{"version": r2["version"], "sections": sec2}, http.StatusOK)
	pm.do("POST", "/api/v1/status-reports/"+r2["id"].(string)+"/submit", nil, http.StatusOK)
	cmp := lettore.do("GET", "/api/v1/status-reports/"+r2["id"].(string)+"/compare", nil, http.StatusOK)
	changes := map[string]map[string]any{}
	for _, c := range list(cmp["changes"]) {
		changes[obj(c)["field"].(string)] = obj(c)
	}
	if obj(cmp["from"])["id"] != id1 || changes["overall"]["from"] != "amber" || changes["overall"]["to"] != "red" || changes["overall"]["trend"] != "worse" ||
		num(changes["actual_cents"]["to"]) != 1_500_000 || changes["actual_cents"]["trend"] != "worse" {
		t.Fatalf("confronto: %v", changes)
	}
	// Export in Markdown.
	mdReq, _ := http.NewRequest("GET", srv.URL+"/api/v1/status-reports/"+id1+"/export.md", nil)
	mdResp, err := lettore.c.Do(mdReq)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := io.ReadAll(mdResp.Body)
	mdResp.Body.Close()
	if mdResp.StatusCode != http.StatusOK || !strings.HasPrefix(string(md), "# Status report "+dora) || !strings.Contains(string(md), "Approvare una consulenza") ||
		!strings.Contains(mdResp.Header.Get("Content-Type"), "text/markdown") {
		t.Fatalf("export markdown: %d %s", mdResp.StatusCode, md)
	}

	// Promemoria al PM: le richieste nelle fasi attese ricevono la periodicità; alla
	// scadenza parte un promemoria, una volta sola.
	admin.do("PUT", "/api/v1/report/settings", map[string]any{"phases": []string{"Triage"}, "reminder_days": 1}, http.StatusOK)
	lettore.do("PUT", "/api/v1/report/settings", map[string]any{"reminder_days": 3}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/report/settings", map[string]any{"phases": []string{"Inesistente"}}, http.StatusUnprocessableEntity)
	setRole(nis2, "project_manager", members["pm"])
	remind := func() int {
		var n int
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			n, err = rep.CheckRemindersTenant(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := remind(); n != 0 {
		t.Fatalf("promemoria prima della scadenza: %d", n)
	}
	if s := obj(admin.do("GET", "/api/v1/demand/requests/"+nis2+"/status-reports", nil, http.StatusOK)["schedule"]); s["next_due"] != dayOff(14) || !s["expected"].(bool) {
		t.Fatalf("periodicità creata dal job: %v", s)
	}
	admin.do("PUT", "/api/v1/demand/requests/"+nis2+"/status-reports/schedule", map[string]any{"frequency": "weekly", "next_due": dayOff(-1)}, http.StatusOK)
	if n := remind(); n != 1 {
		t.Fatalf("promemoria alla scadenza: %d", n)
	}
	if n := remind(); n != 0 {
		t.Fatalf("promemoria ripetuto: %d", n)
	}
	found = false
	for _, n := range list(pm.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"]) {
		if obj(n)["kind"] == "report" && obj(obj(n)["payload"])["event"] == "overdue" && obj(n)["element_key"] == nis2 {
			found = true
		}
	}
	if !found {
		t.Fatal("il PM non ha il promemoria")
	}

	// -----------------------------------------------------------------
	// Cruscotto: ogni numero coincide con la sua query.
	// -----------------------------------------------------------------
	dash := admin.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusOK)
	if num(obj(dash["total"])["count"]) != 3 || num(obj(dash["open"])["count"]) != 3 {
		t.Fatalf("totali: %v %v", dash["total"], dash["open"])
	}
	for _, k := range []string{"total", "open", "late_desired", "late_deadline", "deadline_soon", "mandatory", "reviews_late_total"} {
		coherent(admin, k, dash[k])
	}
	if num(obj(dash["late_deadline"])["count"]) != 1 || num(obj(dash["late_desired"])["count"]) != 1 || num(obj(dash["deadline_soon"])["count"]) != 1 ||
		num(obj(dash["mandatory"])["count"]) != 2 || num(obj(dash["reviews_late_total"])["count"]) != 1 {
		t.Fatalf("ritardi e obblighi: %v %v %v %v", dash["late_deadline"], dash["late_desired"], dash["deadline_soon"], dash["mandatory"])
	}
	phases := map[string]map[string]any{}
	for _, p := range list(dash["phases"]) {
		phases[obj(p)["name"].(string)] = obj(p)
		coherent(admin, "fase "+obj(p)["name"].(string), p)
	}
	if num(phases["Valutazione"]["count"]) != 1 || num(phases["Triage"]["count"]) != 1 || num(phases["Inviata"]["count"]) != 1 ||
		num(phases["Bozza"]["stays"]) != 3 || phases["Bozza"]["avg_days"] == nil || num(phases["Triage"]["stays"]) != 1 {
		t.Fatalf("fasi e tempi di attraversamento: %v", phases)
	}
	for _, k := range []string{"by_driver", "by_unit", "reviews_late"} {
		for _, it := range list(dash[k]) {
			coherent(admin, fmt.Sprint(k, " ", obj(it)["label"], obj(it)["team"]), it)
		}
	}
	if len(list(dash["by_unit"])) != 2 || len(list(dash["reviews_late"])) != 1 {
		t.Fatalf("distribuzioni: %v %v", dash["by_unit"], dash["reviews_late"])
	}
	regs := list(dash["regulatory"])
	if len(regs) != 2 || obj(regs[0])["label"] != "DORA" || num(obj(regs[0])["count"]) != 1 || num(obj(regs[1])["count"]) != 1 {
		t.Fatalf("indicatori normativi: %v", regs)
	}
	for _, r := range regs {
		coherent(admin, "indicatore", r)
	}
	ragStat := obj(dash["rag"])
	for _, k := range []string{"green", "amber", "red", "overdue"} {
		coherent(admin, "semaforo "+k, ragStat[k])
	}
	if num(obj(ragStat["red"])["count"]) != 1 || num(obj(ragStat["overdue"])["count"]) != 1 || len(list(ragStat["items"])) != 1 {
		t.Fatalf("semafori: %v", ragStat)
	}
	fs := obj(dash["finance"])
	if num(fs["approved_cents"]) != 4_000_000 || num(fs["actual_cents"]) != 1_500_000 || num(fs["with_budget"]) != 1 ||
		count(admin, fs["budget_query"].(string)) != 1 || count(admin, fs["cost_query"].(string)) != int(num(fs["with_costs"])) {
		t.Fatalf("budget e costi: %v", fs)
	}
	if fs["can_see_details"] != true || len(list(fs["by_request"])) != 1 || len(list(fs["by_category"])) != 1 {
		t.Fatalf("dettaglio per l'amministratore: %v", fs)
	}
	if len(list(dash["team_load"])) != 3 {
		t.Fatalf("carico dei team: %v", dash["team_load"])
	}
	// Filtri: unità, driver, periodo; la query di base li riporta.
	f := admin.do("GET", "/api/v1/dashboard/portfolio?unit=compliance&driver=regulatory", nil, http.StatusOK)
	if num(obj(f["total"])["count"]) != 2 || !strings.Contains(f["base_query"].(string), `unita: "Compliance"`) {
		t.Fatalf("filtro unità e driver: %v %v", f["total"], f["base_query"])
	}
	coherent(admin, "filtro", f["total"])
	coherent(admin, "filtro aperte", f["open"])
	f = admin.do("GET", "/api/v1/dashboard/portfolio?from="+dayOff(1), nil, http.StatusOK)
	if num(obj(f["total"])["count"]) != 0 {
		t.Fatalf("filtro sul periodo: %v", f["total"])
	}
	coherent(admin, "periodo", f["total"])
	f = admin.do("GET", "/api/v1/dashboard/portfolio?from="+dayOff(-1)+"&to="+dayOff(0), nil, http.StatusOK)
	coherent(admin, "periodo", f["total"])
	if num(obj(f["total"])["count"]) != 3 {
		t.Fatalf("filtro sul periodo di oggi: %v", f["total"])
	}
	admin.do("GET", "/api/v1/dashboard/portfolio?driver=altro", nil, http.StatusUnprocessableEntity)
	admin.do("GET", "/api/v1/dashboard/portfolio?unit=Nessuna", nil, http.StatusUnprocessableEntity)
	// La cache si rinnova dopo una modifica.
	addCost(dora, 250_000)
	if v := num(obj(admin.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusOK)["finance"])["actual_cents"]); v != 1_750_000 {
		t.Fatalf("cruscotto non aggiornato dopo un costo: %v", v)
	}

	// Riservatezza: senza finance.read solo i totali.
	ld := lettore.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusOK)
	lf := obj(ld["finance"])
	if lf["can_see_details"] != false || lf["by_request"] != nil || lf["by_category"] != nil || num(lf["actual_cents"]) != 1_750_000 {
		t.Fatalf("il lettore vede il dettaglio finanziario: %v", lf)
	}
	if fd := obj(fin.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusOK)["finance"]); fd["can_see_details"] != true || len(list(fd["by_request"])) != 1 {
		t.Fatalf("la finanza non vede il dettaglio: %v", fd)
	}
	req.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusForbidden)

	// Pannelli scelti dalla persona.
	lay := list(lettore.do("GET", "/api/v1/dashboard/portfolio/layout", nil, http.StatusOK)["panels"])
	if len(lay) != len(report.Panels) || obj(lay[0])["key"] != "fasi" {
		t.Fatalf("pannelli di default: %v", lay)
	}
	lay = list(lettore.do("PUT", "/api/v1/dashboard/portfolio/layout", map[string]any{"panels": []map[string]any{
		{"key": "semafori", "visible": true}, {"key": "fasi", "visible": false}}}, http.StatusOK)["panels"])
	if obj(lay[0])["key"] != "semafori" || obj(lay[1])["visible"] != false || len(lay) != len(report.Panels) {
		t.Fatalf("pannelli salvati: %v", lay)
	}
	if obj(list(admin.do("GET", "/api/v1/dashboard/portfolio/layout", nil, http.StatusOK)["panels"])[0])["key"] != "fasi" {
		t.Fatal("la scelta dei pannelli di una persona vale per le altre")
	}
	lettore.do("PUT", "/api/v1/dashboard/portfolio/layout", map[string]any{"panels": []map[string]any{{"key": "boh", "visible": true}}}, http.StatusUnprocessableEntity)

	// -----------------------------------------------------------------
	// Portale del richiedente.
	// -----------------------------------------------------------------
	pt := req.do("GET", "/api/v1/portal", nil, http.StatusOK)
	if len(list(pt["types"])) != 1 || obj(list(pt["types"])[0])["type"] != "business_request" || len(list(pt["requests"])) != 0 {
		t.Fatalf("portale iniziale: %v", pt)
	}
	draft := req.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "business_request", "title": "", "step": 0}, http.StatusCreated)
	did := draft["id"].(string)
	req.do("POST", "/api/v1/portal/drafts/"+did+"/submit", nil, http.StatusUnprocessableEntity) // senza titolo
	curioso.do("PUT", "/api/v1/portal/drafts/"+did, map[string]any{"type": "business_request", "title": "Furto"}, http.StatusNotFound)
	curioso.do("POST", "/api/v1/portal/drafts/"+did+"/submit", nil, http.StatusNotFound)
	draft = req.do("PUT", "/api/v1/portal/drafts/"+did, map[string]any{"type": "business_request", "title": "Nuovo modulo di reclamo online", "step": 2,
		"data": map[string]any{"description": "I clienti oggi reclamano solo per email.", "requesting_unit_id": uComm, "desired_date": dayOff(120),
			"fields": map[string]any{"urgenza": "Media"}}}, http.StatusOK)
	if draft["step"] != 2.0 || obj(draft["data"])["requesting_unit_id"] != uComm {
		t.Fatalf("bozza salvata: %v", draft)
	}
	if n := len(list(req.do("GET", "/api/v1/portal", nil, http.StatusOK)["drafts"])); n != 1 {
		t.Fatalf("bozze del richiedente: %d", n)
	}
	if n := len(list(curioso.do("GET", "/api/v1/portal", nil, http.StatusOK)["drafts"])); n != 0 {
		t.Fatalf("bozze altrui visibili: %d", n)
	}
	el := req.do("POST", "/api/v1/portal/drafts/"+did+"/submit", nil, http.StatusCreated)
	mine := el["key"].(string)
	if el["state"] != "Inviata" {
		t.Fatalf("la richiesta del portale non è stata inviata: %v", el)
	}
	pt = req.do("GET", "/api/v1/portal", nil, http.StatusOK)
	if len(list(pt["drafts"])) != 0 || len(list(pt["requests"])) != 1 || obj(list(pt["requests"])[0])["key"] != mine ||
		!strings.Contains(obj(list(pt["requests"])[0])["text"].(string), "ricevuta") {
		t.Fatalf("le mie richieste: %v", pt["requests"])
	}
	pr := req.do("GET", "/api/v1/portal/requests/"+mine, nil, http.StatusOK)
	if pr["phase"] != "Inviata" || !strings.Contains(pr["next_step"].(string), "Triage") || len(list(pr["path"])) < 5 ||
		obj(list(pr["path"])[1])["state"] != "current" {
		t.Fatalf("la richiesta vista dal richiedente: %v", pr)
	}
	vals := map[string]string{}
	for _, v := range list(pr["data"]) {
		vals[obj(v)["label"].(string)] = obj(v)["value"].(string)
	}
	if vals["Unità richiedente"] != "Commerciale" || vals["Urgenza"] != "Media" {
		t.Fatalf("dati della richiesta nel portale: %v", vals)
	}
	// Commenti e chiarimenti con i componenti della collaborazione.
	req.do("POST", "/api/v1/elements/"+mine+"/comments", map[string]any{"body": "Allego il processo attuale."}, http.StatusCreated)
	// Il richiedente vede solo le sue richieste.
	curioso.do("GET", "/api/v1/portal/requests/"+mine, nil, http.StatusNotFound)
	curioso.do("GET", "/api/v1/elements/"+mine, nil, http.StatusNotFound)
	req.do("GET", "/api/v1/portal/requests/"+dora, nil, http.StatusNotFound)
	req.do("GET", "/api/v1/elements/"+dora, nil, http.StatusNotFound)
	if n := count(req, "tipo: business_request"); n != 1 {
		t.Fatalf("il richiedente trova %d richieste, non solo la sua", n)
	}
	if n := len(list(curioso.do("GET", "/api/v1/portal", nil, http.StatusOK)["requests"])); n != 0 {
		t.Fatalf("il curioso vede %d richieste", n)
	}
	req.do("GET", "/api/v1/status-reports/"+id1, nil, http.StatusNotFound)
	req.do("GET", "/api/v1/portal/settings", nil, http.StatusForbidden)

	// Configurazione del portale per il tenant.
	cfg := admin.do("GET", "/api/v1/portal/settings", nil, http.StatusOK)
	if len(list(cfg["available"])) == 0 || len(list(cfg["phases"])) == 0 {
		t.Fatalf("configurazione del portale: %v", cfg)
	}
	req.do("PUT", "/api/v1/portal/settings", map[string]any{"welcome": "x"}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/portal/settings", map[string]any{"types": []map[string]any{{"type": "business_request", "fields": []string{"inesistente"}}}},
		http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/portal/settings", map[string]any{
		"welcome":     "Benvenuto nel portale delle richieste.",
		"types":       []map[string]any{{"type": "business_request", "label": "Esigenza", "fields": []string{"urgenza"}, "standard": []string{"desired_date"}}},
		"phase_texts": map[string]string{"Inviata": "Ricevuta: la leggiamo entro due giorni."},
	}, http.StatusOK)
	pt = req.do("GET", "/api/v1/portal", nil, http.StatusOK)
	ty := obj(list(pt["types"])[0])
	if pt["welcome"] != "Benvenuto nel portale delle richieste." || ty["label"] != "Esigenza" || len(list(ty["fields"])) != 1 || len(list(ty["standard"])) != 1 ||
		obj(list(pt["requests"])[0])["text"] != "Ricevuta: la leggiamo entro due giorni." {
		t.Fatalf("portale configurato: %v", pt)
	}
	vals = map[string]string{}
	for _, v := range list(req.do("GET", "/api/v1/portal/requests/"+mine, nil, http.StatusOK)["data"]) {
		vals[obj(v)["label"].(string)] = obj(v)["value"].(string)
	}
	if _, ok := vals["Unità richiedente"]; ok {
		t.Fatalf("il portale mostra un dato non più esposto: %v", vals)
	}
	d2 := req.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "business_request", "title": "Con un campo non previsto",
		"data": map[string]any{"fields": map[string]any{"benefici_attesi": "tanti"}}}, http.StatusCreated)
	req.do("POST", "/api/v1/portal/drafts/"+d2["id"].(string)+"/submit", nil, http.StatusUnprocessableEntity)
	req.do("DELETE", "/api/v1/portal/drafts/"+d2["id"].(string), nil, http.StatusNoContent)
	req.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "impact", "title": "Tipo non esposto"}, http.StatusUnprocessableEntity)

	// -----------------------------------------------------------------
	// Isolamento tra tenant.
	// -----------------------------------------------------------------
	adminB.do("GET", "/api/v1/status-reports/"+id1, nil, http.StatusNotFound)
	adminB.do("PATCH", "/api/v1/status-reports/"+r2["id"].(string), map[string]any{"version": 1, "overall": "green"}, http.StatusNotFound)
	adminB.do("GET", "/api/v1/status-reports/"+id1+"/export.md", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/demand/requests/"+dora+"/status-reports", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/portal/requests/"+mine, nil, http.StatusNotFound)
	adminB.do("PUT", "/api/v1/portal/drafts/"+did, map[string]any{"type": "business_request", "title": "x"}, http.StatusNotFound)
	db2 := adminB.do("GET", "/api/v1/dashboard/portfolio", nil, http.StatusOK)
	if num(obj(db2["total"])["count"]) != 0 || num(obj(db2["finance"])["actual_cents"]) != 0 || len(list(db2["reviews_late"])) != 0 ||
		len(list(obj(db2["rag"])["items"])) != 0 {
		t.Fatalf("il cruscotto dell'altro tenant vede dati: %v", db2)
	}
	if pB := adminB.do("GET", "/api/v1/portal", nil, http.StatusOK); pB["welcome"] != "" || len(list(pB["requests"])) != 0 {
		t.Fatalf("il portale dell'altro tenant vede dati: %v", pB)
	}
	var n int
	if err := d.InTenant(ctx, tenB.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select (select count(*) from status_reports) + (select count(*) from status_report_schedules)
			+ (select count(*) from portal_drafts) + (select count(*) from portal_settings) + (select count(*) from report_settings)`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("righe dell'altro tenant visibili: %d %v", n, err)
	}
}
