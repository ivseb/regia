package knowledge

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Testo delle pagine: Markdown esteso (titoli, elenchi, tabelle, codice, citazioni, avvisi
// "> [!NOTA]"), chiavi degli elementi (DEM-12) e menzioni (@Nome). Il server non produce HTML:
// lo rende il client con escape di React. Qui servono il testo piano per la ricerca, le chiavi
// citate e i titoli per l'indice.

// maxBodyRunes: limite del testo di una pagina (circa 100 pagine A4; il JSON della richiesta sta in 1 MiB).
const maxBodyRunes = 200000

var (
	keyRe      = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}\b`)
	fenceRe    = regexp.MustCompile("^\\s*(```|~~~)")
	imageRe    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkRe     = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	headingRe  = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$`)
	calloutRe  = regexp.MustCompile(`^\s*>\s*\[!([A-Za-zÀ-ÿ]+)\]\s*`)
	tableSepRe = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	markRe     = regexp.MustCompile("[*_`~]+")
)

// PlainText riduce il Markdown a testo per l'indice full-text: niente marcatori, link e
// immagini ridotti all'etichetta, tabelle senza separatori, codice mantenuto.
func PlainText(md string) string {
	var b strings.Builder
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		if fenceRe.MatchString(line) {
			inCode = !inCode
			continue
		}
		if !inCode {
			if tableSepRe.MatchString(line) && strings.Contains(line, "-") {
				continue
			}
			line = calloutRe.ReplaceAllString(line, "")
			line = strings.TrimLeft(line, " \t>#")
			line = imageRe.ReplaceAllString(line, "$1")
			line = linkRe.ReplaceAllString(line, "$1")
			line = strings.ReplaceAll(line, "|", " ")
			line = markRe.ReplaceAllString(line, "")
			if t := strings.TrimSpace(line); len(t) > 2 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' ' {
				line = t[2:]
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// CitedKeys restituisce le chiavi degli elementi citate nel testo (fuori dai blocchi di
// codice), senza ripetizioni, al massimo 500.
func CitedKeys(md string) []string {
	seen := map[string]bool{}
	var out []string
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		if fenceRe.MatchString(line) {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		for _, k := range keyRe.FindAllString(line, -1) {
			if !seen[k] && len(out) < 500 {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// Heading è una voce dell'indice automatico.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// Headings: titoli di livello 1-4 fuori dai blocchi di codice (per l'indice e i test; il
// client lo ricalcola mentre si scrive).
func Headings(md string) []Heading {
	var out []Heading
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		if fenceRe.MatchString(line) {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil && len(m[1]) <= 4 {
			out = append(out, Heading{Level: len(m[1]), Text: markRe.ReplaceAllString(m[2], "")})
		}
	}
	return out
}

func cleanBody(md string) (string, bool) {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !utf8.ValidString(md) || utf8.RuneCountInString(md) > maxBodyRunes {
		return "", false
	}
	return strings.TrimRight(md, " \n\t"), true
}
