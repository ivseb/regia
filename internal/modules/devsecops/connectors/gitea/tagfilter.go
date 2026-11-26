package gitea

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/platform/db"
)

// Schema dei tag dei rilasci (pilota home lab, difetto 5): come nel connettore GitHub
// (github/tagfilter.go), con le Release di Gitea.

// tagQuestions legge la prima pagina delle Release dei repository candidati e chiede lo schema
// dove ce n'è più d'uno. Un repository che non risponde si salta.
func (s *Service) tagQuestions(ctx context.Context, tenantID string, it *Integration, cl *giteaapi.Client) ([]ghconn.TagQuestion, error) {
	var def *profile.Definition
	var cands []ghconn.TagCandidate
	if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if def, _, err = definitionFor(ctx, tx, it.Config.ProfileID); err != nil {
			return err
		}
		cands, err = ghconn.TagCandidates(ctx, tx, it.ID, def)
		return err
	}); err != nil {
		return nil, err
	}
	out := []ghconn.TagQuestion{}
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		var tags []string
		if err := cl.Releases(ctx, c.FullName, 1, func(r giteaapi.Release) (bool, error) {
			if !r.Draft && def.AcceptsRepoTag(c.FullName, r.TagName, r.Prerelease) {
				tags = append(tags, r.TagName)
			}
			return false, nil
		}); err != nil {
			continue
		}
		if q := ghconn.QuestionFor(c, tags); q != nil {
			out = append(out, *q)
		}
	}
	return out, nil
}

// SetTagFilter risponde alla domanda per un repository dell'integrazione Gitea.
func (s *Service) SetTagFilter(ctx context.Context, a Actor, integrationID, repoID string, in ghconn.TagFilterInput) (*ghconn.TagFilterResult, error) {
	if !validID(repoID) {
		return nil, ErrRepoNotFound
	}
	var out *ghconn.TagFilterResult
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		it, _, err := s.load(ctx, tx, integrationID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectRepo+" and r.integration_id = $1 and r.id = $2", integrationID, repoID)
		if err != nil {
			return err
		}
		list, err := scanRepos(rows)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return ErrRepoNotFound
		}
		r := list[0].Repo
		out, err = ghconn.SaveTagFilter(ctx, tx, a, s.o.Ingestor, ghconn.TagFilterTarget{
			IntegrationID: it.ID, IntegrationName: it.Name, ProfileID: it.Config.ProfileID, Kind: "gitea",
			FullName: r.FullName, Service: serviceOf(r), Confirmed: r.Status == "confirmed",
		}, in.Pattern)
		return err
	})
	return out, err
}
