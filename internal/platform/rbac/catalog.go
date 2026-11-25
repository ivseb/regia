package rbac

// PermissionInfo spiega un permesso in italiano, per la pagina dei ruoli.
type PermissionInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Area        string `json:"area"`
	// Sensitive: permesso che apre dati riservati o poteri di amministrazione.
	Sensitive bool `json:"sensitive,omitempty"`
}

var permissionInfo = map[string]PermissionInfo{
	ElementRead:    {Name: "Leggere gli elementi", Area: "Lavoro", Description: "Vede richieste, impatti, applicazioni e gli altri elementi dell'organizzazione. Senza, si vedono solo gli elementi in cui si ha un ruolo."},
	ElementWrite:   {Name: "Creare e modificare elementi", Area: "Lavoro", Description: "Crea elementi, ne modifica i campi e li fa avanzare di fase dove il processo lo consente."},
	CommentWrite:   {Name: "Commentare", Area: "Lavoro", Description: "Scrive commenti e allega file sugli elementi che può vedere."},
	PortalSubmit:   {Name: "Aprire richieste dal portale", Area: "Lavoro", Description: "Apre nuove richieste e segue le proprie."},
	FinanceRead:    {Name: "Vedere tariffe e costi", Area: "Finanza", Sensitive: true, Description: "Vede le tariffe del personale e le singole voci di costo (dati riservati, protetti anche nel database)."},
	FinanceWrite:   {Name: "Gestire budget e costi", Area: "Finanza", Sensitive: true, Description: "Prepara e rivede i budget, registra e importa i costi, gestisce le tariffe."},
	PortfolioAdmin: {Name: "Governare il portafoglio", Area: "Configurazione", Description: "Configura scorecard, graduatoria e organi di governance."},
	ProcessAdmin:   {Name: "Configurare i processi", Area: "Configurazione", Description: "Modifica fasi, passaggi, gate e regole dei processi, e le valutazioni con questionari."},
	FieldsAdmin:    {Name: "Configurare i campi", Area: "Configurazione", Description: "Crea e modifica i campi personalizzati e le loro associazioni ai tipi di elemento."},
	ArchWrite:      {Name: "Gestire il catalogo dell'architettura", Area: "Configurazione", Description: "Crea e modifica asset logici, applicazioni e team."},
	AuditRead:      {Name: "Leggere il registro di audit", Area: "Amministrazione", Sensitive: true, Description: "Consulta il registro di audit e la verifica della catena di hash."},
	DevsecopsWrite: {Name: "Inviare i dati delle pipeline", Area: "Integrazioni", Description: "Invia a Regia servizi, esiti dei controlli, rilasci, deploy, SBOM e vulnerabilità. Pensato per i token API delle pipeline."},
	DevsecopsGate:  {Name: "Chiedere il gate di rilascio", Area: "Integrazioni", Description: "Chiede a Regia il permesso di rilascio prima del deploy e ne riceve l'esito (pass, warn o fail) con i motivi. Pensato per i token API delle pipeline."},
	ReleaseRead:    {Name: "Interrogare il gate di rilascio", Area: "Integrazioni", Description: "Legge l'esito del gate di rilascio (pass, warn o fail) per un servizio e una versione, senza poter inviare dati. Pensato per i token API delle pipeline."},
	TenantAdmin:    {Name: "Amministrare l'organizzazione", Area: "Amministrazione", Sensitive: true, Description: "Gestisce persone, ruoli, gruppi, sicurezza e integrazioni. Da solo non concede la lettura degli elementi."},
}

// Catalog elenca i permessi assegnabili ai ruoli personalizzati, con la spiegazione.
// Un permesso senza descrizione (aggiunto da un modulo) compare con la sua chiave.
func Catalog() []PermissionInfo {
	out := make([]PermissionInfo, 0, len(Known)+1)
	for _, k := range assignable() {
		info, ok := permissionInfo[k]
		if !ok {
			info = PermissionInfo{Name: k, Area: "Altro"}
		}
		info.Key = k
		out = append(out, info)
	}
	return out
}

// Assignable dice se un permesso si può mettere in un ruolo personalizzato: tutti quelli
// conosciuti, mai "*" (resta del solo ruolo di sistema "Amministratore del tenant").
func Assignable(p string) bool {
	for _, k := range assignable() {
		if k == p {
			return true
		}
	}
	return false
}

// assignable: i permessi di Known più quelli usati dai ruoli di sistema ma non elencati
// lì (portfolio.admin), così un ruolo duplicato non perde nulla.
func assignable() []string {
	out := append([]string(nil), Known...)
	for _, r := range SystemRoles {
		for _, p := range r.Permissions {
			if p != All && !contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
