package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidID dice se s ha la forma di un identificativo (evita errori di sintassi in SQL).
func ValidID(s string) bool { return uuidRe.MatchString(s) }

func ids(v []string) ([]string, error) {
	out := []string{}
	for _, s := range v {
		if s == "" {
			continue
		}
		if !ValidID(s) {
			return nil, ErrNotFound
		}
		out = append(out, s)
	}
	return out, nil
}

// Ref è un riferimento con nome (gruppo, team).
type Ref struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	System bool   `json:"system,omitempty"`
}

// RoleGrant è un ruolo del membro: diretto (AssignmentID) o tramite un gruppo (ViaGroup).
type RoleGrant struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Key          *string `json:"key"`
	AssignmentID string  `json:"assignment_id"`
	ViaGroup     *string `json:"via_group"`
	Admin        bool    `json:"admin"`
}

// Member è una riga dell'elenco dei membri.
type Member struct {
	ID          string      `json:"id"`
	DisplayName string      `json:"display_name"`
	Email       string      `json:"email"`
	Type        string      `json:"member_type"`
	Status      string      `json:"status"`
	HasAccount  bool        `json:"has_account"`
	LastLoginAt *time.Time  `json:"last_login_at"`
	HasPassword bool        `json:"has_password"`
	SSO         bool        `json:"sso"`
	MFA         bool        `json:"mfa"`
	Admin       bool        `json:"admin"`
	Roles       []RoleGrant `json:"roles"`
	Groups      []Ref       `json:"groups"`
	Teams       []Ref       `json:"teams"`
	// Origin: chi ha creato il membro (admin | oidc | scim).
	Origin string `json:"origin"`
	// SuspendedSource: chi ha deciso la sospensione (admin | idp), solo se sospeso.
	SuspendedSource *string `json:"suspended_source"`
	// OnlyViewer: l'unico ruolo è «Lettore» assegnato direttamente (il vecchio default degli inviti).
	OnlyViewer bool `json:"only_viewer"`
	// InviteExpiresAt: invito ancora valido (solo per i membri "invited").
	InviteExpiresAt *time.Time `json:"invite_expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	// Solo nel dettaglio.
	Sessions int `json:"sessions"`
	Tokens   int `json:"tokens"`
	// OtherTenants: l'account vale anche altrove (password e MFA non si reimpostano da qui).
	OtherTenants bool `json:"other_tenants"`
}

const memberSelect = `
	select m.id, m.display_name, coalesce(nullif(m.email, ''), a.email, ''), m.member_type, m.status,
	       m.account_id is not null, a.last_login_at, coalesce(a.password_hash is not null, false),
	       exists (select 1 from account_identities ai where ai.account_id = m.account_id),
	       coalesce(a.mfa_enabled_at is not null and a.mfa_secret_enc is not null, false),
	       coalesce((select jsonb_agg(jsonb_build_object('id', r.id, 'name', r.name, 'key', r.key,
	                   'assignment_id', ra.id, 'via_group', g.name,
	                   'admin', r.permissions && array['*', 'tenant.admin']) order by r.name, g.name)
	                 from role_assignments ra
	                 join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
	                 left join groups g on g.tenant_id = ra.tenant_id and g.id = ra.group_id
	                 where ra.space_id is null
	                   and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id)))), '[]'),
	       coalesce((select jsonb_agg(jsonb_build_object('id', g.id, 'name', g.name, 'system', g.system_kind is not null) order by g.system_kind nulls first, lower(g.name))
	                 from group_members gm join groups g on g.tenant_id = gm.tenant_id and g.id = gm.group_id where gm.member_id = m.id), '[]'),
	       coalesce((select jsonb_agg(jsonb_build_object('id', t.id, 'name', t.name) order by lower(t.name))
	                 from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id where tm.member_id = m.id), '[]'),
	       (select max(i.expires_at) from invitations i where i.member_id = m.id and i.accepted_at is null and i.revoked_at is null and i.expires_at > now()),
	       m.created_at, m.origin, m.suspended_source
	from members m left join accounts a on a.id = m.account_id`

func scanMember(row pgx.Row) (Member, error) {
	var m Member
	var roles, groups, teams []byte
	if err := row.Scan(&m.ID, &m.DisplayName, &m.Email, &m.Type, &m.Status, &m.HasAccount, &m.LastLoginAt, &m.HasPassword,
		&m.SSO, &m.MFA, &roles, &groups, &teams, &m.InviteExpiresAt, &m.CreatedAt, &m.Origin, &m.SuspendedSource); err != nil {
		return m, err
	}
	_ = json.Unmarshal(roles, &m.Roles)
	_ = json.Unmarshal(groups, &m.Groups)
	_ = json.Unmarshal(teams, &m.Teams)
	for _, r := range m.Roles {
		if r.Admin {
			m.Admin = true
		}
	}
	m.OnlyViewer = len(m.Roles) == 1 && m.Roles[0].ViaGroup == nil && m.Roles[0].Key != nil && *m.Roles[0].Key == "viewer"
	return m, nil
}

// ListMembers elenca i membri (non anonimizzati), con ricerca su nome ed email.
func (s *Service) ListMembers(ctx context.Context, tenantID, q string) ([]Member, error) {
	out := []Member{}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(q)) + "%"
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, memberSelect+`
			where m.status <> 'anonymized'
			  and (m.display_name ilike $1 or coalesce(m.email, '') ilike $1 or coalesce(a.email, '') ilike $1)
			order by lower(m.display_name) limit 1000`, pattern)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMember(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// GetMember restituisce un membro con sessioni e token aperti.
func (s *Service) GetMember(ctx context.Context, tenantID, id string) (*Member, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	var m Member
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		m, err = scanMember(tx.QueryRow(ctx, memberSelect+` where m.id = $1 and m.status <> 'anonymized'`, id))
		if err != nil {
			return notFound(err)
		}
		return tx.QueryRow(ctx, `select
			(select count(*) from sessions s join members x on x.account_id = s.account_id
			 where x.id = $1 and s.expires_at > now() and (s.active_tenant_id = app_tenant() or s.active_tenant_id is null)),
			(select count(*) from api_tokens where member_id = $1 and expires_at > now()),
			coalesce((select account_other_memberships(account_id, app_tenant()) > 0 or
			                 (select is_platform_admin from accounts where id = members.account_id)
			          from members where id = $1), false)`, id).Scan(&m.Sessions, &m.Tokens, &m.OtherTenants)
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// NewMember è un invito o una creazione diretta.
type NewMember struct {
	Email    string   `json:"email"`
	Name     string   `json:"display_name"`
	Type     string   `json:"member_type"` // standard | requester
	Mode     string   `json:"mode"`        // invite | create
	RoleIDs  []string `json:"role_ids"`
	GroupIDs []string `json:"group_ids"`
}

// Link è un link monouso da consegnare alla persona (copiandolo, o per email).
type Link struct {
	Kind      string    `json:"kind"` // invite | password
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Created è l'esito di un invito o di una creazione.
type Created struct {
	MemberID   string `json:"member_id"`
	NewAccount bool   `json:"new_account"`
	Link       *Link  `json:"link,omitempty"`
}

// AddMember invita (stato "invited" fino all'accettazione) o crea direttamente (attivo
// subito) una persona. Un account con la stessa email si riusa: password e identità
// restano quelle che ha. Per un account nuovo creato direttamente il link serve a
// scegliere la password (salvo modalità solo SSO).
func (s *Service) AddMember(ctx context.Context, a Actor, in NewMember) (*Created, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(in.Email))
	if err != nil || addr.Address != strings.TrimSpace(in.Email) || len(addr.Address) > 254 {
		return nil, invalid("email", "indirizzo email non valido")
	}
	email := strings.ToLower(addr.Address)
	name := cleanName(in.Name, 120)
	if name == "" {
		name = email[:strings.IndexByte(email, '@')]
	}
	if in.Type == "" {
		in.Type = "standard"
	}
	if in.Type != "standard" && in.Type != "requester" {
		return nil, invalid("member_type", "tipo di membro non valido: membro o richiedente del portale")
	}
	if in.Mode != "invite" && in.Mode != "create" {
		return nil, invalid("mode", "scegli se invitare o creare direttamente")
	}
	roleIDs, err := ids(in.RoleIDs)
	if err != nil {
		return nil, invalid("role_ids", "ruolo inesistente")
	}
	groupIDs, err := ids(in.GroupIDs)
	if err != nil {
		return nil, invalid("group_ids", "gruppo inesistente")
	}
	out := &Created{MemberID: db.NewID()}
	err = s.tx(ctx, a, func(tx pgx.Tx) error {
		var accountID string
		err := tx.QueryRow(ctx, "select id from accounts where lower(email) = $1", email).Scan(&accountID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			accountID, out.NewAccount = db.NewID(), true
			if _, err := tx.Exec(ctx, "insert into accounts (id, email, display_name) values ($1, $2, $3)", accountID, email, name); err != nil {
				if isUnique(err) {
					// Un'altra richiesta, nello stesso istante, ha creato la stessa persona.
					return conflict("member.exists", "questa persona è già membro dell'organizzazione")
				}
				return err
			}
		case err != nil:
			return err
		default:
			var status string
			err := tx.QueryRow(ctx, "select status from members where account_id = $1", accountID).Scan(&status)
			if err == nil {
				if status == "invited" {
					return conflict("member.already_invited", "questa persona è già stata invitata: rinnova l'invito dal suo profilo")
				}
				return conflict("member.exists", "questa persona è già membro dell'organizzazione")
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		status := "active"
		if in.Mode == "invite" {
			status = "invited"
		}
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, account_id, display_name, email, member_type, status)
			values (app_tenant(), $1, $2, $3, $4, $5, $6)`, out.MemberID, accountID, name, email, in.Type, status); err != nil {
			if isUnique(err) {
				return conflict("member.exists", "questa persona è già membro dell'organizzazione")
			}
			return err
		}
		tag, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select app_tenant(), id, $1 from groups where system_kind = 'all_members' or id = any($2::uuid[])`, out.MemberID, groupIDs)
		if err != nil {
			return err
		}
		if int(tag.RowsAffected()) < len(groupIDs) {
			return invalid("group_ids", "gruppo inesistente")
		}
		if len(roleIDs) == 0 && in.Type == "requester" {
			// Senza scelta il richiedente del portale ha il suo ruolo. Un membro standard non
			// riceve nulla: vede solo ciò su cui ha un ruolo (esterni sicuri per default).
			if err := tx.QueryRow(ctx, "select coalesce(array_agg(id::text), '{}') from roles where key = 'requester'").Scan(&roleIDs); err != nil {
				return err
			}
		}
		perms, err := rolePerms(ctx, tx, roleIDs)
		if err != nil {
			return err
		}
		if err := a.canGrant(ctx, tx, perms); err != nil {
			return err
		}
		if err := a.canGrantGroups(ctx, tx, groupIDs); err != nil {
			return err
		}
		if err := checkNotIdPManaged(ctx, tx, groupIDs); err != nil {
			return err
		}
		for _, r := range roleIDs {
			if _, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, member_id) values (app_tenant(), $1, $2, $3)`,
				db.NewID(), r, out.MemberID); err != nil {
				if isFK(err) {
					return invalid("role_ids", "ruolo inesistente")
				}
				return err
			}
		}
		switch {
		case in.Mode == "invite":
			if out.Link, err = s.newInvitation(ctx, tx, a, out.MemberID, out.NewAccount); err != nil {
				return err
			}
		case out.NewAccount && !s.opts.PasswordOff():
			if out.Link, err = s.newReset(ctx, tx, a, accountID); err != nil {
				return err
			}
		}
		action := "member.create"
		if in.Mode == "invite" {
			action = "member.invite"
		}
		return a.audit(ctx, tx, action, "member", out.MemberID, map[string]any{
			"member_type": in.Type, "roles": roleIDs, "groups": groupIDs, "new_account": out.NewAccount, "account": accountID})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateMember cambia nome visualizzato e tipo. Passando a richiedente del portale i ruoli
