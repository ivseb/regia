package glapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// Solo i campi che servono a Regia.

// Version è la risposta di GET /version.
type Version struct {
	Version    string `json:"version"`
	Revision   string `json:"revision"`
	Enterprise bool   `json:"enterprise"`
}

// TokenInfo è la risposta di GET /personal_access_tokens/self (vale anche per i token di
// progetto e di gruppo).
type TokenInfo struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Revoked   bool     `json:"revoked"`
	Active    *bool    `json:"active"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"` // AAAA-MM-GG, vuoto se non scade
}

// Group è un gruppo.
type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullPath string `json:"full_path"`
}

// Project è un progetto.
type Project struct {
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Path              string          `json:"path"`
	PathWithNamespace string          `json:"path_with_namespace"`
	DefaultBranch     string          `json:"default_branch"`
	WebURL            string          `json:"web_url"`
	Description       string          `json:"description"`
	Archived          bool            `json:"archived"`
	Visibility        string          `json:"visibility"`
	ForkedFrom        json.RawMessage `json:"forked_from_project"`
}

// IsFork dice se il progetto è un fork.
func (p Project) IsFork() bool { return len(p.ForkedFrom) > 2 && string(p.ForkedFrom) != "null" }

// Link è un link di una Release (anche un allegato caricato come pacchetto).
type Link struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	DirectAssetURL string `json:"direct_asset_url"`
	LinkType       string `json:"link_type"`
}

// Download è l'URL da scaricare: il permalink dell'istanza se c'è.
func (l Link) Download() string {
	if l.DirectAssetURL != "" {
		return l.DirectAssetURL
	}
	return l.URL
}

// Release è una Release di GitLab.
type Release struct {
	TagName         string     `json:"tag_name"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	CreatedAt       time.Time  `json:"created_at"`
	ReleasedAt      *time.Time `json:"released_at"`
	UpcomingRelease bool       `json:"upcoming_release"`
	Author          struct {
		Username string `json:"username"`
	} `json:"author"`
	Commit struct {
		ID string `json:"id"`
	} `json:"commit"`
	Assets struct {
		Links []Link `json:"links"`
	} `json:"assets"`
	Links struct {
		Self string `json:"self"`
	} `json:"_links"`
}

// At è l'istante del rilascio.
func (r Release) At() time.Time {
	if r.ReleasedAt != nil && !r.ReleasedAt.IsZero() {
		return *r.ReleasedAt
	}
	return r.CreatedAt
}

// Pipeline è una pipeline.
type Pipeline struct {
	ID        int64     `json:"id"`
	SHA       string    `json:"sha"`
	Ref       string    `json:"ref"`
	Status    string    `json:"status"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Finished dice se la pipeline è conclusa (non più in corso né in attesa).
func (p Pipeline) Finished() bool { return FinishedStatus(p.Status) }

// FinishedStatus dice se uno stato di pipeline o di deployment è definitivo.
func FinishedStatus(s string) bool {
	switch s {
	case "success", "failed", "canceled", "skipped":
		return true
	}
	return false
}

// Job è un job di una pipeline.
type Job struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Stage         string     `json:"stage"`
	Status        string     `json:"status"`
	AllowFailure  bool       `json:"allow_failure"`
	WebURL        string     `json:"web_url"`
	FinishedAt    *time.Time `json:"finished_at"`
	ArtifactsFile *struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	} `json:"artifacts_file"`
}

// HasArtifacts dice se il job ha un archivio di artefatti.
func (j Job) HasArtifacts() bool { return j.ArtifactsFile != nil && j.ArtifactsFile.Size > 0 }

// Deployment è un deployment; Raw è il documento intero (per i percorsi del profilo).
type Deployment struct {
	ID          int64     `json:"id"`
	Ref         string    `json:"ref"`
	SHA         string    `json:"sha"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Environment struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"environment"`
	Deployable struct {
		Tag *bool `json:"tag"`
	} `json:"deployable"`
	Raw any `json:"-"`
}

// Environment è un ambiente.
type Environment struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Tier  string `json:"tier"`
	State string `json:"state"`
}

func esc(s string) string { return url.PathEscape(s) }

func pid(id int64) string { return strconv.FormatInt(id, 10) }

