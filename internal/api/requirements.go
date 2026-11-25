package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/approval"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Requisiti, matrice di tracciabilità, verifica, deroghe e gate di rilascio (sdlc.md §6;
// piano, passi 5 e 6).

func (s *Server) requirementsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/requirements/inbox", inTenant(s.requirementInbox))
	mux.HandleFunc("GET /api/v1/elements/{ref}/requirements", inTenant(s.requirementMatrix))
	mux.HandleFunc("GET /api/v1/elements/{ref}/requirements.csv", inTenant(s.requirementMatrixCSV))
	mux.HandleFunc("POST /api/v1/elements/{ref}/requirements", inTenant(s.createRequirement))
	mux.HandleFunc("POST /api/v1/elements/{ref}/requirements/baseline", inTenant(s.applyBaseline))
	mux.HandleFunc("GET /api/v1/requirements/{ref}", inTenant(s.getRequirement))
	mux.HandleFunc("PATCH /api/v1/requirements/{ref}", inTenant(s.updateRequirement))
	mux.HandleFunc("POST /api/v1/requirements/{ref}/cells/{impact}/ready", inTenant(s.requirementReady))
	mux.HandleFunc("POST /api/v1/requirements/{ref}/cells/{impact}/verify", inTenant(s.verifyRequirement))
	mux.HandleFunc("POST /api/v1/requirements/{ref}/exceptions", inTenant(s.requestException))
	mux.HandleFunc("GET /api/v1/exceptions/{ref}", inTenant(s.getException))
	mux.HandleFunc("PATCH /api/v1/exceptions/{ref}", inTenant(s.updateException))
	// Gate di rilascio per le pipeline (devsecops.md §3.5): token API con release.read.
	mux.HandleFunc("GET /api/v1/release-gate", can(rbac.ReleaseRead, s.releaseGate))
	// Configurazione: cataloghi con baseline, livelli di rischio, politica delle deroghe.
	mux.HandleFunc("GET /api/v1/requirement-config", inTenant(s.requirementConfig))
	mux.HandleFunc("PUT /api/v1/requirement-config/settings", inTenant(s.updateRequirementSettings))
	mux.HandleFunc("PUT /api/v1/requirement-config/exception-policy", inTenant(s.updateExceptionPolicy))
	mux.HandleFunc("GET /api/v1/requirement-config/export", inTenant(s.exportRequirements))
	mux.HandleFunc("POST /api/v1/requirement-config/import", inTenant(s.importRequirements))
	mux.HandleFunc("POST /api/v1/requirement-config/preview", inTenant(s.previewBaseline))
	mux.HandleFunc("POST /api/v1/requirement-config/catalogs/validate", inTenant(s.validateCatalog))
	mux.HandleFunc("GET /api/v1/requirement-config/catalogs/{key}", inTenant(s.getCatalog))
	mux.HandleFunc("PUT /api/v1/requirement-config/catalogs/{key}/draft", inTenant(s.saveCatalogDraft))
	mux.HandleFunc("DELETE /api/v1/requirement-config/catalogs/{key}/draft", inTenant(s.discardCatalogDraft))
	mux.HandleFunc("POST /api/v1/requirement-config/catalogs/{key}/publish", inTenant(s.publishCatalog))
}

// reqJSON è tenantJSON con gli errori di validazione dei cataloghi.
func reqJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		var inv *requirements.InvalidDefinition
		if errors.As(err, &inv) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
				"detail": inv.Error(), "code": "catalog.invalid", "problems": inv.Problems, "request_id": requestID(r.Context())})
			return
		}
		s.engineError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) requirementInbox(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, _ := pageParams(r)
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, more, err := s.req.InboxN(r.Context(), tx, actorOf(r, p), limit)
		return map[string]any{"items": items, "more": more}, err
	})
}

func matrixFilter(r *http.Request) requirements.MatrixFilter {
	q := r.URL.Query()
	return requirements.MatrixFilter{Team: q.Get("team"), Category: q.Get("category"), Status: q.Get("status")}
}

func (s *Server) requirementMatrix(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	// limit e offset (facoltativi) paginano le righe; senza, la matrice è intera come sempre.
	flt := matrixFilter(r)
	flt.Limit, flt.Offset = pageParams(r)
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requirements.Matrix, error) {
		return s.req.Matrix(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), flt)
	})
}

