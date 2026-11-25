package planning

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/calendar"
)

// applyAbsences toglie dalla capacità di ogni settimana i giorni di assenza delle persone
// del team (migrazione 0038): un'assenza di una giornata intera pesa le ore giornaliere
// della persona per la sua allocazione nel team, una parziale le ore indicate.
func applyAbsences(ctx context.Context, tx pgx.Tx, teams []TeamLoad, idx map[string]int, from, to time.Time) error {
	rows, err := tx.Query(ctx, `
		select tm.team_id::text, coalesce(tm.weekly_hours, m.weekly_hours)::float8, tm.allocation_pct,
		       a.from_date, a.to_date, a.hours_per_day::float8
		from team_members tm
		join members m on m.tenant_id = tm.tenant_id and m.id = tm.member_id
		join member_absences a on a.tenant_id = tm.tenant_id and a.member_id = tm.member_id
		where a.to_date >= $1::date and a.from_date <= $2::date`, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return err
	}
	var teamID string
	var weekly, perDayPartial *float64
	var pct int
	var aFrom, aTo time.Time
	_, err = pgx.ForEachRow(rows, []any{&teamID, &weekly, &pct, &aFrom, &aTo, &perDayPartial}, func() error {
		i, ok := idx[teamID]
		if !ok || weekly == nil {
			return nil
		}
		daily := *weekly / 5
		hours := daily
		if perDayPartial != nil && *perDayPartial < daily {
			hours = *perDayPartial
		}
		days := hours * float64(pct) / 100 / 8
		start, end := calendar.Day(aFrom), calendar.Day(aTo)
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			if !calendar.Workday(d) {
				continue
			}
			w := int(monday(d).Sub(monday(from)).Hours()/24) / 7
			if w < 0 || w >= len(teams[i].Weeks) {
				continue
			}
			c := teams[i].Weeks[w].Capacity - days
			teams[i].Weeks[w].Capacity = math.Max(math.Round(c*10)/10, 0)
		}
		return nil
	})
	return err
}
