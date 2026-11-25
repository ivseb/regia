package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/work"
	"regia/internal/platform/auth"
)

// Board per team: configurazione delle colonne (bozza, anteprima, pubblicazione) e modelli.
func (s *Server) workBoardTeamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/work/spaces/{key}/board/config", inTenant(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.BoardConfig, error) {
			return s.work.BoardConfigOf(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		})
	}))
	mux.HandleFunc("PUT /api/v1/work/spaces/{key}/board/draft", inTenant(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		var in work.BoardSettings
		if !decodeJSON(w, r, &in) {
			return
		}
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BoardConfig, error) {
			return s.work.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
		})
	}))
	mux.HandleFunc("DELETE /api/v1/work/spaces/{key}/board/draft", inTenant(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BoardConfig, error) {
			return s.work.DiscardDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		})
	}))
	mux.HandleFunc("POST /api/v1/work/spaces/{key}/board/preview", inTenant(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		var in work.PreviewInput
		if !decodeJSON(w, r, &in) {
			return
		}
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BoardPreview, error) {
			return s.work.PreviewBoard(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
		})
	}))
	mux.HandleFunc("POST /api/v1/work/spaces/{key}/board/publish", inTenant(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BoardConfig, error) {
			return s.work.PublishDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		})
	}))
}
