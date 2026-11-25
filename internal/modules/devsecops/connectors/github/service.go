// Package github è il connettore GitHub e GitHub Enterprise Server di DevSecOps, fase 1
// (replica/devsecops.md §2 e §4). Legge da GitHub, con il profilo di pipeline, ciò che le
// pipeline producono e lo consegna al modulo devsecops come oggetti del contratto neutro
// (contract.Ingestor). Non scrive mai su GitHub.
//
//   - Integrazione (tabella integrations, kind github): URL base, organizzazioni, GitHub
//     App o token, intervallo, profilo, rete interna. Segreti cifrati con la chiave
//     applicativa, mai restituiti (service.go).
//   - Scoperta dei repository e proposta di associazione alle applicazioni del catalogo,
//     confermata da interfaccia (repos.go, match.go).
//   - Sincronizzazione incrementale con cursori per repository, job in-process e
//     "Sincronizza ora", limiti di velocità rispettati (sync.go).
//   - Profili di pipeline versionati, export e import, anteprima (profiles.go, preview.go).
//   - Webhook in ingresso firmati che avviano sincronizzazioni mirate (webhook.go).
package github

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
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Kind è il tipo dell'integrazione nella tabella integrations.
const Kind = "github"

// Error è un errore da mostrare così com'è.
type Error struct {
	Status int
	Code   string
	Field  string
	Msg    string
	// Problems sono gli errori di validazione di un profilo.
	Problems []profile.Problem
}

func (e *Error) Error() string { return e.Msg }

func invalid(field, msg string) error {
	return &Error{Status: 422, Code: "validation", Field: field, Msg: msg}
}

var (
	ErrNotFound     = &Error{Status: 404, Code: "integration.not_found", Msg: "integrazione inesistente"}
	ErrRepoNotFound = &Error{Status: 404, Code: "repository.not_found", Msg: "repository inesistente"}
	ErrBusy         = &Error{Status: 409, Code: "sync.running", Msg: "una sincronizzazione di questa integrazione è già in corso"}
)

// Actor è chi configura o avvia (amministratore del tenant).
type Actor struct {
	TenantID  string
	AccountID string
	MemberID  string
	IP        netip.Addr
}

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Box *secretbox.Box
	// Ingestor salva gli oggetti del contratto (modulo devsecops); nil = DiscardIngestor.
	Ingestor contract.Ingestor
	// Dev toglie la protezione SSRF (http e indirizzi locali: il finto GitHub di sviluppo).
	Dev bool
	// AllowPrivate è il consenso dell'operatore alle reti interne
	// (REGIA_CONNECTOR_ALLOW_PRIVATE): senza, l'opzione «Rete interna» delle
	// integrazioni non si può attivare.
	AllowPrivate bool
	Log          *slog.Logger
	// Timeout di una risposta di GitHub (default 30 s).
	Timeout time.Duration
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s).
	MaxWait time.Duration
	// Sleep e Now si sostituiscono nei test.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}

// Service gestisce le integrazioni GitHub di tutti i tenant.
type Service struct {
	o       Options
	tokens  *ghapi.TokenCache
	mu      sync.Mutex
	clients map[string]*http.Client // politica + CA → client HTTP condiviso
	caps    map[string]capEntry     // integrazione → può aprire pull request (adopt.go)
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
		o.Ingestor = DiscardIngestor{}
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, tokens: ghapi.NewTokenCache(), clients: map[string]*http.Client{}, caps: map[string]capEntry{}}
}

// AllowPrivate dice se l'operatore consente le reti interne.
func (s *Service) AllowPrivate() bool { return s.o.AllowPrivate || s.o.Dev }

// Dev dice se la protezione SSRF è spenta (modalità sviluppo).
func (s *Service) Dev() bool { return s.o.Dev }

// Storing dice se c'è un'archiviazione vera degli oggetti (non DiscardIngestor).
func (s *Service) Storing() bool {
	_, discard := s.o.Ingestor.(DiscardIngestor)
	return !discard
}

// TokenCache è la cache dei token di installazione (per i test).
func (s *Service) TokenCache() *ghapi.TokenCache { return s.tokens }

