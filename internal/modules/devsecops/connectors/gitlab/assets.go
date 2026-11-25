package gitlab

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"regia/internal/modules/devsecops/connectors/gitlab/glapi"
	"regia/internal/modules/devsecops/connectors/profile"
)

// Gli artefatti di una Release di GitLab stanno in due posti: i link della Release (un
// pacchetto generico, un file di un job pubblicato con release-cli) e gli artefatti dei
// job della pipeline del tag. Il profilo nomina i file (anche con * e ?, per esempio
// gl-sbom-*.cdx.json come li chiama GitLab); Regia li cerca prima tra i link, poi negli
// archivi dei job della pipeline del tag, scaricandone al massimo maxArchives.

const maxArchives = 12

func matchName(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return ok
}

// matchesAny dice se il nome di un job passa i modelli (vuoto = tutti).
func matchesAny(globs []string, name string) bool {
	if len(globs) == 0 {
		return true
	}
	return slices.ContainsFunc(globs, func(g string) bool { return matchName(g, name) })
}

// resolveAssets cerca gli allegati del profilo. Restituisce i contenuti per nome del
// profilo, i nomi di tutti i file visti (per gli avvisi e l'anteprima) e quante cose ha
// scaricato. missing: gli allegati voluti non trovati.
type resolved struct {
	Assets     map[string][]byte
	Present    []string
	FromLinks  []string
	FromJobs   []string
	Archives   int
	Warnings   []string
	Incomplete bool // un file presente non si è potuto scaricare: la Release si riprova
}

func (s *Service) resolveAssets(ctx context.Context, cl *glapi.Client, project int64, rel glapi.Release, def *profile.Definition,
	jobGlobs []string) (resolved, error) {
	out := resolved{Assets: map[string][]byte{}}
	wanted := def.AssetNames()
	if len(wanted) == 0 {
		return out, nil
	}
	max := def.MaxAssetBytes()
	for _, l := range rel.Assets.Links {
		out.Present = append(out.Present, l.Name)
		for _, w := range wanted {
			if _, done := out.Assets[w]; done || !matchName(w, l.Name) {
				continue
			}
			b, err := cl.Download(ctx, l.Download(), max)
			if err != nil {
				if _, ok := glapi.IsRateLimited(err); ok || ctx.Err() != nil {
					return out, err
				}
				out.Warnings = append(out.Warnings, fmt.Sprintf("link %s di %s non scaricato: %v", l.Name, rel.TagName, err))
				out.Incomplete = true
				continue
			}
			out.Assets[w] = b
			out.FromLinks = append(out.FromLinks, l.Name)
		}
	}
	missing := func() []string {
		var m []string
		for _, w := range wanted {
			if _, ok := out.Assets[w]; !ok {
				m = append(m, w)
			}
		}
		return m
	}
	if len(missing()) == 0 {
		return out, nil
	}

	// Artefatti dei job della pipeline del tag.
	pls, err := cl.PipelinesForRef(ctx, project, rel.TagName, 5)
	if err != nil {
		return out, fmt.Errorf("pipeline del tag %s: %w", rel.TagName, err)
	}
	var pl *glapi.Pipeline
	for i := range pls {
		if pls[i].Finished() && pls[i].Status != "skipped" {
			pl = &pls[i]
			break
		}
	}
	if pl == nil {
		for i := range pls {
			if !pls[i].Finished() {
				out.Incomplete = true // la pipeline del tag è ancora in corso: gli artefatti arriveranno
			}
		}
		if len(pls) == 0 && time.Since(rel.At()) < 24*time.Hour {
			out.Incomplete = true // la pipeline del tag può non essere ancora partita
		}
		return out, nil
	}
	jobs, err := cl.Jobs(ctx, project, pl.ID)
	if err != nil {
		return out, fmt.Errorf("job della pipeline %d: %w", pl.ID, err)
	}
	for _, j := range jobs {
		if len(missing()) == 0 || out.Archives >= maxArchives {
			break
		}
		if !j.HasArtifacts() || j.Status != "success" && !j.AllowFailure || !matchesAny(jobGlobs, j.Name) {
			continue
		}
		if j.ArtifactsFile.Size > 4*max {
			out.Warnings = append(out.Warnings, fmt.Sprintf("artefatti del job %s troppo grandi (%d MB): ignorati", j.Name, j.ArtifactsFile.Size>>20))
			continue
		}
		zb, err := cl.JobArtifacts(ctx, project, j.ID, 4*max)
		out.Archives++
		if err != nil {
			if _, ok := glapi.IsRateLimited(err); ok || ctx.Err() != nil {
				return out, err
			}
			if glapi.IsNotFound(err) { // artefatti scaduti
				continue
			}
			out.Warnings = append(out.Warnings, fmt.Sprintf("artefatti del job %s non scaricati: %v", j.Name, err))
			out.Incomplete = true
			continue
		}
		files, err := readZip(zb, missing(), max)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("artefatti del job %s non leggibili: %v", j.Name, err))
			continue
		}
		for name, b := range files {
			if _, ok := out.Assets[name]; !ok {
				out.Assets[name] = b
				out.FromJobs = append(out.FromJobs, j.Name+": "+name)
			}
		}
		for _, n := range zipNames(zb) {
			out.Present = append(out.Present, n)
		}
	}
	return out, nil
}

