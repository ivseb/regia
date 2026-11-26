package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/finance"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Budget e costi (demand.md §9-10; piano, passi 11 e 12).
//
// Le rotte /api/v1/finance/* sono la pagina /finanza: servono finance.read e aprono la
// transazione con app.finance = 'on' (solo dopo il controllo del permesso), così la RLS
// lascia leggere tariffe e voci. Le scritture chiedono anche finance.write (nel modulo).
// I totali della richiesta e l'earned value sono aggregati: bastano i permessi di lettura
// dell'elemento e la transazione resta senza app.finance.

func (s *Server) financeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/finance", inTenant(s.requestFinance))
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/ev", inTenant(s.getProjectEV))
	mux.HandleFunc("PUT /api/v1/demand/requests/{ref}/ev", inTenant(s.saveProjectEV))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/ev/snapshots", inTenant(s.takeEVSnapshot))

	fr := func(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
		return can(rbac.FinanceRead, h)
	}
	mux.HandleFunc("GET /api/v1/finance/overview", fr(s.financeOverview))
	mux.HandleFunc("PUT /api/v1/finance/settings", fr(s.financeSettings))
	mux.HandleFunc("POST /api/v1/finance/fiscal-years", fr(s.saveFiscalYear))
	mux.HandleFunc("PATCH /api/v1/finance/fiscal-years/{id}", fr(s.saveFiscalYear))
	mux.HandleFunc("POST /api/v1/finance/categories", fr(s.saveCategory))
	mux.HandleFunc("PATCH /api/v1/finance/categories/{id}", fr(s.saveCategory))
	mux.HandleFunc("POST /api/v1/finance/funding-sources", fr(s.createFundingSource))
	mux.HandleFunc("GET /api/v1/finance/exchange-rates", fr(s.listExchangeRates))
	mux.HandleFunc("PUT /api/v1/finance/exchange-rates", fr(s.setExchangeRate))

	mux.HandleFunc("GET /api/v1/finance/budgets", fr(s.listBudgets))
	mux.HandleFunc("POST /api/v1/finance/budgets", fr(s.createBudget))
	mux.HandleFunc("GET /api/v1/finance/budgets/{id}", fr(s.getBudget))
	mux.HandleFunc("PUT /api/v1/finance/budgets/{id}/lines", fr(s.saveBudgetLines))
	mux.HandleFunc("POST /api/v1/finance/budgets/{id}/submit", fr(s.submitBudget))
	mux.HandleFunc("DELETE /api/v1/finance/budgets/{id}", fr(s.deleteBudget))
	mux.HandleFunc("GET /api/v1/finance/transfers", fr(s.listTransfers))
	mux.HandleFunc("POST /api/v1/finance/transfers", fr(s.createTransfer))

	mux.HandleFunc("GET /api/v1/finance/costs", fr(s.listCosts))
	mux.HandleFunc("POST /api/v1/finance/costs", fr(s.createCost))
	mux.HandleFunc("DELETE /api/v1/finance/costs/{id}", fr(s.deleteCost))
	mux.HandleFunc("GET /api/v1/finance/work-logs", fr(s.listWorkLogs))
	mux.HandleFunc("POST /api/v1/finance/work-logs", fr(s.createWorkLog))
	mux.HandleFunc("DELETE /api/v1/finance/work-logs/{id}", fr(s.deleteWorkLog))
	mux.HandleFunc("POST /api/v1/finance/work-logs/recompute", fr(s.recomputeWorkLogs))
	mux.HandleFunc("GET /api/v1/finance/rates", fr(s.listRates))
	mux.HandleFunc("POST /api/v1/finance/rates", fr(s.saveRate))
	mux.HandleFunc("PATCH /api/v1/finance/rates/{id}", fr(s.saveRate))
	mux.HandleFunc("DELETE /api/v1/finance/rates/{id}", fr(s.deleteRate))

	mux.HandleFunc("GET /api/v1/finance/import-profiles", fr(s.listImportProfiles))
	mux.HandleFunc("POST /api/v1/finance/import-profiles", fr(s.saveImportProfile))
	mux.HandleFunc("PUT /api/v1/finance/import-profiles/{id}", fr(s.saveImportProfile))
	mux.HandleFunc("DELETE /api/v1/finance/import-profiles/{id}", fr(s.deleteImportProfile))
	mux.HandleFunc("GET /api/v1/finance/imports", fr(s.listImports))
	mux.HandleFunc("GET /api/v1/finance/imports/{id}", fr(s.getImport))
	mux.HandleFunc("POST /api/v1/finance/imports", fr(s.previewImport))
	mux.HandleFunc("POST /api/v1/finance/imports/{id}/confirm", fr(s.confirmImport))
	mux.HandleFunc("POST /api/v1/finance/imports/{id}/rollback", fr(s.rollbackImport))

	mux.HandleFunc("GET /api/v1/finance/projects", fr(s.listEVProjects))
}

