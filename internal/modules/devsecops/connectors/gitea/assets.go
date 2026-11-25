package gitea

import (
	"context"
	"fmt"
	"path"
	"strings"

	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	"regia/internal/modules/devsecops/connectors/profile"
)

// Gli allegati di una release di Gitea (SBOM, report di vulnerabilità) sono i file caricati
// con la release. Il profilo li nomina (anche con * e ?); Regia li cerca tra gli allegati
// e scarica solo quelli che il profilo vuole, fino a MaxAssetBytes ciascuno. Il token va
// solo all'istanza configurata.

func matchName(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return ok
}

// resolved sono gli allegati trovati per una release.
type resolved struct {
	Assets     map[string][]byte // nome del profilo → contenuto
	Present    []string          // nomi di tutti gli allegati della release
	FromFiles  []string          // allegati effettivamente letti
	Downloaded int
	Warnings   []string
	Incomplete bool // un file presente non si è potuto scaricare: la release si riprova
}

func (s *Service) resolveAssets(ctx context.Context, cl *giteaapi.Client, rel giteaapi.Release, def *profile.Definition) (resolved, error) {
	out := resolved{Assets: map[string][]byte{}}
	for _, a := range rel.Assets {
		out.Present = append(out.Present, a.Name)
	}
	wanted := def.AssetNames()
	if len(wanted) == 0 {
		return out, nil
	}
	max := def.MaxAssetBytes()
	for _, a := range rel.Assets {
		for _, w := range wanted {
			if _, done := out.Assets[w]; done || !matchName(w, a.Name) {
				continue
			}
			if a.Size > max {
				out.Warnings = append(out.Warnings, fmt.Sprintf("allegato %s di %s troppo grande (%d MB): ignorato", a.Name, rel.TagName, a.Size>>20))
				continue
			}
			if !cl.SameInstance(a.DownloadURL) {
				out.Warnings = append(out.Warnings, fmt.Sprintf("allegato %s di %s su un altro host: ignorato", a.Name, rel.TagName))
				continue
			}
			b, err := cl.Download(ctx, a.DownloadURL, max)
			if err != nil {
				if _, ok := giteaapi.IsRateLimited(err); ok || ctx.Err() != nil {
					return out, err
				}
				out.Warnings = append(out.Warnings, fmt.Sprintf("allegato %s di %s non scaricato: %v", a.Name, rel.TagName, err))
				out.Incomplete = true
				continue
			}
			out.Assets[w] = b
			out.FromFiles = append(out.FromFiles, a.Name)
			out.Downloaded++
		}
	}
	return out, nil
}
