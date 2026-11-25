package requirements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// Deroghe (sdlc.md §6; piano, passo 6): un requisito che non si realizza per tempo si
// copre con una deroga motivata, con rischio residuo, scadenza e misure compensative.
// L'approvazione passa dal motore delle approvazioni con la politica "@exception" a soglie
// sul punteggio del rischio; chi chiede non approva. In vigore, la deroga copre le celle
// indicate; alla scadenza (o alla revoca) le celle tornano da realizzare e chi segue il
// requisito riceve la notifica.

// RiskLevel è un livello di rischio residuo, configurabile per tenant.
type RiskLevel struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Score int    `json:"score"`
}

// Settings sono le impostazioni delle deroghe.
type Settings struct {
	RiskLevels       []RiskLevel `json:"risk_levels"`
	MaxExceptionDays int         `json:"max_exception_days"`
	ExpiryNoticeDays int         `json:"expiry_notice_days"`
}

// DefaultRiskLevels sono i livelli di partenza.
var DefaultRiskLevels = []RiskLevel{{"basso", "Basso", 1}, {"medio", "Medio", 2}, {"alto", "Alto", 3}, {"critico", "Critico", 4}}

func loadSettings(ctx context.Context, tx pgx.Tx) (*Settings, error) {
	st := &Settings{RiskLevels: DefaultRiskLevels, MaxExceptionDays: 365, ExpiryNoticeDays: 14}
	var raw []byte
	err := tx.QueryRow(ctx, "select risk_levels, max_exception_days, expiry_notice_days from requirement_settings").Scan(&raw, &st.MaxExceptionDays, &st.ExpiryNoticeDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	return st, json.Unmarshal(raw, &st.RiskLevels)
}

// Settings restituisce le impostazioni delle deroghe del tenant (o quelle di partenza).
func (s *Service) Settings(ctx context.Context, tx pgx.Tx) (*Settings, error) {
	return loadSettings(ctx, tx)
}

func (st *Settings) level(key string) *RiskLevel {
	for i := range st.RiskLevels {
		if st.RiskLevels[i].Key == key {
			return &st.RiskLevels[i]
		}
	}
	return nil
}

// UpdateSettings salva livelli di rischio, durata massima e preavviso (process.admin).
func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in Settings) (*Settings, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if len(in.RiskLevels) < 2 || len(in.RiskLevels) > 10 {
		return nil, engine.Required("risk_levels", "servono da 2 a 10 livelli di rischio")
	}
	seen := map[string]bool{}
	for i, l := range in.RiskLevels {
		in.RiskLevels[i].Name = strings.TrimSpace(l.Name)
		if !keyRe.MatchString(l.Key) || seen[l.Key] {
			return nil, engine.InvalidValue("risk_levels", "chiave del livello %q non valida o ripetuta", l.Key)
		}
		seen[l.Key] = true
		if in.RiskLevels[i].Name == "" || l.Score < 0 || l.Score > 100 {
			return nil, engine.OutOfRange("risk_levels", 0, 100, "il livello %s vuole un nome e un punteggio tra 0 e 100", l.Key)
		}
		if i > 0 && l.Score <= in.RiskLevels[i-1].Score {
			return nil, engine.InvalidCode("validation.requirements.risk_levels_order", "risk_levels", "i livelli vanno in ordine di punteggio crescente")
		}
	}
	if in.MaxExceptionDays < 1 || in.MaxExceptionDays > 1095 {
		return nil, engine.OutOfRange("max_exception_days", 1, 1095, "durata massima tra 1 e 1095 giorni")
	}
	if in.ExpiryNoticeDays < 0 || in.ExpiryNoticeDays > 120 {
		return nil, engine.OutOfRange("expiry_notice_days", 0, 120, "preavviso tra 0 e 120 giorni")
	}
	raw, _ := json.Marshal(in.RiskLevels)
	if _, err := tx.Exec(ctx, `insert into requirement_settings (tenant_id, risk_levels, max_exception_days, expiry_notice_days, updated_by)
		values (app_tenant(), $1, $2, $3, $4)
		on conflict (tenant_id) do update set risk_levels = excluded.risk_levels, max_exception_days = excluded.max_exception_days,
		  expiry_notice_days = excluded.expiry_notice_days, updated_by = excluded.updated_by, updated_at = now()`,
		raw, in.MaxExceptionDays, in.ExpiryNoticeDays, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "requirement_settings.update", "requirement_settings", "", map[string]any{
		"levels": len(in.RiskLevels), "max_days": in.MaxExceptionDays, "notice_days": in.ExpiryNoticeDays}); err != nil {
		return nil, err
	}
	return loadSettings(ctx, tx)
}

