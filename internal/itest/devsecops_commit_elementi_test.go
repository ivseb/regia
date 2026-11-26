package itest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
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
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Commit → elemento: il connettore Gitea (finto) legge i commit del branch predefinito e le PR
// unite, la chiave nel messaggio lega il commit all'elemento, il cursore avanza solo a consegna
// riuscita, la rilettura è idempotente, il lead time DORA parte dal primo commit e la scheda
// dell'elemento ha i suoi commit con il rilascio che li contiene.

type fakeCommit struct {
	sha, msg, author string
	at               time.Time
}

type fakeCommitsGitea struct {
	mu          sync.Mutex
	commits     []fakeCommit // dal più recente
	failCommits bool
	sinces      []string
	prMergedAt  time.Time
}

func (f *fakeCommitsGitea) server() *httptest.Server {
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
		_, _ = w.Write([]byte(`[{"id":11,"name":"ordini","full_name":"acme/ordini","default_branch":"main","html_url":"http://gitea/acme/ordini"}]`))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/releases", auth(empty))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/commits/main/status", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"pending","sha":"` + strings.Repeat("e", 40) + `","statuses":[]}`))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/commits", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Query().Get("sha") != "main" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.sinces = append(f.sinces, r.URL.Query().Get("since"))
		if f.failCommits {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		var all []map[string]any
		for _, c := range f.commits {
			if c.at.Before(since) {
				continue
			}
			d := c.at.Format(time.RFC3339)
			all = append(all, map[string]any{"sha": c.sha, "html_url": "http://gitea/acme/ordini/commit/" + c.sha,
				"commit": map[string]any{"message": c.msg, "author": map[string]any{"name": c.author, "email": "x@y.it", "date": d}, "committer": map[string]any{"date": d}}})
		}
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		l, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		from := min((p-1)*l, len(all))
		_ = json.NewEncoder(w).Encode(all[from:min(from+l, len(all))])
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/ordini/pulls", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			empty(w, r)
			return
		}
		m := f.prMergedAt.Format(time.RFC3339)
		_, _ = w.Write([]byte(`[{"number":7,"title":"` + f.prTitle() + `","html_url":"http://gitea/acme/ordini/pulls/7","merged":true,"merged_at":"` + m +
			`","merge_commit_sha":"` + strings.Repeat("7", 40) + `","updated_at":"` + m + `","head":{"ref":"fix/totale"},"user":{"login":"ana"}},
			{"number":6,"title":"` + f.prTitle() + ` ma chiusa","html_url":"x","merged":false,"merged_at":null,"merge_commit_sha":"","updated_at":"` + m + `","head":{"ref":"x"},"user":{"login":"ana"}}]`))
	}))
	return httptest.NewServer(mux)
}

var prKey string // chiave dell'elemento citato dalla PR (impostata dal test)

func (f *fakeCommitsGitea) prTitle() string { return prKey + " corregge il totale" }

