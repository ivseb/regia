// Package fakegitlab è un finto GitLab (API REST v4) per i test e per lo sviluppo
// (regia dev fake-gitlab). Copre solo le chiamate che usa il connettore di Regia:
// token (Bearer o PRIVATE-TOKEN) con scope e scadenza, gruppi e sottogruppi, progetti con
// paginazione offset e keyset (Link rel="next"), Release con link, pipeline, job e
// artefatti (archivio zip), Environments e Deployments, limiti di velocità (RateLimit-*,
// 429 con Retry-After) simulabili. I dati sono inventati.
package fakegitlab

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server è lo stato del finto GitLab.
type Server struct {
	mu sync.Mutex

	// BasePath è il prefisso dell'API (default /api/v4).
	BasePath string
	// Version è la versione dichiarata da /version.
	Version string
	// Tokens sono i token accettati, con le loro proprietà.
	Tokens map[string]*Token
	// NoTokenInfo spegne /personal_access_tokens/self (istanze precedenti alla 15.5).
	NoTokenInfo bool

	// Limiti di velocità: Limit richieste per finestra di ResetEvery (0 = nessuna intestazione).
	Limit      int
	ResetEvery time.Duration
	remaining  int
	resetAt    time.Time

	Now func() time.Time

	groups   map[string]bool
	projects []*Project
	nextID   int64
	requests []string
	failNext []injected
}

// Token è un token accettato.
type Token struct {
	Name      string
	Scopes    []string
	ExpiresAt string // AAAA-MM-GG
	Revoked   bool
}

type injected struct {
	prefix     string
	status     int
	retryAfter int
}

// Project è un progetto del finto GitLab.
type Project struct {
	ID            int64
	Full          string // gruppo/sottogruppo/nome
	Name          string
	DefaultBranch string
	Visibility    string
	Archived      bool
	Fork          bool
	Description   string
	Releases      []*Release // dalla più vecchia alla più recente
	Pipelines     []*Pipeline
	Deployments   []*Deployment
	Envs          []string
}

// Release è una Release.
type Release struct {
	Tag, Name, Author, Commit string
	At                        time.Time
	Upcoming                  bool
	Links                     []*Link
}

// Link è un link di una Release che punta a un file ospitato dal finto GitLab.
type Link struct {
	ID   int64
	Name string
	Data []byte
}

// Pipeline è una pipeline.
type Pipeline struct {
	ID      int64
	Ref     string
	SHA     string
	Status  string
	Created time.Time
	Updated time.Time
	Jobs    []*Job
}

// Job è un job; Artifacts sono i file dell'archivio (nome → contenuto).
type Job struct {
	ID           int64
	Name, Stage  string
	Status       string
	AllowFailure bool
	Finished     time.Time
	Artifacts    map[string][]byte
	// Expired: gli artefatti sono scaduti (il download risponde 404).
	Expired bool
}

// Deployment è un deployment.
type Deployment struct {
	ID      int64
	Env     string
	Ref     string
	SHA     string
	Status  string
	Tag     bool
	Created time.Time
	Updated time.Time
}

// New crea un finto GitLab senza dati.
func New() *Server {
	return &Server{
		BasePath: "/api/v4", Version: "17.5.1-ee", Tokens: map[string]*Token{}, Now: time.Now,
		groups: map[string]bool{}, nextID: 100,
	}
}

func (s *Server) id() int64 { s.nextID++; return s.nextID }

// AddToken registra un token accettato.
func (s *Server) AddToken(value, name string, scopes ...string) *Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &Token{Name: name, Scopes: scopes}
	s.Tokens[value] = t
	return t
}

// AddGroup aggiunge un gruppo (anche un sottogruppo: acme/piattaforma).
func (s *Server) AddGroup(full string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[full] = true
}

// AddProject aggiunge un progetto con il branch predefinito main.
func (s *Server) AddProject(full string) *Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &Project{ID: s.id(), Full: full, Name: full[strings.LastIndex(full, "/")+1:], DefaultBranch: "main", Visibility: "private"}
	s.projects = append(s.projects, p)
	if i := strings.LastIndex(full, "/"); i > 0 {
		s.groups[full[:i]] = true
	}
	return p
}

// RemoveProject toglie un progetto (per provare i progetti scomparsi).
func (s *Server) RemoveProject(full string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.projects {
		if p.Full == full {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return
		}
	}
}

