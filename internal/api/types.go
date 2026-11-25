package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
)

// Tipi di elemento configurabili (Configurazione → Tipi). La configurazione richiede
// tenant.admin; i nomi per i menu li leggono tutti i membri.

func (s *Server) typesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/types", inTenant(s.listTypes))
	mux.HandleFunc("POST /api/v1/types", inTenant(s.createType))
	mux.HandleFunc("PATCH /api/v1/types/{key}", inTenant(s.updateType))
	mux.HandleFunc("GET /api/v1/type-names", inTenant(s.typeNames))
	mux.HandleFunc("GET /api/v1/state-labels", inTenant(s.stateLabels))
}

func (s *Server) listTypes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.eng.ListTypeConfigs(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items, "creatable_modules": engine.CreatableModules}, err
	})
}

func (s *Server) createType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.TypeCreate
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.TypeConfig, error) {
		return s.eng.CreateType(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.TypeUpdate
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.TypeConfig, error) {
		return s.eng.UpdateType(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) typeNames(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := engine.TypeNames(r.Context(), tx, r.URL.Query().Get("lang"))
		return map[string]any{"items": items}, err
	})
}

// stateLabels: i nomi per lingua delle fasi (nome → lingua → etichetta), per mostrare lo stato nelle liste.
func (s *Server) stateLabels(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := engine.StateLabels(r.Context(), tx)
		return map[string]any{"items": items}, err
	})
}
