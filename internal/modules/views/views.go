// Package views: viste salvate delle liste (piano, passo 16). Una vista è una query con il
// modo di mostrarla (colonne, larghezze, raggruppamento, lista o tabella); può essere
// personale, condivisa con un gruppo o con tutto il tenant, tra i preferiti del menu e
// predefinita di una pagina per un utente o per il tenant.
package views

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

const (
	ScopePrivate = "private"
	ScopeGroup   = "group"
	ScopeTenant  = "tenant"

	maxSettings = 16 << 10
	maxQuery    = 4000
)

var pageRE = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// Viewer è chi guarda: serve a decidere cosa vede e cosa può cambiare.
type Viewer struct {
	MemberID string
	// Admin: amministratore del tenant (può condividere con tutti e cambiare ogni vista condivisa).
	Admin bool
}

type View struct {
	ID        string          `json:"id"`
	Page      string          `json:"page"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Query     string          `json:"query"`
	Settings  json.RawMessage `json:"settings"`
	Scope     string          `json:"scope"`
	GroupID   *string         `json:"group_id"`
	GroupName *string         `json:"group_name"`
	OwnerID   *string         `json:"owner_id"`
	OwnerName *string         `json:"owner_name"`
	Mine      bool            `json:"mine"`
	CanEdit   bool            `json:"can_edit"`
	Favorite  bool            `json:"favorite"`
	// DefaultMe e DefaultTenant: la vista è la predefinita della pagina per me o per il tenant.
	DefaultMe     bool      `json:"default_me"`
	DefaultTenant bool      `json:"default_tenant"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Input struct {
	Page     string          `json:"page"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Query    string          `json:"query"`
	Settings json.RawMessage `json:"settings"`
	Scope    string          `json:"scope"`
	GroupID  *string         `json:"group_id"`
}

// Patch: i campi assenti restano come sono.
type Patch struct {
	Name     *string         `json:"name"`
	Kind     *string         `json:"kind"`
	Query    *string         `json:"query"`
	Settings json.RawMessage `json:"settings"`
	Scope    *string         `json:"scope"`
	GroupID  *string         `json:"group_id"`
}

// selectViews: le viste visibili a $1 (membro), con i suoi preferiti e le predefinite.
const selectViews = `
select v.id::text, v.page, v.name, v.kind, v.query, v.settings, v.scope, v.group_id::text, g.name,
       v.owner_id::text, o.display_name, v.owner_id = $1::uuid,
       exists (select 1 from view_favorites f where f.tenant_id = v.tenant_id and f.member_id = $1::uuid and f.view_id = v.id),
       exists (select 1 from view_defaults d where d.tenant_id = v.tenant_id and d.view_id = v.id and d.member_id = $1::uuid),
       exists (select 1 from view_defaults d where d.tenant_id = v.tenant_id and d.view_id = v.id and d.member_id is null),
       v.updated_at
from views v
left join groups g on g.tenant_id = v.tenant_id and g.id = v.group_id
left join members o on o.tenant_id = v.tenant_id and o.id = v.owner_id
where v.system_key is null and v.page is not null
  and (v.scope = 'tenant' or v.owner_id = $1::uuid
       or (v.scope = 'group' and member_in_group($1::uuid, v.group_id)))
  and ($2::text = '' or v.page = $2)
  and ($3::uuid is null or v.id = $3)`

func scan(rows pgx.Rows, who Viewer) ([]View, error) {
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		var v View
		var settings []byte
		if err := rows.Scan(&v.ID, &v.Page, &v.Name, &v.Kind, &v.Query, &settings, &v.Scope, &v.GroupID, &v.GroupName,
			&v.OwnerID, &v.OwnerName, &v.Mine, &v.Favorite, &v.DefaultMe, &v.DefaultTenant, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Settings = settings
		// Le viste private degli altri non si vedono mai; un amministratore cambia quelle condivise.
		v.CanEdit = v.Mine || (who.Admin && v.Scope != ScopePrivate)
		out = append(out, v)
	}
	return out, rows.Err()
}

// List: le viste di una pagina (vuota = tutte) visibili a chi guarda.
func List(ctx context.Context, tx pgx.Tx, who Viewer, page string) ([]View, error) {
	rows, err := tx.Query(ctx, selectViews+` order by lower(v.name), v.created_at`, who.MemberID, page, nil)
	if err != nil {
		return nil, err
	}
	return scan(rows, who)
}

// Get una vista visibile a chi guarda.
func Get(ctx context.Context, tx pgx.Tx, who Viewer, id string) (*View, error) {
	rows, err := tx.Query(ctx, selectViews, who.MemberID, "", id)
	if err != nil {
		return nil, err
	}
	out, err := scan(rows, who)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, engine.ErrNotFound
	}
	return &out[0], nil
}

// validate controlla i campi comuni; restituisce le impostazioni normalizzate (oggetto JSON).
func validate(name, kind, query string, settings json.RawMessage) (json.RawMessage, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return nil, engine.TooLong("name", 80, "il nome è obbligatorio (al massimo 80 caratteri)")
	}
	switch kind {
	case "list", "table", "board":
	default:
		return nil, engine.InvalidValue("kind", "modalità non ammessa: lista, tabella o board")
	}
	if len(query) > maxQuery || !utf8.ValidString(query) {
		return nil, engine.InvalidValue("query", "query troppo lunga o non valida")
	}
	if len(settings) == 0 || string(settings) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if len(settings) > maxSettings {
		return nil, engine.InvalidCode("validation.views.settings_too_big", "settings", "impostazioni troppo grandi")
	}
	var obj map[string]any
	if err := json.Unmarshal(settings, &obj); err != nil || obj == nil {
		return nil, engine.InvalidCode("validation.views.settings_not_object", "settings", "le impostazioni devono essere un oggetto JSON")
	}
	return settings, nil
}

// checkScope verifica la condivisione richiesta: con tutto il tenant solo un
// amministratore; con un gruppo solo uno dei suoi membri (o un amministratore).
func checkScope(ctx context.Context, tx pgx.Tx, who Viewer, scope string, groupID *string) error {
	switch scope {
	case ScopePrivate, ScopeTenant:
		if groupID != nil {
			return engine.InvalidCode("validation.views.group_only_with_group", "group_id", "il gruppo si indica solo condividendo con un gruppo")
		}
		if scope == ScopeTenant && !who.Admin {
			return engine.InvalidCode("validation.views.share_all_admin", "scope", "solo un amministratore può condividere una vista con tutta l'organizzazione")
		}
	case ScopeGroup:
		if groupID == nil || *groupID == "" {
			return engine.Required("group_id", "scegli il gruppo con cui condividere")
		}
		var ok bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from groups g where g.id = $1::uuid)
			and ($3 or member_in_group($2::uuid, $1::uuid))`,
			*groupID, who.MemberID, who.Admin).Scan(&ok); err != nil {
			return engine.InvalidValue("group_id", "gruppo non valido")
		}
		if !ok {
			return engine.InvalidCode("validation.views.group_not_member", "group_id", "puoi condividere solo con un gruppo di cui fai parte")
		}
	default:
		return engine.InvalidValue("scope", "condivisione non ammessa: personale, gruppo o tutti")
	}
	return nil
}

