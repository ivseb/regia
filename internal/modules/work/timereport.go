package work

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/calendar"
)

// Report del tempo (piano, passo 18; replica/work.md «Tempo e team»): le ore di work_logs per
// persona, team, spazio, epic, richiesta, elemento, settimana o mese, con raggruppamento a
// due livelli, confronto con la stima e con la capacità. Ogni riga porta alle registrazioni
// che la compongono (Entries) con gli stessi filtri. I costi non passano di qui: li aggiunge
// l'API, in una transazione con app.finance, solo a chi ha finance.read.

// TimeFilter seleziona le registrazioni. I valori "none" cercano le ore senza quel
// raggruppamento (per esempio fuori da ogni epic).
type TimeFilter struct {
	From    string
	To      string
	Person  string
	Team    string
	Space   string
	Epic    string
	Request string
	Element string
	Week    string
	Month   string
}

// Dimensioni di raggruppamento: espressione della chiave e dell'etichetta sulla vista "base".
var timeDims = map[string][2]string{
	"person":  {"b.member_id::text", "b.member_name"},
	"team":    {"b.team_id::text", "b.team_name"},
	"space":   {"b.space_id::text", "b.space_key || ' · ' || b.space_name"},
	"epic":    {"b.epic_id::text", "b.epic_label"},
	"request": {"b.request_id::text", "b.request_label"},
	"element": {"b.element_id::text", "b.element_key || ' ' || b.title"},
	"week":    {"to_char(date_trunc('week', b.work_date), 'YYYY-MM-DD')", "'sett. dal ' || to_char(date_trunc('week', b.work_date), 'DD/MM/YYYY')"},
	"month":   {"to_char(b.work_date, 'YYYY-MM')", "to_char(b.work_date, 'YYYY-MM')"},
}

// TimeDims sono le dimensioni ammesse, nell'ordine in cui l'interfaccia le propone.
var TimeDims = []string{"person", "team", "space", "epic", "request", "element", "week", "month"}

const baseCTE = `
	with lg as (
		select w.id, w.member_id, w.element_id, w.work_date, w.minutes, w.text, w.created_at
		from work_logs w where w.work_date between $1::date and $2::date __MEMBERS__
	), chain as (
		with recursive c(root, id, parent_id, depth) as (
			select e.id, e.id, e.parent_id, 0 from elements e where e.id in (select element_id from lg)
			union all
			select c.root, p.id, p.parent_id, c.depth + 1 from c join elements p on p.id = c.parent_id where c.depth < 6)
		select c.root,
		       (array_agg(c.id order by c.depth) filter (where t.key = 'epic'))[1] as epic_id,
		       (array_agg(c.id order by c.depth) filter (where t.key = 'business_request'))[1] as request_id
		from c join elements ce on ce.id = c.id join element_types t on t.tenant_id = ce.tenant_id and t.id = ce.type_id
		group by c.root
	), base as (
		select lg.id, lg.member_id, m.display_name as member_name, lg.element_id, lg.work_date, lg.minutes, lg.text, lg.created_at,
		       s.key || '-' || e.number as element_key, e.title, sp.id as space_id, sp.key as space_key, sp.name as space_name,
		       te.id as team_id, te.name as team_name, a.epic_id, a.request_id,
		       case when ep.id is null then null else es.key || '-' || ep.number || ' ' || ep.title end as epic_label,
		       case when rq.id is null then null else rs.key || '-' || rq.number || ' ' || rq.title end as request_label,
		       case when jsonb_typeof(e.fields->'` + FieldEstimate + `') = 'number' then (e.fields->>'` + FieldEstimate + `')::float8::bigint else 0 end as est
		from lg
		join members m on m.tenant_id = app_tenant() and m.id = lg.member_id
		join elements e on e.tenant_id = app_tenant() and e.id = lg.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join teams te on te.tenant_id = e.tenant_id and te.delivery_space_id = e.space_id and not te.archived
		left join chain a on a.root = lg.element_id
		left join elements ep on ep.id = a.epic_id
		left join spaces es on es.id = ep.space_id
		left join elements rq on rq.id = a.request_id
		left join spaces rs on rs.id = rq.space_id
	)`

// memberScope: tutte le persone per chi amministra o legge i costi; altrimenti sé stessi e
// chi sta nei team che guida.
func memberScope(ctx context.Context, tx pgx.Tx, actor engine.Actor) (all bool, ids []string, err error) {
	if canAdminTime(actor) || actor.Has("finance.read") {
		return true, nil, nil
	}
	rows, err := tx.Query(ctx, `select distinct tm.member_id::text from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		where t.lead_id = $1::uuid and not t.archived`, actor.MemberID)
	if err != nil {
		return false, nil, err
	}
	ids, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return false, append(ids, actor.MemberID), err
}

