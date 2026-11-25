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

// Asset logici, applicazioni e impatti (piano, passo 2): import del catalogo da CSV,
// richiesta che cita un asset, impatti proposti dalla composizione, team owner,
// gate dell'analisi tecnica, ruoli del team sull'impatto, dipendenze.
func TestArchitectureAndImpacts(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("arc-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Architettura", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	}); err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("lead", tenant.MemberInput{Roles: []string{"viewer"}})
	add("dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("arch", tenant.MemberInput{Roles: []string{"architect"}})
	add("req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})

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
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	man := login(t, srv.URL, "dm@"+slug+".test", testPassword)
	archi := login(t, srv.URL, "arch@"+slug+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)

	// Il team Web esiste già con il suo referente; gli altri li crea l'import.
	man.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusForbidden)
	archi.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)

	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"FE-AR;Frontend area riservata;alta;sì;personali;Web;Area riservata\n" +
		"BE-CLI;Backend API clienti;critica;sì;personali;Integrazioni;Area riservata\n" +
		"PAY;Modulo pagamenti;alta;no;finanziari;Payments;Area riservata|Pagamenti\n" +
		"BAD;Applicazione sbagliata;boh;no;;Web;\n"
	man.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusForbidden)
	dry := archi.do("POST", "/api/v1/architecture/applications/import?dry_run=true", map[string]any{"csv": csv}, http.StatusOK)
	if dry["created"].(float64) != 3 || dry["errors"].(float64) != 1 {
		t.Fatalf("prova di import: %v", dry)
	}
	if n := len(archi.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("la prova ha salvato %d applicazioni", n)
	}
	rep := archi.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	if rep["created"].(float64) != 3 || rep["errors"].(float64) != 1 || len(rep["teams_created"].([]any)) != 2 || len(rep["assets_created"].([]any)) != 2 {
		t.Fatalf("import: %v", rep)
	}
	if rep := archi.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK); rep["updated"].(float64) != 3 {
		t.Fatalf("secondo import: %v", rep)
	}
	apps := map[string]map[string]any{}
	for _, a := range archi.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		apps[m["code"].(string)] = m
	}
	if apps["FE-AR"]["team"] != "Web" || apps["FE-AR"]["state"] != "In produzione" || len(apps["PAY"]["assets"].([]any)) != 2 {
		t.Fatalf("catalogo: %v", apps)
	}
	var areaID string
	for _, a := range archi.do("GET", "/api/v1/architecture/assets", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		if m["name"] == "Area riservata" {
			areaID = m["id"].(string)
			if n := len(m["components"].([]any)); n != 3 {
				t.Fatalf("componenti dell'area riservata: %d", n)
			}
		}
	}

	// Il richiedente apre la richiesta e cita l'asset logico (non conosce le applicazioni).
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuova area riservata clienti"}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	req.do("POST", "/api/v1/demand/requests/"+brKey+"/assets", map[string]any{"asset_id": areaID}, http.StatusNoContent)
	an := admin.do("GET", "/api/v1/demand/requests/"+brKey+"/analysis", nil, http.StatusOK)
	if len(an["assets"].([]any)) != 1 || len(an["proposed"].([]any)) != 3 {
		t.Fatalf("analisi: %v", an)
	}

	// Il demand manager (ruolo sulla richiesta) apre gli impatti; il richiedente no.
	req.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps["FE-AR"]["id"]}, http.StatusForbidden)
	fe := man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{
		"application_id": apps["FE-AR"]["id"], "change_kind": "change", "technical_note": "Nuove pagine del profilo"}, http.StatusCreated)
	if fe["team"] != "Web" || fe["state"] != "Da analizzare" || fe["request_key"] != brKey {
		t.Fatalf("impatto: %v", fe)
	}
	man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps["FE-AR"]["id"]}, http.StatusUnprocessableEntity)
	be := man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{
		"application_id": apps["BE-CLI"]["id"], "change_kind": "new", "depends_on": []string{}}, http.StatusCreated)
	be = man.do("PATCH", "/api/v1/impacts/"+be["id"].(string), map[string]any{"depends_on": []string{fe["key"].(string)}}, http.StatusOK)
	if deps := be["depends_on"].([]any); len(deps) != 1 || deps[0] != fe["key"] {
		t.Fatalf("dipendenze: %v", be["depends_on"])
	}
	if n := len(admin.do("GET", "/api/v1/demand/requests/"+brKey+"/analysis", nil, http.StatusOK)["proposed"].([]any)); n != 1 {
		t.Fatalf("proposte residue: %d", n)
	}

	// I fatti dell'architettura entrano nelle condizioni della richiesta.
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{
		"condition": `asset: "Area riservata" criticita_max: critica funzione_critica: sì impatti: 2 impatti_da_stimare: 2 team: Web`, "element": brKey}, http.StatusOK)
	if chk["result"] != true {
		t.Fatalf("fatti della richiesta: %v", chk["facts"])
	}

	// Gate dell'analisi tecnica sulla richiesta: servono le stime degli impatti.
	move := func(u *user, id, to string, status int) map[string]any {
		return u.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)
	man.do("PATCH", "/api/v1/demand/requests/"+brID, map[string]any{"estimate_high_cents": 9_000_000}, http.StatusNoContent)
	if r := move(man, brID, "Prioritizzazione", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "impatti") {
		t.Fatalf("gate dell'analisi: %v", r)
	}

	// Ciclo dell'impatto: analisi, stima, passaggio al team, accettazione del referente.
	feID := fe["id"].(string)
	move(man, feID, "In analisi", http.StatusOK)
	if r := move(man, feID, "Stimato", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "giornate") {
		t.Fatalf("gate della stima dell'impatto: %v", r)
	}
	man.do("PATCH", "/api/v1/impacts/"+feID, map[string]any{"estimate_effort_days": 12}, http.StatusOK)
	move(man, feID, "Stimato", http.StatusOK)
	move(man, feID, "Accettato", http.StatusForbidden)
	move(lead, feID, "Accettato", http.StatusOK)
	if d := lead.do("GET", "/api/v1/elements/"+fe["key"].(string), nil, http.StatusOK); d["impact"].(map[string]any)["state"] != "Accettato" {
		t.Fatalf("dettaglio dell'impatto: %v", d["impact"])
	}

	// Con anche il backend stimato il gate della richiesta passa.
	man.do("PATCH", "/api/v1/impacts/"+be["id"].(string), map[string]any{"estimate_effort_days": 20}, http.StatusOK)
	move(man, brID, "Prioritizzazione", http.StatusOK)

	// Il catalogo lo modifica solo chi ha architecture.write.
	man.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Abusiva"}, http.StatusForbidden)
	archi.do("PATCH", "/api/v1/architecture/applications/"+apps["PAY"]["id"].(string), map[string]any{"criticality": "media", "team_id": nil}, http.StatusOK)
}
