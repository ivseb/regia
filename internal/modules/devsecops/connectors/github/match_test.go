package github

import "testing"

func TestSuggest(t *testing.T) {
	apps := []App{
		{ID: "1", Key: "APP-1", Name: "Ordini", Code: "ORD"},
		{ID: "2", Key: "APP-2", Name: "Pagamenti", Code: "PAG"},
		{ID: "3", Key: "APP-3", Name: "Catalogo web", Code: ""},
		{ID: "4", Key: "APP-4", Name: "Gestione clienti", Code: "CRM"},
	}
	cases := []struct {
		repo   string
		app    string
		atLeas float64
	}{
		{"crm", "4", 1.0},
		{"catalogo-web", "3", 0.97},
		{"pag-gateway", "2", 0.92},
		{"ordini-api", "1", 0.9},
		{"OrdiniApi", "1", 0.9},
		{"pagamneti", "2", 0.6},
		{"gestione-clienti-backend", "4", 0.9},
		{"strumenti-interni", "", 0},
		{"api", "", 0},
	}
	for _, c := range cases {
		s := Suggest(c.repo, apps)
		if c.app == "" {
			if s != nil {
				t.Errorf("%s: proposta inattesa %s (%.2f, %s)", c.repo, s.App.Name, s.Score, s.Reason)
			}
			continue
		}
		if s == nil || s.App.ID != c.app || s.Score < c.atLeas {
			t.Errorf("%s: %+v, attesa l'applicazione %s con almeno %.2f", c.repo, s, c.app, c.atLeas)
		}
	}
	if (App{Key: "APP-3"}).Ref() != "APP-3" || (App{Key: "APP-1", Code: "ORD"}).Ref() != "ORD" {
		t.Fatal("riferimento all'applicazione nel contratto")
	}
	// Due applicazioni ugualmente vicine: la proposta resta, con un punteggio più basso.
	twin := []App{{ID: "a", Name: "Fatture"}, {ID: "b", Name: "Fatture"}}
	if s := Suggest("fatture", twin); s == nil || s.Score >= 0.97 {
		t.Fatalf("pareggio: %+v", s)
	}
}
