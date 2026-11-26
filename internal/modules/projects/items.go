package projects

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/planning"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

// DepInput è una dipendenza da un'altra voce dello stesso progetto (chiave o id).
type DepInput struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"` // fs (predefinita) | ss | ff
	Lag  int    `json:"lag"`
}

// ItemInput crea una voce di piano. Kind: work_package | plan_activity | milestone.
type ItemInput struct {
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	BodyText      string     `json:"body_text"`
	PhaseID       string     `json:"phase_id"`
	ParentID      string     `json:"parent_id"` // work package dell'attività (chiave o id)
	Mode          *string    `json:"delivery_mode"`
	TeamID        *string    `json:"team_id"`
	ApplicationID *string    `json:"application_id"`
	SupplierID    *string    `json:"supplier_id"`
	AssigneeID    *string    `json:"assignee_id"`
	EffortDays    *float64   `json:"estimate_effort_days"`
	Weight        *float64   `json:"weight"`
	Contractual   *bool      `json:"contractual"`
	Start         string     `json:"start"`
	End           string     `json:"end"`
	Date          string     `json:"date"` // milestone
	DependsOn     []DepInput `json:"depends_on"`
	impactID      string
}

// CreateItem aggiunge una voce al piano del progetto.
func (s *Service) CreateItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectRef string, in ItemInput) (*ItemView, error) {
	p, err := s.project(ctx, tx, actor, projectRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, p.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	el, err := s.createItem(ctx, tx, actor, p.ID, in)
	if err != nil {
		return nil, err
	}
	if err := s.setDates(ctx, tx, el, in); err != nil {
		return nil, err
	}
	if len(in.DependsOn) > 0 {
		if err := s.replaceDeps(ctx, tx, actor, p.ID, el.ID, in.DependsOn); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, p.ID, actor.MemberID, "plan", "item", map[string]any{"item": el.Key, "kind": in.Kind}, nil); err != nil {
		return nil, err
	}
	return s.ItemView(ctx, tx, actor, el.ID)
}

func validTeam(ctx context.Context, tx pgx.Tx, id *string) error {
	if id == nil || *id == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from teams where id::text = $1)", *id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
	}
	return nil
}

