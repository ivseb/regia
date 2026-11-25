package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// L'app passa gli id come stringhe, anche negli array (id = any($2::uuid[])). pgx sceglie il
// formato binario per uuid e uuid[], non sa codificarvi una stringa, fallisce e riprova in testo:
// ma il messaggio dell'errore formatta l'intero valore (migliaia di id) e a ogni query allocava
// centinaia di KB (il 7% della memoria allocata dal benchmark del 2026-11-24, 101 MB in 40 s).
// Questo codec codifica le stringhe in binario; una stringa non valida fallisce come prima e
// pgx ripete in testo, così l'errore resta quello di Postgres (22P02).

type uuidStringCodec struct{ pgtype.UUIDCodec }

func (c uuidStringCodec) PlanEncode(m *pgtype.Map, oid uint32, format int16, value any) pgtype.EncodePlan {
	if _, ok := value.(string); ok && format == pgtype.BinaryFormatCode {
		return encodeUUIDString{}
	}
	return c.UUIDCodec.PlanEncode(m, oid, format, value)
}

type encodeUUIDString struct{}

func (encodeUUIDString) Encode(value any, buf []byte) ([]byte, error) {
	var u pgtype.UUID
	if err := u.Scan(value.(string)); err != nil {
		return nil, err
	}
	return append(buf, u.Bytes[:]...), nil
}

// registerUUIDStrings installa il codec nella mappa dei tipi di una connessione.
func registerUUIDStrings(_ context.Context, conn *pgx.Conn) error {
	m := conn.TypeMap()
	m.RegisterType(&pgtype.Type{Name: "uuid", OID: pgtype.UUIDOID, Codec: uuidStringCodec{}})
	uuid, _ := m.TypeForOID(pgtype.UUIDOID)
	m.RegisterType(&pgtype.Type{Name: "_uuid", OID: pgtype.UUIDArrayOID, Codec: &pgtype.ArrayCodec{ElementType: uuid}})
	return nil
}
