package alm

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestSet è un test set letto, con i numeri per stato dei test pianificati.
type TestSet struct {
	ID          string     `json:"id"`
	Integration string     `json:"integration_id"`
	Project     string     `json:"project"`
	ExternalID  string     `json:"external_id"`
	Name        string     `json:"name"`
	Folder      string     `json:"folder"`
	Status      string     `json:"status"`
	Cycle       string     `json:"cycle"`
	Release     string     `json:"release"`
	Change      string     `json:"change"`
	Application *string    `json:"application"` // chiave dell'applicazione, se associata
	URL         string     `json:"url"`
	Counts      Counts     `json:"counts"`
	OpenDefects int        `json:"open_defects"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// Counts sono i test pianificati di un set, per stato attuale.
type Counts struct {
	Planned      int `json:"planned"`
	Passed       int `json:"passed"`
	Failed       int `json:"failed"`
	Blocked      int `json:"blocked"`
	NotCompleted int `json:"not_completed"`
	NoRun        int `json:"no_run"`
	NA           int `json:"na"`
}

// TestSetFilter filtra i test set letti.
type TestSetFilter struct {
	IntegrationID string
	Change        string // numero di change (o alias, risolto con la tabella changes)
	Limit         int
}

// TestSets elenca i test set letti da ALM, i più recenti prima.
func TestSets(ctx context.Context, tx pgx.Tx, f TestSetFilter) ([]TestSet, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `select s.id::text, coalesce(s.integration_id::text, ''), s.project, s.external_id, s.name, s.folder, s.status, s.cycle, s.release,
		  s.change_number, sp.key || '-' || e.number, s.url, s.source_updated_at,
		  count(i.id), count(i.id) filter (where i.status = 'passed'), count(i.id) filter (where i.status = 'failed'),
		  count(i.id) filter (where i.status = 'blocked'), count(i.id) filter (where i.status = 'not_completed'),
		  count(i.id) filter (where i.status = 'no_run'), count(i.id) filter (where i.status = 'na'),
		  (select count(*) from test_defects d where d.open and s.id = any(d.test_set_ids))
		from test_sets s
		left join test_set_items i on i.tenant_id = s.tenant_id and i.test_set_id = s.id
		left join elements e on e.tenant_id = s.tenant_id and e.id = s.application_element_id and e.deleted_at is null
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where s.origin = 'alm'`
	args := []any{}
	if f.IntegrationID != "" {
		if !validID(f.IntegrationID) {
			return []TestSet{}, nil
		}
		args = append(args, f.IntegrationID)
		q += " and s.integration_id = $1"
	}
	if n := strings.ToUpper(strings.TrimSpace(f.Change)); n != "" {
		args = append(args, n)
		q += ` and s.change_number in (select x from changes c, unnest(array_prepend(c.number, c.aliases)) x where c.number = $` + itoa(len(args)) +
			` or $` + itoa(len(args)) + ` = any(c.aliases) union select $` + itoa(len(args)) + `)`
	}
	args = append(args, limit)
	q += " group by s.tenant_id, s.id, sp.key, e.number order by s.source_updated_at desc nulls last, s.name limit $" + itoa(len(args))
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TestSet, error) {
		var t TestSet
		err := r.Scan(&t.ID, &t.Integration, &t.Project, &t.ExternalID, &t.Name, &t.Folder, &t.Status, &t.Cycle, &t.Release, &t.Change,
			&t.Application, &t.URL, &t.UpdatedAt, &t.Counts.Planned, &t.Counts.Passed, &t.Counts.Failed, &t.Counts.Blocked,
			&t.Counts.NotCompleted, &t.Counts.NoRun, &t.Counts.NA, &t.OpenDefects)
		return t, err
	})
	if out == nil {
		out = []TestSet{}
	}
	return out, err
}

func itoa(n int) string { return strconv.Itoa(n) }
