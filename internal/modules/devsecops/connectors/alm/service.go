// Package alm è il connettore OpenText ALM/Quality Center di DevSecOps, in sola lettura. È il
// primo passo per sostituire ALM per gradi: oggi Regia legge i test da ALM; poi verranno il test
// management nativo e la migrazione dello storico, che useranno le stesse tabelle dei test
// (test_sets, test_set_items, test_runs, test_defects, con origin).
//
//   - Integrazione (tabella integrations, kind alm): indirizzo di qcbin, autenticazione (API key
//     oppure utente di servizio), progetti letti (dominio e progetto), dove sta il numero di
//     change (campo del test set, nome del test set, campo del ciclo o della release), come si
//     riconosce l'applicazione (progetto, cartella del Test Lab o campo del test set), stati
//     chiusi dei difetti, fuso orario del server, intervallo, rete interna, proxy, CA interna.
//     I segreti sono cifrati con la chiave applicativa e non tornano mai (service.go).
//   - Unità ALM (progetto, cartella o valore del campo) proposte come applicazioni del
//     catalogo e confermate da una persona (units.go).
//   - Sincronizzazione incrementale con cursori su last-modified per progetto ed entità:
//     release e cicli, test set, istanze di test, esecuzioni, difetti con i loro link (sync.go).
//   - Test set letti, con i numeri per stato (records.go).
//
// Regia non scrive mai su ALM. Condivide con gli altri connettori le tabelle generiche
// (connector_*), la politica SSRF e la CA interna (ghapi) e il proxy in uscita (outbound).
package alm

import (
	"context"
	"crypto/sha256"
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
	"regia/internal/modules/devsecops/connectors/alm/almapi"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/outbound"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Kind è il tipo dell'integrazione nella tabella integrations.
const Kind = "alm"

// Error è un errore da mostrare così com'è (stessa forma degli altri connettori).
type Error = ghconn.Error

// Actor è chi configura o avvia (amministratore del tenant).
type Actor = ghconn.Actor

func invalid(field, msg string) error {
	return &Error{Status: 422, Code: "validation", Field: field, Msg: msg}
}

var (
	ErrNotFound     = &Error{Status: 404, Code: "integration.not_found", Msg: "integrazione inesistente"}
	ErrUnitNotFound = &Error{Status: 404, Code: "alm.unit_not_found", Msg: "unità ALM inesistente"}
	ErrBusy         = &Error{Status: 409, Code: "sync.running", Msg: "una sincronizzazione di questa integrazione è già in corso"}
)

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Box *secretbox.Box
	// Dev toglie la protezione SSRF (http e indirizzi locali: il finto ALM).
	Dev bool
	// AllowPrivate è il consenso dell'operatore alle reti interne (REGIA_CONNECTOR_ALLOW_PRIVATE).
	AllowPrivate bool
	// Proxy è il proxy in uscita dell'operatore (REGIA_CONNECTOR_PROXY); nil = nessuno.
	Proxy *url.URL
	// Resolver risolve i nomi per il controllo della destinazione dietro il proxy (test).
	Resolver outbound.Resolver
	Log      *slog.Logger
	// Timeout di una risposta di ALM (default 60 s: le pagine di esecuzioni sono lente).
	Timeout time.Duration
	// MaxWait è l'attesa massima per un limite di velocità (default 60 s).
	MaxWait time.Duration
	Sleep   func(context.Context, time.Duration) error
	Now     func() time.Time
}

// Service gestisce le integrazioni ALM di tutti i tenant.
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
		o.Timeout = 60 * time.Second
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

// Dove sta il numero di change.
const (
	ChangeTestSetField = "test_set_field" // un campo del test set (predefinito: user-01)
	ChangeTestSetName  = "test_set_name"  // dentro il nome del test set
	ChangeCycleField   = "cycle_field"    // un campo del ciclo (release-cycle)
	ChangeReleaseField = "release_field"  // un campo della release
)

