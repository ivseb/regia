package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
)

// ErrPassphrase: l'archivio è cifrato e serve la passphrase (o quella data è sbagliata).
var ErrPassphrase = errors.New("il backup è cifrato: serve la passphrase giusta")

// ageHeader è l'inizio di un file cifrato con age (formato binario).
const ageHeader = "age-encryption.org/v1\n"

// Encrypted dice se il file è un backup cifrato.
func Encrypted(file string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(ageHeader))
	n, _ := io.ReadFull(f, head)
	return string(head[:n]) == ageHeader, nil
}

// archive legge le voci dell'archivio in ordine, verificandone i checksum.
type archive struct {
	f    *os.File
	tr   *tar.Reader
	man  Manifest
	sums map[string]string // calcolati durante la lettura
}

func openArchive(file, passphrase string) (*archive, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	var r io.Reader = br
	if head, _ := br.Peek(len(ageHeader)); string(head) == ageHeader {
		if passphrase == "" {
			f.Close()
			return nil, ErrPassphrase
		}
		id, err := age.NewScryptIdentity(passphrase)
		if err != nil {
			f.Close()
			return nil, err
		}
		dr, err := age.Decrypt(br, id)
		if err != nil {
			f.Close()
			var noMatch *age.NoIdentityMatchError
			if errors.As(err, &noMatch) {
				return nil, ErrPassphrase
			}
			return nil, fmt.Errorf("decifratura: %w", err)
		}
		r = dr
	}
	a := &archive{f: f, tr: tar.NewReader(r), sums: map[string]string{}}
	hdr, err := a.tr.Next()
	if err != nil || hdr.Name != manifestName {
		f.Close()
		return nil, errors.New("non è un backup di Regia: manca manifest.json all'inizio")
	}
	var buf bytes.Buffer
	if err := a.copy(hdr, &buf); err != nil {
		f.Close()
		return nil, err
	}
	if err := json.Unmarshal(buf.Bytes(), &a.man); err != nil || (a.man.App != "regia" && a.man.App != "mytrack") {
		f.Close()
		return nil, errors.New("manifest.json non valido")
	}
	return a, nil
}

func (a *archive) Close() error { return a.f.Close() }

// copy copia la voce corrente in w calcolandone lo sha256.
func (a *archive) copy(hdr *tar.Header, w io.Writer) error {
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), a.tr); err != nil {
		return fmt.Errorf("%s: %w", hdr.Name, err)
	}
	a.sums[hdr.Name] = hex.EncodeToString(h.Sum(nil))
	return nil
}

// each scorre le voci dopo il manifest; fn riceve quelle diverse da SHA256SUMS, che
// alla fine si confronta con i checksum calcolati.
func (a *archive) each(fn func(hdr *tar.Header) error) error {
	var sums string
	for {
		hdr, err := a.tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("archivio danneggiato: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !safeEntry(hdr.Name) {
			return fmt.Errorf("voce inattesa nell'archivio: %q", hdr.Name)
		}
		if hdr.Name == sumsName {
			var b bytes.Buffer
			if _, err := io.Copy(&b, io.LimitReader(a.tr, 64<<20)); err != nil {
				return err
			}
			sums = b.String()
			continue
		}
		if err := fn(hdr); err != nil {
			return err
		}
	}
	if sums == "" {
		return errors.New("archivio incompleto: manca SHA256SUMS (backup interrotto?)")
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(sums), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			return fmt.Errorf("SHA256SUMS: riga non valida %q", line)
		}
		want[name] = sum
	}
	for name, sum := range want {
		if a.sums[name] != sum {
			if a.sums[name] == "" {
				return fmt.Errorf("archivio incompleto: manca %s", name)
			}
			return fmt.Errorf("checksum diverso per %s: archivio alterato o danneggiato", name)
		}
	}
	for name := range a.sums {
		if _, ok := want[name]; !ok {
			return fmt.Errorf("voce %s non elencata in SHA256SUMS", name)
		}
	}
	return nil
}

// Summary è il risultato di una verifica.
type Summary struct {
	Manifest  Manifest
	Entries   int
	Keys      int
	Files     int
	DumpBytes int64
	Encrypted bool
}

