package architecture

// Tipi di relazione dell'architettura e relazioni con attributi (replica/architettura-ea.md
// §3): chiama API, legge dati da, invia eventi a, usa, più quelli aggiunti dal tenant.
// Ogni relazione rispetta i tipi ammessi (source_type_ids / target_type_ids) e lo schema
// degli attributi del suo tipo (attrs_schema).

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

type AttrField struct {
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"` // text | enum | number | bool
	Values []string `json:"values,omitempty"`
}

type AttrSchema struct {
	Fields []AttrField `json:"fields"`
}

type RelationType struct {
	ID              string      `json:"id"`
	Key             string      `json:"key"`
	OutwardName     string      `json:"outward_name"`
	InwardName      string      `json:"inward_name"`
	Category        string      `json:"category"`
	ImpactDirection string      `json:"impact_direction"`
	SourceTypes     []string    `json:"source_types"` // null = qualunque tipo
	TargetTypes     []string    `json:"target_types"`
	Schema          *AttrSchema `json:"attrs_schema"`
	System          bool        `json:"system"`
	Archived        bool        `json:"archived"`
	Count           int         `json:"relations"`

	sourceIDs, targetIDs []string
}

const selectRelType = `select rt.id, rt.key, rt.outward_name, rt.inward_name, coalesce(rt.ea_category, ''), rt.impact_direction,
	rt.source_type_ids::text[], rt.target_type_ids::text[], rt.attrs_schema, rt.is_system, rt.archived,
	(select count(*) from relations r where r.tenant_id = rt.tenant_id and r.type_id = rt.id)
	from relation_types rt`

func scanRelTypes(ctx context.Context, tx pgx.Tx, rows pgx.Rows) ([]RelationType, error) {
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RelationType, error) {
		var t RelationType
		var schema []byte
		err := r.Scan(&t.ID, &t.Key, &t.OutwardName, &t.InwardName, &t.Category, &t.ImpactDirection, &t.sourceIDs, &t.targetIDs,
			&schema, &t.System, &t.Archived, &t.Count)
		if err == nil && len(schema) > 0 {
			t.Schema = &AttrSchema{}
			err = json.Unmarshal(schema, t.Schema)
		}
		return t, err
	})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].SourceTypes, err = typeKeys(ctx, tx, list[i].sourceIDs); err != nil {
			return nil, err
		}
		if list[i].TargetTypes, err = typeKeys(ctx, tx, list[i].targetIDs); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// RelationTypes elenca i tipi gestiti dall'architettura (categoria impostata).
func (s *Service) RelationTypes(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]RelationType, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, selectRelType+` where rt.ea_category is not null order by rt.ea_category, rt.archived, rt.outward_name`)
	if err != nil {
		return nil, err
	}
	return scanRelTypes(ctx, tx, rows)
}

