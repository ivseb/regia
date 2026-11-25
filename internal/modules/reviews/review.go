package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
	"regia/internal/platform/db"
)

func addWorkdays(t time.Time, n int) time.Time { return calendar.AddWorkdays(t, n) }
func lateDays(due, today time.Time) int        { return calendar.Late(due, today) }

// reviewRow è la riga di ext_review con i dati di tipo e team che servono al modulo.
type reviewRow struct {
	ID, Key, Title    string
	TypeID, TypeKey   string
	TypeName, Scope   string
	TeamID, Team      string
	GroupID           string
	LeadID            *string
	SLADays           int
	Deciders          string
	Outcomes          []Outcome
	TemplateID        string
	RequestID         string
	ImpactID          *string
	Mandatory         bool
	Stage             string
	Answers           map[string]storedAnswer
	SubmittedAt       *time.Time
	DueOn, PausedOn   *time.Time
	Outcome           *string
	OutcomeLabel      *string
	OutcomePolarity   *string
	DecisionNote      string
	DecidedAt         *time.Time
	DecidedBy         *string
	SupersededBy      *string
	OpenedByRule      *string
	RequiredMissing   int
	EscalationDays    *int
	ReminderDays      int
	RemindedAt        *time.Time
	OverdueNotifiedAt *time.Time
	EscalatedAt       *time.Time
	ElementStateCat   string
	ElementState      string
	ElementCreatedAt  time.Time
	ElementVersion    int
	ElementProcessVer *string
	SupersededByKey   *string
	DecidedByName     *string
}

// storedAnswer è la risposta corrente: valore, chi, quando, origine (manual | prefill).
type storedAnswer struct {
	V   any       `json:"v"`
	By  *string   `json:"by"`
	At  time.Time `json:"at"`
	Src string    `json:"src"`
}

const selectReview = `
	select e.id, s.key || '-' || e.number, e.title, rt.id, rt.key, rt.name, rt.scope, t.id, t.name, t.group_id, t.lead_id,
	       t.sla_days, rt.deciders, rt.outcomes, x.template_id, x.request_id, x.impact_id, x.mandatory, x.stage, x.answers,
	       x.submitted_at, x.due_on, x.paused_on, x.outcome, x.outcome_label, x.outcome_polarity, x.decision_note, x.decided_at,
	       x.decided_by, x.superseded_by, x.opened_by_rule, x.required_missing, t.escalation_days, t.reminder_days,
	       x.reminded_at, x.overdue_notified_at, x.escalated_at, coalesce(o.category, ''), coalesce(o.name, ''), e.created_at,
	       e.version, e.process_version_id,
	       (select ss.key || '-' || se.number from elements se join spaces ss on ss.tenant_id = se.tenant_id and ss.id = se.space_id where se.id = x.superseded_by),
	       dm.display_name
	from ext_review x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join review_types rt on rt.tenant_id = x.tenant_id and rt.id = x.review_type_id
	join review_teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
	left join members dm on dm.tenant_id = x.tenant_id and dm.id = x.decided_by`

