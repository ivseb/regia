package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ogni code specifico degli errori di validazione ha la sua voce nei cataloghi del client, in
// italiano e in inglese (il client traduce dal code).
func TestValidationCodesHaveClientText(t *testing.T) {
	for _, lang := range []string{"it", "en"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", lang, "errors.ts"))
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range ValidationCodes {
			if !strings.Contains(string(raw), `"errors.`+code+`"`) {
				t.Errorf("manca errors.%s in web/src/i18n/%s/errors.ts", code, lang)
			}
		}
	}
}

func TestInvalidCode(t *testing.T) {
	err := InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "validation.name_required" || ve.Field != "name" || ve.Error() != "name: il nome è obbligatorio" {
		t.Fatalf("errore: %#v", err)
	}
	if Invalid("name", "x").(*ValidationError).Code != "" {
		t.Fatal("un errore senza code specifico resta «validation»")
	}
}
