package fakegitlab

import (
	"encoding/json"
	"strings"
	"time"

	"regia/internal/devtools/fakegithub"
)

// GLVuln è una vulnerabilità del report di sicurezza di GitLab.
type GLVuln struct {
	ID, Package, Version, Severity, Solution string
	Image                                    string // vuoto = dipendenza del codice (Dependency Scanning)
	Aliases                                  []string
}

// SecurityReport produce un report nel formato di GitLab (gl-dependency-scanning-report.json
// o gl-container-scanning-report.json), inventato.
func SecurityReport(scanner string, vs ...GLVuln) []byte {
	var list []map[string]any
	for _, v := range vs {
		ids := []map[string]any{{"type": "cve", "name": v.ID, "value": v.ID, "url": "https://osv.example.test/" + v.ID}}
		for _, a := range v.Aliases {
			ids = append(ids, map[string]any{"type": "ghsa", "name": a, "value": a})
		}
		loc := map[string]any{"dependency": map[string]any{"package": map[string]any{"name": v.Package}, "version": v.Version}}
		if v.Image != "" {
			loc["image"] = v.Image
			loc["operating_system"] = "debian 12"
		}
		list = append(list, map[string]any{"id": "uuid-" + v.ID, "name": v.ID + " in " + v.Package, "severity": v.Severity, "solution": v.Solution,
			"identifiers": ids, "location": loc})
	}
	doc := map[string]any{"version": "15.0.7", "scan": map[string]any{"scanner": map[string]any{"id": scanner, "name": strings.ToUpper(scanner[:1]) + scanner[1:]},
		"type": "dependency_scanning", "status": "success", "start_time": "2026-10-01T09:00:00", "end_time": "2026-10-01T09:10:00"}, "vulnerabilities": list}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// Demo popola il finto GitLab con un gruppo di esempio (sviluppo e prove a mano): gli stessi
// servizi del finto GitHub, in un sottogruppo, con gli artefatti nei job del tag (v1.3.0) e
// nei link della Release (v1.4.0).
func Demo(s *Server, now time.Time) {
	s.AddGroup("esempio")
	s.AddGroup("esempio/negozio")
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }

	ordini := s.AddProject("esempio/negozio/ordini-api")
	ordini.Description = "API degli ordini"
	// v1.3.0: gli allegati sono artefatti dei job della pipeline del tag.
	s.AddRelease("esempio/negozio/ordini-api", "v1.3.0", "dev-uno", day(40), nil)
	s.AddPipeline("esempio/negozio/ordini-api", "v1.3.0", "success", day(40),
		JobSpec{Name: "build", Stage: "build", Status: "success"},
		JobSpec{Name: "dependency_scanning", Stage: "test", Status: "success", Artifacts: map[string][]byte{
			"gl-sbom-npm-npm.cdx.json": fakegithub.CycloneDX("express@4.19.2", "lodash@4.17.21", "pino@8.17.0"),
			"gl-dependency-scanning-report.json": SecurityReport("trivy",
				GLVuln{ID: "CVE-2026-1111", Package: "express", Version: "4.19.2", Severity: "High", Solution: "Upgrade to version 4.21.0"}),
		}})
	// v1.4.0: gli allegati sono link della Release (pacchetti generici).
	s.AddRelease("esempio/negozio/ordini-api", "v1.4.0", "dev-due", day(6), map[string][]byte{
		"gl-sbom-npm-npm.cdx.json": fakegithub.CycloneDX("express@4.21.0", "lodash@4.17.21", "pino@9.0.0", "zod@3.23.8"),
		"gl-container-scanning-report.json": SecurityReport("trivy",
			GLVuln{ID: "CVE-2026-2222", Package: "openssl", Version: "3.0.13", Severity: "Critical", Solution: "Upgrade openssl to 3.0.14", Image: "registry.example.test/ordini-api:1.4.0"},
			GLVuln{ID: "CVE-2026-3333", Package: "zod", Version: "3.23.8", Severity: "Medium", Aliases: []string{"GHSA-aaaa-bbbb-cccc"}}),
	})
	s.AddPipeline("esempio/negozio/ordini-api", "main", "success", day(1),
		JobSpec{Name: "lint", Stage: "test", Status: "success"},
		JobSpec{Name: "test", Stage: "test", Status: "success"},
		JobSpec{Name: "secret_detection", Stage: "test", Status: "success"},
		JobSpec{Name: "container_scanning", Stage: "test", Status: "failed", AllowFailure: true},
		JobSpec{Name: "deploy_production", Stage: "deploy", Status: "manual"})
	s.AddDeployment("esempio/negozio/ordini-api", "production", "v1.3.0", randSHA(1), true, "success", day(38))
	s.AddDeployment("esempio/negozio/ordini-api", "production", "v1.4.0", randSHA(2), true, "success", day(5))
	s.AddDeployment("esempio/negozio/ordini-api", "staging", "main", randSHA(3), false, "success", day(1))

	pag := s.AddProject("esempio/negozio/pagamenti-api")
	pag.Description = "Servizio dei pagamenti"
	s.AddRelease("esempio/negozio/pagamenti-api", "v2.1.0", "dev-tre", day(12), nil)
	s.AddPipeline("esempio/negozio/pagamenti-api", "main", "failed", day(2),
		JobSpec{Name: "build", Stage: "build", Status: "success"},
		JobSpec{Name: "test", Stage: "test", Status: "failed"})
	s.AddDeployment("esempio/negozio/pagamenti-api", "staging", "v2.1.0", randSHA(4), true, "success", day(11))
	s.AddDeployment("esempio/negozio/pagamenti-api", "production", "v2.1.0", randSHA(5), true, "success", day(10))
	s.AddEnvironment("esempio/negozio/pagamenti-api", "review")

	s.AddProject("esempio/catalogo-web").Description = "Vetrina del catalogo"
	s.AddProject("esempio/negozio/strumenti-interni").Description = "Script del team"
}
