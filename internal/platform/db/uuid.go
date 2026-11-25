package db

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// NewID genera un UUIDv7 (RFC 9562): ordinato nel tempo, quindi gli indici
// B-tree restano compatti e gli inserimenti vanno in coda.
func NewID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint16(b[0:2], uint16(ms>>32))
	binary.BigEndian.PutUint32(b[2:6], uint32(ms))
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err) // crypto/rand non fallisce sui sistemi supportati
	}
	b[6] = (b[6] & 0x0f) | 0x70 // versione 7
	b[8] = (b[8] & 0x3f) | 0x80 // variante RFC 9562
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], b[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], b[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], b[8:10])
	s[23] = '-'
	hex.Encode(s[24:], b[10:])
	return string(s[:])
}
