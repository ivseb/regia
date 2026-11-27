package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai"
	aianthropic "regia/internal/ai/anthropic"
	"regia/internal/ai/openaicompat"
	"regia/internal/ai/mcpserver"
	"regia/internal/ai/tools"
	"regia/internal/devtools/fakeai"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/secretbox"
)

// AI (replica/ai.md, ondata 1: le fondamenta). Le regole stanno in internal/ai; gli strumenti
// in internal/ai/tools passano dai servizi del server con l'attore della persona.
//   - impostazioni, fornitori e consumo: tenant.admin (modifiche solo da una sessione, come le
//     integrazioni: un token API rubato non cambia fornitore né chiave);
//   - stato, conversazioni, domande e proposte: chiunque sia nel tenant, con i suoi permessi.

// aiKinds sono i tipi di fornitore: Anthropic e compatibile OpenAI; il finto solo in sviluppo
// (regia serve --dev) e nei test, come i finti connettori.
func aiKinds(dev bool) []ai.Kind {
	kinds := []ai.Kind{aianthropic.Register(), openaicompat.Register()}
	if dev {
		kinds = append(kinds, fakeai.Register())
	}
	return kinds
}

func (s *Server) aiInit(o Options) {
	s.ai = o.AI
	if s.ai == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		s.ai = ai.New(ai.Options{DB: o.DB, Box: box, Kinds: aiKinds(o.Dev), Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log,
			Tools: s.AITools()})
	}
}

// AITools sono gli strumenti dell'AI con i servizi di questo server (anche per serve.go e il server MCP).
func (s *Server) AITools() *tools.Services {
	return &tools.Services{DB: s.db, Engine: s.eng, Collab: s.collab, Work: s.work, Arch: s.arch}
}

func (s *Server) aiRoutes(mux *http.ServeMux) {
	const b = "/api/v1/ai"
	mux.HandleFunc("GET "+b+"/status", inTenant(s.aiStatus))
	mux.HandleFunc("GET "+b+"/settings", adminOnly(s.aiSettings))
	mux.HandleFunc("PUT "+b+"/settings", adminSession(s.aiSaveSettings))
	mux.HandleFunc("POST "+b+"/providers", adminSession(s.aiCreateProvider))
	mux.HandleFunc("PATCH "+b+"/providers/{id}", adminSession(s.aiUpdateProvider))
	mux.HandleFunc("DELETE "+b+"/providers/{id}", adminSession(s.aiDeleteProvider))
	mux.HandleFunc("GET "+b+"/providers/{id}/models", adminOnly(s.aiProviderModels))
	mux.HandleFunc("GET "+b+"/usage", adminOnly(s.aiUsage))
	mux.HandleFunc("GET "+b+"/conversations", inTenant(s.aiConversations))
	mux.HandleFunc("GET "+b+"/conversations/{id}", inTenant(s.aiConversation))
	mux.HandleFunc("DELETE "+b+"/conversations/{id}", inTenant(s.aiDeleteConversation))
	mux.HandleFunc("POST "+b+"/ask", inTenant(s.aiAsk))
	mux.HandleFunc("POST "+b+"/proposals/{id}/confirm", inTenant(s.aiConfirm))
	mux.HandleFunc("POST "+b+"/proposals/{id}/reject", inTenant(s.aiReject))
	// Server MCP (ondata 2): solo con un token API, nel tenant del token.
	mux.Handle(MCPPath, mcpserver.Handler(mcpserver.Options{AI: s.ai, DB: s.db, Version: s.version, BaseURL: s.adm.baseURL, Log: s.log,
		Caller: func(r *http.Request) *mcpserver.Caller {
			p := principal(r.Context())
			if p == nil || p.TokenID == "" || p.TenantID == "" {
				return nil
			}
			return &mcpserver.Caller{TenantID: p.TenantID, Actor: actorOf(r, p), ReadOnly: p.ReadOnly()}
		}}))
}

// MCPPath è l'indirizzo del server MCP.
const MCPPath = "/api/v1/mcp"

// aiError traduce gli errori dell'AI; gli altri vanno al traduttore del motore.
func (s *Server) aiError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *ai.Error
	var pe *ai.ProviderError
	switch {
	case errors.As(err, &ae):
		body := map[string]any{"type": "about:blank", "title": http.StatusText(ae.Status), "status": ae.Status, "detail": ae.Msg,
			"code": ae.Code, "request_id": requestID(r.Context())}
		if ae.Field != "" {
			body["field"] = ae.Field
		}
		if ae.Params != nil {
			body["params"] = ae.Params
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(ae.Status)
		writeJSONBody(w, body)
	case errors.As(err, &pe):
		writeProblemParams(w, r, http.StatusBadGateway, "ai.provider_error", pe.Error(), map[string]any{"reason": pe.Code})
	default:
		s.engineError(w, r, err)
	}
}

