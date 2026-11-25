package gitlab

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/gitlab/glapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
)

func TestNormalizeBaseURL(t *testing.T) {
	ok := map[string]string{
		"":                              "https://gitlab.com/api/v4",
		"https://gitlab.com":            "https://gitlab.com/api/v4",
		"gitlab.com/api/v4":             "https://gitlab.com/api/v4",
		"https://git.azienda.it/":       "https://git.azienda.it/api/v4",
		"https://azienda.it/gitlab":     "https://azienda.it/gitlab/api/v4",
		"https://git.azienda.it/api/v4": "https://git.azienda.it/api/v4",
	}
	for in, want := range ok {
		got, err := glapi.NormalizeBaseURL(in, ghapi.Public)
		if err != nil || got != want {
			t.Errorf("%q → %q, %v (atteso %q)", in, got, err, want)
		}
	}
	bad := []string{"http://git.azienda.it", "https://127.0.0.1", "https://gitlab.local", "https://u:p@git.azienda.it", "ftp://x.it", "https://x.it/?a=1", "https://intranet"}
	for _, in := range bad {
		if got, err := glapi.NormalizeBaseURL(in, ghapi.Public); err == nil {
			t.Errorf("%q doveva essere rifiutato, dato %q", in, got)
		}
	}
	// Rete interna: gli indirizzi privati sì, quelli link-local (metadati del cloud) mai.
	if _, err := glapi.NormalizeBaseURL("https://10.1.2.3", ghapi.Private); err != nil {
		t.Errorf("rete interna: %v", err)
	}
	if _, err := glapi.NormalizeBaseURL("https://169.254.169.254", ghapi.Private); err == nil {
		t.Error("link-local ammesso")
	}
	if _, err := glapi.NormalizeBaseURL("http://127.0.0.1:8174", ghapi.Any); err != nil {
		t.Errorf("modalità sviluppo: %v", err)
	}
}

func TestVerifyToken(t *testing.T) {
	if !VerifyToken("segreto-lungo-abcdefgh", "segreto-lungo-abcdefgh") {
		t.Error("token valido rifiutato")
	}
	for _, h := range []string{"", "altro", "segreto-lungo-abcdefgh "} {
		if h == "segreto-lungo-abcdefgh " {
			continue // gli spazi ai bordi si ignorano
		}
		if VerifyToken("segreto-lungo-abcdefgh", h) {
			t.Errorf("token %q accettato", h)
		}
	}
	if VerifyToken("", "") {
		t.Error("senza segreto non si accetta niente")
	}
}

func TestMatchName(t *testing.T) {
	cases := []struct {
		p, n string
		want bool
	}{
		{"gl-sbom-*.cdx.json", "gl-sbom-npm-npm.cdx.json", true},
		{"gl-sbom-*.cdx.json", "GL-SBOM-pip.cdx.json", true},
		{"gl-sbom-*.cdx.json", "sbom.json", false},
		{"sbom.cdx.json", "sbom.cdx.json", true},
		{"gl-*-scanning-report.json", "gl-container-scanning-report.json", true},
	}
	for _, c := range cases {
		if got := matchName(c.p, c.n); got != c.want {
			t.Errorf("matchName(%q, %q) = %v", c.p, c.n, got)
		}
	}
	if !matchesAny(nil, "qualunque") || !matchesAny([]string{"scan*"}, "scan-deps") || matchesAny([]string{"scan*"}, "build") {
		t.Error("matchesAny")
	}
}

const glReportJSON = `{"version":"15.0.7","scan":{"scanner":{"id":"trivy"},"end_time":"2026-10-01T09:10:00"},"vulnerabilities":[
 {"name":"x","severity":"Critical","solution":"Upgrade openssl to 3.0.14.","identifiers":[{"type":"ghsa","value":"GHSA-1111-2222-3333"},{"type":"cve","value":"CVE-2026-1"},{"type":"cwe","value":"79"}],
  "location":{"image":"reg/x:1","dependency":{"package":{"name":"openssl"},"version":"3.0.13"}}},
 {"name":"solo-nome","severity":"Info","identifiers":[],"location":{"dependency":{"package":{"name":"zod"},"version":"3"}}},
 {"name":"","severity":"High","identifiers":[]}]}`

