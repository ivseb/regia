// Package requirements sono i requisiti del ciclo di vita della richiesta (sdlc.md §6;
// piano, passi 5 e 6): requisiti nati dalle prescrizioni accettate delle valutazioni,
// dalle baseline dei cataloghi o inseriti a mano, di progetto (la richiesta, condivisi da
// tutti gli impatti) o di singola applicazione (l'impatto); la matrice di tracciabilità
// requisiti × impatti con lo stato di ogni cella; la verifica con evidenze e firma; le
// deroghe con rischio residuo, scadenza e approvazione a soglie; il gate di rilascio per
// applicazione.
//
// Come per le valutazioni nessun contenuto è nel codice: processi, cataloghi, baseline,
// livelli di rischio e politica di approvazione arrivano dal pacchetto di partenza
// (starter/) e si modificano per tenant.
package requirements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

const (
	TypeKey       = "requirement"
	ExceptionType = "exception"
	SpaceKey      = "DEM" // requisiti e deroghe stanno nello spazio delle richieste, come impatti e valutazioni
	RequestType   = "business_request"
	ImpactType    = "impact"
	ReviewType    = "review"

	// Ruoli sul requisito e sulla deroga.
	RoleOwner     = "requirement_owner"    // accetta e realizza (il referente del team o il PM)
	RoleVerifier  = "requirement_verifier" // verifica (il gruppo del team valutatore o i titolari di un ruolo)
	RoleRequester = "exception_requester"  // chi chiede la deroga (non la può approvare)

	// Azioni delle regole di processo registrate dal modulo.
	ActionStage     = "requirement_stage" // sul requisito: proposed, accepted, implementing, to_verify, verified, waived, not_applicable
	ActionBaseline  = "apply_baseline"    // su richiesta o impatto: applica le baseline di un catalogo
	ActionSync      = "sync_requirements" // su un impatto nuovo: celle dei requisiti di progetto e baseline della fase
	ActionException = "exception_stage"   // sulla deroga: draft, submitted, approved, rejected, expired, revoked

	// CheckRelease è il controllo del gate di rilascio sull'impatto.
	CheckRelease = "requisiti_rilascio"

	// RelOrigin collega il requisito alla valutazione da cui nasce.
	RelOrigin = "originates_from"

	// PolicyException è la politica di approvazione delle deroghe (approval_policies.stage).
	PolicyException  = "@exception"
	SubjectException = "exception"
)

// Stadi del requisito (ext_requirement.stage).
const (
	StageProposed      = "proposed"
	StageAccepted      = "accepted"
	StageImplementing  = "implementing"
	StageToVerify      = "to_verify"
	StageVerified      = "verified"
	StageWaived        = "waived"
	StageNotApplicable = "not_applicable"
)

var stages = []string{StageProposed, StageAccepted, StageImplementing, StageToVerify, StageVerified, StageWaived, StageNotApplicable}

// Stadi della deroga (ext_exception.stage).
var exceptionStages = []string{"draft", "submitted", "approved", "rejected", "expired", "revoked"}

// Service è il modulo. Now si può sostituire nei test per le scadenze delle deroghe.
type Service struct {
	eng *engine.Service
	ap  *approval.Service
	Now func() time.Time
}

