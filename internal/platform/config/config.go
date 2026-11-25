// Package config legge la configurazione da variabili d'ambiente e flag.
// Non c'è differenza tra SaaS e on-premise: cambiano solo i valori.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TrustedProxies []netip.Prefix // reti dei proxy fidati (REGIA_TRUSTED_PROXIES)
	// HTTPAddr è l'indirizzo di ascolto del server, es. ":8080".
	HTTPAddr string
	// DataDir contiene il Postgres incluso, i file e le chiavi.
	DataDir string
	// DatabaseURL, se impostata, attiva la modalità "Postgres esterno":
	// il binario non avvia né gestisce Postgres.
	DatabaseURL string
	// AppDatabaseURL è la connessione con il ruolo applicativo (senza BYPASSRLS).
	// In modalità esterna è obbligatoria; in modalità inclusa è derivata.
	AppDatabaseURL string
	// PGBinDir è la cartella con initdb e postgres del pacchetto.
	// Vuota = ricerca automatica (cartella del pacchetto, poi PATH).
	PGBinDir string
	// LogFormat: "text" o "json".
	LogFormat string
	// SourceURL è il repository del codice sorgente mostrato agli utenti (AGPL-3.0 §13,
	// docs/licenza.md). Chi distribuisce una versione modificata lo punta al proprio.
	SourceURL string
	// Dev abilita cookie non Secure e log più verbosi.
	Dev bool
	// SessionTTL è la durata di una sessione di login.
	SessionTTL time.Duration
	// Collab: allegati ed email delle notifiche (collab.go).
	Collab Collab
	// OIDC è l'accesso unico; disattivato se Issuer è vuoto.
	OIDC OIDC
}

// OIDC è la configurazione dell'accesso unico (authorization code con PKCE).
type OIDC struct {
	Issuer       string   // REGIA_OIDC_ISSUER
	ClientID     string   // REGIA_OIDC_CLIENT_ID
	ClientSecret string   // REGIA_OIDC_CLIENT_SECRET; vuoto = client pubblico (solo PKCE)
	RedirectURL  string   // REGIA_OIDC_REDIRECT_URL, es. https://host/api/v1/auth/oidc/callback
	Name         string   // REGIA_OIDC_NAME, etichetta del pulsante
	Scopes       []string // REGIA_OIDC_SCOPES, default "openid email profile"
	// Only disattiva il login con password, tranne per gli amministratori di piattaforma.
	Only bool // REGIA_OIDC_ONLY
	// TrustEmail considera verificata un'email senza il claim email_verified (default no).
	// Un email_verified esplicitamente falso non si accetta mai.
	TrustEmail bool // REGIA_OIDC_TRUST_EMAIL
}

// Enabled dice se l'accesso unico è configurato.
func (o OIDC) Enabled() bool { return o.Issuer != "" }

// Embedded indica se il binario gestisce il proprio Postgres.
func (c Config) Embedded() bool { return c.DatabaseURL == "" }

// Load legge le variabili d'ambiente e poi i flag del sottocomando (i flag vincono).
func Load(fs *flag.FlagSet, args []string) (Config, error) {
	c := Config{
		HTTPAddr:       env("REGIA_HTTP_ADDR", ":8080"),
		DataDir:        env("REGIA_DATA_DIR", "./data"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AppDatabaseURL: os.Getenv("REGIA_APP_DATABASE_URL"),
		PGBinDir:       os.Getenv("REGIA_PG_BIN"),
		LogFormat:      env("REGIA_LOG_FORMAT", "text"),
		SourceURL:      strings.TrimRight(env("REGIA_SOURCE_URL", "https://github.com/ivseb/regia"), "/"),
		Dev:            envBool("REGIA_DEV", false),
		SessionTTL:     envDuration("REGIA_SESSION_TTL", 12*time.Hour),
		OIDC: OIDC{
			Issuer:       strings.TrimSpace(os.Getenv("REGIA_OIDC_ISSUER")),
			ClientID:     strings.TrimSpace(os.Getenv("REGIA_OIDC_CLIENT_ID")),
			ClientSecret: os.Getenv("REGIA_OIDC_CLIENT_SECRET"),
			RedirectURL:  strings.TrimSpace(os.Getenv("REGIA_OIDC_REDIRECT_URL")),
			Name:         env("REGIA_OIDC_NAME", "SSO"),
			Scopes:       strings.Fields(env("REGIA_OIDC_SCOPES", "openid email profile")),
			Only:         envBool("REGIA_OIDC_ONLY", false),
			TrustEmail:   envBool("REGIA_OIDC_TRUST_EMAIL", false),
		},
	}
	trusted := env("REGIA_TRUSTED_PROXIES", "")
	fs.StringVar(&c.HTTPAddr, "http", c.HTTPAddr, "indirizzo di ascolto HTTP")
	fs.StringVar(&c.DataDir, "data", c.DataDir, "cartella dati")
	fs.StringVar(&c.PGBinDir, "pg-bin", c.PGBinDir, "cartella con i binari di Postgres (initdb, postgres)")
	fs.BoolVar(&c.Dev, "dev", c.Dev, "modalità sviluppo")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, fmt.Errorf("cartella dati: %w", err)
	}
	c.DataDir = abs
	if c.DatabaseURL != "" && c.AppDatabaseURL == "" {
		return c, errors.New("con DATABASE_URL serve anche REGIA_APP_DATABASE_URL (ruolo applicativo senza BYPASSRLS)")
	}
	// Proxy davanti all'app (es. Traefik e Cloudflare): solo da loro si accetta X-Forwarded-For.
	for _, p := range strings.FieldsFunc(trusted, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		pr, err := netip.ParsePrefix(p)
		if err != nil {
			a, aerr := netip.ParseAddr(p)
			if aerr != nil {
				return c, fmt.Errorf("REGIA_TRUSTED_PROXIES: %q non è un indirizzo o una rete", p)
			}
			pr = netip.PrefixFrom(a, a.BitLen())
		}
		c.TrustedProxies = append(c.TrustedProxies, pr.Masked())
	}
	if c.Collab, err = loadCollab(); err != nil {
		return c, err
	}
	if err := c.OIDC.validate(); err != nil {
		return c, err
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("REGIA_LOG_FORMAT non valido: %q", c.LogFormat)
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

func (o OIDC) validate() error {
	if !o.Enabled() {
		if o.Only {
			return errors.New("REGIA_OIDC_ONLY richiede REGIA_OIDC_ISSUER: senza SSO nessuno potrebbe accedere")
		}
		return nil
	}
	if o.ClientID == "" || o.RedirectURL == "" {
		return errors.New("con REGIA_OIDC_ISSUER servono anche REGIA_OIDC_CLIENT_ID e REGIA_OIDC_REDIRECT_URL")
	}
	for k, v := range map[string]string{"REGIA_OIDC_ISSUER": o.Issuer, "REGIA_OIDC_REDIRECT_URL": o.RedirectURL} {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%s non è un URL valido: %q", k, v)
		}
	}
	if !slices.Contains(o.Scopes, "openid") {
		return errors.New("REGIA_OIDC_SCOPES deve contenere openid")
	}
	return nil
}
