package work

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Burndown dello sprint ricavato dalla storia degli stati (activities, kind "state"): per ogni
// giorno, punti (o elementi) ancora aperti a fine giornata fra quelli pianificati nello sprint
// a quella data. Gli elementi spostati alla chiusura (carried_over) restano nel conto fino
// alla fine; quelli annullati escono dal conto dal giorno dell'annullamento.

// Point è un valore del grafico in un giorno.
type Point struct {
	Day   string  `json:"day"` // AAAA-MM-GG
	Value float64 `json:"value"`
}

// Burndown è il grafico di uno sprint.
type Burndown struct {
	Sprint    SprintRef `json:"sprint"`
	Goal      string    `json:"goal"`
	Unit      string    `json:"unit"` // points | items
	Start     string    `json:"start"`
	End       string    `json:"end"`
	Committed float64   `json:"committed"` // all'avvio
	Scope     float64   `json:"scope"`     // oggi (con gli elementi aggiunti dopo l'avvio)
	Remaining float64   `json:"remaining"`
	Completed float64   `json:"completed"`
	Ideal     []Point   `json:"ideal"`
	Actual    []Point   `json:"actual"`
	Started   bool      `json:"started"`
	Closed    bool      `json:"closed"`
}

type bdItem struct {
	added  time.Time
	weight float64
	points *float64
	cat    string // categoria attuale
	events []bdEvent
}

type bdEvent struct {
	at       time.Time
	from, to string // categorie
}

// catAt è la categoria dell'elemento alla fine dell'istante t.
func (it *bdItem) catAt(t time.Time) string {
	cat := ""
	for _, e := range it.events {
		if e.at.After(t) {
			if cat == "" {
				return e.from
			}
			return cat
		}
		cat = e.to
	}
	if cat == "" {
		return it.cat
	}
	return cat
}

