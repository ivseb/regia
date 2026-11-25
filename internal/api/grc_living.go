package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/grc"
	"regia/internal/platform/auth"
)

// Controlli vivi (ondata 3, proposta 4): regola e proposta di efficacia, evidenze dal grafo,
// controlli di un rischio, mappatura guidata. I permessi li verifica il modulo.

func (s *Server) grcLivingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/grc/proposals", inTenant(s.grcProposals))
	mux.HandleFunc("POST /api/v1/grc/living/sync", inTenant(s.grcLivingSync))
	mux.HandleFunc("GET /api/v1/grc/controls/{ref}/proposal", inTenant(s.grcProposal))
	mux.HandleFunc("PUT /api/v1/grc/controls/{ref}/proposal/rule", inTenant(s.grcProposalRule))
	mux.HandleFunc("DELETE /api/v1/grc/controls/{ref}/proposal/rule", inTenant(s.grcProposalRuleDelete))
	mux.HandleFunc("POST /api/v1/grc/controls/{ref}/proposal/decision", inTenant(s.grcProposalDecide))
	mux.HandleFunc("GET /api/v1/grc/risks/{ref}/controls", inTenant(s.grcRiskControls))
	mux.HandleFunc("GET /api/v1/grc/mapping/workbench", inTenant(s.grcWorkbench))
	mux.HandleFunc("POST /api/v1/grc/mapping/suggestions", inTenant(s.grcSuggest))
	mux.HandleFunc("POST /api/v1/grc/mapping/bulk", inTenant(s.grcMapBulk))
}

func (s *Server) grcProposals(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Proposals(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) grcLivingSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Control string `json:"control"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.SyncResult, error) {
		return s.grc.SyncLiving(r.Context(), tx, actorOf(r, p), in.Control)
	})
}

func (s *Server) grcProposal(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.Proposal, error) {
		return s.grc.Proposal(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcProposalRule(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.EffRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Proposal, error) {
		return s.grc.SetEffectivenessRule(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) grcProposalRuleDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Proposal, error) {
		return s.grc.DeleteEffectivenessRule(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcProposalDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.DecideInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Proposal, error) {
		return s.grc.Decide(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) grcRiskControls(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.RiskControlsView, error) {
		return s.grc.RiskControls(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcWorkbench(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.Workbench, error) {
		return s.grc.Workbench(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) grcSuggest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Requirements []string `json:"requirements"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Suggest(r.Context(), tx, actorOf(r, p), in.Requirements)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) grcMapBulk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.BulkMapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.MapBulk(r.Context(), tx, actorOf(r, p), in)
	})
}
