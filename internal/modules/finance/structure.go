package finance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// ---------------------------------------------------------------------
// Impostazioni del modulo
// ---------------------------------------------------------------------

// Settings sono le impostazioni del tenant (finance_settings).
type Settings struct {
	BaseCurrency string `json:"base_currency"`
	// LaborCategoryID: categoria dei costi calcolati dalle ore (default: personale interno).
	LaborCategoryID *string `json:"labor_category_id"`
	// PortfolioFiscalYearID: esercizio che alimenta il budget disponibile della graduatoria.
	PortfolioFiscalYearID *string `json:"portfolio_fiscal_year_id"`
	// PortfolioBasis: envelope = plafond dell'esercizio; residual = plafond meno i budget approvati.
	PortfolioBasis  string `json:"portfolio_basis"`
	AlertThresholds []int  `json:"alert_thresholds"`
}

func GetSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	st := Settings{PortfolioBasis: "residual", AlertThresholds: []int{80, 100}}
	var err error
	if st.BaseCurrency, err = BaseCurrency(ctx, tx); err != nil {
		return st, err
	}
	err = tx.QueryRow(ctx, `select labor_category_id, portfolio_fiscal_year_id, portfolio_basis, alert_thresholds from finance_settings`).
		Scan(&st.LaborCategoryID, &st.PortfolioFiscalYearID, &st.PortfolioBasis, &st.AlertThresholds)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return st, err
}

// SettingsInput cambia le impostazioni; i campi assenti restano come sono.
type SettingsInput struct {
	LaborCategoryID       *string `json:"labor_category_id"`
	PortfolioFiscalYearID *string `json:"portfolio_fiscal_year_id"`
	ClearPortfolio        bool    `json:"clear_portfolio"`
	PortfolioBasis        *string `json:"portfolio_basis"`
	AlertThresholds       *[]int  `json:"alert_thresholds"`
}

func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (Settings, error) {
	if err := requireWrite(actor); err != nil {
		return Settings{}, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return st, err
	}
	if in.LaborCategoryID != nil {
		if err := exists(ctx, tx, "cost_categories", *in.LaborCategoryID, "labor_category_id"); err != nil {
			return st, err
		}
		st.LaborCategoryID = in.LaborCategoryID
	}
	if in.PortfolioFiscalYearID != nil {
		if err := exists(ctx, tx, "fiscal_years", *in.PortfolioFiscalYearID, "portfolio_fiscal_year_id"); err != nil {
			return st, err
		}
		st.PortfolioFiscalYearID = in.PortfolioFiscalYearID
	}
	if in.ClearPortfolio {
		st.PortfolioFiscalYearID = nil
	}
	if in.PortfolioBasis != nil {
		if *in.PortfolioBasis != "envelope" && *in.PortfolioBasis != "residual" {
			return st, engine.InvalidValue("portfolio_basis", "valore non ammesso (envelope, residual)")
		}
		st.PortfolioBasis = *in.PortfolioBasis
	}
	if in.AlertThresholds != nil {
		for _, t := range *in.AlertThresholds {
			if t < 1 || t > 200 {
				return st, engine.OutOfRange("alert_thresholds", 1, 200, "soglie tra 1 e 200%%")
			}
		}
		st.AlertThresholds = *in.AlertThresholds
	}
	if _, err := tx.Exec(ctx, `insert into finance_settings (tenant_id, labor_category_id, portfolio_fiscal_year_id, portfolio_basis, alert_thresholds, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, now())
		on conflict (tenant_id) do update set labor_category_id = excluded.labor_category_id, portfolio_fiscal_year_id = excluded.portfolio_fiscal_year_id,
			portfolio_basis = excluded.portfolio_basis, alert_thresholds = excluded.alert_thresholds, updated_by = excluded.updated_by, updated_at = now()`,
		st.LaborCategoryID, st.PortfolioFiscalYearID, st.PortfolioBasis, st.AlertThresholds, nullable(actor.MemberID)); err != nil {
		return st, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "finance.settings", EntityType: "finance_settings", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"labor_category_id": st.LaborCategoryID,
			"portfolio_fiscal_year_id": st.PortfolioFiscalYearID, "portfolio_basis": st.PortfolioBasis, "alert_thresholds": st.AlertThresholds}}); err != nil {
		return st, err
	}
	if st.PortfolioFiscalYearID != nil {
		if err := s.syncPortfolio(ctx, tx, actor, *st.PortfolioFiscalYearID); err != nil {
			return st, err
		}
	}
	return st, nil
}

