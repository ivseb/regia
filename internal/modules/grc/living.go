package grc

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
)

// Controlli vivi (ondata 3, proposta 4). L'efficacia di un controllo la PROPONE il sistema, con
// una regola dichiarativa sui fatti calcolati dal grafo; la CONFERMA una persona con la nota
// (Decide): lo stato ufficiale (ext_control.effectiveness) non cambia mai da solo. Le evidenze
// automatiche dai rilasci, dai gate e dagli SBOM si registrano con SyncLiving (collettori
// graph.*), con data e validità secondo la frequenza del controllo: scadute, tornano a rinnovarsi
// alla sincronizzazione successiva.

// LivingFacts sono i fatti che una regola può usare, nell'ordine in cui si offrono.
var LivingFacts = []string{
	"vuln_critiche_scadute", "vuln_scadute", "gate_superati_90g", "gate_falliti_90g",
	"rilasci_approvati_90g", "rilasci_senza_sbom", "giorni_riesame_accessi", "evidenze_valide", "evidenze_scadute",
}

// LivingSources sono i dati del grafo che diventano evidenze automatiche del controllo.
var LivingSources = []string{"release", "gate", "sbom", "rule"}

const (
	livingWindowDays = 90
	collectorAccess  = "iam.access_review" // collettore del riesame degli accessi (esterno al grafo)
	// ExpectedLimit: applicazioni del perimetro per cui si cercano le evidenze a ogni sincronizzazione.
	syncAppLimit = 25
)

// EffRuleInput è la regola di efficacia di un controllo.
type EffRuleInput struct {
	Name          string   `json:"name"`
	EffectiveWhen string   `json:"effective_when"`
	AtRiskWhen    string   `json:"at_risk_when"`
	Sources       []string `json:"sources"`
	Active        *bool    `json:"active"`
}

// EffRule è la regola salvata.
type EffRule struct {
	Name          string     `json:"name"`
	EffectiveWhen string     `json:"effective_when"`
	AtRiskWhen    string     `json:"at_risk_when"`
	Sources       []string   `json:"sources"`
	Active        bool       `json:"active"`
	UpdatedBy     string     `json:"updated_by"`
	UpdatedAt     *time.Time `json:"updated_at"`
}

// FactValue è il valore di un fatto; Value è assente se il dato non esiste (es. nessun riesame).
type FactValue struct {
	Key   string   `json:"key"`
	Value *float64 `json:"value"`
}

// ProposalDecision è una decisione su una proposta.
type ProposalDecision struct {
	ID        string      `json:"id"`
	Proposed  string      `json:"proposed"`
	Decision  string      `json:"decision"` // confirmed | not_confirmed
	Previous  string      `json:"previous"`
	Resulting string      `json:"resulting"`
	Note      string      `json:"note"`
	By        string      `json:"by"`
	At        time.Time   `json:"at"`
	Facts     []FactValue `json:"facts"`
}

// ProposalCan sono le azioni permesse all'attore.
type ProposalCan struct {
	Edit   bool `json:"edit"`   // regola e sincronizzazione (grc.admin)
	Decide bool `json:"decide"` // conferma o non conferma (grc.admin)
}

// Proposal è la proposta di efficacia di un controllo.
type Proposal struct {
	Control    string             `json:"control"`
	Rule       *EffRule           `json:"rule"`
	State      string             `json:"state"`    // none (nessuna regola) | effective | at_risk | not_assessed
	Proposed   string             `json:"proposed"` // valore ufficiale proposto: effective | partial | ""
	Official   string             `json:"official"`
	Pending    bool               `json:"pending"` // c'è una proposta diversa dallo stato ufficiale, da decidere
	Dismissed  bool               `json:"dismissed"`
	Facts      []FactValue        `json:"facts"`     // quelli che hanno deciso la proposta
	AllFacts   []FactValue        `json:"all_facts"` // tutti i fatti calcolati
	Perimeter  int                `json:"perimeter"` // applicazioni nel perimetro del controllo
	Everywhere bool               `json:"everywhere"`
	Decisions  []ProposalDecision `json:"decisions"`
	Can        ProposalCan        `json:"can"`
}

func proposedOfficial(state string) string {
	switch state {
	case "effective":
		return "effective"
	case "at_risk":
		return "partial"
	}
	return ""
}

