// Package fakegithub è un finto GitHub (API REST) per i test e per lo sviluppo
// (regia dev fake-github). Copre solo le chiamate che usa il connettore di Regia:
// GitHub App (JWT RS256 verificato con la chiave pubblica, token di installazione con
// scadenza), token personali, organizzazioni e repository con paginazione, Release con
// allegati (download con redirect), Deployments API, check run, contenuti dei file,
// limiti di velocità (primari e secondari, simulabili). I dati sono inventati.
package fakegithub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// Server è lo stato del finto GitHub.
type Server struct {
	mu sync.Mutex

	// BasePath è il prefisso dell'API: "" come api.github.com, "/api/v3" come GHES.
	BasePath string
	// Enterprise, se valorizzato, è la versione dichiarata in X-GitHub-Enterprise-Version.
	Enterprise string

	AppID          string
	AppName        string
	InstallationID string
	Key            *rsa.PrivateKey
	TokenTTL       time.Duration
	// PATs sono i token personali accettati.
	PATs map[string]bool
	// PATLogins: l'utente di un token personale (GET /user e /user/repos); un token senza
	// utente, come quello di una installazione, riceve 403 su /user.
	PATLogins map[string]string
	// Permissions dell'installazione (prova di connessione).
	Permissions map[string]string

	// Limiti di velocità: Limit richieste per finestra di ResetEvery.
	Limit      int
	ResetEvery time.Duration
	remaining  int
	resetAt    time.Time

	Now func() time.Time

	owners   map[string]string // login → Organization | User
	repos    map[string]*Repo  // org/nome
	tokens   map[string]time.Time
	issued   int
	nextID   int64
	requests []string
	failNext []injected
}

type injected struct {
	prefix     string
	status     int
	retryAfter int
}

// Repo è un repository del finto GitHub.
type Repo struct {
	ID            int64
	Owner, Name   string
	DefaultBranch string
	Private       bool
	Archived      bool
	Description   string
	Releases      []*Release // dalla più vecchia alla più recente
	Deployments   []*Deployment
	Checks        map[string][]*CheckRun // sha → check run
	Branches      map[string]string      // branch → sha
	Pulls         []*PullRequest
	Files         map[string]map[string][]byte
	FileCommits   map[string]time.Time // branch\x00path → ultimo commit
}

type Release struct {
	ID          int64
	Tag         string
	Name        string
	Target      string
	Author      string
	Draft       bool
	Prerelease  bool
	PublishedAt time.Time
	Assets      []*Asset
}

type Asset struct {
	ID        int64
	Name      string
	Data      []byte
	UpdatedAt time.Time
}

type Deployment struct {
	ID          int64
	SHA, Ref    string
	Environment string
	Payload     map[string]any
	CreatedAt   time.Time
	Statuses    []*DeploymentStatus
}

type DeploymentStatus struct {
	ID        int64
	State     string
	CreatedAt time.Time
}

type CheckRun struct {
	ID          int64
	Name        string
	Status      string
	Conclusion  string
	CompletedAt time.Time
}

// New crea un finto GitHub con una App (chiave RSA generata), un'installazione e un PAT.
func New() *Server {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &Server{
		AppID: "4242", AppName: "Regia (finto)", InstallationID: "777", Key: key, TokenTTL: time.Hour,
		PATs: map[string]bool{}, PATLogins: map[string]string{},
		Permissions: map[string]string{"contents": "read", "metadata": "read", "deployments": "read", "checks": "read"},
		Limit:       5000, ResetEvery: time.Hour,
		Now:    time.Now,
		owners: map[string]string{}, repos: map[string]*Repo{}, tokens: map[string]time.Time{}, nextID: 1000,
	}
}

// PrivateKeyPEM è la chiave privata della App in PEM (come la scarica GitHub).
func (s *Server) PrivateKeyPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(s.Key)}))
}

func (s *Server) id() int64 { s.nextID++; return s.nextID }

// AddOwner aggiunge un'organizzazione (o un utente con kind "User").
func (s *Server) AddOwner(login, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		kind = "Organization"
	}
	s.owners[login] = kind
}

