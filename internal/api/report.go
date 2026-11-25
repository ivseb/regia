package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/finance"
	"regia/internal/modules/report"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Avanzamento (piano, passi 13 e 14): status report della richiesta, dashboard di
// portafoglio e portale del richiedente. La logica sta in internal/modules/report.

func (s *Server) reportRoutes(mux *http.ServeMux) {
	// Status report.
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/status-reports", inTenant(s.listStatusReports))
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/status-reports/prefill", inTenant(s.prefillStatusReport))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/status-reports", inTenant(s.createStatusReport))
	mux.HandleFunc("PUT /api/v1/demand/requests/{ref}/status-reports/schedule", inTenant(s.statusReportSchedule))
	mux.HandleFunc("GET /api/v1/status-reports/{id}", inTenant(s.getStatusReport))
	mux.HandleFunc("PATCH /api/v1/status-reports/{id}", inTenant(s.updateStatusReport))
	mux.HandleFunc("DELETE /api/v1/status-reports/{id}", inTenant(s.deleteStatusReport))
	mux.HandleFunc("POST /api/v1/status-reports/{id}/refresh", inTenant(s.refreshStatusReport))
	mux.HandleFunc("POST /api/v1/status-reports/{id}/submit", inTenant(s.submitStatusReport))
	mux.HandleFunc("POST /api/v1/status-reports/{id}/acknowledge", inTenant(s.acknowledgeStatusReport))
	mux.HandleFunc("GET /api/v1/status-reports/{id}/compare", inTenant(s.compareStatusReports))
	mux.HandleFunc("GET /api/v1/status-reports/{id}/export.md", inTenant(s.exportStatusReport))
	mux.HandleFunc("GET /api/v1/report/settings", inTenant(s.getReportSettings))
	mux.HandleFunc("PUT /api/v1/report/settings", inTenant(s.saveReportSettings))
	// Dashboard di portafoglio.
	mux.HandleFunc("GET /api/v1/dashboard/portfolio", inTenant(s.portfolioDashboard))
	mux.HandleFunc("GET /api/v1/dashboard/portfolio/layout", inTenant(s.dashboardLayout))
	mux.HandleFunc("PUT /api/v1/dashboard/portfolio/layout", inTenant(s.saveDashboardLayout))
	// Portale del richiedente.
	mux.HandleFunc("GET /api/v1/portal", inTenant(s.portal))
	mux.HandleFunc("GET /api/v1/portal/similar", inTenant(s.portalSimilar))
	mux.HandleFunc("GET /api/v1/portal/settings", inTenant(s.portalSettings))
	mux.HandleFunc("PUT /api/v1/portal/settings", inTenant(s.savePortalSettings))
	mux.HandleFunc("POST /api/v1/portal/drafts", inTenant(s.savePortalDraft))
	mux.HandleFunc("PUT /api/v1/portal/drafts/{id}", inTenant(s.savePortalDraft))
	mux.HandleFunc("DELETE /api/v1/portal/drafts/{id}", inTenant(s.deletePortalDraft))
	mux.HandleFunc("POST /api/v1/portal/drafts/{id}/submit", inTenant(s.submitPortalDraft))
	mux.HandleFunc("GET /api/v1/portal/requests/{ref}", inTenant(s.portalRequest))
	s.portalCatalogRoutes(mux)
}

// ---------------------------------------------------------------------
// Status report

func (s *Server) listStatusReports(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.List, error) {
		return s.report.List(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) prefillStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.Prefill, error) {
		return s.report.Preview(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.URL.Query().Get("period_end"))
	})
}

func (s *Server) createStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		PeriodEnd string `json:"period_end"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Create(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.PeriodEnd)
	})
}

func (s *Server) statusReportSchedule(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.ScheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.List, error) {
		return s.report.SetSchedule(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) getStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) updateStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Update(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deleteStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.report.Delete(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) refreshStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Refresh(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) submitStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Submit(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) acknowledgeStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Report, error) {
		return s.report.Acknowledge(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) compareStatusReports(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.Comparison, error) {
		return s.report.Compare(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.URL.Query().Get("with"))
	})
}

// exportStatusReport: il report in Markdown, da scaricare (il PDF lo fa il browser con la stampa).
func (s *Server) exportStatusReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var rep *report.Report
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		rep, err = s.report.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	name := fmt.Sprintf("status-report-%s-%s.md", asciiName(rep.ElementKey), rep.PeriodEnd)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(report.Markdown(rep)))
}

func (s *Server) getReportSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (report.Settings, error) {
		return s.report.Settings(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) saveReportSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (report.Settings, error) {
		return s.report.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

// ---------------------------------------------------------------------
// Dashboard di portafoglio, con la cache breve per tenant e filtro di swr.go.

// dashboardLag: dopo creazioni e passaggi di fase di richieste il cruscotto resta servibile per
// questo tempo, poi si rinnova in background (nessuno aspetta il ricalcolo, 0,3-1,6 s su 300.000 elementi).
const dashboardLag = 2 * time.Second

func (s *Server) portfolioDashboard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	f := report.Filter{Unit: q.Get("unit"), Driver: q.Get("driver"), From: q.Get("from"), To: q.Get("to")}
	actor := actorOf(r, p)
	// La risposta dipende dai permessi (dettaglio finanziario) e dal giorno: entrano nella chiave.
	key := strings.Join([]string{"dash", p.TenantID, fmt.Sprint(finance.CanRead(actor)), fmt.Sprint(actor.Has("element.read")),
		time.Now().Format(time.DateOnly), f.Key()}, "|")
	// Non in sola lettura: il perimetro si materializza in una tabella temporanea. Costi, budget,
	// report e ogni altra modifica si vedono subito; creazione e passaggi di fase delle richieste
	// (scritture tollerate, vedi notify) con un ritardo di al più dashboardLag.
	s.swr.serveMode(s, w, r, p, key, false, dashboardLag, true, func(ctx context.Context, tx pgx.Tx) (any, error) {
		return s.report.Portfolio(ctx, tx, actor, f)
	})
}

func (s *Server) dashboardLayout(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.report.Layout(r.Context(), tx, actorOf(r, p))
		return map[string]any{"panels": items}, err
	})
}

func (s *Server) saveDashboardLayout(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Panels []report.PanelPref `json:"panels"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.report.SaveLayout(r.Context(), tx, actorOf(r, p), in.Panels)
		return map[string]any{"panels": items}, err
	})
}

// ---------------------------------------------------------------------
// Portale del richiedente

func (s *Server) portal(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.Portal, error) {
		return s.report.Portal(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) portalSimilar(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.report.Similar(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("q"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) portalSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.PortalSettings, error) {
		return s.report.PortalConfig(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) savePortalSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.PortalSettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.PortalSettings, error) {
		return s.report.SavePortalConfig(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) savePortalDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.DraftInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	tenantJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*report.Draft, error) {
		return s.report.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deletePortalDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.report.DeleteDraft(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) submitPortalDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.report.SubmitDraft(r.Context(), tx, actorOf(r, p), s.demand, r.PathValue("id"))
	})
}

func (s *Server) portalRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.PortalRequest, error) {
		return s.report.PortalRequest(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}
