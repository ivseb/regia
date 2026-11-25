package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Nomi per lingua di fasi e opzioni dei campi (dati del tenant). Il nome resta canonico: il codice, le
// condizioni e i processi citano sempre il nome; le etichette sono solo testo di visualizzazione
// (lingua → nome). Una lingua senza etichetta segue il nome.

// LabelLangs sono le lingue ammesse come chiave delle etichette (coincidono con le cartelle di web/src/i18n).
var LabelLangs = []string{"it", "en"}

// CleanLabels normalizza le etichette: toglie gli spazi in eccesso e le voci vuote, ammette solo le
// lingue supportate e il testo fino a max caratteri (la lunghezza del nome).
func CleanLabels(field string, in map[string]string, max int) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for lang, text := range in {
		if !slices.Contains(LabelLangs, lang) {
			return nil, InvalidValue(field, "lingua %q non supportata per le etichette", lang)
		}
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			continue
		}
		if len([]rune(text)) > max {
			return nil, TooLong(field, max, "l'etichetta in %s supera %d caratteri", lang, max)
		}
		out[lang] = text
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// labelProblem dice cosa non va nelle etichette di una fase (vuoto se vanno bene).
func labelProblem(labels map[string]string, max int) string {
	for lang, text := range labels {
		if !slices.Contains(LabelLangs, lang) {
			return "lingua " + lang + " non supportata per le etichette"
		}
		if len([]rune(strings.TrimSpace(text))) > max {
			return "l'etichetta in " + lang + " è troppo lunga (massimo " + itoa(max) + " caratteri)"
		}
	}
	return ""
}

func parseLabels(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// labelsJSON è il valore da scrivere nella colonna labels (mai null).
func labelsJSON(m map[string]string) []byte {
	if len(m) == 0 {
		return []byte("{}")
	}
	b, _ := json.Marshal(m)
	return b
}

// StateLabels dà i nomi per lingua delle fasi, per nome della fase (nome → lingua → etichetta): serve
// all'interfaccia per mostrare lo stato di un elemento nelle liste, dove arriva solo il nome. Se due
// insiemi di stati usano lo stesso nome con etichette diverse vale la prima trovata.
func StateLabels(ctx context.Context, tx pgx.Tx) (map[string]map[string]string, error) {
	rows, err := tx.Query(ctx, `select o.name, o.labels from options o join option_sets s on s.tenant_id = o.tenant_id and s.id = o.option_set_id
		where s.kind = 'state' and o.labels <> '{}'::jsonb order by o.archived, o.ordinal, o.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var name string
		var lb []byte
		if err := rows.Scan(&name, &lb); err != nil {
			return nil, err
		}
		if _, dup := out[name]; dup {
			continue
		}
		if m := parseLabels(lb); m != nil {
			out[name] = m
		}
	}
	return out, rows.Err()
}
