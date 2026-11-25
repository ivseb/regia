package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
)

// Percorso della richiesta (ondata «semplicità»; migrazione: nessuna).
//
//	GET /api/v1/elements/{ref}/percorso
//
// Risponde a tre domande di chi guarda una richiesta (o un impatto): dove siamo nel processo, chi è
// coinvolto e che lavoro ne nasce, che cosa succede dopo e che cosa tocca a me. È una lettura
// aggregata in una sola transazione con i permessi e la RLS di chi chiede: ogni dato viene dalla
// stessa funzione della scheda che lo mostra per esteso (fasi e passaggi del motore, valutazioni,
// approvazioni, DeliveryOf del lavoro). Tutto è calcolato dalla configurazione: le regole che
// scattano entrando in una fase sono quelle della versione di processo in uso. Un blocco che la
// persona non può leggere manca (null/vuoto) invece di far fallire tutto. I testi sono del client:
// qui solo codici (kind) e nomi che sono dati del tenant.

func (s *Server) percorsoRichiestaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/elements/{ref}/percorso", inTenant(s.percorso))
}

type pcPhase struct {
	Name     string            `json:"name"`
	Labels   map[string]string `json:"labels,omitempty"`
	Category string            `json:"category"`
	OffPath  bool              `json:"off_path,omitempty"`
	// Status: done | current | next
	Status string `json:"status"`
}

type pcReview struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Team      string `json:"team"`
	Stage     string `json:"stage"`
	Mandatory bool   `json:"mandatory"`
	Open      bool   `json:"open"`
	Impact    string `json:"impact,omitempty"`
	Mine      bool   `json:"mine,omitempty"`
}

type pcApproval struct {
	Stage     string   `json:"stage"`
	Status    string   `json:"status"`
	Waiting   []string `json:"waiting"`
	StepsDone int      `json:"steps_done"`
	StepsAll  int      `json:"steps_all"`
	CanDecide bool     `json:"can_decide"`
}

