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

// Progetti, passo 3 (replica/progetti.md §3 «Baseline e variazioni»): la prima baseline la fissa
// il PM ed è immutabile; il Gantt riceve le date di baseline e gli scostamenti; una modifica
// oltre la tolleranza del tenant apre da sola una change request, che resta da inviare finché
// manca lo sponsor e poi passa dal motore delle approvazioni; approvata, diventa la baseline 2.
// Chi vede il progetto ma non le voci riceve la baseline senza titoli.
func TestProjectBaselines(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("prb-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, work.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Baseline", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"sponsor", "editor"}, {"guest", ""}} {
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
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	guest := login(t, srv.URL, "guest@"+slug+".test", testPassword)
	run := func(fn func(tx pgx.Tx) error) error {
		return d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn)
	}
	sysAdmin := engine.Actor{MemberID: ten.AdminMemberID, Permissions: []string{"*"}}

	// Tolleranze del tenant in Configurazione → Progetti.
	st := admin.do("GET", "/api/v1/projects/settings", nil, http.StatusOK)
	if st["tolerance_days"] != 10.0 || st["tolerance_budget_pct"] != 5.0 {
		t.Fatalf("tolleranze predefinite: %v %v", st["tolerance_days"], st["tolerance_budget_pct"])
	}
	admin.do("PUT", "/api/v1/projects/settings", map[string]any{"tolerance_days": -1}, http.StatusUnprocessableEntity)
	st = admin.do("PUT", "/api/v1/projects/settings", map[string]any{"tolerance_days": 5, "tolerance_budget_pct": 8}, http.StatusOK)
	if st["tolerance_days"] != 5.0 || st["tolerance_budget_pct"] != 8.0 {
		t.Fatalf("tolleranze salvate: %v", st)
	}

	pj := admin.do("POST", "/api/v1/projects", map[string]any{"title": "Portale clienti", "template": "agile", "no_phases": true}, http.StatusCreated)
	pk := pj["key"].(string)
	admin.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "Sviluppo", "delivery_mode": "agile",
		"estimate_effort_days": 10, "start": "2027-01-04", "end": "2027-01-15"}, http.StatusCreated)
	ms := admin.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "milestone", "title": "Go-live", "date": "2027-01-29"}, http.StatusCreated)
	msID := ms["id"].(string)

	// Senza baseline: niente date di baseline e nessuna change request possibile.
	plan := admin.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusOK)
	if plan["baseline"] != nil || plan["tolerance"].(map[string]any)["days"] != 5.0 {
		t.Fatalf("piano senza baseline: %v %v", plan["baseline"], plan["tolerance"])
	}
	admin.do("POST", "/api/v1/projects/"+pk+"/change-requests", map[string]any{"reason": "x"}, http.StatusUnprocessableEntity)

	// La prima baseline la fissa il PM; la seconda si rifiuta (serve una change request).
	b1 := admin.do("POST", "/api/v1/projects/"+pk+"/baselines", map[string]any{"note": "Piano approvato in comitato"}, http.StatusCreated)
	if b1["number"] != 1.0 || len(b1["items"].([]any)) != 2 || b1["start"] != "2027-01-04" || b1["end"] != "2027-01-29" || b1["current"] != true {
		t.Fatalf("prima baseline: %v", b1)
	}
	admin.do("POST", "/api/v1/projects/"+pk+"/baselines", map[string]any{}, http.StatusUnprocessableEntity)

	// Immutabile: né modifica né cancellazione diretta.
	if err := run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update plan_baseline_items set end_date = '2030-01-01' where baseline_id = $1", b1["id"])
		return err
	}); err == nil || !strings.Contains(err.Error(), "immutabile") {
		t.Fatalf("modifica della baseline: %v", err)
	}
	if err := run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "delete from plan_baselines where id = $1", b1["id"])
		return err
	}); err == nil || !strings.Contains(err.Error(), "immutabile") {
		t.Fatalf("cancellazione della baseline: %v", err)
	}

	itemOf := func(c *user, id string) map[string]any {
		t.Helper()
		for _, it := range c.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusOK)["items"].([]any) {
			if m := it.(map[string]any); m["id"] == id {
				return m
			}
		}
		t.Fatalf("voce %s assente", id)
		return nil
	}
	crs := func() []any {
		t.Helper()
		return admin.do("GET", "/api/v1/projects/"+pk+"/change-requests", nil, http.StatusOK)["items"].([]any)
	}

	// Dentro la tolleranza (5 giorni): la milestone slitta di 3 giorni lavorativi, nessuna change request.
	admin.do("PUT", "/api/v1/plan-items/"+msID+"/schedule", map[string]any{"start": "2027-02-03", "end": "2027-02-03"}, http.StatusOK)
	if m := itemOf(admin, msID); m["baseline_end"] != "2027-01-29" || m["variance_days"] != 3.0 || m["in_baseline"] != true {
		t.Fatalf("milestone dentro la tolleranza: %v %v", m["baseline_end"], m["variance_days"])
	}
	if n := len(crs()); n != 0 {
		t.Fatalf("change request dentro la tolleranza: %d", n)
	}
	ov := admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["baseline"] != 1.0 || ov["variance_days"] != 3.0 || ov["baseline_end"] != "2027-01-29" || len(ov["over_tolerance"].([]any)) != 0 {
		t.Fatalf("numeri dentro la tolleranza: %v", ov)
	}

	// Oltre la tolleranza: +10 giorni lavorativi. Nasce la change request; senza sponsor resta da inviare.
	admin.do("PUT", "/api/v1/plan-items/"+msID+"/schedule", map[string]any{"start": "2027-02-12", "end": "2027-02-12"}, http.StatusOK)
	list := crs()
	if len(list) != 1 {
		t.Fatalf("change request oltre la tolleranza: %v", list)
	}
	cr := list[0].(map[string]any)
	crID := cr["id"].(string)
	if cr["status"] != "draft" || cr["origin"] != "tolerance" || cr["state"] != "Da inviare" || cr["baseline"] != 1.0 || cr["can_submit"] != true {
		t.Fatalf("change request automatica: %v", cr)
	}
	if bl, _ := cr["blocked"].(map[string]any); bl == nil || bl["code"] != "validation.required" {
		t.Fatalf("motivo del blocco: %v", cr["blocked"])
	}
	devs := cr["deviations"].([]any)
	if len(devs) != 2 {
		t.Fatalf("scostamenti: %v", devs)
	}
	if d0 := devs[0].(map[string]any); d0["id"] != msID || d0["days"] != 10.0 || d0["title"] != "Go-live" || d0["baseline_end"] != "2027-01-29" {
		t.Fatalf("scostamento della milestone: %v", d0)
	}
	ov = admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["signal"] != "red" || ov["open_change_requests"] != 1.0 || ov["end_over_tolerance"] != true || !strings.Contains(strings.Join(toStrings(ov["reasons"]), ","), "over_tolerance") {
		t.Fatalf("numeri oltre la tolleranza: %v", ov)
	}
	// Una sola change request aperta: un'altra modifica non ne apre una seconda.
	admin.do("PUT", "/api/v1/plan-items/"+msID+"/schedule", map[string]any{"start": "2027-02-15", "end": "2027-02-15"}, http.StatusOK)
	if n := len(crs()); n != 1 {
		t.Fatalf("seconda change request: %d", n)
	}

	// Con lo sponsor sul progetto la change request si invia e passa dal motore delle approvazioni.
	if err := run(func(tx pgx.Tx) error {
		return engine.SetStakeholder(ctx, tx, sysAdmin, pj["id"].(string), "sponsor", sponsor.member, "", false)
	}); err != nil {
		t.Fatal(err)
	}
	sub := admin.do("POST", "/api/v1/change-requests/"+crID+"/submit", map[string]any{"reason": "Il fornitore consegna più tardi"}, http.StatusOK)
	if sub["status"] != "pending" || sub["state"] != "In approvazione" || sub["blocked"] != nil || sub["reason"] != "Il fornitore consegna più tardi" {
		t.Fatalf("change request inviata: %v", sub)
	}
	admin.do("POST", "/api/v1/change-requests/"+crID+"/submit", map[string]any{}, http.StatusUnprocessableEntity)
	approve := func(decision string) {
		t.Helper()
		found := false
		for _, it := range sponsor.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
			if m := it.(map[string]any); m["element_key"] == pk {
				sponsor.do("POST", "/api/v1/approvals/steps/"+m["step_id"].(string)+"/decision", map[string]any{"decision": decision, "comment": "ok"}, http.StatusNoContent)
				found = true
			}
		}
		if !found {
			t.Fatal("nessuna approvazione per lo sponsor")
		}
	}
	approve("approved")
	cr = admin.do("GET", "/api/v1/change-requests/"+crID, nil, http.StatusOK)
	if cr["status"] != "approved" || cr["state"] != "Approvata" || cr["new_baseline"] != 2.0 {
		t.Fatalf("change request approvata: %v", cr)
	}
	// La baseline 2 è il piano proposto: la milestone al 12/02 (la modifica al 15/02 era dopo,
	// entro la tolleranza della nuova baseline), la baseline 1 non cambia.
	m := itemOf(admin, msID)
	if m["baseline_end"] != "2027-02-12" || m["variance_days"] != 1.0 {
		t.Fatalf("milestone sulla baseline 2: %v %v", m["baseline_end"], m["variance_days"])
	}
	bls := admin.do("GET", "/api/v1/projects/"+pk+"/baselines", nil, http.StatusOK)["items"].([]any)
	if len(bls) != 2 || bls[0].(map[string]any)["number"] != 2.0 || bls[0].(map[string]any)["current"] != true ||
		bls[0].(map[string]any)["change_request"].(map[string]any)["id"] != crID {
		t.Fatalf("elenco delle baseline: %v", bls)
	}
	old := admin.do("GET", "/api/v1/plan-baselines/"+b1["id"].(string), nil, http.StatusOK)
	if old["current"] != false || old["end"] != "2027-01-29" {
		t.Fatalf("baseline 1 dopo l'approvazione: %v", old)
	}
	if n := len(crs()); n != 1 {
		t.Fatalf("change request dopo l'approvazione: %d", n)
	}
	ov = admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["baseline"] != 2.0 || ov["open_change_requests"] != 0.0 || ov["signal"] == "red" {
		t.Fatalf("numeri sulla baseline 2: %v", ov)
	}

	// Change request a mano, respinta: la baseline resta la 2.
	man := admin.do("POST", "/api/v1/projects/"+pk+"/change-requests", map[string]any{"title": "Nuovo canale", "reason": "Aggiunta dell'app"}, http.StatusCreated)
	if man["status"] != "pending" || man["origin"] != "manual" || len(man["deviations"].([]any)) != 0 {
		t.Fatalf("change request a mano: %v", man)
	}
	admin.do("POST", "/api/v1/projects/"+pk+"/change-requests", map[string]any{"reason": "un'altra"}, http.StatusUnprocessableEntity)
	approve("rejected")
	if man = admin.do("GET", "/api/v1/change-requests/"+man["id"].(string), nil, http.StatusOK); man["status"] != "rejected" || man["state"] != "Respinta" {
		t.Fatalf("change request respinta: %v", man)
	}
	if bls = admin.do("GET", "/api/v1/projects/"+pk+"/baselines", nil, http.StatusOK)["items"].([]any); len(bls) != 2 {
		t.Fatalf("baseline dopo il rifiuto: %d", len(bls))
	}

	// Visibilità: chi ha un ruolo solo sul progetto lo vede, ma non le voci: la baseline arriva
	// senza titoli, come il piano.
	if err := run(func(tx pgx.Tx) error {
		return engine.SetStakeholder(ctx, tx, sysAdmin, pj["id"].(string), "pmo", guest.member, "", false)
	}); err != nil {
		t.Fatal(err)
	}
	gb := guest.do("GET", "/api/v1/plan-baselines/"+b1["id"].(string), nil, http.StatusOK)
	for _, it := range gb["items"].([]any) {
		if m := it.(map[string]any); m["restricted"] != true || m["title"] != "" {
			t.Fatalf("voce di baseline vista da chi non la legge: %v", m)
		}
	}
	if g := itemOf(guest, msID); g["restricted"] != true || g["baseline_end"] != "2027-02-12" {
		t.Fatalf("milestone vista da chi non la legge: %v", g)
	}
	for _, it := range guest.do("GET", "/api/v1/projects/"+pk+"/change-requests", nil, http.StatusOK)["items"].([]any) {
		t.Fatalf("change request visibile a chi non la legge: %v", it)
	}

	// L'immutabilità non blocca le cancellazioni a cascata (copia dell'ambiente di prova,
	// cancellazione del tenant): via gli elementi, via le baseline.
	if err := run(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "delete from elements"); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from plan_baselines").Scan(&n); err != nil || n != 0 {
			t.Errorf("baseline rimaste: %d", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("cancellazione a cascata: %v", err)
	}
}
