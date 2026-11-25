package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Linguaggio di condizioni (sdlc.md §2): lo stesso stile "campo: valore" delle query,
// valutato in memoria sui fatti di un elemento. Si usa per gate, regole, passaggi e
// applicabilità dei processi. Esempi:
//
//	stima_max: >50000 driver: normativo
//	ruolo.sponsor: presente -stato: Sospesa
//	obbligatoria: sì oppure scadenza_normativa: <2027-01-01
//
// Clausole separate da spazi: tutte vere (e). "oppure" / "or" separa alternative.
// Più valori separati da virgola: almeno uno vero. "-" o "non" davanti nega la clausola.
// Valori speciali: presente / assente, sì / no, >N, >=N, <N, <=N (numeri o date AAAA-MM-GG).

// Facts sono i fatti di un elemento: chiave → valori (testo, numero, booleano).
// Una chiave assente o senza valori vale "assente".
type Facts map[string][]any

// Set imposta un fatto; i valori nil sono ignorati.
func (f Facts) Set(key string, vals ...any) {
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		switch x := v.(type) {
		case nil:
		case *string:
			if x != nil {
				out = append(out, *x)
			}
		case *int64:
			if x != nil {
				out = append(out, float64(*x))
			}
		case *float64:
			if x != nil {
				out = append(out, *x)
			}
		case *bool:
			if x != nil {
				out = append(out, *x)
			}
		case *time.Time:
			if x != nil {
				out = append(out, x.Format(time.DateOnly))
			}
		case int:
			out = append(out, float64(x))
		case int64:
			out = append(out, float64(x))
		case time.Time:
			out = append(out, x.Format(time.DateOnly))
		default:
			out = append(out, v)
		}
	}
	f[strings.ToLower(key)] = out
}

// Condition è una condizione già analizzata. La condizione vuota è sempre vera.
type Condition struct {
	src  string
	alts [][]clause // alternative (oppure) di clausole (e)
}

type clause struct {
	key    string
	negate bool
	values []matcher
}

type matcher struct {
	op    string // eq | present | absent | gt | ge | lt | le | bool
	raw   string
	num   float64
	isNum bool
	b     bool
}

func (c Condition) String() string { return c.src }

// Empty dice se la condizione è vuota (sempre vera).
func (c Condition) Empty() bool { return len(c.alts) == 0 }

// ParseCondition analizza una condizione; l'errore indica dove si è fermato.
func ParseCondition(src string) (Condition, error) {
	c := Condition{src: strings.TrimSpace(src)}
	sc := &scanner{rs: []rune(c.src)}
	var cur []clause
	neg := false
	for {
		sc.skipSpace()
		if sc.eof() {
			break
		}
		start := sc.pos
		word, quoted, err := sc.word()
		if err != nil {
			return c, err
		}
		sc.skipSpace()
		if quoted || !sc.peek(':') {
			switch lw := strings.ToLower(word); {
			case quoted:
				return c, fmt.Errorf("valore %q senza campo: scrivi \"campo: valore\"", word)
			case lw == "oppure" || lw == "or":
				if len(cur) == 0 || neg {
					return c, fmt.Errorf("%q senza una condizione prima", word)
				}
				c.alts = append(c.alts, cur)
				cur = nil
			case lw == "e" || lw == "and":
			case lw == "non" || lw == "not":
				neg = !neg
			default:
				return c, fmt.Errorf("attesa una clausola \"campo: valore\" alla posizione %d, trovato %q", start+1, word)
			}
			continue
		}
		sc.pos++ // ':'
		cl := clause{key: strings.ToLower(word), negate: neg}
		neg = false
		if strings.HasPrefix(cl.key, "-") {
			cl.key, cl.negate = cl.key[1:], !cl.negate
		}
		if !validKey(cl.key) {
			return c, fmt.Errorf("nome di campo %q non valido", cl.key)
		}
		for {
			sc.skipSpace()
			if sc.eof() {
				return c, fmt.Errorf("%s: manca il valore", cl.key)
			}
			v, q, err := sc.word()
			if err != nil {
				return c, err
			}
			if v == "" && !q {
				return c, fmt.Errorf("%s: manca il valore", cl.key)
			}
			if !q && (v == ">" || v == ">=" || v == "<" || v == "<=") {
				// "stima_max: > 50000": l'operatore staccato dal valore.
				sc.skipSpace()
				next, _, err := sc.word()
				if err != nil {
					return c, err
				}
				v += next
			}
			m := matcher{op: "eq", raw: v}
			if !q {
				if m, err = parseMatcher(v); err != nil {
					return c, fmt.Errorf("%s: %w", cl.key, err)
				}
			}
			cl.values = append(cl.values, m)
			sc.skipSpace()
			if !sc.peek(',') {
				break
			}
			sc.pos++
		}
		cur = append(cur, cl)
	}
	if neg {
		return c, fmt.Errorf("\"non\" senza una condizione dopo")
	}
	if len(cur) == 0 && len(c.alts) > 0 {
		return c, fmt.Errorf("\"oppure\" senza una condizione dopo")
	}
	if len(cur) > 0 {
		c.alts = append(c.alts, cur)
	}
	return c, nil
}

