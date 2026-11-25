package grc

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Controlli interni: elementi "control" dello spazio GRC con il loro processo (pacchetto
// control_standard), un responsabile, la frequenza di verifica, il tipo, l'automazione e
// l'efficacia valutata dalla compliance. Un controllo copre requisiti di più normative
// (control_requirements) e vale ovunque, salvo decisioni o regole di applicabilità.

var collectorRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,62}$`)

// MappedInput è un requisito coperto da un controllo.
type MappedInput struct {
	Ref      string `json:"ref"`                // "dora:Art. 9"
	Coverage string `json:"coverage,omitempty"` // full (default) | partial
	Note     string `json:"note,omitempty"`
}

// ControlInput crea un controllo.
type ControlInput struct {
	Title        string        `json:"title"`
	Text         string        `json:"text"`
	OwnerID      string        `json:"owner_id"`
	Frequency    string        `json:"frequency"`
	Kind         string        `json:"kind"`
	Automation   string        `json:"automation"`
	Collectors   []string      `json:"collectors"`
	Requirements []MappedInput `json:"requirements"`
}

// ControlUpdate modifica i dati del controllo (titolo e testo con PATCH dell'elemento).
type ControlUpdate struct {
	OwnerID    *string   `json:"owner_id"`
	Frequency  *string   `json:"frequency"`
	Kind       *string   `json:"kind"`
	Automation *string   `json:"automation"`
	Collectors *[]string `json:"collectors"`
}

func checkControlFields(freq, kind, auto string, collectors []string) error {
	if _, ok := frequencies[freq]; !ok {
		return engine.InvalidValue("frequency", "frequenza non ammessa (continuous, monthly, quarterly, semiannual, annual, biennial)")
	}
	if _, ok := controlKinds[kind]; !ok {
		return engine.InvalidValue("kind", "tipo non ammesso (preventive, detective, corrective)")
	}
	if _, ok := automations[auto]; !ok {
		return engine.InvalidValue("automation", "automazione non ammessa (manual, semi, automatic)")
	}
	if len(collectors) > 20 {
		return engine.InvalidCode("validation.grc.collectors_too_many", "collectors", "al massimo 20 raccolte automatiche")
	}
	for _, c := range collectors {
		if !collectorRe.MatchString(c) {
			return engine.InvalidValue("collectors", "raccolta %q non valida (es. devsecops.release)", c)
		}
	}
	return nil
}

// controlRef trova l'id di un controllo per chiave (GRC-3) o id.
func (s *Service) controlRef(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	el, err := s.eng.Get(ctx, tx, sysActor, ref)
	if err != nil {
		return "", err
	}
	if el.TypeKey != ControlType {
		return "", engine.ErrNotFound
	}
	return el.ID, nil
}

// CreateControl crea un controllo con il responsabile e i requisiti coperti (grc.admin).
func (s *Service) CreateControl(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ControlInput) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in.Frequency == "" {
		in.Frequency = "annual"
	}
	if in.Kind == "" {
		in.Kind = "preventive"
	}
	if in.Automation == "" {
		in.Automation = "manual"
	}
	if in.Collectors == nil {
		in.Collectors = []string{}
	}
	if err := checkControlFields(in.Frequency, in.Kind, in.Automation, in.Collectors); err != nil {
		return nil, err
	}
	if ok, err := configured(ctx, tx); err != nil || !ok {
		return nil, errors.Join(err, engine.InvalidCode("validation.grc.module_not_configured", "type", "modulo della conformità non configurato nel tenant"))
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: ControlType, Title: in.Title,
		BodyText: strings.TrimSpace(in.Text), DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_control (tenant_id, element_id, owner_id, frequency, control_kind, automation, collectors)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`, el.ID, nullable(in.OwnerID), in.Frequency, in.Kind, in.Automation, in.Collectors); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "owner_id", "membro inesistente")
		}
		return nil, err
	}
	if in.OwnerID != "" {
		if err := engine.SetStakeholder(ctx, tx, actor, el.ID, RoleOwner, in.OwnerID, "", true); err != nil {
			return nil, err
		}
	}
	if len(in.Requirements) > 0 {
		if err := s.setMapped(ctx, tx, el.ID, in.Requirements); err != nil {
			return nil, err
		}
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.create", ControlType, el.ID, map[string]any{"key": el.Key, "requirements": len(in.Requirements)}); err != nil {
		return nil, err
	}
	return s.Control(ctx, tx, actor, el.ID)
}

