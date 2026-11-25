package devsecops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// SBOM: CycloneDX (JSON, 1.4-1.6) e SPDX (JSON, 2.2-2.3). Si tengono purl, nome, versione e
// licenza di ogni componente; il documento non si conserva (resta nella pipeline), se ne
// tiene lo SHA-256 per riconoscere un invio ripetuto.

// MaxComponents è il limite di componenti per SBOM (un'immagine grande ne ha qualche migliaio).
const MaxComponents = 50000

type comp struct {
	PURL, Name, Version, License string
}

// purlType è il tipo del package URL (pkg:npm/... → npm).
func purlType(p string) string {
	rest, ok := strings.CutPrefix(p, "pkg:")
	if !ok {
		return ""
	}
	t, _, _ := strings.Cut(rest, "/")
	return strings.ToLower(t)
}

// genericPURL costruisce un purl per i componenti che non lo dichiarano.
func genericPURL(name, version string) string {
	p := "pkg:generic/" + url.PathEscape(name)
	if version != "" {
		p += "@" + url.PathEscape(version)
	}
	return p
}

// purlName: nome dal purl, per i componenti senza nome.
func purlName(p string) string {
	rest, ok := strings.CutPrefix(p, "pkg:")
	if !ok {
		return p
	}
	rest, _, _ = strings.Cut(rest, "?")
	rest, _, _ = strings.Cut(rest, "@")
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		rest = rest[i+1:]
	}
	if n, err := url.PathUnescape(rest); err == nil {
		return n
	}
	return rest
}

func detectFormat(doc []byte) string {
	var probe struct {
		BOMFormat   string `json:"bomFormat"`
		SPDXVersion string `json:"spdxVersion"`
	}
	if json.Unmarshal(doc, &probe) != nil {
		return ""
	}
	switch {
	case strings.EqualFold(probe.BOMFormat, "CycloneDX"):
		return "cyclonedx"
	case probe.SPDXVersion != "":
		return "spdx"
	}
	return ""
}

type cdxComponent struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Version  string `json:"version"`
	PURL     string `json:"purl"`
	Licenses []struct {
		License *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"license"`
		Expression string `json:"expression"`
	} `json:"licenses"`
	Components []cdxComponent `json:"components"`
}

func parseCycloneDX(doc []byte) ([]comp, string, error) {
	var bom struct {
		SpecVersion string         `json:"specVersion"`
		Components  []cdxComponent `json:"components"`
	}
	if err := json.Unmarshal(doc, &bom); err != nil {
		return nil, "", fmt.Errorf("CycloneDX non valido: %w", err)
	}
	var out []comp
	var walk func([]cdxComponent)
	walk = func(cs []cdxComponent) {
		for _, c := range cs {
			if len(out) > MaxComponents {
				return
			}
			name := c.Name
			if c.Group != "" && purlType(c.PURL) != "maven" {
				name = c.Group + "/" + c.Name
			}
			lic := ""
			for _, l := range c.Licenses {
				switch {
				case l.Expression != "":
					lic = l.Expression
				case l.License != nil && l.License.ID != "":
					lic = l.License.ID
				case l.License != nil:
					lic = l.License.Name
				}
				if lic != "" {
					break
				}
			}
			out = append(out, comp{PURL: c.PURL, Name: name, Version: c.Version, License: lic})
			walk(c.Components)
		}
	}
	walk(bom.Components)
	return out, bom.SpecVersion, nil
}

func parseSPDX(doc []byte) ([]comp, string, error) {
	var d struct {
		SPDXVersion string `json:"spdxVersion"`
		Packages    []struct {
			Name             string `json:"name"`
			VersionInfo      string `json:"versionInfo"`
			LicenseConcluded string `json:"licenseConcluded"`
			LicenseDeclared  string `json:"licenseDeclared"`
			ExternalRefs     []struct {
				Category string `json:"referenceCategory"`
				Type     string `json:"referenceType"`
				Locator  string `json:"referenceLocator"`
			} `json:"externalRefs"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, "", fmt.Errorf("SPDX non valido: %w", err)
	}
	out := make([]comp, 0, len(d.Packages))
	for _, p := range d.Packages {
		c := comp{Name: p.Name, Version: p.VersionInfo}
		for _, r := range p.ExternalRefs {
			if strings.EqualFold(r.Type, "purl") {
				c.PURL = r.Locator
				break
			}
		}
		for _, l := range []string{p.LicenseConcluded, p.LicenseDeclared} {
			if l != "" && l != "NOASSERTION" && l != "NONE" {
				c.License = l
				break
			}
		}
		out = append(out, c)
	}
	return out, strings.TrimPrefix(d.SPDXVersion, "SPDX-"), nil
}

