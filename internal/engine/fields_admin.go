package engine

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Amministrazione dei campi personalizzati (Configurazione → Campi): definizioni, insiemi
// di valori, associazioni ai tipi di elemento, archiviazione, export e import in JSON.
// Serve fields.admin (ruolo di sistema "Amministratore dei campi") oppure tenant.admin.
// Ogni modifica lascia una voce nell'audit.

// PermFieldsAdmin è il permesso dell'amministratore dei campi.
const PermFieldsAdmin = "fields.admin"

// FieldsBundleFormat identifica il formato di export e import della configurazione dei campi.
const FieldsBundleFormat = "regia.fields/v1"

func requireFieldsAdmin(a Actor) error {
	if a.Has(PermFieldsAdmin) || a.Has("tenant.admin") {
		return nil
	}
	return ErrForbidden
}

// FieldDataTypes sono i tipi di dato che si possono scegliere (state è riservato al ciclo di vita).
var FieldDataTypes = []string{"string", "text", "integer", "decimal", "boolean", "date", "datetime", "duration",
	"money", "url", "enum", "version", "member", "group", "element"}

// multiTypes: i tipi che ammettono più valori.
var multiTypes = []string{"string", "url", "enum", "version", "member", "group", "element"}

// OptionColors sono i token del tema ammessi come colore di un'opzione.
var OptionColors = []string{"", "accent", "ok", "flag", "danger", "muted", "fg"}

var (
	fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	aliasRe    = regexp.MustCompile(`^\p{Ll}[\p{Ll}\p{N}_]{0,62}$`)
)

func choiceType(dt string) bool { return dt == "enum" || dt == "version" }

// FieldAssoc è l'associazione di un campo a un tipo di elemento.
type FieldAssoc struct {
	Type         string `json:"type"`
	TypeName     string `json:"type_name,omitempty"`
	Required     bool   `json:"required"`
	RequiredFrom string `json:"required_from,omitempty"`
	VisibleWhen  string `json:"visible_when,omitempty"`
	Section      string `json:"section,omitempty"`
	Ordinal      int    `json:"ordinal"`
	// Problem: avviso, es. la fase "obbligatorio da" non esiste più nel processo corrente.
	Problem string `json:"problem,omitempty"`
}

