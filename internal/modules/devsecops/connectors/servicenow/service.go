// Package servicenow è il connettore ServiceNow di DevSecOps, in sola lettura. ServiceNow
// resta il sistema degli incidenti, delle change e della CMDB: Regia non lo sostituisce e
// non vi scrive mai (niente incidenti creati, niente note). Quando serve, è ServiceNow a
// chiamare Regia, per esempio un flow che interroga il gate di rilascio prima del CAB.
//
//   - Integrazione (tabella integrations, kind servicenow): indirizzo dell'istanza,
//     autenticazione (utente di servizio in Basic oppure OAuth client credentials), classe
//     CMDB dei servizi, priorità degli incidenti, regola delle metriche DORA, intervallo,
//     rete interna, proxy, CA interna. I segreti sono cifrati con la chiave applicativa e
//     non tornano mai (service.go).
//   - Scoperta dei servizi della classe CMDB scelta e proposta di associazione alle
//     applicazioni del catalogo, confermata da una persona; solo i servizi associati si
//     sincronizzano (services.go).
//   - Sincronizzazione incrementale con cursore su sys_updated_on: incidenti delle priorità
//     scelte e change request dei servizi associati, più le change già note a Regia per
//     numero (sync.go). Si conservano solo i campi minimi, con il link.
//   - Incidenti e change visti da Regia: elenco, proposta di incidente di Regia, decisione
//     sulle metriche DORA (records.go).
//   - Webhook facoltativo, firmato con HMAC-SHA256 e deduplicato (webhook.go).
//
// Condivide con gli altri connettori le tabelle generiche (connector_*), la politica SSRF e
// la CA interna (ghapi) e il proxy in uscita (outbound).
package servicenow

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
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/outbound"
	"regia/internal/modules/devsecops/connectors/servicenow/snapi"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Kind è il tipo dell'integrazione nella tabella integrations.
const Kind = "servicenow"

// Error è un errore da mostrare così com'è (stessa forma degli altri connettori).
type Error = ghconn.Error

// Actor è chi configura o avvia (amministratore del tenant).
type Actor = ghconn.Actor

func invalid(field, msg string) error {
	return &Error{Status: 422, Code: "validation", Field: field, Msg: msg}
}

var (
	ErrNotFound        = &Error{Status: 404, Code: "integration.not_found", Msg: "integrazione inesistente"}
	ErrServiceNotFound = &Error{Status: 404, Code: "servicenow.service_not_found", Msg: "servizio CMDB inesistente"}
	ErrRecordNotFound  = &Error{Status: 404, Code: "servicenow.record_not_found", Msg: "incidente o change di ServiceNow inesistente"}
	ErrBusy            = &Error{Status: 409, Code: "sync.running", Msg: "una sincronizzazione di questa integrazione è già in corso"}
)

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Box *secretbox.Box
	// Dora, se implementa contract.DoraRecomputer (il modulo devsecops), ricalcola le
	// metriche dopo la prima sincronizzazione riuscita.
	Dora any
	// Dev toglie la protezione SSRF (http e indirizzi locali: il finto ServiceNow).
	Dev bool
	// AllowPrivate è il consenso dell'operatore alle reti interne (REGIA_CONNECTOR_ALLOW_PRIVATE).
	AllowPrivate bool
	// Proxy è il proxy in uscita dell'operatore (REGIA_CONNECTOR_PROXY); nil = nessuno.
	Proxy *url.URL
	// Resolver risolve i nomi per il controllo della destinazione dietro il proxy (test).
	Resolver outbound.Resolver
	Log      *slog.Logger
	// Timeout di una risposta di ServiceNow (default 30 s).
	Timeout time.Duration
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s).
	MaxWait time.Duration
	Sleep   func(context.Context, time.Duration) error
	Now     func() time.Time
}

// Service gestisce le integrazioni ServiceNow di tutti i tenant.
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

// Proxy è il proxy dell'operatore senza credenziali ("" = nessuno).
func (s *Service) Proxy() string { return outbound.Redacted(s.o.Proxy) }

