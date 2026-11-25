// Package approval è il motore di approvazione: politiche per tipo e fase con soglie,
// passi sequenziali o paralleli, approvatori per persona, ruolo sull'elemento o organo
// di governance, deleghe a tempo. L'esito fa avanzare lo stato dell'elemento tramite
// il motore (transizioni "system" del workflow).
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Policy è il contenuto di approval_policies.rules.
type Policy struct {
	// Outcomes: stato di arrivo per ogni esito.
	Outcomes struct {
		Approve string `json:"approve"`
		Reject  string `json:"reject"`
		Defer   string `json:"defer"`
	} `json:"outcomes"`
	// Rules: vince la prima la cui condizione è vera.
	Rules []Rule `json:"rules"`
}

type Rule struct {
	When  When   `json:"when"`
	Steps []Step `json:"steps"`
}

// When: condizioni sui fatti dell'elemento. Importi in centesimi.
type When struct {
	AmountLTE *int64 `json:"amount_lte,omitempty"`
	AmountGT  *int64 `json:"amount_gt,omitempty"`
	Driver    string `json:"driver,omitempty"`
	// RiskLTE e RiskGT: soglie sul punteggio di rischio (deroghe ai requisiti, passo 6).
	RiskLTE *int `json:"risk_lte,omitempty"`
	RiskGT  *int `json:"risk_gt,omitempty"`
}

// Step: gli approvatori di un passo decidono in parallelo; i passi vanno in sequenza.
type Step struct {
	Approvers []Approver `json:"approvers"`
}

type Approver struct {
	Role   string `json:"role,omitempty"`   // ruolo stakeholder sull'elemento
	Body   string `json:"body,omitempty"`   // tipo di organo di governance (es. investment_committee)
	Member string `json:"member,omitempty"` // membro preciso
}

// Facts sono i dati dell'elemento usati dalle condizioni; li fornisce il modulo di dominio.
type Facts struct {
	AmountCents *int64
	Driver      string
	// Risk: punteggio del rischio residuo (deroghe), per le soglie risk_lte e risk_gt.
	Risk *int
}

type FactsFunc func(ctx context.Context, tx pgx.Tx, el *engine.Element) (Facts, error)

type Service struct {
	eng   *engine.Service
	facts map[string]FactsFunc // per modulo del tipo, es. "strategy"
	// closers: reazioni alla chiusura delle richieste con un soggetto diverso dallo
	// stage-gate (budget, trasferimenti), al posto del cambio di stato dell'elemento.
	closers map[string]CloseFunc
	// separate: soggetti in cui chi ha chiesto l'approvazione non può deciderla
	// (separazione dei compiti, es. le deroghe ai requisiti).
	separate map[string]bool
}

// CloseFunc reagisce all'esito (approved, approved_with_conditions, rejected, deferred)
// di una richiesta di approvazione, nella stessa transazione della decisione.
type CloseFunc func(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error

// Register collega il motore di approvazione al motore degli elementi.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng, facts: map[string]FactsFunc{}, closers: map[string]CloseFunc{}, separate: map[string]bool{}}
	eng.OnTransition(s.onTransition)
	return s
}

// Facts registra il fornitore di fatti per un tipo di elemento.
func (s *Service) Facts(typeKey string, fn FactsFunc) { s.facts[typeKey] = fn }

// OnClose registra la reazione alla chiusura delle richieste di un soggetto (es. "budget").
func (s *Service) OnClose(subject string, fn CloseFunc) { s.closers[subject] = fn }

// SeparateRequester impone la separazione dei compiti per un soggetto: chi ha chiesto
// l'approvazione non può decidere nessun passo, nemmeno per delega o tramite il gruppo.
func (s *Service) SeparateRequester(subject string) { s.separate[subject] = true }

func (s *Service) separatedSubjects() []string {
	out := make([]string, 0, len(s.separate))
	for k := range s.separate {
		out = append(out, k)
	}
	return out
}

