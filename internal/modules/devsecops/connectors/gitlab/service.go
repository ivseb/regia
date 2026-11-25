// Package gitlab è il connettore GitLab (gitlab.com e self-managed) di DevSecOps, fase 5b
// (replica/devsecops.md §2 e §4). Legge da GitLab, con un profilo di pipeline, ciò che le
// pipeline producono e lo consegna al modulo devsecops come oggetti del contratto neutro
// (contract.Ingestor), lo stesso del connettore GitHub. Non scrive mai su GitLab.
//
//   - Integrazione (tabella integrations, kind gitlab): URL dell'istanza, gruppi e progetti,
//     token di gruppo o di progetto con lo scope read_api, intervallo, profilo, rete
//     interna. Il token è cifrato con la chiave applicativa e non torna mai (service.go).
//   - Scoperta dei progetti dei gruppi (anche dei sottogruppi) e proposta di associazione
//     alle applicazioni del catalogo, confermata da una persona (projects.go).
//   - Sincronizzazione incrementale: Release con link e artefatti dei job, pipeline e job
//     come check run, Environments e Deployments (sync.go, assets.go).
//   - Webhook in ingresso con X-Gitlab-Token e consegne deduplicate (webhook.go).
//   - Anteprima del profilo su un progetto (preview.go) e preset (preset.go).
//
// Condivide con il connettore GitHub le tabelle generiche dei connettori (connector_*),
// i profili di pipeline, la politica SSRF e la CA interna (ghapi.Policy, ghapi.NewHTTPClient).
package gitlab

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/gitlab/glapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Kind è il tipo dell'integrazione nella tabella integrations.
const Kind = "gitlab"

// Error è un errore da mostrare così com'è (stessa forma di quello del connettore GitHub).
type Error = ghconn.Error

// Actor è chi configura o avvia (amministratore del tenant).
type Actor = ghconn.Actor

func invalid(field, msg string) error {
	return &Error{Status: 422, Code: "validation", Field: field, Msg: msg}
}

var (
	ErrNotFound        = &Error{Status: 404, Code: "integration.not_found", Msg: "integrazione inesistente"}
	ErrProjectNotFound = &Error{Status: 404, Code: "project.not_found", Msg: "progetto inesistente"}
	ErrBusy            = &Error{Status: 409, Code: "sync.running", Msg: "una sincronizzazione di questa integrazione è già in corso"}
)

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Box *secretbox.Box
	// Ingestor salva gli oggetti del contratto (modulo devsecops); nil = nessuna archiviazione.
	Ingestor contract.Ingestor
	// Dev toglie la protezione SSRF (http e indirizzi locali: il finto GitLab di sviluppo).
	Dev bool
	// AllowPrivate è il consenso dell'operatore alle reti interne (REGIA_CONNECTOR_ALLOW_PRIVATE).
	AllowPrivate bool
	Log          *slog.Logger
	// Timeout di una risposta di GitLab (default 30 s).
	Timeout time.Duration
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s).
	MaxWait time.Duration
	// Sleep e Now si sostituiscono nei test.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}

// Service gestisce le integrazioni GitLab di tutti i tenant.
type Service struct {
	o       Options
	mu      sync.Mutex
	clients map[string]*http.Client
	wg      sync.WaitGroup
}

