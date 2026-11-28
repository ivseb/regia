package admin

// Identità aziendale (Configurazione → Persone e accessi → Identità aziendale): gli ingressi
// e i gruppi li governa l'IdP. Migrazioni 0102 e 0103, guida in docs/identita-aziendale.md.
//
//   - Creazione al primo accesso OIDC, se l'organizzazione la accende: serve un'email
//     verificata con un dominio ammesso, oppure almeno un gruppo dell'IdP mappato qui.
//   - Mappatura esplicita: un gruppo dell'IdP conta solo se è mappato su un gruppo di Regia.
//     Un gruppo di Regia mappato è «governato dall'IdP» e la sua appartenenza non si cambia
//     a mano (ErrIdPManaged).
//   - Fonte dei gruppi: SCIM se l'organizzazione ha un token SCIM attivo, altrimenti il
//     claim dei gruppi a ogni accesso. Claim assente o in overage = nessuna modifica.
//   - Chi entra così non riceve ruoli diretti: solo quelli dei gruppi mappati.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// IdPSettings sono le impostazioni dell'organizzazione.
type IdPSettings struct {
	Provisioning   bool       `json:"provisioning"`
	AllowedDomains []string   `json:"allowed_domains"`
	GroupsClaim    string     `json:"groups_claim"`
	UpdatedAt      *time.Time `json:"updated_at"`
}

// IdPGroup è un gruppo dell'IdP con le sue mappature.
type IdPGroup struct {
	ID          string       `json:"id"`
	ExternalID  string       `json:"external_id"`
	DisplayName string       `json:"display_name"`
	Source      string       `json:"source"`
	Members     int          `json:"members"`
	Mappings    []IdPMapping `json:"mappings"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// IdPMapping è una mappatura verso un gruppo di Regia.
type IdPMapping struct {
	ID        string `json:"id"`
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
}

// SCIMToken è un token SCIM come appare nell'elenco (mai il segreto).
type SCIMToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// IdPOverview è la risposta di GET /admin/idp.
type IdPOverview struct {
	Settings   IdPSettings `json:"settings"`
	Groups     []IdPGroup  `json:"groups"`
	SCIMTokens []SCIMToken `json:"scim_tokens"`
	// SCIMActive: c'è un token SCIM attivo, quindi il claim dei gruppi al login si ignora.
	SCIMActive bool `json:"scim_active"`
	// AllMembersRoles: «Tutti i membri» ha ruoli di tenant, che arrivano anche agli esterni.
	AllMembersRoles []string `json:"all_members_roles"`
	// OnlyViewer: membri con il solo «Lettore» diretto (il vecchio default degli inviti).
	OnlyViewer int `json:"only_viewer"`
}

const defaultGroupsClaim = "groups"

var (
	claimRe  = regexp.MustCompile(`^[A-Za-z0-9_:./-]{1,100}$`)
	domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

func readSettings(ctx context.Context, tx pgx.Tx) (IdPSettings, error) {
	out := IdPSettings{AllowedDomains: []string{}, GroupsClaim: defaultGroupsClaim}
	err := tx.QueryRow(ctx, "select provisioning, allowed_domains, groups_claim, updated_at from tenant_idp_settings").
		Scan(&out.Provisioning, &out.AllowedDomains, &out.GroupsClaim, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}

func scimActive(ctx context.Context, tx pgx.Tx) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, "select exists (select 1 from scim_tokens where revoked_at is null)").Scan(&on)
	return on, err
}

// IdP restituisce impostazioni, gruppi dell'IdP con le mappature e token SCIM.
func (s *Service) IdP(ctx context.Context, tenantID string) (*IdPOverview, error) {
	out := &IdPOverview{Groups: []IdPGroup{}, SCIMTokens: []SCIMToken{}, AllMembersRoles: []string{}}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		if out.Settings, err = readSettings(ctx, tx); err != nil {
			return err
		}
		if out.SCIMActive, err = scimActive(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select g.id, g.external_id, g.display_name, g.source, g.updated_at,
			  (select count(*) from idp_group_members m where m.idp_group_id = g.id),
			  coalesce((select jsonb_agg(jsonb_build_object('id', mp.id, 'group_id', rg.id, 'group_name', rg.name) order by lower(rg.name))
			            from idp_group_mappings mp join groups rg on rg.tenant_id = mp.tenant_id and rg.id = mp.group_id
			            where mp.idp_group_id = g.id), '[]')
			from idp_groups g
			order by not exists (select 1 from idp_group_mappings mp where mp.idp_group_id = g.id), lower(coalesce(nullif(g.display_name, ''), g.external_id))
			limit 2000`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var g IdPGroup
			var maps []byte
			if err := rows.Scan(&g.ID, &g.ExternalID, &g.DisplayName, &g.Source, &g.UpdatedAt, &g.Members, &maps); err != nil {
				rows.Close()
				return err
			}
			g.Mappings = []IdPMapping{}
			_ = json.Unmarshal(maps, &g.Mappings)
			out.Groups = append(out.Groups, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, "select id, name, hint, created_at, last_used_at from scim_tokens where revoked_at is null order by created_at")
		if err != nil {
			return err
		}
		if out.SCIMTokens, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SCIMToken]); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select coalesce(array_agg(r.name order by r.name), '{}') from role_assignments ra
			join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			join groups g on g.tenant_id = ra.tenant_id and g.id = ra.group_id
			where g.system_kind = 'all_members' and ra.space_id is null`).Scan(&out.AllMembersRoles); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from members m where m.status in ('active', 'invited')
			and exists (select 1 from role_assignments ra join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			            where ra.member_id = m.id and ra.space_id is null and r.key = 'viewer')
			and not exists (select 1 from role_assignments ra join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			                where ra.space_id is null and coalesce(r.key, '') <> 'viewer'
			                  and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id))))`).Scan(&out.OnlyViewer)
	})
	return out, err
}

