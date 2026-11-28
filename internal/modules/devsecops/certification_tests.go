package devsecops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/grc"
)

// Test del change dalle tabelle generiche dei test (migrazione 0120): test_sets con il numero
// di change, test_set_items con lo stato attuale di ogni test pianificato, test_defects con i
// change dei difetti. Oggi le scrive il connettore ALM in lettura (origin «alm»); domani il
// test management nativo e l'import dello storico, con la stessa forma.

const (
	CollectorTests = "devsecops.tests"
	sourceTests    = "tests"
)

// CertDefects sono i difetti aperti dei test del change.
type CertDefects struct {
	Open       int            `json:"open"`
	BySeverity map[string]int `json:"by_severity"` // critical, high, medium, low, unknown
	Items      []CertDefect   `json:"items"`       // al massimo 50, i più gravi prima
}

// CertDefect è un difetto aperto.
type CertDefect struct {
	Key         string `json:"key"` // id nella fonte
	Summary     string `json:"summary"`
	Severity    string `json:"severity"`
	RawSeverity string `json:"raw_severity"`
	Status      string `json:"status"`
	Project     string `json:"project"`
	URL         string `json:"url"`
}

// testCounts sono i numeri dei test pianificati, per stato attuale.
type testCounts struct {
	planned, passed, failed, blocked, notCompleted, noRun, na int
}

func (c testCounts) executed() int { return c.passed + c.failed + c.notCompleted }

func (c testCounts) outcome() string {
	switch {
	case c.failed > 0:
		return "failed"
	case c.planned == 0 || c.blocked+c.noRun+c.notCompleted > 0:
		return "not_run"
	}
	return "passed"
}

const countCols = `count(i.id), count(i.id) filter (where i.status = 'passed'), count(i.id) filter (where i.status = 'failed'),
	count(i.id) filter (where i.status = 'blocked'), count(i.id) filter (where i.status = 'not_completed'),
	count(i.id) filter (where i.status = 'no_run'), count(i.id) filter (where i.status = 'na')`

func intp(n int) *int { return &n }

// certTests legge i test set dei numeri del change, uno per riga, con i numeri per stato.
func certTests(ctx context.Context, tx pgx.Tx, numbers []string) ([]TestRun, error) {
	rows, err := tx.Query(ctx, `select s.origin, s.project, s.name, s.folder, s.cycle, s.release, s.url, coalesce(e.title, ''), `+countCols+`
		from test_sets s
		left join test_set_items i on i.tenant_id = s.tenant_id and i.test_set_id = s.id
		left join elements e on e.tenant_id = s.tenant_id and e.id = s.application_element_id and e.deleted_at is null
		where s.change_number = any($1)
		group by s.tenant_id, s.id, e.title order by coalesce(e.title, s.project), s.release, s.cycle, s.name limit 200`, numbers)
	if err != nil {
		return nil, err
	}
	out := []TestRun{}
	var origin, project, name, folder, cycle, release, url, app string
	var c testCounts
	_, err = pgx.ForEachRow(rows, []any{&origin, &project, &name, &folder, &cycle, &release, &url, &app,
		&c.planned, &c.passed, &c.failed, &c.blocked, &c.notCompleted, &c.noRun, &c.na}, func() error {
		svc := app
		if svc == "" {
			svc = project
		}
		ver := cycle
		if ver == "" {
			ver = release
		}
		parts := []string{}
		for _, p := range []string{project, release, cycle, folder} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		t := TestRun{Origin: origin, Service: svc, Version: ver, Name: name, Outcome: c.outcome(), URL: url, Detail: strings.Join(parts, " · "),
			Planned: intp(c.planned), Executed: intp(c.executed()), Passed: intp(c.passed), Failed: intp(c.failed), Skipped: intp(c.na),
			Blocked: intp(c.blocked), NotRun: intp(c.noRun)}
		t.Summary = fmt.Sprintf("%d test: %d superati, %d falliti, %d bloccati, %d non eseguiti", c.planned, c.passed, c.failed, c.blocked, c.noRun)
		if c.notCompleted > 0 {
			t.Summary += fmt.Sprintf(", %d non completati", c.notCompleted)
		}
		out = append(out, t)
		return nil
	})
	return out, err
}

