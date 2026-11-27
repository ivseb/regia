package projects

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

// Dipendenze tra progetti (passo 6, progetti.md §5 e §6 «Programma e portafoglio»): un progetto,
// o una sua voce, ha bisogno di una milestone di un altro progetto entro una data, dentro un
// programma o fuori. La data che serve è quella impostata (need_by) oppure, senza, l'inizio della
// voce che dipende. Se la milestone è prevista oltre quella data, o la data è passata e la
// milestone non è raggiunta, la dipendenza è in ritardo: il progetto che dipende diventa giallo
// («external_late») e i suoi PM ricevono un avviso, una volta per slittamento (alerted_end).
//
// La dipendenza si vede da tutte e due le parti: nel piano del progetto che dipende (External) e
// in quello del progetto della milestone (Dependents). Le parti che l'attore non legge restano
// senza titolo, come nei conflitti.

// CrossDep è una dipendenza tra progetti.
type CrossDep struct {
	ID string `json:"id"`
	// Il progetto che dipende e la sua voce (nil = il progetto intero); nil se non leggibili.
	Project *Ref    `json:"project"`
	ItemID  *string `json:"item_id"`
	Item    *Ref    `json:"item"`
	// La milestone di cui ha bisogno e il suo progetto; nil se non leggibili.
	MilestoneID     string  `json:"milestone_id"`
	Milestone       *Ref    `json:"milestone"`
	MilestoneOf     *Ref    `json:"milestone_project"`
	MilestoneDate   *string `json:"milestone_date"`
	MilestoneBase   *string `json:"milestone_baseline"`
	MilestoneState  string  `json:"milestone_state_category"`
	// NeedBy è la data che serve (impostata o, senza, l'inizio della voce); NeedBySet quella impostata.
	NeedBy    *string `json:"need_by"`
	NeedBySet *string `json:"need_by_set"`
	Note      string  `json:"note"`
	// LateDays: di quanti giorni lavorativi la milestone va oltre la data che serve (0 = in tempo).
	LateDays int `json:"late_days"`
	// Restricted: una delle due parti non è leggibile («un altro progetto»).
	Restricted bool `json:"restricted,omitempty"`
	projectID  string
	providerID string
	alerted    *string
}

const depsSQL = `
	select d.id::text, d.project_id::text, ps.key || '-' || p.number, p.title,
	       d.item_id::text, case when i.id is not null then iss.key || '-' || i.number end, i.title, i.start_at,
	       d.milestone_id::text, ms.key || '-' || m.number, m.title, m.due_at, xm.baseline_date::timestamptz, coalesce(mo.category, ''),
	       xm.project_id::text, qs.key || '-' || q.number, q.title, d.need_by::timestamptz, d.note, d.alerted_end::timestamptz
	from project_dependencies d
	join elements p on p.tenant_id = d.tenant_id and p.id = d.project_id and p.deleted_at is null
	join spaces ps on ps.tenant_id = p.tenant_id and ps.id = p.space_id
	left join elements i on i.tenant_id = d.tenant_id and i.id = d.item_id and i.deleted_at is null
	left join spaces iss on iss.tenant_id = i.tenant_id and iss.id = i.space_id
	join elements m on m.tenant_id = d.tenant_id and m.id = d.milestone_id and m.deleted_at is null
	join spaces ms on ms.tenant_id = m.tenant_id and ms.id = m.space_id
	left join options mo on mo.tenant_id = m.tenant_id and mo.id = m.state_id
	join ext_milestone xm on xm.tenant_id = d.tenant_id and xm.element_id = d.milestone_id
	join elements q on q.tenant_id = xm.tenant_id and q.id = xm.project_id and q.deleted_at is null
	join spaces qs on qs.tenant_id = q.tenant_id and qs.id = q.space_id`