type pcImpact struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Application   string `json:"application"`
	Team          string `json:"team"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
	Current       bool   `json:"current,omitempty"`
}

// pcNext è un passaggio possibile dalla fase attuale e ciò che lo sblocca.
type pcNext struct {
	To      string   `json:"to"`
	Label   string   `json:"label"`
	Forward bool     `json:"forward"`
	Roles   []string `json:"roles,omitempty"`
	// Mine: l'attore può eseguirlo (con i gate superati o no).
	Mine bool `json:"mine"`
	// Blocked: i motivi per cui oggi non si può (gate, campi, approvazione).
	Blocked []string `json:"blocked"`
	// Waiting: cosa si aspetta (valutazioni obbligatorie aperte, approvazioni in sospeso).
	Waiting []pcWaiting `json:"waiting"`
}

type pcWaiting struct {
	Kind  string `json:"kind"` // reviews | approval
	Count int    `json:"count,omitempty"`
	Who   string `json:"who,omitempty"`
}

// pcTrigger è una regola che scatta entrando in una fase.
type pcTrigger struct {
	Phase    string `json:"phase"`
	Rule     string `json:"rule"`
	Kind     string `json:"kind"`     // open_review | assign_role | create_epic | …
	Ref      string `json:"ref"`      // chiave dell'oggetto configurato (tipo di valutazione)
	RefName  string `json:"name"`     // nome leggibile dell'oggetto, se noto
	Role     string `json:"role"`     // per assign_role
	When     string `json:"when"`     // condizione della regola
	WhenMet  *bool  `json:"when_met"` // esito sui dati attuali (null = non valutabile qui)
	ActionIf string `json:"action_when"`
}

// pcTodo è una cosa da fare (o da aspettare) per chi guarda.
type pcTodo struct {
	// Kind: decide | compile_review | exam_review | advance | unblock | create_epic |
	//       wait_approval | wait_review | wait_team | wait_work | nothing
	Kind   string `json:"kind"`
	Mine   bool   `json:"mine"`
	Key    string `json:"key,omitempty"`    // la valutazione o l'impatto
	Detail string `json:"detail,omitempty"` // nome della fase, del tipo, della fase di approvazione
	Who    string `json:"who,omitempty"`
	Tab    string `json:"tab,omitempty"` // scheda della richiesta dove si agisce
	Count  int    `json:"count,omitempty"`
	// Link: pagina dove si agisce, se non è una scheda ("/valutazioni/RV-3").
	Link string `json:"link,omitempty"`
}

type pcDelivery struct {
	Epics    []work.EpicView `json:"epics"`
	Progress *work.Progress  `json:"progress,omitempty"`
}

type percorsoOut struct {
	Key          string       `json:"key"`
	Type         string       `json:"type"` // business_request | impact
	Title        string       `json:"title"`
	RequestKey   string       `json:"request_key"`
	RequestTitle string       `json:"request_title,omitempty"`
	Current      string       `json:"current"`
	Closed       bool         `json:"closed"`
	Phases       []pcPhase    `json:"phases"`
	Process      string       `json:"process,omitempty"`
	Reviews      []pcReview   `json:"reviews"`
	Approvals    []pcApproval `json:"approvals"`
	Impacts      []pcImpact   `json:"impacts"`
	Delivery     *pcDelivery  `json:"delivery"`
	Next         []pcNext     `json:"next"`
	Triggers     []pcTrigger  `json:"triggers"`
	Todo         []pcTodo     `json:"todo"`
	// Guide: i riferimenti allegati al processo (phase null) e alla fase attuale, per chi lavora sulla richiesta.
	Guides []engine.Guide `json:"guides"`
	// VersionNotice: la richiesta è su una versione del processo sostituita da una più nuova (null = in vigore).
	VersionNotice *engine.VersionNotice `json:"version_notice"`
}

func (s *Server) percorso(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*percorsoOut, error) {
		return s.buildPercorso(r.Context(), tx, p, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) buildPercorso(ctx context.Context, tx pgx.Tx, p *auth.Principal, actor engine.Actor, ref string) (*percorsoOut, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != "business_request" && el.TypeKey != "impact" {
		return nil, engine.InvalidCode("validation.percorso.of_request", "ref", "il percorso è di una richiesta o di un impatto")
	}
	off, err := s.modulesOff(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	out := &percorsoOut{Key: el.Key, Type: el.TypeKey, Title: el.Title, Current: el.State, RequestKey: el.Key,
		Closed: el.StateCategory == "done" || el.StateCategory == "cancelled",
		Phases: []pcPhase{}, Reviews: []pcReview{}, Approvals: []pcApproval{}, Impacts: []pcImpact{},
		Next: []pcNext{}, Triggers: []pcTrigger{}, Todo: []pcTodo{}, Guides: []engine.Guide{}}
	if el.TypeKey == "impact" && el.ParentID != nil {
		if req, err := s.eng.Get(ctx, tx, engine.Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}, *el.ParentID); err == nil {
			out.RequestKey, out.RequestTitle = req.Key, req.Title
		}
	}

	// Fasi: la linea del processo.
	states, proc, err := s.eng.Phases(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	out.Phases = phaseLine(states, el.State)
	if proc != nil {
		out.Process = proc.Name
	}

	// Guide della fase e del processo; avviso se la richiesta è su una versione deprecata.
	if proc != nil {
		if g, err := s.eng.GuidesFor(ctx, tx, proc.ID, el.State); err == nil {
			out.Guides = g
		}
		if !out.Closed {
			if n, err := s.eng.VersionNoticeOf(ctx, tx, actor, el); err == nil {
				out.VersionNotice = n
			}
		}
	}

	// Valutazioni (pareri).
	var inbox map[string]string // chiave valutazione → compile | team, per chi guarda
	if !off["demand"] {
		inbox = map[string]string{}
		if er, err := s.reviews.ForElement(ctx, tx, actor, el.ID); err != nil {
			if _, err2 := soft(err); err2 != nil {
				return nil, err2
			}
		} else {
			if ib, err := s.reviews.Inbox(ctx, tx, actor); err == nil {
				for _, it := range ib.Compile {
					inbox[it.Key] = "compile"
				}
				for _, it := range ib.Team {
					if _, ok := inbox[it.Key]; !ok {
						inbox[it.Key] = "team"
					}
				}
			}
			for _, it := range er.Items {
				if it.Superseded {
					continue
				}
				open := !(it.StateCategory == "done" || it.StateCategory == "cancelled" || it.Stage == "concluded" || it.Stage == "cancelled")
				rv := pcReview{Key: it.Key, Title: it.Title, Type: it.TypeName, Team: it.Team, Stage: it.Stage, Mandatory: it.Mandatory, Open: open}
				if it.Application != nil {
					rv.Impact = *it.Application
				}
				_, rv.Mine = inbox[it.Key]
				out.Reviews = append(out.Reviews, rv)
			}
		}
	}

	// Approvazioni.
	if apr, err := s.approvals.ForElement(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else {
		for _, ar := range apr {
			if ar.Status != "pending" {
				continue
			}
			a := pcApproval{Stage: ar.Stage, Status: ar.Status, Waiting: []string{}, StepsAll: len(ar.Steps)}
			for _, st := range ar.Steps {
				if st.Decision != "pending" && st.Decision != "" {
					a.StepsDone++
					continue
				}
				a.Waiting = append(a.Waiting, st.Approver)
				if st.CanDecide {
					a.CanDecide = true
				}
			}
			out.Approvals = append(out.Approvals, a)
		}
	}

	// Impatti per team.
	impactSQL := `select e.id::text, s.key || '-' || e.number, e.title, coalesce(ap.title, ''), coalesce(t.name, ''), coalesce(o.name, ''), coalesce(o.category, '')
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id`
	if el.TypeKey == "business_request" {
		impactSQL += ` where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled' order by e.number`
	} else {
		impactSQL += ` where x.element_id = $1`
	}
	if rows, err := tx.Query(ctx, impactSQL, el.ID); err == nil {
		if list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pcImpact, error) {
			var id string
			var v pcImpact
			err := r.Scan(&id, &v.Key, &v.Title, &v.Application, &v.Team, &v.State, &v.StateCategory)
			v.Current = id == el.ID
			return v, err
		}); err == nil {
			out.Impacts = list
		}
	}

	// Lavoro: richiesta → impatti → epic → story (la stessa funzione della scheda «Piano»).
	if dv, err := s.work.DeliveryOf(ctx, tx, actor, el.ID); err == nil && dv != nil {
		if len(dv.Epics) > 0 || dv.CanCreateEpic {
			out.Delivery = &pcDelivery{Epics: dv.Epics, Progress: dv.Progress}
		}
		if dv.CanCreateEpic {
			out.Todo = append(out.Todo, pcTodo{Kind: "create_epic", Mine: true, Key: el.Key, Tab: "piano"})
		}
	}

	// Cosa succede dopo: i passaggi possibili, chi li fa, cosa li blocca; le regole dell'ingresso.
	avail, err := s.eng.Available(ctx, tx, actor, el)
	if err != nil {
		return nil, err
	}
	def := s.loadPcWorkflow(ctx, tx, el)
	cur, _ := stateByName(states, el.State)
	facts, _ := s.eng.ElementFacts(ctx, tx, el)
	if !out.Closed {
		out.Next = nextSteps(def, states, cur, avail, out.Reviews, out.Approvals)
		out.Triggers = triggersFor(def, out.Next, facts)
		s.nameTriggers(ctx, tx, out.Triggers)
	}
	for _, t := range buildTodo(el.Key, el.TypeKey, out) {
		if t.Kind == "nothing" && len(out.Todo) > 0 {
			continue
		}
		out.Todo = append(out.Todo, t)
	}
	return out, nil
}

