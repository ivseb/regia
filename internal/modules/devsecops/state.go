package devsecops

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/contract"
)

// Stato dei servizi a un istante: versione in produzione, il suo rilascio, l'ultimo rilascio
// in produzione e le CVE dell'ultimo report (per scanner) di quella versione. Si calcola
// dalla storia, così lo stesso codice dà "oggi" e "sette giorni fa" senza fotografie.

// CVECounts sono le vulnerabilità di una versione.
type CVECounts struct {
	Critical        int        `json:"critical"`
	High            int        `json:"high"`
	Medium          int        `json:"medium"`
	Low             int        `json:"low"`
	FixableCritical int        `json:"fixable_critical"`
	FixableHigh     int        `json:"fixable_high"`
	Image           Pair       `json:"image"`
	App             Pair       `json:"app"`
	ScannedAt       *time.Time `json:"scanned_at,omitempty"`
	Scanners        []string   `json:"scanners,omitempty"`
	// Declared: numeri dichiarati dal rilascio (senza dettaglio per strato).
	Declared bool `json:"declared,omitempty"`
}

// Pair: critiche e alte di uno strato (immagine o applicazione).
type Pair struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
}

type state struct {
	ProdVersion     string
	ProdSince       *time.Time
	ReleaseID       string
	LastProdRelease *time.Time
	CVE             *CVECounts
	Checks          []contract.CheckResult
}

