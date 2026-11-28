package itest

import (
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

func modulesOn(t *testing.T, v any) map[string]bool {
	t.Helper()
	m, _ := v.(map[string]any)
	out := map[string]bool{}
	for k, x := range m {
		b, _ := x.(bool)
		out[k] = b
	}
	return out
}

// Moduli accendibili per tenant (migrazione 0047): tutti accesi di partenza, solo
// tenant.admin li cambia, un modulo spento risponde 404 module.disabled (i dati restano),
// /me dice quali sono accesi, gli altri tenant non cambiano e il revisore ha il suo profilo
// di menu.
func TestTenantModules(t *testing.T) {
	e := newSGEnv(t, "moduli")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	utente := e.member("utente", tenant.MemberInput{Roles: []string{"viewer"}})

	// Di partenza: otto moduli (con la continuità operativa), tutti accesi, anche in /me.
	got := admin.do("GET", "/api/v1/tenant/modules", nil, http.StatusOK)
	items, _ := got["items"].([]any)
	if len(items) != 8 {
		t.Fatalf("moduli: %v", got)
	}
	for _, it := range items {
		m := it.(map[string]any)
		if m["enabled"] != true || m["name"] == "" || m["effect"] == "" {
			t.Fatalf("modulo di partenza: %v", m)
		}
	}
	on := modulesOn(t, utente.do("GET", "/api/v1/me", nil, http.StatusOK)["active_tenant"].(map[string]any)["modules"])
	for _, k := range []string{"demand", "work", "knowledge", "devsecops", "grc", "architecture"} {
		if !on[k] {
			t.Fatalf("/me: il modulo %s dovrebbe essere acceso: %v", k, on)
		}
	}
	utente.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)
	utente.do("GET", "/api/v1/architecture/capabilities", nil, http.StatusOK)

	// Solo l'amministratore del tenant li cambia; un modulo sconosciuto si rifiuta.
	utente.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]bool{"knowledge": false}}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]bool{"magia": false}}, http.StatusUnprocessableEntity)
	utente.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)

	// Spento Knowledge e architettura: 404 coerente per tutti, anche per l'amministratore.
	after := admin.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]bool{"knowledge": false, "architecture": false}}, http.StatusOK)
	for _, it := range after["items"].([]any) {
		m := it.(map[string]any)
		want := m["key"] != "knowledge" && m["key"] != "architecture"
		if m["enabled"] != want {
			t.Fatalf("dopo lo spegnimento: %v", m)
		}
		if !want && (m["updated_at"] == nil || m["updated_by"] == nil) {
			t.Fatalf("manca chi ha cambiato: %v", m)
		}
	}
	for _, u := range []*user{admin, utente} {
		p := u.do("GET", "/api/v1/kb/spaces", nil, http.StatusNotFound)
		if p["code"] != "module.disabled" {
			t.Fatalf("codice dell'errore: %v", p)
		}
		u.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "X", "name": "X"}, http.StatusNotFound)
		u.do("GET", "/api/v1/architecture/capabilities", nil, http.StatusNotFound)
		// Il catalogo delle applicazioni serve alle altre aree e resta in lettura.
		u.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)
		// I moduli accesi e le rotte della piattaforma non cambiano.
		u.do("GET", "/api/v1/scorecards", nil, http.StatusOK)
		u.do("GET", "/api/v1/tenant/modules", nil, http.StatusOK)
	}
	off := modulesOn(t, utente.do("GET", "/api/v1/me", nil, http.StatusOK)["active_tenant"].(map[string]any)["modules"])
	if off["knowledge"] || off["architecture"] || !off["demand"] || !off["work"] || !off["grc"] || !off["devsecops"] {
		t.Fatalf("/me dopo lo spegnimento: %v", off)
	}

	// Un altro tenant non cambia.
	other := newSGEnv(t, "moduli-altro")
	stranger := login(t, other.srv.URL, "admin@"+other.slug+".test", testPassword)
	stranger.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)
	stranger.do("GET", "/api/v1/architecture/capabilities", nil, http.StatusOK)

	// Riaccendere restituisce le API con i dati di prima, subito.
	admin.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]bool{"knowledge": true, "architecture": true}}, http.StatusOK)
	utente.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)
	utente.do("GET", "/api/v1/architecture/capabilities", nil, http.StatusOK)

	// Il cambio finisce nell'audit.
	if aud := admin.raw("/api/v1/audit?limit=50", http.StatusOK); !containsStr(aud, "tenant.modules.update") {
		t.Fatalf("audit senza tenant.modules.update: %s", aud)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Il revisore (ruolo auditor, senza scrittura) ha il profilo di menu «auditor»; chi ha anche
// un ruolo di scrittura, l'amministratore e un lettore normale hanno il menu intero.
func TestMenuProfileAuditor(t *testing.T) {
	e := newSGEnv(t, "revisore")
	profile := func(u *user) any {
		return u.do("GET", "/api/v1/me", nil, http.StatusOK)["active_tenant"].(map[string]any)["menu_profile"]
	}
	if p := profile(login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)); p != "" {
		t.Fatalf("amministratore: %v", p)
	}
	if p := profile(e.member("lettore", tenant.MemberInput{Roles: []string{"viewer"}})); p != "" {
		t.Fatalf("lettore: %v", p)
	}
	if p := profile(e.member("revisore", tenant.MemberInput{Roles: []string{"auditor"}})); p != "auditor" {
		t.Fatalf("revisore: %v", p)
	}
	if p := profile(e.member("revisore-editor", tenant.MemberInput{Roles: []string{"auditor", "editor"}})); p != "" {
		t.Fatalf("revisore con scrittura: %v", p)
	}
}
