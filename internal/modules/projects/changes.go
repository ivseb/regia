package projects

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Change request (progetti.md §3 «Baseline e variazioni», passo 3).
//
// Quando una modifica del piano porta una milestone o la fine del progetto oltre la tolleranza
// in giorni, nasce da sola una change request: un elemento di tipo change_request figlio del
// progetto, con il piano proposto (la fotografia del piano in quel momento) e gli scostamenti.
// Il PM ne può aprire una anche a mano (per esempio quando è il budget a sforare). La change
// request passa dal motore delle approvazioni, come il cancello di fase: soggetto
// change_request sul progetto, politica «Change request» (di partenza decide lo sponsor).
// Approvata, il piano proposto diventa la nuova baseline; respinta, la baseline resta e il
// piano resta fuori tolleranza finché il PM non lo riporta dentro o ne apre un'altra.
// Una sola change request aperta (da inviare o in approvazione) per progetto.
//
// Se l'approvazione non può partire (nessuna politica, manca lo sponsor), la change request
// resta «da inviare» con il motivo, e il PM la invia quando ha sistemato.

// Deviation è uno scostamento oltre le tolleranze registrato nella change request. Kind è
// milestone o project (la fine del progetto); i titoli si leggono dal piano a chi può.
type Deviation struct {
	ItemID      string  `json:"id"`
	Kind        string  `json:"kind"`
	Key         string  `json:"key"`
	Title       string  `json:"title"`
	BaselineEnd *string `json:"baseline_end"`
	End         *string `json:"end"`
	Days        int     `json:"days"`
	Restricted  bool    `json:"restricted,omitempty"`
}

// Blocker è il motivo per cui l'approvazione non è partita (codice di errore e parametri).
type Blocker struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// ChangeRequest è una change request del progetto.
type ChangeRequest struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Title         string `json:"title"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
	// Status: draft (da inviare) | pending | approved | rejected | withdrawn.
	Status            string      `json:"status"`
	Origin            string      `json:"origin"` // tolerance | manual
	Reason            string      `json:"reason"`
	Baseline          *int        `json:"baseline"`
	NewBaseline       *int        `json:"new_baseline"`
	NewBaselineID     *string     `json:"new_baseline_id"`
	ApprovalRequestID *string     `json:"approval_request_id"`
	Blocked           *Blocker    `json:"blocked"`
	Deviations        []Deviation `json:"deviations"`
	// BudgetPct: scostamento del budget alla nascita, solo a chi vede il budget.
	BudgetPct  *float64  `json:"budget_pct"`
	Tolerance  Tolerance `json:"tolerance"`
	CreatedAt  string    `json:"created_at"`
	CreatedBy  *string   `json:"created_by"`
	DecidedAt  *string   `json:"decided_at"`
	CanSubmit  bool      `json:"can_submit"`
	Restricted bool      `json:"restricted,omitempty"`
}

// ChangeRequestInput apre una change request a mano.
type ChangeRequestInput struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// deviationsOf: milestone e fine del progetto oltre la tolleranza in giorni.
func deviationsOf(p *Plan, n *Numbers) []Deviation {
	out := []Deviation{}
	byID := map[string]*Item{}
	for i := range p.Items {
		byID[p.Items[i].ID] = &p.Items[i]
	}
	for _, r := range n.OverTolerance {
		it := byID[r.ID]
		out = append(out, Deviation{ItemID: r.ID, Kind: TypeMilestone, BaselineEnd: it.BaselineEnd, End: it.End, Days: r.DaysLate})
	}
	if n.EndOverTolerance && n.VarianceDays != nil {
		out = append(out, Deviation{ItemID: p.Project.ID, Kind: "project", BaselineEnd: n.BaselineEnd, End: n.End, Days: *n.VarianceDays})
	}
	return out
}

// openChangeRequest dice se il progetto ha già una change request da inviare o in approvazione.
func openChangeRequest(ctx context.Context, tx pgx.Tx, projectID string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from ext_change_request x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		where x.project_id = $1 and x.status in ('draft', 'pending') and e.deleted_at is null)`, projectID).Scan(&ok)
	return ok, err
}

// checkTolerances: dopo una modifica del piano, se una milestone o la fine del progetto sono
// oltre la tolleranza e non c'è già una change request aperta, ne apre una. Non fallisce mai
// per l'approvazione: se non parte, la change request resta da inviare.
func (s *Service) checkTolerances(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) error {
	var has bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from plan_baselines where project_id = $1)", projectID).Scan(&has); err != nil || !has {
		return err
	}
	if open, err := openChangeRequest(ctx, tx, projectID); err != nil || open {
		return err
	}
	proj, err := s.eng.Get(ctx, tx, engine.Actor{Permissions: []string{"element.read"}}, projectID)
	if err != nil {
		return err
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return err
	}
	n := computeNumbers(p, today())
	devs := deviationsOf(p, &n)
	if len(devs) == 0 {
		return nil
	}
	_, err = s.newChangeRequest(ctx, tx, actor, proj, p, "tolerance", "", "", devs)
	return err
}

