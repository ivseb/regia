package grc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"regia/internal/engine"
)

// Scambio in OSCAL (modello "catalog" di NIST OSCAL 1.1): capi e allegati diventano gruppi,
// le altre voci controlli annidati; il riferimento sta nella proprietà "label", il tipo di
// voce nella classe, la sintesi nella parte "statement". Chiave, tipo e giurisdizione della
// normativa stanno in proprietà con lo spazio dei nomi di Regia, così l'import ritrova la
// normativa.

const oscalNS = "urn:regia:grc"
const oscalVersion = "1.1.2"

type oscalDoc struct {
	Catalog oscalCatalog `json:"catalog"`
}

type oscalCatalog struct {
	UUID     string         `json:"uuid"`
	Metadata oscalMetadata  `json:"metadata"`
	Groups   []oscalGroup   `json:"groups,omitempty"`
	Controls []oscalControl `json:"controls,omitempty"`
}

type oscalMetadata struct {
	Title        string      `json:"title"`
	LastModified string      `json:"last-modified"`
	Version      string      `json:"version"`
	OSCALVersion string      `json:"oscal-version"`
	Published    string      `json:"published,omitempty"`
	Props        []oscalProp `json:"props,omitempty"`
	Links        []oscalLink `json:"links,omitempty"`
	Remarks      string      `json:"remarks,omitempty"`
}

type oscalProp struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	NS    string `json:"ns,omitempty"`
	Class string `json:"class,omitempty"`
}

type oscalLink struct {
	Href string `json:"href"`
	Rel  string `json:"rel,omitempty"`
}

type oscalPart struct {
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name"`
	Prose string      `json:"prose,omitempty"`
	Parts []oscalPart `json:"parts,omitempty"`
}

type oscalGroup struct {
	ID       string         `json:"id,omitempty"`
	Class    string         `json:"class,omitempty"`
	Title    string         `json:"title"`
	Props    []oscalProp    `json:"props,omitempty"`
	Parts    []oscalPart    `json:"parts,omitempty"`
	Groups   []oscalGroup   `json:"groups,omitempty"`
	Controls []oscalControl `json:"controls,omitempty"`
}

type oscalControl struct {
	ID       string         `json:"id"`
	Class    string         `json:"class,omitempty"`
	Title    string         `json:"title"`
	Props    []oscalProp    `json:"props,omitempty"`
	Parts    []oscalPart    `json:"parts,omitempty"`
	Controls []oscalControl `json:"controls,omitempty"`
}

var tokenBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// oscalID rende un riferimento un token OSCAL (lettera iniziale, poi lettere, cifre . - _).
func oscalID(ref string, used map[string]bool) string {
	id := strings.Trim(tokenBad.ReplaceAllString(strings.ToLower(ref), "-"), "-.")
	if id == "" || !(id[0] >= 'a' && id[0] <= 'z') {
		id = "r-" + id
	}
	base, n := id, 2
	for used[id] {
		id = fmt.Sprintf("%s-%d", base, n)
		n++
	}
	used[id] = true
	return id
}