// query compone la query con i filtri; restituisce testo e argomenti ($1 e $2 sono le date).
func (f TimeFilter) query(ctx context.Context, tx pgx.Tx, actor engine.Actor, tail string) (string, []any, error) {
	from, err1 := time.Parse(time.DateOnly, f.From)
	to, err2 := time.Parse(time.DateOnly, f.To)
	if err1 != nil || err2 != nil || to.Before(from) {
		return "", nil, engine.DateInvalid("from", "periodo non valido (AAAA-MM-GG, dal primo all'ultimo giorno)")
	}
	if to.Sub(from) > 3*366*24*time.Hour {
		return "", nil, engine.InvalidCode("validation.work.report_range_too_long", "to", "al massimo tre anni per volta")
	}
	args := []any{from.Format(time.DateOnly), to.Format(time.DateOnly)}
	members := ""
	all, ids, err := memberScope(ctx, tx, actor)
	if err != nil {
		return "", nil, err
	}
	if !all {
		args = append(args, ids)
		members = fmt.Sprintf(" and w.member_id::text = any($%d)", len(args))
	}
	where := []string{}
	add := func(dim, v string) {
		if v == "" {
			return
		}
		expr := timeDims[dim][0]
		if v == "none" {
			where = append(where, expr+" is null")
			return
		}
		args = append(args, v)
		where = append(where, fmt.Sprintf("%s = $%d", expr, len(args)))
	}
	add("person", f.Person)
	add("team", f.Team)
	add("space", f.Space)
	add("epic", f.Epic)
	add("request", f.Request)
	add("element", f.Element)
	add("week", f.Week)
	add("month", f.Month)
	w := ""
	if len(where) > 0 {
		w = " where " + strings.Join(where, " and ")
	}
	return strings.Replace(baseCTE, "__MEMBERS__", members, 1) + tail + w, args, nil
}

// ReportGroup è una riga del report: un gruppo con i suoi sottogruppi.
type ReportGroup struct {
	Key       string        `json:"key"`
	Label     string        `json:"label"`
	Minutes   int64         `json:"minutes"`
	Estimate  int64         `json:"estimate"`           // stima degli elementi su cui si è lavorato
	Capacity  *int64        `json:"capacity,omitempty"` // minuti disponibili (persona e team)
	CostCents *int64        `json:"cost_cents,omitempty"`
	Children  []ReportGroup `json:"children,omitempty"`
}

// TimeReport è il risultato.
type TimeReport struct {
	From    string        `json:"from"`
	To      string        `json:"to"`
	Dim1    string        `json:"dim1"`
	Dim2    string        `json:"dim2"`
	Groups  []ReportGroup `json:"groups"`
	Total   ReportGroup   `json:"total"`
	Scope   string        `json:"scope"` // all | team | own
	CanCost bool          `json:"can_cost"`
}

type aggRow struct {
	k1, l1, k2, l2 string
	minutes, est   int64
}

// aggregate raggruppa per le dimensioni indicate (0, 1 o 2). Con cost=true la colonna "est"
// porta invece il costo in centesimi (serve la transazione con app.finance).
func aggregate(ctx context.Context, tx pgx.Tx, actor engine.Actor, f TimeFilter, dims []string, cost bool) ([]aggRow, error) {
	k := [2]string{"''::text", "''::text"}
	l := [2]string{"''::text", "''::text"}
	for i, d := range dims {
		e, ok := timeDims[d]
		if !ok {
			return nil, engine.InvalidValue("group", "raggruppamento sconosciuto: %s", d)
		}
		k[i], l[i] = "coalesce("+e[0]+", '')", "coalesce("+e[1]+", '')"
	}
	var q string
	if cost {
		q = fmt.Sprintf(` select %[1]s as k1, %[2]s as l1, %[3]s as k2, %[4]s as l2, sum(b.minutes)::bigint, coalesce(sum(ce.amount_base_cents), 0)::bigint
			from base b left join cost_entries ce on ce.tenant_id = app_tenant() and ce.work_log_id = b.id`, k[0], l[0], k[1], l[1])
	} else {
		q = fmt.Sprintf(` select k1, l1, k2, l2, sum(minutes)::bigint, sum(est)::bigint from (
			select %[1]s as k1, %[2]s as l1, %[3]s as k2, %[4]s as l2, b.element_id, sum(b.minutes) as minutes, max(b.est) as est
			from base b`, k[0], l[0], k[1], l[1])
	}
	text, args, err := f.query(ctx, tx, actor, q)
	if err != nil {
		return nil, err
	}
	if cost {
		text += ` group by 1, 2, 3, 4`
	} else {
		text += ` group by 1, 2, 3, 4, b.element_id) x group by k1, l1, k2, l2`
	}
	rows, err := tx.Query(ctx, text+` order by 5 desc limit 5000`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (aggRow, error) {
		var a aggRow
		return a, r.Scan(&a.k1, &a.l1, &a.k2, &a.l2, &a.minutes, &a.est)
	})
}

