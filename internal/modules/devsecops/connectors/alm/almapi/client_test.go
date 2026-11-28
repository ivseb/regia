package almapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"regia/internal/devtools/fakealm"
	"regia/internal/modules/devsecops/connectors/alm/almapi"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
		p        ghapi.Policy
	}{
		{"https://alm.azienda.it", "https://alm.azienda.it/qcbin", ghapi.Public},
		{"alm.azienda.it/qcbin/", "https://alm.azienda.it/qcbin", ghapi.Public},
		{"https://alm.azienda.it/qcbin/rest", "https://alm.azienda.it/qcbin", ghapi.Public},
		{"https://qc.azienda.it:8443/alm/qcbin", "https://qc.azienda.it:8443/alm/qcbin", ghapi.Public},
		{"http://127.0.0.1:8191/qcbin", "http://127.0.0.1:8191/qcbin", ghapi.Any},
	}
	for _, c := range cases {
		got, err := almapi.NormalizeBaseURL(c.in, c.p)
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, atteso %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "http://alm.azienda.it", "https://u:p@alm.azienda.it", "https://10.0.0.5/qcbin", "https://alm/qcbin", "ftp://alm.azienda.it"} {
		if _, err := almapi.NormalizeBaseURL(bad, ghapi.Public); err == nil {
			t.Errorf("%q accettato", bad)
		}
	}
}

func newClient(t *testing.T, fake *fakealm.Server, auth almapi.Auth) *almapi.Client {
	t.Helper()
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(srv.Close)
	cl, err := almapi.New(almapi.Options{BaseURL: srv.URL + "/qcbin", Auth: auth, Username: "lettore", Password: "pw",
		ClientID: "cid", ClientSecret: "zq-segreto-7781", HTTP: srv.Client(), Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestSessionPagingAndXSRF(t *testing.T) {
	fake := fakealm.New()
	fake.AddUser("lettore", "pw")
	fake.RequireXSRF = true
	fake.AddProject("D", "P")
	for i := range 1203 {
		fake.Put("D", "P", "tests", map[string]string{"name": fmt.Sprintf("Test %d", i)})
	}
	cl := newClient(t, fake, almapi.AuthUser)
	ctx := context.Background()
	n := 0
	err := cl.Entities(ctx, almapi.Query{Domain: "D", Project: "P", Collection: "tests", Fields: []string{"id", "name"}, Limit: 1100},
		func(e almapi.Entity) (bool, error) {
			if e["name"] == "" {
				return false, fmt.Errorf("nome vuoto: %v", e)
			}
			n++
			return false, nil
		})
	if err != nil || n != 1100 || cl.Method() != almapi.MethodALMAuthenticate {
		t.Fatalf("lettura a pagine: %d %v %s", n, err, cl.Method())
	}
	// Sessione scaduta: si rientra una volta, senza errori.
	fake.Expire()
	if err := cl.Entities(ctx, almapi.Query{Domain: "D", Project: "P", Collection: "tests", Limit: 1}, func(almapi.Entity) (bool, error) { return false, nil }); err != nil {
		t.Fatalf("dopo la scadenza della sessione: %v", err)
	}
	if fake.Logins != 2 {
		t.Fatalf("accessi: %d", fake.Logins)
	}
	// Logout chiude la sessione (DELETE con X-XSRF-TOKEN).
	cl.Logout(ctx)
	if fake.Open() != 0 || fake.Logouts != 1 {
		t.Fatalf("sessioni dopo il logout: %d, logout %d", fake.Open(), fake.Logouts)
	}
	// Campo inesistente: 400 con un messaggio che lo spiega.
	err = cl.Entities(ctx, almapi.Query{Domain: "D", Project: "P", Collection: "tests", Fields: []string{"user-99"}}, func(almapi.Entity) (bool, error) { return false, nil })
	if almapi.StatusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "user-99") {
		t.Fatalf("campo inesistente: %v", err)
	}
	err = cl.Entities(ctx, almapi.Query{Domain: "D", Project: "ALTRO", Collection: "tests"}, func(almapi.Entity) (bool, error) { return false, nil })
	if almapi.StatusOf(err) != http.StatusNotFound {
		t.Fatalf("progetto inesistente: %v", err)
	}
}

func TestAPIKeyFallback(t *testing.T) {
	fake := fakealm.New()
	fake.AddAPIKey("cid", "zq-segreto-7781")
	fake.OnlyOAuthAPIKeys = true
	cl := newClient(t, fake, almapi.AuthAPIKey)
	if err := cl.Login(context.Background()); err != nil || cl.Method() != almapi.MethodOAuth2Login {
		t.Fatalf("API key su ALM 12.60: %v %s", err, cl.Method())
	}
	bad := fakealm.New()
	cl = newClient(t, bad, almapi.AuthAPIKey)
	if err := cl.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), "zq-segreto-7781") {
		t.Fatalf("API key sconosciuta: %v", err)
	}
}

func TestParseTime(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	got := almapi.ParseTime("2026-07-01 10:00:00", rome)
	if got == nil || got.UTC().Hour() != 8 {
		t.Fatalf("ora estiva di Roma: %v", got)
	}
	if d := almapi.ParseTime("2026-12-01", rome); d == nil || d.UTC().Hour() != 23 {
		t.Fatalf("sola data: %v", d)
	}
	if almapi.ParseTime("domani", rome) != nil {
		t.Fatal("data non valida accettata")
	}
	if f := almapi.IDs("id", []string{"1", "x", "3"}); f != "id[1 or 3]" {
		t.Fatalf("filtro per id: %s", f)
	}
}
