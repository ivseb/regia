package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	defer func() { trustedProxies = nil }()
	req := func(remote, xff string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return clientIP(r).String()
	}
	// Senza proxy fidati X-Forwarded-For si ignora.
	if got := req("10.1.1.1:5000", "203.0.113.9"); got != "10.1.1.1" {
		t.Fatalf("senza proxy fidati: %s", got)
	}
	trustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.64.0.0/13")}
	cases := []struct{ remote, xff, want string }{
		{"10.1.1.1:5000", "203.0.113.9, 172.68.1.2", "203.0.113.9"},          // client → Cloudflare → Traefik
		{"10.1.1.1:5000", "6.6.6.6, 203.0.113.9, 172.68.1.2", "203.0.113.9"}, // il client non può falsificare
		{"198.51.100.7:5000", "203.0.113.9", "198.51.100.7"},                 // connessione diretta non fidata
		{"10.1.1.1:5000", "", "10.1.1.1"},
		{"10.1.1.1:5000", "spazzatura", "10.1.1.1"},
	}
	for _, c := range cases {
		if got := req(c.remote, c.xff); got != c.want {
			t.Errorf("%s / %q = %s, atteso %s", c.remote, c.xff, got, c.want)
		}
	}
}