// SprintBurndown calcola il burndown di uno sprint.
func (s *Service) SprintBurndown(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Burndown, error) {
	el, err := s.sprint(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var tz string
	if err := tx.QueryRow(ctx, `select coalesce((select default_timezone from tenants where id = app_tenant()), 'Europe/Rome')`).Scan(&tz); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation("Europe/Rome")
	}
	var goal string
	var started, closed *time.Time
	var committed *float64
	var unit *string
	var carried []string
	err = tx.QueryRow(ctx, `select goal, started_at, closed_at, committed::float8, committed_unit, carried_over::text[] from ext_sprint where element_id = $1`, el.ID).
		Scan(&goal, &started, &closed, &committed, &unit, &carried)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	day := func(t time.Time) time.Time {
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	now := time.Now()
	// Il grafico parte dalla data di inizio pianificata (o dall'avvio, se è avvenuto prima).
	begin := day(now)
	if started != nil {
		begin = day(*started)
	}
	if el.StartAt != nil {
		if planned := time.Date(el.StartAt.Year(), el.StartAt.Month(), el.StartAt.Day(), 0, 0, 0, 0, loc); started == nil || planned.Before(begin) {
			begin = planned
		}
	}
	end := begin.AddDate(0, 0, 13)
	if el.DueAt != nil {
		end = time.Date(el.DueAt.Year(), el.DueAt.Month(), el.DueAt.Day(), 0, 0, 0, 0, loc)
	}
	if end.Before(begin) {
		end = begin
	}
	out := &Burndown{Goal: goal, Start: begin.Format(time.DateOnly), End: end.Format(time.DateOnly), Ideal: []Point{}, Actual: []Point{},
		Started: started != nil || el.StateCategory == "in_progress" || el.StateCategory == "done", Closed: el.StateCategory == "done"}
	out.Sprint = SprintRef{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, Category: el.StateCategory, Start: &out.Start, End: &out.End}

	// Elementi: pianificati ora (con la data della pianificazione) e spostati alla chiusura.
	items := map[string]*bdItem{}
	rows, err := tx.Query(ctx, `
		select e.id::text, r.created_at, case when jsonb_typeof(e.fields->'`+FieldPoints+`') = 'number' then (e.fields->>'`+FieldPoints+`')::float8 end,
		       coalesce(o.category, '')
		from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where r.target_id = $1
		union all
		select e.id::text, null, case when jsonb_typeof(e.fields->'`+FieldPoints+`') = 'number' then (e.fields->>'`+FieldPoints+`')::float8 end,
		       coalesce(o.category, '')
		from elements e left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.id = any($2::uuid[]) and e.deleted_at is null`, el.ID, carried)
	if err != nil {
		return nil, err
	}
	var id, cat string
	var added *time.Time
	var points *float64
	if _, err := pgx.ForEachRow(rows, []any{&id, &added, &points, &cat}, func() error {
		if _, ok := items[id]; ok {
			return nil
		}
		// Pianificato prima dell'avvio: fa parte dell'impegno da subito. Dopo l'avvio: entra
		// nel conto dal momento in cui è stato aggiunto (aumento del perimetro).
		it := &bdItem{added: begin, points: points, cat: cat}
		if added != nil && added.After(begin) && (started == nil || added.After(*started)) {
			it.added = *added
		}
		items[id] = it
		return nil
	}); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	totalPoints := 0.0
	for k, it := range items {
		ids = append(ids, k)
		if it.points != nil {
			totalPoints += *it.points
		}
	}
	out.Unit = "points"
	if unit != nil {
		out.Unit = *unit
	} else if totalPoints == 0 {
		out.Unit = "items"
	}
	for _, it := range items {
		if out.Unit == "items" {
			it.weight = 1
		} else if it.points != nil {
			it.weight = *it.points
		}
	}
	// Storia degli stati, con la categoria della fase di partenza e di arrivo.
	if len(ids) > 0 {
		rows, err = tx.Query(ctx, `
			select a.element_id::text, a.created_at, coalesce(fo.category, ''), coalesce(tob.category, '')
			from activities a
			join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			left join lateral (select category from options o where o.tenant_id = t.tenant_id and o.option_set_id = t.state_set_id
			                   and lower(o.name) = lower(a.removed->>'name') limit 1) fo on true
			left join lateral (select category from options o where o.tenant_id = t.tenant_id and o.option_set_id = t.state_set_id
			                   and lower(o.name) = lower(a.added->>'name') limit 1) tob on true
			where a.element_id = any($1::uuid[]) and a.kind = 'state'
			order by a.created_at, a.id`, ids)
		if err != nil {
			return nil, err
		}
		var at time.Time
		var from, to string
		if _, err := pgx.ForEachRow(rows, []any{&id, &at, &from, &to}, func() error {
			if it := items[id]; it != nil {
				it.events = append(it.events, bdEvent{at: at, from: from, to: to})
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	open := func(c string) bool { return c != "done" && c != "cancelled" }
	remainingAt := func(t time.Time) (rem, scope float64) {
		for _, it := range items {
			if it.added.After(t) {
				continue
			}
			c := it.catAt(t)
			if c == "cancelled" {
				continue
			}
			scope += it.weight
			if open(c) {
				rem += it.weight
			}
		}
		return rem, scope
	}
	// Giorni: dall'inizio a oggi (o alla chiusura, o alla fine se già passata).
	last := day(now)
	if closed != nil && day(*closed).Before(last) {
		last = day(*closed)
	}
	if out.Started {
		for d := begin; !d.After(last); d = d.AddDate(0, 0, 1) {
			rem, _ := remainingAt(d.AddDate(0, 0, 1).Add(-time.Nanosecond))
			out.Actual = append(out.Actual, Point{Day: d.Format(time.DateOnly), Value: round1(rem)})
			if len(out.Actual) > 400 {
				break
			}
		}
	}
	rem, scope := remainingAt(now)
	out.Remaining, out.Scope, out.Completed = round1(rem), round1(scope), round1(scope-rem)
	if committed != nil {
		out.Committed = round1(*committed)
	} else {
		_, sc := remainingAt(begin.AddDate(0, 0, 1).Add(-time.Nanosecond))
		if !out.Started {
			sc = scope
		}
		out.Committed = round1(sc)
	}
	out.Ideal = []Point{{Day: out.Start, Value: out.Committed}, {Day: out.End, Value: 0}}
	return out, nil
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
