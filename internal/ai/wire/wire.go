// Package wire raccoglie ciò che serve agli adattatori che parlano HTTP con il fornitore
// (OpenAI, Gemini, Ollama): richieste JSON, lettura degli eventi SSE e delle righe NDJSON,
// traduzione degli stati HTTP in ai.ProviderError senza riportare la risposta grezza (niente
// segreti nei log). Solo libreria standard.
package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"regia/internal/ai"
)

// Client fa le richieste a un fornitore. Header aggiunge l'autenticazione (chiave).
type Client struct {
	Base   string
	HC     *http.Client
	Header func(*http.Request)
	// Who nomina il servizio nei messaggi d'errore («OpenAI», «Gemini», «Ollama»).
	Who string
}

// Send fa la richiesta e restituisce la risposta riuscita (il chiamante chiude il corpo).
// Uno stato d'errore diventa un ai.ProviderError con StatusError.
func (c *Client) Send(ctx context.Context, method, path string, body any, accept string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, &ai.ProviderError{Code: "config", Msg: "indirizzo non valido"}
	}
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Header != nil {
		c.Header(req)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, &ai.ProviderError{Code: "network", Msg: c.Who + " non raggiungibile (" + c.host() + ")"}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, StatusError(c.Who, resp.StatusCode, data)
	}
	return resp, nil
}

// JSON fa la richiesta e legge la risposta JSON in out.
func (c *Client) JSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.Send(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return BadResponse(c.Who)
	}
	return nil
}

func (c *Client) host() string {
	if u, err := url.Parse(c.Base); err == nil {
		return u.Host
	}
	return ""
}

// BadResponse è l'errore di una risposta che non si riconosce.
func BadResponse(who string) error {
	return &ai.ProviderError{Code: "bad_response", Msg: "risposta di " + who + " non riconosciuta"}
}

// StatusError traduce uno stato HTTP d'errore. Del corpo si guardano solo poche parole per
// riconoscere i casi utili (modello senza strumenti, quota esaurita, modello inesistente).
func StatusError(who string, status int, body []byte) error {
	low := strings.ToLower(string(body))
	pe := &ai.ProviderError{Status: status}
	switch {
	case status == http.StatusUnauthorized:
		pe.Code, pe.Msg = "auth", "chiave di "+who+" non valida"
	case status == http.StatusForbidden && strings.Contains(low, "region"):
		pe.Code, pe.Msg = "forbidden", who+" non è disponibile da questa regione o per questo account"
	case status == http.StatusForbidden:
		pe.Code, pe.Msg = "auth", "chiave di "+who+" non autorizzata"
	case status == http.StatusNotFound && strings.Contains(low, "model"):
		pe.Code, pe.Msg = "model_not_found", "modello inesistente o non installato"
	case status == http.StatusNotFound:
		pe.Code, pe.Msg = "not_found", "indirizzo non trovato: controlla l'indirizzo base"
	case status == http.StatusTooManyRequests && (strings.Contains(low, "quota") || strings.Contains(low, "billing")):
		pe.Code, pe.Msg = "quota", "quota o credito di "+who+" esauriti"
	case status == http.StatusTooManyRequests:
		pe.Code, pe.Msg = "rate_limit", "troppe richieste: riprova tra poco"
	case status == http.StatusBadRequest && strings.Contains(low, "tool"):
		pe.Code, pe.Msg = "tools_unsupported", "il modello non sa chiamare strumenti"
	case (status == http.StatusBadRequest || status == http.StatusNotFound) && strings.Contains(low, "api key"):
		pe.Code, pe.Msg = "auth", "chiave di "+who+" non valida"
	case status == http.StatusBadRequest:
		pe.Code, pe.Msg = "bad_request", "richiesta rifiutata da "+who
	case status >= 500:
		pe.Code, pe.Msg = "provider_down", who+" non disponibile"
	default:
		pe.Code, pe.Msg = "error", "risposta inattesa di "+who
	}
	return pe
}

// SSE legge gli eventi Server-Sent Events: per ogni evento chiama fn con il nome (vuoto se
// manca la riga «event:») e i dati (le righe «data:» unite). fn può fermare la lettura
// restituendo ErrStop. Commenti e campi sconosciuti si ignorano.
func SSE(ctx context.Context, r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return stopped(err)
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	return stopped(flush())
}

// NDJSON legge una riga JSON alla volta (streaming di Ollama).
func NDJSON(ctx context.Context, r io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return stopped(err)
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ai.ProviderError{Code: "network", Msg: "risposta interrotta"}
	}
	return nil
}

// ErrStop ferma la lettura di SSE o NDJSON senza errore.
var ErrStop = errors.New("stop")

func stopped(err error) error {
	if errors.Is(err, ErrStop) {
		return nil
	}
	return err
}

// Interrupted è l'errore di uno streaming finito prima dell'evento conclusivo.
func Interrupted() error {
	return &ai.ProviderError{Code: "network", Msg: "streaming interrotto prima della fine"}
}
