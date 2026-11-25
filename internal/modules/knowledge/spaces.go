package knowledge

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Spazi di documentazione (spaces.kind = knowledge). Accesso:
//   - aperto: si legge con element.read, si scrive con element.write; i ruoli assegnati sullo
//     spazio aggiungono lettura o scrittura a chi non le ha nel tenant;
//   - riservato: solo i ruoli assegnati sullo spazio (persone o gruppi) e l'amministratore.
// Lo amministra (nome, accesso, ruoli, template) il responsabile dello spazio o un
// amministratore del tenant.

// Rights sono i diritti dell'attore su uno spazio di documentazione.
type Rights struct {
	Read   bool `json:"read"`
	Write  bool `json:"write"`
	Manage bool `json:"manage"`
}

// Space è uno spazio di documentazione.
type Space struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Access      string     `json:"access"` // open | restricted
	LeadID      *string    `json:"lead_id"`
	Lead        string     `json:"lead"`
	Pages       int        `json:"pages"`
	UpdatedAt   *time.Time `json:"updated_at"`
	Rights      Rights     `json:"rights"`
}

var spaceKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

const spaceColumns = `s.id::text, s.key, s.name, coalesce(s.description, ''), coalesce(s.settings->>'access', 'open'), s.lead_id::text,
	coalesce(m.display_name, ''),
	(select count(*) from elements e where e.tenant_id = s.tenant_id and e.space_id = s.id and e.type_id = t.id and e.deleted_at is null)::int,
	(select max(e.updated_at) from elements e where e.tenant_id = s.tenant_id and e.space_id = s.id and e.type_id = t.id and e.deleted_at is null),
	space_access(s.id, $1::uuid)`

const spaceFrom = ` from spaces s
	join element_types t on t.tenant_id = s.tenant_id and t.key = 'page'
	left join members m on m.tenant_id = s.tenant_id and m.id = s.lead_id`

func scanSpace(actor engine.Actor, row pgx.Row) (*Space, error) {
	var sp Space
	var perms []string
	if err := row.Scan(&sp.ID, &sp.Key, &sp.Name, &sp.Description, &sp.Access, &sp.LeadID, &sp.Lead, &sp.Pages, &sp.UpdatedAt, &perms); err != nil {
		return nil, err
	}
	sp.Rights = rightsOf(actor, sp.Access == "restricted", perms, sp.LeadID)
	return &sp, nil
}

// rightsOf: stessa regola di engine.Rights, più l'amministrazione dello spazio.
func rightsOf(actor engine.Actor, restricted bool, perms []string, lead *string) Rights {
	admin := actor.Has("*") || slices.Contains(perms, "*")
	read := admin || slices.Contains(perms, "element.read") || slices.Contains(perms, "element.write") || (!restricted && actor.Has("element.read"))
	write := admin || slices.Contains(perms, "element.write") || (!restricted && actor.Has("element.write"))
	manage := admin || actor.Has("tenant.admin") || (lead != nil && *lead == actor.MemberID && actor.MemberID != "")
	return Rights{Read: read, Write: write, Manage: manage}
}

func memberArg(actor engine.Actor) any { return nullable(actor.MemberID) }

// Spaces elenca gli spazi di documentazione che l'attore può leggere o amministrare.
func (s *Service) Spaces(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Space, error) {
	rows, err := tx.Query(ctx, `select `+spaceColumns+spaceFrom+` where s.kind = 'knowledge' and not s.archived order by lower(s.name)`, memberArg(actor))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Space{}
	for rows.Next() {
		sp, err := scanSpace(actor, rows)
		if err != nil {
			return nil, err
		}
		if sp.Rights.Read || sp.Rights.Manage {
			if !sp.Rights.Read {
				sp.Pages, sp.UpdatedAt = 0, nil
			}
			out = append(out, *sp)
		}
	}
	return out, rows.Err()
}

