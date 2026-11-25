package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/webhooks"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Amministrazione di persone e accessi da interfaccia, MFA con TOTP e webhook in uscita
// (piano, passo 15; migrazione 0022).

type adminEnv struct {
	t     *testing.T
	base  string
	slug  string
	ten   tenant.Created
	auth  *auth.Service
	hooks *webhooks.Service
	eng   *engine.Service
	box   *secretbox.Box
	admin *user
}

var adminSeq int

func newAdminEnv(t *testing.T) *adminEnv { return newAdminEnvBox(t, secretbox.Random()) }

// newAdminEnvBox usa la chiave applicativa indicata (il test del backup la vuole su disco).
func newAdminEnvBox(t *testing.T, box *secretbox.Box) *adminEnv {
	d := needDB(t)
	ctx := context.Background()
	adminSeq++
	slug := fmt.Sprintf("adm%d-%s", adminSeq, strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Admin " + slug, AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	svc := auth.NewService(d, time.Hour)
	svc.SetSecretBox(box)
	// I destinatari dei test sono su 127.0.0.1: qui la protezione SSRF è spenta (la si
	// prova a parte con un servizio senza AllowPrivate).
	hooks := webhooks.New(webhooks.Options{DB: d, Eng: eng, Box: box, AllowPrivate: true, Timeout: 3 * time.Second})
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
		SecretBox: box, Webhooks: hooks,
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	e := &adminEnv{t: t, base: hs.URL, slug: slug, ten: ten, auth: svc, hooks: hooks, eng: eng, box: box}
	e.admin = login(t, hs.URL, "admin@"+slug+".test", testPassword)
	return e
}

func anonClient(t *testing.T, base string) *user {
	jar, _ := cookiejar.New(nil)
	return &user{t: t, base: base, c: &http.Client{Jar: jar}}
}

func (e *adminEnv) email(name string) string { return name + "@" + e.slug + ".test" }

// roleID cerca un ruolo per chiave (sistema) o per nome.
func (e *adminEnv) roleID(keyOrName string) string {
	e.t.Helper()
	for _, it := range e.admin.do("GET", "/api/v1/admin/roles", nil, http.StatusOK)["items"].([]any) {
		r := it.(map[string]any)
		if k, _ := r["key"].(string); k == keyOrName || r["name"] == keyOrName {
			return r["id"].(string)
		}
	}
	e.t.Fatalf("ruolo %q non trovato", keyOrName)
	return ""
}

func (e *adminEnv) groupID(name string) string {
	e.t.Helper()
	for _, it := range e.admin.do("GET", "/api/v1/admin/groups", nil, http.StatusOK)["items"].([]any) {
		g := it.(map[string]any)
		if g["name"] == name {
			return g["id"].(string)
		}
	}
	e.t.Fatalf("gruppo %q non trovato", name)
	return ""
}

// createMember crea direttamente una persona, le fa scegliere la password con il link e
// restituisce la sua sessione.
func (e *adminEnv) createMember(name string, roleKeys ...string) *user {
	e.t.Helper()
	var roles []string
	for _, k := range roleKeys {
		roles = append(roles, e.roleID(k))
	}
	res := e.admin.do("POST", "/api/v1/admin/members", map[string]any{
		"email": e.email(name), "display_name": strings.ToUpper(name[:1]) + name[1:], "mode": "create", "role_ids": roles,
	}, http.StatusCreated)
	link := res["link"].(map[string]any)
	if link["kind"] != "password" {
		e.t.Fatalf("link della creazione diretta: %v", link)
	}
	token := strings.TrimPrefix(link["path"].(string), "/password/")
	anonClient(e.t, e.base).do("POST", "/api/v1/auth/password-reset/"+token, map[string]any{"password": testPassword}, http.StatusNoContent)
	return login(e.t, e.base, e.email(name), testPassword)
}

func (e *adminEnv) memberDetail(id string) map[string]any {
	return e.admin.do("GET", "/api/v1/admin/members/"+id, nil, http.StatusOK)
}

func bearerStatus(t *testing.T, base, token, path string) int {
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := auth.TOTPCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// wrongCode è un codice certamente sbagliato (diverso da quelli della finestra di tolleranza).
func wrongCode(t *testing.T, secret string) string {
	now := time.Now()
	valid := map[string]bool{}
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		valid[codeAt(t, secret, now.Add(d))] = true
	}
	for i := 0; ; i++ {
		c := fmt.Sprintf("%06d", (123457+i*7919)%1000000)
		if !valid[c] {
			return c
		}
	}
}

func resetLastStep(t *testing.T, email string) {
	if _, err := needDB(t).Owner.Exec(context.Background(), "update accounts set mfa_last_step = null where lower(email) = $1", email); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------
// Inviti e creazione diretta
// ---------------------------------------------------------------------

func TestAdminInviteAndAccept(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	pmo := e.groupID("PMO")
	email := e.email("nuova")

	res := e.admin.do("POST", "/api/v1/admin/members", map[string]any{
		"email": email, "display_name": "Nuova Persona", "mode": "invite",
		"role_ids": []string{e.roleID("editor")}, "group_ids": []string{pmo},
	}, http.StatusCreated)
	if res["new_account"] != true || res["emailed"] != false {
		t.Fatalf("invito: %v", res)
	}
	path := res["link"].(map[string]any)["path"].(string)
	if !strings.HasPrefix(path, "/invito/") {
		t.Fatalf("link dell'invito: %q", path)
	}
	token := strings.TrimPrefix(path, "/invito/")
	memberID := res["member_id"].(string)

	// Nel database c'è solo l'impronta del token.
	var plain int
	if err := d.Owner.QueryRow(ctx, "select count(*) from invitations where encode(token_hash, 'escape') like '%' || $1 || '%'", token).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("token dell'invito in chiaro: %d %v", plain, err)
	}
	// Prima di accettare: stato "invited", niente accesso, non compare tra i membri selezionabili.
	if m := e.memberDetail(memberID); m["status"] != "invited" || m["invite_expires_at"] == nil {
		t.Fatalf("membro invitato: %v", m)
	}
	anon := anonClient(t, e.base)
	anon.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, http.StatusUnauthorized)
	for _, it := range e.admin.do("GET", "/api/v1/members", nil, http.StatusOK)["items"].([]any) {
		if it.(map[string]any)["id"] == memberID {
			t.Fatal("un invitato compare tra i membri attivi")
		}
	}
	// Invitare di nuovo la stessa persona non crea un doppione.
	e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": strings.ToUpper(email), "mode": "invite"}, http.StatusConflict)

	info := anon.do("GET", "/api/v1/auth/invitations/"+token, nil, http.StatusOK)
	if info["needs_password"] != true || info["email"] != email || !strings.HasPrefix(info["tenant"].(string), "Admin ") {
		t.Fatalf("informazioni dell'invito: %v", info)
	}
	anon.do("POST", "/api/v1/auth/invitations/"+token+"/accept", map[string]any{"display_name": "Nuova", "password": "corta"}, http.StatusUnprocessableEntity)
	anon.do("POST", "/api/v1/auth/invitations/"+token+"/accept", map[string]any{"display_name": "Nuova Persona", "password": testPassword}, http.StatusNoContent)
	// Monouso.
	anon.do("POST", "/api/v1/auth/invitations/"+token+"/accept", map[string]any{"password": testPassword}, http.StatusNotFound)
	anon.do("GET", "/api/v1/auth/invitations/"+token, nil, http.StatusNotFound)

	u := login(t, e.base, email, testPassword)
	me := u.do("GET", "/api/v1/me", nil, http.StatusOK)
	perms := fmt.Sprint(me["active_tenant"].(map[string]any)["permissions"])
	if !strings.Contains(perms, "element.write") {
		t.Fatalf("permessi dopo l'accettazione: %s", perms)
	}
	m := e.memberDetail(memberID)
	if m["status"] != "active" || !strings.Contains(fmt.Sprint(m["groups"]), "PMO") || !strings.Contains(fmt.Sprint(m["groups"]), "Tutti i membri") {
		t.Fatalf("membro dopo l'accettazione: %v", m)
	}

	// Invito di un account che esiste già (consulente su più clienti): la password resta la sua.
	other := newTenant(t, d)
	otherEmail := adminEmail(t, other.AdminAccountID)
	res = e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": otherEmail, "mode": "invite"}, http.StatusCreated)
	if res["new_account"] != false {
		t.Fatalf("account esistente: %v", res)
	}
	tok2 := strings.TrimPrefix(res["link"].(map[string]any)["path"].(string), "/invito/")
	if info := anon.do("GET", "/api/v1/auth/invitations/"+tok2, nil, http.StatusOK); info["needs_password"] != false {
		t.Fatalf("account esistente: non deve scegliere la password: %v", info)
	}
	anon.do("POST", "/api/v1/auth/invitations/"+tok2+"/accept", map[string]any{"password": "un-altra-password-lunga"}, http.StatusNoContent)
	cons := anonClient(t, e.base)
	me = cons.do("POST", "/api/v1/auth/login", map[string]any{"email": otherEmail, "password": testPassword}, http.StatusOK)
	if n := len(me["tenants"].([]any)); n != 2 {
		t.Fatalf("il consulente vede %d tenant, attesi 2", n)
	}

	// Rinnovo: il link precedente non vale più. Scadenza: un link scaduto non si apre.
	res = e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": e.email("ritardo"), "mode": "invite"}, http.StatusCreated)
	late := res["member_id"].(string)
	old := strings.TrimPrefix(res["link"].(map[string]any)["path"].(string), "/invito/")
	renewed := e.admin.do("POST", "/api/v1/admin/members/"+late+"/invitation", nil, http.StatusOK)
	fresh := strings.TrimPrefix(renewed["link"].(map[string]any)["path"].(string), "/invito/")
	anon.do("GET", "/api/v1/auth/invitations/"+old, nil, http.StatusNotFound)
	anon.do("GET", "/api/v1/auth/invitations/"+fresh, nil, http.StatusOK)
	if _, err := d.Owner.Exec(ctx, "update invitations set expires_at = now() - interval '1 minute' where member_id = $1", late); err != nil {
		t.Fatal(err)
	}
	anon.do("GET", "/api/v1/auth/invitations/"+fresh, nil, http.StatusNotFound)

	// Creazione diretta: attivo subito, la password la sceglie la persona con il suo link.
	direct := e.createMember("diretta", "viewer")
	if direct.member == "" {
		t.Fatal("creazione diretta senza membro")
	}
	// Validazioni.
	e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": "non-una-email", "mode": "invite"}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": e.email("x"), "mode": "boh"}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": e.email("y"), "mode": "invite", "member_type": "guest"}, http.StatusUnprocessableEntity)
	// Un non amministratore non vede né modifica le persone.
	u.do("GET", "/api/v1/admin/members", nil, http.StatusForbidden)
	u.do("POST", "/api/v1/admin/members", map[string]any{"email": e.email("z"), "mode": "invite"}, http.StatusForbidden)
}

