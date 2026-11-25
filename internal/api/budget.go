package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/finance"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Organizzazione e budget gerarchico (replica/budget.md; docs/budget-api.md).
//
// Le rotte /api/v1/org/* e /api/v1/budget/* le può chiamare ogni membro: ciò che vede lo
// decide la RLS (finanza tutto; responsabile o referente finanziario di un'unità il suo
// sottoalbero; PM o responsabile di un elemento il suo nodo; gli altri nulla) e ciò che può
// fare lo decide il servizio (finance.write, oppure responsabile dell'unità con la
// ripartizione libera attiva). La transazione porta il membro (app.member_id) e, solo con
// finance.read, app.finance.

// budgetJSON apre la transazione con il membro che agisce e risponde in JSON.
func budgetJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly, Finance: p.Can(rbac.FinanceRead), Member: p.MemberID}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) budgetRoutes(mux *http.ServeMux) {
	// Organizzazione
	mux.HandleFunc("GET /api/v1/org/units", inTenant(s.orgUnits))
	mux.HandleFunc("POST /api/v1/org/units", inTenant(s.createOrgNode))
	mux.HandleFunc("PATCH /api/v1/org/units/{id}", inTenant(s.updateOrgUnit))
	mux.HandleFunc("POST /api/v1/org/units/{id}/move", inTenant(s.moveOrgUnit))
	mux.HandleFunc("GET /api/v1/org/units/{id}/members", inTenant(s.orgUnitMembers))
	mux.HandleFunc("PUT /api/v1/org/units/{id}/members/{member}", inTenant(s.setOrgMember))
	mux.HandleFunc("DELETE /api/v1/org/units/{id}/members/{member}", inTenant(s.removeOrgMember))
	mux.HandleFunc("GET /api/v1/org/people", inTenant(s.orgPeople))
	mux.HandleFunc("POST /api/v1/org/import/preview", inTenant(s.orgImportPreview))
	mux.HandleFunc("POST /api/v1/org/import/confirm", inTenant(s.orgImportConfirm))

	// Budget
	mux.HandleFunc("GET /api/v1/budget/settings", inTenant(s.getBudgetSettings))
	mux.HandleFunc("PUT /api/v1/budget/settings", inTenant(s.putBudgetSettings))
	mux.HandleFunc("GET /api/v1/budget/periods", inTenant(s.listPeriods))
	mux.HandleFunc("POST /api/v1/budget/fiscal-years/{id}/periods", inTenant(s.generatePeriods))
	mux.HandleFunc("POST /api/v1/budget/periods/{id}/open", inTenant(s.openPeriod))
	mux.HandleFunc("POST /api/v1/budget/periods/{id}/root", inTenant(s.periodRoot))
	mux.HandleFunc("GET /api/v1/budget/periods/{id}/close-preview", inTenant(s.closePreview))
	mux.HandleFunc("POST /api/v1/budget/periods/{id}/start-closing", inTenant(s.startClosing))
	mux.HandleFunc("POST /api/v1/budget/periods/{id}/close", inTenant(s.closePeriod))
	mux.HandleFunc("POST /api/v1/budget/periods/{id}/undo-close", inTenant(s.undoClose))
	mux.HandleFunc("GET /api/v1/budget/periods/{id}/closures", inTenant(s.listClosures))
	mux.HandleFunc("GET /api/v1/budget/tree", inTenant(s.budgetTree))
	mux.HandleFunc("POST /api/v1/budget/allocations", inTenant(s.createAllocation))
	mux.HandleFunc("GET /api/v1/budget/allocations/{id}", inTenant(s.getAllocation))
	mux.HandleFunc("PATCH /api/v1/budget/allocations/{id}", inTenant(s.patchAllocation))
	mux.HandleFunc("PUT /api/v1/budget/allocations/{id}/amount", inTenant(s.setAllocationAmount))
	mux.HandleFunc("GET /api/v1/budget/allocations/{id}/movements", inTenant(s.allocationMovements))
	mux.HandleFunc("POST /api/v1/budget/allocations/{id}/distribute/preview", inTenant(s.distributePreview))
	mux.HandleFunc("POST /api/v1/budget/allocations/{id}/distribute", inTenant(s.distribute))
	mux.HandleFunc("GET /api/v1/budget/transfers", inTenant(s.listAllocTransfers))
	mux.HandleFunc("POST /api/v1/budget/transfers", inTenant(s.createAllocTransfer))
	mux.HandleFunc("GET /api/v1/budget/element-allocations", inTenant(s.elementAllocations))
	mux.HandleFunc("PUT /api/v1/finance/budgets/{id}/allocation", can(rbac.FinanceRead, s.linkBudgetAllocation))
}

// ---------------------------------------------------------------------
// Organizzazione
// ---------------------------------------------------------------------

