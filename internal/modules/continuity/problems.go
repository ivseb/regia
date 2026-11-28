package continuity

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/problems"
	"regia/internal/platform/i18n"
)

// KindProblem è il tipo di problema della continuità nel «dal problema all'azione».
const KindProblem = "continuity"

func registerProblems(s *Service) {
	problems.RegisterKind(KindProblem, s.resolveProblem)
}

// fmtMin scrive una durata in minuti in modo leggibile (ore e giorni interi quando si può).
func fmtMin(lang string, p *int) string {
	if p == nil {
		return i18n.T(lang, "continuity.value.none")
	}
	n := *p
	switch {
	case n >= 1440 && n%1440 == 0:
		return i18n.T(lang, "continuity.value.days", i18n.P{"count": n / 1440})
	case n >= 60 && n%60 == 0:
		return i18n.T(lang, "continuity.value.hours", i18n.P{"count": n / 60})
	}
	return i18n.T(lang, "continuity.value.minutes", i18n.P{"count": n})
}

// resolveProblem: «<codice>:<id>[:<id>]» per un'incoerenza, «finding:<id test>:<indice>» per un
// rilievo di un test. Un'incoerenza già risolta resta citabile con un testo generico.
func (s *Service) resolveProblem(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*problems.Subject, error) {
	lang := i18n.Lang(ctx)
	if strings.HasPrefix(ref, "finding:") {
		return s.resolveFinding(ctx, tx, actor, lang, ref)
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	code, rest, _ := strings.Cut(ref, ":")
	id, _, _ := strings.Cut(rest, ":")
	is := m.findIssue(ref)
	sub := &problems.Subject{Extra: map[string]string{"code": code}}
	if is == nil {
		subject := ""
		if a, ok := m.apps[id]; ok {
			sub.App, subject = a.ID, a.Title
			if a.TeamID != nil {
				sub.Team = *a.TeamID
			}
		} else if c, ok := m.caps[id]; ok {
			sub.Element, subject = c.ID, c.Title
		} else {
			return nil, engine.NotFound("ref", "", "problema della continuità inesistente")
		}
		p := i18n.P{"subject": subject}
		sub.Label = i18n.T(lang, "continuity.problem.resolved.label", p)
		sub.Title = i18n.T(lang, "continuity.problem.resolved.title", p)
		sub.Body = i18n.T(lang, "continuity.problem.resolved.body", p)
		return sub, nil
	}
	subject := ""
	if is.Application != nil {
		sub.App, subject = is.Application.ID, is.Application.Title
	} else if is.Capability != nil {
		sub.Element, subject = is.Capability.ID, is.Capability.Title
	}
	if is.Team != nil {
		sub.Team = is.Team.ID
	}
	other, source, days := "", "", ""
	if is.Other != nil {
		other = is.Other.Title
	}
	if is.Source != nil {
		source = is.Source.Key + " · " + is.Source.Title
	}
	if is.Days != nil {
		days = strconv.Itoa(*is.Days)
	}
	p := i18n.P{"subject": subject, "other": other, "declared": fmtMin(lang, is.Declared), "required": fmtMin(lang, is.Required),
		"other_value": fmtMin(lang, is.OtherValue), "source": source, "days": days}
	sub.Label = i18n.T(lang, "continuity.issue."+is.Code, p)
	sub.Title = i18n.T(lang, "continuity.problem.title", i18n.P{"text": sub.Label})
	sub.Body = i18n.T(lang, "continuity.problem.body", i18n.P{"text": sub.Label, "action": i18n.T(lang, "continuity.action."+is.Action.Kind)})
	sub.Reason = i18n.T(lang, "continuity.problem.reason", i18n.P{"text": sub.Label})
	return sub, nil
}

func (s *Service) resolveFinding(ctx context.Context, tx pgx.Tx, actor engine.Actor, lang, ref string) (*problems.Subject, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 3 {
		return nil, engine.NotFound("ref", "", "rilievo inesistente")
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil || idx < 0 {
		return nil, engine.NotFound("ref", "", "rilievo inesistente")
	}
	el, err := s.testElement(ctx, tx, actor, parts[1])
	if err != nil {
		return nil, err
	}
	var raw []byte
	var subjID, subjType, subjTitle string
	var team *string
	if err := tx.QueryRow(ctx, `select x.findings, x.subject_id::text, st.key, se.title, a.owner_team_id::text
		from ext_resilience_test x join elements se on se.tenant_id = x.tenant_id and se.id = x.subject_id
		join element_types st on st.tenant_id = se.tenant_id and st.id = se.type_id
		left join ext_application a on a.tenant_id = x.tenant_id and a.element_id = x.subject_id
		where x.element_id = $1`, el.ID).Scan(&raw, &subjID, &subjType, &subjTitle, &team); err != nil {
		return nil, err
	}
	var texts []string
	if err := json.Unmarshal(raw, &texts); err != nil {
		return nil, err
	}
	if idx >= len(texts) {
		return nil, engine.NotFound("ref", "", "rilievo inesistente")
	}
	sub := &problems.Subject{Element: el.ID, Extra: map[string]string{"test": el.Key}}
	if subjType == AppType {
		sub.App = subjID
	}
	if team != nil {
		sub.Team = *team
	}
	p := i18n.P{"test": el.Key + " · " + el.Title, "finding": texts[idx], "subject": subjTitle}
	sub.Label = texts[idx]
	sub.Title = i18n.T(lang, "continuity.finding.title", p)
	sub.Body = i18n.T(lang, "continuity.finding.body", p)
	sub.Reason = i18n.T(lang, "continuity.finding.reason", p)
	return sub, nil
}