// space trova lo spazio per chiave con i diritti dell'attore. Chi non può né leggerlo né
// amministrarlo riceve ErrNotFound (l'esistenza non si rivela).
func (s *Service) space(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Space, error) {
	sp, err := scanSpace(actor, tx.QueryRow(ctx, `select `+spaceColumns+spaceFrom+` where s.kind = 'knowledge' and s.key = $2`,
		memberArg(actor), strings.ToUpper(strings.TrimSpace(key))))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Read && !sp.Rights.Manage {
		return nil, engine.ErrNotFound
	}
	return sp, nil
}

// readableSpace: come space, ma serve la lettura.
func (s *Service) readableSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*Space, error) {
	sp, err := s.space(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Read {
		return nil, engine.ErrForbidden
	}
	return sp, nil
}

// SpaceInput crea o modifica uno spazio.
type SpaceInput struct {
	Key         string  `json:"key"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Access      *string `json:"access"`
	LeadID      *string `json:"lead_id"`
}

// CreateSpace crea uno spazio di documentazione. Serve element.write (o l'amministrazione del
// tenant); chi lo crea ne diventa responsabile e riceve il ruolo Editor sullo spazio.
func (s *Service) CreateSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SpaceInput) (*Space, error) {
	if !actor.Has("element.write") && !actor.Has("tenant.admin") {
		return nil, engine.ErrForbidden
	}
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	if !spaceKeyRe.MatchString(key) {
		return nil, engine.TooLong("key", 20, "la chiave è fatta di maiuscole e cifre, inizia con una lettera, al massimo 20 caratteri (es. DOC)")
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	access := "open"
	if in.Access != nil {
		access = *in.Access
	}
	if access != "open" && access != "restricted" {
		return nil, engine.InvalidCode("validation.knowledge.space_access", "access", "accesso: open o restricted")
	}
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	id := db.NewID()
	_, err := tx.Exec(ctx, `insert into spaces (tenant_id, id, key, name, kind, description, lead_id, settings)
		values (app_tenant(), $1, $2, $3, 'knowledge', $4, $5, jsonb_build_object('access', $6::text))`,
		id, key, strings.TrimSpace(*in.Name), desc, nullable(actor.MemberID), access)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, engine.Duplicate("key", "la chiave %s è già usata da un altro spazio", key)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), $1, id from element_types where key = $2`, id, TypePage); err != nil {
		return nil, err
	}
	if actor.MemberID != "" {
		if _, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, member_id, space_id)
			select app_tenant(), $1, r.id, $2, $3 from roles r where r.key = 'editor'`, db.NewID(), actor.MemberID, id); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "kb.space.create", EntityType: "space", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"key": key, "access": access}}); err != nil {
		return nil, err
	}
	return s.space(ctx, tx, actor, key)
}

// UpdateSpace modifica nome, descrizione, accesso e responsabile.
func (s *Service) UpdateSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, in SpaceInput) (*Space, error) {
	sp, err := s.space(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	if in.Access != nil && *in.Access != "open" && *in.Access != "restricted" {
		return nil, engine.InvalidCode("validation.knowledge.space_access", "access", "accesso: open o restricted")
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if in.LeadID != nil && *in.LeadID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from members where id::text = $1 and status = 'active')`, *in.LeadID).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.InvalidCode("validation.member_not_found", "lead_id", "membro inesistente")
		}
	}
	var name *string
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		name = &n
	}
	if _, err := tx.Exec(ctx, `update spaces set name = coalesce($2, name), description = coalesce($3, description),
		settings = case when $4::text is null then settings else jsonb_set(settings, '{access}', to_jsonb($4::text)) end,
		lead_id = case when $5::boolean then nullif($6, '')::uuid else lead_id end
		where id = $1`, sp.ID, name, in.Description, in.Access, in.LeadID != nil, deref(in.LeadID)); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if in.Access != nil {
		payload["access"] = *in.Access
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "kb.space.update", EntityType: "space", EntityID: sp.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload}); err != nil {
		return nil, err
	}
	return s.space(ctx, tx, actor, sp.Key)
}

