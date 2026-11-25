package itest

import (
	"context"
	"fmt"
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
	"regia/internal/modules/finance"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Etichette nelle liste (ricerca, backlog, board) e board paginata per colonna: totali esatti,
// pagine senza doppioni e senza buchi, cursore su una card sparita, spostamenti con la colonna
// parziale, filtro per etichetta.
func TestListLabelsAndPagedBoard(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("lbp-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Liste", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
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
	const sp = "/api/v1/work/spaces/WEB"
	var ids []string
	for i := 0; i < 130; i++ {
		it := admin.do("POST", sp+"/items", map[string]any{"type": "task", "title": fmt.Sprintf("Task %03d", i)}, http.StatusCreated)
		ids = append(ids, it["id"].(string))
	}
	bulk := func(idl []string, add ...string) {
		admin.do("POST", "/api/v1/work/bulk", map[string]any{"ids": idl, "commands": []map[string]any{{"kind": "labels", "add": add}}}, http.StatusOK)
	}
	bulk(ids[:3], "alfa", "beta")
	bulk(ids[3:4], "alfa")

	tagsOf := func(el any) []string {
		var out []string
		raw, _ := el.(map[string]any)["tags"].([]any)
		for _, x := range raw {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	// Ricerca: le etichette arrivano con la pagina; la query le filtra.
	page := admin.do("GET", "/api/v1/elements?limit=200&q="+url.QueryEscape("tipo:task"), nil, http.StatusOK)
	withTags := map[string]string{}
	for _, el := range page["items"].([]any) {
		if tg := tagsOf(el); len(tg) > 0 {
			withTags[el.(map[string]any)["id"].(string)] = strings.Join(tg, ",")
		}
	}
	if len(withTags) != 4 || withTags[ids[0]] != "alfa,beta" || withTags[ids[3]] != "alfa" {
		t.Fatalf("etichette nella ricerca: %v", withTags)
	}
	for q, want := range map[string]int{"etichetta:alfa": 4, "tag:beta": 3, "label:alfa label:beta": 3} {
		got := admin.do("GET", "/api/v1/elements?q="+url.QueryEscape(q), nil, http.StatusOK)["items"].([]any)
		if len(got) != want {
			t.Errorf("query %q: %d elementi, attesi %d", q, len(got), want)
		}
	}

	admin.do("GET", "/api/v1/elements?q="+url.QueryEscape("etichetta:inesistente"), nil, http.StatusUnprocessableEntity)

	// Backlog.
	bl := admin.do("GET", sp+"/backlog", nil, http.StatusOK)
	nb := 0
	for _, it := range bl["items"].([]any) {
		if len(tagsOf(it)) > 0 {
			nb++
		}
	}
	if nb != 4 {
		t.Errorf("backlog: %d elementi con etichette, attesi 4", nb)
	}

	// Board paginata (nessuno sprint attivo: tutto lo spazio).
	const base = sp + "/board?sprint=tutti"
	b := admin.do("GET", base, nil, http.StatusOK)
	if b["paged"] != true || b["page_size"].(float64) != 50 {
		t.Fatalf("la board non è paginata: paged=%v size=%v", b["paged"], b["page_size"])
	}
	count := func(c any) int { return int(c.(map[string]any)["count"].(float64)) }
	if n := count(b["columns"].([]any)[0]); n != 130 {
		t.Fatalf("conteggio esatto della prima colonna: %d, atteso 130", n)
	}
	cards := b["cards"].([]any)
	if len(cards) != 50 {
		t.Fatalf("prima pagina: %d card, attese 50", len(cards))
	}
	seen := map[string]bool{}
	var last, lastTitle string
	for _, c := range cards {
		m := c.(map[string]any)
		seen[m["id"].(string)] = true
		last, lastTitle = m["id"].(string), m["title"].(string)
	}
	if lastTitle != "Task 049" {
		t.Errorf("ordine per rank: ultima della prima pagina %q", lastTitle)
	}
	if tg := tagsOf(cards[0]); strings.Join(tg, ",") != "alfa,beta" {
		t.Errorf("etichette sulla card della board: %v", tg)
	}
	// Pagine successive: nessun doppione, nessun buco, fino alla fine.
	after := last
	for step := 0; step < 5 && len(seen) < 130; step++ {
		pg := admin.do("GET", base+"&col=0&after="+after, nil, http.StatusOK)
		pc := pg["cards"].([]any)
		if len(pc) == 0 {
			t.Fatalf("pagina vuota con %d card su 130", len(seen))
		}
		for _, c := range pc {
			id := c.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("card doppia tra le pagine: %v", c)
			}
			seen[id] = true
			after = id
		}
		if n := count(pg["columns"].([]any)[0]); n != 130 {
			t.Errorf("il conteggio cambia con la pagina: %d", n)
		}
	}
	if len(seen) != 130 {
		t.Fatalf("card lette: %d su 130", len(seen))
	}
	pg := admin.do("GET", base+"&col=0&limit=5&after="+last, nil, http.StatusOK)
	if len(pg["cards"].([]any)) != 5 {
		t.Errorf("limit=5: %d card", len(pg["cards"].([]any)))
	}
	// Cursore su una card che non è più nella colonna: errore chiaro, non una pagina sbagliata.
	mem := admin.do("GET", "/api/v1/members", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)["id"]
	el := admin.do("GET", "/api/v1/elements/"+last, nil, http.StatusOK)
	admin.do("PUT", "/api/v1/work/elements/"+last+"/assignee", map[string]any{"member_id": mem, "version": el["version"]}, http.StatusOK)
	admin.do("POST", sp+"/board/move", map[string]any{"element": last, "column": 1}, http.StatusOK)
	admin.do("GET", base+"&col=0&after="+last, nil, http.StatusUnprocessableEntity)
	// I conteggi restano esatti con la colonna parziale; il filtro q li restringe.
	b = admin.do("GET", base, nil, http.StatusOK)
	if count(b["columns"].([]any)[0]) != 129 || count(b["columns"].([]any)[1]) != 1 {
		t.Errorf("conteggi dopo lo spostamento: %v", b["columns"])
	}
	b = admin.do("GET", base+"&q="+url.QueryEscape("etichetta:alfa"), nil, http.StatusOK)
	if len(b["cards"].([]any)) != 4 || count(b["columns"].([]any)[0]) != 4 {
		t.Errorf("board filtrata: %d card, colonna %d", len(b["cards"].([]any)), count(b["columns"].([]any)[0]))
	}
	admin.do("GET", base+"&col=99", nil, http.StatusUnprocessableEntity)
}
