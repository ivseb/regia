package devsecops

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Elenco, dettaglio e cruscotto delle vulnerabilità governate.

// VulnFilter sono i filtri dell'elenco e del cruscotto. Stage: open (da correggere), un
// singolo stadio, closed (risolte, chiuse, falsi positivi) o all. Due: overdue, soon, nosla.
type VulnFilter struct {
	Team        string
	Application string
	Severity    string // elenco separato da virgole
	Stage       string
	Due         string
	Q           string
	Mine        bool
	Sort        string
	Limit       int
	Offset      int
}

// where costruisce la clausola con i parametri numerati da first. forDashboard ignora stadio
// e scadenza: il cruscotto li definisce da sé.
func (fl VulnFilter) where(first int, member string, forDashboard bool, now time.Time, soon int) (string, []any) {
	var cond []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(first+len(args)-1)
	}
	cond = append(cond, "f.severity <> 'info'")
	if fl.Team == "none" {
		cond = append(cond, "f.team_id is null")
	} else if fl.Team != "" {
		cond = append(cond, "f.team_id::text = "+arg(fl.Team))
	}
	if fl.Application != "" {
		cond = append(cond, "(f.application_element_id::text = "+arg(fl.Application)+" or (asp.key || '-' || ae.number) = upper("+arg(fl.Application)+"))")
	}
	if sev := splitList(fl.Severity); len(sev) > 0 {
		cond = append(cond, "f.severity = any("+arg(sev)+"::text[])")
	}
	if q := strings.TrimSpace(fl.Q); q != "" {
		p := arg("%" + strings.ToLower(q) + "%")
		cond = append(cond, "(lower(f.cve) like "+p+" or lower(coalesce(f.package, '')) like "+p+" or lower(sv.name) like "+p+" or lower(coalesce(ae.title, '')) like "+p+")")
	}
	if fl.Mine && member != "" {
		cond = append(cond, "f.assignee_id::text = "+arg(member))
	}
	if !forDashboard {
		switch fl.Stage {
		case "", "open":
			cond = append(cond, "f.stage in "+openStages)
		case "closed":
			cond = append(cond, "f.stage in ('resolved','closed','false_positive')")
		case "all":
		default:
			cond = append(cond, "f.stage = "+arg(fl.Stage))
		}
		switch fl.Due {
		case "overdue":
			cond = append(cond, "f.stage in "+openStages+" and f.due_at < "+arg(now))
		case "soon":
			cond = append(cond, "f.stage in "+openStages+" and f.due_at >= "+arg(now)+" and f.due_at < "+arg(now)+"::timestamptz + "+arg(soon)+"::int * interval '1 day'")
		case "nosla":
			cond = append(cond, "f.stage in "+openStages+" and f.due_at is null")
		}
	}
	return strings.Join(cond, " and "), args
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

const vulnFrom = `
	from findings f
	left join elements ae on ae.tenant_id = f.tenant_id and ae.id = f.application_element_id
	left join spaces asp on asp.tenant_id = ae.tenant_id and asp.id = ae.space_id
	left join ext_application a on a.tenant_id = ae.tenant_id and a.element_id = ae.id
	left join teams t on t.tenant_id = f.tenant_id and t.id = f.team_id
	left join services sv on sv.tenant_id = f.tenant_id and sv.id = f.service_id
	left join members am on am.tenant_id = f.tenant_id and am.id = f.assignee_id
	left join vuln_exceptions x on x.tenant_id = f.tenant_id and x.id = f.exception_id`

