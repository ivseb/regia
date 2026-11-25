// Package backup crea e ripristina il backup completo di un'istanza di Regia:
// dump coerente di Postgres (pg_dump in formato custom), chiavi applicative (keys/),
// allegati (files/) e un manifest con versioni e checksum.
//
// Il file è un archivio tar leggibile con gli strumenti standard:
//
//	manifest.json      versione di Regia, di Postgres, migrazioni ed estensioni (prima voce:
//	                   il ripristino controlla la compatibilità prima di scrivere qualcosa)
//	db/mytrack.dump    pg_dump -Fc
//	keys/…             chiavi applicative (secrets.key cifra MFA e segreti dei webhook)
//	files/…            allegati, indirizzati per sha256
//	SHA256SUMS         checksum di tutte le voci (ultima voce; `sha256sum -c` dopo `tar x`)
//
// Con una passphrase l'archivio è cifrato con age (scrypt): si apre anche con il comando
// `age -d`, senza Regia. Estensione .tar.age.
package backup

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
)

// Format è la versione del formato dell'archivio. Un ripristino rifiuta i formati più recenti.
const Format = 1

const (
	manifestName = "manifest.json"
	dumpName     = "db/mytrack.dump"
	sumsName     = "SHA256SUMS"
)

// Manifest descrive il backup. Sta all'inizio dell'archivio.
type Manifest struct {
	Format     int       `json:"format"`
	App        string    `json:"app"`
	Version    string    `json:"version"`
	Commit     string    `json:"commit"`
	CreatedAt  time.Time `json:"created_at"`
	Mode       string    `json:"mode"` // "embedded" (Postgres incluso) o "external"
	Postgres   PGInfo    `json:"postgres"`
	Database   string    `json:"database"`
	Migrations []string  `json:"migrations"`
	Extensions []string  `json:"extensions"`
}

// PGInfo sono le versioni di Postgres al momento del backup.
type PGInfo struct {
	Server      string `json:"server"`
	ServerMajor int    `json:"server_major"`
	Dump        string `json:"pg_dump"`
	DumpMajor   int    `json:"pg_dump_major"`
}

// LastMigration è l'ultima migrazione applicata al momento del backup.
func (m Manifest) LastMigration() string {
	if len(m.Migrations) == 0 {
		return ""
	}
	return m.Migrations[len(m.Migrations)-1]
}

// Options per Create.
type Options struct {
	DataDir    string // cartella dati: si salvano keys/ e files/
	OwnerURL   string // connessione del proprietario dello schema
	BinDir     string // cartella di pg_dump; vuota = ricerca (accanto al binario, poi PATH)
	Embedded   bool   // Postgres incluso: ambiente ripulito per il socket locale
	Out        string // file di destinazione (scritto in modo atomico, permessi 0600)
	Passphrase string // se presente l'archivio è cifrato con age
	Version    string
	Commit     string
	Log        *slog.Logger
	// WorkFactor di scrypt per la cifratura (0 = default di age, circa un secondo).
	WorkFactor int
}

// Result riassume il backup scritto.
type Result struct {
	Path      string
	Manifest  Manifest
	Bytes     int64 // dimensione del file
	Keys      int
	Files     int
	Encrypted bool
}

