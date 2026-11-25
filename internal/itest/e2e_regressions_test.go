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
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Regressioni dei bug trovati dai test end-to-end dell'interfaccia (web/e2e).
func TestE2ERegressions(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("e2e-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Regressioni e2e", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
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
		{"sponsor", tenant.MemberInput{Roles: []string{"viewer"}}},
	} {
		in := m.in
		in.TenantSlug, in.Email, in.Name, in.Password = slug, m.name+"@"+slug+".test", "Persona "+m.name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: architecture.Register(eng),
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	req := login(t, srv.URL, "richiedente@"+slug+".test", testPassword)
	man := login(t, srv.URL, "dm@"+slug+".test", testPassword)
	spo := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)

	created := req.do("POST", "/api/v1/demand/requests", map[string]any{
		"title": "Badge elettronici per i visitatori", "sponsor_id": spo.member, "fields": map[string]any{"urgenza": "Media"},
	}, http.StatusCreated)
	id, key := created["id"].(string), created["key"].(string)

	// Bug 1: una chiave (DEM-1) dove serve l'id finiva in un 500 ("invalid input syntax
	// for type uuid"). È un errore del client: 400.
	p := req.do("POST", "/api/v1/elements/"+key+"/transitions", map[string]any{"to": "Inviata"}, http.StatusBadRequest)
	if p["code"] != "request.invalid_id" {
		t.Fatalf("codice dell'errore per un id malformato: %v", p)
	}
	man.do("PATCH", "/api/v1/impacts/non-un-uuid", map[string]any{"estimate_effort_days": 3}, http.StatusBadRequest)

	// Bug 2: con un campo obbligatorio da "Pianificazione" ancora vuoto, la richiesta
	// d'approvazione si apriva e poi lo sponsor, approvando, riceveva un 422 su un campo che
	// non può compilare. Ora "Chiedi l'approvazione" è bloccato con il motivo.
	admin.do("POST", "/api/v1/fields", map[string]any{"key": "golive", "name": "Data go-live richiesta", "data_type": "date"}, http.StatusCreated)
	admin.do("PUT", "/api/v1/fields/golive/types/business_request", map[string]any{"required_from": "Pianificazione"}, http.StatusOK)
	req.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	for _, to := range []string{"Triage", "Valutazione"} {
		man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	man.do("PATCH", "/api/v1/demand/requests/"+id, map[string]any{"estimate_low_cents": 2_000_000, "estimate_high_cents": 3_000_000}, http.StatusNoContent)
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Prioritizzazione"}, http.StatusOK)

	blocked := func() []any {
		for _, tr := range man.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["transitions"].([]any) {
			if m := tr.(map[string]any); m["to"] == "Approvazione" {
				return m["blocked"].([]any)
			}
		}
		t.Fatal("passaggio ad Approvazione non offerto")
		return nil
	}
	if b := blocked(); len(b) != 1 || !strings.Contains(b[0].(string), "l'approvazione porta a «Pianificazione»: prima compila Data go-live richiesta") {
		t.Fatalf("motivo del blocco: %v", b)
	}
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Approvazione"}, http.StatusUnprocessableEntity)
	el := man.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"].(map[string]any)
	man.do("PATCH", "/api/v1/elements/"+id, map[string]any{"version": el["version"], "fields": map[string]any{"golive": "2027-05-31"}}, http.StatusOK)
	if b := blocked(); len(b) != 0 {
		t.Fatalf("ancora bloccato dopo aver compilato il campo: %v", b)
	}
	man.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Approvazione"}, http.StatusOK)
	steps := man.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["approvals"].([]any)[0].(map[string]any)["steps"].([]any)
	spo.do("POST", "/api/v1/approvals/steps/"+steps[0].(map[string]any)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if s := state(t, man, key); s != "Pianificazione" {
		t.Fatalf("dopo l'approvazione dello sponsor: %s, attesa Pianificazione", s)
	}
}