// exists verifica che l'id esista nella tabella del tenant (solo tabelle del modulo).
func exists(ctx context.Context, tx pgx.Tx, table, id, field string) error {
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from "+pgx.Identifier{table}.Sanitize()+" where id::text = $1)", id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound(field, "", "valore inesistente")
	}
	return nil
}

// autoLinkPortfolio collega da solo la graduatoria all'esercizio in corso quando ha un plafond, ma solo se
// nessun esercizio la alimenta già e nessuno ha impostato un budget a mano: non scavalca una scelta.
func (s *Service) autoLinkPortfolio(ctx context.Context, tx pgx.Tx, actor engine.Actor, fyID string, envelope *int64) error {
	if envelope == nil {
		return nil
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from fiscal_years where id = $1 and start_date <= current_date and end_date >= current_date and status <> 'closed')
		and not exists (select 1 from finance_settings where portfolio_fiscal_year_id is not null)
		and not exists (select 1 from portfolio_settings where budget_cents is not null)`, fyID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	_, err := s.UpdateSettings(ctx, tx, actor, SettingsInput{PortfolioFiscalYearID: &fyID})
	return err
}

// syncPortfolio aggiorna il budget disponibile della graduatoria se l'esercizio la alimenta.
func (s *Service) syncPortfolio(ctx context.Context, tx pgx.Tx, actor engine.Actor, fyID string) error {
	st, err := GetSettings(ctx, tx)
	if err != nil || st.PortfolioFiscalYearID == nil || *st.PortfolioFiscalYearID != fyID {
		return err
	}
	var envelope *int64
	var allocated int64
	if err := tx.QueryRow(ctx, `select f.envelope_cents, coalesce((select sum(l.amount_cents) from budgets b
			join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
			where b.fiscal_year_id = f.id and b.status = 'approved' and b.kind <> 'forecast'), 0)
		from fiscal_years f where f.id = $1`, fyID).Scan(&envelope, &allocated); err != nil {
		return err
	}
	var budget *int64
	if envelope != nil {
		v := *envelope
		if st.PortfolioBasis == "residual" {
			v = max(v-allocated, 0)
		}
		budget = &v
	}
	if _, err := tx.Exec(ctx, `insert into portfolio_settings (tenant_id, budget_cents, updated_by, updated_at) values (app_tenant(), $1, $2, now())
		on conflict (tenant_id) do update set budget_cents = excluded.budget_cents, updated_by = excluded.updated_by, updated_at = now()`,
		budget, nullable(actor.MemberID)); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "finance.portfolio_sync", EntityType: "fiscal_year", EntityID: fyID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"budget_cents": budget, "basis": st.PortfolioBasis}})
}

// ---------------------------------------------------------------------
// Esercizi
// ---------------------------------------------------------------------

type FiscalYear struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Start         string `json:"start_date"`
	End           string `json:"end_date"`
	Status        string `json:"status"`
	EnvelopeCents *int64 `json:"envelope_cents"`
	// AllocatedCents: somma dei budget approvati correnti; RequestedCents: quelli in lavorazione.
	AllocatedCents int64  `json:"allocated_cents"`
	RequestedCents int64  `json:"requested_cents"`
	ResidualCents  *int64 `json:"residual_cents"`
	Budgets        int    `json:"budgets"`
}

func (s *Service) FiscalYears(ctx context.Context, tx pgx.Tx) ([]FiscalYear, error) {
	rows, err := tx.Query(ctx, `
		select f.id, f.name, f.start_date, f.end_date, f.status, f.envelope_cents,
		       coalesce(sum(l.amount_cents) filter (where b.status = 'approved' and b.kind <> 'forecast'), 0),
		       coalesce(sum(l.amount_cents) filter (where b.status in ('draft','submitted') and b.kind <> 'forecast'), 0),
		       count(distinct b.element_id) filter (where b.kind <> 'forecast')
		from fiscal_years f
		left join budgets b on b.tenant_id = f.tenant_id and b.fiscal_year_id = f.id
		left join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
		group by f.tenant_id, f.id order by f.start_date desc`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (FiscalYear, error) {
		var f FiscalYear
		var a, b time.Time
		err := r.Scan(&f.ID, &f.Name, &a, &b, &f.Status, &f.EnvelopeCents, &f.AllocatedCents, &f.RequestedCents, &f.Budgets)
		f.Start, f.End = a.Format(time.DateOnly), b.Format(time.DateOnly)
		if f.EnvelopeCents != nil {
			v := *f.EnvelopeCents - f.AllocatedCents
			f.ResidualCents = &v
		}
		return f, err
	})
	if out == nil {
		out = []FiscalYear{}
	}
	return out, err
}

type FiscalYearInput struct {
	Name          *string `json:"name"`
	Start         *string `json:"start_date"`
	End           *string `json:"end_date"`
	Status        *string `json:"status"` // planning | open | closed
	EnvelopeCents *int64  `json:"envelope_cents"`
	ClearEnvelope bool    `json:"clear_envelope"`
}

// SaveFiscalYear crea (id vuoto) o modifica un esercizio. Gli esercizi non si sovrappongono.
func (s *Service) SaveFiscalYear(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in FiscalYearInput) (*FiscalYear, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	var cur struct {
		name, status string
		start, end   time.Time
		env          *int64
	}
	if id != "" {
		err := tx.QueryRow(ctx, "select name, start_date, end_date, status, envelope_cents from fiscal_years where id = $1 for update", id).
			Scan(&cur.name, &cur.start, &cur.end, &cur.status, &cur.env)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	} else {
		cur.status = "open"
		if in.Name == nil || in.Start == nil || in.End == nil {
			return nil, engine.Required("name", "servono nome, inizio e fine dell'esercizio")
		}
	}
	if in.Name != nil {
		cur.name = strings.TrimSpace(*in.Name)
		if cur.name == "" {
			return nil, engine.Required("name", "serve un nome")
		}
	}
	var err error
	if in.Start != nil {
		if cur.start, err = parseDate("start_date", *in.Start); err != nil {
			return nil, err
		}
	}
	if in.End != nil {
		if cur.end, err = parseDate("end_date", *in.End); err != nil {
			return nil, err
		}
	}
	if !cur.end.After(cur.start) {
		return nil, engine.InvalidCode("validation.finance.end_before_start", "end_date", "la fine deve seguire l'inizio")
	}
	if in.Status != nil {
		switch *in.Status {
		case "planning", "open", "closed":
			cur.status = *in.Status
		default:
			return nil, engine.InvalidValue("status", "stato non ammesso (planning, open, closed)")
		}
	}
	if in.EnvelopeCents != nil {
		if *in.EnvelopeCents < 0 {
			return nil, engine.InvalidCode("validation.not_negative", "envelope_cents", "il plafond non può essere negativo")
		}
		cur.env = in.EnvelopeCents
	}
	if in.ClearEnvelope {
		cur.env = nil
	}
	var overlap bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from fiscal_years where id::text <> $1 and start_date <= $3 and end_date >= $2)`,
		id, cur.start, cur.end).Scan(&overlap); err != nil {
		return nil, err
	}
	if overlap {
		return nil, engine.InvalidCode("validation.finance.year_overlap", "start_date", "l'esercizio si sovrappone a un altro")
	}
	action := "fiscal_year.update"
	if id == "" {
		id, action = db.NewID(), "fiscal_year.create"
		_, err = tx.Exec(ctx, `insert into fiscal_years (tenant_id, id, name, start_date, end_date, base_currency, status, envelope_cents)
			values (app_tenant(), $1, $2, $3, $4, (select base_currency from tenants where id = app_tenant()), $5, $6)`,
			id, cur.name, cur.start, cur.end, cur.status, cur.env)
	} else {
		_, err = tx.Exec(ctx, `update fiscal_years set name = $2, start_date = $3, end_date = $4, status = $5, envelope_cents = $6 where id = $1`,
			id, cur.name, cur.start, cur.end, cur.status, cur.env)
	}
	if err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("name", "esiste già un esercizio con questo nome")
		}
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "fiscal_year", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"start": cur.start.Format(time.DateOnly),
			"end": cur.end.Format(time.DateOnly), "status": cur.status, "envelope_cents": cur.env}}); err != nil {
		return nil, err
	}
	if cur.env != nil && cur.status != "closed" && (action == "fiscal_year.create" || in.EnvelopeCents != nil) {
		// Il plafond è la radice dell'albero degli stanziamenti (budget.md §2).
		if err := s.rootFromEnvelope(ctx, tx, actor, id, *cur.env); err != nil {
			return nil, err
		}
	}
	if err := s.autoLinkPortfolio(ctx, tx, actor, id, cur.env); err != nil {
		return nil, err
	}
	if err := s.syncPortfolio(ctx, tx, actor, id); err != nil {
		return nil, err
	}
	fys, err := s.FiscalYears(ctx, tx)
	if err != nil {
		return nil, err
	}
	for i := range fys {
		if fys[i].ID == id {
			return &fys[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

// fiscalYearOf trova l'esercizio che contiene la data (per le righe senza esercizio esplicito).
func fiscalYearOf(ctx context.Context, tx pgx.Tx, on time.Time) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id from fiscal_years where start_date <= $1 and end_date >= $1", on).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ---------------------------------------------------------------------
// Categorie e fonti di finanziamento
// ---------------------------------------------------------------------

type Category struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Code     *string `json:"code"`
	ParentID *string `json:"parent_id"`
	Nature   string  `json:"nature"`   // capex | opex
	Sourcing string  `json:"sourcing"` // internal | external
	Active   bool    `json:"active"`
}

func (s *Service) Categories(ctx context.Context, tx pgx.Tx) ([]Category, error) {
	rows, err := tx.Query(ctx, `select id, name, code, parent_id, nature, sourcing, active from cost_categories order by active desc, nature, name`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Category])
	if out == nil {
		out = []Category{}
	}
	return out, err
}

type CategoryInput struct {
	Name     *string `json:"name"`
	Code     *string `json:"code"`
	ParentID *string `json:"parent_id"`
	Nature   *string `json:"nature"`
	Sourcing *string `json:"sourcing"`
	Active   *bool   `json:"active"`
}

func (s *Service) SaveCategory(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in CategoryInput) (*Category, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	c := Category{Nature: "opex", Sourcing: "internal", Active: true}
	if id != "" {
		err := tx.QueryRow(ctx, `select id, name, code, parent_id, nature, sourcing, active from cost_categories where id = $1`, id).
			Scan(&c.ID, &c.Name, &c.Code, &c.ParentID, &c.Nature, &c.Sourcing, &c.Active)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if c.Name == "" {
		return nil, engine.Required("name", "serve un nome")
	}
	if in.Code != nil {
		v := strings.TrimSpace(*in.Code)
		c.Code = &v
		if v == "" {
			c.Code = nil
		}
	}
	if in.ParentID != nil {
		c.ParentID = in.ParentID
		if *in.ParentID == "" || *in.ParentID == id {
			c.ParentID = nil
		} else if err := exists(ctx, tx, "cost_categories", *in.ParentID, "parent_id"); err != nil {
			return nil, err
		}
	}
	if in.Nature != nil {
		if *in.Nature != "capex" && *in.Nature != "opex" {
			return nil, engine.InvalidValue("nature", "natura non ammessa (capex, opex)")
		}
		c.Nature = *in.Nature
	}
	if in.Sourcing != nil {
		if *in.Sourcing != "internal" && *in.Sourcing != "external" {
			return nil, engine.InvalidValue("sourcing", "valore non ammesso (internal, external)")
		}
		c.Sourcing = *in.Sourcing
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	action := "cost_category.update"
	var err error
	if id == "" {
		c.ID, action = db.NewID(), "cost_category.create"
		_, err = tx.Exec(ctx, `insert into cost_categories (tenant_id, id, name, code, parent_id, nature, sourcing, active)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`, c.ID, c.Name, c.Code, c.ParentID, c.Nature, c.Sourcing, c.Active)
	} else {
		_, err = tx.Exec(ctx, `update cost_categories set name = $2, code = $3, parent_id = $4, nature = $5, sourcing = $6, active = $7 where id = $1`,
			c.ID, c.Name, c.Code, c.ParentID, c.Nature, c.Sourcing, c.Active)
	}
	if err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("name", "esiste già una categoria con questo nome o codice")
		}
		return nil, err
	}
	return &c, audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "cost_category", EntityID: c.ID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"nature": c.Nature, "sourcing": c.Sourcing, "active": c.Active}})
}

