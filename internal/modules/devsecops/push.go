package devsecops

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
)

// Push*: i metodi di Ingestor con il riepilogo dell'invio, per l'API di push.

func (s *Service) PushCheckRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, c contract.CheckRun) (*Receipt, error) {
	return s.ingestCheckRun(ctx, tx, actor, c)
}

func (s *Service) PushRelease(ctx context.Context, tx pgx.Tx, actor engine.Actor, r contract.Release) (*Receipt, error) {
	return s.ingestRelease(ctx, tx, actor, r)
}

func (s *Service) PushDeployment(ctx context.Context, tx pgx.Tx, actor engine.Actor, d contract.Deployment) (*Receipt, error) {
	return s.ingestDeployment(ctx, tx, actor, d)
}

func (s *Service) PushSBOM(ctx context.Context, tx pgx.Tx, actor engine.Actor, b contract.SBOM) (*Receipt, error) {
	return s.ingestSBOM(ctx, tx, actor, b)
}

func (s *Service) PushVulnReport(ctx context.Context, tx pgx.Tx, actor engine.Actor, v contract.VulnReport) (*Receipt, error) {
	return s.ingestVulnReport(ctx, tx, actor, v)
}
