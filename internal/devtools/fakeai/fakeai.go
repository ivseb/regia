// Package fakeai è il fornitore del modello finto, senza chiavi né rete, per i test Go e gli
// e2e (replica/ai.md §6). Si registra solo in sviluppo (regia serve --dev) e nei test, come i
// finti connettori di internal/devtools: in produzione il tipo «fake» non esiste.
//
// Due modi d'uso:
//   - un modello qualunque (es. «finto-approfondito») ha un comportamento a regole, utile agli
//     e2e: «commenta DEM-1: testo» propone un commento, una chiave nella domanda la legge,
//     altrimenti cerca le parole della domanda; dopo i risultati degli strumenti risponde
//     elencando gli elementi trovati con chiave e titolo;
//   - un modello «scenario:<nome>» segue le risposte programmate con Program, una per chiamata.
//
// Ogni richiesta ricevuta si conserva (Requests), così i test controllano cosa è uscito verso
// il «fornitore» (per esempio che i dati personali fossero mascherati).
package fakeai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"regia/internal/ai"
)

// Kind è il tipo di fornitore.
const Kind = "fake"

// Modelli proposti per i livelli.
const (
	ModelFast = "finto-rapido"
	ModelDeep = "finto-approfondito"
)

// Register descrive il tipo di fornitore. External: si comporta come un servizio esterno
// (il mascheramento si applica), così i test lo verificano.
func Register() ai.Kind {
	return ai.Kind{Key: Kind, New: New, External: true, Tools: true,
		Proposed: []ai.ModelInfo{
			{ID: ModelFast, Name: "Finto rapido", Tier: ai.TierFast},
			{ID: ModelDeep, Name: "Finto approfondito", Tier: ai.TierDeep},
		}}
}

// Step è una risposta programmata.
type Step struct {
	Text         string
	Calls        []Call
	InputTokens  int64
	OutputTokens int64
	// Err: la chiamata fallisce con questo errore (es. &ai.ProviderError{...}).
	Err error
	// Refuse: il modello rifiuta.
	Refuse bool
}

// Call è una chiamata di strumento programmata.
type Call struct {
	Name  string
	Input map[string]any
}

var (
	mu        sync.Mutex
	scenarios = map[string][]Step{}
	requests  = map[string][]ai.Request{}
)

// Program assegna allo scenario le risposte, in ordine (sostituisce le precedenti).
func Program(name string, steps ...Step) {
	mu.Lock()
	defer mu.Unlock()
	scenarios[name] = steps
	requests[name] = nil
}

// Requests sono le richieste ricevute per uno scenario (o per un modello a regole).
func Requests(name string) []ai.Request {
	mu.Lock()
	defer mu.Unlock()
	return append([]ai.Request(nil), requests[name]...)
}

type provider struct{}

// New crea il fornitore finto (la configurazione non serve).
func New(ai.ProviderConfig) (ai.Provider, error) { return provider{}, nil }

func (provider) Models(context.Context) ([]ai.ModelInfo, error) {
	return []ai.ModelInfo{{ID: ModelFast, Name: "Finto rapido", Tier: ai.TierFast}, {ID: ModelDeep, Name: "Finto approfondito", Tier: ai.TierDeep}}, nil
}

func approxTokens(req ai.Request) int64 {
	n := len(req.System)
	for _, m := range req.Messages {
		n += len(m.Text)
		for _, r := range m.ToolResults {
			n += len(r.Content)
		}
		for _, c := range m.ToolCalls {
			n += len(c.Input)
		}
	}
	return int64(n/4 + 1)
}

// CountTokens conta i token come li conterà Chat (stessa stima), senza registrare la richiesta:
// così il controllo del budget prima della chiamata si prova anche con il finto.
func (provider) CountTokens(ctx context.Context, req ai.Request) (int64, error) {
	if strings.HasPrefix(req.Model, "scenario:") {
		mu.Lock()
		list := scenarios[strings.TrimPrefix(req.Model, "scenario:")]
		mu.Unlock()
		if len(list) > 0 && list[0].InputTokens > 0 {
			return list[0].InputTokens, ctx.Err()
		}
	}
	return approxTokens(req), ctx.Err()
}

