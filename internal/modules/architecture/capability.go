package architecture

// Capability di business (replica/architettura-ea.md §2): gerarchia a più livelli con
// elements.parent_id, collegamenti "supportata da" (applicazioni) e "realizzata da"
// (asset logici). La mappa si costruisce nel browser dall'elenco piatto.

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

type CapabilityApp struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Code        *string `json:"code"`
	Criticality *string `json:"criticality"`
	DORA        bool    `json:"dora"`
	Time        *string `json:"time"`
	Team        *string `json:"team"`
	Retired     bool    `json:"retired"`
}

type CapabilityAsset struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Capability struct {
	ID            string            `json:"id"`
	Key           string            `json:"key"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Code          *string           `json:"code"`
	ParentID      *string           `json:"parent_id"`
	Level         int               `json:"level"`
	Path          []string          `json:"path"`
	Criticality   *string           `json:"criticality"`
	Position      int               `json:"position"`
	State         string            `json:"state"`
	StateCategory string            `json:"state_category"`
	OwnerUnitID   *string           `json:"owner_unit_id"`
	OwnerUnit     *string           `json:"owner_unit"`
	Apps          []CapabilityApp   `json:"applications"`
	Assets        []CapabilityAsset `json:"assets"`
}

// Capabilities restituisce tutte le capability in ordine di albero (genitore prima dei
// figli, fratelli per posizione e nome), con livello e percorso.
func (s *Service) Capabilities(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Capability, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	return capabilities(ctx, tx, "")
}

func capabilities(ctx context.Context, tx pgx.Tx, onlyID string) ([]Capability, error) {
	rows, err := tx.Query(ctx, `
		with recursive caps as (
			select e.id, e.parent_id, e.title, c.position from elements e
			join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id
			where e.deleted_at is null
		), tree(id, level, path, sort) as (
			select id, 1, array[title], array[lpad(position::text, 6, '0') || lower(title)] from caps
			where parent_id is null or parent_id not in (select id from caps)
			union all
			select c.id, t.level + 1, t.path || c.title, t.sort || (lpad(c.position::text, 6, '0') || lower(c.title))
			from caps c join tree t on c.parent_id = t.id where t.level < 20
		)
		select e.id, s.key || '-' || e.number, e.title, e.body_text, c.code, case when tree.level = 1 then null else e.parent_id end,
		       tree.level, tree.path, c.criticality, c.position, coalesce(o.name, ''), coalesce(o.category, ''), c.owner_unit_id, u.name
		from tree
		join elements e on e.id = tree.id
		join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join org_units u on u.tenant_id = c.tenant_id and u.id = c.owner_unit_id
		where $1 = '' or e.id::text = $1
		order by tree.sort`, onlyID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Capability, error) {
		var c Capability
		err := r.Scan(&c.ID, &c.Key, &c.Name, &c.Description, &c.Code, &c.ParentID, &c.Level, &c.Path, &c.Criticality, &c.Position,
			&c.State, &c.StateCategory, &c.OwnerUnitID, &c.OwnerUnit)
		c.Apps, c.Assets = []CapabilityApp{}, []CapabilityAsset{}
		return c, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	idx := map[string]int{}
	ids := make([]string, len(list))
	for i, c := range list {
		idx[c.ID], ids[i] = i, c.ID
	}
	arows, err := tx.Query(ctx, `
		select r.target_id, e.id, s.key || '-' || e.number, e.title, t.key, a.code, a.criticality, coalesce(a.supports_critical_function, false),
		       a.time_rating, tm.name, coalesce(o.category, '') = 'done'
		from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.ea_category = 'capability'
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		left join teams tm on tm.tenant_id = a.tenant_id and tm.id = a.owner_team_id
		where r.target_id = any($1::uuid[]) order by e.title`, ids)
	if err != nil {
		return nil, err
	}
	var capID, typeKey string
	var a CapabilityApp
	_, err = pgx.ForEachRow(arows, []any{&capID, &a.ID, &a.Key, &a.Name, &typeKey, &a.Code, &a.Criticality, &a.DORA, &a.Time, &a.Team, &a.Retired}, func() error {
		c := &list[idx[capID]]
		if typeKey == AppType {
			c.Apps = append(c.Apps, a)
		} else {
			c.Assets = append(c.Assets, CapabilityAsset{ID: a.ID, Key: a.Key, Name: a.Name})
		}
		return nil
	})
	return list, err
}

type CapabilityInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Code        *string `json:"code"`
	ParentID    *string `json:"parent_id"` // "" = radice
	Criticality *string `json:"criticality"`
	Position    *int    `json:"position"`
	OwnerUnitID *string `json:"owner_unit_id"`
}

func (in *CapabilityInput) validate() error {
	if in.Criticality != nil {
		c, ok := normCriticality(*in.Criticality)
		if !ok {
			return engine.InvalidValue("criticality", "valore non ammesso (bassa, media, alta, critica)")
		}
		in.Criticality = &c
	}
	if in.Code != nil {
		c := strings.TrimSpace(*in.Code)
		if len(c) > 60 {
			return engine.TooLong("code", 60, "codice troppo lungo (massimo 60 caratteri)")
		}
		in.Code = &c
	}
	if in.Position != nil && (*in.Position < 0 || *in.Position > 999999) {
		return engine.OutOfRange("position", 0, 999999, "posizione tra 0 e 999999")
	}
	return nil
}

func (s *Service) CreateCapability(ctx context.Context, tx pgx.Tx, actor engine.Actor, in CapabilityInput) (*Capability, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	id, err := s.createCapability(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	return s.capability(ctx, tx, id)
}

func (s *Service) createCapability(ctx context.Context, tx pgx.Tx, actor engine.Actor, in CapabilityInput) (string, error) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return "", engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if err := in.validate(); err != nil {
		return "", err
	}
	parent := deref(in.ParentID)
	if parent != "" {
		if err := checkParent(ctx, tx, "", parent); err != nil {
			return "", err
		}
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: CapabilityType, Title: *in.Name,
		BodyText: deref(in.Description), ParentID: parent, DeferStart: true})
	if err != nil {
		return "", err
	}
	pos := 0
	if in.Position != nil {
		pos = *in.Position
	} else if err := tx.QueryRow(ctx, `select coalesce(max(c.position) + 10, 0) from ext_capability c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.element_id
		where e.deleted_at is null and e.parent_id is not distinct from $1::uuid and e.id <> $2`, nullable(parent), el.ID).Scan(&pos); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into ext_capability (tenant_id, element_id, position) values (app_tenant(), $1, $2)`, el.ID, pos); err != nil {
		return "", err
	}
	if err := writeCapability(ctx, tx, el.ID, in); err != nil {
		return "", err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return "", err
	}
	return el.ID, nil
}

