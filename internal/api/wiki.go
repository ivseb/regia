package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai"
	"regia/internal/ai/wiki"
	"regia/internal/engine"
	"regia/internal/modules/knowledge"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
)

// Wiki personale (replica/ai.md §2 punto 1, ondata 4). Ogni persona lavora sulla sua wiki con i
// suoi permessi: le transazioni hanno il membro (app.member_id), così la RLS della migrazione
// 0087 lascia vedere solo le sue pagine e quelle che gli altri hanno condiviso. Tutto funziona
// anche senza AI; «Aggiorna adesso» e l'aggiornamento automatico richiedono la funzione «wiki».

func (s *Server) wikiRoutes(mux *http.ServeMux) {
	const b = "/api/v1/wiki"
	mux.HandleFunc("GET "+b, inTenant(s.wikiHome))
	mux.HandleFunc("GET "+b+"/log", inTenant(s.wikiLog))
	mux.HandleFunc("GET "+b+"/export", inTenant(s.wikiExport))
	mux.HandleFunc("PUT "+b+"/settings", inTenant(s.wikiSettings))
	mux.HandleFunc("POST "+b+"/refresh", inTenant(s.wikiRefresh))
	mux.HandleFunc("POST "+b+"/add", inTenant(s.wikiAdd))
	mux.HandleFunc("POST "+b+"/pages", inTenant(s.wikiCreate))
	mux.HandleFunc("GET "+b+"/pages/{id}", inTenant(s.wikiGet))
	mux.HandleFunc("PUT "+b+"/pages/{id}", inTenant(s.wikiSave))
	mux.HandleFunc("DELETE "+b+"/pages/{id}", inTenant(s.wikiDelete))
	mux.HandleFunc("GET "+b+"/pages/{id}/history", inTenant(s.wikiHistory))
	mux.HandleFunc("GET "+b+"/pages/{id}/versions/{n}", inTenant(s.wikiVersion))
	mux.HandleFunc("POST "+b+"/pages/{id}/restore", inTenant(s.wikiRestore))
	mux.HandleFunc("POST "+b+"/pages/{id}/share", inTenant(s.wikiShare))
	mux.HandleFunc("POST "+b+"/pages/{id}/promote", inTenant(s.wikiPromote))
	mux.HandleFunc("POST "+b+"/revisions/{id}/undo", inTenant(s.wikiUndo))
}

// wikiTx esegue fn nella transazione della persona e scrive la risposta.
func (s *Server) wikiTx(w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (any, error)) {
	if p.MemberID == "" {
		writeProblem(w, r, http.StatusForbidden, "wiki.no_member", "la wiki è di chi lavora nell'organizzazione")
		return
	}
	var out any
	err := s.ai.WikiTx(r.Context(), p.TenantID, p.MemberID, readOnly, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.wikiError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) wikiError(w http.ResponseWriter, r *http.Request, err error) {
	var we *wiki.Error
	if errors.As(err, &we) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(we.Status)
		body := map[string]any{"type": "about:blank", "title": http.StatusText(we.Status), "status": we.Status, "detail": we.Msg,
			"code": we.Code, "request_id": requestID(r.Context())}
		if we.Field != "" {
			body["field"] = we.Field
		}
		writeJSONBody(w, body)
		return
	}
	s.aiError(w, r, err)
}

type wikiAI struct {
	// Ready: l'aggiornamento con l'AI si può fare (AI accesa, modello con strumenti).
	Ready  bool `json:"ready"`
	Masked bool `json:"masked"`
}

func (s *Server) wikiHomeView(ctx context.Context, tx pgx.Tx, actor engine.Actor) (any, error) {
	pages, err := wiki.List(ctx, tx, actor.MemberID)
	if err != nil {
		return nil, err
	}
	shared := []wiki.Summary{}
	if actor.Has("element.read") {
		if shared, err = wiki.SharedWithMe(ctx, tx, actor.MemberID); err != nil {
			return nil, err
		}
	}
	st, err := wiki.GetState(ctx, tx, actor.MemberID)
	if err != nil {
		return nil, err
	}
	ready, err := s.ai.WikiReady(ctx, tx)
	if err != nil {
		return nil, err
	}
	status, err := s.ai.StatusFor(ctx, tx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pages": pages, "shared": shared, "state": st,
		"ai": wikiAI{Ready: ready, Masked: status.Masked}, "context_tokens": ai.WikiContextChars / 4}, nil
}