func (provider) Chat(ctx context.Context, req ai.Request) (*ai.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(req.Model, "scenario:")
	mu.Lock()
	requests[name] = append(requests[name], req)
	var step *Step
	if strings.HasPrefix(req.Model, "scenario:") {
		list := scenarios[name]
		if len(list) == 0 {
			mu.Unlock()
			return nil, &ai.ProviderError{Code: "scenario_empty", Msg: "lo scenario finto " + name + " non ha più risposte"}
		}
		step = &list[0]
		scenarios[name] = list[1:]
	}
	mu.Unlock()
	if step == nil {
		s := rules(req)
		step = &s
	}
	if step.Err != nil {
		return nil, step.Err
	}
	resp := &ai.Response{Model: req.Model, InputTokens: step.InputTokens, OutputTokens: step.OutputTokens}
	if resp.InputTokens == 0 {
		resp.InputTokens = approxTokens(req)
	}
	if resp.OutputTokens == 0 {
		resp.OutputTokens = int64(len(step.Text)/4 + 1)
	}
	resp.Message = ai.Message{Role: ai.RoleAssistant, Text: step.Text}
	for i, c := range step.Calls {
		in, _ := json.Marshal(c.Input)
		resp.Message.ToolCalls = append(resp.Message.ToolCalls, ai.ToolCall{ID: fmt.Sprintf("finto_%d", i+1), Name: c.Name, Input: in})
	}
	switch {
	case step.Refuse:
		resp.Stop = ai.StopRefusal
	case len(resp.Message.ToolCalls) > 0:
		resp.Stop = ai.StopToolUse
	default:
		resp.Stop = ai.StopEnd
	}
	return resp, nil
}

// ChatStream risponde come Chat e passa il testo a pezzi (una parola alla volta, con una
// piccola pausa), così l'interfaccia e i test vedono la risposta crescere.
func (p provider) ChatStream(ctx context.Context, req ai.Request, onText func(string)) (*ai.Response, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	text := resp.Message.Text
	for text != "" {
		i := strings.IndexByte(text, ' ')
		part := text
		if i >= 0 {
			part = text[:i+1]
		}
		text = text[len(part):]
		onText(part)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(StreamPause):
		}
	}
	return resp, nil
}

// StreamPause è la pausa fra i pezzi del testo in streaming.
var StreamPause = 15 * time.Millisecond

var (
	keyRe     = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{0,19}-[1-9][0-9]*\b`)
	commentRe = regexp.MustCompile(`(?is)^\s*commenta\s+([A-Z][A-Z0-9_]{0,19}-[1-9][0-9]*)\s*[:,]?\s*(.+)$`)
	listRe    = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]{0,19}-[A-Z0-9_.-]+) · [^·]* · [^·]* · (.+)$`)
	readRe    = regexp.MustCompile(`(?m)^chiave: (\S+)\n(?:.*\n)*?titolo: (.+)$`)
)

// GraphMarker è la riga del formato che il prompt delle domande sul grafo chiede al modello:
// con questa nelle istruzioni il finto traduce la domanda in una query.
const GraphMarker = "QUERY: la query"

var (
	graphFixRe   = regexp.MustCompile(`(?s)^La query «(.*)» non è valida: .*\(frammento: «(.*)»\)`)
	graphQuoteRe = regexp.MustCompile("`([^`]+)`")
	clauseRe     = regexp.MustCompile(`-?[^\s:"]+:\s*(?:"[^"]*"|\S+)|"[^"]*"|\S+`)
	graphWords   = []struct{ prefix, clause string }{
		{"applicazion", "tipo: application"},
		// Relazioni e sottoquery (ondata 3): il campo delle vulnerabilità di DevSecOps.
		{"vulnerabil", "scadenza_vulnerabilità: <oggi"},
		{"richiest", "tipo: business_request"},
		{"rischi", "tipo: risk"},
		{"incident", "tipo: incident"},
		{"attivit", "tipo: task"},
		{"apert", "fase: aperta"},
		{"chius", "fase: chiusa"},
		{"mie", "assegnatario: me"},
		{"miei", "assegnatario: me"},
		// Un campo che non esiste: il motore lo rifiuta e la correzione lo toglie.
		{"critic", "criticità: alta"},
	}
)

