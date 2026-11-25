package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops"
	"regia/internal/platform/auth"
)

// Metriche DORA delle quattro chiavi (devsecops.md §3.6, fase 5a): cruscotto, approfondimento,
// impostazioni con anteprima e ricalcolo.
func (s *Server) doraRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devsecops/dora", inTenant(s.doraDashboard))
	mux.HandleFunc("GET /api/v1/devsecops/dora/drilldown", inTenant(s.doraDrilldown))
	mux.HandleFunc("GET /api/v1/devsecops/dora/settings", inTenant(s.doraSettings))
	mux.HandleFunc("PUT /api/v1/devsecops/dora/settings", inTenant(s.doraSaveSettings))
	mux.HandleFunc("POST /api/v1/devsecops/dora/preview", inTenant(s.doraPreview))
	mux.HandleFunc("POST /api/v1/devsecops/dora/recompute", inTenant(s.doraRecompute))
}

func (s *Server) doraDashboard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	period, scope := q.Get("period"), q.Get("scope")
	if period == "" {
		period = devsecops.PeriodMonth
	}
	if scope == "" {
		scope = devsecops.ScopeTenant
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Dashboard(r.Context(), tx, actorOf(r, p), period, scope, q.Get("subject"), q.Get("compare"))
	})
}

func (s *Server) doraDrilldown(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	at, err := time.Parse(time.RFC3339, q.Get("at"))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid", "serve at (data e ora ISO 8601)")
		return
	}
	period, scope := q.Get("period"), q.Get("scope")
	if period == "" {
		period = devsecops.PeriodMonth
	}
	if scope == "" {
		scope = devsecops.ScopeTenant
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Drilldown(r.Context(), tx, actorOf(r, p), period, scope, q.Get("subject"), at)
	})
}

func (s *Server) doraSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.DoraSettings(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) doraSaveSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.DoraSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SaveDoraSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) doraPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Period   string                 `json:"period"`
		Settings devsecops.DoraSettings `json:"settings"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Period == "" {
		in.Period = devsecops.PeriodMonth
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		rows, err := s.dso.Preview(r.Context(), tx, actorOf(r, p), in.Period, in.Settings)
		return map[string]any{"items": rows}, err
	})
}

func (s *Server) doraRecompute(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Recompute(r.Context(), tx, actorOf(r, p))
	})
}