func New(o Options) *Service {
	if o.Box == nil {
		o.Box = secretbox.Random()
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Ingestor == nil {
		o.Ingestor = ghconn.DiscardIngestor{}
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, clients: map[string]*http.Client{}}
}

// AllowPrivate dice se l'operatore consente le reti interne.
func (s *Service) AllowPrivate() bool { return s.o.AllowPrivate || s.o.Dev }

// Dev dice se la protezione SSRF è spenta (modalità sviluppo).
func (s *Service) Dev() bool { return s.o.Dev }

// Storing dice se c'è un'archiviazione vera degli oggetti.
func (s *Service) Storing() bool {
	_, discard := s.o.Ingestor.(ghconn.DiscardIngestor)
	return !discard
}

// WaitIdle aspetta la fine delle sincronizzazioni avviate in background (test, arresto).
func (s *Service) WaitIdle() { s.wg.Wait() }

// ---------------------------------------------------------------------
// Configurazione
// ---------------------------------------------------------------------

// Config è la configurazione salvata in integrations.config (mai segreti).
type Config struct {
	BaseURL            string   `json:"base_url"`
	Groups             []string `json:"groups,omitempty"`
	Projects           []string `json:"projects,omitempty"`
	IntervalMinutes    int      `json:"interval_minutes"`
	ProfileID          string   `json:"profile_id,omitempty"`
	VerdictCheck       string   `json:"verdict_check,omitempty"`
	ReleaseEnvironment string   `json:"release_environment,omitempty"`
	ArtifactJobs       []string `json:"artifact_jobs,omitempty"`
	PrivateNetwork     bool     `json:"private_network,omitempty"`
	CACertificate      string   `json:"ca_certificate,omitempty"`
	IncludeArchived    bool     `json:"include_archived,omitempty"`
}

type secrets struct {
	Token         string `json:"token,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

func secretContext(id string) string { return "integration:" + id }

// Input crea o modifica un'integrazione. I segreti: assenti o vuoti = invariati.
type Input struct {
	Name               string   `json:"name"`
	Active             *bool    `json:"active"`
	BaseURL            string   `json:"base_url"`
	Groups             []string `json:"groups"`
	Projects           []string `json:"projects"`
	Token              string   `json:"token"`
	WebhookSecret      string   `json:"webhook_secret"`
	RemoveWebhook      bool     `json:"remove_webhook_secret"`
	IntervalMinutes    int      `json:"interval_minutes"`
	ProfileID          string   `json:"profile_id"`
	VerdictCheck       string   `json:"verdict_check"`
	ReleaseEnvironment string   `json:"release_environment"`
	ArtifactJobs       []string `json:"artifact_jobs"`
	PrivateNetwork     bool     `json:"private_network"`
	CACertificate      string   `json:"ca_certificate"`
	IncludeArchived    bool     `json:"include_archived"`
}

var (
	pathRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_.][A-Za-z0-9_.-]*)*$`)
	envRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)
)

func (s *Service) policy(c Config) ghapi.Policy {
	switch {
	case s.o.Dev:
		return ghapi.Any
	case c.PrivateNetwork && s.o.AllowPrivate:
		return ghapi.Private
	}
	return ghapi.Public
}

// splitList separa su virgole, spazi e a capo, senza doppioni.
func splitList(in []string, fold bool) []string {
	var out []string
	for _, o := range in {
		for _, part := range strings.FieldsFunc(o, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
			part = strings.Trim(part, "/")
			if part != "" && !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, part) }) {
				out = append(out, part)
			}
		}
	}
	return out
}

func (s *Service) clean(in *Input, prev secrets) (Config, secrets, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return Config{}, prev, invalid("name", "serve un nome (al massimo 80 caratteri)")
	}
	c := Config{IntervalMinutes: in.IntervalMinutes, ProfileID: strings.TrimSpace(in.ProfileID), VerdictCheck: strings.TrimSpace(in.VerdictCheck),
		ReleaseEnvironment: strings.TrimSpace(in.ReleaseEnvironment), PrivateNetwork: in.PrivateNetwork,
		CACertificate: strings.TrimSpace(in.CACertificate), IncludeArchived: in.IncludeArchived}
	if c.PrivateNetwork && !s.AllowPrivate() {
		return c, prev, invalid("private_network", "l'operatore non ha consentito le reti interne ai connettori (REGIA_CONNECTOR_ALLOW_PRIVATE)")
	}
	base, err := glapi.NormalizeBaseURL(in.BaseURL, s.policy(c))
	if err != nil {
		return c, prev, &Error{Status: 422, Code: "validation", Field: "base_url", Msg: err.Error()}
	}
	c.BaseURL = base
	c.Groups, c.Projects = splitList(in.Groups, false), splitList(in.Projects, false)
	for _, g := range c.Groups {
		if len(g) > 400 || !pathRe.MatchString(g) {
			return c, prev, invalid("groups", "percorso di gruppo non valido: "+g+" (per esempio acme o acme/piattaforma)")
		}
	}
	for _, p := range c.Projects {
		if len(p) > 400 || !pathRe.MatchString(p) || !strings.Contains(p, "/") {
			return c, prev, invalid("projects", "percorso di progetto non valido: "+p+" (serve gruppo/progetto)")
		}
	}
	if len(c.Groups)+len(c.Projects) == 0 || len(c.Groups) > 20 || len(c.Projects) > 100 {
		return c, prev, invalid("groups", "indica da 1 a 20 gruppi, oppure i progetti (fino a 100) per un token di progetto")
	}
	if c.IntervalMinutes == 0 {
		c.IntervalMinutes = 30
	}
	if c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
		return c, prev, invalid("interval_minutes", "intervallo tra 5 minuti e 24 ore")
	}
	if c.ReleaseEnvironment != "" && !envRe.MatchString(c.ReleaseEnvironment) {
		return c, prev, invalid("release_environment", "nome di ambiente non valido (minuscole, cifre, - _ .)")
	}
	if len(c.VerdictCheck) > 100 {
		return c, prev, invalid("verdict_check", "nome del job troppo lungo")
	}
	c.ArtifactJobs = splitList(in.ArtifactJobs, false)
	if len(c.ArtifactJobs) > 20 {
		return c, prev, invalid("artifact_jobs", "al massimo 20 modelli di job")
	}
	if c.CACertificate != "" {
		if len(c.CACertificate) > 20000 {
			return c, prev, invalid("ca_certificate", "certificato troppo lungo")
		}
		if _, err := ghapi.NewHTTPClient(ghapi.Public, c.CACertificate, time.Second); err != nil {
			return c, prev, invalid("ca_certificate", err.Error())
		}
	}
	if c.ProfileID != "" && !validID(c.ProfileID) {
		return c, prev, invalid("profile_id", "profilo inesistente")
	}
	sec := prev
	if t := strings.TrimSpace(in.Token); t != "" {
		if len(t) > 500 || strings.ContainsAny(t, " \n\t") {
			return c, prev, invalid("token", "token non valido")
		}
		sec.Token = t
	}
	if sec.Token == "" {
		return c, prev, invalid("token", "serve il token di gruppo o di progetto (scope read_api)")
	}
	if w := in.WebhookSecret; w != "" {
		if len(w) < 16 || len(w) > 200 {
			return c, prev, invalid("webhook_secret", "il segreto del webhook va da 16 a 200 caratteri")
		}
		sec.WebhookSecret = w
	}
	if in.RemoveWebhook {
		sec.WebhookSecret = ""
	}
	return c, sec, nil
}

