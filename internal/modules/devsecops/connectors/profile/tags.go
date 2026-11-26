package profile

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Schemi dei tag (pilota home lab, difetto 5): un repository può pubblicare più tag per versione,
// per esempio il chart e l'applicazione (chart-1.4.0 e v1.4.0). Contarli tutti come rilasci
// raddoppia la frequenza dei rilasci nelle metriche DORA. Alla scoperta si raggruppano i tag
// recenti per schema e, se ce n'è più d'uno, si chiede quale è il rilascio vero; la risposta
// diventa il tag_filter del profilo per quel repository (ReleaseSpec.TagFilters).

// TagScheme è un gruppo di tag con lo stesso prefisso davanti alla versione.
type TagScheme struct {
	// Pattern è il modello (glob di path.Match) che riconosce i tag dello schema: il prefisso e
	// poi una cifra (v[0-9]*, chart-[0-9]*, [0-9]* per i tag senza prefisso).
	Pattern  string   `json:"pattern"`
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
}

// versionStart trova l'inizio della versione: la prima sequenza di cifre seguita da .cifre
// (1.4.0, 2.1); se non ce n'è, l'ultima sequenza di cifre in fondo al tag (release-42).
var (
	dottedRe   = regexp.MustCompile(`\d+(\.\d+)+`)
	trailingRe = regexp.MustCompile(`\d+$`)
)

// SchemeOf restituisce il modello dello schema di un tag.
func SchemeOf(tag string) string {
	i := -1
	if loc := dottedRe.FindStringIndex(tag); loc != nil {
		i = loc[0]
	} else if loc := trailingRe.FindStringIndex(tag); loc != nil {
		i = loc[0]
	}
	if i < 0 {
		return escapeGlob(tag) // nessuna versione: il tag stesso
	}
	return escapeGlob(tag[:i]) + "[0-9]*"
}

func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TagSchemes raggruppa i tag per schema, dal più usato; a parità, in ordine di modello.
// Gli esempi sono i primi tre tag dello schema nell'ordine ricevuto (i più recenti).
func TagSchemes(tags []string) []TagScheme {
	idx := map[string]int{}
	var out []TagScheme
	for _, t := range tags {
		if t == "" {
			continue
		}
		p := SchemeOf(t)
		i, ok := idx[p]
		if !ok {
			i = len(out)
			idx[p] = i
			out = append(out, TagScheme{Pattern: p, Examples: []string{}})
		}
		out[i].Count++
		if len(out[i].Examples) < 3 {
			out[i].Examples = append(out[i].Examples, t)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Pattern < out[b].Pattern
	})
	return out
}

// HasTagFilter dice se il profilo ha già una scelta per il repository.
func (d *Definition) HasTagFilter(repo string) bool {
	if d == nil || d.Release == nil {
		return false
	}
	_, ok := d.Release.TagFilters[repo]
	return ok
}

// WithTagFilter restituisce una copia della definizione (nil = nessun profilo) con il
// tag_filter del repository impostato. Senza sezione release ne crea una che si comporta come
// i dati base (prefisso «v» tolto dalla versione).
func (d *Definition) WithTagFilter(repo, filter string) (*Definition, error) {
	if _, err := path.Match(filter, "v1.0.0"); err != nil {
		return nil, err
	}
	var out Definition
	if d != nil {
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		cp, err := Parse(raw)
		if err != nil {
			return nil, err
		}
		out = *cp
	}
	out.Format = Format
	if out.Release == nil {
		out.Release = &ReleaseSpec{TagPrefix: "v"}
	}
	if out.Release.TagFilters == nil {
		out.Release.TagFilters = map[string]string{}
	}
	out.Release.TagFilters[repo] = filter
	return &out, nil
}

// TagOfReleaseURL estrae il tag dall'indirizzo di una Release di GitHub o di Gitea
// (…/releases/tag/<tag>); vuoto se l'indirizzo non è di quella forma.
func TagOfReleaseURL(u string) string {
	const marker = "/releases/tag/"
	i := strings.LastIndex(u, marker)
	if i < 0 {
		return ""
	}
	t := u[i+len(marker):]
	if j := strings.IndexAny(t, "?#"); j >= 0 {
		t = t[:j]
	}
	if dec, err := url.PathUnescape(t); err == nil {
		t = dec
	}
	return t
}

// MatchTag dice se il tag rispetta il filtro (vuoto = tutti i tag).
func MatchTag(filter, tag string) bool {
	ok, _ := matchTag(filter, tag)
	return ok
}

func matchTag(filter, tag string) (bool, error) {
	if filter == "" {
		return true, nil
	}
	return path.Match(filter, tag)
}
