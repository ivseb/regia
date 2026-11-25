package api

import (
	"strings"
	"testing"
	"time"
)

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/richieste?q=aperte":    "/richieste?q=aperte",
		"/richieste/DEM-1/piano": "/richieste/DEM-1/piano",
		"https://evil.example":   "/",
		"//evil.example/x":       "/",
		"/\\evil.example":        "/",
		"/%0d%0aSet-Cookie:x":    "/%0d%0aSet-Cookie:x", // resta un percorso interno, codificato
		"/ok\r\nLocation: x":     "/",
		"javascript:alert(1)":    "/",
		"/api/v1/me":             "/",
		"richieste":              "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, atteso %q", in, got, want)
		}
	}
}

func TestOIDCStateCookie(t *testing.T) {
	s := &Server{stateKey: []byte("chiave-di-prova-di-32-byte-esatti")}
	st := oidcState{State: "s", Nonce: "n", Verifier: "v", Next: "/", Expires: time.Now().Add(time.Minute).Unix()}
	v, err := s.sealState(st)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.openState(v); !ok || got != st {
		t.Fatalf("cookie valido rifiutato: %v %v", got, ok)
	}
	// Firma alterata, contenuto alterato, chiave diversa, scaduto.
	payload, sig, _ := strings.Cut(v, ".")
	bad := []string{payload + "." + strings.Repeat("A", len(sig)), "e30." + sig, v + "x", "senza-punto"}
	for _, b := range bad {
		if _, ok := s.openState(b); ok {
			t.Fatalf("cookie alterato accettato: %q", b)
		}
	}
	other := &Server{stateKey: []byte("un'altra-chiave-di-32-byte-esatti")}
	if _, ok := other.openState(v); ok {
		t.Fatal("cookie firmato con un'altra chiave accettato")
	}
	st.Expires = time.Now().Add(-time.Second).Unix()
	old, _ := s.sealState(st)
	if _, ok := s.openState(old); ok {
		t.Fatal("cookie scaduto accettato")
	}
}
