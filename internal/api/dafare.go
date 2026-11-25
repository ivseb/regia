package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/report"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// «Da fare» (replica/usabilita.md, proposta 1): una sola pagina con ciò che aspetta la persona, letto dalle
// stesse fonti dei numeri già usati altrove (inbox delle approvazioni, delle valutazioni e dei requisiti,
// graduatoria, impatti, timesheet, status report, menzioni). Le decisioni portano il contesto (importo,
// punteggio, posizione in graduatoria, cosa manca); gli impatti la capacità libera del team.

const todoLimit = 100

// preAcceptance sono le fasi dell'impatto in cui il team deve ancora fare qualcosa (processo di partenza
// «impact_standard»): stimare, accettare il perimetro o rimandarlo all'analisi.
var preAcceptance = []string{"Da analizzare", "In analisi", "Stimato"}

type todoDecision struct {
	approval.InboxItem
	Subject string `json:"subject"`
	Type    string `json:"type"`
	Body    string `json:"body,omitempty"`
	// Contesto di una richiesta: punteggio, posizione in graduatoria, budget, cosa manca.
	IsRequest            bool     `json:"is_request"`
	Score                *float64 `json:"score"`
	Position             *int     `json:"position"`
	RankTotal            int      `json:"rank_total"`
	Funded               *bool    `json:"funded"`
	CumulativeCents      *int64   `json:"cumulative_cents"`
	BudgetCents          *int64   `json:"budget_cents"`
	Mandatory            bool     `json:"mandatory"`
	ReviewsOpen          int      `json:"reviews_open"`
	ReviewsMandatoryOpen int      `json:"reviews_mandatory_open"`
	RequirementsToAccept int      `json:"requirements_to_accept"`
}

type todoReview struct {
	ID              string  `json:"id"`
	Key             string  `json:"key"`
	Title           string  `json:"title"`
	Role            string  `json:"role"` // compile | team
	TypeName        string  `json:"type_name"`
	Team            string  `json:"team"`
	RequestKey      string  `json:"request_key"`
	RequestTitle    string  `json:"request_title"`
	Application     *string `json:"application"`
	Stage           string  `json:"stage"`
	Mandatory       bool    `json:"mandatory"`
	Due             *string `json:"due_on"`
	LateDays        int     `json:"late_days"`
	RequiredTotal   int     `json:"required_total"`
	RequiredMissing int     `json:"required_missing"`
}

type todoVerify struct {
	RequirementKey string    `json:"requirement_key"`
	RequirementID  string    `json:"requirement_id"`
	Title          string    `json:"title"`
	Category       string    `json:"category"`
	ImpactKey      string    `json:"impact_key"`
	Application    string    `json:"application"`
	Team           string    `json:"team"`
	RequestKey     string    `json:"request_key"`
	RequestTitle   string    `json:"request_title"`
	ImplementedBy  *string   `json:"implemented_by"`
	Since          time.Time `json:"since"`
}

type todoTransition struct {
	To              string   `json:"to"`
	Label           string   `json:"label"`
	Forward         bool     `json:"forward"`
	CommentRequired bool     `json:"comment_required"`
	Blocked         []string `json:"blocked"`
}

type todoImpact struct {
	ID           string           `json:"id"`
	Key          string           `json:"key"`
	Title        string           `json:"title"`
	State        string           `json:"state"`
	RequestKey   string           `json:"request_key"`
	RequestTitle string           `json:"request_title"`
	Application  string           `json:"application"`
	ChangeKind   string           `json:"change_kind"`
	TeamID       *string          `json:"team_id"`
	Team         *string          `json:"team"`
	EffortDays   *float64         `json:"effort_days"`
	From         *string          `json:"from"`
	To           *string          `json:"to"`
	CanEstimate  bool             `json:"can_estimate"`
	Transitions  []todoTransition `json:"transitions"`
	// Capacità del team nelle settimane dell'impatto (o nelle prossime quattro, senza date).
	CapacityDays *float64 `json:"capacity_days"`
	LoadDays     *float64 `json:"load_days"`
	FreeDays     *float64 `json:"free_days"`
	Weeks        int      `json:"weeks"`
}

type todoWeek struct {
	Kind    string `json:"kind"` // submit | approve
	Week    string `json:"week"`
	Minutes int    `json:"minutes"`
	// Solo per approve: chi ha inviato la settimana.
	MemberID string `json:"member_id,omitempty"`
	Member   string `json:"member,omitempty"`
}

