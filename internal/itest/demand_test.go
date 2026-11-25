package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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

// user è un client HTTP con la propria sessione.
type user struct {
	t      *testing.T
	base   string
	c      *http.Client
	member string
}

func (u *user) do(method, path string, body any, wantStatus int) map[string]any {
	u.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
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
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		u.t.Fatalf("%s %s: stato %d, atteso %d: %s", method, path, resp.StatusCode, wantStatus, raw)
	}
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func login(t *testing.T, base, email, password string) *user {
	jar, _ := cookiejar.New(nil)
	u := &user{t: t, base: base, c: &http.Client{Jar: jar}}
	me := u.do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password}, http.StatusOK)
	u.member = me["active_tenant"].(map[string]any)["member_id"].(string)
	return u
}

func state(t *testing.T, u *user, key string) string {
	t.Helper()
	d := u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)
	return d["element"].(map[string]any)["state"].(string)
}

// La fetta verticale del demand (architecture.md §11.0.2): richiesta dal portale,
// triage, valutazione, approvazione a due livelli secondo soglia, query, storia, audit.
func TestDemandVerticalSlice(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := "slice-" + time.Now().Format("150405.000")
	slug = strings.ReplaceAll(slug, ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Slice", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
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
	add("richiedente", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	add("pmo", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	add("curioso", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()

	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	req := login(t, srv.URL, "richiedente@"+slug+".test", testPassword)
	man := login(t, srv.URL, "demandmanager@"+slug+".test", testPassword)
	spo := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	pmo := login(t, srv.URL, "pmo@"+slug+".test", testPassword)
	cur := login(t, srv.URL, "curioso@"+slug+".test", testPassword)

	// L'amministratore crea l'unità di business; il richiedente non può.
	req.do("POST", "/api/v1/org-units", map[string]any{"name": "Abusiva"}, http.StatusForbidden)
	unit := admin.do("POST", "/api/v1/org-units", map[string]any{"name": "Compliance", "kind": "business_unit"}, http.StatusCreated)

	// Il richiedente apre la business request dal portale (120 k€: due livelli di approvazione).
	created := req.do("POST", "/api/v1/demand/requests", map[string]any{
		"title":               "Adeguamento DORA: registro delle informazioni",
		"description":         "Registro dei fornitori ICT secondo l'art. 28.",
		"sponsor_id":          spo.member,
		"requesting_unit_id":  unit["id"],
		"driver":              "regulatory",
		"is_mandatory":        true,
		"regulatory_deadline": "2027-04-30",
		"fields":              map[string]any{"urgenza": "Alta", "benefici_attesi": "Conformità e meno rischio di sanzioni"},
	}, http.StatusCreated)
	key := created["key"].(string)
	id := created["id"].(string)
	if !strings.HasPrefix(key, "DEM-") || created["state"] != "Bozza" {
		t.Fatalf("richiesta creata male: %v", created)
	}

	// Validazioni: campo personalizzato inesistente e opzione sbagliata.
	req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "x", "fields": map[string]any{"colore": "rosso"}}, http.StatusUnprocessableEntity)
	req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "x", "fields": map[string]any{"urgenza": "Altissima"}}, http.StatusUnprocessableEntity)

	// Un altro richiedente non la vede, né nella lista né nel dettaglio.
	cur.do("GET", "/api/v1/elements/"+key, nil, http.StatusNotFound)
	if items := cur.do("GET", "/api/v1/elements?q=tipo:business_request", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("il richiedente estraneo vede %d richieste", len(items))
	}

	// Invio; il richiedente non può fare il triage.
	req.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	req.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Triage"}, http.StatusForbidden)

	// Il demand manager (via gruppo) porta avanti la richiesta; senza stima non si prioritizza.
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
	// In Triage il passaggio «avanti» è quello principale, non «Rimanda al richiedente»: l'interfaccia
	// promuove il primo con forward = vero.
	for _, tr := range man.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["transitions"].([]any) {
		m := tr.(map[string]any)
		if got, want := m["forward"] == true, m["to"] == "Valutazione"; got != want {
			t.Fatalf("passaggio %v: forward = %v, atteso %v", m["to"], got, want)
		}
	}
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Valutazione"}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Prioritizzazione"}, http.StatusUnprocessableEntity)
	man.do("PATCH", "/api/v1/demand/requests/"+id, map[string]any{"estimate_low_cents": 8_000_000, "estimate_high_cents": 12_000_000, "estimate_effort_days": 140}, http.StatusNoContent)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Prioritizzazione"}, http.StatusOK)
	// Il passaggio ad "Approvazione" apre la richiesta di approvazione (sponsor, poi PMO).
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Approvazione"}, http.StatusOK)
	// Nessuno può forzare l'esito saltando l'approvazione.
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Pianificazione"}, http.StatusUnprocessableEntity)

	detail := man.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)
	reqs := detail["approvals"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("richieste di approvazione: %d", len(reqs))
	}
	steps := reqs[0].(map[string]any)["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("passi di approvazione per 120 k€: %d, attesi 2 (sponsor, PMO)", len(steps))
	}
	step1 := steps[0].(map[string]any)["id"].(string)
	step2 := steps[1].(map[string]any)["id"].(string)

	// Il PMO non può decidere prima dello sponsor; il demand manager non può decidere affatto.
	pmo.do("POST", "/api/v1/approvals/steps/"+step2+"/decision", map[string]any{"decision": "approved"}, http.StatusConflict)
	man.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	if n := len(pmo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("inbox del PMO prima del suo turno: %d", n)
	}
	if n := len(spo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)); n != 1 {
		t.Fatalf("inbox dello sponsor: %d, attesa 1", n)
	}
	spo.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "approved", "comment": "Obbligo DORA"}, http.StatusNoContent)
	spo.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "approved"}, http.StatusConflict)
	if s := state(t, man, key); s != "Approvazione" {
		t.Fatalf("dopo il solo sponsor lo stato è %q", s)
	}
	if n := len(pmo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)); n != 1 {
		t.Fatalf("inbox del PMO dopo lo sponsor: %d, attesa 1", n)
	}
	if n := len(spo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("inbox dello sponsor dopo la sua decisione: %d, attesa 0", n)
	}
	pmo.do("POST", "/api/v1/approvals/steps/"+step2+"/decision", map[string]any{"decision": "approved_with_conditions", "conditions": "Rivedere la stima a fine analisi"}, http.StatusNoContent)
	if s := state(t, man, key); s != "Pianificazione" {
		t.Fatalf("dopo l'approvazione completa lo stato è %q, atteso Pianificazione", s)
	}

	// La query trova la richiesta approvata.
	items := admin.do("GET", "/api/v1/elements?q="+urlq("tipo: business_request stato: Pianificazione"), nil, http.StatusOK)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != key {
		t.Fatalf("query: %v", items)
	}
	admin.do("GET", "/api/v1/elements?q="+urlq("colore: rosso"), nil, http.StatusUnprocessableEntity)
	admin.do("GET", "/api/v1/elements?q="+urlq("stato: Inesistente"), nil, http.StatusUnprocessableEntity)
	if items := admin.do("GET", "/api/v1/elements?q="+urlq("stato: Pianificazione, Respinta tipo: business_request"), nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("query con più stati: %d risultati", len(items))
	}
	// Ricerca full-text con entrambe le strategie: scorrimento ordinato e indice GIN.
	for _, dense := range []float64{0, 2} { // 0: sempre scorrimento; 2: sempre indice GIN
		old := engine.FTSDenseFrequency
		engine.FTSDenseFrequency = dense
		items := admin.do("GET", "/api/v1/elements?q="+urlq("registro informazioni"), nil, http.StatusOK)["items"].([]any)
		inState := admin.do("GET", "/api/v1/elements?q="+urlq("registro informazioni stato: Pianificazione"), nil, http.StatusOK)["items"].([]any)
		none := admin.do("GET", "/api/v1/elements?q="+urlq("registro stato: Triage"), nil, http.StatusOK)["items"].([]any)
		engine.FTSDenseFrequency = old
		if len(items) != 1 || len(inState) != 1 || len(none) != 0 {
			t.Fatalf("ricerca full-text (soglia %v): %d, %d con stato, %d con stato sbagliato", dense, len(items), len(inState), len(none))
		}
	}

	// Storia: creazione, stati, approvazioni.
	hist := admin.do("GET", "/api/v1/elements/"+id+"/activity", nil, http.StatusOK)["items"].([]any)
	kinds := map[string]int{}
	for _, h := range hist {
		kinds[h.(map[string]any)["kind"].(string)]++
	}
	if kinds["created"] != 1 || kinds["state"] != 6 || kinds["approval"] != 3 {
		t.Fatalf("storia inattesa: %v", kinds)
	}

	// Seconda richiesta da 30 k€: basta lo sponsor; il rifiuto chiede una motivazione.
	small := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Firma digitale dei contratti", "sponsor_id": spo.member}, http.StatusCreated)
	sid := small["id"].(string)
	req.do("POST", "/api/v1/elements/"+sid+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	for _, to := range []string{"Triage", "Valutazione"} {
		man.do("POST", "/api/v1/elements/"+sid+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	man.do("PATCH", "/api/v1/demand/requests/"+sid, map[string]any{"estimate_low_cents": 2_000_000, "estimate_high_cents": 3_000_000}, http.StatusNoContent)
	man.do("POST", "/api/v1/elements/"+sid+"/transitions", map[string]any{"to": "Prioritizzazione"}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+sid+"/transitions", map[string]any{"to": "Approvazione"}, http.StatusOK)
	inbox := spo.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)
	if len(inbox) != 1 {
		t.Fatalf("inbox sponsor per la seconda richiesta: %d", len(inbox))
	}
	only := inbox[0].(map[string]any)["step_id"].(string)
	spo.do("POST", "/api/v1/approvals/steps/"+only+"/decision", map[string]any{"decision": "rejected"}, http.StatusUnprocessableEntity)
	spo.do("POST", "/api/v1/approvals/steps/"+only+"/decision", map[string]any{"decision": "rejected", "comment": "Non prioritario quest'anno"}, http.StatusNoContent)
	if s := state(t, man, small["key"].(string)); s != "Respinta" {
		t.Fatalf("dopo il rifiuto lo stato è %q", s)
	}

	// La catena di audit del tenant è integra dopo tutto il flusso.
	var broken *int64
	if err := d.Owner.QueryRow(ctx, "select audit_verify($1)", ten.TenantID).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != nil {
		t.Fatalf("catena di audit rotta al seq %d", *broken)
	}
}

func urlq(s string) string {
	r := strings.NewReplacer(" ", "%20", ":", "%3A", ",", "%2C")
	return r.Replace(s)
}
