package itest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/config"
	"regia/internal/platform/tenant"
)

// ---------------------------------------------------------------------
// Provider OIDC finto: discovery, JWKS, authorize e token, con ID token firmati
// (ES256, come Kanidm) da una chiave generata nel test.
// ---------------------------------------------------------------------

type idpCode struct {
	nonce, challenge, redirect string
	claims                     map[string]any
}

type fakeIdP struct {
	*httptest.Server
	t        *testing.T
	key      *ecdsa.PrivateKey
	clientID string
	secret   string // vuoto = client pubblico

	mu       sync.Mutex
	codes    map[string]idpCode
	claims   map[string]any    // claim del prossimo accesso; nil come valore toglie il claim
	signWith *ecdsa.PrivateKey // se impostata, firma con una chiave che non è nella JWKS
}

func newFakeIdP(t *testing.T, clientID, secret string) *fakeIdP {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, clientID: clientID, secret: secret, codes: map[string]idpCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, map[string]any{
			"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
			"jwks_uri": f.URL + "/keys", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"ES256"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("response_type") != "code" || q.Get("client_id") != f.clientID || q.Get("code_challenge_method") != "S256" ||
			q.Get("code_challenge") == "" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
			http.Error(w, "richiesta di autorizzazione non valida: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		code := randomID()
		f.mu.Lock()
		f.codes[code] = idpCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: f.claims}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, sec, basic := r.BasicAuth()
		if !basic {
			id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != f.clientID || sec != f.secret {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		c, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || c.redirect != r.PostForm.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims := map[string]any{"iss": f.URL, "aud": f.clientID, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "nonce": c.nonce}
		for k, v := range c.claims {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		key := f.key
		if f.signWith != nil {
			key = f.signWith
		}
		writeJSONT(w, map[string]any{"access_token": randomID(), "token_type": "Bearer", "expires_in": 300, "id_token": signJWT(t, key, claims)})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// next imposta i claim del prossimo accesso.
func (f *fakeIdP) next(claims map[string]any) {
	f.mu.Lock()
	f.claims, f.signWith = claims, nil
	f.mu.Unlock()
}

func signJWT(t *testing.T, key *ecdsa.PrivateKey, claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(claims)
	obj, err := signer.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSONT(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------------
// Applicazione con OIDC
// ---------------------------------------------------------------------

type ssoApp struct {
	*httptest.Server
	auth *auth.Service
}

func newSSOApp(t *testing.T, idp *fakeIdP, only bool) *ssoApp {
	d := needDB(t)
	app := httptest.NewUnstartedServer(nil)
	app.Start()
	t.Cleanup(app.Close)
	svc := auth.NewService(d, time.Hour)
	svc.DisablePasswordLogin(only)
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	cfg := config.OIDC{Issuer: idp.URL, ClientID: idp.clientID, ClientSecret: idp.secret, RedirectURL: app.URL + "/api/v1/auth/oidc/callback",
		Name: "Kanidm", Scopes: []string{"openid", "email", "profile"}, Only: only}
	srv := api.New(api.Options{
		DB: d, Auth: svc, Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
		OIDC: auth.NewOIDC(cfg, nil),
	})
	app.Config.Handler = srv.Handler()
	t.Cleanup(srv.Close)
	return &ssoApp{Server: app, auth: svc}
}

// browser segue i redirect a mano, per vedere ogni passaggio.
func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func getLoc(t *testing.T, c *http.Client, u string, want int) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: stato %d, atteso %d", u, resp.StatusCode, want)
	}
	return resp.Header.Get("Location")
}

// ssoLogin percorre il flusso: avvio, IdP, callback. tweak può alterare la callback
// (per simulare uno state falso). Restituisce dove l'app manda il browser alla fine.
func ssoLogin(t *testing.T, c *http.Client, app *ssoApp, next string, tweak func(*url.URL)) string {
	t.Helper()
	authz := getLoc(t, c, app.URL+"/api/v1/auth/oidc/start?next="+url.QueryEscape(next), http.StatusFound)
	cb := getLoc(t, c, authz, http.StatusFound)
	u, err := url.Parse(cb)
	if err != nil {
		t.Fatal(err)
	}
	if tweak != nil {
		tweak(u)
	}
	return getLoc(t, c, u.String(), http.StatusSeeOther)
}

func status(t *testing.T, c *http.Client, u string) int {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func adminEmail(t *testing.T, accountID string) string {
	var email string
	if err := needDB(t).Owner.QueryRow(context.Background(), "select email from accounts where id = $1", accountID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

func TestOIDCLogin(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a := newTenant(t, d)
	email := adminEmail(t, a.AdminAccountID)
	idp := newFakeIdP(t, "regia", "segreto-del-client")
	app := newSSOApp(t, idp, false)

	var cfg map[string]any
	resp, err := http.Get(app.URL + "/api/v1/auth/oidc/config")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	resp.Body.Close()
	if cfg["enabled"] != true || cfg["name"] != "Kanidm" || cfg["password_login"] != true {
		t.Fatalf("configurazione OIDC: %v", cfg)
	}

	sub := "sub-" + randomID()
	t.Run("flusso completo con account esistente", func(t *testing.T) {
		idp.next(map[string]any{"sub": sub, "email": strings.ToUpper(email), "email_verified": true})
		c := browser()
		if loc := ssoLogin(t, c, app, "/richieste?q=aperte", nil); loc != "/richieste?q=aperte" {
			t.Fatalf("redirect finale: %q", loc)
		}
		if s := status(t, c, app.URL+"/api/v1/me"); s != http.StatusOK {
			t.Fatalf("/me dopo SSO: %d", s)
		}
		var n int
		if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id is null and action = 'auth.login'
			and entity_id = $1 and payload->>'method' = 'oidc'`, a.AdminAccountID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("audit auth.login con metodo oidc: %d (%v)", n, err)
		}
		var method string
		if err := d.Owner.QueryRow(ctx, "select auth_method from sessions where account_id = $1 order by created_at desc limit 1", a.AdminAccountID).Scan(&method); err != nil || method != "oidc" {
			t.Fatalf("metodo della sessione: %q (%v)", method, err)
		}
	})

	t.Run("dai login successivi vale il sub, anche con email cambiata", func(t *testing.T) {
		idp.next(map[string]any{"sub": sub, "email": "nuovo-indirizzo@altrove.test", "email_verified": false})
		c := browser()
		if loc := ssoLogin(t, c, app, "/", nil); loc != "/" {
			t.Fatalf("login per sub: %q", loc)
		}
		var me map[string]any
		resp, _ := c.Get(app.URL + "/api/v1/me")
		_ = json.NewDecoder(resp.Body).Decode(&me)
		resp.Body.Close()
		if me["account_id"] != a.AdminAccountID {
			t.Fatalf("account collegato al sub: %v", me["account_id"])
		}
		// Un'altra identità con la stessa email non si collega allo stesso account.
		idp.next(map[string]any{"sub": "intruso-" + randomID(), "email": email, "email_verified": true})
		if loc := ssoLogin(t, browser(), app, "/", nil); loc != "/?sso_error=already_linked" {
			t.Fatalf("secondo sub sulla stessa email: %q", loc)
		}
	})

	t.Run("account sconosciuto rifiutato, senza crearlo", func(t *testing.T) {
		ghost := "sconosciuto-" + strings.ToLower(randomID()) + "@example.test"
		idp.next(map[string]any{"sub": "s-" + randomID(), "email": ghost, "email_verified": true})
		c := browser()
		if loc := ssoLogin(t, c, app, "/", nil); loc != "/?sso_error=unknown_account" {
			t.Fatalf("account sconosciuto: %q", loc)
		}
		if s := status(t, c, app.URL+"/api/v1/me"); s != http.StatusUnauthorized {
			t.Fatalf("sessione creata per un account sconosciuto: %d", s)
		}
		var n int
		_ = d.Owner.QueryRow(ctx, "select count(*) from accounts where lower(email) = $1", ghost).Scan(&n)
		if n != 0 {
			t.Fatal("account creato automaticamente")
		}
	})

	t.Run("email non verificata o senza email_verified", func(t *testing.T) {
		other := newTenant(t, d)
		otherEmail := adminEmail(t, other.AdminAccountID)
		for name, claims := range map[string]map[string]any{
			"falsa":   {"sub": "s-" + randomID(), "email": otherEmail, "email_verified": false},
			"assente": {"sub": "s-" + randomID(), "email": otherEmail},
		} {
			idp.next(claims)
			if loc := ssoLogin(t, browser(), app, "/", nil); loc != "/?sso_error=email_unverified" {
				t.Fatalf("email_verified %s: %q", name, loc)
			}
		}
	})

	t.Run("token non validi rifiutati", func(t *testing.T) {
		base := func() map[string]any { return map[string]any{"sub": sub, "email": email, "email_verified": true} }
		cases := map[string]map[string]any{
			"nonce errato":   {"nonce": "un-altro-nonce"},
			"nonce assente":  {"nonce": nil},
			"aud errata":     {"aud": "un-altro-client"},
			"issuer errato":  {"iss": "https://idp.falso.test"},
			"token scaduto":  {"exp": time.Now().Add(-time.Hour).Unix(), "iat": time.Now().Add(-2 * time.Hour).Unix()},
			"firma estranea": nil,
		}
		for name, over := range cases {
			claims := base()
			for k, v := range over {
				claims[k] = v
			}
			idp.next(claims)
			if name == "firma estranea" {
				k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				idp.mu.Lock()
				idp.signWith = k
				idp.mu.Unlock()
			}
			c := browser()
			if loc := ssoLogin(t, c, app, "/", nil); loc != "/?sso_error=invalid" {
				t.Fatalf("%s: %q", name, loc)
			}
			if s := status(t, c, app.URL+"/api/v1/me"); s != http.StatusUnauthorized {
				t.Fatalf("%s: sessione creata (%d)", name, s)
			}
		}
	})

	t.Run("state errato o senza cookie", func(t *testing.T) {
		idp.next(map[string]any{"sub": sub, "email": email, "email_verified": true})
		loc := ssoLogin(t, browser(), app, "/", func(u *url.URL) {
			q := u.Query()
			q.Set("state", "state-inventato")
			u.RawQuery = q.Encode()
		})
		if loc != "/?sso_error=state" {
			t.Fatalf("state errato: %q", loc)
		}
		// Callback aperta da un altro browser (senza il cookie del flusso): rifiutata.
		c := browser()
		authz := getLoc(t, c, app.URL+"/api/v1/auth/oidc/start", http.StatusFound)
		cb := getLoc(t, c, authz, http.StatusFound)
		other := browser()
		if loc := getLoc(t, other, cb, http.StatusSeeOther); loc != "/?sso_error=state" {
			t.Fatalf("callback senza cookie: %q", loc)
		}
		if s := status(t, other, app.URL+"/api/v1/me"); s != http.StatusUnauthorized {
			t.Fatalf("sessione creata senza cookie del flusso: %d", s)
		}
	})

	t.Run("redirect solo verso percorsi interni", func(t *testing.T) {
		for _, next := range []string{"https://evil.example/x", "//evil.example", "/\\evil.example", "/api/v1/me", "javascript:alert(1)"} {
			idp.next(map[string]any{"sub": sub, "email": email, "email_verified": true})
			if loc := ssoLogin(t, browser(), app, next, nil); loc != "/" {
				t.Fatalf("next %q: redirect a %q", next, loc)
			}
		}
	})
}

func TestOIDCOnly(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a := newTenant(t, d)
	b := newTenant(t, d)
	email, rootEmail := adminEmail(t, a.AdminAccountID), adminEmail(t, b.AdminAccountID)
	if _, err := d.Owner.Exec(ctx, "update accounts set is_platform_admin = true where id = $1", b.AdminAccountID); err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t, "regia-pubblico", "") // client pubblico: solo PKCE
	app := newSSOApp(t, idp, true)

	var cfg map[string]any
	resp, _ := http.Get(app.URL + "/api/v1/auth/oidc/config")
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	resp.Body.Close()
	if cfg["password_login"] != false {
		t.Fatalf("solo SSO non segnalato: %v", cfg)
	}
	u := &user{t: t, base: app.URL, c: browser()}
	got := u.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": testPassword}, http.StatusForbidden)
	if got["code"] != "auth.password_disabled" {
		t.Fatalf("login con password in modalità solo SSO: %v", got)
	}
	// L'amministratore di piattaforma entra con la password (emergenza).
	login(t, app.URL, rootEmail, testPassword)

	idp.next(map[string]any{"sub": "s-" + randomID(), "email": email, "email_verified": true})
	c := browser()
	if loc := ssoLogin(t, c, app, "/", nil); loc != "/" {
		t.Fatalf("SSO in modalità solo SSO: %q", loc)
	}
	if s := status(t, c, app.URL+"/api/v1/me"); s != http.StatusOK {
		t.Fatalf("/me: %d", s)
	}
}

// ---------------------------------------------------------------------
// Token API personali
// ---------------------------------------------------------------------

// bearerDo fa una richiesta con un token API, senza cookie.
func bearerDo(t *testing.T, base, token, method, path string, body any, want int) map[string]any {
	t.Helper()
	u := &user{t: t, base: base, c: &http.Client{Transport: bearerTransport(token)}}
	return u.do(method, path, body, want)
}

type bearerTransport string

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func TestAPITokens(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	slugA, slugB := "tok-a-"+stamp, "tok-b-"+stamp
	email := "consulente-" + stamp + "@example.test"
	var ids [2]tenant.Created
	for i, slug := range []string{slugA, slugB} {
		c, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Token " + slug, AdminEmail: email, AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision}})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = c
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slugA, Email: "lettore-" + stamp + "@example.test",
		Name: "Lettore", Password: testPassword, Roles: []string{"viewer"}}); err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t, "regia", "s")
	app := newSSOApp(t, idp, false)
	base := app.URL

	admin := login(t, base, email, testPassword) // tenant attivo: A (primo per nome)
	me := admin.do("GET", "/api/v1/me", nil, http.StatusOK)
	if me["active_tenant"].(map[string]any)["tenant_id"] != ids[0].TenantID {
		t.Fatal("tenant attivo inatteso")
	}

	// Creazione: il segreto si vede una volta sola; nel database c'è solo lo sha256.
	created := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI"}, http.StatusCreated)
	full := created["token"].(string)
	item := created["item"].(map[string]any)
	if !strings.HasPrefix(full, "myt_") || len(full) < 40 {
		t.Fatalf("token: %q", full)
	}
	exp, _ := time.Parse(time.RFC3339, item["expires_at"].(string))
	if d := time.Until(exp); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("scadenza di default: %v", d)
	}
	var plain int
	if err := d.Owner.QueryRow(ctx, "select count(*) from api_tokens t where strpos(t::text, $1) > 0", full).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("token in chiaro nel database: %d (%v)", plain, err)
	}
	var storedHash []byte
	if err := d.Owner.QueryRow(ctx, "select token_hash from api_tokens where id = $1", item["id"]).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if h := sha256.Sum256([]byte(full)); string(storedHash) != string(h[:]) {
		t.Fatal("nel database non c'è lo sha256 del token")
	}
	list := admin.do("GET", "/api/v1/me/tokens", nil, http.StatusOK)
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), full) {
		t.Fatal("l'elenco contiene il segreto")
	}

	// Validazione: scadenza oltre un anno, permessi non posseduti.
	admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "expires_in_days": 400}, http.StatusUnprocessableEntity)
	reader := login(t, base, "lettore-"+stamp+"@example.test", testPassword)
	reader.do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{"element.write"}}, http.StatusUnprocessableEntity)

	// Uso: permessi del membro, nel tenant del token, con l'audit che registra il token.
	bme := bearerDo(t, base, full, "GET", "/api/v1/me", nil, http.StatusOK)
	if bme["active_tenant"].(map[string]any)["tenant_id"] != ids[0].TenantID || len(bme["tenants"].([]any)) != 1 {
		t.Fatalf("/me con token: %v", bme)
	}
	team := bearerDo(t, base, full, "POST", "/api/v1/teams", map[string]any{"name": "Squadra token"}, http.StatusCreated)
	bearerDo(t, base, full, "GET", "/api/v1/audit", nil, http.StatusOK)
	var tokAudit string
	if err := d.Owner.QueryRow(ctx, `select coalesce(payload->>'api_token', '') from audit_log
		where tenant_id = $1 and action = 'team.create' and entity_id = $2`, ids[0].TenantID, team["id"]).Scan(&tokAudit); err != nil {
		t.Fatal(err)
	}
	if tokAudit != item["id"] {
		t.Fatalf("audit senza il token usato: %q", tokAudit)
	}
	// Niente CSRF con il token: un Origin estraneo non conta, perché il cookie non si usa.
	req, _ := http.NewRequest("POST", base+"/api/v1/teams", strings.NewReader(`{"name":"Da altra origine"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://altro.example")
	req.Header.Set("Authorization", "Bearer "+full)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("token con Origin estraneo: %v %v", resp.StatusCode, err)
	}
	// Con un token non si gestiscono token né si cambia tenant.
	bearerDo(t, base, full, "POST", "/api/v1/me/tokens", map[string]any{"name": "figlio"}, http.StatusForbidden)
	bearerDo(t, base, full, "POST", "/api/v1/me/tenant", map[string]any{"tenant_id": ids[1].TenantID}, http.StatusForbidden)
	bearerDo(t, base, "myt_inventato", "GET", "/api/v1/me", nil, http.StatusUnauthorized)

	// Ultimo uso aggiornato.
	list = admin.do("GET", "/api/v1/me/tokens", nil, http.StatusOK)
	if list["items"].([]any)[0].(map[string]any)["last_used_at"] == nil {
		t.Fatal("ultimo uso non registrato")
	}

	// Permessi ridotti: lettura e scrittura degli elementi, niente audit né architettura.
	rw := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "ridotto", "expires_in_days": 7,
		"scopes": []string{"element.read", "element.write"}}, http.StatusCreated)["token"].(string)
	bearerDo(t, base, rw, "GET", "/api/v1/audit", nil, http.StatusForbidden)
	bearerDo(t, base, rw, "POST", "/api/v1/teams", map[string]any{"name": "Vietata"}, http.StatusForbidden)
	perms := bearerDo(t, base, rw, "GET", "/api/v1/me", nil, http.StatusOK)["active_tenant"].(map[string]any)["permissions"].([]any)
	if len(perms) != 2 {
		t.Fatalf("permessi effettivi del token ridotto: %v", perms)
	}
	// Sola lettura: nessuna richiesta che modifica.
	ro := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	bearerDo(t, base, ro, "GET", "/api/v1/teams", nil, http.StatusOK)
	if got := bearerDo(t, base, ro, "POST", "/api/v1/teams", map[string]any{"name": "No"}, http.StatusForbidden); got["code"] != "auth.token_read_only" {
		t.Fatalf("token di sola lettura: %v", got)
	}
	// I permessi del token non superano quelli attuali del membro: se il membro li perde, li perde il token.
	readerTok := reader.do("POST", "/api/v1/me/tokens", map[string]any{"name": "lettore"}, http.StatusCreated)["token"].(string)
	bearerDo(t, base, readerTok, "POST", "/api/v1/teams", map[string]any{"name": "No"}, http.StatusForbidden)

	// Isolamento tra tenant: il token di A non vede i dati di B, anche se l'account è membro di entrambi.
	admin.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": ids[1].TenantID}, http.StatusOK)
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Solo in B"}, http.StatusCreated)
	teams := bearerDo(t, base, full, "GET", "/api/v1/teams", nil, http.StatusOK)
	raw, _ := json.Marshal(teams)
	if strings.Contains(string(raw), "Solo in B") || !strings.Contains(string(raw), "Squadra token") {
		t.Fatalf("il token di A vede i team sbagliati: %s", raw)
	}
	// Elenco e revoca riguardano il tenant attivo: da B il token di A non si vede né si revoca.
	if n := len(admin.do("GET", "/api/v1/me/tokens", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("in B visibili %d token di A", n)
	}
	admin.do("DELETE", "/api/v1/me/tokens/"+item["id"].(string), nil, http.StatusNotFound)
	admin.do("POST", "/api/v1/me/tenant", map[string]any{"tenant_id": ids[0].TenantID}, http.StatusOK)

	// Scadenza.
	if _, err := d.Owner.Exec(ctx, "update api_tokens set expires_at = now() - interval '1 second' where token_hash = $1",
		func() []byte { h := sha256.Sum256([]byte(rw)); return h[:] }()); err != nil {
		t.Fatal(err)
	}
	bearerDo(t, base, rw, "GET", "/api/v1/me", nil, http.StatusUnauthorized)

	// Revoca.
	admin.do("DELETE", "/api/v1/me/tokens/"+item["id"].(string), nil, http.StatusNoContent)
	bearerDo(t, base, full, "GET", "/api/v1/me", nil, http.StatusUnauthorized)
	var revoked int
	_ = d.Owner.QueryRow(ctx, "select count(*) from audit_log where tenant_id = $1 and action = 'api_token.revoke' and entity_id = $2",
		ids[0].TenantID, item["id"]).Scan(&revoked)
	if revoked != 1 {
		t.Fatal("revoca non registrata nell'audit")
	}
}