var (
	ErrNotPending  = errors.New("la decisione è già stata presa")
	ErrNotYourTurn = errors.New("questo passo non è ancora attivo: si attende il passo precedente")
	// ErrSelfApproval: separazione dei compiti, chi ha chiesto non decide.
	ErrSelfApproval = fmt.Errorf("%w: chi ha chiesto l'approvazione non può deciderla (separazione dei compiti)", engine.ErrForbidden)
)

func (s *Service) onTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	if ev.Transition.Approval == "" {
		return nil
	}
	return s.open(ctx, tx, ev)
}

// policyFor trova la politica attiva per la fase (o la chiave) e il tipo dell'elemento; nil se non c'è.
func policyFor(ctx context.Context, tx pgx.Tx, stage, typeID string) (string, *Policy, error) {
	var policyID string
	var raw []byte
	err := tx.QueryRow(ctx, `select id, rules from approval_policies
		where active and stage = $1 and (type_id = $2 or type_id is null)
		order by type_id nulls last limit 1`, stage, typeID).Scan(&policyID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", nil, err
	}
	return policyID, &p, nil
}

func (s *Service) open(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	el := ev.Element
	policyID, p, err := policyFor(ctx, tx, ev.To, el.TypeID)
	if err != nil {
		return err
	}
	if p == nil {
		return engine.InvalidParams("validation.approval.no_policy_for_stage", "approval", map[string]any{"stage": ev.To}, "nessuna politica di approvazione per la fase %q", ev.To)
	}
	facts := Facts{}
	if fn, ok := s.facts[el.TypeKey]; ok {
		if facts, err = fn(ctx, tx, el); err != nil {
			return err
		}
	}
	rule, ok := match(p.Rules, facts)
	if !ok {
		return engine.InvalidCode("validation.approval.no_matching_rule", "approval", "nessuna regola della politica si applica a questa richiesta")
	}
	_, err = s.create(ctx, tx, ev.Actor, el, policyID, ev.To, ev.Transition.Approval, facts.AmountCents, rule)
	return err
}

// OpenSubject apre una richiesta di approvazione che non nasce da un cambio di stato
// (budget, trasferimenti). La politica è quella attiva con stage = policyStage; la regola
// si sceglie sull'importo indicato (e sul driver della domanda). Restituisce "" se non
// serve approvazione: nessuna politica attiva, oppure la regola scelta non ha passi.
// L'esito arriva alla reazione registrata con OnClose per il soggetto.
func (s *Service) OpenSubject(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element,
	subject, policyStage, label string, amountCents int64) (string, error) {
	policyID, p, err := policyFor(ctx, tx, policyStage, el.TypeID)
	if err != nil || p == nil {
		return "", err
	}
	facts := Facts{}
	if fn, ok := s.facts[el.TypeKey]; ok {
		if facts, err = fn(ctx, tx, el); err != nil {
			return "", err
		}
	}
	facts.AmountCents = &amountCents
	rule, ok := match(p.Rules, facts)
	if !ok {
		return "", engine.InvalidParams("validation.approval.no_rule_for_amount", "approval", map[string]any{"policy": policyStage}, "nessuna regola della politica %q si applica a questo importo", policyStage)
	}
	if len(rule.Steps) == 0 {
		return "", nil
	}
	return s.create(ctx, tx, actor, el, policyID, label, subject, &amountCents, rule)
}

// OpenSubjectRule apre una richiesta con i passi indicati dal chiamante, senza politica:
// per i casi in cui l'approvatore dipende dai dati (es. il responsabile del nodo comune a due
// stanziamenti). Una regola senza passi è un errore, perché qualcuno deve decidere.
func (s *Service) OpenSubjectRule(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element,
	subject, label string, amountCents int64, rule Rule) (string, error) {
	if len(rule.Steps) == 0 {
		return "", engine.InvalidParams("validation.approval.no_approver", "approval", map[string]any{"label": label}, "nessun approvatore per %q", label)
	}
	return s.create(ctx, tx, actor, el, "", label, subject, &amountCents, rule)
}

