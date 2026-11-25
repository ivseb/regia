package planning

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/calendar"
)

// Carico dei team per la cabina di portafoglio: lo sforzo che ogni richiesta chiede a ogni team,
// settimana per settimana, accanto alla capacità e al carico di tutto il resto (le richieste non
// in graduatoria). La cabina somma gli sforzi delle richieste sopra la linea di taglio e li
// confronta con la capacità: una sola chiamata per l'intera pagina.

// TeamEffort è lo sforzo di una richiesta su un team: giorni totali e per settimana (come Weeks).
type TeamEffort struct {
	TeamID string    `json:"team_id"`
	Days   float64   `json:"days"`
	Weeks  []float64 `json:"weeks"`
}

// TeamCapacity è la capacità settimanale di un team e il carico del resto del piano.
type TeamCapacity struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Capacity []float64 `json:"capacity"` // giorni disponibili per settimana (festività e assenze escluse)
	Base     []float64 `json:"base"`     // giorni già occupati dalle richieste fuori dalla graduatoria
}

// PortfolioLoad è il carico di un insieme di richieste.
type PortfolioLoad struct {
	Weeks    []string                `json:"weeks"` // lunedì AAAA-MM-GG
	Teams    []TeamCapacity          `json:"teams"`
	Requests map[string][]TeamEffort `json:"requests"`
}

// PortfolioLoad calcola gli sforzi delle richieste indicate. L'orizzonte va da questa settimana
// alla fine dell'ultimo impatto pianificato (almeno 26 settimane, al massimo 3 anni).
func (s *Service) PortfolioLoad(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestIDs []string) (*PortfolioLoad, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	from := monday(time.Now())
	to := from.AddDate(0, 0, 26*7-1)
	var last *time.Time
	if err := tx.QueryRow(ctx, `select max(e.due_at) from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		where e.deleted_at is null and e.due_at is not null and x.team_id is not null`).Scan(&last); err != nil {
		return nil, err
	}
	if last != nil && last.After(to) {
		to = *last
	}
	if limit := from.AddDate(3, 0, 0); to.After(limit) {
		to = limit
	}
	teams, err := s.Load(ctx, tx, actor, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	out := &PortfolioLoad{Weeks: []string{}, Teams: []TeamCapacity{}, Requests: map[string][]TeamEffort{}}
	if len(teams) == 0 {
		return out, nil
	}
	n := len(teams[0].Weeks)
	for _, w := range teams[0].Weeks {
		out.Weeks = append(out.Weeks, w.Week)
	}
	idx := map[string]int{}
	for i, t := range teams {
		idx[t.ID] = i
	}
	rows, err := tx.Query(ctx, `
		select x.request_id::text, x.team_id::text, e.start_at, e.due_at, x.estimate_effort_days::float8
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.team_id is not null and e.start_at is not null and e.due_at is not null and x.estimate_effort_days > 0
		  and x.request_id = any($3::uuid[]) and coalesce(o.category, '') not in ('done', 'cancelled')
		  and e.start_at <= $2 and e.due_at >= $1`, from, to, requestIDs)
	if err != nil {
		return nil, err
	}
	mine := map[string]map[string]*TeamEffort{} // richiesta -> team
	used := make([][]float64, len(teams))
	for i := range used {
		used[i] = make([]float64, n)
	}
	var reqID, teamID string
	var st, en time.Time
	var effort float64
	if _, err := pgx.ForEachRow(rows, []any{&reqID, &teamID, &st, &en, &effort}, func() error {
		i, ok := idx[teamID]
		days := calendar.Workdays(st, en)
		if !ok || days == 0 {
			return nil
		}
		per := effort / float64(days)
		te := mine[reqID][teamID]
		if te == nil {
			if mine[reqID] == nil {
				mine[reqID] = map[string]*TeamEffort{}
			}
			te = &TeamEffort{TeamID: teamID, Weeks: make([]float64, n)}
			mine[reqID][teamID] = te
		}
		for d := calendar.Day(st); !d.After(calendar.Day(en)); d = d.AddDate(0, 0, 1) {
			if d.Before(from) || d.After(to) || !calendar.Workday(d) {
				continue
			}
			j := int(monday(d).Sub(from).Hours() / 24 / 7)
			if j < 0 || j >= n {
				continue
			}
			te.Weeks[j] += per
			te.Days += per
			used[i][j] += per
		}
		return nil
	}); err != nil {
		return nil, err
	}
	r1 := func(v float64) float64 { return math.Round(v*10) / 10 }
	for rid, byTeam := range mine {
		for _, te := range byTeam {
			for j := range te.Weeks {
				te.Weeks[j] = r1(te.Weeks[j])
			}
			te.Days = r1(te.Days)
			out.Requests[rid] = append(out.Requests[rid], *te)
		}
	}
	// Solo i team che le richieste chiamano in causa.
	for i, t := range teams {
		involved := false
		for _, v := range used[i] {
			if v > 0 {
				involved = true
				break
			}
		}
		if !involved {
			continue
		}
		tc := TeamCapacity{ID: t.ID, Name: t.Name, Capacity: make([]float64, n), Base: make([]float64, n)}
		for j, w := range t.Weeks {
			tc.Capacity[j] = w.Capacity
			tc.Base[j] = r1(math.Max(w.Load-used[i][j], 0))
		}
		out.Teams = append(out.Teams, tc)
	}
	return out, nil
}
