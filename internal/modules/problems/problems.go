// Package problems è «dal problema all'azione» (replica/usabilita.md, proposta 5): da una
// vulnerabilità, un blocco del gate, un requisito non soddisfatto o un'aderenza mancante, crea il
// lavoro giusto nel team dell'applicazione senza riscrivere i dati del problema, e tiene il
// collegamento tracciato (tabella problem_links, relazione «raised_from» nel grafo).
//
// Eccezione, deroga, override e pull request di adozione restano nei loro moduli: qui si
// leggono per mostrare, dalla stessa riga del problema, tutto ciò che è stato fatto.
package problems

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
)

// Tipi di problema.
const (
	KindFinding     = "finding"
	KindGate        = "gate_decision"
	KindRequirement = "requirement"
	KindAdherence   = "adherence"

	// RelRaisedFrom: elemento di lavoro → applicazione o requisito da cui nasce.
	RelRaisedFrom = "raised_from"

	TaskType = "task"
)

// Kinds sono i tipi ammessi.
var Kinds = []string{KindFinding, KindGate, KindRequirement, KindAdherence}

// Actions sono le azioni che un collegamento può registrare.
var Actions = []string{"work_item", "exception", "override", "adoption_pr"}

// Service è il modulo.
type Service struct {
	eng *engine.Service
	Now func() time.Time
}

// New crea il servizio.
func New(eng *engine.Service) *Service { return &Service{eng: eng, Now: time.Now} }

// AppRef è l'applicazione del problema.
type AppRef struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// TeamRef è il team a cui va il lavoro.
type TeamRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	HasSpace bool   `json:"has_space"`
}

// Link è un collegamento del problema: lavoro creato, eccezione, deroga, override o pull request.
type Link struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	// Derived: letto da un altro modulo (eccezione, override, PR), non creato da qui.
	Derived bool       `json:"derived"`
	Ref     string     `json:"ref"`
	Label   string     `json:"label"`
	Status  string     `json:"status"`
	URL     string     `json:"url,omitempty"`
	At      *time.Time `json:"at"`
	By      *string    `json:"by"`
	// Element: per un elemento di lavoro, la sua chiave (apribile in /e/<chiave>).
	Element string `json:"element,omitempty"`
}