func scanDeps(rows pgx.Rows, now time.Time) ([]CrossDep, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CrossDep, error) {
		var d CrossDep
		var pj, ms, mp Ref
		var itemKey, itemTitle *string
		var itemStart, mDate, mBase, need, alerted *time.Time
		err := r.Scan(&d.ID, &d.projectID, &pj.Key, &pj.Title, &d.ItemID, &itemKey, &itemTitle, &itemStart,
			&d.MilestoneID, &ms.Key, &ms.Title, &mDate, &mBase, &d.MilestoneState, &d.providerID, &mp.Key, &mp.Title, &need, &d.Note, &alerted)
		if err != nil {
			return d, err
		}
		pj.ID, ms.ID, mp.ID = d.projectID, d.MilestoneID, d.providerID
		d.Project, d.Milestone, d.MilestoneOf = &pj, &ms, &mp
		if d.ItemID != nil && itemKey != nil {
			d.Item = &Ref{ID: *d.ItemID, Key: *itemKey, Title: deref(itemTitle)}
		}
		d.MilestoneDate, d.MilestoneBase = dateStr(mDate), dateStr(mBase)
		d.NeedBySet, d.alerted = dateStr(need), dateStr(alerted)
		d.NeedBy = d.NeedBySet
		if d.NeedBy == nil && d.Item != nil {
			d.NeedBy = dateStr(itemStart)
		}
		d.LateDays = depLate(d.NeedBy, d.MilestoneDate, d.MilestoneState, now)
		return d, nil
	})
}

// depLate: giorni lavorativi oltre la data che serve. Una milestone raggiunta (o annullata) non è
// in ritardo; una non raggiunta con la data passata vale come prevista oggi.
func depLate(need, date *string, state string, now time.Time) int {
	if need == nil || state == "done" || state == "cancelled" {
		return 0
	}
	n := parse(need)
	if n == nil {
		return 0
	}
	eff := now
	if d := parse(date); d != nil && d.After(now) {
		eff = *d
	}
	if !eff.After(*n) {
		return 0
	}
	return calendar.Workdays(n.AddDate(0, 0, 1), eff)
}

// loadDeps mette sul piano le dipendenze verso altri progetti (External) e quelle che altri
// progetti hanno sulle sue milestone (Dependents).
func loadDeps(ctx context.Context, tx pgx.Tx, p *Plan) error {
	p.External, p.Dependents = []CrossDep{}, []CrossDep{}
	rows, err := tx.Query(ctx, depsSQL+` where d.project_id = $1 or xm.project_id = $1 order by m.due_at nulls last, d.created_at`, p.Project.ID)
	if err != nil {
		return err
	}
	all, err := scanDeps(rows, today())
	if err != nil {
		return err
	}
	for _, d := range all {
		if d.projectID == p.Project.ID {
			p.External = append(p.External, d)
		} else {
			p.Dependents = append(p.Dependents, d)
		}
	}
	return nil
}

// limitDeps applica i limiti di visibilità del singolo elemento alle dipendenze.
func limitDeps(ctx context.Context, tx pgx.Tx, r *reader, ds []CrossDep) error {
	for i := range ds {
		d := &ds[i]
		for _, side := range []struct {
			proj, item **Ref
		}{{&d.Project, &d.Item}, {&d.MilestoneOf, &d.Milestone}} {
			if *side.proj != nil {
				ok, err := r.can(ctx, tx, (*side.proj).ID)
				if err != nil {
					return err
				}
				if !ok {
					*side.proj, *side.item, d.Restricted = nil, nil, true
					continue
				}
			}
			if *side.item != nil {
				ok, err := r.can(ctx, tx, (*side.item).ID)
				if err != nil {
					return err
				}
				if !ok {
					*side.item = nil
				}
			}
		}
	}
	return nil
}

// CrossDepInput crea una dipendenza da una milestone di un altro progetto.
type CrossDepInput struct {
	MilestoneID string  `json:"milestone_id"`
	ItemID      *string `json:"item_id"`
	NeedBy      *string `json:"need_by"`
	Note        string  `json:"note"`
}