func (s *Server) project(full string) *Project {
	for _, p := range s.projects {
		if p.Full == full {
			return p
		}
	}
	panic("fakegitlab: progetto inesistente " + full)
}

// ProjectID restituisce l'id numerico di un progetto.
func (s *Server) ProjectID(full string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.project(full).ID
}

// AddRelease pubblica una Release; links sono i file allegati come link (nome → contenuto).
func (s *Server) AddRelease(full, tag, author string, at time.Time, links map[string][]byte) *Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.project(full)
	rel := &Release{Tag: tag, Name: tag, Author: author, At: at.UTC(), Commit: randSHA(s.nextID)}
	names := make([]string, 0, len(links))
	for n := range links {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rel.Links = append(rel.Links, &Link{ID: s.id(), Name: n, Data: links[n]})
	}
	p.Releases = append(p.Releases, rel)
	return rel
}

// AddLink aggiunge un link a una Release esistente (un allegato arrivato dopo).
func (s *Server) AddLink(full, tag, name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.project(full).Releases {
		if r.Tag == tag {
			r.Links = append(r.Links, &Link{ID: s.id(), Name: name, Data: data})
		}
	}
}

// JobSpec descrive un job di una pipeline.
type JobSpec struct {
	Name, Stage  string
	Status       string
	AllowFailure bool
	Artifacts    map[string][]byte
}

// AddPipeline aggiunge una pipeline su un ref (branch o tag) e ne restituisce l'id.
func (s *Server) AddPipeline(full, ref, status string, at time.Time, jobs ...JobSpec) *Pipeline {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.project(full)
	pl := &Pipeline{ID: s.id(), Ref: ref, SHA: randSHA(s.nextID), Status: status, Created: at.UTC(), Updated: at.UTC()}
	for _, j := range jobs {
		st := j.Stage
		if st == "" {
			st = "test"
		}
		pl.Jobs = append(pl.Jobs, &Job{ID: s.id(), Name: j.Name, Stage: st, Status: j.Status, AllowFailure: j.AllowFailure,
			Finished: at.UTC(), Artifacts: j.Artifacts})
	}
	p.Pipelines = append(p.Pipelines, pl)
	return pl
}

// SetPipelineStatus cambia lo stato di una pipeline (e dei job ancora aperti, se conclusa).
func (s *Server) SetPipelineStatus(full string, id int64, status string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pl := range s.project(full).Pipelines {
		if pl.ID == id {
			pl.Status, pl.Updated = status, at.UTC()
			for _, j := range pl.Jobs {
				if j.Status == "running" || j.Status == "pending" {
					j.Status = status
					j.Finished = at.UTC()
				}
			}
		}
	}
}

// AddEnvironment registra un ambiente senza deployment.
func (s *Server) AddEnvironment(full, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.project(full)
	for _, e := range p.Envs {
		if e == name {
			return
		}
	}
	p.Envs = append(p.Envs, name)
}

// AddDeployment registra un deployment (e l'ambiente).
func (s *Server) AddDeployment(full, env, ref, sha string, tag bool, status string, at time.Time) *Deployment {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.project(full)
	d := &Deployment{ID: s.id(), Env: env, Ref: ref, SHA: sha, Tag: tag, Status: status, Created: at.UTC(), Updated: at.UTC()}
	p.Deployments = append(p.Deployments, d)
	found := false
	for _, e := range p.Envs {
		found = found || e == env
	}
	if !found {
		p.Envs = append(p.Envs, env)
	}
	return d
}

// SetDeploymentStatus cambia lo stato di un deployment.
func (s *Server) SetDeploymentStatus(full string, id int64, status string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.project(full).Deployments {
		if d.ID == id {
			d.Status, d.Updated = status, at.UTC()
		}
	}
}

// FailNext fa rispondere con uno stato di errore alle prossime richieste il cui percorso
// inizia con prefix (dopo il prefisso dell'API).
func (s *Server) FailNext(prefix string, status, retryAfter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, injected{prefix, status, retryAfter})
}

// SetRemaining simula una finestra di limiti quasi esaurita.
func (s *Server) SetRemaining(n int, resetIn time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Limit == 0 {
		s.Limit = 2000
	}
	s.remaining, s.resetAt = n, s.Now().Add(resetIn)
}

// Requests restituisce (e, con reset, svuota) l'elenco delle richieste ricevute.
func (s *Server) Requests(reset bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.requests...)
	if reset {
		s.requests = nil
	}
	return out
}

