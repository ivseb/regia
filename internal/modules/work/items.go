package work

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// ItemInput crea un elemento di lavoro in uno spazio di team.
type ItemInput struct {
	Type       string         `json:"type"` // epic | story | task | bug
	Title      string         `json:"title"`
	Body       string         `json:"body_text"`
	ParentID   string         `json:"parent"` // chiave o id
	AssigneeID string         `json:"assignee_id"`
	Sprint     string         `json:"sprint"` // chiave o id dello sprint; vuoto = backlog
	Fields     map[string]any `json:"fields"`
	Top        bool           `json:"top"` // in cima al backlog invece che in fondo
}

// CreateItem crea un elemento di lavoro (epic, story, task, bug o un tipo del modulo work) nello spazio, in fondo (o in cima) all'ordine.
func (s *Service) CreateItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in ItemInput) (*engine.Element, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canWork(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	kinds, err := workItemTypes(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(kinds, in.Type) {
		return nil, engine.InvalidParams("validation.work.item_type_invalid", "type", map[string]any{"types": strings.Join(kinds, ", ")}, "tipo non ammesso: %s", strings.Join(kinds, ", "))
	}
	allowed := parentTypesOf(in.Type)
	parentID := ""
	if strings.TrimSpace(in.ParentID) != "" {
		p, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(in.ParentID))
		if errors.Is(err, engine.ErrNotFound) {
			return nil, engine.NotFound("parent", "element", "elemento genitore inesistente")
		}
		if err != nil {
			return nil, err
		}
		if !slices.Contains(allowed, p.TypeKey) || p.SpaceID != sp.ID {
			return nil, engine.InvalidParams("validation.work.parent_type", "parent", map[string]any{"type": in.Type, "parents": strings.Join(allowed, " o ")}, "un elemento %s sta sotto %s dello stesso spazio", in.Type, strings.Join(allowed, " o "))
		}
		parentID = p.ID
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: sp.Key, TypeKey: in.Type, Title: in.Title,
		BodyText: in.Body, Fields: in.Fields, AssigneeID: in.AssigneeID, ParentID: parentID})
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "assignee_id", "membro inesistente")
		}
		return nil, err
	}
	// fillRanks mette il nuovo elemento in fondo; con Top va in cima.
	if err := fillRanks(ctx, tx, sp.ID); err != nil {
		return nil, err
	}
	if in.Top {
		var first *string
		if err := tx.QueryRow(ctx, `select min(rank collate "C") from elements where space_id = $1 and deleted_at is null and id <> $2`, sp.ID, el.ID).Scan(&first); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "update elements set rank = $2 where id = $1", el.ID, rankBetween("", deref(first))); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(in.Sprint) != "" {
		if err := s.planIn(ctx, tx, actor, el, in.Sprint); err != nil {
			return nil, err
		}
	}
	return s.eng.Get(ctx, tx, actor, el.ID)
}

// workItem carica un elemento di lavoro (epic, story, task, bug) verificando che l'attore
// possa lavorarci.
func (s *Service) workItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, types ...string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if len(types) == 0 {
		types = []string{anyWork}
	}
	if ok, err := matchesTypes(ctx, tx, el.TypeID, el.TypeKey, types); err != nil {
		return nil, err
	} else if !ok {
		names := expandTypes(ctx, tx, types)
		return nil, engine.InvalidParams("validation.work.element_type_only", "element", map[string]any{"types": names}, "operazione possibile solo su %s", names)
	}
	if ok, err := canWork(ctx, tx, actor, el.SpaceID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	return el, nil
}

// assignable: gli elementi a cui si può dare un responsabile persona. Oltre al lavoro dei team, le richieste
// («Prendi in carico», ondata 2).
var assignable = []string{anyWork, RequestType}

// SetAssignee cambia l'assegnatario (vuoto = nessuno). Version 0 = nessun controllo.
func (s *Service) SetAssignee(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, memberID string, version int) (*engine.Element, error) {
	el, err := s.workItem(ctx, tx, actor, ref, assignable...)
	if err != nil {
		return nil, err
	}
	return el, s.setAssignee(ctx, tx, actor, el, memberID, version)
}

