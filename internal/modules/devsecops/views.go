package devsecops

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/devsecops/contract"
)

// Viste per l'interfaccia: pannello dell'asset o dell'applicazione, panoramica del
// portafoglio, storico dei rilasci di un servizio. Letture piccole e a insiemi: poche query
// per pagina, qualunque sia il numero dei servizi.

// ChangeRef è un change con la richiesta collegata.
type ChangeRef struct {
	ID           string   `json:"id"`
	Number       string   `json:"number"`
	Aliases      []string `json:"aliases"`
	Title        string   `json:"title"`
	RequestID    *string  `json:"request_id"`
	RequestKey   *string  `json:"request_key"`
	RequestTitle *string  `json:"request_title"`
	RequestState *string  `json:"request_state"`
	LinkedBy     *string  `json:"linked_by"`
}

// ReleaseView è un rilascio.
type ReleaseView struct {
	ID            string                 `json:"id"`
	ServiceID     string                 `json:"service_id"`
	Service       string                 `json:"service"`
	ApplicationID *string                `json:"application_id"`
	Application   *string                `json:"application"`
	Environment   string                 `json:"environment"`
	Production    bool                   `json:"production"`
	Version       string                 `json:"version"`
	Commit        *string                `json:"commit"`
	Image         *string                `json:"image"`
	Digest        *string                `json:"digest"`
	At            time.Time              `json:"at"`
	RequestedBy   *string                `json:"requested_by"`
	ApprovedBy    *string                `json:"approved_by"`
	ApprovedAt    *time.Time             `json:"approved_at"`
	PullRequest   *string                `json:"pull_request"`
	ReleaseURL    *string                `json:"release_url"`
	Change        *ChangeRef             `json:"change"`
	ChangeNumber  *string                `json:"change_number"`
	CVEsDeclared  *contract.VulnSummary  `json:"cves_at_release"`
	Checks        []contract.CheckResult `json:"checks"`
	ChecksStatus  string                 `json:"checks_status"`
}

const selectRelease = `
	select r.id::text, r.service_id::text, sv.name, sv.application_element_id::text, ap.title, en.name, en.is_production, r.version,
	       r.commit, r.image, r.digest, r.released_at, r.requested_by, r.approved_by, r.approved_at, r.pull_request, r.release_url,
	       r.change_number, r.cves_at_release, r.checks,
	       c.id::text, c.number, c.aliases, c.title, c.request_element_id::text, rs.key || '-' || re.number, re.title, ro.name, c.request_source
	from releases r
	join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
	join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id
	left join elements ap on ap.tenant_id = sv.tenant_id and ap.id = sv.application_element_id
	left join changes c on c.tenant_id = r.tenant_id and c.id = r.change_id
	left join elements re on re.tenant_id = c.tenant_id and re.id = c.request_element_id and re.deleted_at is null
	left join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id`

func loadReleases(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]ReleaseView, error) {
	rows, err := tx.Query(ctx, selectRelease+" where "+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReleaseView, error) {
		var r ReleaseView
		var cves, checks []byte
		var cid, cnum, ctitle *string
		var aliases []string
		var ch ChangeRef
		err := row.Scan(&r.ID, &r.ServiceID, &r.Service, &r.ApplicationID, &r.Application, &r.Environment, &r.Production, &r.Version,
			&r.Commit, &r.Image, &r.Digest, &r.At, &r.RequestedBy, &r.ApprovedBy, &r.ApprovedAt, &r.PullRequest, &r.ReleaseURL,
			&r.ChangeNumber, &cves, &checks, &cid, &cnum, &aliases, &ctitle, &ch.RequestID, &ch.RequestKey, &ch.RequestTitle, &ch.RequestState, &ch.LinkedBy)
		if err != nil {
			return r, err
		}
		if cid != nil {
			ch.ID, ch.Number, ch.Aliases, ch.Title = *cid, *cnum, aliases, *ctitle
			if ch.Aliases == nil {
				ch.Aliases = []string{}
			}
			if ch.RequestKey == nil {
				ch.RequestID = nil
			}
			r.Change = &ch
		}
		if len(cves) > 0 {
			r.CVEsDeclared = &contract.VulnSummary{}
			_ = json.Unmarshal(cves, r.CVEsDeclared)
		}
		r.Checks = []contract.CheckResult{}
		_ = json.Unmarshal(checks, &r.Checks)
		r.ChecksStatus = checksStatus(r.Checks)
		return r, nil
	})
}

