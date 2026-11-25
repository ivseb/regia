package profile_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"regia/internal/devtools/fakegithub"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
)

func TestPathLookup(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"release":{"digest":"sha256:abc","n":3},"list":[{"name":"a"},{"name":"b"}],
		"annotations":{"ci/version":"1.2.3","a.b":"punto"},"vuoto":"","zero":0}`), &doc)
	cases := map[string]string{
		"release.digest":            "sha256:abc",
		"release.n":                 "3",
		"list[1].name":              "b",
		`annotations["ci/version"]`: "1.2.3",
		`annotations['a.b']`:        "punto",
		"=prod":                     "prod",
		"manca.del.tutto":           "",
		"zero":                      "0",
	}
	for p, want := range cases {
		if got := (profile.Path{p}).Text(doc); got != want {
			t.Errorf("%s = %q, atteso %q", p, got, want)
		}
	}
	// La prima alternativa non vuota vince.
	if got := (profile.Path{"vuoto", "manca", "release.digest"}).Text(doc); got != "sha256:abc" {
		t.Errorf("alternative: %q", got)
	}
	for _, bad := range []string{"a..b", ".a", "a.", "a[x]", "a[", "a]b[0]c"} {
		if err := (profile.Path{bad}).Check(); err == nil {
			t.Errorf("percorso %q accettato", bad)
		}
	}
	var p profile.Path
	if err := json.Unmarshal([]byte(`["a","b"]`), &p); err != nil || len(p) != 2 {
		t.Fatalf("elenco di alternative: %v %v", p, err)
	}
	if err := json.Unmarshal([]byte(`"a.b"`), &p); err != nil || len(p) != 1 {
		t.Fatalf("stringa: %v %v", p, err)
	}
}

func presetDef(t *testing.T) *profile.Definition {
	t.Helper()
	p := profile.PresetByKey(profile.PresetGitOpsArgo)
	if p == nil {
		t.Fatal("preset mancante")
	}
	if ps := p.Definition.Validate(); len(ps) > 0 {
		t.Fatalf("il preset non è valido: %v", ps)
	}
	// Il preset deve sopravvivere a export e import (JSON canonico).
	def, err := profile.Parse(p.Definition.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestPresetsAreValid(t *testing.T) {
	for _, p := range profile.Presets() {
		if ps := p.Definition.Validate(); len(ps) > 0 {
			t.Errorf("preset %s: %v", p.Key, ps)
		}
		if p.Name == "" || p.Description == "" {
			t.Errorf("preset %s senza nome o descrizione", p.Key)
		}
	}
}

func TestExtractReleaseWithPreset(t *testing.T) {
	def := presetDef(t)
	merged := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	commit := strings.Repeat("c", 40)
	in := profile.ReleaseInput{
		Service: contract.Service{Name: "ordini-api", Repository: "esempio/ordini-api", Application: "ORD"},
		Tag:     "v1.4.0", Author: "bot-rilasci", URL: "https://github.example.test/r/1", PublishedAt: merged.Add(time.Hour),
		Assets: map[string][]byte{
			"release-record.json": fakegithub.ReleaseRecord(fakegithub.Record{Service: "ordini-api", Version: "1.4.0", Image: "reg/ordini-api:1.4.0",
				Digest: "sha256:" + strings.Repeat("b", 64), Commit: commit, Change: "CHG-1", ChangeTitle: "Rate", RequestedBy: "dev-due",
				MergedBy: "approvatrice", MergedAt: merged, PullRequest: "https://github.example.test/pr/14"}),
			"ci-checks.json": fakegithub.CIChecks(commit, map[string]string{"lint": "failure", "test": "success", "secrets": ""}, "ERROR"),
			"sbom.cdx.json":  fakegithub.CycloneDX("express@4.21.0", "zod@3.23.8"),
			"grype-report.json": fakegithub.Grype(
				fakegithub.Vuln{ID: "CVE-2026-2222", Package: "openssl", Version: "3.0.13", Type: "deb", Severity: "Critical", Fix: "3.0.14"},
				fakegithub.Vuln{ID: "GHSA-aaaa", Package: "zod", Version: "3.23.8", Type: "npm", Severity: "High", Related: []string{"CVE-2026-3333"}},
				fakegithub.Vuln{ID: "CVE-2026-2222", Package: "openssl", Version: "3.0.13", Type: "deb", Severity: "Critical", Fix: "3.0.14"},
			),
		},
	}
	out := def.ExtractRelease(in, "")
	r := out.Release
	if r.Version != "1.4.0" || r.Commit != commit || r.Image != "reg/ordini-api:1.4.0" || !strings.HasPrefix(r.Digest, "sha256:b") ||
		r.Environment != "prod" || r.ChangeNumber != "CHG-1" || r.ChangeTitle != "Rate" || r.RequestedBy != "dev-due" ||
		r.ApprovedBy != "approvatrice" || r.ApprovedAt == nil || !r.ApprovedAt.Equal(merged) || r.PullRequest != "https://github.example.test/pr/14" ||
		!r.At.Equal(merged) || r.ReleaseURL != in.URL {
		t.Fatalf("registro non tradotto: %+v", r)
	}
	got := map[string]string{}
	for _, c := range r.Checks {
		got[c.Name] = c.Outcome
	}
	if got["lint"] != "failed" || got["test"] != "passed" || got["secrets"] != "not_run" || got["sonar"] != "failed" || len(got) != 4 {
		t.Fatalf("esiti: %v (i check run non vanno aggiunti quando c'è il verdetto)", got)
	}
	if out.SBOM == nil || out.SBOM.Format != "cyclonedx" || out.SBOM.Version != "1.4.0" || profile.SBOMComponents(out.SBOM.Document) != 2 {
		t.Fatalf("SBOM: %+v", out.SBOM)
	}
	if out.Vulns == nil || out.Vulns.Scanner != "grype" || len(out.Vulns.Vulnerabilities) != 2 {
		t.Fatalf("vulnerabilità (deduplicate): %+v", out.Vulns)
	}
	var ghsa contract.Vulnerability
	for _, v := range out.Vulns.Vulnerabilities {
		if v.Package == "zod" {
			ghsa = v
		}
	}
	if ghsa.ID != "CVE-2026-3333" || len(ghsa.Aliases) != 1 || ghsa.Aliases[0] != "GHSA-aaaa" || ghsa.Layer != "app" || ghsa.FixedIn != "" {
		t.Fatalf("GHSA con CVE equivalente: %+v", ghsa)
	}
	if s := r.CVEsAtRelease; s == nil || s.Critical != 1 || s.FixableCritical != 1 || s.High != 1 || s.FixableHigh != 0 {
		t.Fatalf("riassunto delle CVE al rilascio: %+v", s)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("avvisi inattesi: %v", out.Warnings)
	}

	// Verdetto scaduto: si ripiega sui check run del file.
	in.Assets["ci-checks.json"] = fakegithub.CIChecks(commit, nil, "")
	in.Assets["ci-checks.json"] = []byte(`{"commit":"x","ci_verdict":null,"check_runs":[{"name":"ci / Test","conclusion":"success","url":"u"}],"sonar":null}`)
	out = def.ExtractRelease(in, "")
	if len(out.Release.Checks) != 1 || out.Release.Checks[0].Name != "ci / Test" || out.Release.Checks[0].Outcome != "passed" {
		t.Fatalf("ripiego sui check run: %+v", out.Release.Checks)
	}

	// Allegati mancanti: avvisi, ma la Release c'è con i dati base.
	in.Assets = map[string][]byte{}
	out = def.ExtractRelease(in, "")
	if out.Release.Version != "1.4.0" || out.Release.RequestedBy != "bot-rilasci" || len(out.Warnings) != 4 || out.SBOM != nil {
		t.Fatalf("senza allegati: %+v %v", out.Release, out.Warnings)
	}
}

func TestExtractReleaseWithoutProfile(t *testing.T) {
	var def *profile.Definition
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := def.ExtractRelease(profile.ReleaseInput{Service: contract.Service{Name: "x"}, Tag: "v2.0.1", Author: "anna", PublishedAt: at}, "")
	if out.Release.Version != "2.0.1" || out.Release.Environment != "prod" || out.Release.RequestedBy != "anna" || !out.Release.At.Equal(at) {
		t.Fatalf("dati base: %+v", out.Release)
	}
	if !def.AcceptsTag("qualunque", false) || def.AcceptsTag("x", true) {
		t.Fatal("senza profilo: tutte le Release tranne le pre-release")
	}
}

func TestTrivyAndContractReports(t *testing.T) {
	trivy := []byte(`{"SchemaVersion":2,"CreatedAt":"2026-10-02T10:00:00Z","Results":[
	  {"Class":"os-pkgs","Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"libc","InstalledVersion":"1","FixedVersion":"2, 3","Severity":"HIGH"}]},
	  {"Class":"lang-pkgs","Vulnerabilities":[{"VulnerabilityID":"CVE-2","PkgName":"left-pad","InstalledVersion":"1","Severity":"LOW","PkgIdentifier":{"PURL":"pkg:npm/left-pad@1"}}]}]}`)
	rep, err := profile.ParseVulnReport(trivy, "auto")
	if err != nil || rep.Scanner != "trivy" || len(rep.Vulnerabilities) != 2 {
		t.Fatalf("trivy: %+v %v", rep, err)
	}
	if v := rep.Vulnerabilities[0]; v.Layer != "image" || v.FixedIn != "2" || v.Severity != "high" {
		t.Fatalf("trivy os: %+v", v)
	}
	if v := rep.Vulnerabilities[1]; v.Layer != "app" || v.PURL != "pkg:npm/left-pad@1" || v.Severity != "low" {
		t.Fatalf("trivy lang: %+v", v)
	}
	c := []byte(`{"scanner":"osv","vulnerabilities":[{"id":"CVE-9","package":"p","severity":"critical","fixed_in":"2"}]}`)
	rep, err = profile.ParseVulnReport(c, "")
	if err != nil || rep.Scanner != "osv" || profile.Summarize(rep.Vulnerabilities).FixableCritical != 1 {
		t.Fatalf("contratto: %+v %v", rep, err)
	}
	if _, err := profile.ParseVulnReport([]byte(`{"altro":1}`), "auto"); err == nil {
		t.Fatal("formato sconosciuto accettato")
	}
	if _, err := profile.SBOMFormat([]byte(`{"spdxVersion":"SPDX-2.3","packages":[]}`), "cyclonedx"); err == nil {
		t.Fatal("SPDX accettato come CycloneDX")
	}
}

func TestGitOpsEnvFile(t *testing.T) {
	def := presetDef(t)
	g := def.GitOps
	raw := fakegithub.EnvFile(map[string]fakegithub.EnvEntry{
		"ordini-api":    {Image: "registry.example.test/team/ordini-api", Tag: "1.4.0", Version: "1.4.0", Commit: "abc123"},
		"pagamenti-api": {Image: "registry.example.test/pagamenti-api", Tag: "2.1.0-3-deadbeef"},
	})
	list, err := g.ParseEnvFile("negozio.yaml", raw)
	if err != nil || len(list) != 2 {
		t.Fatalf("servizi: %+v %v", list, err)
	}
	o, p := list[0], list[1]
	if o.Key != "ordini-api" || o.Version != "1.4.0" || o.Commit != "abc123" || profile.ImageName(o.Image) != "ordini-api" {
		t.Fatalf("ordini: %+v", o)
	}
	if p.Version != "2.1.0-3-deadbeef" || p.Commit != "" {
		t.Fatalf("pagamenti (versione dal tag): %+v", p)
	}
	if g.EnvPath("prod") != "envs/prod" || g.EnvName("production") != "prod" || !g.MatchFile("a.yml") || g.MatchFile("README.md") {
		t.Fatal("percorsi e nomi degli ambienti")
	}
	if base, ok := g.EnvBase(); !ok || base != "envs" {
		t.Fatalf("base: %q %v", base, ok)
	}
	// Un file per servizio (senza la mappa services): il nome è quello del file.
	single := &profile.GitOpsSpec{Fields: map[string]profile.Path{"version": {"image.tag"}}}
	list, err = single.ParseEnvFile("envs/prod/catalogo.json", []byte(`{"image":{"tag":"3.0.0"}}`))
	if err != nil || len(list) != 1 || list[0].Key != "catalogo" || list[0].Version != "3.0.0" {
		t.Fatalf("file singolo: %+v %v", list, err)
	}
	if _, err := g.ParseEnvFile("rotto.yaml", []byte("services: [\n")); err == nil {
		t.Fatal("YAML rotto accettato")
	}
}

func TestBuildCheckRun(t *testing.T) {
	def := presetDef(t)
	at := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	svc := contract.Service{Name: "ordini-api"}
	runs := []profile.CheckRunInput{
		{ID: "1", Name: "ci / Lint and test", Status: "completed", Conclusion: "failure", CompletedAt: at},
		{ID: "2", Name: "ci / Verdict", Status: "in_progress"},
	}
	if _, ready := def.BuildCheckRun(svc, "sha", "main", runs, ""); ready {
		t.Fatal("verdetto in corso: non pronto")
	}
	runs[1] = profile.CheckRunInput{ID: "2", Name: "ci / Verdict", Status: "completed", Conclusion: "success", CompletedAt: at.Add(time.Minute), URL: "u"}
	cr, ready := def.BuildCheckRun(svc, "sha", "main", runs, "")
	if !ready || cr.Verdict != "passed" || cr.RunID != "2" || len(cr.Checks) != 1 || cr.Checks[0].Outcome != "failed" || !cr.At.Equal(at.Add(time.Minute)) {
		t.Fatalf("il verdetto decide (un warn fallito non lo rende rosso): %+v %v", cr, ready)
	}
	// Verdetto configurato nell'integrazione, e senza verdetto: tutti conclusi.
	if !profile.MatchesVerdict("ci / Verdict", "verdict") || profile.MatchesVerdict("Verdict extra", "Verdict") {
		t.Fatal("corrispondenza del verdetto")
	}
	var none *profile.Definition
	cr, ready = none.BuildCheckRun(svc, "sha", "main", []profile.CheckRunInput{
		{Name: "build", Status: "completed", Conclusion: "success"}, {Name: "test", Status: "completed", Conclusion: "failure"}}, "")
	if !ready || cr.Verdict != "failed" || len(cr.Checks) != 2 {
		t.Fatalf("senza verdetto: %+v", cr)
	}
}

func TestValidateReportsProblems(t *testing.T) {
	def, err := profile.Parse([]byte(`{"format":"altro","release":{"record":{"asset":"a/b.json","fields":{"sconosciuto":"x","version":"a..b"}},
		"checks":{"asset":"c.json","sources":[{"kind":"strano"}]}},"gitops":{"path":"envs","fields":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	ps := def.Validate()
	want := []string{"format", "release.record.asset", "release.record.fields.sconosciuto", "release.record.fields.version",
		"release.checks.sources[0].kind", "gitops.path", "gitops.fields.version"}
	for _, w := range want {
		found := false
		for _, p := range ps {
			if p.Path == w {
				found = true
			}
		}
		if !found {
			t.Errorf("manca il problema %s in %v", w, ps)
		}
	}
	if _, err := profile.Parse([]byte(`{"format":"x","campo_ignoto":1}`)); err == nil {
		t.Fatal("campo sconosciuto accettato")
	}
}
