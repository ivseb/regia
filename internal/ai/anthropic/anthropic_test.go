package anthropic

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

// L'adattatore contro un server locale che risponde come l'API Messages: strumenti, effort,
// ritorno dei blocchi nativi, conteggio dei token, errori senza segreti. Nessuna chiave vera.
func TestAnthropicAdapter(t *testing.T) {
	const key = "sk-ant-finta-test"
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Api-Key") != key && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","type":"model","created_at":"2026-01-01T00:00:00Z"},
				{"id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5","type":"model","created_at":"2026-01-01T00:00:00Z"}],"has_more":false}`)
		case "/v1/messages":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			bodies = append(bodies, b)
			if len(bodies) == 1 {
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",
					"content":[{"type":"thinking","thinking":"","signature":"firma"},{"type":"text","text":"Cerco."},
						{"type":"tool_use","id":"toolu_1","name":"cerca","input":{"testo":"magazzino"}}],
					"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":0}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
				"content":[{"type":"text","text":"Ho trovato DEM-1."}],"usage":{"input_tokens":150,"output_tokens":8}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tools := []ai.ToolSpec{{Name: "cerca", Description: "Cerca", Schema: map[string]any{"type": "object",
		"properties": map[string]any{"testo": map[string]any{"type": "string"}}, "required": []string{"testo"}}}}
	msgs := []ai.Message{{Role: ai.RoleUser, Text: "cerca magazzino"}}
	r1, err := p.Chat(ctx, ai.Request{Model: ModelDeep, System: "regole", Messages: msgs, Tools: tools, Deep: true})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stop != ai.StopToolUse || len(r1.Message.ToolCalls) != 1 || r1.Message.ToolCalls[0].Name != "cerca" || r1.InputTokens != 110 || r1.OutputTokens != 20 {
		t.Fatalf("prima risposta: %+v", r1)
	}
	var in map[string]string
	if err := json.Unmarshal(r1.Message.ToolCalls[0].Input, &in); err != nil || in["testo"] != "magazzino" {
		t.Fatalf("input dello strumento: %s", r1.Message.ToolCalls[0].Input)
	}
	b := bodies[0]
	if b["model"] != ModelDeep || b["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatalf("richiesta: %v", b)
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "cerca" || tool["input_schema"].(map[string]any)["required"].([]any)[0] != "testo" {
		t.Fatalf("strumento: %v", tool)
	}
	if sys := b["system"].([]any)[0].(map[string]any); sys["text"] != "regole" {
		t.Fatalf("istruzioni: %v", b["system"])
	}

	msgs = append(msgs, r1.Message, ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{{CallID: "toolu_1", Content: "DEM-1 · Richiesta"}}})
	r2, err := p.Chat(ctx, ai.Request{Model: ModelFast, Messages: msgs, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Stop != ai.StopEnd || r2.Message.Text != "Ho trovato DEM-1." {
		t.Fatalf("seconda risposta: %+v", r2)
	}
	sent, _ := json.Marshal(bodies[1]["messages"])
	// Il blocco di ragionamento torna invariato, il risultato dello strumento porta l'id.
	if !strings.Contains(string(sent), `"signature":"firma"`) || !strings.Contains(string(sent), `"tool_use_id":"toolu_1"`) {
		t.Fatalf("storia rimandata: %s", sent)
	}
	if bodies[1]["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("effort del livello rapido: %v", bodies[1]["output_config"])
	}

	models, err := p.Models(ctx)
	if err != nil || len(models) != 2 || models[0].Tier != ai.TierDeep || models[1].Tier != ai.TierFast {
		t.Fatalf("modelli: %+v %v", models, err)
	}

	// Chiave sbagliata: errore «auth», senza la risposta grezza.
	bad, _ := New(ai.ProviderConfig{APIKey: "sk-sbagliata", BaseURL: srv.URL + "/"})
	_, err = bad.Chat(ctx, ai.Request{Model: ModelDeep, Messages: msgs[:1]})
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Code != "auth" || strings.Contains(err.Error(), "sk-sbagliata") {
		t.Fatalf("errore di autenticazione: %v", err)
	}
	if _, err := New(ai.ProviderConfig{}); err == nil {
		t.Fatal("adattatore senza chiave")
	}
}
