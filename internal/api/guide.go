package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/rbac"
)

// Versioni in vigore e guide allegate ai processi (migrazione 0069; docs/sviluppo/versioni-vigore.md).
//
//	POST   /api/v1/processes/{ref}/publish/preview           anteprima della pubblicazione (process.admin)
//	PATCH  /api/v1/processes/{ref}/versions/{number}/vigore  data di entrata in vigore (programmata) e di fine
//	GET    /api/v1/processes/{ref}/guides                    lettura per tutti
//	POST   /api/v1/processes/{ref}/guides                    solo process.admin
//	PATCH  /api/v1/processes/{ref}/guides/{id}
//	DELETE /api/v1/processes/{ref}/guides/{id}
//
// La pubblicazione stessa (POST /processes/{ref}/publish) accetta effective_from, effective_to e transition.

func (s *Server) guideRoutes(mux *http.ServeMux) {
	pa := func(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
		return can(rbac.ProcessAdmin, h)
	}
	mux.HandleFunc("POST /api/v1/processes/{ref}/publish/preview", pa(s.previewPublish))
	mux.HandleFunc("PATCH /api/v1/processes/{ref}/versions/{number}/vigore", pa(s.setVigore))
	mux.HandleFunc("GET /api/v1/processes/{ref}/guides", inTenant(s.listGuides))
	mux.HandleFunc("POST /api/v1/processes/{ref}/guides", pa(s.createGuide))
	mux.HandleFunc("PATCH /api/v1/processes/{ref}/guides/{id}", pa(s.updateGuide))
	mux.HandleFunc("DELETE /api/v1/processes/{ref}/guides/{id}", pa(s.deleteGuide))
}

// publishBody è il corpo della pubblicazione e della sua anteprima.
type publishBody struct {
	Notes         string                 `json:"notes"`
	EffectiveFrom string                 `json:"effective_from"`
	EffectiveTo   string                 `json:"effective_to"`
	Transition    *engine.TransitionRule `json:"transition"`
}

func (b publishBody) input() (engine.PublishInput, error) {
	in := engine.PublishInput{Notes: b.Notes, Transition: b.Transition}
	var err error
	if in.EffectiveFrom, err = engine.ParseVigoreDate(b.EffectiveFrom); err != nil {
		return in, err
	}
	if in.EffectiveTo, err = engine.ParseVigoreDate(b.EffectiveTo); err != nil {
		return in, engine.InvalidValue("effective_to", "data non valida: usa AAAA-MM-GG")
	}
	return in, nil
}

func (s *Server) previewPublish(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var body publishBody
	if !decodeJSON(w, r, &body) {
		return
	}
	in, err := body.input()
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*engine.PublishPreview, error) {
		return s.eng.PreviewPublish(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) setVigore(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n < 1 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid", "numero di versione non valido")
		return
	}
	var body struct {
		EffectiveFrom *string `json:"effective_from"`
		EffectiveTo   *string `json:"effective_to"` // "" = toglie la data di fine
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var patch engine.VigorePatch
	if body.EffectiveFrom != nil {
		if patch.From, err = engine.ParseVigoreDate(*body.EffectiveFrom); err != nil {
			s.engineError(w, r, err)
			return
		}
	}
	if body.EffectiveTo != nil {
		if patch.To, err = engine.ParseVigoreDate(*body.EffectiveTo); err != nil {
			s.engineError(w, r, engine.InvalidValue("effective_to", "data non valida: usa AAAA-MM-GG"))
			return
		}
		patch.ClearTo = patch.To == nil
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.ProcessInfo, error) {
		return s.eng.SetVigore(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), n, patch)
	})
}

func (s *Server) listGuides(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		list, err := s.eng.Guides(r.Context(), tx, r.PathValue("ref"))
		return map[string]any{"items": list}, err
	})
}

func (s *Server) createGuide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.GuideInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Guide, error) {
		return s.eng.CreateGuide(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) updateGuide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.GuideInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Guide, error) {
		return s.eng.UpdateGuide(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("id"), in)
	})
}

func (s *Server) deleteGuide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (struct{}, error) {
		return struct{}{}, s.eng.DeleteGuide(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("id"))
	})
}
