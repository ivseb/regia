package admin

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
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Link monouso a scadenza per gli inviti e per scegliere o reimpostare la password.
// Il token (32 byte casuali) si mostra una volta, all'amministratore che lo consegna
// (o parte per email se c'è un server SMTP); nel database c'è solo lo sha256.

var ErrLinkInvalid = &Error{Status: 404, Code: "link.invalid", Msg: "il link non è valido, è scaduto o è già stato usato"}

func newLinkToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(t))
	return t, h[:], nil
}

func hashLink(token string) ([]byte, bool) {
	if len(token) < 20 || len(token) > 100 {
		return nil, false
	}
	h := sha256.Sum256([]byte(token))
	return h[:], true
}

func (s *Service) newInvitation(ctx context.Context, tx pgx.Tx, a Actor, memberID string, newAccount bool) (*Link, error) {
	token, h, err := newLinkToken()
	if err != nil {
		return nil, err
	}
	l := &Link{Kind: "invite", Token: token, ExpiresAt: time.Now().Add(s.opts.InviteTTL).Truncate(time.Second)}
	if _, err := tx.Exec(ctx, `update invitations set revoked_at = now() where member_id = $1 and accepted_at is null and revoked_at is null`, memberID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `insert into invitations (tenant_id, id, member_id, token_hash, new_account, created_by, expires_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`, db.NewID(), memberID, h, newAccount, nullable(a.MemberID), l.ExpiresAt)
	return l, err
}

func (s *Service) newReset(ctx context.Context, tx pgx.Tx, a Actor, accountID string) (*Link, error) {
	token, h, err := newLinkToken()
	if err != nil {
		return nil, err
	}
	l := &Link{Kind: "password", Token: token, ExpiresAt: time.Now().Add(s.opts.ResetTTL).Truncate(time.Second)}
	// Un link nuovo annulla i precedenti dello stesso account.
	if _, err := tx.Exec(ctx, "update password_resets set used_at = now() where account_id = $1 and used_at is null", accountID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `insert into password_resets (id, account_id, token_hash, issued_tenant_id, issued_by, expires_at)
		values ($1, $2, $3, app_tenant(), $4, $5)`, db.NewID(), accountID, h, nullable(a.MemberID), l.ExpiresAt)
	return l, err
}

// ResendInvitation emette un nuovo link per un membro ancora invitato (il vecchio non vale più).
func (s *Service) ResendInvitation(ctx context.Context, a Actor, memberID string) (*Link, error) {
	if !ValidID(memberID) {
		return nil, ErrNotFound
	}
	var l *Link
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		var status string
		var newAccount bool
		if err := tx.QueryRow(ctx, `select m.status, coalesce(a.password_hash is null and not exists
			(select 1 from account_identities ai where ai.account_id = a.id), false)
			from members m left join accounts a on a.id = m.account_id where m.id = $1`, memberID).Scan(&status, &newAccount); err != nil {
			return notFound(err)
		}
		if status != "invited" {
			return conflict("member.not_invited", "il membro ha già accettato l'invito")
		}
		var err error
		if l, err = s.newInvitation(ctx, tx, a, memberID, newAccount); err != nil {
			return err
		}
		return a.audit(ctx, tx, "member.invitation_renew", "member", memberID, nil)
	})
	return l, err
}

// PasswordLink emette un link per scegliere una nuova password, solo per account che
// valgono soltanto in questo tenant e non in modalità solo SSO.
func (s *Service) PasswordLink(ctx context.Context, a Actor, memberID string) (*Link, error) {
	if !ValidID(memberID) {
		return nil, ErrNotFound
	}
	if s.opts.PasswordOff() {
		return nil, conflict("auth.password_disabled", "l'accesso con password è disattivato: si entra solo con l'SSO")
	}
	var l *Link
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		accountID, err := exclusiveAccount(ctx, tx, memberID)
		if err != nil {
			return err
		}
		if l, err = s.newReset(ctx, tx, a, accountID); err != nil {
			return err
		}
		return a.audit(ctx, tx, "member.password_link", "member", memberID, map[string]any{"account": accountID, "expires_at": l.ExpiresAt})
	})
	return l, err
}

