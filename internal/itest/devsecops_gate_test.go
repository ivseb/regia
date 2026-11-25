package itest

import (
	"context"
	"log/slog"
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
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// DevSecOps, fase 4 (piano, passo 21): gate di rilascio per le pipeline. Permesso
// devsecops.gate (ruolo "Pipeline"), regole per ambiente con motivi leggibili e link,
// modalità warn e block, anteprima di una bozza, decisioni registrate (audit, storia,
// stesse chiamate ripetute valgono una), override motivato con approvazione, evidenze
// automatiche dei controlli GRC mappati (rilasci con controlli block superati e decisioni
// superate), pannello per applicazione e richiesta, isolamento tra tenant.
func TestGateRilascio(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, slugB := "gate-"+stamp, "gateb-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Gate", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	add("lead", tenant.MemberInput{Roles: []string{"viewer", "assessor"}})
	add("ci", tenant.MemberInput{Roles: []string{"pipeline"}})
	add("lettore", tenant.MemberInput{Roles: []string{"viewer"}})
	add("co", tenant.MemberInput{Roles: []string{"compliance_officer"}, Groups: []string{"Compliance"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	gs := grc.Register(eng)
	// Martedì 1 dicembre 2026, le 9:00 a Roma (le 8:00 UTC).
	var clock atomic.Int64
	clock.Store(time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	rev.Now, rq.Now, dso.Now, gs.Now = now, now, now, now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, Devsecops: dso, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
		BaseURL: "https://regia.example.test",
	}).Handler())
	defer srv.Close()
	u := func(name, s string) *user { return login(t, srv.URL, name+"@"+s+".test", testPassword) }
	admin, req, man, sponsor, lead, ci, reader, co := u("admin", slug), u("req", slug), u("dm", slug), u("sponsor", slug), u("lead", slug), u("ci", slug), u("lettore", slug), u("co", slug)
	adminB := u("admin", slugB)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Il ruolo di sistema "Pipeline" ha il permesso del gate (anche nei tenant esistenti: migrazione).
	var perms []string
	if err := d.Owner.QueryRow(ctx, "select permissions from roles where tenant_id = $1 and key = 'pipeline'", ten.TenantID).Scan(&perms); err != nil ||
		len(perms) != 2 || perms[1] != "devsecops.gate" {
		t.Fatalf("permessi del ruolo Pipeline: %v %v", perms, err)
	}

	// --- Catalogo, richiesta con l'impatto su ORD (requisiti da evidenza SEC-02 e SEC-03).
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini", "lead_id": lead.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\nORD;Gestione ordini;critica;sì;personali;Ordini;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	appKey := ""
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		if obj(a)["code"] == "ORD" {
			appKey = obj(a)["key"].(string)
		}
	}
	appID := obj(admin.do("GET", "/api/v1/elements/"+appKey, nil, http.StatusOK)["element"])["id"].(string)
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Pagamento in tre rate", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": appID, "estimate_effort_days": 5}, http.StatusCreated)
	for _, to := range []string{"Inviata", "Triage", "Valutazione"} {
		u2 := man
		if to == "Inviata" {
			u2 = req
		}
		u2.do("POST", "/api/v1/elements/"+brID+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}

	// --- Token: il gate chiede devsecops.gate; devsecops.write da solo e element.read no.
	gateTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Gate", "scopes": []string{"devsecops.gate"}}, http.StatusCreated)["token"].(string)
	writeTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Invio", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	readTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	ciTok := ci.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI"}, http.StatusCreated)["token"].(string)
	push := func(path string, body map[string]any, want int) map[string]any {
		t.Helper()
		body["format"] = "regia.devsecops/v1"
		return bearerDo(t, srv.URL, ciTok, "POST", "/api/v1/devsecops/"+path, body, want)
	}
	ask := func(tok string, body map[string]any, want int) map[string]any {
		t.Helper()
		if _, ok := body["format"]; !ok {
			body["format"] = "regia.devsecops/v1"
		}
		return bearerDo(t, srv.URL, tok, "POST", "/api/v1/devsecops/gate", body, want)
	}
	ask(writeTok, map[string]any{"application": "ORD", "environment": "prod"}, http.StatusForbidden)
	ask(readTok, map[string]any{"application": "ORD", "environment": "prod"}, http.StatusForbidden)
	ask(gateTok, map[string]any{"application": "ORD", "environment": "prod", "format": "regia.devsecops/v0"}, http.StatusUnprocessableEntity)
	ask(gateTok, map[string]any{"environment": "prod"}, http.StatusUnprocessableEntity)                               // né applicazione né servizio
	ask(gateTok, map[string]any{"application": "INESISTENTE", "environment": "prod"}, http.StatusUnprocessableEntity) // sconosciuta
	ask(gateTok, map[string]any{"application": "ORD"}, http.StatusUnprocessableEntity)                                // manca l'ambiente

	// --- Senza regole l'ambiente passa (e la decisione si registra).
	g0 := ask(gateTok, map[string]any{"application": "ORD", "environment": "prod", "version": "1.4.0"}, http.StatusOK)
	if g0["result"] != "pass" || g0["mode"] != "none" || g0["decision_id"] == nil || !strings.HasPrefix(g0["url"].(string), "https://regia.example.test/e/"+appKey) {
		t.Fatalf("gate senza regole: %v", g0)
	}

	// --- Regole dell'ambiente: solo chi configura il modulo; controlli di validità.
	rule := func(kind, level string, params map[string]any) map[string]any {
		return map[string]any{"kind": kind, "enabled": true, "level": level, "params": params}
	}
	prodRules := []any{
		rule("requirements", "block", map[string]any{}),
		rule("vulnerabilities", "block", map[string]any{"severity": "high", "only_fixable": true, "max": 0, "require_report": true}),
		rule("change_approved", "block", map[string]any{"phases": []string{"Valutazione"}, "require_change": true}),
		rule("checks", "block", map[string]any{"names": []string{"secrets", "test"}}),
		rule("condition", "warn", map[string]any{"when": "cve_critiche_produzione: 0", "on": "application"}),
	}
	pol := func(mode string, rules []any) map[string]any {
		return map[string]any{"mode": mode, "rules": rules, "override_enabled": true, "override_hours": 24}
	}
	reader.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", prodRules), http.StatusForbidden)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("boh", prodRules), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", []any{rule("magia", "block", map[string]any{})}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", []any{rule("condition", "block", map[string]any{"when": "parola senza campo", "on": "application"})}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", []any{rule("window", "warn", map[string]any{"days": []int{9}, "from": "08:00", "to": "18:00", "timezone": "Europe/Rome"})}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", []any{rule("vulnerabilities", "block", map[string]any{"severity": "low"})}), http.StatusUnprocessableEntity)
	saved := admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", prodRules), http.StatusOK)
	if len(list(saved["rules"])) != 5 || obj(list(saved["rules"])[0])["id"] == "" {
		t.Fatalf("regole salvate: %v", saved)
	}
	cfg := reader.do("GET", "/api/v1/devsecops/gate/config", nil, http.StatusOK)
	if cfg["can_edit"] != false || len(list(cfg["policies"])) != 1 || len(list(cfg["request_phases"])) == 0 || len(list(cfg["default_rules"])) == 0 {
		t.Fatalf("configurazione del gate: %v", cfg)
	}

	// --- Dati della pipeline per orders-api 1.4.0: controlli del commit, SBOM e una CVE alta correggibile.
	svc := map[string]any{"name": "orders-api", "repository": "shop/orders-api", "provider": "github", "application": "ORD"}
	push("check-runs", map[string]any{"service": svc, "commit": "c0ffee1234567890", "run_id": "7", "verdict": "passed", "at": "2026-11-30T08:00:00Z", "checks": []any{
		map[string]any{"name": "test", "mode": "block", "outcome": "passed"}, map[string]any{"name": "secrets", "mode": "block", "outcome": "passed"}}}, http.StatusAccepted)
	push("vuln-reports", map[string]any{"service": svc, "version": "1.4.0", "scanner": "grype", "scanned_at": "2026-11-30T08:10:00Z", "vulnerabilities": []any{
		map[string]any{"id": "CVE-2026-40001", "package": "lodash", "purl": "pkg:npm/lodash@4.17.20", "version": "4.17.20", "fixed_in": "4.17.21", "severity": "high", "layer": "app"}}}, http.StatusAccepted)

	// --- Il gate dice fail con i motivi leggibili e i link; 409 con lo stesso corpo.
	ask1 := map[string]any{"application": "ORD", "environment": "prod", "version": "1.4.0", "commit": "c0ffee1234567890", "request": brKey, "service": "orders-api"}
	g1 := ask(gateTok, ask1, http.StatusConflict)
	reasons := map[string]map[string]any{}
	for _, r := range list(g1["reasons"]) {
		reasons[obj(r)["kind"].(string)] = obj(r)
	}
	if g1["result"] != "fail" || g1["mode"] != "block" || len(reasons) != 5 {
		t.Fatalf("gate fail: %v", g1)
	}
	if reasons["requirements"]["status"] != "fail" || !strings.Contains(reasons["requirements"]["message"].(string), brKey) ||
		!strings.HasPrefix(reasons["requirements"]["url"].(string), "https://regia.example.test/e/"+brKey) {
		t.Fatalf("motivo dei requisiti: %v", reasons["requirements"])
	}
	if reasons["vulnerabilities"]["status"] != "fail" || !strings.Contains(reasons["vulnerabilities"]["message"].(string), "orders-api 1.4.0: 1 CVE critiche o alte con correzione disponibile") {
		t.Fatalf("motivo delle CVE: %v", reasons["vulnerabilities"])
	}
	if reasons["change_approved"]["status"] != "pass" || reasons["checks"]["status"] != "pass" || reasons["condition"]["status"] != "pass" {
		t.Fatalf("regole superate: %v", reasons)
	}
	dec1 := g1["decision_id"].(string)
	// Ripetuta identica: la stessa decisione, non una nuova.
	if g1b := ask(gateTok, ask1, http.StatusConflict); g1b["decision_id"] != dec1 {
		t.Fatalf("decisione ripetuta: %v", g1b["decision_id"])
	}
	var n int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'devsecops.gate' and payload->>'result' = 'fail'`, ten.TenantID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit delle decisioni fail: %d %v", n, err)
	}
	if err := d.Owner.QueryRow(ctx, `select count(*) from activities where tenant_id = $1 and kind = 'gate' and element_id in ($2, $3)`, ten.TenantID, appID, brID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("storia (applicazione e richiesta, più la decisione senza regole): %d %v", n, err)
	}

	// --- Applicazione dal servizio, change dal numero (sconosciuto: la regola del change non passa).
	g2 := ask(gateTok, map[string]any{"service": "orders-api", "environment": "prod", "version": "1.4.0", "change": "CHG0099999"}, http.StatusConflict)
	for _, r := range list(g2["reasons"]) {
		if obj(r)["kind"] == "change_approved" && !strings.Contains(obj(r)["message"].(string), "CHG0099999") {
			t.Fatalf("change sconosciuto: %v", r)
		}
	}
	if obj(g2["application"])["key"] != appKey {
		t.Fatalf("applicazione dal servizio: %v", g2["application"])
	}

	// --- Anteprima: non registra nulla; con una bozza in modalità avviso il fail diventa warn.
	var before int
	_ = d.Owner.QueryRow(ctx, "select count(*) from gate_decisions where tenant_id = $1", ten.TenantID).Scan(&before)
	pv := reader.do("POST", "/api/v1/devsecops/gate/preview", ask1, http.StatusOK)
	if pv["result"] != "fail" || pv["decision_id"] != nil {
		t.Fatalf("anteprima: %v", pv)
	}
	draftBody := map[string]any{"application": "ORD", "environment": "prod", "request": brKey, "version": "1.4.0", "service": "orders-api", "commit": "c0ffee1234567890",
		"policy": pol("warn", prodRules)}
	if pw := reader.do("POST", "/api/v1/devsecops/gate/preview", draftBody, http.StatusOK); pw["result"] != "warn" || pw["raw_result"] != "fail" {
		t.Fatalf("anteprima in avviso: %v", pw)
	}
	var after int
	_ = d.Owner.QueryRow(ctx, "select count(*) from gate_decisions where tenant_id = $1", ten.TenantID).Scan(&after)
	if after != before {
		t.Fatalf("l'anteprima ha registrato decisioni: %d → %d", before, after)
	}
	req.do("POST", "/api/v1/devsecops/gate/preview", ask1, http.StatusForbidden) // il richiedente del portale non legge gli elementi

	// --- Modalità warn salvata: non blocca (200 warn).
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("warn", prodRules), http.StatusOK)
	if gw := ask(gateTok, ask1, http.StatusOK); gw["result"] != "warn" || gw["raw_result"] != "fail" {
		t.Fatalf("gate in avviso: %v", gw)
	}
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", pol("block", prodRules), http.StatusOK)
	g1c := ask(gateTok, ask1, http.StatusConflict)
	dec1 = g1c["decision_id"].(string)

	// --- Finestra di rilascio: martedì alle 9:00 (Roma) dentro lun–ven 08–18; fuori il sabato e la notte; blocco di calendario.
	win := func(days []int, from, to string, black []any) map[string]any {
		p := pol("block", []any{rule("window", "block", map[string]any{"days": days, "from": from, "to": to, "timezone": "Europe/Rome", "blackouts": black})})
		return reader.do("POST", "/api/v1/devsecops/gate/preview", map[string]any{"application": "ORD", "environment": "prod", "policy": p}, http.StatusOK)
	}
	if w := win([]int{1, 2, 3, 4, 5}, "08:00", "18:00", []any{}); w["result"] != "pass" {
		t.Fatalf("finestra aperta: %v", w)
	}
	if w := win([]int{6, 7}, "08:00", "18:00", []any{}); w["result"] != "fail" || !strings.Contains(obj(list(w["reasons"])[0])["message"].(string), "oggi è mar") {
		t.Fatalf("finestra nei giorni sbagliati: %v", w)
	}
	if w := win([]int{}, "10:00", "18:00", []any{}); w["result"] != "fail" {
		t.Fatalf("finestra oltre l'orario: %v", w)
	}
	if w := win([]int{}, "22:00", "07:00", []any{}); w["result"] != "fail" { // a cavallo della mezzanotte: le 9 sono fuori
		t.Fatalf("finestra notturna: %v", w)
	}
	if w := win([]int{}, "08:00", "18:00", []any{map[string]any{"from": "2026-11-30", "to": "2026-12-02", "label": "Chiusura contabile"}}); w["result"] != "fail" ||
		!strings.Contains(obj(list(w["reasons"])[0])["message"].(string), "Chiusura contabile") {
		t.Fatalf("blocco di calendario: %v", w)
	}

	// --- Override: serve la politica degli approvatori, la motivazione, e una decisione fail.
	man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": "Hotfix urgente"}, http.StatusUnprocessableEntity) // nessun approvatore
	reader.do("PUT", "/api/v1/devsecops/gate/approvers", map[string]any{"approvers": []any{map[string]any{"member": sponsor.member}}}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/devsecops/gate/approvers", map[string]any{"approvers": []any{map[string]any{"member": sponsor.member, "role": "sponsor"}}}, http.StatusUnprocessableEntity)
	ap1 := admin.do("PUT", "/api/v1/devsecops/gate/approvers", map[string]any{"approvers": []any{map[string]any{"member": sponsor.member}}}, http.StatusOK)
	if len(list(ap1["approvers"])) != 1 {
		t.Fatalf("approvatori: %v", ap1)
	}
	man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": " "}, http.StatusUnprocessableEntity)
	man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": "x", "hours": 100}, http.StatusUnprocessableEntity)        // oltre il massimo dell'ambiente
	man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": g0["decision_id"], "reason": "Non serve"}, http.StatusUnprocessableEntity) // la decisione era pass
	reader.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": "Provo"}, http.StatusForbidden)                         // il lettore non vede l'applicazione per scrivere? vedi sotto
	ov := man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": "Hotfix urgente sul pagamento", "hours": 12}, http.StatusCreated)
	if ov["status"] != "pending" || ov["environment"] != "prod" || int(ov["hours"].(float64)) != 12 {
		t.Fatalf("override richiesto: %v", ov)
	}
	man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": dec1, "reason": "Doppione"}, http.StatusUnprocessableEntity) // già in attesa
	if gp := ask(gateTok, ask1, http.StatusConflict); gp["result"] != "fail" {
		t.Fatalf("override in attesa non basta: %v", gp)
	}
	stepOf := func(u *user, key string) map[string]any {
		det := u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)
		for _, a := range list(det["approvals"]) {
			if obj(a)["status"] != "pending" {
				continue
			}
			for _, st := range list(obj(a)["steps"]) {
				if obj(st)["decision"] == "pending" {
					return obj(st)
				}
			}
		}
		t.Fatalf("nessun passo di approvazione attivo su %s", key)
		return nil
	}
	st := stepOf(admin, appKey)
	man.do("POST", "/api/v1/approvals/steps/"+st["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)  // chi chiede non decide
	lead.do("POST", "/api/v1/approvals/steps/"+st["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden) // non è l'approvatore
	sponsor.do("POST", "/api/v1/approvals/steps/"+st["id"].(string)+"/decision", map[string]any{"decision": "approved", "comment": "Ok, con la correzione al prossimo giro"}, http.StatusNoContent)
	g3 := ask(gateTok, ask1, http.StatusOK)
	if g3["result"] != "pass" || g3["overridden"] != true || g3["raw_result"] != "fail" || obj(g3["override"])["id"] != ov["id"] {
		t.Fatalf("gate con override approvato: %v", g3)
	}
	overridden := 0
	for _, r := range list(g3["reasons"]) {
		if obj(r)["overridden"] == true {
			overridden++
		}
	}
	if overridden != 2 {
		t.Fatalf("motivi superati dall'override: %d", overridden)
	}
	// L'override vale per quella versione: un'altra non è coperta; scade dopo le ore dichiarate.
	if gv := ask(gateTok, map[string]any{"application": "ORD", "environment": "prod", "version": "1.4.1", "request": brKey}, http.StatusConflict); gv["overridden"] == true {
		t.Fatalf("override su un'altra versione: %v", gv)
	}
	clock.Add(13 * 3600)
	if ge := ask(gateTok, ask1, http.StatusConflict); ge["result"] != "fail" {
		t.Fatalf("override scaduto: %v", ge)
	}
	clock.Add(-13 * 3600)
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action in ('devsecops.gate_override_request', 'devsecops.gate_override_approved')`, ten.TenantID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit dell'override: %d %v", n, err)
	}
	// Revoca da chi configura: torna fail.
	reader.do("POST", "/api/v1/devsecops/gate/overrides/"+ov["id"].(string)+"/revoke", nil, http.StatusForbidden)
	admin.do("POST", "/api/v1/devsecops/gate/overrides/"+ov["id"].(string)+"/revoke", nil, http.StatusNoContent)
	if gr := ask(gateTok, ask1, http.StatusConflict); gr["overridden"] == true {
		t.Fatalf("override revocato: %v", gr)
	}

	// --- Override respinto: non cambia l'esito.
	g4 := ask(gateTok, ask1, http.StatusConflict)
	ov2 := man.do("POST", "/api/v1/devsecops/gate/overrides", map[string]any{"decision": g4["decision_id"], "reason": "Secondo tentativo"}, http.StatusCreated)
	sponsor.do("POST", "/api/v1/approvals/steps/"+stepOf(admin, appKey)["id"].(string)+"/decision", map[string]any{"decision": "rejected", "comment": "No"}, http.StatusNoContent)
	if gj := ask(gateTok, ask1, http.StatusConflict); gj["overridden"] == true {
		t.Fatalf("override respinto: %v", gj)
	}
	_ = ov2

	// --- Evidenze GRC: controllo mappato ai controlli della pipeline e alla decisione superata.
	co.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusCreated)
	ctl := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Scansione dei segreti nel rilascio",
		"requirements": []any{map[string]any{"ref": "dora:Art. 12"}}}, http.StatusCreated)
	ctlKey := ctl["key"].(string)
	ctl2 := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Gate di rilascio approvato",
		"requirements": []any{map[string]any{"ref": "dora:Art. 11"}}}, http.StatusCreated)
	ctl2Key := ctl2["key"].(string)
	reader.do("PUT", "/api/v1/devsecops/gate/evidence-map", map[string]any{"items": []any{}}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/devsecops/gate/evidence-map", map[string]any{"items": []any{map[string]any{"source": "check:secrets", "control": "CTL-999"}}}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/evidence-map", map[string]any{"items": []any{map[string]any{"source": "boh", "control": ctlKey}}}, http.StatusUnprocessableEntity)
	em := admin.do("PUT", "/api/v1/devsecops/gate/evidence-map", map[string]any{"items": []any{
		map[string]any{"source": "check:secrets", "control": ctlKey, "valid_days": 90, "production_only": true},
		map[string]any{"source": "gate", "control": ctl2Key, "production_only": false},
	}}, http.StatusOK)
	if len(list(em["items"])) != 2 || len(list(em["controls"])) < 2 || !strings.Contains(jsonString(em["check_names"]), "secrets") {
		t.Fatalf("mappatura delle evidenze: %v", em)
	}
	// Un rilascio in staging non genera l'evidenza (solo produzione); uno in produzione sì, con scadenza a 90 giorni.
	push("releases", map[string]any{"service": svc, "version": "1.4.0", "environment": "staging", "commit": "c0ffee1234567890", "at": "2026-11-30T09:00:00Z"}, http.StatusAccepted)
	evCount := func(title string) int {
		c := 0
		for _, e := range list(co.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK)["items"]) {
			if strings.Contains(obj(e)["title"].(string), title) {
				c++
			}
		}
		return c
	}
	if evCount("Rilascio orders-api 1.4.0") != 0 {
		t.Fatal("evidenza da un rilascio in staging")
	}
	rel := map[string]any{"service": svc, "version": "1.4.0", "environment": "prod", "commit": "c0ffee1234567890", "at": "2026-12-01T07:30:00Z",
		"release_url": "https://git.example.test/shop/orders-api/releases/1.4.0", "checks": []any{
			map[string]any{"name": "test", "mode": "block", "outcome": "passed"}, map[string]any{"name": "secrets", "mode": "block", "outcome": "passed"}}}
	push("releases", rel, http.StatusAccepted)
	push("releases", rel, http.StatusOK)
	if evCount("Rilascio orders-api 1.4.0 in prod") != 1 {
		t.Fatalf("evidenza del rilascio: %v", co.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK))
	}
	var evKey string
	for _, e := range list(co.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK)["items"]) {
		if strings.Contains(obj(e)["title"].(string), "Rilascio orders-api 1.4.0 in prod") {
			evKey = obj(e)["key"].(string)
			if !strings.Contains(jsonString(obj(e)["controls"]), ctlKey) {
				t.Fatalf("controlli dell'evidenza: %v", e)
			}
		}
	}
	ev := co.do("GET", "/api/v1/grc/evidence/"+evKey, nil, http.StatusOK)
	if ev["status"] != "valid" || ev["kind"] != "release" || ev["signature_ok"] != true || !strings.Contains(jsonString(ev["valid_until"]), "2027-03-01") {
		t.Fatalf("evidenza del rilascio: %v", ev)
	}
	// Staging: una regola sola, la decisione superata diventa un'evidenza del secondo controllo.
	admin.do("PUT", "/api/v1/devsecops/gate/policies/staging", pol("block", []any{rule("checks", "block", map[string]any{"names": []string{"secrets"}})}), http.StatusOK)
	gs1 := ask(gateTok, map[string]any{"application": "ORD", "environment": "staging", "version": "1.4.0", "commit": "c0ffee1234567890", "service": "orders-api"}, http.StatusOK)
	if gs1["result"] != "pass" || gs1["mode"] != "block" {
		t.Fatalf("gate di staging: %v", gs1)
	}
	if evCount("Gate superato: "+appKey+" in staging") != 1 {
		t.Fatalf("evidenza della decisione superata: %v", co.do("GET", "/api/v1/grc/evidence", nil, http.StatusOK))
	}
	ask(gateTok, map[string]any{"application": "ORD", "environment": "staging", "version": "1.4.0", "commit": "c0ffee1234567890", "service": "orders-api"}, http.StatusOK)
	if evCount("Gate superato: "+appKey+" in staging") != 1 {
		t.Fatal("la stessa decisione ha prodotto due evidenze")
	}
	// Una decisione non superata (fail) non è un'evidenza.
	ask(gateTok, ask1, http.StatusConflict)
	if evCount("Gate superato: "+appKey+" in prod") != 0 {
		t.Fatal("evidenza da una decisione fail")
	}
	// Un ambiente senza regole proprie usa "*".
	admin.do("PUT", "/api/v1/devsecops/gate/policies/*", pol("off", []any{}), http.StatusOK)
	if gq := ask(gateTok, map[string]any{"application": "ORD", "environment": "qa"}, http.StatusOK); gq["result"] != "pass" || gq["mode"] != "off" {
		t.Fatalf("ambiente con la regola generale: %v", gq)
	}
	admin.do("DELETE", "/api/v1/devsecops/gate/policies/*", nil, http.StatusNoContent)
	admin.do("DELETE", "/api/v1/devsecops/gate/policies/*", nil, http.StatusNotFound)

	// --- Pannello dell'applicazione e della richiesta; elenco delle decisioni.
	pa := reader.do("GET", "/api/v1/devsecops/elements/"+appKey+"/gate", nil, http.StatusOK)
	if pa["configured"] != true || len(list(pa["cells"])) != 2 || len(list(pa["decisions"])) == 0 || len(list(pa["overrides"])) < 2 {
		t.Fatalf("pannello dell'applicazione: %v", pa)
	}
	pr := reader.do("GET", "/api/v1/devsecops/elements/"+brKey+"/gate", nil, http.StatusOK)
	if len(list(pr["cells"])) != 2 || obj(pr["subject"])["type"] != "business_request" {
		t.Fatalf("pannello della richiesta: %v", pr)
	}
	reader.do("GET", "/api/v1/devsecops/elements/"+strings.ToUpper(strings.Split(appKey, "-")[0])+"-999999/gate", nil, http.StatusNotFound)
	fails := list(reader.do("GET", "/api/v1/devsecops/gate/decisions?result=fail&limit=5", nil, http.StatusOK)["items"])
	if len(fails) == 0 || len(fails) > 5 || obj(fails[0])["result"] != "fail" || obj(obj(fails[0])["application"])["key"] != appKey {
		t.Fatalf("decisioni fail: %v", fails)
	}
	req.do("GET", "/api/v1/devsecops/gate/decisions", nil, http.StatusForbidden)

	// --- Isolamento tra tenant.
	if l := list(adminB.do("GET", "/api/v1/devsecops/gate/decisions", nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("decisioni di un altro tenant: %v", l)
	}
	if cb := adminB.do("GET", "/api/v1/devsecops/gate/config", nil, http.StatusOK); len(list(cb["policies"])) != 0 {
		t.Fatalf("regole di un altro tenant: %v", cb["policies"])
	}
	adminB.do("GET", "/api/v1/devsecops/elements/"+appKey+"/gate", nil, http.StatusNotFound)
}
