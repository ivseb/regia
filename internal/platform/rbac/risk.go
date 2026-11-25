package rbac

// Permessi e ruoli di sistema del modulo dei rischi, delle terze parti e degli incidenti
// (piano, passo 20). Stanno in un file a parte perché si aggiungono a Known, ai ruoli di
// sistema e al catalogo senza toccare le liste degli altri moduli.
const (
	// RiskAdmin: registro dei rischi, matrici, fornitori ICT e contratti, registro delle
	// informazioni, configurazione del modulo.
	RiskAdmin = "risk.admin"
	// IncidentManage: classificazione, timeline, notifiche normative e postmortem degli incidenti.
	IncidentManage = "incident.manage"
)

func init() {
	Known = append(Known, RiskAdmin, IncidentManage)
	SystemRoles = append(SystemRoles,
		SystemRole{"risk_manager", "Risk manager", []string{ElementRead, ElementWrite, CommentWrite, RiskAdmin}},
		SystemRole{"incident_manager", "Incident manager", []string{ElementRead, ElementWrite, CommentWrite, IncidentManage}},
	)
	permissionInfo[RiskAdmin] = PermissionInfo{Name: "Gestire rischi e fornitori ICT", Area: "Governo",
		Description: "Valuta e tratta i rischi ICT, gestisce matrici, fornitori, contratti e il registro delle informazioni."}
	permissionInfo[IncidentManage] = PermissionInfo{Name: "Gestire gli incidenti", Area: "Governo",
		Description: "Classifica gli incidenti, ne aggiorna la timeline, prepara e registra le notifiche normative e il postmortem."}
}