// MustCondition è per le condizioni scritte nel codice (pacchetti di partenza, test).
func MustCondition(src string) Condition {
	c, err := ParseCondition(src)
	if err != nil {
		panic(err)
	}
	return c
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

type scanner struct {
	rs  []rune
	pos int
}

func (s *scanner) eof() bool { return s.pos >= len(s.rs) }

func (s *scanner) peek(r rune) bool { return !s.eof() && s.rs[s.pos] == r }

func (s *scanner) skipSpace() {
	for !s.eof() && unicode.IsSpace(s.rs[s.pos]) {
		s.pos++
	}
}

// word legge una parola fino a spazio, due punti o virgola, oppure un testo tra virgolette.
// I due punti dentro un valore (es. un orario) non sono ammessi senza virgolette.
func (s *scanner) word() (string, bool, error) {
	if s.peek('"') {
		end := -1
		for j := s.pos + 1; j < len(s.rs); j++ {
			if s.rs[j] == '"' {
				end = j
				break
			}
		}
		if end < 0 {
			return "", true, fmt.Errorf("virgolette non chiuse")
		}
		w := string(s.rs[s.pos+1 : end])
		s.pos = end + 1
		return w, true, nil
	}
	start := s.pos
	for !s.eof() {
		r := s.rs[s.pos]
		if unicode.IsSpace(r) || r == ':' || r == ',' || r == '"' {
			break
		}
		s.pos++
	}
	return string(s.rs[start:s.pos]), false, nil
}

var presentWords = map[string]bool{"presente": true, "present": true, "*": true, "esiste": true, "exists": true, "valorizzato": true}
var absentWords = map[string]bool{"assente": true, "absent": true, "vuoto": true, "empty": true, "none": true, "nessuno": true}
var trueWords = map[string]bool{"sì": true, "si": true, "yes": true, "true": true, "vero": true}
var falseWords = map[string]bool{"no": true, "false": true, "falso": true}

func parseMatcher(s string) (matcher, error) {
	ls := strings.ToLower(s)
	switch {
	case presentWords[ls]:
		return matcher{op: "present"}, nil
	case absentWords[ls]:
		return matcher{op: "absent"}, nil
	case trueWords[ls]:
		return matcher{op: "bool", b: true, raw: s}, nil
	case falseWords[ls]:
		return matcher{op: "bool", b: false, raw: s}, nil
	}
	for _, op := range []struct{ p, op string }{{">=", "ge"}, {"<=", "le"}, {">", "gt"}, {"<", "lt"}} {
		if strings.HasPrefix(s, op.p) {
			v := strings.TrimSpace(s[len(op.p):])
			if v == "" {
				return matcher{}, fmt.Errorf("manca il valore dopo %q", op.p)
			}
			m := matcher{op: op.op, raw: v}
			if n, ok := parseNumber(v); ok {
				m.num, m.isNum = n, true
			} else if _, err := time.Parse(time.DateOnly, v); err != nil {
				return matcher{}, fmt.Errorf("%q non è un numero né una data AAAA-MM-GG", v)
			}
			return m, nil
		}
	}
	m := matcher{op: "eq", raw: s}
	if n, ok := parseNumber(s); ok {
		m.num, m.isNum = n, true
	}
	return m, nil
}

// parseNumber accetta 50000, 50.000 (migliaia all'italiana), 1,5 no (la virgola separa i valori), 1.5, 50k.
func parseNumber(s string) (float64, bool) {
	mult := 1.0
	ls := strings.ToLower(s)
	switch {
	case strings.HasSuffix(ls, "k"):
		mult, ls = 1_000, ls[:len(ls)-1]
	case strings.HasSuffix(ls, "m"):
		mult, ls = 1_000_000, ls[:len(ls)-1]
	}
	if parts := strings.Split(ls, "."); len(parts) > 2 || (len(parts) == 2 && len(parts[1]) == 3 && mult == 1) {
		ls = strings.ReplaceAll(ls, ".", "")
	}
	n, err := strconv.ParseFloat(ls, 64)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

// Eval valuta la condizione sui fatti.
func (c Condition) Eval(f Facts) bool {
	if len(c.alts) == 0 {
		return true
	}
	for _, alt := range c.alts {
		ok := true
		for _, cl := range alt {
			if cl.eval(f) == cl.negate {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// Keys elenca i fatti usati dalla condizione (per mostrare cosa serve e per gli avvisi).
func (c Condition) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, alt := range c.alts {
		for _, cl := range alt {
			if !seen[cl.key] {
				seen[cl.key] = true
				out = append(out, cl.key)
			}
		}
	}
	return out
}

func (cl clause) eval(f Facts) bool {
	vals := f[cl.key]
	for _, m := range cl.values {
		if m.match(vals) {
			return true
		}
	}
	return false
}

func (m matcher) match(vals []any) bool {
	switch m.op {
	case "present":
		return len(vals) > 0
	case "absent":
		return len(vals) == 0
	}
	for _, v := range vals {
		switch x := v.(type) {
		case bool:
			if m.op == "bool" && x == m.b {
				return true
			}
		case float64:
			if m.isNum && compareNum(x, m.num, m.op) {
				return true
			}
		case string:
			if m.op == "eq" {
				if strings.EqualFold(x, m.raw) {
					return true
				}
				if m.isNum {
					if n, ok := parseNumber(x); ok && n == m.num {
						return true
					}
				}
			} else if m.op != "bool" && !m.isNum && len(x) >= 10 {
				// Date AAAA-MM-GG: l'ordine lessicografico è quello cronologico.
				if compareStr(x[:10], m.raw, m.op) {
					return true
				}
			}
		}
	}
	return false
}

func compareNum(a, b float64, op string) bool {
	switch op {
	case "eq":
		return a == b
	case "gt":
		return a > b
	case "ge":
		return a >= b
	case "lt":
		return a < b
	case "le":
		return a <= b
	}
	return false
}

func compareStr(a, b, op string) bool {
	switch op {
	case "gt":
		return a > b
	case "ge":
		return a >= b
	case "lt":
		return a < b
	case "le":
		return a <= b
	}
	return false
}
