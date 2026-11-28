package reviews

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"regia/internal/engine"
)

// Definition è il contenuto di una versione di un modello di questionario (sdlc.md §5).
type Definition struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Sections    []Section    `json:"sections"`
	Derivations []Derivation `json:"derivations,omitempty"`
	// Scoring: soglie sul punteggio che propongono l'esito (assente = nessun esito proposto).
	Scoring *Scoring `json:"scoring,omitempty"`
}

// Scoring decide l'esito proposto dalla percentuale di punteggio ottenuta.
type Scoring struct {
	Thresholds []Threshold `json:"thresholds"`
}

// Threshold: da Min (percentuale, inclusa) in su l'esito proposto è Outcome (la chiave di
// un esito del tipo di valutazione). Vale la soglia più alta raggiunta.
type Threshold struct {
	Min     float64 `json:"min"`
	Outcome string  `json:"outcome"`
}

// Section è una sezione, assegnabile a una persona.
type Section struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	VisibleWhen string `json:"visible_when,omitempty"`
	// FilledBy: chi compila la sezione. Vuoto = chi avvia il questionario e il team;
	// "team" = solo il team (e chi ha la sezione assegnata a mano).
	FilledBy  string     `json:"filled_by,omitempty"`
	Questions []Question `json:"questions"`
}

// Tipi di domanda.
const (
	QText       = "text"
	QSingle     = "single"
	QMulti      = "multi"
	QYesNo      = "yes_no"
	QNumber     = "number"
	QDate       = "date"
	QAttachment = "attachment"
	QScale      = "scale"  // numero intero tra Min e Max (default 1-5)
	QMatrix     = "matrix" // una scelta tra le colonne (Options) per ogni riga (Rows)
)

var questionTypes = []string{QText, QSingle, QMulti, QYesNo, QNumber, QDate, QAttachment, QScale, QMatrix}

