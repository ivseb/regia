package engine

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Workflow è la macchina a stati di un tipo. Ha due origini:
//   - una versione pubblicata di un processo configurabile (process_versions.definition),
//     con fasi, gate e regole: è il caso normale (sdlc.md);
//   - un workflow semplice (workflows.definition) per i tipi senza processo.
//
// Gli stati sono opzioni dell'insieme di stati del tipo, riferiti per nome.
type Workflow struct {
	// Phases: fasi del processo, nell'ordine dello stage-gate. Vuoto nei workflow semplici.
	Phases      []Phase      `json:"phases,omitempty"`
	Initial     string       `json:"initial"`
	Transitions []Transition `json:"transitions"`
	Gates       []Gate       `json:"gates,omitempty"`
	Rules       []Rule       `json:"rules,omitempty"`

	conds map[string]Condition // condizioni già analizzate, per testo
}

// Phase è una fase del processo.
type Phase struct {
	Name        string `json:"name"`
	Category    string `json:"category"` // todo | in_progress | done | cancelled
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	// OffPath: fuori dal percorso principale (sospensioni, esiti negativi); non è un segmento dello stage-gate.
	OffPath bool `json:"off_path,omitempty"`
	// Labels: nome della fase per lingua (lingua → nome). Solo etichetta di visualizzazione: il codice, le
	// condizioni e i processi citano sempre Name.
	Labels map[string]string `json:"labels,omitempty"`
}

type Transition struct {
	From  []string `json:"from"` // "*" = qualunque stato non terminale
	To    string   `json:"to"`
	Label string   `json:"label,omitempty"`
	// Roles: ruoli stakeholder sull'elemento autorizzati (es. "sponsor", "demand_manager").
	// Vuoto = chiunque abbia element.write.
	Roles []string `json:"roles,omitempty"`
	// System: solo il motore può eseguirla (es. esito di un'approvazione).
	System bool `json:"system,omitempty"`
	// When: condizione perché il passaggio esista (es. percorso rapido sotto una soglia).
	When string `json:"when,omitempty"`
	// Gates: chiavi dei gate da superare prima del cambio.
	Gates []string `json:"gates,omitempty"`
	// Requires: controlli scritti nel codice dei moduli (solo workflow semplici; nei processi si usano i gate).
	Requires []string `json:"requires,omitempty"`
	// Approval: entrando nello stato di arrivo si apre una richiesta di approvazione.
	Approval        string `json:"approval,omitempty"`
	CommentRequired bool   `json:"comment_required,omitempty"`
}

// Gate è una condizione per un passaggio: o una condizione sui fatti, o un controllo
// registrato da un modulo (Check) per i casi che la condizione non esprime.
type Gate struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Condition string `json:"condition,omitempty"`
	Check     string `json:"check,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Rule reagisce a un evento del processo: "create", "enter:<fase>", "exit:<fase>".
type Rule struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	On      string   `json:"on"`
	When    string   `json:"when,omitempty"`
	Actions []Action `json:"actions"`
}

// Action è un effetto di una regola. Il motore conosce assign_role (assegna un ruolo
// sull'elemento a un gruppo o a un membro); gli altri tipi li registrano i moduli con
// Service.Action (es. open_review del modulo delle valutazioni).
type Action struct {
	Kind    string `json:"kind"`
	Role    string `json:"role,omitempty"`
	Group   string `json:"group,omitempty"`  // nome del gruppo
	Member  string `json:"member,omitempty"` // id del membro
	Replace bool   `json:"replace,omitempty"`
	// Ref: chiave dell'oggetto configurato su cui agisce un'azione dei moduli
	// (per open_review la chiave del tipo di valutazione).
	Ref string `json:"ref,omitempty"`
	// When: condizione propria dell'azione, valutata dal modulo sui suoi oggetti
	// (per open_review con ambito impatto: sui fatti di ciascun impatto).
	When string `json:"when,omitempty"`
	// Params: parametri aggiuntivi delle azioni dei moduli.
	Params map[string]string `json:"params,omitempty"`
}

// cond restituisce la condizione analizzata (le versioni pubblicate sono già validate).
func (w *Workflow) cond(src string) Condition {
	if c, ok := w.conds[src]; ok {
		return c
	}
	c, err := ParseCondition(src)
	if err != nil {
		// Non dovrebbe accadere: la pubblicazione rifiuta le condizioni non valide.
		// Per prudenza una condizione illeggibile non è mai vera.
		return Condition{src: src, alts: [][]clause{{{key: "\x00", values: []matcher{{op: "present"}}}}}}
	}
	return c
}

func (w *Workflow) compile() {
	w.conds = map[string]Condition{}
	add := func(s string) {
		if s == "" {
			return
		}
		if c, err := ParseCondition(s); err == nil {
			w.conds[s] = c
		}
	}
	for _, t := range w.Transitions {
		add(t.When)
	}
	for _, g := range w.Gates {
		add(g.Condition)
	}
	for _, r := range w.Rules {
		add(r.When)
		for _, a := range r.Actions {
			add(a.When)
		}
	}
}

// Cond restituisce la condizione già analizzata della versione (per le azioni dei moduli).
func (w *Workflow) Cond(src string) Condition { return w.cond(src) }

func (w *Workflow) gate(key string) (Gate, bool) {
	for _, g := range w.Gates {
		if g.Key == key {
			return g, true
		}
	}
	return Gate{}, false
}

// State è un'opzione dell'insieme di stati.
type State struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"` // todo | in_progress | done | cancelled
	Color    string `json:"color,omitempty"`
	OffPath  bool   `json:"off_path,omitempty"`
	// Labels: nome per lingua (solo visualizzazione).
	Labels map[string]string `json:"labels,omitempty"`
}

