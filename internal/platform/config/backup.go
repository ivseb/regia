package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Backup sono le impostazioni dei backup (piano, passo 17; docs/operativo.md).
type Backup struct {
	// Dir è la cartella dei backup automatici e di quelli con --dir (REGIA_BACKUP_DIR,
	// default <data>/backups). Meglio su un disco diverso da quello dei dati.
	Dir string
	// BeforeMigrate fa un backup prima di applicare migrazioni nuove a un database già in uso
	// (REGIA_BACKUP_BEFORE_MIGRATE, default true).
	BeforeMigrate bool
	// Keep è quanti backup automatici tenere (REGIA_BACKUP_KEEP, default 5; 0 = tutti).
	Keep int
	// PassphraseFile cifra i backup con la passphrase contenuta nel file
	// (REGIA_BACKUP_PASSPHRASE_FILE). In alternativa REGIA_BACKUP_PASSPHRASE.
	PassphraseFile string
}

// BackupSettings legge le impostazioni dei backup dall'ambiente.
func BackupSettings(dataDir string) (Backup, error) {
	b := Backup{
		Dir:            env("REGIA_BACKUP_DIR", filepath.Join(dataDir, "backups")),
		BeforeMigrate:  envBool("REGIA_BACKUP_BEFORE_MIGRATE", true),
		Keep:           5,
		PassphraseFile: os.Getenv("REGIA_BACKUP_PASSPHRASE_FILE"),
	}
	if v := os.Getenv("REGIA_BACKUP_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return b, fmt.Errorf("REGIA_BACKUP_KEEP non valido: %q", v)
		}
		b.Keep = n
	}
	abs, err := filepath.Abs(b.Dir)
	if err != nil {
		return b, err
	}
	b.Dir = abs
	return b, nil
}

// MinPassphrase è la lunghezza minima della passphrase dei backup.
const MinPassphrase = 12

// Passphrase restituisce la passphrase configurata per i backup ("" = backup in chiaro).
func (b Backup) Passphrase() (string, error) {
	if b.PassphraseFile != "" {
		return ReadPassphraseFile(b.PassphraseFile)
	}
	p := os.Getenv("REGIA_BACKUP_PASSPHRASE")
	if p != "" && len(p) < MinPassphrase {
		return "", fmt.Errorf("REGIA_BACKUP_PASSPHRASE: servono almeno %d caratteri", MinPassphrase)
	}
	return p, nil
}

// ReadPassphraseFile legge una passphrase da file (senza l'a capo finale).
func ReadPassphraseFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file della passphrase: %w", err)
	}
	p := strings.TrimRight(string(raw), "\r\n")
	if p == "" {
		return "", errors.New("il file della passphrase è vuoto")
	}
	if len(p) < MinPassphrase {
		return "", fmt.Errorf("passphrase troppo corta: servono almeno %d caratteri", MinPassphrase)
	}
	return p, nil
}
