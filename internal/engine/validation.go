package engine

import "fmt"

// Errori di validazione rivolti alla persona, con un code specifico e i parametri del messaggio: il
// client li traduce (errors.<code>, con i `params`), mentre il detail resta in italiano per chi legge
// l'API. Ogni helper prende il testo italiano del detail, come Invalid. I code stanno in
// ValidationCodes e hanno una voce in web/src/i18n/{it,en}/errors.ts.

// InvalidParams è InvalidCode con i parametri del messaggio.
func InvalidParams(code, field string, params map[string]any, format string, args ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, args...), Code: code, Params: params}
}

// Required: un valore obbligatorio manca (validation.required).
func Required(field, format string, args ...any) error {
	return InvalidParams("validation.required", field, nil, format, args...)
}

// TooLong: il testo supera max caratteri (validation.too_long, parametro max).
func TooLong(field string, max int, format string, args ...any) error {
	return InvalidParams("validation.too_long", field, map[string]any{"max": max}, format, args...)
}

// OutOfRange: il valore non sta tra min e max (validation.out_of_range, parametri min e max).
func OutOfRange(field string, min, max int, format string, args ...any) error {
	return InvalidParams("validation.out_of_range", field, map[string]any{"min": min, "max": max}, format, args...)
}

// DateInvalid: la data non è nel formato AAAA-MM-GG (validation.date_invalid).
func DateInvalid(field, format string, args ...any) error {
	return InvalidParams("validation.date_invalid", field, nil, format, args...)
}

// NotFound: il riferimento indicato non esiste (validation.not_found). what è il genere
// dell'oggetto (member, group, unit, application, element, role, team, type, control, page, space…):
// il client lo traduce con errors.kind.<what>; vuoto = «l'elemento indicato».
func NotFound(field, what, format string, args ...any) error {
	var params map[string]any
	if what != "" {
		params = map[string]any{"what": what}
	}
	return InvalidParams("validation.not_found", field, params, format, args...)
}

// Duplicate: esiste già un oggetto con lo stesso nome, codice o chiave (validation.duplicate).
func Duplicate(field, format string, args ...any) error {
	return InvalidParams("validation.duplicate", field, nil, format, args...)
}

// InvalidValue: il valore non è ammesso o non ha il formato giusto (validation.invalid_value).
func InvalidValue(field, format string, args ...any) error {
	return InvalidParams("validation.invalid_value", field, nil, format, args...)
}
