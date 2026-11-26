package work

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Sprint come elementi (architecture.md §12, punto 2): tipo "sprint" nello spazio del team,
// date in start_at/due_at, obiettivo in ext_sprint, appartenenza con la relazione
// "pianificato in". Avvio e chiusura sono passaggi del processo dello sprint: le regole
// sprint_start e sprint_close fotografano l'impegno e spostano gli incompiuti.

// sprintsOf elenca gli sprint dello spazio: pianificati e attivi (e i chiusi con all).
func sprintsOf(ctx context.Context, tx pgx.Tx, spaceID string, all bool) ([]SprintRef, error) {
	rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		       to_char(e.start_at at time zone 'UTC', 'YYYY-MM-DD'), to_char(e.due_at at time zone 'UTC', 'YYYY-MM-DD')
		from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.space_id = $1 and e.deleted_at is null and ($2 or coalesce(o.category, '') in ('todo', 'in_progress'))
		order by case o.category when 'in_progress' then 0 when 'todo' then 1 else 2 end,
		         case when o.category in ('done', 'cancelled') then null else e.start_at end nulls last,
		         e.start_at desc nulls last, e.number`, spaceID, all)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SprintRef, error) {
		var x SprintRef
		err := r.Scan(&x.ID, &x.Key, &x.Title, &x.State, &x.Category, &x.Start, &x.End)
		return x, err
	})
	if out == nil {
		out = []SprintRef{}
	}
	return out, err
}

// SprintInput crea o modifica uno sprint.
type SprintInput struct {
	Title *string `json:"title"`
	Goal  *string `json:"goal"`
	Start *string `json:"start"` // AAAA-MM-GG
	End   *string `json:"end"`
}

func parseDay(field string, v *string) (*time.Time, error) {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	d, err := time.Parse(time.DateOnly, strings.TrimSpace(*v))
	if err != nil {
		return nil, engine.InvalidCode("validation.date_invalid", field, "data non valida (AAAA-MM-GG)")
	}
	return &d, nil
}

// CreateSprint crea uno sprint nello spazio (pianificato).
func (s *Service) CreateSprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in SprintInput) (*engine.Element, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSprints(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	start, err := parseDay("start", in.Start)
	if err != nil {
		return nil, err
	}
	end, err := parseDay("end", in.End)
	if err != nil {
		return nil, err
	}
	if start != nil && end != nil && end.Before(*start) {
		return nil, engine.InvalidCode("validation.work.sprint_end_before_start", "end", "la fine viene prima dell'inizio")
	}
	title := ""
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
	}
	if title == "" {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) + 1 from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
			where e.space_id = $1`, sp.ID).Scan(&n); err != nil {
			return nil, err
		}
		title = "Sprint " + itoa(n)
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: sp.Key, TypeKey: TypeSprint, Title: title, DeferStart: true})
	if err != nil {
		return nil, err
	}
	goal := ""
	if in.Goal != nil {
		goal = strings.TrimSpace(*in.Goal)
	}
	if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, goal) values (app_tenant(), $1, $2)`, el.ID, goal); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update elements set start_at = $2, due_at = $3 where id = $1`, el.ID, start, end); err != nil {
		return nil, err
	}
	el.StartAt, el.DueAt = start, end
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	return s.eng.Get(ctx, tx, actor, el.ID)
}