// Version legge la versione dell'istanza.
func (c *Client) Version(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.Get(ctx, "/version", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// TokenSelf legge le informazioni sul token in uso (GitLab 15.5 e successivi).
func (c *Client) TokenSelf(ctx context.Context) (*TokenInfo, error) {
	var t TokenInfo
	if err := c.Get(ctx, "/personal_access_tokens/self", nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Group legge un gruppo per percorso completo (o id).
func (c *Client) Group(ctx context.Context, path string) (*Group, error) {
	var g Group
	if err := c.Get(ctx, "/groups/"+esc(path), nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Project legge un progetto per percorso completo (o id).
func (c *Client) Project(ctx context.Context, pathOrID string) (*Project, error) {
	var p Project
	if err := c.Get(ctx, "/projects/"+esc(pathOrID), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GroupProjects elenca i progetti di un gruppo e dei suoi sottogruppi, con la paginazione
// keyset (ordinata per id), che regge anche gruppi con migliaia di progetti.
func (c *Client) GroupProjects(ctx context.Context, group string, each func(Project) error) error {
	q := url.Values{"include_subgroups": {"true"}, "with_shared": {"false"}, "order_by": {"id"}, "sort": {"asc"}, "pagination": {"keyset"}}
	return c.paginate(ctx, "/groups/"+esc(group)+"/projects", q, 200, func(raw json.RawMessage) (bool, error) {
		var p Project
		if err := json.Unmarshal(raw, &p); err != nil {
			return false, err
		}
		return false, each(p)
	})
}

// Releases elenca le Release dalla più recente; each può fermarsi.
func (c *Client) Releases(ctx context.Context, project int64, maxPages int, each func(Release) (bool, error)) error {
	q := url.Values{"order_by": {"released_at"}, "sort": {"desc"}}
	return c.paginate(ctx, "/projects/"+pid(project)+"/releases", q, maxPages, func(raw json.RawMessage) (bool, error) {
		var r Release
		if err := json.Unmarshal(raw, &r); err != nil {
			return false, err
		}
		return each(r)
	})
}

// ReleaseByTag legge una Release.
func (c *Client) ReleaseByTag(ctx context.Context, project int64, tag string) (*Release, error) {
	var r Release
	if err := c.Get(ctx, "/projects/"+pid(project)+"/releases/"+esc(tag), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// PipelinesForRef elenca le pipeline di un ref (branch o tag), dalla più recente.
func (c *Client) PipelinesForRef(ctx context.Context, project int64, ref string, n int) ([]Pipeline, error) {
	var out []Pipeline
	q := url.Values{"ref": {ref}, "order_by": {"id"}, "sort": {"desc"}, "per_page": {strconv.Itoa(n)}}
	err := c.Get(ctx, "/projects/"+pid(project)+"/pipelines", q, &out)
	return out, err
}

// Jobs elenca i job di una pipeline (senza i tentativi sostituiti).
func (c *Client) Jobs(ctx context.Context, project, pipeline int64) ([]Job, error) {
	var out []Job
	err := c.paginate(ctx, "/projects/"+pid(project)+"/pipelines/"+pid(pipeline)+"/jobs", url.Values{"include_retried": {"false"}}, 5, func(raw json.RawMessage) (bool, error) {
		var j Job
		if err := json.Unmarshal(raw, &j); err != nil {
			return false, err
		}
		out = append(out, j)
		return false, nil
	})
	return out, err
}

// JobArtifacts scarica l'archivio zip degli artefatti di un job.
func (c *Client) JobArtifacts(ctx context.Context, project, job int64, max int64) ([]byte, error) {
	return c.Download(ctx, c.resolve("/projects/"+pid(project)+"/jobs/"+pid(job)+"/artifacts", nil), max)
}

// BranchHead restituisce lo sha dell'ultimo commit di un branch.
func (c *Client) BranchHead(ctx context.Context, project int64, branch string) (string, error) {
	var b struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := c.Get(ctx, "/projects/"+pid(project)+"/repository/branches/"+esc(branch), nil, &b); err != nil {
		return "", err
	}
	return b.Commit.ID, nil
}

// Deployments elenca i deployment più recenti (per ultimo aggiornamento); environment, se
// non vuoto, li limita a un ambiente.
func (c *Client) Deployments(ctx context.Context, project int64, environment string, max int) ([]Deployment, error) {
	if max <= 0 {
		max = 30
	}
	q := url.Values{"order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(min(max, 100))}}
	if environment != "" {
		q.Set("environment", environment)
	}
	var out []Deployment
	err := c.paginate(ctx, "/projects/"+pid(project)+"/deployments", q, (max+99)/100, func(raw json.RawMessage) (bool, error) {
		var d Deployment
		if err := json.Unmarshal(raw, &d); err != nil {
			return false, err
		}
		_ = json.Unmarshal(raw, &d.Raw)
		out = append(out, d)
		return len(out) >= max, nil
	})
	return out, err
}

// Environments elenca gli ambienti disponibili di un progetto.
func (c *Client) Environments(ctx context.Context, project int64) ([]Environment, error) {
	var out []Environment
	err := c.paginate(ctx, "/projects/"+pid(project)+"/environments", url.Values{"states": {"available"}}, 3, func(raw json.RawMessage) (bool, error) {
		var e Environment
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, err
		}
		out = append(out, e)
		return false, nil
	})
	return out, err
}
