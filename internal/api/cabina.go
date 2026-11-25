package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/governance"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/planning"
	"regia/internal/modules/portfolio"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/rbac"
)

// Cabina di portafoglio (ondata 3, proposta 9): graduatoria, budget, capacità dei team, roadmap e
// scenari sulla stessa pagina, con UNA chiamata (non una per riga). Il pacchetto della seduta
// aggiunge all'ordine del giorno posizione, valutazioni e requisiti, e genera il verbale in Knowledge.

// cabinaPlafond è il plafond dell'esercizio in corso (o di quello collegato alla graduatoria).
type cabinaPlafond struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	EnvelopeCents  int64  `json:"envelope_cents"`
	AllocatedCents int64  `json:"allocated_cents"`
	ResidualCents  int64  `json:"residual_cents"`
	Linked         bool   `json:"linked"` // l'esercizio alimenta già la graduatoria
	Basis          string `json:"basis"`  // envelope | residual
}

// cabinaRow arricchisce una riga della graduatoria: sponsor, unità, rischio di esecuzione, sforzo per team.
type cabinaRow struct {
	Sponsor string                `json:"sponsor"`
	Unit    string                `json:"unit"`
	Risk    []string              `json:"risk"` // late | unplanned | no_estimate | no_score
	Efforts []planning.TeamEffort `json:"efforts"`
}

type cabinaOut struct {
	Ranking *portfolio.Ranking `json:"ranking"`
	// BudgetOrigin: allocation | fiscal_year | plafond (proposto, non ancora collegato) | simulation | manual | none.
	BudgetOrigin string                            `json:"budget_origin"`
	Plafond      *cabinaPlafond                    `json:"plafond"`
	Rows         map[string]*cabinaRow             `json:"rows"`
	Windows      map[string]planning.RequestWindow `json:"windows"`
	Weeks        []string                          `json:"weeks"`
	Teams        []planning.TeamCapacity           `json:"teams"`
	Scenarios    []portfolio.Scenario              `json:"scenarios"`
}

func (s *Server) plafondOf(tx pgx.Tx, r *http.Request) (*cabinaPlafond, error) {
	var pl cabinaPlafond
	var env *int64
	err := tx.QueryRow(r.Context(), `
		select f.id::text, f.name, f.envelope_cents,
		       coalesce((select sum(l.amount_cents) from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
		                 where b.fiscal_year_id = f.id and b.status = 'approved' and b.kind <> 'forecast'), 0),
		       coalesce(f.id = fs.portfolio_fiscal_year_id, false), coalesce(fs.portfolio_basis, 'residual')
		from fiscal_years f left join finance_settings fs on fs.tenant_id = f.tenant_id
		where f.envelope_cents is not null and f.status <> 'closed'
		  and (f.id = fs.portfolio_fiscal_year_id or (f.start_date <= current_date and f.end_date >= current_date))
		order by coalesce(f.id = fs.portfolio_fiscal_year_id, false) desc, f.start_date desc limit 1`).
		Scan(&pl.ID, &pl.Name, &env, &pl.AllocatedCents, &pl.Linked, &pl.Basis)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if env == nil {
		return nil, nil
	}
	pl.EnvelopeCents = *env
	pl.ResidualCents = max(*env-pl.AllocatedCents, 0)
	return &pl, nil
}

