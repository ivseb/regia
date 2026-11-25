package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/knowledge"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Knowledge (piano, passo 19; replica/knowledge.md): spazi di documentazione, pagine ad
// albero, versioni con confronto e ripristino, blocco di modifica, template, documenti
// controllati, ricerca nello spazio, collegamenti agli elementi.

func (s *Server) knowledgeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/kb/home", inTenant(s.kbHome))
	mux.HandleFunc("GET /api/v1/kb/spaces", inTenant(s.kbSpaces))
	mux.HandleFunc("POST /api/v1/kb/spaces", inTenant(s.kbCreateSpace))
	mux.HandleFunc("GET /api/v1/kb/spaces/{key}", inTenant(s.kbTree))
	mux.HandleFunc("PATCH /api/v1/kb/spaces/{key}", inTenant(s.kbUpdateSpace))
	mux.HandleFunc("GET /api/v1/kb/spaces/{key}/access", inTenant(s.kbAccess))
	mux.HandleFunc("POST /api/v1/kb/spaces/{key}/access", inTenant(s.kbGrant))
	mux.HandleFunc("DELETE /api/v1/kb/spaces/{key}/access/{id}", inTenant(s.kbRevoke))
	mux.HandleFunc("GET /api/v1/kb/spaces/{key}/search", inTenant(s.kbSearch))
	mux.HandleFunc("GET /api/v1/kb/spaces/{key}/templates", inTenant(s.kbTemplates))
	mux.HandleFunc("POST /api/v1/kb/spaces/{key}/templates", inTenant(s.kbCreateTemplate))
	mux.HandleFunc("PATCH /api/v1/kb/templates/{id}", inTenant(s.kbUpdateTemplate))
	mux.HandleFunc("POST /api/v1/kb/spaces/{key}/pages", inTenant(s.kbCreatePage))
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}", inTenant(s.kbPage))
	mux.HandleFunc("PUT /api/v1/kb/pages/{ref}", inTenant(s.kbSavePage))
	mux.HandleFunc("DELETE /api/v1/kb/pages/{ref}", inTenant(s.kbDeletePage))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/move", inTenant(s.kbMovePage))
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}/versions", inTenant(s.kbVersions))
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}/versions/{n}", inTenant(s.kbVersion))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/versions/{n}/restore", inTenant(s.kbRestore))
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}/diff", inTenant(s.kbDiff))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/lock", inTenant(s.kbLock))
	mux.HandleFunc("DELETE /api/v1/kb/pages/{ref}/lock", inTenant(s.kbUnlock))
	mux.HandleFunc("PUT /api/v1/kb/pages/{ref}/control", inTenant(s.kbControl))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/reviewed", inTenant(s.kbReviewed))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/links", inTenant(s.kbLink))
	mux.HandleFunc("DELETE /api/v1/kb/pages/{ref}/links/{target}", inTenant(s.kbUnlink))
	mux.HandleFunc("GET /api/v1/kb/elements/{ref}/documents", inTenant(s.kbDocuments))
	mux.HandleFunc("GET /api/v1/kb/refs", inTenant(s.kbRefs))
	// Commenti in linea, conferma di lettura, export e import in Markdown (knowledge_plus.go).
	s.knowledgePlusRoutes(mux)
}

// kbJSON è tenantJSON con gli errori del modulo (blocco e versione superata: 409).
func kbJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	var le *knowledge.LockedError
	switch {
	case errors.As(err, &le):
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Conflict", "status": http.StatusConflict,
			"detail": le.Error(), "code": "page.locked", "lock": le.Lock, "request_id": requestID(r.Context())})
		return
	case errors.Is(err, knowledge.ErrStale):
		writeProblem(w, r, http.StatusConflict, "page.stale", err.Error())
		return
	case err != nil:
		s.engineError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

// kbPageDetail è la pagina con passaggi e approvazioni del documento controllato.
type kbPageDetail struct {
	*knowledge.PageView
	Transitions []engine.TransitionView `json:"transitions"`
	Phases      []engine.State          `json:"phases"`
	Approvals   []approval.RequestView  `json:"approvals"`
}

