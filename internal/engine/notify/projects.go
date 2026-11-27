package notify

import "regia/internal/platform/i18n"

// KindProject: progetti, passo 4 (replica/progetti.md). Eventi nel payload:
//   - conflict: due progetti pianificano lavoro sulla stessa applicazione nello stesso periodo;
//     va al team owner dell'applicazione, sull'applicazione (che il team legge). Il payload porta
//     le chiavi dei progetti, non i titoli, e la fine del periodo sovrapposto;
//   - claim_submitted: il fornitore ha dichiarato un SAL da accettare, ai PM del progetto;
//   - deliverable_delivered: il fornitore ha consegnato un deliverable, ai PM del progetto;
//   - dependency_late (passo 6): la milestone di un altro progetto da cui il progetto dipende è
//     prevista oltre la data che serve, ai PM del progetto che dipende. Il payload porta le chiavi
//     dei progetti e della milestone, la data che serve e quella prevista.
//
// Sta in un file a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli.
const KindProject = "project"

func init() {
	Kinds = append(Kinds, KindProject)
	i18n.Register(map[string]i18n.Msg{
		"notify.project.conflict": {It: "{project} e {other} modificano {application} nello stesso periodo, fino al {until}",
			En: "{project} and {other} both change {application} in the same period, until {until}"},
		"notify.project.claim_submitted":       {It: "{actor} ha dichiarato il SAL {number} di {work_package}: è da accettare", En: "{actor} submitted progress claim {number} for {work_package}: it needs your decision"},
		"notify.project.deliverable_delivered": {It: "{actor} ha consegnato {key}: è da accettare", En: "{actor} delivered {key}: it needs your decision"},
		"notify.project.dependency_late": {It: "{milestone} di {other} è prevista il {date}, oltre il {need} che serve a {project}",
			En: "{milestone} of {other} is now due on {date}, after {need} when {project} needs it"},
		"notify.project": {It: "Novità sul progetto: {key}", En: "Project update: {key}"},
	})
}
