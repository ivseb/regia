package api

import (
	"errors"
	"net/http"

	"regia/internal/platform/auth"
)

// Secondo fattore TOTP nel login e nel profilo, e pagine pubbliche dei link monouso
// (invito, scelta della password). Le regole stanno in internal/platform/auth/mfa.go
// e internal/platform/admin/links.go.

const (
	mfaCookie     = "regia_mfa"
	mfaCookiePath = "/api/v1/auth/mfa"
)

func (s *Server) mfaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", s.mfaVerify)
	mux.HandleFunc("POST /api/v1/auth/mfa/setup", s.mfaChallengeSetup)
	mux.HandleFunc("POST /api/v1/auth/mfa/setup/confirm", s.mfaChallengeConfirm)

	mux.HandleFunc("GET /api/v1/me/mfa", session(s.mfaStatus))
	mux.HandleFunc("POST /api/v1/me/mfa/setup", session(s.mfaBegin))
	mux.HandleFunc("POST /api/v1/me/mfa/enable", session(s.mfaEnable))
	mux.HandleFunc("POST /api/v1/me/mfa/disable", session(s.mfaDisable))
	mux.HandleFunc("POST /api/v1/me/mfa/recovery-codes", session(s.mfaRecoveryCodes))

	mux.HandleFunc("GET /api/v1/auth/invitations/{token}", s.invitationInfo)
	mux.HandleFunc("POST /api/v1/auth/invitations/{token}/accept", s.acceptInvitation)
	mux.HandleFunc("GET /api/v1/auth/password-reset/{token}", s.resetInfo)
	mux.HandleFunc("POST /api/v1/auth/password-reset/{token}", s.resetPassword)
}

// mfaChallengeResponse gestisce, nel login, l'esito "serve il secondo fattore": cookie
// della sfida e risposta {mfa_required, mfa_setup}. false se l'errore è un altro.
func (s *Server) mfaChallengeResponse(w http.ResponseWriter, r *http.Request, err error) bool {
	var mr *auth.MFARequiredError
	if !errors.As(err, &mr) {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: mfaCookie, Value: mr.Challenge, Path: mfaCookiePath, HttpOnly: true, Secure: !s.dev,
		SameSite: http.SameSiteStrictMode, MaxAge: 300})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"mfa_required": true, "mfa_setup": mr.Setup})
	return true
}

func (s *Server) challengeCookie(r *http.Request) string {
	c, err := r.Cookie(mfaCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) clearChallenge(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: mfaCookie, Value: "", Path: mfaCookiePath, MaxAge: -1, HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteStrictMode})
}

// mfaAuthError risponde agli errori del secondo fattore.
func (s *Server) mfaAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrMFAChallenge):
		s.clearChallenge(w)
		writeProblem(w, r, http.StatusUnauthorized, "auth.mfa_expired", err.Error())
	case errors.Is(err, auth.ErrMFACode):
		problemField(w, r, http.StatusUnprocessableEntity, "auth.mfa_invalid_code", err.Error(), "code")
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		writeProblem(w, r, http.StatusTooManyRequests, "auth.rate_limited", "troppi codici sbagliati: riprova tra qualche minuto")
	case errors.Is(err, auth.ErrMFAUnavailable):
		writeProblem(w, r, http.StatusConflict, "auth.mfa_unavailable", err.Error())
	case errors.Is(err, auth.ErrMFAPolicy):
		writeProblem(w, r, http.StatusConflict, "auth.mfa_required_by_policy", err.Error())
	case errors.Is(err, auth.ErrMFAAlready), errors.Is(err, auth.ErrMFANotEnabled), errors.Is(err, auth.ErrMFANotPending):
		writeProblem(w, r, http.StatusConflict, "auth.mfa_state", err.Error())
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.ttl.Seconds()),
	})
}

type codeInput struct {
	Code string `json:"code"`
}

func (s *Server) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var in codeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	token, err := s.auth.VerifyMFA(r.Context(), s.challengeCookie(r), in.Code, clientIP(r), r.UserAgent())
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	s.clearChallenge(w)
	s.setSessionCookie(w, token)
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := meView(p)
	s.decorateLanguage(r, p, &out)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) mfaChallengeSetup(w http.ResponseWriter, r *http.Request) {
	setup, err := s.auth.BeginMFAChallengeSetup(r.Context(), s.challengeCookie(r))
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, setup)
}

func (s *Server) mfaChallengeConfirm(w http.ResponseWriter, r *http.Request) {
	var in codeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	token, codes, err := s.auth.ConfirmMFAChallengeSetup(r.Context(), s.challengeCookie(r), in.Code, clientIP(r), r.UserAgent())
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	s.clearChallenge(w)
	s.setSessionCookie(w, token)
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out := meView(p)
	s.decorateLanguage(r, p, &out)
	writeJSON(w, http.StatusOK, map[string]any{"me": out, "recovery_codes": codes})
}

// ---------------------------------------------------------------------
// Profilo
// ---------------------------------------------------------------------

func (s *Server) mfaStatus(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	st, err := s.auth.MFAStatus(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) mfaBegin(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	setup, err := s.auth.BeginMFASetup(r.Context(), p)
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, setup)
}

func (s *Server) mfaEnable(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in codeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	codes, err := s.auth.EnableMFA(r.Context(), p, in.Code, clientIP(r))
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in codeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.auth.DisableMFA(r.Context(), p, in.Code, clientIP(r)); err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mfaRecoveryCodes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in codeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	codes, err := s.auth.RegenerateRecoveryCodes(r.Context(), p, in.Code, clientIP(r))
	if err != nil {
		s.mfaAuthError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ---------------------------------------------------------------------
// Link monouso (senza sessione)
// ---------------------------------------------------------------------

func (s *Server) invitationInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.adm.admin.Invitation(r.Context(), r.PathValue("token"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.AcceptInvitation(r.Context(), r.PathValue("token"), in.DisplayName, in.Password, clientIP(r)); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetInfo(w http.ResponseWriter, r *http.Request) {
	email, expires, err := s.adm.admin.ResetInfo(r.Context(), r.PathValue("token"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "expires_at": expires})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.ResetPassword(r.Context(), r.PathValue("token"), in.Password, clientIP(r)); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
