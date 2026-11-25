package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	gtconn "regia/internal/modules/devsecops/connectors/gitea"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Connettore Gitea, contro un Gitea finto in httptest (http, rete interna): configurazione
// con RLS, scoperta paginata, associazione, sincronizzazione idempotente e webhook firmato.

const gtToken = "gitea-token-di-prova"
const gtAPI = "/api/v1/devsecops/gitea"

func fakeGiteaServer() *httptest.Server {
	now := time.Now().UTC().Format(time.RFC3339)
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "token "+gtToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/version", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"version":"1.22.3"}`)) }))
	mux.HandleFunc("GET /api/v1/user", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":1,"login":"regia"}`)) }))
	mux.HandleFunc("GET /api/v1/orgs/acme", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":1,"username":"acme"}`)) }))
	mux.HandleFunc("GET /api/v1/orgs/acme/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":11,"name":"ordini","full_name":"acme/ordini","default_branch":"main","html_url":"http://gitea/acme/ordini"},
			{"id":12,"name":"strumenti","full_name":"acme/strumenti","default_branch":"main","html_url":"http://gitea/acme/strumenti"}]`))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/releases", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`[{"id":5,"tag_name":"v1.0.0","name":"1.0.0","created_at":%q,"published_at":%q,"author":{"login":"ci"},"assets":[]}]`, now, now)))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/tags/", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"commit":{"sha":"` + strings.Repeat("c", 40) + `"}}`))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/commits/main/status", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"state":"success","sha":%q,"statuses":[{"id":1,"status":"success","context":"ci / test","updated_at":%q,"created_at":%q}]}`,
			strings.Repeat("d", 40), now, now)))
	}))
	return httptest.NewServer(mux)
}

