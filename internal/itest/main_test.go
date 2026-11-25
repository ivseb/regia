// Package itest contiene i test di integrazione: girano su un Postgres incluso
// avviato in una cartella temporanea. Se i binari di Postgres non ci sono, si saltano.
package itest

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"regia/internal/platform/db"
	"regia/internal/platform/embeddedpg"
	"regia/migrations"
)

var (
	testDB  *db.DB
	skipMsg string
	// Cartella dati e Postgres incluso dei test (backup e ripristino, operativo_test.go).
	testDataDir string
	testPG      *embeddedpg.Server
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dir, err := os.MkdirTemp("", "regia-itest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	testDataDir = dir

	pg, err := embeddedpg.New(dir, os.Getenv("REGIA_PG_BIN"), log)
	// In CI REGIA_ITEST_REQUIRE_PG=18 trasforma il salto in un errore: i test di
	// integrazione devono girare davvero, con quella versione di Postgres.
	if req := os.Getenv("REGIA_ITEST_REQUIRE_PG"); req != "" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "REGIA_ITEST_REQUIRE_PG: Postgres non disponibile:", err)
			return 1
		}
		if want, _ := strconv.Atoi(req); pg.Major() < want {
			fmt.Fprintf(os.Stderr, "REGIA_ITEST_REQUIRE_PG: trovato Postgres %d, richiesto %s\n", pg.Major(), req)
			return 1
		}
	}
	if err != nil {
		skipMsg = "Postgres non disponibile: " + err.Error()
		return m.Run()
	}
	if err := pg.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer pg.Stop(10 * time.Second) //nolint:errcheck
	testPG = pg

	owner, err := db.Open(ctx, pg.OwnerURL(), pg.OwnerURL())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := db.Migrate(ctx, owner.Owner, migrations.FS, log); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	owner.Close()
	testDB, err = db.Open(ctx, pg.OwnerURL(), pg.AppURL())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer testDB.Close()
	return m.Run()
}

func needDB(t *testing.T) *db.DB {
	t.Helper()
	if testDB == nil {
		t.Skip(skipMsg)
	}
	return testDB
}
