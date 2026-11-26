package itest

import (
	"context"
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
	"regia/internal/modules/devsecops"
	gtconn "regia/internal/modules/devsecops/connectors/gitea"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Schema dei tag (pilota home lab, difetto 5): un repository Gitea pubblica due tag per versione
// (v1.1.0 e chart-0.2.0). La scoperta chiede quale è il rilascio vero, la risposta diventa il
// tag_filter di un profilo nuovo collegato all'integrazione, i rilasci già letti dello schema
// scartato si tolgono e la domanda non torna.
func TestSchemaTagRilasci(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := "st-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Schema tag", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision}})
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	rq := requirements.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	box := secretbox.Random()
	as := auth.NewService(d, time.Hour)
	as.SetSecretBox(box)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := gtconn.New(gtconn.Options{DB: d, Box: box, Ingestor: dso, AllowPrivate: true, Log: log, Timeout: 5 * time.Second})
	srv := api.New(api.Options{
		DB: d, Auth: as, Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng), Requirements: rq, Devsecops: dso, Log: log,
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, Gitea: svc, FilesDir: t.TempDir(),
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(svc.WaitIdle)
	admin := login(t, hs.URL, "admin@"+slug+".test", testPassword)

	// Gitea finto: acme/proxy con due schemi di tag, acme/sito con uno solo.
	base := time.Now().UTC().Add(-72 * time.Hour)
	rel := func(repo string, id int, tag string, h int) string {
		at := base.Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
		return fmt.Sprintf(`{"id":%d,"tag_name":%q,"name":%q,"html_url":"http://gitea/%s/releases/tag/%s","created_at":%q,"published_at":%q,"author":{"login":"ci"},"assets":[]}`,
			id, tag, tag, repo, tag, at, at)
	}
	releases := map[string]string{
		"acme/proxy": "[" + strings.Join([]string{rel("acme/proxy", 4, "v1.1.0", 30), rel("acme/proxy", 3, "chart-0.2.0", 29), rel("acme/proxy", 2, "v1.0.0", 10), rel("acme/proxy", 1, "chart-0.1.0", 9)}, ",") + "]",
		"acme/sito":  "[" + strings.Join([]string{rel("acme/sito", 6, "v2.0.0", 20), rel("acme/sito", 5, "v1.9.0", 5)}, ",") + "]",
	}
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
	empty := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }
	mux.HandleFunc("GET /api/v1/version", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"version":"1.22.3"}`)) }))
	mux.HandleFunc("GET /api/v1/user", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":1,"login":"regia"}`)) }))
	mux.HandleFunc("GET /api/v1/orgs/acme", auth(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":1,"username":"acme"}`)) }))
	mux.HandleFunc("GET /api/v1/orgs/acme/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			empty(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":21,"name":"proxy","full_name":"acme/proxy","default_branch":"main","html_url":"http://gitea/acme/proxy"},
			{"id":22,"name":"sito","full_name":"acme/sito","default_branch":"main","html_url":"http://gitea/acme/sito"}]`))
	}))
	for repo, body := range releases {
		mux.HandleFunc("GET /api/v1/repos/"+repo+"/releases", auth(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") != "1" {
				empty(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		mux.HandleFunc("GET /api/v1/repos/"+repo+"/commits", auth(empty))
		mux.HandleFunc("GET /api/v1/repos/"+repo+"/pulls", auth(empty))
		mux.HandleFunc("GET /api/v1/repos/"+repo+"/commits/main/status", auth(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"state":"pending","sha":"` + strings.Repeat("e", 40) + `","statuses":[]}`))
		}))
	}
	fake := httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Proxy", "code": "PRX"}, http.StatusCreated)
	id := admin.do("POST", gtAPI+"/integrations", map[string]any{"name": "Gitea di casa", "base_url": fake.URL, "owners": []string{"acme"}, "token": gtToken,
		"private_network": true}, http.StatusCreated)["id"].(string)

	// La scoperta chiede solo per acme/proxy, con i due schemi e i loro esempi.
	res := admin.do("POST", gtAPI+"/integrations/"+id+"/discover", nil, http.StatusOK)
	qs, _ := res["tag_questions"].([]any)
	if len(qs) != 1 {
		t.Fatalf("domande sugli schemi dei tag: %v", res["tag_questions"])
	}
	q := qs[0].(map[string]any)
	schemes := q["schemes"].([]any)
	if q["full_name"] != "acme/proxy" || len(schemes) != 2 {
		t.Fatalf("domanda: %v", q)
	}
	if s0 := schemes[0].(map[string]any); s0["pattern"] != "chart-[0-9]*" || s0["count"].(float64) != 2 {
		t.Fatalf("primo schema (a parità, in ordine di modello): %v", s0)
	}
	repoID := q["repository_id"].(string)

	// Associa il repository e sincronizza: arrivano tutti e quattro i rilasci.
	admin.do("POST", gtAPI+"/integrations/"+id+"/repositories/accept", map[string]any{"ids": []string{repoID}}, http.StatusOK)
	admin.do("POST", gtAPI+"/integrations/"+id+"/sync", nil, http.StatusAccepted)
	svc.WaitIdle()
	count := func() int {
		var n int
		if err := d.Owner.QueryRow(ctx, "select count(*) from releases where tenant_id = $1", ten.TenantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 4 {
		t.Fatalf("rilasci dopo la prima sincronizzazione: %d", n)
	}

	// Risposte non valide.
	admin.do("PUT", gtAPI+"/integrations/"+id+"/repositories/"+repoID+"/tag-filter", map[string]any{"pattern": "["}, http.StatusUnprocessableEntity)
	admin.do("PUT", gtAPI+"/integrations/"+id+"/repositories/00000000-0000-0000-0000-000000000000/tag-filter", map[string]any{"pattern": "v[0-9]*"}, http.StatusNotFound)

	// La scelta: un profilo nuovo collegato all'integrazione, i rilasci chart tolti.
	out := admin.do("PUT", gtAPI+"/integrations/"+id+"/repositories/"+repoID+"/tag-filter", map[string]any{"pattern": "v[0-9]*"}, http.StatusOK)
	if out["profile_created"] != true || out["pruned"].(float64) != 2 || out["profile_version"].(float64) != 1 {
		t.Fatalf("scelta dello schema: %v", out)
	}
	if n := count(); n != 2 {
		t.Fatalf("rilasci dopo la scelta: %d", n)
	}
	it := admin.do("GET", gtAPI+"/integrations/"+id, nil, http.StatusOK)
	if cfg, _ := it["config"].(map[string]any); cfg["profile_id"] != out["profile_id"] {
		t.Fatalf("il profilo nuovo non è collegato all'integrazione: %v", it)
	}
	var def string
	if err := d.Owner.QueryRow(ctx, `select v.definition::text from pipeline_profile_versions v where v.profile_id = $1 and v.version = 1`, out["profile_id"]).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, `"tag_filters": {"acme/proxy": "v[0-9]*"}`) {
		t.Fatalf("definizione del profilo: %s", def)
	}

	// La sincronizzazione completa non li rilegge e la domanda non torna.
	admin.do("POST", gtAPI+"/integrations/"+id+"/sync", map[string]any{"full": true}, http.StatusAccepted)
	svc.WaitIdle()
	if n := count(); n != 2 {
		t.Fatalf("rilasci dopo la seconda sincronizzazione: %d", n)
	}
	res = admin.do("POST", gtAPI+"/integrations/"+id+"/discover", nil, http.StatusOK)
	if qs, _ := res["tag_questions"].([]any); len(qs) != 0 {
		t.Fatalf("la domanda è tornata: %v", qs)
	}

	// Cambiare idea (tutti i tag sono rilasci) è una nuova versione dello stesso profilo.
	out2 := admin.do("PUT", gtAPI+"/integrations/"+id+"/repositories/"+repoID+"/tag-filter", map[string]any{"pattern": ""}, http.StatusOK)
	if out2["profile_created"] != false || out2["profile_id"] != out["profile_id"] || out2["profile_version"].(float64) != 2 || out2["pruned"].(float64) != 0 {
		t.Fatalf("seconda scelta: %v", out2)
	}
}
