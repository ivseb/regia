package grc

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Rischio ↔ controlli: nella scheda del rischio i controlli di trattamento (relazione
// «mitigates», dal controllo al rischio) con l'efficacia ufficiale, la proposta del sistema e le
// evidenze, e ciò che ha concretizzato il rischio nel grafo: gli incidenti («materializes») e le
// CVE delle eccezioni sulle vulnerabilità collegate al rischio (vuln_exceptions.risk_element_id).

// RiskControl è un controllo di trattamento del rischio.
type RiskControl struct {
	Key           string     `json:"key"`
	Title         string     `json:"title"`
	Owner         string     `json:"owner"`
	Effectiveness string     `json:"effectiveness"`
	Proposed      string     `json:"proposed"` // proposta in attesa di conferma (effective | partial) o vuota
	ProposalState string     `json:"proposal_state"`
	ValidEvidence int        `json:"valid_evidence"`
	NextExpiry    *time.Time `json:"next_expiry"`
	Operational   bool       `json:"operational"`
	Detail        bool       `json:"detail"` // l'attore vede efficacia ed evidenze
}

// RiskIncident è un incidente che ha concretizzato il rischio.
type RiskIncident struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// RiskCVE è una vulnerabilità accettata (eccezione) collegata al rischio.
type RiskCVE struct {
	CVE         string `json:"cve"`
	Severity    string `json:"severity"`
	Application string `json:"application"`
	Exception   int    `json:"exception"`
	Status      string `json:"status"`
}

// RiskControlsView è la vista per la scheda del rischio.
type RiskControlsView struct {
	Controls  []RiskControl  `json:"controls"`
	Incidents []RiskIncident `json:"incidents"`
	CVEs      []RiskCVE      `json:"cves"`
}

// RiskControls restituisce i controlli di trattamento e i fatti che hanno concretizzato il
// rischio. Chi vede il rischio vede l'elenco; efficacia ed evidenze solo chi legge la conformità.
func (s *Service) RiskControls(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*RiskControlsView, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != "risk" {
		return nil, engine.ErrNotFound
	}
	out := &RiskControlsView{Controls: []RiskControl{}, Incidents: []RiskIncident{}, CVEs: []RiskCVE{}}
	rows, err := tx.Query(ctx, `select e.id::text from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'mitigates'
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null and e.type_id = (select id from element_types where key = $2)
		where r.target_id = $1 order by e.number`, el.ID, ControlType)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		m, err := s.loadModel(ctx, tx)
		if err != nil {
			return nil, err
		}
		detail := CanRead(actor)
		for _, id := range ids {
			c := m.ctrls[id]
			if c == nil {
				continue
			}
			rc := RiskControl{Key: c.Key, Title: c.Title, Owner: c.Owner, Detail: detail}
			if detail {
				sum := m.summary(c)
				rc.Effectiveness, rc.ValidEvidence, rc.NextExpiry, rc.Operational = c.Effectiveness, sum.ValidEvidence, sum.NextExpiry, c.operational()
				p, err := s.proposalFor(ctx, tx, actor, m, c)
				if err != nil {
					return nil, err
				}
				rc.ProposalState = p.State
				if p.Pending {
					rc.Proposed = p.Proposed
				}
			}
			out.Controls = append(out.Controls, rc)
		}
	}
	rows, err = tx.Query(ctx, `select sp.key || '-' || e.number, e.title from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'materializes'
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where r.target_id = $1 order by e.number`, el.ID)
	if err != nil {
		return nil, err
	}
	var inc RiskIncident
	if _, err := pgx.ForEachRow(rows, []any{&inc.Key, &inc.Title}, func() error {
		out.Incidents = append(out.Incidents, inc)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select distinct f.cve, f.severity, sp.key || '-' || ae.number, x.number, x.status
		from vuln_exceptions x join vuln_exception_findings vf on vf.tenant_id = x.tenant_id and vf.exception_id = x.id
		join findings f on f.tenant_id = vf.tenant_id and f.id = vf.finding_id
		join elements ae on ae.tenant_id = x.tenant_id and ae.id = x.application_id
		join spaces sp on sp.tenant_id = ae.tenant_id and sp.id = ae.space_id
		where x.risk_element_id = $1 and f.cve is not null order by x.number, f.cve limit 30`, el.ID)
	if err != nil {
		return nil, err
	}
	var cv RiskCVE
	if _, err := pgx.ForEachRow(rows, []any{&cv.CVE, &cv.Severity, &cv.Application, &cv.Exception, &cv.Status}, func() error {
		out.CVEs = append(out.CVEs, cv)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