func (s *Server) wikiHome(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.wikiHomeView(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) wikiLog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		list, err := wiki.Log(r.Context(), tx, p.MemberID, 100)
		return map[string]any{"items": list}, err
	})
}

// wikiPageView è la pagina con gli elementi citati, risolti con i permessi di chi legge.
func (s *Server) wikiPageView(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (any, error) {
	pg, err := wiki.Get(ctx, tx, actor.MemberID, id)
	if err != nil {
		return nil, err
	}
	// Le pagine condivise le leggono le persone che lavorano negli spazi, non i richiedenti del portale.
	if !pg.Mine && !actor.Has("element.read") {
		return nil, wiki.ErrNotFound
	}
	refs, err := wiki.Refs(ctx, tx, s.eng, actor, pg.Body)
	if err != nil {
		return nil, err
	}
	return map[string]any{"page": pg, "refs": refs}, nil
}

func (s *Server) wikiGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.wikiPageView(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

type wikiSaveIn struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	BaseVersion int    `json:"base_version"`
	Note        string `json:"note"`
}

func (s *Server) wikiCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in wikiSaveIn
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		pg, err := wiki.Save(r.Context(), tx, p.MemberID, wiki.SaveInput{Title: in.Title, Body: in.Body, Author: wiki.AuthorPerson, Note: in.Note})
		if err != nil {
			return nil, err
		}
		return s.wikiPageView(r.Context(), tx, actorOf(r, p), pg.ID)
	})
}

func (s *Server) wikiSave(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in wikiSaveIn
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		pg, err := wiki.Save(r.Context(), tx, p.MemberID, wiki.SaveInput{ID: r.PathValue("id"), Title: in.Title, Body: in.Body,
			BaseVersion: in.BaseVersion, Author: wiki.AuthorPerson, Note: in.Note})
		if err != nil {
			return nil, err
		}
		return s.wikiPageView(r.Context(), tx, actorOf(r, p), pg.ID)
	})
}

func (s *Server) wikiDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, wiki.Delete(r.Context(), tx, p.MemberID, r.PathValue("id"))
	})
}

func (s *Server) wikiHistory(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		list, err := wiki.History(r.Context(), tx, p.MemberID, r.PathValue("id"))
		return map[string]any{"items": list}, err
	})
}

func (s *Server) wikiVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		s.wikiError(w, r, wiki.ErrNotFound)
		return
	}
	s.wikiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return wiki.Diff(r.Context(), tx, p.MemberID, r.PathValue("id"), n)
	})
}

