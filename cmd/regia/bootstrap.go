package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/embeddedpg"
	"regia/migrations"
)

// runtime è ciò che serve a ogni sottocomando che usa il database.
type runtime struct {
	cfg config.Config
	log *slog.Logger
	db  *db.DB
	pg  *embeddedpg.Server // nil in modalità esterna
}

// start avvia (se serve) il Postgres incluso, apre i pool e applica le migrazioni.
func start(ctx context.Context, cfg config.Config, log *slog.Logger) (*runtime, error) {
	rt := &runtime{cfg: cfg, log: log}
	ownerURL, appURL := cfg.DatabaseURL, cfg.AppDatabaseURL
	if cfg.Embedded() {
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return nil, fmt.Errorf("cartella dati: %w", err)
		}
		pg, err := embeddedpg.New(cfg.DataDir, cfg.PGBinDir, log)
		if err != nil {
			return nil, err
		}
		if err := pg.Start(ctx); err != nil {
			return nil, err
		}
		rt.pg = pg
		ownerURL, appURL = pg.OwnerURL(), pg.AppURL()
	}

	// Le migrazioni girano col ruolo di piattaforma prima di aprire il pool applicativo:
	// al primo avvio il ruolo mytrack_app nasce proprio da una migrazione.
	ownerOnly, err := db.Open(ctx, ownerURL, ownerURL)
	if err != nil {
		rt.close()
		return nil, err
	}
	// Backup automatico prima di migrazioni nuove su un database già in uso (piano, passo 17).
	binDir := cfg.PGBinDir
	if rt.pg != nil {
		binDir = rt.pg.BinDir()
	}
	if err := autoBackup(ctx, cfg, ownerOnly.Owner, ownerURL, binDir, rt.pg != nil, log); err != nil {
		ownerOnly.Close()
		rt.close()
		return nil, err
	}
	n, err := db.Migrate(ctx, ownerOnly.Owner, migrations.FS, log)
	ownerOnly.Close()
	if err != nil {
		rt.close()
		return nil, err
	}
	if n > 0 {
		log.Info("migrazioni applicate", "numero", n)
	}

	rt.db, err = db.Open(ctx, ownerURL, appURL)
	if err != nil {
		rt.close()
		return nil, err
	}
	return rt, nil
}

func (rt *runtime) close() {
	if rt.db != nil {
		rt.db.Close()
	}
	if rt.pg != nil {
		if err := rt.pg.Stop(20 * time.Second); err != nil {
			rt.log.Warn(err.Error())
		}
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Dev {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