func validElement(ctx context.Context, tx pgx.Tx, id *string, typeKey, field, what string) error {
	if id == nil || *id == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where e.id::text = $1 and t.key = $2 and e.deleted_at is null)`, *id, typeKey).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound(field, what, "%s inesistente", what)
	}
	return nil
}

func validPhase(ctx context.Context, tx pgx.Tx, projectID, phaseID string) error {
	if phaseID == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from plan_phases where id::text = $1 and project_id = $2)", phaseID, projectID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound("phase_id", "phase", "fase inesistente nel progetto")
	}
	return nil
}

// createItem crea la voce senza il controllo dei permessi (lo usano anche la nascita dalla
// richiesta e la migrazione). Il referente del team diventa «Referente del team» sul work package.
func (s *Service) createItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string, in ItemInput) (*engine.Element, error) {
	if in.EffortDays != nil && *in.EffortDays < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "estimate_effort_days", "non può essere negativa")
	}
	if in.Weight != nil && *in.Weight <= 0 {
		return nil, engine.InvalidCode("validation.projects.weight", "weight", "il peso deve essere maggiore di zero")
	}
	if err := validPhase(ctx, tx, projectID, in.PhaseID); err != nil {
		return nil, err
	}
	parent := projectID
	var wpID, wpPhase string
	switch in.Kind {
	case TypeWorkPackage:
		mode := ModeAgile
		if in.Mode != nil {
			mode = *in.Mode
		}
		if !slices.Contains(Modes, mode) {
			return nil, engine.InvalidCode("validation.projects.mode", "delivery_mode", "modo di consegna non ammesso (agile, waterfall, supplier)")
		}
		in.Mode = &mode
		if err := validTeam(ctx, tx, in.TeamID); err != nil {
			return nil, err
		}
		if err := validElement(ctx, tx, in.ApplicationID, "application", "application_id", "applicazione"); err != nil {
			return nil, err
		}
		if err := validElement(ctx, tx, in.SupplierID, "ict_provider", "supplier_id", "fornitore"); err != nil {
			return nil, err
		}
	case TypeActivity, TypeMilestone:
		if in.ParentID != "" {
			pid, kind, err := itemProject(ctx, tx, resolveID(ctx, tx, in.ParentID))
			if err != nil && !errors.Is(err, engine.ErrNotFound) {
				return nil, err
			}
			if err != nil || pid != projectID || kind != TypeWorkPackage {
				return nil, engine.NotFound("parent_id", "work_package", "work package inesistente nel progetto")
			}
			wpID = resolveID(ctx, tx, in.ParentID)
			parent = wpID
			_ = tx.QueryRow(ctx, "select coalesce(phase_id::text, '') from ext_work_package where element_id = $1", wpID).Scan(&wpPhase)
		} else if in.Kind == TypeActivity {
			return nil, engine.Required("parent_id", "l'attività sta dentro un work package")
		}
	default:
		return nil, engine.InvalidValue("kind", "tipo di voce non ammesso (work_package, plan_activity, milestone)")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: in.Kind, Title: in.Title,
		BodyText: in.BodyText, AssigneeID: deref(in.AssigneeID), ParentID: parent, DeferStart: true})
	if err != nil {
		return nil, err
	}
	switch in.Kind {
	case TypeWorkPackage:
		if _, err := tx.Exec(ctx, `insert into ext_work_package (tenant_id, element_id, project_id, phase_id, delivery_mode,
			application_id, team_id, supplier_id, estimate_effort_days, weight, impact_id)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			el.ID, projectID, nullable(in.PhaseID), *in.Mode, nullable(deref(in.ApplicationID)), nullable(deref(in.TeamID)),
			nullable(deref(in.SupplierID)), in.EffortDays, in.Weight, nullable(in.impactID)); err != nil {
			return nil, err
		}
		if err := setTeamLead(ctx, tx, actor, el.ID, deref(in.TeamID)); err != nil {
			return nil, err
		}
	case TypeActivity:
		if _, err := tx.Exec(ctx, `insert into ext_plan_activity (tenant_id, element_id, project_id, work_package_id, estimate_effort_days)
			values (app_tenant(), $1, $2, $3, $4)`, el.ID, projectID, wpID, in.EffortDays); err != nil {
			return nil, err
		}
	case TypeMilestone:
		phase := in.PhaseID
		if phase == "" {
			phase = wpPhase
		}
		contractual := in.Contractual != nil && *in.Contractual
		if _, err := tx.Exec(ctx, `insert into ext_milestone (tenant_id, element_id, project_id, phase_id, work_package_id, contractual)
			values (app_tenant(), $1, $2, $3, $4, $5)`, el.ID, projectID, nullable(phase), nullable(wpID), contractual); err != nil {
			return nil, err
		}
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	return el, audit.Append(ctx, tx, audit.Event{Action: "plan.item.create", EntityType: in.Kind, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"project_id": projectID}})
}

// resolveID trasforma una chiave (PRJ-12) nell'id; restituisce il riferimento com'è se non la trova.
func resolveID(ctx context.Context, tx pgx.Tx, ref string) string {
	ref = strings.TrimSpace(ref)
	if engine.IsUUID(ref) {
		return ref
	}
	var id string
	if err := tx.QueryRow(ctx, `select e.id::text from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where upper(s.key || '-' || e.number) = upper($1) and e.deleted_at is null`, ref).Scan(&id); err != nil {
		return ref
	}
	return id
}

func setTeamLead(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, teamID string) error {
	if teamID == "" {
		return nil
	}
	var lead *string
	if err := tx.QueryRow(ctx, "select lead_id::text from teams where id::text = $1", teamID).Scan(&lead); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if lead == nil {
		return nil
	}
	return engine.SetStakeholder(ctx, tx, actor, elementID, "team_lead", *lead, "", true)
}

// setDates imposta le date indicate alla creazione (senza cascata: la voce è nuova).
func (s *Service) setDates(ctx context.Context, tx pgx.Tx, el *engine.Element, in ItemInput) error {
	var start, end time.Time
	switch {
	case in.Kind == TypeMilestone && in.Date != "":
		d, err := calendar.Parse(in.Date)
		if err != nil {
			return engine.DateInvalid("date", "data non valida, formato AAAA-MM-GG")
		}
		start, end = d, d
	case in.Start != "" && in.End != "":
		a, err1 := calendar.Parse(in.Start)
		b, err2 := calendar.Parse(in.End)
		if err1 != nil || err2 != nil {
			return engine.DateInvalid("start", "date non valide, formato AAAA-MM-GG")
		}
		if b.Before(a) {
			return engine.InvalidCode("validation.planning.end_before_start", "end", "la fine è prima dell'inizio")
		}
		start, end = a, b
		if in.Kind == TypeMilestone {
			start = end
		}
	default:
		return nil
	}
	_, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $3 where id = $1", el.ID, start, end)
	return err
}