func Create(ctx context.Context, tx pgx.Tx, who Viewer, in Input) (*View, error) {
	if !pageRE.MatchString(in.Page) {
		return nil, engine.InvalidValue("page", "pagina non valida")
	}
	if in.Scope == "" {
		in.Scope = ScopePrivate
	}
	if in.Kind == "" {
		in.Kind = "table"
	}
	settings, err := validate(in.Name, in.Kind, in.Query, in.Settings)
	if err != nil {
		return nil, err
	}
	if err := checkScope(ctx, tx, who, in.Scope, in.GroupID); err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from views where owner_id = $1::uuid and page is not null`, who.MemberID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= 200 {
		return nil, engine.InvalidCode("validation.views.views_limit", "name", "hai già 200 viste: eliminane qualcuna")
	}
	var id string
	err = tx.QueryRow(ctx, `insert into views (tenant_id, name, kind, query, settings, owner_id, shared, scope, group_id, page)
		values (app_tenant(), $1, $2, $3, $4, $5::uuid, $6, $7, $8::uuid, $9) returning id::text`,
		strings.TrimSpace(in.Name), in.Kind, in.Query, settings, who.MemberID, in.Scope != ScopePrivate, in.Scope, in.GroupID, in.Page).Scan(&id)
	if err != nil {
		return nil, err
	}
	return Get(ctx, tx, who, id)
}

// editable carica una vista che chi guarda può cambiare.
func editable(ctx context.Context, tx pgx.Tx, who Viewer, id string) (*View, error) {
	v, err := Get(ctx, tx, who, id)
	if err != nil {
		return nil, err
	}
	if !v.CanEdit {
		return nil, engine.ErrForbidden
	}
	return v, nil
}

func Update(ctx context.Context, tx pgx.Tx, who Viewer, id string, p Patch) (*View, error) {
	v, err := editable(ctx, tx, who, id)
	if err != nil {
		return nil, err
	}
	name, kind, query, settings, scope, group := v.Name, v.Kind, v.Query, v.Settings, v.Scope, v.GroupID
	if p.Name != nil {
		name = *p.Name
	}
	if p.Kind != nil {
		kind = *p.Kind
	}
	if p.Query != nil {
		query = *p.Query
	}
	if len(p.Settings) > 0 {
		settings = p.Settings
	}
	changedScope := false
	if p.Scope != nil {
		scope, group = *p.Scope, p.GroupID
		if scope != ScopeGroup {
			group = nil
		}
		changedScope = scope != v.Scope || (group == nil) != (v.GroupID == nil) || (group != nil && *group != *v.GroupID)
	}
	if settings, err = validate(name, kind, query, settings); err != nil {
		return nil, err
	}
	// La condivisione si ricontrolla solo se cambia: un amministratore che rinomina una vista
	// di gruppo non deve esserne membro.
	if changedScope {
		if err := checkScope(ctx, tx, who, scope, group); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `update views set name = $2, kind = $3, query = $4, settings = $5, scope = $6, group_id = $7::uuid,
		shared = $6 <> 'private', updated_at = now() where id = $1::uuid`,
		id, strings.TrimSpace(name), kind, query, settings, scope, group); err != nil {
		return nil, err
	}
	// Una vista tornata personale non resta predefinita né preferita di altri.
	if scope == ScopePrivate {
		if _, err := tx.Exec(ctx, `delete from view_defaults where view_id = $1::uuid and member_id is distinct from $2::uuid`, id, v.OwnerID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `delete from view_favorites where view_id = $1::uuid and member_id is distinct from $2::uuid`, id, v.OwnerID); err != nil {
			return nil, err
		}
	}
	return Get(ctx, tx, who, id)
}

func Delete(ctx context.Context, tx pgx.Tx, who Viewer, id string) error {
	if _, err := editable(ctx, tx, who, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from views where id = $1::uuid`, id)
	return err
}

