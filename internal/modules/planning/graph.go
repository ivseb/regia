package planning

import (
	"math"
	"time"

	"regia/internal/engine"
	"regia/internal/platform/calendar"
)

// Tipi di dipendenza tra le voci di un piano (replica/progetti.md §3): fine→inizio (la
// predefinita, l'unica degli impatti di una richiesta), inizio→inizio e fine→fine.
const (
	DepFS = "fs"
	DepSS = "ss"
	DepFF = "ff"
)

// Dep è una dipendenza da un predecessore, con il ritardo in giorni lavorativi.
type Dep struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // fs | ss | ff
	Lag  int    `json:"lag"`
}

// graph sono le voci di un piano con le dipendenze.
type graph struct {
	ids        []string
	nodes      map[string]*node
	dependents map[string][]edge // id del predecessore → chi dipende da lui
}

type edge struct {
	to   string
	kind string
	lag  int
}

type node struct {
	t          Task
	start, end *time.Time
}

// deps restituisce le dipendenze della voce: quelle tipizzate se ci sono, altrimenti
// DependsOn come fine→inizio senza ritardo (impatti delle richieste).
func (t Task) deps() []Dep {
	if t.Deps != nil {
		return t.Deps
	}
	out := make([]Dep, len(t.DependsOn))
	for i, id := range t.DependsOn {
		out[i] = Dep{ID: id, Kind: DepFS}
	}
	return out
}