func scanReview(row pgx.Row) (*reviewRow, error) {
	r := &reviewRow{}
	var outcomes, answers []byte
	err := row.Scan(&r.ID, &r.Key, &r.Title, &r.TypeID, &r.TypeKey, &r.TypeName, &r.Scope, &r.TeamID, &r.Team, &r.GroupID, &r.LeadID,
		&r.SLADays, &r.Deciders, &outcomes, &r.TemplateID, &r.RequestID, &r.ImpactID, &r.Mandatory, &r.Stage, &answers,
		&r.SubmittedAt, &r.DueOn, &r.PausedOn, &r.Outcome, &r.OutcomeLabel, &r.OutcomePolarity, &r.DecisionNote, &r.DecidedAt,
		&r.DecidedBy, &r.SupersededBy, &r.OpenedByRule, &r.RequiredMissing, &r.EscalationDays, &r.ReminderDays,
		&r.RemindedAt, &r.OverdueNotifiedAt, &r.EscalatedAt, &r.ElementStateCat, &r.ElementState, &r.ElementCreatedAt,
		&r.ElementVersion, &r.ElementProcessVer, &r.SupersededByKey, &r.DecidedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(outcomes, &r.Outcomes); err != nil {
		return nil, err
	}
	r.Answers = map[string]storedAnswer{}
	if len(answers) > 0 {
		if err := json.Unmarshal(answers, &r.Answers); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// loadReview legge la valutazione; lock = blocca la riga per le modifiche.
func (s *Service) loadReview(ctx context.Context, tx pgx.Tx, id string, lock bool) (*reviewRow, error) {
	q := selectReview + ` where x.element_id = $1`
	if lock {
		q += ` for update of x`
	}
	return scanReview(tx.QueryRow(ctx, q, id))
}

// resolve trova la valutazione per chiave (DEM-30) o id, verificando che l'attore la veda.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, lock bool) (*engine.Element, *reviewRow, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, nil, err
	}
	if el.TypeKey != TypeKey {
		return nil, nil, engine.ErrNotFound
	}
	r, err := s.loadReview(ctx, tx, el.ID, lock)
	return el, r, err
}

func (r *reviewRow) answerValues() map[string]any {
	out := make(map[string]any, len(r.Answers))
	for k, a := range r.Answers {
		out[k] = a.V
	}
	return out
}

// contextFacts sono i fatti su cui si valutano condizioni e precompilazioni: quelli
// dell'oggetto valutato (impatto o richiesta) e, per un impatto, anche quelli della
// richiesta con il prefisso "richiesta.".
func (s *Service) contextFacts(ctx context.Context, tx pgx.Tx, r *reviewRow) (engine.Facts, error) {
	sys := reader("")
	req, err := s.eng.Get(ctx, tx, sys, r.RequestID)
	if err != nil {
		return nil, err
	}
	rf, err := s.eng.ElementFacts(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if r.ImpactID == nil {
		return rf, nil
	}
	imp, err := s.eng.Get(ctx, tx, sys, *r.ImpactID)
	if err != nil {
		return nil, err
	}
	f, err := s.eng.ElementFacts(ctx, tx, imp)
	if err != nil {
		return nil, err
	}
	for k, v := range rf {
		f["richiesta."+k] = v
	}
	return f, nil
}

func (s *Service) evaluate(ctx context.Context, tx pgx.Tx, r *reviewRow) (*Evaluation, *Definition, error) {
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, nil, err
	}
	f, err := s.contextFacts(ctx, tx, r)
	if err != nil {
		return nil, nil, err
	}
	return Evaluate(def, f, r.answerValues()), def, nil
}

// ---------------------------------------------------------------------------
// Apertura

// setRole assegna un ruolo sulla valutazione senza la notifica generica dei ruoli: il
// modulo manda le sue (valutazione aperta, sezione assegnata).
func setRole(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, role, memberID, groupID string) error {
	tag, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id, group_id)
		select app_tenant(), $1, r.id, $3, $4 from stakeholder_roles r where r.key = $2 on conflict do nothing`,
		elementID, role, nullable(memberID), nullable(groupID))
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return engine.AddActivity(ctx, tx, elementID, actor.MemberID, "stakeholder", role, map[string]any{"member_id": memberID, "group_id": groupID}, nil)
}

type holder struct{ member, group *string }

// owners: i titolari del primo ruolo di owner_roles presente sull'impatto o sulla richiesta.
func ownersFor(ctx context.Context, tx pgx.Tx, roles []string, elements ...string) ([]holder, error) {
	for _, role := range roles {
		for _, id := range elements {
			if id == "" {
				continue
			}
			rows, err := tx.Query(ctx, `select sh.member_id, sh.group_id from stakeholders sh
				join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
				where sh.element_id = $1 and r.key = $2 and (sh.member_id is not null or sh.group_id is not null)`, id, role)
			if err != nil {
				return nil, err
			}
			var hs []holder
			for rows.Next() {
				var h holder
				if err := rows.Scan(&h.member, &h.group); err != nil {
					rows.Close()
					return nil, err
				}
				hs = append(hs, h)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
			if len(hs) > 0 {
				return hs, nil
			}
		}
	}
	return nil, nil
}

// open apre una valutazione del tipo per la richiesta o per un impatto. Con byRule una
// valutazione già esistente (aperta o conclusa) fa saltare l'apertura (nil, nil); a mano
// si può riaprire solo dopo la conclusione, e la nuova sostituisce la precedente.
func (s *Service) open(ctx context.Context, tx pgx.Tx, actor engine.Actor, typ *ReviewType, req, imp *engine.Element, rule string, byRule bool) (*engine.Element, error) {
	if typ.TemplateIDCu == nil {
		if byRule {
			return nil, nil
		}
		return nil, engine.InvalidParams("validation.reviews.template_unpublished", "type", map[string]any{"template": typ.TemplateKey}, "il modello di questionario %q non ha una versione pubblicata", typ.TemplateKey)
	}
	target := req.ID
	if imp != nil {
		target = imp.ID
	}
	// Una sola apertura alla volta per tipo e oggetto (la regola può scattare in parallelo).
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 7))", typ.ID+"/"+target); err != nil {
		return nil, err
	}
	var prevID, prevStage string
	err := tx.QueryRow(ctx, `select element_id, stage from ext_review where review_type_id = $1 and coalesce(impact_id, request_id) = $2
		and superseded_by is null and stage <> 'cancelled'`, typ.ID, target).Scan(&prevID, &prevStage)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		prevID = ""
	case err != nil:
		return nil, err
	case byRule:
		return nil, nil
	case prevStage != StageConcluded:
		return nil, engine.Duplicate("type", "c'è già una valutazione %s aperta", typ.Name)
	}
	title := typ.Name + " · " + req.Title
	if imp != nil {
		var app string
		if err := tx.QueryRow(ctx, `select ap.title from ext_impact x join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
			where x.element_id = $1`, imp.ID).Scan(&app); err != nil {
			return nil, err
		}
		title = typ.Name + " · " + app + " · " + req.Title
	}
	if len(title) > 500 {
		title = title[:497] + "…"
	}
	parent := req.ID
	if imp != nil {
		parent = imp.ID
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeKey, Title: title, ParentID: parent, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if prevID != "" {
		if _, err := tx.Exec(ctx, "update ext_review set superseded_by = $2 where element_id = $1", prevID, el.ID); err != nil {
			return nil, err
		}
	}
	var impID any
	if imp != nil {
		impID = imp.ID
	}
	if _, err := tx.Exec(ctx, `insert into ext_review (tenant_id, element_id, review_type_id, team_id, template_id, request_id, impact_id, mandatory, opened_by_rule)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`,
		el.ID, typ.ID, typ.TeamID, *typ.TemplateIDCu, req.ID, impID, typ.Mandatory, nullable(rule)); err != nil {
		return nil, err
	}
	// Ruoli: il team (gruppo), il suo responsabile, chi compila.
	if err := setRole(ctx, tx, actor, el.ID, RoleTeam, "", typ.GroupID); err != nil {
		return nil, err
	}
	if typ.LeadID != nil {
		if err := setRole(ctx, tx, actor, el.ID, RoleLead, *typ.LeadID, ""); err != nil {
			return nil, err
		}
	}
	impRef := ""
	if imp != nil {
		impRef = imp.ID
	}
	owners, err := ownersFor(ctx, tx, typ.OwnerRoles, impRef, req.ID)
	if err != nil {
		return nil, err
	}
	if len(owners) == 0 && actor.MemberID != "" {
		owners = []holder{{member: &actor.MemberID}}
	}
	var ownerMembers, ownerGroups []string
	for _, h := range owners {
		if err := setRole(ctx, tx, actor, el.ID, RoleOwner, deref(h.member), deref(h.group)); err != nil {
			return nil, err
		}
		ownerMembers, ownerGroups = append(ownerMembers, deref(h.member)), append(ownerGroups, deref(h.group))
	}
	// Precompilazione dai fatti dell'oggetto valutato.
	r, err := s.loadReview(ctx, tx, el.ID, true)
	if err != nil {
		return nil, err
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	facts, err := s.contextFacts(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	pre := map[string]any{}
	for _, sec := range def.Sections {
		for i := range sec.Questions {
			q := &sec.Questions[i]
			if q.Prefill == "" {
				continue
			}
			if v := q.prefill(facts[strings.ToLower(q.Prefill)]); v != nil {
				pre[q.Key] = v
			}
		}
	}
	if err := s.writeAnswers(ctx, tx, engine.Actor{}, r, def, facts, pre, "prefill"); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	// Storia della richiesta (e dell'impatto), audit, notifiche.
	payload := map[string]any{"review": el.Key, "type": typ.Name, "team": typ.Team}
	if rule != "" {
		payload["rule"] = rule
	}
	for _, id := range []string{req.ID, impRef} {
		if id == "" {
			continue
		}
		if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "review", "opened", payload, nil); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "review.open", EntityType: TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"type": typ.Key, "request": req.Key, "rule": rule}}); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReview, ElementID: el.ID, ActorID: actor.MemberID,
		Groups: append([]string{typ.GroupID}, ownerGroups...), Members: ownerMembers,
		Payload: map[string]any{"event": "opened", "team": typ.Team, "type": typ.Name}}); err != nil {
		return nil, err
	}
	return el, nil
}

// OpenManual apre a mano una valutazione di un tipo sulla richiesta o su un impatto.
// Possono farlo chi ha un ruolo di analisi sulla richiesta (o element.write) e i membri
// del team del tipo.
func (s *Service) OpenManual(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, typeKey, impactRef string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	typ, err := s.reviewType(ctx, tx, typeKey)
	if errors.Is(err, engine.ErrNotFound) {
		return nil, engine.NotFound("type", "type", "tipo di valutazione inesistente")
	}
	if err != nil {
		return nil, err
	}
	if typ.Archived {
		return nil, engine.InvalidParams("validation.reviews.type_archived", "type", map[string]any{"name": typ.Name}, "il tipo %s è archiviato", typ.Name)
	}
	sys := reader(actor.MemberID)
	var req, imp *engine.Element
	switch el.TypeKey {
	case RequestType:
		req = el
		if typ.Scope == "impact" {
			if impactRef == "" {
				return nil, engine.Required("impact", "indica l'impatto da valutare")
			}
			if imp, err = s.eng.Get(ctx, tx, actor, impactRef); err != nil {
				return nil, err
			}
		}
	case ImpactType:
		imp = el
		if req, err = s.eng.Get(ctx, tx, sys, deref(el.ParentID)); err != nil {
			return nil, err
		}
		if typ.Scope == "request" {
			imp = nil
		}
	default:
		return nil, engine.InvalidCode("validation.reviews.element_kind", "element", "le valutazioni si aprono su richieste e impatti")
	}
	if imp != nil && (imp.TypeKey != ImpactType || deref(imp.ParentID) != req.ID) {
		return nil, engine.InvalidCode("validation.requirements.impact_not_of_request", "impact", "l'impatto non è di questa richiesta")
	}
	ok, err := s.canOpen(ctx, tx, actor, req, typ)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	return s.open(ctx, tx, actor, typ, req, imp, "", false)
}

var analystRoles = []string{"demand_manager", "pmo", "project_manager", "architect", "technical_analyst"}

func (s *Service) canOpen(ctx context.Context, tx pgx.Tx, actor engine.Actor, req *engine.Element, typ *ReviewType) (bool, error) {
	if actor.Has("element.write") {
		return true, nil
	}
	if ok, err := engine.HasRole(ctx, tx, req.ID, actor.MemberID, analystRoles); err != nil || ok {
		return ok, err
	}
	if typ == nil {
		return false, nil
	}
	return inGroup(ctx, tx, typ.GroupID, actor.MemberID)
}

func inGroup(ctx context.Context, tx pgx.Tx, groupID, memberID string) (bool, error) {
	if memberID == "" {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, "select member_in_group($2, $1)", groupID, memberID).Scan(&ok)
	return ok, err
}

// ---------------------------------------------------------------------------
// Permessi sulla valutazione

type perms struct {
	member      string
	owner       bool
	team        bool
	lead        bool
	sections    []string // sezioni assegnate all'attore
	assignments map[string]string
}

func (s *Service) permsOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *reviewRow) (*perms, error) {
	p := &perms{member: actor.MemberID, assignments: map[string]string{}}
	var err error
	if p.owner, err = engine.HasRole(ctx, tx, r.ID, actor.MemberID, []string{RoleOwner}); err != nil {
		return nil, err
	}
	if p.team, err = inGroup(ctx, tx, r.GroupID, actor.MemberID); err != nil {
		return nil, err
	}
	p.lead = r.LeadID != nil && *r.LeadID == actor.MemberID
	rows, err := tx.Query(ctx, "select section_key, member_id::text from questionnaire_assignments where review_id = $1", r.ID)
	if err != nil {
		return nil, err
	}
	var sec, mem string
	if _, err := pgx.ForEachRow(rows, []any{&sec, &mem}, func() error {
		p.assignments[sec] = mem
		if mem == actor.MemberID {
			p.sections = append(p.sections, sec)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// canAnswer: mentre si compila (o durante i chiarimenti) rispondono chi avvia il
// questionario, chi ha la sezione assegnata e il team (che può correggere); durante
// l'esame solo il team. Una volta inviata, conclusa o annullata nessuno.
func (p *perms) canAnswer(stage, section string, teamOnly bool) bool {
	switch stage {
	case StageCompile, StageClarification:
		if teamOnly {
			return p.team || slices.Contains(p.sections, section)
		}
		return p.owner || p.team || slices.Contains(p.sections, section)
	case StageExam:
		return p.team
	}
	return false
}

func (p *perms) canDecide(r *reviewRow) bool {
	if r.Deciders == "lead" {
		return p.lead
	}
	return p.team || p.lead
}

// ---------------------------------------------------------------------------
// Risposte

// writeAnswers salva le risposte (già normalizzate) con la storia, aggiorna il riepilogo
// della valutazione e le prescrizioni proposte dalle regole di derivazione.
func (s *Service) writeAnswers(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *reviewRow, def *Definition, facts engine.Facts, vals map[string]any, source string) error {
	now := time.Now().UTC()
	changed := map[string]any{}
	for k, v := range vals {
		cur, had := r.Answers[k]
		if had && jsonEqual(cur.V, v) && !(cur.Src == "prefill" && source == "manual") {
			continue // nessun cambiamento (una conferma di una precompilazione invece conta)
		}
		if !had && v == nil {
			continue
		}
		raw, _ := json.Marshal(v)
		if v == nil {
			raw = nil
		}
		if _, err := tx.Exec(ctx, `insert into questionnaire_answers (tenant_id, review_id, question_key, value, source, answered_by)
			values (app_tenant(), $1, $2, $3, $4, $5)`, r.ID, k, raw, source, nullable(actor.MemberID)); err != nil {
			return err
		}
		if v == nil {
			delete(r.Answers, k)
		} else {
			var by *string
			if actor.MemberID != "" {
				by = &actor.MemberID
			}
			r.Answers[k] = storedAnswer{V: v, By: by, At: now, Src: source}
		}
		changed[k] = v
	}
	ev := Evaluate(def, facts, r.answerValues())
	answers, _ := json.Marshal(r.Answers)
	if _, err := tx.Exec(ctx, `update ext_review set answers = $2, required_total = $3, required_missing = $4 where element_id = $1`,
		r.ID, answers, ev.Required, len(ev.Missing)); err != nil {
		return err
	}
	if err := syncDerived(ctx, tx, actor, r.ID, ev.Derived); err != nil {
		return err
	}
	if err := syncDerivedRequirements(ctx, tx, r.ID, ev.DerivedRequirements); err != nil {
		return err
	}
	if len(changed) > 0 && source == "manual" {
		keys := make([]string, 0, len(changed))
		for k := range changed {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if err := engine.AddActivity(ctx, tx, r.ID, actor.MemberID, "answers", "", map[string]any{"questions": keys}, nil); err != nil {
			return err
		}
	}
	return nil
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// syncDerived allinea le prescrizioni proposte dalle regole alle risposte: le nuove si
// aggiungono, quelle non più proposte si tolgono finché il team non le ha confermate o scartate.
func syncDerived(ctx context.Context, tx pgx.Tx, actor engine.Actor, reviewID string, derived []Derivation) error {
	keys := make([]string, 0, len(derived))
	for _, d := range derived {
		keys = append(keys, d.Key)
		if _, err := tx.Exec(ctx, `insert into review_prescriptions (tenant_id, id, review_id, source, rule_key, text, category, created_by)
			values (app_tenant(), $1, $2, 'rule', $3, $4, $5, $6) on conflict (tenant_id, review_id, rule_key) where source = 'rule' do nothing`,
			db.NewID(), reviewID, d.Key, strings.TrimSpace(d.Text), d.Category, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `delete from review_prescriptions where review_id = $1 and source = 'rule' and status = 'proposed'
		and not (rule_key = any($2))`, reviewID, keys)
	return err
}

// syncDerivedRequirements allinea i «requisiti proposti» alle risposte: i nuovi si aggiungono,
// quelli non più derivati si tolgono se non sono ancora diventati requisiti. Una regola che in
// una versione precedente aveva già prodotto una prescrizione decisa (accettata o scartata)
// non propone anche il requisito: la prescrizione già creata resta com'è.
func syncDerivedRequirements(ctx context.Context, tx pgx.Tx, reviewID string, derived []Derivation) error {
	keys := make([]string, 0, len(derived))
	for _, d := range derived {
		keys = append(keys, d.Key)
		title := strings.TrimSpace(d.Title)
		if _, err := tx.Exec(ctx, `insert into review_requirement_proposals (tenant_id, review_id, rule_key, title, text, category, method, check_key)
			select app_tenant(), $1, $2, $3, $4, $5, $6, $7
			where not exists (select 1 from review_prescriptions where review_id = $1 and source = 'rule' and rule_key = $2 and status <> 'proposed')
			on conflict (tenant_id, review_id, rule_key) do update
			  set title = excluded.title, text = excluded.text, category = excluded.category, method = excluded.method, check_key = excluded.check_key`,
			reviewID, d.Key, title, strings.TrimSpace(d.Text), d.Category, d.Method, nullable(strings.TrimSpace(d.Check))); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `delete from review_requirement_proposals p where p.review_id = $1 and not (p.rule_key = any($2))
		and not exists (select 1 from ext_requirement r where r.review_id = p.review_id and r.origin = 'questionnaire' and r.rule_key = p.rule_key)`,
		reviewID, keys)
	return err
}

// SaveAnswers salva in parte le risposte (salvataggio automatico): valori nil cancellano.
// Il primo salvataggio porta la valutazione da "da compilare" alla fase di compilazione.
func (s *Service) SaveAnswers(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in map[string]any) (*View, error) {
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	if len(in) > 500 {
		return nil, engine.InvalidCode("validation.reviews.answers_too_many", "answers", "troppe risposte in un salvataggio")
	}
	vals := map[string]any{}
	for k, v := range in {
		q, sec := def.question(k)
		if q == nil {
			return nil, engine.InvalidCode("validation.reviews.question_unknown", "answers."+k, "domanda inesistente in questo modello")
		}
		if !p.canAnswer(r.Stage, sec.Key, sec.FilledBy == "team") {
			if r.Stage == StageCompile || r.Stage == StageClarification || r.Stage == StageExam {
				return nil, engine.ErrForbidden
			}
			return nil, engine.InvalidCode("validation.reviews.not_fillable", "answers", "la valutazione non si compila in questa fase")
		}
		nv, err := q.NormalizeValue(v)
		if err != nil {
			return nil, err
		}
		if q.Type == QAttachment && nv != nil {
			ids := nv.([]string)
			var n int
			if err := tx.QueryRow(ctx, "select count(*) from attachments where element_id = $1 and id::text = any($2) and removed_at is null",
				r.ID, ids).Scan(&n); err != nil {
				return nil, err
			}
			if n != len(ids) {
				return nil, engine.InvalidCode("validation.reviews.attachment_not_uploaded", "answers."+k, "allegato non caricato su questa valutazione")
			}
		}
		vals[k] = nv
	}
	facts, err := s.contextFacts(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if err := s.writeAnswers(ctx, tx, actor, r, def, facts, vals, "manual"); err != nil {
		return nil, err
	}
	if r.ElementStateCat == "todo" && r.Stage == StageCompile {
		if err := s.startCompiling(ctx, tx, actor, el); err != nil {
			return nil, err
		}
	} else if _, err := tx.Exec(ctx, "update elements set version = version + 1, updated_by = $2 where id = $1", el.ID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// startCompiling porta la valutazione nella prima fase "in corso" raggiungibile, come
// farebbe "Inizia la compilazione".
func (s *Service) startCompiling(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element) error {
	phases, _, err := s.eng.Phases(ctx, tx, el)
	if err != nil {
		return err
	}
	for _, ph := range phases {
		if ph.Category != "in_progress" || ph.OffPath {
			continue
		}
		_, err := s.eng.Transition(ctx, tx, actor, el.ID, ph.Name, "", engine.TransitionOpts{System: true})
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			continue // passaggio non previsto da questa fase: si prova la successiva
		}
		return err
	}
	return nil
}

// AnswerHistory è una voce della storia delle risposte.
type AnswerHistory struct {
	ID       int64     `json:"id"`
	Question string    `json:"question"`
	Value    any       `json:"value"`
	Source   string    `json:"source"`
	By       *string   `json:"by"`
	At       time.Time `json:"at"`
}

// History restituisce la storia delle risposte (tutte o di una domanda), dalla più recente.
func (s *Service) History(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, question string) ([]AnswerHistory, error) {
	_, r, err := s.resolve(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select a.id, a.question_key, a.value, a.source, m.display_name, a.answered_at
		from questionnaire_answers a left join members m on m.tenant_id = a.tenant_id and m.id = a.answered_by
		where a.review_id = $1 and ($2 = '' or a.question_key = $2) order by a.id desc limit 500`, r.ID, question)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AnswerHistory, error) {
		var h AnswerHistory
		var raw []byte
		err := row.Scan(&h.ID, &h.Question, &raw, &h.Source, &h.By, &h.At)
		if err == nil && len(raw) > 0 {
			err = json.Unmarshal(raw, &h.Value)
		}
		return h, err
	})
}

