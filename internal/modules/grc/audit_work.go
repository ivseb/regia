package grc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
)

// Spazio dell'auditor, parte 2: richieste di evidenza, commenti in linea e rilievi. Ognuno avvisa
// la controparte con le notifiche di piattaforma (tipo «audit», sul controllo).

// controlInfo: chiave, titolo e responsabile di un controllo.
func controlInfo(ctx context.Context, tx pgx.Tx, id string) (key, title, owner string, err error) {
	err = tx.QueryRow(ctx, `select sp.key || '-' || e.number, e.title, coalesce(x.owner_id::text, '') from ext_control x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where x.element_id = $1`, id).Scan(&key, &title, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		err = engine.ErrNotFound
	}
	return
}

func (s *Service) notifyAudit(ctx context.Context, tx pgx.Tx, actor engine.Actor, controlID string, members []string, payload map[string]any) error {
	var to []string
	for _, m := range members {
		if m != "" && m != actor.MemberID {
			to = append(to, m)
		}
	}
	if len(to) == 0 {
		return nil
	}
	_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindAudit, ElementID: controlID, ActorID: actor.MemberID, Members: to, Payload: payload})
	return err
}

// canRespond: il responsabile del controllo o chi amministra la conformità.
func canRespond(actor engine.Actor, owner string) bool {
	return actor.Has(PermAdmin) || (owner != "" && owner == actor.MemberID)
}

// evidenceRef risolve un'evidenza per chiave o id.
func (s *Service) evidenceRef(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	el, err := s.eng.Get(ctx, tx, sysActor, ref)
	if err != nil {
		return "", engine.InvalidCode("validation.audit.evidence_not_found", "evidence", "evidenza inesistente")
	}
	if el.TypeKey != EvidenceType {
		return "", engine.InvalidCode("validation.audit.evidence_not_found", "evidence", "evidenza inesistente")
	}
	return el.ID, nil
}

// ---------------------------------------------------------------------------
// Richieste di evidenza

// Request è una richiesta di evidenza dell'auditor al responsabile del controllo.
type Request struct {
	ID          string     `json:"id"`
	Engagement  string     `json:"engagement"`
	Control     Brief      `json:"control"`
	Evidence    *Brief     `json:"evidence"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Due         string     `json:"due"`
	Overdue     bool       `json:"overdue"`
	AssigneeID  string     `json:"assignee_id"`
	Assignee    string     `json:"assignee"`
	Status      string     `json:"status"`
	Response    string     `json:"response"`
	ResponseURL string     `json:"response_url"`
	ResponseEv  *Brief     `json:"response_evidence"`
	RequestedBy string     `json:"requested_by"`
	RequestedAt time.Time  `json:"requested_at"`
	AnsweredBy  string     `json:"answered_by"`
	AnsweredAt  *time.Time `json:"answered_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	CanAnswer   bool       `json:"can_answer"`
	CanClose    bool       `json:"can_close"`
}

