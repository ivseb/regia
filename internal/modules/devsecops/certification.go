package devsecops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/grc"
)

// Certificazione del rilascio: un documento di una pagina per numero di change, con le
// versioni rilasciate, gli elementi, i test, Sonar, le CVE al rilascio, la decisione del gate
// e le approvazioni. Si legge sempre dal vivo; «Genera» congela un'istantanea (JSON con hash)
// in release_certifications e, se la conformità ha un controllo mappato sulla sorgente
// «certification», registra un'evidenza GRC automatica.

const (
	CollectorCertification = "devsecops.certification"
	sourceCertification    = "certification"
)

// CertChange è il change della certificazione.
type CertChange struct {
	ID      string   `json:"id"`
	Number  string   `json:"number"`
	Aliases []string `json:"aliases"`
	Title   string   `json:"title"`
	Request *Ref     `json:"request"`
	// ServiceNow: la change letta dal connettore ServiceNow (tabella servicenow_changes), se c'è.
	ServiceNow *CertSnChange `json:"servicenow"`
}

// CertSnChange è la change di ServiceNow: stato grezzo (-5 nuova … -2 pianificata, 3 chiusa,
// 4 annullata), approvazione (not requested | requested | approved | rejected), tipo, finestra
// pianificata ed effettiva, link al record. Chi ha approvato non si legge (dato personale).
type CertSnChange struct {
	State        string     `json:"state"`
	Approval     string     `json:"approval"`
	Type         string     `json:"type"`
	PlannedStart *time.Time `json:"planned_start"`
	PlannedEnd   *time.Time `json:"planned_end"`
	ActualStart  *time.Time `json:"actual_start"`
	ActualEnd    *time.Time `json:"actual_end"`
	URL          string     `json:"url"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

// Ref è un elemento citato (chiave, titolo, stato).
type Ref struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state"`
}

// TestRun è un esito di test di una versione rilasciata. Origin dice da dove viene: oggi
// «pipeline» (i controlli del rilascio); poi un connettore ALM in lettura e il test
// management nativo. I numeri mancano se la fonte non li dà.
type TestRun struct {
	Origin   string `json:"origin"`
	Service  string `json:"service"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Outcome  string `json:"outcome"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
	Executed *int   `json:"executed"`
	Passed   *int   `json:"passed"`
	Failed   *int   `json:"failed"`
	Skipped  *int   `json:"skipped"`
}

// TestTotals somma i numeri delle esecuzioni che li hanno.
type TestTotals struct {
	Executed int  `json:"executed"`
	Passed   int  `json:"passed"`
	Failed   int  `json:"failed"`
	Skipped  int  `json:"skipped"`
	Counted  bool `json:"counted"` // almeno una fonte ha dato i numeri
}

// CertCheck è un controllo della pipeline di una versione (Sonar).
type CertCheck struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}

// CertSecurity sono le CVE dichiarate al rilascio di una versione.
type CertSecurity struct {
	Service     string                `json:"service"`
	Version     string                `json:"version"`
	Environment string                `json:"environment"`
	CVEs        *contract.VulnSummary `json:"cves"`
}

// CertGate è l'ultima decisione del gate per una versione e un ambiente.
type CertGate struct {
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	Version     string    `json:"version"`
	Outcome     string    `json:"outcome"`
	Overridden  bool      `json:"overridden"`
	At          time.Time `json:"at"`
}

// CertApproval è l'approvazione dichiarata dal rilascio.
type CertApproval struct {
	Service     string     `json:"service"`
	Version     string     `json:"version"`
	Environment string     `json:"environment"`
	RequestedBy *string    `json:"requested_by"`
	ApprovedBy  *string    `json:"approved_by"`
	ApprovedAt  *time.Time `json:"approved_at"`
}