func writeCapability(ctx context.Context, tx pgx.Tx, id string, in CapabilityInput) error {
	var code any
	if in.Code != nil && *in.Code != "" {
		code = *in.Code
	}
	_, err := tx.Exec(ctx, `update ext_capability set
		code = case when $2 then $3 else code end,
		criticality = case when $4 then nullif($5, '') else criticality end,
		position = coalesce($6, position),
		owner_unit_id = case when $7 then $8::uuid else owner_unit_id end
		where element_id = $1`,
		id, in.Code != nil, code, in.Criticality != nil, deref(in.Criticality), in.Position, in.OwnerUnitID != nil, nullable(deref(in.OwnerUnitID)))
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "ext_capability_code_uq"):
			return engine.Duplicate("code", "codice già usato da un'altra capability")
		case strings.Contains(err.Error(), "foreign key"):
			return engine.InvalidCode("validation.org_unit_not_found", "owner_unit_id", "unità organizzativa inesistente")
		}
	}
	return err
}

// checkParent verifica che il genitore sia una capability e che non si crei un ciclo
// (il genitore non può essere la capability stessa né una sua discendente) né si superi
// la profondità massima.
func checkParent(ctx context.Context, tx pgx.Tx, id, parent string) error {
	if !uuidRe.MatchString(parent) {
		return engine.NotFound("parent_id", "", "capability padre inesistente")
	}
	if ok, err := isType(ctx, tx, parent, CapabilityType); err != nil || !ok {
		return firstErr(err, engine.NotFound("parent_id", "", "capability padre inesistente"))
	}
	var cycle bool
	var depth int
	err := tx.QueryRow(ctx, `with recursive up(id, parent_id, n) as (
			select id, parent_id, 1 from elements where id = $1
			union all select e.id, e.parent_id, up.n + 1 from elements e join up on e.id = up.parent_id where up.n < 50
		) select coalesce(bool_or(id::text = $2), false), count(*) from up`, parent, id).Scan(&cycle, &depth)
	if err != nil {
		return err
	}
	if cycle {
		return engine.InvalidCode("validation.architecture.capability_cycle", "parent_id", "una capability non può stare dentro sé stessa o una sua sotto-capability")
	}
	var below int
	if id != "" {
		if err := tx.QueryRow(ctx, `with recursive down(id, n) as (
				select $1::uuid, 0 union all select e.id, d.n + 1 from elements e join down d on e.parent_id = d.id where d.n < 50 and e.deleted_at is null
			) select max(n) from down`, id).Scan(&below); err != nil {
			return err
		}
	}
	if depth+1+below > maxCapabilityDepth {
		return engine.InvalidParams("validation.architecture.capability_depth", "parent_id", map[string]any{"max": maxCapabilityDepth}, "troppi livelli (massimo %d)", maxCapabilityDepth)
	}
	return nil
}