func (s *Server) getCabina(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budget, ok := budgetParam(w, r)
	if !ok {
		return
	}
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*cabinaOut, error) {
		ctx, actor := r.Context(), actorOf(r, p)
		pl, err := s.plafondOf(tx, r)
		if err != nil {
			return nil, err
		}
		rk, err := s.rankingScoped(r, p, tx, budget)
		if err != nil {
			return nil, err
		}
		out := &cabinaOut{Plafond: pl, Rows: map[string]*cabinaRow{}, Windows: map[string]planning.RequestWindow{}}
		switch {
		case budget != nil:
			out.BudgetOrigin = "simulation"
		case rk.BudgetSource.Kind == "allocation":
			out.BudgetOrigin = "allocation"
		case rk.BudgetSource.Kind == "fiscal_year":
			out.BudgetOrigin = "fiscal_year"
		case rk.BudgetSource.Kind == "manual":
			out.BudgetOrigin = "manual"
		case pl != nil:
			// Nessun collegamento e nessun valore a mano: il plafond dell'esercizio in corso è la base, senza
			// aspettare che qualcuno lo attivi. Il collegamento resta una scelta (pulsante nella pagina).
			base := pl.ResidualCents
			if rk, err = s.rankingScoped(r, p, tx, &base); err != nil {
				return nil, err
			}
			out.BudgetOrigin = "plafond"
		default:
			out.BudgetOrigin = "none"
		}
		out.Ranking = rk
		ids := make([]string, len(rk.Items))
		for i, it := range rk.Items {
			ids[i] = it.ID
		}
		wins, err := s.plan.Roadmap(ctx, tx, actor, false)
		if err != nil {
			return nil, err
		}
		for _, w := range wins {
			out.Windows[w.ID] = w
		}
		load, err := s.plan.PortfolioLoad(ctx, tx, actor, ids)
		if err != nil {
			return nil, err
		}
		out.Weeks, out.Teams = load.Weeks, load.Teams
		rows, err := tx.Query(ctx, `
			select d.element_id::text, coalesce(u.name, ''),
			       coalesce((select string_agg(coalesce(m.display_name, g.name, ou.name), ', ' order by coalesce(m.display_name, g.name, ou.name))
			                 from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'sponsor'
			                 left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
			                 left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
			                 left join org_units ou on ou.tenant_id = sh.tenant_id and ou.id = sh.org_unit_id
			                 where sh.element_id = d.element_id), '')
			from ext_demand d left join org_units u on u.tenant_id = d.tenant_id and u.id = d.requesting_unit_id
			where d.element_id = any($1::uuid[])`, ids)
		if err != nil {
			return nil, err
		}
		type extra struct{ id, unit, sponsor string }
		ex, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (extra, error) {
			var e extra
			return e, r.Scan(&e.id, &e.unit, &e.sponsor)
		})
		if err != nil {
			return nil, err
		}
		for _, e := range ex {
			out.Rows[e.id] = &cabinaRow{Unit: e.unit, Sponsor: e.sponsor, Risk: []string{}, Efforts: []planning.TeamEffort{}}
		}
		for _, it := range rk.Items {
			row := out.Rows[it.ID]
			if row == nil {
				row = &cabinaRow{Risk: []string{}}
				out.Rows[it.ID] = row
			}
			if ef := load.Requests[it.ID]; ef != nil {
				row.Efforts = ef
			} else if row.Efforts == nil {
				row.Efforts = []planning.TeamEffort{}
			}
			win, planned := out.Windows[it.ID]
			planned = planned && win.Start != nil && win.End != nil
			if !planned {
				row.Risk = append(row.Risk, "unplanned")
			} else if win.RegulatoryDeadline != nil && *win.End > *win.RegulatoryDeadline {
				row.Risk = append(row.Risk, "late")
			}
			if it.EstimateCents == nil {
				row.Risk = append(row.Risk, "no_estimate")
			}
			if it.Score == nil {
				row.Risk = append(row.Risk, "no_score")
			}
		}
		if out.Scenarios, err = s.portfolio.Scenarios(ctx, tx); err != nil {
			return nil, err
		}
		return out, nil
	})
}