type FundingSource struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Code   *string `json:"code"`
	Active bool    `json:"active"`
}

func (s *Service) FundingSources(ctx context.Context, tx pgx.Tx) ([]FundingSource, error) {
	rows, err := tx.Query(ctx, `select id, name, code, active from funding_sources order by active desc, name`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[FundingSource])
	if out == nil {
		out = []FundingSource{}
	}
	return out, err
}

func (s *Service) CreateFundingSource(ctx context.Context, tx pgx.Tx, actor engine.Actor, name, code string) (*FundingSource, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	f := FundingSource{ID: db.NewID(), Name: strings.TrimSpace(name), Active: true}
	if f.Name == "" {
		return nil, engine.Required("name", "serve un nome")
	}
	if c := strings.TrimSpace(code); c != "" {
		f.Code = &c
	}
	if _, err := tx.Exec(ctx, `insert into funding_sources (tenant_id, id, name, code) values (app_tenant(), $1, $2, $3)`, f.ID, f.Name, f.Code); err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("name", "esiste già una fonte con questo nome")
		}
		return nil, err
	}
	return &f, audit.Append(ctx, tx, audit.Event{Action: "funding_source.create", EntityType: "funding_source", EntityID: f.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

// ---------------------------------------------------------------------
// Cambi
// ---------------------------------------------------------------------

type ExchangeRate struct {
	Currency   string  `json:"currency"`
	ValidOn    string  `json:"valid_on"`
	RateToBase float64 `json:"rate_to_base"` // 1 unità della valuta = rate_to_base unità della valuta base
}

func (s *Service) ExchangeRates(ctx context.Context, tx pgx.Tx, currency string) ([]ExchangeRate, error) {
	rows, err := tx.Query(ctx, `select currency, valid_on, rate_to_base::float8 from exchange_rates
		where $1 = '' or currency = upper($1) order by currency, valid_on desc limit 500`, currency)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExchangeRate, error) {
		var x ExchangeRate
		var d time.Time
		err := r.Scan(&x.Currency, &d, &x.RateToBase)
		x.ValidOn = d.Format(time.DateOnly)
		return x, err
	})
	if out == nil {
		out = []ExchangeRate{}
	}
	return out, err
}

