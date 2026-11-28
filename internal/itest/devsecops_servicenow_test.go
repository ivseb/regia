package itest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/devtools/fakeservicenow"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	snconn "regia/internal/modules/devsecops/connectors/servicenow"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/secretbox"
	"regia/internal/platform/tenant"
)

// Connettore ServiceNow in sola lettura, contro il finto ServiceNow (fakeservicenow, http,
// rete interna): configurazione con segreti cifrati, prova di connessione (Basic, OAuth,
// credenziali errate, ACL mancante), scoperta dei servizi con proposta e conferma, lettura
// incrementale di incidenti P1-P2 e change, metriche DORA solo dopo conferma e senza doppi
// conteggi, proposta di incidente di Regia, gate con l'approvazione di ServiceNow, webhook
// firmato e deduplicato, permessi e isolamento tra tenant.

const snAPI = "/api/v1/devsecops/servicenow"

func TestServiceNowConnector(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	slug, slugB := "sn-"+stamp, "snb-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, risk.Provision}
	for _, sl := range []string{slug, slugB} {
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: sl, Name: "ServiceNow " + sl, AdminEmail: "admin@" + sl + ".test",
			AdminName: "Amministratrice", AdminPassword: testPassword, Provision: provision}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: "lettore@" + slug + ".test", Name: "Lettore",
		Password: testPassword, Roles: []string{"viewer"}}); err != nil {
		t.Fatal(err)
	}

	// Finto ServiceNow: tre servizi, incidenti P1-P3, change approvate e no.
	fake := fakeservicenow.New()
	fake.AddUser("regia.lettura", "password-di-prova")
	fake.AddClient("regia-client", "segreto-oauth-di-prova")
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(fakeservicenow.Layout) }
	ordini := fake.Put("cmdb_ci_service", map[string]string{"name": "Ordini online", "sys_class_name": "cmdb_ci_service_auto", "u_codice": "ORD"})
	pagamenti := fake.Put("cmdb_ci_service", map[string]string{"name": "Pagamenti", "sys_class_name": "cmdb_ci_service_auto"})
	posta := fake.Put("cmdb_ci_service", map[string]string{"name": "Posta interna", "sys_class_name": "cmdb_ci_service"})
	h := time.Hour
	inc1 := fake.Put("incident", map[string]string{"number": "INC0010001", "priority": "1", "state": "7", "short_description": "Ordini online non raggiungibili",
		"business_service": ordini, "opened_at": at(10 * 24 * h), "resolved_at": at(10*24*h - 3*h), "closed_at": at(9 * 24 * h), "sys_updated_on": at(9 * 24 * h)})
	fake.Put("incident", map[string]string{"number": "INC0010002", "priority": "2", "state": "6", "short_description": "Pagamenti lenti",
		"business_service": pagamenti, "opened_at": at(5 * 24 * h), "resolved_at": at(5*24*h - h), "sys_updated_on": at(5*24*h - h)})
	fake.Put("incident", map[string]string{"number": "INC0010003", "priority": "3", "state": "2", "short_description": "Stampa lenta",
		"business_service": ordini, "opened_at": at(2 * 24 * h), "sys_updated_on": at(2 * 24 * h)})
	inc4 := fake.Put("incident", map[string]string{"number": "INC0010004", "priority": "1", "state": "2", "short_description": "Carrello vuoto",
		"cmdb_ci": ordini, "opened_at": at(5 * h), "sys_updated_on": at(5 * h)})
	fake.Put("incident", map[string]string{"number": "INC0010005", "priority": "1", "state": "2", "short_description": "Posta ferma",
		"business_service": posta, "opened_at": at(3 * h), "sys_updated_on": at(3 * h)})
	fake.Put("incident", map[string]string{"number": "INC0000999", "priority": "1", "state": "7", "short_description": "Vecchio",
		"business_service": ordini, "opened_at": at(400 * 24 * h), "sys_updated_on": at(400 * 24 * h)})
	fake.Put("change_request", map[string]string{"number": "CHG0030002", "state": "-2", "approval": "approved", "short_description": "Rilascio ordini 2.5.0",
		"business_service": ordini, "start_date": at(-24 * h), "sys_updated_on": at(h)})
	fake.Put("change_request", map[string]string{"number": "CHG0030003", "state": "-3", "approval": "requested", "short_description": "Nuovo carrello",
		"business_service": ordini, "sys_updated_on": at(h)})
	fake.Put("change_request", map[string]string{"number": "CHG0030099", "state": "-2", "approval": "approved", "short_description": "Cambio della posta",
		"business_service": posta, "sys_updated_on": at(h)})
	fakeSrv := httptest.NewServer(fake.Handler())
	t.Cleanup(fakeSrv.Close)

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rq := requirements.Register(eng, ap)
	rk := risk.Register(eng, ap)
	dso := devsecops.Register(eng, rq)
	box := secretbox.Random()
	as := auth.NewService(d, time.Hour)
	as.SetSecretBox(box)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := snconn.New(snconn.Options{DB: d, Box: box, Dora: dso, AllowPrivate: true, Log: log, Timeout: 5 * time.Second})
	srv := api.New(api.Options{DB: d, Auth: as, Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Requirements: rq, Devsecops: dso, Risk: rk,
		Log: log, Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, SecretBox: box, ServiceNow: svc, FilesDir: t.TempDir()})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	t.Cleanup(svc.WaitIdle)
	admin := login(t, hs.URL, "admin@"+slug+".test", testPassword)
	reader := login(t, hs.URL, "lettore@"+slug+".test", testPassword)
	adminB := login(t, hs.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Gestione ordini", "code": "ORD"}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Pagamenti", "code": "PAG"}, http.StatusCreated)
	apps := map[string]map[string]any{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)
	}
	ordID, ordKey := apps["ORD"]["id"].(string), apps["ORD"]["key"].(string)

	// --- Configurazione: rete interna obbligatoria per 127.0.0.1, credenziali obbligatorie.
	in := map[string]any{"name": "ServiceNow di prova", "base_url": fakeSrv.URL, "auth": "basic", "username": "regia.lettura",
		"password": "password-di-prova", "code_field": "u_codice", "private_network": true}
	bad := func(k string, v any) {
		t.Helper()
		b := map[string]any{}
		for kk, vv := range in {
			b[kk] = vv
		}
		b[k] = v
		admin.do("POST", snAPI+"/integrations", b, http.StatusUnprocessableEntity)
	}
	bad("private_network", false)
	bad("password", "")
	bad("auth", "token")
	bad("priorities", []int{0})
	bad("service_class", "cmdb ci")
	bad("use_proxy", true) // l'operatore non ha configurato REGIA_CONNECTOR_PROXY
	reader.do("POST", snAPI+"/integrations", in, http.StatusForbidden)
	id := admin.do("POST", snAPI+"/integrations", in, http.StatusCreated)["id"].(string)
	raw, _ := json.Marshal(admin.do("GET", snAPI+"/integrations/"+id, nil, http.StatusOK))
	if strings.Contains(string(raw), "password-di-prova") || !strings.Contains(string(raw), `"has_password":true`) ||
		!strings.Contains(string(raw), `"priorities":[1,2]`) || !strings.Contains(string(raw), `"dora_mode":"confirm"`) ||
		!strings.Contains(string(raw), `"service_class":"cmdb_ci_service"`) {
		t.Fatalf("integrazione: %s", raw)
	}
	adminB.do("GET", snAPI+"/integrations/"+id, nil, http.StatusNotFound)

	// --- Prova di connessione: Basic riuscita; password errata (401) spiegata; OAuth; ACL mancante.
	tr := admin.do("POST", snAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != true || !strings.Contains(tr["message"].(string), "come «regia.lettura»") || len(list(tr["tables"])) != 3 {
		t.Fatalf("prova Basic: %v", tr)
	}
	wrong := admin.do("POST", snAPI+"/integrations", map[string]any{"name": "Password errata", "base_url": fakeSrv.URL, "username": "regia.lettura",
		"password": "sbagliata", "private_network": true}, http.StatusCreated)["id"].(string)
	if tr := admin.do("POST", snAPI+"/integrations/"+wrong+"/test", nil, http.StatusOK); tr["ok"] != false || !strings.Contains(tr["message"].(string), "(401)") {
		t.Fatalf("prova con password errata: %v", tr)
	}
	oauth := admin.do("POST", snAPI+"/integrations", map[string]any{"name": "OAuth", "base_url": fakeSrv.URL, "auth": "oauth", "client_id": "regia-client",
		"client_secret": "segreto-oauth-di-prova", "private_network": true}, http.StatusCreated)["id"].(string)
	if tr := admin.do("POST", snAPI+"/integrations/"+oauth+"/test", nil, http.StatusOK); tr["ok"] != true || !strings.Contains(tr["message"].(string), "client credentials") {
		t.Fatalf("prova OAuth: %v", tr)
	}
	admin.do("PATCH", snAPI+"/integrations/"+oauth, map[string]any{"name": "OAuth", "base_url": fakeSrv.URL, "auth": "oauth", "client_id": "regia-client",
		"client_secret": "segreto-sbagliato", "private_network": true}, http.StatusNoContent)
	if tr := admin.do("POST", snAPI+"/integrations/"+oauth+"/test", nil, http.StatusOK); tr["ok"] != false || !strings.Contains(tr["message"].(string), "token OAuth") {
		t.Fatalf("prova OAuth con segreto errato: %v", tr)
	}
	admin.do("DELETE", snAPI+"/integrations/"+oauth, nil, http.StatusNoContent)
	admin.do("DELETE", snAPI+"/integrations/"+wrong, nil, http.StatusNoContent)

	// --- Scoperta: tre servizi; «Ordini online» proposto per codice, «Pagamenti» per nome.
	res := admin.do("POST", snAPI+"/integrations/"+id+"/discover", nil, http.StatusOK)
	if res["found"].(float64) != 3 || res["suggested"].(float64) != 2 {
		t.Fatalf("scoperta: %v", res)
	}
	services := map[string]map[string]any{}
	for _, it := range list(admin.do("GET", snAPI+"/integrations/"+id+"/services", nil, http.StatusOK)["items"]) {
		services[obj(it)["name"].(string)] = obj(it)
	}
	if s := services["Ordini online"]; s["status"] != "suggested" || obj(obj(s["suggestion"])["application"])["code"] != "ORD" || s["class"] != "cmdb_ci_service_auto" ||
		!strings.Contains(obj(s["suggestion"])["reason"].(string), "codice") {
		t.Fatalf("proposta di Ordini online: %v", s)
	}
	if services["Posta interna"]["status"] != "none" {
		t.Fatalf("Posta interna: %v", services["Posta interna"])
	}
	// Prima della conferma non si sincronizza nulla.
	sync := func() map[string]any {
		t.Helper()
		admin.do("POST", snAPI+"/integrations/"+id+"/sync", nil, http.StatusAccepted)
		svc.WaitIdle()
		return obj(list(admin.do("GET", snAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"])[0])
	}
	if run := sync(); run["status"] != "ok" || obj(run["stats"])["incidents"].(float64) != 0 {
		t.Fatalf("sincronizzazione senza associazioni: %v", run)
	}
	n := admin.do("POST", snAPI+"/integrations/"+id+"/services/accept", map[string]any{"ids": []string{services["Ordini online"]["id"].(string), services["Pagamenti"]["id"].(string)}}, http.StatusOK)
	if n["confirmed"].(float64) != 2 {
		t.Fatalf("conferma: %v", n)
	}

	// --- Sincronizzazione: P1-P2 dei servizi associati, nella finestra di 90 giorni (né P3, né
	// Posta interna, né l'incidente di 400 giorni fa); incidente trovato anche dal CI.
	run := sync()
	st := obj(run["stats"])
	if run["status"] != "ok" || st["new_incidents"].(float64) != 3 || st["new_changes"].(float64) != 2 {
		t.Fatalf("prima sincronizzazione: %v", run)
	}
	if q := fake.Queries["incident"]; !slices.ContainsFunc(q, func(s string) bool {
		return strings.HasPrefix(s, "priorityIN1,2^business_service="+ordini+"^ORcmdb_ci="+ordini+"^sys_updated_on>=")
	}) {
		t.Fatalf("query degli incidenti: %v", q)
	}
	if run := sync(); obj(run["stats"])["new_incidents"].(float64) != 0 || obj(run["stats"])["new_changes"].(float64) != 0 {
		t.Fatalf("seconda sincronizzazione non idempotente: %v", run)
	}
	// Incrementale: l'incidente risolto in ServiceNow si aggiorna.
	fake.Put("incident", map[string]string{"sys_id": inc4, "state": "6", "resolved_at": now.Add(-time.Hour).Format(fakeservicenow.Layout),
		"sys_updated_on": now.Add(-time.Hour).Format(fakeservicenow.Layout)})
	sync()
	incs := map[string]map[string]any{}
	for _, it := range list(reader.do("GET", snAPI+"/incidents?application="+ordID, nil, http.StatusOK)["items"]) {
		incs[obj(it)["number"].(string)] = obj(it)
	}
	if len(incs) != 2 || incs["INC0010004"]["resolved_at"] == nil || incs["INC0010004"]["state_label"] != "Risolto" || incs["INC0010001"]["counts"] != false ||
		!strings.Contains(incs["INC0010001"]["url"].(string), "incident.do") {
		t.Fatalf("incidenti dell'applicazione: %v", incs)
	}
	raw, _ = json.Marshal(incs)
	if strings.Contains(string(raw), "caller") || strings.Contains(string(raw), "work_notes") {
		t.Fatalf("dati personali negli incidenti: %s", raw)
	}

	// --- DORA: con «solo dopo conferma» l'incidente conta dopo la decisione.
	drill := func() []any {
		t.Helper()
		atISO := now.Add(-10 * 24 * h).Format(time.RFC3339)
		return list(reader.do("GET", "/api/v1/devsecops/dora/drilldown?period=week&scope=application&subject="+ordKey+"&at="+atISO, nil, http.StatusOK)["incidents"])
	}
	if got := drill(); len(got) != 0 {
		t.Fatalf("incidente non confermato già nelle DORA: %v", got)
	}
	inc1ID := incs["INC0010001"]["id"].(string)
	reader.do("POST", snAPI+"/incidents/"+inc1ID+"/decision", map[string]any{"status": "counted"}, http.StatusForbidden)
	admin.do("POST", snAPI+"/incidents/"+inc1ID+"/decision", map[string]any{"status": "forse"}, http.StatusUnprocessableEntity)
	if x := admin.do("POST", snAPI+"/incidents/"+inc1ID+"/decision", map[string]any{"status": "counted"}, http.StatusOK); x["counts"] != true {
		t.Fatalf("decisione: %v", x)
	}
	got := drill()
	if len(got) != 1 || obj(got[0])["key"] != "INC0010001" || obj(got[0])["source"] != "servicenow" || obj(got[0])["hours"] == nil {
		t.Fatalf("incidente confermato nelle DORA: %v", got)
	}

	// --- Proposta di incidente di Regia: precompilata, confermata da una persona; poi conta una volta sola.
	reader.do("GET", snAPI+"/incidents?proposals=true", nil, http.StatusForbidden)
	props := list(admin.do("GET", snAPI+"/incidents?proposals=true", nil, http.StatusOK)["items"])
	if len(props) != 3 {
		t.Fatalf("proposte: %v", props)
	}
	p := admin.do("GET", snAPI+"/incidents/"+inc1ID+"/proposal", nil, http.StatusOK)
	if p["title"] != "Ordini online non raggiungibili" || len(list(p["impacted"])) != 1 || list(p["impacted"])[0] != ordID ||
		!strings.Contains(p["description"].(string), "INC0010001") {
		t.Fatalf("proposta: %v", p)
	}
	view := admin.do("POST", snAPI+"/incidents/"+inc1ID+"/promote", map[string]any{"title": p["title"], "description": p["description"],
		"detected_at": p["detected_at"], "impacted": p["impacted"]}, http.StatusCreated)
	regiaKey := view["key"].(string)
	admin.do("POST", snAPI+"/incidents/"+inc1ID+"/promote", map[string]any{"title": "Doppione", "impacted": p["impacted"]}, http.StatusConflict)
	got = drill()
	if len(got) != 1 || obj(got[0])["key"] != regiaKey || obj(got[0])["source"] != nil {
		t.Fatalf("dopo la proposta conta solo l'incidente di Regia: %v", got)
	}
	if x := obj(list(admin.do("GET", snAPI+"/incidents?application="+ordID, nil, http.StatusOK)["items"])[1]); x["number"] != "INC0010001" ||
		obj(x["regia_incident"])["key"] != regiaKey || x["counts"] != false {
		t.Fatalf("incidente ServiceNow collegato: %v", x)
	}
	inc2 := ""
	for _, it := range props {
		if obj(it)["number"] == "INC0010002" {
			inc2 = obj(it)["id"].(string)
		}
	}
	admin.do("POST", snAPI+"/incidents/"+inc2+"/decision", map[string]any{"status": "excluded"}, http.StatusOK)
	if props := list(admin.do("GET", snAPI+"/incidents?proposals=true", nil, http.StatusOK)["items"]); len(props) != 1 || obj(props[0])["number"] != "INC0010004" {
		t.Fatalf("proposte dopo le decisioni: %v", props)
	}
	_ = inc1

	// --- Change: dei servizi associati, e quelle già note a Regia per numero.
	chg := map[string]map[string]any{}
	for _, it := range list(reader.do("GET", snAPI+"/changes?application="+ordID, nil, http.StatusOK)["items"]) {
		chg[obj(it)["number"].(string)] = obj(it)
	}
	if len(chg) != 2 || chg["CHG0030002"]["approval_label"] != "Approvata" || chg["CHG0030003"]["state_label"] != "Autorizzazione" {
		t.Fatalf("change dell'applicazione: %v", chg)
	}
	ciTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "CI", "scopes": []string{"devsecops.write"}}, http.StatusCreated)["token"].(string)
	bearerDo(t, hs.URL, ciTok, "POST", "/api/v1/devsecops/releases", map[string]any{"format": "regia.devsecops/v1",
		"service": map[string]any{"name": "posta", "repository": "acme/posta", "provider": "github", "application": "ORD"},
		"version": "1.0.0", "commit": strings.Repeat("a", 40), "environment": "prod", "change_number": "CHG0030099", "at": now.Format(time.RFC3339)}, http.StatusAccepted)
	if run := sync(); obj(run["stats"])["known_changes"].(float64) != 1 {
		t.Fatalf("change note a Regia: %v", run)
	}

	// --- Gate: «accept» basta l'approvazione in ServiceNow; un'approvazione chiesta non basta.
	rule := func(params map[string]any) map[string]any {
		return map[string]any{"mode": "block", "override_enabled": false, "override_hours": 24,
			"rules": []any{map[string]any{"kind": "change_approved", "enabled": true, "level": "block", "params": params}}}
	}
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"phases": []string{}, "require_change": true}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"phases": []string{}, "require_change": true, "servicenow": "forse"}), http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"phases": []string{}, "require_change": true, "servicenow": "accept"}), http.StatusOK)
	gateTok := admin.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Gate", "scopes": []string{"devsecops.gate"}}, http.StatusCreated)["token"].(string)
	gate := func(change string, want int) map[string]any {
		t.Helper()
		return bearerDo(t, hs.URL, gateTok, "POST", "/api/v1/devsecops/gate", map[string]any{"format": "regia.devsecops/v1", "application": "ORD",
			"environment": "prod", "change": change}, want)
	}
	reason := func(g map[string]any) string {
		for _, r := range list(g["reasons"]) {
			if obj(r)["kind"] == "change_approved" {
				return obj(r)["message"].(string)
			}
		}
		return ""
	}
	if g := gate("CHG0030002", http.StatusOK); g["result"] != "pass" || !strings.Contains(reason(g), "approvato in ServiceNow") {
		t.Fatalf("gate con change approvata: %v", g)
	}
	if g := gate("CHG0030003", http.StatusConflict); g["result"] != "fail" || !strings.Contains(reason(g), "non è approvato in ServiceNow") {
		t.Fatalf("gate con approvazione chiesta: %v", g)
	}
	if g := gate("CHG0030099", http.StatusOK); g["result"] != "pass" {
		t.Fatalf("gate con change nota a Regia: %v", g)
	}
	admin.do("PUT", "/api/v1/devsecops/gate/policies/prod", rule(map[string]any{"phases": []string{}, "require_change": false, "servicenow": "require"}), http.StatusOK)
	if g := gate("CHG0099999", http.StatusConflict); g["result"] != "fail" || !strings.Contains(reason(g), "non è tra quelli letti da ServiceNow") {
		t.Fatalf("gate con change sconosciuta: %v", g)
	}

	// --- Webhook: senza firma 401, firma esadecimale o base64 valida 202, la stessa consegna una volta.
	admin.do("POST", snAPI+"/integrations/"+id+"/webhook-secret", nil, http.StatusOK)
	secret := "segreto-del-webhook-di-prova"
	admin.do("PATCH", snAPI+"/integrations/"+id, map[string]any{"name": "ServiceNow di prova", "base_url": fakeSrv.URL, "auth": "basic",
		"username": "regia.lettura", "code_field": "u_codice", "private_network": true, "webhook_secret": secret}, http.StatusNoContent)
	post := func(sig, delivery string, body []byte) int {
		t.Helper()
		req, _ := http.NewRequest("POST", hs.URL+snAPI+"/webhook/"+id, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Regia-Delivery", delivery)
		if sig != "" {
			req.Header.Set("X-Regia-Signature", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		svc.WaitIdle()
		return resp.StatusCode
	}
	body := []byte(`{"table":"incident","sys_id":"` + inc4 + `","number":"INC0010004"}`)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	b64 := base64.StdEncoding.EncodeToString(m.Sum(nil))
	for _, c := range []struct {
		sig, delivery string
		want          int
	}{{"", "d1", 401}, {"sha256=00ff", "d2", 401}, {"sha256=" + snconn.Sign(secret, body), "d3", 202}, {snconn.Sign(secret, body), "d3", 200}, {b64, "d4", 202}} {
		if got := post(c.sig, c.delivery, body); got != c.want {
			t.Fatalf("webhook %s/%s: %d, atteso %d", c.sig, c.delivery, got, c.want)
		}
	}
	if r := obj(list(admin.do("GET", snAPI+"/integrations/"+id+"/runs", nil, http.StatusOK)["items"])[0]); r["trigger"] != "webhook" || r["scope"] != "incidenti" {
		t.Fatalf("sincronizzazione del webhook: %v", r)
	}

	// --- Associazione annullata: gli incidenti escono dall'applicazione (e dalle metriche).
	admin.do("PUT", snAPI+"/integrations/"+id+"/services/"+services["Ordini online"]["id"].(string), map[string]any{"status": "ignored"}, http.StatusOK)
	if l := list(admin.do("GET", snAPI+"/incidents?application="+ordID, nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("incidenti dopo l'associazione annullata: %v", l)
	}

	// --- ACL mancante: la prova dice quale tabella non si legge.
	fake.Deny("change_request")
	tr = admin.do("POST", snAPI+"/integrations/"+id+"/test", nil, http.StatusOK)
	if tr["ok"] != false || !strings.Contains(tr["message"].(string), "non si leggono: change_request") {
		t.Fatalf("prova con ACL mancante: %v", tr)
	}

	// --- Isolamento: l'altro tenant non vede nulla.
	if l := list(adminB.do("GET", snAPI+"/integrations", nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("integrazioni dell'altro tenant: %v", l)
	}
	if l := list(adminB.do("GET", snAPI+"/incidents", nil, http.StatusOK)["items"]); len(l) != 0 {
		t.Fatalf("incidenti dell'altro tenant: %v", l)
	}
	_ = fmt.Sprint
}
