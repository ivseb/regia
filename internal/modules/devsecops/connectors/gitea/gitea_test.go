package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

const testToken = "tok-segreto-di-prova"

func TestNormalizeBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://gitea.azienda.it":        "https://gitea.azienda.it/api/v1",
		"gitea.azienda.it/":               "https://gitea.azienda.it/api/v1",
		"https://azienda.it/gitea":        "https://azienda.it/gitea/api/v1",
		"https://gitea.azienda.it/api/v1": "https://gitea.azienda.it/api/v1",
	}
	for in, want := range ok {
		got, err := giteaapi.NormalizeBaseURL(in, ghapi.Public)
		if err != nil || got != want {
			t.Errorf("%q → %q, %v (atteso %q)", in, got, err, want)
		}
	}
	bad := []string{"", "http://gitea.azienda.it", "https://127.0.0.1", "https://gitea.local", "https://u:p@gitea.azienda.it", "ftp://x.it", "https://x.it/?a=1", "https://intranet"}
	for _, in := range bad {
		if got, err := giteaapi.NormalizeBaseURL(in, ghapi.Public); err == nil {
			t.Errorf("%q doveva essere rifiutato, dato %q", in, got)
		}
	}
	// Rete interna: http e indirizzi privati sì, link-local (metadati del cloud) mai.
	if got, err := giteaapi.NormalizeBaseURL("http://192.168.1.20:3000", ghapi.Private); err != nil || got != "http://192.168.1.20:3000/api/v1" {
		t.Errorf("rete interna: %q, %v", got, err)
	}
	if _, err := giteaapi.NormalizeBaseURL("http://169.254.169.254", ghapi.Private); err == nil {
		t.Error("link-local ammesso")
	}
	if _, err := giteaapi.NormalizeBaseURL("http://127.0.0.1:3000", ghapi.Any); err != nil {
		t.Errorf("modalità sviluppo: %v", err)
	}
}

func TestVerifySignature(t *testing.T) {
	secret, body := "segreto-lungo-abcdefgh", []byte(`{"ref":"refs/tags/v1"}`)
	sig := Sign(secret, body)
	if !VerifySignature(secret, body, sig) || !VerifySignature(secret, body, "sha256="+strings.ToUpper(sig)) {
		t.Error("firma valida rifiutata")
	}
	for _, h := range []string{"", "zz", Sign("altro-segreto-xxxxxxx", body), Sign(secret, []byte("altro corpo"))} {
		if VerifySignature(secret, body, h) {
			t.Errorf("firma %q accettata", h)
		}
	}
	if VerifySignature("", body, sig) {
		t.Error("senza segreto non si accetta niente")
	}
}

// fakeGitea è un Gitea finto: un'organizzazione con 120 repository (tre pagine da 50),
// release e stati del commit.
type fakeGitea struct {
	t        *testing.T
	requests atomic.Int32
	releases []giteaapi.Release
	status   giteaapi.Combined
	badToken bool
}

func (f *fakeGitea) handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.requests.Add(1)
			if r.Header.Get("Authorization") != "token "+testToken {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"token is required"}`))
				return
			}
			h(w, r)
		}
	}
	page := func(r *http.Request, total int) (from, to int) {
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		l, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if p < 1 || l < 1 || l > 50 {
			f.t.Errorf("paginazione non valida: page=%d limit=%d", p, l)
		}
		from = (p - 1) * l
		to = min(from+l, total)
		if from > total {
			from = total
		}
		return
	}
	mux.HandleFunc("GET /api/v1/version", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"1.22.3"}`))
	}))
	mux.HandleFunc("GET /api/v1/orgs/acme", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"username":"acme"}`))
	}))
	mux.HandleFunc("GET /api/v1/orgs/mario", auth(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	mux.HandleFunc("GET /api/v1/users/mario", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":2,"login":"mario"}`))
	}))
	mux.HandleFunc("GET /api/v1/users/mario/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":900,"name":"giochi","full_name":"mario/giochi","default_branch":"main","html_url":"http://x/mario/giochi"}]`))
	}))
	mux.HandleFunc("GET /api/v1/orgs/acme/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		from, to := page(r, 120)
		var out []giteaapi.Repo
		for i := from; i < to; i++ {
			out = append(out, giteaapi.Repo{ID: int64(100 + i), Name: fmt.Sprintf("r%03d", i), FullName: fmt.Sprintf("acme/r%03d", i),
				DefaultBranch: "main", Fork: i == 7})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/web/releases", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("draft") != "false" {
			f.t.Error("le bozze vanno escluse")
		}
		from, to := page(r, len(f.releases))
		_ = json.NewEncoder(w).Encode(f.releases[from:to])
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/web/tags/", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"x","commit":{"sha":"` + strings.Repeat("a", 40) + `"}}`))
	}))
	mux.HandleFunc("GET /api/v1/repos/acme/web/commits/main/status", auth(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(f.status)
	}))
	return mux
}

