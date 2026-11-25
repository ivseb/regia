package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP secondo RFC 6238 (HOTP RFC 4226 sul tempo): HMAC-SHA1, 6 cifre, intervalli di 30
// secondi, tolleranza di un intervallo prima e dopo per l'orologio del telefono. Sono i
// parametri che tutte le app di autenticazione supportano.
const (
	totpDigits    = 6
	totpPeriod    = 30
	totpSkew      = 1
	totpSecretLen = 20 // 160 bit, come raccomanda RFC 4226
	totpIssuer    = "Regia"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() []byte {
	b := make([]byte, totpSecretLen)
	_, _ = rand.Read(b)
	return b
}

// hotp calcola il codice per un contatore (RFC 4226 §5.3, troncamento dinamico).
func hotp(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// verifyTOTP confronta il codice con gli intervalli vicini a now e restituisce
// l'intervallo riconosciuto. Gli intervalli <= lastStep sono già stati usati: un codice
// intercettato non vale una seconda volta.
func verifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = normalizeCode(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	found := int64(-1)
	for d := -totpSkew; d <= totpSkew; d++ {
		st := cur + int64(d)
		if st <= lastStep || st < 0 {
			continue
		}
		// Si provano tutti gli intervalli (niente uscita anticipata): tempo costante.
		if subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(st))), []byte(code)) == 1 && found < 0 {
			found = st
		}
	}
	return found, found >= 0
}

func normalizeCode(c string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(c))
}

// totpURI è l'indirizzo otpauth:// che le app leggono dal QR (formato Key Uri di Google
// Authenticator, supportato da tutte le app comuni).
func totpURI(secret []byte, account string) string {
	label := url.PathEscape(totpIssuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", b32.EncodeToString(secret))
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCode calcola il codice corrente di un segreto in base32 (per i test e per gli
// script di verifica; l'app non lo usa).
func TOTPCode(secretB32 string, t time.Time) (string, error) {
	s, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secretB32, " ", "")))
	if err != nil {
		return "", err
	}
	return hotp(s, uint64(totpStep(t))), nil
}
