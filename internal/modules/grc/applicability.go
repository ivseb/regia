package grc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Applicabilità di requisiti normativi e controlli a perimetri (tabella applicability):
//   - senza regole né decisioni un requisito di una normativa abilitata vale per tutto il
//     tenant; un controllo vale ovunque;
//   - le regole (grc_applicability_rules) applicano un insieme di requisiti o di controlli
//     ai perimetri i cui fatti soddisfano la condizione, nel linguaggio delle condizioni dei
//     processi (es. "funzione_critica: sì"); un requisito citato da una regola attiva vale
//     solo dove la regola lo porta;
//   - le decisioni a mano (rule_key null) prevalgono sulle regole: "applicabile",
//     "non applicabile" con motivazione, "da valutare".
// Le righe delle regole si ricalcolano quando cambiano regole o cataloghi, a richiesta e
// ogni ora (RunRecompute), perché i fatti delle applicazioni cambiano nel tempo.

var scopeKinds = []string{"tenant", "legal_entity", "application", "process"}

// ScopeNames sono i nomi italiani dei perimetri.
var ScopeNames = map[string]string{"tenant": "Tutta l'organizzazione", "legal_entity": "Entità legale", "application": "Applicazione", "process": "Processo"}

var applicabilityStatuses = []string{"applicable", "not_applicable", "under_review"}

// Scope è un perimetro: il tenant, un'entità legale (org_units), un'applicazione o un processo.
type Scope struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Key  string `json:"key,omitempty"` // chiave dell'elemento (ARC-3) o codice dell'entità
	Name string `json:"name"`
}

func (sc Scope) id() string { return sc.Kind + ":" + sc.ID }

var tenantScope = Scope{Kind: "tenant", Name: ScopeNames["tenant"]}

// resolveScope trova il perimetro indicato da tipo e destinazione (chiave o id dell'elemento,
// id o codice dell'entità legale).
func (s *Service) resolveScope(ctx context.Context, tx pgx.Tx, kind, target string) (Scope, error) {
	target = strings.TrimSpace(target)
	switch kind {
	case "tenant", "":
		return tenantScope, nil
	case "legal_entity":
		var sc Scope
		err := tx.QueryRow(ctx, `select id, coalesce(code, ''), name from org_units where kind = 'legal_entity' and not archived
			and (id::text = $1 or lower(code) = lower($1))`, target).Scan(&sc.ID, &sc.Key, &sc.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return sc, engine.NotFound("target", "", "entità legale %q inesistente", target)
		}
		sc.Kind = kind
		return sc, err
	case "application", "process":
		el, err := s.eng.Get(ctx, tx, sysActor, target)
		if errors.Is(err, engine.ErrNotFound) && kind == "application" {
			// Anche per codice del catalogo (API-CLI).
			var id string
			if e := tx.QueryRow(ctx, "select element_id from ext_application where lower(code) = lower($1)", target).Scan(&id); e == nil {
				el, err = s.eng.Get(ctx, tx, sysActor, id)
			}
		}
		if err != nil {
			if errors.Is(err, engine.ErrNotFound) {
				return Scope{}, engine.NotFound("target", "element", "elemento %q inesistente", target)
			}
			return Scope{}, err
		}
		if kind == "application" && el.TypeKey != AppType {
			return Scope{}, engine.InvalidParams("validation.grc.target_not_application", "target", map[string]any{"key": el.Key}, "%s non è un'applicazione", el.Key)
		}
		if kind == "process" {
			st, err := loadSettings(ctx, tx)
			if err != nil {
				return Scope{}, err
			}
			if !slices.Contains(st.ProcessTypes, el.TypeKey) {
				return Scope{}, engine.InvalidParams("validation.grc.target_not_process", "target", map[string]any{"key": el.Key, "types": strings.Join(st.ProcessTypes, ", ")}, "%s non è un processo (tipi ammessi: %s)", el.Key, strings.Join(st.ProcessTypes, ", "))
			}
		}
		return Scope{Kind: kind, ID: el.ID, Key: el.Key, Name: el.Title}, nil
	}
	return Scope{}, engine.InvalidValue("scope", "perimetro non ammesso (%s)", strings.Join(scopeKinds, ", "))
}

