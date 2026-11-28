package continuity

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Il modello della continuità si carica tutto in poche query e si calcola in memoria: le
// incoerenze dipendono da capability, BIA, applicazioni, dipendenze, contratti, piani e test
// insieme, e cambiano appena cambia uno di questi. Niente viene salvato: si ricalcola a ogni lettura.

// Ref è un elemento citato.
type Ref struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

type appN struct {
	Ref
	TeamID   *string
	Team     *string
	RTO, RPO *int
	Critical bool
	Caps     []string // capability supportate direttamente
}

type capN struct {
	Ref
	Code   *string
	Parent *string
}

type approvedV struct {
	Version  int
	At       time.Time
	By       *string
	Critical bool
	MTPD     *int
	RTO, RPO *int
}

type biaN struct {
	Ref
	State, StateCat string
	CapID           string
	Critical        *bool
	MTPD, RTO, RPO  *int
	Approved        *approvedV
	CreatedAt       time.Time
}

type provN struct {
	Ref
	RTO, RPO *int // peggiore fra i contratti attivi
}

type planN struct {
	Page            Ref
	Target          string
	ApprovedVersion *int
}

type testN struct {
	Ref
	State, StateCat string
	Type            string
	Subject         string
	Planned         *time.Time
	Executed        *time.Time
	Outcome         *string
	RTOm, RPOm      *int
}

func (t *testN) done() bool {
	return t.Outcome != nil && t.Executed != nil && t.StateCat != "cancelled"
}

type model struct {
	cfg      *Config
	now      time.Time
	apps     map[string]*appN
	appOrder []string
	caps     map[string]*capN
	capOrder []string
	bias     map[string]*biaN // per capability: la BIA non archiviata più recente
	provs    map[string]*provN
	deps     map[string][]string // applicazione → applicazioni da cui dipende direttamente
	provided map[string][]string // applicazione → fornitori
	plans    map[string][]planN  // oggetto → piani
	tests    map[string][]*testN // oggetto → test, dal più recente
	testByID map[string]*testN
}

// depAppEdgesSQL: archi «dependent dipende da dependency» fra applicazioni, secondo il verso del
// tipo di relazione (come l'analisi d'impatto dell'architettura).
const depAppEdgesSQL = `select distinct case when rt.impact_direction = 'reverse' then r.target_id else r.source_id end,
	       case when rt.impact_direction = 'reverse' then r.source_id else r.target_id end
	from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
	where rt.ea_category = 'dependency' and rt.impact_direction <> 'none'`