// cleanComps normalizza e deduplica per purl; senza purl se ne costruisce uno generico.
func cleanComps(in []comp) []comp {
	seen := map[string]int{}
	out := make([]comp, 0, len(in))
	for _, c := range in {
		c.Name, c.Version, c.License, c.PURL = clip(c.Name, 300), clip(c.Version, 120), clip(c.License, 200), clip(c.PURL, 1000)
		if c.PURL == "" {
			if c.Name == "" {
				continue
			}
			c.PURL = genericPURL(c.Name, c.Version)
		}
		if c.Name == "" {
			c.Name = clip(purlName(c.PURL), 300)
		}
		if i, ok := seen[c.PURL]; ok {
			if out[i].License == "" {
				out[i].License = c.License
			}
			continue
		}
		seen[c.PURL] = len(out)
		out = append(out, c)
	}
	return out
}

// upsertComponents crea i componenti mancanti e restituisce gli id per purl.
func upsertComponents(ctx context.Context, tx pgx.Tx, cs []comp) (map[string]string, error) {
	ids := map[string]string{}
	for start := 0; start < len(cs); start += 5000 {
		end := min(start+5000, len(cs))
		purls, names, versions, lics := make([]string, 0, end-start), make([]string, 0, end-start), make([]string, 0, end-start), make([]string, 0, end-start)
		for _, c := range cs[start:end] {
			purls, names, versions, lics = append(purls, c.PURL), append(names, c.Name), append(versions, c.Version), append(lics, c.License)
		}
		if _, err := tx.Exec(ctx, `insert into components (tenant_id, id, purl, name, version, license)
			select app_tenant(), gen_random_uuid(), p, n, nullif(v, ''), nullif(l, '') from unnest($1::text[], $2::text[], $3::text[], $4::text[]) t(p, n, v, l)
			on conflict (tenant_id, purl) do nothing`, purls, names, versions, lics); err != nil {
			return nil, err
		}
		// La licenza arriva spesso solo da una delle SBOM: si completa senza sovrascrivere.
		if _, err := tx.Exec(ctx, `update components c set license = t.l from unnest($1::text[], $2::text[]) t(p, l)
			where c.purl = t.p and c.license is null and t.l <> ''`, purls, lics); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, "select purl, id::text from components where purl = any($1)", purls)
		if err != nil {
			return nil, err
		}
		var p, id string
		if _, err := pgx.ForEachRow(rows, []any{&p, &id}, func() error { ids[p] = id; return nil }); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// IngestSBOM scompone la SBOM nei componenti. La stessa SBOM (servizio, versione, digest,
// formato e contenuto) ricevuta di nuovo non cambia nulla; un contenuto diverso la sostituisce.
func (s *Service) IngestSBOM(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.SBOM) error {
	_, err := s.ingestSBOM(ctx, tx, actor, in)
	return err
}

func (s *Service) ingestSBOM(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.SBOM) (*Receipt, error) {
	doc := bytes.TrimSpace(in.Document)
	if len(doc) == 0 || doc[0] != '{' {
		return nil, engine.Required("document", "serve il documento SBOM (oggetto JSON CycloneDX o SPDX)")
	}
	format := strings.ToLower(strings.TrimSpace(in.Format))
	if format == "" || format == strings.ToLower(contract.Version) {
		format = detectFormat(doc)
	}
	var cs []comp
	var spec string
	var err error
	switch format {
	case "cyclonedx":
		cs, spec, err = parseCycloneDX(doc)
	case "spdx":
		cs, spec, err = parseSPDX(doc)
	default:
		return nil, engine.InvalidValue("format", "formato della SBOM non riconosciuto (cyclonedx, spdx)")
	}
	if err != nil {
		return nil, engine.InvalidParams("validation.devsecops.sbom_invalid", "document", map[string]any{"detail": err.Error()}, "%s", err.Error())
	}
	cs = cleanComps(cs)
	if len(cs) > MaxComponents {
		return nil, engine.InvalidParams("validation.devsecops.sbom_too_many_components", "document", map[string]any{"max": MaxComponents}, "troppi componenti (massimo %d)", MaxComponents)
	}
	version, digest := clip(in.Version, 200), clip(in.Digest, 200)
	if version == "" {
		if version, err = versionOfDigest(ctx, tx, in.Service, digest); err != nil {
			return nil, err
		}
	}
	if version == "" {
		return nil, engine.Required("version", "serve la versione (o il digest di un artefatto già rilasciato)")
	}
	ref, err := s.ensureService(ctx, tx, actor, in.Service, "pipeline", true)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/sbom/"+ref.ID); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(doc)
	hash := hex.EncodeToString(sum[:])
	rc := &Receipt{Kind: "sbom", Service: ref.Name, ServiceID: ref.ID, Application: ref.AppID, Components: len(cs)}
	var id, oldHash string
	err = tx.QueryRow(ctx, "select id::text, document_sha256 from sboms where service_id = $1 and lower(version) = lower($2) and digest = $3 and format = $4",
		ref.ID, version, digest, format).Scan(&id, &oldHash)
	switch {
	case err == nil && oldHash == hash:
		rc.ID, rc.Summary = id, fmt.Sprintf("SBOM %s di %s %s già ricevuta", format, ref.Name, version)
		return rc, nil
	case err == nil:
		if _, err := tx.Exec(ctx, "delete from sbom_components where sbom_id = $1", id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "update sboms set document_sha256 = $2, spec_version = $3, components = $4, received_at = now() where id = $1",
			id, hash, nullable(spec), len(cs)); err != nil {
			return nil, err
		}
	case errors.Is(err, pgx.ErrNoRows):
		id, rc.Created = db.NewID(), true
		if _, err := tx.Exec(ctx, `insert into sboms (tenant_id, id, service_id, version, digest, format, spec_version, document_sha256, components)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, id, ref.ID, version, digest, format, nullable(spec), hash, len(cs)); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	ids, err := upsertComponents(ctx, tx, cs)
	if err != nil {
		return nil, err
	}
	compIDs := make([]string, 0, len(ids))
	for _, c := range cs {
		compIDs = append(compIDs, ids[c.PURL])
	}
	if _, err := tx.Exec(ctx, `insert into sbom_components (tenant_id, sbom_id, component_id)
		select app_tenant(), $1, c from unnest($2::uuid[]) c on conflict do nothing`, id, compIDs); err != nil {
		return nil, err
	}
	if ref.AppID != "" {
		if _, err := tx.Exec(ctx, `insert into component_usages (tenant_id, application_element_id, component_id)
			select app_tenant(), $1, c from unnest($2::uuid[]) c
			on conflict (tenant_id, application_element_id, component_id) do update set last_seen = now()`, ref.AppID, compIDs); err != nil {
			return nil, err
		}
	}
	rc.ID, rc.Changed = id, true
	rc.Summary = fmt.Sprintf("SBOM %s di %s %s: %d componenti", format, ref.Name, version, len(cs))
	return rc, auditEvent(ctx, tx, actor, "devsecops.sbom", "sbom", id, map[string]any{"service": ref.ID, "format": format,
		"components": len(cs), "sha256": hash})
}

// versionOfDigest trova la versione di un artefatto dal digest (rilasci e deploy del servizio).
func versionOfDigest(ctx context.Context, tx pgx.Tx, svc contract.Service, digest string) (string, error) {
	if digest == "" {
		return "", nil
	}
	var v string
	err := tx.QueryRow(ctx, `select v from (
		select r.version v, r.released_at at from releases r join services s on s.tenant_id = r.tenant_id and s.id = r.service_id
		where lower(s.name) = lower($1) and r.digest = $2
		union all
		select d.version, d.started_at from deployments d join services s on s.tenant_id = d.tenant_id and s.id = d.service_id
		where lower(s.name) = lower($1) and d.digest = $2) x order by at desc limit 1`, strings.TrimSpace(svc.Name), digest).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}