// Create scrive il backup. Con il Postgres incluso funziona anche a server avviato:
// pg_dump legge un'istantanea coerente senza bloccare le scritture. Gli allegati si
// copiano dopo il dump: non si cancellano mai e nascono prima della riga che li cita,
// quindi ogni file nominato dal dump c'è.
func Create(ctx context.Context, o Options) (*Result, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Out == "" {
		return nil, errors.New("manca il file di destinazione")
	}
	pgDump, err := FindTool(o.BinDir, "pg_dump")
	if err != nil {
		return nil, err
	}
	dumpVer, dumpMajor, err := ToolVersion(ctx, pgDump)
	if err != nil {
		return nil, err
	}
	m := Manifest{Format: Format, App: "regia", Version: o.Version, Commit: o.Commit,
		CreatedAt: time.Now().UTC().Truncate(time.Second), Mode: "external",
		Postgres: PGInfo{Dump: dumpVer, DumpMajor: dumpMajor}}
	if o.Embedded {
		m.Mode = "embedded"
	}
	if err := readDBInfo(ctx, o.OwnerURL, &m); err != nil {
		return nil, err
	}
	if dumpMajor < m.Postgres.ServerMajor {
		return nil, fmt.Errorf("pg_dump %d è più vecchio del server Postgres %d: serve pg_dump %d o successivo (REGIA_PG_BIN)",
			dumpMajor, m.Postgres.ServerMajor, m.Postgres.ServerMajor)
	}

	dir := filepath.Dir(o.Out)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cartella dei backup: %w", err)
	}
	dumpFile, err := tempPath(dir, ".regia-dump-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(dumpFile)
	start := time.Now()
	cmd := exec.CommandContext(ctx, pgDump, "--format=custom", "--no-password", "--file="+dumpFile, "--dbname="+o.OwnerURL)
	cmd.Env = os.Environ()
	if o.Embedded {
		cmd.Env = pgEnv()
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pg_dump: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	o.Log.Info("dump del database completato", "durata", time.Since(start).Round(time.Millisecond).String())

	tmp, err := tempPath(dir, ".regia-backup-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp) // dopo il rename non c'è più
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	res := &Result{Path: o.Out, Manifest: m, Encrypted: o.Passphrase != ""}
	if err := writeArchive(f, o, &m, dumpFile, res); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, o.Out); err != nil {
		return nil, err
	}
	syncDir(dir)
	if fi, err := os.Stat(o.Out); err == nil {
		res.Bytes = fi.Size()
	}
	return res, nil
}

func writeArchive(f *os.File, o Options, m *Manifest, dumpFile string, res *Result) error {
	bw := bufio.NewWriterSize(f, 1<<20)
	var w io.Writer = bw
	var enc io.WriteCloser
	if o.Passphrase != "" {
		rcp, err := age.NewScryptRecipient(o.Passphrase)
		if err != nil {
			return err
		}
		if o.WorkFactor > 0 {
			rcp.SetWorkFactor(o.WorkFactor)
		}
		if enc, err = age.Encrypt(bw, rcp); err != nil {
			return err
		}
		w = enc
	}
	tw := tar.NewWriter(w)
	var sums []string
	add := func(name string, r io.Reader, size int64, mtime time.Time) error {
		hdr := &tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: mtime, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(tw, io.TeeReader(io.LimitReader(r, size), h))
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("%s è cambiato durante il backup (%d byte invece di %d)", name, n, size)
		}
		sums = append(sums, hex.EncodeToString(h.Sum(nil))+"  "+name)
		return nil
	}
	addFile := func(name, src string) error {
		fh, err := os.Open(src)
		if err != nil {
			return err
		}
		defer fh.Close()
		fi, err := fh.Stat()
		if err != nil {
			return err
		}
		return add(name, fh, fi.Size(), fi.ModTime())
	}

	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mb = append(mb, '\n')
	if err := add(manifestName, strings.NewReader(string(mb)), int64(len(mb)), m.CreatedAt); err != nil {
		return err
	}
	if err := addFile(dumpName, dumpFile); err != nil {
		return err
	}
	for _, sub := range []string{"keys", "files"} {
		root := filepath.Join(o.DataDir, sub)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && p == root {
					return nil // cartella non ancora creata: niente da salvare
				}
				return err
			}
			rel, _ := filepath.Rel(o.DataDir, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "files/tmp" { // caricamenti in corso
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if err := addFile(rel, p); err != nil {
				return err
			}
			if sub == "keys" {
				res.Keys++
			} else {
				res.Files++
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", sub, err)
		}
	}
	body := strings.Join(sums, "\n") + "\n"
	if err := add(sumsName, strings.NewReader(body), int64(len(body)), m.CreatedAt); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// readDBInfo legge versione del server, migrazioni ed estensioni.
func readDBInfo(ctx context.Context, url string, m *Manifest) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("connessione al database: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	var num int
	if err := conn.QueryRow(ctx, "select current_setting('server_version'), current_setting('server_version_num')::int, current_database()").
		Scan(&m.Postgres.Server, &num, &m.Database); err != nil {
		return err
	}
	m.Postgres.ServerMajor = num / 10000
	m.Extensions, err = queryStrings(ctx, conn, "select extname from pg_extension where extname <> 'plpgsql' order by 1")
	if err != nil {
		return err
	}
	var has bool
	if err := conn.QueryRow(ctx, "select to_regclass('schema_migrations') is not null").Scan(&has); err != nil {
		return err
	}
	if has {
		if m.Migrations, err = queryStrings(ctx, conn, "select version from schema_migrations order by version"); err != nil {
			return err
		}
	}
	if m.Migrations == nil {
		m.Migrations = []string{}
	}
	return nil
}

func queryStrings(ctx context.Context, conn *pgx.Conn, sql string) ([]string, error) {
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// FindTool cerca un programma di Postgres: nella cartella indicata, poi in pgsql/bin
// accanto al binario (layout del pacchetto), poi nel PATH.
func FindTool(binDir, name string) (string, error) {
	var candidates []string
	if binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, name))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "pgsql", "bin", name))
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s non trovato: indica la cartella dei binari di Postgres con --pg-bin o REGIA_PG_BIN", name)
}