func (s *Service) setAssignee(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, memberID string, version int) error {
	if version != 0 && version != el.Version {
		return engine.ErrConflict
	}
	if deref(el.AssigneeID) == memberID {
		return nil
	}
	if memberID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", memberID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.InvalidCode("validation.member_not_found", "member_id", "membro inesistente o non attivo")
		}
	}
	if _, err := tx.Exec(ctx, `update elements set assignee_id = $2, version = version + 1, updated_by = $3 where id = $1`,
		el.ID, nullable(memberID), nullable(actor.MemberID)); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "assignee", memberRef(ctx, tx, memberID), memberRef(ctx, tx, deref(el.AssigneeID))); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "element.assign", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"assignee_id": memberID}})
}

// setParent cambia il genitore (swimlane per epic sulla board), con la stessa gerarchia.
func (s *Service) setParent(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, parentID string) error {
	if deref(el.ParentID) == parentID {
		return nil
	}
	if parentID != "" {
		p, err := s.eng.Get(ctx, tx, actor, parentID)
		if err != nil {
			return engine.NotFound("lane", "", "epic inesistente")
		}
		if !slices.Contains(parentTypesOf(el.TypeKey), p.TypeKey) || p.SpaceID != el.SpaceID {
			return engine.InvalidParams("validation.work.lane_parent", "lane", map[string]any{"type": el.TypeKey, "parent_type": p.TypeKey, "parent": p.Key}, "un elemento %s non può stare sotto %s %s", el.TypeKey, p.TypeKey, p.Key)
		}
	}
	if _, err := tx.Exec(ctx, `update elements set parent_id = $2, version = version + 1, updated_by = $3 where id = $1`,
		el.ID, nullable(parentID), nullable(actor.MemberID)); err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "parent", elementRef(ctx, tx, parentID), elementRef(ctx, tx, deref(el.ParentID)))
}

// PlanInSprint pianifica l'elemento in uno sprint dello stesso spazio (vuoto = backlog) e,
// se indicati, lo mette tra after e before.
func (s *Service) PlanInSprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, sprint, after, before string) (*engine.Element, error) {
	el, err := s.workItem(ctx, tx, actor, ref, anyItem)
	if err != nil {
		return nil, err
	}
	if err := s.planIn(ctx, tx, actor, el, sprint); err != nil {
		return nil, err
	}
	if after != "" || before != "" {
		if _, err := placeBetween(ctx, tx, el.SpaceID, el.ID, after, before); err != nil {
			return nil, err
		}
	}
	return s.eng.Get(ctx, tx, actor, el.ID)
}

