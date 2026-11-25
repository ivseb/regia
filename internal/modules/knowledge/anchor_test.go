package knowledge

import "testing"

func TestLocateSceglieIlContesto(t *testing.T) {
	text := AnchorText("# Titolo\n\nIl server si riavvia ogni notte.\n\nSe il server si riavvia di giorno, avvisa il team.\n")
	a := Anchor{Quote: "server si riavvia", Prefix: "Se il ", Suffix: " di giorno, avvisa", Pos: -1}
	got, ok := Locate(text, a)
	if !ok || got.Nth != 1 {
		t.Fatalf("occorrenza sbagliata: %+v ok=%v", got, ok)
	}
	if got.Prefix == "" || got.Suffix == "" {
		t.Fatalf("contesto non aggiornato: %+v", got)
	}
}

func TestLocateSeguePosizioneEOrfano(t *testing.T) {
	before := AnchorText("Prima riga.\n\nAvvia il servizio e controlla i log. Poi avvia il servizio e controlla i log.")
	a := Anchor{Quote: "avvia il servizio", Pos: -1}
	first, ok := Locate(before, a)
	if !ok {
		t.Fatal("non trovata")
	}
	// Il testo cambia davanti: l'ancora si sposta ma resta sulla stessa frase.
	after := AnchorText("Intro nuova e più lunga.\n\nPrima riga.\n\nAvvia il servizio e controlla i log. Poi avvia il servizio e controlla i log.")
	moved, ok := Locate(after, Anchor{Quote: a.Quote, Prefix: first.Prefix, Suffix: first.Suffix, Pos: first.Pos, Nth: first.Nth})
	if !ok || moved.Pos <= first.Pos || moved.Nth != first.Nth {
		t.Fatalf("ancora non spostata: %+v -> %+v", first, moved)
	}
	// La frase sparisce: orfano.
	if _, ok := Locate("Tutto riscritto.", Anchor{Quote: a.Quote, Pos: moved.Pos}); ok {
		t.Fatal("doveva diventare orfana")
	}
}

func TestNormQuote(t *testing.T) {
	if got := normQuote("  una  **frase**\n con a capo "); got != "una frase con a capo" {
		t.Fatalf("normQuote: %q", got)
	}
}