// replaceDeps sostituisce le dipendenze della voce (solo voci dello stesso progetto, niente cicli).
func (s *Service) replaceDeps(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID, itemID string, deps []DepInput) error {
	if _, err := tx.Exec(ctx, `delete from relations r using relation_types rt where rt.tenant_id = r.tenant_id and rt.id = r.type_id
		and rt.key = 'depends_on' and r.source_id = $1`, itemID); err != nil {
		return err
	}
	for _, d := range deps {
		kind := d.Kind
		if kind == "" {
			kind = planning.DepFS
		}
		if kind != planning.DepFS && kind != planning.DepSS && kind != planning.DepFF {
			return engine.InvalidCode("validation.projects.dep_kind", "depends_on", "tipo di dipendenza non ammesso (fs, ss, ff)")
		}
		if d.Lag < 0 || d.Lag > 365 {
			return engine.InvalidCode("validation.projects.dep_lag", "depends_on", "ritardo non ammesso (da 0 a 365 giorni lavorativi)")
		}
		depID := resolveID(ctx, tx, d.Ref)
		pid, _, err := itemProject(ctx, tx, depID)
		if errors.Is(err, engine.ErrNotFound) || (err == nil && pid != projectID) {
			return engine.InvalidParams("validation.projects.dep_unknown", "depends_on", map[string]any{"ref": d.Ref}, "%q non è una voce di questo progetto", d.Ref)
		}
		if err != nil {
			return err
		}
		if depID == itemID {
			return engine.InvalidCode("validation.projects.dep_self", "depends_on", "una voce non può dipendere da sé stessa")
		}
		if _, err := tx.Exec(ctx, `insert into relations (tenant_id, type_id, source_id, target_id, attrs, created_by)
			select app_tenant(), id, $1, $2, jsonb_build_object('kind', $3::text, 'lag', $4::int), $5 from relation_types where key = 'depends_on'
			on conflict (tenant_id, type_id, source_id, target_id) do update set attrs = excluded.attrs`,
			itemID, depID, kind, d.Lag, nullable(actor.MemberID)); err != nil {
			return err
		}
	}
	// Niente cicli nel piano.
	el, err := s.eng.Get(ctx, tx, engine.Actor{Permissions: []string{"element.read"}}, projectID)
	if err != nil {
		return err
	}
	p, err := s.loadPlan(ctx, tx, el)
	if err != nil {
		return err
	}
	return planning.NewScheduler(p.tasks()).CheckCycle()
}

// SetDependencies sostituisce le dipendenze di una voce.
func (s *Service) SetDependencies(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string, deps []DepInput) (*ItemView, error) {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if err := s.replaceDeps(ctx, tx, actor, projectID, el.ID, deps); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "plan", "dependencies", deps, nil); err != nil {
		return nil, err
	}
	return s.ItemView(ctx, tx, actor, el.ID)
}

// item restituisce la voce e il suo progetto, controllando che l'attore la veda.
func (s *Service) item(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, string, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, "", err
	}
	projectID, _, err := itemProject(ctx, tx, el.ID)
	if err != nil {
		return nil, "", err
	}
	return el, projectID, nil
}

// ItemPatch modifica una voce: solo i campi indicati; "" toglie team, applicazione, fornitore,
// fase o assegnatario.
type ItemPatch struct {
	Title         *string  `json:"title"`
	PhaseID       *string  `json:"phase_id"`
	Mode          *string  `json:"delivery_mode"`
	TeamID        *string  `json:"team_id"`
	ApplicationID *string  `json:"application_id"`
	SupplierID    *string  `json:"supplier_id"`
	AssigneeID    *string  `json:"assignee_id"`
	EffortDays    *float64 `json:"estimate_effort_days"`
	Weight        *float64 `json:"weight"` // 0 = torna alla stima
	Contractual   *bool    `json:"contractual"`
}

