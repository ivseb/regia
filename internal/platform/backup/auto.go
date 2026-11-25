package backup

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"regia/internal/platform/db"
)

// AutoPrefix è il prefisso dei backup automatici fatti prima delle migrazioni.
const AutoPrefix = "auto"

// Auto è l'esito del controllo prima delle migrazioni.
type Auto struct {
	Status  db.MigrationStatus
	Result  *Result  // nil se il backup non serviva
	Removed []string // backup automatici vecchi cancellati dalla retention
}

// BeforeMigrate fa un backup in dir se il database è già in uso e ci sono migrazioni da
// applicare (al primo avvio non c'è niente da salvare), poi tiene solo gli ultimi keep
// backup automatici. o.Out viene scelto qui.
func BeforeMigrate(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS, o Options, dir string, keep int) (*Auto, error) {
	st, err := db.Status(ctx, pool, migrations)
	if err != nil {
		return nil, err
	}
	a := &Auto{Status: st}
	if len(st.Pending) == 0 || st.Fresh() {
		return a, nil
	}
	o.Out = filepath.Join(dir, FileName(AutoPrefix, time.Now(), o.Passphrase != ""))
	if o.Log != nil {
		o.Log.Info("migrazioni nuove da applicare: backup automatico prima dell'aggiornamento",
			"migrazioni", len(st.Pending), "da", last(st.Applied), "a", last(st.Pending), "file", o.Out)
	}
	if a.Result, err = Create(ctx, o); err != nil {
		return a, err
	}
	a.Removed, err = Prune(dir, AutoPrefix, keep)
	return a, err
}

func last(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}
