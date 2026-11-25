package admin

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Gruppi: insiemi di membri a cui dare ruoli e ruoli sugli oggetti. La gerarchia
// (parent_id) organizza l'elenco; i permessi non si ereditano dal gruppo padre.
// "Tutti i membri" è di sistema: lo popola l'app e non si modifica.

// GroupUsage dice dove il gruppo è usato: le regole dei processi lo citano per nome,
// i team valutatori lo hanno come gruppo, gli elementi e le approvazioni in corso lo
// hanno come titolare o assegnatario.
type GroupUsage struct {
	Processes   []string `json:"processes"`
	ReviewTeams []string `json:"review_teams"`
	Bodies      []string `json:"bodies"`
	Elements    int      `json:"elements"`
	Approvals   int      `json:"approvals"`
}

func (u GroupUsage) blocking() []string {
	var out []string
	if len(u.Processes) > 0 {
		out = append(out, "processi: "+strings.Join(u.Processes, ", "))
	}
	if len(u.ReviewTeams) > 0 {
		out = append(out, "team valutatori: "+strings.Join(u.ReviewTeams, ", "))
	}
	if len(u.Bodies) > 0 {
		out = append(out, "organi: "+strings.Join(u.Bodies, ", "))
	}
	return out
}

// Group è un gruppo con membri, ruoli e utilizzi.
type Group struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	ParentID *string    `json:"parent_id"`
	System   bool       `json:"system"`
	Members  []Holder   `json:"members"`
	Roles    []Holder   `json:"roles"`
	Usage    GroupUsage `json:"usage"`
	Created  time.Time  `json:"created_at"`
}

// usageSQL calcola gli utilizzi del gruppo g. Le regole si cercano nelle versioni
// pubblicate e nelle bozze dei processi non archiviati (azioni con "group": nome).
// Gli organi di governance non hanno oggi un gruppo collegato: l'elenco resta vuoto
// finché lo schema non lo prevede.
const usageSQL = `
	coalesce((select jsonb_agg(distinct p.name) from process_definitions p
	          join process_versions v on v.tenant_id = p.tenant_id and v.process_id = p.id and v.status in ('published', 'draft')
	          where not p.archived and exists (select 1 from jsonb_path_query(v.definition, 'lax $.**.group') x
	                                           where jsonb_typeof(x) = 'string' and lower(x #>> '{}') = lower(g.name))), '[]'),
	coalesce((select jsonb_agg(rt.name order by rt.name) from review_teams rt where rt.group_id = g.id and not rt.archived), '[]'),
	'[]'::jsonb,
	(select count(distinct sh.element_id) from stakeholders sh where sh.group_id = g.id),
	(select count(*) from approval_step_assignees asg join approval_steps st on st.tenant_id = asg.tenant_id and st.id = asg.step_id
	 where asg.group_id = g.id and st.decision = 'pending')`

func scanUsage(procs, teams, bodies []byte, u *GroupUsage) {
	_ = json.Unmarshal(procs, &u.Processes)
	_ = json.Unmarshal(teams, &u.ReviewTeams)
	_ = json.Unmarshal(bodies, &u.Bodies)
	if u.Processes == nil {
		u.Processes = []string{}
	}
	if u.ReviewTeams == nil {
		u.ReviewTeams = []string{}
	}
	if u.Bodies == nil {
		u.Bodies = []string{}
	}
}

