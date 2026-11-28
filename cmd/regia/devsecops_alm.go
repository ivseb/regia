package main

import (
	"fmt"
	"log/slog"

	almconn "regia/internal/modules/devsecops/connectors/alm"
	"regia/internal/modules/devsecops/connectors/outbound"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// devsecopsALM crea il connettore ALM/Quality Center (sola lettura) di DevSecOps. Il job
// periodico lo avvia serve.go. Il proxy in uscita è quello esplicito dell'operatore
// (REGIA_CONNECTOR_PROXY), mai HTTP_PROXY o HTTPS_PROXY.
func devsecopsALM(d *db.DB, box *secretbox.Box, cfg config.Config, log *slog.Logger) (*almconn.Service, error) {
	proxy, err := outbound.ParseProxy(config.ConnectorProxy())
	if err != nil {
		return nil, fmt.Errorf("REGIA_CONNECTOR_PROXY: %w", err)
	}
	return almconn.New(almconn.Options{DB: d, Box: box, Dev: cfg.Dev, AllowPrivate: config.ConnectorAllowPrivate(), Proxy: proxy, Log: log}), nil
}