// ---------------------------------------------------------------------
// Sospensione, cambio di tipo, reset della password
// ---------------------------------------------------------------------

func TestAdminSuspendClosesSessionsAndTokens(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	u := e.createMember("sospesa", "editor")
	token := u.do("POST", "/api/v1/me/tokens", map[string]any{"name": "script"}, http.StatusCreated)["token"].(string)
	if s := bearerStatus(t, e.base, token, "/api/v1/me"); s != http.StatusOK {
		t.Fatalf("token prima della sospensione: %d", s)
	}
	// Un token API non può amministrare le persone, anche con tenant.admin.
	adminToken := e.admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "amministrazione"}, http.StatusCreated)["token"].(string)
	req, _ := http.NewRequest("POST", e.base+"/api/v1/admin/members/"+u.member+"/suspend", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sospensione con un token API: %v %v", resp, err)
	}

	e.admin.do("POST", "/api/v1/admin/members/"+e.admin.member+"/suspend", nil, http.StatusConflict) // sé stessi no
	e.admin.do("POST", "/api/v1/admin/members/"+u.member+"/suspend", nil, http.StatusNoContent)
	u.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	if s := bearerStatus(t, e.base, token, "/api/v1/me"); s != http.StatusUnauthorized {
		t.Fatalf("token dopo la sospensione: %d", s)
	}
	var sessions, tokens int
	if err := d.Owner.QueryRow(ctx, `select (select count(*) from sessions s join members m on m.account_id = s.account_id where m.id = $1),
		(select count(*) from api_tokens where member_id = $1)`, u.member).Scan(&sessions, &tokens); err != nil || sessions != 0 || tokens != 0 {
		t.Fatalf("sessioni %d e token %d dopo la sospensione (%v)", sessions, tokens, err)
	}
	// Può ancora autenticarsi, ma non ha più questo tenant.
	me := anonClient(t, e.base).do("POST", "/api/v1/auth/login", map[string]any{"email": e.email("sospesa"), "password": testPassword}, http.StatusOK)
	if me["active_tenant"] != nil {
		t.Fatalf("un membro sospeso entra nel tenant: %v", me["active_tenant"])
	}
	if m := e.memberDetail(u.member); m["status"] != "suspended" {
		t.Fatalf("stato: %v", m["status"])
	}
	e.admin.do("POST", "/api/v1/admin/members/"+u.member+"/suspend", nil, http.StatusConflict)
	e.admin.do("POST", "/api/v1/admin/members/"+u.member+"/reactivate", nil, http.StatusNoContent)
	login(t, e.base, e.email("sospesa"), testPassword)

	// Richiedente del portale: i ruoli diretti diventano "Richiedente (portale)".
	e.admin.do("PATCH", "/api/v1/admin/members/"+u.member, map[string]any{"member_type": "requester"}, http.StatusNoContent)
	m := e.memberDetail(u.member)
	if m["member_type"] != "requester" || !strings.Contains(fmt.Sprint(m["roles"]), "requester") || strings.Contains(fmt.Sprint(m["roles"]), "editor") {
		t.Fatalf("cambio di tipo: %v %v", m["member_type"], m["roles"])
	}
	e.admin.do("PATCH", "/api/v1/admin/members/"+u.member, map[string]any{"member_type": "standard", "display_name": "Di nuovo membro"}, http.StatusNoContent)
	m = e.memberDetail(u.member)
	if m["member_type"] != "standard" || !strings.Contains(fmt.Sprint(m["roles"]), "viewer") || m["display_name"] != "Di nuovo membro" {
		t.Fatalf("ritorno a membro: %v %v", m["member_type"], m["roles"])
	}

	// Reimpostazione della password: link monouso, sessioni chiuse.
	v := login(t, e.base, e.email("sospesa"), testPassword)
	link := e.admin.do("POST", "/api/v1/admin/members/"+u.member+"/password-link", nil, http.StatusOK)["link"].(map[string]any)
	tok := strings.TrimPrefix(link["path"].(string), "/password/")
	anon := anonClient(t, e.base)
	if info := anon.do("GET", "/api/v1/auth/password-reset/"+tok, nil, http.StatusOK); info["email"] != e.email("sospesa") {
		t.Fatalf("link della password: %v", info)
	}
	anon.do("POST", "/api/v1/auth/password-reset/"+tok, map[string]any{"password": "nuova-password-di-prova"}, http.StatusNoContent)
	anon.do("POST", "/api/v1/auth/password-reset/"+tok, map[string]any{"password": "altra-password-di-prova"}, http.StatusNotFound)
	v.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	login(t, e.base, e.email("sospesa"), "nuova-password-di-prova")

	// Un account che vale anche in un altro tenant non si reimposta da qui.
	other := newTenant(t, d)
	res := e.admin.do("POST", "/api/v1/admin/members", map[string]any{"email": adminEmail(t, other.AdminAccountID), "mode": "create"}, http.StatusCreated)
	if res["link"] != nil {
		t.Fatalf("account esistente: nessun link della password atteso: %v", res["link"])
	}
	shared := res["member_id"].(string)
	if m := e.memberDetail(shared); m["other_tenants"] != true {
		t.Fatalf("account condiviso non segnalato: %v", m["other_tenants"])
	}
	e.admin.do("POST", "/api/v1/admin/members/"+shared+"/password-link", nil, http.StatusConflict)
	e.admin.do("POST", "/api/v1/admin/members/"+shared+"/mfa-reset", nil, http.StatusConflict)
}

