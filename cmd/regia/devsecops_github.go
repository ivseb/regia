package main

import (
	"log/slog"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// devsecopsGitHub crea il connettore GitHub di DevSecOps (fase 1). Il job periodico lo
// avvia serve.go.
func devsecopsGitHub(d *db.DB, box *secretbox.Box, cfg config.Config, ing contract.Ingestor, log *slog.Logger) *ghconn.Service {
	allow := config.ConnectorAllowPrivate()
	if allow && !cfg.Dev {
		log.Warn("connettori DevSecOps verso reti interne consentiti (REGIA_CONNECTOR_ALLOW_PRIVATE): ogni integrazione può attivare «Rete interna»")
	}
	if ing == nil {
		log.Info("connettore GitHub senza archiviazione DevSecOps: i dati letti non vengono salvati")
	}
	return ghconn.New(ghconn.Options{DB: d, Box: box, Ingestor: ing, Dev: cfg.Dev, AllowPrivate: allow, Log: log})
}