// AppRef è un'applicazione del pannello.
type AppRef struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	Name             string  `json:"name"`
	Code             *string `json:"code"`
	Criticality      *string `json:"criticality"`
	CriticalFunction bool    `json:"supports_critical_function"`
	Team             *string `json:"team"`
}

// EnvCell è la versione di un servizio in un ambiente.
type EnvCell struct {
	Version    string    `json:"version"`
	At         time.Time `json:"at"`
	LastStatus string    `json:"last_status"` // stato dell'ultimo tentativo (anche fallito)
	LastAt     time.Time `json:"last_at"`
}

// ServiceRow è un servizio del pannello.
type ServiceRow struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Repository       *string            `json:"repository"`
	Provider         *string            `json:"provider"`
	URL              *string            `json:"url"`
	ApplicationID    *string            `json:"application_id"`
	ApplicationKey   *string            `json:"application_key"`
	Application      *string            `json:"application"`
	OnPipeline       bool               `json:"on_pipeline"`
	FirstSeen        *time.Time         `json:"first_seen_at"`
	LastSeen         *time.Time         `json:"last_seen_at"`
	ProdVersion      string             `json:"prod_version"`
	ProdSince        *time.Time         `json:"prod_since"`
	Release          *ReleaseView       `json:"release"`
	CVEToday         *CVECounts         `json:"cves_today"`
	CVEAtRelease     *CVECounts         `json:"cves_at_release"`
	Runtimes         []RuntimeView      `json:"runtimes"`
	DaysSinceRelease *int               `json:"days_since_release"`
	Stale            bool               `json:"stale"`
	Envs             map[string]EnvCell `json:"environments"`
	Releases         int                `json:"releases"`
}

// Trend è un numero oggi e sette giorni fa.
type Trend struct {
	Now     int `json:"now"`
	WeekAgo int `json:"week_ago"`
}

// KPIs sono i tre numeri in testa al pannello.
type KPIs struct {
	CriticalCVEs   Trend `json:"critical_cves"`
	BlockingChecks Trend `json:"blocking_checks"`
	Stale          Trend `json:"stale"`
	StaleDays      int   `json:"stale_days"`
	Services       int   `json:"services"`
	OnPipeline     int   `json:"on_pipeline"`
	Unscanned      int   `json:"unscanned"` // servizi la cui versione in produzione non ha mai avuto una scansione
}

// EnvView è un ambiente.
type EnvView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Production bool   `json:"production"`
	Ordinal    int    `json:"ordinal"`
}

// ChangeGroup sono i rilasci di un change (o quelli senza change).
type ChangeGroup struct {
	Change   *ChangeRef    `json:"change"`
	Releases []ReleaseView `json:"releases"`
	Last     time.Time     `json:"last"`
}

// Panel è il pannello "Produzione" di un asset logico o di un'applicazione.
type Panel struct {
	Subject      map[string]string `json:"subject"`
	Applications []AppRef          `json:"applications"`
	KPIs         KPIs              `json:"kpis"`
	Services     []ServiceRow      `json:"services"`
	Environments []EnvView         `json:"environments"`
	Changes      []ChangeGroup     `json:"changes"`
	CanConfigure bool              `json:"can_configure"`
}

