package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/approval"
	"regia/internal/modules/devsecops"
	"regia/internal/platform/auth"
)

// Vulnerabilità governate (replica/devsecops.md §3.2): cruscotto, elenco, azioni in blocco,
// eccezioni con approvazione, politica di SLA e di escalation con l'anteprima.

func (s *Server) vulnRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devsecops/vuln/dashboard", inTenant(s.vulnDashboard))
	mux.HandleFunc("GET /api/v1/devsecops/vuln/items", inTenant(s.vulnItems))
	mux.HandleFunc("GET /api/v1/devsecops/vuln/items/{id}", inTenant(s.vulnItem))
	mux.HandleFunc("POST /api/v1/devsecops/vuln/bulk", inTenant(s.vulnBulk))
	mux.HandleFunc("GET /api/v1/devsecops/vuln/exceptions", inTenant(s.vulnExceptions))
	mux.HandleFunc("POST /api/v1/devsecops/vuln/exceptions", inTenant(s.vulnRequestException))
	mux.HandleFunc("PATCH /api/v1/devsecops/vuln/exceptions/{id}", inTenant(s.vulnExceptionRisk))
	mux.HandleFunc("POST /api/v1/devsecops/vuln/exceptions/{id}/revoke", inTenant(s.vulnRevokeException))
	mux.HandleFunc("GET /api/v1/devsecops/vuln/risks", inTenant(s.vulnRisks))
	mux.HandleFunc("GET /api/v1/devsecops/vuln/config", inTenant(s.vulnConfig))
	mux.HandleFunc("PUT /api/v1/devsecops/vuln/config/policy", inTenant(s.vulnSavePolicy))
	mux.HandleFunc("POST /api/v1/devsecops/vuln/config/policy/preview", inTenant(s.vulnPreviewPolicy))
	mux.HandleFunc("PUT /api/v1/devsecops/vuln/config/exception-policy", inTenant(s.vulnSaveExceptionPolicy))
}

func vulnFilter(r *http.Request, p *auth.Principal) devsecops.VulnFilter {
	q := r.URL.Query()
	n := func(k string) int { v, _ := strconv.Atoi(q.Get(k)); return v }
	return devsecops.VulnFilter{Team: q.Get("team"), Application: q.Get("application"), Severity: q.Get("severity"), Stage: q.Get("stage"),
		Due: q.Get("due"), Q: q.Get("q"), Mine: q.Get("mine") == "1", Sort: q.Get("sort"), Limit: n("limit"), Offset: n("offset")}
}

func (s *Server) vulnDashboard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.VulnDashboard(r.Context(), tx, actorOf(r, p), vulnFilter(r, p))
	})
}

func (s *Server) vulnItems(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.ListVulns(r.Context(), tx, actorOf(r, p), vulnFilter(r, p))
	})
}

func (s *Server) vulnItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.VulnDetail(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) vulnBulk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.BulkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.BulkAction(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) vulnExceptions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.dso.ListExceptions(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("status"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) vulnRequestException(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.ExceptionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		items, err := s.dso.RequestException(r.Context(), tx, actorOf(r, p), in)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) vulnExceptionRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Risk string `json:"risk"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SetExceptionRisk(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Risk)
	})
}

func (s *Server) vulnRevokeException(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Comment string `json:"comment"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.RevokeException(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Comment)
	})
}

func (s *Server) vulnRisks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.dso.RiskChoices(r.Context(), tx, r.URL.Query().Get("q"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) vulnConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.VulnConfig(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) vulnSavePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.VulnPolicy
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		pol, n, err := s.dso.SaveVulnPolicy(r.Context(), tx, actorOf(r, p), in)
		return map[string]any{"policy": pol, "recomputed": n}, err
	})
}

func (s *Server) vulnPreviewPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.VulnPolicy
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.PreviewVulnPolicy(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) vulnSaveExceptionPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Rules []approval.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.UpdateExceptionPolicy(r.Context(), tx, actorOf(r, p), in.Rules)
	})
}
