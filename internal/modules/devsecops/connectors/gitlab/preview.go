package gitlab

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/gitlab/glapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Anteprima «prova il profilo su un progetto»: legge da GitLab come farebbe la
// sincronizzazione, con lo stesso codice, ma non salva nulla e non tocca i cursori. Ha la
// stessa forma dell'anteprima del connettore GitHub.

// PreviewInput sceglie progetto, profilo e tag.
type PreviewInput = ghconn.PreviewInput

// PreviewOutput è ciò che la sincronizzazione estrarrebbe.
type PreviewOutput = ghconn.PreviewOutput

// Preview prova un profilo su un progetto senza salvare.
func (s *Service) Preview(ctx context.Context, a Actor, integrationID string, in PreviewInput) (*PreviewOutput, error) {
	var it *Integration
	var sec secrets
	var def *profile.Definition
	var proj *projectRow
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
		rows, err := tx.Query(ctx, selectProject+" and r.integration_id = $1 and r.missing_since is null", integrationID)
		if err != nil {
			return err
		}
		list, err := scanProjects(rows)
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
		return nil, invalid("repository", "scegli un progetto scoperto dell'integrazione")
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
		out.Warnings = append(out.Warnings, "il progetto non è ancora associato a un'applicazione: la sincronizzazione lo salterebbe")
	}
	svc := serviceOf(proj.Project)
	out.Service = svc
	pid := proj.pid()
	fail := func(what string, err error) error {
		if _, ok := glapi.IsRateLimited(err); ok || ctx.Err() != nil {
			return &Error{Status: 503, Code: "gitlab.unavailable", Msg: err.Error()}
		}
		out.Warnings = append(out.Warnings, what+": "+err.Error())
		return nil
	}

	var rel *glapi.Release
	if in.Tag != "" {
		r, err := cl.ReleaseByTag(ctx, pid, in.Tag)
		if err != nil {
			if e := fail("Release "+in.Tag, err); e != nil {
				return nil, e
			}
		} else {
			rel = r
		}
	} else if err := cl.Releases(ctx, pid, 2, func(r glapi.Release) (bool, error) {
		if r.UpcomingRelease || !def.AcceptsTag(r.TagName, false) {
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
		out.Warnings = append(out.Warnings, "nessuna Release letta dal profilo in questo progetto")
	}
	if rel != nil {
		out.ReleaseTag = rel.TagName
		for _, l := range rel.Assets.Links {
			out.Assets = append(out.Assets, l.Name)
		}
		ro, res, _, err := s.readRelease(ctx, cl, def, svc, pid, *rel, it.Config.ReleaseEnvironment, it.Config.ArtifactJobs)
		if err != nil {
			if e := fail("Release "+rel.TagName, err); e != nil {
				return nil, e
			}
		} else {
			r := ro.Release
			out.Release = &r
			out.Warnings = append(out.Warnings, ro.Warnings...)
			for _, n := range res.FromLinks {
				out.AssetsRead = append(out.AssetsRead, "link: "+n)
			}
			for _, n := range res.FromJobs {
				out.AssetsRead = append(out.AssetsRead, "artefatto del job "+n)
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

	// Deployments e job della pipeline, con lo stesso codice della sincronizzazione.
	in2 := &syncInput{it: it, def: def, verdict: it.Config.VerdictCheck, env: it.Config.ReleaseEnvironment}
	objs, _, err := s.syncProject(ctx, cl, in2, *proj, Scope{Deployments: true, Checks: true, Full: true})
	out.Warnings = append(out.Warnings, objs.warnings...)
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
		out.Warnings = append(out.Warnings, "pipeline del branch "+proj.DefaultBranch+" non ancora conclusa, o assente: nessun esito")
	}
	if def != nil && def.GitOps != nil {
		out.Warnings = append(out.Warnings, "la sezione gitops del profilo non viene letta dal connettore GitLab: gli ambienti vengono da Environments e Deployments")
	}
	return out, nil
}
