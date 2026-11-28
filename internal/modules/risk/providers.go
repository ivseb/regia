package risk

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/db"
)

// Fornitori ICT e registro delle informazioni (DORA, art. 28): anagrafica del fornitore,
// contratti con servizi, funzioni supportate, criticità, ubicazione dei dati, subappalti,
// strategia di uscita e date; applicazioni servite (relazione provided_by). Un fornitore è
// "critico" se serve un'applicazione che supporta una funzione critica o importante, oppure
// se un suo contratto lo dichiara: è un fatto calcolato, non un dato inserito.

var leiRe = regexp.MustCompile(`^[A-Z0-9]{18}[0-9]{2}$`)
var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

var substitutability = map[string]string{"easy": "facile", "medium": "media", "hard": "difficile", "none": "impossibile"}

func normSubst(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	for en, it := range substitutability {
		if s == en || s == it {
			return en, true
		}
	}
	return "", false
}

var critNames = map[string]string{"low": "bassa", "medium": "media", "high": "alta", "critical": "critica"}
var critRank = map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}

func normCriticality(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	for en, it := range critNames {
		if s == en || s == it {
			return en, true
		}
	}
	return "", false
}

// ProviderInput crea o modifica un fornitore. Nella modifica solo i campi valorizzati cambiano.
type ProviderInput struct {
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	LegalName        *string  `json:"legal_name"`
	LEI              *string  `json:"lei"`
	Country          *string  `json:"country"`
	ProviderType     *string  `json:"provider_type"`
	Intragroup       *bool    `json:"is_intragroup"`
	Substitutability *string  `json:"substitutability"`
	OwnerID          *string  `json:"owner_id"`
	Applications     []string `json:"applications"` // solo alla creazione
}

func (in *ProviderInput) validate() error {
	if in.LEI != nil {
		v := strings.ToUpper(strings.TrimSpace(*in.LEI))
		if v != "" && !leiRe.MatchString(v) {
			return engine.InvalidValue("lei", "codice LEI non valido (20 caratteri: lettere maiuscole e cifre)")
		}
		in.LEI = &v
	}
	if in.Country != nil {
		v := strings.ToUpper(strings.TrimSpace(*in.Country))
		if v != "" && !countryRe.MatchString(v) {
			return engine.InvalidCode("validation.risk.country_code", "country", "paese: codice ISO di due lettere (es. IT)")
		}
		in.Country = &v
	}
	if in.Substitutability != nil {
		v, ok := normSubst(*in.Substitutability)
		if !ok {
			return engine.InvalidValue("substitutability", "sostituibilità non ammessa (facile, media, difficile, impossibile)")
		}
		in.Substitutability = &v
	}
	for _, p := range []*string{in.LegalName, in.ProviderType} {
		if p != nil {
			*p = strings.TrimSpace(*p)
			if len(*p) > 300 {
				return engine.TooLong("legal_name", 300, "testo troppo lungo (massimo 300 caratteri)")
			}
		}
	}
	return nil
}

// CreateProvider crea un fornitore ICT (risk.admin).
func (s *Service) CreateProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ProviderInput) (*ProviderView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, engine.Required("name", "il nome del fornitore è obbligatorio")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if ok, err := typeExists(ctx, tx, ProviderType); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.InvalidCode("validation.risk.providers_not_configured", "type", "i fornitori ICT non sono configurati in questo tenant")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: ProviderSpace, TypeKey: ProviderType, Title: *in.Name,
		BodyText: deref(in.Description), DeferStart: true})
	if err != nil {
		return nil, err
	}
	legal := strings.TrimSpace(deref(in.LegalName))
	if legal == "" {
		legal = strings.TrimSpace(*in.Name)
	}
	if _, err := tx.Exec(ctx, `insert into ext_ict_provider (tenant_id, element_id, legal_name) values (app_tenant(), $1, $2)`, el.ID, legal); err != nil {
		return nil, err
	}
	in.LegalName = &legal
	if err := writeProvider(ctx, tx, el.ID, in); err != nil {
		return nil, err
	}
	if err := setOwner(ctx, tx, actor, el.ID, RoleProviderOwner, deref(in.OwnerID)); err != nil {
		return nil, err
	}
	for _, a := range in.Applications {
		if _, err := s.LinkApplication(ctx, tx, actor, el.ID, a); err != nil {
			return nil, err
		}
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "ict_provider.create", ProviderType, el.ID, nil); err != nil {
		return nil, err
	}
	return s.ProviderView(ctx, tx, actor, el.ID)
}

func writeProvider(ctx context.Context, tx pgx.Tx, id string, in ProviderInput) error {
	_, err := tx.Exec(ctx, `update ext_ict_provider set
		legal_name = coalesce(nullif($2, ''), legal_name),
		lei = case when $9 then nullif($3, '') else lei end,
		country = case when $10 then nullif($4, '') else country end,
		provider_type = case when $11 then nullif($5, '') else provider_type end,
		is_intragroup = coalesce($6, is_intragroup),
		substitutability = case when $12 then nullif($7, '') else substitutability end
		where element_id = $1 and $8`, id, deref(in.LegalName), deref(in.LEI), deref(in.Country), deref(in.ProviderType), in.Intragroup,
		deref(in.Substitutability), true, in.LEI != nil, in.Country != nil, in.ProviderType != nil, in.Substitutability != nil)
	return err
}

