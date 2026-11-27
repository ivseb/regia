package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Livelli (replica/ai.md §4): le funzioni nominano un livello, non un modello.
const (
	TierFast = "rapido"
	TierDeep = "approfondito"
)

// Feature è una funzione dell'AI con il suo livello predefinito. Active: già costruita
// (le altre si possono già spostare di livello o di modello, per quando arriveranno).
type Feature struct {
	Key        string `json:"key"`
	Tier       string `json:"tier"`
	NeedsTools bool   `json:"needs_tools"`
	Active     bool   `json:"active"`
}

// Features sono le funzioni dell'AI (replica/ai.md §2 e §4).
var Features = []Feature{
	{Key: "assistente", Tier: TierDeep, NeedsTools: true, Active: true},
	// Domande sul grafo (ondata 2): il modello scrive una query, Regia la prova; non chiama strumenti.
	{Key: "domande_grafo", Tier: TierDeep, Active: true},
	{Key: "riassunti", Tier: TierFast},
	{Key: "classificazione", Tier: TierFast},
	{Key: "bozze", Tier: TierDeep},
	{Key: "wiki", Tier: TierFast, NeedsTools: true},
	{Key: "grc", Tier: TierDeep, NeedsTools: true},
}

func featureByKey(key string) (Feature, bool) {
	for _, f := range Features {
		if f.Key == key {
			return f, true
		}
	}
	return Feature{}, false
}

// Errori del dominio (l'API li traduce con il codice).
var (
	ErrDisabled      = &Error{Status: 409, Code: "ai.disabled", Msg: "l'AI non è attiva in questa organizzazione"}
	ErrNotConfigured = &Error{Status: 409, Code: "ai.not_configured", Msg: "manca il modello del livello: l'amministratore deve completare le impostazioni dell'AI"}
	ErrBudget        = &Error{Status: 429, Code: "ai.budget_exhausted", Msg: "il budget mensile dell'AI è esaurito: le funzioni senza AI restano disponibili"}
	// ErrBudgetCall: il budget non è ancora esaurito, ma questa chiamata (token contati prima) lo supererebbe.
	ErrBudgetCall = &Error{Status: 429, Code: "ai.budget_exhausted", Msg: "il budget mensile dell'AI non basta per questa richiesta: prova con meno testo o aspetta il mese prossimo; le funzioni senza AI restano disponibili"}
	ErrNotFound   = &Error{Status: 404, Code: "ai.not_found", Msg: "non trovato"}
	ErrForbidden  = &Error{Status: 403, Code: "ai.forbidden", Msg: "operazione non consentita"}
)

// Error è un errore da mostrare così com'è, con il campo per i moduli.
type Error struct {
	Status int
	Code   string
	Field  string
	Msg    string
	Params map[string]any
}

func (e *Error) Error() string { return e.Msg }

func invalid(field, code, msg string) error {
	return &Error{Status: 422, Code: code, Field: field, Msg: msg}
}

// TierChoice è il modello di un livello.
type TierChoice struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// Route sposta una funzione su un altro livello o su un modello preciso (anche di un altro fornitore).
type Route struct {
	Tier       string `json:"tier,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
}

// Settings sono le impostazioni AI del tenant.
type Settings struct {
	Enabled            bool             `json:"enabled"`
	MaskPersonalData   bool             `json:"mask_personal_data"`
	MonthlyBudgetUSD   *float64         `json:"monthly_budget_usd"`
	MonthlyTokenBudget *int64           `json:"monthly_token_budget"`
	RetentionDays      int              `json:"retention_days"`
	Fast               TierChoice       `json:"fast"`
	Deep               TierChoice       `json:"deep"`
	Routes             map[string]Route `json:"routes"`
	// MCPEnabled: il server MCP è acceso (interruttore suo, indipendente da Enabled: il modello
	// è quello della persona e Regia non chiama nessun fornitore).
	MCPEnabled bool `json:"mcp_enabled"`
	// Saved: le impostazioni sono già state salvate una volta (altrimenti sono i valori di partenza).
	Saved bool `json:"saved"`
	// Regulated: il tenant ha normative attive (DORA, NIS2...): il mascheramento parte acceso.
	Regulated bool `json:"regulated"`
}

// ProviderRow è un fornitore del tenant, senza la chiave.
type ProviderRow struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	BaseURL       string   `json:"base_url"`
	HasKey        bool     `json:"has_key"`
	NoToolsModels []string `json:"no_tools_models"`
	PriceInput    *float64 `json:"price_input_usd"`
	PriceOutput   *float64 `json:"price_output_usd"`
	// Local: indirizzo su questa macchina o sulla rete interna: i dati non escono (niente mascheramento).
	Local  bool `json:"local"`
	secret []byte
}

// regulated: il tenant ha almeno una normativa (regolamento o direttiva) attiva.
func regulated(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from tenant_frameworks tf
		join framework_versions v on v.id = tf.framework_version_id
		join frameworks f on f.id = v.framework_id
		where tf.tenant_id = app_tenant() and tf.status = 'active' and f.kind in ('regulation','directive'))`).Scan(&ok)
	return ok, err
}

