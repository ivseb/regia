package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Rilasci con elementi: la finestra dei commit fra il rilascio precedente (senza i gemelli con
// la stessa versione) e questo, i gruppi delle note, «in produzione dal…» sulla story e in
// salita su epic e richiesta, la chiusura con le parole chiave (spenta per default, accesa
// per tenant, forzata per spazio) e la certificazione del rilascio per numero di change.
func TestRilasciElementi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	slug, slugB := "rl-"+stamp, "rlb-"+stamp
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, work.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Rilasci", AdminEmail: "admin@" + slug + ".test", AdminName: "Amministratrice",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rq := requirements.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := api.New(api.Options{DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Requirements: rq, Devsecops: dso,
		Log: log, Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir()})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	admin := login(t, hs.URL, "admin@"+slug+".test", testPassword)
	adminB := login(t, hs.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Richiesta → impatto → epic → story, come nel work management.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": admin.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome,team\nWEB-AR,Frontend area riservata,Web\n"}, http.StatusOK)
	var appID string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		if obj(a)["code"] == "WEB-AR" {
			appID = obj(a)["id"].(string)
		}
	}
	req := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Portale clienti B2B"}, http.StatusCreated)
	reqKey := req["key"].(string)
	imp := admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/impacts", map[string]any{"application_id": appID, "estimate_effort_days": 20}, http.StatusCreated)
	impKey := imp["key"].(string)
	move := func(ref, to string) {
		admin.do("POST", "/api/v1/elements/"+ref+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	move(imp["id"].(string), "In analisi")
	move(imp["id"].(string), "Stimato")
	move(imp["id"].(string), "Accettato")
	epics := list(admin.do("GET", "/api/v1/work/elements/"+impKey+"/delivery", nil, http.StatusOK)["epics"])
	if len(epics) != 1 {
		t.Fatalf("epic dell'impatto: %v", epics)
	}
	epicKey := obj(epics[0])["key"].(string)
	item := func(body map[string]any) string {
		return admin.do("POST", "/api/v1/work/spaces/WEB/items", body, http.StatusCreated)["key"].(string)
	}
	s1 := item(map[string]any{"type": "story", "title": "Login con SPID", "parent": epicKey})
	s2 := item(map[string]any{"type": "story", "title": "Elenco contratti", "parent": epicKey})
	s3 := item(map[string]any{"type": "story", "title": "Download fatture", "parent": epicKey})
	item(map[string]any{"type": "story", "title": "Solo analisi, senza codice", "parent": epicKey})
	b1 := item(map[string]any{"type": "bug", "title": "Errore 500 al logout"})

	// --- Rilasci della pipeline: 1.0.0 (8 giorni fa, ridistribuito 7 giorni fa), 1.1.0 in
	// staging (4 giorni e mezzo fa) e in produzione (4 giorni fa, ridistribuito 2 giorni fa).
	tok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour
	svc := map[string]any{"name": "portale", "repository": "acme/portale", "provider": "gitea"}
	release := func(env, version string, at time.Time, extra map[string]any) string {
		body := map[string]any{"format": "regia.devsecops/v1", "service": svc, "environment": env, "version": version, "at": at.Format(time.RFC3339)}
		for k, v := range extra {
			body[k] = v
		}
		return obj(bearerDo(t, hs.URL, tok, "POST", "/api/v1/devsecops/releases", body, http.StatusAccepted)["result"])["id"].(string)
	}
	deploy := func(env, version string, at time.Time, ref string) {
		bearerDo(t, hs.URL, tok, "POST", "/api/v1/devsecops/deployments", map[string]any{"format": "regia.devsecops/v1", "service": svc, "environment": env,
			"version": version, "status": "succeeded", "at": at.Format(time.RFC3339), "source": "gitops", "external_ref": ref}, http.StatusAccepted)
	}
	r100 := release("prod", "1.0.0", now.Add(-8*day), map[string]any{"change_number": "CHG0001"})
	deploy("prod", "1.0.0", now.Add(-7*day), "argo/1.0.0-bis")
	r110s := release("staging", "1.1.0", now.Add(-4*day-12*time.Hour), nil)
	r110 := release("prod", "1.1.0", now.Add(-4*day), map[string]any{"change_number": "CHG0002", "approved_by": "Carla Bianchi",
		"checks": []map[string]any{{"name": "test", "mode": "block", "outcome": "passed", "summary": "120 passed, 2 skipped"},
			{"name": "sonar", "mode": "warn", "outcome": "passed", "summary": "Quality gate OK"}},
		"cves_at_release": map[string]any{"critical": 0, "high": 1}})
	deploy("prod", "1.1.0", now.Add(-2*day), "argo/1.1.0-bis")

	// --- Commit legati (come da un connettore): s1 e s2 prima della 1.0.0, s2 e b1 fra la
	// 1.0.0 e la 1.1.0, s3 dopo.
	actor := engine.Actor{MemberID: admin.member, Permissions: []string{"*"}}
	ingest := func(commits ...contract.Commit) {
		t.Helper()
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			_, err := dso.IngestCommits(ctx, tx, actor, contract.Service{Name: "portale"}, commits)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	ingest(
		contract.Commit{SHA: sha('a'), Message: s1 + " login", At: now.Add(-10 * day), Author: "Ana"},
		contract.Commit{SHA: sha('b'), Message: s2 + " primo elenco", At: now.Add(-9 * day), Author: "Ana"},
		contract.Commit{SHA: sha('c'), Message: s2 + " filtri dell'elenco", At: now.Add(-6 * day), Author: "Bruno"},
		contract.Commit{SHA: sha('d'), Message: "fixes " + b1, At: now.Add(-5 * day), Author: "Bruno"}, // chiusura spenta: non chiude
		contract.Commit{SHA: sha('e'), Message: s3 + " fatture", At: now.Add(-3 * day), Author: "Ana"},
	)

	keys := func(rel map[string]any) string {
		var out []string
		for _, it := range list(rel["items"]) {
			out = append(out, fmt.Sprintf("%s:%s", obj(it)["key"], obj(it)["group"]))
		}
		return strings.Join(out, ",")
	}
	// 1.0.0: prima release, finestra aperta; s1 e s2 (che continua).
	d100 := admin.do("GET", "/api/v1/devsecops/releases/"+r100, nil, http.StatusOK)
	if got := keys(d100); got != s2+":new,"+s1+":new" || obj(d100["window"])["from"] != nil {
		t.Fatalf("elementi della 1.0.0: %s, finestra %v", got, d100["window"])
	}
	for _, it := range list(d100["items"]) {
		if obj(it)["key"] == s2 && obj(it)["continues"] != true {
			t.Fatalf("s2 continua dopo la 1.0.0: %v", it)
		}
	}
	// 1.1.0: dal gemello della 1.0.0 (7 giorni fa) al primo deploy della 1.1.0 (4 giorni fa,
	// non il gemello di 2 giorni fa): s2 e b1; le novità prima delle correzioni.
	d110 := admin.do("GET", "/api/v1/devsecops/releases/"+r110, nil, http.StatusOK)
	if got := keys(d110); got != s2+":new,"+b1+":fix" {
		t.Fatalf("elementi della 1.1.0: %s", got)
	}
	w := obj(d110["window"])
	if w["previous_version"] != "1.0.0" || w["from"] != now.Add(-7*day).Format(time.RFC3339) || w["to"] != now.Add(-4*day).Format(time.RFC3339) {
		t.Fatalf("finestra della 1.1.0: %v", w)
	}
	if d110["commits"] != float64(2) || obj(list(d110["items"])[0])["type"] != "story" || obj(list(d110["items"])[0])["state"] == "" {
		t.Fatalf("1.1.0: %v", d110)
	}
	// In staging la finestra è propria dell'ambiente: tutto fino a 4 giorni e mezzo fa.
	if got := keys(admin.do("GET", "/api/v1/devsecops/releases/"+r110s, nil, http.StatusOK)); got != s2+":new,"+s1+":new,"+b1+":fix" {
		t.Fatalf("elementi della 1.1.0 in staging: %s", got)
	}
	adminB.do("GET", "/api/v1/devsecops/releases/"+r110, nil, http.StatusNotFound)
	admin.do("GET", "/api/v1/devsecops/releases/non-un-id", nil, http.StatusNotFound)

	// --- «In produzione dal…»
	prod := func(key string) map[string]any {
		return admin.do("GET", "/api/v1/elements/"+key+"/production", nil, http.StatusOK)
	}
	if p := obj(prod(s1)["self"]); p["in_production"] != true || p["version"] != "1.0.0" || p["at"] != now.Add(-8*day).Format(time.RFC3339) {
		t.Fatalf("s1 in produzione: %v", p)
	}
	if p := obj(prod(s2)["self"]); p["in_production"] != true || p["version"] != "1.1.0" || p["release_id"] != r110 {
		t.Fatalf("s2 in produzione: %v", p)
	}
	if p := obj(prod(s3)["self"]); p["in_production"] != false || len(list(p["pending"])) != 1 {
		t.Fatalf("s3 non ancora in produzione: %v", p)
	}
	// Il blocco «Codice» usa lo stesso calcolo e porta il link al rilascio.
	if rel := obj(admin.do("GET", "/api/v1/elements/"+s2+"/commits", nil, http.StatusOK)["release"]); rel["version"] != "1.1.0" || rel["release_id"] != r110 {
		t.Fatalf("rilascio di s2 nel blocco Codice: %v", rel)
	}
	for _, key := range []string{epicKey, impKey, reqKey} {
		p := prod(key)
		if p["total"] != float64(3) || p["in_production"] != float64(2) || p["last_at"] != now.Add(-4*day).Format(time.RFC3339) || p["self"] != nil {
			t.Fatalf("produzione in salita su %s: %v", key, p)
		}
	}
	adminB.do("GET", "/api/v1/elements/"+reqKey+"/production", nil, http.StatusNotFound)

	// --- Chiusura con le parole chiave.
	stateOf := func(key string) string {
		return obj(admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"])["state"].(string)
	}
	activity := func(key, field string) int {
		var n int
		if err := d.Owner.QueryRow(ctx, `select count(*) from activities a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where a.tenant_id = $1 and sp.key || '-' || e.number = $2 and a.kind = 'commit' and a.field_key = $3`, ten.TenantID, key, field).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if stateOf(b1) == "Fatto" || activity(b1, "closed")+activity(b1, "close_skipped") != 0 {
		t.Fatalf("con la chiusura spenta non succede niente: %s", stateOf(b1))
	}
	st := admin.do("GET", "/api/v1/devsecops/release-settings", nil, http.StatusOK)
	if st["close_on_keywords"] != false || len(list(st["cert_sections"])) != 7 {
		t.Fatalf("impostazioni predefinite: %v", st)
	}
	admin.do("PUT", "/api/v1/devsecops/release-settings", map[string]any{"close_on_keywords": true, "spaces": []map[string]any{{"key": "WEB", "close_on_keywords": false}}}, http.StatusOK)
	ingest(contract.Commit{SHA: sha('f'), Message: "Closes " + s3, At: now.Add(-1 * day)})
	if stateOf(s3) == "Fatto" || activity(s3, "closed") != 0 {
		t.Fatalf("lo spazio WEB ha la chiusura forzata a no: %s", stateOf(s3))
	}
	admin.do("PUT", "/api/v1/devsecops/release-settings", map[string]any{"close_on_keywords": true, "spaces": []map[string]any{}}, http.StatusOK)
	// La story appena creata non può andare in «Fatto»: resta una nota nella storia.
	ingest(contract.Commit{SHA: sha('g'), Message: "fixes " + s3 + " totale", At: now.Add(-1 * day)})
	if stateOf(s3) == "Fatto" || activity(s3, "close_skipped") != 1 {
		t.Fatalf("passaggio non permesso: stato %s, note %d", stateOf(s3), activity(s3, "close_skipped"))
	}
	// Portata fino al passo prima di «Fatto», il commit successivo la chiude.
	admin.do("PUT", "/api/v1/work/elements/"+s3+"/assignee", map[string]any{"member_id": admin.member}, http.StatusOK)
	for i := 0; i < 6; i++ {
		el := admin.do("GET", "/api/v1/elements/"+s3, nil, http.StatusOK)
		var next string
		done := false
		for _, tr := range list(el["transitions"]) {
			tv := obj(tr)
			if len(list(tv["blocked"])) > 0 {
				continue
			}
			if tv["to"] == "Fatto" {
				done = true
			}
			if next == "" && tv["forward"] == true {
				next = tv["to"].(string)
			}
		}
		if done {
			break
		}
		if next == "" {
			t.Fatalf("nessun passaggio in avanti da %s: %v", stateOf(s3), el["transitions"])
		}
		move(obj(el["element"])["id"].(string), next)
	}
	ingest(contract.Commit{SHA: sha('h'), Message: "Totale fatture\n\nResolves: " + s3, At: now.Add(-12 * time.Hour), URL: "http://gitea/acme/portale/commit/" + sha('h')})
	if stateOf(s3) != "Fatto" || activity(s3, "closed") != 1 {
		t.Fatalf("chiusura con resolves: stato %s, chiusure %d", stateOf(s3), activity(s3, "closed"))
	}
	var comment string
	if err := d.Owner.QueryRow(ctx, `select a.added->>'comment' from activities a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where a.tenant_id = $1 and sp.key || '-' || e.number = $2 and a.kind = 'state' order by a.created_at desc limit 1`, ten.TenantID, s3).Scan(&comment); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comment, "hhhhhhh") || !strings.Contains(comment, "resolves "+s3) {
		t.Fatalf("commento del passaggio: %q", comment)
	}
	// Riletto lo stesso commit, niente di nuovo.
	ingest(contract.Commit{SHA: sha('h'), Message: "Resolves: " + s3, At: now.Add(-12 * time.Hour)})
	if activity(s3, "closed") != 1 {
		t.Fatalf("la rilettura non richiude")
	}

	// --- Certificazione del rilascio per numero di change.
	cert := admin.do("GET", "/api/v1/devsecops/changes/chg0002/certification", nil, http.StatusOK)
	if obj(cert["change"])["number"] != "CHG0002" || len(list(cert["releases"])) != 1 || cert["version"] != nil || cert["can_generate"] != true ||
		cert["path"] != "/change/CHG0002/certificazione" {
		t.Fatalf("certificazione dal vivo: %v", cert)
	}
	if got := keys(cert); got != s2+":new,"+b1+":fix" {
		t.Fatalf("elementi della certificazione: %s", got)
	}
	tt := obj(cert["test_totals"])
	if tt["counted"] != true || tt["executed"] != float64(122) || tt["passed"] != float64(120) || tt["skipped"] != float64(2) {
		t.Fatalf("numeri dei test: %v", tt)
	}
	if s := list(cert["sonar"]); len(s) != 1 || obj(s[0])["outcome"] != "passed" {
		t.Fatalf("sonar: %v", cert["sonar"])
	}
	if a := list(cert["approvals"]); len(a) != 1 || obj(a[0])["approved_by"] != "Carla Bianchi" {
		t.Fatalf("approvazioni: %v", cert["approvals"])
	}
	if s := list(cert["security"]); len(s) != 1 || obj(obj(s[0])["cves"])["high"] != float64(1) {
		t.Fatalf("sicurezza: %v", cert["security"])
	}
	// La change letta dal connettore ServiceNow, collegata sul numero.
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var integ string
		if err := tx.QueryRow(ctx, `insert into integrations (tenant_id, kind, name) values (app_tenant(), 'generic', 'ServiceNow') returning id::text`).Scan(&integ); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into servicenow_changes (tenant_id, id, integration_id, sys_id, number, state, approval, type, planned_start, url, sys_updated_on)
			values (app_tenant(), gen_random_uuid(), $1, 'abc', 'chg0002', '-2', 'approved', 'normal', now(), 'https://sn.example.test/chg', now())`, integ)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sn := obj(obj(admin.do("GET", "/api/v1/devsecops/changes/CHG0002/certification", nil, http.StatusOK)["change"])["servicenow"])
	if sn["approval"] != "approved" || sn["state"] != "-2" || sn["url"] != "https://sn.example.test/chg" || sn["planned_start"] == nil {
		t.Fatalf("change di ServiceNow: %v", sn)
	}
	g1 := admin.do("POST", "/api/v1/devsecops/changes/CHG0002/certification", nil, http.StatusCreated)
	v1 := obj(g1["version"])
	if v1["version"] != float64(1) || len(v1["sha256"].(string)) != 64 || len(list(g1["generated"])) != 1 {
		t.Fatalf("prima generazione: %v", g1["version"])
	}
	g2 := admin.do("POST", "/api/v1/devsecops/changes/CHG0002/certification", nil, http.StatusCreated)
	if obj(g2["version"])["version"] != float64(2) || len(list(g2["generated"])) != 2 {
		t.Fatalf("seconda generazione: %v", g2["version"])
	}
	old := admin.do("GET", "/api/v1/devsecops/changes/CHG0002/certification?version=1", nil, http.StatusOK)
	if obj(old["version"])["sha256"] != v1["sha256"] || keys(old) != s2+":new,"+b1+":fix" {
		t.Fatalf("istantanea della versione 1: %v", old["version"])
	}
	admin.do("GET", "/api/v1/devsecops/changes/CHG0002/certification?version=9", nil, http.StatusNotFound)
	admin.do("GET", "/api/v1/devsecops/changes/CHG9999/certification", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/devsecops/changes/CHG0002/certification", nil, http.StatusNotFound)
	// Un change senza rilasci non si certifica.
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into changes (tenant_id, number) values (app_tenant(), 'CHG0003')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin.do("POST", "/api/v1/devsecops/changes/CHG0003/certification", nil, http.StatusUnprocessableEntity)
	// Le sezioni si configurano e si ordinano.
	admin.do("PUT", "/api/v1/devsecops/release-settings", map[string]any{"close_on_keywords": false, "cert_sections": []string{"items", "change", "tests"}}, http.StatusOK)
	if s := list(admin.do("GET", "/api/v1/devsecops/changes/CHG0002/certification", nil, http.StatusOK)["sections"]); fmt.Sprint(s) != "[items change tests]" {
		t.Fatalf("sezioni configurate: %v", s)
	}
	admin.do("PUT", "/api/v1/devsecops/release-settings", map[string]any{"cert_sections": []string{"boh"}}, http.StatusUnprocessableEntity)
}
