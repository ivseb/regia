package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Board del team: colonne = fasi del processo (più fasi per colonna se serve), limite WIP
// per colonna, swimlane per assegnatario, epic o priorità, card nell'ordine manuale.
// La configurazione è una vista (tabella views, kind "board") dello spazio.

// BoardColumn è una colonna: nome, fasi che raccoglie, limite WIP (0 = nessuno).
type BoardColumn struct {
	Name   string   `json:"name"`
	Phases []string `json:"phases"`
	WIP    int      `json:"wip"`
	Color  string   `json:"color,omitempty"`
	// TypePhases: per un tipo di elemento le fasi della colonna sono queste e non Phases (processi
	// diversi per tipo). Un tipo senza voce usa Phases.
	TypePhases map[string][]string `json:"type_phases,omitempty"`
}

// phasesFor sono le fasi della colonna per un tipo di elemento.
func (c BoardColumn) phasesFor(typ string) []string {
	if p := c.TypePhases[typ]; len(p) > 0 {
		return p
	}
	return c.Phases
}

func hasPhase(list []string, name string) bool {
	return slices.ContainsFunc(list, func(p string) bool { return strings.EqualFold(p, name) })
}

// BoardSettings è la configurazione della board di uno spazio.
type BoardSettings struct {
	Columns  []BoardColumn `json:"columns"`
	Swimlane string        `json:"swimlane"`            // none | assignee | epic | priority
	Types    []string      `json:"types"`               // tipi delle card
	DoneDays int           `json:"done_days"`           // le card chiuse restano visibili per N giorni (fuori dagli sprint)
	Custom   bool          `json:"custom"`              // false = colonne di default dalle fasi dei processi
	Model    string        `json:"model,omitempty"`     // modello applicato (scrum, kanban, supporto) o vuoto
	NoSprint bool          `json:"no_sprint,omitempty"` // flusso continuo: la board non mostra gli sprint
	Sort     string        `json:"sort,omitempty"`      // "" = ordine manuale, "due" = per scadenza
}

// columnOf è la colonna di una card (per tipo e fase), -1 se la fase non ha colonna.
func (st *BoardSettings) columnOf(typ, phase string) int {
	for i, c := range st.Columns {
		if hasPhase(c.phasesFor(typ), phase) {
			return i
		}
	}
	return -1
}

var swimlanes = []string{"none", "assignee", "epic", "priority"}