// FieldConfig è un campo come lo vede l'amministratore.
type FieldConfig struct {
	ID          string        `json:"id"`
	Key         string        `json:"key"`
	Name        string        `json:"name"`
	DataType    string        `json:"data_type"`
	Multi       bool          `json:"multi"`
	Help        string        `json:"help"`
	Default     any           `json:"default"`
	Settings    FieldSettings `json:"settings"`
	Aliases     []string      `json:"aliases"`
	Archived    bool          `json:"archived"`
	System      bool          `json:"system"`
	OptionSetID *string       `json:"option_set_id"`
	OptionSet   string        `json:"option_set,omitempty"`
	// SharedWith: altri campi che usano lo stesso insieme di valori.
	SharedWith []string     `json:"shared_with,omitempty"`
	Options    []OptionView `json:"options,omitempty"`
	Types      []FieldAssoc `json:"types"`
	InUse      bool         `json:"in_use"`
	// Elements: elementi con un valore (solo nel dettaglio del campo).
	Elements  int64     `json:"elements"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OptionInput è un'opzione nell'editor dell'insieme di valori (senza ID = nuova).
type OptionInput struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Color    string `json:"color,omitempty"`
	Archived bool   `json:"archived,omitempty"`
	// Labels: nome per lingua (lingua → nome); vuoto = segue il nome.
	Labels map[string]string `json:"labels,omitempty"`
}

// FieldInput crea o modifica un campo. Nella modifica contano solo i valori presenti.
type FieldInput struct {
	Key      *string         `json:"key"`
	Name     *string         `json:"name"`
	DataType string          `json:"data_type"`
	Multi    *bool           `json:"multi"`
	Help     *string         `json:"help"`
	Default  json.RawMessage `json:"default"`
	Settings *FieldSettings  `json:"settings"`
	Aliases  *[]string       `json:"aliases"`
	Archived *bool           `json:"archived"`
	// Solo alla creazione dei campi a scelta: insieme esistente da condividere, oppure
	// le opzioni del nuovo insieme.
	OptionSetID *string       `json:"option_set_id"`
	Options     []OptionInput `json:"options"`
}

// AssocInput associa un campo a un tipo (o ne cambia le regole).
type AssocInput struct {
	Required     bool   `json:"required"`
	RequiredFrom string `json:"required_from"`
	VisibleWhen  string `json:"visible_when"`
	Section      string `json:"section"`
	Ordinal      *int   `json:"ordinal"`
}

type fieldRow struct {
	id, key, name, dataType string
	multi, system, archived bool
	optionSetID             *string
	settings                FieldSettings
}

func loadFieldRow(ctx context.Context, tx pgx.Tx, ref string, lock bool) (*fieldRow, error) {
	q := `select id::text, key, name, data_type, is_multi, is_system, archived, option_set_id::text, settings
		from field_defs where id::text = $1 or key = $1`
	if lock {
		q += " for update"
	}
	f := &fieldRow{}
	var settings []byte
	err := tx.QueryRow(ctx, q, strings.TrimSpace(ref)).Scan(&f.id, &f.key, &f.name, &f.dataType, &f.multi, &f.system, &f.archived, &f.optionSetID, &settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(settings, &f.settings)
	return f, nil
}

func (f *fieldRow) def() FieldDef {
	return FieldDef{ID: f.id, Key: f.key, Name: f.name, DataType: f.dataType, Multi: f.multi, OptionSetID: f.optionSetID, Settings: f.settings}
}

func fieldInUse(ctx context.Context, tx pgx.Tx, fieldID string) (bool, error) {
	var used bool
	err := tx.QueryRow(ctx, `select exists (select 1 from element_values where field_id = $1)`, fieldID).Scan(&used)
	return used, err
}

func auditField(ctx context.Context, tx pgx.Tx, actor Actor, action, fieldID string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "field", EntityID: fieldID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

// ListFields elenca i campi del tenant con opzioni, associazioni e uso.
func (s *Service) ListFields(ctx context.Context, tx pgx.Tx, actor Actor) ([]FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	return s.fieldConfigs(ctx, tx, "")
}

// GetField restituisce un campo (per id o chiave) con il numero di elementi che lo usano.
func (s *Service) GetField(ctx context.Context, tx pgx.Tx, actor Actor, ref string) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	f, err := loadFieldRow(ctx, tx, ref, false)
	if err != nil {
		return nil, err
	}
	list, err := s.fieldConfigs(ctx, tx, f.id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	out := list[0]
	if err := tx.QueryRow(ctx, `select count(distinct element_id) from element_values where field_id = $1`, f.id).Scan(&out.Elements); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Service) fieldConfigs(ctx context.Context, tx pgx.Tx, onlyID string) ([]FieldConfig, error) {
	rows, err := tx.Query(ctx, `
		select f.id::text, f.key, f.name, f.data_type, f.is_multi, f.help, f.default_value, f.settings, f.aliases,
		       f.archived, f.is_system, f.option_set_id::text, coalesce(os.name, ''), f.updated_at,
		       exists (select 1 from element_values v where v.field_id = f.id)
		from field_defs f left join option_sets os on os.tenant_id = f.tenant_id and os.id = f.option_set_id
		where $1 = '' or f.id::text = $1
		order by f.archived, lower(f.name)`, onlyID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (FieldConfig, error) {
		var c FieldConfig
		var def, settings []byte
		err := r.Scan(&c.ID, &c.Key, &c.Name, &c.DataType, &c.Multi, &c.Help, &def, &settings, &c.Aliases,
			&c.Archived, &c.System, &c.OptionSetID, &c.OptionSet, &c.UpdatedAt, &c.InUse)
		if len(def) > 0 {
			_ = json.Unmarshal(def, &c.Default)
		}
		_ = json.Unmarshal(settings, &c.Settings)
		c.Types = []FieldAssoc{}
		return c, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	byID := map[string]*FieldConfig{}
	var sets []string
	bySet := map[string][]string{}
	for i := range list {
		byID[list[i].ID] = &list[i]
		if list[i].OptionSetID != nil {
			sets = append(sets, *list[i].OptionSetID)
		}
	}
	// Chi condivide l'insieme di valori (anche fuori dal filtro).
	if len(sets) > 0 {
		r, err := tx.Query(ctx, `select option_set_id::text, key from field_defs where option_set_id = any($1::uuid[]) order by key`, sets)
		if err != nil {
			return nil, err
		}
		var set, key string
		if _, err := pgx.ForEachRow(r, []any{&set, &key}, func() error {
			bySet[set] = append(bySet[set], key)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	opts, err := optionsOf(ctx, tx, sets)
	if err != nil {
		return nil, err
	}
	for i := range list {
		c := &list[i]
		if c.OptionSetID != nil {
			c.Options = opts[*c.OptionSetID]
			for _, k := range bySet[*c.OptionSetID] {
				if k != c.Key {
					c.SharedWith = append(c.SharedWith, k)
				}
			}
		}
	}
	// Associazioni, con il controllo della fase "obbligatorio da" sul processo corrente.
	r, err := tx.Query(ctx, `
		select tf.field_id::text, t.id::text, t.key, t.name, tf.required, tf.required_from, tf.visible_when, tf.section, tf.ordinal
		from type_fields tf join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id
		where tf.space_id is null and ($1 = '' or tf.field_id::text = $1)
		order by t.name, tf.ordinal`, onlyID)
	if err != nil {
		return nil, err
	}
	type assocRow struct {
		fieldID, typeID string
		a               FieldAssoc
	}
	assocs, err := pgx.CollectRows(r, func(row pgx.CollectableRow) (assocRow, error) {
		var x assocRow
		err := row.Scan(&x.fieldID, &x.typeID, &x.a.Type, &x.a.TypeName, &x.a.Required, &x.a.RequiredFrom, &x.a.VisibleWhen, &x.a.Section, &x.a.Ordinal)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	types := map[string]*TypeInfo{}
	for _, x := range assocs {
		c, ok := byID[x.fieldID]
		if !ok {
			continue
		}
		if x.a.RequiredFrom != "" {
			t, ok := types[x.typeID]
			if !ok {
				if t, err = loadTypeByID(ctx, tx, x.typeID); err != nil {
					return nil, err
				}
				types[x.typeID] = t
			}
			if len(t.States) > 0 && mainPathIndex(t.States, x.a.RequiredFrom) < 0 {
				x.a.Problem = "la fase «" + x.a.RequiredFrom + "» non è nel percorso principale del processo corrente: l'obbligo non si applica"
			}
		}
		c.Types = append(c.Types, x.a)
	}
	return list, nil
}

// validateFieldInput controlla nome, alias e vincoli; restituisce le impostazioni pulite.
func validateSettings(ctx context.Context, tx pgx.Tx, dataType string, in FieldSettings) (FieldSettings, error) {
	out := FieldSettings{}
	switch dataType {
	case "element":
		for _, k := range in.Types {
			k = strings.TrimSpace(k)
			if k == "" || slices.Contains(out.Types, k) {
				continue
			}
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from element_types where key = $1)`, k).Scan(&ok); err != nil {
				return out, err
			}
			if !ok {
				return out, NotFound("settings.types", "type", "tipo di elemento %q inesistente", k)
			}
			out.Types = append(out.Types, k)
		}
	case "member":
		if g := strings.TrimSpace(in.Group); g != "" {
			var name string
			if err := tx.QueryRow(ctx, `select name from groups where lower(name) = lower($1)`, g).Scan(&name); err != nil {
				return out, NotFound("settings.group", "group", "gruppo %q inesistente", g)
			}
			out.Group = name
		}
	case "group":
		for _, g := range in.Groups {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			var name string
			if err := tx.QueryRow(ctx, `select name from groups where lower(name) = lower($1)`, g).Scan(&name); err != nil {
				return out, NotFound("settings.groups", "group", "gruppo %q inesistente", g)
			}
			if !slices.Contains(out.Groups, name) {
				out.Groups = append(out.Groups, name)
			}
		}
	case "money":
		if c := strings.ToUpper(strings.TrimSpace(in.Currency)); c != "" {
			if !currencyRe.MatchString(c) {
				return out, InvalidValue("settings.currency", "valuta non valida: codice ISO 4217 di tre lettere (es. EUR)")
			}
			out.Currency = c
		}
	}
	switch dataType {
	case "integer", "decimal", "money", "duration":
		out.Min, out.Max = in.Min, in.Max
		if out.Min != nil && out.Max != nil && *out.Min > *out.Max {
			return out, invalid("settings.max", "il massimo è minore del minimo")
		}
	}
	if dataType == "duration" && in.HoursPerDay != 0 {
		if in.HoursPerDay < 1 || in.HoursPerDay > 24 {
			return out, OutOfRange("settings.hours_per_day", 1, 24, "ore per giornata tra 1 e 24")
		}
		out.HoursPerDay = in.HoursPerDay
	}
	return out, nil
}

