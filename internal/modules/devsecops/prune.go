package devsecops

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
)

// PruneReleases toglie i rilasci del servizio che keep scarta (contract.ReleasePruner): i
// deployment che li citavano restano, senza rilascio. Se ne ha tolti, ricalcola tutta la
// finestra delle metriche DORA, così la frequenza dei rilasci non conta più quelli scartati.
func (s *Service) PruneReleases(ctx context.Context, tx pgx.Tx, actor engine.Actor, svc contract.Service, keep func(releaseURL string) bool) (int, error) {
	var serviceID string
	err := tx.QueryRow(ctx, "select id::text from services where lower(name) = lower($1)", svc.Name).Scan(&serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, "select id::text, release_url from releases where service_id = $1 and release_url is not null and release_url <> ''", serviceID)
	if err != nil {
		return 0, err
	}
	var drop []string
	for rows.Next() {
		var id, u string
		if err := rows.Scan(&id, &u); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep(u) {
			drop = append(drop, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(drop) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, "delete from releases where id::text = any($1)", drop); err != nil {
		return 0, err
	}
	if _, err := s.recompute(ctx, tx, true); err != nil {
		return 0, err
	}
	return len(drop), auditEvent(ctx, tx, actor, "devsecops.releases_pruned", "service", serviceID, map[string]any{"service": svc.Name, "releases": len(drop)})
}