// CertData è il contenuto della certificazione (quello che l'istantanea congela).
type CertData struct {
	Change     CertChange     `json:"change"`
	Sections   []string       `json:"sections"`
	Releases   []ReleaseView  `json:"releases"`
	Items      []ReleaseItem  `json:"items"`
	Hidden     int            `json:"hidden"`
	Tests      []TestRun      `json:"tests"`
	TestTotals TestTotals     `json:"test_totals"`
	Sonar      []CertCheck    `json:"sonar"`
	Security   []CertSecurity `json:"security"`
	Gate       []CertGate     `json:"gate"`
	Approvals  []CertApproval `json:"approvals"`
	At         time.Time      `json:"at"` // quando i dati sono stati letti
}

// CertVersion è una certificazione generata.
type CertVersion struct {
	Version     int       `json:"version"`
	At          time.Time `json:"at"`
	By          *string   `json:"by"`
	SHA256      string    `json:"sha256"`
	EvidenceKey *string   `json:"evidence_key"`
}

// Certification è la risposta di GET /api/v1/devsecops/changes/{number}/certification.
type Certification struct {
	CertData
	// Version: l'istantanea mostrata (nil = dati dal vivo).
	Version     *CertVersion  `json:"version"`
	Generated   []CertVersion `json:"generated"`
	CanGenerate bool          `json:"can_generate"`
	Path        string        `json:"path"` // indirizzo stabile della pagina, da mettere nella change
	// EvidenceMapped: c'è almeno un controllo GRC mappato sulla sorgente «certification».
	EvidenceMapped bool `json:"evidence_mapped"`
}

func canCertify(a engine.Actor) bool { return CanConfigure(a) || a.Has(PermWrite) }

func certPath(number string) string { return "/change/" + url.PathEscape(number) + "/certificazione" }