// Verify legge tutto l'archivio e ne verifica i checksum, senza scrivere nulla.
func Verify(file, passphrase string) (*Summary, error) {
	enc, err := Encrypted(file)
	if err != nil {
		return nil, err
	}
	a, err := openArchive(file, passphrase)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	s := &Summary{Manifest: a.man, Encrypted: enc}
	err = a.each(func(hdr *tar.Header) error {
		s.Entries++
		count(s, hdr)
		return a.copy(hdr, io.Discard)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func count(s *Summary, hdr *tar.Header) {
	switch {
	case hdr.Name == dumpName:
		s.DumpBytes = hdr.Size
	case strings.HasPrefix(hdr.Name, "keys/"):
		s.Keys++
	case strings.HasPrefix(hdr.Name, "files/"):
		s.Files++
	}
}

// Compatible controlla che il backup si possa ripristinare con questo binario:
// formato conosciuto e nessuna migrazione sconosciuta (backup di una versione più recente).
// Un backup più vecchio va bene: le migrazioni mancanti si applicano al primo avvio.
func Compatible(m Manifest, known []string) error {
	if m.Format > Format {
		return fmt.Errorf("formato di backup %d non supportato da questa versione (massimo %d): aggiorna Regia", m.Format, Format)
	}
	var unknown []string
	for _, v := range m.Migrations {
		if !slices.Contains(known, v) {
			unknown = append(unknown, v)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("il backup viene da una versione più recente di Regia (%s, migrazioni sconosciute: %s): ripristinalo con un binario almeno di quella versione",
			m.Version, strings.Join(unknown, ", "))
	}
	return nil
}

// RestoreOptions per Restore.
type RestoreOptions struct {
	In         string
	Passphrase string
	DataDir    string // destinazione di keys/ e files/ (e di pg/ con il Postgres incluso)
	// OwnerURL è il database di destinazione, già avviato e vuoto.
	OwnerURL string
	BinDir   string // cartella di pg_restore
	Embedded bool
	// Known sono le migrazioni del binario (compatibilità).
	Known []string
	// Force sposta keys/ e files/ esistenti in <data>/pre-restore-<data e ora>/ invece di fermarsi.
	Force bool
	Log   *slog.Logger
}

// Check apre l'archivio e controlla la compatibilità senza scrivere nulla (solo il manifest).
func Check(file, passphrase string, known []string) (*Manifest, error) {
	a, err := openArchive(file, passphrase)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	return &a.man, Compatible(a.man, known)
}

// Restore ripristina il backup nel database OwnerURL (che deve essere vuoto) e in DataDir.
// Il database si ripristina in un'unica transazione: o tutto o niente.
func Restore(ctx context.Context, o RestoreOptions) (*Summary, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	pgRestore, err := FindTool(o.BinDir, "pg_restore")
	if err != nil {
		return nil, err
	}
	_, restoreMajor, err := ToolVersion(ctx, pgRestore)
	if err != nil {
		return nil, err
	}
	enc, err := Encrypted(o.In)
	if err != nil {
		return nil, err
	}
	a, err := openArchive(o.In, o.Passphrase)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	m := a.man
	if err := Compatible(m, o.Known); err != nil {
		return nil, err
	}
	if restoreMajor < m.Postgres.DumpMajor {
		return nil, fmt.Errorf("il dump è di pg_dump %d: serve pg_restore %d o successivo (trovato %d)", m.Postgres.DumpMajor, m.Postgres.DumpMajor, restoreMajor)
	}

	// Destinazione: database vuoto, estensioni disponibili.
	conn, err := pgx.Connect(ctx, o.OwnerURL)
	if err != nil {
		return nil, fmt.Errorf("connessione al database di destinazione: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	var tables, serverNum int
	if err := conn.QueryRow(ctx, `select (select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = 'public' and c.relkind in ('r', 'p')), current_setting('server_version_num')::int`).Scan(&tables, &serverNum); err != nil {
		return nil, err
	}
	if tables > 0 {
		return nil, errors.New("il database di destinazione non è vuoto: il ripristino va fatto in un database nuovo")
	}
	if serverNum/10000 < m.Postgres.ServerMajor {
		o.Log.Warn("il server di destinazione è più vecchio di quello del backup: il ripristino potrebbe fallire",
			"backup", m.Postgres.ServerMajor, "destinazione", serverNum/10000)
	}
	available, err := queryStrings(ctx, conn, "select name from pg_available_extensions")
	if err != nil {
		return nil, err
	}
	var skip []string
	for _, ext := range m.Extensions {
		if slices.Contains(available, ext) {
			continue
		}
		if ext == "vector" {
			// pgvector è facoltativo: senza, la ricerca semantica resta spenta e gli
			// embedding (dati derivati, si ricalcolano) non si ripristinano.
			o.Log.Warn("pgvector non disponibile sulla destinazione: gli embedding della ricerca semantica non si ripristinano")
			skip = append(skip, ext)
			continue
		}
		return nil, fmt.Errorf("estensione %s del backup non disponibile sulla destinazione", ext)
	}
	// Il ruolo applicativo non è nel dump (pg_dump non salva i ruoli): lo si crea come fa la migrazione 0002.
	if _, err := conn.Exec(ctx, `do $$ begin
		if not exists (select 1 from pg_roles where rolname = 'mytrack_app') then create role mytrack_app login; end if;
	end $$`); err != nil {
		return nil, fmt.Errorf("ruolo mytrack_app: %w (in modalità esterna può crearlo l'amministratore del database)", err)
	}

	// Cartelle di destinazione e area di lavoro sullo stesso disco (i rename sono atomici).
	for _, sub := range []string{"keys", "files"} {
		if err := PrepareDir(o.DataDir, sub, o.Force, o.Log); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(o.DataDir, ".restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	s := &Summary{Manifest: m, Encrypted: enc}
	err = a.each(func(hdr *tar.Header) error {
		s.Entries++
		count(s, hdr)
		dst := filepath.Join(stage, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if err := a.copy(hdr, f); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Chtimes(dst, hdr.ModTime, hdr.ModTime)
	})
	if err != nil {
		return nil, err
	}
	o.Log.Info("archivio verificato", "voci", s.Entries, "allegati", s.Files)

	dump := filepath.Join(stage, filepath.FromSlash(dumpName))
	args := []string{"--no-owner", "--exit-on-error", "--single-transaction", "--no-password", "--dbname=" + o.OwnerURL}
	if len(skip) > 0 {
		list, err := filteredList(ctx, pgRestore, dump, stage, skip)
		if err != nil {
			return nil, err
		}
		args = append(args, "--use-list="+list)
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, pgRestore, append(args, dump)...)
	cmd.Env = os.Environ()
	if o.Embedded {
		cmd.Env = pgEnv()
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pg_restore: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	// Le statistiche non sono nel dump: senza, il planner sceglie male fino al primo autovacuum.
	if _, err := conn.Exec(ctx, "analyze"); err != nil {
		return nil, err
	}
	o.Log.Info("database ripristinato", "durata", time.Since(start).Round(time.Millisecond).String())

	for _, sub := range []string{"keys", "files"} {
		src := filepath.Join(stage, sub)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(src, filepath.Join(o.DataDir, sub)); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// PrepareDir verifica che <data>/<sub> non esista o sia vuota; con force la sposta da parte.
func PrepareDir(dataDir, sub string, force bool, log *slog.Logger) error {
	p := filepath.Join(dataDir, sub)
	entries, err := os.ReadDir(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(p)
	}
	if !force {
		return fmt.Errorf("%s esiste già e non è vuota: ripristina in una cartella dati nuova, oppure usa --force per spostarla da parte", p)
	}
	aside := filepath.Join(dataDir, "pre-restore-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return err
	}
	log.Warn("sposto da parte la cartella esistente", "da", p, "a", aside)
	return os.Rename(p, filepath.Join(aside, sub))
}

// filteredList prepara l'elenco delle voci da ripristinare senza le estensioni mancanti
// e gli oggetti che ne dipendono (oggi solo vector e la tabella embeddings).
func filteredList(ctx context.Context, pgRestore, dump, dir string, skip []string) (string, error) {
	out, err := exec.CommandContext(ctx, pgRestore, "--list", dump).Output()
	if err != nil {
		return "", fmt.Errorf("pg_restore --list: %w", err)
	}
	list := filepath.Join(dir, "restore.list")
	return list, os.WriteFile(list, []byte(FilterTOC(string(out), skip)), 0o600)
}

// FilterTOC commenta le righe dell'elenco di pg_restore che riguardano le estensioni
// indicate e, per vector, la tabella embeddings con indici, vincoli, policy e dati.
func FilterTOC(toc string, skip []string) string {
	lines := strings.Split(toc, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ";") || strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Fields(l)
		drop := false
		for _, ext := range skip {
			// "EXTENSION - vector" e "COMMENT - EXTENSION vector".
			if k := slices.Index(f, "EXTENSION"); k >= 0 && slices.Contains(f[k+1:], ext) {
				drop = true
			}
			if ext == "vector" && slices.ContainsFunc(f, func(w string) bool {
				return w == "embeddings" || strings.HasPrefix(w, "embeddings_")
			}) {
				drop = true
			}
		}
		if drop {
			lines[i] = ";" + l
		}
	}
	return strings.Join(lines, "\n")
}
