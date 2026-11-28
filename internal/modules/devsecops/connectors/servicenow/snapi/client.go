// Package snapi è un client minimo della Table API di ServiceNow (/api/now/table), scritto
// con la sola libreria standard. Legge soltanto: nessuna chiamata modifica ServiceNow.
//
//   - Autenticazione: utente di servizio in Basic (ogni release) oppure OAuth 2.0 client
//     credentials (/oauth_token.do, da Washington DC, con la proprietà di sistema
//     glide.oauth.inbound.client.credential.grant_type.enabled accesa). Il token OAuth si
//     tiene in memoria fino a poco prima della scadenza.
//   - Campi minimi (sysparm_fields), valori interni (sysparm_display_value=false: date in
//     UTC nel formato «2006-01-02 15:04:05»), riferimenti come sys_id semplici
//     (sysparm_exclude_reference_link=true), paginazione con sysparm_offset/sysparm_limit.
//   - 429 con Retry-After rispettato fino a MaxWait, 5xx ritentati due volte.
//   - Le credenziali non compaiono mai nei messaggi d'errore.
package snapi

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

// PageSize è il numero di record chiesti per pagina.
const PageSize = 200

// TimeLayout è il formato delle date della Table API con sysparm_display_value=false (UTC).
const TimeLayout = "2006-01-02 15:04:05"

// Auth è il modo di autenticarsi.
type Auth string

const (
	AuthBasic Auth = "basic"
	AuthOAuth Auth = "oauth"
)

// Options configura un client.
type Options struct {
	BaseURL string // già normalizzato (NormalizeBaseURL), senza / finale
	Auth    Auth
	// Basic: utente e password dell'utente di servizio.
	Username, Password string
	// OAuth client credentials: id e segreto del client registrato in ServiceNow.
	ClientID, ClientSecret string
	HTTP                   *http.Client
	MaxWait                time.Duration
	Sleep                  func(context.Context, time.Duration) error
	Now                    func() time.Time
}

