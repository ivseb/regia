package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"regia/internal/ai"
)

var cercaTool = []ai.ToolSpec{{Name: "cerca", Description: "Cerca", Schema: map[string]any{"type": "object",
	"properties": map[string]any{"testo": map[string]any{"type": "string"}}, "required": []string{"testo"}}}}

// Server locale che risponde come l'API di Ollama: modelli installati con le capacità (anche
// dalla vecchia /api/show), chat con strumenti a righe JSON, errori.
func TestOllama(t *testing.T) {
	var chats []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("senza chiave non va l'intestazione: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[
				{"name":"qwen4:32b","model":"qwen4:32b","size":20000000000,"capabilities":["completion","tools","thinking"]},
				{"name":"qwen4:4b","model":"qwen4:4b","size":3000000000,"capabilities":["completion","tools"]},
				{"name":"gemma:2b","model":"gemma:2b","size":1500000000,"capabilities":["completion"]},
				{"name":"nomic-embed","model":"nomic-embed","size":300000000,"capabilities":["embedding"]},
				{"name":"vecchio:7b","model":"vecchio:7b","size":5000000000}]}`)
		case "/api/show":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["model"] != "vecchio:7b" {
				t.Errorf("show per un modello che ha già le capacità: %v", b)
			}
			_, _ = io.WriteString(w, `{"capabilities":["completion"]}`)
		case "/api/chat":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			chats = append(chats, b)
			if b["model"] == "gemma:2b" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"registry.ollama.ai/library/gemma:2b does not support tools"}`)
				return
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			if len(chats) == 1 {
				_, _ = io.WriteString(w, `{"model":"qwen4:32b","message":{"role":"assistant","content":"","thinking":"penso"},"done":false}
{"model":"qwen4:32b","message":{"role":"assistant","content":"","tool_calls":[{"function":{"index":0,"name":"cerca","arguments":{"testo":"magazzino"}}}]},"done":false}
{"model":"qwen4:32b","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":80,"eval_count":12}
`)
				return
			}
			_, _ = io.WriteString(w, `{"model":"qwen4:32b","message":{"role":"assistant","content":"Ho trovato "},"done":false}
{"model":"qwen4:32b","message":{"role":"assistant","content":"DEM-1."},"done":false}
{"model":"qwen4:32b","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":120,"eval_count":5,"prompt_eval_cached_count":80}
`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, err := New(ai.ProviderConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	list, err := p.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ai.ModelInfo{}
	for _, m := range list {
		byID[m.ID] = m
	}
	if len(list) != 4 || byID["nomic-embed"].ID != "" {
		t.Fatalf("modelli (senza quelli solo di embedding): %+v", list)
	}
	if byID["qwen4:32b"].Tier != ai.TierDeep || byID["qwen4:4b"].Tier != ai.TierFast || !byID["qwen4:32b"].Thinking {
		t.Fatalf("livelli proposti: %+v", list)
	}
	if g := byID["gemma:2b"]; g.Tools == nil || *g.Tools || g.Tier != "" {
		t.Fatalf("modello senza strumenti: %+v", g)
	}
	if v := byID["vecchio:7b"]; v.Tools == nil || *v.Tools {
		t.Fatalf("capacità dalla /api/show: %+v", v)
	}

	msgs := []ai.Message{{Role: ai.RoleUser, Text: "cerca magazzino"}}
	r1, err := p.Chat(ctx, ai.Request{Model: "qwen4:32b", System: "regole", Messages: msgs, Tools: cercaTool, MaxTokens: 500})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stop != ai.StopToolUse || len(r1.Message.ToolCalls) != 1 || string(r1.Message.ToolCalls[0].Input) != `{"testo":"magazzino"}` || r1.InputTokens != 80 || r1.OutputTokens != 12 {
		t.Fatalf("chiamata: %+v", r1)
	}
	if c := chats[0]; c["stream"] != false || c["options"].(map[string]any)["num_predict"].(float64) != 500 || c["messages"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Fatalf("richiesta: %v", c)
	}
	msgs = append(msgs, r1.Message, ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{{CallID: r1.Message.ToolCalls[0].ID, Content: "DEM-1"}}})
	var got []string
	r2, err := p.(ai.Streamer).ChatStream(ctx, ai.Request{Model: "qwen4:32b", Messages: msgs, Tools: cercaTool}, func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Ho trovato |DEM-1." || r2.Message.Text != "Ho trovato DEM-1." || r2.CacheReadTokens != 80 {
		t.Fatalf("streaming: %v %+v", got, r2)
	}
	sent := chats[1]["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("messaggi: %v", sent)
	}
	if tm := sent[2].(map[string]any); tm["role"] != "tool" || tm["tool_name"] != "cerca" || tm["content"] != "DEM-1" {
		t.Fatalf("risultato dello strumento: %v", tm)
	}
	if am := sent[1].(map[string]any); am["thinking"] != "penso" || am["tool_calls"] == nil {
		t.Fatalf("risposta del modello rimandata: %v", am)
	}
	if _, err := p.Chat(ctx, ai.Request{Model: "gemma:2b", Messages: msgs, Tools: cercaTool}); ai.ErrorCode(err) != "tools_unsupported" {
		t.Fatalf("modello senza strumenti: %v", err)
	}
}

// Con la chiave (Ollama dietro un proxy o in cloud) va come Bearer; streaming interrotto.
func TestOllamaKeyAndCut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chiave-di-prova" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"a metà"},"done":false}`+"\n")
	}))
	defer srv.Close()
	p, _ := New(ai.ProviderConfig{BaseURL: srv.URL + "/", APIKey: "chiave-di-prova"})
	if _, err := p.Chat(context.Background(), ai.Request{Model: "m", Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}}); ai.ErrorCode(err) != "network" {
		t.Fatalf("streaming interrotto: %v", err)
	}
	nokey, _ := New(ai.ProviderConfig{BaseURL: srv.URL})
	if _, err := nokey.Models(context.Background()); ai.ErrorCode(err) != "auth" {
		t.Fatalf("senza chiave: %v", err)
	}
}