// Register collega il modulo al motore: fatti, controllo del gate di rilascio, azioni
// delle regole, reazione all'esito delle valutazioni e all'approvazione delle deroghe.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := &Service{eng: eng, ap: ap, Now: time.Now}
	eng.Facts(RequestType, s.requestFacts)
	eng.Facts(ImpactType, s.impactFacts)
	eng.Facts(TypeKey, s.requirementFacts)
	eng.Facts(ExceptionType, s.exceptionFacts)
	eng.Guard(CheckRelease, s.releaseGuard)
	eng.Action(ActionStage, s.actionStage, checkIn(stages, "stadio"))
	eng.Action(ActionBaseline, s.actionBaseline, s.checkBaseline)
	eng.Action(ActionSync, s.actionSync)
	eng.Action(ActionException, s.actionException, checkIn(exceptionStages, "stadio della deroga"))
	eng.OnTransition(s.onReviewConcluded)
	if ap != nil {
		ap.OnClose(SubjectException, s.closeException)
		ap.SeparateRequester(SubjectException)
	}
	coverage := []engine.FactInfo{
		{Key: "requisiti", Name: "Requisiti applicabili", Kind: "number", Example: "8"},
		{Key: "requisiti_aperti", Name: "Requisiti da accettare o realizzare", Kind: "number", Example: "3"},
		{Key: "requisiti_da_verificare", Name: "Requisiti in attesa di verifica", Kind: "number", Example: "2"},
		{Key: "requisiti_verificati", Name: "Requisiti verificati", Kind: "number", Example: "4"},
		{Key: "requisiti_in_deroga", Name: "Requisiti coperti da una deroga valida", Kind: "number", Example: "1"},
		{Key: "deroghe_scadute", Name: "Deroghe scadute non ancora sostituite", Kind: "number", Example: "0"},
		{Key: "pronto_al_rilascio", Name: "Tutti i requisiti verificati o in deroga valida", Kind: "bool", Values: []string{"sì", "no"}},
	}
	eng.DescribeFacts(RequestType, coverage...)
	eng.DescribeFacts(ImpactType, coverage...)
	eng.DescribeFacts(TypeKey,
		engine.FactInfo{Key: "stadio", Name: "Stadio del requisito", Kind: "text", Values: stages},
		engine.FactInfo{Key: "ambito", Name: "Ambito", Kind: "text", Values: []string{"progetto", "applicazione"}},
		engine.FactInfo{Key: "origine", Name: "Origine", Kind: "text", Values: []string{"prescrizione", "catalogo", "manuale", "questionario"}},
		engine.FactInfo{Key: "categoria", Name: "Categoria", Kind: "text", Example: "Identità e accessi"},
		engine.FactInfo{Key: "verifica", Name: "Criterio di verifica", Kind: "text", Values: []string{"manuale", "evidenza"}},
	)
	eng.DescribeFacts(ExceptionType,
		engine.FactInfo{Key: "rischio", Name: "Rischio residuo", Kind: "text", Example: "medio"},
		engine.FactInfo{Key: "punteggio_rischio", Name: "Punteggio del rischio residuo", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "scadenza", Name: "Scadenza della deroga", Kind: "date", Example: "2027-06-30"},
		engine.FactInfo{Key: "giorni_alla_scadenza", Name: "Giorni alla scadenza", Kind: "number", Example: "30"},
	)
	eng.DescribeAction(engine.ActionSchema{Kind: ActionBaseline, Name: "Applica le baseline di un catalogo",
		Description: "Crea i requisiti delle baseline la cui condizione è vera: quelle di applicazione per ogni impatto (sui suoi fatti), quelle di progetto sulla richiesta.",
		Fields: []engine.ActionField{
			{Key: "ref", Name: "Catalogo (chiave)", Kind: "text", Required: true,
				Help: "La chiave del catalogo, da Configurazione → Requisiti (es. sicurezza_applicativa)."},
			{Key: "when", Name: "Solo per gli impatti con", Kind: "text",
				Help: "Condizione aggiuntiva sui fatti di ciascun impatto (es. tipo_intervento: nuova)."},
		}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionSync, Name: "Allinea i requisiti del nuovo impatto",
		Description: "Su un impatto creato dopo: aggiunge le celle dei requisiti di progetto e applica le baseline previste dalla fase della richiesta.",
		Fields:      []engine.ActionField{}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionStage, Name: "Imposta lo stadio del requisito",
		Description: "Usata dal processo dei requisiti: proposed, accepted, implementing, to_verify, verified, waived, not_applicable.",
		Fields:      []engine.ActionField{{Key: "ref", Name: "Stadio", Kind: "text", Required: true}}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionException, Name: "Imposta lo stadio della deroga",
		Description: "Usata dal processo delle deroghe: draft, submitted (apre l'approvazione), approved, rejected, expired, revoked.",
		Fields:      []engine.ActionField{{Key: "ref", Name: "Stadio", Kind: "text", Required: true}}})
	return s
}

