package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"regia/internal/modules/devsecops/contract"
)

// outcomes sono gli esiti ammessi dal contratto.
var outcomes = []string{"passed", "failed", "skipped", "not_run"}

// defaultOutcomes traduce i valori più comuni (GitHub, Sonar, CI generiche).
var defaultOutcomes = map[string]string{
	"success": "passed", "passed": "passed", "pass": "passed", "ok": "passed", "neutral": "passed", "true": "passed",
	"failure": "failed", "failed": "failed", "fail": "failed", "error": "failed", "cancelled": "failed", "timed_out": "failed",
	"action_required": "failed", "startup_failure": "failed", "false": "failed",
	"skipped": "skipped", "skip": "skipped", "stale": "skipped",
	"": "not_run", "not_run": "not_run", "none": "not_run", "null": "not_run",
}

// Outcome traduce un valore in un esito del contratto, con la tabella del profilo prima.
func Outcome(v string, custom map[string]string) string {
	if o, ok := custom[v]; ok {
		return o
	}
	if o, ok := defaultOutcomes[strings.ToLower(strings.TrimSpace(v))]; ok {
		return o
	}
	return "not_run"
}

// ReleaseInput è una Release di GitHub già letta, con gli allegati richiesti dal profilo.
type ReleaseInput struct {
	Service     contract.Service
	Tag         string
	Name        string
	Author      string
	URL         string
	Commit      string // commit del tag, se noto
	PublishedAt time.Time
	// Assets: nome del file → contenuto (solo gli allegati del profilo presenti).
	Assets map[string][]byte
	// Present: nomi di tutti gli allegati della Release (per gli avvisi).
	Present []string
}

