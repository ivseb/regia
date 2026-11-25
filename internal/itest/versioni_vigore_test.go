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
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Versioni dei processi con data di entrata in vigore: programmazione, deprecazione, regola di
// transizione, guide allegate e avviso sul percorso della richiesta (migrazione 0069).
func TestVersioniInVigore(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("vig-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Versioni", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range [][2]string{{"editor", "editor"}, {"proc", "process_admin"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m[0] + "@" + slug + ".test", Name: m[0],
			Password: testPassword, Roles: []string{m[1]}}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	editor := login(t, srv.URL, "editor@"+slug+".test", testPassword)
	conf := login(t, srv.URL, "proc@"+slug+".test", testPassword)

	procVersion := func(key string) float64 {
		return admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["process"].(map[string]any)["version"].(float64)
	}
	// A resta in fase iniziale; B avanza fino a una fase già avviata.
	a := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta A"}, http.StatusCreated)
	b := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta B"}, http.StatusCreated)
	aKey, bKey, bID := a["key"].(string), b["key"].(string), b["id"].(string)
	admin.do("POST", "/api/v1/elements/"+bID+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	admin.do("POST", "/api/v1/elements/"+bID+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
	admin.do("POST", "/api/v1/elements/"+bID+"/transitions", map[string]any{"to": "Valutazione"}, http.StatusOK)

	// Bozza uguale alla corrente (basta per provare il ciclo).
	cur := conf.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)["current"]
	conf.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": cur, "notes": "Nuovo standard"}, http.StatusOK)

	// Anteprima: pubblicazione programmata, regola predefinita proposta.
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	pv := conf.do("POST", "/api/v1/processes/demand_standard/publish/preview", map[string]any{"effective_from": tomorrow}, http.StatusOK)
	if pv["immediate"] != false || pv["in_force"].(float64) != 1 || pv["version"].(float64) != 2 {
		t.Fatalf("anteprima: %v", pv)
	}
	plan := pv["plan"].(map[string]any)
	if plan["auto"].(float64) < 1 || plan["stay"].(float64) < 1 || plan["total"].(float64) != 2 {
		t.Fatalf("la regola predefinita dovrebbe migrare A e lasciare B: %v", plan)
	}
	editor.do("POST", "/api/v1/processes/demand_standard/publish/preview", map[string]any{}, http.StatusForbidden)

	// Pubblicazione programmata: nulla cambia finché la data non arriva.
	conf.do("POST", "/api/v1/processes/demand_standard/publish", map[string]any{
		"effective_from": tomorrow, "transition": plan["rule"]}, http.StatusOK)
	p := conf.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	lifes := map[float64]string{}
	for _, v := range p["versions"].([]any) {
		m := v.(map[string]any)
		lifes[m["number"].(float64)] = m["lifecycle"].(string)
	}
	if lifes[1] != "in_force" || lifes[2] != "scheduled" {
		t.Fatalf("stati delle versioni: %v", lifes)
	}
	c := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta C"}, http.StatusCreated)
	if procVersion(c["key"].(string)) != 1 || procVersion(aKey) != 1 {
		t.Fatal("prima della data tutto resta sulla v1")
	}

	// Arriva la data: all'avvio di una richiesta la v2 entra in vigore, A migra, B resta, v1 è deprecata.
	if _, err := d.Owner.Exec(ctx, `update process_versions set effective_from = now() - interval '1 minute' where status = 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	e := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta D"}, http.StatusCreated)
	if procVersion(e["key"].(string)) != 2 || procVersion(aKey) != 2 || procVersion(bKey) != 1 {
		t.Fatalf("dopo la data: D=%v A=%v B=%v", procVersion(e["key"].(string)), procVersion(aKey), procVersion(bKey))
	}
	p = conf.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	for _, v := range p["versions"].([]any) {
		m := v.(map[string]any)
		if m["number"].(float64) == 1 && (m["lifecycle"] != "deprecated" || m["deprecated_at"] == nil) {
			t.Fatalf("la v1 dovrebbe essere deprecata: %v", m)
		}
	}
	var n int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where action in ('process.activate', 'process.transition')`).Scan(&n); err != nil || n < 2 {
		t.Fatalf("audit dell'entrata in vigore: %d %v", n, err)
	}

	// Guide: lettura per tutti, scrittura solo per process.admin, controlli sui dati.
	conf.do("POST", "/api/v1/processes/demand_standard/guides", map[string]any{"kind": "link", "title": "Linee guida", "target": "https://esempio.test/guida"}, http.StatusCreated)
	conf.do("POST", "/api/v1/processes/demand_standard/guides", map[string]any{"kind": "link", "title": "Valutare", "target": "/documenti/DOC-1", "phase": "Valutazione"}, http.StatusCreated)
	conf.do("POST", "/api/v1/processes/demand_standard/guides", map[string]any{"kind": "link", "title": "Cattiva", "target": "javascript:alert(1)"}, http.StatusUnprocessableEntity)
	conf.do("POST", "/api/v1/processes/demand_standard/guides", map[string]any{"kind": "link", "title": "X", "target": "https://a.test", "phase": "Inesistente"}, http.StatusUnprocessableEntity)
	editor.do("POST", "/api/v1/processes/demand_standard/guides", map[string]any{"kind": "link", "title": "X", "target": "https://a.test"}, http.StatusForbidden)
	if g := editor.do("GET", "/api/v1/processes/demand_standard/guides", nil, http.StatusOK)["items"].([]any); len(g) != 2 {
		t.Fatalf("guide: %v", g)
	}

	// Percorso: B (fase Valutazione, v1 deprecata) vede l'avviso e le due guide; A (Bozza) solo quella del processo.
	pb := admin.do("GET", "/api/v1/elements/"+bKey+"/percorso", nil, http.StatusOK)
	notice, _ := pb["version_notice"].(map[string]any)
	if notice == nil || notice["current_version"].(float64) != 2 || notice["version"].(float64) != 1 || notice["can_migrate"] != true {
		t.Fatalf("avviso: %v", pb["version_notice"])
	}
	if len(pb["guides"].([]any)) != 2 {
		t.Fatalf("guide di B: %v", pb["guides"])
	}
	pa := admin.do("GET", "/api/v1/elements/"+aKey+"/percorso", nil, http.StatusOK)
	if pa["version_notice"] != nil || len(pa["guides"].([]any)) != 1 {
		t.Fatalf("percorso di A: %v %v", pa["version_notice"], pa["guides"])
	}

	// Data di fine sulla versione in vigore; la data di una versione non programmata non si sposta.
	end := time.Now().AddDate(0, 6, 0).Format("2006-01-02")
	conf.do("PATCH", "/api/v1/processes/demand_standard/versions/2/vigore", map[string]any{"effective_to": end}, http.StatusOK)
	conf.do("PATCH", "/api/v1/processes/demand_standard/versions/2/vigore", map[string]any{"effective_from": tomorrow}, http.StatusUnprocessableEntity)
}

func TestVersioniUnit(t *testing.T) {
	r := engine.TransitionRule{Default: engine.ModeStay, Phases: map[string]string{"Bozza": engine.ModeAuto}}
	if r.ModeOf("bozza") != engine.ModeAuto || r.ModeOf("Altro") != engine.ModeStay {
		t.Fatal("ModeOf")
	}
	if (engine.TransitionRule{Default: "boh"}).Check() == nil {
		t.Fatal("modo non valido accettato")
	}
	w := &engine.Workflow{Phases: []engine.Phase{{Name: "Bozza", Category: "todo"}, {Name: "Esecuzione", Category: "in_progress"}, {Name: "Sospesa", Category: "todo", OffPath: true}}}
	dr := engine.DefaultTransitionRule(w)
	if dr.Phases["Bozza"] != engine.ModeAuto || dr.Phases["Esecuzione"] != engine.ModeStay || dr.Phases["Sospesa"] != engine.ModeStay {
		t.Fatalf("regola predefinita: %v", dr)
	}
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, c := range []struct {
		status         string
		to, deprecated *time.Time
		want           string
	}{
		{"draft", nil, nil, "draft"}, {"scheduled", nil, nil, "scheduled"}, {"published", nil, nil, "in_force"},
		{"published", &past, nil, "concluded"}, {"superseded", nil, &past, "deprecated"},
		{"superseded", &past, &past, "concluded"}, {"superseded", &future, &past, "deprecated"}, {"superseded", nil, nil, "withdrawn"},
	} {
		if got := engine.Lifecycle(c.status, nil, c.to, c.deprecated, now); got != c.want {
			t.Fatalf("Lifecycle(%s) = %s, atteso %s", c.status, got, c.want)
		}
	}
	if d, err := engine.ParseVigoreDate("2026-12-01"); err != nil || d == nil || d.Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("data: %v %v", d, err)
	}
	if _, err := engine.ParseVigoreDate("domani"); err == nil {
		t.Fatal("data non valida accettata")
	}
}
