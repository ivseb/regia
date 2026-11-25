package importer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Mappatura proposta: quello che si può dedurre da nomi ed email, sui dati del tenant.
// L'utente la corregge nella procedura guidata.

func suggest(ctx context.Context, tx pgx.Tx, a *Analysis) (*Mapping, error) {
	m := &Mapping{Columns: a.Columns, DateFormat: a.DateFormat, Types: map[string]string{}, States: map[string]map[string]string{},
		Users: map[string]UserTarget{}, Fields: map[string]FieldTarget{}, Sprints: map[string]SprintTarget{}, Links: map[string]string{},
		Include: Include{Comments: true, Worklogs: true, Attachments: a.Format == "api", Links: true, Sprints: true}}
	if err := tx.QueryRow(ctx, `select coalesce((select default_timezone from tenants where id = app_tenant()), 'Europe/Rome')`).Scan(&m.Timezone); err != nil {
		return nil, err
	}
	// Spazio: quello con la chiave del progetto, se è di un team; altrimenti uno nuovo.
	key := a.KeyPrefix
	team := key
	if key == "" {
		key, team = "IMP", "Importati"
	}
	var spaceID string
	err := tx.QueryRow(ctx, `select s.id::text from spaces s join teams t on t.tenant_id = s.tenant_id and t.delivery_space_id = s.id where s.key = $1`, key).Scan(&spaceID)
	switch {
	case err == nil:
		m.Space = SpaceTarget{Mode: "existing", Key: key}
	case errors.Is(err, pgx.ErrNoRows):
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from spaces where key = $1)`, key).Scan(&used); err != nil {
			return nil, err
		}
		if used {
			key = work.ProposeKey(team) + "I"
		}
		m.Space = SpaceTarget{Mode: "new", Key: key, TeamName: team, KeepNumbers: a.KeyPrefix != "" && !used}
	default:
		return nil, err
	}
	// Tipi e fasi.
	types := map[string]*engine.TypeInfo{}
	for _, t := range WorkTypes {
		ti, err := engine.LoadType(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		types[t] = ti
	}
	for _, t := range a.Types {
		m.Types[t.Name] = defaultType(t.Name)
	}
	for _, st := range a.States {
		for _, src := range st.Types {
			t := m.Types[src]
			if t == "" {
				continue
			}
			if m.States[t] == nil {
				m.States[t] = map[string]string{}
			}
			if _, done := m.States[t][st.Name]; !done {
				m.States[t][st.Name] = defaultPhase(&typeTarget{info: types[t]}, st.Name, st.Category)
			}
		}
	}
	// Persone: per email, poi per nome, poi per nome utente uguale alla parte locale dell'email.
	rows, err := tx.Query(ctx, `select id::text, display_name, coalesce(lower(email), '') from members
		where status in ('active', 'invited') and member_type <> 'service'`)
	if err != nil {
		return nil, err
	}
	type mem struct{ id, name, email string }
	var members []mem
	var x mem
	if _, err := pgx.ForEachRow(rows, []any{&x.id, &x.name, &x.email}, func() error { members = append(members, x); return nil }); err != nil {
		return nil, err
	}
	for _, u := range a.Users {
		t := UserTarget{Mode: "placeholder"}
		for _, mm := range members {
			local, _, _ := strings.Cut(mm.email, "@")
			if (u.Email != "" && strings.EqualFold(mm.email, u.Email)) || strings.EqualFold(mm.email, u.Ref) {
				t = UserTarget{Mode: "member", MemberID: mm.id}
				break
			}
			if t.Mode != "member" && (strings.EqualFold(mm.name, u.Name) || (local != "" && strings.EqualFold(local, u.Ref))) {
				t = UserTarget{Mode: "member", MemberID: mm.id}
			}
		}
		m.Users[u.Ref] = t
	}
	// Campi: per nome, chiave o alias; i più comuni hanno già un posto (Punti).
	rows, err = tx.Query(ctx, `select key, name, data_type, is_multi, aliases from field_defs where not archived`)
	if err != nil {
		return nil, err
	}
	type fd struct {
		key, name, dt string
		multi         bool
		aliases       []string
	}
	var defs []fd
	var f fd
	if _, err := pgx.ForEachRow(rows, []any{&f.key, &f.name, &f.dt, &f.multi, &f.aliases}, func() error { defs = append(defs, f); return nil }); err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, d := range defs {
		taken[d.key] = true
	}
	prefix := map[string]string{"jira": "jira_", "youtrack": "yt_"}[a.System]
	if prefix == "" {
		prefix = "csv_"
	}
	compatible := func(dt, want string) bool {
		switch dt {
		case "money", "group", "element", "version", "state":
			return false
		}
		return dt == want || dt == "string" || dt == "text" || (dt == "decimal" && want == "integer") || (dt == "enum" && want == "string")
	}
	for _, fi := range a.Fields {
		low := strings.ToLower(fi.Name)
		var match *fd
		for i := range defs {
			d := &defs[i]
			if strings.EqualFold(d.name, fi.Name) || strings.EqualFold(d.key, fi.Name) || containsFold(d.aliases, low) {
				match = d
				break
			}
		}
		if match == nil && (low == "story points" || low == "story point estimate" || low == "punti") {
			for i := range defs {
				if defs[i].key == "punti" {
					match = &defs[i]
				}
			}
		}
		if match != nil && compatible(match.dt, fi.DataType) {
			m.Fields[fi.Name] = FieldTarget{Mode: "existing", Key: match.key}
			continue
		}
		k := prefix + slug(fi.Name)
		for i := 2; taken[k]; i++ {
			k = prefix + slug(fi.Name) + "_" + itoa(i)
		}
		taken[k] = true
		multi := fi.Multi && (fi.DataType == "enum" || fi.DataType == "string" || fi.DataType == "url")
		m.Fields[fi.Name] = FieldTarget{Mode: "new", Key: k, Name: fi.Name, DataType: fi.DataType, Multi: multi}
	}
	// Priorità e etichette.
	for _, d := range defs {
		if d.key == "priorita" && d.dt == "enum" {
			m.Priority.Field = d.key
		}
	}
	if m.Priority.Field == "" && len(a.Priorities) > 0 {
		for _, d := range defs {
			if d.dt == "enum" && (strings.EqualFold(d.name, "priorità") || strings.EqualFold(d.name, "priority") || containsFold(d.aliases, "priority")) {
				m.Priority.Field = d.key
			}
		}
	}
	if m.Priority.Field != "" {
		opts, err := optionNames(ctx, tx, m.Priority.Field)
		if err != nil {
			return nil, err
		}
		m.Priority.Values = map[string]string{}
		for _, pr := range a.Priorities {
			m.Priority.Values[pr.Name] = defaultPriority(&fieldMeta{optionNames: opts}, pr.Name)
		}
	}
	m.Labels = FieldTarget{Mode: "ignore"}
	if a.LabelsTotal > 0 {
		m.Labels = FieldTarget{Mode: "new", Key: "etichette", Name: "Etichette", DataType: "enum", Multi: true}
		for _, d := range defs {
			if d.key == "etichette" || strings.EqualFold(d.name, "etichette") || strings.EqualFold(d.name, "labels") {
				if d.dt == "enum" || d.dt == "string" {
					m.Labels = FieldTarget{Mode: "existing", Key: d.key}
				} else {
					m.Labels.Key = "etichette_import"
				}
			}
		}
		if m.Labels.Mode == "new" && a.LabelsTotal > 500 {
			m.Labels.DataType = "string"
		}
	}
	// Sprint: chiusi se non hanno elementi aperti; l'ultimo con elementi aperti è attivo.
	lastOpen := ""
	for _, sp := range a.Sprints {
		if sp.State == "" && sp.Open > 0 {
			lastOpen = sp.Name
		}
	}
	for _, sp := range a.Sprints {
		t := SprintTarget{State: map[string]string{"future": "planned", "active": "active", "closed": "closed"}[strings.ToLower(sp.State)]}
		if t.State == "" {
			switch {
			case sp.Open == 0:
				t.State = "closed"
			case sp.Name == lastOpen:
				t.State = "active"
			default:
				t.State = "planned"
			}
		}
		t.Start, t.End = isoDay(sp.Start, m), isoDay(sp.End, m)
		m.Sprints[sp.Name] = t
	}
	for _, l := range a.LinkTypes {
		m.Links[l.Name] = defaultRelation(l.Name)
	}
	return m, nil
}

func isoDay(v string, m *Mapping) string {
	if v == "" {
		return ""
	}
	t, err := parseDate(v, m.DateFormat, m.location())
	if err != nil {
		return ""
	}
	return dayOnly(t, m.location()).Format(time.DateOnly)
}

func optionNames(ctx context.Context, tx pgx.Tx, fieldKey string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `select o.name from options o join field_defs f on f.tenant_id = o.tenant_id and f.option_set_id = o.option_set_id
		where f.key = $1 and not o.archived`, fieldKey)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var n string
	_, err = pgx.ForEachRow(rows, []any{&n}, func() error { out[strings.ToLower(n)] = n; return nil })
	return out, err
}

// slug: chiave di un campo dal nome (minuscole ASCII, cifre e _).
func slug(name string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(name) {
		if base, ok := accents[r]; ok {
			r = base
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 && (unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)) {
			b.WriteByte('_')
			under = true
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		s = "campo"
	}
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "_")
	}
	return s
}

var accents = map[rune]rune{'à': 'a', 'á': 'a', 'â': 'a', 'ä': 'a', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'ö': 'o', 'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ç': 'c', 'ñ': 'n', 'σ': 's'}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------
// Catalogo per la procedura guidata

type CatalogSpace struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Team string `json:"team"`
}

type CatalogType struct {
	Key    string         `json:"key"`
	Name   string         `json:"name"`
	Phases []engine.State `json:"phases"`
}

type CatalogMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email,omitempty"`
	Status string `json:"status"`
}

