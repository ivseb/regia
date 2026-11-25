package architecture

// Grafo dell'architettura e analisi d'impatto (replica/architettura-ea.md §4). I nodi sono
// gli elementi dei tipi indicati nelle impostazioni (applicazioni, asset logici,
// capability, fornitori ICT...); gli archi sono le relazioni dei tipi dell'architettura
// tra due nodi, più la gerarchia delle capability. Il browser riceve il grafo intero
// (centinaia o poche migliaia di nodi) e filtra e disegna da sé.

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

type Node struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	TypeName      string  `json:"type_name"`
	State         string  `json:"state"`
	StateCategory string  `json:"state_category"`
	Code          *string `json:"code,omitempty"`
	Criticality   *string `json:"criticality,omitempty"`
	DORA          bool    `json:"dora,omitempty"`
	Time          *string `json:"time,omitempty"`
	TeamID        *string `json:"team_id,omitempty"`
	Team          *string `json:"team,omitempty"`
	ParentID      *string `json:"parent_id,omitempty"`
	// EndOfSupport: la data di fine supporto più vicina tra le tecnologie dell'applicazione.
	EndOfSupport *string `json:"end_of_support,omitempty"`
}

type Edge struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Source string         `json:"source"`
	Target string         `json:"target"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

type EdgeType struct {
	Key             string `json:"key"`
	OutwardName     string `json:"outward_name"`
	InwardName      string `json:"inward_name"`
	Category        string `json:"category"`
	ImpactDirection string `json:"impact_direction"`
}

type Graph struct {
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
	EdgeTypes []EdgeType `json:"edge_types"`
	NodeTypes []NamedKey `json:"node_types"`
	Teams     []NamedKey `json:"teams"`
}

type NamedKey struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// relHierarchy è l'arco sintetico tra una capability e la sua capability padre.
const relHierarchy = "_parent"

const selectNodes = `
	select e.id, coalesce(s.key || '-' || e.number, ''), e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, ''),
	       coalesce(a.code, c.code), coalesce(a.criticality, c.criticality), coalesce(a.supports_critical_function, false), a.time_rating,
	       a.owner_team_id, tm.name, case when t.key = 'capability' then e.parent_id end,
	       (select to_char(min(coalesce(tc.end_of_support, tc.end_of_life)), 'YYYY-MM-DD') from application_technologies at
	        join technologies tc on tc.tenant_id = at.tenant_id and tc.id = at.technology_id where at.application_id = e.id)
	from elements e
	join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
	left join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id
	left join teams tm on tm.tenant_id = a.tenant_id and tm.id = a.owner_team_id
	where e.deleted_at is null and not e.is_draft and t.key = any($1)`

func scanNodes(rows pgx.Rows) ([]Node, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Node, error) {
		var n Node
		err := r.Scan(&n.ID, &n.Key, &n.Name, &n.Type, &n.TypeName, &n.State, &n.StateCategory, &n.Code, &n.Criticality, &n.DORA, &n.Time,
			&n.TeamID, &n.Team, &n.ParentID, &n.EndOfSupport)
		return n, err
	})
}

// loadGraph carica nodi e archi; ids non vuoto limita il grafo a quei nodi (diagrammi).
func loadGraph(ctx context.Context, tx pgx.Tx, ids []string) (*Graph, error) {
	st, err := settings(ctx, tx)
	if err != nil {
		return nil, err
	}
	g := &Graph{Nodes: []Node{}, Edges: []Edge{}, EdgeTypes: []EdgeType{}, NodeTypes: []NamedKey{}, Teams: []NamedKey{}}
	q, args := selectNodes+` order by t.key, e.title`, []any{st.NodeTypes}
	if ids != nil {
		q, args = selectNodes+` and e.id = any($2::uuid[]) order by t.key, e.title`, []any{st.NodeTypes, ids}
	}
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	if g.Nodes, err = scanNodes(rows); err != nil {
		return nil, err
	}
	in := make(map[string]bool, len(g.Nodes))
	nodeIDs := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		in[n.ID], nodeIDs[i] = true, n.ID
	}
	erows, err := tx.Query(ctx, `select r.id, rt.key, r.source_id, r.target_id, r.attrs from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.ea_category is not null
		where r.source_id = any($1::uuid[]) and r.target_id = any($1::uuid[])`, nodeIDs)
	if err != nil {
		return nil, err
	}
	var e Edge
	_, err = pgx.ForEachRow(erows, []any{&e.ID, &e.Type, &e.Source, &e.Target, &e.Attrs}, func() error {
		if len(e.Attrs) == 0 {
			e.Attrs = nil
		}
		g.Edges = append(g.Edges, e)
		e = Edge{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, n := range g.Nodes {
		if n.ParentID != nil && in[*n.ParentID] {
			g.Edges = append(g.Edges, Edge{ID: "p:" + n.ID, Type: relHierarchy, Source: n.ID, Target: *n.ParentID})
		}
	}
	trows, err := tx.Query(ctx, `select key, outward_name, inward_name, ea_category, impact_direction from relation_types
		where ea_category is not null order by ea_category, outward_name`)
	if err != nil {
		return nil, err
	}
	if g.EdgeTypes, err = pgx.CollectRows(trows, pgx.RowToStructByPos[EdgeType]); err != nil {
		return nil, err
	}
	// La gerarchia: la capability padre dipende dalle sue figlie.
	g.EdgeTypes = append(g.EdgeTypes, EdgeType{Key: relHierarchy, OutwardName: "fa parte di", InwardName: "comprende", Category: catCapability, ImpactDirection: "reverse"})
	nrows, err := tx.Query(ctx, `select key, name from element_types where key = any($1) order by name`, st.NodeTypes)
	if err != nil {
		return nil, err
	}
	if g.NodeTypes, err = pgx.CollectRows(nrows, pgx.RowToStructByPos[NamedKey]); err != nil {
		return nil, err
	}
	teams, err := tx.Query(ctx, `select id::text, name from teams where not archived order by name`)
	if err != nil {
		return nil, err
	}
	g.Teams, err = pgx.CollectRows(teams, pgx.RowToStructByPos[NamedKey])
	return g, err
}

func (s *Service) Graph(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Graph, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	return loadGraph(ctx, tx, nil)
}

// ---------------------------------------------------------------------------
// Analisi d'impatto

type ImpactItem struct {
	Level       int     `json:"level"`
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	TypeName    string  `json:"type_name"`
	Code        *string `json:"code,omitempty"`
	Team        *string `json:"team,omitempty"`
	Criticality *string `json:"criticality,omitempty"`
	DORA        bool    `json:"dora"`
	State       string  `json:"state"`
	// Via: la relazione con il nodo del livello precedente, nel verso della dipendenza.
	Via string `json:"via"`
	// Path: le chiavi dal nodo di partenza a questo (percorso più breve).
	Path []string `json:"path"`
}

type ImpactResult struct {
	Root      Node         `json:"root"`
	Direction string       `json:"direction"` // dependents | dependencies
	Depth     int          `json:"depth"`
	Items     []ImpactItem `json:"items"`
	// Cycles: archi che tornano su un nodo già raggiunto (dipendenze circolari).
	Cycles int `json:"cycles"`
}

// adjacency costruisce le liste "chi dipende da chi" secondo il verso dei tipi.
type depEdge struct {
	to  string
	rel string // nome della relazione nel verso della lettura
}

func dependencyMaps(g *Graph) (dependents, dependencies map[string][]depEdge) {
	types := map[string]EdgeType{}
	for _, t := range g.EdgeTypes {
		types[t.Key] = t
	}
	dependents, dependencies = map[string][]depEdge{}, map[string][]depEdge{}
	for _, e := range g.Edges {
		t, ok := types[e.Type]
		if !ok || t.ImpactDirection == "none" {
			continue
		}
		// dependent dipende da dependency.
		dependent, dependency := e.Source, e.Target
		if t.ImpactDirection == "reverse" {
			dependent, dependency = e.Target, e.Source
		}
		// Il nome nel verso della lettura: da dependency verso chi ne dipende e viceversa.
		outName, inName := t.OutwardName, t.InwardName
		if t.ImpactDirection == "reverse" {
			outName, inName = t.InwardName, t.OutwardName
		}
		dependents[dependency] = append(dependents[dependency], depEdge{to: dependent, rel: inName})
		dependencies[dependent] = append(dependencies[dependent], depEdge{to: dependency, rel: outName})
	}
	return dependents, dependencies
}

const maxImpactDepth = 20

// Impact risponde a "cosa dipende da X" (dependents) o "da cosa dipende X"
// (dependencies) fino a depth livelli (0 = tutti). Visita in ampiezza: ogni nodo compare
// una volta, al livello più vicino; i cicli non fanno girare a vuoto la visita.
func (s *Service) Impact(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, direction string, depth int) (*ImpactResult, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	if direction == "" {
		direction = "dependents"
	}
	if direction != "dependents" && direction != "dependencies" {
		return nil, engine.InvalidValue("direction", "verso non ammesso (dependents, dependencies)")
	}
	if depth <= 0 || depth > maxImpactDepth {
		depth = maxImpactDepth
	}
	n, err := resolveNode(ctx, tx, ref, nil)
	if err != nil || n == nil {
		return nil, firstErr(err, engine.ErrNotFound)
	}
	g, err := loadGraph(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	return impactOn(g, n.ID, direction, depth)
}

func impactOn(g *Graph, rootID, direction string, depth int) (*ImpactResult, error) {
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	root, ok := byID[rootID]
	if !ok {
		return nil, engine.InvalidCode("validation.architecture.graph_node_required", "ref", "l'elemento non è un nodo del grafo dell'architettura")
	}
	dependents, dependencies := dependencyMaps(g)
	adj := dependents
	if direction == "dependencies" {
		adj = dependencies
	}
	res := &ImpactResult{Root: *root, Direction: direction, Depth: depth, Items: []ImpactItem{}}
	seen := map[string][]string{rootID: {root.Key}}
	frontier := []string{rootID}
	for level := 1; level <= depth && len(frontier) > 0; level++ {
		var next []string
		for _, id := range frontier {
			for _, e := range adj[id] {
				if _, ok := seen[e.to]; ok {
					continue
				}
				nd := byID[e.to]
				path := append(append([]string{}, seen[id]...), nd.Key)
				seen[e.to] = path
				next = append(next, e.to)
				res.Items = append(res.Items, ImpactItem{Level: level, ID: nd.ID, Key: nd.Key, Name: nd.Name, Type: nd.Type, TypeName: nd.TypeName,
					Code: nd.Code, Team: nd.Team, Criticality: nd.Criticality, DORA: nd.DORA, State: nd.State, Via: e.rel, Path: path})
			}
		}
		frontier = next
	}
	// Cycles conta gli archi che chiudono un ciclo tra i nodi raggiunti (i percorsi
	// alternativi verso lo stesso nodo non sono cicli).
	res.Cycles = countCycles(adj, seen)
	return res, nil
}

// countCycles conta i nodi raggiunti che fanno parte di un ciclo (ricerca in profondità
// con colori, iterativa per non esaurire lo stack su grafi grandi).
func countCycles(adj map[string][]depEdge, reach map[string][]string) int {
	const white, grey, black = 0, 1, 2
	color := map[string]int{}
	back := 0
	starts := make([]string, 0, len(reach))
	for id := range reach {
		starts = append(starts, id)
	}
	sort.Strings(starts)
	for _, start := range starts {
		if color[start] != white {
			continue
		}
		type frame struct {
			id string
			i  int
		}
		stack := []frame{{start, 0}}
		color[start] = grey
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.i < len(adj[f.id]) {
				to := adj[f.id][f.i].to
				f.i++
				if _, ok := reach[to]; !ok {
					continue
				}
				switch color[to] {
				case grey:
					back++
				case white:
					color[to] = grey
					stack = append(stack, frame{to, 0})
				}
				continue
			}
			color[f.id] = black
			stack = stack[:len(stack)-1]
		}
	}
	return back
}

// ImpactCSV è l'elenco dell'analisi d'impatto da scaricare (separatore ";" per Excel).
func ImpactCSV(r *ImpactResult) []byte {
	var b bytes.Buffer
	b.WriteString(bom)
	w := csv.NewWriter(&b)
	w.Comma = ';'
	verso := "dipendono da"
	if r.Direction == "dependencies" {
		verso = "da cui dipende"
	}
	_ = w.Write([]string{"# Analisi d'impatto: elementi che " + verso + " " + r.Root.Key + " " + r.Root.Name, "", "", "", "", "", "", "", "", time.Now().Format(time.DateOnly)})
	_ = w.Write([]string{"livello", "chiave", "nome", "tipo", "codice", "team", "criticita", "dora", "fase", "relazione", "percorso"})
	for _, it := range r.Items {
		dora := "no"
		if it.DORA {
			dora = "sì"
		}
		crit := ""
		if it.Criticality != nil {
			crit = critNames[*it.Criticality]
		}
		_ = w.Write([]string{strconv.Itoa(it.Level), it.Key, it.Name, it.TypeName, deref(it.Code), deref(it.Team), crit, dora, it.State, it.Via,
			strings.Join(it.Path, " → ")})
	}
	w.Flush()
	return b.Bytes()
}

// bom fa riconoscere a Excel il file come UTF-8.
const bom = "\ufeff"