// RequestInput apre una richiesta (Assignee vuoto = il responsabile del controllo).
type RequestInput struct {
	Control  string `json:"control"`
	Evidence string `json:"evidence"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Due      string `json:"due"`
	Assignee string `json:"assignee"`
}

const requestSelect = `select q.id::text, q.engagement_id::text, c.id::text, sp.key || '-' || c.number, c.title,
	ev.id::text, coalesce(esp.key || '-' || ev.number, ''), coalesce(ev.title, ''),
	q.title, q.body, q.due_date, coalesce(q.assignee_id::text, ''), coalesce(am.display_name, ''), q.status, q.response, coalesce(q.response_url, ''),
	rv.id::text, coalesce(rsp.key || '-' || rv.number, ''), coalesce(rv.title, ''),
	coalesce(rm.display_name, ''), q.requested_at, coalesce(nm.display_name, ''), q.answered_at, q.closed_at, coalesce(x.owner_id::text, '')
	from grc_audit_requests q
	join elements c on c.tenant_id = q.tenant_id and c.id = q.control_element_id
	join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id
	join ext_control x on x.tenant_id = c.tenant_id and x.element_id = c.id
	left join elements ev on ev.tenant_id = q.tenant_id and ev.id = q.evidence_element_id
	left join spaces esp on esp.tenant_id = ev.tenant_id and esp.id = ev.space_id
	left join elements rv on rv.tenant_id = q.tenant_id and rv.id = q.response_evidence_id
	left join spaces rsp on rsp.tenant_id = rv.tenant_id and rsp.id = rv.space_id
	left join members am on am.tenant_id = q.tenant_id and am.id = q.assignee_id
	left join members rm on rm.tenant_id = q.tenant_id and rm.id = q.requested_by
	left join members nm on nm.tenant_id = q.tenant_id and nm.id = q.answered_by`

func (s *Service) requests(ctx context.Context, tx pgx.Tx, actor engine.Actor, where string, args ...any) ([]Request, error) {
	rows, err := tx.Query(ctx, requestSelect+" "+where+" order by (q.status = 'open') desc, q.due_date nulls last, q.requested_at desc", args...)
	if err != nil {
		return nil, err
	}
	out := []Request{}
	var q Request
	var evID, evKey, evTitle, rvID, rvKey, rvTitle, owner *string
	var due *time.Time
	if _, err := pgx.ForEachRow(rows, []any{&q.ID, &q.Engagement, &q.Control.ID, &q.Control.Key, &q.Control.Title, &evID, &evKey, &evTitle,
		&q.Title, &q.Body, &due, &q.AssigneeID, &q.Assignee, &q.Status, &q.Response, &q.ResponseURL, &rvID, &rvKey, &rvTitle,
		&q.RequestedBy, &q.RequestedAt, &q.AnsweredBy, &q.AnsweredAt, &q.ClosedAt, &owner}, func() error {
		x := q
		x.Evidence, x.ResponseEv, x.Due = nil, nil, ""
		if evID != nil {
			x.Evidence = &Brief{ID: *evID, Key: *evKey, Title: *evTitle}
		}
		if rvID != nil {
			x.ResponseEv = &Brief{ID: *rvID, Key: *rvKey, Title: *rvTitle}
		}
		if due != nil {
			x.Due = due.Format(time.DateOnly)
			x.Overdue = x.Status == "open" && due.Before(time.Now().Truncate(24*time.Hour))
		}
		ow := ""
		if owner != nil {
			ow = *owner
		}
		x.CanAnswer = x.Status == "open" && (canRespond(actor, ow) || (x.AssigneeID != "" && x.AssigneeID == actor.MemberID))
		x.CanClose = actor.Has(PermAudit) && x.Status != "closed"
		out = append(out, x)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateRequest apre una richiesta di evidenza al responsabile del controllo.
func (s *Service) CreateRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, engID string, in RequestInput) (*Request, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	e, err := s.requireEngagement(ctx, tx, engID, true)
	if err != nil {
		return nil, err
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	if len(in.Title) > 200 || len(in.Body) > 10000 {
		return nil, engine.InvalidCode("validation.too_long", "title", "testo troppo lungo")
	}
	cid, err := s.inScope(ctx, tx, e, in.Control)
	if err != nil {
		return nil, err
	}
	var due *time.Time
	if due, err = parseDay("due", in.Due); err != nil {
		return nil, err
	}
	key, _, owner, err := controlInfo(ctx, tx, cid)
	if err != nil {
		return nil, err
	}
	assignee := owner
	if in.Assignee != "" {
		assignee = in.Assignee
	}
	if assignee == "" {
		return nil, engine.InvalidCode("validation.audit.no_assignee", "assignee", "il controllo non ha un responsabile: indica a chi chiedere")
	}
	var evID any
	if in.Evidence != "" {
		id, err := s.evidenceRef(ctx, tx, in.Evidence)
		if err != nil {
			return nil, err
		}
		evID = id
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into grc_audit_requests (tenant_id, engagement_id, control_element_id, evidence_element_id, title, body, due_date, assignee_id, requested_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8) returning id::text`,
		engID, cid, evID, in.Title, strings.TrimSpace(in.Body), due, assignee, nullable(actor.MemberID)).Scan(&id); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "assignee", "membro inesistente")
		}
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.request.create", "grc_audit_request", id, map[string]any{"engagement": engID, "control": key, "title": in.Title, "due": in.Due}); err != nil {
		return nil, err
	}
	if err := s.notifyAudit(ctx, tx, actor, cid, []string{assignee}, map[string]any{"event": "request_opened", "title": in.Title, "due": in.Due, "request": id}); err != nil {
		return nil, err
	}
	return s.oneRequest(ctx, tx, actor, id)
}