// CreateCrossDep: il progetto (o una sua voce) dipende da una milestone di un altro progetto.
// Serve poter pianificare il progetto e leggere la milestone.
func (s *Service) CreateCrossDep(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectRef string, in CrossDepInput) (*Plan, error) {
	proj, err := s.project(ctx, tx, actor, projectRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	mID := resolveID(ctx, tx, strings.TrimSpace(in.MilestoneID))
	var other string
	if engine.IsUUID(mID) {
		_ = tx.QueryRow(ctx, "select project_id::text from ext_milestone where element_id = $1", mID).Scan(&other)
	}
	if other == "" {
		return nil, engine.NotFound("milestone_id", "milestone", "milestone inesistente")
	}
	if ok, err := s.eng.CanRead(ctx, tx, actor, mID); err != nil || !ok {
		return nil, firstErr(err, engine.NotFound("milestone_id", "milestone", "milestone inesistente"))
	}
	if other == proj.ID {
		return nil, engine.InvalidCode("validation.projects.xdep_same", "milestone_id", "la milestone è di questo progetto: usa le dipendenze del piano")
	}
	var itemID any
	if in.ItemID != nil && strings.TrimSpace(*in.ItemID) != "" {
		id := resolveID(ctx, tx, strings.TrimSpace(*in.ItemID))
		pid := ""
		if engine.IsUUID(id) {
			pid, _, _ = itemProject(ctx, tx, id)
		}
		if pid != proj.ID {
			return nil, engine.InvalidCode("validation.projects.xdep_item", "item_id", "la voce non è di questo progetto")
		}
		itemID = id
	}
	var need any
	if in.NeedBy != nil && strings.TrimSpace(*in.NeedBy) != "" {
		d, err := calendar.Parse(strings.TrimSpace(*in.NeedBy))
		if err != nil {
			return nil, engine.InvalidValue("need_by", "data non valida (AAAA-MM-GG)")
		}
		need = d.Format(time.DateOnly)
	}
	if itemID == nil && need == nil {
		return nil, engine.InvalidCode("validation.projects.xdep_need", "need_by", "indica la data che serve o la voce che dipende")
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 500 {
		return nil, engine.TooLong("note", 500, "nota troppo lunga (massimo 500 caratteri)")
	}
	var id string
	err = tx.QueryRow(ctx, `insert into project_dependencies (tenant_id, project_id, item_id, milestone_id, need_by, note, created_by)
		values (app_tenant(), $1, $2, $3, $4::date, $5, $6) returning id::text`, proj.ID, itemID, mID, need, note, nullable(actor.MemberID)).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, engine.InvalidCode("validation.projects.xdep_exists", "milestone_id", "la dipendenza c'è già")
	}
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"dependency": id, "milestone": mID, "item": itemID, "need_by": need}
	if err := engine.AddActivity(ctx, tx, proj.ID, actor.MemberID, "plan", "cross_dependency", payload, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.xdep.create", EntityType: TypeProject, EntityID: proj.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload}); err != nil {
		return nil, err
	}
	if err := s.notifyDeps(ctx, tx, actor, proj.ID); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, tx, actor, proj.ID)
}

