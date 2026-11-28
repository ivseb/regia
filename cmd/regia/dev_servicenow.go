package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"regia/internal/devtools/fakeservicenow"
)

// cmdDevServiceNow avvia un finto ServiceNow con dati inventati (regia dev
// fake-servicenow), per provare il connettore DevSecOps senza un'istanza vera.
func cmdDevServiceNow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev fake-servicenow", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8190", "indirizzo di ascolto")
	user := fs.String("user", "regia.lettura", "utente di servizio (Basic)")
	password := fs.String("password", "finta-password-sviluppo", "password dell'utente di servizio")
	clientID := fs.String("client-id", "regia-finto", "id del client OAuth (client credentials)")
	clientSecret := fs.String("client-secret", "finto-segreto-sviluppo", "segreto del client OAuth")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := fakeservicenow.New()
	s.AddUser(*user, *password)
	s.AddClient(*clientID, *clientSecret)
	fakeservicenow.Demo(s, time.Now())
	fmt.Printf(`Finto ServiceNow in ascolto su http://%s (dati inventati).

Per provarlo in Regia (serve --dev: la protezione SSRF accetta http e localhost):
  Istanza          http://%s
  Basic            utente %s, password %s
  OAuth            client %s, segreto %s
  Classe CMDB      cmdb_ci_service   (campo del codice: u_codice)
  Servizi          «Ordini online», «Pagamenti», «Posta interna»

Ctrl+C per fermarlo.
`, *addr, *addr, *user, *password, *clientID, *clientSecret)
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