func (s *Service) oneRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Request, error) {
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	l, err := s.requests(ctx, tx, actor, "where q.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, engine.ErrNotFound
	}
	return &l[0], nil
}

// AnswerInput è la risposta del responsabile: un testo, un link e/o un'evidenza già caricata.
type AnswerInput struct {
	Response string `json:"response"`
	URL      string `json:"url"`
	Evidence string `json:"evidence"`
}

// AnswerRequest risponde a una richiesta (il destinatario, il responsabile o la compliance).
func (s *Service) AnswerRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in AnswerInput) (*Request, error) {
	q, err := s.oneRequest(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	var owner string
	if _, _, owner, err = controlInfo(ctx, tx, q.Control.ID); err != nil {
		return nil, err
	}
	if !canRespond(actor, owner) && !(q.AssigneeID != "" && q.AssigneeID == actor.MemberID) {
		return nil, engine.ErrForbidden
	}
	if q.Status != "open" {
		return nil, engine.InvalidCode("validation.audit.request_not_open", "request", "la richiesta non è più aperta")
	}
	in.Response, in.URL = strings.TrimSpace(in.Response), strings.TrimSpace(in.URL)
	if len(in.Response) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "response", "risposta troppo lunga")
	}
	if in.URL != "" {
		if err := checkURL(in.URL); err != nil {
			return nil, err
		}
	}
	var evID any
	if in.Evidence != "" {
		eid, err := s.evidenceRef(ctx, tx, in.Evidence)
		if err != nil {
			return nil, err
		}
		evID = eid
	}
	if in.Response == "" && in.URL == "" && evID == nil {
		return nil, engine.InvalidCode("validation.audit.response_required", "response", "scrivi la risposta o indica un link o un'evidenza")
	}
	if _, err := tx.Exec(ctx, `update grc_audit_requests set status = 'answered', response = $2, response_url = $3, response_evidence_id = $4,
		answered_by = $5, answered_at = now() where id = $1`, id, in.Response, nullable(in.URL), evID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.request.answer", "grc_audit_request", id, map[string]any{"control": q.Control.Key}); err != nil {
		return nil, err
	}
	var requester string
	_ = tx.QueryRow(ctx, "select coalesce(requested_by::text, '') from grc_audit_requests where id = $1", id).Scan(&requester)
	if err := s.notifyAudit(ctx, tx, actor, q.Control.ID, []string{requester}, map[string]any{"event": "request_answered", "title": q.Title, "request": id}); err != nil {
		return nil, err
	}
	return s.oneRequest(ctx, tx, actor, id)
}

// CloseRequest chiude la richiesta (l'auditor ha ricevuto quanto chiedeva, o rinuncia).
func (s *Service) CloseRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Request, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	q, err := s.oneRequest(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if q.Status == "closed" {
		return nil, engine.InvalidCode("validation.audit.request_not_open", "request", "la richiesta è già chiusa")
	}
	if _, err := tx.Exec(ctx, "update grc_audit_requests set status = 'closed', closed_at = now() where id = $1", id); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.request.close", "grc_audit_request", id, map[string]any{"control": q.Control.Key}); err != nil {
		return nil, err
	}
	if err := s.notifyAudit(ctx, tx, actor, q.Control.ID, []string{q.AssigneeID}, map[string]any{"event": "request_closed", "title": q.Title, "request": id}); err != nil {
		return nil, err
	}
	return s.oneRequest(ctx, tx, actor, id)
}