func TestCommitElementi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	slug, slugB := "cm-"+stamp, "cmb-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, risk.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Commit", AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	rk := risk.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	box := secretbox.Random()
	as := auth.NewService(d, time.Hour)
	as.SetSecretBox(box)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := gtconn.New(gtconn.Options{DB: d, Box: box, Ingestor: dso, AllowPrivate: true, Log: log, Timeout: 5 * time.Second})
	srv := api.New(api.Options{
		DB: d, Auth: as, Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, Devsecops: dso, Risk: rk, Log: log,
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, Gitea: svc, FilesDir: t.TempDir(),
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(svc.WaitIdle)
	admin := login(t, hs.URL, "admin@"+slug+".test", testPassword)
	adminB := login(t, hs.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// Applicazione, due incidenti (gli «elementi» citati nei commit) e un terzo mai citato.
	app := admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Ordini", "code": "ORD"}, http.StatusCreated)
	mk := func(title string) string {
		return admin.do("POST", "/api/v1/incidents", map[string]any{"title": title, "detected_at": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
			"impacted": []string{app["key"].(string)}}, http.StatusCreated)["key"].(string)
	}
	k1, k2, k3 := mk("Totale errato"), mk("Spedizione lenta"), mk("Mai citato")
	prKey = k2

	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour
	fake := &fakeCommitsGitea{prMergedAt: now.Add(-5 * day)}
	fake.commits = []fakeCommit{ // dal più recente
		{"d" + strings.Repeat("0", 39), "aggiorna le dipendenze (CVE-2024-1234, SHA-256)", "bot", now.Add(-4 * day)},
		{"c" + strings.Repeat("0", 39), k1 + " completa il totale\n\ncorpo del messaggio " + k2, "Ana Rossi", now.Add(-8 * day)},
		{"b" + strings.Repeat("0", 39), "senza chiave", "Bruno", now.Add(-9 * day)},
		{"a" + strings.Repeat("0", 39), "fix(" + k1 + "): primo passo", "Ana Rossi", now.Add(-10 * day)},
	}
	fs := fake.server()
	t.Cleanup(fs.Close)

	id := admin.do("POST", gtAPI+"/integrations", map[string]any{"name": "Gitea locale", "base_url": fs.URL, "owners": []string{"acme"}, "token": gtToken,
		"webhook_secret": "segreto-del-webhook-di-prova", "private_network": true}, http.StatusCreated)["id"].(string)
	admin.do("POST", gtAPI+"/integrations/"+id+"/discover", nil, http.StatusOK)
	var repoID string
	for _, it := range list(admin.do("GET", gtAPI+"/integrations/"+id+"/repos", nil, http.StatusOK)["items"]) {
		repoID = obj(it)["id"].(string)
	}
	admin.do("POST", gtAPI+"/integrations/"+id+"/repos/accept", map[string]any{"ids": []string{repoID}}, http.StatusOK)
	var svcName string
	if err := d.Owner.QueryRow(ctx, "select name from services where tenant_id = $1", ten.TenantID).Scan(&svcName); err != nil {
		t.Fatal(err)
	}

	// Un rilascio in produzione registrato dalla pipeline, 3 giorni fa (dopo tutti i commit).
	tok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	deployAt := now.Add(-3 * day)
	bearerDo(t, hs.URL, tok, "POST", "/api/v1/devsecops/deployments", map[string]any{"format": "regia.devsecops/v1",
		"service": map[string]any{"name": svcName, "repository": "acme/ordini", "provider": "gitea"}, "environment": "prod", "version": "1.0.0",
		"status": "succeeded", "at": deployAt.Format(time.RFC3339), "source": "gitops", "external_ref": "argo/ordini/1"}, http.StatusAccepted)

	if d0 := admin.do("GET", "/api/v1/devsecops/dora?period=month", nil, http.StatusOK); d0["computed"] != false {
		t.Fatalf("prima della sincronizzazione le metriche non esistono: %v", d0["computed"])
	}

	cursorOf := func() map[string]any {
		var raw []byte
		if err := d.Owner.QueryRow(ctx, "select cursors from connector_repositories where tenant_id = $1 and id = $2", ten.TenantID, repoID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var c map[string]any
		_ = json.Unmarshal(raw, &c)
		return obj(c["commits"])
	}
	countLinks := func() int {
		var n int
		if err := d.Owner.QueryRow(ctx, "select count(*) from element_commits where tenant_id = $1", ten.TenantID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sync := func(q string) map[string]any {
		admin.do("POST", gtAPI+"/integrations/"+id+"/sync"+q, nil, http.StatusAccepted)
		svc.WaitIdle()
		return obj(list(admin.do("GET", gtAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"])[0])
	}

	// --- Prima sincronizzazione: k1 ha 2 commit, k2 un commit (citato nel corpo) e la PR unita;
	// k3 niente; i commit senza chiave o con chiavi inesistenti (CVE, SHA) non legano nulla.
	run := sync("")
	if run["status"] != "ok" {
		t.Fatalf("prima sincronizzazione: %v %v", run["status"], run["error"])
	}
	if n := obj(run["stats"])["commits"]; n != float64(4) {
		t.Fatalf("collegamenti creati: %v (statistiche %v)", n, run["stats"])
	}
	if got := countLinks(); got != 4 {
		t.Fatalf("righe in element_commits: %d", got)
	}
	cur := cursorOf()
	wantAt := now.Add(-4 * day).Format(time.RFC3339)
	if cur["at"] != wantAt || cur["sha"] != "d"+strings.Repeat("0", 39) {
		t.Fatalf("cursore dei commit: %v (atteso at %s)", cur, wantAt)
	}
	if first := fake.sinces[0]; first >= now.Add(-89*day).Format(time.RFC3339) || first <= now.Add(-91*day).Format(time.RFC3339) {
		t.Fatalf("la prima lettura parte da 90 giorni fa: since=%s", first)
	}
	// Il primo ricalcolo DORA è già stato fatto dalla sincronizzazione.
	if d1 := admin.do("GET", "/api/v1/devsecops/dora?period=month", nil, http.StatusOK); d1["computed"] != true {
		t.Fatalf("metriche non ricalcolate dopo la prima sincronizzazione: %v", d1["computed"])
	}

	// --- Rilettura: il cursore è la data dell'ultimo commit, niente righe nuove.
	run = sync("")
	if run["status"] != "ok" || countLinks() != 4 || obj(run["stats"])["commits"] != float64(0) {
		t.Fatalf("rilettura: %v, %d righe, %v", run["status"], countLinks(), run["stats"])
	}
	if last := fake.sinces[len(fake.sinces)-1]; last != wantAt {
		t.Fatalf("la rilettura parte dal cursore: since=%s, atteso %s", last, wantAt)
	}
	// Completa: ignora il cursore (since torna a 90 giorni fa) e resta idempotente.
	run = sync("?full=true")
	if last := fake.sinces[len(fake.sinces)-1]; last >= now.Add(-89*day).Format(time.RFC3339) || run["status"] != "ok" || countLinks() != 4 {
		t.Fatalf("sincronizzazione completa: since=%s, %v, %d righe", last, run["status"], countLinks())
	}

	// --- Un errore di Gitea sui commit non fa avanzare il cursore né perdere il resto.
	fake.mu.Lock()
	fake.failCommits = true
	fake.commits = append([]fakeCommit{{"e" + strings.Repeat("0", 39), k1 + " ultimo ritocco", "Ana Rossi", now.Add(-1 * day)}}, fake.commits...)
	fake.mu.Unlock()
	run = sync("")
	if ws, _ := obj(run["stats"])["warnings"].([]any); len(ws) == 0 || cursorOf()["at"] != wantAt || countLinks() != 4 {
		t.Fatalf("con l'errore: avvisi %v, cursore %v, %d righe", obj(run["stats"])["warnings"], cursorOf(), countLinks())
	}
	fake.mu.Lock()
	fake.failCommits = false
	fake.mu.Unlock()
	run = sync("")
	if run["status"] != "ok" || countLinks() != 5 || cursorOf()["at"] != now.Add(-1*day).Format(time.RFC3339) {
		t.Fatalf("dopo l'errore: %v, %d righe, cursore %v", run["status"], countLinks(), cursorOf())
	}

	// --- La scheda dell'elemento: k1 ha 3 commit, rilasciato in 1.0.0; il lead time dal primo commit.
	c1 := admin.do("GET", "/api/v1/elements/"+k1+"/commits", nil, http.StatusOK)
	cs := list(c1["commits"])
	if len(cs) != 3 {
		t.Fatalf("commit di %s: %v", k1, c1)
	}
	top := obj(cs[0])
	if top["title"] != k1+" ultimo ritocco" || top["author"] != "Ana Rossi" || top["source"] != "commit" || top["repository"] != "acme/ordini" {
		t.Fatalf("commit più recente: %v", top)
	}
	if strings.Contains(fmt.Sprint(c1), "x@y.it") {
		t.Fatalf("l'email non deve uscire: %v", c1)
	}
	// L'ultimo commit di k1 è di 1 giorno fa, dopo il rilascio (3 giorni fa): non ancora rilasciato.
	if c1["release"] != nil {
		t.Fatalf("k1 ha un commit successivo al rilascio: %v", c1["release"])
	}
	c2 := admin.do("GET", "/api/v1/elements/"+k2+"/commits", nil, http.StatusOK)
	cs2 := list(c2["commits"])
	if len(cs2) != 2 {
		t.Fatalf("commit di %s: %v", k2, c2)
	}
	var pr map[string]any
	for _, c := range cs2 {
		if obj(c)["source"] == "pull_request" {
			pr = obj(c)
		}
	}
	if pr == nil || pr["pr_number"] != float64(7) || pr["author"] != "ana" {
		t.Fatalf("PR unita di %s: %v", k2, cs2)
	}
	// k2: ultimo commit 5 giorni fa (PR unita), rilascio 3 giorni fa; primo commit 8 giorni fa → 5 giorni.
	rel := obj(c2["release"])
	if rel == nil || rel["version"] != "1.0.0" || math.Abs(rel["lead_days"].(float64)-5) > 0.01 {
		t.Fatalf("rilascio di %s: %v", k2, c2["release"])
	}
	if c3 := admin.do("GET", "/api/v1/elements/"+k3+"/commits", nil, http.StatusOK); len(list(c3["commits"])) != 0 || c3["release"] != nil {
		t.Fatalf("elemento senza commit: %v", c3)
	}
	// Un altro tenant non vede l'elemento, e quindi nemmeno i suoi commit.
	adminB.do("GET", "/api/v1/elements/"+k1+"/commits", nil, http.StatusNotFound)
	admin.do("GET", "/api/v1/elements/ZZZ-999/commits", nil, http.StatusNotFound)

	// --- DORA: il deploy ha come inizio il primo commit tra nessun deploy precedente e il deploy
	// (10 giorni fa → 7 giorni di lead time), fonte «commit».
	if rep := admin.do("POST", "/api/v1/devsecops/dora/recompute", nil, http.StatusOK); rep["deploys"] == nil {
		t.Fatalf("ricalcolo: %v", rep)
	}
	dd := admin.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=tenant&at="+deployAt.Format(time.RFC3339), nil, http.StatusOK)
	deps := list(dd["deploys"])
	if len(deps) != 1 {
		t.Fatalf("deploy della settimana: %v", dd)
	}
	dep := obj(deps[0])
	if dep["lead_source"] != "commit" || math.Abs(dep["lead_days"].(float64)-7) > 0.01 {
		t.Fatalf("lead time dal primo commit: %v", dep)
	}
	// Con la fonte fissata sui controlli non c'è nessun controllo: il candidato commit non vale.
	cfg := admin.do("GET", "/api/v1/devsecops/dora/settings", nil, http.StatusOK)
	cfg["lead_time_start"] = "check_run"
	admin.do("PUT", "/api/v1/devsecops/dora/settings", cfg, http.StatusOK)
	dd = admin.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=tenant&at="+deployAt.Format(time.RFC3339), nil, http.StatusOK)
	if dep = obj(list(dd["deploys"])[0]); dep["lead_source"] != nil && dep["lead_source"] != "" {
		t.Fatalf("con check_run: %v", dep)
	}
	cfg["lead_time_start"] = "commit"
	admin.do("PUT", "/api/v1/devsecops/dora/settings", cfg, http.StatusOK)
	dd = admin.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=tenant&at="+deployAt.Format(time.RFC3339), nil, http.StatusOK)
	if dep = obj(list(dd["deploys"])[0]); dep["lead_source"] != "commit" {
		t.Fatalf("con commit: %v", dep)
	}
}
