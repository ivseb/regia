package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/devtools/fakegithub"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/requirements"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// DevSecOps fase 1: connettore GitHub / GitHub Enterprise Server e profili di pipeline
// (replica/devsecops.md §2 e §4; migrazione 0025), contro un finto GitHub in httptest.

// recIngestor è un Ingestor finto che registra le chiamate con il tenant della transazione.
type recIngestor struct {
	mu    sync.Mutex
	calls []ingested
}

type ingested struct {
	kind, tenant string
	obj          any
}

func (r *recIngestor) add(ctx context.Context, tx pgx.Tx, kind string, obj any) error {
	var t string
	if err := tx.QueryRow(ctx, "select app_tenant()::text").Scan(&t); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ingested{kind: kind, tenant: t, obj: obj})
	return nil
}

func (r *recIngestor) IngestCheckRun(ctx context.Context, tx pgx.Tx, _ engine.Actor, c contract.CheckRun) error {
	return r.add(ctx, tx, "check_run", c)
}
func (r *recIngestor) IngestRelease(ctx context.Context, tx pgx.Tx, _ engine.Actor, x contract.Release) error {
	return r.add(ctx, tx, "release", x)
}
func (r *recIngestor) IngestDeployment(ctx context.Context, tx pgx.Tx, _ engine.Actor, d contract.Deployment) error {
	return r.add(ctx, tx, "deployment", d)
}
func (r *recIngestor) IngestSBOM(ctx context.Context, tx pgx.Tx, _ engine.Actor, s contract.SBOM) error {
	return r.add(ctx, tx, "sbom", s)
}
func (r *recIngestor) IngestVulnReport(ctx context.Context, tx pgx.Tx, _ engine.Actor, v contract.VulnReport) error {
	return r.add(ctx, tx, "vulns", v)
}
func (r *recIngestor) IngestService(ctx context.Context, tx pgx.Tx, _ engine.Actor, s contract.Service) error {
	return r.add(ctx, tx, "service", s)
}

// take restituisce le chiamate dall'ultima volta, escluse quelle dei servizi.
func (r *recIngestor) take() []ingested {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ingested
	for _, c := range r.calls {
		if c.kind != "service" {
			out = append(out, c)
		}
	}
	r.calls = nil
	return out
}

func countKind(calls []ingested, kind string) int {
	n := 0
	for _, c := range calls {
		if c.kind == kind {
			n++
		}
	}
	return n
}

type ghEnv struct {
	t     *testing.T
	base  string
	gh    *ghconn.Service
	ing   *recIngestor
	fg    *fakegithub.Server
	fgURL string
	caPEM string
	ten   tenant.Created
	admin *user
	slug  string
}

var ghSeq int

