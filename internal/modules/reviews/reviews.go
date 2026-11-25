// Package reviews sono le valutazioni con questionari del ciclo di vita della richiesta
// (sdlc.md §2, §4-5; piano, passo 4): team valutatori con SLA ed escalation, tipi di
// valutazione per richiesta o per impatto, modelli di questionario versionati con
// condizioni, precompilazioni e regole di derivazione, la valutazione come elemento
// "review" con il suo processo, la storia delle risposte e le prescrizioni proposte.
//
// Nessun contenuto è nel codice: team, tipi, modelli e processo arrivano dal pacchetto
// di partenza (starter/) e si modificano per tenant.
package reviews

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

const (
	TypeKey     = "review"
	SpaceKey    = "DEM" // le valutazioni stanno nello spazio delle richieste, come gli impatti
	RequestType = "business_request"
	ImpactType  = "impact"

	// Ruoli sulla valutazione.
	RoleOwner    = "review_owner"    // avvia e invia il questionario (PM, referente)
	RoleCompiler = "review_compiler" // compila le sezioni assegnate
	RoleTeam     = "reviewer"        // il gruppo del team valutatore
	RoleLead     = "review_lead"     // il responsabile del team valutatore

	// Azioni delle regole di processo registrate dal modulo.
	ActionOpen  = "open_review"  // apre le valutazioni di un tipo (per la richiesta o per ogni impatto)
	ActionSync  = "sync_reviews" // su un impatto nuovo: riapplica le regole open_review della fase della richiesta
	ActionStage = "review_stage" // sulla valutazione: compile, submitted, exam, clarification, cancelled
)

// Stadi della valutazione (ext_review.stage).
const (
	StageCompile       = "compile"
	StageSubmitted     = "submitted"
	StageExam          = "exam"
	StageClarification = "clarification"
	StageConcluded     = "concluded"
	StageCancelled     = "cancelled"
)

var stages = []string{StageCompile, StageSubmitted, StageExam, StageClarification, StageCancelled}

// Service è il modulo. Now si può sostituire nei test per gli SLA.
type Service struct {
	eng *engine.Service
	Now func() time.Time
}

// Register collega il modulo al motore: fatti per i gate e azioni delle regole.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng, Now: time.Now}
	eng.Facts(RequestType, s.requestFacts)
	eng.Facts(ImpactType, s.impactFacts)
	eng.Facts(TypeKey, s.reviewFacts)
	eng.Action(ActionOpen, s.actionOpen, s.checkOpen)
	eng.Action(ActionSync, s.actionSync)
	eng.Action(ActionStage, s.actionStage, checkStage)
	// Descrizioni per l'editor visuale dei processi.
	eng.DescribeAction(engine.ActionSchema{Kind: ActionOpen, Name: "Apri una valutazione",
		Description: "Apre una valutazione del tipo indicato: per la richiesta, oppure una per ogni impatto che soddisfa la condizione.",
		Fields: []engine.ActionField{
			{Key: "ref", Name: "Tipo di valutazione (chiave)", Kind: "text", Required: true,
				Help: "La chiave del tipo, da Configurazione → Valutazioni → Tipi (es. security_review)."},
			{Key: "when", Name: "Solo per gli impatti con", Kind: "text",
				Help: "Condizione sui fatti di ciascun impatto, per i tipi con ambito impatto (es. criticita: alta, critica)."},
		}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionSync, Name: "Allinea le valutazioni del nuovo impatto",
		Description: "Su un impatto creato dopo l'apertura delle valutazioni, applica le regole «apri una valutazione» della fase della richiesta.",
		Fields:      []engine.ActionField{}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionStage, Name: "Imposta lo stadio della valutazione",
		Description: "Usata dal processo delle valutazioni: compile, submitted, exam, clarification, cancelled.",
		Fields:      []engine.ActionField{{Key: "ref", Name: "Stadio", Kind: "text", Required: true}}})
	return s
}

type cachedTemplate struct {
	def     *Definition
	version int
}