// ---------------------------------------------------------------------
// Ruoli personalizzati
// ---------------------------------------------------------------------

func TestAdminCustomRoles(t *testing.T) {
	e := newAdminEnv(t)
	u := e.createMember("revisore", "viewer")
	u.do("GET", "/api/v1/audit", nil, http.StatusForbidden)

	roles := e.admin.do("GET", "/api/v1/admin/roles", nil, http.StatusOK)
	if perms := roles["permissions"].([]any); len(perms) < 10 || perms[0].(map[string]any)["description"] == "" {
		t.Fatalf("catalogo dei permessi: %v", perms)
	}
	editor := e.roleID("editor")
	// I ruoli di sistema non si modificano né si cancellano: si duplicano.
	e.admin.do("PATCH", "/api/v1/admin/roles/"+editor, map[string]any{"name": "Editor", "permissions": []string{"element.read"}}, http.StatusConflict)
	e.admin.do("DELETE", "/api/v1/admin/roles/"+editor, nil, http.StatusConflict)
	e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Tutto", "permissions": []string{"*"}}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Strano", "permissions": []string{"volare"}}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Vuoto", "permissions": []string{}}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "editor", "permissions": []string{"element.read"}}, http.StatusUnprocessableEntity)
	copyID := e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Editor con portafoglio", "copy_of": editor,
		"permissions": []string{"element.read", "element.write", "comment.write", "portfolio.admin"}}, http.StatusCreated)["id"].(string)

	role := e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Revisore dei conti", "description": "Legge tutto e l'audit",
		"permissions": []string{"audit.read", "element.read", "audit.read"}}, http.StatusCreated)["id"].(string)
	asg := e.admin.do("POST", "/api/v1/admin/roles/"+role+"/assignments", map[string]any{"member_id": u.member}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/roles/"+role+"/assignments", map[string]any{"member_id": u.member}, http.StatusConflict)
	e.admin.do("POST", "/api/v1/admin/roles/"+role+"/assignments", map[string]any{}, http.StatusUnprocessableEntity)
	u.do("GET", "/api/v1/audit", nil, http.StatusOK)

	// Tramite gruppo.
	w := e.createMember("revisora", "viewer")
	g := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Revisori"}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/groups/"+g+"/members", map[string]any{"member_id": w.member}, http.StatusNoContent)
	e.admin.do("POST", "/api/v1/admin/roles/"+role+"/assignments", map[string]any{"group_id": g}, http.StatusCreated)
	w.do("GET", "/api/v1/audit", nil, http.StatusOK)
	if m := e.memberDetail(w.member); !strings.Contains(fmt.Sprint(m["roles"]), "via_group:Revisori") {
		t.Fatalf("ruolo tramite gruppo non mostrato: %v", m["roles"])
	}

	// Togliere un permesso al ruolo vale subito.
	e.admin.do("PATCH", "/api/v1/admin/roles/"+role, map[string]any{"name": "Revisore dei conti", "permissions": []string{"element.read"}}, http.StatusNoContent)
	u.do("GET", "/api/v1/audit", nil, http.StatusForbidden)
	e.admin.do("PATCH", "/api/v1/admin/roles/"+role, map[string]any{"name": "Revisore dei conti", "permissions": []string{"element.read", "audit.read"}}, http.StatusNoContent)
	e.admin.do("DELETE", "/api/v1/admin/role-assignments/"+asg, nil, http.StatusNoContent)
	u.do("GET", "/api/v1/audit", nil, http.StatusForbidden)
	e.admin.do("DELETE", "/api/v1/admin/roles/"+role, nil, http.StatusNoContent)
	w.do("GET", "/api/v1/audit", nil, http.StatusForbidden)
	e.admin.do("DELETE", "/api/v1/admin/roles/"+copyID, nil, http.StatusNoContent)

	// Gruppi: gerarchia senza cicli, sistema intoccabile, uso nei processi.
	child := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Revisori interni", "parent_id": g}, http.StatusCreated)["id"].(string)
	e.admin.do("PATCH", "/api/v1/admin/groups/"+g, map[string]any{"name": "Revisori", "parent_id": child}, http.StatusUnprocessableEntity)
	e.admin.do("PATCH", "/api/v1/admin/groups/"+g, map[string]any{"name": "Revisori", "parent_id": g}, http.StatusUnprocessableEntity)
	all := e.groupID("Tutti i membri")
	e.admin.do("PATCH", "/api/v1/admin/groups/"+all, map[string]any{"name": "Tutti"}, http.StatusConflict)
	e.admin.do("DELETE", "/api/v1/admin/groups/"+all, nil, http.StatusConflict)
	e.admin.do("POST", "/api/v1/admin/groups/"+all+"/members", map[string]any{"member_id": w.member}, http.StatusConflict)
	pmo := e.groupID("PMO")
	var usage map[string]any
	for _, it := range e.admin.do("GET", "/api/v1/admin/groups", nil, http.StatusOK)["items"].([]any) {
		if it.(map[string]any)["id"] == pmo {
			usage = it.(map[string]any)["usage"].(map[string]any)
		}
	}
	if len(usage["processes"].([]any)) == 0 {
		t.Fatalf("il gruppo PMO è usato dalle regole del processo del demand: %v", usage)
	}
	e.admin.do("PATCH", "/api/v1/admin/groups/"+pmo, map[string]any{"name": "Ufficio PMO"}, http.StatusConflict)
	e.admin.do("DELETE", "/api/v1/admin/groups/"+pmo, nil, http.StatusConflict)
	e.admin.do("DELETE", "/api/v1/admin/groups/"+child, nil, http.StatusNoContent)
	e.admin.do("DELETE", "/api/v1/admin/groups/"+g+"/members/"+w.member, nil, http.StatusNoContent)

	// Ruoli sugli oggetti: elenco con utilizzi, creazione e rinomina (la chiave resta).
	sh := e.admin.do("GET", "/api/v1/admin/stakeholder-roles", nil, http.StatusOK)["items"].([]any)
	var sponsor map[string]any
	for _, it := range sh {
		if it.(map[string]any)["key"] == "sponsor" {
			sponsor = it.(map[string]any)
		}
	}
	if sponsor == nil || len(sponsor["processes"].([]any)) == 0 {
		t.Fatalf("ruolo sponsor e processi che lo usano: %v", sponsor)
	}
	e.admin.do("PATCH", "/api/v1/admin/stakeholder-roles/"+sponsor["id"].(string), map[string]any{"name": "Sponsor di business"}, http.StatusNoContent)
	e.admin.do("POST", "/api/v1/admin/stakeholder-roles", map[string]any{"key": "Data Owner", "name": "Data owner"}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/stakeholder-roles", map[string]any{"key": "data_owner", "name": "Data owner"}, http.StatusCreated)
	e.admin.do("POST", "/api/v1/admin/stakeholder-roles", map[string]any{"key": "data_owner", "name": "Doppione"}, http.StatusUnprocessableEntity)
}

