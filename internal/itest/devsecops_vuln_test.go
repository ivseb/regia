package itest

import (
	"context"
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
	"regia/internal/modules/devsecops"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// DevSecOps, fase 2: vulnerabilità come lavoro governato. SLA per gravità × criticità con i
// fattori DORA ed esposizione e l'anteprima, scadenza calcolata e team assegnato da solo,
// ciclo di vita (presa in carico, correzione, risolta), chiusura e riapertura automatiche
// dai report, eccezione con approvazione (separazione dei compiti), collegamento a un rischio,
// scadenza dell'eccezione, escalation una volta sola per passo con i destinatari della
// catena (referente, responsabile dell'unità, unità sopra), fatti per le condizioni,
// cruscotto, permessi e isolamento tra tenant.
func TestDevsecopsVulnerabilita(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, slugB := "vul-"+stamp, "vulb-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Vulnerabilità", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("dm", tenant.MemberInput{Roles: []string{"editor"}})
	add("lead", tenant.MemberInput{Roles: []string{"viewer"}})
	add("capo", tenant.MemberInput{Roles: []string{"viewer"}})
	add("direttore", tenant.MemberInput{Roles: []string{"viewer"}})
	add("lettore", tenant.MemberInput{Roles: []string{"viewer"}})
	add("rm", tenant.MemberInput{Roles: []string{"risk_manager"}, Groups: []string{"Risk management"}})
	add("ci", tenant.MemberInput{Roles: []string{"pipeline"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	rk := risk.Register(eng, ap)
	_ = wk
	dso := devsecops.Register(eng, nil)
	var clock atomic.Int64
	setTime := func(t time.Time) { clock.Store(t.Unix()) }
	base := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	setTime(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	dso.Now, rk.Now = now, now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Risk: rk, Devsecops: dso,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name, s string) *user { return login(t, srv.URL, name+"@"+s+".test", testPassword) }
	admin, dm, lead, capo, direttore, lettore, rm := u("admin", slug), u("dm", slug), u("lead", slug), u("capo", slug), u("direttore", slug), u("lettore", slug), u("rm", slug)
	adminB := u("admin", slugB)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	num := func(v any) int { f, _ := v.(float64); return int(f) }
	const V = "/api/v1/devsecops/vuln"

	// --- Catalogo: il team Ordini ha il referente `lead`, sta nell'unità Vendite (responsabile
	// `capo`) sotto la Direzione commerciale (responsabile `direttore`).
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini", "lead_id": lead.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Vetrina", "lead_id": dm.member}, http.StatusCreated)
	var parent, unit string
	if err := d.Owner.QueryRow(ctx, `insert into org_units (tenant_id, id, name, manager_id) values ($1, gen_random_uuid(), 'Direzione commerciale', $2) returning id::text`,
		ten.TenantID, direttore.member).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if err := d.Owner.QueryRow(ctx, `insert into org_units (tenant_id, id, name, parent_id, manager_id) values ($1, gen_random_uuid(), 'Vendite', $2, $3) returning id::text`,
		ten.TenantID, parent, capo.member).Scan(&unit); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Owner.Exec(ctx, "update teams set org_unit_id = $2 where tenant_id = $1 and name = 'Ordini'", ten.TenantID, unit); err != nil {
		t.Fatal(err)
	}
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"ORD;Gestione ordini;critica;sì;personali;Ordini;Commercio elettronico\n" +
		"WEB;Vetrina web;media;no;pubblici;Vetrina;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]map[string]any{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)
	}

	// --- Pipeline: due servizi, una release in produzione e un report con tre vulnerabilità
	// sull'applicazione critica DORA e due sulla vetrina.
	pipeTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	push := func(path string, body map[string]any, want int) map[string]any {
		t.Helper()
		body["format"] = "regia.devsecops/v1"
		return bearerDo(t, srv.URL, pipeTok, "POST", "/api/v1/devsecops/"+path, body, want)
	}
	orders := map[string]any{"name": "orders-api", "repository": "shop/orders-api", "application": "ORD"}
	web := map[string]any{"name": "shop-web", "repository": "shop/shop-web", "application": "WEB"}
	for _, svc := range []map[string]any{orders, web} {
		push("releases", map[string]any{"service": svc, "version": "1.4.0", "environment": "prod", "at": "2026-11-20T09:00:00Z"}, http.StatusAccepted)
	}
	match := func(id, sev, pkg, ver, fix string) map[string]any {
		v := map[string]any{"id": id, "severity": sev}
		if fix != "" {
			v["fix"] = map[string]any{"versions": []any{fix}, "state": "fixed"}
		}
		return map[string]any{"vulnerability": v, "artifact": map[string]any{"name": pkg, "version": ver, "type": "npm", "purl": "pkg:npm/" + pkg + "@" + ver}}
	}
	report := func(svc map[string]any, at string, matches ...any) {
		push("vuln-reports", map[string]any{"service": svc, "version": "1.4.0", "scanned_at": at, "report": map[string]any{"matches": matches}}, http.StatusAccepted)
	}
	crit := match("CVE-2026-31001", "Critical", "lodash", "4.17.20", "4.17.21")
	high := match("CVE-2026-31002", "High", "express", "4.19.2", "")
	med := match("CVE-2026-31003", "Medium", "moment", "2.29.0", "2.29.4")
	report(orders, "2026-11-20T08:10:00Z", crit, high, med)
	report(web, "2026-11-20T08:20:00Z", match("CVE-2026-31010", "Medium", "react", "18.2.0", "18.3.1"), match("CVE-2026-31011", "Low", "nginx", "1.25.0", "1.25.4"))

	items := func(u *user, query string) []map[string]any {
		var out []map[string]any
		for _, x := range list(u.do("GET", V+"/items?"+query, nil, http.StatusOK)["items"]) {
			out = append(out, obj(x))
		}
		return out
	}
	byCVE := func(u *user, cve string) map[string]any {
		t.Helper()
		for _, x := range items(u, "stage=all&q="+cve) {
			if x["cve"] == cve {
				return x
			}
		}
		t.Fatalf("vulnerabilità %s non trovata", cve)
		return nil
	}

	// --- Scadenza calcolata: critica × critica = 3 giorni, dimezzati per la funzione DORA = 2
	// (1,5 arrotondato), dalla prima rilevazione (20/11); alta × critica = 7 → 4. Il team è
	// quello proprietario dell'applicazione.
	c := byCVE(admin, "CVE-2026-31001")
	if num(c["sla_days"]) != 2 || !strings.HasPrefix(c["due_at"].(string), "2026-11-22") || c["overdue"] != true || c["team"] != "Ordini" || c["stage"] != "open" || c["dora"] != true {
		t.Fatalf("scadenza della critica DORA: %s", jsonString(c))
	}
	if h := byCVE(admin, "CVE-2026-31002"); num(h["sla_days"]) != 4 || h["fixed_in"] != nil {
		t.Fatalf("scadenza dell'alta: %s", jsonString(h))
	}
	if m := byCVE(admin, "CVE-2026-31003"); num(m["sla_days"]) != 15 || m["overdue"] == true {
		t.Fatalf("media su applicazione critica: %s", jsonString(m))
	}
	if w := byCVE(admin, "CVE-2026-31010"); num(w["sla_days"]) != 90 || w["team"] != "Vetrina" {
		t.Fatalf("media sulla vetrina: %s", jsonString(w))
	}
	// Bassa × media = 180 giorni.
	if l := byCVE(admin, "CVE-2026-31011"); num(l["sla_days"]) != 180 {
		t.Fatalf("bassa: %s", jsonString(l))
	}

	// --- Fatti per le condizioni: scadute 2 su ORD, in scadenza 0, eccezioni 0.
	check := func(cond, ref string) bool {
		return admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": cond, "element": ref}, http.StatusOK)["result"] == true
	}
	ordKey := apps["ORD"]["key"].(string)
	if !check("vuln_scadute: 2 vuln_in_scadenza: 1 vuln_eccezioni: 0", ordKey) {
		t.Fatalf("fatti di ORD: %v", admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "vuln_scadute: 2", "element": ordKey}, http.StatusOK)["facts"])
	}

	// --- Cruscotto di un tenant senza vulnerabilità (pilota Home lab): elenchi vuoti, mai null,
	// altrimenti la pagina va in errore.
	empty := adminB.do("GET", V+"/dashboard", nil, http.StatusOK)
	for _, k := range []string{"teams", "applications", "aging", "trend"} {
		if _, ok := empty[k].([]any); !ok {
			t.Fatalf("cruscotto vuoto: %q non è un elenco: %s", k, jsonString(empty))
		}
	}

	// --- Cruscotto: scadute, aperte, per team e applicazione, andamento.
	dash := admin.do("GET", V+"/dashboard", nil, http.StatusOK)
	k := obj(dash["kpis"])
	if num(k["open"]) != 5 || num(k["overdue"]) != 2 || num(k["critical_open"]) != 1 || len(list(dash["trend"])) != 12 || len(list(dash["aging"])) != 4 {
		t.Fatalf("cruscotto: %s", jsonString(dash))
	}
	// Una sola definizione di «critiche in produzione»: il KPI della pagina e il pannello DevSecOps
	// danno lo stesso numero (le critiche aperte su una versione non in produzione non contano).
	ov := obj(admin.do("GET", "/api/v1/devsecops/overview", nil, http.StatusOK)["kpis"])
	if prod := num(k["critical_prod"]); prod != num(obj(ov["critical_cves"])["now"]) || prod > num(k["critical_open"]) {
		t.Fatalf("critiche in produzione: pagina %v, pannello %v, aperte %v", k["critical_prod"], obj(ov["critical_cves"])["now"], k["critical_open"])
	}
	if teams := list(dash["teams"]); len(teams) != 2 || obj(teams[0])["name"] != "Ordini" || num(obj(teams[0])["overdue"]) != 2 {
		t.Fatalf("per team: %v", dash["teams"])
	}
	if n := len(items(admin, "due=overdue")); n != 2 {
		t.Fatalf("filtro delle scadute: %d", n)
	}
	if n := len(items(admin, "team="+obj(list(dash["filters"].(map[string]any)["teams"])[0])["id"].(string))); n == 0 {
		t.Fatalf("filtro per team vuoto")
	}

	// --- Politica: anteprima (nessun effetto), poi salvataggio che ricalcola.
	cfg := admin.do("GET", V+"/config", nil, http.StatusOK)
	pol := obj(cfg["policy"])
	if pol["default"] != true || len(list(pol["escalation"])) != 4 || cfg["can_edit"] != true {
		t.Fatalf("configurazione di partenza: %s", jsonString(cfg))
	}
	cand := map[string]any{}
	for k2, v := range pol {
		cand[k2] = v
	}
	m2 := map[string]any{}
	for sev, row := range obj(pol["matrix"]) {
		r := map[string]any{}
		for cr, v := range obj(row) {
			r[cr] = v
		}
		m2[sev] = r
	}
	obj(m2["critical"])["critical"], obj(m2["high"])["critical"] = 30, 30
	cand["matrix"] = m2
	pv := admin.do("POST", V+"/config/policy/preview", cand, http.StatusOK)
	if num(obj(pv["current"])["overdue"]) != 2 || num(obj(pv["proposed"])["overdue"]) != 0 || len(list(pv["cells"])) != 20 {
		t.Fatalf("anteprima: %s", jsonString(pv))
	}
	if c := byCVE(admin, "CVE-2026-31001"); num(c["sla_days"]) != 2 {
		t.Fatalf("l'anteprima non deve salvare: %v", c["sla_days"])
	}
	dm.do("PUT", V+"/config/policy", cand, http.StatusForbidden)
	dm.do("POST", V+"/config/policy/preview", cand, http.StatusForbidden)
	bad := map[string]any{}
	for k2, v := range cand {
		bad[k2] = v
	}
	bad["escalation"] = []any{map[string]any{"name": "A", "days": 5, "to": []string{"team"}}, map[string]any{"name": "B", "days": 5, "to": []string{"team"}}}
	admin.do("PUT", V+"/config/policy", bad, http.StatusUnprocessableEntity)
	// Esposizione: un campo dell'applicazione; qui solo la validazione del nome.
	bad["escalation"] = pol["escalation"]
	bad["exposure_field"] = "campo_che_non_c_e"
	admin.do("PUT", V+"/config/policy", bad, http.StatusUnprocessableEntity)
	saved := admin.do("PUT", V+"/config/policy", cand, http.StatusOK)
	if num(saved["recomputed"]) < 2 {
		t.Fatalf("ricalcolo alla modifica: %v", saved["recomputed"])
	}
	// critica × critica = 30, DORA 50% = 15 giorni: scade il 5/12, dopo l'1/12.
	if c := byCVE(admin, "CVE-2026-31001"); num(c["sla_days"]) != 15 || c["overdue"] == true {
		t.Fatalf("scadenza dopo la politica nuova: %s", jsonString(c))
	}
	// Si torna alla politica di partenza per il resto della prova (escalation e scadute).
	cand["matrix"] = pol["matrix"]
	admin.do("PUT", V+"/config/policy", cand, http.StatusOK)
	if c := byCVE(admin, "CVE-2026-31001"); num(c["sla_days"]) != 2 {
		t.Fatalf("ritorno alla politica di partenza: %s", jsonString(c))
	}

	// --- Ciclo di vita e azioni in blocco: permessi, presa in carico, correzione, assegnazione.
	idOf := func(cve string) string { return byCVE(admin, cve)["id"].(string) }
	critID, highID, medID, webMedID := idOf("CVE-2026-31001"), idOf("CVE-2026-31002"), idOf("CVE-2026-31003"), idOf("CVE-2026-31010")
	lettore.do("POST", V+"/bulk", map[string]any{"ids": []string{critID}, "action": "take"}, http.StatusForbidden)
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{}, "action": "take"}, http.StatusUnprocessableEntity)
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{critID}, "action": "spiegami"}, http.StatusUnprocessableEntity)
	res := dm.do("POST", V+"/bulk", map[string]any{"ids": []string{critID, highID}, "action": "take"}, http.StatusOK)
	if num(res["changed"]) != 2 {
		t.Fatalf("presa in carico: %v", res)
	}
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "taken" || c["assignee_id"] != dm.member {
		t.Fatalf("presa in carico: %s", jsonString(c))
	}
	// Di nuovo «prendi in carico» non cambia niente; «in correzione» sì.
	if res := dm.do("POST", V+"/bulk", map[string]any{"ids": []string{critID}, "action": "take"}, http.StatusOK); num(res["changed"]) != 0 || num(res["skipped"]) != 1 {
		t.Fatalf("seconda presa in carico: %v", res)
	}
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{critID}, "action": "start"}, http.StatusOK)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "in_progress" {
		t.Fatalf("in correzione: %s", jsonString(c))
	}
	// Assegnazione a un altro team: il team scelto a mano non torna più a quello dell'applicazione
	// finché non si chiede «auto».
	var vetrinaID string
	for _, tm := range list(cfg["teams"]) {
		if obj(tm)["name"] == "Vetrina" {
			vetrinaID = obj(tm)["id"].(string)
		}
	}
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{highID}, "action": "assign", "team": vetrinaID}, http.StatusOK)
	if h := byCVE(admin, "CVE-2026-31002"); h["team"] != "Vetrina" || h["team_manual"] != true {
		t.Fatalf("team scelto a mano: %s", jsonString(h))
	}
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{highID}, "action": "assign", "team": "auto"}, http.StatusOK)
	if h := byCVE(admin, "CVE-2026-31002"); h["team"] != "Ordini" || h["team_manual"] != false {
		t.Fatalf("team di nuovo automatico: %s", jsonString(h))
	}
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{medID}, "action": "false_positive"}, http.StatusUnprocessableEntity)
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{medID}, "action": "false_positive", "note": "Il modulo non è raggiungibile"}, http.StatusOK)
	if m := byCVE(admin, "CVE-2026-31003"); m["stage"] != "false_positive" {
		t.Fatalf("falso positivo: %s", jsonString(m))
	}
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{medID}, "action": "reopen"}, http.StatusOK)
	det := admin.do("GET", V+"/items/"+critID, nil, http.StatusOK)
	if evs := list(det["events"]); len(evs) < 2 || (obj(evs[0])["kind"] != "start" && obj(evs[1])["kind"] != "start") {
		t.Fatalf("storia: %v", det["events"])
	}

	// --- Eccezione: senza politica non si chiede; con la politica approva `lead` (e non chi chiede).
	until := base.AddDate(0, 0, 30).Format(time.DateOnly)
	exc := map[string]any{"ids": []string{webMedID}, "reason": "Componente non raggiungibile dall'esterno", "until": until}
	dm.do("POST", V+"/exceptions", exc, http.StatusUnprocessableEntity)
	rules := []any{map[string]any{"when": map[string]any{"risk_lte": 4}, "steps": []any{map[string]any{"approvers": []any{map[string]any{"member": lead.member}}}}}}
	dm.do("PUT", V+"/config/exception-policy", map[string]any{"rules": rules}, http.StatusForbidden)
	admin.do("PUT", V+"/config/exception-policy", map[string]any{"rules": []any{}}, http.StatusUnprocessableEntity)
	admin.do("PUT", V+"/config/exception-policy", map[string]any{"rules": []any{map[string]any{"when": map[string]any{"risk_lte": 4},
		"steps": []any{map[string]any{"approvers": []any{map[string]any{"member": "00000000-0000-0000-0000-000000000000"}}}}}}}, http.StatusUnprocessableEntity)
	admin.do("PUT", V+"/config/exception-policy", map[string]any{"rules": rules}, http.StatusOK)
	lettore.do("POST", V+"/exceptions", exc, http.StatusForbidden)
	dm.do("POST", V+"/exceptions", map[string]any{"ids": []string{webMedID}, "reason": "", "until": until}, http.StatusUnprocessableEntity)
	dm.do("POST", V+"/exceptions", map[string]any{"ids": []string{webMedID}, "reason": "x", "until": base.AddDate(0, 0, 400).Format(time.DateOnly)}, http.StatusUnprocessableEntity)
	dm.do("POST", V+"/exceptions", map[string]any{"ids": []string{webMedID}, "reason": "x", "until": "2026-11-01"}, http.StatusUnprocessableEntity)
	// Un rischio del registro a cui collegarla.
	rk1 := rm.do("POST", "/api/v1/risks", map[string]any{"title": "Dipendenza vulnerabile nella vetrina", "category": "Terze parti", "inherent_likelihood": 3, "inherent_impact": 3}, http.StatusCreated)
	riskKey := rk1["key"].(string)
	exc["risk"] = riskKey
	created := dm.do("POST", V+"/exceptions", exc, http.StatusCreated)
	ex := obj(list(created["items"])[0])
	if ex["status"] != "pending" || ex["risk_key"] != riskKey || ex["application_key"] != apps["WEB"]["key"] || ex["key"] != "EV-1" || len(list(ex["waiting"])) != 1 {
		t.Fatalf("eccezione richiesta: %s", jsonString(ex))
	}
	if w := byCVE(admin, "CVE-2026-31010"); w["exception"] != "EV-1" || w["stage"] != "open" {
		t.Fatalf("la vulnerabilità segue l'eccezione in attesa: %s", jsonString(w))
	}
	var rel int
	if err := d.Owner.QueryRow(ctx, `select count(*) from relations r join relation_types rt on rt.id = r.type_id and rt.key = 'risk_of'
		join elements e on e.id = r.source_id join spaces s on s.id = e.space_id where s.key || '-' || e.number = $1 and r.target_id = $2::uuid`,
		riskKey, apps["WEB"]["id"]).Scan(&rel); err != nil || rel != 1 {
		t.Fatalf("relazione risk_of tra rischio e applicazione: %d %v", rel, err)
	}
	// Una seconda richiesta sulla stessa vulnerabilità è rifiutata finché la prima è aperta.
	dm.do("POST", V+"/exceptions", exc, http.StatusUnprocessableEntity)
	stepOf := func(u *user, key string) string {
		for _, a := range list(u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["approvals"]) {
			if obj(a)["status"] != "pending" {
				continue
			}
			for _, s := range list(obj(a)["steps"]) {
				if obj(s)["decision"] == "pending" {
					return obj(s)["id"].(string)
				}
			}
		}
		return ""
	}
	step := stepOf(admin, apps["WEB"]["key"].(string))
	if step == "" {
		t.Fatalf("passo di approvazione sull'applicazione non trovato")
	}
	dm.do("POST", "/api/v1/approvals/steps/"+step+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	lead.do("POST", "/api/v1/approvals/steps/"+step+"/decision", map[string]any{"decision": "approved", "comment": "Va bene per un mese"}, http.StatusNoContent)
	if w := byCVE(admin, "CVE-2026-31010"); w["stage"] != "exception" || w["exception_status"] != "approved" || w["exception_until"] != until {
		t.Fatalf("eccezione approvata: %s", jsonString(w))
	}
	if !check("vuln_eccezioni: 1 vuln_scadute: 0", apps["WEB"]["key"].(string)) {
		t.Fatalf("fatti dopo l'eccezione")
	}
	if n := len(list(admin.do("GET", V+"/exceptions?status=approved", nil, http.StatusOK)["items"])); n != 1 {
		t.Fatalf("elenco delle eccezioni: %d", n)
	}
	// Collegamento a un altro rischio e scollegamento.
	if x := admin.do("PATCH", V+"/exceptions/"+ex["id"].(string), map[string]any{"risk": ""}, http.StatusOK); x["risk_key"] != nil {
		t.Fatalf("scollegamento del rischio: %v", x)
	}
	admin.do("PATCH", V+"/exceptions/"+ex["id"].(string), map[string]any{"risk": "RIS-9999"}, http.StatusUnprocessableEntity)

	// --- Escalation: la critica (in correzione) e l'alta, scadute da ~9 giorni, fanno scattare il
	// passo dei 7 giorni (responsabile dell'unità `capo`), una volta sola; la media che scade fra
	// quattro giorni avvisa il referente `lead` (preavviso); la vetrina in eccezione non avvisa nessuno.
	count := func(member string) int {
		var n int
		if err := d.Owner.QueryRow(ctx, "select count(*) from notifications where tenant_id = $1 and member_id = $2::uuid and kind = 'vulnerability'", ten.TenantID, member).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Il giro del job è sul tenant della prova: gli altri test condividono il database.
	runJob := func() (devsecops.VulnReport, error) {
		var r devsecops.VulnReport
		err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			r, err = dso.CheckVulnerabilitiesTenant(ctx, tx)
			return err
		})
		return r, err
	}
	rep, err := runJob()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Notifications != 2 || rep.Findings != 3 || count(capo.member) != 1 || count(lead.member) != 1 || count(direttore.member) != 0 {
		t.Fatalf("escalation: %+v capo=%d lead=%d direttore=%d", rep, count(capo.member), count(lead.member), count(direttore.member))
	}
	if rep, err = runJob(); err != nil || rep.Notifications != 0 || count(capo.member) != 1 || count(lead.member) != 1 {
		t.Fatalf("un avviso parte una volta sola: %+v %v", rep, err)
	}
	// Dopo 20 giorni scatta il passo dei 21 giorni (unità sopra) anche se i precedenti erano già partiti.
	setTime(base.AddDate(0, 0, 13))
	if rep, err = runJob(); err != nil || rep.Notifications != 2 || count(direttore.member) != 1 || count(capo.member) != 2 {
		t.Fatalf("passo dell'unità sopra: %+v %v direttore=%d", rep, err, count(direttore.member))
	}
	var txt string
	if err := d.Owner.QueryRow(ctx, "select payload->>'event' from notifications where tenant_id = $1 and member_id = $2::uuid and kind = 'vulnerability'", ten.TenantID, direttore.member).Scan(&txt); err != nil || txt != "vuln_overdue" {
		t.Fatalf("evento della notifica: %q %v", txt, err)
	}

	// --- Eccezione scaduta: la vulnerabilità torna da correggere e l'escalation riparte.
	setTime(base.AddDate(0, 0, 40))
	if rep, err = runJob(); err != nil || rep.ExpiredExceptions != 1 {
		t.Fatalf("scadenza dell'eccezione: %+v %v", rep, err)
	}
	if w := byCVE(admin, "CVE-2026-31010"); w["stage"] != "open" || w["exception"] != nil {
		t.Fatalf("dopo la scadenza dell'eccezione: %s", jsonString(w))
	}
	if x := list(admin.do("GET", V+"/exceptions?status=expired", nil, http.StatusOK)["items"]); len(x) != 1 {
		t.Fatalf("eccezione scaduta: %v", x)
	}
	if n := count(dm.member); n != 2 { // l'avviso della scadenza all'autore
		t.Fatalf("avvisi delle eccezioni a chi le ha chieste: %d", n)
	}

	// --- Chiusura e riapertura automatiche dai report: la critica sparisce dall'ultimo report
	// della versione in produzione, poi ricompare.
	setTime(base)
	report(orders, "2026-11-29T03:00:00Z", high, med)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "closed" || c["closed_reason"] != "auto" {
		t.Fatalf("chiusura automatica: %s", jsonString(c))
	}
	if k := obj(admin.do("GET", V+"/dashboard", nil, http.StatusOK)["kpis"]); num(k["resolved_30d"]) != 1 {
		t.Fatalf("chiuse negli ultimi 30 giorni: %v", k)
	}
	report(orders, "2026-11-30T03:00:00Z", crit, high, med)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "open" || c["closed_reason"] != nil {
		t.Fatalf("riapertura automatica: %s", jsonString(c))
	}
	// Risolta a mano: resta risolta finché è nei report, poi si chiude da sola.
	dm.do("POST", V+"/bulk", map[string]any{"ids": []string{critID}, "action": "resolve", "note": "Aggiornato a 4.17.21"}, http.StatusOK)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "resolved" {
		t.Fatalf("risolta: %s", jsonString(c))
	}
	report(orders, "2026-11-30T09:00:00Z", crit, high, med)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "resolved" {
		t.Fatalf("risolta ma ancora nei report: %s", jsonString(c))
	}
	report(orders, "2026-12-01T03:00:00Z", high, med)
	if c := byCVE(admin, "CVE-2026-31001"); c["stage"] != "closed" {
		t.Fatalf("risolta e sparita: %s", jsonString(c))
	}

	// Il giro su tutti i tenant (come il job di serve) non deve fallire.
	if _, err := dso.CheckVulnerabilities(ctx, d); err != nil {
		t.Fatal(err)
	}

	// --- Revoca di un'eccezione in attesa, isolamento tra tenant.
	exc2 := dm.do("POST", V+"/exceptions", map[string]any{"ids": []string{webMedID}, "reason": "Rinnovo", "until": base.AddDate(0, 0, 20).Format(time.DateOnly)}, http.StatusCreated)
	id2 := obj(list(exc2["items"])[0])["id"].(string)
	lettore.do("POST", V+"/exceptions/"+id2+"/revoke", map[string]any{}, http.StatusForbidden)
	dm.do("POST", V+"/exceptions/"+id2+"/revoke", map[string]any{"comment": "Non serve più"}, http.StatusOK)
	if w := byCVE(admin, "CVE-2026-31010"); w["exception"] != nil || w["stage"] != "open" {
		t.Fatalf("dopo la revoca: %s", jsonString(w))
	}
	if k := obj(adminB.do("GET", V+"/dashboard", nil, http.StatusOK)["kpis"]); num(k["open"]) != 0 {
		t.Fatalf("isolamento: %v", k)
	}
	adminB.do("GET", V+"/items/"+critID, nil, http.StatusNotFound)
	// L'audit tiene traccia della politica e delle eccezioni.
	var audits int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action in ('devsecops.vuln_policy', 'devsecops.vuln_exception_requested', 'devsecops.vuln_take')`, ten.TenantID).Scan(&audits); err != nil || audits < 4 {
		t.Fatalf("audit: %d %v", audits, err)
	}
}
