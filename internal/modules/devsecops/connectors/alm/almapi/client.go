// Package almapi è un client minimo della REST API di OpenText ALM/Quality Center
// (/qcbin/rest), scritto con la sola libreria standard. Legge soltanto: le uniche chiamate che
// non sono GET aprono e chiudono la sessione.
//
// Autenticazione (documentazione pubblica OpenText, REST API core):
//   - utente di servizio: POST /authentication-point/alm-authenticate con utente e password in
//     XML (da ALM 12.00);
//   - API key (client id e segreto, creati in Site Administration → API Key Management):
//     prima alm-authenticate con il client id come utente e il segreto come password (da ALM
//     15.00), poi, se ALM lo rifiuta, POST /rest/oauth2/login in JSON (da ALM 12.60);
//   - poi POST /rest/site-session apre la sessione: i cookie (LWSSO_COOKIE_KEY o
//     OAUTH2_COOKIE_KEY, QCSession, ALM_USER, XSRF-TOKEN) restano nel client;
//   - da ALM 16.00 le chiamate che non sono GET vogliono l'intestazione X-XSRF-TOKEN con il
//     valore del cookie: si manda sempre, se c'è;
//   - Logout chiude la sessione (DELETE /rest/site-session e /authentication-point/logout):
//     una sessione REST occupa una licenza finché è aperta.
//
// Lettura: GET /rest/domains/{d}/projects/{p}/{collezione} in JSON, campi minimi (fields),
// filtro (query), ordinamento (order-by), paginazione (page-size, start-index). Le date di ALM
// sono nell'ora del server, senza fuso: chi chiama indica il fuso (Options.Location).
// 429 con Retry-After rispettato fino a MaxWait, 5xx ritentati due volte, 401 a sessione aperta
// = sessione scaduta: si rientra una volta. Le credenziali non compaiono mai negli errori.
package almapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// PageSize è il numero di entità chieste per pagina (ALM ne consente fino a 2000 di default).
const PageSize = 500

// TimeLayout è il formato delle date di ALM (ora del server).
const TimeLayout = "2006-01-02 15:04:05"

// Auth è il modo di autenticarsi.
type Auth string

const (
	AuthAPIKey Auth = "apikey"
	AuthUser   Auth = "user"
)

// Metodi di accesso riusciti (per la prova di connessione).
const (
	MethodALMAuthenticate = "alm-authenticate"
	MethodOAuth2Login     = "oauth2/login"
)

// Options configura un client.
type Options struct {
	BaseURL string // già normalizzato (NormalizeBaseURL): …/qcbin, senza / finale
	Auth    Auth
	// Utente di servizio.
	Username, Password string
	// API key.
	ClientID, ClientSecret string
	HTTP                   *http.Client
	// Location è il fuso orario del server ALM (date di last-modified ed esecuzione).
	Location *time.Location
	MaxWait  time.Duration
	Sleep    func(context.Context, time.Duration) error
	Now      func() time.Time
}

