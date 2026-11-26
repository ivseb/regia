package projects

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

// phaseTpl è una fase di partenza di un modello di progetto.
type phaseTpl struct {
	name string
	gate bool
	// build: la fase in cui vanno i work package nati dagli impatti della richiesta.
	build bool
}

// templatePhases: fasi di partenza per modello (progetti.md §8: cancelli spenti nel modello
// agile e misto, accesi nel modello a cascata).
var templatePhases = map[string][]phaseTpl{
	"agile":     {{name: "Realizzazione", build: true}, {name: "Rilascio"}},
	"mixed":     {{name: "Analisi"}, {name: "Realizzazione", build: true}, {name: "Collaudo"}, {name: "Rilascio"}},
	"waterfall": {{name: "Analisi", gate: true}, {name: "Disegno", gate: true}, {name: "Sviluppo", gate: true, build: true}, {name: "Test", gate: true}, {name: "Collaudo", gate: true}, {name: "Rilascio"}},
	"contract":  {{name: "Gara"}, {name: "Esecuzione", build: true}, {name: "Accettazione"}},
}

// ProjectInput crea un progetto da zero.
type ProjectInput struct {
	Title     string `json:"title"`
	BodyText  string `json:"body_text"`
	Template  string `json:"template"` // vuoto = il predefinito del tenant
	Start     string `json:"start"`    // AAAA-MM-GG, facoltativo
	End       string `json:"end"`
	ProgramID string `json:"program_id"`
	// NoPhases: nessuna fase di partenza (per esempio il progetto di una persona sola).
	NoPhases bool `json:"no_phases"`
}

// CreateProject crea un progetto da zero: obblighi, manutenzioni, progetti imposti dall'alto, home lab.
func (s *Service) CreateProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ProjectInput) (*engine.Element, error) {
	if !actor.Has("element.write") {
		return nil, engine.ErrForbidden
	}
	el, err := s.createProject(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	// Chi crea il progetto ne è il PM, finché non lo cambia.
	if actor.MemberID != "" {
		if err := engine.SetStakeholder(ctx, tx, actor, el.ID, "project_manager", actor.MemberID, "", false); err != nil {
			return nil, err
		}
	}
	return s.eng.StartProcess(ctx, tx, actor, el)
}

func (s *Service) createProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ProjectInput) (*engine.Element, error) {
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	if in.Template == "" {
		in.Template = st.DefaultTemplate
	}
	if !slices.Contains(Templates, in.Template) {
		return nil, engine.InvalidCode("validation.projects.template", "template", "modello non ammesso (agile, waterfall, mixed, contract)")
	}
	var start, end *time.Time
	if in.Start != "" || in.End != "" {
		a, err1 := calendar.Parse(in.Start)
		b, err2 := calendar.Parse(in.End)
		if err1 != nil || err2 != nil || b.Before(a) {
			return nil, engine.DateInvalid("start", "date non valide (AAAA-MM-GG, fine dopo l'inizio)")
		}
		start, end = &a, &b
	}
	if in.ProgramID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			where e.id::text = $1 and t.key = $2 and e.deleted_at is null)`, in.ProgramID, TypeProgram).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.NotFound("program_id", "program", "programma inesistente")
		}
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeProject, Title: in.Title,
		BodyText: in.BodyText, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_project (tenant_id, element_id, template, program_id) values (app_tenant(), $1, $2, $3)`,
		el.ID, in.Template, nullable(in.ProgramID)); err != nil {
		return nil, err
	}
	if start != nil {
		if _, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $3 where id = $1", el.ID, *start, *end); err != nil {
			return nil, err
		}
	}
	if !in.NoPhases {
		for i, ph := range templatePhases[in.Template] {
			if _, err := tx.Exec(ctx, `insert into plan_phases (tenant_id, project_id, name, ordinal, gate_enabled) values (app_tenant(), $1, $2, $3, $4)`,
				el.ID, ph.name, i, ph.gate); err != nil {
				return nil, err
			}
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "project.create", EntityType: TypeProject, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"template": in.Template}}); err != nil {
		return nil, err
	}
	return el, nil
}