// ReleaseOutput sono gli oggetti del contratto ricavati da una Release.
type ReleaseOutput struct {
	Release  contract.Release     `json:"release"`
	SBOM     *contract.SBOM       `json:"sbom,omitempty"`
	Vulns    *contract.VulnReport `json:"vulnerabilities,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

// AcceptsTag dice se il profilo legge una Release con quel tag.
func (d *Definition) AcceptsTag(tag string, prerelease bool) bool {
	if d == nil || d.Release == nil {
		return !prerelease
	}
	if prerelease && !d.Release.IncludePrereleases {
		return false
	}
	if d.Release.TagFilter != "" {
		ok, _ := path.Match(d.Release.TagFilter, tag)
		return ok
	}
	return true
}

// ExtractRelease traduce una Release negli oggetti del contratto. Senza profilo (d nil o
// senza sezione release) usa solo i dati base: tag, data, autore.
func (d *Definition) ExtractRelease(in ReleaseInput, defaultEnv string) ReleaseOutput {
	var spec *ReleaseSpec
	if d != nil {
		spec = d.Release
	}
	env := defaultEnv
	prefix := "v"
	if spec != nil {
		if spec.Environment != "" {
			env = spec.Environment
		}
		prefix = spec.TagPrefix
	}
	if env == "" {
		env = "prod"
	}
	version := in.Tag
	if prefix != "" && strings.HasPrefix(version, prefix) && len(version) > len(prefix) {
		version = version[len(prefix):]
	}
	out := ReleaseOutput{Release: contract.Release{
		Service: in.Service, Version: version, Commit: in.Commit, Environment: env,
		RequestedBy: in.Author, ReleaseURL: in.URL, At: in.PublishedAt.UTC(),
	}}
	if spec == nil {
		return out
	}
	warn := func(f string, a ...any) { out.Warnings = append(out.Warnings, fmt.Sprintf(f, a...)) }
	asset := func(name string) ([]byte, bool) {
		b, ok := in.Assets[name]
		if !ok {
			warn("allegato %s assente nella Release %s", name, in.Tag)
		}
		return b, ok
	}

	// Registro di rilascio.
	var cvesFromRecord *contract.VulnSummary
	if rec := spec.Record; rec != nil {
		if raw, ok := asset(rec.Asset); ok {
			doc, err := decodeJSON(raw)
			if err != nil {
				warn("%s non è un JSON valido: %v", rec.Asset, err)
			} else {
				cvesFromRecord = applyRecord(&out.Release, rec.Fields, doc, warn)
			}
		}
	}
	if out.Release.Environment == "" {
		out.Release.Environment = env
	}

	// Esiti dei controlli del commit rilasciato.
	if cs := spec.Checks; cs != nil {
		if raw, ok := asset(cs.Asset); ok {
			doc, err := decodeJSON(raw)
			if err != nil {
				warn("%s non è un JSON valido: %v", cs.Asset, err)
			} else {
				out.Release.Checks = extractChecks(cs, doc)
				if len(out.Release.Checks) == 0 {
					warn("%s non contiene esiti leggibili con le fonti del profilo", cs.Asset)
				}
			}
		}
	}

	// Vulnerabilità al rilascio.
	if vs := spec.Vulnerabilities; vs != nil {
		if raw, ok := asset(vs.Asset); ok {
			rep, err := ParseVulnReport(raw, vs.Format)
			if err != nil {
				warn("%s: %v", vs.Asset, err)
			} else {
				rep.Service = in.Service
				rep.Version = out.Release.Version
				rep.Digest = out.Release.Digest
				if rep.ScannedAt.IsZero() {
					rep.ScannedAt = out.Release.At
				}
				out.Vulns = rep
				sum := Summarize(rep.Vulnerabilities)
				out.Release.CVEsAtRelease = &sum
			}
		}
	}
	if out.Release.CVEsAtRelease == nil && cvesFromRecord != nil {
		out.Release.CVEsAtRelease = cvesFromRecord
	}

	// SBOM.
	if ss := spec.SBOM; ss != nil {
		if raw, ok := asset(ss.Asset); ok {
			format, err := SBOMFormat(raw, ss.Format)
			if err != nil {
				warn("%s: %v", ss.Asset, err)
			} else {
				out.SBOM = &contract.SBOM{Service: in.Service, Version: out.Release.Version, Digest: out.Release.Digest, Format: format,
					Document: json.RawMessage(bytes.TrimSpace(raw))}
			}
		}
	}
	return out
}

// applyRecord valorizza la Release con i campi del registro; restituisce il riassunto
// delle CVE se il registro lo porta.
func applyRecord(r *contract.Release, fields map[string]Path, doc any, warn func(string, ...any)) *contract.VulnSummary {
	str := func(k string, dst *string) {
		if p, ok := fields[k]; ok {
			if v := p.Text(doc); v != "" {
				*dst = v
			}
		}
	}
	str("version", &r.Version)
	str("commit", &r.Commit)
	str("image", &r.Image)
	str("digest", &r.Digest)
	str("environment", &r.Environment)
	str("change_number", &r.ChangeNumber)
	str("change_title", &r.ChangeTitle)
	str("requested_by", &r.RequestedBy)
	str("approved_by", &r.ApprovedBy)
	str("pull_request", &r.PullRequest)
	if p, ok := fields["approved_at"]; ok {
		if t, ok := p.Time(doc); ok {
			r.ApprovedAt = &t
		}
	}
	if p, ok := fields["at"]; ok {
		if t, ok := p.Time(doc); ok {
			r.At = t
		} else if p.Text(doc) != "" {
			warn("istante del rilascio non leggibile: %q", p.Text(doc))
		}
	}
	var sum contract.VulnSummary
	found := false
	for k, dst := range map[string]*int{"cves.critical": &sum.Critical, "cves.high": &sum.High, "cves.medium": &sum.Medium,
		"cves.low": &sum.Low, "cves.fixable_critical": &sum.FixableCritical, "cves.fixable_high": &sum.FixableHigh} {
		if p, ok := fields[k]; ok {
			if n, ok := p.Int(doc); ok {
				*dst = n
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &sum
}

// extractChecks legge gli esiti dalle fonti del profilo, senza duplicati per nome.
func extractChecks(cs *AssetChecksSpec, doc any) []contract.CheckResult {
	var out []contract.CheckResult
	seen := map[string]bool{}
	add := func(c contract.CheckResult) {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" || seen[c.Name] {
			return
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	for _, s := range cs.Sources {
		if s.OnlyIfEmpty && len(out) > 0 {
			continue
		}
		switch s.Kind {
		case "map":
			v, ok := Path{s.Path}.Value(doc)
			m, isMap := v.(map[string]any)
			if !ok || !isMap {
				continue
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val := m[k]
				// Il valore può essere l'esito o un oggetto con outcome/mode.
				if obj, ok := val.(map[string]any); ok {
					add(contract.CheckResult{Name: k, Outcome: Outcome(Path{"outcome", "conclusion", "status"}.Text(obj), cs.Outcomes),
						Mode: normMode(Path{"mode"}.Text(obj)), Summary: Path{"summary"}.Text(obj), URL: Path{"url"}.Text(obj)})
					continue
				}
				add(contract.CheckResult{Name: k, Outcome: Outcome(scalarString(val), cs.Outcomes)})
			}
		case "list":
			v, ok := Path{s.Path}.Value(doc)
			list, isList := v.([]any)
			if !ok || !isList {
				continue
			}
			for _, it := range list {
				add(contract.CheckResult{
					Name: Path{s.Name}.Text(it), Outcome: Outcome(Path{s.Outcome}.Text(it), cs.Outcomes),
					URL: optText(s.URL, it), Summary: optText(s.Summary, it), Mode: normMode(optText(s.Mode, it)),
				})
			}
		case "single":
			if v, ok := (Path{s.Outcome}).Value(doc); ok {
				add(contract.CheckResult{Name: s.Name, Outcome: Outcome(scalarString(v), cs.Outcomes), URL: optText(s.URL, doc),
					Summary: optText(s.Summary, doc), Mode: normMode(optText(s.Mode, doc))})
			}
		}
	}
	return out
}

func optText(p string, doc any) string {
	if p == "" {
		return ""
	}
	return Path{p}.Text(doc)
}

func normMode(m string) string {
	switch strings.ToLower(m) {
	case "off", "warn", "block":
		return strings.ToLower(m)
	}
	return ""
}

func decodeJSON(raw []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// ---------------------------------------------------------------------
// SBOM e vulnerabilità
// ---------------------------------------------------------------------

// SBOMFormat riconosce il formato di un SBOM (cyclonedx | spdx).
func SBOMFormat(raw []byte, want string) (string, error) {
	var head struct {
		BOMFormat   string `json:"bomFormat"`
		SPDXVersion string `json:"spdxVersion"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return "", fmt.Errorf("l'SBOM non è un JSON valido")
	}
	got := ""
	switch {
	case strings.EqualFold(head.BOMFormat, "CycloneDX"):
		got = "cyclonedx"
	case head.SPDXVersion != "":
		got = "spdx"
	}
	if got == "" {
		return "", fmt.Errorf("formato dell'SBOM non riconosciuto (attesi CycloneDX o SPDX in JSON)")
	}
	if want != "" && want != "auto" && want != got {
		return "", fmt.Errorf("l'SBOM è %s, il profilo si aspetta %s", got, want)
	}
	return got, nil
}

