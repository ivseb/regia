// Comando regia: un solo binario per server, worker, migrazioni e amministrazione.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"regia/internal/platform/config"
)

// Valorizzate in fase di build con -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "sconosciuto"
)

const usage = `regia: piattaforma per demand, delivery, conformità e architettura.

Uso:
  regia serve            avvia server HTTP e worker (default)
  regia migrate          applica le migrazioni ed esce
  regia tenant create    crea un tenant con il suo primo amministratore
  regia tenant provision configura i moduli in un tenant esistente
  regia member add       aggiunge una persona a un tenant
  regia platform-admin   dà o toglie il ruolo di superamministratore (grant|revoke --email ...)
  regia backup           backup completo: database, chiavi e allegati (--out file o --dir cartella)
  regia restore          ripristina un backup in una cartella dati nuova (--in file, --check per verificarlo)
  regia doctor           controlla cartella dati, Postgres, migrazioni, chiavi, disco e orologio
  regia version          mostra la versione

Opzioni comuni: --data, --pg-bin, --http, --dev (anche via REGIA_*; DATABASE_URL per un Postgres esterno).
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "errore:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "version":
		fmt.Printf("regia %s (%s)\n", version, commit)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "serve":
		return cmdServe(ctx, args)
	case "migrate":
		return cmdMigrate(ctx, args)
	case "tenant":
		return cmdTenant(ctx, args)
	case "member":
		return cmdMember(ctx, args)
	case "platform-admin":
		return cmdPlatformAdmin(ctx, args)
	case "bench":
		return cmdBench(ctx, args)
	case "backup":
		return cmdBackup(ctx, args)
	case "restore":
		return cmdRestore(ctx, args)
	case "doctor":
		return cmdDoctor(ctx, args)
	case "dev":
		// Strumenti di sviluppo, non documentati nell'uso (dev.go).
		return cmdDev(ctx, args)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("comando sconosciuto: %s", cmd)
	}
}

func cmdMigrate(ctx context.Context, args []string) error {
	cfg, err := config.Load(flag.NewFlagSet("migrate", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	rt, err := start(ctx, cfg, log)
	if err != nil {
		return err
	}
	rt.close()
	log.Info("database aggiornato")
	return nil
}
