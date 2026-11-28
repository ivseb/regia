package itest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"regia/internal/ai"
	"regia/internal/api"
	"regia/internal/devtools/fakeai"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/projects"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Lo strumento «aggiorna» dell'assistente oltre a titolo e testo: assegnatario, responsabile del
// progetto, valori dei campi del tipo e passaggio di fase, sempre come proposta da confermare.
// Le persone arrivano per nome (anche come segnaposto mascherato), i campi per chiave o nome; una
// persona ambigua, un campo o un'opzione inesistenti tornano al modello come errore con le
// alternative. La versione si controlla alla conferma; lo stesso strumento vale nel server MCP.
func TestAIAggiorna(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("aiagg-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, projects.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Aggiorna " + slug, AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, name, role string }{
		{"ed", "Edoardo Neri", "editor"}, {"mario.rossi", "Mario Rossi", "editor"}, {"mario.bianchi", "Mario Bianchi", "editor"},
		{"luca", "Luca Verdi", "editor"}, {"vista", "Vittoria Sola", "viewer"},
	} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.name,
			Password: testPassword, Roles: []string{m.role}}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: demand.Register(eng, ap), Arch: architecture.Register(eng),
		Work: work.Register(eng), Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	ed := login(t, srv.URL, "ed@"+slug+".test", testPassword)
	vista := login(t, srv.URL, "vista@"+slug+".test", testPassword)
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	list := func(v any) []any { l, _ := v.([]any); return l }
	memberID := func(name string) string {
		var id string
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select id::text from members where display_name = $1`, name).Scan(&id)
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mario, luca := memberID("Mario Rossi"), memberID("Luca Verdi")

	// Assistente acceso con il fornitore finto; ogni domanda segue uno scenario programmato.
	fake := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	setModel := func(scenario string, mask bool) {
		admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "mask_personal_data": mask, "mcp_enabled": true,
			"deep": map[string]any{"provider_id": fake["id"], "model": "scenario:" + scenario}}, http.StatusOK)
	}
	// ask fa chiamare aggiorna con l'input dato e restituisce la proposta (se c'è) e ciò che il
	// modello ha letto come esito dello strumento.
	ask := func(u *user, scenario string, mask bool, question string, in map[string]any) (map[string]any, ai.ToolResult) {
		t.Helper()
		fakeai.Program(scenario, fakeai.Step{Calls: []fakeai.Call{{Name: "aggiorna", Input: in}}}, fakeai.Step{Text: "fatto"})
		setModel(scenario, mask)
		res := u.do("POST", "/api/v1/ai/ask", map[string]any{"question": question}, http.StatusOK)
		reqs := fakeai.Requests(scenario)
		if len(reqs) != 2 {
			t.Fatalf("%s: richieste al modello: %d", scenario, len(reqs))
		}
		last := reqs[1].Messages[len(reqs[1].Messages)-1]
		if len(last.ToolResults) != 1 {
			t.Fatalf("%s: esito dello strumento: %+v", scenario, last)
		}
		var prop map[string]any
		if ps := list(obj(list(res["messages"])[1])["proposals"]); len(ps) == 1 {
			prop = obj(ps[0])
		}
		return prop, last.ToolResults[0]
	}
	mustFail := func(scenario string, tr ai.ToolResult, prop map[string]any, want ...string) {
		t.Helper()
		if prop != nil || !tr.IsError {
			t.Fatalf("%s: atteso un errore, proposta %v, esito %+v", scenario, prop, tr)
		}
		for _, w := range want {
			if !strings.Contains(tr.Content, w) {
				t.Fatalf("%s: l'errore non dice «%s»: %s", scenario, w, tr.Content)
			}
		}
	}
	detail := func(key string) map[string]any { return ed.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK) }
	get := func(key string) map[string]any { return obj(detail(key)["element"]) }

	// --- Dati: un task del team Web e un progetto di Edoardo (che ne è il project manager).
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	task := ed.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "task", "title": "Rifare il login"}, http.StatusCreated)
	tk := task["key"].(string)
	prj := ed.do("POST", "/api/v1/projects", map[string]any{"title": "Portale clienti", "template": "agile", "no_phases": true}, http.StatusCreated)
	pk := prj["key"].(string)

	// --- Assegnatario e campo insieme: la proposta dice in chiaro che cosa cambia, e niente cambia prima della conferma.
	before := get(tk)
	prop, tr := ask(ed, "agg-assegna", false, "assegna "+tk+" a Mario Rossi con priorità alta",
		map[string]any{"elemento": tk, "assegnatario": "Mario Rossi", "campi": map[string]any{"Priorità": "alta"}})
	if prop == nil || tr.IsError {
		t.Fatalf("proposta assente: %+v", tr)
	}
	sum := prop["summary"].(string)
	if !strings.Contains(sum, "assegnatario: Mario Rossi (prima: nessuno)") || !strings.Contains(sum, "Priorità: Alta") || !strings.HasPrefix(sum, "Aggiornare "+tk) {
		t.Fatalf("riepilogo: %s", sum)
	}
	if el := get(tk); el["assignee_id"] != nil || el["version"] != before["version"] {
		t.Fatalf("modificato prima della conferma: %v", el)
	}
	if p := ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/confirm", nil, http.StatusOK); p["status"] != "applied" {
		t.Fatalf("conferma: %v", p)
	}
	after := get(tk)
	if after["assignee_id"] != mario || after["version"].(float64) <= before["version"].(float64) {
		t.Fatalf("assegnatario dopo la conferma: %v", after)
	}
	var prio string
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select o.name from elements e join options o on o.id::text = e.fields->>'priorita' where e.id = $1`, task["id"]).Scan(&prio)
	}); err != nil || prio != "Alta" {
		t.Fatalf("priorità dopo la conferma: %q %v", prio, err)
	}

	// --- Persona mascherata: il modello vede solo il segnaposto, la proposta il nome; «me» è chi scrive.
	prop, tr = ask(ed, "agg-maschera", true, "assegna "+tk+" a Luca Verdi", map[string]any{"elemento": tk, "assegnatario": "[persona-1]"})
	if prop == nil || !strings.Contains(prop["summary"].(string), "assegnatario: Luca Verdi (prima: Mario Rossi)") {
		t.Fatalf("persona mascherata: %v %+v", prop, tr)
	}
	sent, _ := json.Marshal(fakeai.Requests("agg-maschera"))
	if strings.Contains(string(sent), "Luca Verdi") || strings.Contains(string(sent), "Mario Rossi") || !strings.Contains(tr.Content, "[persona-") {
		t.Fatalf("nome uscito verso il fornitore: %s", sent)
	}
	ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/reject", nil, http.StatusOK)
	if prop, _ = ask(ed, "agg-me", false, "prendo io "+tk, map[string]any{"elemento": tk, "responsabile": "me"}); prop == nil ||
		!strings.Contains(prop["summary"].(string), "assegnatario: Edoardo Neri") {
		t.Fatalf("«me» come responsabile del task (= assegnatario): %v", prop)
	}

	// --- Errori leggibili dal modello, con le alternative.
	p, tr := ask(ed, "agg-ambigua", false, "assegna a Mario", map[string]any{"elemento": tk, "assegnatario": "Mario"})
	mustFail("persona ambigua", tr, p, "più persone", "Mario Bianchi", "Mario Rossi")
	p, tr = ask(ed, "agg-nessuno", false, "assegna a Giulio", map[string]any{"elemento": tk, "assegnatario": "Giulio Cesare"})
	mustFail("persona inesistente", tr, p, "nessuna persona attiva", "Giulio Cesare")
	p, tr = ask(ed, "agg-campo", false, "colore blu", map[string]any{"elemento": tk, "campi": map[string]any{"colore": "blu"}})
	mustFail("campo inesistente", tr, p, "non ha il campo «colore»", "priorita")
	p, tr = ask(ed, "agg-opzione", false, "priorità urgentissima", map[string]any{"elemento": tk, "campi": map[string]any{"priorita": "Urgentissima"}})
	mustFail("opzione inesistente", tr, p, "non è un'opzione", "Bloccante", "Bassa")
	p, tr = ask(ed, "agg-assegna-prj", false, "assegna "+pk, map[string]any{"elemento": pk, "assegnatario": "Luca Verdi"})
	mustFail("assegnatario del progetto", tr, p, "responsabile")
	p, tr = ask(ed, "agg-fase", false, "porta in Marte", map[string]any{"elemento": tk, "fase": "Marte"})
	mustFail("fase non raggiungibile", tr, p, "fasi possibili")
	p, tr = ask(vista, "agg-vista", false, "assegna "+tk, map[string]any{"elemento": tk, "assegnatario": "Luca Verdi"})
	mustFail("sola lettura", tr, p, "permesso")
	p, tr = ask(vista, "agg-vista-pm", false, "PM di "+pk, map[string]any{"elemento": pk, "responsabile": "Luca Verdi"})
	mustFail("responsabile in sola lettura", tr, p, "permesso")

	// --- Responsabile del progetto: il project manager cambia alla conferma (sostituito, non aggiunto).
	prop, tr = ask(ed, "agg-pm", false, "il PM di "+pk+" è Luca Verdi", map[string]any{"elemento": pk, "responsabile": "luca verdi"})
	if prop == nil || !strings.Contains(prop["summary"].(string), "responsabile: Luca Verdi (prima: Edoardo Neri)") {
		t.Fatalf("proposta del responsabile: %v %+v", prop, tr)
	}
	if p := ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/confirm", nil, http.StatusOK); p["status"] != "applied" {
		t.Fatalf("conferma del responsabile: %v", p)
	}
	var pms []string
	for _, sh := range list(detail(pk)["stakeholders"]) {
		if obj(sh)["role"] == "project_manager" {
			pms = append(pms, obj(sh)["member_id"].(string))
		}
	}
	if len(pms) != 1 || pms[0] != luca {
		t.Fatalf("project manager dopo la conferma: %v", pms)
	}

	// --- Voce di piano: l'assegnatario del work package passa dal servizio dei progetti.
	wp := ed.do("POST", "/api/v1/projects/"+pk+"/items", map[string]any{"kind": "work_package", "title": "Login unico", "delivery_mode": "agile"}, http.StatusCreated)
	wk := wp["key"].(string)
	prop, tr = ask(ed, "agg-wp", false, "assegna "+wk+" a Mario Rossi", map[string]any{"elemento": wk, "assegnatario": "mario.rossi@" + slug + ".test"})
	if prop == nil || !strings.Contains(prop["summary"].(string), "assegnatario: Mario Rossi") {
		t.Fatalf("proposta sul work package: %v %+v", prop, tr)
	}
	if p := ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/confirm", nil, http.StatusOK); p["status"] != "applied" || get(wk)["assignee_id"] != mario {
		t.Fatalf("assegnatario del work package: %v %v", p, get(wk))
	}

	// --- Versione: se l'elemento cambia dopo la proposta, la conferma non applica niente.
	prop, _ = ask(ed, "agg-versione", false, "priorità bassa", map[string]any{"elemento": tk, "campi": map[string]any{"priorita": "Bassa"}})
	el := get(tk)
	ed.do("PATCH", "/api/v1/elements/"+el["id"].(string), map[string]any{"version": el["version"], "title": "Rifare il login (v2)"}, http.StatusOK)
	if p := ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/confirm", nil, http.StatusOK); p["status"] != "failed" || !strings.Contains(p["error"].(string), "cambiato") {
		t.Fatalf("conferma dopo una modifica: %v", p)
	}

	// --- Fase: il passaggio è fra quelli che la persona può avviare, e avviene alla conferma.
	var target string
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cur, err := eng.Get(ctx, tx, engine.Actor{MemberID: memberID("Edoardo Neri"), Permissions: []string{"element.read", "element.write"}}, tk)
		if err != nil {
			return err
		}
		avail, err := eng.Available(ctx, tx, engine.Actor{MemberID: memberID("Edoardo Neri"), Permissions: []string{"element.read", "element.write"}}, cur)
		for _, a := range avail {
			if len(a.Blocked) == 0 && target == "" {
				target = a.To
			}
		}
		return err
	}); err != nil || target == "" {
		t.Fatalf("passaggi del task: %q %v", target, err)
	}
	prop, tr = ask(ed, "agg-passaggio", false, "porta avanti "+tk, map[string]any{"elemento": tk, "fase": strings.ToLower(target)})
	if prop == nil || !strings.Contains(prop["summary"].(string), "→ "+target) {
		t.Fatalf("proposta di passaggio: %v %+v", prop, tr)
	}
	ed.do("POST", "/api/v1/ai/proposals/"+prop["id"].(string)+"/confirm", nil, http.StatusOK)
	if st := get(tk)["state"]; st != target {
		t.Fatalf("fase dopo la conferma: %v (attesa %s)", st, target)
	}

	// --- Server MCP: lo stesso strumento, con proposta e conferma separata.
	tok := ed.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Claude Code"}, http.StatusCreated)["token"].(string)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + api.MCPPath,
		HTTPClient: &http.Client{Transport: bearerRT{tok}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatalf("connessione MCP: %v", err)
	}
	defer s.Close()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "aggiorna", Arguments: map[string]any{"elemento": tk, "assegnatario": "Mario Rossi", "campi": map[string]any{"priorita": "Bloccante"}}})
	if err != nil || r.IsError || !strings.Contains(toolText(r), "assegnatario: Mario Rossi") || !strings.Contains(toolText(r), "Priorità: Bloccante") {
		t.Fatalf("aggiorna via MCP: %v %s", err, toolText(r))
	}
	sc, _ := json.Marshal(r.StructuredContent)
	var mp struct {
		ID string `json:"proposta"`
	}
	_ = json.Unmarshal(sc, &mp)
	if r, err = s.CallTool(ctx, &mcp.CallToolParams{Name: "conferma", Arguments: map[string]any{"proposta": mp.ID}}); err != nil || r.IsError {
		t.Fatalf("conferma via MCP: %v %s", err, toolText(r))
	}
	if get(tk)["assignee_id"] != mario {
		t.Fatalf("assegnatario dopo la conferma MCP: %v", get(tk))
	}
	r, _ = s.CallTool(ctx, &mcp.CallToolParams{Name: "aggiorna", Arguments: map[string]any{"elemento": tk, "assegnatario": "Mario"}})
	if !r.IsError || !strings.Contains(toolText(r), "più persone") {
		t.Fatalf("persona ambigua via MCP: %s", toolText(r))
	}
}
