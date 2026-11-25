package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"regia/internal/bench"
	"regia/internal/platform/config"
)

// cmdBench: strumenti di sviluppo per misurare le prestazioni (architecture.md §7).
func cmdBench(ctx context.Context, args []string) error {
	usage := errors.New("uso: regia bench seed --data ./data-bench --tenant bench --elements 1000000 [--only-requests] | regia bench load --url http://127.0.0.1:8090 --tenant bench --elements 1000000 [--duration 60s --concurrency 8 --pid N --only board,backlog --only-requests]")
	if len(args) == 0 {
		return usage
	}
	pw := os.Getenv("REGIA_ADMIN_PASSWORD")
	if pw == "" {
		return errors.New("imposta REGIA_ADMIN_PASSWORD (password dell'amministratore del tenant di prova)")
	}
	switch args[0] {
	case "seed":
		fs := flag.NewFlagSet("bench seed", flag.ContinueOnError)
		slug := fs.String("tenant", "bench", "tenant di prova")
		n := fs.Int("elements", 1_000_000, "elementi totali del tenant (40% business request, il resto dei moduli)")
		onlyReq := fs.Bool("only-requests", false, "solo business request, come le prime misure (niente moduli)")
		batch := fs.Int("batch", 50_000, "elementi per lotto di COPY (meno su macchine cariche: ogni COPY ha 15 s)")
		cfg, err := config.Load(fs, args[1:])
		if err != nil {
			return err
		}
		log := newLogger(cfg)
		rt, err := start(ctx, cfg, log)
		if err != nil {
			return err
		}
		defer rt.close()
		t0 := time.Now()
		if err := bench.Seed(ctx, rt.db, bench.SeedOptions{Slug: *slug, Elements: *n, Password: pw, Batch: *batch, OnlyRequests: *onlyReq}, log); err != nil {
			return err
		}
		fmt.Printf("Caricati %d elementi in %s\n", *n, time.Since(t0).Round(time.Second))
		return nil
	case "load":
		fs := flag.NewFlagSet("bench load", flag.ContinueOnError)
		base := fs.String("url", "http://127.0.0.1:8090", "indirizzo del server")
		slug := fs.String("tenant", "bench", "tenant di prova")
		n := fs.Int("elements", 1_000_000, "elementi del tenant")
		dur := fs.Duration("duration", 60*time.Second, "durata")
		conc := fs.Int("concurrency", 8, "richieste in parallelo")
		pid := fs.Int("pid", 0, "pid del server, per misurarne la memoria")
		onlyReq := fs.Bool("only-requests", false, "tenant con sole business request (niente scenari dei moduli)")
		only := fs.String("only", "", "solo gli scenari che contengono queste parole, separate da virgola")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		rep, err := bench.Load(ctx, bench.LoadOptions{BaseURL: *base, Email: "admin@" + *slug + ".test", Password: pw,
			Duration: *dur, Concurrency: *conc, Elements: *n, PID: *pid, OnlyRequests: *onlyReq, Only: splitList(*only)})
		if err != nil {
			return err
		}
		if !rep.Print(os.Stdout) {
			return errors.New("budget di prestazioni non rispettati")
		}
		return nil
	}
	return usage
}