// ---------------------------------------------------------------------
// Ultimo amministratore
// ---------------------------------------------------------------------

func TestAdminLastAdminProtected(t *testing.T) {
	e := newAdminEnv(t)
	adminRole := e.roleID("tenant_admin")
	assignment := func(memberID string) string {
		for _, r := range e.memberDetail(memberID)["roles"].([]any) {
			rr := r.(map[string]any)
			if rr["id"] == adminRole && rr["via_group"] == nil {
				return rr["assignment_id"].(string)
			}
		}
		t.Fatalf("assegnazione di amministratore non trovata per %s", memberID)
		return ""
	}
	mine := assignment(e.admin.member)
	e.admin.do("DELETE", "/api/v1/admin/role-assignments/"+mine, nil, http.StatusConflict)
	e.admin.do("PATCH", "/api/v1/admin/members/"+e.admin.member, map[string]any{"member_type": "requester"}, http.StatusConflict)

	// Con un secondo amministratore (tramite gruppo) il primo può lasciare il ruolo.
	b := e.createMember("seconda", "viewer")
	g := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Amministratori"}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/groups/"+g+"/members", map[string]any{"member_id": b.member}, http.StatusNoContent)
	e.admin.do("POST", "/api/v1/admin/roles/"+adminRole+"/assignments", map[string]any{"group_id": g}, http.StatusCreated)
	e.admin.do("DELETE", "/api/v1/admin/role-assignments/"+mine, nil, http.StatusNoContent)
	e.admin.do("GET", "/api/v1/admin/members", nil, http.StatusForbidden) // non è più amministratore

	// Ora l'unica amministratrice è b, tramite il gruppo.
	b.do("DELETE", "/api/v1/admin/groups/"+g+"/members/"+b.member, nil, http.StatusConflict)
	b.do("DELETE", "/api/v1/admin/groups/"+g, nil, http.StatusConflict)
	b.do("POST", "/api/v1/admin/members/"+b.member+"/suspend", nil, http.StatusConflict)
	// Un ruolo personalizzato con tenant.admin conta come amministratore.
	custom := b.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Amministrazione persone", "permissions": []string{"tenant.admin", "element.read"}}, http.StatusCreated)["id"].(string)
	b.do("POST", "/api/v1/admin/roles/"+custom+"/assignments", map[string]any{"member_id": e.admin.member}, http.StatusCreated)
	b.do("DELETE", "/api/v1/admin/groups/"+g+"/members/"+b.member, nil, http.StatusNoContent)
	// Ora l'unico amministratore è il primo, con il ruolo personalizzato.
	e.admin.do("PATCH", "/api/v1/admin/roles/"+custom, map[string]any{"name": "Amministrazione persone", "permissions": []string{"element.read"}}, http.StatusConflict)
	e.admin.do("DELETE", "/api/v1/admin/roles/"+custom, nil, http.StatusConflict)
	var body map[string]any
	resp := e.admin.do("DELETE", "/api/v1/admin/roles/"+custom, nil, http.StatusConflict)
	body = resp
	if body["code"] != "admin.last_admin" {
		t.Fatalf("codice dell'errore: %v", body)
	}
}

// ---------------------------------------------------------------------
// MFA con TOTP
// ---------------------------------------------------------------------

func TestMFATOTP(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	u := e.createMember("totp", "editor")
	email := e.email("totp")

	st := u.do("GET", "/api/v1/me/mfa", nil, http.StatusOK)
	if st["available"] != true || st["enabled"] != false {
		t.Fatalf("stato iniziale: %v", st)
	}
	setup := u.do("POST", "/api/v1/me/mfa/setup", nil, http.StatusOK)
	secret := strings.ReplaceAll(setup["secret"].(string), " ", "")
	if !strings.HasPrefix(setup["qr_svg"].(string), "<svg") || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/Regia:") {
		t.Fatalf("attivazione: %v", setup)
	}
	// Prima della conferma il segreto non vale.
	var pending, active []byte
	if err := d.Owner.QueryRow(ctx, "select mfa_pending_enc, mfa_secret_enc from accounts where lower(email) = $1", email).Scan(&pending, &active); err != nil {
		t.Fatal(err)
	}
	if pending == nil || active != nil || bytes.Contains(pending, []byte(secret)) {
		t.Fatal("segreto in attesa non cifrato o già attivo")
	}
	u.do("POST", "/api/v1/me/mfa/enable", map[string]any{"code": wrongCode(t, secret)}, http.StatusUnprocessableEntity)
	now := time.Now()
	enabled := u.do("POST", "/api/v1/me/mfa/enable", map[string]any{"code": codeAt(t, secret, now)}, http.StatusOK)
	codes := enabled["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("codici di recupero: %d", len(codes))
	}
	st = u.do("GET", "/api/v1/me/mfa", nil, http.StatusOK)
	if st["enabled"] != true || st["recovery_codes_left"] != float64(10) || st["session_mfa"] != true {
		t.Fatalf("stato dopo l'attivazione: %v", st)
	}
	if err := d.Owner.QueryRow(ctx, "select mfa_secret_enc from accounts where lower(email) = $1", email).Scan(&active); err != nil || active == nil {
		t.Fatalf("segreto attivo: %v", err)
	}
	var plainCodes int
	if err := d.Owner.QueryRow(ctx, `select count(*) from mfa_recovery_codes r join accounts a on a.id = r.account_id
		where lower(a.email) = $1 and encode(r.code_hash, 'escape') like '%' || $2 || '%'`, email, codes[0].(string)).Scan(&plainCodes); err != nil || plainCodes != 0 {
		t.Fatal("codici di recupero in chiaro")
	}

	// Login in due tempi.
	c := anonClient(t, e.base)
	r := c.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, http.StatusOK)
	if r["mfa_required"] != true || r["mfa_setup"] != false || r["active_tenant"] != nil {
		t.Fatalf("primo passo: %v", r)
	}
	c.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	// Il codice già usato per l'attivazione non vale una seconda volta.
	c.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codeAt(t, secret, now)}, http.StatusUnprocessableEntity)
	me := c.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codeAt(t, secret, now.Add(30*time.Second))}, http.StatusOK)
	if me["active_tenant"] == nil {
		t.Fatalf("sessione dopo il codice: %v", me)
	}
	c.do("GET", "/api/v1/me", nil, http.StatusOK)
	var mfaSession bool
	if err := d.Owner.QueryRow(ctx, `select mfa from sessions s join accounts a on a.id = s.account_id where lower(a.email) = $1
		order by s.created_at desc limit 1`, email).Scan(&mfaSession); err != nil || !mfaSession {
		t.Fatalf("sessione con MFA: %v %v", mfaSession, err)
	}
	// La sfida vale una volta: dopo l'accesso non si riusa.
	c.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codes[1].(string)}, http.StatusUnauthorized)
	// Senza il cookie della sfida non si entra.
	anonClient(t, e.base).do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codes[1].(string)}, http.StatusUnauthorized)

	// Codice di recupero, monouso.
	c2 := anonClient(t, e.base)
	c2.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, http.StatusOK)
	c2.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": strings.ToUpper(codes[0].(string))}, http.StatusOK)
	c3 := anonClient(t, e.base)
	c3.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": testPassword}, http.StatusOK)
	c3.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codes[0].(string)}, http.StatusUnprocessableEntity)

	// Nuovi codici di recupero: i vecchi non valgono più.
	resetLastStep(t, email)
	fresh := u.do("POST", "/api/v1/me/mfa/recovery-codes", map[string]any{"code": codeAt(t, secret, time.Now())}, http.StatusOK)["recovery_codes"].([]any)
	c3.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codes[2].(string)}, http.StatusUnprocessableEntity)
	c3.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": fresh[0].(string)}, http.StatusOK)

	// Reset dell'MFA da parte dell'amministratore: niente più secondo fattore, sessioni chiuse.
	e.admin.do("POST", "/api/v1/admin/members/"+u.member+"/mfa-reset", nil, http.StatusNoContent)
	c3.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	v := login(t, e.base, email, testPassword)
	if st := v.do("GET", "/api/v1/me/mfa", nil, http.StatusOK); st["enabled"] != false {
		t.Fatalf("MFA dopo il reset: %v", st)
	}

	// Riattivazione e disattivazione dal profilo (serve un codice).
	setup = v.do("POST", "/api/v1/me/mfa/setup", nil, http.StatusOK)
	secret = strings.ReplaceAll(setup["secret"].(string), " ", "")
	v.do("POST", "/api/v1/me/mfa/enable", map[string]any{"code": codeAt(t, secret, time.Now())}, http.StatusOK)
	v.do("POST", "/api/v1/me/mfa/setup", nil, http.StatusConflict) // già attiva
	resetLastStep(t, email)
	v.do("POST", "/api/v1/me/mfa/disable", map[string]any{"code": wrongCode(t, secret)}, http.StatusUnprocessableEntity)
	v.do("POST", "/api/v1/me/mfa/disable", map[string]any{"code": codeAt(t, secret, time.Now())}, http.StatusNoContent)
	login(t, e.base, email, testPassword) // di nuovo con la sola password

	// Audit (catena di piattaforma per l'account, del tenant per il reset).
	var n int
	if err := d.Owner.QueryRow(ctx, `select count(distinct action) from audit_log a join accounts ac on ac.id = a.entity_id
		where lower(ac.email) = $1 and action in ('auth.mfa_enable', 'auth.mfa_failed', 'auth.mfa_recovery_used', 'auth.mfa_disable', 'auth.mfa_recovery_regenerate')`,
		email).Scan(&n); err != nil || n != 5 {
		t.Fatalf("eventi MFA nell'audit: %d (%v)", n, err)
	}
	if err := d.Owner.QueryRow(ctx, "select count(*) from audit_log where tenant_id = $1 and action = 'member.mfa_reset'", e.ten.TenantID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reset MFA nell'audit: %d (%v)", n, err)
	}
}