// defaultColumns: le fasi dei processi dei tipi, unite per nome, nell'ordine (da fare, in
// corso, fatto); le fasi annullate restano fuori dalla board.
func defaultColumns(ctx context.Context, tx pgx.Tx, types []string) ([]BoardColumn, error) {
	rank := map[string]int{"todo": 0, "in_progress": 1, "done": 2}
	type ph struct {
		name string
		cat  int
	}
	var list []ph
	index := func(name string) int {
		return slices.IndexFunc(list, func(p ph) bool { return strings.EqualFold(p.name, name) })
	}
	for _, tk := range types {
		t, err := engine.LoadType(ctx, tx, tk)
		if errors.Is(err, engine.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Una fase nuova di un altro tipo va subito dopo la fase che la precede nel suo processo.
		at := -1
		for _, st := range t.States {
			c, ok := rank[st.Category]
			if !ok {
				continue
			}
			if i := index(st.Name); i >= 0 {
				at = i
				continue
			}
			pos := len(list)
			if at >= 0 {
				pos = at + 1
			}
			list = slices.Insert(list, pos, ph{name: st.Name, cat: c})
			at = pos
		}
	}
	slices.SortStableFunc(list, func(a, b ph) int { return a.cat - b.cat })
	out := make([]BoardColumn, 0, len(list))
	for _, p := range list {
		out = append(out, BoardColumn{Name: p.name, Phases: []string{p.name}})
	}
	return out, nil
}

// boardOrder: ordine manuale, o per scadenza (la più vicina prima) se il modello lo chiede.
func boardOrder(st *BoardSettings) string {
	if st.Sort == "due" {
		return `e.due_at nulls last, e.rank collate "C" nulls last, e.number`
	}
	return `e.rank collate "C" nulls last, e.number`
}

func boardKey(spaceID string) string { return "board:" + spaceID }

// Settings restituisce la configurazione della board (quella di default se non salvata).
func (s *Service) Settings(ctx context.Context, tx pgx.Tx, spaceID string) (*BoardSettings, error) {
	var raw []byte
	err := tx.QueryRow(ctx, "select settings from views where system_key = $1", boardKey(spaceID)).Scan(&raw)
	st := &BoardSettings{}
	if err == nil {
		if err := json.Unmarshal(raw, st); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Solo tipi del lavoro non archiviati: un tipo archiviato sparisce dalla board.
	current, err := workItemTypes(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	if len(st.Types) == 0 {
		if st.Types, err = workItemTypes(ctx, tx, false); err != nil {
			return nil, err
		}
	} else {
		st.Types = slices.DeleteFunc(st.Types, func(t string) bool { return !slices.Contains(current, t) })
	}
	if !slices.Contains(swimlanes, st.Swimlane) || (st.Swimlane == "epic" && !slices.Contains(current, TypeEpic)) {
		st.Swimlane = "none"
	}
	if typeArchived(ctx, tx, TypeSprint) {
		st.NoSprint = true // senza il tipo sprint la board è un flusso continuo
	}
	if st.DoneDays <= 0 {
		st.DoneDays = 14
	}
	if !st.Custom || len(st.Columns) == 0 {
		cols, err := defaultColumns(ctx, tx, st.Types)
		if err != nil {
			return nil, err
		}
		// I limiti WIP salvati valgono anche sulle colonne di default (per nome).
		for i := range cols {
			for _, c := range st.Columns {
				if strings.EqualFold(c.Name, cols[i].Name) {
					cols[i].WIP = c.WIP
				}
			}
		}
		st.Columns = cols
	}
	return st, nil
}

// SaveSettings salva la configurazione della board.
func (s *Service) SaveSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in BoardSettings) (*BoardSettings, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSpace(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if err := validateSettings(ctx, tx, &in, true); err != nil {
		return nil, err
	}
	if err := s.storeSettings(ctx, tx, sp.ID, sp.Key, sp.Name, in); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "board.settings", EntityType: "space", EntityID: sp.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"swimlane": in.Swimlane, "columns": len(in.Columns)}}); err != nil {
		return nil, err
	}
	return s.Settings(ctx, tx, sp.ID)
}

// validateSettings controlla la configurazione (nomi, WIP, swimlane, tipi). requirePhases: ogni
// colonna di una board personalizzata deve avere almeno una fase (la bozza può essere incompleta).
func validateSettings(ctx context.Context, tx pgx.Tx, in *BoardSettings, requirePhases bool) error {
	if !slices.Contains(swimlanes, in.Swimlane) {
		return engine.InvalidCode("validation.work.swimlane_invalid", "swimlane", "swimlane: none, assignee, epic o priority")
	}
	allowed, err := workItemTypes(ctx, tx, true)
	if err != nil {
		return err
	}
	for _, t := range in.Types {
		if !slices.Contains(allowed, t) {
			return engine.InvalidValue("types", "tipo %q non ammesso sulla board", t)
		}
	}
	if in.DoneDays < 0 || in.DoneDays > 365 {
		return engine.OutOfRange("done_days", 0, 365, "tra 0 e 365 giorni")
	}
	if in.Sort != "" && in.Sort != "due" {
		return engine.InvalidCode("validation.board.sort_invalid", "sort", "ordinamento: manuale o per scadenza")
	}
	if len(in.Columns) > 20 {
		return engine.InvalidCode("validation.work.columns_too_many", "columns", "al massimo 20 colonne")
	}
	names := map[string]bool{}
	for i, c := range in.Columns {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" || len(c.Name) > 60 {
			return engine.TooLong("columns", 60, "colonna %d: nome obbligatorio (massimo 60 caratteri)", i+1)
		}
		if names[strings.ToLower(c.Name)] {
			return engine.InvalidParams("validation.work.column_duplicate", "columns", map[string]any{"name": c.Name}, "due colonne con il nome %q", c.Name)
		}
		names[strings.ToLower(c.Name)] = true
		if c.WIP < 0 || c.WIP > 999 {
			return engine.InvalidParams("validation.work.column_wip", "columns", map[string]any{"name": c.Name}, "colonna %q: limite WIP tra 0 (nessuno) e 999", c.Name)
		}
		if requirePhases && in.Custom && len(c.Phases) == 0 {
			return engine.InvalidParams("validation.work.column_no_phase", "columns", map[string]any{"name": c.Name}, "colonna %q: indica almeno una fase", c.Name)
		}
		in.Columns[i].Name = c.Name
		if len(c.Color) > 20 {
			return engine.TooLong("columns", 20, "colonna %d: colore troppo lungo", i+1)
		}
		for tk := range c.TypePhases {
			if !slices.Contains(in.Types, tk) {
				return engine.InvalidParams("validation.board.type_unknown", "columns", map[string]any{"name": c.Name, "type": tk}, "colonna %q: il tipo %q non è sulla board", c.Name, tk)
			}
		}
	}
	return nil
}

