package api

import (
	"errors"
	"io"
	"net/http"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	glconn "regia/internal/modules/devsecops/connectors/gitlab"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
)

// Configurazione → GitLab (DevSecOps, fase 5b; replica/devsecops.md §2 e §4). Le regole
// stanno in internal/modules/devsecops/connectors/gitlab; il job periodico parte da
// serve.go. Stessi permessi del connettore GitHub: lettura con tenant.admin (anche con un
// token API), modifiche della configurazione solo con una sessione; «Sincronizza ora»,
// scoperta e anteprima anche con un token API con tenant.admin. I profili di pipeline sono
// quelli di tutti i connettori (/devsecops/pipeline-profiles, devsecops_github.go).

func (s *Server) gitlabInit(o Options) {
	s.gl = o.GitLab
	if s.gl == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		ing := o.DevSecOpsIngestor
		if ing == nil && s.dso != nil {
			ing = s.dso
		}
		s.gl = glconn.New(glconn.Options{DB: o.DB, Box: box, Ingestor: ing, Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log})
	}
}

func (s *Server) gitlabRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops/gitlab"
	mux.HandleFunc("GET "+b+"/integrations", adminOnly(s.glList))
	mux.HandleFunc("POST "+b+"/integrations", adminSession(s.glCreate))
	mux.HandleFunc("GET "+b+"/integrations/{id}", adminOnly(s.glGet))
	mux.HandleFunc("PATCH "+b+"/integrations/{id}", adminSession(s.glUpdate))
	mux.HandleFunc("DELETE "+b+"/integrations/{id}", adminSession(s.glDelete))
	mux.HandleFunc("POST "+b+"/integrations/{id}/test", adminSession(s.glTest))
	mux.HandleFunc("POST "+b+"/integrations/{id}/webhook-secret", adminSession(s.glWebhookSecret))
	mux.HandleFunc("GET "+b+"/integrations/{id}/projects", adminOnly(s.glProjects))
	mux.HandleFunc("POST "+b+"/integrations/{id}/discover", adminOnly(s.glDiscover))
	mux.HandleFunc("PUT "+b+"/integrations/{id}/projects/{project}", adminSession(s.glMapProject))
	mux.HandleFunc("POST "+b+"/integrations/{id}/projects/accept", adminSession(s.glAccept))
	mux.HandleFunc("POST "+b+"/integrations/{id}/sync", adminOnly(s.glSync))
	mux.HandleFunc("GET "+b+"/integrations/{id}/runs", adminOnly(s.glRuns))
	mux.HandleFunc("POST "+b+"/integrations/{id}/preview", adminOnly(s.glPreview))
	mux.HandleFunc("GET "+b+"/presets", adminOnly(s.glPresets))
	// Webhook di GitLab: niente sessione né token API, vale X-Gitlab-Token.
	mux.HandleFunc("POST "+b+"/webhook/{integration}", s.glWebhook)
}

func glActor(r *http.Request, p *auth.Principal) glconn.Actor {
	return glconn.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

// glError traduce gli errori del connettore in risposte (stessa forma di quelli di GitHub).
func (s *Server) glError(w http.ResponseWriter, r *http.Request, err error) { s.ghError(w, r, err) }

// glRemote mostra come errore del servizio remoto (non interno) ciò che GitLab ha risposto.
func (s *Server) glRemote(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if !errors.As(err, &ge) {
		s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "gitlab.error", Msg: err.Error()})
		return
	}
	s.ghError(w, r, err)
}

func (s *Server) glList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gl.List(r.Context(), p.TenantID)
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allow_private": s.gl.AllowPrivate(), "dev": s.gl.Dev(),
		"storing": s.gl.Storing(), "base_url": s.adm.baseURL})
}

func (s *Server) glGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	it, err := s.gl.Get(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) glCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in glconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.gl.Create(r.Context(), glActor(r, p), in)
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) glUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in glconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.gl.Update(r.Context(), glActor(r, p), r.PathValue("id"), in); err != nil {
		s.glError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) glDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.gl.Delete(r.Context(), glActor(r, p), r.PathValue("id")); err != nil {
		s.glError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) glTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gl.Test(r.Context(), glActor(r, p), r.PathValue("id"))
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) glWebhookSecret(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	secret, err := s.gl.GenerateWebhookSecret(r.Context(), glActor(r, p), r.PathValue("id"))
	if err != nil {
		s.glError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) glProjects(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gl.Projects(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) glDiscover(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gl.Discover(r.Context(), glActor(r, p), r.PathValue("id"))
	if err != nil {
		s.glRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) glMapProject(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in glconn.MapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	pr, err := s.gl.MapProject(r.Context(), glActor(r, p), r.PathValue("id"), r.PathValue("project"), in)
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (s *Server) glAccept(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := s.gl.AcceptSuggestions(r.Context(), glActor(r, p), r.PathValue("id"), in.IDs)
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": n})
}

func (s *Server) glSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	run, err := s.gl.SyncNow(r.Context(), glActor(r, p), r.PathValue("id"), r.URL.Query().Get("full") == "true")
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run})
}

func (s *Server) glRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gl.Runs(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.glError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) glPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in glconn.PreviewInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.gl.Preview(r.Context(), glActor(r, p), r.PathValue("id"), in)
	if err != nil {
		s.glRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) glPresets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	writeJSON(w, http.StatusOK, map[string]any{"presets": glconn.Presets()})
}

func (s *Server) glWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, glconn.MaxWebhookBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "corpo del webhook troppo grande")
		return
	}
	res := s.gl.HandleWebhook(r.Context(), r.PathValue("integration"), r.Header, body, true)
	writeJSON(w, res.Status, res)
}
