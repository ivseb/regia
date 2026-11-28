package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/config"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Identità aziendale (migrazioni 0102-0103): scalata chiusa, esterni a zero ruoli,
// OIDC con creazione al primo accesso e gruppi mappati, SCIM 2.0.

func errCode(t *testing.T, u *user, method, path string, body any, want int) string {
	t.Helper()
	c, _ := u.do(method, path, body, want)["code"].(string)
	return c
}

func TestScalataChiusa(t *testing.T) {
	e := newAdminEnv(t)
	adminRole := e.roleID("tenant_admin")
	editor := e.roleID("editor")
	viewer := e.roleID("viewer")

	// Un ruolo personalizzato con tenant.admin ma senza tutto il resto.
	delegato := e.admin.do("POST", "/api/v1/admin/roles", map[string]any{"name": "Amministrazione delegata",
		"permissions": []string{"tenant.admin", "element.read"}}, http.StatusCreated)["id"].(string)
	d := e.createMember("delegato")
	if m := e.memberDetail(d.member); len(m["roles"].([]any)) != 0 {
		t.Fatalf("un membro senza ruoli scelti non deve averne: %v", m["roles"])
	}
	e.admin.do("POST", "/api/v1/admin/roles/"+delegato+"/assignments", map[string]any{"member_id": d.member}, http.StatusCreated)

	// Non si dà «*» né permessi che non si hanno, neanche a sé stessi.
	if c := errCode(t, d, "POST", "/api/v1/admin/roles/"+adminRole+"/assignments", map[string]any{"member_id": d.member}, http.StatusForbidden); c != "admin.escalation" {
		t.Fatalf("assegnarsi l'amministratore pieno: %s", c)
	}
	altro := e.createMember("altro")
	errCode(t, d, "POST", "/api/v1/admin/roles/"+editor+"/assignments", map[string]any{"member_id": altro.member}, http.StatusForbidden)
	d.do("POST", "/api/v1/admin/roles/"+viewer+"/assignments", map[string]any{"member_id": altro.member}, http.StatusCreated)

	// Invito con un ruolo più forte: rifiutato. Senza ruoli: nessun ruolo, nemmeno Lettore.
	errCode(t, d, "POST", "/api/v1/admin/members", map[string]any{"email": e.email("forte"), "mode": "invite", "role_ids": []string{adminRole}}, http.StatusForbidden)
	res := d.do("POST", "/api/v1/admin/members", map[string]any{"email": e.email("esterno"), "mode": "invite"}, http.StatusCreated)
	if m := e.memberDetail(res["member_id"].(string)); len(m["roles"].([]any)) != 0 || m["only_viewer"] != false {
		t.Fatalf("esterno invitato: %v", m["roles"])
	}

	// Gruppi con ruoli forti: non ci si entra e non ci si fa entrare nessuno.
	g := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Amministratori " + e.slug}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/roles/"+adminRole+"/assignments", map[string]any{"group_id": g}, http.StatusCreated)
	errCode(t, d, "POST", "/api/v1/admin/groups/"+g+"/members", map[string]any{"member_id": d.member}, http.StatusForbidden)
	errCode(t, d, "POST", "/api/v1/admin/members", map[string]any{"email": e.email("viagruppo"), "mode": "invite", "group_ids": []string{g}}, http.StatusForbidden)
	// Con l'ereditarietà, spostare un gruppo sotto quello degli amministratori darebbe i suoi ruoli.
	figlio := d.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Figlio " + e.slug}, http.StatusCreated)["id"].(string)
	d.do("POST", "/api/v1/admin/groups/"+figlio+"/members", map[string]any{"member_id": d.member}, http.StatusNoContent)
	errCode(t, d, "PATCH", "/api/v1/admin/groups/"+figlio, map[string]any{"name": "Figlio " + e.slug, "parent_id": g}, http.StatusForbidden)

	// Ruoli personalizzati: niente permessi che non si hanno, né in creazione né in modifica.
	errCode(t, d, "POST", "/api/v1/admin/roles", map[string]any{"name": "Revisore", "permissions": []string{"audit.read"}}, http.StatusForbidden)
	errCode(t, d, "PATCH", "/api/v1/admin/roles/"+delegato, map[string]any{"name": "Amministrazione delegata",
		"permissions": []string{"tenant.admin", "element.read", "finance.read"}}, http.StatusForbidden)

	// Non si toglie ciò che non si darebbe, e non si agisce su chi è più forte.
	adminID := e.admin.member
	var asg string
	for _, r := range e.memberDetail(adminID)["roles"].([]any) {
		if r.(map[string]any)["id"] == adminRole {
			asg = r.(map[string]any)["assignment_id"].(string)
		}
	}
	errCode(t, d, "DELETE", "/api/v1/admin/role-assignments/"+asg, nil, http.StatusForbidden)
	errCode(t, d, "POST", "/api/v1/admin/members/"+adminID+"/suspend", nil, http.StatusForbidden)
	errCode(t, d, "POST", "/api/v1/admin/members/"+adminID+"/password-link", nil, http.StatusForbidden)
	errCode(t, d, "POST", "/api/v1/admin/members/"+adminID+"/mfa-reset", nil, http.StatusForbidden)
	// Su chi ha meno permessi sì.
	d.do("POST", "/api/v1/admin/members/"+altro.member+"/suspend", nil, http.StatusNoContent)
	if m := e.memberDetail(altro.member); m["suspended_source"] != "admin" {
		t.Fatalf("sospensione dall'interfaccia: %v", m["suspended_source"])
	}
	d.do("POST", "/api/v1/admin/members/"+altro.member+"/reactivate", nil, http.StatusNoContent)

	// L'amministratore pieno resta libero.
	e.admin.do("POST", "/api/v1/admin/roles/"+editor+"/assignments", map[string]any{"member_id": altro.member}, http.StatusCreated)
}

