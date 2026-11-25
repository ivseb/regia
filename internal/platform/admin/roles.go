package admin

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Ruoli di tenant. Quelli di sistema (key valorizzata) non si modificano né si
// cancellano: si duplicano in un ruolo personalizzato e si cambia la copia. Così un
// aggiornamento del prodotto può ancora contare sul loro significato.

// Holder è un titolare di un ruolo (membro o gruppo) con l'id dell'assegnazione.
type Holder struct {
	AssignmentID string `json:"assignment_id"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status,omitempty"`
}

// Role è un ruolo di tenant con i titolari.
type Role struct {
	ID          string    `json:"id"`
	Key         *string   `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	System      bool      `json:"system"`
	Admin       bool      `json:"admin"`
	Members     []Holder  `json:"members"`
	Groups      []Holder  `json:"groups"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListRoles elenca i ruoli, prima quelli di sistema.
func (s *Service) ListRoles(ctx context.Context, tenantID string) ([]Role, error) {
	out := []Role{}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select r.id, r.key, r.name, r.description, r.permissions, r.created_at,
			  coalesce((select jsonb_agg(jsonb_build_object('assignment_id', ra.id, 'id', m.id, 'name', m.display_name, 'status', m.status) order by lower(m.display_name))
			            from role_assignments ra join members m on m.tenant_id = ra.tenant_id and m.id = ra.member_id
			            where ra.role_id = r.id and ra.space_id is null and m.status <> 'anonymized'), '[]'),
			  coalesce((select jsonb_agg(jsonb_build_object('assignment_id', ra.id, 'id', g.id, 'name', g.name) order by lower(g.name))
			            from role_assignments ra join groups g on g.tenant_id = ra.tenant_id and g.id = ra.group_id
			            where ra.role_id = r.id and ra.space_id is null), '[]')
			from roles r
			order by r.key is null, lower(r.name)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Role
			var members, groups []byte
			if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.Description, &r.Permissions, &r.CreatedAt, &members, &groups); err != nil {
				return err
			}
			_ = json.Unmarshal(members, &r.Members)
			_ = json.Unmarshal(groups, &r.Groups)
			r.System = r.Key != nil
			r.Admin = slices.Contains(r.Permissions, rbac.All) || slices.Contains(r.Permissions, rbac.TenantAdmin)
			if r.Permissions == nil {
				r.Permissions = []string{}
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// RoleInput crea o modifica un ruolo personalizzato.
type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	// CopyOf: ruolo (anche di sistema) da cui nasce la copia, solo per l'audit.
	CopyOf string `json:"copy_of,omitempty"`
}

func (in *RoleInput) clean() error {
	in.Name = cleanName(in.Name, 80)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" {
		return invalid("name", "serve un nome")
	}
	if len([]rune(in.Description)) > 500 {
		return invalid("description", "descrizione troppo lunga (al massimo 500 caratteri)")
	}
	perms := []string{}
	for _, p := range in.Permissions {
		p = strings.TrimSpace(p)
		if p == rbac.All {
			return invalid("permissions", "il permesso * resta del solo ruolo di sistema «Amministratore del tenant»: duplicalo o scegli i permessi")
		}
		if !rbac.Assignable(p) {
			return invalid("permissions", "permesso sconosciuto: "+p)
		}
		if !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	if len(perms) == 0 {
		return invalid("permissions", "scegli almeno un permesso")
	}
	slices.Sort(perms)
	in.Permissions = perms
	return nil
}

// CreateRole crea un ruolo personalizzato.
func (s *Service) CreateRole(ctx context.Context, a Actor, in RoleInput) (string, error) {
	if err := in.clean(); err != nil {
		return "", err
	}
	if in.CopyOf != "" && !ValidID(in.CopyOf) {
		return "", invalid("copy_of", "ruolo di partenza inesistente")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "insert into roles (tenant_id, id, name, description, permissions) values (app_tenant(), $1, $2, $3, $4)",
			id, in.Name, in.Description, in.Permissions); err != nil {
			if isUnique(err) {
				return invalid("name", "esiste già un ruolo con questo nome")
			}
			return err
		}
		payload := map[string]any{"permissions": in.Permissions}
		if in.CopyOf != "" {
			payload["copy_of"] = in.CopyOf
		}
		return a.audit(ctx, tx, "role.create", "role", id, payload)
	})
	return id, err
}