// WaitIdle aspetta la fine delle sincronizzazioni avviate in background (test, arresto).
func (s *Service) WaitIdle() { s.wg.Wait() }

// ---------------------------------------------------------------------
// Configurazione
// ---------------------------------------------------------------------

// Regole delle metriche DORA per gli incidenti di ServiceNow.
const (
	DoraConfirm = "confirm" // contano solo dopo la conferma di una persona (predefinita)
	DoraAuto    = "auto"    // contano da soli
)

// DefaultServiceClass è la classe CMDB predefinita dei servizi: cmdb_ci_service comprende
// anche le sue sottoclassi (application service, servizi tecnici) ed è la tabella del campo
// «Servizio» (business_service) di incidenti e change.
const DefaultServiceClass = "cmdb_ci_service"

// Config è la configurazione salvata in integrations.config (mai segreti).
type Config struct {
	BaseURL         string `json:"base_url"`
	Auth            string `json:"auth"` // basic | oauth
	Username        string `json:"username,omitempty"`
	ClientID        string `json:"client_id,omitempty"`
	ServiceClass    string `json:"service_class"`
	ServiceFilter   string `json:"service_filter,omitempty"` // encoded query aggiuntiva sulla classe
	CodeField       string `json:"code_field,omitempty"`     // campo del CI con il codice dell'applicazione
	Priorities      []int  `json:"priorities"`
	DoraMode        string `json:"dora_mode"`
	LookbackDays    int    `json:"lookback_days"`
	IntervalMinutes int    `json:"interval_minutes"`
	PrivateNetwork  bool   `json:"private_network,omitempty"`
	UseProxy        bool   `json:"use_proxy,omitempty"`
	CACertificate   string `json:"ca_certificate,omitempty"`
}

type secrets struct {
	Password      string `json:"password,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

func secretContext(id string) string { return "integration:" + id }

// Input crea o modifica un'integrazione. I segreti: assenti o vuoti = invariati.
type Input struct {
	Name            string `json:"name"`
	Active          *bool  `json:"active"`
	BaseURL         string `json:"base_url"`
	Auth            string `json:"auth"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	ServiceClass    string `json:"service_class"`
	ServiceFilter   string `json:"service_filter"`
	CodeField       string `json:"code_field"`
	Priorities      []int  `json:"priorities"`
	DoraMode        string `json:"dora_mode"`
	LookbackDays    int    `json:"lookback_days"`
	IntervalMinutes int    `json:"interval_minutes"`
	PrivateNetwork  bool   `json:"private_network"`
	UseProxy        bool   `json:"use_proxy"`
	CACertificate   string `json:"ca_certificate"`
	WebhookSecret   string `json:"webhook_secret"`
	RemoveWebhook   bool   `json:"remove_webhook_secret"`
}