// Question è una domanda tipizzata.
type Question struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Help     string `json:"help,omitempty"`
	// VisibleWhen: condizione sulle risposte precedenti e sui fatti dell'elemento.
	VisibleWhen string `json:"visible_when,omitempty"`
	// Prefill: fatto dell'elemento da cui si precompila la risposta (es. classificazione_dati).
	Prefill   string   `json:"prefill,omitempty"`
	Options   []Option `json:"options,omitempty"`   // scelta singola o multipla
	Multiline bool     `json:"multiline,omitempty"` // testo su più righe
	Min       *float64 `json:"min,omitempty"`       // numero
	Max       *float64 `json:"max,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	// Matrice: le righe (le colonne sono le Options).
	Rows []Option `json:"rows,omitempty"`
	// Scala: etichette degli estremi; Reverse = più alto è peggio, per il punteggio.
	MinLabel string `json:"min_label,omitempty"`
	MaxLabel string `json:"max_label,omitempty"`
	Reverse  bool   `json:"reverse,omitempty"`
	// Punteggio: peso della domanda (default 1; 0 = non conta) e punti di sì e no.
	Weight   *float64 `json:"weight,omitempty"`
	YesScore *float64 `json:"yes_score,omitempty"`
	NoScore  *float64 `json:"no_score,omitempty"`
}

// Option è una scelta. Value è ciò che usano le condizioni (es. "classificazione_dati: personali").
type Option struct {
	Value string   `json:"value"`
	Label string   `json:"label"`
	Score *float64 `json:"score,omitempty"` // punti della scelta
}

// Derivation è una regola di derivazione: risposta → prescrizione proposta.
type Derivation struct {
	Key      string `json:"key"`
	When     string `json:"when"`
	Text     string `json:"text"`
	Category string `json:"category,omitempty"`
	// Kind: "prescription" (default) o "requirement". La prescrizione è proposta al team,
	// che la accetta o la scarta; il requisito è un «requisito proposto» che alla chiusura
	// della valutazione (esito favorevole o con prescrizioni) diventa un requisito vero del
	// modulo requisiti, senza passare dall'accettazione. Un requisito senza categoria
	// prende la categoria "Requisito".
	Kind string `json:"kind,omitempty"`
	// Solo per i requisiti: titolo (se manca si ricava dal testo), metodo di verifica
	// ("manual" con firma, default, oppure "evidence" con la chiave del controllo in Check).
	Title  string `json:"title,omitempty"`
	Method string `json:"method,omitempty"`
	Check  string `json:"check,omitempty"`
}

// Problem è un errore della definizione, con il percorso per l'editor.
type Problem struct {
	Level string `json:"level"` // error | warning
	Path  string `json:"path"`
	Msg   string `json:"message"`
}

func hasErrors(ps []Problem) bool {
	return slices.ContainsFunc(ps, func(p Problem) bool { return p.Level == "error" })
}

// InvalidDefinition accompagna i problemi di validazione di un modello.
type InvalidDefinition struct{ Problems []Problem }

func (e *InvalidDefinition) Error() string {
	for _, p := range e.Problems {
		if p.Level == "error" {
			return "modello di questionario non valido: " + p.Path + ": " + p.Msg
		}
	}
	return "modello di questionario non valido"
}

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var factRe = regexp.MustCompile(`^[\p{L}0-9_.]{1,80}$`)

// DecodeDefinition legge una definizione JSON rifiutando i campi sconosciuti.
func DecodeDefinition(raw []byte) (*Definition, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	d := &Definition{}
	if err := dec.Decode(d); err != nil {
		return nil, engine.InvalidValue("definition", "JSON non valido: %v", err)
	}
	return d, nil
}

// Validate controlla la definizione. Le condizioni possono usare i fatti dell'elemento e
// le risposte alle domande che vengono prima (l'ordine è quello di compilazione).
func (d *Definition) Validate() []Problem {
	ps := []Problem{}
	errf := func(path, format string, args ...any) {
		ps = append(ps, Problem{"error", path, fmt.Sprintf(format, args...)})
	}
	warn := func(path, format string, args ...any) {
		ps = append(ps, Problem{"warning", path, fmt.Sprintf(format, args...)})
	}
	all := map[string]int{} // chiave della domanda → posizione
	pos := 0
	for _, s := range d.Sections {
		for _, q := range s.Questions {
			if _, dup := all[q.Key]; !dup {
				all[q.Key] = pos
			}
			pos++
		}
	}
	// cond controlla la sintassi e che le domande citate vengano prima di "at".
	cond := func(path, src string, at int) {
		if strings.TrimSpace(src) == "" {
			return
		}
		c, err := engine.ParseCondition(src)
		if err != nil {
			errf(path, "condizione non valida: %v", err)
			return
		}
		for _, k := range c.Keys() {
			if p, ok := all[k]; ok && p >= at {
				errf(path, "la condizione usa %q, una domanda che non viene prima di questa", k)
			}
		}
	}
	if strings.TrimSpace(d.Name) == "" {
		errf("name", "il nome del modello è obbligatorio")
	}
	if len(d.Sections) == 0 {
		errf("sections", "serve almeno una sezione")
	}
	sections := map[string]bool{}
	seen := map[string]bool{}
	pos = 0
	for i, s := range d.Sections {
		sp := fmt.Sprintf("sections[%d]", i)
		if !keyRe.MatchString(s.Key) {
			errf(sp+".key", "chiave %q non valida (minuscole, cifre e _)", s.Key)
		} else if sections[s.Key] {
			errf(sp+".key", "sezione %q ripetuta", s.Key)
		}
		sections[s.Key] = true
		if strings.TrimSpace(s.Title) == "" {
			errf(sp+".title", "il titolo della sezione è obbligatorio")
		}
		cond(sp+".visible_when", s.VisibleWhen, pos)
		if s.FilledBy != "" && s.FilledBy != "team" {
			errf(sp+".filled_by", "valore %q non ammesso (vuoto o team)", s.FilledBy)
		}
		if len(s.Questions) == 0 {
			errf(sp+".questions", "la sezione non ha domande")
		}
		for j, q := range s.Questions {
			qp := fmt.Sprintf("%s.questions[%d]", sp, j)
			if !keyRe.MatchString(q.Key) {
				errf(qp+".key", "chiave %q non valida (minuscole, cifre e _)", q.Key)
			} else if seen[q.Key] {
				errf(qp+".key", "domanda %q ripetuta: le chiavi valgono in tutto il modello", q.Key)
			}
			seen[q.Key] = true
			if strings.TrimSpace(q.Label) == "" {
				errf(qp+".label", "il testo della domanda è obbligatorio")
			}
			if !slices.Contains(questionTypes, q.Type) {
				errf(qp+".type", "tipo %q non ammesso (%s)", q.Type, strings.Join(questionTypes, ", "))
			}
			cond(qp+".visible_when", q.VisibleWhen, pos)
			if q.Prefill != "" {
				if !factRe.MatchString(q.Prefill) {
					errf(qp+".prefill", "nome di fatto %q non valido", q.Prefill)
				} else if q.Type == QAttachment {
					errf(qp+".prefill", "un allegato non si precompila")
				}
			}
			checkOpts := func(path string, opts []Option, min int) {
				if len(opts) < min {
					errf(path, "servono almeno %d voci", min)
				}
				vals := map[string]bool{}
				for k, o := range opts {
					op := fmt.Sprintf("%s[%d]", path, k)
					v := strings.ToLower(strings.TrimSpace(o.Value))
					switch {
					case v == "":
						errf(op+".value", "valore della scelta obbligatorio")
					case strings.ContainsAny(v, ",\":") || (q.Type == QMatrix && strings.Contains(v, ".")):
						errf(op+".value", "il valore %q non può contenere virgole, punti, due punti o virgolette", o.Value)
					case vals[v]:
						errf(op+".value", "valore %q ripetuto", o.Value)
					}
					vals[v] = true
					if strings.TrimSpace(o.Label) == "" {
						errf(op+".label", "etichetta della scelta obbligatoria")
					}
				}
			}
			switch q.Type {
			case QSingle, QMulti:
				checkOpts(qp+".options", q.Options, 2)
			case QMatrix:
				checkOpts(qp+".options", q.Options, 2)
				checkOpts(qp+".rows", q.Rows, 1)
			default:
				if len(q.Options) > 0 {
					warn(qp+".options", "le scelte contano solo per le domande a scelta e le matrici")
				}
			}
			if len(q.Rows) > 0 && q.Type != QMatrix {
				warn(qp+".rows", "le righe contano solo per le matrici")
			}
			if q.Type == QScale {
				lo, hi := q.scaleRange()
				if lo >= hi || hi-lo > 20 || lo != math.Trunc(lo) || hi != math.Trunc(hi) {
					errf(qp+".min", "la scala va da un intero a un intero maggiore, al massimo 20 passi")
				}
			}
			if q.Weight != nil && (*q.Weight < 0 || *q.Weight > 100) {
				errf(qp+".weight", "il peso va da 0 a 100")
			}
			for _, o := range append(append([]Option{}, q.Options...), q.Rows...) {
				if o.Score != nil && (*o.Score < -1000 || *o.Score > 1000) {
					errf(qp+".options", "i punti di %q sono fuori misura", o.Value)
				}
			}
			if (q.YesScore != nil || q.NoScore != nil) && q.Type != QYesNo {
				warn(qp+".yes_score", "i punti di sì e no contano solo per le domande sì/no")
			}
			if q.Min != nil && q.Max != nil && *q.Min > *q.Max {
				errf(qp+".min", "il minimo è maggiore del massimo")
			}
			if (q.Min != nil || q.Max != nil) && q.Type != QNumber && q.Type != QScale {
				warn(qp+".min", "minimo e massimo contano solo per numeri e scale")
			}
			pos++
		}
	}
	der := map[string]bool{}
	for i, r := range d.Derivations {
		rp := fmt.Sprintf("derivations[%d]", i)
		if !keyRe.MatchString(r.Key) {
			errf(rp+".key", "chiave %q non valida (minuscole, cifre e _)", r.Key)
		} else if der[r.Key] {
			errf(rp+".key", "regola %q ripetuta", r.Key)
		}
		der[r.Key] = true
		if strings.TrimSpace(r.When) == "" {
			errf(rp+".when", "la regola di derivazione ha bisogno di una condizione")
		}
		cond(rp+".when", r.When, math.MaxInt)
		if r.Kind != "" && r.Kind != "prescription" && r.Kind != "requirement" {
			errf(rp+".kind", "tipo %q non ammesso (prescription o requirement)", r.Kind)
		}
		if r.Kind != "requirement" && (r.Title != "" || r.Method != "" || r.Check != "") {
			errf(rp+".kind", "titolo, metodo e controllo valgono solo per le regole che generano un requisito")
		}
		if r.Method != "" && r.Method != "manual" && r.Method != "evidence" {
			errf(rp+".method", "metodo %q non ammesso (manual o evidence)", r.Method)
		}
		if r.Method == "evidence" && strings.TrimSpace(r.Check) == "" {
			errf(rp+".check", "la verifica da evidenza ha bisogno della chiave del controllo")
		}
		if len(r.Title) > 140 {
			errf(rp+".title", "titolo troppo lungo (massimo 140 caratteri)")
		}
		if t := strings.TrimSpace(r.Text); t == "" {
			errf(rp+".text", "il testo della prescrizione o del requisito è obbligatorio")
		} else if len(t) > 2000 {
			errf(rp+".text", "testo troppo lungo (massimo 2000 caratteri)")
		}
	}
	if sc := d.Scoring; sc != nil {
		if len(sc.Thresholds) == 0 {
			errf("scoring.thresholds", "servono delle soglie")
		}
		mins := map[float64]bool{}
		low := false
		for i, t := range sc.Thresholds {
			tp := fmt.Sprintf("scoring.thresholds[%d]", i)
			if t.Min < 0 || t.Min > 100 {
				errf(tp+".min", "la soglia è una percentuale tra 0 e 100")
			}
			if mins[t.Min] {
				errf(tp+".min", "soglia del %v%% ripetuta", t.Min)
			}
			mins[t.Min] = true
			low = low || t.Min == 0
			if !keyRe.MatchString(t.Outcome) {
				errf(tp+".outcome", "chiave dell'esito %q non valida", t.Outcome)
			}
		}
		if len(sc.Thresholds) > 0 && !low {
			warn("scoring.thresholds", "sotto la soglia più bassa non viene proposto nessun esito: aggiungi una soglia da 0%%")
		}
		scored := false
		for _, s := range d.Sections {
			for i := range s.Questions {
				if _, m := s.Questions[i].points(nil); m > 0 {
					scored = true
				}
			}
		}
		if !scored {
			warn("scoring", "nessuna domanda ha punteggio: le soglie non scatteranno mai")
		}
	}
	return ps
}

// question cerca una domanda per chiave.
func (d *Definition) question(key string) (*Question, *Section) {
	for i := range d.Sections {
		for j := range d.Sections[i].Questions {
			if d.Sections[i].Questions[j].Key == key {
				return &d.Sections[i].Questions[j], &d.Sections[i]
			}
		}
	}
	return nil, nil
}

// NormalizeValue controlla una risposta e la porta nella forma salvata. nil = nessuna risposta.
func (q *Question) NormalizeValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch q.Type {
	case QText:
		s, ok := v.(string)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_text_required", "answers."+q.Key, "serve un testo")
		}
		s = strings.TrimSpace(s)
		if len(s) > 20000 {
			return nil, engine.TooLong("answers."+q.Key, 20000, "testo troppo lungo (massimo 20000 caratteri)")
		}
		if s == "" {
			return nil, nil
		}
		return s, nil
	case QSingle:
		s, ok := v.(string)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_choice_required", "answers."+q.Key, "serve una delle scelte")
		}
		if s == "" {
			return nil, nil
		}
		for _, o := range q.Options {
			if strings.EqualFold(o.Value, s) {
				return o.Value, nil
			}
		}
		return nil, engine.InvalidParams("validation.reviews.answer_not_choice", "answers."+q.Key, map[string]any{"value": s}, "%q non è una delle scelte", s)
	case QMulti:
		list, ok := v.([]any)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_choices_required", "answers."+q.Key, "serve un elenco di scelte")
		}
		out := []string{}
		for _, x := range list {
			s, _ := x.(string)
			found := false
			for _, o := range q.Options {
				if strings.EqualFold(o.Value, s) {
					if !slices.Contains(out, o.Value) {
						out = append(out, o.Value)
					}
					found = true
				}
			}
			if !found {
				return nil, engine.InvalidParams("validation.reviews.answer_not_choice", "answers."+q.Key, map[string]any{"value": s}, "%q non è una delle scelte", s)
			}
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case QYesNo:
		b, ok := v.(bool)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_yesno", "answers."+q.Key, "serve sì o no")
		}
		return b, nil
	case QNumber:
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, engine.InvalidCode("validation.reviews.answer_number", "answers."+q.Key, "serve un numero")
		}
		if q.Min != nil && n < *q.Min {
			return nil, engine.InvalidParams("validation.reviews.answer_min", "answers."+q.Key, map[string]any{"min": *q.Min}, "il minimo è %v", *q.Min)
		}
		if q.Max != nil && n > *q.Max {
			return nil, engine.InvalidParams("validation.reviews.answer_max", "answers."+q.Key, map[string]any{"max": *q.Max}, "il massimo è %v", *q.Max)
		}
		return n, nil
	case QDate:
		s, ok := v.(string)
		if !ok {
			return nil, engine.DateInvalid("answers."+q.Key, "serve una data AAAA-MM-GG")
		}
		if s == "" {
			return nil, nil
		}
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return nil, engine.DateInvalid("answers."+q.Key, "serve una data AAAA-MM-GG")
		}
		return s, nil
	case QScale:
		n, ok := v.(float64)
		lo, hi := q.scaleRange()
		if !ok || math.IsNaN(n) || n != math.Trunc(n) {
			return nil, engine.InvalidCode("validation.reviews.answer_integer", "answers."+q.Key, "serve un numero intero")
		}
		if n < lo || n > hi {
			return nil, engine.InvalidParams("validation.reviews.answer_range", "answers."+q.Key, map[string]any{"min": lo, "max": hi}, "il valore va da %v a %v", lo, hi)
		}
		return n, nil
	case QMatrix:
		m, ok := v.(map[string]any)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_row_choice", "answers."+q.Key, "serve una scelta per riga")
		}
		out := map[string]any{}
		for r, x := range m {
			c, _ := x.(string)
			if c == "" {
				continue
			}
			row, col := "", ""
			for _, o := range q.Rows {
				if strings.EqualFold(o.Value, r) {
					row = o.Value
				}
			}
			for _, o := range q.Options {
				if strings.EqualFold(o.Value, c) {
					col = o.Value
				}
			}
			if row == "" || col == "" {
				return nil, engine.InvalidParams("validation.reviews.answer_unknown_cell", "answers."+q.Key, map[string]any{"row": r, "column": c}, "riga o scelta sconosciuta: %q, %q", r, c)
			}
			out[row] = col
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case QAttachment:
		list, ok := v.([]any)
		if !ok {
			return nil, engine.InvalidCode("validation.reviews.answer_attachments_required", "answers."+q.Key, "serve un elenco di allegati")
		}
		out := []string{}
		for _, x := range list {
			s, ok := x.(string)
			if !ok || !uuidRe.MatchString(s) {
				return nil, engine.InvalidCode("validation.reviews.answer_attachment_invalid", "answers."+q.Key, "allegato non valido")
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	return nil, engine.InvalidCode("validation.reviews.answer_type_unknown", "answers."+q.Key, "tipo di domanda sconosciuto")
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// answerFacts trasforma una risposta nei valori di un fatto per le condizioni: le scelte
// valgono per valore ed etichetta, gli allegati per numero.
func (q *Question) answerFacts(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		if q.Type == QSingle {
			for _, o := range q.Options {
				if o.Value == x {
					return []any{x, o.Label}
				}
			}
		}
		return []any{x}
	case map[string]any:
		out := []any{}
		for r, c := range x {
			out = append(out, fmt.Sprintf("%s.%v", r, c))
		}
		slices.SortFunc(out, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
		return out
	case []string:
		if q.Type == QAttachment {
			return []any{float64(len(x))}
		}
		out := []any{}
		for _, s := range x {
			out = append(out, s)
		}
		return out
	case []any:
		if q.Type == QAttachment {
			return []any{float64(len(x))}
		}
		return x
	default:
		return []any{x}
	}
}

// prefill ricava la risposta proposta dai valori di un fatto.
func (q *Question) prefill(vals []any) any {
	if len(vals) == 0 {
		return nil
	}
	matchOption := func(v any) (string, bool) {
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		for _, o := range q.Options {
			if strings.EqualFold(o.Value, s) || strings.EqualFold(o.Label, s) {
				return o.Value, true
			}
		}
		return "", false
	}
	switch q.Type {
	case QYesNo:
		for _, v := range vals {
			switch x := v.(type) {
			case bool:
				return x
			case string:
				switch strings.ToLower(x) {
				case "sì", "si", "yes", "true":
					return true
				case "no", "false":
					return false
				}
			}
		}
	case QSingle:
		for _, v := range vals {
			if o, ok := matchOption(v); ok {
				return o
			}
		}
	case QMulti:
		out := []string{}
		for _, v := range vals {
			if o, ok := matchOption(v); ok && !slices.Contains(out, o) {
				out = append(out, o)
			}
		}
		if len(out) > 0 {
			return out
		}
	case QNumber, QScale:
		for _, v := range vals {
			if n, ok := v.(float64); ok {
				return n
			}
		}
	case QDate:
		for _, v := range vals {
			if s, ok := v.(string); ok {
				if _, err := time.Parse(time.DateOnly, s); err == nil {
					return s
				}
			}
		}
	case QText:
		parts := []string{}
		for _, v := range vals {
			parts = append(parts, fmt.Sprint(v))
		}
		return strings.Join(parts, ", ")
	}
	return nil
}

// Evaluation è lo stato del questionario: cosa si vede, cosa manca, cosa propongono le regole.
type Evaluation struct {
	Sections map[string]SectionState `json:"sections"`
	Visible  map[string]bool         `json:"visible"` // domanda → visibile
	Total    int                     `json:"total"`   // domande visibili
	Answered int                     `json:"answered"`
	Required int                     `json:"required"` // obbligatorie visibili
	Missing  []string                `json:"missing"`  // obbligatorie visibili senza risposta
	Derived  []Derivation            `json:"derived"`  // prescrizioni proposte dalle risposte
	// Requisiti proposti dalle risposte (regole di tipo "requirement"): diventano requisiti
	// veri alla chiusura della valutazione.
	DerivedRequirements []Derivation `json:"derived_requirements"`
	FactsUsed           engine.Facts `json:"-"`
	// Punteggio: somma pesata dei punti delle domande visibili che ne hanno, sul massimo
	// ottenibile. Le domande visibili senza risposta valgono zero. Scored è falso se
	// nessuna domanda visibile ha punteggio.
	Scored   bool    `json:"scored"`
	Score    float64 `json:"score"`
	MaxScore float64 `json:"max_score"`
	Percent  float64 `json:"percent"`
	// Proposed è l'esito proposto dalle soglie (vuoto se non ce n'è).
	Proposed string `json:"proposed_outcome,omitempty"`
}

// SectionState è l'avanzamento di una sezione.
type SectionState struct {
	Visible  bool `json:"visible"`
	Total    int  `json:"total"`
	Answered int  `json:"answered"`
	Missing  int  `json:"missing"`
}

// scaleRange restituisce gli estremi della scala (default 1-5).
func (q *Question) scaleRange() (float64, float64) {
	lo, hi := 1.0, 5.0
	if q.Min != nil {
		lo = *q.Min
	}
	if q.Max != nil {
		hi = *q.Max
	}
	return lo, hi
}

func pts(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func maxScore(opts []Option) (best float64, has bool) {
	for _, o := range opts {
		if o.Score != nil {
			if !has || *o.Score > best {
				best = *o.Score
			}
			has = true
		}
	}
	return best, has
}

// points restituisce i punti ottenuti con la risposta v (nil = nessuna) e il massimo
// ottenibile dalla domanda, prima del peso. Massimo 0 = la domanda non ha punteggio.
func (q *Question) points(v any) (got, max float64) {
	switch q.Type {
	case QYesNo:
		if q.YesScore == nil && q.NoScore == nil {
			return 0, 0
		}
		max = math.Max(0, math.Max(pts(q.YesScore), pts(q.NoScore)))
		if b, ok := v.(bool); ok {
			got = pts(q.NoScore)
			if b {
				got = pts(q.YesScore)
			}
		}
	case QSingle:
		best, has := maxScore(q.Options)
		if !has {
			return 0, 0
		}
		max = math.Max(0, best)
		if s, ok := v.(string); ok {
			for _, o := range q.Options {
				if o.Value == s {
					got = pts(o.Score)
				}
			}
		}
	case QMulti:
		_, has := maxScore(q.Options)
		if !has {
			return 0, 0
		}
		for _, o := range q.Options {
			max += math.Max(0, pts(o.Score))
		}
		for _, s := range stringsOf(v) {
			for _, o := range q.Options {
				if o.Value == s {
					got += pts(o.Score)
				}
			}
		}
	case QScale:
		lo, hi := q.scaleRange()
		max = hi - lo
		if n, ok := v.(float64); ok {
			got = n - lo
			if q.Reverse {
				got = hi - n
			}
		}
	case QMatrix:
		best, has := maxScore(q.Options)
		if !has || len(q.Rows) == 0 {
			return 0, 0
		}
		max = math.Max(0, best) * float64(len(q.Rows))
		if m, ok := v.(map[string]any); ok {
			for _, r := range q.Rows {
				c, _ := m[r.Value].(string)
				for _, o := range q.Options {
					if o.Value == c {
						got += pts(o.Score)
					}
				}
			}
		}
	}
	return got, max
}

func stringsOf(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := []string{}
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// complete: una risposta c'è; per la matrice servono tutte le righe.
func (q *Question) complete(v any) bool {
	if v == nil {
		return false
	}
	if q.Type == QMatrix {
		m, _ := v.(map[string]any)
		return len(q.Rows) > 0 && len(m) >= len(q.Rows)
	}
	return true
}

// proposedOutcome sceglie l'esito della soglia più alta raggiunta.
func (sc *Scoring) proposedOutcome(percent float64) string {
	best, out := -1.0, ""
	for _, t := range sc.Thresholds {
		if percent >= t.Min && t.Min > best {
			best, out = t.Min, t.Outcome
		}
	}
	return out
}

func cond(src string) engine.Condition {
	c, err := engine.ParseCondition(src)
	if err != nil {
		// Le versioni pubblicate sono validate; per prudenza una condizione illeggibile non è mai vera.
		return engine.MustCondition("__condizione_non_valida: presente")
	}
	return c
}

// Evaluate valuta il questionario nell'ordine di compilazione: ogni condizione vede i
// fatti dell'elemento e le risposte visibili che vengono prima. Le risposte alle domande
// nascoste non contano (né per le condizioni né per le derivazioni). Se una domanda ha
// la stessa chiave di un fatto, vale la risposta.
func Evaluate(d *Definition, facts engine.Facts, answers map[string]any) *Evaluation {
	f := engine.Facts{}
	for k, v := range facts {
		f[k] = v
	}
	ev := &Evaluation{Sections: map[string]SectionState{}, Visible: map[string]bool{}, Missing: []string{}, Derived: []Derivation{}, DerivedRequirements: []Derivation{}}
	for _, s := range d.Sections {
		st := SectionState{Visible: s.VisibleWhen == "" || cond(s.VisibleWhen).Eval(f)}
		for i := range s.Questions {
			q := &s.Questions[i]
			vis := st.Visible && (q.VisibleWhen == "" || cond(q.VisibleWhen).Eval(f))
			ev.Visible[q.Key] = vis
			if !vis {
				continue // vale l'eventuale fatto omonimo, non la risposta nascosta
			}
			st.Total++
			v, ok := answers[q.Key]
			answered := ok && q.complete(v)
			if ok && v != nil {
				f.Set(q.Key, q.answerFacts(v)...)
			}
			if answered {
				st.Answered++
			}
			if got, max := q.points(v); max > 0 {
				w := 1.0
				if q.Weight != nil {
					w = *q.Weight
				}
				if w > 0 {
					ev.Scored = true
					ev.Score += w * got
					ev.MaxScore += w * max
				}
			}
			if q.Required && !answered {
				st.Missing++
				ev.Missing = append(ev.Missing, q.Key)
			}
			if q.Required {
				ev.Required++
			}
		}
		ev.Sections[s.Key] = st
		ev.Total += st.Total
		ev.Answered += st.Answered
	}
	for _, r := range d.Derivations {
		if cond(r.When).Eval(f) {
			if r.Kind == "requirement" {
				if r.Category == "" {
					r.Category = "Requisito"
				}
				if r.Method == "" {
					r.Method = "manual"
				}
				ev.DerivedRequirements = append(ev.DerivedRequirements, r)
				continue
			}
			ev.Derived = append(ev.Derived, r)
		}
	}
	if ev.Scored && ev.MaxScore > 0 {
		ev.Percent = math.Round(ev.Score/ev.MaxScore*1000) / 10
		if d.Scoring != nil {
			ev.Proposed = d.Scoring.proposedOutcome(ev.Percent)
		}
	}
	ev.FactsUsed = f
	return ev
}

// Highlights sono le domande da guardare con attenzione quando si compila (e quando si accetta
// una proposta dell'AI): «score» se la risposta cambia il punteggio, «rules» se una regola di
// derivazione la usa (genera prescrizioni o requisiti).
func (d *Definition) Highlights() map[string][]string {
	out := map[string][]string{}
	used := map[string]bool{}
	for _, r := range d.Derivations {
		for _, k := range cond(r.When).Keys() {
			used[strings.ToLower(k)] = true
		}
	}
	for _, s := range d.Sections {
		for i := range s.Questions {
			q := &s.Questions[i]
			w := 1.0
			if q.Weight != nil {
				w = *q.Weight
			}
			if _, max := q.points(nil); max > 0 && w > 0 {
				out[q.Key] = append(out[q.Key], "score")
			}
			if used[strings.ToLower(q.Key)] {
				out[q.Key] = append(out[q.Key], "rules")
			}
		}
	}
	return out
}
