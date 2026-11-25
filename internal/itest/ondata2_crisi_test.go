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

// Ondata 2, crisi e dal problema all'azione (replica/usabilita.md, proposte 3 e 5; migrazione 0056):
//   - la vista di crisi dell'incidente (prossima azione, applicazioni con ultimo rilascio e
//     vulnerabilità, fornitori, cronologia con le scadenze future, bozza con i campi mancanti);
//   - il lavoro creato da una vulnerabilità, un blocco del gate, un requisito o un'aderenza
//     mancante, con il collegamento visibile dal problema e dal lavoro.
func TestOndata2Crisi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("on2-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Ondata 2", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	})
	if err != nil {
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
	add("ed", tenant.MemberInput{Roles: []string{"editor"}})
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
	admin, rm, im, ed, viewer := u("admin"), u("rm"), u("im"), u("ed"), u("viewer")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	eid := func(key string) string {
		return obj(admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"])["id"].(string)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := d.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	// --- Catalogo: team Ordini (con il suo spazio) e applicazione che lo ha come titolare.
	team := admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini"}, http.StatusCreated)
	teamID := team["id"].(string)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"ORD;Gestione ordini;critica;sì;personali;Ordini;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	appKey := "ORD"
	appID := eid(appKey)
	appKey = obj(admin.do("GET", "/api/v1/elements/"+appID, nil, http.StatusOK)["element"])["key"].(string)

	// Dati di DevSecOps scritti dal proprietario: servizio, ambiente, rilascio in produzione,
	// una vulnerabilità aperta e già scaduta.
	var svcID, envID, findingID string
	now := time.Now().UTC()
	if err := d.Owner.QueryRow(ctx, `insert into services (tenant_id, id, name, application_element_id) values ($1, gen_random_uuid(), 'ordini-api', $2) returning id::text`, ten.TenantID, appID).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	if err := d.Owner.QueryRow(ctx, `insert into environments (tenant_id, id, name, is_production) values ($1, gen_random_uuid(), 'produzione', true) returning id::text`, ten.TenantID).Scan(&envID); err != nil {
		t.Fatal(err)
	}
	exec(`insert into releases (tenant_id, id, service_id, environment_id, version, released_at, payload_sha256) values ($1, gen_random_uuid(), $2, $3, '2.4.0', $4, 'x')`,
		ten.TenantID, svcID, envID, now.Add(-3*time.Hour))
	if err := d.Owner.QueryRow(ctx, `insert into findings (tenant_id, id, fingerprint, source, cve, severity, application_element_id, package, installed_version, fixed_in, due_at)
		values ($1, gen_random_uuid(), 'fp-on2', 'test', 'CVE-2026-1111', 'critical', $2, 'libssl', '3.0.1', '3.0.9', $3) returning id::text`,
		ten.TenantID, appID, now.Add(-48*time.Hour)).Scan(&findingID); err != nil {
		t.Fatal(err)
	}

	// === Vista di crisi ======================================================================
	prov := rm.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Nuvola Calcolo"}, http.StatusCreated)
	inc := im.do("POST", "/api/v1/incidents", map[string]any{
		"title": "Pagamenti rifiutati", "description": "Errori sul checkout.", "detected_at": now.Add(-time.Hour).Format(time.RFC3339),
		"clients_affected": 180000, "clients_affected_pct": 14, "economic_impact_cents": 21000000, "cause": "provider", "cause_provider": prov["key"],
		"impacted": []string{appKey},
	}, http.StatusCreated)
	incKey := inc["key"].(string)
	crisis := func(key string) map[string]any {
		return im.do("GET", "/api/v1/incidents/"+key+"/crisis", nil, http.StatusOK)
	}

	c := crisis(incKey)
	if obj(c["next"])["kind"] != "classify" {
		t.Fatalf("un incidente non classificato chiede prima la classificazione: %s", jsonString(c["next"]))
	}
	apps := list(c["applications"])
	if len(apps) != 1 {
		t.Fatalf("applicazioni colpite: %s", jsonString(c["applications"]))
	}
	a := obj(apps[0])
	if obj(a["ref"])["key"] != appKey || a["team"] != "Ordini" {
		t.Fatalf("applicazione o team sbagliati: %s", jsonString(a))
	}
	if rel := obj(a["last_release"]); rel["version"] != "2.4.0" || rel["suspect"] != true {
		t.Fatalf("ultimo rilascio, sospetto perché prima della rilevazione: %s", jsonString(a["last_release"]))
	}
	if v := obj(a["vulns"]); v["open"] != float64(1) || v["overdue"] != float64(1) || v["critical"] != float64(1) {
		t.Fatalf("vulnerabilità aperte: %s", jsonString(a["vulns"]))
	}
	provs := list(c["providers"])
	if len(provs) != 1 || obj(provs[0])["cause"] != true || obj(obj(provs[0])["ref"])["key"] != prov["key"] {
		t.Fatalf("fornitore causa dell'incidente: %s", jsonString(c["providers"]))
	}
	// La cronologia ha il rilascio e la vulnerabilità che precedono l'incidente.
	kinds := map[string]bool{}
	for _, it := range list(c["timeline"]) {
		kinds[obj(it)["kind"].(string)] = true
	}
	if !kinds["release"] || !kinds["detected"] {
		t.Fatalf("cronologia: %s", jsonString(c["timeline"]))
	}

	// Classificato: la notifica da inviare è la prossima azione, con la scadenza futura come segnaposto.
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Classificato"}, http.StatusOK)
	c = crisis(incKey)
	if obj(c["next"])["kind"] != "notify" || obj(c["next"])["due_at"] == nil {
		t.Fatalf("dopo la classificazione la prossima azione è la notifica: %s", jsonString(c["next"]))
	}
	future := 0
	for _, it := range list(c["timeline"]) {
		if obj(it)["kind"] == "deadline" && obj(it)["future"] == true {
			future++
		}
	}
	if future == 0 {
		t.Fatalf("nessuna scadenza futura nella cronologia: %s", jsonString(c["timeline"]))
	}
	draft := obj(c["draft"])
	if obj(draft["notification"]) == nil {
		t.Fatalf("la bozza porta la notifica in scadenza: %s", jsonString(draft))
	}
	fields := map[string]map[string]any{}
	for _, f := range list(draft["fields"]) {
		fields[obj(f)["key"].(string)] = obj(f)
	}
	if fields["clients"]["missing"] != false || fields["downtime"]["missing"] != true || fields["downtime"]["required"] != true {
		t.Fatalf("campi della bozza: clients=%v downtime=%v", fields["clients"], fields["downtime"])
	}
	if draft["missing"].(float64) < 1 {
		t.Fatalf("i campi obbligatori mancanti non sono contati: %v", draft["missing"])
	}
	// Con il tempo di fermo indicato il campo non manca più.
	im.do("PATCH", "/api/v1/incidents/"+incKey, map[string]any{"downtime_minutes": 190}, http.StatusOK)
	f2 := map[string]any{}
	for _, f := range list(obj(crisis(incKey)["draft"])["fields"]) {
		if obj(f)["key"] == "downtime" {
			f2 = obj(f)
		}
	}
	if f2["missing"] != false || f2["value"] != "190" {
		t.Fatalf("tempo di fermo dopo l'aggiornamento: %v", f2)
	}

	// Decisioni e comunicazioni sono eventi a sé; un tipo sconosciuto no.
	im.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "decision", "text": "Si passa al sito di ripristino"}, http.StatusOK)
	im.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "communication", "text": "Avviso ai clienti sul sito"}, http.StatusOK)
	im.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "pippo", "text": "x"}, http.StatusUnprocessableEntity)
	c = crisis(incKey)
	if len(list(c["decisions"])) != 1 || len(list(c["communications"])) != 1 {
		t.Fatalf("decisioni e comunicazioni: %s / %s", jsonString(c["decisions"]), jsonString(c["communications"]))
	}
	// Chi non gestisce incidenti legge la vista ma non registra.
	viewer.do("GET", "/api/v1/incidents/"+incKey+"/crisis", nil, http.StatusOK)
	viewer.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "decision", "text": "no"}, http.StatusForbidden)
	im.do("GET", "/api/v1/incidents/INC-99999/crisis", nil, http.StatusNotFound)

	// === Dal problema all'azione =============================================================
	const P = "/api/v1/problems"
	ctxF := ed.do("GET", P+"/context?kind=finding&ref="+findingID, nil, http.StatusOK)
	if obj(ctxF["application"])["key"] != appKey || obj(ctxF["team"])["name"] != "Ordini" || obj(ctxF["team"])["has_space"] != true {
		t.Fatalf("contesto della vulnerabilità: %s", jsonString(ctxF))
	}
	if !strings.Contains(ctxF["title"].(string), "CVE-2026-1111") || !strings.Contains(ctxF["body"].(string), "libssl") || !strings.Contains(ctxF["reason"].(string), "CVE-2026-1111") {
		t.Fatalf("testi proposti: %s / %s / %s", ctxF["title"], ctxF["body"], ctxF["reason"])
	}
	if len(list(ctxF["links"])) != 0 || obj(ctxF["can"])["work_item"] != true {
		t.Fatalf("collegamenti iniziali: %s", jsonString(ctxF["links"]))
	}
	if obj(viewer.do("GET", P+"/context?kind=finding&ref="+findingID, nil, http.StatusOK)["can"])["work_item"] != false {
		t.Fatalf("chi sola legge non crea lavoro")
	}
	viewer.do("POST", P+"/work-items", map[string]any{"kind": "finding", "ref": findingID}, http.StatusForbidden)
	ed.do("GET", P+"/context?kind=finding&ref=00000000-0000-0000-0000-000000000000", nil, http.StatusUnprocessableEntity)
	ed.do("GET", P+"/context?kind=boh&ref=x", nil, http.StatusUnprocessableEntity)
	// Il team proposto è quello dell'applicazione, ma si può scegliere un altro; un team senza spazio
	// di lavoro lo riceve al momento (come alla creazione).
	ed.do("POST", P+"/work-items", map[string]any{"kind": "finding", "ref": findingID, "team_id": "00000000-0000-0000-0000-000000000000"}, http.StatusUnprocessableEntity)
	teamless := admin.do("POST", "/api/v1/teams", map[string]any{"name": "Senza spazio"}, http.StatusCreated)
	exec("update teams set delivery_space_id = null where id::text = $1", teamless["id"])
	other := ed.do("POST", P+"/work-items", map[string]any{"kind": "finding", "ref": findingID, "team_id": teamless["id"], "title": "Altro lavoro"}, http.StatusCreated)
	var hasSpace bool
	if err := d.Owner.QueryRow(ctx, "select delivery_space_id is not null from teams where id::text = $1", teamless["id"]).Scan(&hasSpace); err != nil || !hasSpace || other["element"] == "" {
		t.Fatalf("spazio del team creato al volo: %v %v %v", hasSpace, err, other)
	}

	w := ed.do("POST", P+"/work-items", map[string]any{"kind": "finding", "ref": findingID}, http.StatusCreated)
	taskKey := w["element"].(string)
	if taskKey == "" || w["action"] != "work_item" {
		t.Fatalf("lavoro creato: %s", jsonString(w))
	}
	task := ed.do("GET", "/api/v1/elements/"+taskKey, nil, http.StatusOK)
	if obj(task["element"])["type"] != "task" || !strings.Contains(obj(task["element"])["title"].(string), "CVE-2026-1111") {
		t.Fatalf("elemento di lavoro: %s", jsonString(task["element"]))
	}
	// Dal lato del lavoro: l'applicazione è nei collegamenti e il problema d'origine si legge.
	links := ed.do("GET", "/api/v1/elements/"+taskKey+"/links", nil, http.StatusOK)
	rels := list(links["relations"])
	if len(rels) == 0 || obj(rels[0])["key"] != appKey || obj(rels[0])["relation"] != "nasce da" {
		t.Fatalf("collegamento nel grafo dal lavoro all'applicazione: %s", jsonString(links))
	}
	// E dal lato dell'applicazione il lavoro compare con il verso opposto.
	appLinks := ed.do("GET", "/api/v1/elements/"+appKey+"/links", nil, http.StatusOK)
	if !strings.Contains(jsonString(appLinks["relations"]), taskKey) {
		t.Fatalf("il lavoro non compare tra i collegamenti dell'applicazione: %s", jsonString(appLinks))
	}
	origins := list(ed.do("GET", "/api/v1/elements/"+taskKey+"/problems", nil, http.StatusOK)["items"])
	if len(origins) != 1 || obj(origins[0])["kind"] != "finding" || obj(origins[0])["ref"] != findingID || obj(obj(origins[0])["application"])["key"] != appKey {
		t.Fatalf("origine del lavoro: %s", jsonString(origins))
	}
	// Dal lato del problema: il lavoro è tra i collegamenti.
	ctxF = ed.do("GET", P+"/context?kind=finding&ref="+findingID, nil, http.StatusOK)
	if links := list(ctxF["links"]); len(links) != 2 || obj(links[1])["element"] != taskKey {
		t.Fatalf("collegamenti dal problema: %s", jsonString(ctxF["links"]))
	}
	// L'eccezione richiesta da un altro modulo compare tra i collegamenti senza riscrivere nulla.
	var excID string
	if err := d.Owner.QueryRow(ctx, `insert into vuln_exceptions (tenant_id, id, number, application_id, reason, until, status) values ($1, gen_random_uuid(), 7, $2, 'Non raggiungibile', current_date + 30, 'pending') returning id::text`, ten.TenantID, appID).Scan(&excID); err != nil {
		t.Fatal(err)
	}
	exec(`insert into vuln_exception_findings (tenant_id, exception_id, finding_id) values ($1, $2, $3)`, ten.TenantID, excID, findingID)
	ctxF = ed.do("GET", P+"/context?kind=finding&ref="+findingID, nil, http.StatusOK)
	var sawExc bool
	for _, l := range list(ctxF["links"]) {
		if obj(l)["action"] == "exception" && obj(l)["ref"] == "EV-7" && obj(l)["derived"] == true && obj(l)["status"] == "pending" {
			sawExc = true
		}
	}
	if !sawExc || len(list(ctxF["links"])) != 3 {
		t.Fatalf("eccezione tra i collegamenti: %s", jsonString(ctxF["links"]))
	}

	// Gate: il motivo del blocco diventa titolo, descrizione e motivo dell'override.
	var decID string
	if err := d.Owner.QueryRow(ctx, `insert into gate_decisions (tenant_id, id, application_element_id, environment, version, outcome, raw_outcome, mode, digest, reasons)
		values ($1, gen_random_uuid(), $2, 'produzione', '2.5.0', 'fail', 'fail', 'block', 'd1',
		'[{"rule":"vuln","kind":"vulnerabilities","label":"Vulnerabilità","status":"fail","message":"1 vulnerabilità critica oltre la scadenza"}]') returning id::text`, ten.TenantID, appID).Scan(&decID); err != nil {
		t.Fatal(err)
	}
	ctxG := ed.do("GET", P+"/context?kind=gate_decision&ref="+decID, nil, http.StatusOK)
	if !strings.Contains(ctxG["reason"].(string), "1 vulnerabilità critica oltre la scadenza") || !strings.Contains(ctxG["title"].(string), "produzione") {
		t.Fatalf("contesto del gate: %s", jsonString(ctxG))
	}
	exec(`insert into gate_overrides (tenant_id, id, decision_id, application_element_id, environment, version, reason, hours, status) values ($1, gen_random_uuid(), $2, $3, 'produzione', '2.5.0', 'Urgente', 24, 'pending')`,
		ten.TenantID, decID, appID)
	ed.do("POST", P+"/work-items", map[string]any{"kind": "gate_decision", "ref": decID, "title": "Sbloccare ordini-api"}, http.StatusCreated)
	ctxG = ed.do("GET", P+"/context?kind=gate_decision&ref="+decID, nil, http.StatusOK)
	actions := map[string]bool{}
	for _, l := range list(ctxG["links"]) {
		actions[obj(l)["action"].(string)] = true
	}
	if !actions["override"] || !actions["work_item"] {
		t.Fatalf("collegamenti del gate: %s", jsonString(ctxG["links"]))
	}

	// Aderenza: il lavoro e la pull request di adozione si collegano alla stessa applicazione.
	ctxA := ed.do("GET", P+"/context?kind=adherence&ref="+appKey+"&control=SAST", nil, http.StatusOK)
	if obj(ctxA["application"])["key"] != appKey || !strings.Contains(ctxA["body"].(string), "SAST") {
		t.Fatalf("contesto dell'aderenza: %s", jsonString(ctxA))
	}
	ed.do("POST", P+"/links", map[string]any{"kind": "adherence", "ref": appKey, "action": "adoption_pr", "target_ref": "https://git.example.test/org/ordini/pull/12", "target_label": "Adozione della pipeline"}, http.StatusCreated)
	ed.do("POST", P+"/links", map[string]any{"kind": "adherence", "ref": appKey, "action": "work_item", "target_ref": "x"}, http.StatusUnprocessableEntity)
	ed.do("POST", P+"/links", map[string]any{"kind": "adherence", "ref": appKey, "action": "adoption_pr"}, http.StatusUnprocessableEntity)
	viewer.do("POST", P+"/links", map[string]any{"kind": "adherence", "ref": appKey, "action": "adoption_pr", "target_ref": "https://x.test/pr/1"}, http.StatusForbidden)
	ctxA = ed.do("GET", P+"/context?kind=adherence&ref="+appKey, nil, http.StatusOK)
	if links := list(ctxA["links"]); len(links) != 1 || obj(links[0])["url"] != "https://git.example.test/org/ordini/pull/12" {
		t.Fatalf("collegamento della pull request: %s", jsonString(ctxA["links"]))
	}
	_ = teamID
}