func (s *Server) kbDetail(r *http.Request, tx pgx.Tx, actor engine.Actor, v *knowledge.PageView) (*kbPageDetail, error) {
	out := &kbPageDetail{PageView: v, Transitions: []engine.TransitionView{}, Phases: []engine.State{}, Approvals: []approval.RequestView{}}
	if !v.Page.Controlled {
		return out, nil
	}
	ctx := r.Context()
	el, err := s.eng.Get(ctx, tx, actor, v.Page.ID)
	if err != nil {
		return nil, err
	}
	if out.Transitions, err = s.eng.Available(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if out.Phases, _, err = s.eng.Phases(ctx, tx, el); err != nil {
		return nil, err
	}
	if s.approvals != nil {
		if out.Approvals, err = s.approvals.ForElement(ctx, tx, actor, el.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// kbPageJSON esegue fn (che restituisce la pagina) e risponde con il dettaglio completo.
func (s *Server) kbPageJSON(w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int,
	fn func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error)) {
	actor := actorOf(r, p)
	kbJSON(s, w, r, p, readOnly, status, func(tx pgx.Tx) (*kbPageDetail, error) {
		v, err := fn(tx, actor)
		if err != nil {
			return nil, err
		}
		return s.kbDetail(r, tx, actor, v)
	})
}

func (s *Server) kbHome(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.Home, error) {
		return s.kb.Home(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) kbSpaces(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Spaces(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbCreateSpace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.SpaceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*knowledge.Space, error) {
		return s.kb.CreateSpace(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) kbTree(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *knowledge.SpaceView
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.kb.Tree(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSONCompressed(w, r, out)
}

func (s *Server) kbUpdateSpace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.SpaceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.Space, error) {
		return s.kb.UpdateSpace(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) kbAccess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Access(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.GrantInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Grant(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbRevoke(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Revoke(r.Context(), tx, actorOf(r, p), r.PathValue("key"), r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbSearch(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Search(r.Context(), tx, actorOf(r, p), r.PathValue("key"), r.URL.Query().Get("q"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbTemplates(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Templates(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbCreateTemplate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.TemplateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*knowledge.Template, error) {
		return s.kb.CreateTemplate(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) kbUpdateTemplate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.TemplateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.Template, error) {
		return s.kb.UpdateTemplate(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) kbCreatePage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusCreated, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.CreatePage(r.Context(), tx, actor, r.PathValue("key"), in)
	})
}

func (s *Server) kbPage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.kbPageJSON(w, r, p, true, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.GetPage(r.Context(), tx, actor, r.PathValue("ref"))
	})
}

func (s *Server) kbSavePage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.SaveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.SavePage(r.Context(), tx, actor, r.PathValue("ref"), in)
	})
}

func (s *Server) kbDeletePage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.kb.DeletePage(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) kbMovePage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.MoveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.MovePage(r.Context(), tx, actor, r.PathValue("ref"), in)
	})
}

func (s *Server) kbVersions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Versions(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"items": items}, err
	})
}

func versionParam(r *http.Request, name string) (int, bool) {
	v := r.PathValue(name)
	if v == "" {
		v = r.URL.Query().Get(name)
	}
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n > 0
}

func (s *Server) kbVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := versionParam(r, "n")
	if !ok || n == 0 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "numero di versione non valido")
		return
	}
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.VersionView, error) {
		return s.kb.Version(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), n)
	})
}

func (s *Server) kbRestore(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := versionParam(r, "n")
	if !ok || n == 0 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "numero di versione non valido")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.Restore(r.Context(), tx, actor, r.PathValue("ref"), n, in.Note)
	})
}

func (s *Server) kbDiff(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	from, ok1 := versionParam(r, "from")
	to, ok2 := versionParam(r, "to")
	if !ok1 || !ok2 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "from e to sono numeri di versione")
		return
	}
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.Comparison, error) {
		return s.kb.Compare(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), from, to)
	})
}

func (s *Server) kbLock(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	force := r.URL.Query().Get("force") == "true"
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.LockResult, error) {
		return s.kb.AcquireLock(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), force)
	})
}

func (s *Server) kbUnlock(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.kb.ReleaseLock(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) kbControl(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.ControlInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.SetControl(r.Context(), tx, actor, r.PathValue("ref"), in)
	})
}

func (s *Server) kbReviewed(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.ReviewDone(r.Context(), tx, actor, r.PathValue("ref"), in.Note)
	})
}

func (s *Server) kbLink(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Element string `json:"element"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.Link(r.Context(), tx, actor, r.PathValue("ref"), in.Element)
	})
}

func (s *Server) kbUnlink(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.kbPageJSON(w, r, p, false, http.StatusOK, func(tx pgx.Tx, actor engine.Actor) (*knowledge.PageView, error) {
		return s.kb.Unlink(r.Context(), tx, actor, r.PathValue("ref"), r.PathValue("target"))
	})
}

func (s *Server) kbDocuments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Documents(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) kbRefs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.kb.Refs(r.Context(), tx, actorOf(r, p), keys)
		return map[string]any{"items": items}, err
	})
}
