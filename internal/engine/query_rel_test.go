package engine

import (
	"errors"
	"testing"
)

// Sintassi delle sottoquery e delle relazioni (query_rel.go).
func TestParseSottoquery(t *testing.T) {
	src := `tipo: application collegata: (tipo: risk fase: aperta "registro (fornitori)"), DEM-3 (nota)`
	q, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Clauses) != 2 || q.Clauses[1].Name != "collegata" {
		t.Fatalf("clausole: %+v", q.Clauses)
	}
	vs := q.Clauses[1].Values
	if len(vs) != 2 || vs[0].Op != "sub" || vs[1].Op != "eq" || vs[1].A != "DEM-3" {
		t.Fatalf("valori: %+v", vs)
	}
	sub := vs[0].Sub
	if sub == nil || len(sub.Clauses) != 2 || sub.Clauses[0].Name != "tipo" || len(sub.Text) != 1 || sub.Text[0].Text != "registro (fornitori)" {
		t.Fatalf("sottoquery: %+v", sub)
	}
	// Le posizioni sono quelle della query intera.
	if got := src[sub.Clauses[1].NamePos:sub.Clauses[1].NameEnd]; got != "fase" {
		t.Errorf("posizione nella sottoquery: %q", got)
	}
	// Fuori da una clausola le parentesi restano testo libero.
	if len(q.Text) != 1 || q.Text[0].Text != "(nota)" {
		t.Errorf("testo libero: %+v", q.Text)
	}
	// Sottoquery annidata.
	q, err = Parse(`genitore: (tipo: epic collegata: (tipo: application))`)
	if err != nil || q.Clauses[0].Values[0].Sub.Clauses[1].Values[0].Sub == nil {
		t.Fatalf("annidata: %v %+v", err, q)
	}
	for _, bad := range []struct{ src, frag string }{
		{`collegata: (tipo: risk`, "(tipo: risk"},
		{`collegata: ()`, "()"},
		{`collegata: (tipo: risk ordina per: fase)`, "fase"},
		{`a: (b: (c: (d: (e: x))))`, "(d: (e: x))"},
	} {
		_, err := Parse(bad.src)
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Errorf("%s: atteso un errore, ottenuto %v", bad.src, err)
			continue
		}
		if got := bad.src[qe.Pos:qe.End]; got != bad.frag {
			t.Errorf("%s: frammento %q, atteso %q (%s)", bad.src, got, bad.frag, qe.Msg)
		}
	}
	// In modalità tollerante (autocompletamento) la parentesi aperta arriva a fine testo.
	q, err = parseQuery(`tipo: application collegata: (tipo: ri`, true)
	if q == nil || len(q.Clauses) != 2 || q.Clauses[1].Values[0].Sub == nil {
		t.Fatalf("tollerante: %v %+v", err, q)
	}
}