// UpdateProvider modifica il fornitore (risk.admin).
func (s *Service) UpdateProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ProviderInput) (*ProviderView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, ProviderType)
	if err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Name != nil || in.Description != nil {
		if _, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: el.Version, Title: in.Name, BodyText: in.Description}); err != nil {
			return nil, err
		}
	}
	if err := writeProvider(ctx, tx, el.ID, in); err != nil {
		return nil, err
	}
	if in.OwnerID != nil {
		if err := setOwner(ctx, tx, actor, el.ID, RoleProviderOwner, *in.OwnerID); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "ict_provider.update", ProviderType, el.ID, nil); err != nil {
		return nil, err
	}
	return s.ProviderView(ctx, tx, actor, el.ID)
}

// LinkApplication collega un'applicazione al fornitore (provided_by: applicazione → fornitore).
func (s *Service) LinkApplication(ctx context.Context, tx pgx.Tx, actor engine.Actor, providerRef, appRef string) (*ProviderView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	p, err := s.resolveTyped(ctx, tx, actor, providerRef, ProviderType)
	if err != nil {
		return nil, err
	}
	app, err := s.resolveTyped(ctx, tx, actor, appRef, AppType)
	if errors.Is(err, engine.ErrNotFound) {
		return nil, engine.NotFound("application", "application", "applicazione %q inesistente", appRef)
	}
	if err != nil {
		return nil, err
	}
	if _, err := relate(ctx, tx, actor, RelProvidedBy, app.ID, p.ID); err != nil {
		return nil, err
	}
	return s.ProviderView(ctx, tx, actor, p.ID)
}