func TestConvertGitLabReport(t *testing.T) {
	if !isGitLabReport([]byte(glReportJSON)) || isGitLabReport([]byte(`{"vulnerabilities":[]}`)) || isGitLabReport([]byte(`{"matches":[]}`)) {
		t.Fatal("riconoscimento del formato")
	}
	b, err := convertGitLabReport([]byte(glReportJSON))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := profile.ParseVulnReport(b, "contract")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanner != "trivy" || rep.ScannedAt.IsZero() || len(rep.Vulnerabilities) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	v := rep.Vulnerabilities[0]
	if v.ID != "CVE-2026-1" || len(v.Aliases) != 1 || v.Aliases[0] != "GHSA-1111-2222-3333" || v.FixedIn != "3.0.14" || v.Layer != "image" || v.Severity != "critical" || v.Version != "3.0.13" {
		t.Fatalf("vulnerabilità: %+v", v)
	}
	if w := rep.Vulnerabilities[1]; w.ID != "solo-nome" || w.Severity != "low" || w.Layer != "app" {
		t.Fatalf("seconda vulnerabilità: %+v", w)
	}
	sum := profile.Summarize(rep.Vulnerabilities)
	if sum.Critical != 1 || sum.FixableCritical != 1 {
		t.Fatalf("riassunto: %+v", sum)
	}
}

func TestWithGitLabReports(t *testing.T) {
	def := Presets()[0].Definition
	assets := map[string][]byte{"gl-*-scanning-report.json": []byte(glReportJSON)}
	var warns []string
	d2 := withGitLabReports(&def, assets, func(f string, a ...any) { warns = append(warns, f) })
	if d2 == &def || d2.Release.Vulnerabilities.Format != "contract" || def.Release.Vulnerabilities.Format != "auto" {
		t.Fatal("il profilo salvato non si deve modificare")
	}
	out := d2.ExtractRelease(profile.ReleaseInput{Service: contract.Service{Name: "x"}, Tag: "v1.0.0", Assets: assets}, "prod")
	if out.Vulns == nil || out.Release.CVEsAtRelease == nil || out.Release.CVEsAtRelease.Critical != 1 || out.Release.Version != "1.0.0" {
		t.Fatalf("estrazione: %+v", out)
	}
	// Un report grype passa com'è.
	assets = map[string][]byte{"gl-*-scanning-report.json": []byte(`{"matches":[]}`)}
	if d3 := withGitLabReports(&def, assets, nil); d3 != &def {
		t.Fatal("un report che non è di GitLab non si tocca")
	}
}

func TestReadZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range map[string]string{"report/gl-sbom-npm.cdx.json": "{}", "report/altro.txt": "x", "gl-sbom-big.cdx.json": strings.Repeat("a", 100)} {
		f, _ := zw.Create(n)
		_, _ = f.Write([]byte(c))
	}
	_ = zw.Close()
	got, err := readZip(buf.Bytes(), []string{"gl-sbom-*.cdx.json", "mancante.json"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got["gl-sbom-*.cdx.json"]) != "{}" {
		t.Fatalf("estratti: %v (il file sopra il limite va saltato)", got)
	}
	if _, err := readZip([]byte("non è uno zip"), nil, 10); err == nil {
		t.Error("uno zip rotto deve dare errore")
	}
}

func TestPresetValid(t *testing.T) {
	for _, p := range Presets() {
		if ps := p.Definition.Validate(); len(ps) > 0 {
			t.Errorf("preset %s non valido: %v", p.Key, ps)
		}
	}
	if PresetByKey(PresetGitLabCI) == nil || PresetByKey("x") != nil {
		t.Error("PresetByKey")
	}
}

func TestReleaseFingerprint(t *testing.T) {
	a := glapi.Release{TagName: "v1"}
	b := a
	b.Assets.Links = append(b.Assets.Links, glapi.Link{Name: "sbom", URL: "https://x"})
	if releaseFingerprint(a) == releaseFingerprint(b) {
		t.Error("un link aggiunto deve cambiare l'impronta")
	}
}
