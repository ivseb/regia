// Package ai è il livello AI di Regia (replica/ai.md): impostazioni del tenant, interfaccia
// unica del modello con i suoi adattatori, registro delle chiamate con il budget, mascheramento
// dei dati personali e assistente con gli strumenti della persona (internal/ai/tools).
//
// Principi (replica/ai.md §1): spenta di default; propone e la persona conferma; stessi
// permessi della persona; ogni chiamata registrata in ai_calls; il contenuto degli elementi
// è un dato e non un'istruzione; senza AI tutto funziona.
//
// Adattatori: internal/ai/anthropic (SDK ufficiale) e internal/ai/openaicompat (protocollo
// compatibile OpenAI: OpenAI, Mistral, vLLM, LM Studio, Ollama...). Il fornitore finto dei test
// (internal/devtools/fakeai) si registra solo in sviluppo e nei test (Options.Kinds).
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Ruoli dei messaggi verso il modello.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message è un messaggio della conversazione con il modello, nella forma neutra comune a
// tutti gli adattatori. Un messaggio «user» porta testo oppure i risultati degli strumenti;
// uno «assistant» porta testo e chiamate di strumenti.
type Message struct {
	Role        string       `json:"role"`
	Text        string       `json:"text,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
	// Native è la forma propria del fornitore della risposta, da rimandare così com'è (per
	// Anthropic i blocchi di ragionamento vanno restituiti invariati). Solo in memoria.
	Native any `json:"-"`
}

// ToolCall è una chiamata di strumento chiesta dal modello.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult è il risultato di uno strumento, da restituire al modello.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ToolSpec descrive uno strumento al modello: nome, descrizione e schema JSON dell'input.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Request è una richiesta al modello.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
	// Deep chiede più ragionamento (livello «approfondito»); l'adattatore decide come.
	Deep bool
}

// Stop: perché il modello si è fermato.
const (
	StopEnd       = "end"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
)

// Response è la risposta del modello.
type Response struct {
	Message      Message
	Stop         string
	InputTokens  int64
	OutputTokens int64
	// Model è il modello che ha risposto (può differire da quello chiesto, es. un alias).
	Model string
}

// ModelInfo è un modello offerto dal fornitore (proposto da Regia o installato).
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Tier: livello per cui Regia lo propone («rapido», «approfondito»), se lo conosce.
	Tier string `json:"tier,omitempty"`
}

// Provider è l'interfaccia unica del modello: ogni adattatore la implementa.
type Provider interface {
	// Chat fa una chiamata al modello.
	Chat(ctx context.Context, req Request) (*Response, error)
	// Models elenca i modelli: quelli installati (Ollama, LM Studio) o quelli noti.
	Models(ctx context.Context) ([]ModelInfo, error)
}

// ProviderConfig è ciò che serve a un adattatore per collegarsi.
type ProviderConfig struct {
	BaseURL string
	APIKey  string
	// Local: indirizzo su questa macchina o sulla rete interna (niente mascheramento).
	Local bool
	// HTTPClient è il client con la politica di rete dei connettori (lo prepara ai.Service).
	HTTPClient *http.Client
}

// Factory crea un adattatore. Kind è il tipo di fornitore («anthropic», «openai_compat»...).
type Factory func(cfg ProviderConfig) (Provider, error)

// Kind descrive un tipo di fornitore registrato.
type Kind struct {
	Key string
	New Factory
	// NeedsKey: la chiave è obbligatoria (Anthropic); per un servizio compatibile è facoltativa.
	NeedsKey bool
	// NeedsURL: serve l'indirizzo base (servizio compatibile OpenAI).
	NeedsURL bool
	// External: i dati escono dall'organizzazione (mascheramento), salvo indirizzi locali.
	External bool
	// Tools: il fornitore sa chiamare strumenti con ogni modello (Anthropic). Falso = dipende dal modello.
	Tools bool
	// Proposed: modelli proposti per i livelli, se Regia li conosce.
	Proposed []ModelInfo
}

// ProviderError è un errore del fornitore da mostrare all'amministratore (senza segreti).
type ProviderError struct {
	Status int    // stato HTTP del fornitore, 0 se di rete
	Code   string // codice breve per il registro (auth, rate_limit, tools_unsupported, network...)
	Msg    string
}

func (e *ProviderError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("fornitore del modello: %s (HTTP %d)", e.Msg, e.Status)
	}
	return "fornitore del modello: " + e.Msg
}

// ErrorCode è il codice breve dell'errore per il registro delle chiamate.
func ErrorCode(err error) string {
	var pe *ProviderError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &pe):
		return pe.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "error"
}
