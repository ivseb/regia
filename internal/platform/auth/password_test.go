package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("una-password-lunga")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=12288,t=3,p=1$") {
		t.Fatalf("formato inatteso: %s", h)
	}
	if ok, _ := VerifyPassword(h, "una-password-lunga"); !ok {
		t.Fatal("la password corretta non è accettata")
	}
	if ok, _ := VerifyPassword(h, "una-password-lungA"); ok {
		t.Fatal("una password sbagliata è accettata")
	}
}

func TestPasswordTooShort(t *testing.T) {
	if _, err := HashPassword("corta"); err == nil {
		t.Fatal("password corta accettata")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(3, 60_000_000_000)
	for i := 0; i < 3; i++ {
		if !l.allow("k") {
			t.Fatalf("tentativo %d bloccato", i+1)
		}
	}
	if l.allow("k") {
		t.Fatal("il quarto tentativo doveva essere bloccato")
	}
	if !l.allow("altra") {
		t.Fatal("chiavi diverse devono essere indipendenti")
	}
	l.reset("k")
	if !l.allow("k") {
		t.Fatal("dopo il reset il tentativo deve passare")
	}
}