// Come si riconosce l'applicazione.
const (
	AppProject      = "project"        // un progetto ALM = un'applicazione (predefinito)
	AppFolder       = "folder"         // cartella di primo livello del Test Lab
	AppTestSetField = "test_set_field" // un campo del test set con il codice o il nome
)

// DefaultChangePattern riconosce un numero di change dentro un testo (CHG0045120, RFC123456…).
const DefaultChangePattern = `[A-Za-z]{2,10}[0-9]{4,12}`

// DefaultChangeField è il campo utente predefinito del test set con il numero di change.
const DefaultChangeField = "user-01"

// Project è un progetto ALM letto dall'integrazione.
type Project struct {
	Domain  string `json:"domain"`
	Project string `json:"project"`
}

func (p Project) key() string { return p.Domain + "/" + p.Project }

// Config è la configurazione salvata in integrations.config (mai segreti).
type Config struct {
	BaseURL         string    `json:"base_url"`
	Auth            string    `json:"auth"` // apikey | user
	Username        string    `json:"username,omitempty"`
	ClientID        string    `json:"client_id,omitempty"`
	Projects        []Project `json:"projects"`
	ChangeSource    string    `json:"change_source"`
	ChangeField     string    `json:"change_field,omitempty"`
	ChangePattern   string    `json:"change_pattern"`
	AppSource       string    `json:"app_source"`
	AppField        string    `json:"app_field,omitempty"`
	DefectClosed    []string  `json:"defect_closed"`
	KeepTester      bool      `json:"keep_tester,omitempty"`
	Timezone        string    `json:"timezone"`
	LookbackDays    int       `json:"lookback_days"`
	IntervalMinutes int       `json:"interval_minutes"`
	PrivateNetwork  bool      `json:"private_network,omitempty"`
	UseProxy        bool      `json:"use_proxy,omitempty"`
	CACertificate   string    `json:"ca_certificate,omitempty"`
}