// ListGroups elenca i gruppi con membri, ruoli e utilizzi.
func (s *Service) ListGroups(ctx context.Context, tenantID string) ([]Group, error) {
	out := []Group{}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select g.id, g.name, g.parent_id, g.system_kind is not null, g.created_at,
			  coalesce((select jsonb_agg(jsonb_build_object('id', m.id, 'name', m.display_name, 'status', m.status, 'assignment_id', '') order by lower(m.display_name))
			            from group_members gm join members m on m.tenant_id = gm.tenant_id and m.id = gm.member_id
			            where gm.group_id = g.id and m.status <> 'anonymized'), '[]'),
			  coalesce((select jsonb_agg(jsonb_build_object('assignment_id', ra.id, 'id', r.id, 'name', r.name) order by lower(r.name))
			            from role_assignments ra join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
			            where ra.group_id = g.id and ra.space_id is null), '[]'),
			  `+usageSQL+`
			from groups g
			order by g.system_kind is null, lower(g.name)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g Group
			var members, roles, procs, teams, bodies []byte
			if err := rows.Scan(&g.ID, &g.Name, &g.ParentID, &g.System, &g.Created, &members, &roles,
				&procs, &teams, &bodies, &g.Usage.Elements, &g.Usage.Approvals); err != nil {
				return err
			}
			_ = json.Unmarshal(members, &g.Members)
			_ = json.Unmarshal(roles, &g.Roles)
			scanUsage(procs, teams, bodies, &g.Usage)
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}

func groupUsage(ctx context.Context, tx pgx.Tx, id string) (name string, system bool, u GroupUsage, err error) {
	var procs, teams, bodies []byte
	err = tx.QueryRow(ctx, `select g.name, g.system_kind is not null, `+usageSQL+` from groups g where g.id = $1`, id).
		Scan(&name, &system, &procs, &teams, &bodies, &u.Elements, &u.Approvals)
	if err != nil {
		return "", false, u, notFound(err)
	}
	scanUsage(procs, teams, bodies, &u)
	return name, system, u, nil
}

// GroupInput crea o modifica un gruppo.
type GroupInput struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
}

func (s *Service) checkParent(ctx context.Context, tx pgx.Tx, id string, parent *string) error {
	if parent == nil || *parent == "" {
		return nil
	}
	if !ValidID(*parent) {
		return invalid("parent_id", "gruppo padre inesistente")
	}
	if *parent == id {
		return invalid("parent_id", "un gruppo non può stare sotto sé stesso")
	}
	// Niente cicli: risalendo dal padre non si deve incontrare il gruppo.
	var cycle, found bool
	if err := tx.QueryRow(ctx, `with recursive up as (
			select id, parent_id, 1 as depth from groups where id = $1
			union all
			select g.id, g.parent_id, up.depth + 1 from groups g join up on g.id = up.parent_id where up.depth < 50)
		select exists (select 1 from up where id = $2), exists (select 1 from up)`, *parent, nullable(id)).Scan(&cycle, &found); err != nil {
		return err
	}
	if !found {
		return invalid("parent_id", "gruppo padre inesistente")
	}
	if cycle {
		return invalid("parent_id", "il gruppo padre scelto sta già sotto questo gruppo")
	}
	return nil
}

// CreateGroup crea un gruppo.
func (s *Service) CreateGroup(ctx context.Context, a Actor, in GroupInput) (string, error) {
	in.Name = cleanName(in.Name, 120)
	if in.Name == "" {
		return "", invalid("name", "serve un nome")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		if err := s.checkParent(ctx, tx, id, in.ParentID); err != nil {
			return err
		}
		parent := in.ParentID
		if parent != nil && *parent == "" {
			parent = nil
		}
		if _, err := tx.Exec(ctx, "insert into groups (tenant_id, id, name, parent_id) values (app_tenant(), $1, $2, $3)", id, in.Name, parent); err != nil {
			if isUnique(err) {
				return invalid("name", "esiste già un gruppo con questo nome")
			}
			return err
		}
		return a.audit(ctx, tx, "group.create", "group", id, map[string]any{"parent": parent})
	})
	return id, err
}