// WaitIdle aspetta la fine delle sincronizzazioni avviate in background (test, arresto).
func (s *Service) WaitIdle() { s.wg.Wait() }

// ---------------------------------------------------------------------
// Configurazione
// ---------------------------------------------------------------------

// Config è la configurazione salvata in integrations.config (mai segreti).
type Config struct {
	BaseURL            string   `json:"base_url"`
	Owners             []string `json:"owners"`
	AuthMode           string   `json:"auth_mode"` // app | token
	AppID              string   `json:"app_id,omitempty"`
	InstallationID     string   `json:"installation_id,omitempty"`
	IntervalMinutes    int      `json:"interval_minutes"`
	ProfileID          string   `json:"profile_id,omitempty"`
	VerdictCheck       string   `json:"verdict_check,omitempty"`
	ReleaseEnvironment string   `json:"release_environment,omitempty"`
	GitOpsRepositories []string `json:"gitops_repositories,omitempty"`
	PrivateNetwork     bool     `json:"private_network,omitempty"`
	CACertificate      string   `json:"ca_certificate,omitempty"`
	IncludeArchived    bool     `json:"include_archived,omitempty"`
}

// secrets sono i segreti, cifrati insieme in integrations.secret_enc.
type secrets struct {
	Token         string `json:"token,omitempty"`
	PrivateKey    string `json:"private_key,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

func secretContext(id string) string { return "integration:" + id }

// Input crea o modifica un'integrazione. I segreti: assenti o vuoti = invariati.
type Input struct {
	Name               string   `json:"name"`
	Active             *bool    `json:"active"`
	BaseURL            string   `json:"base_url"`
	Owners             []string `json:"owners"`
	AuthMode           string   `json:"auth_mode"`
	AppID              string   `json:"app_id"`
	InstallationID     string   `json:"installation_id"`
	Token              string   `json:"token"`
	PrivateKey         string   `json:"private_key"`
	WebhookSecret      string   `json:"webhook_secret"`
	RemoveWebhook      bool     `json:"remove_webhook_secret"`
	IntervalMinutes    int      `json:"interval_minutes"`
	ProfileID          string   `json:"profile_id"`
	VerdictCheck       string   `json:"verdict_check"`
	ReleaseEnvironment string   `json:"release_environment"`
	GitOpsRepositories []string `json:"gitops_repositories"`
	PrivateNetwork     bool     `json:"private_network"`
	CACertificate      string   `json:"ca_certificate"`
	IncludeArchived    bool     `json:"include_archived"`
}

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	numRe   = regexp.MustCompile(`^[0-9]{1,20}$`)
	envRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)
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

// clean valida l'input e prepara configurazione e segreti (prev: quelli salvati).
func (s *Service) clean(in *Input, prev secrets) (Config, secrets, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return Config{}, prev, invalid("name", "serve un nome (al massimo 80 caratteri)")
	}
	c := Config{AuthMode: in.AuthMode, AppID: strings.TrimSpace(in.AppID), InstallationID: strings.TrimSpace(in.InstallationID),
		IntervalMinutes: in.IntervalMinutes, ProfileID: strings.TrimSpace(in.ProfileID), VerdictCheck: strings.TrimSpace(in.VerdictCheck),
		ReleaseEnvironment: strings.TrimSpace(in.ReleaseEnvironment), PrivateNetwork: in.PrivateNetwork,
		CACertificate: strings.TrimSpace(in.CACertificate), IncludeArchived: in.IncludeArchived}
	if c.PrivateNetwork && !s.AllowPrivate() {
		return c, prev, invalid("private_network", "l'operatore non ha consentito le reti interne ai connettori (REGIA_CONNECTOR_ALLOW_PRIVATE)")
	}
	base, err := ghapi.NormalizeBaseURL(in.BaseURL, s.policy(c))
	if err != nil {
		return c, prev, &Error{Status: 422, Code: "validation", Field: "base_url", Msg: err.Error()}
	}
	c.BaseURL = base
	for _, o := range in.Owners {
		for _, part := range strings.FieldsFunc(o, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
			if !ownerRe.MatchString(part) {
				return c, prev, invalid("owners", "nome di organizzazione non valido: "+part)
			}
			if !slices.ContainsFunc(c.Owners, func(x string) bool { return strings.EqualFold(x, part) }) {
				c.Owners = append(c.Owners, part)
			}
		}
	}
	if len(c.Owners) == 0 || len(c.Owners) > 20 {
		return c, prev, invalid("owners", "indica da 1 a 20 organizzazioni da leggere")
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
		return c, prev, invalid("verdict_check", "nome del controllo troppo lungo")
	}
	for _, r := range in.GitOpsRepositories {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		o, n, ok := strings.Cut(r, "/")
		if !ok || !ownerRe.MatchString(o) || n == "" || strings.ContainsAny(n, "/ ") || len(n) > 100 {
			return c, prev, invalid("gitops_repositories", "repository GitOps non valido: "+r+" (serve organizzazione/nome)")
		}
		if !slices.Contains(c.GitOpsRepositories, r) {
			c.GitOpsRepositories = append(c.GitOpsRepositories, r)
		}
	}
	if len(c.GitOpsRepositories) > 20 {
		return c, prev, invalid("gitops_repositories", "al massimo 20 repository GitOps")
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
	if k := strings.TrimSpace(in.PrivateKey); k != "" {
		if len(k) > 20000 {
			return c, prev, invalid("private_key", "chiave troppo lunga")
		}
		if _, err := ghapi.ParsePrivateKey(k); err != nil {
			return c, prev, invalid("private_key", err.Error())
		}
		sec.PrivateKey = k + "\n"
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
	switch c.AuthMode {
	case "app":
		if !numRe.MatchString(c.AppID) {
			return c, prev, invalid("app_id", "serve l'App ID (un numero, nella pagina della App)")
		}
		if !numRe.MatchString(c.InstallationID) {
			return c, prev, invalid("installation_id", "serve l'installation ID (un numero, nell'URL dell'installazione)")
		}
		if sec.PrivateKey == "" {
			return c, prev, invalid("private_key", "serve la chiave privata della App (file .pem)")
		}
		sec.Token = "" // un segreto che non si usa non si conserva
	case "token":
		c.AppID, c.InstallationID = "", ""
		if sec.Token == "" {
			return c, prev, invalid("token", "serve il token")
		}
		sec.PrivateKey = ""
	default:
		return c, prev, invalid("auth_mode", "scegli GitHub App o token")
	}
	return c, sec, nil
}

// Integration è un'integrazione come appare nella configurazione (mai i segreti).
type Integration struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Active           bool       `json:"active"`
	Config           Config     `json:"config"`
	HasToken         bool       `json:"has_token"`
	HasPrivateKey    bool       `json:"has_private_key"`
	HasWebhookSecret bool       `json:"has_webhook_secret"`
	WebhookPath      string     `json:"webhook_path"`
	ProfileName      *string    `json:"profile_name"`
	ProfileVersion   *int       `json:"profile_version"`
	Repositories     RepoCounts `json:"repositories"`
	Sync             SyncState  `json:"sync"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// RepoCounts riassume i repository scoperti.
type RepoCounts struct {
	Total     int `json:"total"`
	Confirmed int `json:"confirmed"`
	Suggested int `json:"suggested"`
	Ignored   int `json:"ignored"`
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
	where i.kind = 'github'`

func (s *Service) scanIntegration(row pgx.Row) (*Integration, secrets, error) {
	var it Integration
	var cfg []byte
	var sealed []byte
	var rate []byte
	err := row.Scan(&it.ID, &it.Name, &it.Active, &cfg, &sealed, &it.CreatedAt, &it.UpdatedAt, &it.ProfileName, &it.ProfileVersion,
		&it.Repositories.Total, &it.Repositories.Confirmed, &it.Repositories.Suggested, &it.Repositories.Ignored,
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
	it.HasToken, it.HasPrivateKey, it.HasWebhookSecret = sec.Token != "", sec.PrivateKey != "", sec.WebhookSecret != ""
	it.WebhookPath = "/api/v1/devsecops/github/webhook/" + it.ID
	return &it, sec, nil
}

// List elenca le integrazioni GitHub del tenant.
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

// Create crea un'integrazione GitHub.
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
		if err := tx.QueryRow(ctx, "select count(*) from integrations where kind = 'github'").Scan(&n); err != nil {
			return err
		}
		if n >= 20 {
			return invalid("name", "al massimo 20 integrazioni GitHub per organizzazione")
		}
		if err := s.checkProfile(ctx, tx, c.ProfileID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into integrations (tenant_id, id, kind, name, config, secret_enc, active)
			values (app_tenant(), $1, 'github', $2, $3, $4, $5)`, id, in.Name, cfg, sealed, active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into connector_sync_state (tenant_id, integration_id) values (app_tenant(), $1)`, id); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.github.create", "integration", id, auditConfig(c, sec))
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func auditConfig(c Config, sec secrets) map[string]any {
	return map[string]any{"base_url": c.BaseURL, "owners": c.Owners, "auth_mode": c.AuthMode, "app_id": c.AppID,
		"installation_id": c.InstallationID, "interval_minutes": c.IntervalMinutes, "profile_id": c.ProfileID,
		"private_network": c.PrivateNetwork, "webhook": sec.WebhookSecret != "", "ca": c.CACertificate != ""}
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
		// Un nuovo intervallo vale dalla prossima esecuzione.
		if _, err := tx.Exec(ctx, `update connector_sync_state set next_run_at = least(next_run_at, now() + make_interval(mins => $2))
			where integration_id = $1`, id, c.IntervalMinutes); err != nil {
			return err
		}
		s.tokens.Forget(id)
		p := auditConfig(c, sec)
		p["active"] = active
		p["secrets_changed"] = in.Token != "" || in.PrivateKey != "" || in.WebhookSecret != "" || in.RemoveWebhook
		return audited(ctx, tx, a, "devsecops.github.update", "integration", id, p)
	})
}

// Delete cancella un'integrazione con i suoi repository e lo stato (gli oggetti già
// consegnati al modulo devsecops restano: sono storia).
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from integrations where id = $1 and kind = 'github'", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "devsecops.github.delete", "integration", id, nil)
	})
	if err == nil {
		s.tokens.Forget(id)
	}
	return err
}

// GenerateWebhookSecret crea un nuovo segreto del webhook e lo restituisce una volta.
func (s *Service) GenerateWebhookSecret(ctx context.Context, a Actor, id string) (string, error) {
	secret := randomSecret()
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
		return audited(ctx, tx, a, "devsecops.github.webhook_secret", "integration", id, nil)
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

func (s *Service) client(id string, c Config, sec secrets) (*ghapi.Client, error) {
	hc, err := s.httpClient(s.policy(c), c.CACertificate)
	if err != nil {
		return nil, err
	}
	return ghapi.New(ghapi.Options{
		BaseURL: c.BaseURL, HTTP: hc, Tokens: s.tokens, CacheKey: id, MaxWait: s.o.MaxWait, Sleep: s.o.Sleep, Now: s.o.Now,
		Auth: ghapi.Auth{Mode: c.AuthMode, Token: sec.Token, AppID: c.AppID, InstallationID: c.InstallationID, PrivateKeyPEM: sec.PrivateKey},
	})
}

// TestResult è l'esito della prova di connessione.
type TestResult struct {
	OK        bool             `json:"ok"`
	Message   string           `json:"message"`
	Details   []string         `json:"details"`
	Warnings  []string         `json:"warnings"`
	Server    string           `json:"server"`
	RateLimit *ghapi.RateLimit `json:"rate_limit,omitempty"`
}

// requiredPermissions sono i permessi minimi (in sola lettura) della GitHub App.
var requiredPermissions = []string{"contents", "metadata", "deployments", "checks"}

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
	res := &TestResult{Details: []string{}, Warnings: []string{}}
	fail := func(err error) *TestResult {
		res.OK = false
		res.Message = "Connessione non riuscita: " + err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cl, err := s.client(id, it.Config, sec)
	if err != nil {
		return s.recordTest(ctx, a, id, fail(err))
	}
	who := "con un token"
	if it.Config.AuthMode == "app" {
		app, err := cl.App(ctx)
		if err != nil {
			return s.recordTest(ctx, a, id, fail(err))
		}
		who = fmt.Sprintf("come GitHub App «%s»", app.Name)
		inst, err := cl.Installation(ctx)
		if err != nil {
			return s.recordTest(ctx, a, id, fail(fmt.Errorf("installazione %s non trovata: %w", it.Config.InstallationID, err)))
		}
		res.Details = append(res.Details, fmt.Sprintf("Installazione %d su %s (%s).", inst.ID, inst.Account.Login, selection(inst.RepositorySelection)))
		var missing []string
		for _, p := range requiredPermissions {
			if inst.Permissions[p] == "" {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			res.Warnings = append(res.Warnings, "Permessi mancanti nella App: "+strings.Join(missing, ", ")+" (in sola lettura).")
		}
		if ghapi.HasWrite(inst.Permissions) {
			res.Details = append(res.Details, "Adozione guidata: la App può aprire pull request («Contents» e «Pull requests» in scrittura).")
		} else {
			res.Details = append(res.Details, "Adozione guidata: senza i permessi facoltativi «Contents» e «Pull requests» in scrittura i file si scaricano invece di aprire una pull request.")
		}
		var write []string
		for p, v := range inst.Permissions {
			// «contents» e «pull_requests» in scrittura sono facoltativi: servono all'adozione guidata.
			if (v == "write" || v == "admin") && !slices.Contains(ghapi.WritePermissions, p) {
				write = append(write, p)
			}
		}
		slices.Sort(write)
		if len(write) > 0 {
			res.Warnings = append(res.Warnings, "La App ha permessi di scrittura che Regia non usa: "+strings.Join(write, ", ")+".")
		}
		if err := cl.CheckToken(ctx); err != nil {
			return s.recordTest(ctx, a, id, fail(err))
		}
	}
	rate, err := cl.RateLimitNow(ctx)
	if err != nil {
		return s.recordTest(ctx, a, id, fail(err))
	}
	res.RateLimit = &rate
	if sc := cl.Scopes(); sc != "" {
		res.Details = append(res.Details, "Scope del token: "+sc+".")
	}
	var reached []string
	for _, o := range it.Config.Owners {
		acc, err := cl.Owner(ctx, o)
		if err != nil {
			return s.recordTest(ctx, a, id, fail(fmt.Errorf("organizzazione «%s»: %w", o, err)))
		}
		reached = append(reached, acc.Login)
	}
	res.Server = "github.com"
	if v := cl.ServerVersion(); v != "" {
		res.Server = "GitHub Enterprise Server " + v
	} else if it.Config.BaseURL != "https://api.github.com" {
		res.Server = "GitHub Enterprise Server"
	}
	res.OK = true
	what := "Organizzazione raggiungibile"
	if len(reached) > 1 {
		what = "Organizzazioni raggiungibili"
	}
	res.Message = fmt.Sprintf("Connessione riuscita con %s %s. %s: %s.", res.Server, who, what, strings.Join(reached, ", "))
	res.Details = append(res.Details, fmt.Sprintf("Limite di velocità: %d richieste rimaste su %d.", rate.Remaining, rate.Limit))
	return s.recordTest(ctx, a, id, res)
}

func selection(s string) string {
	if s == "all" {
		return "tutti i repository"
	}
	return "repository scelti"
}

func (s *Service) recordTest(ctx context.Context, a Actor, id string, r *TestResult) (*TestResult, error) {
	s.mu.Lock()
	delete(s.caps, id) // la prova di connessione rilegge anche i permessi di scrittura (adopt.go)
	s.mu.Unlock()
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.github.test", "integration", id, map[string]any{"ok": r.OK})
	})
	return r, err
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b)
}

// validID dice se s è un UUID (gli id arrivano dai percorsi delle richieste).
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

// systemActor è l'attore con cui il connettore consegna gli oggetti al modulo devsecops.
func systemActor(a Actor) engine.Actor {
	return engine.Actor{AccountID: a.AccountID, MemberID: a.MemberID, IP: a.IP, Permissions: []string{"*"}}
}