// ---------------------------------------------------------------------------
// Politica di approvazione delle deroghe (approval_policies, stage "@exception")

// Policy è la politica delle deroghe: regole in ordine, vince la prima con la soglia vera.
type Policy struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Rules []approval.Rule `json:"rules"`
}

// GetPolicy legge la politica delle deroghe.
func (s *Service) GetPolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Policy, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	p := &Policy{Rules: []approval.Rule{}}
	var raw []byte
	err := tx.QueryRow(ctx, `select id::text, name, rules from approval_policies where stage = $1 and active order by type_id nulls last limit 1`,
		PolicyException).Scan(&p.ID, &p.Name, &raw)
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

// UpdatePolicy salva le regole della politica delle deroghe (process.admin). Ogni regola
// ha almeno un passo con approvatori esistenti: una deroga non si approva da sola.
func (s *Service) UpdatePolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, rules []approval.Rule) (*Policy, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if len(rules) == 0 || len(rules) > 20 {
		return nil, engine.Required("rules", "servono da 1 a 20 regole")
	}
	for i, r := range rules {
		if r.When.AmountGT != nil || r.When.AmountLTE != nil || r.When.Driver != "" {
			return nil, engine.InvalidParams("validation.requirements.exception_rule_thresholds", "rules", map[string]any{"rule": i + 1}, "regola %d: per le deroghe valgono solo le soglie risk_lte e risk_gt", i+1)
		}
		if len(r.Steps) == 0 {
			return nil, engine.InvalidParams("validation.policy.rule_no_step", "rules", map[string]any{"rule": i + 1}, "regola %d: serve almeno un passo di approvazione", i+1)
		}
		for j, st := range r.Steps {
			if len(st.Approvers) == 0 {
				return nil, engine.InvalidParams("validation.policy.step_no_approvers", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: senza approvatori", i+1, j+1)
			}
			for _, a := range st.Approvers {
				n := 0
				for _, v := range []string{a.Role, a.Body, a.Member} {
					if v != "" {
						n++
					}
				}
				if n != 1 {
					return nil, engine.InvalidParams("validation.policy.step_approver_kind", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: ogni approvatore è un ruolo, un organo o un membro", i+1, j+1)
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
					return nil, engine.NotFound("rules", "", "regola %d, passo %d: approvatore %s%s%s inesistente", i+1, j+1, a.Role, a.Body, a.Member)
				}
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"rules": rules})
	tag, err := tx.Exec(ctx, `update approval_policies set rules = $2 where stage = $1 and active`, PolicyException, raw)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
			values (app_tenant(), $1, 'Approvazione delle deroghe', (select id from element_types where key = $2), $3, $4)`,
			db.NewID(), ExceptionType, PolicyException, raw); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "exception_policy.update", "approval_policy", "", map[string]any{"rules": len(rules)}); err != nil {
		return nil, err
	}
	return s.GetPolicy(ctx, tx, actor)
}

// ---------------------------------------------------------------------------
// La deroga

// exceptionRow è la riga di ext_exception con i dati dell'elemento.
type exceptionRow struct {
	ID, Key, Title    string
	RequirementID     string
	RequirementKey    string
	RequirementTitle  string
	RequestID         string
	ImpactIDs         []string
	Reason, RiskLevel string
	RiskScore         int
	Compensating      string
	ExpiresOn         time.Time
	Stage             string
	ApprovalRequestID *string
	RequestedBy       *string
	RequestedByName   *string
	ApprovedAt        *time.Time
	ClosedAt          *time.Time
	NoticeSentAt      *time.Time
	CreatedAt         time.Time
	Phase, PhaseCat   string
}

const selectException = `
	select x.element_id::text, s.key || '-' || e.number, e.title, x.requirement_id::text, rs.key || '-' || re.number, re.title,
	       x.request_id::text, x.impact_ids::text[], x.reason, x.risk_level, x.risk_score, x.compensating, x.expires_on, x.stage,
	       x.approval_request_id::text, x.requested_by::text, m.display_name, x.approved_at, x.closed_at, x.notice_sent_at, x.created_at,
	       coalesce(o.name, ''), coalesce(o.category, '')
	from ext_exception x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join elements re on re.tenant_id = x.tenant_id and re.id = x.requirement_id
	join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join members m on m.tenant_id = x.tenant_id and m.id = x.requested_by`

func scanException(row pgx.Row) (*exceptionRow, error) {
	x := &exceptionRow{}
	err := row.Scan(&x.ID, &x.Key, &x.Title, &x.RequirementID, &x.RequirementKey, &x.RequirementTitle, &x.RequestID, &x.ImpactIDs,
		&x.Reason, &x.RiskLevel, &x.RiskScore, &x.Compensating, &x.ExpiresOn, &x.Stage, &x.ApprovalRequestID, &x.RequestedBy,
		&x.RequestedByName, &x.ApprovedAt, &x.ClosedAt, &x.NoticeSentAt, &x.CreatedAt, &x.Phase, &x.PhaseCat)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return x, err
}

func loadException(ctx context.Context, tx pgx.Tx, id string, lock bool) (*exceptionRow, error) {
	q := selectException + " where x.element_id = $1"
	if lock {
		q += " for update of x"
	}
	return scanException(tx.QueryRow(ctx, q, id))
}

// pendingExceptionImpacts: gli impatti già coperti da una deroga in approvazione o in
// vigore (non scaduta), per non chiederne due sulla stessa cella.
func pendingExceptionImpacts(ctx context.Context, tx pgx.Tx, requirementID string, today time.Time) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `select distinct unnest(impact_ids)::text from ext_exception
		where requirement_id = $1 and (stage in ('draft','pending') or (stage = 'approved' and expires_on >= $2))`, requirementID, today)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m, err
}

func exceptionsOf(ctx context.Context, tx pgx.Tx, requirementID string, st *Settings) ([]ExceptionSummary, error) {
	rows, err := tx.Query(ctx, selectException+" where x.requirement_id = $1 order by x.created_at desc", requirementID)
	if err != nil {
		return nil, err
	}
	out := []ExceptionSummary{}
	for rows.Next() {
		x, err := scanException(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, ExceptionSummary{ID: x.ID, Key: x.Key, Stage: x.Stage, Phase: x.Phase, RiskLevel: x.RiskLevel,
			RiskName: riskName(st, x.RiskLevel), ExpiresOn: x.ExpiresOn.Format(time.DateOnly), Impacts: x.ImpactIDs, CreatedAt: x.CreatedAt})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Gli impatti per chiave.
	for i := range out {
		keys, err := impactKeys(ctx, tx, out[i].Impacts)
		if err != nil {
			return nil, err
		}
		out[i].Impacts = keys
	}
	return out, nil
}

func impactKeys(ctx context.Context, tx pgx.Tx, ids []string) ([]string, error) {
	rows, err := tx.Query(ctx, `select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.id = any($1::uuid[]) order by e.number`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func riskName(st *Settings, key string) string {
	if l := st.level(key); l != nil {
		return l.Name
	}
	return key
}

// ExceptionInput chiede una deroga per un requisito.
type ExceptionInput struct {
	Impacts      []string `json:"impacts"` // chiavi o id; vuoto = tutte le celle ancora aperte
	Reason       string   `json:"reason"`
	RiskLevel    string   `json:"risk_level"`
	ExpiresOn    string   `json:"expires_on"`
	Compensating string   `json:"compensating"`
	Submit       *bool    `json:"submit"` // default: si chiede subito l'approvazione
}

func (s *Service) validateException(st *Settings, in *ExceptionInput) (time.Time, *RiskLevel, error) {
	in.Reason, in.Compensating = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Compensating)
	if in.Reason == "" || len(in.Reason) > 10000 {
		return time.Time{}, nil, engine.TooLong("reason", 10000, "la motivazione è obbligatoria (massimo 10000 caratteri)")
	}
	if len(in.Compensating) > 10000 {
		return time.Time{}, nil, engine.InvalidCode("validation.requirements.compensating_too_long", "compensating", "misure compensative troppo lunghe")
	}
	lvl := st.level(in.RiskLevel)
	if lvl == nil {
		keys := make([]string, len(st.RiskLevels))
		for i, l := range st.RiskLevels {
			keys[i] = l.Key
		}
		return time.Time{}, nil, engine.InvalidValue("risk_level", "livello di rischio residuo non ammesso (%s)", strings.Join(keys, ", "))
	}
	exp, err := time.Parse(time.DateOnly, strings.TrimSpace(in.ExpiresOn))
	if err != nil {
		return time.Time{}, nil, engine.DateInvalid("expires_on", "scadenza non valida (AAAA-MM-GG)")
	}
	today := s.today()
	if !exp.After(today) {
		return time.Time{}, nil, engine.InvalidCode("validation.requirements.expires_future", "expires_on", "la scadenza deve essere futura")
	}
	if exp.After(today.AddDate(0, 0, st.MaxExceptionDays)) {
		return time.Time{}, nil, engine.InvalidParams("validation.requirements.waiver_max_days", "expires_on", map[string]any{"max": st.MaxExceptionDays}, "una deroga dura al massimo %d giorni", st.MaxExceptionDays)
	}
	return exp, lvl, nil
}

// RequestException crea la deroga (figlia del requisito) e, salvo submit=false, ne chiede
// subito l'approvazione. La chiede chi realizza: il responsabile del requisito, il team
// dell'impatto o chi ha element.write.
func (s *Service) RequestException(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ExceptionInput) (*ExceptionView, error) {
	el, err := s.resolve(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := typeExists(ctx, tx, ExceptionType); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.InvalidCode("validation.requirements.waivers_not_configured", "exception", "le deroghe non sono configurate in questo tenant")
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	exp, lvl, err := s.validateException(st, &in)
	if err != nil {
		return nil, err
	}
	rows, err := s.loadCells(ctx, tx, "x.element_id = $1", el.ID)
	if err != nil {
		return nil, err
	}
	pending, err := pendingExceptionImpacts(ctx, tx, el.ID, s.today())
	if err != nil {
		return nil, err
	}
	owner, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleOwner})
	if err != nil {
		return nil, err
	}
	today := s.today()
	var impacts []string
	for _, r := range rows {
		if !r.relevant() {
			continue
		}
		wanted := len(in.Impacts) == 0 || slices.Contains(in.Impacts, r.ImpactKey) || slices.Contains(in.Impacts, r.ImpactID)
		if !wanted {
			continue
		}
		st := r.status(today)
		if st == "verified" || st == "waived" || pending[r.ImpactID] {
			if len(in.Impacts) > 0 {
				return nil, engine.InvalidParams("validation.requirements.impact_already", "impacts", map[string]any{"impact": r.ImpactKey, "status": strings.ToLower(StatusLabels[st])}, "%s è già %s o ha una deroga in corso", r.ImpactKey, strings.ToLower(StatusLabels[st]))
			}
			continue
		}
		if !owner && !actor.Has(rbac.ElementWrite) {
			impl, err := implementer(ctx, tx, r.ImpactID, actor.MemberID, nil)
			if err != nil {
				return nil, err
			}
			if !impl {
				return nil, engine.ErrForbidden
			}
		}
		impacts = append(impacts, r.ImpactID)
	}
	if len(impacts) == 0 {
		return nil, engine.InvalidCode("validation.requirements.waiver_no_cells", "impacts", "nessuna cella del requisito da coprire con una deroga")
	}
	if len(in.Impacts) > 0 && len(impacts) != len(slices.Compact(slices.Sorted(slices.Values(in.Impacts)))) {
		return nil, engine.InvalidCode("validation.requirements.waiver_not_cells", "impacts", "alcuni impatti non sono celle del requisito")
	}
	info, err := loadInfo(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	title := "Deroga · " + info.Title
	if len(title) > 500 {
		title = title[:497] + "…"
	}
	ex, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: ExceptionType, Title: title, BodyText: in.Reason,
		ParentID: el.ID, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_exception (tenant_id, element_id, requirement_id, request_id, impact_ids, reason, risk_level, risk_score,
		compensating, expires_on, requested_by) values (app_tenant(), $1, $2, $3, $4::uuid[], $5, $6, $7, $8, $9, $10)`,
		ex.ID, el.ID, info.RequestID, impacts, in.Reason, lvl.Key, lvl.Score, in.Compensating, exp, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	// Ruoli: chi chiede; responsabile e verificatori del requisito; lo sponsor della
	// richiesta (accetta il rischio residuo sopra la soglia, secondo la politica).
	if err := setRole(ctx, tx, actor, ex.ID, RoleRequester, actor.MemberID, ""); err != nil {
		return nil, err
	}
	for _, c := range []struct{ role, from string }{{RoleOwner, el.ID}, {RoleVerifier, el.ID}, {"sponsor", info.RequestID}} {
		hs, err := holdersOf(ctx, tx, []string{c.role}, c.from)
		if err != nil {
			return nil, err
		}
		for _, h := range hs {
			if err := setRole(ctx, tx, actor, ex.ID, c.role, h.member, h.group); err != nil {
				return nil, err
			}
		}
	}
	if ex, err = s.eng.StartProcess(ctx, tx, actor, ex); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "exception", "requested",
		map[string]any{"exception": ex.Key, "risk": lvl.Name, "expires_on": exp.Format(time.DateOnly)}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "exception.create", ExceptionType, ex.ID, map[string]any{"requirement": el.ID,
		"impacts": impacts, "risk": lvl.Key, "expires_on": exp.Format(time.DateOnly)}); err != nil {
		return nil, err
	}
	if in.Submit == nil || *in.Submit {
		phase, err := s.phaseFor(ctx, tx, ex, ActionException, "submitted")
		if err != nil {
			return nil, err
		}
		if phase == "" {
			return nil, engine.InvalidCode("validation.requirements.process_no_waiver_stage", "process", "il processo delle deroghe non ha una fase con l'azione exception_stage submitted")
		}
		if _, err := s.eng.Transition(ctx, tx, actor, ex.ID, phase, "", engine.TransitionOpts{}); err != nil {
			return nil, err
		}
	}
	return s.ExceptionView(ctx, tx, actor, ex.ID)
}

// ExceptionUpdate modifica una deroga in bozza (per esempio per rinnovarla dopo la scadenza).
type ExceptionUpdate struct {
	Reason       *string `json:"reason"`
	RiskLevel    *string `json:"risk_level"`
	ExpiresOn    *string `json:"expires_on"`
	Compensating *string `json:"compensating"`
}

// UpdateException modifica motivazione, rischio, scadenza e misure di una deroga in bozza.
// Solo chi l'ha chiesta.
func (s *Service) UpdateException(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ExceptionUpdate) (*ExceptionView, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != ExceptionType {
		return nil, engine.ErrNotFound
	}
	x, err := loadException(ctx, tx, el.ID, true)
	if err != nil {
		return nil, err
	}
	if ok, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleRequester}); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	if x.Stage != "draft" {
		return nil, engine.InvalidCode("validation.requirements.waiver_only_draft", "stage", "si modifica solo una deroga in bozza")
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	ei := ExceptionInput{Reason: x.Reason, RiskLevel: x.RiskLevel, ExpiresOn: x.ExpiresOn.Format(time.DateOnly), Compensating: x.Compensating}
	for _, p := range []struct {
		dst *string
		src *string
	}{{&ei.Reason, in.Reason}, {&ei.RiskLevel, in.RiskLevel}, {&ei.ExpiresOn, in.ExpiresOn}, {&ei.Compensating, in.Compensating}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
	exp, lvl, err := s.validateException(st, &ei)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update ext_exception set reason = $2, risk_level = $3, risk_score = $4, expires_on = $5, compensating = $6,
		notice_sent_at = null where element_id = $1`, el.ID, ei.Reason, lvl.Key, lvl.Score, exp, ei.Compensating); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "exception", "updated",
		map[string]any{"risk": lvl.Name, "expires_on": exp.Format(time.DateOnly)}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "exception.update", ExceptionType, el.ID, map[string]any{"risk": lvl.Key, "expires_on": exp.Format(time.DateOnly)}); err != nil {
		return nil, err
	}
	return s.ExceptionView(ctx, tx, actor, el.ID)
}

// ExceptionView è la pagina della deroga.
type ExceptionView struct {
	Element        *engine.Element `json:"element"`
	Key            string          `json:"key"`
	RequirementKey string          `json:"requirement_key"`
	RequirementTtl string          `json:"requirement_title"`
	Impacts        []string        `json:"impacts"`
	Reason         string          `json:"reason"`
	RiskLevel      string          `json:"risk_level"`
	RiskName       string          `json:"risk_name"`
	RiskScore      int             `json:"risk_score"`
	Compensating   string          `json:"compensating"`
	ExpiresOn      string          `json:"expires_on"`
	DaysLeft       int             `json:"days_left"`
	Stage          string          `json:"stage"`
	Phase          string          `json:"phase"`
	RequestedBy    *string         `json:"requested_by"`
	ApprovedAt     *time.Time      `json:"approved_at"`
	ClosedAt       *time.Time      `json:"closed_at"`
	Settings       *Settings       `json:"settings"`
	Can            map[string]bool `json:"can"`
}

// ExceptionView restituisce la deroga.
func (s *Service) ExceptionView(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ExceptionView, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != ExceptionType {
		return nil, engine.ErrNotFound
	}
	x, err := loadException(ctx, tx, el.ID, false)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	keys, err := impactKeys(ctx, tx, x.ImpactIDs)
	if err != nil {
		return nil, err
	}
	requester, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleRequester})
	if err != nil {
		return nil, err
	}
	return &ExceptionView{Element: el, Key: x.Key, RequirementKey: x.RequirementKey, RequirementTtl: x.RequirementTitle, Impacts: keys,
		Reason: x.Reason, RiskLevel: x.RiskLevel, RiskName: riskName(st, x.RiskLevel), RiskScore: x.RiskScore, Compensating: x.Compensating,
		ExpiresOn: x.ExpiresOn.Format(time.DateOnly), DaysLeft: int(x.ExpiresOn.Sub(s.today()).Hours() / 24), Stage: x.Stage, Phase: x.Phase,
		RequestedBy: x.RequestedByName, ApprovedAt: x.ApprovedAt, ClosedAt: x.ClosedAt, Settings: st,
		Can: map[string]bool{"edit": requester && x.Stage == "draft"}}, nil
}

func (s *Service) exceptionFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	x, err := loadException(ctx, tx, el.ID, false)
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	f.Set("rischio", x.RiskLevel, riskName(st, x.RiskLevel))
	f.Set("punteggio_rischio", x.RiskScore)
	f.Set("scadenza", x.ExpiresOn)
	f.Set("giorni_alla_scadenza", int(x.ExpiresOn.Sub(s.today()).Hours()/24))
	return nil
}

// ---------------------------------------------------------------------------
// Azione exception_stage e approvazione

func (s *Service) actionException(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != ExceptionType {
		return "exception_stage vale solo sulle deroghe", nil
	}
	x, err := loadException(ctx, tx, el.ID, true)
	if errors.Is(err, engine.ErrNotFound) {
		return "", engine.ErrNoEffect
	}
	if err != nil {
		return "", err
	}
	payload := map[string]any{"exception": el.Key, "requirement": x.RequirementKey}
	note := ""
	switch a.Ref {
	case "draft":
		if _, err := tx.Exec(ctx, "update ext_exception set stage = 'draft', approval_request_id = null where element_id = $1", el.ID); err != nil {
			return "", err
		}
	case "submitted":
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return "", err
		}
		if !x.ExpiresOn.After(s.today()) {
			return "", engine.InvalidCode("validation.requirements.waiver_expired", "expires_on", "la scadenza della deroga è passata: aggiornala prima di chiedere l'approvazione")
		}
		lvl := st.level(x.RiskLevel)
		if lvl == nil {
			return "", engine.InvalidParams("validation.requirements.risk_level_gone", "risk_level", map[string]any{"level": x.RiskLevel}, "il livello di rischio %q non è più configurato: aggiornalo", x.RiskLevel)
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from ext_exception where requirement_id = $1 and element_id <> $2 and impact_ids && $3::uuid[]
			and (stage = 'pending' or (stage = 'approved' and expires_on >= $4)))`, x.RequirementID, el.ID, x.ImpactIDs, s.today()).Scan(&overlap); err != nil {
			return "", err
		}
		if overlap {
			return "", engine.Duplicate("impacts", "c'è già una deroga in approvazione o in vigore sulle stesse celle")
		}
		if s.ap == nil {
			return "", engine.InvalidCode("validation.approval.engine_unavailable", "approval", "motore delle approvazioni non disponibile")
		}
		score := lvl.Score
		reqID, err := s.ap.OpenSubjectWith(ctx, tx, actor, el, SubjectException, PolicyException, "Deroga", approval.Facts{Risk: &score})
		if err != nil {
			return "", err
		}
		if reqID == "" {
			return "", engine.InvalidCode("validation.requirements.no_waiver_policy", "approval", "nessuna politica di approvazione delle deroghe attiva: configurala in Configurazione → Requisiti")
		}
		if _, err := tx.Exec(ctx, `update ext_exception set stage = 'pending', risk_score = $2, approval_request_id = $3, closed_at = null
			where element_id = $1`, el.ID, score, reqID); err != nil {
			return "", err
		}
		note = "rischio " + lvl.Name + ", scadenza " + x.ExpiresOn.Format(time.DateOnly)
	case "approved":
		if _, err := tx.Exec(ctx, "update ext_exception set stage = 'approved', approved_at = now(), closed_at = null, notice_sent_at = null where element_id = $1", el.ID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `update applicability set progress = 'waived', exception_id = $2, updated_at = now()
			where requirement_element_id = $1 and target_element_id = any($3::uuid[]) and progress <> 'verified'`, x.RequirementID, el.ID, x.ImpactIDs); err != nil {
			return "", err
		}
		if err := s.syncPhase(ctx, tx, actor, x.RequirementID); err != nil {
			return "", err
		}
		payload["event"] = "exception_approved"
		if err := s.notifyException(ctx, tx, actor, x, payload, false); err != nil {
			return "", err
		}
		note = "in vigore fino al " + x.ExpiresOn.Format(time.DateOnly)
	case "rejected":
		if _, err := tx.Exec(ctx, "update ext_exception set stage = 'rejected', closed_at = now() where element_id = $1", el.ID); err != nil {
			return "", err
		}
		payload["event"] = "exception_rejected"
		if err := s.notifyException(ctx, tx, actor, x, payload, false); err != nil {
			return "", err
		}
	case "expired", "revoked":
		if _, err := tx.Exec(ctx, "update ext_exception set stage = $2, closed_at = now() where element_id = $1", el.ID, a.Ref); err != nil {
			return "", err
		}
		// Le celle coperte tornano da realizzare: la deroga resta citata come scaduta.
		if _, err := tx.Exec(ctx, `update applicability set progress = 'open', updated_at = now()
			where requirement_element_id = $1 and exception_id = $2 and progress = 'waived'`, x.RequirementID, el.ID); err != nil {
			return "", err
		}
		if err := s.syncPhase(ctx, tx, actor, x.RequirementID); err != nil {
			return "", err
		}
		payload["event"] = "exception_" + a.Ref
		if err := s.notifyException(ctx, tx, actor, x, payload, true); err != nil {
			return "", err
		}
	default:
		return "stadio " + a.Ref + " non ammesso", nil
	}
	if err := audited(ctx, tx, actor, "exception."+a.Ref, ExceptionType, el.ID, map[string]any{"requirement": x.RequirementID}); err != nil {
		return "", err
	}
	if note == "" {
		return "", engine.ErrNoEffect
	}
	return note, nil
}

