package fakeai

import (
	"encoding/json"
	"regexp"
	"strings"

	"regia/internal/ai"
)

// Regole dei questionari precompilati (internal/ai/questionnaire.go): per ogni domanda la risposta
// confermata della stessa domanda (confidenza alta) o, per una scelta singola, il fatto del grafo
// con la stessa chiave se coincide con un'opzione (media); altrimenti nessun valore. Con
// «inventa» nel testo di una domanda il finto cita apposta una fonte che non esiste e un valore
// che la domanda non accetta, per provare i controlli di Regia.

var (
	qQuestionRe = regexp.MustCompile(`(?m)^- (\S+) · (\S+) · ([^·\n]+)(.*)$`)
	qAnswerRe   = regexp.MustCompile(`(?m)^- (f\d+) · risposta confermata · .* · per la domanda (\S+) · valore: (.+)$`)
	qFactRe     = regexp.MustCompile(`(?m)^- (f\d+) · fatto del grafo · .* \(([^()\s]+)\): (.+)$`)
	qOptionsRe  = regexp.MustCompile(`opzioni: ([^·]+)`)
)

func questionnaireRules(req ai.Request) Step {
	bl := blocks(req.Messages[len(req.Messages)-1].Text)
	type ans struct {
		alias string
		value any
	}
	answers := map[string]ans{}
	for _, m := range qAnswerRe.FindAllStringSubmatch(bl["fonti"], -1) {
		if _, ok := answers[m[2]]; ok {
			continue
		}
		var v any
		if json.Unmarshal([]byte(m[3]), &v) == nil {
			answers[m[2]] = ans{m[1], v}
		}
	}
	facts := map[string]ans{}
	for _, m := range qFactRe.FindAllStringSubmatch(bl["fonti"], -1) {
		facts[m[2]] = ans{m[1], strings.TrimSpace(m[3])}
	}
	var out []map[string]any
	for _, m := range qQuestionRe.FindAllStringSubmatch(bl["questionario"], -1) {
		key, typ, label, rest := m[1], m[2], m[3], m[4]
		item := map[string]any{"domanda": key, "valore": nil, "fonti": []string{}, "confidenza": "bassa", "motivo": ""}
		switch {
		case strings.Contains(label, "inventa"):
			item["valore"], item["fonti"], item["confidenza"], item["motivo"] = "valore-inventato", []string{"f99"}, "alta", "Fonte inventata."
		case answers[key].alias != "":
			a := answers[key]
			item["valore"], item["fonti"], item["confidenza"], item["motivo"] = a.value, []string{a.alias}, "alta", "Risposta confermata nella valutazione precedente."
		case typ == "single" && facts[key].alias != "":
			f := facts[key]
			if om := qOptionsRe.FindStringSubmatch(rest); om != nil {
				for _, o := range strings.Split(om[1], " | ") {
					val, lbl, _ := strings.Cut(strings.TrimSpace(o), "=")
					if strings.EqualFold(val, f.value.(string)) || strings.EqualFold(lbl, f.value.(string)) {
						// Il modello esagera la confidenza: Regia la riporta a «media».
						item["valore"], item["fonti"], item["confidenza"], item["motivo"] = val, []string{f.alias}, "alta", "Dal fatto del grafo."
					}
				}
			}
		}
		out = append(out, item)
	}
	b, _ := json.Marshal(map[string]any{"risposte": out})
	return Step{Text: string(b)}
}
