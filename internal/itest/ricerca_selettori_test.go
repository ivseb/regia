package itest

import (
	"context"
	"fmt"
	"log/slog"
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
	"regia/internal/platform/tenant"
)

// Selettore comune dell'interfaccia: gli elenchi di entità accettano q (ricerca nel nome), limit
// (massimo 200) e ids (restringe a certe voci), così il selettore funziona uguale con 60 o 60.000 voci;
// senza parametri l'elenco resta completo e un altro tenant non compare mai.
func TestRicercaSelettori(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()

	setup := func(prefix string) (slug, id string) {
		slug = strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000"), ".", "")
		ten, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: slug, Name: "Ricerca " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
		})
		if err != nil {
			t.Fatal(err)
		}
		return slug, ten.TenantID
	}
	slugA, tenA := setup("sa")
	slugB, tenB := setup("sb")
	admin := login(t, srv.URL, "admin@"+slugA+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)

	// 150 persone (alcune con accento e un richiedente), 30 gruppi, 40 unità e 40 team in A; una persona omonima in B.
	seed := func(tid, tag string, n int) {
		if _, err := d.Owner.Exec(ctx, `insert into members (tenant_id, display_name, member_type) select $1, $2 || ' ' || lpad(g::text, 3, '0'), 'standard' from generate_series(1, $3::int) g`, tid, tag, n); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			`insert into groups (tenant_id, name) select $1, 'Gruppo ' || $2 || ' ' || lpad(g::text, 2, '0') from generate_series(1, 30) g`,
			`insert into org_units (tenant_id, name, code) select $1, 'Unità ' || $2 || ' ' || lpad(g::text, 2, '0'), 'U' || g from generate_series(1, 40) g`,
			`insert into teams (tenant_id, name) select $1, 'Team ' || $2 || ' ' || lpad(g::text, 2, '0') from generate_series(1, 40) g`,
		} {
			if _, err := d.Owner.Exec(ctx, q, tid, tag); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(tenA, "Alfa", 150)
	seed(tenB, "Alfa", 5)
	if _, err := d.Owner.Exec(ctx, `insert into members (tenant_id, display_name, member_type) values ($1, 'Àlvaro Núñez', 'requester'), ($1, 'Giulia Zanetti', 'standard')`, tenA); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": fmt.Sprintf("Gestionale %02d", i), "code": fmt.Sprintf("GST%02d", i)}, 201)
	}

	items := func(u *user, path string) []map[string]any {
		t.Helper()
		out := []map[string]any{}
		for _, x := range u.do("GET", path, nil, 200)["items"].([]any) {
			out = append(out, x.(map[string]any))
		}
		return out
	}
	nameKey := map[string]string{"/api/v1/members": "display_name"}
	key := func(path string) string {
		if k, ok := nameKey[path]; ok {
			return k
		}
		return "name"
	}

	for _, path := range []string{"/api/v1/members", "/api/v1/groups", "/api/v1/teams", "/api/v1/org-units", "/api/v1/architecture/applications"} {
		path := path
		t.Run(path, func(t *testing.T) {
			all := items(admin, path)
			if len(all) < 25 {
				t.Fatalf("senza parametri l'elenco è completo: %d voci", len(all))
			}
			// limit: al massimo n voci, le prime per nome.
			lim := items(admin, path+"?limit=20")
			if len(lim) != 20 {
				t.Fatalf("limit=20: %d voci", len(lim))
			}
			for i := range lim {
				if lim[i]["id"] != all[i]["id"] {
					t.Fatalf("limit tiene l'ordine dell'elenco completo (voce %d)", i)
				}
			}
			// limit oltre il massimo: tagliato a 200.
			if n := len(items(admin, path+"?limit=100000")); n > 200 {
				t.Fatalf("limit massimo 200, avute %d voci", n)
			}
			// ids: solo quelle voci, anche con q che non le include non servono; q vuoto + ids.
			a, b := all[3]["id"].(string), all[17]["id"].(string)
			got := items(admin, path+"?ids="+url.QueryEscape(a+","+b))
			if len(got) != 2 {
				t.Fatalf("ids: %d voci invece di 2", len(got))
			}
			if n := len(items(admin, path+"?ids=00000000-0000-0000-0000-000000000000")); n != 0 {
				t.Fatalf("id sconosciuto: %d voci", n)
			}
			// q: contiene il testo (anche in mezzo al nome), senza distinguere le maiuscole.
			first := all[0][key(path)].(string)
			part := strings.ToLower(first[len(first)-3:])
			found := items(admin, path+"?q="+url.QueryEscape(part))
			if len(found) == 0 {
				t.Fatalf("q=%q non trova %q", part, first)
			}
			for _, f := range found {
				if !strings.Contains(strings.ToLower(f[key(path)].(string)), part) {
					t.Fatalf("q=%q: %q non contiene il testo", part, f[key(path)])
				}
			}
			// q con caratteri speciali di ilike: letterali, non jolly.
			if n := len(items(admin, path+"?q="+url.QueryEscape("%"))); path != "/api/v1/architecture/applications" && n != 0 {
				t.Fatalf("q=%%: %d voci, il segno non è un jolly", n)
			}
			// L'altro tenant vede solo le sue voci.
			for _, f := range items(adminB, path+"?q=alfa&limit=200") {
				if f["id"] == all[0]["id"] {
					t.Fatalf("voce di un altro tenant")
				}
			}
		})
	}

	// Persone: ricerca senza accenti dall'utente non coperta dal server (la fa il client); q per nome parziale e limit 1.
	if n := len(items(admin, "/api/v1/members?q=zanetti&limit=1")); n != 1 {
		t.Fatalf("q=zanetti: %d voci", n)
	}
	if n := len(items(admin, "/api/v1/members?q="+url.QueryEscape("alfa 014"))); n != 1 {
		t.Fatalf("q con spazio: %d voci", n)
	}
	// Unità: la ricerca vale anche per il codice.
	if n := len(items(admin, "/api/v1/org-units?q=U17")); n < 1 {
		t.Fatalf("q sul codice dell'unità: %d voci", n)
	}
	// Applicazioni: q per codice, limit e ids insieme.
	apps := items(admin, "/api/v1/architecture/applications?q=GST1&limit=5")
	if len(apps) != 5 {
		t.Fatalf("applicazioni q=GST1 limit=5: %d voci", len(apps))
	}
	one := items(admin, "/api/v1/architecture/applications?ids="+apps[2]["id"].(string)+"&limit=50")
	if len(one) != 1 || one[0]["id"] != apps[2]["id"] {
		t.Fatalf("applicazioni per id: %v", one)
	}
}