// Client parla con un server ALM. Non è pensato per l'uso concorrente da più goroutine
// (una sincronizzazione alla volta per integrazione), ma i contatori sono protetti.
type Client struct {
	o        Options
	http     *http.Client
	jar      *cookiejar.Jar
	base     *url.URL
	mu       sync.Mutex
	session  bool
	method   string
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

// New crea un client (senza collegarsi: lo fa la prima lettura, o Login).
func New(o Options) (*Client, error) {
	if o.HTTP == nil {
		return nil, errors.New("client HTTP mancante")
	}
	base, err := url.Parse(o.BaseURL)
	if err != nil || o.BaseURL == "" || base.Host == "" {
		return nil, errors.New("URL di ALM non valido")
	}
	switch o.Auth {
	case AuthUser:
		if o.Username == "" || o.Password == "" {
			return nil, errors.New("mancano utente o password dell'utente di servizio")
		}
	case AuthAPIKey:
		if o.ClientID == "" || o.ClientSecret == "" {
			return nil, errors.New("mancano client id o segreto dell'API key")
		}
	default:
		return nil, errors.New("autenticazione sconosciuta")
	}
	if o.Location == nil {
		o.Location = time.UTC
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
	jar, _ := cookiejar.New(nil)
	hc := *o.HTTP // copia: i cookie sono di questo client, non del trasporto condiviso
	hc.Jar = jar
	return &Client{o: o, http: &hc, jar: jar, base: base}, nil
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

// Method è il metodo di accesso riuscito ("" prima del login).
func (c *Client) Method() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method
}

// Location è il fuso del server ALM.
func (c *Client) Location() *time.Location { return c.o.Location }

// NormalizeBaseURL valida l'indirizzo di ALM e lo porta a …/qcbin:
//
//	https://alm.azienda.it                 → https://alm.azienda.it/qcbin
//	https://alm.azienda.it/qcbin/          → https://alm.azienda.it/qcbin
//	https://alm.azienda.it/qcbin/rest      → https://alm.azienda.it/qcbin
//	https://qc.azienda.it:8443/alm/qcbin   → invariato
//	http://127.0.0.1:8191/qcbin (sviluppo) → invariato
func NormalizeBaseURL(raw string, p ghapi.Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("serve l'indirizzo di ALM (per esempio https://alm.azienda.it/qcbin)")
	}
	if len(raw) > 300 {
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
				return "", fmt.Errorf("%w: indirizzo interno. Per un ALM nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
			}
		} else if localName(host) {
			return "", fmt.Errorf("%w: nome di rete locale. Per un ALM nella rete interna attiva «Rete interna»", ghapi.ErrBlocked)
		}
	case ghapi.Private:
		if ip, err := netip.ParseAddr(host); err == nil && !p.Allowed(ip) {
			return "", fmt.Errorf("%w: indirizzo link-local o riservato", ghapi.ErrBlocked)
		}
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suf := range []string{"/rest", "/authentication-point"} {
		path = strings.TrimSuffix(path, suf)
	}
	if !strings.HasSuffix(path, "/qcbin") {
		path += "/qcbin"
	}
	return u.Scheme + "://" + u.Host + path, nil
}

func localName(host string) bool {
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa")
}

// ---------------------------------------------------------------------
// Sessione
// ---------------------------------------------------------------------

// Login si autentica e apre la sessione REST. Si chiama da sé alla prima lettura.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	open := c.session
	c.mu.Unlock()
	if open {
		return nil
	}
	method, err := c.authenticate(ctx)
	if err != nil {
		return err
	}
	body := []byte("<session-parameters><client-type>REST client</client-type></session-parameters>")
	st, raw, err := c.send(ctx, http.MethodPost, "/rest/site-session", "application/xml", body)
	if err != nil {
		return err
	}
	if st != http.StatusOK && st != http.StatusCreated {
		msg := "ALM ha autenticato l'utente ma non ha aperto la sessione (" + strconv.Itoa(st) + ")" + detailOf(raw)
		if st == http.StatusForbidden || st == http.StatusUnauthorized {
			msg += ". Controlla che l'utente abbia una licenza disponibile e l'accesso ai progetti"
		}
		return &Error{Status: st, Path: "/rest/site-session", Msg: msg}
	}
	c.mu.Lock()
	c.session, c.method = true, method
	c.mu.Unlock()
	return nil
}

type almAuth struct {
	XMLName  xml.Name `xml:"alm-authentication"`
	User     string   `xml:"user"`
	Password string   `xml:"password"`
}

func (c *Client) authenticate(ctx context.Context) (string, error) {
	user, pass := c.o.Username, c.o.Password
	if c.o.Auth == AuthAPIKey {
		user, pass = c.o.ClientID, c.o.ClientSecret
	}
	body, _ := xml.Marshal(almAuth{User: user, Password: pass})
	st, raw, err := c.send(ctx, http.MethodPost, "/authentication-point/alm-authenticate", "application/xml", body)
	if err != nil {
		return "", err
	}
	if st == http.StatusOK || st == http.StatusCreated {
		return MethodALMAuthenticate, nil
	}
	if c.o.Auth == AuthAPIKey && (st == http.StatusUnauthorized || st == http.StatusNotFound || st == http.StatusBadRequest) {
		// Da 12.60 a 14.x l'API key entra solo da /rest/oauth2/login.
		js, _ := json.Marshal(map[string]string{"clientId": c.o.ClientID, "secret": c.o.ClientSecret})
		st2, raw2, err := c.send(ctx, http.MethodPost, "/rest/oauth2/login", "application/json", js)
		if err != nil {
			return "", err
		}
		if st2 == http.StatusOK || st2 == http.StatusCreated {
			return MethodOAuth2Login, nil
		}
		msg := "ALM ha rifiutato l'API key (" + strconv.Itoa(st2) + ")" + detailOf(raw2) +
			". Controlla client id e segreto, che la chiave sia attiva in Site Administration → API Key Management e che l'utente collegato alla chiave abbia accesso ai progetti (l'API key c'è da ALM 12.60)"
		return "", &Error{Status: http.StatusUnauthorized, Path: "/rest/oauth2/login", Msg: msg}
	}
	if st == http.StatusUnauthorized {
		return "", &Error{Status: st, Path: "/authentication-point/alm-authenticate",
			Msg: "ALM ha rifiutato utente o password (401): controlla le credenziali e che l'utente non sia disattivato" + detailOf(raw)}
	}
	if st == http.StatusNotFound {
		return "", &Error{Status: st, Path: "/authentication-point/alm-authenticate",
			Msg: "non trovato su ALM (404): controlla l'indirizzo, che di solito finisce con /qcbin (alm-authenticate c'è da ALM 12.00)"}
	}
	return "", &Error{Status: st, Path: "/authentication-point/alm-authenticate", Msg: fmt.Sprintf("ALM ha risposto %d all'autenticazione%s", st, detailOf(raw))}
}

