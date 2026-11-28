package continuity

import (
	"strings"
)

// Codici delle incoerenze.
const (
	IssueRTOOver        = "rto_over_required"
	IssueRPOOver        = "rpo_over_required"
	IssueRTOMissing     = "rto_missing"
	IssueDepSlower      = "dependency_slower"
	IssueDepMissing     = "dependency_rto_missing"
	IssueCriticalUndecl = "critical_not_declared"
	IssuePlanMissing    = "plan_missing"
	IssueTestMissing    = "test_missing"
	IssueTestFailed     = "test_failed"
	IssueBIAMissing     = "bia_missing"
	IssueBIAReview      = "bia_review_due"
)

// IssueCodes sono i codici, nell'ordine in cui si presentano.
var IssueCodes = []string{IssueRTOOver, IssueRPOOver, IssueRTOMissing, IssueDepSlower, IssueDepMissing, IssueCriticalUndecl,
	IssuePlanMissing, IssueTestMissing, IssueTestFailed, IssueBIAMissing, IssueBIAReview}

// OtherRef è la dipendenza o il fornitore di un'incoerenza.
type OtherRef struct {
	Ref
	Kind string `json:"kind"` // application | provider
}

// TeamRef è il team dell'applicazione.
type TeamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Action è l'azione diretta proposta.
type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// Issue è un'incoerenza della continuità.
type Issue struct {
	Code        string    `json:"code"`
	Ref         string    `json:"ref"`
	Severity    string    `json:"severity"`
	Application *Ref      `json:"application"`
	Capability  *Ref      `json:"capability"`
	Other       *OtherRef `json:"other"`
	Declared    *int      `json:"declared"`
	Required    *int      `json:"required"`
	OtherValue  *int      `json:"other_value"`
	Days        *int      `json:"days"`
	Source      *Ref      `json:"source"`
	Team        *TeamRef  `json:"team"`
	Action      Action    `json:"action"`
}

func issueRef(code string, ids ...string) string {
	return code + ":" + strings.Join(ids, ":")
}

func refOf(r Ref) *Ref { x := r; return &x }

func intp(n int) *int { return &n }

func severity(code string) string {
	switch code {
	case IssueDepMissing, IssueCriticalUndecl, IssueBIAMissing, IssueBIAReview:
		return "medium"
	}
	return "high"
}

// appIssues sono le incoerenze di un'applicazione.
func (m *model) appIssues(app *appN) []Issue {
	r := m.required(app)
	var team *TeamRef
	if app.TeamID != nil && app.Team != nil {
		team = &TeamRef{ID: *app.TeamID, Name: *app.Team}
	}
	var source *Ref
	if r.Source != nil {
		source = refOf(r.Source.Ref)
	}
	out := []Issue{}
	add := func(i Issue) {
		i.Severity = severity(i.Code)
		i.Application = refOf(app.Ref)
		if i.Team == nil {
			i.Team = team
		}
		out = append(out, i)
	}
	if r.RTO != nil {
		if app.RTO == nil {
			add(Issue{Code: IssueRTOMissing, Ref: issueRef(IssueRTOMissing, app.ID), Required: r.RTO, Source: source, Action: Action{"set_rto", app.Key}})
		} else if *app.RTO > *r.RTO {
			add(Issue{Code: IssueRTOOver, Ref: issueRef(IssueRTOOver, app.ID), Declared: app.RTO, Required: r.RTO, Source: source, Action: Action{"set_rto", app.Key}})
		}
	}
	if r.RPO != nil && app.RPO != nil && *app.RPO > *r.RPO {
		add(Issue{Code: IssueRPOOver, Ref: issueRef(IssueRPOOver, app.ID), Declared: app.RPO, Required: r.RPO, Source: source, Action: Action{"set_rpo", app.Key}})
	}
	if eff := effective(app, r); eff != nil {
		for _, d := range m.deps[app.ID] {
			dep := m.apps[d]
			o := &OtherRef{Ref: dep.Ref, Kind: "application"}
			if dep.RTO == nil {
				add(Issue{Code: IssueDepMissing, Ref: issueRef(IssueDepMissing, app.ID, dep.ID), Other: o, Declared: eff, Action: Action{"set_rto", dep.Key}})
			} else if *dep.RTO > *eff {
				add(Issue{Code: IssueDepSlower, Ref: issueRef(IssueDepSlower, app.ID, dep.ID), Other: o, Declared: eff, OtherValue: dep.RTO, Action: Action{"set_rto", dep.Key}})
			}
		}
		for _, pid := range m.provided[app.ID] {
			p := m.provs[pid]
			o := &OtherRef{Ref: p.Ref, Kind: "provider"}
			if p.RTO == nil {
				add(Issue{Code: IssueDepMissing, Ref: issueRef(IssueDepMissing, app.ID, p.ID), Other: o, Declared: eff, Action: Action{"set_provider_rto", p.Key}})
			} else if *p.RTO > *eff {
				add(Issue{Code: IssueDepSlower, Ref: issueRef(IssueDepSlower, app.ID, p.ID), Other: o, Declared: eff, OtherValue: p.RTO, Action: Action{"set_provider_rto", p.Key}})
			}
		}
	}
	if r.Critical && !app.Critical {
		add(Issue{Code: IssueCriticalUndecl, Ref: issueRef(IssueCriticalUndecl, app.ID), Source: source, Action: Action{"set_critical", app.Key}})
	}
	if app.Critical || r.Critical {
		if !m.approvedPlan(append([]string{app.ID}, r.Caps...)...) {
			add(Issue{Code: IssuePlanMissing, Ref: issueRef(IssuePlanMissing, app.ID), Action: Action{"write_plan", app.Key}})
		}
		// Vale anche un test fatto sulla capability (per esempio uno scenario o un tabletop).
		out = append(out, m.testIssues(app.ID, refOf(app.Ref), nil, team, append([]string{app.ID}, r.Caps...))...)
	}
	return out
}

