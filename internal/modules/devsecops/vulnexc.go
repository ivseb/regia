package devsecops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Eccezioni sulle vulnerabilità (accettazione del rischio). Chi corregge motiva, indica fino
// a quando e, se vuole, il rischio del registro dei rischi a cui si collega; l'approvazione
// passa dal motore delle approvazioni con la politica «@vuln_exception», a soglie sulla
// gravità più alta tra le vulnerabilità coperte (1 bassa … 4 critica). Chi chiede non decide.
// Approvata, l'eccezione ferma l'orologio; alla scadenza (o alla revoca) le vulnerabilità
// tornano da correggere e l'escalation riparte.

const (
	SubjectVulnException = "vuln_exception"
	PolicyVulnException  = "@vuln_exception"
)

// UseApprovals collega le eccezioni al motore delle approvazioni: la reazione all'esito e la
// separazione dei compiti. Senza, le eccezioni non si possono chiedere.
func (s *Service) UseApprovals(ap *approval.Service) {
	if ap == nil {
		return
	}
	s.ap = ap
	ap.OnClose(SubjectVulnException, s.closeException)
	ap.SeparateRequester(SubjectVulnException)
}

// ExceptionInput è la richiesta di eccezione su una o più vulnerabilità.
type ExceptionInput struct {
	IDs          []string `json:"ids"`
	Reason       string   `json:"reason"`
	Until        string   `json:"until"` // AAAA-MM-GG
	Compensating string   `json:"compensating"`
	Risk         string   `json:"risk"` // chiave di un rischio del registro, facoltativa
}

