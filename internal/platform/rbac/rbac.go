// Package rbac definisce i permessi e i ruoli di sistema creati con ogni tenant.
// I ruoli sono dati (tabella roles): il tenant può crearne altri combinando i permessi.
package rbac

import "slices"

// Permessi. Il formato è "area.azione"; "*" li concede tutti.
const (
	All = "*"

	TenantAdmin  = "tenant.admin"  // membri, gruppi, ruoli, configurazione
	ElementRead  = "element.read"  // leggere elementi visibili
	ElementWrite = "element.write" // creare e modificare elementi
	CommentWrite = "comment.write"
	AuditRead    = "audit.read"
	FinanceRead  = "finance.read"       // tariffe e singole voci di costo (apre app.finance)
	FinanceWrite = "finance.write"      // budget, tariffe, import dei costi
	PortalSubmit = "portal.submit"      // aprire richieste dal portale
	ProcessAdmin = "process.admin"      // processi, gate, regole (sdlc.md §1); non serve essere tenant admin
	ArchWrite    = "architecture.write" // catalogo di asset logici, applicazioni e team
	// PortfolioAdmin: scorecard, impostazioni della graduatoria e organi di governance.
	PortfolioAdmin = "portfolio.admin"
	// FieldsAdmin: campi personalizzati, insiemi di valori e associazioni ai tipi di elemento.
	FieldsAdmin = "fields.admin"
	// ReleaseRead: interrogare il gate di rilascio (GET /api/v1/release-gate), per le pipeline.
	// Finisce in .read: un token con solo questo permesso è di sola lettura.
	ReleaseRead = "release.read"
	// DevsecopsWrite: inviare a Regia servizi, esiti dei controlli, rilasci, deploy, SBOM e
	// report delle vulnerabilità (POST /api/v1/devsecops/..., piano, passo 21), per le pipeline.
	DevsecopsWrite = "devsecops.write"
	// DevsecopsGate: chiedere il gate di rilascio prima del deploy (POST /api/v1/devsecops/gate,
	// piano, passo 21, fase 4). Non è di sola lettura: ogni chiamata registra una decisione.
	DevsecopsGate = "devsecops.gate"
)

// Known sono i permessi esistenti (senza "*"): servono, per esempio, a scegliere
// i permessi ridotti di un token API quando il membro li ha tutti.
var Known = []string{
	ElementRead, ElementWrite, CommentWrite, AuditRead, FinanceRead, FinanceWrite,
	PortalSubmit, ProcessAdmin, ArchWrite, TenantAdmin, FieldsAdmin, ReleaseRead,
	DevsecopsWrite, DevsecopsGate,
}

// SystemRole è un ruolo creato con ogni nuovo tenant.
type SystemRole struct {
	Key         string
	Name        string
	Permissions []string
}

// SystemRoles sono i ruoli di default; nomi in italiano, modificabili dal tenant.
var SystemRoles = []SystemRole{
	{"tenant_admin", "Amministratore del tenant", []string{All}},
	{"editor", "Editor", []string{ElementRead, ElementWrite, CommentWrite}},
	{"viewer", "Lettore", []string{ElementRead}},
	{"auditor", "Auditor", []string{ElementRead, AuditRead}},
	{"finance", "Finanza e PMO", []string{ElementRead, FinanceRead, FinanceWrite}},
	{"requester", "Richiedente (portale)", []string{PortalSubmit, CommentWrite}}, // commenta solo ciò che vede
	{"process_admin", "Amministratore di processo", []string{ElementRead, ProcessAdmin}},
	{"architect", "Architetto", []string{ElementRead, ElementWrite, CommentWrite, ArchWrite}},
	{"portfolio_manager", "Governo del portafoglio", []string{ElementRead, ElementWrite, CommentWrite, PortfolioAdmin}},
	// Membro di un team valutatore (piano, passo 4): legge, commenta per i chiarimenti, decide
	// sulle valutazioni del suo team (il permesso di decidere viene dal gruppo del team).
	{"assessor", "Valutatore", []string{ElementRead, CommentWrite}},
	// Configura i campi personalizzati senza essere amministratore del tenant.
	{"fields_admin", "Amministratore dei campi", []string{ElementRead, FieldsAdmin}},
	// Pipeline di rilascio (piano, passo 6): solo il gate di rilascio, per i token API.
	{"release_gate", "Pipeline di rilascio", []string{ReleaseRead}},
	// Pipeline (piano, passo 21): solo l'invio dei dati DevSecOps, per i token API.
	{"pipeline", "Pipeline", []string{DevsecopsWrite, DevsecopsGate}},
}

// Has dice se l'insieme di permessi concede p.
func Has(perms []string, p string) bool {
	return slices.Contains(perms, All) || slices.Contains(perms, p)
}
