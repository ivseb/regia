package i18n

// Nomi di sistema della continuità operativa (internal/modules/continuity): in un file a parte,
// così chi lavora su altri moduli non tocca le stesse righe di names_en.go.
func init() {
	for it, en := range map[string]string{
		// Tipi, ruolo, relazione, processi di partenza.
		"Analisi d'impatto (BIA)":              "Business impact analysis (BIA)",
		"Test di resilienza":                   "Resilience test",
		"Responsabile della continuità":        "Business continuity manager",
		"è il piano di continuità di":          "is the continuity plan of",
		"ha come piano di continuità":          "has as continuity plan",
		"Ciclo di vita della BIA":              "BIA lifecycle",
		"Ciclo di vita del test di resilienza": "Resilience test lifecycle",
		"Gestire BIA e test di continuità":     "Manage BIAs and continuity tests",
		"Approvare le BIA":                     "Approve BIAs",
		"Configurare la continuità":            "Configure business continuity",
		// Pacchetto di partenza della configurazione.
		"Trascurabile":         "Negligible",
		"Basso":                "Low",
		"Medio":                "Medium",
		"Alto":                 "High",
		"Grave":                "Severe",
		"Economico":            "Financial",
		"Normativo":            "Regulatory",
		"Reputazionale":        "Reputational",
		"Clienti":              "Customers",
		"1 ora":                "1 hour",
		"4 ore":                "4 hours",
		"1 giorno":             "1 day",
		"3 giorni":             "3 days",
		"1 settimana":          "1 week",
		"Tabletop":             "Tabletop",
		"Failover":             "Failover",
		"Ripristino da backup": "Restore from backup",
		"Scenario":             "Scenario",
		"Esercitazione a tavolino sullo scenario, senza toccare i sistemi.": "Discussion-based exercise on the scenario, without touching the systems.",
		"Passaggio al sito o al sistema secondario e ritorno.":              "Switch to the secondary site or system and back.",
		"Ripristino dei dati da backup, con misura di RTO e RPO.":           "Data restore from backup, measuring RTO and RPO.",
		"Simulazione completa di uno scenario di interruzione.":             "Full simulation of a disruption scenario.",
		// Fatti.
		"RTO richiesto dalle BIA (minuti)":          "RTO required by the BIAs (minutes)",
		"RPO richiesto dalle BIA (minuti)":          "RPO required by the BIAs (minutes)",
		"RTO o RPO incoerente con BIA o dipendenze": "RTO or RPO inconsistent with BIA or dependencies",
		"Incoerenze della continuità":               "Continuity inconsistencies",
		"Giorni dall'ultimo test di resilienza":     "Days since the latest resilience test",
		"Piano di continuità approvato":             "Approved continuity plan",
		"Funzione critica secondo la BIA":           "Critical function according to the BIA",
		"Funzione critica o importante (BIA)":       "Critical or important function (BIA)",
		"MTPD della BIA (minuti)":                   "BIA MTPD (minutes)",
		"RTO richiesto dalla BIA (minuti)":          "RTO required by the BIA (minutes)",
		"Giorni dall'approvazione della BIA":        "Days since the BIA was approved",
		"Funzione critica o importante":             "Critical or important function",
		"MTPD (minuti)":                             "MTPD (minutes)",
		"RTO (minuti)":                              "RTO (minutes)",
		"RPO (minuti)":                              "RPO (minutes)",
		"Tipo di test":                              "Test type",
		"Esito del test":                            "Test outcome",
		"RTO misurato (minuti)":                     "Measured RTO (minutes)",
		"RPO misurato (minuti)":                     "Measured RPO (minutes)",
	} {
		if _, ok := namesEn[it]; !ok {
			namesEn[it] = en
		}
	}
}
