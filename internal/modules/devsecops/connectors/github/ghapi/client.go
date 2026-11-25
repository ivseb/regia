// Package ghapi è un client minimo dell'API REST di GitHub e GitHub Enterprise Server,
// scritto con la sola libreria standard: autenticazione come GitHub App (JWT RS256 e token
// di installazione con cache fino alla scadenza) o con un token, paginazione con
// l'intestazione Link, rispetto dei limiti di velocità (X-RateLimit-*, Retry-After) con
// attesa, protezione SSRF (net.go). Legge soltanto: nessuna chiamata modifica GitHub.
package ghapi

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIVersion è la versione dell'API REST richiesta a GitHub.
const APIVersion = "2022-11-28"

// Auth sono le credenziali: Mode "app" (consigliata) oppure "token".
type Auth struct {
	Mode           string
	Token          string
	AppID          string
	InstallationID string
	PrivateKeyPEM  string
}

// Options configura un client.
type Options struct {
	BaseURL string // già normalizzato (NormalizeBaseURL)
	Auth    Auth
	HTTP    *http.Client
	// Tokens è la cache dei token di installazione, condivisa tra i client.
	Tokens *TokenCache
	// CacheKey distingue le integrazioni nella cache (di solito il loro id).
	CacheKey string
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s): oltre, la
	// chiamata fallisce con un *Error RateLimited e la sincronizzazione riprende dopo.
	MaxWait time.Duration
	// Reserve: sotto questo numero di richieste rimaste si aspetta il reset prima di
	// continuare (default 25), per lasciare margine ad altri usi dello stesso token.
	Reserve int
	// Sleep e Now si sostituiscono nei test.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}

// RateLimit è l'ultimo stato dei limiti di velocità visto nelle risposte.
type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Used      int       `json:"used"`
	Reset     time.Time `json:"reset"`
	Resource  string    `json:"resource,omitempty"`
	Observed  time.Time `json:"observed"`
	Waited    float64   `json:"waited_seconds,omitempty"` // tempo passato ad aspettare i limiti
}

// Client parla con un'istanza di GitHub.
type Client struct {
	o        Options
	base     *url.URL
	key      *rsa.PrivateKey
	mu       sync.Mutex
	rate     RateLimit
	requests int
	server   string // versione di GHES, se l'istanza la dichiara
	scopes   string
}

// Error è un errore dell'API, con un messaggio in italiano da mostrare.
type Error struct {
	Status      int
	Path        string
	Msg         string
	RateLimited bool
	Reset       time.Time
}

func (e *Error) Error() string { return e.Msg }

// IsNotFound dice se l'errore è un 404.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsRateLimited dice se l'errore è un limite di velocità non atteso.
func IsRateLimited(err error) (time.Time, bool) {
	var e *Error
	if errors.As(err, &e) && e.RateLimited {
		return e.Reset, true
	}
	return time.Time{}, false
}

// New crea un client. Con la modalità app la chiave privata deve essere valida.
func New(o Options) (*Client, error) {
	if o.HTTP == nil {
		return nil, errors.New("client HTTP mancante")
	}
	u, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return nil, errors.New("URL base non valido")
	}
	if o.MaxWait == 0 {
		o.MaxWait = time.Minute
	}
	if o.Reserve == 0 {
		o.Reserve = 25
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tokens == nil {
		o.Tokens = NewTokenCache()
	}
	c := &Client{o: o, base: u}
	switch o.Auth.Mode {
	case "app":
		if o.Auth.AppID == "" || o.Auth.InstallationID == "" {
			return nil, errors.New("per la GitHub App servono App ID e installation ID")
		}
		k, err := ParsePrivateKey(o.Auth.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		c.key = k
	case "token":
		if strings.TrimSpace(o.Auth.Token) == "" {
			return nil, errors.New("manca il token")
		}
	default:
		return nil, errors.New("modalità di autenticazione sconosciuta")
	}
	return c, nil
}

// Rate restituisce l'ultimo stato dei limiti di velocità.
func (c *Client) Rate() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rate
}

// Requests è il numero di richieste fatte dal client.
func (c *Client) Requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// ServerVersion è la versione di GitHub Enterprise Server (vuota per github.com).
func (c *Client) ServerVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.server
}

// Scopes sono gli scope di un token classico (intestazione X-OAuth-Scopes), se presenti.
func (c *Client) Scopes() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scopes
}

