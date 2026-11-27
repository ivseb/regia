package ai

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Mascheramento dei dati personali verso i fornitori esterni (replica/ai.md §7): prima di
// uscire, nomi delle persone del tenant, email, telefoni, IBAN e codici fiscali diventano
// segnaposto come [persona-1] o [email-2]. Il modello lavora con i segnaposto; la risposta e
// gli input degli strumenti tornano con i valori veri, così la persona vede i nomi e gli
// strumenti cercano quelli giusti. La tabella dei segnaposto vive solo in memoria, per una domanda.

var maskPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"email", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{"iban", regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){3,7}(?: ?[A-Z0-9]{1,3})?\b`)},
	{"codice-fiscale", regexp.MustCompile(`(?i)\b[A-Z]{6}\d{2}[A-EHLMPRST]\d{2}[A-Z]\d{3}[A-Z]\b`)},
	// Telefoni: con prefisso internazionale, o cellulari italiani (3xx). Le date (2026-11-27) no.
	{"telefono", regexp.MustCompile(`\+\d{1,3}[ .]?\d{2,4}(?:[ .]?\d{2,4}){2,4}\b|\b3\d{2}[ .]?\d{3}[ .]?\d{3,4}\b`)},
}

var tokenRe = regexp.MustCompile(`\[(persona|email|iban|codice-fiscale|telefono)-(\d+)\]`)

// Masker sostituisce i dati personali con segnaposto e li rimette nelle risposte.
type Masker struct {
	names   []string // nomi delle persone, i più lunghi prima
	toToken map[string]string
	toValue map[string]string
	counts  map[string]int
}

// NewMasker prepara il mascheramento con i nomi delle persone del tenant.
func NewMasker(names []string) *Masker {
	m := &Masker{toToken: map[string]string{}, toValue: map[string]string{}, counts: map[string]int{}}
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		// Nomi di un solo carattere o due non si mascherano (troppi falsi positivi).
		if utf8.RuneCountInString(n) >= 3 && !seen[n] {
			seen[n] = true
			m.names = append(m.names, n)
		}
	}
	sort.Slice(m.names, func(i, j int) bool { return len(m.names[i]) > len(m.names[j]) })
	return m
}

// memberNames sono i nomi delle persone del tenant (anche quelle non più attive).
func memberNames(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `select display_name from members where status <> 'anonymized' limit 20000`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (m *Masker) token(kind, value string) string {
	if t, ok := m.toToken[value]; ok {
		return t
	}
	m.counts[kind]++
	t := fmt.Sprintf("[%s-%d]", kind, m.counts[kind])
	m.toToken[value], m.toValue[t] = t, value
	return t
}

// Mask toglie i dati personali da un testo. Un Masker nil non tocca niente.
func (m *Masker) Mask(s string) string {
	if m == nil || s == "" {
		return s
	}
	for _, p := range maskPatterns {
		s = p.re.ReplaceAllStringFunc(s, func(v string) string { return m.token(p.kind, v) })
	}
	for _, n := range m.names {
		if !strings.Contains(s, n) {
			continue
		}
		s = replaceWord(s, n, m.token("persona", n))
	}
	return s
}

// replaceWord sostituisce le occorrenze di old che non sono dentro una parola più lunga.
func replaceWord(s, old, repl string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		before, after := rune(' '), rune(' ')
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(s[:i])
		}
		if i+len(old) < len(s) {
			after, _ = utf8.DecodeRuneInString(s[i+len(old):])
		}
		b.WriteString(s[:i])
		if isWordRune(before) || isWordRune(after) {
			b.WriteString(old)
		} else {
			b.WriteString(repl)
		}
		s = s[i+len(old):]
	}
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 && r != '«' && r != '»' && r != '’'
}

// Unmask rimette i valori veri al posto dei segnaposto.
func (m *Masker) Unmask(s string) string {
	if m == nil || s == "" || !strings.Contains(s, "[") {
		return s
	}
	return tokenRe.ReplaceAllStringFunc(s, func(t string) string {
		if v, ok := m.toValue[t]; ok {
			return v
		}
		return t
	})
}

// Count è il numero di valori mascherati finora (per i test e il registro).
func (m *Masker) Count() int {
	if m == nil {
		return 0
	}
	return len(m.toToken)
}
