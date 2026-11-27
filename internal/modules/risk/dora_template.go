package risk

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Modello della segnalazione di un incidente ICT grave secondo DORA (ondata 5 dell'AI).
//
// Fonti ufficiali, verificate su EUR-Lex il 2026-11-27:
//   - Regolamento di esecuzione (UE) 2025/302 della Commissione (GU L, 2025/302, 20.2.2025), norme
//     tecniche di attuazione su moduli, modelli e procedure: allegato I (il modello, con i numeri
//     dei campi) e allegato II (glossario e istruzioni: obbligatorietà per tappa e tipo di dato);
//   - Regolamento delegato (UE) 2025/301 della Commissione (GU L, 2025/301, 20.2.2025), norme
//     tecniche di regolamentazione: art. 1 (informazioni generali), art. 2 (notifica iniziale),
//     art. 3 (relazione intermedia), art. 4 (relazione finale), art. 5 (termini: 4 ore dalla
//     classificazione e non oltre 24 dalla conoscenza; 72 ore dall'invio della notifica iniziale;
//     un mese dall'ultima relazione intermedia).
//
// I numeri e i nomi inglesi sono quelli dell'allegato I; i nomi italiani e le indicazioni «da
// dove si ricava» sono nostri. Con la relazione intermedia e la finale il modello si ripresenta
// intero e si aggiornano anche i campi delle tappe prima (art. 1, par. 4, del 2025/302).

// DoraField è un campo del modello.
type DoraField struct {
	Num     string `json:"num"`
	Name    string `json:"name"`
	NameEN  string `json:"name_en"`
	Section int    `json:"section"` // 1 generali, 2 iniziale, 3 intermedia, 4 finale
	// When: obbligatorio solo a una condizione (dal glossario dell'allegato II); vuoto = sempre.
	When string `json:"when,omitempty"`
	// Hint: da dove si ricava, quando Regia non lo sa.
	Hint string `json:"hint,omitempty"`
	// Narrative: testo che l'assistente può scrivere dai dati dell'incidente.
	Narrative bool `json:"narrative,omitempty"`
}

// DoraFields è il modello dell'allegato I del Reg. di esecuzione (UE) 2025/302.
var DoraFields = []DoraField{
	{Num: "1.1", Section: 1, NameEN: "Type of submission", Name: "Tipo di invio"},
	{Num: "1.2", Section: 1, NameEN: "Name of the entity submitting the report", Name: "Nome dell'entità che invia la segnalazione", Hint: "ragione sociale completa di chi invia (anagrafica societaria)"},
	{Num: "1.3", Section: 1, NameEN: "Identification code of the entity submitting the report", Name: "Codice identificativo dell'entità che invia", Hint: "LEI di 20 caratteri (ISO 17442-1:2020); Regia non lo conserva"},
	{Num: "1.4", Section: 1, NameEN: "Type of financial entity affected", Name: "Tipo di entità finanziaria colpita", Hint: "categoria dell'art. 2, par. 1, del Reg. (UE) 2022/2554 (ente creditizio, istituto di pagamento, impresa di assicurazione…)"},
	{Num: "1.5", Section: 1, NameEN: "Name of the financial entity affected", Name: "Nome dell'entità finanziaria colpita", When: "se diversa da chi invia o in una segnalazione aggregata", Hint: "solo se diversa da chi invia"},
	{Num: "1.6", Section: 1, NameEN: "LEI code of the financial entity affected", Name: "LEI dell'entità finanziaria colpita", When: "se diversa da chi invia o in una segnalazione aggregata", Hint: "solo se diversa da chi invia"},
	{Num: "1.7", Section: 1, NameEN: "Primary contact person name", Name: "Nome del referente principale", Hint: "chi segue i rapporti con l'autorità sull'incidente"},
	{Num: "1.8", Section: 1, NameEN: "Primary contact person email", Name: "Email del referente principale"},
	{Num: "1.9", Section: 1, NameEN: "Primary contact person telephone", Name: "Telefono del referente principale", Hint: "con il prefisso internazionale (+39…); Regia non lo conserva"},
	{Num: "1.10", Section: 1, NameEN: "Second contact person name", Name: "Nome del secondo referente", Hint: "una seconda persona o il team responsabile"},
	{Num: "1.11", Section: 1, NameEN: "Second contact person email", Name: "Email del secondo referente", Hint: "anche una casella del team"},
	{Num: "1.12", Section: 1, NameEN: "Second contact person telephone", Name: "Telefono del secondo referente", Hint: "con il prefisso internazionale"},
	{Num: "1.13", Section: 1, NameEN: "Name of the ultimate parent undertaking", Name: "Nome della capogruppo", When: "se l'entità appartiene a un gruppo", Hint: "anagrafica del gruppo"},
	{Num: "1.14", Section: 1, NameEN: "LEI code of the ultimate parent undertaking", Name: "LEI della capogruppo", When: "se l'entità appartiene a un gruppo", Hint: "anagrafica del gruppo"},
	{Num: "1.15", Section: 1, NameEN: "Reporting currency", Name: "Valuta della segnalazione"},

	{Num: "2.1", Section: 2, NameEN: "Incident reference code assigned by the financial entity", Name: "Codice dell'incidente assegnato dall'entità"},
	{Num: "2.2", Section: 2, NameEN: "Date and time of detection of the major ICT-related incident", Name: "Data e ora di rilevazione"},
	{Num: "2.3", Section: 2, NameEN: "Date and time of classification of the ICT-related incident as major", Name: "Data e ora della classificazione come grave", Hint: "si registra classificando l'incidente in Regia"},
	{Num: "2.4", Section: 2, NameEN: "Description of the major ICT-related incident", Name: "Descrizione dell'incidente", Narrative: true, Hint: "descrizione dell'incidente, timeline e applicazioni impattate"},
	{Num: "2.5", Section: 2, NameEN: "Classification criteria that triggered the incident report", Name: "Criteri di classificazione che hanno fatto scattare la segnalazione", Hint: "classificazione dell'incidente con lo schema DORA"},
	{Num: "2.6", Section: 2, NameEN: "Materiality thresholds for the classification criterion 'Geographical spread'", Name: "Stati membri colpiti (diffusione geografica)", When: "se è superata la soglia della diffusione geografica", Hint: "codici ISO 3166 alfa-2 degli Stati SEE colpiti; Regia conta solo quanti sono"},
	{Num: "2.7", Section: 2, NameEN: "Discovery of the major ICT-related incident", Name: "Come è stato scoperto", Narrative: true, Hint: "una scelta: sicurezza IT, personale, audit interno o esterno, clienti, controparti, fornitore terzo, attaccante, sistemi di monitoraggio, autorità, altro"},
	{Num: "2.8", Section: 2, NameEN: "Indication whether the major ICT-related incident originates from a third-party provider or another financial entity", Name: "Origine da un fornitore terzo o da un'altra entità finanziaria", When: "se l'incidente nasce da un terzo", Hint: "causa dell'incidente («fornitore terzo») e fornitore ICT collegato"},
	{Num: "2.9", Section: 2, NameEN: "Activation of business continuity plan, if activated", Name: "Attivazione del piano di continuità operativa", Narrative: true, Hint: "sì o no: decisioni nella timeline dell'incidente"},
	{Num: "2.10", Section: 2, NameEN: "Other relevant information", Name: "Altre informazioni rilevanti", When: "se ci sono informazioni non coperte dal modello o una riclassificazione", Narrative: true},

	{Num: "3.1", Section: 3, NameEN: "Incident reference code provided by the competent authority", Name: "Codice dell'incidente assegnato dall'autorità", When: "se applicabile", Hint: "ricevuta della notifica iniziale (protocollo registrato all'invio)"},
	{Num: "3.2", Section: 3, NameEN: "Date and time of occurrence of the major ICT-related incident", Name: "Data e ora in cui si è verificato", Hint: "se diversa dalla rilevazione: log dei sistemi coinvolti"},
	{Num: "3.3", Section: 3, NameEN: "Date and time when services, activities or operations have been recovered", Name: "Data e ora del ripristino dei servizi", When: "se c'è stato un fermo del servizio (3.16)", Hint: "momento della risoluzione o del contenimento registrato nell'incidente"},
	{Num: "3.4", Section: 3, NameEN: "Number of clients affected", Name: "Numero di clienti coinvolti", Hint: "criteri d'impatto dell'incidente (anche stimati)"},
	{Num: "3.5", Section: 3, NameEN: "Percentage of clients affected", Name: "Percentuale di clienti coinvolti", Hint: "criteri d'impatto dell'incidente (anche stimati)"},
	{Num: "3.6", Section: 3, NameEN: "Number of financial counterparts affected", Name: "Numero di controparti finanziarie coinvolte", Hint: "Regia non lo registra: area che gestisce le controparti"},
	{Num: "3.7", Section: 3, NameEN: "Percentage of financial counterparts affected", Name: "Percentuale di controparti finanziarie coinvolte", Hint: "Regia non lo registra"},
	{Num: "3.8", Section: 3, NameEN: "Impact on relevant clients or financial counterparts", Name: "Impatto su clienti o controparti rilevanti", When: "se è superata la soglia della rilevanza", Hint: "sì o no"},
	{Num: "3.9", Section: 3, NameEN: "Number of affected transactions", Name: "Numero di operazioni coinvolte", When: "se sono coinvolte operazioni", Hint: "sistemi delle operazioni (anche stimato)"},
	{Num: "3.10", Section: 3, NameEN: "Percentage of affected transactions", Name: "Percentuale di operazioni coinvolte", When: "se sono coinvolte operazioni", Hint: "rispetto alla media giornaliera del servizio"},
	{Num: "3.11", Section: 3, NameEN: "Value of affected transactions", Name: "Valore delle operazioni coinvolte", When: "se sono coinvolte operazioni", Hint: "in migliaia di unità della valuta"},
	{Num: "3.12", Section: 3, NameEN: "Information on whether the numbers are actual or estimates, or whether there has not been any impact", Name: "Numeri effettivi o stimati (clienti, controparti, operazioni)", Hint: "dire per clienti, controparti e operazioni se i numeri sono effettivi, stimati o senza impatto"},
	{Num: "3.13", Section: 3, NameEN: "Reputational impact", Name: "Impatto reputazionale", When: "se il criterio reputazionale è soddisfatto", Hint: "media, reclami ripetuti, requisiti normativi non rispettati, perdita di clienti"},
	{Num: "3.14", Section: 3, NameEN: "Contextual information about the reputational impact", Name: "Contesto dell'impatto reputazionale", When: "se il criterio reputazionale è soddisfatto", Narrative: true, Hint: "tipo e portata dei media, reclami, informazioni date al pubblico"},
	{Num: "3.15", Section: 3, NameEN: "Duration of the major ICT-related incident", Name: "Durata dell'incidente (GG:OO:MM)"},
	{Num: "3.16", Section: 3, NameEN: "Service downtime", Name: "Fermo del servizio (GG:OO:MM)", When: "se c'è stato un fermo del servizio", Hint: "criteri d'impatto dell'incidente"},
	{Num: "3.17", Section: 3, NameEN: "Information on whether the numbers for duration and service downtime are actual or estimates", Name: "Durata e fermo effettivi o stimati", When: "se il criterio della durata è soddisfatto", Hint: "effettivi, stimati, effettivi e stimati, nessuna informazione"},
	{Num: "3.18", Section: 3, NameEN: "Types of impact in the Member States", Name: "Tipi di impatto negli altri Stati membri", When: "se è superata la soglia della diffusione geografica", Hint: "clienti, controparti, succursali, entità del gruppo, infrastrutture di mercato, fornitori terzi comuni"},
	{Num: "3.19", Section: 3, NameEN: "Description of how the major ICT-related incident has an impact in other Member States", Name: "Come l'incidente incide negli altri Stati membri", When: "se è superata la soglia della diffusione geografica", Narrative: true},
	{Num: "3.20", Section: 3, NameEN: "Materiality thresholds for the classification criterion 'Data losses'", Name: "Perdita di dati (disponibilità, autenticità, integrità, riservatezza)", When: "se il criterio della perdita di dati è soddisfatto", Hint: "criteri d'impatto dell'incidente"},
	{Num: "3.21", Section: 3, NameEN: "Description of the data losses", Name: "Descrizione della perdita di dati", When: "se il criterio della perdita di dati è soddisfatto", Narrative: true},
	{Num: "3.22", Section: 3, NameEN: "Classification criterion 'Critical services affected'", Name: "Servizi critici colpiti", Hint: "applicazioni impattate che supportano funzioni critiche o importanti"},
	{Num: "3.23", Section: 3, NameEN: "Type of the major ICT-related incident", Name: "Tipo di incidente", Narrative: true, Hint: "dalla causa registrata nell'incidente"},
	{Num: "3.24", Section: 3, NameEN: "Other types of incidents", Name: "Altri tipi di incidente", When: "se il tipo è «altro»"},
	{Num: "3.25", Section: 3, NameEN: "Threats and techniques used by the threat actor", Name: "Minacce e tecniche dell'attore", When: "se applicabile (incidenti informatici)", Narrative: true, Hint: "analisi della sicurezza IT"},
	{Num: "3.26", Section: 3, NameEN: "Other types of techniques", Name: "Altre tecniche", When: "se la tecnica è «altro»"},
	{Num: "3.27", Section: 3, NameEN: "Information about affected functional areas and business processes", Name: "Aree funzionali e processi colpiti", Narrative: true, Hint: "applicazioni impattate e processi che supportano"},
	{Num: "3.28", Section: 3, NameEN: "Affected infrastructure components supporting business processes", Name: "Componenti infrastrutturali colpiti", Hint: "applicazioni e asset impattati nell'incidente"},
	{Num: "3.29", Section: 3, NameEN: "Information about affected infrastructure components supporting business processes", Name: "Dettagli sui componenti infrastrutturali colpiti", Narrative: true},
	{Num: "3.30", Section: 3, NameEN: "Impact on the financial interest of clients", Name: "Impatto sugli interessi finanziari dei clienti", Hint: "valutazione dell'area che segue i clienti"},
	{Num: "3.31", Section: 3, NameEN: "Reporting to other authorities", Name: "Segnalazioni ad altre autorità", Hint: "altre notifiche dell'incidente registrate come inviate (per esempio NIS2)"},
	{Num: "3.32", Section: 3, NameEN: "Specification of 'other' authorities", Name: "Quali altre autorità", When: "se si è indicato «altre autorità»"},
	{Num: "3.33", Section: 3, NameEN: "Temporary actions/measures taken or planned to be taken to recover from the incident", Name: "Misure temporanee per il ripristino", Narrative: true, Hint: "contenimento e azioni nella timeline"},
	{Num: "3.34", Section: 3, NameEN: "Description of any temporary actions and measures taken or planned to be taken to recover from the incident", Name: "Descrizione delle misure temporanee", Narrative: true},
	{Num: "3.35", Section: 3, NameEN: "Indicators of compromise", Name: "Indicatori di compromissione", When: "se applicabile", Hint: "analisi della sicurezza IT (indirizzi, hash, domini)"},

	{Num: "4.1", Section: 4, NameEN: "High-level classification of root causes of the incident", Name: "Classificazione generale delle cause", Narrative: true, Hint: "postmortem: causa radice"},
	{Num: "4.2", Section: 4, NameEN: "Detailed classification of root causes of the incident", Name: "Classificazione dettagliata delle cause", Narrative: true, Hint: "postmortem: causa radice"},
	{Num: "4.3", Section: 4, NameEN: "Additional classification of root causes of the incident", Name: "Classificazione aggiuntiva delle cause", Narrative: true},
	{Num: "4.4", Section: 4, NameEN: "Other types of root cause types", Name: "Altri tipi di causa", When: "se la causa è «altro»"},
	{Num: "4.5", Section: 4, NameEN: "Information about the root causes of the incident", Name: "Informazioni sulle cause", Hint: "postmortem: causa radice"},
	{Num: "4.6", Section: 4, NameEN: "Incident resolution summary", Name: "Sintesi della risoluzione", Hint: "postmortem: sintesi"},
	{Num: "4.7", Section: 4, NameEN: "Date and time when the incident root cause was addressed", Name: "Data e ora in cui la causa è stata rimossa", Hint: "chiusura delle azioni correttive"},
	{Num: "4.8", Section: 4, NameEN: "Date and time when the incident was resolved", Name: "Data e ora della risoluzione"},
	{Num: "4.9", Section: 4, NameEN: "Information if the permanent resolution date of the incident differs from the initially planned implementation date", Name: "Scostamento dalla data di risoluzione prevista", When: "se la data è cambiata"},
	{Num: "4.10", Section: 4, NameEN: "Assessment of risk to critical functions for resolution purposes", Name: "Rischio per le funzioni critiche ai fini della risoluzione", When: "se applicabile", Hint: "solo per le entità soggette alle autorità di risoluzione"},
	{Num: "4.11", Section: 4, NameEN: "Information relevant for resolution authorities", Name: "Informazioni per le autorità di risoluzione", When: "se applicabile"},
	{Num: "4.12", Section: 4, NameEN: "Materiality threshold for the classification criterion 'Economic impact'", Name: "Soglia dell'impatto economico", Hint: "classificazione con lo schema DORA"},
	{Num: "4.13", Section: 4, NameEN: "Amount of gross direct and indirect costs and losses", Name: "Costi e perdite lordi, diretti e indiretti", Hint: "criteri d'impatto dell'incidente (impatto economico)"},
	{Num: "4.14", Section: 4, NameEN: "Amount of financial recoveries", Name: "Importi recuperati", Hint: "amministrazione: assicurazioni e rimborsi"},
	{Num: "4.15", Section: 4, NameEN: "Information on whether the non-major incidents have been recurring", Name: "Incidenti non gravi ricorrenti"},
	{Num: "4.16", Section: 4, NameEN: "Date and time of occurrence of recurring incidents", Name: "Data e ora degli incidenti ricorrenti", When: "se ricorrenti"},
}

// DoraSources sono i riferimenti normativi del modello, mostrati nella bozza e nell'esportazione.
var DoraSources = []string{
	"Reg. (UE) 2022/2554 (DORA), art. 19",
	"Reg. delegato (UE) 2025/301, artt. 1-5 (contenuto e termini)",
	"Reg. di esecuzione (UE) 2025/302, allegati I e II (modello e glossario)",
}

// IsDora dice se lo schema della notifica segue DORA (lo schema di partenza «dora», o uno
// schema del tenant con il riferimento al regolamento).
func IsDora(n *Notification, reference string) bool {
	return n.SchemeKey == "dora" || strings.Contains(reference, "2022/2554")
}

// doraSections dice quali sezioni del modello compilare per la tappa: la notifica iniziale le
// informazioni generali e la sua sezione; l'intermedia anche la 3; la finale tutto.
func doraSections(stageKey, stageName string, ordinal int) (int, string) {
	k := strings.ToLower(stageKey + " " + stageName)
	switch {
	case strings.Contains(k, "final"):
		return 4, "final report"
	case strings.Contains(k, "intermed"):
		return 3, "intermediate report"
	case strings.Contains(k, "inizial") || strings.Contains(k, "initial"):
		return 2, "initial notification"
	}
	return min(max(ordinal+1, 2), 4), []string{"", "", "initial notification", "intermediate report", "final report"}[min(max(ordinal+1, 2), 4)]
}

// PrefilledField è un campo con il valore precompilato da Regia.
type PrefilledField struct {
	DoraField
	Value string `json:"value"`
	// Status: compilato (dai dati di Regia) | da_verificare (dedotto, da controllare) | mancante.
	Status string `json:"status"`
	// Source: da dove viene il valore (vuoto se manca).
	Source string `json:"source,omitempty"`
}

// DoraPrefill è il modello precompilato per una notifica.
type DoraPrefill struct {
	Notification *Notification    `json:"notification"`
	Incident     *IncidentView    `json:"-"`
	Reference    string           `json:"reference"`
	Fields       []PrefilledField `json:"fields"`
	Sources      []string         `json:"sources"`
}

func fmtUTC(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// ddhhmm scrive una durata come GG:OO:MM.
func ddhhmm(minutes int) string {
	if minutes < 0 {
		minutes = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", minutes/1440, (minutes%1440)/60, minutes%60)
}

// doraCriteria traduce i criteri dello schema di partenza nelle scelte del campo 2.5.
var doraCriteria = map[string]string{
	"servizi_critici": "critical services affected",
	"perdita_dati":    "data losses",
	"clienti":         "clients, financial counterparts and transactions affected",
	"durata":          "duration and service downtime",
	"diffusione":      "geographical spread",
	"economico":       "economic impact",
	"reputazione":     "reputational impact",
}

// PrefillDora prepara il modello della notifica con i dati dell'incidente (incident.manage).
// Non chiama il modello: i campi descrittivi restano da scrivere (Narrative) e quelli che Regia
// non conosce restano mancanti, con l'indicazione di dove si ricavano.
func (s *Service) PrefillDora(ctx context.Context, tx pgx.Tx, actor engine.Actor, notificationID string) (*DoraPrefill, error) {
	n, err := s.NotificationOf(ctx, tx, actor, notificationID)
	if err != nil {
		return nil, err
	}
	iv, err := s.IncidentView(ctx, tx, actor, n.IncidentID)
	if err != nil {
		return nil, err
	}
	var reference string
	for _, c := range iv.Classification {
		if c.Key == n.SchemeKey {
			reference = c.Reference
		}
	}
	if !IsDora(n, reference) {
		return nil, engine.InvalidCode("validation.risk.notification_not_dora", "notification", "il modello DORA vale solo per le notifiche dello schema DORA")
	}
	upTo, submission := doraSections(n.StageKey, n.StageName, n.Ordinal)
	var tenantName, currency string
	if err := tx.QueryRow(ctx, `select name, base_currency from tenants where id = app_tenant()`).Scan(&tenantName, &currency); err != nil {
		return nil, err
	}
	var leadEmail string
	if iv.LeadID != nil {
		_ = tx.QueryRow(ctx, `select coalesce(m.email, '') from members m where m.id = $1`, *iv.LeadID).Scan(&leadEmail)
	}
	var dora *SchemeResult
	for i := range iv.Classification {
		if iv.Classification[i].Key == n.SchemeKey {
			dora = &iv.Classification[i]
		}
	}
	met := func(key string) bool {
		if dora == nil {
			return false
		}
		return slices.ContainsFunc(dora.Criteria, func(c CriterionResult) bool { return c.Key == key && c.Met })
	}
	values := map[string][2]string{}  // num → valore, fonte (compilato)
	guesses := map[string][2]string{} // num → valore, fonte (da verificare)
	set := func(num, value, source string) {
		if strings.TrimSpace(value) != "" {
			values[num] = [2]string{value, source}
		}
	}
	guess := func(num, value, source string) {
		if strings.TrimSpace(value) != "" {
			guesses[num] = [2]string{value, source}
		}
	}
	set("1.1", submission, "tappa della notifica: "+n.StageName)
	guess("1.2", tenantName, "nome dell'organizzazione in Regia")
	if iv.Lead != nil {
		guess("1.7", *iv.Lead, "responsabile dell'incidente")
		guess("1.8", leadEmail, "email del responsabile dell'incidente")
	}
	set("1.15", currency, "valuta base dell'organizzazione")
	set("2.1", iv.Key, "chiave dell'incidente in Regia")
	set("2.2", iv.DetectedAt.UTC().Format("2006-01-02T15:04:05Z"), "rilevazione registrata nell'incidente")
	set("2.3", fmtUTC(iv.ClassifiedAt), "classificazione registrata nell'incidente")
	if dora != nil {
		var crit []string
		for _, c := range dora.Criteria {
			if !c.Met {
				continue
			}
			if v, ok := doraCriteria[c.Key]; ok {
				crit = append(crit, v)
			} else {
				crit = append(crit, c.Name)
			}
		}
		set("2.5", strings.Join(crit, "; "), "criteri soddisfatti nella classificazione «"+dora.Name+"»")
	}
	if iv.MemberStates != nil && *iv.MemberStates > 0 {
		guess("2.6", fmt.Sprintf("%d Stati membri (codici ISO da indicare)", *iv.MemberStates), "criteri d'impatto: Stati membri")
	}
	if iv.CauseProvider != nil {
		set("2.8", "Sì: "+iv.CauseProvider.Title+" ("+iv.CauseProvider.Key+")", "causa «fornitore terzo» e fornitore ICT collegato")
	} else if iv.Cause != nil {
		guess("2.8", "No ("+iv.CauseName+")", "causa registrata nell'incidente")
	}
	for _, other := range iv.Notifications {
		if other.SchemeKey == n.SchemeKey && other.Ordinal < n.Ordinal && other.Reference != nil && *other.Reference != "" {
			guess("3.1", *other.Reference, "riferimento registrato all'invio di «"+other.StageName+"»")
		}
	}
	if iv.ResolvedAt != nil {
		guess("3.3", fmtUTC(iv.ResolvedAt), "risoluzione registrata nell'incidente")
	} else if iv.ContainedAt != nil {
		guess("3.3", fmtUTC(iv.ContainedAt), "contenimento registrato nell'incidente")
	}
	if iv.ClientsAffected != nil {
		set("3.4", fmt.Sprint(*iv.ClientsAffected), "criteri d'impatto: clienti coinvolti")
	}
	if iv.ClientsPct != nil {
		set("3.5", strings.Replace(fmt.Sprintf("%.1f", *iv.ClientsPct), ".", ",", 1), "criteri d'impatto: percentuale dei clienti")
	}
	if iv.Reputational {
		guess("3.13", "impatto reputazionale presente (indicare le categorie)", "criteri d'impatto: impatto reputazionale")
	}
	if mins := int(iv.DurationHours * 60); mins > 0 {
		src := "dalla rilevazione alla risoluzione"
		if iv.ResolvedAt == nil {
			src = "dalla rilevazione a oggi: incidente non ancora risolto, è una stima"
			guess("3.15", ddhhmm(mins), src)
		} else {
			set("3.15", ddhhmm(mins), src)
		}
	}
	if iv.DowntimeMinutes != nil {
		set("3.16", ddhhmm(*iv.DowntimeMinutes), "criteri d'impatto: fermo del servizio")
	}
	if len(iv.DataLoss) > 0 {
		en := map[string]string{"availability": "availability", "integrity": "integrity", "confidentiality": "confidentiality", "authenticity": "authenticity"}
		var dl []string
		for _, d := range iv.DataLoss {
			dl = append(dl, en[d])
		}
		set("3.20", strings.Join(dl, "; "), "criteri d'impatto: perdita di dati")
	}
	var critical, comps []string
	for _, a := range iv.Impacted {
		comps = append(comps, a.Key+" "+a.Title)
		if a.Critical {
			critical = append(critical, a.Key+" "+a.Title)
		}
	}
	if len(critical) > 0 {
		set("3.22", strings.Join(critical, "; "), "applicazioni impattate con funzione critica")
	} else if met("servizi_critici") {
		guess("3.22", "servizi critici colpiti (da indicare)", "criterio «servizi critici» soddisfatto")
	}
	guess("3.28", strings.Join(comps, "; "), "applicazioni e asset impattati")
	var others []string
	for _, other := range iv.Notifications {
		if other.SchemeKey != n.SchemeKey && other.Status == "submitted" {
			at := ""
			if other.SubmittedAt != nil {
				at = " il " + other.SubmittedAt.UTC().Format("2006-01-02")
			}
			others = append(others, other.SchemeName+" · "+other.StageName+at)
		}
	}
	if len(others) > 0 {
		set("3.31", strings.Join(others, "; "), "altre notifiche dell'incidente registrate come inviate")
	}
	if iv.Postmortem != nil {
		set("4.5", strings.TrimSpace(iv.Postmortem.RootCause), "postmortem: causa radice")
		set("4.6", strings.TrimSpace(iv.Postmortem.Summary), "postmortem: sintesi")
	}
	set("4.8", fmtUTC(iv.ResolvedAt), "risoluzione registrata nell'incidente")
	if dora != nil {
		if met("economico") {
			set("4.12", "soglia superata", "criterio «impatto economico» soddisfatto")
		} else {
			guess("4.12", "soglia non superata", "criterio «impatto economico» non soddisfatto")
		}
	}
	if iv.EconomicCents != nil {
		set("4.13", strings.Replace(fmt.Sprintf("%.2f %s", float64(*iv.EconomicCents)/100, currency), ".", ",", 1), "criteri d'impatto: impatto economico")
	}
	if iv.Recurring {
		set("4.15", "sì", "incidente segnato come ricorrente")
	} else {
		guess("4.15", "no", "incidente non segnato come ricorrente")
	}

	out := &DoraPrefill{Notification: n, Incident: iv, Reference: reference, Fields: []PrefilledField{}, Sources: DoraSources}
	for _, f := range DoraFields {
		if f.Section > upTo {
			continue
		}
		pf := PrefilledField{DoraField: f, Status: "mancante"}
		if v, ok := values[f.Num]; ok {
			pf.Value, pf.Source, pf.Status = v[0], v[1], "compilato"
		} else if v, ok := guesses[f.Num]; ok {
			pf.Value, pf.Source, pf.Status = v[0], v[1], "da_verificare"
		}
		out.Fields = append(out.Fields, pf)
	}
	return out, nil
}

// DoraMarkdown scrive la bozza come documento Markdown da esportare (o da salvare come contenuto).
func DoraMarkdown(n *Notification, fields []NoticeField, version int, source string, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n", n.SchemeName, n.StageName)
	fmt.Fprintf(&b, "Incidente: %s · %s\n\n", n.IncidentKey, n.Incident)
	if version > 0 {
		who := "salvata a mano"
		if source == DraftByAssistant {
			who = "preparata con l'assistente e rivista dalla persona"
		}
		fmt.Fprintf(&b, "Bozza versione %d del %s, %s. Non inviata da Regia.\n\n", version, at.UTC().Format("2006-01-02 15:04 UTC"), who)
	} else {
		b.WriteString("Bozza non ancora salvata. Non inviata da Regia.\n\n")
	}
	b.WriteString("Riferimenti: " + strings.Join(DoraSources, "; ") + ".\n")
	section := ""
	names := map[string]string{"1": "Informazioni generali", "2": "Notifica iniziale", "3": "Relazione intermedia", "4": "Relazione finale"}
	var missing []NoticeField
	for _, f := range fields {
		if sec, _, _ := strings.Cut(f.Num, "."); sec != section {
			section = sec
			fmt.Fprintf(&b, "\n## %s\n", names[sec])
		}
		fmt.Fprintf(&b, "\n**%s %s**", f.Num, f.Name)
		switch {
		case f.Status == "mancante" || strings.TrimSpace(f.Value) == "":
			b.WriteString(" · *mancante*")
			if f.Hint != "" {
				b.WriteString(" (da: " + f.Hint + ")")
			}
			b.WriteString("\n")
			missing = append(missing, f)
		case f.Status == "da_verificare":
			fmt.Fprintf(&b, " · *da verificare*\n\n%s\n", f.Value)
		default:
			fmt.Fprintf(&b, "\n\n%s\n", f.Value)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\n## Campi mancanti (%d)\n\n", len(missing))
		for _, f := range missing {
			line := "- " + f.Num + " " + f.Name
			if f.Hint != "" {
				line += ": " + f.Hint
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
