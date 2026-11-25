// Package contract è il contratto neutro tra Regia e le pipeline (replica/devsecops.md §2).
// Ogni sorgente (API di push, connettore GitHub/GitLab, profilo di template aziendali)
// traduce i propri dati in questi oggetti; il modulo devsecops li salva tramite Ingestor.
// Il contratto cresce solo per aggiunte: un campo nuovo è sempre facoltativo.
package contract

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Version identifica il formato dei payload JSON dell'API di push.
const Version = "regia.devsecops/v1"

// Service identifica un servizio (un repository o un componente rilasciabile) e, se noto,
// l'applicazione del catalogo a cui appartiene. Un'applicazione può avere più servizi.
type Service struct {
	Name        string `json:"name"`                  // es. orders-api; univoco nel tenant
	Repository  string `json:"repository,omitempty"`  // es. org/orders-api
	Provider    string `json:"provider,omitempty"`    // github | gitlab | gitea | azure_devops | bitbucket | other
	URL         string `json:"url,omitempty"`         // pagina del repository
	Application string `json:"application,omitempty"` // codice o chiave dell'applicazione (es. API-CLI, ARC-3); vuoto = associazione già nota
}

// CheckResult è l'esito di un controllo della pipeline.
type CheckResult struct {
	Name    string         `json:"name"`              // es. test, lint, secrets, dependencies, sonar, semgrep, image
	Mode    string         `json:"mode,omitempty"`    // off | warn | block
	Outcome string         `json:"outcome"`           // passed | failed | skipped | not_run
	Skip    *SkipException `json:"skip,omitempty"`    // eccezione concessa (es. etichetta skip-checks)
	Summary string         `json:"summary,omitempty"` // una riga leggibile
	URL     string         `json:"url,omitempty"`
}

// SkipException è un controllo bloccante fallito ma saltato da una persona.
type SkipException struct {
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
}

// CheckRun è l'esito complessivo dei controlli su un commit (il "verdict").
type CheckRun struct {
	Service Service       `json:"service"`
	Commit  string        `json:"commit"`
	Ref     string        `json:"ref,omitempty"` // branch o PR
	RunID   string        `json:"run_id,omitempty"`
	URL     string        `json:"url,omitempty"`
	Verdict string        `json:"verdict"` // passed | failed
	Checks  []CheckResult `json:"checks"`
	At      time.Time     `json:"at"`
}

// VulnSummary riassume le vulnerabilità di un artefatto.
type VulnSummary struct {
	Critical        int `json:"critical"`
	High            int `json:"high"`
	Medium          int `json:"medium"`
	Low             int `json:"low"`
	FixableCritical int `json:"fixable_critical"`
	FixableHigh     int `json:"fixable_high"`
}

// Release è il registro di un rilascio in un ambiente (di solito la produzione).
type Release struct {
	Service       Service       `json:"service"`
	Version       string        `json:"version"`
	Commit        string        `json:"commit,omitempty"`
	Image         string        `json:"image,omitempty"`
	Digest        string        `json:"digest,omitempty"` // sha256:… dell'artefatto rilasciato
	Environment   string        `json:"environment"`      // es. prod
	ChangeNumber  string        `json:"change_number,omitempty"`
	ChangeTitle   string        `json:"change_title,omitempty"`
	RequestedBy   string        `json:"requested_by,omitempty"`
	ApprovedBy    string        `json:"approved_by,omitempty"`
	ApprovedAt    *time.Time    `json:"approved_at,omitempty"`
	PullRequest   string        `json:"pull_request,omitempty"` // URL della PR di rilascio
	ReleaseURL    string        `json:"release_url,omitempty"`
	At            time.Time     `json:"at"`
	CVEsAtRelease *VulnSummary  `json:"cves_at_release,omitempty"`
	Checks        []CheckResult `json:"checks,omitempty"` // esiti dei controlli del commit rilasciato
	// ChangeAliases: numeri provvisori dello stesso change, sostituiti da ChangeNumber
	// (es. un numero temporaneo della pipeline prima di quello definitivo). Facoltativo.
	ChangeAliases []string `json:"change_aliases,omitempty"`
}

// Deployment è una versione in esecuzione (o tentata) in un ambiente.
type Deployment struct {
	Service     Service   `json:"service"`
	Environment string    `json:"environment"`
	Version     string    `json:"version"`
	Digest      string    `json:"digest,omitempty"`
	Status      string    `json:"status"` // running | succeeded | failed | rolled_back
	At          time.Time `json:"at"`
	Source      string    `json:"source,omitempty"` // gitops | pipeline | manual
	ExternalRef string    `json:"external_ref,omitempty"`
}

// SBOM è la distinta dei componenti di un artefatto.
type SBOM struct {
	Service  Service         `json:"service"`
	Version  string          `json:"version"`
	Digest   string          `json:"digest,omitempty"`
	Format   string          `json:"format"` // cyclonedx | spdx
	Document json.RawMessage `json:"document"`
}

// Vulnerability è una vulnerabilità trovata in un artefatto.
type Vulnerability struct {
	ID       string   `json:"id"` // CVE-… o GHSA-…
	Aliases  []string `json:"aliases,omitempty"`
	Package  string   `json:"package"`
	PURL     string   `json:"purl,omitempty"`
	Version  string   `json:"version,omitempty"`
	FixedIn  string   `json:"fixed_in,omitempty"`
	Severity string   `json:"severity"`        // critical | high | medium | low | unknown
	Layer    string   `json:"layer,omitempty"` // image (base e runtime) | app (dipendenze del codice)
	URL      string   `json:"url,omitempty"`
}

// VulnReport è il risultato di una scansione di un artefatto.
type VulnReport struct {
	Service         Service         `json:"service"`
	Version         string          `json:"version,omitempty"`
	Digest          string          `json:"digest,omitempty"`
	Scanner         string          `json:"scanner"` // grype | trivy | osv | other
	ScannedAt       time.Time       `json:"scanned_at"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
}

// Ingestor salva gli oggetti del contratto nel tenant della transazione. Ogni metodo è
// idempotente: lo stesso oggetto ricevuto due volte (webhook ripetuti, sincronizzazioni)
// non crea duplicati. Lo implementa il modulo devsecops; lo usano l'API di push e i connettori.
type Ingestor interface {
	IngestCheckRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, c CheckRun) error
	IngestRelease(ctx context.Context, tx pgx.Tx, actor engine.Actor, r Release) error
	IngestDeployment(ctx context.Context, tx pgx.Tx, actor engine.Actor, d Deployment) error
	IngestSBOM(ctx context.Context, tx pgx.Tx, actor engine.Actor, s SBOM) error
	IngestVulnReport(ctx context.Context, tx pgx.Tx, actor engine.Actor, v VulnReport) error
}

// ServiceIngestor registra un servizio senza altri dati: per esempio un repository scoperto
// da un connettore, o un servizio censito prima di arrivare sulla pipeline. Idempotente come
// Ingestor; lo implementa lo stesso modulo devsecops.
type ServiceIngestor interface {
	IngestService(ctx context.Context, tx pgx.Tx, actor engine.Actor, s Service) error
}
