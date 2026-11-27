package itest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOpenAI risponde come la Responses API: al primo passo chiama «cerca», al secondo
// risponde con il risultato dello strumento (così la risposta contiene la chiave trovata).
func fakeOpenAI(t *testing.T) (*httptest.Server, func() []map[string]any) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-finta-ondata6" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key","code":"invalid_api_key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-6-luna"},{"id":"gpt-6.2-astra"}]}`)
			return
		case "/v1/responses/input_tokens":
			_, _ = io.WriteString(w, `{"input_tokens":50}`)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		var toolOut string
		for _, it := range b["input"].([]any) {
			if m, _ := it.(map[string]any); m["type"] == "function_call_output" {
				toolOut, _ = m["output"].(string)
			}
		}
		out := `[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"cifrato"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"cerca","arguments":"{\"testo\":\"magazzino sigma\"}"}]`
		if toolOut != "" {
			txt, _ := json.Marshal("Trovato: " + toolOut)
			out = `[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":` + string(txt) + `}]}]`
		}
		resp := `{"id":"resp","model":"gpt-6.2-astra","status":"completed","output":` + out +
			`,"usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":1000},"output_tokens":100}}`
		if b["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			resp = strings.Join(strings.Fields(resp), " ")
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+resp+"}\n\n")
			return
		}
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any { mu.Lock(); defer mu.Unlock(); return append([]map[string]any(nil), bodies...) }
}

