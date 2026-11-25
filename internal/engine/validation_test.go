package engine

import (
	"errors"
	"testing"
)

// Gli helper di validazione danno un code specifico e i parametri del messaggio, e il detail resta italiano.
func TestValidationHelpers(t *testing.T) {
	cases := []struct {
		err    error
		code   string
		params map[string]any
	}{
		{Required("name", "serve un nome"), "validation.required", nil},
		{TooLong("title", 500, "titolo troppo lungo (massimo %d caratteri)", 500), "validation.too_long", map[string]any{"max": 500}},
		{OutOfRange("pct", 0, 100, "tra 0 e 100"), "validation.out_of_range", map[string]any{"min": 0, "max": 100}},
		{DateInvalid("from", "data non valida"), "validation.date_invalid", nil},
		{NotFound("member_id", "member", "persona inesistente"), "validation.not_found", map[string]any{"what": "member"}},
		{NotFound("x", "", "oggetto inesistente"), "validation.not_found", nil},
		{Duplicate("key", "chiave già usata"), "validation.duplicate", nil},
		{InvalidValue("icon", "icona non valida"), "validation.invalid_value", nil},
	}
	known := map[string]bool{}
	for _, c := range ValidationCodes {
		known[c] = true
	}
	for _, c := range cases {
		var ve *ValidationError
		if !errors.As(c.err, &ve) || ve.Code != c.code {
			t.Fatalf("%v: code %q, atteso %q", c.err, ve.Code, c.code)
		}
		if !known[ve.Code] {
			t.Errorf("%s non è in ValidationCodes", ve.Code)
		}
		if len(ve.Params) != len(c.params) {
			t.Errorf("%s: parametri %v, attesi %v", c.code, ve.Params, c.params)
		}
		for k, v := range c.params {
			if ve.Params[k] != v {
				t.Errorf("%s: parametro %s = %v, atteso %v", c.code, k, ve.Params[k], v)
			}
		}
	}
}
