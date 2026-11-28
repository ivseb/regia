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

// Editor visuale dei processi (sdlc.md §10): metadati per l'editor, catalogo per la
// creazione e validazione dal vivo senza salvataggio.
func TestProcessEditorMeta(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("ped-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Editor processi", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name  string
		roles []string
	}{{"editor", []string{"editor"}}, {"proc", []string{"process_admin"}}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.name + "@" + slug + ".test", Name: m.name,
			Password: testPassword, Roles: m.roles}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	// Un modulo futuro registra un tipo di azione: l'editor lo riceve nei metadati.
	eng.DescribeAction(engine.ActionSchema{Kind: "open_review", Name: "Apri una valutazione",
		Fields: []engine.ActionField{{Key: "questionnaire", Name: "Questionario", Kind: "text", Required: true}}})
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	editor := login(t, srv.URL, "editor@"+slug+".test", testPassword)
	conf := login(t, srv.URL, "proc@"+slug+".test", testPassword)

	// Solo chi configura i processi vede i metadati, il catalogo e la validazione.
	editor.do("GET", "/api/v1/processes/demand_standard/meta", nil, http.StatusForbidden)
	editor.do("GET", "/api/v1/process-catalog", nil, http.StatusForbidden)
	editor.do("POST", "/api/v1/processes/demand_standard/check", map[string]any{"definition": map[string]any{}}, http.StatusForbidden)
	conf.do("GET", "/api/v1/processes/inesistente/meta", nil, http.StatusNotFound)

	facts := func(m map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, f := range m["facts"].([]any) {
			fm := f.(map[string]any)
			out[fm["key"].(string)] = fm
		}
		return out
	}
	has := func(list any, field, want string) bool {
		for _, x := range list.([]any) {
			if x.(map[string]any)[field] == want {
				return true
			}
		}
		return false
	}
	values := func(f map[string]any) []string {
		out := []string{}
		if v, ok := f["values"].([]any); ok {
			for _, x := range v {
				out = append(out, x.(string))
			}
		}
		return out
	}

	// Senza elementi del tipo: fatti di base, descritti dai moduli, ruoli.
	m := conf.do("GET", "/api/v1/processes/demand_standard/meta", nil, http.StatusOK)
	if m["type"].(map[string]any)["key"] != "business_request" || m["sample"] != nil {
		t.Fatalf("tipo o esempio: %v %v", m["type"], m["sample"])
	}
	if !has(m["roles"], "key", "sponsor") || !has(m["roles"], "key", "compliance") {
		t.Fatalf("ruoli: %v", m["roles"])
	}
	if !has(m["groups"], "name", "Compliance") || !has(m["groups"], "name", "Demand management") {
		t.Fatalf("gruppi: %v", m["groups"])
	}
	checks := strings.Join(func() []string {
		var s []string
		for _, c := range m["checks"].([]any) {
			s = append(s, c.(string))
		}
		return s
	}(), ",")
	// requisiti_rilascio: gate di rilascio dei requisiti (passo 6); bia_completa ed esito_test: continuità operativa.
	if checks != "bia_completa,esito_test,estimate,requisiti_rilascio,sponsor" {
		t.Fatalf("controlli: %s", checks)
	}
	var policy map[string]any
	for _, p := range m["policies"].([]any) {
		if pm := p.(map[string]any); pm["stage"] == "Approvazione" {
			policy = pm
		}
	}
	if policy == nil || policy["outcomes"].(map[string]any)["approve"] != "Pianificazione" {
		t.Fatalf("politica dell'approvazione: %v", m["policies"])
	}
	f := facts(m)
	for key, src := range map[string]string{"stato": "base", "tipo": "base", "stima_max": "modulo", "impatti_da_stimare": "modulo",
		"ruolo.sponsor": "ruolo"} {
		if f[key] == nil || f[key]["source"] != src {
			t.Fatalf("fatto %s (%s): %v", key, src, f[key])
		}
	}
	if f["stima_max"]["kind"] != "number" || f["ruolo.sponsor"]["kind"] != "presence" || f["obbligatoria"]["kind"] != "bool" {
		t.Fatalf("tipi dei fatti: %v %v %v", f["stima_max"], f["ruolo.sponsor"], f["obbligatoria"])
	}
	if !strings.Contains(strings.Join(values(f["stato"]), ","), "Approvazione") {
		t.Fatalf("fasi suggerite: %v", f["stato"])
	}
	if !strings.Contains(strings.Join(values(f["driver"]), ","), "normativo") {
		t.Fatalf("driver suggeriti: %v", f["driver"])
	}
	actions := m["actions"].([]any)
	kinds := map[string]bool{}
	for _, a := range actions {
		kinds[a.(map[string]any)["kind"].(string)] = true
	}
	if actions[0].(map[string]any)["kind"] != "assign_role" || !kinds["open_review"] {
		t.Fatalf("tipi di azione: %v", actions)
	}
	if one := actions[0].(map[string]any)["one_of"].([]any); len(one) != 2 {
		t.Fatalf("assign_role: gruppo oppure membro: %v", one)
	}

	// Con un elemento del tipo: esempi dai suoi fatti, anche per i fatti non descritti.
	r := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta normativa", "driver": "regulatory",
		"estimate_high_cents": 12_000_000}, http.StatusCreated)
	m = conf.do("GET", "/api/v1/processes/demand_standard/meta", nil, http.StatusOK)
	if m["sample"] != r["key"] {
		t.Fatalf("elemento di esempio: %v, atteso %v", m["sample"], r["key"])
	}
	f = facts(m)
	// I ruoli che nessun elemento del tipo usa sono «avanzati» (fuori dal menu base); quelli in uso no.
	var inUse, outOfContext int
	for k, fact := range f {
		if strings.HasPrefix(k, "ruolo.") {
			if adv, _ := fact["advanced"].(bool); adv {
				outOfContext++
			} else {
				inUse++
			}
		}
	}
	if inUse == 0 || outOfContext == 0 {
		t.Fatalf("ruoli in uso %d, fuori contesto %d: attesi entrambi", inUse, outOfContext)
	}
	if f["stima_max"]["example"] != "120000" || !strings.Contains(f["driver"]["example"].(string), "normativo") {
		t.Fatalf("esempi: %v %v", f["stima_max"], f["driver"])
	}
	if f["ruolo.demand_manager"]["source"] != "ruolo" {
		t.Fatalf("ruolo assegnato dalla regola: %v", f["ruolo.demand_manager"])
	}

	// Catalogo: tipi con il numero di processi e pacchetti di partenza dei moduli.
	c := conf.do("GET", "/api/v1/process-catalog", nil, http.StatusOK)
	var br map[string]any
	for _, ty := range c["types"].([]any) {
		if tm := ty.(map[string]any); tm["key"] == "business_request" {
			br = tm
		}
	}
	if br == nil || br["processes"].(float64) < 1 {
		t.Fatalf("tipi: %v", c["types"])
	}
	starters := map[string]map[string]any{}
	for _, s := range c["starters"].([]any) {
		sm := s.(map[string]any)
		starters[sm["key"].(string)] = sm
	}
	for _, k := range []string{"demand_standard", "impact_standard", "application_standard", "logical_asset_standard"} {
		if starters[k] == nil || starters[k]["format"] != engine.BundleFormat {
			t.Fatalf("pacchetto %s: %v", k, starters[k])
		}
	}
	if len(starters["demand_standard"]["definition"].(map[string]any)["phases"].([]any)) != 12 {
		t.Fatal("il pacchetto del demand deve portare la definizione")
	}

	// Validazione dal vivo: gli stessi problemi della pubblicazione, senza salvare.
	def := starters["demand_standard"]["definition"].(map[string]any)
	trs := def["transitions"].([]any)
	trs[0].(map[string]any)["gates"] = []any{"inesistente"}
	trs[1].(map[string]any)["roles"] = []any{"ruolo_che_non_esiste"}
	def["phases"] = append(def["phases"].([]any), map[string]any{"name": "Isolata", "category": "in_progress"})
	ps := conf.do("POST", "/api/v1/processes/demand_standard/check", map[string]any{"definition": def}, http.StatusOK)["problems"].([]any)
	want := map[string]bool{"transitions[0].gates": false, "transitions[1].roles": false, "phases": false}
	for _, p := range ps {
		pm := p.(map[string]any)
		if _, ok := want[pm["path"].(string)]; ok {
			want[pm["path"].(string)] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Fatalf("manca il problema su %s: %v", k, ps)
		}
	}
	ps = conf.do("POST", "/api/v1/processes/demand_standard/check", map[string]any{"definition": map[string]any{"fasi": []any{}}}, http.StatusOK)["problems"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["path"] != "definition" {
		t.Fatalf("campo sconosciuto: %v", ps)
	}
	// Niente è stato salvato: il processo non ha bozze.
	p := conf.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	if p["draft"] != nil {
		t.Fatal("la validazione non deve salvare la bozza")
	}

	// Priorità: un processo nuovo senza priorità non pareggia con lo standard (100). Senza condizione
	// vale per tutto e va dopo; con una condizione passa davanti. A parità esplicita l'editor avvisa.
	if sp := conf.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK); sp["priority"].(float64) != 100 || sp["warnings"] != nil {
		t.Fatalf("lo standard parte da solo a 100: %v %v", sp["priority"], sp["warnings"])
	}
	catchAll := conf.do("POST", "/api/v1/processes", map[string]any{"key": "bug_critico", "name": "Bug critico", "type": "business_request"}, http.StatusCreated)
	if catchAll["priority"].(float64) <= 100 || catchAll["warnings"] != nil {
		t.Fatalf("senza condizione il processo nuovo va dopo lo standard: %v %v", catchAll["priority"], catchAll["warnings"])
	}
	special := conf.do("POST", "/api/v1/processes", map[string]any{"key": "urgente", "name": "Urgente", "type": "business_request", "applies_when": "stima_max: >1000"}, http.StatusCreated)
	if special["priority"].(float64) >= 100 {
		t.Fatalf("con una condizione il processo nuovo passa davanti allo standard: %v", special["priority"])
	}
	tie := conf.do("POST", "/api/v1/processes", map[string]any{"key": "pari", "name": "Pari", "type": "business_request", "priority": 100}, http.StatusCreated)
	ws, _ := tie["warnings"].([]any)
	if len(ws) == 0 || !strings.Contains(ws[0].(string), "Stessa priorità (100)") {
		t.Fatalf("a parità di priorità serve un avviso: %v", tie["warnings"])
	}
	if back := conf.do("PATCH", "/api/v1/processes/pari", map[string]any{"priority": 120}, http.StatusOK); back["warnings"] != nil {
		// 120 non è di nessun altro (il catch-all sta a 110).
		t.Fatalf("dopo aver cambiato il numero l'avviso sparisce: %v", back["warnings"])
	}
	// Togliere la condizione senza dire la priorità non vale: senza condizione decide solo il numero.
	rej := conf.do("PATCH", "/api/v1/processes/urgente", map[string]any{"applies_when": ""}, http.StatusUnprocessableEntity)
	if rej["code"] != "validation.process.priority_required" {
		t.Fatalf("serve il code della priorità obbligatoria: %v", rej)
	}
	conf.do("PATCH", "/api/v1/processes/urgente", map[string]any{"applies_when": "", "priority": 130}, http.StatusOK)
}