// LoadSettings legge le impostazioni; senza riga, i valori di partenza (spenta, 90 giorni,
// mascheramento acceso solo se il tenant è regolamentato).
func LoadSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	reg, err := regulated(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	st := Settings{RetentionDays: 90, Routes: map[string]Route{}, Regulated: reg, MaskPersonalData: reg}
	var fastP, deepP, fastM, deepM *string
	var routes []byte
	err = tx.QueryRow(ctx, `select enabled, mask_personal_data, monthly_budget_usd::float8, monthly_token_budget, retention_days,
		fast_provider_id::text, fast_model, deep_provider_id::text, deep_model, routes, mcp_enabled
		from ai_settings where tenant_id = app_tenant()`).Scan(&st.Enabled, &st.MaskPersonalData, &st.MonthlyBudgetUSD, &st.MonthlyTokenBudget,
		&st.RetentionDays, &fastP, &fastM, &deepP, &deepM, &routes, &st.MCPEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return Settings{}, err
	}
	st.Saved = true
	st.Fast = TierChoice{ProviderID: deref(fastP), Model: deref(fastM)}
	st.Deep = TierChoice{ProviderID: deref(deepP), Model: deref(deepM)}
	if len(routes) > 0 {
		if err := json.Unmarshal(routes, &st.Routes); err != nil {
			return Settings{}, err
		}
	}
	if st.Routes == nil {
		st.Routes = map[string]Route{}
	}
	return st, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SettingsInput è il salvataggio delle impostazioni (sostituisce tutto).
type SettingsInput struct {
	Enabled            bool             `json:"enabled"`
	MaskPersonalData   bool             `json:"mask_personal_data"`
	MonthlyBudgetUSD   *float64         `json:"monthly_budget_usd"`
	MonthlyTokenBudget *int64           `json:"monthly_token_budget"`
	RetentionDays      int              `json:"retention_days"`
	Fast               TierChoice       `json:"fast"`
	Deep               TierChoice       `json:"deep"`
	Routes             map[string]Route `json:"routes"`
	MCPEnabled         bool             `json:"mcp_enabled"`
}

// SaveSettings valida e salva le impostazioni (solo l'amministratore del tenant, controllato dall'API).
func (s *Service) SaveSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (Settings, error) {
	provs, err := s.listProviders(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	has := func(id string) bool {
		return slices.ContainsFunc(provs, func(p ProviderRow) bool { return p.ID == id })
	}
	checkTier := func(field string, c *TierChoice) error {
		c.ProviderID, c.Model = strings.TrimSpace(c.ProviderID), strings.TrimSpace(c.Model)
		switch {
		case c.ProviderID == "" && c.Model == "":
			return nil
		case c.ProviderID == "" || !has(c.ProviderID):
			return invalid(field, "ai.provider_unknown", "scegli un fornitore tra quelli configurati")
		case c.Model == "" || utf8.RuneCountInString(c.Model) > 200:
			return invalid(field, "ai.model_required", "scegli il modello del livello")
		}
		return nil
	}
	if err := checkTier("deep", &in.Deep); err != nil {
		return Settings{}, err
	}
	if err := checkTier("fast", &in.Fast); err != nil {
		return Settings{}, err
	}
	if in.Enabled && in.Deep.ProviderID == "" && in.Fast.ProviderID == "" {
		return Settings{}, invalid("deep", "ai.enable_needs_model", "per accendere l'AI serve almeno il modello di un livello")
	}
	if in.RetentionDays == 0 {
		in.RetentionDays = 90
	}
	if in.RetentionDays < 1 || in.RetentionDays > 3650 {
		return Settings{}, invalid("retention_days", "ai.retention_range", "la conservazione va da 1 a 3650 giorni")
	}
	if in.MonthlyBudgetUSD != nil && (*in.MonthlyBudgetUSD <= 0 || *in.MonthlyBudgetUSD > 1e9) {
		return Settings{}, invalid("monthly_budget_usd", "ai.budget_range", "il budget va lasciato vuoto o maggiore di zero")
	}
	if in.MonthlyTokenBudget != nil && *in.MonthlyTokenBudget <= 0 {
		return Settings{}, invalid("monthly_token_budget", "ai.budget_range", "il budget va lasciato vuoto o maggiore di zero")
	}
	routes := map[string]Route{}
	for key, r := range in.Routes {
		if _, ok := featureByKey(key); !ok {
			return Settings{}, invalid("routes", "ai.feature_unknown", "funzione sconosciuta: "+key)
		}
		r.Tier, r.ProviderID, r.Model = strings.TrimSpace(r.Tier), strings.TrimSpace(r.ProviderID), strings.TrimSpace(r.Model)
		switch {
		case r.ProviderID != "":
			if !has(r.ProviderID) || r.Model == "" || utf8.RuneCountInString(r.Model) > 200 {
				return Settings{}, invalid("routes", "ai.route_invalid", "per la funzione "+key+" scegli fornitore e modello")
			}
			routes[key] = Route{ProviderID: r.ProviderID, Model: r.Model}
		case r.Tier == TierFast || r.Tier == TierDeep:
			if f, _ := featureByKey(key); f.Tier != r.Tier {
				routes[key] = Route{Tier: r.Tier}
			}
		case r.Tier == "":
		default:
			return Settings{}, invalid("routes", "ai.route_invalid", "livello sconosciuto per la funzione "+key)
		}
	}
	rj, _ := json.Marshal(routes)
	if _, err := tx.Exec(ctx, `insert into ai_settings (tenant_id, enabled, mask_personal_data, monthly_budget_usd, monthly_token_budget,
			retention_days, fast_provider_id, fast_model, deep_provider_id, deep_model, routes, mcp_enabled, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		on conflict (tenant_id) do update set enabled = excluded.enabled, mask_personal_data = excluded.mask_personal_data,
			monthly_budget_usd = excluded.monthly_budget_usd, monthly_token_budget = excluded.monthly_token_budget,
			retention_days = excluded.retention_days, fast_provider_id = excluded.fast_provider_id, fast_model = excluded.fast_model,
			deep_provider_id = excluded.deep_provider_id, deep_model = excluded.deep_model, routes = excluded.routes,
			mcp_enabled = excluded.mcp_enabled, updated_at = now()`,
		in.Enabled, in.MaskPersonalData, in.MonthlyBudgetUSD, in.MonthlyTokenBudget, in.RetentionDays,
		nullable(in.Fast.ProviderID), nullable(in.Fast.Model), nullable(in.Deep.ProviderID), nullable(in.Deep.Model), rj, in.MCPEnabled); err != nil {
		return Settings{}, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "ai.settings.update", EntityType: "ai_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"enabled": in.Enabled, "mask_personal_data": in.MaskPersonalData, "retention_days": in.RetentionDays,
			"fast_model": in.Fast.Model, "deep_model": in.Deep.Model, "routes": len(routes), "mcp_enabled": in.MCPEnabled,
			"monthly_budget_usd": in.MonthlyBudgetUSD, "monthly_token_budget": in.MonthlyTokenBudget}}); err != nil {
		return Settings{}, err
	}
	return LoadSettings(ctx, tx)
}

