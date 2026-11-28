package devsecops

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/architecture"
)

// Scope dei punti: il soggetto a cui si riferisce un valore.
const (
	ScopeTenant      = "tenant"
	ScopeTeam        = "team"
	ScopeApplication = "application"
	ScopeAsset       = "asset"
	ScopeService     = "service"
)

var scopes = []string{ScopeTenant, ScopeTeam, ScopeApplication, ScopeAsset, ScopeService}

// Periodi di calendario (UTC): settimana dal lunedì, mese, trimestre.
const (
	PeriodWeek    = "week"
	PeriodMonth   = "month"
	PeriodQuarter = "quarter"
)

var periods = []string{PeriodWeek, PeriodMonth, PeriodQuarter}

func validPeriod(p string) bool { return slices.Contains(periods, p) }
func validScope(s string) bool  { return slices.Contains(scopes, s) }

// bucketStart: inizio del periodo di calendario che contiene t.
func bucketStart(t time.Time, period string) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case PeriodWeek:
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case PeriodQuarter:
		return time.Date(t.Year(), time.Month((int(t.Month())-1)/3*3+1), 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func bucketEnd(start time.Time, period string) time.Time {
	switch period {
	case PeriodWeek:
		return start.AddDate(0, 0, 7)
	case PeriodQuarter:
		return start.AddDate(0, 3, 0)
	}
	return start.AddDate(0, 1, 0)
}

// windowBuckets: quanti periodi si ricalcolano e si mostrano per ognuno.
var windowBuckets = map[string]int{PeriodWeek: 26, PeriodMonth: 12, PeriodQuarter: 8}

// windowFrom: inizio del primo periodo della finestra completa; quello della finestra
// breve (il job orario) copre il periodo in corso e il precedente.
func windowFrom(now time.Time, period string, full bool) time.Time {
	n := windowBuckets[period]
	if !full {
		n = 2
	}
	s := bucketStart(now, period)
	for i := 1; i < n; i++ {
		s = prevBucket(s, period)
	}
	return s
}

func prevBucket(start time.Time, period string) time.Time {
	switch period {
	case PeriodWeek:
		return start.AddDate(0, 0, -7)
	case PeriodQuarter:
		return start.AddDate(0, -3, 0)
	}
	return start.AddDate(0, -1, 0)
}

// ---------------------------------------------------------------------------
// Dati

// FailReason spiega perché un deploy è contato come fallito.
type FailReason struct {
	Kind string `json:"kind"` // status | incident | rollback | hotfix | linked
	Text string `json:"text"`
	Ref  string `json:"ref,omitempty"` // chiave dell'incidente, versione del rollback o dell'hotfix
}

// DeployRow è un deploy di produzione con ciò che serve alle metriche.
type DeployRow struct {
	ID          string       `json:"id"`
	ServiceID   string       `json:"service_id"`
	Service     string       `json:"service"`
	AppID       *string      `json:"application_id"`
	Version     string       `json:"version"`
	At          time.Time    `json:"at"`
	Status      string       `json:"status"`
	ReleaseID   *string      `json:"release_id"`
	Commit      *string      `json:"commit"`
	Change      *string      `json:"change"`
	ReleaseURL  *string      `json:"release_url"`
	LeadStart   *time.Time   `json:"lead_start"`
	LeadSource  string       `json:"lead_source,omitempty"` // commit | check_run | change | request
	LeadDays    *float64     `json:"lead_days"`
	Failed      bool         `json:"failed"`
	Reasons     []FailReason `json:"reasons"`
	title, pr   string
	source      string
	linkedIncID string
	app         *appInfo
	cand        [4]*time.Time // inizio del lead time: controlli, change, richiesta, primo commit
}

// IncidentRow è un incidente collegato ad applicazioni o asset.
type IncidentRow struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	DetectedAt time.Time  `json:"detected_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Hours      *float64   `json:"hours"`
	Apps       []string   `json:"applications"`
	// Source e URL: un incidente letto da un sistema esterno (servicenow) ha il link al
	// record; quelli di Regia hanno la scheda (/e/{key}).
	Source string   `json:"source,omitempty"`
	URL    string   `json:"url,omitempty"`
	apps   []string // id delle applicazioni toccate (dirette e degli asset)
	assets []string // id degli asset toccati (diretti e di cui fanno parte le applicazioni)
}

type appInfo struct {
	team   string
	assets []string
}

type doraData struct {
	deploys   []*DeployRow // ordinati per istante
	incidents []*IncidentRow
	apps      map[string]*appInfo
	appNames  map[string]string
}

// loadDora legge i deploy di produzione e gli incidenti nell'intervallo [from, to). Se appID
// non è vuoto, solo quelli dell'applicazione.
func loadDora(ctx context.Context, tx pgx.Tx, from, to time.Time, appID string) (*doraData, error) {
	d := &doraData{apps: map[string]*appInfo{}, appNames: map[string]string{}}
	// Applicazioni: team owner e asset che le compongono.
	rows, err := tx.Query(ctx, `select a.element_id::text, coalesce(a.owner_team_id::text, ''), s.key || '-' || e.number || ' ' || e.title
		from ext_application a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id`)
	if err != nil {
		return nil, err
	}
	var id, team, name string
	if _, err := pgx.ForEachRow(rows, []any{&id, &team, &name}, func() error {
		d.apps[id] = &appInfo{team: team}
		d.appNames[id] = name
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select r.source_id::text, r.target_id::text from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key in ('`+architecture.RelComposedOf+`', '`+architecture.RelUses+`')
		join ext_logical_asset x on x.tenant_id = r.tenant_id and x.element_id = r.source_id`)
	if err != nil {
		return nil, err
	}
	var asset, app string
	assetApps := map[string][]string{}
	if _, err := pgx.ForEachRow(rows, []any{&asset, &app}, func() error {
		if a := d.apps[app]; a != nil && !slices.Contains(a.assets, asset) {
			a.assets = append(a.assets, asset)
		}
		assetApps[asset] = append(assetApps[asset], app)
		return nil
	}); err != nil {
		return nil, err
	}

	// Il candidato «commit» (ultima colonna): il primo committed_at di element_commits dello stesso servizio tra il deploy
	// di produzione precedente (escluso) e questo (incluso); approssima «primo commit → rilascio che lo contiene». Il deploy
	// precedente con la stessa versione si ignora: è il gemello di un rilascio registrato due volte.
	q := `select d.id::text, d.service_id::text, sv.name, coalesce(d.application_element_id, sv.application_element_id)::text,
		d.version, d.started_at, d.status, d.release_id::text, d.caused_incident_element_id::text, coalesce(d.source, ''),
		r.commit, r.change_number, coalesce(r.pull_request, ''), r.release_url, coalesce(c.title, ''), c.created_at, req.created_at,
		(select min(cr.at) from check_runs cr where cr.tenant_id = r.tenant_id and cr.service_id = r.service_id and cr.commit = r.commit),
		(select min(ec.committed_at) from element_commits ec where ec.tenant_id = d.tenant_id and ec.service_id = d.service_id
		   and ec.committed_at <= d.started_at
		   and ec.committed_at > coalesce((select max(p.started_at) from deployments p
		     join environments pe on pe.tenant_id = p.tenant_id and pe.id = p.environment_id and pe.is_production
		     where p.tenant_id = d.tenant_id and p.service_id = d.service_id and p.status <> 'running'
		       and p.started_at < d.started_at and lower(p.version) <> lower(d.version)), '-infinity'::timestamptz))
		from deployments d
		join environments e on e.tenant_id = d.tenant_id and e.id = d.environment_id and e.is_production
		join services sv on sv.tenant_id = d.tenant_id and sv.id = d.service_id
		left join releases r on r.tenant_id = d.tenant_id and r.id = d.release_id
		left join changes c on c.tenant_id = r.tenant_id and c.id = r.change_id
		left join elements req on req.tenant_id = c.tenant_id and req.id = c.request_element_id
		where d.service_id is not null and d.status <> 'running' and d.started_at >= $1 and d.started_at < $2`
	args := []any{from, to}
	if appID != "" {
		q += ` and coalesce(d.application_element_id, sv.application_element_id) = $3`
		args = append(args, appID)
	}
	rows, err = tx.Query(ctx, q+` order by d.started_at, d.id`, args...)
	if err != nil {
		return nil, err
	}
	type cand struct{ check, change, request, commit *time.Time }
	var x DeployRow
	var linked, relID *string
	var c cand
	if _, err := pgx.ForEachRow(rows, []any{&x.ID, &x.ServiceID, &x.Service, &x.AppID, &x.Version, &x.At, &x.Status, &relID, &linked, &x.source,
		&x.Commit, &x.Change, &x.pr, &x.ReleaseURL, &x.title, &c.change, &c.request, &c.check, &c.commit}, func() error {
		y := x
		y.ReleaseID = relID
		if linked != nil {
			y.linkedIncID = *linked
		}
		y.At = y.At.UTC()
		y.Reasons = []FailReason{}
		if y.AppID != nil {
			y.app = d.apps[*y.AppID]
		}
		y.cand = [4]*time.Time{c.check, c.change, c.request, c.commit}
		d.deploys = append(d.deploys, &y)
		x, relID, linked, c = DeployRow{}, nil, nil, cand{}
		return nil
	}); err != nil {
		return nil, err
	}
	// Deploy dei connettori e dei rilasci per la stessa versione: si conta una volta.
	d.deploys = dedupeDeploys(d.deploys)
	// Incidenti collegati ad applicazioni o asset.
	rows, err = tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, i.detected_at, i.resolved_at,
		coalesce(array_agg(r.target_id::text) filter (where tt.key = '`+architecture.AppType+`'), '{}'),
		coalesce(array_agg(r.target_id::text) filter (where tt.key = '`+architecture.AssetType+`'), '{}')
		from ext_incident i
		join elements e on e.tenant_id = i.tenant_id and e.id = i.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join relation_types rt on rt.tenant_id = i.tenant_id and rt.key = '`+doraRelImpacts+`'
		join relations r on r.tenant_id = i.tenant_id and r.source_id = i.element_id and r.type_id = rt.id
		join elements te on te.tenant_id = r.tenant_id and te.id = r.target_id and te.deleted_at is null
		join element_types tt on tt.tenant_id = te.tenant_id and tt.id = te.type_id
		where i.detected_at >= $1 and i.detected_at < $2
		group by 1, 2, 3, 4, 5 order by i.detected_at`, from.Add(-90*24*time.Hour), to)
	if err != nil {
		return nil, err
	}
	var in IncidentRow
	var directApps, directAssets []string
	if _, err := pgx.ForEachRow(rows, []any{&in.ID, &in.Key, &in.Title, &in.DetectedAt, &in.ResolvedAt, &directApps, &directAssets}, func() error {
		j := in
		j.DetectedAt = j.DetectedAt.UTC()
		apps := slices.Clone(directApps)
		assets := slices.Clone(directAssets)
		for _, a := range directAssets {
			for _, ap := range assetApps[a] {
				if !slices.Contains(apps, ap) {
					apps = append(apps, ap)
				}
			}
		}
		for _, ap := range directApps {
			if info := d.apps[ap]; info != nil {
				for _, a := range info.assets {
					if !slices.Contains(assets, a) {
						assets = append(assets, a)
					}
				}
			}
		}
		if len(apps) == 0 && len(assets) == 0 {
			return nil
		}
		if appID != "" && !slices.Contains(apps, appID) {
			return nil
		}
		j.apps, j.assets = apps, assets
		j.Apps = []string{}
		for _, ap := range apps {
			if n := d.appNames[ap]; n != "" {
				j.Apps = append(j.Apps, n)
			}
		}
		if j.ResolvedAt != nil {
			t := j.ResolvedAt.UTC()
			j.ResolvedAt = &t
			if h := t.Sub(j.DetectedAt).Hours(); h >= 0 {
				j.Hours = &h
			}
		}
		d.incidents = append(d.incidents, &j)
		in = IncidentRow{}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := d.loadExternalIncidents(ctx, tx, from.Add(-90*24*time.Hour), to, appID); err != nil {
		return nil, err
	}
	slices.SortStableFunc(d.incidents, func(a, b *IncidentRow) int { return a.DetectedAt.Compare(b.DetectedAt) })
	return d, nil
}

// loadExternalIncidents aggiunge gli incidenti letti da ServiceNow (connettore in sola
// lettura) delle applicazioni associate. Contano se una persona li ha confermati o se
// l'integrazione dice «contano da soli»; mai quelli annullati, esclusi o già diventati un
// incidente di Regia (conta quello, una volta sola).
func (d *doraData) loadExternalIncidents(ctx context.Context, tx pgx.Tx, from, to time.Time, appID string) error {
	q := `select x.id::text, x.number, x.short_description, x.opened_at, x.resolved_at, x.application_element_id::text, x.url
		from servicenow_incidents x
		join integrations i on i.tenant_id = x.tenant_id and i.id = x.integration_id
		where x.application_element_id is not null and x.regia_incident_element_id is null and x.state <> '8'
		  and (x.dora_status = 'counted' or (x.dora_status = 'pending' and coalesce(i.config->>'dora_mode', 'confirm') = 'auto'))
		  and x.opened_at >= $1 and x.opened_at < $2`
	args := []any{from, to}
	if appID != "" {
		q += " and x.application_element_id = $3"
		args = append(args, appID)
	}
	rows, err := tx.Query(ctx, q+" order by x.opened_at", args...)
	if err != nil {
		return err
	}
	var j IncidentRow
	var app string
	_, err = pgx.ForEachRow(rows, []any{&j.ID, &j.Key, &j.Title, &j.DetectedAt, &j.ResolvedAt, &app, &j.URL}, func() error {
		in := j
		in.Source = "servicenow"
		in.DetectedAt = in.DetectedAt.UTC()
		in.apps = []string{app}
		if info := d.apps[app]; info != nil {
			in.assets = slices.Clone(info.assets)
		}
		in.Apps = []string{}
		if n := d.appNames[app]; n != "" {
			in.Apps = append(in.Apps, n)
		}
		if in.ResolvedAt != nil {
			t := in.ResolvedAt.UTC()
			in.ResolvedAt = &t
			if h := t.Sub(in.DetectedAt).Hours(); h >= 0 {
				in.Hours = &h
			}
		}
		d.incidents = append(d.incidents, &in)
		j = IncidentRow{}
		return nil
	})
	return err
}

func dedupeDeploys(in []*DeployRow) []*DeployRow {
	type k struct{ svc, ver string }
	rel := map[k][]time.Time{}
	for _, d := range in {
		if d.source == "release" {
			key := k{d.ServiceID, strings.ToLower(d.Version)}
			rel[key] = append(rel[key], d.At)
		}
	}
	out := in[:0:0]
	for _, d := range in {
		if d.source != "release" && d.ReleaseID == nil {
			dup := false
			for _, t := range rel[k{d.ServiceID, strings.ToLower(d.Version)}] {
				if d.At.Sub(t).Abs() <= 12*time.Hour {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}