// findChange trova il change dal numero o da un suo alias.
func findChange(ctx context.Context, tx pgx.Tx, number string) (*CertChange, error) {
	n := normChange(strings.TrimSpace(number))
	if n == "" {
		return nil, engine.ErrNotFound
	}
	c := &CertChange{}
	var reqKey, reqTitle, reqState *string
	err := tx.QueryRow(ctx, `select c.id::text, c.number, c.aliases, c.title, rs.key || '-' || re.number, re.title, ro.name
		from changes c
		left join elements re on re.tenant_id = c.tenant_id and re.id = c.request_element_id and re.deleted_at is null
		left join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id
		where c.number = $1 or $1 = any(c.aliases) order by (c.number = $1) desc limit 1`, n).
		Scan(&c.ID, &c.Number, &c.Aliases, &c.Title, &reqKey, &reqTitle, &reqState)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.Aliases == nil {
		c.Aliases = []string{}
	}
	if reqKey != nil {
		c.Request = &Ref{Key: *reqKey, Title: deref(reqTitle), State: deref(reqState)}
	}
	if c.ServiceNow, err = serviceNowChange(ctx, tx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// serviceNowChange legge la change dal connettore ServiceNow, se il modulo c'è (la tabella
// servicenow_changes nasce con la sua migrazione): il collegamento è sul numero o su un alias.
func serviceNowChange(ctx context.Context, tx pgx.Tx, c *CertChange) (*CertSnChange, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `select to_regclass('servicenow_changes') is not null`).Scan(&exists); err != nil || !exists {
		return nil, err
	}
	sn := &CertSnChange{}
	var state, approval, typ, url *string
	err := tx.QueryRow(ctx, `select state::text, approval::text, type::text, planned_start, planned_end, actual_start, actual_end, url::text, sys_updated_on
		from servicenow_changes where upper(number) = any($1) order by sys_updated_on desc nulls last limit 1`,
		append([]string{c.Number}, c.Aliases...)).
		Scan(&state, &approval, &typ, &sn.PlannedStart, &sn.PlannedEnd, &sn.ActualStart, &sn.ActualEnd, &url, &sn.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sn.State, sn.Approval, sn.Type, sn.URL = deref(state), deref(approval), deref(typ), deref(url)
	return sn, nil
}

// certData legge dal vivo i dati della certificazione di un change.
func (s *Service) certData(ctx context.Context, tx pgx.Tx, actor engine.Actor, ch *CertChange) (*CertData, error) {
	st, err := loadReleaseSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	d := &CertData{Change: *ch, Sections: st.CertSections, Items: []ReleaseItem{}, Tests: []TestRun{}, Sonar: []CertCheck{},
		Security: []CertSecurity{}, Gate: []CertGate{}, Approvals: []CertApproval{}, At: s.now()}
	if d.Releases, err = loadReleases(ctx, tx, "r.change_id = $1 order by r.released_at, sv.name", ch.ID); err != nil {
		return nil, err
	}
	// Elementi: dai rilasci di produzione (se non ce ne sono, da tutti), senza doppioni.
	prodOnly := false
	for _, r := range d.Releases {
		prodOnly = prodOnly || r.Production
	}
	seen := map[string]bool{}
	for _, r := range d.Releases {
		if prodOnly && !r.Production {
			continue
		}
		items, _, _, err := s.releaseItems(ctx, tx, actor, r.ID)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.ID == "" {
				d.Hidden++
				continue
			}
			if !seen[it.ID] {
				seen[it.ID] = true
				d.Items = append(d.Items, it)
			}
		}
	}
	for _, r := range d.Releases {
		for _, c := range r.Checks {
			switch {
			case isTestCheck(c.Name):
				d.Tests = append(d.Tests, testRunOf("pipeline", r, c))
			case isSonarCheck(c.Name):
				d.Sonar = append(d.Sonar, CertCheck{Service: r.Service, Version: r.Version, Name: c.Name, Outcome: c.Outcome, Summary: c.Summary, URL: c.URL})
			}
		}
		d.Security = append(d.Security, CertSecurity{Service: r.Service, Version: r.Version, Environment: r.Environment, CVEs: r.CVEsDeclared})
		d.Approvals = append(d.Approvals, CertApproval{Service: r.Service, Version: r.Version, Environment: r.Environment,
			RequestedBy: r.RequestedBy, ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt})
	}
	for _, t := range d.Tests {
		if t.Executed == nil && t.Passed == nil && t.Failed == nil && t.Skipped == nil {
			continue
		}
		d.TestTotals.Counted = true
		d.TestTotals.Executed += intOr(t.Executed)
		d.TestTotals.Passed += intOr(t.Passed)
		d.TestTotals.Failed += intOr(t.Failed)
		d.TestTotals.Skipped += intOr(t.Skipped)
	}
	// Gate: l'ultima decisione per servizio, ambiente e versione rilasciati (o col numero).
	rows, err := tx.Query(ctx, `select distinct on (coalesce(sv.name, ''), g.environment, coalesce(g.version, ''))
			coalesce(sv.name, ''), g.environment, coalesce(g.version, ''), g.outcome, g.overridden, g.created_at
		from gate_decisions g left join services sv on sv.tenant_id = g.tenant_id and sv.id = g.service_id
		where upper(g.change_number) = any($1)
		   or exists (select 1 from releases r join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id
		              where r.change_id = $2 and r.service_id = g.service_id and lower(r.version) = lower(g.version) and lower(en.name) = lower(g.environment))
		order by coalesce(sv.name, ''), g.environment, coalesce(g.version, ''), g.created_at desc`,
		append([]string{ch.Number}, ch.Aliases...), ch.ID)
	if err != nil {
		return nil, err
	}
	var g CertGate
	if _, err := pgx.ForEachRow(rows, []any{&g.Service, &g.Environment, &g.Version, &g.Outcome, &g.Overridden, &g.At}, func() error {
		g.At = g.At.UTC()
		d.Gate = append(d.Gate, g)
		return nil
	}); err != nil {
		return nil, err
	}
	return d, nil
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

var testNames = []string{"test", "tests", "unit", "junit", "integration", "e2e", "unit-tests", "unit_tests"}

func isTestCheck(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, t := range testNames {
		if n == t {
			return true
		}
	}
	return strings.HasPrefix(n, "test") || strings.HasSuffix(n, "tests") || strings.HasSuffix(n, "test")
}

func isSonarCheck(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "sonar") || n == "quality" || n == "quality_gate" || n == "quality-gate"
}

// I numeri dei test si leggono, se ci sono, dalla riga di sintesi del controllo («120 passed,
// 2 failed», «118 passati, 2 falliti, 3 saltati», «Tests: 120»).
var (
	reTestPassed  = regexp.MustCompile(`(?i)(\d+)\s*(?:passed|passati|superati|ok)\b`)
	reTestFailed  = regexp.MustCompile(`(?i)(\d+)\s*(?:failed|falliti|errors?|errori)\b`)
	reTestSkipped = regexp.MustCompile(`(?i)(\d+)\s*(?:skipped|saltati|ignorati|pending)\b`)
	reTestTotal   = regexp.MustCompile(`(?i)(?:(\d+)\s*(?:tests?|test eseguiti|eseguiti)\b|\btests?\s*[:=]\s*(\d+))`)
)

func firstInt(re *regexp.Regexp, s string) *int {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	for _, g := range m[1:] {
		if g != "" {
			if n, err := strconv.Atoi(g); err == nil {
				return &n
			}
		}
	}
	return nil
}

func testRunOf(origin string, r ReleaseView, c contract.CheckResult) TestRun {
	t := TestRun{Origin: origin, Service: r.Service, Version: r.Version, Name: c.Name, Outcome: c.Outcome, Summary: c.Summary, URL: c.URL}
	t.Passed, t.Failed, t.Skipped = firstInt(reTestPassed, c.Summary), firstInt(reTestFailed, c.Summary), firstInt(reTestSkipped, c.Summary)
	t.Executed = firstInt(reTestTotal, c.Summary)
	if t.Executed == nil && (t.Passed != nil || t.Failed != nil || t.Skipped != nil) {
		n := intOr(t.Passed) + intOr(t.Failed) + intOr(t.Skipped)
		t.Executed = &n
	}
	return t
}

func (s *Service) certVersions(ctx context.Context, tx pgx.Tx, changeID string) ([]CertVersion, error) {
	rows, err := tx.Query(ctx, `select c.version, c.generated_at, m.display_name, c.sha256, es.key || '-' || ee.number
		from release_certifications c
		left join members m on m.tenant_id = c.tenant_id and m.id = c.generated_by
		left join elements ee on ee.tenant_id = c.tenant_id and ee.id = c.evidence_id and ee.deleted_at is null
		left join spaces es on es.tenant_id = ee.tenant_id and es.id = ee.space_id
		where c.change_id = $1 order by c.version desc`, changeID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CertVersion, error) {
		var v CertVersion
		err := r.Scan(&v.Version, &v.At, &v.By, &v.SHA256, &v.EvidenceKey)
		v.At = v.At.UTC()
		return v, err
	})
}