// MyRequests sono le richieste aperte rivolte all'attore.
func (s *Service) MyRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Request, error) {
	if actor.MemberID == "" {
		return []Request{}, nil
	}
	return s.requests(ctx, tx, actor, "where q.assignee_id = $1 and q.status = 'open'", actor.MemberID)
}

// ---------------------------------------------------------------------------
// Commenti in linea

// AuditComment è un commento sul controllo o su una sua evidenza.
type AuditComment struct {
	ID        string    `json:"id"`
	Evidence  *Brief    `json:"evidence"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

// CommentInput è un commento (Evidence facoltativa, Engagement facoltativo).
type CommentInput struct {
	Body       string `json:"body"`
	Evidence   string `json:"evidence"`
	Engagement string `json:"engagement"`
}

func (s *Service) comments(ctx context.Context, tx pgx.Tx, controlID string) ([]AuditComment, error) {
	rows, err := tx.Query(ctx, `select k.id::text, ev.id::text, coalesce(esp.key || '-' || ev.number, ''), coalesce(ev.title, ''), k.body,
		coalesce(m.display_name, ''), coalesce(k.author_id::text, ''), k.created_at
		from grc_audit_comments k left join elements ev on ev.tenant_id = k.tenant_id and ev.id = k.evidence_element_id
		left join spaces esp on esp.tenant_id = ev.tenant_id and esp.id = ev.space_id
		left join members m on m.tenant_id = k.tenant_id and m.id = k.author_id
		where k.control_element_id = $1 order by k.created_at`, controlID)
	if err != nil {
		return nil, err
	}
	out := []AuditComment{}
	var c AuditComment
	var evID, evKey, evTitle *string
	_, err = pgx.ForEachRow(rows, []any{&c.ID, &evID, &evKey, &evTitle, &c.Body, &c.Author, &c.AuthorID, &c.CreatedAt}, func() error {
		x := c
		x.Evidence = nil
		if evID != nil {
			x.Evidence = &Brief{ID: *evID, Key: *evKey, Title: *evTitle}
		}
		out = append(out, x)
		return nil
	})
	return out, err
}

// AddComment scrive un commento sul controllo: l'auditor, il responsabile o la compliance.
func (s *Service) AddComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in CommentInput) ([]AuditComment, error) {
	cid, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	key, _, owner, err := controlInfo(ctx, tx, cid)
	if err != nil {
		return nil, err
	}
	if !actor.Has(PermAudit) && !canRespond(actor, owner) {
		return nil, engine.ErrForbidden
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return nil, engine.InvalidCode("validation.required", "body", "scrivi il commento")
	}
	if len(in.Body) > 4000 {
		return nil, engine.InvalidCode("validation.too_long", "body", "commento troppo lungo")
	}
	var evID, engID any
	if in.Evidence != "" {
		id, err := s.evidenceRef(ctx, tx, in.Evidence)
		if err != nil {
			return nil, err
		}
		evID = id
	}
	if in.Engagement != "" {
		if _, err := s.requireEngagement(ctx, tx, in.Engagement, false); err != nil {
			return nil, err
		}
		engID = in.Engagement
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into grc_audit_comments (tenant_id, control_element_id, evidence_element_id, engagement_id, body, author_id)
		values (app_tenant(), $1, $2, $3, $4, $5) returning id::text`, cid, evID, engID, in.Body, nullable(actor.MemberID)).Scan(&id); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.comment", "grc_audit_comment", id, map[string]any{"control": key}); err != nil {
		return nil, err
	}
	// Avvisa l'altra parte: il responsabile se scrive l'auditor, altrimenti chi ha già scritto o chiesto sul controllo.
	to := []string{}
	if actor.Has(PermAudit) {
		to = append(to, owner)
	} else {
		rows, err := tx.Query(ctx, `select distinct x from (
			select author_id::text x from grc_audit_comments where control_element_id = $1
			union select requested_by::text from grc_audit_requests where control_element_id = $1
			union select opened_by::text from grc_audit_findings where control_element_id = $1) u where x is not null`, cid)
		if err != nil {
			return nil, err
		}
		var m string
		if _, err := pgx.ForEachRow(rows, []any{&m}, func() error { to = append(to, m); return nil }); err != nil {
			return nil, err
		}
	}
	if err := s.notifyAudit(ctx, tx, actor, cid, to, map[string]any{"event": "comment"}); err != nil {
		return nil, err
	}
	return s.comments(ctx, tx, cid)
}

