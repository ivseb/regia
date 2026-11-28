package main

import (
	"fmt"
	"log/slog"

	"regia/internal/modules/devsecops/connectors/outbound"
	snconn "regia/internal/modules/devsecops/connectors/servicenow"
	"regia/internal/platform/config"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// devsecopsServiceNow crea il connettore ServiceNow (sola lettura) di DevSecOps. Il job
// periodico lo avvia serve.go. dora è il modulo devsecops, per il primo ricalcolo delle
// metriche dopo una sincronizzazione. Il proxy in uscita è quello esplicito dell'operatore
// (REGIA_CONNECTOR_PROXY), mai HTTP_PROXY o HTTPS_PROXY.
func devsecopsServiceNow(d *db.DB, box *secretbox.Box, cfg config.Config, dora any, log *slog.Logger) (*snconn.Service, error) {
	proxy, err := outbound.ParseProxy(config.ConnectorProxy())
	if err != nil {
		return nil, fmt.Errorf("REGIA_CONNECTOR_PROXY: %w", err)
	}
	if proxy != nil {
		log.Info("proxy in uscita dei connettori configurato", "proxy", outbound.Redacted(proxy))
	}
	return snconn.New(snconn.Options{DB: d, Box: box, Dora: dora, Dev: cfg.Dev, AllowPrivate: config.ConnectorAllowPrivate(), Proxy: proxy, Log: log}), nil
}