// BaseURL è l'URL base dell'API.
func (c *Client) BaseURL() string { return c.base.String() }

// ---------------------------------------------------------------------
// Token di installazione
// ---------------------------------------------------------------------

// TokenCache conserva i token di installazione fino a poco prima della scadenza.
type TokenCache struct {
	mu     sync.Mutex
	m      map[string]cachedToken
	issued int
}

type cachedToken struct {
	token string
	exp   time.Time
}

func NewTokenCache() *TokenCache { return &TokenCache{m: map[string]cachedToken{}} }

// Issued è il numero di token chiesti a GitHub (per i test della cache).
func (t *TokenCache) Issued() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.issued
}

// Forget toglie dalla cache i token di un'integrazione (credenziali cambiate).
func (t *TokenCache) Forget(prefix string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.m {
		if strings.HasPrefix(k, prefix+"|") {
			delete(t.m, k)
		}
	}
}

func (c *Client) cacheKey() string {
	h := sha256.Sum256([]byte(c.base.String() + "\x00" + c.o.Auth.AppID + "\x00" + c.o.Auth.InstallationID + "\x00" + c.o.Auth.PrivateKeyPEM))
	return c.o.CacheKey + "|" + hex.EncodeToString(h[:8])
}

// installationToken restituisce un token valido almeno altri 5 minuti.
func (c *Client) installationToken(ctx context.Context, refresh bool) (string, error) {
	key := c.cacheKey()
	now := c.o.Now()
	tc := c.o.Tokens
	tc.mu.Lock()
	if t, ok := tc.m[key]; ok && !refresh && t.exp.After(now.Add(5*time.Minute)) {
		tc.mu.Unlock()
		return t.token, nil
	}
	tc.mu.Unlock()
	jwt, err := AppJWT(c.o.Auth.AppID, c.key, now)
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.request(ctx, http.MethodPost, "/app/installations/"+url.PathEscape(c.o.Auth.InstallationID)+"/access_tokens", nil,
		"Bearer "+jwt, &out); err != nil {
		return "", fmt.Errorf("token di installazione non ottenuto: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("GitHub non ha restituito il token di installazione")
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = now.Add(time.Hour)
	}
	tc.mu.Lock()
	tc.m[key] = cachedToken{token: out.Token, exp: out.ExpiresAt}
	tc.issued++
	tc.mu.Unlock()
	return out.Token, nil
}

func (c *Client) authorization(ctx context.Context, refresh bool) (string, error) {
	if c.o.Auth.Mode == "token" {
		return "Bearer " + strings.TrimSpace(c.o.Auth.Token), nil
	}
	t, err := c.installationToken(ctx, refresh)
	if err != nil {
		return "", err
	}
	return "Bearer " + t, nil
}

// AppAuthorization è l'intestazione per le chiamate come App (JWT), per la prova di connessione.
func (c *Client) appAuthorization() (string, error) {
	if c.key == nil {
		return "", errors.New("non è una GitHub App")
	}
	jwt, err := AppJWT(c.o.Auth.AppID, c.key, c.o.Now())
	if err != nil {
		return "", err
	}
	return "Bearer " + jwt, nil
}

// ---------------------------------------------------------------------
// Richieste
// ---------------------------------------------------------------------

// Get legge un percorso dell'API (es. /orgs/acme) in dst.
func (c *Client) Get(ctx context.Context, path string, q url.Values, dst any) error {
	_, err := c.get(ctx, c.resolve(path, q), dst)
	return err
}

func (c *Client) resolve(path string, q url.Values) string {
	// path è già codificato (segmenti con url.PathEscape).
	s := strings.TrimRight(c.base.String(), "/") + path
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

// sameOrigin: i link di paginazione devono restare sull'istanza configurata.
func (c *Client) sameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == c.base.Scheme && strings.EqualFold(u.Host, c.base.Host)
}

// get esegue una GET con il token e restituisce l'URL della pagina successiva.
func (c *Client) get(ctx context.Context, full string, dst any) (next string, err error) {
	auth, err := c.authorization(ctx, false)
	if err != nil {
		return "", err
	}
	resp, err := c.send(ctx, http.MethodGet, full, nil, auth, "application/vnd.github+json")
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized && c.o.Auth.Mode == "app" {
			// Token di installazione revocato o scaduto prima del previsto: uno nuovo, una volta.
			if auth, err = c.authorization(ctx, true); err != nil {
				return "", err
			}
			resp, err = c.send(ctx, http.MethodGet, full, nil, auth, "application/vnd.github+json")
		}
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(dst); err != nil {
			return "", fmt.Errorf("risposta di GitHub non leggibile (%s): %v", pathOf(full), err)
		}
	}
	return nextLink(resp.Header.Get("Link")), nil
}

