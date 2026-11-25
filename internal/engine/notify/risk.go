package notify

import "regia/internal/platform/i18n"

// KindRisk: rischi, fornitori e incidenti (piano, passo 20): accettazione del rischio
// decisa, revisione o trattamento in scadenza, contratto ICT in scadenza, incidente grave o
// significativo, notifica normativa in scadenza o scaduta. Sta in un file a parte per
// aggiungersi a Kinds senza toccare la lista degli altri moduli.
const KindRisk = "risk"

func init() { Kinds = append(Kinds, KindRisk) }

// describeRisk è il testo delle notifiche KindRisk, nella lingua di chi le riceve.
func describeRisk(lang string, it Item, p i18n.P) string {
	str := func(k string) string { s, _ := it.Payload[k].(string); return s }
	return byEvent(lang, "notify.risk", str("event"), p)
}