func (s *Service) UpdateItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string, in ItemPatch) (*ItemView, error) {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		// Il responsabile della voce corregge solo la stima.
		if ok2, err2 := canUpdateItem(ctx, tx, actor, projectID, el); err2 != nil || !ok2 || in.Title != nil || in.PhaseID != nil || in.Mode != nil ||
			in.TeamID != nil || in.ApplicationID != nil || in.SupplierID != nil || in.AssigneeID != nil || in.Weight != nil {
			return nil, firstErr(firstErr(err, err2), engine.ErrForbidden)
		}
	}
	if in.EffortDays != nil && *in.EffortDays < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "estimate_effort_days", "non può essere negativa")
	}
	if in.Weight != nil && *in.Weight < 0 {
		return nil, engine.InvalidCode("validation.projects.weight", "weight", "il peso deve essere maggiore di zero")
	}
	if in.Mode != nil && !slices.Contains(Modes, *in.Mode) {
		return nil, engine.InvalidCode("validation.projects.mode", "delivery_mode", "modo di consegna non ammesso (agile, waterfall, supplier)")
	}
	if in.PhaseID != nil {
		if err := validPhase(ctx, tx, projectID, *in.PhaseID); err != nil {
			return nil, err
		}
	}
	if err := validTeam(ctx, tx, in.TeamID); err != nil {
		return nil, err
	}
	if err := validElement(ctx, tx, in.ApplicationID, "application", "application_id", "applicazione"); err != nil {
		return nil, err
	}
	if err := validElement(ctx, tx, in.SupplierID, "ict_provider", "supplier_id", "fornitore"); err != nil {
		return nil, err
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
		}
		if len(t) > 500 {
			return nil, engine.TooLong("title", 500, "titolo troppo lungo (massimo 500 caratteri)")
		}
		if _, err := tx.Exec(ctx, "update elements set title = $2 where id = $1", el.ID, t); err != nil {
			return nil, err
		}
	}
	if in.AssigneeID != nil {
		if _, err := tx.Exec(ctx, "update elements set assignee_id = $2 where id = $1", el.ID, nullable(*in.AssigneeID)); err != nil {
			if strings.Contains(err.Error(), "foreign key") || strings.Contains(err.Error(), "uuid") {
				return nil, engine.InvalidCode("validation.member_not_found", "assignee_id", "persona inesistente")
			}
			return nil, err
		}
	}
	set := func(cond bool, v any) (bool, any) { return cond, v }
	switch el.TypeKey {
	case TypeWorkPackage:
		weight := in.Weight
		if weight != nil && *weight == 0 {
			weight = nil
		}
		c1, v1 := set(in.PhaseID != nil, nullable(deref(in.PhaseID)))
		c2, v2 := set(in.TeamID != nil, nullable(deref(in.TeamID)))
		c3, v3 := set(in.ApplicationID != nil, nullable(deref(in.ApplicationID)))
		c4, v4 := set(in.SupplierID != nil, nullable(deref(in.SupplierID)))
		if _, err := tx.Exec(ctx, `update ext_work_package set
			phase_id = case when $2 then $3::uuid else phase_id end,
			delivery_mode = coalesce($4, delivery_mode),
			team_id = case when $5 then $6::uuid else team_id end,
			application_id = case when $7 then $8::uuid else application_id end,
			supplier_id = case when $9 then $10::uuid else supplier_id end,
			estimate_effort_days = coalesce($11, estimate_effort_days),
			weight = case when $12 then $13::numeric else weight end
			where element_id = $1`, el.ID, c1, v1, in.Mode, c2, v2, c3, v3, c4, v4, in.EffortDays, in.Weight != nil, weight); err != nil {
			return nil, err
		}
		if in.TeamID != nil {
			if err := setTeamLead(ctx, tx, actor, el.ID, *in.TeamID); err != nil {
				return nil, err
			}
		}
	case TypeActivity:
		if _, err := tx.Exec(ctx, `update ext_plan_activity set estimate_effort_days = coalesce($2, estimate_effort_days) where element_id = $1`,
			el.ID, in.EffortDays); err != nil {
			return nil, err
		}
	case TypeMilestone:
		c1, v1 := set(in.PhaseID != nil, nullable(deref(in.PhaseID)))
		if _, err := tx.Exec(ctx, `update ext_milestone set phase_id = case when $2 then $3::uuid else phase_id end,
			contractual = coalesce($4, contractual) where element_id = $1`, el.ID, c1, v1, in.Contractual); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update elements set version = version + 1, updated_by = $2 where id = $1", el.ID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "field", "plan_item", in, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.item.update", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: toMap(in)}); err != nil {
		return nil, err
	}
	return s.ItemView(ctx, tx, actor, el.ID)
}