// relationType trova un tipo per chiave oppure per nome diretto ("chiama le API di") tra i
// tipi dell'architettura, limitandosi a una categoria se indicata.
func relationType(ctx context.Context, tx pgx.Tx, key string, category ...string) (*RelationType, error) {
	cat := ""
	if len(category) > 0 {
		cat = category[0]
	}
	rows, err := tx.Query(ctx, selectRelType+` where rt.key = $1 or (rt.ea_category is not null and ($2 = '' or rt.ea_category = $2) and lower(rt.outward_name) = lower($1))
		order by rt.key = $1 desc, rt.archived, rt.is_system desc, rt.key = 'uses_app' desc limit 1`, strings.TrimSpace(key), cat)
	if err != nil {
		return nil, err
	}
	list, err := scanRelTypes(ctx, tx, rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

type RelationTypeInput struct {
	Key             string      `json:"key"`
	OutwardName     *string     `json:"outward_name"`
	InwardName      *string     `json:"inward_name"`
	Category        *string     `json:"category"`
	ImpactDirection *string     `json:"impact_direction"`
	SourceTypes     *[]string   `json:"source_types"` // [] o null = qualunque tipo
	TargetTypes     *[]string   `json:"target_types"`
	Schema          *AttrSchema `json:"attrs_schema"`
	Archived        *bool       `json:"archived"`
}

var categories = map[string]bool{catDependency: true, catCapability: true, catComposition: true}
var directions = map[string]bool{"forward": true, "reverse": true, "none": true}
var attrKinds = map[string]bool{"text": true, "enum": true, "number": true, "bool": true}

func (in *RelationTypeInput) validate() error {
	for name, p := range map[string]*string{"outward_name": in.OutwardName, "inward_name": in.InwardName} {
		if p != nil {
			v := strings.TrimSpace(*p)
			if v == "" || len(v) > 80 {
				return engine.TooLong(name, 80, "nome obbligatorio, massimo 80 caratteri")
			}
			*p = v
		}
	}
	if in.Category != nil && !categories[*in.Category] {
		return engine.InvalidValue("category", "categoria non ammessa (dependency, capability, composition)")
	}
	if in.ImpactDirection != nil && !directions[*in.ImpactDirection] {
		return engine.InvalidValue("impact_direction", "verso non ammesso (forward, reverse, none)")
	}
	if in.Schema != nil {
		if len(in.Schema.Fields) > 20 {
			return engine.InvalidCode("validation.architecture.attrs_too_many", "attrs_schema", "al massimo 20 attributi")
		}
		seen := map[string]bool{}
		for i, f := range in.Schema.Fields {
			if !typeKeyRe.MatchString(f.Key) || seen[f.Key] {
				return engine.InvalidValue("attrs_schema", "attributo %d: chiave %q non valida o ripetuta", i+1, f.Key)
			}
			seen[f.Key] = true
			if strings.TrimSpace(f.Name) == "" || len(f.Name) > 80 {
				return engine.TooLong("attrs_schema", 80, "attributo %q: nome obbligatorio (massimo 80 caratteri)", f.Key)
			}
			if !attrKinds[f.Kind] {
				return engine.InvalidValue("attrs_schema", "attributo %q: tipo %q non ammesso (text, enum, number, bool)", f.Key, f.Kind)
			}
			if f.Kind == "enum" && len(f.Values) == 0 {
				return engine.InvalidParams("validation.architecture.attr_list_values", "attrs_schema", map[string]any{"key": f.Key}, "attributo %q: un elenco ha bisogno dei valori", f.Key)
			}
			if len(f.Values) > 100 {
				return engine.InvalidParams("validation.architecture.attr_values_max", "attrs_schema", map[string]any{"key": f.Key}, "attributo %q: al massimo 100 valori", f.Key)
			}
		}
	}
	return nil
}

func typeIDs(ctx context.Context, tx pgx.Tx, field string, keys *[]string) (any, error) {
	if keys == nil || len(*keys) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `select id::text, key from element_types where key = any($1)`, *keys)
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	var id, key string
	if _, err := pgx.ForEachRow(rows, []any{&id, &key}, func() error { found[key] = id; return nil }); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, k := range *keys {
		v, ok := found[k]
		if !ok {
			return nil, engine.NotFound(field, "type", "tipo di elemento %q inesistente", k)
		}
		ids = append(ids, v)
	}
	return ids, nil
}

func (s *Service) CreateRelationType(ctx context.Context, tx pgx.Tx, actor engine.Actor, in RelationTypeInput) (*RelationType, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if !typeKeyRe.MatchString(in.Key) {
		return nil, engine.Required("key", "chiave obbligatoria: minuscole, cifre e _, inizia con una lettera")
	}
	if in.OutwardName == nil || in.InwardName == nil {
		return nil, engine.Required("outward_name", "servono il nome diretto e quello inverso")
	}
	if in.Category == nil {
		c := catDependency
		in.Category = &c
	}
	if in.ImpactDirection == nil {
		d := "forward"
		in.ImpactDirection = &d
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	src, err := typeIDs(ctx, tx, "source_types", in.SourceTypes)
	if err != nil {
		return nil, err
	}
	tgt, err := typeIDs(ctx, tx, "target_types", in.TargetTypes)
	if err != nil {
		return nil, err
	}
	var schema any
	if in.Schema != nil {
		b, _ := json.Marshal(in.Schema)
		schema = string(b)
	}
	tag, err := tx.Exec(ctx, `insert into relation_types (tenant_id, id, key, outward_name, inward_name, source_type_ids, target_type_ids,
			attrs_schema, impact_direction, ea_category)
		values (app_tenant(), $1, $2, $3, $4, $5::uuid[], $6::uuid[], $7::jsonb, $8, $9) on conflict do nothing`,
		db.NewID(), in.Key, *in.OutwardName, *in.InwardName, src, tgt, schema, *in.ImpactDirection, *in.Category)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.Duplicate("key", "esiste già un tipo di relazione %q", in.Key)
	}
	if err := s.auditEA(ctx, tx, actor, "relation_type.create", "relation_type", "", map[string]any{"key": in.Key}); err != nil {
		return nil, err
	}
	return relationType(ctx, tx, in.Key)
}

func (s *Service) UpdateRelationType(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, in RelationTypeInput) (*RelationType, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	rt, err := relationType(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if rt == nil || rt.Key != key || rt.Category == "" {
		return nil, engine.ErrNotFound
	}
	src, tgt := any(rt.sourceIDs), any(rt.targetIDs)
	if in.SourceTypes != nil {
		if src, err = typeIDs(ctx, tx, "source_types", in.SourceTypes); err != nil {
			return nil, err
		}
	}
	if in.TargetTypes != nil {
		if tgt, err = typeIDs(ctx, tx, "target_types", in.TargetTypes); err != nil {
			return nil, err
		}
	}
	// I tipi ammessi non possono escludere relazioni già presenti.
	var bad int
	if err := tx.QueryRow(ctx, `select count(*) from relations r
		join elements s on s.tenant_id = r.tenant_id and s.id = r.source_id
		join elements t on t.tenant_id = r.tenant_id and t.id = r.target_id
		where r.type_id = $1 and (($2::uuid[] is not null and not s.type_id = any($2::uuid[])) or ($3::uuid[] is not null and not t.type_id = any($3::uuid[])))`,
		rt.ID, src, tgt).Scan(&bad); err != nil {
		return nil, err
	}
	if bad > 0 {
		return nil, engine.InvalidParams("validation.architecture.relations_violate_types", "source_types", map[string]any{"count": bad}, "%d relazioni esistenti non rispetterebbero i tipi ammessi", bad)
	}
	var schema any
	if in.Schema != nil {
		b, _ := json.Marshal(in.Schema)
		schema = string(b)
	}
	if _, err := tx.Exec(ctx, `update relation_types set
		outward_name = coalesce($2, outward_name), inward_name = coalesce($3, inward_name),
		ea_category = coalesce($4, ea_category), impact_direction = coalesce($5, impact_direction),
		source_type_ids = $6::uuid[], target_type_ids = $7::uuid[],
		attrs_schema = case when $8::jsonb is null then attrs_schema else $8::jsonb end,
		archived = coalesce($9, archived)
		where id = $1`, rt.ID, in.OutwardName, in.InwardName, in.Category, in.ImpactDirection, src, tgt, schema, in.Archived); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "relation_type.update", "relation_type", rt.ID, map[string]any{"key": rt.Key}); err != nil {
		return nil, err
	}
	return relationType(ctx, tx, key)
}

// ---------------------------------------------------------------------------
// Relazioni

type Relation struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	TypeName   string         `json:"type_name"`
	Category   string         `json:"category"`
	SourceID   string         `json:"source_id"`
	SourceKey  string         `json:"source_key"`
	SourceName string         `json:"source_name"`
	SourceType string         `json:"source_type"`
	SourceCode *string        `json:"source_code"`
	TargetID   string         `json:"target_id"`
	TargetKey  string         `json:"target_key"`
	TargetName string         `json:"target_name"`
	TargetType string         `json:"target_type"`
	TargetCode *string        `json:"target_code"`
	Attrs      map[string]any `json:"attrs"`
	CreatedAt  time.Time      `json:"created_at"`
}

