package itest

import (
	"context"
	"io"
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
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Ondata 3, Vista DPO (replica/usabilita/governo.md, C8; migrazione 0064): registro dei trattamenti,
// DPIA con rischi e controlli, violazioni dei dati come filtro sugli incidenti con la scadenza delle
// 72 ore, flussi di dati, applicazioni con dati personali senza trattamento e isolamento tra tenant.
func TestOndata3VistaDPO(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, slugB := "dpo-"+stamp, "dpob-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	for _, s := range []string{slug, slugB} {
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: s, Name: "DPO " + s, AdminEmail: "admin@" + s + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
			t.Fatal(err)
		}
	}
	for n, role := range map[string]string{"dpo": "dpo", "viewer": "viewer", "ed": "editor"} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: n + "@" + slug + ".test", Name: n, Password: testPassword, Roles: []string{role}}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	rk := risk.Register(eng, ap)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	gs := grc.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Work: wk, Risk: rk, Reviews: rev, Requirements: rq, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	admin, dpo, viewer, ed := login(t, srv.URL, "admin@"+slug+".test", testPassword), login(t, srv.URL, "dpo@"+slug+".test", testPassword),
		login(t, srv.URL, "viewer@"+slug+".test", testPassword), login(t, srv.URL, "ed@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	eid := func(key string) string {
		return obj(admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"])["id"].(string)
	}

	// --- Catalogo: quattro applicazioni con dati diversi.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Ordini"}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"ORD;Gestione ordini;alta;sì;personali;Ordini;Commercio elettronico\n" +
		"PAY;Pagamenti;alta;no;finanziari;Ordini;Commercio elettronico\n" +
		"AUT;Accessi;media;no;credenziali;Ordini;Commercio elettronico\n" +
		"BLG;Blog;bassa;no;pubblici;Ordini;Commercio elettronico\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	ord, pay, blg := eid("ORD"), eid("PAY"), eid("BLG")
	admin.do("POST", "/api/v1/architecture/relations", map[string]any{"type": "calls_api", "source": "ORD", "target": "PAY", "attrs": map[string]any{"data": "ordini e importi"}}, http.StatusCreated)

	// --- Permessi: il lettore non vede la privacy, l'editor la vede? No: serve privacy.read.
	viewer.do("GET", "/api/v1/privacy/home", nil, http.StatusForbidden)
	ed.do("GET", "/api/v1/privacy/treatments", nil, http.StatusForbidden)
	ed.do("POST", "/api/v1/privacy/treatments", map[string]any{"name": "x"}, http.StatusForbidden)
	h := dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)
	un := list(h["unlinked"])
	if len(un) != 1 || obj(un[0])["name"] != "Gestione ordini" || obj(un[0])["personal"] != true {
		t.Fatalf("applicazioni con dati personali senza trattamento (solo Gestione ordini): %s", jsonString(h["unlinked"]))
	}

	// --- Trattamento: nome obbligatorio, base giuridica vuota segnalata, DPIA richiesta per i dati particolari.
	dpo.do("POST", "/api/v1/privacy/treatments", map[string]any{"name": " "}, http.StatusUnprocessableEntity)
	tr := dpo.do("POST", "/api/v1/privacy/treatments", map[string]any{
		"name": "Gestione degli ordini online", "purpose": "Evadere gli ordini dei clienti.", "data_subjects": []string{"Clienti", "clienti"},
		"data_categories": []string{"Anagrafica", "Indirizzo di consegna"}, "special_categories": true, "applications": []string{ord, pay},
		"retention": "10 anni", "transfers_extra_eu": true, "transfer_details": "Server di supporto negli Stati Uniti.",
	}, http.StatusCreated)
	tid := tr["id"].(string)
	if issues := list(tr["issues"]); len(issues) != 2 || issues[0] != "no_basis" || issues[1] != "dpia_missing" || tr["dpia_required"] != true {
		t.Fatalf("segnalazioni del trattamento: %s", jsonString(tr["issues"]))
	}
	if ds := list(tr["data_subjects"]); len(ds) != 1 {
		t.Fatalf("i doppioni delle categorie si tolgono: %s", jsonString(ds))
	}
	dpo.do("POST", "/api/v1/privacy/treatments", map[string]any{"name": "gestione degli ordini ONLINE"}, http.StatusUnprocessableEntity)
	dpo.do("POST", "/api/v1/privacy/treatments", map[string]any{"name": "Con base falsa", "legal_basis": "boh"}, http.StatusUnprocessableEntity)
	dpo.do("POST", "/api/v1/privacy/treatments", map[string]any{"name": "Applicazione inesistente", "applications": []string{blg, "non-un-id"}}, http.StatusUnprocessableEntity)
	h = dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)
	c := obj(h["counts"])
	if c["treatments"] != float64(1) || c["no_basis"] != float64(1) || c["dpia_missing"] != float64(1) || c["unlinked_apps"] != float64(0) {
		t.Fatalf("home dopo il trattamento: %s", jsonString(c))
	}
	// Con la base giuridica la segnalazione sparisce.
	tr = dpo.do("PUT", "/api/v1/privacy/treatments/"+tid, map[string]any{
		"name": "Gestione degli ordini online", "purpose": "Evadere gli ordini dei clienti.", "legal_basis": "contract", "special_categories": true,
		"data_subjects": []string{"Clienti"}, "applications": []string{ord, pay}, "retention": "10 anni", "transfers_extra_eu": true, "transfer_details": "Server di supporto negli Stati Uniti.",
	}, http.StatusOK)
	if issues := list(tr["issues"]); len(issues) != 1 || issues[0] != "dpia_missing" {
		t.Fatalf("segnalazioni dopo la base giuridica: %s", jsonString(tr["issues"]))
	}

	// --- DPIA: rischio e controllo collegati, in revisione, parere, approvazione, riapertura.
	dpo.do("GET", "/api/v1/privacy/treatments/"+tid+"/dpia", nil, http.StatusNotFound)
	rsk := admin.do("POST", "/api/v1/risks", map[string]any{"title": "Accesso non autorizzato ai dati dei clienti", "category": "Privacy", "inherent_likelihood": 3, "inherent_impact": 4}, http.StatusCreated)
	admin.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusCreated)
	ctl := admin.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Cifratura dei dati a riposo", "text": "Cifratura dei dati personali.",
		"requirements": []any{map[string]any{"ref": "dora:Art. 9"}}}, http.StatusCreated)
	riskID, ctlID := eid(rsk["key"].(string)), eid(ctl["key"].(string))
	dpo.do("PUT", "/api/v1/privacy/treatments/"+tid+"/dpia", map[string]any{"risk_ids": []string{ctlID}}, http.StatusUnprocessableEntity) // un controllo non è un rischio
	dp := dpo.do("PUT", "/api/v1/privacy/treatments/"+tid+"/dpia", map[string]any{"description": "Trattamento su larga scala di dati dei clienti.",
		"necessity": "Necessario per eseguire il contratto.", "risk_assessment": "Rischio di accesso non autorizzato.", "risk_ids": []string{riskID}, "control_ids": []string{ctlID}}, http.StatusOK)
	if dp["status"] != "draft" || len(list(dp["risks"])) != 1 || len(list(dp["controls"])) != 1 || obj(list(dp["controls"])[0])["detail"] != "not_assessed" {
		t.Fatalf("DPIA in bozza: %s", jsonString(dp))
	}
	h = dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)
	if c := obj(h["counts"]); c["dpia_missing"] != float64(0) || c["dpia_required"] != float64(1) {
		t.Fatalf("la DPIA c'è: %s", jsonString(c))
	}
	post := "/api/v1/privacy/treatments/" + tid + "/dpia/transition"
	dpo.do("POST", post, map[string]any{"to": "approved"}, http.StatusUnprocessableEntity) // dalla bozza non si approva
	dp = dpo.do("POST", post, map[string]any{"to": "in_review"}, http.StatusOK)
	if dp["status"] != "in_review" {
		t.Fatalf("in revisione: %s", jsonString(dp))
	}
	if n := obj(dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)["counts"])["dpia_in_review"]; n != float64(1) {
		t.Fatalf("la home conta la DPIA in revisione: %v", n)
	}
	dpo.do("POST", post, map[string]any{"to": "approved"}, http.StatusUnprocessableEntity) // serve il parere
	dpo.do("PUT", "/api/v1/privacy/treatments/"+tid+"/dpia", map[string]any{"description": "d", "risk_assessment": "r", "dpo_opinion": "Parere favorevole con le misure indicate.",
		"risk_ids": []string{riskID}, "control_ids": []string{ctlID}}, http.StatusOK)
	if dp = dpo.do("POST", post, map[string]any{"to": "approved"}, http.StatusOK); dp["status"] != "approved" || dp["approved_by"] == nil {
		t.Fatalf("approvata: %s", jsonString(dp))
	}
	dpo.do("PUT", "/api/v1/privacy/treatments/"+tid+"/dpia", map[string]any{"description": "cambio"}, http.StatusUnprocessableEntity)
	dpo.do("POST", post, map[string]any{"to": "in_review"}, http.StatusOK)
	if items := list(dpo.do("GET", "/api/v1/privacy/dpias", nil, http.StatusOK)["items"]); len(items) != 1 || obj(items[0])["risks"] != float64(1) {
		t.Fatalf("elenco delle DPIA: %s", jsonString(items))
	}

	// --- Violazioni: gli incidenti che toccano dati personali, con la scadenza delle 72 ore.
	mk := func(title string, ago time.Duration, apps ...string) string {
		t.Helper()
		inc := admin.do("POST", "/api/v1/incidents", map[string]any{"title": title, "description": "Creato dai test.",
			"detected_at": time.Now().Add(-ago).UTC().Format(time.RFC3339), "impacted": apps}, http.StatusCreated)
		return eid(inc["key"].(string))
	}
	fresh := mk("Esposizione ordini", 2*time.Hour, "ORD")
	late := mk("Accessi anomali agli ordini", 80*time.Hour, "ORD")
	other := mk("Blog fuori servizio", time.Hour, "BLG")
	br := list(dpo.do("GET", "/api/v1/privacy/breaches", nil, http.StatusOK)["items"])
	if len(br) != 2 {
		t.Fatalf("solo gli incidenti con dati personali: %s", jsonString(br))
	}
	if first := obj(br[0]); first["id"] != late || first["status"] != "overdue" || first["source"] != "application" || first["hours_left"].(float64) >= 0 {
		t.Fatalf("la scaduta viene prima: %s", jsonString(first))
	}
	if second := obj(br[1]); second["id"] != fresh || second["status"] != "open" || second["hours_left"].(float64) < 69 || second["hours_left"].(float64) > 71 {
		t.Fatalf("la recente ha circa 70 ore: %s", jsonString(second))
	}
	for _, b := range br {
		if obj(b)["id"] == other {
			t.Fatal("l'incidente del blog non è una violazione")
		}
	}
	if all := list(dpo.do("GET", "/api/v1/privacy/breaches?all=1", nil, http.StatusOK)["items"]); len(all) != 3 {
		t.Fatalf("tutti gli incidenti: %d", len(all))
	}
	if n := obj(dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)["counts"]); n["open_breaches"] != float64(2) || n["soon_breaches"] != float64(1) {
		t.Fatalf("home: violazioni aperte e vicine: %s", jsonString(n))
	}
	// Segno manuale sul blog e collegamento al trattamento; la notifica registrata ferma l'orologio.
	bu := "/api/v1/privacy/breaches/" + other
	ed.do("PUT", bu, map[string]any{"personal": true}, http.StatusForbidden)
	dpo.do("PUT", bu, map[string]any{"personal": true, "note": "Form di contatto con nomi ed email.", "treatment_ids": []string{tid}}, http.StatusOK)
	if b := dpo.do("GET", bu, nil, http.StatusOK); b["source"] != "manual" || len(list(b["treatments"])) != 1 || b["status"] != "open" {
		t.Fatalf("segno manuale: %s", jsonString(b))
	}
	dpo.do("POST", bu+"/notification", map[string]any{"not_required": true}, http.StatusUnprocessableEntity)
	if b := dpo.do("POST", bu+"/notification", map[string]any{"not_required": true, "reason": "Solo nomi già pubblici."}, http.StatusOK); b["status"] != "not_required" {
		t.Fatalf("notifica non dovuta: %s", jsonString(b))
	}
	if b := dpo.do("POST", "/api/v1/privacy/breaches/"+late+"/notification", map[string]any{}, http.StatusOK); b["status"] != "notified_late" {
		t.Fatalf("notificata oltre le 72 ore: %s", jsonString(b))
	}
	if b := dpo.do("POST", "/api/v1/privacy/breaches/"+fresh+"/notification", map[string]any{}, http.StatusOK); b["status"] != "notified" {
		t.Fatalf("notificata nei termini: %s", jsonString(b))
	}
	dpo.do("POST", "/api/v1/privacy/breaches/"+fresh+"/notification", map[string]any{"notified_at": "2999-01-01T00:00:00Z"}, http.StatusUnprocessableEntity)
	dpo.do("PUT", "/api/v1/privacy/breaches/"+tid, map[string]any{"personal": true}, http.StatusNotFound) // non è un incidente
	if n := obj(dpo.do("GET", "/api/v1/privacy/home", nil, http.StatusOK)["counts"])["open_breaches"]; n != float64(0) {
		t.Fatalf("nessuna violazione aperta: %v", n)
	}

	// --- Flussi: il trattamento ha ORD e PAY e la dipendenza tra loro; l'applicazione con le credenziali non c'entra.
	fl := dpo.do("GET", "/api/v1/privacy/flows", nil, http.StatusOK)
	fts := list(fl["treatments"])
	if len(fts) != 1 || len(list(obj(fts[0])["nodes"])) != 2 || len(list(obj(fts[0])["edges"])) != 1 {
		t.Fatalf("flusso del trattamento: %s", jsonString(fl["treatments"]))
	}
	if e := obj(list(obj(fts[0])["edges"])[0]); e["data"] != "ordini e importi" {
		t.Fatalf("dati scambiati sul flusso: %s", jsonString(e))
	}
	if len(list(fl["unlinked"])) != 0 || fl["personal"] != float64(1) || fl["credentials"] != float64(1) || fl["financial"] != float64(1) {
		t.Fatalf("conteggi dei dati: %s", jsonString(fl))
	}
	// Se il trattamento perde Gestione ordini, l'applicazione torna tra quelle senza trattamento.
	dpo.do("PUT", "/api/v1/privacy/treatments/"+tid, map[string]any{"name": "Gestione degli ordini online", "legal_basis": "contract", "applications": []string{pay}}, http.StatusOK)
	if un := list(dpo.do("GET", "/api/v1/privacy/flows", nil, http.StatusOK)["unlinked"]); len(un) != 1 {
		t.Fatalf("Gestione ordini senza trattamento: %s", jsonString(un))
	}
	dpo.do("PUT", "/api/v1/privacy/treatments/"+tid, map[string]any{"name": "Gestione degli ordini online", "legal_basis": "contract", "purpose": "Evadere gli ordini", "applications": []string{ord, pay}}, http.StatusOK)

	// --- CSV del registro.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/privacy/treatments.csv", nil)
	resp, err := dpo.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(raw)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("CSV: %d %q", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Esecuzione di un contratto") || !strings.Contains(body, "Gestione ordini, Pagamenti") {
		t.Fatalf("CSV senza base giuridica o applicazioni: %q", body)
	}

	// --- RLS: l'altro tenant non vede niente e non raggiunge il trattamento per id.
	if items := list(adminB.do("GET", "/api/v1/privacy/treatments", nil, http.StatusOK)["items"]); len(items) != 0 {
		t.Fatalf("l'altro tenant vede i trattamenti: %s", jsonString(items))
	}
	adminB.do("GET", "/api/v1/privacy/treatments/"+tid, nil, http.StatusNotFound)
	adminB.do("PUT", "/api/v1/privacy/treatments/"+tid, map[string]any{"name": "Rubato"}, http.StatusNotFound)
	adminB.do("GET", "/api/v1/privacy/treatments/"+tid+"/dpia", nil, http.StatusNotFound)
	if items := list(adminB.do("GET", "/api/v1/privacy/dpias", nil, http.StatusOK)["items"]); len(items) != 0 {
		t.Fatalf("l'altro tenant vede le DPIA: %s", jsonString(items))
	}
	adminB.do("GET", "/api/v1/privacy/breaches/"+late, nil, http.StatusNotFound)
	var n int
	if err := d.Owner.QueryRow(ctx, `select count(*) from privacy_treatments`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("righe nel database: %d %v", n, err)
	}

	// --- Elimina: il trattamento se ne va con la DPIA.
	ed.do("DELETE", "/api/v1/privacy/treatments/"+tid, nil, http.StatusForbidden)
	dpo.do("DELETE", "/api/v1/privacy/treatments/"+tid, nil, http.StatusNoContent)
	if err := d.Owner.QueryRow(ctx, `select count(*) from privacy_dpias`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la DPIA segue il trattamento: %d %v", n, err)
	}
}
