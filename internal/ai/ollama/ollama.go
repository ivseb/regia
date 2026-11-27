// Package ollama è l'adattatore nativo di Ollama (replica/ai.md §3 e §4) sulla sua API
// documentata: /api/chat con gli strumenti e lo streaming a righe JSON, /api/tags per i modelli
// installati con le loro capacità (/api/show per le versioni che non le elencano), così Regia
// segnala i modelli che non sanno usare strumenti e propone i livelli: il modello più grande
// installato per «approfondito», il più piccolo per «rapido».
//
// Non usa il client Go ufficiale (github.com/ollama/ollama/api): legge l'ambiente del server
// (OLLAMA_AUTH) e, verso ollama.com, firma le richieste con la chiave privata dell'operatore in
// ~/.ollama; un tenant deve usare solo ciò che c'è nelle sue impostazioni. I nomi dei campi
// sono quelli dei tipi del client ufficiale v0.40.18. La chiave è facoltativa (Ollama locale
// non la chiede; un Ollama dietro un proxy o il servizio in cloud la vogliono come Bearer).
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"regia/internal/ai"
	"regia/internal/ai/wire"
)

// Kind è il tipo di fornitore.
const Kind = "ollama"

// DefaultBaseURL è l'indirizzo di un Ollama sulla stessa macchina.
const DefaultBaseURL = "http://localhost:11434"

// Register descrive il tipo di fornitore. External: dipende dall'indirizzo (sulla rete interna
// i dati non escono e il mascheramento non serve); Tools: dipende dal modello.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, NeedsURL: true, External: true, DefaultURL: DefaultBaseURL}
}

type provider struct{ c wire.Client }

// New crea l'adattatore. L'indirizzo è quello del server Ollama, senza /v1.
func New(cfg ai.ProviderConfig) (ai.Provider, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	base = strings.TrimSuffix(base, "/api")
	if base == "" {
		return nil, &ai.ProviderError{Code: "config", Msg: "serve l'indirizzo di Ollama (es. " + DefaultBaseURL + ")"}
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	key := strings.TrimSpace(cfg.APIKey)
	return &provider{c: wire.Client{Base: base, HC: hc, Who: "Ollama", Header: func(r *http.Request) {
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
	}}}, nil
}

type toolCall struct {
	ID       string `json:"id,omitempty"`
	Function struct {
		Index     int             `json:"index"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type request struct {
	Model    string         `json:"model"`
	Messages []message      `json:"messages"`
	Tools    []tool         `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type chunk struct {
	Model           string  `json:"model"`
	Message         message `json:"message"`
	Done            bool    `json:"done"`
	DoneReason      string  `json:"done_reason"`
	PromptEvalCount int64   `json:"prompt_eval_count"`
	EvalCount       int64   `json:"eval_count"`
	// PromptEvalCachedCount: token dell'input ripresi dalla cache del contesto (versioni recenti).
	PromptEvalCachedCount int64  `json:"prompt_eval_cached_count"`
	Error                 string `json:"error"`
}

func build(req ai.Request, stream bool) request {
	w := request{Model: req.Model, Stream: stream}
	if req.MaxTokens > 0 {
		w.Options = map[string]any{"num_predict": req.MaxTokens}
	}
	if req.System != "" {
		w.Messages = append(w.Messages, message{Role: "system", Content: req.System})
	}
	names := map[string]string{}
	for _, m := range req.Messages {
		if m.Role == ai.RoleAssistant {
			wm := message{Role: "assistant", Content: m.Text}
			if n, ok := m.Native.(message); ok {
				wm = n
			} else {
				for i, c := range m.ToolCalls {
					tc := toolCall{ID: c.ID}
					tc.Function.Index, tc.Function.Name, tc.Function.Arguments = i, c.Name, c.Input
					if len(tc.Function.Arguments) == 0 {
						tc.Function.Arguments = json.RawMessage("{}")
					}
					wm.ToolCalls = append(wm.ToolCalls, tc)
				}
			}
			for _, c := range m.ToolCalls {
				names[c.ID] = c.Name
			}
			w.Messages = append(w.Messages, wm)
			continue
		}
		for _, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "ERRORE: " + content
			}
			w.Messages = append(w.Messages, message{Role: "tool", Content: content, ToolName: names[r.CallID], ToolCallID: r.CallID})
		}
		if m.Text != "" {
			w.Messages = append(w.Messages, message{Role: "user", Content: m.Text})
		}
	}
	for _, t := range req.Tools {
		wt := tool{Type: "function"}
		wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Schema
		w.Tools = append(w.Tools, wt)
	}
	return w
}

func convert(req ai.Request, msg message, last chunk) (*ai.Response, error) {
	if last.Error != "" {
		return nil, chatError(last.Error)
	}
	out := &ai.Response{Model: last.Model, InputTokens: last.PromptEvalCount, OutputTokens: last.EvalCount, CacheReadTokens: last.PromptEvalCachedCount}
	if out.Model == "" {
		out.Model = req.Model
	}
	msg.Role = "assistant"
	out.Message = ai.Message{Role: ai.RoleAssistant, Text: msg.Content, Native: msg}
	for i, c := range msg.ToolCalls {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
			msg.ToolCalls[i].ID = id
		}
		args := c.Function.Arguments
		if len(args) == 0 || !json.Valid(args) || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, ai.ToolCall{ID: id, Name: c.Function.Name, Input: args})
	}
	out.Message.Native = msg
	switch {
	case len(out.Message.ToolCalls) > 0:
		out.Stop = ai.StopToolUse
	case last.DoneReason == "length":
		out.Stop = ai.StopMaxTokens
	default:
		out.Stop = ai.StopEnd
	}
	return out, nil
}

