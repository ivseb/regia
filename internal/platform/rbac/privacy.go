package rbac

// Permessi e ruolo della Vista DPO (ondata 3, replica/usabilita/governo.md, C8). Stanno in un file
// a parte e si aggiungono all'avvio, così gli elenchi comuni non cambiano.
const (
	// PrivacyRead: registro dei trattamenti, DPIA, violazioni dei dati e flussi, in sola lettura.
	PrivacyRead = "privacy.read"
	// PrivacyWrite: tiene il registro dei trattamenti, le DPIA e il segno di violazione sugli incidenti.
	PrivacyWrite = "privacy.write"
)

func init() {
	Known = append(Known, PrivacyRead, PrivacyWrite)
	permissionInfo[PrivacyRead] = PermissionInfo{Name: "Vedere la privacy", Area: "Governo",
		Description: "Consulta il registro dei trattamenti, le DPIA, le violazioni dei dati personali e i flussi di dati."}
	permissionInfo[PrivacyWrite] = PermissionInfo{Name: "Gestire la privacy", Area: "Governo",
		Description: "Tiene il registro dei trattamenti e le DPIA, segna gli incidenti che toccano dati personali e registra la notifica al Garante."}
	SystemRoles = append(SystemRoles,
		SystemRole{"dpo", "Responsabile della protezione dei dati (DPO)", []string{ElementRead, CommentWrite, PrivacyRead, PrivacyWrite}},
	)
}
