package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"regia/internal/platform/config"
	"regia/internal/platform/tenant"
)

func cmdMember(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New(`uso: regia member add --tenant acme --email nome@dominio --name "Nome Cognome" [--type requester] [--roles editor,finance] [--groups "PMO,Demand management"]`)
	}
	fs := flag.NewFlagSet("member add", flag.ContinueOnError)
	slug := fs.String("tenant", "", "identificativo del tenant")
	email := fs.String("email", "", "email della persona")
	name := fs.String("name", "", "nome visualizzato")
	typ := fs.String("type", "standard", "standard | requester | guest | auditor")
	roles := fs.String("roles", "viewer", "ruoli di tenant separati da virgola")
	groups := fs.String("groups", "", "gruppi separati da virgola")
	cfg, err := config.Load(fs, args[1:])
	if err != nil {
		return err
	}
	rt, err := start(ctx, cfg, newLogger(cfg))
	if err != nil {
		return err
	}
	defer rt.close()
	in := tenant.MemberInput{TenantSlug: *slug, Email: *email, Name: *name, MemberType: *typ,
		Roles: splitList(*roles), Groups: splitList(*groups), Password: os.Getenv("REGIA_MEMBER_PASSWORD")}
	id, err := tenant.AddMember(ctx, rt.db, in)
	if err != nil && in.Password == "" && strings.Contains(err.Error(), "password") {
		if in.Password, err = askPassword(); err != nil {
			return err
		}
		id, err = tenant.AddMember(ctx, rt.db, in)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Membro aggiunto: %s (%s)\n", *email, id)
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
