package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Tipi di elemento configurabili per tenant: nome, plurale, descrizione, icona, colore, portale,
// nomi per lingua, tipi nuovi da un modello e archiviazione. Serve tenant.admin. Rinominare
// cambia solo i testi: chiave, dati, processi e campi restano com'erano.

var typeKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var typeIconRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var langRe = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

// CreatableModules sono i moduli per cui si può creare un tipo da interfaccia: gli altri
// richiedono una tabella di estensione di dominio.
var CreatableModules = []string{"work", "knowledge", "strategy", "core"}

func requireTypesAdmin(a Actor) error {
	if a.Has("tenant.admin") {
		return nil
	}
	return ErrForbidden
}

// TypeLabel è il nome di un tipo in una lingua.
type TypeLabel struct {
	Name   string `json:"name"`
	Plural string `json:"plural"`
}

// TypeConfig è un tipo come lo vede la pagina di configurazione.
type TypeConfig struct {
	Key           string               `json:"key"`
	Name          string               `json:"name"`
	PluralName    string               `json:"plural_name"`
	Description   string               `json:"description"`
	Module        string               `json:"module"`
	Icon          string               `json:"icon"`
	Color         string               `json:"color"`
	IsSystem      bool                 `json:"is_system"`
	PortalEnabled bool                 `json:"portal_enabled"`
	Labels        map[string]TypeLabel `json:"labels"`
	Archived      bool                 `json:"archived"`
	ArchivedAt    *time.Time           `json:"archived_at"`
	Elements      int                  `json:"elements"`
	Fields        int                  `json:"fields"`
	ProcessKey    string               `json:"process_key"`
	ProcessName   string               `json:"process_name"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

const typeConfigSelect = `
	select t.key, t.name, coalesce(t.plural_name, ''), t.description, t.module, coalesce(t.icon, ''), coalesce(t.color, ''),
	       t.is_system, t.portal_enabled, t.labels, t.archived_at,
	       (select count(*) from elements e where e.type_id = t.id and e.deleted_at is null and not e.is_draft)::int,
	       (select count(*) from type_fields tf join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
	         where tf.type_id = t.id and tf.space_id is null and not f.archived)::int,
	       coalesce(p.key, ''), coalesce(p.name, ''), t.updated_at
	from element_types t
	left join lateral (select pd.key, pd.name from process_definitions pd
	  where pd.tenant_id = t.tenant_id and pd.type_id = t.id and not pd.archived order by pd.priority, pd.key limit 1) p on true`

func scanTypeConfig(row pgx.Row) (*TypeConfig, error) {
	var c TypeConfig
	var labels []byte
	if err := row.Scan(&c.Key, &c.Name, &c.PluralName, &c.Description, &c.Module, &c.Icon, &c.Color,
		&c.IsSystem, &c.PortalEnabled, &labels, &c.ArchivedAt, &c.Elements, &c.Fields, &c.ProcessKey, &c.ProcessName, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Labels = map[string]TypeLabel{}
	if len(labels) > 0 {
		_ = json.Unmarshal(labels, &c.Labels)
	}
	c.Archived = c.ArchivedAt != nil
	return &c, nil
}

// ListTypeConfigs elenca i tipi del tenant (anche archiviati) con i contatori.
func (s *Service) ListTypeConfigs(ctx context.Context, tx pgx.Tx, actor Actor) ([]TypeConfig, error) {
	if err := requireTypesAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, typeConfigSelect+` order by t.module, lower(t.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TypeConfig{}
	for rows.Next() {
		c, err := scanTypeConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetTypeConfig restituisce un tipo per chiave.
func (s *Service) GetTypeConfig(ctx context.Context, tx pgx.Tx, actor Actor, key string) (*TypeConfig, error) {
	if err := requireTypesAdmin(actor); err != nil {
		return nil, err
	}
	return getTypeConfig(ctx, tx, key)
}

func getTypeConfig(ctx context.Context, tx pgx.Tx, key string) (*TypeConfig, error) {
	c, err := scanTypeConfig(tx.QueryRow(ctx, typeConfigSelect+` where t.key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, NotFound("tipo", "type", "tipo di elemento inesistente")
	}
	return c, err
}

// TypeName è la forma leggera per i menu: la leggono tutti i membri.
type TypeName struct {
	Key           string               `json:"key"`
	Name          string               `json:"name"`
	PluralName    string               `json:"plural_name"`
	Module        string               `json:"module"`
	Icon          string               `json:"icon"`
	Color         string               `json:"color"`
	PortalEnabled bool                 `json:"portal_enabled"`
	IsSystem      bool                 `json:"is_system"`
	Archived      bool                 `json:"archived"`
	Labels        map[string]TypeLabel `json:"labels"`
}

// TypeNames elenca nomi, plurali, icone e colori dei tipi (senza permessi di amministrazione).
// Con lang valorizzato (es. "en") il nome è quello della lingua, se c'è; altrimenti resta name.
func TypeNames(ctx context.Context, tx pgx.Tx, lang string) ([]TypeName, error) {
	rows, err := tx.Query(ctx, `select key, name, coalesce(plural_name, ''), module, coalesce(icon, ''), coalesce(color, ''),
		portal_enabled, is_system, archived_at is not null, labels from element_types order by module, lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TypeName{}
	for rows.Next() {
		var n TypeName
		var labels []byte
		if err := rows.Scan(&n.Key, &n.Name, &n.PluralName, &n.Module, &n.Icon, &n.Color, &n.PortalEnabled, &n.IsSystem, &n.Archived, &labels); err != nil {
			return nil, err
		}
		n.Labels = map[string]TypeLabel{}
		_ = json.Unmarshal(labels, &n.Labels)
		if l, ok := n.Labels[lang]; ok && lang != "" {
			if l.Name != "" {
				n.Name = l.Name
			}
			if l.Plural != "" {
				n.PluralName = l.Plural
			}
		}
		if n.PluralName == "" {
			n.PluralName = n.Name
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// TypeUpdate: solo i campi valorizzati cambiano.
type TypeUpdate struct {
	Name          *string               `json:"name"`
	PluralName    *string               `json:"plural_name"`
	Description   *string               `json:"description"`
	Icon          *string               `json:"icon"`
	Color         *string               `json:"color"`
	PortalEnabled *bool                 `json:"portal_enabled"`
	Labels        *map[string]TypeLabel `json:"labels"`
	Archived      *bool                 `json:"archived"`
}

func auditType(ctx context.Context, tx pgx.Tx, actor Actor, action, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "element_type", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

// UpdateType modifica i testi e l'aspetto di un tipo. Chiave e modulo non cambiano mai; un tipo
// di sistema si rinomina ma non si archivia.
func (s *Service) UpdateType(ctx context.Context, tx pgx.Tx, actor Actor, key string, in TypeUpdate) (*TypeConfig, error) {
	if err := requireTypesAdmin(actor); err != nil {
		return nil, err
	}
	var id string
	var system bool
	err := tx.QueryRow(ctx, "select id, is_system from element_types where key = $1 for update", key).Scan(&id, &system)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	changed := []string{}
	set := func(col string, v any, field string) error {
		if _, err := tx.Exec(ctx, "update element_types set "+col+" = $2, updated_at = now() where id = $1", id, v); err != nil {
			return err
		}
		changed = append(changed, field)
		return nil
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len([]rune(n)) > 80 {
			return nil, TooLong("name", 80, "il nome è obbligatorio (massimo 80 caratteri)")
		}
		if err := set("name", n, "name"); err != nil {
			return nil, err
		}
	}
	if in.PluralName != nil {
		p := strings.TrimSpace(*in.PluralName)
		if len([]rune(p)) > 80 {
			return nil, TooLong("plural_name", 80, "massimo 80 caratteri")
		}
		if err := set("plural_name", nullable(p), "plural_name"); err != nil {
			return nil, err
		}
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len([]rune(d)) > 500 {
			return nil, TooLong("description", 500, "massimo 500 caratteri")
		}
		if err := set("description", d, "description"); err != nil {
			return nil, err
		}
	}
	if in.Icon != nil {
		if *in.Icon != "" && !typeIconRe.MatchString(*in.Icon) {
			return nil, InvalidValue("icon", "icona non valida")
		}
		if err := set("icon", nullable(*in.Icon), "icon"); err != nil {
			return nil, err
		}
	}
	if in.Color != nil {
		if !validColor(*in.Color) {
			return nil, InvalidValue("color", "colore non ammesso: scegli uno della palette")
		}
		if err := set("color", nullable(*in.Color), "color"); err != nil {
			return nil, err
		}
	}
	if in.PortalEnabled != nil {
		if err := set("portal_enabled", *in.PortalEnabled, "portal_enabled"); err != nil {
			return nil, err
		}
	}
	if in.Labels != nil {
		clean := map[string]TypeLabel{}
		for lang, l := range *in.Labels {
			if !langRe.MatchString(lang) {
				return nil, InvalidValue("labels", "lingua %q non valida (per esempio «en»)", lang)
			}
			l.Name, l.Plural = strings.TrimSpace(l.Name), strings.TrimSpace(l.Plural)
			if len([]rune(l.Name)) > 80 || len([]rune(l.Plural)) > 80 {
				return nil, TooLong("labels", 80, "massimo 80 caratteri")
			}
			if l.Name != "" || l.Plural != "" {
				clean[lang] = l
			}
		}
		raw, _ := json.Marshal(clean)
		if err := set("labels", raw, "labels"); err != nil {
			return nil, err
		}
	}
	if in.Archived != nil {
		if system {
			return nil, invalid("archived", "un tipo di sistema non si archivia: si può rinominare")
		}
		if *in.Archived {
			if _, err := tx.Exec(ctx, "update element_types set archived_at = coalesce(archived_at, now()), updated_at = now() where id = $1", id); err != nil {
				return nil, err
			}
		} else if _, err := tx.Exec(ctx, "update element_types set archived_at = null, updated_at = now() where id = $1", id); err != nil {
			return nil, err
		}
		changed = append(changed, "archived")
	}
	if len(changed) > 0 {
		if err := auditType(ctx, tx, actor, "type.update", id, map[string]any{"type": key, "changed": changed}); err != nil {
			return nil, err
		}
	}
	return getTypeConfig(ctx, tx, key)
}

// TypeCreate è un tipo nuovo. From: chiave del tipo da cui copiare fasi, campi e spazi.
type TypeCreate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	PluralName  string `json:"plural_name"`
	Description string `json:"description"`
	Module      string `json:"module"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	From        string `json:"from"`
}

// CreateType crea un tipo non di sistema. Con From copia dal modello le fasi (come processo
// semplice, passaggi liberi tra le fasi), i campi associati e gli spazi in cui si può creare.
func (s *Service) CreateType(ctx context.Context, tx pgx.Tx, actor Actor, in TypeCreate) (*TypeConfig, error) {
	if err := requireTypesAdmin(actor); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		return nil, TooLong("name", 80, "il nome è obbligatorio (massimo 80 caratteri)")
	}
	if !typeKeyRe.MatchString(in.Key) {
		return nil, InvalidValue("key", "chiave non valida (minuscole, cifre e _, inizia con una lettera)")
	}
	if in.Icon != "" && !typeIconRe.MatchString(in.Icon) {
		return nil, InvalidValue("icon", "icona non valida")
	}
	if !validColor(in.Color) {
		return nil, InvalidValue("color", "colore non ammesso: scegli uno della palette")
	}
	var src *TypeInfo
	var srcModule string
	if in.From != "" {
		var err error
		if src, err = LoadType(ctx, tx, in.From); err != nil {
			return nil, NotFound("from", "", "il tipo di partenza %q non esiste", in.From)
		}
		srcModule = src.Module
	}
	if in.Module == "" {
		in.Module = srcModule
	}
	if in.Module == "" {
		in.Module = "work"
	}
	if !slices.Contains(CreatableModules, in.Module) {
		return nil, invalid("module", "da qui si creano tipi dei moduli work, knowledge, strategy e core: gli altri richiedono una tabella propria")
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1)", in.Key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, Duplicate("key", "esiste già un tipo con chiave %q", in.Key)
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into element_types (tenant_id, id, key, name, plural_name, description, module, icon, color, is_system)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, false)`,
		id, in.Key, in.Name, nullable(strings.TrimSpace(in.PluralName)), strings.TrimSpace(in.Description), in.Module, nullable(in.Icon), nullable(in.Color)); err != nil {
		return nil, err
	}
	copied := map[string]any{"type": in.Key, "module": in.Module}
	if src != nil {
		copied["from"] = src.Key
		if _, err := tx.Exec(ctx, `insert into type_fields (tenant_id, id, type_id, field_id, space_id, required, default_value, ordinal, is_private, show_in_list, condition)
			select app_tenant(), gen_random_uuid(), $1, field_id, null, required, default_value, ordinal, is_private, show_in_list, condition
			from type_fields where type_id = $2 and space_id is null`, id, src.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), space_id, $1 from space_types where type_id = $2`, id, src.ID); err != nil {
			return nil, err
		}
		if err := s.copyPhases(ctx, tx, actor, in.Key, in.Name, src); err != nil {
			return nil, err
		}
	}
	if err := auditType(ctx, tx, actor, "type.create", id, copied); err != nil {
		return nil, err
	}
	return getTypeConfig(ctx, tx, in.Key)
}

// copyPhases crea e pubblica per il tipo nuovo un processo semplice con le fasi del modello:
// si parte dalla prima e da ogni fase si può passare a qualunque altra. L'azienda lo rifinisce
// poi nell'editor dei processi.
func (s *Service) copyPhases(ctx context.Context, tx pgx.Tx, actor Actor, key, name string, src *TypeInfo) error {
	if len(src.States) == 0 {
		return nil
	}
	w := &Workflow{Transitions: []Transition{}}
	for _, st := range src.States {
		w.Phases = append(w.Phases, Phase{Name: st.Name, Category: st.Category, Color: st.Color, OffPath: st.OffPath, Labels: st.Labels})
	}
	w.Initial = w.Phases[0].Name
	for _, p := range w.Phases {
		w.Transitions = append(w.Transitions, Transition{From: []string{"*"}, To: p.Name, Label: "Passa a " + p.Name})
	}
	pkey := key + "_processo"
	for i := 2; i < 20; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from process_definitions where key = $1)", pkey).Scan(&taken); err != nil {
			return err
		}
		if !taken {
			break
		}
		pkey = fmt.Sprintf("%s_processo_%d", key, i)
	}
	// Il processo è un effetto della creazione del tipo: si scrive con i permessi del motore,
	// l'autore resta la persona che ha chiesto la creazione (che è amministratore del tenant).
	eng := actor
	eng.Permissions = append(slices.Clone(actor.Permissions), PermProcessAdmin)
	pname := "Processo di " + name
	if _, err := s.CreateProcess(ctx, tx, eng, ProcessInput{Key: pkey, Name: &pname, TypeKey: key}, w); err != nil {
		return err
	}
	if _, _, err := s.Publish(ctx, tx, eng, pkey, "Creato con il tipo"); err != nil {
		return err
	}
	return nil
}
