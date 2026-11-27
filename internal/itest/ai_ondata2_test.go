package itest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
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
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// bearerRT aggiunge il token API a ogni richiesta del client MCP.
type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// ondata2Env è l'ambiente comune dei test dell'ondata 2: un tenant con un amministratore, un
// editor e un richiedente del portale, il server API e l'accesso al database.
type ondata2Env struct {
	d              *db.DB
	srv            *httptest.Server
	slug, tid      string
	admin, ed, cur *user
	ctx            context.Context
	obj            func(any) map[string]any
	list           func(any) []any
	// api è il server, per i lavori in background (wiki personale, ondata 4).
	api *api.Server
}

func newOndata2(t *testing.T, prefix string) *ondata2Env {
	d := needDB(t)
	ctx := context.Background()
	stamp := strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	slug := prefix + "-" + stamp
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "AI " + slug, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision}}); err != nil {
		t.Fatal(err)
	}
	for n, in := range map[string]tenant.MemberInput{
		"ed":      {Roles: []string{"editor"}},
		"curioso": {MemberType: "requester", Roles: []string{"requester"}},
	} {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, n+"@"+slug+".test", n, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Work: wk,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)
	e := &ondata2Env{d: d, srv: srv, slug: slug, ctx: ctx, api: apiSrv,
		obj:  func(v any) map[string]any { m, _ := v.(map[string]any); return m },
		list: func(v any) []any { l, _ := v.([]any); return l }}
	if err := d.Owner.QueryRow(ctx, `select id::text from tenants where slug = $1`, slug).Scan(&e.tid); err != nil {
		t.Fatal(err)
	}
	e.admin = login(t, srv.URL, "admin@"+slug+".test", testPassword)
	e.ed = login(t, srv.URL, "ed@"+slug+".test", testPassword)
	e.cur = login(t, srv.URL, "curioso@"+slug+".test", testPassword)
	return e
}