// storeSettings scrive la configurazione pubblicata della board dello spazio.
func (s *Service) storeSettings(ctx context.Context, tx pgx.Tx, spaceID, spaceKey, spaceName string, in BoardSettings) error {
	raw, _ := json.Marshal(in)
	_, err := tx.Exec(ctx, `insert into views (tenant_id, id, name, kind, settings, space_id, shared, system_key)
		values (app_tenant(), $1, $2, 'board', $3, $4, true, $5)
		on conflict (tenant_id, system_key) where system_key is not null do update set settings = excluded.settings, updated_at = now()`,
		db.NewID(), "Board "+spaceKey, raw, spaceID, boardKey(spaceID))
	return err
}

// Card è una card della board, in forma compatta (migliaia per risposta).
type Card struct {
	ID       string       `json:"id"`
	Key      string       `json:"key"`
	Title    string       `json:"title"`
	Type     string       `json:"type"`
	State    string       `json:"state"`
	Col      int          `json:"col"`            // indice della colonna
	Lane     string       `json:"lane,omitempty"` // valore della swimlane ("" = senza)
	Assignee string       `json:"assignee,omitempty"`
	Priority string       `json:"priority,omitempty"` // id dell'opzione
	Points   *float64     `json:"points,omitempty"`
	Estimate *int64       `json:"estimate,omitempty"` // minuti
	Parent   string       `json:"parent,omitempty"`
	Epic     string       `json:"epic,omitempty"`
	Children int          `json:"children,omitempty"`
	Done     int          `json:"children_done,omitempty"`
	Version  int          `json:"version"`
	Resolved bool         `json:"resolved,omitempty"`
	Tags     []engine.Tag `json:"tags,omitempty"` // etichette
}

// ColumnView è una colonna con i conteggi.
type ColumnView struct {
	BoardColumn
	Count    int    `json:"count"`
	Category string `json:"category"`
	Over     bool   `json:"over"` // limite WIP superato
}

// Lane è una swimlane.
type Lane struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Option è un'opzione di un campo (priorità).
type Option struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// SprintRef è uno sprint selezionabile sulla board.
type SprintRef struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	State    string  `json:"state"`
	Category string  `json:"category"`
	Start    *string `json:"start"`
	End      *string `json:"end"`
}

// Board è la board di uno spazio.
type Board struct {
	Space      Ref               `json:"space"`
	Team       *string           `json:"team"`
	Settings   *BoardSettings    `json:"settings"`
	Columns    []ColumnView      `json:"columns"`
	Lanes      []Lane            `json:"lanes"`
	Cards      []Card            `json:"cards"`
	Hidden     int               `json:"hidden"` // card in fasi senza colonna (es. annullate)
	Members    map[string]string `json:"members"`
	Epics      map[string]Ref    `json:"epics"`
	Priorities []Option          `json:"priorities"`
	// CreateTypes: i tipi della board che «Nuovo elemento» propone, nell'ordine della board.
	CreateTypes []CreateType `json:"create_types"`
	Sprints     []SprintRef  `json:"sprints"`
	Sprint      *SprintRef   `json:"sprint"` // sprint mostrato (nil = tutto lo spazio)
	CanManage   bool         `json:"can_manage"`
	CanWork     bool         `json:"can_work"`
	Truncated   bool         `json:"truncated"`
	// Paged: la board legge tutto lo spazio (nessuno sprint) ed è paginata per colonna: Cards ha le
	// prime PageSize card di ogni colonna (nell'ordine della board) e Columns[].Count è il totale esatto.
	// Le successive si chiedono con col e after (BoardQuery).
	Paged    bool `json:"paged,omitempty"`
	PageSize int  `json:"page_size,omitempty"`
}