// ---------------------------------------------------------------------------
// Rilievi

// Finding è un rilievo dell'auditor su un controllo.
type Finding struct {
	ID          string     `json:"id"`
	Engagement  string     `json:"engagement"`
	Control     Brief      `json:"control"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"` // open | plan_proposed | closed
	ActionPlan  string     `json:"action_plan"`
	ActionDue   string     `json:"action_due"`
	PlanBy      string     `json:"plan_by"`
	PlanAt      *time.Time `json:"plan_at"`
	ClosureNote string     `json:"closure_note"`
	OpenedBy    string     `json:"opened_by"`
	OpenedAt    time.Time  `json:"opened_at"`
	ClosedBy    string     `json:"closed_by"`
	ClosedAt    *time.Time `json:"closed_at"`
	CanPlan     bool       `json:"can_plan"`
	CanClose    bool       `json:"can_close"`
}

// FindingInput apre un rilievo.
type FindingInput struct {
	Control     string `json:"control"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

const findingSelect = `select f.id::text, f.engagement_id::text, c.id::text, sp.key || '-' || c.number, c.title, f.severity, f.title, f.description, f.status,
	f.action_plan, f.action_due, coalesce(pm.display_name, ''), f.plan_at, f.closure_note, coalesce(om.display_name, ''), f.opened_at,
	coalesce(cm.display_name, ''), f.closed_at, coalesce(x.owner_id::text, '')
	from grc_audit_findings f join elements c on c.tenant_id = f.tenant_id and c.id = f.control_element_id
	join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id
	join ext_control x on x.tenant_id = c.tenant_id and x.element_id = c.id
	left join members pm on pm.tenant_id = f.tenant_id and pm.id = f.plan_by
	left join members om on om.tenant_id = f.tenant_id and om.id = f.opened_by
	left join members cm on cm.tenant_id = f.tenant_id and cm.id = f.closed_by`

func (s *Service) findings(ctx context.Context, tx pgx.Tx, actor engine.Actor, where string, args ...any) ([]Finding, error) {
	rows, err := tx.Query(ctx, findingSelect+" "+where+` order by (f.status = 'closed'),
		array_position(array['critical','high','medium','low'], f.severity), f.opened_at desc`, args...)
	if err != nil {
		return nil, err
	}
	out := []Finding{}
	var f Finding
	var due *time.Time
	var owner string
	if _, err := pgx.ForEachRow(rows, []any{&f.ID, &f.Engagement, &f.Control.ID, &f.Control.Key, &f.Control.Title, &f.Severity, &f.Title, &f.Description, &f.Status,
		&f.ActionPlan, &due, &f.PlanBy, &f.PlanAt, &f.ClosureNote, &f.OpenedBy, &f.OpenedAt, &f.ClosedBy, &f.ClosedAt, &owner}, func() error {
		x := f
		x.ActionDue = ""
		if due != nil {
			x.ActionDue = due.Format(time.DateOnly)
		}
		x.CanPlan = x.Status != "closed" && canRespond(actor, owner)
		x.CanClose = x.Status != "closed" && actor.Has(PermAudit)
		out = append(out, x)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) oneFinding(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Finding, error) {
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	l, err := s.findings(ctx, tx, actor, "where f.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, engine.ErrNotFound
	}
	return &l[0], nil
}

// OpenFinding apre un rilievo su un controllo del perimetro.
func (s *Service) OpenFinding(ctx context.Context, tx pgx.Tx, actor engine.Actor, engID string, in FindingInput) (*Finding, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	e, err := s.requireEngagement(ctx, tx, engID, true)
	if err != nil {
		return nil, err
	}
	switch in.Severity {
	case "low", "medium", "high", "critical":
	default:
		return nil, engine.InvalidCode("validation.audit.severity_invalid", "severity", "gravità non ammessa (low, medium, high, critical)")
	}
	in.Title, in.Description = strings.TrimSpace(in.Title), strings.TrimSpace(in.Description)
	if in.Title == "" {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	if len(in.Title) > 200 || len(in.Description) > 10000 {
		return nil, engine.InvalidCode("validation.too_long", "title", "testo troppo lungo")
	}
	cid, err := s.inScope(ctx, tx, e, in.Control)
	if err != nil {
		return nil, err
	}
	_, _, owner, err := controlInfo(ctx, tx, cid)
	if err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into grc_audit_findings (tenant_id, engagement_id, control_element_id, severity, title, description, opened_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6) returning id::text`, engID, cid, in.Severity, in.Title, in.Description, nullable(actor.MemberID)).Scan(&id); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.finding.open", "grc_audit_finding", id, map[string]any{"engagement": engID, "control": in.Control, "severity": in.Severity, "title": in.Title}); err != nil {
		return nil, err
	}
	if err := s.notifyAudit(ctx, tx, actor, cid, []string{owner}, map[string]any{"event": "finding_opened", "title": in.Title, "severity": in.Severity, "finding": id}); err != nil {
		return nil, err
	}
	return s.oneFinding(ctx, tx, actor, id)
}

