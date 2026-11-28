package devsecops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/db"
)

// Gate di rilascio (devsecops.md §3.5; piano, passo 21, fase 4): le regole si configurano per
// ambiente, da Configurazione → Gate di rilascio. Ogni regola ha un tipo, un livello (block:
// se non è soddisfatta il gate fallisce; warn: avvisa soltanto) e i suoi parametri.

// Tipi di regola.
const (
	RuleRequirements    = "requirements"    // requisiti di progetto verificati o con deroga valida
	RuleVulnerabilities = "vulnerabilities" // CVE critiche o alte
	RuleChangeApproved  = "change_approved" // la richiesta del change è in una fase approvata
	RuleWindow          = "window"          // finestra di rilascio e blocchi
	RuleCondition       = "condition"       // condizione libera sui fatti
	RuleChecks          = "checks"          // controlli della pipeline superati
	RuleTests           = "tests"           // test del change (ALM in lettura, poi test management)
	gateWildcard        = "*"               // ambiente: tutti gli altri
	gateOverrideStage   = "@gate_override"  // approval_policies.stage
	gateOverrideSubject = "gate_override"   // approval_requests.subject
	maxGateRules        = 30
)

var ruleKinds = []string{RuleRequirements, RuleVulnerabilities, RuleChangeApproved, RuleWindow, RuleChecks, RuleTests, RuleCondition}

// GateRule è una regola di un ambiente.
type GateRule struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Enabled bool            `json:"enabled"`
	Level   string          `json:"level"` // block | warn
	Label   string          `json:"label,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Parametri delle regole.
type vulnParams struct {
	Severity      string `json:"severity"`       // critical | high (critiche e alte)
	OnlyFixable   bool   `json:"only_fixable"`   // solo con correzione disponibile
	Max           int    `json:"max"`            // quante se ne tollerano
	RequireReport bool   `json:"require_report"` // senza scansione il gate fallisce
}

type changeParams struct {
	Phases        []string `json:"phases"`
	RequireChange bool     `json:"require_change"`
	// ServiceNow dice come conta l'approvazione della change request letta da ServiceNow
	// (connettore in sola lettura): "" non conta, "accept" basta al posto della fase della
	// richiesta (e un rifiuto blocca), "require" serve sempre, insieme alla fase se indicata.
	ServiceNow string `json:"servicenow,omitempty"`
}

// Modi dell'approvazione di ServiceNow nella regola change_approved.
const (
	SNApprovalAccept  = "accept"
	SNApprovalRequire = "require"
)

type blackout struct {
	From  string `json:"from"` // AAAA-MM-GG
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

type windowParams struct {
	Days      []int      `json:"days"` // 1 = lunedì … 7 = domenica; vuoto = tutti
	From      string     `json:"from"` // HH:MM
	To        string     `json:"to"`
	Timezone  string     `json:"timezone"`
	Blackouts []blackout `json:"blackouts"`
}

type conditionParams struct {
	When string `json:"when"`
	On   string `json:"on"` // application | request
}

type checksParams struct {
	Names []string `json:"names"`
}

// testsParams sono le soglie della regola sui test del change. Si contano i test pianificati
// nei test set del numero di change (stato attuale di ciascuno) e i difetti aperti collegati.
type testsParams struct {
	MaxFailed       int    `json:"max_failed"`       // falliti tollerati
	MaxBlocked      *int   `json:"max_blocked"`      // bloccati tollerati; nil = non contano
	MinExecutedPct  int    `json:"min_executed_pct"` // eseguiti sul totale (senza i non applicabili), 0-100
	DefectsSeverity string `json:"defects_severity"` // critical | high | medium | low; "" = i difetti non contano
	MaxOpenDefects  int    `json:"max_open_defects"` // difetti aperti tollerati dalla gravità indicata in su
	RequireResults  bool   `json:"require_results"`  // senza test del change il gate fallisce
}

// GatePolicy sono le regole di un ambiente.
type GatePolicy struct {
	ID              string     `json:"id"`
	Environment     string     `json:"environment"`
	Mode            string     `json:"mode"` // off | warn | block
	Rules           []GateRule `json:"rules"`
	OverrideEnabled bool       `json:"override_enabled"`
	OverrideHours   int        `json:"override_hours"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

