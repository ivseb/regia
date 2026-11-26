// Package projects è il progetto sopra le epiche (replica/progetti.md, passi 1 e 2): la
// richiesta dice perché e quanto, il progetto come, quando e chi, l'epic cosa fa un team.
//
// Il progetto è un elemento di tipo project del motore unico, nello spazio PRJ, con il suo
// piano: fasi (plan_phases, con un cancello di approvazione facoltativo), work package
// (ext_work_package, ognuno con il suo modo di consegna), attività dei work package a cascata
// (ext_plan_activity) e milestone (ext_milestone). Le date stanno in elements.start_at /
// due_at, le dipendenze nelle relazioni depends_on con gli attributi kind (fs, ss, ff) e lag;
// il motore del Gantt è quello del modulo planning.
//
// Nasce da una richiesta approvata (che diventa direttamente il progetto, con gli impatti come
// work package agili) oppure da zero. I numeri del progetto (avanzamento pesato sulle stime,
// date, milestone, ritardi, dipendenze bloccate, budget, scostamenti dalla baseline) li calcola
// una sola funzione: Numbers.
//
// Passo 3: la baseline è una fotografia immutabile del piano (plan_baselines). La prima la fissa
// il PM; quando una modifica porta una milestone o la fine del progetto oltre le tolleranze del
// tenant, nasce una change request (ext_change_request) che passa dal motore delle approvazioni
// e, approvata, diventa la nuova baseline.
//
// Passo 4: il work package del fornitore ha contratto, SAL (ext_progress_claim) e deliverable
// (ext_deliverable), dichiarati dal contatto del fornitore o dal PM e accettati dal PM; due
// progetti sulla stessa applicazione nello stesso periodo sono un conflitto, mostrato nel piano e
// segnalato al team owner; il programma raggruppa progetti con numeri aggregati da computeNumbers.
package projects

import (
	"context"
	"embed"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
)

const (
	SpaceKey = "PRJ"

	TypeProgram       = "program"
	TypeProject       = "project"
	TypeWorkPackage   = "work_package"
	TypeActivity      = "plan_activity"
	TypeMilestone     = "milestone"
	TypeDeliverable   = "deliverable"
	TypeChangeRequest = "change_request"
	// TypeProgressClaim: SAL, stato di avanzamento lavori di un work package del fornitore (passo 4).
	TypeProgressClaim = "progress_claim"

	RequestType = "business_request" // modulo demand
	ImpactType  = "impact"           // modulo architettura

	RelDependsOn = "depends_on"

	// RoleSupplierContact: il contatto del fornitore sul work package. Se il work package lo
	// prevede (supplier_updates) dichiara i SAL e consegna i deliverable (decisione 3 del 2026-11-26).
	RoleSupplierContact = "supplier_contact"

	// SubjectPhaseGate: approvazione del cancello di una fase, nel motore delle approvazioni.
	SubjectPhaseGate = "phase_gate"
	// GatePolicyStage: la politica di approvazione dei cancelli (approval_policies.stage).
	GatePolicyStage = "Cancello di fase"
	// SubjectChangeRequest: approvazione di una change request (passo 3).
	SubjectChangeRequest = "change_request"
	// CRPolicyStage: la politica di approvazione delle change request.
	CRPolicyStage = "Change request"

	// Modi di consegna di un work package.
	ModeAgile     = "agile"
	ModeWaterfall = "waterfall"
	ModeSupplier  = "supplier"
)

// Modes sono i modi di consegna ammessi.
var Modes = []string{ModeAgile, ModeWaterfall, ModeSupplier}

// Templates sono i modelli di progetto.
var Templates = []string{"agile", "waterfall", "mixed", "contract"}

// planRoles possono modificare il piano, oltre a chi ha element.write.
var planRoles = []string{"project_manager", "pmo"}

//go:embed starter/*.json
var starters embed.FS

var processFiles = []string{"program_standard.json", "project_standard.json", "work_package_standard.json",
	"plan_activity_standard.json", "milestone_standard.json", "change_request_standard.json",
	"progress_claim_standard.json", "deliverable_standard.json"}

// BudgetFunc restituisce il budget delle richieste confluite nel progetto (il budget resta
// sulla richiesta fino al passo 5): la imposta il server con il modulo finance.
type BudgetFunc func(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestIDs []string) (*Budget, error)

// EpicFunc crea (se manca) l'epic di un work package agile: la imposta il server con il
// modulo del lavoro (create_epic degli impatti).
type EpicFunc func(ctx context.Context, tx pgx.Tx, actor engine.Actor, wpRef string) (*engine.Element, error)

