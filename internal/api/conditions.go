package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Costruttore di condizioni: dati disponibili per un tipo di elemento e conteggio dal vivo.

func (s *Server) conditionFacts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	typ := r.URL.Query().Get("type")
	var facts []engine.FactInfo
	var sample string
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		facts, sample, err = s.eng.TypeFacts(langCtx(r.Context(), tx, p), tx, actorOf(r, p), typ)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if facts == nil {
		facts = []engine.FactInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": facts, "sample": sample})
}

func (s *Server) countCondition(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Condition string `json:"condition"`
		Type      string `json:"type"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.ConditionCount
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.CountCondition(r.Context(), tx, actorOf(r, p), in.Condition, in.Type)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