// ProcessRef identifica la versione di processo in uso.
type ProcessRef struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	VersionID string `json:"version_id"`
	Version   int    `json:"version"`
}

// TypeInfo è un tipo di elemento con stati e workflow già caricati. Per i tipi con
// processo, States e Workflow sono quelli della versione corrente del processo
// principale (priorità più alta); i singoli elementi possono stare su altre versioni.
type TypeInfo struct {
	ID       string      `json:"id"`
	Key      string      `json:"key"`
	Name     string      `json:"name"`
	Module   string      `json:"module"`
	States   []State     `json:"states"`
	Process  *ProcessRef `json:"process,omitempty"`
	Workflow *Workflow   `json:"-"`

	options []State // tutte le opzioni dell'insieme di stati
}

func findState(states []State, name string) (State, bool) {
	for _, s := range states {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return State{}, false
}

func findStateByID(states []State, id string) (State, bool) {
	for _, s := range states {
		if s.ID == id {
			return s, true
		}
	}
	return State{}, false
}

func (t *TypeInfo) state(name string) (State, bool) { return findState(t.States, name) }

// startsFrom dice se la transizione parte dallo stato indicato.
func (t Transition) startsFrom(from State) bool {
	terminal := from.Category == "done" || from.Category == "cancelled"
	if strings.EqualFold(t.To, from.Name) {
		return false
	}
	for _, f := range t.From {
		if strings.EqualFold(f, from.Name) {
			return true
		}
	}
	return slices.Contains(t.From, "*") && !terminal
}

// phaseStates collega le fasi della versione alle opzioni dell'insieme di stati.
// Una fase senza opzione (non dovrebbe succedere: la pubblicazione le crea) viene saltata.
func phaseStates(w *Workflow, options []State) []State {
	if len(w.Phases) == 0 {
		return options
	}
	out := make([]State, 0, len(w.Phases))
	for _, p := range w.Phases {
		if o, ok := findState(options, p.Name); ok {
			labels := o.Labels
			if len(p.Labels) > 0 {
				labels = p.Labels
			}
			out = append(out, State{ID: o.ID, Name: o.Name, Category: p.Category, Color: p.Color, OffPath: p.OffPath, Labels: labels})
		}
	}
	return out
}

// LoadType carica un tipo per chiave con il suo insieme di stati e il workflow corrente.
func LoadType(ctx context.Context, tx pgx.Tx, key string) (*TypeInfo, error) {
	return loadType(ctx, tx, "t.key = $1", key)
}

func loadTypeByID(ctx context.Context, tx pgx.Tx, id string) (*TypeInfo, error) {
	return loadType(ctx, tx, "t.id = $1", id)
}

func loadType(ctx context.Context, tx pgx.Tx, where string, arg string) (*TypeInfo, error) {
	t := &TypeInfo{}
	var stateSet *string
	var def []byte
	var pr ProcessRef
	var prID *string
	err := tx.QueryRow(ctx, `
		select t.id, t.key, t.name, t.module, t.state_set_id, w.definition,
		       p.id, coalesce(p.key, ''), coalesce(p.name, ''), coalesce(p.version_id::text, ''), coalesce(p.number, 0)
		from element_types t
		left join workflows w on w.tenant_id = t.tenant_id and w.id = t.workflow_id
		left join lateral (
			select pd.id, pd.key, pd.name, v.id as version_id, v.number
			from process_definitions pd
			join process_versions v on v.tenant_id = pd.tenant_id and v.process_id = pd.id and v.status = 'published'
			where pd.tenant_id = t.tenant_id and pd.type_id = t.id and not pd.archived
			order by pd.priority, pd.key limit 1) p on true
		where `+where, arg).Scan(&t.ID, &t.Key, &t.Name, &t.Module, &stateSet, &def, &prID, &pr.Key, &pr.Name, &pr.VersionID, &pr.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, NotFound("tipo", "type", "tipo di elemento inesistente")
	}
	if err != nil {
		return nil, err
	}
	if stateSet != nil {
		rows, err := tx.Query(ctx, `
			select id, name, coalesce(category, 'todo'), coalesce(color, ''), false, labels from options
			where option_set_id = $1 and not archived order by ordinal`, *stateSet)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var st State
			var lb []byte
			if err := rows.Scan(&st.ID, &st.Name, &st.Category, &st.Color, &st.OffPath, &lb); err != nil {
				rows.Close()
				return nil, err
			}
			st.Labels = parseLabels(lb)
			t.options = append(t.options, st)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	switch {
	case prID != nil:
		pr.ID = *prID
		t.Process = &pr
		if t.Workflow, err = loadVersion(ctx, tx, pr.VersionID); err != nil {
			return nil, err
		}
	case len(def) > 0:
		t.Workflow = &Workflow{}
		if err := json.Unmarshal(def, t.Workflow); err != nil {
			return nil, err
		}
		t.Workflow.compile()
	default:
		t.Workflow = &Workflow{}
	}
	t.States = phaseStates(t.Workflow, t.options)
	return t, nil
}

// Le versioni pubblicate sono immutabili: si tengono in memoria una volta lette.
// L'id è un UUIDv7 unico tra tutti i tenant e si ottiene solo da una query già
// filtrata dalla RLS, quindi la cache non attraversa i confini del tenant.
var versionCache sync.Map // id → *Workflow

func loadVersion(ctx context.Context, tx pgx.Tx, id string) (*Workflow, error) {
	if w, ok := versionCache.Load(id); ok {
		return w.(*Workflow), nil
	}
	var def []byte
	var status string
	if err := tx.QueryRow(ctx, "select definition, status from process_versions where id = $1", id).Scan(&def, &status); err != nil {
		return nil, err
	}
	w := &Workflow{}
	if err := json.Unmarshal(def, w); err != nil {
		return nil, err
	}
	w.compile()
	if status != "draft" {
		versionCache.Store(id, w)
	}
	return w, nil
}

// flow è il workflow effettivo di un elemento: la sua versione di processo, oppure
// quello corrente del tipo.
type flow struct {
	t      *TypeInfo
	w      *Workflow
	states []State
}

func (s *Service) flowOf(ctx context.Context, tx pgx.Tx, el *Element) (*flow, error) {
	t, err := loadTypeByID(ctx, tx, el.TypeID)
	if err != nil {
		return nil, err
	}
	f := &flow{t: t, w: t.Workflow, states: t.States}
	if el.ProcessVersionID != nil && (t.Process == nil || t.Process.VersionID != *el.ProcessVersionID) {
		if f.w, err = loadVersion(ctx, tx, *el.ProcessVersionID); err != nil {
			return nil, err
		}
		f.states = phaseStates(f.w, t.options)
	}
	return f, nil
}

// find restituisce la transizione da "from" a "to", se esiste (senza valutare "when").
func (w *Workflow) find(from State, to string) []Transition {
	var out []Transition
	for _, t := range w.Transitions {
		if strings.EqualFold(t.To, to) && t.startsFrom(from) {
			out = append(out, t)
		}
	}
	return out
}

// EntryPhaseWithAction restituisce la fase del processo dell'elemento la cui regola
// «enter:<fase>» esegue l'azione indicata (tipo e, se non vuoto, riferimento). Serve ai moduli
// che devono sapere quale fase corrisponde a un evento (per esempio la risoluzione di un
// incidente) senza dipendere dal nome che il tenant ha dato alla fase. Vuoto se non c'è.
func (s *Service) EntryPhaseWithAction(ctx context.Context, tx pgx.Tx, el *Element, kind, ref string) (string, error) {
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return "", err
	}
	for _, r := range fl.w.Rules {
		phase, ok := strings.CutPrefix(r.On, "enter:")
		if !ok {
			continue
		}
		for _, a := range r.Actions {
			if a.Kind == kind && (ref == "" || a.Ref == ref) {
				return phase, nil
			}
		}
	}
	return "", nil
}

// AtOrBeyondPhase dice se l'elemento è nella fase indicata o in una successiva del percorso,
// oppure in una fase finale (completato o annullato).
func (s *Service) AtOrBeyondPhase(ctx context.Context, tx pgx.Tx, el *Element, name string) (bool, error) {
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return false, err
	}
	cur, target := -1, -1
	for i, st := range fl.states {
		if st.ID == deref(el.StateID) {
			cur = i
			if st.Category == "done" || st.Category == "cancelled" {
				return true, nil
			}
		}
		if strings.EqualFold(st.Name, name) {
			target = i
		}
	}
	return cur >= 0 && target >= 0 && cur >= target, nil
}
