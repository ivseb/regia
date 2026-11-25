package api

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/grc"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Spazio dell'auditor (ondata 3, C6; docs/sviluppo/spazio-auditor.md). I permessi li verifica il
// modulo: l'auditor (grc.audit) scrive incarichi, campioni, richieste, commenti e rilievi.

func (s *Server) grcAuditRoutes(mux *http.ServeMux) {
	const b = "/api/v1/grc/audit"
	mux.HandleFunc("GET "+b+"/home", inTenant(s.auditHome))
	mux.HandleFunc("GET "+b+"/engagements", inTenant(s.auditEngagements))
	mux.HandleFunc("POST "+b+"/engagements", inTenant(s.auditCreateEngagement))
	mux.HandleFunc("GET "+b+"/engagements/{id}", inTenant(s.auditEngagement))
	mux.HandleFunc("POST "+b+"/engagements/{id}/close", inTenant(s.auditCloseEngagement))
	mux.HandleFunc("GET "+b+"/engagements/{id}/pack", inTenant(s.auditPack))
	mux.HandleFunc("PUT "+b+"/engagements/{id}/controls/{ref}/review", inTenant(s.auditReview))
	mux.HandleFunc("POST "+b+"/engagements/{id}/samples", inTenant(s.auditDraw))
	mux.HandleFunc("POST "+b+"/engagements/{id}/requests", inTenant(s.auditCreateRequest))
	mux.HandleFunc("POST "+b+"/engagements/{id}/findings", inTenant(s.auditOpenFinding))
	mux.HandleFunc("GET "+b+"/samples/{id}", inTenant(s.auditSample))
	mux.HandleFunc("PUT "+b+"/samples/{id}/items/{pos}", inTenant(s.auditAssess))
	mux.HandleFunc("GET "+b+"/requests/mine", inTenant(s.auditMyRequests))
	mux.HandleFunc("POST "+b+"/requests/{id}/answer", inTenant(s.auditAnswer))
	mux.HandleFunc("POST "+b+"/requests/{id}/close", inTenant(s.auditCloseRequest))
	mux.HandleFunc("POST "+b+"/findings/{id}/plan", inTenant(s.auditPlan))
	mux.HandleFunc("POST "+b+"/findings/{id}/close", inTenant(s.auditCloseFinding))
	mux.HandleFunc("GET /api/v1/grc/controls/{ref}/audit", inTenant(s.auditControl))
	mux.HandleFunc("POST /api/v1/grc/controls/{ref}/audit/comments", inTenant(s.auditComment))
}

func (s *Server) auditHome(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.AuditHome, error) {
		return s.grc.Home(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("engagement"))
	})
}

func (s *Server) auditEngagements(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Engagements(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) auditCreateEngagement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.EngagementInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.EngagementDetail, error) {
		return s.grc.CreateEngagement(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) auditEngagement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.EngagementDetail, error) {
		return s.grc.EngagementDetail(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) auditCloseEngagement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.EngagementDetail, error) {
		return s.grc.CloseEngagement(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) auditReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Conclusion string `json:"conclusion"`
		Note       string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.EngagementDetail, error) {
		return s.grc.ReviewControl(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("ref"), in.Conclusion, in.Note)
	})
}

func (s *Server) auditDraw(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.DrawInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.Sample, error) {
		return s.grc.DrawSample(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) auditSample(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.Sample, error) {
		return s.grc.Sample(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) auditAssess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pos, err := strconv.Atoi(r.PathValue("pos"))
	if err != nil {
		s.engineError(w, r, engine.ErrNotFound)
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Sample, error) {
		return s.grc.AssessSampleItem(r.Context(), tx, actorOf(r, p), r.PathValue("id"), pos, in.Outcome, in.Note)
	})
}

func (s *Server) auditCreateRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.RequestInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.Request, error) {
		return s.grc.CreateRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) auditMyRequests(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.MyRequests(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) auditAnswer(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.AnswerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Request, error) {
		return s.grc.AnswerRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) auditCloseRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Request, error) {
		return s.grc.CloseRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) auditOpenFinding(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.FindingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.Finding, error) {
		return s.grc.OpenFinding(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) auditPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.PlanInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Finding, error) {
		return s.grc.ProposePlan(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) auditCloseFinding(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Finding, error) {
		return s.grc.CloseFinding(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Note)
	})
}

func (s *Server) auditControl(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.ControlAudit, error) {
		return s.grc.ControlAudit(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) auditComment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.CommentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.AddComment(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return map[string]any{"items": items}, err
	})
}

// auditPack: il pacchetto per l'audit sul perimetro e sul periodo dell'incarico.
func (s *Server) auditPack(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	f, err := os.CreateTemp("", "regia-audit-*.zip")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	var files grc.FileSource
	if s.grcFiles != nil {
		files = s.grcFiles
	}
	fw := ""
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		fw, err = s.grc.WriteEngagementPack(r.Context(), tx, actorOf(r, p), r.PathValue("id"), f, files)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.internal(w, r, err)
		return
	}
	name := "audit-incarico"
	if fw != "" {
		name += "-" + fw
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+"-"+time.Now().Format(time.DateOnly)+`.zip"`)
	_, _ = io.Copy(w, f)
}
