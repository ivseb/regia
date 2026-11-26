package ghapi

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// Commit è un commit di un elenco (GET /repos/{r}/commits). Gitea ha la stessa forma.
type Commit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

// When è l'istante del commit (del committer, altrimenti dell'autore).
func (c Commit) When() time.Time {
	if !c.Commit.Committer.Date.IsZero() {
		return c.Commit.Committer.Date.UTC()
	}
	return c.Commit.Author.Date.UTC()
}

// Who è chi ha scritto il commit: il login se c'è, altrimenti il nome (mai l'email).
func (c Commit) Who() string {
	if c.Author != nil && c.Author.Login != "" {
		return c.Author.Login
	}
	return c.Commit.Author.Name
}

// PullRequest è una pull request di un elenco (GET /repos/{r}/pulls). Gitea ha la stessa forma.
type PullRequest struct {
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	HTMLURL        string     `json:"html_url"`
	Merged         bool       `json:"merged"` // solo Gitea; GitHub usa merged_at
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Head           struct {
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Commits elenca i commit di un branch dal più recente, dalla data since in poi; each può
// fermare la lettura.
func (c *Client) Commits(ctx context.Context, full, branch string, since time.Time, maxPages int, each func(Commit) (stop bool, err error)) error {
	p, err := repoPath(full)
	if err != nil {
		return err
	}
	q := url.Values{"per_page": {"100"}}
	if branch != "" {
		q.Set("sha", branch)
	}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	return c.paginate(ctx, p+"/commits", q, "", maxPages, func(raw json.RawMessage) (bool, error) {
		var x Commit
		if err := json.Unmarshal(raw, &x); err != nil {
			return true, err
		}
		return each(x)
	})
}

// ClosedPulls elenca le pull request chiuse, dalla modificata più di recente; each può
// fermare la lettura (quelle chiuse senza unione le scarta chi legge).
func (c *Client) ClosedPulls(ctx context.Context, full string, maxPages int, each func(PullRequest) (stop bool, err error)) error {
	p, err := repoPath(full)
	if err != nil {
		return err
	}
	q := url.Values{"state": {"closed"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"50"}}
	return c.paginate(ctx, p+"/pulls", q, "", maxPages, func(raw json.RawMessage) (bool, error) {
		var x PullRequest
		if err := json.Unmarshal(raw, &x); err != nil {
			return true, err
		}
		return each(x)
	})
}