func TestMFAPolicyAndLimiter(t *testing.T) {
	e := newAdminEnv(t)
	adminEmail := "admin@" + e.slug + ".test"
	other := login(t, e.base, adminEmail, testPassword) // seconda sessione dell'amministratrice
	ed := e.createMember("redattore", "editor")

	sec := e.admin.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "admins"}, http.StatusOK)
	if sec["security"].(map[string]any)["mfa_policy"] != "admins" || sec["sessions_closed"].(float64) < 1 {
		t.Fatalf("politica: %v", sec)
	}
	e.admin.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "sempre"}, http.StatusUnprocessableEntity)
	// La sessione di chi cambia la politica resta; le altre sessioni senza MFA degli amministratori no.
	e.admin.do("GET", "/api/v1/me", nil, http.StatusOK)
	other.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	ed.do("GET", "/api/v1/me", nil, http.StatusOK) // non amministratore: la politica non lo tocca
	login(t, e.base, e.email("redattore"), testPassword)

	// Al prossimo accesso l'amministratrice attiva l'MFA prima di entrare.
	c := anonClient(t, e.base)
	r := c.do("POST", "/api/v1/auth/login", map[string]any{"email": adminEmail, "password": testPassword}, http.StatusOK)
	if r["mfa_required"] != true || r["mfa_setup"] != true {
		t.Fatalf("attivazione obbligatoria: %v", r)
	}
	c.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": "123456"}, http.StatusUnauthorized) // la sfida è di attivazione
	c2 := anonClient(t, e.base)
	c2.do("POST", "/api/v1/auth/login", map[string]any{"email": adminEmail, "password": testPassword}, http.StatusOK)
	setup := c2.do("POST", "/api/v1/auth/mfa/setup", nil, http.StatusOK)
	secret := strings.ReplaceAll(setup["secret"].(string), " ", "")
	done := c2.do("POST", "/api/v1/auth/mfa/setup/confirm", map[string]any{"code": codeAt(t, secret, time.Now())}, http.StatusOK)
	if len(done["recovery_codes"].([]any)) != 10 || done["me"].(map[string]any)["active_tenant"] == nil {
		t.Fatalf("conferma dell'attivazione: %v", done)
	}
	c2.do("GET", "/api/v1/admin/members", nil, http.StatusOK)
	// Con la politica attiva l'MFA non si spegne.
	resetLastStep(t, adminEmail)
	c2.do("POST", "/api/v1/me/mfa/disable", map[string]any{"code": codeAt(t, secret, time.Now())}, http.StatusConflict)
	if st := c2.do("GET", "/api/v1/me/mfa", nil, http.StatusOK); st["required"] != true {
		t.Fatalf("obbligo non segnalato: %v", st)
	}

	// Politica "per tutti": anche il redattore attiva l'MFA al prossimo accesso, e le
	// sessioni senza MFA si chiudono (anche la prima dell'amministratrice).
	c2.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "all"}, http.StatusOK)
	ed.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	e.admin.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	if r := anonClient(t, e.base).do("POST", "/api/v1/auth/login", map[string]any{"email": e.email("redattore"), "password": testPassword}, http.StatusOK); r["mfa_setup"] != true {
		t.Fatalf("politica per tutti: %v", r)
	}
	c2.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "off"}, http.StatusOK)
	c2.member = done["me"].(map[string]any)["active_tenant"].(map[string]any)["member_id"].(string)
	e.admin = c2

	// Limitatore: 5 codici sbagliati per account, poi 429 anche con una nuova sfida.
	lim := e.createMember("limitato", "viewer")
	s := lim.do("POST", "/api/v1/me/mfa/setup", nil, http.StatusOK)
	ls := strings.ReplaceAll(s["secret"].(string), " ", "")
	lim.do("POST", "/api/v1/me/mfa/enable", map[string]any{"code": codeAt(t, ls, time.Now())}, http.StatusOK)
	lc := anonClient(t, e.base)
	lc.do("POST", "/api/v1/auth/login", map[string]any{"email": e.email("limitato"), "password": testPassword}, http.StatusOK)
	bad := wrongCode(t, ls)
	for i := 0; i < 5; i++ {
		lc.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": bad}, http.StatusUnprocessableEntity)
	}
	lc.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": bad}, http.StatusUnauthorized) // sfida esaurita
	lc.do("POST", "/api/v1/auth/login", map[string]any{"email": e.email("limitato"), "password": testPassword}, http.StatusOK)
	lc.do("POST", "/api/v1/auth/mfa/verify", map[string]any{"code": codeAt(t, ls, time.Now().Add(30*time.Second))}, http.StatusTooManyRequests)
}

// ---------------------------------------------------------------------
// Webhook
// ---------------------------------------------------------------------

type received struct {
	path    string
	headers http.Header
	body    []byte
}

