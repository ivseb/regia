package itest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
	"regia/migrations"
)

const testPassword = "password-di-test-lunga"

var seq int

// newTenant crea un tenant con un amministratore dedicato (email unica per test).
func newTenant(t *testing.T, d *db.DB) tenant.Created {
	t.Helper()
	seq++
	slug := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	slug = strings.ReplaceAll(slug, "_", "-")
	if len(slug) > 50 {
		slug = slug[:50]
	}
	slug = slug + "-" + string(rune('a'+seq%26)) + time.Now().Format("150405")
	out, err := tenant.Create(context.Background(), d, tenant.CreateInput{
		Slug: slug, Name: "Tenant " + slug, AdminEmail: slug + "@example.test", AdminPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigrationsAreIdempotent(t *testing.T) {
	d := needDB(t)
	n, err := db.Migrate(context.Background(), d.Owner, migrations.FS, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("la seconda esecuzione ha applicato %d migrazioni", n)
	}
}

// Ogni tabella con tenant_id deve avere la RLS attiva e forzata (esclusi i casi noti).
func TestEveryTenantTableHasRLS(t *testing.T) {
	d := needDB(t)
	rows, err := d.Owner.Query(context.Background(), `
		select c.relname
		from pg_class c
		join pg_attribute a on a.attrelid = c.oid and a.attname = 'tenant_id' and not a.attisdropped
		where c.relnamespace = 'public'::regnamespace and c.relkind in ('r', 'p')
		  and not (c.relrowsecurity and c.relforcerowsecurity)
		  and c.relname not in ('jobs', 'audit_log')
		  and c.relname not like '%\_default' escape '\'`)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("tabelle del tenant senza RLS forzata: %v", missing)
	}
}

func TestTenantIsolation(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a, b := newTenant(t, d), newTenant(t, d)

	// Dentro A si vede solo il membro di A.
	err := d.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		var other int
		if err := tx.QueryRow(ctx, "select count(*), count(*) filter (where tenant_id <> $1) from members", a.TenantID).Scan(&n, &other); err != nil {
			return err
		}
		if n != 1 || other != 0 {
			t.Errorf("in A visibili %d membri (%d di altri tenant)", n, other)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Da A non si può scrivere in B.
	err = d.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into groups (tenant_id, id, name) values ($1, $2, 'intruso')", b.TenantID, db.NewID())
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("scrittura tra tenant non bloccata dalla RLS: %v", err)
	}

	// Senza tenant non si vede nulla.
	err = d.Global(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from members").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("senza tenant visibili %d membri", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.InTenant(ctx, "", db.TenantOpts{}, func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrNoTenant) {
		t.Fatalf("transazione senza tenant ammessa: %v", err)
	}

	// Il ruolo applicativo non può leggere l'audit direttamente né creare tenant.
	err = d.Global(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select * from audit_log limit 1")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("lettura diretta dell'audit non bloccata: %v", err)
	}
	err = d.Global(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into tenants (slug, name) values ('abusivo', 'x')")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("creazione di tenant dal ruolo applicativo non bloccata: %v", err)
	}
}

func TestFinanceRowsNeedFinanceFlag(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a := newTenant(t, d)
	insert := func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into rates (tenant_id, role_key, hourly_cents, valid_from) values ($1, 'backend', 6500, '2026-01-01')", a.TenantID)
		return err
	}
	if err := d.InTenant(ctx, a.TenantID, db.TenantOpts{}, insert); err == nil {
		t.Fatal("tariffa scritta senza il flag finanza")
	}
	if err := d.InTenant(ctx, a.TenantID, db.TenantOpts{Finance: true}, insert); err != nil {
		t.Fatal(err)
	}
	count := func(o db.TenantOpts) int {
		var n int
		if err := d.InTenant(ctx, a.TenantID, o, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "select count(*) from rates").Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(db.TenantOpts{}); n != 0 {
		t.Fatalf("senza flag finanza visibili %d tariffe", n)
	}
	if n := count(db.TenantOpts{Finance: true}); n != 1 {
		t.Fatalf("con flag finanza visibili %d tariffe, attese 1", n)
	}
}

func TestAuditChainDetectsTampering(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a := newTenant(t, d)
	// Qualche evento in più nella catena.
	for i := 0; i < 3; i++ {
		err := d.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "select audit_append('test.event', 'test', null, $1, null, $2, null)", map[string]any{"i": i}, a.AdminMemberID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	verify := func() *int64 {
		var broken *int64
		if err := d.Owner.QueryRow(ctx, "select audit_verify($1)", a.TenantID).Scan(&broken); err != nil {
			t.Fatal(err)
		}
		return broken
	}
	if b := verify(); b != nil {
		t.Fatalf("catena integra segnalata rotta al seq %d", *b)
	}
	if _, err := d.Owner.Exec(ctx, "update audit_log set payload = '{\"i\": 99}' where tenant_id = $1 and seq = 3", a.TenantID); err != nil {
		t.Fatal(err)
	}
	if b := verify(); b == nil || *b != 3 {
		t.Fatalf("manomissione non rilevata (risultato %v)", b)
	}
}

func TestLoginAndPermissions(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	a := newTenant(t, d)
	var email string
	if err := d.Owner.QueryRow(ctx, "select email from accounts where id = $1", a.AdminAccountID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(d, time.Hour)
	ip := netip.MustParseAddr("192.0.2.10")

	if _, err := svc.Login(ctx, email, "password-sbagliata!!", ip, "test"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("password sbagliata: %v", err)
	}
	if _, err := svc.Login(ctx, "nessuno@example.test", "password-sbagliata!!", ip, "test"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("account inesistente: %v", err)
	}
	token, err := svc.Login(ctx, strings.ToUpper(email), testPassword, ip, "test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if p.TenantID != a.TenantID || p.MemberID != a.AdminMemberID || !p.Can("tenant.admin") {
		t.Fatalf("principal inatteso: %+v", p)
	}
	if _, err := svc.Authenticate(ctx, token+"x"); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("token alterato accettato: %v", err)
	}
	if err := svc.Logout(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("sessione valida dopo il logout: %v", err)
	}

	// Dopo 10 tentativi sbagliati dallo stesso IP l'account è bloccato, anche con la password giusta.
	for i := 0; i < 10; i++ {
		_, _ = svc.Login(ctx, email, "password-sbagliata!!", ip, "test")
	}
	if _, err := svc.Login(ctx, email, testPassword, ip, "test"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("limite dei tentativi non applicato: %v", err)
	}
}

func TestHTTPFlow(t *testing.T) {
	d := needDB(t)
	a := newTenant(t, d)
	var email string
	if err := d.Owner.QueryRow(context.Background(), "select email from accounts where id = $1", a.AdminAccountID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	post := func(path, body string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	get := func(path string) int {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if s := get("/api/v1/me"); s != http.StatusUnauthorized {
		t.Fatalf("/me senza sessione: %d", s)
	}
	if r := post("/api/v1/auth/login", `{"email":"`+email+`","password":"`+testPassword+`"}`, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", r.StatusCode)
	}
	if s := get("/api/v1/me"); s != http.StatusOK {
		t.Fatalf("/me con sessione: %d", s)
	}
	if s := get("/api/v1/audit"); s != http.StatusOK {
		t.Fatalf("/audit da amministratore: %d", s)
	}
	if r := post("/api/v1/me/tenant", `{"tenant_id":"`+a.TenantID+`"}`, map[string]string{"Origin": "https://altro.example"}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("richiesta da altra origine: %d", r.StatusCode)
	}
	if r := post("/api/v1/auth/logout", ``, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", r.StatusCode)
	}
	if s := get("/api/v1/me"); s != http.StatusUnauthorized {
		t.Fatalf("/me dopo il logout: %d", s)
	}
}

// Al login si rientra nell'ultimo tenant usato, non nel primo in ordine di nome.
func TestLoginRemembersLastTenant(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := time.Now().Format("150405.000000")
	stamp = strings.ReplaceAll(stamp, ".", "")
	email := "consulente-" + stamp + "@example.test"
	alfa, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: "alfa-" + stamp, Name: "Alfa " + stamp, AdminEmail: email, AdminPassword: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	zeta, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: "zeta-" + stamp, Name: "Zeta " + stamp, AdminEmail: email, AdminPassword: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(d, time.Hour)
	active := func() *auth.Principal {
		tok, err := svc.Login(ctx, email, testPassword, netip.MustParseAddr("192.0.2.10"), "test")
		if err != nil {
			t.Fatal(err)
		}
		p, err := svc.Authenticate(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := active()
	if p.TenantID != alfa.TenantID {
		t.Fatalf("primo accesso: atteso Alfa, trovato %s", p.TenantID)
	}
	if err := svc.SwitchTenant(ctx, p, zeta.TenantID); err != nil {
		t.Fatal(err)
	}
	if p := active(); p.TenantID != zeta.TenantID {
		t.Fatalf("dopo il cambio: atteso Zeta, trovato %s", p.TenantID)
	}
}