// chatError traduce l'errore scritto da Ollama nella risposta.
func chatError(msg string) error {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "does not support tools"):
		return &ai.ProviderError{Code: "tools_unsupported", Msg: "il modello non sa chiamare strumenti"}
	case strings.Contains(low, "not found"):
		return &ai.ProviderError{Code: "model_not_found", Msg: "modello non installato in Ollama"}
	}
	return &ai.ProviderError{Code: "provider_error", Msg: "Ollama non ha completato la risposta"}
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	return p.ChatStream(ctx, req, nil)
}

// ChatStream legge la risposta a righe JSON: il testo arriva a pezzi, le chiamate di strumenti
// intere, i conteggi dei token nell'ultima riga («done»). Senza onText la risposta è intera.
func (p *provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	stream := onText != nil
	resp, err := p.c.Send(ctx, http.MethodPost, "/api/chat", build(req, stream), "application/x-ndjson")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var (
		msg  message
		last chunk
		done bool
	)
	err = wire.NDJSON(ctx, resp.Body, func(line []byte) error {
		var c chunk
		if err := json.Unmarshal(line, &c); err != nil {
			return wire.BadResponse("Ollama")
		}
		if c.Error != "" {
			last, done = c, true
			return wire.ErrStop
		}
		if c.Message.Content != "" {
			msg.Content += c.Message.Content
			if onText != nil {
				onText(c.Message.Content)
			}
		}
		msg.Thinking += c.Message.Thinking
		msg.ToolCalls = append(msg.ToolCalls, c.Message.ToolCalls...)
		if c.Done {
			last, done = c, true
			return wire.ErrStop
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, wire.Interrupted()
	}
	return convert(req, msg, last)
}

type tagModel struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Size         int64    `json:"size"`
	Capabilities []string `json:"capabilities"`
}

// Models elenca i modelli installati con le capacità (strumenti, ragionamento) e propone i
// livelli: fra quelli che sanno usare strumenti, il più grande per «approfondito» e il più
// piccolo per «rapido». I modelli che non conversano (solo embedding) non compaiono.
func (p *provider) Models(ctx context.Context) ([]ai.ModelInfo, error) {
	var w struct {
		Models []tagModel `json:"models"`
	}
	if err := p.c.JSON(ctx, http.MethodGet, "/api/tags", nil, &w); err != nil {
		return nil, err
	}
	out := []ai.ModelInfo{}
	for i, m := range w.Models {
		if m.Name == "" {
			continue
		}
		caps := m.Capabilities
		if caps == nil && i < 50 {
			var s struct {
				Capabilities []string `json:"capabilities"`
			}
			if err := p.c.JSON(ctx, http.MethodPost, "/api/show", map[string]string{"model": m.Name}, &s); err == nil {
				caps = s.Capabilities
			}
		}
		info := ai.ModelInfo{ID: m.Name, Size: m.Size}
		if caps != nil {
			if !slices.Contains(caps, "completion") {
				continue
			}
			tools := slices.Contains(caps, "tools")
			info.Tools = &tools
			info.Thinking = slices.Contains(caps, "thinking")
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	proposeTiers(out)
	return out, nil
}

// proposeTiers segna il modello più grande (approfondito) e il più piccolo (rapido) fra quelli
// che sanno usare strumenti o non lo dichiarano. Con un solo modello vale per tutti e due i
// livelli (replica/ai.md §4): lo si propone come approfondito, e il rapido ricade su di lui.
func proposeTiers(list []ai.ModelInfo) {
	big, small := -1, -1
	for i, m := range list {
		if m.Tools != nil && !*m.Tools {
			continue
		}
		if big < 0 || m.Size > list[big].Size {
			big = i
		}
		if small < 0 || m.Size < list[small].Size {
			small = i
		}
	}
	if big >= 0 {
		list[big].Tier = ai.TierDeep
	}
	if small >= 0 && small != big {
		list[small].Tier = ai.TierFast
	}
}
