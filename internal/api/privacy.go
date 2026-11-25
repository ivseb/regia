package api

import (
	"bytes"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/privacy"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Vista DPO (ondata 3, replica/usabilita/governo.md, C8): registro dei trattamenti, DPIA,
// violazioni dei dati personali e flussi di dati. I permessi (privacy.read, privacy.write) li
// controlla il modulo; qui solo il passaggio da HTTP.

func (s *Server) privacyHome(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*privacy.Home, error) {
		return s.privacy.Home(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) privacyTreatments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.privacy.Treatments(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("archived") == "1")
		return map[string]any{"items": items, "legal_bases": legalBases()}, err
	})
}

func legalBases() []map[string]string {
	out := make([]map[string]string, 0, len(privacy.LegalBases))
	for _, b := range privacy.LegalBases {
		out = append(out, map[string]string{"key": b.Key, "name": b.Name})
	}
	return out
}

func (s *Server) privacyTreatment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*privacy.Treatment, error) {
		return s.privacy.Treatment(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) privacyCreateTreatment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in privacy.TreatmentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*privacy.Treatment, error) {
		return s.privacy.CreateTreatment(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) privacyUpdateTreatment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in privacy.TreatmentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*privacy.Treatment, error) {
		return s.privacy.UpdateTreatment(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) privacyDeleteTreatment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.privacy.DeleteTreatment(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) privacyCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var buf bytes.Buffer
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return s.privacy.WriteCSV(r.Context(), tx, actorOf(r, p), &buf)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="registro-trattamenti-`+time.Now().Format(time.DateOnly)+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) privacyDPIAs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.privacy.DPIAs(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) privacyDPIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*privacy.DPIA, error) {
		return s.privacy.DPIA(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) privacySaveDPIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in privacy.DPIAInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*privacy.DPIA, error) {
		return s.privacy.SaveDPIA(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) privacyDPIATransition(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		To string `json:"to"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*privacy.DPIA, error) {
		return s.privacy.TransitionDPIA(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.To)
	})
}

func (s *Server) privacyBreaches(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.privacy.Breaches(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("all") == "1")
		return map[string]any{"items": items, "hours": privacy.BreachHours}, err
	})
}

func (s *Server) privacyBreach(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*privacy.Breach, error) {
		return s.privacy.Breach(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) privacySetBreach(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in privacy.BreachInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*privacy.Breach, error) {
		return s.privacy.SetBreach(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) privacyNotifyBreach(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in privacy.NotifyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*privacy.Breach, error) {
		return s.privacy.NotifyBreach(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) privacyFlows(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*privacy.Flows, error) {
		return s.privacy.Flows(r.Context(), tx, actorOf(r, p))
	})
}
