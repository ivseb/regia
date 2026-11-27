package gemini

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

// Server locale che risponde come l'API Gemini (generateContent). Nessuna chiave vera.
const key = "chiave-gemini-di-prova"

func authOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("x-goog-api-key") != key || strings.Contains(r.URL.RawQuery, key) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`)
		return false
	}
	return true
}

var cercaTool = []ai.ToolSpec{{Name: "cerca", Description: "Cerca", Schema: map[string]any{"type": "object",
	"properties": map[string]any{"testo": map[string]any{"type": "string"}}, "required": []string{"testo"}}}}

// Chiamata di strumenti: la firma del ragionamento torna invariata, il risultato porta il nome
// della funzione, il livello di ragionamento segue il livello di Regia.
func TestGeminiTools(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authOK(w, r) {
			return
		}
		if r.URL.Path != "/v1/models/gemini-4-carbon:generateContent" {
			t.Errorf("percorso: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[
				{"text":"penso","thought":true},
				{"functionCall":{"name":"cerca","args":{"testo":"magazzino"}},"thoughtSignature":"ZmlybWEx"}]},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":900,"cachedContentTokenCount":512,"candidatesTokenCount":10,"thoughtsTokenCount":30},"modelVersion":"gemini-4-carbon"}`)
			return
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Ho trovato DEM-1."}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":950,"candidatesTokenCount":6}}`)
	}))
	defer srv.Close()
	p, err := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	msgs := []ai.Message{{Role: ai.RoleUser, Text: "cerca magazzino"}}
	r1, err := p.Chat(ctx, ai.Request{Model: ModelDeep, System: "regole", Messages: msgs, Tools: cercaTool, Deep: true, MaxTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stop != ai.StopToolUse || len(r1.Message.ToolCalls) != 1 || r1.Message.ToolCalls[0].Name != "cerca" || r1.Message.Text != "" {
		t.Fatalf("chiamata: %+v", r1)
	}
	if r1.InputTokens != 900 || r1.CacheReadTokens != 512 || r1.OutputTokens != 40 {
		t.Fatalf("token: %+v", r1)
	}
	b := bodies[0]
	if si := b["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]; si != "regole" {
		t.Fatalf("istruzioni: %v", b)
	}
	gc := b["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"].(float64) != 2000 || gc["thinkingConfig"].(map[string]any)["thinkingLevel"] != "HIGH" {
		t.Fatalf("configurazione: %v", gc)
	}
	decl := b["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["name"] != "cerca" || decl["parametersJsonSchema"] == nil {
		t.Fatalf("dichiarazione: %v", decl)
	}
	msgs = append(msgs, r1.Message, ai.Message{Role: ai.RoleUser, ToolResults: []ai.ToolResult{{CallID: r1.Message.ToolCalls[0].ID, Content: "DEM-1 Magazzino"}}})
	r2, err := p.Chat(ctx, ai.Request{Model: ModelDeep, Messages: msgs, Tools: cercaTool, Deep: true})
	if err != nil || r2.Message.Text != "Ho trovato DEM-1." || r2.Stop != ai.StopEnd {
		t.Fatalf("risposta: %+v %v", r2, err)
	}
	contents := bodies[1]["contents"].([]any)
	model := contents[1].(map[string]any)["parts"].([]any)[1].(map[string]any)
	if model["thoughtSignature"] != "ZmlybWEx" || model["functionCall"] == nil {
		t.Fatalf("firma non rimandata: %v", model)
	}
	fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "cerca" || fr["id"] != nil || fr["response"].(map[string]any)["output"] != "DEM-1 Magazzino" {
		t.Fatalf("risultato: %v", fr)
	}
}

// Streaming SSE, blocco per sicurezza, conteggio dei token, modelli a pagine, chiave sbagliata.
func TestGeminiStreamCountModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authOK(w, r) {
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
			if r.URL.Query().Get("alt") != "sse" {
				t.Errorf("manca alt=sse")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, d := range []string{
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"Ciao "}]}}]}`,
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"mondo"}]}}]}`,
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"","thoughtSignature":"c2ln"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
			} {
				_, _ = io.WriteString(w, "data: "+d+"\r\n\r\n")
			}
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			_, _ = io.WriteString(w, `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":5}}`)
		case strings.HasSuffix(r.URL.Path, ":countTokens"):
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["contents"] == nil {
				t.Errorf("conteggio senza contenuti: %v", b)
			}
			_, _ = io.WriteString(w, `{"totalTokens":40}`)
		case r.URL.Path == "/v1/models":
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-4-carbon","displayName":"Gemini 4 Carbon","supportedGenerationMethods":["generateContent","countTokens"]},
					{"name":"models/text-embedding-5","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"p2"}`)
				return
			}
			_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-4-hydrogen","displayName":"Gemini 4 Hydrogen","supportedGenerationMethods":["generateContent"]}]}`)
		}
	}))
	defer srv.Close()
	p, _ := New(ai.ProviderConfig{APIKey: key, BaseURL: srv.URL + "/v1"})
	ctx := context.Background()
	var got []string
	r, err := p.(ai.Streamer).ChatStream(ctx, ai.Request{Model: ModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "ciao"}}}, func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Ciao |mondo" || r.Message.Text != "Ciao mondo" || r.InputTokens != 5 || r.OutputTokens != 2 {
		t.Fatalf("streaming: %v %+v", got, r)
	}
	var native map[string]any
	_ = json.Unmarshal(r.Message.Native.(json.RawMessage), &native)
	if parts := native["parts"].([]any); len(parts) != 1 || parts[0].(map[string]any)["thoughtSignature"] != "c2ln" {
		t.Fatalf("parti ricomposte: %v", native)
	}
	blocked, err := p.Chat(ctx, ai.Request{Model: ModelFast, Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}})
	if err != nil || blocked.Stop != ai.StopRefusal {
		t.Fatalf("blocco: %+v %v", blocked, err)
	}
	n, err := p.(ai.TokenCounter).CountTokens(ctx, ai.Request{Model: ModelFast, System: "regole", Tools: cercaTool, Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}})
	if err != nil || n <= 40 {
		t.Fatalf("conteggio (contenuti + stima di istruzioni e strumenti): %d %v", n, err)
	}
	list, err := p.Models(ctx)
	if err != nil || len(list) != 2 || list[0].ID != ModelDeep || list[0].Tier != ai.TierDeep || list[1].Tier != ai.TierFast {
		t.Fatalf("modelli: %+v %v", list, err)
	}
	bad, _ := New(ai.ProviderConfig{APIKey: "sbagliata", BaseURL: srv.URL + "/v1"})
	if _, err := bad.Models(ctx); ai.ErrorCode(err) != "auth" {
		t.Fatalf("chiave sbagliata: %v", err)
	}
	if _, err := p.Chat(ctx, ai.Request{Model: "../segreti", Messages: []ai.Message{{Role: ai.RoleUser, Text: "x"}}}); ai.ErrorCode(err) != "model_not_found" {
		t.Fatalf("modello con percorso: %v", err)
	}
	if b := build(ai.Request{Model: "gemini-2.5-flash", Deep: true}); b.GenerationConfig != nil {
		t.Fatalf("thinkingLevel su un modello che non lo accetta: %+v", b.GenerationConfig)
	}
}