// Duplicate crea una copia personale di una vista visibile.
func Duplicate(ctx context.Context, tx pgx.Tx, who Viewer, id, name string) (*View, error) {
	v, err := Get(ctx, tx, who, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = v.Name + " (copia)"
	}
	return Create(ctx, tx, who, Input{Page: v.Page, Name: name, Kind: v.Kind, Query: v.Query, Settings: v.Settings, Scope: ScopePrivate})
}

func SetFavorite(ctx context.Context, tx pgx.Tx, who Viewer, id string, on bool) error {
	if _, err := Get(ctx, tx, who, id); err != nil {
		return err
	}
	if !on {
		_, err := tx.Exec(ctx, `delete from view_favorites where view_id = $1::uuid and member_id = $2::uuid`, id, who.MemberID)
		return err
	}
	_, err := tx.Exec(ctx, `insert into view_favorites (tenant_id, member_id, view_id, position)
		values (app_tenant(), $2::uuid, $1::uuid, coalesce((select max(position) + 1 from view_favorites where member_id = $2::uuid), 0))
		on conflict do nothing`, id, who.MemberID)
	return err
}

// SetDefault imposta (o toglie, con viewID vuoto) la vista predefinita di una pagina, per chi
// guarda o per tutto il tenant (solo amministratore; la vista dev'essere condivisa con tutti).
func SetDefault(ctx context.Context, tx pgx.Tx, who Viewer, page, viewID string, forTenant bool) error {
	if !pageRE.MatchString(page) {
		return engine.InvalidValue("page", "pagina non valida")
	}
	var owner any = who.MemberID
	if forTenant {
		if !who.Admin {
			return engine.InvalidCode("validation.views.default_admin_only", "for", "solo un amministratore sceglie la vista predefinita dell'organizzazione")
		}
		owner = nil
	}
	if _, err := tx.Exec(ctx, `delete from view_defaults where page = $1 and member_id is not distinct from $2::uuid`, page, owner); err != nil {
		return err
	}
	if viewID == "" {
		return nil
	}
	v, err := Get(ctx, tx, who, viewID)
	if err != nil {
		return err
	}
	if v.Page != page {
		return engine.InvalidCode("validation.views.view_other_page", "view_id", "la vista è di un'altra pagina")
	}
	if forTenant && v.Scope != ScopeTenant {
		return engine.InvalidCode("validation.views.default_must_be_shared", "view_id", "la vista predefinita dell'organizzazione dev'essere condivisa con tutti")
	}
	_, err = tx.Exec(ctx, `insert into view_defaults (tenant_id, page, member_id, view_id) values (app_tenant(), $1, $2::uuid, $3::uuid)`, page, owner, viewID)
	return err
}