// graphRules: la query tra apici inversi nella domanda, se c'è; altrimenti le parole note
// della domanda diventano clausole. Nel giro di correzione toglie la clausola sbagliata.
func graphRules(req ai.Request) Step {
	last := req.Messages[len(req.Messages)-1].Text
	if m := graphFixRe.FindStringSubmatch(last); m != nil {
		var keep []string
		dropped := false
		for _, c := range clauseRe.FindAllString(m[1], -1) {
			if !dropped && m[2] != "" && strings.Contains(c, strings.TrimSpace(m[2])) {
				dropped = true
				continue
			}
			keep = append(keep, c)
		}
		return Step{Text: "QUERY: " + strings.Join(keep, " ") + "\nSPIEGAZIONE: Ho tolto «" + m[2] + "», che il catalogo non conosce."}
	}
	if m := graphQuoteRe.FindStringSubmatch(last); m != nil {
		return Step{Text: "QUERY: " + m[1] + "\nSPIEGAZIONE: Ho usato la query che hai scritto."}
	}
	var clauses []string
	for _, w := range strings.Fields(strings.ToLower(last)) {
		w = strings.Trim(w, "?.,;:!«»\"'")
		for _, g := range graphWords {
			if strings.HasPrefix(w, g.prefix) && !slices.Contains(clauses, g.clause) {
				clauses = append(clauses, g.clause)
				break
			}
		}
	}
	if len(clauses) == 0 {
		clauses = []string{strings.Trim(last, "?")}
	}
	return Step{Text: "QUERY: " + strings.Join(clauses, " ") + "\nSPIEGAZIONE: Ho cercato gli elementi che la domanda nomina."}
}

// Marcatori dei compiti dell'assistente nella scheda (internal/ai/card.go).
const (
	CardSummaryMarker = "Compito: riassumi l'elemento"
	CardGapsMarker    = "Compito: cosa manca per decidere"
	CardDraftMarker   = "Compito: scrivi"
	CardTriageMarker  = "Compito: la persona sta creando"
)

var (
	dataRe     = regexp.MustCompile(`(?s)<dati origine="([^"]*)">\n(.*?)\n</dati>`)
	lineFldRe  = regexp.MustCompile(`(?m)^(chiave|titolo|fase): (.+)$`)
	commentQRe = regexp.MustCompile(`(?m)^\[(c\d+)\] ([^,]+), [0-9-]+: (.*\?.*)$`)
	linkedRe   = regexp.MustCompile(`(?m)^- (?:genitore|figlio|relazione[^:]*): (\S+) `)
	itemRe     = regexp.MustCompile(`(?m)^- (\S+) · ([^·\n]+) · (.+)$`)
	fieldLnRe  = regexp.MustCompile(`(?m)^- (\S+) · [^:\n]+: (.+)$`)
	fwordRe    = regexp.MustCompile(`[\p{L}\p{N}]{4,}`)
)