// AddRepo aggiunge un repository con il branch predefinito main.
func (s *Server) AddRepo(owner, name string) *Repo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[owner]; !ok {
		s.owners[owner] = "Organization"
	}
	r := &Repo{ID: s.id(), Owner: owner, Name: name, DefaultBranch: "main", Checks: map[string][]*CheckRun{},
		Branches: map[string]string{"main": randSHA()}, Files: map[string]map[string][]byte{}, FileCommits: map[string]time.Time{}}
	s.repos[owner+"/"+name] = r
	return r
}

// RemoveRepo toglie un repository (per provare i repository scomparsi).
func (s *Server) RemoveRepo(full string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.repos, full)
}

// AddRelease pubblica una Release con gli allegati (nome → contenuto).
func (s *Server) AddRelease(full, tag, author string, at time.Time, assets map[string][]byte) *Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	rel := &Release{ID: s.id(), Tag: tag, Name: tag, Target: r.Branches[r.DefaultBranch], Author: author, PublishedAt: at.UTC()}
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rel.Assets = append(rel.Assets, &Asset{ID: s.id(), Name: n, Data: assets[n], UpdatedAt: at.UTC()})
	}
	r.Releases = append(r.Releases, rel)
	return rel
}

// AddAsset aggiunge un allegato a una Release esistente (un finalize che completa il registro).
func (s *Server) AddAsset(full, tag, name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rel := range s.repos[full].Releases {
		if rel.Tag == tag {
			rel.Assets = append(rel.Assets, &Asset{ID: s.id(), Name: name, Data: data, UpdatedAt: s.Now().UTC()})
		}
	}
}

// AddDeployment registra un deployment con il suo primo stato.
func (s *Server) AddDeployment(full, env, ref, sha string, payload map[string]any, state string, at time.Time) *Deployment {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	d := &Deployment{ID: s.id(), SHA: sha, Ref: ref, Environment: env, Payload: payload, CreatedAt: at.UTC()}
	if state != "" {
		d.Statuses = append(d.Statuses, &DeploymentStatus{ID: s.id(), State: state, CreatedAt: at.UTC()})
	}
	r.Deployments = append(r.Deployments, d)
	return d
}

// AddDeploymentStatus aggiunge uno stato a un deployment.
func (s *Server) AddDeploymentStatus(full string, id int64, state string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.repos[full].Deployments {
		if d.ID == id {
			d.Statuses = append(d.Statuses, &DeploymentStatus{ID: s.id(), State: state, CreatedAt: at.UTC()})
		}
	}
}

// Push sposta il branch su un nuovo commit e imposta i check run di quel commit.
func (s *Server) Push(full, branch string, checks []CheckRun) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	sha := randSHA()
	r.Branches[branch] = sha
	for _, c := range checks {
		c := c
		c.ID = s.id()
		r.Checks[sha] = append(r.Checks[sha], &c)
	}
	return sha
}

// SetCheck aggiorna (o aggiunge) un check run dell'ultimo commit di un branch.
func (s *Server) SetCheck(full, branch string, c CheckRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	sha := r.Branches[branch]
	for _, x := range r.Checks[sha] {
		if x.Name == c.Name {
			x.Status, x.Conclusion, x.CompletedAt = c.Status, c.Conclusion, c.CompletedAt
			return
		}
	}
	c.ID = s.id()
	r.Checks[sha] = append(r.Checks[sha], &c)
}

// SetFile scrive un file su un branch (nuovo commit); content nil cancella il file.
func (s *Server) SetFile(full, branch, file string, content []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	if r.Files[branch] == nil {
		r.Files[branch] = map[string][]byte{}
	}
	if content == nil {
		delete(r.Files[branch], file)
	} else {
		r.Files[branch][file] = content
	}
	sha := randSHA()
	r.Branches[branch] = sha
	r.FileCommits[branch+"\x00"+file] = s.Now().UTC()
	return sha
}

// FailNext fa fallire la prossima richiesta il cui percorso inizia con prefix (dopo
// BasePath): status 429 o 403 con Retry-After (limite secondario), oppure 500.
func (s *Server) FailNext(prefix string, status, retryAfter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, injected{prefix: prefix, status: status, retryAfter: retryAfter})
}

// SetRemaining imposta le richieste rimaste nella finestra corrente.
func (s *Server) SetRemaining(n int, resetIn time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remaining = n
	s.resetAt = s.Now().Add(resetIn)
}

// IssuedTokens è il numero di token di installazione emessi.
func (s *Server) IssuedTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issued
}