// PlanInput è il piano d'azione del responsabile.
type PlanInput struct {
	Plan string `json:"action_plan"`
	Due  string `json:"action_due"`
}

// ProposePlan registra il piano d'azione del rilievo (responsabile del controllo o compliance).
func (s *Service) ProposePlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in PlanInput) (*Finding, error) {
	f, err := s.oneFinding(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if !f.CanPlan {
		if f.Status == "closed" {
			return nil, engine.InvalidCode("validation.audit.finding_closed", "finding", "il rilievo è già chiuso")
		}
		return nil, engine.ErrForbidden
	}
	in.Plan = strings.TrimSpace(in.Plan)
	if in.Plan == "" {
		return nil, engine.InvalidCode("validation.audit.plan_required", "action_plan", "scrivi il piano d'azione")
	}
	if len(in.Plan) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "action_plan", "piano troppo lungo")
	}
	due, err := parseDay("action_due", in.Due)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update grc_audit_findings set status = 'plan_proposed', action_plan = $2, action_due = $3, plan_by = $4, plan_at = now() where id = $1`,
		id, in.Plan, due, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.finding.plan", "grc_audit_finding", id, map[string]any{"control": f.Control.Key, "due": in.Due}); err != nil {
		return nil, err
	}
	var opener string
	_ = tx.QueryRow(ctx, "select coalesce(opened_by::text, '') from grc_audit_findings where id = $1", id).Scan(&opener)
	if err := s.notifyAudit(ctx, tx, actor, f.Control.ID, []string{opener}, map[string]any{"event": "finding_plan", "title": f.Title, "finding": id}); err != nil {
		return nil, err
	}
	return s.oneFinding(ctx, tx, actor, id)
}

// CloseFinding chiude il rilievo (solo l'auditor), con una nota.
func (s *Service) CloseFinding(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, note string) (*Finding, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	f, err := s.oneFinding(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if f.Status == "closed" {
		return nil, engine.InvalidCode("validation.audit.finding_closed", "finding", "il rilievo è già chiuso")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, engine.InvalidCode("validation.audit.note_required", "note", "scrivi perché chiudi il rilievo")
	}
	if len(note) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	if _, err := tx.Exec(ctx, `update grc_audit_findings set status = 'closed', closure_note = $2, closed_by = $3, closed_at = now() where id = $1`, id, note, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.finding.close", "grc_audit_finding", id, map[string]any{"control": f.Control.Key}); err != nil {
		return nil, err
	}
	_, _, owner, err := controlInfo(ctx, tx, f.Control.ID)
	if err != nil {
		return nil, err
	}
	var planner string
	_ = tx.QueryRow(ctx, "select coalesce(plan_by::text, '') from grc_audit_findings where id = $1", id).Scan(&planner)
	if err := s.notifyAudit(ctx, tx, actor, f.Control.ID, []string{owner, planner}, map[string]any{"event": "finding_closed", "title": f.Title, "finding": id}); err != nil {
		return nil, err
	}
	return s.oneFinding(ctx, tx, actor, id)
}

// Findings elenca i rilievi di un incarico.
func (s *Service) Findings(ctx context.Context, tx pgx.Tx, actor engine.Actor, engID string) ([]Finding, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(engID) {
		return nil, engine.ErrNotFound
	}
	return s.findings(ctx, tx, actor, "where f.engagement_id = $1", engID)
}

// EngagementRequests elenca le richieste di un incarico.
func (s *Service) EngagementRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor, engID string) ([]Request, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(engID) {
		return nil, engine.ErrNotFound
	}
	return s.requests(ctx, tx, actor, "where q.engagement_id = $1", engID)
}

// ---------------------------------------------------------------------------
// Scheda del controllo

// ControlAudit è ciò che l'audit ha scritto sul controllo: richieste, commenti, rilievi e campioni.
type ControlAudit struct {
	Requests []Request      `json:"requests"`
	Comments []AuditComment `json:"comments"`
	Findings []Finding      `json:"findings"`
	Samples  []Sample       `json:"samples"`
	Open     []Engagement   `json:"engagements"` // incarichi aperti che comprendono il controllo (per l'auditor)
	Can      struct {
		Audit   bool `json:"audit"`
		Respond bool `json:"respond"`
	} `json:"can"`
}

// ControlAudit restituisce lo stato dell'audit sul controllo (chi legge la conformità o il responsabile).
func (s *Service) ControlAudit(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ControlAudit, error) {
	cid, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	_, _, owner, err := controlInfo(ctx, tx, cid)
	if err != nil {
		return nil, err
	}
	if !CanRead(actor) && !canRespond(actor, owner) {
		return nil, engine.ErrForbidden
	}
	v := &ControlAudit{}
	if v.Requests, err = s.requests(ctx, tx, actor, "where q.control_element_id = $1", cid); err != nil {
		return nil, err
	}
	if v.Comments, err = s.comments(ctx, tx, cid); err != nil {
		return nil, err
	}
	if v.Findings, err = s.findings(ctx, tx, actor, "where f.control_element_id = $1", cid); err != nil {
		return nil, err
	}
	if v.Samples, err = s.samplesOf(ctx, tx, cid); err != nil {
		return nil, err
	}
	v.Open = []Engagement{}
	if actor.Has(PermAudit) {
		rows, err := tx.Query(ctx, "select id::text from grc_audit_engagements where status = 'open' order by created_at desc")
		if err != nil {
			return nil, err
		}
		var ids []string
		var x string
		if _, err := pgx.ForEachRow(rows, []any{&x}, func() error { ids = append(ids, x); return nil }); err != nil {
			return nil, err
		}
		for _, id := range ids {
			e, err := s.scanEngagement(ctx, tx, tx.QueryRow(ctx, engagementSelect+" where g.id = $1", id), false)
			if err != nil {
				return nil, err
			}
			sc, err := scopeControls(ctx, tx, id, e.Framework)
			if err != nil {
				return nil, err
			}
			for _, c := range sc {
				if c == cid {
					v.Open = append(v.Open, *e)
					break
				}
			}
		}
	}
	v.Can.Audit = actor.Has(PermAudit)
	v.Can.Respond = canRespond(actor, owner)
	return v, nil
}
