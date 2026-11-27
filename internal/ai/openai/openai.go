// Package openai è l'adattatore nativo di OpenAI (replica/ai.md §3) sulla Responses API, la
// forma che OpenAI indica per i modelli che ragionano: chiamata di strumenti con il
// ragionamento che resta fra un passo e l'altro (elementi «reasoning» cifrati, store: false),
// sforzo di ragionamento per livello, cache del prompt automatica (prompt_cache_key, token in
// cache nel registro e nel costo), conteggio dei token prima della chiamata
// (/responses/input_tokens), streaming SSE.
//
// Usa l'API HTTP documentata e non l'SDK ufficiale github.com/openai/openai-go/v3: l'SDK
// aggiunge circa 19 MB al binario (misurati il 2026-11-27, da 43 a 62 MB) per un adattatore
// che usa quattro chiamate. I nomi dei campi sono quelli dell'SDK ufficiale v3.108.0.
//
// L'indirizzo base si può cambiare per la residenza dei dati (es. https://eu.api.openai.com/v1);
// i servizi compatibili (Azure, Mistral, vLLM...) restano su internal/ai/openaicompat.
package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"regia/internal/ai"
	"regia/internal/ai/wire"
)

// Kind è il tipo di fornitore.
const Kind = "openai"

// DefaultBaseURL è l'API di OpenAI.
const DefaultBaseURL = "https://api.openai.com/v1"

// Modelli proposti per i livelli (listino OpenAI verificato il 2026-11-27).
const (
	ModelFast = "gpt-6-luna"
	ModelDeep = "gpt-6.2-astra"
)

// Register descrive il tipo di fornitore per il registro di ai.Service.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, NeedsKey: true, External: true, Tools: true,
		OptionalURL: true, DefaultURL: DefaultBaseURL, URLSuffix: "/v1",
		Proposed: []ai.ModelInfo{
			{ID: ModelFast, Name: "GPT-6 Luna", Tier: ai.TierFast},
			{ID: ModelDeep, Name: "GPT-6.2 Astra", Tier: ai.TierDeep},
		}}
}

type provider struct{ c wire.Client }

// New crea l'adattatore con la chiave del tenant.
func New(cfg ai.ProviderConfig) (ai.Provider, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, &ai.ProviderError{Code: "auth", Msg: "manca la chiave di OpenAI"}
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return &provider{c: wire.Client{Base: base, HC: hc, Who: "OpenAI",
		Header: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }}}, nil
}

