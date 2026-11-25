package architecture

// Diagrammi C4 (modello pubblico di Simon Brown): contesto di sistema, container e componenti,
// generati dal grafo. Il diagramma salva solo la specifica (livello, elemento radice, attori,
// nodi nascosti) e le posizioni scelte dall'utente: nodi, tecnologie ed archi si calcolano a
// ogni apertura, quindi il diagramma segue i dati.
//
//	contesto   (applicazione o asset logico): il sistema al centro, i sistemi con cui scambia
//	           dati (relazioni di dipendenza) e gli attori.
//	container  (applicazione): l'applicazione, i suoi servizi DevSecOps, i database (tecnologie
//	           di categoria database) e gli asset logici di cui fa parte.
//	componenti (asset logico): i moduli, cioè le applicazioni che lo compongono, con le
//	           dipendenze tra loro e con i sistemi esterni.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

const (
	C4Context   = "c4_context"
	C4Container = "c4_container"
	C4Component = "c4_component"
)

var c4Levels = map[string]string{"context": C4Context, "container": C4Container, "component": C4Component}

// C4Actor è una persona o un ruolo che usa il sistema. Se MemberID c'è, il nome segue il membro.
type C4Actor struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"` // person | role
	Description string  `json:"description,omitempty"`
	MemberID    *string `json:"member_id,omitempty"`
	Label       string  `json:"label,omitempty"` // etichetta della freccia (default "Usa")
}

// C4Spec è la parte salvata di un diagramma C4.
type C4Spec struct {
	Level  string    `json:"level"` // context | container | component
	RootID string    `json:"root_id"`
	Actors []C4Actor `json:"actors"`
	// Hidden: chiavi dei nodi che l'utente ha tolto dalla vista.
	Hidden []string `json:"hidden,omitempty"`
}

// C4Node è un riquadro del diagramma.
type C4Node struct {
	// ID è la chiave stabile: "e:<elemento>", "s:<servizio>", "t:<tecnologia>", "a:<attore>".
	ID string `json:"id"`
	// Kind: person | system | external | container | database | component | asset.
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	TypeLabel   string  `json:"type_label"`
	Tech        string  `json:"tech,omitempty"`
	Description string  `json:"description,omitempty"`
	ElementID   *string `json:"element_id,omitempty"`
	Key         *string `json:"key,omitempty"`
	Criticality *string `json:"criticality,omitempty"`
	// Inside: il riquadro sta dentro il confine del sistema (container e componenti).
	Inside bool `json:"inside,omitempty"`
}

// C4Edge è una freccia etichettata.
type C4Edge struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	Label    string `json:"label"`
	Protocol string `json:"protocol,omitempty"`
	Data     string `json:"data,omitempty"`
}

// C4Model è il diagramma calcolato.
type C4Model struct {
	Level    string   `json:"level"`
	RootID   string   `json:"root_id"`
	RootName string   `json:"root_name"`
	Nodes    []C4Node `json:"nodes"`
	Edges    []C4Edge `json:"edges"`
	// Boundary: nome del confine del sistema (container e componenti).
	Boundary string `json:"boundary,omitempty"`
	Missing  bool   `json:"missing,omitempty"`
}

var dbCategoryRe = regexp.MustCompile(`(?i)(database|dbms|\bdb\b|base dati|banca dati|datastore)`)

type c4Rel struct {
	id, typeKey, typeName, inName string
	src, tgt                      string
	attrs                         map[string]any
}

func attrStr(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return ""
}

// c4Relations: le relazioni EA della categoria data che toccano uno degli id.
func c4Relations(ctx context.Context, tx pgx.Tx, ids []string, categories ...string) ([]c4Rel, error) {
	rows, err := tx.Query(ctx, `select r.id, rt.key, rt.outward_name, rt.inward_name, r.source_id, r.target_id, r.attrs
		from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.ea_category = any($2)
		where r.source_id = any($1::uuid[]) or r.target_id = any($1::uuid[])
		order by r.created_at, r.id`, ids, categories)
	if err != nil {
		return nil, err
	}
	var out []c4Rel
	var r c4Rel
	_, err = pgx.ForEachRow(rows, []any{&r.id, &r.typeKey, &r.typeName, &r.inName, &r.src, &r.tgt, &r.attrs}, func() error {
		out = append(out, r)
		r = c4Rel{}
		return nil
	})
	return out, err
}