// ---------------------------------------------------------------------
// Fornitori
// ---------------------------------------------------------------------

func secretContext(id string) string { return "ai-provider:" + id }

func (s *Service) listProviders(ctx context.Context, tx pgx.Tx) ([]ProviderRow, error) {
	rows, err := tx.Query(ctx, `select id::text, kind, name, coalesce(base_url, ''), secret_enc, no_tools_models,
		price_input_usd::float8, price_output_usd::float8 from ai_providers order by created_at, name`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProviderRow, error) {
		var p ProviderRow
		err := r.Scan(&p.ID, &p.Kind, &p.Name, &p.BaseURL, &p.secret, &p.NoToolsModels, &p.PriceInput, &p.PriceOutput)
		p.HasKey = len(p.secret) > 0
		p.Local = s.isLocal(p)
		if p.NoToolsModels == nil {
			p.NoToolsModels = []string{}
		}
		return p, err
	})
	if list == nil {
		list = []ProviderRow{}
	}
	return list, err
}

// Providers elenca i fornitori del tenant (senza chiavi).
func (s *Service) Providers(ctx context.Context, tx pgx.Tx) ([]ProviderRow, error) {
	return s.listProviders(ctx, tx)
}

func (s *Service) provider(ctx context.Context, tx pgx.Tx, id string) (*ProviderRow, error) {
	list, err := s.listProviders(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// isLocal: il fornitore è sulla rete interna o su questa macchina (i dati non escono).
func (s *Service) isLocal(p ProviderRow) bool {
	k, ok := s.kinds[p.Kind]
	if ok && !k.External {
		return true
	}
	if p.BaseURL == "" {
		return false
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	return LocalHost(u.Hostname())
}

// LocalHost dice se un host è locale: localhost, nomi della rete di casa, indirizzi privati.
func LocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" {
		return true
	}
	for _, suf := range []string{".localhost", ".local", ".lan", ".internal", ".home.arpa"} {
		if strings.HasSuffix(h, suf) {
			return true
		}
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		ip = ip.Unmap()
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return false
}

// ProviderInput crea o modifica un fornitore. APIKey vuota = invariata (RemoveKey la toglie).
type ProviderInput struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	BaseURL       string   `json:"base_url"`
	APIKey        string   `json:"api_key"`
	RemoveKey     bool     `json:"remove_key"`
	NoToolsModels []string `json:"no_tools_models"`
	PriceInput    *float64 `json:"price_input_usd"`
	PriceOutput   *float64 `json:"price_output_usd"`
}

func (s *Service) cleanProvider(in *ProviderInput, prev *ProviderRow) (Kind, error) {
	if prev != nil {
		in.Kind = prev.Kind
	}
	k, ok := s.kinds[in.Kind]
	if !ok {
		return Kind{}, invalid("kind", "ai.kind_unknown", "tipo di fornitore non disponibile")
	}
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return k, invalid("name", "ai.name_required", "serve un nome (al massimo 80 caratteri)")
	}
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if k.NeedsURL {
		base, err := s.normalizeURL(in.BaseURL)
		if err != nil {
			return k, &Error{Status: 422, Code: "ai.base_url_invalid", Field: "base_url", Msg: err.Error()}
		}
		in.BaseURL = base
	} else {
		in.BaseURL = ""
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if len(in.APIKey) > 4000 || strings.ContainsAny(in.APIKey, " \n\r\t") {
		return k, invalid("api_key", "ai.key_invalid", "chiave non valida")
	}
	if k.NeedsKey && in.APIKey == "" && (prev == nil || !prev.HasKey || in.RemoveKey) {
		return k, invalid("api_key", "ai.key_required", "serve la chiave del fornitore")
	}
	var models []string
	for _, m := range in.NoToolsModels {
		m = strings.TrimSpace(m)
		if m != "" && utf8.RuneCountInString(m) <= 200 && !slices.Contains(models, m) {
			models = append(models, m)
		}
	}
	if len(models) > 100 {
		return k, invalid("no_tools_models", "ai.too_many_models", "al massimo 100 modelli")
	}
	if models == nil {
		models = []string{}
	}
	in.NoToolsModels = models
	for _, p := range []*float64{in.PriceInput, in.PriceOutput} {
		if p != nil && (*p < 0 || *p > 10000) {
			return k, invalid("price_input_usd", "ai.price_range", "prezzo per milione di token tra 0 e 10000 dollari")
		}
	}
	return k, nil
}

// normalizeURL controlla l'indirizzo base con la politica di rete dei connettori.
func (s *Service) normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("serve l'indirizzo base del servizio (es. http://localhost:11434/v1)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("indirizzo non valido: serve http:// o https:// con l'host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("l'indirizzo non può contenere credenziali, parametri o frammenti")
	}
	local := LocalHost(u.Hostname())
	if local && !s.o.AllowPrivate && !s.o.Dev {
		return "", errors.New("l'operatore non ha consentito gli indirizzi interni (REGIA_CONNECTOR_ALLOW_PRIVATE)")
	}
	if !local && u.Scheme != "https" && !s.o.Dev {
		return "", errors.New("verso un servizio esterno serve https://")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return "", errors.New("indirizzo non consentito")
	}
	if net.ParseIP(u.Hostname()) == nil && len(u.Hostname()) > 253 {
		return "", errors.New("nome dell'host troppo lungo")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

// CreateProvider aggiunge un fornitore; la chiave si cifra con la chiave applicativa legata all'id.
func (s *Service) CreateProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ProviderInput) (*ProviderRow, error) {
	if _, err := s.cleanProvider(&in, nil); err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from ai_providers`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= 20 {
		return nil, invalid("name", "ai.too_many_providers", "al massimo 20 fornitori")
	}
	id := db.NewID()
	var sealed []byte
	if in.APIKey != "" {
		var err error
		if sealed, err = s.o.Box.Seal([]byte(in.APIKey), secretContext(id)); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `insert into ai_providers (tenant_id, id, kind, name, base_url, secret_enc, no_tools_models, price_input_usd, price_output_usd)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, id, in.Kind, in.Name, nullable(in.BaseURL), sealed, in.NoToolsModels, in.PriceInput, in.PriceOutput); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "ai.provider.create", EntityType: "ai_provider", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"kind": in.Kind, "base_url": in.BaseURL, "key": in.APIKey != ""}}); err != nil {
		return nil, err
	}
	return s.provider(ctx, tx, id)
}