const selectRelation = `
	select r.id, rt.key, rt.outward_name, coalesce(rt.ea_category, ''),
	       se.id, coalesce(ss.key || '-' || se.number, ''), se.title, st.key, coalesce(sa.code, sc.code),
	       te.id, coalesce(ts.key || '-' || te.number, ''), te.title, tt.key, coalesce(ta.code, tc.code),
	       r.attrs, r.created_at
	from relations r
	join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
	join elements se on se.tenant_id = r.tenant_id and se.id = r.source_id and se.deleted_at is null
	join element_types st on st.tenant_id = se.tenant_id and st.id = se.type_id
	join spaces ss on ss.tenant_id = se.tenant_id and ss.id = se.space_id
	left join ext_application sa on sa.tenant_id = se.tenant_id and sa.element_id = se.id
	left join ext_capability sc on sc.tenant_id = se.tenant_id and sc.element_id = se.id
	join elements te on te.tenant_id = r.tenant_id and te.id = r.target_id and te.deleted_at is null
	join element_types tt on tt.tenant_id = te.tenant_id and tt.id = te.type_id
	join spaces ts on ts.tenant_id = te.tenant_id and ts.id = te.space_id
	left join ext_application ta on ta.tenant_id = te.tenant_id and ta.element_id = te.id
	left join ext_capability tc on tc.tenant_id = te.tenant_id and tc.element_id = te.id`

