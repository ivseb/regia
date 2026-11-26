package giteaapi

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// Commits elenca i commit di un branch dal più recente, dalla data since in poi (stessa
// forma di GitHub: ghapi.Commit); each può fermare la lettura.
func (c *Client) Commits(ctx context.Context, repo, branch string, since time.Time, maxPages int, each func(ghapi.Commit) (stop bool, err error)) error {
	q := url.Values{"stat": {"false"}, "verification": {"false"}, "files": {"false"}}
	if branch != "" {
		q.Set("sha", branch)
	}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	return c.paginate(ctx, repoPath(repo)+"/commits", q, maxPages, func(raw json.RawMessage) (bool, error) {
		var x ghapi.Commit
		if err := json.Unmarshal(raw, &x); err != nil {
			return true, err
		}
		return each(x)
	})
}

// ClosedPulls elenca le pull request chiuse, dalla modificata più di recente; each può
// fermare la lettura (quelle chiuse senza unione le scarta chi legge).
func (c *Client) ClosedPulls(ctx context.Context, repo string, maxPages int, each func(ghapi.PullRequest) (stop bool, err error)) error {
	q := url.Values{"state": {"closed"}, "sort": {"recentupdate"}}
	return c.paginate(ctx, repoPath(repo)+"/pulls", q, maxPages, func(raw json.RawMessage) (bool, error) {
		var x ghapi.PullRequest
		if err := json.Unmarshal(raw, &x); err != nil {
			return true, err
		}
		return each(x)
	})
}