type receiver struct {
	*httptest.Server
	mu    sync.Mutex
	got   []received
	fails map[string]int // richieste da far fallire per percorso
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{fails: map[string]int{}}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.got = append(rc.got, received{path: r.URL.Path, headers: r.Header.Clone(), body: b})
		fail := rc.fails[r.URL.Path] > 0
		if fail {
			rc.fails[r.URL.Path]--
		}
		rc.mu.Unlock()
		if fail {
			http.Error(w, "occupato", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(rc.Close)
	return rc
}

func (rc *receiver) on(path string) []received {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []received
	for _, g := range rc.got {
		if g.path == path {
			out = append(out, g)
		}
	}
	return out
}

func verifySignature(t *testing.T, r received, secret string) map[string]any {
	t.Helper()
	sig := r.headers.Get("X-Regia-Signature")
	var ts int64
	var v1 string
	for _, part := range strings.Split(sig, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			fmt.Sscan(v, &ts)
		case "v1":
			v1 = v
		}
	}
	if ts == 0 || v1 == "" || webhooks.Sign(secret, ts, r.body) != v1 {
		t.Fatalf("firma non valida: %q", sig)
	}
	if webhooks.Sign("whsec_altro", ts, r.body) == v1 {
		t.Fatal("la firma non dipende dal segreto")
	}
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWebhooksDelivery(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	rc := newReceiver(t)
	rc.fails["/tutto"] = 1

	all := e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Tutto", "url": rc.URL + "/tutto",
		"events": []string{"element.created", "element.phase_changed", "comment.created"}}, http.StatusCreated)
	allID, allSecret := all["id"].(string), all["secret"].(string)
	if !strings.HasPrefix(allSecret, "whsec_") {
		t.Fatalf("segreto: %q", allSecret)
	}
	norm := e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Solo normative", "url": rc.URL + "/norm",
		"events": []string{"element.created"}, "filter": "driver: normativo"}, http.StatusCreated)
	normID, normSecret := norm["id"].(string), norm["secret"].(string)
	e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Rotto", "url": rc.URL, "events": []string{"element.created"}, "filter": "driver:"}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Ignoto", "url": rc.URL, "events": []string{"element.exploded"}}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Nessuno", "url": rc.URL, "events": []string{}}, http.StatusUnprocessableEntity)
	// Il segreto è cifrato a riposo.
	var sealed []byte
	if err := d.Owner.QueryRow(ctx, "select secret_enc from webhooks where id = $1", allID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(allSecret)) {
		t.Fatalf("segreto del webhook in chiaro (%v)", err)
	}
	list := e.admin.do("GET", "/api/v1/admin/webhooks", nil, http.StatusOK)
	if len(list["items"].([]any)) != 2 || list["version"] != float64(1) {
		t.Fatalf("elenco: %v", list)
	}

	regulatory := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Adeguamento normativo", "description": "x", "driver": "regulatory"}, http.StatusCreated)
	e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Efficienza del magazzino", "description": "y", "driver": "efficiency"}, http.StatusCreated)
	var events int
	if err := d.Owner.QueryRow(ctx, "select count(*) from outbox_events where tenant_id = $1 and kind = 'webhook'", e.ten.TenantID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("eventi nell'outbox: %d (%v)", events, err)
	}
	if _, err := e.hooks.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Filtro: solo la richiesta normativa arriva al secondo webhook.
	got := rc.on("/norm")
	if len(got) != 1 {
		t.Fatalf("consegne filtrate: %d, attesa 1", len(got))
	}
	p := verifySignature(t, got[0], normSecret)
	el := p["element"].(map[string]any)
	if p["version"] != float64(1) || p["type"] != "element.created" || el["key"] != regulatory["key"] || el["title"] != "Adeguamento normativo" ||
		p["tenant"].(map[string]any)["slug"] != e.slug || p["actor"].(map[string]any)["name"] != "Amministratrice" {
		t.Fatalf("payload: %s", got[0].body)
	}
	if got[0].headers.Get("X-Regia-Event") != "element.created" || got[0].headers.Get("X-Regia-Event-Id") != p["id"] {
		t.Fatalf("intestazioni: %v", got[0].headers)
	}

	// Retry: la prima consegna verso /tutto ha ricevuto 503 e resta in attesa con l'errore.
	dl := e.admin.do("GET", "/api/v1/admin/webhooks/"+allID+"/deliveries", nil, http.StatusOK)["items"].([]any)
	var failed map[string]any
	delivered := 0
	for _, it := range dl {
		x := it.(map[string]any)
		switch x["status"] {
		case "pending":
			failed = x
		case "delivered":
			delivered++
		}
	}
	if failed == nil || delivered != 1 || failed["response_status"] != float64(503) || failed["attempts"] != float64(1) || failed["next_attempt_at"] == nil {
		t.Fatalf("consegne dopo il primo giro: %v", dl)
	}
	if webhooks.Backoff(1) != 30*time.Second || webhooks.Backoff(3) != 2*time.Minute || webhooks.Backoff(30) != 6*time.Hour {
		t.Fatal("attese crescenti dei tentativi")
	}
	// Non è ancora il momento: un altro giro non la rimanda.
	before := len(rc.on("/tutto"))
	_, _ = e.hooks.RunOnce(ctx)
	if len(rc.on("/tutto")) != before {
		t.Fatal("consegna ritentata prima dell'attesa")
	}
	if _, err := d.Owner.Exec(ctx, "update webhook_deliveries set next_attempt_at = now() where id = $1", failed["id"]); err != nil {
		t.Fatal(err)
	}
	_, _ = e.hooks.RunOnce(ctx)
	for _, it := range e.admin.do("GET", "/api/v1/admin/webhooks/"+allID+"/deliveries", nil, http.StatusOK)["items"].([]any) {
		x := it.(map[string]any)
		if x["id"] == failed["id"] && (x["status"] != "delivered" || x["attempts"] != float64(2) || x["response_status"] != float64(204)) {
			t.Fatalf("consegna dopo il nuovo tentativo: %v", x)
		}
	}
	for _, r := range rc.on("/tutto") {
		verifySignature(t, r, allSecret)
	}

	// Reinvia: stesso corpo, stesso id evento, nuova consegna.
	re := e.admin.do("POST", "/api/v1/admin/webhook-deliveries/"+failed["id"].(string)+"/redeliver", nil, http.StatusAccepted)
	_, _ = e.hooks.RunOnce(ctx)
	last := rc.on("/tutto")
	if lr := last[len(last)-1]; lr.headers.Get("X-Regia-Event-Id") != failed["event_id"] || lr.headers.Get("X-Regia-Delivery") != re["delivery_id"] {
		t.Fatalf("reinvio: %v", lr.headers)
	}

	// Cambio di fase e commento.
	e.admin.do("POST", "/api/v1/elements/"+regulatory["id"].(string)+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	e.admin.do("POST", "/api/v1/elements/"+regulatory["id"].(string)+"/comments", map[string]any{"body": "Serve il parere della compliance"}, http.StatusCreated)
	_, _ = e.hooks.RunOnce(ctx)
	types := map[string]map[string]any{}
	for _, r := range rc.on("/tutto") {
		p := verifySignature(t, r, allSecret)
		types[p["type"].(string)] = p
	}
	if ph := types["element.phase_changed"]; ph == nil || ph["data"].(map[string]any)["to"] != "Inviata" || ph["data"].(map[string]any)["from"] != "Bozza" {
		t.Fatalf("evento di cambio di fase: %v", ph)
	}
	if cm := types["comment.created"]; cm == nil || !strings.Contains(fmt.Sprint(cm["data"]), "parere della compliance") {
		t.Fatalf("evento di commento: %v", cm)
	}

	// Prova, rotazione del segreto, disattivazione.
	newSecret := e.admin.do("POST", "/api/v1/admin/webhooks/"+normID+"/secret", nil, http.StatusOK)["secret"].(string)
	e.admin.do("POST", "/api/v1/admin/webhooks/"+normID+"/ping", nil, http.StatusAccepted)
	_, _ = e.hooks.RunOnce(ctx)
	pings := rc.on("/norm")
	if p := verifySignature(t, pings[len(pings)-1], newSecret); p["type"] != "ping" {
		t.Fatalf("evento di prova: %v", p)
	}
	e.admin.do("PATCH", "/api/v1/admin/webhooks/"+allID, map[string]any{"name": "Tutto", "url": rc.URL + "/tutto",
		"events": []string{"element.created"}, "active": false}, http.StatusNoContent)
	e.admin.do("PATCH", "/api/v1/admin/webhooks/"+normID, map[string]any{"name": "Solo normative", "url": rc.URL + "/norm",
		"events": []string{"element.created"}, "active": false}, http.StatusNoContent)
	e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Silenziosa", "description": "z", "driver": "regulatory"}, http.StatusCreated)
	if err := d.Owner.QueryRow(ctx, "select count(*) from outbox_events where tenant_id = $1 and kind = 'webhook' and dispatched_at is null", e.ten.TenantID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("eventi accodati con i webhook spenti: %d", events)
	}

	// Dopo troppi tentativi la consegna è fallita definitivamente.
	rc.fails["/rotto"] = 100
	broken := e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Rotto", "url": rc.URL + "/rotto", "events": []string{"element.created"}}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/webhooks/"+broken+"/ping", nil, http.StatusAccepted)
	if _, err := d.Owner.Exec(ctx, "update webhook_deliveries set attempts = 9 where webhook_id = $1", broken); err != nil {
		t.Fatal(err)
	}
	_, _ = e.hooks.RunOnce(ctx)
	bd := e.admin.do("GET", "/api/v1/admin/webhooks/"+broken+"/deliveries", nil, http.StatusOK)["items"].([]any)
	if bd[0].(map[string]any)["status"] != "failed" || !strings.Contains(fmt.Sprint(bd[0].(map[string]any)["last_error"]), "503") {
		t.Fatalf("consegna oltre il massimo dei tentativi: %v", bd[0])
	}
	e.admin.do("DELETE", "/api/v1/admin/webhooks/"+broken, nil, http.StatusNoContent)
	e.admin.do("GET", "/api/v1/admin/webhooks/"+broken+"/deliveries", nil, http.StatusNotFound)

	var audited int
	if err := d.Owner.QueryRow(ctx, `select count(distinct action) from audit_log where tenant_id = $1 and entity_type = 'webhook'
		and action in ('webhook.create', 'webhook.update', 'webhook.delete', 'webhook.secret_rotate', 'webhook.ping', 'webhook.redeliver')`, e.ten.TenantID).Scan(&audited); err != nil || audited != 6 {
		t.Fatalf("audit dei webhook: %d (%v)", audited, err)
	}
}