// Context è ciò che serve all'interfaccia per proporre le azioni su un problema.
type Context struct {
	Kind        string            `json:"kind"`
	Ref         string            `json:"ref"`
	Label       string            `json:"label"`
	Application *AppRef           `json:"application"`
	Team        *TeamRef          `json:"team"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	Reason      string            `json:"reason"`
	Extra       map[string]string `json:"extra"`
	Links       []Link            `json:"links"`
	Can         map[string]bool   `json:"can"`
}

type subject struct {
	label string
	app   string // id dell'applicazione
	team  string // team proposto
	extra map[string]string
	// elemento a cui collegare il lavoro oltre all'applicazione (il requisito)
	element string
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func orDash(lang, s string) string {
	if strings.TrimSpace(s) == "" {
		return i18n.T(lang, "problems.not_given")
	}
	return s
}

// resolve legge il problema e propone titolo, descrizione e motivo.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, actor engine.Actor, kind, ref, control string) (*subject, error) {
	lang := i18n.Lang(ctx)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, engine.Required("ref", "indica il problema")
	}
	sub := &subject{extra: map[string]string{}}
	switch kind {
	case KindFinding:
		var app, team *string
		var cve, sev, pkg, ver, fixed string
		var due *time.Time
		err := tx.QueryRow(ctx, `select f.application_element_id::text, f.team_id::text, coalesce(f.cve, f.rule_ref, ''), f.severity, coalesce(f.package, ''),
			coalesce(f.installed_version, ''), coalesce(f.fixed_in, ''), f.due_at from findings f where f.id::text = $1`, ref).Scan(&app, &team, &cve, &sev, &pkg, &ver, &fixed, &due)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("ref", "", "vulnerabilità inesistente")
		}
		if err != nil {
			return nil, err
		}
		if app == nil {
			return nil, engine.InvalidCode("validation.problems.no_application", "ref", "la vulnerabilità non ha un'applicazione")
		}
		sub.app = *app
		if team != nil {
			sub.team = *team
		}
		sub.label = strings.TrimSpace(cve + " · " + pkg)
		sub.extra["cve"], sub.extra["severity"] = cve, sev
		dueText := i18n.T(lang, "problems.none")
		if due != nil {
			dueText = due.Format(time.DateOnly)
		}
		sub.extra["due"] = dueText
		sub.extra["fixed_in"] = fixed
		sub.extra["package"], sub.extra["version"] = pkg, ver
	case KindGate:
		var app string
		var req *string
		var env, version, outcome string
		var reasons []byte
		err := tx.QueryRow(ctx, `select application_element_id::text, request_element_id::text, environment, coalesce(version, ''), outcome, reasons
			from gate_decisions where id::text = $1`, ref).Scan(&app, &req, &env, &version, &outcome, &reasons)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("ref", "", "decisione inesistente")
		}
		if err != nil {
			return nil, err
		}
		sub.app = app
		sub.label = env + " · " + orDash(lang, version)
		msgs, err := gateMessages(ctx, tx, ref)
		if err != nil {
			return nil, err
		}
		sub.extra["environment"], sub.extra["version"], sub.extra["outcome"] = env, version, outcome
		sub.extra["reasons"] = strings.Join(msgs, "; ")
		sub.extra["reasons_list"] = "- " + strings.Join(msgs, "\n- ")
	case KindRequirement:
		el, err := s.eng.Get(ctx, tx, actor, ref)
		if err != nil {
			return nil, err
		}
		var impact *string
		err = tx.QueryRow(ctx, `select impact_id::text from ext_requirement where element_id = $1`, el.ID).Scan(&impact)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("ref", "", "requisito inesistente")
		}
		if err != nil {
			return nil, err
		}
		sub.label = el.Key + " · " + el.Title
		sub.element = el.ID
		sub.extra["key"], sub.extra["title"] = el.Key, el.Title
		if impact != nil {
			var team *string
			if err := tx.QueryRow(ctx, `select application_id::text, team_id::text from ext_impact where element_id = $1`, *impact).Scan(&sub.app, &team); err != nil {
				return nil, err
			}
			if team != nil {
				sub.team = *team
			}
		}
	case KindAdherence:
		el, err := s.eng.Get(ctx, tx, actor, ref)
		if err != nil {
			return nil, err
		}
		sub.app = el.ID
		sub.label = el.Title
		sub.extra["controls"] = strings.TrimSpace(control)
	default:
		return nil, engine.InvalidValue("kind", "tipo di problema non ammesso (%s)", strings.Join(Kinds, ", "))
	}
	if sub.app != "" && sub.team == "" {
		_ = tx.QueryRow(ctx, `select owner_team_id::text from ext_application where element_id = $1`, sub.app).Scan(&sub.team)
	}
	return sub, nil
}

func gateMessages(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	rows, err := tx.Query(ctx, `select coalesce(r->>'label', r->>'rule', ''), coalesce(r->>'message', '')
		from gate_decisions d, jsonb_array_elements(d.reasons) r
		where d.id::text = $1 and r->>'status' in ('fail', 'warn')`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var label, msg string
		if err := rows.Scan(&label, &msg); err != nil {
			return nil, err
		}
		if msg != "" {
			out = append(out, msg)
		} else {
			out = append(out, label)
		}
	}
	return out, rows.Err()
}

// compose riempie i testi proposti, nella lingua di chi agisce.
func compose(ctx context.Context, kind string, sub *subject, app *AppRef) (title, body, reason string) {
	lang := i18n.Lang(ctx)
	name := ""
	if app != nil {
		name = app.Title
	}
	x := sub.extra
	switch kind {
	case KindFinding:
		p := i18n.P{"cve": x["cve"], "app": name, "severity": x["severity"], "package": x["package"], "version": x["version"], "fixed": orDash(lang, x["fixed_in"]), "due": x["due"]}
		return i18n.T(lang, "problems.finding.title", p), i18n.T(lang, "problems.finding.body", p), i18n.T(lang, "problems.finding.reason", p)
	case KindGate:
		p := i18n.P{"app": name, "environment": x["environment"], "version": orDash(lang, x["version"]), "reasons": x["reasons_list"]}
		r := i18n.P{"reasons": x["reasons"]}
		return i18n.T(lang, "problems.gate.title", p), i18n.T(lang, "problems.gate.body", p), i18n.T(lang, "problems.gate.reason", r)
	case KindRequirement:
		p := i18n.P{"key": x["key"], "title": x["title"]}
		return i18n.T(lang, "problems.requirement.title", p), i18n.T(lang, "problems.requirement.body", p), i18n.T(lang, "problems.requirement.reason", p)
	default:
		c := x["controls"]
		if c == "" {
			c = i18n.T(lang, "problems.not_given")
		}
		p := i18n.P{"app": name, "controls": c}
		return i18n.T(lang, "problems.adherence.title", p), i18n.T(lang, "problems.adherence.body", p), ""
	}
}

func (s *Service) appRef(ctx context.Context, tx pgx.Tx, id string) (*AppRef, error) {
	if id == "" {
		return nil, nil
	}
	var a AppRef
	err := tx.QueryRow(ctx, `select e.id::text, coalesce(s.key || '-' || e.number, ''), e.title from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id::text = $1 and e.deleted_at is null`, id).Scan(&a.ID, &a.Key, &a.Title)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func teamRef(ctx context.Context, tx pgx.Tx, id string) (*TeamRef, error) {
	if id == "" {
		return nil, nil
	}
	var t TeamRef
	err := tx.QueryRow(ctx, `select id::text, name, delivery_space_id is not null from teams where id::text = $1 and not archived`, id).Scan(&t.ID, &t.Name, &t.HasSpace)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Context propone le azioni su un problema e ne elenca i collegamenti.
func (s *Service) Context(ctx context.Context, tx pgx.Tx, actor engine.Actor, kind, ref, control string) (*Context, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	sub, err := s.resolve(ctx, tx, actor, kind, ref, control)
	if err != nil {
		return nil, err
	}
	app, err := s.appRef(ctx, tx, sub.app)
	if err != nil {
		return nil, err
	}
	team, err := teamRef(ctx, tx, sub.team)
	if err != nil {
		return nil, err
	}
	out := &Context{Kind: kind, Ref: strings.TrimSpace(ref), Label: sub.label, Application: app, Team: team, Extra: sub.extra,
		Can: map[string]bool{"work_item": actor.Has(rbac.ElementWrite)}}
	out.Title, out.Body, out.Reason = compose(ctx, kind, sub, app)
	if out.Links, err = s.Links(ctx, tx, kind, out.Ref, sub.app); err != nil {
		return nil, err
	}
	return out, nil
}

// WorkInput crea l'elemento di lavoro del problema.
type WorkInput struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Control    string `json:"control"`
	Title      string `json:"title"`
	Body       string `json:"body_text"`
	TeamID     string `json:"team_id"`
	AssigneeID string `json:"assignee_id"`
	Due        string `json:"due"`
}

// CreateWork crea un task nello spazio del team, lo collega all'applicazione (e al requisito) con
// «raised_from» e registra il collegamento. Non tocca i dati del problema.
func (s *Service) CreateWork(ctx context.Context, tx pgx.Tx, actor engine.Actor, in WorkInput) (*Link, error) {
	if !actor.Has(rbac.ElementWrite) {
		return nil, engine.ErrForbidden
	}
	sub, err := s.resolve(ctx, tx, actor, in.Kind, in.Ref, in.Control)
	if err != nil {
		return nil, err
	}
	app, err := s.appRef(ctx, tx, sub.app)
	if err != nil {
		return nil, err
	}
	teamID := strings.TrimSpace(in.TeamID)
	if teamID == "" {
		teamID = sub.team
	}
	if teamID == "" {
		return nil, engine.Required("team_id", "scegli il team a cui va il lavoro")
	}
	var spaceKey *string
	err = tx.QueryRow(ctx, `select s.key from teams t left join spaces s on s.tenant_id = t.tenant_id and s.id = t.delivery_space_id
		where t.id::text = $1 and not t.archived`, teamID).Scan(&spaceKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
	}
	if err != nil {
		return nil, err
	}
	if spaceKey == nil {
		// Un team senza spazio di lavoro lo riceve adesso (come alla creazione di un team nuovo).
		if _, err := work.EnsureTeamSpace(ctx, tx, actor, work.SpaceInput{TeamID: teamID}); err != nil {
			return nil, err
		}
		var key string
		if err := tx.QueryRow(ctx, `select s.key from teams t join spaces s on s.tenant_id = t.tenant_id and s.id = t.delivery_space_id where t.id::text = $1`, teamID).Scan(&key); err != nil {
			return nil, err
		}
		spaceKey = &key
	}
	title, body, _ := compose(ctx, in.Kind, sub, app)
	if t := strings.TrimSpace(in.Title); t != "" {
		title = t
	}
	if b := strings.TrimSpace(in.Body); b != "" {
		body = b
	}
	var due *time.Time
	if d := strings.TrimSpace(in.Due); d != "" {
		t, err := time.Parse(time.DateOnly, d)
		if err != nil {
			return nil, engine.DateInvalid("due", "data non valida (AAAA-MM-GG)")
		}
		due = &t
	}
	task, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: *spaceKey, TypeKey: TaskType, Title: clip(title, 500), BodyText: clip(body, 20000), AssigneeID: strings.TrimSpace(in.AssigneeID)})
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "assignee_id", "membro inesistente")
		}
		return nil, err
	}
	if due != nil {
		if _, err := tx.Exec(ctx, "update elements set due_at = $2 where id = $1", task.ID, due); err != nil {
			return nil, err
		}
	}
	if err := s.ensureRelation(ctx, tx); err != nil {
		return nil, err
	}
	for _, target := range []string{sub.app, sub.element} {
		if target == "" || target == task.ID {
			continue
		}
		if err := relate(ctx, tx, actor, task.ID, target); err != nil {
			return nil, err
		}
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into problem_links (tenant_id, id, source_kind, source_ref, source_label, application_id, action, target_element_id, target_ref, target_label, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, 'work_item', $6, $7, $8, $9)`,
		id, in.Kind, strings.TrimSpace(in.Ref), clip(sub.label, 300), nullable(sub.app), task.ID, task.Key, clip(task.Title, 500), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "problem.work_item", EntityType: "element", EntityID: task.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"kind": in.Kind, "ref": in.Ref}}); err != nil {
		return nil, err
	}
	now := s.Now()
	return &Link{ID: id, Action: "work_item", Ref: task.Key, Label: task.Title, Element: task.Key, Status: task.State, At: &now}, nil
}