func newRuleID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func strictDecode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// DefaultGateRules: le regole proposte per un ambiente nuovo (tutte avvisano soltanto, finché
// il tenant non le porta a block).
func DefaultGateRules(production bool) []GateRule {
	mk := func(kind string, enabled bool, level string, p any) GateRule {
		raw, _ := json.Marshal(p)
		return GateRule{ID: newRuleID(), Kind: kind, Enabled: enabled, Level: level, Params: raw}
	}
	rules := []GateRule{
		mk(RuleRequirements, production, "block", struct{}{}),
		mk(RuleVulnerabilities, true, "block", vulnParams{Severity: "high", OnlyFixable: true, Max: 0, RequireReport: false}),
		mk(RuleChangeApproved, production, "block", changeParams{Phases: []string{}, RequireChange: true}),
		mk(RuleWindow, false, "warn", windowParams{Days: []int{1, 2, 3, 4, 5}, From: "08:00", To: "18:00", Timezone: "Europe/Rome", Blackouts: []blackout{}}),
		mk(RuleChecks, false, "block", checksParams{Names: []string{}}),
	}
	return rules
}

// normRule controlla e normalizza una regola.
func normRule(r GateRule, i int, now time.Time) (GateRule, error) {
	field := fmt.Sprintf("rules[%d]", i)
	if !slices.Contains(ruleKinds, r.Kind) {
		return r, engine.InvalidValue(field, "regola %d: tipo %q sconosciuto", i+1, r.Kind)
	}
	if r.Level != "block" && r.Level != "warn" {
		return r, engine.InvalidParams("validation.devsecops.gate_rule_level", field, map[string]any{"rule": i + 1}, "regola %d: il livello è block o warn", i+1)
	}
	r.ID = clip(r.ID, 40)
	if r.ID == "" {
		r.ID = newRuleID()
	}
	r.Label = clip(r.Label, 200)
	bad := func(format string, a ...any) error {
		return engine.InvalidParams("validation.devsecops.gate_rule_invalid", field, map[string]any{"rule": i + 1, "detail": fmt.Sprintf(format, a...)}, "regola %d: "+format, append([]any{i + 1}, a...)...)
	}
	var out any
	switch r.Kind {
	case RuleRequirements:
		var p struct{}
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		out = p
	case RuleVulnerabilities:
		var p vulnParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		if p.Severity != "critical" && p.Severity != "high" {
			return r, bad("la gravità è critical (critiche) o high (critiche e alte)")
		}
		if p.Max < 0 || p.Max > 100000 {
			return r, bad("il numero tollerato va da 0 a 100000")
		}
		out = p
	case RuleChangeApproved:
		var p changeParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		ph := []string{}
		for _, x := range p.Phases {
			if x = clip(x, 100); x != "" && !slices.Contains(ph, x) {
				ph = append(ph, x)
			}
		}
		switch p.ServiceNow {
		case "", SNApprovalAccept, SNApprovalRequire:
		default:
			return r, bad("l'approvazione di ServiceNow è accept (basta), require (serve) o vuota (non conta)")
		}
		if len(ph) == 0 && r.Enabled && p.ServiceNow == "" {
			return r, bad("scegli almeno una fase in cui il change è approvato")
		}
		p.Phases = ph
		out = p
	case RuleWindow:
		var p windowParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		for _, d := range p.Days {
			if d < 1 || d > 7 {
				return r, bad("i giorni vanno da 1 (lunedì) a 7 (domenica)")
			}
		}
		slices.Sort(p.Days)
		p.Days = slices.Compact(p.Days)
		if p.Days == nil {
			p.Days = []int{}
		}
		for _, hm := range []string{p.From, p.To} {
			if _, err := time.Parse("15:04", hm); err != nil {
				return r, bad("gli orari sono nel formato HH:MM")
			}
		}
		if p.Timezone == "" {
			p.Timezone = "Europe/Rome"
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return r, bad("fuso orario sconosciuto: %s", p.Timezone)
		}
		bo := []blackout{}
		for _, b := range p.Blackouts {
			if b.From == "" && b.To == "" {
				continue
			}
			f, e1 := time.Parse(time.DateOnly, b.From)
			t, e2 := time.Parse(time.DateOnly, b.To)
			if e1 != nil || e2 != nil || t.Before(f) {
				return r, bad("un blocco ha date non valide (AAAA-MM-GG, dal non dopo al)")
			}
			b.Label = clip(b.Label, 120)
			bo = append(bo, b)
		}
		if len(bo) > 50 {
			return r, bad("al massimo 50 blocchi")
		}
		p.Blackouts = bo
		out = p
	case RuleChecks:
		var p checksParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		names := []string{}
		for _, n := range p.Names {
			if n = strings.ToLower(clip(n, 60)); n != "" && !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		if len(names) == 0 && r.Enabled {
			return r, bad("scegli almeno un controllo della pipeline")
		}
		p.Names = names
		out = p
	case RuleTests:
		var p testsParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		if p.MaxFailed < 0 || p.MaxFailed > 100000 || p.MaxOpenDefects < 0 || p.MaxOpenDefects > 100000 {
			return r, bad("i numeri tollerati vanno da 0 a 100000")
		}
		if p.MaxBlocked != nil && (*p.MaxBlocked < 0 || *p.MaxBlocked > 100000) {
			return r, bad("i bloccati tollerati vanno da 0 a 100000")
		}
		if p.MinExecutedPct < 0 || p.MinExecutedPct > 100 {
			return r, bad("la percentuale minima di eseguiti va da 0 a 100")
		}
		switch p.DefectsSeverity {
		case "", "critical", "high", "medium", "low":
		default:
			return r, bad("la gravità dei difetti è critical, high, medium, low o vuota (non contano)")
		}
		out = p
	case RuleCondition:
		var p conditionParams
		if err := strictDecode(r.Params, &p); err != nil {
			return r, bad("parametri non validi")
		}
		p.When = strings.TrimSpace(p.When)
		if p.On == "" {
			p.On = "application"
		}
		if p.On != "application" && p.On != "request" {
			return r, bad("la condizione vale sull'applicazione o sulla richiesta")
		}
		if p.When == "" && r.Enabled {
			return r, bad("scrivi la condizione")
		}
		if _, err := engine.ParseCondition(p.When); err != nil {
			return r, bad("condizione non valida: %v", err)
		}
		out = p
	}
	raw, _ := json.Marshal(out)
	r.Params = raw
	_ = now
	return r, nil
}

func loadPolicies(ctx context.Context, tx pgx.Tx) ([]GatePolicy, error) {
	rows, err := tx.Query(ctx, `select id::text, environment, mode, rules, override_enabled, override_hours, updated_at
		from gate_policies order by (environment = '*'), environment`)
	if err != nil {
		return nil, err
	}
	out := []GatePolicy{}
	var raw []byte
	for rows.Next() {
		var p GatePolicy
		var at time.Time
		if err := rows.Scan(&p.ID, &p.Environment, &p.Mode, &raw, &p.OverrideEnabled, &p.OverrideHours, &at); err != nil {
			rows.Close()
			return nil, err
		}
		p.UpdatedAt = &at
		p.Rules = []GateRule{}
		if err := json.Unmarshal(raw, &p.Rules); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// policyFor sceglie la politica dell'ambiente: quella con il suo nome, altrimenti "*".
func policyFor(ps []GatePolicy, env string) *GatePolicy {
	env = strings.ToLower(env)
	for i := range ps {
		if ps[i].Environment == env {
			return &ps[i]
		}
	}
	for i := range ps {
		if ps[i].Environment == gateWildcard {
			return &ps[i]
		}
	}
	return nil
}

// SaveGatePolicy salva le regole di un ambiente (o di "*").
func (s *Service) SaveGatePolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, env string, in GatePolicy) (*GatePolicy, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	env = strings.ToLower(strings.TrimSpace(env))
	if env != gateWildcard {
		env = normEnv(env)
	}
	if env == "" {
		return nil, engine.Required("environment", "indica l'ambiente")
	}
	if in.Mode != "off" && in.Mode != "warn" && in.Mode != "block" {
		return nil, engine.InvalidCode("validation.devsecops.gate_mode", "mode", "la modalità è off, warn o block")
	}
	if len(in.Rules) > maxGateRules {
		return nil, engine.InvalidParams("validation.devsecops.gate_rules_too_many", "rules", map[string]any{"max": maxGateRules}, "al massimo %d regole", maxGateRules)
	}
	if in.OverrideHours == 0 {
		in.OverrideHours = 24
	}
	if in.OverrideHours < 1 || in.OverrideHours > 720 {
		return nil, engine.OutOfRange("override_hours", 1, 720, "la durata di un override va da 1 a 720 ore")
	}
	rules := make([]GateRule, 0, len(in.Rules))
	seen := map[string]bool{}
	now := s.now()
	for i, r := range in.Rules {
		n, err := normRule(r, i, now)
		if err != nil {
			return nil, err
		}
		if seen[n.ID] {
			n.ID = newRuleID()
		}
		seen[n.ID] = true
		rules = append(rules, n)
	}
	raw, _ := json.Marshal(rules)
	var id string
	err := tx.QueryRow(ctx, `insert into gate_policies (tenant_id, id, environment, mode, rules, override_enabled, override_hours, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, now())
		on conflict (tenant_id, environment) do update set mode = excluded.mode, rules = excluded.rules,
		  override_enabled = excluded.override_enabled, override_hours = excluded.override_hours, updated_by = excluded.updated_by, updated_at = now()
		returning id::text`, db.NewID(), env, in.Mode, raw, in.OverrideEnabled, in.OverrideHours, nullable(actor.MemberID)).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.gate_policy", "gate_policy", id, map[string]any{
		"environment": env, "mode": in.Mode, "rules": len(rules), "override": in.OverrideEnabled}); err != nil {
		return nil, err
	}
	ps, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	return policyFor(ps, env), nil
}

