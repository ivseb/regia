package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	almconn "regia/internal/modules/devsecops/connectors/alm"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
	"regia/internal/platform/secretbox"
)

// Configurazione → ALM/Quality Center (DevSecOps, connettore in sola lettura). Le regole stanno
// in internal/modules/devsecops/connectors/alm; il job periodico parte da serve.go.
//
//   - Configurazione, unità, sincronizzazioni: come gli altri connettori, lettura con
//     tenant.admin (anche con un token API), modifiche solo con una sessione.
//   - Test set letti: chi legge le applicazioni (element.read).

func (s *Server) almInit(o Options) {
	s.alm = o.ALM
	if s.alm == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		s.alm = almconn.New(almconn.Options{DB: o.DB, Box: box, Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log})
	}
}

func (s *Server) almRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops/alm"
	mux.HandleFunc("GET "+b+"/integrations", adminOnly(s.almList))
	mux.HandleFunc("POST "+b+"/integrations", adminSession(s.almCreate))
	mux.HandleFunc("GET "+b+"/integrations/{id}", adminOnly(s.almGet))
	mux.HandleFunc("PATCH "+b+"/integrations/{id}", adminSession(s.almUpdate))
	mux.HandleFunc("DELETE "+b+"/integrations/{id}", adminSession(s.almDelete))
	mux.HandleFunc("POST "+b+"/integrations/{id}/test", adminSession(s.almTest))
	mux.HandleFunc("GET "+b+"/integrations/{id}/units", adminOnly(s.almUnits))
	mux.HandleFunc("PUT "+b+"/integrations/{id}/units/{unit}", adminSession(s.almMapUnit))
	mux.HandleFunc("POST "+b+"/integrations/{id}/units/accept", adminSession(s.almAccept))
	mux.HandleFunc("POST "+b+"/integrations/{id}/discover", adminOnly(s.almDiscover))
	mux.HandleFunc("POST "+b+"/integrations/{id}/sync", adminOnly(s.almSync))
	mux.HandleFunc("GET "+b+"/integrations/{id}/runs", adminOnly(s.almRuns))
	mux.HandleFunc("GET "+b+"/test-sets", inTenant(s.almTestSets))
}

func almActor(r *http.Request, p *auth.Principal) almconn.Actor {
	return almconn.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

// almRemote mostra come errore del servizio remoto ciò che ALM ha risposto.
func (s *Server) almRemote(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if !errors.As(err, &ge) {
		s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "alm.error", Msg: err.Error()})
		return
	}
	s.ghError(w, r, err)
}

func (s *Server) almList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.alm.List(r.Context(), p.TenantID)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allow_private": s.alm.AllowPrivate(), "dev": s.alm.Dev(), "proxy": s.alm.Proxy()})
}

func (s *Server) almGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	it, err := s.alm.Get(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) almCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in almconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.alm.Create(r.Context(), almActor(r, p), in)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) almUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in almconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.alm.Update(r.Context(), almActor(r, p), r.PathValue("id"), in); err != nil {
		s.snError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) almDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.alm.Delete(r.Context(), almActor(r, p), r.PathValue("id")); err != nil {
		s.snError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) almTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.alm.Test(r.Context(), almActor(r, p), r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) almUnits(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.alm.Units(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) almMapUnit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in almconn.MapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.alm.MapUnit(r.Context(), almActor(r, p), r.PathValue("id"), r.PathValue("unit"), in)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) almAccept(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := s.alm.AcceptSuggestions(r.Context(), almActor(r, p), r.PathValue("id"), in.IDs)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": n})
}

func (s *Server) almDiscover(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.alm.Discover(r.Context(), almActor(r, p), r.PathValue("id"))
	if err != nil {
		s.almRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) almSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	run, err := s.alm.SyncNow(r.Context(), almActor(r, p), r.PathValue("id"), r.URL.Query().Get("full") == "true")
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run})
}

func (s *Server) almRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.alm.Runs(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// almTestSets: ?integration=<id>, ?change=<numero o alias>, ?limit=N.
func (s *Server) almTestSets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !actorOf(r, p).Has(rbac.ElementRead) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	q := r.URL.Query()
	f := almconn.TestSetFilter{IntegrationID: q.Get("integration"), Change: q.Get("change"), Limit: snLimit(r)}
	var items []almconn.TestSet
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		items, err = almconn.TestSets(r.Context(), tx, f)
		return err
	})
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