// UpdateGroup rinomina o sposta un gruppo. Un gruppo citato per nome dalle regole dei
// processi non si rinomina: le regole smetterebbero di trovarlo.
func (s *Service) UpdateGroup(ctx context.Context, a Actor, id string, in GroupInput) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	in.Name = cleanName(in.Name, 120)
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		name, system, usage, err := groupUsage(ctx, tx, id)
		if err != nil {
			return err
		}
		if system {
			return conflict("group.system", "il gruppo «Tutti i membri» è di sistema e non si modifica")
		}
		payload := map[string]any{}
		if in.Name != "" && in.Name != name {
			if len(usage.Processes) > 0 && !strings.EqualFold(in.Name, name) {
				return conflict("group.in_use", "il gruppo è citato per nome nelle regole dei processi ("+strings.Join(usage.Processes, ", ")+
					"): aggiorna prima le regole, poi rinominalo")
			}
			if _, err := tx.Exec(ctx, "update groups set name = $2 where id = $1", id, in.Name); err != nil {
				if isUnique(err) {
					return invalid("name", "esiste già un gruppo con questo nome")
				}
				return err
			}
			payload["renamed"] = true
		}
		if err := s.checkParent(ctx, tx, id, in.ParentID); err != nil {
			return err
		}
		parent := in.ParentID
		if parent != nil && *parent == "" {
			parent = nil
		}
		tag, err := tx.Exec(ctx, "update groups set parent_id = $2 where id = $1 and parent_id is distinct from $2::uuid", id, parent)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			payload["parent"] = parent
		}
		if len(payload) == 0 {
			return nil
		}
		return a.audit(ctx, tx, "group.update", "group", id, payload)
	})
}

// DeleteGroup cancella un gruppo non di sistema e non usato.
func (s *Service) DeleteGroup(ctx context.Context, a Actor, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		_, system, usage, err := groupUsage(ctx, tx, id)
		if err != nil {
			return err
		}
		if system {
			return conflict("group.system", "il gruppo «Tutti i membri» è di sistema e non si cancella")
		}
		why := usage.blocking()
		if usage.Elements > 0 {
			why = append(why, "ruoli su elementi")
		}
		if usage.Approvals > 0 {
			why = append(why, "approvazioni in corso")
		}
		if len(why) > 0 {
			return conflict("group.in_use", "il gruppo è in uso ("+strings.Join(why, "; ")+"): toglilo prima da lì")
		}
		var members int
		if err := tx.QueryRow(ctx, "select count(*) from group_members where group_id = $1", id).Scan(&members); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from groups where id = $1", id); err != nil {
			return err
		}
		if err := ensureAdmin(ctx, tx); err != nil {
			return err
		}
		return a.audit(ctx, tx, "group.delete", "group", id, map[string]any{"members": members})
	})
}

// AddGroupMember aggiunge un membro al gruppo.
func (s *Service) AddGroupMember(ctx context.Context, a Actor, groupID, memberID string) error {
	if !ValidID(groupID) || !ValidID(memberID) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var system bool
		if err := tx.QueryRow(ctx, "select system_kind is not null from groups where id = $1", groupID).Scan(&system); err != nil {
			return notFound(err)
		}
		if system {
			return conflict("group.system", "«Tutti i membri» comprende già ogni membro")
		}
		tag, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select app_tenant(), $1, m.id from members m where m.id = $2 and m.status <> 'anonymized'
			on conflict do nothing`, groupID, memberID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, "select exists (select 1 from group_members where group_id = $1 and member_id = $2)", groupID, memberID).Scan(&exists)
			if exists {
				return nil
			}
			return invalid("member_id", "membro inesistente")
		}
		return a.audit(ctx, tx, "group.member_add", "group", groupID, map[string]any{"member": memberID})
	})
}

// RemoveGroupMember toglie un membro dal gruppo.
func (s *Service) RemoveGroupMember(ctx context.Context, a Actor, groupID, memberID string) error {
	if !ValidID(groupID) || !ValidID(memberID) {
		return ErrNotFound
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		var system bool
		if err := tx.QueryRow(ctx, "select system_kind is not null from groups where id = $1", groupID).Scan(&system); err != nil {
			return notFound(err)
		}
		if system {
			return conflict("group.system", "da «Tutti i membri» non si toglie nessuno: si sospende il membro")
		}
		tag, err := tx.Exec(ctx, "delete from group_members where group_id = $1 and member_id = $2", groupID, memberID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := ensureAdmin(ctx, tx); err != nil {
			return err
		}
		return a.audit(ctx, tx, "group.member_remove", "group", groupID, map[string]any{"member": memberID})
	})
}