// newChangeRequest crea la change request con il piano proposto e prova ad avviarne l'approvazione.
func (s *Service) newChangeRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element, p *Plan,
	origin, title, reason string, devs []Deviation) (string, error) {
	sn := snapshotOf(p)
	var err error
	if sn.Budget, err = s.budgetSnapshot(ctx, tx, p); err != nil {
		return "", err
	}
	var budgetPct *float64
	if p.Baseline != nil {
		budgetPct = budgetVariance(p.Baseline.budget, sn.Budget)
	}
	if title == "" {
		title = "Variazione del piano"
		if origin == "tolerance" {
			title = "Scostamento oltre le tolleranze"
		}
	}
	el, err := s.eng.Create(ctx, tx, sysActor(actor), engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeChangeRequest, Title: title,
		BodyText: reason, ParentID: proj.ID, DeferStart: true})
	if err != nil {
		return "", err
	}
	proposal, _ := json.Marshal(sn)
	for i := range devs {
		devs[i].Key, devs[i].Title = "", "" // i titoli si leggono dal piano, con i limiti di chi guarda
	}
	devRaw, _ := json.Marshal(devs)
	var baselineID *string
	if p.Baseline != nil {
		baselineID = &p.Baseline.ID
	}
	if _, err := tx.Exec(ctx, `insert into ext_change_request (tenant_id, element_id, project_id, origin, reason, baseline_id,
		proposal, deviations, budget_pct, tolerance_days, tolerance_budget_pct)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		el.ID, proj.ID, origin, strings.TrimSpace(reason), baselineID, proposal, devRaw, budgetPct, p.Tolerance.Days, p.Tolerance.BudgetPct); err != nil {
		return "", err
	}
	if _, err := s.eng.StartProcess(ctx, tx, sysActor(actor), el); err != nil {
		return "", err
	}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "plan", "change_request", map[string]any{"change_request": el.Key, "origin": origin}, nil); err != nil {
		return "", err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.change_request", EntityType: TypeChangeRequest, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"project_id": proj.ID, "origin": origin, "deviations": len(devs)}}); err != nil {
		return "", err
	}
	return el.ID, s.submit(ctx, tx, actor, proj, el.ID, el.Key, false)
}

// submit avvia l'approvazione della change request. Con strict l'errore torna a chi l'ha
// chiesta; altrimenti (nascita automatica) la change request resta da inviare con il motivo.
func (s *Service) submit(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element, crID, crKey string, strict bool) error {
	block := func(code string, params map[string]any) error {
		raw, _ := json.Marshal(params)
		_, err := tx.Exec(ctx, "update ext_change_request set blocked_code = $2, blocked_params = $3 where element_id = $1", crID, code, raw)
		return err
	}
	if s.ap == nil {
		if strict {
			return engine.InvalidCode("validation.projects.no_approvals", "approval", "il motore delle approvazioni non è disponibile")
		}
		return block("validation.projects.no_approvals", nil)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	reqID, err := s.ap.OpenSubject(ctx, sp, actor, proj, SubjectChangeRequest, CRPolicyStage, CRPolicyStage+": "+crKey, 0)
	if err == nil && reqID == "" {
		err = engine.InvalidCode("validation.projects.cr_no_policy", "approval", "nessuna politica di approvazione per le change request: configurala in Approvazioni")
	}
	if err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return rbErr
		}
		var ve *engine.ValidationError
		if !errors.As(err, &ve) {
			return err
		}
		if strict {
			return err
		}
		code := ve.Code
		if code == "" {
			code = "validation"
		}
		// Il messaggio del server resta nei parametri: è il testo da mostrare in italiano.
		params := map[string]any{"detail": ve.Msg}
		for k, v := range ve.Params {
			params[k] = v
		}
		return block(code, params)
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update ext_change_request set status = 'pending', approval_request_id = $2, blocked_code = null, blocked_params = null
		where element_id = $1`, crID, reqID); err != nil {
		return err
	}
	return forceState(ctx, tx, crID, TypeChangeRequest, "In approvazione")
}

