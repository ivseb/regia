package api

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/portfolio"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Valutazione e priorità del portafoglio (demand.md §6; piano, passo 8): scorecard
// versionate, valutazione delle richieste, graduatoria con linea di budget e ordine manuale.

// portfolioError aggiunge ai casi comuni gli errori di validazione della scorecard.
func (s *Server) portfolioError(w http.ResponseWriter, r *http.Request, err error) {
	var inv *portfolio.InvalidDefinition
	if errors.As(err, &inv) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
			"detail": inv.Error(), "code": "scorecard.invalid", "problems": inv.Problems, "request_id": requestID(r.Context())})
		return
	}
	s.engineError(w, r, err)
}

func (s *Server) listScorecards(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.portfolio.Scorecards(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) getScorecard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*portfolio.Scorecard, error) {
		return s.portfolio.Scorecard(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) saveScorecardDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition portfolio.Definition `json:"definition"`
		Type       string               `json:"type"`
		Notes      string               `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *portfolio.Scorecard
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.portfolio.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Definition, in.Type, in.Notes)
		return err
	})
	if err != nil {
		s.portfolioError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) discardScorecardDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.portfolio.DiscardDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) publishScorecard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Notes string `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *portfolio.Scorecard
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.portfolio.Publish(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Notes)
		return err
	})
	if err != nil {
		s.portfolioError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getEvaluation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*portfolio.Evaluation, error) {
		return s.portfolio.Evaluation(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) scoreRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in portfolio.ScoreInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*portfolio.Result, error) {
		return s.portfolio.Score(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

// budgetParam legge ?budget_cents= (simulazione di un budget diverso da quello salvato).
func budgetParam(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	v := r.URL.Query().Get("budget_cents")
	if v == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "budget_cents deve essere un intero non negativo")
		return nil, false
	}
	return &n, true
}

// rankingScoped calcola la graduatoria, ristretta a ?unit= e ?period= se presenti. Con un'area e un periodo
// il budget è il residuo dello stanziamento dell'area (la RLS decide se lo si vede); senza stanziamento
// visibile resta il budget di prima. ?budget_cents= ha la precedenza (simulazione).
func (s *Server) rankingScoped(r *http.Request, p *auth.Principal, tx pgx.Tx, budget *int64) (*portfolio.Ranking, error) {
	ctx := r.Context()
	f := portfolio.RankFilter{UnitID: r.URL.Query().Get("unit"), PeriodID: r.URL.Query().Get("period")}
	var sc *portfolio.Scope
	if f.UnitID != "" {
		var name string
		if err := tx.QueryRow(ctx, `select name from org_units where id::text = $1`, f.UnitID).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, engine.NotFound("unit", "unit", "unità inesistente")
			}
			return nil, err
		}
		sc = &portfolio.Scope{UnitID: f.UnitID, Unit: name, PeriodID: f.PeriodID}
		if f.PeriodID != "" {
			per, err := s.fin.Periods(ctx, tx, "")
			if err != nil {
				return nil, err
			}
			for _, pr := range per {
				if pr.ID == f.PeriodID {
					sc.Period = pr.Name
				}
			}
			var id string
			err = tx.QueryRow(ctx, `select id::text from budget_allocations where period_id::text = $1 and org_unit_id::text = $2 and not archived limit 1`,
				f.PeriodID, f.UnitID).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				if n, err := s.fin.Node(ctx, tx, actorOf(r, p), id); err == nil {
					sc.HasAllocation, sc.AllocationID, sc.AllocatedCents, sc.ResidualCents = true, n.ID, n.AllocatedCents, max(n.ResidualCents, 0)
					if budget == nil {
						b := sc.ResidualCents
						budget = &b
					}
				} else if !errors.Is(err, engine.ErrNotFound) {
					return nil, err
				}
			}
		}
	}
	rk, err := s.portfolio.RankingFor(ctx, tx, actorOf(r, p), budget, f)
	if err != nil {
		return nil, err
	}
	if sc != nil {
		rk.Scope = sc
		if sc.HasAllocation && r.URL.Query().Get("budget_cents") == "" {
			rk.BudgetSource = portfolio.BudgetSource{Kind: "allocation", FiscalYear: sc.Period}
		}
	}
	return rk, nil
}

func (s *Server) getRanking(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budget, ok := budgetParam(w, r)
	if !ok {
		return
	}
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*portfolio.Ranking, error) {
		return s.rankingScoped(r, p, tx, budget)
	})
}

func (s *Server) rankingCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budget, ok := budgetParam(w, r)
	if !ok {
		return
	}
	var buf bytes.Buffer
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Finance: p.Can(rbac.FinanceRead), Member: p.MemberID}, func(tx pgx.Tx) error {
		rk, err := s.rankingScoped(r, p, tx, budget)
		if err != nil {
			return err
		}
		return rk.WriteCSV(&buf)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="graduatoria-`+time.Now().Format(time.DateOnly)+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) updatePortfolioSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in portfolio.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (portfolio.Settings, error) {
		return s.portfolio.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) moveInRanking(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in portfolio.MoveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*portfolio.Ranking, error) {
		return s.portfolio.Move(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}