// DeleteGatePolicy toglie le regole di un ambiente: il gate lo lascia passare.
func (s *Service) DeleteGatePolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, env string) error {
	if !CanConfigure(actor) {
		return engine.ErrForbidden
	}
	env = strings.ToLower(strings.TrimSpace(env))
	tag, err := tx.Exec(ctx, "delete from gate_policies where environment = $1", env)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return auditEvent(ctx, tx, actor, "devsecops.gate_policy_delete", "gate_policy", "", map[string]any{"environment": env})
}

// ---------------------------------------------------------------------------
// Approvatori degli override: politica "@gate_override" del motore delle approvazioni, un
// approvatore per passo, in sequenza (come le deroghe). Chi chiede l'override non lo approva.

// GateApprover è un approvatore: un membro preciso o un ruolo sull'applicazione.
type GateApprover struct {
	Member string `json:"member,omitempty"`
	Role   string `json:"role,omitempty"`
	Name   string `json:"name,omitempty"`
}

func loadApprovers(ctx context.Context, tx pgx.Tx) ([]GateApprover, error) {
	out := []GateApprover{}
	var raw []byte
	err := tx.QueryRow(ctx, `select rules from approval_policies where stage = $1 and active order by type_id nulls last limit 1`, gateOverrideStage).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var p approval.Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if len(p.Rules) == 0 {
		return out, nil
	}
	for _, st := range p.Rules[0].Steps {
		for _, a := range st.Approvers {
			g := GateApprover{Member: a.Member, Role: a.Role}
			switch {
			case a.Member != "":
				_ = tx.QueryRow(ctx, "select display_name from members where id::text = $1", a.Member).Scan(&g.Name)
			case a.Role != "":
				_ = tx.QueryRow(ctx, "select name from stakeholder_roles where key = $1", a.Role).Scan(&g.Name)
			}
			out = append(out, g)
		}
	}
	return out, nil
}