// Integration è un'integrazione come appare nella configurazione (mai i segreti).
type Integration struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Active           bool              `json:"active"`
	Config           Config            `json:"config"`
	HasToken         bool              `json:"has_token"`
	HasWebhookSecret bool              `json:"has_webhook_secret"`
	WebhookPath      string            `json:"webhook_path"`
	ProfileName      *string           `json:"profile_name"`
	ProfileVersion   *int              `json:"profile_version"`
	Projects         ghconn.RepoCounts `json:"projects"`
	Sync             SyncState         `json:"sync"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// SyncState è lo stato della sincronizzazione di un'integrazione.
type SyncState struct {
	NextRunAt      *time.Time       `json:"next_run_at"`
	Running        bool             `json:"running"`
	LastStartedAt  *time.Time       `json:"last_started_at"`
	LastFinishedAt *time.Time       `json:"last_finished_at"`
	LastStatus     *string          `json:"last_status"`
	LastError      *string          `json:"last_error"`
	RateLimit      *ghapi.RateLimit `json:"rate_limit"`
}

func audited(ctx context.Context, tx pgx.Tx, a Actor, action, entityType, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: id, Payload: payload,
		ActorAccountID: a.AccountID, ActorMemberID: a.MemberID, IP: a.IP})
}

const selectIntegration = `
	select i.id, i.name, i.active, i.config, i.secret_enc, i.created_at, i.updated_at,
	  p.name, p.current_version,
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'confirmed'),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'suggested'),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'ignored'),
	  st.next_run_at, coalesce(st.lease_until > now(), false), st.last_started_at, st.last_finished_at, st.last_status, st.last_error, st.rate_limit
	from integrations i
	left join pipeline_profiles p on p.tenant_id = i.tenant_id and p.id = nullif(i.config->>'profile_id', '')::uuid
	left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
	where i.kind = 'gitlab'`

func (s *Service) scanIntegration(row pgx.Row) (*Integration, secrets, error) {
	var it Integration
	var cfg, sealed, rate []byte
	err := row.Scan(&it.ID, &it.Name, &it.Active, &cfg, &sealed, &it.CreatedAt, &it.UpdatedAt, &it.ProfileName, &it.ProfileVersion,
		&it.Projects.Total, &it.Projects.Confirmed, &it.Projects.Suggested, &it.Projects.Ignored,
		&it.Sync.NextRunAt, &it.Sync.Running, &it.Sync.LastStartedAt, &it.Sync.LastFinishedAt, &it.Sync.LastStatus, &it.Sync.LastError, &rate)
	if err != nil {
		return nil, secrets{}, err
	}
	_ = json.Unmarshal(cfg, &it.Config)
	if len(rate) > 0 {
		var r ghapi.RateLimit
		if json.Unmarshal(rate, &r) == nil {
			it.Sync.RateLimit = &r
		}
	}
	var sec secrets
	if len(sealed) > 0 {
		plain, err := s.o.Box.Open(sealed, secretContext(it.ID))
		if err != nil {
			return nil, secrets{}, fmt.Errorf("segreti dell'integrazione %s: %w", it.ID, err)
		}
		_ = json.Unmarshal(plain, &sec)
	}
	it.HasToken, it.HasWebhookSecret = sec.Token != "", sec.WebhookSecret != ""
	it.WebhookPath = "/api/v1/devsecops/gitlab/webhook/" + it.ID
	return &it, sec, nil
}

// List elenca le integrazioni GitLab del tenant.
func (s *Service) List(ctx context.Context, tenantID string) ([]Integration, error) {
	out := []Integration{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectIntegration+" order by lower(i.name), i.created_at")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it, _, err := s.scanIntegration(rows)
			if err != nil {
				return err
			}
			out = append(out, *it)
		}
		return rows.Err()
	})
	return out, err
}

// Get restituisce un'integrazione.
func (s *Service) Get(ctx context.Context, tenantID, id string) (*Integration, error) {
	var it *Integration
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		it, _, err = s.load(ctx, tx, id)
		return err
	})
	return it, err
}

func (s *Service) load(ctx context.Context, tx pgx.Tx, id string) (*Integration, secrets, error) {
	if !validID(id) {
		return nil, secrets{}, ErrNotFound
	}
	it, sec, err := s.scanIntegration(tx.QueryRow(ctx, selectIntegration+" and i.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, secrets{}, ErrNotFound
	}
	return it, sec, err
}

func (s *Service) checkProfile(ctx context.Context, tx pgx.Tx, id string) error {
	if id == "" {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from pipeline_profiles where id = $1)", id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid("profile_id", "profilo inesistente")
	}
	return nil
}

func (s *Service) seal(id string, sec secrets) ([]byte, error) {
	b, _ := json.Marshal(sec)
	return s.o.Box.Seal(b, secretContext(id))
}

// Create crea un'integrazione GitLab.
func (s *Service) Create(ctx context.Context, a Actor, in Input) (string, error) {
	c, sec, err := s.clean(&in, secrets{})
	if err != nil {
		return "", err
	}
	id := db.NewID()
	sealed, err := s.seal(id, sec)
	if err != nil {
		return "", err
	}
	active := in.Active == nil || *in.Active
	cfg, _ := json.Marshal(c)
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from integrations where kind = 'gitlab'").Scan(&n); err != nil {
			return err
		}
		if n >= 20 {
			return invalid("name", "al massimo 20 integrazioni GitLab per organizzazione")
		}
		if err := s.checkProfile(ctx, tx, c.ProfileID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into integrations (tenant_id, id, kind, name, config, secret_enc, active)
			values (app_tenant(), $1, 'gitlab', $2, $3, $4, $5)`, id, in.Name, cfg, sealed, active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into connector_sync_state (tenant_id, integration_id) values (app_tenant(), $1)`, id); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.gitlab.create", "integration", id, auditConfig(c, sec))
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func auditConfig(c Config, sec secrets) map[string]any {
	return map[string]any{"base_url": c.BaseURL, "groups": c.Groups, "projects": c.Projects, "interval_minutes": c.IntervalMinutes,
		"profile_id": c.ProfileID, "private_network": c.PrivateNetwork, "webhook": sec.WebhookSecret != "", "ca": c.CACertificate != ""}
}

// Update modifica un'integrazione; i segreti non indicati restano.
func (s *Service) Update(ctx context.Context, a Actor, id string, in Input) error {
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cur, prev, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		c, sec, err := s.clean(&in, prev)
		if err != nil {
			return err
		}
		if err := s.checkProfile(ctx, tx, c.ProfileID); err != nil {
			return err
		}
		sealed, err := s.seal(id, sec)
		if err != nil {
			return err
		}
		active := cur.Active
		if in.Active != nil {
			active = *in.Active
		}
		cfg, _ := json.Marshal(c)
		if _, err := tx.Exec(ctx, `update integrations set name = $2, config = $3, secret_enc = $4, active = $5, updated_at = now() where id = $1`,
			id, in.Name, cfg, sealed, active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update connector_sync_state set next_run_at = least(next_run_at, now() + make_interval(mins => $2))
			where integration_id = $1`, id, c.IntervalMinutes); err != nil {
			return err
		}
		p := auditConfig(c, sec)
		p["active"] = active
		p["secrets_changed"] = in.Token != "" || in.WebhookSecret != "" || in.RemoveWebhook
		return audited(ctx, tx, a, "devsecops.gitlab.update", "integration", id, p)
	})
}

