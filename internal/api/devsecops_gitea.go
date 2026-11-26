package api

import (
	"errors"
	"io"
	"net/http"

	gtconn "regia/internal/modules/devsecops/connectors/gitea"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
)

// Configurazione → Gitea (DevSecOps; replica/devsecops.md §2 e §4). Le regole
// stanno in internal/modules/devsecops/connectors/gitea; il job periodico parte da
// serve.go. Stessi permessi del connettore GitHub: lettura con tenant.admin (anche con un
// token API), modifiche della configurazione solo con una sessione; «Sincronizza ora»,
// scoperta e anteprima anche con un token API con tenant.admin. I profili di pipeline sono
// quelli di tutti i connettori (/devsecops/pipeline-profiles, devsecops_github.go).

func (s *Server) giteaInit(o Options) {
	s.gt = o.Gitea
	if s.gt == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		ing := o.DevSecOpsIngestor
		if ing == nil && s.dso != nil {
			ing = s.dso
		}
		s.gt = gtconn.New(gtconn.Options{DB: o.DB, Box: box, Ingestor: ing, Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log})
	}
}

func (s *Server) giteaRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops/gitea"
	mux.HandleFunc("GET "+b+"/integrations", adminOnly(s.gtList))
	mux.HandleFunc("POST "+b+"/integrations", adminSession(s.gtCreate))
	mux.HandleFunc("GET "+b+"/integrations/{id}", adminOnly(s.gtGet))
	mux.HandleFunc("PATCH "+b+"/integrations/{id}", adminSession(s.gtUpdate))
	mux.HandleFunc("DELETE "+b+"/integrations/{id}", adminSession(s.gtDelete))
	mux.HandleFunc("POST "+b+"/integrations/{id}/test", adminSession(s.gtTest))
	mux.HandleFunc("POST "+b+"/integrations/{id}/webhook-secret", adminSession(s.gtWebhookSecret))
	// «repositories» come nel connettore GitHub; «repos» resta come alias per i client già scritti.
	for _, seg := range []string{"repositories", "repos"} {
		mux.HandleFunc("GET "+b+"/integrations/{id}/"+seg, adminOnly(s.gtRepos))
		mux.HandleFunc("PUT "+b+"/integrations/{id}/"+seg+"/{repo}", adminSession(s.gtMapRepo))
		mux.HandleFunc("POST "+b+"/integrations/{id}/"+seg+"/accept", adminSession(s.gtAccept))
	}
	mux.HandleFunc("POST "+b+"/integrations/{id}/discover", adminOnly(s.gtDiscover))
	mux.HandleFunc("POST "+b+"/integrations/{id}/sync", adminOnly(s.gtSync))
	mux.HandleFunc("GET "+b+"/integrations/{id}/runs", adminOnly(s.gtRuns))
	mux.HandleFunc("POST "+b+"/integrations/{id}/preview", adminOnly(s.gtPreview))
	mux.HandleFunc("GET "+b+"/presets", adminOnly(s.gtPresets))
	// Webhook di Gitea: niente sessione né token API, vale la firma X-Gitea-Signature.
	mux.HandleFunc("POST "+b+"/webhook/{integration}", s.gtWebhook)
}

func gtActor(r *http.Request, p *auth.Principal) gtconn.Actor {
	return gtconn.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

// gtError traduce gli errori del connettore in risposte (stessa forma di quelli di GitHub).
func (s *Server) gtError(w http.ResponseWriter, r *http.Request, err error) { s.ghError(w, r, err) }

// gtRemote mostra come errore del servizio remoto (non interno) ciò che Gitea ha risposto.
func (s *Server) gtRemote(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if !errors.As(err, &ge) {
		s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "gitea.error", Msg: err.Error()})
		return
	}
	s.ghError(w, r, err)
}

func (s *Server) gtList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gt.List(r.Context(), p.TenantID)
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allow_private": s.gt.AllowPrivate(), "dev": s.gt.Dev(),
		"storing": s.gt.Storing(), "base_url": s.adm.baseURL})
}

func (s *Server) gtGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	it, err := s.gt.Get(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) gtCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in gtconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.gt.Create(r.Context(), gtActor(r, p), in)
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) gtUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in gtconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.gt.Update(r.Context(), gtActor(r, p), r.PathValue("id"), in); err != nil {
		s.gtError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gtDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.gt.Delete(r.Context(), gtActor(r, p), r.PathValue("id")); err != nil {
		s.gtError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gtTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gt.Test(r.Context(), gtActor(r, p), r.PathValue("id"))
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) gtWebhookSecret(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	secret, err := s.gt.GenerateWebhookSecret(r.Context(), gtActor(r, p), r.PathValue("id"))
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) gtRepos(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gt.Repos(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) gtDiscover(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.gt.Discover(r.Context(), gtActor(r, p), r.PathValue("id"))
	if err != nil {
		s.gtRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) gtMapRepo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in gtconn.MapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	pr, err := s.gt.MapRepo(r.Context(), gtActor(r, p), r.PathValue("id"), r.PathValue("repo"), in)
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (s *Server) gtAccept(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := s.gt.AcceptSuggestions(r.Context(), gtActor(r, p), r.PathValue("id"), in.IDs)
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": n})
}

func (s *Server) gtSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	run, err := s.gt.SyncNow(r.Context(), gtActor(r, p), r.PathValue("id"), r.URL.Query().Get("full") == "true")
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run})
}

func (s *Server) gtRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.gt.Runs(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.gtError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) gtPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in gtconn.PreviewInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.gt.Preview(r.Context(), gtActor(r, p), r.PathValue("id"), in)
	if err != nil {
		s.gtRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) gtPresets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	writeJSON(w, http.StatusOK, map[string]any{"presets": gtconn.Presets()})
}

func (s *Server) gtWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, gtconn.MaxWebhookBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "corpo del webhook troppo grande")
		return
	}
	res := s.gt.HandleWebhook(r.Context(), r.PathValue("integration"), r.Header, body, true)
	writeJSON(w, res.Status, res)
}