func (s *Service) sprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != TypeSprint {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// UpdateSprint modifica titolo, obiettivo e date.
func (s *Service) UpdateSprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in SprintInput) (*engine.Element, error) {
	el, err := s.sprint(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSprints(ctx, tx, actor, el.SpaceID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" || len(t) > 200 {
			return nil, engine.TooLong("title", 200, "titolo obbligatorio, massimo 200 caratteri")
		}
		if _, err := tx.Exec(ctx, "update elements set title = $2, version = version + 1, updated_by = $3 where id = $1", el.ID, t, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
	}
	if in.Goal != nil {
		if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, goal) values (app_tenant(), $1, $2)
			on conflict (tenant_id, element_id) do update set goal = excluded.goal`, el.ID, strings.TrimSpace(*in.Goal)); err != nil {
			return nil, err
		}
	}
	if in.Start != nil || in.End != nil {
		start, end := el.StartAt, el.DueAt
		if in.Start != nil {
			if start, err = parseDay("start", in.Start); err != nil {
				return nil, err
			}
		}
		if in.End != nil {
			if end, err = parseDay("end", in.End); err != nil {
				return nil, err
			}
		}
		if start != nil && end != nil && end.Before(*start) {
			return nil, engine.InvalidCode("validation.work.sprint_end_before_start", "end", "la fine viene prima dell'inizio")
		}
		if _, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $3, version = version + 1 where id = $1", el.ID, start, end); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "sprint.update", in, nil); err != nil {
		return nil, err
	}
	return s.eng.Get(ctx, tx, actor, el.ID)
}

// phaseOfCategory restituisce la prima fase del processo dello sprint con la categoria.
func (s *Service) phaseOfCategory(ctx context.Context, tx pgx.Tx, el *engine.Element, cat string) (string, error) {
	states, _, err := s.eng.Phases(ctx, tx, el)
	if err != nil {
		return "", err
	}
	for _, st := range states {
		if st.Category == cat && !st.OffPath {
			return st.Name, nil
		}
	}
	return "", engine.InvalidParams("validation.work.sprint_no_phase", "sprint", map[string]any{"category": cat}, "il processo dello sprint non ha una fase %s", cat)
}

// StartSprint porta lo sprint nella fase attiva (gate: date e un solo sprint attivo).
func (s *Service) StartSprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.sprint(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSprints(ctx, tx, actor, el.SpaceID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	to, err := s.phaseOfCategory(ctx, tx, el, "in_progress")
	if err != nil {
		return nil, err
	}
	return s.eng.Transition(ctx, tx, actor, el.ID, to, "", engine.TransitionOpts{})
}

// CloseSprint sposta gli incompiuti (next = primo sprint pianificato, backlog, oppure la
// chiave di uno sprint) e chiude lo sprint.
func (s *Service) CloseSprint(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, moveTo string) (*engine.Element, error) {
	el, err := s.sprint(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.StateCategory != "in_progress" {
		return nil, engine.InvalidCode("validation.work.sprint_close_active", "sprint", "si chiude solo uno sprint attivo")
	}
	if ok, err := canManageSprints(ctx, tx, actor, el.SpaceID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	to, err := s.phaseOfCategory(ctx, tx, el, "done")
	if err != nil {
		return nil, err
	}
	if moveTo == "" {
		moveTo = "next"
	}
	if _, err := s.carryOver(ctx, tx, actor, el, moveTo); err != nil {
		return nil, err
	}
	return s.eng.Transition(ctx, tx, actor, el.ID, to, "", engine.TransitionOpts{})
}

// carryOver sposta gli elementi non chiusi dello sprint e li ricorda per il burndown.
func (s *Service) carryOver(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, moveTo string) (string, error) {
	relID, err := relationTypeID(ctx, tx, RelPlannedIn)
	if err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, `select e.id::text from relations r
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where r.target_id = $1 and r.type_id = $2 and coalesce(o.category, '') not in ('done', 'cancelled')
		order by e.rank collate "C" nulls last`, el.ID, relID)
	if err != nil {
		return "", err
	}
	open, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(open) == 0 {
		return "", err
	}
	target, label := "", "backlog"
	switch strings.ToLower(moveTo) {
	case "backlog":
	case "next":
		var id, key *string
		if err := tx.QueryRow(ctx, `select e.id::text, s.key || '-' || e.number from elements e
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
			join options o on o.tenant_id = e.tenant_id and o.id = e.state_id and o.category = 'todo'
			where e.space_id = $1 and e.id <> $2 and e.deleted_at is null
			order by e.start_at nulls last, e.number limit 1`, el.SpaceID, el.ID).Scan(&id, &key); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if id != nil {
			target, label = *id, *key
		}
	default:
		nx, err := s.sprint(ctx, tx, actor, moveTo)
		if err != nil {
			return "", engine.NotFound("move_to", "sprint", "sprint di destinazione inesistente")
		}
		if nx.SpaceID != el.SpaceID || nx.ID == el.ID || nx.StateCategory == "done" || nx.StateCategory == "cancelled" {
			return "", engine.InvalidCode("validation.work.sprint_move_target", "move_to", "la destinazione è uno sprint aperto dello stesso spazio, diverso da questo")
		}
		target, label = nx.ID, nx.Key
	}
	for _, id := range open {
		item, err := s.eng.Get(ctx, tx, engine.Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}, id)
		if err != nil {
			return "", err
		}
		if err := s.planIn(ctx, tx, actor, item, target); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, carried_over) values (app_tenant(), $1, $2::uuid[])
		on conflict (tenant_id, element_id) do update set carried_over = (select array(select distinct unnest(ext_sprint.carried_over || excluded.carried_over)))`,
		el.ID, open); err != nil {
		return "", err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "sprint.carry_over", EntityType: TypeSprint, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"items": len(open), "to": label}}); err != nil {
		return "", err
	}
	return itoa(len(open)) + " incompiuti spostati in " + label, nil
}

