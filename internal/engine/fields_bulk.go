package engine

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scrittura in blocco dei campi personalizzati, per l'import di progetti da altri strumenti
// (internal/modules/importer): l'importatore prepara da solo la forma salvata in
// elements.fields (gli id delle opzioni e dei membri li risolve in memoria, senza una query
// per valore) e chiede qui le righe dell'indice tipizzato, con le stesse regole di
// syncValues. Così l'indice resta uno solo, anche per gli elementi importati.

// IndexedValue è una riga dell'indice tipizzato element_values.
type IndexedValue struct {
	FieldID string
	Ord     int
	Text    *string
	Num     *float64
	TS      *time.Time
	Ref     *string
}

// TypeFieldDefs restituisce i campi associati al tipo (id del tipo), come li usa il motore.
func TypeFieldDefs(ctx context.Context, tx pgx.Tx, typeID string) ([]FieldDef, error) {
	return loadTypeFields(ctx, tx, typeID)
}

// IndexValues calcola le righe dell'indice per i valori già normalizzati di un elemento.
func IndexValues(defs []FieldDef, fields map[string]any) []IndexedValue {
	rows := indexRows(defs, fields)
	out := make([]IndexedValue, len(rows))
	for i, r := range rows {
		out[i] = IndexedValue{FieldID: r.fieldID, Ord: r.ord, Text: r.text, Num: r.num, TS: r.ts, Ref: r.ref}
	}
	return out
}

// IsUUID dice se il testo è un id (minuscolo) di un elemento, un'opzione o un membro.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }
