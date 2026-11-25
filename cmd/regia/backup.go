package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"regia/internal/platform/backup"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/embeddedpg"
	"regia/migrations"
)

// Backup, ripristino e backup automatico prima delle migrazioni (piano, passo 17; docs/operativo.md).

func cmdBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "file di destinazione (.tar, o .tar.age se cifrato)")
	dir := fs.String("dir", "", "cartella dei backup: nome con data e ora (default REGIA_BACKUP_DIR o <data>/backups)")
	keep := fs.Int("keep", 0, "con --dir: tiene solo gli ultimi N backup con lo stesso prefisso (0 = tutti)")
	prefix := fs.String("prefix", "regia", "con --dir: prefisso del nome del file")
	passFile := fs.String("passphrase-file", "", "cifra il backup con la passphrase contenuta nel file")
	encrypt := fs.Bool("encrypt", false, "cifra il backup chiedendo la passphrase a terminale")
	cfg, err := config.Load(fs, args)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	bs, err := config.BackupSettings(cfg.DataDir)
	if err != nil {
		return err
	}
	pass, err := backupPassphrase(*passFile, *encrypt, bs)
	if err != nil {
		return err
	}
	target := *out
	if target == "" {
		if *dir == "" {
			*dir = bs.Dir
		}
		target = filepath.Join(*dir, backup.FileName(*prefix, time.Now(), pass != ""))
	} else if *dir != "" || *keep != 0 {
		return errors.New("--out esclude --dir e --keep")
	}

	src, err := openBackupSource(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer src.close()
	res, err := backup.Create(ctx, backup.Options{
		DataDir: cfg.DataDir, OwnerURL: src.ownerURL, BinDir: src.binDir, Embedded: src.embedded,
		Out: target, Passphrase: pass, Version: version, Commit: commit, Log: log,
	})
	if err != nil {
		return err
	}
	printBackup(res)
	if *keep > 0 {
		removed, err := backup.Prune(filepath.Dir(target), *prefix, *keep)
		if err != nil {
			return fmt.Errorf("pulizia dei backup vecchi: %w", err)
		}
		for _, n := range removed {
			log.Info("backup vecchio rimosso", "file", n)
		}
	}
	return nil
}

func printBackup(res *backup.Result) {
	m := res.Manifest
	cifr := "no"
	if res.Encrypted {
		cifr = "sì (age, passphrase)"
	}
	fmt.Printf("Backup scritto: %s\n  dimensione: %s\n  cifrato: %s\n  Regia %s (%s), Postgres %s, ultima migrazione %s\n  chiavi: %d, allegati: %d\n",
		res.Path, humanBytes(res.Bytes), cifr, m.Version, m.Commit, m.Postgres.Server, m.LastMigration(), res.Keys, res.Files)
	if res.Keys == 0 {
		fmt.Println("  ATTENZIONE: nessuna chiave in keys/: i segreti MFA e dei webhook non saranno decifrabili dopo il ripristino")
	}
}

func cmdRestore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	in := fs.String("in", "", "file di backup da ripristinare")
	check := fs.Bool("check", false, "verifica l'archivio (checksum e compatibilità) senza ripristinare")
	force := fs.Bool("force", false, "sposta da parte database, chiavi e allegati esistenti in <data>/pre-restore-<data e ora>/")
	passFile := fs.String("passphrase-file", "", "passphrase di un backup cifrato, letta da file")
	cfg, err := config.Load(fs, args)
	if err != nil {
		return err
	}
	if *in == "" {
		return errors.New("uso: regia restore --in backup.tar[.age] --data cartella-nuova [--force] [--check]")
	}
	log := newLogger(cfg)
	known, err := db.Versions(migrations.FS)
	if err != nil {
		return err
	}
	pass := ""
	if enc, err := backup.Encrypted(*in); err != nil {
		return err
	} else if enc {
		if pass, err = restorePassphrase(*passFile); err != nil {
			return err
		}
	}

	if *check {
		s, err := backup.Verify(*in, pass)
		if err != nil {
			return err
		}
		m := s.Manifest
		fmt.Printf("Backup integro: %s\n  creato il %s da Regia %s (%s), modalità %s\n  Postgres %s, ultima migrazione %s, estensioni %s\n  dump: %s, chiavi: %d, allegati: %d\n",
			*in, m.CreatedAt.Format(time.RFC3339), m.Version, m.Commit, m.Mode, m.Postgres.Server, m.LastMigration(),
			strings.Join(m.Extensions, ", "), humanBytes(s.DumpBytes), s.Keys, s.Files)
		if err := backup.Compatible(m, known); err != nil {
			return err
		}
		fmt.Println("  compatibile con questa versione: sì")
		return nil
	}
	// Compatibilità prima di toccare qualcosa (initdb, cartelle).
	m, err := backup.Check(*in, pass, known)
	if err != nil {
		return err
	}

	opts := backup.RestoreOptions{In: *in, Passphrase: pass, DataDir: cfg.DataDir, Known: known, Force: *force, Log: log}
	var s *backup.Summary
	if cfg.Embedded() {
		s, err = restoreEmbedded(ctx, cfg, opts, log)
	} else {
		opts.OwnerURL, opts.BinDir = cfg.DatabaseURL, cfg.PGBinDir
		s, err = backup.Restore(ctx, opts)
	}
	if err != nil {
		return err
	}
	pending := 0
	for _, v := range known {
		if !slices.Contains(m.Migrations, v) {
			pending++
		}
	}
	fmt.Printf("Ripristino completato in %s\n  backup del %s (Regia %s, ultima migrazione %s)\n  chiavi: %d, allegati: %d\n",
		cfg.DataDir, m.CreatedAt.Format(time.RFC3339), m.Version, m.LastMigration(), s.Keys, s.Files)
	if pending > 0 {
		fmt.Printf("  al prossimo avvio si applicheranno %d migrazioni\n", pending)
	}
	fmt.Println("  prossimo passo: regia doctor, poi regia serve")
	return nil
}