func cleanName(field, s string, max int) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", invalidCode("validation.name_required", field, "il nome è obbligatorio")
	}
	if utf8.RuneCountInString(s) > max {
		return "", invalid(field, "massimo %d caratteri", max)
	}
	return s, nil
}

// validateAliases: nomi alternativi per le query e le condizioni, unici nel tenant.
func validateAliases(ctx context.Context, tx pgx.Tx, fieldID, key string, in []string) ([]string, error) {
	out := []string{}
	for _, a := range in {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || a == key || slices.Contains(out, a) {
			continue
		}
		if !aliasRe.MatchString(a) {
			return nil, InvalidValue("aliases", "alias %q non valido: lettere minuscole, cifre e _, inizia con una lettera", a)
		}
		var taken string
		err := tx.QueryRow(ctx, `select name from field_defs where id::text <> $1 and (key = $2 or $2 = any(aliases)) limit 1`, fieldID, a).Scan(&taken)
		if err == nil {
			return nil, Duplicate("aliases", "l'alias %q è già usato dal campo «%s»", a, taken)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, a)
	}
	if len(out) > 10 {
		return nil, invalid("aliases", "al massimo 10 alias")
	}
	return out, nil
}

// normalizeDefault porta il valore predefinito nella forma salvata (es. opzione per id).
func normalizeDefault(ctx context.Context, tx pgx.Tx, d FieldDef, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, InvalidValue("default", "valore predefinito non valido")
	}
	if isEmptyValue(v) {
		return nil, nil
	}
	d.Key = "default"
	out, _, err := normalize(ctx, tx, []FieldDef{d}, nil, map[string]any{"default": v}, true)
	if err != nil {
		return nil, err
	}
	return out["default"], nil
}