// crProject trova la change request (id o chiave) e il suo progetto, controllando che l'attore li veda.
func (s *Service) crProject(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, *engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, nil, err
	}
	if el.TypeKey != TypeChangeRequest {
		return nil, nil, engine.ErrNotFound
	}
	var projectID string
	if err := tx.QueryRow(ctx, "select project_id::text from ext_change_request where element_id = $1", el.ID).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, engine.ErrNotFound
		}
		return nil, nil, err
	}
	proj, err := s.project(ctx, tx, actor, projectID)
	return el, proj, err
}

// CreateChangeRequest apre a mano una change request sul piano attuale (serve una baseline).
func (s *Service) CreateChangeRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ChangeRequestInput) (*ChangeRequest, error) {
	proj, err := s.planProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if len(title) > 500 {
		return nil, engine.TooLong("title", 500, "titolo troppo lungo (massimo 500 caratteri)")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, engine.Required("reason", "scrivi il motivo della variazione")
	}
	if len(reason) > 4000 {
		return nil, engine.TooLong("reason", 4000, "motivo troppo lungo (massimo 4000 caratteri)")
	}
	if _, err := tx.Exec(ctx, "select 1 from elements where id = $1 for update", proj.ID); err != nil {
		return nil, err
	}
	if open, err := openChangeRequest(ctx, tx, proj.ID); err != nil {
		return nil, err
	} else if open {
		return nil, engine.InvalidCode("validation.projects.cr_open", "change_request", "il progetto ha già una change request aperta")
	}
	p, err := s.loadPlan(ctx, tx, proj)
	if err != nil {
		return nil, err
	}
	if p.Baseline == nil {
		return nil, engine.InvalidCode("validation.projects.baseline_missing", "baseline", "il piano non ha ancora una baseline: fissala prima di chiedere una variazione")
	}
	n := computeNumbers(p, today())
	id, err := s.newChangeRequest(ctx, tx, actor, proj, p, "manual", title, reason, deviationsOf(p, &n))
	if err != nil {
		return nil, err
	}
	return s.ChangeRequest(ctx, tx, actor, id)
}

// SubmitChangeRequest invia all'approvazione la change request da inviare (con un motivo nuovo, se indicato).
func (s *Service) SubmitChangeRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, reason string) (*ChangeRequest, error) {
	el, proj, err := s.crProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	var status string
	if err := tx.QueryRow(ctx, "select status from ext_change_request where element_id = $1 for update", el.ID).Scan(&status); err != nil {
		return nil, err
	}
	if status != "draft" {
		return nil, engine.InvalidCode("validation.projects.cr_status", "status", "la change request non è da inviare")
	}
	if r := strings.TrimSpace(reason); r != "" {
		if len(r) > 4000 {
			return nil, engine.TooLong("reason", 4000, "motivo troppo lungo (massimo 4000 caratteri)")
		}
		if _, err := tx.Exec(ctx, "update ext_change_request set reason = $2 where element_id = $1", el.ID, r); err != nil {
			return nil, err
		}
	}
	if err := s.submit(ctx, tx, actor, proj, el.ID, el.Key, true); err != nil {
		return nil, err
	}
	return s.ChangeRequest(ctx, tx, actor, el.ID)
}

// WithdrawChangeRequest ritira la change request da inviare.
func (s *Service) WithdrawChangeRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ChangeRequest, error) {
	el, proj, err := s.crProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	tag, err := tx.Exec(ctx, "update ext_change_request set status = 'withdrawn', decided_at = now() where element_id = $1 and status = 'draft'", el.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidCode("validation.projects.cr_status", "status", "si ritira solo una change request da inviare")
	}
	if err := forceState(ctx, tx, el.ID, TypeChangeRequest, "Ritirata"); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "plan", "change_request_withdrawn", map[string]any{"change_request": el.Key}, nil); err != nil {
		return nil, err
	}
	return s.ChangeRequest(ctx, tx, actor, el.ID)
}

// changeRequestClosed applica l'esito dell'approvazione: approvata, il piano proposto diventa
// la nuova baseline (e se il piano è già andato oltre si riapre il controllo); respinta, la
// baseline resta; rimandata, la change request torna da inviare.
func (s *Service) changeRequestClosed(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var crID, projectID string
	var raw []byte
	err := tx.QueryRow(ctx, "select element_id::text, project_id::text, proposal from ext_change_request where approval_request_id = $1 and status = 'pending' for update",
		requestID).Scan(&crID, &projectID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var status, state string
	switch outcome {
	case "approved", "approved_with_conditions":
		status, state = "approved", "Approvata"
	case "rejected":
		status, state = "rejected", "Respinta"
	default:
		status, state = "draft", "Da inviare"
	}
	var newID *string
	if status == "approved" {
		var sn snapshot
		if err := json.Unmarshal(raw, &sn); err != nil {
			return err
		}
		id, _, err := writeBaseline(ctx, tx, actor, projectID, crID, comment, sn)
		if err != nil {
			return err
		}
		newID = &id
	}
	if _, err := tx.Exec(ctx, `update ext_change_request set status = $2, new_baseline_id = $3,
		decided_at = case when $2 = 'draft' then null else now() end, approval_request_id = case when $2 = 'draft' then null else approval_request_id end
		where element_id = $1`, crID, status, newID); err != nil {
		return err
	}
	if err := forceState(ctx, tx, crID, TypeChangeRequest, state); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, projectID, actor.MemberID, "plan", "change_request_closed",
		map[string]any{"outcome": outcome, "comment": comment}, nil); err != nil {
		return err
	}
	if status == "approved" {
		// Il piano può essersi mosso durante l'approvazione: si confronta con la nuova baseline.
		return s.checkTolerances(ctx, tx, sysActor(actor), projectID)
	}
	return nil
}

