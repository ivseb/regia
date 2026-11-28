package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Problem è un errore o un avviso nella definizione di un processo. Path indica il
// punto (es. "transitions[3].to") per l'editor.
type Problem struct {
	Level string `json:"level"` // error | warning
	Path  string `json:"path"`
	Msg   string `json:"message"`
	// Code è il code specifico (validation.rule.xxx) per gli errori delle regole: il client lo traduce.
	Code string `json:"code,omitempty"`
}

// HasErrors dice se ci sono errori bloccanti.
func HasErrors(ps []Problem) bool {
	return slices.ContainsFunc(ps, func(p Problem) bool { return p.Level == "error" })
}

var (
	gateKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	colorRe   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

var categories = map[string]bool{"todo": true, "in_progress": true, "done": true, "cancelled": true}

// DecodeDefinition legge una definizione JSON rifiutando i campi sconosciuti
// (un errore di battitura in un campo non deve sparire in silenzio).
func DecodeDefinition(raw []byte) (*Workflow, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	w := &Workflow{}
	if err := dec.Decode(w); err != nil {
		return nil, InvalidValue("definition", "JSON non valido: %v", err)
	}
	return w, nil
}

// Validate controlla la coerenza interna della definizione (senza database).
func Validate(w *Workflow) []Problem {
	ps := []Problem{}
	add := func(level, path, format string, args ...any) {
		ps = append(ps, Problem{Level: level, Path: path, Msg: fmt.Sprintf(format, args...)})
	}
	errf := func(path, format string, args ...any) { add("error", path, format, args...) }
	errc := func(code, path, format string, args ...any) {
		ps = append(ps, Problem{Level: "error", Path: path, Msg: fmt.Sprintf(format, args...), Code: code})
	}
	warn := func(path, format string, args ...any) { add("warning", path, format, args...) }
	cond := func(path, src string) {
		if src == "" {
			return
		}
		if _, err := ParseCondition(src); err != nil {
			errf(path, "condizione non valida: %v", err)
		}
	}

	if len(w.Phases) == 0 {
		errf("phases", "serve almeno una fase")
	}
	phases := map[string]Phase{}
	for i, p := range w.Phases {
		path := fmt.Sprintf("phases[%d]", i)
		name := strings.TrimSpace(p.Name)
		switch {
		case name == "":
			errf(path+".name", "nome della fase obbligatorio")
		case len(name) > 80:
			errf(path+".name", "nome troppo lungo (massimo 80 caratteri)")
		case strings.ContainsAny(name, ",:\""):
			errf(path+".name", "il nome %q non può contenere virgole, due punti o virgolette", name)
		}
		if _, dup := phases[strings.ToLower(name)]; dup {
			errf(path+".name", "fase %q ripetuta", name)
		}
		phases[strings.ToLower(name)] = p
		if !categories[p.Category] {
			errf(path+".category", "categoria %q non ammessa (todo, in_progress, done, cancelled)", p.Category)
		}
		if p.Color != "" && !colorRe.MatchString(p.Color) {
			errf(path+".color", "colore %q non valido (formato #RRGGBB)", p.Color)
		}
		if msg := labelProblem(p.Labels, 80); msg != "" {
			errf(path+".labels", "%s", msg)
		}
	}
	hasPhase := func(n string) bool { _, ok := phases[strings.ToLower(n)]; return ok }
	if w.Initial == "" {
		errf("initial", "manca la fase iniziale")
	} else if !hasPhase(w.Initial) {
		errf("initial", "la fase iniziale %q non esiste", w.Initial)
	}

	gates := map[string]bool{}
	for i, g := range w.Gates {
		path := fmt.Sprintf("gates[%d]", i)
		if !gateKeyRe.MatchString(g.Key) {
			errf(path+".key", "chiave %q non valida (minuscole, cifre e _)", g.Key)
		}
		if gates[g.Key] {
			errf(path+".key", "gate %q ripetuto", g.Key)
		}
		gates[g.Key] = true
		if strings.TrimSpace(g.Name) == "" {
			errf(path+".name", "nome del gate obbligatorio")
		}
		if (g.Condition == "") == (g.Check == "") {
			errf(path, "il gate %q deve avere una condizione oppure un controllo, non entrambi né nessuno", g.Key)
		}
		cond(path+".condition", g.Condition)
		if g.Message == "" {
			warn(path+".message", "il gate %q non ha un messaggio: si mostrerà il nome", g.Key)
		}
	}

	usedGates := map[string]bool{}
	type pair struct{ from, to, when string }
	seen := map[pair]int{}
	reach := map[string][]string{}
	for i, t := range w.Transitions {
		path := fmt.Sprintf("transitions[%d]", i)
		if !hasPhase(t.To) {
			errf(path+".to", "la fase di arrivo %q non esiste", t.To)
		}
		if len(t.From) == 0 {
			errf(path+".from", "indica almeno una fase di partenza (o \"*\")")
		}
		for _, f := range t.From {
			if f != "*" && !hasPhase(f) {
				errf(path+".from", "la fase di partenza %q non esiste", f)
			}
			k := pair{strings.ToLower(f), strings.ToLower(t.To), t.When}
			if j, dup := seen[k]; dup && !t.System {
				errf(path, "passaggio da %q a %q già definito in transitions[%d] con la stessa condizione", f, t.To, j)
			}
			if !t.System {
				seen[k] = i
			}
			reach[strings.ToLower(f)] = append(reach[strings.ToLower(f)], strings.ToLower(t.To))
		}
		if !t.System && strings.TrimSpace(t.Label) == "" {
			errf(path+".label", "serve l'etichetta del pulsante")
		}
		if t.System && len(t.Roles) > 0 {
			warn(path+".roles", "i ruoli non contano nei passaggi di sistema")
		}
		if len(t.Requires) > 0 {
			errf(path+".requires", "nei processi si usano i gate (\"gates\") al posto di \"requires\"")
		}
		for _, g := range t.Gates {
			usedGates[g] = true
			if !gates[g] {
				errf(path+".gates", "il gate %q non è definito", g)
			}
		}
		cond(path+".when", t.When)
	}
	for g := range gates {
		if !usedGates[g] {
			warn("gates", "il gate %q non è usato da nessun passaggio", g)
		}
	}

	// Raggiungibilità dalla fase iniziale (i passaggi da "*" valgono per tutte le fasi non terminali).
	if w.Initial != "" && hasPhase(w.Initial) {
		visited := map[string]bool{strings.ToLower(w.Initial): true}
		queue := []string{strings.ToLower(w.Initial)}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			next := slices.Clone(reach[cur])
			if p := phases[cur]; p.Category != "done" && p.Category != "cancelled" {
				next = append(next, reach["*"]...)
			}
			for _, n := range next {
				if !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
		}
		for _, p := range w.Phases {
			if !visited[strings.ToLower(p.Name)] {
				warn("phases", "la fase %q non è raggiungibile dalla fase iniziale", p.Name)
			}
		}
	}

	ruleKeys := map[string]bool{}
	for i, r := range w.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		if !gateKeyRe.MatchString(r.Key) {
			errc("validation.rule.key_invalid", path+".key", "chiave %q non valida (minuscole, cifre e _)", r.Key)
		}
		if ruleKeys[r.Key] {
			errc("validation.rule.key_duplicate", path+".key", "regola %q ripetuta", r.Key)
		}
		ruleKeys[r.Key] = true
		ev, err := ParseRuleEvent(r.On)
		switch {
		case err != nil:
			errc("validation.rule.event_invalid", path+".on", "evento %q non valido: %v", r.On, err)
		case ev.Kind == "enter" || ev.Kind == "exit":
			if !hasPhase(ev.Phase) {
				errc("validation.rule.phase_unknown", path+".on", "la fase %q non esiste", ev.Phase)
			}
		}
		ruleCond := func(p, src string) {
			if src == "" {
				return
			}
			if _, err := ParseCondition(src); err != nil {
				errc("validation.rule.condition_invalid", p, "condizione non valida: %v", err)
			}
		}
		ruleCond(path+".when", r.When)
		if len(r.Actions) == 0 {
			errc("validation.rule.no_actions", path+".actions", "la regola non fa nulla: aggiungi almeno un'azione")
		}
		for j, a := range r.Actions {
			ap := fmt.Sprintf("%s.actions[%d]", path, j)
			switch a.Kind {
			case "assign_role":
				if a.Role == "" {
					errc("validation.rule.role_required", ap+".role", "indica il ruolo da assegnare")
				}
				if (a.Group == "") == (a.Member == "") {
					errc("validation.rule.target_required", ap, "indica un gruppo oppure un membro")
				}
			case "":
				errc("validation.rule.kind_required", ap+".kind", "indica il tipo di azione")
			default:
				// Le azioni dei moduli si controllano in validateRefs, che conosce le registrazioni.
				if !gateKeyRe.MatchString(a.Kind) {
					errc("validation.rule.kind_invalid", ap+".kind", "tipo di azione %q non valido", a.Kind)
				}
			}
			ruleCond(ap+".when", a.When)
		}
	}
	return ps
}