func critPtr(n Node) *string { return n.Criticality }

// appTech: le tecnologie delle applicazioni, separate in database e altre.
func appTech(ctx context.Context, tx pgx.Tx, ids []string) (map[string][]string, map[string][][3]string, error) {
	rows, err := tx.Query(ctx, `select at.application_id, tc.id, tc.name, tc.version, tc.category
		from application_technologies at join technologies tc on tc.tenant_id = at.tenant_id and tc.id = at.technology_id
		where at.application_id = any($1::uuid[]) order by lower(tc.name)`, ids)
	if err != nil {
		return nil, nil, err
	}
	techs := map[string][]string{}
	dbs := map[string][][3]string{} // app -> [id, name, version]
	var app, id, name, version, category string
	_, err = pgx.ForEachRow(rows, []any{&app, &id, &name, &version, &category}, func() error {
		label := strings.TrimSpace(name + " " + version)
		if dbCategoryRe.MatchString(category) {
			dbs[app] = append(dbs[app], [3]string{id, name, version})
		} else {
			techs[app] = append(techs[app], label)
		}
		return nil
	})
	return techs, dbs, err
}

func techText(l []string) string {
	if len(l) > 3 {
		l = append(l[:3:3], fmt.Sprintf("+%d", len(l)-3))
	}
	return strings.Join(l, ", ")
}

func relLabel(r c4Rel, outward bool) string {
	if outward {
		return r.typeName
	}
	return r.inName
}

