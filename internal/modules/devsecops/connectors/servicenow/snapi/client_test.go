package snapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"regia/internal/devtools/fakeservicenow"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

func TestNormalizeBaseURL(t *testing.T) {
	for _, c := range []struct {
		in   string
		p    ghapi.Policy
		want string
		err  string
	}{
		{"acme", ghapi.Public, "https://acme.service-now.com", ""},
		{"https://acme.service-now.com/", ghapi.Public, "https://acme.service-now.com", ""},
		{"https://itsm.azienda.it/sn/api/now", ghapi.Public, "https://itsm.azienda.it/sn", ""},
		{"http://acme.service-now.com", ghapi.Public, "", "https"},
		{"http://10.0.0.5:8080", ghapi.Private, "http://10.0.0.5:8080", ""},
		{"https://10.0.0.5", ghapi.Public, "", "Rete interna"},
		{"https://servicenow.lan", ghapi.Public, "", "Rete interna"},
		{"https://utente:pw@acme.service-now.com", ghapi.Public, "", "credenziali"},
		{"", ghapi.Public, "", "indirizzo"},
	} {
		got, err := NormalizeBaseURL(c.in, c.p)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: %q %v, atteso un errore con %q", c.in, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: %q %v, atteso %q", c.in, got, err, c.want)
		}
	}
}

func client(t *testing.T, fake *fakeservicenow.Server, o Options) *Client {
	t.Helper()
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(srv.Close)
	hc, err := ghapi.NewHTTPClient(ghapi.Any, "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	o.BaseURL, o.HTTP = srv.URL, hc
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Paginazione oltre una pagina, campi minimi, token OAuth riusato.
func TestTableOAuthAndPaging(t *testing.T) {
	fake := fakeservicenow.New()
	fake.AddClient("id", "segreto")
	for i := range PageSize + 5 {
		fake.Put("incident", map[string]string{"number": fmt.Sprintf("INC%07d", i), "priority": "1", "caller_id": "persona"})
	}
	c := client(t, fake, Options{Auth: AuthOAuth, ClientID: "id", ClientSecret: "segreto"})
	n := 0
	err := c.Table(context.Background(), Query{Table: "incident", Query: "priority=1^ORDERBYnumber", Fields: []string{"sys_id", "number"}}, func(r map[string]string) (bool, error) {
		n++
		if _, ok := r["caller_id"]; ok {
			return true, fmt.Errorf("campo non chiesto: %v", r)
		}
		return false, nil
	})
	if err != nil || n != PageSize+5 {
		t.Fatalf("letti %d record: %v", n, err)
	}
	// 1 token + 2 pagine; una seconda lettura riusa il token.
	before := c.Requests()
	_ = c.Table(context.Background(), Query{Table: "incident", Limit: 1}, func(map[string]string) (bool, error) { return false, nil })
	if before != 3 || c.Requests() != 4 {
		t.Fatalf("richieste: %d poi %d", before, c.Requests())
	}
}

func TestErrors(t *testing.T) {
	fake := fakeservicenow.New()
	fake.AddUser("regia", "giusta")
	fake.Deny("change_request")
	bad := client(t, fake, Options{Auth: AuthBasic, Username: "regia", Password: "sbagliata"})
	err := bad.Table(context.Background(), Query{Table: "incident"}, func(map[string]string) (bool, error) { return false, nil })
	if StatusOf(err) != 401 || !strings.Contains(err.Error(), "utente o password") || strings.Contains(err.Error(), "sbagliata") {
		t.Fatalf("401: %v", err)
	}
	ok := client(t, fake, Options{Auth: AuthBasic, Username: "regia", Password: "giusta"})
	err = ok.Table(context.Background(), Query{Table: "change_request"}, func(map[string]string) (bool, error) { return false, nil })
	if StatusOf(err) != 403 || !strings.Contains(err.Error(), "«change_request»") || !strings.Contains(err.Error(), "ACL") {
		t.Fatalf("403: %v", err)
	}
	oauth := client(t, fake, Options{Auth: AuthOAuth, ClientID: "nessuno", ClientSecret: "x"})
	err = oauth.Table(context.Background(), Query{Table: "incident"}, func(map[string]string) (bool, error) { return false, nil })
	if !strings.Contains(err.Error(), "glide.oauth.inbound.client.credential.grant_type.enabled") {
		t.Fatalf("token OAuth negato: %v", err)
	}
}

func TestTimes(t *testing.T) {
	tm := ParseTime("2026-11-28 10:15:00")
	if tm == nil || tm.Location() != time.UTC || FormatTime(*tm) != "2026-11-28 10:15:00" || ParseTime("") != nil || ParseTime("ieri") != nil {
		t.Fatalf("date: %v", tm)
	}
	if u := RecordURL("https://acme.service-now.com", "incident", "abc"); u != "https://acme.service-now.com/nav_to.do?uri=incident.do%3Fsys_id%3Dabc" {
		t.Fatalf("link: %s", u)
	}
}