// notifyException avvisa chi ha chiesto la deroga e chi realizza il requisito; con wide
// anche i verificatori e chi segue il requisito (scadenza, revoca).
func (s *Service) notifyException(ctx context.Context, tx pgx.Tx, actor engine.Actor, x *exceptionRow, payload map[string]any, wide bool) error {
	roles := []string{RoleOwner}
	if wide {
		roles = append(roles, RoleVerifier)
	}
	var members, groups []string
	members = append(members, deref(x.RequestedBy))
	for _, r := range roles {
		hs, err := holdersOf(ctx, tx, []string{r}, x.RequirementID)
		if err != nil {
			return err
		}
		for _, h := range hs {
			members, groups = append(members, h.member), append(groups, h.group)
		}
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRequirement, ElementID: x.ID, ActorID: actor.MemberID,
		Members: members, Groups: groups, Payload: payload}); err != nil {
		return err
	}
	if wide {
		_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRequirement, ElementID: x.RequirementID, ActorID: actor.MemberID,
			Followers: true, Exclude: members, Payload: payload})
		return err
	}
	return nil
}

// closeException applica l'esito dell'approvazione: in vigore, respinta o (rinviata) di
// nuovo in bozza, con un passaggio di sistema del processo della deroga.
func (s *Service) closeException(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var id string
	if err := tx.QueryRow(ctx, "select element_id::text from approval_requests where id = $1", requestID).Scan(&id); err != nil {
		return err
	}
	stage := map[string]string{"approved": "approved", "approved_with_conditions": "approved", "rejected": "rejected", "deferred": "draft"}[outcome]
	if stage == "" {
		return nil
	}
	el, err := s.eng.Get(ctx, tx, reader(actor.MemberID), id)
	if err != nil {
		return err
	}
	phase, err := s.phaseFor(ctx, tx, el, ActionException, stage)
	if err != nil {
		return err
	}
	if phase == "" {
		return engine.InvalidParams("validation.requirements.process_no_stage", "process", map[string]any{"stage": stage}, "il processo delle deroghe non ha una fase per lo stadio %s", stage)
	}
	sys := engine.Actor{AccountID: actor.AccountID, MemberID: actor.MemberID, IP: actor.IP, Permissions: []string{"element.read"}}
	_, err = s.eng.Transition(ctx, tx, sys, id, phase, comment, engine.TransitionOpts{System: true})
	return err
}

