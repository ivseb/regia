// Package gemini è l'adattatore nativo di Google Gemini (replica/ai.md §3) sull'API
// generateContent: chiamata di strumenti (functionDeclarations con lo schema JSON), livello di
// ragionamento per livello di Regia (thinkingConfig.thinkingLevel), firme del ragionamento
// rimandate invariate nel passo successivo (thoughtSignature, obbligatorie con gli strumenti),
// cache implicita del prompt (token in cache nel registro e nel costo), conteggio dei token
// (countTokens) e streaming SSE (streamGenerateContent?alt=sse).
//
// Usa l'API HTTP documentata e non l'SDK ufficiale google.golang.org/genai: l'SDK aggiunge
// circa 22 MB al binario (misurati il 2026-11-27: porta con sé le librerie di autenticazione di
// Google e gRPC). I nomi dei campi sono quelli dell'SDK ufficiale v1.79.1, versione dell'API
// «v1» come l'SDK per l'API Gemini.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"regia/internal/ai"
	"regia/internal/ai/wire"
)

// Kind è il tipo di fornitore.
const Kind = "gemini"

// DefaultBaseURL è l'API Gemini, versione v1.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1"

// Modelli proposti per i livelli (elenco ufficiale dei modelli verificato il 2026-11-27).
const (
	ModelFast = "gemini-4-hydrogen"
	ModelDeep = "gemini-4-carbon"
)

// Register descrive il tipo di fornitore per il registro di ai.Service.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, NeedsKey: true, External: true, Tools: true,
		Proposed: []ai.ModelInfo{
			{ID: ModelFast, Name: "Gemini 4 Hydrogen", Tier: ai.TierFast},
			{ID: ModelDeep, Name: "Gemini 4 Carbon", Tier: ai.TierDeep},
		}}
}

type provider struct{ c wire.Client }

// New crea l'adattatore con la chiave del tenant (intestazione x-goog-api-key, mai nell'URL).
func New(cfg ai.ProviderConfig) (ai.Provider, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, &ai.ProviderError{Code: "auth", Msg: "manca la chiave di Gemini"}
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return &provider{c: wire.Client{Base: base, HC: hc, Who: "Gemini",
		Header: func(r *http.Request) { r.Header.Set("x-goog-api-key", key) }}}, nil
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type declaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"parametersJsonSchema,omitempty"`
}

type tools struct {
	FunctionDeclarations []declaration `json:"functionDeclarations"`
}

type thinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int             `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type request struct {
	Contents          []json.RawMessage `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Tools             []tools           `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

// syntheticID: identificativo dato da Regia a una chiamata senza id; non si rimanda a Gemini.
const syntheticID = "regia_"

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// thinking: i modelli Gemini 3 e successivi regolano il ragionamento con thinkingLevel.
func thinking(model string) bool {
	m := strings.TrimPrefix(model, "models/")
	return strings.HasPrefix(m, "gemini-3") || strings.HasPrefix(m, "gemini-4")
}

func build(req ai.Request) request {
	w := request{Contents: contents(req.Messages)}
	if req.System != "" {
		w.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		t := tools{}
		for _, s := range req.Tools {
			t.FunctionDeclarations = append(t.FunctionDeclarations, declaration{Name: s.Name, Description: s.Description, Schema: s.Schema})
		}
		w.Tools = []tools{t}
	}
	gc := &generationConfig{MaxOutputTokens: req.MaxTokens}
	if thinking(req.Model) {
		// thinkingLevel: Gemini 4 accetta LOW, MEDIUM e HIGH; Gemini 3 Pro solo LOW e HIGH,
		// quindi lì il medio diventa alto.
		level := strings.ToUpper(ai.EffortOf(req))
		if level == "MEDIUM" && strings.HasPrefix(strings.TrimPrefix(req.Model, "models/"), "gemini-3-pro") {
			level = "HIGH"
		}
		gc.ThinkingConfig = &thinkingConfig{ThinkingLevel: level}
	}
	if gc.MaxOutputTokens > 0 || gc.ThinkingConfig != nil {
		w.GenerationConfig = gc
	}
	return w
}

// contents traduce i messaggi neutri. Le risposte del modello nella stessa richiesta tornano
// come il contenuto ricevuto, con le firme del ragionamento; i risultati degli strumenti
// portano il nome della funzione, preso dalla chiamata a cui rispondono.
func contents(msgs []ai.Message) []json.RawMessage {
	names := map[string]string{}
	out := []json.RawMessage{}
	for _, m := range msgs {
		if m.Role == ai.RoleAssistant {
			for _, c := range m.ToolCalls {
				names[c.ID] = c.Name
			}
			if n, ok := m.Native.(json.RawMessage); ok && len(n) > 0 {
				// Una risposta troncata o bloccata può arrivare senza parti: Gemini rifiuta un
				// contenuto vuoto nella richiesta successiva, quindi si salta.
				var c content
				if json.Unmarshal(n, &c) == nil && len(c.Parts) == 0 {
					continue
				}
				out = append(out, n)
				continue
			}
			ct := content{Role: "model"}
			if m.Text != "" {
				ct.Parts = append(ct.Parts, part{Text: m.Text})
			}
			for _, c := range m.ToolCalls {
				fc := &functionCall{Name: c.Name, Args: c.Input}
				if !strings.HasPrefix(c.ID, syntheticID) {
					fc.ID = c.ID
				}
				ct.Parts = append(ct.Parts, part{FunctionCall: fc})
			}
			if len(ct.Parts) > 0 {
				out = append(out, raw(ct))
			}
			continue
		}
		ct := content{Role: "user"}
		for _, r := range m.ToolResults {
			fr := &functionResponse{Name: names[r.CallID], Response: map[string]any{"output": r.Content}}
			if r.IsError {
				fr.Response = map[string]any{"error": r.Content}
			}
			if !strings.HasPrefix(r.CallID, syntheticID) {
				fr.ID = r.CallID
			}
			ct.Parts = append(ct.Parts, part{FunctionResponse: fr})
		}
		if m.Text != "" {
			ct.Parts = append(ct.Parts, part{Text: m.Text})
		}
		if len(ct.Parts) > 0 {
			out = append(out, raw(ct))
		}
	}
	return out
}

type usage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	ToolUsePromptTokenCount int64 `json:"toolUsePromptTokenCount"`
}

type response struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *usage `json:"usageMetadata"`
	ModelVersion  string `json:"modelVersion"`
}