// SetIdPSettings cambia le impostazioni dell'organizzazione.
func (s *Service) SetIdPSettings(ctx context.Context, a Actor, in IdPSettings) error {
	claim := strings.TrimSpace(in.GroupsClaim)
	if claim == "" {
		claim = defaultGroupsClaim
	}
	if !claimRe.MatchString(claim) {
		return invalid("groups_claim", "nome del claim non valido: lettere, cifre e _ : . / -")
	}
	domains := []string{}
	for _, d := range in.AllowedDomains {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@")))
		if d == "" {
			continue
		}
		if !domainRe.MatchString(d) || len(d) > 253 {
			return invalid("allowed_domains", "dominio non valido: "+d)
		}
		if !slices.Contains(domains, d) {
			domains = append(domains, d)
		}
	}
	if len(domains) > 50 {
		return invalid("allowed_domains", "al massimo 50 domini")
	}
	slices.Sort(domains)
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		before, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into tenant_idp_settings (tenant_id, provisioning, allowed_domains, groups_claim, updated_at, updated_by)
			values (app_tenant(), $1, $2, $3, now(), $4)
			on conflict (tenant_id) do update set provisioning = excluded.provisioning, allowed_domains = excluded.allowed_domains,
			  groups_claim = excluded.groups_claim, updated_at = now(), updated_by = excluded.updated_by`,
			in.Provisioning, domains, claim, nullable(a.MemberID)); err != nil {
			return err
		}
		return a.audit(ctx, tx, "tenant.idp.update", "tenant", a.TenantID, map[string]any{
			"provisioning": in.Provisioning, "from": before.Provisioning, "domains": len(domains), "groups_claim": claim})
	})
}

// CreateIdPGroup aggiunge a mano un gruppo dell'IdP (per mapparlo prima del primo accesso).
func (s *Service) CreateIdPGroup(ctx context.Context, a Actor, externalID, displayName string) (string, error) {
	externalID = strings.TrimSpace(externalID)
	displayName = cleanName(displayName, 200)
	if externalID == "" || len(externalID) > 300 {
		return "", invalid("external_id", "serve l'identificativo con cui l'IdP nomina il gruppo")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "insert into idp_groups (tenant_id, id, external_id, display_name, source) values (app_tenant(), $1, $2, $3, 'manual')",
			id, externalID, displayName); err != nil {
			if isUnique(err) {
				return invalid("external_id", "questo gruppo dell'IdP c'è già")
			}
			return err
		}
		return a.audit(ctx, tx, "idp.group.create", "idp_group", id, nil)
	})
	return id, err
}

// DeleteIdPGroup toglie un gruppo dell'IdP scritto a mano o visto nel claim (quelli di
// SCIM li toglie l'IdP). Le mappature cadono e i gruppi governati si riallineano.
func (s *Service) DeleteIdPGroup(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var source string
		if err := tx.QueryRow(ctx, "select source from idp_groups where id = $1 for update", id).Scan(&source); err != nil {
			return notFound(err)
		}
		if source == "scim" {
			return conflict("idp.group_scim", "il gruppo arriva da SCIM: lo toglie l'IdP")
		}
		groups, members, err := mappingScope(ctx, tx, id, "")
		if err != nil {
			return err
		}
		if err := a.canGrantGroups(ctx, tx, groups); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from idp_groups where id = $1", id); err != nil {
			return err
		}
		if err := reconcile(ctx, tx, members); err != nil {
			return err
		}
		return a.audit(ctx, tx, "idp.group.delete", "idp_group", id, map[string]any{"members": len(members)})
	})
}

// mappingScope: gruppi di Regia mappati dal gruppo dell'IdP (o solo groupID) e membri del
// gruppo dell'IdP, cioè chi cambia appartenenza se la mappatura cambia.
func mappingScope(ctx context.Context, tx pgx.Tx, idpGroupID, groupID string) (groups, members []string, err error) {
	if groupID != "" {
		groups = []string{groupID}
	} else if err = tx.QueryRow(ctx, "select coalesce(array_agg(group_id::text), '{}') from idp_group_mappings where idp_group_id = $1",
		idpGroupID).Scan(&groups); err != nil {
		return nil, nil, err
	}
	err = tx.QueryRow(ctx, "select coalesce(array_agg(member_id::text), '{}') from idp_group_members where idp_group_id = $1", idpGroupID).Scan(&members)
	return groups, members, err
}

// CreateMapping mappa un gruppo dell'IdP su un gruppo di Regia. Chi la crea deve poter
// dare i ruoli del gruppo di Regia; da quel momento il gruppo è governato dall'IdP.
func (s *Service) CreateMapping(ctx context.Context, a Actor, idpGroupID, groupID string) (string, error) {
	if !ValidID(idpGroupID) || !ValidID(groupID) {
		return "", invalid("group_id", "gruppo inesistente")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		var system bool
		if err := tx.QueryRow(ctx, "select system_kind is not null from groups where id = $1", groupID).Scan(&system); err != nil {
			return invalid("group_id", "gruppo di Regia inesistente")
		}
		if system {
			return conflict("group.system", "«Tutti i membri» comprende già ogni membro: scegli un altro gruppo")
		}
		if err := a.canGrantGroups(ctx, tx, []string{groupID}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into idp_group_mappings (tenant_id, id, idp_group_id, group_id, created_by) values (app_tenant(), $1, $2, $3, $4)",
			id, idpGroupID, groupID, nullable(a.MemberID)); err != nil {
			if isUnique(err) {
				return conflict("idp.mapping_exists", "la mappatura c'è già")
			}
			if isFK(err) {
				return invalid("idp_group_id", "gruppo dell'IdP inesistente")
			}
			return err
		}
		// Il gruppo diventa governato: chi ci stava a mano e non è nel gruppo dell'IdP esce.
		var members []string
		if err := tx.QueryRow(ctx, `select coalesce(array_agg(distinct m::text), '{}') from (
			select member_id m from group_members where group_id = $1
			union select member_id from idp_group_members where idp_group_id = $2) x`, groupID, idpGroupID).Scan(&members); err != nil {
			return err
		}
		if err := reconcile(ctx, tx, members); err != nil {
			return err
		}
		return a.audit(ctx, tx, "idp.mapping.create", "idp_group", idpGroupID, map[string]any{"mapping": id, "group": groupID})
	})
	return id, err
}

// DeleteMapping toglie una mappatura (chi la toglie deve poter dare i ruoli del gruppo).
func (s *Service) DeleteMapping(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var idpGroupID, groupID string
		if err := tx.QueryRow(ctx, "select idp_group_id, group_id from idp_group_mappings where id = $1", id).Scan(&idpGroupID, &groupID); err != nil {
			return notFound(err)
		}
		if err := a.canGrantGroups(ctx, tx, []string{groupID}); err != nil {
			return err
		}
		_, members, err := mappingScope(ctx, tx, idpGroupID, groupID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from idp_group_mappings where id = $1", id); err != nil {
			return err
		}
		// Se il gruppo resta governato da altre mappature si riallinea; altrimenti torna
		// manuale e chi c'era resta (si toglie a mano, se serve).
		var still bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from idp_group_mappings where group_id = $1)", groupID).Scan(&still); err != nil {
			return err
		}
		if still {
			if err := reconcile(ctx, tx, members); err != nil {
				return err
			}
		}
		return a.audit(ctx, tx, "idp.mapping.delete", "idp_group", idpGroupID, map[string]any{"mapping": id, "group": groupID})
	})
}

// reconcile riallinea i gruppi governati per i membri indicati e controlla, prima del
// commit, che resti un amministratore.
func reconcile(ctx context.Context, tx pgx.Tx, members []string) error {
	changed := false
	for _, m := range members {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from idp_reconcile($1)", m).Scan(&n); err != nil {
			return err
		}
		changed = changed || n > 0
	}
	if changed {
		return ensureAdmin(ctx, tx)
	}
	return nil
}

// ---------------------------------------------------------------------
// Token SCIM
// ---------------------------------------------------------------------

// SCIMTokenPrefix distingue i token SCIM dai token API personali (myt_).
const SCIMTokenPrefix = "scim_"

// CreateSCIMToken crea un token SCIM e lo restituisce una volta sola.
func (s *Service) CreateSCIMToken(ctx context.Context, a Actor, name string) (string, *SCIMToken, error) {
	name = cleanName(name, 80)
	if name == "" {
		name = "SCIM"
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	secret := SCIMTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(secret))
	t := &SCIMToken{ID: db.NewID(), Name: name, Hint: secret[:len(SCIMTokenPrefix)+4], CreatedAt: time.Now()}
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from scim_tokens where revoked_at is null").Scan(&n); err != nil {
			return err
		}
		if n >= 5 {
			return conflict("scim.too_many_tokens", "al massimo 5 token SCIM attivi: revocane uno")
		}
		if _, err := tx.Exec(ctx, "insert into scim_tokens (tenant_id, id, name, token_hash, hint, created_by) values (app_tenant(), $1, $2, $3, $4, $5)",
			t.ID, t.Name, h[:], t.Hint, nullable(a.MemberID)); err != nil {
			return err
		}
		return a.audit(ctx, tx, "scim.token.create", "scim_token", t.ID, nil)
	})
	if err != nil {
		return "", nil, err
	}
	return secret, t, nil
}

// RevokeSCIMToken revoca un token SCIM.
func (s *Service) RevokeSCIMToken(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update scim_tokens set revoked_at = now() where id = $1 and revoked_at is null", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return a.audit(ctx, tx, "scim.token.revoke", "scim_token", id, nil)
	})
}

// ErrSCIMToken: token SCIM sconosciuto, revocato o di un'organizzazione non attiva.
var ErrSCIMToken = errors.New("token SCIM non valido o revocato")

// SCIMAuth risolve un token SCIM: restituisce tenant e token.
func (s *Service) SCIMAuth(ctx context.Context, secret string) (tenantID, tokenID string, err error) {
	if !strings.HasPrefix(secret, SCIMTokenPrefix) || len(secret) > 100 {
		return "", "", ErrSCIMToken
	}
	h := sha256.Sum256([]byte(secret))
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select tenant_id, token_id from scim_token_use($1)", h[:]).Scan(&tenantID, &tokenID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrSCIMToken
	}
	return tenantID, tokenID, err
}

// ---------------------------------------------------------------------
// Login OIDC (auth.OIDCHook)
// ---------------------------------------------------------------------

type provisionTarget struct {
	tenantID   string
	domains    []string
	claim      string
	mappedKeys []string
}

func (s *Service) provisionTargets(ctx context.Context, id *auth.Identity) ([]provisionTarget, error) {
	if id == nil || id.Email == "" || !id.EmailVerified {
		return nil, nil
	}
	domain := id.Email[strings.LastIndexByte(id.Email, '@')+1:]
	var out []provisionTarget
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "select tenant_id, allowed_domains, groups_claim, mapped_keys from idp_provisioning_targets()")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t provisionTarget
			if err := rows.Scan(&t.tenantID, &t.domains, &t.claim, &t.mappedKeys); err != nil {
				return err
			}
			ok := slices.Contains(t.domains, domain)
			if !ok {
				groups, _ := id.Groups(t.claim)
				for _, g := range groups {
					if slices.Contains(t.mappedKeys, strings.ToLower(g)) {
						ok = true
						break
					}
				}
			}
			if ok {
				out = append(out, t)
			}
		}
		return rows.Err()
	})
	return out, err
}

// Eligible: almeno un'organizzazione accoglie questa identità al primo accesso.
func (s *Service) Eligible(ctx context.Context, id *auth.Identity) (bool, error) {
	t, err := s.provisionTargets(ctx, id)
	return len(t) > 0, err
}

// AfterLogin crea i membri mancanti nelle organizzazioni che accolgono l'identità e
// riallinea i gruppi governati dall'IdP dove il membro è attivo.
func (s *Service) AfterLogin(ctx context.Context, accountID string, id *auth.Identity, ip netip.Addr) error {
	targets, err := s.provisionTargets(ctx, id)
	if err != nil {
		return err
	}
	for _, t := range targets {
		if err := s.provisionMember(ctx, t.tenantID, accountID, id, ip); err != nil {
			return err
		}
	}
	type ms struct{ tenantID, memberID string }
	var active []ms
	if err := s.db.Global(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "select tenant_id, member_id from account_memberships($1) where status = 'active'", accountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m ms
			if err := rows.Scan(&m.tenantID, &m.memberID); err != nil {
				return err
			}
			active = append(active, m)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, m := range active {
		err := s.syncClaim(ctx, m.tenantID, m.memberID, accountID, id, ip)
		if errors.Is(err, ErrLastAdmin) {
			// L'IdP toglierebbe l'ultimo amministratore: il riallineamento non si applica
			// (resta la regola di sempre) e l'accesso prosegue.
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) provisionMember(ctx context.Context, tenantID, accountID string, id *auth.Identity, ip netip.Addr) error {
	return s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where account_id = $1)", accountID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil // qualunque stato: sospeso o anonimizzato restano tali
		}
		name := cleanName(id.Name, 120)
		if name == "" {
			name = id.Email[:strings.IndexByte(id.Email, '@')]
		}
		memberID := db.NewID()
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, account_id, display_name, email, member_type, status, origin)
			values (app_tenant(), $1, $2, $3, $4, 'standard', 'active', 'oidc') on conflict do nothing`, memberID, accountID, name, id.Email); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select app_tenant(), id, $1 from groups where system_kind = 'all_members' on conflict do nothing`, memberID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "member.provision", EntityType: "member", EntityID: memberID,
			ActorAccountID: accountID, ActorMemberID: memberID, IP: ip, Payload: map[string]any{"origin": "oidc", "account": accountID}})
	})
}

// syncClaim riallinea i gruppi dell'IdP del membro al claim dei gruppi, se l'organizzazione
// non usa SCIM e il claim c'è.
func (s *Service) syncClaim(ctx context.Context, tenantID, memberID, accountID string, id *auth.Identity, ip netip.Addr) error {
	return s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if on, err := scimActive(ctx, tx); err != nil || on {
			return err
		}
		set, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}
		groups, present := id.Groups(set.GroupsClaim)
		if !present {
			return nil
		}
		// I valori nuovi diventano gruppi «visti dall'IdP», da mappare.
		if _, err := tx.Exec(ctx, `insert into idp_groups (tenant_id, external_id, display_name, source)
			select app_tenant(), v, v, 'oidc' from unnest($1::text[]) v
			on conflict (tenant_id, lower(external_id)) do nothing`, groups); err != nil {
			return err
		}
		lower := make([]string, len(groups))
		for i, g := range groups {
			lower[i] = strings.ToLower(g)
		}
		removed, err := tx.Exec(ctx, `delete from idp_group_members m using idp_groups g
			where m.member_id = $1 and g.tenant_id = m.tenant_id and g.id = m.idp_group_id and not (lower(g.external_id) = any($2::text[]))`,
			memberID, lower)
		if err != nil {
			return err
		}
		added, err := tx.Exec(ctx, `insert into idp_group_members (tenant_id, idp_group_id, member_id)
			select app_tenant(), g.id, $1 from idp_groups g where lower(g.external_id) = any($2::text[])
			on conflict do nothing`, memberID, lower)
		if err != nil {
			return err
		}
		var changes []struct {
			GroupID string
			Added   bool
		}
		rows, err := tx.Query(ctx, "select group_id, added from idp_reconcile($1)", memberID)
		if err != nil {
			return err
		}
		if changes, err = pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
			GroupID string
			Added   bool
		}]); err != nil {
			return err
		}
		if len(changes) > 0 {
			if err := ensureAdmin(ctx, tx); err != nil {
				return err
			}
		}
		if removed.RowsAffected() == 0 && added.RowsAffected() == 0 && len(changes) == 0 {
			return nil
		}
		in, out := []string{}, []string{}
		for _, c := range changes {
			if c.Added {
				in = append(in, c.GroupID)
			} else {
				out = append(out, c.GroupID)
			}
		}
		return audit.Append(ctx, tx, audit.Event{Action: "member.idp_sync", EntityType: "member", EntityID: memberID,
			ActorAccountID: accountID, ActorMemberID: memberID, IP: ip,
			Payload: map[string]any{"source": "oidc", "groups_added": in, "groups_removed": out}})
	})
}
