package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"regia/internal/platform/db"
)

func levels(r *Report) map[string]Level {
	out := map[string]Level{}
	for _, c := range r.Checks {
		if l, ok := out[c.Area]; !ok || c.Level > l {
			out[c.Area] = c.Level
		}
	}
	return out
}

func TestDataDirAndKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Report{}
	DataDir(r, dir)
	Keys(r, dir, 0, false)
	if r.Failed() || levels(r)["cartella"] != OK || levels(r)["chiavi"] != Info {
		t.Fatalf("cartella nuova:\n%+v", r.Checks)
	}

	// Segreti nel database ma chiave assente: errore.
	r = &Report{}
	Keys(r, dir, 3, false)
	if !r.Failed() || !strings.Contains(r.Checks[0].Msg, "3 segreti cifrati") {
		t.Fatalf("chiave mancante: %+v", r.Checks)
	}

	// Permessi larghi e chiave della lunghezza sbagliata.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "keys")
	_ = os.MkdirAll(keys, 0o700)
	_ = os.WriteFile(filepath.Join(keys, "secrets.key"), []byte("corta"), 0o600)
	r = &Report{}
	DataDir(r, dir)
	Keys(r, dir, 1, false)
	if levels(r)["cartella"] != Warn || levels(r)["chiavi"] != Fail {
		t.Fatalf("permessi e chiave corta: %+v", r.Checks)
	}
	_ = os.WriteFile(filepath.Join(keys, "secrets.key"), make([]byte, 32), 0o600)
	_ = os.Chmod(filepath.Join(keys, "secrets.key"), 0o644)
	r = &Report{}
	Keys(r, dir, 1, false)
	if levels(r)["chiavi"] != Warn {
		t.Fatalf("chiave leggibile da altri: %+v", r.Checks)
	}

	r = &Report{}
	DataDir(r, filepath.Join(dir, "non-esiste"))
	if !r.Failed() {
		t.Fatal("cartella inesistente accettata")
	}
}

func TestMigrationsAndPostgres(t *testing.T) {
	r := &Report{}
	Migrations(r, db.MigrationStatus{Applied: []string{"0001"}, Pending: []string{"0002"}})
	if r.Failed() || !strings.Contains(r.Checks[0].Msg, "1 da applicare") {
		t.Fatalf("in attesa: %+v", r.Checks)
	}
	r = &Report{}
	Migrations(r, db.MigrationStatus{Applied: []string{"0001"}, Modified: []string{"0002"}})
	if !r.Failed() {
		t.Fatal("migrazione modificata accettata")
	}
	r = &Report{}
	Migrations(r, db.MigrationStatus{Applied: []string{"0001"}, Unknown: []string{"0009"}})
	if r.Failed() || levels(r)["migrazioni"] != Warn {
		t.Fatalf("database più recente (un rilascio indietro si può): %+v", r.Checks)
	}

	r = &Report{}
	Postgres(r, 18, 17, 16, 18)
	if !r.Failed() || !strings.Contains(r.Checks[1].Msg, "pg_upgrade") {
		t.Fatalf("cluster di un'altra versione: %+v", r.Checks)
	}
	r = &Report{}
	Postgres(r, 15, 0, 16, 18)
	if !r.Failed() {
		t.Fatal("Postgres 15 accettato")
	}
}

func TestClockDiskBackups(t *testing.T) {
	now := time.Now()
	r := &Report{}
	Clock(r, now, now.Add(-45*time.Second))
	if !r.Failed() {
		t.Fatal("orologio sfasato di 45 s accettato")
	}
	r = &Report{}
	Clock(r, now, now.Add(time.Second))
	if r.Failed() {
		t.Fatalf("orologio allineato: %+v", r.Checks)
	}

	r = &Report{}
	Disk(r, "disco", t.TempDir())
	if len(r.Checks) != 1 || !strings.Contains(r.Checks[0].Msg, "GiB liberi") {
		t.Fatalf("disco: %+v", r.Checks)
	}

	dir := t.TempDir()
	r = &Report{}
	Backups(r, dir, now)
	if levels(r)["backup"] != Warn {
		t.Fatal("nessun backup: attesa un'avvertenza")
	}
	name := filepath.Join(dir, "regia-20261124T023000Z.tar")
	_ = os.WriteFile(name, nil, 0o600)
	old := now.Add(-72 * time.Hour)
	_ = os.Chtimes(name, old, old)
	r = &Report{}
	Backups(r, dir, now)
	if levels(r)["backup"] != Warn || !strings.Contains(r.Checks[0].Msg, "fa") {
		t.Fatalf("backup vecchio: %+v", r.Checks)
	}
	_ = os.Chtimes(name, now, now)
	r = &Report{}
	Backups(r, dir, now)
	if levels(r)["backup"] != OK {
		t.Fatalf("backup recente: %+v", r.Checks)
	}
}
