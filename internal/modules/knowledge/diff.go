package knowledge

import (
	"strings"
	"unicode"
)

// Confronto tra due versioni: diff a righe (algoritmo di Myers, O((N+M)·D)) e, per le righe
// cambiate una per una, diff a parole dentro la riga. Il risultato è già pronto da mostrare.

// DiffPart è un pezzo di una riga modificata.
type DiffPart struct {
	Op   string `json:"op"` // eq | add | del
	Text string `json:"text"`
}

// DiffLine è una riga del confronto. Op: eq, add, del, mod (riga cambiata, con Parts).
// A e B sono i numeri di riga (da 1) nella versione vecchia e nuova; 0 = assente.
type DiffLine struct {
	Op    string     `json:"op"`
	A     int        `json:"a,omitempty"`
	B     int        `json:"b,omitempty"`
	Text  string     `json:"text,omitempty"`
	Old   string     `json:"old,omitempty"`
	Parts []DiffPart `json:"parts,omitempty"`
}

// DiffStats riassume il confronto.
type DiffStats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

// maxDiffEdits: oltre questa distanza (testi completamente diversi) si rinuncia al diff
// minimo e si mostra tutto il vecchio come tolto e tutto il nuovo come aggiunto.
const maxDiffEdits = 2000

type edit struct {
	op   byte // '=', '-', '+'
	a, b int  // indici nelle sequenze (a per '=' e '-', b per '=' e '+')
}

// myers calcola lo script di modifica minimo tra a e b.
func myers(a, b []string) []edit {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	limit := max
	if limit > maxDiffEdits {
		limit = maxDiffEdits
	}
	off := limit + 1
	v := make([]int, 2*limit+3)
	// trace[d] è la finestra [-(d+1), d+1] di v prima del passo d (memoria O(D²) in int32).
	var trace [][]int32
	found := false
	for d := 0; d <= limit && !found; d++ {
		snap := make([]int32, 2*d+3)
		for k := -(d + 1); k <= d+1; k++ {
			snap[k+d+1] = int32(v[off+k])
		}
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		out := make([]edit, 0, n+m)
		for i := range a {
			out = append(out, edit{op: '-', a: i})
		}
		for j := range b {
			out = append(out, edit{op: '+', b: j})
		}
		return out
	}
	// Ricostruzione all'indietro.
	var out []edit
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		vv := trace[d]
		at := func(k int) int { return int(vv[k+d+1]) }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			out = append(out, edit{op: '=', a: x, b: y})
		}
		if d > 0 {
			if x == prevX {
				out = append(out, edit{op: '+', b: prevY})
			} else {
				out = append(out, edit{op: '-', a: prevX})
			}
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// Diff confronta due testi a righe; le righe sostituite una per una diventano "mod" con il
// confronto a parole.
func Diff(oldText, newText string) ([]DiffLine, DiffStats) {
	a, b := splitLines(oldText), splitLines(newText)
	script := myers(a, b)
	var out []DiffLine
	var st DiffStats
	for i := 0; i < len(script); {
		e := script[i]
		if e.op == '=' {
			out = append(out, DiffLine{Op: "eq", A: e.a + 1, B: e.b + 1, Text: a[e.a]})
			i++
			continue
		}
		// Blocco di modifiche consecutive: righe tolte e aggiunte.
		var dels, adds []edit
		for i < len(script) && script[i].op != '=' {
			if script[i].op == '-' {
				dels = append(dels, script[i])
			} else {
				adds = append(adds, script[i])
			}
			i++
		}
		pairs := min(len(dels), len(adds))
		for p := 0; p < pairs; p++ {
			oldL, newL := a[dels[p].a], b[adds[p].b]
			if similar(oldL, newL) {
				out = append(out, DiffLine{Op: "mod", A: dels[p].a + 1, B: adds[p].b + 1, Text: newL, Old: oldL, Parts: wordDiff(oldL, newL)})
				st.Changed++
			} else {
				out = append(out, DiffLine{Op: "del", A: dels[p].a + 1, Text: oldL}, DiffLine{Op: "add", B: adds[p].b + 1, Text: newL})
				st.Removed++
				st.Added++
			}
		}
		for _, d := range dels[pairs:] {
			out = append(out, DiffLine{Op: "del", A: d.a + 1, Text: a[d.a]})
			st.Removed++
		}
		for _, ad := range adds[pairs:] {
			out = append(out, DiffLine{Op: "add", B: ad.b + 1, Text: b[ad.b]})
			st.Added++
		}
	}
	if out == nil {
		out = []DiffLine{}
	}
	return out, st
}

// tokens divide una riga in parole, spazi e punteggiatura (ogni pezzo resta com'è).
func tokens(s string) []string {
	var out []string
	var cur []rune
	kind := 0 // 1 parola, 2 spazio
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		k := 3
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			k = 1
		case unicode.IsSpace(r):
			k = 2
		}
		if k != kind || k == 3 {
			flush()
		}
		kind = k
		cur = append(cur, r)
	}
	flush()
	return out
}

// similar: due righe sono "la stessa riga modificata" se hanno in comune almeno un terzo del testo.
func similar(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ta, tb := tokens(a), tokens(b)
	common := 0
	for _, e := range myers(ta, tb) {
		if e.op == '=' {
			common += len([]rune(ta[e.a]))
		}
	}
	total := len([]rune(a)) + len([]rune(b))
	return total > 0 && float64(2*common)/float64(total) >= 0.34
}

func wordDiff(a, b string) []DiffPart {
	ta, tb := tokens(a), tokens(b)
	var out []DiffPart
	push := func(op, t string) {
		if n := len(out); n > 0 && out[n-1].Op == op {
			out[n-1].Text += t
			return
		}
		out = append(out, DiffPart{Op: op, Text: t})
	}
	script := myers(ta, tb)
	// Un solo spazio uguale tra due cambiamenti non spezza il cambiamento: "da cambiare" →
	// "cambiata bene" si legge meglio di due sostituzioni separate.
	for i := 0; i < len(script); {
		if script[i].op == '=' {
			push("eq", ta[script[i].a])
			i++
			continue
		}
		var del, add strings.Builder
		for i < len(script) {
			e := script[i]
			if e.op == '=' {
				if strings.TrimSpace(ta[e.a]) == "" && i+1 < len(script) && script[i+1].op != '=' {
					del.WriteString(ta[e.a])
					add.WriteString(ta[e.a])
					i++
					continue
				}
				break
			}
			if e.op == '-' {
				del.WriteString(ta[e.a])
			} else {
				add.WriteString(tb[e.b])
			}
			i++
		}
		if del.Len() > 0 && strings.TrimSpace(del.String()) != "" {
			push("del", del.String())
		}
		if add.Len() > 0 && strings.TrimSpace(add.String()) != "" {
			push("add", add.String())
		}
	}
	return out
}
