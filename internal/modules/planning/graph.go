package planning

import (
	"time"

	"regia/internal/engine"
	"regia/internal/platform/calendar"
)

// graph sono gli impatti di una richiesta con le dipendenze (fine → inizio).
type graph struct {
	ids        []string
	nodes      map[string]*node
	dependents map[string][]string // id → impatti che dipendono da lui
}

type node struct {
	t          Task
	start, end *time.Time
}

func newGraph(tasks []Task) *graph {
	g := &graph{nodes: map[string]*node{}, dependents: map[string][]string{}}
	for _, t := range tasks {
		n := &node{t: t}
		if t.Start != nil && t.End != nil {
			s, _ := calendar.Parse(*t.Start)
			e, _ := calendar.Parse(*t.End)
			n.start, n.end = &s, &e
		}
		g.ids = append(g.ids, t.ID)
		g.nodes[t.ID] = n
	}
	for _, t := range tasks {
		for _, p := range t.DependsOn {
			if _, ok := g.nodes[p]; ok {
				g.dependents[p] = append(g.dependents[p], t.ID)
			}
		}
	}
	return g
}

func (g *graph) set(id string, start, end time.Time) {
	n := g.nodes[id]
	n.start, n.end = &start, &end
}

func (g *graph) change(id, cause string) Change {
	n := g.nodes[id]
	return Change{ID: id, Key: n.t.Key, Start: n.start.Format(time.DateOnly), End: n.end.Format(time.DateOnly), Cause: cause}
}

func (g *graph) scheduled() int {
	n := 0
	for _, x := range g.nodes {
		if x.start != nil {
			n++
		}
	}
	return n
}

// cascade fa slittare in avanti chi dipende da id (e a catena) quando inizierebbe
// prima della fine del predecessore. Non anticipa mai: lo slittamento è solo in avanti.
func (g *graph) cascade(id string) []Change {
	var out []Change
	queue := []string{id}
	moved := map[string]bool{}
	for steps := 0; len(queue) > 0 && steps < 10_000; steps++ {
		cur := queue[0]
		queue = queue[1:]
		cn := g.nodes[cur]
		if cn.end == nil {
			continue
		}
		for _, d := range g.dependents[cur] {
			dn := g.nodes[d]
			if dn.start == nil || dn.t.StateCategory == "cancelled" {
				continue
			}
			earliest := calendar.AfterEnd(*cn.end)
			if !dn.start.Before(earliest) {
				continue
			}
			dur := calendar.Workdays(*dn.start, *dn.end)
			g.set(d, earliest, calendar.EndAfter(earliest, dur))
			moved[d] = true
			queue = append(queue, d)
		}
	}
	// Le modifiche riportano le date finali di ogni impatto spostato.
	for _, id := range g.ids {
		if moved[id] {
			out = append(out, g.change(id, "cascade"))
		}
	}
	return out
}

// topo restituisce gli impatti in ordine di dipendenza; un ciclo è un errore.
func (g *graph) topo() ([]string, error) {
	state := map[string]int{} // 0 da visitare, 1 in corso, 2 fatto
	var out []string
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return engine.InvalidParams("validation.planning.dependency_cycle", "depends_on", map[string]any{"key": g.nodes[id].t.Key}, "le dipendenze tra gli impatti formano un ciclo (%s)", g.nodes[id].t.Key)
		case 2:
			return nil
		}
		state[id] = 1
		for _, p := range g.nodes[id].t.DependsOn {
			if _, ok := g.nodes[p]; ok {
				if err := visit(p); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		out = append(out, id)
		return nil
	}
	for _, id := range g.ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}