// InviteInfo è ciò che la pagina pubblica dell'invito mostra prima di accettare.
type InviteInfo struct {
	Tenant        string    `json:"tenant"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email"`
	NeedsPassword bool      `json:"needs_password"`
	SSOOnly       bool      `json:"sso_only"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (s *Service) invitationTenant(ctx context.Context, token string) (string, []byte, error) {
	h, ok := hashLink(token)
	if !ok {
		return "", nil, ErrLinkInvalid
	}
	var tenantID *string
	if err := s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select invitation_tenant($1)::text", h).Scan(&tenantID)
	}); err != nil {
		return "", nil, err
	}
	if tenantID == nil {
		return "", nil, ErrLinkInvalid
	}
	return *tenantID, h, nil
}

// Invitation legge un invito valido.
func (s *Service) Invitation(ctx context.Context, token string) (*InviteInfo, error) {
	tenantID, h, err := s.invitationTenant(ctx, token)
	if err != nil {
		return nil, err
	}
	out := &InviteInfo{SSOOnly: s.opts.PasswordOff()}
	err = s.read(ctx, tenantID, func(tx pgx.Tx) error {
		var hasPassword bool
		err := tx.QueryRow(ctx, `select t.name, m.display_name, coalesce(nullif(m.email, ''), a.email), i.expires_at,
				i.new_account, a.password_hash is not null
			from invitations i
			join members m on m.tenant_id = i.tenant_id and m.id = i.member_id and m.status = 'invited'
			join accounts a on a.id = m.account_id
			join tenants t on t.id = i.tenant_id
			where i.token_hash = $1 and i.accepted_at is null and i.revoked_at is null and i.expires_at > now()`, h).
			Scan(&out.Tenant, &out.DisplayName, &out.Email, &out.ExpiresAt, &out.NeedsPassword, &hasPassword)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLinkInvalid
		}
		out.NeedsPassword = out.NeedsPassword && !hasPassword && !out.SSOOnly
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AcceptInvitation attiva l'appartenenza. Per un account nato con l'invito si scelgono
// anche nome e password (salvo modalità solo SSO). Non apre una sessione: poi si accede.
func (s *Service) AcceptInvitation(ctx context.Context, token, name, password string, ip netip.Addr) error {
	tenantID, h, err := s.invitationTenant(ctx, token)
	if err != nil {
		return err
	}
	return s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var invID, memberID, accountID string
		var newAccount, hasPassword bool
		err := tx.QueryRow(ctx, `select i.id, i.member_id, m.account_id, i.new_account, a.password_hash is not null
			from invitations i
			join members m on m.tenant_id = i.tenant_id and m.id = i.member_id and m.status = 'invited'
			join accounts a on a.id = m.account_id
			where i.token_hash = $1 and i.accepted_at is null and i.revoked_at is null and i.expires_at > now()
			for update of i`, h).Scan(&invID, &memberID, &accountID, &newAccount, &hasPassword)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLinkInvalid
		}
		if err != nil {
			return err
		}
		payload := map[string]any{"new_account": newAccount}
		if newAccount && !hasPassword {
			if n := cleanName(name, 120); n != "" {
				if _, err := tx.Exec(ctx, "update accounts set display_name = $2, updated_at = now() where id = $1", accountID, n); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "update members set display_name = $2 where id = $1", memberID, n); err != nil {
					return err
				}
			}
			if !s.opts.PasswordOff() {
				if err := auth.ValidatePassword(password); err != nil {
					return invalid("password", err.Error())
				}
				hash, err := auth.HashPassword(password)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "update accounts set password_hash = $2, updated_at = now() where id = $1", accountID, hash); err != nil {
					return err
				}
				payload["password_set"] = true
			}
		}
		if _, err := tx.Exec(ctx, "update members set status = 'active', updated_at = now() where id = $1", memberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update invitations set accepted_at = now() where id = $1", invID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "member.invitation_accept", EntityType: "member", EntityID: memberID,
			ActorAccountID: accountID, ActorMemberID: memberID, IP: ip, Payload: payload})
	})
}

// ResetInfo legge un link di reimpostazione valido (per la pagina pubblica).
func (s *Service) ResetInfo(ctx context.Context, token string) (email string, expires time.Time, err error) {
	h, ok := hashLink(token)
	if !ok {
		return "", time.Time{}, ErrLinkInvalid
	}
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `select a.email, r.expires_at from password_resets r join accounts a on a.id = r.account_id
			where r.token_hash = $1 and r.used_at is null and r.expires_at > now()`, h).Scan(&email, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLinkInvalid
		}
		return err
	})
	return email, expires, err
}

// ResetPassword imposta la nuova password e chiude tutte le sessioni dell'account.
// L'MFA, se attiva, resta: la password da sola non basta comunque ad entrare.
func (s *Service) ResetPassword(ctx context.Context, token, password string, ip netip.Addr) error {
	h, ok := hashLink(token)
	if !ok {
		return ErrLinkInvalid
	}
	if s.opts.PasswordOff() {
		return conflict("auth.password_disabled", "l'accesso con password è disattivato: si entra solo con l'SSO")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return invalid("password", err.Error())
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.Global(ctx, func(tx pgx.Tx) error {
		var id, accountID string
		err := tx.QueryRow(ctx, `update password_resets set used_at = now()
			where token_hash = $1 and used_at is null and expires_at > now() returning id, account_id`, h).Scan(&id, &accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLinkInvalid
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update accounts set password_hash = $2, updated_at = now() where id = $1", accountID, hash); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "delete from sessions where account_id = $1", accountID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from mfa_challenges where account_id = $1", accountID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.password_reset", EntityType: "account", EntityID: accountID,
			ActorAccountID: accountID, IP: ip, Payload: map[string]any{"link": id, "sessions_closed": tag.RowsAffected()}})
	})
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
