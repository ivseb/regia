package github

import (
	"context"
	"time"

	"regia/internal/modules/devsecops/connectors/commitsync"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/contract"
)

// commitSource adatta il client alla lettura dei commit (commitsync.Source).
type commitSource struct {
	cl     *ghapi.Client
	repo   string
	branch string
}

const (
	commitPages = commitsync.MaxCommits / 100
	pullPages   = commitsync.MaxPulls/50 + 1
)

func (s commitSource) Commits(ctx context.Context, since time.Time, each func(ghapi.Commit) (bool, error)) error {
	return s.cl.Commits(ctx, s.repo, s.branch, since, commitPages, each)
}

func (s commitSource) ClosedPulls(ctx context.Context, each func(ghapi.PullRequest) (bool, error)) error {
	return s.cl.ClosedPulls(ctx, s.repo, pullPages, each)
}

// readCommits legge i commit e le PR unite che citano un elemento (cursore per repository).
func (s *Service) readCommits(ctx context.Context, cl *ghapi.Client, r repoRow, svc contract.Service, cur commitsync.Cursor, full bool) (commitsync.Result, error) {
	return commitsync.Read(ctx, commitSource{cl: cl, repo: r.FullName, branch: r.DefaultBranch}, svc, cur, full, s.o.Now())
}