// Requests restituisce le richieste ricevute ("GET /repos/…") e le azzera se reset.
func (s *Server) Requests(reset bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.requests...)
	if reset {
		s.requests = nil
	}
	return out
}

// Count conta le richieste ricevute che contengono sub.
func (s *Server) Count(sub string) int {
	n := 0
	for _, r := range s.Requests(false) {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return n
}

func randSHA() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

// Handler restituisce il gestore HTTP del finto GitHub.
func (s *Server) Handler() http.Handler {
	b := s.BasePath
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+b+"/app/installations/{id}/access_tokens", s.accessToken)
	mux.HandleFunc("GET "+b+"/app", s.appOnly(s.app))
	mux.HandleFunc("GET "+b+"/app/installations/{id}", s.appOnly(s.installation))
	mux.HandleFunc("GET "+b+"/rate_limit", s.authed(false, s.rateLimit))
	mux.HandleFunc("GET "+b+"/orgs/{owner}", s.authed(true, s.owner("Organization")))
	mux.HandleFunc("GET "+b+"/users/{owner}", s.authed(true, s.owner("User")))
	mux.HandleFunc("GET "+b+"/orgs/{owner}/repos", s.authed(true, s.ownerRepos("Organization")))
	mux.HandleFunc("GET "+b+"/users/{owner}/repos", s.authed(true, s.ownerRepos("User")))
	mux.HandleFunc("GET "+b+"/user", s.authed(true, s.user))
	mux.HandleFunc("GET "+b+"/user/repos", s.authed(true, s.userRepos))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}", s.authed(true, s.repo))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/releases", s.authed(true, s.releases))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/releases/tags/{tag}", s.authed(true, s.releaseByTag))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/releases/assets/{id}", s.authed(true, s.asset))
	mux.HandleFunc("GET /_download/{id}", s.download)
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/deployments", s.authed(true, s.deployments))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/deployments/{id}/statuses", s.authed(true, s.deploymentStatuses))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/commits/{ref}/check-runs", s.authed(true, s.checkRuns))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/commits", s.authed(true, s.commits))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/branches/{branch}", s.authed(true, s.branch))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/contents/{path...}", s.authed(true, s.contents))
	// Scritture (pulls.go): adozione guidata di Regia.
	mux.HandleFunc("POST "+b+"/repos/{owner}/{repo}/git/refs", s.authed(true, s.createRef))
	mux.HandleFunc("PUT "+b+"/repos/{owner}/{repo}/contents/{path...}", s.authed(true, s.putContents))
	mux.HandleFunc("POST "+b+"/repos/{owner}/{repo}/pulls", s.authed(true, s.createPull))
	mux.HandleFunc("GET "+b+"/repos/{owner}/{repo}/pulls/{n}", s.authed(true, s.getPull))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		jsonError(w, http.StatusNotFound, "Not Found")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+strings.TrimPrefix(r.URL.Path, b))
		var inj *injected
		rel := strings.TrimPrefix(r.URL.Path, b)
		for i, f := range s.failNext {
			if strings.HasPrefix(rel, f.prefix) {
				inj = &f
				s.failNext = append(s.failNext[:i], s.failNext[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		if s.Enterprise != "" {
			w.Header().Set("X-GitHub-Enterprise-Version", s.Enterprise)
		}
		if inj != nil {
			if inj.retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(inj.retryAfter))
			}
			jsonError(w, inj.status, "You have exceeded a secondary rate limit. Please wait a few minutes before you try again.")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// origin è schema e host con cui il client ha raggiunto il finto GitHub.
func origin(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg, "documentation_url": "https://docs.github.com/rest"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	t, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if t == "" {
		t, _ = strings.CutPrefix(r.Header.Get("Authorization"), "token ")
	}
	return strings.TrimSpace(t)
}

// appOnly richiede il JWT della App.
func (s *Server) appOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iss, err := ghapi.VerifyAppJWT(bearer(r), &s.Key.PublicKey, s.Now())
		if err != nil || iss != s.AppID {
			jsonError(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
			return
		}
		h(w, r)
	}
}

// authed richiede un token di installazione valido o un PAT; count consuma il limite.
func (s *Server) authed(count bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		s.mu.Lock()
		exp, isInst := s.tokens[tok]
		valid := (isInst && exp.After(s.Now())) || s.PATs[tok]
		now := s.Now()
		if s.resetAt.IsZero() || !now.Before(s.resetAt) {
			s.remaining = s.Limit
			s.resetAt = now.Add(s.ResetEvery)
		}
		limited := false
		if valid && count {
			if s.remaining <= 0 {
				limited = true
			} else {
				s.remaining--
			}
		}
		rem, reset, lim := s.remaining, s.resetAt, s.Limit
		s.mu.Unlock()
		if !valid {
			jsonError(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(lim))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(rem))
		w.Header().Set("X-RateLimit-Used", strconv.Itoa(lim-rem))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.Header().Set("X-RateLimit-Resource", "core")
		if s.PATs[tok] {
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		}
		if limited {
			jsonError(w, http.StatusForbidden, "API rate limit exceeded for installation.")
			return
		}
		h(w, r)
	}
}

func (s *Server) accessToken(w http.ResponseWriter, r *http.Request) {
	iss, err := ghapi.VerifyAppJWT(bearer(r), &s.Key.PublicKey, s.Now())
	if err != nil || iss != s.AppID {
		jsonError(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
		return
	}
	if r.PathValue("id") != s.InstallationID {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	s.mu.Lock()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := "ghs_" + hex.EncodeToString(b)
	exp := s.Now().Add(s.TokenTTL).UTC().Truncate(time.Second)
	s.tokens[tok] = exp
	s.issued++
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": exp, "permissions": s.Permissions})
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(s.AppID, 10, 64)
	writeJSON(w, map[string]any{"id": id, "slug": "regia-finto", "name": s.AppName, "owner": map[string]string{"login": "esempio", "type": "Organization"}})
}

func (s *Server) installation(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != s.InstallationID {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	id, _ := strconv.ParseInt(s.InstallationID, 10, 64)
	writeJSON(w, map[string]any{"id": id, "account": map[string]string{"login": "esempio", "type": "Organization"},
		"repository_selection": "all", "permissions": s.Permissions})
}

func (s *Server) rateLimit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rem, reset, lim := s.remaining, s.resetAt, s.Limit
	s.mu.Unlock()
	writeJSON(w, map[string]any{"resources": map[string]any{"core": map[string]any{"limit": lim, "remaining": rem, "used": lim - rem, "reset": reset.Unix()}}})
}

func (s *Server) owner(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		k, ok := s.owners[r.PathValue("owner")]
		s.mu.Unlock()
		if !ok || k != kind {
			jsonError(w, http.StatusNotFound, "Not Found")
			return
		}
		writeJSON(w, map[string]any{"login": r.PathValue("owner"), "type": kind})
	}
}

// paginate scrive una pagina dell'elenco con l'intestazione Link.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 30
	}
	if per > 100 {
		per = 100
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * per
	if start > len(items) {
		start = len(items)
	}
	end := min(start+per, len(items))
	if end < len(items) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(page+1))
		q.Set("per_page", strconv.Itoa(per))
		next := origin(r) + r.URL.Path + "?" + q.Encode()
		q.Set("page", strconv.Itoa((len(items)+per-1)/per))
		last := origin(r) + r.URL.Path + "?" + q.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next", <%s>; rel="last"`, next, last))
	}
	out := items[start:end]
	if out == nil {
		out = []T{}
	}
	writeJSON(w, out)
}

