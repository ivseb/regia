package anthropic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"regia/internal/ai"
)

const messageJSON = `{"id":"msg_1","type":"message","role":"assistant","model":"%s","stop_reason":"end_turn",
	"content":[{"type":"text","text":"Ciao."}],"usage":{"input_tokens":20,"output_tokens":3,"cache_read_input_tokens":10}}`

// Bedrock con le chiavi d'accesso: richiesta firmata SigV4 per bedrock-mantle nella regione,
// modello con il prefisso anthropic., niente variabili d'ambiente del server; con la chiave
// API di Bedrock va come Bearer; il conteggio dei token fa da prova di connessione.
func TestBedrock(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIADELSERVERNONUSARE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "segreto-del-server")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "token-del-server")
	t.Setenv("ANTHROPIC_BEDROCK_MANTLE_BASE_URL", "http://non-usare.invalid")
	var mu sync.Mutex
	var auths, paths []string
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		paths = append(paths, r.URL.Path)
		m, _ := b["model"].(string)
		models = append(models, m)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			_, _ = io.WriteString(w, `{"input_tokens":7}`)
			return
		}
		_, _ = io.WriteString(w, strings.ReplaceAll(messageJSON, "%s", m))
	}))
	defer srv.Close()
	creds, _ := json.Marshal(ai.AWSCredentials{AccessKeyID: "AKIAPROVAREGIA0001", SecretAccessKey: "segreto-di-prova"})
	p, err := NewBedrock(ai.ProviderConfig{APIKey: string(creds), Region: "eu-central-1", BaseURL: srv.URL + "/anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, err := p.Chat(ctx, ai.Request{Model: BedrockModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Message.Text != "Ciao." || r.CacheReadTokens != 10 {
		t.Fatalf("risposta: %+v", r)
	}
	if err := p.(ai.Tester).Test(ctx, BedrockModelDeep); err != nil {
		t.Fatal(err)
	}
	list, _ := p.Models(ctx)
	if len(list) != 2 || list[0].ID != BedrockModelFast {
		t.Fatalf("modelli proposti: %+v", list)
	}
	if paths[0] != "/anthropic/v1/messages" || paths[1] != "/anthropic/v1/messages/count_tokens" || models[0] != "anthropic.claude-haiku-5-5" {
		t.Fatalf("richieste: %v %v", paths, models)
	}
	for _, a := range auths[:2] {
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIAPROVAREGIA0001/") || !strings.Contains(a, "/eu-central-1/bedrock-mantle/") {
			t.Fatalf("firma SigV4: %q", a)
		}
	}
	key, _ := json.Marshal(ai.AWSCredentials{APIKey: "chiave-bedrock-di-prova"})
	pk, err := NewBedrock(ai.ProviderConfig{APIKey: string(key), Region: "eu-south-1", BaseURL: srv.URL + "/anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pk.Chat(ctx, ai.Request{Model: BedrockModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}}); err != nil {
		t.Fatal(err)
	}
	if a := auths[len(auths)-1]; a != "Bearer chiave-bedrock-di-prova" {
		t.Fatalf("chiave API di Bedrock: %q", a)
	}
	if _, err := NewBedrock(ai.ProviderConfig{APIKey: "{}", Region: "eu-central-1"}); ai.ErrorCode(err) != "auth" {
		t.Fatalf("senza credenziali (non si ricade sull'ambiente): %v", err)
	}
	if !supportsEffort(BedrockModelDeep) {
		t.Fatal("effort sui modelli di Bedrock")
	}
	if BedrockBaseURL("eu-central-1") != "https://bedrock-mantle.eu-central-1.api.aws/anthropic" {
		t.Fatal(BedrockBaseURL("eu-central-1"))
	}
}

// serviceAccount crea al volo la chiave JSON di un service account finto (chiave RSA generata
// nel test, indirizzo dei token sul server locale).
func serviceAccount(t *testing.T, tokenURI string) string {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	b, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "progetto-prova", "private_key_id": "k1",
		"private_key": pemKey, "client_email": "regia@progetto-prova.iam.gserviceaccount.com", "token_uri": tokenURI})
	return string(b)
}

