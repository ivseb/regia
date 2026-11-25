package architecture

// Ciclo di vita e razionalizzazione (replica/architettura-ea.md §5): valutazione TIME
// delle applicazioni, tecnologie con fine supporto e fine vita, vista delle applicazioni
// da razionalizzare, profilo architetturale di un elemento.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

var timeNames = map[string]string{"tolerate": "tollerare", "invest": "investire", "migrate": "migrare", "eliminate": "eliminare"}

func normTime(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for en, it := range timeNames {
		if s == en || s == it || (len(s) == 1 && s[0] == en[0]) {
			return en, true
		}
	}
	return "", s == ""
}

type TimeInput struct {
	Rating string `json:"time_rating"` // "" = nessuna valutazione
	Note   string `json:"note"`
}

// SetTime registra la valutazione TIME di un'applicazione con la motivazione.
func (s *Service) SetTime(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in TimeInput) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	r, ok := normTime(in.Rating)
	if !ok {
		return engine.InvalidValue("time_rating", "valore non ammesso (tollerare, investire, migrare, eliminare)")
	}
	if len(in.Note) > 2000 {
		return engine.TooLong("note", 2000, "motivazione troppo lunga (massimo 2000 caratteri)")
	}
	n, err := resolveNode(ctx, tx, ref, []string{AppType})
	if err != nil || n == nil {
		return firstErr(err, engine.ErrNotFound)
	}
	var old *string
	if err := tx.QueryRow(ctx, `select time_rating from ext_application where element_id = $1`, n.ID).Scan(&old); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update ext_application set time_rating = nullif($2, ''), time_note = $3,
		time_rated_at = case when nullif($2, '') is distinct from time_rating then now() else time_rated_at end where element_id = $1`,
		n.ID, r, strings.TrimSpace(in.Note)); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, n.ID, actor.MemberID, "field", "time_rating", nullable(r), old); err != nil {
		return err
	}
	return s.auditEA(ctx, tx, actor, "application.time", AppType, n.ID, map[string]any{"time_rating": r})
}

// ---------------------------------------------------------------------------
// Tecnologie

type Technology struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Vendor       string `json:"vendor"`
	Category     string `json:"category"`
	EndOfSupport *Day   `json:"end_of_support"`
	EndOfLife    *Day   `json:"end_of_life"`
	Notes        string `json:"notes"`
	Apps         int    `json:"applications"`
	// DaysLeft: giorni alla prima scadenza tra fine supporto e fine vita (negativi = passata).
	DaysLeft *int `json:"days_left"`
}

func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Day è una data senza ora: in JSON "AAAA-MM-GG".
type Day struct{ time.Time }

func (d Day) MarshalJSON() ([]byte, error) { return []byte(`"` + d.Format(time.DateOnly) + `"`), nil }

func (d *Day) Scan(src any) error {
	t, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("data non valida: %T", src)
	}
	d.Time = t
	return nil
}

func daysLeft(dates ...*Day) *int {
	var best *int
	for _, d := range dates {
		if d == nil {
			continue
		}
		n := int(d.Sub(today()).Hours() / 24)
		if best == nil || n < *best {
			best = &n
		}
	}
	return best
}

func (s *Service) Technologies(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Technology, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `select t.id, t.name, t.version, t.vendor, t.category, t.end_of_support, t.end_of_life, t.notes,
		(select count(*) from application_technologies a where a.technology_id = t.id)
		from technologies t order by lower(t.name), t.version`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Technology, error) {
		var t Technology
		err := r.Scan(&t.ID, &t.Name, &t.Version, &t.Vendor, &t.Category, &t.EndOfSupport, &t.EndOfLife, &t.Notes, &t.Apps)
		t.DaysLeft = daysLeft(t.EndOfSupport, t.EndOfLife)
		return t, err
	})
}

type TechnologyInput struct {
	Name         *string `json:"name"`
	Version      *string `json:"version"`
	Vendor       *string `json:"vendor"`
	Category     *string `json:"category"`
	EndOfSupport *string `json:"end_of_support"` // AAAA-MM-GG, "" = nessuna
	EndOfLife    *string `json:"end_of_life"`
	Notes        *string `json:"notes"`
}

func parseDate(field string, p *string) (any, bool, error) {
	if p == nil {
		return nil, false, nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil, true, nil
	}
	d, err := time.Parse(time.DateOnly, v)
	if err != nil {
		if d, err = time.Parse("02/01/2006", v); err != nil {
			return nil, false, engine.DateInvalid(field, "data %q non valida (AAAA-MM-GG)", v)
		}
	}
	return d, true, nil
}

func (in *TechnologyInput) clean() error {
	for name, p := range map[string]*string{"name": in.Name, "version": in.Version, "vendor": in.Vendor, "category": in.Category} {
		if p != nil {
			*p = strings.TrimSpace(*p)
			max := 120
			if name == "version" || name == "category" {
				max = 60
			}
			if len(*p) > max {
				return engine.TooLong(name, max, "massimo %d caratteri", max)
			}
		}
	}
	if in.Name != nil && *in.Name == "" {
		return engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	return nil
}

func (s *Service) SaveTechnology(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in TechnologyInput) (*Technology, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := in.clean(); err != nil {
		return nil, err
	}
	eos, setEOS, err := parseDate("end_of_support", in.EndOfSupport)
	if err != nil {
		return nil, err
	}
	eol, setEOL, err := parseDate("end_of_life", in.EndOfLife)
	if err != nil {
		return nil, err
	}
	if id == "" {
		if in.Name == nil {
			return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
		}
		id = db.NewID()
		_, err = tx.Exec(ctx, `insert into technologies (tenant_id, id, name, version, vendor, category, end_of_support, end_of_life, notes)
			values (app_tenant(), $1, $2, coalesce($3, ''), coalesce($4, ''), coalesce($5, ''), $6, $7, coalesce($8, ''))`,
			id, *in.Name, in.Version, in.Vendor, in.Category, eos, eol, in.Notes)
	} else {
		if !uuidRe.MatchString(id) {
			return nil, engine.ErrNotFound
		}
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `update technologies set name = coalesce($2, name), version = coalesce($3, version), vendor = coalesce($4, vendor),
			category = coalesce($5, category), end_of_support = case when $6 then $7::date else end_of_support end,
			end_of_life = case when $8 then $9::date else end_of_life end, notes = coalesce($10, notes), updated_at = now() where id = $1`,
			id, in.Name, in.Version, in.Vendor, in.Category, setEOS, eos, setEOL, eol, in.Notes)
		if err == nil && tag.RowsAffected() == 0 {
			return nil, engine.ErrNotFound
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "technologies_name_uq") {
			return nil, engine.Duplicate("name", "esiste già questa tecnologia in questa versione")
		}
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "technology.save", "technology", id, nil); err != nil {
		return nil, err
	}
	list, err := s.Technologies(ctx, tx, engine.Actor{Permissions: []string{"element.read"}})
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Service) DeleteTechnology(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `delete from technologies where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return s.auditEA(ctx, tx, actor, "technology.delete", "technology", id, nil)
}

