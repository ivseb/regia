package api

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Editor visuale dei processi (sdlc.md §10): metadati, catalogo per la creazione,
// validazione dal vivo senza salvataggio.

func (s *Server) processMeta(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *engine.ProcessMeta
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.ProcessMeta(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) processCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *engine.ProcessCatalog
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.Catalog(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) checkDefinition(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out []engine.Problem
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.CheckDefinition(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Definition)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": out})
}