// Vertex: token dal service account, percorso rawPredict/streamRawPredict con progetto e
// regione, modello tolto dal corpo, versione dell'API nel corpo, conteggio dei token.
func TestVertex(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/non/usare.json")
	var mu sync.Mutex
	tokens := 0
	type seen struct {
		path, auth string
		body       map[string]any
	}
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.PostForm.Get("assertion") == "" {
				t.Errorf("richiesta del token: %v", r.PostForm)
			}
			mu.Lock()
			tokens++
			mu.Unlock()
			_, _ = io.WriteString(w, `{"access_token":"token-di-prova","token_type":"Bearer","expires_in":3600}`)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		reqs = append(reqs, seen{r.URL.Path, r.Header.Get("Authorization"), b})
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "count-tokens:rawPredict"):
			_, _ = io.WriteString(w, `{"input_tokens":9}`)
		case strings.HasSuffix(r.URL.Path, ":streamRawPredict"):
			w.Header().Set("Content-Type", "text/event-stream")
			ev := func(name, data string) { _, _ = io.WriteString(w, "event: "+name+"\ndata: "+data+"\n\n") }
			ev("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":12,"output_tokens":1}}}`)
			ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Ciao "}}`)
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"UE"}}`)
			ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
			ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`)
			ev("message_stop", `{"type":"message_stop"}`)
		default:
			_, _ = io.WriteString(w, strings.ReplaceAll(messageJSON, "%s", ModelFast))
		}
	}))
	defer srv.Close()
	sa := serviceAccount(t, srv.URL+"/token")
	p, err := NewVertex(ai.ProviderConfig{APIKey: sa, Region: "europe-west8", ProjectID: "progetto-prova", BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.Chat(ctx, ai.Request{Model: ModelFast, System: "regole", Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	r, err := p.(ai.Streamer).ChatStream(ctx, ai.Request{Model: ModelDeep, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}, Deep: true}, func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "") != "Ciao UE" || r.Message.Text != "Ciao UE" || r.OutputTokens != 4 {
		t.Fatalf("streaming: %v %+v", got, r)
	}
	if err := p.(ai.Tester).Test(ctx, ModelFast); err != nil {
		t.Fatal(err)
	}
	const prefix = "/v1/projects/progetto-prova/locations/europe-west8/publishers/anthropic/models/"
	want := []string{prefix + "claude-haiku-5-5:rawPredict", prefix + "claude-opus-5-5:streamRawPredict", prefix + "count-tokens:rawPredict"}
	for i, w := range want {
		if reqs[i].path != w {
			t.Fatalf("percorso %d: %s", i, reqs[i].path)
		}
		if reqs[i].auth != "Bearer token-di-prova" || reqs[i].body["anthropic_version"] == nil {
			t.Fatalf("token o versione: %+v", reqs[i])
		}
	}
	if _, ok := reqs[0].body["model"]; ok {
		t.Fatalf("il modello resta nel corpo: %v", reqs[0].body)
	}
	if reqs[2].body["model"] != ModelFast {
		t.Fatalf("il conteggio vuole il modello nel corpo: %v", reqs[2].body)
	}
	if tokens != 1 {
		t.Fatalf("il token si riusa: %d richieste", tokens)
	}
	// Senza indirizzo di prova, un indirizzo dei token che non è di Google si rifiuta.
	if _, err := NewVertex(ai.ProviderConfig{APIKey: sa, Region: "eu", ProjectID: "progetto-prova"}); ai.ErrorCode(err) != "auth" {
		t.Fatalf("indirizzo dei token estraneo: %v", err)
	}
	if _, err := p.Chat(ctx, ai.Request{Model: "../x", Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}}); err == nil {
		t.Fatal("modello con percorso accettato")
	}
	for r, want := range map[string]string{"global": "https://aiplatform.googleapis.com/", "eu": "https://aiplatform.eu.rep.googleapis.com/",
		"europe-west8": "https://europe-west8-aiplatform.googleapis.com/"} {
		if VertexBaseURL(r) != want {
			t.Fatalf("%s: %s", r, VertexBaseURL(r))
		}
	}
}