// severityRank ordina le gravità: critical prima.
const severityRank = `case severity when 'critical' then 0 when 'high' then 1 when 'medium' then 2 when 'low' then 3 else 4 end`

// certDefects legge i difetti aperti dei numeri del change.
func certDefects(ctx context.Context, tx pgx.Tx, numbers []string) (*CertDefects, error) {
	d := &CertDefects{BySeverity: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "unknown": 0}, Items: []CertDefect{}}
	rows, err := tx.Query(ctx, `select coalesce(nullif(severity, ''), 'unknown'), count(*) from test_defects
		where open and change_numbers && $1::text[] group by 1`, numbers)
	if err != nil {
		return nil, err
	}
	var sev string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&sev, &n}, func() error {
		d.BySeverity[sev] += n
		d.Open += n
		return nil
	}); err != nil {
		return nil, err
	}
	if d.Open == 0 {
		return d, nil
	}
	rows, err = tx.Query(ctx, `select external_id, summary, severity, raw_severity, status, project, url from test_defects
		where open and change_numbers && $1::text[] order by `+severityRank+`, project, external_id limit 50`, numbers)
	if err != nil {
		return nil, err
	}
	var x CertDefect
	_, err = pgx.ForEachRow(rows, []any{&x.Key, &x.Summary, &x.Severity, &x.RawSeverity, &x.Status, &x.Project, &x.URL}, func() error {
		d.Items = append(d.Items, x)
		return nil
	})
	return d, err
}

// certTestsEvidence registra l'evidenza GRC dell'esito dei test del change sui controlli mappati
// sulla sorgente «tests», alla generazione della certificazione. Senza mappatura o senza test,
// niente.
func (s *Service) certTestsEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, ch *CertChange, d *CertData, version int) error {
	if s.grc == nil || !d.TestTotals.Counted {
		return nil
	}
	m, err := s.mapped(ctx, tx, []string{sourceTests}, true)
	if err != nil || len(m[sourceTests]) == 0 {
		return err
	}
	controls := []string{}
	for _, c := range m[sourceTests] {
		controls = append(controls, c.key)
	}
	t := d.TestTotals
	summary := fmt.Sprintf("Change %s: %d test pianificati, %d eseguiti, %d superati, %d falliti, %d bloccati, %d non eseguiti.",
		ch.Number, t.Planned, t.Executed, t.Passed, t.Failed, t.Blocked, t.NotRun)
	if d.Defects != nil {
		summary += fmt.Sprintf(" Difetti aperti: %d (critici %d, alti %d).", d.Defects.Open, d.Defects.BySeverity["critical"], d.Defects.BySeverity["high"])
	}
	origins := []string{}
	for _, r := range d.Tests {
		if !containsStr(origins, r.Origin) {
			origins = append(origins, r.Origin)
		}
	}
	summary += " Fonti: " + strings.Join(origins, ", ") + "."
	in := grc.AutoEvidence{Collector: CollectorTests, ExternalRef: fmt.Sprintf("tests:%s:%d", ch.ID, version),
		Title: fmt.Sprintf("Esito dei test del change %s (certificazione v%d)", ch.Number, version), Summary: summary,
		Controls: controls, OccurredAt: &d.At}
	if s.PublicURL != "" {
		in.URL = s.link(certPath(ch.Number) + "?versione=" + fmt.Sprint(version))
	}
	_, err = s.grc.RecordAutomatic(ctx, tx, sysGRC(actor), in)
	var ve *engine.ValidationError
	if errors.As(err, &ve) {
		return nil
	}
	return err
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