// DeleteItem toglie la voce dal piano (cancellazione logica, con le sue attività).
func (s *Service) DeleteItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string) error {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return firstErr(err, engine.ErrForbidden)
	}
	if _, err := tx.Exec(ctx, `update elements set deleted_at = now(), version = version + 1, updated_by = $2
		where (id = $1 or id in (select element_id from ext_plan_activity where work_package_id = $1)) and deleted_at is null`,
		el.ID, nullable(actor.MemberID)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from relations r using relation_types rt where rt.tenant_id = r.tenant_id and rt.id = r.type_id
		and rt.key = 'depends_on' and (r.source_id = $1 or r.target_id = $1)`, el.ID); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, projectID, actor.MemberID, "plan", "item_removed", map[string]any{"item": el.Key}, nil); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "plan.item.delete", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

// Schedule sposta o ridimensiona una voce; con cascade chi ne dipende slitta in avanti.
func (s *Service) Schedule(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef, startS, endS string, cascade bool) ([]planning.Change, error) {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canUpdateItem(ctx, tx, actor, projectID, el); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	start, err1 := calendar.Parse(startS)
	end, err2 := calendar.Parse(endS)
	if err1 != nil || err2 != nil {
		return nil, engine.DateInvalid("start", "date non valide, formato AAAA-MM-GG")
	}
	if end.Before(start) {
		return nil, engine.InvalidCode("validation.planning.end_before_start", "end", "la fine è prima dell'inizio")
	}
	if end.Sub(start) > 5*365*24*time.Hour {
		return nil, engine.InvalidCode("validation.planning.duration_too_long", "end", "durata eccessiva (massimo 5 anni)")
	}
	proj, err := s.eng.Get(ctx, tx, engine.Actor{Permissions: []string{"element.read"}}, projectID)
	if err != nil {
		return nil, err
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	sch := planning.NewScheduler(p.tasks())
	changes := sch.Move(el.ID, start, end, cascade)
	return changes, s.saveChanges(ctx, tx, actor, proj.ID, sch, changes)
}

// AutoPlan dà le date alle voci senza date (o a tutte con replan), rispettando le dipendenze.
func (s *Service) AutoPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, replan bool, from string) ([]planning.Change, error) {
	proj, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	base := calendar.NextWorkday(time.Now())
	if proj.StartAt != nil && proj.StartAt.After(base) {
		base = calendar.NextWorkday(*proj.StartAt)
	}
	if from != "" {
		t, err := calendar.Parse(from)
		if err != nil {
			return nil, engine.DateInvalid("from", "data non valida, formato AAAA-MM-GG")
		}
		base = calendar.NextWorkday(t)
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	sch := planning.NewScheduler(p.tasks())
	changes, err := sch.Auto(base, replan)
	if err != nil {
		return nil, err
	}
	return changes, s.saveChanges(ctx, tx, actor, proj.ID, sch, changes)
}

// saveChanges scrive le date cambiate, allarga la finestra del progetto (inviluppo delle voci),
// registra storia e audit e controlla le tolleranze della baseline.
func (s *Service) saveChanges(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string, sch *planning.Scheduler, changes []planning.Change) error {
	for _, c := range changes {
		st, en := sch.Span(c.ID)
		if _, err := tx.Exec(ctx, `update elements set start_at = $2, due_at = $3, version = version + 1, updated_by = $4 where id = $1`,
			c.ID, *st, *en, nullable(actor.MemberID)); err != nil {
			return err
		}
		if err := engine.AddActivity(ctx, tx, c.ID, actor.MemberID, "schedule", c.Cause, map[string]any{"start": c.Start, "end": c.End}, nil); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update elements p set start_at = w.s, due_at = w.e from (
			select min(e.start_at) s, max(e.due_at) e from elements e
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where e.deleted_at is null and e.start_at is not null and coalesce(o.category, '') <> 'cancelled'
			  and e.id in (select element_id from ext_work_package where project_id = $1
			               union all select element_id from ext_plan_activity where project_id = $1
			               union all select element_id from ext_milestone where project_id = $1)) w
		where p.id = $1 and w.s is not null`, projectID); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.update", EntityType: TypeProject, EntityID: projectID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"changes": changes}}); err != nil {
		return err
	}
	// Oltre le tolleranze rispetto alla baseline nasce la change request (passo 3).
	return s.checkTolerances(ctx, tx, actor, projectID)
}

// ProgressEntry è una riga dello storico dell'avanzamento dichiarato.
type ProgressEntry struct {
	ID      string  `json:"id"`
	Date    string  `json:"date"`
	Percent float64 `json:"percent"`
	Note    string  `json:"note"`
	Member  *string `json:"member"`
	At      string  `json:"created_at"`
}