// BoardPageSize è il numero di card per colonna nella board paginata (chiedibile fino a BoardMaxPage).
const (
	BoardPageSize = 50
	BoardMaxPage  = 200
)

// BoardLimit è il massimo di card per risposta: oltre si chiede di filtrare.
const BoardLimit = 20000

// BoardQuery: q = query del linguaggio, sprint = chiave dello sprint, "tutti" o vuoto (lo
// sprint attivo se c'è), swimlane = sostituisce quella salvata.
//
// Senza sprint (la board legge tutto lo spazio) la risposta è paginata per colonna: Col (indice)
// con After (id dell'ultima card già ricevuta della colonna) dà le successive Limit card di quella
// colonna, solo quelle (Columns resta con i conteggi esatti).
type BoardQuery struct {
	Q        string
	Sprint   string
	Swimlane string
	Col      *int
	After    string
	Limit    int
}

// GetBoard restituisce la board dello spazio.
func (s *Service) GetBoard(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, bq BoardQuery) (*Board, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	st, err := s.Settings(ctx, tx, sp.ID)
	if err != nil {
		return nil, err
	}
	if bq.Swimlane != "" && slices.Contains(swimlanes, bq.Swimlane) && (bq.Swimlane != "epic" || !typeArchived(ctx, tx, TypeEpic)) {
		st.Swimlane = bq.Swimlane
	}
	createTypes, err := createTypesOf(ctx, tx, st.Types)
	if err != nil {
		return nil, err
	}
	out := &Board{CreateTypes: createTypes, Space: Ref{Key: sp.Key, Title: sp.Name}, Team: sp.Team, Settings: st, Lanes: []Lane{}, Cards: []Card{},
		Members: map[string]string{}, Epics: map[string]Ref{}, Priorities: []Option{}}
	if out.CanManage, err = canManageSpace(ctx, tx, actor, sp.ID); err != nil {
		return nil, err
	}
	if out.CanWork, err = canWork(ctx, tx, actor, sp.ID); err != nil {
		return nil, err
	}
	if out.Sprints, err = sprintsOf(ctx, tx, sp.ID, false); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(bq.Sprint)) {
	case "tutti", "all":
	case "":
		for i := range out.Sprints {
			if st.NoSprint { // flusso continuo: la board è tutto lo spazio
				break
			}
			if out.Sprints[i].Category == "in_progress" {
				out.Sprint = &out.Sprints[i]
				break
			}
		}
	default:
		for i := range out.Sprints {
			if strings.EqualFold(out.Sprints[i].Key, bq.Sprint) {
				out.Sprint = &out.Sprints[i]
			}
		}
		if out.Sprint == nil {
			return nil, engine.NotFound("sprint", "sprint", "sprint %s inesistente nello spazio %s", bq.Sprint, sp.Key)
		}
	}
	tids, err := typeIDs(ctx, tx, st.Types)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tids))
	for _, id := range tids {
		ids = append(ids, id)
	}
	colCache := map[string]int{}
	args := []any{sp.ID, ids}
	where := `e.space_id = $1 and e.type_id = any($2::uuid[]) and e.deleted_at is null and not e.is_draft`
	// Con uno sprint le card si cercano partendo dai suoi elementi (le relazioni «pianificato in») e
	// non da tutti gli elementi dello spazio, che con 300.000 elementi sono migliaia e il piano li
	// leggeva tutti (26 ms per 209 card, ora 11). Senza sprint («tutti») la lettura dello spazio resta
	// la più veloce: provato a partire dagli aperti e dai chiusi da poco, costava di più (36 contro 29 ms).
	from := `elements e`
	if out.Sprint != nil {
		args = append(args, out.Sprint.ID)
		from = `(select r.source_id as id from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
			where r.target_id = $3) ids
		join elements e on e.id = ids.id`
	} else {
		args = append(args, time.Now().AddDate(0, 0, -st.DoneDays))
		where += ` and (e.resolved_at is null or e.resolved_at >= $3)`
	}
	if strings.TrimSpace(bq.Q) != "" {
		pred, a, err := s.eng.QueryPredicate(ctx, tx, actor, bq.Q, args)
		if err != nil {
			return nil, err
		}
		where += " and " + pred
		args = a
	}
	members := map[string]bool{}
	epics := map[string]bool{}
	cats := make([]string, len(st.Columns))
	counts := make([]int, len(st.Columns))
	out.Paged = out.Sprint == nil
	if bq.Col != nil && !out.Paged {
		return nil, engine.InvalidCode("validation.board.page_needs_all", "col", "le pagine per colonna valgono solo senza sprint (tutto lo spazio)")
	}
	if bq.Col != nil && (*bq.Col < 0 || *bq.Col >= len(st.Columns)) {
		return nil, engine.NotFound("column", "", "colonna inesistente")
	}
	const cardSelect = `
		select e.id::text, s.key || '-' || e.number, e.title, t.key, coalesce(o.name, ''), coalesce(o.category, ''),
		       coalesce(e.assignee_id::text, ''), coalesce(e.fields->>'` + FieldPriority + `', ''),
		       case when jsonb_typeof(e.fields->'` + FieldPoints + `') = 'number' then (e.fields->>'` + FieldPoints + `')::float8 end,
		       case when jsonb_typeof(e.fields->'` + FieldEstimate + `') = 'number' then (e.fields->>'` + FieldEstimate + `')::float8::bigint end,
		       coalesce(e.parent_id::text, ''),
		       coalesce(case when pt.key = 'epic' then p.id::text end, case when gt.key = 'epic' then gp.id::text end, ''),
		       coalesce(ch.total, 0), coalesce(ch.done, 0), e.version, e.resolved_at is not null
		from %s
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join elements p on p.tenant_id = e.tenant_id and p.id = e.parent_id
		left join element_types pt on pt.tenant_id = p.tenant_id and pt.id = p.type_id
		left join elements gp on gp.tenant_id = p.tenant_id and gp.id = p.parent_id
		left join element_types gt on gt.tenant_id = gp.tenant_id and gt.id = gp.type_id
		left join lateral (
			select count(*)::int as total, count(*) filter (where co.category = 'done')::int as done
			from elements c left join options co on co.tenant_id = c.tenant_id and co.id = c.state_id
			where c.parent_id = e.id and c.deleted_at is null and coalesce(co.category, '') <> 'cancelled') ch on true
		where %s
		order by %s
		limit $%d`
	// scan legge le card dalle righe; in modalità paginata conteggi e card nascoste vengono dai gruppi.
	scan := func(rows pgx.Rows) error {
		var c Card
		var cat string
		_, err := pgx.ForEachRow(rows, []any{&c.ID, &c.Key, &c.Title, &c.Type, &c.State, &cat, &c.Assignee, &c.Priority, &c.Points, &c.Estimate,
			&c.Parent, &c.Epic, &c.Children, &c.Done, &c.Version, &c.Resolved}, func() error {
			ck := c.Type + "\x00" + strings.ToLower(c.State)
			col, ok := colCache[ck]
			if !ok {
				col = st.columnOf(c.Type, c.State)
				colCache[ck] = col
			}
			if col < 0 {
				if !out.Paged {
					out.Hidden++
				}
				return nil
			}
			c.Col = col
			if !out.Paged {
				counts[col]++
				if cats[col] == "" {
					cats[col] = cat
				}
			}
			if c.Assignee != "" {
				members[c.Assignee] = true
			}
			if c.Epic != "" {
				epics[c.Epic] = true
			}
			switch st.Swimlane {
			case "assignee":
				c.Lane = c.Assignee
			case "epic":
				c.Lane = c.Epic
			case "priority":
				c.Lane = c.Priority
			}
			out.Cards = append(out.Cards, c)
			c = Card{}
			return nil
		})
		return err
	}
	if out.Paged {
		if err := s.pagedCards(ctx, tx, st, out, bq, from, where, args, counts, cats, cardSelect, scan); err != nil {
			return nil, err
		}
	} else {
		args = append(args, BoardLimit+1)
		rows, err := tx.Query(ctx, fmt.Sprintf(cardSelect, from, where, boardOrder(st), len(args)), args...)
		if err != nil {
			return nil, err
		}
		if err := scan(rows); err != nil {
			return nil, err
		}
		if len(out.Cards) > BoardLimit {
			out.Cards = out.Cards[:BoardLimit]
			out.Truncated = true
		}
	}
	if err := attachCardTags(ctx, tx, actor.MemberID, out.Cards); err != nil {
		return nil, err
	}
	for i, col := range st.Columns {
		cv := ColumnView{BoardColumn: col, Count: counts[i], Category: cats[i]}
		cv.Over = col.WIP > 0 && counts[i] > col.WIP
		if cv.Phases == nil {
			cv.Phases = []string{}
		}
		out.Columns = append(out.Columns, cv)
	}
	if err := s.boardLabels(ctx, tx, sp.ID, out, members, epics); err != nil {
		return nil, err
	}
	return out, nil
}