func newGraph(tasks []Task) *graph {
	g := &graph{nodes: map[string]*node{}, dependents: map[string][]edge{}}
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
		for _, d := range t.deps() {
			if _, ok := g.nodes[d.ID]; ok {
				g.dependents[d.ID] = append(g.dependents[d.ID], edge{to: t.ID, kind: d.Kind, lag: d.Lag})
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

// startFor è il primo giorno di una voce di n giorni lavorativi che finisce il giorno end.
func startFor(end time.Time, n int) time.Time {
	t := calendar.Day(end)
	for n > 1 {
		t = t.AddDate(0, 0, -1)
		if calendar.Workday(t) {
			n--
		}
	}
	return t
}

// earliest calcola dove deve stare il successore per rispettare la dipendenza dal
// predecessore p: restituisce le date nuove e true se deve slittare in avanti. La milestone
// (durata zero) come successore fine→inizio può cadere il giorno stesso della fine.
func earliest(p, d *node, kind string, lag int) (time.Time, time.Time, bool) {
	dur := calendar.Workdays(*d.start, *d.end)
	if d.t.Milestone {
		dur = 1
	}
	switch kind {
	case DepSS:
		s := calendar.AddWorkdays(*p.start, lag)
		if !d.start.Before(s) {
			return time.Time{}, time.Time{}, false
		}
		return s, calendar.EndAfter(s, dur), true
	case DepFF:
		e := calendar.AddWorkdays(*p.end, lag)
		if !d.end.Before(e) {
			return time.Time{}, time.Time{}, false
		}
		if d.t.Milestone {
			return e, e, true
		}
		return startFor(e, dur), e, true
	default:
		n := 1 + lag
		if d.t.Milestone {
			n = lag
		}
		s := calendar.AddWorkdays(*p.end, n)
		if !d.start.Before(s) {
			return time.Time{}, time.Time{}, false
		}
		if d.t.Milestone {
			return s, s, true
		}
		return s, calendar.EndAfter(s, dur), true
	}
}

// cascade fa slittare in avanti chi dipende da id (e a catena) quando non rispetterebbe la
// dipendenza. Non anticipa mai: lo slittamento è solo in avanti.
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
		for _, e := range g.dependents[cur] {
			dn := g.nodes[e.to]
			if dn.start == nil || dn.t.StateCategory == "cancelled" {
				continue
			}
			s, en, ok := earliest(cn, dn, e.kind, e.lag)
			if !ok {
				continue
			}
			g.set(e.to, s, en)
			moved[e.to] = true
			queue = append(queue, e.to)
		}
	}
	// Le modifiche riportano le date finali di ogni voce spostata.
	for _, id := range g.ids {
		if moved[id] {
			out = append(out, g.change(id, "cascade"))
		}
	}
	return out
}

// topo restituisce le voci in ordine di dipendenza; un ciclo è un errore.
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
		for _, d := range g.nodes[id].t.deps() {
			if _, ok := g.nodes[d.ID]; ok {
				if err := visit(d.ID); err != nil {
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

// ---------------------------------------------------------------------------
// Scheduler: lo stesso motore per il piano dei progetti (modulo projects).

// Scheduler pianifica voci qualsiasi (work package, attività, milestone) con le stesse
// regole del piano della richiesta: giorni lavorativi, cascata solo in avanti, cicli vietati.
type Scheduler struct{ g *graph }

// NewScheduler costruisce il grafo delle voci.
func NewScheduler(tasks []Task) *Scheduler { return &Scheduler{g: newGraph(tasks)} }

// Has dice se la voce fa parte del piano.
func (s *Scheduler) Has(id string) bool { _, ok := s.g.nodes[id]; return ok }

// Move sposta o ridimensiona una voce e, con cascade, fa slittare chi ne dipende. La
// milestone ha inizio e fine uguali.
func (s *Scheduler) Move(id string, start, end time.Time, cascade bool) []Change {
	n := s.g.nodes[id]
	if n.t.Milestone {
		start = end
	}
	s.g.set(id, calendar.Day(start), calendar.Day(end))
	changes := []Change{s.g.change(id, "moved")}
	if cascade {
		changes = append(changes, s.g.cascade(id)...)
	}
	return changes
}

// Auto dà le date alle voci senza date (o a tutte con replan), in ordine di dipendenza, a
// partire da base. La durata è la stima divisa per la capacità giornaliera; le milestone
// hanno durata zero.
func (s *Scheduler) Auto(base time.Time, replan bool) ([]Change, error) {
	order, err := s.g.topo()
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	for _, id := range order {
		n := s.g.nodes[id]
		if n.t.StateCategory == "cancelled" || (n.start != nil && !replan) {
			continue
		}
		days := defaultDurationDays
		if n.t.EffortDays != nil && *n.t.EffortDays > 0 {
			capacity := n.t.Capacity
			if capacity <= 0 {
				capacity = defaultCapacity
			}
			days = int(math.Max(1, math.Ceil(*n.t.EffortDays/(capacity/5))))
		}
		st := calendar.NextWorkday(base)
		en := calendar.EndAfter(st, days)
		if n.t.Milestone {
			en = st
		}
		s.g.set(id, st, en)
		for _, d := range n.t.deps() {
			p, ok := s.g.nodes[d.ID]
			if !ok || p.end == nil || p.t.StateCategory == "cancelled" {
				continue
			}
			if ns, ne, move := earliest(p, n, d.Kind, d.Lag); move {
				s.g.set(id, ns, ne)
			}
		}
		changes = append(changes, s.g.change(id, "auto"))
	}
	return changes, nil
}

// Span restituisce le date correnti della voce (nil se non pianificata).
func (s *Scheduler) Span(id string) (*time.Time, *time.Time) {
	n, ok := s.g.nodes[id]
	if !ok {
		return nil, nil
	}
	return n.start, n.end
}

// CheckCycle dice se le dipendenze formano un ciclo.
func (s *Scheduler) CheckCycle() error {
	_, err := s.g.topo()
	return err
}

// Violated dice se la voce non rispetta una dipendenza (per esempio dopo una modifica fatta
// senza cascata): serve all'avviso «dipendenza non rispettata» della Panoramica.
func (s *Scheduler) Violated(id string) []string {
	n := s.g.nodes[id]
	if n == nil || n.start == nil {
		return nil
	}
	var out []string
	for _, d := range n.t.deps() {
		p, ok := s.g.nodes[d.ID]
		if !ok || p.end == nil || p.t.StateCategory == "cancelled" {
			continue
		}
		if _, _, move := earliest(p, n, d.Kind, d.Lag); move {
			out = append(out, d.ID)
		}
	}
	return out
}
