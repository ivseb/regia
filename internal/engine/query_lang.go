package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Linguaggio di query (architecture.md §6), bilingue e retrocompatibile con la prima
// versione. Grammatica:
//
//	query        = { ordinamento | clausola | testo }
//	clausola     = [ "-" ] campo ":" valore { ( "," | "oppure" | "or" ) valore }
//	campo        = parola | "nome tra virgolette"
//	valore       = [ ">" | ">=" | "<" | "<=" ] termine
//	             | [ termine ] ".." [ termine ]      intervallo, estremi inclusi
//	             | "vuoto" | "presente"
//	             | "(" query ")"                    sottoquery: gli elementi che trova
//	relazione    = [ "-" ] ( "collegata" | tipo di relazione ) ":" valore { "," valore }
//	termine      = parola | "testo tra virgolette"
//	ordinamento  = ( "ordina per" | "ordina" | "sort by" | "sort" ) ":" campo [ "asc" | "desc" ]
//	testo        = parole libere (full-text), "parola*" per i prefissi, una chiave come DEM-12
//
// Esempio:
//
//	tipo: business_request fase: Triage,Valutazione urgenza: Alta aggiornata: -7g
//	  -assegnatario: me importo: 10000..50000 registro fornitori ordina per: fase
//
// Clausola di relazione (ondata 3 dell'AI): «tipo: application collegata: (tipo: risk fase: aperta)»
// trova le applicazioni collegate a un rischio aperto; al posto di «collegata» (qualsiasi
// relazione, nei due versi) si può scrivere un tipo di relazione per chiave o per nome nel suo
// verso («dipende_da», «"è usata da"»). Una sottoquery vale anche per i campi che puntano a
// elementi: «genitore: (tipo: epic fase: aperta)», «applicazione: (tipo: application criticità: Alta)».
// Le parentesi aprono una sottoquery solo subito dopo «:» o «,»: nel testo libero restano parole.
//
// Questo file contiene solo la sintassi: campi e valori si risolvono sul tenant in
// query_plan.go (relazioni e sottoquery in query_rel.go), l'autocompletamento è in query_suggest.go.

// QueryError è un errore della query con la posizione del frammento sbagliato, in unità
// UTF-16 (gli indici delle stringhe JavaScript), così l'interfaccia lo sottolinea.
type QueryError struct {
	Msg string `json:"message"`
	Pos int    `json:"pos"`
	End int    `json:"end"`
}

func (e *QueryError) Error() string { return e.Msg }

// qerr crea un errore per il frammento src[pos:end] (indici in byte).
func qerr(src string, pos, end int, format string, args ...any) *QueryError {
	p, e := u16(src, pos), u16(src, end)
	if e <= p {
		e = p + 1
	}
	return &QueryError{Msg: fmt.Sprintf(format, args...) + fmt.Sprintf(" (posizione %d)", p+1), Pos: p, End: e}
}

