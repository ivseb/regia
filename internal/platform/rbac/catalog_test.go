package rbac

import "testing"

// Ogni permesso assegnabile ha un nome e una descrizione: nel modulo dei ruoli non devono
// comparire chiavi tecniche nude sotto «Altro».
func TestCatalogoSenzaVociNude(t *testing.T) {
	for _, p := range Catalog() {
		if p.Area == "Altro" || p.Name == p.Key || p.Description == "" {
			t.Errorf("permesso %q senza nome o descrizione (area %q)", p.Key, p.Area)
		}
	}
}