var templateCache sync.Map // id della versione pubblicata → cachedTemplate

// ---------------------------------------------------------------------------
// Azioni delle regole

func (s *Service) checkOpen(ctx context.Context, tx pgx.Tx, a engine.Action) ([]string, error) {
	if a.Ref == "" {
		return []string{"indica in \"ref\" la chiave del tipo di valutazione"}, nil
	}
	var scope string
	err := tx.QueryRow(ctx, "select scope from review_types where key = $1", a.Ref).Scan(&scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{fmt.Sprintf("tipo di valutazione %q inesistente", a.Ref)}, nil
	}
	if err != nil {
		return nil, err
	}
	if scope == "request" && a.When != "" {
		return []string{"\"when\" dell'azione vale solo per i tipi con ambito impatto: usa il \"when\" della regola"}, nil
	}
	return nil, nil
}

func checkStage(_ context.Context, _ pgx.Tx, a engine.Action) ([]string, error) {
	if !slices.Contains(stages, a.Ref) {
		return []string{fmt.Sprintf("stadio %q non ammesso (%s)", a.Ref, strings.Join(stages, ", "))}, nil
	}
	return nil, nil
}

// actionOpen apre le valutazioni del tipo indicato. Sulla richiesta: per la richiesta
// (ambito richiesta) o per ogni impatto non annullato che soddisfa "when" (ambito
// impatto). Su un impatto: solo per quell'impatto. Una valutazione già aperta non si riapre.
func (s *Service) actionOpen(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	typ, err := s.reviewType(ctx, tx, a.Ref)
	if errors.Is(err, engine.ErrNotFound) {
		return "tipo di valutazione " + a.Ref + " inesistente", nil
	}
	if err != nil {
		return "", err
	}
	if typ.Archived {
		return "tipo di valutazione " + typ.Name + " archiviato", nil
	}
	var requestID string
	var impacts []string
	switch el.TypeKey {
	case RequestType:
		requestID = el.ID
		if typ.Scope == "impact" {
			rows, err := tx.Query(ctx, `select x.element_id from ext_impact x
				join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
				left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
				where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled' order by e.number`, el.ID)
			if err != nil {
				return "", err
			}
			if impacts, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
				return "", err
			}
		}
	case ImpactType:
		if el.ParentID == nil {
			return "impatto senza richiesta", nil
		}
		requestID = *el.ParentID
		if typ.Scope == "impact" {
			impacts = []string{el.ID}
		}
	default:
		return "open_review vale solo su richieste e impatti", nil
	}
	sys := reader(actor.MemberID)
	req, err := s.eng.Get(ctx, tx, sys, requestID)
	if err != nil {
		return "", err
	}
	var opened []string
	if typ.Scope == "request" {
		r, err := s.open(ctx, tx, actor, typ, req, nil, a.Ref, true)
		if err != nil {
			return "", err
		}
		if r != nil {
			opened = append(opened, r.Key)
		}
	} else {
		var c engine.Condition
		if a.When != "" {
			if c, err = engine.ParseCondition(a.When); err != nil {
				return "condizione non valida: " + err.Error(), nil
			}
		}
		for _, id := range impacts {
			imp, err := s.eng.Get(ctx, tx, sys, id)
			if err != nil {
				return "", err
			}
			if !c.Empty() {
				f, err := s.eng.ElementFacts(ctx, tx, imp)
				if err != nil {
					return "", err
				}
				if !c.Eval(f) {
					continue
				}
			}
			r, err := s.open(ctx, tx, actor, typ, req, imp, a.Ref, true)
			if err != nil {
				return "", err
			}
			if r != nil {
				opened = append(opened, r.Key)
			}
		}
	}
	if len(opened) == 0 {
		return "", engine.ErrNoEffect
	}
	return typ.Name + ": " + strings.Join(opened, ", "), nil
}

