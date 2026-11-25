// Package auth gestisce login con password e con OIDC (oidc.go), sessioni, token API
// personali (tokens.go), secondo fattore TOTP (mfa.go) e principal della richiesta. SAML arriverà dopo.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
	"regia/internal/platform/secretbox"
)

var (
	ErrInvalidCredentials = errors.New("email o password non validi")
	ErrNoSession          = errors.New("sessione assente o scaduta")
	ErrNotMember          = errors.New("non sei membro di questo tenant")
	ErrRateLimited        = errors.New("troppi tentativi: riprova tra qualche minuto")
	ErrPasswordDisabled   = errors.New("l'accesso con password è disattivato: usa l'accesso unico (SSO)")
)

type Service struct {
	db      *db.DB
	ttl     time.Duration
	limiter *limiter
	// passwordOff: solo SSO (REGIA_OIDC_ONLY); la password resta per gli amministratori di piattaforma.
	passwordOff bool
	// box cifra i segreti TOTP con la chiave applicativa (SetSecretBox); mfaLimiter
	// limita i codici sbagliati per account (mfa.go).
	box        *secretbox.Box
	mfaLimiter *limiter
}

// DisablePasswordLogin attiva la modalità "solo SSO": il login con password è rifiutato,
// tranne per gli amministratori di piattaforma (accesso di emergenza se l'IdP non risponde).
func (s *Service) DisablePasswordLogin(off bool) { s.passwordOff = off }

// PasswordLoginDisabled dice se è attiva la modalità "solo SSO".
func (s *Service) PasswordLoginDisabled() bool { return s.passwordOff }

func NewService(d *db.DB, ttl time.Duration) *Service {
	// Senza SetSecretBox la chiave è casuale in memoria: i segreti MFA non sopravvivono al
	// riavvio. serve.go imposta sempre quella della cartella dati.
	return &Service{db: d, ttl: ttl, limiter: newLimiter(10, 15*time.Minute), box: secretbox.Random(),
		mfaLimiter: newLimiter(mfaMaxAttempts, 15*time.Minute)}
}

// Membership è un tenant a cui l'account può accedere.
type Membership struct {
	TenantID   string `json:"tenant_id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	MemberID   string `json:"member_id"`
	MemberType string `json:"member_type"`
}

// Principal è chi sta facendo la richiesta, con il tenant attivo già verificato.
type Principal struct {
	SessionID       string
	AccountID       string
	Email           string
	DisplayName     string
	IsPlatformAdmin bool
	Tenants         []Membership
	// Valorizzati solo se c'è un tenant attivo di cui l'account è membro.
	TenantID    string
	MemberID    string
	MemberType  string
	Permissions []string
	// AuthMethod è come si è autenticata la richiesta: "password", "oidc" (sessione) o "token".
	AuthMethod string
	// TokenID è valorizzato se la richiesta usa un token API (niente sessione né cookie).
	TokenID string
	// MFA: la sessione è passata dal secondo fattore TOTP.
	MFA bool
}

func (p *Principal) Can(perm string) bool { return p.TenantID != "" && rbac.Has(p.Permissions, perm) }

// Login verifica le credenziali e crea una sessione. Restituisce il token da mettere nel cookie.
func (s *Service) Login(ctx context.Context, email, password string, ip netip.Addr, userAgent string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	key := email + "|" + ip.String()
	if !s.limiter.allow(key) {
		return "", ErrRateLimited
	}
	var (
		accountID string
		hash      *string
		admin     bool
	)
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select id, password_hash, is_platform_admin from accounts where lower(email) = $1", email).Scan(&accountID, &hash, &admin)
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && hash == nil) {
		_, _ = VerifyPassword(dummyHash(), password) // stesso tempo di risposta
		s.auditLoginFailure(ctx, "", email, ip)
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", err
	}
	ok, err := VerifyPassword(*hash, password)
	if err != nil {
		return "", err
	}
	if !ok {
		s.auditLoginFailure(ctx, accountID, email, ip)
		return "", ErrInvalidCredentials
	}
	s.limiter.reset(key)
	// Solo SSO: si controlla dopo la password, così la risposta non rivela chi è amministratore.
	if s.passwordOff && !admin {
		s.auditLoginFailure(ctx, accountID, email, ip)
		return "", ErrPasswordDisabled
	}
	// Secondo fattore (mfa.go): con l'MFA attiva, o obbligatoria per la politica di un
	// tenant, la sessione nasce solo dopo il codice. Restituisce *MFARequiredError.
	if err := s.mfaGate(ctx, accountID, ip, userAgent); err != nil {
		return "", err
	}
	return s.newSession(ctx, accountID, "password", false, ip, userAgent)
}

// newSession crea la sessione di un account già autenticato (password o OIDC) e
// restituisce il token da mettere nel cookie.
func (s *Service) newSession(ctx context.Context, accountID, method string, mfa bool, ip netip.Addr, userAgent string) (string, error) {
	token, tokenHash, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		// Tenant attivo iniziale: l'ultimo usato, se l'account ne è ancora membro; altrimenti
		// il primo disponibile (in ordine di nome).
		var first *string
		if err := tx.QueryRow(ctx, `select m.tenant_id from account_memberships($1) m
			left join accounts a on a.id = $1 and a.last_tenant_id = m.tenant_id
			order by (a.id is null), m.name limit 1`, accountID).Scan(&first); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx,
			`insert into sessions (id, account_id, token_hash, active_tenant_id, ip, user_agent, expires_at, auth_method, mfa)
			 values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			db.NewID(), accountID, tokenHash, first, ipOrNil(ip), truncate(userAgent, 400), time.Now().Add(s.ttl), method, mfa); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update accounts set last_login_at = now() where id = $1", accountID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.login", EntityType: "account", EntityID: accountID, ActorAccountID: accountID, IP: ip,
			Payload: map[string]any{"method": method, "mfa": mfa}})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) auditLoginFailure(ctx context.Context, accountID, email string, ip netip.Addr) {
	_ = s.db.Global(ctx, func(tx pgx.Tx) error {
		// Si registra il dominio dell'email, non l'indirizzo: niente dati personali nell'audit.
		domain := ""
		if i := strings.LastIndexByte(email, '@'); i >= 0 {
			domain = email[i+1:]
		}
		return audit.Append(ctx, tx, audit.Event{
			Action: "auth.login_failed", EntityType: "account", EntityID: accountID,
			ActorAccountID: accountID, IP: ip, Payload: map[string]any{"email_domain": domain},
		})
	})
}

