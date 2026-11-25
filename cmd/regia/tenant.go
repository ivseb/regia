package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"regia/internal/api"
	"regia/internal/platform/config"
	"regia/internal/platform/tenant"
)

func cmdTenant(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "provision" {
		return cmdTenantProvision(ctx, args[1:])
	}
	if len(args) == 0 || args[0] != "create" {
		return errors.New("uso: regia tenant create --slug acme --name \"ACME S.p.A.\" --admin-email nome@dominio | regia tenant provision --slug acme")
	}
	fs := flag.NewFlagSet("tenant create", flag.ContinueOnError)
	slug := fs.String("slug", "", "identificativo del tenant (minuscole, cifre, trattini)")
	name := fs.String("name", "", "nome dell'organizzazione")
	email := fs.String("admin-email", "", "email del primo amministratore")
	adminName := fs.String("admin-name", "", "nome visualizzato dell'amministratore")
	currency := fs.String("currency", "EUR", "valuta base (ISO 4217)")
	language := fs.String("language", "", "lingua del tenant e dei suoi dati di partenza: it o en (vuoto = italiano)")
	platformAdmin := fs.Bool("platform-admin", false, "rende superamministratore dell'istanza anche l'account dell'amministratore")
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

	// La password non passa mai dalla riga di comando (finirebbe nella history):
	// variabile d'ambiente per gli script, altrimenti richiesta a terminale.
	pw := os.Getenv("REGIA_ADMIN_PASSWORD")
	in := tenant.CreateInput{Slug: *slug, Name: *name, AdminEmail: *email, AdminName: *adminName, BaseCurrency: strings.ToUpper(*currency), Language: *language,
		Provision: api.Provisioners()}
	in.AdminPassword = pw
	out, err := tenant.Create(ctx, rt.db, in)
	if err != nil && pw == "" && strings.Contains(err.Error(), "serve una password") {
		if in.AdminPassword, err = askPassword(); err != nil {
			return err
		}
		out, err = tenant.Create(ctx, rt.db, in)
	}
	if err != nil {
		return err
	}
	if *platformAdmin {
		if err := tenant.SetPlatformAdmin(ctx, rt.db, *email, true); err != nil {
			return err
		}
		fmt.Printf("%s è ora superamministratore dell'istanza\n", *email)
	}
	fmt.Printf("Tenant creato: %s (%s)\n", *name, out.TenantID)
	if out.NewAccount {
		fmt.Printf("Nuovo account amministratore: %s\n", *email)
	} else {
		fmt.Printf("Account esistente aggiunto come amministratore: %s\n", *email)
	}
	return nil
}

func askPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password iniziale dell'amministratore (min. 12 caratteri): ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func cmdTenantProvision(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tenant provision", flag.ContinueOnError)
	slug := fs.String("slug", "", "identificativo del tenant")
	cfg, err := config.Load(fs, args)
	if err != nil {
		return err
	}
	rt, err := start(ctx, cfg, newLogger(cfg))
	if err != nil {
		return err
	}
	defer rt.close()
	if err := tenant.Provision(ctx, rt.db, *slug, api.Provisioners()); err != nil {
		return err
	}
	fmt.Printf("Moduli configurati nel tenant %s\n", *slug)
	return nil
}

// cmdPlatformAdmin dà o toglie il ruolo di superamministratore dell'istanza: chi lo ha crea
// le organizzazioni dall'interfaccia (Organizzazioni). L'account deve già esistere.
func cmdPlatformAdmin(ctx context.Context, args []string) error {
	const uso = "uso: regia platform-admin grant|revoke --email nome@dominio"
	if len(args) == 0 || (args[0] != "grant" && args[0] != "revoke") {
		return errors.New(uso)
	}
	fs := flag.NewFlagSet("platform-admin", flag.ContinueOnError)
	email := fs.String("email", "", "email dell'account")
	cfg, err := config.Load(fs, args[1:])
	if err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return errors.New(uso)
	}
	rt, err := start(ctx, cfg, newLogger(cfg))
	if err != nil {
		return err
	}
	defer rt.close()
	on := args[0] == "grant"
	if err := tenant.SetPlatformAdmin(ctx, rt.db, *email, on); err != nil {
		return err
	}
	if on {
		fmt.Printf("%s è ora superamministratore dell'istanza\n", *email)
	} else {
		fmt.Printf("%s non è più superamministratore dell'istanza\n", *email)
	}
	return nil
}
