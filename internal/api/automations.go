package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Automazioni (docs/sviluppo/automazioni.md): anteprima di una regola, ultime esecuzioni delle
// regole a tempo e periodiche, impostazioni del tenant. Tutto con process.admin.

func (s *Server) previewRule(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Type      string `json:"type"`
		On        string `json:"on"`
		Condition string `json:"condition"`
		Days      int    `json:"days"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.RulePreview
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.PreviewRule(r.Context(), tx, actorOf(r, p), in.Type, in.On, in.Condition, in.Days)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ruleRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var out []engine.RuleRunItem
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.RuleRuns(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("rule"), limit)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if out == nil {
		out = []engine.RuleRunItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) automationSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out engine.AutomationSettings
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = engine.GetAutomationSettings(r.Context(), tx)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setAutomationSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.AutomationSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	var out engine.AutomationSettings
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = engine.SetAutomationSettings(r.Context(), tx, actorOf(r, p), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
