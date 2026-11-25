package itest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Organizzazioni dall'interfaccia: solo il superamministratore elenca e crea; chi crea diventa
// amministratore con il proprio account; le due organizzazioni restano isolate dalla RLS.
func TestOrganizzazioniDaInterfaccia(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	base := strings.ReplaceAll("org-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: base, Name: "Base", AdminEmail: "super@" + base + ".test",
		AdminPassword: testPassword, Provision: api.Provisioners()}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: base, Email: "semplice@" + base + ".test", Name: "Semplice", Password: testPassword, Roles: []string{"editor"}}); err != nil {
		t.Fatal(err)
	}
	if err := tenant.SetPlatformAdmin(ctx, d, "super@"+base+".test", true); err != nil {
		t.Fatal(err)
	}
	if err := tenant.SetPlatformAdmin(ctx, d, "nessuno@"+base+".test", true); err == nil {
		t.Fatal("un account inesistente non si può rendere superamministratore")
	}
	eng := engine.New()
	ap := approval.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap),
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	sup, plain := login(t, srv.URL, "super@"+base+".test", testPassword), login(t, srv.URL, "semplice@"+base+".test", testPassword)

	// Chi non è superamministratore non vede né crea.
	plain.do("GET", "/api/v1/platform/tenants", nil, http.StatusForbidden)
	plain.do("POST", "/api/v1/platform/tenants", map[string]any{"name": "Abusiva", "slug": base + "-x"}, http.StatusForbidden)
	if me := plain.do("GET", "/api/v1/me", nil, http.StatusOK); me["is_platform_admin"] != false {
		t.Fatalf("is_platform_admin di un membro semplice: %v", me["is_platform_admin"])
	}
	if me := sup.do("GET", "/api/v1/me", nil, http.StatusOK); me["is_platform_admin"] != true {
		t.Fatalf("is_platform_admin del superamministratore: %v", me["is_platform_admin"])
	}

	// Validazioni con code specifici.
	code := func(u *user, body map[string]any, status int) string {
		t.Helper()
		return u.do("POST", "/api/v1/platform/tenants", body, status)["code"].(string)
	}
	if c := code(sup, map[string]any{"name": "", "slug": base + "-n"}, 422); c != "validation.organizzazioni.name_invalid" {
		t.Fatalf("nome: %s", c)
	}
	if c := code(sup, map[string]any{"name": "N", "slug": "Non valido!"}, 422); c != "validation.organizzazioni.slug_invalid" {
		t.Fatalf("slug: %s", c)
	}
	if c := code(sup, map[string]any{"name": "N", "slug": base + "-l", "language": "fr"}, 422); c != "validation.organizzazioni.language_invalid" {
		t.Fatalf("lingua: %s", c)
	}
	if c := code(sup, map[string]any{"name": "N", "slug": base + "-v", "base_currency": "euro"}, 422); c != "validation.organizzazioni.currency_invalid" {
		t.Fatalf("valuta: %s", c)
	}
	if c := code(sup, map[string]any{"name": "N", "slug": base + "-m", "admin_email": "non-una-email"}, 422); c != "validation.organizzazioni.admin_email_invalid" {
		t.Fatalf("email: %s", c)
	}

	// Creazione: il superamministratore entra come amministratore con il proprio account.
	nuovo := base + "-nuova"
	out := sup.do("POST", "/api/v1/platform/tenants", map[string]any{"name": "Nuova Org", "slug": nuovo, "language": "en", "base_currency": "usd",
		"admin_email": "secondo@" + base + ".test"}, http.StatusCreated)
	if out["invite"] == nil {
		t.Fatalf("manca l'invito del secondo amministratore: %v", out)
	}
	if c := code(sup, map[string]any{"name": "Doppia", "slug": nuovo}, http.StatusConflict); c != "validation.organizzazioni.slug_taken" {
		t.Fatalf("slug doppio: %s", c)
	}
	var found map[string]any
	for _, it := range sup.do("GET", "/api/v1/platform/tenants", nil, http.StatusOK)["items"].([]any) {
		if m := it.(map[string]any); m["slug"] == nuovo {
			found = m
		}
	}
	if found == nil || found["language"] != "en" || found["base_currency"] != "USD" || found["members"].(float64) != 2 {
		t.Fatalf("elenco: %v", found)
	}

	// Il nuovo tenant compare tra quelli dell'account e vi si entra; i dati restano separati.
	me := sup.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": out["tenant_id"]}, http.StatusOK)
	if len(me["tenants"].([]any)) != 2 {
		t.Fatalf("organizzazioni dell'account: %v", me["tenants"])
	}
	members := sup.do("GET", "/api/v1/members", nil, http.StatusOK)
	raw := ""
	for _, it := range members["items"].([]any) {
		raw += it.(map[string]any)["display_name"].(string) + ","
	}
	if strings.Contains(raw, "Semplice") || !strings.Contains(raw, "super@") {
		t.Fatalf("membri della nuova organizzazione (RLS): %s", raw)
	}
	// Le persone si invitano con il flusso normale di Configurazione.
	sup.do("GET", "/api/v1/admin/members", nil, http.StatusOK)
}
