package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"regia/internal/platform/auth"
)

// Accesso unico OIDC e token API personali (piano, passo 15). Le verifiche crittografiche
// stanno in internal/platform/auth; qui ci sono i cookie, i redirect e le risposte.

const (
	oidcCookie     = "regia_oidc"
	oidcCookiePath = "/api/v1/auth/oidc"
	oidcFlowTTL    = 10 * time.Minute
)

// ---------------------------------------------------------------------
// OIDC
// ---------------------------------------------------------------------

func (s *Server) oidcConfig(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enabled": s.oidc != nil, "name": "", "password_login": !s.auth.PasswordLoginDisabled()}
	if s.oidc != nil {
		out["name"] = s.oidc.Name()
	}
	writeJSON(w, http.StatusOK, out)
}

// oidcState è il contenuto del cookie temporaneo, firmato con HMAC: lega la risposta
// dell'IdP al browser che ha avviato il flusso.
type oidcState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"r"`
	Expires  int64  `json:"e"`
}

func (s *Server) sealState(st oidcState) (string, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.stateKey)
	m.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func (s *Server) openState(v string) (oidcState, bool) {
	var st oidcState
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return st, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return st, false
	}
	m := hmac.New(sha256.New, s.stateKey)
	m.Write([]byte(payload))
	if !hmac.Equal(got, m.Sum(nil)) {
		return st, false
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(b, &st) != nil || time.Now().Unix() > st.Expires || st.State == "" {
		return oidcState{}, false
	}
	return st, true
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeNext accetta solo percorsi interni dell'interfaccia: niente URL assoluti,
// "//host" o "/\host" (che i browser trattano come altri siti), niente API.
func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.ContainsAny(n, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(n)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil || strings.HasPrefix(u.Path, "/api/") {
		return "/"
	}
	return n
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, r, http.StatusNotFound, "auth.oidc_disabled", "accesso unico non configurato")
		return
	}
	st := oidcState{State: randomString(), Nonce: randomString(), Verifier: auth.NewPKCEVerifier(),
		Next: safeNext(r.URL.Query().Get("next")), Expires: time.Now().Add(oidcFlowTTL).Unix()}
	target, err := s.oidc.AuthURL(r.Context(), st.State, st.Nonce, st.Verifier)
	if err != nil {
		s.log.Warn("oidc: avvio non riuscito", "errore", err, "request_id", requestID(r.Context()))
		s.oidcFail(w, r, "unavailable")
		return
	}
	v, err := s.sealState(st)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: v, Path: oidcCookiePath, HttpOnly: true, Secure: !s.dev,
		SameSite: http.SameSiteLaxMode, MaxAge: int(oidcFlowTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, r, http.StatusNotFound, "auth.oidc_disabled", "accesso unico non configurato")
		return
	}
	// Il cookie del flusso vale una volta sola.
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: oidcCookiePath, MaxAge: -1, HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		s.oidcFail(w, r, "state")
		return
	}
	st, ok := s.openState(c.Value)
	if !ok || !hmac.Equal([]byte(q.Get("state")), []byte(st.State)) {
		s.oidcFail(w, r, "state")
		return
	}
	if e := q.Get("error"); e != "" {
		s.log.Info("oidc: accesso annullato dal provider", "errore", e, "request_id", requestID(r.Context()))
		s.oidcFail(w, r, "denied")
		return
	}
	id, err := s.oidc.Exchange(r.Context(), q.Get("code"), st.Verifier, st.Nonce)
	if err != nil {
		s.log.Warn("oidc: risposta rifiutata", "errore", err, "request_id", requestID(r.Context()))
		if errors.Is(err, auth.ErrOIDCUnavailable) {
			s.oidcFail(w, r, "unavailable")
		} else {
			s.oidcFail(w, r, "invalid")
		}
		return
	}
	token, err := s.auth.LoginOIDC(r.Context(), id, clientIP(r), r.UserAgent())
	switch {
	case errors.Is(err, auth.ErrOIDCUnknownAccount):
		s.oidcFail(w, r, "unknown_account")
		return
	case errors.Is(err, auth.ErrOIDCEmailNotVerified):
		s.oidcFail(w, r, "email_unverified")
		return
	case errors.Is(err, auth.ErrOIDCAlreadyLinked):
		s.oidcFail(w, r, "already_linked")
		return
	case err != nil:
		s.log.Error("oidc: login", "errore", err, "request_id", requestID(r.Context()))
		s.oidcFail(w, r, "failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.ttl.Seconds()),
	})
	http.Redirect(w, r, st.Next, http.StatusSeeOther)
}

// oidcFail torna alla pagina di accesso con un codice che l'interfaccia traduce in un messaggio.
func (s *Server) oidcFail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+code, http.StatusSeeOther)
}

// ---------------------------------------------------------------------
// Profilo e token API
// ---------------------------------------------------------------------

// session richiede una sessione con cookie: gestire i token o cambiare tenant con un
// token permetterebbe a un token ridotto di crearne uno con più permessi.
func session(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.TokenID != "" {
			writeProblem(w, r, http.StatusForbidden, "auth.session_required", "operazione non consentita con un token API")
			return
		}
		h(w, r, p)
	})
}

// sessionTenant richiede una sessione con cookie e un tenant attivo.
func sessionTenant(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return session(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.TenantID == "" {
			writeProblem(w, r, http.StatusForbidden, "tenant.required", "nessun tenant attivo")
			return
		}
		h(w, r, p)
	})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	info, err := s.auth.AccountInfo(r.Context(), p.AccountID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{
		"account_id": p.AccountID, "email": p.Email, "display_name": p.DisplayName,
		"is_platform_admin": p.IsPlatformAdmin, "method": p.AuthMethod,
		"has_password": info.HasPassword, "identities": info.Identities, "last_login_at": info.LastLoginAt,
		"oidc_name": "", "password_login": !s.auth.PasswordLoginDisabled(),
	}
	if s.oidc != nil {
		out["oidc_name"] = s.oidc.Name()
	}
	for _, m := range p.Tenants {
		if m.TenantID == p.TenantID {
			out["tenant"] = m
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.auth.ListTokens(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "scopes": auth.TokenScopes(p),
		"default_days": auth.DefaultTokenTTL, "max_days": auth.MaxTokenTTL})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Name          string   `json:"name"`
		ExpiresInDays int      `json:"expires_in_days"`
		Scopes        []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	secret, t, err := s.auth.CreateToken(r.Context(), p, in.Name, in.ExpiresInDays, in.Scopes, clientIP(r))
	var ie *auth.TokenInputError
	if errors.As(err, &ie) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"type": "about:blank", "title": "Unprocessable Entity",
			"status": http.StatusUnprocessableEntity, "detail": ie.Msg, "code": "validation", "field": ie.Field, "request_id": requestID(r.Context())})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": secret, "item": t})
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeProblem(w, r, http.StatusNotFound, "api_token.not_found", auth.ErrTokenNotFound.Error())
		return
	}
	err := s.auth.RevokeToken(r.Context(), p, id, clientIP(r))
	if errors.Is(err, auth.ErrTokenNotFound) {
		writeProblem(w, r, http.StatusNotFound, "api_token.not_found", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