func loadModel(ctx context.Context, tx pgx.Tx, now time.Time) (*model, error) {
	cfg, _, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	m := &model{cfg: cfg, now: now, apps: map[string]*appN{}, caps: map[string]*capN{}, bias: map[string]*biaN{}, provs: map[string]*provN{},
		deps: map[string][]string{}, provided: map[string][]string{}, plans: map[string][]planN{}, tests: map[string][]*testN{}, testByID: map[string]*testN{}}

	// Applicazioni in vita (non dismesse).
	rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, a.owner_team_id::text, t.name, a.rto_minutes, a.rpo_minutes, a.supports_critical_function
		from elements e join element_types et on et.tenant_id = e.tenant_id and et.id = e.type_id and et.key = $1
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where e.deleted_at is null and not e.is_draft and coalesce(o.category, '') not in ('done', 'cancelled')
		order by e.title`, AppType)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &appN{}
		if err := rows.Scan(&a.ID, &a.Key, &a.Title, &a.TeamID, &a.Team, &a.RTO, &a.RPO, &a.Critical); err != nil {
			rows.Close()
			return nil, err
		}
		m.apps[a.ID] = a
		m.appOrder = append(m.appOrder, a.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Capability non dismesse.
	rows, err = tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, c.code, e.parent_id::text
		from elements e join element_types et on et.tenant_id = e.tenant_id and et.id = e.type_id and et.key = $1
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and not e.is_draft and coalesce(o.category, '') <> 'done'
		order by coalesce(c.position, 0), e.title`, CapabilityType)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := &capN{}
		if err := rows.Scan(&c.ID, &c.Key, &c.Title, &c.Code, &c.Parent); err != nil {
			rows.Close()
			return nil, err
		}
		m.caps[c.ID] = c
		m.capOrder = append(m.capOrder, c.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Applicazione → capability (supports).
	rows, err = tx.Query(ctx, `select r.source_id::text, r.target_id::text from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1`, RelSupports)
	if err != nil {
		return nil, err
	}
	var a, b string
	if _, err := pgx.ForEachRow(rows, []any{&a, &b}, func() error {
		if app, ok := m.apps[a]; ok {
			if _, ok := m.caps[b]; ok {
				app.Caps = append(app.Caps, b)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Dipendenze dirette fra applicazioni.
	rows, err = tx.Query(ctx, depAppEdgesSQL)
	if err != nil {
		return nil, err
	}
	if _, err := pgx.ForEachRow(rows, []any{&a, &b}, func() error {
		if _, ok := m.apps[a]; ok && a != b {
			if _, ok := m.apps[b]; ok {
				m.deps[a] = append(m.deps[a], b)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Fornitori collegati e impegno peggiore fra i contratti attivi.
	rows, err = tx.Query(ctx, `select p.id::text, s.key || '-' || p.number, p.title,
			(select max(c.rto_minutes) from ict_contracts c where c.provider_element_id = p.id and (c.end_date is null or c.end_date >= $2::date)),
			(select max(c.rpo_minutes) from ict_contracts c where c.provider_element_id = p.id and (c.end_date is null or c.end_date >= $2::date))
		from elements p join element_types et on et.tenant_id = p.tenant_id and et.id = p.type_id and et.key = $1
		join spaces s on s.tenant_id = p.tenant_id and s.id = p.space_id
		where p.deleted_at is null and not p.is_draft`, ProviderType, today(now))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p := &provN{}
		if err := rows.Scan(&p.ID, &p.Key, &p.Title, &p.RTO, &p.RPO); err != nil {
			rows.Close()
			return nil, err
		}
		m.provs[p.ID] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select r.source_id::text, r.target_id::text from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1`, RelProvided)
	if err != nil {
		return nil, err
	}
	if _, err := pgx.ForEachRow(rows, []any{&a, &b}, func() error {
		if _, ok := m.apps[a]; ok {
			if _, ok := m.provs[b]; ok {
				m.provided[a] = append(m.provided[a], b)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// BIA (con l'ultima versione approvata).
	rows, err = tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''), x.capability_id::text,
			x.critical, x.mtpd_minutes, x.rto_minutes, x.rpo_minutes, e.created_at,
			v.version, v.approved_at, (select m.display_name from members m where m.tenant_id = v.tenant_id and m.id = v.approved_by),
			v.critical, v.mtpd_minutes, v.rto_minutes, v.rpo_minutes
		from ext_bia x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join lateral (select * from bia_versions bv where bv.tenant_id = x.tenant_id and bv.bia_id = x.element_id order by bv.version desc limit 1) v on true
		where coalesce(o.category, '') <> 'cancelled'
		order by e.created_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		bn := &biaN{}
		var ver *int
		var at *time.Time
		var by *string
		var crit *bool
		var mt, rt, rp *int
		if err := rows.Scan(&bn.ID, &bn.Key, &bn.Title, &bn.State, &bn.StateCat, &bn.CapID, &bn.Critical, &bn.MTPD, &bn.RTO, &bn.RPO, &bn.CreatedAt,
			&ver, &at, &by, &crit, &mt, &rt, &rp); err != nil {
			rows.Close()
			return nil, err
		}
		if ver != nil {
			bn.Approved = &approvedV{Version: *ver, At: *at, By: by, Critical: crit != nil && *crit, MTPD: mt, RTO: rt, RPO: rp}
		}
		m.bias[bn.CapID] = bn // l'ordine per data lascia la più recente
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Piani di continuità.
	rows, err = tx.Query(ctx, `select pg.id::text, s.key || '-' || pg.number, pg.title, r.target_id::text, kp.approved_version
		from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1
		join elements pg on pg.tenant_id = r.tenant_id and pg.id = r.source_id and pg.deleted_at is null
		join spaces s on s.tenant_id = pg.tenant_id and s.id = pg.space_id
		left join kb_pages kp on kp.tenant_id = pg.tenant_id and kp.element_id = pg.id
		order by pg.title`, RelPlanFor)
	if err != nil {
		return nil, err
	}
	var p planN
	if _, err := pgx.ForEachRow(rows, []any{&p.Page.ID, &p.Page.Key, &p.Page.Title, &p.Target, &p.ApprovedVersion}, func() error {
		m.plans[p.Target] = append(m.plans[p.Target], p)
		return nil
	}); err != nil {
		return nil, err
	}

	// Test di resilienza.
	rows, err = tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''), x.test_type, x.subject_id::text,
			x.planned_on, x.executed_on, x.outcome, x.rto_measured_minutes, x.rpo_measured_minutes
		from ext_resilience_test x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		order by coalesce(x.executed_on, x.planned_on) desc nulls last, e.created_at desc`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t := &testN{}
		if err := rows.Scan(&t.ID, &t.Key, &t.Title, &t.State, &t.StateCat, &t.Type, &t.Subject, &t.Planned, &t.Executed, &t.Outcome, &t.RTOm, &t.RPOm); err != nil {
			rows.Close()
			return nil, err
		}
		m.tests[t.Subject] = append(m.tests[t.Subject], t)
		m.testByID[t.ID] = t
	}
	rows.Close()
	return m, rows.Err()
}

// ancestors restituisce la capability e quelle che la contengono (senza cicli).
func (m *model) ancestors(id string) []string {
	out := []string{}
	seen := map[string]bool{}
	for cur := id; cur != "" && !seen[cur]; {
		c, ok := m.caps[cur]
		if !ok {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		if c.Parent == nil {
			break
		}
		cur = *c.Parent
	}
	return out
}

// requirement è il richiesto di un'applicazione, dalle BIA approvate delle sue capability.
type requirement struct {
	RTO, RPO *int
	Source   *biaN // BIA con l'RTO più stringente
	Critical bool
	Caps     []string // capability coinvolte (con quelle che le contengono)
}

func minPtr(a, b *int) *int {
	if a == nil {
		return b
	}
	if b == nil || *a <= *b {
		return a
	}
	return b
}

func (m *model) required(app *appN) requirement {
	r := requirement{}
	seen := map[string]bool{}
	for _, c := range app.Caps {
		for _, id := range m.ancestors(c) {
			if seen[id] {
				continue
			}
			seen[id] = true
			r.Caps = append(r.Caps, id)
			bn, ok := m.bias[id]
			if !ok || bn.Approved == nil {
				continue
			}
			v := bn.Approved
			if v.RTO != nil && (r.RTO == nil || *v.RTO < *r.RTO) {
				r.RTO, r.Source = v.RTO, bn
			}
			r.RPO = minPtr(r.RPO, v.RPO)
			r.Critical = r.Critical || v.Critical
		}
	}
	return r
}

// appsOfCap sono le applicazioni che supportano la capability o una che contiene.
func (m *model) appsOfCap(capID string) []*appN {
	out := []*appN{}
	for _, id := range m.appOrder {
		a := m.apps[id]
		for _, c := range a.Caps {
			if containsStr(m.ancestors(c), capID) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// approvedPlan dice se uno degli oggetti ha un piano approvato.
func (m *model) approvedPlan(ids ...string) bool {
	for _, id := range ids {
		for _, p := range m.plans[id] {
			if p.ApprovedVersion != nil {
				return true
			}
		}
	}
	return false
}

// lastDone è l'ultimo test eseguito fra quelli degli oggetti.
func (m *model) lastDone(ids ...string) *testN {
	var best *testN
	for _, id := range ids {
		for _, t := range m.tests[id] {
			if t.done() && (best == nil || t.Executed.After(*best.Executed)) {
				best = t
			}
		}
	}
	return best
}

func (m *model) daysSince(t time.Time) int {
	return int(today(m.now).Sub(t.UTC().Truncate(24*time.Hour)).Hours() / 24)
}

// effective è l'RTO con cui si confrontano le dipendenze: il dichiarato, o il richiesto se manca.
func effective(app *appN, r requirement) *int {
	if app.RTO != nil {
		return app.RTO
	}
	return r.RTO
}

func sortIssues(list []Issue) {
	rank := map[string]int{"high": 0, "medium": 1}
	sort.SliceStable(list, func(i, j int) bool { return rank[list[i].Severity] < rank[list[j].Severity] })
}
