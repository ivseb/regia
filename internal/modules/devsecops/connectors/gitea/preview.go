package gitea

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Anteprima «prova il profilo su un repository»: legge da Gitea come farebbe la
// sincronizzazione, con lo stesso codice, ma non salva nulla e non tocca i cursori. Ha la
// stessa forma dell'anteprima del connettore GitHub.

// PreviewInput sceglie repository, profilo e tag.
type PreviewInput = ghconn.PreviewInput

// PreviewOutput è ciò che la sincronizzazione estrarrebbe.
type PreviewOutput = ghconn.PreviewOutput

// Preview prova un profilo su un repository senza salvare.
func (s *Service) Preview(ctx context.Context, a Actor, integrationID string, in PreviewInput) (*PreviewOutput, error) {
	var it *Integration
	var sec secrets
	var def *profile.Definition
	var proj *repoRow
	out := &PreviewOutput{Assets: []string{}, AssetsRead: []string{}, Deployments: []contract.Deployment{}, GitOps: []contract.Deployment{}, Warnings: []string{}}
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if it, sec, err = s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		switch {
		case len(bytes.TrimSpace(in.Definition)) > 0 && string(bytes.TrimSpace(in.Definition)) != "null":
			d, ps := ghconn.ValidateDefinition(in.Definition)
			if len(ps) > 0 {
				return &Error{Status: 422, Code: "profile.invalid", Field: "definition", Msg: "profilo non valido: " + ps[0].String(), Problems: ps}
			}
			def, out.Profile = d, "bozza dell'editor (non salvata)"
		case in.ProfileID != "":
			if !validID(in.ProfileID) {
				return invalid("profile_id", "profilo inesistente")
			}
			var raw []byte
			var name string
			var v int
			if err := tx.QueryRow(ctx, `select p.name, p.current_version, v.definition from pipeline_profiles p
				join pipeline_profile_versions v on v.tenant_id = p.tenant_id and v.profile_id = p.id and v.version = p.current_version
				where p.id = $1`, in.ProfileID).Scan(&name, &v, &raw); err != nil {
				return invalid("profile_id", "profilo inesistente")
			}
			if def, err = profile.Parse(raw); err != nil {
				return err
			}
			out.Profile = fmt.Sprintf("«%s» versione %d", name, v)
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
			if r.ID == want || strings.EqualFold(r.FullName, want) || r.ExternalID == want {
				proj = &r
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if proj == nil {
		return nil, invalid("repository", "scegli un repository scoperto dell'integrazione")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cl, err := s.client(it.Config, sec)
	if err != nil {
		return nil, &Error{Status: 422, Code: "integration.config", Msg: err.Error()}
	}
	defer func() { out.Requests = cl.Requests() }()
	out.Repository = proj.FullName
	out.Associated = proj.Status == "confirmed"
	if !out.Associated {
		out.Warnings = append(out.Warnings, "il repository non è ancora associato a un'applicazione: la sincronizzazione lo salterebbe")
	}
	svc := serviceOf(proj.Repo)
	out.Service = svc
	repoName := proj.FullName
	fail := func(what string, err error) error {
		if _, ok := giteaapi.IsRateLimited(err); ok || ctx.Err() != nil {
			return &Error{Status: 503, Code: "gitea.unavailable", Msg: err.Error()}
		}
		out.Warnings = append(out.Warnings, what+": "+err.Error())
		return nil
	}

	var rel *giteaapi.Release
	if in.Tag != "" {
		r, err := cl.ReleaseByTag(ctx, repoName, in.Tag)
		if err != nil {
			if e := fail("Release "+in.Tag, err); e != nil {
				return nil, e
			}
		} else {
			rel = r
		}
	} else if err := cl.Releases(ctx, repoName, 2, func(r giteaapi.Release) (bool, error) {
		if r.Draft || !def.AcceptsRepoTag(repoName, r.TagName, r.Prerelease) {
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
		for _, l := range rel.Assets {
			out.Assets = append(out.Assets, l.Name)
		}
		ro, res, _, err := s.readRelease(ctx, cl, def, svc, repoName, *rel, it.Config.ReleaseEnvironment)
		if err != nil {
			if e := fail("Release "+rel.TagName, err); e != nil {
				return nil, e
			}
		} else {
			r := ro.Release
			out.Release = &r
			out.Warnings = append(out.Warnings, ro.Warnings...)
			for _, n := range res.FromFiles {
				out.AssetsRead = append(out.AssetsRead, "allegato: "+n)
			}
			for _, n := range res.Present {
				if !slices.Contains(out.Assets, n) {
					out.Assets = append(out.Assets, n)
				}
			}
			if ro.SBOM != nil {
				out.SBOM = &ghconn.PreviewSBOM{Format: ro.SBOM.Format, Components: profile.SBOMComponents(ro.SBOM.Document), Bytes: len(ro.SBOM.Document)}
			}
			if ro.Vulns != nil {
				pv := &ghconn.PreviewVulns{Scanner: ro.Vulns.Scanner, Total: len(ro.Vulns.Vulnerabilities), Summary: profile.Summarize(ro.Vulns.Vulnerabilities),
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

	// Stati del commit del branch predefinito, con lo stesso codice della sincronizzazione.
	in2 := &syncInput{it: it, def: def, verdict: it.Config.VerdictCheck, env: it.Config.ReleaseEnvironment}
	objs, _, err := s.syncRepo(ctx, cl, in2, *proj, Scope{Checks: true, Full: true})
	out.Warnings = append(out.Warnings, objs.warnings...)
	if err != nil {
		if e := fail("lettura", err); e != nil {
			return nil, e
		}
	}
	if objs.checkRun != nil {
		out.CheckRun, out.CheckReady = objs.checkRun, true
	} else if err == nil {
		out.Warnings = append(out.Warnings, "stati del branch "+proj.DefaultBranch+" assenti o non ancora conclusi: nessun esito")
	}
	if def != nil && def.GitOps != nil {
		out.Warnings = append(out.Warnings, "la sezione gitops del profilo non viene letta dal connettore Gitea")
	}
	return out, nil
}
