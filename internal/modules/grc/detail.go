package grc

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// DecisionView è una riga di applicabilità di un requisito o di un controllo.
type DecisionView struct {
	Scope         Scope     `json:"scope"`
	Status        string    `json:"status"`
	Rule          string    `json:"rule,omitempty"`
	Justification string    `json:"justification"`
	DecidedBy     string    `json:"decided_by"`
	DecidedAt     time.Time `json:"decided_at"`
}

func (m *model) decisions(rows []applRow) []DecisionView {
	out := []DecisionView{}
	for _, r := range rows {
		out = append(out, DecisionView{Scope: m.scopes[r.scope], Status: r.status, Rule: r.rule, Justification: r.justification,
			DecidedBy: r.decidedBy, DecidedAt: r.decidedAt})
	}
	return out
}

// ControlBrief è un controllo collegato a un requisito.
type ControlBrief struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Title         string `json:"title"`
	Phase         string `json:"phase"`
	Operational   bool   `json:"operational"`
	Coverage      string `json:"coverage"`
	Effectiveness string `json:"effectiveness"`
	Owner         string `json:"owner"`
}

// RequirementDetail è il drill-down di un requisito normativo.
type RequirementDetail struct {
	CoverageItem
	Framework FrameworkSummary `json:"framework"`
	Enabled   bool             `json:"enabled"`
	Mappings  []MappingView    `json:"mappings"`
	Decisions []DecisionView   `json:"decisions"`
	Rules     []string         `json:"rules"`
	Controls  []ControlBrief   `json:"mapped_controls"`
	CanEdit   bool             `json:"can_edit"`
}

// Requirement restituisce un requisito normativo con perimetri, controlli ed evidenze.
func (s *Service) Requirement(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*RequirementDetail, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	d := &RequirementDetail{Mappings: []MappingView{}, Decisions: []DecisionView{}, Rules: []string{}, Controls: []ControlBrief{},
		CanEdit: actor.Has(PermAdmin)}
	if r := m.reqs[id]; r != nil {
		d.CoverageItem = m.itemOf(r, true)
		d.Framework = FrameworkSummary{Key: r.fw.Key, Name: r.fw.Name, Version: r.fw.Version}
		d.Enabled = true
		d.Decisions = m.decisions(r.rows)
		for _, mp := range r.maps {
			d.Controls = append(d.Controls, ControlBrief{ID: mp.c.ID, Key: mp.c.Key, Title: mp.c.Title, Phase: mp.c.Phase,
				Operational: mp.c.operational(), Coverage: mp.coverage, Effectiveness: mp.c.Effectiveness, Owner: mp.c.Owner})
		}
	} else {
		err := tx.QueryRow(ctx, `select r.id, r.ref, coalesce(p.ref, ''), r.kind, r.title, coalesce(r.body, ''),
			r.kind not in ('chapter','annex') and not exists (select 1 from requirements c where c.parent_id = r.id), f.key, f.name, v.version
			from requirements r join framework_versions v on v.id = r.version_id join frameworks f on f.id = v.framework_id
			left join requirements p on p.id = r.parent_id where r.id = $1`, id).
			Scan(&d.ID, &d.Ref, &d.Parent, &d.Kind, &d.Title, &d.Summary, &d.Assessable, &d.Framework.Key, &d.Framework.Name, &d.Framework.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	rules, err := s.loadRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, rl := range rules {
		for _, rid := range rl.reqIDs {
			if rid == id {
				d.Rules = append(d.Rules, rl.in.Key)
			}
		}
	}
	all, err := s.Mappings(ctx, tx, actor, d.Framework.Key)
	if err != nil {
		return nil, err
	}
	for _, mv := range all {
		if mv.FromID == id || mv.ToID == id {
			d.Mappings = append(d.Mappings, mv)
		}
	}
	return d, nil
}

// ScopeItem è un requisito nel perimetro.
type ScopeItem struct {
	ID    string `json:"id"`
	Ref   string `json:"ref"`
	Title string `json:"title"`
	PairView
}

// ScopeFrameworkDetail è una normativa in un perimetro, requisito per requisito.
type ScopeFrameworkDetail struct {
	Key   string      `json:"key"`
	Name  string      `json:"name"`
	Worst string      `json:"worst"`
	Items []ScopeItem `json:"items"`
	Counts
}

// ScopeCompliance è la conformità di un perimetro (di solito un'applicazione).
type ScopeCompliance struct {
	Scope      Scope                  `json:"scope"`
	Frameworks []ScopeFrameworkDetail `json:"frameworks"`
	Counts     Counts                 `json:"counts"`
	// Tenant: requisiti che valgono per tutta l'organizzazione (e quindi anche qui).
	Tenant      Counts            `json:"tenant"`
	StatusNames map[string]string `json:"status_names"`
}

// ScopeCompliance restituisce a quali normative è soggetto il perimetro e con che stato.
func (s *Service) ScopeCompliance(ctx context.Context, tx pgx.Tx, actor engine.Actor, kind, target string) (*ScopeCompliance, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	sc, err := s.resolveScope(ctx, tx, kind, target)
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &ScopeCompliance{Scope: sc, Frameworks: []ScopeFrameworkDetail{}, StatusNames: StatusNames}
	for _, f := range m.fws {
		fd := ScopeFrameworkDetail{Key: f.Key, Name: f.Name, Items: []ScopeItem{}}
		for _, r := range f.reqs {
			for _, p := range m.pairs(r) {
				switch p.scope {
				case sc.id():
					fd.Counts.add(p)
					out.Counts.add(p)
					fd.Items = append(fd.Items, ScopeItem{ID: r.ID, Ref: r.Ref, Title: r.Title, PairView: m.pairView(p)})
				case tenantScope.id():
					out.Tenant.add(p)
				}
			}
		}
		if len(fd.Items) > 0 {
			fd.Counts.finish()
			fd.Worst = fd.Counts.Worst()
			out.Frameworks = append(out.Frameworks, fd)
		}
	}
	out.Counts.finish()
	out.Tenant.finish()
	return out, nil
}
