package webhooks

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Protezione SSRF: un webhook non deve diventare un modo per far parlare il server con
// la rete interna (database, metadati del cloud, pannelli di amministrazione).
//   - Alla configurazione: solo https (http solo in sviluppo o con l'opzione
//     dell'operatore), niente credenziali nell'URL, niente IP interni o nomi locali.
//   - Alla consegna: il controllo avviene sull'indirizzo a cui ci si connette davvero,
//     dopo la risoluzione DNS (Control del dialer): un nome che punta a 127.0.0.1 o
//     cambia indirizzo tra la configurazione e l'invio (DNS rebinding) è bloccato.
//   - Niente redirect seguiti, niente proxy dalle variabili d'ambiente, tempi limitati.
//
// REGIA_WEBHOOK_ALLOW_PRIVATE (o la modalità sviluppo) toglie i blocchi di rete.

// ErrBlockedAddress: la destinazione è un indirizzo interno.
var ErrBlockedAddress = errors.New("destinazione non consentita: indirizzo interno, di loopback o riservato")

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "questa rete"
		"100.64.0.0/10",   // CGNAT
		"192.0.0.0/24",    // assegnazioni IETF
		"192.0.2.0/24",    // documentazione
		"198.18.0.0/15",   // benchmark
		"198.51.100.0/24", // documentazione
		"203.0.113.0/24",  // documentazione
		"240.0.0.0/4",     // riservati
		"64:ff9b::/96",    // NAT64: può tradurre verso IPv4 interni
		"64:ff9b:1::/48",
		"2001:db8::/32", // documentazione
		"2002::/16",     // 6to4
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// BlockedIP dice se un indirizzo non è una destinazione pubblica.
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// checkURL valida l'URL di un webhook alla configurazione.
func checkURL(raw string, allowPrivate bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2000 {
		return "", invalid("url", "serve un URL (al massimo 2000 caratteri)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", invalid("url", "URL non valido")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate {
			return "", invalid("url", "serve https: i payload viaggiano firmati ma in chiaro con http")
		}
	default:
		return "", invalid("url", "sono ammessi solo URL https")
	}
	if u.User != nil {
		return "", invalid("url", "niente credenziali nell'URL: l'autenticità la garantisce la firma")
	}
	if u.Fragment != "" {
		return "", invalid("url", "l'URL non deve avere un frammento (#)")
	}
	if !allowPrivate {
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if ip, err := netip.ParseAddr(host); err == nil {
			if BlockedIP(ip) {
				return "", &Error{Status: 422, Code: "webhook.ssrf", Field: "url", Msg: ErrBlockedAddress.Error()}
			}
		} else if host == "localhost" || !strings.Contains(host, ".") ||
			strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
			strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") {
			return "", &Error{Status: 422, Code: "webhook.ssrf", Field: "url", Msg: "destinazione non consentita: nome di rete locale"}
		}
	}
	return u.String(), nil
}

// newClient crea il client HTTP delle consegne con il controllo degli indirizzi.
func newClient(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || BlockedIP(ip) {
				return fmt.Errorf("%w (%s)", ErrBlockedAddress, host)
			}
			return nil
		}
	}
	tr := &http.Transport{
		Proxy:                 nil, // mai i proxy dell'ambiente: la destinazione la decide la configurazione
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // un redirect è un errore di consegna, non si segue
		},
	}
}