// u16 converte un indice in byte nell'indice UTF-16 corrispondente.
func u16(src string, b int) int {
	if b > len(src) {
		b = len(src)
	}
	n := 0
	for _, r := range src[:b] {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// fromU16 converte un indice UTF-16 (dal browser) in byte.
func fromU16(src string, p int) int {
	n := 0
	for i, r := range src {
		if n >= p {
			return i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(src)
}

type qtokKind uint8

const (
	qWord qtokKind = iota
	qQuoted
	qColon
	qComma
	qSub // "( … )" dopo ":" o ",": sottoquery
)

// maxSubDepth è il numero massimo di sottoquery una dentro l'altra.
const maxSubDepth = 3

type qtok struct {
	kind     qtokKind
	text     string // per qQuoted: il contenuto senza virgolette
	pos, end int    // byte
	open     bool   // virgolette o parentesi non chiuse (solo in modalità tollerante)
	// in0, in1: per qSub, il testo della sottoquery dentro le parentesi (byte).
	in0, in1 int
}

func isQuerySpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// lexQuery divide la query in parole, testi tra virgolette, due punti e virgole.
// In modalità tollerante (autocompletamento) le virgolette non chiuse arrivano a fine testo.
func lexQuery(src string, tolerant bool) ([]qtok, error) { return lexRange(src, 0, len(src), tolerant) }

// lexRange divide la parte src[from:to] (le posizioni restano quelle di src). Una «(» subito
// dopo «:» o «,» apre una sottoquery fino alla «)» corrispondente (fuori dalle virgolette).
func lexRange(src string, from, to int, tolerant bool) ([]qtok, error) {
	var out []qtok
	i := from
	for i < to {
		c := src[i]
		switch {
		case isQuerySpace(c):
			i++
		case c == ':':
			out = append(out, qtok{kind: qColon, text: ":", pos: i, end: i + 1})
			i++
		case c == ',':
			out = append(out, qtok{kind: qComma, text: ",", pos: i, end: i + 1})
			i++
		case c == '(' && len(out) > 0 && (out[len(out)-1].kind == qColon || out[len(out)-1].kind == qComma):
			j, closed := matchParen(src, i, to)
			if !closed {
				if !tolerant {
					return nil, qerr(src, i, to, "parentesi non chiusa: la sottoquery finisce con «)»")
				}
				out = append(out, qtok{kind: qSub, text: src[i+1 : to], pos: i, end: to, open: true, in0: i + 1, in1: to})
				i = to
				continue
			}
			out = append(out, qtok{kind: qSub, text: src[i+1 : j], pos: i, end: j + 1, in0: i + 1, in1: j})
			i = j + 1
		case c == '"':
			j := strings.IndexByte(src[i+1:to], '"')
			if j < 0 {
				if !tolerant {
					return nil, qerr(src, i, to, "virgolette non chiuse")
				}
				out = append(out, qtok{kind: qQuoted, text: src[i+1 : to], pos: i, end: to, open: true})
				i = to
				continue
			}
			out = append(out, qtok{kind: qQuoted, text: src[i+1 : i+1+j], pos: i, end: i + j + 2})
			i += j + 2
		default:
			// I byte delle lettere accentate sono tutti >= 0x80: non si confondono con i separatori.
			j := i
			for j < to && !isQuerySpace(src[j]) && src[j] != ':' && src[j] != ',' && src[j] != '"' {
				j++
			}
			out = append(out, qtok{kind: qWord, text: src[i:j], pos: i, end: j})
			i = j
		}
	}
	return out, nil
}

// matchParen trova la «)» che chiude la «(» in src[i], fuori dalle virgolette.
func matchParen(src string, i, to int) (int, bool) {
	depth, quoted := 0, false
	for j := i; j < to; j++ {
		switch src[j] {
		case '"':
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				if depth--; depth == 0 {
					return j, true
				}
			}
		}
	}
	return to, false
}

// Query è una query analizzata (solo la sintassi).
type Query struct {
	Src     string
	Clauses []QClause
	Text    []QText
	Sort    *QSort
}

// QClause è una condizione "campo: valori"; più valori sono in alternativa.
type QClause struct {
	Name             string // come scritto, senza "-"
	Negated          bool
	Values           []QValue
	NamePos, NameEnd int // byte
	Pos, End         int
}

// QValue è un valore di una clausola.
type QValue struct {
	Op       string // eq | gt | ge | lt | le | range | empty | present | sub
	A, B     string // per range: gli estremi ("" = aperto)
	Quoted   bool
	Pos, End int
	// Sub: la sottoquery di un valore «( … )» (Op "sub"); le posizioni sono quelle di Src.
	Sub *Query
}

// QText è una parola (o una frase tra virgolette) del testo libero.
type QText struct {
	Text     string
	Quoted   bool
	Pos, End int
}

// QSort è l'ordinamento richiesto.
type QSort struct {
	Name     string
	Asc      bool
	Explicit bool // direzione scritta nella query
	Pos, End int  // del nome del campo
}

var (
	emptyWords    = map[string]bool{"vuoto": true, "vuota": true, "assente": true, "nessuno": true, "nessuna": true, "empty": true, "none": true, "null": true}
	qPresentWords = map[string]bool{"presente": true, "qualsiasi": true, "present": true, "any": true, "set": true}
	orWords       = map[string]bool{"oppure": true, "or": true}
	sortWords     = map[string]bool{"ordina": true, "ordinamento": true, "sort": true, "order": true}
	sortByWords   = map[string]bool{"per": true, "by": true}
	ascWords      = map[string]bool{"asc": true, "crescente": true, "ascendente": true, "ascending": true}
	descWords     = map[string]bool{"desc": true, "decrescente": true, "discendente": true, "descending": true}
)

// Parse analizza una query; l'errore (*QueryError) indica dove si è fermato.
func Parse(src string) (*Query, error) { return parseQuery(src, false) }

// parseQuery: in modalità tollerante restituisce comunque la parte riconosciuta e il primo errore.
func parseQuery(src string, tolerant bool) (*Query, error) {
	toks, err := lexQuery(src, tolerant)
	if err != nil {
		return &Query{Src: src}, err
	}
	return parseToks(src, toks, tolerant, 0)
}

// parseToks analizza i token di una query (depth: quante sottoquery la contengono).
func parseToks(src string, toks []qtok, tolerant bool, depth int) (*Query, error) {
	q := &Query{Src: src}
	var first error
	fail := func(e error) bool {
		if first == nil {
			first = e
		}
		return !tolerant
	}
	i := 0
	for i < len(toks) {
		t := toks[i]
		if n := sortHead(toks, i); n > 0 {
			colon := toks[i+n-1]
			i += n
			if q.Sort != nil {
				if fail(qerr(src, t.pos, colon.end, "ordinamento già indicato: se ne usa uno solo")) {
					return q, first
				}
			}
			if i >= len(toks) || (toks[i].kind != qWord && toks[i].kind != qQuoted) || isFieldStart(toks, i) {
				if fail(qerr(src, t.pos, colon.end, "manca il campo dopo «%s»", src[t.pos:colon.end])) {
					return q, first
				}
				continue
			}
			f := toks[i]
			s := &QSort{Name: f.text, Pos: f.pos, End: f.end}
			i++
			if i < len(toks) && toks[i].kind == qWord && !isFieldStart(toks, i) {
				lw := strings.ToLower(toks[i].text)
				if ascWords[lw] || descWords[lw] {
					s.Asc, s.Explicit = ascWords[lw], true
					i++
				}
			}
			if i < len(toks) && toks[i].kind == qComma {
				if fail(qerr(src, toks[i].pos, toks[i].end, "per ora si ordina per un solo campo")) {
					return q, first
				}
				i++
			}
			if q.Sort == nil {
				q.Sort = s
			}
			continue
		}
		if isFieldStart(toks, i) {
			name, neg := t.text, false
			np := t.pos
			if t.kind == qWord && strings.HasPrefix(name, "-") && len(name) > 1 {
				name, neg, np = name[1:], true, t.pos+1
			}
			if t.kind == qQuoted {
				np++
			}
			c := QClause{Name: name, Negated: neg, NamePos: np, NameEnd: t.end, Pos: t.pos, End: toks[i+1].end}
			if t.kind == qQuoted {
				c.NameEnd = t.end - 1
			}
			i += 2
			vals, j, err := parseValues(src, toks, i, strings.ToLower(name), tolerant, depth)
			if err != nil && fail(err) {
				return q, first
			}
			if len(vals) == 0 && err == nil {
				if fail(qerr(src, t.pos, toks[i-1].end, "manca il valore dopo «%s:»", t.text)) {
					return q, first
				}
			}
			c.Values = vals
			if len(vals) > 0 {
				c.End = vals[len(vals)-1].End
			}
			q.Clauses = append(q.Clauses, c)
			i = j
			continue
		}
		switch t.kind {
		case qColon:
			if fail(qerr(src, t.pos, t.end, "«:» senza un campo prima: scrivi «campo: valore»")) {
				return q, first
			}
		case qComma:
			if fail(qerr(src, t.pos, t.end, "virgola fuori da un elenco di valori: scrivi «campo: a, b»")) {
				return q, first
			}
		case qSub:
			if fail(qerr(src, t.pos, t.end, "una sottoquery tra parentesi va dopo un campo: scrivi «collegata: (tipo: risk)»")) {
				return q, first
			}
		default:
			q.Text = append(q.Text, QText{Text: t.text, Quoted: t.kind == qQuoted, Pos: t.pos, End: t.end})
		}
		i++
	}
	return q, first
}

// isFieldStart: una parola (o un nome tra virgolette) seguita da ":".
func isFieldStart(toks []qtok, i int) bool {
	return i+1 < len(toks) && (toks[i].kind == qWord || toks[i].kind == qQuoted) && toks[i+1].kind == qColon
}

// sortHead riconosce "ordina per:", "ordina:", "sort by:", "sort:" e restituisce quanti token occupa.
func sortHead(toks []qtok, i int) int {
	if i >= len(toks) || toks[i].kind != qWord || !sortWords[strings.ToLower(toks[i].text)] {
		return 0
	}
	if i+1 < len(toks) && toks[i+1].kind == qColon {
		return 2
	}
	if i+2 < len(toks) && toks[i+1].kind == qWord && sortByWords[strings.ToLower(toks[i+1].text)] && toks[i+2].kind == qColon {
		return 3
	}
	return 0
}

var opOnly = map[string]bool{">": true, ">=": true, "<": true, "<=": true, "=": true}

// parseValues legge i valori di una clausola a partire da toks[i]. field è il nome del
// campo in minuscolo: "oppure campo: valore" sullo stesso campo continua l'elenco.
func parseValues(src string, toks []qtok, i int, field string, tolerant bool, depth int) ([]QValue, int, error) {
	var vals []QValue
	expect := true
	for i < len(toks) {
		t := toks[i]
		if expect {
			if t.kind == qColon {
				return vals, i + 1, qerr(src, t.pos, t.end, "«:» inatteso: manca il valore")
			}
			if t.kind == qComma {
				return vals, i + 1, qerr(src, t.pos, t.end, "manca un valore prima della virgola")
			}
			if isFieldStart(toks, i) || sortHead(toks, i) > 0 {
				if len(vals) > 0 {
					return vals, i, qerr(src, toks[i-1].pos, toks[i-1].end, "manca un valore dopo «%s»", toks[i-1].text)
				}
				return vals, i, nil
			}
			// Operatore staccato dal valore: "importo: > 100".
			if t.kind == qWord && opOnly[t.text] && i+1 < len(toks) && (toks[i+1].kind == qWord || toks[i+1].kind == qQuoted) && !isFieldStart(toks, i+1) {
				v, err := parseValue(src, toks[i+1])
				if err != nil {
					return vals, i + 2, err
				}
				if v.Op != "eq" {
					return vals, i + 2, qerr(src, t.pos, toks[i+1].end, "operatore ripetuto")
				}
				v.Op = map[string]string{">": "gt", ">=": "ge", "<": "lt", "<=": "le", "=": "eq"}[t.text]
				v.Pos = t.pos
				vals = append(vals, v)
				i += 2
				expect = false
				continue
			}
			if t.kind == qSub {
				v, err := parseSub(src, t, tolerant, depth)
				if err != nil && (!tolerant || v.Sub == nil) {
					return vals, i + 1, err
				}
				vals = append(vals, v)
				i++
				expect = false
				continue
			}
			v, err := parseValue(src, t)
			if err != nil {
				return vals, i + 1, err
			}
			vals = append(vals, v)
			i++
			expect = false
			continue
		}
		if t.kind == qComma {
			i++
			expect = true
			continue
		}
		if t.kind == qWord && orWords[strings.ToLower(t.text)] && i+1 < len(toks) {
			// "fase: Triage oppure Valutazione" e "fase: Triage oppure fase: Valutazione".
			if isFieldStart(toks, i+1) {
				name := strings.ToLower(strings.TrimPrefix(toks[i+1].text, "-"))
				if name == field && !strings.HasPrefix(toks[i+1].text, "-") {
					i += 3
					expect = true
					continue
				}
				break
			}
			if (toks[i+1].kind == qWord || toks[i+1].kind == qQuoted) && sortHead(toks, i+1) == 0 {
				i++
				expect = true
				continue
			}
		}
		break
	}
	if expect && len(vals) > 0 {
		last := toks[i-1]
		return vals, i, qerr(src, last.pos, last.end, "manca un valore dopo «%s»", last.text)
	}
	return vals, i, nil
}

// parseSub analizza la sottoquery di un valore «( … )».
func parseSub(src string, t qtok, tolerant bool, depth int) (QValue, error) {
	v := QValue{Op: "sub", Pos: t.pos, End: t.end}
	if depth+1 >= maxSubDepth {
		return v, qerr(src, t.pos, t.end, "troppe sottoquery una dentro l'altra (al massimo %d)", maxSubDepth-1)
	}
	if strings.TrimSpace(t.text) == "" {
		return v, qerr(src, t.pos, t.end, "sottoquery vuota: scrivi le condizioni tra le parentesi, es. (tipo: risk)")
	}
	toks, err := lexRange(src, t.in0, t.in1, tolerant)
	if err != nil {
		return v, err
	}
	sub, err := parseToks(src, toks, tolerant, depth+1)
	v.Sub = sub
	if err == nil && sub.Sort != nil {
		err = qerr(src, sub.Sort.Pos, sub.Sort.End, "in una sottoquery l'ordinamento non serve")
	}
	return v, err
}

func parseValue(src string, t qtok) (QValue, error) {
	v := QValue{Op: "eq", A: t.text, Quoted: t.kind == qQuoted, Pos: t.pos, End: t.end}
	if v.Quoted {
		return v, nil
	}
	s := t.text
	lw := strings.ToLower(s)
	switch {
	case emptyWords[lw]:
		v.Op, v.A = "empty", ""
		return v, nil
	case qPresentWords[lw]:
		v.Op, v.A = "present", ""
		return v, nil
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(s, op) {
			v.Op = map[string]string{">": "gt", ">=": "ge", "<": "lt", "<=": "le", "=": "eq"}[op]
			v.A = s[len(op):]
			if v.A == "" {
				return v, qerr(src, t.pos, t.end, "manca il valore dopo «%s»", op)
			}
			return v, nil
		}
	}
	if k := strings.Index(s, ".."); k >= 0 {
		v.Op, v.A, v.B = "range", s[:k], s[k+2:]
		if v.A == "" && v.B == "" {
			return v, qerr(src, t.pos, t.end, "intervallo senza estremi: scrivi «a..b»")
		}
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Date

// span è un intervallo di tempo [lo, hi); nil = aperto.
type span struct{ lo, hi *time.Time }

var (
	isoDayRe   = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	isoMonthRe = regexp.MustCompile(`^(\d{4})-(\d{1,2})$`)
	yearRe     = regexp.MustCompile(`^(\d{4})$`)
	itDayRe    = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	offsetRe   = regexp.MustCompile(`^([+-])(\d{1,4})([gdswmay])$`)
)

// dayOf interpreta una data (assoluta, parola o spostamento) come intervallo e dice se è
// uno spostamento relativo (-1 indietro, +1 avanti, 0 altrimenti). now è nel fuso del tenant.
func dayOf(raw string, now time.Time) (lo, hi time.Time, offset int, ok bool) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	day := func(t time.Time) (time.Time, time.Time) { return t, t.AddDate(0, 0, 1) }
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "oggi", "today":
		lo, hi = day(today)
		return lo, hi, 0, true
	case "ieri", "yesterday":
		lo, hi = day(today.AddDate(0, 0, -1))
		return lo, hi, 0, true
	case "domani", "tomorrow":
		lo, hi = day(today.AddDate(0, 0, 1))
		return lo, hi, 0, true
	case "settimana", "week":
		// Settimana corrente, da lunedì.
		wd := (int(today.Weekday()) + 6) % 7
		lo = today.AddDate(0, 0, -wd)
		return lo, lo.AddDate(0, 0, 7), 0, true
	case "mese", "month":
		lo = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
		return lo, lo.AddDate(0, 1, 0), 0, true
	case "anno", "year":
		lo = time.Date(today.Year(), 1, 1, 0, 0, 0, 0, loc)
		return lo, lo.AddDate(1, 0, 0), 0, true
	}
	if m := offsetRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[2])
		sign := 1
		if m[1] == "-" {
			sign = -1
		}
		d := today
		switch m[3] {
		case "g", "d":
			d = today.AddDate(0, 0, sign*n)
		case "s", "w":
			d = today.AddDate(0, 0, sign*n*7)
		case "m":
			d = today.AddDate(0, sign*n, 0)
		case "a", "y":
			d = today.AddDate(sign*n, 0, 0)
		}
		lo, hi = day(d)
		return lo, hi, sign, true
	}
	atoi := func(x string) int { n, _ := strconv.Atoi(x); return n }
	valid := func(y, mo, d int) bool {
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
		return mo >= 1 && mo <= 12 && t.Day() == d && t.Month() == time.Month(mo)
	}
	if m := isoDayRe.FindStringSubmatch(s); m != nil && valid(atoi(m[1]), atoi(m[2]), atoi(m[3])) {
		lo, hi = day(time.Date(atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]), 0, 0, 0, 0, loc))
		return lo, hi, 0, true
	}
	if m := itDayRe.FindStringSubmatch(s); m != nil && valid(atoi(m[3]), atoi(m[2]), atoi(m[1])) {
		lo, hi = day(time.Date(atoi(m[3]), time.Month(atoi(m[2])), atoi(m[1]), 0, 0, 0, 0, loc))
		return lo, hi, 0, true
	}
	if m := isoMonthRe.FindStringSubmatch(s); m != nil && atoi(m[2]) >= 1 && atoi(m[2]) <= 12 {
		lo = time.Date(atoi(m[1]), time.Month(atoi(m[2])), 1, 0, 0, 0, 0, loc)
		return lo, lo.AddDate(0, 1, 0), 0, true
	}
	if m := yearRe.FindStringSubmatch(s); m != nil {
		lo = time.Date(atoi(m[1]), 1, 1, 0, 0, 0, 0, loc)
		return lo, lo.AddDate(1, 0, 0), 0, true
	}
	return time.Time{}, time.Time{}, 0, false
}

