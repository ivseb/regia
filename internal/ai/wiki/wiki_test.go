package wiki

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	keys, codes := Tokens("Segue DEM-12 e APP-MOB.\n```\nDEM-99 nel codice\n```\nPoi PRJ-3, APP-MOB di nuovo e CRM.CORE.")
	if !slices.Equal(keys, []string{"DEM-12", "PRJ-3"}) {
		t.Fatalf("chiavi: %v", keys)
	}
	if !slices.Equal(codes, []string{"APP-MOB", "CRM.CORE"}) {
		t.Fatalf("codici: %v", codes)
	}
}

func TestPertinent(t *testing.T) {
	now := time.Now()
	pages := []Page{
		{Title: "Le mie priorità", Body: "- DEM-4 urgente", UpdatedAt: now},
		{Title: "Comitato architettura", Body: "Riunione mensile, APP-MOB in discussione.", UpdatedAt: now.Add(-time.Hour)},
		{Title: "Ricette", Body: "pasta e fagioli", UpdatedAt: now},
	}
	got := Pertinent(pages, "Cosa ha deciso il comitato su APP-MOB?", nil, 6000)
	if len(got) != 1 || got[0].Title != "Comitato architettura" {
		t.Fatalf("pertinenti per il comitato: %v", got)
	}
	got = Pertinent(pages, "riassunto", []string{"DEM-4"}, 6000)
	if len(got) != 1 || got[0].Title != "Le mie priorità" {
		t.Fatalf("pertinenti per chiave: %v", got)
	}
	if got := Pertinent(pages, "niente di simile", nil, 6000); len(got) != 0 {
		t.Fatalf("pagine non pertinenti: %v", got)
	}
	// Limite: il testo lungo si taglia.
	long := []Page{{Title: "Comitato", Body: strings.Repeat("comitato parola ", 1000), UpdatedAt: now}}
	got = Pertinent(long, "comitato", nil, 1000)
	if len(got) != 1 || len([]rune(got[0].Body)) > 1000 {
		t.Fatalf("taglio al limite: %d", len([]rune(got[0].Body)))
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt("# Titolo\n\n- **DEM-1** da _fare_\n", 50); got != "Titolo DEM-1 da fare" {
		t.Fatalf("estratto: %q", got)
	}
}