// AccessEntry è un ruolo assegnato sullo spazio.
type AccessEntry struct {
	ID       string  `json:"id"`
	RoleKey  string  `json:"role_key"`
	RoleName string  `json:"role_name"`
	Level    string  `json:"level"` // write | read | none
	MemberID *string `json:"member_id"`
	GroupID  *string `json:"group_id"`
	Name     string  `json:"name"`
}

// Access elenca i ruoli assegnati sullo spazio.
func (s *Service) Access(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) ([]AccessEntry, error) {
	sp, err := s.space(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select ra.id::text, coalesce(r.key, ''), r.name,
		       case when r.permissions && array['*', 'element.write'] then 'write'
		            when r.permissions && array['element.read'] then 'read' else 'none' end,
		       ra.member_id::text, ra.group_id::text, coalesce(m.display_name, g.name, '')
		from role_assignments ra
		join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
		left join members m on m.tenant_id = ra.tenant_id and m.id = ra.member_id
		left join groups g on g.tenant_id = ra.tenant_id and g.id = ra.group_id
		where ra.space_id = $1 order by 4, 7`, sp.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[AccessEntry])
	if out == nil {
		out = []AccessEntry{}
	}
	return out, err
}

// GrantInput assegna un ruolo sullo spazio a una persona o a un gruppo.
type GrantInput struct {
	Role     string `json:"role"` // chiave del ruolo: viewer (lettura), editor (scrittura) o altro
	MemberID string `json:"member_id"`
	GroupID  string `json:"group_id"`
}

// Grant assegna il ruolo (idempotente).
func (s *Service) Grant(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, in GrantInput) ([]AccessEntry, error) {
	sp, err := s.space(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	if (in.MemberID == "") == (in.GroupID == "") {
		return nil, engine.Required("member_id", "indica una persona o un gruppo")
	}
	var roleID string
	var perms []string
	if err := tx.QueryRow(ctx, `select id::text, permissions from roles where key = $1 or id::text = $1 limit 1`, in.Role).Scan(&roleID, &perms); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidCode("validation.role_not_found", "role", "ruolo inesistente")
		}
		return nil, err
	}
	if slices.Contains(perms, "*") {
		return nil, engine.InvalidCode("validation.knowledge.role_admin_not_assignable", "role", "il ruolo di amministratore del tenant non si assegna su uno spazio")
	}
	if !slices.Contains(perms, "element.read") && !slices.Contains(perms, "element.write") {
		return nil, engine.InvalidCode("validation.knowledge.role_no_access", "role", "il ruolo non concede né lettura né scrittura degli elementi")
	}
	var ok bool
	if in.MemberID != "" {
		err = tx.QueryRow(ctx, `select exists (select 1 from members where id::text = $1 and status = 'active')`, in.MemberID).Scan(&ok)
	} else {
		err = tx.QueryRow(ctx, `select exists (select 1 from groups where id::text = $1)`, in.GroupID).Scan(&ok)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.NotFound("member_id", "", "persona o gruppo inesistente")
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from role_assignments where space_id = $1 and role_id = $2
		and (member_id::text = $3 or group_id::text = $4))`, sp.ID, roleID, in.MemberID, in.GroupID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, member_id, group_id, space_id)
			values (app_tenant(), $1, $2, $3, $4, $5)`, id, roleID, nullable(in.MemberID), nullable(in.GroupID), sp.ID); err != nil {
			return nil, err
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "kb.space.grant", EntityType: "space", EntityID: sp.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"assignment_id": id, "role_id": roleID, "member_id": in.MemberID, "group_id": in.GroupID}}); err != nil {
			return nil, err
		}
	}
	return s.Access(ctx, tx, actor, key)
}

// Revoke toglie un ruolo assegnato sullo spazio.
func (s *Service) Revoke(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, assignmentID string) ([]AccessEntry, error) {
	sp, err := s.space(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	tag, err := tx.Exec(ctx, `delete from role_assignments where id::text = $1 and space_id = $2`, assignmentID, sp.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.ErrNotFound
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "kb.space.revoke", EntityType: "space", EntityID: sp.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"assignment_id": assignmentID}}); err != nil {
		return nil, err
	}
	return s.Access(ctx, tx, actor, key)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