func validColor(c string) bool { return slices.Contains(OptionColors, c) }

func cleanOptions(in []OptionInput) ([]OptionInput, error) {
	seen := map[string]bool{}
	out := make([]OptionInput, 0, len(in))
	for _, o := range in {
		name, err := cleanName("options", o.Name, 100)
		if err != nil {
			return nil, TooLong("options", 100, "ogni opzione ha un nome (massimo 100 caratteri)")
		}
		if seen[strings.ToLower(name)] {
			return nil, invalid("options", "opzione %q ripetuta", name)
		}
		seen[strings.ToLower(name)] = true
		if !validColor(o.Color) {
			return nil, InvalidValue("options", "colore %q non ammesso (accent, ok, flag, danger, muted, fg)", o.Color)
		}
		lb, err := CleanLabels("options", o.Labels, 100)
		if err != nil {
			return nil, err
		}
		o.Name = name
		o.Labels = lb
		out = append(out, o)
	}
	if len(out) > 500 {
		return nil, invalid("options", "al massimo 500 opzioni")
	}
	return out, nil
}

// CreateField crea un campo (per i campi a scelta, con il suo insieme di valori o uno esistente).
func (s *Service) CreateField(ctx context.Context, tx pgx.Tx, actor Actor, in FieldInput) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	key := ""
	if in.Key != nil {
		key = strings.TrimSpace(*in.Key)
	}
	if !fieldKeyRe.MatchString(key) {
		return nil, InvalidValue("key", "chiave non valida: lettere minuscole senza accenti, cifre e _, inizia con una lettera (es. data_go_live)")
	}
	var taken bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from field_defs where key = $1 or $1 = any(aliases))`, key).Scan(&taken); err != nil {
		return nil, err
	}
	if taken {
		return nil, Duplicate("key", "la chiave %q è già usata da un altro campo", key)
	}
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	name, err := cleanName("name", name, 200)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(FieldDataTypes, in.DataType) {
		return nil, InvalidValue("data_type", "tipo di dato %q non ammesso", in.DataType)
	}
	multi := in.Multi != nil && *in.Multi
	if multi && !slices.Contains(multiTypes, in.DataType) {
		return nil, invalid("multi", "il tipo %s non ammette più valori", in.DataType)
	}
	settings := FieldSettings{}
	if in.Settings != nil {
		if settings, err = validateSettings(ctx, tx, in.DataType, *in.Settings); err != nil {
			return nil, err
		}
	}
	id := db.NewID()
	var aliases []string
	if in.Aliases != nil {
		if aliases, err = validateAliases(ctx, tx, id, key, *in.Aliases); err != nil {
			return nil, err
		}
	}
	if aliases == nil {
		aliases = []string{}
	}
	help := ""
	if in.Help != nil {
		help = strings.TrimSpace(*in.Help)
		if utf8.RuneCountInString(help) > 1000 {
			return nil, TooLong("help", 1000, "aiuto troppo lungo (massimo 1000 caratteri)")
		}
	}
	var setID *string
	if choiceType(in.DataType) {
		kind := in.DataType
		if in.OptionSetID != nil && *in.OptionSetID != "" {
			var k string
			if err := tx.QueryRow(ctx, `select kind from option_sets where id::text = $1`, *in.OptionSetID).Scan(&k); err != nil {
				return nil, NotFound("option_set_id", "", "insieme di valori inesistente")
			}
			if k != kind {
				return nil, invalid("option_set_id", "l'insieme di valori è di un altro genere (%s)", k)
			}
			setID = in.OptionSetID
		} else {
			opts, err := cleanOptions(in.Options)
			if err != nil {
				return nil, err
			}
			sid := db.NewID()
			if _, err := tx.Exec(ctx, `insert into option_sets (tenant_id, id, name, kind) values (app_tenant(), $1, $2, $3)`, sid, name, kind); err != nil {
				return nil, err
			}
			for i, o := range opts {
				if _, err := tx.Exec(ctx, `insert into options (tenant_id, id, option_set_id, name, color, ordinal, archived, labels)
					values (app_tenant(), $1, $2, $3, nullif($4, ''), $5, $6, $7)`, db.NewID(), sid, o.Name, o.Color, i, o.Archived, labelsJSON(o.Labels)); err != nil {
					return nil, err
				}
			}
			setID = &sid
		}
	}
	if _, err := tx.Exec(ctx, `insert into field_defs (tenant_id, id, key, name, data_type, is_multi, option_set_id, settings, aliases, help)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, key, name, in.DataType, multi, setID, settings, aliases, help); err != nil {
		return nil, err
	}
	d := FieldDef{ID: id, Key: key, Name: name, DataType: in.DataType, Multi: multi, OptionSetID: setID, Settings: settings}
	def, err := normalizeDefault(ctx, tx, d, in.Default)
	if err != nil {
		return nil, err
	}
	if def != nil {
		if _, err := tx.Exec(ctx, `update field_defs set default_value = $2 where id = $1`, id, jsonParam(def)); err != nil {
			return nil, err
		}
	}
	if err := auditField(ctx, tx, actor, "field.create", id, map[string]any{"key": key, "data_type": in.DataType, "multi": multi}); err != nil {
		return nil, err
	}
	return s.GetField(ctx, tx, actor, id)
}

