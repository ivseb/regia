package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
)

// Configurazione → Integrazioni → GitHub (DevSecOps, fase 1; replica/devsecops.md §2 e §4).
// Le regole stanno in internal/modules/devsecops/connectors/github; il job periodico parte
// da serve.go. Lettura con tenant.admin (anche con un token API), modifiche della
// configurazione solo con una sessione; "Sincronizza ora", scoperta e anteprima anche con
// un token API con tenant.admin (automazioni).

func (s *Server) githubInit(o Options) {
	s.gh = o.GitHub
	if s.gh == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		ing := o.DevSecOpsIngestor
		if ing == nil && s.dso != nil {
			ing = s.dso // il modulo devsecops (devsecops.go) è l'Ingestor del contratto
		}
		s.gh = ghconn.New(ghconn.Options{DB: o.DB, Box: box, Ingestor: ing, Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log})
	}
}

func (s *Server) githubRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops/github"
	mux.HandleFunc("GET "+b+"/integrations", adminOnly(s.ghList))
	mux.HandleFunc("POST "+b+"/integrations", adminSession(s.ghCreate))
	mux.HandleFunc("GET "+b+"/integrations/{id}", adminOnly(s.ghGet))
	mux.HandleFunc("PATCH "+b+"/integrations/{id}", adminSession(s.ghUpdate))
	mux.HandleFunc("DELETE "+b+"/integrations/{id}", adminSession(s.ghDelete))
	mux.HandleFunc("POST "+b+"/integrations/{id}/test", adminSession(s.ghTest))
	mux.HandleFunc("POST "+b+"/integrations/{id}/webhook-secret", adminSession(s.ghWebhookSecret))
	mux.HandleFunc("GET "+b+"/integrations/{id}/repositories", adminOnly(s.ghRepos))
	mux.HandleFunc("POST "+b+"/integrations/{id}/discover", adminOnly(s.ghDiscover))
	mux.HandleFunc("PUT "+b+"/integrations/{id}/repositories/{repo}", adminSession(s.ghMapRepo))
	mux.HandleFunc("POST "+b+"/integrations/{id}/repositories/accept", adminSession(s.ghAcceptSuggestions))
	mux.HandleFunc("POST "+b+"/integrations/{id}/sync", adminOnly(s.ghSync))
	mux.HandleFunc("GET "+b+"/integrations/{id}/runs", adminOnly(s.ghRuns))
	mux.HandleFunc("POST "+b+"/integrations/{id}/preview", adminOnly(s.ghPreview))
	// Webhook di GitHub: niente sessione né token, vale la firma HMAC.
	mux.HandleFunc("POST "+b+"/webhook/{integration}", s.ghWebhook)

	const p = "/api/v1/devsecops/pipeline-profiles"
	mux.HandleFunc("GET "+p, adminOnly(s.profList))
	mux.HandleFunc("POST "+p, adminSession(s.profCreate))
	mux.HandleFunc("POST "+p+"/import", adminSession(s.profImport))
	mux.HandleFunc("POST "+p+"/validate", adminOnly(s.profValidate))
	mux.HandleFunc("GET "+p+"/{id}", adminOnly(s.profGet))
	mux.HandleFunc("PUT "+p+"/{id}", adminSession(s.profUpdate))
	mux.HandleFunc("DELETE "+p+"/{id}", adminSession(s.profDelete))
	mux.HandleFunc("GET "+p+"/{id}/versions/{n}", adminOnly(s.profVersion))
	mux.HandleFunc("GET "+p+"/{id}/export", adminOnly(s.profExport))
}

func ghActor(r *http.Request, p *auth.Principal) ghconn.Actor {
	return ghconn.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

// ghError traduce gli errori del connettore in risposte.
func (s *Server) ghError(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if !errors.As(err, &ge) {
		s.internal(w, r, err)
		return
	}
	body := map[string]any{"type": "about:blank", "title": http.StatusText(ge.Status), "status": ge.Status, "detail": ge.Msg,
		"code": ge.Code, "request_id": requestID(r.Context())}
	if ge.Field != "" {
		body["field"] = ge.Field
	}
	if len(ge.Problems) > 0 {
		ps := make([]map[string]string, 0, len(ge.Problems))
		for _, pr := range ge.Problems {
			ps = append(ps, map[string]string{"level": "error", "path": pr.Path, "message": pr.Message})
		}
		body["problems"] = ps
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(ge.Status)
	writeJSONBody(w, body)
}

func (s *Server) ghList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gh.List(r.Context(), p.TenantID)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allow_private": s.gh.AllowPrivate(), "dev": s.gh.Dev(),
		"storing": s.gh.Storing(), "base_url": s.adm.baseURL})
}

