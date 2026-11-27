// Package openaicompat è l'adattatore generico per i servizi che espongono il protocollo
// compatibile OpenAI (Chat Completions): OpenAI, Azure OpenAI, Mistral, vLLM, LM Studio, gli
// endpoint compatibili di Gemini e di Ollama (replica/ai.md §3). Solo libreria standard.
//
// L'indirizzo base è quello del servizio con il suo prefisso (https://api.openai.com/v1,
// http://localhost:11434/v1); un indirizzo senza percorso riceve /v1 (Ollama, LM Studio).
// La chiave è facoltativa (Ollama e LM Studio non la chiedono). Il client HTTP lo prepara
// ai.Service con la politica di rete dei connettori (niente indirizzi interni senza consenso).
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"regia/internal/ai"
)

// Kind è il tipo di fornitore.
const Kind = "openai_compat"

// Register descrive il tipo di fornitore per il registro di ai.Service.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, NeedsURL: true, External: true, URLSuffix: "/v1", DefaultURL: "http://localhost:1234/v1"}
}

type provider struct {
	base string
	key  string
	hc   *http.Client
}

// NormalizeBaseURL controlla l'indirizzo e aggiunge /v1 se manca il percorso.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("serve l'indirizzo base del servizio (es. http://localhost:11434/v1)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("indirizzo non valido: serve http:// o https:// con l'host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("l'indirizzo non può contenere credenziali, parametri o frammenti")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

// New crea l'adattatore. Senza HTTPClient usa un client con timeout (solo nei test).
func New(cfg ai.ProviderConfig) (ai.Provider, error) {
	base, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, &ai.ProviderError{Code: "config", Msg: err.Error()}
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return &provider{base: base, key: strings.TrimSpace(cfg.APIKey), hc: hc}, nil
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	// Stream chiede la risposta a pezzi (SSE); StreamOptions l'uso dei token nell'ultimo pezzo.
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func str(s string) *string { return &s }

func toWire(req ai.Request) wireRequest {
	w := wireRequest{Model: req.Model, MaxTokens: req.MaxTokens}
	if req.System != "" {
		w.Messages = append(w.Messages, wireMessage{Role: "system", Content: str(req.System)})
	}
	for _, m := range req.Messages {
		if m.Role == ai.RoleAssistant {
			wm := wireMessage{Role: "assistant", Content: str(m.Text)}
			for _, c := range m.ToolCalls {
				tc := wireToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(c.Input)
				if tc.Function.Arguments == "" {
					tc.Function.Arguments = "{}"
				}
				wm.ToolCalls = append(wm.ToolCalls, tc)
			}
			w.Messages = append(w.Messages, wm)
			continue
		}
		for _, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "ERRORE: " + content
			}
			w.Messages = append(w.Messages, wireMessage{Role: "tool", ToolCallID: r.CallID, Content: str(content)})
		}
		if m.Text != "" {
			w.Messages = append(w.Messages, wireMessage{Role: "user", Content: str(m.Text)})
		}
	}
	for _, t := range req.Tools {
		wt := wireTool{Type: "function"}
		wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Schema
		w.Tools = append(w.Tools, wt)
	}
	return w
}

// send fa la richiesta e restituisce la risposta riuscita (il chiamante chiude il corpo).
func (p *provider) send(ctx context.Context, method, path string, body any, accept string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return nil, &ai.ProviderError{Code: "config", Msg: "indirizzo non valido"}
	}
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, &ai.ProviderError{Code: "network", Msg: "servizio non raggiungibile (" + p.host() + ")"}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, statusError(resp.StatusCode, data)
	}
	return resp, nil
}

func (p *provider) do(ctx context.Context, method, path string, body any, out any) error {
	resp, err := p.send(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &ai.ProviderError{Code: "bad_response", Msg: "risposta non riconosciuta: il servizio è compatibile OpenAI?"}
	}
	return nil
}

func (p *provider) host() string {
	if u, err := url.Parse(p.base); err == nil {
		return u.Host
	}
	return ""
}

// statusError: il corpo dell'errore serve solo a riconoscere il caso «niente strumenti».
func statusError(status int, body []byte) error {
	low := strings.ToLower(string(body))
	pe := &ai.ProviderError{Status: status}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		pe.Code, pe.Msg = "auth", "chiave non valida o non autorizzata"
	case status == http.StatusNotFound && strings.Contains(low, "model"):
		pe.Code, pe.Msg = "model_not_found", "modello inesistente o non installato"
	case status == http.StatusNotFound:
		pe.Code, pe.Msg = "not_found", "indirizzo non trovato: controlla il percorso (es. /v1)"
	case status == http.StatusTooManyRequests:
		pe.Code, pe.Msg = "rate_limit", "troppe richieste: riprova tra poco"
	case status == http.StatusBadRequest && strings.Contains(low, "tool"):
		pe.Code, pe.Msg = "tools_unsupported", "il modello non sa chiamare strumenti"
	case status == http.StatusBadRequest:
		pe.Code, pe.Msg = "bad_request", "richiesta rifiutata dal servizio"
	case status >= 500:
		pe.Code, pe.Msg = "provider_down", "servizio non disponibile"
	default:
		pe.Code, pe.Msg = "error", "risposta inattesa del servizio"
	}
	return pe
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	var w wireResponse
	if err := p.do(ctx, http.MethodPost, "/chat/completions", toWire(req), &w); err != nil {
		return nil, err
	}
	return fromWire(req, w)
}