// phaseLine segna le fasi del percorso: fatte, attuale, prossime. Le fasi fuori percorso (sospensioni,
// esiti negativi) restano nell'elenco ma sono marcate off_path; il client le mostra solo se sono l'attuale.
func phaseLine(states []engine.State, current string) []pcPhase {
	out := []pcPhase{}
	idx := -1
	for i, st := range states {
		if strings.EqualFold(st.Name, current) {
			idx = i
		}
	}
	// Una fase fuori percorso come attuale: il resto del percorso non è «fatto» né «prossimo» con certezza.
	curOff := idx >= 0 && states[idx].OffPath
	for i, st := range states {
		ph := pcPhase{Name: st.Name, Labels: st.Labels, Category: st.Category, OffPath: st.OffPath, Status: "next"}
		switch {
		case i == idx:
			ph.Status = "current"
		case idx >= 0 && i < idx && !curOff:
			ph.Status = "done"
		}
		out = append(out, ph)
	}
	return out
}

func stateByName(states []engine.State, name string) (engine.State, bool) {
	for _, st := range states {
		if strings.EqualFold(st.Name, name) {
			return st, true
		}
	}
	return engine.State{}, false
}

// loadPcWorkflow legge la versione di processo in uso dall'elemento (stessa riga che usa il motore).
func (s *Server) loadPcWorkflow(ctx context.Context, tx pgx.Tx, el *engine.Element) *engine.Workflow {
	if el.ProcessVersionID == nil {
		return nil
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `select definition from process_versions where id = $1`, *el.ProcessVersionID).Scan(&raw); err != nil {
		return nil
	}
	w := &engine.Workflow{}
	if json.Unmarshal(raw, w) != nil {
		return nil
	}
	return w
}

