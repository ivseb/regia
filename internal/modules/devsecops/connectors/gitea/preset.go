package gitea

import "regia/internal/modules/devsecops/connectors/profile"

// PresetGiteaActions è la chiave del preset per Gitea Actions con SBOM e scansioni.
const PresetGiteaActions = "gitea-actions-sbom-scans"

// Presets sono i profili pronti per Gitea. Si aggiungono ai preset degli altri connettori
// e si creano con POST /devsecops/pipeline-profiles dando la definizione.
//
// «Gitea Actions con SBOM e scansioni» legge ciò che un workflow di rilascio carica tra gli
// allegati della release: sbom.cdx.json (SBOM CycloneDX, per esempio da syft) e
// vulnerabilities.json (report di grype o trivy). Gli stati del commit del branch
// predefinito (quelli che Gitea Actions scrive per ogni job) sono i controlli.
func Presets() []profile.Preset {
	return []profile.Preset{{
		Key:  PresetGiteaActions,
		Name: "Gitea Actions con SBOM e scansioni",
		Description: "Release Gitea con allegati sbom.cdx.json (CycloneDX) e vulnerabilities.json (grype o trivy). " +
			"Gli stati del commit del branch predefinito sono i controlli. Gitea non ha deployment: i deploy arrivano " +
			"da altre fonti (API di push o GitOps).",
		Definition: profile.Definition{
			Format: profile.Format,
			Release: &profile.ReleaseSpec{
				Environment:     "prod",
				TagPrefix:       "v",
				TagFilter:       "v*",
				SBOM:            &profile.AssetSpec{Asset: "sbom.cdx.json", Format: "auto"},
				Vulnerabilities: &profile.AssetSpec{Asset: "vulnerabilities.json", Format: "auto"},
			},
			Checks: &profile.ChecksSpec{},
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