// ---------------------------------------------------------------------------
// Sezioni assegnate

// Assign assegna una sezione a una persona (memberID vuoto = toglie l'assegnazione). Lo
// fanno chi avvia il questionario e il team.
func (s *Service) Assign(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, section, memberID string) (*View, error) {
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	if !(p.owner || p.team) {
		return nil, engine.ErrForbidden
	}
	if r.Stage == StageConcluded || r.Stage == StageCancelled {
		return nil, engine.InvalidCode("validation.reviews.closed", "section", "la valutazione è chiusa")
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	var sec *Section
	for i := range def.Sections {
		if def.Sections[i].Key == section {
			sec = &def.Sections[i]
		}
	}
	if sec == nil {
		return nil, engine.NotFound("section", "", "sezione inesistente")
	}
	if memberID == "" {
		if _, err := tx.Exec(ctx, "delete from questionnaire_assignments where review_id = $1 and section_key = $2", r.ID, section); err != nil {
			return nil, err
		}
	} else {
		var name string
		err := tx.QueryRow(ctx, "select display_name from members where id::text = $1 and status = 'active'", memberID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidCode("validation.member_not_found", "member_id", "membro inesistente o non attivo")
		}
		if err != nil {
			return nil, err
		}
		if p.assignments[section] == memberID {
			return s.View(ctx, tx, actor, el.ID)
		}
		if _, err := tx.Exec(ctx, `insert into questionnaire_assignments (tenant_id, review_id, section_key, member_id, assigned_by)
			values (app_tenant(), $1, $2, $3, $4) on conflict (tenant_id, review_id, section_key)
			do update set member_id = excluded.member_id, assigned_by = excluded.assigned_by, assigned_at = now()`,
			r.ID, section, memberID, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
		// Il compilatore vede la valutazione (ruolo sull'elemento) e riceve l'avviso.
		if err := setRole(ctx, tx, actor, r.ID, RoleCompiler, memberID, ""); err != nil {
			return nil, err
		}
		if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReview, ElementID: r.ID, ActorID: actor.MemberID,
			Members: []string{memberID}, Payload: map[string]any{"event": "section", "section": sec.Title, "team": r.Team}}); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, r.ID, actor.MemberID, "assignment", section, map[string]any{"member_id": memberID}, nil); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// ---------------------------------------------------------------------------
// Prescrizioni ed esito

// Prescription è una prescrizione della valutazione.
type Prescription struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"` // rule | manual
	RuleKey   *string   `json:"rule_key"`
	Text      string    `json:"text"`
	Category  string    `json:"category"`
	Status    string    `json:"status"` // proposed | accepted | rejected
	CreatedBy *string   `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	DecidedBy *string   `json:"decided_by"`
}

func prescriptions(ctx context.Context, tx pgx.Tx, reviewID string) ([]Prescription, error) {
	rows, err := tx.Query(ctx, `select p.id, p.source, p.rule_key, p.text, p.category, p.status, c.display_name, p.created_at, d.display_name
		from review_prescriptions p
		left join members c on c.tenant_id = p.tenant_id and c.id = p.created_by
		left join members d on d.tenant_id = p.tenant_id and d.id = p.decided_by
		where p.review_id = $1 order by p.source desc, p.created_at, p.id`, reviewID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Prescription])
}

// RequirementProposal è un requisito proposto da una regola del questionario. Finché la
// valutazione non si chiude Requirement è vuoto; dopo porta la chiave del requisito creato.
type RequirementProposal struct {
	RuleKey     string  `json:"rule_key"`
	Title       string  `json:"title"`
	Text        string  `json:"text"`
	Category    string  `json:"category"`
	Method      string  `json:"method"`
	Check       *string `json:"check"`
	Requirement *string `json:"requirement"`
}

func requirementProposals(ctx context.Context, tx pgx.Tx, reviewID string) ([]RequirementProposal, error) {
	rows, err := tx.Query(ctx, `select p.rule_key, p.title, p.text, p.category, p.method, p.check_key, rs.key || '-' || re.number
		from review_requirement_proposals p
		left join ext_requirement x on x.tenant_id = p.tenant_id and x.review_id = p.review_id and x.origin = 'questionnaire' and x.rule_key = p.rule_key
		left join elements re on re.tenant_id = x.tenant_id and re.id = x.element_id
		left join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		where p.review_id = $1 order by p.created_at, p.rule_key`, reviewID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[RequirementProposal])
}

// PrescriptionInput aggiunge o modifica una prescrizione.
type PrescriptionInput struct {
	Text     *string `json:"text"`
	Category *string `json:"category"`
	Status   *string `json:"status"`
}

func (s *Service) teamInExam(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, *reviewRow, error) {
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, nil, err
	}
	if !p.canDecide(r) && !p.team {
		return nil, nil, engine.ErrForbidden
	}
	if r.Stage != StageExam {
		return nil, nil, engine.InvalidCode("validation.reviews.prescriptions_in_review", "stage", "le prescrizioni e l'esito si decidono durante l'esame del team")
	}
	return el, r, nil
}

// AddPrescription aggiunge a mano una prescrizione (il team, durante l'esame).
func (s *Service) AddPrescription(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in PrescriptionInput) (*View, error) {
	el, r, err := s.teamInExam(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(deref(in.Text))
	if text == "" || len(text) > 2000 {
		return nil, engine.TooLong("text", 2000, "testo della prescrizione obbligatorio (massimo 2000 caratteri)")
	}
	if _, err := tx.Exec(ctx, `insert into review_prescriptions (tenant_id, id, review_id, source, text, category, status, created_by, decided_by, decided_at)
		values (app_tenant(), $1, $2, 'manual', $3, $4, 'accepted', $5, $5, now())`,
		db.NewID(), r.ID, text, strings.TrimSpace(deref(in.Category)), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, r.ID, actor.MemberID, "prescription", "added", map[string]any{"text": text}, nil); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// UpdatePrescription conferma o scarta una proposta, o corregge il testo (il team).
func (s *Service) UpdatePrescription(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, id string, in PrescriptionInput) (*View, error) {
	el, r, err := s.teamInExam(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if in.Status != nil && !slices.Contains([]string{"proposed", "accepted", "rejected"}, *in.Status) {
		return nil, engine.InvalidValue("status", "stato non ammesso (proposed, accepted, rejected)")
	}
	if in.Text != nil {
		if t := strings.TrimSpace(*in.Text); t == "" || len(t) > 2000 {
			return nil, engine.TooLong("text", 2000, "testo della prescrizione obbligatorio (massimo 2000 caratteri)")
		}
	}
	tag, err := tx.Exec(ctx, `update review_prescriptions set text = coalesce(nullif(trim($3), ''), text), category = coalesce($4, category),
		status = coalesce($5, status), decided_by = $6, decided_at = now() where id::text = $1 and review_id = $2`,
		id, r.ID, in.Text, in.Category, in.Status, nullable(actor.MemberID))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.ErrNotFound
	}
	if err := engine.AddActivity(ctx, tx, r.ID, actor.MemberID, "prescription", "updated", map[string]any{"id": id, "status": in.Status}, nil); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// RemovePrescription toglie una prescrizione aggiunta a mano.
func (s *Service) RemovePrescription(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, id string) (*View, error) {
	el, r, err := s.teamInExam(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, "delete from review_prescriptions where id::text = $1 and review_id = $2 and source = 'manual'", id, r.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidCode("validation.reviews.prescription_manual_only", "id", "si tolgono solo le prescrizioni aggiunte a mano: quelle proposte si scartano")
	}
	return s.View(ctx, tx, actor, el.ID)
}

// DecisionInput è l'esito della valutazione.
type DecisionInput struct {
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

// Decide registra l'esito: solo il team (o il responsabile, se il tipo lo prevede),
// durante l'esame. Le proposte devono essere confermate o scartate; un esito
// favorevole non ha prescrizioni accettate, uno "con prescrizioni" ne ha almeno una.
// La valutazione passa alla fase conclusiva del suo processo.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in DecisionInput) (*View, error) {
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	if !p.canDecide(r) {
		return nil, engine.ErrForbidden
	}
	if r.Stage != StageExam {
		return nil, engine.InvalidCode("validation.reviews.outcome_in_review", "stage", "l'esito si decide durante l'esame del team")
	}
	var o *Outcome
	for i := range r.Outcomes {
		if r.Outcomes[i].Key == in.Outcome {
			o = &r.Outcomes[i]
		}
	}
	if o == nil {
		return nil, engine.InvalidValue("outcome", "esito non ammesso per questo tipo di valutazione")
	}
	var proposed, accepted int
	if err := tx.QueryRow(ctx, `select count(*) filter (where status = 'proposed')::int, count(*) filter (where status = 'accepted')::int
		from review_prescriptions where review_id = $1`, r.ID).Scan(&proposed, &accepted); err != nil {
		return nil, err
	}
	switch {
	case proposed > 0:
		return nil, engine.InvalidParams("validation.reviews.prescriptions_pending", "prescriptions", map[string]any{"n": proposed}, "conferma o scarta le %d prescrizioni proposte prima dell'esito", proposed)
	case o.Polarity == "positive" && accepted > 0:
		return nil, engine.InvalidCode("validation.reviews.outcome_with_prescriptions", "outcome", "ci sono prescrizioni accettate: scegli un esito con prescrizioni")
	case o.Polarity == "conditional" && accepted == 0:
		return nil, engine.InvalidParams("validation.reviews.outcome_needs_prescription", "outcome", map[string]any{"label": o.Label}, "«%s» richiede almeno una prescrizione accettata", o.Label)
	case o.Polarity == "negative" && strings.TrimSpace(in.Note) == "":
		return nil, engine.Required("note", "per un esito non favorevole serve la motivazione")
	}
	if len(in.Note) > 10000 {
		return nil, engine.InvalidCode("validation.reviews.note_too_long", "note", "motivazione troppo lunga")
	}
	if _, err := tx.Exec(ctx, `update ext_review set stage = 'concluded', outcome = $2, outcome_label = $3, outcome_polarity = $4,
		decision_note = $5, decided_at = now(), decided_by = $6 where element_id = $1`,
		r.ID, o.Key, o.Label, o.Polarity, strings.TrimSpace(in.Note), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	// Fase conclusiva del processo della valutazione (passaggio di sistema).
	phases, _, err := s.eng.Phases(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	moved := false
	for _, ph := range phases {
		if ph.Category != "done" || ph.OffPath {
			continue
		}
		if _, err := s.eng.Transition(ctx, tx, actor, el.ID, ph.Name, in.Note, engine.TransitionOpts{System: true}); err != nil {
			var ve *engine.ValidationError
			if errors.As(err, &ve) {
				continue
			}
			return nil, err
		}
		moved = true
		break
	}
	if !moved {
		return nil, engine.InvalidParams("validation.reviews.process_no_final_transition", "process", map[string]any{"state": r.ElementState}, "il processo della valutazione non ha un passaggio di sistema verso una fase conclusiva da %q", r.ElementState)
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "review.decide", EntityType: TypeKey, EntityID: r.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"outcome": o.Key, "prescriptions": accepted}}); err != nil {
		return nil, err
	}
	if err := s.requestActivity(ctx, tx, actor, r, "decided", map[string]any{"outcome": o.Label, "polarity": o.Polarity, "prescriptions": accepted}); err != nil {
		return nil, err
	}
	if err := s.notifyOwners(ctx, tx, actor, r, "decided", "outcome", o.Label); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// requestActivity scrive nella storia della richiesta (e dell'impatto) un evento della valutazione.
func (s *Service) requestActivity(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *reviewRow, event string, extra map[string]any) error {
	payload := map[string]any{"review": r.Key, "type": r.TypeName, "team": r.Team}
	for k, v := range extra {
		payload[k] = v
	}
	for _, id := range []string{r.RequestID, deref(r.ImpactID)} {
		if id == "" {
			continue
		}
		if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "review", event, payload, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) notifyTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *reviewRow, event string) error {
	_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReview, ElementID: r.ID, ActorID: actor.MemberID,
		Groups: []string{r.GroupID}, Members: []string{deref(r.LeadID)}, Payload: map[string]any{"event": event, "team": r.Team}})
	return err
}

// notifyOwners avvisa chi compila: i titolari del ruolo di owner e i compilatori.
func (s *Service) notifyOwners(ctx context.Context, tx pgx.Tx, actor engine.Actor, r *reviewRow, event string, kv ...string) error {
	rows, err := tx.Query(ctx, `select sh.member_id::text, sh.group_id::text from stakeholders sh
		join stakeholder_roles ro on ro.tenant_id = sh.tenant_id and ro.id = sh.role_id
		where sh.element_id = $1 and ro.key = any($2)`, r.ID, []string{RoleOwner, RoleCompiler})
	if err != nil {
		return err
	}
	var members, groups []string
	var m, g *string
	if _, err := pgx.ForEachRow(rows, []any{&m, &g}, func() error {
		members, groups = append(members, deref(m)), append(groups, deref(g))
		return nil
	}); err != nil {
		return err
	}
	payload := map[string]any{"event": event, "team": r.Team}
	for i := 0; i+1 < len(kv); i += 2 {
		payload[kv[i]] = kv[i+1]
	}
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindReview, ElementID: r.ID, ActorID: actor.MemberID,
		Members: members, Groups: groups, Payload: payload})
	return err
}