// UpdateControl modifica responsabile, frequenza, tipo, automazione e raccolte (grc.admin).
func (s *Service) UpdateControl(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ControlUpdate) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var owner *string
	var freq, kind, auto string
	var coll []string
	if err := tx.QueryRow(ctx, "select owner_id::text, frequency, control_kind, automation, collectors from ext_control where element_id = $1 for update", id).
		Scan(&owner, &freq, &kind, &auto, &coll); err != nil {
		return nil, err
	}
	if in.Frequency != nil {
		freq = *in.Frequency
	}
	if in.Kind != nil {
		kind = *in.Kind
	}
	if in.Automation != nil {
		auto = *in.Automation
	}
	if in.Collectors != nil {
		coll = *in.Collectors
		if coll == nil {
			coll = []string{}
		}
	}
	if err := checkControlFields(freq, kind, auto, coll); err != nil {
		return nil, err
	}
	newOwner := owner
	if in.OwnerID != nil {
		newOwner = nil
		if *in.OwnerID != "" {
			newOwner = in.OwnerID
		}
	}
	if _, err := tx.Exec(ctx, `update ext_control set owner_id = $2, frequency = $3, control_kind = $4, automation = $5, collectors = $6
		where element_id = $1`, id, newOwner, freq, kind, auto, coll); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "owner_id", "membro inesistente")
		}
		return nil, err
	}
	if in.OwnerID != nil && deref(owner) != *in.OwnerID {
		if _, err := tx.Exec(ctx, `delete from stakeholders where element_id = $1 and role_id = (select id from stakeholder_roles where key = $2)`,
			id, RoleOwner); err != nil {
			return nil, err
		}
		if *in.OwnerID != "" {
			if err := engine.SetStakeholder(ctx, tx, actor, id, RoleOwner, *in.OwnerID, "", true); err != nil {
				return nil, err
			}
		}
		if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", "responsabile", in.OwnerID, owner); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "grc.control.update", ControlType, id, map[string]any{"frequency": freq, "kind": kind, "automation": auto}); err != nil {
		return nil, err
	}
	return s.Control(ctx, tx, actor, id)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Assess registra la valutazione dell'efficacia (grc.admin).