// LinkInput registra un collegamento già creato altrove (la pull request di adozione).
type LinkInput struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Action string `json:"action"`
	Target string `json:"target_ref"`
	Label  string `json:"target_label"`
	Note   string `json:"note"`
}

// AddLink registra che un'azione svolta in un altro modulo (eccezione, override, PR) nasce da questo problema.
func (s *Service) AddLink(ctx context.Context, tx pgx.Tx, actor engine.Actor, in LinkInput) (*Link, error) {
	if !actor.Has(rbac.ElementWrite) {
		return nil, engine.ErrForbidden
	}
	if !slices.Contains(Actions, in.Action) || in.Action == "work_item" {
		return nil, engine.InvalidValue("action", "azione non ammessa (%s)", strings.Join(Actions[1:], ", "))
	}
	if strings.TrimSpace(in.Target) == "" {
		return nil, engine.Required("target_ref", "indica che cosa è stato creato")
	}
	sub, err := s.resolve(ctx, tx, actor, in.Kind, in.Ref, "")
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into problem_links (tenant_id, id, source_kind, source_ref, source_label, application_id, action, target_ref, target_label, note, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, in.Kind, strings.TrimSpace(in.Ref), clip(sub.label, 300), nullable(sub.app), in.Action, clip(in.Target, 500), clip(in.Label, 500), clip(in.Note, 2000), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	now := s.Now()
	return &Link{ID: id, Action: in.Action, Ref: in.Target, Label: in.Label, URL: urlOf(in.Target), At: &now}, nil
}

func urlOf(ref string) string {
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		return ref
	}
	return ""
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (s *Service) ensureRelation(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
		values (app_tenant(), $1, $2, 'nasce da', 'ha generato', true) on conflict do nothing`, db.NewID(), RelRaisedFrom)
	return err
}

func relate(ctx context.Context, tx pgx.Tx, actor engine.Actor, sourceID, targetID string) error {
	tag, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), $1, rt.id, $3, $4, $5 from relation_types rt where rt.key = $2 on conflict do nothing`,
		db.NewID(), RelRaisedFrom, sourceID, targetID, nullable(actor.MemberID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := engine.AddActivity(ctx, tx, sourceID, actor.MemberID, "relation", RelRaisedFrom, map[string]any{"target": targetID}, nil); err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, targetID, actor.MemberID, "relation", RelRaisedFrom+".inverse", map[string]any{"source": sourceID}, nil)
}

// Links elenca ciò che è stato fatto a partire dal problema: il lavoro creato da qui e, letti dagli
// altri moduli, eccezioni, deroghe, override e pull request di adozione.
func (s *Service) Links(ctx context.Context, tx pgx.Tx, kind, ref, appID string) ([]Link, error) {
	out := []Link{}
	rows, err := tx.Query(ctx, `select l.id::text, l.action, l.target_ref, l.target_label, coalesce(o.name, ''), coalesce(sp.key || '-' || e.number, ''), l.created_at, m.display_name
		from problem_links l
		left join elements e on e.tenant_id = l.tenant_id and e.id = l.target_element_id and e.deleted_at is null
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join members m on m.tenant_id = l.tenant_id and m.id = l.created_by
		where l.source_kind = $1 and l.source_ref = $2 order by l.created_at`, kind, ref)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l Link
		var key string
		if err := rows.Scan(&l.ID, &l.Action, &l.Ref, &l.Label, &l.Status, &key, &l.At, &l.By); err != nil {
			rows.Close()
			return nil, err
		}
		if key != "" {
			l.Element, l.Ref = key, key
		}
		l.URL = urlOf(l.Ref)
		out = append(out, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	derived, err := s.derived(ctx, tx, kind, ref, appID)
	if err != nil {
		return nil, err
	}
	// Un collegamento registrato e quello letto dal modulo possono descrivere la stessa cosa.
	for _, d := range derived {
		if !slices.ContainsFunc(out, func(l Link) bool { return l.Action == d.Action && l.Ref == d.Ref }) {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b Link) int {
		switch {
		case a.At == nil || b.At == nil:
			return 0
		case a.At.Before(*b.At):
			return -1
		case a.At.After(*b.At):
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *Service) derived(ctx context.Context, tx pgx.Tx, kind, ref, appID string) ([]Link, error) {
	var q string
	var args []any
	switch kind {
	case KindFinding:
		q = `select x.id::text, 'exception', 'EV-' || x.number, x.reason, x.status, '', x.requested_at, m.display_name
			from vuln_exceptions x join vuln_exception_findings vf on vf.tenant_id = x.tenant_id and vf.exception_id = x.id
			left join members m on m.tenant_id = x.tenant_id and m.id = x.requested_by
			where vf.finding_id::text = $1`
		args = []any{ref}
	case KindGate:
		q = `select o.id::text, 'override', 'override', o.reason, o.status, '', o.requested_at, m.display_name
			from gate_overrides o left join members m on m.tenant_id = o.tenant_id and m.id = o.requested_by
			where o.decision_id::text = $1`
		args = []any{ref}
	case KindRequirement:
		q = `select e.element_id::text, 'exception', coalesce(sp.key || '-' || el.number, ''), el.title, e.stage, '', e.created_at, m.display_name
			from ext_exception e join elements el on el.tenant_id = e.tenant_id and el.id = e.element_id and el.deleted_at is null
			join spaces sp on sp.tenant_id = el.tenant_id and sp.id = el.space_id
			left join members m on m.tenant_id = e.tenant_id and m.id = e.requested_by
			join elements rq on rq.tenant_id = e.tenant_id and rq.id = e.requirement_id
			join spaces rs on rs.tenant_id = rq.tenant_id and rs.id = rq.space_id
			where rs.key || '-' || rq.number = $1 or rq.id::text = $1`
		args = []any{ref}
	case KindAdherence:
		if appID == "" {
			return nil, nil
		}
		q = `select a.id::text, 'adoption_pr', a.pr_url, a.title, a.state, a.pr_url, a.created_at, m.display_name
			from pipeline_adoptions a left join members m on m.tenant_id = a.tenant_id and m.id = a.created_by
			where a.application_element_id::text = $1`
		args = []any{appID}
	default:
		return nil, nil
	}
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l := Link{Derived: true}
		var key string
		if err := rows.Scan(&l.ID, &l.Action, &key, &l.Label, &l.Status, &l.URL, &l.At, &l.By); err != nil {
			return nil, err
		}
		l.Ref = key
		if kind == KindRequirement {
			l.Element = key
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ElementLinks elenca i collegamenti dal lato del lavoro: da quali problemi nasce l'elemento.
type Origin struct {
	ID    string     `json:"id"`
	Kind  string     `json:"kind"`
	Ref   string     `json:"ref"`
	Label string     `json:"label"`
	At    *time.Time `json:"at"`
	By    *string    `json:"by"`
	// Application: l'applicazione del problema (chiave e nome).
	Application *AppRef `json:"application"`
}

// Origins elenca i problemi da cui nasce un elemento di lavoro.
func (s *Service) Origins(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]Origin, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select l.id::text, l.source_kind, l.source_ref, l.source_label, l.created_at, m.display_name,
		a.id::text, coalesce(sp.key || '-' || a.number, ''), a.title
		from problem_links l left join members m on m.tenant_id = l.tenant_id and m.id = l.created_by
		left join elements a on a.tenant_id = l.tenant_id and a.id = l.application_id and a.deleted_at is null
		left join spaces sp on sp.tenant_id = a.tenant_id and sp.id = a.space_id
		where l.target_element_id = $1 order by l.created_at`, el.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Origin{}
	for rows.Next() {
		var o Origin
		var aid, akey, atitle *string
		if err := rows.Scan(&o.ID, &o.Kind, &o.Ref, &o.Label, &o.At, &o.By, &aid, &akey, &atitle); err != nil {
			return nil, err
		}
		if aid != nil {
			o.Application = &AppRef{ID: *aid, Key: *akey, Title: *atitle}
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