// ---------------------------------------------------------------------
// OIDC: creazione al primo accesso e gruppi mappati
// ---------------------------------------------------------------------

type idpEnv struct {
	*adminEnv
	idp *fakeIdP
	app *ssoApp
}

func newIdPEnv(t *testing.T) *idpEnv {
	d := needDB(t)
	ctx := context.Background()
	adminSeq++
	slug := fmt.Sprintf("idp%d-%s", adminSeq, strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "IdP " + slug, AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t, "regia", "segreto-del-client")
	hs := httptest.NewUnstartedServer(nil)
	hs.Start()
	t.Cleanup(hs.Close)
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	svc := auth.NewService(d, time.Hour)
	box := secretbox.Random()
	svc.SetSecretBox(box)
	cfg := config.OIDC{Issuer: idp.URL, ClientID: idp.clientID, ClientSecret: idp.secret, RedirectURL: hs.URL + "/api/v1/auth/oidc/callback",
		Name: "IdP aziendale", Scopes: []string{"openid", "email", "profile"}}
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box,
		OIDC: auth.NewOIDC(cfg, nil),
	})
	hs.Config.Handler = srv.Handler()
	t.Cleanup(srv.Close)
	e := &adminEnv{t: t, base: hs.URL, slug: slug, ten: ten, auth: svc, eng: eng, box: box}
	e.admin = login(t, hs.URL, "admin@"+slug+".test", testPassword)
	return &idpEnv{adminEnv: e, idp: idp, app: &ssoApp{Server: hs, auth: svc}}
}

// oidc entra con l'IdP finto; restituisce la sessione e dove è finito il browser.
func (e *idpEnv) oidc(sub, email string, extra map[string]any) (*user, string) {
	e.t.Helper()
	claims := map[string]any{"sub": sub, "email": email, "email_verified": true, "name": "Persona " + sub}
	for k, v := range extra {
		claims[k] = v
	}
	e.idp.next(claims)
	c := browser()
	loc := ssoLogin(e.t, c, e.app, "/", nil)
	return &user{t: e.t, base: e.base, c: c}, loc
}

func (e *idpEnv) memberByEmail(email string) map[string]any {
	e.t.Helper()
	for _, it := range e.admin.do("GET", "/api/v1/admin/members?q="+email, nil, http.StatusOK)["items"].([]any) {
		m := it.(map[string]any)
		if m["email"] == email {
			return m
		}
	}
	return nil
}