// elementRef risolve una chiave (ARC-3) o un id in elemento con il tipo.
func elementRef(ctx context.Context, tx pgx.Tx, ref string) (id, key, title, typ string, err error) {
	err = tx.QueryRow(ctx, `select e.id::text, s.key || '-' || e.number, e.title, t.key from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where e.deleted_at is null and (e.id::text = $1 or (e.number is not null and s.key || '-' || e.number = upper($1)))`, strings.TrimSpace(ref)).
		Scan(&id, &key, &title, &typ)
	if errors.Is(err, pgx.ErrNoRows) {
		err = engine.ErrNotFound
	}
	return
}

func appsOf(ctx context.Context, tx pgx.Tx, ids []string) ([]AppRef, error) {
	rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, a.code, a.criticality, a.supports_critical_function, t.name
		from elements e join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id
		where e.id = any($1) and e.deleted_at is null order by e.title`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[AppRef])
}

// assetApps: le applicazioni che compongono l'asset o che l'asset usa.
func assetApps(ctx context.Context, tx pgx.Tx, assetID string) ([]string, error) {
	rows, err := tx.Query(ctx, `select r.target_id::text from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key in ('`+architecture.RelComposedOf+`', '`+architecture.RelUses+`')
		where r.source_id = $1`, assetID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Panel compone il pannello di un asset logico (le sue applicazioni) o di un'applicazione.
func (s *Service) Panel(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Panel, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	id, key, title, typ, err := elementRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var appIDs []string
	switch typ {
	case architecture.AssetType:
		if appIDs, err = assetApps(ctx, tx, id); err != nil {
			return nil, err
		}
	case architecture.AppType:
		appIDs = []string{id}
	default:
		return nil, engine.InvalidCode("validation.devsecops.pipeline_ref_scope", "ref", "il pannello è di un asset logico o di un'applicazione")
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	p := &Panel{Subject: map[string]string{"id": id, "key": key, "title": title, "type": typ}, CanConfigure: CanConfigure(actor)}
	if p.Applications, err = appsOf(ctx, tx, appIDs); err != nil {
		return nil, err
	}
	rows, err := s.serviceRows(ctx, tx, "s.application_element_id = any($1)", st, appIDs)
	if err != nil {
		return nil, err
	}
	p.Services = rows
	svcIDs := make([]string, len(rows))
	for i, r := range rows {
		svcIDs[i] = r.ID
	}
	if p.KPIs, err = s.kpis(ctx, tx, svcIDs, rows, st); err != nil {
		return nil, err
	}
	if p.Environments, err = envsOf(ctx, tx, svcIDs); err != nil {
		return nil, err
	}
	// Rilasci raggruppati per change: gli ultimi 120 dei servizi del pannello.
	rels, err := loadReleases(ctx, tx, "r.service_id = any($1) order by r.released_at desc limit 120", svcIDs)
	if err != nil {
		return nil, err
	}
	p.Changes = groupByChange(rels)
	return p, nil
}

func groupByChange(rels []ReleaseView) []ChangeGroup {
	idx := map[string]int{}
	out := []ChangeGroup{}
	for _, r := range rels {
		k := ""
		if r.Change != nil {
			k = r.Change.ID
		}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, ChangeGroup{Change: r.Change, Last: r.At})
		}
		out[i].Releases = append(out[i].Releases, r)
	}
	// I rilasci senza change vanno in fondo.
	sort.SliceStable(out, func(a, b int) bool {
		if (out[a].Change == nil) != (out[b].Change == nil) {
			return out[b].Change == nil
		}
		return out[a].Last.After(out[b].Last)
	})
	return out
}

func envsOf(ctx context.Context, tx pgx.Tx, svcIDs []string) ([]EnvView, error) {
	rows, err := tx.Query(ctx, `select e.id::text, e.name, e.is_production, e.ordinal from environments e
		where exists (select 1 from deployments d where d.tenant_id = e.tenant_id and d.environment_id = e.id and d.service_id = any($1))
		order by e.ordinal, e.name`, svcIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[EnvView])
}

// serviceRows carica i servizi con lo stato di oggi, gli ambienti, le CVE e i runtime.
func (s *Service) serviceRows(ctx context.Context, tx pgx.Tx, where string, st Settings, args ...any) ([]ServiceRow, error) {
	rows, err := tx.Query(ctx, `select s.id::text, s.name, s.repository, s.provider, s.url, s.application_element_id::text,
		sp.key || '-' || e.number, e.title, s.first_seen_at is not null, s.first_seen_at, s.last_seen_at,
		(select count(*) from releases r where r.service_id = s.id)
		from services s left join elements e on e.tenant_id = s.tenant_id and e.id = s.application_element_id
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where `+where+` order by (s.first_seen_at is null), s.name`, args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ServiceRow, error) {
		var x ServiceRow
		err := r.Scan(&x.ID, &x.Name, &x.Repository, &x.Provider, &x.URL, &x.ApplicationID, &x.ApplicationKey, &x.Application,
			&x.OnPipeline, &x.FirstSeen, &x.LastSeen, &x.Releases)
		x.Envs, x.Runtimes = map[string]EnvCell{}, []RuntimeView{}
		return x, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	now := s.now()
	ids := make([]string, len(list))
	byID := map[string]*ServiceRow{}
	for i := range list {
		ids[i] = list[i].ID
		byID[list[i].ID] = &list[i]
	}
	states, err := statesAt(ctx, tx, ids, now.Add(time.Minute), true)
	if err != nil {
		return nil, err
	}
	var relIDs, verSvc, verVer []string
	for id, x := range states {
		r := byID[id]
		r.ProdVersion, r.ProdSince, r.CVEToday = x.ProdVersion, x.ProdSince, x.CVE
		if x.LastProdRelease != nil {
			d := int(now.Sub(*x.LastProdRelease).Hours() / 24)
			r.DaysSinceRelease = &d
			r.Stale = stale(x, now, st.StaleDays)
		}
		if x.ReleaseID != "" {
			relIDs = append(relIDs, x.ReleaseID)
		}
		if x.ProdVersion != "" {
			verSvc, verVer = append(verSvc, id), append(verVer, x.ProdVersion)
		}
	}
	if len(relIDs) > 0 {
		rels, err := loadReleases(ctx, tx, "r.id = any($1)", relIDs)
		if err != nil {
			return nil, err
		}
		for i := range rels {
			byID[rels[i].ServiceID].Release = &rels[i]
		}
	}
	// CVE al rilascio: dichiarate dal rilascio, altrimenti il primo report della versione.
	first, err := cveCounts(ctx, tx, verSvc, verVer, now.Add(time.Minute), true, false)
	if err != nil {
		return nil, err
	}
	for _, r := range list {
		x := byID[r.ID]
		if x.Release != nil && x.Release.CVEsDeclared != nil {
			d := x.Release.CVEsDeclared
			x.CVEAtRelease = &CVECounts{Critical: d.Critical, High: d.High, Medium: d.Medium, Low: d.Low,
				FixableCritical: d.FixableCritical, FixableHigh: d.FixableHigh, Declared: true}
		} else if c := first[r.ID]; c != nil {
			x.CVEAtRelease = c
		}
	}
	rts, err := runtimes(ctx, tx, verSvc, verVer, st, now)
	if err != nil {
		return nil, err
	}
	for id, v := range rts {
		byID[id].Runtimes = v
	}
	// Ambienti: l'ultima versione riuscita e l'ultimo tentativo per servizio e ambiente.
	erows, err := tx.Query(ctx, `
		select x.service_id::text, e.name, ok.version, ok.started_at, x.status, x.started_at from (
			select distinct on (d.service_id, d.environment_id) d.service_id, d.environment_id, d.status, d.started_at
			from deployments d where d.service_id = any($1) order by d.service_id, d.environment_id, d.started_at desc) x
		join environments e on e.tenant_id = app_tenant() and e.id = x.environment_id
		left join lateral (select d.version, d.started_at from deployments d where d.service_id = x.service_id and d.environment_id = x.environment_id
			and d.status in ('succeeded', 'running') order by d.started_at desc limit 1) ok on true`, ids)
	if err != nil {
		return nil, err
	}
	var sid, env, status string
	var ver *string
	var okAt *time.Time
	var lastAt time.Time
	if _, err := pgx.ForEachRow(erows, []any{&sid, &env, &ver, &okAt, &status, &lastAt}, func() error {
		c := EnvCell{LastStatus: status, LastAt: lastAt}
		if ver != nil {
			c.Version, c.At = *ver, *okAt
		}
		byID[sid].Envs[env] = c
		return nil
	}); err != nil {
		return nil, err
	}
	return list, nil
}

// kpis: CVE critiche in produzione, servizi con controlli bloccanti falliti o in eccezione,
// servizi non rilasciati da più di N giorni; oggi e sette giorni fa.
func (s *Service) kpis(ctx context.Context, tx pgx.Tx, ids []string, rows []ServiceRow, st Settings) (KPIs, error) {
	k := KPIs{StaleDays: st.StaleDays, Services: len(rows)}
	for _, r := range rows {
		if r.OnPipeline {
			k.OnPipeline++
		}
		if r.CVEToday != nil {
			k.CriticalCVEs.Now += r.CVEToday.Critical
		} else if r.ProdVersion != "" {
			k.Unscanned++
		}
		if r.Release != nil && blocking(r.Release.Checks) {
			k.BlockingChecks.Now++
		}
		if r.Stale {
			k.Stale.Now++
		}
	}
	week := s.now().AddDate(0, 0, -7)
	past, err := statesAt(ctx, tx, ids, week, false)
	if err != nil {
		return k, err
	}
	for _, x := range past {
		if x.CVE != nil {
			k.CriticalCVEs.WeekAgo += x.CVE.Critical
		}
		if blocking(x.Checks) {
			k.BlockingChecks.WeekAgo++
		}
		if stale(x, week, st.StaleDays) {
			k.Stale.WeekAgo++
		}
	}
	return k, nil
}

// ---------------------------------------------------------------------------
// Storico di un servizio

// ServiceView è un servizio con il suo storico dei rilasci.
type ServiceView struct {
	ServiceRow
	Declared *string       `json:"declared_application"`
	Mapping  *string       `json:"mapping_source"`
	History  []ReleaseView `json:"history"`
}

func (s *Service) serviceView(ctx context.Context, tx pgx.Tx, id string) (*ServiceView, error) {
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := s.serviceRows(ctx, tx, "s.id::text = $1", st, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, engine.ErrNotFound
	}
	v := &ServiceView{ServiceRow: rows[0]}
	if err := tx.QueryRow(ctx, "select declared_application, mapping_source from services where id = $1", rows[0].ID).Scan(&v.Declared, &v.Mapping); err != nil {
		return nil, err
	}
	return v, nil
}

// ServiceHistory: il servizio e i suoi rilasci, dal più recente (al massimo 200).
func (s *Service) ServiceHistory(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*ServiceView, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	v, err := s.serviceView(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if v.History, err = loadReleases(ctx, tx, "r.service_id = $1 order by r.released_at desc limit 200", v.ID); err != nil {
		return nil, err
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Panoramica del portafoglio

// AssetSummary è una riga del riepilogo per asset.
type AssetSummary struct {
	ID           string     `json:"id"`
	Key          string     `json:"key"`
	Name         string     `json:"name"`
	Applications int        `json:"applications"`
	Services     int        `json:"services"`
	OnPipeline   int        `json:"on_pipeline"`
	Critical     int        `json:"critical"`
	High         int        `json:"high"`
	Blocking     int        `json:"blocking"`
	Stale        int        `json:"stale"`
	Unsupported  int        `json:"unsupported_runtimes"`
	LastRelease  *time.Time `json:"last_release"`
	DORA         bool       `json:"dora"`
}

// VulnRow è una vulnerabilità in produzione.
type VulnRow struct {
	CVE              string    `json:"id"`
	Severity         string    `json:"severity"`
	Package          *string   `json:"package"`
	Version          *string   `json:"version"`
	FixedIn          *string   `json:"fixed_in"`
	Layer            *string   `json:"layer"`
	URL              *string   `json:"url"`
	FirstSeen        time.Time `json:"first_seen"`
	ServiceID        string    `json:"service_id"`
	Service          string    `json:"service"`
	ProdVersion      string    `json:"prod_version"`
	ApplicationID    *string   `json:"application_id"`
	ApplicationKey   *string   `json:"application_key"`
	Application      *string   `json:"application"`
	Criticality      *string   `json:"criticality"`
	CriticalFunction bool      `json:"supports_critical_function"`
}

// Overview è la pagina /devsecops.
type Overview struct {
	KPIs       KPIs           `json:"kpis"`
	Assets     []AssetSummary `json:"assets"`
	Unassigned AssetSummary   `json:"unassigned"` // servizi di applicazioni senza asset o non associati
	Critical   []VulnRow      `json:"critical"`
	Releases   []ReleaseView  `json:"releases"`
	Pending    int            `json:"unmapped_services"`
}

func (s *Service) Overview(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Overview, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := s.serviceRows(ctx, tx, "true", st)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	o := &Overview{Assets: []AssetSummary{}, Unassigned: AssetSummary{Name: "Senza asset"}}
	if o.KPIs, err = s.kpis(ctx, tx, ids, rows, st); err != nil {
		return nil, err
	}
	// Asset → applicazioni.
	arows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title,
		coalesce(array_agg(r.target_id::text) filter (where r.target_id is not null), '{}'),
		coalesce(bool_or(a.supports_critical_function), false)
		from elements e join ext_logical_asset x on x.tenant_id = e.tenant_id and x.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join relation_types rt on rt.tenant_id = e.tenant_id and rt.key in ('`+architecture.RelComposedOf+`', '`+architecture.RelUses+`')
		left join relations r on r.tenant_id = e.tenant_id and r.source_id = e.id and r.type_id = rt.id
		left join ext_application a on a.tenant_id = r.tenant_id and a.element_id = r.target_id
		where e.deleted_at is null group by 1, 2, 3 order by e.title`)
	if err != nil {
		return nil, err
	}
	type assetApps struct {
		sum  AssetSummary
		apps []string
	}
	var assets []assetApps
	var a assetApps
	if _, err := pgx.ForEachRow(arows, []any{&a.sum.ID, &a.sum.Key, &a.sum.Name, &a.apps, &a.sum.DORA}, func() error {
		x := a
		x.sum.Applications = len(x.apps)
		assets = append(assets, x)
		return nil
	}); err != nil {
		return nil, err
	}
	inAsset := map[string]bool{}
	for _, as := range assets {
		for _, ap := range as.apps {
			inAsset[ap] = true
		}
	}
	add := func(sum *AssetSummary, r ServiceRow) {
		sum.Services++
		if r.OnPipeline {
			sum.OnPipeline++
		}
		if r.CVEToday != nil {
			sum.Critical += r.CVEToday.Critical
			sum.High += r.CVEToday.High
		}
		if r.Release != nil && blocking(r.Release.Checks) {
			sum.Blocking++
		}
		if r.Stale {
			sum.Stale++
		}
		for _, rt := range r.Runtimes {
			if rt.Unsupported {
				sum.Unsupported++
				break
			}
		}
		if r.Release != nil && (sum.LastRelease == nil || r.Release.At.After(*sum.LastRelease)) {
			t := r.Release.At
			sum.LastRelease = &t
		}
	}
	for _, as := range assets {
		sum := as.sum
		for _, r := range rows {
			if r.ApplicationID != nil && slices.Contains(as.apps, *r.ApplicationID) {
				add(&sum, r)
			}
		}
		o.Assets = append(o.Assets, sum)
	}
	for _, r := range rows {
		if r.ApplicationID == nil {
			o.Pending++
		}
		if r.ApplicationID == nil || !inAsset[*r.ApplicationID] {
			add(&o.Unassigned, r)
		}
	}
	// Vulnerabilità critiche in produzione, le applicazioni DORA prima.
	var verSvc, verVer []string
	for _, r := range rows {
		if r.ProdVersion != "" {
			verSvc, verVer = append(verSvc, r.ID), append(verVer, r.ProdVersion)
		}
	}
	if o.Critical, err = criticalInProd(ctx, tx, verSvc, verVer); err != nil {
		return nil, err
	}
	if o.Releases, err = loadReleases(ctx, tx, "true order by r.released_at desc limit 25"); err != nil {
		return nil, err
	}
	return o, nil
}

