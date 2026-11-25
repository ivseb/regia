package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Anteprima "prova il profilo su un repository": legge da GitHub come farebbe la
// sincronizzazione, con lo stesso codice, ma non salva nulla e non tocca i cursori.

// PreviewInput sceglie repository e profilo.
type PreviewInput struct {
	// Repository: org/nome, oppure l'id di un repository scoperto.
	Repository string `json:"repository"`
	// Definition è un profilo non ancora salvato (editor); altrimenti ProfileID, altrimenti
	// il profilo dell'integrazione.
	Definition json.RawMessage `json:"definition"`
	ProfileID  string          `json:"profile_id"`
	// Tag di una Release precisa; vuoto = l'ultima che il profilo legge.
	Tag string `json:"tag"`
}

// PreviewSBOM riassume l'SBOM (il documento intero non si mostra).
type PreviewSBOM struct {
	Format     string `json:"format"`
	Components int    `json:"components"`
	Bytes      int    `json:"bytes"`
}

// PreviewVulns riassume il report delle vulnerabilità.
type PreviewVulns struct {
	Scanner string                   `json:"scanner"`
	Total   int                      `json:"total"`
	Summary contract.VulnSummary     `json:"summary"`
	Sample  []contract.Vulnerability `json:"sample"`
}

// PreviewOutput è ciò che la sincronizzazione estrarrebbe.
type PreviewOutput struct {
	Repository  string                `json:"repository"`
	Service     contract.Service      `json:"service"`
	Associated  bool                  `json:"associated"`
	Profile     string                `json:"profile"`
	ReleaseTag  string                `json:"release_tag,omitempty"`
	Assets      []string              `json:"assets"`
	AssetsRead  []string              `json:"assets_read"`
	Release     *contract.Release     `json:"release"`
	SBOM        *PreviewSBOM          `json:"sbom"`
	Vulns       *PreviewVulns         `json:"vulnerabilities"`
	CheckRun    *contract.CheckRun    `json:"check_run"`
	CheckReady  bool                  `json:"check_ready"`
	Deployments []contract.Deployment `json:"deployments"`
	GitOps      []contract.Deployment `json:"gitops"`
	Warnings    []string              `json:"warnings"`
	Requests    int                   `json:"requests"`
}