func (e *ondata2Env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, sql, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *ondata2Env) mcpSession(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(e.ctx, &mcp.StreamableClientTransport{Endpoint: e.srv.URL + api.MCPPath,
		HTTPClient: &http.Client{Transport: bearerRT{token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatalf("connessione MCP: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func toolText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Server MCP (replica/ai.md §2 punto 4, ondata 2): token del membro, interruttore suo spento di
// default, strumenti con i permessi della persona, scritture come proposte confermate con uno
// strumento separato, registro con funzione «mcp» e fornitore «client», token di sola lettura.
func TestAIMCP(t *testing.T) {
	e := newOndata2(t, "mcp")
	dem := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Migrazione del magazzino omega", "description": "Testo della richiesta."}, http.StatusCreated)
	demKey, demID := dem["key"].(string), dem["id"].(string)
	edTok := e.ed.do("POST", "/api/v1/me/tokens", map[string]any{"name": "Claude Code"}, http.StatusCreated)["token"].(string)
	roTok := e.ed.do("POST", "/api/v1/me/tokens", map[string]any{"name": "sola lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	curTok := e.cur.do("POST", "/api/v1/me/tokens", map[string]any{"name": "portale"}, http.StatusCreated)["token"].(string)

	post := func(token string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+api.MCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	// --- Senza token no (nemmeno con la sessione del browser); token sbagliato no; spento di default.
	if c := post(""); c != http.StatusUnauthorized {
		t.Fatalf("senza token: %d", c)
	}
	if c := post("myt_sbagliato"); c != http.StatusUnauthorized {
		t.Fatalf("token sbagliato: %d", c)
	}
	e.ed.do("POST", api.MCPPath, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, http.StatusUnauthorized)
	if c := post(edTok); c != http.StatusForbidden {
		t.Fatalf("MCP acceso di default: %d", c)
	}
	if s := e.obj(e.admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK)["settings"]); s["mcp_enabled"] != false {
		t.Fatalf("interruttore MCP: %v", s)
	}
	// Si accende da solo, senza un fornitore del modello: il modello è quello del client.
	e.ed.do("PUT", "/api/v1/ai/settings", map[string]any{"mcp_enabled": true}, http.StatusForbidden)
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"mcp_enabled": true}, http.StatusOK)
	var audited int
	if err := e.d.Owner.QueryRow(e.ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'ai.settings.update' and payload->>'mcp_enabled' = 'true'`, e.tid).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("accensione dell'MCP senza audit: %d %v", audited, err)
	}

	// --- Strumenti: quelli di internal/ai/tools, più conferma, rifiuta e proposte.
	s := e.mcpSession(t, edTok)
	init := s.InitializeResult()
	if init == nil || init.ServerInfo.Name != "regia" || !strings.Contains(init.Instructions, "<dati>") {
		t.Fatalf("inizializzazione: %+v", init)
	}
	tl, err := s.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tl.Tools {
		names = append(names, tool.Name)
		if tool.Name == "commenta" && (!strings.Contains(tool.Description, "conferma") || tool.Annotations.ReadOnlyHint) {
			t.Fatalf("descrizione della scrittura: %+v", tool)
		}
		if tool.Name == "conferma" && (tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || !strings.Contains(tool.Description, "sì")) {
			t.Fatalf("strumento di conferma: %+v", tool)
		}
		if tool.Name == "cerca" && !tool.Annotations.ReadOnlyHint {
			t.Fatalf("lettura non segnata: %+v", tool)
		}
	}
	for _, want := range []string{"cerca", "query", "leggi", "crea", "aggiorna", "collega", "commenta", "registra_ore", "proposte", "conferma", "rifiuta"} {
		if !slices.Contains(names, want) {
			t.Fatalf("manca lo strumento %s: %v", want, names)
		}
	}

	// --- Lettura: risultato come dato, con il link all'elemento; nel registro come «mcp»/«client».
	r, err := s.CallTool(e.ctx, &mcp.CallToolParams{Name: "cerca", Arguments: map[string]any{"testo": "magazzino omega"}})
	if err != nil || r.IsError {
		t.Fatalf("cerca: %v %+v", err, r)
	}
	if txt := toolText(r); !strings.Contains(txt, demKey) || !strings.Contains(txt, "<dati") || !strings.Contains(txt, "/e/"+demKey) {
		t.Fatalf("risultato di cerca: %s", txt)
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'mcp' and provider = 'client' and model = 'strumento:cerca'
		and outcome = 'ok' and $1::uuid = any(element_ids) and length(prompt_sha256) = 32 and input_tokens = 0 and cost_usd = 0`, demID); n != 1 {
		t.Fatalf("chiamata MCP nel registro: %d", n)
	}
	// Input sbagliato: errore leggibile dal modello, registrato come errore.
	r, err = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "leggi", Arguments: map[string]any{"elemento": "NONC-999"}})
	if err != nil || !r.IsError || !strings.Contains(toolText(r), "non visibile") {
		t.Fatalf("leggi inesistente: %v %+v", err, r)
	}
	if e.count(t, `select count(*) from ai_calls where feature = 'mcp' and outcome = 'error' and error_code = 'input'`) != 1 {
		t.Fatal("errore dello strumento non registrato")
	}

	// --- Scrittura: una proposta; il commento nasce solo con «conferma».
	commentCount := func(text string) int {
		_, _, body := e.admin.get("/api/v1/elements/" + demID + "/comments")
		return strings.Count(string(body), text)
	}
	r, err = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "commenta", Arguments: map[string]any{"elemento": demKey, "testo": "parere del client MCP"}})
	if err != nil || r.IsError {
		t.Fatalf("commenta: %v %+v", err, r)
	}
	sc, _ := json.Marshal(r.StructuredContent)
	var prop struct {
		ID    string `json:"proposta"`
		Stato string `json:"stato"`
	}
	_ = json.Unmarshal(sc, &prop)
	if prop.ID == "" || prop.Stato != "pending" || !strings.Contains(toolText(r), "NON ancora applicata") {
		t.Fatalf("proposta: %s %s", sc, toolText(r))
	}
	if commentCount("parere del client MCP") != 0 {
		t.Fatal("commento scritto prima della conferma")
	}
	if r, _ = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "proposte"}); !strings.Contains(toolText(r), prop.ID) {
		t.Fatalf("proposte in sospeso: %s", toolText(r))
	}
	// Un'altra persona non conferma la proposta (non è sua).
	e.admin.do("POST", "/api/v1/ai/proposals/"+prop.ID+"/confirm", nil, http.StatusNotFound)
	r, err = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "conferma", Arguments: map[string]any{"proposta": prop.ID}})
	if err != nil || r.IsError || !strings.Contains(toolText(r), "applicata") {
		t.Fatalf("conferma: %v %s", err, toolText(r))
	}
	if commentCount("parere del client MCP") != 1 {
		t.Fatal("commento non scritto alla conferma")
	}
	r, _ = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "conferma", Arguments: map[string]any{"proposta": prop.ID}})
	if !r.IsError || !strings.Contains(toolText(r), "già") {
		t.Fatalf("seconda conferma: %s", toolText(r))
	}
	// Rifiuto: non cambia niente.
	r, _ = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "commenta", Arguments: map[string]any{"elemento": demKey, "testo": "da non pubblicare"}})
	sc, _ = json.Marshal(r.StructuredContent)
	_ = json.Unmarshal(sc, &prop)
	if r, _ = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "rifiuta", Arguments: map[string]any{"proposta": prop.ID}}); r.IsError || commentCount("da non pubblicare") != 0 {
		t.Fatalf("rifiuto: %s", toolText(r))
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'mcp' and provider = 'client' and outcome in ('accepted_by_user', 'rejected_by_user')`); n != 2 {
		t.Fatalf("decisioni MCP nel registro: %d", n)
	}

	// --- Token di sola lettura: solo le letture, e la scrittura non si chiama nemmeno a mano.
	ro := e.mcpSession(t, roTok)
	tl, err = ro.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tl.Tools {
		if slices.Contains([]string{"crea", "aggiorna", "collega", "commenta", "registra_ore", "conferma", "rifiuta"}, tool.Name) {
			t.Fatalf("token di sola lettura con %s", tool.Name)
		}
	}
	if r, err := ro.CallTool(e.ctx, &mcp.CallToolParams{Name: "commenta", Arguments: map[string]any{"elemento": demKey, "testo": "x"}}); err == nil && !r.IsError {
		t.Fatal("scrittura con un token di sola lettura")
	}
	if r, err := ro.CallTool(e.ctx, &mcp.CallToolParams{Name: "leggi", Arguments: map[string]any{"elemento": demKey}}); err != nil || r.IsError {
		t.Fatalf("lettura con il token di sola lettura: %v %+v", err, r)
	}

	// --- Stessi permessi della persona: il richiedente del portale non vede la richiesta.
	cs := e.mcpSession(t, curTok)
	if r, err := cs.CallTool(e.ctx, &mcp.CallToolParams{Name: "leggi", Arguments: map[string]any{"elemento": demKey}}); err != nil || !r.IsError || strings.Contains(toolText(r), "omega") {
		t.Fatalf("il richiedente legge con l'MCP: %v %s", err, toolText(r))
	}

	// --- Spento di nuovo: il client riceve 403.
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"mcp_enabled": false}, http.StatusOK)
	if c := post(edTok); c != http.StatusForbidden {
		t.Fatalf("MCP spento: %d", c)
	}
}

// Domande sul grafo, streaming dell'assistente, avviso del budget e cache nel registro
// (replica/ai.md, ondata 2), tutto con il fornitore finto.
func TestAIOndata2(t *testing.T) {
	e := newOndata2(t, "aig")
	dem := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuovo portale fornitori sigma", "description": "Testo."}, http.StatusCreated)
	demKey := dem["key"].(string)
	fake := e.admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	fakeID := fake["id"].(string)
	settings := func(extra map[string]any) {
		body := map[string]any{"enabled": true, "deep": map[string]any{"provider_id": fakeID, "model": fakeai.ModelDeep}}
		for k, v := range extra {
			body[k] = v
		}
		e.admin.do("PUT", "/api/v1/ai/settings", body, http.StatusOK)
	}
	// Spenta: la domanda sul grafo non parte.
	e.ed.do("POST", "/api/v1/ai/graph", map[string]any{"question": "Quali richieste aperte?"}, http.StatusConflict)
	settings(nil)

	// --- Traduzione valida al primo colpo: la query trova la richiesta con la ricerca normale.
	g := e.ed.do("POST", "/api/v1/ai/graph", map[string]any{"question": "Quali richieste aperte ci sono?"}, http.StatusOK)
	if g["query"] != "tipo: business_request fase: aperta" || g["valid"] != true || g["corrected"] != false || g["explanation"] == "" {
		t.Fatalf("traduzione: %v", g)
	}
	found := e.ed.do("GET", "/api/v1/elements?q="+url.QueryEscape(g["query"].(string)), nil, http.StatusOK)
	if !strings.Contains(fmtJSON(found), demKey) {
		t.Fatalf("la query non trova la richiesta: %v", found)
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'domande_grafo' and outcome = 'ok' and tier = 'approfondito'`); n != 1 {
		t.Fatalf("chiamate della domanda sul grafo: %d", n)
	}
	// Il prompt porta il catalogo del tenant come dato.
	reqs := fakeai.Requests(fakeai.ModelDeep)
	if last := reqs[len(reqs)-1]; !strings.Contains(last.System, "business_request") || !strings.Contains(last.System, "<dati origine=\"catalogo\">") || len(last.Tools) != 0 {
		t.Fatalf("istruzioni della domanda sul grafo: %.300s", last.System)
	}

	// --- Query non valida: una correzione sola, e si vede.
	g = e.ed.do("POST", "/api/v1/ai/graph", map[string]any{"question": "Quali applicazioni critiche abbiamo?"}, http.StatusOK)
	if g["valid"] != true || g["corrected"] != true || g["query"] != "tipo: application" || g["first_query"] == "" || g["first_error"] == nil {
		t.Fatalf("correzione: %v", g)
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'domande_grafo'`); n != 3 {
		t.Fatalf("chiamate dopo la correzione: %d", n)
	}

	// --- Ancora non valida dopo la correzione: niente terzo tentativo, la query resta da correggere a mano.
	fakeai.Program("grafo",
		fakeai.Step{Text: "QUERY: campo_finto: uno\nSPIEGAZIONE: prima prova"},
		fakeai.Step{Text: "QUERY: altro_campo_finto: due\nSPIEGAZIONE: seconda prova"},
		fakeai.Step{Text: "QUERY: tipo: business_request\nSPIEGAZIONE: terza prova, da non usare"})
	settings(map[string]any{"routes": map[string]any{"domande_grafo": map[string]any{"provider_id": fakeID, "model": "scenario:grafo"}}})
	g = e.ed.do("POST", "/api/v1/ai/graph", map[string]any{"question": "Una domanda impossibile"}, http.StatusOK)
	if g["valid"] != false || g["corrected"] != true || g["query"] != "altro_campo_finto: due" || g["error"] == nil {
		t.Fatalf("doppio errore: %v", g)
	}
	if n := len(fakeai.Requests("grafo")); n != 2 {
		t.Fatalf("tentativi: %d", n)
	}
	// Il secondo tentativo ha visto l'errore del motore.
	if fix := fakeai.Requests("grafo")[1].Messages; !strings.Contains(fix[len(fix)-1].Text, "campo_finto") {
		t.Fatalf("correzione senza l'errore: %v", fix)
	}
	settings(nil)

	// --- Streaming: eventi mentre arriva la risposta, poi lo stesso risultato della risposta JSON.
	body, _ := json.Marshal(map[string]any{"question": "cerca portale fornitori sigma"})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/ai/ask", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := e.ed.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	stream := string(raw)
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(stream, "event: tool") || strings.Count(stream, "event: text") < 2 ||
		!strings.Contains(stream, "event: done") || !strings.Contains(stream, demKey) {
		t.Fatalf("streaming: %s", stream)
	}
	if strings.Index(stream, "event: text") > strings.Index(stream, "event: done") {
		t.Fatalf("il testo arriva dopo la fine: %s", stream)
	}
	// Errore in streaming: l'evento «error» porta il codice dell'API.
	fakeai.Program("rotto", fakeai.Step{Err: &ai.ProviderError{Code: "rate_limit", Msg: "troppe richieste"}})
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": map[string]any{"provider_id": fakeID, "model": "scenario:rotto"}}, http.StatusOK)
	req, _ = http.NewRequest("POST", e.srv.URL+"/api/v1/ai/ask", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err = e.ed.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if s := string(raw); !strings.Contains(s, "event: error") || !strings.Contains(s, "ai.provider_error") {
		t.Fatalf("errore in streaming: %s", s)
	}
	settings(nil)

	// --- Avviso all'80%: una notifica a chi amministra, una sola volta nel mese; non agli altri.
	settings(map[string]any{"monthly_budget_usd": 0.01})
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctx, `insert into ai_calls (tenant_id, feature, provider, model, cost_usd, outcome) values (app_tenant(), 'assistente', 'fake', 'x', 0.0085, 'ok')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	notes := func(u *user) int {
		n := 0
		for _, it := range e.list(u.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"]) {
			if m := e.obj(it); m["kind"] == "ai" && e.obj(m["payload"])["event"] == "budget_warning" {
				n++
			}
		}
		return n
	}
	e.ed.do("POST", "/api/v1/ai/ask", map[string]any{"question": "cerca sigma"}, http.StatusOK)
	e.ed.do("POST", "/api/v1/ai/ask", map[string]any{"question": "cerca sigma"}, http.StatusOK)
	if notes(e.admin) != 1 || notes(e.ed) != 0 {
		t.Fatalf("notifiche del budget: amministratore %d, editor %d", notes(e.admin), notes(e.ed))
	}

	// --- Cache del prompt: le colonne nuove del registro ci sono e partono da zero.
	if n := e.count(t, `select count(*) from ai_calls where cache_read_tokens = 0 and cache_write_tokens = 0`); n == 0 {
		t.Fatal("colonne della cache assenti")
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
