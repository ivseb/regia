package ghapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Solo i campi che servono a Regia.

type Account struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type Repo struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	FullName      string  `json:"full_name"`
	DefaultBranch string  `json:"default_branch"`
	HTMLURL       string  `json:"html_url"`
	Description   string  `json:"description"`
	Archived      bool    `json:"archived"`
	Private       bool    `json:"private"`
	Fork          bool    `json:"fork"`
	Owner         Account `json:"owner"`
}

type Asset struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	URL         string    `json:"url"`
	ContentType string    `json:"content_type"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Release struct {
	ID              int64      `json:"id"`
	TagName         string     `json:"tag_name"`
	Name            string     `json:"name"`
	TargetCommitish string     `json:"target_commitish"`
	HTMLURL         string     `json:"html_url"`
	Draft           bool       `json:"draft"`
	Prerelease      bool       `json:"prerelease"`
	CreatedAt       time.Time  `json:"created_at"`
	PublishedAt     *time.Time `json:"published_at"`
	Author          Account    `json:"author"`
	Assets          []Asset    `json:"assets"`
}

type Deployment struct {
	ID          int64     `json:"id"`
	SHA         string    `json:"sha"`
	Ref         string    `json:"ref"`
	Environment string    `json:"environment"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	Creator     Account   `json:"creator"`
	// Raw è il documento completo, per i percorsi del profilo (payload.version…).
	Raw any `json:"-"`
}

type DeploymentStatus struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	TargetURL   string    `json:"target_url"`
	LogURL      string    `json:"log_url"`
	Environment string    `json:"environment"`
}

type CheckRun struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	HeadSHA     string     `json:"head_sha"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	HTMLURL     string     `json:"html_url"`
	CompletedAt *time.Time `json:"completed_at"`
	Output      struct {
		Title string `json:"title"`
	} `json:"output"`
}

type Content struct {
	Type     string `json:"type"` // file | dir | symlink | submodule
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type App struct {
	ID    int64   `json:"id"`
	Slug  string  `json:"slug"`
	Name  string  `json:"name"`
	Owner Account `json:"owner"`
}

type Installation struct {
	ID                  int64             `json:"id"`
	Account             Account           `json:"account"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
}

// ---------------------------------------------------------------------

func esc(s string) string { return url.PathEscape(s) }

func splitFull(full string) (string, string, error) {
	o, r, ok := strings.Cut(full, "/")
	if !ok || o == "" || r == "" || strings.Contains(r, "/") {
		return "", "", fmt.Errorf("repository non valido: %q (serve org/nome)", full)
	}
	return o, r, nil
}

func repoPath(full string) (string, error) {
	o, r, err := splitFull(full)
	if err != nil {
		return "", err
	}
	return "/repos/" + esc(o) + "/" + esc(r), nil
}