// UpdateProvider modifica un fornitore (il tipo non cambia).
func (s *Service) UpdateProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ProviderInput) (*ProviderRow, error) {
	prev, err := s.provider(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.cleanProvider(&in, prev); err != nil {
		return nil, err
	}
	sealed := prev.secret
	keyChanged := false
	switch {
	case in.APIKey != "":
		if sealed, err = s.o.Box.Seal([]byte(in.APIKey), secretContext(id)); err != nil {
			return nil, err
		}
		keyChanged = true
	case in.RemoveKey:
		sealed, keyChanged = nil, true
	}
	if _, err := tx.Exec(ctx, `update ai_providers set name = $2, base_url = $3, secret_enc = $4, no_tools_models = $5,
		price_input_usd = $6, price_output_usd = $7, updated_at = now() where id = $1`,
		id, in.Name, nullable(in.BaseURL), sealed, in.NoToolsModels, in.PriceInput, in.PriceOutput); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "ai.provider.update", EntityType: "ai_provider", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"base_url": in.BaseURL, "key_changed": keyChanged}}); err != nil {
		return nil, err
	}
	return s.provider(ctx, tx, id)
}

// DeleteProvider toglie un fornitore che nessun livello e nessuna funzione usa.
func (s *Service) DeleteProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if _, err := s.provider(ctx, tx, id); err != nil {
		return err
	}
	st, err := LoadSettings(ctx, tx)
	if err != nil {
		return err
	}
	used := st.Fast.ProviderID == id || st.Deep.ProviderID == id
	for _, r := range st.Routes {
		used = used || r.ProviderID == id
	}
	if used {
		return &Error{Status: 409, Code: "ai.provider_in_use", Msg: "il fornitore è usato da un livello o da una funzione: cambiali prima di toglierlo"}
	}
	if _, err := tx.Exec(ctx, `delete from ai_providers where id = $1`, id); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "ai.provider.delete", EntityType: "ai_provider", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