// c4Model calcola il diagramma dalla specifica.
func (s *Service) c4Model(ctx context.Context, tx pgx.Tx, spec *C4Spec) (*C4Model, error) {
	m := &C4Model{Level: spec.Level, RootID: spec.RootID, Nodes: []C4Node{}, Edges: []C4Edge{}}
	g, err := loadGraph(ctx, tx, []string{spec.RootID})
	if err != nil {
		return nil, err
	}
	if len(g.Nodes) == 0 {
		m.Missing = true
		return m, nil
	}
	root := g.Nodes[0]
	m.RootName = root.Name
	isAsset := root.Type == AssetType
	rootKey := "e:" + root.ID
	hidden := map[string]bool{}
	for _, h := range spec.Hidden {
		hidden[h] = true
	}
	nodes := map[string]*C4Node{}
	var order []string
	add := func(n C4Node) *C4Node {
		if hidden[n.ID] {
			return nil
		}
		if old, ok := nodes[n.ID]; ok {
			return old
		}
		nodes[n.ID] = &n
		order = append(order, n.ID)
		return nodes[n.ID]
	}
	edgeSeen := map[string]*C4Edge{}
	var edgeOrder []string
	addEdge := func(id, src, tgt, label, protocol, data string) {
		if nodes[src] == nil || nodes[tgt] == nil || src == tgt {
			return
		}
		// Archi uguali (stessi estremi ed etichetta) si fondono: protocolli e dati si sommano.
		k := src + ">" + tgt + "|" + label
		if e := edgeSeen[k]; e != nil {
			e.Protocol = joinUniq(e.Protocol, protocol)
			e.Data = joinUniq(e.Data, data)
			return
		}
		e := &C4Edge{ID: id, Source: src, Target: tgt, Label: label, Protocol: protocol, Data: data}
		edgeSeen[k] = e
		edgeOrder = append(edgeOrder, k)
	}

	// Moduli (asset logico): le applicazioni che lo compongono.
	var modules []string
	if isAsset {
		rels, err := c4Relations(ctx, tx, []string{root.ID}, catComposition)
		if err != nil {
			return nil, err
		}
		for _, r := range rels {
			if r.src == root.ID {
				modules = append(modules, r.tgt)
			}
		}
	}
	inner := append([]string{root.ID}, modules...)
	rels, err := c4Relations(ctx, tx, inner, catDependency)
	if err != nil {
		return nil, err
	}
	isInner := map[string]bool{}
	for _, id := range inner {
		isInner[id] = true
	}
	// Vicini: gli altri estremi delle dipendenze, fuori dal sistema.
	neighbourIDs := map[string]bool{}
	for _, r := range rels {
		for _, id := range []string{r.src, r.tgt} {
			if !isInner[id] {
				neighbourIDs[id] = true
			}
		}
	}
	// Per i container le dipendenze interessano solo se toccano la radice.
	var assetIDs []string
	if spec.Level == "container" && !isAsset {
		comp, err := c4Relations(ctx, tx, []string{root.ID}, catComposition)
		if err != nil {
			return nil, err
		}
		for _, r := range comp {
			if r.tgt == root.ID {
				assetIDs = append(assetIDs, r.src)
				neighbourIDs[r.src] = true
			}
		}
	}
	load := append([]string{}, inner...)
	for id := range neighbourIDs {
		load = append(load, id)
	}
	sort.Strings(load[len(inner):])
	g, err = loadGraph(ctx, tx, load)
	if err != nil {
		return nil, err
	}
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	techs, dbs, err := appTech(ctx, tx, load)
	if err != nil {
		return nil, err
	}
	isAssetID := map[string]bool{}
	for _, id := range assetIDs {
		isAssetID[id] = true
	}

	element := func(n Node, kind string, inside bool) C4Node {
		id := n.ID
		key := n.Key
		c := C4Node{ID: "e:" + id, Kind: kind, Name: n.Name, TypeLabel: n.TypeName, Tech: techText(techs[id]),
			ElementID: &id, Key: &key, Criticality: critPtr(n), Inside: inside}
		return c
	}

	switch spec.Level {
	case "context":
		add(element(root, "system", false))
	case "container":
		if isAsset {
			return nil, engine.InvalidCode("validation.architecture.c4_container_from_app", "level", "il diagramma di container si genera da un'applicazione")
		}
		m.Boundary = root.Name
		n := element(root, "container", true)
		n.TypeLabel = "Container · " + root.TypeName
		add(n)
	case "component":
		if !isAsset {
			return nil, engine.InvalidCode("validation.architecture.c4_component_from_asset", "level", "il diagramma di componenti si genera da un asset logico")
		}
		m.Boundary = root.Name
		for _, id := range modules {
			if n, ok := byID[id]; ok {
				c := element(n, "component", true)
				c.TypeLabel = "Componente · " + n.TypeName
				add(c)
			}
		}
	}

	// Vicini esterni.
	var ids []string
	for id := range neighbourIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return byID[ids[i]].Name < byID[ids[j]].Name })
	for _, id := range ids {
		n, ok := byID[id]
		if !ok {
			continue
		}
		if isAssetID[id] {
			c := element(n, "asset", false)
			add(c)
			continue
		}
		add(element(n, "external", false))
	}

	// target di una relazione: nel contesto di un asset e nei container la radice assorbe i moduli.
	target := func(id string) string {
		if spec.Level == "component" {
			return "e:" + id
		}
		if isInner[id] {
			return rootKey
		}
		return "e:" + id
	}
	for _, r := range rels {
		src, tgt := target(r.src), target(r.tgt)
		addEdge("r:"+r.id, src, tgt, r.typeName, attrStr(r.attrs, "protocol"), attrStr(r.attrs, "data"))
	}
	if spec.Level == "container" {
		comp, err := c4Relations(ctx, tx, []string{root.ID}, catComposition)
		if err != nil {
			return nil, err
		}
		for _, r := range comp {
			if r.tgt == root.ID && isAssetID[r.src] {
				addEdge("r:"+r.id, "e:"+r.src, rootKey, r.typeName, "", "")
			}
		}
		// Servizi DevSecOps dell'applicazione.
		rows, err := tx.Query(ctx, `select id, name, coalesce(provider, ''), coalesce(repository, '') from services
			where application_element_id = $1 order by lower(name)`, root.ID)
		if err != nil {
			return nil, err
		}
		var sid, name, provider, repo string
		type svc struct{ id, name, provider, repo string }
		var list []svc
		if _, err := pgx.ForEachRow(rows, []any{&sid, &name, &provider, &repo}, func() error {
			list = append(list, svc{sid, name, provider, repo})
			return nil
		}); err != nil {
			return nil, err
		}
		for _, v := range list {
			tech := v.provider
			if v.repo != "" {
				tech = strings.TrimSpace(v.provider + " " + v.repo)
			}
			if add(C4Node{ID: "s:" + v.id, Kind: "container", Name: v.name, TypeLabel: "Servizio", Tech: tech, Inside: true}) != nil {
				addEdge("s:"+v.id, rootKey, "s:"+v.id, "include", "", "")
			}
		}
		for _, d := range dbs[root.ID] {
			if add(C4Node{ID: "t:" + d[0], Kind: "database", Name: d[1], TypeLabel: "Database", Tech: d[2], Inside: true}) != nil {
				addEdge("d:"+d[0], rootKey, "t:"+d[0], "legge e scrive", "", "")
			}
		}
		if n := nodes[rootKey]; n != nil {
			n.Tech = techText(techs[root.ID])
		}
	}
	if spec.Level == "component" {
		for _, id := range modules {
			for _, d := range dbs[id] {
				if nodes["e:"+id] == nil {
					continue
				}
				if add(C4Node{ID: "t:" + d[0], Kind: "database", Name: d[1], TypeLabel: "Database", Tech: d[2], Inside: true}) != nil {
					addEdge("d:"+id+":"+d[0], "e:"+id, "t:"+d[0], "legge e scrive", "", "")
				}
			}
		}
	}

	// Attori: persone e ruoli configurati.
	actorTargets := []string{rootKey}
	if spec.Level == "component" {
		actorTargets = nil
		hasIn := map[string]bool{}
		for _, k := range edgeOrder {
			e := edgeSeen[k]
			if strings.HasPrefix(e.Source, "e:") && nodes[e.Source] != nil && nodes[e.Source].Kind == "component" {
				hasIn[e.Target] = true
			}
		}
		for _, id := range order {
			if nodes[id].Kind == "component" && !hasIn[id] {
				actorTargets = append(actorTargets, id)
			}
		}
		if len(actorTargets) == 0 {
			for _, id := range order {
				if nodes[id].Kind == "component" {
					actorTargets = append(actorTargets, id)
					break
				}
			}
		}
	}
	for _, a := range spec.Actors {
		name, desc := a.Name, a.Description
		if a.MemberID != nil {
			var dn string
			if err := tx.QueryRow(ctx, `select display_name from members where id = $1`, *a.MemberID).Scan(&dn); err == nil {
				name = dn
			}
		}
		kind := "person"
		tl := "Persona"
		if a.Kind == "role" {
			tl = "Ruolo"
		}
		n := C4Node{ID: "a:" + a.ID, Kind: kind, Name: name, TypeLabel: tl, Description: desc}
		if add(n) == nil {
			continue
		}
		label := a.Label
		if label == "" {
			label = "Usa"
		}
		for _, t := range actorTargets {
			addEdge("a:"+a.ID+">"+t, "a:"+a.ID, t, label, "", "")
		}
	}

	for _, id := range order {
		m.Nodes = append(m.Nodes, *nodes[id])
	}
	for _, k := range edgeOrder {
		m.Edges = append(m.Edges, *edgeSeen[k])
	}
	return m, nil
}