// refusal: motivi di fine che vengono dai filtri di sicurezza o di contenuto.
func refusal(reason string) bool {
	switch reason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "MODEL_ARMOR":
		return true
	}
	return false
}

func convert(req ai.Request, ct content, finish, block string, u *usage, model string) *ai.Response {
	out := &ai.Response{Model: model}
	if out.Model == "" {
		out.Model = req.Model
	}
	if u != nil {
		out.InputTokens = u.PromptTokenCount + u.ToolUsePromptTokenCount
		out.OutputTokens = u.CandidatesTokenCount + u.ThoughtsTokenCount
		out.CacheReadTokens = u.CachedContentTokenCount
	}
	if ct.Role == "" {
		ct.Role = "model"
	}
	if ct.Parts == nil {
		ct.Parts = []part{}
	}
	out.Message = ai.Message{Role: ai.RoleAssistant, Native: raw(ct)}
	var text []string
	for _, p := range ct.Parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("%s%d", syntheticID, len(out.Message.ToolCalls)+1)
			}
			args := p.FunctionCall.Args
			if len(args) == 0 || !json.Valid(args) || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			out.Message.ToolCalls = append(out.Message.ToolCalls, ai.ToolCall{ID: id, Name: p.FunctionCall.Name, Input: args})
		case p.Text != "" && !p.Thought:
			text = append(text, p.Text)
		}
	}
	out.Message.Text = strings.Join(text, "")
	switch {
	case block != "" || refusal(finish):
		out.Stop = ai.StopRefusal
	case len(out.Message.ToolCalls) > 0:
		out.Stop = ai.StopToolUse
	case finish == "MAX_TOKENS":
		out.Stop = ai.StopMaxTokens
	default:
		out.Stop = ai.StopEnd
	}
	return out
}

func modelPath(model string) (string, error) {
	m := strings.TrimPrefix(model, "models/")
	if m == "" || strings.ContainsAny(m, "/?#:% ") {
		return "", &ai.ProviderError{Code: "model_not_found", Msg: "nome del modello non valido"}
	}
	return "/models/" + url.PathEscape(m), nil
}

func (r response) first() (content, string, string) {
	block := ""
	if r.PromptFeedback != nil {
		block = r.PromptFeedback.BlockReason
	}
	if len(r.Candidates) == 0 {
		return content{}, "", block
	}
	return r.Candidates[0].Content, r.Candidates[0].FinishReason, block
}