func (s *Server) ghGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	it, err := s.gh.Get(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) ghCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.gh.Create(r.Context(), ghActor(r, p), in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) ghUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.gh.Update(r.Context(), ghActor(r, p), r.PathValue("id"), in); err != nil {
		s.ghError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ghDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.gh.Delete(r.Context(), ghActor(r, p), r.PathValue("id")); err != nil {
		s.ghError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ghTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gh.Test(r.Context(), ghActor(r, p), r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) ghWebhookSecret(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	secret, err := s.gh.GenerateWebhookSecret(r.Context(), ghActor(r, p), r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) ghRepos(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gh.Repositories(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ghDiscover(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gh.Discover(r.Context(), ghActor(r, p), r.PathValue("id"))
	if err != nil {
		var ge *ghconn.Error
		if !errors.As(err, &ge) {
			// Un errore di GitHub (credenziali, rete) va mostrato, non è un errore interno.
			s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "github.error", Msg: err.Error()})
			return
		}
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) ghMapRepo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.MapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	repo, err := s.gh.MapRepository(r.Context(), ghActor(r, p), r.PathValue("id"), r.PathValue("repo"), in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) ghAcceptSuggestions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := s.gh.AcceptSuggestions(r.Context(), ghActor(r, p), r.PathValue("id"), in.IDs)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": n})
}

func (s *Server) ghSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	full := r.URL.Query().Get("full") == "true"
	run, err := s.gh.SyncNow(r.Context(), ghActor(r, p), r.PathValue("id"), full)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run})
}

func (s *Server) ghRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gh.Runs(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ghPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.PreviewInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.gh.Preview(r.Context(), ghActor(r, p), r.PathValue("id"), in)
	if err != nil {
		var ge *ghconn.Error
		if !errors.As(err, &ge) {
			s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "github.error", Msg: err.Error()})
			return
		}
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ghWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ghconn.MaxWebhookBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "corpo del webhook troppo grande")
		return
	}
	res := s.gh.HandleWebhook(r.Context(), r.PathValue("integration"), r.Header, body, true)
	writeJSON(w, res.Status, res)
}

// ---------------------------------------------------------------------
// Profili di pipeline
// ---------------------------------------------------------------------

func (s *Server) profList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gh.Profiles(r.Context(), p.TenantID)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "presets": profile.Presets(), "format": profile.Format,
		"record_fields": profile.RecordFields, "gitops_fields": profile.GitOpsFields})
}

func (s *Server) profGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	pr, versions, err := s.gh.GetProfile(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": pr, "versions": versions})
}

func (s *Server) profVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "numero di versione non valido")
		return
	}
	v, err := s.gh.GetProfileVersion(r.Context(), p.TenantID, r.PathValue("id"), n)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) profCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.ProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.gh.CreateProfile(r.Context(), ghActor(r, p), in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) profUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.ProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.gh.UpdateProfile(r.Context(), ghActor(r, p), r.PathValue("id"), in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *Server) profDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.gh.DeleteProfile(r.Context(), ghActor(r, p), r.PathValue("id")); err != nil {
		s.ghError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) profValidate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	_, ps := ghconn.ValidateDefinition(in.Definition)
	if ps == nil {
		ps = []profile.Problem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": len(ps) == 0, "problems": ps})
}

func (s *Server) profExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	ex, err := s.gh.ExportProfile(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	b, _ := json.MarshalIndent(ex, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="profilo-`+ex.Key+`-v`+strconv.Itoa(ex.Version)+`.json"`)
	_, _ = w.Write(append(b, '\n'))
}

func (s *Server) profImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Document json.RawMessage `json:"document"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := s.gh.ImportProfile(r.Context(), ghActor(r, p), in.Document)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}