// SaveApprovers salva chi approva gli override.
func (s *Service) SaveApprovers(ctx context.Context, tx pgx.Tx, actor engine.Actor, in []GateApprover) ([]GateApprover, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if len(in) > 6 {
		return nil, engine.InvalidCode("validation.devsecops.approvers_too_many", "approvers", "al massimo 6 passi di approvazione")
	}
	steps := []approval.Step{}
	for i, a := range in {
		if (a.Member == "") == (a.Role == "") {
			return nil, engine.InvalidParams("validation.devsecops.approver_choice", "approvers", map[string]any{"n": i + 1}, "approvatore %d: scegli un membro oppure un ruolo", i+1)
		}
		var ok bool
		var err error
		if a.Member != "" {
			err = tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", a.Member).Scan(&ok)
		} else {
			err = tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", a.Role).Scan(&ok)
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.NotFound("approvers", "", "approvatore %d inesistente", i+1)
		}
		steps = append(steps, approval.Step{Approvers: []approval.Approver{{Member: a.Member, Role: a.Role}}})
	}
	if len(steps) == 0 {
		if _, err := tx.Exec(ctx, "delete from approval_policies where stage = $1", gateOverrideStage); err != nil {
			return nil, err
		}
	} else {
		raw, _ := json.Marshal(map[string]any{"rules": []approval.Rule{{Steps: steps}}})
		tag, err := tx.Exec(ctx, "update approval_policies set rules = $2, active = true where stage = $1", gateOverrideStage, raw)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
				values (app_tenant(), $1, 'Approvazione degli override del gate', null, $2, $3)`, db.NewID(), gateOverrideStage, raw); err != nil {
				return nil, err
			}
		}
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.gate_approvers", "approval_policy", "", map[string]any{"steps": len(steps)}); err != nil {
		return nil, err
	}
	return loadApprovers(ctx, tx)
}