// diretti diventano "Richiedente (portale)"; tornando membro pieno quel ruolo si toglie e
// non se ne aggiunge nessuno (niente "Lettore" per default). I ruoli dati dai gruppi restano.
func (s *Service) UpdateMember(ctx context.Context, a Actor, id string, name *string, typ *string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var cur, status string
		if err := tx.QueryRow(ctx, "select member_type, status from members where id = $1 and status <> 'anonymized' for update", id).Scan(&cur, &status); err != nil {
			return notFound(err)
		}
		payload := map[string]any{}
		if name != nil {
			n := cleanName(*name, 120)
			if n == "" {
				return invalid("display_name", "serve un nome")
			}
			if _, err := tx.Exec(ctx, "update members set display_name = $2, updated_at = now() where id = $1", id, n); err != nil {
				return err
			}
			payload["display_name"] = true
		}
		if typ != nil && *typ != cur {
			if *typ != "standard" && *typ != "requester" {
				return invalid("member_type", "tipo di membro non valido: membro o richiedente del portale")
			}
			if err := a.canActOn(ctx, tx, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "update members set member_type = $2, updated_at = now() where id = $1", id, *typ); err != nil {
				return err
			}
			from, to := "requester", ""
			if *typ == "requester" {
				// Si tolgono tutti i ruoli diretti: un richiedente usa solo il portale.
				if _, err := tx.Exec(ctx, "delete from role_assignments where member_id = $1 and space_id is null", id); err != nil {
					return err
				}
				from, to = "", "requester"
			} else if _, err := tx.Exec(ctx, `delete from role_assignments ra using roles r
				where ra.member_id = $1 and ra.space_id is null and r.tenant_id = ra.tenant_id and r.id = ra.role_id and r.key = $2`, id, from); err != nil {
				return err
			}
			if to != "" {
				if _, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, member_id)
					select app_tenant(), $1, r.id, $2 from roles r where r.key = $3
					  and not exists (select 1 from role_assignments x where x.member_id = $2 and x.role_id = r.id and x.space_id is null)`,
					db.NewID(), id, to); err != nil {
					return err
				}
			}
			payload["member_type"] = map[string]string{"from": cur, "to": *typ}
			if err := ensureAdmin(ctx, tx); err != nil {
				return err
			}
		}
		if len(payload) == 0 {
			return nil
		}
		return a.audit(ctx, tx, "member.update", "member", id, payload)
	})
}

// Suspend sospende un membro: non entra più in questo tenant, i suoi token API sono
// revocati e le sue sessioni su questo tenant chiuse.
func (s *Service) Suspend(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	if id == a.MemberID {
		return ErrSelf
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var accountID *string
		var status string
		if err := tx.QueryRow(ctx, "select account_id, status from members where id = $1 for update", id).Scan(&accountID, &status); err != nil {
			return notFound(err)
		}
		if status != "active" && status != "invited" {
			return conflict("member.not_active", "il membro non è attivo")
		}
		if err := a.canActOn(ctx, tx, id); err != nil {
			return err
		}
		payload, err := suspendMember(ctx, tx, id, accountID, "admin")
		if err != nil {
			return err
		}
		payload["from"] = status
		return a.audit(ctx, tx, "member.suspend", "member", id, payload)
	})
}

// suspendMember porta il membro nello stato sospeso (unico per interfaccia, OIDC e SCIM;
// source dice chi l'ha deciso), revoca token API e inviti e chiude le sessioni su questo
// tenant. Non lascia l'organizzazione senza amministratori.
func suspendMember(ctx context.Context, tx pgx.Tx, id string, accountID *string, source string) (map[string]any, error) {
	if _, err := tx.Exec(ctx, "update members set status = 'suspended', suspended_source = $2, updated_at = now() where id = $1", id, source); err != nil {
		return nil, err
	}
	if err := ensureAdmin(ctx, tx); err != nil {
		return nil, err
	}
	tokens, err := tx.Exec(ctx, "delete from api_tokens where member_id = $1", id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update invitations set revoked_at = now() where member_id = $1 and accepted_at is null and revoked_at is null", id); err != nil {
		return nil, err
	}
	var closed int64
	if accountID != nil {
		if closed, err = closeSessions(ctx, tx, *accountID, false); err != nil {
			return nil, err
		}
	}
	return map[string]any{"source": source, "tokens_revoked": tokens.RowsAffected(), "sessions_closed": closed}, nil
}

// Reactivate riattiva un membro sospeso.
func (s *Service) Reactivate(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var source *string
		if err := tx.QueryRow(ctx, "select suspended_source from members where id = $1 and status = 'suspended' for update", id).Scan(&source); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return conflict("member.not_suspended", "il membro non è sospeso")
			}
			return err
		}
		if err := a.canActOn(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update members set status = 'active', suspended_source = null, updated_at = now() where id = $1", id); err != nil {
			return err
		}
		return a.audit(ctx, tx, "member.reactivate", "member", id, map[string]any{"source": source})
	})
}

// ResetMFA toglie l'MFA a un account che appartiene solo a questo tenant e ne chiude le
// sessioni: al prossimo accesso la persona la riattiva (se la politica lo chiede, subito).
func (s *Service) ResetMFA(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		accountID, err := exclusiveAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := a.canActOn(ctx, tx, id); err != nil {
			return err
		}
		if err := auth.ClearMFA(ctx, tx, accountID); err != nil {
			return err
		}
		closed, err := closeSessions(ctx, tx, accountID, true)
		if err != nil {
			return err
		}
		return a.audit(ctx, tx, "member.mfa_reset", "member", id, map[string]any{"account": accountID, "sessions_closed": closed})
	})
}

// exclusiveAccount restituisce l'account del membro se vale solo in questo tenant.
func exclusiveAccount(ctx context.Context, tx pgx.Tx, memberID string) (string, error) {
	var accountID *string
	if err := tx.QueryRow(ctx, "select account_id from members where id = $1 and status <> 'anonymized'", memberID).Scan(&accountID); err != nil {
		return "", notFound(err)
	}
	if accountID == nil {
		return "", conflict("member.no_account", "il membro non ha un account")
	}
	var others int
	var platform bool
	if err := tx.QueryRow(ctx, "select account_other_memberships($1, app_tenant()), (select is_platform_admin from accounts where id = $1)",
		*accountID).Scan(&others, &platform); err != nil {
		return "", err
	}
	if others > 0 || platform {
		return "", ErrSharedAccount
	}
	return *accountID, nil
}
