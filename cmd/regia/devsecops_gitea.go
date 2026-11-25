package main

import (
	"log/slog"

	gtconn "regia/internal/modules/devsecops/connectors/gitea"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// devsecopsGitea crea il connettore Gitea di DevSecOps. Il job periodico lo avvia serve.go.
func devsecopsGitea(d *db.DB, box *secretbox.Box, cfg config.Config, ing contract.Ingestor, log *slog.Logger) *gtconn.Service {
	return gtconn.New(gtconn.Options{DB: d, Box: box, Ingestor: ing, Dev: cfg.Dev, AllowPrivate: config.ConnectorAllowPrivate(), Log: log})
}
