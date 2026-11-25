package github

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
)

// DiscardIngestor non salva niente: si usa finché il modulo devsecops non fornisce
// l'archiviazione. La sincronizzazione gira lo stesso (cursori, stato, anteprima) e
// l'interfaccia avvisa che i dati letti non vengono conservati.
type DiscardIngestor struct{}

func (DiscardIngestor) IngestCheckRun(context.Context, pgx.Tx, engine.Actor, contract.CheckRun) error {
	return nil
}
func (DiscardIngestor) IngestRelease(context.Context, pgx.Tx, engine.Actor, contract.Release) error {
	return nil
}
func (DiscardIngestor) IngestDeployment(context.Context, pgx.Tx, engine.Actor, contract.Deployment) error {
	return nil
}
func (DiscardIngestor) IngestSBOM(context.Context, pgx.Tx, engine.Actor, contract.SBOM) error {
	return nil
}
func (DiscardIngestor) IngestVulnReport(context.Context, pgx.Tx, engine.Actor, contract.VulnReport) error {
	return nil
}