type secrets struct {
	Password     string `json:"password,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

func secretContext(id string) string { return "integration:" + id }

// Input crea o modifica un'integrazione. I segreti: assenti o vuoti = invariati.
type Input struct {
	Name            string    `json:"name"`
	Active          *bool     `json:"active"`
	BaseURL         string    `json:"base_url"`
	Auth            string    `json:"auth"`
	Username        string    `json:"username"`
	Password        string    `json:"password"`
	ClientID        string    `json:"client_id"`
	ClientSecret    string    `json:"client_secret"`
	Projects        []Project `json:"projects"`
	ChangeSource    string    `json:"change_source"`
	ChangeField     string    `json:"change_field"`
	ChangePattern   string    `json:"change_pattern"`
	AppSource       string    `json:"app_source"`
	AppField        string    `json:"app_field"`
	DefectClosed    []string  `json:"defect_closed"`
	KeepTester      bool      `json:"keep_tester"`
	Timezone        string    `json:"timezone"`
	LookbackDays    int       `json:"lookback_days"`
	IntervalMinutes int       `json:"interval_minutes"`
	PrivateNetwork  bool      `json:"private_network"`
	UseProxy        bool      `json:"use_proxy"`
	CACertificate   string    `json:"ca_certificate"`
}

var (
	// Nomi di campo di ALM: user-01, user-template-03, name, status…
	fieldRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,59}$`)
	// Domini e progetti: lettere, cifre, trattino basso, trattino, punto e spazio.
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_. -]{0,99}$`)
	userRe = regexp.MustCompile(`^[^\s:<>&]{1,100}$`)
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
		ChangeSource: strings.TrimSpace(in.ChangeSource), ChangeField: strings.TrimSpace(in.ChangeField), ChangePattern: strings.TrimSpace(in.ChangePattern),
		AppSource: strings.TrimSpace(in.AppSource), AppField: strings.TrimSpace(in.AppField), KeepTester: in.KeepTester,
		Timezone: strings.TrimSpace(in.Timezone), LookbackDays: in.LookbackDays, IntervalMinutes: in.IntervalMinutes,
		PrivateNetwork: in.PrivateNetwork, UseProxy: in.UseProxy, CACertificate: strings.TrimSpace(in.CACertificate)}
	if c.PrivateNetwork && !s.AllowPrivate() {
		return c, prev, invalid("private_network", "l'operatore non ha consentito le reti interne ai connettori (REGIA_CONNECTOR_ALLOW_PRIVATE)")
	}
	if c.UseProxy && s.o.Proxy == nil {
		return c, prev, invalid("use_proxy", "l'operatore non ha configurato il proxy in uscita (REGIA_CONNECTOR_PROXY)")
	}
	base, err := almapi.NormalizeBaseURL(in.BaseURL, s.policy(c))
	if err != nil {
		return c, prev, invalid("base_url", err.Error())
	}
	c.BaseURL = base
	c.Projects = []Project{}
	seen := map[string]bool{}
	for _, p := range in.Projects {
		p.Domain, p.Project = strings.TrimSpace(p.Domain), strings.TrimSpace(p.Project)
		if p.Domain == "" && p.Project == "" {
			continue
		}
		if !nameRe.MatchString(p.Domain) || !nameRe.MatchString(p.Project) {
			return c, prev, invalid("projects", "dominio e progetto vanno scritti come in ALM (lettere, cifre, _ - . e spazi)")
		}
		if k := strings.ToLower(p.key()); !seen[k] {
			seen[k] = true
			c.Projects = append(c.Projects, p)
		}
	}
	if len(c.Projects) == 0 {
		return c, prev, invalid("projects", "indica almeno un progetto ALM (dominio e progetto)")
	}
	if len(c.Projects) > 50 {
		return c, prev, invalid("projects", "al massimo 50 progetti per integrazione")
	}
	switch c.ChangeSource {
	case "":
		c.ChangeSource = ChangeTestSetField
	case ChangeTestSetField, ChangeTestSetName, ChangeCycleField, ChangeReleaseField:
	default:
		return c, prev, invalid("change_source", "il numero di change sta in un campo del test set, nel nome del test set, in un campo del ciclo o della release")
	}
	if c.ChangeSource == ChangeTestSetName {
		c.ChangeField = ""
	} else {
		if c.ChangeField == "" && c.ChangeSource == ChangeTestSetField {
			c.ChangeField = DefaultChangeField
		}
		if !fieldRe.MatchString(c.ChangeField) {
			return c, prev, invalid("change_field", "serve il nome del campo di ALM, per esempio user-01 (si vede in Customization → Project Entities)")
		}
	}
	if c.ChangePattern == "" {
		c.ChangePattern = DefaultChangePattern
	}
	if len(c.ChangePattern) > 200 {
		return c, prev, invalid("change_pattern", "espressione troppo lunga")
	}
	if _, err := regexp.Compile(c.ChangePattern); err != nil {
		return c, prev, invalid("change_pattern", "espressione regolare non valida: "+err.Error())
	}
	switch c.AppSource {
	case "":
		c.AppSource = AppProject
	case AppProject, AppFolder, AppTestSetField:
	default:
		return c, prev, invalid("app_source", "l'applicazione si riconosce dal progetto, dalla cartella del Test Lab o da un campo del test set")
	}
	if c.AppSource == AppTestSetField {
		if !fieldRe.MatchString(c.AppField) {
			return c, prev, invalid("app_field", "serve il nome del campo del test set con l'applicazione, per esempio user-02")
		}
	} else {
		c.AppField = ""
	}
	c.DefectClosed = []string{}
	for _, st := range in.DefectClosed {
		if st = strings.TrimSpace(st); st != "" && utf8.RuneCountInString(st) <= 60 && !slices.ContainsFunc(c.DefectClosed, func(x string) bool { return strings.EqualFold(x, st) }) {
			c.DefectClosed = append(c.DefectClosed, st)
		}
	}
	if in.DefectClosed == nil {
		c.DefectClosed = []string{"Closed", "Rejected"}
	}
	if len(c.DefectClosed) > 20 {
		return c, prev, invalid("defect_closed", "al massimo 20 stati chiusi")
	}
	if c.Timezone == "" {
		c.Timezone = "Europe/Rome"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return c, prev, invalid("timezone", "fuso orario sconosciuto: "+c.Timezone)
	}
	if c.LookbackDays == 0 {
		c.LookbackDays = 90
	}
	if c.LookbackDays < 1 || c.LookbackDays > 1095 {
		return c, prev, invalid("lookback_days", "la prima lettura va da 1 a 1095 giorni indietro")
	}
	if c.IntervalMinutes == 0 {
		c.IntervalMinutes = 60
	}
	if c.IntervalMinutes < 10 || c.IntervalMinutes > 1440 {
		return c, prev, invalid("interval_minutes", "intervallo tra 10 minuti e 24 ore (ogni lettura occupa una licenza di ALM per qualche secondo)")
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
	case "", string(almapi.AuthAPIKey):
		c.Auth = string(almapi.AuthAPIKey)
		c.Username = ""
		sec.Password = ""
		if c.ClientID == "" || len(c.ClientID) > 200 || strings.ContainsAny(c.ClientID, " \n\t<>&") {
			return c, prev, invalid("client_id", "serve il client id dell'API key (Site Administration → API Key Management)")
		}
		if p := strings.TrimSpace(in.ClientSecret); p != "" {
			if len(p) > 500 || strings.ContainsAny(p, "\n\r") {
				return c, prev, invalid("client_secret", "segreto non valido")
			}
			sec.ClientSecret = p
		}
		if sec.ClientSecret == "" {
			return c, prev, invalid("client_secret", "serve il segreto dell'API key")
		}
	case string(almapi.AuthUser):
		c.ClientID = ""
		sec.ClientSecret = ""
		if !userRe.MatchString(c.Username) {
			return c, prev, invalid("username", "serve l'utente di servizio di ALM (senza spazi)")
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
	default:
		return c, prev, invalid("auth", "ammessi apikey (API key) e user (utente di servizio)")
	}
	return c, sec, nil
}

// Integration è un'integrazione come appare nella configurazione (mai i segreti).
type Integration struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Active          bool              `json:"active"`
	Config          Config            `json:"config"`
	HasPassword     bool              `json:"has_password"`
	HasClientSecret bool              `json:"has_client_secret"`
	Units           ghconn.RepoCounts `json:"units"`
	Records         RecordCounts      `json:"records"`
	Sync            SyncState         `json:"sync"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// RecordCounts sono i numeri dei dati letti.
