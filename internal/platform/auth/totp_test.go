package auth

import (
	"strings"
	"testing"
	"time"
)

// Vettori di prova di RFC 6238 (appendice B, SHA-1), con le ultime 6 cifre.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		if got := hotp(secret, uint64(ts/30)); got != want {
			t.Errorf("T=%d: %s, atteso %s", ts, got, want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	cur := totpStep(now)
	code := hotp(secret, uint64(cur-1))
	st, ok := verifyTOTP(secret, code[:3]+" "+code[3:], now, 0)
	if !ok || st != cur-1 {
		t.Fatalf("codice dell'intervallo precedente: %v %d", ok, st)
	}
	if _, ok := verifyTOTP(secret, code, now, st); ok {
		t.Fatal("lo stesso intervallo non deve valere due volte")
	}
	if _, ok := verifyTOTP(secret, hotp(secret, uint64(cur-3)), now, 0); ok {
		t.Fatal("codice troppo vecchio accettato")
	}
	if _, ok := verifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("codice corto accettato")
	}
	uri := totpURI(secret, "anna@example.test")
	if !strings.HasPrefix(uri, "otpauth://totp/Regia:anna@example.test?") || !strings.Contains(uri, "secret="+b32.EncodeToString(secret)) {
		t.Fatalf("URI: %s", uri)
	}
	c, err := TOTPCode(b32.EncodeToString(secret), now)
	if err != nil || c != hotp(secret, uint64(cur)) {
		t.Fatalf("TOTPCode: %s %v", c, err)
	}
}