// validateRefs controlla i riferimenti ai dati del tenant: ruoli, gruppi, controlli,
// politiche di approvazione.
func (s *Service) validateRefs(ctx context.Context, tx pgx.Tx, w *Workflow, typeID string) ([]Problem, error) {
	ps := []Problem{}
	rows, err := tx.Query(ctx, "select key from stakeholder_roles")
	if err != nil {
		return nil, err
	}
	roleKeys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for i, t := range w.Transitions {
		for _, r := range t.Roles {
			if !slices.Contains(roleKeys, r) {
				ps = append(ps, Problem{"error", fmt.Sprintf("transitions[%d].roles", i), fmt.Sprintf("ruolo %q inesistente", r), ""})
			}
		}
		if t.Approval != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from approval_policies where active and lower(stage) = lower($1)
				and (type_id = $2 or type_id is null))`, t.To, typeID).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				ps = append(ps, Problem{"error", fmt.Sprintf("transitions[%d].approval", i),
					fmt.Sprintf("nessuna politica di approvazione attiva per la fase %q", t.To), ""})
			}
		}
	}
	// Azioni e controlli già presenti in una versione pubblicata dei processi del tipo erano
	// validi alla loro pubblicazione: si accettano anche se questo motore non li conosce.
	// Succede nel provisioning, dove ogni modulo valida con un motore che ha solo le sue
	// registrazioni; nel server tutti i moduli sono registrati e la regola non cambia nulla.
	knownActions, knownGuards, err := publishedRefs(ctx, tx, typeID)
	if err != nil {
		return nil, err
	}
	for i, g := range w.Gates {
		if g.Check != "" && !knownGuards[g.Check] {
			if _, ok := s.hooks.Guards[g.Check]; !ok {
				ps = append(ps, Problem{"error", fmt.Sprintf("gates[%d].check", i), fmt.Sprintf("controllo %q non disponibile", g.Check), ""})
			}
		}
	}
	var dates []DateSource
	for i, r := range w.Rules {
		ev, _ := ParseRuleEvent(r.On)
		switch {
		case ev.Kind == "change" && ev.Field != "titolo" && typeID != "":
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from type_fields tf join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
				where tf.type_id = $1 and tf.space_id is null and f.key = $2)`, typeID, ev.Field).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				ps = append(ps, Problem{"error", fmt.Sprintf("rules[%d].on", i), fmt.Sprintf("il campo %q non è del tipo", ev.Field), "validation.rule.field_unknown"})
			}
		case ev.Kind == "at" && typeID != "":
			if dates == nil {
				var err error
				if dates, err = s.DateSources(ctx, tx, typeID); err != nil {
					return nil, err
				}
			}
			if _, ok := findDate(dates, ev.Date); !ok {
				ps = append(ps, Problem{"error", fmt.Sprintf("rules[%d].on", i), fmt.Sprintf("la data %q non è del tipo", ev.Date), "validation.rule.date_unknown"})
			}
		}
		for j, a := range r.Actions {
			ap := fmt.Sprintf("rules[%d].actions[%d]", i, j)
			if a.Role != "" && !slices.Contains(roleKeys, a.Role) {
				ps = append(ps, Problem{"error", ap + ".role", fmt.Sprintf("ruolo %q inesistente", a.Role), "validation.rule.role_unknown"})
			}
			if a.Group != "" {
				var ok bool
				if err := tx.QueryRow(ctx, "select exists (select 1 from groups where lower(name) = lower($1))", a.Group).Scan(&ok); err != nil {
					return nil, err
				}
				if !ok {
					ps = append(ps, Problem{"error", ap + ".group", fmt.Sprintf("gruppo %q inesistente", a.Group), "validation.rule.group_unknown"})
				}
			}
			if automationKinds[a.Kind] {
				if missing := s.missingParams(a); len(missing) > 0 {
					for _, f := range missing {
						ps = append(ps, Problem{"error", ap + "." + f.Key, fmt.Sprintf("manca %q: %s", f.Key, f.Name), "validation.rule.param_required"})
					}
				} else {
					msgs, err := s.checkAutomation(ctx, tx, typeID, a)
					if err != nil {
						return nil, err
					}
					for _, m := range msgs {
						ps = append(ps, Problem{"error", ap, m, "validation.rule.param_invalid"})
					}
				}
			} else if a.Kind != "assign_role" && a.Kind != "" {
				d, ok := s.hooks.Actions[a.Kind]
				if !ok && knownActions[a.Kind] {
					continue
				}
				if !ok {
					ps = append(ps, Problem{"error", ap + ".kind", fmt.Sprintf("azione %q non disponibile (%s)", a.Kind, strings.Join(s.ActionKinds(), ", ")), "validation.rule.action_unknown"})
				} else if missing := s.missingParams(a); len(missing) > 0 {
					for _, f := range missing {
						ps = append(ps, Problem{"error", ap + "." + f.Key, fmt.Sprintf("manca %q: %s", f.Key, f.Name), "validation.rule.param_required"})
					}
				} else if d.check != nil {
					msgs, err := d.check(ctx, tx, a)
					if err != nil {
						return nil, err
					}
					for _, m := range msgs {
						ps = append(ps, Problem{"error", ap, m, "validation.rule.param_invalid"})
					}
				}
			}
			if a.Member != "" {
				var ok bool
				if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", a.Member).Scan(&ok); err != nil {
					return nil, err
				}
				if !ok {
					ps = append(ps, Problem{"error", ap + ".member", "membro inesistente o non attivo", "validation.rule.member_unknown"})
				}
			}
		}
	}
	return ps, nil
}