type RecordCounts struct {
	TestSets int `json:"test_sets"`
	Linked   int `json:"linked"` // test set con un numero di change
	Runs     int `json:"runs"`
	Defects  int `json:"defects"`
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
	  (select count(*) from test_sets x where x.integration_id = i.id),
	  (select count(*) from test_sets x where x.integration_id = i.id and x.change_number <> ''),
	  (select count(*) from test_runs x where x.integration_id = i.id),
	  (select count(*) from test_defects x where x.integration_id = i.id),
	  st.next_run_at, coalesce(st.lease_until > now(), false), st.last_started_at, st.last_finished_at, st.last_status, st.last_error
	from integrations i
	left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
	where i.kind = 'alm'`

func (s *Service) scanIntegration(row pgx.Row) (*Integration, secrets, error) {
	var it Integration
	var cfg, sealed []byte
	err := row.Scan(&it.ID, &it.Name, &it.Active, &cfg, &sealed, &it.CreatedAt, &it.UpdatedAt,
		&it.Units.Total, &it.Units.Confirmed, &it.Units.Suggested, &it.Units.Ignored,
		&it.Records.TestSets, &it.Records.Linked, &it.Records.Runs, &it.Records.Defects,
		&it.Sync.NextRunAt, &it.Sync.Running, &it.Sync.LastStartedAt, &it.Sync.LastFinishedAt, &it.Sync.LastStatus, &it.Sync.LastError)
	if err != nil {
		return nil, secrets{}, err
	}
	_ = json.Unmarshal(cfg, &it.Config)
	if it.Config.Projects == nil {
		it.Config.Projects = []Project{}
	}
	if it.Config.DefectClosed == nil {
		it.Config.DefectClosed = []string{}
	}
	var sec secrets
	if len(sealed) > 0 {
		plain, err := s.o.Box.Open(sealed, secretContext(it.ID))
		if err != nil {
			return nil, secrets{}, fmt.Errorf("segreti dell'integrazione %s: %w", it.ID, err)
		}
		_ = json.Unmarshal(plain, &sec)
	}
	it.HasPassword, it.HasClientSecret = sec.Password != "", sec.ClientSecret != ""
	return &it, sec, nil
}

// List elenca le integrazioni ALM del tenant.
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

// Create crea un'integrazione ALM.
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
		if err := tx.QueryRow(ctx, "select count(*) from integrations where kind = 'alm'").Scan(&n); err != nil {
			return err
		}
		if n >= 5 {
			return invalid("name", "al massimo 5 integrazioni ALM per organizzazione")
		}
		if _, err := tx.Exec(ctx, `insert into integrations (tenant_id, id, kind, name, config, secret_enc, active)
			values (app_tenant(), $1, 'alm', $2, $3, $4, $5)`, id, in.Name, cfg, sealed, active); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into connector_sync_state (tenant_id, integration_id) values (app_tenant(), $1)`, id); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.alm.create", "integration", id, auditConfig(c))
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func auditConfig(c Config) map[string]any {
	projects := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		projects[i] = p.key()
	}
	return map[string]any{"base_url": c.BaseURL, "auth": c.Auth, "username": c.Username, "client_id": c.ClientID, "projects": projects,
		"change_source": c.ChangeSource, "change_field": c.ChangeField, "change_pattern": c.ChangePattern, "app_source": c.AppSource,
		"app_field": c.AppField, "defect_closed": c.DefectClosed, "keep_tester": c.KeepTester, "timezone": c.Timezone,
		"lookback_days": c.LookbackDays, "interval_minutes": c.IntervalMinutes, "private_network": c.PrivateNetwork,
		"use_proxy": c.UseProxy, "ca": c.CACertificate != ""}
}