func newGHEnv(t *testing.T) *ghEnv {
	d := needDB(t)
	ctx := context.Background()
	ghSeq++
	slug := fmt.Sprintf("gh%d-%s", ghSeq, strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "GitHub " + slug, AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Un finto GitHub Enterprise Server in https (certificato del test, da fidare come CA interna).
	fg := fakegithub.New()
	fg.BasePath, fg.Enterprise = "/api/v3", "3.15.0"
	fakegithub.Demo(fg, time.Now())
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
	// Dev falso: la protezione SSRF è attiva; l'operatore consente le reti interne.
	gh := ghconn.New(ghconn.Options{DB: d, Box: box, Ingestor: ing, AllowPrivate: true, Log: log, Timeout: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error { return nil }})
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Log: log,
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, GitHub: gh,
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(gh.WaitIdle)
	e := &ghEnv{t: t, base: hs.URL, gh: gh, ing: ing, fg: fg, fgURL: fgs.URL, caPEM: ca, ten: ten, slug: slug}
	e.admin = login(t, hs.URL, "admin@"+slug+".test", testPassword)
	return e
}

func (e *ghEnv) integrationInput(profileID string) map[string]any {
	return map[string]any{
		"name": "GitHub Enterprise", "base_url": e.fgURL, "owners": []string{"esempio"}, "auth_mode": "app",
		"app_id": e.fg.AppID, "installation_id": e.fg.InstallationID, "private_key": e.fg.PrivateKeyPEM(),
		"webhook_secret": "segreto-del-webhook-di-prova", "profile_id": profileID, "gitops_repositories": []string{"esempio/negozio-gitops"},
		"private_network": true, "ca_certificate": e.caPEM,
	}
}

const ghAPI = "/api/v1/devsecops/github"

func (e *ghEnv) repos(id string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, it := range e.admin.do("GET", ghAPI+"/integrations/"+id+"/repositories", nil, http.StatusOK)["items"].([]any) {
		r := it.(map[string]any)
		out[r["full_name"].(string)] = r
	}
	return out
}

func (e *ghEnv) sync(id string, full bool) map[string]any {
	e.t.Helper()
	q := ""
	if full {
		q = "?full=true"
	}
	e.admin.do("POST", ghAPI+"/integrations/"+id+"/sync"+q, nil, http.StatusAccepted)
	e.gh.WaitIdle()
	runs := e.admin.do("GET", ghAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"].([]any)
	return runs[0].(map[string]any)
}

// setup crea applicazioni, profilo dal preset, integrazione, e associa i repository.
func (e *ghEnv) setup() (integrationID, profileID string) {
	t := e.t
	for _, a := range []map[string]any{{"name": "Ordini", "code": "ORD"}, {"name": "Pagamenti", "code": "PAG"}, {"name": "Catalogo web"}} {
		e.admin.do("POST", "/api/v1/architecture/applications", a, http.StatusCreated)
	}
	profileID = e.admin.do("POST", "/api/v1/devsecops/pipeline-profiles", map[string]any{"preset": profile.PresetGitOpsArgo}, http.StatusCreated)["id"].(string)
	integrationID = e.admin.do("POST", ghAPI+"/integrations", e.integrationInput(profileID), http.StatusCreated)["id"].(string)
	res := e.admin.do("POST", ghAPI+"/integrations/"+integrationID+"/discover", nil, http.StatusOK)
	if res["found"].(float64) != 5 || res["new"].(float64) != 5 {
		t.Fatalf("scoperta: %v", res)
	}
	repos := e.repos(integrationID)
	want := map[string]string{"esempio/ordini-api": "Ordini", "esempio/pagamenti-api": "Pagamenti", "esempio/catalogo-web": "Catalogo web"}
	var ids []string
	for full, app := range want {
		r := repos[full]
		sg, _ := r["suggestion"].(map[string]any)
		if r["status"] != "suggested" || sg == nil || sg["application"].(map[string]any)["name"] != app {
			t.Fatalf("proposta per %s: %v", full, r)
		}
		if full != "esempio/catalogo-web" {
			ids = append(ids, r["id"].(string))
		}
	}
	if repos["esempio/strumenti-interni"]["status"] != "none" || repos["esempio/negozio-gitops"]["status"] != "none" {
		t.Fatalf("proposte inattese: %v %v", repos["esempio/strumenti-interni"], repos["esempio/negozio-gitops"])
	}
	if n := e.admin.do("POST", ghAPI+"/integrations/"+integrationID+"/repositories/accept", map[string]any{"ids": ids}, http.StatusOK)["confirmed"]; n != 2.0 {
		t.Fatalf("conferme: %v", n)
	}
	e.admin.do("PUT", ghAPI+"/integrations/"+integrationID+"/repositories/"+repos["esempio/catalogo-web"]["id"].(string),
		map[string]any{"status": "ignored"}, http.StatusOK)
	return integrationID, profileID
}

func TestGitHubConnectorSync(t *testing.T) {
	e := newGHEnv(t)
	d := needDB(t)
	ctx := context.Background()

	// Senza «Rete interna» un GHES su un indirizzo interno si rifiuta già alla configurazione.
	in := e.integrationInput("")
	in["private_network"] = false
	e.admin.do("POST", ghAPI+"/integrations", in, http.StatusUnprocessableEntity)

	id, profileID := e.setup()
	services := e.ing.calls
	if len(services) != 2 || services[0].kind != "service" {
		t.Fatalf("servizi annunciati alla conferma: %+v", services)
	}
	e.ing.take()

	// Prova di connessione come GitHub App su GHES.
	tr := e.admin.do("POST", ghAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != true || !strings.Contains(tr["message"].(string), "Connessione riuscita con GitHub Enterprise Server 3.15.0 come GitHub App") {
		t.Fatalf("prova di connessione: %v", tr)
	}

	// Prima sincronizzazione.
	run := e.sync(id, false)
	calls := e.ing.take()
	if run["status"] != "partial" && run["status"] != "ok" {
		t.Fatalf("esito: %v", run)
	}
	if n := countKind(calls, "release"); n != 3 {
		t.Fatalf("Release: %d (attese 2 di ordini-api e 1 di pagamenti-api): %+v", n, calls)
	}
	if countKind(calls, "sbom") != 2 || countKind(calls, "vulns") != 2 || countKind(calls, "check_run") != 2 {
		t.Fatalf("SBOM %d, report %d, check run %d", countKind(calls, "sbom"), countKind(calls, "vulns"), countKind(calls, "check_run"))
	}
	// Deployments API spenta dal preset: solo i 3 deploy letti dal repository GitOps.
	if n := countKind(calls, "deployment"); n != 3 {
		t.Fatalf("deploy: %d", n)
	}
	var v140 contract.Release
	var gitops []contract.Deployment
	for _, c := range calls {
		if c.tenant != e.ten.TenantID {
			t.Fatalf("oggetto consegnato nel tenant sbagliato: %+v", c)
		}
		switch x := c.obj.(type) {
		case contract.Release:
			if x.Version == "1.4.0" {
				v140 = x
			}
		case contract.Deployment:
			gitops = append(gitops, x)
		case contract.CheckRun:
			want := map[string]string{"ordini-api": "passed", "pagamenti-api": "failed"}[x.Service.Name]
			if x.Verdict != want {
				t.Fatalf("verdetto di %s: %s, atteso %s", x.Service.Name, x.Verdict, want)
			}
		}
	}
	if v140.ChangeNumber != "CHG-1042" || v140.ApprovedBy != "approvatrice" || v140.RequestedBy != "dev-due" || v140.Service.Application != "ORD" ||
		v140.Service.Repository != "esempio/ordini-api" || v140.Service.Provider != "github" || v140.CVEsAtRelease == nil || v140.CVEsAtRelease.FixableCritical != 1 ||
		len(v140.Checks) != 5 {
		t.Fatalf("Release 1.4.0: %+v", v140)
	}
	envs := map[string]string{}
	for _, g := range gitops {
		if g.Source != "gitops" || g.Status != "running" {
			t.Fatalf("deploy GitOps: %+v", g)
		}
		envs[g.Service.Name+"@"+g.Environment] = g.Version
	}
	if envs["ordini-api@prod"] != "1.4.0" || envs["pagamenti-api@prod"] != "2.1.0" || envs["ordini-api@staging"] != "1.5.0-3-abcdef12" {
		t.Fatalf("ambienti: %v", envs)
	}
	stats := run["stats"].(map[string]any)
	if !strings.Contains(fmt.Sprint(stats["warnings"]), "vecchio-batch") {
		t.Fatalf("il servizio GitOps senza repository va segnalato: %v", stats["warnings"])
	}

	// Idempotenza: niente di nuovo, niente oggetti.
	run = e.sync(id, false)
	if calls := e.ing.take(); len(calls) != 0 {
		t.Fatalf("seconda sincronizzazione senza novità: %d oggetti (%+v)", len(calls), calls)
	}

	// Incrementale: una nuova Release, un allegato aggiunto dopo (finalize), un nuovo commit.
	now := time.Now()
	c3 := e.fg.Push("esempio/ordini-api", "main", []fakegithub.CheckRun{{Name: "ci / Verdict", Status: "completed", Conclusion: "failure", CompletedAt: now}})
	e.fg.AddRelease("esempio/ordini-api", "v1.5.0", "dev-uno", now, map[string][]byte{
		"release-record.json": fakegithub.ReleaseRecord(fakegithub.Record{Service: "ordini-api", Version: "1.5.0", Commit: c3, MergedBy: "approvatrice", MergedAt: now}),
	})
	e.fg.AddAsset("esempio/pagamenti-api", "v2.1.0", "release-record.json",
		fakegithub.ReleaseRecord(fakegithub.Record{Service: "pagamenti-api", Version: "2.1.0", Change: "CHG-2000", MergedBy: "approvatore", MergedAt: now}))
	e.sync(id, false)
	calls = e.ing.take()
	if countKind(calls, "release") != 2 || countKind(calls, "check_run") != 1 || countKind(calls, "deployment") != 0 {
		t.Fatalf("incrementale: %+v", calls)
	}
	// Un file GitOps cambiato: solo quel file si rilegge.
	e.fg.SetFile("esempio/negozio-gitops", "main", "envs/prod/negozio.yaml", fakegithub.EnvFile(map[string]fakegithub.EnvEntry{
		"ordini-api": {Image: "registry.example.test/ordini-api", Tag: "1.5.0", Version: "1.5.0"},
	}))
	e.sync(id, false)
	calls = e.ing.take()
	if len(calls) != 1 || calls[0].obj.(contract.Deployment).Version != "1.5.0" {
		t.Fatalf("GitOps incrementale: %+v", calls)
	}

	// Nuova versione del profilo: la Deployments API si legge, con i nomi degli ambienti.
	prof := e.admin.do("GET", "/api/v1/devsecops/pipeline-profiles/"+profileID, nil, http.StatusOK)["profile"].(map[string]any)
	def := prof["definition"].(map[string]any)
	def["deployments"] = map[string]any{"environments": map[string]string{"production": "prod"}}
	if v := e.admin.do("PUT", "/api/v1/devsecops/pipeline-profiles/"+profileID, map[string]any{"name": prof["name"], "definition": def,
		"note": "Deployments API"}, http.StatusOK)["version"]; v != 2.0 {
		t.Fatalf("nuova versione del profilo: %v", v)
	}
	run = e.sync(id, false)
	calls = e.ing.take()
	deps := map[string]string{}
	for _, c := range calls {
		if dep, ok := c.obj.(contract.Deployment); ok {
			deps[dep.Environment] = dep.Status + " " + dep.Version + " " + dep.Source
		}
	}
	if len(calls) != 2 || deps["prod"] != "succeeded 2.1.0 pipeline" || deps["staging"] != "succeeded 2.1.0 pipeline" {
		t.Fatalf("Deployments API: %v (%+v)", deps, calls)
	}
	if run["stats"].(map[string]any)["profile_version"] != 2.0 {
		t.Fatalf("versione del profilo usata: %v", run["stats"])
	}

	// Sincronizzazione completa: si rilegge tutto (l'Ingestor scarta i doppioni).
	e.sync(id, true)
	if calls := e.ing.take(); countKind(calls, "release") != 4 || countKind(calls, "deployment") < 3 {
		t.Fatalf("completa: %d Release, %d deploy", countKind(calls, "release"), countKind(calls, "deployment"))
	}

	// Limite di velocità esaurito: la sincronizzazione si ferma e riprende dopo il reset.
	e.fg.SetRemaining(0, 2*time.Hour)
	run = e.sync(id, false)
	if run["status"] != "rate_limited" || !strings.Contains(fmt.Sprint(run["error"]), "limite di velocità") {
		t.Fatalf("limite di velocità: %v", run)
	}
	var next time.Time
	if err := d.Owner.QueryRow(ctx, "select next_run_at from connector_sync_state where integration_id = $1", id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if time.Until(next) < 110*time.Minute {
		t.Fatalf("prossima esecuzione prima del reset: %v", next)
	}
	it := e.admin.do("GET", ghAPI+"/integrations/"+id, nil, http.StatusOK)
	if rl := it["sync"].(map[string]any)["rate_limit"].(map[string]any); rl["remaining"] != 0.0 || rl["limit"] != 5000.0 {
		t.Fatalf("limiti mostrati: %v", rl)
	}
	e.fg.SetRemaining(5000, time.Hour)

	// Il job periodico prende le integrazioni dovute.
	if _, err := d.Owner.Exec(ctx, "update connector_sync_state set next_run_at = now() - interval '1 minute' where integration_id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.gh.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	runs := e.admin.do("GET", ghAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"].([]any)
	if r0 := runs[0].(map[string]any); r0["trigger"] != "schedule" || r0["status"] == "running" {
		t.Fatalf("esecuzione pianificata: %v", r0)
	}

	// Una sincronizzazione alla volta.
	if _, err := d.Owner.Exec(ctx, "update connector_sync_state set lease_until = now() + interval '5 minutes' where integration_id = $1", id); err != nil {
		t.Fatal(err)
	}
	e.admin.do("POST", ghAPI+"/integrations/"+id+"/sync", nil, http.StatusConflict)
	if _, err := d.Owner.Exec(ctx, "update connector_sync_state set lease_until = null where integration_id = $1", id); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubConnectorSecretsAndSSRF(t *testing.T) {
	e := newGHEnv(t)
	d := needDB(t)
	ctx := context.Background()
	id, _ := e.setup()
	pemKey := e.fg.PrivateKeyPEM()

	// I segreti non escono mai dalle API, né dal registro di audit, né in chiaro nel database.
	for _, path := range []string{ghAPI + "/integrations", ghAPI + "/integrations/" + id, ghAPI + "/integrations/" + id + "/repositories", ghAPI + "/integrations/" + id + "/runs"} {
		_, _, raw := e.admin.get(path)
		for _, secret := range []string{"PRIVATE KEY", "segreto-del-webhook-di-prova", strings.Split(pemKey, "\n")[1]} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("%s restituisce un segreto", path)
			}
		}
	}
	it := e.admin.do("GET", ghAPI+"/integrations/"+id, nil, http.StatusOK)
	if it["has_private_key"] != true || it["has_webhook_secret"] != true || it["has_token"] != false {
		t.Fatalf("indicatori dei segreti: %v", it)
	}
	var sealed []byte
	var auditText string
	if err := d.Owner.QueryRow(ctx, "select secret_enc from integrations where id = $1", id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("PRIVATE KEY")) || bytes.Contains(sealed, []byte("segreto-del-webhook")) {
		t.Fatal("segreti in chiaro nel database")
	}
	if err := d.Owner.QueryRow(ctx, "select coalesce(string_agg(payload::text, ' '), '') from audit_log where tenant_id = $1 and action like 'devsecops.%'",
		e.ten.TenantID).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, "PRIVATE KEY") || strings.Contains(auditText, "segreto-del-webhook") || !strings.Contains(auditText, `"auth_mode": "app"`) {
		t.Fatalf("audit: %s", auditText)
	}
	// Una modifica senza segreti li conserva; passare al token toglie la chiave.
	upd := e.integrationInput("")
	delete(upd, "private_key")
	delete(upd, "webhook_secret")
	upd["name"] = "GHES interno"
	e.admin.do("PATCH", ghAPI+"/integrations/"+id, upd, http.StatusNoContent)
	if tr := e.admin.do("POST", ghAPI+"/integrations/"+id+"/test", nil, http.StatusOK); tr["ok"] != true {
		t.Fatalf("dopo la modifica senza segreti: %v", tr)
	}
	upd["auth_mode"] = "token"
	e.admin.do("PATCH", ghAPI+"/integrations/"+id, upd, http.StatusUnprocessableEntity) // manca il token
	upd["token"] = "ghp_prova_dei_test"
	e.fg.PATs["ghp_prova_dei_test"] = true
	e.admin.do("PATCH", ghAPI+"/integrations/"+id, upd, http.StatusNoContent)
	it = e.admin.do("GET", ghAPI+"/integrations/"+id, nil, http.StatusOK)
	if it["has_private_key"] != false || it["has_token"] != true || it["has_webhook_secret"] != true {
		t.Fatalf("cambio di autenticazione: %v", it)
	}
	if tr := e.admin.do("POST", ghAPI+"/integrations/"+id+"/test", nil, http.StatusOK); tr["ok"] != true || !strings.Contains(tr["message"].(string), "con un token") {
		t.Fatalf("prova con il token: %v", tr)
	}
	// Token sbagliato: messaggio chiaro, non un errore interno.
	upd["token"] = "ghp_sbagliato"
	e.admin.do("PATCH", ghAPI+"/integrations/"+id, upd, http.StatusNoContent)
	tr := e.admin.do("POST", ghAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != false || !strings.Contains(tr["message"].(string), "Connessione non riuscita: GitHub ha rifiutato le credenziali (401)") {
		t.Fatalf("token sbagliato: %v", tr)
	}

	// SSRF: con la rete interna spenta, alla connessione l'indirizzo interno è bloccato
	// (qui la configurazione si cambia nel database, come farebbe un DNS che cambia).
	if _, err := d.Owner.Exec(ctx, `update integrations set config = jsonb_set(config, '{private_network}', 'false') where id = $1`, id); err != nil {
		t.Fatal(err)
	}
	tr = e.admin.do("POST", ghAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != false || !strings.Contains(tr["message"].(string), "destinazione non consentita") {
		t.Fatalf("SSRF alla connessione: %v", tr)
	}
	// Senza il consenso dell'operatore la rete interna non si attiva.
	strict := ghconn.New(ghconn.Options{DB: d})
	_, err := strict.Create(ctx, ghconn.Actor{TenantID: e.ten.TenantID}, ghconn.Input{Name: "x", BaseURL: e.fgURL, Owners: []string{"esempio"},
		AuthMode: "token", Token: "ghp_x", PrivateNetwork: true})
	if ge, ok := err.(*ghconn.Error); !ok || ge.Field != "private_network" {
		t.Fatalf("rete interna senza consenso: %v", err)
	}
}

func TestGitHubWebhooks(t *testing.T) {
	e := newGHEnv(t)
	id, _ := e.setup()
	e.sync(id, false)
	e.ing.take()
	repos := e.repos(id)
	ordiniExt := repos["esempio/ordini-api"]["external_id"].(string)
	catalogoExt := repos["esempio/catalogo-web"]["external_id"].(string)
	secret := "segreto-del-webhook-di-prova"

	post := func(integration, event, delivery string, payload any, sign string) (int, map[string]any) {
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", e.base+ghAPI+"/webhook/"+integration, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-GitHub-Delivery", delivery)
		if sign != "" {
			req.Header.Set("X-Hub-Signature-256", ghconn.Sign(sign, b))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		e.gh.WaitIdle()
		return resp.StatusCode, out
	}
	rel := map[string]any{"action": "published", "repository": map[string]any{"id": json.Number(ordiniExt), "full_name": "esempio/ordini-api"}}

	if st, _ := post(id, "release", "d-0", rel, ""); st != http.StatusUnauthorized {
		t.Fatalf("webhook senza firma: %d", st)
	}
	if st, _ := post(id, "release", "d-0", rel, "segreto-sbagliato-di-prova"); st != http.StatusUnauthorized {
		t.Fatalf("firma sbagliata: %d", st)
	}
	if st, _ := post("00000000-0000-7000-8000-000000000000", "ping", "d-x", map[string]any{}, secret); st != http.StatusNotFound {
		t.Fatalf("integrazione inesistente: %d", st)
	}
	if st, out := post(id, "ping", "d-1", map[string]any{"zen": "x"}, secret); st != http.StatusOK || !strings.Contains(out["message"].(string), "pong") {
		t.Fatalf("ping: %d %v", st, out)
	}
	if len(e.ing.take()) != 0 {
		t.Fatal("oggetti da un webhook non firmato o di prova")
	}

	// Una Release nuova arriva con il webhook: sincronizzazione mirata, solo le Release.
	e.fg.AddRelease("esempio/ordini-api", "v1.6.0", "dev-uno", time.Now(), map[string][]byte{
		"release-record.json": fakegithub.ReleaseRecord(fakegithub.Record{Service: "ordini-api", Version: "1.6.0", MergedBy: "approvatrice", MergedAt: time.Now()}),
	})
	e.fg.Push("esempio/ordini-api", "main", []fakegithub.CheckRun{{Name: "ci / Verdict", Status: "completed", Conclusion: "success", CompletedAt: time.Now()}})
	st, out := post(id, "release", "d-2", rel, secret)
	if st != http.StatusAccepted || out["scope"] != "release" {
		t.Fatalf("release: %d %v", st, out)
	}
	calls := e.ing.take()
	if len(calls) != 1 || calls[0].obj.(contract.Release).Version != "1.6.0" {
		t.Fatalf("sincronizzazione mirata: %+v", calls)
	}
	// La stessa consegna ripetuta non si elabora due volte.
	if st, out := post(id, "release", "d-2", rel, secret); st != http.StatusOK || !strings.Contains(out["message"].(string), "già ricevuta") {
		t.Fatalf("consegna ripetuta: %d %v", st, out)
	}
	// check_suite completata: solo i controlli.
	cs := map[string]any{"action": "completed", "repository": rel["repository"]}
	if st, _ := post(id, "check_suite", "d-3", cs, secret); st != http.StatusAccepted {
		t.Fatalf("check_suite: %d", st)
	}
	if calls := e.ing.take(); len(calls) != 1 || calls[0].kind != "check_run" {
		t.Fatalf("check_suite: %+v", calls)
	}
	// Repository non associato, evento non gestito: ignorati.
	ign := map[string]any{"action": "published", "repository": map[string]any{"id": json.Number(catalogoExt), "full_name": "esempio/catalogo-web"}}
	if st, out := post(id, "release", "d-4", ign, secret); st != http.StatusOK || !strings.Contains(out["message"].(string), "non associato") {
		t.Fatalf("repository ignorato: %d %v", st, out)
	}
	if st, _ := post(id, "issues", "d-5", ign, secret); st != http.StatusOK {
		t.Fatalf("evento non gestito: %d", st)
	}
	// Push sul repository GitOps.
	e.fg.SetFile("esempio/negozio-gitops", "main", "envs/staging/negozio.yaml", fakegithub.EnvFile(map[string]fakegithub.EnvEntry{
		"ordini-api": {Image: "registry.example.test/ordini-api", Tag: "1.6.0-1-0a1b2c3d"},
	}))
	push := map[string]any{"ref": "refs/heads/main", "repository": map[string]any{"id": 1, "full_name": "esempio/negozio-gitops"}}
	if st, out := post(id, "push", "d-6", push, secret); st != http.StatusAccepted || out["scope"] != "gitops" {
		t.Fatalf("push GitOps: %d %v", st, out)
	}
	if calls := e.ing.take(); len(calls) != 1 || calls[0].obj.(contract.Deployment).Environment != "staging" {
		t.Fatalf("push GitOps: %+v", calls)
	}
	// Segreto del webhook generato da Regia: mostrato una volta, il vecchio non vale più.
	newSecret := e.admin.do("POST", ghAPI+"/integrations/"+id+"/webhook-secret", nil, http.StatusOK)["secret"].(string)
	if st, _ := post(id, "ping", "d-7", map[string]any{}, secret); st != http.StatusUnauthorized {
		t.Fatalf("vecchio segreto: %d", st)
	}
	if st, _ := post(id, "ping", "d-8", map[string]any{}, newSecret); st != http.StatusOK {
		t.Fatalf("nuovo segreto: %d", st)
	}
}

func TestGitHubProfilesPreviewAndIsolation(t *testing.T) {
	e := newGHEnv(t)
	id, profileID := e.setup()
	profiles := "/api/v1/devsecops/pipeline-profiles"

	// Anteprima: cosa estrarrebbe il profilo, senza salvare.
	pv := e.admin.do("POST", ghAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/ordini-api"}, http.StatusOK)
	relp := pv["release"].(map[string]any)
	if pv["release_tag"] != "v1.4.0" || relp["change_number"] != "CHG-1042" || pv["sbom"].(map[string]any)["components"] != 4.0 ||
		pv["vulnerabilities"].(map[string]any)["total"] != 2.0 || pv["check_ready"] != true || len(pv["gitops"].([]any)) != 2 {
		t.Fatalf("anteprima: %v", pv)
	}
	if calls := e.ing.take(); len(calls) != 0 {
		t.Fatalf("l'anteprima ha salvato oggetti: %+v", calls)
	}
	// Anteprima con una bozza non salvata, su una Release precisa.
	draft := map[string]any{"format": profile.Format, "release": map[string]any{"tag_prefix": "v",
		"record": map[string]any{"asset": "release-record.json", "fields": map[string]any{"change_number": "=CHG-FISSO"}}}}
	pv = e.admin.do("POST", ghAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/ordini-api", "definition": draft, "tag": "v1.3.0"}, http.StatusOK)
	if pv["release"].(map[string]any)["change_number"] != "CHG-FISSO" || pv["release"].(map[string]any)["version"] != "1.3.0" ||
		!strings.Contains(pv["profile"].(string), "bozza") {
		t.Fatalf("anteprima della bozza: %v", pv)
	}
	bad := e.admin.doRaw("POST", ghAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/ordini-api",
		"definition": map[string]any{"format": "x", "gitops": map[string]any{"fields": map[string]any{}}}})
	bad.expect(t, http.StatusUnprocessableEntity)
	if !strings.Contains(bad.body, `"problems"`) {
		t.Fatalf("problemi della bozza: %s", bad.body)
	}
	if v := e.admin.do("POST", profiles+"/validate", map[string]any{"definition": draft}, http.StatusOK); v["valid"] != true {
		t.Fatalf("validazione: %v", v)
	}

	// Export e import (in un altro tenant: crea; di nuovo: nessuna nuova versione).
	_, hdr, raw := e.admin.get(profiles + "/" + profileID + "/export")
	if !strings.Contains(hdr.Get("Content-Disposition"), "attachment") || !bytes.Contains(raw, []byte(ghconn.ExportKind)) {
		t.Fatalf("export: %v %s", hdr, raw)
	}
	var doc any
	_ = json.Unmarshal(raw, &doc)
	other := newGHEnv(t)
	res := other.admin.do("POST", profiles+"/import", map[string]any{"document": doc}, http.StatusCreated)
	if res["created"] != true || res["version"] != 1.0 {
		t.Fatalf("import: %v", res)
	}
	res = other.admin.do("POST", profiles+"/import", map[string]any{"document": doc}, http.StatusOK)
	if res["created"] != false || res["version"] != 1.0 {
		t.Fatalf("import ripetuto: %v", res)
	}
	other.admin.do("POST", profiles+"/import", map[string]any{"document": map[string]any{"kind": "altro"}}, http.StatusUnprocessableEntity)
	// Un profilo usato non si cancella.
	e.admin.do("DELETE", profiles+"/"+profileID, nil, http.StatusConflict)

	// Isolamento tra tenant: l'altro amministratore non vede né tocca niente di qui.
	if items := other.admin.do("GET", ghAPI+"/integrations", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("integrazioni di un altro tenant visibili: %v", items)
	}
	other.admin.do("GET", ghAPI+"/integrations/"+id, nil, http.StatusNotFound)
	other.admin.do("GET", ghAPI+"/integrations/"+id+"/repositories", nil, http.StatusNotFound)
	other.admin.do("POST", ghAPI+"/integrations/"+id+"/sync", nil, http.StatusNotFound)
	other.admin.do("POST", ghAPI+"/integrations/"+id+"/preview", map[string]any{"repository": "esempio/ordini-api"}, http.StatusNotFound)
	other.admin.do("GET", profiles+"/"+profileID, nil, http.StatusNotFound)
	other.admin.do("PATCH", ghAPI+"/integrations/"+id, e.integrationInput(""), http.StatusNotFound)
	other.admin.do("DELETE", ghAPI+"/integrations/"+id, nil, http.StatusNotFound)
	// Il webhook di questo tenant, anche arrivato dall'altro server, resta di questo tenant.
	e.sync(id, false)
	for _, c := range e.ing.take() {
		if c.tenant != e.ten.TenantID {
			t.Fatalf("oggetto nel tenant sbagliato: %+v", c)
		}
	}
	otherID, _ := other.setup()
	other.sync(otherID, false)
	for _, c := range other.ing.take() {
		if c.tenant != other.ten.TenantID {
			t.Fatalf("oggetto dell'altro tenant nel tenant sbagliato: %+v", c)
		}
	}

	// Senza tenant.admin: niente configurazione.
	member := createPlainMember(t, e)
	member.do("GET", ghAPI+"/integrations", nil, http.StatusForbidden)
	member.do("GET", profiles, nil, http.StatusForbidden)
	member.do("POST", ghAPI+"/integrations/"+id+"/sync", nil, http.StatusForbidden)
}

// createPlainMember crea una persona senza ruoli di amministrazione.
func createPlainMember(t *testing.T, e *ghEnv) *user {
	t.Helper()
	res := e.admin.do("POST", "/api/v1/admin/members", map[string]any{
		"email": "persona@" + e.slug + ".test", "display_name": "Persona", "mode": "create", "role_ids": []string{},
	}, http.StatusCreated)
	token := strings.TrimPrefix(res["link"].(map[string]any)["path"].(string), "/password/")
	anonClient(t, e.base).do("POST", "/api/v1/auth/password-reset/"+token, map[string]any{"password": testPassword}, http.StatusNoContent)
	return login(t, e.base, "persona@"+e.slug+".test", testPassword)
}

// Con l'Ingestor vero del modulo devsecops: servizi, Release, deploy, SBOM e controlli
// arrivano nelle tabelle, e una seconda sincronizzazione completa non crea doppioni.
func TestGitHubConnectorWithRealIngestor(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	e := newGHEnv(t)
	eng := engine.New()
	req := requirements.Register(eng, approval.Register(eng))
	dso := devsecops.Register(eng, req)
	e.gh = ghconn.New(ghconn.Options{DB: d, Ingestor: dso, AllowPrivate: true, Timeout: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error { return nil }})
	a := ghconn.Actor{TenantID: e.ten.TenantID, AccountID: e.ten.AdminAccountID, MemberID: e.ten.AdminMemberID}
	for _, app := range []map[string]any{{"name": "Ordini", "code": "ORD"}, {"name": "Pagamenti", "code": "PAG"}} {
		e.admin.do("POST", "/api/v1/architecture/applications", app, http.StatusCreated)
	}
	pid, err := e.gh.CreateProfile(ctx, a, ghconn.ProfileInput{Preset: profile.PresetGitOpsArgo})
	if err != nil {
		t.Fatal(err)
	}
	in := e.integrationInput(pid)
	id, err := e.gh.Create(ctx, a, ghconn.Input{Name: "GHES", BaseURL: in["base_url"].(string), Owners: []string{"esempio"}, AuthMode: "app",
		AppID: e.fg.AppID, InstallationID: e.fg.InstallationID, PrivateKey: e.fg.PrivateKeyPEM(), ProfileID: pid,
		GitOpsRepositories: []string{"esempio/negozio-gitops"}, PrivateNetwork: true, CACertificate: e.caPEM})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.gh.Discover(ctx, a, id); err != nil {
		t.Fatal(err)
	}
	repos, _ := e.gh.Repositories(ctx, e.ten.TenantID, id)
	var ids []string
	for _, r := range repos {
		if r.Status == "suggested" {
			ids = append(ids, r.ID)
		}
	}
	if n, err := e.gh.AcceptSuggestions(ctx, a, id, ids); err != nil || n != 2 {
		t.Fatalf("conferme: %d %v", n, err)
	}
	count := func(q string) int {
		var n int
		if err := d.Owner.QueryRow(ctx, q+" where tenant_id = $1", e.ten.TenantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("select count(*) from services") != 2 {
		t.Fatal("servizi associati non registrati")
	}
	for i, full := range []bool{false, true} {
		if _, err := e.gh.SyncNow(ctx, a, id, full); err != nil {
			t.Fatal(err)
		}
		e.gh.WaitIdle()
		runs, _ := e.gh.Runs(ctx, e.ten.TenantID, id)
		if runs[0].Status != "ok" && runs[0].Status != "partial" || len(runs[0].Stats.Errors) > 0 {
			t.Fatalf("sincronizzazione %d: %+v", i, runs[0])
		}
		if r, s, dep, c := count("select count(*) from releases"), count("select count(*) from sboms"), count("select count(*) from deployments"),
			count("select count(*) from check_runs"); r != 3 || s != 2 || dep < 3 || c != 2 {
			t.Fatalf("sincronizzazione %d: %d Release, %d SBOM, %d deploy, %d controlli", i, r, s, dep, c)
		}
	}
	var app string
	if err := d.Owner.QueryRow(ctx, `select coalesce(declared_application, '') from services where tenant_id = $1 and name = 'ordini-api'`,
		e.ten.TenantID).Scan(&app); err != nil || app != "ORD" {
		t.Fatalf("applicazione del servizio: %q %v", app, err)
	}
}