func newGTEnv(t *testing.T) (e *user, svc *gtconn.Service, ing *recIngestor, fake *httptest.Server, base string, ten tenant.Created) {
	d := needDB(t)
	slug := fmt.Sprintf("gt-%s", strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	ten, err := tenant.Create(context.Background(), d, tenant.CreateInput{
		Slug: slug, Name: "Gitea " + slug, AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake = fakeGiteaServer()
	t.Cleanup(fake.Close)
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	box := secretbox.Random()
	as := auth.NewService(d, time.Hour)
	as.SetSecretBox(box)
	ing = &recIngestor{}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc = gtconn.New(gtconn.Options{DB: d, Box: box, Ingestor: ing, AllowPrivate: true, Log: log, Timeout: 5 * time.Second})
	srv := api.New(api.Options{
		DB: d, Auth: as, Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Log: log,
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, Gitea: svc,
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(svc.WaitIdle)
	return login(t, hs.URL, "admin@"+slug+".test", testPassword), svc, ing, fake, hs.URL, ten
}

func TestGiteaConnector(t *testing.T) {
	admin, svc, ing, fake, base, ten := newGTEnv(t)
	for _, a := range []map[string]any{{"name": "Ordini", "code": "ORD"}} {
		admin.do("POST", "/api/v1/architecture/applications", a, http.StatusCreated)
	}
	in := map[string]any{"name": "Gitea locale", "base_url": fake.URL, "owners": []string{"acme"}, "token": gtToken,
		"webhook_secret": "segreto-del-webhook-di-prova", "private_network": true}

	// Senza «Rete interna» un Gitea su un indirizzo locale si rifiuta; il token è obbligatorio;
	// servono da 1 a 20 proprietari.
	bad := map[string]any{}
	for k, v := range in {
		bad[k] = v
	}
	bad["private_network"] = false
	admin.do("POST", gtAPI+"/integrations", bad, http.StatusUnprocessableEntity)
	bad["private_network"], bad["token"] = true, ""
	admin.do("POST", gtAPI+"/integrations", bad, http.StatusUnprocessableEntity)
	bad["token"], bad["owners"] = gtToken, make([]string, 21)
	for i := range bad["owners"].([]string) {
		bad["owners"].([]string)[i] = fmt.Sprintf("o%d", i)
	}
	admin.do("POST", gtAPI+"/integrations", bad, http.StatusUnprocessableEntity)

	id := admin.do("POST", gtAPI+"/integrations", in, http.StatusCreated)["id"].(string)
	raw, _ := json.Marshal(admin.do("GET", gtAPI+"/integrations/"+id, nil, http.StatusOK))
	if strings.Contains(string(raw), gtToken) || strings.Contains(string(raw), "segreto-del-webhook") || !strings.Contains(string(raw), `"has_token":true`) {
		t.Fatalf("segreti nella risposta: %s", raw)
	}
	tr := admin.do("POST", gtAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != true || !strings.Contains(tr["message"].(string), "Connessione riuscita con Gitea 1.22.3") {
		t.Fatalf("prova di connessione: %v", tr)
	}

	res := admin.do("POST", gtAPI+"/integrations/"+id+"/discover", nil, http.StatusOK)
	if res["found"].(float64) != 2 {
		t.Fatalf("scoperta: %v", res)
	}
	repos := map[string]map[string]any{}
	for _, it := range admin.do("GET", gtAPI+"/integrations/"+id+"/repos", nil, http.StatusOK)["items"].([]any) {
		r := it.(map[string]any)
		repos[r["full_name"].(string)] = r
	}
	if repos["acme/ordini"]["status"] != "suggested" || repos["acme/strumenti"]["status"] != "none" {
		t.Fatalf("proposte: %v", repos)
	}
	admin.do("POST", gtAPI+"/integrations/"+id+"/repos/accept", map[string]any{"ids": []string{repos["acme/ordini"]["id"].(string)}}, http.StatusOK)
	ing.take()

	sync := func() map[string]any {
		admin.do("POST", gtAPI+"/integrations/"+id+"/sync", nil, http.StatusAccepted)
		svc.WaitIdle()
		return admin.do("GET", gtAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)
	}
	run := sync()
	calls := ing.take()
	if run["status"] != "ok" || countKind(calls, "release") != 1 || countKind(calls, "check_run") != 1 {
		t.Fatalf("prima sincronizzazione: %v, %+v", run["status"], calls)
	}
	// Idempotente: la seconda non consegna niente di nuovo.
	run = sync()
	if calls = ing.take(); run["status"] != "ok" || countKind(calls, "release") != 0 || countKind(calls, "check_run") != 0 {
		t.Fatalf("seconda sincronizzazione: %v, %+v", run["status"], calls)
	}

	// Webhook: firma errata o assente 401, valida 202, la stessa consegna due volte una sola.
	post := func(sig, delivery string) int {
		body, _ := json.Marshal(map[string]any{"ref": "refs/tags/v1.1.0", "repository": map[string]any{"id": 11, "full_name": "acme/ordini", "default_branch": "main"}})
		req, _ := http.NewRequest("POST", base+gtAPI+"/webhook/"+id, bytes.NewReader(body))
		req.Header.Set("X-Gitea-Event", "push")
		req.Header.Set("X-Gitea-Delivery", delivery)
		if sig == "valida" {
			sig = gtconn.Sign("segreto-del-webhook-di-prova", body)
		}
		if sig != "" {
			req.Header.Set("X-Gitea-Signature", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		svc.WaitIdle()
		return resp.StatusCode
	}
	if c := post("", "d1"); c != http.StatusUnauthorized {
		t.Fatalf("senza firma: %d", c)
	}
	if c := post("00ff", "d2"); c != http.StatusUnauthorized {
		t.Fatalf("firma errata: %d", c)
	}
	if c := post("valida", "d3"); c != http.StatusAccepted {
		t.Fatalf("firma valida: %d", c)
	}
	if c := post("valida", "d3"); c != http.StatusOK {
		t.Fatalf("consegna ripetuta: %d", c)
	}
	_ = ten
}