func checkRule(in *EffRuleInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.EffectiveWhen = strings.TrimSpace(in.EffectiveWhen)
	in.AtRiskWhen = strings.TrimSpace(in.AtRiskWhen)
	if len(in.Name) > 200 {
		return engine.TooLong("name", 200, "nome troppo lungo")
	}
	if in.EffectiveWhen == "" && in.AtRiskWhen == "" {
		return engine.InvalidCode("validation.grc.rule_empty", "effective_when", "scrivi almeno una condizione: «efficace se» o «a rischio se»")
	}
	for field, src := range map[string]string{"effective_when": in.EffectiveWhen, "at_risk_when": in.AtRiskWhen} {
		if len(src) > 2000 {
			return engine.TooLong(field, 2000, "condizione troppo lunga")
		}
		c, err := engine.ParseCondition(src)
		if err != nil {
			return engine.InvalidParams("validation.grc.rule_condition_invalid", field, map[string]any{"detail": err.Error()}, "condizione non valida: %v", err)
		}
		for _, k := range c.Keys() {
			if !slices.Contains(LivingFacts, k) {
				return engine.InvalidParams("validation.grc.rule_fact_unknown", field, map[string]any{"fact": k}, "dato %q sconosciuto per le regole di efficacia", k)
			}
		}
	}
	if in.Sources == nil {
		in.Sources = []string{}
	}
	for _, s := range in.Sources {
		if !slices.Contains(LivingSources, s) {
			return engine.InvalidParams("validation.grc.rule_source_invalid", "sources", map[string]any{"source": s}, "sorgente %q non ammessa (release, gate, sbom, rule)", s)
		}
	}
	return nil
}

// SetEffectivenessRule salva la regola di efficacia di un controllo (grc.admin). Le evidenze
// automatiche si collegano alla prima sincronizzazione.
func (s *Service) SetEffectivenessRule(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in EffRuleInput) (*Proposal, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := checkRule(&in); err != nil {
		return nil, err
	}
	active := in.Active == nil || *in.Active
	if _, err := tx.Exec(ctx, `insert into grc_effectiveness_rules (tenant_id, control_element_id, name, effective_when, at_risk_when, sources, active, updated_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)
		on conflict (tenant_id, control_element_id) do update set name = excluded.name, effective_when = excluded.effective_when,
		  at_risk_when = excluded.at_risk_when, sources = excluded.sources, active = excluded.active, updated_by = excluded.updated_by, updated_at = now()`,
		id, in.Name, in.EffectiveWhen, in.AtRiskWhen, in.Sources, active, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.effectiveness_rule", ControlType, id, map[string]any{
		"effective_when": in.EffectiveWhen, "at_risk_when": in.AtRiskWhen, "sources": in.Sources, "active": active}); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, tx, actor, id)
}

// DeleteEffectivenessRule toglie la regola (le decisioni passate restano) (grc.admin).
func (s *Service) DeleteEffectivenessRule(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Proposal, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "delete from grc_effectiveness_rules where control_element_id = $1", id); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.effectiveness_rule.delete", ControlType, id, nil); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, tx, actor, id)
}

