package devsecops

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Regola del gate «tests»: i test del numero di change, dalle tabelle generiche dei test (oggi
// il connettore ALM in lettura). Contano i test set del change che sono dell'applicazione del
// rilascio o di nessuna applicazione (unità non ancora confermata): un change con più
// applicazioni non blocca un'applicazione per i test di un'altra.

// severities dalla gravità indicata in su.
func severitiesFrom(sev string) []string {
	all := []string{"critical", "high", "medium", "low", ""}
	for i, s := range all {
		if s == sev {
			if sev == "low" {
				return all // da «bassa» in su conta anche la gravità sconosciuta
			}
			return all[:i+1]
		}
	}
	return nil
}

var sevLabel = map[string]string{"critical": "critici", "high": "critici o alti", "medium": "di gravità media o superiore", "low": "di qualunque gravità"}

func (s *Service) ruleTests(ctx context.Context, tx pgx.Tx, g *gateCtx, r GateRule) ([]GateReason, error) {
	var p testsParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	numbers, err := changeNumbers(ctx, tx, g)
	if err != nil {
		return nil, err
	}
	if len(numbers) == 0 {
		if p.RequireResults {
			return []GateReason{g.reason(r, "fail", "Nessun numero di change: servono i test del change.", "")}, nil
		}
		return []GateReason{g.reason(r, "pass", "Nessun numero di change: la regola non si applica.", "")}, nil
	}
	label := numbers[0]
	link := s.link("/change/" + url.PathEscape(label) + "/certificazione")
	var c testCounts
	var sets int
	err = tx.QueryRow(ctx, `select count(distinct s.id), `+countCols+`
		from test_sets s left join test_set_items i on i.tenant_id = s.tenant_id and i.test_set_id = s.id
		where s.change_number = any($1) and (s.application_element_id is null or s.application_element_id::text = $2)`, numbers, g.appID).
		Scan(&sets, &c.planned, &c.passed, &c.failed, &c.blocked, &c.notCompleted, &c.noRun, &c.na)
	if err != nil {
		return nil, err
	}
	if c.planned == 0 {
		msg := fmt.Sprintf("Nessun test per il change %s: controlla il numero nel test set e la sincronizzazione del connettore ALM.", label)
		if p.RequireResults {
			return []GateReason{g.reason(r, "fail", msg, link)}, nil
		}
		return []GateReason{g.reason(r, "pass", fmt.Sprintf("Nessun test per il change %s: la regola non si applica.", label), link)}, nil
	}
	base := c.planned - c.na
	pct := 100
	if base > 0 {
		pct = c.executed() * 100 / base
	}
	var problems []string
	if c.failed > p.MaxFailed {
		problems = append(problems, fmt.Sprintf("%d falliti (tollerati %d)", c.failed, p.MaxFailed))
	}
	if p.MaxBlocked != nil && c.blocked > *p.MaxBlocked {
		problems = append(problems, fmt.Sprintf("%d bloccati (tollerati %d)", c.blocked, *p.MaxBlocked))
	}
	if pct < p.MinExecutedPct {
		problems = append(problems, fmt.Sprintf("eseguiti il %d%% (minimo %d%%)", pct, p.MinExecutedPct))
	}
	open := 0
	if sevs := severitiesFrom(p.DefectsSeverity); sevs != nil {
		if err := tx.QueryRow(ctx, `select count(*) from test_defects where open and change_numbers && $1::text[] and severity = any($2)`,
			numbers, sevs).Scan(&open); err != nil {
			return nil, err
		}
		if open > p.MaxOpenDefects {
			problems = append(problems, fmt.Sprintf("%d difetti aperti %s (tollerati %d)", open, sevLabel[p.DefectsSeverity], p.MaxOpenDefects))
		}
	}
	summary := fmt.Sprintf("Change %s: %d test in %d test set, eseguiti %d (%d%%), superati %d, falliti %d, bloccati %d, non eseguiti %d",
		label, c.planned, sets, c.executed(), pct, c.passed, c.failed, c.blocked, c.noRun)
	if p.DefectsSeverity != "" {
		summary += fmt.Sprintf(", difetti aperti %s %d", sevLabel[p.DefectsSeverity], open)
	}
	if len(problems) > 0 {
		return []GateReason{g.reason(r, "fail", summary+". Non va: "+strings.Join(problems, "; ")+".", link)}, nil
	}
	return []GateReason{g.reason(r, "pass", summary+".", link)}, nil
}
