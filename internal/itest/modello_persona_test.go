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

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Modello «persona sola / home lab»: moduli, tipi (Attività, Bug, Idea), flusso a quattro fasi per i tre
// tipi, solo la priorità tra i campi, team con spazio e board kanban senza sprint. Riapplicarlo non duplica.
func TestAvvioModelloPersonaSola(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("persona-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Persona", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng),
		Reviews: reviews.Register(eng), Requirements: requirements.Register(eng, ap), GRC: grc.Register(eng),
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	adm := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// Il modello è disponibile e subito dopo quello di banca e assicurazione.
	ms := list(adm.do("GET", "/api/v1/avvio/modelli", nil, http.StatusOK)["items"])
	if obj(ms[1])["key"] != "persona_sola" || obj(ms[1])["available"] != true {
		t.Fatalf("modelli: %v", ms)
	}

	// Una chiave non valida è un errore di campo; l'anteprima elenca cosa nasce e non lascia niente.
	bad := adm.do("GET", "/api/v1/avvio/modelli/persona_sola?board_key=1x", nil, http.StatusUnprocessableEntity)
	if bad["field"] != "board_key" {
		t.Fatalf("chiave non valida: %v", bad)
	}
	pre := adm.do("GET", "/api/v1/avvio/modelli/persona_sola?board_key=HL&team_name=Home%20lab", nil, http.StatusOK)
	kinds := map[string]map[string]string{}
	for _, it := range list(pre["items"]) {
		o := obj(it)
		k := o["kind"].(string)
		if kinds[k] == nil {
			kinds[k] = map[string]string{}
		}
		kinds[k][o["key"].(string)] = o["state"].(string)
	}
	if kinds["type"]["idea"] != "new" || kinds["team"]["team"] != "new" || kinds["board"]["HL"] != "new" || len(kinds["process"]) != 3 || len(kinds["module"]) != 6 {
		t.Fatalf("anteprima: %v", kinds)
	}
	if n := len(list(adm.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)["items"])); n != 0 {
		t.Fatalf("l'anteprima ha creato spazi: %d", n)
	}

	// Applicazione.
	body := map[string]string{"board_key": "hl", "team_name": "Home lab"}
	res := adm.do("POST", "/api/v1/avvio/modelli/persona_sola", body, http.StatusCreated)
	if res["applied"] != true || res["new"].(float64) < 10 {
		t.Fatalf("applicazione: %v", res)
	}

	// Moduli: accesi Work, DevSecOps, Knowledge, Architettura; spenti Demand e GRC.
	mods := map[string]bool{}
	for _, m := range list(adm.do("GET", "/api/v1/avvio/applicato", nil, http.StatusOK)["modules"]) {
		mods[obj(m)["key"].(string)] = obj(m)["enabled"] == true
	}
	for k, want := range map[string]bool{"work": true, "devsecops": true, "knowledge": true, "architecture": true, "demand": false, "grc": false} {
		if mods[k] != want {
			t.Fatalf("modulo %s: %v", k, mods)
		}
	}

	// Tipi.
	types := map[string]map[string]any{}
	for _, it := range list(adm.do("GET", "/api/v1/types", nil, http.StatusOK)["items"]) {
		types[obj(it)["key"].(string)] = obj(it)
	}
	if types["task"]["name"] != "Attività" || obj(obj(types["task"]["labels"])["en"])["name"] != "Task" || types["bug"]["name"] != "Bug" {
		t.Fatalf("task/bug: %v %v", types["task"], types["bug"])
	}
	if types["idea"] == nil || types["idea"]["archived"] == true || types["idea"]["module"] != "work" || types["idea"]["is_system"] == true {
		t.Fatalf("idea: %v", types["idea"])
	}
	for _, k := range []string{"story", "epic", "sprint"} {
		if types[k]["archived"] != true {
			t.Fatalf("%s non archiviato: %v", k, types[k])
		}
	}

	// Flusso: quattro fasi, nessun gate.
	versions := map[string]int{}
	for _, tk := range []string{"task", "bug", "idea"} {
		pk := tk + "_standard"
		if tk == "idea" {
			pk = "idea_processo"
		}
		p := adm.do("GET", "/api/v1/processes/"+pk, nil, http.StatusOK)
		cur := obj(p["current"])
		var names []string
		for _, ph := range list(cur["phases"]) {
			names = append(names, obj(ph)["name"].(string))
		}
		if strings.Join(names, ",") != "Da fare,In corso,Fatto,Annullato" || len(list(cur["gates"])) != 0 {
			t.Fatalf("flusso di %s: %v", tk, cur)
		}
		versions[pk] = len(list(p["versions"]))
	}

	// Campi: solo la priorità.
	for _, tk := range []string{"task", "bug", "idea"} {
		var keys []string
		for _, f := range list(adm.do("GET", "/api/v1/types/"+tk+"/fields", nil, http.StatusOK)["items"]) {
			keys = append(keys, obj(f)["key"].(string))
		}
		for _, k := range keys {
			if k == "punti" || k == "stima" {
				t.Fatalf("%s ha ancora %s: %v", tk, k, keys)
			}
		}
		if !strings.Contains(strings.Join(keys, ","), "priorita") {
			t.Fatalf("%s senza priorità: %v", tk, keys)
		}
	}

	// Team e board: kanban senza sprint, colonne Da fare, In corso, Fatto, nessuna vuota.
	board := adm.do("GET", "/api/v1/work/spaces/HL/board", nil, http.StatusOK)
	st := obj(board["settings"])
	if st["model"] != "kanban" || st["no_sprint"] != true {
		t.Fatalf("impostazioni della board: %v", st)
	}
	var cols []string
	for _, c := range list(board["columns"]) {
		cols = append(cols, obj(c)["name"].(string))
	}
	if strings.Join(cols, ",") != "Da fare,In corso,Fatto" {
		t.Fatalf("colonne: %v", cols)
	}
	var tt []string
	for _, x := range list(st["types"]) {
		tt = append(tt, x.(string))
	}
	if strings.Join(tt, ",") != "task,bug,idea" {
		t.Fatalf("tipi della board: %v", tt)
	}
	if board["team"] != "Home lab" {
		t.Fatalf("team: %v", board["team"])
	}
	card := adm.do("POST", "/api/v1/work/spaces/HL/items", map[string]any{"type": "task", "title": "Prima attività"}, http.StatusCreated)
	if card["key"] == nil {
		t.Fatalf("card: %v", card)
	}

	// Riapplicarlo non duplica niente: nessuna versione in più, un solo spazio.
	again := adm.do("POST", "/api/v1/avvio/modelli/persona_sola", body, http.StatusCreated)
	if again["new"].(float64) != 0 {
		t.Fatalf("la seconda applicazione crea ancora: %v", again)
	}
	for pk, n := range versions {
		if m := len(list(adm.do("GET", "/api/v1/processes/"+pk, nil, http.StatusOK)["versions"])); m != n {
			t.Fatalf("%s: versioni %d dopo la seconda applicazione, erano %d", pk, m, n)
		}
	}
	if n := len(list(adm.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)["items"])); n != 1 {
		t.Fatalf("spazi dopo la seconda applicazione: %d", n)
	}

	// Una chiave già usata (con un altro nome di team) è un errore di campo.
	dup := adm.do("POST", "/api/v1/avvio/modelli/persona_sola", map[string]string{"board_key": "HL", "team_name": "Altro team"}, http.StatusUnprocessableEntity)
	if dup["field"] != "board_key" {
		t.Fatalf("chiave usata: %v", dup)
	}
}