func checkIn(allowed []string, what string) engine.ActionCheck {
	return func(_ context.Context, _ pgx.Tx, a engine.Action) ([]string, error) {
		if !slices.Contains(allowed, a.Ref) {
			return []string{fmt.Sprintf("%s %q non ammesso (%s)", what, a.Ref, strings.Join(allowed, ", "))}, nil
		}
		return nil, nil
	}
}

func (s *Service) today() time.Time {
	t := s.Now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// reader è un attore di sola lettura per i caricamenti interni del modulo.
func reader(memberID string) engine.Actor {
	if memberID == "" {
		memberID = "00000000-0000-0000-0000-000000000000"
	}
	return engine.Actor{MemberID: memberID, Permissions: []string{"element.read"}}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func audited(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

// typeExists dice se il tenant ha il tipo (il modulo può essere registrato sul motore anche
// in tenant non ancora configurati: lì non fa nulla).
func typeExists(ctx context.Context, tx pgx.Tx, key string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1)", key).Scan(&ok)
	return ok, err
}

// setRole assegna un ruolo sull'elemento senza la notifica generica dei ruoli.
func setRole(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, role, memberID, groupID string) error {
	if memberID == "" && groupID == "" {
		return nil
	}
	tag, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id, group_id)
		select app_tenant(), $1, r.id, $3, $4 from stakeholder_roles r where r.key = $2 on conflict do nothing`,
		elementID, role, nullable(memberID), nullable(groupID))
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return engine.AddActivity(ctx, tx, elementID, actor.MemberID, "stakeholder", role, map[string]any{"member_id": memberID, "group_id": groupID}, nil)
}

type holder struct{ member, group string }

// holdersOf: i titolari del primo ruolo presente, cercando negli elementi nell'ordine.
func holdersOf(ctx context.Context, tx pgx.Tx, roles []string, elements ...string) ([]holder, error) {
	for _, role := range roles {
		for _, id := range elements {
			if id == "" {
				continue
			}
			rows, err := tx.Query(ctx, `select coalesce(sh.member_id::text, ''), coalesce(sh.group_id::text, '') from stakeholders sh
				join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
				where sh.element_id = $1 and r.key = $2`, id, role)
			if err != nil {
				return nil, err
			}
			var hs []holder
			var m, g string
			if _, err := pgx.ForEachRow(rows, []any{&m, &g}, func() error {
				hs = append(hs, holder{m, g})
				return nil
			}); err != nil {
				return nil, err
			}
			if len(hs) > 0 {
				return hs, nil
			}
		}
	}
	return nil, nil
}

func inGroup(ctx context.Context, tx pgx.Tx, groupID, memberID string) (bool, error) {
	if memberID == "" || groupID == "" {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, "select member_in_group($2, $1)", groupID, memberID).Scan(&ok)
	return ok, err
}

// ---------------------------------------------------------------------------
// Azione requirement_stage: le fasi restano configurabili, il modulo sa solo cosa fare
// entrando in ciascuno stadio.

func (s *Service) actionStage(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != TypeKey {
		return "requirement_stage vale solo sui requisiti", nil
	}
	var prev string
	if err := tx.QueryRow(ctx, "select stage from ext_requirement where element_id = $1 for update", el.ID).Scan(&prev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", engine.ErrNoEffect
		}
		return "", err
	}
	if _, err := tx.Exec(ctx, "update ext_requirement set stage = $2 where element_id = $1", el.ID, a.Ref); err != nil {
		return "", err
	}
	switch {
	case a.Ref == StageNotApplicable:
		// Non applicabile: tutte le celle escono dalla matrice, con la motivazione del passaggio.
		if _, err := tx.Exec(ctx, `update applicability set status = 'not_applicable', decided_by = $2, decided_at = now(), updated_at = now(),
			justification = coalesce((select nullif(a.added->>'comment', '') from activities a where a.element_id = $1 and a.kind = 'state' order by a.id desc limit 1), justification)
			where requirement_element_id = $1`, el.ID, nullable(actor.MemberID)); err != nil {
			return "", err
		}
	case prev == StageNotApplicable:
		// Riaperto: le celle tornano applicabili e da realizzare.
		if _, err := tx.Exec(ctx, `update applicability set status = 'applicable', progress = case when progress = 'verified' then progress else 'open' end,
			decided_by = $2, decided_at = now(), updated_at = now() where requirement_element_id = $1`, el.ID, nullable(actor.MemberID)); err != nil {
			return "", err
		}
	}
	return "", engine.ErrNoEffect
}

// ---------------------------------------------------------------------------
// Prescrizioni accettate → requisiti

// onReviewConcluded: quando una valutazione arriva alla fase conclusiva con l'esito «con
// prescrizioni», ogni prescrizione accettata diventa un requisito: di applicazione se la
// valutazione è di un impatto, di progetto se è della richiesta. Verifica il team che ha
// prescritto.
func (s *Service) onReviewConcluded(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	if ev.Element.TypeKey != ReviewType || ev.Element.StateCategory != "done" {
		return nil
	}
	if ok, err := typeExists(ctx, tx, TypeKey); err != nil || !ok {
		return err
	}
	var polarity, requestID, teamID string
	var impactID *string
	err := tx.QueryRow(ctx, `select coalesce(outcome_polarity, ''), request_id::text, impact_id::text, team_id::text
		from ext_review where element_id = $1`, ev.Element.ID).Scan(&polarity, &requestID, &impactID, &teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if polarity != "conditional" && polarity != "positive" {
		return nil // non favorevole: nessun requisito (né da prescrizioni né dalle regole)
	}
	_, err = s.FromReview(ctx, tx, ev.Actor, ev.Element.ID)
	return err
}

// FromReview crea i requisiti dalle prescrizioni accettate di una valutazione conclusa che
// non li hanno ancora. È idempotente. Restituisce le chiavi create.
func (s *Service) FromReview(ctx context.Context, tx pgx.Tx, actor engine.Actor, reviewID string) ([]string, error) {
	var requestID, teamID, reviewKey string
	var impactID *string
	if err := tx.QueryRow(ctx, `select x.request_id::text, x.impact_id::text, x.team_id::text, s.key || '-' || e.number
		from ext_review x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where x.element_id = $1`, reviewID).
		Scan(&requestID, &impactID, &teamID, &reviewKey); err != nil {
		return nil, err
	}
	var polarity string
	if err := tx.QueryRow(ctx, "select coalesce(outcome_polarity, '') from ext_review where element_id = $1", reviewID).Scan(&polarity); err != nil {
		return nil, err
	}
	if polarity == "negative" {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `select p.id::text, p.text, p.category from review_prescriptions p
		where p.review_id = $1 and p.status = 'accepted' and $2 = 'conditional'
		  and not exists (select 1 from ext_requirement r where r.prescription_id = p.id)
		order by p.source desc, p.created_at, p.id`, reviewID, polarity)
	if err != nil {
		return nil, err
	}
	type presc struct{ ID, Text, Category string }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[presc])
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select p.rule_key, p.title, p.text, p.category, p.method, coalesce(p.check_key, '') from review_requirement_proposals p
		where p.review_id = $1
		  and not exists (select 1 from ext_requirement r where r.review_id = p.review_id and r.origin = 'questionnaire' and r.rule_key = p.rule_key)
		order by p.created_at, p.rule_key`, reviewID)
	if err != nil {
		return nil, err
	}
	type prop struct{ RuleKey, Title, Text, Category, Method, Check string }
	props, err := pgx.CollectRows(rows, pgx.RowToStructByPos[prop])
	if err != nil {
		return nil, err
	}
	if len(list) == 0 && len(props) == 0 {
		return nil, nil
	}
	sys := reader(actor.MemberID)
	req, err := s.eng.Get(ctx, tx, sys, requestID)
	if err != nil {
		return nil, err
	}
	var imp *engine.Element
	if impactID != nil {
		if imp, err = s.eng.Get(ctx, tx, sys, *impactID); err != nil {
			return nil, err
		}
	}
	var keys []string
	for _, p := range list {
		in := NewRequirement{Title: titleOf(p.Text), Text: p.Text, Category: p.Category, Origin: "prescription",
			ReviewID: reviewID, PrescriptionID: p.ID, Method: "manual", VerifierTeamID: teamID,
			Criterion: "Verifica del team che ha prescritto il requisito (" + reviewKey + ")."}
		el, err := s.create(ctx, tx, actor, req, imp, in)
		if err != nil {
			return nil, err
		}
		if el != nil {
			keys = append(keys, el.Key)
		}
	}
	for _, p := range props {
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = titleOf(p.Text)
		}
		in := NewRequirement{Title: title, Text: p.Text, Category: p.Category, Origin: "questionnaire",
			ReviewID: reviewID, RuleKey: p.RuleKey, Method: p.Method, Check: p.Check, VerifierTeamID: teamID,
			Criterion: "Verifica del team che ha derivato il requisito dal questionario (" + reviewKey + ")."}
		el, err := s.create(ctx, tx, actor, req, imp, in)
		if err != nil {
			return nil, err
		}
		if el != nil {
			keys = append(keys, el.Key)
		}
	}
	if len(keys) > 0 {
		if err := engine.AddActivity(ctx, tx, reviewID, actor.MemberID, "requirement", "created",
			map[string]any{"requirements": keys}, nil); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// titleOf ricava il titolo dal testo: la prima frase, al massimo 140 caratteri.
func titleOf(text string) string {
	t := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if i := strings.Index(t, ". "); i > 20 {
		t = t[:i]
	}
	r := []rune(t)
	if len(r) > 140 {
		t = strings.TrimSpace(string(r[:137])) + "…"
	}
	return strings.TrimSuffix(t, ".")
}

// ---------------------------------------------------------------------------
// Creazione del requisito

// NewRequirement è un requisito da creare.
type NewRequirement struct {
	Title, Text, Category string
	Origin                string // prescription | catalog | manual | questionnaire
	ReviewID              string
	RuleKey               string // regola del questionario (origine questionnaire)
	PrescriptionID        string
	CatalogKey            string
	CatalogVersion        int
	CatalogItemKey        string
	BaselineKey           string
	Method                string // manual | evidence
	Check                 string
	Criterion             string
	VerifierTeamID        string
	VerifierRole          string
}

// create crea il requisito sulla richiesta (imp nil, ambito di progetto) o sull'impatto
// (ambito di applicazione), con le celle della matrice, i ruoli e la storia. Una voce di
// catalogo già presente sullo stesso oggetto o una prescrizione già trasformata non si
// ripetono (nil, nil).
func (s *Service) create(ctx context.Context, tx pgx.Tx, actor engine.Actor, req, imp *engine.Element, in NewRequirement) (*engine.Element, error) {
	scope := "project"
	parent := req.ID
	var impID any
	if imp != nil {
		scope, parent, impID = "application", imp.ID, imp.ID
	}
	target := parent
	// Una sola creazione alla volta per oggetto (le regole possono scattare in parallelo).
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 11))", "req/"+target); err != nil {
		return nil, err
	}
	var exists bool
	switch in.Origin {
	case "catalog":
		if err := tx.QueryRow(ctx, `select exists (select 1 from ext_requirement where origin = 'catalog' and coalesce(impact_id, request_id) = $1
			and catalog_key = $2 and catalog_item_key = $3)`, target, in.CatalogKey, in.CatalogItemKey).Scan(&exists); err != nil {
			return nil, err
		}
	case "prescription":
		if err := tx.QueryRow(ctx, "select exists (select 1 from ext_requirement where prescription_id = $1)", in.PrescriptionID).Scan(&exists); err != nil {
			return nil, err
		}
	case "questionnaire":
		if err := tx.QueryRow(ctx, "select exists (select 1 from ext_requirement where origin = 'questionnaire' and review_id = $1 and rule_key = $2)",
			in.ReviewID, in.RuleKey).Scan(&exists); err != nil {
			return nil, err
		}
	}
	if exists {
		return nil, nil
	}
	if in.Method == "" {
		in.Method = "manual"
	}
	var verifierGroup string
	if in.VerifierTeamID != "" {
		if err := tx.QueryRow(ctx, "select group_id::text from review_teams where id = $1", in.VerifierTeamID).Scan(&verifierGroup); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, engine.NotFound("verifier", "team", "team verificatore inesistente")
			}
			return nil, err
		}
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeKey, Title: in.Title, BodyText: in.Text,
		ParentID: parent, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_requirement (tenant_id, element_id, request_id, impact_id, scope, origin, review_id, prescription_id,
		catalog_key, catalog_version, catalog_item_key, baseline_key, category, verification_method, verification_check, verification_criterion,
		verifier_team_id, verifier_role, rule_key)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		el.ID, req.ID, impID, scope, in.Origin, nullable(in.ReviewID), nullable(in.PrescriptionID),
		nullable(in.CatalogKey), nullableInt(in.CatalogVersion), nullable(in.CatalogItemKey), nullable(in.BaselineKey),
		strings.TrimSpace(in.Category), in.Method, nullable(in.Check), strings.TrimSpace(in.Criterion),
		nullable(in.VerifierTeamID), nullable(in.VerifierRole), nullable(in.RuleKey)); err != nil {
		return nil, err
	}
	// Celle: l'impatto del requisito, oppure ogni impatto attivo della richiesta.
	if imp != nil {
		if err := addCell(ctx, tx, el.ID, req.ID, imp.ID); err != nil {
			return nil, err
		}
	} else if _, err := tx.Exec(ctx, `insert into applicability (tenant_id, id, requirement_element_id, request_id, target_element_id, status, progress)
		select app_tenant(), gen_random_uuid(), $1, $2, x.element_id, 'applicable', 'open'
		from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.request_id = $2 and coalesce(o.category, '') <> 'cancelled'
		on conflict do nothing`, el.ID, req.ID); err != nil {
		return nil, err
	}
	// Ruoli: chi realizza (referente del team dell'impatto, altrimenti PM o demand manager),
	// chi verifica (il gruppo del team valutatore, oppure i titolari del ruolo indicato).
	impRef := ""
	if imp != nil {
		impRef = imp.ID
	}
	ownerRoles := []string{"project_manager", "demand_manager"}
	if imp != nil {
		ownerRoles = []string{"team_lead", "technical_analyst"}
	}
	owners, err := holdersOf(ctx, tx, ownerRoles, impRef, req.ID)
	if err != nil {
		return nil, err
	}
	if len(owners) == 0 && actor.MemberID != "" {
		owners = []holder{{member: actor.MemberID}}
	}
	var ownerMembers, ownerGroups []string
	for _, h := range owners {
		if err := setRole(ctx, tx, actor, el.ID, RoleOwner, h.member, h.group); err != nil {
			return nil, err
		}
		ownerMembers, ownerGroups = append(ownerMembers, h.member), append(ownerGroups, h.group)
	}
	if verifierGroup != "" {
		if err := setRole(ctx, tx, actor, el.ID, RoleVerifier, "", verifierGroup); err != nil {
			return nil, err
		}
	} else if in.VerifierRole != "" {
		vs, err := holdersOf(ctx, tx, []string{in.VerifierRole}, impRef, req.ID)
		if err != nil {
			return nil, err
		}
		for _, h := range vs {
			if err := setRole(ctx, tx, actor, el.ID, RoleVerifier, h.member, h.group); err != nil {
				return nil, err
			}
		}
	}
	if in.ReviewID != "" {
		if _, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
			select app_tenant(), gen_random_uuid(), t.id, $1, $2, $3 from relation_types t where t.key = $4 on conflict do nothing`,
			el.ID, in.ReviewID, nullable(actor.MemberID), RelOrigin); err != nil {
			return nil, err
		}
	}
	if el, err = s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	payload := map[string]any{"requirement": el.Key, "title": el.Title, "origin": in.Origin, "scope": scope}
	for _, id := range []string{req.ID, impRef} {
		if id == "" {
			continue
		}
		if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "requirement", "created", payload, nil); err != nil {
			return nil, err
		}
	}
	auditPayload := map[string]any{"origin": in.Origin, "scope": scope, "request": req.Key}
	if in.PrescriptionID != "" {
		auditPayload["prescription"] = in.PrescriptionID
		auditPayload["review"] = in.ReviewID
	}
	if in.CatalogKey != "" {
		auditPayload["catalog"] = in.CatalogKey
		auditPayload["catalog_version"] = in.CatalogVersion
		auditPayload["item"] = in.CatalogItemKey
		auditPayload["baseline"] = in.BaselineKey
	}
	if err := audited(ctx, tx, actor, "requirement.create", TypeKey, el.ID, auditPayload); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRequirement, ElementID: el.ID, ActorID: actor.MemberID,
		Members: ownerMembers, Groups: ownerGroups, Payload: map[string]any{"event": "assigned", "origin": in.Origin}}); err != nil {
		return nil, err
	}
	return el, nil
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func addCell(ctx context.Context, tx pgx.Tx, requirementID, requestID, impactID string) error {
	_, err := tx.Exec(ctx, `insert into applicability (tenant_id, id, requirement_element_id, request_id, target_element_id, status, progress)
		select app_tenant(), $1, $2, $3, $4, case when r.stage = 'not_applicable' then 'not_applicable' else 'applicable' end, 'open'
		from ext_requirement r where r.element_id = $2
		on conflict do nothing`, db.NewID(), requirementID, requestID, impactID)
	return err
}

// actionSync: un impatto nuovo riceve le celle dei requisiti di progetto della richiesta e
// le baseline previste dalla fase in cui si trova la richiesta.
func (s *Service) actionSync(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, _ engine.Action) (string, error) {
	if el.TypeKey != ImpactType || el.ParentID == nil {
		return "", engine.ErrNoEffect
	}
	if ok, err := typeExists(ctx, tx, TypeKey); err != nil || !ok {
		return "", err
	}
	tag, err := tx.Exec(ctx, `insert into applicability (tenant_id, id, requirement_element_id, request_id, target_element_id, status, progress)
		select app_tenant(), gen_random_uuid(), r.element_id, r.request_id, $1,
		       case when r.stage = 'not_applicable' then 'not_applicable' else 'applicable' end, 'open'
		from ext_requirement r join elements e on e.tenant_id = r.tenant_id and e.id = r.element_id and e.deleted_at is null
		where r.request_id = $2 and r.scope = 'project'
		on conflict do nothing`, el.ID, *el.ParentID)
	if err != nil {
		return "", err
	}
	req, err := s.eng.Get(ctx, tx, reader(actor.MemberID), *el.ParentID)
	if err != nil {
		return "", err
	}
	if req.StateCategory != "done" && req.StateCategory != "cancelled" {
		if err := s.eng.RerunRules(ctx, tx, actor, req, "enter:"+req.State, ActionBaseline); err != nil {
			return "", err
		}
	}
	if tag.RowsAffected() == 0 {
		return "", engine.ErrNoEffect
	}
	return fmt.Sprintf("%d requisiti di progetto", tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Allineamento della fase del requisito allo stato delle sue celle

// phaseStages: per ogni versione di processo (immutabile), tipo di azione e stadio → la
// fase in cui si entra (dalle regole "enter:<fase>" con l'azione dello stadio).
var phaseStages sync.Map // id della versione → map[string]string ("tipo/stadio" → fase)

func (s *Service) phaseFor(ctx context.Context, tx pgx.Tx, el *engine.Element, kind, stage string) (string, error) {
	if el.ProcessVersionID == nil {
		return "", nil
	}
	if m, ok := phaseStages.Load(*el.ProcessVersionID); ok {
		return m.(map[string]string)[kind+"/"+stage], nil
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "select definition from process_versions where id = $1", *el.ProcessVersionID).Scan(&raw); err != nil {
		return "", err
	}
	var w struct {
		Rules []engine.Rule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return "", err
	}
	m := map[string]string{}
	for _, r := range w.Rules {
		ph, ok := strings.CutPrefix(r.On, "enter:")
		if !ok || r.When != "" {
			continue
		}
		for _, a := range r.Actions {
			if a.Kind == ActionStage || a.Kind == ActionException {
				if _, seen := m[a.Kind+"/"+a.Ref]; !seen {
					m[a.Kind+"/"+a.Ref] = ph
				}
			}
		}
	}
	phaseStages.Store(*el.ProcessVersionID, m)
	return m[kind+"/"+stage], nil
}

// syncPhase porta il requisito nella fase che corrisponde alle sue celle: tutte verificate
// → verificato; verificate o in deroga → in deroga; il resto verificato, in deroga o da
// verificare → da verificare; con celle da realizzare un requisito già avanzato torna in
// realizzazione. Il passaggio è di sistema; se il processo non lo prevede si lascia stare.
func (s *Service) syncPhase(ctx context.Context, tx pgx.Tx, actor engine.Actor, requirementID string) error {
	var stage string
	if err := tx.QueryRow(ctx, "select stage from ext_requirement where element_id = $1", requirementID).Scan(&stage); err != nil {
		return err
	}
	if stage == StageNotApplicable {
		return nil
	}
	rows, err := s.loadCells(ctx, tx, "x.element_id = $1", requirementID)
	if err != nil {
		return err
	}
	today := s.today()
	n, verified, waived, toVerify := 0, 0, 0, 0
	for _, r := range rows {
		if !r.relevant() {
			continue
		}
		n++
		switch r.status(today) {
		case "verified":
			verified++
		case "waived":
			waived++
		case "to_verify":
			toVerify++
		}
	}
	if n == 0 {
		return nil
	}
	target := ""
	switch {
	case verified == n:
		target = StageVerified
	case verified+waived == n:
		target = StageWaived
	case verified+waived+toVerify == n:
		target = StageToVerify
	case slices.Contains([]string{StageToVerify, StageVerified, StageWaived}, stage):
		target = StageImplementing
	}
	if target == "" || target == stage {
		return nil
	}
	el, err := s.eng.Get(ctx, tx, reader(actor.MemberID), requirementID)
	if err != nil {
		return err
	}
	phase, err := s.phaseFor(ctx, tx, el, ActionStage, target)
	if err != nil || phase == "" || phase == el.State {
		return err
	}
	// Il passaggio è del sistema, a nome di chi ha causato il cambio (che può non avere
	// element.read: per esempio chi approva una deroga con un ruolo sulla sola deroga).
	sys := engine.Actor{AccountID: actor.AccountID, MemberID: actor.MemberID, IP: actor.IP, Permissions: []string{"element.read"}}
	if _, err := s.eng.Transition(ctx, tx, sys, requirementID, phase, "", engine.TransitionOpts{System: true}); err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return nil // il processo configurato non prevede il passaggio: la cella resta la fonte di verità
		}
		return err
	}
	return nil
}
