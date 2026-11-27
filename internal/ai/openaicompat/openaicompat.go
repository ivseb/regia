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
	return ai.Kind{Key: Kind, New: New, NeedsURL: true, External: true}
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

func (p *provider) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return &ai.ProviderError{Code: "config", Msg: "indirizzo non valido"}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return err
		}
		return &ai.ProviderError{Code: "network", Msg: "servizio non raggiungibile (" + p.host() + ")"}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	if resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, data)
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