// Guards elenca i controlli registrati dai moduli (per l'editor dei gate).
func (s *Service) GuardNames() []string {
	out := make([]string, 0, len(s.hooks.Guards))
	for k := range s.hooks.Guards {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// publishedRefs raccoglie i tipi di azione e i controlli usati dalle versioni pubblicate
// dei processi di un tipo.
func publishedRefs(ctx context.Context, tx pgx.Tx, typeID string) (map[string]bool, map[string]bool, error) {
	actions, guards := map[string]bool{}, map[string]bool{}
	if typeID == "" {
		return actions, guards, nil
	}
	rows, err := tx.Query(ctx, `select v.definition from process_versions v
		join process_definitions p on p.tenant_id = v.tenant_id and p.id = v.process_id
		where p.type_id = $1 and v.status = 'published'`, typeID)
	if err != nil {
		return nil, nil, err
	}
	defs, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, nil, err
	}
	for _, raw := range defs {
		var w Workflow
		if json.Unmarshal(raw, &w) != nil {
			continue
		}
		for _, g := range w.Gates {
			if g.Check != "" {
				guards[g.Check] = true
			}
		}
		for _, r := range w.Rules {
			for _, a := range r.Actions {
				actions[a.Kind] = true
			}
		}
	}
	return actions, guards, nil
}

// missingParams elenca i parametri obbligatori (dallo schema registrato dall'azione) che mancano.
func (s *Service) missingParams(a Action) []ActionField {
	r := &s.editor
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ActionField
	for _, sc := range append(append([]ActionSchema{}, automationSchemas...), r.actions...) {
		if sc.Kind != a.Kind {
			continue
		}
		for _, f := range sc.Fields {
			if !f.Required {
				continue
			}
			var v string
			switch f.Key {
			case "ref":
				v = a.Ref
			case "when":
				v = a.When
			case "group":
				v = a.Group
			case "member":
				v = a.Member
			case "role":
				v = a.Role
			default:
				v = a.Params[f.Key]
			}
			if v == "" {
				out = append(out, f)
			}
		}
	}
	return out
}

// RuleCodeError restituisce l'errore con code specifico della prima regola non valida, se c'è.
func RuleCodeError(ps []Problem) error {
	for _, p := range ps {
		if p.Level == "error" && strings.HasPrefix(p.Code, "validation.rule.") {
			return invalidCode(p.Code, p.Path, "%s", p.Msg)
		}
	}
	return nil
}
