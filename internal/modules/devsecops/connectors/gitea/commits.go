package gitea

import (
	"context"
	"time"

	"regia/internal/modules/devsecops/connectors/commitsync"
	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// commitSource adatta il client alla lettura dei commit (commitsync.Source).
type commitSource struct {
	cl     *giteaapi.Client
	repo   string
	branch string
}

const (
	commitPages = commitsync.MaxCommits / giteaapi.PageSize
	pullPages   = commitsync.MaxPulls/giteaapi.PageSize + 1
)

func (s commitSource) Commits(ctx context.Context, since time.Time, each func(ghapi.Commit) (bool, error)) error {
	return s.cl.Commits(ctx, s.repo, s.branch, since, commitPages, each)
}

func (s commitSource) ClosedPulls(ctx context.Context, each func(ghapi.PullRequest) (bool, error)) error {
	return s.cl.ClosedPulls(ctx, s.repo, pullPages, each)
}