// dateSpan traduce un valore di data nell'intervallo da cercare:
//   - "oggi", "2026-11-24": quel giorno; "settimana", "mese", "2026-11": il periodo;
//   - "-7g": dal giorno di 7 giorni fa a oggi; "+30g": da oggi ai prossimi 30 giorni;
//   - ">x" dopo il periodo, ">=x" dal suo inizio, "<x" prima, "<=x" fino alla sua fine;
//   - "a..b" dall'inizio di a alla fine di b (estremi inclusi, anche aperti).
func dateSpan(v QValue, now time.Time) (span, bool) {
	t := func(x time.Time) *time.Time { return &x }
	if v.Op == "range" {
		var s span
		if v.A != "" {
			lo, _, _, ok := dayOf(v.A, now)
			if !ok {
				return s, false
			}
			s.lo = t(lo)
		}
		if v.B != "" {
			_, hi, _, ok := dayOf(v.B, now)
			if !ok {
				return s, false
			}
			s.hi = t(hi)
		}
		return s, true
	}
	lo, hi, off, ok := dayOf(v.A, now)
	if !ok {
		return span{}, false
	}
	switch v.Op {
	case "gt":
		return span{lo: t(hi)}, true
	case "ge":
		return span{lo: t(lo)}, true
	case "lt":
		return span{hi: t(lo)}, true
	case "le":
		return span{hi: t(hi)}, true
	}
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch off {
	case -1:
		return span{lo: t(lo), hi: t(today.AddDate(0, 0, 1))}, true
	case 1:
		return span{lo: t(today), hi: t(hi)}, true
	}
	return span{lo: t(lo), hi: t(hi)}, true
}
