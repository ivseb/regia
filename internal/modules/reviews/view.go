package reviews

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Ref è un riferimento leggibile a un elemento.
type Ref struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Info sono i dati della valutazione per la pagina di compilazione ed esame.
type Info struct {
	ID              string     `json:"id"`
	Key             string     `json:"key"`
	Title           string     `json:"title"`
	TypeKey         string     `json:"type_key"`
	TypeName        string     `json:"type_name"`
	Scope           string     `json:"scope"`
	Mandatory       bool       `json:"mandatory"`
	TeamID          string     `json:"team_id"`
	Team            string     `json:"team"`
	Deciders        string     `json:"deciders"`
	Stage           string     `json:"stage"`
	Request         Ref        `json:"request"`
	Impact          *Ref       `json:"impact"`
	Application     *string    `json:"application"`
	SubmittedAt     *time.Time `json:"submitted_at"`
	DueOn           *string    `json:"due_on"`
	Paused          bool       `json:"paused"`
	LateDays        int        `json:"late_days"`
	Outcome         *string    `json:"outcome"`
	OutcomeLabel    *string    `json:"outcome_label"`
	OutcomePolarity *string    `json:"outcome_polarity"`
	DecisionNote    string     `json:"decision_note"`
	DecidedAt       *time.Time `json:"decided_at"`
	DecidedBy       *string    `json:"decided_by"`
	SupersededBy    *string    `json:"superseded_by"`
	OpenedByRule    *string    `json:"opened_by_rule"`
	TemplateKey     string     `json:"template_key"`
	TemplateVersion int        `json:"template_version"`
	TemplateCurrent *int       `json:"template_current_version"`
}

// AnswerView è una risposta corrente con chi l'ha data.
type AnswerView struct {
	Value  any       `json:"value"`
	By     *string   `json:"by"`
	At     time.Time `json:"at"`
	Source string    `json:"source"` // manual | prefill
}

// Assignment è una sezione assegnata.
type Assignment struct {
	Section  string `json:"section"`
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
}

// Attachment è un allegato della valutazione (per le domande di tipo allegato).
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Can dice cosa può fare l'attore.
type Can struct {
	AnswerAll   bool     `json:"answer_all"` // tutte le sezioni
	Sections    []string `json:"sections"`   // sezioni assegnate a lui (se non tutte)
	Assign      bool     `json:"assign"`
	Prescribe   bool     `json:"prescribe"`
	Decide      bool     `json:"decide"`
	IsTeam      bool     `json:"is_team"`
	IsOwner     bool     `json:"is_owner"`
	ReadOnlyWhy string   `json:"read_only_reason,omitempty"`
}

// View è tutto ciò che serve alla pagina della valutazione.
type View struct {
	Review        Info                    `json:"review"`
	Element       *engine.Element         `json:"element"`
	Transitions   []engine.TransitionView `json:"transitions"`
	Phases        []engine.State          `json:"phases"`
	Process       *engine.ProcessRef      `json:"process"`
	Stakeholders  []engine.Stakeholder    `json:"stakeholders"`
	Template      *Definition             `json:"template"`
	Answers       map[string]AnswerView   `json:"answers"`
	Facts         engine.Facts            `json:"facts"`
	Evaluation    *Evaluation             `json:"evaluation"`
	Assignments   []Assignment            `json:"assignments"`
	Prescriptions []Prescription          `json:"prescriptions"`
	// Requisiti proposti dalle regole del questionario: diventano requisiti alla chiusura.
	RequirementProposals []RequirementProposal `json:"requirement_proposals"`
	Outcomes             []Outcome             `json:"outcomes"`
	Attachments          []Attachment          `json:"attachments"`
	Can                  Can                   `json:"can"`
}

