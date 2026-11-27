package openaicompat

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

// Streaming del protocollo Chat Completions («stream: true»): testo a pezzi, chiamata di
// strumento ricomposta dai pezzi per indice, uso dei token nell'ultimo pezzo, «[DONE]»; un
// servizio che ignora lo streaming e risponde in JSON si legge lo stesso.
func TestOpenAICompatStream(t *testing.T) {
	var bodies []wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req wireRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		bodies = append(bodies, req)
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept: %q", r.Header.Get("Accept"))
		}
		switch req.Model {
		case "json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"model":"json","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"tutto insieme"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			return
		case "interrotto":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a metà\"}}]}\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{
			`: ping`,
			`data: {"model":"qwen3:8b","choices":[{"delta":{"role":"assistant","content":"Cerco "}}]}`,
			`data: {"choices":[{"delta":{"content":"DEM-3."}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"leg","arguments":"{\"elem"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"gi","arguments":"ento\":\"DEM-3\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":9}}`,
			`data: [DONE]`,
		} {
			_, _ = io.WriteString(w, ev+"\n\n")
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	p, err := New(ai.ProviderConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	st := p.(ai.Streamer)
	var parts []string
	resp, err := st.ChatStream(context.Background(), ai.Request{Model: "qwen3:8b", Messages: []ai.Message{{Role: ai.RoleUser, Text: "leggi DEM-3"}},
		Tools: []ai.ToolSpec{{Name: "leggi", Schema: map[string]any{"type": "object"}}}}, func(s string) { parts = append(parts, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(parts, "|") != "Cerco |DEM-3." || resp.Message.Text != "Cerco DEM-3." {
		t.Fatalf("testo a pezzi: %q %q", parts, resp.Message.Text)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_x" || resp.Message.ToolCalls[0].Name != "leggi" ||
		string(resp.Message.ToolCalls[0].Input) != `{"elemento":"DEM-3"}` || resp.Stop != ai.StopToolUse {
		t.Fatalf("strumento ricomposto: %+v", resp)
	}
	if resp.InputTokens != 40 || resp.OutputTokens != 9 || resp.Model != "qwen3:8b" {
		t.Fatalf("uso dei token: %+v", resp)
	}
	if !bodies[0].Stream || bodies[0].StreamOptions == nil || !bodies[0].StreamOptions.IncludeUsage {
		t.Fatalf("richiesta senza stream: %+v", bodies[0])
	}
	// Servizio che risponde in JSON nonostante «stream: true».
	parts = nil
	resp, err = st.ChatStream(context.Background(), ai.Request{Model: "json", Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}}, func(s string) { parts = append(parts, s) })
	if err != nil || resp.Message.Text != "tutto insieme" || len(parts) != 1 || resp.InputTokens != 3 {
		t.Fatalf("risposta JSON: %v %+v %q", err, resp, parts)
	}
	// Streaming interrotto prima della fine: errore di rete, non una risposta a metà.
	if _, err := st.ChatStream(context.Background(), ai.Request{Model: "interrotto", Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}}, func(string) {}); err == nil {
		t.Fatal("streaming interrotto accettato")
	}
}
