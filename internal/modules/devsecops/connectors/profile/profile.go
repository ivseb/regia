// Package profile è il profilo di pipeline (replica/devsecops.md §2, punto 2): configurazione,
// non codice, che spiega a Regia dove una famiglia di template deposita i suoi artefatti
// e come tradurli negli oggetti del contratto neutro.
//
//   - Allegati delle Release: nome del file e mappatura dei campi JSON sui campi del
//     contratto con percorsi semplici (path.go); SBOM e report delle vulnerabilità nei
//     formati standard (CycloneDX, SPDX, grype, trivy).
//   - Check run dell'ultimo commit: quale controllo è il "verdetto", nomi da tradurre.
//   - Deployments API di GitHub: dove stanno versione e digest.
//   - Repository GitOps: branch, cartella per ambiente (envs/<ambiente>/), file e campi con
//     versione e immagine. Ne escono oggetti Deployment.
//
// Il pacchetto non fa chiamate di rete: riceve i documenti già letti e restituisce gli
// oggetti del contratto, così l'anteprima e la sincronizzazione usano lo stesso codice.
package profile

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// Format identifica il formato del profilo (export e import).
const Format = "regia.pipeline-profile/v1"

// Definition è il contenuto di una versione del profilo.
type Definition struct {
	Format      string           `json:"format"`
	Release     *ReleaseSpec     `json:"release,omitempty"`
	Checks      *ChecksSpec      `json:"checks,omitempty"`
	Deployments *DeploymentsSpec `json:"deployments,omitempty"`
	GitOps      *GitOpsSpec      `json:"gitops,omitempty"`
	// Adoption dice come Regia propone la pipeline a un repository (adoption.go, fase 3).
	Adoption *AdoptionSpec `json:"adoption,omitempty"`
}

// ReleaseSpec dice come leggere le Release di GitHub.
type ReleaseSpec struct {
	// Environment è l'ambiente dei rilasci quando il registro non lo dice (default "prod").
	Environment string `json:"environment,omitempty"`
	// TagPrefix si toglie dal tag per ottenere la versione (es. "v": v1.4.0 → 1.4.0).
	TagPrefix string `json:"tag_prefix,omitempty"`
	// TagFilter, se presente, è un modello (glob) dei tag da leggere (es. "v*").
	TagFilter string `json:"tag_filter,omitempty"`
	// IncludePrereleases legge anche le pre-release (di default no) e le bozze mai.
	IncludePrereleases bool `json:"include_prereleases,omitempty"`
	// MaxAssetMB è il limite di un allegato scaricato (default 25 MB).
	MaxAssetMB int `json:"max_asset_mb,omitempty"`

	Record          *RecordSpec      `json:"record,omitempty"`
	Checks          *AssetChecksSpec `json:"checks,omitempty"`
	SBOM            *AssetSpec       `json:"sbom,omitempty"`
	Vulnerabilities *AssetSpec       `json:"vulnerabilities,omitempty"`
}

// RecordSpec è il registro di rilascio allegato alla Release (JSON).
type RecordSpec struct {
	Asset  string          `json:"asset"`
	Fields map[string]Path `json:"fields"`
}

// RecordFields sono i campi del contratto (Release) che il registro può valorizzare.
var RecordFields = map[string]string{
	"version":               "versione rilasciata (altrimenti dal tag)",
	"commit":                "commit da cui è costruito l'artefatto",
	"image":                 "immagine",
	"digest":                "digest dell'artefatto (sha256:…)",
	"environment":           "ambiente del rilascio",
	"change_number":         "numero di change",
	"change_title":          "titolo della change",
	"requested_by":          "chi ha chiesto il rilascio",
	"approved_by":           "chi l'ha approvato",
	"approved_at":           "quando è stato approvato",
	"pull_request":          "URL della pull request di rilascio",
	"at":                    "istante del rilascio (altrimenti la pubblicazione della Release)",
	"cves.critical":         "CVE critiche al rilascio",
	"cves.high":             "CVE alte al rilascio",
	"cves.medium":           "CVE medie al rilascio",
	"cves.low":              "CVE basse al rilascio",
	"cves.fixable_critical": "CVE critiche correggibili al rilascio",
	"cves.fixable_high":     "CVE alte correggibili al rilascio",
}

