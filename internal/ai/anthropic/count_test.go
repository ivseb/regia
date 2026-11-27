package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"regia/internal/ai"
)

// Conteggio dei token prima della chiamata con l'API ufficiale (/v1/messages/count_tokens):
// stessi modello, istruzioni, strumenti e messaggi; nessuna generazione.
func TestAnthropicCountTokens(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":1234}`)
	}))
	defer srv.Close()
	p, err := New(ai.ProviderConfig{APIKey: "sk-ant-finta-test", BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := p.(ai.TokenCounter).CountTokens(context.Background(), ai.Request{Model: ModelFast, System: "regole",
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "quanti token?"}},
		Tools:    []ai.ToolSpec{{Name: "cerca", Description: "Cerca", Schema: map[string]any{"type": "object", "properties": map[string]any{}}}}})
	if err != nil || n != 1234 {
		t.Fatalf("conteggio: %d %v", n, err)
	}
	if body["model"] != ModelFast || body["system"] == nil || len(body["tools"].([]any)) != 1 || len(body["messages"].([]any)) != 1 || body["max_tokens"] != nil {
		t.Fatalf("richiesta di conteggio: %v", body)
	}
}
