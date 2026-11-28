package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"regia/internal/platform/audit"
	"regia/internal/platform/config"
)

// Accesso unico con OpenID Connect: authorization code con PKCE (S256) e nonce.
// L'ID token è verificato da go-oidc: firma con le chiavi della JWKS dell'IdP,
// iss uguale all'issuer configurato, aud che contiene il client_id, exp nel futuro.
// Il nonce si confronta qui con quello salvato all'avvio del flusso.
//
// Collegamento all'account:
//   - Se (issuer, sub) è già collegato, vale quello, anche se l'email è cambiata.
//   - Altrimenti si cerca l'account con la stessa email, purché verificata dall'IdP,
//     e si salva il collegamento. Senza email_verified l'email non è considerata
//     verificata, a meno di REGIA_OIDC_TRUST_EMAIL=true.
//   - Se non c'è un account, lo si crea solo se almeno un'organizzazione accoglie questa
//     identità al primo accesso (OIDCHook.Eligible: creazione accesa e dominio ammesso o
//     gruppo dell'IdP mappato). Nessuna registrazione libera.
//
// Dopo il collegamento l'OIDCHook crea i membri nelle organizzazioni che lo accolgono e
// riallinea i gruppi governati dall'IdP dal claim dei gruppi (internal/platform/admin/idp.go).

var (
	ErrOIDCUnknownAccount   = errors.New("nessun account Regia corrisponde a questa identità")
	ErrOIDCEmailNotVerified = errors.New("l'identità non ha un'email verificata")
	ErrOIDCAlreadyLinked    = errors.New("l'account è già collegato a un'altra identità di questo provider")
	ErrOIDCInvalidToken     = errors.New("risposta del provider non valida")
	ErrOIDCUnavailable      = errors.New("provider di identità non raggiungibile")
)

// OIDC è il client verso l'IdP. La discovery avviene al primo uso e resta in memoria;
// se l'IdP non risponde si riprova alla richiesta successiva.
type OIDC struct {
	cfg    config.OIDC
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDC prepara il client; client nil = client HTTP con timeout di 10 secondi.
func NewOIDC(cfg config.OIDC, client *http.Client) *OIDC {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDC{cfg: cfg, client: client}
}

func (o *OIDC) Name() string { return o.cfg.Name }

func (o *OIDC) ctx(ctx context.Context) context.Context { return oidc.ClientContext(ctx, o.client) }

func (o *OIDC) discover(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.oauth, o.verifier, nil
	}
	p, err := oidc.NewProvider(o.ctx(ctx), o.cfg.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrOIDCUnavailable, err)
	}
	o.provider = p
	o.verifier = p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID})
	o.oauth = &oauth2.Config{
		ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret, RedirectURL: o.cfg.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: o.cfg.Scopes,
	}
	return o.oauth, o.verifier, nil
}

// AuthURL è l'indirizzo dell'IdP a cui mandare il browser.
func (o *OIDC) AuthURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	oc, _, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	return oc.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Identity sono i dati dell'ID token che servono al collegamento.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	// Claims sono tutti i claim dell'ID token: il nome del claim dei gruppi lo sceglie
	// ogni organizzazione.
	Claims map[string]any
}

// maxClaimGroups: oltre questo numero i valori del claim dei gruppi si ignorano.
const maxClaimGroups = 500

// Groups legge il claim dei gruppi con il nome indicato. present è falso se il claim manca
// o se l'IdP lo ha spostato altrove per le dimensioni (overage di Entra ID: _claim_names):
// in quel caso i gruppi non si toccano. Un claim presente e vuoto vale «nessun gruppo».
func (id *Identity) Groups(claim string) (groups []string, present bool) {
	if id == nil || id.Claims == nil || claim == "" {
		return nil, false
	}
	if names, ok := id.Claims["_claim_names"].(map[string]any); ok {
		if _, over := names[claim]; over {
			return nil, false
		}
	}
	v, ok := id.Claims[claim]
	if !ok || v == nil {
		return nil, false
	}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && len(s) <= 300 && len(groups) < maxClaimGroups {
			groups = append(groups, s)
		}
	}
	switch t := v.(type) {
	case string:
		add(t)
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range t {
			add(s)
		}
	default:
		return nil, false
	}
	return groups, true
}

// OIDCHook collega il login OIDC alle organizzazioni: lo implementa admin.Service.
type OIDCHook interface {
	// Eligible: almeno un'organizzazione crea l'account di questa identità al primo accesso.
	Eligible(ctx context.Context, id *Identity) (bool, error)
	// AfterLogin crea i membri nelle organizzazioni che accolgono l'identità e riallinea
	// i gruppi governati dall'IdP in quelle dove il membro è attivo.
	AfterLogin(ctx context.Context, accountID string, id *Identity, ip netip.Addr) error
}

// SetOIDCHook collega il gancio delle organizzazioni (nil = solo collegamento, come prima).
func (s *Service) SetOIDCHook(h OIDCHook) { s.oidcHook = h }