func (s *Server) orgUnits(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.OrgUnits(r.Context(), tx, q.Get("q"), limit, q.Get("archived") == "1")
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createOrgNode(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.OrgUnitInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.OrgUnit, error) {
		return s.fin.CreateOrgUnit(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateOrgUnit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.OrgUnitInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.OrgUnit, error) {
		return s.fin.UpdateOrgUnit(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) moveOrgUnit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		ParentID string `json:"parent_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.OrgUnit, error) {
		return s.fin.MoveOrgUnit(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.ParentID)
	})
}

func (s *Server) orgUnitMembers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.OrgUnitMembers(r.Context(), tx, r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) setOrgMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if err := s.fin.SetOrgMember(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("member"), in.Role); err != nil {
			return nil, err
		}
		items, err := s.fin.OrgUnitMembers(r.Context(), tx, r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) removeOrgMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.RemoveOrgMember(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("member"))
	})
}

func (s *Server) orgPeople(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.People(r.Context(), tx, q.Get("q"), limit)
		return map[string]any{"items": items}, err
	})
}

type orgImportBody struct {
	CSV       string `json:"csv"`
	Delimiter string `json:"delimiter"`
	SHA256    string `json:"sha256"`
}

func (s *Server) orgImportPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in orgImportBody
	if !decodeLargeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.OrgImportReport, error) {
		return s.fin.ImportOrg(r.Context(), tx, actorOf(r, p), in.CSV, in.Delimiter, "", false)
	})
}

func (s *Server) orgImportConfirm(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in orgImportBody
	if !decodeLargeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.OrgImportReport, error) {
		return s.fin.ImportOrg(r.Context(), tx, actorOf(r, p), in.CSV, in.Delimiter, in.SHA256, true)
	})
}

// ---------------------------------------------------------------------
// Impostazioni e periodi
// ---------------------------------------------------------------------

func (s *Server) getBudgetSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (finance.BudgetSettings, error) {
		return finance.GetBudgetSettings(r.Context(), tx)
	})
}

func (s *Server) putBudgetSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.BudgetSettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (finance.BudgetSettings, error) {
		return s.fin.UpdateBudgetSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) listPeriods(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Periods(r.Context(), tx, r.URL.Query().Get("fiscal_year_id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) generatePeriods(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Granularity string `json:"granularity"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.EnsurePeriods(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Granularity)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) openPeriod(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Period, error) {
		return s.fin.OpenPeriod(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) periodRoot(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.RootInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.PeriodID = r.PathValue("id")
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Node, error) {
		return s.fin.EnsureRoot(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) closePreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.ClosurePreview, error) {
		return s.fin.ClosePreview(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) startClosing(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Period, error) {
		return s.fin.StartClosing(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) closePeriod(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.CloseInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Closure, error) {
		return s.fin.ClosePeriod(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) undoClose(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Period, error) {
		return s.fin.UndoClose(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) listClosures(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Closures(r.Context(), tx, r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

// ---------------------------------------------------------------------
// Albero, nodi, ripartizione
// ---------------------------------------------------------------------

func (s *Server) budgetTree(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ctx := r.Context()
		pid := r.URL.Query().Get("period_id")
		per, err := s.fin.Periods(ctx, tx, "")
		if err != nil {
			return nil, err
		}
		if pid == "" {
			return map[string]any{"periods": per, "items": []any{}}, nil
		}
		items, err := s.fin.Tree(ctx, tx, actorOf(r, p), pid)
		if err != nil {
			return nil, err
		}
		return map[string]any{"periods": per, "period_id": pid, "items": items}, nil
	})
}

func (s *Server) createAllocation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.NodeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.Node, error) {
		return s.fin.CreateNode(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getAllocation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.Node, error) {
		return s.fin.Node(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) patchAllocation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.NodePatch
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Node, error) {
		return s.fin.PatchNode(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) setAllocationAmount(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		AmountCents int64  `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		a := actorOf(r, p)
		n, err := s.fin.SetAmount(r.Context(), tx, a, r.PathValue("id"), in.AmountCents, in.Reason)
		if err != nil {
			return nil, err
		}
		// Il padre mostra subito la nuova riserva (modifica in linea).
		var parent *finance.Node
		if n.ParentID != nil {
			if parent, err = s.fin.Node(r.Context(), tx, a, *n.ParentID); err != nil {
				parent = nil // il padre può non essere visibile a chi modifica
			}
		}
		return map[string]any{"node": n, "parent": parent}, nil
	})
}

func (s *Server) allocationMovements(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Movements(r.Context(), tx, r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) distributePreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.DistInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.DistPreview, error) {
		return s.fin.DistributePreview(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) distribute(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.DistInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.DistPreview, error) {
		return s.fin.Distribute(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) listAllocTransfers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.AllocTransfers(r.Context(), tx, r.URL.Query().Get("period_id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createAllocTransfer(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.AllocTransferInput
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.AllocTransfer, error) {
		return s.fin.CreateAllocTransfer(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) elementAllocations(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		q := r.URL.Query()
		return s.fin.ElementAllocations(r.Context(), tx, finance.ElementAllocationQuery{Element: q.Get("element_id"), FiscalYear: q.Get("fiscal_year_id"), BudgetID: q.Get("budget_id")})
	})
}

func (s *Server) linkBudgetAllocation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		AllocationID *string `json:"allocation_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	budgetJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Budget, error) {
		a := actorOf(r, p)
		if err := s.fin.SetBudgetAllocation(r.Context(), tx, a, r.PathValue("id"), in.AllocationID); err != nil {
			return nil, err
		}
		return s.fin.Budget(r.Context(), tx, a, r.PathValue("id"))
	})
}