type CatalogField struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	DataType string   `json:"data_type"`
	Multi    bool     `json:"multi"`
	Options  []string `json:"options,omitempty"`
}

type CatalogRelation struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Catalog struct {
	Spaces      []CatalogSpace      `json:"spaces"`
	Types       []CatalogType       `json:"types"`
	Members     []CatalogMember     `json:"members"`
	Fields      []CatalogField      `json:"fields"`
	Relations   []CatalogRelation   `json:"relations"`
	DateFormats []DateFormat        `json:"date_formats"`
	Roles       map[string]string   `json:"roles"`
	DataTypes   []string            `json:"data_types"`
	Profiles    []Profile           `json:"profiles"`
	Sources     []map[string]string `json:"sources"`
}

// GetCatalog restituisce i dati del tenant che servono alla mappatura.
func (s *Service) GetCatalog(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Catalog, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	c := &Catalog{DateFormats: DateFormats, Roles: RoleNames, DataTypes: creatable,
		Sources: []map[string]string{
			{"key": SourceJiraCSV, "name": "Jira, file CSV"}, {"key": SourceYouTrackCSV, "name": "YouTrack, file CSV"},
			{"key": SourceCSV, "name": "CSV generico"}, {"key": SourceJiraAPI, "name": "Jira, API REST"}, {"key": SourceYouTrackAPI, "name": "YouTrack, API REST"}}}
	rows, err := tx.Query(ctx, `select s.key, s.name, t.name from spaces s join teams t on t.tenant_id = s.tenant_id and t.delivery_space_id = s.id
		where not s.archived order by s.key`)
	if err != nil {
		return nil, err
	}
	if c.Spaces, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogSpace]); err != nil {
		return nil, err
	}
	for _, t := range WorkTypes {
		ti, err := engine.LoadType(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		c.Types = append(c.Types, CatalogType{Key: ti.Key, Name: ti.Name, Phases: ti.States})
	}
	rows, err = tx.Query(ctx, `select id::text, display_name, coalesce(email, ''), status from members
		where status in ('active', 'invited') and member_type <> 'service' order by lower(display_name)`)
	if err != nil {
		return nil, err
	}
	if c.Members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogMember]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select f.key, f.name, f.data_type, f.is_multi,
		coalesce((select array_agg(o.name order by o.ordinal) from options o where o.option_set_id = f.option_set_id and not o.archived), '{}')
		from field_defs f where not f.archived and f.data_type not in ('money', 'group', 'element', 'version', 'state') order by lower(f.name)`)
	if err != nil {
		return nil, err
	}
	if c.Fields, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogField]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select key, outward_name from relation_types where key <> all($1) order by key`,
		[]string{work.RelPlannedIn, "composed_of", "nasce_da", "origin"})
	if err != nil {
		return nil, err
	}
	if c.Relations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CatalogRelation]); err != nil {
		return nil, err
	}
	for _, r := range builtinRelations {
		found := false
		for _, x := range c.Relations {
			found = found || x.Key == r.key
		}
		if !found {
			c.Relations = append(c.Relations, CatalogRelation{Key: r.key, Name: r.out})
		}
	}
	if c.Profiles, err = s.Profiles(ctx, tx, actor); err != nil {
		return nil, err
	}
	return c, nil
}