// Preview prova un profilo su un repository senza salvare.
func (s *Service) Preview(ctx context.Context, a Actor, integrationID string, in PreviewInput) (*PreviewOutput, error) {
	var it *Integration
	var sec secrets
	var def *profile.Definition
	var repo *repoRow
	out := &PreviewOutput{Assets: []string{}, AssetsRead: []string{}, Deployments: []contract.Deployment{}, GitOps: []contract.Deployment{}, Warnings: []string{}}
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if it, sec, err = s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		switch {
		case len(bytes.TrimSpace(in.Definition)) > 0 && string(bytes.TrimSpace(in.Definition)) != "null":
			d, ps := ValidateDefinition(in.Definition)
			if len(ps) > 0 {
				return profileInvalid(ps)
			}
			def, out.Profile = d, "bozza dell'editor (non salvata)"
		case in.ProfileID != "":
			p, err := s.profileByID(ctx, tx, in.ProfileID)
			if err != nil {
				return err
			}
			if def, err = profile.Parse(p.Definition); err != nil {
				return err
			}
			out.Profile = fmt.Sprintf("«%s» versione %d", p.Name, p.Version)
		default:
			var v int
			if def, v, err = definitionFor(ctx, tx, it.Config.ProfileID); err != nil {
				return err
			}
			if def != nil {
				out.Profile = fmt.Sprintf("«%s» versione %d (dell'integrazione)", deref(it.ProfileName), v)
			} else {
				out.Profile = "nessun profilo: solo i dati base delle Release (tag, data, autore)"
			}
		}
		rows, err := tx.Query(ctx, selectRepo+" and r.integration_id = $1 and r.missing_since is null", integrationID)
		if err != nil {
			return err
		}
		list, err := scanRepos(rows)
		if err != nil {
			return err
		}
		want := strings.TrimSpace(in.Repository)
		for i := range list {
			r := list[i]
			if r.ID == want || strings.EqualFold(r.FullName, want) {
				repo = &r
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, invalid("repository", "scegli un repository scoperto dell'integrazione")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cl, err := s.client(integrationID, it.Config, sec)
	if err != nil {
		return nil, &Error{Status: 422, Code: "integration.config", Msg: err.Error()}
	}
	defer func() { out.Requests = cl.Requests() }()
	out.Repository = repo.FullName
	out.Associated = repo.Status == "confirmed"
	if !out.Associated {
		out.Warnings = append(out.Warnings, "il repository non è ancora associato a un'applicazione: la sincronizzazione lo salterebbe")
	}
	svc := repo.service()
	out.Service = svc
	fail := func(what string, err error) error {
		if _, ok := ghapi.IsRateLimited(err); ok || ctx.Err() != nil {
			return &Error{Status: 503, Code: "github.unavailable", Msg: err.Error()}
		}
		out.Warnings = append(out.Warnings, what+": "+err.Error())
		return nil
	}

	// Release.
	var rel *ghapi.Release
	if in.Tag != "" {
		r, err := cl.ReleaseByTag(ctx, repo.FullName, in.Tag)
		if err != nil {
			if e := fail("Release "+in.Tag, err); e != nil {
				return nil, e
			}
		} else {
			rel = r
		}
	} else if err := cl.Releases(ctx, repo.FullName, 2, func(r ghapi.Release) (bool, error) {
		if r.Draft || !def.AcceptsTag(r.TagName, r.Prerelease) {
			return false, nil
		}
		rel = &r
		return true, nil
	}); err != nil {
		if e := fail("Release", err); e != nil {
			return nil, e
		}
	}
	if rel == nil && in.Tag == "" {
		out.Warnings = append(out.Warnings, "nessuna Release letta dal profilo in questo repository")
	}
	if rel != nil {
		out.ReleaseTag = rel.TagName
		for _, a := range rel.Assets {
			out.Assets = append(out.Assets, a.Name)
		}
		ro, _, err := s.readRelease(ctx, cl, def, svc, repo.FullName, *rel, it.Config.ReleaseEnvironment)
		if err != nil {
			if e := fail("Release "+rel.TagName, err); e != nil {
				return nil, e
			}
		} else {
			r := ro.Release
			out.Release = &r
			out.Warnings = append(out.Warnings, ro.Warnings...)
			for _, n := range def.AssetNames() {
				for _, a := range rel.Assets {
					if a.Name == n {
						out.AssetsRead = append(out.AssetsRead, n)
					}
				}
			}
			if ro.SBOM != nil {
				out.SBOM = &PreviewSBOM{Format: ro.SBOM.Format, Components: profile.SBOMComponents(ro.SBOM.Document), Bytes: len(ro.SBOM.Document)}
			}
			if ro.Vulns != nil {
				pv := &PreviewVulns{Scanner: ro.Vulns.Scanner, Total: len(ro.Vulns.Vulnerabilities), Summary: profile.Summarize(ro.Vulns.Vulnerabilities),
					Sample: ro.Vulns.Vulnerabilities}
				if len(pv.Sample) > 10 {
					pv.Sample = pv.Sample[:10]
				}
				if pv.Sample == nil {
					pv.Sample = []contract.Vulnerability{}
				}
				out.Vulns = pv
			}
		}
	}

	// Deployments API e check run, con lo stesso codice della sincronizzazione.
	in2 := &syncInput{it: it, def: def, verdict: it.Config.VerdictCheck, env: it.Config.ReleaseEnvironment}
	objs, _, warns, err := s.syncRepo(ctx, cl, in2, repoRow{Repository: repo.Repository}, Scope{Deployments: true, Checks: true, Full: true})
	out.Warnings = append(out.Warnings, warns...)
	if err != nil {
		if e := fail("lettura", err); e != nil {
			return nil, e
		}
	}
	if len(objs.deployments) > 0 {
		d := objs.deployments
		if len(d) > 10 {
			d = d[len(d)-10:]
		}
		out.Deployments = d
	}
	if objs.checkRun != nil {
		out.CheckRun, out.CheckReady = objs.checkRun, true
	} else if err == nil {
		out.Warnings = append(out.Warnings, "check run dell'ultimo commit non ancora conclusi, o assenti: nessun esito")
	}

	// Repository GitOps: i deploy di questo servizio.
	gitRepos := it.Config.GitOpsRepositories
	if len(gitRepos) == 0 && def != nil && def.GitOps != nil {
		gitRepos = def.GitOps.Repositories
	}
	if len(gitRepos) > 0 && (def == nil || def.GitOps == nil) {
		out.Warnings = append(out.Warnings, "per leggere i repository GitOps serve un profilo con la sezione gitops")
	} else if len(gitRepos) > 0 {
		only := newServiceIndex([]repoRow{{Repository: repo.Repository}})
		for _, g := range gitRepos {
			deps, _, warns, err := s.syncGitOps(ctx, cl, def.GitOps, g, gitopsCursor{}, true, only)
			for _, w := range warns {
				if !strings.Contains(w, "senza repository associato") {
					out.Warnings = append(out.Warnings, g+": "+w)
				}
			}
			if err != nil {
				if e := fail("GitOps "+g, err); e != nil {
					return nil, e
				}
				continue
			}
			out.GitOps = append(out.GitOps, deps...)
		}
		if len(out.GitOps) == 0 {
			out.Warnings = append(out.Warnings, "nessun ambiente del repository GitOps nomina il servizio "+svc.Name)
		}
	}
	return out, nil
}