// Count conta le richieste ricevute il cui percorso contiene sub.
func (s *Server) Count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return n
}

func randSHA(seed int64) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 40)
	x := uint64(seed)*6364136223846793005 + 1442695040888963407
	for i := range b {
		x = x*6364136223846793005 + 1442695040888963407
		b[i] = hex[(x>>33)&15]
	}
	return string(b)
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

// Handler è l'handler HTTP dell'API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	b := s.BasePath
	mux.HandleFunc("GET "+b+"/version", s.authed(s.version))
	mux.HandleFunc("GET "+b+"/personal_access_tokens/self", s.authed(s.tokenSelf))
	mux.HandleFunc("GET "+b+"/groups/{id}", s.authed(s.group))
	mux.HandleFunc("GET "+b+"/groups/{id}/projects", s.authed(s.groupProjects))
	mux.HandleFunc("GET "+b+"/projects/{id}", s.authed(s.projectGet))
	mux.HandleFunc("GET "+b+"/projects/{id}/releases", s.authed(s.releases))
	mux.HandleFunc("GET "+b+"/projects/{id}/releases/{tag}", s.authed(s.releaseByTag))
	mux.HandleFunc("GET "+b+"/projects/{id}/pipelines", s.authed(s.pipelines))
	mux.HandleFunc("GET "+b+"/projects/{id}/pipelines/{pid}/jobs", s.authed(s.jobs))
	mux.HandleFunc("GET "+b+"/projects/{id}/jobs/{jid}/artifacts", s.authed(s.artifacts))
	mux.HandleFunc("GET "+b+"/projects/{id}/deployments", s.authed(s.deployments))
	mux.HandleFunc("GET "+b+"/projects/{id}/environments", s.authed(s.environments))
	mux.HandleFunc("GET "+b+"/projects/{id}/links/{lid}/download", s.authed(s.linkDownload))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	})
}

func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func token(r *http.Request) string {
	if t := r.Header.Get("PRIVATE-TOKEN"); t != "" {
		return t
	}
	if a, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(a)
	}
	return ""
}

// authed controlla il token, applica i limiti di velocità e gli errori iniettati.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
		rel := strings.TrimPrefix(r.URL.Path, s.BasePath)
		for i, f := range s.failNext {
			if strings.HasPrefix(rel, f.prefix) {
				s.failNext = append(s.failNext[:i], s.failNext[i+1:]...)
				s.mu.Unlock()
				if f.retryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(f.retryAfter))
				}
				jsonError(w, f.status, "errore simulato")
				return
			}
		}
		if _, ok := s.Tokens[token(r)]; !ok || token(r) == "" {
			s.mu.Unlock()
			jsonError(w, http.StatusUnauthorized, "401 Unauthorized")
			return
		}
		if s.Limit > 0 {
			now := s.Now()
			if s.resetAt.IsZero() || now.After(s.resetAt) {
				s.remaining, s.resetAt = s.Limit, now.Add(s.ResetEvery)
			}
			if s.remaining <= 0 {
				reset := s.resetAt
				s.mu.Unlock()
				w.Header().Set("RateLimit-Limit", strconv.Itoa(s.Limit))
				w.Header().Set("RateLimit-Remaining", "0")
				w.Header().Set("RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
				w.Header().Set("Retry-After", strconv.Itoa(int(reset.Sub(now).Seconds())+1))
				jsonError(w, http.StatusTooManyRequests, "Retry later")
				return
			}
			s.remaining--
			w.Header().Set("RateLimit-Limit", strconv.Itoa(s.Limit))
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(s.remaining))
			w.Header().Set("RateLimit-Reset", strconv.FormatInt(s.resetAt.Unix(), 10))
		}
		defer s.mu.Unlock()
		h(w, r)
	}
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"version": s.Version, "revision": "abcdef12", "enterprise": strings.HasSuffix(s.Version, "-ee")})
}

func (s *Server) tokenSelf(w http.ResponseWriter, r *http.Request) {
	if s.NoTokenInfo {
		jsonError(w, http.StatusNotFound, "404 Not Found")
		return
	}
	t := s.Tokens[token(r)]
	var exp any
	if t.ExpiresAt != "" {
		exp = t.ExpiresAt
	}
	writeJSON(w, map[string]any{"id": 9, "name": t.Name, "revoked": t.Revoked, "active": !t.Revoked, "scopes": t.Scopes, "expires_at": exp})
}

