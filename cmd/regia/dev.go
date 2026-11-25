package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"regia/internal/devtools/fakegithub"
)

// cmdDev raccoglie gli strumenti di sviluppo (non compaiono nell'uso):
//
//	regia dev fake-github   un finto GitHub con dati di esempio, per provare il
//	                          connettore DevSecOps senza un GitHub vero
func cmdDev(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "fake-gitlab" { // un finto GitLab: dev_gitlab.go
		return cmdDevGitLab(ctx, args[1:])
	}
	if len(args) == 0 || args[0] != "fake-github" {
		return errors.New("uso: regia dev fake-github [--addr 127.0.0.1:8122] [--enterprise] [--write] [--key chiave.pem]\n" +
			"     regia dev fake-gitlab [--addr 127.0.0.1:8174] [--token glpat-…]")
	}
	fs := flag.NewFlagSet("dev fake-github", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8122", "indirizzo di ascolto")
	enterprise := fs.Bool("enterprise", false, "si comporta come GitHub Enterprise Server (API sotto /api/v3)")
	keyOut := fs.String("key", "fake-github-app.pem", "dove scrivere la chiave privata della finta GitHub App")
	token := fs.String("token", "ghp_finto_sviluppo", "token personale accettato")
	write := fs.Bool("write", false, "la finta GitHub App ha anche i permessi facoltativi in scrittura (adozione guidata: pull request)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	s := fakegithub.New()
	s.PATs[*token] = true
	if *enterprise {
		s.BasePath = "/api/v3"
		s.Enterprise = "3.15.0"
	}
	fakegithub.Demo(s, time.Now())
	if *write {
		s.GrantWrite()
	}
	if err := os.MkdirAll(filepath.Dir(*keyOut), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(*keyOut, []byte(s.PrivateKeyPEM()), 0o600); err != nil {
		return err
	}
	base := "http://" + *addr + s.BasePath
	fmt.Printf(`Finto GitHub in ascolto su http://%s (dati inventati, organizzazione "esempio").

Per provarlo in Regia (serve --dev: la protezione SSRF accetta http e localhost):
  URL base        %s
  Organizzazione  esempio
  GitHub App      App ID %s, installation ID %s, chiave privata in %s
  oppure token    %s
  Repository GitOps  esempio/negozio-gitops (profilo "Template GitHub Actions + GitOps (Argo CD)")

Ctrl+C per fermarlo.
`, *addr, base, s.AppID, s.InstallationID, *keyOut, *token)
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