// actionSync: un impatto nuovo su una richiesta già entrata in una fase con regole
// open_review riceve le valutazioni di quelle regole.
func (s *Service) actionSync(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, _ engine.Action) (string, error) {
	if el.TypeKey != ImpactType || el.ParentID == nil {
		return "", engine.ErrNoEffect
	}
	req, err := s.eng.Get(ctx, tx, reader(actor.MemberID), *el.ParentID)
	if err != nil {
		return "", err
	}
	if req.StateCategory == "done" || req.StateCategory == "cancelled" {
		return "", engine.ErrNoEffect
	}
	if err := s.eng.RerunRules(ctx, tx, actor, req, "enter:"+req.State, ActionOpen); err != nil {
		return "", err
	}
	return "", engine.ErrNoEffect
}

// actionStage muove lo stadio della valutazione: le fasi del processo restano
// configurabili, il modulo sa solo cosa fare entrando in ciascuno stadio.
func (s *Service) actionStage(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != TypeKey {
		return "review_stage vale solo sulle valutazioni", nil
	}
	r, err := s.loadReview(ctx, tx, el.ID, true)
	if err != nil {
		return "", err
	}
	today := s.today()
	note := ""
	switch a.Ref {
	case StageSubmitted:
		if r.SubmittedAt == nil {
			due := addWorkdays(today, r.SLADays)
			if _, err := tx.Exec(ctx, `update ext_review set stage = $2, submitted_at = now(), due_on = $3 where element_id = $1`,
				el.ID, a.Ref, due); err != nil {
				return "", err
			}
			note = "scadenza " + due.Format(time.DateOnly)
		} else if err := s.resume(ctx, tx, r, a.Ref, today); err != nil {
			return "", err
		}
		if err := s.notifyTeam(ctx, tx, actor, r, "submitted"); err != nil {
			return "", err
		}
	case StageExam:
		if err := s.resume(ctx, tx, r, a.Ref, today); err != nil {
			return "", err
		}
	case StageClarification, StageCompile:
		// Lo SLA si ferma mentre il team aspetta i compilatori.
		if _, err := tx.Exec(ctx, `update ext_review set stage = $2,
			paused_on = case when due_on is not null and stage in ('submitted','exam') then $3::date else paused_on end
			where element_id = $1`, el.ID, a.Ref, today); err != nil {
			return "", err
		}
		if a.Ref == StageClarification {
			if err := s.notifyOwners(ctx, tx, actor, r, "clarification"); err != nil {
				return "", err
			}
		}
	case StageCancelled:
		if _, err := tx.Exec(ctx, `update ext_review set stage = 'cancelled' where element_id = $1`, el.ID); err != nil {
			return "", err
		}
		if err := s.requestActivity(ctx, tx, actor, r, "cancelled", nil); err != nil {
			return "", err
		}
	default:
		return "stadio " + a.Ref + " non ammesso", nil
	}
	if note == "" {
		return "", engine.ErrNoEffect
	}
	return note, nil
}

// resume riprende lo SLA dopo una sospensione: la scadenza slitta dei giorni lavorativi
// passati in attesa dei compilatori.
func (s *Service) resume(ctx context.Context, tx pgx.Tx, r *reviewRow, stage string, today time.Time) error {
	if r.PausedOn != nil && r.DueOn != nil {
		n := lateDays(*r.PausedOn, today)
		due := addWorkdays(*r.DueOn, n)
		_, err := tx.Exec(ctx, `update ext_review set stage = $2, paused_on = null, due_on = $3,
			reminded_at = case when $4 then null else reminded_at end,
			overdue_notified_at = case when $4 then null else overdue_notified_at end
			where element_id = $1`, r.ID, stage, due, n > 0)
		return err
	}
	_, err := tx.Exec(ctx, `update ext_review set stage = $2, paused_on = null where element_id = $1`, r.ID, stage)
	return err
}

