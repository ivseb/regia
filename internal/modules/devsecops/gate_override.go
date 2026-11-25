package devsecops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/grc"
	"regia/internal/platform/db"
)

// Override del gate: una decisione fail si supera solo con una motivazione e l'approvazione
// di chi è indicato nella politica "@gate_override" (Configurazione → Gate di rilascio). Chi
// chiede l'override non lo approva (separazione dei compiti). Approvato, vale per la stessa
// applicazione e lo stesso ambiente (e versione) fino alla scadenza.

// SetGate collega al modulo il motore delle approvazioni (override) e la conformità
// (evidenze automatiche). Entrambi facoltativi.
func (s *Service) SetGate(g *grc.Service, ap *approval.Service) {
	s.grc, s.ap = g, ap
	if ap != nil {
		ap.OnClose(gateOverrideSubject, s.closeOverride)
		ap.SeparateRequester(gateOverrideSubject)
	}
}

// activeOverride: l'override approvato e non scaduto che copre la domanda.
func (s *Service) activeOverride(ctx context.Context, tx pgx.Tx, g *gateCtx) (*OverrideRef, error) {
	var o OverrideRef
	var exp *time.Time
	reqID := any(nil)
	if g.reqID != "" {
		reqID = g.reqID
	}
	err := tx.QueryRow(ctx, `select id::text, status, reason, expires_at from gate_overrides
		where application_element_id = $1 and environment = $2 and status = 'approved' and expires_at > $3
		  and (version is null or lower(version) = lower($4))
		  and (request_element_id is null or request_element_id = $5::uuid)
		order by expires_at desc limit 1`, g.appID, g.env, g.now, g.in.Version, reqID).Scan(&o.ID, &o.Status, &o.Reason, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	o.ExpiresAt = exp
	return &o, err
}

// OverrideView è un override con i suoi dati.
type OverrideView struct {
	ID          string     `json:"id"`
	DecisionID  string     `json:"decision_id"`
	Application GateRef    `json:"application"`
	Request     *GateRef   `json:"request,omitempty"`
	Environment string     `json:"environment"`
	Version     string     `json:"version,omitempty"`
	Reason      string     `json:"reason"`
	Hours       int        `json:"hours"`
	Status      string     `json:"status"` // pending | approved | rejected | revoked | expired
	RequestedBy string     `json:"requested_by"`
	RequestedAt time.Time  `json:"requested_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ApprovalID  *string    `json:"approval_request_id,omitempty"`
}

func (s *Service) loadOverrides(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]OverrideView, error) {
	rows, err := tx.Query(ctx, `select o.id::text, o.decision_id::text, o.application_element_id::text, o.request_element_id::text, o.environment, coalesce(o.version, ''),
		o.reason, o.hours, o.status, coalesce(m.display_name, ''), o.requested_at, o.decided_at, o.expires_at, o.approval_request_id::text
		from gate_overrides o left join members m on m.tenant_id = o.tenant_id and m.id = o.requested_by
		where `+where+` order by o.requested_at desc limit 50`, args...)
	if err != nil {
		return nil, err
	}
	type raw struct {
		v        OverrideView
		app, req string
		hasReq   bool
	}
	var list []raw
	for rows.Next() {
		var r raw
		var req *string
		if err := rows.Scan(&r.v.ID, &r.v.DecisionID, &r.app, &req, &r.v.Environment, &r.v.Version, &r.v.Reason, &r.v.Hours, &r.v.Status,
			&r.v.RequestedBy, &r.v.RequestedAt, &r.v.DecidedAt, &r.v.ExpiresAt, &r.v.ApprovalID); err != nil {
			rows.Close()
			return nil, err
		}
		if req != nil {
			r.req, r.hasReq = *req, true
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]OverrideView, 0, len(list))
	now := s.now()
	for _, r := range list {
		if r.v.Application, err = s.elementRefByID(ctx, tx, r.app); err != nil {
			return nil, err
		}
		if r.hasReq {
			ref, err := s.elementRefByID(ctx, tx, r.req)
			if err != nil {
				return nil, err
			}
			r.v.Request = &ref
		}
		if r.v.Status == "approved" && r.v.ExpiresAt != nil && !r.v.ExpiresAt.After(now) {
			r.v.Status = "expired"
		}
		out = append(out, r.v)
	}
	return out, nil
}

// RequestOverride chiede di superare una decisione fail.
func (s *Service) RequestOverride(ctx context.Context, tx pgx.Tx, actor engine.Actor, decisionID, reason string, hours int) (*OverrideView, error) {
	if !actor.Has("element.write") {
		return nil, engine.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, engine.Required("reason", "scrivi la motivazione dell'override")
	}
	if len(reason) > 4000 {
		return nil, engine.InvalidCode("validation.devsecops.override_reason_too_long", "reason", "la motivazione è troppo lunga")
	}
	if s.ap == nil {
		return nil, engine.InvalidCode("validation.devsecops.approval_unavailable", "approval", "motore delle approvazioni non disponibile")
	}
	var appID, env, version, outcome, mode string
	var reqID *string
	err := tx.QueryRow(ctx, `select application_element_id::text, request_element_id::text, environment, coalesce(version, ''), raw_outcome, mode
		from gate_decisions where id::text = $1`, decisionID).Scan(&appID, &reqID, &env, &version, &outcome, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if outcome != "fail" {
		return nil, engine.InvalidCode("validation.devsecops.override_only_fail", "decision", "si supera solo una decisione con esito fail")
	}
	policies, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	pol := policyFor(policies, env)
	if pol == nil || !pol.OverrideEnabled {
		return nil, engine.InvalidParams("validation.devsecops.override_not_allowed", "decision", map[string]any{"env": env}, "per l'ambiente %s gli override non sono consentiti", env)
	}
	if hours == 0 {
		hours = pol.OverrideHours
	}
	if hours < 1 || hours > pol.OverrideHours {
		return nil, engine.InvalidParams("validation.devsecops.override_hours_range", "hours", map[string]any{"max": pol.OverrideHours}, "la durata va da 1 a %d ore (il massimo dell'ambiente)", pol.OverrideHours)
	}
	app, err := s.eng.Get(ctx, tx, actor, appID)
	if err != nil {
		return nil, err
	}
	var pending bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from gate_overrides where application_element_id = $1 and environment = $2 and status = 'pending')`, appID, env).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, engine.Duplicate("decision", "c'è già un override in attesa di approvazione per %s in %s", app.Key, env)
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into gate_overrides (tenant_id, id, decision_id, application_element_id, request_element_id, environment, version, reason, hours, requested_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, decisionID, appID, reqID, env, nullable(version), reason, hours, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	rq, err := s.ap.OpenSubjectWith(ctx, tx, actor, app, gateOverrideSubject, gateOverrideStage, "Override del gate", approval.Facts{})
	if err != nil {
		return nil, err
	}
	if rq == "" {
		return nil, engine.InvalidCode("validation.devsecops.override_no_approvers", "approval", "nessun approvatore degli override: configurali in Configurazione → Gate di rilascio")
	}
	if _, err := tx.Exec(ctx, "update gate_overrides set approval_request_id = $2 where id = $1", id, rq); err != nil {
		return nil, err
	}
	payload := map[string]any{"override": id, "environment": env, "decision": decisionID, "reason": reason}
	if err := engine.AddActivity(ctx, tx, appID, actor.MemberID, "gate", "override_requested", payload, nil); err != nil {
		return nil, err
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.gate_override_request", "gate_override", id, map[string]any{
		"application": appID, "environment": env, "decision": decisionID, "hours": hours, "approval": rq}); err != nil {
		return nil, err
	}
	list, err := s.loadOverrides(ctx, tx, "o.id::text = $1", id)
	if err != nil || len(list) == 0 {
		return nil, errors.Join(err, engine.ErrNotFound)
	}
	return &list[0], nil
}