// App restituisce la App (chiamata con il JWT).
func (c *Client) App(ctx context.Context) (*App, error) {
	auth, err := c.appAuthorization()
	if err != nil {
		return nil, err
	}
	var a App
	if err := c.request(ctx, "GET", "/app", nil, auth, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Installation restituisce l'installazione configurata (chiamata con il JWT).
func (c *Client) Installation(ctx context.Context) (*Installation, error) {
	auth, err := c.appAuthorization()
	if err != nil {
		return nil, err
	}
	var in Installation
	if err := c.request(ctx, "GET", "/app/installations/"+esc(c.o.Auth.InstallationID), nil, auth, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// CheckToken ottiene (o rinnova) le credenziali: per la App il token di installazione.
func (c *Client) CheckToken(ctx context.Context) error {
	_, err := c.authorization(ctx, false)
	return err
}

// RateLimitNow legge i limiti correnti (la chiamata non consuma il limite).
func (c *Client) RateLimitNow(ctx context.Context) (RateLimit, error) {
	var out struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Used      int   `json:"used"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := c.Get(ctx, "/rate_limit", nil, &out); err != nil {
		return RateLimit{}, err
	}
	core := out.Resources.Core
	c.mu.Lock()
	c.rate.Limit, c.rate.Remaining, c.rate.Used = core.Limit, core.Remaining, core.Used
	c.rate.Reset = time.Unix(core.Reset, 0).UTC()
	c.rate.Resource = "core"
	c.rate.Observed = c.o.Now().UTC()
	r := c.rate
	c.mu.Unlock()
	return r, nil
}

// Owner verifica che un'organizzazione (o un utente) esista e sia visibile.
func (c *Client) Owner(ctx context.Context, name string) (*Account, error) {
	var a Account
	err := c.Get(ctx, "/orgs/"+esc(name), nil, &a)
	if IsNotFound(err) {
		err = c.Get(ctx, "/users/"+esc(name), nil, &a)
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// OwnerRepos elenca i repository di un'organizzazione (o di un utente, se non è
// un'organizzazione), tutte le pagine.
func (c *Client) OwnerRepos(ctx context.Context, owner string, each func(Repo) error) error {
	fn := func(raw json.RawMessage) (bool, error) {
		var r Repo
		if err := json.Unmarshal(raw, &r); err != nil {
			return true, err
		}
		return false, each(r)
	}
	err := c.paginate(ctx, "/orgs/"+esc(owner)+"/repos", url.Values{"type": {"all"}, "sort": {"full_name"}}, "", 100, fn)
	if IsNotFound(err) {
		// Per un utente /users/{u}/repos dà solo i repository pubblici, anche al proprietario.
		// Se l'owner è l'utente del token personale, /user/repos dà anche i privati che il token
		// vede (con un token fine-grained, solo quelli scelti). Il token di una GitHub App non ha
		// un utente: /user fallisce e resta l'elenco pubblico.
		if login, lerr := c.tokenLogin(ctx); lerr == nil && strings.EqualFold(login, owner) {
			return c.paginate(ctx, "/user/repos", url.Values{"affiliation": {"owner"}, "visibility": {"all"}, "sort": {"full_name"}}, "", 100, fn)
		}
		err = c.paginate(ctx, "/users/"+esc(owner)+"/repos", url.Values{"type": {"owner"}, "sort": {"full_name"}}, "", 100, fn)
	}
	return err
}

// tokenLogin è l'utente del token personale (errore con il token di una GitHub App).
func (c *Client) tokenLogin(ctx context.Context) (string, error) {
	var a Account
	if err := c.Get(ctx, "/user", nil, &a); err != nil {
		return "", err
	}
	return a.Login, nil
}

// Repo legge un repository.
func (c *Client) Repo(ctx context.Context, full string) (*Repo, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var r Repo
	if err := c.Get(ctx, p, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Releases elenca le Release, dalla più recente; each può fermare la lettura.
func (c *Client) Releases(ctx context.Context, full string, maxPages int, each func(Release) (stop bool, err error)) error {
	p, err := repoPath(full)
	if err != nil {
		return err
	}
	return c.paginate(ctx, p+"/releases", url.Values{"per_page": {"30"}}, "", maxPages, func(raw json.RawMessage) (bool, error) {
		var r Release
		if err := json.Unmarshal(raw, &r); err != nil {
			return true, err
		}
		return each(r)
	})
}

// ReleaseByTag legge una Release dal tag.
func (c *Client) ReleaseByTag(ctx context.Context, full, tag string) (*Release, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := c.Get(ctx, p+"/releases/tags/"+esc(tag), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DownloadAsset scarica un allegato di una Release (segue il redirect all'URL firmato).
func (c *Client) DownloadAsset(ctx context.Context, full string, id int64, max int64) ([]byte, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	return c.download(ctx, c.resolve(p+"/releases/assets/"+strconv.FormatInt(id, 10), nil), max)
}

// Deployments elenca i deployment più recenti (al massimo max).
func (c *Client) Deployments(ctx context.Context, full string, max int) ([]Deployment, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	if max <= 0 || max > 100 {
		max = 30
	}
	var out []Deployment
	err = c.paginate(ctx, p+"/deployments", url.Values{"per_page": {strconv.Itoa(max)}}, "", 1, func(raw json.RawMessage) (bool, error) {
		var d Deployment
		if err := json.Unmarshal(raw, &d); err != nil {
			return true, err
		}
		_ = json.Unmarshal(raw, &d.Raw)
		out = append(out, d)
		return len(out) >= max, nil
	})
	return out, err
}

// LatestDeploymentStatus restituisce l'ultimo stato di un deployment (nil se non ne ha).
func (c *Client) LatestDeploymentStatus(ctx context.Context, full string, id int64) (*DeploymentStatus, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var list []DeploymentStatus
	if err := c.Get(ctx, p+"/deployments/"+strconv.FormatInt(id, 10)+"/statuses", url.Values{"per_page": {"1"}}, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// CheckRuns elenca i check run di un commit (o di un branch: l'ultimo commit).
func (c *Client) CheckRuns(ctx context.Context, full, ref string) ([]CheckRun, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var out []CheckRun
	err = c.paginate(ctx, p+"/commits/"+esc(ref)+"/check-runs", url.Values{"filter": {"latest"}}, "check_runs", 10, func(raw json.RawMessage) (bool, error) {
		var r CheckRun
		if err := json.Unmarshal(raw, &r); err != nil {
			return true, err
		}
		out = append(out, r)
		return false, nil
	})
	return out, err
}

// BranchHead restituisce lo sha dell'ultimo commit di un branch.
func (c *Client) BranchHead(ctx context.Context, full, branch string) (string, error) {
	p, err := repoPath(full)
	if err != nil {
		return "", err
	}
	var b struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := c.Get(ctx, p+"/branches/"+esc(branch), nil, &b); err != nil {
		return "", err
	}
	if b.Commit.SHA == "" {
		return "", errors.New("branch senza commit: " + branch)
	}
	return b.Commit.SHA, nil
}

// Dir elenca una cartella del repository a un ref.
func (c *Client) Dir(ctx context.Context, full, dir, ref string) ([]Content, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var out []Content
	if err := c.Get(ctx, p+"/contents/"+escPath(dir), url.Values{"ref": {ref}}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// File legge un file del repository a un ref (anche oltre 1 MB, tramite il blob).
func (c *Client) File(ctx context.Context, full, file, ref string) ([]byte, string, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, "", err
	}
	var f Content
	if err := c.Get(ctx, p+"/contents/"+escPath(file), url.Values{"ref": {ref}}, &f); err != nil {
		return nil, "", err
	}
	if f.Type != "file" {
		return nil, "", fmt.Errorf("%s non è un file", file)
	}
	content, enc := f.Content, f.Encoding
	if content == "" && f.Size > 0 {
		var blob Content
		if err := c.Get(ctx, p+"/git/blobs/"+esc(f.SHA), nil, &blob); err != nil {
			return nil, "", err
		}
		content, enc = blob.Content, blob.Encoding
	}
	if enc != "base64" {
		return []byte(content), f.SHA, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content, "\n", ""))
	if err != nil {
		return nil, "", fmt.Errorf("contenuto di %s non decodificabile", file)
	}
	return b, f.SHA, nil
}

// LastCommitTime restituisce l'istante dell'ultimo commit che ha toccato un file.
func (c *Client) LastCommitTime(ctx context.Context, full, file, ref string) (string, time.Time, error) {
	p, err := repoPath(full)
	if err != nil {
		return "", time.Time{}, err
	}
	var list []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.Get(ctx, p+"/commits", url.Values{"path": {file}, "sha": {ref}, "per_page": {"1"}}, &list); err != nil {
		return "", time.Time{}, err
	}
	if len(list) == 0 {
		return "", time.Time{}, nil
	}
	return list[0].SHA, list[0].Commit.Committer.Date.UTC(), nil
}

func escPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