// UpdateRole modifica un ruolo personalizzato.
func (s *Service) UpdateRole(ctx context.Context, a Actor, id string, in RoleInput) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	if err := in.clean(); err != nil {
		return err
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var key *string
		var before []string
		if err := tx.QueryRow(ctx, "select key, permissions from roles where id = $1 for update", id).Scan(&key, &before); err != nil {
			return notFound(err)
		}
		if key != nil {
			return conflict("role.system", "i ruoli di sistema non si modificano: duplicalo e cambia la copia")
		}
		if _, err := tx.Exec(ctx, "update roles set name = $2, description = $3, permissions = $4 where id = $1",
			id, in.Name, in.Description, in.Permissions); err != nil {
			if isUnique(err) {
				return invalid("name", "esiste già un ruolo con questo nome")
			}
			return err
		}
		if err := ensureAdmin(ctx, tx); err != nil {
			return err
		}
		return a.audit(ctx, tx, "role.update", "role", id, map[string]any{"permissions": in.Permissions, "before": before})
	})
}

// DeleteRole cancella un ruolo personalizzato e le sue assegnazioni.
func (s *Service) DeleteRole(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var key *string
		var name string
		if err := tx.QueryRow(ctx, "select key, name from roles where id = $1", id).Scan(&key, &name); err != nil {
			return notFound(err)
		}
		if key != nil {
			return conflict("role.system", "i ruoli di sistema non si cancellano")
		}
		var holders int
		if err := tx.QueryRow(ctx, "select count(*) from role_assignments where role_id = $1", id).Scan(&holders); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from roles where id = $1", id); err != nil {
			return err
		}
		if err := ensureAdmin(ctx, tx); err != nil {
			return err
		}
		return a.audit(ctx, tx, "role.delete", "role", id, map[string]any{"assignments_removed": holders})
	})
}

// Assign assegna un ruolo (a livello di tenant) a un membro o a un gruppo.
func (s *Service) Assign(ctx context.Context, a Actor, roleID, memberID, groupID string) (string, error) {
	if !ValidID(roleID) {
		return "", ErrNotFound
	}
	if (memberID == "") == (groupID == "") {
		return "", invalid("member_id", "indica un membro oppure un gruppo")
	}
	if (memberID != "" && !ValidID(memberID)) || (groupID != "" && !ValidID(groupID)) {
		return "", invalid("member_id", "membro o gruppo inesistente")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from role_assignments where role_id = $1 and space_id is null
			and member_id is not distinct from $2::uuid and group_id is not distinct from $3::uuid)`,
			roleID, nullable(memberID), nullable(groupID)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return conflict("role.already_assigned", "il ruolo è già assegnato")
		}
		if memberID != "" {
			var status string
			if err := tx.QueryRow(ctx, "select status from members where id = $1", memberID).Scan(&status); err != nil {
				return invalid("member_id", "membro inesistente")
			}
			if status == "anonymized" {
				return invalid("member_id", "membro inesistente")
			}
		}
		if _, err := tx.Exec(ctx, "insert into role_assignments (tenant_id, id, role_id, member_id, group_id) values (app_tenant(), $1, $2, $3, $4)",
			id, roleID, nullable(memberID), nullable(groupID)); err != nil {
			if isFK(err) {
				return invalid("role_id", "ruolo, membro o gruppo inesistente")
			}
			return err
		}
		return a.audit(ctx, tx, "role.assign", "role", roleID, map[string]any{"assignment": id, "member": nullable(memberID), "group": nullable(groupID)})
	})
	return id, err
}

// Unassign toglie un'assegnazione di ruolo.
func (s *Service) Unassign(ctx context.Context, a Actor, assignmentID string) error {
	if !ValidID(assignmentID) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var roleID string
		var memberID, groupID *string
		if err := tx.QueryRow(ctx, "delete from role_assignments where id = $1 returning role_id, member_id, group_id", assignmentID).
			Scan(&roleID, &memberID, &groupID); err != nil {
			return notFound(err)
		}
		if err := ensureAdmin(ctx, tx); err != nil {
			return err
		}
		return a.audit(ctx, tx, "role.unassign", "role", roleID, map[string]any{"assignment": assignmentID, "member": memberID, "group": groupID})
	})
}
