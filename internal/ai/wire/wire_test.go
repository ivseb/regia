package wire

import (
	"net/http"
	"strings"
	"testing"
)

// Il motivo del fornitore resta nell'errore di una richiesta rifiutata, breve e senza chiavi.
func TestDetail(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"code":400,"message":"Invalid value at 'generation_config.thinking_config.thinking_level'","status":"INVALID_ARGUMENT"}}`: "Invalid value at 'generation_config.thinking_config.thinking_level'",
		`{"error":{"message":"bad\n  request"}}`: "bad request",
		`{"error":"model is required"}`:          "model is required",
		`{"message":"nope"}`:                     "nope",
		`non json`:                               "",
		`{"error":{"message":"key AIzaSyA1234567890abcdefghijklmnop is wrong"}}`: "key … is wrong",
	} {
		if got := Detail([]byte(body)); got != want {
			t.Errorf("Detail(%s) = %q, voluto %q", body, got, want)
		}
	}
	if d := Detail([]byte(`{"error":{"message":"` + strings.Repeat("a b ", 200) + `"}}`)); len([]rune(d)) > 301 {
		t.Errorf("troppo lungo: %d", len(d))
	}
	err := StatusError("Gemini", http.StatusBadRequest, []byte(`{"error":{"message":"Unknown name \"foo\""}}`))
	if !strings.Contains(err.Error(), `richiesta rifiutata da Gemini: Unknown name "foo"`) {
		t.Fatalf("errore: %v", err)
	}
}