// fakeOllama risponde come l'API di Ollama, con un modello che non sa usare strumenti.
func fakeOllama(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"grande:70b","size":40000000000,"capabilities":["completion","tools"]},
				{"name":"piccolo:3b","size":2000000000,"capabilities":["completion","tools"]},
				{"name":"vecchio:7b","size":4000000000,"capabilities":["completion"]}]}`)
		case "/api/chat":
			var b struct {
				Stream   bool `json:"stream"`
				Messages []struct {
					Role, Content string
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			last := b.Messages[len(b.Messages)-1]
			if last.Role == "tool" {
				txt, _ := json.Marshal("Da Ollama: " + last.Content)
				fmt.Fprintf(w, `{"model":"grande:70b","message":{"role":"assistant","content":%s},"done":true,"done_reason":"stop","prompt_eval_count":300,"eval_count":20}`+"\n", txt)
				return
			}
			_, _ = io.WriteString(w, `{"model":"grande:70b","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"cerca","arguments":{"testo":"magazzino sigma"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":200,"eval_count":10}`+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testServiceAccount(t *testing.T) string {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	b, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "regia-prova-ue", "private_key_id": "k1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "regia@regia-prova-ue.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return string(b)
}

// Ondata 6 (replica/ai.md §3, §4): fornitori nativi dal pannello alla chiamata. OpenAI e Ollama
// contro server finti; Bedrock e Vertex solo configurazione (nessuna chiamata a servizi veri):
// credenziali cifrate e mai restituite, regione e progetto in chiaro, prova di connessione che
// segnala i modelli senza strumenti, funzione spostata su un altro fornitore, registro e costo.
func TestAIOndata6(t *testing.T) {
	e := newOndata2(t, "ai6")
	oa, openaiBodies := fakeOpenAI(t)
	ol := fakeOllama(t)
	admin := e.admin

	view := admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK)
	kinds := map[string]map[string]any{}
	for _, k := range e.list(view["kinds"]) {
		kinds[e.obj(k)["key"].(string)] = e.obj(k)
	}
	for _, k := range []string{"anthropic", "anthropic_bedrock", "anthropic_vertex", "openai", "gemini", "ollama", "openai_compat"} {
		if kinds[k] == nil {
			t.Fatalf("tipo %s mancante: %v", k, kinds)
		}
	}
	if kinds["anthropic_bedrock"]["cloud"] != "aws" || kinds["anthropic_vertex"]["cloud"] != "gcp" || kinds["openai"]["optional_url"] != true {
		t.Fatalf("descrizione dei tipi: %v", kinds)
	}

	// --- OpenAI con l'indirizzo del server finto; la chiave non torna.
	op := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "openai", "name": "OpenAI", "base_url": oa.URL, "api_key": "sk-finta-ondata6"}, http.StatusCreated)
	opID := op["id"].(string)
	if op["base_url"] != oa.URL+"/v1" || op["has_key"] != true || strings.Contains(fmt.Sprint(op), "sk-finta") {
		t.Fatalf("fornitore OpenAI: %v", op)
	}
	test := admin.do("POST", "/api/v1/ai/providers/"+opID+"/test", nil, http.StatusOK)
	if test["ok"] != true || len(e.list(test["models"])) != 2 || len(e.list(test["no_tools"])) != 0 {
		t.Fatalf("prova OpenAI: %v", test)
	}
	e.ed.do("POST", "/api/v1/ai/providers/"+opID+"/test", nil, http.StatusForbidden)

	// --- Ollama: la prova segna il modello senza strumenti e propone i livelli dalle dimensioni.
	ollama := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "ollama", "name": "Ollama di casa", "base_url": ol.URL}, http.StatusCreated)
	olID := ollama["id"].(string)
	if ollama["base_url"] != ol.URL || ollama["local"] != true {
		t.Fatalf("Ollama (senza /v1, sulla rete interna): %v", ollama)
	}
	test = admin.do("POST", "/api/v1/ai/providers/"+olID+"/test", map[string]any{}, http.StatusOK)
	nt := e.list(test["no_tools"])
	if len(nt) != 1 || nt[0] != "vecchio:7b" {
		t.Fatalf("modelli senza strumenti: %v", test)
	}
	tiers := map[string]string{}
	for _, m := range e.list(test["models"]) {
		if tier, _ := e.obj(m)["tier"].(string); tier != "" {
			tiers[tier] = e.obj(m)["id"].(string)
		}
	}
	if tiers["approfondito"] != "grande:70b" || tiers["rapido"] != "piccolo:3b" {
		t.Fatalf("livelli proposti: %v", tiers)
	}
	for _, p := range e.list(admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK)["providers"]) {
		if e.obj(p)["id"] == olID && fmt.Sprint(e.obj(p)["no_tools_models"]) != "[vecchio:7b]" {
			t.Fatalf("modello senza strumenti non salvato: %v", p)
		}
	}
	var audited int
	if err := e.d.Owner.QueryRow(e.ctx, `select count(*) from audit_log where tenant_id = $1 and action = 'ai.provider.test' and entity_id = $2`, e.tid, olID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit della prova: %d %v", audited, err)
	}

	// --- Bedrock e Vertex: regione e progetto validati, credenziali cifrate e mai restituite.
	admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "anthropic_bedrock", "name": "Bedrock", "region": "Francoforte",
		"aws_access_key_id": "AKIAPROVAREGIA0001", "aws_secret_access_key": "segreto-aws-di-prova"}, http.StatusUnprocessableEntity)
	admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "anthropic_bedrock", "name": "Bedrock", "region": "eu-central-1"}, http.StatusUnprocessableEntity)
	bed := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "anthropic_bedrock", "name": "Bedrock UE", "region": "eu-central-1",
		"base_url": "http://non-si-usa.invalid", "aws_access_key_id": "AKIAPROVAREGIA0001", "aws_secret_access_key": "segreto-aws-di-prova"}, http.StatusCreated)
	if bed["region"] != "eu-central-1" || bed["has_key"] != true || bed["base_url"] != "" || bed["local"] != false || strings.Contains(fmt.Sprint(bed), "segreto-aws") {
		t.Fatalf("Bedrock: %v", bed)
	}
	sa := testServiceAccount(t)
	vx := admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "anthropic_vertex", "name": "Vertex UE", "region": "europe-west8", "service_account_json": sa}, http.StatusCreated)
	if vx["project_id"] != "regia-prova-ue" || vx["region"] != "europe-west8" || strings.Contains(fmt.Sprint(vx), "PRIVATE KEY") {
		t.Fatalf("Vertex: %v", vx)
	}
	// In modifica, credenziali vuote = invariate; la regione cambia.
	vx = admin.do("PATCH", "/api/v1/ai/providers/"+vx["id"].(string), map[string]any{"name": "Vertex UE", "region": "eu"}, http.StatusOK)
	if vx["region"] != "eu" || vx["has_key"] != true || vx["project_id"] != "regia-prova-ue" {
		t.Fatalf("Vertex modificato: %v", vx)
	}
	all := fmt.Sprint(admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK))
	var auditTxt string
	if err := e.d.Owner.QueryRow(e.ctx, `select coalesce(string_agg(payload::text, ' '), '') from audit_log where tenant_id = $1 and action like 'ai.provider.%'`, e.tid).Scan(&auditTxt); err != nil {
		t.Fatal(err)
	}
	var secrets string
	if err := e.d.Owner.QueryRow(e.ctx, `select string_agg(encode(secret_enc, 'escape'), ' ') from ai_providers where tenant_id = $1`, e.tid).Scan(&secrets); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"segreto-aws", "PRIVATE KEY", "sk-finta"} {
		if strings.Contains(all, s) || strings.Contains(auditTxt, s) || strings.Contains(secrets, s) {
			t.Fatalf("segreto %q in chiaro (API, audit o database)", s)
		}
	}

	// --- Livelli su OpenAI: l'assistente chiama lo strumento e risponde; registro con la cache.
	dem := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Magazzino sigma da rifare", "description": "Testo."}, http.StatusCreated)
	demKey := dem["key"].(string)
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true,
		"deep": map[string]any{"provider_id": opID, "model": "gpt-6.2-astra"}, "fast": map[string]any{"provider_id": olID, "model": "piccolo:3b"}}, http.StatusOK)
	res := e.ed.do("POST", "/api/v1/ai/ask", map[string]any{"question": "dov'è il magazzino sigma?"}, http.StatusOK)
	msgs := e.list(res["messages"])
	if txt := e.obj(msgs[len(msgs)-1])["text"].(string); !strings.Contains(txt, demKey) {
		t.Fatalf("risposta via OpenAI: %v", res)
	}
	bodies := openaiBodies()
	if len(bodies) != 2 || bodies[0]["store"] != false || e.obj(bodies[0]["reasoning"])["effort"] != "high" {
		t.Fatalf("richieste a OpenAI: %v", bodies)
	}
	replayed := false
	for _, it := range e.list(bodies[1]["input"]) {
		replayed = replayed || e.obj(it)["encrypted_content"] == "cifrato"
	}
	if !replayed {
		t.Fatalf("ragionamento cifrato non rimandato: %v", bodies[1]["input"])
	}
	if n := e.count(t, `select count(*) from ai_calls where provider = 'openai' and model = 'gpt-6.2-astra' and outcome = 'ok'
		and cache_read_tokens = 1000 and cost_usd > 0 and tier = 'approfondito'`); n != 2 {
		t.Fatalf("registro OpenAI: %d", n)
	}

	// --- La funzione «assistente» spostata su Ollama (modello preciso di un altro fornitore).
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true,
		"deep": map[string]any{"provider_id": opID, "model": "gpt-6.2-astra"}, "fast": map[string]any{"provider_id": olID, "model": "piccolo:3b"},
		"routes": map[string]any{"assistente": map[string]any{"provider_id": olID, "model": "grande:70b"}}}, http.StatusOK)
	res = e.ed.do("POST", "/api/v1/ai/ask", map[string]any{"question": "dov'è il magazzino sigma?"}, http.StatusOK)
	msgs = e.list(res["messages"])
	if txt := e.obj(msgs[len(msgs)-1])["text"].(string); !strings.HasPrefix(txt, "Da Ollama:") || !strings.Contains(txt, demKey) {
		t.Fatalf("risposta via Ollama: %v", res)
	}
	if n := e.count(t, `select count(*) from ai_calls where provider = 'ollama' and model = 'grande:70b' and outcome = 'ok' and cost_usd = 0 and input_tokens > 0`); n != 2 {
		t.Fatalf("registro Ollama: %d", n)
	}
	// Un modello senza strumenti scelto per l'assistente: lo stato lo dice.
	admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true,
		"deep": map[string]any{"provider_id": olID, "model": "vecchio:7b"}}, http.StatusOK)
	if st := e.ed.do("GET", "/api/v1/ai/status", nil, http.StatusOK); st["tools"] != false {
		t.Fatalf("stato con un modello senza strumenti: %v", st)
	}
}
