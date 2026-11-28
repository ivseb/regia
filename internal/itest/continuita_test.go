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
	"regia/internal/modules/knowledge"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Continuità operativa (docs/sviluppo/continuita.md): preparazione del modulo, BIA con proposta e
// versione congelata, RTO richiesto ereditato dalle capability, incoerenze lungo le dipendenze e
// i fornitori, piani, test con evidenza GRC e rilievi che diventano lavoro, fatti, configurazione
// versionata, permessi.
func TestContinuita(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()

	slug := strings.ReplaceAll("cont-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Continuità", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision, grc.Provision, knowledge.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]tenant.MemberInput{
		"bcm":  {Roles: []string{"continuity_manager"}},
		"view": {Roles: []string{"viewer"}},
	} {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	bcm := login(t, srv.URL, "bcm@"+slug+".test", testPassword)
	view := login(t, srv.URL, "view@"+slug+".test", testPassword)
	const base = "/api/v1/continuity"

	// --- Preparazione: il tenant esistente non ha ancora la continuità --------------
	if o := view.do("GET", base+"/overview", nil, http.StatusOK); o["ready"] != false {
		t.Fatalf("modulo non preparato: %v", o)
	}
	view.do("POST", base+"/setup", nil, http.StatusForbidden)
	bcm.do("POST", base+"/bia", map[string]any{"capability": "x"}, http.StatusUnprocessableEntity) // non pronto
	if r := admin.do("POST", base+"/setup", nil, http.StatusOK); r["ready"] != true {
		t.Fatalf("setup: %v", r)
	}
	admin.do("POST", base+"/setup", nil, http.StatusOK) // idempotente
	st := bcm.do("GET", base+"/settings", nil, http.StatusOK)
	cfg := cObj(st["config"])
	if st["ready"] != true || st["version"].(float64) != 1 || len(cList(cfg["categories"])) != 4 || len(cList(cfg["horizons"])) != 5 || len(cList(cfg["test_types"])) != 4 {
		t.Fatalf("configurazione di partenza: %s", jsonString(st))
	}

	// --- Catalogo: applicazioni, capability, dipendenze, fornitore -------------------
	csv := "codice;nome;criticita;funzione_critica;team;rto_minuti;rpo_minuti\n" +
		"HB;Home banking;critica;sì;Web;60;15\n" +
		"API;API conti;alta;no;Integrazioni;240;60\n" +
		"CORE;Core conti;critica;no;Core;1440;60\n" +
		"DWH;Data warehouse;media;no;Dati;;\n"
	if rep := admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK); rep["created"].(float64) != 4 {
		t.Fatalf("catalogo: %v", rep)
	}
	apps := map[string]map[string]any{}
	for _, a := range cList(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		m := cObj(a)
		apps[m["code"].(string)] = m
	}
	id := func(code string) string { return apps[code]["id"].(string) }
	key := func(code string) string { return apps[code]["key"].(string) }
	pay := admin.do("POST", "/api/v1/architecture/capabilities", map[string]any{"name": "Pagamenti", "code": "CAP-PAY"}, http.StatusCreated)
	bon := admin.do("POST", "/api/v1/architecture/capabilities", map[string]any{"name": "Bonifici", "code": "CAP-BON", "parent_id": pay["id"]}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/capabilities/CAP-BON/links", map[string]any{"element_id": id("HB")}, http.StatusNoContent)
	admin.do("POST", "/api/v1/architecture/capabilities/CAP-PAY/links", map[string]any{"element_id": id("API")}, http.StatusNoContent)
	admin.do("POST", "/api/v1/architecture/relations", map[string]any{"type": "calls_api", "source": id("HB"), "target": id("API")}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/relations", map[string]any{"type": "reads_data_from", "source": id("API"), "target": id("CORE")}, http.StatusCreated)
	prov := admin.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Nuvola Calcolo", "applications": []string{id("HB")}}, http.StatusCreated)
	contract := admin.do("POST", "/api/v1/ict/providers/"+prov["key"].(string)+"/contracts", map[string]any{"reference": "NC-1", "rto_minutes": 2880, "rpo_minutes": 60}, http.StatusCreated)
	if contract["rto_minutes"].(float64) != 2880 {
		t.Fatalf("RTO del contratto: %s", jsonString(contract))
	}

	// --- BIA: griglia, proposta, permessi, approvazione ------------------------------
	view.do("POST", base+"/bia", map[string]any{"capability": "CAP-BON"}, http.StatusForbidden)
	bcm.do("POST", base+"/bia", map[string]any{"capability": key("HB")}, http.StatusUnprocessableEntity) // non è una capability
	bia := bcm.do("POST", base+"/bia", map[string]any{"capability": "CAP-BON"}, http.StatusCreated)
	biaKey := bia["key"].(string)
	if bia["state"] != "Bozza" || bia["proposal"] != nil || cObj(bia["capability"])["id"] != bon["id"] {
		t.Fatalf("BIA nuova: %s", jsonString(bia))
	}
	bcm.do("POST", base+"/bia", map[string]any{"capability": "CAP-BON"}, http.StatusUnprocessableEntity) // già presente
	grid := map[string]any{}
	for _, c := range []string{"economico", "normativo", "reputazionale", "clienti"} {
		row := map[string]any{}
		for i, h := range []string{"1h", "4h", "1g", "3g", "1s"} {
			lv := i + 1
			if c == "economico" && h == "4h" {
				lv = 4 // intollerabile a 4 ore
			}
			if lv > 5 {
				lv = 5
			}
			row[h] = lv
		}
		grid[c] = row
	}
	bcm.do("PUT", base+"/bia/"+biaKey, map[string]any{"impacts": map[string]any{"economico": map[string]any{"1h": 9}}}, http.StatusUnprocessableEntity)
	// Senza RTO e RPO non si invia in revisione.
	bcm.do("PUT", base+"/bia/"+biaKey, map[string]any{"impacts": grid}, http.StatusOK)
	bcm.do("POST", "/api/v1/elements/"+bia["id"].(string)+"/transitions", map[string]any{"to": "In revisione"}, http.StatusUnprocessableEntity)
	b := bcm.do("PUT", base+"/bia/"+biaKey, map[string]any{"critical": true, "critical_reason": "Pagamenti dei clienti", "mtpd_minutes": 240, "rto_minutes": 60, "rpo_minutes": 15}, http.StatusOK)
	pr := cObj(b["proposal"])
	if pr["mtpd_minutes"].(float64) != 240 || pr["rto_minutes"].(float64) != 60 || pr["critical"] != true || pr["category"] != "economico" || pr["horizon"] != "4h" {
		t.Fatalf("proposta: %s", jsonString(pr))
	}
	if b["rto_minutes"].(float64) != 60 || b["approved"] != nil || cObj(b["can"])["edit"] != true {
		t.Fatalf("BIA in bozza: %s", jsonString(b))
	}
	bcm.do("POST", "/api/v1/elements/"+bia["id"].(string)+"/transitions", map[string]any{"to": "In revisione"}, http.StatusOK)
	bcm.do("PUT", base+"/bia/"+biaKey, map[string]any{"rto_minutes": 30}, http.StatusUnprocessableEntity) // bloccata in revisione
	// «Approva» è un passaggio di sistema: dal motore non si esegue, solo con continuity.approve.
	bcm.do("POST", "/api/v1/elements/"+bia["id"].(string)+"/transitions", map[string]any{"to": "Approvata"}, http.StatusUnprocessableEntity)
	view.do("POST", base+"/bia/"+biaKey+"/approve", nil, http.StatusForbidden)
	b = bcm.do("POST", base+"/bia/"+biaKey+"/approve", map[string]any{"comment": "Approvata dal comitato"}, http.StatusOK)
	if b["state"] != "Approvata" || cObj(b["approved"])["version"].(float64) != 1 || cObj(b["approved"])["rto_minutes"].(float64) != 60 || len(cList(b["versions"])) != 1 {
		t.Fatalf("BIA approvata: %s", jsonString(b))
	}
	if hb := findBy(cList(b["applications"]), "id", id("HB")); hb == nil || hb["rto_ok"] != true {
		t.Fatalf("applicazioni della BIA: %s", jsonString(b["applications"]))
	}

	// --- Incoerenze ------------------------------------------------------------------
	ov := view.do("GET", base+"/overview", nil, http.StatusOK)
	codes := issueCodes(ov)
	for _, want := range []string{
		"dependency_slower:" + id("HB") + ":" + id("API"),           // 1 h dipende da 4 h
		"dependency_slower:" + id("HB") + ":" + prov["id"].(string), // fornitore 48 h
		"plan_missing:" + id("HB"), "test_missing:" + id("HB"),
		"plan_missing:" + bon["id"].(string), "test_missing:" + bon["id"].(string),
		"dependency_slower:" + id("API") + ":" + id("CORE"), // 4 h dipende da 24 h
	} {
		if !codes[want] {
			t.Fatalf("manca l'incoerenza %s in %s", want, jsonString(ov["issues"]))
		}
	}
	if codes["rto_over_required:"+id("HB")] || codes["critical_not_declared:"+id("HB")] {
		t.Fatalf("incoerenze in più: %s", jsonString(ov["issues"]))
	}
	if s := cObj(ov["summary"]); s["bia_approved"].(float64) != 1 || s["critical_functions"].(float64) != 1 || s["high"].(float64) < 6 {
		t.Fatalf("riepilogo: %s", jsonString(s))
	}
	// L'RTO dichiarato peggiora: supera il richiesto.
	admin.do("PATCH", "/api/v1/architecture/applications/"+id("HB"), map[string]any{"rto_minutes": 120}, http.StatusOK)
	if !issueCodes(view.do("GET", base+"/overview?capability=CAP-PAY", nil, http.StatusOK))["rto_over_required:"+id("HB")] {
		t.Fatal("RTO oltre il richiesto non segnalato")
	}
	if n := len(cList(view.do("GET", base+"/overview?code=rto_over_required", nil, http.StatusOK)["issues"])); n != 1 {
		t.Fatalf("filtro per codice: %d", n)
	}
	app := view.do("GET", base+"/applications/"+key("HB"), nil, http.StatusOK)
	req := cObj(app["required"])
	if req["rto"].(float64) != 60 || cObj(req["source"])["key"] != biaKey || app["critical_required"] != true || len(cList(app["dependencies"])) != 2 {
		t.Fatalf("blocco dell'applicazione: %s", jsonString(app))
	}
	// Fatti per le condizioni.
	if f := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "rto_incoerente: sì rto_richiesto_minuti: 60 funzione_critica_bia: sì piano_continuita: no", "element": key("HB")}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti dell'applicazione: %s", jsonString(f))
	}
	if f := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "bia_critica: sì mtpd_minuti: 240", "element": "CAP-BON"}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti della capability: %s", jsonString(f))
	}
	g := view.do("GET", base+"/graph", nil, http.StatusOK)
	incoherent := false
	for _, e := range cList(g["edges"]) {
		m := cObj(e)
		if m["dependent"] == id("HB") && m["dependency"] == id("API") && m["incoherent"] == true {
			incoherent = true
		}
	}
	if !incoherent || cObj(cObj(g["nodes"])[id("HB")])["rto_required"].(float64) != 60 {
		t.Fatalf("livello della mappa: %s", jsonString(g))
	}

	// --- Dal problema all'azione -----------------------------------------------------
	ref := "dependency_slower:" + id("HB") + ":" + id("API")
	pc := bcm.do("GET", "/api/v1/problems/context?kind=continuity&ref="+ref, nil, http.StatusOK)
	if !strings.Contains(pc["title"].(string), "API conti") || cObj(pc["application"])["id"] != id("HB") || pc["team"] == nil {
		t.Fatalf("contesto del problema: %s", jsonString(pc))
	}
	w := bcm.do("POST", "/api/v1/problems/work-items", map[string]any{"kind": "continuity", "ref": ref}, http.StatusCreated)
	if !strings.HasPrefix(w["label"].(string), "Continuità") {
		t.Fatalf("lavoro creato: %s", jsonString(w))
	}

	// --- Piano di continuità ---------------------------------------------------------
	admin.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "BCP", "name": "Continuità"}, http.StatusCreated)
	page := admin.do("POST", "/api/v1/kb/spaces/BCP/pages", map[string]any{"title": "Piano di continuità dei pagamenti", "body": "Procedura."}, http.StatusCreated)
	pageKey := cObj(page["page"])["key"].(string)
	bcm.do("POST", base+"/plans", map[string]any{"page": key("HB"), "target": "CAP-BON"}, http.StatusUnprocessableEntity)
	plan := bcm.do("POST", base+"/plans", map[string]any{"page": pageKey, "target": "CAP-BON"}, http.StatusCreated)
	if plan["approved"] != false {
		t.Fatalf("piano non approvato: %s", jsonString(plan))
	}
	if !issueCodes(view.do("GET", base+"/overview", nil, http.StatusOK))["plan_missing:"+bon["id"].(string)] {
		t.Fatal("un piano non approvato non basta")
	}
	if _, err := d.Owner.Exec(ctx, `update kb_pages set approved_version = 1 where element_id = $1`, cObj(page["page"])["id"]); err != nil {
		t.Fatal(err)
	}
	codes = issueCodes(view.do("GET", base+"/overview", nil, http.StatusOK))
	if codes["plan_missing:"+bon["id"].(string)] || codes["plan_missing:"+id("HB")] {
		t.Fatal("il piano approvato sulla capability copre la capability e le sue applicazioni")
	}

	// --- Test di resilienza: esito, evidenza, rilievi --------------------------------
	ctrl := admin.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Test di continuità delle funzioni critiche", "collectors": []string{"continuity.test"}}, http.StatusCreated)
	bcm.do("POST", base+"/tests", map[string]any{"subject": key("HB"), "type": "inesistente", "planned_on": "2026-11-20"}, http.StatusUnprocessableEntity)
	bcm.do("POST", base+"/tests", map[string]any{"subject": biaKey, "type": "restore", "planned_on": "2026-11-20"}, http.StatusUnprocessableEntity)
	tst := bcm.do("POST", base+"/tests", map[string]any{"subject": key("HB"), "type": "restore", "planned_on": time.Now().Format(time.DateOnly)}, http.StatusCreated)
	tKey := tst["key"].(string)
	if tst["state"] != "Pianificato" || cObj(tst["required"])["rto"].(float64) != 60 {
		t.Fatalf("test nuovo: %s", jsonString(tst))
	}
	// Senza esito non si registra.
	bcm.do("POST", "/api/v1/elements/"+tst["id"].(string)+"/transitions", map[string]any{"to": "Eseguito"}, http.StatusUnprocessableEntity)
	bcm.do("PUT", base+"/tests/"+tKey, map[string]any{"executed_on": "2099-01-01"}, http.StatusUnprocessableEntity)
	tv := bcm.do("PUT", base+"/tests/"+tKey, map[string]any{"executed_on": time.Now().Format(time.DateOnly), "outcome": "failed",
		"rto_measured_minutes": 180, "rpo_measured_minutes": 10, "findings": []string{"Il failover del database richiede un intervento manuale", "  "}}, http.StatusOK)
	if tv["rto_ok"] != false || tv["rpo_ok"] != true || len(cList(tv["findings"])) != 1 {
		t.Fatalf("esito: %s", jsonString(tv))
	}
	bcm.do("POST", "/api/v1/elements/"+tst["id"].(string)+"/transitions", map[string]any{"to": "Eseguito"}, http.StatusOK)
	tv = bcm.do("GET", base+"/tests/"+tKey, nil, http.StatusOK)
	if tv["evidence"] == nil || tv["evidence_note"] != "" {
		t.Fatalf("evidenza del test: %s", jsonString(tv))
	}
	evKey := cObj(tv["evidence"])["key"].(string)
	ev := admin.do("GET", "/api/v1/grc/evidence/"+evKey, nil, http.StatusOK)
	if !strings.Contains(jsonString(ev), ctrl["key"].(string)) {
		t.Fatalf("evidenza collegata al controllo iscritto: %s", jsonString(ev))
	}
	codes = issueCodes(view.do("GET", base+"/overview", nil, http.StatusOK))
	if !codes["test_failed:"+id("HB")] || codes["test_missing:"+id("HB")] {
		t.Fatalf("test fallito: %v", codes)
	}
	fref := cObj(cList(tv["findings"])[0])["ref"].(string)
	fc := bcm.do("GET", "/api/v1/problems/context?kind=continuity&ref="+fref, nil, http.StatusOK)
	if !strings.Contains(fc["body"].(string), "failover del database") {
		t.Fatalf("contesto del rilievo: %s", jsonString(fc))
	}
	bcm.do("POST", "/api/v1/problems/work-items", map[string]any{"kind": "continuity", "ref": fref}, http.StatusCreated)
	tv = bcm.do("GET", base+"/tests/"+tKey, nil, http.StatusOK)
	if cObj(cList(tv["findings"])[0])["work"] == nil {
		t.Fatalf("lavoro del rilievo: %s", jsonString(tv))
	}
	plan2026 := view.do("GET", base+"/tests?year="+time.Now().Format("2006"), nil, http.StatusOK)
	row := findRow(cList(plan2026["rows"]), id("HB"))
	if row == nil || row["planned_in_year"] != true || cObj(row["last_done"])["outcome"] != "failed" {
		t.Fatalf("piano annuale: %s", jsonString(plan2026))
	}
	if f := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "ultimo_test_giorni: <=1", "element": key("HB")}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatto ultimo_test_giorni: %s", jsonString(f))
	}

	// --- Configurazione versionata ---------------------------------------------------
	bcm.do("PUT", base+"/settings", map[string]any{"version": 7, "config": cfg}, http.StatusConflict)
	cfg["test_max_age_days"] = 10
	bcm.do("PUT", base+"/settings", map[string]any{"version": 1, "config": cfg}, http.StatusUnprocessableEntity)
	cfg["test_max_age_days"] = 180
	view.do("PUT", base+"/settings", map[string]any{"version": 1, "config": cfg}, http.StatusForbidden)
	if s := bcm.do("PUT", base+"/settings", map[string]any{"version": 1, "config": cfg}, http.StatusOK); s["version"].(float64) != 2 {
		t.Fatalf("salvataggio: %v", s["version"])
	}
	if v := cList(view.do("GET", base+"/settings/versions", nil, http.StatusOK)["items"]); len(v) != 2 {
		t.Fatalf("storico: %d", len(v))
	}
	if b := view.do("GET", base+"/bia/"+biaKey, nil, http.StatusOK); b["config_changed"] != true {
		t.Fatal("la BIA dice che la configurazione è cambiata")
	}

	// --- Modulo spento: le API rispondono 404 ----------------------------------------
	admin.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]any{"continuity": false}}, http.StatusOK)
	view.do("GET", base+"/overview", nil, http.StatusNotFound)
	admin.do("PUT", "/api/v1/tenant/modules", map[string]any{"modules": map[string]any{"continuity": true}}, http.StatusOK)
}

func issueCodes(ov map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, it := range cList(ov["issues"]) {
		out[cObj(it)["ref"].(string)] = true
	}
	return out
}

func findBy(items []any, k, v string) map[string]any {
	for _, it := range items {
		if m := cObj(it); m[k] == v {
			return m
		}
	}
	return nil
}

func findRow(rows []any, id string) map[string]any {
	for _, r := range rows {
		if m := cObj(r); cObj(m["subject"])["id"] == id {
			return m
		}
	}
	return nil
}

func cList(v any) []any         { l, _ := v.([]any); return l }
func cObj(v any) map[string]any { m, _ := v.(map[string]any); return m }