var sysActor = engine.Actor{Permissions: []string{"element.read"}}

// scopedFacts è un perimetro con i suoi fatti.
type scopedFacts struct {
	Scope
	facts engine.Facts
}

// scopesOf elenca i perimetri di un tipo con i fatti, per valutare le regole.
func (s *Service) scopesOf(ctx context.Context, tx pgx.Tx, kind string, st *Settings) ([]scopedFacts, error) {
	var out []scopedFacts
	switch kind {
	case "legal_entity":
		rows, err := tx.Query(ctx, `select id, coalesce(code, ''), name from org_units where kind = 'legal_entity' and not archived order by name`)
		if err != nil {
			return nil, err
		}
		var sc Scope
		if _, err := pgx.ForEachRow(rows, []any{&sc.ID, &sc.Key, &sc.Name}, func() error {
			f := engine.Facts{}
			f.Set("nome", sc.Name)
			f.Set("codice", sc.Key)
			f.Set("tipo", "entità legale")
			out = append(out, scopedFacts{Scope{Kind: kind, ID: sc.ID, Key: sc.Key, Name: sc.Name}, f})
			return nil
		}); err != nil {
			return nil, err
		}
		return out, nil
	case "application", "process":
		types := []string{AppType}
		if kind == "process" {
			types = st.ProcessTypes
		}
		rows, err := tx.Query(ctx, `select e.id from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where t.key = any($1) and e.deleted_at is null and not e.is_draft and coalesce(o.category, '') not in ('done', 'cancelled')
			order by e.number`, types)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			el, err := s.eng.Get(ctx, tx, sysActor, id)
			if err != nil {
				return nil, err
			}
			f, err := s.eng.ElementFacts(ctx, tx, el)
			if err != nil {
				return nil, err
			}
			out = append(out, scopedFacts{Scope{Kind: kind, ID: el.ID, Key: el.Key, Name: el.Title}, f})
		}
		return out, nil
	}
	return nil, nil
}

// ---------------------------------------------------------------------
// Regole

// RuleInput è una regola di applicabilità.
type RuleInput struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Framework   string   `json:"framework,omitempty"` // requisiti di questa normativa…
	Refs        []string `json:"refs,omitempty"`      // …tutti, o solo questi riferimenti
	Controls    []string `json:"controls,omitempty"`  // oppure questi controlli (chiavi, es. GRC-3)
	Scope       string   `json:"scope"`               // legal_entity | application | process
	Condition   string   `json:"condition,omitempty"`
	Active      *bool    `json:"active,omitempty"`
}

// RuleView è una regola con i perimetri che oggi la soddisfano.
type RuleView struct {
	RuleInput
	ID      string   `json:"id"`
	Matches int      `json:"matches"`
	Missing []string `json:"missing"`
}

// RulesView è la pagina delle regole.
type RulesView struct {
	Items        []RuleView `json:"items"`
	RecomputedAt *time.Time `json:"recomputed_at"`
	CanEdit      bool       `json:"can_edit"`
}

type ruleRow struct {
	id                         string
	in                         RuleInput
	controlIDs                 []string
	reqIDs                     []string
	cond                       engine.Condition
	missing                    []string
	frameworkMissing, inactive bool
}

