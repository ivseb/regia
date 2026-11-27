package fakeai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"regia/internal/ai"
)

// Regole del GRC assistito (internal/ai/grc.go, ondata 5): proposte di collegamento per parole in
// comune, bozze con la struttura chiesta (e una ricerca con gli strumenti, se ci sono), campi
// descrittivi della notifica DORA dai dati dell'incidente. Con «inventa» nei dati il finto cita
// apposta un controllo, un'evidenza o un requisito che non esistono, per provare i controlli di Regia.

var (
	grcCtrlRe  = regexp.MustCompile(`(?m)^- (\S+) · ([^·\n]+) · requisiti: (.*)\n(?:  (.*)\n)?`)
	grcEvRe    = regexp.MustCompile(`(?m)^- (\S+) · ([^·\n]+) · [^\n]*\n(?:  (.*)\n)?`)
	grcReqRe   = regexp.MustCompile(`(?m)^- ([a-z][a-z0-9_]*:[^·\n]+?) · `)
	grcTitleRe = regexp.MustCompile(`(?m)^titolo: (.+)$`)
	grcFieldRe = regexp.MustCompile(`(?m)^- (\d+\.\d+) · `)
	grcSrcRe   = regexp.MustCompile(`(?m)^(?:controllo|rischio|requisito normativo): (.+)$`)
	grcTlRe    = regexp.MustCompile(`(?m)^- [0-9-]+ [0-9:]+ · (contained|action|decision|update) · (.+)$`)
)

func words(s string) []string { return fwordRe.FindAllString(strings.ToLower(s), -1) }

func common(a []string, b string) int {
	n := 0
	seen := map[string]bool{}
	for _, w := range words(b) {
		if !seen[w] && slices.Contains(a, w) {
			n++
		}
		seen[w] = true
	}
	return n
}