func (s *Server) saveScenario(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in portfolio.ScenarioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*portfolio.Scenario, error) {
		return s.portfolio.SaveScenario(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) deleteScenario(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.portfolio.DeleteScenario(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// ---------------------------------------------------------------------
// Pacchetto della seduta
// ---------------------------------------------------------------------

type sedutaReview struct {
	Type     string  `json:"type"`
	Stage    string  `json:"stage"`
	Outcome  *string `json:"outcome"`
	Polarity *string `json:"polarity"`
}

type sedutaRequirements struct {
	Total    int `json:"total"`
	Verified int `json:"verified"`
}

type sedutaItem struct {
	governance.Item
	Position     *int               `json:"position"`
	Reviews      []sedutaReview     `json:"reviews"`
	Requirements sedutaRequirements `json:"requirements"`
}

type sedutaPackage struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	ScheduledAt string       `json:"scheduled_at"`
	Status      string       `json:"status"`
	Location    string       `json:"location"`
	CanManage   bool         `json:"can_manage"`
	Items       []sedutaItem `json:"items"`
	Total       int64        `json:"total_cents"`
}

func (s *Server) sedutaOf(r *http.Request, p *auth.Principal, tx pgx.Tx) (*governance.Meeting, *sedutaPackage, error) {
	ctx, actor := r.Context(), actorOf(r, p)
	m, err := s.gov.Meeting(ctx, tx, actor, r.PathValue("id"))
	if err != nil {
		return nil, nil, err
	}
	pk := &sedutaPackage{ID: m.ID, Title: m.Title, Body: m.Body, ScheduledAt: m.ScheduledAt.Format("2006-01-02T15:04:05Z07:00"),
		Status: m.Status, Location: m.Location, CanManage: m.CanManage, Items: []sedutaItem{}}
	ids := make([]string, len(m.Items))
	for i, it := range m.Items {
		ids[i] = it.ElementID
	}
	rk, err := s.portfolio.Ranking(ctx, tx, actor, nil)
	if err != nil {
		return nil, nil, err
	}
	pos := map[string]int{}
	for _, it := range rk.Items {
		pos[it.ID] = it.Position
	}
	rev := map[string][]sedutaReview{}
	rows, err := tx.Query(ctx, `select r.request_id::text, rt.name, r.stage, r.outcome_label, r.outcome_polarity
		from ext_review r join review_types rt on rt.tenant_id = r.tenant_id and rt.id = r.review_type_id
		where r.request_id = any($1::uuid[]) and r.stage <> 'cancelled' and r.superseded_by is null order by rt.name`, ids)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var rv sedutaReview
		if err := rows.Scan(&id, &rv.Type, &rv.Stage, &rv.Outcome, &rv.Polarity); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rev[id] = append(rev[id], rv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	req := map[string]sedutaRequirements{}
	rows, err = tx.Query(ctx, `select request_id::text, count(*)::int, (count(*) filter (where stage = 'verified'))::int
		from ext_requirement where request_id = any($1::uuid[]) and stage not in ('waived', 'not_applicable') group by request_id`, ids)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var rq sedutaRequirements
		if err := rows.Scan(&id, &rq.Total, &rq.Verified); err != nil {
			rows.Close()
			return nil, nil, err
		}
		req[id] = rq
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, it := range m.Items {
		si := sedutaItem{Item: it, Reviews: rev[it.ElementID], Requirements: req[it.ElementID]}
		if si.Reviews == nil {
			si.Reviews = []sedutaReview{}
		}
		if n, ok := pos[it.ElementID]; ok {
			si.Position = &n
		}
		if it.AmountCents != nil {
			pk.Total += *it.AmountCents
		}
		pk.Items = append(pk.Items, si)
	}
	return m, pk, nil
}

func (s *Server) getSedutaPackage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*sedutaPackage, error) {
		_, pk, err := s.sedutaOf(r, p, tx)
		return pk, err
	})
}

// eurIT scrive un importo come «1.234.500 €».
func eurIT(c int64) string {
	n := c / 100
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s + " €"
}

func cell(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " ") }

