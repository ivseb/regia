package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Parametri argon2id: una delle configurazioni equivalenti raccomandate da OWASP
// (12 MiB, 3 passaggi; resistenza pari a 19 MiB con 2 passaggi), scelta per il budget
// di memoria del backend. Le hash salvano i parametri: si possono cambiare in futuro
// senza invalidare le password esistenti.
const (
	argonMemoryKiB = 12 * 1024
	argonTime      = 3
	argonThreads   = 1
	argonKeyLen    = 32
	saltLen        = 16
	// MinPasswordLen segue NIST SP 800-63B: lunghezza, non regole di composizione.
	MinPasswordLen = 12
	MaxPasswordLen = 256
)

var b64 = base64.RawStdEncoding

// HashPassword restituisce la hash in formato PHC: $argon2id$v=19$m=...,t=...,p=...$sale$hash
func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	<-hashSem
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func ValidatePassword(pw string) error {
	if n := len([]rune(pw)); n < MinPasswordLen || len(pw) > MaxPasswordLen {
		return fmt.Errorf("la password deve avere almeno %d caratteri", MinPasswordLen)
	}
	return nil
}

// VerifyPassword confronta in tempo costante.
func VerifyPassword(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("formato della hash non riconosciuto")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false, errors.New("versione argon2 non supportata")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, err
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	if len(pw) > MaxPasswordLen {
		return false, nil
	}
	hashSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSem
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// hashSem limita le hash in parallelo: con 2 slot il picco è ~24 MiB anche sotto
// un attacco di login, invece di crescere con il numero di richieste.
var hashSem = make(chan struct{}, 2)

// dummyHash serve a spendere lo stesso tempo quando l'account non esiste
// (non si rivela quali email sono registrate).
// Calcolata al primo uso, non all'avvio: un server a riposo non spende memoria per argon2.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("password-di-riferimento-non-usata")
	return h
})