// financeJSON è tenantJSON con app.finance attivo: si usa solo dietro fr (finance.read).
func financeJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	if !p.Can(rbac.FinanceRead) { // difesa in profondità: le rotte sono già dietro fr
		writeProblemParams(w, r, http.StatusForbidden, "auth.forbidden", "permesso mancante: "+rbac.FinanceRead, map[string]any{"perm": rbac.FinanceRead})
		return
	}
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly, Finance: true}, func(tx pgx.Tx) error {
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

// maxImportBody: un CSV di costi può superare il limite generale dei corpi JSON.
const maxImportBody = 16 << 20

// decodeLargeJSON è decodeJSON con un limite più alto (import CSV).
func decodeLargeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "file troppo grande (massimo 16 MB)")
		} else {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", fmt.Sprintf("JSON non valido: %v", err))
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", "un solo oggetto JSON per richiesta")
		return false
	}
	return true
}

// ---------------------------------------------------------------------
// Richiesta: totali ed earned value (aggregati)
// ---------------------------------------------------------------------

func (s *Server) requestFinance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.Totals, error) {
		return s.fin.RequestTotals(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) getProjectEV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.Project, error) {
		return s.fin.Project(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) saveProjectEV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.ProjectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Project, error) {
		return s.fin.SaveProject(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) takeEVSnapshot(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.SnapshotInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Project, error) {
		return s.fin.TakeSnapshot(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

// ---------------------------------------------------------------------
// Struttura
// ---------------------------------------------------------------------

type financeOverview struct {
	Settings       finance.Settings        `json:"settings"`
	FiscalYears    []finance.FiscalYear    `json:"fiscal_years"`
	Categories     []finance.Category      `json:"categories"`
	FundingSources []finance.FundingSource `json:"funding_sources"`
	ImportFields   map[string]string       `json:"import_fields"`
	CanWrite       bool                    `json:"can_write"`
	// Budget gerarchico (replica/budget.md): impostazioni del tenant e periodi.
	BudgetSettings finance.BudgetSettings `json:"budget_settings"`
	Periods        []finance.Period       `json:"periods"`
}

func (s *Server) financeOverview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*financeOverview, error) {
		ctx := r.Context()
		out := &financeOverview{ImportFields: finance.ImportFields(), CanWrite: p.Can(rbac.FinanceWrite)}
		var err error
		if out.Settings, err = finance.GetSettings(ctx, tx); err != nil {
			return nil, err
		}
		if out.FiscalYears, err = s.fin.FiscalYears(ctx, tx); err != nil {
			return nil, err
		}
		if out.Categories, err = s.fin.Categories(ctx, tx); err != nil {
			return nil, err
		}
		if out.FundingSources, err = s.fin.FundingSources(ctx, tx); err != nil {
			return nil, err
		}
		if out.BudgetSettings, err = finance.GetBudgetSettings(ctx, tx); err != nil {
			return nil, err
		}
		out.Periods, err = s.fin.Periods(ctx, tx, "")
		return out, err
	})
}

func (s *Server) financeSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (finance.Settings, error) {
		return s.fin.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) saveFiscalYear(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.FiscalYearInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	financeJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*finance.FiscalYear, error) {
		return s.fin.SaveFiscalYear(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) saveCategory(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.CategoryInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	financeJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*finance.Category, error) {
		return s.fin.SaveCategory(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) createFundingSource(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.FundingSource, error) {
		return s.fin.CreateFundingSource(r.Context(), tx, actorOf(r, p), in.Name, in.Code)
	})
}