// UpdateField modifica un campo. Su un campo in uso non cambiano chiave, tipo di dato e
// molteplicità (i valori salvati non si convertono): si aggiunge un alias o un campo nuovo.
func (s *Service) UpdateField(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in FieldInput) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	f, err := loadFieldRow(ctx, tx, ref, true)
	if err != nil {
		return nil, err
	}
	used, err := fieldInUse(ctx, tx, f.id)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	if in.DataType != "" && in.DataType != f.dataType {
		return nil, invalid("data_type", "il tipo di dato non cambia dopo la creazione: crea un nuovo campo")
	}
	key := f.key
	if in.Key != nil && strings.TrimSpace(*in.Key) != f.key {
		key = strings.TrimSpace(*in.Key)
		if used {
			return nil, invalid("key", "la chiave non cambia su un campo con valori: aggiungi un alias")
		}
		if f.system {
			return nil, invalid("key", "la chiave di un campo di sistema non cambia")
		}
		if !fieldKeyRe.MatchString(key) {
			return nil, InvalidValue("key", "chiave non valida: lettere minuscole senza accenti, cifre e _")
		}
		var taken bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from field_defs where id <> $1 and (key = $2 or $2 = any(aliases)))`, f.id, key).Scan(&taken); err != nil {
			return nil, err
		}
		if taken {
			return nil, Duplicate("key", "la chiave %q è già usata da un altro campo", key)
		}
		changed = append(changed, "key")
	}
	name := f.name
	if in.Name != nil {
		if name, err = cleanName("name", *in.Name, 200); err != nil {
			return nil, err
		}
		if name != f.name {
			changed = append(changed, "name")
		}
	}
	multi := f.multi
	if in.Multi != nil && *in.Multi != f.multi {
		if used {
			return nil, invalid("multi", "la molteplicità non cambia su un campo con valori")
		}
		if *in.Multi && !slices.Contains(multiTypes, f.dataType) {
			return nil, invalid("multi", "il tipo %s non ammette più valori", f.dataType)
		}
		multi = *in.Multi
		changed = append(changed, "multi")
	}
	settings := f.settings
	if in.Settings != nil {
		if settings, err = validateSettings(ctx, tx, f.dataType, *in.Settings); err != nil {
			return nil, err
		}
		changed = append(changed, "settings")
	}
	sets := []string{"key = $2", "name = $3", "is_multi = $4", "settings = $5", "updated_at = now()"}
	args := []any{f.id, key, name, multi, settings}
	if in.Aliases != nil {
		aliases, err := validateAliases(ctx, tx, f.id, key, *in.Aliases)
		if err != nil {
			return nil, err
		}
		args = append(args, aliases)
		sets = append(sets, "aliases = $6")
		changed = append(changed, "aliases")
	}
	if in.Help != nil {
		help := strings.TrimSpace(*in.Help)
		if utf8.RuneCountInString(help) > 1000 {
			return nil, TooLong("help", 1000, "aiuto troppo lungo (massimo 1000 caratteri)")
		}
		args = append(args, help)
		sets = append(sets, "help = $"+itoa(len(args)))
		changed = append(changed, "help")
	}
	if len(in.Default) > 0 {
		d := f.def()
		d.Key, d.Multi, d.Settings = key, multi, settings
		def, err := normalizeDefault(ctx, tx, d, in.Default)
		if err != nil {
			return nil, err
		}
		args = append(args, jsonParam(def))
		sets = append(sets, "default_value = $"+itoa(len(args)))
		changed = append(changed, "default")
	}
	action := "field.update"
	if in.Archived != nil && *in.Archived != f.archived {
		args = append(args, *in.Archived)
		sets = append(sets, "archived = $"+itoa(len(args)))
		if *in.Archived {
			action = "field.archive"
		} else {
			action = "field.restore"
		}
		changed = append(changed, "archived")
	}
	if _, err := tx.Exec(ctx, `update field_defs set `+strings.Join(sets, ", ")+` where id = $1`, args...); err != nil {
		return nil, err
	}
	if err := auditField(ctx, tx, actor, action, f.id, map[string]any{"key": key, "changed": changed}); err != nil {
		return nil, err
	}
	return s.GetField(ctx, tx, actor, f.id)
}

func itoa(n int) string { return strconv.Itoa(n) }

// jsonParam codifica un valore per una colonna jsonb: pgx passerebbe un testo Go così
// com'è, come JSON già scritto.
func jsonParam(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// DeleteField elimina un campo senza valori; un campo usato si archivia.
func (s *Service) DeleteField(ctx context.Context, tx pgx.Tx, actor Actor, ref string) error {
	if err := requireFieldsAdmin(actor); err != nil {
		return err
	}
	f, err := loadFieldRow(ctx, tx, ref, true)
	if err != nil {
		return err
	}
	if f.system {
		return invalid("field", "un campo di sistema non si elimina: archivialo")
	}
	var n int64
	if err := tx.QueryRow(ctx, `select count(distinct element_id) from element_values where field_id = $1`, f.id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return invalid("field", "il campo ha valori in %d elementi: non si elimina, si archivia (i valori restano)", n)
	}
	if _, err := tx.Exec(ctx, `delete from field_defs where id = $1`, f.id); err != nil {
		return err
	}
	if f.optionSetID != nil {
		// L'insieme di valori se ne va con l'ultimo campo che lo usa (gli stati non sono qui).
		if _, err := tx.Exec(ctx, `delete from option_sets os where os.id = $1 and os.kind <> 'state'
			and not exists (select 1 from field_defs where option_set_id = os.id)
			and not exists (select 1 from element_types where state_set_id = os.id)`, *f.optionSetID); err != nil {
			return err
		}
	}
	return auditField(ctx, tx, actor, "field.delete", f.id, map[string]any{"key": f.key})
}

// SaveOptions salva l'elenco delle opzioni del campo nell'ordine dato: aggiorna quelle con
// ID, aggiunge le nuove; quelle tolte si eliminano se nessun elemento le usa, altrimenti si
// archiviano (i valori restano leggibili).
func (s *Service) SaveOptions(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in []OptionInput) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	f, err := loadFieldRow(ctx, tx, ref, true)
	if err != nil {
		return nil, err
	}
	if f.optionSetID == nil {
		return nil, invalid("options", "il campo «%s» non è a scelta", f.name)
	}
	opts, err := cleanOptions(in)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select id::text from options where option_set_id = $1 for update`, *f.optionSetID)
	if err != nil {
		return nil, err
	}
	existing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, o := range opts {
		if o.ID != "" {
			if !slices.Contains(existing, o.ID) {
				return nil, invalid("options", "opzione %q non appartiene a questo campo", o.Name)
			}
			kept[o.ID] = true
		}
	}
	removed, archived := 0, 0
	for _, id := range existing {
		if kept[id] {
			continue
		}
		var used bool
		var oname string
		if err := tx.QueryRow(ctx, `select name, exists (select 1 from element_values where v_ref = $1)
			or exists (select 1 from elements where state_id = $1) from options where id = $1`, id).Scan(&oname, &used); err != nil {
			return nil, err
		}
		if used {
			// Un'opzione usata non si elimina: resta archiviata, in fondo, e i valori restano leggibili.
			for _, o := range opts {
				if strings.EqualFold(o.Name, oname) {
					return nil, invalid("options", "«%s» è un'opzione usata che stai togliendo: tienila (anche archiviata) invece di crearne una nuova con lo stesso nome", oname)
				}
			}
			if _, err := tx.Exec(ctx, `update options set archived = true, ordinal = 100000 where id = $1`, id); err != nil {
				return nil, err
			}
			archived++
			continue
		}
		if _, err := tx.Exec(ctx, `delete from options where id = $1`, id); err != nil {
			return nil, err
		}
		removed++
	}
	// Prima si liberano i nomi delle opzioni da riscrivere (gli scambi di nome non violano
	// l'unicità), poi si scrivono nell'ordine dato.
	ids := make([]string, 0, len(kept))
	for id := range kept {
		ids = append(ids, id)
	}
	if _, err := tx.Exec(ctx, `update options set name = id::text where id = any($1::uuid[])`, ids); err != nil {
		return nil, err
	}
	for i, o := range opts {
		if o.ID != "" {
			if _, err := tx.Exec(ctx, `update options set name = $2, color = nullif($3, ''), ordinal = $4, archived = $5, labels = $6 where id = $1`,
				o.ID, o.Name, o.Color, i, o.Archived, labelsJSON(o.Labels)); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `insert into options (tenant_id, id, option_set_id, name, color, ordinal, archived, labels)
			values (app_tenant(), $1, $2, $3, nullif($4, ''), $5, $6, $7)`, db.NewID(), *f.optionSetID, o.Name, o.Color, i, o.Archived, labelsJSON(o.Labels)); err != nil {
			return nil, err
		}
	}
	if err := auditField(ctx, tx, actor, "field.options", f.id, map[string]any{"key": f.key, "options": len(opts),
		"removed": removed, "archived": archived}); err != nil {
		return nil, err
	}
	return s.GetField(ctx, tx, actor, f.id)
}