// ExceptionView è un'eccezione con le sue vulnerabilità.
type ExceptionView struct {
	ID             string     `json:"id"`
	Key            string     `json:"key"`
	ApplicationID  string     `json:"application_id"`
	ApplicationKey string     `json:"application_key"`
	Application    string     `json:"application"`
	Reason         string     `json:"reason"`
	Compensating   string     `json:"compensating"`
	Until          string     `json:"until"`
	DaysLeft       int        `json:"days_left"`
	Status         string     `json:"status"`
	RiskScore      int        `json:"risk_score"`
	RiskKey        *string    `json:"risk_key"`
	RiskTitle      *string    `json:"risk_title"`
	Findings       int        `json:"findings"`
	Worst          string     `json:"worst"`
	RequestedBy    *string    `json:"requested_by"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedAt      *time.Time `json:"decided_at"`
	Waiting        []string   `json:"waiting"`
	CVEs           []string   `json:"cves,omitempty"`
}

const selectVulnExc = `
	select x.id::text, 'EV-' || x.number, x.application_id::text, s.key || '-' || e.number, e.title, x.reason, x.compensating, x.until,
	       x.status, x.risk_score, rs.key || '-' || re.number, re.title,
	       (select count(*) from vuln_exception_findings l where l.exception_id = x.id),
	       coalesce((select (array_agg(f.severity order by case f.severity when 'critical' then 4 when 'high' then 3 when 'medium' then 2 else 1 end desc))[1]
	                 from vuln_exception_findings l join findings f on f.tenant_id = l.tenant_id and f.id = l.finding_id where l.exception_id = x.id), ''),
	       m.display_name, x.requested_at, x.decided_at, x.approval_request_id::text
	from vuln_exceptions x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.application_id
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join elements re on re.tenant_id = x.tenant_id and re.id = x.risk_element_id
	left join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join members m on m.tenant_id = x.tenant_id and m.id = x.requested_by`

func (s *Service) scanExceptions(ctx context.Context, tx pgx.Tx, where, order string, args ...any) ([]ExceptionView, error) {
	rows, err := tx.Query(ctx, selectVulnExc+" "+where+" "+order, args...)
	if err != nil {
		return nil, err
	}
	today := s.today()
	type row struct {
		v   ExceptionView
		req *string
	}
	var list []row
	for rows.Next() {
		var r row
		var until time.Time
		if err := rows.Scan(&r.v.ID, &r.v.Key, &r.v.ApplicationID, &r.v.ApplicationKey, &r.v.Application, &r.v.Reason, &r.v.Compensating, &until,
			&r.v.Status, &r.v.RiskScore, &r.v.RiskKey, &r.v.RiskTitle, &r.v.Findings, &r.v.Worst, &r.v.RequestedBy, &r.v.RequestedAt, &r.v.DecidedAt, &r.req); err != nil {
			rows.Close()
			return nil, err
		}
		r.v.Until = until.Format(time.DateOnly)
		r.v.DaysLeft = int(until.Sub(today).Hours() / 24)
		r.v.Waiting = []string{}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ExceptionView, 0, len(list))
	for _, r := range list {
		if r.v.Status == "pending" && r.req != nil {
			w, err := waitingFor(ctx, tx, *r.req)
			if err != nil {
				return nil, err
			}
			r.v.Waiting = w
		}
		out = append(out, r.v)
	}
	return out, nil
}

// waitingFor: chi deve ancora decidere il passo attivo di una richiesta.
func waitingFor(ctx context.Context, tx pgx.Tx, requestID string) ([]string, error) {
	rows, err := tx.Query(ctx, `select coalesce(m.display_name, gb.name, 'ruolo ' || st.approver_role_key, '?')
		from approval_steps st
		left join members m on m.tenant_id = st.tenant_id and m.id = st.approver_member_id
		left join governance_bodies gb on gb.tenant_id = st.tenant_id and gb.id = st.approver_body_id
		where st.request_id = $1 and st.decision = 'pending'
		  and st.ordinal = (select min(ordinal) from approval_steps where request_id = $1 and decision = 'pending')`, requestID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if out == nil {
		out = []string{}
	}
	return out, err
}

func (s *Service) today() time.Time {
	n := s.now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// ListExceptions elenca le eccezioni (le più recenti prima); status vuoto = tutte.
func (s *Service) ListExceptions(ctx context.Context, tx pgx.Tx, actor engine.Actor, status string) ([]ExceptionView, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	where, args := "", []any{}
	if status != "" {
		where, args = "where x.status = $1", []any{status}
	}
	out, err := s.scanExceptions(ctx, tx, where, "order by x.number desc limit 300", args...)
	if out == nil {
		out = []ExceptionView{}
	}
	return out, err
}

func asReader(memberID string) engine.Actor {
	if memberID == "" {
		memberID = "00000000-0000-0000-0000-000000000000"
	}
	return engine.Actor{MemberID: memberID, Permissions: []string{rbac.ElementRead}}
}

// RequestException apre una eccezione per ogni applicazione delle vulnerabilità indicate.
func (s *Service) RequestException(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ExceptionInput) ([]ExceptionView, error) {
	if !canAct(actor) {
		return nil, engine.ErrForbidden
	}
	if s.ap == nil {
		return nil, engine.InvalidCode("validation.devsecops.approval_unavailable", "approval", "motore delle approvazioni non disponibile")
	}
	ids := dedupe(in.IDs)
	if len(ids) == 0 {
		return nil, engine.Required("ids", "scegli almeno una vulnerabilità")
	}
	if len(ids) > maxBulk {
		return nil, engine.InvalidParams("validation.devsecops.vuln_bulk_limit", "ids", map[string]any{"max": maxBulk}, "al massimo %d vulnerabilità per volta", maxBulk)
	}
	reason := clip(in.Reason, 4000)
	if reason == "" {
		return nil, engine.Required("reason", "spiega perché la vulnerabilità si accetta")
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	until, err := time.Parse(time.DateOnly, strings.TrimSpace(in.Until))
	if err != nil {
		return nil, engine.Required("until", "indica fino a quando vale l'eccezione (AAAA-MM-GG)")
	}
	today := s.today()
	if !until.After(today) {
		return nil, engine.InvalidCode("validation.devsecops.exception_until_past", "until", "la scadenza dell'eccezione deve essere futura")
	}
	if until.After(today.AddDate(0, 0, pol.MaxExceptionDays)) {
		return nil, engine.InvalidParams("validation.devsecops.exception_too_long", "until", map[string]any{"max": daysText(pol.MaxExceptionDays)}, "un'eccezione dura al massimo %s", daysText(pol.MaxExceptionDays))
	}
	riskID := ""
	if r := strings.TrimSpace(in.Risk); r != "" {
		id, _, _, typ, err := elementRef(ctx, tx, r)
		if err != nil || typ != "risk" {
			return nil, engine.NotFound("risk", "", "rischio inesistente: %s", r)
		}
		riskID = id
	}
	if err := lock(ctx, tx, "dso/vulnexc"); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select id::text, application_element_id::text, severity, stage, exception_id is not null from findings
		where id = any($1::uuid[]) for update`, ids)
	if err != nil {
		return nil, err
	}
	type f struct {
		id, app, sev, stage string
		taken               bool
	}
	var fs []f
	for rows.Next() {
		var x f
		var app *string
		if err := rows.Scan(&x.id, &app, &x.sev, &x.stage, &x.taken); err != nil {
			rows.Close()
			return nil, err
		}
		if app != nil {
			x.app = *app
		}
		fs = append(fs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(fs) != len(ids) {
		return nil, engine.ErrNotFound
	}
	byApp := map[string][]f{}
	var apps []string
	noApp, wrong := 0, 0
	for _, x := range fs {
		switch {
		case x.app == "":
			noApp++
		case x.taken || !slices.Contains([]string{"open", "taken", "in_progress"}, x.stage):
			wrong++
		default:
			if byApp[x.app] == nil {
				apps = append(apps, x.app)
			}
			byApp[x.app] = append(byApp[x.app], x)
		}
	}
	if len(apps) == 0 {
		switch {
		case noApp > 0:
			return nil, engine.InvalidCode("validation.devsecops.exception_service_no_app", "ids", "le vulnerabilità di un servizio senza applicazione non si possono mettere in eccezione: associa prima il servizio a un'applicazione")
		default:
			return nil, engine.InvalidCode("validation.devsecops.exception_none_eligible", "ids", "nessuna delle vulnerabilità scelte è da correggere o senza una richiesta in corso")
		}
	}
	var created []string
	for _, appID := range apps {
		list := byApp[appID]
		score := 1
		for _, x := range list {
			score = max(score, sevScore(x.sev))
		}
		var number int
		if err := tx.QueryRow(ctx, "select coalesce(max(number), 0) + 1 from vuln_exceptions").Scan(&number); err != nil {
			return nil, err
		}
		excID := db.NewID()
		if _, err := tx.Exec(ctx, `insert into vuln_exceptions (tenant_id, id, number, application_id, reason, compensating, until, risk_score, risk_element_id,
			requested_by, requested_at) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			excID, number, appID, reason, clip(in.Compensating, 4000), until, score, nullable(riskID), nullable(actor.MemberID), s.now()); err != nil {
			return nil, err
		}
		fids := make([]string, len(list))
		for i, x := range list {
			fids[i] = x.id
		}
		if _, err := tx.Exec(ctx, `insert into vuln_exception_findings (tenant_id, exception_id, finding_id) select app_tenant(), $1, unnest($2::uuid[])`, excID, fids); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "update findings set exception_id = $1 where id = any($2::uuid[])", excID, fids); err != nil {
			return nil, err
		}
		el, err := s.eng.Get(ctx, tx, asReader(actor.MemberID), appID)
		if err != nil {
			return nil, err
		}
		reqID, err := s.ap.OpenSubjectWith(ctx, tx, actor, el, SubjectVulnException, PolicyVulnException, "Eccezione per vulnerabilità", approval.Facts{Risk: &score})
		if err != nil {
			return nil, err
		}
		if reqID == "" {
			return nil, engine.InvalidCode("validation.devsecops.exception_no_policy", "approval", "nessuna politica di approvazione delle eccezioni: configurala in Configurazione → Vulnerabilità")
		}
		if _, err := tx.Exec(ctx, "update vuln_exceptions set approval_request_id = $2 where id = $1", excID, reqID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
			select app_tenant(), f.id, $2, $3::uuid, 'exception_requested', f.stage, f.stage, $4 from findings f where f.id = any($1::uuid[])`,
			fids, s.now(), nullable(actor.MemberID), "EV-"+fmt.Sprint(number)+": "+clip(reason, 300)); err != nil {
			return nil, err
		}
		if riskID != "" {
			if err := linkRisk(ctx, tx, actor, riskID, appID); err != nil {
				return nil, err
			}
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.vuln_exception_requested", "vuln_exception", excID, map[string]any{
			"number": number, "application": appID, "findings": len(list), "until": until.Format(time.DateOnly), "score": score}); err != nil {
			return nil, err
		}
		created = append(created, excID)
	}
	return s.exceptionsByID(ctx, tx, created)
}