func (s *Server) repoJSON(r *Repo, host string) map[string]any {
	full := r.Owner + "/" + r.Name
	return map[string]any{"id": r.ID, "name": r.Name, "full_name": full, "default_branch": r.DefaultBranch,
		"html_url": "https://github.example.test/" + full, "description": r.Description, "archived": r.Archived, "private": r.Private,
		"fork": false, "owner": map[string]string{"login": r.Owner, "type": s.owners[r.Owner]}}
}

func (s *Server) ownerRepos(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner := r.PathValue("owner")
		s.mu.Lock()
		if k, ok := s.owners[owner]; !ok || k != kind {
			s.mu.Unlock()
			jsonError(w, http.StatusNotFound, "Not Found")
			return
		}
		var names []string
		for full, rp := range s.repos {
			// Come GitHub: l'elenco di un utente mostra solo i pubblici, anche al proprietario.
			if rp.Owner == owner && (kind != "User" || !rp.Private) {
				names = append(names, full)
			}
		}
		sort.Strings(names)
		var items []map[string]any
		for _, n := range names {
			items = append(items, s.repoJSON(s.repos[n], r.Host))
		}
		s.mu.Unlock()
		paginate(w, r, items)
	}
}

// user è l'utente del token personale; il token di una installazione non ne ha uno.
func (s *Server) user(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	login := s.PATLogins[bearer(r)]
	s.mu.Unlock()
	if login == "" {
		jsonError(w, http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	writeJSON(w, map[string]any{"login": login, "type": "User"})
}

// userRepos elenca i repository dell'utente del token, privati compresi.
func (s *Server) userRepos(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	login := s.PATLogins[bearer(r)]
	if login == "" {
		s.mu.Unlock()
		jsonError(w, http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	var names []string
	for full, rp := range s.repos {
		if rp.Owner == login {
			names = append(names, full)
		}
	}
	sort.Strings(names)
	var items []map[string]any
	for _, n := range names {
		items = append(items, s.repoJSON(s.repos[n], r.Host))
	}
	s.mu.Unlock()
	paginate(w, r, items)
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) *Repo {
	s.mu.Lock()
	rp := s.repos[r.PathValue("owner")+"/"+r.PathValue("repo")]
	s.mu.Unlock()
	if rp == nil {
		jsonError(w, http.StatusNotFound, "Not Found")
	}
	return rp
}

func (s *Server) repo(w http.ResponseWriter, r *http.Request) {
	if rp := s.find(w, r); rp != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, s.repoJSON(rp, r.Host))
	}
}

func (s *Server) releaseJSON(rp *Repo, rel *Release, host string) map[string]any {
	full := rp.Owner + "/" + rp.Name
	assets := []map[string]any{}
	for _, a := range rel.Assets {
		assets = append(assets, map[string]any{"id": a.ID, "name": a.Name, "size": len(a.Data), "content_type": "application/json",
			"updated_at": a.UpdatedAt, "url": host + s.BasePath + "/repos/" + full + "/releases/assets/" + strconv.FormatInt(a.ID, 10)})
	}
	return map[string]any{"id": rel.ID, "tag_name": rel.Tag, "name": rel.Name, "target_commitish": rel.Target, "draft": rel.Draft,
		"prerelease": rel.Prerelease, "created_at": rel.PublishedAt, "published_at": rel.PublishedAt,
		"html_url": "https://github.example.test/" + full + "/releases/tag/" + rel.Tag,
		"author":   map[string]string{"login": rel.Author, "type": "User"}, "assets": assets}
}

func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	var items []map[string]any
	for i := len(rp.Releases) - 1; i >= 0; i-- {
		items = append(items, s.releaseJSON(rp, rp.Releases[i], origin(r)))
	}
	s.mu.Unlock()
	paginate(w, r, items)
}

func (s *Server) releaseByTag(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rel := range rp.Releases {
		if rel.Tag == r.PathValue("tag") {
			writeJSON(w, s.releaseJSON(rp, rel, origin(r)))
			return
		}
	}
	jsonError(w, http.StatusNotFound, "Not Found")
}

func (s *Server) findAsset(rp *Repo, id string) *Asset {
	for _, rel := range rp.Releases {
		for _, a := range rel.Assets {
			if strconv.FormatInt(a.ID, 10) == id {
				return a
			}
		}
	}
	return nil
}

// asset: con Accept application/octet-stream rimanda (302) a un URL "firmato", come GitHub.
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	a := s.findAsset(rp, r.PathValue("id"))
	s.mu.Unlock()
	if a == nil {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	if r.Header.Get("Accept") != "application/octet-stream" {
		writeJSON(w, map[string]any{"id": a.ID, "name": a.Name, "size": len(a.Data)})
		return
	}
	sig := base64.RawURLEncoding.EncodeToString([]byte(rp.Owner + "/" + rp.Name))
	http.Redirect(w, r, origin(r)+"/_download/"+strconv.FormatInt(a.ID, 10)+"?sig="+sig, http.StatusFound)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	// Un URL firmato non vuole altre credenziali: GitHub (e S3) rifiutano Authorization.
	if r.Header.Get("Authorization") != "" {
		jsonError(w, http.StatusBadRequest, "Only one auth mechanism allowed")
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("sig"))
	if err != nil {
		jsonError(w, http.StatusForbidden, "Request has expired")
		return
	}
	s.mu.Lock()
	rp := s.repos[string(raw)]
	var a *Asset
	if rp != nil {
		a = s.findAsset(rp, r.PathValue("id"))
	}
	s.mu.Unlock()
	if a == nil {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(a.Data)
}

func (s *Server) deployments(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	var items []map[string]any
	for i := len(rp.Deployments) - 1; i >= 0; i-- {
		d := rp.Deployments[i]
		items = append(items, map[string]any{"id": d.ID, "sha": d.SHA, "ref": d.Ref, "environment": d.Environment, "task": "deploy",
			"payload": d.Payload, "created_at": d.CreatedAt, "creator": map[string]string{"login": "pipeline[bot]"}})
	}
	s.mu.Unlock()
	paginate(w, r, items)
}

func (s *Server) deploymentStatuses(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	var items []map[string]any
	for _, d := range rp.Deployments {
		if strconv.FormatInt(d.ID, 10) != r.PathValue("id") {
			continue
		}
		for i := len(d.Statuses) - 1; i >= 0; i-- {
			st := d.Statuses[i]
			items = append(items, map[string]any{"id": st.ID, "state": st.State, "created_at": st.CreatedAt, "environment": d.Environment})
		}
	}
	s.mu.Unlock()
	paginate(w, r, items)
}

func (s *Server) checkRuns(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	ref := r.PathValue("ref")
	sha := ref
	if b, ok := rp.Branches[ref]; ok {
		sha = b
	}
	items := []map[string]any{}
	for _, c := range rp.Checks[sha] {
		var done any
		concl := any(nil)
		if c.Status == "completed" {
			done = c.CompletedAt
			concl = c.Conclusion
		}
		items = append(items, map[string]any{"id": c.ID, "name": c.Name, "head_sha": sha, "status": c.Status, "conclusion": concl,
			"completed_at": done, "html_url": "https://github.example.test/" + rp.Owner + "/" + rp.Name + "/runs/" + strconv.FormatInt(c.ID, 10),
			"output": map[string]any{"title": c.Name + ": " + c.Conclusion}})
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"total_count": len(items), "check_runs": items})
}

func (s *Server) commits(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	branch := r.URL.Query().Get("sha")
	if branch == "" {
		branch = rp.DefaultBranch
	}
	branch = rp.branchOf(branch)
	at, ok := rp.FileCommits[branch+"\x00"+r.URL.Query().Get("path")]
	if !ok {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, []map[string]any{{"sha": rp.Branches[branch], "commit": map[string]any{"committer": map[string]any{"date": at}}}})
}

func (s *Server) branch(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	s.mu.Lock()
	sha, ok := rp.Branches[r.PathValue("branch")]
	s.mu.Unlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "Branch not found")
		return
	}
	writeJSON(w, map[string]any{"name": r.PathValue("branch"), "commit": map[string]any{"sha": sha}})
}

