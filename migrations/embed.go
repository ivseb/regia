// Package migrations contiene le migrazioni SQL, incluse nel binario.
// Regole: i file applicati non si modificano più (il checksum è verificato);
// ogni modifica è una nuova migrazione, additiva e retrocompatibile per un rilascio.
// Ogni nuova tabella con tenant_id deve attivare la RLS con enable_tenant_rls().
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
