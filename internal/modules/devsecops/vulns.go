package devsecops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Vulnerabilità: una per servizio, vulnerabilità e componente (fingerprint), qualunque sia lo
// scanner o la scansione che la trova. Ogni report elenca i finding trovati, così si sa cosa
// c'era in una versione a un certo istante (al rilascio, oggi, sette giorni fa).

// MaxVulnerabilities è il limite di vulnerabilità per report.
const MaxVulnerabilities = 20000

var severities = map[string]string{"critical": "critical", "high": "high", "important": "high", "medium": "medium", "moderate": "medium",
	"low": "low", "negligible": "low", "minor": "low", "info": "info", "informational": "info", "unknown": "info", "none": "info"}

var sevRank = map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1, "info": 0}

// osPURL: tipi di package URL dei pacchetti del sistema operativo (strato immagine).
var osPURL = map[string]bool{"deb": true, "apk": true, "rpm": true, "alpm": true, "alpine": true, "debian": true, "ubuntu": true}

type vuln struct {
	ID, Package, PURL, Version, FixedIn, Severity, Layer, URL string
	Aliases                                                   []string
	fp, componentID                                           string
}

func normVuln(v contract.Vulnerability) (vuln, bool) {
	id := strings.ToUpper(clip(v.ID, 100))
	als := []string{}
	for _, a := range v.Aliases {
		if a = strings.ToUpper(clip(a, 100)); a != "" && a != id && !slices.Contains(als, a) && len(als) < 20 {
			als = append(als, a)
		}
	}
	// Si preferisce l'identificativo CVE: lo stesso problema da GHSA e da CVE vale una volta.
	if !strings.HasPrefix(id, "CVE-") {
		for i, a := range als {
			if strings.HasPrefix(a, "CVE-") {
				als[i], id = id, a
				break
			}
		}
	}
	if id == "" {
		return vuln{}, false
	}
	out := vuln{ID: id, Aliases: als, Package: clip(v.Package, 300), PURL: clip(v.PURL, 1000), Version: clip(v.Version, 120),
		FixedIn: clip(v.FixedIn, 200), URL: cleanURL(v.URL)}
	sev, ok := severities[strings.ToLower(strings.TrimSpace(v.Severity))]
	if !ok {
		sev = "info"
	}
	out.Severity = sev
	if out.Package == "" && out.PURL != "" {
		out.Package = clip(purlName(out.PURL), 300)
	}
	if out.Package == "" {
		return vuln{}, false
	}
	switch l := strings.ToLower(strings.TrimSpace(v.Layer)); l {
	case "image", "app":
		out.Layer = l
	default:
		out.Layer = "app"
		if osPURL[purlType(out.PURL)] {
			out.Layer = "image"
		}
	}
	return out, true
}

// IngestVulnReport salva un report: i finding deduplicati, il report con i conteggi e i
// collegamenti; poi allinea lo stato dei finding alla versione in produzione.
func (s *Service) IngestVulnReport(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.VulnReport) error {
	_, err := s.ingestVulnReport(ctx, tx, actor, in)
	return err
}