// fromWire traduce la risposta (intera o ricomposta dai pezzi dello streaming).
func fromWire(req ai.Request, w wireResponse) (*ai.Response, error) {
	if len(w.Choices) == 0 {
		return nil, &ai.ProviderError{Code: "bad_response", Msg: "risposta senza contenuto"}
	}
	ch := w.Choices[0]
	out := &ai.Response{Model: w.Model, InputTokens: w.Usage.PromptTokens, OutputTokens: w.Usage.CompletionTokens}
	if out.Model == "" {
		out.Model = req.Model
	}
	out.Message = ai.Message{Role: ai.RoleAssistant}
	if ch.Message.Content != nil {
		out.Message.Text = *ch.Message.Content
	}
	for i, c := range ch.Message.ToolCalls {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		args := strings.TrimSpace(c.Function.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, ai.ToolCall{ID: id, Name: c.Function.Name, Input: json.RawMessage(args)})
	}
	switch {
	case len(out.Message.ToolCalls) > 0:
		out.Stop = ai.StopToolUse
	case ch.FinishReason == "length":
		out.Stop = ai.StopMaxTokens
	case ch.FinishReason == "content_filter":
		out.Stop = ai.StopRefusal
	default:
		out.Stop = ai.StopEnd
	}
	return out, nil
}

// streamChunk è un pezzo della risposta in streaming (chat.completion.chunk).
type streamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// ChatStream chiede la risposta in streaming (Chat Completions con «stream: true»: eventi SSE
// «data: {chunk}» fino a «data: [DONE]») e passa a onText i pezzi di testo man mano. Le chiamate
// di strumenti arrivano a pezzi per indice e si ricompongono; l'uso dei token arriva nell'ultimo
// pezzo («stream_options.include_usage»). Un servizio che ignora lo streaming e risponde in JSON
// si legge come Chat.
func (p *provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	body := toWire(req)
	body.Stream, body.StreamOptions = true, &streamOptions{IncludeUsage: true}
	resp, err := p.send(ctx, http.MethodPost, "/chat/completions", body, "text/event-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		var w wireResponse
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, &ai.ProviderError{Code: "bad_response", Msg: "risposta non riconosciuta: il servizio è compatibile OpenAI?"}
		}
		out, err := fromWire(req, w)
		if err == nil && onText != nil && out.Message.Text != "" {
			onText(out.Message.Text)
		}
		return out, err
	}
	var (
		w      wireResponse
		text   strings.Builder
		calls  []wireToolCall
		finish string
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	done := false
	for !done && sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // commenti («: ping»), righe vuote fra gli eventi, «event:»
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return nil, &ai.ProviderError{Code: "bad_response", Msg: "pezzo dello streaming non riconosciuto"}
		}
		if ch.Model != "" {
			w.Model = ch.Model
		}
		if ch.Usage != nil {
			w.Usage.PromptTokens, w.Usage.CompletionTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != nil && *c.Delta.Content != "" {
				text.WriteString(*c.Delta.Content)
				if onText != nil {
					onText(*c.Delta.Content)
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, wireToolCall{Type: "function"})
				}
				if tc.ID != "" {
					calls[tc.Index].ID = tc.ID
				}
				calls[tc.Index].Function.Name += tc.Function.Name
				calls[tc.Index].Function.Arguments += tc.Function.Arguments
			}
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	if !done && finish == "" {
		return nil, &ai.ProviderError{Code: "network", Msg: "streaming interrotto prima della fine"}
	}
	content := text.String()
	w.Choices = append(w.Choices, struct {
		Message      wireMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	}{Message: wireMessage{Role: "assistant", Content: &content, ToolCalls: calls}, FinishReason: finish})
	return fromWire(req, w)
}

// Models elenca i modelli del servizio (con Ollama e LM Studio: quelli installati).
func (p *provider) Models(ctx context.Context) ([]ai.ModelInfo, error) {
	var w struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.do(ctx, http.MethodGet, "/models", nil, &w); err != nil {
		return nil, err
	}
	out := make([]ai.ModelInfo, 0, len(w.Data))
	for _, m := range w.Data {
		if m.ID != "" {
			out = append(out, ai.ModelInfo{ID: m.ID})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