// VulnItem è una vulnerabilità nell'elenco.
type VulnItem struct {
	ID          string     `json:"id"`
	CVE         string     `json:"cve"`
	Severity    string     `json:"severity"`
	Package     *string    `json:"package"`
	Version     *string    `json:"version"`
	FixedIn     *string    `json:"fixed_in"`
	Layer       *string    `json:"layer"`
	URL         *string    `json:"url"`
	Stage       string     `json:"stage"`
	StageName   string     `json:"stage_name"`
	FirstSeen   time.Time  `json:"first_seen"`
	DueAt       *time.Time `json:"due_at"`
	SLADays     *int       `json:"sla_days"`
	DaysLeft    *int       `json:"days_left"`
	Overdue     bool       `json:"overdue"`
	ServiceID   *string    `json:"service_id"`
	Service     *string    `json:"service"`
	AppID       *string    `json:"application_id"`
	AppKey      *string    `json:"application_key"`
	App         *string    `json:"application"`
	Criticality *string    `json:"criticality"`
	DORA        bool       `json:"dora"`
	TeamID      *string    `json:"team_id"`
	Team        *string    `json:"team"`
	TeamManual  bool       `json:"team_manual"`
	AssigneeID  *string    `json:"assignee_id"`
	Assignee    *string    `json:"assignee"`
	Exception   *string    `json:"exception"`
	ExcStatus   *string    `json:"exception_status"`
	ExcUntil    *string    `json:"exception_until"`
	ClosedAt    *time.Time `json:"closed_at"`
	ClosedBy    *string    `json:"closed_reason"`
}

const vulnCols = `f.id::text, f.cve, f.severity, f.package, f.installed_version, f.fixed_in, f.layer, f.url, f.stage, f.first_seen, f.due_at, f.sla_days,
	f.service_id::text, sv.name, f.application_element_id::text, asp.key || '-' || ae.number, ae.title, a.criticality, coalesce(a.supports_critical_function, false),
	f.team_id::text, t.name, f.team_manual, f.assignee_id::text, am.display_name,
	case when x.id is null then null else 'EV-' || x.number end, x.status, to_char(x.until, 'YYYY-MM-DD'), f.closed_at, f.closed_reason`

func scanVuln(row pgx.Row, now time.Time) (VulnItem, error) {
	var v VulnItem
	err := row.Scan(&v.ID, &v.CVE, &v.Severity, &v.Package, &v.Version, &v.FixedIn, &v.Layer, &v.URL, &v.Stage, &v.FirstSeen, &v.DueAt, &v.SLADays,
		&v.ServiceID, &v.Service, &v.AppID, &v.AppKey, &v.App, &v.Criticality, &v.DORA, &v.TeamID, &v.Team, &v.TeamManual, &v.AssigneeID, &v.Assignee,
		&v.Exception, &v.ExcStatus, &v.ExcUntil, &v.ClosedAt, &v.ClosedBy)
	v.StageName = StageNames[v.Stage]
	if v.DueAt != nil && (v.Stage == "open" || v.Stage == "taken" || v.Stage == "in_progress") {
		d := int(v.DueAt.Sub(now).Hours() / 24)
		if v.DueAt.After(now) && v.DueAt.Sub(now) < 24*time.Hour {
			d = 0
		} else if v.DueAt.Before(now) {
			d = -int(now.Sub(*v.DueAt).Hours()/24) - 1
			v.Overdue = true
		}
		v.DaysLeft = &d
	}
	return v, err
}

// VulnList è una pagina dell'elenco.
type VulnList struct {
	Items []VulnItem `json:"items"`
	Total int        `json:"total"`
}