func (s *Service) loadRules(ctx context.Context, tx pgx.Tx) ([]*ruleRow, error) {
	rows, err := tx.Query(ctx, `select r.id, r.key, r.name, r.description, coalesce(r.framework_key, ''), r.refs, r.control_ids::text[],
		r.scope_kind, r.condition, r.active,
		coalesce((select array_agg(coalesce(s.key || '-' || e.number, e.id::text) order by e.number)
		  from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = any(r.control_ids)), '{}')
		from grc_applicability_rules r order by r.ordinal, r.key`)
	if err != nil {
		return nil, err
	}
	var out []*ruleRow
	for rows.Next() {
		r := &ruleRow{}
		var active bool
		if err := rows.Scan(&r.id, &r.in.Key, &r.in.Name, &r.in.Description, &r.in.Framework, &r.in.Refs, &r.controlIDs,
			&r.in.Scope, &r.in.Condition, &active, &r.in.Controls); err != nil {
			rows.Close()
			return nil, err
		}
		r.in.Active = &active
		r.inactive = !active
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range out {
		r.cond, _ = engine.ParseCondition(r.in.Condition)
		if r.in.Framework == "" {
			continue
		}
		ids, missing, err := frameworkTargets(ctx, tx, r.in.Framework, r.in.Refs)
		if err != nil {
			return nil, err
		}
		r.reqIDs, r.missing, r.frameworkMissing = ids, missing, ids == nil && len(missing) == 0
	}
	return out, nil
}

// frameworkTargets restituisce i requisiti valutabili della versione pubblicata (tutti, o
// i riferimenti indicati) e i riferimenti che non ci sono.
func frameworkTargets(ctx context.Context, tx pgx.Tx, key string, refs []string) ([]string, []string, error) {
	rows, err := tx.Query(ctx, `select r.id, r.ref from frameworks f
		join framework_versions v on v.framework_id = f.id and v.status = 'published'
		join requirements r on r.version_id = v.id
		where f.key = $1 and r.kind not in ('chapter','annex') and not exists (select 1 from requirements c where c.parent_id = r.id)
		order by f.owner_tenant_id is null, r.ordinal`, key)
	if err != nil {
		return nil, nil, err
	}
	byRef := map[string]string{}
	var all []string
	var id, ref string
	if _, err := pgx.ForEachRow(rows, []any{&id, &ref}, func() error {
		if _, ok := byRef[ref]; !ok {
			byRef[ref] = id
			all = append(all, id)
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return all, []string{}, nil
	}
	ids, missing := []string{}, []string{}
	for _, r := range refs {
		if id, ok := byRef[r]; ok {
			ids = append(ids, id)
		} else {
			missing = append(missing, r)
		}
	}
	return ids, missing, nil
}

// Rules elenca le regole con il numero di perimetri che le soddisfano oggi.
func (s *Service) Rules(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*RulesView, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rs, err := s.loadRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &RulesView{Items: []RuleView{}, RecomputedAt: st.RecomputedAt, CanEdit: actor.Has(PermAdmin)}
	counts := map[string]int{}
	rows, err := tx.Query(ctx, `select rule_key, count(distinct coalesce(target_element_id, target_org_unit_id)) from applicability
		where rule_key is not null and requirement_element_id is null group by rule_key`)
	if err != nil {
		return nil, err
	}
	var k string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&k, &n}, func() error { counts[k] = n; return nil }); err != nil {
		return nil, err
	}
	for _, r := range rs {
		missing := r.missing
		if missing == nil {
			missing = []string{}
		}
		if r.frameworkMissing {
			missing = append(missing, r.in.Framework)
		}
		out.Items = append(out.Items, RuleView{RuleInput: r.in, ID: r.id, Matches: counts[r.in.Key], Missing: missing})
	}
	return out, nil
}

// checkRule valida una regola e risolve i controlli citati.
func (s *Service) checkRule(ctx context.Context, tx pgx.Tx, r *RuleInput) ([]string, error) {
	r.Key, r.Name = strings.TrimSpace(r.Key), strings.TrimSpace(r.Name)
	if !keyRe.MatchString(r.Key) {
		return nil, engine.InvalidValue("key", "chiave della regola %q non valida (minuscole, cifre e _)", r.Key)
	}
	if r.Name == "" || len(r.Name) > 200 {
		return nil, engine.TooLong("name", 200, "nome della regola %s obbligatorio (massimo 200 caratteri)", r.Key)
	}
	if !slices.Contains([]string{"legal_entity", "application", "process"}, r.Scope) {
		return nil, engine.InvalidValue("scope", "perimetro della regola %s non ammesso (legal_entity, application, process)", r.Key)
	}
	if _, err := engine.ParseCondition(r.Condition); err != nil {
		return nil, engine.InvalidValue("condition", "condizione della regola %s non valida: %v", r.Key, err)
	}
	if (r.Framework == "") == (len(r.Controls) == 0) {
		return nil, engine.InvalidParams("validation.grc.rule_scope", "framework", map[string]any{"key": r.Key}, "la regola %s si applica ai requisiti di una normativa oppure a dei controlli", r.Key)
	}
	if r.Framework != "" {
		ids, missing, err := frameworkTargets(ctx, tx, r.Framework, r.Refs)
		if err != nil {
			return nil, err
		}
		if ids == nil && len(missing) == 0 {
			return nil, engine.InvalidParams("validation.grc.framework_unpublished", "framework", map[string]any{"framework": r.Framework}, "normativa %q senza versione pubblicata", r.Framework)
		}
		if len(missing) > 0 {
			return nil, engine.InvalidParams("validation.grc.refs_missing", "refs", map[string]any{"framework": r.Framework, "refs": strings.Join(missing, ", ")}, "riferimenti inesistenti in %s: %s", r.Framework, strings.Join(missing, ", "))
		}
		return nil, nil
	}
	ids := []string{}
	for _, k := range r.Controls {
		c, err := s.controlRef(ctx, tx, k)
		if err != nil {
			return nil, engine.NotFound("controls", "control", "controllo %q inesistente", k)
		}
		ids = append(ids, c)
	}
	return ids, nil
}

// SaveRules sostituisce le regole e ricalcola l'applicabilità.
func (s *Service) SaveRules(ctx context.Context, tx pgx.Tx, actor engine.Actor, rules []RuleInput) (*RulesView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if seen[r.Key] {
			return nil, engine.InvalidParams("validation.grc.key_repeated", "key", map[string]any{"key": r.Key}, "chiave %q ripetuta", r.Key)
		}
		seen[r.Key] = true
	}
	if _, err := tx.Exec(ctx, "delete from grc_applicability_rules"); err != nil {
		return nil, err
	}
	if _, err := s.addRules(ctx, tx, actor, rules); err != nil {
		return nil, err
	}
	if _, err := s.Recompute(ctx, tx, actor); err != nil {
		return nil, err
	}
	return s.Rules(ctx, tx, actor)
}

