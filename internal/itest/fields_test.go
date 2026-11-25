package itest

import (
	"context"
	"encoding/json"
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
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Campi personalizzati gestibili da interfaccia e pagina di dettaglio (sdlc.md §10):
// creazione e associazione, tutti i tipi con la validazione, multipli, visibilità e
// obbligatorietà per fase (gate implicito), archiviazione, query e fatti, permessi,
// isolamento tra tenant, export e import.
func TestCustomFields(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()

	setup := func(prefix string) string {
		slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000"), ".", "")
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: slug, Name: "Campi " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
		}); err != nil {
			t.Fatal(err)
		}
		for _, m := range []struct {
			name string
			in   tenant.MemberInput
		}{
			{"richiedente", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}}},
			{"dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}}},
			{"lettore", tenant.MemberInput{Roles: []string{"viewer"}}},
			{"campi", tenant.MemberInput{Roles: []string{"fields_admin"}}},
		} {
			in := m.in
			in.TenantSlug, in.Email, in.Name, in.Password = slug, m.name+"@"+slug+".test", "Persona "+m.name, testPassword
			if _, err := tenant.AddMember(ctx, d, in); err != nil {
				t.Fatal(err)
			}
		}
		return slug
	}
	slugA, slugB := setup("fa"), setup("fb")
	admin := login(t, srv.URL, "admin@"+slugA+".test", testPassword)
	req := login(t, srv.URL, "richiedente@"+slugA+".test", testPassword)
	man := login(t, srv.URL, "dm@"+slugA+".test", testPassword)
	reader := login(t, srv.URL, "lettore@"+slugA+".test", testPassword)
	fa := login(t, srv.URL, "campi@"+slugA+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)

	// --- Permessi: servono fields.admin (ruolo "Amministratore dei campi") o tenant.admin.
	reader.do("GET", "/api/v1/fields", nil, http.StatusForbidden)
	man.do("POST", "/api/v1/fields", map[string]any{"key": "abusivo", "name": "Abusivo", "data_type": "string"}, http.StatusForbidden)
	fa.do("GET", "/api/v1/fields", nil, http.StatusOK)
	admin.do("GET", "/api/v1/fields/catalog", nil, http.StatusOK)

	// --- Creazione, con le validazioni della definizione.
	field := func(body map[string]any) map[string]any {
		return fa.do("POST", "/api/v1/fields", body, http.StatusCreated)
	}
	bad := func(body map[string]any) {
		t.Helper()
		fa.do("POST", "/api/v1/fields", body, http.StatusUnprocessableEntity)
	}
	bad(map[string]any{"key": "Canale", "name": "Canale", "data_type": "enum"})                   // chiave non valida
	bad(map[string]any{"key": "urgenza", "name": "Doppione", "data_type": "string"})              // chiave già usata
	bad(map[string]any{"key": "spunta", "name": "Spunta", "data_type": "boolean", "multi": true}) // multiplo non ammesso
	bad(map[string]any{"key": "stato_x", "name": "Stato", "data_type": "state"})                  // riservato al ciclo di vita
	bad(map[string]any{"key": "rif", "name": "Rif", "data_type": "element", "settings": map[string]any{"types": []string{"inesistente"}}})
	bad(map[string]any{"key": "ref", "name": "Ref", "data_type": "member", "settings": map[string]any{"group": "Nessuno"}})
	bad(map[string]any{"key": "scelta", "name": "Scelta", "data_type": "enum", "options": []map[string]any{{"name": "A"}, {"name": "a"}}})
	bad(map[string]any{"key": "scelta", "name": "Scelta", "data_type": "enum", "options": []map[string]any{{"name": "A", "color": "#ff0000"}}})

	canale := field(map[string]any{"key": "canale", "name": "Canale", "data_type": "enum", "multi": true, "aliases": []string{"channel"},
		"help":    "Da dove arriva la richiesta",
		"options": []map[string]any{{"name": "Email", "color": "accent"}, {"name": "Telefono", "color": "ok"}, {"name": "Portale", "color": "flag"}}})
	if len(canale["options"].([]any)) != 3 || canale["options"].([]any)[0].(map[string]any)["color"] != "accent" {
		t.Fatalf("opzioni del canale: %v", canale["options"])
	}
	field(map[string]any{"key": "golive", "name": "Data go-live richiesta", "data_type": "date"})
	field(map[string]any{"key": "referente_it", "name": "Referente IT", "data_type": "member", "settings": map[string]any{"group": "demand management"}})
	field(map[string]any{"key": "link_doc", "name": "Link documento", "data_type": "url"})
	field(map[string]any{"key": "classificazione", "name": "Classificazione", "data_type": "enum",
		"options": []map[string]any{{"name": "Pubblica"}, {"name": "Interna"}, {"name": "Riservata", "color": "danger"}}, "default": "Interna"})
	field(map[string]any{"key": "momento", "name": "Momento", "data_type": "datetime"})
	field(map[string]any{"key": "durata", "name": "Durata stimata", "data_type": "duration"})
	field(map[string]any{"key": "budget", "name": "Budget", "data_type": "money", "settings": map[string]any{"currency": "eur", "min": 0}})
	field(map[string]any{"key": "gruppo", "name": "Gruppo competente", "data_type": "group", "multi": true})
	field(map[string]any{"key": "collegata", "name": "Richiesta collegata", "data_type": "element", "settings": map[string]any{"types": []string{"business_request"}}})
	field(map[string]any{"key": "esterno", "name": "Fornitore esterno", "data_type": "boolean"})
	field(map[string]any{"key": "lotti", "name": "Lotti", "data_type": "integer", "settings": map[string]any{"min": 1, "max": 10}})
	field(map[string]any{"key": "quota", "name": "Quota", "data_type": "decimal"})
	field(map[string]any{"key": "tag", "name": "Etichette", "data_type": "string", "multi": true})
	field(map[string]any{"key": "inutile", "name": "Mai usato", "data_type": "text"})
	cl := fa.do("GET", "/api/v1/fields/classificazione", nil, http.StatusOK)
	if cl["default"] == nil || cl["default"] == "Interna" {
		t.Fatalf("il default si salva come id dell'opzione: %v", cl["default"])
	}

	// --- Associazione al tipo, con visibilità condizionale e obbligatorietà da una fase.
	assoc := func(key string, body map[string]any, status int) map[string]any {
		t.Helper()
		return fa.do("PUT", "/api/v1/fields/"+key+"/types/business_request", body, status)
	}
	assoc("golive", map[string]any{"required_from": "fase inventata"}, http.StatusUnprocessableEntity)
	assoc("classificazione", map[string]any{"visible_when": "driver: : normativo"}, http.StatusUnprocessableEntity)
	assoc("canale", map[string]any{"section": "Origine"}, http.StatusOK)
	assoc("golive", map[string]any{"required_from": "triage", "section": "Pianificazione"}, http.StatusOK)
	assoc("referente_it", map[string]any{"section": "Persone"}, http.StatusOK)
	assoc("link_doc", map[string]any{}, http.StatusOK)
	got := assoc("classificazione", map[string]any{"visible_when": "driver: normativo", "required_from": "Triage"}, http.StatusOK)
	if a := got["types"].([]any)[0].(map[string]any); a["required_from"] != "Triage" || a["visible_when"] != "driver: normativo" {
		t.Fatalf("associazione: %v", a)
	}
	for _, k := range []string{"momento", "durata", "budget", "gruppo", "collegata", "esterno", "lotti", "quota", "tag", "inutile"} {
		assoc(k, map[string]any{"section": "Altro"}, http.StatusOK)
	}
	tf := req.do("GET", "/api/v1/types/business_request/fields", nil, http.StatusOK)["items"].([]any)
	if len(tf) < 15 {
		t.Fatalf("campi del tipo per il form: %d", len(tf))
	}

	// --- Valori: tutti i tipi, con normalizzazione.
	other := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta di riferimento"}, http.StatusCreated)
	app := admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Contabilità"}, http.StatusCreated)
	appEl := admin.do("GET", "/api/v1/elements/"+app["id"].(string), nil, http.StatusOK)["element"].(map[string]any)
	full := map[string]any{
		"canale": []any{"Email", "email", "Portale"}, "referente_it": man.member, "link_doc": "https://docs.example.com/dora",
		"momento": "2026-11-24T10:30:00+01:00", "durata": "1g 2h", "budget": map[string]any{"amount": 1500.555},
		"gruppo": []any{"PMO", "Compliance"}, "collegata": other["key"], "esterno": true, "lotti": 3, "quota": 2.5,
		"tag": []any{"dora", "ict"},
	}
	created := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Registro DORA", "driver": "regulatory", "fields": full}, http.StatusCreated)
	key, id := created["key"].(string), created["id"].(string)
	f := created["fields"].(map[string]any)
	if n := len(f["canale"].([]any)); n != 2 {
		t.Fatalf("i doppioni dei multipli si tolgono: %v", f["canale"])
	}
	if f["durata"] != 600.0 {
		t.Fatalf("durata «1g 2h» in minuti: %v", f["durata"])
	}
	if m := f["budget"].(map[string]any); m["amount"] != 1500.56 || m["currency"] != "EUR" {
		t.Fatalf("importo: %v", m)
	}
	if f["momento"] != "2026-11-24T09:30:00Z" {
		t.Fatalf("data e ora in UTC: %v", f["momento"])
	}
	if f["collegata"] != other["id"] || len(f["gruppo"].([]any)) != 2 {
		t.Fatalf("riferimenti per id: %v %v", f["collegata"], f["gruppo"])
	}
	if f["classificazione"] == nil {
		t.Fatalf("il valore predefinito si applica alla creazione: %v", f)
	}

	// Validazione per tipo: ognuno di questi valori è rifiutato con 422.
	for _, c := range []map[string]any{
		{"link_doc": "ftp://server/documento"}, {"link_doc": "non è un indirizzo"},
		{"lotti": 11}, {"lotti": 2.5}, {"quota": "molto"},
		{"budget": -5}, {"budget": map[string]any{"amount": 10, "currency": "euro"}},
		{"referente_it": reader.member}, // non è nel gruppo Demand management
		{"collegata": appEl["key"]},     // tipo non ammesso
		{"collegata": "DEM-99999"}, {"collegata": "non-una-chiave"},
		{"canale": []any{"Email", "Piccione"}}, {"esterno": "sì"}, {"momento": "2026-11-24"},
		{"durata": "tre giorni"}, {"gruppo": "Inesistenti"}, {"classificazione": []any{"Pubblica", "Interna"}},
	} {
		req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Non valida", "fields": c}, http.StatusUnprocessableEntity)
	}

	// --- Dettaglio: campi con visibilità ed etichette dei riferimenti.
	cfOf := func(u *user, ref string) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, x := range u.do("GET", "/api/v1/elements/"+ref, nil, http.StatusOK)["custom_fields"].([]any) {
			m := x.(map[string]any)
			out[m["key"].(string)] = m
		}
		return out
	}
	cf := cfOf(req, key)
	if cf["classificazione"]["visible"] != true || cf["golive"]["required_now"] != false {
		t.Fatalf("classificazione visibile (driver normativo), go-live non ancora obbligatorio: %v %v", cf["classificazione"], cf["golive"])
	}
	if l := cf["collegata"]["labels"].(map[string]any)[other["id"].(string)].(map[string]any); l["key"] != other["key"] {
		t.Fatalf("etichetta dell'elemento collegato: %v", l)
	}
	if cf["referente_it"]["labels"].(map[string]any)[man.member].(map[string]any)["label"] != "Persona dm" {
		t.Fatalf("etichetta della persona: %v", cf["referente_it"]["labels"])
	}
	plain := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta efficienza", "driver": "efficiency"}, http.StatusCreated)
	if cfOf(req, plain["key"].(string))["classificazione"]["visible"] != false {
		t.Fatal("classificazione nascosta senza driver normativo")
	}

	// --- Obbligatorietà per fase: gate implicito sul passaggio a Triage.
	pid := plain["id"].(string)
	req.do("POST", "/api/v1/elements/"+pid+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	det := man.do("GET", "/api/v1/elements/"+pid, nil, http.StatusOK)
	blocked := ""
	for _, tr := range det["transitions"].([]any) {
		if m := tr.(map[string]any); m["to"] == "Triage" {
			for _, b := range m["blocked"].([]any) {
				blocked += b.(string)
			}
		}
	}
	if !strings.Contains(blocked, "Data go-live richiesta") || strings.Contains(blocked, "Classificazione") {
		t.Fatalf("passaggio a Triage bloccato solo dal go-live (classificazione nascosta): %q", blocked)
	}
	r := man.doRaw("POST", "/api/v1/elements/"+pid+"/transitions", map[string]any{"to": "Triage"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "per passare a «Triage» compila: Data go-live richiesta") {
		t.Fatalf("messaggio del gate implicito: %d %s", r.status, r.body)
	}
	ver := det["element"].(map[string]any)["version"]
	upd := man.do("PATCH", "/api/v1/elements/"+pid, map[string]any{"version": ver, "fields": map[string]any{"golive": "2027-03-31"}}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+pid+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
	el := man.do("GET", "/api/v1/elements/"+pid, nil, http.StatusOK)["element"].(map[string]any)
	r = man.doRaw("PATCH", "/api/v1/elements/"+pid, map[string]any{"version": el["version"], "fields": map[string]any{"golive": nil}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "non si può svuotare") {
		t.Fatalf("un obbligatorio della fase non si svuota: %d %s", r.status, r.body)
	}
	_ = upd
	// La richiesta normativa: serve anche la classificazione (visibile), e svuotarla blocca.
	req.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	el = man.do("GET", "/api/v1/elements/"+id, nil, http.StatusOK)["element"].(map[string]any)
	man.do("PATCH", "/api/v1/elements/"+id, map[string]any{"version": el["version"], "fields": map[string]any{"classificazione": nil, "golive": "2027-01-15"}}, http.StatusOK)
	r = man.doRaw("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Triage"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Classificazione") || strings.Contains(r.body, "go-live") {
		t.Fatalf("classificazione obbligatoria se visibile: %d %s", r.status, r.body)
	}
	el = man.do("GET", "/api/v1/elements/"+id, nil, http.StatusOK)["element"].(map[string]any)
	man.do("PATCH", "/api/v1/elements/"+id, map[string]any{"version": el["version"], "fields": map[string]any{"classificazione": "Riservata"}}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)

	// --- Query sui campi nuovi (per chiave e alias) e fatti delle condizioni.
	count := func(u *user, q string) int {
		t.Helper()
		res := u.do("GET", "/api/v1/elements?q="+url.QueryEscape(q), nil, http.StatusOK)
		return len(res["items"].([]any))
	}
	for q, want := range map[string]int{
		"canale: Email":                       1,
		"channel: Portale":                    1,
		"canale: Telefono":                    0,
		"link_doc: presente":                  1,
		"budget: >1000":                       1,
		"budget: >2000":                       0,
		"durata: >=600":                       1,
		"momento: >2026-11-01":                1,
		"gruppo: PMO":                         1,
		"gruppo: \"Demand management\"":       0,
		"collegata: " + other["key"].(string): 1,
		"referente_it: \"Persona dm\"":        1,
		"esterno: sì":                         1,
		"lotti: 3":                            1,
		"golive: 2027-03-31":                  1,
		"classificazione: Riservata":          1,
		"tipo: business_request tag: dora":    1,
	} {
		if n := count(man, q); n != want {
			t.Errorf("query %q: %d risultati, attesi %d", q, n, want)
		}
	}
	man.doRaw("GET", "/api/v1/elements?q="+url.QueryEscape("gruppo: Inesistenti"), nil).expect(t, http.StatusUnprocessableEntity)
	chk := man.do("POST", "/api/v1/conditions/check", map[string]any{"element": key,
		"condition": "canale: Email gruppo: PMO budget: >1000 budget.valuta: EUR collegata: " + other["key"].(string) + " esterno: sì fase: Triage referente_it: \"Persona dm\""}, http.StatusOK)
	if chk["result"] != true {
		t.Fatalf("fatti dei campi nelle condizioni: %v", chk)
	}
	qf := man.do("GET", "/api/v1/query/fields", nil, http.StatusOK)["items"].([]any)
	seen := map[string]bool{}
	for _, x := range qf {
		m := x.(map[string]any)
		if m["source"] == "custom" {
			seen[m["key"].(string)] = true
		}
	}
	for _, k := range []string{"canale", "gruppo", "collegata", "momento", "budget", "durata", "link_doc"} {
		if !seen[k] {
			t.Errorf("campo %s assente da /query/fields (colonne della tabella)", k)
		}
	}

	// --- Archiviazione: i valori restano, il campo non si modifica né si elimina.
	r = fa.doRaw("DELETE", "/api/v1/fields/tag", nil)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "archivia") {
		t.Fatalf("un campo usato non si elimina: %d %s", r.status, r.body)
	}
	fa.do("PATCH", "/api/v1/fields/tag", map[string]any{"archived": true}, http.StatusOK)
	el = man.do("GET", "/api/v1/elements/"+id, nil, http.StatusOK)["element"].(map[string]any)
	if len(el["fields"].(map[string]any)["tag"].([]any)) != 2 {
		t.Fatal("i valori di un campo archiviato restano")
	}
	man.do("PATCH", "/api/v1/elements/"+id, map[string]any{"version": el["version"], "fields": map[string]any{"tag": []any{"altro"}}}, http.StatusUnprocessableEntity)
	if count(man, "tag: dora") != 1 {
		t.Fatal("un campo archiviato resta cercabile")
	}
	fa.do("PATCH", "/api/v1/fields/canale", map[string]any{"data_type": "string"}, http.StatusUnprocessableEntity)
	fa.do("PATCH", "/api/v1/fields/canale", map[string]any{"key": "canale2"}, http.StatusUnprocessableEntity)
	fa.do("DELETE", "/api/v1/fields/inutile", nil, http.StatusNoContent)
	// Opzioni: quella usata tolta dall'elenco si archivia, quella mai usata si elimina.
	opts := fa.do("GET", "/api/v1/fields/canale", nil, http.StatusOK)["options"].([]any)
	var tel map[string]any
	for _, o := range opts {
		if o.(map[string]any)["name"] == "Telefono" {
			tel = o.(map[string]any)
		}
	}
	// Fax si aggiunge e poi si toglie senza essere mai usata: sparisce.
	var keepAll []map[string]any
	for _, o := range opts {
		m := o.(map[string]any)
		keepAll = append(keepAll, map[string]any{"id": m["id"], "name": m["name"], "color": m["color"]})
	}
	fa.do("PUT", "/api/v1/fields/canale/options", map[string]any{"options": append(keepAll, map[string]any{"name": "Fax"})}, http.StatusOK)
	after := fa.do("PUT", "/api/v1/fields/canale/options", map[string]any{"options": []map[string]any{
		{"id": tel["id"], "name": "Telefono fisso", "color": "muted"}, {"name": "Chat", "color": "ok"},
	}}, http.StatusOK)["options"].([]any)
	names := []string{}
	for _, o := range after {
		m := o.(map[string]any)
		names = append(names, m["name"].(string)+map[bool]string{true: "*", false: ""}[m["archived"] == true])
	}
	if strings.Join(names, ",") != "Telefono fisso,Chat,Email*,Portale*" {
		t.Fatalf("opzioni dopo il salvataggio: %v", names)
	}
	if count(man, "canale: Email") != 1 {
		t.Fatal("i valori su un'opzione archiviata restano cercabili")
	}

	// --- Audit di ogni modifica della configurazione.
	actions := map[string]bool{}
	for _, it := range admin.do("GET", "/api/v1/audit?limit=500", nil, http.StatusOK)["items"].([]any) {
		actions[it.(map[string]any)["action"].(string)] = true
	}
	for _, a := range []string{"field.create", "field.associate", "field.archive", "field.delete", "field.options"} {
		if !actions[a] {
			t.Errorf("manca l'audit %s", a)
		}
	}

	// --- Isolamento tra tenant.
	adminB.do("GET", "/api/v1/fields/canale", nil, http.StatusNotFound)
	if items := adminB.do("GET", "/api/v1/fields", nil, http.StatusOK)["items"].([]any); len(items) != 2 { // urgenza e benefici_attesi
		t.Fatalf("il tenant B vede solo i suoi campi: %d", len(items))
	}
	adminB.doRaw("GET", "/api/v1/elements?q="+url.QueryEscape("canale: Email"), nil).expect(t, http.StatusUnprocessableEntity)
	adminB.do("GET", "/api/v1/elements/"+key, nil, http.StatusNotFound)

	// --- Export e import: il file passa al tenant B (prima di prova, poi davvero).
	status, _, raw := admin.get("/api/v1/fields/export")
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, raw)
	}
	var bundle map[string]any
	if err := json.Unmarshal(raw, &bundle); err != nil || bundle["format"] != engine.FieldsBundleFormat {
		t.Fatalf("export: %v %s", err, raw)
	}
	fa.do("POST", "/api/v1/fields/import", bundle, http.StatusOK) // nello stesso tenant: solo aggiornamenti
	dry := adminB.do("POST", "/api/v1/fields/import?dry_run=true", bundle, http.StatusOK)["report"].(map[string]any)
	if len(dry["created"].([]any)) < 10 {
		t.Fatalf("import di prova: %v", dry)
	}
	if items := adminB.do("GET", "/api/v1/fields", nil, http.StatusOK)["items"].([]any); len(items) != 2 {
		t.Fatal("l'import di prova non lascia traccia")
	}
	rep := adminB.do("POST", "/api/v1/fields/import", bundle, http.StatusOK)["report"].(map[string]any)
	if rep["associations"].(float64) < 10 {
		t.Fatalf("import: %v", rep)
	}
	cb := adminB.do("GET", "/api/v1/fields/classificazione", nil, http.StatusOK)
	if cb["types"].([]any)[0].(map[string]any)["visible_when"] != "driver: normativo" || cb["default"] == nil {
		t.Fatalf("classificazione importata: %v", cb)
	}
	if cb["default"] == cl["default"] {
		t.Fatal("il default importato punta all'opzione del tenant B")
	}
	reqB := adminB.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nel tenant B", "fields": map[string]any{"canale": []any{"Chat"}}}, http.StatusCreated)
	if len(reqB["fields"].(map[string]any)["canale"].([]any)) != 1 {
		t.Fatalf("campo importato usabile: %v", reqB["fields"])
	}
	// Tipo di dato diverso: l'import si ferma senza cambiare niente.
	for _, x := range bundle["fields"].([]any) {
		if m := x.(map[string]any); m["key"] == "lotti" {
			m["data_type"] = "decimal"
		}
	}
	adminB.do("POST", "/api/v1/fields/import", bundle, http.StatusUnprocessableEntity)
	adminB.do("POST", "/api/v1/fields/import", map[string]any{"format": "altro/v1"}, http.StatusUnprocessableEntity)
	reader.do("POST", "/api/v1/fields/import", bundle, http.StatusForbidden)

	// --- Collegamenti: la richiesta riferita non è figlia, l'impatto sì.
	links := admin.do("GET", "/api/v1/elements/"+key+"/links", nil, http.StatusOK)
	if links["parent"] != nil {
		t.Fatalf("collegamenti: %v", links)
	}
}

type rawResp struct {
	status int
	body   string
}

func (r rawResp) expect(t *testing.T, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("stato %d, atteso %d: %s", r.status, status, r.body)
	}
}

// doRaw esegue la richiesta senza verificare lo stato (per leggere i messaggi di errore).
func (u *user) doRaw(method, path string, body any) rawResp {
	u.t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, u.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	return rawResp{status: resp.StatusCode, body: buf.String()}
}