// View restituisce la valutazione con questionario, risposte, valutazione delle condizioni,
// prescrizioni e permessi dell'attore.
func (s *Service) View(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*View, error) {
	el, r, err := s.resolve(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	v := &View{Element: el, Outcomes: r.Outcomes}
	if v.Transitions, err = s.eng.Available(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if v.Phases, v.Process, err = s.eng.Phases(ctx, tx, el); err != nil {
		return nil, err
	}
	if v.Stakeholders, err = engine.Stakeholders(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	def, version, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	v.Template = def
	if v.Facts, err = s.contextFacts(ctx, tx, r); err != nil {
		return nil, err
	}
	v.Evaluation = Evaluate(def, v.Facts, r.answerValues())
	info := Info{ID: r.ID, Key: r.Key, Title: r.Title, TypeKey: r.TypeKey, TypeName: r.TypeName, Scope: r.Scope, Mandatory: r.Mandatory,
		TeamID: r.TeamID, Team: r.Team, Deciders: r.Deciders, Stage: r.Stage, SubmittedAt: r.SubmittedAt, Paused: r.PausedOn != nil,
		Outcome: r.Outcome, OutcomeLabel: r.OutcomeLabel, OutcomePolarity: r.OutcomePolarity, DecisionNote: r.DecisionNote,
		DecidedAt: r.DecidedAt, DecidedBy: r.DecidedByName, SupersededBy: r.SupersededByKey, OpenedByRule: r.OpenedByRule,
		TemplateVersion: version}
	if r.DueOn != nil {
		d := r.DueOn.Format(time.DateOnly)
		info.DueOn = &d
		if r.Stage == StageSubmitted || r.Stage == StageExam {
			info.LateDays = lateDays(*r.DueOn, s.today())
		}
	}
	if err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title, q.key,
		(select c.version from questionnaire_templates c where c.key = q.key and c.status = 'published')
		from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join questionnaire_templates q on q.id = $2
		where e.id = $1`, r.RequestID, r.TemplateID).
		Scan(&info.Request.ID, &info.Request.Key, &info.Request.Title, &info.TemplateKey, &info.TemplateCurrent); err != nil {
		return nil, err
	}
	if r.ImpactID != nil {
		imp := Ref{}
		var app string
		if err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title, ap.title
			from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join ext_impact x on x.tenant_id = e.tenant_id and x.element_id = e.id
			join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
			where e.id = $1`, *r.ImpactID).Scan(&imp.ID, &imp.Key, &imp.Title, &app); err != nil {
			return nil, err
		}
		info.Impact, info.Application = &imp, &app
	}
	v.Review = info

	// Risposte con il nome di chi ha risposto.
	v.Answers = map[string]AnswerView{}
	ids := []string{}
	for _, a := range r.Answers {
		if a.By != nil {
			ids = append(ids, *a.By)
		}
	}
	names := map[string]string{}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, "select id::text, display_name from members where id::text = any($1)", ids)
		if err != nil {
			return nil, err
		}
		var id, name string
		if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error { names[id] = name; return nil }); err != nil {
			return nil, err
		}
	}
	for k, a := range r.Answers {
		av := AnswerView{Value: a.V, At: a.At, Source: a.Src}
		if a.By != nil {
			n := names[*a.By]
			av.By = &n
		}
		v.Answers[k] = av
	}
	rows, err := tx.Query(ctx, `select a.section_key, a.member_id::text, m.display_name from questionnaire_assignments a
		join members m on m.tenant_id = a.tenant_id and m.id = a.member_id where a.review_id = $1 order by a.section_key`, r.ID)
	if err != nil {
		return nil, err
	}
	if v.Assignments, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Assignment]); err != nil {
		return nil, err
	}
	if v.Prescriptions, err = prescriptions(ctx, tx, r.ID); err != nil {
		return nil, err
	}
	if v.RequirementProposals, err = requirementProposals(ctx, tx, r.ID); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select id::text, name, size_bytes from attachments where element_id = $1 and removed_at is null order by created_at`, r.ID)
	if err != nil {
		return nil, err
	}
	if v.Attachments, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Attachment]); err != nil {
		return nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	v.Can = Can{IsTeam: p.team || p.lead, IsOwner: p.owner, Sections: []string{}}
	switch r.Stage {
	case StageCompile, StageClarification:
		v.Can.AnswerAll = p.owner || p.team
		if p.owner && !p.team && slices.ContainsFunc(def.Sections, func(s Section) bool { return s.FilledBy == "team" }) {
			// Ci sono sezioni solo del team: chi ha avviato il questionario compila le altre.
			v.Can.AnswerAll = false
			for _, s := range def.Sections {
				if s.FilledBy != "team" || slices.Contains(p.sections, s.Key) {
					v.Can.Sections = append(v.Can.Sections, s.Key)
				}
			}
		} else if !v.Can.AnswerAll {
			v.Can.Sections = append(v.Can.Sections, p.sections...)
		}
		v.Can.Assign = p.owner || p.team
	case StageExam:
		v.Can.AnswerAll = p.team
		v.Can.Assign = p.owner || p.team
		v.Can.Prescribe = p.team || p.canDecide(r)
		v.Can.Decide = p.canDecide(r)
	case StageSubmitted:
		v.Can.Assign = p.owner || p.team
		v.Can.ReadOnlyWhy = "inviata al team: in attesa della presa in carico"
	case StageConcluded:
		v.Can.ReadOnlyWhy = "valutazione conclusa"
	case StageCancelled:
		v.Can.ReadOnlyWhy = "valutazione annullata"
	}
	if r.Stage == StageExam && !p.team && v.Can.ReadOnlyWhy == "" {
		v.Can.ReadOnlyWhy = "in esame dal team " + r.Team
	}
	return v, nil
}

// Summary è una valutazione negli elenchi (dettaglio della richiesta, inbox).
type Summary struct {
	ID              string     `json:"id"`
	Key             string     `json:"key"`
	Title           string     `json:"title"`
	TypeKey         string     `json:"type_key"`
	TypeName        string     `json:"type_name"`
	Scope           string     `json:"scope"`
	Mandatory       bool       `json:"mandatory"`
	Team            string     `json:"team"`
	Stage           string     `json:"stage"`
	State           string     `json:"state"`
	StateCategory   string     `json:"state_category"`
	RequestKey      string     `json:"request_key"`
	RequestTitle    string     `json:"request_title"`
	ImpactKey       *string    `json:"impact_key"`
	Application     *string    `json:"application"`
	DueOn           *time.Time `json:"-"`
	Due             *string    `json:"due_on"`
	Paused          bool       `json:"paused"`
	LateDays        int        `json:"late_days"`
	OutcomeLabel    *string    `json:"outcome_label"`
	OutcomePolarity *string    `json:"outcome_polarity"`
	RequiredTotal   int        `json:"required_total"`
	RequiredMissing int        `json:"required_missing"`
	Superseded      bool       `json:"superseded"`
	CreatedAt       time.Time  `json:"created_at"`
	Role            string     `json:"role,omitempty"` // inbox: compile | team
}

const selectSummary = `
	select e.id, s.key || '-' || e.number, e.title, rt.key, rt.name, rt.scope, x.mandatory, t.name, x.stage,
	       coalesce(o.name, ''), coalesce(o.category, ''), rs.key || '-' || re.number, re.title,
	       case when ie.id is null then null else is_.key || '-' || ie.number end, ap.title,
	       x.due_on, x.paused_on is not null, x.outcome_label, x.outcome_polarity, x.required_total, x.required_missing,
	       x.superseded_by is not null, e.created_at
	from ext_review x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join review_types rt on rt.tenant_id = x.tenant_id and rt.id = x.review_type_id
	join review_teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
	join elements re on re.tenant_id = x.tenant_id and re.id = x.request_id
	join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.impact_id
	left join spaces is_ on is_.tenant_id = ie.tenant_id and is_.id = ie.space_id
	left join ext_impact xi on xi.tenant_id = x.tenant_id and xi.element_id = x.impact_id
	left join elements ap on ap.tenant_id = xi.tenant_id and ap.id = xi.application_id`

func (s *Service) scanSummaries(rows pgx.Rows) ([]Summary, error) {
	today := s.today()
	// I giorni di ritardo si contano giorno per giorno: pochi valori diversi per molte righe.
	late := map[time.Time]int{}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var x Summary
		err := r.Scan(&x.ID, &x.Key, &x.Title, &x.TypeKey, &x.TypeName, &x.Scope, &x.Mandatory, &x.Team, &x.Stage,
			&x.State, &x.StateCategory, &x.RequestKey, &x.RequestTitle, &x.ImpactKey, &x.Application,
			&x.DueOn, &x.Paused, &x.OutcomeLabel, &x.OutcomePolarity, &x.RequiredTotal, &x.RequiredMissing,
			&x.Superseded, &x.CreatedAt)
		if x.DueOn != nil {
			d := x.DueOn.Format(time.DateOnly)
			x.Due = &d
			if x.Stage == StageSubmitted || x.Stage == StageExam {
				n, ok := late[*x.DueOn]
				if !ok {
					n = lateDays(*x.DueOn, today)
					late[*x.DueOn] = n
				}
				x.LateDays = n
			}
		}
		return x, err
	})
}

// TypeOption è un tipo che l'attore può aprire a mano.
type TypeOption struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Team      string `json:"team"`
	Mandatory bool   `json:"mandatory"`
}

// ImpactOption è un impatto della richiesta (per aprire una valutazione per applicazione).
type ImpactOption struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Application string `json:"application"`
}

// ElementReviews è la sezione "Valutazioni" del dettaglio di una richiesta o di un impatto.
type ElementReviews struct {
	Items   []Summary      `json:"items"`
	Types   []TypeOption   `json:"types"`   // tipi apribili a mano dall'attore
	Impacts []ImpactOption `json:"impacts"` // per i tipi con ambito impatto (solo sulla richiesta)
	Facts   map[string]any `json:"facts"`   // conteggi per i gate
}

// ForElement elenca le valutazioni di una richiesta (comprese quelle dei suoi impatti) o
// di un impatto, con i tipi che l'attore può aprire.
func (s *Service) ForElement(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ElementReviews, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &ElementReviews{Items: []Summary{}, Types: []TypeOption{}, Impacts: []ImpactOption{}, Facts: map[string]any{}}
	var where string
	var req *engine.Element
	switch el.TypeKey {
	case RequestType:
		where, req = ` where x.request_id = $1`, el
	case ImpactType:
		where = ` where x.impact_id = $1`
		if req, err = s.eng.Get(ctx, tx, reader(actor.MemberID), deref(el.ParentID)); err != nil {
			return nil, err
		}
	default:
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, selectSummary+where+` order by x.superseded_by is not null, coalesce(ie.number, 0), rt.name, e.number desc`, el.ID)
	if err != nil {
		return nil, err
	}
	if out.Items, err = s.scanSummaries(rows); err != nil {
		return nil, err
	}
	f := engine.Facts{}
	col := "request_id"
	if el.TypeKey == ImpactType {
		col = "impact_id"
	}
	if err := s.counts(ctx, tx, col, el.ID, f); err != nil {
		return nil, err
	}
	for k, v := range f {
		if len(v) == 1 {
			out.Facts[k] = v[0]
		} else {
			out.Facts[k] = v
		}
	}
	if req.StateCategory == "done" || req.StateCategory == "cancelled" {
		return out, nil
	}
	types, err := s.Types(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	general, err := s.canOpen(ctx, tx, actor, req, nil)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.Archived || t.TemplateIDCu == nil || (el.TypeKey == ImpactType && t.Scope == "request") {
			continue
		}
		ok := general
		if !ok {
			if ok, err = inGroup(ctx, tx, t.GroupID, actor.MemberID); err != nil {
				return nil, err
			}
		}
		if ok {
			out.Types = append(out.Types, TypeOption{Key: t.Key, Name: t.Name, Scope: t.Scope, Team: t.Team, Mandatory: t.Mandatory})
		}
	}
	if el.TypeKey == RequestType {
		rows, err := tx.Query(ctx, `select e.id, s.key || '-' || e.number, ap.title from ext_impact x
			join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
			where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled' order by e.number`, el.ID)
		if err != nil {
			return nil, err
		}
		if out.Impacts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ImpactOption]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Inbox sono le valutazioni che riguardano l'attore: da compilare (chi avvia il