// AssociateField associa il campo a un tipo di elemento o ne cambia le regole.
func (s *Service) AssociateField(ctx context.Context, tx pgx.Tx, actor Actor, ref, typeKey string, in AssocInput) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	f, err := loadFieldRow(ctx, tx, ref, false)
	if err != nil {
		return nil, err
	}
	if f.archived {
		return nil, invalid("field", "il campo «%s» è archiviato: ripristinalo prima di associarlo", f.name)
	}
	t, err := LoadType(ctx, tx, typeKey)
	if err != nil {
		return nil, err
	}
	in.VisibleWhen = strings.TrimSpace(in.VisibleWhen)
	if in.VisibleWhen != "" {
		if _, err := ParseCondition(in.VisibleWhen); err != nil {
			return nil, InvalidValue("visible_when", "condizione non valida: %v", err)
		}
		if len(in.VisibleWhen) > 2000 {
			return nil, invalid("visible_when", "condizione troppo lunga")
		}
	}
	in.RequiredFrom = strings.TrimSpace(in.RequiredFrom)
	if in.RequiredFrom != "" {
		idx := mainPathIndex(t.States, in.RequiredFrom)
		if idx < 0 {
			var names []string
			for _, st := range t.States {
				if !st.OffPath {
					names = append(names, st.Name)
				}
			}
			if len(names) == 0 {
				return nil, invalid("required_from", "il tipo %s non ha fasi", t.Name)
			}
			return nil, NotFound("required_from", "", "fase %q inesistente nel percorso del tipo (%s)", in.RequiredFrom, strings.Join(names, ", "))
		}
		for _, st := range t.States {
			if strings.EqualFold(st.Name, in.RequiredFrom) {
				in.RequiredFrom = st.Name // con le maiuscole del processo
			}
		}
	}
	section := strings.Join(strings.Fields(in.Section), " ")
	if utf8.RuneCountInString(section) > 60 {
		return nil, TooLong("section", 60, "sezione troppo lunga (massimo 60 caratteri)")
	}
	var assocID string
	err = tx.QueryRow(ctx, `select id::text from type_fields where type_id = $1 and field_id = $2 and space_id is null`, t.ID, f.id).Scan(&assocID)
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		return nil, err
	}
	ordinal := 0
	if in.Ordinal != nil {
		ordinal = *in.Ordinal
	} else if isNew {
		if err := tx.QueryRow(ctx, `select coalesce(max(ordinal) + 1, 0) from type_fields where type_id = $1 and space_id is null`, t.ID).Scan(&ordinal); err != nil {
			return nil, err
		}
	}
	if isNew {
		_, err = tx.Exec(ctx, `insert into type_fields (tenant_id, id, type_id, field_id, required, required_from, visible_when, section, ordinal)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, db.NewID(), t.ID, f.id, in.Required, in.RequiredFrom, in.VisibleWhen, section, ordinal)
	} else if in.Ordinal != nil {
		_, err = tx.Exec(ctx, `update type_fields set required = $2, required_from = $3, visible_when = $4, section = $5, ordinal = $6 where id = $1`,
			assocID, in.Required, in.RequiredFrom, in.VisibleWhen, section, ordinal)
	} else {
		_, err = tx.Exec(ctx, `update type_fields set required = $2, required_from = $3, visible_when = $4, section = $5 where id = $1`,
			assocID, in.Required, in.RequiredFrom, in.VisibleWhen, section)
	}
	if err != nil {
		return nil, err
	}
	if err := auditField(ctx, tx, actor, "field.associate", f.id, map[string]any{"key": f.key, "type": t.Key, "new": isNew,
		"required": in.Required, "required_from": in.RequiredFrom, "visible_when": in.VisibleWhen, "section": section}); err != nil {
		return nil, err
	}
	return s.GetField(ctx, tx, actor, f.id)
}

// DissociateField toglie il campo da un tipo. I valori già salvati negli elementi restano
// (e restano cercabili): tornano visibili se il campo si riassocia.
func (s *Service) DissociateField(ctx context.Context, tx pgx.Tx, actor Actor, ref, typeKey string) (*FieldConfig, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	f, err := loadFieldRow(ctx, tx, ref, false)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `delete from type_fields tf using element_types t
		where t.tenant_id = tf.tenant_id and t.id = tf.type_id and t.key = $1 and tf.field_id = $2 and tf.space_id is null`, typeKey, f.id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := auditField(ctx, tx, actor, "field.dissociate", f.id, map[string]any{"key": f.key, "type": typeKey}); err != nil {
		return nil, err
	}
	return s.GetField(ctx, tx, actor, f.id)
}

// ReorderTypeFields fissa l'ordine dei campi di un tipo (chiavi nell'ordine voluto).
func (s *Service) ReorderTypeFields(ctx context.Context, tx pgx.Tx, actor Actor, typeKey string, keys []string) error {
	if err := requireFieldsAdmin(actor); err != nil {
		return err
	}
	t, err := LoadType(ctx, tx, typeKey)
	if err != nil {
		return err
	}
	for i, k := range keys {
		tag, err := tx.Exec(ctx, `update type_fields tf set ordinal = $3 from field_defs f
			where f.tenant_id = tf.tenant_id and f.id = tf.field_id and tf.type_id = $1 and f.key = $2 and tf.space_id is null`, t.ID, k, i)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return invalid("fields", "il campo %q non è associato al tipo %s", k, t.Name)
		}
	}
	return audit.Append(ctx, tx, audit.Event{Action: "fields.reorder", EntityType: "element_type", EntityID: t.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"type": t.Key, "fields": keys}})
}

// FieldCatalog sono i dati per l'editor: tipi con le fasi, gruppi, valuta base.
type FieldCatalog struct {
	Types        []FieldCatalogType `json:"types"`
	Groups       []string           `json:"groups"`
	BaseCurrency string             `json:"base_currency"`
	DataTypes    []string           `json:"data_types"`
	MultiTypes   []string           `json:"multi_types"`
	Colors       []string           `json:"colors"`
	OptionSets   []CatalogSet       `json:"option_sets"`
}

// FieldCatalogType è un tipo di elemento con le fasi del processo corrente.
type FieldCatalogType struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Module string  `json:"module"`
	Phases []State `json:"phases"`
}

// CatalogSet è un insieme di valori condivisibile tra campi a scelta.
type CatalogSet struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Fields int    `json:"fields"`
}

// FieldsCatalog restituisce i riferimenti per l'editor dei campi.
func (s *Service) FieldsCatalog(ctx context.Context, tx pgx.Tx, actor Actor) (*FieldCatalog, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	out := &FieldCatalog{DataTypes: FieldDataTypes, MultiTypes: multiTypes, Colors: OptionColors}
	rows, err := tx.Query(ctx, `select key from element_types order by name`)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		t, err := LoadType(ctx, tx, k)
		if err != nil {
			return nil, err
		}
		out.Types = append(out.Types, FieldCatalogType{Key: t.Key, Name: t.Name, Module: t.Module, Phases: t.States})
	}
	rows, err = tx.Query(ctx, `select name from groups order by lower(name)`)
	if err != nil {
		return nil, err
	}
	if out.Groups, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select os.id::text, os.name, os.kind, count(f.id)::int from option_sets os
		join field_defs f on f.tenant_id = os.tenant_id and f.option_set_id = os.id
		where os.kind in ('enum', 'version') group by os.id, os.name, os.kind order by lower(os.name)`)
	if err != nil {
		return nil, err
	}
	if out.OptionSets, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogSet]); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `select base_currency from tenants where id = app_tenant()`).Scan(&out.BaseCurrency); err != nil {
		return nil, err
	}
	if out.Types == nil {
		out.Types = []FieldCatalogType{}
	}
	return out, nil
}
