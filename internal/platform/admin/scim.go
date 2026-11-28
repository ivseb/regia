package admin

// SCIM 2.0 (RFC 7643/7644): le operazioni su utenti e gruppi, già tradotte dal protocollo
// (internal/api/scim.go). Chi agisce è il token SCIM dell'organizzazione.
//
//   - Utente = membro. La creazione riusa l'account con la stessa email e non dà ruoli.
//   - active=false e DELETE sospendono (suspended_source = idp): niente cancellazioni,
//     l'audit resta. active=true riattiva solo chi aveva sospeso l'IdP.
//   - Gruppo = gruppo dell'IdP (idp_groups). Conta solo se è mappato su un gruppo di Regia;
//     SCIM non crea gruppi di Regia e non assegna ruoli.

import (
	"context"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// SCIMActor è il token SCIM che agisce.
type SCIMActor struct {
	TenantID string
	TokenID  string
}

func (a SCIMActor) audit(ctx context.Context, tx pgx.Tx, action, entityType, entityID string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["scim_token"] = a.TokenID
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: entityID, Payload: payload})
}

// SCIMError è un errore del protocollo SCIM (status HTTP e scimType).
type SCIMError struct {
	Status   int
	SCIMType string
	Detail   string
}

func (e *SCIMError) Error() string { return e.Detail }

var (
	ErrSCIMNotFound = &SCIMError{Status: 404, Detail: "risorsa inesistente"}
)

func scimInvalid(detail string) error {
	return &SCIMError{Status: 400, SCIMType: "invalidValue", Detail: detail}
}

// SCIMUser è un utente SCIM.
type SCIMUser struct {
	ID          string
	ExternalID  string
	UserName    string
	DisplayName string
	Email       string
	Active      bool
	Created     time.Time
	Modified    time.Time
}

// SCIMRef è un membro di un gruppo.
type SCIMRef struct {
	Value   string
	Display string
}

// SCIMGroup è un gruppo SCIM.
type SCIMGroup struct {
	ID          string
	ExternalID  string
	DisplayName string
	Members     []SCIMRef
	Created     time.Time
	Modified    time.Time
}

// SCIMFilter è un filtro «attributo eq valore» (gli unici che usano Entra ID, Okta e Kanidm).
type SCIMFilter struct {
	Attr  string // minuscolo: username, externalid, id, emails.value, displayname
	Value string
}

const scimUserSelect = `select m.id, coalesce(m.scim_external_id, ''),
	coalesce(m.scim_user_name, nullif(m.email, ''), a.email, ''), m.display_name, coalesce(nullif(m.email, ''), a.email, ''),
	m.status in ('active', 'invited'), m.created_at, m.updated_at
	from members m left join accounts a on a.id = m.account_id
	where m.status <> 'anonymized' and m.member_type <> 'service'`

func scanUser(row pgx.Row) (SCIMUser, error) {
	var u SCIMUser
	err := row.Scan(&u.ID, &u.ExternalID, &u.UserName, &u.DisplayName, &u.Email, &u.Active, &u.Created, &u.Modified)
	return u, err
}

func (s *Service) scimTx(ctx context.Context, a SCIMActor, fn func(pgx.Tx) error) error {
	return s.db.InTenant(ctx, a.TenantID, db.TenantOpts{}, fn)
}