func groupNames(m map[string]any) string {
	var out []string
	for _, g := range m["groups"].([]any) {
		out = append(out, g.(map[string]any)["name"].(string))
	}
	return strings.Join(out, ",")
}

func TestIdentitaAziendaleOIDC(t *testing.T) {
	e := newIdPEnv(t)
	domain := e.slug + ".test"
	editori := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Editori " + e.slug}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/roles/"+e.roleID("editor")+"/assignments", map[string]any{"group_id": editori}, http.StatusCreated)

	// Creazione spenta (default): chi non ha un account non entra.
	if _, loc := e.oidc("nuova-1", "nuova@"+domain, nil); loc != "/?sso_error=unknown_account" {
		t.Fatalf("creazione spenta: %s", loc)
	}
	e.admin.do("PUT", "/api/v1/admin/idp/settings", map[string]any{"provisioning": true, "allowed_domains": []string{"@" + strings.ToUpper(domain)},
		"groups_claim": "ruoli_regia"}, http.StatusNoContent)
	e.admin.do("PUT", "/api/v1/admin/idp/settings", map[string]any{"provisioning": true, "groups_claim": "nome con spazi"}, http.StatusUnprocessableEntity)
	ov := e.admin.do("GET", "/api/v1/admin/idp", nil, http.StatusOK)
	if s := ov["settings"].(map[string]any); fmt.Sprint(s["allowed_domains"]) != "["+domain+"]" || s["groups_claim"] != "ruoli_regia" || !strings.HasSuffix(ov["scim_base_url"].(string), "/scim/v2") {
		t.Fatalf("impostazioni: %v", ov)
	}

	// Primo accesso con dominio ammesso: account e membro nuovi, nessun ruolo.
	u, loc := e.oidc("nuova-1", "nuova@"+domain, map[string]any{"ruoli_regia": []string{"Regia-Editori", "Altro-Gruppo"}})
	if loc != "/" {
		t.Fatalf("primo accesso: %s", loc)
	}
	me := u.do("GET", "/api/v1/me", nil, http.StatusOK)
	if me["active_tenant"] == nil {
		t.Fatalf("il membro nuovo non ha l'organizzazione attiva: %v", me)
	}
	m := e.memberByEmail("nuova@" + domain)
	if m == nil || m["origin"] != "oidc" || len(m["roles"].([]any)) != 0 || m["display_name"] != "Persona nuova-1" {
		t.Fatalf("membro creato: %v", m)
	}

	// I gruppi visti nel claim compaiono da mappare; un gruppo non mappato non dà nulla.
	var regiaEditori string
	for _, it := range e.admin.do("GET", "/api/v1/admin/idp", nil, http.StatusOK)["groups"].([]any) {
		g := it.(map[string]any)
		if g["external_id"] == "Regia-Editori" && g["source"] == "oidc" {
			regiaEditori = g["id"].(string)
		}
	}
	if regiaEditori == "" {
		t.Fatal("il gruppo del claim non compare tra quelli visti dall'IdP")
	}
	if strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("gruppo non mappato ha dato un'appartenenza: %s", groupNames(m))
	}
	mp := e.admin.do("POST", "/api/v1/admin/idp/mappings", map[string]any{"idp_group_id": regiaEditori, "group_id": editori}, http.StatusCreated)["id"].(string)
	// La mappatura vale subito per chi è già nel gruppo dell'IdP.
	if m = e.memberByEmail("nuova@" + domain); !strings.Contains(groupNames(m), "Editori "+e.slug) {
		t.Fatalf("dopo la mappatura: %s", groupNames(m))
	}
	// Il gruppo è governato dall'IdP: a mano non si cambia.
	if c := errCode(t, e.admin, "DELETE", "/api/v1/admin/groups/"+editori+"/members/"+m["id"].(string), nil, http.StatusConflict); c != "group.idp_managed" {
		t.Fatalf("gruppo governato: %s", c)
	}
	altra := e.createMember("interna")
	errCode(t, e.admin, "POST", "/api/v1/admin/groups/"+editori+"/members", map[string]any{"member_id": altra.member}, http.StatusConflict)
	for _, it := range e.admin.do("GET", "/api/v1/admin/groups", nil, http.StatusOK)["items"].([]any) {
		if g := it.(map[string]any); g["id"] == editori && g["idp_managed"] != true {
			t.Fatalf("il gruppo mappato non è segnato come governato: %v", g)
		}
	}

	// Claim assente: nessuna modifica. Claim presente senza il gruppo: si esce.
	e.oidc("nuova-1", "nuova@"+domain, nil)
	if m = e.memberByEmail("nuova@" + domain); !strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("claim assente ha tolto i gruppi: %s", groupNames(m))
	}
	e.oidc("nuova-1", "nuova@"+domain, map[string]any{"_claim_names": map[string]any{"ruoli_regia": "src1"}})
	if m = e.memberByEmail("nuova@" + domain); !strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("overage ha tolto i gruppi: %s", groupNames(m))
	}
	e.oidc("nuova-1", "nuova@"+domain, map[string]any{"ruoli_regia": []string{}})
	if m = e.memberByEmail("nuova@" + domain); strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("claim senza il gruppo: %s", groupNames(m))
	}
	e.oidc("nuova-1", "nuova@"+domain, map[string]any{"ruoli_regia": "regia-editori"}) // stringa singola, maiuscole diverse
	if m = e.memberByEmail("nuova@" + domain); !strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("claim come stringa: %s", groupNames(m))
	}

	// Un altro dominio entra solo con un gruppo mappato; senza, no.
	if _, loc := e.oidc("fuori-1", "fornitore@altrove-"+domain, map[string]any{"ruoli_regia": []string{"Altro-Gruppo"}}); loc != "/?sso_error=unknown_account" {
		t.Fatalf("dominio non ammesso senza gruppi mappati: %s", loc)
	}
	if _, loc := e.oidc("fuori-2", "consulente@altrove-"+domain, map[string]any{"ruoli_regia": []string{"Regia-Editori"}}); loc != "/" {
		t.Fatalf("dominio non ammesso con gruppo mappato: %s", loc)
	}
	// Email non verificata: mai creazione.
	if _, loc := e.oidc("nv-1", "nonverificata@"+domain, map[string]any{"email_verified": false}); loc != "/?sso_error=email_unverified" {
		t.Fatalf("email non verificata: %s", loc)
	}

	// Sospeso: niente organizzazione attiva al login, e la creazione non lo riattiva.
	mid := e.memberByEmail("nuova@" + domain)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/members/"+mid+"/suspend", nil, http.StatusNoContent)
	u, _ = e.oidc("nuova-1", "nuova@"+domain, map[string]any{"ruoli_regia": []string{"Regia-Editori"}})
	if me := u.do("GET", "/api/v1/me", nil, http.StatusOK); me["active_tenant"] != nil {
		t.Fatalf("membro sospeso con organizzazione attiva: %v", me["active_tenant"])
	}
	e.admin.do("POST", "/api/v1/admin/members/"+mid+"/reactivate", nil, http.StatusNoContent)

	// Togliendo l'ultima mappatura il gruppo torna manuale.
	e.admin.do("DELETE", "/api/v1/admin/idp/mappings/"+mp, nil, http.StatusNoContent)
	e.admin.do("POST", "/api/v1/admin/groups/"+editori+"/members", map[string]any{"member_id": altra.member}, http.StatusNoContent)
}

