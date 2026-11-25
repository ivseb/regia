package main

import (
	"log/slog"

	glconn "regia/internal/modules/devsecops/connectors/gitlab"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// devsecopsGitLab crea il connettore GitLab di DevSecOps (fase 5b). Il job periodico lo
// avvia serve.go.
func devsecopsGitLab(d *db.DB, box *secretbox.Box, cfg config.Config, ing contract.Ingestor, log *slog.Logger) *glconn.Service {
	return glconn.New(glconn.Options{DB: d, Box: box, Ingestor: ing, Dev: cfg.Dev, AllowPrivate: config.ConnectorAllowPrivate(), Log: log})
}