// Delete cancella un'integrazione con i suoi progetti e lo stato (gli oggetti già
// consegnati al modulo devsecops restano: sono storia).
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from integrations where id = $1 and kind = 'gitlab'", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "devsecops.gitlab.delete", "integration", id, nil)
	})
}

// GenerateWebhookSecret crea un nuovo segreto del webhook e lo restituisce una volta.
func (s *Service) GenerateWebhookSecret(ctx context.Context, a Actor, id string) (string, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret := "glwh_" + base64.RawURLEncoding.EncodeToString(b)
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, prev, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		prev.WebhookSecret = secret
		sealed, err := s.seal(id, prev)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update integrations set secret_enc = $2, updated_at = now() where id = $1", id, sealed); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.gitlab.webhook_secret", "integration", id, nil)
	})
	return secret, err
}

// ---------------------------------------------------------------------
// Client e prova di connessione
// ---------------------------------------------------------------------

func (s *Service) httpClient(p ghapi.Policy, ca string) (*http.Client, error) {
	h := sha256.Sum256([]byte(ca))
	key := p.String() + "|" + hex.EncodeToString(h[:8])
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[key]; ok {
		return c, nil
	}
	c, err := ghapi.NewHTTPClient(p, ca, s.o.Timeout)
	if err != nil {
		return nil, err
	}
	s.clients[key] = c
	return c, nil
}

