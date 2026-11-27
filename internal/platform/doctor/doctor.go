// Package doctor controlla lo stato di un'istanza di Regia prima di un avvio, di un
// aggiornamento o dopo un ripristino: cartella dati, Postgres, migrazioni, chiavi
// applicative, spazio su disco, orologio e backup (comando `regia doctor`).
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"regia/internal/platform/backup"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Level è l'esito di un controllo.
type Level int

const (
	OK Level = iota
	Info
	Warn
	Fail
)

func (l Level) String() string {
	return [...]string{"ok", "info", "attenzione", "ERRORE"}[l]
}

// Check è un controllo con il suo esito.
type Check struct {
	Area  string
	Level Level
	Msg   string
}

// Report raccoglie i controlli.
type Report struct {
	Checks []Check
}

func (r *Report) add(area string, l Level, format string, a ...any) {
	r.Checks = append(r.Checks, Check{Area: area, Level: l, Msg: fmt.Sprintf(format, a...)})
}

// Failed dice se almeno un controllo è fallito.
func (r *Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Level == Fail {
			return true
		}
	}
	return false
}

// Count conta i controlli di un livello.
func (r *Report) Count(l Level) int {
	n := 0
	for _, c := range r.Checks {
		if c.Level == l {
			n++
		}
	}
	return n
}

// Print scrive il rapporto, una riga per controllo.
func (r *Report) Print(w io.Writer) {
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%-11s %-12s %s\n", "["+c.Level.String()+"]", c.Area, c.Msg)
	}
	fmt.Fprintf(w, "\n%d controlli: %d errori, %d avvertenze\n", len(r.Checks), r.Count(Fail), r.Count(Warn))
}

// DataDir controlla esistenza, proprietario, permessi e scrivibilità della cartella dati.
func DataDir(r *Report, dir string) {
	const area = "cartella"
	fi, err := os.Stat(dir)
	if err != nil {
		r.add(area, Fail, "%s: %v", dir, err)
		return
	}
	if !fi.IsDir() {
		r.add(area, Fail, "%s non è una cartella", dir)
		return
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		r.add(area, Fail, "%s appartiene all'utente %d, il comando gira come %d: avvialo con l'utente del servizio", dir, st.Uid, os.Getuid())
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		r.add(area, Warn, "%s ha permessi %04o: contiene chiavi e dati, meglio 0700 (chmod 700)", dir, perm)
	} else {
		r.add(area, OK, "%s, permessi %04o", dir, perm)
	}
	f, err := os.CreateTemp(dir, ".doctor-")
	if err != nil {
		r.add(area, Fail, "non scrivibile: %v", err)
		return
	}
	f.Close()
	os.Remove(f.Name())
}

// Keys controlla le chiavi applicative. encrypted è il numero di segreti cifrati nel
// database (-1 = non noto): con segreti presenti e chiave assente non si decifrano più.
func Keys(r *Report, dataDir string, encrypted int, oidc bool) {
	const area = "chiavi"
	dir := filepath.Join(dataDir, "keys")
	if fi, err := os.Stat(dir); err == nil && fi.Mode().Perm()&0o077 != 0 {
		r.add(area, Warn, "%s ha permessi %04o: meglio 0700", dir, fi.Mode().Perm())
	}
	check := func(name string, required bool, why string) {
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist) && required:
			r.add(area, Fail, "%s manca: %s", name, why)
		case errors.Is(err, fs.ErrNotExist):
			r.add(area, Info, "%s non c'è ancora: si crea al primo avvio", name)
		case err != nil:
			r.add(area, Fail, "%s: %v", name, err)
		case fi.Size() != secretbox.KeySize:
			r.add(area, Fail, "%s ha %d byte invece di %d: chiave danneggiata", name, fi.Size(), secretbox.KeySize)
		case fi.Mode().Perm()&0o077 != 0:
			r.add(area, Warn, "%s ha permessi %04o: meglio 0600", name, fi.Mode().Perm())
		default:
			r.add(area, OK, "%s presente (%04o)", name, fi.Mode().Perm())
		}
	}
	check("secrets.key", encrypted > 0, fmt.Sprintf("nel database ci sono %d segreti cifrati (MFA, webhook) che senza questa chiave non si decifrano: ripristinala dal backup", encrypted))
	if oidc {
		check("oidc-state.key", false, "")
	}
}

// Sealed è un segreto MFA cifrato, con l'account che fa da contesto.
type Sealed struct {
	AccountID string
	Value     []byte
}