// planIn sostituisce lo sprint dell'elemento (al massimo uno). Uno sprint chiuso o annullato
// non riceve elementi nuovi.
func (s *Service) planIn(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, sprint string) error {
	relID, err := relationTypeID(ctx, tx, RelPlannedIn)
	if err != nil {
		return err
	}
	var current *string
	if err := tx.QueryRow(ctx, "select target_id::text from relations where source_id = $1 and type_id = $2 limit 1", el.ID, relID).Scan(&current); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	target := ""
	if strings.TrimSpace(sprint) != "" {
		sp, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(sprint))
		if errors.Is(err, engine.ErrNotFound) {
			return engine.NotFound("sprint", "sprint", "sprint inesistente")
		}
		if err != nil {
			return err
		}
		if sp.TypeKey != TypeSprint || sp.SpaceID != el.SpaceID {
			return engine.InvalidParams("validation.work.sprint_other_space", "sprint", map[string]any{"sprint": sp.Key, "space": el.SpaceKey}, "%s non è uno sprint dello spazio %s", sp.Key, el.SpaceKey)
		}
		if sp.StateCategory == "done" || sp.StateCategory == "cancelled" {
			return engine.InvalidParams("validation.work.sprint_not_open", "sprint", map[string]any{"sprint": sp.Key, "state": strings.ToLower(sp.State)}, "lo sprint %s è %s: non riceve elementi", sp.Key, strings.ToLower(sp.State))
		}
		target = sp.ID
	}
	if deref(current) == target {
		return nil
	}
	if _, err := tx.Exec(ctx, "delete from relations where source_id = $1 and type_id = $2", el.ID, relID); err != nil {
		return err
	}
	if target != "" {
		if _, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5)`, db.NewID(), relID, el.ID, target, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	return engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "sprint", elementRef(ctx, tx, target), elementRef(ctx, tx, deref(current)))
}

// ---------------------------------------------------------------------------
// Fatti per i gate dei processi

// itemFacts: assegnatario, figli (aperti), sprint, ore registrate; per l'epic l'avanzamento.
func (s *Service) itemFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var assignee *string
	var children, open int
	var sprint *string
	var minutes int64
	err := tx.QueryRow(ctx, `
		select (select display_name from members where id = $2),
		       (select count(*) from elements c left join options o on o.tenant_id = c.tenant_id and o.id = c.state_id
		         where c.parent_id = $1 and c.deleted_at is null and coalesce(o.category, '') <> 'cancelled')::int,
		       (select count(*) from elements c left join options o on o.tenant_id = c.tenant_id and o.id = c.state_id
		         where c.parent_id = $1 and c.deleted_at is null and coalesce(o.category, '') not in ('done', 'cancelled'))::int,
		       (select sp.key || '-' || e.number from relations r
		          join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		          join elements e on e.tenant_id = r.tenant_id and e.id = r.target_id
		          join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		         where r.source_id = $1 limit 1),
		       coalesce((select sum(minutes) from work_logs where element_id = $1), 0)`,
		el.ID, el.AssigneeID).Scan(&assignee, &children, &open, &sprint, &minutes)
	if err != nil {
		return err
	}
	f.Set("assegnatario", assignee)
	f.Set("figli", children)
	f.Set("figli_aperti", open)
	f.Set("sprint", sprint)
	f.Set("ore_registrate", float64(minutes)/60)
	if el.TypeKey == TypeEpic {
		p, err := progressOf(ctx, tx, []string{el.ID})
		if err != nil {
			return err
		}
		f.Set("avanzamento", p.Percent)
	}
	return nil
}

// sprintFacts: date, altri sprint attivi nello spazio, elementi pianificati.
func (s *Service) sprintFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	f.Set("inizio", el.StartAt)
	f.Set("fine", el.DueAt)
	var active, items, open int
	err := tx.QueryRow(ctx, `
		select (select count(*) from elements e
		          join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
		          join options o on o.tenant_id = e.tenant_id and o.id = e.state_id and o.category = 'in_progress'
		         where e.space_id = $2 and e.id <> $1 and e.deleted_at is null)::int,
		       (select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		         where r.target_id = $1)::int,
		       (select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		          join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id
		          left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		         where r.target_id = $1 and coalesce(o.category, '') not in ('done', 'cancelled'))::int`,
		el.ID, el.SpaceID).Scan(&active, &items, &open)
	if err != nil {
		return err
	}
	f.Set("sprint_attivi", active)
	f.Set("elementi", items)
	f.Set("elementi_aperti", open)
	return nil
}

// memberRef e elementRef danno id e nome (o chiave e titolo) per la storia; nil se vuoto.
func memberRef(ctx context.Context, tx pgx.Tx, id string) any {
	if id == "" {
		return nil
	}
	var name string
	_ = tx.QueryRow(ctx, "select display_name from members where id::text = $1", id).Scan(&name)
	return map[string]any{"id": id, "name": name}
}

func elementRef(ctx context.Context, tx pgx.Tx, id string) any {
	if id == "" {
		return nil
	}
	var key, title string
	_ = tx.QueryRow(ctx, `select s.key || '-' || e.number, e.title from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.id::text = $1`, id).Scan(&key, &title)
	return map[string]any{"id": id, "key": key, "title": title}
}
