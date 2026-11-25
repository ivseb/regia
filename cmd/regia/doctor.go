package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"regia/internal/platform/config"
	"regia/internal/platform/doctor"
	"regia/internal/platform/embeddedpg"
	"regia/migrations"
)

// cmdDoctor controlla l'istanza senza modificarla (piano, passo 17; docs/operativo.md).
// Con il Postgres incluso fermo lo avvia per il tempo dei controlli, senza initdb e
// senza applicare migrazioni.
func cmdDoctor(ctx context.Context, args []string) error {
	cfg, err := config.Load(flag.NewFlagSet("doctor", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	// I messaggi di avvio di Postgres non servono qui: solo gli errori.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bs, err := config.BackupSettings(cfg.DataDir)
	if err != nil {
		return err
	}
	r := &doctor.Report{}
	fmt.Printf("regia %s (%s): controllo di %s\n\n", version, commit, cfg.DataDir)
	doctor.DataDir(r, cfg.DataDir)
	doctor.Disk(r, "disco", cfg.DataDir)
	if bs.Dir != cfg.DataDir && !isUnder(bs.Dir, cfg.DataDir) {
		if _, err := os.Stat(bs.Dir); err == nil {
			doctor.Disk(r, "disco", bs.Dir)
		}
	}

	ownerURL, binDir := cfg.DatabaseURL, cfg.PGBinDir
	if cfg.Embedded() {
		pg, err := embeddedpg.New(cfg.DataDir, cfg.PGBinDir, log)
		if err != nil {
			r.Checks = append(r.Checks, doctor.Check{Area: "postgres", Level: doctor.Fail, Msg: err.Error()})
			return finish(r)
		}
		dataMajor, err := embeddedpg.DataMajor(cfg.DataDir)
		if err != nil {
			r.Checks = append(r.Checks, doctor.Check{Area: "postgres", Level: doctor.Fail, Msg: "PG_VERSION: " + err.Error()})
		}
		doctor.Postgres(r, pg.Major(), dataMajor, embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
		binDir = pg.BinDir()
		if !pg.Initialized() || (dataMajor != 0 && dataMajor != pg.Major()) {
			doctor.Tools(r, binDir, 0)
			doctor.Keys(r, cfg.DataDir, -1, cfg.OIDC.Enabled())
			doctor.Backups(r, bs.Dir, time.Now())
			return finish(r)
		}
		if err := pg.Start(ctx); err != nil {
			r.Checks = append(r.Checks, doctor.Check{Area: "postgres", Level: doctor.Fail, Msg: "avvio: " + err.Error()})
			return finish(r)
		}
		defer pg.Stop(20 * time.Second) //nolint:errcheck
		ownerURL = pg.OwnerURL()
	}
	pool, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		r.Checks = append(r.Checks, doctor.Check{Area: "postgres", Level: doctor.Fail, Msg: "connessione: " + err.Error()})
		return finish(r)
	}
	defer pool.Close()
	major := doctor.Database(ctx, r, pool, migrations.FS, cfg.DataDir, cfg.OIDC.Enabled(), embeddedpg.MinMajor, embeddedpg.RecommendedMajor)
	doctor.Tools(r, binDir, major)
	doctor.Backups(r, bs.Dir, time.Now())
	return finish(r)
}

func finish(r *doctor.Report) error {
	r.Print(os.Stdout)
	if r.Failed() {
		return errors.New("ci sono controlli non superati")
	}
	return nil
}

func isUnder(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != "../")
}