// KeyMatches verifica che secrets.key decifri i segreti MFA del database (un campione).
// Nessuno decifrato: chiave di un'altra istanza. Solo alcuni: dati alterati o chiave cambiata a metà.
func KeyMatches(r *Report, dataDir string, samples []Sealed) {
	const area = "chiavi"
	if len(samples) == 0 {
		return
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "keys", "secrets.key"))
	if err != nil {
		return // già segnalato da Keys
	}
	box, err := secretbox.New(key)
	if err != nil {
		return
	}
	ok := 0
	for _, s := range samples {
		if _, err := box.Open(s.Value, "mfa:"+s.AccountID); err == nil {
			ok++
		}
	}
	switch {
	case ok == 0:
		r.add(area, Fail, "secrets.key non decifra i segreti MFA del database: è la chiave di un'altra istanza? Ripristina quella del backup")
	case ok < len(samples):
		r.add(area, Warn, "secrets.key decifra %d segreti MFA su %d controllati: gli altri vanno azzerati (reset dell'MFA)", ok, len(samples))
	default:
		r.add(area, OK, "secrets.key decifra i segreti MFA del database (%d controllati)", ok)
	}
}

// Disk controlla lo spazio libero sul disco della cartella indicata.
func Disk(r *Report, area, dir string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		r.add(area, Warn, "spazio su disco non misurabile: %v", err)
		return
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	total := uint64(st.Blocks) * uint64(st.Bsize)
	pct := 0.0
	if total > 0 {
		pct = float64(free) / float64(total) * 100
	}
	msg := fmt.Sprintf("%s liberi su %s (%.0f%%) in %s", gib(free), gib(total), pct, dir)
	switch {
	case free < 1<<30 || pct < 2:
		r.add(area, Fail, "%s: Postgres si ferma quando il disco è pieno", msg)
	case free < 5<<30 || pct < 10:
		r.add(area, Warn, "%s: un backup prima di un aggiornamento occupa quanto il database", msg)
	default:
		r.add(area, OK, "%s", msg)
	}
}

func gib(n uint64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30)) }

// Clock confronta l'orologio locale con quello del database e, dove c'è, chiede a
// systemd se l'orologio è sincronizzato. TOTP e scadenze dipendono dall'ora giusta.
func Clock(r *Report, local, dbNow time.Time) {
	const area = "orologio"
	skew := local.Sub(dbNow)
	if skew < 0 {
		skew = -skew
	}
	switch {
	case dbNow.IsZero():
	case skew > 30*time.Second:
		r.add(area, Fail, "differenza di %s con l'orologio del database: i codici MFA falliscono", skew.Round(time.Second))
	case skew > 2*time.Second:
		r.add(area, Warn, "differenza di %s con l'orologio del database", skew.Round(time.Millisecond))
	default:
		r.add(area, OK, "allineato con il database (differenza %s)", skew.Round(time.Millisecond))
	}
	if p, err := exec.LookPath("timedatectl"); err == nil {
		out, err := exec.Command(p, "show", "-p", "NTPSynchronized", "--value").Output()
		switch v := strings.TrimSpace(string(out)); {
		case err != nil:
		case v == "yes":
			r.add(area, OK, "sincronizzato con NTP")
		default:
			r.add(area, Warn, "non sincronizzato con NTP (timedatectl): attiva systemd-timesyncd o chrony, i codici MFA valgono 30 secondi")
		}
	}
}

// Postgres controlla i binari del Postgres incluso e la versione del cluster su disco.
func Postgres(r *Report, binMajor, dataMajor, minMajor, recommended int) {
	const area = "postgres"
	switch {
	case binMajor < minMajor:
		r.add(area, Fail, "binari di Postgres %d: serve almeno la %d", binMajor, minMajor)
	case binMajor < recommended:
		r.add(area, Warn, "binari di Postgres %d: il pacchetto usa la %d", binMajor, recommended)
	default:
		r.add(area, OK, "binari di Postgres %d", binMajor)
	}
	switch {
	case dataMajor == 0:
		r.add(area, Info, "cluster non ancora inizializzato: initdb al primo avvio")
	case dataMajor != binMajor:
		r.add(area, Fail, "il cluster su disco è di Postgres %d, i binari sono della %d: serve un aggiornamento con pg_upgrade, oppure backup e ripristino", dataMajor, binMajor)
	default:
		r.add(area, OK, "cluster su disco di Postgres %d", dataMajor)
	}
}

// Server controlla la versione del server a cui ci si collega.
func Server(r *Report, version string, major, minMajor, recommended int) {
	const area = "postgres"
	switch {
	case major < minMajor:
		r.add(area, Fail, "server Postgres %s: serve almeno la %d", version, minMajor)
	case major < recommended:
		r.add(area, Warn, "server Postgres %s: consigliata la %d", version, recommended)
	default:
		r.add(area, OK, "server Postgres %s", version)
	}
}

