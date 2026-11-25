package grc

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Copertura. Per ogni requisito valutabile di una normativa abilitata e per ogni perimetro
// in cui si applica (una "coppia"), lo stato è:
//   - covered: un controllo in esercizio che vale nel perimetro, con copertura piena ed
//     efficacia non negativa, ha un'evidenza valida (del perimetro o di tutto il tenant);
//   - partial: c'è un'evidenza valida, ma la copertura è parziale o l'efficacia parziale o
//     insufficiente;
//   - no_evidence: ci sono controlli ma nessuna evidenza valida;
//   - no_control: nessun controllo in esercizio copre il requisito nel perimetro;
//   - not_applicable: deciso a mano, con motivazione.
// La percentuale di copertura è covered / coppie applicabili. Le evidenze valgono una volta
// sola per tutti i requisiti, di tutte le normative, dei controlli a cui sono collegate.

// Stati di copertura, dal migliore al peggiore.
const (
	StCovered       = "covered"
	StPartial       = "partial"
	StNoEvidence    = "no_evidence"
	StNoControl     = "no_control"
	StNotApplicable = "not_applicable"
)

// StatusNames sono i nomi italiani degli stati di copertura.
var StatusNames = map[string]string{StCovered: "coperto", StPartial: "parziale", StNoEvidence: "senza evidenza valida",
	StNoControl: "senza controllo", StNotApplicable: "non applicabile"}

var statusRank = map[string]int{StCovered: 0, StPartial: 1, StNoEvidence: 2, StNoControl: 3, StNotApplicable: -1}

type fwModel struct {
	ID, Key, Name, VersionID, Version string
	reqs                              []*reqModel
}

type applRow struct {
	scope         string
	status        string
	rule          string
	justification string
	decidedBy     string
	decidedAt     time.Time
}

type ctrlMap struct {
	c        *ctrlModel
	coverage string
}

type reqModel struct {
	ID, Ref, Title, Kind, Summary, Parent string
	Assessable                            bool
	fw                                    *fwModel
	ruled                                 bool
	rows                                  []applRow
	maps                                  []ctrlMap
}

type ctrlModel struct {
	ID, Key, Title, Phase, PhaseCat            string
	OwnerID, Owner                             string
	Frequency, Effectiveness, Kind, Automation string
	AssessedAt                                 *time.Time
	collectors                                 []string
	rows                                       []applRow
	evidence                                   []*evModel
	reqs                                       []*reqModel
}

func (c *ctrlModel) operational() bool { return c.PhaseCat == "in_progress" }

// applies dice se il controllo vale nel perimetro: ovunque se non ha righe di applicabilità,
// altrimenti dove c'è una riga applicabile (o una del tenant), mai dove è non applicabile.
func (c *ctrlModel) applies(scope string) bool {
	if len(c.rows) == 0 {
		return true
	}
	tenant := false
	for _, r := range c.rows {
		if r.scope == scope {
			return r.status != "not_applicable"
		}
		if r.scope == tenantScope.id() && r.status != "not_applicable" {
			tenant = true
		}
	}
	return tenant
}

type evModel struct {
	ID, Key, Title, Kind, Collector, Status string
	Scope                                   string
	ValidFrom                               time.Time
	ValidUntil                              *time.Time
	ValidatedBy                             string
	ValidatedAt                             *time.Time
	Signature                               string
	Derived                                 bool // verifica di un requisito di progetto
	SourceKey                               string
}

func (e *evModel) valid(now time.Time) bool {
	return e.Status == "valid" && !e.ValidFrom.After(now) && (e.ValidUntil == nil || e.ValidUntil.After(now))
}

type model struct {
	now, notice time.Time
	settings    *Settings
	fws         []*fwModel
	reqs        map[string]*reqModel
	ctrls       map[string]*ctrlModel
	ctrlOrder   []*ctrlModel
	scopes      map[string]Scope
	// weights: peso del perimetro per la copertura pesata (applicazioni per criticità).
	weights map[string]int
}

// Pesi della copertura pesata: un'applicazione critica conta quattro volte una a bassa
// criticità; il tenant, le entità legali, i processi e le applicazioni senza criticità valgono
// come una media.
var criticalityWeight = map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}

const defaultScopeWeight = 2

func (m *model) weight(scope string) int {
	if w, ok := m.weights[scope]; ok {
		return w
	}
	return defaultScopeWeight
}