// DeleteCrossDep toglie la dipendenza (chi pianifica il progetto che dipende).
func (s *Service) DeleteCrossDep(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectRef, id string) (*Plan, error) {
	proj, err := s.project(ctx, tx, actor, projectRef)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, proj.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if !engine.IsUUID(id) {
		return nil, engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, "delete from project_dependencies where id = $1 and project_id = $2", id, proj.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.ErrNotFound
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.xdep.delete", EntityType: TypeProject, EntityID: proj.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"dependency": id}}); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, tx, actor, proj.ID)
}

// DepCandidate è una milestone di un altro progetto da cui si può dipendere.
type DepCandidate struct {
	Milestone     Ref     `json:"milestone"`
	Project       Ref     `json:"project"`
	Date          *string `json:"date"`
	StateCategory string  `json:"state_category"`
	// SameProgram: il progetto della milestone sta nello stesso programma (si propone per primo).
	SameProgram bool `json:"same_program"`
}

// DepCandidates elenca le milestone aperte degli altri progetti aperti che l'attore legge: prima
// quelle dello stesso programma, poi per data.
func (s *Service) DepCandidates(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectRef string) ([]DepCandidate, error) {
	proj, err := s.project(ctx, tx, actor, projectRef)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select m.id::text, ms.key || '-' || m.number, m.title, q.id::text, qs.key || '-' || q.number, q.title,
		m.due_at, coalesce(mo.category, ''), coalesce(xq.program_id is not null and xq.program_id = xp.program_id, false)
		from ext_milestone xm
		join elements m on m.tenant_id = xm.tenant_id and m.id = xm.element_id and m.deleted_at is null
		join spaces ms on ms.tenant_id = m.tenant_id and ms.id = m.space_id
		left join options mo on mo.tenant_id = m.tenant_id and mo.id = m.state_id
		join elements q on q.tenant_id = xm.tenant_id and q.id = xm.project_id and q.deleted_at is null and not q.is_draft
		join spaces qs on qs.tenant_id = q.tenant_id and qs.id = q.space_id
		left join options qo on qo.tenant_id = q.tenant_id and qo.id = q.state_id
		left join ext_project xq on xq.tenant_id = q.tenant_id and xq.element_id = q.id
		left join ext_project xp on xp.tenant_id = xm.tenant_id and xp.element_id = $1
		where xm.project_id <> $1 and coalesce(mo.category, '') not in ('done', 'cancelled') and coalesce(qo.category, '') not in ('done', 'cancelled')
		order by 9 desc, m.due_at nulls last, q.number, m.number limit 300`, proj.ID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (DepCandidate, error) {
		var c DepCandidate
		var due *time.Time
		err := r.Scan(&c.Milestone.ID, &c.Milestone.Key, &c.Milestone.Title, &c.Project.ID, &c.Project.Key, &c.Project.Title, &due, &c.StateCategory, &c.SameProgram)
		c.Date = dateStr(due)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	out := []DepCandidate{}
	rd := s.reader(actor)
	for _, c := range list {
		okM, err := rd.can(ctx, tx, c.Milestone.ID)
		if err != nil {
			return nil, err
		}
		okP, err := rd.can(ctx, tx, c.Project.ID)
		if err != nil {
			return nil, err
		}
		if okM && okP {
			out = append(out, c)
		}
	}
	return out, nil
}

// notifyDeps controlla le dipendenze che toccano il progetto (le sue verso altri e quelle degli
// altri sulle sue milestone): quando una milestone va oltre la data che serve avvisa i PM del
// progetto che dipende, una volta per data della milestone; quando rientra dimentica l'avviso.
func (s *Service) notifyDeps(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID string) error {
	rows, err := tx.Query(ctx, depsSQL+` where d.project_id = $1 or xm.project_id = $1`, projectID)
	if err != nil {
		return err
	}
	ds, err := scanDeps(rows, today())
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.LateDays == 0 {
			if d.alerted != nil {
				if _, err := tx.Exec(ctx, "update project_dependencies set alerted_end = null where id = $1", d.ID); err != nil {
					return err
				}
			}
			continue
		}
		mark := d.MilestoneDate
		if mark == nil || parse(mark).Before(today()) {
			t := today().Format(time.DateOnly)
			mark = &t
		}
		// Già segnalata per questa data (o, con la data passata, già segnalata dopo la scadenza).
		if d.alerted != nil && (*d.alerted == *mark || d.MilestoneDate == nil || parse(d.MilestoneDate).Before(today())) {
			continue
		}
		if _, err := tx.Exec(ctx, "update project_dependencies set alerted_end = $2::date where id = $1", d.ID, *mark); err != nil {
			return err
		}
		payload := map[string]any{"project": d.Project.Key, "other": d.MilestoneOf.Key, "milestone": d.Milestone.Key,
			"need": *d.NeedBy, "date": "", "days": d.LateDays}
		if d.MilestoneDate != nil {
			payload["date"] = *d.MilestoneDate
		}
		if d.Item != nil {
			payload["item"] = d.Item.Key
		}
		if err := s.notifyPM(ctx, tx, actor, d.projectID, d.projectID, "dependency_late", payload); err != nil {
			return err
		}
	}
	return nil
}