// ListVulns elenca le vulnerabilità filtrate, le più urgenti prima.
func (s *Service) ListVulns(ctx context.Context, tx pgx.Tx, actor engine.Actor, fl VulnFilter) (*VulnList, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	where, args := fl.where(1, actor.MemberID, false, now, pol.SoonDays)
	var total int
	if err := tx.QueryRow(ctx, "select count(*) "+vulnFrom+" where "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := fl.Limit
	if limit < 1 || limit > 200 {
		limit = 50
	}
	order := "f.due_at nulls last, case f.severity when 'critical' then 4 when 'high' then 3 when 'medium' then 2 else 1 end desc, f.first_seen"
	switch fl.Sort {
	case "severity":
		order = "case f.severity when 'critical' then 4 when 'high' then 3 when 'medium' then 2 else 1 end desc, f.due_at nulls last, f.first_seen"
	case "age":
		order = "f.first_seen, f.cve"
	case "application":
		order = "ae.title nulls last, f.due_at nulls last"
	}
	rows, err := tx.Query(ctx, "select "+vulnCols+vulnFrom+" where "+where+" order by "+order+" limit "+strconv.Itoa(limit)+" offset "+strconv.Itoa(max(fl.Offset, 0)), args...)
	if err != nil {
		return nil, err
	}
	out := &VulnList{Items: []VulnItem{}, Total: total}
	for rows.Next() {
		v, err := scanVuln(rows, now)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	rows.Close()
	return out, rows.Err()
}

// VulnEvent è un passaggio nella storia di una vulnerabilità.
type VulnEvent struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	From  *string   `json:"from"`
	To    *string   `json:"to"`
	Note  string    `json:"note"`
	Actor *string   `json:"actor"`
}

// VulnDetail è una vulnerabilità con la storia e le altre applicazioni colpite.
type VulnDetail struct {
	VulnItem
	Aliases    []string        `json:"aliases"`
	Events     []VulnEvent     `json:"events"`
	Exceptions []ExceptionView `json:"exceptions"`
	Others     []VulnItem      `json:"others"`
}

// VulnDetail legge una vulnerabilità.
func (s *Service) VulnDetail(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*VulnDetail, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	now := s.now()
	v, err := scanVuln(tx.QueryRow(ctx, "select "+vulnCols+vulnFrom+" where f.id::text = $1", id), now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &VulnDetail{VulnItem: v, Aliases: []string{}, Events: []VulnEvent{}, Exceptions: []ExceptionView{}, Others: []VulnItem{}}
	if err := tx.QueryRow(ctx, "select aliases from findings where id = $1", id).Scan(&d.Aliases); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select e.at, e.kind, e.from_stage, e.to_stage, e.note, m.display_name
		from vuln_events e left join members m on m.tenant_id = e.tenant_id and m.id = e.actor_id
		where e.finding_id = $1 order by e.at desc, e.id limit 60`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e VulnEvent
		if err := rows.Scan(&e.At, &e.Kind, &e.From, &e.To, &e.Note, &e.Actor); err != nil {
			rows.Close()
			return nil, err
		}
		d.Events = append(d.Events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	d.Exceptions, err = s.scanExceptions(ctx, tx, "where x.id in (select exception_id from vuln_exception_findings where finding_id = $1)", "order by x.number desc", id)
	if err != nil {
		return nil, err
	}
	orows, err := tx.Query(ctx, "select "+vulnCols+vulnFrom+" where f.cve = $1 and f.id <> $2 and f.stage in "+openStages+" order by f.due_at nulls last limit 12", d.CVE, id)
	if err != nil {
		return nil, err
	}
	for orows.Next() {
		o, err := scanVuln(orows, now)
		if err != nil {
			orows.Close()
			return nil, err
		}
		d.Others = append(d.Others, o)
	}
	orows.Close()
	return d, orows.Err()
}

// ---------------------------------------------------------------------------
// Cruscotto

// VulnKPIs sono i numeri in alto.
type VulnKPIs struct {
	Open         int      `json:"open"`
	Overdue      int      `json:"overdue"`
	Soon         int      `json:"soon"`
	NoSLA        int      `json:"no_sla"`
	Exceptions   int      `json:"exceptions"`
	Pending      int      `json:"exceptions_pending"`
	Resolved30   int      `json:"resolved_30d"`
	MTTRDays     *float64 `json:"mttr_days"`
	AvgAgeDays   *float64 `json:"avg_age_days"`
	SoonDays     int      `json:"soon_days"`
	CriticalOpen int      `json:"critical_open"` // tutte le critiche da correggere
	CriticalProd int      `json:"critical_prod"` // quelle sulla versione in produzione: lo stesso numero del pannello DevSecOps
}

// AgingRow: vulnerabilità da correggere di una gravità per età.
type AgingRow struct {
	Severity string `json:"severity"`
	Buckets  [4]int `json:"buckets"` // 0-7, 8-30, 31-90, oltre 90 giorni
	Overdue  int    `json:"overdue"`
	Total    int    `json:"total"`
}

// GroupRow è una riga per team o per applicazione.
type GroupRow struct {
	ID          string  `json:"id"`
	Key         string  `json:"key,omitempty"`
	Name        string  `json:"name"`
	Team        *string `json:"team,omitempty"`
	Criticality *string `json:"criticality,omitempty"`
	DORA        bool    `json:"dora,omitempty"`
	Open        int     `json:"open"`
	Overdue     int     `json:"overdue"`
	Soon        int     `json:"soon"`
	Critical    int     `json:"critical"`
	Exceptions  int     `json:"exceptions"`
	OldestDays  int     `json:"oldest_overdue_days"`
}

// TrendPoint è una settimana: aperte a fine settimana, scadute, nuove e chiuse nella settimana.
type TrendPoint struct {
	At      time.Time `json:"at"`
	Open    int       `json:"open"`
	Overdue int       `json:"overdue"`
	Opened  int       `json:"opened"`
	Closed  int       `json:"closed"`
}

// VulnDashboard è il cruscotto.
type VulnDashboard struct {
	KPIs   VulnKPIs     `json:"kpis"`
	Aging  []AgingRow   `json:"aging"`
	Teams  []GroupRow   `json:"teams"`
	Apps   []GroupRow   `json:"applications"`
	Trend  []TrendPoint `json:"trend"`
	Filter struct {
		Teams []IDName `json:"teams"`
		Apps  []IDName `json:"applications"`
	} `json:"filters"`
	CanAct       bool `json:"can_act"`
	CanConfigure bool `json:"can_configure"`
}

// IDName è una voce di elenco.
type IDName struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
}

// VulnDashboard calcola il cruscotto. I filtri su team, applicazione, gravità e testo valgono per
// tutto; stadio e scadenza no (il cruscotto fissa da sé cosa conta).
func (s *Service) VulnDashboard(ctx context.Context, tx pgx.Tx, actor engine.Actor, fl VulnFilter) (*VulnDashboard, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	where, args := fl.where(3, actor.MemberID, true, now, pol.SoonDays)
	d := &VulnDashboard{Aging: []AgingRow{}, Teams: []GroupRow{}, Apps: []GroupRow{}, Trend: []TrendPoint{}, CanAct: canAct(actor), CanConfigure: CanConfigure(actor)}
	d.KPIs.SoonDays = pol.SoonDays
	prodIDs, err := s.prodCriticalIDs(ctx, tx)
	if err != nil {
		return nil, err
	}
	common := append([]any{now, pol.SoonDays}, args...)
	prodArg := "$" + strconv.Itoa(len(common)+1)
	openish := "f.stage in " + openStages

	err = tx.QueryRow(ctx, `select count(*) filter (where `+openish+`),
		count(*) filter (where `+openish+` and f.due_at < $1::timestamptz),
		count(*) filter (where `+openish+` and f.due_at >= $1::timestamptz and f.due_at < $1::timestamptz + $2::int * interval '1 day'),
		count(*) filter (where `+openish+` and f.due_at is null),
		count(*) filter (where `+openish+` and f.severity = 'critical'),
		count(*) filter (where `+openish+` and f.severity = 'critical' and f.id::text = any(`+prodArg+`::text[])),
		count(*) filter (where f.stage in ('resolved','closed') and f.closed_at >= $1::timestamptz - interval '30 days'),
		avg(extract(epoch from (f.closed_at - f.first_seen)) / 86400) filter (where f.stage in ('resolved','closed') and f.closed_at >= $1::timestamptz - interval '90 days'),
		avg(extract(epoch from ($1::timestamptz - f.first_seen)) / 86400) filter (where `+openish+`)
		`+vulnFrom+` where `+where, append(slices.Clone(common), prodIDs)...).
		Scan(&d.KPIs.Open, &d.KPIs.Overdue, &d.KPIs.Soon, &d.KPIs.NoSLA, &d.KPIs.CriticalOpen, &d.KPIs.CriticalProd, &d.KPIs.Resolved30, &d.KPIs.MTTRDays, &d.KPIs.AvgAgeDays)
	if err != nil {
		return nil, err
	}
	// Eccezioni: in vigore e in attesa, per le applicazioni del filtro.
	exWhere, exArgs := "", []any{s.today()}
	if fl.Application != "" {
		exWhere = ` and (x.application_id::text = $2 or (xs.key || '-' || xe.number) = upper($2))`
		exArgs = append(exArgs, fl.Application)
	}
	if err := tx.QueryRow(ctx, `select count(*) filter (where x.status = 'approved' and x.until >= $1), count(*) filter (where x.status = 'pending')
		from vuln_exceptions x join elements xe on xe.tenant_id = x.tenant_id and xe.id = x.application_id
		join spaces xs on xs.tenant_id = xe.tenant_id and xs.id = xe.space_id where true`+exWhere, exArgs...).Scan(&d.KPIs.Exceptions, &d.KPIs.Pending); err != nil {
		return nil, err
	}

	// Età delle vulnerabilità da correggere per gravità.
	rows, err := tx.Query(ctx, `select f.severity,
		count(*) filter (where $1::timestamptz - f.first_seen < interval '8 days'),
		count(*) filter (where $1::timestamptz - f.first_seen >= interval '8 days' and $1::timestamptz - f.first_seen < interval '31 days'),
		count(*) filter (where $1::timestamptz - f.first_seen >= interval '31 days' and $1::timestamptz - f.first_seen < interval '91 days'),
		count(*) filter (where $1::timestamptz - f.first_seen >= interval '91 days'),
		count(*) filter (where f.due_at < $1::timestamptz), count(*)
		`+vulnFrom+` where `+where+` and `+openish+` and $2::int is not null group by 1`, common...)
	if err != nil {
		return nil, err
	}
	bySev := map[string]AgingRow{}
	for rows.Next() {
		var r AgingRow
		if err := rows.Scan(&r.Severity, &r.Buckets[0], &r.Buckets[1], &r.Buckets[2], &r.Buckets[3], &r.Overdue, &r.Total); err != nil {
			rows.Close()
			return nil, err
		}
		bySev[r.Severity] = r
	}
	rows.Close()
	for _, sev := range VulnSeverities {
		r, ok := bySev[sev]
		if !ok {
			r = AgingRow{Severity: sev}
		}
		d.Aging = append(d.Aging, r)
	}

	// Per team e per applicazione.
	group := func(sel, from, groupBy, order string, limit int) ([]GroupRow, error) {
		rows, err := tx.Query(ctx, `select `+sel+`,
			count(*), count(*) filter (where f.due_at < $1::timestamptz),
			count(*) filter (where f.due_at >= $1::timestamptz and f.due_at < $1::timestamptz + $2::int * interval '1 day'),
			count(*) filter (where f.severity in ('critical','high')),
			coalesce(max(extract(epoch from ($1::timestamptz - f.due_at)) / 86400) filter (where f.due_at < $1::timestamptz), 0)::int
			`+from+` where `+where+` and `+openish+` group by `+groupBy+` order by `+order+` limit `+strconv.Itoa(limit), common...)
		if err != nil {
			return nil, err
		}
		var out []GroupRow
		for rows.Next() {
			var r GroupRow
			var id, key *string
			if err := rows.Scan(&id, &key, &r.Name, &r.Team, &r.Criticality, &r.DORA, &r.Open, &r.Overdue, &r.Soon, &r.Critical, &r.OldestDays); err != nil {
				rows.Close()
				return nil, err
			}
			if id != nil {
				r.ID = *id
			}
			if key != nil {
				r.Key = *key
			}
			out = append(out, r)
		}
		rows.Close()
		return out, rows.Err()
	}
	if d.Teams, err = group(`f.team_id::text, null::text, coalesce(t.name, 'Senza team'), null::text, null::text, false`, vulnFrom,
		"f.team_id, t.name", "8 desc, 7 desc, 3", 25); err != nil {
		return nil, err
	}
	// Ordine: scadute, poi aperte.
	if d.Apps, err = group(`f.application_element_id::text, asp.key || '-' || ae.number, coalesce(ae.title, 'Senza applicazione'), t.name, a.criticality, coalesce(a.supports_critical_function, false)`, vulnFrom,
		"f.application_element_id, asp.key, ae.number, ae.title, t.name, a.criticality, a.supports_critical_function", "8 desc, 7 desc, 3", 30); err != nil {
		return nil, err
	}
	// Le colonne dell'elenco per team contano da sole le eccezioni: una query a parte.
	exc := map[string]int{}
	erows, err := tx.Query(ctx, `select application_id::text, count(*) from vuln_exceptions where status = 'approved' and until >= $1::date group by 1`, s.today())
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var id string
		var n int
		if err := erows.Scan(&id, &n); err != nil {
			erows.Close()
			return nil, err
		}
		exc[id] = n
	}
	erows.Close()
	for i := range d.Apps {
		d.Apps[i].Exceptions = exc[d.Apps[i].ID]
	}

	// Andamento delle ultime dodici settimane.
	trows, err := tx.Query(ctx, `select w.t,
		count(*) filter (where f.first_seen <= w.t and (f.closed_at is null or f.closed_at > w.t)),
		count(*) filter (where f.first_seen <= w.t and (f.closed_at is null or f.closed_at > w.t) and f.due_at < w.t),
		count(*) filter (where f.first_seen > w.t - interval '7 days' and f.first_seen <= w.t),
		count(*) filter (where f.closed_at > w.t - interval '7 days' and f.closed_at <= w.t)
		from generate_series($1::timestamptz - interval '77 days', $1::timestamptz, interval '7 days') w(t)
		left join lateral (select f.* `+vulnFrom+` where `+where+` and $2::int is not null) f on true
		group by w.t order by w.t`, common...)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var p TrendPoint
		if err := trows.Scan(&p.At, &p.Open, &p.Overdue, &p.Opened, &p.Closed); err != nil {
			trows.Close()
			return nil, err
		}
		d.Trend = append(d.Trend, p)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, err
	}

	// Voci dei filtri.
	frows, err := tx.Query(ctx, "select id::text, name from teams where not archived order by name")
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var t IDName
		if err := frows.Scan(&t.ID, &t.Name); err != nil {
			frows.Close()
			return nil, err
		}
		d.Filter.Teams = append(d.Filter.Teams, t)
	}
	frows.Close()
	arows, err := tx.Query(ctx, `select distinct ae.id::text, asp.key || '-' || ae.number, ae.title
		from findings f join elements ae on ae.tenant_id = f.tenant_id and ae.id = f.application_element_id
		join spaces asp on asp.tenant_id = ae.tenant_id and asp.id = ae.space_id
		where f.stage in `+openStages+` order by 3 limit 300`)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var t IDName
		if err := arows.Scan(&t.ID, &t.Key, &t.Name); err != nil {
			arows.Close()
			return nil, err
		}
		d.Filter.Apps = append(d.Filter.Apps, t)
	}
	arows.Close()
	if d.Filter.Teams == nil {
		d.Filter.Teams = []IDName{}
	}
	if d.Filter.Apps == nil {
		d.Filter.Apps = []IDName{}
	}
	return d, arows.Err()
}
