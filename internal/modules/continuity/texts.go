package continuity

import "regia/internal/platform/i18n"

// Testi del server: titoli proposti, testi del lavoro creato da un'incoerenza o da un rilievo,
// sintesi dell'evidenza. Si scrivono nella lingua di chi agisce.
func init() {
	i18n.Register(map[string]i18n.Msg{
		"continuity.bia.title":  {It: "BIA · {capability}", En: "BIA · {capability}"},
		"continuity.test.title": {It: "{type} · {subject} · {year}", En: "{type} · {subject} · {year}"},

		"continuity.value.none":    {It: "non indicato", En: "not set"},
		"continuity.value.minutes": {It: "{count} minuti", En: "{count} minutes", OneIt: "{count} minuto", OneEn: "{count} minute"},
		"continuity.value.hours":   {It: "{count} ore", En: "{count} hours", OneIt: "{count} ora", OneEn: "{count} hour"},
		"continuity.value.days":    {It: "{count} giorni", En: "{count} days", OneIt: "{count} giorno", OneEn: "{count} day"},

		"continuity.issue.rto_over_required": {It: "{subject} dichiara RTO {declared}, ma la BIA chiede {required}",
			En: "{subject} declares an RTO of {declared}, but the BIA requires {required}"},
		"continuity.issue.rpo_over_required": {It: "{subject} dichiara RPO {declared}, ma la BIA chiede {required}",
			En: "{subject} declares an RPO of {declared}, but the BIA requires {required}"},
		"continuity.issue.rto_missing": {It: "{subject} non dichiara l'RTO, la BIA chiede {required}",
			En: "{subject} has no declared RTO; the BIA requires {required}"},
		"continuity.issue.dependency_slower": {It: "{subject} ha RTO {declared} ma dipende da {other} con RTO {other_value}",
			En: "{subject} has an RTO of {declared} but depends on {other} with an RTO of {other_value}"},
		"continuity.issue.dependency_rto_missing": {It: "{subject} ha RTO {declared} e dipende da {other}, che non dichiara l'RTO",
			En: "{subject} has an RTO of {declared} and depends on {other}, which has no declared RTO"},
		"continuity.issue.critical_not_declared": {It: "{subject} serve una funzione critica secondo la BIA, ma non è segnata come tale",
			En: "{subject} supports a critical function according to the BIA but is not marked as such"},
		"continuity.issue.plan_missing": {It: "{subject} è una funzione critica senza un piano di continuità approvato",
			En: "{subject} is a critical function without an approved continuity plan"},
		"continuity.issue.test_missing": {It: "{subject} è una funzione critica senza un test di resilienza recente",
			En: "{subject} is a critical function without a recent resilience test"},
		"continuity.issue.test_failed": {It: "L'ultimo test di resilienza di {subject} non è stato superato",
			En: "The latest resilience test of {subject} failed"},
		"continuity.issue.bia_missing": {It: "{subject} ha applicazioni critiche ma nessuna BIA approvata",
			En: "{subject} has critical applications but no approved BIA"},
		"continuity.issue.bia_review_due": {It: "La BIA di {subject} è stata approvata {days} giorni fa: va riesaminata",
			En: "The BIA of {subject} was approved {days} days ago: it is due for review"},

		"continuity.action.set_rto":          {It: "rivedere l'RTO dichiarato o l'architettura di ripristino", En: "review the declared RTO or the recovery architecture"},
		"continuity.action.set_rpo":          {It: "rivedere l'RPO dichiarato o la frequenza dei backup", En: "review the declared RPO or the backup frequency"},
		"continuity.action.set_critical":     {It: "segnare l'applicazione come funzione critica o motivare il contrario", En: "mark the application as a critical function or justify why not"},
		"continuity.action.set_provider_rto": {It: "riportare l'impegno di ripristino nel contratto del fornitore", En: "record the recovery commitment in the provider contract"},
		"continuity.action.write_plan":       {It: "scrivere e approvare il piano di continuità", En: "write and approve the continuity plan"},
		"continuity.action.plan_test":        {It: "pianificare un test di resilienza", En: "plan a resilience test"},
		"continuity.action.start_bia":        {It: "avviare la BIA della capability", En: "start the BIA of the capability"},
		"continuity.action.review_bia":       {It: "riesaminare e riapprovare la BIA", En: "review and re-approve the BIA"},
		"continuity.action.open_test":        {It: "aprire il test e trasformare i rilievi in azioni", En: "open the test and turn the findings into actions"},

		"continuity.problem.title":  {It: "Continuità: {text}", En: "Continuity: {text}"},
		"continuity.problem.body":   {It: "Origine: continuità operativa.\n{text}.\nDa fare: {action}.", En: "Source: business continuity.\n{text}.\nTo do: {action}."},
		"continuity.problem.reason": {It: "{text}: ", En: "{text}: "},

		"continuity.problem.resolved.label": {It: "Continuità di {subject}", En: "Continuity of {subject}"},
		"continuity.problem.resolved.title": {It: "Continuità di {subject}", En: "Continuity of {subject}"},
		"continuity.problem.resolved.body":  {It: "Origine: continuità operativa di {subject}.", En: "Source: business continuity of {subject}."},

		"continuity.finding.title":  {It: "Rilievo del test {test}", En: "Finding of test {test}"},
		"continuity.finding.body":   {It: "Origine: test di resilienza {test} su {subject}.\nRilievo: {finding}", En: "Source: resilience test {test} on {subject}.\nFinding: {finding}"},
		"continuity.finding.reason": {It: "Rilievo «{finding}»: ", En: "Finding “{finding}”: "},

		"continuity.outcome.passed":               {It: "superato", En: "passed"},
		"continuity.outcome.passed_with_findings": {It: "superato con rilievi", En: "passed with findings"},
		"continuity.outcome.failed":               {It: "non superato", En: "failed"},
		"continuity.evidence.summary": {It: "Test di resilienza {type} su {subject}: {outcome}. RTO misurato {rto}, RPO misurato {rpo}.",
			En: "Resilience test {type} on {subject}: {outcome}. Measured RTO {rto}, measured RPO {rpo}."},
		"continuity.evidence.no_grc": {It: "Conformità non collegata: nessuna evidenza registrata.", En: "Compliance not connected: no evidence recorded."},
		"continuity.evidence.failed": {It: "Evidenza non registrata: {reason}", En: "Evidence not recorded: {reason}"},
	})
}
