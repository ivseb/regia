package servicenow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"table":"incident"}`)
	const secret = "segreto-di-almeno-sedici"
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	b64 := base64.StdEncoding.EncodeToString(m.Sum(nil))
	for _, c := range []struct {
		sig  string
		want bool
	}{
		{Sign(secret, body), true},
		{"sha256=" + Sign(secret, body), true},
		{"SHA256=" + Sign(secret, body), true},
		{b64, true}, // GlideCertificateEncryption.generateMac
		{"", false},
		{"sha256=00ff", false},
		{Sign("altro-segreto-lungo-abbastanza", body), false},
		{base64.StdEncoding.EncodeToString([]byte("corta")), false},
	} {
		if got := VerifySignature(secret, body, c.sig); got != c.want {
			t.Errorf("%q: %v, atteso %v", c.sig, got, c.want)
		}
	}
	if VerifySignature("", body, Sign("", body)) {
		t.Error("senza segreto nessuna firma vale")
	}
}

func TestCleanDefaults(t *testing.T) {
	s := New(Options{Dev: true})
	in := Input{Name: " ServiceNow ", BaseURL: "acme", Username: "regia", Password: "pw", Priorities: []int{2, 1, 2}}
	c, sec, err := s.clean(&in, secrets{})
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://acme.service-now.com" || c.Auth != "basic" || c.ServiceClass != DefaultServiceClass || c.DoraMode != DoraConfirm ||
		len(c.Priorities) != 2 || c.Priorities[0] != 1 || c.LookbackDays != 90 || c.IntervalMinutes != 30 || sec.Password != "pw" {
		t.Fatalf("predefiniti: %+v %+v", c, sec)
	}
	// Passando a OAuth la password si toglie.
	in2 := Input{Name: "x", BaseURL: "acme", Auth: "oauth", ClientID: "id", ClientSecret: "s"}
	_, sec2, err := s.clean(&in2, sec)
	if err != nil || sec2.Password != "" || sec2.ClientSecret != "s" {
		t.Fatalf("cambio di autenticazione: %+v %v", sec2, err)
	}
	for _, bad := range []Input{
		{Name: "x", BaseURL: "acme", Username: "regia"}, // manca la password
		{Name: "x", BaseURL: "acme", Username: "regia", Password: "pw", ServiceFilter: "a=1^NQb=2"},
		{Name: "x", BaseURL: "acme", Username: "regia", Password: "pw", DoraMode: "sempre"},
		{Name: "x", BaseURL: "acme", Username: "regia", Password: "pw", UseProxy: true}, // nessun proxy dell'operatore
	} {
		if _, _, err := s.clean(&bad, secrets{}); err == nil {
			t.Errorf("atteso un errore: %+v", bad)
		}
	}
}
