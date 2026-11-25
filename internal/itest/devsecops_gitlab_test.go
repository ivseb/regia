package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/devtools/fakegithub"
	"regia/internal/devtools/fakegitlab"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	glconn "regia/internal/modules/devsecops/connectors/gitlab"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// DevSecOps fase 5b: connettore GitLab, contro un finto GitLab (self-managed, in https con
// una CA interna) in httptest. Stesse tabelle, stesso Ingestor e stessi profili del
// connettore GitHub: nessuna migrazione.

const glToken = "glpat-test-lettura"

type glEnv struct {
	t     *testing.T
	base  string
	gl    *glconn.Service
	ing   *recIngestor
	fg    *fakegitlab.Server
	fgURL string
	caPEM string
	ten   tenant.Created
	admin *user
	hs    *httptest.Server
}

var glSeq int

func newGLEnv(t *testing.T) *glEnv {
	d := needDB(t)
	ctx := context.Background()
	glSeq++
	slug := fmt.Sprintf("gl%d-%s", glSeq, strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "GitLab " + slug, AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	fg := fakegitlab.New()
	fg.AddToken(glToken, "Regia (lettura)", "read_api")
	fakegitlab.Demo(fg, time.Now())
	fgs := httptest.NewTLSServer(fg.Handler())
	t.Cleanup(fgs.Close)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fgs.Certificate().Raw}))

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	box := secretbox.Random()
	svc := auth.NewService(d, time.Hour)
	svc.SetSecretBox(box)
	ing := &recIngestor{}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	gl := glconn.New(glconn.Options{DB: d, Box: box, Ingestor: ing, AllowPrivate: true, Log: log, Timeout: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error { return nil }})
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Log: log,
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, GitLab: gl,
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(gl.WaitIdle)
	e := &glEnv{t: t, base: hs.URL, gl: gl, ing: ing, fg: fg, fgURL: fgs.URL, caPEM: ca, ten: ten, hs: hs}
	e.admin = login(t, hs.URL, "admin@"+slug+".test", testPassword)
	return e
}

const glAPI = "/api/v1/devsecops/gitlab"

func (e *glEnv) input(profileID string) map[string]any {
	return map[string]any{
		"name": "GitLab self-managed", "base_url": e.fgURL, "groups": []string{"esempio"}, "token": glToken,
		"webhook_secret": "segreto-del-webhook-di-prova", "profile_id": profileID,
		"private_network": true, "ca_certificate": e.caPEM,
	}
}

func (e *glEnv) projects(id string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, it := range e.admin.do("GET", glAPI+"/integrations/"+id+"/projects", nil, http.StatusOK)["items"].([]any) {
		r := it.(map[string]any)
		out[r["full_name"].(string)] = r
	}
	return out
}