// closeOverride applica l'esito dell'approvazione.
func (s *Service) closeOverride(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var id, appID, env string
	var reqID *string
	var hours int
	err := tx.QueryRow(ctx, `select id::text, application_element_id::text, request_element_id::text, environment, hours from gate_overrides
		where approval_request_id::text = $1 for update`, requestID).Scan(&id, &appID, &reqID, &env, &hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status, field := "rejected", "override_rejected"
	var exp any
	if outcome == "approved" || outcome == "approved_with_conditions" {
		status, field = "approved", "override_approved"
		exp = s.now().Add(time.Duration(hours) * time.Hour)
	}
	if _, err := tx.Exec(ctx, "update gate_overrides set status = $2, decided_at = now(), expires_at = $3 where id = $1", id, status, exp); err != nil {
		return err
	}
	payload := map[string]any{"override": id, "environment": env, "comment": comment}
	els := []string{appID}
	if reqID != nil {
		els = append(els, *reqID)
	}
	for _, el := range els {
		if err := engine.AddActivity(ctx, tx, el, actor.MemberID, "gate", field, payload, nil); err != nil {
			return err
		}
	}
	return auditEvent(ctx, tx, actor, "devsecops.gate_"+field, "gate_override", id, map[string]any{"application": appID, "environment": env, "outcome": outcome})
}

// RevokeOverride ritira un override in attesa o approvato (chi configura il modulo).
func (s *Service) RevokeOverride(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if !CanConfigure(actor) {
		return engine.ErrForbidden
	}
	var appID, status string
	var rq *string
	err := tx.QueryRow(ctx, `select application_element_id::text, status, approval_request_id::text from gate_overrides where id::text = $1 for update`, id).Scan(&appID, &status, &rq)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "pending" && status != "approved" {
		return engine.InvalidCode("validation.devsecops.override_closed", "override", "l'override è già chiuso")
	}
	if _, err := tx.Exec(ctx, "update gate_overrides set status = 'revoked', decided_at = now() where id::text = $1", id); err != nil {
		return err
	}
	if status == "pending" && rq != nil {
		if _, err := tx.Exec(ctx, "update approval_requests set status = 'cancelled', closed_at = now() where id::text = $1 and status = 'pending'", *rq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update approval_steps set decision = 'skipped' where request_id::text = $1 and decision = 'pending'", *rq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1::uuid)", *rq); err != nil {
			return err
		}
	}
	if err := engine.AddActivity(ctx, tx, appID, actor.MemberID, "gate", "override_revoked", map[string]any{"override": id}, nil); err != nil {
		return err
	}
	return auditEvent(ctx, tx, actor, "devsecops.gate_override_revoke", "gate_override", id, map[string]any{"application": appID})
}