type Service struct {
	eng    *engine.Service
	ap     *approval.Service
	Budget BudgetFunc
	Epic   EpicFunc
}

// New crea il servizio senza registrarlo sul motore (per il provisioning).
func New(eng *engine.Service) *Service { return &Service{eng: eng} }

// Register collega il modulo al motore: fatti, pacchetti di partenza, reazione ai passaggi
// (la richiesta approvata diventa il progetto; cancelli delle fasi) ed esito dei cancelli.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := &Service{eng: eng, ap: ap}
	eng.Facts(TypeWorkPackage, s.wpFacts)
	eng.Facts(TypeProject, s.projectFacts)
	describe(eng)
	eng.OnTransition(s.afterTransition)
	for _, f := range processFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			panic(err)
		}
		eng.Starter(raw)
	}
	if ap != nil {
		ap.OnClose(SubjectPhaseGate, s.gateClosed)
		ap.OnClose(SubjectChangeRequest, s.changeRequestClosed)
	}
	return s
}

func describe(eng *engine.Service) {
	eng.DescribeFacts(TypeWorkPackage,
		engine.FactInfo{Key: "modo_consegna", Name: "Modo di consegna", Kind: "text", Values: Modes, Example: ModeAgile},
		engine.FactInfo{Key: "responsabile", Name: "Responsabile del work package (team, persona o fornitore)", Kind: "presence"},
		engine.FactInfo{Key: "team", Name: "Team responsabile", Kind: "text"},
		engine.FactInfo{Key: "progetto", Name: "Progetto", Kind: "text"},
		engine.FactInfo{Key: "fase", Name: "Fase del piano", Kind: "text"},
		engine.FactInfo{Key: "avanzamento_piano", Name: "Avanzamento nel piano (%)", Kind: "number", Example: "100"},
	)
	eng.DescribeFacts(TypeProject,
		engine.FactInfo{Key: "modello", Name: "Modello di progetto", Kind: "text", Values: Templates, Example: "mixed"},
		engine.FactInfo{Key: "avanzamento", Name: "Avanzamento del progetto (%)", Kind: "number", Example: "100"},
		engine.FactInfo{Key: "work_package", Name: "Work package", Kind: "number"},
		engine.FactInfo{Key: "work_package_aperti", Name: "Work package ancora aperti", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "milestone_in_ritardo", Name: "Milestone in ritardo", Kind: "number", Example: "0"},
	)
}

// ---------------------------------------------------------------------------
// Permessi

// canPlan: chi può scrivere gli elementi o ha un ruolo di pianificazione sul progetto.
func canPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) (bool, error) {
	if actor.Has("element.write") {
		return true, nil
	}
	return engine.HasRole(ctx, tx, projectID, actor.MemberID, planRoles)
}

// canUpdateItem: oltre a chi pianifica, il responsabile della voce (assegnatario, referente
// del team) aggiorna avanzamento e date previste del suo perimetro.
func canUpdateItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string, el *engine.Element) (bool, error) {
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || ok {
		return ok, err
	}
	if el.AssigneeID != nil && *el.AssigneeID == actor.MemberID {
		return true, nil
	}
	// Il contatto del fornitore aggiorna il suo work package come il referente del team (§5).
	owners := []string{"team_lead", RoleSupplierContact}
	ok, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, owners)
	if err != nil || ok || el.ParentID == nil {
		return ok, err
	}
	// Le attività ereditano il referente del work package.
	return engine.HasRole(ctx, tx, *el.ParentID, actor.MemberID, owners)
}

// sysActor agisce per conto del motore (regole, migrazione): chi ha fatto avanzare la
// richiesta non deve per forza poter scrivere i progetti.
func sysActor(a engine.Actor) engine.Actor {
	sys := a
	sys.Permissions = append(slices.Clone(a.Permissions), "element.read", "element.write")
	return sys
}