func (s *Service) today() time.Time {
	t := s.Now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ---------------------------------------------------------------------------
// Fatti per le condizioni

// countsSQL conta le valutazioni attive (non annullate, non sostituite da una nuova) di
// una richiesta (comprese quelle dei suoi impatti) o di un impatto.
const countsSQL = `
	select count(*)::int,
	       count(*) filter (where r.stage not in ('concluded','cancelled'))::int,
	       count(*) filter (where r.mandatory and r.stage not in ('concluded','cancelled'))::int,
	       count(*) filter (where r.stage = 'concluded' and r.outcome_polarity = 'negative')::int,
	       count(*) filter (where r.stage in ('submitted','exam') and r.due_on < $2)::int,
	       coalesce((select count(*) from review_prescriptions p join ext_review r2 on r2.tenant_id = p.tenant_id and r2.element_id = p.review_id
	                 where p.status = 'accepted' and r2.stage = 'concluded' and r2.superseded_by is null and %s), 0)::int,
	       coalesce(array_agg(distinct t.name) filter (where t.name is not null), '{}')
	from ext_review r
	join review_teams t on t.tenant_id = r.tenant_id and t.id = r.team_id
	where r.stage <> 'cancelled' and r.superseded_by is null and %s`

func (s *Service) counts(ctx context.Context, tx pgx.Tx, col, id string, f engine.Facts) error {
	var total, open, mandatoryOpen, negative, late, prescriptions int
	var teams []string
	q := fmt.Sprintf(countsSQL, "r2."+col+" = $1", "r."+col+" = $1")
	if err := tx.QueryRow(ctx, q, id, s.today()).Scan(&total, &open, &mandatoryOpen, &negative, &late, &prescriptions, &teams); err != nil {
		return err
	}
	f.Set("valutazioni", total)
	f.Set("valutazioni_aperte", open)
	f.Set("valutazioni_obbligatorie_aperte", mandatoryOpen)
	f.Set("valutazioni_non_favorevoli", negative)
	f.Set("valutazioni_in_ritardo", late)
	f.Set("prescrizioni", prescriptions)
	vals := make([]any, len(teams))
	for i, t := range teams {
		vals[i] = t
	}
	f.Set("team_valutatori", vals...)
	return nil
}

func (s *Service) requestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	return s.counts(ctx, tx, "request_id", el.ID, f)
}

func (s *Service) impactFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	return s.counts(ctx, tx, "impact_id", el.ID, f)
}

// reviewFacts: stadio, esito, team, tipo, domande obbligatorie mancanti (calcolate sulle
// risposte e sui fatti correnti), prescrizioni accettate, ritardo.
func (s *Service) reviewFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	r, err := s.loadReview(ctx, tx, el.ID, false)
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	f.Set("stadio", r.Stage)
	f.Set("ambito", r.Scope)
	f.Set("obbligatoria", r.Mandatory)
	f.Set("team", r.Team)
	f.Set("tipo_valutazione", r.TypeKey, r.TypeName)
	if r.Outcome != nil {
		f.Set("esito", *r.Outcome, deref(r.OutcomeLabel))
	}
	if r.DueOn != nil {
		f.Set("scadenza", *r.DueOn)
		f.Set("giorni_ritardo", lateDays(*r.DueOn, s.today()))
	}
	ev, _, err := s.evaluate(ctx, tx, r)
	if err != nil {
		return err
	}
	f.Set("domande_mancanti", len(ev.Missing))
	f.Set("domande_obbligatorie", ev.Required)
	var accepted int
	if err := tx.QueryRow(ctx, "select count(*)::int from review_prescriptions where review_id = $1 and status = 'accepted'", el.ID).Scan(&accepted); err != nil {
		return err
	}
	f.Set("prescrizioni", accepted)
	return nil
}

// reader è un attore di sola lettura per i caricamenti interni del modulo. Il motore usa
// l'id del membro per le bozze: senza un membro vale l'uuid nullo, che non ne ha.
func reader(memberID string) engine.Actor {
	if memberID == "" {
		memberID = "00000000-0000-0000-0000-000000000000"
	}
	return engine.Actor{MemberID: memberID, Permissions: []string{"element.read"}}
}