// Exchange scambia il codice (con il verifier PKCE), verifica l'ID token e il nonce.
func (o *OIDC) Exchange(ctx context.Context, code, verifier, nonce string) (*Identity, error) {
	oc, v, err := o.discover(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := oc.Exchange(o.ctx(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("%w: scambio del codice: %v", ErrOIDCInvalidToken, err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, fmt.Errorf("%w: manca l'id_token", ErrOIDCInvalidToken)
	}
	idt, err := v.Verify(o.ctx(ctx), raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCInvalidToken, err)
	}
	if nonce == "" || !constantEq(idt.Nonce, nonce) {
		return nil, fmt.Errorf("%w: nonce diverso", ErrOIDCInvalidToken)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCInvalidToken, err)
	}
	all := map[string]any{}
	if err := idt.Claims(&all); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCInvalidToken, err)
	}
	if idt.Subject == "" {
		return nil, fmt.Errorf("%w: sub vuoto", ErrOIDCInvalidToken)
	}
	verified := o.cfg.TrustEmail
	if claims.EmailVerified != nil {
		verified = *claims.EmailVerified
	}
	return &Identity{Issuer: idt.Issuer, Subject: idt.Subject, Email: strings.ToLower(strings.TrimSpace(claims.Email)), EmailVerified: verified,
		Name: strings.TrimSpace(claims.Name), Claims: all}, nil
}

// LoginOIDC collega l'identità a un account (esistente o, se un'organizzazione lo accoglie,
// nuovo) e crea la sessione, come il login con password (stesso cookie, audit auth.login
// con metodo "oidc").
func (s *Service) LoginOIDC(ctx context.Context, id *Identity, ip netip.Addr, userAgent string) (string, error) {
	accountID, err := s.linkOIDC(ctx, id, ip, false)
	if errors.Is(err, ErrOIDCUnknownAccount) && s.oidcHook != nil {
		var ok bool
		if ok, err = s.oidcHook.Eligible(ctx, id); err == nil {
			err = ErrOIDCUnknownAccount
			if ok {
				accountID, err = s.linkOIDC(ctx, id, ip, true)
			}
		}
	}
	if err != nil {
		if errors.Is(err, ErrOIDCUnknownAccount) || errors.Is(err, ErrOIDCEmailNotVerified) || errors.Is(err, ErrOIDCAlreadyLinked) {
			s.auditLoginFailure(ctx, accountID, id.Email, ip)
		}
		return "", err
	}
	if s.oidcHook != nil {
		if err := s.oidcHook.AfterLogin(ctx, accountID, id, ip); err != nil {
			return "", err
		}
	}
	// Niente MFA locale: con l'SSO il secondo fattore lo chiede l'IdP.
	return s.newSession(ctx, accountID, "oidc", false, ip, userAgent)
}

// linkOIDC trova l'account dell'identità e, al primo accesso, salva il collegamento.
// create: se nessun account ha quell'email lo crea (solo dopo OIDCHook.Eligible).
func (s *Service) linkOIDC(ctx context.Context, id *Identity, ip netip.Addr, create bool) (string, error) {
	var accountID string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "select account_id from account_identities where issuer = $1 and subject = $2",
			id.Issuer, id.Subject).Scan(&accountID)
		if err == nil {
			_, err = tx.Exec(ctx, "update account_identities set last_login_at = now() where issuer = $1 and subject = $2", id.Issuer, id.Subject)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Primo accesso con questa identità: serve un'email verificata di un account esistente.
		if id.Email == "" || !id.EmailVerified {
			return ErrOIDCEmailNotVerified
		}
		err = tx.QueryRow(ctx, "select id from accounts where lower(email) = $1", id.Email).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			if !create {
				return ErrOIDCUnknownAccount
			}
			name := id.Name
			if name == "" {
				name = id.Email[:max(strings.IndexByte(id.Email, '@'), 0)]
			}
			if r := []rune(name); len(r) > 120 {
				name = string(r[:120])
			}
			if err := tx.QueryRow(ctx, "insert into accounts (email, display_name) values ($1, $2) returning id", id.Email, name).Scan(&accountID); err != nil {
				return err
			}
			if err := audit.Append(ctx, tx, audit.Event{Action: "auth.account_provision", EntityType: "account", EntityID: accountID,
				ActorAccountID: accountID, IP: ip, Payload: map[string]any{"method": "oidc"}}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var other bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from account_identities where account_id = $1 and issuer = $2)",
			accountID, id.Issuer).Scan(&other); err != nil {
			return err
		}
		if other {
			return ErrOIDCAlreadyLinked
		}
		var identityID string
		if err := tx.QueryRow(ctx, `insert into account_identities (account_id, issuer, subject, last_login_at)
			values ($1, $2, $3, now()) returning id`, accountID, id.Issuer, id.Subject).Scan(&identityID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.identity_link", EntityType: "account", EntityID: accountID,
			ActorAccountID: accountID, IP: ip, Payload: map[string]any{"identity": identityID}})
	})
	return accountID, err
}

// LinkedIdentity è un'identità esterna collegata all'account (per il profilo).
type LinkedIdentity struct {
	Issuer      string     `json:"issuer"`
	LinkedAt    time.Time  `json:"linked_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

// AccountInfo riassume il metodo di accesso dell'account: password e identità collegate.
type AccountInfo struct {
	HasPassword bool             `json:"has_password"`
	Identities  []LinkedIdentity `json:"identities"`
	LastLoginAt *time.Time       `json:"last_login_at"`
}

func (s *Service) AccountInfo(ctx context.Context, accountID string) (*AccountInfo, error) {
	out := &AccountInfo{Identities: []LinkedIdentity{}}
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "select password_hash is not null, last_login_at from accounts where id = $1", accountID).
			Scan(&out.HasPassword, &out.LastLoginAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "select issuer, created_at, last_login_at from account_identities where account_id = $1 order by created_at", accountID)
		if err != nil {
			return err
		}
		out.Identities, err = pgx.CollectRows(rows, pgx.RowToStructByPos[LinkedIdentity])
		return err
	})
	return out, err
}

// NewPKCEVerifier genera il code_verifier PKCE (32 byte casuali).
func NewPKCEVerifier() string { return oauth2.GenerateVerifier() }

func constantEq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