// AssetSpec è un allegato in un formato standard.
type AssetSpec struct {
	Asset string `json:"asset"`
	// Format: auto (default) | cyclonedx | spdx per l'SBOM; auto | grype | trivy | contract
	// per le vulnerabilità.
	Format string `json:"format,omitempty"`
}

// AssetChecksSpec è l'allegato con gli esiti dei controlli del commit rilasciato.
type AssetChecksSpec struct {
	Asset   string        `json:"asset"`
	Sources []CheckSource `json:"sources"`
	// Outcomes traduce i valori del file negli esiti del contratto (passed, failed,
	// skipped, not_run); si aggiunge alla tabella predefinita (success → passed…).
	Outcomes map[string]string `json:"outcomes,omitempty"`
}

// CheckSource è un punto del file da cui leggere esiti.
//
//	kind "map":    Path porta a un oggetto nome → esito (es. {"test": "success"})
//	kind "list":   Path porta a un elenco di oggetti; Name, Outcome, URL, Summary e Mode
//	               sono i campi di ogni elemento
//	kind "single": un controllo solo, di nome Name, con l'esito in Outcome (percorso)
type CheckSource struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary,omitempty"`
	Mode    string `json:"mode,omitempty"`
	// OnlyIfEmpty: la fonte si usa solo se le precedenti non hanno dato esiti.
	OnlyIfEmpty bool `json:"only_if_empty,omitempty"`
}

// ChecksSpec dice come leggere i check run dell'ultimo commit del branch predefinito.
type ChecksSpec struct {
	// Verdict è il nome del controllo "verdetto": vale il nome esatto o la sua parte
	// finale dopo " / " (i job dei workflow riusabili si chiamano "ci / Verdict").
	Verdict string `json:"verdict,omitempty"`
	// Names traduce i nomi dei check run nei nomi del contratto (es. "ci / Lint" → "lint").
	Names map[string]string `json:"names,omitempty"`
	// Ignore sono modelli (glob) di check run da non riportare.
	Ignore []string `json:"ignore,omitempty"`
}

// DeploymentsSpec dice come leggere la Deployments API di GitHub.
type DeploymentsSpec struct {
	// Disabled spegne la lettura (repository che non usano la Deployments API).
	Disabled bool `json:"disabled,omitempty"`
	// Version: percorso nel deployment (default payload.version, poi ref).
	Version Path `json:"version,omitempty"`
	Digest  Path `json:"digest,omitempty"`
	// Environments traduce i nomi degli ambienti (es. "production" → "prod").
	Environments map[string]string `json:"environments,omitempty"`
	// Max è il numero di deployment recenti letti per repository (default 30).
	Max int `json:"max,omitempty"`
}

// GitOpsSpec dice come leggere lo stato degli ambienti da un repository GitOps.
type GitOpsSpec struct {
	// Repositories (org/nome): se vuoto, si indicano nell'integrazione.
	Repositories []string `json:"repositories,omitempty"`
	// Branch letto (default: il branch predefinito del repository).
	Branch string `json:"branch,omitempty"`
	// Path della cartella di un ambiente, con {environment} (default "envs/{environment}").
	Path string `json:"path,omitempty"`
	// Environments da leggere; vuoto = tutte le sottocartelle.
	Environments []string `json:"environments,omitempty"`
	// Files: modelli (glob) dei file letti in ogni cartella (default *.yaml, *.yml, *.json).
	Files []string `json:"files,omitempty"`
	// Services è il percorso dell'oggetto con i servizi (nome → valori); vuoto = ogni file
	// descrive un solo servizio, con il nome del file.
	Services string `json:"services,omitempty"`
	// Fields: version, image, digest, commit dentro il blocco del servizio.
	Fields map[string]Path `json:"fields"`
	// EnvironmentNames traduce i nomi delle cartelle (es. "production" → "prod").
	EnvironmentNames map[string]string `json:"environment_names,omitempty"`
}

