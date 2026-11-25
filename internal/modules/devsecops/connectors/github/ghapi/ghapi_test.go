package ghapi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"regia/internal/devtools/fakegithub"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

type sleeper struct {
	mu    sync.Mutex
	total time.Duration
	calls int
}

func (s *sleeper) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total += d
	s.calls++
	return nil
}

func newFake(t *testing.T, enterprise bool) (*fakegithub.Server, *httptest.Server) {
	t.Helper()
	fg := fakegithub.New()
	if enterprise {
		fg.BasePath = "/api/v3"
		fg.Enterprise = "3.15.0"
	}
	hs := httptest.NewServer(fg.Handler())
	t.Cleanup(hs.Close)
	return fg, hs
}

func appClient(t *testing.T, fg *fakegithub.Server, base string, tokens *ghapi.TokenCache, sl *sleeper) *ghapi.Client {
	t.Helper()
	hc, err := ghapi.NewHTTPClient(ghapi.Any, "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ghapi.New(ghapi.Options{BaseURL: base, HTTP: hc, Tokens: tokens, CacheKey: "int-1", Sleep: sl.sleep,
		Auth: ghapi.Auth{Mode: "app", AppID: fg.AppID, InstallationID: fg.InstallationID, PrivateKeyPEM: fg.PrivateKeyPEM()}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAppJWTAndInstallationTokenCache(t *testing.T) {
	fg, hs := newFake(t, true)
	fg.AddOwner("esempio", "")
	ctx := context.Background()
	tokens := ghapi.NewTokenCache()
	sl := &sleeper{}
	c := appClient(t, fg, hs.URL+"/api/v3", tokens, sl)

	app, err := c.App(ctx)
	if err != nil || app.Name == "" {
		t.Fatalf("GET /app con il JWT: %+v %v", app, err)
	}
	inst, err := c.Installation(ctx)
	if err != nil || inst.Permissions["checks"] != "read" {
		t.Fatalf("installazione: %+v %v", inst, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Owner(ctx, "esempio"); err != nil {
			t.Fatal(err)
		}
	}
	if fg.IssuedTokens() != 1 || tokens.Issued() != 1 {
		t.Fatalf("token di installazione emessi %d (cache: %d): atteso 1", fg.IssuedTokens(), tokens.Issued())
	}
	if c.ServerVersion() != "3.15.0" {
		t.Fatalf("versione di GHES: %q", c.ServerVersion())
	}
	// Un secondo client della stessa integrazione riusa il token in cache.
	c2 := appClient(t, fg, hs.URL+"/api/v3", tokens, sl)
	if _, err := c2.Owner(ctx, "esempio"); err != nil || fg.IssuedTokens() != 1 {
		t.Fatalf("cache condivisa: %d token, %v", fg.IssuedTokens(), err)
	}
	// Un token vicino alla scadenza si rinnova.
	fg.TokenTTL = 2 * time.Minute
	tokens.Forget("int-1")
	if _, err := c.Owner(ctx, "esempio"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Owner(ctx, "esempio"); err != nil {
		t.Fatal(err)
	}
	if fg.IssuedTokens() != 3 {
		t.Fatalf("token che scade entro 5 minuti: %d emessi, attesi 3", fg.IssuedTokens())
	}

	// Chiave sbagliata: la firma del JWT non si verifica.
	other := fakegithub.New()
	hc, _ := ghapi.NewHTTPClient(ghapi.Any, "", time.Second)
	bad, err := ghapi.New(ghapi.Options{BaseURL: hs.URL + "/api/v3", HTTP: hc, CacheKey: "x",
		Auth: ghapi.Auth{Mode: "app", AppID: fg.AppID, InstallationID: fg.InstallationID, PrivateKeyPEM: other.PrivateKeyPEM()}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.Owner(ctx, "esempio")
	var ge *ghapi.Error
	if !errors.As(err, &ge) || ge.Status != 401 || !strings.Contains(err.Error(), "token di installazione non ottenuto") {
		t.Fatalf("chiave errata: %v", err)
	}
	if _, err := ghapi.ParsePrivateKey("non è un pem"); err == nil {
		t.Fatal("chiave non PEM accettata")
	}
}

func TestPaginationAndRedirectedDownload(t *testing.T) {
	fg, hs := newFake(t, false)
	fg.PATs["ghp_x"] = true
	for i := 0; i < 230; i++ {
		fg.AddRepo("grande", fmt.Sprintf("repo-%03d", i))
	}
	fg.AddRelease("grande/repo-001", "v1.0.0", "anna", time.Now(), map[string][]byte{"record.json": []byte(`{"version":"1.0.0"}`)})
	hc, _ := ghapi.NewHTTPClient(ghapi.Any, "", 5*time.Second)
	c, err := ghapi.New(ghapi.Options{BaseURL: hs.URL, HTTP: hc, Auth: ghapi.Auth{Mode: "token", Token: "ghp_x"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	n := 0
	if err := c.OwnerRepos(ctx, "grande", func(ghapi.Repo) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 230 || fg.Count("/orgs/grande/repos") != 3 {
		t.Fatalf("paginazione: %d repository in %d pagine", n, fg.Count("/orgs/grande/repos"))
	}
	var rel ghapi.Release
	if err := c.Releases(ctx, "grande/repo-001", 1, func(r ghapi.Release) (bool, error) { rel = r; return true, nil }); err != nil {
		t.Fatal(err)
	}
	b, err := c.DownloadAsset(ctx, "grande/repo-001", rel.Assets[0].ID, 1<<20)
	if err != nil || string(b) != `{"version":"1.0.0"}` {
		t.Fatalf("download con redirect (senza Authorization verso l'URL firmato): %q %v", b, err)
	}
	if _, err := c.DownloadAsset(ctx, "grande/repo-001", rel.Assets[0].ID, 5); err == nil {
		t.Fatal("allegato oltre il limite accettato")
	}
	if c.Scopes() == "" {
		t.Fatal("scope del token classico non letti")
	}
}

func TestRateLimits(t *testing.T) {
	fg, hs := newFake(t, false)
	fg.PATs["ghp_x"] = true
	fg.AddOwner("esempio", "")
	hc, _ := ghapi.NewHTTPClient(ghapi.Any, "", 5*time.Second)
	sl := &sleeper{}
	c, _ := ghapi.New(ghapi.Options{BaseURL: hs.URL, HTTP: hc, Sleep: sl.sleep, MaxWait: time.Minute, Reserve: 5,
		Auth: ghapi.Auth{Mode: "token", Token: "ghp_x"}})
	ctx := context.Background()

	// Limite secondario: Retry-After rispettato, poi si riprova.
	fg.FailNext("/orgs/esempio", 429, 7)
	if _, err := c.Owner(ctx, "esempio"); err != nil {
		t.Fatal(err)
	}
	if sl.total != 7*time.Second {
		t.Fatalf("attesa per Retry-After: %v", sl.total)
	}
	r := c.Rate()
	if r.Limit != 5000 || r.Remaining != 4999 {
		t.Fatalf("X-RateLimit-* non letti: %+v", r)
	}

	// Poche richieste rimaste e reset vicino: si aspetta il reset.
	fg.SetRemaining(5, 20*time.Second)
	sl.total = 0
	if _, err := c.Owner(ctx, "esempio"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Owner(ctx, "esempio"); err != nil {
		t.Fatal(err)
	}
	if sl.total < 15*time.Second || sl.total > 22*time.Second {
		t.Fatalf("attesa per la riserva: %v", sl.total)
	}

	// Limite esaurito con reset lontano: errore RateLimited, senza attese lunghe.
	fg.SetRemaining(0, 2*time.Hour)
	sl.total = 0
	c2, _ := ghapi.New(ghapi.Options{BaseURL: hs.URL, HTTP: hc, Sleep: sl.sleep, MaxWait: time.Minute, Auth: ghapi.Auth{Mode: "token", Token: "ghp_x"}})
	_, err := c2.Owner(ctx, "esempio")
	reset, ok := ghapi.IsRateLimited(err)
	if !ok || reset.Before(time.Now().Add(time.Hour)) || sl.total != 0 {
		t.Fatalf("limite esaurito: %v (reset %v, atteso %v)", err, reset, sl.total)
	}
	if !strings.Contains(err.Error(), "si riprende") {
		t.Fatalf("messaggio: %v", err)
	}
}

func TestSSRFPolicy(t *testing.T) {
	for _, c := range []struct {
		ip              string
		public, private bool
	}{
		{"8.8.8.8", true, true}, {"10.1.2.3", false, true}, {"192.168.1.10", false, true}, {"127.0.0.1", false, true},
		{"169.254.169.254", false, false}, {"fe80::1", false, false}, {"0.0.0.0", false, false}, {"fd00::1", false, true},
	} {
		ip := netip.MustParseAddr(c.ip)
		if got := ghapi.Public.Allowed(ip); got != c.public {
			t.Errorf("Public %s = %v", c.ip, got)
		}
		if got := ghapi.Private.Allowed(ip); got != c.private {
			t.Errorf("Private %s = %v", c.ip, got)
		}
	}
	for raw, want := range map[string]string{
		"":                               "https://api.github.com",
		"https://github.com":             "https://api.github.com",
		"github.example.com":             "https://github.example.com/api/v3",
		"https://github.example.com/":    "https://github.example.com/api/v3",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/v3",
	} {
		got, err := ghapi.NormalizeBaseURL(raw, ghapi.Public)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q %v, atteso %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"http://ghe.example.com", "https://10.0.0.5", "https://ghe.internal", "https://localhost:8443",
		"https://user:pw@ghe.example.com", "https://ghe.example.com/?x=1", "ftp://ghe.example.com"} {
		if _, err := ghapi.NormalizeBaseURL(bad, ghapi.Public); err == nil {
			t.Errorf("URL %s accettato con la politica pubblica", bad)
		}
	}
	if _, err := ghapi.NormalizeBaseURL("https://10.0.0.5", ghapi.Private); err != nil {
		t.Errorf("rete interna consentita: %v", err)
	}
	if _, err := ghapi.NormalizeBaseURL("https://169.254.169.254", ghapi.Private); err == nil {
		t.Error("metadati del cloud accettati con la rete interna")
	}

	// Alla connessione conta l'indirizzo vero: un server su 127.0.0.1 è irraggiungibile
	// con la politica pubblica, raggiungibile con la rete interna.
	fg, hs := newFake(t, false)
	fg.PATs["ghp_x"] = true
	fg.AddOwner("esempio", "")
	for _, p := range []ghapi.Policy{ghapi.Public, ghapi.Private} {
		hc, _ := ghapi.NewHTTPClient(p, "", 2*time.Second)
		c, _ := ghapi.New(ghapi.Options{BaseURL: hs.URL, HTTP: hc, Auth: ghapi.Auth{Mode: "token", Token: "ghp_x"}})
		_, err := c.Owner(context.Background(), "esempio")
		if p == ghapi.Public && !errors.Is(err, ghapi.ErrBlocked) {
			t.Fatalf("politica pubblica verso 127.0.0.1: %v", err)
		}
		if p == ghapi.Private && err != nil {
			t.Fatalf("rete interna verso 127.0.0.1: %v", err)
		}
	}
}