func (s *Server) wikiRestore(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Version int `json:"version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		pg, err := wiki.Restore(r.Context(), tx, p.MemberID, r.PathValue("id"), in.Version, wiki.OpRestore, wiki.AuthorPerson,
			"Ripristinata la versione "+strconv.Itoa(in.Version))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": pg}, nil
	})
}

func (s *Server) wikiUndo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		pg, err := wiki.Undo(r.Context(), tx, p.MemberID, r.PathValue("id"), wiki.AuthorPerson)
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": pg}, nil
	})
}

func (s *Server) wikiShare(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Shared bool `json:"shared"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if _, err := wiki.SetShared(r.Context(), tx, p.MemberID, r.PathValue("id"), in.Shared); err != nil {
			return nil, err
		}
		return s.wikiPageView(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// wikiPromote crea una pagina Knowledge dalla pagina della wiki, con i permessi della persona
// sullo spazio scelto. La pagina della wiki resta com'è; quella Knowledge vive da sola.
func (s *Server) wikiPromote(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Space string `json:"space"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		ctx, actor := r.Context(), actorOf(r, p)
		pg, err := wiki.Get(ctx, tx, p.MemberID, r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if !pg.Mine {
			return nil, wiki.ErrNotOwner
		}
		if strings.TrimSpace(in.Space) == "" {
			return nil, &wiki.Error{Status: 422, Code: "wiki.space_required", Field: "space", Msg: "scegli lo spazio Knowledge"}
		}
		kp, err := s.kb.CreatePage(ctx, tx, actor, strings.TrimSpace(in.Space), knowledge.CreateInput{Title: pg.Title, Body: pg.Body,
			Note: "Dalla wiki personale"})
		if err != nil {
			return nil, err
		}
		if err := wiki.SetPromoted(ctx, tx, p.MemberID, pg.ID, kp.Page.Key); err != nil {
			return nil, err
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "wiki.promote", EntityType: "element", EntityID: kp.Page.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"wiki_page": pg.ID}}); err != nil {
			return nil, err
		}
		return map[string]any{"key": kp.Page.Key}, nil
	})
}

func (s *Server) wikiAdd(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in wiki.AddInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		pg, err := wiki.Add(r.Context(), tx, s.eng, actorOf(r, p), in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": map[string]any{"id": pg.ID, "title": pg.Title, "version": pg.Version}}, nil
	})
}

func (s *Server) wikiSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Auto bool `json:"auto"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	s.wikiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if err := wiki.SetAuto(r.Context(), tx, p.MemberID, in.Auto); err != nil {
			return nil, err
		}
		return s.wikiHomeView(r.Context(), tx, actorOf(r, p))
	})
}

// wikiRefresh avvia subito un aggiornamento con l'AI («Aggiorna adesso») e risponde senza
// aspettarlo (202): l'interfaccia segue lo stato. Se non parte qui, lo prende il lavoro in background.
func (s *Server) wikiRefresh(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if p.MemberID == "" {
		writeProblem(w, r, http.StatusForbidden, "wiki.no_member", "la wiki è di chi lavora nell'organizzazione")
		return
	}
	err := s.ai.WikiTx(r.Context(), p.TenantID, p.MemberID, false, func(tx pgx.Tx) error {
		if _, _, err := s.ai.Resolve(r.Context(), tx, ai.FeatureWiki); err != nil {
			return err
		}
		if ok, err := s.ai.WikiReady(r.Context(), tx); err != nil || !ok {
			if err == nil {
				err = &ai.Error{Status: 409, Code: "ai.wiki_no_tools", Msg: "il modello della wiki non sa usare gli strumenti"}
			}
			return err
		}
		return wiki.Request(r.Context(), tx, p.MemberID)
	})
	if err != nil {
		s.wikiError(w, r, err)
		return
	}
	tenantID, memberID := p.TenantID, p.MemberID
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
		defer cancel()
		if _, err := s.ai.UpdateWiki(ctx, tenantID, memberID, true); err != nil && !errors.Is(err, ai.ErrWikiBusy) {
			s.log.Info("aggiornamento della wiki non riuscito", "errore", err)
		}
	}()
	s.wikiTx(w, r, p, true, http.StatusAccepted, func(tx pgx.Tx) (any, error) {
		return s.wikiHomeView(r.Context(), tx, actorOf(r, p))
	})
}

// wikiExport è la wiki della persona in un file Markdown (i dati personali sono suoi).
func (s *Server) wikiExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if p.MemberID == "" {
		writeProblem(w, r, http.StatusForbidden, "wiki.no_member", "la wiki è di chi lavora nell'organizzazione")
		return
	}
	var md string
	if err := s.ai.WikiTx(r.Context(), p.TenantID, p.MemberID, true, func(tx pgx.Tx) error {
		var err error
		md, err = wiki.Export(r.Context(), tx, p.MemberID)
		return err
	}); err != nil {
		s.wikiError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="wiki-`+time.Now().Format("2006-01-02")+`.md"`)
	_, _ = w.Write([]byte(md))
}
