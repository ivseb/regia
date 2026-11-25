package requirements

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Verifica automatica dalle evidenze delle pipeline (devsecops.md §3.5; piano, passo 21).
// Un requisito con criterio "da evidenza" e una chiave di controllo (verification_check) si
// verifica da solo quando il modulo devsecops riceve un rilascio dell'applicazione con quel
// controllo superato in modalità block. La verifica è firmata come quelle manuali, con
// metodo "automatic" e il raccoglitore (collector) al posto di chi verifica.

// PipelineEvidence è l'evidenza di un rilascio: l'applicazione, la richiesta collegata dal numero di
// change (facoltativa) e le chiavi di controllo che il rilascio soddisfa.
type PipelineEvidence struct {
	ApplicationID string
	// RequestID: se c'è, si verificano solo i requisiti degli impatti di quella richiesta;
	// altrimenti quelli di tutti gli impatti dell'applicazione ancora aperti.
	RequestID string
	// Checks sono le chiavi accettate (es. "secrets", "release.secrets", "secret_scan").
	Checks    []string
	Collector string   // es. "devsecops"
	Note      string   // descrizione leggibile dell'evidenza
	Links     []string // es. la pagina della Release
	Ref       string   // identificativo dell'evidenza (id del rilascio), firmato
}

// AutoVerified è una cella verificata automaticamente.
type AutoVerified struct {
	RequirementKey string `json:"requirement"`
	ImpactKey      string `json:"impact"`
	Check          string `json:"check"`
	VerificationID string `json:"verification_id"`
}

// VerifyFromEvidence verifica le celle aperte (accettate, non ancora verificate né in
// deroga) dei requisiti da evidenza che chiedono uno dei controlli indicati, sugli impatti
// dell'applicazione. Le celle già verificate restano come sono: la funzione è idempotente.
func (s *Service) VerifyFromEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, ev PipelineEvidence) ([]AutoVerified, error) {
	if ev.ApplicationID == "" || len(ev.Checks) == 0 {
		return nil, nil
	}
	if ev.Collector == "" {
		ev.Collector = "devsecops"
	}
	rows, err := tx.Query(ctx, `
		select x.element_id::text, rs.key || '-' || re.number, c.target_element_id::text, isp.key || '-' || ie.number, x.verification_check
		from ext_requirement x
		join elements re on re.tenant_id = x.tenant_id and re.id = x.element_id and re.deleted_at is null
		join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		join applicability c on c.tenant_id = x.tenant_id and c.requirement_element_id = x.element_id
		join ext_impact xi on xi.tenant_id = c.tenant_id and xi.element_id = c.target_element_id
		join elements ie on ie.tenant_id = xi.tenant_id and ie.id = xi.element_id and ie.deleted_at is null
		join spaces isp on isp.tenant_id = ie.tenant_id and isp.id = ie.space_id
		left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
		where x.verification_method = 'evidence' and lower(x.verification_check) = any($1)
		  and xi.application_id = $2 and ($3 = '' or xi.request_id::text = $3)
		  and x.stage not in ('proposed', 'not_applicable')
		  and coalesce(c.status, '') <> 'not_applicable' and c.progress in ('open', 'to_verify')
		  and coalesce(io.category, '') not in ('done', 'cancelled')
		order by re.number, ie.number
		for update of c`, ev.Checks, ev.ApplicationID, ev.RequestID)
	if err != nil {
		return nil, err
	}
	type cell struct{ reqID, reqKey, impID, impKey, check string }
	cells, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cell, error) {
		var c cell
		err := r.Scan(&c.reqID, &c.reqKey, &c.impID, &c.impKey, &c.check)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	links := []string{}
	for _, l := range ev.Links {
		if l != "" && len(links) < 20 {
			links = append(links, l)
		}
	}
	note := ev.Note
	if len(note) > 10000 {
		note = note[:10000]
	}
	out := []AutoVerified{}
	touched := map[string]bool{}
	for _, c := range cells {
		id := db.NewID()
		at := s.Now().UTC().Truncate(time.Microsecond)
		signed, _ := json.Marshal(map[string]any{"verification": id, "requirement": c.reqID, "impact": c.impID, "outcome": "verified",
			"method": "automatic", "collector": ev.Collector, "check": c.check, "evidence": ev.Ref, "note": note, "links": links,
			"at": at.Format(time.RFC3339Nano)})
		sum := sha256.Sum256(signed)
		signature := hex.EncodeToString(sum[:])
		if _, err := tx.Exec(ctx, `insert into requirement_verifications (tenant_id, id, requirement_id, impact_id, outcome, method, collector,
			note, links, verified_at, signature)
			values (app_tenant(), $1, $2, $3, 'verified', 'automatic', $4, $5, $6, $7, $8)`,
			id, c.reqID, c.impID, ev.Collector, note, links, at, signature); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `update applicability set progress = 'verified', verified_by = null, verified_at = $3, last_verification_id = $4, updated_at = now()
			where requirement_element_id = $1 and target_element_id = $2`, c.reqID, c.impID, at, id); err != nil {
			return nil, err
		}
		if err := engine.AddActivity(ctx, tx, c.reqID, actor.MemberID, "verification", "verified",
			map[string]any{"impact": c.impKey, "signature": signature, "method": "automatic", "check": c.check, "links": len(links)}, nil); err != nil {
			return nil, err
		}
		if err := audited(ctx, tx, actor, "requirement.verify", TypeKey, c.reqID, map[string]any{"verification": id, "impact": c.impID,
			"outcome": "verified", "method": "automatic", "collector": ev.Collector, "check": c.check, "evidence": ev.Ref, "signature": signature}); err != nil {
			return nil, err
		}
		touched[c.reqID] = true
		out = append(out, AutoVerified{RequirementKey: c.reqKey, ImpactKey: c.impKey, Check: c.check, VerificationID: id})
	}
	for reqID := range touched {
		if err := s.syncPhase(ctx, tx, actor, reqID); err != nil {
			return nil, err
		}
		if err := s.notifyRole(ctx, tx, actor, reqID, RoleOwner, map[string]any{"event": "verified", "method": "automatic"}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
