package knowledge

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	old := "# Titolo\nRiga uno\nRiga due da cambiare\nRiga tre"
	nu := "# Titolo\nRiga uno\nRiga due cambiata bene\nRiga tre\nRiga quattro"
	lines, st := Diff(old, nu)
	if st.Added != 1 || st.Changed != 1 || st.Removed != 0 {
		t.Fatalf("statistiche: %+v %+v", st, lines)
	}
	var mod *DiffLine
	for i := range lines {
		if lines[i].Op == "mod" {
			mod = &lines[i]
		}
	}
	if mod == nil || mod.A != 3 || mod.B != 3 {
		t.Fatalf("riga modificata: %+v", lines)
	}
	var del, add []string
	for _, p := range mod.Parts {
		switch p.Op {
		case "del":
			del = append(del, p.Text)
		case "add":
			add = append(add, p.Text)
		}
	}
	if strings.Join(del, "|") != "da cambiare" || strings.Join(add, "|") != "cambiata bene" {
		t.Fatalf("parole: %+v", mod.Parts)
	}
	// Ricostruzione: eq, add e mod danno il testo nuovo; eq, del e mod il vecchio.
	var a, b []string
	for _, l := range lines {
		switch l.Op {
		case "eq":
			a, b = append(a, l.Text), append(b, l.Text)
		case "del":
			a = append(a, l.Text)
		case "add":
			b = append(b, l.Text)
		case "mod":
			a, b = append(a, l.Old), append(b, l.Text)
		}
	}
	if strings.Join(a, "\n") != old || strings.Join(b, "\n") != nu {
		t.Fatalf("ricostruzione:\n%v\n%v", a, b)
	}
	if l, _ := Diff("", "uno\ndue"); len(l) != 2 || l[0].Op != "add" {
		t.Fatalf("da vuoto: %+v", l)
	}
	if l, st := Diff("a\nb", "a\nb"); len(l) != 2 || st != (DiffStats{}) {
		t.Fatalf("uguali: %+v", l)
	}
	// Testi lunghi e diversi: nessun errore, ricostruzione corretta anche oltre il limite.
	var x, y []string
	for i := 0; i < 3000; i++ {
		x = append(x, "vecchia "+strings.Repeat("a", i%7))
		y = append(y, "nuova "+strings.Repeat("b", i%5))
	}
	if l, _ := Diff(strings.Join(x, "\n"), strings.Join(y, "\n")); len(l) < 3000 {
		t.Fatalf("testi lunghi: %d righe", len(l))
	}
}

func TestPlainTextAndKeys(t *testing.T) {
	md := "# Runbook\n> [!ATTENZIONE] Fermare **prima** il job\n\n| a | b |\n|---|---|\n| DEM-12 | [link](https://x.it) |\n\n```\nWEB-9 nel codice\n```\n- voce WEB-3"
	pt := PlainText(md)
	if strings.Contains(pt, "**") || strings.Contains(pt, "---") || !strings.Contains(pt, "Fermare prima il job") || !strings.Contains(pt, "link") {
		t.Fatalf("testo piano: %q", pt)
	}
	if k := CitedKeys(md); strings.Join(k, ",") != "DEM-12,WEB-3" {
		t.Fatalf("chiavi: %v", k)
	}
	if h := Headings(md); len(h) != 1 || h[0].Text != "Runbook" {
		t.Fatalf("titoli: %v", h)
	}
}