func newTestClient(t *testing.T, f *fakeGitea, token string) (*giteaapi.Client, *Service) {
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	s := New(Options{Dev: true, Timeout: 5 * time.Second})
	cfg := Config{BaseURL: srv.URL + "/api/v1"}
	cl, err := s.client(cfg, secrets{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return cl, s
}

func TestOwnerReposPaginated(t *testing.T) {
	f := &fakeGitea{t: t}
	cl, _ := newTestClient(t, f, testToken)
	var names []string
	if err := cl.OwnerRepos(context.Background(), "acme", func(r giteaapi.Repo) error { names = append(names, r.FullName); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(names) != 120 || names[0] != "acme/r000" || names[119] != "acme/r119" {
		t.Fatalf("repository letti: %d (%v…)", len(names), names[:1])
	}
	// Un proprietario che non è un'organizzazione si legge come utente.
	names = nil
	if err := cl.OwnerRepos(context.Background(), "mario", func(r giteaapi.Repo) error { names = append(names, r.FullName); return nil }); err != nil || len(names) != 1 {
		t.Fatalf("utente: %v, %v", names, err)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	f := &fakeGitea{t: t}
	cl, _ := newTestClient(t, f, "token-sbagliato-0123456789")
	_, err := cl.Version(context.Background())
	if err == nil {
		t.Fatal("un token sbagliato doveva fallire")
	}
	if !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "token-sbagliato") {
		t.Fatalf("errore: %v", err)
	}
	if _, err := giteaapi.New(giteaapi.Options{BaseURL: "https://x.it/api/v1", Token: " ", HTTP: http.DefaultClient}); err == nil {
		t.Error("senza token non si crea il client")
	}
}

func TestSyncRepoIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	f := &fakeGitea{t: t, releases: []giteaapi.Release{
		{ID: 2, TagName: "v1.1.0", CreatedAt: now, PublishedAt: &now},
		{ID: 1, TagName: "v1.0.0", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: 3, TagName: "v1.2.0-rc1", Prerelease: true, CreatedAt: now},
	}}
	f.status = giteaapi.Combined{State: "success", SHA: strings.Repeat("b", 40), Statuses: []giteaapi.Status{
		{ID: 1, State: "success", Context: "ci / build", UpdatedAt: now},
		{ID: 2, State: "success", Context: "ci / test", UpdatedAt: now},
	}}
	cl, s := newTestClient(t, f, testToken)
	row := repoRow{Repo: Repo{FullName: "acme/web", Name: "web", DefaultBranch: "main", ServiceName: "web"}}
	in := &syncInput{def: nil, verdict: "", env: "prod"}
	sc := Scope{Releases: true, Checks: true}

	objs, cur, err := s.syncRepo(context.Background(), cl, in, row, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs.releases) != 2 { // la pre-release non passa senza includerla nel profilo
		t.Fatalf("release lette: %d", len(objs.releases))
	}
	if objs.releases[0].Release.Version != "1.0.0" || objs.releases[1].Release.Version != "1.1.0" {
		t.Fatalf("ordine dal più vecchio: %q, %q", objs.releases[0].Release.Version, objs.releases[1].Release.Version)
	}
	if objs.checkRun == nil || objs.checkRun.Verdict != "passed" || len(objs.checkRun.Checks) != 2 || objs.checkRun.Commit != strings.Repeat("b", 40) {
		t.Fatalf("controlli: %+v", objs.checkRun)
	}

	// Seconda lettura con i cursori: niente di nuovo.
	row.cursors, _ = json.Marshal(cur)
	objs2, _, err := s.syncRepo(context.Background(), cl, in, row, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs2.releases) != 0 || objs2.checkRun != nil {
		t.Fatalf("la rilettura doveva essere vuota: %d release, controlli %v", len(objs2.releases), objs2.checkRun != nil)
	}
	// Una lettura completa rilegge tutto (l'Ingestor scarta i doppioni).
	objs3, _, err := s.syncRepo(context.Background(), cl, in, row, Scope{Releases: true, Checks: true, Full: true})
	if err != nil || len(objs3.releases) != 2 || objs3.checkRun == nil {
		t.Fatalf("lettura completa: %v, %d release", err, len(objs3.releases))
	}
	// Uno stato in corso non produce un esito.
	f.status.Statuses[1].State, f.status.State = "pending", "pending"
	row.cursors = nil
	objs4, _, err := s.syncRepo(context.Background(), cl, in, row, Scope{Checks: true})
	if err != nil || objs4.checkRun != nil {
		t.Fatalf("controlli in corso: %v, %v", err, objs4.checkRun)
	}
}

func TestStatusInputs(t *testing.T) {
	in := statusInputs([]giteaapi.Status{
		{ID: 1, State: "failure", Context: "a"}, {ID: 2, State: "error", Context: "b"},
		{ID: 3, State: "warning", Context: "c"}, {ID: 4, State: "pending", Context: "d"},
	})
	want := [][2]string{{"completed", "failure"}, {"completed", "failure"}, {"completed", "neutral"}, {"in_progress", ""}}
	for i, w := range want {
		if in[i].Status != w[0] || in[i].Conclusion != w[1] {
			t.Errorf("stato %d: %+v", i, in[i])
		}
	}
}

func TestPresetValid(t *testing.T) {
	for _, p := range Presets() {
		if ps := p.Definition.Validate(); len(ps) > 0 {
			t.Errorf("preset %s non valido: %v", p.Key, ps)
		}
	}
	if PresetByKey(PresetGiteaActions) == nil || PresetByKey("x") != nil {
		t.Error("PresetByKey")
	}
	d := Presets()[0].Definition
	if !d.AcceptsTag("v1.0.0", false) || d.AcceptsTag("1.0.0", false) {
		t.Error("filtro dei tag del preset")
	}
}

func TestMatchName(t *testing.T) {
	if !matchName("sbom.cdx.json", "SBOM.cdx.json") || !matchName("*.json", "x.json") || matchName("sbom.json", "altro.json") {
		t.Error("matchName")
	}
}

func TestReleaseFingerprint(t *testing.T) {
	a := giteaapi.Release{ID: 1, TagName: "v1"}
	b := a
	b.Assets = append(b.Assets, giteaapi.Attachment{Name: "sbom.json", Size: 10})
	if releaseFingerprint(a) == releaseFingerprint(b) {
		t.Error("un allegato aggiunto deve cambiare l'impronta")
	}
}
