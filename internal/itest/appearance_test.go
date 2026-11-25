package itest

import (
	"context"
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

// appearanceOf legge tema e font da un oggetto {"theme": …, "font": …} (nil = nessuna scelta).
func appearanceOf(v any) (theme, font any) {
	m, _ := v.(map[string]any)
	return m["theme"], m["font"]
}

func wantAppearance(t *testing.T, label string, v any, theme, font any) {
	t.Helper()
	gotT, gotF := appearanceOf(v)
	if gotT != theme || gotF != font {
		t.Fatalf("%s: tema %v e font %v, attesi %v e %v", label, gotT, gotF, theme, font)
	}
}

// Aspetto: la scelta personale vale campo per campo sopra il default del tenant, che
// solo tenant.admin può cambiare; i valori sconosciuti si rifiutano; il default resta
// nel suo tenant e il cambio finisce nell'audit.
func TestAppearancePreferences(t *testing.T) {
	e := newSGEnv(t, "aspetto")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	user := e.member("utente", tenant.MemberInput{})

	// Nessuna scelta: valgono i default di prodotto.
	me := user.do("GET", "/api/v1/me", nil, http.StatusOK)
	wantAppearance(t, "/me iniziale", me["appearance"], "atlante", "atkinson")
	prefs := user.do("GET", "/api/v1/me/preferences", nil, http.StatusOK)
	wantAppearance(t, "scelta iniziale", prefs["appearance"], nil, nil)
	wantAppearance(t, "default del tenant iniziale", prefs["tenant_default"], nil, nil)
	if opts, _ := prefs["options"].(map[string]any); len(opts["themes"].([]any)) < 3 || len(opts["fonts"].([]any)) < 3 {
		t.Fatalf("opzioni incomplete: %v", prefs["options"])
	}

	// Il default del tenant lo cambia solo l'amministratore.
	user.do("PUT", "/api/v1/tenant/appearance", map[string]any{"theme": "petrolio", "font": "plex"}, http.StatusForbidden)
	got := admin.do("PUT", "/api/v1/tenant/appearance", map[string]any{"theme": "petrolio", "font": "plex"}, http.StatusOK)
	wantAppearance(t, "default salvato", got, "petrolio", "plex")
	if got["updated_by"] == nil || got["updated_at"] == nil {
		t.Fatalf("manca chi ha cambiato il default: %v", got)
	}
	wantAppearance(t, "default letto da un membro", user.do("GET", "/api/v1/tenant/appearance", nil, http.StatusOK), "petrolio", "plex")
	wantAppearance(t, "/me con il default del tenant", user.do("GET", "/api/v1/me", nil, http.StatusOK)["appearance"], "petrolio", "plex")

	// Scelta personale solo del tema: il font resta quello del tenant.
	prefs = user.do("PUT", "/api/v1/me/preferences", map[string]any{"appearance": map[string]any{"theme": "contrasto"}}, http.StatusOK)
	wantAppearance(t, "scelta personale", prefs["appearance"], "contrasto", nil)
	wantAppearance(t, "effettivo dopo la scelta", prefs["effective"], "contrasto", "plex")

	// Il tenant cambia default: cambia solo il campo non scelto dall'utente.
	admin.do("PUT", "/api/v1/tenant/appearance", map[string]any{"theme": "nebbia", "font": "source"}, http.StatusOK)
	wantAppearance(t, "effettivo dopo il nuovo default", user.do("GET", "/api/v1/me", nil, http.StatusOK)["appearance"], "contrasto", "source")
	wantAppearance(t, "l'amministratore segue il default", admin.do("GET", "/api/v1/me", nil, http.StatusOK)["appearance"], "nebbia", "source")

	// Valori sconosciuti: rifiutati, niente cambia.
	bad := user.do("PUT", "/api/v1/me/preferences", map[string]any{"appearance": map[string]any{"theme": "viola-ai"}}, http.StatusUnprocessableEntity)
	if bad["field"] != "theme" {
		t.Fatalf("campo dell'errore: %v", bad)
	}
	admin.do("PUT", "/api/v1/tenant/appearance", map[string]any{"font": "comic-sans"}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/tenant/appearance", map[string]any{"colore": "viola"}, http.StatusBadRequest)
	wantAppearance(t, "dopo i rifiuti", user.do("GET", "/api/v1/me/preferences", nil, http.StatusOK)["effective"], "contrasto", "source")

	// Tornare al default dell'organizzazione: stringa vuota o chiave assente.
	prefs = user.do("PUT", "/api/v1/me/preferences", map[string]any{"appearance": map[string]any{"theme": ""}}, http.StatusOK)
	wantAppearance(t, "scelta azzerata", prefs["appearance"], nil, nil)
	wantAppearance(t, "effettivo azzerato", prefs["effective"], "nebbia", "source")

	// La preferenza è dell'account: vale anche in un altro tenant, dove il default è un altro.
	other := newSGEnv(t, "aspetto-altro")
	user.do("PUT", "/api/v1/me/preferences", map[string]any{"appearance": map[string]any{"font": "public"}}, http.StatusOK)
	stranger := other.member("estraneo", tenant.MemberInput{})
	wantAppearance(t, "altro tenant", stranger.do("GET", "/api/v1/me", nil, http.StatusOK)["appearance"], "atlante", "atkinson")
	wantAppearance(t, "default dell'altro tenant", stranger.do("GET", "/api/v1/tenant/appearance", nil, http.StatusOK), nil, nil)

	// Il cambio del default è nell'audit del tenant, con chi l'ha fatto.
	var n int
	if err := testDB.Owner.QueryRow(context.Background(),
		"select count(*) from audit_log where tenant_id = $1 and action = 'tenant.appearance.update' and actor_member_id is not null",
		e.tenantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("eventi di audit: %d, attesi 2", n)
	}
}
