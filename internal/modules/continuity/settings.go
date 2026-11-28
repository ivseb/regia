package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/i18n"
)

// Configurazione del tenant: niente è scritto nel codice tranne il pacchetto di partenza. Ogni
// salvataggio incrementa la versione e lascia una riga nello storico; la BIA ricorda la versione
// usata, così si vede se le scale sono cambiate dopo la compilazione.

// Level è un livello di una scala d'impatto.
type Level struct {
	Value int    `json:"value"`
	Label string `json:"label"`
}

// Category è una categoria d'impatto (economico, normativo…) con la sua scala.
type Category struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Intolerable int     `json:"intolerable"` // da questo livello in su l'impatto non è tollerabile
	Levels      []Level `json:"levels"`
}

// Horizon è un orizzonte temporale della BIA.
type Horizon struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Minutes int    `json:"minutes"`
}

// TestType è un tipo di test di resilienza.
type TestType struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Config è la configurazione della continuità.
type Config struct {
	Categories          []Category `json:"categories"`
	Horizons            []Horizon  `json:"horizons"`
	TestTypes           []TestType `json:"test_types"`
	TestMaxAgeDays      int        `json:"test_max_age_days"`
	BIAReviewMonths     int        `json:"bia_review_months"`
	CriticalMTPDMinutes int        `json:"critical_mtpd_minutes"`
}