// Tools controlla che pg_dump e pg_restore ci siano e non siano più vecchi del server.
func Tools(r *Report, binDir string, serverMajor int) {
	const area = "backup"
	for _, name := range []string{"pg_dump", "pg_restore"} {
		p, err := backup.FindTool(binDir, name)
		if err != nil {
			r.add(area, Fail, "%s non trovato: backup automatici e ripristino non funzionano", name)
			continue
		}
		v, major, err := backup.ToolVersion(context.Background(), p)
		if err != nil {
			r.add(area, Fail, "%v", err)
			continue
		}
		if serverMajor > 0 && major < serverMajor {
			r.add(area, Fail, "%s è della versione %d, il server della %d: serve la stessa o una successiva", name, major, serverMajor)
			continue
		}
		r.add(area, OK, "%s %s", name, v)
	}
}

// Migrations controlla lo stato delle migrazioni rispetto al binario.
func Migrations(r *Report, st db.MigrationStatus) {
	const area = "migrazioni"
	if len(st.Modified) > 0 {
		r.add(area, Fail, "migrazioni modificate dopo l'applicazione: %s (l'avvio si blocca)", strings.Join(st.Modified, ", "))
	}
	if len(st.Unknown) > 0 {
		// Le migrazioni sono retrocompatibili per un rilascio (architecture.md §10): tornare
		// indietro di uno si può, di più no senza ripristinare il backup di prima.
		r.add(area, Warn, "il database ha migrazioni che questo binario non conosce (%s): è di una versione più recente; va bene solo per tornare indietro di un rilascio",
			strings.Join(st.Unknown, ", "))
	}
	switch {
	case st.Fresh() && len(st.Pending) > 0:
		r.add(area, Info, "database vuoto: %d migrazioni al primo avvio", len(st.Pending))
	case len(st.Pending) > 0:
		r.add(area, Info, "%d applicate, %d da applicare al prossimo avvio (%s → %s), con backup automatico prima",
			len(st.Applied), len(st.Pending), last(st.Applied), last(st.Pending))
	default:
		r.add(area, OK, "%d applicate, nessuna in attesa (ultima %s)", len(st.Applied), last(st.Applied))
	}
}

// Backups controlla l'ultimo backup nella cartella indicata.
func Backups(r *Report, dir string, now time.Time) {
	const area = "backup"
	var newest string
	var at time.Time
	for _, prefix := range []string{"regia", "mytrack", "auto"} {
		names, err := backup.List(dir, prefix)
		if err != nil || len(names) == 0 {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, names[0])); err == nil && fi.ModTime().After(at) {
			newest, at = names[0], fi.ModTime()
		}
	}
	switch age := now.Sub(at); {
	case newest == "":
		r.add(area, Warn, "nessun backup in %s: pianifica `regia backup` (docs/operativo.md)", dir)
	case age > 48*time.Hour:
		r.add(area, Warn, "ultimo backup %s di %s fa: il backup periodico gira?", newest, age.Round(time.Hour))
	default:
		r.add(area, OK, "ultimo backup %s (%s fa)", newest, age.Round(time.Minute))
	}
}

// Database esegue i controlli che richiedono la connessione.
func Database(ctx context.Context, r *Report, pool *pgxpool.Pool, fsys fs.FS, dataDir string, oidc bool, minMajor, recommended int) (serverMajor int) {
	var version string
	var num int
	var now time.Time
	if err := pool.QueryRow(ctx, "select current_setting('server_version'), current_setting('server_version_num')::int, clock_timestamp()").
		Scan(&version, &num, &now); err != nil {
		r.add("postgres", Fail, "connessione: %v", err)
		return 0
	}
	Server(r, version, num/10000, minMajor, recommended)
	Clock(r, time.Now(), now)
	st, err := db.Status(ctx, pool, fsys)
	if err != nil {
		r.add("migrazioni", Fail, "%v", err)
		return num / 10000
	}
	Migrations(r, st)
	encrypted := -1
	if !st.Fresh() {
		// Segreti cifrati con secrets.key: MFA, webhook, integrazioni DevSecOps, provider AI.
		if err := pool.QueryRow(ctx, `select (select count(*) from accounts where mfa_secret_enc is not null)
			+ (select count(*) from webhooks)
			+ (select count(*) from integrations where secret_enc is not null)
			+ (select count(*) from ai_settings where secret_enc is not null)
			+ (select count(*) from ai_providers where secret_enc is not null)`).Scan(&encrypted); err != nil {
			encrypted = -1
		}
	}
	Keys(r, dataDir, encrypted, oidc)
	if rows, err := pool.Query(ctx, `select id::text, mfa_secret_enc from accounts where mfa_secret_enc is not null
		order by id desc limit 50`); err == nil {
		if samples, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sealed, error) {
			var s Sealed
			return s, row.Scan(&s.AccountID, &s.Value)
		}); err == nil {
			KeyMatches(r, dataDir, samples)
		}
	}
	return num / 10000
}

func last(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return s[len(s)-1]
}
