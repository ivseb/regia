package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func vals(c QClause) []string {
	var out []string
	for _, v := range c.Values {
		s := v.Op + ":" + v.A
		if v.Op == "range" {
			s += ".." + v.B
		}
		out = append(out, s)
	}
	return out
}

func TestParseCompat(t *testing.T) {
	// La sintassi della prima versione resta valida.
	q, err := Parse(`tipo: business_request stato: Triage, "In esecuzione" registro fornitori ordina per: creazione asc`)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Clauses) != 2 || q.Clauses[0].Name != "tipo" || q.Clauses[1].Name != "stato" {
		t.Fatalf("clausole: %+v", q.Clauses)
	}
	if got := vals(q.Clauses[1]); !reflect.DeepEqual(got, []string{"eq:Triage", "eq:In esecuzione"}) {
		t.Errorf("valori: %v", got)
	}
	if !q.Clauses[1].Values[1].Quoted {
		t.Error("valore tra virgolette non marcato")
	}
	if len(q.Text) != 2 || q.Text[0].Text != "registro" || q.Text[1].Text != "fornitori" {
		t.Errorf("testo: %+v", q.Text)
	}
	if q.Sort == nil || q.Sort.Name != "creazione" || !q.Sort.Asc || !q.Sort.Explicit {
		t.Errorf("ordinamento: %+v", q.Sort)
	}

	q, err = Parse("type: risk state: open assignee: me sort by: updated desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Clauses) != 3 || q.Sort == nil || q.Sort.Name != "updated" || q.Sort.Asc {
		t.Errorf("inglese: %+v %+v", q.Clauses, q.Sort)
	}
}

func TestParseOperators(t *testing.T) {
	q, err := Parse(`importo: >1000, <=10 creata: 2026-01-01..2026-03-31 aggiornata: -7g..oggi -assegnatario: me ` +
		`genitore: vuoto scadenza: presente fase: Triage oppure Valutazione oppure fase: Bozza "data desiderata": ..2027-01-01 punti: > 5`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"gt:1000", "le:10"},
		{"range:2026-01-01..2026-03-31"},
		{"range:-7g..oggi"},
		{"eq:me"},
		{"empty:"},
		{"present:"},
		{"eq:Triage", "eq:Valutazione", "eq:Bozza"},
		{"range:..2027-01-01"},
		{"gt:5"},
	}
	if len(q.Clauses) != len(want) {
		t.Fatalf("clausole: %d, attese %d: %+v", len(q.Clauses), len(want), q.Clauses)
	}
	for i, w := range want {
		if got := vals(q.Clauses[i]); !reflect.DeepEqual(got, w) {
			t.Errorf("clausola %d (%s): %v, attesi %v", i, q.Clauses[i].Name, got, w)
		}
	}
	if !q.Clauses[3].Negated || q.Clauses[3].Name != "assegnatario" || q.Clauses[3].NamePos != strings.Index(q.Src, "assegnatario") {
		t.Errorf("negazione: %+v", q.Clauses[3])
	}
	if q.Clauses[7].Name != "data desiderata" {
		t.Errorf("campo tra virgolette: %q", q.Clauses[7].Name)
	}
	if len(q.Text) != 0 {
		t.Errorf("testo inatteso: %+v", q.Text)
	}
	// "oppure" senza un valore dopo resta testo libero; -parola è testo (esclusione full-text).
	q, err = Parse("fase: Triage -sanzioni prefisso* oppure")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Text) != 3 || q.Text[0].Text != "-sanzioni" || q.Text[1].Text != "prefisso*" {
		t.Errorf("testo: %+v", q.Text)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		q, frag, msg string
	}{
		{"fase:", "fase:", "manca il valore"},
		{"fase: Triage,", ",", "manca un valore dopo"},
		{`titolo "aperto`, `"aperto`, "virgolette non chiuse"},
		{"fase: : x", ":", "inatteso"},
		{": x", ":", "senza un campo"},
		{"a , b", ",", "virgola fuori"},
		{"ordina per:", "ordina per:", "manca il campo"},
		{"ordina per: fase ordina per: titolo", "ordina per:", "già indicato"},
		{"importo: >", ">", "manca il valore dopo"},
		{"ordina per: fase, titolo", ",", "un solo campo"},
		{"creata: ..", "..", "senza estremi"},
	}
	for _, c := range cases {
		_, err := Parse(c.q)
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Errorf("%q: errore %v, atteso QueryError", c.q, err)
			continue
		}
		if !strings.Contains(qe.Msg, c.msg) || !strings.Contains(qe.Msg, "posizione") {
			t.Errorf("%q: messaggio %q, atteso %q", c.q, qe.Msg, c.msg)
		}
		at := strings.LastIndex(c.q, c.frag) // il frammento sbagliato è l'ultimo con quel testo
		if qe.Pos != at {
			t.Errorf("%q: posizione %d, attesa %d (%q)", c.q, qe.Pos, at, c.frag)
		}
	}
}

func TestPositionsUTF16(t *testing.T) {
	// Le posizioni sono in unità UTF-16, come gli indici delle stringhe JavaScript.
	src := "perché 🚀 fase:"
	_, err := Parse(src)
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatal(err)
	}
	if qe.Pos != 10 || qe.End != 15 { // "perché " = 7, razzo = 2 unità, spazio = 1
		t.Errorf("posizione %d-%d, attesa 10-15", qe.Pos, qe.End)
	}
	if b := fromU16(src, 10); src[b:] != "fase:" {
		t.Errorf("da UTF-16 a byte: %q", src[b:])
	}
}