// ChangeRequests elenca le change request del progetto, dalla più recente.
func (s *Service) ChangeRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]ChangeRequest, error) {
	proj, err := s.project(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	return s.changeRequests(ctx, tx, actor, proj, "")
}

// ChangeRequest restituisce una change request.
func (s *Service) ChangeRequest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ChangeRequest, error) {
	el, proj, err := s.crProject(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out, err := s.changeRequests(ctx, tx, actor, proj, el.ID)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, engine.ErrNotFound
	}
	return &out[0], nil
}

func (s *Service) changeRequests(ctx context.Context, tx pgx.Tx, actor engine.Actor, proj *engine.Element, only string) ([]ChangeRequest, error) {
	rows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		x.status, x.origin, x.reason, b.number, nb.number, x.new_baseline_id::text, x.approval_request_id::text, x.blocked_code, x.blocked_params,
		x.deviations, x.budget_pct::float8, x.tolerance_days, x.tolerance_budget_pct::float8,
		to_char(e.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), m.display_name,
		to_char(x.decided_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		from ext_change_request x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join plan_baselines b on b.tenant_id = x.tenant_id and b.id = x.baseline_id
		left join plan_baselines nb on nb.tenant_id = x.tenant_id and nb.id = x.new_baseline_id
		left join members m on m.tenant_id = e.tenant_id and m.id = e.reporter_id
		where x.project_id = $1 and ($2 = '' or x.element_id::text = $2)
		order by e.created_at desc, e.number desc`, proj.ID, only)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChangeRequest, error) {
		var c ChangeRequest
		var code *string
		var params, devs []byte
		err := r.Scan(&c.ID, &c.Key, &c.Title, &c.State, &c.StateCategory, &c.Status, &c.Origin, &c.Reason, &c.Baseline, &c.NewBaseline,
			&c.NewBaselineID, &c.ApprovalRequestID, &code, &params, &devs, &c.BudgetPct, &c.Tolerance.Days, &c.Tolerance.BudgetPct,
			&c.CreatedAt, &c.CreatedBy, &c.DecidedAt)
		if err != nil {
			return c, err
		}
		if code != nil {
			c.Blocked = &Blocker{Code: *code}
			if len(params) > 0 {
				_ = json.Unmarshal(params, &c.Blocked.Params)
			}
		}
		c.Deviations = []Deviation{}
		if err := json.Unmarshal(devs, &c.Deviations); err != nil {
			return c, err
		}
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	// Titoli e chiavi degli scostamenti dal piano, con i limiti di visibilità dell'attore.
	r := s.reader(actor)
	keys := map[string][2]string{}
	if len(out) > 0 {
		rows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title from elements e
			join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where e.id in (select (d->>'id')::uuid from ext_change_request x, jsonb_array_elements(x.deviations) d where x.project_id = $1)`, proj.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, key, title string
			if err := rows.Scan(&id, &key, &title); err != nil {
				rows.Close()
				return nil, err
			}
			keys[id] = [2]string{key, title}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	budgetOK, err := s.budgetVisible(ctx, tx, actor, proj)
	if err != nil {
		return nil, err
	}
	canPlanIt, err := canPlan(ctx, tx, actor, proj.ID)
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, c := range out {
		ok, err := r.can(ctx, tx, c.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for i := range c.Deviations {
			d := &c.Deviations[i]
			kt := keys[d.ItemID]
			d.Key, d.Title = kt[0], kt[1]
			if d.Kind == TypeMilestone {
				if ok, err := r.can(ctx, tx, d.ItemID); err != nil {
					return nil, err
				} else if !ok {
					d.Restricted, d.Title = true, ""
				}
			}
		}
		if !budgetOK {
			c.BudgetPct = nil
		}
		c.CanSubmit = canPlanIt && c.Status == "draft"
		kept = append(kept, c)
	}
	return kept, nil
}