// Client parla con un'istanza di ServiceNow.
type Client struct {
	o        Options
	mu       sync.Mutex
	token    string
	tokenExp time.Time
	requests int
	rate     ghapi.RateLimit
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

// IsRateLimited dice se l'errore è un limite di velocità non atteso.
func IsRateLimited(err error) (time.Time, bool) {
	var e *Error
	if errors.As(err, &e) && e.RateLimited {
		return e.Reset, true
	}
	return time.Time{}, false
}

// StatusOf restituisce lo stato HTTP di un errore dell'API (0 se non lo è).
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// New crea un client.
func New(o Options) (*Client, error) {
	if o.HTTP == nil {
		return nil, errors.New("client HTTP mancante")
	}
	if _, err := url.Parse(o.BaseURL); err != nil || o.BaseURL == "" {
		return nil, errors.New("URL dell'istanza non valido")
	}
	switch o.Auth {
	case AuthBasic:
		if o.Username == "" || o.Password == "" {
			return nil, errors.New("mancano utente o password dell'utente di servizio")
		}
	case AuthOAuth:
		if o.ClientID == "" || o.ClientSecret == "" {
			return nil, errors.New("mancano id o segreto del client OAuth")
		}
	default:
		return nil, errors.New("autenticazione sconosciuta")
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
	return &Client{o: o}, nil
}

// Requests è il numero di richieste fatte dal client.
func (c *Client) Requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// Rate è quanto si è aspettato per i limiti di velocità.
func (c *Client) Rate() ghapi.RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rate
}

// NormalizeBaseURL valida l'indirizzo dell'istanza:
//
//	acme                                → https://acme.service-now.com
//	https://acme.service-now.com/       → https://acme.service-now.com
//	https://itsm.azienda.it/sn          → https://itsm.azienda.it/sn
//	http://127.0.0.1:8190 (sviluppo)    → invariato
func NormalizeBaseURL(raw string, p ghapi.Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("serve l'indirizzo dell'istanza ServiceNow (per esempio https://azienda.service-now.com)")
	}
	if len(raw) > 300 {
		return "", errors.New("URL troppo lungo")
	}
	if !strings.Contains(raw, "://") {
		if !strings.ContainsAny(raw, ".:/") {
			raw += ".service-now.com" // il solo nome dell'istanza
		}
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("URL non valido")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if p != ghapi.Any && p != ghapi.Private {
			return "", errors.New("serve https: le credenziali viaggerebbero in chiaro")
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
	switch p {
	case ghapi.Public:
		if ip, err := netip.ParseAddr(host); err == nil {
			if !p.Allowed(ip) {
				return "", fmt.Errorf("%w: indirizzo interno. Per un'istanza nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
			}
		} else if localName(host) {
			return "", fmt.Errorf("%w: nome di rete locale. Per un'istanza nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
		}
	case ghapi.Private:
		if ip, err := netip.ParseAddr(host); err == nil && !p.Allowed(ip) {
			return "", fmt.Errorf("%w: indirizzo link-local o riservato", ghapi.ErrBlocked)
		}
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api/now")
	return u.Scheme + "://" + u.Host + path, nil
}

func localName(host string) bool {
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa")
}

// RecordURL è il link a un record nell'interfaccia di ServiceNow.
func (c *Client) RecordURL(table, sysID string) string {
	return RecordURL(c.o.BaseURL, table, sysID)
}

// RecordURL è il link a un record nell'interfaccia di ServiceNow.
func RecordURL(base, table, sysID string) string {
	return strings.TrimRight(base, "/") + "/nav_to.do?uri=" + url.QueryEscape(table+".do?sys_id="+sysID)
}

// Query è una lettura della Table API.
type Query struct {
	Table  string
	Query  string   // sysparm_query (encoded query)
	Fields []string // sysparm_fields
	Limit  int      // record massimi in tutto (0 = 5000)
}

// Table legge i record di una tabella pagina per pagina; each può fermare la lettura.
func (c *Client) Table(ctx context.Context, q Query, each func(map[string]string) (stop bool, err error)) error {
	max := q.Limit
	if max <= 0 {
		max = 5000
	}
	read := 0
	for offset := 0; read < max; offset += PageSize {
		v := url.Values{}
		if q.Query != "" {
			v.Set("sysparm_query", q.Query)
		}
		if len(q.Fields) > 0 {
			v.Set("sysparm_fields", strings.Join(q.Fields, ","))
		}
		n := min(PageSize, max-read)
		v.Set("sysparm_limit", strconv.Itoa(n))
		v.Set("sysparm_offset", strconv.Itoa(offset))
		v.Set("sysparm_display_value", "false")
		v.Set("sysparm_exclude_reference_link", "true")
		v.Set("sysparm_no_count", "true")
		var out struct {
			Result []map[string]any `json:"result"`
		}
		if err := c.get(ctx, "/api/now/table/"+url.PathEscape(q.Table), v, &out); err != nil {
			return err
		}
		for _, rec := range out.Result {
			read++
			stop, err := each(flatten(rec))
			if err != nil || stop {
				return err
			}
		}
		if len(out.Result) < n {
			return nil
		}
	}
	return nil
}

// flatten porta ogni valore a stringa (i riferimenti, se arrivano come oggetto, al sys_id).
func flatten(rec map[string]any) map[string]string {
	out := make(map[string]string, len(rec))
	for k, v := range rec {
		switch x := v.(type) {
		case string:
			out[k] = x
		case map[string]any:
			if s, ok := x["value"].(string); ok {
				out[k] = s
			}
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(x)
		}
	}
	return out
}

// ParseTime legge una data della Table API (UTC); vuota o non valida = nil.
func ParseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.ParseInLocation(TimeLayout, s, time.UTC)
	if err != nil {
		return nil
	}
	return &t
}

// FormatTime scrive una data per una encoded query.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	full := strings.TrimRight(c.o.BaseURL, "/") + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "regia-connector")
		if err := c.authorize(ctx, req); err != nil {
			return err
		}
		c.mu.Lock()
		c.requests++
		c.mu.Unlock()
		resp, err := c.o.HTTP.Do(req)
		if err != nil {
			return networkError(err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			if dst == nil {
				return nil
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(dst); err != nil {
				return fmt.Errorf("risposta di ServiceNow non leggibile (%s): %v. Se è una pagina HTML, l'istanza potrebbe essere in ibernazione o dietro un accesso SSO", path, err)
			}
			return nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && c.o.Auth == AuthOAuth && attempt == 0 {
			c.mu.Lock()
			c.token = "" // token scaduto prima del previsto: se ne chiede uno nuovo
			c.mu.Unlock()
			continue
		}
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
					return err
				}
				continue
			}
			reset := c.o.Now().Add(wait)
			return &Error{Status: resp.StatusCode, Path: path, RateLimited: true, Reset: reset,
				Msg: fmt.Sprintf("limite di velocità di ServiceNow raggiunto (regola di rate limit dell'istanza): si riprende dopo le %s", reset.Local().Format("15:04"))}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
			if err := c.o.Sleep(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return err
			}
			continue
		}
		return c.apiError(resp.StatusCode, path, raw)
	}
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.o.Auth == AuthBasic {
		req.SetBasicAuth(c.o.Username, c.o.Password)
		return nil
	}
	tok, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// accessToken restituisce il token OAuth, chiedendone uno nuovo se manca o sta per scadere.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && c.o.Now().Before(c.tokenExp) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.o.ClientID}, "client_secret": {c.o.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.o.BaseURL, "/")+"/oauth_token.do", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "regia-connector")
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return "", networkError(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		detail := strings.TrimSpace(out.Error + " " + truncate(out.Description, 200))
		if detail != "" {
			detail = " (" + detail + ")"
		}
		msg := fmt.Sprintf("ServiceNow non ha concesso il token OAuth (%d)%s. Controlla id e segreto del client, che l'utente dell'applicazione OAuth sia impostato e che la proprietà glide.oauth.inbound.client.credential.grant_type.enabled sia true (client credentials, da Washington DC)", resp.StatusCode, detail)
		return "", &Error{Status: resp.StatusCode, Path: "/oauth_token.do", Msg: msg}
	}
	life := time.Duration(out.ExpiresIn) * time.Second
	if life <= 0 {
		life = 30 * time.Minute
	}
	c.mu.Lock()
	c.token, c.tokenExp = out.AccessToken, c.o.Now().Add(life-life/10)
	c.mu.Unlock()
	return out.AccessToken, nil
}

func (c *Client) apiError(status int, path string, body []byte) error {
	var g struct {
		Error struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &g)
	detail := strings.TrimSpace(g.Error.Message)
	if g.Error.Detail != "" && g.Error.Detail != detail {
		detail = strings.TrimSpace(detail + " — " + g.Error.Detail)
	}
	if detail != "" {
		detail = ": " + truncate(detail, 240)
	}
	table := strings.TrimPrefix(path, "/api/now/table/")
	var msg string
	switch status {
	case http.StatusUnauthorized:
		if c.o.Auth == AuthOAuth {
			msg = "ServiceNow ha rifiutato il token OAuth (401)" + detail
		} else {
			msg = "ServiceNow ha rifiutato utente o password (401): controlla le credenziali e che l'utente non sia bloccato o senza accesso ai web service" + detail
		}
	case http.StatusForbidden:
		msg = "ServiceNow ha negato la lettura di «" + table + "» (403): all'utente manca un ruolo con l'ACL di lettura su quella tabella" + detail
	case http.StatusNotFound:
		msg = "non trovato su ServiceNow (404): «" + table + "». Controlla il nome della tabella o della classe CMDB e l'indirizzo dell'istanza" + detail
	case http.StatusBadRequest:
		msg = "ServiceNow ha rifiutato la richiesta su «" + table + "» (400)" + detail
	default:
		msg = fmt.Sprintf("ServiceNow ha risposto %d su %s%s", status, path, detail)
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
		return fmt.Errorf("%w. Se l'istanza è nella rete interna, attiva «Rete interna» nell'integrazione", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	msg := err.Error() // l'URL senza intestazioni: le credenziali non compaiono
	switch {
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate"):
		return fmt.Errorf("certificato TLS non valido: aggiungi la CA interna nell'integrazione (%v)", err)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("nome host non risolto: controlla l'indirizzo, o attiva il proxy se la rete aziendale lo richiede (%v)", err)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("connessione rifiutata: il server non risponde su quella porta (%v)", err)
	case strings.Contains(msg, "timeout"):
		return fmt.Errorf("ServiceNow non ha risposto in tempo: se la rete aziendale esce solo dal proxy, attiva «Usa il proxy» (%v)", err)
	}
	return fmt.Errorf("errore di rete verso ServiceNow: %v", err)
}