// Update modifica un'integrazione; i segreti non indicati restano. Se cambia dove sta il
// numero di change o come si riconosce l'applicazione, i cursori ripartono: la prossima
// lettura rilegge la finestra iniziale con la regola nuova.
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
		o := cur.Config
		reset := o.ChangeSource != c.ChangeSource || o.ChangeField != c.ChangeField || o.ChangePattern != c.ChangePattern ||
			o.AppSource != c.AppSource || o.AppField != c.AppField || o.KeepTester != c.KeepTester || o.Timezone != c.Timezone
		if _, err := tx.Exec(ctx, `update connector_sync_state set next_run_at = least(next_run_at, now() + make_interval(mins => $2)),
			cursors = case when $3 then '{}'::jsonb else cursors end where integration_id = $1`, id, c.IntervalMinutes, reset); err != nil {
			return err
		}
		if !slices.Equal(o.DefectClosed, c.DefectClosed) {
			// Gli stati chiusi valgono subito anche per i difetti già letti.
			if _, err := tx.Exec(ctx, `update test_defects set open = not (lower(status) = any($2::text[])), updated_at = now()
				where integration_id = $1`, id, lowerAll(c.DefectClosed)); err != nil {
				return err
			}
		}
		if !c.KeepTester {
			if _, err := tx.Exec(ctx, "update test_runs set tester = null where integration_id = $1 and tester is not null", id); err != nil {
				return err
			}
		}
		p := auditConfig(c)
		p["active"] = active
		p["secrets_changed"] = in.Password != "" || in.ClientSecret != ""
		p["cursors_reset"] = reset
		return audited(ctx, tx, a, "devsecops.alm.update", "integration", id, p)
	})
}

// Delete cancella un'integrazione con le unità, i test set, le esecuzioni e i difetti letti.
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from integrations where id = $1 and kind = 'alm'", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "devsecops.alm.delete", "integration", id, nil)
	})
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

