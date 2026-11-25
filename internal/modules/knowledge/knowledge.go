// Package knowledge è il modulo Knowledge (piano, passo 19; replica/knowledge.md): spazi di
// documentazione con pagine ad albero, editor Markdown esteso, versioni con confronto e
// ripristino, blocco di modifica leggero, template per spazio, documenti controllati con il
// motore delle approvazioni e la revisione periodica, ricerca full-text con estratti,
// collegamenti alle richieste, applicazioni e asset del grafo.
//
// Le pagine sono elementi del motore (tipo "page", architecture.md §12 punto 6): stanno nella
// query, nella palette, nelle relazioni e possono essere evidenze. Il modulo aggiunge
// l'estensione kb_pages, le versioni (page_versions), i blocchi (page_locks), i riferimenti
// citati (kb_refs) e i template (kb_templates). L'accesso per spazio è del motore
// (engine.Rights, migrazione 0026).
package knowledge

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/audit"
)

const (
	// TypePage è il tipo delle pagine; SpaceKind il kind degli spazi di documentazione.
	TypePage  = "page"
	SpaceKind = "knowledge"

	// Ruoli sull'elemento per i documenti controllati (gate e passi di approvazione).
	RoleReviewer = "doc_reviewer"
	RoleApprover = "doc_approver"

	// ApprovalSubject: soggetto delle richieste di approvazione dei documenti. Chi invia in
	// revisione non può approvare (separazione dei compiti).
	ApprovalSubject = "kb_document"
	// ReviewPhase: fase in cui si apre l'approvazione (stage della politica).
	ReviewPhase = "In revisione"

	// RelDocuments: pagina → elemento che documenta (richiesta, applicazione, asset, …).
	RelDocuments = "documents"

	// LockTTL: durata del blocco di modifica; l'editor lo rinnova ogni minuto.
	LockTTL = 5 * time.Minute
)

//go:embed starter/*.json
var starters embed.FS

// Errori del modulo, tradotti dall'API.
var (
	// ErrLocked: un'altra persona sta modificando la pagina (blocco attivo).
	ErrLocked = errors.New("pagina in modifica da un'altra persona")
	// ErrStale: la pagina è stata salvata da qualcun altro dopo l'apertura dell'editor.
	ErrStale = errors.New("la pagina è stata salvata da qualcun altro nel frattempo: il tuo testo resta nella bozza locale, ricarica e riapplica le modifiche")
)

// LockedError descrive chi tiene il blocco.
type LockedError struct {
	Lock *Lock
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("%s sta modificando la pagina (blocco fino alle %s)", e.Lock.Name, e.Lock.ExpiresAt.Local().Format("15:04"))
}

func (e *LockedError) Unwrap() error { return ErrLocked }

type Service struct {
	eng   *engine.Service
	files FileStore // allegati per export e import (SetFiles)
}

// Register collega il modulo al motore: pacchetto di partenza del documento controllato,
// reazione ai passaggi (versione approvata, revisione periodica), campo di query
// "revisione", separazione dei compiti sulle approvazioni dei documenti.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := &Service{eng: eng}
	raw, err := starters.ReadFile("starter/page_controlled.json")
	if err != nil {
		panic(err)
	}
	eng.Starter(raw)
	eng.OnTransition(s.afterTransition)
	eng.Facts(TypePage, s.facts)
	if ap != nil {
		ap.SeparateRequester(ApprovalSubject)
	}
	eng.QueryField(engine.QueryField{
		Key: "revisione", Names: []string{"revisione", "review"},
		Label: "Prossima revisione di un documento controllato (es. revisione: +30g, revisione: <oggi)",
		Kind:  "date", DateOnly: true,
		Source: `select element_id, next_review as v from kb_pages where controlled and next_review is not null`,
	})
	return s
}

// afterTransition: entrando in una fase "fatta" (Approvata) la versione corrente diventa
// quella approvata, con chi l'ha approvata, e riparte la revisione periodica. Il cambio di
// fase lo notifica già il motore agli osservatori.
func (s *Service) afterTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	el := ev.Element
	if el.TypeKey != TypePage || el.StateCategory != "done" {
		return nil
	}
	var version int
	err := tx.QueryRow(ctx, `update kb_pages set approved_version = version, reminded_for = null,
			next_review = case when review_months is null then null
			                   else (current_date + make_interval(months => review_months))::date end
		where element_id = $1 returning version`, el.ID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update page_versions set approved_by = $2, approved_at = now() where element_id = $1 and version = $3`,
		el.ID, nullable(ev.Actor.MemberID), version); err != nil {
		return err
	}
	if err := s.reopenCampaigns(ctx, tx, el.ID, el.SpaceID, ev.Actor.MemberID, version); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "page.approve", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: ev.Actor.AccountID, ActorMemberID: ev.Actor.MemberID, IP: ev.Actor.IP,
		Payload: map[string]any{"version": version}})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
