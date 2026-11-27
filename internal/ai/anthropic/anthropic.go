// Package anthropic è l'adattatore Anthropic dell'interfaccia unica del modello (ai.Provider),
// con l'SDK ufficiale per Go (github.com/anthropics/anthropic-sdk-go/v2).
//
// La chiave e l'indirizzo arrivano sempre dalle impostazioni del tenant: il client non legge
// le variabili d'ambiente del server (ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL), così un tenant
// non usa mai le credenziali di un altro né quelle dell'operatore.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"regia/internal/ai"
)

// Kind è il tipo di fornitore.
const Kind = "anthropic"

// DefaultBaseURL è l'API di Anthropic.
const DefaultBaseURL = "https://api.anthropic.com/"

// Modelli proposti per i livelli (replica/ai.md §4).
const (
	ModelFast = "claude-haiku-5-5"
	ModelDeep = "claude-opus-5-5"
)

// Register descrive il tipo di fornitore per il registro di ai.Service.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, NeedsKey: true, External: true, Tools: true,
		Proposed: []ai.ModelInfo{
			{ID: ModelFast, Name: "Claude Haiku 5.5", Tier: ai.TierFast},
			{ID: ModelDeep, Name: "Claude Opus 5.5", Tier: ai.TierDeep},
		}}
}

type provider struct {
	client sdk.Client
}

// New crea l'adattatore con la chiave del tenant. Senza HTTPClient usa un client con timeout.
func New(cfg ai.ProviderConfig) (ai.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, &ai.ProviderError{Code: "auth", Msg: "manca la chiave di Anthropic"}
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	c := sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(base),
		option.WithHTTPClient(hc),
		option.WithMaxRetries(2),
	)
	return &provider{client: c}, nil
}

// effortModels sono i modelli che accettano output_config.effort (Claude 5 e successivi).
func supportsEffort(model string) bool {
	for _, p := range []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable-5", "claude-mythos-5", "claude-opus-4-8", "claude-opus-4-7"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := sdk.MessageNewParams{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  toParams(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	// Ragionamento: sui modelli Claude 5 è adattivo e sempre attivo; si regola con l'effort.
	if supportsEffort(req.Model) {
		if req.Deep {
			params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffortHigh}
		} else {
			params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffortLow}
		}
	}
	for _, t := range req.Tools {
		props, required := splitSchema(t.Schema)
		tp := sdk.ToolParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			InputSchema: sdk.ToolInputSchemaParam{Properties: props, Required: required},
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &tp})
	}
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, mapError(err)
	}
	out := &ai.Response{Model: resp.Model, InputTokens: resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
		OutputTokens: resp.Usage.OutputTokens}
	out.Message = ai.Message{Role: ai.RoleAssistant, Native: resp.ToParam()}
	var text []string
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case sdk.TextBlock:
			text = append(text, b.Text)
		case sdk.ToolUseBlock:
			out.Message.ToolCalls = append(out.Message.ToolCalls, ai.ToolCall{ID: b.ID, Name: b.Name, Input: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	out.Message.Text = strings.Join(text, "\n\n")
	switch resp.StopReason {
	case sdk.StopReasonToolUse:
		out.Stop = ai.StopToolUse
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		out.Stop = ai.StopMaxTokens
	case sdk.StopReasonRefusal:
		out.Stop = ai.StopRefusal
	default:
		out.Stop = ai.StopEnd
	}
	return out, nil
}

// splitSchema separa proprietà e campi obbligatori dello schema JSON dello strumento.
func splitSchema(schema map[string]any) (any, []string) {
	props := schema["properties"]
	if props == nil {
		props = map[string]any{}
	}
	var req []string
	switch r := schema["required"].(type) {
	case []string:
		req = r
	case []any:
		for _, x := range r {
			if s, ok := x.(string); ok {
				req = append(req, s)
			}
		}
	}
	return props, req
}

// toParams traduce i messaggi neutri in quelli dell'SDK. Le risposte del modello nella stessa
// richiesta tornano nella forma nativa (con i blocchi di ragionamento invariati).
func toParams(msgs []ai.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == ai.RoleAssistant {
			if n, ok := m.Native.(sdk.MessageParam); ok {
				out = append(out, n)
				continue
			}
			var blocks []sdk.ContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Text))
			}
			for _, c := range m.ToolCalls {
				var in any = map[string]any{}
				_ = json.Unmarshal(c.Input, &in)
				blocks = append(blocks, sdk.NewToolUseBlock(c.ID, in, c.Name))
			}
			if len(blocks) > 0 {
				out = append(out, sdk.NewAssistantMessage(blocks...))
			}
			continue
		}
		var blocks []sdk.ContentBlockParamUnion
		for _, r := range m.ToolResults {
			blocks = append(blocks, sdk.NewToolResultBlock(r.CallID, r.Content, r.IsError))
		}
		if m.Text != "" {
			blocks = append(blocks, sdk.NewTextBlock(m.Text))
		}
		if len(blocks) > 0 {
			out = append(out, sdk.NewUserMessage(blocks...))
		}
	}
	return out
}

// Models verifica la chiave elencando i modelli dell'account e segna quelli proposti.
func (p *provider) Models(ctx context.Context) ([]ai.ModelInfo, error) {
	pager := p.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	var out []ai.ModelInfo
	for pager.Next() {
		m := pager.Current()
		info := ai.ModelInfo{ID: m.ID, Name: m.DisplayName}
		switch m.ID {
		case ModelFast:
			info.Tier = ai.TierFast
		case ModelDeep:
			info.Tier = ai.TierDeep
		}
		out = append(out, info)
		if len(out) >= 200 {
			break
		}
	}
	if err := pager.Err(); err != nil {
		return nil, mapError(err)
	}
	return out, nil
}

// mapError traduce gli errori dell'SDK senza riportare la risposta grezza (niente segreti).
func mapError(err error) error {
	var apierr *sdk.Error
	if errors.As(err, &apierr) {
		pe := &ai.ProviderError{Status: apierr.StatusCode}
		switch {
		case apierr.StatusCode == http.StatusUnauthorized:
			pe.Code, pe.Msg = "auth", "chiave di Anthropic non valida"
		case apierr.StatusCode == http.StatusForbidden:
			pe.Code, pe.Msg = "forbidden", "la chiave non può usare questo modello"
		case apierr.StatusCode == http.StatusNotFound:
			pe.Code, pe.Msg = "model_not_found", "modello inesistente"
		case apierr.StatusCode == http.StatusTooManyRequests:
			pe.Code, pe.Msg = "rate_limit", "troppe richieste: riprova tra poco"
		case apierr.StatusCode == http.StatusBadRequest:
			pe.Code, pe.Msg = "bad_request", "richiesta rifiutata dal fornitore"
		case apierr.StatusCode >= 500:
			pe.Code, pe.Msg = "provider_down", "servizio del fornitore non disponibile"
		default:
			pe.Code, pe.Msg = "error", "errore del fornitore"
		}
		return pe
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return &ai.ProviderError{Code: "network", Msg: "fornitore non raggiungibile"}
	}
	return &ai.ProviderError{Code: "bad_response", Msg: "risposta del fornitore non riconosciuta"}
}
