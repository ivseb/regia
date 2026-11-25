package grc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// StarterKeys sono i cataloghi dei pacchetti di partenza, nell'ordine in cui si offrono.
var StarterKeys = []string{"dora", "nis2", "iso27001", "gdpr", "agid_acn"}

func starterCatalog(key string) (*CatalogFile, error) {
	if !slices.Contains(StarterKeys, key) {
		return nil, engine.ErrNotFound
	}
	raw, err := starters.ReadFile("starter/catalogs/" + key + ".json")
	if err != nil {
		return nil, err
	}
	c, err := DecodeCatalog(raw)
	if err != nil {
		return nil, fmt.Errorf("pacchetto %s: %w", key, err)
	}
	return c, c.Validate()
}

// Package è la configurazione della conformità in JSON (regia.grc/v1): impostazioni,
// regole di applicabilità, corrispondenze tra normative e, nell'export, i cataloghi.
type Package struct {
	Format      string         `json:"format"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	ExportedAt  string         `json:"exported_at,omitempty"`
	Settings    *Settings      `json:"settings,omitempty"`
	Catalogs    []*CatalogFile `json:"catalogs,omitempty"`
	Rules       []RuleInput    `json:"rules,omitempty"`
	Mappings    []MappingInput `json:"mappings,omitempty"`
}

// ParsePackage legge un pacchetto rifiutando i campi sconosciuti.
func ParsePackage(raw []byte) (*Package, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	p := &Package{}
	if err := dec.Decode(p); err != nil {
		return nil, engine.InvalidValue("package", "JSON non valido: %v", err)
	}
	if p.Format != PackageFormat {
		return nil, engine.InvalidValue("format", "formato %q non riconosciuto (atteso %s)", p.Format, PackageFormat)
	}
	return p, nil
}

func starterPackage() (*Package, error) {
	raw, err := starters.ReadFile("starter/conformita_standard.json")
	if err != nil {
		return nil, err
	}
	return ParsePackage(raw)
}

// StarterResult dice cosa ha portato un pacchetto di partenza.
type StarterResult struct {
	Framework *FrameworkInfo `json:"framework"`
	Rules     []string       `json:"rules"`
	Mappings  int            `json:"mappings"`
}

// ImportStarter importa il catalogo di un pacchetto di partenza (pubblicato, se ha
// requisiti; altrimenti resta in bozza da completare), con le regole di applicabilità che
// lo riguardano e le corrispondenze verso le normative già presenti.
func (s *Service) ImportStarter(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, enable bool) (*StarterResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	c, err := starterCatalog(key)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from frameworks where owner_tenant_id = app_tenant() and key = $1)", c.Framework.Key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, engine.Duplicate("key", "%s c'è già: per aggiornarla importa una nuova versione", c.Framework.Key)
	}
	publish := len(c.Assessable()) > 0
	fw, err := s.ImportCatalog(ctx, tx, actor, c, ImportOptions{Publish: publish, Enable: enable && publish, Starter: key})
	if err != nil {
		return nil, err
	}
	res := &StarterResult{Framework: fw, Rules: []string{}}
	p, err := starterPackage()
	if err != nil {
		return nil, err
	}
	if p.Settings != nil {
		if _, err := tx.Exec(ctx, `insert into grc_settings (tenant_id, expiry_notice_days, default_validity_days, process_types)
			values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			p.Settings.ExpiryNoticeDays, p.Settings.DefaultValidityDays, p.Settings.ProcessTypes); err != nil {
			return nil, err
		}
	}
	if publish {
		var mine []RuleInput
		for _, r := range p.Rules {
			if r.Framework == c.Framework.Key {
				mine = append(mine, r)
			}
		}
		if res.Rules, err = s.addRules(ctx, tx, actor, mine); err != nil {
			return nil, err
		}
		if res.Mappings, err = s.applyMappings(ctx, tx, actor, p.Mappings, true); err != nil {
			return nil, err
		}
		if _, err := s.Recompute(ctx, tx, actor); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// ExportPackage restituisce la configurazione della conformità: cataloghi del tenant (versione
// corrente, o la bozza se non c'è), regole, corrispondenze del tenant e impostazioni.
func (s *Service) ExportPackage(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Package, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	st.RecomputedAt = nil
	p := &Package{Format: PackageFormat, ExportedAt: s.Now().UTC().Format(time.RFC3339), Settings: st, Catalogs: []*CatalogFile{}}
	fs, err := s.loadFrameworks(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		v := f.Current
		if v == nil {
			v = f.Draft
		}
		if f.System || v == nil {
			continue
		}
		c, err := catalogFromDB(ctx, tx, f, v)
		if err != nil {
			return nil, err
		}
		p.Catalogs = append(p.Catalogs, c)
	}
	rules, err := s.Rules(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, r := range rules.Items {
		p.Rules = append(p.Rules, r.RuleInput)
	}
	ms, err := s.Mappings(ctx, tx, actor, "")
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if !m.System {
			p.Mappings = append(p.Mappings, MappingInput{From: m.From, To: m.To, Relation: m.Relation, Source: m.Source})
		}
	}
	return p, nil
}

// ImportResult riassume l'import di un pacchetto.
type ImportResult struct {
	Catalogs []string `json:"catalogs"`
	Rules    []string `json:"rules"`
	Mappings int      `json:"mappings"`
	Settings bool     `json:"settings"`
}

// ImportPackage porta un pacchetto nel tenant: cataloghi (come nuova versione pubblicata, o
// bozza se publish è falso), impostazioni, regole (sostituite se hanno la stessa chiave) e
// corrispondenze (quelle con normative mancanti si saltano).
func (s *Service) ImportPackage(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Package, publish bool) (*ImportResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	res := &ImportResult{Catalogs: []string{}, Rules: []string{}}
	if p.Settings != nil {
		if _, err := s.UpdateSettings(ctx, tx, actor, *p.Settings); err != nil {
			return nil, err
		}
		res.Settings = true
	}
	for _, c := range p.Catalogs {
		if c.Format == "" {
			c.Format = CatalogFormat
		}
		if _, err := s.ImportCatalog(ctx, tx, actor, c, ImportOptions{Publish: publish && len(c.Assessable()) > 0}); err != nil {
			return nil, fmt.Errorf("catalogo %s: %w", c.Framework.Key, err)
		}
		res.Catalogs = append(res.Catalogs, c.Framework.Key)
	}
	var err error
	if res.Rules, err = s.addRules(ctx, tx, actor, p.Rules); err != nil {
		return nil, err
	}
	if res.Mappings, err = s.applyMappings(ctx, tx, actor, p.Mappings, true); err != nil {
		return nil, err
	}
	if _, err := s.Recompute(ctx, tx, actor); err != nil {
		return nil, err
	}
	return res, audited(ctx, tx, actor, "grc.package.import", "grc", "", map[string]any{
		"catalogs": res.Catalogs, "rules": res.Rules, "mappings": res.Mappings})
}
