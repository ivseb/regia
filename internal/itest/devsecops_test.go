package itest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// DevSecOps, fase 1 (piano, passo 21): push con token e permesso devsecops.write (e rifiuto
// senza), idempotenza, SBOM CycloneDX e SPDX (anche gzip e oltre 1 MiB), CVE al rilascio e
// di oggi con deduplica, change collegato alla richiesta e numero provvisorio come alias,
// verifica automatica dei requisiti da evidenza, fatti, pannello con il confronto a sette
// giorni, ambienti, configurazione, isolamento tra tenant.
func TestDevsecops(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, slugB := "dso-"+stamp, "dsob-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "DevSecOps", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision})
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

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	rev.Now, rq.Now, dso.Now = now, now, now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, Devsecops: dso,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name, s string) *user { return login(t, srv.URL, name+"@"+s+".test", testPassword) }
	admin, req, man, sponsor, lead, ci, reader := u("admin", slug), u("req", slug), u("dm", slug), u("sponsor", slug), u("lead", slug), u("ci", slug), u("lettore", slug)
	adminB := u("admin", slugB)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	num := func(v any) int { f, _ := v.(float64); return int(f) }

	// --- Catalogo: due applicazioni nell'asset "Commercio elettronico", una critica e DORA.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini", "lead_id": lead.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"ORD;Gestione ordini;critica;sì;personali;Ordini;Commercio elettronico\n" +
		"WEB;Vetrina web;media;no;pubblici;Ordini;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]map[string]any{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)
	}
	assetKey := obj(list(admin.do("GET", "/api/v1/architecture/assets", nil, http.StatusOK)["items"])[0])["key"].(string)

	// --- Richiesta con l'impatto su ORD: le baseline creano SEC-02 (secret_scan) e SEC-03 (sca_block).
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Pagamento in tre rate", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	imp := man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps["ORD"]["id"], "estimate_effort_days": 5}, http.StatusCreated)
	impKey := imp["key"].(string)
	move := func(u *user, id, to string, status int) {
		u.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)
	mr := man.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Nessun segreto nelle immagini", "scope": "application",
		"impact": impKey, "verification_method": "evidence", "verification_check": "release.secrets", "verifier_team": "sicurezza"}, http.StatusCreated)
	manualKey, manualID := obj(mr["requirement"])["key"].(string), obj(mr["element"])["id"].(string)
	evidenceRows := map[string]string{} // titolo → chiave
	for _, r := range list(man.do("GET", "/api/v1/elements/"+impKey+"/requirements", nil, http.StatusOK)["rows"]) {
		x := obj(r)
		if x["method"] == "evidence" {
			evidenceRows[x["title"].(string)] = x["key"].(string)
			move(lead, x["id"].(string), "Accettato", http.StatusOK)
		}
	}
	secKey, scaKey := evidenceRows["Segreti fuori dal codice sorgente"], evidenceRows["Nessuna vulnerabilità critica o alta correggibile nelle dipendenze"]
	if len(evidenceRows) != 3 || secKey == "" || scaKey == "" || evidenceRows["Nessun segreto nelle immagini"] != manualKey {
		t.Fatalf("requisiti da evidenza sull'impatto: %v", evidenceRows)
	}
	_ = manualID

	// --- Token: con devsecops.write (amministratore con permessi ridotti, membro "Pipeline"),
	// rifiuti senza permesso, senza token, con un formato sbagliato.
	pipeTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI ordini", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	ciTok := ci.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI"}, http.StatusCreated)["token"].(string)
	readTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	push := func(tok, path string, body map[string]any, want int) map[string]any {
		t.Helper()
		if _, ok := body["format"]; !ok {
			body["format"] = "regia.devsecops/v1"
		}
		return bearerDo(t, srv.URL, tok, "POST", "/api/v1/devsecops/"+path, body, want)
	}
	result := func(m map[string]any) map[string]any { return obj(m["result"]) }
	ordersAPI := map[string]any{"name": "orders-api", "repository": "shop/orders-api", "provider": "github", "url": "https://git.example.test/shop/orders-api", "application": "ORD"}
	push(readTok, "check-runs", map[string]any{"service": ordersAPI, "commit": "a1", "verdict": "passed"}, http.StatusForbidden)
	reader.do("POST", "/api/v1/devsecops/releases", map[string]any{"format": "regia.devsecops/v1", "service": ordersAPI, "version": "1"}, http.StatusForbidden)
	anon := &user{t: t, base: srv.URL, c: http.DefaultClient}
	anon.do("POST", "/api/v1/devsecops/releases", map[string]any{"format": "regia.devsecops/v1"}, http.StatusUnauthorized)
	push(pipeTok, "releases", map[string]any{"format": "regia.devsecops/v0", "service": ordersAPI, "version": "1"}, http.StatusUnprocessableEntity)
	push(pipeTok, "releases", map[string]any{"service": ordersAPI}, http.StatusUnprocessableEntity)

	// --- Esiti dei controlli del commit (idempotenti) e SBOM CycloneDX.
	checks := []map[string]any{
		{"name": "test", "mode": "block", "outcome": "passed"},
		{"name": "secrets", "mode": "block", "outcome": "passed"},
		{"name": "cves", "mode": "block", "outcome": "passed"},
		{"name": "image", "mode": "block", "outcome": "failed", "skip": map[string]any{"by": "responsabile.rilascio", "reason": "CVE dell'immagine base senza correzione"}},
		{"name": "lint", "mode": "warn", "outcome": "failed"},
	}
	cr := map[string]any{"service": ordersAPI, "commit": "a1b2c3d4e5f6a7b8", "ref": "main", "run_id": "991", "verdict": "passed", "checks": checks, "at": "2026-11-20T08:00:00Z"}
	if rc := result(push(ciTok, "check-runs", cr, http.StatusAccepted)); rc["created"] != true || rc["application_id"] != apps["ORD"]["id"] {
		t.Fatalf("esito dei controlli: %v", rc)
	}
	if rc := result(push(ciTok, "check-runs", cr, http.StatusAccepted)); rc["created"] != false {
		t.Fatalf("esito ripetuto: %v", rc)
	}
	cdx := map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.5", "components": []any{
		map[string]any{"type": "library", "name": "express", "version": "4.19.2", "purl": "pkg:npm/express@4.19.2", "licenses": []any{map[string]any{"license": map[string]any{"id": "MIT"}}}},
		map[string]any{"type": "library", "name": "lodash", "version": "4.17.20", "purl": "pkg:npm/lodash@4.17.20"},
		map[string]any{"type": "application", "name": "node", "version": "18.19.0", "purl": "pkg:generic/node@18.19.0"},
		map[string]any{"type": "library", "name": "openssl", "version": "3.0.2", "purl": "pkg:deb/debian/openssl@3.0.2", "components": []any{
			map[string]any{"type": "library", "name": "libssl3", "version": "3.0.2", "purl": "pkg:deb/debian/libssl3@3.0.2"}}},
	}}
	sb := map[string]any{"service": ordersAPI, "version": "1.4.0", "digest": "sha256:aaa1", "document": cdx}
	if rc := result(push(pipeTok, "sboms", sb, http.StatusAccepted)); num(rc["components"]) != 5 || rc["created"] != true {
		t.Fatalf("SBOM CycloneDX: %v", rc)
	}
	if rc := result(push(pipeTok, "sboms", sb, http.StatusOK)); rc["changed"] != false {
		t.Fatalf("SBOM ripetuta: %v", rc)
	}

	// --- Report di grype così com'è, alla data del rilascio: GHSA con CVE correlata e una
	// vulnerabilità ripetuta valgono una volta.
	grype := map[string]any{"matches": []any{
		map[string]any{"vulnerability": map[string]any{"id": "CVE-2026-31001", "severity": "Critical", "fix": map[string]any{"versions": []any{"4.17.21"}, "state": "fixed"}},
			"artifact": map[string]any{"name": "lodash", "version": "4.17.20", "type": "npm", "purl": "pkg:npm/lodash@4.17.20"}},
		map[string]any{"vulnerability": map[string]any{"id": "GHSA-7h2x-q4p9-3fmc", "severity": "High", "fix": map[string]any{"state": "not-fixed"}},
			"relatedVulnerabilities": []any{map[string]any{"id": "CVE-2026-31002"}},
			"artifact":               map[string]any{"name": "express", "version": "4.19.2", "type": "npm", "purl": "pkg:npm/express@4.19.2"}},
		map[string]any{"vulnerability": map[string]any{"id": "CVE-2026-31002", "severity": "High"},
			"artifact": map[string]any{"name": "express", "version": "4.19.2", "type": "npm", "purl": "pkg:npm/express@4.19.2"}},
	}}
	vr := map[string]any{"service": ordersAPI, "version": "1.4.0", "digest": "sha256:aaa1", "scanned_at": "2026-11-20T08:10:00Z", "report": grype}
	if rc := result(push(pipeTok, "vuln-reports", vr, http.StatusAccepted)); num(obj(rc["counts"])["total"]) != 2 || num(obj(rc["counts"])["critical"]) != 1 {
		t.Fatalf("report grype: %v", rc)
	}
	push(pipeTok, "vuln-reports", vr, http.StatusOK)

	// --- Rilascio in produzione con il change = chiave della richiesta: requisiti verificati
	// dall'evidenza (secrets in block), non quello delle dipendenze (una alta correggibile).
	rel := map[string]any{"service": ordersAPI, "version": "1.4.0", "commit": "a1b2c3d4e5f6a7b8", "image": "registry.example.test/shop/orders-api:1.4.0",
		"digest": "sha256:aaa1", "environment": "prod", "change_number": strings.ToLower(brKey), "change_title": "Pagamento in tre rate",
		"requested_by": "Giulia Neri", "approved_by": "Paolo Conti", "approved_at": "2026-11-20T08:30:00Z",
		"release_url": "https://git.example.test/shop/orders-api/releases/1.4.0", "at": "2026-11-20T09:00:00Z",
		"cves_at_release": map[string]any{"critical": 0, "high": 1, "fixable_high": 1}}
	rr := result(push(pipeTok, "releases", rel, http.StatusAccepted))
	if rr["request"] != brKey || rr["change"] != brKey || rr["production"] != true {
		t.Fatalf("rilascio: %v", rr)
	}
	verified := map[string]bool{}
	for _, v := range list(rr["verified_requirements"]) {
		verified[obj(v)["requirement"].(string)] = true
	}
	if len(verified) != 2 || !verified[secKey] || !verified[manualKey] || verified[scaKey] {
		t.Fatalf("requisiti verificati dal rilascio: %v (attesi %s, %s)", rr["verified_requirements"], secKey, manualKey)
	}
	// Ripetuto identico: nessun effetto.
	if again := push(pipeTok, "releases", rel, http.StatusOK); result(again)["changed"] != false {
		t.Fatalf("rilascio ripetuto: %v", again)
	}
	rv := lead.do("GET", "/api/v1/requirements/"+manualKey, nil, http.StatusOK)
	vs := list(rv["verifications"])
	if len(vs) != 1 || obj(vs[0])["method"] != "automatic" || obj(vs[0])["collector"] != "devsecops" || obj(rv["element"])["state"] != "Verificato" ||
		!strings.Contains(obj(vs[0])["note"].(string), "secrets") {
		t.Fatalf("verifica automatica: %v / %v", rv["verifications"], obj(rv["element"])["state"])
	}
	if c := obj(list(rv["cells"])[0]); c["status"] != "verified" {
		t.Fatalf("cella verificata: %v", c)
	}
	var auto int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'requirement.verify' and payload->>'method' = 'automatic'`,
		ten.TenantID).Scan(&auto); err != nil || auto != 2 {
		t.Fatalf("audit delle verifiche automatiche: %d %v", auto, err)
	}

	// --- Report di oggi sulla versione in produzione: una critica in più nell'immagine.
	trivy := map[string]any{"Results": []any{
		map[string]any{"Class": "os-pkgs", "Vulnerabilities": []any{
			map[string]any{"VulnerabilityID": "CVE-2026-31077", "PkgName": "libssl3", "InstalledVersion": "3.0.2", "FixedVersion": "3.0.2-0+deb12u9", "Severity": "CRITICAL",
				"PkgIdentifier": map[string]any{"PURL": "pkg:deb/debian/libssl3@3.0.2"}}}},
		map[string]any{"Class": "lang-pkgs", "Vulnerabilities": []any{
			map[string]any{"VulnerabilityID": "CVE-2026-31001", "PkgName": "lodash", "InstalledVersion": "4.17.20", "FixedVersion": "4.17.21", "Severity": "CRITICAL",
				"PkgIdentifier": map[string]any{"PURL": "pkg:npm/lodash@4.17.20"}}}},
	}}
	push(pipeTok, "vuln-reports", map[string]any{"service": map[string]any{"name": "orders-api"}, "digest": "sha256:aaa1", "scanner": "trivy",
		"scanned_at": "2026-11-28T03:00:00Z", "report": trivy}, http.StatusAccepted)
	var findings int
	if err := d.Owner.QueryRow(ctx, "select count(*) from findings where tenant_id = $1", ten.TenantID).Scan(&findings); err != nil || findings != 3 {
		t.Fatalf("finding deduplicati per servizio e componente: %d %v", findings, err)
	}

	// --- Altri servizi: SPDX gzip per la vetrina, staging più avanti, un servizio fermo da
	// mesi, uno censito ma non ancora sulla pipeline.
	web := map[string]any{"name": "shop-web", "repository": "shop/shop-web", "application": apps["WEB"]["key"]}
	spdx := map[string]any{"spdxVersion": "SPDX-2.3", "packages": []any{
		map[string]any{"name": "react", "versionInfo": "18.3.1", "licenseConcluded": "MIT",
			"externalRefs": []any{map[string]any{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": "pkg:npm/react@18.3.1"}}},
		map[string]any{"name": "nginx", "versionInfo": "1.25.4", "licenseDeclared": "BSD-2-Clause"},
	}}
	if st, body := gzipPush(t, srv.URL, pipeTok, "/api/v1/devsecops/sboms", map[string]any{"format": "regia.devsecops/v1", "service": web, "version": "2.0.0", "document": spdx}); st != http.StatusAccepted ||
		!strings.Contains(body, `"components":2`) {
		t.Fatalf("SBOM SPDX compressa: %d %s", st, body)
	}
	push(pipeTok, "releases", map[string]any{"service": web, "version": "2.0.0", "environment": "production", "at": "2026-11-25T10:00:00Z",
		"change_number": "TMP-4471", "checks": []any{map[string]any{"name": "test", "mode": "block", "outcome": "passed"}}}, http.StatusAccepted)
	// Il change definitivo sostituisce il provvisorio, che resta come alias.
	r2 := result(push(pipeTok, "releases", map[string]any{"service": web, "version": "2.0.1", "environment": "production", "at": "2026-11-26T10:00:00Z",
		"change_number": "CHG0045120", "change_aliases": []any{"TMP-4471"}}, http.StatusAccepted))
	if r2["change"] != "CHG0045120" {
		t.Fatalf("change definitivo: %v", r2)
	}
	push(pipeTok, "deployments", map[string]any{"service": map[string]any{"name": "orders-api"}, "environment": "staging", "version": "1.5.0-rc1",
		"status": "running", "at": "2026-11-30T10:00:00Z", "external_ref": "argocd/orders-api-staging/17"}, http.StatusAccepted)
	push(pipeTok, "deployments", map[string]any{"service": map[string]any{"name": "orders-api"}, "environment": "staging", "version": "1.5.0-rc1",
		"status": "succeeded", "at": "2026-11-30T10:04:00Z", "external_ref": "argocd/orders-api-staging/17"}, http.StatusAccepted)
	push(pipeTok, "releases", map[string]any{"service": map[string]any{"name": "orders-batch", "application": "ORD"}, "version": "0.9.3",
		"environment": "prod", "at": "2026-08-01T10:00:00Z"}, http.StatusAccepted)
	admin.do("POST", "/api/v1/devsecops/services", map[string]any{"format": "regia.devsecops/v1", "name": "orders-reporting", "application": "ORD"}, http.StatusCreated)
	lead.do("POST", "/api/v1/devsecops/services", map[string]any{"format": "regia.devsecops/v1", "name": "x", "application": "ORD"}, http.StatusForbidden)
	// Un SBOM oltre 1 MiB (seimila componenti) passa dal limite dell'API di push.
	big := []any{}
	for i := 0; i < 6000; i++ {
		big = append(big, map[string]any{"type": "library", "name": fmt.Sprintf("pacchetto-di-prova-con-un-nome-lungo-%05d", i), "version": "1.0.0",
			"purl": fmt.Sprintf("pkg:npm/%%40azienda/pacchetto-di-prova-con-un-nome-lungo-%05d@1.0.0?descrizione=componente-generato-per-la-prova-del-limite", i)})
	}
	if rc := result(push(pipeTok, "sboms", map[string]any{"service": map[string]any{"name": "orders-batch"}, "version": "0.9.3",
		"document": map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.6", "components": big}}, http.StatusAccepted)); num(rc["components"]) != 6000 {
		t.Fatalf("SBOM grande: %v", rc)
	}

	// --- Pannello dell'asset: tre numeri con il confronto a sette giorni, servizi, ambienti, change.
	p := reader.do("GET", "/api/v1/devsecops/elements/"+assetKey+"/panel", nil, http.StatusOK)
	k := obj(p["kpis"])
	if c := obj(k["critical_cves"]); num(c["now"]) != 2 || num(c["week_ago"]) != 1 {
		t.Fatalf("CVE critiche in produzione: %v", c)
	}
	if c := obj(k["blocking_checks"]); num(c["now"]) != 1 || num(c["week_ago"]) != 1 {
		t.Fatalf("controlli bloccanti in eccezione: %v", c)
	}
	if c := obj(k["stale"]); num(c["now"]) != 1 || num(c["week_ago"]) != 1 || num(k["services"]) != 4 || num(k["on_pipeline"]) != 3 {
		t.Fatalf("servizi non rilasciati: %v", k)
	}
	svcs := map[string]map[string]any{}
	for _, s := range list(p["services"]) {
		svcs[obj(s)["name"].(string)] = obj(s)
	}
	oa := svcs["orders-api"]
	today, atRel := obj(oa["cves_today"]), obj(oa["cves_at_release"])
	if oa["prod_version"] != "1.4.0" || num(today["critical"]) != 2 || num(obj(today["image"])["critical"]) != 1 || num(obj(today["app"])["critical"]) != 1 ||
		atRel["declared"] != true || num(atRel["high"]) != 1 || len(list(today["scanners"])) != 2 {
		t.Fatalf("CVE di oggi e al rilascio di orders-api: %v / %v", today, atRel)
	}
	relv := obj(oa["release"])
	if relv["approved_by"] != "Paolo Conti" || obj(relv["change"])["request_key"] != brKey || relv["checks_status"] != "exception" {
		t.Fatalf("rilascio in produzione di orders-api: %v", relv)
	}
	if rts := list(oa["runtimes"]); len(rts) != 1 || obj(rts[0])["name"] != "node" || obj(rts[0])["unsupported"] != true {
		t.Fatalf("runtime di orders-api: %v", oa["runtimes"])
	}
	if envs := obj(oa["environments"]); obj(envs["staging"])["version"] != "1.5.0-rc1" || obj(envs["prod"])["version"] != "1.4.0" {
		t.Fatalf("ambienti di orders-api: %v", envs)
	}
	if svcs["orders-reporting"]["on_pipeline"] != false || svcs["orders-batch"]["stale"] != true || svcs["shop-web"]["prod_version"] != "2.0.1" {
		t.Fatalf("servizi del pannello: %v", svcs)
	}
	if envs := list(p["environments"]); len(envs) != 3 || obj(envs[0])["name"] != "staging" {
		t.Fatalf("ambienti del pannello: %v", envs)
	}
	groups := list(p["changes"])
	var chg map[string]any
	for _, g := range groups {
		if c := obj(obj(g)["change"]); c["number"] == "CHG0045120" {
			chg = obj(g)
		}
	}
	if chg == nil || len(list(chg["releases"])) != 2 || list(obj(chg["change"])["aliases"])[0] != "TMP-4471" {
		t.Fatalf("rilasci per change: %v", groups)
	}
	// Storico di un servizio.
	h := reader.do("GET", "/api/v1/devsecops/services/"+oa["id"].(string), nil, http.StatusOK)
	if len(list(h["history"])) != 1 || h["declared_application"] != "ORD" || h["mapping_source"] != "pipeline" {
		t.Fatalf("storico di orders-api: %v", h)
	}

	// --- Fatti: sull'applicazione e sulla richiesta.
	ordKey := apps["ORD"]["key"].(string)
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": ordKey,
		"condition": "cve_critiche_produzione: 2 servizi: 3 servizi_fuori_pipeline: 1 runtime_fuori_supporto: sì ultimo_rilascio_giorni: 11"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti dell'applicazione: %v", chk["facts"])
	}
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": brKey,
		"condition": "rilasci: 1 rilasciata: sì servizi_rilasciati: orders-api ultimo_rilascio: 2026-11-20"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti della richiesta: %v", chk["facts"])
	}

	// --- Panoramica: la critica DORA in testa, gli ultimi rilasci.
	o := reader.do("GET", "/api/v1/devsecops/overview", nil, http.StatusOK)
	crit := list(o["critical"])
	if len(crit) != 2 || obj(crit[0])["supports_critical_function"] != true || len(list(o["releases"])) != 4 || num(obj(list(o["assets"])[0])["critical"]) != 2 {
		t.Fatalf("panoramica: %v", o)
	}

	// --- Configurazione: associazione manuale (prevale sulla pipeline), ambiente, impostazioni, change.
	lead.do("PUT", "/api/v1/devsecops/config/settings", map[string]any{"production_envs": []any{"prod"}, "stale_days": 10}, http.StatusForbidden)
	cfg := admin.do("PUT", "/api/v1/devsecops/config/settings", map[string]any{"production_envs": []any{"prod", "production", "prod-eu"}, "stale_days": 150,
		"runtimes": []any{map[string]any{"name": "node", "version": "18", "end_of_support": "2025-04-30"}}}, http.StatusOK)
	if num(cfg["stale_days"]) != 150 || len(list(cfg["runtimes"])) != 1 {
		t.Fatalf("impostazioni: %v", cfg)
	}
	if k := obj(reader.do("GET", "/api/v1/devsecops/elements/"+assetKey+"/panel", nil, http.StatusOK)["kpis"]); num(obj(k["stale"])["now"]) != 0 {
		t.Fatalf("non rilasciati con 150 giorni: %v", k)
	}
	admin.do("PATCH", "/api/v1/devsecops/services/"+svcs["orders-batch"]["id"].(string), map[string]any{"application": "WEB"}, http.StatusOK)
	push(pipeTok, "releases", map[string]any{"service": map[string]any{"name": "orders-batch", "application": "ORD"}, "version": "0.9.4",
		"environment": "prod", "at": "2026-11-29T10:00:00Z"}, http.StatusAccepted)
	if s := admin.do("GET", "/api/v1/devsecops/services/"+svcs["orders-batch"]["id"].(string), nil, http.StatusOK); s["application_key"] != apps["WEB"]["key"] || s["mapping_source"] != "manual" {
		t.Fatalf("l'associazione manuale deve prevalere: %v", s)
	}
	conf := admin.do("GET", "/api/v1/devsecops/config", nil, http.StatusOK)
	var staging string
	for _, e := range list(conf["environments"]) {
		if obj(e)["name"] == "staging" {
			staging = obj(e)["id"].(string)
		}
	}
	admin.do("PATCH", "/api/v1/devsecops/environments/"+staging, map[string]any{"ordinal": 30}, http.StatusNoContent)
	var chgID string
	for _, c := range list(conf["changes"]) {
		if obj(c)["number"] == "CHG0045120" {
			chgID = obj(c)["id"].(string)
		}
	}
	if c := admin.do("PATCH", "/api/v1/devsecops/changes/"+chgID, map[string]any{"request": brKey}, http.StatusOK); c["request_key"] != brKey {
		t.Fatalf("change collegato a mano: %v", c)
	}
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": brKey, "condition": "rilasci: 3 change: CHG0045120"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("rilasci della richiesta dopo il collegamento: %v", chk["facts"])
	}

	// --- Isolamento: l'altro tenant non vede nulla e non legge i servizi per id.
	if ob := adminB.do("GET", "/api/v1/devsecops/overview", nil, http.StatusOK); num(obj(ob["kpis"])["services"]) != 0 || len(list(ob["releases"])) != 0 {
		t.Fatalf("panoramica dell'altro tenant: %v", ob)
	}
	adminB.do("GET", "/api/v1/devsecops/services/"+oa["id"].(string), nil, http.StatusNotFound)
	if cb := adminB.do("GET", "/api/v1/devsecops/config", nil, http.StatusOK); len(list(cb["services"])) != 0 || len(list(cb["environments"])) != 0 {
		t.Fatalf("configurazione dell'altro tenant: %v", cb)
	}
	var audits int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action like 'devsecops.%' and payload ? 'api_token'`, ten.TenantID).Scan(&audits); err != nil || audits < 10 {
		t.Fatalf("audit degli invii con token: %d %v", audits, err)
	}
}

// gzipPush invia un corpo JSON compresso con un token API.
func gzipPush(t *testing.T, base, token, path string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	req, _ := http.NewRequest("POST", base+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}