func TestWebhooksSSRF(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	for ip, blocked := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.16.0.1": true, "192.168.1.1": true, "169.254.169.254": true, "100.64.0.1": true,
		"0.0.0.0": true, "::1": true, "fc00::1": true, "fe80::1": true, "::ffff:127.0.0.1": true, "64:ff9b::a00:1": true,
		"8.8.8.8": false, "2a00:1450:4001:80b::200e": false,
	} {
		if got := webhooks.BlockedIP(netip.MustParseAddr(ip)); got != blocked {
			t.Errorf("BlockedIP(%s) = %v, atteso %v", ip, got, blocked)
		}
	}
	strict := webhooks.New(webhooks.Options{DB: d, Eng: e.eng, Box: e.box, Timeout: 2 * time.Second})
	a := webhooks.Actor{TenantID: e.ten.TenantID, AccountID: e.ten.AdminAccountID, MemberID: e.ten.AdminMemberID}
	for _, u := range []string{
		"http://hooks.example.com/x", "https://127.0.0.1/x", "https://10.0.0.5/", "https://169.254.169.254/latest/meta-data",
		"https://localhost:8443/x", "https://[::1]/x", "https://intranet/x", "https://db.internal/x", "https://user:pw@hooks.example.com/",
		"ftp://hooks.example.com/", "https://[fd00::1]/",
	} {
		_, _, err := strict.Create(ctx, a, webhooks.Input{Name: "x", URL: u, Events: []string{"element.created"}})
		var we *webhooks.Error
		if !errors.As(err, &we) || we.Status != 422 {
			t.Errorf("URL %s accettato (%v)", u, err)
		}
	}
	id, _, err := strict.Create(ctx, a, webhooks.Input{Name: "Pubblico", URL: "https://hooks.example.com/regia", Events: []string{"element.created"}})
	if err != nil {
		t.Fatal(err)
	}
	// Alla consegna conta l'indirizzo vero: un nome che "diventa" 127.0.0.1 (DNS rebinding)
	// è bloccato dal dialer. Si simula puntando la riga a un server locale.
	rc := newReceiver(t)
	if _, err := d.Owner.Exec(ctx, "update webhooks set url = $2 where id = $1", id, rc.URL+"/interno"); err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Ping(ctx, a, id); err != nil {
		t.Fatal(err)
	}
	_, _ = strict.RunOnce(ctx)
	if n := len(rc.on("/interno")); n != 0 {
		t.Fatalf("il server interno ha ricevuto %d richieste", n)
	}
	items, err := strict.Deliveries(ctx, e.ten.TenantID, id)
	if err != nil || len(items) != 1 || items[0].Status != "pending" || items[0].LastError == nil ||
		!strings.Contains(*items[0].LastError, "destinazione non consentita") {
		t.Fatalf("consegna verso un indirizzo interno: %+v %v", items, err)
	}
	// I redirect non si seguono (un 302 verso un indirizzo interno sarebbe un'altra via).
	redir := httptest.NewServer(http.RedirectHandler(rc.URL+"/dopo-redirect", http.StatusFound))
	defer redir.Close()
	lax := e.hooks
	rid := e.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Redirect", "url": redir.URL, "events": []string{"element.created"}}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/webhooks/"+rid+"/ping", nil, http.StatusAccepted)
	_, _ = lax.RunOnce(ctx)
	if n := len(rc.on("/dopo-redirect")); n != 0 {
		t.Fatal("redirect seguito")
	}
}

// ---------------------------------------------------------------------
// Audit e isolamento tra tenant
// ---------------------------------------------------------------------

func TestAdminAuditAndChain(t *testing.T) {
	e := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	u := e.createMember("auditor", "auditor")
	role := e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Solo lettura", "permissions": []string{"element.read"}}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/roles/"+role+"/assignments", map[string]any{"member_id": u.member}, http.StatusCreated)
	e.admin.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "admins"}, http.StatusOK)
	e.admin.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "off"}, http.StatusOK)

	page := u.do("GET", "/api/v1/audit?action=member", nil, http.StatusOK)
	items := page["items"].([]any)
	if len(items) == 0 {
		t.Fatal("nessun evento member.*")
	}
	for _, it := range items {
		if a := it.(map[string]any)["action"].(string); !strings.HasPrefix(a, "member.") {
			t.Fatalf("filtro per azione: %s", a)
		}
	}
	if actors := page["actors"].(map[string]any); actors[e.admin.member] != "Amministratrice" {
		t.Fatalf("nomi degli autori: %v", actors)
	}
	roleEvents := u.do("GET", "/api/v1/audit?entity_type=role&entity_id="+role, nil, http.StatusOK)["items"].([]any)
	if len(roleEvents) != 2 {
		t.Fatalf("eventi del ruolo: %d", len(roleEvents))
	}
	byActor := u.do("GET", "/api/v1/audit?actor="+e.admin.member+"&action=tenant.security.update", nil, http.StatusOK)["items"].([]any)
	if len(byActor) != 2 {
		t.Fatalf("eventi della politica: %d", len(byActor))
	}
	today := time.Now().Format("2006-01-02")
	if n := len(u.do("GET", "/api/v1/audit?from="+today+"&to="+today, nil, http.StatusOK)["items"].([]any)); n == 0 {
		t.Fatal("filtro per data di oggi vuoto")
	}
	if n := len(u.do("GET", "/api/v1/audit?from=2000-01-01&to=2000-01-02", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("filtro per data passata: %d", n)
	}
	u.do("GET", "/api/v1/audit?actor=nonvalido", nil, http.StatusBadRequest)
	u.do("GET", "/api/v1/audit?from=ieri", nil, http.StatusBadRequest)

	v := u.do("GET", "/api/v1/audit/verify", nil, http.StatusOK)
	if v["intact"] != true || v["entries"].(float64) < 5 || v["last_hash"] == nil {
		t.Fatalf("verifica della catena: %v", v)
	}
	login(t, e.base, e.email("auditor"), testPassword) // senza audit.read? l'auditor ce l'ha
	viewer := e.createMember("lettore", "viewer")
	viewer.do("GET", "/api/v1/audit/verify", nil, http.StatusForbidden)

	// Manomissione: la verifica indica il primo evento rotto.
	if _, err := d.Owner.Exec(ctx, `update audit_log set payload = '{"manomesso": true}' where tenant_id = $1 and seq = 2`, e.ten.TenantID); err != nil {
		t.Fatal(err)
	}
	v = u.do("GET", "/api/v1/audit/verify", nil, http.StatusOK)
	if v["intact"] != false || v["broken_seq"] != float64(2) {
		t.Fatalf("catena manomessa: %v", v)
	}
}