// GitOpsFields sono i campi che il blocco del servizio può valorizzare.
var GitOpsFields = map[string]string{
	"version": "versione in esecuzione",
	"image":   "immagine (serve anche ad associare il servizio al repository)",
	"digest":  "digest dell'immagine",
	"commit":  "commit da cui è costruita",
	"name":    "nome del servizio, se diverso dalla chiave",
}

// Problem è un errore di validazione, con il punto del profilo a cui si riferisce.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Parse legge una definizione JSON rifiutando i campi sconosciuti.
func Parse(raw []byte) (*Definition, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var d Definition
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("profilo non valido: %v", err)
	}
	return &d, nil
}

// Validate controlla la definizione e restituisce i problemi (vuoto = valida).
func (d *Definition) Validate() []Problem {
	var out []Problem
	add := func(p, m string) { out = append(out, Problem{Path: p, Message: m}) }
	if d.Format != Format {
		add("format", "deve valere "+Format)
	}
	if d.Release == nil && d.Checks == nil && d.Deployments == nil && d.GitOps == nil && d.Adoption == nil {
		add("", "il profilo è vuoto: serve almeno una tra release, checks, deployments, gitops e adoption")
	}
	if r := d.Release; r != nil {
		if r.TagFilter != "" {
			if _, err := path.Match(r.TagFilter, "v1.0.0"); err != nil {
				add("release.tag_filter", "modello non valido")
			}
		}
		if r.MaxAssetMB < 0 || r.MaxAssetMB > 200 {
			add("release.max_asset_mb", "tra 1 e 200")
		}
		names := map[string]string{}
		asset := func(where, name string) {
			name = strings.TrimSpace(name)
			if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\") {
				add(where+".asset", "serve il nome del file allegato (senza cartelle)")
				return
			}
			if prev, ok := names[name]; ok {
				add(where+".asset", "lo stesso allegato è già usato da "+prev)
			}
			names[name] = where
		}
		if rec := r.Record; rec != nil {
			asset("release.record", rec.Asset)
			for k, p := range rec.Fields {
				if _, ok := RecordFields[k]; !ok {
					add("release.record.fields."+k, "campo sconosciuto; ammessi: "+strings.Join(sortedKeys(RecordFields), ", "))
					continue
				}
				if err := p.Check(); err != nil {
					add("release.record.fields."+k, err.Error())
				}
			}
		}
		if c := r.Checks; c != nil {
			asset("release.checks", c.Asset)
			if len(c.Sources) == 0 {
				add("release.checks.sources", "serve almeno una fonte di esiti")
			}
			for i, s := range c.Sources {
				where := fmt.Sprintf("release.checks.sources[%d]", i)
				switch s.Kind {
				case "map", "list":
					if s.Path == "" {
						add(where+".path", "serve il percorso")
					}
					if s.Kind == "list" && (s.Name == "" || s.Outcome == "") {
						add(where, "un elenco ha bisogno dei campi name e outcome")
					}
				case "single":
					if s.Name == "" || s.Outcome == "" {
						add(where, "serve il nome del controllo (name) e il percorso dell'esito (outcome)")
					}
				default:
					add(where+".kind", "ammessi map, list e single")
				}
				fields := map[string]string{"path": s.Path, "outcome": s.Outcome, "url": s.URL, "summary": s.Summary, "mode": s.Mode}
				if s.Kind == "list" {
					fields["name"] = s.Name
				}
				for k, p := range fields {
					if p == "" {
						continue
					}
					if err := (Path{p}).Check(); err != nil {
						add(where+"."+k, err.Error())
					}
				}
			}
			for k, v := range c.Outcomes {
				if !slices.Contains(outcomes, v) {
					add("release.checks.outcomes."+k, "esito ammesso: "+strings.Join(outcomes, ", "))
				}
			}
		}
		if s := r.SBOM; s != nil {
			asset("release.sbom", s.Asset)
			if !slices.Contains([]string{"", "auto", "cyclonedx", "spdx"}, s.Format) {
				add("release.sbom.format", "ammessi auto, cyclonedx e spdx")
			}
		}
		if v := r.Vulnerabilities; v != nil {
			asset("release.vulnerabilities", v.Asset)
			if !slices.Contains([]string{"", "auto", "grype", "trivy", "contract"}, v.Format) {
				add("release.vulnerabilities.format", "ammessi auto, grype, trivy e contract")
			}
		}
	}
	if c := d.Checks; c != nil {
		for _, g := range c.Ignore {
			if _, err := path.Match(g, "x"); err != nil {
				add("checks.ignore", "modello non valido: "+g)
			}
		}
	}
	if dp := d.Deployments; dp != nil {
		for k, p := range map[string]Path{"version": dp.Version, "digest": dp.Digest} {
			if err := p.Check(); err != nil {
				add("deployments."+k, err.Error())
			}
		}
		if dp.Max < 0 || dp.Max > 100 {
			add("deployments.max", "tra 1 e 100")
		}
	}
	if g := d.GitOps; g != nil {
		for i, r := range g.Repositories {
			if !validFullName(r) {
				add(fmt.Sprintf("gitops.repositories[%d]", i), "serve organizzazione/nome")
			}
		}
		if g.Path != "" && (strings.HasPrefix(g.Path, "/") || strings.Contains(g.Path, "..")) {
			add("gitops.path", "percorso relativo alla radice del repository, senza ..")
		}
		if g.Path != "" && !strings.Contains(g.Path, "{environment}") {
			add("gitops.path", "deve contenere {environment} (es. envs/{environment})")
		}
		for _, f := range g.Files {
			if _, err := path.Match(f, "x.yaml"); err != nil {
				add("gitops.files", "modello non valido: "+f)
			}
		}
		if g.Services != "" {
			if err := (Path{g.Services}).Check(); err != nil {
				add("gitops.services", err.Error())
			}
		}
		if len(g.Fields) == 0 || len(g.Fields["version"]) == 0 {
			add("gitops.fields.version", "serve il percorso della versione")
		}
		for k, p := range g.Fields {
			if _, ok := GitOpsFields[k]; !ok {
				add("gitops.fields."+k, "campo sconosciuto; ammessi: "+strings.Join(sortedKeys(GitOpsFields), ", "))
				continue
			}
			if err := p.Check(); err != nil {
				add("gitops.fields."+k, err.Error())
			}
		}
	}
	d.Adoption.validate(add)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func validFullName(s string) bool {
	org, name, ok := strings.Cut(s, "/")
	return ok && org != "" && name != "" && !strings.ContainsAny(name, "/ ") && !strings.ContainsAny(org, " ")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AssetNames sono gli allegati che il profilo legge da una Release.
func (d *Definition) AssetNames() []string {
	if d == nil || d.Release == nil {
		return nil
	}
	var out []string
	r := d.Release
	if r.Record != nil {
		out = append(out, r.Record.Asset)
	}
	if r.Checks != nil {
		out = append(out, r.Checks.Asset)
	}
	if r.SBOM != nil {
		out = append(out, r.SBOM.Asset)
	}
	if r.Vulnerabilities != nil {
		out = append(out, r.Vulnerabilities.Asset)
	}
	return out
}

// MaxAssetBytes è il limite di un allegato.
func (d *Definition) MaxAssetBytes() int64 {
	if d != nil && d.Release != nil && d.Release.MaxAssetMB > 0 {
		return int64(d.Release.MaxAssetMB) << 20
	}
	return 25 << 20
}

// Canonical restituisce il JSON della definizione con indentazione stabile.
func (d *Definition) Canonical() []byte {
	b, _ := json.MarshalIndent(d, "", "  ")
	return b
}