func (s *Server) requirementMatrixCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var buf bytes.Buffer
	var name string
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		m, err := s.req.Matrix(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), matrixFilter(r))
		if err != nil {
			return err
		}
		name = m.Request.Key
		if m.Impact != "" {
			name = m.Impact
		}
		return m.WriteCSV(&buf)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="requisiti-`+name+`-`+time.Now().Format(time.DateOnly)+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) createRequirement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*requirements.View, error) {
		return s.req.Create(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) applyBaseline(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Catalog string `json:"catalog"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		keys, err := s.req.ApplyBaselines(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Catalog)
		return map[string]any{"created": keys}, err
	})
}

func (s *Server) getRequirement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requirements.View, error) {
		return s.req.View(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) updateRequirement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.UpdateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.View, error) {
		return s.req.Update(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) requirementReady(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.ReadyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.View, error) {
		return s.req.Ready(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("impact"), in)
	})
}

func (s *Server) verifyRequirement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.VerifyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.View, error) {
		return s.req.Verify(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("impact"), in)
	})
}

func (s *Server) requestException(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.ExceptionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*requirements.ExceptionView, error) {
		return s.req.RequestException(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) getException(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requirements.ExceptionView, error) {
		return s.req.ExceptionView(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) updateException(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.ExceptionUpdate
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.ExceptionView, error) {
		return s.req.UpdateException(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

// releaseGate risponde sì o no con i motivi; la decisione finisce nell'audit (per questo la
// transazione non è di sola lettura, anche se la richiesta è un GET).
func (s *Server) releaseGate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.GateResult, error) {
		return s.req.ReleaseGate(r.Context(), tx, actorOf(r, p), q.Get("application"), q.Get("request"))
	})
}

type roleView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (s *Server) requirementConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ctx, a := r.Context(), actorOf(r, p)
		catalogs, err := s.req.Catalogs(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		settings, err := s.req.Settings(ctx, tx)
		if err != nil {
			return nil, err
		}
		policy, err := s.req.GetPolicy(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		teams, err := s.reviews.Teams(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `select key, name from stakeholder_roles order by name`)
		if err != nil {
			return nil, err
		}
		roles, err := pgx.CollectRows(rows, pgx.RowToStructByPos[roleView])
		if err != nil {
			return nil, err
		}
		if teams == nil {
			teams = []reviews.Team{}
		}
		return map[string]any{"catalogs": catalogs, "settings": settings, "policy": policy, "teams": teams, "roles": roles,
			"statuses": requirements.StatusLabels, "can_edit": p.Can(rbac.ProcessAdmin)}, nil
	})
}

func (s *Server) updateRequirementSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in requirements.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.Settings, error) {
		return s.req.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateExceptionPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Rules []approval.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.Policy, error) {
		return s.req.UpdatePolicy(r.Context(), tx, actorOf(r, p), in.Rules)
	})
}

func (s *Server) exportRequirements(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requirements.Package, error) {
		return s.req.Export(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) importRequirements(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "pacchetto troppo grande")
		return
	}
	pkg, err := requirements.ParsePackage(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	publish := r.URL.Query().Get("publish") == "true"
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.ImportResult, error) {
		return s.req.Import(r.Context(), tx, actorOf(r, p), pkg, publish)
	})
}

func (s *Server) previewBaseline(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Condition string `json:"condition"`
		Scope     string `json:"scope"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.req.PreviewCondition(r.Context(), tx, actorOf(r, p), in.Condition, in.Scope)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) validateCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ps, err := s.req.ValidateDefinition(r.Context(), tx, actorOf(r, p), in.Definition)
		return map[string]any{"problems": ps}, err
	})
}

func (s *Server) getCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requirements.Catalog, error) {
		return s.req.GetCatalog(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) saveCatalogDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
		Notes      string          `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.Catalog, error) {
		return s.req.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Definition, in.Notes)
	})
}

func (s *Server) discardCatalogDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reqJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.req.DiscardDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) publishCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Notes string `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reqJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*requirements.Catalog, error) {
		return s.req.Publish(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Notes)
	})
}