// Favorite è un preferito del menu: la query serve per contare i risultati.
type Favorite struct {
	ID    string `json:"id"`
	Page  string `json:"page"`
	Name  string `json:"name"`
	Query string `json:"query"`
	// Count: risultati (al massimo CountCap); More = ce ne sono altri. Nil se la query non è valida.
	Count *int `json:"count"`
	More  bool `json:"more"`
}

const CountCap = 99

// Favorites: i preferiti di chi guarda, nell'ordine scelto, con il contatore.
func Favorites(ctx context.Context, tx pgx.Tx, eng *engine.Service, actor engine.Actor, who Viewer) ([]Favorite, error) {
	rows, err := tx.Query(ctx, `select v.id::text, v.page, v.name, v.query
		from view_favorites f join views v on v.tenant_id = f.tenant_id and v.id = f.view_id
		where f.member_id = $1::uuid and v.page is not null
		  and (v.scope = 'tenant' or v.owner_id = $1::uuid
		       or (v.scope = 'group' and member_in_group($1::uuid, v.group_id)))
		order by f.position, f.created_at`, who.MemberID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Favorite, error) {
		var f Favorite
		return f, r.Scan(&f.ID, &f.Page, &f.Name, &f.Query)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		// Il contatore non deve far fallire il menu: una query diventata non valida dà "—".
		page, err := eng.Search(ctx, tx, actor, out[i].Query, CountCap+1, "")
		var qe *engine.QueryError
		var ve *engine.ValidationError
		if errors.As(err, &qe) || errors.As(err, &ve) {
			continue
		}
		if err != nil {
			return nil, err
		}
		n := len(page.Items)
		if n > CountCap {
			n, out[i].More = CountCap, true
		}
		out[i].Count = &n
	}
	return out, nil
}

// Group è un gruppo con cui si può condividere una vista.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ShareGroups: i gruppi di cui chi guarda fa parte (tutti, per un amministratore).
func ShareGroups(ctx context.Context, tx pgx.Tx, who Viewer) ([]Group, error) {
	rows, err := tx.Query(ctx, `select g.id::text, g.name from groups g
		where $2 or member_in_group($1::uuid, g.id)
		order by lower(g.name)`, who.MemberID, who.Admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Group, error) {
		var g Group
		return g, r.Scan(&g.ID, &g.Name)
	})
}
