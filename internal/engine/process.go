package engine

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
)

// FactsFunc aggiunge ai fatti di un elemento quelli del modulo (es. stima e driver della domanda).
type FactsFunc func(ctx context.Context, tx pgx.Tx, el *Element, f Facts) error

// Facts registra il fornitore di fatti di un modulo per un tipo di elemento.
func (s *Service) Facts(typeKey string, fn FactsFunc) {
	s.hooks.Facts[typeKey] = append(s.hooks.Facts[typeKey], fn)
}

// ElementFacts raccoglie i fatti su cui si valutano condizioni, gate e regole:
//   - tipo, stato, categoria, spazio;
//   - i campi personalizzati, per chiave (opzioni, persone e gruppi per nome, elementi per
//     chiave e titolo, importi in unità con la valuta in <chiave>.valuta);
//   - i ruoli sull'elemento: ruolo.<chiave> = nomi dei titolari;
//   - i fatti del modulo del tipo.
func (s *Service) ElementFacts(ctx context.Context, tx pgx.Tx, el *Element) (Facts, error) {
	f := Facts{}
	f.Set("tipo", el.TypeKey, el.TypeName)
	f.Set("stato", el.State)
	f.Set("fase", el.State) // stesso fatto, col nome usato nelle condizioni di visibilità dei campi
	f.Set("categoria", el.StateCategory)
	f.Set("spazio", el.SpaceKey)
	if len(el.Fields) > 0 {
		defs, err := loadTypeFields(ctx, tx, el.TypeID)
		if err != nil {
			return nil, err
		}
		if err := fieldFacts(ctx, tx, defs, el.Fields, f); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `
		select r.key, coalesce(m.display_name, g.name, '')
		from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		where sh.element_id = $1`, el.ID)
	if err != nil {
		return nil, err
	}
	roles := map[string][]any{}
	var rk, name string
	if _, err := pgx.ForEachRow(rows, []any{&rk, &name}, func() error {
		roles[rk] = append(roles[rk], name)
		return nil
	}); err != nil {
		return nil, err
	}
	for k, v := range roles {
		f.Set("ruolo."+k, v...)
	}
	for _, fn := range s.hooks.Facts[el.TypeKey] {
		if err := fn(ctx, tx, el, f); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// lazyFacts calcola i fatti solo se servono, una volta.
type lazyFacts struct {
	s  *Service
	el *Element
	f  Facts
	// defs: campi del tipo, letti una volta (gate implicito dei campi obbligatori).
	defs       []FieldDef
	defsLoaded bool
}

func (l *lazyFacts) fieldDefs(ctx context.Context, tx pgx.Tx) ([]FieldDef, error) {
	if !l.defsLoaded {
		defs, err := loadTypeFields(ctx, tx, l.el.TypeID)
		if err != nil {
			return nil, err
		}
		l.defs, l.defsLoaded = defs, true
	}
	return l.defs, nil
}

func (l *lazyFacts) get(ctx context.Context, tx pgx.Tx) (Facts, error) {
	if l.f != nil {
		return l.f, nil
	}
	var err error
	l.f, err = l.s.ElementFacts(ctx, tx, l.el)
	return l.f, err
}

// StartProcess sceglie il processo dell'elemento (il primo per priorità la cui
// condizione di applicabilità è vera), lo fissa alla versione corrente, porta
// l'elemento nella fase iniziale ed esegue le regole "create". Senza processi per il
// tipo non fa nulla: resta il workflow semplice.
func (s *Service) StartProcess(ctx context.Context, tx pgx.Tx, actor Actor, el *Element) (*Element, error) {
	// Una versione la cui data di entrata in vigore è arrivata vale subito, anche se il lavoratore periodico non è ancora passato.
	if _, err := s.ActivateDue(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select p.id, p.name, p.applies_when, v.id, v.number
		from process_definitions p
		join process_versions v on v.tenant_id = p.tenant_id and v.process_id = p.id and v.status = 'published'
		where p.type_id = $1 and not p.archived
		order by p.priority, p.key`, el.TypeID)
	if err != nil {
		return nil, err
	}
	type cand struct {
		id, name, when, versionID string
		number                    int
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
		var c cand
		err := r.Scan(&c.id, &c.name, &c.when, &c.versionID, &c.number)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return el, nil
	}
	lf := &lazyFacts{s: s, el: el}
	var chosen *cand
	for i, c := range cands {
		if c.when == "" {
			chosen = &cands[i]
			break
		}
		f, err := lf.get(ctx, tx)
		if err != nil {
			return nil, err
		}
		cond, err := ParseCondition(c.when)
		if err != nil {
			return nil, InvalidValue("process", "il processo %q ha una condizione di applicabilità non valida: %v", c.name, err)
		}
		if cond.Eval(f) {
			chosen = &cands[i]
			break
		}
	}
	if chosen == nil {
		return nil, invalid("process", "nessun processo si applica a questo elemento: chiedi all'amministratore di processo")
	}
	t, err := loadTypeByID(ctx, tx, el.TypeID)
	if err != nil {
		return nil, err
	}
	w, err := loadVersion(ctx, tx, chosen.versionID)
	if err != nil {
		return nil, err
	}
	initial, ok := findState(phaseStates(w, t.options), w.Initial)
	if !ok {
		return nil, invalid("process", "il processo %q non ha una fase iniziale valida", chosen.name)
	}
	if _, err := tx.Exec(ctx, `update elements set process_version_id = $2, state_id = $3 where id = $1`,
		el.ID, chosen.versionID, initial.ID); err != nil {
		return nil, err
	}
	el.ProcessVersionID, el.StateID, el.State, el.StateCategory = &chosen.versionID, &initial.ID, initial.Name, initial.Category
	if err := addActivity(ctx, tx, el.ID, actor.MemberID, "process", "",
		map[string]any{"process": chosen.name, "version": chosen.number, "state": initial.Name}, nil); err != nil {
		return nil, err
	}
	if err := s.runRules(ctx, tx, actor, el, w, "create"); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}, el.ID)
}

// GateResult è l'esito di un gate per un passaggio.
type GateResult struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// checkGates valuta i gate di un passaggio (e i controlli "requires" dei workflow semplici).
func (s *Service) checkGates(ctx context.Context, tx pgx.Tx, el *Element, w *Workflow, tr Transition, lf *lazyFacts) ([]GateResult, error) {
	out := []GateResult{}
	for _, key := range tr.Gates {
		g, ok := w.gate(key)
		if !ok {
			out = append(out, GateResult{Key: key, Name: key, Message: "gate non definito nel processo"})
			continue
		}
		r := GateResult{Key: g.Key, Name: g.Name, Passed: true}
		if g.Condition != "" {
			f, err := lf.get(ctx, tx)
			if err != nil {
				return nil, err
			}
			r.Passed = w.cond(g.Condition).Eval(f)
		}
		if r.Passed && g.Check != "" {
			fn, ok := s.hooks.Guards[g.Check]
			if !ok {
				r.Passed = false
				r.Message = "controllo " + g.Check + " non disponibile"
			} else if err := fn(ctx, tx, el); err != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					return nil, err
				}
				r.Passed, r.Message = false, ve.Msg
			}
		}
		if !r.Passed && r.Message == "" {
			r.Message = g.Message
			if r.Message == "" {
				r.Message = "condizione non soddisfatta: " + g.Name
			}
		}
		out = append(out, r)
	}
	for _, g := range tr.Requires {
		r := GateResult{Key: g, Name: g, Passed: true}
		fn, ok := s.hooks.Guards[g]
		if !ok {
			r.Passed, r.Message = false, "controllo "+g+" non disponibile"
		} else if err := fn(ctx, tx, el); err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				return nil, err
			}
			r.Passed, r.Message = false, ve.Msg
		}
		out = append(out, r)
	}
	return out, nil
}

// runRules esegue le regole dell'evento ("create", "enter:<fase>", "exit:<fase>").
func (s *Service) runRules(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, w *Workflow, event string) error {
	return s.runRulesOf(ctx, tx, actor, el, w, event, nil)
}

// RerunRules esegue di nuovo le regole di un evento sull'elemento, limitandosi alle azioni
// dei tipi indicati, che devono essere idempotenti (es. open_review: una valutazione già
// aperta non si riapre). Serve quando cambiano i dati su cui le regole si basano, per
// esempio un impatto aggiunto a una richiesta già entrata nella fase delle valutazioni.
func (s *Service) RerunRules(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, event string, kinds ...string) error {
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return err
	}
	return s.runRulesOf(ctx, tx, actor, el, fl.w, event, kinds)
}

func (s *Service) runRulesOf(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, w *Workflow, event string, kinds []string) error {
	var lf *lazyFacts
	for _, r := range w.Rules {
		if !strings.EqualFold(r.On, event) {
			continue
		}
		if kinds != nil && !slices.ContainsFunc(r.Actions, func(a Action) bool { return slices.Contains(kinds, a.Kind) }) {
			continue
		}
		if r.When != "" {
			if lf == nil {
				lf = &lazyFacts{s: s, el: el}
			}
			f, err := lf.get(ctx, tx)
			if err != nil {
				return err
			}
			if !w.cond(r.When).Eval(f) {
				continue
			}
		}
		for _, a := range r.Actions {
			if kinds != nil && !slices.Contains(kinds, a.Kind) {
				continue
			}
			note, err := s.runAction(ctx, tx, actor, el, a)
			if errors.Is(err, ErrNoEffect) {
				continue // niente da fare (es. valutazione già aperta): nessuna voce nella storia
			}
			if err != nil {
				return err
			}
			if err := addActivity(ctx, tx, el.ID, actor.MemberID, "rule", r.Key,
				map[string]any{"rule": r.Name, "action": a.Kind, "note": note}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) runAction(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action) (string, error) {
	switch a.Kind {
	case "assign_role":
		groupID := ""
		if a.Group != "" {
			err := tx.QueryRow(ctx, "select id from groups where lower(name) = lower($1)", a.Group).Scan(&groupID)
			if errors.Is(err, pgx.ErrNoRows) {
				// Il gruppo è stato rinominato o archiviato dopo la pubblicazione: si segnala nella storia.
				return "gruppo " + a.Group + " non trovato", nil
			}
			if err != nil {
				return "", err
			}
		}
		if err := SetStakeholder(ctx, tx, actor, el.ID, a.Role, a.Member, groupID, a.Replace); err != nil {
			return "", err
		}
		return "", nil
	default:
		if d, ok := s.hooks.Actions[a.Kind]; ok {
			return d.run(ctx, tx, actor, el, a)
		}
		return "azione " + a.Kind + " non disponibile", nil
	}
}

// auditProcess registra un evento della configurazione dei processi.
func auditProcess(ctx context.Context, tx pgx.Tx, actor Actor, action, processID string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "process", EntityID: processID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}
