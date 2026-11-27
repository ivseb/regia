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

// params traduce la richiesta neutra in quella dell'SDK.
//
// Cache del prompt: il marcatore cache_control in cima alla richiesta mette in cache tutto
// fino all'ultimo blocco (strumenti, istruzioni, messaggi). Nel giro con gli strumenti ogni
// chiamata rilegge dalla cache il prefisso della precedente; istruzioni e strumenti restano
// identici fra le domande, così anche la conversazione successiva parte dalla cache.
func params(req ai.Request) sdk.MessageNewParams {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := sdk.MessageNewParams{
		Model:        req.Model,
		MaxTokens:    maxTokens,
		Messages:     toParams(req.Messages),
		CacheControl: sdk.NewCacheControlEphemeralParam(),
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
	return params
}

// CountTokens conta i token dell'input con l'API ufficiale (Messages.CountTokens): stessi
// modello, istruzioni, strumenti e messaggi della chiamata, senza generare né consumare.
func (p *provider) CountTokens(ctx context.Context, req ai.Request) (int64, error) {
	full := params(req)
	cp := sdk.MessageCountTokensParams{Model: full.Model, Messages: full.Messages, OutputConfig: full.OutputConfig}
	if len(full.System) > 0 {
		cp.System = sdk.MessageCountTokensParamsSystemUnion{OfTextBlockArray: full.System}
	}
	for _, t := range full.Tools {
		cp.Tools = append(cp.Tools, sdk.MessageCountTokensToolUnionParam{OfTool: t.OfTool})
	}
	res, err := p.client.Messages.CountTokens(ctx, cp)
	if err != nil {
		return 0, mapError(err)
	}
	return res.InputTokens, nil
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	resp, err := p.client.Messages.New(ctx, params(req))
	if err != nil {
		return nil, mapError(err)
	}
	return convert(resp), nil
}

// ChatStream chiede la risposta in streaming e passa a onText i pezzi di testo man mano;
// alla fine il messaggio accumulato è lo stesso di Chat (blocchi nativi compresi).
func (p *provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	stream := p.client.Messages.NewStreaming(ctx, params(req))
	defer stream.Close()
	msg := sdk.Message{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, &ai.ProviderError{Code: "bad_response", Msg: "risposta del fornitore non riconosciuta"}
		}
		if d, ok := ev.AsAny().(sdk.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(sdk.TextDelta); ok && t.Text != "" && onText != nil {
				onText(t.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, mapError(err)
	}
	return convert(&msg), nil
}

func convert(resp *sdk.Message) *ai.Response {
	u := resp.Usage
	out := &ai.Response{Model: resp.Model, InputTokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens}
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
	return out
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
