package rbac

// Permessi e ruoli della conformità (piano, passo 20; replica/grc.md). Stanno in un file a
// parte e si aggiungono all'avvio, così gli elenchi comuni non cambiano.
const (
	// GRCAdmin: cataloghi normativi, controlli, applicabilità, validazione delle evidenze.
	GRCAdmin = "grc.admin"
	// GRCRead: cruscotto della conformità, cataloghi, controlli ed evidenze in sola lettura.
	GRCRead = "grc.read"
	// GRCCollect: invio di evidenze automatiche (pipeline, collettori), per i token API.
	GRCCollect = "grc.collect"
	// GRCAudit: spazio dell'auditor: incarichi, campioni, richieste di evidenza, commenti e rilievi.
	GRCAudit = "grc.audit"
)

func init() {
	Known = append(Known, GRCAdmin, GRCRead, GRCCollect, GRCAudit)
	permissionInfo[GRCAudit] = PermissionInfo{Name: "Fare audit", Area: "Conformità",
		Description: "Apre incarichi di audit, estrae campioni di evidenze, chiede evidenze ai responsabili, commenta e apre e chiude i rilievi. Il resto resta in sola lettura."}
	permissionInfo[GRCAdmin] = PermissionInfo{Name: "Gestire la conformità", Area: "Conformità",
		Description: "Importa e pubblica i cataloghi normativi, crea i controlli e li collega ai requisiti, decide l'applicabilità e valida le evidenze."}
	permissionInfo[GRCRead] = PermissionInfo{Name: "Vedere la conformità", Area: "Conformità",
		Description: "Consulta il cruscotto della conformità, i cataloghi, i controlli e le evidenze, ed esporta il pacchetto per l'audit."}
	permissionInfo[GRCCollect] = PermissionInfo{Name: "Inviare evidenze automatiche", Area: "Conformità",
		Description: "Registra evidenze automatiche dalle pipeline e dai collettori (rilasci, scansioni), per i token API."}
	SystemRoles = append(SystemRoles,
		SystemRole{"compliance_officer", "Compliance officer", []string{ElementRead, ElementWrite, CommentWrite, GRCRead, GRCAdmin}},
		SystemRole{"evidence_collector", "Raccolta evidenze", []string{GRCCollect}},
	)
	// L'auditor vede anche la conformità, sempre in sola lettura.
	for i := range SystemRoles {
		if SystemRoles[i].Key == "auditor" {
			SystemRoles[i].Permissions = append(SystemRoles[i].Permissions, GRCRead, GRCAudit)
		}
	}
}
