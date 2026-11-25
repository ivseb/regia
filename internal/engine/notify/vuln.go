package notify

import "regia/internal/platform/i18n"

// KindVulnerability: vulnerabilità governate (replica/devsecops.md §3.2): scadenze in arrivo
// o superate con l'escalation, eccezioni decise, in scadenza o scadute. L'elemento della
// notifica è l'applicazione. Sta in un file a parte per aggiungersi a Kinds senza toccare la
// lista degli altri moduli.
const KindVulnerability = "vulnerability"

func init() { Kinds = append(Kinds, KindVulnerability) }

// describeVuln è il testo delle notifiche KindVulnerability, nella lingua di chi le riceve.
func describeVuln(lang string, it Item, p i18n.P) string {
	str := func(k string) string { s, _ := it.Payload[k].(string); return s }
	num := func(k string) int { f, _ := it.Payload[k].(float64); return int(f) }
	if it.ElementKey == "" {
		p["key"] = i18n.T(lang, "notify.an_app")
	}
	p["n"] = i18n.T(lang, "notify.vuln.count", i18n.P{"count": num("count")})
	return byEvent(lang, "notify.vuln", str("event"), p)
}
