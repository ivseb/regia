package knowledge

import (
	"strings"
	"unicode/utf8"
)

// Ancore dei commenti in linea. Un'ancora è una frase (quote) con il contesto che la
// precede e la segue (prefix, suffix), la posizione nel testo di ancoraggio (pos, in
// caratteri) e quale delle occorrenze uguali è (nth). Il testo di ancoraggio è il testo
// piano della pagina con gli spazi ridotti a uno: lo stesso che vede chi seleziona una
// frase dentro un paragrafo.

const (
	// contextRunes: caratteri di contesto prima e dopo la frase.
	contextRunes = 40
	// maxQuoteRunes: lunghezza massima della frase citata.
	maxQuoteRunes = 1000
)

// AnchorText è il testo su cui si cercano le frasi citate.
func AnchorText(md string) string { return normSpace(PlainText(md)) }

// normSpace riduce ogni sequenza di spazi (a capo compresi) a uno solo.
func normSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normQuote porta una frase selezionata alla forma del testo di ancoraggio: toglie i
// marcatori del Markdown rimasti nella frase e riduce gli spazi.
func normQuote(s string) string { return normSpace(markRe.ReplaceAllString(s, "")) }

// Anchor è la posizione di una frase nel testo.
type Anchor struct {
	Quote  string
	Prefix string
	Suffix string
	Pos    int // -1 se ignota
	Nth    int
}

// Located è l'esito della ricerca: la nuova posizione, quale occorrenza è e il contesto
// aggiornato.
type Located struct {
	Pos    int
	Nth    int
	Prefix string
	Suffix string
}

// Locate cerca la frase nel testo. Se compare più volte vince l'occorrenza con più contesto
// in comune (prima e dopo); a pari merito la più vicina alla posizione precedente, poi quella
// con lo stesso numero d'ordine. Se non compare, ok è falso: il commento è orfano.
func Locate(text string, a Anchor) (Located, bool) {
	if a.Quote == "" {
		return Located{}, false
	}
	var starts []int // indici in byte
	for from := 0; from <= len(text); {
		i := strings.Index(text[from:], a.Quote)
		if i < 0 {
			break
		}
		starts = append(starts, from+i)
		_, w := utf8.DecodeRuneInString(text[from+i:])
		from += i + max(w, 1)
		if len(starts) >= 5000 {
			break
		}
	}
	if len(starts) == 0 {
		return Located{}, false
	}
	best, bestScore, bestDist := 0, -1, 0
	for n, b := range starts {
		score := commonSuffixRunes(text[:b], a.Prefix) + commonPrefixRunes(text[b+len(a.Quote):], a.Suffix)
		dist := 0
		if a.Pos >= 0 {
			dist = abs(utf8.RuneCountInString(text[:b]) - a.Pos)
		}
		better := score > bestScore ||
			score == bestScore && a.Pos >= 0 && dist < bestDist ||
			score == bestScore && a.Pos < 0 && n == a.Nth
		if bestScore < 0 || better {
			best, bestScore, bestDist = n, score, dist
		}
	}
	b := starts[best]
	return Located{
		Pos:    utf8.RuneCountInString(text[:b]),
		Nth:    best,
		Prefix: lastRunes(text[:b], contextRunes),
		Suffix: firstRunes(text[b+len(a.Quote):], contextRunes),
	}, true
}

func commonSuffixRunes(a, b string) int {
	n := 0
	for len(a) > 0 && len(b) > 0 {
		ra, wa := utf8.DecodeLastRuneInString(a)
		rb, wb := utf8.DecodeLastRuneInString(b)
		if ra != rb {
			break
		}
		a, b = a[:len(a)-wa], b[:len(b)-wb]
		n++
	}
	return n
}

func commonPrefixRunes(a, b string) int {
	n := 0
	for len(a) > 0 && len(b) > 0 {
		ra, wa := utf8.DecodeRuneInString(a)
		rb, wb := utf8.DecodeRuneInString(b)
		if ra != rb {
			break
		}
		a, b = a[wa:], b[wb:]
		n++
	}
	return n
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
