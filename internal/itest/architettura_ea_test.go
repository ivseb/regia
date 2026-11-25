package itest

import (
	"context"
	"encoding/json"
	"fmt"
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

type eaEnv struct {
	t     *testing.T
	url   string
	slug  string
	admin *user
	arch  *user
	view  *user
	req   *user
	dm    *user
	apps  map[string]map[string]any // per codice
}

// newEAEnv crea un tenant con demand e architettura, le persone di prova e il catalogo
// di sei applicazioni (A-F) con due asset.
func newEAEnv(t *testing.T, srvURL, prefix string) *eaEnv {
	t.Helper()
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "EA " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]tenant.MemberInput{
		"arch": {Roles: []string{"architect"}},
		"view": {Roles: []string{"viewer"}},
		"dm":   {Roles: []string{"editor"}, Groups: []string{"Demand management"}},
		"req":  {MemberType: "requester", Roles: []string{"requester"}},
	} {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	e := &eaEnv{t: t, url: srvURL, slug: slug, apps: map[string]map[string]any{}}
	e.admin = login(t, srvURL, "admin@"+slug+".test", testPassword)
	e.arch = login(t, srvURL, "arch@"+slug+".test", testPassword)
	e.view = login(t, srvURL, "view@"+slug+".test", testPassword)
	e.req = login(t, srvURL, "req@"+slug+".test", testPassword)
	e.dm = login(t, srvURL, "dm@"+slug+".test", testPassword)
	csv := "codice;nome;criticita;funzione_critica;team;asset\n" +
		"A;Home banking;critica;sì;Web;Canali digitali\n" +
		"B;API conti;alta;sì;Integrazioni;Canali digitali\n" +
		"C;Core conti;critica;sì;Core;\n" +
		"D;Anagrafe clienti;alta;no;Core;\n" +
		"E;Motore antifrode;alta;sì;Sicurezza;\n" +
		"F;Data warehouse;media;no;Dati;Reportistica\n"
	if rep := e.arch.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK); rep["created"].(float64) != 6 {
		t.Fatalf("catalogo: %v", rep)
	}
	for _, a := range e.arch.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		e.apps[m["code"].(string)] = m
	}
	return e
}

func (e *eaEnv) id(code string) string { return e.apps[code]["id"].(string) }