var (
	tableRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,79}$`)
	userRe  = regexp.MustCompile(`^[^\s:]{1,100}$`)
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

func (s *Service) clean(in *Input, prev secrets) (Config, secrets, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return Config{}, prev, invalid("name", "serve un nome (al massimo 80 caratteri)")
	}
	c := Config{Auth: strings.TrimSpace(in.Auth), Username: strings.TrimSpace(in.Username), ClientID: strings.TrimSpace(in.ClientID),
		ServiceClass: strings.TrimSpace(in.ServiceClass), ServiceFilter: strings.TrimSpace(in.ServiceFilter), CodeField: strings.TrimSpace(in.CodeField),
		DoraMode: in.DoraMode, LookbackDays: in.LookbackDays, IntervalMinutes: in.IntervalMinutes,
		PrivateNetwork: in.PrivateNetwork, UseProxy: in.UseProxy, CACertificate: strings.TrimSpace(in.CACertificate)}
	if c.PrivateNetwork && !s.AllowPrivate() {
		return c, prev, invalid("private_network", "l'operatore non ha consentito le reti interne ai connettori (REGIA_CONNECTOR_ALLOW_PRIVATE)")
	}
	if c.UseProxy && s.o.Proxy == nil {
		return c, prev, invalid("use_proxy", "l'operatore non ha configurato il proxy in uscita (REGIA_CONNECTOR_PROXY)")
	}
	base, err := snapi.NormalizeBaseURL(in.BaseURL, s.policy(c))
	if err != nil {
		return c, prev, invalid("base_url", err.Error())
	}
	c.BaseURL = base
	if c.ServiceClass == "" {
		c.ServiceClass = DefaultServiceClass
	}
	if !tableRe.MatchString(c.ServiceClass) {
		return c, prev, invalid("service_class", "classe CMDB non valida: è il nome della tabella, per esempio cmdb_ci_service o cmdb_ci_service_auto")
	}
	if c.CodeField != "" && !tableRe.MatchString(c.CodeField) {
		return c, prev, invalid("code_field", "nome di campo non valido (per esempio u_codice_applicazione o correlation_id)")
	}
	if len(c.ServiceFilter) > 500 || strings.ContainsAny(c.ServiceFilter, "\n\r") || strings.Contains(strings.ToUpper(c.ServiceFilter), "^NQ") {
		return c, prev, invalid("service_filter", "filtro non valido: una encoded query su una riga, senza ^NQ, al massimo 500 caratteri")
	}
	prio := []int{}
	for _, p := range in.Priorities {
		if p < 1 || p > 5 {
			return c, prev, invalid("priorities", "le priorità vanno da 1 (critica) a 5 (pianificazione)")
		}
		if !slices.Contains(prio, p) {
			prio = append(prio, p)
		}
	}
	if len(prio) == 0 {
		prio = []int{1, 2}
	}
	slices.Sort(prio)
	c.Priorities = prio
	switch c.DoraMode {
	case "":
		c.DoraMode = DoraConfirm
	case DoraConfirm, DoraAuto:
	default:
		return c, prev, invalid("dora_mode", "ammessi confirm (solo dopo conferma) e auto (contano da soli)")
	}
	if c.LookbackDays == 0 {
		c.LookbackDays = 90
	}
	if c.LookbackDays < 1 || c.LookbackDays > 730 {
		return c, prev, invalid("lookback_days", "la prima lettura va da 1 a 730 giorni indietro")
	}
	if c.IntervalMinutes == 0 {
		c.IntervalMinutes = 30
	}
	if c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
		return c, prev, invalid("interval_minutes", "intervallo tra 5 minuti e 24 ore")
	}
	if c.CACertificate != "" {
		if len(c.CACertificate) > 20000 {
			return c, prev, invalid("ca_certificate", "certificato troppo lungo")
		}
		if _, err := ghapi.NewHTTPClient(ghapi.Public, c.CACertificate, time.Second); err != nil {
			return c, prev, invalid("ca_certificate", err.Error())
		}
	}
	sec := prev
	switch c.Auth {
	case "", string(snapi.AuthBasic):
		c.Auth = string(snapi.AuthBasic)
		c.ClientID = ""
		sec.ClientSecret = ""
		if !userRe.MatchString(c.Username) {
			return c, prev, invalid("username", "serve l'utente di servizio di ServiceNow (senza spazi né due punti)")
		}
		if p := in.Password; p != "" {
			if len(p) > 500 || strings.ContainsAny(p, "\n\r") {
				return c, prev, invalid("password", "password non valida")
			}
			sec.Password = p
		}
		if sec.Password == "" {
			return c, prev, invalid("password", "serve la password dell'utente di servizio")
		}
	case string(snapi.AuthOAuth):
		c.Username = ""
		sec.Password = ""
		if c.ClientID == "" || len(c.ClientID) > 200 || strings.ContainsAny(c.ClientID, " \n\t") {
			return c, prev, invalid("client_id", "serve l'id del client OAuth registrato in ServiceNow")
		}
		if p := strings.TrimSpace(in.ClientSecret); p != "" {
			if len(p) > 500 || strings.ContainsAny(p, " \n\t") {
				return c, prev, invalid("client_secret", "segreto del client non valido")
			}
			sec.ClientSecret = p
		}
		if sec.ClientSecret == "" {
			return c, prev, invalid("client_secret", "serve il segreto del client OAuth")
		}
	default:
		return c, prev, invalid("auth", "ammessi basic (utente di servizio) e oauth (client credentials)")
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
	HasPassword      bool              `json:"has_password"`
	HasClientSecret  bool              `json:"has_client_secret"`
	HasWebhookSecret bool              `json:"has_webhook_secret"`
	WebhookPath      string            `json:"webhook_path"`
	Services         ghconn.RepoCounts `json:"services"`
	Records          RecordCounts      `json:"records"`
	Sync             SyncState         `json:"sync"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// RecordCounts sono i numeri degli incidenti e delle change letti.