type todoReport struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Due   string `json:"due"`
	Late  bool   `json:"late"`
	Draft bool   `json:"draft"`
}

type todoMeeting struct {
	At    time.Time `json:"at"`
	Body  string    `json:"body"`
	Title string    `json:"title"`
}

type todoCounts struct {
	Decisions     int  `json:"decisions"`
	Reviews       int  `json:"reviews"`
	Verifications int  `json:"verifications"`
	Impacts       int  `json:"impacts"`
	Time          int  `json:"time"`
	Reports       int  `json:"reports"`
	Mentions      int  `json:"mentions"`
	Total         int  `json:"total"`
	More          bool `json:"more"`
}

type todoResponse struct {
	Counts        todoCounts     `json:"counts"`
	Decisions     []todoDecision `json:"decisions"`
	Reviews       []todoReview   `json:"reviews"`
	Verifications []todoVerify   `json:"verifications"`
	Impacts       []todoImpact   `json:"impacts"`
	Time          []todoWeek     `json:"time"`
	Reports       []todoReport   `json:"reports"`
	Mentions      []notify.Item  `json:"mentions"`
	NextMeeting   *todoMeeting   `json:"next_meeting"`
	Silenced      []string       `json:"silenced"`
	CanTime       bool           `json:"can_time"`
}

func (s *Server) todoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/todo", inTenant(s.todo))
	mux.HandleFunc("GET /api/v1/todo/count", inTenant(s.todoCount))
	mux.HandleFunc("GET /api/v1/me/silenced", inTenant(s.listSilenced))
	mux.HandleFunc("PUT /api/v1/me/silenced/{id}", inTenant(s.silenceElement))
	mux.HandleFunc("DELETE /api/v1/me/silenced/{id}", inTenant(s.unsilenceElement))
}

func (s *Server) todo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *todoResponse
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Finance: p.Can(rbac.FinanceRead), Member: p.MemberID}, func(tx pgx.Tx) error {
		var err error
		out, err = s.collectTodo(r, p, tx, true)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// todoCount dà i numeri per il menu e per il campanello: solo ciò che chiede un'azione.
func (s *Server) todoCount(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *todoResponse
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Member: p.MemberID}, func(tx pgx.Tx) error {
		var err error
		out, err = s.collectTodo(r, p, tx, false)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out.Counts)
}