// SetExchangeRate inserisce o corregge il cambio di una valuta per un giorno. Le voci già
// registrate non cambiano: conservano l'importo convertito e il cambio usato.
func (s *Service) SetExchangeRate(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ExchangeRate) (*ExchangeRate, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	base, err := BaseCurrency(ctx, tx)
	if err != nil {
		return nil, err
	}
	cur, err := normCurrency(in.Currency, "")
	if err != nil || cur == "" {
		return nil, engine.InvalidValue("currency", "valuta non valida (codice ISO a tre lettere)")
	}
	if cur == base {
		return nil, engine.InvalidCode("validation.finance.base_currency_no_rate", "currency", "la valuta base non ha un cambio")
	}
	d, err := parseDate("valid_on", in.ValidOn)
	if err != nil {
		return nil, err
	}
	if in.RateToBase <= 0 {
		return nil, engine.InvalidCode("validation.finance.rate_not_positive", "rate_to_base", "il cambio deve essere positivo")
	}
	if _, err := tx.Exec(ctx, `insert into exchange_rates (tenant_id, currency, valid_on, rate_to_base) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, currency, valid_on) do update set rate_to_base = excluded.rate_to_base`, cur, d, in.RateToBase); err != nil {
		return nil, err
	}
	out := ExchangeRate{Currency: cur, ValidOn: d.Format(time.DateOnly), RateToBase: in.RateToBase}
	return &out, audit.Append(ctx, tx, audit.Event{Action: "exchange_rate.set", EntityType: "exchange_rate", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"currency": cur, "valid_on": out.ValidOn, "rate_to_base": in.RateToBase}})
}

