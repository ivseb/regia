package itest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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

// loginSenzaOrg fa il login di un account senza pretendere un'organizzazione attiva (serve per
// chi ha solo organizzazioni sospese).
func loginSenzaOrg(t *testing.T, base, email string) (*user, map[string]any) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	u := &user{t: t, base: base, c: &http.Client{Jar: jar}}
	return u, u.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": testPassword}, http.StatusOK)
}

// Modifica e sospensione di un'organizzazione: nome, audit del tenant, accesso negato a chi è
// dentro e a chi prova a entrare, riattivazione.
func TestOrganizzazioniModificaESospensione(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	base := strings.ReplaceAll("sosp-"+time.Now().Format("150405.000"), ".", "")
	mail := func(n string) string { return n + "@" + base + ".test" }
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: base, Name: "Base", AdminEmail: mail("super"),
		AdminPassword: testPassword, Provision: api.Provisioners()}); err != nil {
		t.Fatal(err)
	}
	if err := tenant.SetPlatformAdmin(ctx, d, mail("super"), true); err != nil {
		t.Fatal(err)
	}
	// Seconda organizzazione, con una persona solo lì (utente «solo») e una anche nella base («doppio»).
	altro, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: base + "-b", Name: "Org B", AdminEmail: mail("adminb"),
		AdminPassword: testPassword, Provision: api.Provisioners()})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []tenant.MemberInput{
		{TenantSlug: base + "-b", Email: mail("solo"), Name: "Solo", Password: testPassword, Roles: []string{"editor"}},
		{TenantSlug: base + "-b", Email: mail("doppio"), Name: "Doppio", Password: testPassword, Roles: []string{"editor"}},
		{TenantSlug: base, Email: mail("doppio"), Name: "Doppio", Roles: []string{"editor"}},
		{TenantSlug: base + "-b", Email: mail("super"), Name: "Super", Roles: []string{"editor"}},
		{TenantSlug: base, Email: mail("semplice"), Name: "Semplice", Password: testPassword, Roles: []string{"editor"}},
	} {
		if _, err := tenant.AddMember(ctx, d, m); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap),
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	sup, plain := login(t, srv.URL, mail("super"), testPassword), login(t, srv.URL, mail("semplice"), testPassword)
	id := altro.TenantID
	path := "/api/v1/platform/tenants/" + id

	// Solo il superamministratore.
	plain.do("PATCH", path, map[string]any{"name": "X"}, http.StatusForbidden)
	plain.do("POST", path+"/suspend", nil, http.StatusForbidden)
	plain.do("POST", path+"/reactivate", nil, http.StatusForbidden)

	// Rinomina: validazione, inesistente, identificativo invariato.
	if c := sup.do("PATCH", path, map[string]any{"name": "  "}, 422)["code"]; c != "validation.organizzazioni.name_invalid" {
		t.Fatalf("nome vuoto: %v", c)
	}
	if c := sup.do("PATCH", path, map[string]any{"name": strings.Repeat("a", 121)}, 422)["code"]; c != "validation.organizzazioni.name_invalid" {
		t.Fatalf("nome lungo: %v", c)
	}
	sup.do("PATCH", "/api/v1/platform/tenants/00000000-0000-0000-0000-000000000000", map[string]any{"name": "X"}, http.StatusNotFound)
	sup.do("PATCH", "/api/v1/platform/tenants/non-un-id", map[string]any{"name": "X"}, http.StatusNotFound)
	sup.do("PATCH", path, map[string]any{"name": " Org B rinominata "}, http.StatusOK)
	stato := func(slug string) map[string]any {
		t.Helper()
		for _, it := range sup.do("GET", "/api/v1/platform/tenants", nil, http.StatusOK)["items"].([]any) {
			if m := it.(map[string]any); m["slug"] == slug {
				return m
			}
		}
		t.Fatalf("organizzazione %s assente dall'elenco", slug)
		return nil
	}
	if o := stato(base + "-b"); o["name"] != "Org B rinominata" || o["status"] != "active" {
		t.Fatalf("dopo la rinomina: %v", o)
	}

	// Sessioni già aperte prima della sospensione.
	solo, _ := loginSenzaOrg(t, srv.URL, mail("solo"))
	doppio := login(t, srv.URL, mail("doppio"), testPassword)
	doppio.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": id}, http.StatusOK)
	solo.do("GET", "/api/v1/members", nil, http.StatusOK)

	// Sospensione: il superamministratore non può sospendere l'organizzazione in cui lavora.
	sup.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": id}, http.StatusOK)
	if c := sup.do("POST", path+"/suspend", nil, http.StatusConflict)["code"]; c != "platform.cannot_suspend_current" {
		t.Fatalf("sospensione della propria: %v", c)
	}
	sup.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": id}, http.StatusOK) // resta lì
	// Torna alla base: l'altra si può sospendere.
	var baseID string
	for _, m := range sup.do("GET", "/api/v1/me", nil, http.StatusOK)["tenants"].([]any) {
		if m.(map[string]any)["slug"] == base {
			baseID = m.(map[string]any)["tenant_id"].(string)
		}
	}
	sup.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": baseID}, http.StatusOK)
	sup.do("POST", path+"/suspend", nil, http.StatusOK)
	if o := stato(base + "-b"); o["status"] != "suspended" {
		t.Fatalf("stato dopo la sospensione: %v", o)
	}

	// La sessione di chi era solo lì perde l'accesso subito, e /me lo dice.
	solo.do("GET", "/api/v1/members", nil, http.StatusForbidden)
	me := solo.do("GET", "/api/v1/me", nil, http.StatusOK)
	if me["active_tenant"] != nil {
		t.Fatalf("organizzazione attiva dopo la sospensione: %v", me["active_tenant"])
	}
	if ts := me["tenants"].([]any); len(ts) != 1 || ts[0].(map[string]any)["status"] != "suspended" || ts[0].(map[string]any)["name"] != "Org B rinominata" {
		t.Fatalf("organizzazioni dell'account sospeso: %v", me["tenants"])
	}
	// Chi ha anche un'altra organizzazione attiva passa a quella.
	me = doppio.do("GET", "/api/v1/me", nil, http.StatusOK)
	if a, _ := me["active_tenant"].(map[string]any); a == nil || a["tenant_id"] != baseID {
		t.Fatalf("doppio non è passato all'organizzazione attiva: %v", me["active_tenant"])
	}
	doppio.do("GET", "/api/v1/members", nil, http.StatusOK)
	// Il cambio verso la sospesa è rifiutato con un codice chiaro.
	if c := doppio.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": id}, http.StatusForbidden)["code"]; c != "tenant.suspended" {
		t.Fatalf("cambio verso la sospesa: %v", c)
	}
	// Un nuovo login di chi ha solo la sospesa riesce, senza organizzazione attiva.
	_, me = loginSenzaOrg(t, srv.URL, mail("solo"))
	if me["active_tenant"] != nil || me["tenants"].([]any)[0].(map[string]any)["status"] != "suspended" {
		t.Fatalf("login con la sola organizzazione sospesa: %v", me)
	}
	// Il login di chi ha anche una attiva entra in quella.
	if a := login(t, srv.URL, mail("doppio"), testPassword); a.member == "" {
		t.Fatal("doppio non entra")
	}
	// Le pagine di piattaforma restano raggiungibili al superamministratore.
	sup.do("GET", "/api/v1/platform/tenants", nil, http.StatusOK)

	// Riattivazione: chi era solo lì rientra alla richiesta successiva dopo aver scelto l'organizzazione.
	sup.do("POST", path+"/reactivate", nil, http.StatusOK)
	if o := stato(base + "-b"); o["status"] != "active" {
		t.Fatalf("stato dopo la riattivazione: %v", o)
	}
	solo2, me := loginSenzaOrg(t, srv.URL, mail("solo"))
	if me["active_tenant"] == nil {
		t.Fatalf("dopo la riattivazione non entra: %v", me)
	}
	solo2.do("GET", "/api/v1/members", nil, http.StatusOK)
	doppio.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": id}, http.StatusOK)

	// Audit nel tenant, con il superamministratore come attore (membro di Org B? no: solo l'account).
	var adminB string
	if err := d.Owner.QueryRow(ctx, "select a.id from accounts a where lower(a.email) = $1", mail("super")).Scan(&adminB); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"tenant.update": false, "tenant.suspend": false, "tenant.reactivate": false}
	rows, err := d.Owner.Query(ctx, "select action, actor_account_id::text from audit_log where tenant_id = $1 and action like 'tenant.%'", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		var actor *string
		if err := rows.Scan(&action, &actor); err != nil {
			t.Fatal(err)
		}
		if _, ok := want[action]; ok {
			if actor == nil || *actor != adminB {
				t.Fatalf("%s: attore %v, atteso il superamministratore", action, actor)
			}
			want[action] = true
		}
	}
	for a, ok := range want {
		if !ok {
			t.Fatalf("evento %s assente dall'audit del tenant", a)
		}
	}
}
