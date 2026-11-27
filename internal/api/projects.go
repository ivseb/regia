package api

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/planning"
	"regia/internal/modules/projects"
	"regia/internal/modules/report"
	"regia/internal/platform/auth"
)

// Progetti (replica/progetti.md, passi 1-4; docs/sviluppo/progetti.md): progetto da
// richiesta o da zero, piano con fasi, work package, attività, milestone e dipendenze,
// modi di consegna, avanzamento, Panoramica del PM, baseline e change request, fornitori
// con SAL e deliverable, conflitti sui componenti condivisi, programmi.

func (s *Server) projectsInit() {
	if s.eng == nil {
		return
	}
	s.proj = projects.Register(s.eng, s.approvals)
	s.proj.Budget = s.projectBudget
	s.proj.Epic = func(ctx context.Context, tx pgx.Tx, actor engine.Actor, wpRef string) (*engine.Element, error) {
		return s.work.CreateEpicFor(ctx, tx, actor, wpRef)
	}
	// Passo 5: il budget approvato (o la previsione, o un trasferimento) ricontrolla le tolleranze del
	// progetto; lo status report del progetto parte dai suoi numeri.
	if s.fin != nil {
		s.fin.Changed = s.proj.BudgetChanged
	}
	if s.report != nil {
		s.report.ProjectSnapshot = s.projectStatusSnapshot
		s.report.ProjectBudgetVisible = s.proj.BudgetVisibleTo
	}
}

// projectBudget è il budget del progetto (passo 5): i totali della finanza sul progetto e sulle
// richieste confluite. nil se non c'è nulla: nessun budget, nessun costo.
func (s *Server) projectBudget(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string, requestIDs []string) (*projects.Budget, error) {
	if s.fin == nil {
		return nil, nil
	}
	t, err := s.fin.ProjectTotals(ctx, tx, actor, projectID, requestIDs)
	if err != nil {
		return nil, err
	}
	if len(t.Budgets) == 0 && t.ActualCents == 0 && t.CommittedCents == 0 {
		return nil, nil
	}
	out := &projects.Budget{Currency: t.Currency, ApprovedCents: t.ApprovedCents, PendingCents: t.PendingCents, SpentCents: t.ActualCents,
		CommittedCents: t.CommittedCents, ForecastCents: t.ForecastCents, ConsumptionPct: t.ConsumptionPct, Budgets: []projects.BudgetRef{}}
	if out.ConsumptionPct != nil {
		v := math.Round(*out.ConsumptionPct*10) / 10
		out.ConsumptionPct = &v
	}
	for _, b := range t.Budgets {
		out.Budgets = append(out.Budgets, projects.BudgetRef{ID: b.ID, ElementKey: b.ElementKey, FiscalYear: b.FiscalYear, Kind: b.Kind,
			Version: b.Version, Status: b.Status, TotalCents: b.TotalCents})
	}
	return out, nil
}

