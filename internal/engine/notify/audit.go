package notify

import "regia/internal/platform/i18n"

// KindAudit: spazio dell'auditor (ondata 3, C6): richieste di evidenza e risposte, commenti,
// rilievi aperti, piani d'azione e chiusure. L'elemento della notifica è il controllo. Sta in
// un file a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli.
const KindAudit = "audit"

func init() {
	Kinds = append(Kinds, KindAudit)
	i18n.Register(map[string]i18n.Msg{
		"notify.audit.request_opened":   {It: "{actor} chiede un'evidenza su {key}: {title}{due_suffix}", En: "{actor} requests evidence on {key}: {title}{due_suffix}"},
		"notify.audit.request_answered": {It: "{actor} ha risposto alla richiesta su {key}: {title}", En: "{actor} answered the request on {key}: {title}"},
		"notify.audit.request_closed":   {It: "{actor} ha chiuso la richiesta su {key}: {title}", En: "{actor} closed the request on {key}: {title}"},
		"notify.audit.comment":          {It: "{actor} ha commentato {key} nell'audit", En: "{actor} commented on {key} in the audit"},
		"notify.audit.finding_opened":   {It: "{actor} ha aperto un rilievo su {key}: {title}", En: "{actor} opened a finding on {key}: {title}"},
		"notify.audit.finding_plan":     {It: "{actor} ha proposto il piano d'azione del rilievo su {key}: {title}", En: "{actor} proposed the action plan for the finding on {key}: {title}"},
		"notify.audit.finding_closed":   {It: "{actor} ha chiuso il rilievo su {key}: {title}", En: "{actor} closed the finding on {key}: {title}"},
		"notify.audit":                  {It: "Audit su {key}", En: "Audit on {key}"},
	})
}

// describeAudit è il testo delle notifiche KindAudit, nella lingua di chi le riceve.
func describeAudit(lang string, it Item, p i18n.P) string {
	str := func(k string) string { s, _ := it.Payload[k].(string); return s }
	return byEvent(lang, "notify.audit", str("event"), p)
}
