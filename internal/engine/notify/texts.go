package notify

import "regia/internal/platform/i18n"

// Testi delle notifiche e delle email, nelle due lingue (replica/lingua.md, passo 3). Si scrivono
// nella lingua di chi le riceve. Stile inglese: GLOSSARIO.md (item, request, review team…).
// Segnaposto {nome}: actor, key, e i campi del payload con lo stesso nome.
func init() {
	i18n.Register(map[string]i18n.Msg{
		// Email
		"mail.subject.one":    {It: "[Regia] {text}", En: "[Regia] {text}"},
		"mail.subject.many":   {It: "[Regia] {count} nuove notifiche", En: "[Regia] {count} new notifications", OneIt: "[Regia] 1 nuova notifica", OneEn: "[Regia] 1 new notification"},
		"mail.intro":          {It: "Novità su Regia:", En: "What's new in Regia:"},
		"mail.all":            {It: "Tutte le notifiche: {url}", En: "All notifications: {url}"},
		"mail.prefs":          {It: "Puoi scegliere quali email ricevere nelle preferenze delle notifiche.", En: "You can choose which emails you get in your notification preferences."},
		"mail.invite.subject": {It: "[Regia] Invito in {tenant}", En: "[Regia] Invitation to {tenant}"},
		"mail.invite.body": {
			It: "Sei stato invitato a usare Regia in {tenant}.\n\nPer accettare apri questo link entro il {expires}:\n{url}\n\nSe non ti aspettavi questo invito, ignora il messaggio.\n",
			En: "You have been invited to use Regia in {tenant}.\n\nTo accept, open this link by {expires}:\n{url}\n\nIf you weren't expecting this invitation, ignore this message.\n"},
		"mail.password.subject": {It: "[Regia] Scegli la tua password", En: "[Regia] Choose your password"},
		"mail.password.body": {
			It: "Per scegliere la password di Regia ({tenant}) apri questo link entro il {expires}:\n{url}\n\nSe non hai chiesto nulla, ignora il messaggio.\n",
			En: "To choose your Regia password ({tenant}), open this link by {expires}:\n{url}\n\nIf you didn't ask for this, ignore this message.\n"},

		// Parole di servizio
		"notify.system":       {It: "Sistema", En: "System"},
		"notify.an_item":      {It: "un elemento", En: "an item"},
		"notify.an_app":       {It: "un'applicazione", En: "an application"},
		"notify.due_suffix":   {It: " entro il {due}", En: " by {due}"},
		"notify.status.green": {It: "verde", En: "green"},
		"notify.status.amber": {It: "giallo", En: "amber"},
		"notify.status.red":   {It: "rosso", En: "red"},

		// Menzioni, commenti, ruoli, fasi
		"notify.mention.inline":          {It: "{actor} ti ha menzionato in una discussione su una frase di {key}", En: "{actor} mentioned you in a discussion on a sentence of {key}"},
		"notify.mention":                 {It: "{actor} ti ha menzionato in {key}", En: "{actor} mentioned you in {key}"},
		"notify.comment.inline_comment":  {It: "{actor} ha commentato una frase di {key}", En: "{actor} commented on a sentence of {key}"},
		"notify.comment.inline_reply":    {It: "{actor} ha risposto in una discussione su {key}", En: "{actor} replied in a discussion on {key}"},
		"notify.comment.inline_resolved": {It: "{actor} ha risolto la tua discussione su {key}", En: "{actor} resolved your discussion on {key}"},
		"notify.comment.inline_reopened": {It: "{actor} ha riaperto la tua discussione su {key}", En: "{actor} reopened your discussion on {key}"},
		"notify.comment":                 {It: "{actor} ha commentato {key}", En: "{actor} commented on {key}"},
		"notify.role.group":              {It: "{actor} ha assegnato al tuo gruppo il ruolo {role} su {key}", En: "{actor} assigned the {role} role on {key} to your group"},
		"notify.role":                    {It: "{actor} ti ha assegnato il ruolo {role} su {key}", En: "{actor} assigned you the {role} role on {key}"},
		"notify.phase":                   {It: "{key} è passata da {from} a {to}", En: "{key} moved from {from} to {to}"},
		"notify.approval":                {It: "Approvazione in attesa della tua decisione: {key} (fase {stage})", En: "Approval waiting for your decision: {key} (phase {stage})"},
		"notify.referent":                {It: "Sei referente del team sull'impatto {key}", En: "You are the team lead on impact {key}"},

		// Valutazioni
		"notify.review.opened":        {It: "Nuova valutazione per il team {team}: {key}", En: "New review for the {team} team: {key}"},
		"notify.review.section":       {It: "{actor} ti ha assegnato la sezione «{section}» di {key}", En: "{actor} assigned you the “{section}” section of {key}"},
		"notify.review.submitted":     {It: "{key} è stata inviata al team {team}", En: "{key} was sent to the {team} team"},
		"notify.review.clarification": {It: "Il team {team} chiede chiarimenti su {key}", En: "The {team} team asks for clarification on {key}"},
		"notify.review.decided":       {It: "Esito di {key}: {outcome}", En: "Outcome of {key}: {outcome}"},
		"notify.review":               {It: "Valutazione {key}", En: "Review {key}"},
		"notify.sla.reminder":         {It: "{key} scade il {due} (team {team})", En: "{key} is due on {due} ({team} team)"},
		"notify.sla.overdue":          {It: "{key} è in ritardo: scadeva il {due}", En: "{key} is overdue: it was due on {due}"},
		"notify.sla.escalation":       {It: "Escalation: {key} è in ritardo di {late_days} giorni lavorativi (team {team})", En: "Escalation: {key} is {late_days} business days late ({team} team)"},
		"notify.sla":                  {It: "Scadenza di {key}", En: "Deadline of {key}"},

		// Status report
		"notify.report.due":       {It: "Status report di {key} da scrivere entro il {due}", En: "Status report of {key} to write by {due}"},
		"notify.report.overdue":   {It: "Status report di {key} in ritardo: era atteso il {due}", En: "Status report of {key} is overdue: it was expected on {due}"},
		"notify.report.submitted": {It: "Nuovo status report di {key} (stato {status})", En: "New status report of {key} (status {status})"},
		"notify.report":           {It: "Status report di {key}", En: "Status report of {key}"},

		// Requisiti e deroghe
		"notify.requirement.assigned":           {It: "Nuovo requisito da realizzare: {key}", En: "New requirement to implement: {key}"},
		"notify.requirement.to_verify":          {It: "{key} è pronto per la verifica ({application})", En: "{key} is ready for verification ({application})"},
		"notify.requirement.verified":           {It: "{key} è stato verificato ({impact})", En: "{key} was verified ({impact})"},
		"notify.requirement.rejected":           {It: "Verifica non superata: {key} ({impact})", En: "Verification failed: {key} ({impact})"},
		"notify.requirement.exception_approved": {It: "Deroga {exception} approvata per {requirement}", En: "Waiver {exception} approved for {requirement}"},
		"notify.requirement.exception_rejected": {It: "Deroga {exception} respinta per {requirement}", En: "Waiver {exception} rejected for {requirement}"},
		"notify.requirement.exception_expiring": {It: "La deroga {exception} su {requirement} scade il {expires_on}", En: "Waiver {exception} on {requirement} expires on {expires_on}"},
		"notify.requirement.exception_expired":  {It: "Deroga scaduta: {requirement} è di nuovo da realizzare", En: "Waiver expired: {requirement} is to be implemented again"},
		"notify.requirement.exception_revoked":  {It: "Deroga revocata: {requirement} è di nuovo da realizzare", En: "Waiver revoked: {requirement} is to be implemented again"},
		"notify.requirement":                    {It: "Requisito {key}", En: "Requirement {key}"},

		// Knowledge
		"notify.document.review_due":              {It: "Revisione periodica di {key} entro il {due}", En: "Periodic review of {key} by {due}"},
		"notify.document.review_overdue":          {It: "Revisione periodica di {key} in ritardo: era prevista il {due}", En: "Periodic review of {key} is overdue: it was scheduled for {due}"},
		"notify.document.approved":                {It: "{key} è stato approvato (versione {version})", En: "{key} was approved (version {version})"},
		"notify.document.rejected":                {It: "{key} è tornato in bozza dopo la revisione", En: "{key} went back to draft after the review"},
		"notify.document.reading_requested":       {It: "Devi leggere e confermare {key} (versione {version}){due}", En: "You need to read and confirm {key} (version {version}){due}"},
		"notify.document.reading_reminder":        {It: "Promemoria: conferma di aver letto {key} (versione {version}){due}", En: "Reminder: confirm you have read {key} (version {version}){due}"},
		"notify.document.reading_overdue":         {It: "Lettura in ritardo: conferma {key} (versione {version}), era prevista il {due}", En: "Reading overdue: confirm {key} (version {version}), it was due on {due}"},
		"notify.document.reading_reopened":        {It: "Nuova versione di {key}: conferma di averla letta (versione {version}){due}", En: "New version of {key}: confirm you have read it (version {version}){due}"},
		"notify.document.reading_overdue_summary": {It: "Campagna di lettura in ritardo su {key}: mancano {missing} conferme", En: "Reading campaign overdue on {key}: {missing} confirmations missing", OneIt: "Campagna di lettura in ritardo su {key}: manca 1 conferma", OneEn: "Reading campaign overdue on {key}: 1 confirmation missing"},
		"notify.document":                         {It: "Documento {key}", En: "Document {key}"},

		// Rischi, fornitori, incidenti
		"notify.risk.risk_accepted":           {It: "Accettazione del rischio {key} approvata fino al {until}", En: "Acceptance of risk {key} approved until {until}"},
		"notify.risk.risk_rejected":           {It: "Accettazione del rischio {key} non approvata", En: "Acceptance of risk {key} not approved"},
		"notify.risk.risk_acceptance_expired": {It: "Accettazione del rischio {key} scaduta: va rivalutato", En: "Acceptance of risk {key} expired: it needs reassessing"},
		"notify.risk.risk_review_due":         {It: "Revisione del rischio {key} entro il {due}", En: "Review of risk {key} by {due}"},
		"notify.risk.risk_treatment_due":      {It: "Trattamento del rischio {key} in scadenza il {due}", En: "Treatment of risk {key} due on {due}"},
		"notify.risk.contract_expiring":       {It: "Il contratto {contract} con {provider} scade il {end_date}", En: "The {contract} contract with {provider} expires on {end_date}"},
		"notify.risk.incident_classified":     {It: "Incidente {key} {outcome} secondo {scheme}: notifiche da preparare", En: "Incident {key} {outcome} under {scheme}: notifications to prepare"},
		"notify.risk.notification_due":        {It: "Notifica {stage} ({scheme}) di {key} da inviare entro {due}", En: "{stage} notification ({scheme}) of {key} to send by {due}"},
		"notify.risk.notification_overdue":    {It: "Notifica {stage} ({scheme}) di {key} scaduta: era attesa entro {due}", En: "{stage} notification ({scheme}) of {key} is overdue: it was expected by {due}"},
		"notify.risk":                         {It: "Rischi e incidenti: {key}", En: "Risks and incidents: {key}"},

		// Vulnerabilità
		"notify.vuln.count":                   {It: "{count} vulnerabilità", En: "{count} vulnerabilities", OneIt: "1 vulnerabilità", OneEn: "1 vulnerability"},
		"notify.vuln.vuln_due_soon":           {It: "{n} su {key} scadono entro il {due}", En: "{n} on {key} are due by {due}"},
		"notify.vuln.vuln_overdue":            {It: "{n} su {key} scadute ({step}), la più vecchia da {days} giorni", En: "{n} on {key} overdue ({step}), the oldest for {days} days"},
		"notify.vuln.vuln_exception_approved": {It: "Eccezione {exception} su {key} approvata fino al {until}", En: "Exception {exception} on {key} approved until {until}"},
		"notify.vuln.vuln_exception_rejected": {It: "Eccezione {exception} su {key} non approvata", En: "Exception {exception} on {key} not approved"},
		"notify.vuln.vuln_exception_expiring": {It: "L'eccezione {exception} su {key} scade il {until}", En: "Exception {exception} on {key} expires on {until}"},
		"notify.vuln.vuln_exception_expired":  {It: "Eccezione {exception} su {key} scaduta: le vulnerabilità tornano da correggere", En: "Exception {exception} on {key} expired: the vulnerabilities are to be fixed again"},
		"notify.vuln":                         {It: "Vulnerabilità su {key}", En: "Vulnerabilities on {key}"},
		"notify.other":                        {It: "{kind} su {key}", En: "{kind} on {key}"},
	})
}
