package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"regia/internal/devtools/fakegitlab"
)

// cmdDevGitLab avvia un finto GitLab con dati di esempio (regia dev fake-gitlab), per
// provare il connettore DevSecOps senza un GitLab vero.
func cmdDevGitLab(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev fake-gitlab", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8174", "indirizzo di ascolto")
	token := fs.String("token", "glpat-finto-sviluppo", "token di gruppo accettato")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := fakegitlab.New()
	s.AddToken(*token, "Regia (lettura)", "read_api")
	fakegitlab.Demo(s, time.Now())
	fmt.Printf(`Finto GitLab in ascolto su http://%s (dati inventati, gruppo "esempio").

Per provarlo in Regia (serve --dev: la protezione SSRF accetta http e localhost):
  URL base   http://%s
  Gruppo     esempio   (con il sottogruppo esempio/negozio)
  Token      %s   (scope read_api)
  Profilo    «GitLab CI con SBOM e scansioni»

Ctrl+C per fermarlo.
`, *addr, *addr, *token)
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