func validDim(d string) bool { return d == "" || slices.Contains(TimeDims, d) }

// Report calcola il report. dim2 vuoto = un solo livello.
func (s *Service) Report(ctx context.Context, tx pgx.Tx, actor engine.Actor, f TimeFilter, dim1, dim2 string) (*TimeReport, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	if dim1 == "" {
		dim1 = "person"
	}
	if !validDim(dim1) || !validDim(dim2) || dim1 == dim2 {
		return nil, engine.InvalidCode("validation.work.report_group_invalid", "group", "raggruppamenti non validi: scegli due dimensioni diverse")
	}
	out := &TimeReport{From: f.From, To: f.To, Dim1: dim1, Dim2: dim2, Groups: []ReportGroup{}, CanCost: actor.Has("finance.read")}
	all, ids, err := memberScope(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	out.Scope = "all"
	if !all {
		out.Scope = "own"
		if len(ids) > 1 {
			out.Scope = "team"
		}
	}
	top, err := aggregate(ctx, tx, actor, f, []string{dim1}, false)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for _, r := range top {
		idx[r.k1] = len(out.Groups)
		out.Groups = append(out.Groups, ReportGroup{Key: r.k1, Label: label(r.l1, dim1), Minutes: r.minutes, Estimate: r.est})
	}
	if dim2 != "" {
		sub, err := aggregate(ctx, tx, actor, f, []string{dim1, dim2}, false)
		if err != nil {
			return nil, err
		}
		for _, r := range sub {
			if i, ok := idx[r.k1]; ok {
				out.Groups[i].Children = append(out.Groups[i].Children, ReportGroup{Key: r.k2, Label: label(r.l2, dim2), Minutes: r.minutes, Estimate: r.est})
			}
		}
	}
	tot, err := aggregate(ctx, tx, actor, f, nil, false)
	if err != nil {
		return nil, err
	}
	out.Total = ReportGroup{Label: "Totale"}
	for _, r := range tot {
		out.Total.Minutes, out.Total.Estimate = r.minutes, r.est
	}
	if dim1 == "person" || dim1 == "team" {
		if err := s.fillCapacity(ctx, tx, out, f, dim1, all, ids); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// label dà un nome alle ore che non appartengono a nessun gruppo.
func label(l, dim string) string {
	if l != "" {
		return l
	}
	switch dim {
	case "team":
		return "Senza team"
	case "epic":
		return "Senza epic"
	case "request":
		return "Senza richiesta"
	}
	return "—"
}

// ReportCosts restituisce i costi per gruppo (chiave "k1" o "k1\x00k2"): da chiamare in una
// transazione con app.finance, solo per chi ha finance.read.
func (s *Service) ReportCosts(ctx context.Context, tx pgx.Tx, actor engine.Actor, f TimeFilter, dim1, dim2 string) (map[string]int64, error) {
	if !actor.Has("finance.read") {
		return nil, engine.ErrForbidden
	}
	out := map[string]int64{}
	dimsets := [][]string{{}, {dim1}}
	if dim2 != "" {
		dimsets = append(dimsets, []string{dim1, dim2})
	}
	for _, ds := range dimsets {
		rows, err := aggregate(ctx, tx, actor, f, ds, true)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			switch len(ds) {
			case 0:
				out[""] = r.est
			case 1:
				out[r.k1] = r.est
			default:
				out[r.k1+"\x00"+r.k2] = r.est
			}
		}
	}
	return out, nil
}

// MergeCosts scrive i costi nel report.
func MergeCosts(r *TimeReport, costs map[string]int64) {
	set := func(g *ReportGroup, v int64) { g.CostCents = &v }
	set(&r.Total, costs[""])
	for i := range r.Groups {
		g := &r.Groups[i]
		set(g, costs[g.Key])
		for j := range g.Children {
			set(&g.Children[j], costs[g.Key+"\x00"+g.Children[j].Key])
		}
	}
}

// fillCapacity aggiunge la capacità del periodo ai gruppi per persona o per team: giorni
// lavorativi (festività escluse) per le ore a settimana, tolte le assenze.
func (s *Service) fillCapacity(ctx context.Context, tx pgx.Tx, r *TimeReport, f TimeFilter, dim string, all bool, scope []string) error {
	from, _ := time.Parse(time.DateOnly, f.From)
	to, _ := time.Parse(time.DateOnly, f.To)
	type person struct {
		hours float64 // ore a settimana nel gruppo
		abs   []Absence
		share float64
	}
	cap := map[string]int64{}
	var days []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if calendar.Workday(d) {
			days = append(days, d)
		}
	}
	absRows, err := tx.Query(ctx, `select a.id::text, a.member_id::text, '', to_char(a.from_date, 'YYYY-MM-DD'), to_char(a.to_date, 'YYYY-MM-DD'), a.kind, a.hours_per_day::float8, a.note
		from member_absences a where a.to_date >= $1::date and a.from_date <= $2::date`, f.From, f.To)
	if err != nil {
		return err
	}
	allAbs, err := pgx.CollectRows(absRows, pgx.RowToStructByPos[Absence])
	if err != nil {
		return err
	}
	absOf := map[string][]Absence{}
	for _, a := range allAbs {
		absOf[a.MemberID] = append(absOf[a.MemberID], a)
	}
	minutesOf := func(memberID string, weekly float64) int64 {
		perDay := int(weekly * 60 / 5)
		var total int64
		for _, d := range days {
			total += int64(max(perDay-absenceMinutes(absOf[memberID], d, perDay), 0))
		}
		return total
	}
	if dim == "person" {
		rows, err := tx.Query(ctx, "select id::text, weekly_hours::float8 from members where status <> 'anonymized'")
		if err != nil {
			return err
		}
		var id string
		var h float64
		if _, err := pgx.ForEachRow(rows, []any{&id, &h}, func() error {
			if all || slices.Contains(scope, id) {
				cap[id] = minutesOf(id, h)
			}
			return nil
		}); err != nil {
			return err
		}
	} else {
		rows, err := tx.Query(ctx, `select tm.team_id::text, tm.member_id::text, coalesce(tm.weekly_hours, m.weekly_hours)::float8 * tm.allocation_pct / 100
			from team_members tm join members m on m.tenant_id = tm.tenant_id and m.id = tm.member_id where m.status <> 'anonymized'`)
		if err != nil {
			return err
		}
		var team, id string
		var h float64
		if _, err := pgx.ForEachRow(rows, []any{&team, &id, &h}, func() error {
			cap[team] += minutesOf(id, h)
			return nil
		}); err != nil {
			return err
		}
	}
	for i := range r.Groups {
		if c, ok := cap[r.Groups[i].Key]; ok {
			r.Groups[i].Capacity = &c
		}
	}
	return nil
}

// TimeRecord è una registrazione, per il dettaglio dietro un numero del report.
type TimeRecord struct {
	ID         string  `json:"id"`
	Date       string  `json:"date"`
	MemberID   string  `json:"member_id"`
	Member     string  `json:"member"`
	ElementID  string  `json:"element_id"`
	ElementKey string  `json:"element_key"`
	Title      string  `json:"title"`
	Minutes    int     `json:"minutes"`
	Text       *string `json:"text"`
	Space      string  `json:"space"`
	Epic       *string `json:"epic"`
}

// TimeRecords sono le registrazioni che compongono un numero del report.
type TimeRecords struct {
	Items     []TimeRecord `json:"items"`
	Total     int64        `json:"total_minutes"`
	Count     int          `json:"count"`
	Truncated bool         `json:"truncated"`
}

// Records elenca le registrazioni che corrispondono al filtro (al massimo limit, le più recenti).
func (s *Service) Records(ctx context.Context, tx pgx.Tx, actor engine.Actor, f TimeFilter, limit int) (*TimeRecords, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	if limit <= 0 || limit > 20000 {
		limit = 500
	}
	text, args, err := f.query(ctx, tx, actor, ` select b.id::text, to_char(b.work_date, 'YYYY-MM-DD'), b.member_id::text, b.member_name, b.element_id::text,
		b.element_key, b.title, b.minutes, b.text, b.space_key, b.epic_label, count(*) over (), sum(b.minutes) over () from base b`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, text+fmt.Sprintf(` order by b.work_date desc, b.member_name, b.created_at desc limit %d`, limit+1), args...)
	if err != nil {
		return nil, err
	}
	out := &TimeRecords{Items: []TimeRecord{}}
	var r TimeRecord
	var count, total int64
	if _, err := pgx.ForEachRow(rows, []any{&r.ID, &r.Date, &r.MemberID, &r.Member, &r.ElementID, &r.ElementKey, &r.Title, &r.Minutes, &r.Text, &r.Space, &r.Epic, &count, &total}, func() error {
		if len(out.Items) >= limit {
			out.Truncated = true
			return nil
		}
		out.Items = append(out.Items, r)
		return nil
	}); err != nil {
		return nil, err
	}
	out.Count, out.Total = int(count), total
	return out, nil
}