// projectStatusSnapshot fotografa i numeri del progetto per lo status report (passo 5): gli stessi
// della Panoramica, con gli occhi di chi scrive il report.
func (s *Server) projectStatusSnapshot(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, asOf time.Time) (*report.Snapshot, error) {
	n, p, err := s.proj.Numbers(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	sn := &report.Snapshot{}
	sc := &sn.Schedule
	sc.Start, sc.End = n.Start, n.End
	switch n.TargetKind {
	case "regulatory":
		sc.Deadline = n.Target
	case "desired":
		sc.DesiredDate = n.Target
	}
	sc.Tasks, sc.TasksDone = n.WorkPackages, n.WorkPackagesDone
	sc.Late = []report.TaskRef{}
	for _, it := range p.Items {
		if it.Kind == projects.TypeWorkPackage && it.StateCategory != "cancelled" && it.Start != nil && it.End != nil {
			sc.TasksScheduled++
		}
	}
	for _, l := range n.Late {
		if l.Kind != projects.TypeWorkPackage {
			continue
		}
		sc.TasksLate++
		sc.Late = append(sc.Late, report.TaskRef{ID: l.ID, Key: l.Key, Application: l.Title, Team: l.Team, End: l.Date})
	}
	actual := n.Progress
	sc.ActualPct = &actual
	sc.PlannedPct = projects.PlannedPct(p, asOf)
	if sc.PlannedPct == nil {
		zero := 0.0
		sc.PlannedPct = &zero
	}
	if b := n.Budget; b != nil {
		sn.Cost = report.CostData{Currency: b.Currency, ApprovedCents: b.ApprovedCents, ActualCents: b.SpentCents,
			CommittedCents: b.CommittedCents, ForecastCents: b.ForecastCents, ConsumptionPct: b.ConsumptionPct}
	} else if !n.BudgetVisible {
		sn.Cost.Hidden = true
	}
	conv := func(refs []projects.ItemRef) []report.ProjRef {
		out := make([]report.ProjRef, 0, len(refs))
		for _, r := range refs {
			out = append(out, report.ProjRef{ID: r.ID, Key: r.Key, Title: r.Title, Kind: r.Kind, Date: r.Date, DaysLate: r.DaysLate, Restricted: r.Restricted})
		}
		return out
	}
	pd := &report.ProjectData{Signal: n.Signal, Reasons: n.Reasons, Progress: n.Progress, WorkPackages: n.WorkPackages,
		WorkPackagesDone: n.WorkPackagesDone, Milestones: n.Milestones, MilestonesDone: n.MilestonesDone, Baseline: n.Baseline,
		BaselineEnd: n.BaselineEnd, VarianceDays: n.VarianceDays, VariancePct: n.VariancePct, BudgetVariancePct: n.BudgetVariancePct,
		ToleranceDays: n.Tolerance.Days, ToleranceBudget: n.Tolerance.BudgetPct, Target: n.Target, TargetKind: n.TargetKind,
		MarginDays: n.MarginDays, Late: conv(n.Late), OverTolerance: conv(n.OverTolerance), ScopeAdded: conv(n.ScopeAdded),
		ScopeRemoved: conv(n.ScopeRemoved), Blocked: len(n.Blocked), ClaimsPending: n.ClaimsPending, Conflicts: len(n.Conflicts),
		OpenChanges: n.OpenChangeRequests, Risks: conv(n.Risks), MissingRoles: []string{}}
	if n.NextMilestone != nil {
		nm := conv([]projects.ItemRef{*n.NextMilestone})[0]
		pd.NextMilestone = &nm
	}
	for _, r := range n.MissingRoles {
		pd.MissingRoles = append(pd.MissingRoles, r.Name)
	}
	sn.Project = pd
	return sn, nil
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
	// Passo 3: baseline e change request.
	bl, cr := "/api/v1/plan-baselines", "/api/v1/change-requests"
	mux.HandleFunc("GET "+b+"/{ref}/baselines", inTenant(s.projectBaselines))
	mux.HandleFunc("POST "+b+"/{ref}/baselines", inTenant(s.createBaseline))
	mux.HandleFunc("GET "+bl+"/{id}", inTenant(s.planBaseline))
	mux.HandleFunc("GET "+b+"/{ref}/change-requests", inTenant(s.projectChangeRequests))
	mux.HandleFunc("POST "+b+"/{ref}/change-requests", inTenant(s.createChangeRequest))
	mux.HandleFunc("GET "+cr+"/{id}", inTenant(s.changeRequest))
	mux.HandleFunc("POST "+cr+"/{id}/submit", inTenant(s.submitChangeRequest))
	mux.HandleFunc("POST "+cr+"/{id}/withdraw", inTenant(s.withdrawChangeRequest))
	// Passo 5: tolleranze del progetto, «In arrivo dai progetti», status report del progetto.
	mux.HandleFunc("PUT "+b+"/{ref}/tolerance", inTenant(s.setProjectTolerance))
	mux.HandleFunc("GET "+b+"/incoming", inTenant(s.incomingWork))
	mux.HandleFunc("GET "+b+"/{ref}/status-reports", inTenant(s.listStatusReports))
	mux.HandleFunc("GET "+b+"/{ref}/status-reports/prefill", inTenant(s.prefillStatusReport))
	mux.HandleFunc("POST "+b+"/{ref}/status-reports", inTenant(s.createStatusReport))
	mux.HandleFunc("PUT "+b+"/{ref}/status-reports/schedule", inTenant(s.statusReportSchedule))
	// Il singolo report del progetto ha un prefisso dei progetti: resta raggiungibile anche con la
	// domanda spenta (gli status report della richiesta stanno sotto il modulo demand).
	pr := "/api/v1/project-reports"
	mux.HandleFunc("GET "+pr+"/{id}", inTenant(s.getStatusReport))
	mux.HandleFunc("PATCH "+pr+"/{id}", inTenant(s.updateStatusReport))
	mux.HandleFunc("DELETE "+pr+"/{id}", inTenant(s.deleteStatusReport))
	mux.HandleFunc("POST "+pr+"/{id}/refresh", inTenant(s.refreshStatusReport))
	mux.HandleFunc("POST "+pr+"/{id}/submit", inTenant(s.submitStatusReport))
	mux.HandleFunc("POST "+pr+"/{id}/acknowledge", inTenant(s.acknowledgeStatusReport))
	mux.HandleFunc("GET "+pr+"/{id}/compare", inTenant(s.compareStatusReports))
	mux.HandleFunc("GET "+pr+"/{id}/export.md", inTenant(s.exportStatusReport))
	// Passo 4: fornitori (contratto, SAL, deliverable) e programmi.
	pc, dl, pg := "/api/v1/progress-claims", "/api/v1/deliverables", "/api/v1/programs"
	mux.HandleFunc("GET "+it+"/{id}/supplier", inTenant(s.planItemSupplier))
	mux.HandleFunc("PUT "+it+"/{id}/supplier", inTenant(s.updatePlanItemSupplier))
	mux.HandleFunc("POST "+it+"/{id}/claims", inTenant(s.createClaim))
	mux.HandleFunc("POST "+pc+"/{id}/decision", inTenant(s.decideClaim))
	mux.HandleFunc("POST "+it+"/{id}/deliverables", inTenant(s.createDeliverable))
	mux.HandleFunc("POST "+dl+"/{id}/deliver", inTenant(s.deliverDeliverable))
	mux.HandleFunc("POST "+dl+"/{id}/decision", inTenant(s.decideDeliverable))
	mux.HandleFunc("GET "+pg, inTenant(s.listPrograms))
	mux.HandleFunc("POST "+pg, inTenant(s.createProgram))
	mux.HandleFunc("GET "+pg+"/{ref}", inTenant(s.programView))
	mux.HandleFunc("PUT "+b+"/{ref}/program", inTenant(s.setProjectProgram))
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

func (s *Server) projectBaselines(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.proj.Baselines(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"items": items}, err
	})
}