func (e *glEnv) sync(id string, full bool) map[string]any {
	e.t.Helper()
	q := ""
	if full {
		q = "?full=true"
	}
	e.admin.do("POST", glAPI+"/integrations/"+id+"/sync"+q, nil, http.StatusAccepted)
	e.gl.WaitIdle()
	runs := e.admin.do("GET", glAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"].([]any)
	return runs[0].(map[string]any)
}

// webhook invia un webhook come lo farebbe GitLab (X-Gitlab-Token) e restituisce stato e corpo.
func (e *glEnv) webhook(id, token, event, uuid string, payload any) (int, map[string]any) {
	e.t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", e.base+glAPI+"/webhook/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	req.Header.Set("X-Gitlab-Event", event)
	if uuid != "" {
		req.Header.Set("X-Gitlab-Event-UUID", uuid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	e.gl.WaitIdle()
	return resp.StatusCode, out
}

// setup crea applicazioni, profilo dal preset GitLab, integrazione, e associa i progetti.
func (e *glEnv) setup() (integrationID, profileID string) {
	t := e.t
	for _, a := range []map[string]any{{"name": "Ordini", "code": "ORD"}, {"name": "Pagamenti", "code": "PAG"}, {"name": "Catalogo web"}} {
		e.admin.do("POST", "/api/v1/architecture/applications", a, http.StatusCreated)
	}
	pre := e.admin.do("GET", glAPI+"/presets", nil, http.StatusOK)["presets"].([]any)
	if len(pre) != 1 || pre[0].(map[string]any)["name"] != "GitLab CI con SBOM e scansioni" {
		t.Fatalf("preset: %v", pre)
	}
	p := pre[0].(map[string]any)
	profileID = e.admin.do("POST", "/api/v1/devsecops/pipeline-profiles",
		map[string]any{"name": p["name"], "description": p["description"], "definition": p["definition"]}, http.StatusCreated)["id"].(string)
	integrationID = e.admin.do("POST", glAPI+"/integrations", e.input(profileID), http.StatusCreated)["id"].(string)
	res := e.admin.do("POST", glAPI+"/integrations/"+integrationID+"/discover", nil, http.StatusOK)
	if res["found"].(float64) != 4 || res["new"].(float64) != 4 {
		t.Fatalf("scoperta: %v", res)
	}
	prj := e.projects(integrationID)
	want := map[string]string{"esempio/negozio/ordini-api": "Ordini", "esempio/negozio/pagamenti-api": "Pagamenti", "esempio/catalogo-web": "Catalogo web"}
	var ids []string
	for full, app := range want {
		r := prj[full]
		sg, _ := r["suggestion"].(map[string]any)
		if r["status"] != "suggested" || sg == nil || sg["application"].(map[string]any)["name"] != app {
			t.Fatalf("proposta per %s: %v", full, r)
		}
		if full != "esempio/catalogo-web" {
			ids = append(ids, r["id"].(string))
		}
	}
	if prj["esempio/negozio/strumenti-interni"]["status"] != "none" {
		t.Fatalf("proposta inattesa: %v", prj["esempio/negozio/strumenti-interni"])
	}
	if n := e.admin.do("POST", glAPI+"/integrations/"+integrationID+"/projects/accept", map[string]any{"ids": ids}, http.StatusOK)["confirmed"]; n != 2.0 {
		t.Fatalf("conferme: %v", n)
	}
	e.admin.do("PUT", glAPI+"/integrations/"+integrationID+"/projects/"+prj["esempio/catalogo-web"]["id"].(string),
		map[string]any{"status": "ignored"}, http.StatusOK)
	return integrationID, profileID
}

func TestGitLabConnectorSync(t *testing.T) {
	e := newGLEnv(t)

	// Senza «Rete interna» un GitLab su un indirizzo interno si rifiuta già alla configurazione.
	in := e.input("")
	in["private_network"] = false
	e.admin.do("POST", glAPI+"/integrations", in, http.StatusUnprocessableEntity)
	// Il token è obbligatorio, e l'integrazione vuole almeno un gruppo o un progetto.
	in = e.input("")
	in["token"] = ""
	e.admin.do("POST", glAPI+"/integrations", in, http.StatusUnprocessableEntity)
	in = e.input("")
	in["groups"] = []string{}
	e.admin.do("POST", glAPI+"/integrations", in, http.StatusUnprocessableEntity)

	id, _ := e.setup()
	if len(e.ing.calls) != 2 || e.ing.calls[0].kind != "service" {
		t.Fatalf("servizi annunciati alla conferma: %+v", e.ing.calls)
	}
	e.ing.take()
	// Il progetto di un sottogruppo si è scoperto con la paginazione keyset.
	if e.fg.Count("pagination=keyset") == 0 || e.fg.Count("include_subgroups=true") == 0 {
		t.Fatalf("la scoperta doveva usare il keyset e i sottogruppi: %v", e.fg.Requests(false))
	}

	// Il token non torna mai dalle API.
	raw, _ := json.Marshal(e.admin.do("GET", glAPI+"/integrations/"+id, nil, http.StatusOK))
	if strings.Contains(string(raw), glToken) || strings.Contains(string(raw), "segreto-del-webhook") || !strings.Contains(string(raw), `"has_token":true`) {
		t.Fatalf("segreti nella risposta: %s", raw)
	}

	// Prova di connessione.
	tr := e.admin.do("POST", glAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != true || !strings.Contains(tr["message"].(string), "Connessione riuscita con GitLab 17.5.1-ee con il token «Regia (lettura)»") ||
		len(tr["warnings"].([]any)) != 0 {
		t.Fatalf("prova di connessione: %v", tr)
	}

	// Prima sincronizzazione.
	run := e.sync(id, false)
	calls := e.ing.take()
	if run["status"] != "ok" {
		t.Fatalf("esito: %v", run)
	}
	if n := countKind(calls, "release"); n != 3 {
		t.Fatalf("Release: %d (attese 2 di ordini-api e 1 di pagamenti-api): %+v", n, calls)
	}
	if countKind(calls, "sbom") != 2 || countKind(calls, "vulns") != 2 || countKind(calls, "check_run") != 2 {
		t.Fatalf("SBOM %d, report %d, check run %d", countKind(calls, "sbom"), countKind(calls, "vulns"), countKind(calls, "check_run"))
	}
	if n := countKind(calls, "deployment"); n != 5 {
		t.Fatalf("deploy: %d", n)
	}
	stats := run["stats"].(map[string]any)
	if stats["artifacts"].(float64) < 1 || stats["pipelines"].(float64) != 2 || stats["environments"].(float64) != 5 {
		t.Fatalf("numeri della sincronizzazione: %v", stats)
	}
	var rels = map[string]contract.Release{}
	var vulns = map[string]contract.VulnReport{}
	deps := map[string]string{}
	for _, c := range calls {
		if c.tenant != e.ten.TenantID {
			t.Fatalf("oggetto consegnato nel tenant sbagliato: %+v", c)
		}
		switch x := c.obj.(type) {
		case contract.Release:
			rels[x.Service.Name+"@"+x.Version] = x
		case contract.VulnReport:
			vulns[x.Service.Name+"@"+x.Version] = x
		case contract.Deployment:
			if x.Source != "pipeline" || !strings.HasPrefix(x.ExternalRef, "gitlab:deployment:") {
				t.Fatalf("deploy: %+v", x)
			}
			deps[x.Service.Name+"@"+x.Environment+"@"+x.Version] = x.Status
		case contract.CheckRun:
			want := map[string]string{"ordini-api": "passed", "pagamenti-api": "failed"}[x.Service.Name]
			if x.Verdict != want || x.RunID == "" {
				t.Fatalf("verdetto di %s: %s, atteso %s", x.Service.Name, x.Verdict, want)
			}
			if x.Service.Name == "ordini-api" {
				outcomes := map[string]string{}
				for _, ch := range x.Checks {
					outcomes[ch.Name] = ch.Outcome
				}
				if outcomes["container_scanning"] != "passed" || outcomes["deploy_production"] != "skipped" || outcomes["lint"] != "passed" {
					t.Fatalf("job come controlli: %v", outcomes)
				}
			}
		}
	}
	// v1.3.0: gli allegati stanno negli artefatti del job; v1.4.0: nei link della Release.
	r130, r140 := rels["ordini-api@1.3.0"], rels["ordini-api@1.4.0"]
	if r130.Service.Application != "ORD" || r130.Service.Provider != "gitlab" || r130.Service.Repository != "esempio/negozio/ordini-api" ||
		r130.CVEsAtRelease == nil || r130.CVEsAtRelease.High != 1 || r130.CVEsAtRelease.FixableHigh != 1 || r130.RequestedBy != "dev-uno" {
		t.Fatalf("Release 1.3.0: %+v", r130)
	}
	if r140.CVEsAtRelease == nil || r140.CVEsAtRelease.Critical != 1 || r140.CVEsAtRelease.FixableCritical != 1 || r140.CVEsAtRelease.Medium != 1 || r140.Environment != "prod" {
		t.Fatalf("Release 1.4.0: %+v", r140)
	}
	// Il report nel formato di GitLab si traduce: CVE preferita, GHSA come alias, correzione ricavata dalla soluzione.
	var sawFix, sawAlias bool
	for _, v := range vulns["ordini-api@1.4.0"].Vulnerabilities {
		if v.ID == "CVE-2026-2222" && v.FixedIn == "3.0.14" && v.Layer == "image" && v.Severity == "critical" {
			sawFix = true
		}
		if v.ID == "CVE-2026-3333" && len(v.Aliases) == 1 && v.Aliases[0] == "GHSA-aaaa-bbbb-cccc" && v.Layer == "app" {
			sawAlias = true
		}
	}
	if !sawFix || !sawAlias {
		t.Fatalf("report 1.4.0: %+v", vulns["ordini-api@1.4.0"])
	}
	if deps["ordini-api@prod@1.3.0"] != "succeeded" || deps["ordini-api@prod@1.4.0"] != "succeeded" || deps["pagamenti-api@prod@2.1.0"] != "succeeded" {
		t.Fatalf("deploy: %v", deps)
	}
	var staging string
	for k := range deps {
		if strings.HasPrefix(k, "ordini-api@staging@") {
			staging = strings.TrimPrefix(k, "ordini-api@staging@")
		}
	}
	if len(staging) != 12 {
		t.Fatalf("un deploy da branch ha per versione lo sha breve: %v", deps)
	}

	// Idempotenza: niente di nuovo, niente oggetti.
	e.sync(id, false)
	if calls := e.ing.take(); len(calls) != 0 {
		t.Fatalf("seconda sincronizzazione senza novità: %d oggetti (%+v)", len(calls), calls)
	}
	// «Rileggi tutto» ignora i cursori, e l'Ingestor scarta i doppioni.
	e.sync(id, true)
	if calls := e.ing.take(); countKind(calls, "release") != 3 {
		t.Fatalf("sincronizzazione completa: %+v", calls)
	}

	// Incrementale: una Release il cui tag ha una pipeline ancora in corso si riprova finché
	// gli artefatti non arrivano.
	now := time.Now()
	const proj = "esempio/negozio/ordini-api"
	e.fg.AddRelease(proj, "v1.5.0", "dev-uno", now, nil)
	pl := e.fg.AddPipeline(proj, "v1.5.0", "running", now, fakegitlab.JobSpec{Name: "dependency_scanning", Status: "running", Artifacts: map[string][]byte{
		"gl-sbom-npm-npm.cdx.json":           fakegithub.CycloneDX("express@4.21.0"),
		"gl-dependency-scanning-report.json": fakegitlab.SecurityReport("trivy", fakegitlab.GLVuln{ID: "CVE-2026-4444", Package: "express", Version: "4.21.0", Severity: "Low"}),
	}})
	e.sync(id, false)
	calls = e.ing.take()
	if countKind(calls, "release") != 1 || countKind(calls, "sbom") != 0 {
		t.Fatalf("release con pipeline in corso: %+v", calls)
	}
	e.sync(id, false)
	if calls = e.ing.take(); countKind(calls, "release") != 1 {
		t.Fatalf("la Release incompleta si riprova: %+v", calls)
	}
	e.fg.SetPipelineStatus(proj, pl.ID, "success", now)
	e.sync(id, false)
	if calls = e.ing.take(); countKind(calls, "release") != 1 || countKind(calls, "sbom") != 1 || countKind(calls, "vulns") != 1 {
		t.Fatalf("Release completata dagli artefatti: %+v", calls)
	}
	e.sync(id, false)
	if calls = e.ing.take(); len(calls) != 0 {
		t.Fatalf("Release completa, niente da rileggere: %+v", calls)
	}

	// Un deployment che cambia stato si riporta due volte; un nuovo esito della pipeline una volta.
	d := e.fg.AddDeployment(proj, "production", "v1.5.0", "0123456789abcdef0123456789abcdef01234567", true, "running", now)
	e.sync(id, false)
	if calls = e.ing.take(); countKind(calls, "deployment") != 1 || calls[0].obj.(contract.Deployment).Status != "running" {
		t.Fatalf("deploy in corso: %+v", calls)
	}
	e.fg.SetDeploymentStatus(proj, d.ID, "success", now.Add(time.Minute))
	e.fg.AddPipeline(proj, "main", "failed", now.Add(time.Minute), fakegitlab.JobSpec{Name: "test", Status: "failed"})
	e.sync(id, false)
	calls = e.ing.take()
	if countKind(calls, "deployment") != 1 || calls[0].obj.(contract.Deployment).Status != "succeeded" || countKind(calls, "check_run") != 1 {
		t.Fatalf("deploy concluso e nuova pipeline: %+v", calls)
	}

	// Limite di velocità: un 429 breve si aspetta, uno lungo ferma la sincronizzazione.
	e.fg.FailNext("/projects/"+fmt.Sprint(e.fg.ProjectID(proj))+"/releases", 429, 1)
	if run = e.sync(id, false); run["status"] != "ok" {
		t.Fatalf("429 breve: %v", run)
	}
	e.fg.FailNext("/projects/", 429, 3600)
	run = e.sync(id, false)
	if run["status"] != "rate_limited" || !strings.Contains(run["error"].(string), "limite di velocità") {
		t.Fatalf("429 lungo: %v", run)
	}
	it := e.admin.do("GET", glAPI+"/integrations/"+id, nil, http.StatusOK)
	if it["sync"].(map[string]any)["last_status"] != "rate_limited" {
		t.Fatalf("stato: %v", it["sync"])
	}
	e.ing.take()
}

func TestGitLabConnectorWebhook(t *testing.T) {
	e := newGLEnv(t)
	id, _ := e.setup()
	e.sync(id, false)
	e.ing.take()
	pid := e.fg.ProjectID("esempio/negozio/ordini-api")
	hook := map[string]any{"object_kind": "pipeline", "project": map[string]any{"id": pid},
		"object_attributes": map[string]any{"status": "success", "tag": true, "ref": "v1.6.0"}}
	const secret = "segreto-del-webhook-di-prova"

	if st, _ := e.webhook(id, "", "Pipeline Hook", "u1", hook); st != http.StatusUnauthorized {
		t.Fatalf("senza token: %d", st)
	}
	if st, _ := e.webhook(id, "sbagliato", "Pipeline Hook", "u1", hook); st != http.StatusUnauthorized {
		t.Fatalf("token errato: %d", st)
	}
	if st, _ := e.webhook("00000000-0000-4000-8000-000000000000", secret, "Pipeline Hook", "u1", hook); st != http.StatusNotFound {
		t.Fatalf("integrazione inesistente: %d", st)
	}

	// Una Release nuova con la pipeline del tag conclusa: il webhook la porta subito.
	now := time.Now()
	const proj = "esempio/negozio/ordini-api"
	e.fg.AddRelease(proj, "v1.6.0", "dev-due", now, nil)
	e.fg.AddPipeline(proj, "v1.6.0", "success", now, fakegitlab.JobSpec{Name: "sbom", Status: "success", Artifacts: map[string][]byte{
		"gl-sbom-npm-npm.cdx.json": fakegithub.CycloneDX("express@4.21.0"),
	}})
	st, out := e.webhook(id, secret, "Pipeline Hook", "u2", hook)
	if st != http.StatusAccepted || !strings.Contains(out["scope"].(string), "release") {
		t.Fatalf("webhook pipeline di un tag: %d %v", st, out)
	}
	calls := e.ing.take()
	if countKind(calls, "release") != 1 || countKind(calls, "sbom") != 1 {
		t.Fatalf("sincronizzazione mirata: %+v", calls)
	}
	// La stessa consegna vale una volta sola.
	if st, out := e.webhook(id, secret, "Pipeline Hook", "u2", hook); st != http.StatusOK || out["message"] != "consegna già ricevuta" {
		t.Fatalf("consegna ripetuta: %d %v", st, out)
	}
	if calls := e.ing.take(); len(calls) != 0 {
		t.Fatalf("la consegna ripetuta non deve sincronizzare: %+v", calls)
	}
	// Una pipeline non conclusa, un progetto non associato e un evento sconosciuto si ignorano.
	running := map[string]any{"object_kind": "pipeline", "project": map[string]any{"id": pid}, "object_attributes": map[string]any{"status": "running"}}
	if st, out := e.webhook(id, secret, "Pipeline Hook", "u3", running); st != http.StatusOK || !strings.Contains(out["message"].(string), "non ancora conclusa") {
		t.Fatalf("pipeline in corso: %d %v", st, out)
	}
	other := map[string]any{"object_kind": "deployment", "status": "success", "project": map[string]any{"id": e.fg.ProjectID("esempio/catalogo-web")}}
	if st, out := e.webhook(id, secret, "Deployment Hook", "u4", other); st != http.StatusOK || !strings.Contains(out["message"].(string), "non associato") {
		t.Fatalf("progetto non associato: %d %v", st, out)
	}
	if st, out := e.webhook(id, secret, "Note Hook", "u5", map[string]any{"object_kind": "note"}); st != http.StatusOK || !strings.Contains(out["message"].(string), "ignorato") {
		t.Fatalf("evento sconosciuto: %d %v", st, out)
	}
	// Un deployment arriva con il suo webhook; senza UUID la consegna si riconosce dal corpo.
	e.fg.AddDeployment(proj, "production", "v1.6.0", "abcdefabcdefabcdefabcdefabcdefabcdefabcd", true, "success", now)
	dep := map[string]any{"object_kind": "deployment", "status": "success", "project": map[string]any{"id": pid}}
	if st, out := e.webhook(id, secret, "Deployment Hook", "", dep); st != http.StatusAccepted || out["scope"] != "deployment" {
		t.Fatalf("deployment: %d %v", st, out)
	}
	if calls := e.ing.take(); countKind(calls, "deployment") != 1 {
		t.Fatalf("deploy del webhook: %+v", calls)
	}
	if st, out := e.webhook(id, secret, "Deployment Hook", "", dep); st != http.StatusOK || out["message"] != "consegna già ricevuta" {
		t.Fatalf("consegna senza UUID ripetuta: %d %v", st, out)
	}
	// Il webhook si disattiva togliendo il segreto.
	e.admin.do("PATCH", glAPI+"/integrations/"+id, func() map[string]any {
		in := e.input("")
		delete(in, "webhook_secret")
		in["remove_webhook_secret"] = true
		return in
	}(), http.StatusNoContent)
	if st, _ := e.webhook(id, secret, "Pipeline Hook", "u9", hook); st != http.StatusNotFound {
		t.Fatalf("webhook senza segreto: %d", st)
	}
	// Un nuovo segreto si mostra una volta.
	sec := e.admin.do("POST", glAPI+"/integrations/"+id+"/webhook-secret", nil, http.StatusOK)["secret"].(string)
	if !strings.HasPrefix(sec, "glwh_") {
		t.Fatalf("segreto generato: %q", sec)
	}
	if st, _ := e.webhook(id, sec, "Pipeline Hook", "u10", running); st != http.StatusOK {
		t.Fatalf("nuovo segreto: %d", st)
	}
}

func TestGitLabConnectorTokenChecks(t *testing.T) {
	e := newGLEnv(t)
	check := func(token string) map[string]any {
		in := e.input("")
		in["token"] = token
		in["name"] = "prova " + token
		id := e.admin.do("POST", glAPI+"/integrations", in, http.StatusCreated)["id"].(string)
		return e.admin.do("POST", glAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	}
	// Scope in eccesso: funziona, ma avvisa.
	e.fg.AddToken("glpat-api", "troppo-potente", "api", "write_repository")
	if r := check("glpat-api"); r["ok"] != true || !strings.Contains(fmt.Sprint(r["warnings"]), "write_repository") {
		t.Fatalf("scope in eccesso: %v", r)
	}
	// Scope mancante: non funziona e lo dice.
	e.fg.AddToken("glpat-repo", "solo-repo", "read_repository")
	if r := check("glpat-repo"); r["ok"] != false || !strings.Contains(r["message"].(string), "manca lo scope read_api") {
		t.Fatalf("scope mancante: %v", r)
	}
	// In scadenza e scaduto.
	e.fg.AddToken("glpat-scade", "in-scadenza", "read_api").ExpiresAt = time.Now().Add(5 * 24 * time.Hour).Format("2006-01-02")
	if r := check("glpat-scade"); r["ok"] != true || !strings.Contains(fmt.Sprint(r["warnings"]), "scade il") {
		t.Fatalf("token in scadenza: %v", r)
	}
	e.fg.AddToken("glpat-vecchio", "scaduto", "read_api").ExpiresAt = time.Now().Add(-48 * time.Hour).Format("2006-01-02")
	if r := check("glpat-vecchio"); r["ok"] != false || !strings.Contains(r["message"].(string), "scaduto") {
		t.Fatalf("token scaduto: %v", r)
	}
	// Token sconosciuto o gruppo inesistente.
	if r := check("glpat-sconosciuto"); r["ok"] != false || !strings.Contains(r["message"].(string), "401") {
		t.Fatalf("token rifiutato: %v", r)
	}
	in := e.input("")
	in["groups"] = []string{"non-esiste"}
	id := e.admin.do("POST", glAPI+"/integrations", in, http.StatusCreated)["id"].(string)
	if r := e.admin.do("POST", glAPI+"/integrations/"+id+"/test", nil, http.StatusOK); r["ok"] != false || !strings.Contains(r["message"].(string), "gruppo «non-esiste»") {
		t.Fatalf("gruppo inesistente: %v", r)
	}
	// Istanza precedente alla 15.5: gli scope non si possono verificare, ma si va avanti.
	e.fg.NoTokenInfo = true
	if r := check(glToken); r["ok"] != true || !strings.Contains(fmt.Sprint(r["warnings"]), "non si possono verificare") {
		t.Fatalf("istanza vecchia: %v", r)
	}
	// Un token di progetto: l'integrazione elenca i progetti, non i gruppi.
	e.fg.NoTokenInfo = false
	in = e.input("")
	in["groups"] = []string{}
	in["projects"] = []string{"esempio/negozio/ordini-api"}
	id = e.admin.do("POST", glAPI+"/integrations", in, http.StatusCreated)["id"].(string)
	if r := e.admin.do("POST", glAPI+"/integrations/"+id+"/test", nil, http.StatusOK); r["ok"] != true || !strings.Contains(r["message"].(string), "Progetto raggiungibile: esempio/negozio/ordini-api") {
		t.Fatalf("token di progetto: %v", r)
	}
	if res := e.admin.do("POST", glAPI+"/integrations/"+id+"/discover", nil, http.StatusOK); res["found"] != 1.0 {
		t.Fatalf("scoperta per progetto: %v", res)
	}
}

func TestGitLabConnectorPreview(t *testing.T) {
	e := newGLEnv(t)
	id, profileID := e.setup()
	pv := e.admin.do("POST", glAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/negozio/ordini-api", "profile_id": profileID}, http.StatusOK)
	if pv["release_tag"] != "v1.4.0" || pv["sbom"] == nil || pv["vulnerabilities"] == nil || pv["check_ready"] != true || len(pv["deployments"].([]any)) == 0 {
		t.Fatalf("anteprima: %v", pv)
	}
	if !strings.Contains(fmt.Sprint(pv["assets_read"]), "link: gl-sbom-npm-npm.cdx.json") {
		t.Fatalf("allegati letti: %v", pv["assets_read"])
	}
	// Una Release precisa, i cui allegati sono negli artefatti dei job; il profilo è la bozza dell'editor.
	pre := e.admin.do("GET", glAPI+"/presets", nil, http.StatusOK)["presets"].([]any)[0].(map[string]any)
	pv = e.admin.do("POST", glAPI+"/integrations/"+id+"/preview",
		map[string]any{"repository": "esempio/negozio/ordini-api", "definition": pre["definition"], "tag": "v1.3.0"}, http.StatusOK)
	if pv["release_tag"] != "v1.3.0" || pv["sbom"] == nil || !strings.Contains(fmt.Sprint(pv["assets_read"]), "artefatto del job dependency_scanning") {
		t.Fatalf("anteprima degli artefatti: %v", pv)
	}
	if n := len(e.ing.take()); n != 0 {
		t.Fatalf("l'anteprima non deve salvare niente (%d oggetti)", n)
	}
	// Un profilo non valido non si prova.
	e.admin.do("POST", glAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/negozio/ordini-api", "definition": map[string]any{"format": "x"}}, http.StatusUnprocessableEntity)
	// Un progetto non scoperto nemmeno.
	e.admin.do("POST", glAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/altro"}, http.StatusUnprocessableEntity)
}
