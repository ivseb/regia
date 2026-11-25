package gitlab

import "regia/internal/modules/devsecops/connectors/profile"

// PresetGitLabCI è la chiave del preset per GitLab CI con SBOM e scansioni.
const PresetGitLabCI = "gitlab-ci-sbom-scans"

// Presets sono i profili pronti per GitLab. Si aggiungono ai preset del connettore GitHub
// (che restano dove sono) e si creano con POST /devsecops/pipeline-profiles dando la
// definizione.
//
// «GitLab CI con SBOM e scansioni» legge ciò che i template di sicurezza di GitLab (e
// quasi ogni pipeline con syft, grype o trivy) lasciano come artefatti dei job del tag:
//   - gl-sbom-*.cdx.json: SBOM CycloneDX del Dependency Scanning;
//   - gl-*-scanning-report.json: report di Dependency Scanning o Container Scanning nel
//     formato di GitLab (Regia lo traduce), oppure grype/trivy se la pipeline li produce
//     con quel nome;
//   - i job della pipeline del branch predefinito diventano i check run, e Environments e
//     Deployments di GitLab diventano i deploy.
//
// Se la pipeline pubblica gli stessi file come link della Release, vale lo stesso profilo:
// i link si cercano prima degli artefatti.
func Presets() []profile.Preset {
	return []profile.Preset{{
		Key:  PresetGitLabCI,
		Name: "GitLab CI con SBOM e scansioni",
		Description: "Release GitLab il cui tag ha una pipeline con SBOM CycloneDX (gl-sbom-*.cdx.json) e report di vulnerabilità " +
			"(gl-*-scanning-report.json, in formato GitLab, grype o trivy) tra gli artefatti dei job, o come link della Release. " +
			"I job del branch predefinito sono i controlli; Environments e Deployments sono i deploy.",
		Definition: profile.Definition{
			Format: profile.Format,
			Release: &profile.ReleaseSpec{
				Environment:     "prod",
				TagPrefix:       "v",
				TagFilter:       "v*",
				SBOM:            &profile.AssetSpec{Asset: "gl-sbom-*.cdx.json", Format: "auto"},
				Vulnerabilities: &profile.AssetSpec{Asset: "gl-*-scanning-report.json", Format: "auto"},
			},
			Checks: &profile.ChecksSpec{},
			Deployments: &profile.DeploymentsSpec{
				Environments: map[string]string{"production": "prod"},
			},
		},
	}}
}

// PresetByKey restituisce un preset (nil se non esiste).
func PresetByKey(key string) *profile.Preset {
	for _, p := range Presets() {
		if p.Key == key {
			return &p
		}
	}
	return nil
}