func (s *Service) client(c Config, sec secrets) (*almapi.Client, error) {
	hc, err := s.httpClient(s.policy(c), c.CACertificate, c.UseProxy)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return almapi.New(almapi.Options{BaseURL: c.BaseURL, Auth: almapi.Auth(c.Auth), Username: c.Username, Password: sec.Password,
		ClientID: c.ClientID, ClientSecret: sec.ClientSecret, HTTP: hc, Location: loc, MaxWait: s.o.MaxWait, Sleep: s.o.Sleep, Now: s.o.Now})
}

// TestResult è l'esito della prova di connessione.
type TestResult struct {
	OK       bool          `json:"ok"`
	Message  string        `json:"message"`
	Method   string        `json:"method,omitempty"` // metodo di accesso riuscito
	Details  []string      `json:"details"`
	Warnings []string      `json:"warnings"`
	Projects []ProjectTest `json:"projects"`
}

// ProjectTest è l'esito della lettura di un progetto.
type ProjectTest struct {
	Project     string           `json:"project"` // dominio/progetto
	Readable    bool             `json:"readable"`
	Collections []CollectionTest `json:"collections"`
	Error       string           `json:"error,omitempty"`
}

// CollectionTest è l'esito della lettura di una collezione.
type CollectionTest struct {
	Collection string `json:"collection"`
	Label      string `json:"label"`
	Readable   bool   `json:"readable"`
	Sample     int    `json:"sample"` // entità viste (al massimo 5)
	Error      string `json:"error,omitempty"`
}