// request è una chiamata con un'intestazione di autorizzazione data (JWT della App).
func (c *Client) request(ctx context.Context, method, path string, q url.Values, auth string, dst any) error {
	resp, err := c.send(ctx, method, c.resolve(path, q), nil, auth, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if dst != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst)
	}
	return nil
}

// send invia una richiesta rispettando i limiti di velocità; restituisce solo risposte 2xx.
func (c *Client) send(ctx context.Context, method, full string, _ io.Reader, auth, accept string) (*http.Response, error) {
	return c.sendWith(ctx, c.o.HTTP, method, full, auth, accept)
}

func (c *Client) sendWith(ctx context.Context, hc *http.Client, method, full string, auth, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := c.respectReserve(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, full, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", APIVersion)
		req.Header.Set("User-Agent", "regia-connector")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		c.mu.Lock()
		c.requests++
		c.mu.Unlock()
		resp, err := hc.Do(req)
		if err != nil {
			return nil, networkError(err)
		}
		c.observe(resp.Header)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		wait, limited := c.rateWait(resp, raw)
		if limited {
			if wait <= c.o.MaxWait && attempt < 3 {
				if err := c.wait(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			reset := c.o.Now().Add(wait)
			return nil, &Error{Status: resp.StatusCode, Path: pathOf(full), RateLimited: true, Reset: reset,
				Msg: fmt.Sprintf("limite di velocità di GitHub raggiunto: si riprende dopo le %s", reset.Local().Format("15:04"))}
		}
		if resp.StatusCode >= 500 && attempt < 2 && method == http.MethodGet {
			if err := c.wait(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		return nil, apiError(resp.StatusCode, pathOf(full), raw)
	}
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	c.mu.Lock()
	c.rate.Waited += d.Seconds()
	c.mu.Unlock()
	return c.o.Sleep(ctx, d)
}

// respectReserve aspetta il reset quando restano poche richieste.
func (c *Client) respectReserve(ctx context.Context) error {
	c.mu.Lock()
	r := c.rate
	c.mu.Unlock()
	if r.Limit == 0 || r.Remaining > c.o.Reserve {
		return nil
	}
	wait := r.Reset.Sub(c.o.Now()) + time.Second
	if wait <= 0 {
		return nil
	}
	if wait > c.o.MaxWait {
		return &Error{Status: 0, RateLimited: true, Reset: r.Reset,
			Msg: fmt.Sprintf("restano %d richieste su %d a GitHub: si riprende dopo le %s", r.Remaining, r.Limit, r.Reset.Local().Format("15:04"))}
	}
	if err := c.wait(ctx, wait); err != nil {
		return err
	}
	c.mu.Lock()
	c.rate.Remaining = c.rate.Limit // dopo il reset (la prossima risposta lo confermerà)
	c.mu.Unlock()
	return nil
}

// observe registra i limiti di velocità e le informazioni sull'istanza.
func (c *Client) observe(h http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := h.Get("X-GitHub-Enterprise-Version"); v != "" {
		c.server = v
	}
	if v, ok := h["X-Oauth-Scopes"]; ok {
		c.scopes = strings.Join(v, ",")
	}
	lim, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	rem, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if err1 != nil || err2 != nil {
		return
	}
	c.rate.Limit, c.rate.Remaining = lim, rem
	c.rate.Used, _ = strconv.Atoi(h.Get("X-RateLimit-Used"))
	if s, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		c.rate.Reset = time.Unix(s, 0).UTC()
	}
	c.rate.Resource = h.Get("X-RateLimit-Resource")
	c.rate.Observed = c.o.Now().UTC()
}

// rateWait riconosce i limiti di velocità primari (Remaining 0) e secondari (Retry-After,
// o il messaggio "secondary rate limit") e dice quanto aspettare.
func (c *Client) rateWait(resp *http.Response, body []byte) (time.Duration, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if s, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			d := time.Unix(s, 0).Sub(c.o.Now()) + time.Second
			if d < time.Second {
				d = time.Second
			}
			return d, true
		}
		return time.Minute, true
	}
	if resp.StatusCode == http.StatusTooManyRequests || strings.Contains(strings.ToLower(string(body)), "secondary rate limit") {
		return time.Minute, true // GitHub chiede almeno un minuto senza un'indicazione
	}
	return 0, false
}

func apiError(status int, path string, body []byte) error {
	var gh struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &gh)
	detail := ""
	if gh.Message != "" {
		detail = ": " + gh.Message
	}
	var msg string
	switch status {
	case http.StatusUnauthorized:
		msg = "GitHub ha rifiutato le credenziali (401): token scaduto o revocato, oppure App ID o chiave privata errati" + detail
	case http.StatusForbidden:
		msg = "GitHub ha negato l'accesso (403): mancano i permessi per " + path + detail
	case http.StatusNotFound:
		msg = "non trovato su GitHub (404): " + path + ". Controlla il nome e che la App o il token vedano quella risorsa"
	default:
		msg = fmt.Sprintf("GitHub ha risposto %d su %s%s", status, path, detail)
	}
	return &Error{Status: status, Path: path, Msg: msg}
}