func (s *Service) Assess(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, effectiveness, note string) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if _, ok := effectivenessNames[effectiveness]; !ok || effectiveness == "not_assessed" {
		return nil, engine.InvalidValue("effectiveness", "efficacia non ammessa (effective, partial, ineffective)")
	}
	note = strings.TrimSpace(note)
	if effectiveness != "effective" && note == "" {
		return nil, engine.InvalidCode("validation.grc.effectiveness_note", "note", "spiega perché l'efficacia non è piena")
	}
	if len(note) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var old string
	if err := tx.QueryRow(ctx, "select effectiveness from ext_control where element_id = $1 for update", id).Scan(&old); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update ext_control set effectiveness = $2, effectiveness_note = $3, assessed_by = $4, assessed_at = $5
		where element_id = $1`, id, effectiveness, note, nullable(actor.MemberID), s.Now()); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", "efficacia", effectivenessNames[effectiveness], effectivenessNames[old]); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.assess", ControlType, id, map[string]any{"effectiveness": effectiveness}); err != nil {
		return nil, err
	}
	return s.Control(ctx, tx, actor, id)
}

// SetControlRequirements sostituisce i requisiti coperti dal controllo (grc.admin).
func (s *Service) SetControlRequirements(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, items []MappedInput) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.setMapped(ctx, tx, id, items); err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, normRef(it.Ref))
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "grc", "requisiti", refs, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.control.requirements", ControlType, id, map[string]any{"count": len(items)}); err != nil {
		return nil, err
	}
	return s.Control(ctx, tx, actor, id)
}

func (s *Service) setMapped(ctx context.Context, tx pgx.Tx, controlID string, items []MappedInput) error {
	if len(items) > 500 {
		return engine.InvalidCode("validation.grc.requirements_too_many", "requirements", "al massimo 500 requisiti per controllo")
	}
	refs := make([]string, 0, len(items))
	for i := range items {
		if items[i].Coverage == "" {
			items[i].Coverage = "full"
		}
		if items[i].Coverage != "full" && items[i].Coverage != "partial" {
			return engine.InvalidValue("coverage", "copertura non ammessa (full, partial)")
		}
		if len(items[i].Note) > 2000 {
			return engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
		}
		refs = append(refs, items[i].Ref)
	}
	found, err := resolveRefs(ctx, tx, refs)
	if err != nil {
		return err
	}
	// Si tolgono le mappature verso le versioni pubblicate; quelle verso versioni superate
	// (riferimenti spariti nella nuova versione) restano come storia.
	if _, err := tx.Exec(ctx, `delete from control_requirements cr using requirements r, framework_versions v
		where cr.control_element_id = $1 and r.id = cr.requirement_id and v.id = r.version_id and v.status = 'published'`, controlID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, it := range items {
		r, ok := found[normRef(it.Ref)]
		if !ok {
			return engine.NotFound("requirements", "requirement", "requisito %q inesistente nelle versioni pubblicate", it.Ref)
		}
		if !r.Assessable {
			return engine.InvalidParams("validation.grc.item_is_group", "requirements", map[string]any{"ref": it.Ref}, "%s raggruppa altre voci: collega le voci sotto", it.Ref)
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		if _, err := tx.Exec(ctx, `insert into control_requirements (tenant_id, control_element_id, requirement_id, coverage, note)
			values (app_tenant(), $1, $2, $3, $4)`, controlID, r.ID, it.Coverage, nullable(strings.TrimSpace(it.Note))); err != nil {
			return err
		}
	}
	return nil
}

// SetControlScopes sostituisce le decisioni a mano sui perimetri del controllo (grc.admin).
func (s *Service) SetControlScopes(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, items []Decision) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.setApplicability(ctx, tx, actor, "control_element_id", id, items); err != nil {
		return nil, err
	}
	return s.Control(ctx, tx, actor, id)
}

// ---------------------------------------------------------------------
// Viste

// ControlSummary è un controllo nell'elenco.
type ControlSummary struct {
	ID               string     `json:"id"`
	Key              string     `json:"key"`
	Title            string     `json:"title"`
	Phase            string     `json:"phase"`
	Operational      bool       `json:"operational"`
	OwnerID          string     `json:"owner_id"`
	Owner            string     `json:"owner"`
	Frequency        string     `json:"frequency"`
	Kind             string     `json:"kind"`
	Automation       string     `json:"automation"`
	Effectiveness    string     `json:"effectiveness"`
	AssessedAt       *time.Time `json:"assessed_at"`
	Frameworks       []string   `json:"frameworks"`
	Requirements     int        `json:"requirements"`
	ValidEvidence    int        `json:"valid_evidence"`
	ExpiringEvidence int        `json:"expiring_evidence"`
	MissingScopes    int        `json:"missing_scopes"` // perimetri in cui serve e non ha evidenza valida
	NextExpiry       *time.Time `json:"next_expiry"`
	NextReview       *time.Time `json:"next_review"`
}

func (m *model) summary(c *ctrlModel) ControlSummary {
	cs := ControlSummary{ID: c.ID, Key: c.Key, Title: c.Title, Phase: c.Phase, Operational: c.operational(), OwnerID: c.OwnerID, Owner: c.Owner,
		Frequency: c.Frequency, Kind: c.Kind, Automation: c.Automation, Effectiveness: c.Effectiveness, AssessedAt: c.AssessedAt,
		Frameworks: []string{}, Requirements: len(c.reqs)}
	for _, r := range c.reqs {
		if !containsStr(cs.Frameworks, r.fw.Key) {
			cs.Frameworks = append(cs.Frameworks, r.fw.Key)
		}
	}
	for _, ev := range c.evidence {
		if !ev.valid(m.now) {
			continue
		}
		cs.ValidEvidence++
		if ev.ValidUntil != nil {
			if ev.ValidUntil.Before(m.notice) {
				cs.ExpiringEvidence++
			}
			if cs.NextExpiry == nil || ev.ValidUntil.Before(*cs.NextExpiry) {
				t := *ev.ValidUntil
				cs.NextExpiry = &t
			}
		}
	}
	if c.AssessedAt != nil {
		t := c.AssessedAt.AddDate(0, 0, validityDays(c.Frequency, m.settings))
		cs.NextReview = &t
	}
	if c.operational() {
		missing := map[string]bool{}
		for _, r := range c.reqs {
			for _, p := range m.pairs(r) {
				for _, pc := range p.controls {
					if pc.c == c && len(pc.evidence) == 0 {
						missing[p.scope] = true
					}
				}
			}
		}
		cs.MissingScopes = len(missing)
	}
	return cs
}

// Controls elenca i controlli con lo stato delle evidenze.
func (s *Service) Controls(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]ControlSummary, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]ControlSummary, 0, len(m.ctrlOrder))
	for _, c := range m.ctrlOrder {
		out = append(out, m.summary(c))
	}
	return out, nil
}

// MappedReq è un requisito coperto dal controllo.
type MappedReq struct {
	ID            string `json:"id"`
	Ref           string `json:"ref"` // "dora:Art. 9"
	Framework     string `json:"framework"`
	FrameworkName string `json:"framework_name"`
	ReqRef        string `json:"req_ref"`
	Title         string `json:"title"`
	Coverage      string `json:"coverage"`
	Note          string `json:"note"`
	Current       bool   `json:"current"` // versione pubblicata (non superata)
	Enabled       bool   `json:"enabled"` // normativa abilitata nel tenant
}

// ScopeNeed è un perimetro in cui il controllo serve, con le evidenze valide.
type ScopeNeed struct {
	Scope        Scope         `json:"scope"`
	Requirements []string      `json:"requirements"`
	Evidence     []EvidenceRef `json:"evidence"`
	Status       string        `json:"status"` // ok | expiring | missing
}

// ControlView è il dettaglio di un controllo.
type ControlView struct {
	ControlSummary
	Text              string         `json:"text"`
	EffectivenessNote string         `json:"effectiveness_note"`
	AssessedBy        string         `json:"assessed_by"`
	Collectors        []string       `json:"collectors"`
	Mapped            []MappedReq    `json:"requirements"`
	Scopes            []DecisionView `json:"scopes"`
	Needs             []ScopeNeed    `json:"needs"`
	Evidence          []EvidenceRef  `json:"evidence"`
	Can               ControlCan     `json:"can"`
}

// ControlCan sono le azioni permesse all'attore.
type ControlCan struct {
	Edit        bool `json:"edit"`
	AddEvidence bool `json:"add_evidence"`
	Validate    bool `json:"validate"`
}

// Control restituisce il dettaglio di un controllo: chi ha grc.read lo vede, e anche il suo
// responsabile.
func (s *Service) Control(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ControlView, error) {
	id, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var owner *string
	var note, assessedBy string
	var collectors []string
	var text string
	if err := tx.QueryRow(ctx, `select x.owner_id::text, x.effectiveness_note, coalesce(m.display_name, ''), x.collectors, e.body_text
		from ext_control x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		left join members m on m.tenant_id = x.tenant_id and m.id = x.assessed_by where x.element_id = $1`, id).
		Scan(&owner, &note, &assessedBy, &collectors, &text); err != nil {
		return nil, err
	}
	isOwner := owner != nil && *owner == actor.MemberID && actor.MemberID != ""
	if !CanRead(actor) && !isOwner {
		return nil, engine.ErrForbidden
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	c := m.ctrls[id]
	if c == nil {
		return nil, engine.ErrNotFound
	}
	v := &ControlView{ControlSummary: m.summary(c), Text: text, EffectivenessNote: note, AssessedBy: assessedBy, Collectors: collectors,
		Mapped: []MappedReq{}, Scopes: m.decisions(c.rows), Needs: []ScopeNeed{}, Evidence: []EvidenceRef{},
		Can: ControlCan{Edit: actor.Has(PermAdmin), AddEvidence: actor.Has(PermAdmin) || isOwner, Validate: actor.Has(PermAdmin)}}
	rows, err := tx.Query(ctx, `select r.id, f.key, f.name, r.ref, r.title, cr.coverage, coalesce(cr.note, ''), v.status = 'published',
		exists (select 1 from tenant_frameworks tf where tf.framework_version_id = v.id and tf.status = 'active')
		from control_requirements cr join requirements r on r.id = cr.requirement_id
		join framework_versions v on v.id = r.version_id join frameworks f on f.id = v.framework_id
		where cr.control_element_id = $1 order by f.key, r.ordinal`, id)
	if err != nil {
		return nil, err
	}
	var mr MappedReq
	if _, err := pgx.ForEachRow(rows, []any{&mr.ID, &mr.Framework, &mr.FrameworkName, &mr.ReqRef, &mr.Title, &mr.Coverage, &mr.Note, &mr.Current, &mr.Enabled}, func() error {
		x := mr
		x.Ref = x.Framework + ":" + x.ReqRef
		v.Mapped = append(v.Mapped, x)
		return nil
	}); err != nil {
		return nil, err
	}
	// Perimetri in cui serve: quelli delle coppie dei requisiti coperti.
	needs := map[string]*ScopeNeed{}
	order := []string{}
	for _, r := range c.reqs {
		for _, p := range m.pairs(r) {
			for _, pc := range p.controls {
				if pc.c != c {
					continue
				}
				n := needs[p.scope]
				if n == nil {
					n = &ScopeNeed{Scope: m.scopes[p.scope], Requirements: []string{}, Evidence: []EvidenceRef{}}
					needs[p.scope] = n
					order = append(order, p.scope)
					for _, ev := range pc.evidence {
						n.Evidence = append(n.Evidence, m.evRef(ev))
					}
				}
				n.Requirements = append(n.Requirements, r.fw.Key+":"+r.Ref)
			}
		}
	}
	for _, sid := range order {
		n := needs[sid]
		n.Status = "missing"
		for _, e := range n.Evidence {
			if e.Status == "valid" {
				n.Status = "ok"
				break
			}
			if e.Status == "expiring" {
				n.Status = "expiring"
			}
		}
		v.Needs = append(v.Needs, *n)
	}
	for _, ev := range c.evidence {
		v.Evidence = append(v.Evidence, m.evRef(ev))
	}
	return v, nil
}

// controlFacts: fatti del controllo per i gate del processo.
func (s *Service) controlFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var owner, freq, eff string
	var reqs int
	var fws []string
	var valid, expiring int
	err := tx.QueryRow(ctx, `select coalesce(m.display_name, ''), x.frequency, x.effectiveness,
		(select count(*) from control_requirements cr join requirements r on r.id = cr.requirement_id
		   join framework_versions v on v.id = r.version_id and v.status = 'published' where cr.control_element_id = x.element_id),
		coalesce((select array_agg(distinct f.key) from control_requirements cr join requirements r on r.id = cr.requirement_id
		   join framework_versions v on v.id = r.version_id and v.status = 'published' join frameworks f on f.id = v.framework_id
		   where cr.control_element_id = x.element_id), '{}'),
		(select count(*) from grc_evidence_controls g join ext_evidence ev on ev.element_id = g.evidence_element_id
		   where g.control_element_id = x.element_id and ev.status = 'valid' and ev.valid_from <= now() and (ev.valid_until is null or ev.valid_until > now())),
		(select count(*) from grc_evidence_controls g join ext_evidence ev on ev.element_id = g.evidence_element_id
		   where g.control_element_id = x.element_id and ev.status = 'valid' and ev.valid_until > now()
		   and ev.valid_until < now() + make_interval(days => coalesce((select expiry_notice_days from grc_settings), 30)))
		from ext_control x left join members m on m.tenant_id = x.tenant_id and m.id = x.owner_id where x.element_id = $1`, el.ID).
		Scan(&owner, &freq, &eff, &reqs, &fws, &valid, &expiring)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if owner != "" {
		f.Set("responsabile", owner)
	}
	f.Set("frequenza", frequencies[freq].name)
	f.Set("efficacia", effectivenessNames[eff])
	f.Set("requisiti", reqs)
	vals := make([]any, len(fws))
	for i, k := range fws {
		vals[i] = k
	}
	f.Set("normative", vals...)
	f.Set("evidenze_valide", valid)
	f.Set("evidenze_in_scadenza", expiring)
	return nil
}

// appFacts: normative a cui l'applicazione è soggetta (righe di applicabilità del perimetro).
func (s *Service) appFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	rows, err := tx.Query(ctx, `select distinct fw.key from applicability a join requirements r on r.id = a.requirement_id
		join framework_versions v on v.id = r.version_id and v.status = 'published' join frameworks fw on fw.id = v.framework_id
		join tenant_frameworks tf on tf.framework_version_id = v.id and tf.status = 'active'
		where a.target_element_id = $1 and a.scope_kind = 'application' and a.status <> 'not_applicable' order by 1`, el.ID)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		vals := make([]any, len(keys))
		for i, k := range keys {
			vals[i] = k
		}
		f.Set("normative", vals...)
	}
	return nil
}