// CertificationOf: la certificazione dal vivo o, con version, l'istantanea generata.
func (s *Service) CertificationOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, number, version string) (*Certification, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	ch, err := findChange(ctx, tx, number)
	if err != nil {
		return nil, err
	}
	out := &Certification{CanGenerate: canCertify(actor), Path: certPath(ch.Number)}
	if out.Generated, err = s.certVersions(ctx, tx, ch.ID); err != nil {
		return nil, err
	}
	if out.EvidenceMapped, err = s.certMapped(ctx, tx); err != nil {
		return nil, err
	}
	if version != "" {
		n, err := strconv.Atoi(version)
		if err != nil || n < 1 {
			return nil, engine.ErrNotFound
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `select snapshot from release_certifications where change_id = $1 and version = $2`, ch.ID, n).Scan(&raw); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, engine.ErrNotFound
			}
			return nil, err
		}
		if err := json.Unmarshal(raw, &out.CertData); err != nil {
			return nil, err
		}
		for i := range out.Generated {
			if out.Generated[i].Version == n {
				out.Version = &out.Generated[i]
			}
		}
		return out, nil
	}
	d, err := s.certData(ctx, tx, actor, ch)
	if err != nil {
		return nil, err
	}
	out.CertData = *d
	return out, nil
}

func (s *Service) certMapped(ctx context.Context, tx pgx.Tx) (bool, error) {
	if s.grc == nil {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from gate_evidence_map where source = $1)`, sourceCertification).Scan(&ok)
	return ok, err
}

// GenerateCertification congela i dati dal vivo in una nuova versione e registra l'evidenza.
// Gli elementi che chi genera non vede restano fuori anche dall'istantanea (solo il numero).
func (s *Service) GenerateCertification(ctx context.Context, tx pgx.Tx, actor engine.Actor, number string) (*Certification, error) {
	if !canCertify(actor) {
		return nil, engine.ErrForbidden
	}
	ch, err := findChange(ctx, tx, number)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/cert/"+ch.ID); err != nil {
		return nil, err
	}
	d, err := s.certData(ctx, tx, actor, ch)
	if err != nil {
		return nil, err
	}
	if len(d.Releases) == 0 {
		return nil, engine.Invalid("change", "il change %s non ha ancora nessun rilascio: non c'è niente da certificare", ch.Number)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var version int
	if err := tx.QueryRow(ctx, `select coalesce(max(version), 0) + 1 from release_certifications where change_id = $1`, ch.ID).Scan(&version); err != nil {
		return nil, err
	}
	id := ""
	if err := tx.QueryRow(ctx, `insert into release_certifications (tenant_id, change_id, version, snapshot, sha256, generated_by, generated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6) returning id::text`, ch.ID, version, raw, hash, nullable(actor.MemberID), d.At).Scan(&id); err != nil {
		return nil, err
	}
	if err := s.certEvidence(ctx, tx, actor, ch, d, id, version, hash); err != nil {
		return nil, err
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.certification", "release_certification", id, map[string]any{
		"change": ch.Number, "version": version, "sha256": hash, "releases": len(d.Releases), "items": len(d.Items)}); err != nil {
		return nil, err
	}
	return s.CertificationOf(ctx, tx, actor, ch.Number, strconv.Itoa(version))
}

// certEvidence registra l'evidenza GRC della certificazione sui controlli mappati sulla
// sorgente «certification» (Configurazione → Gate di rilascio). Senza mappatura, niente.
func (s *Service) certEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, ch *CertChange, d *CertData, id string, version int, hash string) error {
	if s.grc == nil {
		return nil
	}
	m, err := s.mapped(ctx, tx, []string{sourceCertification}, true)
	if err != nil || len(m[sourceCertification]) == 0 {
		return err
	}
	controls := []string{}
	for _, c := range m[sourceCertification] {
		controls = append(controls, c.key)
	}
	services := []string{}
	for _, r := range d.Releases {
		services = append(services, r.Service+" "+r.Version+" ("+r.Environment+")")
	}
	summary := fmt.Sprintf("Change %s, versione %d della certificazione. Rilasci: %s. Elementi: %d. SHA-256: %s.",
		ch.Number, version, strings.Join(services, ", "), len(d.Items), hash)
	in := grc.AutoEvidence{Collector: CollectorCertification, ExternalRef: fmt.Sprintf("cert:%s:%d", ch.ID, version),
		Title: fmt.Sprintf("Certificazione del rilascio %s (v%d)", ch.Number, version), Summary: summary,
		Controls: controls, OccurredAt: &d.At}
	if s.PublicURL != "" { // l'evidenza vuole un indirizzo assoluto
		in.URL = s.link(certPath(ch.Number) + "?versione=" + strconv.Itoa(version))
	}
	res, err := s.grc.RecordAutomatic(ctx, tx, sysGRC(actor), in)
	var ve *engine.ValidationError
	if errors.As(err, &ve) {
		return nil // conformità non configurata o controllo sparito: la certificazione resta valida
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update release_certifications set evidence_id = (
			select e.id from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			where sp.key || '-' || e.number = $2) where id = $1`, id, res.Key)
	return err
}