func TestDateSpan(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	now := time.Date(2026, 11, 25, 15, 30, 0, 0, rome) // mercoledì
	d := func(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, rome) }
	cases := []struct {
		v      QValue
		lo, hi *time.Time
	}{
		{QValue{Op: "eq", A: "oggi"}, ptr(d(2026, 11, 25)), ptr(d(2026, 11, 26))},
		{QValue{Op: "eq", A: "ieri"}, ptr(d(2026, 11, 24)), ptr(d(2026, 11, 25))},
		{QValue{Op: "eq", A: "settimana"}, ptr(d(2026, 11, 23)), ptr(d(2026, 11, 30))},
		{QValue{Op: "eq", A: "mese"}, ptr(d(2026, 11, 1)), ptr(d(2026, 12, 1))},
		{QValue{Op: "eq", A: "-7g"}, ptr(d(2026, 11, 18)), ptr(d(2026, 11, 26))},
		{QValue{Op: "eq", A: "+30g"}, ptr(d(2026, 11, 25)), ptr(d(2026, 12, 26))},
		{QValue{Op: "eq", A: "-1m"}, ptr(d(2026, 10, 25)), ptr(d(2026, 11, 26))},
		{QValue{Op: "eq", A: "2026-03-01"}, ptr(d(2026, 3, 1)), ptr(d(2026, 3, 2))},
		{QValue{Op: "eq", A: "01/03/2026"}, ptr(d(2026, 3, 1)), ptr(d(2026, 3, 2))},
		{QValue{Op: "eq", A: "2026-03"}, ptr(d(2026, 3, 1)), ptr(d(2026, 4, 1))},
		{QValue{Op: "gt", A: "2026-03-01"}, ptr(d(2026, 3, 2)), nil},
		{QValue{Op: "ge", A: "2026-03-01"}, ptr(d(2026, 3, 1)), nil},
		{QValue{Op: "lt", A: "-30g"}, nil, ptr(d(2026, 10, 26))},
		{QValue{Op: "le", A: "oggi"}, nil, ptr(d(2026, 11, 26))},
		{QValue{Op: "range", A: "-30g", B: "-7g"}, ptr(d(2026, 10, 26)), ptr(d(2026, 11, 19))},
		{QValue{Op: "range", B: "2026-12-31"}, nil, ptr(d(2027, 1, 1))},
	}
	for _, c := range cases {
		sp, ok := dateSpan(c.v, now)
		if !ok {
			t.Errorf("%+v non riconosciuta", c.v)
			continue
		}
		if !sameTime(sp.lo, c.lo) || !sameTime(sp.hi, c.hi) {
			t.Errorf("%s %s..%s: [%v, %v), atteso [%v, %v)", c.v.Op, c.v.A, c.v.B, sp.lo, sp.hi, c.lo, c.hi)
		}
	}
	for _, bad := range []string{"2026-02-30", "domenica", "-7x", "2026-13"} {
		if _, ok := dateSpan(QValue{Op: "eq", A: bad}, now); ok {
			t.Errorf("data %q accettata", bad)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func TestLocate(t *testing.T) {
	cases := []struct {
		q          string // "|" è il cursore
		kind       string
		field      string
		prefix, op string
		replaced   string
	}{
		{"|", "field", "", "", "", ""},
		{"fa|", "field", "", "fa", "", "fa"},
		{"-asse|", "field", "", "asse", "", "-asse"},
		{"fase: Tr|", "value", "fase", "Tr", "", "Tr"},
		{"fase:|", "value", "fase", "", "", ""},
		{"fase: Triage, Va|", "value", "fase", "Va", "", "Va"},
		{"fase: Triage oppure |", "value", "fase", "", "", ""},
		{"importo: >=10|", "value", "importo", "10", ">=", ">=10"},
		{"creata: -30g..og|", "value", "creata", "og", "-30g..", "-30g..og"},
		{`team: "Paga|`, "value", "team", "Paga", "", `"Paga`},
		{"fase: Triage |", "field", "", "", "", ""},
		{"ordina per: |", "sort", "", "", "", ""},
		{"ordina per: fa|", "sort", "", "fa", "", "fa"},
		{"ordina per: fase |", "direction", "", "", "", ""},
		{"fa|se: Triage", "field", "", "fa", "", "fase"},
	}
	for _, c := range cases {
		pos := strings.Index(c.q, "|")
		src := strings.Replace(c.q, "|", "", 1)
		toks, _ := lexQuery(src, true)
		sp := locate(src, toks, pos)
		if sp.kind != c.kind || sp.field != c.field || sp.prefix != c.prefix || sp.op != c.op || src[sp.from:sp.to] != c.replaced {
			t.Errorf("%q: %+v (sostituisce %q)", c.q, sp, src[sp.from:sp.to])
		}
	}
	// Nome di campo già seguito dai due punti: non si aggiunge ": ".
	src := "fa: Triage"
	toks, _ := lexQuery(src, true)
	if sp := locate(src, toks, 2); !sp.colonAfter {
		t.Errorf("due punti dopo il campo non riconosciuti: %+v", sp)
	}
}
