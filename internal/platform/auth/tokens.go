package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Token API personali: "Authorization: Bearer myt_…".
//   - Il token è casuale (32 byte) con prefisso myt_; nel database c'è solo lo sha256.
//     Si mostra una volta sola, alla creazione.
//   - Vale nel tenant in cui è stato creato, per il membro che l'ha creato.
//   - Permessi: quelli attuali del membro intersecati con quelli del token (vuoti = tutti).
//     Un token con soli permessi di lettura è in sola lettura: il middleware rifiuta ogni
//     richiesta che modifica, anche quelle che dipendono da un'assegnazione e non da un
//     permesso (per esempio le decisioni di approvazione).
//   - Scadenza obbligatoria: 90 giorni di default, al massimo un anno.

const (
	TokenPrefix     = "myt_"
	DefaultTokenTTL = 90
	MaxTokenTTL     = 365
	maxTokens       = 50 // per membro
)

var (
	ErrTokenInvalid  = errors.New("token API non valido, scaduto o revocato")
	ErrTokenNotFound = errors.New("token inesistente")
)

// TokenInputError è un dato non valido nella creazione di un token.
type TokenInputError struct{ Field, Msg string }

func (e *TokenInputError) Error() string { return e.Msg }

// APIToken è un token come appare nell'elenco (mai il segreto).
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TokenScopes sono i permessi che il membro può dare a un suo token.
func TokenScopes(p *Principal) []string {
	if rbac.Has(p.Permissions, rbac.All) {
		return slices.Clone(rbac.Known)
	}
	out := slices.Clone(p.Permissions)
	slices.Sort(out)
	return out
}

// ReadOnly dice se la richiesta usa un token con soli permessi di lettura.
func (p *Principal) ReadOnly() bool {
	if p.TokenID == "" {
		return false
	}
	for _, perm := range p.Permissions {
		if !strings.HasSuffix(perm, ".read") {
			return false
		}
	}
	return true
}

// CreateToken crea un token per il membro nel tenant attivo e restituisce il segreto.
// Va chiamata solo da una sessione (non da un altro token).
func (s *Service) CreateToken(ctx context.Context, p *Principal, name string, days int, scopes []string, ip netip.Addr) (string, APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", APIToken{}, &TokenInputError{"name", "serve un nome (al massimo 80 caratteri)"}
	}
	if days == 0 {
		days = DefaultTokenTTL
	}
	if days < 1 || days > MaxTokenTTL {
		return "", APIToken{}, &TokenInputError{"expires_in_days", "la scadenza va da 1 a 365 giorni"}
	}
	clean := []string{}
	for _, sc := range scopes {
		sc = strings.TrimSpace(sc)
		if sc == rbac.All {
			// "*" equivale a nessuna riduzione: i permessi restano quelli del membro.
			clean = []string{}
			break
		}
		if !rbac.Has(p.Permissions, sc) {
			return "", APIToken{}, &TokenInputError{"scopes", "permesso non concesso al tuo profilo: " + sc}
		}
		if !slices.Contains(clean, sc) {
			clean = append(clean, sc)
		}
	}
	slices.Sort(clean)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", APIToken{}, err
	}
	secret := TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(secret))
	t := APIToken{ID: db.NewID(), Name: name, Hint: secret[:len(TokenPrefix)+6], Scopes: clean,
		ExpiresAt: time.Now().Add(time.Duration(days) * 24 * time.Hour).Truncate(time.Second), CreatedAt: time.Now()}
	err := s.db.InTenant(ctx, p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from api_tokens where member_id = $1", p.MemberID).Scan(&n); err != nil {
			return err
		}
		if n >= maxTokens {
			return &TokenInputError{"name", "hai già 50 token: revocane qualcuno"}
		}
		if _, err := tx.Exec(ctx, `insert into api_tokens (tenant_id, id, member_id, name, token_hash, scopes, expires_at, hint)
			values ($1, $2, $3, $4, $5, $6, $7, $8)`, p.TenantID, t.ID, p.MemberID, t.Name, h[:], t.Scopes, t.ExpiresAt, t.Hint); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "api_token.create", EntityType: "api_token", EntityID: t.ID,
			ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: ip,
			Payload: map[string]any{"scopes": t.Scopes, "expires_at": t.ExpiresAt}})
	})
	if err != nil {
		return "", APIToken{}, err
	}
	return secret, t, nil
}

// ListTokens elenca i token del membro nel tenant attivo, i più recenti per primi.
func (s *Service) ListTokens(ctx context.Context, p *Principal) ([]APIToken, error) {
	var out []APIToken
	err := s.db.InTenant(ctx, p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select id, name, hint, scopes, expires_at, last_used_at, created_at
			from api_tokens where member_id = $1 order by created_at desc`, p.MemberID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[APIToken])
		return err
	})
	if out == nil {
		out = []APIToken{}
	}
	return out, err
}

// RevokeToken cancella un token del membro: smette di valere subito.
func (s *Service) RevokeToken(ctx context.Context, p *Principal, id string, ip netip.Addr) error {
	return s.db.InTenant(ctx, p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from api_tokens where id = $1 and member_id = $2", id, p.MemberID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTokenNotFound
		}
		return audit.Append(ctx, tx, audit.Event{Action: "api_token.revoke", EntityType: "api_token", EntityID: id,
			ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: ip})
	})
}

// AuthenticateToken risolve un token API in un Principal legato al tenant del token.
func (s *Service) AuthenticateToken(ctx context.Context, raw string) (*Principal, error) {
	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) > 128 {
		return nil, ErrTokenInvalid
	}
	h := sha256.Sum256([]byte(raw))
	p := &Principal{AuthMethod: "token"}
	var (
		m      Membership
		scopes []string
	)
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "select tenant_id, token_id, member_id, account_id, member_type, slug, name, scopes from api_token_use($1)", h[:]).
			Scan(&m.TenantID, &p.TokenID, &m.MemberID, &p.AccountID, &m.MemberType, &m.Slug, &m.Name, &scopes)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, "select email, display_name from accounts where id = $1", p.AccountID).Scan(&p.Email, &p.DisplayName)
	})
	if err != nil {
		return nil, err
	}
	// Un token non porta mai i poteri di amministratore di piattaforma, e vede solo il suo tenant.
	p.Tenants = []Membership{m}
	if err := s.loadTenant(ctx, p, m); err != nil {
		return nil, err
	}
	if len(scopes) > 0 {
		eff := []string{}
		for _, sc := range scopes {
			if rbac.Has(p.Permissions, sc) {
				eff = append(eff, sc)
			}
		}
		p.Permissions = eff
	}
	return p, nil
}