// Logout chiude la sessione e libera la licenza. Gli errori si ignorano: la sessione scade da sé.
func (c *Client) Logout(ctx context.Context) {
	c.mu.Lock()
	open := c.session
	c.session = false
	c.mu.Unlock()
	if !open {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _, _ = c.send(ctx, http.MethodDelete, "/rest/site-session", "", nil)
	_, _, _ = c.send(ctx, http.MethodGet, "/authentication-point/logout", "", nil)
}

func (c *Client) xsrf() string {
	for _, k := range c.jar.Cookies(c.base) {
		if k.Name == "XSRF-TOKEN" {
			return k.Value
		}
	}
	return ""
}

// send fa una richiesta senza ritentare e restituisce stato e corpo (al massimo 64 KiB).
func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.o.BaseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "regia-connector")
	if method != http.MethodGet {
		if t := c.xsrf(); t != "" {
			req.Header.Set("X-XSRF-TOKEN", t)
		}
	}
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, networkError(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, raw, nil
}

// ---------------------------------------------------------------------
// Lettura
// ---------------------------------------------------------------------

// Domain è un dominio con i suoi progetti.
type Domain struct {
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
}

// Projects elenca i domini e i progetti visibili all'utente.
func (c *Client) Projects(ctx context.Context) ([]Domain, error) {
	var out struct {
		Domains struct {
			Domain []struct {
				Name     string `json:"Name"`
				Projects struct {
					Project []struct {
						Name string `json:"Name"`
					} `json:"Project"`
				} `json:"Projects"`
			} `json:"Domain"`
		} `json:"Domains"`
	}
	if err := c.get(ctx, "/rest/domains", url.Values{"include-projects-info": {"y"}}, &out); err != nil {
		return nil, err
	}
	list := []Domain{}
	for _, d := range out.Domains.Domain {
		dd := Domain{Name: d.Name, Projects: []string{}}
		for _, p := range d.Projects.Project {
			dd.Projects = append(dd.Projects, p.Name)
		}
		list = append(list, dd)
	}
	return list, nil
}

// Query è una lettura di una collezione di un progetto.
type Query struct {
	Domain, Project string
	Collection      string   // test-sets, test-instances, runs, defects, release-cycles, releases, test-set-folders, tests, defect-links
	Filter          string   // il contenuto delle graffe di query, per esempio last-modified[>="2026-01-01 00:00:00"]
	Fields          []string // campi minimi
	OrderBy         string   // per esempio last-modified[ASC];id[ASC]
	Limit           int      // entità massime in tutto (0 = 5000)
}

// Entity è un'entità di ALM: nome del campo → valore (stringa; vuota se il campo non ha valore).
type Entity map[string]string

type entitiesJSON struct {
	Entities []struct {
		Fields []struct {
			Name   string `json:"Name"`
			Values []struct {
				Value          any `json:"value"`
				ReferenceValue any `json:"ReferenceValue"`
			} `json:"values"`
		} `json:"Fields"`
	} `json:"entities"`
	TotalResults int `json:"TotalResults"`
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// Entities legge una collezione pagina per pagina; each può fermare la lettura.
func (c *Client) Entities(ctx context.Context, q Query, each func(Entity) (stop bool, err error)) error {
	max := q.Limit
	if max <= 0 {
		max = 5000
	}
	path := "/rest/domains/" + url.PathEscape(q.Domain) + "/projects/" + url.PathEscape(q.Project) + "/" + q.Collection
	read := 0
	for start := 1; read < max; start += PageSize {
		v := url.Values{}
		if q.Filter != "" {
			v.Set("query", "{"+q.Filter+"}")
		}
		if len(q.Fields) > 0 {
			v.Set("fields", strings.Join(q.Fields, ","))
		}
		if q.OrderBy != "" {
			v.Set("order-by", "{"+q.OrderBy+"}")
		}
		n := min(PageSize, max-read)
		v.Set("page-size", strconv.Itoa(n))
		v.Set("start-index", strconv.Itoa(start))
		var out entitiesJSON
		if err := c.get(ctx, path, v, &out); err != nil {
			return err
		}
		for _, e := range out.Entities {
			ent := make(Entity, len(e.Fields))
			for _, f := range e.Fields {
				val := ""
				if len(f.Values) > 0 {
					val = str(f.Values[0].Value)
					if val == "" {
						val = str(f.Values[0].ReferenceValue)
					}
				}
				ent[f.Name] = val
			}
			read++
			stop, err := each(ent)
			if err != nil || stop {
				return err
			}
		}
		if len(out.Entities) < n || (out.TotalResults > 0 && start-1+len(out.Entities) >= out.TotalResults) {
			return nil
		}
	}
	return nil
}

// ParseTime legge una data di ALM nell'ora del server; vuota o non valida = nil. Accetta anche
// la sola data (exec-date) e la data con l'ora separata (execution-date ed execution-time).
func (c *Client) ParseTime(s string) *time.Time { return ParseTime(s, c.o.Location) }

// ParseTime legge una data di ALM nel fuso indicato.
func ParseTime(s string, loc *time.Location) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{TimeLayout, time.DateOnly} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return &t
		}
	}
	return nil
}