// OpenSubjectWith è come OpenSubject, ma la regola si sceglie sui fatti indicati (per
// esempio il punteggio di rischio di una deroga) invece che su un importo. Restituisce ""
// senza politica attiva; una regola senza passi è un errore, perché qualcuno deve decidere.
func (s *Service) OpenSubjectWith(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element,
	subject, policyStage, label string, f Facts) (string, error) {
	policyID, p, err := policyFor(ctx, tx, policyStage, el.TypeID)
	if err != nil || p == nil {
		return "", err
	}
	rule, ok := match(p.Rules, f)
	if !ok || len(rule.Steps) == 0 {
		return "", engine.InvalidParams("validation.approval.no_approvers_for_case", "approval", map[string]any{"policy": policyStage}, "la politica %q non prevede approvatori per questo caso", policyStage)
	}
	return s.create(ctx, tx, actor, el, policyID, label, subject, f.AmountCents, rule)
}

// create scrive la richiesta e i suoi passi, risolve gli assegnatari e avvisa i primi.
func (s *Service) create(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, policyID, stage, subject string,
	amount *int64, rule Rule) (string, error) {
	reqID := db.NewID()
	if _, err := tx.Exec(ctx, `insert into approval_requests (tenant_id, id, element_id, policy_id, stage, subject, amount_cents, requested_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`,
		reqID, el.ID, nullable(policyID), stage, subject, amount, nullable(actor.MemberID)); err != nil {
		return "", err
	}
	for i, st := range rule.Steps {
		for _, a := range st.Approvers {
			var bodyID *string
			if a.Body != "" {
				var id string
				if err := tx.QueryRow(ctx, "select id from governance_bodies where kind = $1 and not archived limit 1", a.Body).Scan(&id); err != nil {
					return "", engine.InvalidParams("validation.approval.body_not_configured", "approval", map[string]any{"body": a.Body}, "organo di governance %q non configurato", a.Body)
				}
				bodyID = &id
			}
			if a.Role != "" {
				if n, err := holders(ctx, tx, el.ID, a.Role); err != nil {
					return "", err
				} else if n == 0 {
					return "", engine.Required("approval", "manca il ruolo %q sulla richiesta: assegnalo prima di chiedere l'approvazione", a.Role)
				}
			}
			if _, err := tx.Exec(ctx, `insert into approval_steps (tenant_id, id, request_id, ordinal, parallel_group,
				approver_member_id, approver_body_id, approver_role_key) values (app_tenant(), $1, $2, $3, $3, $4, $5, $6)`,
				db.NewID(), reqID, i+1, nullable(a.Member), bodyID, nullable(a.Role)); err != nil {
				return "", err
			}
		}
	}
	if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1)", reqID); err != nil {
		return "", err
	}
	if err := notify.ApprovalActive(ctx, tx, actor.MemberID, reqID, 0); err != nil {
		return "", err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "approval", "requested", map[string]any{"request_id": reqID, "stage": stage, "steps": len(rule.Steps)}, nil); err != nil {
		return "", err
	}
	payload := map[string]any{"element_id": el.ID, "stage": stage, "amount_cents": amount}
	if subject != "stage_gate" {
		payload["subject"] = subject
	}
	return reqID, audit.Append(ctx, tx, audit.Event{Action: "approval.open", EntityType: "approval_request", EntityID: reqID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

func match(rules []Rule, f Facts) (Rule, bool) {
	for _, r := range rules {
		w := r.When
		if w.AmountLTE != nil && (f.AmountCents == nil || *f.AmountCents > *w.AmountLTE) {
			continue
		}
		if w.AmountGT != nil && (f.AmountCents == nil || *f.AmountCents <= *w.AmountGT) {
			continue
		}
		if w.Driver != "" && w.Driver != f.Driver {
			continue
		}
		if w.RiskLTE != nil && (f.Risk == nil || *f.Risk > *w.RiskLTE) {
			continue
		}
		if w.RiskGT != nil && (f.Risk == nil || *f.Risk <= *w.RiskGT) {
			continue
		}
		return r, true
	}
	return Rule{}, false
}

func holders(ctx context.Context, tx pgx.Tx, elementID, role string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `select count(*) from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		where sh.element_id = $1 and r.key = $2`, elementID, role).Scan(&n)
	return n, err
}

// candidatesSQL: membri che possono decidere un passo ($1 = id del passo), prima delle deleghe.
const candidatesSQL = `
	select st.approver_member_id from approval_steps st where st.id = $1 and st.approver_member_id is not null
	union
	select coalesce(sh.member_id, gm.member_id)
	from approval_steps st
	join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id
	join stakeholder_roles r on r.tenant_id = st.tenant_id and r.key = st.approver_role_key
	join stakeholders sh on sh.tenant_id = st.tenant_id and sh.element_id = rq.element_id and sh.role_id = r.id
	left join lateral group_effective_members(sh.group_id) gm(member_id) on true
	where st.id = $1
	union
	select bm.member_id from approval_steps st
	cross join lateral body_effective_members(st.approver_body_id) bm
	where st.id = $1 and bm.voting
	union
	select pm.member_id from approval_step_members pm where pm.step_id = $1`

// mayDecide: l'attore è tra i candidati, oppure ha una delega valida da uno di loro.
func mayDecide(ctx context.Context, tx pgx.Tx, stepID, memberID string) (bool, *string, error) {
	var direct bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from (`+candidatesSQL+`) c(member_id) where c.member_id = $2)`, stepID, memberID).Scan(&direct); err != nil {
		return false, nil, err
	}
	if direct {
		return true, nil, nil
	}
	var from string
	err := tx.QueryRow(ctx, `select d.from_member_id from delegations d
		where d.to_member_id = $2 and d.scope in ('approvals', 'all') and now() between d.valid_from and d.valid_to
		  and d.from_member_id in (`+candidatesSQL+`) limit 1`, stepID, memberID).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, &from, nil
}

// DecideInput è la decisione su un passo.
type DecideInput struct {
	Decision   string // approved | approved_with_conditions | rejected | deferred
	Comment    string
	Conditions string
	// MeetingID: decisione presa in seduta dall'organo a cui è assegnato il passo. La
	// registra il presidente o il segretario dell'organo a nome del collegio (anche se
	// non votante); il passo ricorda la seduta.
	MeetingID string
}

// meetingAuthority: il passo è assegnato all'organo della seduta e l'attore ne è
// presidente o segretario.
func meetingAuthority(ctx context.Context, tx pgx.Tx, stepID, meetingID, memberID string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from approval_steps st
		join meetings m on m.tenant_id = st.tenant_id and m.id = $2 and m.body_id = st.approver_body_id
		join governance_bodies b on b.tenant_id = m.tenant_id and b.id = m.body_id
		where st.id = $1 and $3::uuid in (b.chair_id, b.secretary_id))`, stepID, meetingID, memberID).Scan(&ok)
	return ok, err
}

