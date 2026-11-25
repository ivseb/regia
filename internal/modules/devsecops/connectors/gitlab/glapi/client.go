// Package glapi è un client minimo dell'API REST v4 di GitLab (gitlab.com e self-managed),
// scritto con la sola libreria standard: token di gruppo, di progetto o personale
// (Authorization: Bearer), paginazione che segue l'intestazione Link (vale sia per l'offset
// sia per il keyset, che dove c'è si preferisce), rispetto dei limiti di velocità
// (RateLimit-*, Retry-After), protezione SSRF e CA interna come il connettore GitHub
// (stessa politica e stesso client HTTP di ghapi). Legge soltanto: nessuna chiamata
// modifica GitLab.
package glapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// Options configura un client.
type Options struct {
	BaseURL string // già normalizzato (NormalizeBaseURL), con /api/v4
	Token   string
	HTTP    *http.Client
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s): oltre, la
	// chiamata fallisce con un *Error RateLimited e la sincronizzazione riprende dopo.
	MaxWait time.Duration
	// Reserve: sotto questo numero di richieste rimaste si aspetta il reset (default 10).
	Reserve int
	// Sleep e Now si sostituiscono nei test.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}

// Client parla con un'istanza di GitLab.
type Client struct {
	o        Options
	base     *url.URL
	mu       sync.Mutex
	rate     ghapi.RateLimit
	requests int
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

// New crea un client.
func New(o Options) (*Client, error) {
	if o.HTTP == nil {
		return nil, errors.New("client HTTP mancante")
	}
	u, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return nil, errors.New("URL base non valido")
	}
	if strings.TrimSpace(o.Token) == "" {
		return nil, errors.New("manca il token")
	}
	if o.MaxWait == 0 {
		o.MaxWait = time.Minute
	}
	if o.Reserve == 0 {
		o.Reserve = 10
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
	return &Client{o: o, base: u}, nil
}

// Rate restituisce l'ultimo stato dei limiti di velocità.
func (c *Client) Rate() ghapi.RateLimit {
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

// ---------------------------------------------------------------------
// Rete
// ---------------------------------------------------------------------

// NormalizeBaseURL valida l'URL dell'istanza e lo porta alla forma dell'API:
//
//	https://gitlab.com, https://gitlab.com/api/v4   → https://gitlab.com/api/v4
//	https://git.azienda.it, https://git.azienda.it/  → https://git.azienda.it/api/v4
//	https://azienda.it/gitlab                        → https://azienda.it/gitlab/api/v4
func NormalizeBaseURL(raw string, p ghapi.Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "https://gitlab.com/api/v4", nil
	}
	if len(raw) > 500 {
		return "", errors.New("URL troppo lungo")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("URL non valido")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if p != ghapi.Any {
			return "", errors.New("serve https: il token viaggerebbe in chiaro")
		}
	default:
		return "", errors.New("sono ammessi solo URL https")
	}
	if u.User != nil {
		return "", errors.New("niente credenziali nell'URL: si configurano a parte")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("l'URL non deve avere parametri né frammenti")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "gitlab.com" || host == "www.gitlab.com" {
		return "https://gitlab.com/api/v4", nil
	}
	switch p {
	case ghapi.Public:
		if ip, err := netip.ParseAddr(host); err == nil {
			if !p.Allowed(ip) {
				return "", fmt.Errorf("%w: indirizzo interno. Per un GitLab nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
			}
		} else if localName(host) {
			return "", fmt.Errorf("%w: nome di rete locale. Per un GitLab nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
		}
	case ghapi.Private:
		if ip, err := netip.ParseAddr(host); err == nil && !p.Allowed(ip) {
			return "", fmt.Errorf("%w: indirizzo link-local o riservato", ghapi.ErrBlocked)
		}
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/api/v4") {
		path += "/api/v4"
	}
	return u.Scheme + "://" + u.Host + path, nil
}

func localName(host string) bool {
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa")
}

// ---------------------------------------------------------------------
// Richieste
// ---------------------------------------------------------------------

func (c *Client) resolve(path string, q url.Values) string {
	// path è già codificato (segmenti con url.PathEscape).
	s := strings.TrimRight(c.base.String(), "/") + path
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

func (c *Client) sameOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == c.base.Scheme && strings.EqualFold(u.Host, c.base.Host)
}

// SameInstance dice se un URL sta sull'istanza configurata (solo lì va il token).
func (c *Client) SameInstance(raw string) bool { return c.sameOrigin(raw) }

// Get legge un percorso dell'API in dst e restituisce l'URL della pagina successiva.
func (c *Client) Get(ctx context.Context, path string, q url.Values, dst any) error {
	_, err := c.get(ctx, c.resolve(path, q), dst)
	return err
}

func (c *Client) get(ctx context.Context, full string, dst any) (next string, err error) {
	resp, err := c.send(ctx, c.o.HTTP, full, "application/json", true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(dst); err != nil {
			return "", fmt.Errorf("risposta di GitLab non leggibile (%s): %v", pathOf(full), err)
		}
	}
	return nextLink(resp.Header.Get("Link")), nil
}

// send invia una GET rispettando i limiti di velocità; restituisce solo risposte 2xx.
// withToken false: la richiesta va senza credenziali (link esterni all'istanza).
func (c *Client) send(ctx context.Context, hc *http.Client, full, accept string, withToken bool) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := c.respectReserve(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "regia-connector")
		if withToken {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.o.Token))
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
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := c.retryAfter(resp)
			if wait <= c.o.MaxWait && attempt < 3 {
				if err := c.wait(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			reset := c.o.Now().Add(wait)
			return nil, &Error{Status: resp.StatusCode, Path: pathOf(full), RateLimited: true, Reset: reset,
				Msg: fmt.Sprintf("limite di velocità di GitLab raggiunto: si riprende dopo le %s", reset.Local().Format("15:04"))}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
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
		return &Error{RateLimited: true, Reset: r.Reset,
			Msg: fmt.Sprintf("restano %d richieste su %d a GitLab: si riprende dopo le %s", r.Remaining, r.Limit, r.Reset.Local().Format("15:04"))}
	}
	if err := c.wait(ctx, wait); err != nil {
		return err
	}
	c.mu.Lock()
	c.rate.Remaining = c.rate.Limit
	c.mu.Unlock()
	return nil
}

// observe registra RateLimit-* (le istanze self-managed spesso non li mandano).
func (c *Client) observe(h http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lim, err1 := strconv.Atoi(h.Get("RateLimit-Limit"))
	rem, err2 := strconv.Atoi(h.Get("RateLimit-Remaining"))
	if err1 != nil || err2 != nil {
		return
	}
	c.rate.Limit, c.rate.Remaining = lim, rem
	c.rate.Used = lim - rem
	if s, err := strconv.ParseInt(h.Get("RateLimit-Reset"), 10, 64); err == nil {
		c.rate.Reset = time.Unix(s, 0).UTC()
	}
	c.rate.Observed = c.o.Now().UTC()
}

func (c *Client) retryAfter(resp *http.Response) time.Duration {
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if s, err := strconv.ParseInt(resp.Header.Get("RateLimit-Reset"), 10, 64); err == nil {
		if d := time.Unix(s, 0).Sub(c.o.Now()) + time.Second; d > 0 {
			return d
		}
	}
	return time.Minute
}

func apiError(status int, path string, body []byte) error {
	var gl struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &gl)
	detail := ""
	switch m := gl.Message.(type) {
	case string:
		detail = m
	case nil:
	default:
		b, _ := json.Marshal(m)
		detail = string(b)
	}
	if detail == "" {
		detail = gl.Error
	}
	if detail == "" {
		detail = gl.Desc
	}
	if detail != "" {
		detail = ": " + detail
	}
	var msg string
	switch status {
	case http.StatusUnauthorized:
		msg = "GitLab ha rifiutato il token (401): scaduto, revocato o sbagliato" + detail
	case http.StatusForbidden:
		msg = "GitLab ha negato l'accesso (403): al token mancano gli scope o i permessi per " + path + detail
	case http.StatusNotFound:
		msg = "non trovato su GitLab (404): " + path + ". Controlla il nome e che il token veda quella risorsa"
	default:
		msg = fmt.Sprintf("GitLab ha risposto %d su %s%s", status, path, detail)
	}
	return &Error{Status: status, Path: path, Msg: msg}
}

func networkError(err error) error {
	if errors.Is(err, ghapi.ErrBlocked) {
		return fmt.Errorf("%w. Se GitLab è nella rete interna, attiva «Rete interna» nell'integrazione", err)
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
		return fmt.Errorf("GitLab non ha risposto in tempo (%v)", err)
	}
	return fmt.Errorf("errore di rete verso GitLab: %v", err)
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

// paginate legge le pagine di un elenco seguendo Link rel="next" (offset o keyset), al
// massimo maxPages (0 = 50). each può fermare la lettura restituendo stop.
func (c *Client) paginate(ctx context.Context, path string, q url.Values, maxPages int, each func(json.RawMessage) (stop bool, err error)) error {
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
		next, err := c.get(ctx, full, &items)
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

// Download scarica un contenuto (al massimo max byte). Il token va solo se l'URL sta
// sull'istanza, e non segue i redirect verso altri host (storage degli artefatti).
func (c *Client) Download(ctx context.Context, full string, max int64) ([]byte, error) {
	withToken := c.sameOrigin(full)
	dl := *c.o.HTTP
	base := c.o.HTTP.CheckRedirect
	dl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		if base != nil {
			return base(req, via)
		}
		return nil
	}
	resp, err := c.send(ctx, &dl, full, "application/octet-stream", withToken)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("download interrotto: %v", err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file più grande del limite (%d MB)", max>>20)
	}
	return b, nil
}
