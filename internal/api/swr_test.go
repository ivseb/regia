package api

import (
	"testing"
	"time"
)

func TestSwrCacheStrictECoalescente(t *testing.T) {
	var c swrCache
	c.put("k", 1, "v1")

	if out, refresh := c.get("k", 1, 0); out != "v1" || refresh {
		t.Fatalf("stessa generazione: voce valida, atteso v1 senza rinnovo, trovato %v %v", out, refresh)
	}
	// Modalità stretta (cruscotto): dopo una modifica la voce non si serve più.
	if out, _ := c.get("k", 2, 0); out != nil {
		t.Fatalf("modalità stretta: la voce di una generazione vecchia non va servita, trovato %v", out)
	}
	// Modalità con ritardo tollerato (roadmap): si serve, e si rinnova solo oltre il ritardo.
	if out, refresh := c.get("k", 2, time.Minute); out != "v1" || refresh {
		t.Fatalf("entro il ritardo: atteso v1 senza rinnovo, trovato %v %v", out, refresh)
	}
	time.Sleep(15 * time.Millisecond)
	if out, refresh := c.get("k", 2, 5*time.Millisecond); out != "v1" || !refresh {
		t.Fatalf("oltre il ritardo: atteso v1 e rinnovo, trovato %v %v", out, refresh)
	}
	// Un solo ricalcolo per chiave.
	ch, leader := c.begin("k")
	if !leader {
		t.Fatal("il primo deve guidare il ricalcolo")
	}
	if ch2, leader2 := c.begin("k"); leader2 || ch2 != ch {
		t.Fatal("il secondo deve attendere lo stesso ricalcolo")
	}
	c.end("k", ch)
	select {
	case <-ch:
	default:
		t.Fatal("end deve chiudere il canale")
	}
	if _, leader := c.begin("k"); !leader {
		t.Fatal("dopo la fine si può ricalcolare di nuovo")
	}
}

// Il cruscotto tollera le sole modifiche «soft» (creazioni e passaggi di fase delle richieste): ogni
// altra modifica (strict) lo rende subito non servibile, anche entro il ritardo.
func TestSwrCacheStrictGeneration(t *testing.T) {
	var c swrCache
	c.putStrict("k", 1, 10, "v1")

	if out, refresh := c.getStrict("k", 1, 10, true, time.Minute); out != "v1" || refresh {
		t.Fatalf("nessuna modifica: atteso v1 senza rinnovo, trovato %v %v", out, refresh)
	}
	// Una modifica soft (gen sale, strict no): servibile per il ritardo.
	if out, refresh := c.getStrict("k", 5, 10, true, time.Minute); out != "v1" || refresh {
		t.Fatalf("modifica tollerata entro il ritardo: atteso v1, trovato %v %v", out, refresh)
	}
	time.Sleep(15 * time.Millisecond)
	if out, refresh := c.getStrict("k", 5, 10, true, 5*time.Millisecond); out != "v1" || !refresh {
		t.Fatalf("modifica tollerata oltre il ritardo: atteso v1 e rinnovo, trovato %v %v", out, refresh)
	}
	// Una modifica strict (un costo, un report...): subito non servibile, qualunque sia il ritardo.
	if out, _ := c.getStrict("k", 6, 11, true, time.Minute); out != nil {
		t.Fatalf("modifica strict: la voce non va servita, trovato %v", out)
	}
	// Senza la modalità (roadmap) la generazione strict non conta.
	if out, _ := c.getStrict("k", 6, 11, false, time.Minute); out != "v1" {
		t.Fatalf("senza modalità strict: atteso v1, trovato %v", out)
	}
}
