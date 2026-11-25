// Package secretbox cifra i segreti a riposo (segreti TOTP, segreti dei webhook) con la
// chiave applicativa della cartella dati: AES-256-GCM, nonce casuale, e un "contesto"
// (per esempio l'id dell'account) legato al testo cifrato come dato autenticato, così un
// segreto copiato su un'altra riga non si decifra.
//
// Formato: 1 byte di versione (1) | 12 byte di nonce | testo cifrato con il tag.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
)

const version = 1

// KeySize è la lunghezza della chiave (AES-256).
const KeySize = 32

var (
	ErrKey     = errors.New("chiave applicativa non valida: servono 32 byte")
	ErrDecrypt = errors.New("impossibile decifrare il segreto: chiave applicativa diversa o dato alterato")
)

// Box cifra e decifra con una chiave.
type Box struct {
	aead cipher.AEAD
	key  []byte
}

// New prepara la cifratura con una chiave di 32 byte.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, ErrKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: append([]byte(nil), key...)}, nil
}

// Random crea una cifratura con una chiave casuale in memoria (test, o server senza
// cartella dati: i segreti non sopravvivono al riavvio).
func Random() *Box {
	k := make([]byte, KeySize)
	_, _ = rand.Read(k)
	b, _ := New(k)
	return b
}

// Seal cifra plain legandolo a context.
func (b *Box) Seal(plain []byte, context string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plain)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plain, []byte(context)), nil
}

// Open decifra un valore prodotto da Seal con lo stesso contesto.
func (b *Box) Open(sealed []byte, context string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return nil, ErrDecrypt
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// MAC è un HMAC-SHA256 con una chiave derivata da quella applicativa (per i codici di
// recupero: il database da solo non basta a verificarli né a indovinarli offline).
func (b *Box) MAC(purpose string, data []byte) []byte {
	sub := hmac.New(sha256.New, b.key)
	sub.Write([]byte("regia/" + purpose))
	m := hmac.New(sha256.New, sub.Sum(nil))
	m.Write(data)
	return m.Sum(nil)
}

// LoadKey legge la chiave dal file, creandola al primo avvio (permessi 0600, cartella
// 0700). Perderla rende illeggibili i segreti MFA e dei webhook: va nel backup della
// cartella dati.
func LoadKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		if len(b) != KeySize {
			return nil, ErrKey
		}
		return b, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	b := make([]byte, KeySize)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	// O_EXCL: se due processi partono insieme vince il primo, l'altro rilegge.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	return b, f.Close()
}