// blocks: i dati delimitati del messaggio, per origine.
func blocks(text string) map[string]string {
	out := map[string]string{}
	for _, m := range dataRe.FindAllStringSubmatch(text, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// cardRules: risposte deterministiche per riassunto, mancanze, bozze e analisi alla creazione.
func cardRules(req ai.Request) Step {
	user := req.Messages[len(req.Messages)-1].Text
	bl := blocks(user)
	var el string
	for k, v := range bl {
		if strings.Contains(v, "chiave: ") {
			el = bl[k]
		}
	}
	f := map[string]string{}
	for _, m := range lineFldRe.FindAllStringSubmatch(el, -1) {
		if _, ok := f[m[1]]; !ok {
			f[m[1]] = strings.TrimSpace(m[2])
		}
	}
	switch {
	case strings.Contains(req.System, CardSummaryMarker):
		text := fmt.Sprintf("%s «%s» è in fase %s.", f["chiave"], f["titolo"], f["fase"])
		if m := linkedRe.FindStringSubmatch(el); m != nil {
			text += " È collegata a " + m[1] + "."
		}
		return Step{Text: text}
	case strings.Contains(req.System, CardGapsMarker):
		var items []map[string]string
		for _, m := range commentQRe.FindAllStringSubmatch(el, -1) {
			items = append(items, map[string]string{"tipo": "domanda", "testo": "Nessuno ha risposto alla domanda di " + strings.TrimSpace(m[2]) + ".", "commento": m[1]})
		}
		if strings.Contains(el, "inventa") {
			items = append(items, map[string]string{"tipo": "relazione", "testo": "Manca il collegamento a un elemento inventato.", "elemento": "ZZZ-999"})
		}
		b, _ := json.Marshal(map[string]any{"mancanze": items})
		return Step{Text: string(b)}
	case strings.Contains(req.System, CardTriageMarker):
		nuovo := strings.ToLower(bl["nuovo elemento"])
		words := fwordRe.FindAllString(nuovo, -1)
		out := map[string]any{"duplicati": []any{}, "campi": []any{}, "collegamenti": []any{}}
		var dups, fields, links []any
		for _, m := range itemRe.FindAllStringSubmatch(bl["simili"], -1) {
			common := 0
			for _, w := range fwordRe.FindAllString(strings.ToLower(m[3]), -1) {
				if slices.Contains(words, w) {
					common++
				}
			}
			if common >= 2 {
				dups = append(dups, map[string]string{"elemento": m[1], "motivo": "Stesse parole nel titolo."})
			}
		}
		for _, m := range fieldLnRe.FindAllStringSubmatch(bl["campi"], -1) {
			for _, o := range strings.Split(m[2], ",") {
				o = strings.TrimSpace(o)
				if o != "" && slices.Contains(words, strings.ToLower(o)) {
					fields = append(fields, map[string]string{"campo": m[1], "valore": o, "motivo": "Il testo dice «" + strings.ToLower(o) + "»."})
					break
				}
			}
		}
		for _, m := range itemRe.FindAllStringSubmatch(bl["collegamenti possibili"], -1) {
			if t := strings.ToLower(strings.TrimSpace(m[3])); t != "" && strings.Contains(nuovo, t) {
				links = append(links, map[string]string{"elemento": m[1], "motivo": "Il testo la nomina."})
			}
		}
		if dups != nil {
			out["duplicati"] = dups
		}
		if fields != nil {
			out["campi"] = fields
		}
		if links != nil {
			out["collegamenti"] = links
		}
		b, _ := json.Marshal(out)
		return Step{Text: string(b)}
	}
	// Bozze.
	hint := strings.TrimSpace(bl["indicazioni della persona"])
	var text string
	switch {
	case strings.Contains(req.System, "nuova descrizione"):
		text = "Contesto: " + f["titolo"] + ".\n\nBisogno: da definire."
	case strings.Contains(req.System, "per chi ha aperto"):
		text = "La sua richiesta " + f["chiave"] + " è in fase " + f["fase"] + "."
	default:
		text = "Aggiornamento su " + f["chiave"] + ": la fase è " + f["fase"] + "."
	}
	if hint != "" {
		text += " " + hint
	}
	return Step{Text: text}
}

var wikiCtxRe = regexp.MustCompile(`(?s)<dati origine="wiki personale">.*?</dati>\s*`)

// withoutWiki toglie dai messaggi il contesto della wiki personale, così le regole dell'assistente
// e delle domande sul grafo leggono solo la domanda della persona.
func withoutWiki(req ai.Request) ai.Request {
	msgs := make([]ai.Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Text = wikiCtxRe.ReplaceAllString(m.Text, "")
		msgs[i] = m
	}
	req.Messages = msgs
	return req
}

var (
	wikiElemRe = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]{0,19}-[1-9][0-9]*) · ([^·\n]+) · ([^·\n]*) · ([^·\n]+?)(?: · .*)?$`)
	wikiConvRe = regexp.MustCompile(`(?m)^\[(\d{4}-\d{2}-\d{2}), «[^»]*»\] (Domanda|Risposta): (.+)$`)
)

// wikiRules aggiorna la wiki in modo deterministico: «Le mie priorità» con gli elementi delle
// fonti, una pagina per ogni progetto, «Registro delle decisioni» con le frasi delle
// conversazioni che parlano di una decisione. Dopo i risultati degli strumenti conclude.
func wikiRules(req ai.Request) Step {
	last := req.Messages[len(req.Messages)-1]
	if len(last.ToolResults) > 0 {
		n := 0
		var errs []string
		for _, r := range last.ToolResults {
			if r.IsError {
				errs = append(errs, r.Content)
			} else if strings.Contains(r.Content, "salvata") {
				n++
			}
		}
		if len(errs) > 0 {
			return Step{Text: fmt.Sprintf("Aggiornate %d pagine; non riuscite: %s", n, strings.Join(errs, "; "))}
		}
		return Step{Text: fmt.Sprintf("Aggiornate %d pagine della wiki.", n)}
	}
	bl := blocks(last.Text)
	var prio []string
	var calls []Call
	for _, origin := range []string{"elementi seguiti o creati, cambiati dall'ultimo aggiornamento", "aggiunte a mano"} {
		for _, m := range wikiElemRe.FindAllStringSubmatch(bl[origin], -1) {
			key, typ, state, title := m[1], strings.TrimSpace(m[2]), strings.TrimSpace(m[3]), strings.TrimSpace(m[4])
			prio = append(prio, fmt.Sprintf("- %s — %s (%s)", key, title, state))
			if strings.Contains(strings.ToLower(typ), "progetto") || strings.Contains(strings.ToLower(typ), "project") {
				calls = append(calls, Call{Name: "wiki_scrivi", Input: map[string]any{"pagina": title,
					"testo": fmt.Sprintf("Progetto %s, fase %s.\n\nDa seguire: avanzamento e decisioni.", key, state), "nota": "progetto " + key}})
			}
		}
	}
	var decisions []string
	for _, m := range wikiConvRe.FindAllStringSubmatch(bl["conversazioni con l'assistente"], -1) {
		if strings.Contains(strings.ToLower(m[3]), "decis") {
			decisions = append(decisions, fmt.Sprintf("- %s: %s", m[1], m[3]))
		}
	}
	if len(prio) > 0 {
		calls = append([]Call{{Name: "wiki_scrivi", Input: map[string]any{"pagina": "Le mie priorità",
			"testo": "Elementi da seguire:\n\n" + strings.Join(prio, "\n"), "nota": "priorità dalle fonti"}}}, calls...)
	}
	if len(decisions) > 0 {
		calls = append(calls, Call{Name: "wiki_scrivi", Input: map[string]any{"pagina": "Registro delle decisioni",
			"testo": strings.Join(decisions, "\n"), "nota": "decisioni dalle conversazioni"}})
	}
	if len(calls) == 0 {
		return Step{Text: "Niente da aggiornare."}
	}
	return Step{Calls: calls}
}

// rules è il comportamento a regole dei modelli non programmati.
func rules(req ai.Request) Step {
	if strings.Contains(req.System, ai.WikiMarker) {
		return wikiRules(req)
	}
	if !strings.Contains(req.System, "Lavori sulla scheda di un elemento") {
		req = withoutWiki(req)
	}
	if strings.Contains(req.System, GraphMarker) {
		return graphRules(req)
	}
	if strings.Contains(req.System, "Lavori sulla scheda di un elemento") {
		return cardRules(req)
	}
	last := req.Messages[len(req.Messages)-1]
	if len(last.ToolResults) > 0 {
		var found, errs, props []string
		for _, r := range last.ToolResults {
			switch {
			case r.IsError:
				errs = append(errs, r.Content)
			case strings.HasPrefix(r.Content, "Proposta preparata"):
				props = append(props, r.Content)
			default:
				for _, m := range listRe.FindAllStringSubmatch(r.Content, -1) {
					found = append(found, m[1]+" «"+strings.TrimSpace(m[2])+"»")
				}
				for _, m := range readRe.FindAllStringSubmatch(r.Content, -1) {
					found = append(found, m[1]+" «"+strings.TrimSpace(m[2])+"»")
				}
			}
		}
		switch {
		case len(props) > 0:
			return Step{Text: "Ho preparato la proposta: confermala o rifiutala qui sotto."}
		case len(found) > 0:
			return Step{Text: "Ho trovato: " + strings.Join(found, ", ") + "."}
		case len(errs) > 0:
			return Step{Text: "Non ci sono riuscito: " + strings.Join(errs, "; ")}
		}
		return Step{Text: "Non ho trovato elementi che corrispondono."}
	}
	q := strings.TrimSpace(last.Text)
	if len(req.Tools) == 0 {
		return Step{Text: "Bozza (senza strumenti): " + q}
	}
	if m := commentRe.FindStringSubmatch(q); m != nil {
		return Step{Calls: []Call{{Name: "commenta", Input: map[string]any{"elemento": m[1], "testo": strings.TrimSpace(m[2])}}}}
	}
	if k := keyRe.FindString(q); k != "" {
		return Step{Calls: []Call{{Name: "leggi", Input: map[string]any{"elemento": k}}}}
	}
	words := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(q, "cerca "), "Cerca "))
	return Step{Calls: []Call{{Name: "cerca", Input: map[string]any{"testo": words}}}}
}