func (s *Server) collectTodo(r *http.Request, p *auth.Principal, tx pgx.Tx, full bool) (*todoResponse, error) {
	ctx := r.Context()
	actor := actorOf(r, p)
	out := &todoResponse{Decisions: []todoDecision{}, Reviews: []todoReview{}, Verifications: []todoVerify{}, Impacts: []todoImpact{},
		Time: []todoWeek{}, Reports: []todoReport{}, Mentions: []notify.Item{}, Silenced: []string{}}
	more := false

	// Decisioni: i passi che l'attore può decidere.
	items, err := s.approvals.Inbox(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	// Le settimane di timesheet hanno la loro sezione («Tempo»): non si contano anche tra le decisioni.
	items = slices.DeleteFunc(items, func(it approval.InboxItem) bool { return it.ApprovalSubject == "timesheet" })
	out.Counts.Decisions = len(items)
	if len(items) > todoLimit {
		items, more = items[:todoLimit], true
	}
	if full {
		if out.Decisions, err = s.decisionContext(r, p, tx, items); err != nil {
			return nil, err
		}
	}

	// Valutazioni da compilare o da esaminare.
	rin, err := s.reviews.InboxN(ctx, tx, actor, todoLimit)
	if err != nil {
		return nil, err
	}
	out.Counts.Reviews = len(rin.Compile) + len(rin.Team)
	more = more || rin.More
	if full {
		for _, group := range [][]reviews.Summary{rin.Compile, rin.Team} {
			for _, x := range group {
				out.Reviews = append(out.Reviews, todoReview{ID: x.ID, Key: x.Key, Title: x.Title, Role: x.Role, TypeName: x.TypeName, Team: x.Team,
					RequestKey: x.RequestKey, RequestTitle: x.RequestTitle, Application: x.Application, Stage: x.Stage, Mandatory: x.Mandatory,
					Due: x.Due, LateDays: x.LateDays, RequiredTotal: x.RequiredTotal, RequiredMissing: x.RequiredMissing})
			}
		}
	}

	// Verifiche: celle da verificare.
	ver, vmore, err := s.req.InboxN(ctx, tx, actor, todoLimit)
	if err != nil {
		return nil, err
	}
	out.Counts.Verifications = len(ver)
	more = more || vmore
	if full {
		for _, v := range ver {
			out.Verifications = append(out.Verifications, todoVerify{RequirementKey: v.RequirementKey, RequirementID: v.RequirementID, Title: v.Title, Category: v.Category,
				ImpactKey: v.ImpactKey, Application: v.Application, Team: v.Team, RequestKey: v.RequestKey, RequestTitle: v.RequestTitle,
				ImplementedBy: v.ImplementedBy, Since: v.Since})
		}
	}

	// Impatti del team dell'attore.
	imp, err := s.teamImpacts(ctx, tx, actor, full)
	if err != nil {
		return nil, err
	}
	out.Counts.Impacts = len(imp)
	if full {
		out.Impacts = imp
	}

	// Timesheet da inviare o da approvare, status report da scrivere.
	weeks, err := s.timeWeeks(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	out.Counts.Time = len(weeks)
	reps, err := dueReports(ctx, tx, p.MemberID)
	if err != nil {
		return nil, err
	}
	out.Counts.Reports = len(reps)
	if full {
		out.Time, out.Reports = weeks, reps
		out.CanTime = actor.Has("element.write")
	}

	// Menzioni non lette (le sole notifiche che chiedono una risposta).
	men, err := unreadMentions(ctx, tx, p.MemberID)
	if err != nil {
		return nil, err
	}
	out.Counts.Mentions = len(men)
	if full {
		out.Mentions = men
	}

	c := &out.Counts
	c.More = more
	c.Total = c.Decisions + c.Reviews + c.Verifications + c.Impacts + c.Time + c.Reports + c.Mentions
	if full {
		if out.NextMeeting, err = nextMeeting(ctx, tx, p.MemberID); err != nil {
			return nil, err
		}
		if out.Silenced, err = notify.Silenced(ctx, tx, p.MemberID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Decisioni

// decisionContext arricchisce i passi con tipo, organo e, per le richieste, punteggio, posizione in
// graduatoria, budget e cosa manca (valutazioni obbligatorie aperte, requisiti da accettare).
func (s *Server) decisionContext(r *http.Request, p *auth.Principal, tx pgx.Tx, items []approval.InboxItem) ([]todoDecision, error) {
	ctx := r.Context()
	out := make([]todoDecision, 0, len(items))
	if len(items) == 0 {
		return out, nil
	}
	stepIDs := make([]string, len(items))
	for i, it := range items {
		stepIDs[i] = it.StepID
	}
	type meta struct{ subject, typ, body string }
	metas := map[string]meta{}
	rows, err := tx.Query(ctx, `select st.id::text, rq.subject, t.key, coalesce(b.name, '')
		from approval_steps st
		join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id
		join elements e on e.tenant_id = rq.tenant_id and e.id = rq.element_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join governance_bodies b on b.tenant_id = st.tenant_id and b.id = st.approver_body_id
		where st.id::text = any($1)`, stepIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var m meta
		if err := rows.Scan(&id, &m.subject, &m.typ, &m.body); err != nil {
			rows.Close()
			return nil, err
		}
		metas[id] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Valutazioni obbligatorie aperte e requisiti da accettare, per richiesta.
	elIDs := make([]string, len(items))
	for i, it := range items {
		elIDs[i] = it.ElementID
	}
	type gaps struct{ open, mandatory, reqs int }
	gap := map[string]*gaps{}
	g := func(id string) *gaps {
		if gap[id] == nil {
			gap[id] = &gaps{}
		}
		return gap[id]
	}
	rows, err = tx.Query(ctx, `select x.request_id::text, count(*), count(*) filter (where x.mandatory)
		from ext_review x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		where x.request_id::text = any($1) and x.stage not in ('concluded', 'cancelled') and x.superseded_by is null
		group by x.request_id`, elIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var a, b int
		if err := rows.Scan(&id, &a, &b); err != nil {
			rows.Close()
			return nil, err
		}
		g(id).open, g(id).mandatory = a, b
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select x.request_id::text, count(*)
		from ext_requirement x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		where x.request_id::text = any($1) and x.stage = 'proposed' group by x.request_id`, elIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		g(id).reqs = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Graduatoria: una volta sola, e solo se c'è almeno una richiesta tra le decisioni.
	var rk *portfolio.Ranking
	byID := map[string]*portfolio.Item{}
	for _, it := range items {
		if metas[it.StepID].typ == "business_request" {
			if rk, err = s.rankingScoped(r, p, tx, nil); err != nil {
				rk = nil // senza i permessi sulla graduatoria la riga resta senza posizione
			}
			break
		}
	}
	if rk != nil {
		for _, it := range rk.Items {
			byID[it.ID] = it
		}
	}
	for _, it := range items {
		m := metas[it.StepID]
		d := todoDecision{InboxItem: it, Subject: m.subject, Type: m.typ, Body: m.body, IsRequest: m.typ == "business_request"}
		if gp := gap[it.ElementID]; gp != nil {
			d.ReviewsOpen, d.ReviewsMandatoryOpen, d.RequirementsToAccept = gp.open, gp.mandatory, gp.reqs
		}
		if ri := byID[it.ElementID]; ri != nil && rk != nil {
			pos := ri.Position
			cum := ri.CumulativeCents
			d.Score, d.Position, d.RankTotal, d.Funded, d.CumulativeCents, d.BudgetCents, d.Mandatory = ri.Score, &pos, len(rk.Items), ri.Funded, &cum, rk.BudgetCents, ri.IsMandatory
		}
		out = append(out, d)
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Valutazioni

// ---------------------------------------------------------------------
// Impatti

// teamImpacts: gli impatti di cui l'attore è referente del team (sull'impatto, del team o membro del team)
// e che aspettano una stima o una decisione del team. Con full anche passaggi disponibili e capacità.
func (s *Server) teamImpacts(ctx context.Context, tx pgx.Tx, actor engine.Actor, full bool) ([]todoImpact, error) {
	rows, err := tx.Query(ctx, `
		select e.id::text, sp.key || '-' || e.number, e.title, coalesce(o.name, ''), rs.key || '-' || r.number, r.title, ap.title,
		       x.change_kind, x.team_id::text, t.name, x.estimate_effort_days::float8,
		       to_char(e.start_at, 'YYYY-MM-DD'), to_char(e.due_at, 'YYYY-MM-DD')
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null and not e.is_draft
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		join elements r on r.tenant_id = x.tenant_id and r.id = x.request_id and r.deleted_at is null
		join spaces rs on rs.tenant_id = r.tenant_id and rs.id = r.space_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		where o.name = any($2)
		  and (exists (select 1 from stakeholders sh join stakeholder_roles ro on ro.tenant_id = sh.tenant_id and ro.id = sh.role_id
		               where sh.element_id = e.id and ro.key in ('team_lead', 'technical_analyst')
		                 and (sh.member_id = $1 or sh.group_id in (select member_groups($1))))
		       or t.lead_id = $1
		       or exists (select 1 from team_members tm where tm.tenant_id = t.tenant_id and tm.team_id = t.id and tm.member_id = $1))
		order by e.due_at nulls last, e.created_at
		limit 100`, actor.MemberID, preAcceptance)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (todoImpact, error) {
		var i todoImpact
		err := r.Scan(&i.ID, &i.Key, &i.Title, &i.State, &i.RequestKey, &i.RequestTitle, &i.Application, &i.ChangeKind, &i.TeamID, &i.Team,
			&i.EffortDays, &i.From, &i.To)
		i.Transitions = []todoTransition{}
		return i, err
	})
	if err != nil || !full || len(out) == 0 {
		return out, err
	}
	// Passaggi disponibili, e chi può correggere la stima.
	for i := range out {
		el, err := s.eng.Get(ctx, tx, actor, out[i].ID)
		if err != nil {
			continue
		}
		tr, err := s.eng.Available(ctx, tx, actor, el)
		if err != nil {
			return nil, err
		}
		for _, t := range tr {
			out[i].Transitions = append(out[i].Transitions, todoTransition{To: t.To, Label: t.Label, Forward: t.Forward, CommentRequired: t.CommentRequired, Blocked: t.Blocked})
		}
		ok := actor.Has("element.write")
		if !ok {
			if ok, err = engine.HasRole(ctx, tx, out[i].ID, actor.MemberID, []string{"team_lead", "technical_analyst"}); err != nil {
				return nil, err
			}
		}
		out[i].CanEstimate = ok
	}
	// Capacità libera del team nelle settimane dell'impatto.
	if actor.Has("element.read") {
		today := time.Now().UTC()
		from, to := today.Format(time.DateOnly), today.AddDate(0, 0, 28).Format(time.DateOnly)
		for _, i := range out {
			if i.From != nil && *i.From < from {
				from = *i.From
			}
			if i.To != nil && *i.To > to {
				to = *i.To
			}
		}
		if to > time.Now().UTC().AddDate(2, 0, 0).Format(time.DateOnly) {
			to = time.Now().UTC().AddDate(2, 0, 0).Format(time.DateOnly)
		}
		loads, err := s.plan.Load(ctx, tx, actor, from, to)
		if err == nil {
			byTeam := map[string][]struct {
				week      string
				cap, load float64
			}{}
			for _, tl := range loads {
				for _, wk := range tl.Weeks {
					byTeam[tl.ID] = append(byTeam[tl.ID], struct {
						week      string
						cap, load float64
					}{wk.Week, wk.Capacity, wk.Load})
				}
			}
			for k := range out {
				i := &out[k]
				if i.TeamID == nil {
					continue
				}
				a, b := today.Format(time.DateOnly), today.AddDate(0, 0, 28).Format(time.DateOnly)
				if i.From != nil {
					a = *i.From
				}
				if i.To != nil {
					b = *i.To
				}
				var capD, loadD float64
				n := 0
				for _, wk := range byTeam[*i.TeamID] {
					// La settimana (lunedì) tocca l'intervallo [a, b]?
					wkEnd := weekEnd(wk.week)
					if wkEnd < a || wk.week > b {
						continue
					}
					capD += wk.cap
					loadD += wk.load
					n++
				}
				if n > 0 {
					free := capD - loadD
					i.CapacityDays, i.LoadDays, i.FreeDays, i.Weeks = &capD, &loadD, &free, n
				}
			}
		}
	}
	return out, nil
}

// weekEnd è la domenica della settimana che inizia il lunedì indicato (AAAA-MM-GG).
func weekEnd(monday string) string {
	t, err := time.Parse(time.DateOnly, monday)
	if err != nil {
		return monday
	}
	return t.AddDate(0, 0, 6).Format(time.DateOnly)
}

// ---------------------------------------------------------------------
// Tempo e status report

// timeWeeks: le settimane da inviare (con ore registrate e non ancora inviate; la corrente dal venerdì)
// e quelle inviate da altri che l'attore deve approvare.
func (s *Server) timeWeeks(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]todoWeek, error) {
	out := []todoWeek{}
	st, err := s.work.TimeSettings(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if st.RequireApproval && actor.MemberID != "" {
		now := time.Now()
		monday := now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7)).Format(time.DateOnly)
		rows, err := tx.Query(ctx, `
			select to_char(date_trunc('week', l.work_date), 'YYYY-MM-DD') as wk, sum(l.minutes)::int
			from work_logs l
			where l.member_id = $1 and l.work_date >= $2::date - 42 and l.work_date < $2::date + 7
			group by wk
			having not exists (select 1 from timesheets t where t.member_id = $1 and to_char(t.week_start, 'YYYY-MM-DD') = to_char(date_trunc('week', min(l.work_date)), 'YYYY-MM-DD')
			                   and t.status in ('submitted', 'approved'))
			   and sum(l.minutes) > 0
			order by wk`, actor.MemberID, monday)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var w todoWeek
			if err := rows.Scan(&w.Week, &w.Minutes); err != nil {
				rows.Close()
				return nil, err
			}
			// La settimana in corso si invia dal venerdì.
			if w.Week == monday && now.Weekday() != time.Friday && now.Weekday() != time.Saturday && now.Weekday() != time.Sunday {
				continue
			}
			w.Kind = "submit"
			out = append(out, w)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	pend, err := s.work.PendingWeeks(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, pw := range pend {
		out = append(out, todoWeek{Kind: "approve", Week: pw.Week, Minutes: pw.Minutes, MemberID: pw.MemberID, Member: pw.Member})
	}
	return out, nil
}

// dueReports: gli status report da scrivere (scadenza vicina o passata) sulle richieste di cui l'attore è
// project manager o, in mancanza, demand manager (gli stessi destinatari dei promemoria).
func dueReports(ctx context.Context, tx pgx.Tx, memberID string) ([]todoReport, error) {
	out := []todoReport{}
	st, err := report.GetSettings(ctx, tx)
	if err != nil || len(st.Phases) == 0 || st.Frequency == "off" {
		return out, err
	}
	today := time.Now().UTC().Format(time.DateOnly)
	rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, to_char(sc.next_due, 'YYYY-MM-DD'),
		       exists (select 1 from status_reports sr where sr.element_id = e.id and sr.status = 'draft')
		from status_report_schedules sc
		join elements e on e.tenant_id = sc.tenant_id and e.id = sc.element_id and e.deleted_at is null and e.resolved_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where o.name = any($1) and sc.next_due is not null and coalesce(sc.frequency, $2) <> 'off'
		  and sc.next_due - $3::int <= $4::date
		  and exists (select 1 from stakeholders sh join stakeholder_roles ro on ro.tenant_id = sh.tenant_id and ro.id = sh.role_id
		              where sh.element_id = e.id and ro.key in ('project_manager', 'demand_manager')
		                and (sh.member_id = $5 or sh.group_id in (select member_groups($5))))
		order by sc.next_due limit 100`, st.Phases, st.Frequency, st.ReminderDays, today, memberID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x todoReport
		if err := rows.Scan(&x.ID, &x.Key, &x.Title, &x.Due, &x.Draft); err != nil {
			rows.Close()
			return nil, err
		}
		x.Late = x.Due < today
		out = append(out, x)
	}
	rows.Close()
	return out, rows.Err()
}

// ---------------------------------------------------------------------
// Menzioni, prossima seduta, silenzia

func unreadMentions(ctx context.Context, tx pgx.Tx, memberID string) ([]notify.Item, error) {
	out := []notify.Item{}
	var cursor string
	// Le notifiche arrivano per data: si leggono finché non bastano cento menzioni (o finiscono).
	for page := 0; page < 5 && len(out) < todoLimit; page++ {
		pg, err := notify.List(ctx, tx, memberID, true, 200, cursor)
		if err != nil {
			return nil, err
		}
		for _, it := range pg.Items {
			if it.Kind == notify.KindMention {
				out = append(out, it)
			}
		}
		if pg.Next == "" {
			break
		}
		cursor = pg.Next
	}
	return out, nil
}

// nextMeeting: la prima seduta in programma degli organi di cui l'attore fa parte; se non è di nessun
// organo, la prima del tenant.
func nextMeeting(ctx context.Context, tx pgx.Tx, memberID string) (*todoMeeting, error) {
	for _, mine := range []bool{true, false} {
		var m todoMeeting
		err := tx.QueryRow(ctx, `select m.scheduled_at, b.name, m.title
			from meetings m join governance_bodies b on b.tenant_id = m.tenant_id and b.id = m.body_id and not b.archived
			where m.status in ('planned', 'convened') and m.scheduled_at >= now()
			  and (not $2 or m.body_id in (
			        select bm.body_id from governance_body_members bm where bm.member_id = $1
			        union select bg.body_id from governance_body_groups bg where bg.group_id in (select member_groups($1))
			        union select b2.id from governance_bodies b2 where b2.chair_id = $1 or b2.secretary_id = $1))
			order by m.scheduled_at limit 1`, memberID, mine).Scan(&m.At, &m.Body, &m.Title)
		if err == nil {
			return &m, nil
		}
		if err != pgx.ErrNoRows {
			return nil, err
		}
	}
	return nil, nil
}

func (s *Server) listSilenced(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ids, err := notify.Silenced(r.Context(), tx, p.MemberID)
		return map[string]any{"items": ids}, err
	})
}

func (s *Server) silenceElement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.setSilence(w, r, p, true)
}

func (s *Server) unsilenceElement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.setSilence(w, r, p, false)
}

func (s *Server) setSilence(w http.ResponseWriter, r *http.Request, p *auth.Principal, on bool) {
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		if err != nil {
			return err
		}
		return notify.Silence(r.Context(), tx, p.MemberID, el.ID, on)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