type RecordCounts struct {
	Incidents int `json:"incidents"`
	Pending   int `json:"pending"` // incidenti in attesa di una decisione
	Changes   int `json:"changes"`
}

// SyncState è lo stato della sincronizzazione di un'integrazione.
type SyncState struct {
	NextRunAt      *time.Time `json:"next_run_at"`
	Running        bool       `json:"running"`
	LastStartedAt  *time.Time `json:"last_started_at"`
	LastFinishedAt *time.Time `json:"last_finished_at"`
	LastStatus     *string    `json:"last_status"`
	LastError      *string    `json:"last_error"`
}

func audited(ctx context.Context, tx pgx.Tx, a Actor, action, entityType, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: id, Payload: payload,
		ActorAccountID: a.AccountID, ActorMemberID: a.MemberID, IP: a.IP})
}

const selectIntegration = `
	select i.id, i.name, i.active, i.config, i.secret_enc, i.created_at, i.updated_at,
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'confirmed'),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'suggested'),
	  (select count(*) from connector_repositories r where r.integration_id = i.id and r.missing_since is null and r.mapping_status = 'ignored'),
	  (select count(*) from servicenow_incidents x where x.integration_id = i.id),
	  (select count(*) from servicenow_incidents x where x.integration_id = i.id and x.dora_status = 'pending' and x.regia_incident_element_id is null),
	  (select count(*) from servicenow_changes x where x.integration_id = i.id),
	  st.next_run_at, coalesce(st.lease_until > now(), false), st.last_started_at, st.last_finished_at, st.last_status, st.last_error
	from integrations i
	left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
	where i.kind = 'servicenow'`

func (s *Service) scanIntegration(row pgx.Row) (*Integration, secrets, error) {
	var it Integration
	var cfg, sealed []byte
	err := row.Scan(&it.ID, &it.Name, &it.Active, &cfg, &sealed, &it.CreatedAt, &it.UpdatedAt,
		&it.Services.Total, &it.Services.Confirmed, &it.Services.Suggested, &it.Services.Ignored,
		&it.Records.Incidents, &it.Records.Pending, &it.Records.Changes,
		&it.Sync.NextRunAt, &it.Sync.Running, &it.Sync.LastStartedAt, &it.Sync.LastFinishedAt, &it.Sync.LastStatus, &it.Sync.LastError)
	if err != nil {
		return nil, secrets{}, err
	}
	_ = json.Unmarshal(cfg, &it.Config)
	if it.Config.Priorities == nil {
		it.Config.Priorities = []int{}
	}
	var sec secrets
	if len(sealed) > 0 {
		plain, err := s.o.Box.Open(sealed, secretContext(it.ID))
		if err != nil {
			return nil, secrets{}, fmt.Errorf("segreti dell'integrazione %s: %w", it.ID, err)
		}
		_ = json.Unmarshal(plain, &sec)
	}
	it.HasPassword, it.HasClientSecret, it.HasWebhookSecret = sec.Password != "", sec.ClientSecret != "", sec.WebhookSecret != ""
	it.WebhookPath = "/api/v1/devsecops/servicenow/webhook/" + it.ID
	return &it, sec, nil
}

// List elenca le integrazioni ServiceNow del tenant.
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

