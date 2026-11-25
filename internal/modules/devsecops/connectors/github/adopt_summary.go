package github

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// AdoptionBrief è lo stato del collegamento a GitHub, in sintesi, per l'elenco «cosa manca»
// delle applicazioni.
type AdoptionBrief struct {
	Integrations int                     `json:"integrations"` // integrazioni GitHub attive
	Templates    bool                    `json:"templates"`    // c'è un profilo con template di adozione
	Adoptions    map[string][]AdoptionPR `json:"adoptions"`    // per applicazione, dalla più recente
}

// AdoptionSummary legge lo stato dell'adozione per un gruppo di applicazioni, senza chiamare GitHub.
func (s *Service) AdoptionSummary(ctx context.Context, tenantID string, appIDs []string) (*AdoptionBrief, error) {
	b := &AdoptionBrief{Adoptions: map[string][]AdoptionPR{}}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "select count(*) from integrations where kind = 'github' and active").Scan(&b.Integrations); err != nil {
			return err
		}
		srcs, err := s.sources(ctx, tx)
		if err != nil {
			return err
		}
		b.Templates = len(srcs) > 0
		if len(appIDs) == 0 {
			return nil
		}
		list, err := adoptionsOf(ctx, tx, "application_element_id = any($1)", appIDs)
		if err != nil {
			return err
		}
		for _, p := range list {
			if p.ApplicationID != nil {
				b.Adoptions[*p.ApplicationID] = append(b.Adoptions[*p.ApplicationID], p)
			}
		}
		return nil
	})
	return b, err
}

// RefreshAdoptionsAsync rilegge in background lo stato delle pull request aperte (al più una
// volta al minuto per pull request).
func (s *Service) RefreshAdoptionsAsync(tenantID string, appIDs []string) {
	if len(appIDs) == 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, id := range appIDs {
			if !validID(id) {
				continue
			}
			if _, err := s.SyncAdoptions(ctx, tenantID, id, false); err != nil {
				s.o.Log.Debug("pull request di adozione", "errore", err)
			}
		}
	}()
}
