package outbound

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

type resolver map[string][]netip.Addr

func (r resolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestParseProxy(t *testing.T) {
	for _, c := range []struct{ in, want, err string }{
		{"", "", ""},
		{"proxy.azienda.it:3128", "http://proxy.azienda.it:3128", ""},
		{"http://utente:segreto@proxy:3128/", "http://utente@proxy:3128", ""},
		{"socks5://proxy:1080", "", "http:// o https://"},
		{"http://proxy:3128/percorso", "", "percorso"},
	} {
		u, err := ParseProxy(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: errore %v, atteso %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || Redacted(u) != c.want {
			t.Errorf("%q: %q %v, atteso %q", c.in, Redacted(u), err, c.want)
		}
	}
}

// Il proxy riceve la richiesta con l'URL assoluto; la destinazione si controlla prima.
func TestViaProxy(t *testing.T) {
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") == "" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		seen = append(seen, r.URL.String())
		_, _ = io.WriteString(w, "dal proxy")
	}))
	defer proxy.Close()
	base, err := ghapi.NewHTTPClient(ghapi.Public, "", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := ParseProxy(strings.Replace(proxy.URL, "http://", "http://utente:segreto@", 1))
	res := resolver{"pubblico.esempio.it": {netip.MustParseAddr("93.184.216.34")}, "interno.esempio.it": {netip.MustParseAddr("10.1.2.3")}}
	c, err := Via(base, ghapi.Public, pu, res)
	if err != nil {
		t.Fatal(err)
	}
	get := func(u string) (string, error) {
		resp, err := c.Get(u)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	// Pubblico: passa dal proxy (che sta su 127.0.0.1: il dialer di base lo bloccherebbe).
	if b, err := get("http://pubblico.esempio.it/api/now/table/incident"); err != nil || b != "dal proxy" {
		t.Fatalf("destinazione pubblica: %q %v", b, err)
	}
	// Non risolto localmente: lo risolve il proxy.
	if _, err := get("http://solo-dal-proxy.esempio.it/"); err != nil {
		t.Fatalf("nome risolto dal proxy: %v", err)
	}
	// Interno o letterale privato: bloccato prima di uscire.
	for _, u := range []string{"http://interno.esempio.it/", "http://169.254.169.254/latest/meta-data"} {
		if _, err := get(u); !errors.Is(err, ghapi.ErrBlocked) {
			t.Fatalf("%s: atteso il blocco SSRF, ottenuto %v", u, err)
		}
	}
	if len(seen) != 2 || !strings.HasPrefix(seen[0], "http://pubblico.esempio.it/") {
		t.Fatalf("richieste viste dal proxy: %v", seen)
	}
	// Senza credenziali il proxy risponde 407: il messaggio lo spiega senza password.
	pu2, _ := ParseProxy(proxy.URL)
	c2, _ := Via(base, ghapi.Public, pu2, res)
	_, err = c2.Get("http://pubblico.esempio.it/")
	if err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("407: %v", err)
	}
	if strings.Contains(err.Error(), "segreto") {
		t.Fatalf("password del proxy nel messaggio: %v", err)
	}
	// Senza proxy: il client di base, invariato.
	if same, _ := Via(base, ghapi.Public, nil, nil); same != base {
		t.Fatal("senza proxy il client deve restare quello di base")
	}
}
