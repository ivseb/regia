package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Valutazioni con questionari (sdlc.md §4-5; piano, passo 4): configurazione per tenant
// (team, tipi, modelli versionati), valutazioni di una richiesta o di un impatto,
// compilazione, esame del team con prescrizioni ed esito, inbox.

func (s *Server) reviewsHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/reviews/inbox", inTenant(s.reviewInbox))
	mux.HandleFunc("GET /api/v1/review-config", inTenant(s.reviewConfig))
	mux.HandleFunc("GET /api/v1/review-config/export", inTenant(s.exportReviews))
	mux.HandleFunc("POST /api/v1/review-config/import", inTenant(s.importReviews))
	mux.HandleFunc("POST /api/v1/review-config/teams", inTenant(s.createReviewTeam))
	mux.HandleFunc("PATCH /api/v1/review-config/teams/{id}", inTenant(s.updateReviewTeam))
	mux.HandleFunc("POST /api/v1/review-config/types", inTenant(s.createReviewType))
	mux.HandleFunc("PATCH /api/v1/review-config/types/{id}", inTenant(s.updateReviewType))
	mux.HandleFunc("GET /api/v1/review-config/template-facts", inTenant(s.templateFacts))
	mux.HandleFunc("POST /api/v1/review-config/templates/validate", inTenant(s.validateTemplate))
	mux.HandleFunc("GET /api/v1/review-config/templates/{key}", inTenant(s.getTemplate))
	mux.HandleFunc("GET /api/v1/review-config/templates/{key}/versions/{n}", inTenant(s.getTemplateVersion))
	mux.HandleFunc("PUT /api/v1/review-config/templates/{key}/draft", inTenant(s.saveTemplateDraft))
	mux.HandleFunc("DELETE /api/v1/review-config/templates/{key}/draft", inTenant(s.discardTemplateDraft))
	mux.HandleFunc("POST /api/v1/review-config/templates/{key}/publish", inTenant(s.publishTemplate))
	mux.HandleFunc("GET /api/v1/elements/{ref}/reviews", inTenant(s.elementReviews))
	mux.HandleFunc("POST /api/v1/elements/{ref}/reviews", inTenant(s.openReview))
	mux.HandleFunc("GET /api/v1/reviews/{ref}", inTenant(s.getReview))
	mux.HandleFunc("PUT /api/v1/reviews/{ref}/answers", inTenant(s.saveAnswers))
	mux.HandleFunc("GET /api/v1/reviews/{ref}/history", inTenant(s.answerHistory))
	mux.HandleFunc("PUT /api/v1/reviews/{ref}/assignments/{section}", inTenant(s.assignSection))
	mux.HandleFunc("POST /api/v1/reviews/{ref}/prescriptions", inTenant(s.addPrescription))
	mux.HandleFunc("PATCH /api/v1/reviews/{ref}/prescriptions/{id}", inTenant(s.updatePrescription))
	mux.HandleFunc("DELETE /api/v1/reviews/{ref}/prescriptions/{id}", inTenant(s.removePrescription))
	mux.HandleFunc("POST /api/v1/reviews/{ref}/decision", inTenant(s.decideReview))
}

// reviewsError aggiunge ai casi comuni i problemi di validazione dei modelli.
func (s *Server) reviewsError(w http.ResponseWriter, r *http.Request, err error) {
	var inv *reviews.InvalidDefinition
	if errors.As(err, &inv) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
			"detail": inv.Error(), "code": "questionnaire.invalid", "problems": inv.Problems, "request_id": requestID(r.Context())})
		return
	}
	s.engineError(w, r, err)
}

// reviewJSON è tenantJSON con gli errori dei modelli.
func reviewJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.reviewsError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) reviewInbox(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, _ := pageParams(r) // facoltativo: voci per coda (di default le 300 più urgenti)
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.Inbox, error) {
		return s.reviews.InboxN(r.Context(), tx, actorOf(r, p), limit)
	})
}

type groupView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
}

func (s *Server) reviewConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ctx, a := r.Context(), actorOf(r, p)
		teams, err := s.reviews.Teams(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		types, err := s.reviews.Types(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		templates, err := s.reviews.Templates(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `select g.id, g.name, (select count(*) from group_members gm where gm.group_id = g.id)::int
			from groups g where g.system_kind is null order by g.name`)
		if err != nil {
			return nil, err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowToStructByPos[groupView])
		if err != nil {
			return nil, err
		}
		triggers, err := s.reviews.Triggers(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		return map[string]any{"teams": teams, "types": types, "templates": templates, "groups": groups,
			"triggers": triggers, "outcomes_default": reviews.DefaultOutcomes, "can_edit": p.Can("process.admin")}, nil
	})
}

func (s *Server) exportReviews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.Package, error) {
		return s.reviews.Export(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) importReviews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "pacchetto troppo grande")
		return
	}
	pkg, err := reviews.ParsePackage(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	publish := r.URL.Query().Get("publish") == "true"
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.ImportResult, error) {
		return s.reviews.Import(r.Context(), tx, actorOf(r, p), pkg, publish)
	})
}

func (s *Server) createReviewTeam(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.TeamInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*reviews.Team, error) {
		return s.reviews.CreateTeam(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateReviewTeam(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.TeamInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.Team, error) {
		return s.reviews.UpdateTeam(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) createReviewType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.TypeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*reviews.ReviewType, error) {
		return s.reviews.CreateType(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateReviewType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.TypeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.ReviewType, error) {
		return s.reviews.UpdateType(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

// validateTemplate controlla una definizione senza salvarla (per l'editor).
func (s *Server) validateTemplate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	d, err := reviews.DecodeDefinition(in.Definition)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": d.Validate()})
}

func (s *Server) templateFacts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.TemplateFacts, error) {
		return s.reviews.TemplateFacts(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
	})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.Template, error) {
		return s.reviews.Template(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) getTemplateVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid", "numero di versione non valido")
		return
	}
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.TemplateVersion, error) {
		return s.reviews.TemplateVersionByNumber(r.Context(), tx, actorOf(r, p), r.PathValue("key"), n)
	})
}

func (s *Server) saveTemplateDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition json.RawMessage `json:"definition"`
		Notes      string          `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.Template, error) {
		return s.reviews.SaveTemplateDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Definition, in.Notes)
	})
}

func (s *Server) discardTemplateDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.reviews.DiscardTemplateDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) publishTemplate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Notes string `json:"notes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.Template, error) {
		return s.reviews.PublishTemplate(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Notes)
	})
}

func (s *Server) elementReviews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.ElementReviews, error) {
		return s.reviews.ForElement(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) openReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Type   string `json:"type"`
		Impact string `json:"impact"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*reviews.View, error) {
		el, err := s.reviews.OpenManual(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Type, in.Impact)
		if err != nil {
			return nil, err
		}
		return s.reviews.View(r.Context(), tx, actorOf(r, p), el.ID)
	})
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.View(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) saveAnswers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Answers map[string]any `json:"answers"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.SaveAnswers(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Answers)
	})
}

func (s *Server) answerHistory(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.reviews.History(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.URL.Query().Get("question"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) assignSection(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MemberID string `json:"member_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.Assign(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("section"), in.MemberID)
	})
}

func (s *Server) addPrescription(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.PrescriptionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.AddPrescription(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) updatePrescription(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.PrescriptionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.UpdatePrescription(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("id"), in)
	})
}

func (s *Server) removePrescription(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.RemovePrescription(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("id"))
	})
}

func (s *Server) decideReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in reviews.DecisionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviews.View, error) {
		return s.reviews.Decide(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}
