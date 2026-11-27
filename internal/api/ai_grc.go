package api

import (
	"net/http"

	"regia/internal/ai"
	"regia/internal/platform/auth"
)

// GRC assistito (replica/ai.md §2 punto 5, ondata 5): proposte di collegamento fra evidenze e
// controlli, bozze di policy e runbook, bozza della notifica DORA. Le proposte e le bozze sono
// letture in POST; solo le decisioni e i salvataggi della persona scrivono, con i suoi permessi.

func aiHandler[In any, Out any](s *Server, fn func(r *http.Request, p *auth.Principal, in In) (Out, error)) func(http.ResponseWriter, *http.Request, *auth.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		var in In
		if !decodeJSON(w, r, &in) {
			return
		}
		out, err := fn(r, p, in)
		if err != nil {
			s.aiError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) aiGRCMap(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCMapInput) (*ai.GRCMapResult, error) {
		return s.ai.GRCMap(r.Context(), p.TenantID, actorOf(r, p), s.memberLang(r, p), in)
	})(w, r, p)
}

func (s *Server) aiGRCDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCDecision) (any, error) {
		return s.ai.GRCDecide(r.Context(), p.TenantID, actorOf(r, p), in)
	})(w, r, p)
}

func (s *Server) aiGRCDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCDraftInput) (*ai.GRCDraftResult, error) {
		return s.ai.GRCDraft(r.Context(), p.TenantID, actorOf(r, p), s.memberLang(r, p), in)
	})(w, r, p)
}

func (s *Server) aiGRCDraftSave(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCDraftSave) (*ai.GRCSaved, error) {
		return s.ai.GRCSaveDraft(r.Context(), p.TenantID, actorOf(r, p), in)
	})(w, r, p)
}

func (s *Server) aiGRCNotice(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCNoticeInput) (*ai.GRCNoticeResult, error) {
		return s.ai.GRCNotice(r.Context(), p.TenantID, actorOf(r, p), s.memberLang(r, p), in)
	})(w, r, p)
}

func (s *Server) aiGRCNoticeSave(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	aiHandler(s, func(r *http.Request, p *auth.Principal, in ai.GRCNoticeSave) (*ai.GRCNoticeSaved, error) {
		return s.ai.GRCSaveNotice(r.Context(), p.TenantID, actorOf(r, p), in)
	})(w, r, p)
}
