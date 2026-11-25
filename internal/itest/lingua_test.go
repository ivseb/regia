package itest

import (
	"context"
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

// headerTransport aggiunge un'intestazione a ogni richiesta (per provare Accept-Language).
type headerTransport struct{ k, v string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(h.k, h.v)
	return http.DefaultTransport.RoundTrip(r)
}

func langOf(t *testing.T, me map[string]any) (effective, source string) {
	t.Helper()
	l, ok := me["language"].(map[string]any)
	if !ok {
		t.Fatalf("/me senza lingua: %v", me)
	}
	return l["effective"].(string), l["source"].(string)
}

func wantLang(t *testing.T, label string, me map[string]any, effective, source string) {
	t.Helper()
	e, s := langOf(t, me)
	if e != effective || s != source {
		t.Fatalf("%s: lingua %s da %s, attesa %s da %s", label, e, s, effective, source)
	}
}

// Lingua: la scelta del membro vale sopra il default del tenant, che vale sopra
// Accept-Language; senza nulla l'italiano. Il default lo cambia solo tenant.admin.
func TestLanguagePrecedence(t *testing.T) {
	e := newSGEnv(t, "lingua")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	user := e.member("utente", tenant.MemberInput{})

	// Nessuna scelta: italiano; con Accept-Language inglese, inglese ("browser").
	wantLang(t, "iniziale", user.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "default")
	user.c.Transport = headerTransport{"Accept-Language", "de-DE,en-GB;q=0.8,it;q=0.5"}
	wantLang(t, "Accept-Language", user.do("GET", "/api/v1/me", nil, http.StatusOK), "en", "browser")
	user.c.Transport = headerTransport{"Accept-Language", "fr, de;q=0.9"}
	wantLang(t, "lingua non supportata", user.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "default")
	user.c.Transport = headerTransport{"Accept-Language", "en"}

	// Il default del tenant batte Accept-Language, e lo cambia solo l'amministratore.
	user.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": "it"}, http.StatusForbidden)
	got := admin.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": "it"}, http.StatusOK)
	wantLang(t, "risposta del default", map[string]any{"language": got}, "it", "tenant")
	wantLang(t, "default del tenant", user.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "tenant")

	// La scelta del membro batte il default del tenant, ed è sua soltanto.
	got = user.do("PUT", "/api/v1/me/language", map[string]any{"locale": "en"}, http.StatusOK)
	wantLang(t, "scelta del membro", map[string]any{"language": got}, "en", "member")
	user.c.Transport = nil
	wantLang(t, "membro su /me", user.do("GET", "/api/v1/me", nil, http.StatusOK), "en", "member")
	wantLang(t, "l'amministratore resta al default", admin.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "tenant")

	// Valori non supportati: rifiutati, niente cambia.
	bad := user.do("PUT", "/api/v1/me/language", map[string]any{"locale": "klingon"}, http.StatusUnprocessableEntity)
	if bad["field"] != "locale" {
		t.Fatalf("campo dell'errore: %v", bad)
	}
	admin.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": "xx"}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/tenant/language", map[string]any{"lingua": "en"}, http.StatusBadRequest)
	wantLang(t, "dopo i rifiuti", user.do("GET", "/api/v1/me", nil, http.StatusOK), "en", "member")

	// Stringa vuota: si torna a seguire il tenant; poi il tenant stesso torna a nessuna scelta.
	user.do("PUT", "/api/v1/me/language", map[string]any{"locale": ""}, http.StatusOK)
	wantLang(t, "membro azzerato", user.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "tenant")
	admin.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": ""}, http.StatusOK)
	wantLang(t, "tenant azzerato", user.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "default")

	// Il cambio del default è nell'audit, con chi l'ha fatto.
	var n int
	if err := testDB.Owner.QueryRow(context.Background(),
		"select count(*) from audit_log where tenant_id = $1 and action = 'tenant.language.update' and actor_member_id is not null",
		e.tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("eventi di audit: %d, attesi 2", n)
	}
}

// La scelta di un tenant non tocca un altro tenant.
func TestLanguageIsPerTenant(t *testing.T) {
	a := newSGEnv(t, "lingua-a")
	b := newSGEnv(t, "lingua-b")
	adminA := login(t, a.srv.URL, "admin@"+a.slug+".test", testPassword)
	adminA.do("PUT", "/api/v1/tenant/language", map[string]any{"locale": "en"}, http.StatusOK)
	userB := b.member("utente", tenant.MemberInput{})
	wantLang(t, "altro tenant", userB.do("GET", "/api/v1/me", nil, http.StatusOK), "it", "default")
	wantLang(t, "tenant A", adminA.do("GET", "/api/v1/me", nil, http.StatusOK), "en", "tenant")
}