// Una risposta troncata dal limite di token può arrivare senza parti: non torna nella
// richiesta successiva (Gemini rifiuterebbe il contenuto vuoto), e il livello di ragionamento
// resta basso per le richieste brevi anche al livello approfondito.
func TestGeminiRispostaVuotaEBreve(t *testing.T) {
	vuota := convert(ai.Request{Model: "gemini-4-carbon"}, content{}, "MAX_TOKENS", "", nil, "")
	if vuota.Stop != ai.StopMaxTokens || vuota.Message.Text != "" {
		t.Fatalf("risposta troncata: %+v", vuota)
	}
	req := ai.Request{Model: "gemini-4-carbon", Deep: true, Brief: true, Messages: []ai.Message{
		{Role: ai.RoleUser, Text: "domanda"},
		vuota.Message,
		{Role: ai.RoleUser, Text: "correggi"},
	}}
	w := build(req)
	if len(w.Contents) != 2 {
		t.Fatalf("contenuti: %d, voluti 2 (la risposta vuota si salta)", len(w.Contents))
	}
	for _, c := range w.Contents {
		if strings.Contains(string(c), `"parts":[]`) {
			t.Fatalf("contenuto vuoto nella richiesta: %s", c)
		}
	}
	if w.GenerationConfig == nil || w.GenerationConfig.ThinkingConfig == nil || w.GenerationConfig.ThinkingConfig.ThinkingLevel != "LOW" {
		t.Fatalf("ragionamento di una richiesta breve: %+v", w.GenerationConfig)
	}
	req.Brief = false
	if l := build(req).GenerationConfig.ThinkingConfig.ThinkingLevel; l != "HIGH" {
		t.Fatalf("ragionamento approfondito: %s", l)
	}
}
