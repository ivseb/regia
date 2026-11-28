package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/engine/webhooks"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/finance"
	"regia/internal/modules/grc"
	"regia/internal/modules/importer"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/report"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/config"
	"regia/internal/platform/secretbox"
	"regia/internal/webui"
)

func cmdServe(ctx context.Context, args []string) error {
	cfg, err := config.Load(flag.NewFlagSet("serve", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	// Limite morbido per il garbage collector: tiene il processo nel budget di memoria
	// (architecture.md §7) restituendo prima la memoria al sistema. GOMEMLIMIT lo sovrascrive.
	// 30 MiB: con 40 il picco sotto carico (8 client, 300.000 elementi) era di 56-59 MB di RSS,
	// con 30 di 46-49 MB; il costo è qualche GC in più (docs/benchmark.md, «Il budget»).
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(30 << 20)
	}
	go releaseMemory(ctx)
	startPprof(log)
	rt, err := start(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer rt.close()

	authSvc := auth.NewService(rt.db, cfg.SessionTTL)
	var oidcClient *auth.OIDC
	var stateKey []byte
	if cfg.OIDC.Enabled() {
		oidcClient = auth.NewOIDC(cfg.OIDC, nil)
		authSvc.DisablePasswordLogin(cfg.OIDC.Only)
		if stateKey, err = loadKey(filepath.Join(cfg.DataDir, "keys", "oidc-state.key")); err != nil {
			return err
		}
		log.Info("accesso unico OIDC attivo", "issuer", cfg.OIDC.Issuer, "solo_sso", cfg.OIDC.Only)
	}
	eng := engine.New()
	approvals := approval.Register(eng)
	demandSvc := demand.Register(eng, approvals)
	archSvc := architecture.Register(eng)
	// Valutazioni (piano, passo 4): fatti, azioni delle regole e job degli SLA.
	reviewSvc := reviews.Register(eng)
	// Avanzamento (piano, passi 13 e 14): campi di query del cruscotto e promemoria dei report.
	reportSvc := report.Register(eng)
	// Requisiti e deroghe (piano, passi 5 e 6): fatti, gate di rilascio, azioni e scadenze.
	reqSvc := requirements.Register(eng, approvals)
	// DevSecOps (piano, passo 21): implementa l'Ingestor del contratto neutro (API di push e
	// connettori), fatti sull'applicazione e sulla richiesta, evidenze per i requisiti.
	dsoSvc := devsecops.Register(eng, reqSvc)
	// Knowledge (piano, passo 19): documento controllato, revisioni periodiche dei documenti.
	kbSvc := knowledge.Register(eng, approvals)
	// Rischi, fornitori ICT e incidenti (piano, passo 20): fatti, azioni, accettazione e scadenze.
	riskSvc := risk.Register(eng, approvals)
	// Conformità (piano, passo 20): fatti del controllo e ricalcolo orario dell'applicabilità.
	grcSvc := grc.Register(eng)
	// Chiave applicativa (<data>/keys/secrets.key): cifra i segreti MFA e dei webhook.
	// Va nel backup insieme al database: senza, quei segreti non si decifrano più.
	key, err := secretbox.LoadKey(filepath.Join(cfg.DataDir, "keys", "secrets.key"))
	if err != nil {
		return err
	}
	box, err := secretbox.New(key)
	if err != nil {
		return err
	}
	authSvc.SetSecretBox(box)
	// Webhook in uscita (piano, passo 15): worker delle consegne con protezione SSRF.
	allowPrivate := cfg.Dev || config.WebhookAllowPrivate()
	hooks := webhooks.New(webhooks.Options{DB: rt.db, Eng: eng, Box: box, AllowPrivate: allowPrivate, BaseURL: cfg.Collab.BaseURL, Log: log})
	go hooks.Run(ctx, 5*time.Second)
	if allowPrivate && !cfg.Dev {
		log.Warn("webhook verso indirizzi interni consentiti (REGIA_WEBHOOK_ALLOW_PRIVATE): protezione SSRF disattivata")
	}
	// DevSecOps, connettore GitHub (fase 1): sincronizzazione periodica in-process. Gli
	// oggetti del contratto vanno all'Ingestor del modulo devsecops; finché non c'è (nil) il
	// connettore gira lo stesso ma non salva i dati letti.
	var dsoIngestor contract.Ingestor = dsoSvc
	ghSvc := devsecopsGitHub(rt.db, box, cfg, dsoIngestor, log)
	go ghSvc.Run(ctx, time.Minute)
	// DevSecOps, connettore GitLab (fase 5b): stesso schema, stesso Ingestor.
	glSvc := devsecopsGitLab(rt.db, box, cfg, dsoIngestor, log)
	go glSvc.Run(ctx, time.Minute)
	// DevSecOps, connettore Gitea: stesso schema, stesso Ingestor.
	gtSvc := devsecopsGitea(rt.db, box, cfg, dsoIngestor, log)
	go gtSvc.Run(ctx, time.Minute)
	// DevSecOps, connettore ServiceNow in sola lettura: incidenti e change dei servizi CMDB associati.
	snSvc, err := devsecopsServiceNow(rt.db, box, cfg, dsoSvc, log)
	if err != nil {
		return err
	}
	go snSvc.Run(ctx, time.Minute)
	// Inviti e link della password per email: solo con SMTP (altrimenti link da copiare).
	var inviteMail notify.Sender
	if mc := (notify.SMTPConfig{Host: cfg.Collab.SMTPHost, Port: cfg.Collab.SMTPPort, User: cfg.Collab.SMTPUser,
		Password: cfg.Collab.SMTPPassword, From: cfg.Collab.SMTPFrom}); mc.Enabled() {
		inviteMail = notify.SMTPSender{Cfg: mc}
	}
	apiSrv := api.New(api.Options{
		DB: rt.db, Auth: authSvc, Engine: eng, Approvals: approvals, Demand: demandSvc, Arch: archSvc, Log: log, Web: webui.FS(),
		Version: version, Commit: commit, SourceURL: cfg.SourceURL, Dev: cfg.Dev, SessionTTL: cfg.SessionTTL, TrustedProxies: cfg.TrustedProxies,
		FilesDir: filepath.Join(cfg.DataDir, "files"), MaxUploadBytes: int64(cfg.Collab.MaxUploadMB) << 20,
		OIDC: oidcClient, StateKey: stateKey,
		Reviews: reviewSvc, Requirements: reqSvc,
		Report:    reportSvc,
		Devsecops: dsoSvc,
		SecretBox: box, Webhooks: hooks, InviteMail: inviteMail, BaseURL: cfg.Collab.BaseURL,
		Knowledge: kbSvc,
		GitHub:    ghSvc, DevSecOpsIngestor: dsoIngestor, ConnectorAllowPrivate: config.ConnectorAllowPrivate(),
		GitLab:     glSvc,
		Gitea:      gtSvc,
		ServiceNow: snSvc,
		Risk:       riskSvc,
		GRC:        grcSvc,
		// Import di progetti: connettori verso Jira o YouTrack nella rete interna solo se consentito.
		ImportAllowPrivate: importer.AllowPrivateFromEnv(),
	})
	// AI: conservazione delle conversazioni (retention_days del tenant, 90 di default).
	go apiSrv.AI().Run(ctx, time.Hour, log)
	// Wiki personali (ondata 4): aggiornamento con l'AI per chi l'ha acceso o l'ha chiesto.
	go apiSrv.AI().RunWiki(ctx, 5*time.Minute, log)
	go grcSvc.RunRecompute(ctx, rt.db, time.Hour, log)
	// Metriche DORA: ricalcolo orario dei punti di metric_points (DevSecOps, fase 5a).
	go dsoSvc.RunDora(ctx, rt.db, time.Hour, log)
	go reviewSvc.RunSLA(ctx, rt.db, 15*time.Minute, log)
	go reportSvc.RunReminders(ctx, rt.db, time.Hour, log)
	// Versioni dei processi programmate: alla data di entrata in vigore la precedente diventa deprecata e
	// le richieste previste dalla regola di transizione migrano.
	go eng.RunVersioni(ctx, rt.db, time.Minute, log)
	go reqSvc.RunExpiry(ctx, rt.db, time.Hour, log)
	go kbSvc.RunReminders(ctx, rt.db, time.Hour, log)
	go riskSvc.RunDeadlines(ctx, rt.db, 15*time.Minute, log)
	// Vulnerabilità governate: scadenze, eccezioni scadute ed escalation (piano, passo 21, fase 2).
	go dsoSvc.RunVulnerabilities(ctx, rt.db, 15*time.Minute, log)
	// Email delle notifiche: solo con un server SMTP configurato, altrimenti restano in app.
	if smtpCfg := (notify.SMTPConfig{Host: cfg.Collab.SMTPHost, Port: cfg.Collab.SMTPPort, User: cfg.Collab.SMTPUser,
		Password: cfg.Collab.SMTPPassword, From: cfg.Collab.SMTPFrom}); smtpCfg.Enabled() {
		notify.EnableEmail(true)
		go notify.NewMailer(rt.db, notify.SMTPSender{Cfg: smtpCfg}, cfg.Collab.MailInterval, cfg.Collab.BaseURL, log).Run(ctx)
		log.Info("email delle notifiche attive", "smtp", smtpCfg.Host, "intervallo", cfg.Collab.MailInterval.String())
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	// I flussi di eventi restano aperti: allo spegnimento si chiudono per primi.
	srv.RegisterOnShutdown(apiSrv.Close)
	// Gli import in background si fermano a fine lotto e restano da riprendere.
	srv.RegisterOnShutdown(apiSrv.CloseImports)

	// Earned value: fotografia giornaliera dei progetti che lo usano (piano, passo 12).
	go finance.RunDaily(ctx, rt.db, log)

	// Manutenzione periodica. Diventerà un job della coda su Postgres con il worker.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := authSvc.PurgeExpired(ctx); err != nil {
					log.Warn("pulizia sessioni", "errore", err)
				} else if n > 0 {
					log.Info("sessioni scadute rimosse", "numero", n)
				}
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("server avviato", "indirizzo", cfg.HTTPAddr, "versione", version, "dev", cfg.Dev)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("arresto in corso: chiudo le richieste aperte")
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Warn("arresto del server HTTP", "errore", err)
		}
	}
	return nil
}

// loadKey legge una chiave di 32 byte dalla cartella dati, creandola al primo avvio
// (permessi 0600). Firma il cookie del flusso OIDC: resta valida tra i riavvii.
func loadKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, os.WriteFile(path, b, 0o600)
}

// releaseMemory restituisce al sistema la memoria libera ogni minuto: dopo un picco di carico il
// runtime la rilascia solo lentamente e la memoria residente del processo resta alta a riposo.
func releaseMemory(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			debug.FreeOSMemory()
		}
	}
}