// ProgressInput dichiara l'avanzamento di un'attività o di un work package senza epic.
type ProgressInput struct {
	Percent float64 `json:"percent"`
	Note    string  `json:"note"`
	Date    string  `json:"date"` // AAAA-MM-GG; vuota = oggi
}

// RecordProgress registra l'avanzamento dichiarato (storicizzato) di una voce. Sull'attività
// aggiorna anche la percentuale corrente. Il work package agile con epic prende l'avanzamento
// dagli epic: lì la dichiarazione non serve.
func (s *Service) RecordProgress(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string, in ProgressInput) (*ItemView, error) {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canUpdateItem(ctx, tx, actor, projectID, el); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if el.TypeKey == TypeMilestone {
		return nil, engine.InvalidCode("validation.projects.progress_milestone", "percent", "la milestone si raggiunge, non avanza: usa il passaggio «Segna come raggiunta»")
	}
	if math.IsNaN(in.Percent) || in.Percent < 0 || in.Percent > 100 {
		return nil, engine.InvalidCode("validation.projects.percent", "percent", "la percentuale va da 0 a 100")
	}
	if len(in.Note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	day := today()
	if in.Date != "" {
		d, err := calendar.Parse(in.Date)
		if err != nil {
			return nil, engine.DateInvalid("date", "data non valida, formato AAAA-MM-GG")
		}
		day = d
	}
	pct := math.Round(in.Percent*100) / 100
	if _, err := tx.Exec(ctx, `insert into ext_plan_progress (tenant_id, element_id, recorded_on, percent, note, member_id)
		values (app_tenant(), $1, $2, $3, $4, $5)`, el.ID, day, pct, strings.TrimSpace(in.Note), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if el.TypeKey == TypeActivity {
		if _, err := tx.Exec(ctx, "update ext_plan_activity set percent = $2 where element_id = $1", el.ID, pct); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "progress", "percent", map[string]any{"percent": pct, "note": in.Note}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.progress", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"percent": pct}}); err != nil {
		return nil, err
	}
	return s.ItemView(ctx, tx, actor, el.ID)
}

// ProgressHistory è l'andamento dichiarato della voce, dal più recente.
func (s *Service) ProgressHistory(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string) ([]ProgressEntry, error) {
	el, _, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select p.id::text, to_char(p.recorded_on, 'YYYY-MM-DD'), p.percent::float8, p.note, m.display_name,
		to_char(p.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		from ext_plan_progress p left join members m on m.tenant_id = p.tenant_id and m.id = p.member_id
		where p.element_id = $1 order by p.created_at desc limit 200`, el.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ProgressEntry])
}

// ItemView è la voce con il suo progetto, per la scheda della voce e le risposte delle API.
type ItemView struct {
	Item
	Project   Ref     `json:"project"`
	Phase     *string `json:"phase"`
	CanEdit   bool    `json:"can_edit"`
	CanUpdate bool    `json:"can_update"` // avanzamento e date del proprio perimetro
}

// ItemView restituisce la voce di piano (ErrNotFound se l'elemento non è una voce di piano).
func (s *Service) ItemView(ctx context.Context, tx pgx.Tx, actor engine.Actor, itemRef string) (*ItemView, error) {
	el, projectID, err := s.item(ctx, tx, actor, itemRef)
	if err != nil {
		return nil, err
	}
	proj, err := s.eng.Get(ctx, tx, engine.Actor{Permissions: []string{"element.read"}}, projectID)
	if err != nil {
		return nil, err
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	if err := s.limitPlan(ctx, tx, actor, p, nil); err != nil {
		return nil, err
	}
	out := &ItemView{Project: Ref{ID: proj.ID, Key: proj.Key, Title: proj.Title}}
	for _, it := range p.Items {
		if it.ID == el.ID {
			out.Item = it
		}
	}
	if out.ID == "" {
		return nil, engine.ErrNotFound
	}
	for _, ph := range p.Phases {
		if out.PhaseID != nil && ph.ID == *out.PhaseID {
			name := ph.Name
			out.Phase = &name
		}
	}
	if out.CanEdit, err = canPlan(ctx, tx, actor, projectID); err != nil {
		return nil, err
	}
	if out.CanUpdate, err = canUpdateItem(ctx, tx, actor, projectID, el); err != nil {
		return nil, err
	}
	return out, nil
}