// readZip estrae dall'archivio i file che corrispondono ai modelli indicati (per nome del
// file, senza cartelle): restituisce modello → contenuto. Il primo che corrisponde vince.
func readZip(zb []byte, patterns []string, max int64) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		for _, p := range patterns {
			if _, done := out[p]; done || !matchName(p, base) {
				continue
			}
			if int64(f.UncompressedSize64) > max {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(io.LimitReader(rc, max+1))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if int64(len(b)) > max {
				continue
			}
			out[p] = b
		}
	}
	return out, nil
}

func zipNames(zb []byte) []string {
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && len(out) < 50 {
			out = append(out, path.Base(f.Name))
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Report di sicurezza nel formato di GitLab
// ---------------------------------------------------------------------

// I report di Dependency Scanning e Container Scanning di GitLab (gl-*-report.json) hanno
// un formato loro (vulnerabilities[].identifiers, location.dependency). Prima di passarli
// al profilo si traducono nel JSON del contratto, che il profilo legge già.

var fixRe = regexp.MustCompile(`(?i)\b(?:upgrade|update)\b.*?\bto\b\s+(?:version\s+)?v?([0-9][^\s,;]*)`)

type glReport struct {
	Scan struct {
		Scanner struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"scanner"`
		EndTime string `json:"end_time"`
	} `json:"scan"`
	Vulnerabilities []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Severity    string `json:"severity"`
		Solution    string `json:"solution"`
		Identifiers []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
			URL   string `json:"url"`
		} `json:"identifiers"`
		Location struct {
			Image      string `json:"image"`
			Dependency struct {
				Package struct {
					Name string `json:"name"`
				} `json:"package"`
				Version string `json:"version"`
			} `json:"dependency"`
		} `json:"location"`
		Links []struct {
			URL string `json:"url"`
		} `json:"links"`
	} `json:"vulnerabilities"`
}

// isGitLabReport riconosce il formato dei report di sicurezza di GitLab.
func isGitLabReport(raw []byte) bool {
	var top map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(raw), &top) != nil {
		return false
	}
	_, scan := top["scan"]
	_, vul := top["vulnerabilities"]
	_, ver := top["version"]
	return scan && vul && ver
}

// convertGitLabReport traduce un report di GitLab nel JSON del contratto.
func convertGitLabReport(raw []byte) ([]byte, error) {
	var r glReport
	if err := json.Unmarshal(bytes.TrimSpace(raw), &r); err != nil {
		return nil, fmt.Errorf("report di GitLab non leggibile: %v", err)
	}
	scanner := strings.ToLower(r.Scan.Scanner.ID)
	switch scanner {
	case "trivy", "grype", "osv":
	default:
		scanner = "other"
	}
	type vul struct {
		ID       string   `json:"id"`
		Aliases  []string `json:"aliases,omitempty"`
		Package  string   `json:"package"`
		Version  string   `json:"version,omitempty"`
		FixedIn  string   `json:"fixed_in,omitempty"`
		Severity string   `json:"severity"`
		Layer    string   `json:"layer,omitempty"`
		URL      string   `json:"url,omitempty"`
	}
	out := struct {
		Scanner         string `json:"scanner"`
		ScannedAt       string `json:"scanned_at,omitempty"`
		Vulnerabilities []vul  `json:"vulnerabilities"`
	}{Scanner: scanner, Vulnerabilities: []vul{}}
	if t, err := time.Parse("2006-01-02T15:04:05", r.Scan.EndTime); err == nil {
		out.ScannedAt = t.UTC().Format(time.RFC3339)
	} else if t, err := time.Parse(time.RFC3339, r.Scan.EndTime); err == nil {
		out.ScannedAt = t.UTC().Format(time.RFC3339)
	}
	for _, v := range r.Vulnerabilities {
		x := vul{Package: v.Location.Dependency.Package.Name, Version: v.Location.Dependency.Version, Layer: "app"}
		if v.Location.Image != "" {
			x.Layer = "image"
		}
		var ids []string
		for _, id := range v.Identifiers {
			t := strings.ToLower(id.Type)
			if (t == "cve" || t == "ghsa" || t == "osv") && id.Value != "" {
				ids = append(ids, id.Value)
				if x.URL == "" {
					x.URL = id.URL
				}
			}
		}
		slices.SortStableFunc(ids, func(a, b string) int { // le CVE prima
			return boolInt(strings.HasPrefix(b, "CVE-")) - boolInt(strings.HasPrefix(a, "CVE-"))
		})
		switch {
		case len(ids) > 0:
			x.ID, x.Aliases = ids[0], ids[1:]
		case v.Name != "":
			x.ID = v.Name
		default:
			continue
		}
		if x.Package == "" {
			x.Package = "sconosciuto"
		}
		if x.URL == "" && len(v.Links) > 0 {
			x.URL = v.Links[0].URL
		}
		if m := fixRe.FindStringSubmatch(v.Solution); m != nil {
			x.FixedIn = strings.TrimRight(m[1], ".")
		}
		switch strings.ToLower(v.Severity) {
		case "critical":
			x.Severity = "critical"
		case "high":
			x.Severity = "high"
		case "medium":
			x.Severity = "medium"
		case "low", "info":
			x.Severity = "low"
		default:
			x.Severity = "unknown"
		}
		out.Vulnerabilities = append(out.Vulnerabilities, x)
	}
	return json.Marshal(out)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// withGitLabReports traduce i report di GitLab tra gli allegati e, se è il caso, restituisce
// un profilo che li legge come JSON del contratto (il profilo salvato non cambia).
func withGitLabReports(def *profile.Definition, assets map[string][]byte, warn func(string, ...any)) *profile.Definition {
	if def == nil || def.Release == nil || def.Release.Vulnerabilities == nil {
		return def
	}
	name := def.Release.Vulnerabilities.Asset
	raw, ok := assets[name]
	if !ok || !isGitLabReport(raw) {
		return def
	}
	b, err := convertGitLabReport(raw)
	if err != nil {
		warn("%s: %v", name, err)
		return def
	}
	assets[name] = b
	d2, r2, v2 := *def, *def.Release, *def.Release.Vulnerabilities
	v2.Format = "contract"
	r2.Vulnerabilities = &v2
	d2.Release = &r2
	return &d2
}