// verbaleBody scrive il verbale in Markdown dalla seduta: presenze, quorum, tabella delle decisioni.
func verbaleBody(m *governance.Meeting, pk *sedutaPackage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Verbale — %s\n\n", m.Title)
	fmt.Fprintf(&b, "%s · seduta del %s", m.Body, m.ScheduledAt.Format("02/01/2006 15:04"))
	if m.Location != "" {
		fmt.Fprintf(&b, " · %s", m.Location)
	}
	b.WriteString("\n\n## Presenze\n\n")
	var present, absent []string
	for _, a := range m.Attendance {
		if a.Present {
			present = append(present, a.Name)
		} else {
			absent = append(absent, a.Name)
		}
	}
	none := func(xs []string) string {
		if len(xs) == 0 {
			return "nessuno"
		}
		return strings.Join(xs, ", ")
	}
	fmt.Fprintf(&b, "- Presenti: %s\n- Assenti: %s\n", none(present), none(absent))
	verdict := "raggiunto"
	if !m.Quorum.Reached {
		verdict = "non raggiunto"
	}
	fmt.Fprintf(&b, "- Quorum: %d votanti presenti su %d, richiesti %d: %s\n", m.Quorum.Present, m.Quorum.Voting, m.Quorum.Required, verdict)
	b.WriteString("\n## Ordine del giorno e decisioni\n\n| # | Richiesta | Importo | Punteggio | Posizione | Decisione |\n|---|---|---|---|---|---|\n")
	for i, it := range pk.Items {
		amount, score, pos, outcome := "—", "—", "—", "non discussa"
		if it.AmountCents != nil {
			amount = eurIT(*it.AmountCents)
		}
		if it.Score != nil {
			score = fmt.Sprintf("%.1f", *it.Score)
		}
		if it.Position != nil {
			pos = fmt.Sprint(*it.Position)
		}
		if it.Outcome != nil {
			outcome = governance.Outcomes[*it.Outcome]
		}
		fmt.Fprintf(&b, "| %d | %s %s | %s | %s | %s | %s |\n", i+1, it.Key, cell(it.Title), amount, score, pos, outcome)
	}
	for _, it := range pk.Items {
		if it.Outcome == nil {
			continue
		}
		fmt.Fprintf(&b, "\n### %s %s\n\nDecisione: %s.\n", it.Key, it.Title, governance.Outcomes[*it.Outcome])
		for _, rv := range it.Reviews {
			label := "in corso"
			if rv.Outcome != nil {
				label = *rv.Outcome
			}
			fmt.Fprintf(&b, "\n- Valutazione %s: %s", rv.Type, label)
		}
		if it.Requirements.Total > 0 {
			fmt.Fprintf(&b, "\n- Requisiti: %d verificati su %d", it.Requirements.Verified, it.Requirements.Total)
		}
		if it.Conditions != nil && *it.Conditions != "" {
			fmt.Fprintf(&b, "\n\nCondizioni: %s", *it.Conditions)
		}
		if it.Note != nil && *it.Note != "" {
			fmt.Fprintf(&b, "\n\nMotivazione e note: %s", *it.Note)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if m.Chair != nil {
		fmt.Fprintf(&b, "Il presidente: %s\n\n", *m.Chair)
	}
	if m.Secretary != nil {
		fmt.Fprintf(&b, "Il segretario: %s\n", *m.Secretary)
	}
	return b.String()
}

type verbaleIn struct {
	Space string `json:"space"` // spazio di Knowledge (vuoto = il primo in cui si può scrivere)
}

type verbaleOut struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Space   string `json:"space"`
	Created bool   `json:"created"`
	Linked  int    `json:"linked"`
}

// generateVerbale crea (una volta sola per seduta) la pagina del verbale in Knowledge e la collega alle
// richieste decise. Riusa le API di Knowledge: creazione della pagina e relazione «documenta».
func (s *Server) generateVerbale(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in verbaleIn
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*verbaleOut, error) {
		ctx, actor := r.Context(), actorOf(r, p)
		m, pk, err := s.sedutaOf(r, p, tx)
		if err != nil {
			return nil, err
		}
		if !m.CanManage && !actor.Has(rbac.PortfolioAdmin) {
			return nil, engine.ErrForbidden
		}
		decided := 0
		for _, it := range pk.Items {
			if it.Outcome != nil {
				decided++
			}
		}
		if decided == 0 {
			return nil, engine.InvalidCode("validation.portfolio.minutes_no_decisions", "id", "nessun punto ha una decisione: il verbale si genera dopo le decisioni")
		}
		spaces, err := s.kb.Spaces(ctx, tx, actor)
		if err != nil {
			return nil, err
		}
		var space *knowledge.Space
		for i := range spaces {
			if spaces[i].Rights.Write && (in.Space == "" || spaces[i].Key == in.Space) {
				space = &spaces[i]
				break
			}
		}
		if space == nil {
			return nil, engine.InvalidCode("validation.portfolio.minutes_no_space", "space", "nessuno spazio di Knowledge in cui puoi scrivere")
		}
		title := fmt.Sprintf("Verbale — %s — %s", m.Title, m.ScheduledAt.Format("02/01/2006"))
		out := &verbaleOut{Title: title, Space: space.Key}
		err = tx.QueryRow(ctx, `select sp.key || '-' || e.number from elements e
			join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = $3
			where e.deleted_at is null and sp.key = $1 and e.title = $2 limit 1`, space.Key, title, knowledge.TypePage).Scan(&out.Key)
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		if err == pgx.ErrNoRows {
			pv, err := s.kb.CreatePage(ctx, tx, actor, space.Key, knowledge.CreateInput{Title: title, Body: verbaleBody(m, pk), Note: "Generato dalla cabina di portafoglio"})
			if err != nil {
				return nil, err
			}
			out.Key, out.Created = pv.Page.Key, true
			if err := audit.Append(ctx, tx, audit.Event{Action: "portfolio.minutes.generate", EntityType: "meeting", EntityID: m.ID,
				ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"page": pv.Page.Key, "decisions": decided}}); err != nil {
				return nil, err
			}
		}
		// Collega (anche a una pagina già generata: le decisioni nuove si aggiungono) le richieste decise.
		for _, it := range pk.Items {
			if it.Outcome == nil {
				continue
			}
			if _, err := s.kb.Link(ctx, tx, actor, out.Key, it.ElementID); err != nil {
				return nil, err
			}
			out.Linked++
		}
		return out, nil
	})
}
