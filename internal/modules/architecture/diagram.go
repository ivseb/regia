package architecture

// Diagrammi (replica/architettura-ea.md §6): viste salvate del grafo. Il layout conserva
// solo gli id dei nodi con le posizioni fissate dall'utente e le annotazioni; nomi, dati e
// archi si leggono dal grafo a ogni apertura, così il diagramma segue i cambiamenti.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

type DiagramNode struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type DiagramNote struct {
	ID   string  `json:"id"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w,omitempty"`
	Text string  `json:"text"`
}

type DiagramLayout struct {
	Nodes []DiagramNode `json:"nodes"`
	Notes []DiagramNote `json:"notes"`
	// EdgeTypes: tipi di relazione da mostrare (vuoto = tutti).
	EdgeTypes []string `json:"edge_types,omitempty"`
	// C4: specifica dei diagrammi C4 generati (c4.go); in quel caso i nodi vengono dal modello.
	C4 *C4Spec `json:"c4,omitempty"`
	// Pos: posizioni scelte dall'utente nei diagrammi C4, per chiave del riquadro.
	Pos map[string]DiagramPos `json:"pos,omitempty"`
}

type DiagramPos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Diagram struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Description string        `json:"description"`
	Version     int           `json:"version"`
	Layout      DiagramLayout `json:"layout"`
	NodeCount   int           `json:"node_count"`
	CreatedBy   *string       `json:"created_by"`
	UpdatedBy   *string       `json:"updated_by"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// DiagramView è il diagramma con i dati attuali dei suoi nodi e gli archi tra di loro.
type DiagramView struct {
	Diagram
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
	EdgeTypes []EdgeType `json:"edge_types"`
	// Missing: nodi del layout che non esistono più (cancellati o non più nodi del grafo).
	Missing int `json:"missing"`
	// C4: il modello calcolato, per i diagrammi C4 generati.
	C4 *C4Model `json:"c4,omitempty"`
}

var diagramKinds = map[string]bool{"c4_context": true, "c4_container": true, "c4_component": true, "capability_map": true, "dependency": true, "archimate": true, "free": true}

const selectDiagram = `select d.id, d.name, d.kind, d.description, d.version, d.layout, jsonb_array_length(coalesce(d.layout->'nodes', '[]')),
	cm.display_name, um.display_name, d.updated_at
	from diagrams d
	left join members cm on cm.tenant_id = d.tenant_id and cm.id = d.created_by
	left join members um on um.tenant_id = d.tenant_id and um.id = d.updated_by`

func scanDiagrams(rows pgx.Rows) ([]Diagram, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Diagram, error) {
		var d Diagram
		var raw []byte
		err := r.Scan(&d.ID, &d.Name, &d.Kind, &d.Description, &d.Version, &raw, &d.NodeCount, &d.CreatedBy, &d.UpdatedBy, &d.UpdatedAt)
		if err == nil {
			err = json.Unmarshal(raw, &d.Layout)
		}
		if d.Layout.Nodes == nil {
			d.Layout.Nodes = []DiagramNode{}
		}
		if d.Layout.Notes == nil {
			d.Layout.Notes = []DiagramNote{}
		}
		return d, err
	})
}

func (s *Service) Diagrams(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Diagram, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, selectDiagram+` order by lower(d.name)`)
	if err != nil {
		return nil, err
	}
	return scanDiagrams(rows)
}

func (s *Service) DiagramView(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*DiagramView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, selectDiagram+` where d.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanDiagrams(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	v := &DiagramView{Diagram: list[0]}
	if c := v.Layout.C4; c != nil {
		v.Nodes, v.Edges, v.EdgeTypes = []Node{}, []Edge{}, []EdgeType{}
		if v.C4, err = s.c4Model(ctx, tx, c); err != nil {
			return nil, err
		}
		return v, nil
	}
	ids := make([]string, 0, len(v.Layout.Nodes))
	for _, n := range v.Layout.Nodes {
		ids = append(ids, n.ID)
	}
	g, err := loadGraph(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	v.Nodes, v.EdgeTypes = g.Nodes, g.EdgeTypes
	v.Edges = []Edge{}
	keep := map[string]bool{}
	for _, t := range v.Layout.EdgeTypes {
		keep[t] = true
	}
	for _, e := range g.Edges {
		if len(keep) == 0 || keep[e.Type] {
			v.Edges = append(v.Edges, e)
		}
	}
	v.Missing = len(ids) - len(g.Nodes)
	return v, nil
}

type DiagramInput struct {
	Name        *string        `json:"name"`
	Kind        *string        `json:"kind"`
	Description *string        `json:"description"`
	Layout      *DiagramLayout `json:"layout"`
	Version     int            `json:"version"`
}

const (
	maxDiagramNodes = 2000
	maxDiagramNotes = 300
)

func (in *DiagramInput) validate() error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 200 {
			return engine.TooLong("name", 200, "nome obbligatorio, massimo 200 caratteri")
		}
		in.Name = &n
	}
	if in.Kind != nil && !diagramKinds[*in.Kind] {
		return engine.InvalidValue("kind", "tipo di diagramma non ammesso (dependency, capability_map, c4_context, c4_container, c4_component, archimate, free)")
	}
	if in.Description != nil && len(*in.Description) > 4000 {
		return engine.TooLong("description", 4000, "descrizione troppo lunga (massimo 4000 caratteri)")
	}
	if l := in.Layout; l != nil {
		if len(l.Nodes) > maxDiagramNodes {
			return engine.InvalidParams("validation.architecture.diagram_nodes_max", "layout", map[string]any{"max": maxDiagramNodes}, "al massimo %d nodi per diagramma", maxDiagramNodes)
		}
		if len(l.Notes) > maxDiagramNotes {
			return engine.InvalidParams("validation.architecture.diagram_notes_max", "layout", map[string]any{"max": maxDiagramNotes}, "al massimo %d annotazioni per diagramma", maxDiagramNotes)
		}
		seen := map[string]bool{}
		nodes := l.Nodes[:0]
		for _, n := range l.Nodes {
			n.ID = strings.ToLower(n.ID)
			if !uuidRe.MatchString(n.ID) {
				return engine.InvalidValue("layout", "nodo %q non valido", n.ID)
			}
			if seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			if !finite(n.X) || !finite(n.Y) {
				return engine.InvalidCode("validation.architecture.diagram_node_position", "layout", "posizione del nodo fuori scala")
			}
			nodes = append(nodes, n)
		}
		l.Nodes = nodes
		for i := range l.Notes {
			nt := &l.Notes[i]
			nt.Text = strings.TrimSpace(nt.Text)
			if len(nt.Text) > 2000 {
				return engine.TooLong("layout", 2000, "annotazione troppo lunga (massimo 2000 caratteri)")
			}
			if !finite(nt.X) || !finite(nt.Y) || nt.W < 0 || nt.W > 2000 {
				return engine.InvalidCode("validation.architecture.diagram_note_position", "layout", "posizione dell'annotazione fuori scala")
			}
			if nt.ID == "" || len(nt.ID) > 40 {
				nt.ID = db.NewID()
			}
		}
		if l.C4 != nil {
			if err := validateC4(l.C4); err != nil {
				return err
			}
		}
		if len(l.Pos) > maxDiagramNodes {
			return engine.InvalidCode("validation.architecture.diagram_positions_too_many", "layout", "troppe posizioni")
		}
		for k, p := range l.Pos {
			if len(k) > 80 || !finite(p.X) || !finite(p.Y) {
				return engine.InvalidCode("validation.architecture.diagram_box_position", "layout", "posizione del riquadro fuori scala")
			}
		}
		if len(l.EdgeTypes) > 50 {
			return engine.InvalidCode("validation.architecture.diagram_relation_types_too_many", "layout", "troppi tipi di relazione")
		}
		if l.Notes == nil {
			l.Notes = []DiagramNote{}
		}
	}
	return nil
}