// requestApproved: la richiesta ha un'approvazione dello stage-gate concessa, oppure è su una
// fase del percorso principale che viene dopo l'esito «approvato» della sua politica.
func requestApproved(ctx context.Context, tx pgx.Tx, req *engine.Element) (bool, error) {
	if req.StateCategory == "cancelled" {
		return false, nil
	}
	var ok bool
	var approveState *string
	if err := tx.QueryRow(ctx, `select exists (select 1 from approval_requests where element_id = $1 and subject = 'stage_gate'
			and status in ('approved', 'approved_with_conditions')),
		(select p.rules->'outcomes'->>'approve' from approval_policies p where p.type_id = $2 and p.active
		   and p.rules->'outcomes'->>'approve' is not null order by p.stage limit 1)`, req.ID, req.TypeID).Scan(&ok, &approveState); err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	target := "Pianificazione"
	if approveState != nil && *approveState != "" {
		target = *approveState
	}
	t, err := engine.LoadType(ctx, tx, req.TypeKey)
	if err != nil {
		return false, err
	}
	at, cur := -1, -1
	for i, st := range t.States {
		if st.OffPath {
			continue
		}
		if strings.EqualFold(st.Name, target) {
			at = i
		}
		if strings.EqualFold(st.Name, req.State) {
			cur = i
		}
	}
	return at >= 0 && cur >= at, nil
}

// FromRequest fa della richiesta approvata il suo progetto (o la aggiunge a un progetto
// esistente, se projectRef è indicato): dati, ruoli ereditati, date, fasi del modello e gli
// impatti come work package agili. Se la richiesta ha già un progetto lo restituisce.
func (s *Service) FromRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestRef, projectRef string) (*engine.Element, bool, error) {
	req, err := s.eng.Get(ctx, tx, actor, requestRef)
	if err != nil {
		return nil, false, err
	}
	if req.TypeKey != RequestType {
		return nil, false, engine.ErrNotFound
	}
	if !actor.Has("element.write") {
		ok, err := engine.HasRole(ctx, tx, req.ID, actor.MemberID, []string{"demand_manager", "pmo", "project_manager"})
		if err != nil || !ok {
			return nil, false, firstErr(err, engine.ErrForbidden)
		}
	}
	targetID := ""
	if projectRef != "" {
		p, err := s.project(ctx, tx, actor, projectRef)
		if err != nil {
			return nil, false, err
		}
		if ok, err := canPlan(ctx, tx, actor, p.ID); err != nil || !ok {
			return nil, false, firstErr(err, engine.ErrForbidden)
		}
		targetID = p.ID
	}
	return s.fromRequest(ctx, tx, sysActor(actor), req, targetID)
}