// nextSteps elenca i passaggi che partono dalla fase attuale: quelli in avanti per primi. Per ognuno,
// i motivi che oggi lo bloccano (dal motore) e cosa si aspetta: valutazioni obbligatorie aperte e
// approvazioni in sospeso. Se il processo non è leggibile, restano i passaggi disponibili per l'attore.
func nextSteps(def *engine.Workflow, states []engine.State, cur engine.State, avail []engine.TransitionView, reviews []pcReview, approvals []pcApproval) []pcNext {
	out := []pcNext{}
	seen := map[string]bool{}
	mand, teams := 0, []string{}
	for _, rv := range reviews {
		if rv.Open && rv.Mandatory {
			mand++
			if rv.Team != "" && !slices.Contains(teams, rv.Team) {
				teams = append(teams, rv.Team)
			}
		}
	}
	add := func(to, label string, forward bool, roles []string, mine bool, blocked []string) {
		key := strings.ToLower(to)
		if seen[key] {
			return
		}
		seen[key] = true
		n := pcNext{To: to, Label: label, Forward: forward, Roles: roles, Mine: mine, Blocked: []string{}, Waiting: []pcWaiting{}}
		if blocked != nil {
			n.Blocked = blocked
		}
		if forward {
			if mand > 0 {
				n.Waiting = append(n.Waiting, pcWaiting{Kind: "reviews", Count: mand, Who: strings.Join(teams, ", ")})
			}
			for _, a := range approvals {
				n.Waiting = append(n.Waiting, pcWaiting{Kind: "approval", Who: strings.Join(a.Waiting, ", "), Count: len(a.Waiting)})
			}
		}
		out = append(out, n)
	}
	mine := map[string]pcNext{}
	for _, a := range avail {
		mine[strings.ToLower(a.To)] = pcNext{Forward: a.Forward, Blocked: a.Blocked, Label: a.Label}
	}
	if def != nil {
		for _, tr := range def.Transitions {
			if tr.System || !startsFromState(tr, cur) {
				continue
			}
			m, isMine := mine[strings.ToLower(tr.To)]
			fwd := forwardTo(states, cur, tr.To)
			label := tr.Label
			if label == "" {
				label = m.Label
			}
			add(tr.To, label, fwd, tr.Roles, isMine, m.Blocked)
		}
	}
	for _, a := range avail {
		add(a.To, a.Label, a.Forward, nil, true, a.Blocked)
	}
	slices.SortStableFunc(out, func(a, b pcNext) int {
		switch {
		case a.Forward && !b.Forward:
			return -1
		case !a.Forward && b.Forward:
			return 1
		}
		return 0
	})
	return out
}

// startsFromState replica la regola del motore: da una fase indicata, o da «*» se non è terminale.
func startsFromState(t engine.Transition, from engine.State) bool {
	terminal := from.Category == "done" || from.Category == "cancelled"
	if strings.EqualFold(t.To, from.Name) {
		return false
	}
	for _, f := range t.From {
		if strings.EqualFold(f, from.Name) {
			return true
		}
	}
	return slices.Contains(t.From, "*") && !terminal
}

func forwardTo(states []engine.State, from engine.State, to string) bool {
	fi, ti := -1, -1
	for i, st := range states {
		if st.ID == from.ID {
			fi = i
		}
		if strings.EqualFold(st.Name, to) {
			ti = i
			if st.OffPath {
				return false
			}
		}
	}
	return fi >= 0 && ti > fi
}

