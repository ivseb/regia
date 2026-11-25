package itest

import (
	"context"
	"fmt"
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
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Requisiti, matrice, verifica, deroghe e gate di rilascio (piano, passi 5 e 6): baseline
// applicate per condizione, prescrizione accettata → requisito, ambito di progetto e di
// applicazione, matrice con filtri e CSV, separazione dei compiti nella verifica e
// nell'approvazione delle deroghe, scadenza delle deroghe, fatti e gate dell'impatto, API
// di gate con un token, versioni dei cataloghi, isolamento tra tenant.
func TestRequirements(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("req-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Requisiti", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	// Il referente del team Web è anche nel gruppo Sicurezza: verificatore e realizzatore
	// insieme, per provare la separazione dei compiti.
	add("lead", tenant.MemberInput{Roles: []string{"viewer", "assessor"}, Groups: []string{"Sicurezza"}})
	add("lead2", tenant.MemberInput{Roles: []string{"viewer", "assessor"}})
	add("sec1", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Sicurezza"}})
	add("sec2", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Sicurezza"}})
	add("arch1", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Architetture"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	var clock atomic.Int64
	setDay := func(s string) {
		day, _ := time.Parse(time.DateOnly, s)
		clock.Store(day.Add(9 * time.Hour).Unix())
	}
	setDay("2026-12-01")
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	rev.Now, rq.Now = now, now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, req, man, sponsor, lead, lead2 := u("admin"), u("req"), u("dm"), u("sponsor"), u("lead"), u("lead2")
	sec1, sec2, arch1 := u("sec1"), u("sec2"), u("arch1")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Pacchetto di partenza: catalogo con tre baseline, livelli di rischio, politica,
	// regola delle baseline sulla domanda e gate di rilascio sull'impatto.
	cfg := admin.do("GET", "/api/v1/requirement-config", nil, http.StatusOK)
	cats := list(cfg["catalogs"])
	if len(cats) != 1 || len(list(obj(cats[0])["baselines"])) != 3 || len(list(obj(cfg["settings"])["risk_levels"])) != 4 ||
		len(list(obj(cfg["policy"])["rules"])) != 2 || cfg["can_edit"] != true {
		t.Fatalf("configurazione di partenza: %v", cfg)
	}
	impProc := obj(admin.do("GET", "/api/v1/processes/impact_standard", nil, http.StatusOK)["current"])
	gated := false
	for _, tr := range list(impProc["transitions"]) {
		if obj(tr)["to"] == "Rilasciato" && strings.Contains(jsonString(obj(tr)["gates"]), "requisiti") {
			gated = true
		}
	}
	if !gated {
		t.Fatalf("gate di rilascio mancante sull'impatto: %v", impProc["transitions"])
	}
	if acts := admin.do("GET", "/api/v1/processes", nil, http.StatusOK)["actions"]; !strings.Contains(jsonString(acts), "apply_baseline") {
		t.Fatalf("azione apply_baseline non registrata: %v", acts)
	}
	// Un'azione apply_baseline con un catalogo inesistente non passa la validazione.
	dproc := obj(admin.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)["current"])
	dproc["rules"] = append(list(dproc["rules"]), map[string]any{"key": "prova", "name": "Prova", "on": "enter:Triage",
		"actions": []any{map[string]any{"kind": "apply_baseline", "ref": "inesistente"}}})
	if dr := admin.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": dproc}, http.StatusOK); !strings.Contains(jsonString(dr["draft_problems"]), "inesistente") {
		t.Fatalf("azione non validata: %v", dr["draft_problems"])
	}
	admin.do("DELETE", "/api/v1/processes/demand_standard/draft", nil, http.StatusNoContent)

	// --- Catalogo applicazioni, team, richiesta con due impatti.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Dati", "lead_id": lead2.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"FE;Frontend area clienti;alta;sì;personali;Web;Area clienti\n" +
		"REP;Reportistica;bassa;no;aggregati;Dati;Area clienti\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)["id"].(string)
	}
	// Anteprima della condizione di una baseline sulle applicazioni.
	prev := list(admin.do("POST", "/api/v1/requirement-config/preview", map[string]any{"condition": "criticita: alta, critica", "scope": "application"}, http.StatusOK)["items"])
	if len(prev) != 2 || obj(prev[0])["code"] != "FE" || obj(prev[0])["matched"] != true || obj(prev[1])["matched"] != false ||
		obj(obj(prev[0])["facts"])["criticita"] == "" {
		t.Fatalf("anteprima della condizione: %v", prev)
	}
	admin.do("POST", "/api/v1/requirement-config/preview", map[string]any{"condition": "criticita:", "scope": "application"}, http.StatusUnprocessableEntity)

	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Area clienti con pagamenti", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	impact := func(code string) map[string]any {
		return man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps[code], "estimate_effort_days": 10}, http.StatusCreated)
	}
	fe, rep := impact("FE"), impact("REP")
	feKey, repKey := fe["key"].(string), rep["key"].(string)
	move := func(u *user, id, to string, status int, comment ...string) map[string]any {
		body := map[string]any{"to": to}
		if len(comment) > 0 {
			body["comment"] = comment[0]
		}
		return u.do("POST", "/api/v1/elements/"+id+"/transitions", body, status)
	}
	matrix := func(u *user, ref, query string) map[string]any {
		return u.do("GET", "/api/v1/elements/"+ref+"/requirements"+query, nil, http.StatusOK)
	}
	if n := len(list(matrix(man, brKey, "")["rows"])); n != 0 {
		t.Fatalf("requisiti prima della Valutazione: %d", n)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)

	// --- Baseline per condizione: FE (criticità alta) riceve base + sicurezza alta, REP solo
	// la base; la richiesta tocca una funzione critica e riceve la baseline di progetto.
	type row struct {
		id, key, scope, impact, origin string
	}
	rowsOf := func(m map[string]any) map[string]row {
		out := map[string]row{}
		for _, r := range list(m["rows"]) {
			x := obj(r)
			imp, _ := x["impact_key"].(string)
			title := x["title"].(string)
			out[title+"/"+imp] = row{x["id"].(string), x["key"].(string), x["scope"].(string), imp, x["origin"].(string)}
		}
		return out
	}
	m := matrix(man, brKey, "")
	rows := rowsOf(m)
	if len(rows) != 13 || len(list(m["columns"])) != 2 {
		t.Fatalf("matrice dopo le baseline: %d righe, %d colonne: %v", len(rows), len(list(m["columns"])), rows)
	}
	count := func(scope, imp string) int {
		n := 0
		for _, r := range rows {
			if r.scope == scope && r.impact == imp {
				n++
			}
		}
		return n
	}
	if count("application", feKey) != 8 || count("application", repKey) != 3 || count("project", "") != 2 {
		t.Fatalf("requisiti per ambito: FE %d, REP %d, progetto %d", count("application", feKey), count("application", repKey), count("project", ""))
	}
	sum := obj(m["summary"])
	if sum["requisiti"] != 13.0 || sum["requisiti_aperti"] != 13.0 || sum["pronto_al_rilascio"] != false {
		t.Fatalf("riepilogo della richiesta: %v", sum)
	}
	// Un requisito di progetto ha una cella per ogni impatto.
	for _, r := range list(m["rows"]) {
		x := obj(r)
		if x["scope"] == "project" && (list(x["cells"])[0] == nil || list(x["cells"])[1] == nil) {
			t.Fatalf("requisito di progetto senza tutte le celle: %v", x)
		}
	}
	// Le baseline non si ripetono.
	if c := man.do("POST", "/api/v1/elements/"+brKey+"/requirements/baseline", map[string]any{"catalog": "sicurezza_applicativa"}, http.StatusOK); len(list(c["created"])) != 0 {
		t.Fatalf("baseline ripetuta: %v", c["created"])
	}
	req.do("POST", "/api/v1/elements/"+brKey+"/requirements/baseline", map[string]any{"catalog": "sicurezza_applicativa"}, http.StatusForbidden)
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "requisiti: 10 requisiti_aperti: 10 pronto_al_rilascio: no", "element": feKey}, http.StatusOK)
	if chk["result"] != true {
		t.Fatalf("fatti dell'impatto FE (8 di applicazione e 2 celle di progetto): %v", chk["facts"])
	}

	// --- Prescrizioni accettate → requisiti: security review del frontend con esito «con prescrizioni».
	revs := man.do("GET", "/api/v1/elements/"+feKey+"/reviews", nil, http.StatusOK)
	secKey := ""
	for _, it := range list(revs["items"]) {
		if obj(it)["type_key"] == "security_review" {
			secKey = obj(it)["key"].(string)
		}
	}
	if secKey == "" {
		t.Fatalf("security review del frontend non aperta: %v", revs)
	}
	save := func(u *user, answers map[string]any) map[string]any {
		return u.do("PUT", "/api/v1/reviews/"+secKey+"/answers", map[string]any{"answers": answers}, http.StatusOK)
	}
	save(lead, map[string]any{"esposto_internet": true, "autenticazione": "nessuna"})
	v := save(lead, map[string]any{"cifratura_riposo": false, "segreti": "config", "terze_parti": true, "elenco_terze_parti": "Servizio SMS"})
	if m := list(obj(v["evaluation"])["missing"]); len(m) != 0 {
		t.Fatalf("domande mancanti: %v", m)
	}
	secID := obj(v["element"])["id"].(string)
	move(lead, secID, "Inviata al team", http.StatusOK)
	move(sec1, secID, "In esame", http.StatusOK)
	v = sec1.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusOK)
	accepted := 0
	for _, p := range list(v["prescriptions"]) {
		status := "rejected"
		if k, _ := obj(p)["rule_key"].(string); k == "waf" || k == "segreti" {
			status = "accepted"
			accepted++
		}
		sec1.do("PATCH", "/api/v1/reviews/"+secKey+"/prescriptions/"+obj(p)["id"].(string), map[string]any{"status": status}, http.StatusOK)
	}
	if accepted != 2 {
		t.Fatalf("prescrizioni proposte: %v", v["prescriptions"])
	}
	sec1.do("POST", "/api/v1/reviews/"+secKey+"/decision", map[string]any{"outcome": "favorevole_con_prescrizioni", "note": "Ok con le prescrizioni"}, http.StatusOK)
	m = matrix(man, feKey, "")
	presc := []row{}
	for _, r := range rowsOf(m) {
		if r.origin == "prescription" {
			presc = append(presc, r)
		}
	}
	if len(presc) != 2 || presc[0].scope != "application" || presc[0].impact != feKey {
		t.Fatalf("requisiti dalle prescrizioni: %v", presc)
	}
	pv := lead.do("GET", "/api/v1/requirements/"+presc[0].key, nil, http.StatusOK)
	pinfo := obj(pv["requirement"])
	if pinfo["review_key"] != secKey || pinfo["verifier_team_key"] != "sicurezza" || pinfo["prescription"] == nil || pinfo["stage"] != "proposed" {
		t.Fatalf("origine del requisito: %v", pinfo)
	}
	if links := lead.do("GET", "/api/v1/elements/"+presc[0].key+"/links", nil, http.StatusOK); !strings.Contains(jsonString(links["relations"]), secKey) {
		t.Fatalf("collegamento alla valutazione d'origine: %v", links)
	}

	// --- Inserimento manuale: di progetto e di applicazione (dalla richiesta, indicando l'impatto).
	req.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "X", "verifier_team": "sicurezza"}, http.StatusForbidden)
	man.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Senza verificatore"}, http.StatusUnprocessableEntity)
	man.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Impatto mancante", "scope": "application", "verifier_team": "sicurezza"}, http.StatusUnprocessableEntity)
	mp := man.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Manuale operativo aggiornato", "text": "Il manuale operativo descrive avvio, arresto e ripristino.",
		"category": "Documentazione", "scope": "project", "verifier_team": "architetture"}, http.StatusCreated)
	manualProject := obj(mp["requirement"])["key"].(string)
	if len(list(mp["cells"])) != 2 || obj(mp["requirement"])["origin"] != "manual" {
		t.Fatalf("requisito di progetto manuale: %v", mp)
	}
	ma := man.do("POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "Report senza dati personali in chiaro", "category": "Dati",
		"scope": "application", "impact": repKey, "verifier_team": "sicurezza", "verification_criterion": "Campione di report controllato"}, http.StatusCreated)
	manualApp := obj(ma["requirement"])["key"].(string)
	if len(list(ma["cells"])) != 1 || obj(list(ma["cells"])[0])["impact_key"] != repKey {
		t.Fatalf("requisito di applicazione manuale: %v", ma)
	}
	// Un impatto aggiunto dopo riceve le celle dei requisiti di progetto.
	if mm := matrix(man, brKey, ""); len(list(mm["rows"])) != 17 {
		t.Fatalf("righe dopo gli inserimenti: %d", len(list(mm["rows"])))
	}

	// --- Filtri e CSV.
	if mm := matrix(man, brKey, "?team=Web"); len(list(mm["columns"])) != 1 || len(list(mm["rows"])) != 13 {
		t.Fatalf("filtro per team: %d colonne, %d righe", len(list(mm["columns"])), len(list(mm["rows"])))
	}
	if mm := matrix(man, brKey, "?category=Continuit%C3%A0"); len(list(mm["rows"])) != 2 {
		t.Fatalf("filtro per categoria: %v", rowsOf(mm))
	}
	status, hdr, body := man.get("/api/v1/elements/" + brKey + "/requirements.csv?team=Dati")
	if status != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !strings.HasPrefix(string(body), "\xEF\xBB\xBFRequisito;Titolo") ||
		!strings.Contains(string(body), repKey+" Reportistica") || strings.Contains(string(body), "Frontend") {
		t.Fatalf("CSV della matrice: %d %v\n%s", status, hdr, body)
	}

	// --- Verifica e separazione dei compiti sul requisito SEC-05 del frontend.
	reqKey := func(title, imp string) row {
		t.Helper()
		for k, r := range rowsOf(matrix(man, brKey, "")) {
			if strings.HasPrefix(k, title+"/") && r.impact == imp {
				return r
			}
		}
		t.Fatalf("requisito %q su %q non trovato", title, imp)
		return row{}
	}
	tls := reqKey("Cifratura dei dati in transito", feKey)
	cellPath := func(r row, imp string) string { return "/api/v1/requirements/" + r.key + "/cells/" + imp }
	// Proposto: prima va accettato.
	if r := lead.do("POST", cellPath(tls, feKey)+"/ready", map[string]any{}, http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "accettato") {
		t.Fatalf("pronta prima dell'accettazione: %v", r)
	}
	move(sec1, tls.id, "Accettato", http.StatusForbidden)
	move(lead, tls.id, "Accettato", http.StatusOK)
	sec1.do("POST", cellPath(tls, feKey)+"/ready", map[string]any{}, http.StatusForbidden)
	rv := lead.do("POST", cellPath(tls, feKey)+"/ready", map[string]any{"note": "TLS 1.3 sul bilanciatore"}, http.StatusOK)
	if obj(rv["element"])["state"] != "Da verificare" || obj(list(rv["cells"])[0])["status"] != "to_verify" {
		t.Fatalf("dopo «pronta per la verifica»: %v %v", obj(rv["element"])["state"], rv["cells"])
	}
	// Il referente del team è nel gruppo dei verificatori, ma ha realizzato: non verifica.
	lv := lead.do("GET", "/api/v1/requirements/"+tls.key, nil, http.StatusOK)
	if c := obj(obj(list(lv["cells"])[0])["can"]); c["verify"] != false || c["why_not"] == nil {
		t.Fatalf("permessi del realizzatore: %v", c)
	}
	if r := lead.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified", "note": "ok"}, http.StatusForbidden); !strings.Contains(r["detail"].(string), "separazione dei compiti") {
		t.Fatalf("separazione dei compiti: %v", r)
	}
	for _, it := range list(lead.do("GET", "/api/v1/requirements/inbox", nil, http.StatusOK)["items"]) {
		if obj(it)["requirement_key"] == tls.key {
			t.Fatal("il realizzatore ha la cella nella sua inbox di verifica")
		}
	}
	if n := len(list(sec2.do("GET", "/api/v1/requirements/inbox", nil, http.StatusOK)["items"])); n != 1 {
		t.Fatalf("inbox del verificatore: %d", n)
	}
	arch1.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified", "note": "ok"}, http.StatusForbidden)
	sec1.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified"}, http.StatusUnprocessableEntity)
	sec1.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified", "links": []string{"javascript:alert(1)"}}, http.StatusUnprocessableEntity)
	att := sec1.upload("/api/v1/elements/"+tls.key+"/attachments", "scansione-tls.txt", []byte("TLSv1.3 OK su tutti gli endpoint"), "", http.StatusCreated)
	vv := sec1.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified", "note": "Configurazione controllata",
		"links": []string{"https://scanner.example.test/report/42"}, "attachments": []string{att["id"].(string)}}, http.StatusOK)
	if obj(vv["element"])["state"] != "Verificato" || obj(list(vv["cells"])[0])["status"] != "verified" {
		t.Fatalf("dopo la verifica: %v %v", obj(vv["element"])["state"], vv["cells"])
	}
	ver := obj(list(vv["verifications"])[0])
	if sig, _ := ver["signature"].(string); len(sig) != 64 || ver["by"] != "sec1" || len(list(ver["attachments"])) != 1 || len(list(ver["links"])) != 1 {
		t.Fatalf("verifica firmata: %v", ver)
	}
	// Verifica non superata: la cella torna da realizzare.
	arc01 := reqKey("Architettura documentata", feKey)
	move(lead, arc01.id, "Accettato", http.StatusOK)
	lead.do("POST", cellPath(arc01, feKey)+"/ready", map[string]any{}, http.StatusOK)
	arch1.do("POST", cellPath(arc01, feKey)+"/verify", map[string]any{"outcome": "rejected"}, http.StatusUnprocessableEntity)
	rj := arch1.do("POST", cellPath(arc01, feKey)+"/verify", map[string]any{"outcome": "rejected", "note": "Manca il flusso verso i pagamenti"}, http.StatusOK)
	if obj(rj["element"])["state"] != "In realizzazione" || obj(list(rj["cells"])[0])["status"] != "open" {
		t.Fatalf("dopo la verifica non superata: %v %v", obj(rj["element"])["state"], rj["cells"])
	}
	// Le verifiche firmate non si modificano, nemmeno nel database.
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update requirement_verifications set outcome = 'verified'")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "non si modificano") {
		t.Fatalf("verifica modificata: %v", err)
	}
	var audits int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'requirement.verify' and payload ? 'signature'`, ten.TenantID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit delle verifiche: %d %v", audits, err)
	}

	// --- Deroga con approvazione: chi chiede non approva.
	mfa := reqKey("Autenticazione a più fattori per gli accessi amministrativi", feKey)
	exBody := func(risk, expires string) map[string]any {
		return map[string]any{"impacts": []string{feKey}, "reason": "Il secondo fattore arriva con la migrazione all'IdP", "risk_level": risk,
			"expires_on": expires, "compensating": "Accesso amministrativo solo da VPN con registrazione delle sessioni"}
	}
	lead.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("inesistente", "2027-01-15"), http.StatusUnprocessableEntity)
	lead.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("medio", "2026-11-30"), http.StatusUnprocessableEntity)
	lead.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("medio", "2028-06-30"), http.StatusUnprocessableEntity)
	arch1.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("medio", "2027-01-15"), http.StatusForbidden)
	ex := lead.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("medio", "2027-01-15"), http.StatusCreated)
	exKey := ex["key"].(string)
	if obj(ex["element"])["state"] != "In approvazione" || ex["stage"] != "pending" || ex["risk_name"] != "Medio" {
		t.Fatalf("deroga chiesta: %v", ex)
	}
	lead.do("POST", "/api/v1/requirements/"+mfa.key+"/exceptions", exBody("basso", "2027-01-10"), http.StatusUnprocessableEntity) // già una in corso
	stepOf := func(u *user, key string) map[string]any {
		det := u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)
		for _, a := range list(det["approvals"]) {
			if obj(a)["status"] != "pending" {
				continue
			}
			for _, st := range list(obj(a)["steps"]) {
				if obj(st)["decision"] == "pending" {
					return obj(st)
				}
			}
		}
		t.Fatalf("nessun passo di approvazione attivo su %s", key)
		return nil
	}
	st := stepOf(lead, exKey)
	if st["can_decide"] != false {
		t.Fatalf("chi chiede la deroga la potrebbe approvare: %v", st)
	}
	for _, it := range list(lead.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"]) {
		if obj(it)["element_key"] == exKey {
			t.Fatal("la deroga è nell'inbox di chi l'ha chiesta")
		}
	}
	if r := lead.do("POST", "/api/v1/approvals/steps/"+st["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden); !strings.Contains(r["detail"].(string), "separazione dei compiti") {
		t.Fatalf("auto-approvazione: %v", r)
	}
	if obj(stepOf(sec2, exKey))["can_decide"] != true {
		t.Fatal("il verificatore non può approvare la deroga")
	}
	sec2.do("POST", "/api/v1/approvals/steps/"+st["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	exv := lead.do("GET", "/api/v1/exceptions/"+exKey, nil, http.StatusOK)
	if exv["stage"] != "approved" || obj(exv["element"])["state"] != "In vigore" {
		t.Fatalf("deroga approvata: %v", exv)
	}
	mv := lead.do("GET", "/api/v1/requirements/"+mfa.key, nil, http.StatusOK)
	if obj(mv["element"])["state"] != "In deroga" || obj(list(mv["cells"])[0])["status"] != "waived" || len(list(mv["exceptions"])) != 1 {
		t.Fatalf("requisito in deroga: %v %v", obj(mv["element"])["state"], mv["cells"])
	}
	// Rischio alto: dopo il verificatore decide anche lo sponsor della richiesta.
	sca := reqKey("Nessuna vulnerabilità critica o alta correggibile nelle dipendenze", feKey)
	ex2 := lead.do("POST", "/api/v1/requirements/"+sca.key+"/exceptions", exBody("alto", "2027-02-01"), http.StatusCreated)
	ex2Key := ex2["key"].(string)
	sec1.do("POST", "/api/v1/approvals/steps/"+stepOf(sec1, ex2Key)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if s2 := stepOf(sponsor, ex2Key); s2["can_decide"] != true {
		t.Fatalf("secondo passo dello sponsor: %v", s2)
	} else {
		sponsor.do("POST", "/api/v1/approvals/steps/"+s2["id"].(string)+"/decision", map[string]any{"decision": "rejected", "comment": "Rischio non accettabile"}, http.StatusNoContent)
	}
	if e := lead.do("GET", "/api/v1/exceptions/"+ex2Key, nil, http.StatusOK); e["stage"] != "rejected" || obj(e["element"])["state"] != "Respinta" {
		t.Fatalf("deroga respinta: %v", e)
	}

	// --- Scadenza: preavviso una volta sola, poi la deroga torna aperta con le celle.
	expiry := func(day string) requirements.ExpiryReport {
		setDay(day)
		var rep requirements.ExpiryReport
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			rep, err = rq.CheckExpiryTenant(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	if r := expiry("2026-12-20"); r.Notices != 0 || r.Expired != 0 {
		t.Fatalf("preavviso troppo presto: %+v", r)
	}
	if r := expiry("2027-01-05"); r.Notices != 1 {
		t.Fatalf("preavviso a 10 giorni: %+v", r)
	}
	if r := expiry("2027-01-06"); r.Notices != 0 {
		t.Fatalf("preavviso ripetuto: %+v", r)
	}
	if r := expiry("2027-01-16"); r.Expired != 1 {
		t.Fatalf("scadenza: %+v", r)
	}
	if e := lead.do("GET", "/api/v1/exceptions/"+exKey, nil, http.StatusOK); e["stage"] != "expired" || obj(e["element"])["state"] != "Scaduta" {
		t.Fatalf("deroga scaduta: %v", e)
	}
	mv = lead.do("GET", "/api/v1/requirements/"+mfa.key, nil, http.StatusOK)
	if obj(mv["element"])["state"] != "In realizzazione" || obj(list(mv["cells"])[0])["status"] != "open" || obj(list(mv["cells"])[0])["exception_expired"] != true {
		t.Fatalf("requisito dopo la scadenza: %v %v", obj(mv["element"])["state"], mv["cells"])
	}
	notified := map[string]bool{}
	for _, n := range list(lead.do("GET", "/api/v1/notifications?limit=200", nil, http.StatusOK)["items"]) {
		if obj(n)["kind"] == "requirement" {
			notified[obj(obj(n)["payload"])["event"].(string)] = true
		}
	}
	if !notified["exception_expiring"] || !notified["exception_expired"] || !notified["exception_approved"] {
		t.Fatalf("notifiche delle deroghe: %v", notified)
	}
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "deroghe_scadute: 1 requisiti_in_deroga: 0", "element": feKey}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti dopo la scadenza: %v", chk["facts"])
	}
	setDay("2026-12-01")

	// --- Gate di rilascio dell'impatto REP: bloccato con i requisiti mancanti, poi libero.
	move(man, rep["id"].(string), "In analisi", http.StatusOK)
	move(man, rep["id"].(string), "Stimato", http.StatusOK)
	move(lead2, rep["id"].(string), "Accettato", http.StatusOK)
	move(lead2, rep["id"].(string), "In realizzazione", http.StatusOK)
	if r := move(lead2, rep["id"].(string), "Rilasciato", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "requisiti non verificati") ||
		!strings.Contains(r["detail"].(string), "Segreti fuori dal codice sorgente") || !strings.Contains(r["detail"].(string), "e altri 2") {
		t.Fatalf("gate di rilascio: %v", r)
	}
	det := lead2.do("GET", "/api/v1/elements/"+repKey, nil, http.StatusOK)
	blocked := false
	for _, tr := range list(det["transitions"]) {
		if obj(tr)["to"] == "Rilasciato" && strings.Contains(jsonString(obj(tr)["blocked"]), "requisiti") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("il passaggio a Rilasciato non risulta bloccato: %v", det["transitions"])
	}
	verifyCell := func(owner, verifier *user, r row, imp string) {
		t.Helper()
		if obj(owner.do("GET", "/api/v1/requirements/"+r.key, nil, http.StatusOK)["requirement"])["stage"] == "proposed" {
			move(owner, r.id, "Accettato", http.StatusOK)
		}
		owner.do("POST", cellPath(r, imp)+"/ready", map[string]any{}, http.StatusOK)
		verifier.do("POST", cellPath(r, imp)+"/verify", map[string]any{"outcome": "verified", "note": "Controllato"}, http.StatusOK)
	}
	verifyCell(lead2, sec1, reqKey("Segreti fuori dal codice sorgente", repKey), repKey)
	verifyCell(lead2, sec1, reqKey("Cifratura dei dati in transito", repKey), repKey)
	verifyCell(lead2, sec2, row{key: manualApp, id: obj(ma["element"])["id"].(string)}, repKey)
	move(lead2, reqKey("Architettura documentata", repKey).id, "Non applicabile", http.StatusUnprocessableEntity)
	move(lead2, reqKey("Architettura documentata", repKey).id, "Non applicabile", http.StatusOK, "Solo nuovi report, nessun componente nuovo")
	// Requisiti di progetto: il demand management li realizza, i verificatori verificano la cella di REP.
	verifyCell(man, arch1, row{key: manualProject, id: obj(mp["element"])["id"].(string)}, repKey)
	for _, title := range []string{"Fornitori ICT censiti nel registro", "Continuità della funzione critica provata"} {
		r := rowsOf(matrix(man, brKey, ""))[title+"/"]
		if r.key == "" {
			t.Fatalf("requisito di progetto %q non trovato", title)
		}
		move(man, r.id, "Accettato", http.StatusOK)
		man.do("POST", cellPath(r, repKey)+"/ready", map[string]any{}, http.StatusOK)
	}
	comp := rowsOf(matrix(man, brKey, ""))["Fornitori ICT censiti nel registro/"]
	// Verifica il team Compliance: nessuno dei membri di prova; l'amministratore non è nel gruppo.
	admin.do("POST", cellPath(comp, repKey)+"/verify", map[string]any{"outcome": "verified", "note": "x"}, http.StatusForbidden)
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "requisiti_da_verificare: 2 pronto_al_rilascio: no", "element": repKey}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti di REP prima della verifica di progetto: %v", chk["facts"])
	}
	// Una deroga sulle due celle di progetto mancanti, approvata dalla Compliance: si aggiunge
	// un membro al gruppo e si approva.
	add("comp", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Compliance"}})
	compUser := u("comp")
	for _, title := range []string{"Fornitori ICT censiti nel registro", "Continuità della funzione critica provata"} {
		r := rowsOf(matrix(man, brKey, ""))[title+"/"]
		compUser.do("POST", cellPath(r, repKey)+"/verify", map[string]any{"outcome": "verified", "note": "Registro aggiornato"}, http.StatusOK)
	}
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "pronto_al_rilascio: sì requisiti_aperti: 0 requisiti_da_verificare: 0 requisiti: 6", "element": repKey}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti di REP pronta al rilascio: %v", chk["facts"])
	}
	move(lead2, rep["id"].(string), "Rilasciato", http.StatusOK)
	if chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "pronto_al_rilascio: no requisiti: 16 requisiti_verificati: 4 deroghe_scadute: 1", "element": brKey}, http.StatusOK); chk["result"] != true {
		t.Fatalf("fatti della richiesta: %v", chk["facts"])
	}

	// --- Matrice a pagine (limit/offset): stesse righe, celle e riepilogo di quella intera, per
	// ogni filtro, a pagine di 3 righe (benchmark 2026-11-24: 300×30 celle in 681 ms e 1,3 MB).
	for _, filter := range []string{"", "?team=Web", "?category=Continuit%C3%A0", "?status=verified", "?status=waived", "?status=open", "?status=to_verify"} {
		full := matrix(man, brKey, filter)
		fullRows := list(full["rows"])
		paged := []any{}
		var last map[string]any
		for off := 0; ; off += 3 {
			sep := "?"
			if filter != "" {
				sep = "&"
			}
			last = matrix(man, brKey, fmt.Sprintf("%s%slimit=3&offset=%d", filter, sep, off))
			if got := int(num(last["total_rows"])); got != len(fullRows) {
				t.Fatalf("%q: total_rows %d, righe intere %d", filter, got, len(fullRows))
			}
			paged = append(paged, list(last["rows"])...)
			if last["more"] != true {
				break
			}
			if len(list(last["rows"])) != 3 {
				t.Fatalf("%q: pagina intermedia di %d righe", filter, len(list(last["rows"])))
			}
		}
		if jsonString(paged) != jsonString(fullRows) {
			t.Fatalf("%q: le pagine non ricompongono la matrice.\nintera: %s\npagine: %s", filter, jsonString(fullRows), jsonString(paged))
		}
		for _, k := range []string{"columns", "teams", "categories"} {
			if jsonString(last[k]) != jsonString(full[k]) {
				t.Fatalf("%q: %s diverso.\nintera: %s\npagine: %s", filter, k, jsonString(full[k]), jsonString(last[k]))
			}
		}
		// Riepilogo: uguale, tranne l'elenco dei mancanti che nelle pagine si ferma ai primi 25 con il totale.
		fs, ls := obj(full["summary"]), obj(last["summary"])
		fm := list(fs["mancanti"])
		wantTot := len(fm)
		gotTot := len(list(ls["mancanti"]))
		if tot := int(num(ls["mancanti_totale"])); tot > 0 {
			gotTot = tot
		}
		if gotTot != wantTot || len(list(ls["mancanti"])) != min(wantTot, 25) || jsonString(list(ls["mancanti"])) != jsonString(fm[:min(wantTot, 25)]) {
			t.Fatalf("%q: mancanti %d (totale %d) contro %d: %v", filter, len(list(ls["mancanti"])), gotTot, wantTot, ls["mancanti"])
		}
		delete(fs, "mancanti")
		delete(ls, "mancanti")
		delete(ls, "mancanti_totale")
		if jsonString(fs) != jsonString(ls) {
			t.Fatalf("%q: riepilogo diverso.\nintero: %s\npagine: %s", filter, jsonString(fs), jsonString(ls))
		}
	}

	// --- API di gate con un token API (solo release.read): risposta con i motivi, audit.
	lead.do("GET", "/api/v1/release-gate?application=FE&request="+brKey, nil, http.StatusForbidden)
	tok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Pipeline", "scopes": []string{"release.read"}}, http.StatusCreated)["token"].(string)
	g := bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate?application=FE&request="+brKey, nil, http.StatusOK)
	if g["allowed"] != false || len(list(g["reasons"])) != 1 || !strings.Contains(jsonString(g["reasons"]), "Fornitori ICT censiti") || len(list(obj(obj(list(g["impacts"])[0])["summary"])["mancanti"])) != 12 {
		t.Fatalf("gate di FE: %v", g)
	}
	g = bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate?application=rep&request="+strings.ToLower(brKey), nil, http.StatusOK)
	if g["allowed"] != true || obj(list(g["impacts"])[0])["impact"] != repKey {
		t.Fatalf("gate di REP: %v", g)
	}
	// Senza la richiesta valgono gli impatti in corso: REP è rilasciato, FE no.
	if g = bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate?application=REP", nil, http.StatusOK); g["allowed"] != true || len(list(g["impacts"])) != 0 {
		t.Fatalf("gate di REP senza richiesta: %v", g)
	}
	if g = bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate?application="+apps["FE"], nil, http.StatusOK); g["allowed"] != false {
		t.Fatalf("gate di FE senza richiesta: %v", g)
	}
	bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate?application=INESISTENTE", nil, http.StatusNotFound)
	bearerDo(t, srv.URL, tok, "GET", "/api/v1/release-gate", nil, http.StatusUnprocessableEntity)
	bearerDo(t, srv.URL, tok, "POST", "/api/v1/elements/"+brKey+"/requirements", map[string]any{"title": "x"}, http.StatusForbidden)
	readTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	bearerDo(t, srv.URL, readTok, "GET", "/api/v1/release-gate?application=FE", nil, http.StatusForbidden)
	var gateAudits, withToken int
	if err := d.Owner.QueryRow(ctx, `select count(*), count(*) filter (where payload ? 'api_token') from audit_log
		where tenant_id = $1 and action = 'release_gate.check'`, ten.TenantID).Scan(&gateAudits, &withToken); err != nil || gateAudits != 4 || withToken != 4 {
		t.Fatalf("audit del gate: %d (%d con token) %v", gateAudits, withToken, err)
	}

	// --- Versioni del catalogo: bozza con errori bloccata, pubblicazione, immutabilità.
	cat := admin.do("GET", "/api/v1/requirement-config/catalogs/sicurezza_applicativa", nil, http.StatusOK)
	def := obj(obj(cat["current"])["definition"])
	def["baselines"] = append(list(def["baselines"]), map[string]any{"key": "nuova", "name": "Nuova", "scope": "application", "items": []any{"NON-ESISTE"}})
	dr := admin.do("PUT", "/api/v1/requirement-config/catalogs/sicurezza_applicativa/draft", map[string]any{"definition": def}, http.StatusOK)
	if !strings.Contains(jsonString(dr["draft_problems"]), "NON-ESISTE") {
		t.Fatalf("problemi della bozza: %v", dr["draft_problems"])
	}
	lead.do("PUT", "/api/v1/requirement-config/catalogs/sicurezza_applicativa/draft", map[string]any{"definition": def}, http.StatusForbidden)
	admin.do("POST", "/api/v1/requirement-config/catalogs/sicurezza_applicativa/publish", map[string]any{}, http.StatusUnprocessableEntity)
	bl := list(def["baselines"])
	obj(bl[len(bl)-1])["items"] = []any{"SEC-01"}
	admin.do("PUT", "/api/v1/requirement-config/catalogs/sicurezza_applicativa/draft", map[string]any{"definition": def}, http.StatusOK)
	if pub := admin.do("POST", "/api/v1/requirement-config/catalogs/sicurezza_applicativa/publish", map[string]any{"notes": "Baseline nuova"}, http.StatusOK); obj(pub["current"])["version"] != 2.0 {
		t.Fatalf("versione pubblicata: %v", pub["current"])
	}
	if info := obj(lead.do("GET", "/api/v1/requirements/"+tls.key, nil, http.StatusOK)["requirement"]); info["catalog_version"] != 1.0 || info["baseline_key"] != "base" {
		t.Fatalf("il requisito deve restare sulla versione 1: %v", info)
	}
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update requirement_catalogs set definition = '{}' where key = 'sicurezza_applicativa' and version = 1")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "non si può modificare") {
		t.Fatalf("versione pubblicata del catalogo modificata: %v", err)
	}
	// Impostazioni e politica: solo l'amministratore di processo; la politica vuole approvatori.
	lead.do("PUT", "/api/v1/requirement-config/settings", map[string]any{"risk_levels": []any{}, "max_exception_days": 30, "expiry_notice_days": 5}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/requirement-config/exception-policy", map[string]any{"rules": []any{map[string]any{"when": map[string]any{}, "steps": []any{}}}}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/requirement-config/exception-policy", map[string]any{"rules": []any{map[string]any{"when": map[string]any{},
		"steps": []any{map[string]any{"approvers": []any{map[string]any{"role": "ruolo_inventato"}}}}}}}, http.StatusUnprocessableEntity)
	exp := admin.do("GET", "/api/v1/requirement-config/export", nil, http.StatusOK)
	if exp["format"] != "regia.requirements/v1" || len(list(exp["catalogs"])) != 1 || len(list(exp["exception_policy"])) != 2 {
		t.Fatalf("export: %v", exp)
	}

	// --- Isolamento tra tenant.
	slugB := slug + "b"
	tenB, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	other := login(t, srv.URL, "admin@"+slugB+".test", testPassword)
	other.do("GET", "/api/v1/requirements/"+tls.key, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/requirements/"+tls.id, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/elements/"+brID+"/requirements", nil, http.StatusNotFound)
	other.do("POST", cellPath(tls, feKey)+"/verify", map[string]any{"outcome": "verified", "note": "x"}, http.StatusNotFound)
	other.do("GET", "/api/v1/exceptions/"+exKey, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/release-gate?application=FE", nil, http.StatusNotFound)
	if n := len(list(other.do("GET", "/api/v1/requirements/inbox", nil, http.StatusOK)["items"])); n != 0 {
		t.Fatalf("inbox dell'altro tenant: %d", n)
	}
	if err := d.InTenant(ctx, tenB.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select (select count(*) from ext_requirement) + (select count(*) from applicability)
			+ (select count(*) from requirement_verifications) + (select count(*) from ext_exception)`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("dati dei requisiti visibili da un altro tenant: %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