// actionSprintStart fotografa l'impegno: punti degli elementi pianificati (o il loro
// numero, se nessuno ha punti).
func (s *Service) actionSprintStart(ctx context.Context, tx pgx.Tx, _ engine.Actor, el *engine.Element, _ engine.Action) (string, error) {
	if el.TypeKey != TypeSprint {
		return "azione sprint_start valida solo sugli sprint", nil
	}
	var points float64
	var items int
	if err := tx.QueryRow(ctx, `select coalesce(sum(case when jsonb_typeof(e.fields->'`+FieldPoints+`') = 'number' then (e.fields->>'`+FieldPoints+`')::float8 end), 0),
		count(*)::int from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where r.target_id = $1 and coalesce(o.category, '') <> 'cancelled'`, el.ID).Scan(&points, &items); err != nil {
		return "", err
	}
	unit, committed := "points", points
	if points == 0 {
		unit, committed = "items", float64(items)
	}
	if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, started_at, committed, committed_unit)
		values (app_tenant(), $1, now(), $2, $3)
		on conflict (tenant_id, element_id) do update set started_at = coalesce(ext_sprint.started_at, now()),
		  committed = excluded.committed, committed_unit = excluded.committed_unit`, el.ID, committed, unit); err != nil {
		return "", err
	}
	if unit == "points" {
		return "impegno: " + fmtNum(committed) + " punti", nil
	}
	return "impegno: " + itoa(items) + " elementi (senza punti)", nil
}

// actionSprintClose sposta gli incompiuti (se non l'ha già fatto la chiusura con la scelta
// della destinazione) e registra l'ora di chiusura.
func (s *Service) actionSprintClose(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != TypeSprint {
		return "azione sprint_close valida solo sugli sprint", nil
	}
	if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, closed_at) values (app_tenant(), $1, now())
		on conflict (tenant_id, element_id) do update set closed_at = now()`, el.ID); err != nil {
		return "", err
	}
	note, err := s.carryOver(ctx, tx, actor, el, a.Ref)
	if err != nil {
		return "", err
	}
	if note == "" {
		return "", engine.ErrNoEffect
	}
	return note, nil
}

func fmtNum(f float64) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', -1, 64), ".", ",", 1)
}

// ---------------------------------------------------------------------------
// Backlog

// BacklogItem è un elemento del backlog o di uno sprint.
type BacklogItem struct {
	ID       string       `json:"id"`
	Key      string       `json:"key"`
	Title    string       `json:"title"`
	Type     string       `json:"type"`
	State    string       `json:"state"`
	Category string       `json:"category"`
	Assignee string       `json:"assignee,omitempty"`
	Priority string       `json:"priority,omitempty"`
	Points   *float64     `json:"points,omitempty"`
	Epic     string       `json:"epic,omitempty"`
	Sprint   string       `json:"sprint,omitempty"` // id dello sprint ("" = backlog)
	Tags     []engine.Tag `json:"tags,omitempty"`   // etichette
}

// SprintView è uno sprint con i suoi elementi e i totali.
type SprintView struct {
	SprintRef
	Goal      string  `json:"goal"`
	Points    float64 `json:"points"`
	Done      float64 `json:"points_done"`
	Items     int     `json:"items"`
	DoneItems int     `json:"items_done"`
}

