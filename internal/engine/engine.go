// Package engine è il motore unico degli elementi: tipi, campi, stati, workflow,
// stakeholder, attività e query. Non conosce i moduli di dominio: i moduli
// registrano controlli (Guards) e reazioni ai cambi di stato (AfterTransition).
// Tutte le funzioni lavorano dentro una transazione già legata al tenant.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Actor è chi compie l'operazione, già autenticato e con il tenant verificato.
type Actor struct {
	AccountID   string
	MemberID    string
	Permissions []string
	IP          netip.Addr
}

// Has dice se l'attore ha il permesso ("*" li concede tutti).
func (a Actor) Has(p string) bool {
	return slices.Contains(a.Permissions, "*") || slices.Contains(a.Permissions, p)
}

// Errori che l'API traduce in risposte HTTP.
var (
	ErrNotFound  = errors.New("elemento non trovato")
	ErrForbidden = errors.New("operazione non consentita")
	ErrConflict  = errors.New("l'elemento è stato modificato da qualcun altro: ricarica e riprova")
)

// ValidationError è un errore dell'input, da mostrare all'utente così com'è.
type ValidationError struct {
	Field string
	Msg   string
	// Code è un identificativo stabile quando il messaggio ha una traduzione propria nel client
	// (errors.<code>); vuoto = «validation», che mostra il detail del server.
	Code string
	// Params sono i valori variabili del messaggio tradotto (massimo, intervallo, tipo di
	// oggetto…): escono nel campo `params` del Problem.
	Params map[string]any
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// Invalid è la versione esportata per i moduli.
func Invalid(field, format string, args ...any) error { return invalid(field, format, args...) }

// invalidCode è invalid con un code specifico (validation.name_required…): il client lo traduce.
// I codici stanno in ValidationCodes, e ognuno ha una voce in web/src/i18n/{it,en}/errors.ts.
func invalidCode(code, field, format string, args ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, args...), Code: code}
}

// InvalidCode è la versione esportata per i moduli.
func InvalidCode(code, field, format string, args ...any) error {
	return invalidCode(code, field, format, args...)
}

// ValidationCodes sono i code specifici degli errori di validazione (per il controllo dei cataloghi).
var ValidationCodes = []string{
	"validation.date_invalid", "validation.duplicate", "validation.invalid_value", "validation.member_not_found", "validation.name_required", "validation.no_draft",
	"validation.not_found", "validation.not_negative", "validation.note_too_long", "validation.org_unit_not_found",
	"validation.out_of_range", "validation.percent_range", "validation.reason_required", "validation.required",
	"validation.role_not_found", "validation.team_not_found", "validation.title_required", "validation.too_long",
}

// GuardFunc verifica una precondizione di una transizione (es. "stima presente").
type GuardFunc func(ctx context.Context, tx pgx.Tx, el *Element) error

// TransitionEvent descrive un cambio di stato appena avvenuto.
type TransitionEvent struct {
	Element    *Element
	From       string
	To         string
	Transition Transition
	Actor      Actor
	Comment    string
	System     bool
}

// Hooks sono i punti di estensione per i moduli.
type Hooks struct {
	Guards          map[string]GuardFunc
	Facts           map[string][]FactsFunc // per chiave del tipo; più moduli possono contribuire
	AfterTransition []func(ctx context.Context, tx pgx.Tx, ev TransitionEvent) error
	Actions         map[string]actionDef // azioni delle regole registrate dai moduli
}

// ActionFunc esegue un'azione di una regola registrata da un modulo, nella transazione
// dell'evento. La nota restituita finisce nella storia dell'elemento.
type ActionFunc func(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action) (note string, err error)

// ErrNoEffect: un'azione dei moduli non aveva niente da fare (es. valutazione già aperta);
// la regola prosegue senza lasciare una voce nella storia.
var ErrNoEffect = errors.New("azione senza effetto")

// ActionCheck controlla un'azione prima della pubblicazione del processo: parametri e
// riferimenti ai dati del tenant. Restituisce i problemi trovati (vuoto = valida).
type ActionCheck func(ctx context.Context, tx pgx.Tx, a Action) ([]string, error)

type actionDef struct {
	run   ActionFunc
	check ActionCheck
}

// Action registra un tipo di azione delle regole di processo (es. "open_review").
// check è facoltativo: senza, l'azione si valida solo per il tipo.
func (s *Service) Action(kind string, fn ActionFunc, check ...ActionCheck) {
	d := actionDef{run: fn}
	if len(check) > 0 {
		d.check = check[0]
	}
	s.hooks.Actions[kind] = d
}

// ActionKinds elenca i tipi di azione disponibili (per l'editor delle regole).
func (s *Service) ActionKinds() []string {
	out := []string{"assign_role"}
	for k := range s.hooks.Actions {
		out = append(out, k)
	}
	slices.Sort(out[1:])
	return out
}

type Service struct {
	// queryFields: campi del linguaggio di query offerti dai moduli (query_fields.go).
	queryFields []QueryField
	hooks       Hooks
	editor      editorRegistry // descrizioni per l'editor dei processi (process_editor.go)
}

func New() *Service {
	return &Service{hooks: Hooks{Guards: map[string]GuardFunc{}, Facts: map[string][]FactsFunc{}, Actions: map[string]actionDef{}}}
}

// Guard registra un controllo usabile nel campo "requires" delle transizioni.
func (s *Service) Guard(name string, fn GuardFunc) { s.hooks.Guards[name] = fn }

// OnTransition registra una reazione eseguita nella stessa transazione del cambio di stato.
func (s *Service) OnTransition(fn func(ctx context.Context, tx pgx.Tx, ev TransitionEvent) error) {
	s.hooks.AfterTransition = append(s.hooks.AfterTransition, fn)
}
