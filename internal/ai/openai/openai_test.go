package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"regia/internal/ai"
)

// Server locale che risponde come la Responses API di OpenAI. Nessuna chiave vera.
const key = "sk-finta-di-prova"

func authOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+key {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`)
		return false
	}
	return true
}

var cercaTool = []ai.ToolSpec{{Name: "cerca", Description: "Cerca", Schema: map[string]any{"type": "object",
	"properties": map[string]any{"testo": map[string]any{"type": "string"}}, "required": []string{"testo"}}}}

// Chiamata di strumenti con il ragionamento cifrato che torna nel passo successivo, sforzo
// per livello, store false, cache del prompt e token in cache.
func TestResponsesTools(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authOK(w, r) {
			return
		}
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"id":"resp_1","model":"gpt-6.2-astra","status":"completed","output":[
				{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"cifrato-1"},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"cerca","arguments":"{\"testo\":\"magazzino\"}","status":"completed"}],
				"usage":{"input_tokens":1200,"input_tokens_details":{"cached_tokens":1024},"output_tokens":40,"output_tokens_details":{"reasoning_tokens":30}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_2","model":"gpt-6.2-astra","status":"completed","output":[
			{"type":"message","id":"msg_2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Ho trovato DEM-1.","annotations":[]}]}],
			"usage":{"input_tokens":1300,"input_tokens_details":{"cached_tokens":0},"output_tokens":8}}`)
	}))
	defer srv.Close()
	p, err := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	msgs := []ai.Message{{Role: ai.RoleUser, Text: "cerca magazzino"}}
	r1, err := p.Chat(ctx, ai.Request{Model: ModelDeep, System: "regole", Messages: msgs, Tools: cercaTool, Deep: true, MaxTokens: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stop != ai.StopToolUse || len(r1.Message.ToolCalls) != 1 || r1.Message.ToolCalls[0].ID != "call_1" || string(r1.Message.ToolCalls[0].Input) != `{"testo":"magazzino"}` {
		t.Fatalf("chiamata: %+v", r1)
	}
	if r1.InputTokens != 1200 || r1.CacheReadTokens != 1024 || r1.OutputTokens != 40 {
		t.Fatalf("token: %+v", r1)
	}
	b := bodies[0]
	if b["instructions"] != "regole" || b["store"] != false || b["max_output_tokens"].(float64) != 4000 {
		t.Fatalf("richiesta: %v", b)
	}
	if eff := b["reasoning"].(map[string]any)["effort"]; eff != "high" {
		t.Fatalf("sforzo approfondito: %v", eff)
	}
	if !strings.HasPrefix(b["prompt_cache_key"].(string), "regia-") {
		t.Fatalf("chiave della cache: %v", b["prompt_cache_key"])
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "cerca" || tool["parameters"] == nil {
		t.Fatalf("strumento: %v", tool)
	}
	msgs = append(msgs, r1.Message, ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{{CallID: "call_1", Content: "DEM-1 Magazzino"}}})
	r2, err := p.Chat(ctx, ai.Request{Model: ModelFast, System: "regole", Messages: msgs, Tools: cercaTool})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Stop != ai.StopEnd || r2.Message.Text != "Ho trovato DEM-1." {
		t.Fatalf("risposta: %+v", r2)
	}
	in := bodies[1]["input"].([]any)
	if len(in) != 4 {
		t.Fatalf("elementi rimandati: %v", in)
	}
	if rs := in[1].(map[string]any); rs["type"] != "reasoning" || rs["encrypted_content"] != "cifrato-1" {
		t.Fatalf("ragionamento non rimandato: %v", rs)
	}
	if out := in[3].(map[string]any); out["type"] != "function_call_output" || out["call_id"] != "call_1" || out["output"] != "DEM-1 Magazzino" {
		t.Fatalf("risultato dello strumento: %v", out)
	}
	if eff := bodies[1]["reasoning"].(map[string]any)["effort"]; eff != "low" {
		t.Fatalf("sforzo rapido: %v", eff)
	}
}

// Streaming SSE: testo a pezzi, risposta intera nell'evento conclusivo; streaming interrotto.
func TestResponsesStream(t *testing.T) {
	cut := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authOK(w, r) {
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["stream"] != true {
			t.Errorf("manca stream: %v", b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ev := func(name, data string) {
			data = strings.Join(strings.Fields(data), " ") // un evento SSE è una riga sola
			_, _ = io.WriteString(w, "event: "+name+"\ndata: "+data+"\n\n")
		}
		ev("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`)
		ev("response.output_text.delta", `{"type":"response.output_text.delta","delta":"Ciao ","item_id":"msg_1","output_index":0,"content_index":0}`)
		ev("response.output_text.delta", `{"type":"response.output_text.delta","delta":"mondo","item_id":"msg_1","output_index":0,"content_index":0}`)
		if cut {
			return
		}
		ev("response.completed", `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-6-luna","status":"incomplete",
			"incomplete_details":{"reason":"max_output_tokens"},
			"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Ciao mondo"}]}],
			"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":0}}}}`)
	}))
	defer srv.Close()
	p, _ := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/v1"})
	var got []string
	r, err := p.(ai.Streamer).ChatStream(context.Background(), ai.Request{Model: ModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}},
		func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Ciao |mondo" || r.Message.Text != "Ciao mondo" || r.Stop != ai.StopMaxTokens || r.OutputTokens != 3 {
		t.Fatalf("streaming: %v %+v", got, r)
	}
	cut = true
	if _, err := p.(ai.Streamer).ChatStream(context.Background(), ai.Request{Model: ModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}}, func(string) {}); ai.ErrorCode(err) != "network" {
		t.Fatalf("streaming interrotto: %v", err)
	}
}