// Backlog è la pagina "Backlog e sprint" di uno spazio.
type Backlog struct {
	Space      Ref               `json:"space"`
	Sprints    []SprintView      `json:"sprints"`
	Items      []BacklogItem     `json:"items"` // tutti: quelli con Sprint = "" sono il backlog
	Members    map[string]string `json:"members"`
	Epics      map[string]Ref    `json:"epics"`
	Priorities []Option          `json:"priorities"`
	CanManage  bool              `json:"can_manage"`
	CanWork    bool              `json:"can_work"`
	Truncated  bool              `json:"truncated"`
	// CreateTypes: i tipi che «Nuovo elemento» propone (quelli della board dello spazio).
	CreateTypes []CreateType `json:"create_types"`
	// Solo con la paginazione (BacklogPage.Limit > 0): totale del backlog senza sprint (calcolato
	// con Offset = 0) e se dopo la pagina ce ne sono altri. Gli elementi degli sprint sono sempre tutti.
	BacklogTotal int  `json:"backlog_total,omitempty"`
	More         bool `json:"more,omitempty"`
}

// BacklogPage chiede una pagina del solo backlog (gli elementi senza sprint); Limit = 0 è la
// risposta completa di sempre (fino a BoardLimit voci).
type BacklogPage struct{ Limit, Offset int }

// BacklogMaxPage è il massimo di voci del backlog per pagina.
const BacklogMaxPage = 2000

// GetBacklog restituisce sprint aperti e backlog (elementi non chiusi), nell'ordine manuale.
func (s *Service) GetBacklog(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey, q string) (*Backlog, error) {
	return s.GetBacklogPage(ctx, tx, actor, spaceKey, q, BacklogPage{})
}