func (s *Server) group(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("id")
	if !s.groups[full] {
		jsonError(w, http.StatusNotFound, "404 Group Not Found")
		return
	}
	writeJSON(w, map[string]any{"id": 1, "name": full[strings.LastIndex(full, "/")+1:], "full_path": full})
}

func (s *Server) projectJSON(p *Project, host string) map[string]any {
	var forked any
	if p.Fork {
		forked = map[string]any{"id": 1}
	}
	return map[string]any{"id": p.ID, "name": p.Name, "path": p.Name, "path_with_namespace": p.Full, "default_branch": p.DefaultBranch,
		"web_url": host + "/" + p.Full, "description": p.Description, "archived": p.Archived, "visibility": p.Visibility, "forked_from_project": forked}
}

// page legge page e per_page.
func page(r *http.Request) (n, per int) {
	n, _ = strconv.Atoi(r.URL.Query().Get("page"))
	per, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if n < 1 {
		n = 1
	}
	if per < 1 || per > 100 {
		per = 20
	}
	return
}

// paginate scrive una pagina (offset) con l'intestazione Link.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) {
	n, per := page(r)
	lo, hi := (n-1)*per, n*per
	if lo > len(items) {
		lo = len(items)
	}
	if hi > len(items) {
		hi = len(items)
	}
	if hi < len(items) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(n+1))
		q.Set("per_page", strconv.Itoa(per))
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, origin(r), r.URL.Path, q.Encode()))
	}
	if lo == hi {
		writeJSON(w, []T{})
		return
	}
	writeJSON(w, items[lo:hi])
}

func (s *Server) groupProjects(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("id")
	if !s.groups[full] {
		jsonError(w, http.StatusNotFound, "404 Group Not Found")
		return
	}
	var list []*Project
	for _, p := range s.projects {
		if strings.HasPrefix(p.Full, full+"/") && (r.URL.Query().Get("include_subgroups") == "true" || !strings.Contains(strings.TrimPrefix(p.Full, full+"/"), "/")) {
			list = append(list, p)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	host := origin(r)
	if r.URL.Query().Get("pagination") == "keyset" {
		// Keyset: id_after, l'ordine è per id; il Link porta il cursore.
		after, _ := strconv.ParseInt(r.URL.Query().Get("id_after"), 10, 64)
		_, per := page(r)
		var out []map[string]any
		var last int64
		more := false
		for _, p := range list {
			if p.ID <= after {
				continue
			}
			if len(out) == per {
				more = true
				break
			}
			out = append(out, s.projectJSON(p, host))
			last = p.ID
		}
		if more {
			q := r.URL.Query()
			q.Set("id_after", strconv.FormatInt(last, 10))
			q.Set("per_page", strconv.Itoa(per))
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, host, r.URL.Path, q.Encode()))
		}
		if out == nil {
			out = []map[string]any{}
		}
		writeJSON(w, out)
		return
	}
	var out []map[string]any
	for _, p := range list {
		out = append(out, s.projectJSON(p, host))
	}
	paginate(w, r, out)
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) *Project {
	raw := r.PathValue("id")
	id, _ := strconv.ParseInt(raw, 10, 64)
	for _, p := range s.projects {
		if p.ID == id || p.Full == raw {
			return p
		}
	}
	jsonError(w, http.StatusNotFound, "404 Project Not Found")
	return nil
}

func (s *Server) projectGet(w http.ResponseWriter, r *http.Request) {
	if p := s.find(w, r); p != nil {
		writeJSON(w, s.projectJSON(p, origin(r)))
	}
}

func (s *Server) releaseJSON(p *Project, rel *Release, host string) map[string]any {
	links := []map[string]any{}
	for _, l := range rel.Links {
		u := fmt.Sprintf("%s%s/projects/%d/links/%d/download", host, s.BasePath, p.ID, l.ID)
		links = append(links, map[string]any{"id": l.ID, "name": l.Name, "url": u, "direct_asset_url": u, "link_type": "other"})
	}
	return map[string]any{"tag_name": rel.Tag, "name": rel.Name, "description": "", "created_at": rel.At.Format(time.RFC3339), "released_at": rel.At.Format(time.RFC3339),
		"upcoming_release": rel.Upcoming, "author": map[string]any{"username": rel.Author}, "commit": map[string]any{"id": rel.Commit},
		"assets": map[string]any{"count": len(links), "sources": []any{}, "links": links},
		"_links": map[string]any{"self": host + "/" + p.Full + "/-/releases/" + rel.Tag}}
}