func (s *Service) fromRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, req *engine.Element, targetID string) (*engine.Element, bool, error) {
	sys := engine.Actor{MemberID: actor.MemberID, AccountID: actor.AccountID, IP: actor.IP, Permissions: []string{"element.read"}}
	var existing *string
	if err := tx.QueryRow(ctx, "select project_id::text from project_requests where request_id = $1", req.ID).Scan(&existing); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	if existing != nil {
		el, err := s.eng.Get(ctx, tx, sys, *existing)
		return el, false, err
	}
	ok, err := requestApproved(ctx, tx, req)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, engine.InvalidCode("validation.projects.request_not_approved", "request", "la richiesta non è ancora approvata: diventa un progetto dopo l'approvazione")
	}
	var proj *engine.Element
	created := targetID == ""
	if created {
		if proj, err = s.createProject(ctx, tx, actor, ProjectInput{Title: req.Title, BodyText: req.BodyText}); err != nil {
			return nil, false, err
		}
		if req.StartAt != nil && req.DueAt != nil {
			if _, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $3 where id = $1", proj.ID, *req.StartAt, *req.DueAt); err != nil {
				return nil, false, err
			}
		}
	} else if proj, err = s.eng.Get(ctx, tx, sys, targetID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `insert into project_requests (tenant_id, project_id, request_id) values (app_tenant(), $1, $2)`, proj.ID, req.ID); err != nil {
		return nil, false, err
	}
	// Ruoli ereditati dalla richiesta (impostazioni dei progetti).
	if _, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id, group_id, org_unit_id)
		select sh.tenant_id, $2, sh.role_id, sh.member_id, sh.group_id, sh.org_unit_id from stakeholders sh
		join project_roles pr on pr.tenant_id = sh.tenant_id and pr.role_id = sh.role_id and pr.inherit_from_request
		where sh.element_id = $1 on conflict do nothing`, req.ID, proj.ID); err != nil {
		return nil, false, err
	}
	if err := s.impactsToWorkPackages(ctx, tx, actor, proj.ID, req.ID); err != nil {
		return nil, false, err
	}
	if created {
		if proj, err = s.eng.StartProcess(ctx, tx, actor, proj); err != nil {
			return nil, false, err
		}
	}
	if err := engine.AddActivity(ctx, tx, req.ID, actor.MemberID, "project", "created", map[string]any{"project": proj.Key}, nil); err != nil {
		return nil, false, err
	}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "project", "request", map[string]any{"request": req.Key}, nil); err != nil {
		return nil, false, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "project.from_request", EntityType: TypeProject, EntityID: proj.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"request": req.Key, "new_project": created}}); err != nil {
		return nil, false, err
	}
	return proj, created, nil
}

// impactsToWorkPackages: ogni impatto della richiesta diventa un work package agile del
// progetto, con applicazione, team, stima, date e dipendenze; l'impatto resta (ext_impact) e
// il work package lo cita, così l'epic del team (figlio dell'impatto) ne porta l'avanzamento.
func (s *Service) impactsToWorkPackages(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID, requestID string) error {
	var phaseID *string
	if err := tx.QueryRow(ctx, `select id::text from plan_phases where project_id = $1
		order by (name = any($2)) desc, ordinal limit 1`, projectID, buildPhaseNames()).Scan(&phaseID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := tx.Query(ctx, `select x.element_id::text, e.title, ap.title, x.application_id::text, x.team_id::text,
		x.estimate_effort_days::float8, e.start_at, e.due_at, coalesce(o.category, '')
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.request_id = $1 and not exists (select 1 from ext_work_package w where w.impact_id = x.element_id)
		order by e.number`, requestID)
	if err != nil {
		return err
	}
	type imp struct {
		id, title, app, appID string
		team                  *string
		effort                *float64
		start, end            *time.Time
		cat                   string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (imp, error) {
		var i imp
		err := r.Scan(&i.id, &i.title, &i.app, &i.appID, &i.team, &i.effort, &i.start, &i.end, &i.cat)
		return i, err
	})
	if err != nil {
		return err
	}
	wpOf := map[string]string{}
	for _, i := range list {
		mode := ModeAgile
		wp, err := s.createItem(ctx, tx, actor, projectID, ItemInput{Kind: TypeWorkPackage, Title: i.app, PhaseID: deref(phaseID),
			Mode: &mode, ApplicationID: &i.appID, TeamID: i.team, EffortDays: i.effort, impactID: i.id})
		if err != nil {
			return fmt.Errorf("work package dall'impatto %s: %w", i.title, err)
		}
		wpOf[i.id] = wp.ID
		if i.start != nil && i.end != nil {
			if _, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $3 where id = $1", wp.ID, *i.start, *i.end); err != nil {
				return err
			}
		}
		// Lo stato segue quello dell'impatto: avviato, chiuso o annullato.
		target := map[string]string{"in_progress": "In corso", "done": "Completato", "cancelled": "Annullato"}[i.cat]
		if target != "" {
			if err := forceState(ctx, tx, wp.ID, TypeWorkPackage, target); err != nil {
				return err
			}
		}
	}
	// Dipendenze tra impatti → dipendenze fine→inizio tra work package.
	_, err = tx.Exec(ctx, `insert into relations (tenant_id, type_id, source_id, target_id, attrs, created_by)
		select r.tenant_id, r.type_id, s.wp, t.wp, '{"kind": "fs", "lag": 0}'::jsonb, $3
		from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'depends_on'
		join unnest($1::uuid[], $2::uuid[]) s(imp, wp) on s.imp = r.source_id
		join unnest($1::uuid[], $2::uuid[]) t(imp, wp) on t.imp = r.target_id
		on conflict do nothing`, keysOf(wpOf), valuesOf(wpOf), nullable(actor.MemberID))
	return err
}

func buildPhaseNames() []string {
	var out []string
	for _, phs := range templatePhases {
		for _, p := range phs {
			if p.build && !slices.Contains(out, p.name) {
				out = append(out, p.name)
			}
		}
	}
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func valuesOf(m map[string]string) []string {
	ks := keysOf(m)
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = m[k]
	}
	return out
}

// forceState porta una voce in una fase senza passaggio (migrazione dagli impatti).
func forceState(ctx context.Context, tx pgx.Tx, id, typeKey, state string) error {
	_, err := tx.Exec(ctx, `update elements e set state_id = o.id,
		resolved_at = case when o.category in ('done', 'cancelled') then now() end
		from element_types t join options o on o.tenant_id = t.tenant_id and o.option_set_id = t.state_set_id and o.name = $3
		where e.id = $1 and t.tenant_id = e.tenant_id and t.key = $2`, id, typeKey, state)
	return err
}

func firstErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
