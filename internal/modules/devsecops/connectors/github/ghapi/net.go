package ghapi

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"regia/internal/engine/webhooks"
)

// Protezione SSRF per le chiamate a GitHub. È la stessa dei webhook in uscita
// (internal/engine/webhooks/ssrf.go): il controllo sta sull'indirizzo a cui ci si connette
// davvero (Control del dialer, dopo la risoluzione DNS), niente proxy dall'ambiente, tempi
// limitati. In più, per GitHub Enterprise Server che spesso sta in una rete interna, la
// politica si sceglie per integrazione:
//
//	Public   solo indirizzi pubblici (github.com e GHES esposti): il default
//	Private  anche reti private (RFC 1918, ULA, CGNAT, loopback), mai link-local
//	         (169.254.0.0/16, fe80::/10: i metadati del cloud) né indirizzi non validi.
//	         Serve il consenso dell'operatore (REGIA_CONNECTOR_ALLOW_PRIVATE)
//	Any      nessun controllo: solo modalità sviluppo e test
type Policy int

const (
	Public Policy = iota
	Private
	Any
)

func (p Policy) String() string {
	switch p {
	case Private:
		return "private"
	case Any:
		return "any"
	}
	return "public"
}

// ErrBlocked: la destinazione non è ammessa dalla politica di rete dell'integrazione.
var ErrBlocked = errors.New("destinazione non consentita dalla protezione SSRF")

// Allowed dice se un indirizzo è una destinazione ammessa dalla politica.
func (p Policy) Allowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch p {
	case Any:
		return true
	case Private:
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			netip.MustParsePrefix("0.0.0.0/8").Contains(ip) || netip.MustParsePrefix("240.0.0.0/4").Contains(ip) {
			return false
		}
		return true
	}
	return !webhooks.BlockedIP(ip)
}

// localName dice se un nome host è chiaramente di rete locale.
func localName(host string) bool {
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa")
}

// NormalizeBaseURL valida l'URL base dell'API e lo porta nella forma usata dal client:
//
//	https://github.com, https://api.github.com          → https://api.github.com
//	https://ghe.example.com, https://ghe.example.com/   → https://ghe.example.com/api/v3
//	https://ghe.example.com/api/v3                      → invariato
func NormalizeBaseURL(raw string, p Policy) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "https://api.github.com", nil
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
		if p != Any {
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
	if host == "github.com" || host == "www.github.com" || host == "api.github.com" {
		return "https://api.github.com", nil
	}
	if p == Public {
		if ip, err := netip.ParseAddr(host); err == nil {
			if !p.Allowed(ip) {
				return "", fmt.Errorf("%w: indirizzo interno. Per un GitHub Enterprise Server nella rete interna attiva «Rete interna»", ErrBlocked)
			}
		} else if localName(host) {
			return "", fmt.Errorf("%w: nome di rete locale. Per un GitHub Enterprise Server nella rete interna attiva «Rete interna»", ErrBlocked)
		}
	}
	if p == Private {
		if ip, err := netip.ParseAddr(host); err == nil && !p.Allowed(ip) {
			return "", fmt.Errorf("%w: indirizzo link-local o riservato", ErrBlocked)
		}
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/api/v3"
	}
	return u.Scheme + "://" + u.Host + path, nil
}

// NewHTTPClient crea il client HTTP con la politica di rete e, se c'è, la CA aggiuntiva
// (PEM) per un GHES con certificato di una CA interna.
func NewHTTPClient(p Policy, caPEM string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if p != Any {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !p.Allowed(ip) {
				return fmt.Errorf("%w (%s)", ErrBlocked, host)
			}
			return nil
		}
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(caPEM) != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("certificato della CA non valido (serve un PEM)")
		}
		tlsCfg.RootCAs = pool
	}
	tr := &http.Transport{
		Proxy:                 nil, // mai i proxy dell'ambiente: la destinazione la decide la configurazione
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   2 * timeout, // gli allegati grandi hanno bisogno di più tempo della sola risposta
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Gli allegati delle Release rimandano a un URL firmato (spesso su un altro host):
			// si seguono pochi redirect, solo https, e ogni connessione passa dal controllo
			// del dialer. Go non inoltra Authorization a un dominio diverso.
			if len(via) >= 5 {
				return errors.New("troppi redirect")
			}
			if req.URL.Scheme != "https" && p != Any {
				return errors.New("redirect verso un URL non https")
			}
			return nil
		},
	}, nil
}
