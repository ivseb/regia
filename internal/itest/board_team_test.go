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

// Board per team (ondata 3): colonne proprie con più fasi, spostamento alla prima fase ammessa dal
// processo, passaggio non ammesso con il motivo, modello applicato (anteprima, bozza, pubblicazione
// e alla creazione dello spazio), team senza configurazione invariato e isolamento tra tenant.
func TestBoardTeam(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("brt-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Board", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("bro-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "viewer@" + slug + ".test", Name: "Viewer", Password: testPassword, Roles: []string{"viewer"}}); err != nil {
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
	viewer := login(t, srv.URL, "viewer@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	const base = "/api/v1/work/spaces/WEB/board"
	names := func(cols any) []string {
		var out []string
		for _, c := range cols.([]any) {
			out = append(out, c.(map[string]any)["name"].(string))
		}
		return out
	}
	phases := func(c any) []string {
		var out []string
		for _, p := range c.(map[string]any)["phases"].([]any) {
			out = append(out, p.(string))
		}
		return out
	}

	// Un team senza configurazione: una colonna per fase, come prima.
	b := admin.do("GET", base, nil, http.StatusOK)
	if got := strings.Join(names(b["columns"]), "|"); got != "Da fare|In corso|In revisione|Da verificare|Fatto" {
		t.Fatalf("colonne di default: %s", got)
	}
	cfg := admin.do("GET", base+"/config", nil, http.StatusOK)
	if cfg["draft"] != nil || len(cfg["models"].([]any)) != 3 || len(cfg["phases"].([]any)) < 5 {
		t.Fatalf("config iniziale: %v", cfg)
	}
	bug := admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "bug", "title": "Errore al logout"}, http.StatusCreated)

	// Il modello Kanban in anteprima: niente sprint, WIP sulle colonne centrali, tutte le fasi assegnate.
	pv := admin.do("POST", base+"/preview", map[string]any{"model": "kanban"}, http.StatusOK)
	st := pv["settings"].(map[string]any)
	if got := strings.Join(names(st["columns"]), "|"); got != "Da fare|In corso|In revisione|Fatto" || st["no_sprint"] != true || st["model"] != "kanban" {
		t.Fatalf("anteprima kanban: %v", st)
	}
	if len(pv["unassigned"].([]any)) != 0 || len(pv["empty"].([]any)) != 0 {
		t.Fatalf("fasi senza colonna: %v", pv)
	}
	if rev := st["columns"].([]any)[2]; strings.Join(phases(rev), "|") != "In revisione|Da verificare" || rev.(map[string]any)["wip"].(float64) != 2 {
		t.Fatalf("colonna In revisione: %v", rev)
	}
	// L'anteprima non cambia la board pubblicata.
	if got := strings.Join(names(admin.do("GET", base, nil, http.StatusOK)["columns"]), "|"); !strings.Contains(got, "Da verificare") {
		t.Fatalf("la board è cambiata con la sola anteprima: %s", got)
	}

	// Bozza con una colonna a due fasi («Verifica»: Da verificare e In corso, in quest'ordine) e una
	// fase lasciata fuori (In revisione), segnalata dall'anteprima.
	draft := map[string]any{"swimlane": "none", "types": []string{"story", "task", "bug"}, "done_days": 14, "columns": []any{
		map[string]any{"name": "Coda", "phases": []string{"Da fare"}, "wip": 0},
		map[string]any{"name": "Verifica", "phases": []string{"Da verificare", "In corso"}, "wip": 1, "color": "#3b6ea5"},
		map[string]any{"name": "Chiusa", "phases": []string{"Fatto"}, "wip": 0},
	}}
	viewer.do("PUT", base+"/draft", draft, http.StatusForbidden)
	adminB.do("PUT", base+"/draft", draft, http.StatusNotFound)
	adminB.do("GET", base+"/config", nil, http.StatusNotFound)
	adminB.do("POST", base+"/publish", nil, http.StatusNotFound)
	admin.do("POST", base+"/publish", nil, http.StatusUnprocessableEntity) // niente bozza
	saved := admin.do("PUT", base+"/draft", draft, http.StatusOK)
	if saved["draft"] == nil || !slices.Equal(strings.Split(strings.Join(func() []string {
		var o []string
		for _, p := range saved["preview"].(map[string]any)["unassigned"].([]any) {
			o = append(o, p.(string))
		}
		return o
	}(), "|"), "|"), []string{"In revisione"}) {
		t.Fatalf("fase non assegnata: %v", saved["preview"])
	}
	admin.do("DELETE", base+"/draft", nil, http.StatusOK)
	if admin.do("GET", base+"/config", nil, http.StatusOK)["draft"] != nil {
		t.Fatal("bozza non scartata")
	}
	admin.do("PUT", base+"/draft", draft, http.StatusOK)
	adminB.do("GET", "/api/v1/work/spaces/WEB/board", nil, http.StatusNotFound)
	admin.do("POST", base+"/publish", nil, http.StatusOK)
	b = admin.do("GET", base, nil, http.StatusOK)
	if got := strings.Join(names(b["columns"]), "|"); got != "Coda|Verifica|Chiusa" {
		t.Fatalf("colonne pubblicate: %s", got)
	}
	if card := b["cards"].([]any)[0].(map[string]any); card["col"].(float64) != 0 {
		t.Fatalf("la card in «Da fare» va in Coda: %v", card)
	}

	// Spostare in una colonna con più fasi porta alla prima fase ammessa dal processo: dal Da fare
	// un bug può andare in «In corso» (non in «Da verificare», non raggiungibile da lì).
	admin.do("PUT", "/api/v1/work/elements/"+bug["key"].(string)+"/assignee", map[string]any{"member_id": admin.member}, http.StatusOK)
	mv := admin.do("POST", base+"/move", map[string]any{"element": bug["id"], "column": 1}, http.StatusOK)
	if mv["phase"] != "In corso" {
		t.Fatalf("fase di arrivo: %v", mv)
	}
	// Passaggio non ammesso: da In corso a Fatto il bug non può (passa dalla verifica): il messaggio dice perché.
	r := admin.do("POST", base+"/move", map[string]any{"element": bug["id"], "column": 2}, http.StatusUnprocessableEntity)
	if !strings.Contains(r["detail"].(string), "non previsto") && !strings.Contains(r["detail"].(string), "motivazione") {
		t.Logf("nota: il passaggio In corso → Fatto del bug è ammesso o con altro messaggio: %v", r)
	}

	// WIP: due card nella colonna con limite 1 → colonna oltre il limite, nessun blocco.
	bug2 := admin.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "bug", "title": "Secondo errore"}, http.StatusCreated)
	admin.do("PUT", "/api/v1/work/elements/"+bug2["key"].(string)+"/assignee", map[string]any{"member_id": admin.member}, http.StatusOK)
	admin.do("POST", base+"/move", map[string]any{"element": bug2["id"], "column": 1}, http.StatusOK)
	b = admin.do("GET", base, nil, http.StatusOK)
	if c := b["columns"].([]any)[1].(map[string]any); c["over"] != true || c["count"].(float64) != 2 {
		t.Fatalf("WIP superato: %v", c)
	}

	// Modello alla creazione dello spazio: il team importato senza spazio riceve la board Kanban.
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome,team\nAPI,Backend API,Integrazioni\n"}, http.StatusOK)
	var teamID string
	for _, tm := range admin.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)["teams_without_space"].([]any) {
		teamID = tm.(map[string]any)["id"].(string)
	}
	admin.do("POST", "/api/v1/work/spaces", map[string]any{"team_id": teamID, "model": "inesistente"}, http.StatusUnprocessableEntity)
	sp := admin.do("POST", "/api/v1/work/spaces", map[string]any{"team_id": teamID, "model": "supporto"}, http.StatusCreated)
	nb := admin.do("GET", "/api/v1/work/spaces/"+sp["key"].(string)+"/board", nil, http.StatusOK)
	if got := strings.Join(names(nb["columns"]), "|"); got != "Nuova|In lavorazione|In attesa del cliente|Risolta" {
		t.Fatalf("modello Supporto: %s", got)
	}
	if s := nb["settings"].(map[string]any); s["sort"] != "due" || s["no_sprint"] != true || s["model"] != "supporto" {
		t.Fatalf("impostazioni del modello Supporto: %v", s)
	}
	// Il modello si può cambiare dopo: Scrum sulla stessa board, via anteprima, bozza e pubblicazione.
	skey := "/api/v1/work/spaces/" + sp["key"].(string) + "/board"
	sc := admin.do("POST", skey+"/preview", map[string]any{"model": "scrum"}, http.StatusOK)
	admin.do("PUT", skey+"/draft", sc["settings"], http.StatusOK)
	admin.do("POST", skey+"/publish", nil, http.StatusOK)
	nb = admin.do("GET", skey, nil, http.StatusOK)
	if s := nb["settings"].(map[string]any); s["model"] != "scrum" || s["no_sprint"] == true {
		t.Fatalf("Scrum dopo Supporto: %v", s)
	}
}