// SCIMListUsers elenca gli utenti (startIndex da 1).
func (s *Service) SCIMListUsers(ctx context.Context, a SCIMActor, f *SCIMFilter, start, count int) ([]SCIMUser, int, error) {
	where, args := "", []any{}
	if f != nil {
		switch f.Attr {
		case "username":
			where = " and lower(coalesce(m.scim_user_name, nullif(m.email, ''), a.email)) = lower($1)"
		case "externalid":
			where = " and m.scim_external_id = $1"
		case "id":
			if !ValidID(f.Value) {
				return []SCIMUser{}, 0, nil
			}
			where = " and m.id = $1::uuid"
		case "emails.value", "emails":
			where = " and lower(coalesce(nullif(m.email, ''), a.email)) = lower($1)"
		default:
			return nil, 0, &SCIMError{Status: 400, SCIMType: "invalidFilter", Detail: "filtro non supportato: " + f.Attr}
		}
		args = append(args, f.Value)
	}
	out := []SCIMUser{}
	var total int
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "select count(*) from ("+scimUserSelect+where+") x", args...).Scan(&total); err != nil {
			return err
		}
		n := len(args)
		rows, err := tx.Query(ctx, scimUserSelect+where+" order by m.created_at, m.id offset $"+itoa(n+1)+" limit $"+itoa(n+2),
			append(args, start-1, count)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, total, err
}