// ---------------------------------------------------------------------
// Profili di mappatura (import_profiles, target work_items)

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	Mapping   Mapping   `json:"mapping"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Service) Profiles(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Profile, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select id::text, name, coalesce(options->>'source', ''), mapping, updated_at from import_profiles
		where target = $1 order by lower(name)`, Target)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Profile, error) {
		var p Profile
		var raw []byte
		if err := r.Scan(&p.ID, &p.Name, &p.Source, &raw, &p.UpdatedAt); err != nil {
			return p, err
		}
		return p, json.Unmarshal(raw, &p.Mapping)
	})
	if out == nil {
		out = []Profile{}
	}
	return out, err
}

// ProfileInput salva la mappatura di un'esecuzione (o una indicata) come profilo.
type ProfileInput struct {
	Name    string   `json:"name"`
	RunID   string   `json:"run_id"`
	Mapping *Mapping `json:"mapping"`
}

func (s *Service) SaveProfile(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ProfileInput) (*Profile, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 120 {
		return nil, engine.TooLong("name", 120, "serve un nome (al massimo 120 caratteri)")
	}
	m := in.Mapping
	source := ""
	if in.RunID != "" {
		r, err := s.run(ctx, tx, in.RunID)
		if err != nil {
			return nil, err
		}
		if m == nil {
			m = r.Mapping
		}
		source = r.Source
	}
	if m == nil {
		return nil, engine.Required("mapping", "manca la mappatura")
	}
	// Nel profilo non vanno le date degli sprint di questo export.
	clean := *m
	clean.Sprints = nil
	opts := jsonb(map[string]string{"source": source})
	if id == "" {
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into import_profiles (tenant_id, id, name, target, mapping, options, created_by) values (app_tenant(), $1, $2, $3, $4, $5, $6)`,
			id, name, Target, jsonb(clean), opts, nullable(actor.MemberID)); err != nil {
			if strings.Contains(err.Error(), "import_profiles_name_uq") {
				return nil, engine.Duplicate("name", "esiste già un profilo con questo nome")
			}
			return nil, err
		}
	} else {
		tag, err := tx.Exec(ctx, `update import_profiles set name = $2, mapping = $3, options = $4, updated_at = now() where id::text = $1 and target = $5`,
			id, name, jsonb(clean), opts, Target)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, engine.ErrNotFound
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "import.profile", EntityType: "import_profile", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	list, err := s.Profiles(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Service) DeleteProfile(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `delete from import_profiles where id::text = $1 and target = $2`, id, Target)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return nil
}

// ApplyProfile applica un profilo all'esecuzione: le voci del profilo prendono il posto di
// quelle proposte, il resto (persone nuove, stati nuovi) resta come proposto.
func (s *Service) ApplyProfile(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID, profileID string) (*MappingResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	r, err := s.run(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if r.Analysis == nil {
		return nil, engine.InvalidCode("validation.importer.analysis_not_ready", "status", "l'analisi non è ancora pronta")
	}
	list, err := s.Profiles(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	var prof *Profile
	for i := range list {
		if list[i].ID == profileID {
			prof = &list[i]
		}
	}
	if prof == nil {
		return nil, engine.ErrNotFound
	}
	base, err := suggest(ctx, tx, r.Analysis)
	if err != nil {
		return nil, err
	}
	pm := prof.Mapping
	if len(pm.Columns) == len(r.Analysis.Header) && len(pm.Columns) > 0 {
		base.Columns = pm.Columns
	}
	if pm.DateFormat != "" {
		base.DateFormat = pm.DateFormat
	}
	if pm.Timezone != "" {
		base.Timezone = pm.Timezone
	}
	if pm.Space.Mode != "" {
		base.Space = pm.Space
	}
	for k, v := range pm.Types {
		if _, ok := base.Types[k]; ok {
			base.Types[k] = v
		}
	}
	for t, states := range pm.States {
		if base.States[t] == nil {
			base.States[t] = map[string]string{}
		}
		for k, v := range states {
			base.States[t][k] = v
		}
	}
	for k, v := range pm.Users {
		if _, ok := base.Users[k]; ok {
			base.Users[k] = v
		}
	}
	for k, v := range pm.Fields {
		if _, ok := base.Fields[k]; ok {
			base.Fields[k] = v
		}
	}
	if pm.Priority.Field != "" {
		base.Priority.Field = pm.Priority.Field
		for k, v := range pm.Priority.Values {
			base.Priority.Values[k] = v
		}
	}
	if pm.Labels.Mode != "" {
		base.Labels = pm.Labels
	}
	for k, v := range pm.Links {
		base.Links[k] = v
	}
	base.Include = pm.Include
	if _, err := tx.Exec(ctx, `update import_runs set profile_id = $2 where id = $1`, r.ID, prof.ID); err != nil {
		return nil, err
	}
	return s.SaveMapping(ctx, tx, actor, r.ID, *base)
}
