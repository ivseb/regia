package problems

import "regia/internal/platform/i18n"

// Testi proposti dal server per il lavoro creato dal problema: titolo e descrizione dell'elemento,
// motivi precompilati di eccezione, deroga e override. Si scrivono nella lingua di chi agisce.
func init() {
	i18n.Register(map[string]i18n.Msg{
		"problems.finding.title": {It: "Correggere {cve} su {app}", En: "Fix {cve} on {app}"},
		"problems.finding.body": {
			It: "Origine: vulnerabilità {cve} ({severity}) sull'applicazione {app}.\nPacchetto: {package} {version}. Corretta in: {fixed}.\nScadenza SLA: {due}.",
			En: "Source: vulnerability {cve} ({severity}) on application {app}.\nPackage: {package} {version}. Fixed in: {fixed}.\nSLA due: {due}."},
		"problems.finding.reason": {
			It: "Non si corregge subito {cve} su {app}: ",
			En: "{cve} on {app} can't be fixed right away: "},
		"problems.gate.title": {It: "Sbloccare il rilascio di {app} in {environment}", En: "Unblock the release of {app} in {environment}"},
		"problems.gate.body": {
			It: "Origine: il gate di rilascio ha bloccato {app} in {environment} (versione {version}).\nMotivi:\n{reasons}",
			En: "Source: the release gate blocked {app} in {environment} (version {version}).\nReasons:\n{reasons}"},
		"problems.gate.reason": {
			It: "Si rilascia lo stesso nonostante: {reasons}. Motivo: ",
			En: "Releasing anyway despite: {reasons}. Reason: "},
		"problems.requirement.title": {It: "Soddisfare il requisito {key}: {title}", En: "Meet requirement {key}: {title}"},
		"problems.requirement.body": {
			It: "Origine: il requisito {key} «{title}» non è ancora soddisfatto.",
			En: "Source: requirement {key} “{title}” is not met yet."},
		"problems.requirement.reason": {
			It: "Il requisito {key} non si soddisfa nei tempi: ",
			En: "Requirement {key} can't be met in time: "},
		"problems.adherence.title": {It: "Portare la pipeline di {app} alla politica", En: "Bring the {app} pipeline up to policy"},
		"problems.adherence.body": {
			It: "Origine: la pipeline di {app} non rispetta la politica.\nControlli mancanti: {controls}.",
			En: "Source: the {app} pipeline doesn't follow the policy.\nMissing controls: {controls}."},
		"problems.none":      {It: "nessuno", En: "none"},
		"problems.not_given": {It: "non indicata", En: "not given"},
	})
}