// Conteggio dei token, elenco dei modelli, errori tradotti senza il corpo della risposta.
func TestResponsesCountModelsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authOK(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/responses/input_tokens":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["model"] != ModelDeep || b["instructions"] != "regole" || b["tools"] == nil {
				t.Errorf("conteggio: %v", b)
			}
			_, _ = io.WriteString(w, `{"object":"response.input_tokens","input_tokens":321}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gpt-6.2-astra"},{"id":"text-embedding-4"},{"id":"gpt-6-luna"},{"id":"whisper-2"}]}`)
		case "/v1/responses":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"You exceeded your current quota, segreto-del-servizio","type":"insufficient_quota","code":"insufficient_quota"}}`)
		}
	}))
	defer srv.Close()
	p, _ := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/v1"})
	ctx := context.Background()
	n, err := p.(ai.TokenCounter).CountTokens(ctx, ai.Request{Model: ModelDeep, System: "regole", Tools: cercaTool, Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}})
	if err != nil || n != 321 {
		t.Fatalf("conteggio: %d %v", n, err)
	}
	list, err := p.Models(ctx)
	if err != nil || len(list) != 2 || list[0].ID != ModelFast || list[0].Tier != ai.TierFast || list[1].Tier != ai.TierDeep {
		t.Fatalf("modelli: %+v %v", list, err)
	}
	_, err = p.Chat(ctx, ai.Request{Model: ModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}})
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Code != "quota" || strings.Contains(pe.Error(), "segreto") {
		t.Fatalf("errore: %v", err)
	}
	bad, _ := New(ai.ProviderConfig{APIKey: "sk-sbagliata", BaseURL: srv.URL + "/v1"})
	if _, err := bad.Models(ctx); ai.ErrorCode(err) != "auth" {
		t.Fatalf("chiave sbagliata: %v", err)
	}
	if _, err := New(ai.ProviderConfig{}); ai.ErrorCode(err) != "auth" {
		t.Fatalf("senza chiave: %v", err)
	}
	// Un modello che non ragiona non riceve reasoning (lo rifiuterebbe).
	if b := build(ai.Request{Model: "gpt-4.1", Deep: true}); b.Reasoning != nil {
		t.Fatalf("reasoning su un modello senza ragionamento: %+v", b.Reasoning)
	}
}