func (p *provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	path, err := modelPath(req.Model)
	if err != nil {
		return nil, err
	}
	var r response
	if err := p.c.JSON(ctx, http.MethodPost, path+":generateContent", build(req), &r); err != nil {
		return nil, err
	}
	ct, finish, block := r.first()
	if len(r.Candidates) == 0 && block == "" {
		return nil, &ai.ProviderError{Code: "bad_response", Msg: "risposta di Gemini senza contenuto"}
	}
	return convert(req, ct, finish, block, r.UsageMetadata, r.ModelVersion), nil
}

// ChatStream chiede la risposta in streaming (streamGenerateContent?alt=sse): ogni evento è
// una risposta parziale; i pezzi di testo si uniscono, le chiamate di strumenti e le firme del
// ragionamento si tengono come arrivano, l'uso dei token è nell'ultimo evento.
func (p *provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	path, err := modelPath(req.Model)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.Send(ctx, http.MethodPost, path+":streamGenerateContent?alt=sse", build(req), "text/event-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var (
		acc                  content
		finish, block, model string
		u                    *usage
		got                  bool
	)
	err = wire.SSE(ctx, resp.Body, func(_, data string) error {
		var r response
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return wire.BadResponse("Gemini")
		}
		got = true
		if r.UsageMetadata != nil {
			u = r.UsageMetadata
		}
		if r.ModelVersion != "" {
			model = r.ModelVersion
		}
		ct, f, b := r.first()
		if f != "" {
			finish = f
		}
		if b != "" {
			block = b
		}
		for _, pt := range ct.Parts {
			if pt.Text != "" && !pt.Thought && onText != nil {
				onText(pt.Text)
			}
			acc.Parts = merge(acc.Parts, pt)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !got || (finish == "" && block == "") {
		return nil, wire.Interrupted()
	}
	acc.Role = "model"
	return convert(req, acc, finish, block, u, model), nil
}

// merge aggiunge un pezzo dello streaming: il testo continua la parte di testo precedente
// dello stesso tipo (risposta o ragionamento); la firma, se arriva, resta sulla parte.
func merge(parts []part, pt part) []part {
	if pt.FunctionCall == nil && pt.FunctionResponse == nil && len(parts) > 0 {
		last := &parts[len(parts)-1]
		if last.FunctionCall == nil && last.Thought == pt.Thought && last.ThoughtSignature == "" {
			last.Text += pt.Text
			last.ThoughtSignature = pt.ThoughtSignature
			return parts
		}
	}
	if pt.FunctionCall == nil && pt.Text == "" && pt.ThoughtSignature == "" {
		return parts
	}
	return append(parts, pt)
}

// CountTokens conta i token dei messaggi con countTokens; istruzioni e strumenti, che l'API
// Gemini non conta su questo endpoint, si aggiungono con la stima prudente.
func (p *provider) CountTokens(ctx context.Context, req ai.Request) (int64, error) {
	path, err := modelPath(req.Model)
	if err != nil {
		return 0, err
	}
	var r struct {
		TotalTokens int64 `json:"totalTokens"`
	}
	if err := p.c.JSON(ctx, http.MethodPost, path+":countTokens", map[string]any{"contents": contents(req.Messages)}, &r); err != nil {
		return 0, err
	}
	extra := int64(0)
	if req.System != "" || len(req.Tools) > 0 {
		extra = ai.EstimateTokens(ai.Request{System: req.System, Tools: req.Tools})
	}
	return r.TotalTokens + extra, nil
}

// Models elenca i modelli che generano testo (verifica la chiave) e segna quelli proposti.
func (p *provider) Models(ctx context.Context) ([]ai.ModelInfo, error) {
	var out []ai.ModelInfo
	token := ""
	for range 5 {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var w struct {
			Models []struct {
				Name                       string   `json:"name"`
				DisplayName                string   `json:"displayName"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := p.c.JSON(ctx, http.MethodGet, "/models?"+q.Encode(), nil, &w); err != nil {
			return nil, err
		}
		for _, m := range w.Models {
			ok := false
			for _, g := range m.SupportedGenerationMethods {
				ok = ok || g == "generateContent"
			}
			id := strings.TrimPrefix(m.Name, "models/")
			if !ok || id == "" {
				continue
			}
			info := ai.ModelInfo{ID: id, Name: m.DisplayName}
			switch id {
			case ModelFast:
				info.Tier = ai.TierFast
			case ModelDeep:
				info.Tier = ai.TierDeep
			}
			out = append(out, info)
		}
		if token = w.NextPageToken; token == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