func scanRelations(rows pgx.Rows) ([]Relation, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Relation, error) {
		var x Relation
		err := r.Scan(&x.ID, &x.Type, &x.TypeName, &x.Category, &x.SourceID, &x.SourceKey, &x.SourceName, &x.SourceType, &x.SourceCode,
			&x.TargetID, &x.TargetKey, &x.TargetName, &x.TargetType, &x.TargetCode, &x.Attrs, &x.CreatedAt)
		if x.Attrs == nil {
			x.Attrs = map[string]any{}
		}
		return x, err
	})
}

// Relations elenca le relazioni di una categoria (vuota = tutte quelle dell'architettura),
// eventualmente di un solo elemento (in entrambi i versi).
func (s *Service) Relations(ctx context.Context, tx pgx.Tx, actor engine.Actor, category, elementRef string) ([]Relation, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	elementID := ""
	if elementRef != "" {
		n, err := resolveNode(ctx, tx, elementRef, nil)
		if err != nil || n == nil {
			return nil, firstErr(err, engine.ErrNotFound)
		}
		elementID = n.ID
	}
	rows, err := tx.Query(ctx, selectRelation+`
		where rt.ea_category is not null and ($1 = '' or rt.ea_category = $1)
		  and ($2 = '' or r.source_id::text = $2 or r.target_id::text = $2)
		order by se.title, rt.outward_name, te.title limit 5000`, category, elementID)
	if err != nil {
		return nil, err
	}
	return scanRelations(rows)
}