func networkError(err error) error {
	if errors.Is(err, ErrBlocked) {
		return fmt.Errorf("%w. Se GitHub Enterprise Server è nella rete interna, attiva «Rete interna» nell'integrazione", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate"):
		return fmt.Errorf("certificato TLS non valido: aggiungi la CA interna nell'integrazione (%v)", err)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("nome host non risolto: controlla l'URL (%v)", err)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("connessione rifiutata: il server non risponde su quella porta (%v)", err)
	case strings.Contains(msg, "timeout"):
		return fmt.Errorf("GitHub non ha risposto in tempo (%v)", err)
	}
	return fmt.Errorf("errore di rete verso GitHub: %v", err)
}

func pathOf(full string) string {
	u, err := url.Parse(full)
	if err != nil {
		return full
	}
	return u.Path
}

// nextLink estrae rel="next" dall'intestazione Link.
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		seg := strings.Split(strings.TrimSpace(part), ";")
		if len(seg) < 2 {
			continue
		}
		u := strings.Trim(strings.TrimSpace(seg[0]), "<>")
		for _, p := range seg[1:] {
			if strings.TrimSpace(p) == `rel="next"` {
				return u
			}
		}
	}
	return ""
}

// paginate legge tutte le pagine di un elenco (al massimo maxPages; 0 = 50). key è il
// campo dell'oggetto che contiene l'elenco (es. "check_runs"); vuoto = l'elenco è la risposta.
// each riceve un elemento alla volta e può fermare la lettura restituendo stop.
func (c *Client) paginate(ctx context.Context, path string, q url.Values, key string, maxPages int, each func(json.RawMessage) (stop bool, err error)) error {
	if maxPages <= 0 {
		maxPages = 50
	}
	if q == nil {
		q = url.Values{}
	}
	if q.Get("per_page") == "" {
		q.Set("per_page", "100")
	}
	full := c.resolve(path, q)
	for page := 0; page < maxPages && full != ""; page++ {
		var items []json.RawMessage
		var next string
		var err error
		if key == "" {
			next, err = c.get(ctx, full, &items)
		} else {
			var obj map[string]json.RawMessage
			next, err = c.get(ctx, full, &obj)
			if err == nil && obj[key] != nil {
				err = json.Unmarshal(obj[key], &items)
			}
		}
		if err != nil {
			return err
		}
		for _, it := range items {
			stop, err := each(it)
			if err != nil || stop {
				return err
			}
		}
		if next != "" && !c.sameOrigin(next) {
			return errors.New("link di paginazione verso un altro host: ignorato")
		}
		full = next
	}
	return nil
}

// download scarica un contenuto binario (al massimo max byte).
func (c *Client) download(ctx context.Context, full string, max int64) ([]byte, error) {
	auth, err := c.authorization(ctx, false)
	if err != nil {
		return nil, err
	}
	// L'allegato risponde con un redirect a un URL già firmato: lì il token non va mai
	// (GitHub e gli storage rifiutano due autenticazioni, e il token non deve uscire),
	// anche quando l'URL è sullo stesso host, come su GitHub Enterprise Server.
	dl := *c.o.HTTP
	base := c.o.HTTP.CheckRedirect
	dl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if base != nil {
			return base(req, via)
		}
		return nil
	}
	resp, err := c.sendWith(ctx, &dl, http.MethodGet, full, auth, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("download interrotto: %v", err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("allegato più grande del limite (%d MB)", max>>20)
	}
	return b, nil
}