// testIssues: test mancante, scaduto o fallito per una funzione critica.
func (m *model) testIssues(subjectID string, app, capability *Ref, team *TeamRef, ids []string) []Issue {
	last := m.lastDone(ids...)
	subjectKey := ""
	if app != nil {
		subjectKey = app.Key
	} else if capability != nil {
		subjectKey = capability.Key
	}
	mk := func(code string) Issue {
		return Issue{Code: code, Ref: issueRef(code, subjectID), Severity: severity(code), Application: app, Capability: capability, Team: team}
	}
	if last == nil {
		i := mk(IssueTestMissing)
		i.Action = Action{"plan_test", subjectKey}
		return []Issue{i}
	}
	days := m.daysSince(*last.Executed)
	if days > m.cfg.TestMaxAgeDays {
		i := mk(IssueTestMissing)
		i.Days = intp(days)
		i.Action = Action{"plan_test", subjectKey}
		return []Issue{i}
	}
	if *last.Outcome == OutcomeFailed {
		i := mk(IssueTestFailed)
		i.Days = intp(days)
		i.Action = Action{"open_test", last.Key}
		return []Issue{i}
	}
	return nil
}

// capIssues sono le incoerenze di una capability.
func (m *model) capIssues(c *capN) []Issue {
	out := []Issue{}
	bn := m.bias[c.ID]
	apps := m.appsOfCap(c.ID)
	criticalApps := 0
	for _, a := range apps {
		if a.Critical {
			criticalApps++
		}
	}
	cref := refOf(c.Ref)
	if bn == nil || bn.Approved == nil {
		if criticalApps > 0 {
			i := Issue{Code: IssueBIAMissing, Ref: issueRef(IssueBIAMissing, c.ID), Severity: severity(IssueBIAMissing), Capability: cref, Action: Action{"start_bia", c.Key}}
			if bn != nil {
				i.Action = Action{"review_bia", bn.Key}
			}
			out = append(out, i)
		}
		return out
	}
	months := m.cfg.BIAReviewMonths
	if due := bn.Approved.At.AddDate(0, months, 0); m.now.After(due) {
		days := m.daysSince(bn.Approved.At)
		out = append(out, Issue{Code: IssueBIAReview, Ref: issueRef(IssueBIAReview, c.ID), Severity: severity(IssueBIAReview), Capability: cref,
			Source: refOf(bn.Ref), Days: intp(days), Action: Action{"review_bia", bn.Key}})
	}
	if bn.Approved.Critical {
		ancestors := m.ancestors(c.ID)
		if !m.approvedPlan(ancestors...) {
			out = append(out, Issue{Code: IssuePlanMissing, Ref: issueRef(IssuePlanMissing, c.ID), Severity: severity(IssuePlanMissing), Capability: cref,
				Action: Action{"write_plan", c.Key}})
		}
		ids := []string{c.ID}
		for _, a := range apps {
			ids = append(ids, a.ID)
		}
		out = append(out, m.testIssues(c.ID, nil, cref, nil, ids)...)
	}
	return out
}

// allIssues sono le incoerenze di tutto il tenant, prima le gravi.
func (m *model) allIssues() []Issue {
	out := []Issue{}
	for _, id := range m.capOrder {
		out = append(out, m.capIssues(m.caps[id])...)
	}
	for _, id := range m.appOrder {
		out = append(out, m.appIssues(m.apps[id])...)
	}
	sortIssues(out)
	return out
}

// findIssue cerca un'incoerenza per riferimento.
func (m *model) findIssue(ref string) *Issue {
	code, rest, _ := strings.Cut(ref, ":")
	id, _, _ := strings.Cut(rest, ":")
	var list []Issue
	if a, ok := m.apps[id]; ok {
		list = m.appIssues(a)
	} else if c, ok := m.caps[id]; ok {
		list = m.capIssues(c)
	}
	for i := range list {
		if list[i].Ref == ref && list[i].Code == code {
			return &list[i]
		}
	}
	return nil
}
