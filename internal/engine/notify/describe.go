package notify

import (
	"fmt"
	"strings"

	"regia/internal/platform/i18n"
)

// Describe è il testo di una notifica in italiano (lo stesso dell'interfaccia).
func Describe(it Item) string { return DescribeLang(i18n.It, it) }

// DescribeLang è il testo di una notifica nella lingua di chi la riceve.
func DescribeLang(lang string, it Item) string {
	str := func(k string) string { s, _ := it.Payload[k].(string); return s }
	num := func(k string) string {
		if v := it.Payload[k]; v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	key := it.ElementKey
	if key == "" {
		key = i18n.T(lang, "notify.an_item")
	}
	actor := it.ActorName
	if actor == "" {
		actor = i18n.T(lang, "notify.system")
	}
	// Parametri comuni: i campi del payload hanno il loro nome, actor e key sono quelli della notifica.
	p := i18n.P{"actor": actor, "key": key}
	for k, v := range it.Payload {
		if _, taken := p[k]; !taken {
			p[k] = v
		}
	}
	t := func(k string) string { return i18n.T(lang, k, p) }
	// Scadenza opzionale in coda ai testi di lettura: « entro il …» / « by …».
	if due := str("due"); due != "" {
		p["due_suffix"] = i18n.T(lang, "notify.due_suffix", i18n.P{"due": due})
	} else {
		p["due_suffix"] = ""
	}
	event := str("event")
	switch it.Kind {
	case KindMention:
		if inline, _ := it.Payload["inline"].(bool); inline {
			return t("notify.mention.inline")
		}
		return t("notify.mention")
	case KindComment:
		if k := "notify.comment." + event; event != "" && i18n.Has(k) {
			return t(k)
		}
		return t("notify.comment")
	case KindRole:
		p["role"] = strings.ToLower(str("role_name"))
		if g, _ := it.Payload["group"].(bool); g {
			return t("notify.role.group")
		}
		return t("notify.role")
	case KindPhase:
		return t("notify.phase")
	case KindApproval:
		return t("notify.approval")
	case KindReferent:
		return t("notify.referent")
	case KindReview:
		return byEvent(lang, "notify.review", event, p)
	case KindSLA:
		return byEvent(lang, "notify.sla", event, p)
	case KindReport:
		p["status"] = str("overall")
		if k := "notify.status." + str("overall"); i18n.Has(k) {
			p["status"] = i18n.T(lang, k)
		}
		return byEvent(lang, "notify.report", event, p)
	case KindRequirement:
		return byEvent(lang, "notify.requirement", event, p)
	case KindDocument:
		p["version"] = num("version")
		p["missing"] = num("missing")
		if event == "reading_overdue_summary" {
			p["count"] = num("missing")
		}
		p["due"] = str("due")
		for _, e := range []string{"reading_requested", "reading_reminder", "reading_reopened"} {
			if event == e {
				p["due"] = p["due_suffix"] // il testo di queste voci chiude con la scadenza facoltativa
			}
		}
		return byEvent(lang, "notify.document", event, p)
	case KindRisk:
		return describeRisk(lang, it, p)
	case KindVulnerability:
		return describeVuln(lang, it, p)
	case KindAudit:
		return describeAudit(lang, it, p)
	case KindBudget:
		return describeBudget(lang, it)
	case KindTimesheet:
		return describeTimesheet(lang, it, p)
	case KindProject:
		return byEvent(lang, "notify.project", event, p)
	case KindAI:
		p["percent"] = num("percent")
		return byEvent(lang, "notify.ai", event, p)
	}
	return i18n.T(lang, "notify.other", i18n.P{"kind": it.Kind, "key": key})
}

// byEvent sceglie «base.evento» se esiste, altrimenti «base».
func byEvent(lang, base, event string, p i18n.P) string {
	if k := base + "." + event; event != "" && i18n.Has(k) {
		return i18n.T(lang, k, p)
	}
	return i18n.T(lang, base, p)
}