// SBOMComponents conta i componenti di un SBOM (per l'anteprima).
func SBOMComponents(raw []byte) int {
	var doc struct {
		Components []json.RawMessage `json:"components"`
		Packages   []json.RawMessage `json:"packages"`
	}
	_ = json.Unmarshal(raw, &doc)
	return len(doc.Components) + len(doc.Packages)
}

// ParseVulnReport legge un report delle vulnerabilità (grype, trivy o il JSON del contratto).
func ParseVulnReport(raw []byte, format string) (*contract.VulnReport, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("report vuoto")
	}
	if format == "" || format == "auto" {
		format = detectVulnFormat(raw)
		if format == "" {
			return nil, fmt.Errorf("formato del report non riconosciuto (attesi grype, trivy o il JSON del contratto)")
		}
	}
	var rep *contract.VulnReport
	var err error
	switch format {
	case "grype":
		rep, err = parseGrype(raw)
	case "trivy":
		rep, err = parseTrivy(raw)
	case "contract":
		rep = &contract.VulnReport{}
		err = json.Unmarshal(raw, rep)
		if rep.Scanner == "" {
			rep.Scanner = "other"
		}
	default:
		return nil, fmt.Errorf("formato %q non gestito", format)
	}
	if err != nil {
		return nil, fmt.Errorf("report %s non leggibile: %v", format, err)
	}
	rep.Vulnerabilities = dedupVulns(rep.Vulnerabilities)
	return rep, nil
}

func detectVulnFormat(raw []byte) string {
	if raw[0] == '[' {
		var arr []map[string]json.RawMessage
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			if _, ok := arr[0]["vulnerability"]; ok {
				return "grype"
			}
		}
		if len(arr) == 0 {
			return "grype"
		}
		return ""
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return ""
	}
	if _, ok := top["matches"]; ok {
		return "grype"
	}
	if _, ok := top["Results"]; ok {
		return "trivy"
	}
	if _, ok := top["SchemaVersion"]; ok {
		return "trivy"
	}
	if _, ok := top["vulnerabilities"]; ok {
		return "contract"
	}
	return ""
}