// reasoning: i modelli che ragionano accettano reasoning.effort (gli altri lo rifiutano).
func reasoning(model string) bool {
	for _, p := range []string{"gpt-6", "gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

type tool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type effort struct {
	Effort string `json:"effort"`
}

type request struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           []json.RawMessage `json:"input"`
	Tools           []tool            `json:"tools,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Reasoning       *effort           `json:"reasoning,omitempty"`
	// Store false: OpenAI non conserva la risposta; il ragionamento torna cifrato negli
	// elementi «reasoning» e Regia lo rimanda nel passo successivo.
	Store          *bool    `json:"store,omitempty"`
	Include        []string `json:"include,omitempty"`
	PromptCacheKey string   `json:"prompt_cache_key,omitempty"`
	Stream         bool     `json:"stream,omitempty"`
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// input traduce i messaggi neutri negli elementi della Responses API. Le risposte del modello
// nella stessa richiesta tornano come gli elementi ricevuti (ragionamento cifrato compreso).
func input(msgs []ai.Message) []json.RawMessage {
	var out []json.RawMessage
	for _, m := range msgs {
		if m.Role == ai.RoleAssistant {
			if items, ok := m.Native.([]json.RawMessage); ok && len(items) > 0 {
				out = append(out, items...)
				continue
			}
			if m.Text != "" {
				out = append(out, raw(map[string]any{"role": "assistant", "content": m.Text}))
			}
			for _, c := range m.ToolCalls {
				args := string(c.Input)
				if args == "" {
					args = "{}"
				}
				out = append(out, raw(map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": args}))
			}
			continue
		}
		for _, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "ERRORE: " + content
			}
			out = append(out, raw(map[string]any{"type": "function_call_output", "call_id": r.CallID, "output": content}))
		}
		if m.Text != "" {
			out = append(out, raw(map[string]any{"role": "user", "content": m.Text}))
		}
	}
	if out == nil {
		out = []json.RawMessage{}
	}
	return out
}

func build(req ai.Request) request {
	no := false
	w := request{Model: req.Model, Instructions: req.System, Input: input(req.Messages), MaxOutputTokens: req.MaxTokens,
		Store: &no, Include: []string{"reasoning.encrypted_content"}}
	if reasoning(req.Model) {
		// I valori di Regia coincidono con quelli di OpenAI (low, medium, high).
		w.Reasoning = &effort{Effort: ai.EffortOf(req)}
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, tool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	// Cache del prompt: la chiave raggruppa le richieste con le stesse istruzioni, così il
	// prefisso comune si rilegge dalla cache (OpenAI la applica da sé oltre una certa lunghezza).
	h := sha256.Sum256([]byte(req.Model + "\x00" + req.System))
	w.PromptCacheKey = "regia-" + hex.EncodeToString(h[:12])
	return w
}

// response è la risposta della Responses API (anche dentro gli eventi dello streaming).
type response struct {
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  *struct {
		InputTokens        int64 `json:"input_tokens"`
		OutputTokens       int64 `json:"output_tokens"`
		InputTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type outputItem struct {
	Type    string `json:"type"`
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Args    string `json:"arguments"`
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

func convert(req ai.Request, r response) (*ai.Response, error) {
	if r.Status == "failed" || r.Error != nil {
		return nil, &ai.ProviderError{Code: "provider_error", Msg: "OpenAI non ha completato la risposta"}
	}
	out := &ai.Response{Model: r.Model}
	if out.Model == "" {
		out.Model = req.Model
	}
	if r.Usage != nil {
		out.InputTokens, out.OutputTokens = r.Usage.InputTokens, r.Usage.OutputTokens
		out.CacheReadTokens = r.Usage.InputTokensDetails.CachedTokens
	}
	out.Message = ai.Message{Role: ai.RoleAssistant, Native: r.Output}
	var text []string
	refused := false
	for _, item := range r.Output {
		var it outputItem
		if err := json.Unmarshal(item, &it); err != nil {
			return nil, wire.BadResponse("OpenAI")
		}
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				switch c.Type {
				case "output_text":
					text = append(text, c.Text)
				case "refusal":
					refused = true
					text = append(text, c.Refusal)
				}
			}
		case "function_call":
			args := strings.TrimSpace(it.Args)
			if args == "" || !json.Valid([]byte(args)) {
				args = "{}"
			}
			out.Message.ToolCalls = append(out.Message.ToolCalls, ai.ToolCall{ID: it.CallID, Name: it.Name, Input: json.RawMessage(args)})
		}
	}
	out.Message.Text = strings.Join(text, "\n\n")
	switch {
	case refused:
		out.Stop = ai.StopRefusal
	case len(out.Message.ToolCalls) > 0:
		out.Stop = ai.StopToolUse
	case r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens":
		out.Stop = ai.StopMaxTokens
	case r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "content_filter":
		out.Stop = ai.StopRefusal
	default:
		out.Stop = ai.StopEnd
	}
	return out, nil
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	var r response
	if err := p.c.JSON(ctx, http.MethodPost, "/responses", build(req), &r); err != nil {
		return nil, err
	}
	return convert(req, r)
}

// ChatStream chiede la risposta in streaming: gli eventi «response.output_text.delta» portano
// il testo, «response.completed» (o «response.incomplete») la risposta intera con l'uso dei token.
func (p *provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	body := build(req)
	body.Stream = true
	resp, err := p.c.Send(ctx, http.MethodPost, "/responses", body, "text/event-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var final *response
	var failed error
	err = wire.SSE(ctx, resp.Body, func(event, data string) error {
		var ev struct {
			Type     string    `json:"type"`
			Delta    string    `json:"delta"`
			Response *response `json:"response"`
			Code     string    `json:"code"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return wire.BadResponse("OpenAI")
		}
		if ev.Type == "" {
			ev.Type = event
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" && onText != nil {
				onText(ev.Delta)
			}
		case "response.completed", "response.incomplete":
			final = ev.Response
			return wire.ErrStop
		case "response.failed", "error":
			failed = &ai.ProviderError{Code: "provider_error", Msg: "OpenAI non ha completato la risposta"}
			return wire.ErrStop
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failed != nil {
		return nil, failed
	}
	if final == nil {
		return nil, wire.Interrupted()
	}
	return convert(req, *final)
}

// CountTokens conta i token dell'input con /responses/input_tokens (stessi modello,
// istruzioni, strumenti e messaggi della chiamata, senza generare).
func (p *provider) CountTokens(ctx context.Context, req ai.Request) (int64, error) {
	b := build(req)
	body := map[string]any{"model": b.Model, "input": b.Input}
	if b.Instructions != "" {
		body["instructions"] = b.Instructions
	}
	if len(b.Tools) > 0 {
		body["tools"] = b.Tools
	}
	if b.Reasoning != nil {
		body["reasoning"] = b.Reasoning
	}
	var r struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := p.c.JSON(ctx, http.MethodPost, "/responses/input_tokens", body, &r); err != nil {
		return 0, err
	}
	return r.InputTokens, nil
}

// chatModel: esclude dall'elenco i modelli che non conversano (embedding, audio, immagini...).
func chatModel(id string) bool {
	for _, x := range []string{"embedding", "whisper", "tts", "dall-e", "moderation", "image", "transcribe", "realtime", "audio", "search", "davinci", "babbage", "sora"} {
		if strings.Contains(id, x) {
			return false
		}
	}
	return true
}

// Models elenca i modelli dell'account (verifica la chiave) e segna quelli proposti.
func (p *provider) Models(ctx context.Context) ([]ai.ModelInfo, error) {
	var w struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.c.JSON(ctx, http.MethodGet, "/models", nil, &w); err != nil {
		return nil, err
	}
	out := make([]ai.ModelInfo, 0, len(w.Data))
	for _, m := range w.Data {
		if m.ID == "" || !chatModel(m.ID) {
			continue
		}
		info := ai.ModelInfo{ID: m.ID}
		switch m.ID {
		case ModelFast:
			info.Tier = ai.TierFast
		case ModelDeep:
			info.Tier = ai.TierDeep
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
