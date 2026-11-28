package rbac

// Permessi e ruolo di sistema della continuità operativa (docs/sviluppo/continuita.md). In un
// file a parte, come quelli dei rischi: si aggiungono a Known, ai ruoli e al catalogo.
const (
	// ContinuityWrite: BIA in bozza, test di resilienza, collegamento dei piani.
	ContinuityWrite = "continuity.write"
	// ContinuityApprove: approvazione della BIA (congela la versione in vigore).
	ContinuityApprove = "continuity.approve"
	// ContinuityAdmin: configurazione (scale, orizzonti, tipi di test) e preparazione del modulo.
	ContinuityAdmin = "continuity.admin"
)

func init() {
	Known = append(Known, ContinuityWrite, ContinuityApprove, ContinuityAdmin)
	SystemRoles = append(SystemRoles,
		SystemRole{"continuity_manager", "Responsabile della continuità", []string{ElementRead, ElementWrite, CommentWrite, ContinuityWrite, ContinuityApprove, ContinuityAdmin}},
	)
	permissionInfo[ContinuityWrite] = PermissionInfo{Name: "Gestire BIA e test di continuità", Area: "Governo",
		Description: "Compila le BIA in bozza, pianifica e registra i test di resilienza, collega i piani di continuità."}
	permissionInfo[ContinuityApprove] = PermissionInfo{Name: "Approvare le BIA", Area: "Governo",
		Description: "Approva una BIA: MTPD, RTO e RPO richiesti diventano quelli in vigore."}
	permissionInfo[ContinuityAdmin] = PermissionInfo{Name: "Configurare la continuità", Area: "Configurazione",
		Description: "Scale d'impatto, orizzonti, tipi di test e soglie della continuità operativa."}
}