func grcRules(req ai.Request) Step {
	first := req.Messages[0].Text
	last := req.Messages[len(req.Messages)-1]
	bl := blocks(first)
	switch {
	case strings.Contains(req.System, ai.GRCMapMarker):
		out := map[string]any{}
		if ev, ok := bl["evidenza"]; ok {
			w := words(ev)
			var items []any
			for _, m := range grcCtrlRe.FindAllStringSubmatch(bl["controlli candidati"]+"\n", -1) {
				n := common(w, m[2]+" "+m[3]+" "+m[4])
				if n >= 2 {
					cov := "parziale"
					if n >= 3 {
						cov = "piena"
					}
					items = append(items, map[string]string{"controllo": m[1], "copertura": cov, "motivo": fmt.Sprintf("L'evidenza parla di %d temi del controllo.", n)})
				}
			}
			if strings.Contains(ev, "inventa") {
				items = append(items, map[string]string{"controllo": "GRC-999", "copertura": "piena", "motivo": "Controllo inventato."})
			}
			out["controlli"] = items
		} else {
			ctrl := bl["controllo"]
			w := words(ctrl)
			var items, missing []any
			for _, m := range grcEvRe.FindAllStringSubmatch(bl["evidenze candidate"]+"\n", -1) {
				if n := common(w, m[2]+" "+m[3]); n >= 2 {
					items = append(items, map[string]string{"evidenza": m[1], "copertura": "parziale", "motivo": "Stesse parole del controllo."})
				}
			}
			if m := grcReqRe.FindStringSubmatch(ctrl); m != nil {
				missing = append(missing, map[string]string{"testo": "Manca la prova del periodo in corso.", "requisito": strings.TrimSpace(m[1])})
			}
			if strings.Contains(ctrl, "inventa") {
				items = append(items, map[string]string{"evidenza": "GRC-999", "copertura": "piena", "motivo": "Evidenza inventata."})
				missing = append(missing, map[string]string{"testo": "Requisito inventato.", "requisito": "xyz:Art. 0"})
			}
			out["evidenze"], out["mancano"] = items, missing
		}
		b, _ := json.Marshal(out)
		return Step{Text: string(b)}
	case strings.Contains(req.System, ai.GRCDraftMarker):
		src := bl["fonte"]
		title := ""
		if m := grcTitleRe.FindStringSubmatch(src); m != nil {
			title = strings.TrimSpace(m[1])
		}
		if len(req.Tools) > 0 && len(last.ToolResults) == 0 {
			if w := words(title); len(w) > 0 {
				return Step{Calls: []Call{{Name: "cerca", Input: map[string]any{"testo": w[0]}}}}
			}
		}
		var refs []string
		if m := grcSrcRe.FindStringSubmatch(src); m != nil {
			refs = append(refs, strings.TrimSpace(m[1]))
		}
		for _, m := range grcReqRe.FindAllStringSubmatch(src, -1) {
			refs = append(refs, strings.TrimSpace(m[1]))
		}
		for _, msg := range req.Messages {
			for _, r := range msg.ToolResults {
				for _, m := range listRe.FindAllStringSubmatch(r.Content, -1) {
					if !slices.Contains(refs, m[1]) {
						refs = append(refs, m[1])
					}
				}
			}
		}
		if strings.Contains(src, "inventa") {
			refs = append(refs, "ZZZ-999")
		}
		var b strings.Builder
		if strings.Contains(req.System, "## Servizio, responsabile e reperibilità") {
			fmt.Fprintf(&b, "## Servizio, responsabile e reperibilità\n%s · responsabile da definire.\n\n## Passi\n1. Verifica lo stato del servizio.\n2. Esegui la procedura.\n\n", title)
		} else {
			fmt.Fprintf(&b, "## Scopo\nBozza di policy per «%s».\n\n## Ruoli e responsabilità\n| ruolo | responsabilità |\n|---|---|\n| Responsabile | da definire |\n\n## Frequenze e verifiche\n| attività | frequenza | evidenza attesa |\n|---|---|---|\n| Verifica | da definire | rapporto |\n\n", title)
		}
		b.WriteString("## Riferimenti\n")
		for _, r := range refs {
			b.WriteString("- " + r + "\n")
		}
		return Step{Text: b.String()}
	case strings.Contains(req.System, ai.GRCNoticeMarker):
		inc := bl["incidente"]
		title := ""
		if m := grcTitleRe.FindStringSubmatch(inc); m != nil {
			title = strings.TrimSpace(m[1])
		}
		desc := ""
		if _, after, ok := strings.Cut(inc, "descrizione:\n"); ok {
			desc, _, _ = strings.Cut(after, "\n")
		}
		todo := map[string]bool{}
		for _, m := range grcFieldRe.FindAllStringSubmatch(bl["campi da scrivere"], -1) {
			todo[m[1]] = true
		}
		var items []any
		if todo["2.4"] {
			items = append(items, map[string]string{"num": "2.4", "valore": strings.TrimSpace(title + ". " + desc)})
		}
		if todo["2.7"] {
			how := "staff"
			if strings.Contains(strings.ToLower(inc), "monitor") {
				how = "monitoring systems"
			}
			items = append(items, map[string]string{"num": "2.7", "valore": how})
		}
		var acts []string
		for _, m := range grcTlRe.FindAllStringSubmatch(inc, -1) {
			acts = append(acts, strings.TrimSpace(m[2]))
		}
		if todo["3.33"] && len(acts) > 0 {
			items = append(items, map[string]string{"num": "3.33", "valore": strings.Join(acts, "; ")})
		}
		// Campi che non sono da scrivere: Regia li deve scartare.
		items = append(items, map[string]string{"num": "2.2", "valore": "2000-01-01T00:00:00Z"}, map[string]string{"num": "9.9", "valore": "x"})
		b, _ := json.Marshal(map[string]any{"campi": items})
		return Step{Text: string(b)}
	}
	return Step{Text: "{}"}
}
