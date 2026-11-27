package projects

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Mappa dei semafori del portafoglio (passo 6, progetti.md §6 «Programma e portafoglio»): i
// progetti che l'attore legge, raggruppabili per programma, con un semaforo per ambito (tempi,
// budget, perimetro, rischi, conflitti) dedotto dagli stessi numeri della Panoramica. La stessa
// risposta serve la roadmap di portafoglio (§4), che mostra i progetti accanto alle richieste.

// Signals sono i semafori per ambito: green | amber | red | none (non misurabile) | hidden (il
// budget per chi non lo vede).
type Signals struct {
	Schedule  string `json:"schedule"`
	Budget    string `json:"budget"`
	Scope     string `json:"scope"`
	Risk      string `json:"risk"`
	Conflicts string `json:"conflicts"`
}

// PortfolioProject è un progetto nella mappa: la riga della roadmap, il programma e i semafori.
type PortfolioProject struct {
	ProgramProject
	Program  *Ref    `json:"program"`
	Template string  `json:"template"`
	Signals  Signals `json:"signals"`
}

// Portfolio è la mappa dei semafori.
type Portfolio struct {
	Projects []PortfolioProject `json:"projects"`
	Programs []Ref              `json:"programs"`
}

func hasAny(reasons []string, codes ...string) bool {
	for _, c := range codes {
		if slices.Contains(reasons, c) {
			return true
		}
	}
	return false
}

// signalsOf deduce i semafori per ambito dai numeri del progetto.
func signalsOf(n *Numbers, p *Plan) Signals {
	sg := Signals{Schedule: "green", Budget: "none", Scope: "none", Risk: "green", Conflicts: "green"}
	switch {
	case n.Signal == "none":
		sg.Schedule = "none"
	case hasAny(n.Reasons, "milestone_late", "past_target", "over_tolerance"):
		sg.Schedule = "red"
	case hasAny(n.Reasons, "work_package_late", "blocked", "external_late"):
		sg.Schedule = "amber"
	}
	switch b := n.Budget; {
	case !n.BudgetVisible:
		sg.Budget = "hidden"
	case b == nil:
	case n.BudgetOverTolerance:
		sg.Budget = "red"
	case b.ApprovedCents != nil && (b.ForecastCents != nil && *b.ForecastCents > *b.ApprovedCents || b.SpentCents+b.CommittedCents > *b.ApprovedCents):
		sg.Budget = "amber"
	default:
		sg.Budget = "green"
	}
	if p.Baseline != nil {
		sg.Scope = "green"
		if hasAny(n.Reasons, "scope_changed") || n.OpenChangeRequests > 0 {
			sg.Scope = "amber"
		}
	}
	if len(n.Risks) > 0 || len(n.MissingRoles) > 0 {
		sg.Risk = "amber"
	}
	if len(n.Conflicts) > 0 {
		sg.Conflicts = "amber"
	}
	return sg
}

// Portfolio restituisce i progetti leggibili (aperti, o anche i chiusi) con i semafori per ambito.
func (s *Service) Portfolio(ctx context.Context, tx pgx.Tx, actor engine.Actor, includeClosed bool) (*Portfolio, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	q := `select e.id::text from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'project'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft`
	if !includeClosed {
		q += ` and coalesce(o.category, '') not in ('done', 'cancelled')`
	}
	rows, err := tx.Query(ctx, q+` order by e.start_at nulls last, e.number limit 200`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := &Portfolio{Projects: []PortfolioProject{}, Programs: []Ref{}}
	seen := map[string]bool{}
	rd := s.reader(actor)
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			continue // non visibile all'attore
		}
		pp, p, n, err := s.projectRow(ctx, tx, actor, el, rd, true)
		if err != nil {
			return nil, err
		}
		row := PortfolioProject{ProgramProject: pp, Program: p.Project.Program, Template: p.Project.Template, Signals: signalsOf(n, p)}
		if pg := p.Project.Program; pg != nil && !seen[pg.ID] {
			seen[pg.ID] = true
			out.Programs = append(out.Programs, *pg)
		}
		out.Projects = append(out.Projects, row)
	}
	sort.SliceStable(out.Programs, func(i, j int) bool { return strings.ToLower(out.Programs[i].Title) < strings.ToLower(out.Programs[j].Title) })
	return out, nil
}