// UnlinkApplication toglie il collegamento (e l'applicazione dai contratti del fornitore).
func (s *Service) UnlinkApplication(ctx context.Context, tx pgx.Tx, actor engine.Actor, providerRef, appRef string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	p, err := s.resolveTyped(ctx, tx, actor, providerRef, ProviderType)
	if err != nil {
		return err
	}
	app, err := s.resolveTyped(ctx, tx, actor, appRef, AppType)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from ict_contract_applications ca using ict_contracts c
		where c.tenant_id = ca.tenant_id and c.id = ca.contract_id and c.provider_element_id = $1 and ca.application_id = $2`, p.ID, app.ID); err != nil {
		return err
	}
	return unrelate(ctx, tx, actor, RelProvidedBy, app.ID, p.ID)
}

// criticalProvidersSQL: fornitori critici con il motivo (applicazioni con funzione critica
// servite, o contratti che dichiarano una funzione critica).
const providerStatsSQL = `
	select p.element_id::text,
	  (select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'provided_by'
	     join ext_application a on a.tenant_id = r.tenant_id and a.element_id = r.source_id and a.supports_critical_function
	     join elements ae on ae.tenant_id = r.tenant_id and ae.id = r.source_id and ae.deleted_at is null
	     where r.target_id = p.element_id) as critical_apps,
	  exists (select 1 from ict_contracts c where c.provider_element_id = p.element_id and c.supports_critical_function) as critical_contract,
	  (select count(*) from ict_contracts c where c.provider_element_id = p.element_id) as contracts,
	  (select min(c.end_date) from ict_contracts c where c.provider_element_id = p.element_id and (c.end_date is null or c.end_date >= current_date)) as next_end
	from ext_ict_provider p`

type providerStats struct {
	CriticalApps     int
	CriticalContract bool
	Contracts        int
	NextEnd          *time.Time
}

func (ps providerStats) critical() bool { return ps.CriticalApps > 0 || ps.CriticalContract }

func statsOf(ctx context.Context, tx pgx.Tx, providerID string) (providerStats, error) {
	var st providerStats
	var id string
	err := tx.QueryRow(ctx, providerStatsSQL+` where p.element_id = $1`, providerID).Scan(&id, &st.CriticalApps, &st.CriticalContract, &st.Contracts, &st.NextEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, engine.ErrNotFound
	}
	return st, err
}

// Provider è un fornitore nell'elenco.
type Provider struct {
	ID               string   `json:"id"`
	Key              string   `json:"key"`
	Name             string   `json:"name"`
	Phase            string   `json:"phase"`
	LegalName        string   `json:"legal_name"`
	LEI              *string  `json:"lei"`
	Country          *string  `json:"country"`
	ProviderType     *string  `json:"provider_type"`
	Intragroup       bool     `json:"is_intragroup"`
	Substitutability *string  `json:"substitutability"`
	SubstName        string   `json:"substitutability_name"`
	Critical         bool     `json:"critical"`
	CriticalReason   string   `json:"critical_reason"`
	CriticalApps     int      `json:"critical_applications"`
	Contracts        int      `json:"contracts"`
	NextEnd          *string  `json:"next_contract_end"`
	Applications     []string `json:"applications"`
}

const selectProvider = `
	select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, ''), p.legal_name, p.lei, p.country, p.provider_type, p.is_intragroup, p.substitutability
	from ext_ict_provider p
	join elements e on e.tenant_id = p.tenant_id and e.id = p.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id`

func scanProviders(rows pgx.Rows) ([]Provider, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Provider, error) {
		var p Provider
		err := r.Scan(&p.ID, &p.Key, &p.Name, &p.Phase, &p.LegalName, &p.LEI, &p.Country, &p.ProviderType, &p.Intragroup, &p.Substitutability)
		if p.Substitutability != nil {
			p.SubstName = substitutability[*p.Substitutability]
		}
		p.Applications = []string{}
		return p, err
	})
}

func reasonOf(st providerStats) string {
	switch {
	case st.CriticalApps == 1 && st.CriticalContract:
		return "serve un'applicazione con funzione critica e un contratto la dichiara"
	case st.CriticalApps > 1 && st.CriticalContract:
		return fmt.Sprintf("serve %d applicazioni con funzione critica e un contratto la dichiara", st.CriticalApps)
	case st.CriticalApps == 1:
		return "serve un'applicazione che supporta una funzione critica"
	case st.CriticalApps > 1:
		return fmt.Sprintf("serve %d applicazioni che supportano funzioni critiche", st.CriticalApps)
	case st.CriticalContract:
		return "un contratto dichiara il supporto a una funzione critica"
	}
	return ""
}

// Providers elenca i fornitori con la criticità calcolata (element.read). critical = solo i critici.
func (s *Service) Providers(ctx context.Context, tx pgx.Tx, actor engine.Actor, q string, critical bool) ([]Provider, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectProvider+` where ($1 = '' or e.title ilike '%' || $1 || '%' or p.legal_name ilike '%' || $1 || '%' or p.lei = upper($1))
		order by e.title limit 1000`, strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	list, err := scanProviders(rows)
	if err != nil {
		return nil, err
	}
	stats := map[string]providerStats{}
	srows, err := tx.Query(ctx, providerStatsSQL)
	if err != nil {
		return nil, err
	}
	var id string
	var st providerStats
	if _, err := pgx.ForEachRow(srows, []any{&id, &st.CriticalApps, &st.CriticalContract, &st.Contracts, &st.NextEnd}, func() error {
		stats[id] = st
		return nil
	}); err != nil {
		return nil, err
	}
	arows, err := tx.Query(ctx, `select r.target_id::text, e.title from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1
		join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null order by e.title`, RelProvidedBy)
	if err != nil {
		return nil, err
	}
	apps := map[string][]string{}
	var title string
	if _, err := pgx.ForEachRow(arows, []any{&id, &title}, func() error {
		apps[id] = append(apps[id], title)
		return nil
	}); err != nil {
		return nil, err
	}
	out := []Provider{}
	for _, p := range list {
		st := stats[p.ID]
		p.Critical, p.CriticalReason, p.CriticalApps, p.Contracts, p.NextEnd = st.critical(), reasonOf(st), st.CriticalApps, st.Contracts, dateStr(st.NextEnd)
		if a := apps[p.ID]; a != nil {
			p.Applications = a
		}
		if critical && !p.Critical {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Contract è un contratto ICT.
type Contract struct {
	ID                 string          `json:"id"`
	ProviderID         string          `json:"provider_id"`
	ProviderKey        string          `json:"provider_key"`
	Provider           string          `json:"provider"`
	Reference          string          `json:"reference"`
	ServiceType        string          `json:"service_type"`
	Services           string          `json:"services"`
	Functions          string          `json:"functions"`
	CriticalFunction   bool            `json:"supports_critical_function"`
	Criticality        *string         `json:"criticality"`
	CriticalityName    string          `json:"criticality_name"`
	StartDate          *string         `json:"start_date"`
	EndDate            *string         `json:"end_date"`
	NoticeDays         *int            `json:"notice_days"`
	AnnualCostCents    *int64          `json:"annual_cost_cents"`
	Currency           *string         `json:"currency"`
	DataLocations      []string        `json:"data_locations"`
	ProcessingLocation []string        `json:"processing_locations"`
	Subcontractors     []Subcontractor `json:"subcontractors"`
	ExitStrategy       string          `json:"exit_strategy"`
	ExitTestedOn       *string         `json:"exit_tested_on"`
	GoverningLaw       *string         `json:"governing_law"`
	AutoRenewal        bool            `json:"auto_renewal"`
	RTOMinutes         *int            `json:"rto_minutes"`
	RPOMinutes         *int            `json:"rpo_minutes"`
	Applications       []Ref           `json:"applications"`
	DaysToEnd          *int            `json:"days_to_end"`
	Expiring           bool            `json:"expiring"`
	Expired            bool            `json:"expired"`
}

// Subcontractor è un subappaltatore della catena di fornitura.
type Subcontractor struct {
	Name    string `json:"name"`
	Country string `json:"country,omitempty"`
	Service string `json:"service,omitempty"`
	LEI     string `json:"lei,omitempty"`
}

const selectContract = `
	select c.id::text, c.provider_element_id::text, s.key || '-' || e.number, e.title, c.reference, c.service_type, c.services, c.functions,
	       c.supports_critical_function, c.criticality, c.start_date, c.end_date, c.notice_days, c.annual_cost_cents, c.currency::text,
	       c.data_locations, c.processing_locations, c.subcontractors, c.exit_strategy, c.exit_tested_on, c.governing_law, c.auto_renewal,
	       c.rto_minutes, c.rpo_minutes
	from ict_contracts c
	join elements e on e.tenant_id = c.tenant_id and e.id = c.provider_element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id`

func (s *Service) scanContracts(ctx context.Context, tx pgx.Tx, rows pgx.Rows, noticeDays int) ([]Contract, error) {
	out := []Contract{}
	for rows.Next() {
		var c Contract
		var start, end, tested *time.Time
		var subs []byte
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.ProviderKey, &c.Provider, &c.Reference, &c.ServiceType, &c.Services, &c.Functions,
			&c.CriticalFunction, &c.Criticality, &start, &end, &c.NoticeDays, &c.AnnualCostCents, &c.Currency,
			&c.DataLocations, &c.ProcessingLocation, &subs, &c.ExitStrategy, &tested, &c.GoverningLaw, &c.AutoRenewal,
			&c.RTOMinutes, &c.RPOMinutes); err != nil {
			rows.Close()
			return nil, err
		}
		c.StartDate, c.EndDate, c.ExitTestedOn = dateStr(start), dateStr(end), dateStr(tested)
		if c.Criticality != nil {
			c.CriticalityName = critNames[*c.Criticality]
		}
		if err := json.Unmarshal(subs, &c.Subcontractors); err != nil {
			rows.Close()
			return nil, err
		}
		if end != nil {
			d := int(end.Sub(s.today()).Hours() / 24)
			c.DaysToEnd = &d
			window := noticeDays
			if c.NoticeDays != nil {
				window += *c.NoticeDays
			}
			c.Expired = d < 0
			c.Expiring = d >= 0 && d <= window
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		r, err := tx.Query(ctx, `select `+refColumns+refFrom+` join ict_contract_applications ca on ca.tenant_id = e.tenant_id and ca.application_id = e.id
			where ca.contract_id = $1 and e.deleted_at is null order by e.title`, out[i].ID)
		if err != nil {
			return nil, err
		}
		if out[i].Applications, err = scanRefs(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ContractInput crea o modifica un contratto. Nella modifica solo i campi valorizzati cambiano.
type ContractInput struct {
	Reference           *string          `json:"reference"`
	ServiceType         *string          `json:"service_type"`
	Services            *string          `json:"services"`
	Functions           *string          `json:"functions"`
	CriticalFunction    *bool            `json:"supports_critical_function"`
	Criticality         *string          `json:"criticality"`
	StartDate           *string          `json:"start_date"`
	EndDate             *string          `json:"end_date"`
	NoticeDays          *int             `json:"notice_days"`
	AnnualCostCents     *int64           `json:"annual_cost_cents"`
	Currency            *string          `json:"currency"`
	DataLocations       []string         `json:"data_locations"`
	ProcessingLocations []string         `json:"processing_locations"`
	Subcontractors      *[]Subcontractor `json:"subcontractors"`
	ExitStrategy        *string          `json:"exit_strategy"`
	ExitTestedOn        *string          `json:"exit_tested_on"`
	GoverningLaw        *string          `json:"governing_law"`
	AutoRenewal         *bool            `json:"auto_renewal"`
	Applications        *[]string        `json:"applications"`
	// Impegno contrattuale di ripristino (continuità operativa): presente e null = cancellato.
	RTOMinutes optMinutes `json:"rto_minutes"`
	RPOMinutes optMinutes `json:"rpo_minutes"`
}

// optMinutes distingue un campo assente da uno messo a null.
type optMinutes struct {
	Set bool
	V   *int
}

func (o *optMinutes) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.V)
}

func countries(field string, in []string) ([]string, error) {
	if in == nil {
		return nil, nil
	}
	out := []string{}
	for _, c := range in {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !countryRe.MatchString(c) {
			return nil, engine.InvalidParams("validation.risk.country_code_value", field, map[string]any{"country": c}, "paese %q: codice ISO di due lettere (es. IT, DE)", c)
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// SaveContract crea (id vuoto) o modifica un contratto del fornitore (risk.admin).
func (s *Service) SaveContract(ctx context.Context, tx pgx.Tx, actor engine.Actor, providerRef, id string, in ContractInput) (*Contract, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var providerID string
	if id == "" {
		p, err := s.resolveTyped(ctx, tx, actor, providerRef, ProviderType)
		if err != nil {
			return nil, err
		}
		providerID = p.ID
		if in.Reference == nil || strings.TrimSpace(*in.Reference) == "" {
			return nil, engine.Required("reference", "il riferimento del contratto è obbligatorio")
		}
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into ict_contracts (tenant_id, id, provider_element_id, reference, created_by) values (app_tenant(), $1, $2, $3, $4)`,
			id, providerID, strings.TrimSpace(*in.Reference), nullable(actor.MemberID)); err != nil {
			if strings.Contains(err.Error(), "ict_contracts_reference_uq") {
				return nil, engine.Duplicate("reference", "c'è già un contratto con questo riferimento")
			}
			return nil, err
		}
	} else {
		if err := tx.QueryRow(ctx, "select provider_element_id::text from ict_contracts where id::text = $1 for update", id).Scan(&providerID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, engine.ErrNotFound
			}
			return nil, err
		}
	}
	// Validazioni.
	if in.Criticality != nil {
		c, ok := normCriticality(*in.Criticality)
		if !ok {
			return nil, engine.InvalidValue("criticality", "criticità non ammessa (bassa, media, alta, critica)")
		}
		in.Criticality = &c
	}
	start, err := parseDate("start_date", deref(in.StartDate))
	if err != nil {
		return nil, err
	}
	end, err := parseDate("end_date", deref(in.EndDate))
	if err != nil {
		return nil, err
	}
	tested, err := parseDate("exit_tested_on", deref(in.ExitTestedOn))
	if err != nil {
		return nil, err
	}
	if in.NoticeDays != nil && (*in.NoticeDays < 0 || *in.NoticeDays > 1095) {
		return nil, engine.OutOfRange("notice_days", 0, 1095, "preavviso di disdetta tra 0 e 1095 giorni")
	}
	if in.AnnualCostCents != nil && *in.AnnualCostCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "annual_cost_cents", "il costo non può essere negativo")
	}
	if in.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if c != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(c) {
			return nil, engine.InvalidCode("validation.risk.currency_code", "currency", "valuta: codice ISO di tre lettere (es. EUR)")
		}
		in.Currency = &c
	}
	dl, err := countries("data_locations", in.DataLocations)
	if err != nil {
		return nil, err
	}
	pl, err := countries("processing_locations", in.ProcessingLocations)
	if err != nil {
		return nil, err
	}
	var subs []byte
	if in.Subcontractors != nil {
		list := []Subcontractor{}
		for _, sc := range *in.Subcontractors {
			sc.Name, sc.Country, sc.Service, sc.LEI = strings.TrimSpace(sc.Name), strings.ToUpper(strings.TrimSpace(sc.Country)), strings.TrimSpace(sc.Service), strings.ToUpper(strings.TrimSpace(sc.LEI))
			if sc.Name == "" {
				continue
			}
			if sc.Country != "" && !countryRe.MatchString(sc.Country) {
				return nil, engine.InvalidValue("subcontractors", "subappaltatore %s: paese non valido", sc.Name)
			}
			if sc.LEI != "" && !leiRe.MatchString(sc.LEI) {
				return nil, engine.InvalidValue("subcontractors", "subappaltatore %s: LEI non valido", sc.Name)
			}
			list = append(list, sc)
		}
		if len(list) > 50 {
			return nil, engine.InvalidCode("validation.risk.subcontractors_too_many", "subcontractors", "al massimo 50 subappaltatori")
		}
		subs, _ = json.Marshal(list)
	}
	for f, v := range map[string]*string{"service_type": in.ServiceType, "services": in.Services, "functions": in.Functions, "exit_strategy": in.ExitStrategy} {
		if v != nil && len(*v) > 10000 {
			return nil, engine.InvalidCode("validation.risk.text_too_long", f, "testo troppo lungo")
		}
	}
	trim := func(p *string) any {
		if p == nil {
			return nil
		}
		return strings.TrimSpace(*p)
	}
	_, err = tx.Exec(ctx, `update ict_contracts set
		reference = coalesce(nullif($2, ''), reference),
		service_type = coalesce($3, service_type), services = coalesce($4, services), functions = coalesce($5, functions),
		supports_critical_function = coalesce($6, supports_critical_function),
		criticality = case when $7::text is null then criticality else nullif($7, '') end,
		start_date = case when $20 then $8::date else start_date end,
		end_date = case when $21 then $9::date else end_date end,
		notice_days = coalesce($10, notice_days), annual_cost_cents = coalesce($11, annual_cost_cents),
		currency = case when $12::text is null then currency else nullif($12, '') end,
		data_locations = coalesce($13, data_locations), processing_locations = coalesce($14, processing_locations),
		subcontractors = coalesce($15::jsonb, subcontractors), exit_strategy = coalesce($16, exit_strategy),
		exit_tested_on = case when $22 then $17::date else exit_tested_on end,
		governing_law = case when $18::text is null then governing_law else nullif($18, '') end,
		auto_renewal = coalesce($19, auto_renewal),
		expiry_notice_sent_at = case when $21 then null else expiry_notice_sent_at end,
		updated_at = now()
		where id = $1`, id, deref(in.Reference), trim(in.ServiceType), trim(in.Services), trim(in.Functions), in.CriticalFunction, in.Criticality,
		start, end, in.NoticeDays, in.AnnualCostCents, in.Currency, dl, pl, subs, trim(in.ExitStrategy), tested, trim(in.GoverningLaw), in.AutoRenewal,
		in.StartDate != nil, in.EndDate != nil, in.ExitTestedOn != nil)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "ict_contracts_dates"):
			return nil, engine.InvalidCode("validation.risk.contract_end_before_start", "end_date", "la fine del contratto è prima dell'inizio")
		case strings.Contains(err.Error(), "ict_contracts_reference_uq"):
			return nil, engine.Duplicate("reference", "c'è già un contratto con questo riferimento")
		}
		return nil, err
	}
	for col, o := range map[string]optMinutes{"rto_minutes": in.RTOMinutes, "rpo_minutes": in.RPOMinutes} {
		if !o.Set {
			continue
		}
		if o.V != nil && (*o.V < 0 || *o.V > 5256000) {
			return nil, engine.OutOfRange(col, 0, 5256000, "minuti fuori intervallo")
		}
		if _, err := tx.Exec(ctx, "update ict_contracts set "+col+" = $2 where id = $1", id, o.V); err != nil {
			return nil, err
		}
	}
	if in.Applications != nil {
		if _, err := tx.Exec(ctx, "delete from ict_contract_applications where contract_id = $1", id); err != nil {
			return nil, err
		}
		for _, a := range *in.Applications {
			app, err := s.resolveTyped(ctx, tx, actor, a, AppType)
			if errors.Is(err, engine.ErrNotFound) {
				return nil, engine.NotFound("applications", "application", "applicazione %q inesistente", a)
			}
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `insert into ict_contract_applications (tenant_id, contract_id, application_id) values (app_tenant(), $1, $2)
				on conflict do nothing`, id, app.ID); err != nil {
				return nil, err
			}
			// L'applicazione del contratto dipende dal fornitore anche nel grafo.
			if _, err := relate(ctx, tx, actor, RelProvidedBy, app.ID, providerID); err != nil {
				return nil, err
			}
		}
	}
	if err := engine.AddActivity(ctx, tx, providerID, actor.MemberID, "contract", "saved", map[string]any{"contract": id, "reference": deref(in.Reference)}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "ict_contract.save", "ict_contract", id, map[string]any{"provider": providerID}); err != nil {
		return nil, err
	}
	return s.contract(ctx, tx, id)
}