func (s *Server) contents(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = rp.DefaultBranch
	}
	p := strings.Trim(r.PathValue("path"), "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	files := rp.Files[rp.branchOf(ref)]
	if data, ok := files[p]; ok {
		writeJSON(w, map[string]any{"type": "file", "name": path.Base(p), "path": p, "sha": blobSHA(data), "size": len(data),
			"encoding": "base64", "content": base64.StdEncoding.EncodeToString(data)})
		return
	}
	seen := map[string]bool{}
	var items []map[string]any
	for fp, data := range files {
		rest, ok := strings.CutPrefix(fp, p+"/")
		if p == "" {
			rest, ok = fp, true
		}
		if !ok {
			continue
		}
		name, _, isDir := strings.Cut(rest, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		full := strings.TrimPrefix(p+"/"+name, "/")
		if isDir {
			items = append(items, map[string]any{"type": "dir", "name": name, "path": full, "sha": "", "size": 0})
		} else {
			items = append(items, map[string]any{"type": "file", "name": name, "path": full, "sha": blobSHA(data), "size": len(data)})
		}
	}
	if len(items) == 0 {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writeJSON(w, items)
}

func blobSHA(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// branchOf accetta un branch o lo sha della sua testa (il connettore legge a un commit
// preciso) e restituisce il branch; il finto GitHub conserva i file solo per branch.
func (r *Repo) branchOf(ref string) string {
	if _, ok := r.Branches[ref]; ok {
		return ref
	}
	for b, sha := range r.Branches {
		if sha == ref {
			return b
		}
	}
	return ref
}