func loadRule(ctx context.Context, tx pgx.Tx, id string) (*EffRule, error) {
	r := &EffRule{}
	var t time.Time
	err := tx.QueryRow(ctx, `select x.name, x.effective_when, x.at_risk_when, x.sources, x.active, coalesce(m.display_name, ''), x.updated_at
		from grc_effectiveness_rules x left join members m on m.tenant_id = x.tenant_id and m.id = x.updated_by
		where x.control_element_id = $1`, id).Scan(&r.Name, &r.EffectiveWhen, &r.AtRiskWhen, &r.Sources, &r.Active, &r.UpdatedBy, &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	r.UpdatedAt = &t
	return r, err
}

// controlApps sono le applicazioni del perimetro del controllo: quelle in cui serve per i
// requisiti che copre; se non ce n'è una sola, tutte le applicazioni del tenant.
func (s *Service) controlApps(ctx context.Context, tx pgx.Tx, m *model, c *ctrlModel) (apps []appRef, everywhere bool, err error) {
	ids := []string{}
	for _, r := range c.reqs {
		for _, p := range m.pairs(r) {
			for _, pc := range p.controls {
				if pc.c != c {
					continue
				}
				if sc, ok := m.scopes[p.scope]; ok && sc.Kind == "application" && sc.ID != "" && !slices.Contains(ids, sc.ID) {
					ids = append(ids, sc.ID)
				}
			}
		}
	}
	q := `select e.id::text, sp.key || '-' || e.number from ext_application a
		join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id`
	var rows pgx.Rows
	if len(ids) == 0 {
		everywhere = true
		rows, err = tx.Query(ctx, q+" order by e.number")
	} else {
		rows, err = tx.Query(ctx, q+" where e.id = any($1::uuid[]) order by e.number", ids)
	}
	if err != nil {
		return nil, false, err
	}
	var a appRef
	_, err = pgx.ForEachRow(rows, []any{&a.ID, &a.Key}, func() error {
		apps = append(apps, a)
		return nil
	})
	return apps, everywhere, err
}

type appRef struct{ ID, Key string }

func appIDs(apps []appRef) []string {
	out := make([]string, len(apps))
	for i, a := range apps {
		out[i] = a.ID
	}
	return out
}

// livingFactValues calcola i fatti del controllo sul suo perimetro (dati già nel grafo: non
// scrive nulla). Una chiave assente nella mappa = dato inesistente.
func (s *Service) livingFactValues(ctx context.Context, tx pgx.Tx, m *model, c *ctrlModel, apps []appRef) (map[string]float64, error) {
	now := s.Now()
	ids := appIDs(apps)
	out := map[string]float64{}
	var crit, over int
	if err := tx.QueryRow(ctx, `select count(*) filter (where severity = 'critical'), count(*) from findings
		where application_element_id = any($1::uuid[]) and stage in ('open','taken','in_progress') and due_at is not null and due_at < $2`,
		ids, now).Scan(&crit, &over); err != nil {
		return nil, err
	}
	out["vuln_critiche_scadute"], out["vuln_scadute"] = float64(crit), float64(over)
	since := now.AddDate(0, 0, -livingWindowDays)
	var pass, fail int
	if err := tx.QueryRow(ctx, `select count(*) filter (where outcome = 'pass'), count(*) filter (where outcome = 'fail')
		from gate_decisions where application_element_id = any($1::uuid[]) and created_at >= $2`, ids, since).Scan(&pass, &fail); err != nil {
		return nil, err
	}
	out["gate_superati_90g"], out["gate_falliti_90g"] = float64(pass), float64(fail)
	var appr, noSbom int
	if err := tx.QueryRow(ctx, `select count(*) filter (where r.approved_at is not null),
		count(*) filter (where not exists (select 1 from sboms b where b.tenant_id = r.tenant_id and b.service_id = r.service_id and lower(b.version) = lower(r.version)))
		from releases r join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
		join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id and en.is_production
		where sv.application_element_id = any($1::uuid[]) and r.released_at >= $2`, ids, since).Scan(&appr, &noSbom); err != nil {
		return nil, err
	}
	out["rilasci_approvati_90g"], out["rilasci_senza_sbom"] = float64(appr), float64(noSbom)
	var last *time.Time
	if err := tx.QueryRow(ctx, `select max(valid_from) from ext_evidence where collector = $1 and status = 'valid'`, collectorAccess).Scan(&last); err != nil {
		return nil, err
	}
	if last != nil {
		out["giorni_riesame_accessi"] = float64(int(now.Sub(*last).Hours() / 24))
	}
	var valid, expired int
	for _, ev := range c.evidence {
		switch {
		case ev.valid(m.now):
			valid++
		case ev.Status == "valid":
			expired++
		}
	}
	out["evidenze_valide"], out["evidenze_scadute"] = float64(valid), float64(expired)
	return out, nil
}

func factList(keys []string, vals map[string]float64) []FactValue {
	out := make([]FactValue, 0, len(keys))
	for _, k := range keys {
		fv := FactValue{Key: k}
		if v, ok := vals[k]; ok {
			x := v
			fv.Value = &x
		}
		out = append(out, fv)
	}
	return out
}

// evalRule applica la regola ai fatti: «a rischio se» prevale su «efficace se»; se nessuna
// delle due è vera la proposta è «non valutato».
func evalRule(r *EffRule, vals map[string]float64) (state string, keys []string) {
	f := engine.Facts{}
	for k, v := range vals {
		f.Set(k, v)
	}
	risk, _ := engine.ParseCondition(r.AtRiskWhen)
	eff, _ := engine.ParseCondition(r.EffectiveWhen)
	if r.AtRiskWhen != "" && risk.Eval(f) {
		return "at_risk", risk.Keys()
	}
	if r.EffectiveWhen != "" && eff.Eval(f) {
		return "effective", eff.Keys()
	}
	keys = risk.Keys()
	for _, k := range eff.Keys() {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	return "not_assessed", keys
}

func (s *Service) proposalFor(ctx context.Context, tx pgx.Tx, actor engine.Actor, m *model, c *ctrlModel) (*Proposal, error) {
	p := &Proposal{Control: c.Key, State: "none", Official: c.Effectiveness, Facts: []FactValue{}, AllFacts: []FactValue{}, Decisions: []ProposalDecision{},
		Can: ProposalCan{Edit: actor.Has(PermAdmin), Decide: actor.Has(PermAdmin)}}
	rule, err := loadRule(ctx, tx, c.ID)
	if err != nil {
		return nil, err
	}
	p.Rule = rule
	rows, err := tx.Query(ctx, `select d.id::text, d.proposed, d.decision, d.previous, d.resulting, d.note, coalesce(m.display_name, ''), d.decided_at, d.reason
		from grc_effectiveness_decisions d left join members m on m.tenant_id = d.tenant_id and m.id = d.decided_by
		where d.control_element_id = $1 order by d.decided_at desc limit 5`, c.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d ProposalDecision
		var raw []byte
		if err := rows.Scan(&d.ID, &d.Proposed, &d.Decision, &d.Previous, &d.Resulting, &d.Note, &d.By, &d.At, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(raw, &d.Facts)
		if d.Facts == nil {
			d.Facts = []FactValue{}
		}
		p.Decisions = append(p.Decisions, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if rule == nil || !rule.Active {
		return p, nil
	}
	apps, everywhere, err := s.controlApps(ctx, tx, m, c)
	if err != nil {
		return nil, err
	}
	vals, err := s.livingFactValues(ctx, tx, m, c, apps)
	if err != nil {
		return nil, err
	}
	state, keys := evalRule(rule, vals)
	p.State, p.Perimeter, p.Everywhere = state, len(apps), everywhere
	p.Proposed = proposedOfficial(state)
	p.Facts = factList(keys, vals)
	p.AllFacts = factList(LivingFacts, vals)
	// Una proposta già rifiutata (stesso stato) non si ripropone finché lo stato non cambia.
	if len(p.Decisions) > 0 && p.Decisions[0].Decision == "not_confirmed" && p.Decisions[0].Proposed == state {
		p.Dismissed = true
	}
	p.Pending = p.Proposed != "" && p.Proposed != c.Effectiveness && !p.Dismissed
	return p, nil
}

func (s *Service) controlAccess(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if CanRead(actor) {
		return nil
	}
	var mine bool
	if actor.MemberID != "" {
		if err := tx.QueryRow(ctx, "select exists (select 1 from ext_control where element_id = $1 and owner_id = $2)", id, actor.MemberID).Scan(&mine); err != nil {
			return err
		}
	}
	if !mine {
		return engine.ErrForbidden
	}
	return nil
}

// Proposal restituisce la proposta di efficacia del controllo (chi legge la conformità o ne è
// il responsabile).
func (s *Service) Proposal(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Proposal, error) {
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.controlAccess(ctx, tx, actor, id); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	c := m.ctrls[id]
	if c == nil {
		return nil, engine.ErrNotFound
	}
	return s.proposalFor(ctx, tx, actor, m, c)
}

// ProposalBrief è la proposta in sintesi, per l'elenco dei controlli.
type ProposalBrief struct {
	Control  string `json:"control"`
	State    string `json:"state"`
	Proposed string `json:"proposed"`
	Pending  bool   `json:"pending"`
}

// Proposals elenca le proposte dei controlli che hanno una regola attiva.
func (s *Service) Proposals(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]ProposalBrief, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "select control_element_id::text from grc_effectiveness_rules where active")
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []ProposalBrief{}
	if len(ids) == 0 {
		return out, nil
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, c := range m.ctrlOrder {
		if !slices.Contains(ids, c.ID) {
			continue
		}
		p, err := s.proposalFor(ctx, tx, actor, m, c)
		if err != nil {
			return nil, err
		}
		out = append(out, ProposalBrief{Control: c.Key, State: p.State, Proposed: p.Proposed, Pending: p.Pending})
	}
	return out, nil
}

// DecideInput è la decisione di un responsabile sulla proposta.
type DecideInput struct {
	Decision      string `json:"decision"`      // confirm | not_confirm
	Effectiveness string `json:"effectiveness"` // solo con not_confirm: stato scelto al posto di quello proposto (facoltativo)
	Note          string `json:"note"`
}

// Decide conferma o non conferma la proposta (grc.admin). La proposta si ricalcola qui, non si
// fida del client. «Non confermare» vuole la nota; lo stato ufficiale cambia solo con la conferma
// o con uno stato scelto dalla persona, sempre con la nota nell'audit.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in DecideInput) (*Proposal, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in.Decision != "confirm" && in.Decision != "not_confirm" {
		return nil, engine.InvalidCode("validation.grc.proposal_decision_invalid", "decision", "decisione non ammessa (confirm, not_confirm)")
	}
	in.Note = strings.TrimSpace(in.Note)
	if in.Decision == "not_confirm" && in.Note == "" {
		return nil, engine.InvalidCode("validation.grc.proposal_note_required", "note", "spiega perché non confermi la proposta")
	}
	if len(in.Note) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	c := m.ctrls[id]
	if c == nil {
		return nil, engine.ErrNotFound
	}
	p, err := s.proposalFor(ctx, tx, actor, m, c)
	if err != nil {
		return nil, err
	}
	if p.State != "effective" && p.State != "at_risk" {
		return nil, engine.InvalidCode("validation.grc.proposal_not_available", "decision", "il sistema non ha una proposta da decidere per questo controllo")
	}
	result := c.Effectiveness
	decision := "not_confirmed"
	if in.Decision == "confirm" {
		decision = "confirmed"
		note := in.Note
		if note == "" {
			note = "Proposta del sistema confermata."
		}
		if _, err := s.Assess(ctx, tx, actor, id, p.Proposed, note); err != nil {
			return nil, err
		}
		result = p.Proposed
	} else if in.Effectiveness != "" {
		if _, err := s.Assess(ctx, tx, actor, id, in.Effectiveness, in.Note); err != nil {
			return nil, err
		}
		result = in.Effectiveness
	}
	facts, _ := json.Marshal(p.Facts)
	if _, err := tx.Exec(ctx, `insert into grc_effectiveness_decisions (tenant_id, control_element_id, proposed, reason, decision, previous, resulting, note, decided_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, id, p.State, facts, decision, c.Effectiveness, result, in.Note, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", "proposta_efficacia", decision, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.proposal."+in.Decision, ControlType, id, map[string]any{
		"proposed": p.State, "previous": c.Effectiveness, "resulting": result, "note": in.Note, "facts": p.Facts}); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, tx, actor, id)
}

// SyncResult riassume una sincronizzazione delle evidenze dal grafo.
type SyncResult struct {
	Controls int `json:"controls"`
	Created  int `json:"created"`
}

// SyncLiving registra le evidenze automatiche dei controlli con una regola attiva: l'ultimo
// rilascio approvato, l'ultimo gate superato e l'ultimo SBOM di ogni applicazione del
// perimetro, e, se la proposta è «efficace», la verifica della regola. Ripetere la chiamata
// non duplica; un'evidenza scaduta si rinnova se esiste un dato più recente (grc.admin).
// Se only non è vuoto limita la sincronizzazione a quel controllo.
func (s *Service) SyncLiving(ctx context.Context, tx pgx.Tx, actor engine.Actor, only string) (*SyncResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	q := "select control_element_id::text, sources from grc_effectiveness_rules where active and cardinality(sources) > 0"
	args := []any{}
	if only != "" {
		id, err := s.controlRef(ctx, tx, only)
		if err != nil {
			return nil, err
		}
		q += " and control_element_id = $1"
		args = append(args, id)
	}
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type job struct {
		id      string
		sources []string
	}
	var jobs []job
	var j job
	if _, err := pgx.ForEachRow(rows, []any{&j.id, &j.sources}, func() error {
		jobs = append(jobs, j)
		return nil
	}); err != nil {
		return nil, err
	}
	res := &SyncResult{}
	if len(jobs) == 0 {
		return res, nil
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, jb := range jobs {
		c := m.ctrls[jb.id]
		if c == nil {
			continue
		}
		res.Controls++
		apps, _, err := s.controlApps(ctx, tx, m, c)
		if err != nil {
			return nil, err
		}
		if len(apps) > syncAppLimit {
			apps = apps[:syncAppLimit]
		}
		days := validityDays(c.Frequency, m.settings)
		for _, src := range jb.sources {
			n, err := s.syncSource(ctx, tx, actor, m, c, src, apps, days)
			if err != nil {
				return nil, err
			}
			res.Created += n
		}
	}
	return res, nil
}

func (s *Service) syncSource(ctx context.Context, tx pgx.Tx, actor engine.Actor, m *model, c *ctrlModel, src string, apps []appRef, days int) (int, error) {
	created := 0
	record := func(in AutoEvidence, at time.Time) error {
		if at.AddDate(0, 0, days).Before(m.now) {
			return nil // più vecchio della validità: nascerebbe già scaduto
		}
		in.Collector = "graph." + src
		in.Controls = []string{c.Key}
		in.OccurredAt = &at
		in.ValidUntil = at.AddDate(0, 0, days).Format(time.DateOnly)
		r, err := s.RecordAutomatic(ctx, tx, actor, in)
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Created {
			created++
		}
		return nil
	}
	switch src {
	case "release", "gate", "sbom":
		for _, a := range apps {
			var id, label, link string
			var at time.Time
			var err error
			switch src {
			case "release":
				err = tx.QueryRow(ctx, `select r.id::text, sv.name || ' ' || r.version, coalesce(r.release_url, ''), r.approved_at
					from releases r join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
					join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id and en.is_production
					where sv.application_element_id = $1 and r.approved_at is not null order by r.approved_at desc limit 1`, a.ID).Scan(&id, &label, &link, &at)
			case "gate":
				err = tx.QueryRow(ctx, `select id::text, environment || ' ' || coalesce(version, ''), '', created_at
					from gate_decisions where application_element_id = $1 and outcome = 'pass' order by created_at desc limit 1`, a.ID).Scan(&id, &label, &link, &at)
			default:
				err = tx.QueryRow(ctx, `select b.id::text, sv.name || ' ' || b.version, '', b.received_at
					from sboms b join services sv on sv.tenant_id = b.tenant_id and sv.id = b.service_id
					where sv.application_element_id = $1 order by b.received_at desc limit 1`, a.ID).Scan(&id, &label, &link, &at)
			}
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return created, err
			}
			title := map[string]string{"release": "Rilascio approvato: ", "gate": "Gate di rilascio superato: ", "sbom": "SBOM ricevuto: "}[src] + strings.TrimSpace(label)
			if err := record(AutoEvidence{ExternalRef: fmt.Sprintf("live:%s:%s:%s", src, id, c.Key), Title: title,
				Summary: "Dato letto dal grafo di Regia.", URL: link, Application: a.Key}, at); err != nil {
				return created, err
			}
		}
	case "rule":
		p, err := s.proposalFor(ctx, tx, engine.Actor{Permissions: []string{"*"}}, m, c)
		if err != nil {
			return created, err
		}
		if p.State != "effective" {
			return created, nil
		}
		for _, ev := range c.evidence {
			if ev.Collector == "graph.rule" && ev.valid(m.now) {
				return created, nil // ancora valida: si rinnova alla scadenza
			}
		}
		parts := []string{}
		for _, f := range p.Facts {
			if f.Value != nil {
				parts = append(parts, fmt.Sprintf("%s = %g", f.Key, *f.Value))
			}
		}
		at := m.now.UTC().Truncate(time.Hour)
		name := p.Rule.Name
		if name == "" {
			name = c.Key
		}
		err = record(AutoEvidence{ExternalRef: fmt.Sprintf("live:rule:%s:%s", c.Key, at.Format("2006-01-02")), Title: "Verifica automatica: " + name,
			Summary: "Regola di efficacia soddisfatta. " + strings.Join(parts, "; ")}, at)
		if err != nil {
			return created, err
		}
	}
	return created, nil
}
