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

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/devtools/fakeai"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Questionari precompilati dall'AI (funzione «questionari»): con l'AI spenta niente proposte, ma
// le domande da guardare con attenzione si vedono; con l'AI accesa le proposte vengono dalle
// risposte confermate della stessa applicazione (confidenza alta) o di un'applicazione simile (al
// massimo media), un valore non valido o una fonte inventata diventano «da compilare a mano»; le
// proposte le vede solo chi le ha chieste; accettare, cambiare e scartare lasciano l'origine nella
// storia e nel registro; una risposta a mano fa decadere la proposta.
func TestAIQuestionari(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("aiq-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Questionari AI", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision}})
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]tenant.MemberInput{
		"req":     {MemberType: "requester", Roles: []string{"requester"}},
		"dm":      {Roles: []string{"editor"}, Groups: []string{"Demand management"}},
		"arch":    {Roles: []string{"assessor"}, Groups: []string{"Architetture"}},
		"curioso": {Roles: []string{"viewer"}},
	} {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng),
		Reviews: reviews.Register(eng),
		Log:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web:     os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, req, man, arch, curioso := u("admin"), u("req"), u("dm"), u("arch"), u("curioso")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	count := func(q string, args ...any) (n int) {
		t.Helper()
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	move := func(u *user, id, to string, status int) map[string]any {
		return u.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}

	// --- Applicazioni: FE e FE2 sono simili (dati personali, criticità alta), REP no.
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;hosting;team;asset\n" +
		"FE;Frontend area clienti;alta;no;personali;cloud_ue;Web;Area clienti\n" +
		"FE2;Frontend partner;alta;no;personali;cloud_ue;Web;Area partner\n" +
		"REP;Reportistica;bassa;no;aggregati;on_premise;Dati;Area clienti\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)["id"].(string)
	}
	// Una richiesta con un impatto per applicazione: la design review di ogni impatto.
	designOf := func(title string, codes ...string) map[string]string {
		br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": title}, http.StatusCreated)
		key, id := br["key"].(string), br["id"].(string)
		imps := map[string]string{}
		for _, c := range codes {
			imps[man.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{"application_id": apps[c], "estimate_effort_days": 5}, http.StatusCreated)["key"].(string)] = c
		}
		move(req, id, "Inviata", http.StatusOK)
		move(man, id, "Triage", http.StatusOK)
		move(man, id, "Valutazione", http.StatusOK)
		out := map[string]string{}
		for _, it := range list(man.do("GET", "/api/v1/elements/"+key+"/reviews", nil, http.StatusOK)["items"]) {
			x := obj(it)
			if x["type_key"] == "design_review" {
				out[imps[x["impact_key"].(string)]] = x["key"].(string)
			}
		}
		return out
	}
	save := func(u *user, key string, answers map[string]any, status int) map[string]any {
		return u.do("PUT", "/api/v1/reviews/"+key+"/answers", map[string]any{"answers": answers}, status)
	}

	// --- Una design review di FE già conclusa: le sue risposte sono confermate.
	first := designOf("Area clienti, prima fase", "FE")["FE"]
	v := save(man, first, map[string]any{"tipo_intervento": "modifica", "descrizione": "Nuovo portale clienti", "hosting": "cloud_ue",
		"nuove_tecnologie": false, "nuove_api": true, "standard_api": true, "volumi": 5000}, http.StatusOK)
	if m := list(obj(v["evaluation"])["missing"]); len(m) != 0 {
		t.Fatalf("domande mancanti nella prima valutazione: %v", m)
	}
	fid := obj(v["element"])["id"].(string)
	move(man, fid, "Inviata al team", http.StatusOK)
	move(arch, fid, "In esame", http.StatusOK)
	arch.do("POST", "/api/v1/reviews/"+first+"/decision", map[string]any{"outcome": "favorevole"}, http.StatusOK)

	second := designOf("Area clienti, seconda fase", "FE", "FE2")
	feKey, fe2Key := second["FE"], second["FE2"]
	if feKey == "" || fe2Key == "" {
		t.Fatalf("design review della seconda richiesta: %v", second)
	}

	// --- AI spenta: niente funzione, nessuna proposta, ma le domande da guardare si vedono.
	v = man.do("GET", "/api/v1/reviews/"+feKey, nil, http.StatusOK)
	hl := obj(v["highlights"])
	if !slices.Contains(toStrings(hl["hosting"]), "rules") || !slices.Contains(toStrings(hl["nuove_api"]), "rules") || hl["descrizione"] != nil {
		t.Fatalf("domande evidenziate: %v", hl)
	}
	if len(list(v["ai_proposals"])) != 0 {
		t.Fatalf("proposte con l'AI spenta: %v", v["ai_proposals"])
	}
	man.do("POST", "/api/v1/reviews/"+feKey+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusConflict)
	fake := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	setModel := func(model string) {
		admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "mask_personal_data": true,
			"deep": map[string]any{"provider_id": fake["id"], "model": model}}, http.StatusOK)
	}
	setModel(fakeai.ModelDeep)
	if st := man.do("GET", "/api/v1/ai/status", nil, http.StatusOK); obj(st["features"])["questionari"] != true {
		t.Fatalf("funzione questionari non pronta: %v", st["features"])
	}
	if !strings.Contains(jsonString(admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK)["features"]), `{"active":true,"key":"questionari","needs_tools":false,"tier":"approfondito"}`) {
		t.Fatal("funzione questionari non attiva nelle impostazioni")
	}

	// --- Permessi e richieste non valide.
	curioso.do("POST", "/api/v1/reviews/"+feKey+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusForbidden)
	arch.do("POST", "/api/v1/reviews/"+feKey+"/ai-proposals", map[string]any{"section": "inesistente"}, http.StatusUnprocessableEntity)

	// --- Stessa applicazione: le risposte confermate della prima valutazione, confidenza alta.
	before := len(fakeai.Requests(fakeai.ModelDeep))
	out := man.do("POST", "/api/v1/reviews/"+feKey+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusOK)
	res, view := obj(out["result"]), obj(out["view"])
	props := map[string]map[string]any{}
	for _, p := range list(view["ai_proposals"]) {
		props[obj(p)["question"].(string)] = obj(p)
	}
	h := props["hosting"]
	if h == nil || h["value"] != "cloud_ue" || h["confidence"] != "alta" {
		t.Fatalf("proposta per hosting: %s", jsonString(view["ai_proposals"]))
	}
	src := obj(list(h["sources"])[0])
	if src["kind"] != "answer" || src["key"] != first || src["question"] != "hosting" {
		t.Fatalf("fonte della proposta: %v", src)
	}
	if props["descrizione"]["value"] != "Nuovo portale clienti" || props["nuove_tecnologie"]["value"] != false || res["proposed"].(float64) < 3 {
		t.Fatalf("proposte della sezione: %s | %v", jsonString(view["ai_proposals"]), res)
	}
	reqs := fakeai.Requests(fakeai.ModelDeep)
	if len(reqs) != before+1 || !strings.Contains(reqs[len(reqs)-1].Messages[0].Text, `<dati origine="fonti">`) ||
		!strings.Contains(reqs[len(reqs)-1].Messages[0].Text, "fatto del grafo") || len(reqs[len(reqs)-1].Tools) != 0 {
		t.Fatalf("richiesta al modello: %.800s", reqs[len(reqs)-1].Messages[0].Text)
	}
	if n := count(`select count(*) from ai_calls where feature = 'questionari' and outcome = 'ok'`); n != 1 {
		t.Fatalf("chiamate nel registro: %d", n)
	}
	// Seconda sezione: le API, dalla stessa valutazione conclusa (non le domande ancora nascoste né gli allegati).
	out = man.do("POST", "/api/v1/reviews/"+feKey+"/ai-proposals", map[string]any{"section": "integrazioni"}, http.StatusOK)
	for _, p := range list(obj(out["view"])["ai_proposals"]) {
		props[obj(p)["question"].(string)] = obj(p)
	}
	if props["nuove_api"]["value"] != true || props["standard_api"] != nil || props["diagramma"] != nil {
		t.Fatalf("proposte delle integrazioni (niente allegati): %s", jsonString(obj(out["view"])["ai_proposals"]))
	}
	// Nulla si salva da sé, e le proposte le vede solo chi le ha chieste.
	v = arch.do("GET", "/api/v1/reviews/"+feKey, nil, http.StatusOK)
	if obj(v["answers"])["hosting"] != nil || len(list(v["ai_proposals"])) != 0 {
		t.Fatalf("risposta salvata da sola o proposte visibili ad altri: %v %v", obj(v["answers"])["hosting"], v["ai_proposals"])
	}

	// --- Applicazione simile: fonte «similar», confidenza al massimo media.
	out = man.do("POST", "/api/v1/reviews/"+fe2Key+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusOK)
	for _, p := range list(obj(out["view"])["ai_proposals"]) {
		x := obj(p)
		if x["value"] == nil {
			continue
		}
		if x["confidence"] == "alta" || obj(list(x["sources"])[0])["kind"] != "similar" {
			t.Fatalf("proposta da un'applicazione simile: %v", x)
		}
	}
	if len(list(obj(out["view"])["ai_proposals"])) == 0 {
		t.Fatal("nessuna proposta dall'applicazione simile")
	}

	// --- Un valore non valido e una fonte inventata: «nessuna fonte, da compilare a mano».
	fakeai.Program("questionari", fakeai.Step{Text: `{"risposte":[{"domanda":"hosting","valore":"marte","fonti":["f1"],"confidenza":"alta","motivo":"x"},` +
		`{"domanda":"descrizione","valore":"Inventato","fonti":["f999"],"confidenza":"alta","motivo":"y"}]}`})
	setModel("scenario:questionari")
	out = arch.do("POST", "/api/v1/reviews/"+fe2Key+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusOK)
	for _, p := range list(obj(out["view"])["ai_proposals"]) {
		if x := obj(p); x["value"] != nil || x["confidence"] != "nessuna" || len(list(x["sources"])) != 0 {
			t.Fatalf("proposta senza fonte valida: %v", x)
		}
	}
	if r := obj(out["result"]); r["proposed"] != 0.0 || r["no_source"].(float64) < 2 {
		t.Fatalf("esito senza fonti: %v", r)
	}
	setModel(fakeai.ModelDeep)

	// --- Decisioni: accetta, cambia e accetta, scarta.
	decide := func(u *user, key string, ds []map[string]any, status int) map[string]any {
		return u.do("POST", "/api/v1/reviews/"+key+"/ai-proposals/decide", map[string]any{"decisions": ds}, status)
	}
	decide(arch, feKey, []map[string]any{{"id": props["hosting"]["id"], "accept": true}}, http.StatusUnprocessableEntity) // non sua
	out = decide(man, feKey, []map[string]any{
		{"id": props["hosting"]["id"], "accept": true},
		{"id": props["descrizione"]["id"], "accept": true, "value": "Portale clienti, seconda fase"},
		{"id": props["nuove_tecnologie"]["id"], "accept": false},
	}, http.StatusOK)
	ans := obj(obj(out["view"])["answers"])
	if a := obj(ans["hosting"]); a["source"] != "ai" || a["value"] != "cloud_ue" || obj(a["ai"])["edited"] == true || a["by"] != "dm" {
		t.Fatalf("risposta accettata: %v", a)
	}
	if a := obj(ans["descrizione"]); a["source"] != "ai" || a["value"] != "Portale clienti, seconda fase" || obj(a["ai"])["edited"] != true {
		t.Fatalf("risposta cambiata e accettata: %v", a)
	}
	if ans["nuove_tecnologie"] != nil {
		t.Fatalf("risposta scartata salvata: %v", ans["nuove_tecnologie"])
	}
	left := map[string]bool{}
	for _, p := range list(obj(out["view"])["ai_proposals"]) {
		left[obj(p)["question"].(string)] = true
	}
	if left["hosting"] || left["descrizione"] || left["nuove_tecnologie"] || !left["nuove_api"] {
		t.Fatalf("proposte rimaste: %v", left)
	}
	decide(man, feKey, []map[string]any{{"id": props["hosting"]["id"], "accept": true}}, http.StatusUnprocessableEntity) // già decisa
	hist := list(man.do("GET", "/api/v1/reviews/"+feKey+"/history?question=descrizione", nil, http.StatusOK)["items"])
	if len(hist) != 1 || obj(hist[0])["source"] != "ai" || obj(hist[0])["ai_edited"] != true || obj(hist[0])["by"] != "dm" {
		t.Fatalf("storia della risposta accettata: %v", hist)
	}
	if n := count(`select count(*) from ai_calls where feature = 'questionari' and model = 'proposta:risposta_questionario' and outcome = 'accepted_by_user'`); n != 2 {
		t.Fatalf("accettazioni nel registro: %d", n)
	}
	if n := count(`select count(*) from ai_calls where feature = 'questionari' and model = 'proposta:risposta_questionario' and outcome = 'rejected_by_user'`); n != 1 {
		t.Fatalf("scarti nel registro: %d", n)
	}
	// Una risposta a mano fa decadere la proposta della stessa domanda.
	v = save(man, feKey, map[string]any{"nuove_api": false}, http.StatusOK)
	for _, p := range list(v["ai_proposals"]) {
		if obj(p)["question"] == "nuove_api" {
			t.Fatalf("proposta rimasta dopo la risposta a mano: %v", p)
		}
	}
	decide(man, feKey, []map[string]any{{"id": props["nuove_api"]["id"], "accept": true}}, http.StatusUnprocessableEntity)
	if n := count(`select count(*) from questionnaire_ai_proposals where status = 'superseded' and question_key = 'nuove_api'`); n < 1 {
		t.Fatalf("proposta decaduta: %d", n)
	}

	// --- Con l'AI spenta la funzione non c'è più.
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": false}, http.StatusOK)
	if st := man.do("GET", "/api/v1/ai/status", nil, http.StatusOK); obj(st["features"])["questionari"] == true {
		t.Fatalf("funzione con l'AI spenta: %v", st["features"])
	}
	man.do("POST", "/api/v1/reviews/"+fe2Key+"/ai-proposals", map[string]any{"section": "soluzione"}, http.StatusConflict)
}
