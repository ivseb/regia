package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/planning"
	"regia/internal/modules/projects"
	"regia/internal/platform/auth"
)

// Progetti (replica/progetti.md, passi 1 e 2; docs/sviluppo/progetti.md): progetto da
// richiesta o da zero, piano con fasi, work package, attività, milestone e dipendenze,
// modi di consegna, avanzamento e Panoramica del PM.

func (s *Server) projectsInit() {
	if s.eng == nil {
		return
	}
	s.proj = projects.Register(s.eng, s.approvals)
	s.proj.Budget = s.projectBudget
	s.proj.Epic = func(ctx context.Context, tx pgx.Tx, actor engine.Actor, wpRef string) (*engine.Element, error) {
		return s.work.CreateEpicFor(ctx, tx, actor, wpRef)
	}
}

// projectBudget somma gli importi aggregati delle richieste confluite nel progetto.
func (s *Server) projectBudget(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestIDs []string) (*projects.Budget, error) {
	if s.fin == nil {
		return nil, nil
	}
	var out *projects.Budget
	for _, id := range requestIDs {
		t, err := s.fin.RequestTotals(ctx, tx, actor, id)
		if err != nil {
			if err == engine.ErrNotFound || err == engine.ErrForbidden {
				continue
			}
			return nil, err
		}
		if out == nil {
			out = &projects.Budget{Currency: t.Currency}
		}
		if t.ApprovedCents != nil {
			v := *t.ApprovedCents
			if out.ApprovedCents != nil {
				v += *out.ApprovedCents
			}
			out.ApprovedCents = &v
		}
		if t.ForecastCents != nil {
			v := *t.ForecastCents
			if out.ForecastCents != nil {
				v += *out.ForecastCents
			}
			out.ForecastCents = &v
		}
		out.SpentCents += t.ActualCents
		out.CommittedCents += t.CommittedCents
	}
	return out, nil
}

func (s *Server) projectRoutes(mux *http.ServeMux) {
	b := "/api/v1/projects"
	// Voci e fasi hanno prefissi propri: /projects/items/{id} si sovrapporrebbe a /projects/{ref}/plan.
	it, ph := "/api/v1/plan-items", "/api/v1/plan-phases"
	mux.HandleFunc("GET "+b, inTenant(s.listProjects))
	mux.HandleFunc("POST "+b, inTenant(s.createProject))
	mux.HandleFunc("GET "+b+"/settings", inTenant(s.projectSettings))
	mux.HandleFunc("PUT "+b+"/settings", inTenant(s.saveProjectSettings))
	mux.HandleFunc("GET "+it+"/{id}", inTenant(s.planItem))
	mux.HandleFunc("PATCH "+it+"/{id}", inTenant(s.updatePlanItem))
	mux.HandleFunc("DELETE "+it+"/{id}", inTenant(s.deletePlanItem))
	mux.HandleFunc("PUT "+it+"/{id}/schedule", inTenant(s.schedulePlanItem))
	mux.HandleFunc("PUT "+it+"/{id}/dependencies", inTenant(s.planItemDeps))
	mux.HandleFunc("GET "+it+"/{id}/progress", inTenant(s.planItemProgress))
	mux.HandleFunc("POST "+it+"/{id}/progress", inTenant(s.recordPlanProgress))
	mux.HandleFunc("POST "+it+"/{id}/epic", inTenant(s.planItemEpic))
	mux.HandleFunc("PATCH "+ph+"/{id}", inTenant(s.updatePhase))
	mux.HandleFunc("DELETE "+ph+"/{id}", inTenant(s.deletePhase))
	mux.HandleFunc("POST "+ph+"/{id}/gate", inTenant(s.requestGate))
	mux.HandleFunc("GET "+b+"/{ref}/plan", inTenant(s.projectPlan))
	mux.HandleFunc("GET "+b+"/{ref}/overview", inTenant(s.projectOverview))
	mux.HandleFunc("POST "+b+"/{ref}/plan/auto", inTenant(s.projectAutoPlan))
	mux.HandleFunc("POST "+b+"/{ref}/items", inTenant(s.createPlanItem))
	mux.HandleFunc("POST "+b+"/{ref}/phases", inTenant(s.createPhase))
	mux.HandleFunc("POST "+b+"/{ref}/requests", inTenant(s.attachRequest))
	// La richiesta: il suo progetto (o se può diventarlo) e «Crea il progetto».
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/project", inTenant(s.requestProject))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/project", inTenant(s.projectFromRequest))
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.proj.List(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("closed") == "true")
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ProjectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.proj.CreateProject(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) projectSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.Settings, error) {
		return s.proj.SettingsFor(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) saveProjectSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.Settings, error) {
		return s.proj.SaveSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) projectPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.Plan, error) {
		return s.proj.GetPlan(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

// projectOverview è la Panoramica del PM: i numeri del progetto con il suo piano.
func (s *Server) projectOverview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		n, plan, err := s.proj.Numbers(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"numbers": n, "project": plan.Project, "phases": plan.Phases, "can_edit": plan.CanEdit}, nil
	})
}

func (s *Server) projectAutoPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Replan bool   `json:"replan"`
		From   string `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.proj.AutoPlan(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Replan, in.From)
		return map[string]any{"changes": ch}, err
	})
}

func (s *Server) createPlanItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ItemInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.ItemView, error) {
		return s.proj.CreateItem(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) planItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.ItemView, error) {
		return s.proj.ItemView(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) updatePlanItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ItemPatch
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.ItemView, error) {
		return s.proj.UpdateItem(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deletePlanItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.proj.DeleteItem(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) schedulePlanItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Start   string `json:"start"`
		End     string `json:"end"`
		Cascade *bool  `json:"cascade"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	cascade := in.Cascade == nil || *in.Cascade
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.proj.Schedule(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Start, in.End, cascade)
		if ch == nil {
			ch = []planning.Change{}
		}
		return map[string]any{"changes": ch}, err
	})
}

func (s *Server) planItemDeps(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Deps []projects.DepInput `json:"depends_on"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.ItemView, error) {
		return s.proj.SetDependencies(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Deps)
	})
}

func (s *Server) planItemProgress(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.proj.ProgressHistory(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) recordPlanProgress(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ProgressInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.ItemView, error) {
		return s.proj.RecordProgress(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

// planItemEpic crea l'epic del work package agile sulla board del team (create_epic).
func (s *Server) planItemEpic(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		return s.proj.Epic(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) createPhase(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.PhaseInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.Phase, error) {
		return s.proj.CreatePhase(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) updatePhase(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.PhaseInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.Phase, error) {
		return s.proj.UpdatePhase(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deletePhase(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.proj.DeletePhase(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) requestGate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.Phase, error) {
		return s.proj.RequestGate(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// attachRequest fa confluire un'altra richiesta approvata nel progetto.
func (s *Server) attachRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Request string `json:"request"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		el, _, err := s.proj.FromRequest(r.Context(), tx, actorOf(r, p), in.Request, r.PathValue("ref"))
		return el, err
	})
}

func (s *Server) requestProject(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ref, approved, err := s.proj.ProjectOfRequest(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"project": ref, "approved": approved}, err
	})
}

func (s *Server) projectFromRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		el, created, err := s.proj.FromRequest(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": el, "created": created}, nil
	})
}