func relationByID(ctx context.Context, tx pgx.Tx, id string) (*Relation, error) {
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, selectRelation+` where r.id = $1 and rt.ea_category is not null`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanRelations(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

type RelationInput struct {
	Type   string         `json:"type"`
	Source string         `json:"source"` // id, chiave, codice o nome
	Target string         `json:"target"`
	Attrs  map[string]any `json:"attrs"`
}

func (s *Service) CreateRelation(ctx context.Context, tx pgx.Tx, actor engine.Actor, in RelationInput) (*Relation, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	id, err := s.createRelation(ctx, tx, actor, in, false)
	if err != nil {
		return nil, err
	}
	return relationByID(ctx, tx, id)
}

// createRelation valida tipo, estremi e attributi e crea la relazione. Con idempotent
// una relazione già presente non è un errore (e i suoi attributi restano).
func (s *Service) createRelation(ctx context.Context, tx pgx.Tx, actor engine.Actor, in RelationInput, idempotent bool) (string, error) {
	rt, err := relationType(ctx, tx, in.Type)
	if err != nil {
		return "", err
	}
	if rt == nil || rt.Category == "" {
		return "", engine.NotFound("type", "type", "tipo di relazione %q inesistente", in.Type)
	}
	if rt.Archived {
		return "", engine.InvalidParams("validation.architecture.relation_type_archived", "type", map[string]any{"name": rt.OutwardName}, "il tipo di relazione %q è archiviato", rt.OutwardName)
	}
	src, err := resolveNode(ctx, tx, in.Source, nil)
	if err != nil {
		return "", err
	}
	if src == nil {
		return "", engine.NotFound("source", "element", "elemento di origine %q inesistente", in.Source)
	}
	tgt, err := resolveNode(ctx, tx, in.Target, nil)
	if err != nil {
		return "", err
	}
	if tgt == nil {
		return "", engine.NotFound("target", "element", "elemento di destinazione %q inesistente", in.Target)
	}
	if src.ID == tgt.ID {
		return "", engine.InvalidCode("validation.architecture.link_self", "target", "un elemento non può essere collegato a sé stesso")
	}
	if err := checkEnd(ctx, tx, "source", rt, rt.sourceIDs, src); err != nil {
		return "", err
	}
	if err := checkEnd(ctx, tx, "target", rt, rt.targetIDs, tgt); err != nil {
		return "", err
	}
	attrs, err := cleanAttrs(rt.Schema, in.Attrs)
	if err != nil {
		return "", err
	}
	id := db.NewID()
	tag, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, attrs, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6) on conflict do nothing`, id, rt.ID, src.ID, tgt.ID, attrs, nullable(actor.MemberID))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		if !idempotent {
			return "", engine.Duplicate("target", "%s %s %s: relazione già presente", src.Title, rt.OutwardName, tgt.Title)
		}
		err := tx.QueryRow(ctx, `select id from relations where type_id = $1 and source_id = $2 and target_id = $3`, rt.ID, src.ID, tgt.ID).Scan(&id)
		return id, err
	}
	if err := engine.AddActivity(ctx, tx, src.ID, actor.MemberID, "relation", rt.Key, map[string]any{"target": tgt.ID}, nil); err != nil {
		return "", err
	}
	if err := engine.AddActivity(ctx, tx, tgt.ID, actor.MemberID, "relation", rt.Key+".inverse", map[string]any{"source": src.ID}, nil); err != nil {
		return "", err
	}
	return id, s.auditEA(ctx, tx, actor, "relation.create", "relation", id, map[string]any{"type": rt.Key, "source": src.ID, "target": tgt.ID})
}

func checkEnd(ctx context.Context, tx pgx.Tx, field string, rt *RelationType, allowed []string, n *nodeRef) error {
	if allowed == nil || slices.Contains(allowed, n.TypeID) {
		return nil
	}
	names, err := typeNames(ctx, tx, allowed)
	if err != nil {
		return err
	}
	verso := "come origine"
	if field == "target" {
		verso = "come destinazione"
	}
	return engine.InvalidParams("validation.architecture.relation_type_not_allowed", field, map[string]any{"name": rt.OutwardName, "side": verso, "type": n.TypeName, "allowed": strings.Join(names, ", ")}, "«%s» non ammette %s il tipo %s (ammessi: %s)", rt.OutwardName, verso, n.TypeName, strings.Join(names, ", "))
}

func typeNames(ctx context.Context, tx pgx.Tx, ids []string) ([]string, error) {
	rows, err := tx.Query(ctx, `select name from element_types where id = any($1::uuid[]) order by name`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// cleanAttrs valida gli attributi rispetto allo schema del tipo: chiavi note, valori
// degli elenchi ammessi, numeri e sì/no; i valori vuoti si tolgono.
func cleanAttrs(schema *AttrSchema, in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if len(in) == 0 {
		return out, nil
	}
	if schema == nil || len(schema.Fields) == 0 {
		return nil, engine.InvalidCode("validation.architecture.relation_no_attrs", "attrs", "questo tipo di relazione non ha attributi")
	}
	fields := map[string]AttrField{}
	for _, f := range schema.Fields {
		fields[f.Key] = f
	}
	for k, v := range in {
		f, ok := fields[k]
		if !ok {
			return nil, engine.InvalidParams("validation.architecture.relation_attr_unknown", "attrs", map[string]any{"key": k}, "attributo %q non previsto dal tipo di relazione", k)
		}
		if v == nil {
			continue
		}
		switch f.Kind {
		case "number":
			switch x := v.(type) {
			case float64:
				out[k] = x
			case string:
				if strings.TrimSpace(x) == "" {
					continue
				}
				n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64)
				if err != nil {
					return nil, engine.InvalidParams("validation.architecture.attr_not_number", "attrs", map[string]any{"name": f.Name, "value": x}, "%s: %q non è un numero", f.Name, x)
				}
				out[k] = n
			default:
				return nil, engine.Required("attrs", "%s: serve un numero", f.Name)
			}
		case "bool":
			switch x := v.(type) {
			case bool:
				out[k] = x
			case string:
				b, ok := parseBool(x)
				if !ok {
					return nil, engine.InvalidParams("validation.architecture.attr_not_yesno", "attrs", map[string]any{"name": f.Name, "value": x}, "%s: %q non è sì o no", f.Name, x)
				}
				out[k] = b
			default:
				return nil, engine.InvalidParams("validation.architecture.attr_yesno_required", "attrs", map[string]any{"name": f.Name}, "%s: serve sì o no", f.Name)
			}
		default:
			str, ok := v.(string)
			if !ok {
				str = fmt.Sprint(v)
			}
			str = strings.TrimSpace(str)
			if str == "" {
				continue
			}
			if len(str) > 500 {
				return nil, engine.TooLong("attrs", 500, "%s: massimo 500 caratteri", f.Name)
			}
			if f.Kind == "enum" {
				if k == "criticality" {
					if c, ok := normCriticality(str); ok && c != "" {
						str = c
					}
				}
				found := ""
				for _, val := range f.Values {
					if strings.EqualFold(val, str) {
						found = val
					}
				}
				if found == "" {
					return nil, engine.InvalidValue("attrs", "%s: valore %q non ammesso (%s)", f.Name, str, strings.Join(f.Values, ", "))
				}
				str = found
			}
			out[k] = str
		}
	}
	return out, nil
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sì", "si", "yes", "true", "1", "x":
		return true, true
	case "no", "false", "0", "":
		return false, true
	}
	return false, false
}

func (s *Service) UpdateRelation(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, attrs map[string]any) (*Relation, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	rel, err := relationByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	rt, err := relationType(ctx, tx, rel.Type)
	if err != nil {
		return nil, err
	}
	clean, err := cleanAttrs(rt.Schema, attrs)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update relations set attrs = $2 where id = $1`, id, clean); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, rel.SourceID, actor.MemberID, "relation", rel.Type+".attrs", map[string]any{"target": rel.TargetID, "attrs": clean}, nil); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "relation.update", "relation", id, map[string]any{"type": rel.Type}); err != nil {
		return nil, err
	}
	return relationByID(ctx, tx, id)
}

func (s *Service) DeleteRelation(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	rel, err := relationByID(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from relations where id = $1`, id); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, rel.SourceID, actor.MemberID, "relation", "removed", nil, map[string]any{"target": rel.TargetID}); err != nil {
		return err
	}
	return s.auditEA(ctx, tx, actor, "relation.delete", "relation", id, map[string]any{"type": rel.Type})
}
