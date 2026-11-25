package giteaapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// Solo i campi che servono a Regia.

// Version è la risposta di GET /version.
type Version struct {
	Version string `json:"version"`
}

// User è l'utente del token (GET /user) o un proprietario di tipo utente.
type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// Org è un'organizzazione.
type Org struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Repo è un repository.
type Repo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	Empty         bool   `json:"empty"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
}

// Attachment è un allegato di una release.
type Attachment struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

// Release è una release di Gitea.
type Release struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Target      string     `json:"target_commitish"`
	Name        string     `json:"name"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	HTMLURL     string     `json:"html_url"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []Attachment `json:"assets"`
}

// At è l'istante del rilascio.
func (r Release) At() time.Time {
	if r.PublishedAt != nil && !r.PublishedAt.IsZero() {
		return *r.PublishedAt
	}
	return r.CreatedAt
}

// Status è uno stato di commit (il «controllo» di Gitea, scritto da Actions o da altri CI).
type Status struct {
	ID          int64     `json:"id"`
	State       string    `json:"status"` // pending, success, error, failure, warning
	Context     string    `json:"context"`
	Description string    `json:"description"`
	TargetURL   string    `json:"target_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Combined è lo stato combinato di un commit: l'ultimo stato per ogni contesto.
type Combined struct {
	State    string   `json:"state"`
	SHA      string   `json:"sha"`
	Statuses []Status `json:"statuses"`
}

func esc(s string) string { return url.PathEscape(s) }

// repoPath costruisce /repos/{owner}/{repo} da «owner/repo».
func repoPath(full string) string {
	owner, name, _ := strings.Cut(full, "/")
	return "/repos/" + esc(owner) + "/" + esc(name)
}

// Version legge la versione dell'istanza.
func (c *Client) Version(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.Get(ctx, "/version", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Self legge l'utente a cui appartiene il token (fallisce con 401 se il token è sbagliato).
func (c *Client) Self(ctx context.Context) (*User, error) {
	var u User
	if err := c.Get(ctx, "/user", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Owner dice se un proprietario è un'organizzazione o un utente («org» o «user»).
func (c *Client) Owner(ctx context.Context, name string) (string, error) {
	var o Org
	err := c.Get(ctx, "/orgs/"+esc(name), nil, &o)
	if err == nil {
		return "org", nil
	}
	if !IsNotFound(err) {
		return "", err
	}
	var u User
	if err := c.Get(ctx, "/users/"+esc(name), nil, &u); err != nil {
		return "", err
	}
	return "user", nil
}

// Repo legge un repository per «owner/nome».
func (c *Client) Repo(ctx context.Context, full string) (*Repo, error) {
	var r Repo
	if err := c.Get(ctx, repoPath(full), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// OwnerRepos elenca i repository di un'organizzazione o di un utente (pagine page/limit).
func (c *Client) OwnerRepos(ctx context.Context, owner string, each func(Repo) error) error {
	kind, err := c.Owner(ctx, owner)
	if err != nil {
		return err
	}
	path := "/users/" + esc(owner) + "/repos"
	if kind == "org" {
		path = "/orgs/" + esc(owner) + "/repos"
	}
	return c.paginate(ctx, path, nil, 200, func(raw json.RawMessage) (bool, error) {
		var r Repo
		if err := json.Unmarshal(raw, &r); err != nil {
			return false, err
		}
		return false, each(r)
	})
}

// Releases elenca le release pubblicate (non bozze) dalla più recente; each può fermarsi.
func (c *Client) Releases(ctx context.Context, repo string, maxPages int, each func(Release) (bool, error)) error {
	q := url.Values{"draft": {"false"}}
	return c.paginate(ctx, repoPath(repo)+"/releases", q, maxPages, func(raw json.RawMessage) (bool, error) {
		var r Release
		if err := json.Unmarshal(raw, &r); err != nil {
			return false, err
		}
		return each(r)
	})
}

// ReleaseByTag legge una release.
func (c *Client) ReleaseByTag(ctx context.Context, repo, tag string) (*Release, error) {
	var r Release
	if err := c.Get(ctx, repoPath(repo)+"/releases/tags/"+esc(tag), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// TagCommit restituisce lo sha del commit di un tag.
func (c *Client) TagCommit(ctx context.Context, repo, tag string) (string, error) {
	var t struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := c.Get(ctx, repoPath(repo)+"/tags/"+esc(tag), nil, &t); err != nil {
		return "", err
	}
	return t.Commit.SHA, nil
}

// CombinedStatus legge gli stati correnti di un ref (branch, tag o sha).
func (c *Client) CombinedStatus(ctx context.Context, repo, ref string) (*Combined, error) {
	var s Combined
	if err := c.Get(ctx, repoPath(repo)+"/commits/"+esc(ref)+"/status", url.Values{"limit": {"100"}}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
