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
	"regia/internal/modules/finance"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/report"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Catalogo del portale (ondata 3): una voce con il suo modulo a passi, la bozza di modifica che si pubblica, la
// condizione di visibilità di un campo, l'instradamento al team, una sola bozza per voce e utente, «È questa» e
// l'isolamento tra tenant.
func TestCatalogoPortale(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug, other := "cat-"+stamp, "cao-"+stamp
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision, reviews.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Catalogo", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test", AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	members := map[string]string{}
	for _, m := range []struct{ email, typ, role string }{{"req", "requester", "requester"}, {"curioso", "requester", "requester"}, {"ref", "", "viewer"}} {
		id, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.email,
			Password: testPassword, MemberType: m.typ, Roles: []string{m.role}})
		if err != nil {
			t.Fatal(err)
		}
		members[m.email] = id
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rep := report.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Report: rep,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)
	req := login(t, srv.URL, "req@"+slug+".test", testPassword)
	curioso := login(t, srv.URL, "curioso@"+slug+".test", testPassword)

	// Senza voci salvate il catalogo è quello di sempre: una voce per il tipo esposto, con il suo modulo.
	pt := req.do("GET", "/api/v1/portal", nil, http.StatusOK)
	if es := list(pt["entries"]); len(es) != 1 || obj(es[0])["key"] != "business_request" || len(list(obj(es[0])["steps"])) < 2 {
		t.Fatalf("catalogo iniziale: %v", pt["entries"])
	}
	req.do("GET", "/api/v1/portal/catalog", nil, http.StatusForbidden)
	req.do("POST", "/api/v1/portal/catalog", map[string]any{"label": "Abuso", "type": "business_request"}, http.StatusForbidden)
	if cfg := admin.do("GET", "/api/v1/portal/catalog", nil, http.StatusOK); cfg["stored"] != false || len(list(cfg["entries"])) != 1 {
		t.Fatalf("configurazione iniziale: %v", cfg)
	}

	// Un tipo «Accesso» con due campi e un team con il suo referente.
	team := admin.do("POST", "/api/v1/teams", map[string]any{"name": "Accessi", "lead_id": members["ref"]}, http.StatusCreated)
	teamID, _ := team["id"].(string)
	if teamID == "" {
		t.Fatalf("team: %v", team)
	}
	admin.do("POST", "/api/v1/types", map[string]any{"key": "accesso", "name": "Accesso", "from": "business_request", "module": "strategy"}, http.StatusCreated)
	sistema := admin.do("POST", "/api/v1/fields", map[string]any{"key": "sistema", "name": "Sistema", "data_type": "enum",
		"options": []map[string]any{{"name": "ERP"}, {"name": "CRM"}}}, http.StatusCreated)
	optID := map[string]string{}
	for _, o := range list(sistema["options"]) {
		optID[obj(o)["name"].(string)] = obj(o)["id"].(string)
	}
	admin.do("POST", "/api/v1/fields", map[string]any{"key": "motivo", "name": "Motivo", "data_type": "string"}, http.StatusCreated)
	for _, f := range []string{"sistema", "motivo"} {
		admin.do("PUT", "/api/v1/fields/"+f+"/types/accesso", map[string]any{}, http.StatusOK)
	}

	// La voce nasce con il modulo di sempre del tipo; il catalogo di prima non sparisce.
	e := admin.do("POST", "/api/v1/portal/catalog", map[string]any{"label": "Accesso a un sistema", "description": "Chiedi l'accesso a un'applicazione.",
		"category": "Accessi", "type": "accesso"}, http.StatusCreated)
	key, _ := e["key"].(string)
	if key != "accesso-a-un-sistema" || e["active"] != true {
		t.Fatalf("voce creata: %v", e)
	}
	admin.do("POST", "/api/v1/portal/catalog", map[string]any{"label": "Senza tipo", "type": "inesistente"}, http.StatusUnprocessableEntity)
	admin.do("POST", "/api/v1/portal/catalog", map[string]any{"label": "", "type": "accesso"}, http.StatusUnprocessableEntity)
	admin.do("POST", "/api/v1/portal/catalog", map[string]any{"key": key, "label": "Doppia", "type": "accesso"}, http.StatusUnprocessableEntity)
	if cfg := admin.do("GET", "/api/v1/portal/catalog", nil, http.StatusOK); cfg["stored"] != true || len(list(cfg["entries"])) != 2 {
		t.Fatalf("catalogo salvato: %v", cfg["entries"])
	}

	// Modulo in bozza: non lo vede nessuno finché non si pubblica. Controlli sul modulo.
	form := map[string]any{
		"intro":       "Compila i dati dell'accesso.",
		"title_label": "Che accesso ti serve?",
		"steps": []map[string]any{
			{"title": "Chi e cosa", "fields": []map[string]any{{"key": "sistema", "label": "Sistema richiesto", "required": true, "help": "Scegli l'applicazione."}}},
			{"title": "Motivazione", "fields": []map[string]any{{"key": "motivo", "required": true, "visible_when": "sistema: ERP"}}},
		},
		"routes": []map[string]any{{"when": "sistema: ERP", "role": "demand_manager", "team_id": teamID, "priority": "high", "label": "Accessi"}},
	}
	bad := func(mut func(f map[string]any)) {
		t.Helper()
		f := map[string]any{}
		for k, v := range form {
			f[k] = v
		}
		mut(f)
		admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"form": f}, http.StatusUnprocessableEntity)
	}
	bad(func(f map[string]any) {
		f["steps"] = []map[string]any{{"fields": []map[string]any{{"key": "inesistente"}}}}
	})
	bad(func(f map[string]any) {
		f["steps"] = []map[string]any{{"fields": []map[string]any{{"key": "sistema"}, {"key": "sistema"}}}}
	})
	bad(func(f map[string]any) {
		f["steps"] = []map[string]any{{"fields": []map[string]any{{"key": "sistema", "visible_when": "boh boh"}}}}
	})
	bad(func(f map[string]any) {
		f["routes"] = []map[string]any{{"when": "sistema: ERP", "role": "demand_manager"}}
	})
	bad(func(f map[string]any) { f["routes"] = []map[string]any{{"role": "non_esiste", "team_id": teamID}} })
	bad(func(f map[string]any) {
		f["routes"] = []map[string]any{{"role": "demand_manager", "team_id": teamID, "priority": "urgentissima"}}
	})
	bad(func(f map[string]any) {
		f["steps"] = []map[string]any{{"fields": []map[string]any{{"key": "driver", "std": true}}}} // dato della domanda: solo per le richieste
	})
	admin.do("POST", "/api/v1/portal/catalog/"+key+"/publish", nil, http.StatusUnprocessableEntity) // nulla da pubblicare
	saved := admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"form": form}, http.StatusOK)
	if saved["draft"] == nil {
		t.Fatalf("bozza non salvata: %v", saved)
	}
	prev := admin.do("POST", "/api/v1/portal/catalog/"+key+"/preview", form, http.StatusOK)
	if st := list(obj(prev["entry"])["steps"]); len(st) != 2 || obj(list(obj(st[0])["fields"])[0])["label"] != "Sistema richiesto" {
		t.Fatalf("anteprima: %v", prev)
	}
	entryOf := func(u *user, k string) map[string]any {
		t.Helper()
		for _, x := range list(u.do("GET", "/api/v1/portal", nil, http.StatusOK)["entries"]) {
			if obj(x)["key"] == k {
				return obj(x)
			}
		}
		return nil
	}
	if v := entryOf(req, key); v == nil || obj(list(v["steps"])[0])["title"] == "Chi e cosa" {
		t.Fatalf("la bozza si vede prima della pubblicazione: %v", v)
	}
	admin.do("DELETE", "/api/v1/portal/catalog/"+key+"/draft", nil, http.StatusOK)
	if got := admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"form": form}, http.StatusOK); got["draft"] == nil {
		t.Fatal("bozza da ripubblicare")
	}
	admin.do("POST", "/api/v1/portal/catalog/"+key+"/publish", nil, http.StatusOK)
	v := entryOf(req, key)
	if v == nil || len(list(v["steps"])) != 2 || v["title_label"] != "Che accesso ti serve?" || v["type"] != "accesso" {
		t.Fatalf("voce pubblicata: %v", v)
	}

	// Attivazione e gruppo: valgono subito. Una voce disattivata sparisce dal portale e non accetta bozze.
	admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"active": false}, http.StatusOK)
	if entryOf(req, key) != nil {
		t.Fatal("voce disattivata ancora visibile")
	}
	req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": key, "type": "accesso", "title": "x"}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"active": true}, http.StatusOK)
	admin.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"group_id": "non-un-gruppo"}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/portal/catalog/order", map[string]any{"keys": []string{key, "business_request"}}, http.StatusNoContent)
	if es := list(req.do("GET", "/api/v1/portal", nil, http.StatusOK)["entries"]); obj(es[0])["key"] != key {
		t.Fatalf("ordine delle voci: %v", es)
	}

	// Una sola bozza per voce e utente; un'altra voce ha la sua.
	d1 := req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": key, "type": "accesso", "title": "Accesso al gestionale", "step": 1}, http.StatusCreated)
	d2 := req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": key, "type": "accesso", "title": "Accesso al gestionale (bis)"}, http.StatusCreated)
	if d1["id"] != d2["id"] || d1["entry"] != key {
		t.Fatalf("due bozze per la stessa voce: %v %v", d1, d2)
	}
	d3 := req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": "business_request", "type": "business_request", "title": "Altro"}, http.StatusCreated)
	if d3["id"] == d1["id"] {
		t.Fatal("la bozza di un'altra voce non è separata")
	}
	req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": key, "type": "business_request", "title": "Tipo sbagliato"}, http.StatusUnprocessableEntity)
	var n int
	if err := d.Owner.QueryRow(ctx, `select count(*) from portal_drafts where tenant_id = $1 and member_id = $2 and entry_key = $3`, ten.TenantID, members["req"], key).Scan(&n); err != nil || n != 1 {
		t.Fatalf("bozze per voce: %d %v", n, err)
	}
	if n := len(list(curioso.do("GET", "/api/v1/portal", nil, http.StatusOK)["drafts"])); n != 0 {
		t.Fatalf("le bozze di un altro: %d", n)
	}

	// Invio: il campo condizionale obbligatorio si chiede solo se la condizione è vera; quello nascosto si scarta.
	save := func(fields map[string]any) {
		t.Helper()
		req.do("PUT", "/api/v1/portal/drafts/"+d1["id"].(string), map[string]any{"entry": key, "type": "accesso", "title": "Accesso al gestionale",
			"data": map[string]any{"description": "Mi serve per il lavoro.", "fields": fields}}, http.StatusOK)
	}
	save(map[string]any{"sistema": optID["ERP"]})
	req.do("POST", "/api/v1/portal/drafts/"+d1["id"].(string)+"/submit", nil, http.StatusUnprocessableEntity) // manca il motivo
	save(map[string]any{"sistema": optID["ERP"], "motivo": "Chiusura contabile"})
	el := req.do("POST", "/api/v1/portal/drafts/"+d1["id"].(string)+"/submit", nil, http.StatusCreated)
	if el["type"] != "accesso" || el["key"] == "" {
		t.Fatalf("elemento inviato: %v", el)
	}
	// Instradamento: il ruolo va al referente del team, con priorità ed etichetta.
	var holder, tags string
	if err := d.Owner.QueryRow(ctx, `select coalesce(string_agg(m.display_name, ','), '') from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'demand_manager'
		join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id where sh.element_id = $1`, el["id"]).Scan(&holder); err != nil || holder != "ref" {
		t.Fatalf("instradamento al team: %q %v", holder, err)
	}
	if err := d.Owner.QueryRow(ctx, `select coalesce(string_agg(t.name, ',' order by t.name), '') from element_tags et join tags t on t.tenant_id = et.tenant_id and t.id = et.tag_id
		where et.element_id = $1`, el["id"]).Scan(&tags); err != nil || tags != "Accessi,Priorità alta" {
		t.Fatalf("etichette iniziali: %q %v", tags, err)
	}
	if n := len(list(req.do("GET", "/api/v1/portal", nil, http.StatusOK)["drafts"])); n != 1 { // resta solo quella dell'altra voce
		t.Fatalf("bozze dopo l'invio: %d", n)
	}
	// Con CRM il motivo non serve e, se c'è, si scarta; la regola non scatta.
	d4 := req.do("POST", "/api/v1/portal/drafts", map[string]any{"entry": key, "type": "accesso", "title": "Accesso al CRM",
		"data": map[string]any{"fields": map[string]any{"sistema": optID["CRM"], "motivo": "da scartare"}}}, http.StatusCreated)
	el2 := req.do("POST", "/api/v1/portal/drafts/"+d4["id"].(string)+"/submit", nil, http.StatusCreated)
	var motivo *string
	if err := d.Owner.QueryRow(ctx, `select fields->>'motivo' from elements where id = $1`, el2["id"]).Scan(&motivo); err != nil || motivo != nil {
		t.Fatalf("campo nascosto salvato: %v %v", motivo, err)
	}
	if err := d.Owner.QueryRow(ctx, `select count(*) from element_tags where element_id = $1`, el2["id"]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la regola non doveva scattare: %d %v", n, err)
	}

	// «È questa»: chi non può leggere la richiesta altrui non la raggiunge; chi può, si aggiunge e commenta.
	curioso.do("POST", "/api/v1/portal/support/"+el["key"].(string), map[string]any{"comment": "Anche io"}, http.StatusNotFound)
	admin.do("POST", "/api/v1/portal/support/"+el["key"].(string), map[string]any{"comment": ""}, http.StatusUnprocessableEntity)
	if got := admin.do("POST", "/api/v1/portal/support/"+el["key"].(string), map[string]any{"comment": "Ho la stessa esigenza."}, http.StatusOK); got["key"] != el["key"] {
		t.Fatalf("È questa: %v", got)
	}
	found := false
	for _, r := range list(admin.do("GET", "/api/v1/portal", nil, http.StatusOK)["requests"]) {
		found = found || obj(r)["key"] == el["key"]
	}
	if !found {
		t.Fatal("la richiesta sostenuta non è tra le mie")
	}
	if err := d.Owner.QueryRow(ctx, `select count(*) from comments where element_id = $1 and body_text = 'Ho la stessa esigenza.'`, el["id"]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("commento di sostegno: %d %v", n, err)
	}

	// Isolamento: l'altro tenant non vede né la voce né le bozze; RLS sulla tabella.
	cfgB := adminB.do("GET", "/api/v1/portal/catalog", nil, http.StatusOK)
	if cfgB["stored"] != false || len(list(cfgB["entries"])) != 1 {
		t.Fatalf("catalogo dell'altro tenant: %v", cfgB["entries"])
	}
	adminB.do("PUT", "/api/v1/portal/catalog/"+key, map[string]any{"active": false}, http.StatusNotFound)
	var rls bool
	if err := d.Owner.QueryRow(ctx, `select relrowsecurity and relforcerowsecurity from pg_class where relname = 'portal_entries'`).Scan(&rls); err != nil || !rls {
		t.Fatalf("RLS su portal_entries: %v %v", rls, err)
	}
}
