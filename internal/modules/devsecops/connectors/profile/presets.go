package profile

// Preset è un profilo pronto da cui partire. I preset sono scritti per Regia: descrivono
// un formato (nomi degli allegati, campi, cartelle), non contengono codice dei template.
type Preset struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Definition  Definition `json:"definition"`
}

// PresetGitOpsArgo è la chiave del preset per i template GitHub Actions con rilascio GitOps.
const PresetGitOpsArgo = "github-actions-gitops-argocd"

// Presets elenca i preset inclusi.
func Presets() []Preset {
	return []Preset{
		{
			Key:  PresetGitOpsArgo,
			Name: "Template GitHub Actions + GitOps (Argo CD)",
			Description: "Workflow riusabili GitHub Actions che allegano a ogni Release il registro di rilascio, gli esiti dei controlli, l'SBOM CycloneDX " +
				"e il report grype, con lo stato degli ambienti in envs/<ambiente>/ di un repository GitOps letto da Argo CD.",
			Definition: gitOpsArgo(),
		},
		{
			Key:  "github-release-standard",
			Name: "Release GitHub con SBOM e vulnerabilità",
			Description: "Solo allegati in formato standard: un SBOM (CycloneDX o SPDX) e un report delle vulnerabilità (grype o trivy) " +
				"su ogni Release, più i check run e la Deployments API. Va bene per pipeline semplici.",
			Definition: Definition{
				Format: Format,
				Release: &ReleaseSpec{
					Environment:     "prod",
					TagPrefix:       "v",
					SBOM:            &AssetSpec{Asset: "sbom.json", Format: "auto"},
					Vulnerabilities: &AssetSpec{Asset: "vulnerabilities.json", Format: "auto"},
				},
				Checks:      &ChecksSpec{},
				Deployments: &DeploymentsSpec{},
			},
		},
	}
}

// PresetByKey restituisce un preset (nil se non esiste).
func PresetByKey(key string) *Preset {
	for _, p := range Presets() {
		if p.Key == key {
			return &p
		}
	}
	return nil
}

func gitOpsArgo() Definition {
	return Definition{
		Format: Format,
		Release: &ReleaseSpec{
			Environment: "prod",
			TagPrefix:   "v",
			TagFilter:   "v*",
			Record: &RecordSpec{
				Asset: "release-record.json",
				Fields: map[string]Path{
					"version":       {"version"},
					"commit":        {"commit"},
					"image":         {"image"},
					"digest":        {"digest"},
					"environment":   {"environment"},
					"change_number": {"change"},
					"change_title":  {"change_title"},
					"requested_by":  {"requested_by"},
					"approved_by":   {"gitops_pull_request.merged_by"},
					"approved_at":   {"gitops_pull_request.merged_at"},
					"pull_request":  {"gitops_pull_request.url"},
					"at":            {"gitops_pull_request.merged_at", "finalized_at"},
				},
			},
			Checks: &AssetChecksSpec{
				Asset: "ci-checks.json",
				Sources: []CheckSource{
					// Il verdetto della CI: un oggetto nome del controllo → esito.
					{Kind: "map", Path: "ci_verdict"},
					// Se il verdetto non c'è più (artifact scaduto), i check run del commit.
					{Kind: "list", Path: "check_runs", Name: "name", Outcome: "conclusion", URL: "url", OnlyIfEmpty: true},
					// Il quality gate di Sonar dell'analisi di quel commit, se presente.
					{Kind: "single", Name: "sonar", Outcome: "sonar.quality_gate"},
				},
				Outcomes: map[string]string{"OK": "passed", "ERROR": "failed", "WARN": "passed", "NONE": "not_run"},
			},
			SBOM:            &AssetSpec{Asset: "sbom.cdx.json", Format: "cyclonedx"},
			Vulnerabilities: &AssetSpec{Asset: "grype-report.json", Format: "grype"},
		},
		Checks: &ChecksSpec{Verdict: "Verdict"},
		// I deploy di questi template passano dal repository GitOps, non dalla Deployments API.
		Deployments: &DeploymentsSpec{Disabled: true},
		GitOps: &GitOpsSpec{
			Path:     "envs/{environment}",
			Files:    []string{"*.yaml", "*.yml"},
			Services: "services",
			Fields: map[string]Path{
				"version": {`annotations["ci/version"]`, "image.tag"},
				"image":   {"image.repository", "image.jfrogImage", "image.name"},
				"digest":  {"image.digest"},
				"commit":  {`annotations["ci/git-commit"]`},
			},
			EnvironmentNames: map[string]string{"production": "prod"},
		},
	}
}