// triggersFor: le regole che scattano entrando nelle fasi di arrivo dei passaggi in avanti. Una
// condizione della regola sui fatti della richiesta si valuta qui; quella propria dell'azione (sui fatti
// di ciascun impatto) resta da leggere.
func triggersFor(def *engine.Workflow, next []pcNext, facts engine.Facts) []pcTrigger {
	out := []pcTrigger{}
	if def == nil {
		return out
	}
	targets := map[string]string{}
	for _, n := range next {
		if n.Forward {
			targets[strings.ToLower(n.To)] = n.To
		}
	}
	for _, r := range def.Rules {
		phase, ok := strings.CutPrefix(r.On, "enter:")
		if !ok {
			continue
		}
		name, ok := targets[strings.ToLower(strings.TrimSpace(phase))]
		if !ok {
			continue
		}
		var met *bool
		if r.When != "" && facts != nil {
			if c, err := engine.ParseCondition(r.When); err == nil {
				v := c.Eval(facts)
				met = &v
			}
		}
		for _, a := range r.Actions {
			out = append(out, pcTrigger{Phase: name, Rule: r.Name, Kind: a.Kind, Ref: a.Ref, Role: a.Role, When: r.When, WhenMet: met, ActionIf: a.When})
		}
	}
	return out
}

// nameTriggers dà il nome leggibile ai tipi di valutazione citati dalle regole.
func (s *Server) nameTriggers(ctx context.Context, tx pgx.Tx, ts []pcTrigger) {
	for i := range ts {
		if ts[i].Kind != "open_review" || ts[i].Ref == "" {
			continue
		}
		var name string
		if err := tx.QueryRow(ctx, `select name from review_types where key = $1`, ts[i].Ref).Scan(&name); err == nil {
			ts[i].RefName = name
		}
	}
}

// buildTodo: cosa tocca a chi guarda, e se non tocca a lui, chi si aspetta. Prima ciò che è suo.
func buildTodo(key, typ string, o *percorsoOut) []pcTodo {
	out := []pcTodo{}
	for _, a := range o.Approvals {
		if a.CanDecide {
			out = append(out, pcTodo{Kind: "decide", Mine: true, Key: key, Detail: a.Stage, Tab: "approvazioni"})
		}
	}
	for _, rv := range o.Reviews {
		if !rv.Open || !rv.Mine {
			continue
		}
		kind := "exam_review"
		if rv.Stage == "compile" || rv.Stage == "clarification" {
			kind = "compile_review"
		}
		out = append(out, pcTodo{Kind: kind, Mine: true, Key: rv.Key, Detail: rv.Type, Who: rv.Team, Link: "/valutazioni/" + rv.Key})
	}
	for _, n := range o.Next {
		if !n.Forward || !n.Mine {
			continue
		}
		if len(n.Blocked) == 0 {
			out = append(out, pcTodo{Kind: "advance", Mine: true, Key: key, Detail: n.To, Tab: "attivita"})
		} else {
			out = append(out, pcTodo{Kind: "unblock", Mine: true, Key: key, Detail: n.To, Tab: "attivita"})
		}
		break
	}
	// Ciò che si aspetta (non è suo): approvazioni, valutazioni, team.
	for _, a := range o.Approvals {
		if !a.CanDecide {
			out = append(out, pcTodo{Kind: "wait_approval", Key: key, Detail: a.Stage, Who: strings.Join(a.Waiting, ", "), Tab: "approvazioni"})
		}
	}
	open, teams := 0, []string{}
	for _, rv := range o.Reviews {
		if rv.Open && !rv.Mine {
			open++
			if rv.Team != "" && !slices.Contains(teams, rv.Team) {
				teams = append(teams, rv.Team)
			}
		}
	}
	if open > 0 {
		out = append(out, pcTodo{Kind: "wait_review", Key: key, Count: open, Who: strings.Join(teams, ", "), Tab: "valutazioni"})
	}
	if typ == "business_request" {
		pend, tm := 0, []string{}
		for _, im := range o.Impacts {
			if im.StateCategory != "done" && im.StateCategory != "cancelled" {
				pend++
				if im.Team != "" && !slices.Contains(tm, im.Team) {
					tm = append(tm, im.Team)
				}
			}
		}
		if pend > 0 {
			out = append(out, pcTodo{Kind: "wait_team", Key: key, Count: pend, Who: strings.Join(tm, ", "), Tab: "piano"})
		}
	}
	if o.Delivery != nil && o.Delivery.Progress != nil && o.Delivery.Progress.Total > 0 && o.Delivery.Progress.Done < o.Delivery.Progress.Total {
		out = append(out, pcTodo{Kind: "wait_work", Key: key, Count: o.Delivery.Progress.Total - o.Delivery.Progress.Done, Tab: "piano"})
	}
	if len(out) == 0 {
		out = append(out, pcTodo{Kind: "nothing", Key: key})
	}
	return out
}
