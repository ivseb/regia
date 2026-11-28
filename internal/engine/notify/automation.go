package notify

import "regia/internal/platform/i18n"

// KindAutomation: notifica mandata da una regola di processo (azione «notify», automazioni).
// Il payload porta il nome della regola (rule) e il testo già composto dal modello (message).
//
// Sta in un file a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli.
const KindAutomation = "automation"

func init() {
	Kinds = append(Kinds, KindAutomation)
	i18n.Register(map[string]i18n.Msg{
		"notify.automation":       {It: "{key}: {message}", En: "{key}: {message}"},
		"notify.automation.empty": {It: "{key}: la regola «{rule}» ti segnala l'elemento", En: "{key}: rule “{rule}” flags this item for you"},
	})
}

func describeAutomation(lang string, it Item, p i18n.P) string {
	if m, _ := it.Payload["message"].(string); m != "" {
		return i18n.T(lang, "notify.automation", p)
	}
	return i18n.T(lang, "notify.automation.empty", p)
}