// questionario o ha una sezione assegnata) e da esaminare (il suo team), per scadenza.
type Inbox struct {
	Compile []Summary `json:"compile"`
	Team    []Summary `json:"team"`
	// More: la coda ha altre voci oltre le prime InboxLimit (le più urgenti).
	More bool `json:"more"`
}

// InboxLimit: voci per coda se il client non chiede altro. Con 1.200 valutazioni in coda la
// risposta pesava 800 KB e 110 ms (benchmark 2026-11-24): si mostrano le più urgenti e
// l'interfaccia chiede il resto (limit) quando More è vero. InboxMax è il tetto della richiesta.
const (
	InboxLimit = 300
	InboxMax   = 5000
)

func (s *Service) Inbox(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Inbox, error) {
	return s.InboxN(ctx, tx, actor, InboxLimit)
}

// InboxN è Inbox con il numero di voci per coda (0 = InboxLimit).
func (s *Service) InboxN(ctx context.Context, tx pgx.Tx, actor engine.Actor, limit int) (*Inbox, error) {
	out := &Inbox{Compile: []Summary{}, Team: []Summary{}}
	if actor.MemberID == "" {
		return out, nil
	}
	if limit <= 0 {
		limit = InboxLimit
	}
	limit = min(limit, InboxMax)
	var more bool
	var err error
	// Da compilare: questionari avviati o con una sezione assegnata (l'indice delle code è nella migrazione 0043).
	out.Compile, more, err = s.inboxQueue(ctx, tx, limit, `x.stage in ('compile','clarification')
		  and (exists (select 1 from questionnaire_assignments qa where qa.review_id = x.element_id and qa.member_id = $1)
		       or exists (select 1 from stakeholders sh join stakeholder_roles ro on ro.tenant_id = sh.tenant_id and ro.id = sh.role_id
		                  where sh.element_id = x.element_id and ro.key = 'review_owner'
		                    and (sh.member_id = $1 or sh.group_id in (select member_groups($1)))))`, actor.MemberID)
	if err != nil {
		return nil, err
	}
	out.More = more
	// Da esaminare: i team di cui l'attore è responsabile o membro del gruppo. Senza team niente query.
	rows, err := tx.Query(ctx, `select t.id::text from review_teams t
		where t.lead_id = $1 or t.group_id in (select member_groups($1))`, actor.MemberID)
	if err != nil {
		return nil, err
	}
	teams, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(teams) > 0 {
		out.Team, more, err = s.inboxQueue(ctx, tx, limit, `x.stage in ('submitted','exam','clarification') and x.team_id = any($1::uuid[])`, teams)
		if err != nil {
			return nil, err
		}
		out.More = out.More || more
	}
	for i := range out.Compile {
		out.Compile[i].Role = "compile"
	}
	for i := range out.Team {
		out.Team[i].Role = "team"
	}
	return out, nil
}

// inboxQueue legge una coda: prima gli id delle più urgenti (solo ext_review, ordinata come
// l'indice delle code), poi il dettaglio con i join per quelle sole. Per scadenza, poi per
// creazione (gli id degli elementi sono ordinati nel tempo).
func (s *Service) inboxQueue(ctx context.Context, tx pgx.Tx, limit int, where string, args ...any) ([]Summary, bool, error) {
	rows, err := tx.Query(ctx, `select x.element_id::text from ext_review x
		where x.superseded_by is null and `+where+`
		  and exists (select 1 from elements e where e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null)
		order by x.due_on nulls last, x.element_id limit `+fmt.Sprint(limit+1), args...)
	if err != nil {
		return nil, false, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return []Summary{}, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	rows, err = tx.Query(ctx, selectSummary+` where x.element_id = any($1::uuid[]) order by x.due_on nulls last, x.element_id`, ids)
	if err != nil {
		return nil, false, err
	}
	out, err := s.scanSummaries(rows)
	if out == nil {
		out = []Summary{}
	}
	return out, more, err
}
