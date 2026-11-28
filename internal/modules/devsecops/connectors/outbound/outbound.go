// Package outbound aggiunge ai client HTTP dei connettori il proxy in uscita, configurato
// in modo esplicito dall'operatore (REGIA_CONNECTOR_PROXY) e mai dalle variabili
// d'ambiente standard (HTTP_PROXY, HTTPS_PROXY, NO_PROXY), che Regia continua a ignorare.
//
// Serve nelle reti aziendali dove l'unica strada verso un servizio SaaS (ServiceNow,
// GitHub.com) passa da un proxy. Il client di base resta quello di ghapi.NewHTTPClient
// (CA interna, timeout, redirect solo https), che questo pacchetto non modifica:
//
//   - senza proxy il client si usa così com'è, e la protezione SSRF resta nel dialer,
//     sull'indirizzo a cui ci si connette davvero;
//   - con il proxy la connessione TCP va al proxy, che l'operatore ha scelto: il dialer
//     non controlla più la destinazione. Il controllo si sposta prima della richiesta:
//     l'host di destinazione si risolve e ogni indirizzo deve essere ammesso dalla
//     politica (ghapi.Policy). Se il nome non si risolve localmente (capita: in molte
//     aziende solo il proxy risolve i nomi esterni) la richiesta parte lo stesso, perché
//     il nome è già stato controllato alla configurazione (niente nomi di rete locale con
//     la politica pubblica) e la destinazione finale la filtra il proxy.
//
// Le credenziali del proxy, se servono, stanno nell'URL (http://utente:password@proxy:3128)
// e Go le manda solo al proxy (Proxy-Authorization), mai al servizio remoto. Nei messaggi
// d'errore l'URL del proxy compare senza credenziali.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// ParseProxy valida l'URL del proxy dell'operatore. Vuoto = nessun proxy (nil, nil).
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" {
		return nil, errors.New("URL del proxy non valido (per esempio http://proxy.azienda.it:3128)")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("il proxy deve essere http:// o https://")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("l'URL del proxy non deve avere percorso, parametri né frammenti")
	}
	u.Path = ""
	return u, nil
}

// Redacted è l'URL del proxy senza la password, per messaggi e interfaccia.
func Redacted(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	if c.User != nil {
		c.User = url.User(c.User.Username())
	}
	return c.String()
}

// Resolver risolve i nomi (si sostituisce nei test).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Via restituisce un client che passa dal proxy: una copia del client di base con lo
// stesso TLS, gli stessi timeout e gli stessi redirect. proxy nil = il client di base.
func Via(base *http.Client, p ghapi.Policy, proxy *url.URL, res Resolver) (*http.Client, error) {
	if proxy == nil {
		return base, nil
	}
	tr, ok := base.Transport.(*http.Transport)
	if !ok {
		return nil, errors.New("client HTTP di base inatteso")
	}
	t := tr.Clone()
	t.Proxy = http.ProxyURL(proxy)
	// Il dialer di base controlla l'indirizzo a cui ci si connette, che ora è il proxy
	// (spesso in una rete privata): si usa un dialer semplice, verso il solo proxy.
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = d.DialContext
	if res == nil {
		res = net.DefaultResolver
	}
	c := *base
	c.Transport = &guard{next: t, policy: p, res: res, proxy: Redacted(proxy)}
	return &c, nil
}

// guard controlla la destinazione prima che la richiesta parta verso il proxy.
type guard struct {
	next   http.RoundTripper
	policy ghapi.Policy
	res    Resolver
	proxy  string
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req.Context(), req.URL.Hostname()); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	resp, err := g.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("tramite il proxy %s: %w", g.proxy, err)
	}
	if resp.StatusCode == http.StatusProxyAuthRequired {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("il proxy %s chiede l'autenticazione (407): controlla utente e password in REGIA_CONNECTOR_PROXY", g.proxy)
	}
	return resp, nil
}

func (g *guard) check(ctx context.Context, host string) error {
	if g.policy == ghapi.Any {
		return nil
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !g.policy.Allowed(ip) {
			return fmt.Errorf("%w (%s)", ghapi.ErrBlocked, host)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := g.res.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil // risolve il proxy: vedi il commento del pacchetto
	}
	for _, ip := range ips {
		if !g.policy.Allowed(ip.Unmap()) {
			return fmt.Errorf("%w (%s → %s)", ghapi.ErrBlocked, host, ip)
		}
	}
	return nil
}