// FormatTime scrive una data per un filtro, nell'ora del server.
func (c *Client) FormatTime(t time.Time) string { return t.In(c.o.Location).Format(TimeLayout) }

// Quote mette un valore tra virgolette per un filtro di ALM.
func Quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, ``) + `"` }

// IDs scrive un filtro «campo[1 or 2 or 3]» (solo id numerici).
func IDs(field string, ids []string) string {
	ok := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := strconv.Atoi(id); err == nil {
			ok = append(ok, id)
		}
	}
	return field + "[" + strings.Join(ok, " or ") + "]"
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	if err := c.Login(ctx); err != nil {
		return err
	}
	full := c.o.BaseURL + path
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
		c.mu.Lock()
		c.requests++
		c.mu.Unlock()
		resp, err := c.http.Do(req)
		if err != nil {
			return networkError(err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(dst); err != nil {
				return fmt.Errorf("risposta di ALM non leggibile (%s): %v. Se è una pagina HTML, davanti ad ALM c'è un accesso SSO o un proxy che risponde al posto suo", path, err)
			}
			return nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			// Sessione scaduta (REST_SESSION_MAX_IDLE_TIME): si rientra una volta.
			c.mu.Lock()
			c.session = false
			c.mu.Unlock()
			if err := c.Login(ctx); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("Retry-After") != "" {
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
				Msg: fmt.Sprintf("ALM chiede di rallentare (%d): si riprende dopo le %s", resp.StatusCode, reset.Local().Format("15:04"))}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
			if err := c.o.Sleep(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return err
			}
			continue
		}
		return apiError(resp.StatusCode, path, raw)
	}
}

// detailOf estrae il titolo di un errore di ALM (JSON o XML QCRestException).
func detailOf(body []byte) string {
	var j struct {
		ID    string `json:"Id"`
		Title string `json:"Title"`
	}
	if json.Unmarshal(body, &j) == nil && (j.Title != "" || j.ID != "") {
		return ": " + truncate(strings.TrimSpace(j.Title+" ("+j.ID+")"), 240)
	}
	var x struct {
		ID    string `xml:"Id"`
		Title string `xml:"Title"`
	}
	if xml.Unmarshal(body, &x) == nil && (x.Title != "" || x.ID != "") {
		return ": " + truncate(strings.TrimSpace(x.Title+" ("+x.ID+")"), 240)
	}
	return ""
}

func apiError(status int, path string, body []byte) error {
	detail := detailOf(body)
	what := path
	if i := strings.Index(path, "/projects/"); i >= 0 {
		what = path[i+len("/projects/"):]
	}
	var msg string
	switch status {
	case http.StatusUnauthorized:
		msg = "ALM ha chiuso la sessione e non ha accettato un nuovo accesso (401)" + detail
	case http.StatusForbidden:
		msg = "ALM ha negato la lettura di «" + what + "» (403): all'utente manca l'accesso al progetto o il permesso di lettura su quel modulo" + detail
	case http.StatusNotFound:
		msg = "non trovato su ALM (404): «" + what + "». Controlla dominio e progetto (rispettano maiuscole e minuscole)" + detail
	case http.StatusBadRequest:
		msg = "ALM ha rifiutato la richiesta su «" + what + "» (400): di solito un campo che non esiste in quel progetto, per esempio il campo del numero di change" + detail
	default:
		msg = fmt.Sprintf("ALM ha risposto %d su %s%s", status, what, detail)
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
		return fmt.Errorf("%w. Se ALM è nella rete interna, attiva «Rete interna» nell'integrazione", err)
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
		return fmt.Errorf("ALM non ha risposto in tempo: se la rete aziendale esce solo dal proxy, attiva «Usa il proxy» (%v)", err)
	}
	return fmt.Errorf("errore di rete verso ALM: %v", err)
}
