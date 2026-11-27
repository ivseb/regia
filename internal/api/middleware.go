package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyPrincipal
)

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(keyRequestID).(string)
	return id
}

func principal(ctx context.Context) *auth.Principal {
	p, _ := ctx.Value(keyPrincipal).(*auth.Principal)
	return p
}

// statusWriter ricorda lo stato della risposta per il log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// base applica, nell'ordine: request id, recupero dai panic, log, header di sicurezza,
// controllo dell'origine sulle richieste che modificano dati.
func (s *Server) base(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-Id")
		if rid == "" || len(rid) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			rid = hex.EncodeToString(b)
		}
		ctx := context.WithValue(r.Context(), keyRequestID, rid)
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w}
		sw.Header().Set("X-Request-Id", rid)
		securityHeaders(sw.Header(), s.dev)

		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic nella richiesta", "errore", v, "request_id", rid, "stack", string(debug.Stack()))
				if sw.status == 0 {
					writeProblem(sw, r, http.StatusInternalServerError, "internal", "errore interno")
				}
			}
			level := slog.LevelInfo
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				level = slog.LevelDebug
			}
			s.log.Log(ctx, level, "richiesta",
				"metodo", r.Method, "percorso", r.URL.Path, "stato", sw.status,
				"durata_ms", float64(time.Since(start).Microseconds())/1000, "byte", sw.bytes, "request_id", rid)
		}()

		if !sameOrigin(r) {
			writeProblem(sw, r, http.StatusForbidden, "request.cross_origin", "richiesta da un'origine diversa")
			return
		}
		next.ServeHTTP(sw, r)
	})
}

func securityHeaders(h http.Header, dev bool) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; "+
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
	if !dev {
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	}
}

// sameOrigin: le richieste che modificano dati devono arrivare dalla stessa origine.
// È una difesa CSRF aggiuntiva ai cookie SameSite=Lax e al Content-Type JSON obbligatorio.
func sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if bearer(r) {
		// Token API: la richiesta non usa i cookie (withPrincipal li ignora), quindi niente CSRF.
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Client non browser (CLI, integrazioni): niente cookie di terze parti in gioco.
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// withPrincipal risolve il cookie di sessione; non blocca le richieste anonime.
func (s *Server) withPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			s.withToken(w, r, next) // con un token API il cookie non si guarda
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" {
			p, err := s.auth.Authenticate(r.Context(), c.Value)
			if err == nil {
				r = r.WithContext(context.WithValue(r.Context(), keyPrincipal, p))
			} else if err != auth.ErrNoSession {
				s.log.Error("autenticazione", "errore", err, "request_id", requestID(r.Context()))
				writeProblem(w, r, http.StatusServiceUnavailable, "internal.unavailable", "servizio temporaneamente non disponibile")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearer dice se la richiesta porta un token API ("Authorization: Bearer myt_…").
func bearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// withToken autentica con un token API. Un token non valido è un 401 subito, senza
// ripiegare sul cookie; un token di sola lettura non può fare richieste che modificano.
func (s *Server) withToken(w http.ResponseWriter, r *http.Request, next http.Handler) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="regia"`)
		writeProblem(w, r, http.StatusUnauthorized, "auth.invalid_token", "serve Authorization: Bearer myt_…")
		return
	}
	p, err := s.auth.AuthenticateToken(r.Context(), strings.TrimSpace(raw))
	if errors.Is(err, auth.ErrTokenInvalid) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="regia", error="invalid_token"`)
		writeProblem(w, r, http.StatusUnauthorized, "auth.invalid_token", err.Error())
		return
	}
	if err != nil {
		s.log.Error("autenticazione con token", "errore", err, "request_id", requestID(r.Context()))
		writeProblem(w, r, http.StatusServiceUnavailable, "internal.unavailable", "servizio temporaneamente non disponibile")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		// Il server MCP usa POST anche per leggere: con un token di sola lettura espone solo
		// gli strumenti di lettura (internal/ai/mcpserver).
		if p.ReadOnly() && r.URL.Path != MCPPath {
			writeProblem(w, r, http.StatusForbidden, "auth.token_read_only", "token API di sola lettura")
			return
		}
	}
	ctx := audit.WithAPIToken(context.WithValue(r.Context(), keyPrincipal, p), p.TokenID)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// authed richiede una sessione valida.
func authed(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if p == nil {
			writeProblem(w, r, http.StatusUnauthorized, "auth.required", "accesso richiesto")
			return
		}
		h(w, r, p)
	}
}

// can richiede una sessione, un tenant attivo e un permesso.
func can(perm string, h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.TenantID == "" {
			writeProblem(w, r, http.StatusForbidden, "tenant.required", "nessun tenant attivo")
			return
		}
		if !p.Can(perm) {
			writeProblemParams(w, r, http.StatusForbidden, "auth.forbidden", "permesso mancante: "+perm, map[string]any{"perm": perm})
			return
		}
		h(w, r, p)
	})
}

// trustedProxies sono i proxy da cui si accetta X-Forwarded-For (impostati da New).
var trustedProxies []netip.Prefix

func isTrusted(ip netip.Addr) bool {
	for _, p := range trustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP è l'indirizzo del client: quello della connessione, oppure, se la connessione
// arriva da un proxy fidato, il primo indirizzo non fidato di X-Forwarded-For letto da
// destra (gli indirizzi a sinistra li scrive il client e non sono attendibili).
func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	ip = ip.Unmap()
	if len(trustedProxies) == 0 || !isTrusted(ip) {
		return ip
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		h, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return ip
		}
		h = h.Unmap()
		if !isTrusted(h) {
			return h
		}
		ip = h
	}
	return ip
}
