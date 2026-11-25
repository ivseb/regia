package ghapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"time"
)

// ParsePrivateKey legge la chiave privata della GitHub App (PEM, PKCS#1 come la scarica
// GitHub oppure PKCS#8).
func ParsePrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil {
		return nil, errors.New("chiave privata non valida: serve il file .pem scaricato da GitHub (BEGIN RSA PRIVATE KEY)")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("chiave privata RSA non leggibile")
		}
		return k, nil
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("chiave privata PKCS#8 non leggibile")
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("la chiave della GitHub App deve essere RSA")
		}
		return rk, nil
	}
	return nil, errors.New("blocco PEM inatteso: " + block.Type)
}

// AppJWT firma il JWT con cui la App si presenta a GitHub (RS256): emesso 60 secondi
// prima di adesso (orologi non allineati), valido 9 minuti (GitHub ne accetta al massimo 10).
func AppJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	if key == nil || appID == "" {
		return "", errors.New("App ID o chiave mancanti")
	}
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// VerifyAppJWT verifica un JWT della App (usato dal finto GitHub dei test e dello sviluppo).
func VerifyAppJWT(token string, pub *rsa.PublicKey, now time.Time) (issuer string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("JWT malformato")
	}
	enc := base64.RawURLEncoding
	var h struct {
		Alg string `json:"alg"`
	}
	hb, err := enc.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &h) != nil || h.Alg != "RS256" {
		return "", errors.New("intestazione del JWT non valida")
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("firma del JWT non valida")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return "", errors.New("firma del JWT non valida")
	}
	var c struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	cb, err := enc.DecodeString(parts[1])
	if err != nil || json.Unmarshal(cb, &c) != nil {
		return "", errors.New("claim del JWT non validi")
	}
	if now.Unix() >= c.Exp || c.Iat > now.Unix()+60 || c.Exp-c.Iat > 11*60 {
		return "", errors.New("JWT scaduto o con durata non ammessa")
	}
	return c.Iss, nil
}