func (s *Service) capability(ctx context.Context, tx pgx.Tx, id string) (*Capability, error) {
	list, err := capabilities(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

func (s *Service) UpdateCapability(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in CapabilityInput) (*Capability, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	n, err := resolveNode(ctx, tx, ref, []string{CapabilityType})
	if err != nil || n == nil {
		return nil, firstErr(err, engine.ErrNotFound)
	}
	el, err := s.eng.Get(ctx, tx, actor, n.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil || in.Description != nil {
		if el, err = s.eng.Update(ctx, tx, actor, n.ID, engine.UpdateInput{Version: el.Version, Title: in.Name, BodyText: in.Description}); err != nil {
			return nil, err
		}
	}
	if in.ParentID != nil {
		parent := strings.TrimSpace(*in.ParentID)
		if parent != deref(el.ParentID) {
			if parent != "" {
				if err := checkParent(ctx, tx, n.ID, parent); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(ctx, `update elements set parent_id = $2, version = version + 1, updated_at = now(), updated_by = $3 where id = $1`,
				n.ID, nullable(parent), nullable(actor.MemberID)); err != nil {
				return nil, err
			}
			if err := engine.AddActivity(ctx, tx, n.ID, actor.MemberID, "work", "parent", map[string]any{"parent": nullable(parent)},
				map[string]any{"parent": el.ParentID}); err != nil {
				return nil, err
			}
		}
	}
	if err := writeCapability(ctx, tx, n.ID, in); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "capability.update", CapabilityType, n.ID, nil); err != nil {
		return nil, err
	}
	return s.capability(ctx, tx, n.ID)
}

// LinkCapability collega un'applicazione ("supporta") o un asset logico ("realizza") a una
// capability. Il tipo di relazione si sceglie dal tipo dell'elemento.
func (s *Service) LinkCapability(ctx context.Context, tx pgx.Tx, actor engine.Actor, capRef, elementRef string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	c, err := resolveNode(ctx, tx, capRef, []string{CapabilityType})
	if err != nil || c == nil {
		return firstErr(err, engine.ErrNotFound)
	}
	n, err := resolveNode(ctx, tx, elementRef, []string{AppType, AssetType})
	if err != nil {
		return err
	}
	if n == nil {
		return engine.NotFound("element_id", "", "applicazione o asset logico inesistente")
	}
	rel := RelSupports
	if n.TypeKey == AssetType {
		rel = RelRealizes
	}
	_, err = s.createRelation(ctx, tx, actor, RelationInput{Type: rel, Source: n.ID, Target: c.ID}, true)
	return err
}

func (s *Service) UnlinkCapability(ctx context.Context, tx pgx.Tx, actor engine.Actor, capRef, elementRef string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	c, err := resolveNode(ctx, tx, capRef, []string{CapabilityType})
	if err != nil || c == nil {
		return firstErr(err, engine.ErrNotFound)
	}
	n, err := resolveNode(ctx, tx, elementRef, []string{AppType, AssetType})
	if err != nil || n == nil {
		return firstErr(err, engine.ErrNotFound)
	}
	return unrelate(ctx, tx, actor, []string{RelSupports, RelRealizes}, n.ID, c.ID)
}

func capabilityByCodeOrName(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	n, err := resolveNode(ctx, tx, ref, []string{CapabilityType})
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return "", engine.InvalidParams("validation.architecture.capability_parent", "padre", map[string]any{"detail": ve.Error()}, "%s", ve.Error())
		}
		return "", err
	}
	if n == nil {
		return "", nil
	}
	return n.ID, nil
}
