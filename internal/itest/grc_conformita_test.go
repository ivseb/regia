package itest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Conformità (piano, passo 20): pacchetti di partenza, import ed export OSCAL, CSV e JSON,
// nuova versione con le mappature che passano per riferimento, corrispondenze tra normative,
// applicabilità per condizione sui fatti delle applicazioni, controlli che coprono requisiti
// di più normative, copertura, evidenze con firma, separazione dei compiti e scadenza,
// verifiche dei requisiti di progetto come evidenze, evidenze automatiche idempotenti,
// pacchetto per l'audit, permessi (compliance officer, auditor, responsabile, editor) e
// isolamento tra tenant.
func TestGRCConformita(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("grc-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Conformità", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	add := func(s, name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = s, name+"@"+s+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add(slug, "co", tenant.MemberInput{Roles: []string{"compliance_officer"}, Groups: []string{"Compliance"}})
	add(slug, "co2", tenant.MemberInput{Roles: []string{"compliance_officer"}, Groups: []string{"Compliance"}})
	add(slug, "owner", tenant.MemberInput{Roles: []string{"editor"}})
	add(slug, "aud", tenant.MemberInput{MemberType: "auditor", Roles: []string{"auditor"}})
	add(slug, "ed", tenant.MemberInput{Roles: []string{"editor"}})
	add(slug, "bot", tenant.MemberInput{Roles: []string{"evidence_collector"}})
	add(slug, "dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add(slug, "req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add(slug, "sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	add(slug, "lead", tenant.MemberInput{Roles: []string{"viewer", "assessor"}})
	add(slug, "sec1", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Sicurezza"}})
	add(slug, "arch1", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Architetture"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	gs := grc.Register(eng)
	var clock atomic.Int64
	// Orologio del modulo: quello vero più uno scarto (per far passare i giorni delle scadenze).
	gs.Now = func() time.Time { return time.Now().UTC().Add(time.Duration(clock.Load())) }
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, co, co2, owner, aud, ed, bot := u("admin"), u("co"), u("co2"), u("owner"), u("aud"), u("ed"), u("bot")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Permessi di lettura: editor senza grc.read no; auditor sì, ma non scrive.
	ed.do("GET", "/api/v1/grc/dashboard", nil, http.StatusForbidden)
	ed.do("GET", "/api/v1/grc/library", nil, http.StatusForbidden)
	lib := aud.do("GET", "/api/v1/grc/library", nil, http.StatusOK)
	if len(list(lib["frameworks"])) != 0 || len(list(lib["starters"])) != 5 || lib["can_edit"] != false {
		t.Fatalf("libreria iniziale: %v", lib)
	}
	aud.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusForbidden)

	// --- Pacchetti di partenza: DORA, NIS2, ISO 27001, GDPR pubblicati e abilitati; AgID/ACN in bozza.
	for _, k := range []string{"dora", "nis2", "iso27001", "gdpr"} {
		r := co.do("POST", "/api/v1/grc/starters/"+k, map[string]any{"enable": true}, http.StatusCreated)
		if obj(r["framework"])["status"] != "active" {
			t.Fatalf("%s non abilitata: %v", k, r)
		}
	}
	co.do("POST", "/api/v1/grc/starters/dora", nil, http.StatusUnprocessableEntity) // c'è già
	agid := co.do("POST", "/api/v1/grc/starters/agid_acn", nil, http.StatusCreated)
	if f := obj(agid["framework"]); f["current"] != nil || f["draft"] == nil || f["status"] != "" {
		t.Fatalf("AgID/ACN deve restare in bozza: %v", f)
	}
	lib = co.do("GET", "/api/v1/grc/library", nil, http.StatusOK)
	fws := map[string]map[string]any{}
	for _, f := range list(lib["frameworks"]) {
		fws[obj(f)["key"].(string)] = obj(f)
	}
	if len(fws) != 5 || obj(fws["iso27001"]["current"])["assessable"] != 93.0 || obj(fws["iso27001"]["current"])["items"] != 97.0 {
		t.Fatalf("cataloghi importati: %v", fws["iso27001"])
	}
	// ISO: solo identificativi e titoli brevi, nessun testo.
	iso := co.do("GET", "/api/v1/grc/frameworks/iso27001", nil, http.StatusOK)
	for _, it := range list(iso["items"]) {
		if obj(it)["summary"] != "" {
			t.Fatalf("testo in una voce ISO: %v", it)
		}
	}
	// Corrispondenze tra normative dal pacchetto, verso le normative presenti.
	maps := list(co.do("GET", "/api/v1/grc/mappings?framework=nis2", nil, http.StatusOK)["items"])
	if len(maps) < 10 || !strings.Contains(jsonString(maps), "dora:Art. 11") || !strings.Contains(jsonString(maps), "iso27001:A.8.24") {
		t.Fatalf("corrispondenze NIS2: %d %v", len(maps), maps)
	}
	co.do("POST", "/api/v1/grc/mappings", map[string]any{"from": "gdpr:Art. 32", "to": "nis2:Art. 21(2)(h)", "relation": "related"}, http.StatusCreated)
	co.do("POST", "/api/v1/grc/mappings", map[string]any{"from": "gdpr:Art. 32", "to": "nis2:Art. 99"}, http.StatusUnprocessableEntity)
	status, hdr, body := co.get("/api/v1/grc/mappings.csv?framework=gdpr")
	if status != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !strings.Contains(string(body), "gdpr:Art. 32") {
		t.Fatalf("CSV delle corrispondenze: %d %s", status, body)
	}
	imp := co.do("POST", "/api/v1/grc/mappings/import", map[string]any{"csv": "da;a;relazione\ngdpr:Art. 25;iso27001:A.8.26;correlato\n"}, http.StatusOK)
	if imp["created"] != 1.0 {
		t.Fatalf("import CSV delle corrispondenze: %v", imp)
	}

	// --- Export OSCAL e reimport come nuova normativa; export CSV e JSON, import CSV.
	status, _, body = aud.get("/api/v1/grc/frameworks/iso27001/export?format=oscal")
	if status != http.StatusOK {
		t.Fatalf("export OSCAL: %d %s", status, body)
	}
	var oscal map[string]any
	if err := json.Unmarshal(body, &oscal); err != nil {
		t.Fatal(err)
	}
	cat := obj(oscal["catalog"])
	groups := list(cat["groups"])
	ctrlCount := 0
	for _, g := range groups {
		ctrlCount += len(list(obj(g)["controls"]))
	}
	if len(groups) != 4 || ctrlCount != 93 || obj(cat["metadata"])["oscal-version"] == nil || cat["uuid"] == nil ||
		!strings.Contains(string(body), `"value": "A.5.1"`) {
		t.Fatalf("OSCAL: %d gruppi, %d controlli", len(groups), ctrlCount)
	}
	isoCopy := co.do("POST", "/api/v1/grc/frameworks/import", map[string]any{"format": "oscal", "content": string(body),
		"framework": map[string]any{"key": "iso_copia", "name": "Copia ISO"}, "publish": true}, http.StatusOK)
	if obj(isoCopy["current"])["assessable"] != 93.0 || isoCopy["status"] != "" {
		t.Fatalf("reimport OSCAL: %v", isoCopy)
	}
	co.do("POST", "/api/v1/grc/frameworks/import", map[string]any{"format": "oscal", "content": string(body),
		"framework": map[string]any{"key": "iso_copia", "name": "Copia ISO"}, "publish": true}, http.StatusUnprocessableEntity) // versione già pubblicata
	co.do("POST", "/api/v1/grc/frameworks/import", map[string]any{"format": "oscal", "content": `{"catalog":{}}`,
		"framework": map[string]any{"key": "vuoto", "name": "Vuoto"}}, http.StatusUnprocessableEntity)
	status, _, body = aud.get("/api/v1/grc/frameworks/dora/export?format=csv")
	if status != http.StatusOK || !strings.HasPrefix(string(body), "\xEF\xBB\xBFriferimento;padre;tipo;titolo;sintesi") ||
		!strings.Contains(string(body), "Art. 9;Capo II;articolo;Protezione e prevenzione;") {
		t.Fatalf("export CSV: %d %s", status, body)
	}
	status, _, body = aud.get("/api/v1/grc/frameworks/gdpr/export?format=json")
	if status != http.StatusOK || !strings.Contains(string(body), `"format": "regia.grc.catalog/v1"`) || !strings.Contains(string(body), `"ref": "Art. 35"`) {
		t.Fatalf("export JSON: %d %s", status, body)
	}
	csvCat := "riferimento;padre;tipo;titolo;sintesi\nPOL;;capo;Politiche interne;\nPOL-1;POL;requisito;Politica di sicurezza approvata;La direzione approva la politica ogni anno.\nPOL-2;POL;requisito;Riesame annuale dei rischi;\n"
	interno := co.do("POST", "/api/v1/grc/frameworks/import", map[string]any{"format": "csv", "content": csvCat,
		"framework": map[string]any{"key": "interno", "name": "Regole interne", "kind": "internal"}, "version": map[string]any{"version": "1"},
		"publish": true, "enable": true}, http.StatusOK)
	if obj(interno["current"])["assessable"] != 2.0 || interno["status"] != "active" {
		t.Fatalf("import CSV: %v", interno)
	}
	co.do("POST", "/api/v1/grc/frameworks/import", map[string]any{"format": "csv", "content": "riferimento;padre;tipo;titolo\nX;Y;requisito;Padre mancante\n",
		"framework": map[string]any{"key": "rotto", "name": "Rotto"}, "version": map[string]any{"version": "1"}}, http.StatusUnprocessableEntity)
	// AgID/ACN: si completa la bozza e si pubblica.
	co.do("POST", "/api/v1/grc/frameworks/agid_acn/publish", nil, http.StatusUnprocessableEntity) // senza requisiti
	co.do("PUT", "/api/v1/grc/frameworks/agid_acn/draft/items", map[string]any{"ref": "MMS-ABSC-1", "parent": "MMS", "kind": "requirement",
		"title": "Inventario dei dispositivi autorizzati"}, http.StatusOK)
	co.do("POST", "/api/v1/grc/frameworks/agid_acn/publish", nil, http.StatusOK)

	// --- Applicazioni e applicabilità per condizione.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": u("lead").member}, http.StatusCreated)
	csvApps := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"FE;Frontend area clienti;alta;sì;personali;Web;Area clienti\n" +
		"REP;Reportistica;bassa;no;aggregati;Web;Area clienti\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csvApps}, http.StatusOK)
	apps := map[string]map[string]any{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)
	}
	feApp := apps["FE"]["key"].(string)
	prev := list(co.do("POST", "/api/v1/grc/rules/preview", map[string]any{"scope": "application", "condition": "funzione_critica: sì"}, http.StatusOK)["items"])
	if len(prev) != 2 || obj(prev[0])["key"] != feApp || obj(prev[0])["matched"] != true || obj(prev[1])["matched"] != false {
		t.Fatalf("anteprima della condizione: %v", prev)
	}
	co.do("POST", "/api/v1/grc/rules/preview", map[string]any{"scope": "application", "condition": "funzione_critica:"}, http.StatusUnprocessableEntity)
	aud.do("POST", "/api/v1/grc/recompute", nil, http.StatusForbidden)
	co.do("POST", "/api/v1/grc/recompute", nil, http.StatusOK)
	rules := co.do("GET", "/api/v1/grc/rules", nil, http.StatusOK)
	matches := map[string]float64{}
	for _, r := range list(rules["items"]) {
		matches[obj(r)["key"].(string)] = obj(r)["matches"].(float64)
	}
	if matches["dora_funzioni_critiche"] != 1 || matches["gdpr_dati_personali"] != 1 || matches["gdpr_dpia"] != 1 || len(matches) != 3 {
		t.Fatalf("regole dopo il ricalcolo: %v", matches)
	}
	fe := co.do("GET", "/api/v1/grc/scopes/application/FE", nil, http.StatusOK)
	feFw := map[string]int{}
	for _, f := range list(fe["frameworks"]) {
		feFw[obj(f)["key"].(string)] = len(list(obj(f)["items"]))
	}
	if feFw["dora"] != 4 || feFw["gdpr"] != 3 || len(feFw) != 2 {
		t.Fatalf("normative dell'applicazione FE: %v", feFw)
	}
	if rep := co.do("GET", "/api/v1/grc/scopes/application/REP", nil, http.StatusOK); len(list(rep["frameworks"])) != 0 {
		t.Fatalf("REP non è soggetta a requisiti per applicazione: %v", rep["frameworks"])
	}
	// I fatti dell'applicazione portano le normative.
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "normative: dora", "element": feApp}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatto normative dell'applicazione: %v", chk["facts"])
	}
	// Regole a mano: una regola non valida non passa; la sostituzione ricalcola.
	co.do("PUT", "/api/v1/grc/rules", map[string]any{"items": []any{map[string]any{"key": "x", "name": "X", "framework": "dora", "refs": []string{"Art. 999"},
		"scope": "application", "condition": ""}}}, http.StatusUnprocessableEntity)
	ruleItems := list(rules["items"])
	ruleItems = append(ruleItems, map[string]any{"key": "interno_critiche", "name": "Riesame dei rischi per le applicazioni critiche",
		"framework": "interno", "refs": []string{"POL-2"}, "scope": "application", "condition": "criticita: alta, critica"})
	rs := co.do("PUT", "/api/v1/grc/rules", map[string]any{"items": stripRuleViews(ruleItems)}, http.StatusOK)
	if len(list(rs["items"])) != 4 {
		t.Fatalf("regole salvate: %v", rs)
	}

	// --- Controlli: uno copre requisiti di tre normative.
	ownerID := owner.member
	owner.do("POST", "/api/v1/grc/controls", map[string]any{"title": "X"}, http.StatusForbidden)
	co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Senza requisito valido", "requirements": []any{map[string]any{"ref": "dora:Art. 99"}}}, http.StatusUnprocessableEntity)
	co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Raggruppa", "requirements": []any{map[string]any{"ref": "nis2:Art. 21"}}}, http.StatusUnprocessableEntity)
	backup := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Backup e prove di ripristino", "text": "Backup giornalieri cifrati e prova trimestrale.",
		"owner_id": ownerID, "frequency": "quarterly", "kind": "corrective", "automation": "semi",
		"requirements": []any{map[string]any{"ref": "dora:Art. 12"}, map[string]any{"ref": "nis2:Art. 21(2)(c)"}, map[string]any{"ref": "iso27001:A.8.13"}}}, http.StatusCreated)
	bkKey := backup["key"].(string)
	if backup["phase"] != "Bozza" || len(list(backup["requirements"])) != 3 || backup["owner"] != "owner" {
		t.Fatalf("controllo creato: %v", backup)
	}
	bkEl := admin.do("GET", "/api/v1/elements/"+bkKey, nil, http.StatusOK)
	bkID := obj(bkEl["element"])["id"].(string)
	// Senza responsabile il gate blocca la messa in esercizio.
	noOwner := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Accessi privilegiati", "requirements": []any{map[string]any{"ref": "iso27001:A.8.2"}}}, http.StatusCreated)
	noOwnerEl := admin.do("GET", "/api/v1/elements/"+noOwner["key"].(string), nil, http.StatusOK)
	co.do("POST", "/api/v1/elements/"+obj(noOwnerEl["element"])["id"].(string)+"/transitions", map[string]any{"to": "In esercizio"}, http.StatusUnprocessableEntity)
	owner.do("POST", "/api/v1/elements/"+bkID+"/transitions", map[string]any{"to": "In esercizio"}, http.StatusForbidden) // solo la compliance
	co.do("POST", "/api/v1/elements/"+bkID+"/transitions", map[string]any{"to": "In esercizio"}, http.StatusOK)
	// Controllo per la continuità delle applicazioni critiche: coperto solo da verifiche di progetto.
	cont := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Continuità delle funzioni critiche", "owner_id": ownerID, "frequency": "annual",
		"requirements": []any{map[string]any{"ref": "dora:Art. 11"}}}, http.StatusCreated)
	contKey := cont["key"].(string)
	contEl := admin.do("GET", "/api/v1/elements/"+contKey, nil, http.StatusOK)
	co.do("POST", "/api/v1/elements/"+obj(contEl["element"])["id"].(string)+"/transitions", map[string]any{"to": "In esercizio"}, http.StatusOK)
	// Controllo dei rilasci: si iscrive al collettore DevSecOps.
	rel := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Rilasci approvati", "owner_id": ownerID, "frequency": "monthly",
		"collectors": []string{"devsecops.release"}, "requirements": []any{map[string]any{"ref": "iso27001:A.8.32"}}}, http.StatusCreated)
	relEl := admin.do("GET", "/api/v1/elements/"+rel["key"].(string), nil, http.StatusOK)
	co.do("POST", "/api/v1/elements/"+obj(relEl["element"])["id"].(string)+"/transitions", map[string]any{"to": "In esercizio"}, http.StatusOK)

	reqStatus := func(fw, ref, scope string) string {
		t.Helper()
		cov := aud.do("GET", "/api/v1/grc/frameworks/"+fw+"/coverage", nil, http.StatusOK)
		for _, it := range list(cov["items"]) {
			if obj(it)["ref"] != ref {
				continue
			}
			for _, p := range list(obj(it)["pairs"]) {
				sc := obj(obj(p)["scope"])
				if sc["kind"] == scope || sc["key"] == scope {
					return obj(p)["status"].(string)
				}
			}
		}
		return ""
	}
	if s := reqStatus("nis2", "Art. 21(2)(c)", "tenant"); s != "no_evidence" {
		t.Fatalf("NIS2 21(2)(c) prima dell'evidenza: %s", s)
	}
	if s := reqStatus("nis2", "Art. 21(2)(a)", "tenant"); s != "no_control" {
		t.Fatalf("NIS2 21(2)(a) senza controllo: %s", s)
	}
	if s := reqStatus("dora", "Art. 12", feApp); s != "no_evidence" {
		t.Fatalf("DORA Art. 12 su FE prima dell'evidenza: %s", s)
	}
	if s := reqStatus("dora", "Art. 12", "tenant"); s != "" {
		t.Fatalf("DORA Art. 12 vale solo per le applicazioni critiche: %s", s)
	}

	// --- Evidenze: il responsabile carica, un'altra persona valida e firma.
	ed.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{bkKey}, "kind": "note", "title": "X", "note": "x"}, http.StatusForbidden)
	owner.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{bkKey}, "kind": "link", "title": "Senza link"}, http.StatusUnprocessableEntity)
	ev := owner.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{bkKey}, "kind": "attachment", "title": "Verbale della prova di ripristino",
		"note": "Prova del trimestre: ripristino in 52 minuti."}, http.StatusCreated)
	evKey := ev["key"].(string)
	if ev["status"] != "draft" || ev["can"].(map[string]any)["validate"] != false {
		t.Fatalf("evidenza creata: %v", ev)
	}
	owner.do("GET", "/api/v1/grc/controls/"+bkKey, nil, http.StatusOK) // il responsabile vede il suo controllo
	ed.do("GET", "/api/v1/grc/controls/"+bkKey, nil, http.StatusForbidden)
	owner.do("POST", "/api/v1/grc/evidence/"+evKey+"/validate", map[string]any{}, http.StatusForbidden)
	co.do("POST", "/api/v1/grc/evidence/"+evKey+"/validate", map[string]any{}, http.StatusUnprocessableEntity) // manca l'allegato
	owner.upload("/api/v1/elements/"+evKey+"/attachments", "verbale-ripristino.txt", []byte("Ripristino completato in 52 minuti"), "", http.StatusCreated)
	val := co.do("POST", "/api/v1/grc/evidence/"+evKey+"/validate", map[string]any{}, http.StatusOK)
	if sig, _ := val["signature"].(string); len(sig) != 64 || val["status"] != "valid" || val["signature_ok"] != true || val["validated_by"] != "co" {
		t.Fatalf("evidenza validata: %v", val)
	}
	// Validità di default: la frequenza trimestrale del controllo.
	until, _ := time.Parse(time.RFC3339, val["valid_until"].(string))
	if d := time.Until(until); d < 85*24*time.Hour || d > 95*24*time.Hour {
		t.Fatalf("validità dell'evidenza: %v", until)
	}
	// Chi carica non valida.
	self := co.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{bkKey}, "kind": "note", "title": "Nota della compliance", "note": "Controllo a campione."}, http.StatusCreated)
	co.do("POST", "/api/v1/grc/evidence/"+self["key"].(string)+"/validate", map[string]any{}, http.StatusUnprocessableEntity)
	co2.do("POST", "/api/v1/grc/evidence/"+self["key"].(string)+"/reject", map[string]any{"note": "Serve il campione allegato"}, http.StatusOK)

	// Un'evidenza vale una volta sola per tre normative.
	if s := reqStatus("nis2", "Art. 21(2)(c)", "tenant"); s != "covered" {
		t.Fatalf("NIS2 21(2)(c) dopo l'evidenza: %s", s)
	}
	if s := reqStatus("iso27001", "A.8.13", "tenant"); s != "covered" {
		t.Fatalf("ISO A.8.13 dopo l'evidenza: %s", s)
	}
	if s := reqStatus("dora", "Art. 12", feApp); s != "covered" {
		t.Fatalf("DORA Art. 12 su FE con l'evidenza del tenant: %s", s)
	}
	// La firma rende visibile una modifica successiva.
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update elements set title = 'Verbale modificato' where id = (select id from elements where number = $1 and space_id = (select id from spaces where key = 'GRC'))",
			mustNumber(evKey))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if v := aud.do("GET", "/api/v1/grc/evidence/"+evKey, nil, http.StatusOK); v["signature_ok"] != false {
		t.Fatalf("firma dopo la modifica: %v", v["signature_ok"])
	}

	// Efficacia parziale: la copertura diventa parziale.
	co.do("POST", "/api/v1/grc/controls/"+bkKey+"/assessment", map[string]any{"effectiveness": "partial"}, http.StatusUnprocessableEntity) // serve la nota
	co.do("POST", "/api/v1/grc/controls/"+bkKey+"/assessment", map[string]any{"effectiveness": "partial", "note": "Prova senza i sistemi on-premise"}, http.StatusOK)
	if s := reqStatus("iso27001", "A.8.13", "tenant"); s != "partial" {
		t.Fatalf("copertura con efficacia parziale: %s", s)
	}
	co.do("POST", "/api/v1/grc/controls/"+bkKey+"/assessment", map[string]any{"effectiveness": "effective"}, http.StatusOK)

	// Evidenza con scadenza vicina: in scadenza, poi scaduta.
	soon := time.Now().AddDate(0, 0, 10).Format(time.DateOnly)
	ev2 := owner.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{rel["key"].(string)}, "kind": "link", "title": "Registro dei rilasci di ottobre",
		"url": "https://ci.example.test/rilasci/ottobre", "valid_until": soon}, http.StatusCreated)
	co.do("POST", "/api/v1/grc/evidence/"+ev2["key"].(string)+"/validate", map[string]any{}, http.StatusOK)
	dash := aud.do("GET", "/api/v1/grc/dashboard", nil, http.StatusOK)
	if !strings.Contains(jsonString(dash["expiring"]), ev2["key"].(string)) {
		t.Fatalf("evidenza in scadenza non segnalata: %v", dash["expiring"])
	}
	if s := reqStatus("iso27001", "A.8.32", "tenant"); s != "covered" {
		t.Fatalf("ISO A.8.32 con l'evidenza in scadenza: %s", s)
	}
	clock.Store(int64(15 * 24 * time.Hour))
	if s := reqStatus("iso27001", "A.8.32", "tenant"); s != "no_evidence" {
		t.Fatalf("ISO A.8.32 dopo la scadenza: %s", s)
	}
	if l := list(aud.do("GET", "/api/v1/grc/evidence?status=expired", nil, http.StatusOK)["items"]); !strings.Contains(jsonString(l), ev2["key"].(string)) {
		t.Fatalf("evidenza scaduta: %v", l)
	}
	clock.Store(0)

	// --- Evidenze automatiche (aggancio DevSecOps): idempotenti, collegate al controllo iscritto.
	ed.do("POST", "/api/v1/grc/evidence/automatic", map[string]any{"collector": "devsecops.release", "external_ref": "r1"}, http.StatusForbidden)
	bot.do("POST", "/api/v1/grc/evidence/automatic", map[string]any{"collector": "devsecops.release"}, http.StatusUnprocessableEntity)
	auto := bot.do("POST", "/api/v1/grc/evidence/automatic", map[string]any{"collector": "devsecops.release", "external_ref": "fe-2.4.0-prod",
		"title": "Rilascio 2.4.0 del frontend in produzione", "url": "https://ci.example.test/rilasci/fe-2.4.0", "application": "FE"}, http.StatusCreated)
	if auto["created"] != true || len(list(auto["controls"])) != 1 || list(auto["controls"])[0] != rel["key"] {
		t.Fatalf("evidenza automatica: %v", auto)
	}
	if again := bot.do("POST", "/api/v1/grc/evidence/automatic", map[string]any{"collector": "devsecops.release", "external_ref": "fe-2.4.0-prod"}, http.StatusOK); again["key"] != auto["key"] || again["created"] != false {
		t.Fatalf("invio ripetuto: %v", again)
	}
	if v := aud.do("GET", "/api/v1/grc/evidence/"+auto["key"].(string), nil, http.StatusOK); v["status"] != "valid" || v["signature_ok"] != true || v["kind"] != "release" {
		t.Fatalf("evidenza automatica nel dettaglio: %v", v)
	}

	// --- Requisiti di progetto → requisiti normativi: la verifica diventa evidenza.
	dm, reqU, sponsor, lead, sec1, arch1 := u("dm"), u("req"), u("sponsor"), u("lead"), u("sec1"), u("arch1")
	br := reqU.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Area clienti con pagamenti", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	impFE := dm.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps["FE"]["id"], "estimate_effort_days": 10}, http.StatusCreated)
	feImp := impFE["key"].(string)
	reqU.do("POST", "/api/v1/elements/"+brID+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	dm.do("POST", "/api/v1/elements/"+brID+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
	dm.do("POST", "/api/v1/elements/"+brID+"/transitions", map[string]any{"to": "Valutazione"}, http.StatusOK)
	findReq := func(title string) (string, string) {
		t.Helper()
		for _, r := range list(dm.do("GET", "/api/v1/elements/"+brKey+"/requirements", nil, http.StatusOK)["rows"]) {
			x := obj(r)
			if strings.HasPrefix(x["title"].(string), title) {
				return x["key"].(string), x["id"].(string)
			}
		}
		t.Fatalf("requisito %q non trovato", title)
		return "", ""
	}
	// Dal catalogo: RES-01 dichiara iso27001:A.8.13 e dora:Art. 12.
	res01, res01ID := findReq("Backup e ripristino provati")
	lk := aud.do("GET", "/api/v1/grc/project-requirements/"+res01+"/links", nil, http.StatusOK)
	if !strings.Contains(jsonString(lk["catalog"]), "dora:Art. 12") || obj(list(lk["catalog"])[0])["found"] != true {
		t.Fatalf("collegamenti dalla voce del catalogo: %v", lk)
	}
	// A mano: un requisito di applicazione collegato a DORA Art. 11.
	mr := dm.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Piano di continuità del frontend provato", "scope": "application",
		"impact": feImp, "verifier_team": "sicurezza"}, http.StatusCreated)
	manKey, manID := obj(mr["requirement"])["key"].(string), obj(mr["requirement"])["id"].(string)
	owner.do("PUT", "/api/v1/grc/project-requirements/"+manKey+"/links", map[string]any{"refs": []string{"dora:Art. 11"}}, http.StatusForbidden)
	co.do("PUT", "/api/v1/grc/project-requirements/"+manKey+"/links", map[string]any{"refs": []string{"dora:Art. 77"}}, http.StatusUnprocessableEntity)
	co.do("PUT", "/api/v1/grc/project-requirements/"+manKey+"/links", map[string]any{"refs": []string{"dora:Art. 11"}}, http.StatusOK)
	if s := reqStatus("dora", "Art. 11", feApp); s != "no_evidence" {
		t.Fatalf("DORA Art. 11 su FE prima della verifica: %s", s)
	}
	cell := func(key string) string { return "/api/v1/requirements/" + key + "/cells/" + feImp }
	lead.do("POST", "/api/v1/elements/"+manID+"/transitions", map[string]any{"to": "Accettato"}, http.StatusOK)
	lead.do("POST", cell(manKey)+"/ready", map[string]any{"note": "Prova di continuità eseguita"}, http.StatusOK)
	sec1.do("POST", cell(manKey)+"/verify", map[string]any{"outcome": "verified", "note": "Verbale della prova controllato"}, http.StatusOK)
	lead.do("POST", "/api/v1/elements/"+res01ID+"/transitions", map[string]any{"to": "Accettato"}, http.StatusOK)
	lead.do("POST", cell(res01)+"/ready", map[string]any{"note": "Ripristino provato"}, http.StatusOK)
	arch1.do("POST", cell(res01)+"/verify", map[string]any{"outcome": "verified", "note": "Tempi di ripristino nei limiti"}, http.StatusOK)
	if s := reqStatus("dora", "Art. 11", feApp); s != "covered" {
		t.Fatalf("DORA Art. 11 su FE dopo la verifica del requisito di progetto: %s", s)
	}
	cv := co.do("GET", "/api/v1/grc/controls/"+contKey, nil, http.StatusOK)
	if !strings.Contains(jsonString(cv["evidence"]), manKey) || !strings.Contains(jsonString(cv["evidence"]), `"derived":true`) {
		t.Fatalf("evidenza dalla verifica nel controllo: %v", cv["evidence"])
	}
	if bv := co.do("GET", "/api/v1/grc/controls/"+bkKey, nil, http.StatusOK); !strings.Contains(jsonString(bv["evidence"]), res01) {
		t.Fatalf("verifica di RES-01 come evidenza del controllo dei backup: %v", bv["evidence"])
	}

	// --- Nuova versione di NIS2: mappature e abilitazione passano per riferimento.
	co.do("POST", "/api/v1/grc/frameworks/nis2/draft", map[string]any{"version": "2022/2555-r1"}, http.StatusCreated)
	co.do("PUT", "/api/v1/grc/frameworks/nis2/draft/items", map[string]any{"ref": "Art. 24", "parent": "Capo IV", "kind": "article",
		"title": "Uso degli schemi europei di certificazione"}, http.StatusOK)
	co.do("DELETE", "/api/v1/grc/frameworks/nis2/draft/items?ref="+urlq("Art. 21(2)(a)"), nil, http.StatusOK)
	pub := co.do("POST", "/api/v1/grc/frameworks/nis2/publish", nil, http.StatusOK)
	if obj(pub["current"])["version"] != "2022/2555-r1" || pub["status"] != "active" || len(list(pub["versions"])) != 2 {
		t.Fatalf("nuova versione di NIS2: %v", pub)
	}
	if s := reqStatus("nis2", "Art. 21(2)(c)", "tenant"); s != "covered" {
		t.Fatalf("NIS2 21(2)(c) dopo la nuova versione: %s", s)
	}
	if !strings.Contains(jsonString(co.do("GET", "/api/v1/grc/mappings?framework=nis2", nil, http.StatusOK)["items"]), "dora:Art. 11") {
		t.Fatal("corrispondenze perse con la nuova versione")
	}
	// Le versioni pubblicate non si modificano, nemmeno nel database.
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update requirements set title = 'x' where ref = 'Art. 9'")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "pubblicata") {
		t.Fatalf("requisito pubblicato modificato: %v", err)
	}

	// --- Non applicabilità decisa a mano, con motivazione.
	dashBefore := aud.do("GET", "/api/v1/grc/dashboard", nil, http.StatusOK)
	var art45 string
	for _, it := range list(aud.do("GET", "/api/v1/grc/frameworks/dora/coverage", nil, http.StatusOK)["items"]) {
		if obj(it)["ref"] == "Art. 45" {
			art45 = obj(it)["id"].(string)
		}
	}
	co.do("PUT", "/api/v1/grc/requirements/"+art45+"/applicability", map[string]any{"items": []any{map[string]any{"scope": "tenant", "status": "not_applicable"}}}, http.StatusUnprocessableEntity)
	rd := co.do("PUT", "/api/v1/grc/requirements/"+art45+"/applicability", map[string]any{"items": []any{map[string]any{"scope": "tenant", "status": "not_applicable",
		"justification": "Nessuna adesione a comunità di condivisione"}}}, http.StatusOK)
	if rd["status"] != "not_applicable" || len(list(rd["decisions"])) != 1 {
		t.Fatalf("decisione di non applicabilità: %v", rd)
	}
	doraOf := func(dash map[string]any) map[string]any {
		for _, f := range list(dash["frameworks"]) {
			if obj(f)["key"] == "dora" {
				return obj(f)
			}
		}
		return nil
	}
	if b, a := doraOf(dashBefore), doraOf(aud.do("GET", "/api/v1/grc/dashboard", nil, http.StatusOK)); a["applicable"].(float64) != b["applicable"].(float64)-1 ||
		a["not_applicable"].(float64) != 1 {
		t.Fatalf("cruscotto DORA prima %v dopo %v", b, a)
	}
	// Cruscotto: per normativa e per applicazione.
	dash = aud.do("GET", "/api/v1/grc/dashboard", nil, http.StatusOK)
	dora := doraOf(dash)
	if dora["percent"].(float64) <= 0 || dora["without_control"].(float64) == 0 || dora["requirements"] != 20.0 {
		t.Fatalf("DORA nel cruscotto: %v", dora)
	}
	foundFE := false
	for _, sc := range list(dash["scopes"]) {
		if obj(sc)["key"] == feApp && len(list(obj(sc)["frameworks"])) >= 2 {
			foundFE = true
		}
	}
	if !foundFE {
		t.Fatalf("FE nel cruscotto per applicazione: %v", dash["scopes"])
	}

	// --- Pacchetto per l'audit: CSV delle mappature, evidenze, allegati e impronte.
	status, hdr, body = aud.get("/api/v1/grc/audit-pack?framework=dora")
	if status != http.StatusOK || hdr.Get("Content-Type") != "application/zip" {
		t.Fatalf("pacchetto per l'audit: %d %s", status, body)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	if !strings.Contains(files["mappature.csv"], "dora;Art. 12;") || !strings.Contains(files["mappature.csv"], bkKey) ||
		!strings.Contains(files["evidenze.csv"], evKey) || files["evidenze/"+evKey+"/verbale-ripristino.txt"] != "Ripristino completato in 52 minuti" ||
		!strings.Contains(files["LEGGIMI.txt"], "mappature.csv") || !strings.Contains(files["copertura.json"], `"generated_at"`) {
		t.Fatalf("contenuto del pacchetto: %v", keysOf(files))
	}
	ed.do("GET", "/api/v1/grc/audit-pack", nil, http.StatusForbidden)
	var exports int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'grc.audit_pack.export'`, ten.TenantID).Scan(&exports); err != nil || exports != 1 {
		t.Fatalf("audit dell'export: %d %v", exports, err)
	}

	// --- Export e import del pacchetto JSON della configurazione.
	status, _, body = co.get("/api/v1/grc/export")
	var pkg map[string]any
	if status != http.StatusOK || json.Unmarshal(body, &pkg) != nil || pkg["format"] != "regia.grc/v1" || len(list(pkg["catalogs"])) != 7 || len(list(pkg["rules"])) != 4 {
		t.Fatalf("export della configurazione: %d %v", status, keysOf(map[string]string{}))
	}

	// --- Isolamento tra tenant.
	slug2 := slug + "b"
	ten2, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug2, Name: "Altro", AdminEmail: "admin@" + slug2 + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	other := login(t, srv.URL, "admin@"+slug2+".test", testPassword)
	if l := other.do("GET", "/api/v1/grc/library", nil, http.StatusOK); len(list(l["frameworks"])) != 0 {
		t.Fatalf("cataloghi dell'altro tenant visibili: %v", l["frameworks"])
	}
	other.do("GET", "/api/v1/grc/controls/"+bkKey, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/grc/requirements/"+art45, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/grc/frameworks/dora", nil, http.StatusNotFound)
	if d2 := other.do("GET", "/api/v1/grc/dashboard", nil, http.StatusOK); len(list(d2["frameworks"])) != 0 {
		t.Fatalf("cruscotto dell'altro tenant: %v", d2)
	}
	// L'altro tenant importa DORA: è una copia sua, indipendente.
	other.do("POST", "/api/v1/grc/starters/dora", nil, http.StatusCreated)
	err = d.InTenant(ctx, ten2.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from frameworks").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("frameworks visibili all'altro tenant: %d", n)
		}
		// Scrivere nei requisiti del primo tenant è impedito dalla RLS.
		_, err := tx.Exec(ctx, "insert into requirements (version_id, ref, title) select id, 'Z', 'Z' from framework_versions where id = $1",
			obj(fws["dora"]["current"])["id"])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.Owner.QueryRow(ctx, "select count(*) from requirements where ref = 'Z'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("requisito scritto nel catalogo di un altro tenant: %d %v", n, err)
	}
}

// stripRuleViews toglie dalle regole lette i campi della vista (id, matches, missing).
func stripRuleViews(items []any) []any {
	out := []any{}
	for _, it := range items {
		m := map[string]any{}
		for k, v := range it.(map[string]any) {
			if k != "id" && k != "matches" && k != "missing" {
				m[k] = v
			}
		}
		out = append(out, m)
	}
	return out
}

func mustNumber(key string) int {
	_, n, _ := strings.Cut(key, "-")
	v := 0
	for _, c := range n {
		v = v*10 + int(c-'0')
	}
	return v
}

func keysOf(m map[string]string) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