// Authenticate risolve il token del cookie in un Principal.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	h := sha256.Sum256([]byte(token))
	p := &Principal{}
	var active *string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`select s.id, a.id, a.email, a.display_name, a.is_platform_admin, s.active_tenant_id, s.auth_method, s.mfa
			 from sessions s join accounts a on a.id = s.account_id
			 where s.token_hash = $1 and s.expires_at > now()`, h[:]).
			Scan(&p.SessionID, &p.AccountID, &p.Email, &p.DisplayName, &p.IsPlatformAdmin, &active, &p.AuthMethod, &p.MFA)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSession
		}
		if err != nil {
			return err
		}
		p.Tenants, err = memberships(ctx, tx, p.AccountID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if active != nil {
		for _, m := range p.Tenants {
			if m.TenantID == *active {
				if err := s.loadTenant(ctx, p, m); err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return p, nil
}

// loadTenant carica i permessi del membro nel tenant: ruoli assegnati direttamente
// o tramite gruppo, a livello di tenant (i ruoli per spazio arrivano con gli spazi).
func (s *Service) loadTenant(ctx context.Context, p *Principal, m Membership) error {
	return s.db.InTenant(ctx, m.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select distinct unnest(r.permissions)
			from role_assignments ra
			join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			where ra.space_id is null
			  and (ra.member_id = $1
			       or ra.group_id in (select member_groups($1)))`, m.MemberID)
		if err != nil {
			return err
		}
		perms, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		p.TenantID, p.MemberID, p.MemberType, p.Permissions = m.TenantID, m.MemberID, m.MemberType, perms
		return nil
	})
}

// SwitchTenant cambia il tenant attivo della sessione, dopo aver verificato l'appartenenza.
func (s *Service) SwitchTenant(ctx context.Context, p *Principal, tenantID string) error {
	var target *Membership
	for i := range p.Tenants {
		if p.Tenants[i].TenantID == tenantID {
			target = &p.Tenants[i]
		}
	}
	if target == nil {
		return ErrNotMember
	}
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "update sessions set active_tenant_id = $1 where id = $2", tenantID, p.SessionID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "update accounts set last_tenant_id = $1 where id = $2", tenantID, p.AccountID)
		return err
	})
	if err != nil {
		return err
	}
	return s.loadTenant(ctx, p, *target)
}

func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.db.Global(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "delete from sessions where id = $1", p.SessionID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.logout", EntityType: "account", EntityID: p.AccountID, ActorAccountID: p.AccountID})
	})
}

// PurgeExpired cancella le sessioni scadute (job periodico).
func (s *Service) PurgeExpired(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from sessions where expires_at < now()")
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		// Secondi passi del login scaduti e link di reimpostazione vecchi (amministrazione).
		if _, err := tx.Exec(ctx, "delete from mfa_challenges where expires_at < now()"); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "delete from password_resets where expires_at < now() - interval '30 days'")
		return err
	})
	return n, err
}

func memberships(ctx context.Context, tx pgx.Tx, accountID string) ([]Membership, error) {
	rows, err := tx.Query(ctx, "select tenant_id, slug, name, member_id, member_type from account_memberships($1)", accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Membership])
}

// newToken genera 32 byte casuali; nel database si salva solo lo sha256.
func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(t))
	return t, h[:], nil
}

func ipOrNil(ip netip.Addr) any {
	if !ip.IsValid() {
		return nil
	}
	return ip.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
