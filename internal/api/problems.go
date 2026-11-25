package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/problems"
	"regia/internal/platform/auth"
)

// Dal problema all'azione (replica/usabilita.md, proposta 5; migrazione 0056).
//
//	GET  /api/v1/problems/context?kind=&ref=[&control=]  azioni proposte, testi precompilati, collegamenti
//	POST /api/v1/problems/work-items                     crea il lavoro nel team e lo collega
//	POST /api/v1/problems/links                          registra un'azione svolta altrove (PR di adozione)
//	GET  /api/v1/elements/{ref}/problems                 i problemi da cui nasce un elemento di lavoro
func (s *Server) problemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/problems/context", inTenant(s.problemContext))
	mux.HandleFunc("POST /api/v1/problems/work-items", inTenant(s.problemWorkItem))
	mux.HandleFunc("POST /api/v1/problems/links", inTenant(s.problemLink))
	mux.HandleFunc("GET /api/v1/elements/{ref}/problems", inTenant(s.problemOrigins))
}

func (s *Server) problemContext(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*problems.Context, error) {
		return s.prob.Context(langCtx(r.Context(), tx, p), tx, actorOf(r, p), q.Get("kind"), q.Get("ref"), q.Get("control"))
	})
}

func (s *Server) problemWorkItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in problems.WorkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*problems.Link, error) {
		return s.prob.CreateWork(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) problemLink(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in problems.LinkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*problems.Link, error) {
		return s.prob.AddLink(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) problemOrigins(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.prob.Origins(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"items": items}, err
	})
}