func (s *Server) listExchangeRates(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.ExchangeRates(r.Context(), tx, r.URL.Query().Get("currency"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) setExchangeRate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.ExchangeRate
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.ExchangeRate, error) {
		return s.fin.SetExchangeRate(r.Context(), tx, actorOf(r, p), in)
	})
}

// ---------------------------------------------------------------------
// Budget e trasferimenti
// ---------------------------------------------------------------------

func (s *Server) listBudgets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		f := finance.BudgetFilter{FiscalYearID: q.Get("fiscal_year"), CurrentOnly: q.Get("all") != "true"}
		if ref := q.Get("element"); ref != "" {
			el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), ref)
			if err != nil {
				return nil, err
			}
			f.ElementID = el.ID
		}
		items, err := s.fin.Budgets(r.Context(), tx, f)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createBudget(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.BudgetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.Budget, error) {
		return s.fin.CreateBudget(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getBudget(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.Budget, error) {
		return s.fin.Budget(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) saveBudgetLines(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Note  *string             `json:"note"`
		Lines []finance.LineInput `json:"lines"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Budget, error) {
		return s.fin.SaveLines(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Note, in.Lines)
	})
}

func (s *Server) submitBudget(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Budget, error) {
		return s.fin.Submit(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) deleteBudget(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.DeleteDraft(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Transfers(r.Context(), tx, r.URL.Query().Get("fiscal_year"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.TransferInput
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.Transfer, error) {
		return s.fin.CreateTransfer(r.Context(), tx, actorOf(r, p), in)
	})
}

// ---------------------------------------------------------------------
// Voci, ore, tariffe
// ---------------------------------------------------------------------

func (s *Server) listCosts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := finance.CostFilter{Element: q.Get("element"), CategoryID: q.Get("category"), Kind: q.Get("kind"), Source: q.Get("source"),
		From: q.Get("from"), To: q.Get("to"), Text: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit}
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.CostPage, error) {
		return s.fin.Costs(r.Context(), tx, actorOf(r, p), f)
	})
}

func (s *Server) createCost(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.CostInput
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.CostEntry, error) {
		return s.fin.CreateCost(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) deleteCost(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.DeleteCost(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) listWorkLogs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	f := finance.WorkLogFilter{Element: q.Get("element"), MemberID: q.Get("member"), From: q.Get("from"), To: q.Get("to")}
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.WorkLogs(r.Context(), tx, actorOf(r, p), f)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createWorkLog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.WorkLogInput
	if !decodeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.WorkLog, error) {
		return s.fin.CreateWorkLog(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) deleteWorkLog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.DeleteWorkLog(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) recomputeWorkLogs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (finance.RecomputeResult, error) {
		return s.fin.Recompute(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) listRates(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Rates(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) saveRate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.RateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	financeJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*finance.Rate, error) {
		return s.fin.SaveRate(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deleteRate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.DeleteRate(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// ---------------------------------------------------------------------
// Import CSV
// ---------------------------------------------------------------------

func (s *Server) listImportProfiles(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		target := r.URL.Query().Get("target") // vuoto = costi; work_logs = ore
		items, err := s.fin.Profiles(r.Context(), tx, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items, "fields": finance.ImportFieldsFor(target)}, nil
	})
}

func (s *Server) saveImportProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.ProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("id") == "" {
		status = http.StatusCreated
	}
	financeJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*finance.Profile, error) {
		return s.fin.SaveProfile(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) deleteImportProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.fin.DeleteProfile(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Runs(r.Context(), tx, r.URL.Query().Get("target"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*finance.Run, error) {
		return s.fin.RunDetail(r.Context(), tx, r.PathValue("id"))
	})
}

func (s *Server) previewImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.ImportInput
	if !decodeLargeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*finance.Run, error) {
		return s.fin.Preview(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) confirmImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in finance.ConfirmInput
	if !decodeLargeJSON(w, r, &in) {
		return
	}
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Run, error) {
		return s.fin.Confirm(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) rollbackImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*finance.Run, error) {
		return s.fin.Rollback(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) listEVProjects(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	financeJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.fin.Projects(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}
