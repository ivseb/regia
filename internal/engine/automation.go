package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Automazioni (docs/sviluppo/automazioni.md): le regole dei processi reagiscono, oltre che alla
// creazione e alle fasi, al cambio di un campo, a una data dell'elemento e al calendario.
//
//	create                 l'elemento nasce
//	enter:<fase>           entra nella fase
//	exit:<fase>            esce dalla fase
//	change:<campo>         cambia il campo (chiave del campo personalizzato, o «titolo»)
//	at:<data>:<±giorni>    N giorni prima (negativo) o dopo una data dell'elemento
//	every:day              ogni giorno
//	every:week:<1-7>       ogni settimana, nel giorno indicato (1 = lunedì)
//
// Gli eventi a tempo e periodici li esegue il lavoratore (automation_job.go), una volta per
// elemento e per occorrenza (rule_runs).

// RuleEvent è l'evento di una regola, analizzato.
type RuleEvent struct {
	Kind    string `json:"kind"` // create | enter | exit | change | at | every
	Phase   string `json:"phase,omitempty"`
	Field   string `json:"field,omitempty"`
	Date    string `json:"date,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	Weekday int    `json:"weekday,omitempty"` // every: 0 = ogni giorno, 1-7 = lunedì…domenica
}

// Timed dice se l'evento lo esegue il lavoratore (a tempo o periodico).
func (e RuleEvent) Timed() bool { return e.Kind == "at" || e.Kind == "every" }

// maxOffsetDays è lo scarto massimo, in giorni, di un evento «at:».
const maxOffsetDays = 3650

// ParseRuleEvent analizza il campo «on» di una regola.
func ParseRuleEvent(on string) (RuleEvent, error) {
	kind, rest, _ := strings.Cut(strings.TrimSpace(on), ":")
	switch kind {
	case "create":
		if rest != "" {
			return RuleEvent{}, fmt.Errorf("\"create\" non ha una fase")
		}
		return RuleEvent{Kind: kind}, nil
	case "enter", "exit":
		return RuleEvent{Kind: kind, Phase: rest}, nil
	case "change":
		if !factKeyRe.MatchString(rest) {
			return RuleEvent{}, fmt.Errorf("campo %q non valido", rest)
		}
		return RuleEvent{Kind: kind, Field: rest}, nil
	case "at":
		i := strings.LastIndex(rest, ":")
		if i <= 0 {
			return RuleEvent{}, fmt.Errorf("serve at:<data>:<giorni>, es. at:scadenza:-30")
		}
		date, off := rest[:i], strings.TrimPrefix(rest[i+1:], "+")
		n, err := strconv.Atoi(off)
		if err != nil || n < -maxOffsetDays || n > maxOffsetDays {
			return RuleEvent{}, fmt.Errorf("giorni %q non validi (da -%d a %d)", rest[i+1:], maxOffsetDays, maxOffsetDays)
		}
		if !factKeyRe.MatchString(date) {
			return RuleEvent{}, fmt.Errorf("data %q non valida", date)
		}
		return RuleEvent{Kind: kind, Date: date, Offset: n}, nil
	case "every":
		if rest == "day" {
			return RuleEvent{Kind: kind}, nil
		}
		if d, ok := strings.CutPrefix(rest, "week:"); ok {
			n, err := strconv.Atoi(d)
			if err == nil && n >= 1 && n <= 7 {
				return RuleEvent{Kind: kind, Weekday: n}, nil
			}
		}
		return RuleEvent{}, fmt.Errorf("periodicità %q non valida (every:day, every:week:1…7)", rest)
	}
	return RuleEvent{}, fmt.Errorf("evento %q non valido", on)
}

var factKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)

// --- Catena delle regole: nessun ciclo ---

// Una modifica fatta da un'azione delle automazioni (un campo impostato, un elemento creato) è
// a sua volta un evento. Le regole di quell'evento scattano solo al primo livello: le modifiche
// che fanno loro non fanno scattare altro. Le azioni dei moduli (create_impact, open_review…)
// non aumentano il livello: le loro catene restano quelle di prima. In nessun caso una regola
// riscatta sullo stesso elemento dentro la propria catena.
const maxRuleDepth = 1

type chainKey struct{}

type ruleChain struct {
	depth int
	rules []string // processo/regola/elemento già eseguiti nella catena
}

func chainOf(ctx context.Context) ruleChain {
	c, _ := ctx.Value(chainKey{}).(ruleChain)
	return c
}

// rulesAllowed dice se in questo contesto le regole degli eventi possono scattare.
func rulesAllowed(ctx context.Context) bool { return chainOf(ctx).depth <= maxRuleDepth }

func (c ruleChain) has(id string) bool {
	for _, r := range c.rules {
		if r == id {
			return true
		}
	}
	return false
}

// withRule è il contesto delle azioni di una regola: la regola entra nella catena e, per le
// azioni delle automazioni (deeper), il livello sale di uno.
func withRule(ctx context.Context, id string, deeper bool) context.Context {
	c := chainOf(ctx)
	d := c.depth
	if deeper {
		d++
	}
	return context.WithValue(ctx, chainKey{}, ruleChain{depth: d, rules: append(append([]string{}, c.rules...), id)})
}

// --- Esecuzione in corso ---

// RuleRun descrive l'esecuzione di una regola: le azioni la leggono dal contesto (modelli di
// testo, storia, audit). I moduli la leggono con RunOf.
type RuleRun struct {
	Rule       Rule
	ProcessID  string
	VersionID  string
	Version    int
	Event      string
	Occurrence string
	Facts      Facts
	// System: l'esecuzione viene dal lavoratore (a tempo o periodica), senza una persona.
	System bool
	Today  string // AAAA-MM-GG nel fuso del tenant
}

type runKey struct{}

// RunOf restituisce l'esecuzione di regola in corso, se c'è.
func RunOf(ctx context.Context) (*RuleRun, bool) {
	r, ok := ctx.Value(runKey{}).(*RuleRun)
	return r, ok
}

// payload è la parte comune delle voci della storia e dell'audit di un'esecuzione.
func (r *RuleRun) payload() map[string]any {
	p := map[string]any{"rule": r.Rule.Name, "key": r.Rule.Key, "event": r.Event, "version": r.Version}
	if r.ProcessID != "" {
		p["process_id"] = r.ProcessID
	}
	if r.Occurrence != "" {
		p["occurrence"] = r.Occurrence
	}
	if r.System {
		p["system"] = true
	}
	return p
}

// versionRef legge processo e numero di una versione (per storia e audit).
func versionRef(ctx context.Context, tx pgx.Tx, versionID string) (processID string, number int, err error) {
	if versionID == "" {
		return "", 0, nil
	}
	err = tx.QueryRow(ctx, "select process_id::text, number from process_versions where id = $1", versionID).Scan(&processID, &number)
	if err == pgx.ErrNoRows {
		return "", 0, nil
	}
	return processID, number, err
}

// --- Modelli di testo ---

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

// TemplateNames sono i segnaposto fissi dei modelli di testo (l'editor li offre in un menu);
// oltre a questi valgono campo.<chiave>, prima.<chiave> e qualunque fatto dell'elemento.
var TemplateNames = []string{"titolo", "chiave", "fase", "tipo", "spazio", "data", "regola", "link"}

// Render sostituisce i segnaposto {{…}} del modello con i dati dell'elemento. Un segnaposto
// sconosciuto diventa vuoto: il testo resta leggibile.
func Render(tpl string, el *Element, f Facts, run *RuleRun) string {
	return placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := strings.ToLower(placeholderRe.FindStringSubmatch(m)[1])
		switch name {
		case "titolo":
			return el.Title
		case "chiave":
			return el.Key
		case "fase", "stato":
			return el.State
		case "tipo":
			return el.TypeName
		case "spazio":
			return el.SpaceKey
		case "link":
			return "/e/" + el.Key
		case "data", "oggi":
			if run != nil && run.Today != "" {
				return run.Today
			}
			return time.Now().Format(time.DateOnly)
		case "regola":
			if run != nil {
				return run.Rule.Name
			}
			return ""
		}
		name = strings.TrimPrefix(name, "campo.")
		vals := f[name]
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			parts = append(parts, formatFact(v))
		}
		return strings.Join(parts, ", ")
	})
}

// --- Cambio di campo ---

// changedFields confronta i campi prima e dopo una modifica (stesso valore salvato = nessun cambio).
func changedFields(before, after map[string]any, keys []string) []string {
	var out []string
	for _, k := range keys {
		if !sameValue(before[k], after[k]) {
			out = append(out, k)
		}
	}
	return out
}

func sameValue(a, b any) bool {
	if isEmptyValue(a) && isEmptyValue(b) {
		return true
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) == string(jb) {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// withPrevious aggiunge ai fatti dell'elemento i valori precedenti dei campi cambiati, come
// prima.<chiave> (e prima.<chiave>.<sotto>, per esempio la valuta degli importi).
func withPrevious(f, before Facts, fields []string) Facts {
	out := Facts{}
	for k, v := range f {
		out[k] = v
	}
	for _, field := range fields {
		for k, v := range before {
			if k == field || strings.HasPrefix(k, field+".") {
				out["prima."+k] = v
			}
		}
		if _, ok := before[field]; !ok {
			out["prima."+field] = nil
		}
	}
	return out
}

// runChangeRules esegue le regole «change:<campo>» dei campi cambiati da una modifica.
func (s *Service) runChangeRules(ctx context.Context, tx pgx.Tx, actor Actor, before, after *Element, fields []string) error {
	if len(fields) == 0 || !rulesAllowed(ctx) {
		return nil
	}
	fl, err := s.flowOf(ctx, tx, after)
	if err != nil {
		return err
	}
	var events []string
	for _, r := range fl.w.Rules {
		ev, err := ParseRuleEvent(r.On)
		if err != nil || ev.Kind != "change" {
			continue
		}
		for _, f := range fields {
			if ev.Field == f {
				events = append(events, r.On)
				break
			}
		}
	}
	if len(events) == 0 {
		return nil
	}
	prev, err := s.ElementFacts(ctx, tx, before)
	if err != nil {
		return err
	}
	prev.Set("titolo", before.Title)
	cur, err := s.ElementFacts(ctx, tx, after)
	if err != nil {
		return err
	}
	cur.Set("titolo", after.Title)
	facts := withPrevious(cur, prev, fields)
	done := map[string]bool{}
	for _, ev := range events {
		if done[ev] {
			continue
		}
		done[ev] = true
		if err := s.runRulesWith(ctx, tx, actor, after, fl.w, ev, nil, &RuleRun{Event: ev, Facts: facts}); err != nil {
			return err
		}
	}
	return nil
}