// ---------------------------------------------------------------------
// SCIM 2.0
// ---------------------------------------------------------------------

type scimClient struct {
	t     *testing.T
	base  string
	token string
}

func (c *scimClient) do(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+"/scim/v2"+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("SCIM %s %s: stato %d, atteso %d: %s", method, path, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func patchOp(ops ...map[string]any) map[string]any {
	return map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, "Operations": ops}
}

func TestIdentitaAziendaleSCIM(t *testing.T) {
	e := newIdPEnv(t)
	domain := e.slug + ".test"
	tok := e.admin.do("POST", "/api/v1/admin/idp/scim-tokens", map[string]any{"name": "Entra ID"}, http.StatusCreated)
	c := &scimClient{t: t, base: e.base, token: tok["token"].(string)}
	if !strings.HasPrefix(c.token, "scim_") {
		t.Fatalf("token: %v", tok)
	}
	(&scimClient{t: t, base: e.base, token: "scim_sbagliato"}).do("GET", "/Users", nil, http.StatusUnauthorized)
	(&scimClient{t: t, base: e.base, token: ""}).do("GET", "/Users", nil, http.StatusUnauthorized)
	if cfg := c.do("GET", "/ServiceProviderConfig", nil, http.StatusOK); cfg["patch"].(map[string]any)["supported"] != true {
		t.Fatalf("configurazione: %v", cfg)
	}

	// Creazione: membro senza ruoli; il filtro lo ritrova (Entra cerca prima di creare).
	email := "mario.rossi@" + domain
	if l := c.do("GET", "/Users?filter="+url.QueryEscape(`userName eq "`+email+`"`), nil, http.StatusOK); l["totalResults"].(float64) != 0 {
		t.Fatalf("prima della creazione: %v", l)
	}
	user := c.do("POST", "/Users", map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email, "externalId": "obj-111", "active": true, "displayName": "Mario Rossi",
		"emails": []any{map[string]any{"value": email, "type": "work", "primary": true}}}, http.StatusCreated)
	uid := user["id"].(string)
	c.do("POST", "/Users", map[string]any{"userName": email}, http.StatusConflict)
	if l := c.do("GET", "/Users?filter="+url.QueryEscape(`userName eq "`+strings.ToUpper(email)+`"`), nil, http.StatusOK); l["totalResults"].(float64) != 1 {
		t.Fatalf("filtro userName: %v", l)
	}
	if l := c.do("GET", "/Users?filter="+url.QueryEscape(`externalId eq "obj-111"`), nil, http.StatusOK); l["totalResults"].(float64) != 1 {
		t.Fatalf("filtro externalId: %v", l)
	}
	c.do("GET", "/Users?filter="+url.QueryEscape(`userName co "mario"`), nil, http.StatusBadRequest)
	m := e.memberDetail(uid)
	if m["origin"] != "scim" || len(m["roles"].([]any)) != 0 || m["status"] != "active" {
		t.Fatalf("membro da SCIM: %v", m)
	}

	// Gruppi: un gruppo mappato in anticipo a mano si adotta alla creazione da SCIM.
	editori := e.admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Editori " + e.slug}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/roles/"+e.roleID("editor")+"/assignments", map[string]any{"group_id": editori}, http.StatusCreated)
	pre := e.admin.do("POST", "/api/v1/admin/idp/groups", map[string]any{"external_id": "grp-ed", "display_name": "Editori IdP"}, http.StatusCreated)["id"].(string)
	e.admin.do("POST", "/api/v1/admin/idp/mappings", map[string]any{"idp_group_id": pre, "group_id": editori}, http.StatusCreated)
	grp := c.do("POST", "/Groups", map[string]any{"displayName": "Editori Regia", "externalId": "grp-ed",
		"members": []any{map[string]any{"value": uid}}}, http.StatusCreated)
	if grp["id"] != pre {
		t.Fatalf("il gruppo scritto a mano non è stato adottato: %v", grp)
	}
	if m = e.memberDetail(uid); !strings.Contains(groupNames(m), "Editori "+e.slug) || !strings.Contains(fmt.Sprint(m["roles"]), "editor") {
		t.Fatalf("appartenenza da SCIM: %s %v", groupNames(m), m["roles"])
	}
	// Patch alla Entra ID: rimozione con filtro nel percorso.
	c.do("PATCH", "/Groups/"+pre, patchOp(map[string]any{"op": "Remove", "path": `members[value eq "` + uid + `"]`}), http.StatusOK)
	if m = e.memberDetail(uid); strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("rimozione dal gruppo: %s", groupNames(m))
	}
	c.do("PATCH", "/Groups/"+pre, patchOp(map[string]any{"op": "add", "path": "members", "value": []any{map[string]any{"value": uid}}}), http.StatusOK)
	if l := c.do("GET", "/Groups?excludedAttributes=members&filter=displayName+eq+%22Editori+Regia%22", nil, http.StatusOK); l["totalResults"].(float64) != 1 {
		t.Fatalf("filtro dei gruppi: %v", l)
	}

	// Con SCIM attivo il claim dei gruppi al login non conta.
	e.admin.do("PUT", "/api/v1/admin/idp/settings", map[string]any{"provisioning": false, "groups_claim": "groups"}, http.StatusNoContent)
	e.oidc("sub-mario", email, map[string]any{"groups": []string{}})
	if m = e.memberDetail(uid); !strings.Contains(groupNames(m), "Editori") {
		t.Fatalf("con SCIM il claim ha tolto i gruppi: %s", groupNames(m))
	}

	// Disattivazione alla Entra ID ("False" come stringa): sospeso dall'IdP, sessioni chiuse.
	u, _ := e.oidc("sub-mario", email, nil)
	u.do("GET", "/api/v1/me", nil, http.StatusOK)
	out := c.do("PATCH", "/Users/"+uid, patchOp(map[string]any{"op": "Replace", "path": "active", "value": "False"}), http.StatusOK)
	if out["active"] != false {
		t.Fatalf("dopo la disattivazione: %v", out)
	}
	if m = e.memberDetail(uid); m["status"] != "suspended" || m["suspended_source"] != "idp" {
		t.Fatalf("sospensione dall'IdP: %v %v", m["status"], m["suspended_source"])
	}
	u.do("GET", "/api/v1/me", nil, http.StatusUnauthorized)
	// Riattivazione alla Okta (valore oggetto senza path).
	c.do("PATCH", "/Users/"+uid, patchOp(map[string]any{"op": "replace", "value": map[string]any{"active": true}}), http.StatusOK)
	if m = e.memberDetail(uid); m["status"] != "active" || m["suspended_source"] != nil {
		t.Fatalf("riattivazione dall'IdP: %v", m)
	}
	// Chi sospende un amministratore resta l'unico a poterlo riattivare.
	e.admin.do("POST", "/api/v1/admin/members/"+uid+"/suspend", nil, http.StatusNoContent)
	c.do("PATCH", "/Users/"+uid, patchOp(map[string]any{"op": "replace", "path": "active", "value": true}), http.StatusOK)
	if m = e.memberDetail(uid); m["status"] != "suspended" || m["suspended_source"] != "admin" {
		t.Fatalf("SCIM non deve riattivare una sospensione dell'amministratore: %v %v", m["status"], m["suspended_source"])
	}
	e.admin.do("POST", "/api/v1/admin/members/"+uid+"/reactivate", nil, http.StatusNoContent)

	// DELETE sospende, non cancella.
	c.do("DELETE", "/Users/"+uid, nil, http.StatusNoContent)
	if m = e.memberDetail(uid); m["status"] != "suspended" {
		t.Fatalf("DELETE: %v", m["status"])
	}
	// L'ultimo amministratore non lo sospende nemmeno l'IdP.
	if l := c.do("GET", "/Users?filter="+url.QueryEscape(`userName eq "admin@`+domain+`"`), nil, http.StatusOK); l["totalResults"].(float64) != 1 {
		t.Fatalf("amministratore: %v", l)
	} else {
		aid := l["Resources"].([]any)[0].(map[string]any)["id"].(string)
		c.do("PATCH", "/Users/"+aid, patchOp(map[string]any{"op": "replace", "path": "active", "value": false}), http.StatusConflict)
	}

	// Token di un'altra organizzazione: non vede questi utenti.
	other := newIdPEnv(t)
	oc := &scimClient{t: t, base: other.base, token: other.admin.do("POST", "/api/v1/admin/idp/scim-tokens", map[string]any{"name": "x"}, http.StatusCreated)["token"].(string)}
	oc.do("GET", "/Users/"+uid, nil, http.StatusNotFound)
	if l := oc.do("GET", "/Users?filter="+url.QueryEscape(`userName eq "`+email+`"`), nil, http.StatusOK); l["totalResults"].(float64) != 0 {
		t.Fatalf("isolamento: %v", l)
	}
	// Revoca.
	for _, it := range e.admin.do("GET", "/api/v1/admin/idp", nil, http.StatusOK)["scim_tokens"].([]any) {
		e.admin.do("DELETE", "/api/v1/admin/idp/scim-tokens/"+it.(map[string]any)["id"].(string), nil, http.StatusNoContent)
	}
	c.do("GET", "/Users", nil, http.StatusUnauthorized)
}