func joinUniq(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	for _, p := range strings.Split(a, ", ") {
		if p == b {
			return a
		}
	}
	return a + ", " + b
}

// validateC4 normalizza la specifica salvata nel layout.
func validateC4(c *C4Spec) error {
	if _, ok := c4Levels[c.Level]; !ok {
		return engine.InvalidValue("layout", "livello C4 non ammesso (context, container, component)")
	}
	c.RootID = strings.ToLower(c.RootID)
	if !uuidRe.MatchString(c.RootID) {
		return engine.InvalidValue("layout", "elemento radice del diagramma C4 non valido")
	}
	if len(c.Actors) > 30 {
		return engine.InvalidCode("validation.architecture.c4_actors_max", "layout", "al massimo 30 attori")
	}
	seen := map[string]bool{}
	for i := range c.Actors {
		a := &c.Actors[i]
		a.Name, a.Description, a.Label = strings.TrimSpace(a.Name), strings.TrimSpace(a.Description), strings.TrimSpace(a.Label)
		if a.ID == "" || len(a.ID) > 40 || seen[a.ID] || strings.ContainsAny(a.ID, " |>") {
			a.ID = db.NewID()
		}
		seen[a.ID] = true
		if a.Kind != "role" {
			a.Kind = "person"
		}
		if a.MemberID != nil && !uuidRe.MatchString(*a.MemberID) {
			return engine.InvalidValue("layout", "membro dell'attore non valido")
		}
		if a.Name == "" || len(a.Name) > 120 || len(a.Description) > 300 || len(a.Label) > 80 {
			return engine.Required("layout", "attore: nome obbligatorio (120 caratteri), descrizione 300, etichetta 80")
		}
	}
	if len(c.Hidden) > 500 {
		return engine.InvalidCode("validation.architecture.c4_hidden_too_many", "layout", "troppi nodi nascosti")
	}
	if c.Actors == nil {
		c.Actors = []C4Actor{}
	}
	return nil
}