// loadModel legge in una volta normative abilitate, requisiti, applicabilità, controlli ed
// evidenze del tenant.
func (s *Service) loadModel(ctx context.Context, tx pgx.Tx) (*model, error) {
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	m := &model{now: now, notice: now.AddDate(0, 0, st.ExpiryNoticeDays), settings: st,
		reqs: map[string]*reqModel{}, ctrls: map[string]*ctrlModel{}, scopes: map[string]Scope{tenantScope.id(): tenantScope}}

	rows, err := tx.Query(ctx, `select f.id, f.key, f.name, v.id, v.version from tenant_frameworks tf
		join framework_versions v on v.id = tf.framework_version_id and v.status = 'published'
		join frameworks f on f.id = v.framework_id
		where tf.status = 'active' order by f.name`)
	if err != nil {
		return nil, err
	}
	byVersion := map[string]*fwModel{}
	var versions []string
	for rows.Next() {
		f := &fwModel{}
		if err := rows.Scan(&f.ID, &f.Key, &f.Name, &f.VersionID, &f.Version); err != nil {
			rows.Close()
			return nil, err
		}
		m.fws = append(m.fws, f)
		byVersion[f.VersionID] = f
		versions = append(versions, f.VersionID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `select r.id, r.version_id, r.ref, r.title, r.kind, coalesce(r.body, ''), coalesce(p.ref, ''),
		r.kind not in ('chapter','annex') and not exists (select 1 from requirements c where c.parent_id = r.id)
		from requirements r left join requirements p on p.id = r.parent_id
		where r.version_id = any($1) order by r.ordinal`, versions)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &reqModel{}
		var vid string
		if err := rows.Scan(&r.ID, &vid, &r.Ref, &r.Title, &r.Kind, &r.Summary, &r.Parent, &r.Assessable); err != nil {
			rows.Close()
			return nil, err
		}
		r.fw = byVersion[vid]
		r.fw.reqs = append(r.fw.reqs, r)
		m.reqs[r.ID] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rules, err := s.loadRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, rl := range rules {
		if rl.inactive {
			continue
		}
		for _, id := range rl.reqIDs {
			if r := m.reqs[id]; r != nil {
				r.ruled = true
			}
		}
	}

	// Controlli (anche in bozza o dismessi: il dettaglio li mostra; la copertura no).
	rows, err = tx.Query(ctx, `select e.id, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		coalesce(x.owner_id::text, ''), coalesce(mb.display_name, ''), x.frequency, x.effectiveness, x.control_kind, x.automation,
		x.assessed_at, x.collectors
		from ext_control x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join members mb on mb.tenant_id = x.tenant_id and mb.id = x.owner_id
		where e.deleted_at is null order by e.number`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := &ctrlModel{}
		if err := rows.Scan(&c.ID, &c.Key, &c.Title, &c.Phase, &c.PhaseCat, &c.OwnerID, &c.Owner, &c.Frequency, &c.Effectiveness,
			&c.Kind, &c.Automation, &c.AssessedAt, &c.collectors); err != nil {
			rows.Close()
			return nil, err
		}
		m.ctrls[c.ID] = c
		m.ctrlOrder = append(m.ctrlOrder, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `select control_element_id, requirement_id, coverage from control_requirements`)
	if err != nil {
		return nil, err
	}
	var cid, rid, cov string
	if _, err := pgx.ForEachRow(rows, []any{&cid, &rid, &cov}, func() error {
		c, r := m.ctrls[cid], m.reqs[rid]
		if c != nil && r != nil {
			r.maps = append(r.maps, ctrlMap{c: c, coverage: cov})
			c.reqs = append(c.reqs, r)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Applicabilità di requisiti e controlli, con i nomi dei perimetri.
	rows, err = tx.Query(ctx, `select coalesce(a.requirement_id, a.control_element_id), a.scope_kind,
		coalesce(a.target_element_id, a.target_org_unit_id)::text, a.status, coalesce(a.rule_key, ''), coalesce(a.justification, ''),
		coalesce(mb.display_name, ''), a.decided_at,
		coalesce(se.key || '-' || e.number, ou.code, ''), coalesce(e.title, ou.name, '')
		from applicability a
		left join members mb on mb.tenant_id = a.tenant_id and mb.id = a.decided_by
		left join elements e on e.tenant_id = a.tenant_id and e.id = a.target_element_id
		left join spaces se on se.tenant_id = e.tenant_id and se.id = e.space_id
		left join org_units ou on ou.tenant_id = a.tenant_id and ou.id = a.target_org_unit_id
		where a.requirement_element_id is null and (e.id is null or e.deleted_at is null)
		order by a.rule_key nulls last`)
	if err != nil {
		return nil, err
	}
	var subject, kind string
	var target *string
	var ar applRow
	var skey, sname string
	if _, err := pgx.ForEachRow(rows, []any{&subject, &kind, &target, &ar.status, &ar.rule, &ar.justification, &ar.decidedBy, &ar.decidedAt,
		&skey, &sname}, func() error {
		sc := tenantScope
		if target != nil {
			sc = Scope{Kind: kind, ID: *target, Key: skey, Name: sname}
		}
		m.scopes[sc.id()] = sc
		row := ar
		row.scope = sc.id()
		if r := m.reqs[subject]; r != nil {
			r.rows = append(r.rows, row)
		} else if c := m.ctrls[subject]; c != nil {
			c.rows = append(c.rows, row)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := s.loadEvidence(ctx, tx, m); err != nil {
		return nil, err
	}
	// Peso dei perimetri: la criticità dell'applicazione.
	m.weights = map[string]int{}
	rows, err = tx.Query(ctx, `select a.element_id::text, coalesce(a.criticality, '') from ext_application a
		join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null`)
	if err != nil {
		return nil, err
	}
	var aid, crit string
	if _, err := pgx.ForEachRow(rows, []any{&aid, &crit}, func() error {
		if w, ok := criticalityWeight[crit]; ok {
			m.weights["application:"+aid] = w
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return m, nil
}

// loadEvidence legge le evidenze dei controlli e le verifiche dei requisiti di progetto che
// valgono come evidenze.
func (s *Service) loadEvidence(ctx context.Context, tx pgx.Tx, m *model) error {
	rows, err := tx.Query(ctx, `select g.control_element_id, e.id, s.key || '-' || e.number, e.title, x.kind, coalesce(x.collector, ''), x.status,
		x.scope_kind, coalesce(x.target_element_id, x.target_org_unit_id)::text, x.valid_from, x.valid_until,
		coalesce(mb.display_name, ''), x.validated_at, coalesce(x.signature, ''),
		coalesce(st.key || '-' || te.number, ou.code, ''), coalesce(te.title, ou.name, '')
		from grc_evidence_controls g
		join ext_evidence x on x.tenant_id = g.tenant_id and x.element_id = g.evidence_element_id
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join members mb on mb.tenant_id = x.tenant_id and mb.id = x.validated_by
		left join elements te on te.tenant_id = x.tenant_id and te.id = x.target_element_id
		left join spaces st on st.tenant_id = te.tenant_id and st.id = te.space_id
		left join org_units ou on ou.tenant_id = x.tenant_id and ou.id = x.target_org_unit_id
		where e.deleted_at is null order by x.valid_from desc`)
	if err != nil {
		return err
	}
	var cid, kind, skey, sname string
	var target *string
	for rows.Next() {
		ev := &evModel{}
		if err := rows.Scan(&cid, &ev.ID, &ev.Key, &ev.Title, &ev.Kind, &ev.Collector, &ev.Status, &kind, &target, &ev.ValidFrom, &ev.ValidUntil,
			&ev.ValidatedBy, &ev.ValidatedAt, &ev.Signature, &skey, &sname); err != nil {
			rows.Close()
			return err
		}
		sc := tenantScope
		if target != nil {
			sc = Scope{Kind: kind, ID: *target, Key: skey, Name: sname}
		}
		m.scopes[sc.id()] = sc
		ev.Scope = sc.id()
		if c := m.ctrls[cid]; c != nil {
			c.evidence = append(c.evidence, ev)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Verifiche dei requisiti di progetto collegati a un requisito normativo, a mano
	// (grc_requirement_links) o dalla voce del catalogo dei requisiti ("regulatory").
	rows, err = tx.Query(ctx, `with links as (
		  select l.requirement_element_id as rid, l.framework_key as fk, l.ref from grc_requirement_links l
		  union
		  select x.element_id, split_part(r.v, ':', 1), substr(r.v, strpos(r.v, ':') + 1)
		  from ext_requirement x
		  join requirement_catalogs c on c.tenant_id = x.tenant_id and c.key = x.catalog_key and c.version = x.catalog_version
		  cross join lateral jsonb_array_elements(c.definition -> 'items') it
		  cross join lateral jsonb_array_elements_text(coalesce(it -> 'regulatory', '[]'::jsonb)) r(v)
		  where x.origin = 'catalog' and it ->> 'key' = x.catalog_item_key and strpos(r.v, ':') > 0
		)
		select links.fk, links.ref, v.id, s.key || '-' || e.number, e.title, imp.application_id::text,
		  coalesce(sa.key || '-' || ae.number, ''), coalesce(ae.title, ''), v.verified_at, coalesce(mb.display_name, ''), v.signature
		from links
		join elements e on e.id = links.rid and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join applicability c on c.tenant_id = e.tenant_id and c.requirement_element_id = links.rid and c.status = 'applicable'
		  and c.progress = 'verified' and c.last_verification_id is not null
		join requirement_verifications v on v.tenant_id = c.tenant_id and v.id = c.last_verification_id and v.outcome = 'verified'
		join ext_impact imp on imp.tenant_id = c.tenant_id and imp.element_id = c.target_element_id
		join elements ae on ae.tenant_id = imp.tenant_id and ae.id = imp.application_id
		join spaces sa on sa.tenant_id = ae.tenant_id and sa.id = ae.space_id
		left join members mb on mb.tenant_id = v.tenant_id and mb.id = v.verified_by`)
	if err != nil {
		return err
	}
	byRef := map[string][]verif{}
	var fk, ref string
	var v verif
	if _, err := pgx.ForEachRow(rows, []any{&fk, &ref, &v.id, &v.key, &v.title, &v.app, &v.appKey, &v.appName, &v.at, &v.by, &v.sig}, func() error {
		k := fk + ":" + ref
		if !containsVerif(byRef[k], v.id) {
			byRef[k] = append(byRef[k], v)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(byRef) == 0 {
		return nil
	}
	for _, c := range m.ctrlOrder {
		seen := map[string]bool{}
		for _, r := range c.reqs {
			for _, v := range byRef[r.fw.Key+":"+r.Ref] {
				if seen[v.id] {
					continue
				}
				seen[v.id] = true
				sc := Scope{Kind: "application", ID: v.app, Key: v.appKey, Name: v.appName}
				m.scopes[sc.id()] = sc
				until := v.at.AddDate(0, 0, validityDays(c.Frequency, m.settings))
				at := v.at
				c.evidence = append(c.evidence, &evModel{ID: v.id, Key: v.key, Title: "Verifica: " + v.title, Kind: CollectorVerification,
					Collector: CollectorVerification, Status: "valid", Scope: sc.id(), ValidFrom: v.at, ValidUntil: &until,
					ValidatedBy: v.by, ValidatedAt: &at, Signature: v.sig, Derived: true, SourceKey: v.key})
			}
		}
	}
	return nil
}

type verif struct {
	id, key, title, app, appKey, appName, by, sig string
	at                                            time.Time
}

func containsVerif(vs []verif, id string) bool {
	for _, v := range vs {
		if v.id == id {
			return true
		}
	}
	return false
}

// pair è un requisito in un perimetro.
type pair struct {
	req      *reqModel
	scope    string
	status   string
	source   string // "tenant" (implicito), "rule:<chiave>", "manual"
	note     string
	controls []pairControl
	expiring bool
	weight   int
}

type pairControl struct {
	c        *ctrlModel
	coverage string
	evidence []*evModel
}

// pairs calcola le coppie di un requisito.
func (m *model) pairs(r *reqModel) []*pair {
	if !r.Assessable {
		return nil
	}
	type slot struct {
		row    applRow
		manual bool
	}
	slots := map[string]slot{}
	order := []string{}
	for _, row := range r.rows {
		manual := row.rule == ""
		if old, ok := slots[row.scope]; ok && old.manual && !manual {
			continue
		}
		if _, ok := slots[row.scope]; !ok {
			order = append(order, row.scope)
		}
		slots[row.scope] = slot{row, manual}
	}
	if _, ok := slots[tenantScope.id()]; !ok && !r.ruled {
		slots[tenantScope.id()] = slot{row: applRow{scope: tenantScope.id(), status: "applicable"}}
		order = append([]string{tenantScope.id()}, order...)
	}
	out := make([]*pair, 0, len(order))
	for _, sid := range order {
		sl := slots[sid]
		p := &pair{req: r, scope: sid, note: sl.row.justification, weight: m.weight(sid)}
		switch {
		case sl.manual:
			p.source = "manual"
		case sl.row.rule != "":
			p.source = "rule:" + sl.row.rule
		default:
			p.source = "tenant"
		}
		if sl.row.status == "not_applicable" {
			p.status = StNotApplicable
			out = append(out, p)
			continue
		}
		m.evaluate(p)
		out = append(out, p)
	}
	return out
}

// evaluate dà lo stato alla coppia.
func (m *model) evaluate(p *pair) {
	best := StNoControl
	var used []*evModel
	for _, mp := range p.req.maps {
		c := mp.c
		if !c.operational() || !c.applies(p.scope) {
			continue
		}
		pc := pairControl{c: c, coverage: mp.coverage}
		for _, ev := range c.evidence {
			if ev.valid(m.now) && (ev.Scope == tenantScope.id() || ev.Scope == p.scope) {
				pc.evidence = append(pc.evidence, ev)
			}
		}
		p.controls = append(p.controls, pc)
		st := StNoEvidence
		if len(pc.evidence) > 0 {
			st = StPartial
			if mp.coverage == "full" && (c.Effectiveness == "effective" || c.Effectiveness == "not_assessed") {
				st = StCovered
			}
		}
		if statusRank[st] < statusRank[best] {
			best = st
			used = pc.evidence
		} else if st == best && st != StNoControl {
			used = append(used, pc.evidence...)
		}
	}
	p.status = best
	if best == StCovered || best == StPartial {
		p.expiring = true
		for _, ev := range used {
			if ev.ValidUntil == nil || !ev.ValidUntil.Before(m.notice) {
				p.expiring = false
				break
			}
		}
	}
}

// ---------------------------------------------------------------------
// Viste

// Counts sono i conteggi delle coppie per stato.
type Counts struct {
	Applicable    int `json:"applicable"`
	Covered       int `json:"covered"`
	Partial       int `json:"partial"`
	NoEvidence    int `json:"no_evidence"`
	NoControl     int `json:"no_control"`
	NotApplicable int `json:"not_applicable"`
	Expiring      int `json:"expiring"`
	Percent       int `json:"percent"`
	// WeightedPercent: copertura pesata per criticità del perimetro (un'applicazione critica
	// pesa più di una a bassa criticità). Misura il rischio scoperto, non la dimensione del catalogo.
	WeightedPercent int `json:"weighted_percent"`

	wCovered, wApplicable int
}

func (c *Counts) add(p *pair) {
	switch p.status {
	case StNotApplicable:
		c.NotApplicable++
		return
	case StCovered:
		c.Covered++
	case StPartial:
		c.Partial++
	case StNoEvidence:
		c.NoEvidence++
	case StNoControl:
		c.NoControl++
	}
	c.Applicable++
	w := p.weight
	if w == 0 {
		w = defaultScopeWeight
	}
	c.wApplicable += w
	if p.status == StCovered {
		c.wCovered += w
	}
	if p.expiring {
		c.Expiring++
	}
}

func (c *Counts) finish() {
	if c.Applicable > 0 {
		c.Percent = c.Covered * 100 / c.Applicable
		if c.wApplicable > 0 {
			c.WeightedPercent = c.wCovered * 100 / c.wApplicable
		} else {
			// Conteggi non passati da add (per esempio costruiti a mano): senza pesi vale la copertura semplice.
			c.WeightedPercent = c.Percent
		}
	}
}

// Worst è lo stato peggiore tra quelli presenti.
func (c *Counts) Worst() string {
	switch {
	case c.NoControl > 0:
		return StNoControl
	case c.NoEvidence > 0:
		return StNoEvidence
	case c.Partial > 0:
		return StPartial
	case c.Covered > 0:
		return StCovered
	}
	return StNotApplicable
}

// FrameworkSummary è la riga di una normativa nel cruscotto.
type FrameworkSummary struct {
	Key                     string `json:"key"`
	Name                    string `json:"name"`
	Version                 string `json:"version"`
	Requirements            int    `json:"requirements"`
	Scopes                  int    `json:"scopes"`
	WithoutControl          int    `json:"without_control"`
	ControlsWithoutEvidence int    `json:"controls_without_evidence"`
	ExpiringEvidence        int    `json:"expiring_evidence"`
	Counts
}

// EvidenceRef è un'evidenza citata nel cruscotto e nel drill-down.
type EvidenceRef struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Title       string     `json:"title"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"` // valid | expiring | expired | draft | rejected | revoked
	ValidUntil  *time.Time `json:"valid_until"`
	ValidatedBy string     `json:"validated_by"`
	Derived     bool       `json:"derived"`
	Scope       Scope      `json:"scope"`
	Controls    []string   `json:"controls,omitempty"`
}

func (m *model) evRef(ev *evModel) EvidenceRef {
	st := ev.Status
	if st == "valid" {
		switch {
		case !ev.valid(m.now):
			st = "expired"
		case ev.ValidUntil != nil && ev.ValidUntil.Before(m.notice):
			st = "expiring"
		}
	}
	return EvidenceRef{ID: ev.ID, Key: ev.Key, Title: ev.Title, Kind: ev.Kind, Status: st, ValidUntil: ev.ValidUntil,
		ValidatedBy: ev.ValidatedBy, Derived: ev.Derived, Scope: m.scopes[ev.Scope]}
}

// ReqRef è un requisito citato nel cruscotto.
type ReqRef struct {
	ID        string `json:"id"`
	Framework string `json:"framework"`
	Ref       string `json:"ref"`
	Title     string `json:"title"`
	Scopes    int    `json:"scopes"`
}

// ControlRef è un controllo citato nel cruscotto.
type ControlRef struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Owner    string  `json:"owner"`
	Missing  []Scope `json:"missing"` // perimetri senza evidenza valida
	Expiring int     `json:"expiring"`
}

// ScopeSummary è un perimetro (applicazione, processo, entità legale) con le sue normative.
type ScopeSummary struct {
	Scope
	Frameworks []ScopeFramework `json:"frameworks"`
	Counts
	Worst string `json:"worst"`
}

// ScopeFramework è lo stato di una normativa in un perimetro.
type ScopeFramework struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Counts
	Worst string `json:"worst"`
}

// Dashboard è il cruscotto della conformità.
type Dashboard struct {
	GeneratedAt    time.Time          `json:"generated_at"`
	Frameworks     []FrameworkSummary `json:"frameworks"`
	Tenant         Counts             `json:"tenant"`
	Scopes         []ScopeSummary     `json:"scopes"`
	WithoutControl []ReqRef           `json:"without_control"`
	NoEvidence     []ControlRef       `json:"controls_without_evidence"`
	Expiring       []EvidenceRef      `json:"expiring"`
	Controls       int                `json:"controls"`
	ValidEvidence  int                `json:"valid_evidence"`
	Settings       *Settings          `json:"settings"`
	StatusNames    map[string]string  `json:"status_names"`
	CanEdit        bool               `json:"can_edit"`
}

// dashboardFor calcola il cruscotto (framework = solo quella normativa).
func (m *model) dashboardFor(framework string) *Dashboard {
	d := &Dashboard{GeneratedAt: m.now, Frameworks: []FrameworkSummary{}, Scopes: []ScopeSummary{}, WithoutControl: []ReqRef{},
		NoEvidence: []ControlRef{}, Expiring: []EvidenceRef{}, Settings: m.settings, StatusNames: StatusNames}
	scopeSum := map[string]*ScopeSummary{}
	scopeFw := map[string]map[string]*ScopeFramework{}
	missingByCtrl := map[string]map[string]bool{}
	expiringEv := map[string]*EvidenceRef{}
	for _, f := range m.fws {
		if framework != "" && f.Key != framework {
			continue
		}
		fs := FrameworkSummary{Key: f.Key, Name: f.Name, Version: f.Version}
		scopes := map[string]bool{}
		ctrlNoEv := map[string]bool{}
		fwExp := map[string]bool{}
		for _, r := range f.reqs {
			if !r.Assessable {
				continue
			}
			fs.Requirements++
			noCtrl := 0
			for _, p := range m.pairs(r) {
				fs.Counts.add(p)
				d.Tenant.add(p)
				if p.status == StNotApplicable {
					continue
				}
				scopes[p.scope] = true
				if p.status == StNoControl {
					noCtrl++
				}
				for _, pc := range p.controls {
					if len(pc.evidence) == 0 {
						ctrlNoEv[pc.c.ID] = true
						if missingByCtrl[pc.c.ID] == nil {
							missingByCtrl[pc.c.ID] = map[string]bool{}
						}
						missingByCtrl[pc.c.ID][p.scope] = true
					}
					for _, ev := range pc.evidence {
						if ev.ValidUntil != nil && ev.ValidUntil.Before(m.notice) {
							fwExp[ev.ID] = true
							if expiringEv[ev.ID] == nil {
								ref := m.evRef(ev)
								expiringEv[ev.ID] = &ref
							}
							if er := expiringEv[ev.ID]; !containsStr(er.Controls, pc.c.Key) {
								er.Controls = append(er.Controls, pc.c.Key)
							}
						}
					}
				}
				if p.scope == tenantScope.id() {
					continue
				}
				ss := scopeSum[p.scope]
				if ss == nil {
					ss = &ScopeSummary{Scope: m.scopes[p.scope]}
					scopeSum[p.scope] = ss
					scopeFw[p.scope] = map[string]*ScopeFramework{}
				}
				ss.Counts.add(p)
				sf := scopeFw[p.scope][f.Key]
				if sf == nil {
					sf = &ScopeFramework{Key: f.Key, Name: f.Name}
					scopeFw[p.scope][f.Key] = sf
				}
				sf.Counts.add(p)
			}
			if noCtrl > 0 {
				fs.WithoutControl++
				d.WithoutControl = append(d.WithoutControl, ReqRef{ID: r.ID, Framework: f.Key, Ref: r.Ref, Title: r.Title, Scopes: noCtrl})
			}
		}
		fs.Scopes = len(scopes)
		fs.ControlsWithoutEvidence = len(ctrlNoEv)
		fs.ExpiringEvidence = len(fwExp)
		fs.Counts.finish()
		d.Frameworks = append(d.Frameworks, fs)
	}
	d.Tenant.finish()
	for sid, ss := range scopeSum {
		ss.Frameworks = []ScopeFramework{}
		for _, f := range m.fws {
			if sf := scopeFw[sid][f.Key]; sf != nil {
				sf.Counts.finish()
				sf.Worst = sf.Counts.Worst()
				ss.Frameworks = append(ss.Frameworks, *sf)
			}
		}
		ss.Counts.finish()
		ss.Worst = ss.Counts.Worst()
		d.Scopes = append(d.Scopes, *ss)
	}
	sort.Slice(d.Scopes, func(i, j int) bool {
		if d.Scopes[i].Kind != d.Scopes[j].Kind {
			return d.Scopes[i].Kind < d.Scopes[j].Kind
		}
		return d.Scopes[i].Key < d.Scopes[j].Key
	})
	for _, c := range m.ctrlOrder {
		if !c.operational() {
			continue
		}
		if framework == "" {
			d.Controls++
		}
		if miss := missingByCtrl[c.ID]; len(miss) > 0 {
			cr := ControlRef{ID: c.ID, Key: c.Key, Title: c.Title, Owner: c.Owner, Missing: []Scope{}}
			for sid := range miss {
				cr.Missing = append(cr.Missing, m.scopes[sid])
			}
			sort.Slice(cr.Missing, func(i, j int) bool { return cr.Missing[i].Key < cr.Missing[j].Key })
			d.NoEvidence = append(d.NoEvidence, cr)
		}
		if framework == "" {
			for _, ev := range c.evidence {
				if ev.valid(m.now) {
					d.ValidEvidence++
				}
			}
		}
	}
	for _, er := range expiringEv {
		d.Expiring = append(d.Expiring, *er)
	}
	sort.Slice(d.Expiring, func(i, j int) bool { return d.Expiring[i].ValidUntil.Before(*d.Expiring[j].ValidUntil) })
	return d
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Dashboard calcola il cruscotto della conformità.
func (s *Service) Dashboard(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Dashboard, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	d := m.dashboardFor("")
	d.CanEdit = actor.Has(PermAdmin)
	return d, nil
}

// ---------------------------------------------------------------------
// Drill-down

// PairView è un requisito in un perimetro, con i controlli e le evidenze.
type PairView struct {
	Scope    Scope             `json:"scope"`
	Status   string            `json:"status"`
	Source   string            `json:"source"`
	Note     string            `json:"note,omitempty"`
	Expiring bool              `json:"expiring"`
	Controls []PairControlView `json:"controls"`
}

// PairControlView è un controllo che lavora su una coppia.
type PairControlView struct {
	ID            string        `json:"id"`
	Key           string        `json:"key"`
	Title         string        `json:"title"`
	Coverage      string        `json:"coverage"`
	Effectiveness string        `json:"effectiveness"`
	Evidence      []EvidenceRef `json:"evidence"`
}

func (m *model) pairView(p *pair) PairView {
	pv := PairView{Scope: m.scopes[p.scope], Status: p.status, Source: p.source, Note: p.note, Expiring: p.expiring, Controls: []PairControlView{}}
	for _, pc := range p.controls {
		v := PairControlView{ID: pc.c.ID, Key: pc.c.Key, Title: pc.c.Title, Coverage: pc.coverage, Effectiveness: pc.c.Effectiveness, Evidence: []EvidenceRef{}}
		for _, ev := range pc.evidence {
			v.Evidence = append(v.Evidence, m.evRef(ev))
		}
		pv.Controls = append(pv.Controls, v)
	}
	return pv
}

// CoverageItem è una voce del catalogo con la sua copertura.
type CoverageItem struct {
	ID         string     `json:"id"`
	Ref        string     `json:"ref"`
	Parent     string     `json:"parent"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Assessable bool       `json:"assessable"`
	Status     string     `json:"status"`
	Counts     Counts     `json:"counts"`
	Controls   int        `json:"controls"`
	Pairs      []PairView `json:"pairs,omitempty"`
}

// FrameworkCoverage è il drill-down di una normativa.
type FrameworkCoverage struct {
	Framework   *FrameworkSummary `json:"framework"`
	Enabled     bool              `json:"enabled"`
	Items       []CoverageItem    `json:"items"`
	Dashboard   *Dashboard        `json:"dashboard"`
	StatusNames map[string]string `json:"status_names"`
}

func (m *model) itemOf(r *reqModel, withPairs bool) CoverageItem {
	it := CoverageItem{ID: r.ID, Ref: r.Ref, Parent: r.Parent, Kind: r.Kind, Title: r.Title, Summary: r.Summary, Assessable: r.Assessable}
	ctrls := map[string]bool{}
	for _, mp := range r.maps {
		ctrls[mp.c.ID] = true
	}
	it.Controls = len(ctrls)
	for _, p := range m.pairs(r) {
		it.Counts.add(p)
		if withPairs {
			it.Pairs = append(it.Pairs, m.pairView(p))
		}
	}
	it.Counts.finish()
	it.Status = it.Counts.Worst()
	if !r.Assessable {
		it.Status = ""
	}
	return it
}

// FrameworkCoverage restituisce la copertura di una normativa voce per voce, con le coppie.
func (s *Service) FrameworkCoverage(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*FrameworkCoverage, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, f := range m.fws {
		if f.Key != key {
			continue
		}
		d := m.dashboardFor(key)
		out := &FrameworkCoverage{Enabled: true, Items: []CoverageItem{}, Dashboard: d, StatusNames: StatusNames}
		if len(d.Frameworks) > 0 {
			out.Framework = &d.Frameworks[0]
		}
		// I totali dei capi e degli articoli con figli sommano le voci sotto.
		items := make([]CoverageItem, 0, len(f.reqs))
		idx := map[string]int{}
		for _, r := range f.reqs {
			idx[r.Ref] = len(items)
			items = append(items, m.itemOf(r, true))
		}
		for i := len(items) - 1; i >= 0; i-- {
			it := items[i]
			if it.Parent == "" {
				continue
			}
			if pi, ok := idx[it.Parent]; ok {
				pc := &items[pi].Counts
				pc.Applicable += it.Counts.Applicable
				pc.Covered += it.Counts.Covered
				pc.Partial += it.Counts.Partial
				pc.NoEvidence += it.Counts.NoEvidence
				pc.NoControl += it.Counts.NoControl
				pc.NotApplicable += it.Counts.NotApplicable
				pc.Expiring += it.Counts.Expiring
				pc.wApplicable += it.Counts.wApplicable
				pc.wCovered += it.Counts.wCovered
			}
		}
		for i := range items {
			if !items[i].Assessable {
				items[i].Counts.finish()
				items[i].Status = items[i].Counts.Worst()
			}
		}
		out.Items = items
		return out, nil
	}
	// Normativa non abilitata: le voci senza copertura.
	fw, err := s.Framework(ctx, tx, actor, key, false)
	if err != nil {
		return nil, err
	}
	out := &FrameworkCoverage{Items: []CoverageItem{}, StatusNames: StatusNames,
		Framework: &FrameworkSummary{Key: fw.Key, Name: fw.Name}}
	if fw.Current != nil {
		out.Framework.Version = fw.Current.Version
	}
	for _, it := range fw.Items {
		out.Items = append(out.Items, CoverageItem{ID: it.ID, Ref: it.Ref, Parent: it.Parent, Kind: it.Kind, Title: it.Title,
			Summary: it.Summary, Assessable: it.Assessable})
	}
	return out, nil
}
