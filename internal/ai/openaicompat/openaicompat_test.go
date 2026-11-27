package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"regia/internal/ai"
)

// L'adattatore contro un server che risponde come Ollama o LM Studio: modelli installati,
// chiamate di strumenti, risultati con l'id, modello senza strumenti.
func TestOpenAICompatAdapter(t *testing.T) {
	var got []wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("chiave inviata senza che ci fosse: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:8b"},{"id":"llama3.2:3b"}]}`)
		case "/v1/chat/completions":
			var req wireRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			got = append(got, req)
			if req.Model == "gemma:2b" && len(req.Tools) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"registry.ollama.ai/library/gemma:2b does not support tools"}}`)
				return
			}
			if len(got) == 1 {
				_, _ = io.WriteString(w, `{"model":"qwen3:8b","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
					"tool_calls":[{"id":"call_a","type":"function","function":{"name":"leggi","arguments":"{\"elemento\":\"DEM-3\"}"}}]}}],
					"usage":{"prompt_tokens":50,"completion_tokens":7}}`)
				return
			}
			_, _ = io.WriteString(w, `{"model":"qwen3:8b","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"DEM-3 è aperta."}}],
				"usage":{"prompt_tokens":80,"completion_tokens":5}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Indirizzo senza percorso: riceve /v1 (Ollama, LM Studio).
	p, err := New(ai.ProviderConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	models, err := p.Models(ctx)
	if err != nil || len(models) != 2 || models[0].ID != "llama3.2:3b" {
		t.Fatalf("modelli installati: %+v %v", models, err)
	}
	tools := []ai.ToolSpec{{Name: "leggi", Description: "Legge", Schema: map[string]any{"type": "object"}}}
	msgs := []ai.Message{{Role: ai.RoleUser, Text: "com'è DEM-3?"}}
	r1, err := p.Chat(ctx, ai.Request{Model: "qwen3:8b", System: "regole", Messages: msgs, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stop != ai.StopToolUse || r1.Message.ToolCalls[0].ID != "call_a" || string(r1.Message.ToolCalls[0].Input) != `{"elemento":"DEM-3"}` || r1.InputTokens != 50 {
		t.Fatalf("prima risposta: %+v", r1)
	}
	if got[0].Messages[0].Role != "system" || got[0].Tools[0].Function.Name != "leggi" {
		t.Fatalf("richiesta: %+v", got[0])
	}
	msgs = append(msgs, r1.Message, ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{{CallID: "call_a", Content: "stato: aperta"}}})
	r2, err := p.Chat(ctx, ai.Request{Model: "qwen3:8b", Messages: msgs, Tools: tools})
	if err != nil || r2.Message.Text != "DEM-3 è aperta." || r2.Stop != ai.StopEnd {
		t.Fatalf("seconda risposta: %+v %v", r2, err)
	}
	last := got[1].Messages
	if last[1].Role != "assistant" || last[1].ToolCalls[0].ID != "call_a" || last[2].Role != "tool" || last[2].ToolCallID != "call_a" {
		t.Fatalf("storia rimandata: %+v", last)
	}

	_, err = p.Chat(ctx, ai.Request{Model: "gemma:2b", Messages: msgs[:1], Tools: tools})
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Code != "tools_unsupported" {
		t.Fatalf("modello senza strumenti: %v", err)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:11434":     "http://localhost:11434/v1",
		"https://api.openai.com/v1/": "https://api.openai.com/v1",
		"http://lm.lan:1234/api/v1":  "http://lm.lan:1234/api/v1",
	} {
		if got, err := NormalizeBaseURL(in); err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	for _, in := range []string{"", "ftp://x", "http://u:p@host", "http://host/v1?x=1"} {
		if _, err := NormalizeBaseURL(in); err == nil {
			t.Errorf("%q accettato", in)
		}
	}
}