// addRules crea le regole, sostituendo quelle con la stessa chiave.
func (s *Service) addRules(ctx context.Context, tx pgx.Tx, actor engine.Actor, rules []RuleInput) ([]string, error) {
	keys := []string{}
	for i := range rules {
		r := rules[i]
		ctrl, err := s.checkRule(ctx, tx, &r)
		if err != nil {
			return nil, err
		}
		active := r.Active == nil || *r.Active
		var fw any
		if r.Framework != "" {
			fw = r.Framework
		}
		if r.Refs == nil {
			r.Refs = []string{}
		}
		if ctrl == nil {
			ctrl = []string{}
		}
		if _, err := tx.Exec(ctx, `insert into grc_applicability_rules (tenant_id, id, key, name, description, framework_key, refs, control_ids,
			scope_kind, condition, active, ordinal, updated_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			on conflict (tenant_id, key) do update set name = excluded.name, description = excluded.description,
			  framework_key = excluded.framework_key, refs = excluded.refs, control_ids = excluded.control_ids, scope_kind = excluded.scope_kind,
			  condition = excluded.condition, active = excluded.active, updated_by = excluded.updated_by, updated_at = now()`,
			db.NewID(), r.Key, r.Name, r.Description, fw, r.Refs, ctrl, r.Scope, strings.TrimSpace(r.Condition), active, i,
			nullable(actor.MemberID)); err != nil {
			return nil, err
		}
		keys = append(keys, r.Key)
	}
	if len(keys) > 0 {
		if err := audited(ctx, tx, actor, "grc.rules.save", "grc_applicability_rule", "", map[string]any{"rules": keys}); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// PreviewItem è un perimetro con l'esito di una condizione.
type PreviewItem struct {
	Scope
	Matched bool                `json:"matched"`
	Facts   map[string][]string `json:"facts"`
}

// Preview valuta una condizione sui perimetri di un tipo, senza salvare.
func (s *Service) Preview(ctx context.Context, tx pgx.Tx, actor engine.Actor, scope, condition string) ([]PreviewItem, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	cond, err := engine.ParseCondition(condition)
	if err != nil {
		return nil, engine.InvalidValue("condition", "condizione non valida: %v", err)
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	scs, err := s.scopesOf(ctx, tx, scope, st)
	if err != nil {
		return nil, err
	}
	out := []PreviewItem{}
	for _, sc := range scs {
		facts := map[string][]string{}
		for _, k := range cond.Keys() {
			for _, v := range sc.facts[k] {
				facts[k] = append(facts[k], factString(v))
			}
		}
		out = append(out, PreviewItem{Scope: sc.Scope, Matched: cond.Eval(sc.facts), Facts: facts})
	}
	return out, nil
}

func factString(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "sì"
		}
		return "no"
	case string:
		return x
	default:
		b, _ := jsonMarshal(x)
		return string(b)
	}
}

// RecomputeResult riassume un ricalcolo.
type RecomputeResult struct {
	Rows int       `json:"rows"`
	At   time.Time `json:"at"`
}

// Recompute ricalcola le righe di applicabilità delle regole. Le decisioni a mano restano.
func (s *Service) Recompute(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*RecomputeResult, error) {
	if ok, err := configured(ctx, tx); err != nil || !ok {
		return &RecomputeResult{At: s.Now()}, err
	}
	rules, err := s.loadRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	cache := map[string][]scopedFacts{}
	var reqIDs, ctrlIDs, kinds, targets, ruleKeys []string
	for _, r := range rules {
		if r.inactive || len(r.reqIDs)+len(r.controlIDs) == 0 {
			continue
		}
		scs, ok := cache[r.in.Scope]
		if !ok {
			if scs, err = s.scopesOf(ctx, tx, r.in.Scope, st); err != nil {
				return nil, err
			}
			cache[r.in.Scope] = scs
		}
		for _, sc := range scs {
			if !r.cond.Eval(sc.facts) {
				continue
			}
			for _, id := range r.reqIDs {
				reqIDs, ctrlIDs = append(reqIDs, id), append(ctrlIDs, "")
				kinds, targets, ruleKeys = append(kinds, sc.Kind), append(targets, sc.ID), append(ruleKeys, r.in.Key)
			}
			for _, id := range r.controlIDs {
				reqIDs, ctrlIDs = append(reqIDs, ""), append(ctrlIDs, id)
				kinds, targets, ruleKeys = append(kinds, sc.Kind), append(targets, sc.ID), append(ruleKeys, r.in.Key)
			}
		}
	}
	if _, err := tx.Exec(ctx, "delete from applicability where rule_key is not null and requirement_element_id is null"); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `insert into applicability (tenant_id, id, requirement_id, control_element_id, scope_kind, target_element_id,
		target_org_unit_id, status, rule_key, justification)
		select app_tenant(), gen_random_uuid(), nullif(x.req, '')::uuid, nullif(x.ctrl, '')::uuid, x.kind,
		  case when x.kind = 'legal_entity' then null else x.target::uuid end,
		  case when x.kind = 'legal_entity' then x.target::uuid end, 'applicable', x.rule, 'Regola ' || x.rule
		from unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[]) as x(req, ctrl, kind, target, rule)
		on conflict do nothing`, reqIDs, ctrlIDs, kinds, targets, ruleKeys)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	if _, err := tx.Exec(ctx, `insert into grc_settings (tenant_id, recomputed_at) values (app_tenant(), $1)
		on conflict (tenant_id) do update set recomputed_at = excluded.recomputed_at`, now); err != nil {
		return nil, err
	}
	if actor.MemberID != "" {
		if err := audited(ctx, tx, actor, "grc.applicability.recompute", "grc", "", map[string]any{"rows": tag.RowsAffected()}); err != nil {
			return nil, err
		}
	}
	return &RecomputeResult{Rows: int(tag.RowsAffected()), At: now}, nil
}

