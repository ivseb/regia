package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Configurazione dei processi (sdlc.md §1-2, §7): elenco, bozza, pubblicazione,
// export e import, migrazione delle richieste in corso, prova delle condizioni.

func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out []engine.ProcessInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.ListProcesses(r.Context(), tx, actorOf(r, p))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "checks": s.eng.GuardNames(), "actions": s.eng.ActionKinds()})
}

func (s *Server) getProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *engine.ProcessInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.GetProcess(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getProcessVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n < 1 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid", "numero di versione non valido")
		return
	}
	var out *engine.Workflow
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.VersionDefinition(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), n)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.ProcessInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.ProcessInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.CreateProcess(r.Context(), tx, actorOf(r, p), in, nil)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.ProcessInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.ProcessInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.UpdateProcess(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
		Notes      string          `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.ProcessInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Definition, in.Notes)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) discardDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return s.eng.DiscardDraft(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// problemsError risponde 422 con l'elenco dei problemi della definizione.
func (s *Server) problemsError(w http.ResponseWriter, r *http.Request, err error, problems []engine.Problem) {
	var ve *engine.ValidationError
	if len(problems) == 0 || !errors.As(err, &ve) {
		s.engineError(w, r, err)
		return
	}
	code := "validation"
	if ve.Code != "" {
		code = ve.Code
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
		"detail": ve.Error(), "code": code, "field": ve.Field, "problems": problems, "request_id": requestID(r.Context())})
}

func (s *Server) publishProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var body publishBody
	if !decodeJSON(w, r, &body) {
		return
	}
	in, err := body.input()
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	var out *engine.ProcessInfo
	var problems []engine.Problem
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, problems, err = s.eng.PublishWith(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return err
	})
	if err != nil {
		s.problemsError(w, r, err, problems)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"process": out, "problems": problems})
}

func (s *Server) exportProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, _ := strconv.Atoi(r.URL.Query().Get("version"))
	var out *engine.Bundle
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.Export(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), n)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-v%d.json"`, out.Key, out.Version))
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (s *Server) importProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	b, err := engine.ParseBundle(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	publish := r.URL.Query().Get("publish") == "true"
	var out *engine.ProcessInfo
	var problems []engine.Problem
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, problems, err = s.eng.Import(r.Context(), tx, actorOf(r, p), b, publish)
		return err
	})
	if err != nil {
		s.problemsError(w, r, err, problems)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"process": out, "problems": problems})
}

func (s *Server) previewMigration(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.MigrationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.MigrationPlan
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.PreviewMigration(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) migrateProcess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.MigrationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.MigrationPlan
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.Migrate(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) checkCondition(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Condition string `json:"condition"`
		Element   string `json:"element"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *engine.ConditionCheck
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.CheckCondition(r.Context(), tx, actorOf(r, p), in.Condition, in.Element)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