func TestAdminTenantIsolation(t *testing.T) {
	a := newAdminEnv(t)
	d := needDB(t)
	ctx := context.Background()
	// Secondo tenant sullo stesso server.
	slugB := a.slug + "-b"
	tb, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	b := login(t, a.base, "admin@"+slugB+".test", testPassword)
	ua := a.createMember("persona", "editor")
	roleA := a.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Ruolo di A", "permissions": []string{"element.read"}}, http.StatusCreated)["id"].(string)
	asgA := a.admin.do("POST", "/api/v1/admin/roles/"+roleA+"/assignments", map[string]any{"member_id": ua.member}, http.StatusCreated)["id"].(string)
	groupA := a.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Gruppo di A"}, http.StatusCreated)["id"].(string)
	rc := newReceiver(t)
	hookA := a.admin.do("POST", "/api/v1/admin/webhooks", map[string]any{"name": "Di A", "url": rc.URL, "events": []string{"element.created"}}, http.StatusCreated)["id"].(string)
	a.admin.do("POST", "/api/v1/admin/webhooks/"+hookA+"/ping", nil, http.StatusAccepted)
	_, _ = a.hooks.RunOnce(ctx)
	delA := a.admin.do("GET", "/api/v1/admin/webhooks/"+hookA+"/deliveries", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)["id"].(string)

	for _, it := range b.do("GET", "/api/v1/admin/members", nil, http.StatusOK)["items"].([]any) {
		if it.(map[string]any)["id"] == ua.member {
			t.Fatal("B vede un membro di A")
		}
	}
	b.do("GET", "/api/v1/admin/members/"+ua.member, nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/members/"+ua.member+"/suspend", nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/members/"+ua.member+"/password-link", nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/members/"+ua.member+"/mfa-reset", nil, http.StatusNotFound)
	b.do("PATCH", "/api/v1/admin/members/"+ua.member, map[string]any{"display_name": "Rubato"}, http.StatusNotFound)
	b.do("PATCH", "/api/v1/admin/roles/"+roleA, map[string]any{"name": "Rubato", "permissions": []string{"element.read"}}, http.StatusNotFound)
	b.do("DELETE", "/api/v1/admin/roles/"+roleA, nil, http.StatusNotFound)
	b.do("DELETE", "/api/v1/admin/role-assignments/"+asgA, nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/roles/"+roleA+"/assignments", map[string]any{"member_id": b.member}, http.StatusUnprocessableEntity)
	b.do("PATCH", "/api/v1/admin/groups/"+groupA, map[string]any{"name": "Rubato"}, http.StatusNotFound)
	b.do("DELETE", "/api/v1/admin/groups/"+groupA, nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/groups/"+groupA+"/members", map[string]any{"member_id": b.member}, http.StatusNotFound)
	b.do("GET", "/api/v1/admin/webhooks/"+hookA+"/deliveries", nil, http.StatusNotFound)
	b.do("PATCH", "/api/v1/admin/webhooks/"+hookA, map[string]any{"name": "x", "url": rc.URL, "events": []string{"element.created"}}, http.StatusNotFound)
	b.do("DELETE", "/api/v1/admin/webhooks/"+hookA, nil, http.StatusNotFound)
	b.do("POST", "/api/v1/admin/webhook-deliveries/"+delA+"/redeliver", nil, http.StatusNotFound)
	if n := len(b.do("GET", "/api/v1/admin/webhooks", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("B vede %d webhook", n)
	}
	// B non può aggiungere a sé un membro di A né creare un ruolo con lo stesso nome "rubando" quello di A.
	b.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Ruolo di A", "permissions": []string{"element.read"}}, http.StatusCreated)
	// La politica MFA di B non tocca A.
	b.do("PUT", "/api/v1/admin/security", map[string]any{"mfa_policy": "all"}, http.StatusOK)
	if r := anonClient(t, a.base).do("POST", "/api/v1/auth/login", map[string]any{"email": a.email("persona"), "password": testPassword}, http.StatusOK); r["mfa_required"] == true {
		t.Fatal("la politica di B vale per un membro di A")
	}
	// Audit: ogni tenant vede solo la sua catena.
	for _, it := range b.do("GET", "/api/v1/audit?limit=500", nil, http.StatusOK)["items"].([]any) {
		if id, _ := it.(map[string]any)["entity_id"].(string); id == ua.member || id == roleA || id == hookA {
			t.Fatal("B vede eventi di A nell'audit")
		}
	}
	var name string
	if err := d.Owner.QueryRow(ctx, "select display_name from members where id = $1", ua.member).Scan(&name); err != nil || name == "Rubato" {
		t.Fatalf("membro di A modificato da B: %q %v", name, err)
	}
	_ = tb
}

// Creazioni e inviti concorrenti di membri: nessuna deve fallire (deadlock tra i lock di riga e la
// catena di audit), e ogni persona ha il suo evento.
func TestMembriConcorrenti(t *testing.T) {
	e := newAdminEnv(t)
	const n = 110
	var wg sync.WaitGroup
	codes := make([]int, n)
	sem := make(chan struct{}, 10)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mode := "create"
			if i%2 == 1 {
				mode = "invite"
			}
			body, _ := json.Marshal(map[string]any{"email": e.email(fmt.Sprintf("conc%d", i)), "display_name": fmt.Sprintf("Conc %d", i), "member_type": "standard", "mode": mode, "role_ids": []string{}, "group_ids": []string{}})
			req, _ := http.NewRequest("POST", e.base+"/api/v1/admin/members", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := e.admin.c.Do(req)
			if err != nil {
				codes[i] = -1
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusCreated {
			t.Errorf("creazione %d: stato %d, atteso 201", i, c)
		}
	}
}

// Più richieste simultanee per la stessa persona: una sola vince, le altre ricevono 409 (non 500).
func TestMembroStessaEmailConcorrente(t *testing.T) {
	e := newAdminEnv(t)
	const n = 12
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"email": e.email("stessa"), "display_name": "Stessa", "mode": "create"})
			req, _ := http.NewRequest("POST", e.base+"/api/v1/admin/members", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := e.admin.c.Do(req)
			if err != nil {
				codes[i] = -1
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	created := 0
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("richiesta %d: stato %d, atteso 201 o 409", i, c)
		}
	}
	if created != 1 {
		t.Errorf("create %d volte, atteso 1", created)
	}
}