func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	var out []map[string]any
	for i := len(p.Releases) - 1; i >= 0; i-- {
		out = append(out, s.releaseJSON(p, p.Releases[i], origin(r)))
	}
	paginate(w, r, out)
}

func (s *Server) releaseByTag(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	for _, rel := range p.Releases {
		if rel.Tag == r.PathValue("tag") {
			writeJSON(w, s.releaseJSON(p, rel, origin(r)))
			return
		}
	}
	jsonError(w, http.StatusNotFound, "404 Not Found")
}

func (s *Server) linkDownload(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("lid"), 10, 64)
	for _, rel := range p.Releases {
		for _, l := range rel.Links {
			if l.ID == id {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(l.Data)
				return
			}
		}
	}
	jsonError(w, http.StatusNotFound, "404 Not Found")
}

func (s *Server) pipelines(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	ref := r.URL.Query().Get("ref")
	var out []map[string]any
	for i := len(p.Pipelines) - 1; i >= 0; i-- {
		pl := p.Pipelines[i]
		if ref != "" && pl.Ref != ref {
			continue
		}
		out = append(out, map[string]any{"id": pl.ID, "sha": pl.SHA, "ref": pl.Ref, "status": pl.Status, "created_at": pl.Created.Format(time.RFC3339),
			"updated_at": pl.Updated.Format(time.RFC3339), "web_url": fmt.Sprintf("%s/%s/-/pipelines/%d", origin(r), p.Full, pl.ID)})
	}
	paginate(w, r, out)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	pid, _ := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	for _, pl := range p.Pipelines {
		if pl.ID != pid {
			continue
		}
		var out []map[string]any
		for _, j := range pl.Jobs {
			m := map[string]any{"id": j.ID, "name": j.Name, "stage": j.Stage, "status": j.Status, "allow_failure": j.AllowFailure,
				"web_url": fmt.Sprintf("%s/%s/-/jobs/%d", origin(r), p.Full, j.ID), "finished_at": j.Finished.Format(time.RFC3339)}
			if len(j.Artifacts) > 0 {
				m["artifacts_file"] = map[string]any{"filename": "artifacts.zip", "size": len(zipOf(j.Artifacts))}
			}
			out = append(out, m)
		}
		paginate(w, r, out)
		return
	}
	jsonError(w, http.StatusNotFound, "404 Not Found")
}

func zipOf(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, _ := zw.Create("report/" + n)
		_, _ = f.Write(files[n])
	}
	_ = zw.Close()
	return buf.Bytes()
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	jid, _ := strconv.ParseInt(r.PathValue("jid"), 10, 64)
	for _, pl := range p.Pipelines {
		for _, j := range pl.Jobs {
			if j.ID == jid && len(j.Artifacts) > 0 && !j.Expired {
				w.Header().Set("Content-Type", "application/zip")
				_, _ = w.Write(zipOf(j.Artifacts))
				return
			}
		}
	}
	jsonError(w, http.StatusNotFound, "404 Not Found")
}

func (s *Server) deployments(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	env := r.URL.Query().Get("environment")
	list := append([]*Deployment(nil), p.Deployments...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Updated.After(list[j].Updated) })
	var out []map[string]any
	for _, d := range list {
		if env != "" && d.Env != env {
			continue
		}
		out = append(out, map[string]any{"id": d.ID, "iid": d.ID, "ref": d.Ref, "sha": d.SHA, "status": d.Status,
			"created_at": d.Created.Format(time.RFC3339), "updated_at": d.Updated.Format(time.RFC3339),
			"environment": map[string]any{"id": 1, "name": d.Env}, "deployable": map[string]any{"tag": d.Tag}})
	}
	paginate(w, r, out)
}

func (s *Server) environments(w http.ResponseWriter, r *http.Request) {
	p := s.find(w, r)
	if p == nil {
		return
	}
	var out []map[string]any
	for i, e := range p.Envs {
		tier := "other"
		if e == "production" || e == "prod" {
			tier = "production"
		}
		out = append(out, map[string]any{"id": i + 1, "name": e, "tier": tier, "state": "available"})
	}
	paginate(w, r, out)
}

// Escape serve ai test per costruire percorsi di gruppo.
func Escape(s string) string { return url.PathEscape(s) }