func (s *Server) aiTx(w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (any, error)) {
	var out any
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) { return s.ai.StatusFor(r.Context(), tx) })
}

type aiKindView struct {
	Key      string         `json:"key"`
	NeedsKey bool           `json:"needs_key"`
	NeedsURL bool           `json:"needs_url"`
	External bool           `json:"external"`
	Tools    bool           `json:"tools"`
	Proposed []ai.ModelInfo `json:"proposed"`
}

func (s *Server) aiSettingsView(r *http.Request, tx pgx.Tx) (any, error) {
	st, err := ai.LoadSettings(r.Context(), tx)
	if err != nil {
		return nil, err
	}
	provs, err := s.ai.Providers(r.Context(), tx)
	if err != nil {
		return nil, err
	}
	bud, err := s.ai.Budget(r.Context(), tx)
	if err != nil {
		return nil, err
	}
	kinds := []aiKindView{}
	for _, k := range s.ai.Kinds() {
		prop := k.Proposed
		if prop == nil {
			prop = []ai.ModelInfo{}
		}
		kinds = append(kinds, aiKindView{Key: k.Key, NeedsKey: k.NeedsKey, NeedsURL: k.NeedsURL, External: k.External, Tools: k.Tools, Proposed: prop})
	}
	return map[string]any{"settings": st, "providers": provs, "kinds": kinds, "features": ai.Features, "budget": bud, "dev": s.dev}, nil
}

func (s *Server) aiSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) { return s.aiSettingsView(r, tx) })
}

func (s *Server) aiSaveSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ai.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.aiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if _, err := s.ai.SaveSettings(r.Context(), tx, actorOf(r, p), in); err != nil {
			return nil, err
		}
		return s.aiSettingsView(r, tx)
	})
}

func (s *Server) aiCreateProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ai.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.aiTx(w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		return s.ai.CreateProvider(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) aiUpdateProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ai.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	s.aiTx(w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.ai.UpdateProvider(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) aiDeleteProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.ai.DeleteProvider(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) aiProviderModels(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		list, err := s.ai.ProviderModels(r.Context(), tx, r.PathValue("id"))
		if list == nil {
			list = []ai.ModelInfo{}
		}
		return map[string]any{"items": list}, err
	})
}

func (s *Server) aiUsage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.ai.Usage(r.Context(), tx, r.URL.Query().Get("month"))
	})
}

func (s *Server) aiConversations(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		list, err := s.ai.Conversations(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": list}, err
	})
}

func (s *Server) aiConversation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		c, msgs, err := s.ai.Messages(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return map[string]any{"conversation": c, "messages": msgs}, err
	})
}

func (s *Server) aiDeleteConversation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiTx(w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.ai.DeleteConversation(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) aiAsk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ai.AskInput
	if !decodeJSON(w, r, &in) {
		return
	}
	lang := i18n.Default
	_ = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		l, err := i18n.MemberLang(r.Context(), tx, p.MemberID)
		if err == nil {
			lang = l
		}
		return nil
	})
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.aiAskStream(w, r, p, lang, in)
		return
	}
	res, err := s.ai.Ask(r.Context(), p.TenantID, actorOf(r, p), lang, in)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// aiAskStream risponde in streaming (Server-Sent Events): eventi «text», «tool» e «step»
// mentre l'assistente lavora, poi «done» con lo stesso corpo della risposta JSON, oppure
// «error» con il problema (stesso formato degli errori dell'API).
func (s *Server) aiAskStream(w http.ResponseWriter, r *http.Request, p *auth.Principal, lang string, in ai.AskInput) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // una risposta lunga dura più del WriteTimeout del server
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		_ = rc.Flush()
	}
	send("start", map[string]any{})
	res, err := s.ai.AskStream(r.Context(), p.TenantID, actorOf(r, p), lang, in, func(ev ai.AskEvent) { send(ev.Type, ev) })
	if err != nil {
		rec := httptest.NewRecorder()
		s.aiError(rec, r, err)
		var problem map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &problem)
		if problem == nil {
			problem = map[string]any{"status": rec.Code}
		}
		send("error", problem)
		return
	}
	send("done", res)
}

func (s *Server) aiDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal, confirm bool) {
	res, err := s.ai.Decide(r.Context(), p.TenantID, actorOf(r, p), r.PathValue("id"), confirm)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) aiConfirm(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiDecide(w, r, p, true)
}

func (s *Server) aiReject(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.aiDecide(w, r, p, false)
}
