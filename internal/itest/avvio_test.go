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

// Primo avvio guidato (ondata 3, proposta 7B): la checklist è calcolata, l'anteprima di un modello non
// scrive niente, l'applicazione porta cataloghi «da verificare» e organi di governo, e un processo di
// partenza modificato si riporta all'originale.
func TestAvvioModelloERipristino(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("avvio-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Avvio", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "ed@" + slug + ".test", Name: "ed", Password: testPassword, Roles: []string{"editor"}}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	gs := grc.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	adm, ed := login(t, srv.URL, "admin@"+slug+".test", testPassword), login(t, srv.URL, "ed@"+slug+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	step := func(st map[string]any, key string) map[string]any {
		for _, s := range list(st["steps"]) {
			if obj(s)["key"] == key {
				return obj(s)
			}
		}
		t.Fatalf("passo %s mancante", key)
		return nil
	}

	// Solo l'amministratore.
	ed.do("GET", "/api/v1/avvio", nil, http.StatusForbidden)
	ed.do("POST", "/api/v1/avvio/modelli/banca_assicurazione", nil, http.StatusForbidden)

	// Checklist calcolata: un tenant appena creato ha l'organizzazione già sistemata dalla creazione ma non le persone,
	// le norme, gli organi né la prima richiesta.
	st := adm.do("GET", "/api/v1/avvio", nil, http.StatusOK)
	if st["total"].(float64) != 8 || step(st, "norme")["done"] != false || step(st, "richiesta")["done"] != false || step(st, "moduli")["done"] != false {
		t.Fatalf("passi iniziali: %v", st)
	}
	if step(st, "persone")["done"] != true { // admin + ed
		t.Fatalf("persone: %v", step(st, "persone"))
	}

	// Modelli: il primo è disponibile, gli altri sono in arrivo.
	ms := list(adm.do("GET", "/api/v1/avvio/modelli", nil, http.StatusOK)["items"])
	if len(ms) != 6 || obj(ms[0])["key"] != "banca_assicurazione" || obj(ms[0])["available"] != true || obj(ms[1])["key"] != "persona_sola" || obj(ms[2])["available"] != false {
		t.Fatalf("modelli: %v", ms)
	}
	adm.do("GET", "/api/v1/avvio/modelli/software_house", nil, http.StatusUnprocessableEntity)
	adm.do("POST", "/api/v1/avvio/modelli/software_house", nil, http.StatusUnprocessableEntity)
	adm.do("GET", "/api/v1/avvio/modelli/inesistente", nil, http.StatusNotFound)

	// L'anteprima dice cosa crea e non scrive niente.
	pre := adm.do("GET", "/api/v1/avvio/modelli/banca_assicurazione", nil, http.StatusOK)
	kinds := map[string]map[string]string{}
	for _, it := range list(pre["items"]) {
		o := obj(it)
		k := o["kind"].(string)
		if kinds[k] == nil {
			kinds[k] = map[string]string{}
		}
		kinds[k][o["key"].(string)] = o["state"].(string)
		if k == "catalog" && o["unverified"] != true {
			t.Fatalf("il catalogo %v deve essere «da verificare»", o["key"])
		}
	}
	if kinds["catalog"]["dora"] != "new" || kinds["catalog"]["nis2"] != "new" || kinds["body"]["rischi_ict"] != "new" || len(kinds["process"]) == 0 || len(kinds["type"]) == 0 || len(kinds["module"]) != 6 {
		t.Fatalf("anteprima: %v", kinds)
	}
	if pre["applied"] != false {
		t.Fatal("non ancora applicato")
	}
	ap0 := adm.do("GET", "/api/v1/avvio/applicato", nil, http.StatusOK)
	if len(list(ap0["catalogs"])) != 0 || len(list(ap0["bodies"])) != 0 || len(list(ap0["models"])) != 0 {
		t.Fatalf("l'anteprima ha scritto: %v", ap0)
	}
	if step(adm.do("GET", "/api/v1/avvio", nil, http.StatusOK), "norme")["done"] != false {
		t.Fatal("l'anteprima ha scritto: le norme risultano fatte")
	}

	// Applicazione.
	res := adm.do("POST", "/api/v1/avvio/modelli/banca_assicurazione", nil, http.StatusCreated)
	if res["applied"] != true || res["new"].(float64) < 6 {
		t.Fatalf("applicazione: %v", res)
	}
	ap1 := adm.do("GET", "/api/v1/avvio/applicato", nil, http.StatusOK)
	if len(list(ap1["catalogs"])) != 2 || len(list(ap1["bodies"])) != 4 || len(list(ap1["models"])) != 1 {
		t.Fatalf("applicato: %v", ap1)
	}
	for _, c := range list(ap1["catalogs"]) {
		if obj(c)["unverified"] != true {
			t.Fatalf("catalogo non segnato da verificare: %v", c)
		}
	}
	st = adm.do("GET", "/api/v1/avvio", nil, http.StatusOK)
	for _, k := range []string{"moduli", "organi", "norme", "processo"} {
		if step(st, k)["done"] != true {
			t.Fatalf("passo %s non avanzato: %v", k, st)
		}
	}
	if step(st, "richiesta")["done"] != false || st["model"] != "banca_assicurazione" {
		t.Fatalf("stato dopo il modello: %v", st)
	}
	// Riapplicarlo non duplica niente.
	again := adm.do("POST", "/api/v1/avvio/modelli/banca_assicurazione", nil, http.StatusCreated)
	if again["new"].(float64) != 0 {
		t.Fatalf("la seconda applicazione crea ancora: %v", again)
	}
	if n := len(list(adm.do("GET", "/api/v1/avvio/applicato", nil, http.StatusOK)["bodies"])); n != 4 {
		t.Fatalf("organi duplicati: %d", n)
	}

	// Verifica di un catalogo.
	v := adm.do("POST", "/api/v1/avvio/applicato/cataloghi/dora/verifica", nil, http.StatusOK)
	for _, c := range list(v["catalogs"]) {
		o := obj(c)
		if (o["key"] == "dora") == (o["unverified"] == true) {
			t.Fatalf("verifica: %v", o)
		}
	}
	adm.do("POST", "/api/v1/avvio/applicato/cataloghi/inesistente/verifica", nil, http.StatusNotFound)

	// Chiusura della pagina.
	if adm.do("POST", "/api/v1/avvio/chiudi", nil, http.StatusOK)["closed"] != true {
		t.Fatal("chiusura")
	}
	if adm.do("DELETE", "/api/v1/avvio/chiudi", nil, http.StatusOK)["closed"] != false {
		t.Fatal("riapertura")
	}

	// Ripristino di un processo di partenza modificato.
	var key string
	for _, p := range list(ap1["processes"]) {
		o := obj(p)
		if o["type"] == "business_request" {
			key = o["key"].(string)
			if o["state"] != "original" {
				t.Fatalf("il processo di partenza risulta modificato: %v", o)
			}
		}
	}
	if key == "" {
		t.Fatalf("nessun processo di partenza della richiesta: %v", ap1["processes"])
	}
	pre0 := adm.do("GET", "/api/v1/avvio/applicato/processi/"+key, nil, http.StatusOK)
	if pre0["modified"] != false {
		t.Fatalf("il processo di partenza risulta modificato: %v", pre0["modified"])
	}
	v0 := pre0["version"].(float64)
	adm.do("POST", "/api/v1/avvio/applicato/processi/"+key+"/ripristina", map[string]any{"from_version": v0}, http.StatusUnprocessableEntity) // già originale
	proc := adm.do("GET", "/api/v1/processes/"+key, nil, http.StatusOK)
	def := obj(proc["current"])
	phases := list(def["phases"])
	obj(phases[0])["description"] = "Fase modificata dai test"
	adm.do("PUT", "/api/v1/processes/"+key+"/draft", map[string]any{"definition": def, "notes": "Modifica di prova"}, http.StatusOK)
	adm.do("POST", "/api/v1/processes/"+key+"/publish", map[string]any{"notes": "Modifica di prova"}, http.StatusOK)

	prev := adm.do("GET", "/api/v1/avvio/applicato/processi/"+key, nil, http.StatusOK)
	if prev["modified"] != true || prev["version"].(float64) != v0+1 || prev["current"] == nil || prev["original"] == nil {
		t.Fatalf("anteprima del ripristino: %v", prev)
	}
	if got := obj(list(obj(prev["current"])["phases"])[0])["description"]; got != "Fase modificata dai test" {
		t.Fatalf("la versione corrente non è quella modificata: %v", got)
	}
	for _, p := range list(adm.do("GET", "/api/v1/avvio/applicato", nil, http.StatusOK)["processes"]) {
		if obj(p)["key"] == key && obj(p)["state"] != "modified" {
			t.Fatalf("lo stato dovrebbe essere «modificato»: %v", p)
		}
	}
	ed.do("POST", "/api/v1/avvio/applicato/processi/"+key+"/ripristina", map[string]any{"from_version": v0 + 1}, http.StatusForbidden)
	adm.do("POST", "/api/v1/avvio/applicato/processi/"+key+"/ripristina", map[string]any{"from_version": v0}, http.StatusConflict) // anteprima superata
	done := adm.do("POST", "/api/v1/avvio/applicato/processi/"+key+"/ripristina", map[string]any{"from_version": v0 + 1}, http.StatusOK)
	for _, p := range list(done["processes"]) {
		o := obj(p)
		if o["key"] == key && (o["state"] != "original" || o["version"].(float64) <= v0+1 || o["restored_at"] == nil) {
			t.Fatalf("dopo il ripristino: %v", o)
		}
	}
	nowVersion := func(a map[string]any, key string) float64 {
		for _, p := range list(a["processes"]) {
			if obj(p)["key"] == key {
				return obj(p)["version"].(float64)
			}
		}
		return 0
	}
	adm.do("POST", "/api/v1/avvio/applicato/processi/"+key+"/ripristina", map[string]any{"from_version": nowVersion(done, key)}, http.StatusUnprocessableEntity)
}