// Test prova la connessione con la configurazione salvata: accesso e sessione, poi, per ogni
// progetto, la lettura di test set (con il campo del numero di change), istanze, esecuzioni e
// difetti. Alla fine chiude la sessione, che occupa una licenza.
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
	res := &TestResult{Details: []string{}, Warnings: []string{}, Projects: []ProjectTest{}}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cl, err := s.client(it.Config, sec)
	if err != nil {
		res.Message = "Connessione non riuscita: " + err.Error()
		return s.recordTest(ctx, a, id, res)
	}
	defer cl.Logout(ctx)
	if err := cl.Login(ctx); err != nil {
		res.Message = "Connessione non riuscita: " + err.Error()
		return s.recordTest(ctx, a, id, res)
	}
	res.Method = cl.Method()
	who := fmt.Sprintf("con l'utente di servizio «%s»", it.Config.Username)
	if it.Config.Auth == string(almapi.AuthAPIKey) {
		who = fmt.Sprintf("con l'API key «%s»", it.Config.ClientID)
	}
	if it.Config.UseProxy {
		who += " tramite il proxy " + s.Proxy()
	}
	switch res.Method {
	case almapi.MethodOAuth2Login:
		res.Details = append(res.Details, "Accesso con rest/oauth2/login: è il metodo delle versioni da 12.60 a 14.x. Funziona, ma OpenText lo indica come superato dalle versioni 15.00 in poi.")
	case almapi.MethodALMAuthenticate:
		res.Details = append(res.Details, "Accesso con authentication-point/alm-authenticate, il metodo consigliato da OpenText.")
	}
	visible := map[string]bool{}
	if doms, err := cl.Projects(ctx); err == nil {
		for _, d := range doms {
			for _, p := range d.Projects {
				visible[strings.ToLower(d.Name+"/"+p)] = true
			}
		}
	}
	changeLabel := ""
	allOK := true
	for _, p := range it.Config.Projects {
		pt := ProjectTest{Project: p.key(), Collections: []CollectionTest{}}
		if len(visible) > 0 && !visible[strings.ToLower(p.key())] {
			pt.Error = "il progetto non è tra quelli visibili all'utente: controlla dominio e progetto, o assegna l'utente al progetto"
			res.Projects = append(res.Projects, pt)
			allOK = false
			continue
		}
		setFields := []string{"id", "name"}
		if it.Config.ChangeSource == ChangeTestSetField {
			setFields = append(setFields, it.Config.ChangeField)
		}
		if it.Config.AppSource == AppTestSetField {
			setFields = append(setFields, it.Config.AppField)
		}
		colls := []struct {
			coll, label string
			fields      []string
		}{
			{"test-sets", "Test set", setFields},
			{"test-instances", "Istanze di test", []string{"id", "status"}},
			{"runs", "Esecuzioni", []string{"id", "status"}},
			{"defects", "Difetti", []string{"id", "status", "severity"}},
		}
		switch it.Config.ChangeSource {
		case ChangeCycleField:
			colls = append(colls, struct {
				coll, label string
				fields      []string
			}{"release-cycles", "Cicli", []string{"id", "name", it.Config.ChangeField}})
		case ChangeReleaseField:
			colls = append(colls, struct {
				coll, label string
				fields      []string
			}{"releases", "Release", []string{"id", "name", it.Config.ChangeField}})
		}
		pt.Readable = true
		for _, c := range colls {
			ct := CollectionTest{Collection: c.coll, Label: c.label}
			withChange := 0
			err := cl.Entities(ctx, almapi.Query{Domain: p.Domain, Project: p.Project, Collection: c.coll, Fields: c.fields,
				OrderBy: "last-modified[DESC]", Limit: 5}, func(e almapi.Entity) (bool, error) {
				ct.Sample++
				if it.Config.ChangeField != "" && e[it.Config.ChangeField] != "" {
					withChange++
				}
				return false, nil
			})
			if err != nil {
				ct.Error = err.Error()
				pt.Readable = false
				pt.Collections = append(pt.Collections, ct)
				if almapi.StatusOf(err) == 0 {
					res.Message = "Connessione non riuscita: " + err.Error()
					res.Projects = append(res.Projects, pt)
					return s.recordTest(ctx, a, id, res)
				}
				continue
			}
			ct.Readable = true
			if ct.Sample == 0 && c.coll != "defects" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: «%s» risponde ma è vuota.", p.key(), c.coll))
			}
			isChangeColl := (c.coll == "test-sets" && it.Config.ChangeSource == ChangeTestSetField) ||
				(c.coll == "release-cycles" && it.Config.ChangeSource == ChangeCycleField) || (c.coll == "releases" && it.Config.ChangeSource == ChangeReleaseField)
			if isChangeColl && ct.Sample > 0 {
				changeLabel = c.label
				if withChange == 0 {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: il campo %s esiste ma è vuoto negli ultimi %d %s modificati. Controlla che sia il campo giusto.",
						p.key(), it.Config.ChangeField, ct.Sample, strings.ToLower(c.label)))
				}
			}
			pt.Collections = append(pt.Collections, ct)
		}
		if !pt.Readable {
			allOK = false
		}
		res.Projects = append(res.Projects, pt)
	}
	res.Details = append(res.Details,
		"Regia legge soltanto e non può verificare che l'utente non abbia permessi di scrittura: dagli in ALM un gruppo di sola lettura (Viewer) sui progetti.",
		"Ogni lettura apre una sessione REST, che occupa una licenza di ALM per qualche secondo, e la chiude alla fine.")
	switch it.Config.ChangeSource {
	case ChangeTestSetName:
		res.Details = append(res.Details, "Il numero di change si cerca nel nome del test set con l'espressione «"+it.Config.ChangePattern+"».")
	default:
		if changeLabel != "" {
			res.Details = append(res.Details, fmt.Sprintf("Il numero di change si legge dal campo %s (%s).", it.Config.ChangeField, strings.ToLower(changeLabel)))
		}
	}
	if !allOK {
		res.Message = fmt.Sprintf("Accesso riuscito a %s %s, ma alcuni progetti non si leggono.", it.Config.BaseURL, who)
		return s.recordTest(ctx, a, id, res)
	}
	res.OK = true
	res.Message = fmt.Sprintf("Connessione riuscita con %s %s. Progetti leggibili: %d.", it.Config.BaseURL, who, len(res.Projects))
	return s.recordTest(ctx, a, id, res)
}

func (s *Service) recordTest(ctx context.Context, a Actor, id string, r *TestResult) (*TestResult, error) {
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.alm.test", "integration", id, map[string]any{"ok": r.OK, "method": r.Method})
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