// ---------------------------------------------------------------------------
// Scadenze

// ExpiryReport conta gli avvisi e le scadenze di un giro del job.
type ExpiryReport struct {
	Notices int `json:"notices"`
	Expired int `json:"expired"`
}

// RunExpiry esegue il job delle scadenze a intervalli finché ctx non è chiuso.
func (s *Service) RunExpiry(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func() {
		rep, err := s.CheckExpiry(ctx, d)
		if err != nil {
			log.Warn("scadenze delle deroghe", "errore", err)
		} else if rep.Notices+rep.Expired > 0 {
			log.Info("scadenze delle deroghe", "preavvisi", rep.Notices, "scadute", rep.Expired)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// CheckExpiry controlla le deroghe in vigore di tutti i tenant.
func (s *Service) CheckExpiry(ctx context.Context, d *db.DB) (ExpiryReport, error) {
	// Solo l'elenco dei tenant passa dal ruolo di piattaforma; il resto gira sotto RLS.
	rows, err := d.Owner.Query(ctx, `select distinct tenant_id::text from ext_exception where stage = 'approved'`)
	if err != nil {
		return ExpiryReport{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return ExpiryReport{}, err
	}
	var total ExpiryReport
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			rep, err := s.CheckExpiryTenant(ctx, tx)
			total.Notices += rep.Notices
			total.Expired += rep.Expired
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckExpiryTenant: le deroghe scadute (scadenza passata) tornano aperte con un passaggio
// di sistema; quelle vicine alla scadenza ricevono un preavviso, una volta sola.
func (s *Service) CheckExpiryTenant(ctx context.Context, tx pgx.Tx) (ExpiryReport, error) {
	var rep ExpiryReport
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return rep, err
	}
	today := s.today()
	rows, err := tx.Query(ctx, selectException+` where x.stage = 'approved' and (x.expires_on < $1 or (x.notice_sent_at is null and x.expires_on <= $2))
		order by x.expires_on for update of x skip locked`, today, today.AddDate(0, 0, st.ExpiryNoticeDays))
	if err != nil {
		return rep, err
	}
	var list []*exceptionRow
	for rows.Next() {
		x, err := scanException(rows)
		if err != nil {
			rows.Close()
			return rep, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	sys := engine.Actor{Permissions: []string{"element.read"}}
	for _, x := range list {
		if x.ExpiresOn.Before(today) {
			el, err := s.eng.Get(ctx, tx, reader(""), x.ID)
			if err != nil {
				return rep, err
			}
			phase, err := s.phaseFor(ctx, tx, el, ActionException, "expired")
			if err != nil {
				return rep, err
			}
			if phase == "" {
				return rep, fmt.Errorf("deroga %s: il processo non ha una fase per lo stadio expired", x.Key)
			}
			if _, err := s.eng.Transition(ctx, tx, sys, x.ID, phase, "Scaduta il "+x.ExpiresOn.Format(time.DateOnly), engine.TransitionOpts{System: true}); err != nil {
				return rep, err
			}
			rep.Expired++
			continue
		}
		if _, err := tx.Exec(ctx, "update ext_exception set notice_sent_at = now() where element_id = $1", x.ID); err != nil {
			return rep, err
		}
		days := int(x.ExpiresOn.Sub(today).Hours() / 24)
		if err := s.notifyException(ctx, tx, sys, x, map[string]any{"event": "exception_expiring", "exception": x.Key,
			"requirement": x.RequirementKey, "expires_on": x.ExpiresOn.Format(time.DateOnly), "days": days}, false); err != nil {
			return rep, err
		}
		rep.Notices++
	}
	return rep, nil
}