// Decide registra la decisione e, se il passo chiude la richiesta, fa avanzare lo stato.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, actor engine.Actor, stepID string, in DecideInput) error {
	in.Comment, in.Conditions = strings.TrimSpace(in.Comment), strings.TrimSpace(in.Conditions)
	switch in.Decision {
	case "approved":
	case "approved_with_conditions":
		if in.Conditions == "" {
			return engine.Required("conditions", "indica le condizioni dell'approvazione")
		}
	case "rejected", "deferred":
		if in.Comment == "" {
			return engine.InvalidCode("validation.reason_required", "comment", "serve una motivazione")
		}
	default:
		return engine.InvalidValue("decision", "decisione non valida")
	}
	var reqID, elementID, reqStatus, stepDecision, policyID, subject, requestedBy string
	var ordinal int
	err := tx.QueryRow(ctx, `select st.request_id, coalesce(rq.element_id::text, ''), rq.status, st.decision, st.ordinal, coalesce(rq.policy_id::text, ''), rq.subject,
		coalesce(rq.requested_by::text, '')
		from approval_steps st join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id
		where st.id = $1 for update of st, rq`, stepID).Scan(&reqID, &elementID, &reqStatus, &stepDecision, &ordinal, &policyID, &subject, &requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if reqStatus != "pending" || stepDecision != "pending" {
		return ErrNotPending
	}
	var active int
	if err := tx.QueryRow(ctx, `select min(ordinal) from approval_steps where request_id = $1 and decision = 'pending'`, reqID).Scan(&active); err != nil {
		return err
	}
	if ordinal != active {
		return ErrNotYourTurn
	}
	var ok bool
	var onBehalfOf *string
	if in.MeetingID != "" {
		ok, err = meetingAuthority(ctx, tx, stepID, in.MeetingID, actor.MemberID)
	} else {
		ok, onBehalfOf, err = mayDecide(ctx, tx, stepID, actor.MemberID)
	}
	if err != nil {
		return err
	}
	if !ok {
		return engine.ErrForbidden
	}
	if s.separate[subject] && requestedBy != "" && requestedBy == actor.MemberID {
		return ErrSelfApproval
	}
	if _, err := tx.Exec(ctx, `update approval_steps set decision = $2, comment = $3, conditions = $4, decided_by = $5, decided_at = now(), meeting_id = $6
		where id = $1`, stepID, in.Decision, nullable(in.Comment), nullable(in.Conditions), actor.MemberID, nullable(in.MeetingID)); err != nil {
		return err
	}
	payload := map[string]any{"request_id": reqID, "step_id": stepID, "decision": in.Decision}
	if onBehalfOf != nil {
		payload["on_behalf_of"] = *onBehalfOf
	}
	if in.MeetingID != "" {
		payload["meeting_id"] = in.MeetingID
	}
	if elementID != "" {
		if err := engine.AddActivity(ctx, tx, elementID, actor.MemberID, "approval", "decision", map[string]any{"decision": in.Decision, "comment": in.Comment, "conditions": in.Conditions}, nil); err != nil {
			return err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "approval.decision", EntityType: "approval_step", EntityID: stepID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload}); err != nil {
		return err
	}

	// Esito della richiesta.
	outcome := ""
	switch in.Decision {
	case "rejected", "deferred":
		outcome = in.Decision
		if _, err := tx.Exec(ctx, `update approval_steps set decision = 'skipped' where request_id = $1 and decision = 'pending'`, reqID); err != nil {
			return err
		}
	default:
		var pending int
		var withConditions bool
		if err := tx.QueryRow(ctx, `select count(*) filter (where decision = 'pending'), bool_or(decision = 'approved_with_conditions')
			from approval_steps where request_id = $1`, reqID).Scan(&pending, &withConditions); err != nil {
			return err
		}
		if pending > 0 {
			// Se il gruppo di passi corrente è concluso si attiva il successivo, e i suoi
			// assegnatari ricevono la notifica.
			if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1)", reqID); err != nil {
				return err
			}
			return notify.ApprovalActive(ctx, tx, actor.MemberID, reqID, active)
		}
		outcome = "approved"
		if withConditions {
			outcome = "approved_with_conditions"
		}
	}
	if _, err := tx.Exec(ctx, `update approval_requests set status = $2, closed_at = now() where id = $1`, reqID, outcome); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1)", reqID); err != nil {
		return err
	}
	// Budget e trasferimenti: l'esito lo applica il modulo, lo stato dell'elemento non cambia.
	if fn, ok := s.closers[subject]; ok {
		return fn(ctx, tx, actor, reqID, outcome, in.Comment)
	}
	var p Policy
	if policyID != "" {
		var raw []byte
		if err := tx.QueryRow(ctx, "select rules from approval_policies where id = $1", policyID).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
	}
	to := map[string]string{"approved": p.Outcomes.Approve, "approved_with_conditions": p.Outcomes.Approve,
		"rejected": p.Outcomes.Reject, "deferred": p.Outcomes.Defer}[outcome]
	if to == "" {
		return nil
	}
	_, err = s.eng.Transition(ctx, tx, actor, elementID, to, in.Comment, engine.TransitionOpts{System: true})
	return err
}