// GetBacklogPage è GetBacklog con la paginazione del backlog.
func (s *Service) GetBacklogPage(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey, q string, page BacklogPage) (*Backlog, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	out := &Backlog{Space: Ref{Key: sp.Key, Title: sp.Name}, Items: []BacklogItem{}, Members: map[string]string{},
		Epics: map[string]Ref{}, Priorities: []Option{}, Sprints: []SprintView{}}
	if out.CanManage, err = canManageSprints(ctx, tx, actor, sp.ID); err != nil {
		return nil, err
	}
	if out.CanWork, err = canWork(ctx, tx, actor, sp.ID); err != nil {
		return nil, err
	}
	sprints, err := sprintsOf(ctx, tx, sp.ID, false)
	if err != nil {
		return nil, err
	}
	sprintIDs := []string{}
	for _, x := range sprints {
		v := SprintView{SprintRef: x}
		_ = tx.QueryRow(ctx, "select goal from ext_sprint where element_id = $1", x.ID).Scan(&v.Goal)
		out.Sprints = append(out.Sprints, v)
		sprintIDs = append(sprintIDs, x.ID)
	}
	itemKeys, err := workItemTypes(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	bst, err := s.Settings(ctx, tx, sp.ID)
	if err != nil {
		return nil, err
	}
	if out.CreateTypes, err = createTypesOf(ctx, tx, bst.Types); err != nil {
		return nil, err
	}
	tids, err := typeIDs(ctx, tx, itemKeys)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tids))
	for _, id := range tids {
		ids = append(ids, id)
	}
	// Backlog: elementi aperti; negli sprint anche quelli chiusi (per i totali e la lista).
	// Con la paginazione due letture: tutti gli elementi degli sprint e una pagina del resto.
	const planned = `select r.source_id as id from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		      where r.target_id = any($3)`
	const openIDs = `select x.id from elements x where x.space_id = $1 and x.type_id = any($2::uuid[]) and x.deleted_at is null and not x.is_draft and x.resolved_at is null`
	args := []any{sp.ID, ids, sprintIDs}
	base := `e.space_id = $1 and e.type_id = any($2::uuid[]) and e.deleted_at is null and not e.is_draft`
	if strings.TrimSpace(q) != "" {
		pred, a, err := s.eng.QueryPredicate(ctx, tx, actor, q, args)
		if err != nil {
			return nil, err
		}
		base += " and " + pred
		args = a
	}
	const lateral = `left join lateral (select r.target_id from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
		                   and rt.key = 'planned_in' where r.source_id = e.id limit 1) pl on true`
	members, epics := map[string]bool{}, map[string]bool{}
	read := func(idsSQL, where, tail string, args []any) error {
		rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, t.key, coalesce(o.name, ''), coalesce(o.category, ''),
		       coalesce(e.assignee_id::text, ''), coalesce(e.fields->>'`+FieldPriority+`', ''),
		       case when jsonb_typeof(e.fields->'`+FieldPoints+`') = 'number' then (e.fields->>'`+FieldPoints+`')::float8 end,
		       coalesce(case when pt.key = 'epic' then p.id::text end, case when gt.key = 'epic' then gp.id::text end, ''),
		       coalesce(pl.target_id::text, '')
		from (`+idsSQL+`) ids
		join elements e on e.id = ids.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join elements p on p.tenant_id = e.tenant_id and p.id = e.parent_id
		left join element_types pt on pt.tenant_id = p.tenant_id and pt.id = p.type_id
		left join elements gp on gp.tenant_id = p.tenant_id and gp.id = p.parent_id
		left join element_types gt on gt.tenant_id = gp.tenant_id and gt.id = gp.type_id
		`+lateral+`
		where `+where+`
		order by e.rank collate "C" nulls last, e.number `+tail, args...)
		if err != nil {
			return err
		}
		var it BacklogItem
		_, err = pgx.ForEachRow(rows, []any{&it.ID, &it.Key, &it.Title, &it.Type, &it.State, &it.Category, &it.Assignee, &it.Priority, &it.Points, &it.Epic, &it.Sprint},
			func() error {
				// Un elemento pianificato in uno sprint chiuso (dati vecchi) torna nel backlog.
				if it.Sprint != "" && !slices.Contains(sprintIDs, it.Sprint) {
					it.Sprint = ""
				}
				if it.Assignee != "" {
					members[it.Assignee] = true
				}
				if it.Epic != "" {
					epics[it.Epic] = true
				}
				out.Items = append(out.Items, it)
				it = BacklogItem{}
				return nil
			})
		return err
	}
	if page.Limit <= 0 {
		a := append(append([]any{}, args...), BoardLimit+1)
		err = read(openIDs+` union `+planned, base+` and (e.resolved_at is null or pl.target_id = any($3::uuid[]))`, `limit $`+itoa(len(a)), a)
		if err != nil {
			return nil, err
		}
		if len(out.Items) > BoardLimit {
			out.Items, out.Truncated = out.Items[:BoardLimit], true
		}
	} else {
		page.Limit = min(page.Limit, BacklogMaxPage)
		a := append(append([]any{}, args...), BoardLimit)
		if err = read(planned, base+` and pl.target_id = any($3::uuid[])`, `limit $`+itoa(len(a)), a); err != nil {
			return nil, err
		}
		nSprint := len(out.Items)
		backlogWhere := base + ` and e.resolved_at is null and (pl.target_id is null or pl.target_id <> all($3::uuid[]))`
		a = append(append([]any{}, args...), page.Limit+1, max(page.Offset, 0))
		if err = read(openIDs, backlogWhere, `limit $`+itoa(len(a)-1)+` offset $`+itoa(len(a)), a); err != nil {
			return nil, err
		}
		if len(out.Items)-nSprint > page.Limit {
			out.Items, out.More = out.Items[:nSprint+page.Limit], true
		}
		if page.Offset <= 0 && !out.More {
			out.BacklogTotal = len(out.Items) - nSprint // tutto il backlog è nella pagina: niente conteggio
		} else if page.Offset <= 0 {
			err = tx.QueryRow(ctx, `select count(*) from (`+openIDs+`) ids join elements e on e.id = ids.id `+lateral+` where `+backlogWhere, args...).Scan(&out.BacklogTotal)
			if err != nil {
				return nil, err
			}
		}
	}
	for i := range out.Sprints {
		v := &out.Sprints[i]
		for _, it := range out.Items {
			if it.Sprint != v.ID {
				continue
			}
			v.Items++
			p := 0.0
			if it.Points != nil {
				p = *it.Points
			}
			v.Points += p
			if it.Category == "done" {
				v.DoneItems++
				v.Done += p
			}
		}
	}
	ids2 := make([]string, len(out.Items))
	for i, it := range out.Items {
		ids2[i] = it.ID
	}
	tags, err := engine.TagsOf(ctx, tx, actor.MemberID, ids2)
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		out.Items[i].Tags = tags[out.Items[i].ID]
	}
	b := &Board{Members: out.Members, Epics: out.Epics, Priorities: out.Priorities, Settings: &BoardSettings{Swimlane: "none"}}
	if err := s.boardLabels(ctx, tx, sp.ID, b, members, epics); err != nil {
		return nil, err
	}
	out.Priorities = b.Priorities
	return out, nil
}
