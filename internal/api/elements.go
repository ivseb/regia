package api

import (
	"cmp"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

func actorOf(r *http.Request, p *auth.Principal) engine.Actor {
	return engine.Actor{AccountID: p.AccountID, MemberID: p.MemberID, Permissions: p.Permissions, IP: clientIP(r)}
}

// inTenant richiede una sessione con un tenant attivo; i permessi fini li verifica il motore.
func inTenant(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.TenantID == "" {
			writeProblem(w, r, http.StatusForbidden, "tenant.required", "nessun tenant attivo")
			return
		}
		h(w, r, p)
	})
}

// engineError traduce gli errori del dominio in risposte HTTP.
func (s *Server) engineError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *engine.ValidationError
	var qe *engine.QueryError
	switch {
	case errors.As(err, &qe):
		// Errore della query con la posizione (unità UTF-16), per sottolinearlo nella barra.
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
			"detail": qe.Msg, "code": "query.invalid", "field": "q", "pos": qe.Pos, "end": qe.End, "request_id": requestID(r.Context())})
	case errors.As(err, &ve):
		pr := Problem{Type: "about:blank", Title: "Unprocessable Entity", Status: http.StatusUnprocessableEntity,
			Detail: ve.Error(), Code: cmp.Or(ve.Code, "validation"), RequestID: requestID(r.Context())}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(pr.Status)
		body := map[string]any{"type": pr.Type, "title": pr.Title, "status": pr.Status, "detail": pr.Detail,
			"code": pr.Code, "field": ve.Field, "request_id": pr.RequestID}
		if len(ve.Params) > 0 {
			body["params"] = ve.Params
		}
		writeJSONBody(w, body)
	case errors.Is(err, engine.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "element.not_found", err.Error())
	case errors.Is(err, engine.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, "auth.forbidden", err.Error())
	case errors.Is(err, engine.ErrConflict):
		writeProblem(w, r, http.StatusConflict, "element.version_conflict", err.Error())
	case errors.Is(err, approval.ErrNotPending), errors.Is(err, approval.ErrNotYourTurn):
		writeProblem(w, r, http.StatusConflict, "approval.state", err.Error())
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	var page struct {
		engine.Page
		// Demand: colonne della domanda per le richieste della pagina (solo con demand=1).
		Demand map[string]demand.Column `json:"demand,omitempty"`
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		page.Page, err = s.eng.Search(r.Context(), tx, actorOf(r, p), q.Get("q"), limit, q.Get("cursor"))
		if err != nil {
			return err
		}
		// Etichette dei riferimenti dei campi personalizzati, per le colonne della tabella.
		page.Labels, err = engine.PageLabels(r.Context(), tx, page.Items)
		if err != nil {
			return err
		}
		// Etichette (tag) degli elementi della pagina: una sola query per pagina.
		if err = engine.AttachTags(r.Context(), tx, actorOf(r, p).MemberID, page.Items); err != nil || q.Get("demand") != "1" {
			return err
		}
		var ids []string
		for _, el := range page.Items {
			if el.TypeKey == demand.TypeKey {
				ids = append(ids, el.ID)
			}
		}
		page.Demand, err = s.demand.Columns(r.Context(), tx, ids)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

type elementDetail struct {
	Element      *engine.Element           `json:"element"`
	Stakeholders []engine.Stakeholder      `json:"stakeholders"`
	Transitions  []engine.TransitionView   `json:"transitions"`
	Demand       *demand.View              `json:"demand,omitempty"`
	Approvals    []approval.RequestView    `json:"approvals"`
	Impact       *architecture.Impact      `json:"impact,omitempty"`
	Application  *architecture.Application `json:"application,omitempty"`
	Phases       []engine.State            `json:"phases"`
	Process      *engine.ProcessRef        `json:"process"`
	// CustomFields: campi personalizzati del tipo con valore, visibilità e obbligatorietà.
	CustomFields []engine.FieldView `json:"custom_fields"`
}

func (s *Server) getType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var t *engine.TypeInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		t, err = engine.LoadType(r.Context(), tx, r.PathValue("key"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) getElement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out elementDetail
	actor := actorOf(r, p)
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		ctx := r.Context()
		el, err := s.eng.Get(ctx, tx, actor, r.PathValue("ref"))
		if err != nil {
			return err
		}
		out.Element = el
		if out.Stakeholders, err = engine.Stakeholders(ctx, tx, el.ID); err != nil {
			return err
		}
		if out.Transitions, err = s.eng.Available(ctx, tx, actor, el); err != nil {
			return err
		}
		if out.Phases, out.Process, err = s.eng.Phases(ctx, tx, el); err != nil {
			return err
		}
		if out.CustomFields, err = s.eng.FieldViews(ctx, tx, el); err != nil {
			return err
		}
		if s.arch != nil {
			switch el.TypeKey {
			case architecture.ImpactType:
				if out.Impact, err = s.arch.ImpactView(ctx, tx, el.ID); err != nil {
					return err
				}
			case architecture.AppType:
				if out.Application, err = s.arch.Application(ctx, tx, el.ID); err != nil {
					return err
				}
			}
		}
		if el.TypeKey == demand.TypeKey {
			if out.Demand, err = s.demand.View(ctx, tx, el.ID); err != nil {
				return err
			}
		}
		out.Approvals, err = s.approvals.ForElement(ctx, tx, actor, el.ID)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateElement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Version  int            `json:"version"`
		Title    *string        `json:"title"`
		BodyText *string        `json:"body_text"`
		Fields   map[string]any `json:"fields"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var el *engine.Element
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		el, err = s.eng.Update(r.Context(), tx, actorOf(r, p), r.PathValue("id"),
			engine.UpdateInput{Version: in.Version, Title: in.Title, BodyText: in.BodyText, Fields: in.Fields})
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, el)
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		To      string `json:"to"`
		Comment string `json:"comment"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var el *engine.Element
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		el, err = s.eng.Transition(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.To, in.Comment, engine.TransitionOpts{})
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, el)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	var items []engine.Activity
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		if err != nil {
			return err
		}
		ctx := langCtx(r.Context(), tx, p)
		if items, err = engine.History(ctx, tx, el.ID, after, 200); err != nil {
			return err
		}
		return s.eng.Describe(ctx, tx, actorOf(r, p), items)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---------------------------------------------------------------------
// Demand
// ---------------------------------------------------------------------

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in demand.NewRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	var el *engine.Element
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		el, err = s.demand.Create(r.Context(), tx, actorOf(r, p), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/elements/"+el.Key)
	writeJSON(w, http.StatusCreated, el)
}

func (s *Server) updateRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in demand.Data
	if !decodeJSON(w, r, &in) {
		return
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return s.demand.UpdateData(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) inbox(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var items []approval.InboxItem
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		items, err = s.approvals.Inbox(r.Context(), tx, actorOf(r, p))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Decision   string `json:"decision"`
		Comment    string `json:"comment"`
		Conditions string `json:"conditions"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return s.approvals.Decide(r.Context(), tx, actorOf(r, p), r.PathValue("id"),
			approval.DecideInput{Decision: in.Decision, Comment: in.Comment, Conditions: in.Conditions})
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// Organizzazione
// ---------------------------------------------------------------------

func (s *Server) listOrgUnits(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var items []demand.OrgUnit
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		q, limit, ids := searchParams(r)
		items, err = demand.SearchOrgUnits(r.Context(), tx, likeEscape(q), limit, ids)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createOrgUnit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in demand.OrgUnit
	if !decodeJSON(w, r, &in) {
		return
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		in, err = demand.CreateOrgUnit(r.Context(), tx, actorOf(r, p), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

type memberView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	MemberType  string `json:"member_type"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var items []memberView
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		q, limit, ids := searchParams(r)
		// q cerca nel nome, ids restringe a quelle voci, limit 0 = tutti.
		rows, err := tx.Query(r.Context(), `select id, display_name, member_type from members
			where status = 'active' and ($1 = '' or display_name ilike '%' || $1 || '%')
			  and (cardinality($2::text[]) = 0 or id::text = any($2))
			order by display_name, id limit nullif($3, 0)`, likeEscape(q), ids, limit)
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[memberView])
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
