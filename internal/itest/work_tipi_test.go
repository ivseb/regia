package itest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// I tipi del lavoro vengono dai dati: un tipo del modulo work creato dall'editor arriva sulla board,
// nel backlog e in «Il mio lavoro»; un tipo (anche di sistema) si archivia solo senza elementi e
// sparisce dai menu, senza che il provisioning lo riattivi.
func TestTipiLavoro(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("tlv-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Tipi lavoro", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng),
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)

	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	const board = "/api/v1/work/spaces/WEB/board"
	strs := func(v any) []string {
		var out []string
		for _, x := range v.([]any) {
			out = append(out, x.(string))
		}
		return out
	}
	createKeys := func(b map[string]any) []string {
		var out []string
		for _, c := range b["create_types"].([]any) {
			out = append(out, c.(map[string]any)["key"].(string))
		}
		return out
	}

	// Un tipo nuovo senza modello: ammesso nello spazio del team e proposto dalla board.
	admin.do("POST", "/api/v1/types", map[string]any{"key": "nuvola", "name": "Nuvola", "module": "work"}, http.StatusCreated)
	b := admin.do("GET", board, nil, http.StatusOK)
	if ck := createKeys(b); !slices.Contains(ck, "nuvola") || slices.Contains(ck, "epic") {
		t.Fatalf("tipi creabili: %v", ck)
	}

	// Un tipo da un modello: sulla board, nel backlog, in «Il mio lavoro» e con i fatti del modulo.
	admin.do("POST", "/api/v1/types", map[string]any{"key": "idea", "name": "Idea", "from": "task"}, http.StatusCreated)
	b = admin.do("GET", board, nil, http.StatusOK)
	var idea map[string]any
	for _, c := range b["create_types"].([]any) {
		if c.(map[string]any)["key"] == "idea" {
			idea = c.(map[string]any)
		}
	}
	if idea == nil || idea["points"] != false {
		t.Fatalf("idea non creabile: %v", b["create_types"])
	}
	if !slices.Contains(strs(b["settings"].(map[string]any)["types"]), "idea") {
		t.Fatalf("idea non è sulla board: %v", b["settings"])
	}
	it := admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "idea", "title": "Una buona idea"}, http.StatusCreated)
	admin.do("PUT", "/api/v1/work/elements/"+it["key"].(string)+"/assignee", map[string]any{"member_id": admin.member}, http.StatusOK)
	found := func(list any) bool {
		for _, c := range list.([]any) {
			if c.(map[string]any)["key"] == it["key"] {
				return true
			}
		}
		return false
	}
	if b = admin.do("GET", board+"?sprint=tutti", nil, http.StatusOK); !found(b["cards"]) {
		t.Fatalf("la card non è sulla board: %v", b["cards"])
	}
	if bl := admin.do("GET", "/api/v1/work/spaces/WEB/backlog", nil, http.StatusOK); !found(bl["items"]) {
		t.Fatalf("l'idea non è nel backlog")
	}
	if my := admin.do("GET", "/api/v1/work/me", nil, http.StatusOK); !found(my["items"]) {
		t.Fatalf("l'idea non è in «Il mio lavoro»")
	}
	// Un'idea sta sotto un epic come ogni tipo del lavoro.
	ep := admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "epic", "title": "Contenitore"}, http.StatusCreated)
	admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "idea", "title": "Sotto l'epic", "parent": ep["key"]}, http.StatusCreated)
	admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "idea", "title": "Sotto una story", "parent": it["key"]}, http.StatusUnprocessableEntity)

	// Archiviazione: epic ha elementi, sprint no.
	admin.do("PATCH", "/api/v1/types/epic", map[string]any{"archived": true}, http.StatusUnprocessableEntity)
	if r := admin.do("PATCH", "/api/v1/types/sprint", map[string]any{"archived": true}, http.StatusOK); r["archived"] != true {
		t.Fatalf("sprint non archiviato: %v", r)
	}
	admin.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Non si crea"}, http.StatusUnprocessableEntity)
	if b = admin.do("GET", board, nil, http.StatusOK); b["settings"].(map[string]any)["no_sprint"] != true {
		t.Fatalf("senza sprint la board è un flusso continuo: %v", b["settings"])
	}
	// Il provisioning, rieseguito a ogni aggiornamento, non riattiva il tipo archiviato.
	if err := tenant.Provision(ctx, d, slug, prov); err != nil {
		t.Fatal(err)
	}
	for _, x := range admin.do("GET", "/api/v1/types", nil, http.StatusOK)["items"].([]any) {
		if m := x.(map[string]any); m["key"] == "sprint" && m["archived"] != true {
			t.Fatalf("il provisioning ha riattivato lo sprint: %v", m)
		}
	}
	admin.do("PATCH", "/api/v1/types/sprint", map[string]any{"archived": false}, http.StatusOK)
	admin.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Si crea"}, http.StatusCreated)
	admin.do("PATCH", "/api/v1/types/sprint", map[string]any{"archived": true}, http.StatusUnprocessableEntity)

	// Un tipo archiviato sparisce dalla board e dai tipi creabili; la corsia «epic» si nasconde.
	admin.do("PATCH", "/api/v1/types/nuvola", map[string]any{"archived": true}, http.StatusOK)
	admin.do("PATCH", "/api/v1/types/nuvola", map[string]any{"archived": false}, http.StatusOK)
	admin.do("PATCH", "/api/v1/types/nuvola", map[string]any{"archived": true}, http.StatusOK)
	b = admin.do("GET", board+"?swimlane=epic", nil, http.StatusOK)
	if slices.Contains(createKeys(b), "nuvola") || !slices.Contains(createKeys(b), "idea") {
		t.Fatalf("tipi creabili dopo l'archiviazione: %v", createKeys(b))
	}
	if b["settings"].(map[string]any)["swimlane"] != "epic" {
		t.Fatalf("corsia epic: %v", b["settings"])
	}
}
