package itest

import (
	"bufio"
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
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Pianificazione: piano automatico con dipendenze e festività, spostamento a cascata,
// finestra della richiesta, roadmap, carico dei team, eventi in tempo reale.
func TestPlanning(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("plan-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Piani", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "req@" + slug + ".test", Name: "Richiedente",
		Password: testPassword, MemberType: "requester", Roles: []string{"requester"}}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)

	csv := "codice,nome,team\nBE,Backend,Integrazioni\nFE,Frontend,Web\nX,Report,Dati\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	ids := map[string]string{}
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		ids[m["code"].(string)] = m["id"].(string)
	}
	br := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Piano di prova", "regulatory_deadline": "2027-01-31"}, http.StatusCreated)
	key := br["key"].(string)
	be := admin.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{"application_id": ids["BE"], "estimate_effort_days": 5}, http.StatusCreated)
	fe := admin.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{
		"application_id": ids["FE"], "estimate_effort_days": 10, "depends_on": []string{be["key"].(string)}}, http.StatusCreated)
	x := admin.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{"application_id": ids["X"]}, http.StatusCreated)

	dates := func() map[string][2]string {
		out := map[string][2]string{}
		for _, tk := range admin.do("GET", "/api/v1/demand/requests/"+key+"/plan", nil, http.StatusOK)["tasks"].([]any) {
			m := tk.(map[string]any)
			s, _ := m["start"].(string)
			e, _ := m["end"].(string)
			out[m["id"].(string)] = [2]string{s, e}
		}
		return out
	}
	expect := func(what string, id, start, end string) {
		t.Helper()
		if got := dates()[id]; got != [2]string{start, end} {
			t.Fatalf("%s: %v, atteso %s → %s", what, got, start, end)
		}
	}

	// Eventi: un client collegato riceve l'avviso della modifica.
	evReq, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	evResp, err := admin.c.Do(evReq)
	if err != nil || evResp.StatusCode != http.StatusOK {
		t.Fatalf("flusso di eventi: %v %v", err, evResp)
	}
	defer evResp.Body.Close()
	got := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(evResp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				got <- sc.Text()
			}
		}
	}()

	// Piano automatico dal 1° dicembre 2026: il backend dura 5 giorni, il frontend parte
	// dopo (l'8 dicembre è festa) e dura 10 giorni; il report senza stima ne prende 5.
	req.do("POST", "/api/v1/demand/requests/"+key+"/plan/auto", map[string]any{"from": "2026-12-01"}, http.StatusNotFound) // non la vede nemmeno
	admin.do("POST", "/api/v1/demand/requests/"+key+"/plan/auto", map[string]any{"from": "2026-12-01"}, http.StatusOK)
	expect("backend", be["id"].(string), "2026-12-01", "2026-12-07")
	expect("frontend", fe["id"].(string), "2026-12-09", "2026-12-22")
	expect("report", x["id"].(string), "2026-12-01", "2026-12-07")
	select {
	case ev := <-got:
		if !strings.Contains(ev, "plan/auto") {
			t.Fatalf("evento: %s", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nessun evento in tempo reale dopo la modifica")
	}

	// Spostando il backend il frontend slitta a cascata, saltando Natale, Capodanno ed Epifania.
	ch := admin.do("PUT", "/api/v1/impacts/"+be["id"].(string)+"/schedule", map[string]any{"start": "2026-12-14", "end": "2026-12-18"}, http.StatusOK)
	if n := len(ch["changes"].([]any)); n != 2 {
		t.Fatalf("modifiche: %v", ch)
	}
	expect("frontend a cascata", fe["id"].(string), "2026-12-21", "2027-01-05")
	plan := admin.do("GET", "/api/v1/demand/requests/"+key+"/plan", nil, http.StatusOK)
	r := plan["request"].(map[string]any)
	if r["start"] != "2026-12-01" || r["end"] != "2027-01-05" || plan["can_edit"] != true {
		t.Fatalf("finestra della richiesta: %v", r)
	}
	admin.do("PUT", "/api/v1/impacts/"+be["id"].(string)+"/schedule", map[string]any{"start": "2026-12-20", "end": "2026-12-14"}, http.StatusUnprocessableEntity)

	// Carico dei team: la settimana del 14 dicembre il team Integrazioni ha 5 giorni su 5.
	load := admin.do("GET", "/api/v1/teams/load?from=2026-11-30&to=2026-12-31", nil, http.StatusOK)["items"].([]any)
	found := false
	for _, tl := range load {
		m := tl.(map[string]any)
		if m["name"] != "Integrazioni" {
			continue
		}
		for _, w := range m["weeks"].([]any) {
			wk := w.(map[string]any)
			if wk["week"] == "2026-12-14" {
				found = wk["load_days"].(float64) == 5 && wk["capacity_days"].(float64) == 5
			}
		}
	}
	if !found {
		t.Fatalf("carico dei team: %v", load)
	}

	// Spostare la richiesta sposta tutti gli impatti di una settimana (l'8 dicembre è festa).
	admin.do("PUT", "/api/v1/demand/requests/"+key+"/schedule", map[string]any{"start": "2026-12-08", "end": "2027-01-12"}, http.StatusOK)
	expect("report spostato", x["id"].(string), "2026-12-09", "2026-12-15")

	// Roadmap: la richiesta compare con la sua finestra e la scadenza normativa.
	var row map[string]any
	for _, it := range admin.do("GET", "/api/v1/demand/roadmap", nil, http.StatusOK)["items"].([]any) {
		if m := it.(map[string]any); m["key"] == key {
			row = m
		}
	}
	if row == nil || row["start"] != "2026-12-09" || row["regulatory_deadline"] != "2027-01-31" || row["scheduled"].(float64) != 3 {
		t.Fatalf("roadmap: %v", row)
	}
}