func (s *Service) project(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != TypeProject {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// installed dice se il tenant ha il modulo dei progetti (tipo project presente).
func installed(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1 and archived_at is null)", TypeProject).Scan(&ok)
	return ok, err
}

// ---------------------------------------------------------------------------
// Fatti per le condizioni dei processi

func (s *Service) wpFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var mode, project string
	var team, phase, supplier *string
	err := tx.QueryRow(ctx, `select x.delivery_mode, p.title, t.name, ph.name, sp.title
		from ext_work_package x
		join elements p on p.tenant_id = x.tenant_id and p.id = x.project_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		left join plan_phases ph on ph.tenant_id = x.tenant_id and ph.id = x.phase_id
		left join elements sp on sp.tenant_id = x.tenant_id and sp.id = x.supplier_id
		where x.element_id = $1`, el.ID).Scan(&mode, &project, &team, &phase, &supplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	f.Set("modo_consegna", mode)
	f.Set("team", team)
	f.Set("progetto", project)
	f.Set("fase", phase)
	f.Set("responsabile", team != nil || el.AssigneeID != nil || supplier != nil)
	p, err := s.loadPlanByItem(ctx, tx, el.ID)
	if err != nil || p == nil {
		return err
	}
	for _, it := range p.Items {
		if it.ID == el.ID {
			f.Set("avanzamento_piano", it.Progress)
		}
	}
	return nil
}

func (s *Service) projectFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var tpl string
	if err := tx.QueryRow(ctx, "select template from ext_project where element_id = $1", el.ID).Scan(&tpl); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	f.Set("modello", tpl)
	p, err := s.loadPlan(ctx, tx, el)
	if err != nil {
		return err
	}
	n := computeNumbers(p, today())
	f.Set("avanzamento", n.Progress)
	f.Set("work_package", n.WorkPackages)
	f.Set("work_package_aperti", n.WorkPackages-n.WorkPackagesDone)
	late := 0
	for _, l := range n.Late {
		if l.Kind == TypeMilestone {
			late++
		}
	}
	f.Set("milestone_in_ritardo", late)
	return nil
}

// ---------------------------------------------------------------------------
// Reazione ai passaggi di fase

func (s *Service) afterTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	el := ev.Element
	switch el.TypeKey {
	case RequestType:
		return s.requestMoved(ctx, tx, ev)
	case TypeProgressClaim, TypeDeliverable:
		// «Ritira» il SAL o «Annulla» il deliverable dal processo.
		if el.StateCategory == "cancelled" {
			return supplierCancelled(ctx, tx, el)
		}
		return nil
	case TypeChangeRequest:
		// «Ritira» dal processo: la change request da inviare non serve più.
		if el.StateCategory == "cancelled" {
			_, err := tx.Exec(ctx, "update ext_change_request set status = 'withdrawn', decided_at = now() where element_id = $1 and status = 'draft'", el.ID)
			return err
		}
		return nil
	case TypeWorkPackage, TypeActivity, TypeMilestone:
		if el.TypeKey == TypeMilestone {
			if _, err := tx.Exec(ctx, `update ext_milestone set actual_date = case when $2 then coalesce(actual_date, current_date) end
				where element_id = $1`, el.ID, el.StateCategory == "done"); err != nil {
				return err
			}
		}
		if ev.System || (el.StateCategory != "in_progress" && el.StateCategory != "done") {
			return nil
		}
		return checkGate(ctx, tx, el)
	}
	return nil
}

// requestMoved: la richiesta approvata diventa direttamente il progetto (se il tenant non
// l'ha spento nelle impostazioni dei progetti).
func (s *Service) requestMoved(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	if ok, err := installed(ctx, tx); err != nil || !ok {
		return err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil || !st.AutoFromRequest {
		return err
	}
	var has bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from project_requests where request_id = $1)", ev.Element.ID).Scan(&has); err != nil || has {
		return err
	}
	ok, err := requestApproved(ctx, tx, ev.Element)
	if err != nil || !ok {
		return err
	}
	_, _, err = s.fromRequest(ctx, tx, sysActor(ev.Actor), ev.Element, "")
	return err
}

// checkGate: una voce di una fase non parte finché un cancello acceso di una fase precedente
// non è approvato (decisione 2: cancello facoltativo, per fase).
func checkGate(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
	var blocking *string
	err := tx.QueryRow(ctx, `
		with mine as (
			select coalesce(w.phase_id, aw.phase_id, m.phase_id) phase_id
			from elements e
			left join ext_work_package w on w.tenant_id = e.tenant_id and w.element_id = e.id
			left join ext_plan_activity a on a.tenant_id = e.tenant_id and a.element_id = e.id
			left join ext_work_package aw on aw.tenant_id = a.tenant_id and aw.element_id = a.work_package_id
			left join ext_milestone m on m.tenant_id = e.tenant_id and m.element_id = e.id
			where e.id = $1)
		select prev.name from mine
		join plan_phases ph on ph.id = mine.phase_id
		join plan_phases prev on prev.tenant_id = ph.tenant_id and prev.project_id = ph.project_id and prev.ordinal < ph.ordinal
		where prev.gate_enabled and prev.gate_status <> 'approved'
		order by prev.ordinal limit 1`, el.ID).Scan(&blocking)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return engine.InvalidParams("validation.projects.gate_closed", "state", map[string]any{"phase": *blocking},
		"la fase «%s» ha un cancello da approvare prima di andare avanti", *blocking)
}
