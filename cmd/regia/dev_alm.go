package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"regia/internal/devtools/fakealm"
)

// cmdDevALM avvia un finto ALM/Quality Center con dati inventati (regia dev fake-alm), per
// provare il connettore DevSecOps senza un server vero.
func cmdDevALM(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev fake-alm", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8191", "indirizzo di ascolto")
	user := fs.String("user", "regia.lettura", "utente di servizio")
	password := fs.String("password", "finta-password-sviluppo", "password dell'utente di servizio")
	clientID := fs.String("client-id", "regia-finto", "client id dell'API key")
	clientSecret := fs.String("client-secret", "finto-segreto-sviluppo", "segreto dell'API key")
	legacy := fs.Bool("legacy", false, "si comporta come ALM 12.60-14.x: l'API key entra solo da rest/oauth2/login")
	xsrf := fs.Bool("xsrf", true, "come ALM 16.00 e successivi: le chiamate non GET vogliono X-XSRF-TOKEN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := fakealm.New()
	s.AddUser(*user, *password)
	s.AddAPIKey(*clientID, *clientSecret)
	s.OnlyOAuthAPIKeys, s.RequireXSRF = *legacy, *xsrf
	if loc, err := time.LoadLocation("Europe/Rome"); err == nil {
		s.Loc = loc
	}
	fakealm.Demo(s, time.Now())
	fmt.Printf(`Finto ALM/Quality Center in ascolto su http://%s/qcbin (dati inventati).

Per provarlo in Regia (serve --dev: la protezione SSRF accetta http e localhost):
  Indirizzo        http://%s/qcbin
  API key          client id %s, segreto %s
  Utente           %s, password %s
  Progetti         QUALITA / Ordini_online, QUALITA / Pagamenti
  Numero di change campo user-01 del test set (CHG0045120, CHG0045121)
  Applicazione     progetto, oppure cartella di primo livello, oppure campo user-02
  Fuso del server  Europe/Rome

Ctrl+C per fermarlo.
`, *addr, *addr, *clientID, *clientSecret, *user, *password)
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