func (s *Service) contract(ctx context.Context, tx pgx.Tx, id string) (*Contract, error) {
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectContract+` where c.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := s.scanContracts(ctx, tx, rows, st.ContractNoticeDays)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

// DeleteContract cancella un contratto (risk.admin): resta la voce nella storia del fornitore.
func (s *Service) DeleteContract(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	var providerID, ref string
	err := tx.QueryRow(ctx, "delete from ict_contracts where id::text = $1 returning provider_element_id::text, reference", id).Scan(&providerID, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, providerID, actor.MemberID, "contract", "deleted", nil, map[string]any{"reference": ref}); err != nil {
		return err
	}
	return audited(ctx, tx, actor, "ict_contract.delete", "ict_contract", id, map[string]any{"provider": providerID})
}

// Contracts elenca i contratti (element.read); expiring = solo quelli in scadenza o scaduti.
func (s *Service) Contracts(ctx context.Context, tx pgx.Tx, actor engine.Actor, providerID string, expiring bool) ([]Contract, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectContract+` where ($1 = '' or c.provider_element_id::text = $1) order by c.end_date nulls last, c.reference`, providerID)
	if err != nil {
		return nil, err
	}
	list, err := s.scanContracts(ctx, tx, rows, st.ContractNoticeDays)
	if err != nil || !expiring {
		return list, err
	}
	out := []Contract{}
	for _, c := range list {
		if c.Expiring || c.Expired {
			out = append(out, c)
		}
	}
	return out, nil
}

// ProviderView è la pagina del fornitore.
type ProviderView struct {
	Provider
	Element  *engine.Element `json:"element"`
	OwnerID  *string         `json:"owner_id"`
	Owner    *string         `json:"owner"`
	Apps     []Ref           `json:"application_refs"`
	Risks    []Ref           `json:"risks"`
	Contract []Contract      `json:"contract_list"`
	Can      map[string]bool `json:"can"`
}

// ProviderView restituisce il fornitore con contratti, applicazioni e rischi.
func (s *Service) ProviderView(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ProviderView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, ProviderType)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectProvider+` where p.element_id = $1`, el.ID)
	if err != nil {
		return nil, err
	}
	list, err := scanProviders(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	v := &ProviderView{Provider: list[0], Element: el, Can: map[string]bool{"edit": actor.Has(PermAdmin)}}
	st, err := statsOf(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	v.Critical, v.CriticalReason, v.CriticalApps, v.Contracts, v.NextEnd = st.critical(), reasonOf(st), st.CriticalApps, st.Contracts, dateStr(st.NextEnd)
	if v.Apps, err = targetsOf(ctx, tx, RelProvidedBy, el.ID, false); err != nil {
		return nil, err
	}
	for _, a := range v.Apps {
		v.Applications = append(v.Applications, a.Title)
	}
	if v.Risks, err = targetsOf(ctx, tx, RelRiskOf, el.ID, false); err != nil {
		return nil, err
	}
	if v.Contract, err = s.Contracts(ctx, tx, actor, el.ID, false); err != nil {
		return nil, err
	}
	if v.OwnerID, v.Owner, err = holderName(ctx, tx, el.ID, RoleProviderOwner); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) providerFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	st, err := statsOf(ctx, tx, el.ID)
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var country, subst *string
	var intragroup bool
	if err := tx.QueryRow(ctx, "select country, substitutability, is_intragroup from ext_ict_provider where element_id = $1", el.ID).Scan(&country, &subst, &intragroup); err != nil {
		return err
	}
	f.Set("critico", st.critical())
	f.Set("funzioni_critiche", st.CriticalApps)
	f.Set("contratti", st.Contracts)
	f.Set("scadenza_contratto", st.NextEnd)
	f.Set("paese", country)
	f.Set("intragruppo", intragroup)
	if subst != nil {
		f.Set("sostituibilita", *subst, substitutability[*subst])
	}
	apps, err := targetsOf(ctx, tx, RelProvidedBy, el.ID, false)
	if err != nil {
		return err
	}
	var names []any
	for _, a := range apps {
		names = append(names, a.Title, a.Key)
	}
	f.Set("applicazioni", names...)
	return nil
}

// appFacts: sull'applicazione, i fornitori ICT da cui dipende e i rischi oltre la soglia.
func (s *Service) appFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	provs, err := targetsOf(ctx, tx, RelProvidedBy, el.ID, true)
	if err != nil {
		return err
	}
	var names []any
	for _, p := range provs {
		names = append(names, p.Title, p.Key)
	}
	f.Set("fornitori_ict", names...)
	var over int
	if err := tx.QueryRow(ctx, `select count(*) from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		join ext_risk x on x.tenant_id = r.tenant_id and x.element_id = r.source_id
		join risk_matrices m on m.tenant_id = x.tenant_id and m.id = x.matrix_id
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where r.target_id = $1 and x.residual_score > m.appetite_score and coalesce(o.category, '') not in ('done', 'cancelled')`, el.ID, RelRiskOf).Scan(&over); err != nil {
		return err
	}
	f.Set("rischi_oltre_soglia", over)
	return nil
}

// ---------------------------------------------------------------------------
// Registro delle informazioni: colonne e export

// ColumnInfo descrive una colonna disponibile per l'export del registro.
type ColumnInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
	Help  string `json:"help,omitempty"`
}

type colDef struct {
	ColumnInfo
	value func(r registerRow) string
}

type registerRow struct {
	p Provider
	c *Contract
}

func yesNo(b bool) string {
	if b {
		return "sì"
	}
	return "no"
}

func appNames(c *Contract, f func(Ref) string) string {
	if c == nil {
		return ""
	}
	var out []string
	for _, a := range c.Applications {
		if v := f(a); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

func cv(c *Contract, f func(*Contract) string) string {
	if c == nil {
		return ""
	}
	return f(c)
}

// Il catalogo delle colonne segue la struttura del registro delle informazioni (entità,
// accordi contrattuali, fornitori, funzioni, catena di subfornitura), con nomi nostri.
var columnList = []colDef{
	{ColumnInfo{"contratto.riferimento", "Riferimento del contratto", "Accordo contrattuale", "Identificativo interno dell'accordo"}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.Reference }) }},
	{ColumnInfo{"contratto.tipo_servizio", "Tipo di servizio ICT", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.ServiceType }) }},
	{ColumnInfo{"contratto.servizi", "Servizi forniti", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.Services }) }},
	{ColumnInfo{"contratto.inizio", "Data di inizio", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return deref(c.StartDate) }) }},
	{ColumnInfo{"contratto.fine", "Data di scadenza", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return deref(c.EndDate) }) }},
	{ColumnInfo{"contratto.preavviso_giorni", "Preavviso di disdetta (giorni)", "Accordo contrattuale", ""}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string {
			if c.NoticeDays == nil {
				return ""
			}
			return strconv.Itoa(*c.NoticeDays)
		})
	}},
	{ColumnInfo{"contratto.rinnovo_automatico", "Rinnovo automatico", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return yesNo(c.AutoRenewal) }) }},
	{ColumnInfo{"contratto.costo_annuo", "Costo annuo", "Accordo contrattuale", "In unità della valuta, con due decimali"}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string {
			if c.AnnualCostCents == nil {
				return ""
			}
			return fmt.Sprintf("%d,%02d", *c.AnnualCostCents/100, *c.AnnualCostCents%100)
		})
	}},
	{ColumnInfo{"contratto.valuta", "Valuta", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return deref(c.Currency) }) }},
	{ColumnInfo{"contratto.legge_applicabile", "Legge applicabile", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return deref(c.GoverningLaw) }) }},
	{ColumnInfo{"contratto.ubicazione_dati", "Paesi di conservazione dei dati", "Accordo contrattuale", ""}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string { return strings.Join(c.DataLocations, ", ") })
	}},
	{ColumnInfo{"contratto.ubicazione_trattamento", "Paesi di trattamento dei dati", "Accordo contrattuale", ""}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string { return strings.Join(c.ProcessingLocation, ", ") })
	}},
	{ColumnInfo{"contratto.strategia_uscita", "Strategia di uscita", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.ExitStrategy }) }},
	{ColumnInfo{"contratto.uscita_provata", "Strategia di uscita provata il", "Accordo contrattuale", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return deref(c.ExitTestedOn) }) }},
	{ColumnInfo{"funzioni.descrizione", "Funzioni supportate", "Funzioni", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.Functions }) }},
	{ColumnInfo{"funzioni.critica", "Supporta una funzione critica o importante", "Funzioni", "Dichiarato nel contratto o derivato dalle applicazioni"}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string {
			crit := c.CriticalFunction
			for _, a := range c.Applications {
				crit = crit || a.Critical
			}
			return yesNo(crit)
		})
	}},
	{ColumnInfo{"funzioni.criticita", "Criticità del servizio", "Funzioni", ""}, func(r registerRow) string { return cv(r.c, func(c *Contract) string { return c.CriticalityName }) }},
	{ColumnInfo{"applicazioni.nomi", "Applicazioni servite", "Funzioni", ""}, func(r registerRow) string { return appNames(r.c, func(a Ref) string { return a.Title }) }},
	{ColumnInfo{"applicazioni.chiavi", "Chiavi delle applicazioni", "Funzioni", ""}, func(r registerRow) string { return appNames(r.c, func(a Ref) string { return a.Key }) }},
	{ColumnInfo{"applicazioni.critiche", "Applicazioni con funzione critica", "Funzioni", ""}, func(r registerRow) string {
		return appNames(r.c, func(a Ref) string {
			if a.Critical {
				return a.Title
			}
			return ""
		})
	}},
	{ColumnInfo{"fornitore.chiave", "Chiave del fornitore", "Fornitore", ""}, func(r registerRow) string { return r.p.Key }},
	{ColumnInfo{"fornitore.nome", "Fornitore", "Fornitore", ""}, func(r registerRow) string { return r.p.Name }},
	{ColumnInfo{"fornitore.ragione_sociale", "Ragione sociale", "Fornitore", ""}, func(r registerRow) string { return r.p.LegalName }},
	{ColumnInfo{"fornitore.lei", "Codice LEI", "Fornitore", ""}, func(r registerRow) string { return deref(r.p.LEI) }},
	{ColumnInfo{"fornitore.paese", "Paese della sede", "Fornitore", ""}, func(r registerRow) string { return deref(r.p.Country) }},
	{ColumnInfo{"fornitore.tipo", "Tipo di fornitore", "Fornitore", ""}, func(r registerRow) string { return deref(r.p.ProviderType) }},
	{ColumnInfo{"fornitore.intragruppo", "Intragruppo", "Fornitore", ""}, func(r registerRow) string { return yesNo(r.p.Intragroup) }},
	{ColumnInfo{"fornitore.sostituibilita", "Sostituibilità", "Fornitore", ""}, func(r registerRow) string { return r.p.SubstName }},
	{ColumnInfo{"fornitore.critico", "Fornitore critico (calcolato)", "Fornitore", ""}, func(r registerRow) string { return yesNo(r.p.Critical) }},
	{ColumnInfo{"fornitore.motivo_criticita", "Motivo della criticità", "Fornitore", ""}, func(r registerRow) string { return r.p.CriticalReason }},
	{ColumnInfo{"fornitore.fase", "Stato del fornitore", "Fornitore", ""}, func(r registerRow) string { return r.p.Phase }},
	{ColumnInfo{"subfornitura.nomi", "Subappaltatori", "Catena di subfornitura", ""}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string {
			var out []string
			for _, s := range c.Subcontractors {
				x := s.Name
				if s.Country != "" {
					x += " (" + s.Country + ")"
				}
				out = append(out, x)
			}
			return strings.Join(out, "; ")
		})
	}},
	{ColumnInfo{"subfornitura.servizi", "Servizi subappaltati", "Catena di subfornitura", ""}, func(r registerRow) string {
		return cv(r.c, func(c *Contract) string {
			var out []string
			for _, s := range c.Subcontractors {
				if s.Service != "" {
					out = append(out, s.Name+": "+s.Service)
				}
			}
			return strings.Join(out, "; ")
		})
	}},
}

var columnCatalog = func() map[string]colDef {
	m := map[string]colDef{}
	for _, c := range columnList {
		m[c.Key] = c
	}
	return m
}()

// ColumnCatalog elenca le colonne disponibili, nell'ordine del catalogo.
func ColumnCatalog() []ColumnInfo {
	out := make([]ColumnInfo, len(columnList))
	for i, c := range columnList {
		out[i] = c.ColumnInfo
	}
	return out
}

func defaultColumns() []Column {
	keys := []string{"contratto.riferimento", "fornitore.nome", "fornitore.lei", "fornitore.paese", "fornitore.intragruppo", "contratto.tipo_servizio",
		"contratto.servizi", "funzioni.descrizione", "funzioni.critica", "applicazioni.nomi", "contratto.inizio", "contratto.fine",
		"contratto.preavviso_giorni", "contratto.ubicazione_dati", "contratto.ubicazione_trattamento", "subfornitura.nomi",
		"fornitore.sostituibilita", "contratto.strategia_uscita", "fornitore.critico"}
	out := make([]Column, len(keys))
	for i, k := range keys {
		out[i] = Column{Key: k, Label: columnCatalog[k].Label}
	}
	return out
}

// RegisterExport scrive il registro delle informazioni in CSV: una riga per contratto
// (grain "contract", i fornitori senza contratti compaiono con le colonne del contratto
// vuote) oppure una per fornitore ("provider"). columns vuoto = quelle configurate.
func (s *Service) RegisterExport(ctx context.Context, tx pgx.Tx, actor engine.Actor, w io.Writer, keys []string, grain string) error {
	if err := requireRead(actor); err != nil {
		return err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	cols := st.RegisterColumns
	if len(keys) > 0 {
		in := make([]Column, 0, len(keys))
		for _, k := range keys {
			in = append(in, Column{Key: strings.TrimSpace(k)})
		}
		if cols, err = validColumns(in); err != nil {
			return err
		}
	}
	if grain != "" && grain != "contract" && grain != "provider" {
		return engine.InvalidCode("validation.risk.grain", "grain", "righe per contratto (contract) o per fornitore (provider)")
	}
	providers, err := s.Providers(ctx, tx, actor, "", false)
	if err != nil {
		return err
	}
	contracts, err := s.Contracts(ctx, tx, actor, "", false)
	if err != nil {
		return err
	}
	byProvider := map[string][]*Contract{}
	for i := range contracts {
		byProvider[contracts[i].ProviderID] = append(byProvider[contracts[i].ProviderID], &contracts[i])
	}
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.Label
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	write := func(r registerRow) error {
		rec := make([]string, len(cols))
		for i, c := range cols {
			rec[i] = columnCatalog[c.Key].value(r)
		}
		return cw.Write(rec)
	}
	for _, p := range providers {
		cs := byProvider[p.ID]
		if grain == "provider" || len(cs) == 0 {
			var c *Contract
			if len(cs) > 0 {
				c = cs[0] // per fornitore: il primo contratto (per scadenza)
			}
			if err := write(registerRow{p: p, c: c}); err != nil {
				return err
			}
			continue
		}
		for _, c := range cs {
			if err := write(registerRow{p: p, c: c}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return audited(ctx, tx, actor, "ict_register.export", "ict_register", "", map[string]any{"columns": len(cols), "providers": len(providers), "grain": grain})
}

// notifyContract avvisa il referente del fornitore e il gruppo Risk management.
func (s *Service) notifyContract(ctx context.Context, tx pgx.Tx, c Contract) error {
	members, groups, err := roleHolders(ctx, tx, c.ProviderID, RoleProviderOwner)
	if err != nil {
		return err
	}
	var gid string
	if err := tx.QueryRow(ctx, "select id::text from groups where name = $1", GroupRisk).Scan(&gid); err == nil {
		groups = append(groups, gid)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRisk, ElementID: c.ProviderID, Members: members, Groups: groups,
		Payload: map[string]any{"event": "contract_expiring", "contract": c.Reference, "provider": c.Provider, "end_date": deref(c.EndDate)}})
	return err
}