func uuidFrom(s string) string {
	h := sha256.Sum256([]byte(s))
	h[6] = (h[6] & 0x0f) | 0x50 // versione 5 (deterministico)
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// ToOSCAL converte il catalogo nel modello catalog di OSCAL (JSON indentato).
func ToOSCAL(c *CatalogFile, now time.Time) ([]byte, error) {
	md := oscalMetadata{Title: c.Framework.Name, LastModified: now.UTC().Format(time.RFC3339), Version: c.Version.Version, OSCALVersion: oscalVersion,
		Remarks: c.Version.LicenseNote}
	if c.Version.EffectiveFrom != "" {
		md.Published = c.Version.EffectiveFrom + "T00:00:00Z"
	}
	md.Props = append(md.Props, oscalProp{Name: "framework-key", Value: c.Framework.Key, NS: oscalNS},
		oscalProp{Name: "framework-kind", Value: c.Framework.Kind, NS: oscalNS})
	if c.Framework.Jurisdiction != "" {
		md.Props = append(md.Props, oscalProp{Name: "jurisdiction", Value: c.Framework.Jurisdiction, NS: oscalNS})
	}
	if c.Framework.Publisher != "" {
		md.Props = append(md.Props, oscalProp{Name: "publisher", Value: c.Framework.Publisher, NS: oscalNS})
	}
	if c.Framework.Description != "" {
		md.Props = append(md.Props, oscalProp{Name: "description", Value: c.Framework.Description, NS: oscalNS})
	}
	if c.Version.SourceURL != "" {
		md.Links = append(md.Links, oscalLink{Href: c.Version.SourceURL, Rel: "source"})
	}
	children := map[string][]CatalogItem{}
	var roots []CatalogItem
	for _, it := range c.Items {
		if it.Parent == "" {
			roots = append(roots, it)
		} else {
			children[it.Parent] = append(children[it.Parent], it)
		}
	}
	used := map[string]bool{}
	props := func(it CatalogItem) []oscalProp {
		ps := []oscalProp{{Name: "label", Value: it.Ref}}
		if it.Kind != "" {
			ps = append(ps, oscalProp{Name: "item-kind", Value: it.Kind, NS: oscalNS})
		}
		return ps
	}
	parts := func(id string, it CatalogItem) []oscalPart {
		if strings.TrimSpace(it.Summary) == "" {
			return nil
		}
		return []oscalPart{{ID: id + "_smt", Name: "statement", Prose: it.Summary}}
	}
	var control func(it CatalogItem) oscalControl
	control = func(it CatalogItem) oscalControl {
		id := oscalID(it.Ref, used)
		oc := oscalControl{ID: id, Class: it.Kind, Title: it.Title, Props: props(it), Parts: parts(id, it)}
		for _, ch := range children[it.Ref] {
			oc.Controls = append(oc.Controls, control(ch))
		}
		return oc
	}
	var group func(it CatalogItem) oscalGroup
	group = func(it CatalogItem) oscalGroup {
		id := oscalID(it.Ref, used)
		g := oscalGroup{ID: id, Class: it.Kind, Title: it.Title, Props: props(it), Parts: parts(id, it)}
		for _, ch := range children[it.Ref] {
			if groupingKind(ch.Kind) {
				g.Groups = append(g.Groups, group(ch))
			} else {
				g.Controls = append(g.Controls, control(ch))
			}
		}
		return g
	}
	cat := oscalCatalog{UUID: uuidFrom("regia:" + c.Framework.Key + ":" + c.Version.Version), Metadata: md}
	for _, it := range roots {
		if groupingKind(it.Kind) {
			cat.Groups = append(cat.Groups, group(it))
		} else {
			cat.Controls = append(cat.Controls, control(it))
		}
	}
	return json.MarshalIndent(oscalDoc{Catalog: cat}, "", "  ")
}

// FromOSCAL legge un catalogo OSCAL (JSON). meta completa ciò che il file non dice: chiave
// della normativa (obbligatoria se manca la proprietà framework-key), nome, tipo, versione.
func FromOSCAL(raw []byte, meta FrameworkMeta, version string) (*CatalogFile, error) {
	var doc oscalDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return nil, engine.InvalidValue("content", "OSCAL non valido: %v", err)
	}
	cat := doc.Catalog
	if cat.Metadata.Title == "" && len(cat.Groups) == 0 && len(cat.Controls) == 0 {
		return nil, engine.InvalidCode("validation.grc.oscal_no_catalog", "content", "il file non contiene un catalogo OSCAL (oggetto \"catalog\")")
	}
	c := &CatalogFile{Format: CatalogFormat, Framework: meta}
	for _, p := range cat.Metadata.Props {
		if p.NS != oscalNS {
			continue
		}
		switch p.Name {
		case "framework-key":
			if c.Framework.Key == "" {
				c.Framework.Key = p.Value
			}
		case "framework-kind":
			if c.Framework.Kind == "" {
				c.Framework.Kind = p.Value
			}
		case "jurisdiction":
			if c.Framework.Jurisdiction == "" {
				c.Framework.Jurisdiction = p.Value
			}
		case "publisher":
			if c.Framework.Publisher == "" {
				c.Framework.Publisher = p.Value
			}
		case "description":
			if c.Framework.Description == "" {
				c.Framework.Description = p.Value
			}
		}
	}
	if c.Framework.Name == "" {
		c.Framework.Name = cat.Metadata.Title
	}
	c.Version.Version = version
	if c.Version.Version == "" {
		c.Version.Version = cat.Metadata.Version
	}
	c.Version.LicenseNote = cat.Metadata.Remarks
	if len(cat.Metadata.Published) >= 10 {
		c.Version.EffectiveFrom = cat.Metadata.Published[:10]
	}
	for _, l := range cat.Metadata.Links {
		if l.Rel == "source" || c.Version.SourceURL == "" {
			c.Version.SourceURL = l.Href
		}
	}
	label := func(id string, ps []oscalProp) (string, string) {
		ref, kind := "", ""
		for _, p := range ps {
			if p.Name == "label" && p.NS == "" && ref == "" {
				ref = p.Value
			}
			if p.Name == "item-kind" && p.NS == oscalNS {
				kind = p.Value
			}
		}
		if ref == "" {
			ref = id
		}
		return ref, kind
	}
	prose := func(ps []oscalPart) string {
		var b []string
		var walk func([]oscalPart)
		walk = func(ps []oscalPart) {
			for _, p := range ps {
				if p.Name == "statement" || p.Name == "item" || p.Name == "overview" {
					if t := strings.TrimSpace(p.Prose); t != "" {
						b = append(b, t)
					}
					walk(p.Parts)
				}
			}
		}
		walk(ps)
		s := strings.Join(b, " ")
		if len(s) > 2000 {
			s = s[:2000]
		}
		return s
	}
	validKind := func(k, def string) string {
		for _, x := range itemKinds {
			if x == k {
				return k
			}
		}
		return def
	}
	var addControl func(oc oscalControl, parent string)
	addControl = func(oc oscalControl, parent string) {
		ref, kind := label(oc.ID, oc.Props)
		if kind == "" {
			kind = validKind(oc.Class, "control")
		}
		c.Items = append(c.Items, CatalogItem{Ref: ref, Parent: parent, Kind: kind, Title: oc.Title, Summary: prose(oc.Parts)})
		for _, ch := range oc.Controls {
			addControl(ch, ref)
		}
	}
	var addGroup func(g oscalGroup, parent string)
	addGroup = func(g oscalGroup, parent string) {
		ref, kind := label(g.ID, g.Props)
		if kind == "" {
			kind = validKind(g.Class, "chapter")
		}
		if ref == "" {
			ref = g.Title
		}
		c.Items = append(c.Items, CatalogItem{Ref: ref, Parent: parent, Kind: kind, Title: g.Title, Summary: prose(g.Parts)})
		for _, ch := range g.Groups {
			addGroup(ch, ref)
		}
		for _, ch := range g.Controls {
			addControl(ch, ref)
		}
	}
	for _, g := range cat.Groups {
		addGroup(g, "")
	}
	for _, oc := range cat.Controls {
		addControl(oc, "")
	}
	if c.Framework.Key == "" {
		return nil, engine.Required("framework.key", "indica la chiave della normativa (il file OSCAL non la contiene)")
	}
	return c, c.Validate()
}