// ---------------------------------------------------------------------
// Decisioni a mano

// Decision è una decisione di applicabilità su un perimetro.
type Decision struct {
	Scope         string `json:"scope"`
	Target        string `json:"target,omitempty"`
	Status        string `json:"status"`
	Justification string `json:"justification,omitempty"`
}

// SetRequirementApplicability sostituisce le decisioni a mano su un requisito normativo.
func (s *Service) SetRequirementApplicability(ctx context.Context, tx pgx.Tx, actor engine.Actor, reqID string, items []Decision) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from requirements where id::text = $1)", reqID).Scan(&ok); err != nil || !ok {
		return errors.Join(err, engine.ErrNotFound)
	}
	return s.setApplicability(ctx, tx, actor, "requirement_id", reqID, items)
}

func (s *Service) setApplicability(ctx context.Context, tx pgx.Tx, actor engine.Actor, col, subject string, items []Decision) error {
	if len(items) > 1000 {
		return engine.InvalidCode("validation.grc.too_many_decisions", "items", "troppe decisioni")
	}
	if _, err := tx.Exec(ctx, "delete from applicability where "+col+" = $1 and requirement_element_id is null and rule_key is null", subject); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range items {
		if !slices.Contains(applicabilityStatuses, d.Status) {
			return engine.InvalidValue("status", "stato non ammesso (applicable, not_applicable, under_review)")
		}
		d.Justification = strings.TrimSpace(d.Justification)
		if d.Status == "not_applicable" && d.Justification == "" {
			return engine.InvalidCode("validation.grc.not_applicable_reason", "justification", "motiva la non applicabilità")
		}
		if len(d.Justification) > 2000 {
			return engine.InvalidCode("validation.grc.text_too_long", "justification", "motivazione troppo lunga")
		}
		sc, err := s.resolveScope(ctx, tx, d.Scope, d.Target)
		if err != nil {
			return err
		}
		if seen[sc.id()] {
			return engine.InvalidParams("validation.grc.scope_repeated", "target", map[string]any{"name": sc.Name}, "perimetro %s ripetuto", sc.Name)
		}
		seen[sc.id()] = true
		var el, ou any
		switch sc.Kind {
		case "legal_entity":
			ou = sc.ID
		case "application", "process":
			el = sc.ID
		}
		// La decisione prende il posto della riga calcolata da una regola.
		if _, err := tx.Exec(ctx, `delete from applicability where `+col+` = $1 and requirement_element_id is null and scope_kind = $2
			and target_element_id is not distinct from $3::uuid and target_org_unit_id is not distinct from $4::uuid`, subject, sc.Kind, el, ou); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into applicability (tenant_id, id, `+col+`, scope_kind, target_element_id, target_org_unit_id,
			status, justification, decided_by) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`,
			db.NewID(), subject, sc.Kind, el, ou, d.Status, nullable(d.Justification), nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	// Le righe delle regole tolte da una decisione ritirata tornano.
	if _, err := s.Recompute(ctx, tx, engine.Actor{}); err != nil {
		return err
	}
	return audited(ctx, tx, actor, "grc.applicability.set", col, subject, map[string]any{"decisions": len(items)})
}