// ---------------------------------------------------------------------
// Configurazione iniziale
// ---------------------------------------------------------------------

// Categorie di partenza (demand.md §9), tutte modificabili: personale interno, consulenze,
// licenze, infrastruttura e cloud, hardware, formazione, sviluppo capitalizzabile.
var starterCategories = []struct{ code, name, nature, sourcing string }{
	{"PERS", "Personale interno", "opex", "internal"},
	{"SVIL", "Sviluppo capitalizzabile", "capex", "internal"},
	{"CONS", "Consulenze esterne", "opex", "external"},
	{"LIC", "Licenze software", "opex", "external"},
	{"CLOUD", "Infrastruttura e cloud", "opex", "external"},
	{"HW", "Hardware", "capex", "external"},
	{"FORM", "Formazione", "opex", "external"},
}

// Provision configura il modulo in un tenant: categorie e fonte di partenza, impostazioni
// e politiche di approvazione di budget e trasferimenti. È idempotente.
//
// Soglie di default (configurabili in approval_policies):
//   - budget: fino a 50 k€ lo sponsor; fino a 250 k€ sponsor e PMO; oltre anche il comitato
//     investimenti (le stesse della demand, demand.md §16);
//   - trasferimenti: fino a 10 k€ basta chi ha finance.write; oltre decide il PMO.
func Provision(ctx context.Context, tx pgx.Tx, _ string) error {
	for _, c := range starterCategories {
		if _, err := tx.Exec(ctx, `insert into cost_categories (tenant_id, id, name, code, nature, sourcing)
			values (app_tenant(), $1, $2, $3, $4, $5) on conflict do nothing`, db.NewID(), c.name, c.code, c.nature, c.sourcing); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into funding_sources (tenant_id, id, name, code) values (app_tenant(), $1, 'Budget ordinario', 'ORD')
		on conflict do nothing`, db.NewID()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into finance_settings (tenant_id, labor_category_id)
		values (app_tenant(), (select id from cost_categories where lower(code) = 'pers')) on conflict do nothing`); err != nil {
		return err
	}
	i64 := func(v int64) *int64 { return &v }
	role := func(r string) map[string]any { return map[string]any{"approvers": []map[string]string{{"role": r}}} }
	committee := map[string]any{"approvers": []map[string]string{{"body": "investment_committee"}}}
	policies := []struct {
		name, stage string
		rules       []map[string]any
	}{
		{"Approvazione del budget", PolicyBudget, []map[string]any{
			{"when": map[string]any{"amount_lte": i64(5_000_000)}, "steps": []any{role("sponsor")}},
			{"when": map[string]any{"amount_lte": i64(25_000_000)}, "steps": []any{role("sponsor"), role("pmo")}},
			{"when": map[string]any{}, "steps": []any{role("sponsor"), role("pmo"), committee}},
		}},
		{"Trasferimenti di budget", PolicyTransfer, []map[string]any{
			{"when": map[string]any{"amount_lte": i64(1_000_000)}, "steps": []any{}},
			{"when": map[string]any{}, "steps": []any{role("pmo")}},
		}},
	}
	for _, p := range policies {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from approval_policies where stage = $1)", p.stage).Scan(&ok); err != nil {
			return err
		}
		if ok {
			continue
		}
		rules, _ := json.Marshal(map[string]any{"rules": p.rules})
		if _, err := tx.Exec(ctx, `insert into approval_policies (tenant_id, id, name, stage, rules) values (app_tenant(), $1, $2, $3, $4)`,
			db.NewID(), p.name, p.stage, rules); err != nil {
			return err
		}
	}
	return nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// rootFromEnvelope porta il plafond dell'esercizio nelle radici dei suoi periodi: con un solo