type grypeMatch struct {
	Vulnerability struct {
		ID         string   `json:"id"`
		Severity   string   `json:"severity"`
		DataSource string   `json:"dataSource"`
		URLs       []string `json:"urls"`
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
}

// Tipi di pacchetto dell'immagine (sistema operativo e runtime): una ricostruzione li corregge.
var imageTypes = map[string]bool{"deb": true, "rpm": true, "apk": true, "alpm": true, "portage": true, "nix": true, "binary": true, "linux-kernel": true}

func parseGrype(raw []byte) (*contract.VulnReport, error) {
	var matches []grypeMatch
	rep := &contract.VulnReport{Scanner: "grype"}
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &matches); err != nil {
			return nil, err
		}
	} else {
		var doc struct {
			Matches    []grypeMatch `json:"matches"`
			Descriptor struct {
				Timestamp string `json:"timestamp"`
			} `json:"descriptor"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		matches = doc.Matches
		if t, err := time.Parse(time.RFC3339Nano, doc.Descriptor.Timestamp); err == nil {
			rep.ScannedAt = t.UTC()
		}
	}
	for _, m := range matches {
		v := contract.Vulnerability{
			ID: m.Vulnerability.ID, Package: m.Artifact.Name, PURL: m.Artifact.PURL, Version: m.Artifact.Version,
			Severity: normSeverity(m.Vulnerability.Severity), URL: m.Vulnerability.DataSource,
		}
		if v.URL == "" && len(m.Vulnerability.URLs) > 0 {
			v.URL = m.Vulnerability.URLs[0]
		}
		if m.Vulnerability.Fix.State != "not-fixed" && m.Vulnerability.Fix.State != "wont-fix" && len(m.Vulnerability.Fix.Versions) > 0 {
			v.FixedIn = m.Vulnerability.Fix.Versions[0]
		}
		for _, r := range m.Related {
			if r.ID != "" && r.ID != v.ID {
				v.Aliases = append(v.Aliases, r.ID)
			}
		}
		// Un GHSA con un CVE equivalente si riporta con il CVE (stesso identificativo per
		// tutti gli scanner); il GHSA resta tra gli alias.
		if !strings.HasPrefix(v.ID, "CVE-") {
			for i, a := range v.Aliases {
				if strings.HasPrefix(a, "CVE-") {
					v.Aliases[i] = v.ID
					v.ID = a
					break
				}
			}
		}
		if imageTypes[m.Artifact.Type] {
			v.Layer = "image"
		} else if m.Artifact.Type != "" {
			v.Layer = "app"
		}
		if v.ID != "" {
			rep.Vulnerabilities = append(rep.Vulnerabilities, v)
		}
	}
	return rep, nil
}

func parseTrivy(raw []byte) (*contract.VulnReport, error) {
	var doc struct {
		CreatedAt string `json:"CreatedAt"`
		Results   []struct {
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
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	rep := &contract.VulnReport{Scanner: "trivy"}
	if t, err := time.Parse(time.RFC3339Nano, doc.CreatedAt); err == nil {
		rep.ScannedAt = t.UTC()
	}
	for _, r := range doc.Results {
		layer := "app"
		if r.Class == "os-pkgs" {
			layer = "image"
		}
		for _, x := range r.Vulnerabilities {
			fixed, _, _ := strings.Cut(x.FixedVersion, ",")
			rep.Vulnerabilities = append(rep.Vulnerabilities, contract.Vulnerability{
				ID: x.VulnerabilityID, Package: x.PkgName, PURL: x.PkgIdentifier.PURL, Version: x.InstalledVersion,
				FixedIn: strings.TrimSpace(fixed), Severity: normSeverity(x.Severity), Layer: layer, URL: x.PrimaryURL,
			})
		}
	}
	return rep, nil
}

func normSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "high":
		return "high"
	case "medium", "moderate":
		return "medium"
	case "low", "negligible":
		return "low"
	}
	return "unknown"
}

func dedupVulns(in []contract.Vulnerability) []contract.Vulnerability {
	seen := map[string]bool{}
	out := make([]contract.Vulnerability, 0, len(in))
	for _, v := range in {
		k := v.ID + "\x00" + v.Package + "\x00" + v.Version
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

// Summarize conta le vulnerabilità per severità; correggibile = c'è una versione corretta.
func Summarize(vs []contract.Vulnerability) contract.VulnSummary {
	var s contract.VulnSummary
	for _, v := range vs {
		fix := v.FixedIn != ""
		switch v.Severity {
		case "critical":
			s.Critical++
			if fix {
				s.FixableCritical++
			}
		case "high":
			s.High++
			if fix {
				s.FixableHigh++
			}
		case "medium":
			s.Medium++
		case "low":
			s.Low++
		}
	}
	return s
}

// ---------------------------------------------------------------------
// Repository GitOps
// ---------------------------------------------------------------------

// EnvService è un servizio letto da un file di un ambiente.
type EnvService struct {
	Key     string `json:"key"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version"`
	Image   string `json:"image,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// EnvPath restituisce la cartella di un ambiente.
func (g *GitOpsSpec) EnvPath(env string) string {
	p := g.Path
	if p == "" {
		p = "envs/{environment}"
	}
	return strings.Trim(strings.ReplaceAll(p, "{environment}", env), "/")
}

// EnvBase restituisce la cartella che contiene gli ambienti (per scoprirli) e se la
// struttura lo consente ({environment} come ultima parte del percorso).
func (g *GitOpsSpec) EnvBase() (string, bool) {
	p := g.Path
	if p == "" {
		p = "envs/{environment}"
	}
	p = strings.Trim(p, "/")
	dir, last := path.Split(p)
	if last != "{environment}" {
		return "", false
	}
	return strings.Trim(dir, "/"), true
}

// EnvName traduce il nome di una cartella nell'ambiente del contratto.
func (g *GitOpsSpec) EnvName(dir string) string {
	if n, ok := g.EnvironmentNames[dir]; ok && n != "" {
		return n
	}
	return dir
}

// MatchFile dice se un file della cartella va letto.
func (g *GitOpsSpec) MatchFile(name string) bool {
	globs := g.Files
	if len(globs) == 0 {
		globs = []string{"*.yaml", "*.yml", "*.json"}
	}
	for _, gl := range globs {
		if ok, _ := path.Match(gl, name); ok {
			return true
		}
	}
	return false
}

// ParseEnvFile legge i servizi di un file di un ambiente (YAML o JSON).
func (g *GitOpsSpec) ParseEnvFile(name string, raw []byte) ([]EnvService, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s non è un YAML/JSON valido: %v", name, firstLine(err.Error()))
	}
	doc = normalizeYAML(doc)
	base := strings.TrimSuffix(path.Base(name), path.Ext(name))
	var out []EnvService
	read := func(key string, block any) {
		s := EnvService{Key: key}
		get := func(k string) string {
			if p, ok := g.Fields[k]; ok {
				return p.Text(block)
			}
			return ""
		}
		s.Version, s.Image, s.Digest, s.Commit, s.Name = get("version"), get("image"), get("digest"), get("commit"), get("name")
		if s.Version != "" {
			out = append(out, s)
		}
	}
	if g.Services == "" {
		read(base, doc)
		return out, nil
	}
	v, ok := Path{g.Services}.Value(doc)
	if !ok {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s non è un oggetto nome → servizio", name, g.Services)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		read(k, m[k])
	}
	return out, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// normalizeYAML porta i valori di yaml.v3 nei tipi di encoding/json (mappe con chiavi
// stringa, numeri float64), così i percorsi funzionano allo stesso modo.
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalizeYAML(e)
		}
		return x
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range x {
			x[i] = normalizeYAML(e)
		}
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return v
}

// ImageName restituisce il nome dell'immagine senza registry, tag e digest
// (registry.example.com/team/orders-api:1.2 → orders-api).
func ImageName(image string) string {
	image = strings.TrimSpace(image)
	if i := strings.IndexByte(image, '@'); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndexByte(image, '/'); i >= 0 {
		image = image[i+1:]
	}
	if i := strings.IndexByte(image, ':'); i >= 0 {
		image = image[:i]
	}
	return strings.ToLower(image)
}

// ---------------------------------------------------------------------
// Deployments API e check run
// ---------------------------------------------------------------------

// DeploymentVersion legge la versione di un deployment (documento JSON di GitHub).
func (d *Definition) DeploymentVersion(dep any) (version, digest string) {
	vp, dp := Path{"payload.version", "ref"}, Path{"payload.digest"}
	if d != nil && d.Deployments != nil {
		if len(d.Deployments.Version) > 0 {
			vp = d.Deployments.Version
		}
		if len(d.Deployments.Digest) > 0 {
			dp = d.Deployments.Digest
		}
	}
	version = vp.Text(dep)
	if strings.HasPrefix(version, "v") && len(version) > 1 && version[1] >= '0' && version[1] <= '9' {
		version = version[1:]
	}
	return version, dp.Text(dep)
}

// DeploymentEnv traduce il nome di un ambiente della Deployments API.
func (d *Definition) DeploymentEnv(env string) string {
	if d != nil && d.Deployments != nil {
		if n, ok := d.Deployments.Environments[env]; ok && n != "" {
			return n
		}
	}
	return env
}

// DeploymentsEnabled dice se leggere la Deployments API.
func (d *Definition) DeploymentsEnabled() bool {
	return d == nil || d.Deployments == nil || !d.Deployments.Disabled
}

// DeploymentsMax è il numero di deployment recenti letti per repository.
func (d *Definition) DeploymentsMax() int {
	if d != nil && d.Deployments != nil && d.Deployments.Max > 0 {
		return d.Deployments.Max
	}
	return 30
}

// DeploymentStatus traduce lo stato di GitHub in quello del contratto.
func DeploymentStatus(state string) string {
	switch state {
	case "success", "inactive":
		return "succeeded" // inactive: sostituito da un deploy più recente, era andato a buon fine
	case "failure", "error":
		return "failed"
	}
	return "running" // queued, pending, in_progress
}

// CheckRunInput è un check run di GitHub già letto.
type CheckRunInput struct {
	ID          string
	Name        string
	Status      string // queued | in_progress | completed
	Conclusion  string
	URL         string
	Summary     string
	CompletedAt time.Time
}

// MatchesVerdict dice se il nome di un check run è il verdetto configurato.
func MatchesVerdict(name, verdict string) bool {
	if verdict == "" {
		return false
	}
	name, verdict = strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(verdict))
	return name == verdict || strings.HasSuffix(name, " / "+verdict)
}

// BuildCheckRun compone l'esito complessivo dei controlli di un commit. ready è falso se
// i controlli non sono ancora conclusi (il verdetto, o tutti se il verdetto non c'è).
func (d *Definition) BuildCheckRun(svc contract.Service, sha, ref string, runs []CheckRunInput, verdict string) (contract.CheckRun, bool) {
	var spec ChecksSpec
	if d != nil && d.Checks != nil {
		spec = *d.Checks
	}
	if verdict == "" {
		verdict = spec.Verdict
	}
	cr := contract.CheckRun{Service: svc, Commit: sha, Ref: ref, Checks: []contract.CheckResult{}}
	var verdictRun *CheckRunInput
	allDone := true
	anyFailed := false
	seen := map[string]bool{}
	for i := range runs {
		r := runs[i]
		if ignored(spec.Ignore, r.Name) {
			continue
		}
		if MatchesVerdict(r.Name, verdict) {
			verdictRun = &runs[i]
			continue
		}
		name := r.Name
		if n, ok := spec.Names[r.Name]; ok && n != "" {
			name = n
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		outcome := "not_run"
		if r.Status == "completed" {
			outcome = Outcome(r.Conclusion, nil)
		} else {
			allDone = false
		}
		if outcome == "failed" {
			anyFailed = true
		}
		cr.Checks = append(cr.Checks, contract.CheckResult{Name: name, Outcome: outcome, URL: r.URL, Summary: r.Summary})
		if r.CompletedAt.After(cr.At) {
			cr.At = r.CompletedAt
		}
	}
	if verdictRun != nil {
		cr.RunID, cr.URL = verdictRun.ID, verdictRun.URL
		if verdictRun.Status != "completed" {
			return cr, false
		}
		cr.Verdict = "failed"
		if Outcome(verdictRun.Conclusion, nil) == "passed" {
			cr.Verdict = "passed"
		}
		if verdictRun.CompletedAt.After(cr.At) {
			cr.At = verdictRun.CompletedAt
		}
		return cr, true
	}
	if !allDone || len(cr.Checks) == 0 {
		return cr, false
	}
	cr.Verdict = "passed"
	if anyFailed {
		cr.Verdict = "failed"
	}
	return cr, true
}

func ignored(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}