var versionRe = regexp.MustCompile(`\(PostgreSQL\)\s+(\d+)(?:\.\d+)*`)

// ToolVersion legge la versione di un programma di Postgres ("pg_dump (PostgreSQL) 18.1",
// anche con suffissi come "(Homebrew)" o "(Debian 18.1-1.pgdg12+1)").
func ToolVersion(ctx context.Context, tool string) (string, int, error) {
	out, err := exec.CommandContext(ctx, tool, "--version").Output()
	if err != nil {
		return "", 0, fmt.Errorf("%s --version: %w", filepath.Base(tool), err)
	}
	mm := versionRe.FindStringSubmatch(string(out))
	if mm == nil {
		return "", 0, fmt.Errorf("versione di %s non riconosciuta: %q", filepath.Base(tool), out)
	}
	major, _ := strconv.Atoi(mm[1])
	return strings.TrimSpace(strings.TrimPrefix(mm[0], "(PostgreSQL)")), major, nil
}

// pgEnv toglie dall'ambiente le variabili PG* e di locale, come per il server incluso:
// la connessione al socket locale non deve ereditare impostazioni dell'utente.
func pgEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LC_") || strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "PG") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "LC_ALL=C", "LANG=C")
}

func tempPath(dir, prefix string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return filepath.Join(dir, prefix+hex.EncodeToString(b)), nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// FileName è il nome di un backup con data e ora: <prefisso>-20261124T183000Z.tar[.age].
// L'ordine alfabetico è quello cronologico (serve alla retention).
func FileName(prefix string, at time.Time, encrypted bool) string {
	name := prefix + "-" + at.UTC().Format("20060102T150405Z") + ".tar"
	if encrypted {
		name += ".age"
	}
	return name
}

var stampRe = regexp.MustCompile(`^-\d{8}T\d{6}Z\.tar(\.age)?$`)

// List elenca i backup con il prefisso indicato nella cartella, dal più recente.
func List(dir, prefix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, prefix) && stampRe.MatchString(strings.TrimPrefix(n, prefix)) {
			out = append(out, n)
		}
	}
	// Stesso istante con e senza .age: l'ordine resta stabile.
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// Prune tiene gli ultimi keep backup con il prefisso indicato e cancella gli altri.
func Prune(dir, prefix string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	names, err := List(dir, prefix)
	if err != nil || len(names) <= keep {
		return nil, err
	}
	var removed []string
	for _, n := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}

// safeEntry controlla che il nome di una voce dell'archivio sia uno di quelli attesi e
// non esca dalla cartella di destinazione.
func safeEntry(name string) bool {
	if name == manifestName || name == dumpName || name == sumsName {
		return true
	}
	clean := path.Clean(name)
	if clean != name || strings.HasPrefix(clean, "/") || slices.Contains(strings.Split(clean, "/"), "..") {
		return false
	}
	return (strings.HasPrefix(clean, "keys/") && strings.Count(clean, "/") == 1) || strings.HasPrefix(clean, "files/")
}