// SetTechnologies sostituisce le tecnologie usate da un'applicazione.
func (s *Service) SetTechnologies(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, ids []string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	n, err := resolveNode(ctx, tx, ref, []string{AppType})
	if err != nil || n == nil {
		return firstErr(err, engine.ErrNotFound)
	}
	for _, id := range ids {
		if !uuidRe.MatchString(id) {
			return engine.NotFound("technology_ids", "", "tecnologia %q inesistente", id)
		}
	}
	if _, err := tx.Exec(ctx, `delete from application_technologies where application_id = $1 and not technology_id = any($2::uuid[])`, n.ID, ids); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `insert into application_technologies (tenant_id, application_id, technology_id)
		select app_tenant(), $1, t.id from technologies t where t.id = any($2::uuid[]) on conflict do nothing`, n.ID, ids)
	if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from technologies where id = any($1::uuid[])`, ids).Scan(&count); err != nil {
		return err
	}
	if count != len(uniq(ids)) {
		return engine.InvalidCode("validation.architecture.technologies_missing", "technology_ids", "una o più tecnologie inesistenti")
	}
	_ = tag
	if err := engine.AddActivity(ctx, tx, n.ID, actor.MemberID, "field", "technologies", map[string]any{"technologies": ids}, nil); err != nil {
		return err
	}
	return s.auditEA(ctx, tx, actor, "application.technologies", AppType, n.ID, map[string]any{"count": len(ids)})
}

func uniq(ss []string) []string {
	m := map[string]bool{}
	out := []string{}
	for _, s := range ss {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Razionalizzazione

type RatApp struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Code        *string `json:"code"`
	Team        *string `json:"team"`
	Criticality *string `json:"criticality"`
	DORA        bool    `json:"dora"`
	Time        *string `json:"time"`
	TimeNote    string  `json:"time_note"`
	State       string  `json:"state"`
	Dependents  int     `json:"dependents"`
}

type Duplicate struct {
	CapabilityID   string   `json:"capability_id"`
	CapabilityKey  string   `json:"capability_key"`
	CapabilityName string   `json:"capability_name"`
	Path           []string `json:"path"`
	Apps           []RatApp `json:"applications"`
}

type EndOfSupport struct {
	App          RatApp `json:"application"`
	TechnologyID string `json:"technology_id"`
	Technology   string `json:"technology"`
	EndOfSupport *Day   `json:"end_of_support"`
	EndOfLife    *Day   `json:"end_of_life"`
	DaysLeft     int    `json:"days_left"`
}

type Rationalization struct {
	Settings     Settings       `json:"settings"`
	TimeCounts   map[string]int `json:"time_counts"` // tolerate, invest, migrate, eliminate, none
	Apps         []RatApp       `json:"applications"`
	Duplicates   []Duplicate    `json:"duplicates"`
	EndOfSupport []EndOfSupport `json:"end_of_support"`
	// Orphans: applicazioni in esercizio che non supportano alcuna capability.
	Orphans []RatApp `json:"orphans"`
	// RetiringWithDependents: da migrare o eliminare, ma qualcuno ne dipende ancora.
	RetiringWithDependents []RatApp `json:"retiring_with_dependents"`
}

func (s *Service) Rationalization(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Rationalization, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	st, err := settings(ctx, tx)
	if err != nil {
		return nil, err
	}
	g, err := loadGraph(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	dependents, _ := dependencyMaps(g)
	nodes := nodeIndex(g)
	appDeps := func(id string) int {
		return len(reachable(dependents, id, func(n *Node) bool { return n.Type == AppType }, nodes))
	}
	out := &Rationalization{Settings: st, TimeCounts: map[string]int{"tolerate": 0, "invest": 0, "migrate": 0, "eliminate": 0, "none": 0},
		Apps: []RatApp{}, Duplicates: []Duplicate{}, EndOfSupport: []EndOfSupport{}, Orphans: []RatApp{}, RetiringWithDependents: []RatApp{}}
	rows, err := tx.Query(ctx, `select e.id, s.key || '-' || e.number, e.title, a.code, t.name, a.criticality, a.supports_critical_function,
		a.time_rating, a.time_note, coalesce(o.name, ''), coalesce(o.category, '') = 'done',
		exists (select 1 from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'supports' where r.source_id = e.id)
		from elements e
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where e.deleted_at is null order by e.title`)
	if err != nil {
		return nil, err
	}
	byID := map[string]RatApp{}
	var a RatApp
	var retired, hasCap bool
	_, err = pgx.ForEachRow(rows, []any{&a.ID, &a.Key, &a.Name, &a.Code, &a.Team, &a.Criticality, &a.DORA, &a.Time, &a.TimeNote, &a.State, &retired, &hasCap}, func() error {
		if retired {
			return nil
		}
		a.Dependents = appDeps(a.ID)
		byID[a.ID] = a
		out.Apps = append(out.Apps, a)
		if a.Time == nil {
			out.TimeCounts["none"]++
		} else {
			out.TimeCounts[*a.Time]++
			if (*a.Time == "migrate" || *a.Time == "eliminate") && a.Dependents > 0 {
				out.RetiringWithDependents = append(out.RetiringWithDependents, a)
			}
		}
		if !hasCap {
			out.Orphans = append(out.Orphans, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	caps, err := capabilities(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for _, c := range caps {
		var apps []RatApp
		for _, ca := range c.Apps {
			if ra, ok := byID[ca.ID]; ok {
				apps = append(apps, ra)
			}
		}
		if len(apps) >= st.DuplicateMin {
			out.Duplicates = append(out.Duplicates, Duplicate{CapabilityID: c.ID, CapabilityKey: c.Key, CapabilityName: c.Name, Path: c.Path, Apps: apps})
		}
	}
	erows, err := tx.Query(ctx, `select at.application_id, t.id, trim(t.name || ' ' || t.version), t.end_of_support, t.end_of_life
		from application_technologies at join technologies t on t.tenant_id = at.tenant_id and t.id = at.technology_id
		where coalesce(least(t.end_of_support, t.end_of_life), t.end_of_support, t.end_of_life) <= current_date + $1::int`, st.EOLWarningDays)
	if err != nil {
		return nil, err
	}
	var appID string
	var e EndOfSupport
	_, err = pgx.ForEachRow(erows, []any{&appID, &e.TechnologyID, &e.Technology, &e.EndOfSupport, &e.EndOfLife}, func() error {
		ra, ok := byID[appID]
		if !ok {
			return nil
		}
		e.App = ra
		if d := daysLeft(e.EndOfSupport, e.EndOfLife); d != nil {
			e.DaysLeft = *d
		}
		out.EndOfSupport = append(out.EndOfSupport, e)
		return nil
	})
	sort.SliceStable(out.EndOfSupport, func(i, j int) bool { return out.EndOfSupport[i].DaysLeft < out.EndOfSupport[j].DaysLeft })
	return out, err
}

func nodeIndex(g *Graph) map[string]*Node {
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	return byID
}

// reachable restituisce i nodi raggiungibili da id (escluso id) che soddisfano keep.
func reachable(adj map[string][]depEdge, id string, keep func(*Node) bool, byID map[string]*Node) []string {
	seen := map[string]bool{id: true}
	queue := []string{id}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur] {
			if seen[e.to] {
				continue
			}
			seen[e.to] = true
			queue = append(queue, e.to)
			if n, ok := byID[e.to]; ok && keep(n) {
				out = append(out, e.to)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Profilo di un elemento del grafo (dettaglio dell'elemento)

type ProfileTech struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	EndOfSupport *Day   `json:"end_of_support"`
	EndOfLife    *Day   `json:"end_of_life"`
	DaysLeft     *int   `json:"days_left"`
}

type ProfileCap struct {
	ID   string   `json:"id"`
	Key  string   `json:"key"`
	Name string   `json:"name"`
	Path []string `json:"path"`
}

type Profile struct {
	Node          Node           `json:"node"`
	Relations     []Relation     `json:"relations"`
	Capabilities  []ProfileCap   `json:"capabilities"`
	Technologies  []ProfileTech  `json:"technologies"`
	TimeNote      string         `json:"time_note"`
	Dependents    int            `json:"dependents"` // applicazioni che dipendono, a tutti i livelli
	DirectDepends int            `json:"direct_dependents"`
	Dependencies  int            `json:"dependencies"` // elementi da cui dipende, a tutti i livelli
	EOSDays       *int           `json:"end_of_support_days"`
	RelationTypes []RelationType `json:"relation_types"`
}

func (s *Service) Profile(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Profile, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	n, err := resolveNode(ctx, tx, ref, nil)
	if err != nil || n == nil {
		return nil, firstErr(err, engine.ErrNotFound)
	}
	g, err := loadGraph(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	p := &Profile{Relations: []Relation{}, Capabilities: []ProfileCap{}, Technologies: []ProfileTech{}}
	found := false
	byID := map[string]*Node{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
		if g.Nodes[i].ID == n.ID {
			p.Node, found = g.Nodes[i], true
		}
	}
	if !found {
		return nil, engine.InvalidCode("validation.architecture.graph_node_required", "ref", "l'elemento non è un nodo del grafo dell'architettura")
	}
	dependents, dependencies := dependencyMaps(g)
	isApp := func(x *Node) bool { return x.Type == AppType }
	p.Dependents = len(reachable(dependents, n.ID, isApp, byID))
	p.Dependencies = len(reachable(dependencies, n.ID, func(*Node) bool { return true }, byID))
	for _, e := range dependents[n.ID] {
		if x, ok := byID[e.to]; ok && isApp(x) {
			p.DirectDepends++
		}
	}
	if p.Relations, err = s.Relations(ctx, tx, actor, "", n.ID); err != nil {
		return nil, err
	}
	if p.RelationTypes, err = s.RelationTypes(ctx, tx, actor); err != nil {
		return nil, err
	}
	if n.TypeKey == AppType || n.TypeKey == AssetType {
		caps, err := capabilities(ctx, tx, "")
		if err != nil {
			return nil, err
		}
		for _, c := range caps {
			linked := false
			for _, a := range c.Apps {
				linked = linked || a.ID == n.ID
			}
			for _, a := range c.Assets {
				linked = linked || a.ID == n.ID
			}
			if linked {
				p.Capabilities = append(p.Capabilities, ProfileCap{ID: c.ID, Key: c.Key, Name: c.Name, Path: c.Path})
			}
		}
	}
	if n.TypeKey == AppType {
		if err := tx.QueryRow(ctx, `select time_note from ext_application where element_id = $1`, n.ID).Scan(&p.TimeNote); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		rows, err := tx.Query(ctx, `select t.id, t.name, t.version, t.end_of_support, t.end_of_life from application_technologies at
			join technologies t on t.tenant_id = at.tenant_id and t.id = at.technology_id where at.application_id = $1 order by lower(t.name)`, n.ID)
		if err != nil {
			return nil, err
		}
		var t ProfileTech
		if _, err := pgx.ForEachRow(rows, []any{&t.ID, &t.Name, &t.Version, &t.EndOfSupport, &t.EndOfLife}, func() error {
			t.DaysLeft = daysLeft(t.EndOfSupport, t.EndOfLife)
			p.Technologies = append(p.Technologies, t)
			if t.DaysLeft != nil && (p.EOSDays == nil || *t.DaysLeft < *p.EOSDays) {
				v := *t.DaysLeft
				p.EOSDays = &v
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return p, nil
}