// DefaultConfig è il pacchetto di partenza, nella lingua del tenant.
func DefaultConfig(lang string) Config {
	n := func(s string) string { return i18n.Name(lang, s) }
	levels := func() []Level {
		return []Level{{1, n("Trascurabile")}, {2, n("Basso")}, {3, n("Medio")}, {4, n("Alto")}, {5, n("Grave")}}
	}
	return Config{
		Categories: []Category{
			{Key: "economico", Name: n("Economico"), Intolerable: 4, Levels: levels()},
			{Key: "normativo", Name: n("Normativo"), Intolerable: 4, Levels: levels()},
			{Key: "reputazionale", Name: n("Reputazionale"), Intolerable: 4, Levels: levels()},
			{Key: "clienti", Name: n("Clienti"), Intolerable: 4, Levels: levels()},
		},
		Horizons: []Horizon{
			{Key: "1h", Name: n("1 ora"), Minutes: 60},
			{Key: "4h", Name: n("4 ore"), Minutes: 240},
			{Key: "1g", Name: n("1 giorno"), Minutes: 1440},
			{Key: "3g", Name: n("3 giorni"), Minutes: 4320},
			{Key: "1s", Name: n("1 settimana"), Minutes: 10080},
		},
		TestTypes: []TestType{
			{Key: "tabletop", Name: n("Tabletop"), Description: n("Esercitazione a tavolino sullo scenario, senza toccare i sistemi.")},
			{Key: "failover", Name: n("Failover"), Description: n("Passaggio al sito o al sistema secondario e ritorno.")},
			{Key: "restore", Name: n("Ripristino da backup"), Description: n("Ripristino dei dati da backup, con misura di RTO e RPO.")},
			{Key: "scenario", Name: n("Scenario"), Description: n("Simulazione completa di uno scenario di interruzione.")},
		},
		TestMaxAgeDays:      365,
		BIAReviewMonths:     12,
		CriticalMTPDMinutes: 4320,
	}
}

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,39}$`)

func cfgErr(msg string, args ...any) error {
	return engine.InvalidParams("validation.continuity.config", "config", nil, msg, args...)
}

// validate controlla la configurazione e ordina gli orizzonti per durata.
func (c *Config) validate() error {
	if len(c.Categories) == 0 || len(c.Categories) > 12 {
		return cfgErr("servono da 1 a 12 categorie d'impatto")
	}
	if len(c.Horizons) == 0 || len(c.Horizons) > 12 {
		return cfgErr("servono da 1 a 12 orizzonti temporali")
	}
	if len(c.TestTypes) == 0 || len(c.TestTypes) > 20 {
		return cfgErr("servono da 1 a 20 tipi di test")
	}
	seen := map[string]bool{}
	for i := range c.Categories {
		k := &c.Categories[i]
		k.Name = strings.TrimSpace(k.Name)
		if !keyRe.MatchString(k.Key) || seen["c:"+k.Key] || k.Name == "" {
			return cfgErr("categoria %q: chiave non valida o doppia, o nome mancante", k.Key)
		}
		seen["c:"+k.Key] = true
		if len(k.Levels) < 2 || len(k.Levels) > 10 {
			return cfgErr("categoria %q: servono da 2 a 10 livelli", k.Key)
		}
		vals := map[int]bool{}
		for j := range k.Levels {
			l := &k.Levels[j]
			l.Label = strings.TrimSpace(l.Label)
			if l.Value < 0 || l.Value > 100 || vals[l.Value] || l.Label == "" {
				return cfgErr("categoria %q: livelli con valori distinti e un'etichetta", k.Key)
			}
			vals[l.Value] = true
		}
		sort.Slice(k.Levels, func(a, b int) bool { return k.Levels[a].Value < k.Levels[b].Value })
		if !vals[k.Intolerable] {
			return cfgErr("categoria %q: il livello intollerabile deve essere uno dei livelli", k.Key)
		}
	}
	for i := range c.Horizons {
		h := &c.Horizons[i]
		h.Name = strings.TrimSpace(h.Name)
		if !keyRe.MatchString(h.Key) || seen["h:"+h.Key] || h.Name == "" || h.Minutes <= 0 || h.Minutes > 525600 {
			return cfgErr("orizzonte %q: chiave non valida o doppia, nome mancante o durata fuori intervallo", h.Key)
		}
		seen["h:"+h.Key] = true
	}
	sort.SliceStable(c.Horizons, func(a, b int) bool { return c.Horizons[a].Minutes < c.Horizons[b].Minutes })
	for i := 1; i < len(c.Horizons); i++ {
		if c.Horizons[i].Minutes == c.Horizons[i-1].Minutes {
			return cfgErr("due orizzonti hanno la stessa durata")
		}
	}
	for i := range c.TestTypes {
		t := &c.TestTypes[i]
		t.Name, t.Description = strings.TrimSpace(t.Name), strings.TrimSpace(t.Description)
		if !keyRe.MatchString(t.Key) || seen["t:"+t.Key] || t.Name == "" || len(t.Description) > 500 {
			return cfgErr("tipo di test %q: chiave non valida o doppia, o nome mancante", t.Key)
		}
		seen["t:"+t.Key] = true
	}
	if c.TestMaxAgeDays < 30 || c.TestMaxAgeDays > 1825 {
		return cfgErr("l'età massima di un test va da 30 a 1825 giorni")
	}
	if c.BIAReviewMonths < 1 || c.BIAReviewMonths > 60 {
		return cfgErr("il riesame della BIA va da 1 a 60 mesi")
	}
	if c.CriticalMTPDMinutes < 0 || c.CriticalMTPDMinutes > 525600 {
		return cfgErr("la soglia della funzione critica va da 0 a 525600 minuti")
	}
	return nil
}

func (c *Config) testType(key string) (TestType, bool) {
	i := slices.IndexFunc(c.TestTypes, func(t TestType) bool { return t.Key == key })
	if i < 0 {
		return TestType{}, false
	}
	return c.TestTypes[i], true
}

// SettingsView è la configurazione in vigore.
type SettingsView struct {
	Ready     bool       `json:"ready"`
	Version   int        `json:"version"`
	Config    Config     `json:"config"`
	UpdatedBy *string    `json:"updated_by"`
	UpdatedAt *time.Time `json:"updated_at"`
	Can       Can        `json:"can"`
}

// loadConfig legge la configurazione; senza riga (modulo non preparato) dà il pacchetto di
// partenza con versione 0.
func loadConfig(ctx context.Context, tx pgx.Tx) (*Config, int, error) {
	var raw []byte
	var version int
	err := tx.QueryRow(ctx, `select config, version from continuity_settings`).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		lang, _ := i18n.TenantLang(ctx, tx)
		c := DefaultConfig(lang)
		return &c, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, 0, err
	}
	sort.SliceStable(c.Horizons, func(a, b int) bool { return c.Horizons[a].Minutes < c.Horizons[b].Minutes })
	return &c, version, nil
}

// Settings restituisce la configurazione.
func (s *Service) Settings(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*SettingsView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	ready, err := isReady(ctx, tx)
	if err != nil {
		return nil, err
	}
	c, v, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &SettingsView{Ready: ready, Version: v, Config: *c, Can: can(actor)}
	err = tx.QueryRow(ctx, `select (select m.display_name from members m where m.id = cs.updated_by), cs.updated_at from continuity_settings cs`).
		Scan(&out.UpdatedBy, &out.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

// SettingsInput è il salvataggio della configurazione.
type SettingsInput struct {
	Version int    `json:"version"`
	Config  Config `json:"config"`
}

// SaveSettings salva una versione nuova della configurazione (continuity.admin).
func (s *Service) SaveSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (*SettingsView, error) {
	if !actor.Has(PermAdmin) {
		return nil, engine.ErrForbidden
	}
	if err := requireReady(ctx, tx); err != nil {
		return nil, err
	}
	if err := in.Config.validate(); err != nil {
		return nil, err
	}
	var current int
	if err := tx.QueryRow(ctx, `select version from continuity_settings for update`).Scan(&current); err != nil {
		return nil, err
	}
	if in.Version != current {
		return nil, engine.ErrConflict // 409: la configurazione è cambiata, si ricarica
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return nil, err
	}
	next := current + 1
	if _, err := tx.Exec(ctx, `update continuity_settings set config = $1, version = $2, updated_by = $3, updated_at = now()`, raw, next, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into continuity_settings_versions (tenant_id, version, config, created_by) values (app_tenant(), $1, $2, $3)`,
		next, raw, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.settings.update", EntityType: "continuity_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"version": next}}); err != nil {
		return nil, err
	}
	return s.Settings(ctx, tx, actor)
}

// SettingsVersion è una voce dello storico.
type SettingsVersion struct {
	Version int       `json:"version"`
	By      *string   `json:"by"`
	At      time.Time `json:"at"`
	Config  Config    `json:"config"`
}

// SettingsVersions è lo storico delle versioni, dalla più recente.
func (s *Service) SettingsVersions(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]SettingsVersion, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `select v.version, m.display_name, v.created_at, v.config from continuity_settings_versions v
		left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by order by v.version desc limit 100`)
	if err != nil {
		return nil, err
	}
	out := []SettingsVersion{}
	var raw []byte
	var it SettingsVersion
	if _, err := pgx.ForEachRow(rows, []any{&it.Version, &it.By, &it.At, &raw}, func() error {
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		it.Config = c
		out = append(out, it)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