// createBaseline fissa la prima baseline del piano (le altre nascono dalle change request).
func (s *Server) createBaseline(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.Baseline, error) {
		return s.proj.CreateBaseline(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Note)
	})
}

func (s *Server) planBaseline(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.Baseline, error) {
		return s.proj.GetBaseline(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) projectChangeRequests(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.proj.ChangeRequests(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createChangeRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ChangeRequestInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.ChangeRequest, error) {
		return s.proj.CreateChangeRequest(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) changeRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.ChangeRequest, error) {
		return s.proj.ChangeRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) submitChangeRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.ChangeRequest, error) {
		return s.proj.SubmitChangeRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Reason)
	})
}

func (s *Server) withdrawChangeRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.ChangeRequest, error) {
		return s.proj.WithdrawChangeRequest(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) planItemSupplier(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.Supplier(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) updatePlanItemSupplier(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.SupplierInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.UpdateSupplier(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

// createClaim dichiara un SAL del work package del fornitore.
func (s *Server) createClaim(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ClaimInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.CreateClaim(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

type decisionInput struct {
	Outcome string `json:"outcome"` // accepted | rejected
	Note    string `json:"note"`
}

func (s *Server) decideClaim(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in decisionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.DecideClaim(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Outcome, in.Note)
	})
}

func (s *Server) createDeliverable(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.DeliverableInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.CreateDeliverable(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deliverDeliverable(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.Deliver(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Note)
	})
}

func (s *Server) decideDeliverable(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in decisionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.SupplierView, error) {
		return s.proj.DecideDeliverable(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Outcome, in.Note)
	})
}

func (s *Server) listPrograms(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.proj.Programs(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("closed") == "true")
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createProgram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ProgramInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.proj.CreateProgram(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) programView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.ProgramView, error) {
		return s.proj.Program(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

// setProjectProgram mette il progetto in un programma (program vuoto lo toglie).
func (s *Server) setProjectProgram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Program string `json:"program"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ref, err := s.proj.SetProgram(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Program)
		return map[string]any{"program": ref}, err
	})
}

func (s *Server) setProjectTolerance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in projects.ToleranceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*projects.Plan, error) {
		return s.proj.SetTolerance(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) incomingWork(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	weeks, _ := strconv.Atoi(q.Get("weeks"))
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*projects.Incoming, error) {
		return s.proj.IncomingWork(r.Context(), tx, actorOf(r, p), projects.IncomingInput{Team: q.Get("team"), Space: q.Get("space"), Weeks: weeks})
	})
}