func (s *Service) exceptionsByID(ctx context.Context, tx pgx.Tx, ids []string) ([]ExceptionView, error) {
	if len(ids) == 0 {
		return []ExceptionView{}, nil
	}
	return s.scanExceptions(ctx, tx, "where x.id = any($1::uuid[])", "order by x.number", ids)
}

// linkRisk collega il rischio all'applicazione con la relazione «risk_of» del registro dei
// rischi, se il tipo di relazione esiste e il collegamento manca.
func linkRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, riskID, appID string) error {
	_, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), gen_random_uuid(), rt.id, $1, $2, $3::uuid from relation_types rt where rt.key = 'risk_of'
		on conflict do nothing`, riskID, appID, nullable(actor.MemberID))
	return err
}

// SetExceptionRisk collega (o scollega, con ref vuoto) l'eccezione a un rischio del registro.
func (s *Service) SetExceptionRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, ref string) (*ExceptionView, error) {
	if !canAct(actor) {
		return nil, engine.ErrForbidden
	}
	var appID string
	var status string
	if err := tx.QueryRow(ctx, "select application_id::text, status from vuln_exceptions where id::text = $1 for update", id).Scan(&appID, &status); errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	riskID := ""
	if ref = strings.TrimSpace(ref); ref != "" {
		rid, _, _, typ, err := elementRef(ctx, tx, ref)
		if err != nil || typ != "risk" {
			return nil, engine.NotFound("risk", "", "rischio inesistente: %s", ref)
		}
		riskID = rid
		if err := linkRisk(ctx, tx, actor, riskID, appID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update vuln_exceptions set risk_element_id = $2 where id = $1", id, nullable(riskID)); err != nil {
		return nil, err
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.vuln_exception_risk", "vuln_exception", id, map[string]any{"risk": riskID}); err != nil {
		return nil, err
	}
	v, err := s.exceptionsByID(ctx, tx, []string{id})
	if err != nil || len(v) == 0 {
		return nil, err
	}
	return &v[0], nil
}

// RevokeException ritira un'eccezione in attesa o in vigore: le vulnerabilità tornano da correggere.
func (s *Service) RevokeException(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, comment string) (*ExceptionView, error) {
	var status string
	var requester *string
	if err := tx.QueryRow(ctx, "select status, requested_by::text from vuln_exceptions where id::text = $1 for update", id).Scan(&status, &requester); errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if !CanConfigure(actor) && !(requester != nil && *requester == actor.MemberID && canAct(actor)) {
		return nil, engine.ErrForbidden
	}
	if status != "pending" && status != "approved" {
		return nil, engine.InvalidCode("validation.devsecops.exception_closed", "status", "l'eccezione è già conclusa")
	}
	if err := s.endException(ctx, tx, actor, id, "revoked", clip(comment, 1000)); err != nil {
		return nil, err
	}
	v, err := s.exceptionsByID(ctx, tx, []string{id})
	if err != nil || len(v) == 0 {
		return nil, err
	}
	return &v[0], nil
}

// endException chiude un'eccezione (scaduta o revocata): annulla l'approvazione ancora in
// corso, rimette le vulnerabilità da correggere e ne riallinea la presenza nei report.
func (s *Service) endException(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, status, note string) error {
	var reqID *string
	if err := tx.QueryRow(ctx, `update vuln_exceptions set status = $2, closed_at = $3 where id = $1 returning approval_request_id::text`, id, status, s.now()).Scan(&reqID); err != nil {
		return err
	}
	if reqID != nil {
		tag, err := tx.Exec(ctx, "update approval_requests set status = 'cancelled', closed_at = $2 where id = $1 and status = 'pending'", *reqID, s.now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, "update approval_steps set decision = 'skipped' where request_id = $1 and decision = 'pending'", *reqID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1)", *reqID); err != nil {
				return err
			}
		}
	}
	kind := "exception_" + status
	rows, err := tx.Query(ctx, `
		with old as (select f.id, f.stage, f.service_id::text as svc from findings f where f.exception_id = $1 for update),
		upd as (update findings f set exception_id = null,
		          stage = case when f.stage = 'exception' then 'open' else f.stage end,
		          status = case when f.status = 'accepted' then 'open' else f.status end,
		          notice_level = 0
		        from old where f.id = old.id returning f.id, old.stage as from_stage, f.stage as to_stage, old.svc),
		ev as (insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
		       select app_tenant(), id, $2, $3::uuid, $4, from_stage, to_stage, $5 from upd)
		select distinct svc from upd where svc is not null`, id, s.now(), nullable(actor.MemberID), kind, note)
	if err != nil {
		return err
	}
	svcs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, sv := range svcs {
		if err := s.refreshFindings(ctx, tx, sv); err != nil {
			return err
		}
	}
	return auditEvent(ctx, tx, actor, "devsecops.vuln_"+kind, "vuln_exception", id, map[string]any{"findings": "all"})
}

// closeException applica l'esito dell'approvazione.
func (s *Service) closeException(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var id, appID string
	var number int
	var until time.Time
	var requester *string
	err := tx.QueryRow(ctx, `select id::text, application_id::text, number, until, requested_by::text from vuln_exceptions
		where approval_request_id = $1 for update`, requestID).Scan(&id, &appID, &number, &until, &requester)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := fmt.Sprintf("EV-%d", number)
	members := []string{}
	if requester != nil {
		members = append(members, *requester)
	}
	switch outcome {
	case "approved", "approved_with_conditions":
		if _, err := tx.Exec(ctx, "update vuln_exceptions set status = 'approved', decided_at = $2 where id = $1", id, s.now()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			with old as (select id, stage from findings where exception_id = $1 and stage in ('open','taken','in_progress') for update),
			upd as (update findings f set stage = 'exception', status = case when f.status = 'open' then 'accepted' else f.status end
			        from old where f.id = old.id returning f.id, old.stage as from_stage)
			insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
			select app_tenant(), id, $2, $3::uuid, 'exception_approved', from_stage, 'exception', $4 from upd`,
			id, s.now(), nullable(actor.MemberID), key+" fino al "+until.Format(time.DateOnly)); err != nil {
			return err
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.vuln_exception_approved", "vuln_exception", id, map[string]any{"number": number}); err != nil {
			return err
		}
		return s.notifyVuln(ctx, tx, appID, actor.MemberID, members, nil, map[string]any{"event": "vuln_exception_approved", "exception": key, "until": until.Format("02/01/2006")})
	default:
		if _, err := tx.Exec(ctx, "update vuln_exceptions set status = 'rejected', decided_at = $2, closed_at = $2 where id = $1", id, s.now()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			with upd as (update findings set exception_id = null where exception_id = $1 returning id, stage)
			insert into vuln_events (tenant_id, finding_id, at, actor_id, kind, from_stage, to_stage, note)
			select app_tenant(), id, $2, $3::uuid, 'exception_rejected', stage, stage, $4 from upd`,
			id, s.now(), nullable(actor.MemberID), clip(key+": "+comment, 500)); err != nil {
			return err
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.vuln_exception_rejected", "vuln_exception", id, map[string]any{"number": number, "outcome": outcome}); err != nil {
			return err
		}
		return s.notifyVuln(ctx, tx, appID, actor.MemberID, members, nil, map[string]any{"event": "vuln_exception_rejected", "exception": key})
	}
}

// notifyVuln manda una notifica sull'applicazione ai membri e ai gruppi indicati.
func (s *Service) notifyVuln(ctx context.Context, tx pgx.Tx, appID, actorID string, members, groups []string, payload map[string]any) error {
	if len(members) == 0 && len(groups) == 0 {
		return nil
	}
	_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindVulnerability, ElementID: appID, ActorID: actorID, Members: members, Groups: groups, Payload: payload})
	return err
}

// ExpireExceptions chiude le eccezioni scadute e avvisa di quelle che stanno per scadere.
// Restituisce quante ne ha chiuse e quanti avvisi ha mandato.
func (s *Service) ExpireExceptions(ctx context.Context, tx pgx.Tx, pol VulnPolicy) (expired, notices int, err error) {
	today := s.today()
	rows, err := tx.Query(ctx, `select id::text from vuln_exceptions where status in ('pending','approved') and until < $1 for update skip locked`, today)
	if err != nil {
		return 0, 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		var appID, key string
		var requester *string
		var number int
		var until time.Time
		if err := tx.QueryRow(ctx, "select application_id::text, number, requested_by::text, until from vuln_exceptions where id = $1", id).Scan(&appID, &number, &requester, &until); err != nil {
			return expired, notices, err
		}
		key = fmt.Sprintf("EV-%d", number)
		if err := s.endException(ctx, tx, engine.Actor{}, id, "expired", "L'eccezione è scaduta il "+until.Format("02/01/2006")); err != nil {
			return expired, notices, err
		}
		members := []string{}
		if requester != nil {
			members = append(members, *requester)
		}
		lead, err := teamLeads(ctx, tx, appID)
		if err != nil {
			return expired, notices, err
		}
		if err := s.notifyVuln(ctx, tx, appID, "", append(members, lead...), nil, map[string]any{"event": "vuln_exception_expired", "exception": key}); err != nil {
			return expired, notices, err
		}
		expired++
	}
	if pol.ExceptionNoticeDays > 0 {
		rows, err := tx.Query(ctx, `select id::text, application_id::text, number, requested_by::text, until from vuln_exceptions
			where status = 'approved' and notice_sent_at is null and until >= $1 and until <= $1 + $2::int * interval '1 day' for update skip locked`, today, pol.ExceptionNoticeDays)
		if err != nil {
			return expired, notices, err
		}
		type due struct {
			id, app string
			number  int
			req     *string
			until   time.Time
		}
		var dues []due
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.id, &d.app, &d.number, &d.req, &d.until); err != nil {
				rows.Close()
				return expired, notices, err
			}
			dues = append(dues, d)
		}
		rows.Close()
		for _, d := range dues {
			if _, err := tx.Exec(ctx, "update vuln_exceptions set notice_sent_at = $2 where id = $1", d.id, s.now()); err != nil {
				return expired, notices, err
			}
			members := []string{}
			if d.req != nil {
				members = append(members, *d.req)
			}
			lead, err := teamLeads(ctx, tx, d.app)
			if err != nil {
				return expired, notices, err
			}
			if err := s.notifyVuln(ctx, tx, d.app, "", append(members, lead...), nil, map[string]any{"event": "vuln_exception_expiring",
				"exception": fmt.Sprintf("EV-%d", d.number), "until": d.until.Format("02/01/2006")}); err != nil {
				return expired, notices, err
			}
			notices++
		}
	}
	return expired, notices, nil
}

func teamLeads(ctx context.Context, tx pgx.Tx, appID string) ([]string, error) {
	rows, err := tx.Query(ctx, `select t.lead_id::text from ext_application a join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where a.element_id = $1 and t.lead_id is not null`, appID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------------------------------------------------------------------------
// Politica di approvazione delle eccezioni

// ExceptionPolicy è la politica: regole in ordine, vince la prima con la soglia vera.
type ExceptionPolicy struct {
	ID    string          `json:"id"`
	Rules []approval.Rule `json:"rules"`
}

// GetExceptionPolicy legge la politica (regole vuote se non è configurata).
func (s *Service) GetExceptionPolicy(ctx context.Context, tx pgx.Tx) (*ExceptionPolicy, error) {
	p := &ExceptionPolicy{Rules: []approval.Rule{}}
	var raw []byte
	err := tx.QueryRow(ctx, `select id::text, rules from approval_policies where stage = $1 and active order by type_id nulls last limit 1`, PolicyVulnException).Scan(&p.ID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var pol approval.Policy
	if err := json.Unmarshal(raw, &pol); err != nil {
		return nil, err
	}
	if pol.Rules != nil {
		p.Rules = pol.Rules
	}
	return p, nil
}

// UpdateExceptionPolicy salva la politica. Ogni regola ha almeno un passo con approvatori
// esistenti: un'eccezione non si approva da sola.
func (s *Service) UpdateExceptionPolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, rules []approval.Rule) (*ExceptionPolicy, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if len(rules) == 0 || len(rules) > 10 {
		return nil, engine.Required("rules", "servono da 1 a 10 regole")
	}
	for i, r := range rules {
		if r.When.AmountGT != nil || r.When.AmountLTE != nil || r.When.Driver != "" {
			return nil, engine.InvalidParams("validation.devsecops.rule_score_only", "rules", map[string]any{"rule": i + 1}, "regola %d: valgono solo le soglie sul punteggio (1 bassa … 4 critica)", i+1)
		}
		if len(r.Steps) == 0 {
			return nil, engine.InvalidParams("validation.devsecops.rule_needs_step", "rules", map[string]any{"rule": i + 1}, "regola %d: serve almeno un passo di approvazione", i+1)
		}
		for j, st := range r.Steps {
			if len(st.Approvers) == 0 {
				return nil, engine.InvalidParams("validation.devsecops.rule_step_no_approvers", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: senza approvatori", i+1, j+1)
			}
			for _, a := range st.Approvers {
				n := 0
				for _, v := range []string{a.Role, a.Body, a.Member} {
					if v != "" {
						n++
					}
				}
				if n != 1 {
					return nil, engine.InvalidParams("validation.devsecops.rule_approver_kind", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: ogni approvatore è un ruolo, un organo o una persona", i+1, j+1)
				}
				var ok bool
				var err error
				switch {
				case a.Role != "":
					err = tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", a.Role).Scan(&ok)
				case a.Body != "":
					err = tx.QueryRow(ctx, "select exists (select 1 from governance_bodies where kind = $1 and not archived)", a.Body).Scan(&ok)
				default:
					err = tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", a.Member).Scan(&ok)
				}
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, engine.NotFound("rules", "", "regola %d, passo %d: approvatore inesistente", i+1, j+1)
				}
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"rules": rules})
	tag, err := tx.Exec(ctx, `update approval_policies set rules = $2 where stage = $1 and active`, PolicyVulnException, raw)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
			values (app_tenant(), $1, 'Approvazione delle eccezioni sulle vulnerabilità', null, $2, $3)`, db.NewID(), PolicyVulnException, raw); err != nil {
			return nil, err
		}
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.vuln_exception_policy", "approval_policy", "", map[string]any{"rules": len(rules)}); err != nil {
		return nil, err
	}
	return s.GetExceptionPolicy(ctx, tx)
}
