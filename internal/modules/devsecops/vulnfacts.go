package devsecops

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
)

// Fatti delle vulnerabilità per le condizioni dei processi: su un'applicazione contano le sue
// vulnerabilità, su una richiesta quelle delle applicazioni toccate dai suoi impatti. Per
// esempio un gate può dire «vuln_scadute: 0» prima di un rilascio, o una regola può aprire un
// rischio se «vuln_eccezioni» supera una soglia.

func (s *Service) registerVuln(eng *engine.Service) {
	// Nel linguaggio di query (ondata 3 dell'AI): le applicazioni con vulnerabilità da correggere
	// per data di scadenza, così «applicazioni con vulnerabilità scadute» è
	// «tipo: application scadenza_vulnerabilità: <oggi». Indici: findings_work_idx (scadenza) e
	// (tenant_id, application_element_id, status).
	eng.QueryField(engine.QueryField{
		Key: "vuln_due", Names: []string{"scadenza_vulnerabilità", "scadenza_vulnerabilita", "vulnerabilità", "vulnerabilita", "vulnerability_due"},
		Label: "Scadenza delle vulnerabilità da correggere (applicazioni): <oggi = scadute, -7g..+7g = in scadenza",
		Kind:  "date", Types: []string{architecture.AppType},
		Source: `select application_element_id as element_id, due_at as v from findings
			where stage in ` + openStages + ` and due_at is not null and application_element_id is not null`,
	})
	eng.Facts(architecture.AppType, s.vulnAppFacts)
	eng.Facts(architecture.RequestType, s.vulnRequestFacts)
	eng.DescribeFacts(architecture.AppType,
		engine.FactInfo{Key: "vuln_scadute", Name: "Vulnerabilità da correggere oltre la scadenza", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "vuln_in_scadenza", Name: "Vulnerabilità da correggere in scadenza (giorni del preavviso della politica)", Kind: "number", Example: "3"},
		engine.FactInfo{Key: "vuln_eccezioni", Name: "Eccezioni sulle vulnerabilità in vigore", Kind: "number", Example: "1"},
	)
	eng.DescribeFacts(architecture.RequestType,
		engine.FactInfo{Key: "vuln_scadute", Name: "Vulnerabilità scadute nelle applicazioni toccate dagli impatti", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "vuln_in_scadenza", Name: "Vulnerabilità in scadenza nelle applicazioni toccate dagli impatti", Kind: "number", Example: "3"},
		engine.FactInfo{Key: "vuln_eccezioni", Name: "Eccezioni in vigore sulle applicazioni toccate dagli impatti", Kind: "number", Example: "1"},
	)
}

const vulnFactsSQL = `
	select count(*) filter (where f.due_at < $2), count(*) filter (where f.due_at >= $2 and f.due_at < $2 + $3::int * interval '1 day'),
	       (select count(*) from vuln_exceptions x where x.application_id = any($1::uuid[]) and x.status = 'approved' and x.until >= $2::date)
	from findings f where f.application_element_id = any($1::uuid[]) and f.stage in ` + openStages + ` and f.due_at is not null`

func (s *Service) setVulnFacts(ctx context.Context, tx pgx.Tx, apps []string, f engine.Facts) error {
	if len(apps) == 0 {
		f.Set("vuln_scadute", 0)
		f.Set("vuln_in_scadenza", 0)
		f.Set("vuln_eccezioni", 0)
		return nil
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return err
	}
	var over, soon, exc int
	if err := tx.QueryRow(ctx, vulnFactsSQL, apps, s.now(), pol.SoonDays).Scan(&over, &soon, &exc); err != nil {
		return err
	}
	f.Set("vuln_scadute", over)
	f.Set("vuln_in_scadenza", soon)
	f.Set("vuln_eccezioni", exc)
	return nil
}

func (s *Service) vulnAppFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	return s.setVulnFacts(ctx, tx, []string{el.ID}, f)
}

func (s *Service) vulnRequestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	rows, err := tx.Query(ctx, "select distinct application_id::text from ext_impact where request_id = $1", el.ID)
	if err != nil {
		return err
	}
	apps, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	return s.setVulnFacts(ctx, tx, apps, f)
}
