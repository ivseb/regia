// Package giteaapi è un client minimo dell'API REST v1 di Gitea (e Forgejo), scritto con la
// sola libreria standard: token di accesso (Authorization: token …), paginazione con
// page/limit, rispetto di 429 e Retry-After, protezione SSRF e CA interna come il
// connettore GitHub (stessa politica e stesso client HTTP di ghapi). Legge soltanto:
// nessuna chiamata modifica Gitea. Il token non compare mai nei messaggi d'errore.
package giteaapi

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

// PageSize è il numero di elementi chiesti per pagina (il massimo di Gitea è 50 di
// default sulle istanze standard).
const PageSize = 50

// Options configura un client.
type Options struct {
	BaseURL string // già normalizzato (NormalizeBaseURL), con /api/v1
	Token   string
	HTTP    *http.Client
	// MaxWait è l'attesa massima per un 429 (default 60 s): oltre, la chiamata fallisce con
	// un *Error RateLimited e la sincronizzazione riprende dopo.
	MaxWait time.Duration
	// Sleep e Now si sostituiscono nei test.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}

// Client parla con un'istanza di Gitea.
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

// Rate restituisce l'ultimo stato dei limiti di velocità (Gitea di norma non ne ha).
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

// NormalizeBaseURL valida l'URL dell'istanza e lo porta alla forma dell'API:
//
//	https://gitea.azienda.it, https://gitea.azienda.it/  → https://gitea.azienda.it/api/v1
//	https://azienda.it/gitea                              → https://azienda.it/gitea/api/v1
//	http://192.168.1.20:3000 (solo con «Rete interna»)    → http://192.168.1.20:3000/api/v1
func NormalizeBaseURL(raw string, p ghapi.Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("serve l'indirizzo di Gitea")
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
		// http si accetta solo per le reti interne (Gitea in casa o in azienda) e in sviluppo.
		if p != ghapi.Any && p != ghapi.Private {
			return "", errors.New("serve https: il token viaggerebbe in chiaro. Per un Gitea nella rete interna attiva «Rete interna»")
		}
	default:
		return "", errors.New("sono ammessi solo URL http o https")
	}
	if u.User != nil {
		return "", errors.New("niente credenziali nell'URL: si configurano a parte")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("l'URL non deve avere parametri né frammenti")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	switch p {
	case ghapi.Public:
		if ip, err := netip.ParseAddr(host); err == nil {
			if !p.Allowed(ip) {
				return "", fmt.Errorf("%w: indirizzo interno. Per un Gitea nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
			}
		} else if localName(host) {
			return "", fmt.Errorf("%w: nome di rete locale. Per un Gitea nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
		}
	case ghapi.Private:
		if ip, err := netip.ParseAddr(host); err == nil && !p.Allowed(ip) {
			return "", fmt.Errorf("%w: indirizzo link-local o riservato", ghapi.ErrBlocked)
		}
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/api/v1") {
		path += "/api/v1"
	}
	return u.Scheme + "://" + u.Host + path, nil
}

func localName(host string) bool {
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa")
}

func (c *Client) resolve(path string, q url.Values) string {
	s := strings.TrimRight(c.base.String(), "/") + path // path è già codificato
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

// SameInstance dice se un URL sta sull'istanza configurata (solo lì va il token).
func (c *Client) SameInstance(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == c.base.Scheme && strings.EqualFold(u.Host, c.base.Host)
}

// Get legge un percorso dell'API in dst.
func (c *Client) Get(ctx context.Context, path string, q url.Values, dst any) error {
	full := c.resolve(path, q)
	resp, err := c.send(ctx, c.o.HTTP, full, "application/json", true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(dst); err != nil {
			return fmt.Errorf("risposta di Gitea non leggibile (%s): %v", pathOf(full), err)
		}
	}
	return nil
}

func (c *Client) send(ctx context.Context, hc *http.Client, full, accept string, withToken bool) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "regia-connector")
		if withToken {
			req.Header.Set("Authorization", "token "+strings.TrimSpace(c.o.Token))
		}
		c.mu.Lock()
		c.requests++
		c.mu.Unlock()
		resp, err := hc.Do(req)
		if err != nil {
			return nil, networkError(err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := time.Minute
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			if wait <= c.o.MaxWait && attempt < 3 {
				c.mu.Lock()
				c.rate.Waited += wait.Seconds()
				c.mu.Unlock()
				if err := c.o.Sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			reset := c.o.Now().Add(wait)
			return nil, &Error{Status: resp.StatusCode, Path: pathOf(full), RateLimited: true, Reset: reset,
				Msg: fmt.Sprintf("limite di velocità di Gitea raggiunto: si riprende dopo le %s", reset.Local().Format("15:04"))}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
			if err := c.o.Sleep(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		return nil, apiError(resp.StatusCode, pathOf(full), raw)
	}
}

func apiError(status int, path string, body []byte) error {
	var g struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &g)
	detail := ""
	if g.Message != "" {
		detail = ": " + truncate(g.Message, 200)
	}
	var msg string
	switch status {
	case http.StatusUnauthorized:
		msg = "Gitea ha rifiutato il token (401): scaduto, revocato o sbagliato" + detail
	case http.StatusForbidden:
		msg = "Gitea ha negato l'accesso (403): al token mancano gli scope o i permessi per " + path + detail
	case http.StatusNotFound:
		msg = "non trovato su Gitea (404): " + path + ". Controlla il nome e che il token veda quella risorsa"
	default:
		msg = fmt.Sprintf("Gitea ha risposto %d su %s%s", status, path, detail)
	}
	return &Error{Status: status, Path: path, Msg: msg}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func networkError(err error) error {
	if errors.Is(err, ghapi.ErrBlocked) {
		return fmt.Errorf("%w. Se Gitea è nella rete interna, attiva «Rete interna» nell'integrazione", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	// Il testo di url.Error contiene l'URL ma non le intestazioni: il token non vi compare.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate"):
		return fmt.Errorf("certificato TLS non valido: aggiungi la CA interna nell'integrazione (%v)", err)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("nome host non risolto: controlla l'URL (%v)", err)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("connessione rifiutata: il server non risponde su quella porta (%v)", err)
	case strings.Contains(msg, "timeout"):
		return fmt.Errorf("Gitea non ha risposto in tempo (%v)", err)
	}
	return fmt.Errorf("errore di rete verso Gitea: %v", err)
}

func pathOf(full string) string {
	u, err := url.Parse(full)
	if err != nil {
		return full
	}
	return u.Path
}

// paginate legge le pagine di un elenco con page/limit, al massimo maxPages (0 = 100): si
// ferma alla prima pagina incompleta. each può fermare la lettura restituendo stop.
func (c *Client) paginate(ctx context.Context, path string, q url.Values, maxPages int, each func(json.RawMessage) (stop bool, err error)) error {
	if maxPages <= 0 {
		maxPages = 100
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", strconv.Itoa(PageSize))
	for page := 1; page <= maxPages; page++ {
		q.Set("page", strconv.Itoa(page))
		var items []json.RawMessage
		if err := c.Get(ctx, path, q, &items); err != nil {
			return err
		}
		for _, it := range items {
			stop, err := each(it)
			if err != nil || stop {
				return err
			}
		}
		if len(items) < PageSize {
			return nil
		}
	}
	return nil
}

// Download scarica un contenuto (al massimo max byte). Il token va solo se l'URL sta
// sull'istanza, e non segue i redirect verso altri host.
func (c *Client) Download(ctx context.Context, full string, max int64) ([]byte, error) {
	withToken := c.SameInstance(full)
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