func finite(f float64) bool { return f > -1e6 && f < 1e6 }

// checkNodes verifica che i nodi esistano (al salvataggio, non alla lettura: un nodo
// cancellato dopo resta nel layout e risulta mancante).
func checkNodes(ctx context.Context, tx pgx.Tx, l *DiagramLayout) error {
	if l == nil || len(l.Nodes) == 0 {
		return nil
	}
	ids := make([]string, len(l.Nodes))
	for i, n := range l.Nodes {
		ids[i] = n.ID
	}
	var found int
	if err := tx.QueryRow(ctx, `select count(*) from elements where id = any($1::uuid[]) and deleted_at is null`, ids).Scan(&found); err != nil {
		return err
	}
	if found != len(ids) {
		return engine.NotFound("layout", "", "%d nodi del diagramma non esistono", len(ids)-found)
	}
	return nil
}

func (s *Service) CreateDiagram(ctx context.Context, tx pgx.Tx, actor engine.Actor, in DiagramInput) (*DiagramView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if in.Name == nil {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if in.Kind == nil {
		k := "dependency"
		in.Kind = &k
	}
	if in.Layout == nil {
		in.Layout = &DiagramLayout{Nodes: []DiagramNode{}, Notes: []DiagramNote{}}
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if err := checkNodes(ctx, tx, in.Layout); err != nil {
		return nil, err
	}
	layout, _ := json.Marshal(in.Layout)
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into diagrams (tenant_id, id, space_id, name, kind, description, layout, created_by, updated_by)
		select app_tenant(), $1, s.id, $2, $3, $4, $5, $6, $6 from spaces s where s.key = $7`,
		id, *in.Name, *in.Kind, deref(in.Description), layout, nullable(actor.MemberID), SpaceKey); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "diagram.create", "diagram", id, map[string]any{"nodes": len(in.Layout.Nodes)}); err != nil {
		return nil, err
	}
	return s.DiagramView(ctx, tx, actor, id)
}

func (s *Service) UpdateDiagram(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in DiagramInput) (*DiagramView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if err := checkNodes(ctx, tx, in.Layout); err != nil {
		return nil, err
	}
	var version int
	err := tx.QueryRow(ctx, `select version from diagrams where id = $1 for update`, id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.Version != version {
		return nil, engine.ErrConflict
	}
	var layout any
	if in.Layout != nil {
		b, _ := json.Marshal(in.Layout)
		layout = b
	}
	if _, err := tx.Exec(ctx, `update diagrams set name = coalesce($2, name), kind = coalesce($3, kind), description = coalesce($4, description),
		layout = coalesce($5::jsonb, layout), version = version + 1, updated_by = $6, updated_at = now() where id = $1`,
		id, in.Name, in.Kind, in.Description, layout, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "diagram.update", "diagram", id, nil); err != nil {
		return nil, err
	}
	return s.DiagramView(ctx, tx, actor, id)
}

func (s *Service) DeleteDiagram(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if !actor.Has(PermWrite) {
		return engine.ErrForbidden
	}
	if !uuidRe.MatchString(id) {
		return engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `delete from diagrams where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return s.auditEA(ctx, tx, actor, "diagram.delete", "diagram", id, nil)
}