func restoreEmbedded(ctx context.Context, cfg config.Config, o backup.RestoreOptions, log *slog.Logger) (*backup.Summary, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	pg, err := embeddedpg.New(cfg.DataDir, cfg.PGBinDir, log)
	if err != nil {
		return nil, err
	}
	if pg.Initialized() {
		if pg.Running(ctx) {
			return nil, errors.New("Postgres incluso è in esecuzione su questa cartella dati: ferma prima il servizio")
		}
		if err := backup.PrepareDir(cfg.DataDir, "pg", o.Force, log); err != nil {
			return nil, fmt.Errorf("la cartella dati contiene già un database: %w", err)
		}
	}
	if err := pg.Start(ctx); err != nil {
		return nil, err
	}
	o.OwnerURL, o.BinDir, o.Embedded = pg.OwnerURL(), pg.BinDir(), true
	s, rerr := backup.Restore(ctx, o)
	if err := pg.Stop(30 * time.Second); err != nil {
		log.Warn(err.Error())
	}
	if rerr != nil {
		// Il cluster l'ha creato questo ripristino: si toglie, così si può riprovare.
		_ = os.RemoveAll(filepath.Join(cfg.DataDir, "pg"))
		return nil, rerr
	}
	return s, nil
}

// backupSource è il database da salvare, con il Postgres incluso avviato (o agganciato) se serve.
type backupSource struct {
	ownerURL, binDir string
	embedded         bool
	pg               *embeddedpg.Server
}

func (s *backupSource) close() {
	if s.pg != nil {
		_ = s.pg.Stop(20 * time.Second)
	}
}

// openBackupSource non applica migrazioni: un backup deve fotografare il database com'è.
func openBackupSource(ctx context.Context, cfg config.Config, log *slog.Logger) (*backupSource, error) {
	if !cfg.Embedded() {
		return &backupSource{ownerURL: cfg.DatabaseURL, binDir: cfg.PGBinDir}, nil
	}
	pg, err := embeddedpg.New(cfg.DataDir, cfg.PGBinDir, log)
	if err != nil {
		return nil, err
	}
	if !pg.Initialized() {
		return nil, fmt.Errorf("nessun database in %s: niente da salvare", filepath.Join(cfg.DataDir, "pg"))
	}
	if err := pg.Start(ctx); err != nil {
		return nil, err
	}
	return &backupSource{ownerURL: pg.OwnerURL(), binDir: pg.BinDir(), embedded: true, pg: pg}, nil
}

// autoBackup fa un backup prima di applicare migrazioni nuove a un database già in uso.
// Senza riuscire a farlo l'avvio si ferma: aggiornare senza un punto di ritorno è peggio
// che non aggiornare. Si spegne con REGIA_BACKUP_BEFORE_MIGRATE=false.
func autoBackup(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, ownerURL, binDir string, embedded bool, log *slog.Logger) error {
	bs, err := config.BackupSettings(cfg.DataDir)
	if err != nil || !bs.BeforeMigrate {
		return err
	}
	pass, err := bs.Passphrase()
	if err != nil {
		return err
	}
	auto, err := backup.BeforeMigrate(ctx, pool, migrations.FS, backup.Options{
		DataDir: cfg.DataDir, OwnerURL: ownerURL, BinDir: binDir, Embedded: embedded,
		Passphrase: pass, Version: version, Commit: commit, Log: log,
	}, bs.Dir, bs.Keep)
	if auto != nil && auto.Result != nil {
		log.Info("backup automatico scritto", "file", auto.Result.Path, "dimensione", humanBytes(auto.Result.Bytes), "allegati", auto.Result.Files)
		for _, n := range auto.Removed {
			log.Info("backup automatico vecchio rimosso", "file", n)
		}
		if err != nil { // solo la pulizia dei vecchi è fallita: il backup c'è
			log.Warn("pulizia dei backup automatici vecchi", "errore", err)
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("backup automatico prima delle migrazioni: %w (per avviare senza: REGIA_BACKUP_BEFORE_MIGRATE=false)", err)
	}
	return nil
}

func backupPassphrase(file string, ask bool, bs config.Backup) (string, error) {
	switch {
	case file != "":
		return config.ReadPassphraseFile(file)
	case ask:
		p, err := askSecret("Passphrase del backup (min. 12 caratteri): ")
		if err != nil {
			return "", err
		}
		if len(p) < config.MinPassphrase {
			return "", fmt.Errorf("passphrase troppo corta: servono almeno %d caratteri", config.MinPassphrase)
		}
		again, err := askSecret("Ripeti la passphrase: ")
		if err != nil {
			return "", err
		}
		if again != p {
			return "", errors.New("le due passphrase non coincidono")
		}
		return p, nil
	default:
		return bs.Passphrase()
	}
}

func restorePassphrase(file string) (string, error) {
	if file != "" {
		return config.ReadPassphraseFile(file)
	}
	bs, err := config.BackupSettings(".")
	if err != nil {
		return "", err
	}
	if p, err := bs.Passphrase(); err != nil || p != "" {
		return p, err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("il backup è cifrato: indica la passphrase con --passphrase-file o REGIA_BACKUP_PASSPHRASE_FILE")
	}
	return askSecret("Passphrase del backup: ")
}

// askSecret chiede un segreto a terminale senza mostrarlo (o lo legge da stdin negli script).
func askSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