// C4Input chiede di generare un diagramma C4 da un'applicazione o da un asset logico.
type C4Input struct {
	RootID string `json:"root_id"`
	Level  string `json:"level"`
	Name   string `json:"name"`
}

// GenerateC4 crea il diagramma del livello richiesto per l'elemento, oppure restituisce
// quello già generato (stessa radice e livello): rigenerare non duplica.
func (s *Service) GenerateC4(ctx context.Context, tx pgx.Tx, actor engine.Actor, in C4Input) (*DiagramView, bool, error) {
	if !actor.Has(PermWrite) {
		return nil, false, engine.ErrForbidden
	}
	kind, ok := c4Levels[in.Level]
	in.RootID = strings.ToLower(in.RootID)
	if !ok || !uuidRe.MatchString(in.RootID) {
		return nil, false, engine.Required("level", "serve il livello (context, container, component) e l'elemento")
	}
	var existing string
	err := tx.QueryRow(ctx, `select id from diagrams where kind = $1 and layout->'c4'->>'root_id' = $2 order by created_at limit 1`, kind, in.RootID).Scan(&existing)
	if err == nil {
		v, err := s.DiagramView(ctx, tx, actor, existing)
		return v, true, err
	}
	if err != pgx.ErrNoRows {
		return nil, false, err
	}
	g, err := loadGraph(ctx, tx, []string{in.RootID})
	if err != nil {
		return nil, false, err
	}
	if len(g.Nodes) == 0 {
		return nil, false, engine.InvalidCode("validation.architecture.c4_root_invalid", "root_id", "l'elemento non è un'applicazione o un asset logico del grafo")
	}
	root := g.Nodes[0]
	if (in.Level == "container" && root.Type == AssetType) || (in.Level == "component" && root.Type != AssetType) {
		return nil, false, engine.InvalidParams("validation.architecture.c4_level_invalid", "level", map[string]any{"type": strings.ToLower(root.TypeName)}, "questo livello non vale per %s", strings.ToLower(root.TypeName))
	}
	spec := &C4Spec{Level: in.Level, RootID: in.RootID}
	if spec.Actors, err = seedActors(ctx, tx, in.RootID); err != nil {
		return nil, false, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = map[string]string{"context": "Contesto di sistema", "container": "Container", "component": "Componenti"}[in.Level] + " · " + root.Name
	}
	if len(name) > 200 {
		name = name[:200]
	}
	layout := DiagramLayout{Nodes: []DiagramNode{}, Notes: []DiagramNote{}, C4: spec}
	v, err := s.CreateDiagram(ctx, tx, actor, DiagramInput{Name: &name, Kind: &kind, Layout: &layout})
	return v, false, err
}

// seedActors propone un attore per ruolo di stakeholder dell'elemento; senza stakeholder, un
// attore generico. Il tenant li modifica dal diagramma.
func seedActors(ctx context.Context, tx pgx.Tx, id string) ([]C4Actor, error) {
	rows, err := tx.Query(ctx, `select r.name, coalesce(string_agg(distinct coalesce(m.display_name, g.name), ', '), '')
		from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		where sh.element_id = $1 group by r.id, r.name order by r.name limit 5`, id)
	if err != nil {
		return nil, err
	}
	out := []C4Actor{}
	var name, who string
	if _, err := pgx.ForEachRow(rows, []any{&name, &who}, func() error {
		out = append(out, C4Actor{ID: db.NewID(), Name: name, Kind: "role", Description: who, Label: "Usa"})
		return nil
	}); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = append(out, C4Actor{ID: db.NewID(), Name: "Utente", Kind: "person", Description: "Chi usa il sistema", Label: "Usa"})
	}
	return out, nil
}

// c4JSON serve ai test.
func c4JSON(m *C4Model) string { b, _ := json.Marshal(m); return string(b) }
