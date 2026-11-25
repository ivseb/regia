package fakegithub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Fixture inventate e anonime, nel formato che il preset "Template GitHub Actions + GitOps
// (Argo CD)" di Regia sa leggere: registro di rilascio, esiti dei controlli, SBOM
// CycloneDX, report grype, file degli ambienti GitOps.

// Record sono i dati del registro di rilascio.
type Record struct {
	Service, Version, Environment string
	Image, Digest, Commit         string
	Change, ChangeTitle           string
	RequestedBy, MergedBy         string
	MergedAt                      time.Time
	PullRequest                   string
	CriticalHighWithFix           int
}

// ReleaseRecord produce release-record.json.
func ReleaseRecord(r Record) []byte {
	if r.Environment == "" {
		r.Environment = "prod"
	}
	var pr any
	if r.MergedBy != "" {
		pr = map[string]any{"repository": "esempio/negozio-gitops", "number": 12, "url": r.PullRequest,
			"title": fmt.Sprintf("rilascio %s %s", r.Service, r.Version), "merged_by": r.MergedBy, "merged_at": r.MergedAt.UTC().Format(time.RFC3339)}
	}
	var change any
	if r.Change != "" {
		change = r.Change
	}
	doc := map[string]any{
		"service": r.Service, "version": r.Version, "environment": r.Environment, "image": r.Image, "digest": r.Digest,
		"commit": r.Commit, "repository": "esempio/" + r.Service, "change": change, "change_title": nilIfEmpty(r.ChangeTitle),
		"requested_by": nilIfEmpty(r.RequestedBy), "gitops_pull_request": pr,
		"cves_at_release":  map[string]any{"critical_high_with_fix": r.CriticalHighWithFix, "findings": []any{}},
		"finalized_at":     r.MergedAt.Add(20 * time.Minute).UTC().Format(time.RFC3339),
		"finalized_by_run": "https://github.example.test/esempio/" + r.Service + "/actions/runs/1",
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CIChecks produce ci-checks.json: il verdetto (nome → esito), i check run e Sonar.
func CIChecks(commit string, verdict map[string]string, sonarGate string) []byte {
	var runs []map[string]any
	names := make([]string, 0, len(verdict))
	for n := range verdict {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		concl := verdict[n]
		runs = append(runs, map[string]any{"name": "ci / " + strings.ToUpper(n[:1]) + n[1:], "status": "completed", "conclusion": concl,
			"url": "https://github.example.test/runs/" + n, "completed_at": "2026-10-01T09:00:00Z"})
	}
	var v any = verdict
	if verdict == nil {
		v = nil
	}
	var sonar any
	if sonarGate != "" {
		sonar = map[string]any{"project": "esempio", "quality_gate": sonarGate}
	}
	doc := map[string]any{"commit": commit, "collected_at": "2026-10-01T09:30:00Z", "ci_verdict": v, "check_runs": runs, "sonar": sonar}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// CycloneDX produce un SBOM minimo con i componenti indicati (nome@versione).
func CycloneDX(components ...string) []byte {
	var cs []map[string]any
	for _, c := range components {
		name, version, _ := strings.Cut(c, "@")
		cs = append(cs, map[string]any{"type": "library", "name": name, "version": version, "purl": "pkg:npm/" + name + "@" + version})
	}
	doc := map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1, "components": cs}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// Vuln è una corrispondenza del report grype.
type Vuln struct {
	ID, Package, Version, Type, Severity, Fix string
	Related                                   []string
}

// Grype produce un report grype con le corrispondenze indicate.
func Grype(vs ...Vuln) []byte {
	matches := []map[string]any{}
	for _, v := range vs {
		fix := map[string]any{"versions": []string{}, "state": "not-fixed"}
		if v.Fix != "" {
			fix = map[string]any{"versions": []string{v.Fix}, "state": "fixed"}
		}
		var rel []map[string]any
		for _, r := range v.Related {
			rel = append(rel, map[string]any{"id": r})
		}
		matches = append(matches, map[string]any{
			"vulnerability":          map[string]any{"id": v.ID, "severity": v.Severity, "fix": fix, "dataSource": "https://osv.example.test/" + v.ID},
			"relatedVulnerabilities": rel,
			"artifact":               map[string]any{"name": v.Package, "version": v.Version, "type": v.Type, "purl": "pkg:" + v.Type + "/" + v.Package + "@" + v.Version},
		})
	}
	doc := map[string]any{"matches": matches, "descriptor": map[string]any{"name": "grype", "timestamp": "2026-10-01T09:10:00Z"}}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// EnvEntry è un servizio in un file di un ambiente GitOps.
type EnvEntry struct {
	Image, Tag, Version, Commit string
}

// EnvFile produce il file YAML di un ambiente con i servizi indicati.
func EnvFile(services map[string]EnvEntry) []byte {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Ambiente di esempio: i valori li aggiorna la pipeline.\nservices:\n")
	for _, n := range names {
		e := services[n]
		fmt.Fprintf(&b, "  %s:\n    image:\n      repository: %s\n      tag: %q\n    replicas: 2\n", n, e.Image, e.Tag)
		if e.Version != "" || e.Commit != "" {
			b.WriteString("    annotations:\n")
			if e.Version != "" {
				fmt.Fprintf(&b, "      ci/version: %q\n", e.Version)
			}
			if e.Commit != "" {
				fmt.Fprintf(&b, "      ci/git-commit: %q\n", e.Commit)
			}
		}
	}
	return []byte(b.String())
}

// Demo popola il finto GitHub con un'organizzazione di esempio (sviluppo e prove a mano).
func Demo(s *Server, now time.Time) {
	s.AddOwner("esempio", "Organization")
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }

	ordini := s.AddRepo("esempio", "ordini-api")
	ordini.Description = "API degli ordini"
	c1 := ordini.Branches["main"]
	s.AddRelease("esempio/ordini-api", "v1.3.0", "dev-uno", day(40), map[string][]byte{
		"release-record.json": ReleaseRecord(Record{Service: "ordini-api", Version: "1.3.0", Image: "registry.example.test/ordini-api:1.3.0",
			Digest: "sha256:" + strings.Repeat("a", 64), Commit: c1, Change: "CHG-1001", ChangeTitle: "Rilascio di settembre",
			RequestedBy: "dev-uno", MergedBy: "approvatrice", MergedAt: day(40), PullRequest: "https://github.example.test/esempio/negozio-gitops/pull/10"}),
		"ci-checks.json": CIChecks(c1, map[string]string{"lint": "success", "test": "success", "secrets": "success", "dependencies": "success"}, "OK"),
		"sbom.cdx.json":  CycloneDX("express@4.19.2", "lodash@4.17.21", "pino@8.17.0"),
		"grype-report.json": Grype(
			Vuln{ID: "CVE-2026-1111", Package: "express", Version: "4.19.2", Type: "npm", Severity: "High", Fix: "4.21.0"},
		),
	})
	c2 := s.Push("esempio/ordini-api", "main", nil)
	s.AddRelease("esempio/ordini-api", "v1.4.0", "dev-due", day(6), map[string][]byte{
		"release-record.json": ReleaseRecord(Record{Service: "ordini-api", Version: "1.4.0", Image: "registry.example.test/ordini-api:1.4.0",
			Digest: "sha256:" + strings.Repeat("b", 64), Commit: c2, Change: "CHG-1042", ChangeTitle: "Pagamenti in tre rate",
			RequestedBy: "dev-due", MergedBy: "approvatrice", MergedAt: day(6), PullRequest: "https://github.example.test/esempio/negozio-gitops/pull/14"}),
		"ci-checks.json": CIChecks(c2, map[string]string{"lint": "failure", "test": "success", "secrets": "success", "dependencies": "success"}, "OK"),
		"sbom.cdx.json":  CycloneDX("express@4.21.0", "lodash@4.17.21", "pino@9.0.0", "zod@3.23.8"),
		"grype-report.json": Grype(
			Vuln{ID: "CVE-2026-2222", Package: "openssl", Version: "3.0.13", Type: "deb", Severity: "Critical", Fix: "3.0.14"},
			Vuln{ID: "GHSA-aaaa-bbbb-cccc", Package: "zod", Version: "3.23.8", Type: "npm", Severity: "Medium", Related: []string{"CVE-2026-3333"}},
		),
	})
	s.Push("esempio/ordini-api", "main", []CheckRun{
		{Name: "ci / Lint and test", Status: "completed", Conclusion: "success", CompletedAt: day(1)},
		{Name: "ci / Secrets and dependencies", Status: "completed", Conclusion: "success", CompletedAt: day(1)},
		{Name: "ci / Image", Status: "completed", Conclusion: "success", CompletedAt: day(1)},
		{Name: "ci / Verdict", Status: "completed", Conclusion: "success", CompletedAt: day(1)},
	})

	pag := s.AddRepo("esempio", "pagamenti-api")
	pag.Description = "Servizio dei pagamenti"
	s.AddRelease("esempio/pagamenti-api", "v2.1.0", "dev-tre", day(12), nil)
	sha := s.Push("esempio/pagamenti-api", "main", []CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success", CompletedAt: day(2)},
		{Name: "test", Status: "completed", Conclusion: "failure", CompletedAt: day(2)},
	})
	s.AddDeployment("esempio/pagamenti-api", "staging", "v2.1.0", sha, map[string]any{"version": "2.1.0"}, "success", day(11))
	s.AddDeployment("esempio/pagamenti-api", "production", "v2.1.0", sha, map[string]any{"version": "2.1.0"}, "success", day(10))

	s.AddRepo("esempio", "catalogo-web").Description = "Vetrina del catalogo"
	s.AddRepo("esempio", "strumenti-interni").Description = "Script del team"

	g := s.AddRepo("esempio", "negozio-gitops")
	g.Description = "Stato degli ambienti del negozio"
	s.SetFile("esempio/negozio-gitops", "main", "envs/prod/negozio.yaml", EnvFile(map[string]EnvEntry{
		"ordini-api":    {Image: "registry.example.test/ordini-api", Tag: "1.4.0", Version: "1.4.0", Commit: c2},
		"pagamenti-api": {Image: "registry.example.test/pagamenti-api", Tag: "2.1.0", Version: "2.1.0"},
		"vecchio-batch": {Image: "registry.example.test/vecchio-batch", Tag: "0.9.1"},
	}))
	s.SetFile("esempio/negozio-gitops", "main", "envs/staging/negozio.yaml", EnvFile(map[string]EnvEntry{
		"ordini-api": {Image: "registry.example.test/ordini-api", Tag: "1.5.0-3-abcdef12", Commit: "abcdef12"},
	}))
	s.SetFile("esempio/negozio-gitops", "main", "README.md", []byte("# Stato degli ambienti\n"))
}
