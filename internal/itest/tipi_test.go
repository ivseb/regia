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

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Tipi di elemento configurabili: rinomina con plurale e nomi per lingua, tipo nuovo da un
// modello (fasi, campi, spazi), archiviazione, divieti sui tipi di sistema, permessi,
// isolamento tra tenant e audit.
func TestTipiConfigurabili(t *testing.T) {
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
			Slug: slug, Name: "Tipi " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
		})
		if err != nil {
			t.Fatal(err)
		}
		for name, in := range map[string]tenant.MemberInput{
			"campi":   {Roles: []string{"fields_admin"}},
			"lettore": {Roles: []string{"viewer"}},
		} {
			in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", "Persona "+name, testPassword
			if _, err := tenant.AddMember(ctx, d, in); err != nil {
				t.Fatal(err)
			}
		}
		return slug, ten.TenantID
	}
	slugA, tenA := setup("ta")
	slugB, _ := setup("tb")
	admin := login(t, srv.URL, "admin@"+slugA+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)
	fa := login(t, srv.URL, "campi@"+slugA+".test", testPassword)
	reader := login(t, srv.URL, "lettore@"+slugA+".test", testPassword)

	// Permessi: solo l'amministratore del tenant configura; chiunque legge i nomi.
	reader.do("GET", "/api/v1/types", nil, http.StatusForbidden)
	fa.do("GET", "/api/v1/types", nil, http.StatusForbidden)
	fa.do("PATCH", "/api/v1/types/business_request", map[string]any{"name": "Abusivo"}, http.StatusForbidden)
	reader.do("POST", "/api/v1/types", map[string]any{"key": "abusivo", "name": "Abusivo"}, http.StatusForbidden)
	if n := reader.do("GET", "/api/v1/type-names", nil, http.StatusOK)["items"].([]any); len(n) == 0 {
		t.Fatal("i nomi dei tipi li legge ogni membro")
	}

	// Elenco con contatori.
	find := func(u *user, key string) map[string]any {
		t.Helper()
		for _, x := range u.do("GET", "/api/v1/types", nil, http.StatusOK)["items"].([]any) {
			if m := x.(map[string]any); m["key"] == key {
				return m
			}
		}
		t.Fatalf("tipo %s non in elenco", key)
		return nil
	}
	br := find(admin, "business_request")
	if br["is_system"] != true || br["portal_enabled"] != true || br["fields"].(float64) < 1 || br["process_key"] == "" {
		t.Fatalf("scheda del tipo di sistema: %v", br)
	}

	// Rinomina: solo i testi cambiano; plurale, descrizione, icona, colore e nomi per lingua.
	got := admin.do("PATCH", "/api/v1/types/business_request", map[string]any{
		"name": "Richiesta di business", "plural_name": "Richieste di business", "description": "Una domanda che entra nel portafoglio",
		"icon": "inbox", "color": "accent",
		"labels": map[string]any{"en": map[string]any{"name": "Business request", "plural": "Business requests"}},
	}, http.StatusOK)
	if got["name"] != "Richiesta di business" || got["plural_name"] != "Richieste di business" || got["icon"] != "inbox" || got["color"] != "accent" {
		t.Fatalf("rinomina: %v", got)
	}
	if got["elements"] != br["elements"] || got["fields"] != br["fields"] || got["key"] != "business_request" {
		t.Fatalf("la rinomina non tocca dati e campi: %v", got)
	}
	en := admin.do("GET", "/api/v1/type-names?lang=en", nil, http.StatusOK)["items"].([]any)
	it := admin.do("GET", "/api/v1/type-names", nil, http.StatusOK)["items"].([]any)
	nameOf := func(items []any, key string) string {
		for _, x := range items {
			if m := x.(map[string]any); m["key"] == key {
				return m["name"].(string)
			}
		}
		return ""
	}
	if nameOf(en, "business_request") != "Business request" || nameOf(it, "business_request") != "Richiesta di business" {
		t.Fatalf("nome per lingua: en=%q it=%q", nameOf(en, "business_request"), nameOf(it, "business_request"))
	}
	// Il nome nuovo compare ovunque: dettaglio del tipo e elementi.
	if n := admin.do("GET", "/api/v1/types/business_request", nil, http.StatusOK)["name"]; n != "Richiesta di business" {
		t.Fatalf("GET /types/{key}: %v", n)
	}

	// Validazioni.
	for _, body := range []map[string]any{
		{"name": "  "}, {"color": "#ff0000"}, {"icon": "Icona Strana!"},
		{"labels": map[string]any{"English": map[string]any{"name": "x"}}},
		// un tipo di sistema senza elementi si archivia (vedi TestTipiLavoro)
	} {
		admin.do("PATCH", "/api/v1/types/business_request", body, http.StatusUnprocessableEntity)
	}
	admin.do("PATCH", "/api/v1/types/inesistente", map[string]any{"name": "x"}, http.StatusNotFound)
	// Chiave e modulo non si cambiano: non sono accettati dall'API.
	admin.do("PATCH", "/api/v1/types/business_request", map[string]any{"key": "altra"}, http.StatusBadRequest)
	admin.do("PATCH", "/api/v1/types/business_request", map[string]any{"module": "work"}, http.StatusBadRequest)
	if k := find(admin, "business_request"); k["module"] != "strategy" {
		t.Fatalf("il modulo non cambia: %v", k)
	}

	// Tipo nuovo da un modello: fasi, campi e spazi si copiano.
	for _, body := range []map[string]any{
		{"key": "Non Valida", "name": "X"}, {"key": "business_request", "name": "Doppione"}, {"key": "x", "name": ""},
		{"key": "rischio2", "name": "X", "module": "grc"}, {"key": "rischio3", "name": "X", "module": "architecture"},
		{"key": "da_nulla", "name": "X", "from": "inesistente"}, {"key": "da_nulla", "name": "X", "color": "viola"},
	} {
		admin.do("POST", "/api/v1/types", body, http.StatusUnprocessableEntity)
	}
	nt := admin.do("POST", "/api/v1/types", map[string]any{
		"key": "richiesta_it", "name": "Richiesta IT", "plural_name": "Richieste IT", "from": "business_request", "module": "strategy", "icon": "wrench", "color": "ok",
	}, http.StatusCreated)
	if nt["is_system"] != false || nt["fields"].(float64) != br["fields"].(float64) || nt["process_key"] != "richiesta_it_processo" || nt["elements"].(float64) != 0 {
		t.Fatalf("tipo da modello: %v", nt)
	}
	ty := admin.do("GET", "/api/v1/types/richiesta_it", nil, http.StatusOK)
	if len(ty["states"].([]any)) < 3 {
		t.Fatalf("fasi copiate: %v", ty["states"])
	}
	// Un elemento del tipo nuovo nasce nella prima fase, nello spazio del modello.
	var spaceKey string
	err := d.InTenant(ctx, tenA, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select s.key from spaces s join space_types st on st.space_id = s.id join element_types t on t.id = st.type_id
			where t.key = 'richiesta_it' limit 1`).Scan(&spaceKey)
	})
	if err != nil {
		t.Fatalf("spazio del tipo nuovo: %v", err)
	}
	mk := func() (*engine.Element, error) {
		var el *engine.Element
		err := d.InTenant(ctx, tenA, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			el, err = eng.Create(ctx, tx, engine.Actor{MemberID: admin.member, Permissions: []string{"*"}},
				engine.CreateInput{SpaceKey: spaceKey, TypeKey: "richiesta_it", Title: "Prima"})
			return err
		})
		return el, err
	}
	el, err := mk()
	if err != nil || el.State != ty["states"].([]any)[0].(map[string]any)["name"] {
		t.Fatalf("elemento del tipo nuovo: %v %v", el, err)
	}

	// Archiviazione: i tipi non di sistema; gli elementi restano, i nuovi non si creano.
	admin.do("PATCH", "/api/v1/types/richiesta_it", map[string]any{"archived": true}, http.StatusOK)
	if a := find(admin, "richiesta_it"); a["archived"] != true || a["elements"].(float64) != 1 {
		t.Fatalf("archiviato: %v", a)
	}
	if _, err := mk(); err == nil || !strings.Contains(err.Error(), "archiviato") {
		t.Fatalf("un tipo archiviato non crea elementi: %v", err)
	}
	for _, x := range reader.do("GET", "/api/v1/type-names", nil, http.StatusOK)["items"].([]any) {
		if m := x.(map[string]any); m["key"] == "richiesta_it" && m["archived"] != true {
			t.Fatalf("i nomi segnalano l'archiviazione: %v", m)
		}
	}
	admin.do("PATCH", "/api/v1/types/richiesta_it", map[string]any{"archived": false}, http.StatusOK)
	if _, err := mk(); err != nil {
		t.Fatalf("riattivato: %v", err)
	}

	// Isolamento: l'altro tenant non vede né modifica i tipi del primo.
	adminB.do("GET", "/api/v1/types/richiesta_it", nil, http.StatusUnprocessableEntity)
	adminB.do("PATCH", "/api/v1/types/richiesta_it", map[string]any{"name": "Rubato"}, http.StatusNotFound)
	if find(adminB, "business_request")["name"] == "Richiesta di business" {
		t.Fatal("il nome di un tenant non passa all'altro")
	}
	for _, x := range adminB.do("GET", "/api/v1/types", nil, http.StatusOK)["items"].([]any) {
		if x.(map[string]any)["key"] == "richiesta_it" {
			t.Fatal("tipo di un altro tenant in elenco")
		}
	}
	// Stessa chiave nell'altro tenant: lecita.
	adminB.do("POST", "/api/v1/types", map[string]any{"key": "richiesta_it", "name": "Mia", "module": "work"}, http.StatusCreated)

	// Audit di ogni modifica.
	var n int
	err = d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action in ('type.create', 'type.update')`, tenA).Scan(&n)
	if err != nil || n < 4 {
		t.Fatalf("voci di audit: %d %v", n, err)
	}
}