// prodCriticalIDs: gli id delle vulnerabilità critiche in produzione (vedi la definizione unica
// in state.go), per contarle con gli stessi criteri nelle altre viste.
func (s *Service) prodCriticalIDs(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, "select id::text from services")
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return []string{}, err
	}
	states, err := statesAt(ctx, tx, ids, s.now().Add(time.Minute), true)
	if err != nil {
		return nil, err
	}
	var svcs, vers []string
	for id, st := range states {
		if st.ProdVersion != "" {
			svcs, vers = append(svcs, id), append(vers, st.ProdVersion)
		}
	}
	if len(svcs) == 0 {
		return []string{}, nil
	}
	rows, err = tx.Query(ctx, `
		with cur as (select * from unnest($1::uuid[], $2::text[]) c(sid, v)),
		rep as (
			select distinct on (r.service_id, r.scanner) r.id from vuln_reports r join cur on cur.sid = r.service_id and lower(r.version) = lower(cur.v)
			order by r.service_id, r.scanner, r.scanned_at desc
		)
		select distinct f.id::text from rep join vuln_report_findings l on l.tenant_id = app_tenant() and l.report_id = rep.id
		join findings f on f.tenant_id = l.tenant_id and f.id = l.finding_id and f.severity = 'critical'
		  and f.status not in ('accepted', 'false_positive') and f.stage in `+openStages, svcs, vers)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