func (s *Service) client(c Config, sec secrets) (*glapi.Client, error) {
	hc, err := s.httpClient(s.policy(c), c.CACertificate)
	if err != nil {
		return nil, err
	}
	return glapi.New(glapi.Options{BaseURL: c.BaseURL, Token: sec.Token, HTTP: hc, MaxWait: s.o.MaxWait, Sleep: s.o.Sleep, Now: s.o.Now})
}

// TestResult è l'esito della prova di connessione.
type TestResult struct {
	OK        bool             `json:"ok"`
	Message   string           `json:"message"`
	Details   []string         `json:"details"`
	Warnings  []string         `json:"warnings"`
	Server    string           `json:"server"`
	Scopes    []string         `json:"scopes"`
	RateLimit *ghapi.RateLimit `json:"rate_limit,omitempty"`
}

// scopi che Regia usa: in sola lettura. Tutti gli altri sono in eccesso.
var (
	neededScopes  = []string{"read_api"}
	allowedScopes = []string{"read_api", "read_repository", "read_user", "read_registry", "read_virtual_registry"}
)

// Test prova la connessione con la configurazione salvata.
func (s *Service) Test(ctx context.Context, a Actor, id string) (*TestResult, error) {
	var it *Integration
	var sec secrets
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		it, sec, err = s.load(ctx, tx, id)
		return err
	}); err != nil {
		return nil, err
	}
	res := &TestResult{Details: []string{}, Warnings: []string{}, Scopes: []string{}}
	fail := func(err error) (*TestResult, error) {
		res.OK = false
		res.Message = "Connessione non riuscita: " + err.Error()
		return s.recordTest(ctx, a, id, res)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cl, err := s.client(it.Config, sec)
	if err != nil {
		return fail(err)
	}
	ver, err := cl.Version(ctx)
	if err != nil {
		return fail(err)
	}
	res.Server = "GitLab " + ver.Version
	if it.Config.BaseURL == "https://gitlab.com/api/v4" {
		res.Server = "gitlab.com"
	}
	who := "con un token"
	if t, err := cl.TokenSelf(ctx); err == nil {
		who = fmt.Sprintf("con il token «%s»", t.Name)
		res.Scopes = t.Scopes
		if t.Revoked || (t.Active != nil && !*t.Active) {
			return fail(errors.New("il token è revocato o non più attivo"))
		}
		if len(res.Scopes) == 0 {
			res.Scopes = []string{}
		}
		var missing, extra []string
		for _, n := range neededScopes {
			if !slices.Contains(t.Scopes, n) && !slices.Contains(t.Scopes, "api") {
				missing = append(missing, n)
			}
		}
		for _, sc := range t.Scopes {
			if !slices.Contains(allowedScopes, sc) {
				extra = append(extra, sc)
			}
		}
		if len(missing) > 0 {
			return fail(fmt.Errorf("al token manca lo scope %s: senza non si leggono progetti, pipeline e Release. Crea un token di gruppo (ruolo Reporter) con lo scope read_api", strings.Join(missing, ", ")))
		}
		if len(extra) > 0 {
			res.Warnings = append(res.Warnings, "Il token ha scope che Regia non usa: "+strings.Join(extra, ", ")+
				". Basta read_api: un token con scope di scrittura è un rischio inutile.")
		}
		if t.ExpiresAt != "" {
			if exp, err := time.Parse("2006-01-02", t.ExpiresAt); err == nil {
				left := exp.Sub(s.o.Now())
				switch {
				case left < 0:
					return fail(errors.New("il token è scaduto il " + exp.Format("02/01/2006")))
				case left < 14*24*time.Hour:
					res.Warnings = append(res.Warnings, fmt.Sprintf("Il token scade il %s: va rinnovato prima, altrimenti la sincronizzazione si ferma.", exp.Format("02/01/2006")))
				}
			}
		} else {
			res.Details = append(res.Details, "Il token non ha scadenza.")
		}
	} else if e := (*glapi.Error)(nil); errors.As(err, &e) && (e.Status == 404 || e.Status == 403) {
		res.Warnings = append(res.Warnings, "Gli scope del token non si possono verificare (GitLab precedente alla 15.5, o un token che non si può ispezionare): controlla a mano che sia read_api.")
	} else {
		return fail(err)
	}
	var reached []string
	for _, g := range it.Config.Groups {
		gr, err := cl.Group(ctx, g)
		if err != nil {
			return fail(fmt.Errorf("gruppo «%s»: %w", g, err))
		}
		reached = append(reached, gr.FullPath)
	}
	var projects []string
	for _, p := range it.Config.Projects {
		pr, err := cl.Project(ctx, p)
		if err != nil {
			return fail(fmt.Errorf("progetto «%s»: %w", p, err))
		}
		projects = append(projects, pr.PathWithNamespace)
	}
	res.OK = true
	var what []string
	if len(reached) > 0 {
		what = append(what, pluralize(len(reached), "Gruppo raggiungibile", "Gruppi raggiungibili")+": "+strings.Join(reached, ", "))
	}
	if len(projects) > 0 {
		what = append(what, pluralize(len(projects), "Progetto raggiungibile", "Progetti raggiungibili")+": "+strings.Join(projects, ", "))
	}
	res.Message = fmt.Sprintf("Connessione riuscita con %s %s. %s.", res.Server, who, strings.Join(what, ". "))
	if r := cl.Rate(); r.Limit > 0 {
		res.RateLimit = &r
		res.Details = append(res.Details, fmt.Sprintf("Limite di velocità: %d richieste rimaste su %d.", r.Remaining, r.Limit))
	}
	return s.recordTest(ctx, a, id, res)
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *Service) recordTest(ctx context.Context, a Actor, id string, r *TestResult) (*TestResult, error) {
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.gitlab.test", "integration", id, map[string]any{"ok": r.OK})
	})
	return r, err
}

func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

func systemActor(a Actor) engine.Actor {
	return engine.Actor{AccountID: a.AccountID, MemberID: a.MemberID, IP: netip.Addr(a.IP), Permissions: []string{"*"}}
}

// definitionFor legge la definizione corrente del profilo di un'integrazione (nil se
// l'integrazione non ha profilo).
func definitionFor(ctx context.Context, tx pgx.Tx, profileID string) (*profile.Definition, int, error) {
	if profileID == "" {
		return nil, 0, nil
	}
	var raw []byte
	var version int
	err := tx.QueryRow(ctx, `select v.definition, v.version from pipeline_profiles p
		join pipeline_profile_versions v on v.tenant_id = p.tenant_id and v.profile_id = p.id and v.version = p.current_version
		where p.id = $1`, profileID).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	def, err := profile.Parse(raw)
	if err != nil {
		return nil, 0, err
	}
	return def, version, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