// StepView è un passo di approvazione da mostrare.
type StepView struct {
	ID         string     `json:"id"`
	Ordinal    int        `json:"ordinal"`
	Approver   string     `json:"approver"` // descrizione: "Ruolo: sponsor", nome del membro o dell'organo
	Decision   string     `json:"decision"`
	Comment    *string    `json:"comment"`
	Conditions *string    `json:"conditions"`
	DecidedBy  *string    `json:"decided_by"`
	DecidedAt  *time.Time `json:"decided_at"`
	CanDecide  bool       `json:"can_decide"`
	// InlineDecision: si può decidere dall'elenco senza aprire il pacchetto. Per un passo di un
	// organo vale l'impostazione dell'organo; negli altri casi è sempre vero.
	InlineDecision bool `json:"inline_decision"`
}

type RequestView struct {
	ID          string     `json:"id"`
	Stage       string     `json:"stage"`
	Status      string     `json:"status"`
	AmountCents *int64     `json:"amount_cents"`
	RequestedAt time.Time  `json:"requested_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Steps       []StepView `json:"steps"`
}

// ForElement elenca le richieste di approvazione di un elemento, la più recente prima.
func (s *Service) ForElement(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) ([]RequestView, error) {
	rows, err := tx.Query(ctx, `select id, stage, status, amount_cents, requested_at, closed_at, subject, coalesce(requested_by::text, '')
		from approval_requests where element_id = $1 order by requested_at desc`, elementID)
	if err != nil {
		return nil, err
	}
	selfRequested := map[string]bool{} // separazione dei compiti: chi ha chiesto non decide
	reqs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestView, error) {
		var v RequestView
		var subject, by string
		err := r.Scan(&v.ID, &v.Stage, &v.Status, &v.AmountCents, &v.RequestedAt, &v.ClosedAt, &subject, &by)
		selfRequested[v.ID] = s.separate[subject] && by != "" && by == actor.MemberID
		return v, err
	})
	if err != nil {
		return nil, err
	}
	for i := range reqs {
		rows, err := tx.Query(ctx, `
			select st.id, st.ordinal,
			       coalesce(m.display_name, b.name, coalesce(r.name, st.approver_role_key) || coalesce(' · ' || (
			         select string_agg(coalesce(am.display_name, 'gruppo ' || ag.name), ', ' order by coalesce(am.display_name, ag.name))
			         from approval_step_assignees a
			         left join members am on am.tenant_id = a.tenant_id and am.id = a.member_id
			         left join groups ag on ag.tenant_id = a.tenant_id and ag.id = a.group_id
			         where a.tenant_id = st.tenant_id and a.step_id = st.id), '')),
			       st.decision, st.comment, st.conditions, d.display_name, st.decided_at,
			       coalesce(b.inline_decision, true)
			from approval_steps st
			left join members m on m.tenant_id = st.tenant_id and m.id = st.approver_member_id
			left join governance_bodies b on b.tenant_id = st.tenant_id and b.id = st.approver_body_id
			left join stakeholder_roles r on r.tenant_id = st.tenant_id and r.key = st.approver_role_key
			left join members d on d.tenant_id = st.tenant_id and d.id = st.decided_by
			where st.request_id = $1 order by st.ordinal, st.id`, reqs[i].ID)
		if err != nil {
			return nil, err
		}
		steps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StepView, error) {
			var v StepView
			err := r.Scan(&v.ID, &v.Ordinal, &v.Approver, &v.Decision, &v.Comment, &v.Conditions, &v.DecidedBy, &v.DecidedAt, &v.InlineDecision)
			return v, err
		})
		if err != nil {
			return nil, err
		}
		if reqs[i].Status == "pending" {
			active := 0
			for _, st := range steps {
				if st.Decision == "pending" && (active == 0 || st.Ordinal < active) {
					active = st.Ordinal
				}
			}
			for j := range steps {
				if steps[j].Decision == "pending" && steps[j].Ordinal == active {
					ok, _, err := mayDecide(ctx, tx, steps[j].ID, actor.MemberID)
					if err != nil {
						return nil, err
					}
					steps[j].CanDecide = ok && !selfRequested[reqs[i].ID]
				}
			}
		}
		reqs[i].Steps = steps
	}
	return reqs, nil
}

// InboxItem è un passo che attende la decisione dell'attore.
type InboxItem struct {
	StepID      string    `json:"step_id"`
	ElementID   string    `json:"element_id"`
	ElementKey  string    `json:"element_key"`
	Title       string    `json:"title"`
	Stage       string    `json:"stage"`
	AmountCents *int64    `json:"amount_cents"`
	RequestedAt time.Time `json:"requested_at"`
	// InlineDecision: false se ogni organo che aspetta la decisione vuole prima l'apertura del pacchetto.
	InlineDecision bool `json:"inline_decision"`
	// ApprovalSubject: «stage_gate», «budget», … o «timesheet». Una settimana di timesheet non ha
	// un elemento (ElementID e ElementKey sono vuoti, Title è il nome della persona): la apre
	// il timesheet della persona (TimesheetMember, TimesheetWeek).
	ApprovalSubject string  `json:"approval_subject"`
	TimesheetMember *string `json:"timesheet_member"`
	TimesheetWeek   *string `json:"timesheet_week"`
}

// Inbox: passi attivi che l'attore può decidere, direttamente o per delega.
// Gli assegnatari dei passi attivi sono già risolti (approval_step_assignees, migrazione
// 0004), quindi il costo dipende dai passi dell'attore, non da quelli del tenant.
func (s *Service) Inbox(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]InboxItem, error) {
	rows, err := tx.Query(ctx, `
		with who as (
			select $1::uuid as member_id
			union
			select d.from_member_id from delegations d
			where d.to_member_id = $1 and d.scope in ('approvals', 'all') and now() between d.valid_from and d.valid_to
		),
		mine as (
			select a.step_id from approval_step_assignees a where a.member_id in (select member_id from who)
			union
			select a.step_id from approval_step_assignees a
			where a.group_id in (select member_groups(w.member_id) from who w)
			union
			select a.step_id from approval_step_assignees a
			where a.body_id in (select bm.body_id from governance_body_members bm where bm.voting and bm.member_id in (select member_id from who)
				union select bg.body_id from governance_body_groups bg where bg.voting and bg.group_id in (select member_groups(w.member_id) from who w))
		)
		select st.id, coalesce(e.id::text, ''), coalesce(sp.key || '-' || e.number, ''), coalesce(e.title, tm.display_name, ''), rq.stage, rq.amount_cents, rq.requested_at,
		       coalesce((select b.inline_decision from governance_bodies b where b.tenant_id = st.tenant_id and b.id = st.approver_body_id), true),
		       rq.subject, rq.timesheet_member_id::text, to_char(rq.timesheet_week, 'YYYY-MM-DD')
		from mine
		join approval_steps st on st.id = mine.step_id and st.decision = 'pending'
		join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id and rq.status = 'pending'
			and not (rq.subject = any($2::text[]) and rq.requested_by is not distinct from $1::uuid)
		left join elements e on e.tenant_id = rq.tenant_id and e.id = rq.element_id and e.deleted_at is null
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join members tm on tm.tenant_id = rq.tenant_id and tm.id = rq.timesheet_member_id
		where e.id is not null or rq.subject = 'timesheet'
		order by rq.requested_at
		limit 500`, actor.MemberID, s.separatedSubjects())
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[InboxItem])
	if out == nil {
		out = []InboxItem{}
	}
	return out, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
