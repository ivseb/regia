package admin

// Spiegazione dei permessi effettivi di un membro: da quale ruolo, assegnato a quale gruppo
// e attraverso quale catena di gruppi arriva ogni permesso (piano, passo 15).

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

// AccessGrant è un ruolo che arriva al membro, con la catena che lo porta fin lì.
type AccessGrant struct {
	RoleID      string   `json:"role_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	// Scope: "tenant" oppure "space" (Space = nome dello spazio).
	Scope string  `json:"scope"`
	Space *string `json:"space"`
	// Source: "direct" (assegnato alla persona) | "group" (assegnato a un gruppo di cui è membro)
	// | "inherited" (assegnato a un gruppo antenato di un suo gruppo).
	Source string `json:"source"`
	// Chain: dal gruppo di cui la persona è membro fino al gruppo a cui è assegnato il ruolo.
	Chain []string `json:"chain"`
}

// AccessPermission è un permesso con le fonti che lo concedono (indici in Grants).
type AccessPermission struct {
	Permission string  `json:"permission"`
	Space      *string `json:"space"` // null = a livello di tenant
	From       []int   `json:"from"`
}

// AccessExplain è la risposta di GET /admin/members/{id}/access.
type AccessExplain struct {
	Inheritance bool               `json:"inheritance"`
	Groups      []AccessGroup      `json:"groups"`
	Grants      []AccessGrant      `json:"grants"`
	Permissions []AccessPermission `json:"permissions"`
}

// AccessGroup è un gruppo che conta per il membro: diretto o ereditato dall'alto.
type AccessGroup struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Direct bool     `json:"direct"`
	Chain  []string `json:"chain"`
}

type grp struct {
	id, name string
	parent   *string
}

// ExplainAccess calcola i permessi effettivi del membro e la loro provenienza.
func (s *Service) ExplainAccess(ctx context.Context, tenantID, memberID string) (*AccessExplain, error) {
	if !ValidID(memberID) {
		return nil, ErrNotFound
	}
	out := &AccessExplain{Inheritance: true, Groups: []AccessGroup{}, Grants: []AccessGrant{}, Permissions: []AccessPermission{}}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id = $1)", memberID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		_ = tx.QueryRow(ctx, "select group_inheritance from tenant_security").Scan(&out.Inheritance)
		rows, err := tx.Query(ctx, "select id, name, parent_id from groups")
		if err != nil {
			return err
		}
		groups := map[string]grp{}
		for rows.Next() {
			var g grp
			if err := rows.Scan(&g.id, &g.name, &g.parent); err != nil {
				rows.Close()
				return err
			}
			groups[g.id] = g
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, "select group_id from group_members where member_id = $1", memberID)
		if err != nil {
			return err
		}
		direct, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		// chains[g] = catena più corta dal gruppo diretto al gruppo g (nomi).
		chains := map[string][]string{}
		var order []string
		for _, d := range direct {
			if _, ok := groups[d]; ok {
				chains[d] = []string{groups[d].name}
				order = append(order, d)
			}
		}
		if out.Inheritance {
			for _, d := range direct {
				chain := []string{groups[d].name}
				cur := groups[d]
				for hops := 0; cur.parent != nil && hops < 50; hops++ {
					p, ok := groups[*cur.parent]
					if !ok {
						break
					}
					chain = append(append([]string{}, chain...), p.name)
					if old, seen := chains[p.id]; !seen {
						chains[p.id] = chain
						order = append(order, p.id)
					} else if len(chain) < len(old) {
						chains[p.id] = chain
					}
					cur = p
				}
			}
		}
		isDirect := map[string]bool{}
		for _, d := range direct {
			isDirect[d] = true
		}
		for _, id := range order {
			out.Groups = append(out.Groups, AccessGroup{ID: id, Name: groups[id].name, Direct: isDirect[id], Chain: chains[id]})
		}
		sort.SliceStable(out.Groups, func(i, j int) bool {
			if out.Groups[i].Direct != out.Groups[j].Direct {
				return out.Groups[i].Direct
			}
			return out.Groups[i].Name < out.Groups[j].Name
		})

		rows, err = tx.Query(ctx, `select r.id, r.name, r.permissions, ra.group_id, ra.member_id is not null, sp.name
			from role_assignments ra
			join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			left join spaces sp on sp.tenant_id = ra.tenant_id and sp.id = ra.space_id
			where ra.member_id = $1 or ra.group_id is not null
			order by r.name`, memberID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g AccessGrant
			var gid *string
			var isMember bool
			if err := rows.Scan(&g.RoleID, &g.Role, &g.Permissions, &gid, &isMember, &g.Space); err != nil {
				return err
			}
			g.Scope = "tenant"
			if g.Space != nil {
				g.Scope = "space"
			}
			switch {
			case isMember:
				g.Source, g.Chain = "direct", []string{}
			case gid != nil && chains[*gid] != nil:
				g.Chain = chains[*gid]
				g.Source = "group"
				if !isDirect[*gid] {
					g.Source = "inherited"
				}
			default:
				continue // gruppo a cui la persona non appartiene
			}
			out.Grants = append(out.Grants, g)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	// Il permesso vale per ambito: un ruolo di spazio non concede il permesso del tenant.
	type key struct{ perm, space string }
	byPerm := map[key][]int{}
	for i, g := range out.Grants {
		for _, p := range g.Permissions {
			k := key{perm: p}
			if g.Space != nil {
				k.space = *g.Space
			}
			byPerm[k] = append(byPerm[k], i)
		}
	}
	keys := make([]key, 0, len(byPerm))
	for k := range byPerm {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].space != keys[j].space {
			return keys[i].space < keys[j].space
		}
		return keys[i].perm < keys[j].perm
	})
	for _, k := range keys {
		ap := AccessPermission{Permission: k.perm, From: byPerm[k]}
		if k.space != "" {
			sp := k.space
			ap.Space = &sp
		}
		out.Permissions = append(out.Permissions, ap)
	}
	return out, nil
}