// SCIMGetUser legge un utente.
func (s *Service) SCIMGetUser(ctx context.Context, a SCIMActor, id string) (*SCIMUser, error) {
	if !ValidID(id) {
		return nil, ErrSCIMNotFound
	}
	var u SCIMUser
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, scimUserSelect+" and m.id = $1", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSCIMNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func cleanSCIMUser(u *SCIMUser) error {
	u.UserName = strings.TrimSpace(u.UserName)
	u.ExternalID = strings.TrimSpace(u.ExternalID)
	if u.UserName == "" || len(u.UserName) > 254 {
		return scimInvalid("userName obbligatorio")
	}
	if len(u.ExternalID) > 300 {
		return scimInvalid("externalId troppo lungo")
	}
	email := strings.TrimSpace(u.Email)
	if email == "" && strings.Contains(u.UserName, "@") {
		email = u.UserName
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return scimInvalid("serve un'email valida (emails o userName)")
	}
	u.Email = strings.ToLower(email)
	u.DisplayName = cleanName(u.DisplayName, 120)
	if u.DisplayName == "" {
		u.DisplayName = u.Email[:strings.IndexByte(u.Email, '@')]
	}
	return nil
}

// SCIMCreateUser crea un utente: account riusato se esiste, membro senza ruoli.
func (s *Service) SCIMCreateUser(ctx context.Context, a SCIMActor, u SCIMUser) (*SCIMUser, error) {
	if err := cleanSCIMUser(&u); err != nil {
		return nil, err
	}
	memberID := db.NewID()
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var taken bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from members m left join accounts a on a.id = m.account_id
			where m.status <> 'anonymized' and (lower(m.scim_user_name) = lower($1)
			  or lower(coalesce(nullif(m.email, ''), a.email)) = $2))`, u.UserName, u.Email).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return &SCIMError{Status: 409, SCIMType: "uniqueness", Detail: "l'utente esiste già"}
		}
		var accountID string
		err := tx.QueryRow(ctx, "select id from accounts where lower(email) = $1", u.Email).Scan(&accountID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		newAccount := accountID == ""
		if newAccount {
			accountID = db.NewID()
			if _, err := tx.Exec(ctx, "insert into accounts (id, email, display_name) values ($1, $2, $3)", accountID, u.Email, u.DisplayName); err != nil {
				if isUnique(err) {
					return &SCIMError{Status: 409, SCIMType: "uniqueness", Detail: "l'utente esiste già"}
				}
				return err
			}
		}
		status := "active"
		if !u.Active {
			status = "suspended"
		}
		var src *string
		if status == "suspended" {
			v := "idp"
			src = &v
		}
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, account_id, display_name, email, member_type, status, origin,
			scim_external_id, scim_user_name, suspended_source)
			values (app_tenant(), $1, $2, $3, $4, 'standard', $5, 'scim', nullif($6, ''), $7, $8)`,
			memberID, accountID, u.DisplayName, u.Email, status, u.ExternalID, u.UserName, src); err != nil {
			if isUnique(err) {
				return &SCIMError{Status: 409, SCIMType: "uniqueness", Detail: "l'utente esiste già"}
			}
			return err
		}
		if _, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select app_tenant(), id, $1 from groups where system_kind = 'all_members'`, memberID); err != nil {
			return err
		}
		return a.audit(ctx, tx, "scim.user.create", "member", memberID, map[string]any{"account": accountID, "new_account": newAccount, "active": u.Active})
	})
	if err != nil {
		return nil, err
	}
	return s.SCIMGetUser(ctx, a, memberID)
}

// SCIMReplaceUser sostituisce gli attributi dell'utente (PUT, e PATCH dopo aver applicato
// le operazioni). L'email dell'account non cambia: è globale e vale anche altrove.
func (s *Service) SCIMReplaceUser(ctx context.Context, a SCIMActor, id string, u SCIMUser) (*SCIMUser, error) {
	if !ValidID(id) {
		return nil, ErrSCIMNotFound
	}
	if err := cleanSCIMUser(&u); err != nil {
		return nil, err
	}
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var status string
		var source, accountID *string
		if err := tx.QueryRow(ctx, "select status, suspended_source, account_id from members where id = $1 and status <> 'anonymized' and member_type <> 'service' for update", id).
			Scan(&status, &source, &accountID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSCIMNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `update members set display_name = $2, scim_user_name = $3, scim_external_id = nullif($4, ''),
			email = $5, updated_at = now() where id = $1`, id, u.DisplayName, u.UserName, u.ExternalID, u.Email); err != nil {
			if isUnique(err) {
				return &SCIMError{Status: 409, SCIMType: "uniqueness", Detail: "userName già usato da un altro utente"}
			}
			return err
		}
		active := status == "active" || status == "invited"
		switch {
		case active && !u.Active:
			payload, err := suspendMember(ctx, tx, id, accountID, "idp")
			if errors.Is(err, ErrLastAdmin) {
				return &SCIMError{Status: 409, Detail: "l'utente è l'ultimo amministratore attivo dell'organizzazione: non si sospende"}
			}
			if err != nil {
				return err
			}
			payload["from"] = status
			return a.audit(ctx, tx, "scim.user.suspend", "member", id, payload)
		case !active && u.Active && source != nil && *source == "idp":
			if _, err := tx.Exec(ctx, "update members set status = 'active', suspended_source = null, updated_at = now() where id = $1", id); err != nil {
				return err
			}
			return a.audit(ctx, tx, "scim.user.reactivate", "member", id, nil)
		}
		return a.audit(ctx, tx, "scim.user.update", "member", id, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.SCIMGetUser(ctx, a, id)
}

// SCIMDeleteUser sospende l'utente (non lo cancella).
func (s *Service) SCIMDeleteUser(ctx context.Context, a SCIMActor, id string) error {
	u, err := s.SCIMGetUser(ctx, a, id)
	if err != nil {
		return err
	}
	u.Active = false
	_, err = s.SCIMReplaceUser(ctx, a, id, *u)
	return err
}

// ---------------------------------------------------------------------
// Gruppi
// ---------------------------------------------------------------------

func (s *Service) loadGroup(ctx context.Context, tx pgx.Tx, id string, members bool) (*SCIMGroup, error) {
	var g SCIMGroup
	if err := tx.QueryRow(ctx, "select id, external_id, display_name, created_at, updated_at from idp_groups where id = $1 and source = 'scim'", id).
		Scan(&g.ID, &g.ExternalID, &g.DisplayName, &g.Created, &g.Modified); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSCIMNotFound
		}
		return nil, err
	}
	g.Members = []SCIMRef{}
	if members {
		rows, err := tx.Query(ctx, `select m.id, m.display_name from idp_group_members gm join members m on m.tenant_id = gm.tenant_id and m.id = gm.member_id
			where gm.idp_group_id = $1 and m.status <> 'anonymized' order by m.id`, id)
		if err != nil {
			return nil, err
		}
		if g.Members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SCIMRef]); err != nil {
			return nil, err
		}
	}
	return &g, nil
}

// SCIMListGroups elenca i gruppi arrivati da SCIM.
func (s *Service) SCIMListGroups(ctx context.Context, a SCIMActor, f *SCIMFilter, start, count int, members bool) ([]SCIMGroup, int, error) {
	where, args := "", []any{}
	if f != nil {
		switch f.Attr {
		case "displayname":
			where = " and lower(display_name) = lower($1)"
		case "externalid":
			where = " and lower(external_id) = lower($1)"
		case "id":
			if !ValidID(f.Value) {
				return []SCIMGroup{}, 0, nil
			}
			where = " and id = $1::uuid"
		default:
			return nil, 0, &SCIMError{Status: 400, SCIMType: "invalidFilter", Detail: "filtro non supportato: " + f.Attr}
		}
		args = append(args, f.Value)
	}
	out := []SCIMGroup{}
	var total int
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "select count(*) from idp_groups where source = 'scim'"+where, args...).Scan(&total); err != nil {
			return err
		}
		n := len(args)
		rows, err := tx.Query(ctx, "select id from idp_groups where source = 'scim'"+where+" order by created_at, id offset $"+itoa(n+1)+" limit $"+itoa(n+2),
			append(args, start-1, count)...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			g, err := s.loadGroup(ctx, tx, id, members)
			if err != nil {
				return err
			}
			out = append(out, *g)
		}
		return nil
	})
	return out, total, err
}

// SCIMGetGroup legge un gruppo.
func (s *Service) SCIMGetGroup(ctx context.Context, a SCIMActor, id string, members bool) (*SCIMGroup, error) {
	if !ValidID(id) {
		return nil, ErrSCIMNotFound
	}
	var g *SCIMGroup
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var err error
		g, err = s.loadGroup(ctx, tx, id, members)
		return err
	})
	return g, err
}

// setGroupMembers porta l'appartenenza al gruppo dell'IdP all'elenco dato e riallinea i
// gruppi governati di chi entra o esce.
func setGroupMembers(ctx context.Context, tx pgx.Tx, groupID string, memberIDs []string) (added, removed int, err error) {
	for _, m := range memberIDs {
		if !ValidID(m) {
			return 0, 0, scimInvalid("membro inesistente: " + m)
		}
	}
	var known int
	if err := tx.QueryRow(ctx, "select count(*) from members where id = any($1::uuid[]) and status <> 'anonymized'", memberIDs).Scan(&known); err != nil {
		return 0, 0, err
	}
	if known != len(uniq(memberIDs)) {
		return 0, 0, scimInvalid("uno o più membri inesistenti")
	}
	var out, in []string
	if err := tx.QueryRow(ctx, `with del as (delete from idp_group_members where idp_group_id = $1 and not (member_id = any($2::uuid[])) returning member_id)
		select coalesce(array_agg(member_id::text), '{}') from del`, groupID, memberIDs).Scan(&out); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `with ins as (insert into idp_group_members (tenant_id, idp_group_id, member_id)
		select app_tenant(), $1, m from unnest($2::uuid[]) m on conflict do nothing returning member_id)
		select coalesce(array_agg(member_id::text), '{}') from ins`, groupID, uniq(memberIDs)).Scan(&in); err != nil {
		return 0, 0, err
	}
	if err := reconcile(ctx, tx, append(out, in...)); err != nil {
		return 0, 0, err
	}
	return len(in), len(out), nil
}

func uniq(v []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range v {
		s = strings.ToLower(s)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func lastAdminSCIM(err error) error {
	if errors.Is(err, ErrLastAdmin) {
		return &SCIMError{Status: 409, Detail: "la modifica lascerebbe l'organizzazione senza amministratori attivi"}
	}
	return err
}

// SCIMCreateGroup crea un gruppo dell'IdP. Se lo stesso identificativo era stato scritto a
// mano o visto nel claim (per mapparlo in anticipo), il gruppo si adotta.
func (s *Service) SCIMCreateGroup(ctx context.Context, a SCIMActor, g SCIMGroup) (*SCIMGroup, error) {
	g.DisplayName = cleanName(g.DisplayName, 200)
	g.ExternalID = strings.TrimSpace(g.ExternalID)
	if g.DisplayName == "" {
		return nil, scimInvalid("displayName obbligatorio")
	}
	key := g.ExternalID
	if key == "" {
		key = g.DisplayName
	}
	if len(key) > 300 {
		return nil, scimInvalid("externalId troppo lungo")
	}
	var id string
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var source string
		err := tx.QueryRow(ctx, "select id, source from idp_groups where lower(external_id) = lower($1) for update", key).Scan(&id, &source)
		switch {
		case err == nil && source == "scim":
			return &SCIMError{Status: 409, SCIMType: "uniqueness", Detail: "il gruppo esiste già"}
		case err == nil:
			if _, err := tx.Exec(ctx, "update idp_groups set source = 'scim', display_name = $2, updated_at = now() where id = $1", id, g.DisplayName); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			id = db.NewID()
			if _, err := tx.Exec(ctx, "insert into idp_groups (tenant_id, id, external_id, display_name, source) values (app_tenant(), $1, $2, $3, 'scim')",
				id, key, g.DisplayName); err != nil {
				return err
			}
		default:
			return err
		}
		refs := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			refs = append(refs, m.Value)
		}
		in, out, err := setGroupMembers(ctx, tx, id, refs)
		if err != nil {
			return lastAdminSCIM(err)
		}
		return a.audit(ctx, tx, "scim.group.create", "idp_group", id, map[string]any{"members_added": in, "members_removed": out})
	})
	if err != nil {
		return nil, err
	}
	return s.SCIMGetGroup(ctx, a, id, true)
}

// SCIMReplaceGroup sostituisce nome e membri (PUT, e PATCH dopo aver applicato le
// operazioni). L'identificativo esterno resta: le mappature lo usano.
func (s *Service) SCIMReplaceGroup(ctx context.Context, a SCIMActor, id string, g SCIMGroup) (*SCIMGroup, error) {
	if !ValidID(id) {
		return nil, ErrSCIMNotFound
	}
	g.DisplayName = cleanName(g.DisplayName, 200)
	if g.DisplayName == "" {
		return nil, scimInvalid("displayName obbligatorio")
	}
	err := s.scimTx(ctx, a, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update idp_groups set display_name = $2, updated_at = now() where id = $1 and source = 'scim'", id, g.DisplayName)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSCIMNotFound
		}
		refs := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			refs = append(refs, m.Value)
		}
		in, out, err := setGroupMembers(ctx, tx, id, refs)
		if err != nil {
			return lastAdminSCIM(err)
		}
		return a.audit(ctx, tx, "scim.group.update", "idp_group", id, map[string]any{"members_added": in, "members_removed": out})
	})
	if err != nil {
		return nil, err
	}
	return s.SCIMGetGroup(ctx, a, id, true)
}

// SCIMDeleteGroup cancella il gruppo dell'IdP (e le sue mappature) e riallinea i membri.
func (s *Service) SCIMDeleteGroup(ctx context.Context, a SCIMActor, id string) error {
	if !ValidID(id) {
		return ErrSCIMNotFound
	}
	return s.scimTx(ctx, a, func(tx pgx.Tx) error {
		var members []string
		if err := tx.QueryRow(ctx, "select coalesce(array_agg(member_id::text), '{}') from idp_group_members where idp_group_id = $1", id).Scan(&members); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "delete from idp_groups where id = $1 and source = 'scim'", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrSCIMNotFound
		}
		if err := reconcile(ctx, tx, members); err != nil {
			return lastAdminSCIM(err)
		}
		return a.audit(ctx, tx, "scim.group.delete", "idp_group", id, map[string]any{"members": len(members)})
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