func (s *Service) ingestVulnReport(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.VulnReport) (*Receipt, error) {
	if len(in.Vulnerabilities) > MaxVulnerabilities {
		return nil, engine.InvalidParams("validation.devsecops.vulns_too_many", "vulnerabilities", map[string]any{"max": MaxVulnerabilities}, "troppe vulnerabilità in un report (massimo %d)", MaxVulnerabilities)
	}
	scanner := strings.ToLower(clip(in.Scanner, 60))
	if scanner == "" {
		scanner = "other"
	}
	version, digest := clip(in.Version, 200), clip(in.Digest, 200)
	var err error
	if version == "" {
		if version, err = versionOfDigest(ctx, tx, in.Service, digest); err != nil {
			return nil, err
		}
	}
	if version == "" {
		return nil, engine.Required("version", "serve la versione (o il digest di un artefatto già rilasciato)")
	}
	ref, err := s.ensureService(ctx, tx, actor, in.Service, "pipeline", true)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/vuln/"+ref.ID); err != nil {
		return nil, err
	}
	at := in.ScannedAt
	if at.IsZero() {
		at = s.now()
	}
	at = at.UTC().Truncate(time.Microsecond)
	rc := &Receipt{Kind: "vuln_report", Service: ref.Name, ServiceID: ref.ID, Application: ref.AppID}
	var existing string
	err = tx.QueryRow(ctx, `select id::text from vuln_reports where service_id = $1 and lower(version) = lower($2) and digest = $3 and scanner = $4 and scanned_at = $5`,
		ref.ID, version, digest, scanner, at).Scan(&existing)
	if err == nil {
		rc.ID, rc.Summary = existing, fmt.Sprintf("report %s di %s %s già ricevuto", scanner, ref.Name, version)
		return rc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Deduplica nel report: stessa vulnerabilità sullo stesso componente una volta, con la
	// severità più alta.
	byFP := map[string]int{}
	var vs []vuln
	skipped := 0
	for _, raw := range in.Vulnerabilities {
		v, ok := normVuln(raw)
		if !ok {
			skipped++
			continue
		}
		if v.PURL == "" {
			v.PURL = genericPURL(v.Package, v.Version)
		}
		h := sha256.Sum256([]byte(ref.ID + "|" + v.ID + "|" + v.PURL))
		v.fp = hex.EncodeToString(h[:])
		if i, ok := byFP[v.fp]; ok {
			if sevRank[v.Severity] > sevRank[vs[i].Severity] {
				vs[i].Severity = v.Severity
			}
			if vs[i].FixedIn == "" {
				vs[i].FixedIn = v.FixedIn
			}
			continue
		}
		byFP[v.fp] = len(vs)
		vs = append(vs, v)
	}
	if skipped > 0 {
		rc.Warnings = append(rc.Warnings, fmt.Sprintf("%d vulnerabilità senza identificativo o pacchetto ignorate", skipped))
	}
	cs := make([]comp, 0, len(vs))
	for _, v := range vs {
		cs = append(cs, comp{PURL: v.PURL, Name: v.Package, Version: v.Version})
	}
	compIDs, err := upsertComponents(ctx, tx, cleanComps(cs))
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	layer := map[string]map[string]int{"image": {}, "app": {}}
	findingIDs := make([]string, 0, len(vs))
	for start := 0; start < len(vs); start += 2000 {
		end := min(start+2000, len(vs))
		n := end - start
		fps, ids, sevs, cves, comps, pkgs, vers, fixes, layers, urls := make([]string, n), make([]string, n), make([]string, n), make([]string, n),
			make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		aliases := make([]string, n)
		for i, v := range vs[start:end] {
			fps[i], ids[i], sevs[i], cves[i], comps[i] = v.fp, db.NewID(), v.Severity, v.ID, compIDs[v.PURL]
			pkgs[i], vers[i], fixes[i], layers[i], urls[i] = v.Package, v.Version, v.FixedIn, v.Layer, v.URL
			raw, _ := json.Marshal(v.Aliases)
			aliases[i] = string(raw)
		}
		rows, err := tx.Query(ctx, `insert into findings (tenant_id, id, fingerprint, source, rule_ref, cve, severity, application_element_id, component_id,
			status, first_seen, last_seen, service_id, package, installed_version, fixed_in, layer, aliases, url)
			select app_tenant(), t.id::uuid, t.fp, $1, t.cve, t.cve, t.sev, $2::uuid, nullif(t.comp, '')::uuid, 'open', $3, $3, $4::uuid, t.pkg,
			       nullif(t.ver, ''), nullif(t.fix, ''), t.layer, array(select jsonb_array_elements_text(t.al::jsonb)), nullif(t.url, '')
			from unnest($5::text[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[], $11::text[], $12::text[], $13::text[], $14::text[], $15::text[])
			  as t(fp, id, sev, cve, comp, pkg, ver, fix, layer, url, al)
			on conflict (tenant_id, fingerprint) do update set severity = excluded.severity, fixed_in = coalesce(excluded.fixed_in, findings.fixed_in),
			  last_seen = greatest(findings.last_seen, excluded.last_seen), first_seen = least(findings.first_seen, excluded.first_seen),
			  aliases = excluded.aliases, url = coalesce(excluded.url, findings.url), application_element_id = excluded.application_element_id
			returning id::text`,
			scanner, nullable(ref.AppID), at, ref.ID, fps, ids, sevs, cves, comps, pkgs, vers, fixes, layers, urls, aliases)
		if err != nil {
			return nil, err
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		findingIDs = append(findingIDs, got...)
	}
	for _, v := range vs {
		counts[sevKey(v.Severity)]++
		if v.FixedIn != "" && (v.Severity == "critical" || v.Severity == "high") {
			counts["fixable_"+v.Severity]++
		}
		if v.Severity == "critical" || v.Severity == "high" {
			layer[v.Layer][v.Severity]++
		}
	}
	rawCounts, _ := json.Marshal(map[string]any{"critical": counts["critical"], "high": counts["high"], "medium": counts["medium"],
		"low": counts["low"], "unknown": counts["unknown"], "fixable_critical": counts["fixable_critical"], "fixable_high": counts["fixable_high"],
		"image": layer["image"], "app": layer["app"]})
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into vuln_reports (tenant_id, id, service_id, version, digest, scanner, scanned_at, counts, total)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, id, ref.ID, version, digest, scanner, at, rawCounts, len(vs)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into vuln_report_findings (tenant_id, report_id, finding_id)
		select app_tenant(), $1, f from unnest($2::uuid[]) f on conflict do nothing`, id, findingIDs); err != nil {
		return nil, err
	}
	if err := s.refreshFindings(ctx, tx, ref.ID); err != nil {
		return nil, err
	}
	rc.ID, rc.Created, rc.Changed = id, true, true
	rc.Counts = map[string]int{"critical": counts["critical"], "high": counts["high"], "medium": counts["medium"], "low": counts["low"],
		"fixable_critical": counts["fixable_critical"], "fixable_high": counts["fixable_high"], "total": len(vs)}
	rc.Summary = fmt.Sprintf("report %s di %s %s: %d vulnerabilità (%d critiche, %d alte)", scanner, ref.Name, version, len(vs), counts["critical"], counts["high"])
	return rc, auditEvent(ctx, tx, actor, "devsecops.vuln_report", "vuln_report", id, map[string]any{"service": ref.ID, "scanner": scanner,
		"total": len(vs), "critical": counts["critical"], "high": counts["high"]})
}

func sevKey(s string) string {
	if s == "info" {
		return "unknown"
	}
	return s
}

// ---------------------------------------------------------------------------
// Report grezzi degli scanner: l'API di push accetta anche l'output JSON di grype e di trivy
// così com'è, senza conversioni nella pipeline.

// ParseScannerReport traduce il JSON di grype ("matches") o di trivy ("Results") nelle
// vulnerabilità del contratto.
func ParseScannerReport(scanner string, raw []byte) ([]contract.Vulnerability, string, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, "", fmt.Errorf("report non valido: %w", err)
	}
	switch {
	case probe["matches"] != nil || scanner == "grype":
		v, err := parseGrype(raw)
		return v, "grype", err
	case probe["Results"] != nil || scanner == "trivy":
		v, err := parseTrivy(raw)
		return v, "trivy", err
	}
	return nil, "", errors.New("formato del report non riconosciuto (grype o trivy in JSON)")
}

func parseGrype(raw []byte) ([]contract.Vulnerability, error) {
	var r struct {
		Matches []struct {
			Vulnerability struct {
				ID         string `json:"id"`
				Severity   string `json:"severity"`
				DataSource string `json:"dataSource"`
				Fix        struct {
					Versions []string `json:"versions"`
					State    string   `json:"state"`
				} `json:"fix"`
			} `json:"vulnerability"`
			Related []struct {
				ID string `json:"id"`
			} `json:"relatedVulnerabilities"`
			Artifact struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Type    string `json:"type"`
				PURL    string `json:"purl"`
			} `json:"artifact"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("report grype non valido: %w", err)
	}
	out := make([]contract.Vulnerability, 0, len(r.Matches))
	for _, m := range r.Matches {
		v := contract.Vulnerability{ID: m.Vulnerability.ID, Package: m.Artifact.Name, PURL: m.Artifact.PURL, Version: m.Artifact.Version,
			Severity: m.Vulnerability.Severity, URL: m.Vulnerability.DataSource}
		for _, rel := range m.Related {
			v.Aliases = append(v.Aliases, rel.ID)
		}
		if m.Vulnerability.Fix.State == "fixed" && len(m.Vulnerability.Fix.Versions) > 0 {
			v.FixedIn = strings.Join(m.Vulnerability.Fix.Versions, ", ")
		}
		switch m.Artifact.Type {
		case "deb", "apk", "rpm", "alpm", "linux-kernel":
			v.Layer = "image"
		default:
			v.Layer = "app"
		}
		out = append(out, v)
	}
	return out, nil
}

func parseTrivy(raw []byte) ([]contract.Vulnerability, error) {
	var r struct {
		Results []struct {
			Class           string `json:"Class"`
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Severity         string `json:"Severity"`
				PrimaryURL       string `json:"PrimaryURL"`
				PkgIdentifier    struct {
					PURL string `json:"PURL"`
				} `json:"PkgIdentifier"`
				VendorIDs []string `json:"VendorIDs"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("report trivy non valido: %w", err)
	}
	var out []contract.Vulnerability
	for _, res := range r.Results {
		layer := "app"
		if res.Class == "os-pkgs" {
			layer = "image"
		}
		for _, x := range res.Vulnerabilities {
			out = append(out, contract.Vulnerability{ID: x.VulnerabilityID, Aliases: x.VendorIDs, Package: x.PkgName, PURL: x.PkgIdentifier.PURL,
				Version: x.InstalledVersion, FixedIn: x.FixedVersion, Severity: x.Severity, Layer: layer, URL: x.PrimaryURL})
		}
	}
	return out, nil
}