// statesAt calcola lo stato dei servizi all'istante at.
func statesAt(ctx context.Context, tx pgx.Tx, ids []string, at time.Time, current bool) (map[string]*state, error) {
	out := make(map[string]*state, len(ids))
	for _, id := range ids {
		out[id] = &state{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select distinct on (d.service_id) d.service_id::text, d.version, d.started_at, coalesce(d.release_id::text, rr.id::text, '')
		from deployments d
		join environments e on e.tenant_id = d.tenant_id and e.id = d.environment_id and e.is_production
		left join lateral (
			select r.id from releases r join environments re on re.tenant_id = r.tenant_id and re.id = r.environment_id and re.is_production
			where r.service_id = d.service_id and lower(r.version) = lower(d.version) and r.released_at <= $2
			order by r.released_at desc limit 1) rr on d.release_id is null
		where d.service_id = any($1) and d.status in ('succeeded', 'running') and d.started_at <= $2
		order by d.service_id, d.started_at desc`, ids, at)
	if err != nil {
		return nil, err
	}
	var sid, ver, rel string
	var since time.Time
	var relIDs, verSvc, verVer []string
	if _, err := pgx.ForEachRow(rows, []any{&sid, &ver, &since, &rel}, func() error {
		st := out[sid]
		t := since
		st.ProdVersion, st.ProdSince, st.ReleaseID = ver, &t, rel
		if rel != "" {
			relIDs = append(relIDs, rel)
		}
		verSvc, verVer = append(verSvc, sid), append(verVer, ver)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select r.service_id::text, max(r.released_at) from releases r
		join environments e on e.tenant_id = r.tenant_id and e.id = r.environment_id and e.is_production
		where r.service_id = any($1) and r.released_at <= $2 group by 1`, ids, at)
	if err != nil {
		return nil, err
	}
	var last time.Time
	if _, err := pgx.ForEachRow(rows, []any{&sid, &last}, func() error {
		t := last
		out[sid].LastProdRelease = &t
		return nil
	}); err != nil {
		return nil, err
	}
	if len(relIDs) > 0 {
		rows, err = tx.Query(ctx, "select service_id::text, checks from releases where id = any($1)", relIDs)
		if err != nil {
			return nil, err
		}
		var raw []byte
		if _, err := pgx.ForEachRow(rows, []any{&sid, &raw}, func() error {
			return json.Unmarshal(raw, &out[sid].Checks)
		}); err != nil {
			return nil, err
		}
	}
	counts, err := cveCounts(ctx, tx, verSvc, verVer, at, false, current)
	if err != nil {
		return nil, err
	}
	for id, c := range counts {
		out[id].CVE = c
	}
	return out, nil
}

// DEFINIZIONE UNICA di «CVE critiche (o alte) in produzione»: le vulnerabilità di quella
// gravità, ancora da correggere (non in eccezione, non falsi positivi, non chiuse), presenti
// nell'ultimo report di ogni scanner sulla VERSIONE in produzione di ogni servizio. Questa
// funzione è l'unica fonte per i fatti dei processi, il pannello DevSecOps, la scheda
// dell'applicazione, i gate e il KPI «in produzione» della pagina Vulnerabilità; l'elenco
// delle critiche (criticalInProd) usa gli stessi criteri. Una versione mai scansionata non ha
// riga nel risultato: la vista lo dice, non mostra «0».
//
// cveCounts conta le vulnerabilità delle versioni indicate (servizio i, versione i): l'ultimo
// report per scanner fino all'istante at (first = il primo report invece dell'ultimo, per le
// CVE "al rilascio" quando il rilascio non le dichiara). current = conta solo quelle ancora da
// correggere oggi (lo stadio è attuale: per le date passate non si può applicare).
func cveCounts(ctx context.Context, tx pgx.Tx, svcs, versions []string, at time.Time, first, current bool) (map[string]*CVECounts, error) {
	out := map[string]*CVECounts{}
	if len(svcs) == 0 {
		return out, nil
	}
	order := "r.scanned_at desc"
	if first {
		order = "r.scanned_at asc"
	}
	rows, err := tx.Query(ctx, `
		with cur as (select * from unnest($1::uuid[], $2::text[]) c(sid, v)),
		rep as (
			select distinct on (r.service_id, r.scanner) r.id, r.service_id, r.scanner, r.scanned_at
			from vuln_reports r join cur on cur.sid = r.service_id and lower(r.version) = lower(cur.v)
			where r.scanned_at <= $3 order by r.service_id, r.scanner, `+order+`
		),
		meta as (select service_id, max(scanned_at) at, array_agg(scanner order by scanner) scanners from rep group by 1),
		f as (
			select distinct rep.service_id, f.id, f.severity, coalesce(f.layer, 'app') layer, coalesce(f.fixed_in, '') <> '' fixable
			from rep join vuln_report_findings l on l.tenant_id = app_tenant() and l.report_id = rep.id
			join findings f on f.tenant_id = l.tenant_id and f.id = l.finding_id and f.status not in ('accepted', 'false_positive')
			  and (not $4::boolean or f.stage in `+openStages+`)
		)
		select m.service_id::text, m.at, m.scanners,
		       count(f.id) filter (where f.severity = 'critical'), count(f.id) filter (where f.severity = 'high'),
		       count(f.id) filter (where f.severity = 'medium'), count(f.id) filter (where f.severity = 'low'),
		       count(f.id) filter (where f.severity = 'critical' and f.fixable), count(f.id) filter (where f.severity = 'high' and f.fixable),
		       count(f.id) filter (where f.severity = 'critical' and f.layer = 'image'), count(f.id) filter (where f.severity = 'high' and f.layer = 'image'),
		       count(f.id) filter (where f.severity = 'critical' and f.layer = 'app'), count(f.id) filter (where f.severity = 'high' and f.layer = 'app')
		from meta m left join f on f.service_id = m.service_id group by 1, 2, 3`, svcs, versions, at, current)
	if err != nil {
		return nil, err
	}
	var sid string
	var c CVECounts
	var scanned time.Time
	_, err = pgx.ForEachRow(rows, []any{&sid, &scanned, &c.Scanners, &c.Critical, &c.High, &c.Medium, &c.Low, &c.FixableCritical, &c.FixableHigh,
		&c.Image.Critical, &c.Image.High, &c.App.Critical, &c.App.High}, func() error {
		x := c
		t := scanned
		x.ScannedAt = &t
		out[sid] = &x
		return nil
	})
	return out, err
}

// checksStatus riassume i controlli: failed (un controllo bloccante fallito), exception
// (bloccante fallito o saltato con un'eccezione), warn (un avviso fallito), ok, none.
func checksStatus(cs []contract.CheckResult) string {
	if len(cs) == 0 {
		return "none"
	}
	st := "ok"
	for _, c := range cs {
		switch {
		case c.Mode == "block" && c.Outcome == "failed" && c.Skip == nil:
			return "failed"
		case c.Mode == "block" && (c.Skip != nil || c.Outcome == "skipped" || c.Outcome == "not_run"):
			st = "exception"
		case c.Mode == "warn" && c.Outcome == "failed" && st == "ok":
			st = "warn"
		}
	}
	return st
}

func blocking(cs []contract.CheckResult) bool {
	s := checksStatus(cs)
	return s == "failed" || s == "exception"
}

// stale: l'ultimo rilascio in produzione è più vecchio di days giorni all'istante at.
func stale(st *state, at time.Time, days int) bool {
	return st.LastProdRelease != nil && at.Sub(*st.LastProdRelease) > time.Duration(days)*24*time.Hour
}

// ---------------------------------------------------------------------------
// Runtime

// RuntimeView è un runtime trovato nella SBOM della versione in produzione.
type RuntimeView struct {
	Name         string  `json:"name"`
	Version      string  `json:"version"`
	EndOfSupport *string `json:"end_of_support"`
	Unsupported  bool    `json:"unsupported"`
}

var runtimeAliases = map[string][]string{
	"node":   {"nodejs", "node.js"},
	"python": {"python3", "cpython"},
	"dotnet": {"dotnet-runtime", "microsoft.netcore.app", "aspnetcore-runtime"},
	"php":    {"php-cli", "php-fpm"},
	"java":   {"openjdk", "jre", "jdk"},
}

func runtimeOf(name string, known map[string]bool) string {
	n := strings.ToLower(name)
	if known[n] {
		return n
	}
	for rt, als := range runtimeAliases {
		if known[rt] && slices.Contains(als, n) {
			return rt
		}
	}
	return ""
}

// runtimes cerca i runtime nelle SBOM delle versioni (servizio i, versione i).
func runtimes(ctx context.Context, tx pgx.Tx, svcs, versions []string, st Settings, today time.Time) (map[string][]RuntimeView, error) {
	out := map[string][]RuntimeView{}
	if len(svcs) == 0 || len(st.Runtimes) == 0 {
		return out, nil
	}
	known := map[string]bool{}
	names := []string{}
	for _, r := range st.Runtimes {
		if !known[r.Name] {
			known[r.Name] = true
			names = append(names, r.Name)
			names = append(names, runtimeAliases[r.Name]...)
		}
	}
	rows, err := tx.Query(ctx, `
		with cur as (select * from unnest($1::uuid[], $2::text[]) c(sid, v))
		select distinct b.service_id::text, lower(c.name), coalesce(c.version, '')
		from sboms b join cur on cur.sid = b.service_id and lower(b.version) = lower(cur.v)
		join sbom_components sc on sc.tenant_id = b.tenant_id and sc.sbom_id = b.id
		join components c on c.tenant_id = sc.tenant_id and c.id = sc.component_id
		where lower(c.name) = any($3) order by 1, 2, 3`, svcs, versions, names)
	if err != nil {
		return nil, err
	}
	var sid, name, ver string
	_, err = pgx.ForEachRow(rows, []any{&sid, &name, &ver}, func() error {
		rt := runtimeOf(name, known)
		if rt == "" {
			return nil
		}
		v := RuntimeView{Name: rt, Version: ver}
		best := ""
		for _, r := range st.Runtimes {
			if r.Name == rt && (ver == r.Version || strings.HasPrefix(ver, r.Version+".")) && len(r.Version) > len(best) {
				best = r.Version
				eos := r.EndOfSupport
				v.EndOfSupport = &eos
				if d, err := time.Parse(time.DateOnly, eos); err == nil {
					v.Unsupported = !today.Before(d.AddDate(0, 0, 1))
				}
			}
		}
		for _, x := range out[sid] {
			if x.Name == v.Name && x.Version == v.Version {
				return nil
			}
		}
		out[sid] = append(out[sid], v)
		return nil
	})
	return out, err
}
