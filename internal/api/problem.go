package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Problem è il formato di errore RFC 9457 (application/problem+json).
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Code è un identificativo stabile per il client (es. "auth.invalid_credentials").
	Code string `json:"code,omitempty"`
	// Params sono i valori variabili del messaggio (es. {"perm": "tenant.admin"}): il client
	// li inserisce nella traduzione del code. Il detail italiano resta come riserva.
	Params    map[string]any `json:"params,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	writeProblemParams(w, r, status, code, detail, nil)
}

// writeProblemParams è writeProblem con i parametri del messaggio, per la traduzione nel client.
func writeProblemParams(w http.ResponseWriter, r *http.Request, status int, code, detail string, params map[string]any) {
	p := Problem{
		Params:    params,
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestID(r.Context()),
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func writeJSONBody(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const maxJSONBody = 1 << 20 // 1 MiB: i file passano dagli allegati, non dal JSON

// decodeJSON legge un corpo JSON rifiutando campi sconosciuti e corpi troppo grandi.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "corpo della richiesta troppo grande")
		} else {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", fmt.Sprintf("JSON non valido: %v", err))
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", "un solo oggetto JSON per richiesta")
		return false
	}
	return true
}
