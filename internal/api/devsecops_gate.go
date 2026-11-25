package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Gate di rilascio (piano, passo 21, fase 4; devsecops.md §3.5): la pipeline chiede il
// permesso prima del deploy con un token API e il permesso devsecops.gate; le regole per
// ambiente, gli approvatori degli override e la mappatura delle evidenze si configurano da
// Configurazione → Gate di rilascio. Esempi curl in docs/devsecops-api.md.

func (s *Server) gateInit(o Options) {
	if s.dso != nil {
		s.dso.PublicURL = o.BaseURL
		s.dso.SetGate(s.grc, o.Approvals)
	}
}

func (s *Server) gateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/devsecops/gate", can(rbac.DevsecopsGate, s.gateDecide))
	mux.HandleFunc("POST /api/v1/devsecops/gate/preview", inTenant(s.gatePreview))
	mux.HandleFunc("GET /api/v1/devsecops/gate/config", inTenant(s.gateConfig))
	mux.HandleFunc("PUT /api/v1/devsecops/gate/policies/{env}", inTenant(s.gateSavePolicy))
	mux.HandleFunc("DELETE /api/v1/devsecops/gate/policies/{env}", inTenant(s.gateDeletePolicy))
	mux.HandleFunc("PUT /api/v1/devsecops/gate/approvers", inTenant(s.gateSaveApprovers))
	mux.HandleFunc("PUT /api/v1/devsecops/gate/evidence-map", inTenant(s.gateSaveEvidenceMap))
	mux.HandleFunc("GET /api/v1/devsecops/gate/decisions", inTenant(s.gateDecisions))
	mux.HandleFunc("POST /api/v1/devsecops/gate/overrides", inTenant(s.gateRequestOverride))
	mux.HandleFunc("POST /api/v1/devsecops/gate/overrides/{id}/revoke", inTenant(s.gateRevokeOverride))
	mux.HandleFunc("GET /api/v1/devsecops/elements/{ref}/gate", inTenant(s.gatePanel))
}

type gateBody struct {
	Format string `json:"format"`
	// Application: codice del catalogo (API-CLI) o chiave (ARC-5). Service può bastare.
	Application string `json:"application"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Change      string `json:"change"`
	Request     string `json:"request"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
}

func (b gateBody) input() devsecops.GateInput {
	return devsecops.GateInput{Application: b.Application, Service: b.Service, Environment: b.Environment, Change: b.Change,
		Request: b.Request, Version: b.Version, Commit: b.Commit}
}

// gateDecide è la chiamata della pipeline: 200 con pass o warn, 409 con fail (stesso corpo).
func (s *Server) gateDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in gateBody
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	var out *devsecops.GateOutcome
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.dso.Decide(r.Context(), tx, actorOf(r, p), in.input())
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	status := http.StatusOK
	if out.Result == "fail" {
		status = http.StatusConflict
	}
	writeJSON(w, status, struct {
		Format string `json:"format"`
		*devsecops.GateOutcome
	}{contract.Version, out})
}

// gatePreview: "cosa succederebbe ora", senza registrare nulla. Con "policy" valuta una bozza.
func (s *Server) gatePreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		gateBody
		Policy *devsecops.GatePolicy `json:"policy"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	a := actorOf(r, p)
	if !a.Has(devsecops.PermRead) {
		writeProblemParams(w, r, http.StatusForbidden, "auth.forbidden", "permesso mancante: "+devsecops.PermRead, map[string]any{"perm": devsecops.PermRead})
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Evaluate(r.Context(), tx, a, in.input(), in.Policy)
	})
}

func (s *Server) gateConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.GateConfig(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
	})
}

func (s *Server) gateSavePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.GatePolicy
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SaveGatePolicy(r.Context(), tx, actorOf(r, p), r.PathValue("env"), in)
	})
}

func (s *Server) gateDeletePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.dso.DeleteGatePolicy(r.Context(), tx, actorOf(r, p), r.PathValue("env"))
	})
}

func (s *Server) gateSaveApprovers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Approvers []devsecops.GateApprover `json:"approvers"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		a, err := s.dso.SaveApprovers(r.Context(), tx, actorOf(r, p), in.Approvers)
		return map[string]any{"approvers": a}, err
	})
}

func (s *Server) gateSaveEvidenceMap(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []devsecops.EvidenceMapItem `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SaveEvidenceMap(r.Context(), tx, actorOf(r, p), in.Items)
	})
}

func (s *Server) gateDecisions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.dso.Decisions(r.Context(), tx, actorOf(r, p), devsecops.DecisionFilter{
			Application: q.Get("application"), Request: q.Get("request"), Result: q.Get("result"), Limit: limit})
		return map[string]any{"items": items}, err
	})
}

func (s *Server) gateRequestOverride(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Hours    int    `json:"hours"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		return s.dso.RequestOverride(r.Context(), tx, actorOf(r, p), in.Decision, in.Reason, in.Hours)
	})
}

func (s *Server) gateRevokeOverride(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.dso.RevokeOverride(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) gatePanel(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.GatePanel(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}