// periodo la radice vale il plafond; con più periodi, la prima volta il plafond si divide in
// parti uguali, poi ogni periodo si modifica da sé (la somma delle radici resta il plafond).
func (s *Service) rootFromEnvelope(ctx context.Context, tx pgx.Tx, a engine.Actor, fyID string, env int64) error {
	periods, err := s.ensurePeriods(ctx, tx, a, fyID, "")
	if err != nil {
		if periods, err = s.Periods(ctx, tx, fyID); err != nil {
			return err
		}
	}
	if len(periods) == 0 {
		return nil
	}
	if len(periods) == 1 {
		_, err := s.EnsureRoot(ctx, tx, a, RootInput{PeriodID: periods[0].ID, AmountCents: env, Reason: "Plafond dell'esercizio"})
		return err
	}
	var sum int64
	var anyRoot bool
	for _, p := range periods {
		if p.RootID != nil {
			anyRoot = true
			v, err := s.allocated(ctx, tx, *p.RootID)
			if err != nil {
				return err
			}
			sum += v
		}
	}
	if anyRoot && sum != 0 {
		if sum == env {
			return nil
		}
		return engine.InvalidParams("validation.finance.envelope_multi_period", "envelope_cents", map[string]any{"sum": money(sum)}, "l'esercizio ha più periodi: modifica il plafond di ogni periodo (la somma è %s)", money(sum))
	}
	w := make([]float64, len(periods))
	for i := range w {
		w[i] = 1
	}
	for i, v := range splitProportional(env, w) {
		if _, err := s.EnsureRoot(ctx, tx, a, RootInput{PeriodID: periods[i].ID, AmountCents: v, Reason: "Plafond dell'esercizio"}); err != nil {
			return err
		}
	}
	return nil
}
