package itest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/platform/tenant"
)

// DevSecOps fase 3 (replica/devsecops.md §3.3 e §3.4; migrazione 0034): politica centrale
// versionata, anteprima dell'impatto, aderenza per servizio e applicazione, fatti per le
// condizioni, adozione guidata con anteprima delle differenze, pull request aperta tramite il
// finto GitHub (permessi di scrittura facoltativi, download dei file senza), stato della pull
// request sincronizzato (a richiesta e con il webhook).
func TestPipelinePolicy(t *testing.T) {
	e := newGHEnv(t)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	num := func(v any) int { f, _ := v.(float64); return int(f) }

	integrationID, profileID := e.setup()
	e.ing.take()

	// Le tre applicazioni del setup (Ordini, Pagamenti, Catalogo web) più una senza repository.
	apps := map[string]map[string]any{}
	e.admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Strumenti interni", "code": "STR", "criticality": "low"}, http.StatusCreated)
	for _, a := range list(e.admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["name"].(string)] = obj(a)
	}
	crit, yes := "critical", true
	e.admin.do("PATCH", "/api/v1/architecture/applications/"+apps["Ordini"]["id"].(string), map[string]any{"criticality": crit, "supports_critical_function": yes}, http.StatusOK)
	e.admin.do("PATCH", "/api/v1/architecture/applications/"+apps["Catalogo web"]["id"].(string), map[string]any{"criticality": "medium"}, http.StatusOK)

	// --- Politica: all'inizio non c'è, l'editor parte da quella proposta.
	st := e.admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK)
	if st["configured"] != false || len(list(obj(st["definition"])["controls"])) != 6 {
		t.Fatalf("politica iniziale: %v", st)
	}
	def := obj(st["definition"])
	// Anteprima senza servizi: nessuno cambia stato, ma si vede la distribuzione.
	im := e.admin.do("POST", "/api/v1/devsecops/policy/impact", map[string]any{"definition": def}, http.StatusOK)
	if im["before_configured"] != false || num(obj(obj(im["after"])["apps"])["total"]) != 4 {
		t.Fatalf("impatto iniziale: %v", im)
	}
	// Una definizione sbagliata dà i problemi per punto.
	bad := map[string]any{"controls": []any{map[string]any{"key": "Test Uno", "name": "x"}}, "classes": []any{}, "fallback": map[string]any{"key": "altre", "name": "Altre"}}
	if r := e.admin.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": bad}, http.StatusUnprocessableEntity); len(list(r["problems"])) == 0 {
		t.Fatalf("problemi di validazione assenti: %v", r)
	}
	if v := e.admin.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": def, "note": "politica iniziale"}, http.StatusOK); num(v["version"]) != 1 || v["configured"] != true {
		t.Fatalf("salvataggio: %v", v)
	}
	// Lo stesso contenuto non crea una versione nuova.
	if v := e.admin.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": def}, http.StatusOK); num(v["version"]) != 1 {
		t.Fatalf("salvataggio identico: %v", v)
	}

	// --- Esiti dei controlli dalle pipeline (API di push).
	tok := e.admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	push := func(app, name string, checks []map[string]any) {
		bearerDo(t, e.base, tok, "POST", "/api/v1/devsecops/check-runs", map[string]any{"format": "regia.devsecops/v1",
			"service": map[string]any{"name": name, "application": app}, "commit": "c0ffee01", "ref": "main", "run_id": "1", "checks": checks,
			"at": time.Now().UTC().Format(time.RFC3339)}, http.StatusAccepted)
	}
	check := func(n, mode string) map[string]any {
		return map[string]any{"name": n, "mode": mode, "outcome": "passed"}
	}
	// Ordini (funzione critica): test, lint, segreti ok; dipendenze troppo deboli; SAST e immagine assenti.
	push("ORD", "ordini-api", []map[string]any{check("test", "block"), check("lint", "warn"), check("secrets", "block"), check("sca", "warn")})
	// Catalogo web (classe di ripiego): i controlli richiesti girano tutti (uno col nome di un alias).
	push("", "catalogo-web", []map[string]any{check("tests", "warn"), check("lint", "warn"), check("gitleaks", "block"), check("dependencies", "warn")})
	e.admin.do("PATCH", "/api/v1/devsecops/services/"+serviceID(t, e, "catalogo-web"), map[string]any{"application": apps["Catalogo web"]["key"]}, http.StatusOK)

	// Conta solo il branch predefinito del repository. Ordini ha il repository del connettore
	// (branch predefinito main): una pipeline di un branch di lavoro, anche più recente e senza
	// controlli, non cambia lo stato. Catalogo web non ha un repository noto: finché nessun
	// check_run è legato a un rilascio non è in pipeline; col rilascio dello stesso commit lo è.
	bearerDo(t, e.base, tok, "POST", "/api/v1/devsecops/check-runs", map[string]any{"format": "regia.devsecops/v1",
		"service": map[string]any{"name": "ordini-api", "application": "ORD"}, "commit": "bad0c0de", "ref": "refs/heads/feature/pr-1", "run_id": "2",
		"checks": []map[string]any{}, "at": time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}, http.StatusAccepted)
	status := func(name string) any {
		for _, a := range list(e.admin.do("GET", "/api/v1/devsecops/adherence", nil, http.StatusOK)["applications"]) {
			if obj(a)["name"] == name {
				return obj(a)["status"]
			}
		}
		return nil
	}
	if got := status("Ordini"); got != "fuori_politica" {
		t.Fatalf("una pipeline di un altro branch ha cambiato lo stato di Ordini: %v", got)
	}
	if got := status("Catalogo web"); got != "non_in_pipeline" {
		t.Fatalf("Catalogo web senza branch noto né rilascio: %v", got)
	}
	bearerDo(t, e.base, tok, "POST", "/api/v1/devsecops/releases", map[string]any{"format": "regia.devsecops/v1",
		"service": map[string]any{"name": "catalogo-web", "application": "CAT"}, "version": "1.0.0", "commit": "c0ffee01", "environment": "prod",
		"at": time.Now().UTC().Format(time.RFC3339)}, http.StatusAccepted)

	// Un servizio censito ma senza alcun esito: l'applicazione resta fuori dalla pipeline.
	e.admin.do("POST", "/api/v1/devsecops/services", map[string]any{"format": "regia.devsecops/v1", "name": "pagamenti-batch", "application": "PAG"}, http.StatusCreated)

	ad := e.admin.do("GET", "/api/v1/devsecops/adherence", nil, http.StatusOK)
	byApp := map[string]map[string]any{}
	for _, a := range list(ad["applications"]) {
		byApp[obj(a)["name"].(string)] = obj(a)
	}
	want := map[string]string{"Ordini": "fuori_politica", "Catalogo web": "aderente", "Pagamenti": "non_in_pipeline", "Strumenti interni": "non_in_pipeline"}
	for name, s := range want {
		if byApp[name]["status"] != s {
			t.Fatalf("aderenza di %s: %v, atteso %s", name, byApp[name]["status"], s)
		}
	}
	if p := obj(byApp["Pagamenti"]); len(list(p["services"])) != 1 || obj(list(p["services"])[0])["status"] != "non_in_pipeline" {
		t.Fatalf("Pagamenti: %v", p)
	}
	if o := obj(byApp["Ordini"]); o["class_key"] != "funzione_critica" || len(list(o["missing"])) != 3 {
		t.Fatalf("Ordini: classe %v, mancanti %v", o["class_key"], o["missing"])
	}
	if c := obj(ad["apps"]); num(c["total"]) != 4 || num(c["aderente"]) != 1 || num(c["fuori_politica"]) != 1 || num(c["non_in_pipeline"]) != 2 {
		t.Fatalf("conteggi delle applicazioni: %v", c)
	}
	// Per controllo: «sast» manca in Ordini, che l'unico servizio col dato è: 1 mancante.
	for _, g := range list(ad["gaps"]) {
		if obj(g)["key"] == "sast" && num(obj(g)["missing"]) != 1 {
			t.Fatalf("lacuna sast: %v", g)
		}
	}

	// --- Fatti per le condizioni dei processi.
	ordKey := apps["Ordini"]["key"].(string)
	if chk := e.admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": ordKey,
		"condition": "aderenza_pipeline: fuori_politica controlli_mancanti: 3 controlli_mancanti_elenco: sast"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti dell'applicazione: %v", chk["facts"])
	}
	if chk := e.admin.do("POST", "/api/v1/conditions/check", map[string]any{"element": apps["Catalogo web"]["key"],
		"condition": "aderenza_pipeline: aderente controlli_mancanti: 0"}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti del catalogo web: %v", chk["facts"])
	}

	// --- Anteprima di una politica più severa: il lint diventa bloccante per tutti.
	strict := obj(e.admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK)["definition"])
	fb := obj(strict["fallback"])
	obj(fb["modes"])["lint"] = "block"
	im = e.admin.do("POST", "/api/v1/devsecops/policy/impact", map[string]any{"definition": strict}, http.StatusOK)
	if num(im["changed_apps"]) != 1 || num(im["changed_services"]) != 1 || len(list(im["changes"])) != 1 {
		t.Fatalf("impatto della politica più severa: %v", im)
	}
	if tr := obj(list(im["transitions"])[0]); tr["from"] != "aderente" || tr["to"] != "fuori_politica" {
		t.Fatalf("transizione: %v", tr)
	}
	e.admin.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": strict, "note": "lint bloccante"}, http.StatusOK)
	df := e.admin.do("GET", "/api/v1/devsecops/policy/diff?from=1&to=2", nil, http.StatusOK)
	if ch := list(df["changes"]); len(ch) != 1 || !strings.Contains(obj(ch[0])["text"].(string), "da avviso a bloccante") {
		t.Fatalf("differenze tra le versioni: %v", df)
	}
	if v := e.admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK); len(list(v["versions"])) != 2 {
		t.Fatalf("storico: %v", v["versions"])
	}
	// Chi non gestisce il catalogo non salva la politica.
	reader := e.reader()
	reader.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": strict}, http.StatusForbidden)
	reader.do("GET", "/api/v1/devsecops/adherence", nil, http.StatusOK)

	// --- Scheda della pipeline di un'applicazione: «cosa manca».
	str := apps["Strumenti interni"]
	pl := e.admin.do("GET", "/api/v1/devsecops/elements/"+str["key"].(string)+"/pipeline", nil, http.StatusOK)
	items := map[string]map[string]any{}
	for _, it := range list(obj(list(obj(pl["pipeline"])["applications"])[0])["items"]) {
		items[obj(it)["key"].(string)] = obj(it)
	}
	if items["repository"]["state"] != "todo" || items["github"]["state"] != "ok" || items["politica"]["state"] != "ok" {
		t.Fatalf("elenco guidato: %v", items)
	}

	// --- Adozione guidata. Il profilo ha la sezione di adozione, scritta dal tenant.
	prof := obj(e.admin.do("GET", "/api/v1/devsecops/pipeline-profiles/"+profileID, nil, http.StatusOK)["profile"])
	pdef := obj(prof["definition"])
	pdef["adoption"] = map[string]any{
		"params": []any{map[string]any{"key": "stack", "label": "Stack", "default": "node", "options": []string{"node", "java"}}},
		"templates": []any{
			map[string]any{"key": "ci", "name": "Build e test", "uses": "esempio/pipeline-standard/.github/workflows/ci.yml@v1", "default": true,
				"with": map[string]string{"stack": "{stack}", "application": "{application}"}, "inherit_secrets": true, "controls": []string{"test", "lint", "secrets", "dependencies"}},
			map[string]any{"key": "sicurezza", "name": "Scansioni di sicurezza", "uses": "esempio/pipeline-standard/.github/workflows/security.yml@v1", "default": true,
				"needs": []string{"ci"}, "controls": []string{"sast", "image"}},
		},
	}
	e.admin.do("PUT", "/api/v1/devsecops/pipeline-profiles/"+profileID, map[string]any{"name": prof["name"], "definition": pdef}, http.StatusOK)
	// Un riferimento che non è un workflow riusabile si rifiuta.
	pdef["adoption"].(map[string]any)["templates"].([]any)[0].(map[string]any)["uses"] = "pip install evil"
	e.admin.do("PUT", "/api/v1/devsecops/pipeline-profiles/"+profileID, map[string]any{"name": prof["name"], "definition": pdef}, http.StatusUnprocessableEntity)

	// Associo il repository degli strumenti interni all'applicazione.
	repos := e.repos(integrationID)
	strRepo := repos["esempio/strumenti-interni"]["id"].(string)
	e.admin.do("PUT", ghAPI+"/integrations/"+integrationID+"/repositories/"+strRepo,
		map[string]any{"status": "confirmed", "application_id": str["id"]}, http.StatusOK)
	e.ing.take()

	// Senza i permessi di scrittura della App: si propone il download, la pull request fallisce chiaramente.
	delete(e.fg.Permissions, "contents")
	delete(e.fg.Permissions, "pull_requests")
	e.admin.do("POST", ghAPI+"/integrations/"+integrationID+"/test", nil, http.StatusOK)
	opt := e.admin.do("GET", "/api/v1/devsecops/applications/"+str["id"].(string)+"/adoption", nil, http.StatusOK)
	srcs := list(opt["sources"])
	if len(srcs) != 1 || obj(srcs[0])["can_open_pr"] != false || len(list(obj(srcs[0])["templates"])) != 2 || len(list(opt["repositories"])) != 1 {
		t.Fatalf("opzioni di adozione senza scrittura: %v", opt)
	}
	in := map[string]any{"application_id": str["id"], "repository_id": strRepo, "params": map[string]string{"stack": "java"}}
	pv := e.admin.do("POST", "/api/v1/devsecops/adoption/preview", in, http.StatusOK)
	files := list(pv["files"])
	if len(files) != 2 || pv["can_open_pr"] != false || obj(files[0])["status"] != "new" || num(pv["policy_version"]) != 2 {
		t.Fatalf("anteprima: %v", pv)
	}
	wf := obj(files[0])["content"].(string)
	cfg := obj(files[1])["content"].(string)
	for _, w := range []string{"uses: esempio/pipeline-standard/.github/workflows/ci.yml@v1", `stack: "java"`, `application: "STR-`, "secrets: inherit", "needs: [ci]"} {
		if !strings.Contains(wf, w) && !strings.Contains(wf, strings.ReplaceAll(w, "STR-", "")) {
			t.Fatalf("workflow generato senza %q:\n%s", w, wf)
		}
	}
	if !strings.Contains(cfg, "secrets: block") || !strings.Contains(cfg, "sast: off") || !strings.Contains(cfg, "Classe dell'applicazione: Tutte le altre") {
		t.Fatalf("configurazione generata:\n%s", cfg)
	}
	if len(list(pv["uncovered"])) != 0 {
		t.Fatalf("controlli non coperti: %v", pv["uncovered"])
	}
	r := e.admin.doRaw("POST", "/api/v1/devsecops/adoption/pull-request", in)
	r.expect(t, http.StatusForbidden)
	if !strings.Contains(r.body, "adoption.no_write") || len(e.fg.Pulls("esempio/strumenti-interni")) != 0 {
		t.Fatalf("senza permessi: %s", r.body)
	}
	// Download: archivio zip con i due file.
	zr := e.admin.doRaw("POST", "/api/v1/devsecops/adoption/download", in)
	zr.expect(t, http.StatusOK)
	z, err := zip.NewReader(bytes.NewReader([]byte(zr.body)), int64(len(zr.body)))
	if err != nil || len(z.File) != 2 || z.File[0].Name != ".github/workflows/regia-pipeline.yml" {
		t.Fatalf("zip: %v %d", err, len(z.File))
	}
	rc, _ := z.File[1].Open()
	if b, _ := io.ReadAll(rc); string(b) != cfg {
		t.Fatalf("il file scaricato è diverso dall'anteprima")
	}
	// Chi non gestisce il catalogo non apre pull request.
	reader.do("POST", "/api/v1/devsecops/adoption/preview", in, http.StatusForbidden)

	// Con i permessi di scrittura (e la prova di connessione che li rilegge): pull request.
	e.fg.GrantWrite()
	e.admin.do("POST", ghAPI+"/integrations/"+integrationID+"/test", nil, http.StatusOK)
	opt = e.admin.do("GET", "/api/v1/devsecops/applications/"+str["id"].(string)+"/adoption", nil, http.StatusOK)
	if obj(list(opt["sources"])[0])["can_open_pr"] != true {
		t.Fatalf("opzioni con scrittura: %v", opt["sources"])
	}
	pr := e.admin.do("POST", "/api/v1/devsecops/adoption/pull-request", in, http.StatusCreated)
	if pr["state"] != "open" || num(pr["number"]) != 1 || pr["repository"] != "esempio/strumenti-interni" || len(list(pr["files"])) != 2 {
		t.Fatalf("pull request: %v", pr)
	}
	pulls := e.fg.Pulls("esempio/strumenti-interni")
	if len(pulls) != 1 || !strings.HasPrefix(pulls[0].Head, "regia/pipeline-") || pulls[0].Base != "main" {
		t.Fatalf("pull request sul finto GitHub: %+v", pulls)
	}
	if got := string(e.fg.File("esempio/strumenti-interni", pulls[0].Head, ".github/pipeline.yml")); got != cfg {
		t.Fatalf("file sul branch:\n%s", got)
	}
	if e.fg.File("esempio/strumenti-interni", "main", ".github/pipeline.yml") != nil {
		t.Fatal("Regia non deve scrivere sul branch principale")
	}
	// Una sola pull request aperta per repository.
	e.admin.do("POST", "/api/v1/devsecops/adoption/pull-request", in, http.StatusConflict)
	// L'elenco guidato la mostra.
	pl = e.admin.do("GET", "/api/v1/devsecops/elements/"+str["key"].(string)+"/pipeline", nil, http.StatusOK)
	prItem := ""
	for _, it := range list(obj(list(obj(pl["pipeline"])["applications"])[0])["items"]) {
		if obj(it)["key"] == "pr" {
			prItem = obj(it)["state"].(string)
		}
	}
	if prItem != "info" {
		t.Fatalf("la pull request aperta nell'elenco guidato: %q", prItem)
	}

	// Unita su GitHub: lo stato si sincronizza a richiesta.
	e.fg.MergePull("esempio/strumenti-interni", 1)
	sy := e.admin.do("POST", "/api/v1/devsecops/applications/"+str["id"].(string)+"/adoption/sync", nil, http.StatusOK)
	if num(sy["checked"]) != 1 || obj(list(sy["adoptions"])[0])["state"] != "merged" {
		t.Fatalf("stato dopo l'unione: %v", sy)
	}
	// Ora il repository è allineato: l'anteprima non vede differenze e non si apre un'altra pull request.
	pv = e.admin.do("POST", "/api/v1/devsecops/adoption/preview", in, http.StatusOK)
	for _, f := range list(pv["files"]) {
		if obj(f)["status"] != "unchanged" {
			t.Fatalf("dopo l'unione: %v", f)
		}
	}
	e.admin.do("POST", "/api/v1/devsecops/adoption/pull-request", in, http.StatusConflict)

	// Cambia la politica (classe di ripiego: SAST in avviso): il file di configurazione cambia
	// e la nuova pull request lo propone; il webhook «pull_request» ne aggiorna lo stato.
	newer := obj(e.admin.do("GET", "/api/v1/devsecops/policy", nil, http.StatusOK)["definition"])
	obj(obj(newer["fallback"])["modes"])["sast"] = "warn"
	e.admin.do("PUT", "/api/v1/devsecops/policy", map[string]any{"definition": newer, "note": "SAST in avviso"}, http.StatusOK)
	pr2 := e.admin.do("POST", "/api/v1/devsecops/adoption/pull-request", in, http.StatusCreated)
	if num(pr2["number"]) != 2 || len(list(pr2["files"])) != 1 || obj(list(pr2["files"])[0])["status"] != "changed" {
		t.Fatalf("seconda pull request: %v", pr2)
	}
	hook := map[string]any{"action": "closed", "number": 2, "repository": map[string]any{"full_name": "esempio/strumenti-interni"},
		"pull_request": map[string]any{"state": "closed", "merged": false, "closed_at": time.Now().UTC().Format(time.RFC3339)}}
	b, _ := json.Marshal(hook)
	req, _ := http.NewRequest("POST", e.base+ghAPI+"/webhook/"+integrationID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "pr-delivery-1")
	req.Header.Set("X-Hub-Signature-256", ghconn.Sign("segreto-del-webhook-di-prova", b))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook pull_request: %d", resp.StatusCode)
	}
	prs := list(e.admin.do("POST", "/api/v1/devsecops/applications/"+str["id"].(string)+"/adoption/sync", nil, http.StatusOK)["adoptions"])
	if len(prs) != 2 || obj(prs[0])["state"] != "closed" || obj(prs[1])["state"] != "merged" {
		t.Fatalf("stati dopo il webhook: %v", prs)
	}
}

// serviceID trova il servizio per nome nella configurazione.
func serviceID(t *testing.T, e *ghEnv, name string) string {
	t.Helper()
	cfg := e.admin.do("GET", "/api/v1/devsecops/config", nil, http.StatusOK)
	for _, s := range cfg["services"].([]any) {
		if m := s.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	t.Fatalf("servizio %s non trovato", name)
	return ""
}

// reader è una persona che può leggere ma non gestisce il catalogo.
func (e *ghEnv) reader() *user {
	e.t.Helper()
	if _, err := tenant.AddMember(context.Background(), needDB(e.t), tenant.MemberInput{TenantSlug: e.slug, Email: "lettore@" + e.slug + ".test",
		Name: "Lettore", Password: testPassword, Roles: []string{"viewer"}}); err != nil {
		e.t.Fatal(err)
	}
	return login(e.t, e.base, "lettore@"+e.slug+".test", testPassword)
}