var critOrder = map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}

func criticalInProd(ctx context.Context, tx pgx.Tx, svcs, versions []string) ([]VulnRow, error) {
	if len(svcs) == 0 {
		return []VulnRow{}, nil
	}
	rows, err := tx.Query(ctx, `
		with cur as (select * from unnest($1::uuid[], $2::text[]) c(sid, v)),
		rep as (
			select distinct on (r.service_id, r.scanner) r.id, r.service_id, cur.v
			from vuln_reports r join cur on cur.sid = r.service_id and lower(r.version) = lower(cur.v)
			order by r.service_id, r.scanner, r.scanned_at desc
		)
		select distinct on (f.id) f.cve, f.severity, f.package, f.installed_version, f.fixed_in, f.layer, f.url, f.first_seen,
		       sv.id::text, sv.name, rep.v, sv.application_element_id::text, sp.key || '-' || ae.number, ae.title, a.criticality,
		       coalesce(a.supports_critical_function, false)
		from rep join vuln_report_findings l on l.tenant_id = app_tenant() and l.report_id = rep.id
		join findings f on f.tenant_id = l.tenant_id and f.id = l.finding_id and f.severity = 'critical' and f.status not in ('accepted', 'false_positive') and f.stage in `+openStages+`
		join services sv on sv.tenant_id = f.tenant_id and sv.id = rep.service_id
		left join elements ae on ae.tenant_id = sv.tenant_id and ae.id = sv.application_element_id
		left join spaces sp on sp.tenant_id = ae.tenant_id and sp.id = ae.space_id
		left join ext_application a on a.tenant_id = ae.tenant_id and a.element_id = ae.id
		order by f.id limit 2000`, svcs, versions)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[VulnRow])
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.CriticalFunction != b.CriticalFunction {
			return a.CriticalFunction
		}
		ca, cb := critOrder[deref(a.Criticality)], critOrder[deref(b.Criticality)]
		if ca != cb {
			return ca > cb
		}
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return a.CVE < b.CVE
	})
	if len(list) > 300 {
		list = list[:300]
	}
	return list, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