// boardLabels completa nomi delle persone, epic, priorità e swimlane.
func (s *Service) boardLabels(ctx context.Context, tx pgx.Tx, spaceID string, out *Board, members, epics map[string]bool) error {
	// Persone: chi ha card, più i membri del team (per assegnare e per le swimlane vuote).
	rows, err := tx.Query(ctx, `select m.id::text, m.display_name from members m
		where m.id::text = any($1) or m.id in (select tm.member_id from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		                                     where t.delivery_space_id = $2)
		   or m.id in (select lead_id from teams where delivery_space_id = $2)
		order by m.display_name`, keysOf(members), spaceID)
	if err != nil {
		return err
	}
	var id, name string
	var order []string
	if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error {
		out.Members[id] = name
		order = append(order, id)
		return nil
	}); err != nil {
		return err
	}
	// Epic dello spazio ancora aperti (swimlane e scelta del genitore) più quelli delle card.
	// Gli epic aperti dello spazio si leggono dall'indice (spazio, tipo) delle voci aperte; con
	// `id::text = any(...)` in OR Postgres scorreva tutta la tabella (700 ms su 300.000 elementi).
	rows, err = tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, '') from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and e.id in (
			select x.id from elements x where x.space_id = $2 and x.type_id = (select id from element_types where key = 'epic')
			   and x.resolved_at is null and x.deleted_at is null and not x.is_draft
			union select unnest($1::uuid[]))
		order by e.rank collate "C" nulls last, e.number`, keysOf(epics), spaceID)
	if err != nil {
		return err
	}
	var r Ref
	var epicOrder []string
	if _, err := pgx.ForEachRow(rows, []any{&id, &r.Key, &r.Title, &r.State}, func() error {
		out.Epics[id] = r
		epicOrder = append(epicOrder, id)
		return nil
	}); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `select o.id::text, o.name, coalesce(o.color, '') from field_defs f
		join options o on o.tenant_id = f.tenant_id and o.option_set_id = f.option_set_id and not o.archived
		where f.key = $1 order by o.ordinal`, FieldPriority)
	if err != nil {
		return err
	}
	var o Option
	if _, err := pgx.ForEachRow(rows, []any{&o.ID, &o.Name, &o.Color}, func() error {
		out.Priorities = append(out.Priorities, o)
		return nil
	}); err != nil {
		return err
	}
	switch out.Settings.Swimlane {
	case "assignee":
		for _, id := range order {
			out.Lanes = append(out.Lanes, Lane{Key: id, Name: out.Members[id]})
		}
		out.Lanes = append(out.Lanes, Lane{Key: "", Name: "Senza assegnatario"})
	case "epic":
		for _, id := range epicOrder {
			out.Lanes = append(out.Lanes, Lane{Key: id, Name: out.Epics[id].Key + " " + out.Epics[id].Title})
		}
		out.Lanes = append(out.Lanes, Lane{Key: "", Name: "Senza epic"})
	case "priority":
		for _, p := range out.Priorities {
			out.Lanes = append(out.Lanes, Lane{Key: p.ID, Name: p.Name, Color: p.Color})
		}
		out.Lanes = append(out.Lanes, Lane{Key: "", Name: "Senza priorità"})
	}
	return nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// MoveInput sposta una card: in un'altra colonna (passaggio di fase secondo il processo),
// tra due card (ordine manuale) e in un'altra swimlane (assegnatario, epic o priorità).
type MoveInput struct {
	Element string  `json:"element"`
	Column  *int    `json:"column"`  // indice della colonna di arrivo; nil = stessa
	Comment string  `json:"comment"` // motivazione, per i passaggi che la chiedono
	After   string  `json:"after"`   // id della card che precede
	Before  string  `json:"before"`  // id della card che segue
	Lane    *string `json:"lane"`    // valore della swimlane di arrivo; nil = stessa
	Swim    string  `json:"swimlane"`
}

// MoveResult è l'esito dello spostamento.
type MoveResult struct {
	Element *engine.Element `json:"element"`
	Rank    string          `json:"rank"`
	Phase   string          `json:"phase"`
}

// Move esegue lo spostamento in una sola transazione: se il passaggio di fase è bloccato
// (gate, ruoli, motivazione mancante, passaggio non previsto) non cambia niente e l'errore
// spiega perché; la card torna al suo posto.
func (s *Service) Move(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in MoveInput) (*MoveResult, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	el, err := s.workItem(ctx, tx, actor, in.Element)
	if err != nil {
		return nil, err
	}
	if el.SpaceID != sp.ID {
		return nil, engine.InvalidParams("validation.work.element_not_in_space", "element", map[string]any{"key": el.Key, "space": sp.Key}, "%s non è nello spazio %s", el.Key, sp.Key)
	}
	res := &MoveResult{Phase: el.State}
	if in.Column != nil {
		st, err := s.Settings(ctx, tx, sp.ID)
		if err != nil {
			return nil, err
		}
		if *in.Column < 0 || *in.Column >= len(st.Columns) {
			return nil, engine.NotFound("column", "", "colonna inesistente")
		}
		col := st.Columns[*in.Column]
		if !hasPhase(col.phasesFor(el.TypeKey), el.State) {
			to, err := s.targetPhase(ctx, tx, actor, el, col)
			if err != nil {
				return nil, err
			}
			if el, err = s.eng.Transition(ctx, tx, actor, el.ID, to, in.Comment, engine.TransitionOpts{}); err != nil {
				return nil, err
			}
			res.Phase = el.State
		}
	}
	if in.Lane != nil {
		switch in.Swim {
		case "assignee":
			if err := s.setAssignee(ctx, tx, actor, el, *in.Lane, 0); err != nil {
				return nil, err
			}
		case "epic":
			if el.TypeKey == TypeTask {
				return nil, engine.InvalidCode("validation.work.task_follows_story", "lane", "un task segue l'epic della sua story: sposta la story")
			}
			if err := s.setParent(ctx, tx, actor, el, *in.Lane); err != nil {
				return nil, err
			}
		case "priority":
			cur, _ := el.Fields[FieldPriority].(string)
			if cur != *in.Lane {
				var v any = *in.Lane
				if *in.Lane == "" {
					v = nil
				}
				fresh, err := s.eng.Get(ctx, tx, actor, el.ID)
				if err != nil {
					return nil, err
				}
				if _, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: fresh.Version, Fields: map[string]any{FieldPriority: v}}); err != nil {
					return nil, err
				}
			}
		case "", "none":
		default:
			return nil, engine.InvalidValue("swimlane", "swimlane sconosciuta")
		}
	}
	if in.After != "" || in.Before != "" {
		if res.Rank, err = placeBetween(ctx, tx, sp.ID, el.ID, in.After, in.Before); err != nil {
			return nil, err
		}
	}
	if res.Element, err = s.eng.Get(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	return res, nil
}

// SetRank mette l'elemento tra after e before (backlog e liste).
func (s *Service) SetRank(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, after, before string) (string, error) {
	el, err := s.workItem(ctx, tx, actor, ref)
	if err != nil {
		return "", err
	}
	if after == "" && before == "" {
		return "", engine.Required("after", "indica dopo quale (after) o prima di quale (before) elemento")
	}
	return placeBetween(ctx, tx, el.SpaceID, el.ID, after, before)
}
