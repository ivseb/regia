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

// Progetti, passo 4 (replica/progetti.md §3, §12): il work package del fornitore con contratto,
// SAL e deliverable; il fornitore come utente esterno (vede solo il suo work package) oppure
// fuori da Regia (aggiorna il PM); il conflitto tra due progetti sulla stessa applicazione, con
// l'avviso al team owner; il programma con i numeri aggregati e la roadmap; il lessico.
func TestProjectSuppliers(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("prf-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, work.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Fornitori", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"lead", "viewer"}, {"vendor", ""}, {"guest", "viewer"}} {
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
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	vendor := login(t, srv.URL, "vendor@"+slug+".test", testPassword)
	run := func(fn func(tx pgx.Tx) error) error {
		return d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn)
	}

	// Lessico: «Work package» e «SAL» sono i nomi di partenza, anche in inglese.
	names := map[string]string{}
	for _, n := range admin.do("GET", "/api/v1/type-names?lang=en", nil, http.StatusOK)["items"].([]any) {
		m := n.(map[string]any)
		names[m["key"].(string)] = m["name"].(string)
	}
	if names["work_package"] != "Work package" || names["progress_claim"] != "Progress claim" || names["program"] != "Program" {
		t.Fatalf("nomi inglesi dei tipi: %v %v %v", names["work_package"], names["progress_claim"], names["program"])
	}
	names = map[string]string{}
	for _, n := range admin.do("GET", "/api/v1/type-names?lang=it", nil, http.StatusOK)["items"].([]any) {
		m := n.(map[string]any)
		names[m["key"].(string)] = m["name"].(string)
	}
	if names["progress_claim"] != "SAL" {
		t.Fatalf("nome italiano del SAL: %q", names["progress_claim"])
	}

	// --- 1. Work package del fornitore: contratto, SAL e deliverable ---------------------------
	pj := admin.do("POST", "/api/v1/projects", map[string]any{"title": "IDP: secondo fattore", "template": "contract", "no_phases": true}, http.StatusCreated)
	pk := pj["key"].(string)
	wp := admin.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "Integrazione IDP",
		"delivery_mode": "supplier", "estimate_effort_days": 20, "start": "2027-03-01", "end": "2027-04-30"}, http.StatusCreated)
	wpID := wp["id"].(string)
	agile := admin.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "Adeguamento app",
		"delivery_mode": "agile", "estimate_effort_days": 20}, http.StatusCreated)
	ms := admin.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "milestone", "title": "Collaudo IDP", "date": "2027-04-30",
		"contractual": true}, http.StatusCreated)

	// Il PM imposta il contratto e il contatto del fornitore, che per ora non aggiorna da sé.
	sv := admin.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contract_ref": "Ordine 2027/14",
		"contract_amount_cents": 4000000, "contract_currency": "eur", "contact_id": vendor.member}, http.StatusOK)
	if c := sv["contract"].(map[string]any); c["ref"] != "Ordine 2027/14" || c["amount_cents"] != 4000000.0 || c["currency"] != "EUR" {
		t.Fatalf("contratto: %v", c)
	}
	if cs := sv["contacts"].([]any); len(cs) != 1 || sv["supplier_updates"] != false || sv["can_edit"] != true || sv["can_declare"] != true {
		t.Fatalf("contatto e permessi del PM: %v %v %v", cs, sv["can_edit"], sv["can_declare"])
	}
	// I SAL valgono solo per i work package del fornitore.
	admin.do("POST", "/api/v1/plan-items/"+agile["id"].(string)+"/claims", map[string]any{"percent": 10}, http.StatusUnprocessableEntity)

	// Senza supplier_updates il fornitore vede il suo work package ma non dichiara.
	vs := vendor.do("GET", "/api/v1/plan-items/"+wpID+"/supplier", nil, http.StatusOK)
	if vs["can_declare"] != false || vs["can_edit"] != false || vs["amounts_visible"] != true {
		t.Fatalf("fornitore senza aggiornamenti: %v %v %v", vs["can_declare"], vs["can_edit"], vs["amounts_visible"])
	}
	vendor.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 10}, http.StatusForbidden)
	// Il fornitore esterno non legge il resto del progetto: il piano non gli si apre.
	vendor.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusNotFound)

	// Il PM inserisce e accetta insieme un SAL ricevuto fuori da Regia: 25% complessivo.
	sv = admin.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 25, "amount_cents": 1000000,
		"period_end": "2027-03-31", "note": "SAL di marzo", "accept": true}, http.StatusCreated)
	cl := sv["claims"].([]any)
	if len(cl) != 1 || cl[0].(map[string]any)["status"] != "accepted" || cl[0].(map[string]any)["state"] != "Accettato" ||
		sv["progress"] != 25.0 || sv["progress_source"] != "claims" || sv["accepted_cents"] != 1000000.0 {
		t.Fatalf("SAL accettato dal PM: %v progress=%v da %v", cl, sv["progress"], sv["progress_source"])
	}

	// Da qui il fornitore aggiorna da sé.
	admin.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"supplier_updates": true}, http.StatusOK)
	// La percentuale è complessiva: non scende sotto l'ultimo SAL accettato.
	vendor.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 20}, http.StatusUnprocessableEntity)
	// Il fornitore non accetta i propri SAL.
	vendor.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 60, "accept": true}, http.StatusForbidden)
	vs = vendor.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 60, "amount_cents": 1400000, "note": "SAL di aprile"}, http.StatusCreated)
	cl = vs["claims"].([]any)
	second := cl[1].(map[string]any)
	if second["status"] != "submitted" || second["number"] != 2.0 || second["value"] != 60.0 || vs["progress"] != 25.0 {
		t.Fatalf("SAL dichiarato dal fornitore: %v progress=%v", second, vs["progress"])
	}
	// Un SAL alla volta; il PM riceve l'avviso.
	vendor.do("POST", "/api/v1/plan-items/"+wpID+"/claims", map[string]any{"percent": 70}, http.StatusUnprocessableEntity)
	if !hasNotice(admin, "project", "claim_submitted") {
		t.Fatal("il PM non ha l'avviso del SAL da accettare")
	}
	ov := admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["claims_pending"] != 1.0 {
		t.Fatalf("SAL da accettare nella Panoramica: %v", ov["claims_pending"])
	}
	claimID := second["id"].(string)
	vendor.do("POST", "/api/v1/progress-claims/"+claimID+"/decision", map[string]any{"outcome": "accepted"}, http.StatusForbidden)
	admin.do("POST", "/api/v1/progress-claims/"+claimID+"/decision", map[string]any{"outcome": "rejected"}, http.StatusUnprocessableEntity)
	sv = admin.do("POST", "/api/v1/progress-claims/"+claimID+"/decision", map[string]any{"outcome": "accepted", "note": "ok"}, http.StatusOK)
	if sv["progress"] != 60.0 || sv["accepted_cents"] != 2400000.0 {
		t.Fatalf("dopo l'accettazione: progress=%v accettati=%v", sv["progress"], sv["accepted_cents"])
	}
	admin.do("POST", "/api/v1/progress-claims/"+claimID+"/decision", map[string]any{"outcome": "rejected", "note": "x"}, http.StatusUnprocessableEntity)
	// L'avanzamento del progetto sale: 60 sul work package del fornitore, 0 sull'agile, stesse stime.
	ov = admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if ov["progress"] != 30.0 || ov["claims_pending"] != 0.0 {
		t.Fatalf("avanzamento del progetto: %v (SAL da accettare %v)", ov["progress"], ov["claims_pending"])
	}
	// Lo storico dell'avanzamento del work package ha i due SAL.
	if h := admin.do("GET", "/api/v1/plan-items/"+wpID+"/progress", nil, http.StatusOK)["items"].([]any); len(h) != 2 || h[0].(map[string]any)["percent"] != 60.0 {
		t.Fatalf("storico dai SAL: %v", h)
	}

	// Deliverable: il PM li crea (anche legati alla milestone), il fornitore consegna, il PM decide.
	vendor.do("POST", "/api/v1/plan-items/"+wpID+"/deliverables", map[string]any{"title": "x"}, http.StatusForbidden)
	admin.do("POST", "/api/v1/plan-items/"+wpID+"/deliverables", map[string]any{"title": "Doc", "milestone_id": agile["id"]}, http.StatusUnprocessableEntity)
	sv = admin.do("POST", "/api/v1/plan-items/"+wpID+"/deliverables", map[string]any{"title": "Documento di disegno", "due": "2027-03-15",
		"milestone_id": ms["key"]}, http.StatusCreated)
	dels := sv["deliverables"].([]any)
	del := dels[0].(map[string]any)
	if del["status"] != "pending" || del["state"] != "Da consegnare" || del["due"] != "2027-03-15" || del["milestone"] != "Collaudo IDP" {
		t.Fatalf("deliverable: %v", del)
	}
	delID := del["id"].(string)
	// Il fornitore legge il deliverable (è del suo work package) e lo consegna.
	vs = vendor.do("POST", "/api/v1/deliverables/"+delID+"/deliver", map[string]any{"note": "in allegato"}, http.StatusOK)
	if d0 := vs["deliverables"].([]any)[0].(map[string]any); d0["status"] != "delivered" || d0["title"] != "Documento di disegno" {
		t.Fatalf("consegna del fornitore: %v", d0)
	}
	vendor.do("POST", "/api/v1/deliverables/"+delID+"/deliver", map[string]any{}, http.StatusUnprocessableEntity)
	sv = admin.do("POST", "/api/v1/deliverables/"+delID+"/decision", map[string]any{"outcome": "rejected", "note": "manca il capitolo 3"}, http.StatusOK)
	if d0 := sv["deliverables"].([]any)[0].(map[string]any); d0["status"] != "rejected" || d0["state"] != "Da consegnare" {
		t.Fatalf("deliverable respinto: %v", d0)
	}
	vendor.do("POST", "/api/v1/deliverables/"+delID+"/deliver", map[string]any{}, http.StatusOK)
	sv = admin.do("POST", "/api/v1/deliverables/"+delID+"/decision", map[string]any{"outcome": "accepted"}, http.StatusOK)
	if d0 := sv["deliverables"].([]any)[0].(map[string]any); d0["status"] != "accepted" || d0["state"] != "Accettato" {
		t.Fatalf("deliverable accettato: %v", d0)
	}
	// Nel piano: conteggi dei deliverable; l'avanzamento resta quello dei SAL.
	plan := admin.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusOK)
	for _, it := range plan["items"].([]any) {
		if m := it.(map[string]any); m["id"] == wpID {
			if m["deliverables"] != 1.0 || m["deliverables_accepted"] != 1.0 || m["progress"] != 60.0 || m["supplier_updates"] != true {
				t.Fatalf("work package nel piano: %v", m)
			}
		}
	}
	// Chi non pianifica e non è il contatto non vede gli importi.
	guest := login(t, srv.URL, "guest@"+slug+".test", testPassword)
	gs := guest.do("GET", "/api/v1/plan-items/"+wpID+"/supplier", nil, http.StatusOK)
	if gs["amounts_visible"] != false || gs["contract"].(map[string]any)["amount_cents"] != nil || gs["accepted_cents"] != nil ||
		gs["claims"].([]any)[0].(map[string]any)["amount_cents"] != nil {
		t.Fatalf("importi visibili a chi non pianifica: %v", gs)
	}
	// Togliere il contatto toglie l'accesso al work package.
	admin.do("PUT", "/api/v1/plan-items/"+wpID+"/supplier", map[string]any{"contact_id": ""}, http.StatusOK)
	vendor.do("GET", "/api/v1/plan-items/"+wpID+"/supplier", nil, http.StatusNotFound)

	// --- 2. Componenti condivisi: conflitto tra due progetti sulla stessa applicazione ----------
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Identità", "lead_id": lead.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome,team\nIDP,Identity provider,Identità\n"}, http.StatusOK)
	var idp string
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		if m := a.(map[string]any); m["code"] == "IDP" {
			idp = m["id"].(string)
		}
	}
	admin.do("PATCH", "/api/v1/plan-items/"+wpID, map[string]any{"application_id": idp}, http.StatusOK)
	other := admin.do("POST", "/api/v1/projects", map[string]any{"title": "Accesso unico", "template": "agile", "no_phases": true}, http.StatusCreated)
	ok := other["key"].(string)
	// Periodo diverso: nessun conflitto.
	owp := admin.do("POST", "/api/v1/projects/"+ok+"/items", map[string]any{"kind": "work_package", "title": "SSO su IDP",
		"application_id": idp, "start": "2027-06-01", "end": "2027-06-30"}, http.StatusCreated)
	if cs, _ := owp["conflicts"].([]any); len(cs) != 0 || hasNotice(lead, "project", "conflict") {
		t.Fatalf("conflitto fuori periodo: %v", cs)
	}
	// Lo spostamento sovrappone i periodi: conflitto nei due piani e avviso al team owner, una volta.
	admin.do("PUT", "/api/v1/plan-items/"+owp["id"].(string)+"/schedule", map[string]any{"start": "2027-04-12", "end": "2027-05-14"}, http.StatusOK)
	conflictOf := func(c *user, projectKey, itemID string) []any {
		t.Helper()
		for _, it := range c.do("GET", "/api/v1/projects/"+projectKey+"/plan", nil, http.StatusOK)["items"].([]any) {
			if m := it.(map[string]any); m["id"] == itemID {
				cs, _ := m["conflicts"].([]any)
				return cs
			}
		}
		t.Fatalf("voce %s assente", itemID)
		return nil
	}
	cs := conflictOf(admin, pk, wpID)
	if len(cs) != 1 {
		t.Fatalf("conflitto nel piano: %v", cs)
	}
	c0 := cs[0].(map[string]any)
	if c0["application"] != "Identity provider" || c0["project"].(map[string]any)["key"] != ok || c0["end"] != "2027-05-14" {
		t.Fatalf("conflitto: %v", c0)
	}
	if len(conflictOf(admin, ok, owp["id"].(string))) != 1 {
		t.Fatal("il conflitto manca nell'altro piano")
	}
	if !hasNotice(lead, "project", "conflict") {
		t.Fatal("il team owner non ha l'avviso di conflitto")
	}
	ov = admin.do("GET", "/api/v1/projects/"+pk+"/overview", nil, http.StatusOK)["numbers"].(map[string]any)
	if len(ov["conflicts"].([]any)) != 1 || !strings.Contains(strings.Join(toStrings(ov["reasons"]), ","), "conflicts") {
		t.Fatalf("conflitto nella Panoramica: %v %v", ov["conflicts"], ov["reasons"])
	}
	// Un altro spostamento dentro il conflitto non ripete l'avviso; finito il conflitto la coppia si dimentica.
	admin.do("PUT", "/api/v1/plan-items/"+owp["id"].(string)+"/schedule", map[string]any{"start": "2027-04-13", "end": "2027-05-14"}, http.StatusOK)
	if n := countNotices(t, run, "conflict"); n != 1 {
		t.Fatalf("avvisi di conflitto ripetuti: %d", n)
	}
	admin.do("PUT", "/api/v1/plan-items/"+owp["id"].(string)+"/schedule", map[string]any{"start": "2027-07-01", "end": "2027-07-30"}, http.StatusOK)
	if len(conflictOf(admin, pk, wpID)) != 0 {
		t.Fatal("conflitto rimasto dopo lo spostamento")
	}
	var pairs int
	if err := run(func(tx pgx.Tx) error { return tx.QueryRow(ctx, "select count(*) from plan_conflicts").Scan(&pairs) }); err != nil || pairs != 0 {
		t.Fatalf("coppie di conflitto rimaste: %d %v", pairs, err)
	}

	// --- 3. Programma: progetti, numeri aggregati con computeNumbers, roadmap ------------------
	admin.do("POST", "/api/v1/programs", map[string]any{"title": ""}, http.StatusUnprocessableEntity)
	pg := admin.do("POST", "/api/v1/programs", map[string]any{"title": "Sicurezza dei canali"}, http.StatusCreated)
	pgk := pg["key"].(string)
	admin.do("PUT", "/api/v1/projects/"+pk+"/program", map[string]any{"program": "PRJ-99999"}, http.StatusUnprocessableEntity)
	if r := admin.do("PUT", "/api/v1/projects/"+pk+"/program", map[string]any{"program": pgk}, http.StatusOK); r["program"].(map[string]any)["key"] != pgk {
		t.Fatalf("progetto nel programma: %v", r)
	}
	admin.do("PUT", "/api/v1/projects/"+ok+"/program", map[string]any{"program": pg["id"]}, http.StatusOK)
	guest.do("PUT", "/api/v1/projects/"+ok+"/program", map[string]any{"program": ""}, http.StatusForbidden)
	pv := admin.do("GET", "/api/v1/programs/"+pgk, nil, http.StatusOK)
	prs := pv["projects"].([]any)
	if len(prs) != 2 || pv["can_edit"] != true {
		t.Fatalf("progetti del programma: %v", prs)
	}
	// Aggregato: tre work package (20 + 20 giorni nel primo progetto, il terzo senza stima pesa la media) a 60, 0, 0.
	pn := pv["numbers"].(map[string]any)
	if pn["work_packages"] != 3.0 || pn["progress"] != 20.0 || pn["milestones"] != 1.0 || pn["start"] != "2027-03-01" {
		t.Fatalf("numeri del programma: wp=%v progress=%v milestone=%v start=%v", pn["work_packages"], pn["progress"], pn["milestones"], pn["start"])
	}
	var roadmap map[string]any
	for _, p := range prs {
		if m := p.(map[string]any); m["key"] == pk {
			roadmap = m
		}
	}
	if mss := roadmap["milestones"].([]any); len(mss) != 1 || mss[0].(map[string]any)["contractual"] != true || roadmap["progress"] != 30.0 {
		t.Fatalf("roadmap del progetto: %v", roadmap)
	}
	if pl := admin.do("GET", "/api/v1/programs", nil, http.StatusOK)["items"].([]any); len(pl) != 1 || pl[0].(map[string]any)["projects"] != 2.0 {
		t.Fatalf("elenco dei programmi: %v", pl)
	}
	// Il progetto mostra il suo programma; toglierlo lo toglie dal programma.
	if h := admin.do("GET", "/api/v1/projects/"+pk+"/plan", nil, http.StatusOK)["project"].(map[string]any); h["program"].(map[string]any)["key"] != pgk {
		t.Fatalf("programma nel piano: %v", h["program"])
	}
	admin.do("PUT", "/api/v1/projects/"+ok+"/program", map[string]any{"program": ""}, http.StatusOK)
	if prs := admin.do("GET", "/api/v1/programs/"+pgk, nil, http.StatusOK)["projects"].([]any); len(prs) != 1 {
		t.Fatalf("progetto tolto dal programma: %d", len(prs))
	}
	// Chi non legge i progetti non li vede nel programma (il fornitore esterno non legge nemmeno il programma).
	vendor.do("GET", "/api/v1/programs/"+pgk, nil, http.StatusNotFound)
}

// hasNotice dice se la persona ha una notifica del tipo e dell'evento indicati.
func hasNotice(u *user, kind, event string) bool {
	for _, it := range u.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"].([]any) {
		m := it.(map[string]any)
		if m["kind"] != kind {
			continue
		}
		if p, _ := m["payload"].(map[string]any); p != nil && p["event"] == event {
			return true
		}
	}
	return false
}

// countNotices conta le notifiche dei progetti con l'evento indicato.
func countNotices(t *testing.T, run func(func(tx pgx.Tx) error) error, event string) int {
	t.Helper()
	var n int
	if err := run(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "select count(*) from notifications where kind = 'project' and payload->>'event' = $1", event).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
