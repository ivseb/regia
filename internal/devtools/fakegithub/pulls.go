package fakegithub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Scritture del finto GitHub, per l'adozione guidata di Regia (replica/devsecops.md §3.4):
// branch, file con un commit e pull request. Come su GitHub servono i permessi «Contents» e
// «Pull requests» in scrittura dell'installazione (Server.Permissions): senza, 403
// «Resource not accessible by integration». I token personali (PATs) possono scrivere.

// PullRequest è una pull request del finto GitHub.
type PullRequest struct {
	Number    int
	Title     string
	Body      string
	Head      string
	Base      string
	State     string // open | closed
	Merged    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  *time.Time
	MergedAt  *time.Time
}

// GrantWrite dà all'installazione i permessi in scrittura usati dall'adozione guidata.
func (s *Server) GrantWrite() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Permissions["contents"], s.Permissions["pull_requests"] = "write", "write"
}

// Pulls restituisce le pull request di un repository, dalla prima aperta.
func (s *Server) Pulls(full string) []PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PullRequest
	if r := s.repos[full]; r != nil {
		for _, p := range r.Pulls {
			out = append(out, *p)
		}
	}
	return out
}

// File legge un file di un branch (nil se non esiste).
func (s *Server) File(full, branch, file string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	if r == nil {
		return nil
	}
	return r.Files[r.branchOf(branch)][file]
}

// MergePull unisce la pull request: i file del branch passano al branch di base.
func (s *Server) MergePull(full string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[full]
	for _, p := range r.Pulls {
		if p.Number != number || p.State != "open" {
			continue
		}
		if r.Files[p.Base] == nil {
			r.Files[p.Base] = map[string][]byte{}
		}
		for f, data := range r.Files[p.Head] {
			r.Files[p.Base][f] = data
			r.FileCommits[p.Base+"\x00"+f] = s.Now().UTC()
		}
		r.Branches[p.Base] = randSHA()
		now := s.Now().UTC()
		p.State, p.Merged, p.MergedAt, p.ClosedAt, p.UpdatedAt = "closed", true, &now, &now, now
	}
}

// ClosePull chiude la pull request senza unirla.
func (s *Server) ClosePull(full string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.repos[full].Pulls {
		if p.Number == number && p.State == "open" {
			now := s.Now().UTC()
			p.State, p.ClosedAt, p.UpdatedAt = "closed", &now, now
		}
	}
}

func (s *Server) mayWrite(w http.ResponseWriter, r *http.Request, perm string) bool {
	tok := bearer(r)
	s.mu.Lock()
	_, inst := s.tokens[tok]
	ok := s.PATs[tok] || (inst && s.Permissions[perm] == "write")
	s.mu.Unlock()
	if !ok {
		jsonError(w, http.StatusForbidden, "Resource not accessible by integration")
	}
	return ok
}

func readBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(dst); err != nil {
		jsonError(w, http.StatusBadRequest, "Problems parsing JSON")
		return false
	}
	return true
}

// createRef: POST /git/refs, un branch che parte dai file di un altro commit.
func (s *Server) createRef(w http.ResponseWriter, r *http.Request) {
	if !s.mayWrite(w, r, "contents") {
		return
	}
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	var in struct{ Ref, SHA string }
	if !readBody(w, r, &in) {
		return
	}
	name, ok := strings.CutPrefix(in.Ref, "refs/heads/")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok || name == "" {
		jsonError(w, http.StatusUnprocessableEntity, "Reference is invalid")
		return
	}
	if _, exists := rp.Branches[name]; exists {
		jsonError(w, http.StatusUnprocessableEntity, "Reference already exists")
		return
	}
	base := ""
	for b, sha := range rp.Branches {
		if sha == in.SHA {
			base = b
		}
	}
	if base == "" {
		jsonError(w, http.StatusUnprocessableEntity, "Object does not exist")
		return
	}
	rp.Branches[name] = in.SHA
	rp.Files[name] = map[string][]byte{}
	for f, data := range rp.Files[base] {
		rp.Files[name][f] = data
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"ref": in.Ref, "object": map[string]string{"sha": in.SHA, "type": "commit"}})
}

// putContents: PUT /contents/{path}, un file con un commit. Se il file esiste serve lo sha.
func (s *Server) putContents(w http.ResponseWriter, r *http.Request) {
	if !s.mayWrite(w, r, "contents") {
		return
	}
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	var in struct {
		Message, Content, Branch, SHA string
	}
	if !readBody(w, r, &in) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.Content)
	if err != nil || in.Message == "" {
		jsonError(w, http.StatusUnprocessableEntity, "content is not valid Base64 or message is missing")
		return
	}
	p := strings.Trim(r.PathValue("path"), "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	branch := in.Branch
	if branch == "" {
		branch = rp.DefaultBranch
	}
	if _, ok := rp.Branches[branch]; !ok {
		jsonError(w, http.StatusNotFound, "Branch not found")
		return
	}
	if rp.Files[branch] == nil {
		rp.Files[branch] = map[string][]byte{}
	}
	status := http.StatusCreated
	if old, exists := rp.Files[branch][p]; exists {
		status = http.StatusOK
		if in.SHA == "" {
			jsonError(w, http.StatusUnprocessableEntity, "Invalid request.\n\n\"sha\" wasn't supplied.")
			return
		}
		if in.SHA != blobSHA(old) {
			jsonError(w, http.StatusConflict, p+" does not match "+in.SHA)
			return
		}
	}
	rp.Files[branch][p] = data
	sha := randSHA()
	rp.Branches[branch] = sha
	rp.FileCommits[branch+"\x00"+p] = s.Now().UTC()
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"content": map[string]any{"path": p, "sha": blobSHA(data)}, "commit": map[string]any{"sha": sha, "message": in.Message}})
}

func (s *Server) pullJSON(rp *Repo, p *PullRequest) map[string]any {
	full := rp.Owner + "/" + rp.Name
	return map[string]any{"number": p.Number, "title": p.Title, "body": p.Body, "state": p.State, "merged": p.Merged,
		"html_url": fmt.Sprintf("https://github.example.test/%s/pull/%d", full, p.Number), "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
		"closed_at": p.ClosedAt, "merged_at": p.MergedAt, "head": map[string]any{"ref": p.Head}, "base": map[string]any{"ref": p.Base}}
}

// createPull: POST /pulls.
func (s *Server) createPull(w http.ResponseWriter, r *http.Request) {
	if !s.mayWrite(w, r, "pull_requests") {
		return
	}
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	var in struct{ Title, Head, Base, Body string }
	if !readBody(w, r, &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := rp.Branches[in.Head]; !ok || in.Title == "" {
		jsonError(w, http.StatusUnprocessableEntity, "Validation Failed: head or title is invalid")
		return
	}
	if _, ok := rp.Branches[in.Base]; !ok {
		jsonError(w, http.StatusUnprocessableEntity, "Validation Failed: base is invalid")
		return
	}
	now := s.Now().UTC()
	p := &PullRequest{Number: len(rp.Pulls) + 1, Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, State: "open", CreatedAt: now, UpdatedAt: now}
	rp.Pulls = append(rp.Pulls, p)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, s.pullJSON(rp, p))
}

// getPull: GET /pulls/{n}.
func (s *Server) getPull(w http.ResponseWriter, r *http.Request) {
	rp := s.find(w, r)
	if rp == nil {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range rp.Pulls {
		if p.Number == n {
			writeJSON(w, s.pullJSON(rp, p))
			return
		}
	}
	jsonError(w, http.StatusNotFound, "Not Found")
}