func (s *Service) seal(id string, sec secrets) ([]byte, error) {
	b, _ := json.Marshal(sec)
	return s.o.Box.Seal(b, secretContext(id))
}

// Create crea un'integrazione ServiceNow.
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
		if err := tx.QueryRow(ctx, "select count(*) from integrations where kind = 'servicenow'").Scan(&n); err != nil {
			return err
		}
		if n >= 5 {
			return invalid("name", "al massimo 5 integrazioni ServiceNow per organizzazione")
		}
		if _, err := tx.Exec(ctx, `insert into integrations (tenant_id, id, kind, name, config, secret_enc, active)
			values (app_tenant(), $1, 'servicenow', $2, $3, $4, $5)`, id, in.Name, cfg, sealed, active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into connector_sync_state (tenant_id, integration_id) values (app_tenant(), $1)`, id); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.servicenow.create", "integration", id, auditConfig(c, sec))
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func auditConfig(c Config, sec secrets) map[string]any {
	return map[string]any{"base_url": c.BaseURL, "auth": c.Auth, "username": c.Username, "client_id": c.ClientID,
		"service_class": c.ServiceClass, "service_filter": c.ServiceFilter, "code_field": c.CodeField, "priorities": c.Priorities,
		"dora_mode": c.DoraMode, "lookback_days": c.LookbackDays, "interval_minutes": c.IntervalMinutes,
		"private_network": c.PrivateNetwork, "use_proxy": c.UseProxy, "webhook": sec.WebhookSecret != "", "ca": c.CACertificate != ""}
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
		p["secrets_changed"] = in.Password != "" || in.ClientSecret != "" || in.WebhookSecret != "" || in.RemoveWebhook
		return audited(ctx, tx, a, "devsecops.servicenow.update", "integration", id, p)
	})
}

// Delete cancella un'integrazione con i suoi servizi, incidenti e change letti (gli
// incidenti di Regia nati da una proposta restano: sono di Regia).
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from integrations where id = $1 and kind = 'servicenow'", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "devsecops.servicenow.delete", "integration", id, nil)
	})
}

// GenerateWebhookSecret crea un nuovo segreto del webhook e lo restituisce una volta.
func (s *Service) GenerateWebhookSecret(ctx context.Context, a Actor, id string) (string, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret := "snwh_" + base64.RawURLEncoding.EncodeToString(b)
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
		return audited(ctx, tx, a, "devsecops.servicenow.webhook_secret", "integration", id, nil)
	})
	return secret, err
}

// ---------------------------------------------------------------------
// Client e prova di connessione
// ---------------------------------------------------------------------

func (s *Service) httpClient(p ghapi.Policy, ca string, proxy bool) (*http.Client, error) {
	h := sha256.Sum256([]byte(ca))
	key := fmt.Sprintf("%s|%s|%t", p, hex.EncodeToString(h[:8]), proxy)
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[key]; ok {
		return c, nil
	}
	c, err := ghapi.NewHTTPClient(p, ca, s.o.Timeout)
	if err != nil {
		return nil, err
	}
	if proxy {
		if s.o.Proxy == nil {
			return nil, errors.New("l'integrazione usa il proxy, ma l'operatore non l'ha configurato (REGIA_CONNECTOR_PROXY)")
		}
		if c, err = outbound.Via(c, p, s.o.Proxy, s.o.Resolver); err != nil {
			return nil, err
		}
	}
	s.clients[key] = c
	return c, nil
}

func (s *Service) client(c Config, sec secrets) (*snapi.Client, error) {
	hc, err := s.httpClient(s.policy(c), c.CACertificate, c.UseProxy)
	if err != nil {
		return nil, err
	}
	return snapi.New(snapi.Options{BaseURL: c.BaseURL, Auth: snapi.Auth(c.Auth), Username: c.Username, Password: sec.Password,
		ClientID: c.ClientID, ClientSecret: sec.ClientSecret, HTTP: hc, MaxWait: s.o.MaxWait, Sleep: s.o.Sleep, Now: s.o.Now})
}

// TestResult è l'esito della prova di connessione.
type TestResult struct {
	OK       bool        `json:"ok"`
	Message  string      `json:"message"`
	Details  []string    `json:"details"`
	Warnings []string    `json:"warnings"`
	Tables   []TableTest `json:"tables"`
}

// TableTest è l'esito della lettura di una tabella.
type TableTest struct {
	Table    string `json:"table"`
	Label    string `json:"label"`
	Readable bool   `json:"readable"`
	Sample   int    `json:"sample"` // record visti (al massimo 5)
	Error    string `json:"error,omitempty"`
}

// Test prova la connessione con la configurazione salvata: autenticazione, poi la lettura
// di incident, change_request e della classe CMDB, una tabella alla volta.
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
	res := &TestResult{Details: []string{}, Warnings: []string{}, Tables: []TableTest{}}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cl, err := s.client(it.Config, sec)
	if err != nil {
		res.Message = "Connessione non riuscita: " + err.Error()
		return s.recordTest(ctx, a, id, res)
	}
	who := fmt.Sprintf("come «%s» (utente di servizio, Basic)", it.Config.Username)
	if it.Config.Auth == string(snapi.AuthOAuth) {
		who = fmt.Sprintf("con il client OAuth «%s» (client credentials)", it.Config.ClientID)
	}
	if it.Config.UseProxy {
		who += " tramite il proxy " + s.Proxy()
	}
	tables := []struct{ table, label, query string }{
		{"incident", "Incidenti", "priorityIN" + joinInts(it.Config.Priorities)},
		{"change_request", "Change request", ""},
		{it.Config.ServiceClass, "Servizi CMDB (" + it.Config.ServiceClass + ")", it.Config.ServiceFilter},
	}
	for _, t := range tables {
		tt := TableTest{Table: t.table, Label: t.label}
		err := cl.Table(ctx, snapi.Query{Table: t.table, Query: t.query, Fields: []string{"sys_id"}, Limit: 5}, func(map[string]string) (bool, error) {
			tt.Sample++
			return false, nil
		})
		if err != nil {
			tt.Error = err.Error()
			res.Tables = append(res.Tables, tt)
			if st := snapi.StatusOf(err); st == 401 || st == 0 {
				// Credenziali, rete o certificato: inutile provare le altre tabelle.
				res.Message = "Connessione non riuscita: " + err.Error()
				return s.recordTest(ctx, a, id, res)
			}
			continue
		}
		tt.Readable = true
		if tt.Sample == 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("«%s» risponde ma non restituisce record: o è vuota, o le ACL di ServiceNow li nascondono all'utente (ServiceNow toglie in silenzio le righe che non si possono leggere).", t.table))
		}
		res.Tables = append(res.Tables, tt)
	}
	var bad []string
	for _, t := range res.Tables {
		if !t.Readable {
			bad = append(bad, t.Table)
		}
	}
	res.Details = append(res.Details,
		"Regia legge soltanto e non può verificare che l'utente non abbia permessi di scrittura: dagli solo ruoli di lettura sulle tabelle usate (con il ruolo snc_read_only, se la tua istanza lo usa, ogni scrittura è bloccata comunque).",
		fmt.Sprintf("Si leggono gli incidenti con priorità %s, le change request e i servizi della classe %s.", joinInts(it.Config.Priorities), it.Config.ServiceClass))
	if len(bad) > 0 {
		res.Message = fmt.Sprintf("Connessione riuscita con %s %s, ma non si leggono: %s.", it.Config.BaseURL, who, strings.Join(bad, ", "))
		return s.recordTest(ctx, a, id, res)
	}
	res.OK = true
	res.Message = fmt.Sprintf("Connessione riuscita con %s %s. Tabelle leggibili: incident, change_request, %s.", it.Config.BaseURL, who, it.Config.ServiceClass)
	return s.recordTest(ctx, a, id, res)
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ",")
}

func (s *Service) recordTest(ctx context.Context, a Actor, id string, r *TestResult) (*TestResult, error) {
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.servicenow.test", "integration", id, map[string]any{"ok": r.OK})
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