func eaItems(m map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, it := range m["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func eaList(v any) []map[string]any {
	out := []map[string]any{}
	if v == nil {
		return out
	}
	for _, it := range v.([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

// Architettura enterprise (piano, passo 20): capability e gerarchia, dipendenze con la
// validazione dei tipi, analisi d'impatto a più livelli con cicli, diagrammi, fatti per le
// condizioni, razionalizzazione, import ed export, isolamento tra tenant.
func TestArchitetturaEnterprise(t *testing.T) {
	needDB(t)
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: needDB(t), Auth: auth.NewService(needDB(t), time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	e := newEAEnv(t, srv.URL, "ea")
	arch, view, req := e.arch, e.view, e.req
	const base = "/api/v1/architecture"

	// --- Capability e gerarchia -------------------------------------------------
	view.do("POST", base+"/capabilities", map[string]any{"name": "Abusiva"}, http.StatusForbidden)
	pay := arch.do("POST", base+"/capabilities", map[string]any{"name": "Pagamenti", "code": "CAP-PAY", "criticality": "critica"}, http.StatusCreated)
	if pay["level"].(float64) != 1 || pay["criticality"] != "critical" || pay["state"] != "Attiva" {
		t.Fatalf("capability radice: %v", pay)
	}
	bon := arch.do("POST", base+"/capabilities", map[string]any{"name": "Bonifici", "parent_id": pay["id"]}, http.StatusCreated)
	ist := arch.do("POST", base+"/capabilities", map[string]any{"name": "Bonifici istantanei", "code": "CAP-INST", "parent_id": bon["id"]}, http.StatusCreated)
	if ist["level"].(float64) != 3 || strings.Join(toStrings(ist["path"]), " / ") != "Pagamenti / Bonifici / Bonifici istantanei" {
		t.Fatalf("capability di terzo livello: %v", ist)
	}
	arch.do("POST", base+"/capabilities", map[string]any{"name": "Doppione", "code": "cap-pay"}, http.StatusUnprocessableEntity)
	cli := arch.do("POST", base+"/capabilities", map[string]any{"name": "Gestione clienti"}, http.StatusCreated)
	// Nessun ciclo: Pagamenti non può finire dentro una sua discendente, né dentro sé stessa.
	arch.do("PATCH", base+"/capabilities/"+pay["id"].(string), map[string]any{"parent_id": ist["id"]}, http.StatusUnprocessableEntity)
	arch.do("PATCH", base+"/capabilities/CAP-PAY", map[string]any{"parent_id": pay["id"]}, http.StatusUnprocessableEntity)
	arch.do("PATCH", base+"/capabilities/"+bon["id"].(string), map[string]any{"parent_id": e.id("A")}, http.StatusUnprocessableEntity)
	// Spostamento lecito e ritorno.
	if m := arch.do("PATCH", base+"/capabilities/"+bon["key"].(string), map[string]any{"parent_id": cli["id"]}, http.StatusOK); m["level"].(float64) != 2 {
		t.Fatalf("spostamento: %v", m)
	}
	arch.do("PATCH", base+"/capabilities/"+bon["id"].(string), map[string]any{"parent_id": pay["id"], "criticality": "alta"}, http.StatusOK)

	// Collegamenti: applicazione "supporta", asset "realizza"; altri tipi no.
	arch.do("POST", base+"/capabilities/"+bon["id"].(string)+"/links", map[string]any{"element_id": e.id("A")}, http.StatusNoContent)
	arch.do("POST", base+"/capabilities/"+bon["id"].(string)+"/links", map[string]any{"element_id": "B"}, http.StatusNoContent) // per codice
	arch.do("POST", base+"/capabilities/"+bon["id"].(string)+"/links", map[string]any{"element_id": "B"}, http.StatusNoContent) // idempotente
	arch.do("POST", base+"/capabilities/"+cli["id"].(string)+"/links", map[string]any{"element_id": "Anagrafe clienti"}, http.StatusNoContent)
	arch.do("POST", base+"/capabilities/"+pay["id"].(string)+"/links", map[string]any{"element_id": "Canali digitali"}, http.StatusNoContent)
	arch.do("POST", base+"/capabilities/"+pay["id"].(string)+"/links", map[string]any{"element_id": ist["id"]}, http.StatusUnprocessableEntity)
	view.do("POST", base+"/capabilities/"+pay["id"].(string)+"/links", map[string]any{"element_id": "C"}, http.StatusForbidden)
	caps := eaItems(view.do("GET", base+"/capabilities", nil, http.StatusOK))
	if len(caps) != 4 || caps[0]["name"] != "Pagamenti" || caps[1]["name"] != "Bonifici" || caps[2]["name"] != "Bonifici istantanei" || caps[3]["name"] != "Gestione clienti" {
		t.Fatalf("ordine dell'albero: %v", eaNames(caps))
	}
	if n := len(eaList(caps[1]["applications"])); n != 2 || len(eaList(caps[0]["assets"])) != 1 {
		t.Fatalf("collegamenti delle capability: %v", caps)
	}
	req.do("GET", base+"/capabilities", nil, http.StatusForbidden) // il richiedente non vede l'architettura

	// --- Dipendenze e validazione dei tipi ------------------------------------
	types := eaItems(view.do("GET", base+"/relation-types", nil, http.StatusOK))
	keys := map[string]map[string]any{}
	for _, rt := range types {
		keys[rt["key"].(string)] = rt
	}
	for _, k := range []string{"calls_api", "reads_data_from", "sends_events_to", "uses_app", "supports", "realizes", "depends_on", "composed_of"} {
		if keys[k] == nil {
			t.Fatalf("manca il tipo %s: %v", k, keys)
		}
	}
	if st := toStrings(keys["calls_api"]["source_types"]); len(st) != 1 || st[0] != "application" {
		t.Fatalf("tipi ammessi di calls_api: %v", keys["calls_api"])
	}
	rel := func(typ, src, tgt string, attrs map[string]any, status int) map[string]any {
		return arch.do("POST", base+"/relations", map[string]any{"type": typ, "source": src, "target": tgt, "attrs": attrs}, status)
	}
	ab := rel("calls_api", "A", "B", map[string]any{"protocol": "REST", "criticality": "alta", "data": "saldi e movimenti", "classification": "personali"}, http.StatusCreated)
	if ab["attrs"].(map[string]any)["criticality"] != "high" || ab["source_name"] != "Home banking" || ab["type_name"] != "chiama le API di" {
		t.Fatalf("dipendenza A→B: %v", ab)
	}
	// La storia non mostra chiavi interne («calls_api.inverse»): ha il nome della relazione nel verso
	// della voce e l'altro elemento, se chi legge lo può vedere.
	for _, x := range []struct{ on, label, other string }{{"A", "chiama le API di", "API conti"}, {"B", "", "Home banking"}} {
		found := false
		for _, it := range eaItems(arch.do("GET", "/api/v1/elements/"+e.id(x.on)+"/activity", nil, http.StatusOK)) {
			if it["kind"] == "relation" && strings.Contains(fmt.Sprint(it["other"]), x.other) {
				found = true
				if x.label != "" && it["relation_label"] != x.label {
					t.Fatalf("nome della relazione nella storia di %s: %v", x.on, it)
				}
				if it["relation_label"] == nil || it["relation_label"] == "" || strings.Contains(fmt.Sprint(it["relation_label"]), "_") {
					t.Fatalf("la storia di %s mostra una chiave interna: %v", x.on, it)
				}
			}
		}
		if !found {
			t.Fatalf("la storia di %s non nomina %s", x.on, x.other)
		}
	}
	rel("calls_api", "A", "B", nil, http.StatusUnprocessableEntity)                                     // già presente
	rel("calls_api", "A", "A", nil, http.StatusUnprocessableEntity)                                     // su sé stessa
	rel("calls_api", "A", "CAP-PAY", nil, http.StatusUnprocessableEntity)                               // destinazione non ammessa
	rel("calls_api", "Canali digitali", "B", nil, http.StatusUnprocessableEntity)                       // origine non ammessa
	rel("calls_api", "A", "C", map[string]any{"colore": "blu"}, http.StatusUnprocessableEntity)         // attributo sconosciuto
	rel("calls_api", "A", "C", map[string]any{"criticality": "enorme"}, http.StatusUnprocessableEntity) // valore non ammesso
	rel("inesistente", "A", "C", nil, http.StatusUnprocessableEntity)
	view.do("POST", base+"/relations", map[string]any{"type": "calls_api", "source": "A", "target": "C"}, http.StatusForbidden)
	rel("calls_api", "B", "C", map[string]any{"protocol": "gRPC"}, http.StatusCreated)
	rel("reads_data_from", "C", "D", nil, http.StatusCreated)
	rel("calls_api", "D", "B", nil, http.StatusCreated) // ciclo B → C → D → B
	rel("sends_events_to", "E", "A", map[string]any{"protocol": "Kafka"}, http.StatusCreated)
	// "dipende da" è generico (per esempio verso un fornitore): nessun vincolo di tipo.
	rel("depends_on", "F", "D", nil, http.StatusCreated)
	// Tipo configurato dal tenant, con il suo schema degli attributi.
	view.do("POST", base+"/relation-types", map[string]any{"key": "replica_dati"}, http.StatusForbidden)
	custom := arch.do("POST", base+"/relation-types", map[string]any{"key": "replica_dati", "outward_name": "replica i dati su", "inward_name": "riceve le repliche di",
		"source_types": []string{"application"}, "target_types": []string{"application"}, "impact_direction": "reverse",
		"attrs_schema": map[string]any{"fields": []any{map[string]any{"key": "frequenza", "name": "Frequenza", "kind": "enum", "values": []string{"continua", "notturna"}}}}}, http.StatusCreated)
	if custom["system"] != false || custom["category"] != "dependency" {
		t.Fatalf("tipo personalizzato: %v", custom)
	}
	arch.do("POST", base+"/relation-types", map[string]any{"key": "replica_dati", "outward_name": "x", "inward_name": "y"}, http.StatusUnprocessableEntity)
	rel("replica i dati su", "C", "F", map[string]any{"frequenza": "notturna"}, http.StatusCreated) // per nome diretto
	// I tipi ammessi non possono escludere relazioni già presenti.
	arch.do("PATCH", base+"/relation-types/replica_dati", map[string]any{"target_types": []string{"capability"}}, http.StatusUnprocessableEntity)
	arch.do("PATCH", base+"/relation-types/replica_dati", map[string]any{"outward_name": "replica su"}, http.StatusOK)
	// Attributi modificabili e relazione cancellabile.
	if m := arch.do("PATCH", base+"/relations/"+ab["id"].(string), map[string]any{"attrs": map[string]any{"protocol": "SOAP"}}, http.StatusOK); m["attrs"].(map[string]any)["protocol"] != "SOAP" {
		t.Fatalf("attributi aggiornati: %v", m)
	}
	tmp := rel("uses_app", "F", "E", nil, http.StatusCreated)
	arch.do("DELETE", base+"/relations/"+tmp["id"].(string), nil, http.StatusNoContent)
	if n := len(eaItems(view.do("GET", base+"/relations?category=dependency", nil, http.StatusOK))); n != 7 {
		t.Fatalf("dipendenze: %d", n)
	}

	// --- Grafo e analisi d'impatto ----------------------------------------------
	g := view.do("GET", base+"/graph", nil, http.StatusOK)
	nodes, edges := eaList(g["nodes"]), eaList(g["edges"])
	if len(nodes) != 6+2+4 { // applicazioni, asset, capability
		t.Fatalf("nodi del grafo: %d", len(nodes))
	}
	hier := 0
	for _, ed := range edges {
		if ed["type"] == "_parent" {
			hier++
		}
	}
	if hier != 2 {
		t.Fatalf("archi della gerarchia: %d", hier)
	}
	impact := func(ref, dir string, depth int) map[string]any {
		return view.do("GET", base+"/impact/"+ref+"?direction="+dir+"&depth="+itoa(depth), nil, http.StatusOK)
	}
	levels := func(m map[string]any) map[string]float64 {
		out := map[string]float64{}
		for _, it := range eaList(m["items"]) {
			out[it["name"].(string)] = it["level"].(float64)
		}
		return out
	}
	// Chi dipende da D: C (legge dati da D), B (chiama C), A (chiama B), F (dipende da D),
	// le capability supportate da A e B, e su fino a Pagamenti. Il ciclo B→C→D→B non
	// fa girare a vuoto la visita.
	dep := impact("D", "dependents", 0)
	lv := levels(dep)
	if lv["Core conti"] != 1 || lv["Data warehouse"] != 1 || lv["API conti"] != 2 || lv["Home banking"] != 3 || lv["Bonifici"] != 3 || lv["Pagamenti"] != 4 {
		t.Fatalf("dipendenti di D: %v", lv)
	}
	if _, ok := lv["Anagrafe clienti"]; ok {
		t.Fatalf("la radice compare tra i dipendenti: %v", lv)
	}
	if dep["cycles"].(float64) < 1 {
		t.Fatalf("ciclo non segnalato: %v", dep["cycles"])
	}
	if lv := levels(impact("D", "dependents", 2)); len(lv) != 5 || lv["Home banking"] != 0 || lv["Reportistica"] != 2 {
		t.Fatalf("dipendenti di D fino al secondo livello: %v", lv)
	}
	// Da cosa dipende A: B, E (gli eventi di E arrivano ad A), poi C, D.
	if lv := levels(impact(e.id("A"), "dependencies", 0)); lv["API conti"] != 1 || lv["Motore antifrode"] != 1 || lv["Core conti"] != 2 || lv["Anagrafe clienti"] != 3 {
		t.Fatalf("dipendenze di A: %v", lv)
	}
	for _, it := range eaList(impact("D", "dependents", 0)["items"]) {
		if it["name"] == "Home banking" && strings.Join(toStrings(it["path"]), ">") != e.apps["D"]["key"].(string)+">"+e.apps["C"]["key"].(string)+">"+e.apps["B"]["key"].(string)+">"+e.apps["A"]["key"].(string) {
			t.Fatalf("percorso: %v", it["path"])
		}
	}
	st, hdr, raw := view.get(base + "/impact/D?direction=dependents&format=csv")
	if st != http.StatusOK || !strings.Contains(hdr.Get("Content-Disposition"), "impatto-") || !strings.Contains(string(raw), "livello;chiave;nome") || !strings.Contains(string(raw), "Home banking") {
		t.Fatalf("export dell'impatto: %d %s", st, raw)
	}
	view.do("GET", base+"/impact/D?direction=sideways", nil, http.StatusUnprocessableEntity)

	// --- Ciclo di vita, tecnologie e fatti ---------------------------------------
	arch.do("PUT", base+"/applications/A/time", map[string]any{"time_rating": "investire", "note": "canale strategico"}, http.StatusNoContent)
	arch.do("PUT", base+"/applications/B/time", map[string]any{"time_rating": "migrate"}, http.StatusNoContent)
	arch.do("PUT", base+"/applications/B/time", map[string]any{"time_rating": "boh"}, http.StatusUnprocessableEntity)
	view.do("PUT", base+"/applications/A/time", map[string]any{"time_rating": "tolerate"}, http.StatusForbidden)
	soon := time.Now().AddDate(0, 0, 30).Format(time.DateOnly)
	java := arch.do("POST", base+"/technologies", map[string]any{"name": "Java", "version": "8", "vendor": "Oracle", "end_of_support": soon}, http.StatusCreated)
	pg := arch.do("POST", base+"/technologies", map[string]any{"name": "PostgreSQL", "version": "17", "end_of_support": "2029-11-08"}, http.StatusCreated)
	arch.do("POST", base+"/technologies", map[string]any{"name": "java", "version": "8"}, http.StatusUnprocessableEntity)
	arch.do("POST", base+"/technologies", map[string]any{"name": "Cobol", "end_of_life": "31-12-2026"}, http.StatusUnprocessableEntity)
	arch.do("PUT", base+"/applications/A/technologies", map[string]any{"technology_ids": []string{java["id"].(string), pg["id"].(string)}}, http.StatusNoContent)
	if d := java["days_left"].(float64); d < 29 || d > 31 {
		t.Fatalf("giorni alla fine supporto: %v", java)
	}
	check := func(cond, ref string) bool {
		m := e.admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": cond, "element": ref}, http.StatusOK)
		return m["result"] == true
	}
	keyOf := func(code string) string { return e.apps[code]["key"].(string) }
	// D: ne dipendono C, B, A e F (applicazioni), direttamente C e F.
	if !check(`dipendenti: 4 dipendenti_diretti: 2`, keyOf("D")) {
		t.Fatalf("fatti di D: %v", e.admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "dipendenti: 4", "element": keyOf("D")}, http.StatusOK)["facts"])
	}
	for cond, want := range map[string]bool{
		`capability: Bonifici`:           true,
		`capability: CAP-PAY`:            true, // la capability che contiene quella supportata
		`capability: "Gestione clienti"`: false,
		`dipende_da: B`:                  true,
		`dipende_da: "Motore antifrode"`: true,
		`time: investire`:                true,
		`time: tolerate`:                 false,
		`fine_supporto_giorni: <60`:      true,
		`dipendenti: 0`:                  true, // da A dipendono solo capability e asset
	} {
		if got := check(cond, keyOf("A")); got != want {
			t.Fatalf("condizione %q su A: %v, attesa %v (fatti %v)", cond, got, want,
				e.admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": cond, "element": keyOf("A")}, http.StatusOK)["facts"])
		}
	}
	if !check(`dipende_da: "Core conti" time: migrare -fine_supporto_giorni: presente`, keyOf("B")) {
		t.Fatal("fatti di B")
	}
	// Un impatto su D (molti dipendenti) e la richiesta che lo contiene.
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuovo canale di onboarding"}, http.StatusCreated)
	imp := e.dm.do("POST", "/api/v1/demand/requests/"+br["key"].(string)+"/impacts", map[string]any{"application_id": e.id("D"), "change_kind": "change"}, http.StatusCreated)
	if !check(`dipendenti: >=4 capability: "Gestione clienti"`, imp["key"].(string)) || !check(`dipendenti_max: 4`, br["key"].(string)) {
		t.Fatal("fatti dell'impatto e della richiesta")
	}

	// --- Razionalizzazione -------------------------------------------------------
	view.do("PUT", base+"/settings", map[string]any{"node_types": []string{"application"}, "eol_warning_days": 90, "duplicate_min": 2}, http.StatusForbidden)
	ra := view.do("GET", base+"/rationalization", nil, http.StatusOK)
	dups := eaList(ra["duplicates"])
	if len(dups) != 1 || dups[0]["capability_name"] != "Bonifici" || len(eaList(dups[0]["applications"])) != 2 {
		t.Fatalf("duplicati di capability: %v", dups)
	}
	eos := eaList(ra["end_of_support"])
	if len(eos) != 1 || eos[0]["technology"] != "Java 8" {
		t.Fatalf("fine supporto vicina: %v", eos)
	}
	if tc := ra["time_counts"].(map[string]any); tc["invest"].(float64) != 1 || tc["migrate"].(float64) != 1 || tc["none"].(float64) != 4 {
		t.Fatalf("conteggi TIME: %v", tc)
	}
	if rw := eaList(ra["retiring_with_dependents"]); len(rw) != 1 || rw[0]["name"] != "API conti" {
		t.Fatalf("da migrare con dipendenti: %v", rw)
	}
	if orph := eaList(ra["orphans"]); len(orph) != 3 { // C, E, F senza capability
		t.Fatalf("applicazioni senza capability: %v", eaNames(orph))
	}
	arch.do("PUT", base+"/settings", map[string]any{"node_types": []string{"application", "logical_asset", "capability"}, "eol_warning_days": 10, "duplicate_min": 3}, http.StatusOK)
	if ra := view.do("GET", base+"/rationalization", nil, http.StatusOK); len(eaList(ra["duplicates"])) != 0 || len(eaList(ra["end_of_support"])) != 0 {
		t.Fatalf("soglie del tenant: %v", ra)
	}
	arch.do("PUT", base+"/settings", map[string]any{"node_types": []string{"application", "logical_asset", "capability", "ict_provider"}, "eol_warning_days": 180, "duplicate_min": 2}, http.StatusOK)
	prof := view.do("GET", base+"/profile/A", nil, http.StatusOK)
	if prof["time_note"] != "canale strategico" || len(eaList(prof["technologies"])) != 2 || len(eaList(prof["capabilities"])) != 1 || prof["dependencies"].(float64) != 4 {
		t.Fatalf("profilo di A: %v", prof)
	}

	// --- Diagrammi ---------------------------------------------------------------
	layout := map[string]any{"nodes": []any{map[string]any{"id": e.id("A"), "x": 10, "y": 20}, map[string]any{"id": e.id("B"), "x": 240, "y": 20},
		map[string]any{"id": e.id("E"), "x": 10, "y": 160}}, "notes": []any{map[string]any{"x": 300, "y": 200, "text": "Da rivedere con la sicurezza"}}}
	view.do("POST", base+"/diagrams", map[string]any{"name": "Canali", "layout": layout}, http.StatusForbidden)
	dg := arch.do("POST", base+"/diagrams", map[string]any{"name": "Canali digitali", "kind": "dependency", "layout": layout}, http.StatusCreated)
	if len(eaList(dg["nodes"])) != 3 || len(eaList(dg["edges"])) != 2 || dg["version"].(float64) != 1 {
		t.Fatalf("diagramma: %v", dg)
	}
	if note := eaList(dg["layout"].(map[string]any)["notes"]); len(note) != 1 || note[0]["id"] == "" {
		t.Fatalf("annotazioni: %v", note)
	}
	arch.do("POST", base+"/diagrams", map[string]any{"name": "Rotto", "layout": map[string]any{"nodes": []any{map[string]any{"id": "00000000-0000-4000-8000-000000000000", "x": 0, "y": 0}}}}, http.StatusUnprocessableEntity)
	arch.do("POST", base+"/diagrams", map[string]any{"name": "Tipo", "kind": "lavagna"}, http.StatusUnprocessableEntity)
	// Il diagramma segue i dati: l'applicazione rinominata compare con il nome nuovo.
	arch.do("PATCH", base+"/applications/"+e.id("A"), map[string]any{"name": "Home banking retail"}, http.StatusOK)
	dgID := dg["id"].(string)
	got := view.do("GET", base+"/diagrams/"+dgID, nil, http.StatusOK)
	found := false
	for _, n := range eaList(got["nodes"]) {
		found = found || n["name"] == "Home banking retail"
	}
	if !found {
		t.Fatalf("nome aggiornato nel diagramma: %v", got["nodes"])
	}
	// Solo i tipi di relazione scelti; lock ottimistico sulla versione.
	upd := arch.do("PUT", base+"/diagrams/"+dgID, map[string]any{"version": 1, "layout": map[string]any{"nodes": layout["nodes"], "notes": []any{}, "edge_types": []string{"calls_api"}}}, http.StatusOK)
	if len(eaList(upd["edges"])) != 1 || upd["version"].(float64) != 2 {
		t.Fatalf("diagramma aggiornato: %v", upd)
	}
	arch.do("PUT", base+"/diagrams/"+dgID, map[string]any{"version": 1, "name": "Vecchio"}, http.StatusConflict)
	if n := len(eaItems(view.do("GET", base+"/diagrams", nil, http.StatusOK))); n != 1 {
		t.Fatalf("elenco dei diagrammi: %d", n)
	}

	// --- Export e import ---------------------------------------------------------
	st, _, raw = view.get(base + "/catalog/export?format=json")
	if st != http.StatusOK {
		t.Fatalf("export JSON: %d %s", st, raw)
	}
	var bundle map[string]any
	if err := json.Unmarshal(raw, &bundle); err != nil || bundle["format"] != "regia.ea/v1" || len(eaList(bundle["capabilities"])) != 4 ||
		len(eaList(bundle["dependencies"])) != 7 || len(eaList(bundle["relation_types"])) != 1 || len(eaList(bundle["technologies"])) != 2 {
		t.Fatalf("catalogo esportato: %v %s", err, raw)
	}
	st, _, raw = view.get(base + "/catalog/export?format=csv&what=dependencies")
	if st != http.StatusOK || !strings.Contains(string(raw), "origine;destinazione;tipo") || !strings.Contains(string(raw), "A;B;calls_api;SOAP;;") {
		t.Fatalf("export CSV delle dipendenze: %s", raw)
	}
	st, _, raw = view.get(base + "/catalog/export?format=csv&what=capabilities")
	if st != http.StatusOK || !strings.Contains(string(raw), "CAP-INST;Bonifici istantanei;Bonifici") {
		t.Fatalf("export CSV delle capability: %s", raw)
	}

	// Un secondo tenant con lo stesso catalogo applicativo riceve il catalogo esteso.
	e2 := newEAEnv(t, srv.URL, "ea2")
	e2.view.do("POST", base+"/catalog/import", map[string]any{"json": bundle}, http.StatusForbidden)
	dry := e2.arch.do("POST", base+"/catalog/import?dry_run=true", map[string]any{"json": bundle}, http.StatusOK)
	if dry["dry_run"] != true || dry["errors"].(float64) != 0 || dry["created"].(float64) < 14 {
		t.Fatalf("prova di import del catalogo: %v", dry)
	}
	if n := len(eaItems(e2.arch.do("GET", base+"/capabilities", nil, http.StatusOK))); n != 0 {
		t.Fatalf("la prova ha salvato %d capability", n)
	}
	imp2 := e2.arch.do("POST", base+"/catalog/import", map[string]any{"json": bundle}, http.StatusOK)
	if imp2["errors"].(float64) != 0 {
		t.Fatalf("import del catalogo: %v", imp2)
	}
	if lv := levels(e2.view.do("GET", base+"/impact/D?direction=dependents", nil, http.StatusOK)); lv["Home banking"] != 3 {
		t.Fatalf("impatto nel secondo tenant: %v", lv)
	}
	if again := e2.arch.do("POST", base+"/catalog/import", map[string]any{"json": bundle}, http.StatusOK); again["created"].(float64) != 0 || again["errors"].(float64) != 0 {
		t.Fatalf("import ripetuto: %v", again)
	}
	// CSV: la figlia prima del padre, un'applicazione inesistente, una capability per codice.
	capCSV := "codice;nome;padre;criticita;applicazioni\n" +
		"CRM-SEG;Segmentazione;CRM;media;F\n" +
		"CRM;Relazione con il cliente;;alta;A|D\n" +
		";Campagne;CRM;;Z-INESISTENTE\n"
	rep := e2.arch.do("POST", base+"/catalog/import", map[string]any{"kind": "capabilities", "csv": capCSV}, http.StatusOK)
	if rep["created"].(float64) != 2 || rep["errors"].(float64) != 1 {
		t.Fatalf("import CSV delle capability: %v", rep)
	}
	depCSV := "origine,destinazione,tipo,protocollo,criticita\n" +
		"F,C,legge dati da,JDBC,media\n" +
		"A,B,calls_api,REST,critica\n" +
		"A,CRM,chiama le API di,,\n"
	rep = e2.arch.do("POST", base+"/catalog/import", map[string]any{"kind": "dependencies", "csv": depCSV}, http.StatusOK)
	if rep["created"].(float64) != 1 || rep["updated"].(float64) != 1 || rep["errors"].(float64) != 1 {
		t.Fatalf("import CSV delle dipendenze: %v", rep)
	}
	e2.arch.do("POST", base+"/catalog/import", map[string]any{"kind": "capabilities", "csv": "colore;nome\nx;y\n"}, http.StatusUnprocessableEntity)

	// --- Isolamento tra tenant ---------------------------------------------------
	e2.view.do("GET", base+"/diagrams/"+dgID, nil, http.StatusNotFound)
	e2.view.do("GET", base+"/impact/"+e.id("D"), nil, http.StatusNotFound)
	e2.arch.do("POST", base+"/relations", map[string]any{"type": "calls_api", "source": e2.id("A"), "target": e.id("C")}, http.StatusUnprocessableEntity)
	e2.arch.do("PATCH", base+"/relations/"+ab["id"].(string), map[string]any{"attrs": map[string]any{}}, http.StatusNotFound)
	for _, n := range eaList(e2.view.do("GET", base+"/graph", nil, http.StatusOK)["nodes"]) {
		if n["id"] == e.id("A") {
			t.Fatal("il grafo del secondo tenant contiene un nodo del primo")
		}
	}
	if n := len(eaItems(e2.view.do("GET", base+"/diagrams", nil, http.StatusOK))); n != 0 {
		t.Fatalf("diagrammi visibili da un altro tenant: %d", n)
	}
	arch.do("DELETE", base+"/diagrams/"+dgID, nil, http.StatusNoContent)
	arch.do("DELETE", base+"/diagrams/"+dgID, nil, http.StatusNotFound)
}

func eaNames(list []map[string]any) []string {
	out := []string{}
	for _, m := range list {
		out = append(out, m["name"].(string))
	}
	return out
}