// instance crea l'adattatore del fornitore con la chiave decifrata (solo in memoria).
func (s *Service) instance(p *ProviderRow) (Provider, Kind, error) {
	k, ok := s.kinds[p.Kind]
	if !ok {
		return nil, Kind{}, &Error{Status: 409, Code: "ai.kind_unavailable", Msg: "il tipo di fornitore non è disponibile in questa installazione"}
	}
	cfg := ProviderConfig{BaseURL: p.BaseURL, Local: p.Local}
	if len(p.secret) > 0 {
		key, err := s.o.Box.Open(p.secret, secretContext(p.ID))
		if err != nil {
			return nil, k, &Error{Status: 409, Code: "ai.key_unreadable", Msg: "la chiave del fornitore non si decifra: inseriscila di nuovo"}
		}
		cfg.APIKey = string(key)
	}
	hc, err := s.httpClient(p)
	if err != nil {
		return nil, k, err
	}
	cfg.HTTPClient = hc
	prov, err := k.New(cfg)
	return prov, k, err
}

// ProviderModels elenca i modelli del fornitore: quelli che il servizio dichiara (installati
// per Ollama e LM Studio, dell'account per Anthropic) uniti a quelli proposti da Regia.
func (s *Service) ProviderModels(ctx context.Context, tx pgx.Tx, id string) ([]ModelInfo, error) {
	p, err := s.provider(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	prov, k, err := s.instance(p)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := prov.Models(cctx)
	if err != nil {
		return nil, err
	}
	for _, m := range k.Proposed {
		i := slices.IndexFunc(list, func(x ModelInfo) bool { return x.ID == m.ID })
		if i < 0 {
			list = append(list, m)
		} else if list[i].Tier == "" {
			list[i].Tier = m.Tier
		}
	}
	return list, nil
}

// Kinds sono i tipi di fornitore disponibili, nell'ordine di registrazione.
func (s *Service) Kinds() []Kind {
	out := make([]Kind, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.kinds[k])
	}
	return out
}

// toolsFor: il modello del fornitore sa chiamare strumenti?
func toolsFor(p *ProviderRow, model string) bool {
	return !slices.Contains(p.NoToolsModels, model)
}
