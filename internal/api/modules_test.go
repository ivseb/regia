package api

import "testing"

func TestModuleForRequest(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/api/v1/kb/spaces", "knowledge"},
		{"POST", "/api/v1/devsecops/vuln/exceptions", "devsecops"},
		{"GET", "/api/v1/risks.csv", "grc"},
		{"GET", "/api/v1/incidents/INC-1", "grc"},
		{"GET", "/api/v1/work/spaces/X/board", "work"},
		{"GET", "/api/v1/dashboard/portfolio", "demand"},
		{"GET", "/api/v1/architecture/capabilities", "architecture"},
		// catalogo condiviso: solo in lettura
		{"GET", "/api/v1/architecture/applications", ""},
		{"GET", "/api/v1/architecture/assets", ""},
		{"POST", "/api/v1/architecture/applications", "architecture"},
		// piattaforma e rotte condivise
		{"GET", "/api/v1/me", ""},
		{"GET", "/api/v1/elements", ""},
		{"GET", "/api/v1/tenant/modules", ""},
		{"GET", "/api/v1/teams", ""},
		{"GET", "/healthz", ""},
	} {
		if got := moduleForRequest(c.method, c.path); got != c.want {
			t.Errorf("%s %s: modulo %q, atteso %q", c.method, c.path, got, c.want)
		}
	}
	// Ogni prefisso appartiene a un solo modulo.
	seen := map[string]string{}
	for _, m := range moduleDefs {
		for _, p := range m.prefixes {
			if prev, dup := seen[p]; dup {
				t.Errorf("prefisso %q in %s e in %s", p, prev, m.Key)
			}
			seen[p] = m.Key
		}
	}
}
