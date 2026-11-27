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
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Progetti, passo 5 (replica/progetti.md §3 e §6): budget del progetto sopra la finanza, il budget
// oltre la tolleranza apre da solo la change request, una change request in approvazione si ritira,
// le voci aggiunte dopo la baseline sono uno scostamento di perimetro, le tolleranze del singolo
// progetto, lo status report del progetto con l'avviso allo sponsor, «In arrivo dai progetti».
func TestProjectStep5(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("pr5-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, work.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Passo 5", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"pm", "editor"}, {"sponsor", "viewer"}, {"lead", "editor"}, {"dev", "viewer"}, {"guest", ""}} {
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
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	guest := login(t, srv.URL, "guest@"+slug+".test", testPassword)
	dev := login(t, srv.URL, "dev@"+slug+".test", testPassword)
	run := func(fn func(tx pgx.Tx) error) error { return d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn) }
	sysAdmin := engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}
	role := func(el, key, member string) {
		t.Helper()
		if err := run(func(tx pgx.Tx) error { return engine.SetStakeholder(ctx, tx, sysAdmin, el, key, member, "", false) }); err != nil {
			t.Fatal(err)
		}
	}

	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	var webTeam string
	for _, tm := range admin.do("GET", "/api/v1/teams", nil, http.StatusOK)["items"].([]any) {
		if m := tm.(map[string]any); m["name"] == "Web" {
			webTeam = m["id"].(string)
		}
	}

	// Il progetto nasce da zero: pm ne è il PM; lo sponsor è sul progetto.
	pj := pm.do("POST", "/api/v1/projects", map[string]any{"title": "Portale clienti", "template": "agile", "no_phases": true}, http.StatusCreated)
	pk, pid := pj["key"].(string), pj["id"].(string)
	role(pid, "sponsor", sponsor.member)
	start := time.Now().UTC().AddDate(0, 0, 7).Format(time.DateOnly)
	end := time.Now().UTC().AddDate(0, 0, 28).Format(time.DateOnly)
	wp := pm.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "Area riservata", "delivery_mode": "waterfall",
		"team_id": webTeam, "estimate_effort_days": 10, "start": start, "end": end}, http.StatusCreated)
	act := pm.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "plan_activity", "title": "Disegno tecnico", "parent_id": wp["id"],
		"start": start, "end": start}, http.StatusCreated)
	ms := pm.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "milestone", "title": "Go-live", "date": end}, http.StatusCreated)

	// --- Budget del progetto: un budget in Finanza sull'elemento del progetto, approvato dallo sponsor.
	fy := admin.do("POST", "/api/v1/finance/fiscal-years", map[string]any{"name": "FY", "start_date": time.Now().UTC().Format("2006") + "-01-01",
		"end_date": time.Now().UTC().AddDate(1, 0, 0).Format("2006") + "-12-31"}, http.StatusCreated)["id"].(string)
	cats := map[string]string{}
	for _, c := range admin.do("GET", "/api/v1/finance/overview", nil, http.StatusOK)["categories"].([]any) {
		m := c.(map[string]any)
		cats[m["code"].(string)] = m["id"].(string)
	}
	ov := pm.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["budget"] != nil || ov["budget_visible"] != true {
		t.Fatalf("budget prima della finanza: %v %v", ov["budget"], ov["budget_visible"])
	}
	bud := admin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": pk, "fiscal_year_id": fy,
		"lines": []map[string]any{{"cost_category_id": cats["CONS"], "amount_cents": 1_000_000}}}, http.StatusCreated)
	admin.do("POST", "/api/v1/finance/budgets/"+bud["id"].(string)+"/submit", nil, http.StatusOK)
	decide := func(u *user, prefix, decision string) bool {
		t.Helper()
		for _, it := range u.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
			if m := it.(map[string]any); strings.HasPrefix(m["stage"].(string), prefix) {
				u.do("POST", "/api/v1/approvals/steps/"+m["step_id"].(string)+"/decision", map[string]any{"decision": decision, "comment": "ok"}, http.StatusNoContent)
				return true
			}
		}
		return false
	}
	if !decide(sponsor, "Budget", "approved") {
		t.Fatal("lo sponsor non ha il budget da approvare")
	}
	ov = pm.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	b, _ := ov["budget"].(map[string]any)
	if b == nil || i64(b["approved_cents"]) != 1_000_000 || len(b["budgets"].([]any)) != 1 || b["budgets"].([]any)[0].(map[string]any)["element_key"] != pk {
		t.Fatalf("budget del progetto: %v", ov["budget"])
	}
	// Il team non vede il budget; il PMO e lo sponsor sì.
	role(pid, "pmo", guest.member)
	if g := guest.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any); g["budget"] == nil {
		t.Fatalf("il PMO non vede il budget")
	}

	// --- Baseline con il budget; poi la previsione a finire +12% (tolleranza 5%): nasce la change request.
	bl := pm.do("POST", "/api/v1/projects/"+pk+"/baselines", map[string]any{"note": "Piano approvato"}, http.StatusCreated)
	if bb, _ := bl["budget"].(map[string]any); bb == nil || i64(bb["approved_cents"]) != 1_000_000 {
		t.Fatalf("budget nella baseline: %v", bl["budget"])
	}
	fc := admin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": pk, "fiscal_year_id": fy, "kind": "forecast",
		"lines": []map[string]any{{"cost_category_id": cats["CONS"], "amount_cents": 1_120_000}}}, http.StatusCreated)
	admin.do("POST", "/api/v1/finance/budgets/"+fc["id"].(string)+"/submit", nil, http.StatusOK)
	crs := func() []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, it := range pm.do("GET", "/api/v1/projects/"+pk+"/change-requests", nil, http.StatusOK)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	list := crs()
	if len(list) != 1 {
		t.Fatalf("change request dal budget: %v", list)
	}
	cr := list[0]
	if cr["title"] != "Budget oltre la tolleranza" || cr["origin"] != "tolerance" || cr["status"] != "pending" || cr["can_withdraw"] != true {
		t.Fatalf("change request del budget: %v", cr)
	}
	if devs := cr["deviations"].([]any); len(devs) != 1 || devs[0].(map[string]any)["kind"] != "budget" || devs[0].(map[string]any)["pct"] != 12.0 {
		t.Fatalf("scostamento del budget: %v", cr["deviations"])
	}
	ov = pm.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["budget_over_tolerance"] != true || ov["signal"] != "red" {
		t.Fatalf("numeri con il budget oltre: %v", ov)
	}

	// --- La change request in approvazione si ritira: sparisce dalla posta dello sponsor.
	w := pm.do("POST", "/api/v1/change-requests/"+cr["id"].(string)+"/withdraw", nil, http.StatusOK)
	if w["status"] != "withdrawn" || w["state"] != "Ritirata" || w["can_withdraw"] != false {
		t.Fatalf("change request ritirata: %v", w)
	}
	if decide(sponsor, "Change request", "approved") {
		t.Fatal("la change request ritirata è ancora nella posta dello sponsor")
	}
	pm.do("POST", "/api/v1/change-requests/"+cr["id"].(string)+"/withdraw", nil, http.StatusUnprocessableEntity)
	// Il budget torna nella tolleranza: la previsione scende a +3%.
	fc2 := admin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": pk, "fiscal_year_id": fy, "kind": "forecast",
		"lines": []map[string]any{{"cost_category_id": cats["CONS"], "amount_cents": 1_030_000}}}, http.StatusCreated)
	admin.do("POST", "/api/v1/finance/budgets/"+fc2["id"].(string)+"/submit", nil, http.StatusOK)
	if n := len(crs()); n != 1 {
		t.Fatalf("change request dopo il rientro: %d", n)
	}

	// --- Perimetro: un work package nuovo dopo la baseline apre la change request «Variazione del perimetro».
	extra := pm.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "App", "delivery_mode": "agile",
		"team_id": webTeam, "start": start, "end": end}, http.StatusCreated)
	list = crs()
	if len(list) != 2 || list[0]["title"] != "Variazione del perimetro" {
		t.Fatalf("change request del perimetro: %v", list)
	}
	if devs := list[0]["deviations"].([]any); len(devs) != 1 || devs[0].(map[string]any)["kind"] != "scope_added" || devs[0].(map[string]any)["title"] != "App" {
		t.Fatalf("scostamento di perimetro: %v", list[0]["deviations"])
	}
	ov = pm.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if len(ov["scope_added"].([]any)) != 1 || !strings.Contains(strings.Join(toStrings(ov["reasons"]), ","), "scope_changed") {
		t.Fatalf("numeri con il perimetro cambiato: %v", ov)
	}
	pm.do("POST", "/api/v1/change-requests/"+list[0]["id"].(string)+"/withdraw", nil, http.StatusOK)

	// --- Tolleranze del progetto: il PM non le cambia, lo sponsor sì; nil torna a quella del tenant.
	plan := pm.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusOK)
	if plan["can_edit_tolerance"] != false || plan["project_tolerance"].(map[string]any)["days"] != nil {
		t.Fatalf("tolleranze viste dal PM: %v %v", plan["can_edit_tolerance"], plan["project_tolerance"])
	}
	pm.do("PUT", "/api/v1/projects/"+pk+"/tolerance", map[string]any{"days": 3}, http.StatusForbidden)
	sponsor.do("PUT", "/api/v1/projects/"+pk+"/tolerance", map[string]any{"days": 400}, http.StatusUnprocessableEntity)
	plan = sponsor.do("PUT", "/api/v1/projects/"+pk+"/tolerance", map[string]any{"days": 3, "budget_pct": 2}, http.StatusOK)
	if tol := plan["tolerance"].(map[string]any); tol["days"] != 3.0 || tol["budget_pct"] != 2.0 || plan["can_edit_tolerance"] != true {
		t.Fatalf("tolleranze del progetto: %v", plan["tolerance"])
	}
	// La previsione al +3% ora è oltre il 2%: le tolleranze nuove aprono subito la change request, che
	// porta anche il work package nuovo (la change request di prima è stata ritirata).
	if list = crs(); len(list) != 3 || list[0]["title"] != "Scostamento oltre le tolleranze" || len(list[0]["deviations"].([]any)) != 2 {
		t.Fatalf("change request con le tolleranze strette: %v", list)
	}
	pm.do("POST", "/api/v1/change-requests/"+list[0]["id"].(string)+"/withdraw", nil, http.StatusOK)
	plan = sponsor.do("PUT", "/api/v1/projects/"+pk+"/tolerance", map[string]any{"days": nil, "budget_pct": nil}, http.StatusOK)
	if tol := plan["tolerance"].(map[string]any); tol["days"] != 10.0 || plan["project_tolerance"].(map[string]any)["days"] != nil {
		t.Fatalf("tolleranze tornate al tenant: %v", plan["tolerance"])
	}

	// --- Status report del progetto: precompilato dai numeri, inviato, lo sponsor riceve l'avviso.
	sr := pm.do("POST", "/api/v1/projects/"+pk+"/status-reports", map[string]any{}, http.StatusCreated)
	data := sr["data"].(map[string]any)
	pd, _ := data["project"].(map[string]any)
	if pd == nil || pd["work_packages"] != 2.0 || len(pd["scope_added"].([]any)) != 1 || data["schedule"].(map[string]any)["tasks"] != 2.0 {
		t.Fatalf("fotografia del progetto: %v", data)
	}
	if sug := data["suggested"].(map[string]any); sug["scope"] != "amber" {
		t.Fatalf("semafori proposti: %v", sug)
	}
	if !strings.Contains(sr["sections"].(map[string]any)["summary"].(string), "work package chiusi") {
		t.Fatalf("sintesi proposta: %v", sr["sections"])
	}
	sec := sr["sections"].(map[string]any)
	sec["summary"] = "Il fornitore del portale ha chiesto due settimane in più."
	sr = pm.do("PATCH", "/api/v1/project-reports/"+sr["id"].(string), map[string]any{"version": sr["version"], "sections": sec, "overall": "amber"}, http.StatusOK)
	sr = pm.do("POST", "/api/v1/project-reports/"+sr["id"].(string)+"/submit", nil, http.StatusOK)
	if sr["status"] != "submitted" {
		t.Fatalf("report inviato: %v", sr)
	}
	if !hasNotice(sponsor, "report", "submitted") {
		t.Fatal("lo sponsor non ha ricevuto l'avviso del report")
	}
	hist := sponsor.do("GET", "/api/v1/projects/"+pk+"/status-reports", nil, http.StatusOK)
	items := hist["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["can_acknowledge"] != true {
		t.Fatalf("storico dei report per lo sponsor: %v", hist)
	}
	sponsor.do("POST", "/api/v1/project-reports/"+sr["id"].(string)+"/acknowledge", nil, http.StatusOK)
	// Chi legge il progetto senza vedere il budget (un membro del team in sola lettura) riceve il report senza importi.
	if dv := dev.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any); dv["budget"] != nil || dv["budget_visible"] != false {
		t.Fatalf("budget visto dal team: %v", dv["budget"])
	}
	lr := dev.do("GET", "/api/v1/project-reports/"+sr["id"].(string), nil, http.StatusOK)
	if c := lr["data"].(map[string]any)["cost"].(map[string]any); c["hidden"] != true || c["approved_cents"] != nil {
		t.Fatalf("importi visti da chi non vede il budget: %v", c)
	}
	if md := dev.raw("/api/v1/project-reports/"+sr["id"].(string)+"/export.md", http.StatusOK); !strings.Contains(md, "Work package: 2") {
		t.Fatalf("export del report: %s", md)
	}

	// --- In arrivo dai progetti: il team Web vede il work package, la sua attività e quello nuovo.
	in := lead.do("GET", "/api/v1/projects/incoming?team="+webTeam+"&weeks=8", nil, http.StatusOK)
	keys := map[string]map[string]any{}
	for _, it := range in["items"].([]any) {
		m := it.(map[string]any)
		keys[m["id"].(string)] = m
	}
	if len(keys) != 3 || keys[wp["id"].(string)]["proposed"] != true || keys[act["id"].(string)]["work_package"].(map[string]any)["key"] != wp["key"] ||
		keys[extra["id"].(string)]["project"].(map[string]any)["key"] != pk {
		t.Fatalf("in arrivo dai progetti: %v", in)
	}
	if _, ok := keys[ms["id"].(string)]; ok {
		t.Fatal("la milestone non è lavoro del team")
	}
	// Fuori dalla finestra non c'è niente; chi non legge le voci le vede solo contate.
	if in = lead.do("GET", "/api/v1/projects/incoming?team="+webTeam+"&weeks=0", nil, http.StatusOK); in["weeks"] != 8.0 {
		t.Fatalf("finestra predefinita: %v", in["weeks"])
	}
	if g := guest.do("GET", "/api/v1/projects/incoming?team="+webTeam, nil, http.StatusOK); len(g["items"].([]any)) != 0 || g["restricted"] != 3.0 {
		t.Fatalf("in arrivo per chi non legge le voci: %v", g)
	}
	lead.do("GET", "/api/v1/projects/incoming", nil, http.StatusUnprocessableEntity)
}
