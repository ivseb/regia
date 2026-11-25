package demand

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

type Service struct {
	eng       *engine.Service
	approvals *approval.Service
}

// Register collega il modulo al motore: controlli del workflow e fatti per le approvazioni.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := &Service{eng: eng, approvals: ap}
	eng.Guard("estimate", func(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
		var high *int64
		if err := tx.QueryRow(ctx, "select estimate_high_cents from ext_demand where element_id = $1", el.ID).Scan(&high); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if high == nil {
			return engine.Required("estimate", "serve una stima di costo prima di questo passaggio")
		}
		return nil
	})
	eng.Guard("sponsor", func(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
			where sh.element_id = $1 and r.key = 'sponsor'`, el.ID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return engine.Required("sponsor", "serve uno sponsor prima di chiedere l'approvazione")
		}
		return nil
	})
	eng.Facts(TypeKey, s.facts)
	registerQueryFields(eng) // scadenza:, desiderata: (query.go)
	eng.DescribeFacts(TypeKey,
		engine.FactInfo{Key: "stima_min", Name: "Stima minima (euro)", Kind: "number", Example: "40000"},
		engine.FactInfo{Key: "stima_max", Name: "Stima massima (euro)", Kind: "number", Example: "120000"},
		engine.FactInfo{Key: "importo", Name: "Importo per le soglie (stima massima)", Kind: "number", Example: "120000"},
		engine.FactInfo{Key: "giornate", Name: "Giornate stimate", Kind: "number", Example: "60"},
		engine.FactInfo{Key: "driver", Name: "Driver", Kind: "text", Values: driverValues()},
		engine.FactInfo{Key: "unita", Name: "Unità richiedente", Kind: "text"},
		engine.FactInfo{Key: "obbligatoria", Name: "Obbligatoria", Kind: "bool"},
		engine.FactInfo{Key: "scadenza_normativa", Name: "Scadenza normativa", Kind: "date", Example: "2027-01-01"},
		engine.FactInfo{Key: "data_desiderata", Name: "Data desiderata", Kind: "date"},
		engine.FactInfo{Key: "valuta", Name: "Valuta", Kind: "text", Values: []string{"EUR"}},
		engine.FactInfo{Key: "punteggio", Name: "Punteggio della scorecard", Kind: "number"},
	)
	eng.Starter(starterProcess)
	ap.Facts(TypeKey, func(ctx context.Context, tx pgx.Tx, el *engine.Element) (approval.Facts, error) {
		var f approval.Facts
		var driver *string
		err := tx.QueryRow(ctx, "select estimate_high_cents, driver from ext_demand where element_id = $1", el.ID).Scan(&f.AmountCents, &driver)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return f, err
		}
		if driver != nil {
			f.Driver = *driver
		}
		return f, nil
	})
	return s
}

// driverNames sono le etichette italiane dei driver, usate anche nelle condizioni.
var driverNames = map[string]string{"strategic": "strategico", "regulatory": "normativo", "operational": "operativo",
	"efficiency": "efficienza", "risk": "rischio"}

// facts espone i dati della domanda alle condizioni di processo (gate, regole,
// applicabilità). Gli importi sono in unità della valuta, non in centesimi.
func (s *Service) facts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var low, high *int64
	var days *float64
	var driver, unit *string
	var mandatory bool
	var deadline, desired *time.Time
	var currency string
	var score *float64
	err := tx.QueryRow(ctx, `select d.estimate_low_cents, d.estimate_high_cents, d.estimate_effort_days, d.driver, u.name,
		d.is_mandatory, d.regulatory_deadline, d.desired_date, d.currency, d.score
		from ext_demand d left join org_units u on u.tenant_id = d.tenant_id and u.id = d.requesting_unit_id
		where d.element_id = $1`, el.ID).Scan(&low, &high, &days, &driver, &unit, &mandatory, &deadline, &desired, &currency, &score)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	euros := func(c *int64) any {
		if c == nil {
			return nil
		}
		return float64(*c) / 100
	}
	f.Set("stima_min", euros(low))
	f.Set("stima_max", euros(high))
	f.Set("importo", euros(high))
	f.Set("giornate", days)
	if driver != nil {
		f.Set("driver", *driver, driverNames[*driver])
	}
	f.Set("unita", unit)
	f.Set("obbligatoria", mandatory)
	f.Set("scadenza_normativa", deadline)
	f.Set("data_desiderata", desired)
	f.Set("valuta", currency)
	f.Set("punteggio", score)
	return nil
}

// Data sono i dati della domanda (ext_demand) modificabili.
type Data struct {
	RequestingUnitID   *string  `json:"requesting_unit_id,omitempty"`
	Driver             *string  `json:"driver,omitempty"` // strategic | regulatory | operational | efficiency | risk
	IsMandatory        *bool    `json:"is_mandatory,omitempty"`
	RegulatoryDeadline *string  `json:"regulatory_deadline,omitempty"` // AAAA-MM-GG
	DesiredDate        *string  `json:"desired_date,omitempty"`
	EstimateLowCents   *int64   `json:"estimate_low_cents,omitempty"`
	EstimateHighCents  *int64   `json:"estimate_high_cents,omitempty"`
	EstimateEffortDays *float64 `json:"estimate_effort_days,omitempty"`
	Currency           *string  `json:"currency,omitempty"`
	// BudgetPeriodID è il periodo di budget di competenza; "" lo toglie.
	BudgetPeriodID *string `json:"budget_period_id,omitempty"`
}

// NewRequest è una nuova business request.
type NewRequest struct {
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	SponsorID       string         `json:"sponsor_id"`
	BusinessOwnerID string         `json:"business_owner_id"`
	Fields          map[string]any `json:"fields"`
	Data
}

var drivers = map[string]bool{"strategic": true, "regulatory": true, "operational": true, "efficiency": true, "risk": true}

func (d Data) validate() error {
	if d.Driver != nil && !drivers[*d.Driver] {
		return engine.InvalidValue("driver", "valore non ammesso (strategic, regulatory, operational, efficiency, risk)")
	}
	for name, v := range map[string]*string{"regulatory_deadline": d.RegulatoryDeadline, "desired_date": d.DesiredDate} {
		if v != nil {
			if _, err := time.Parse(time.DateOnly, *v); err != nil {
				return engine.DateInvalid(name, "data non valida, formato AAAA-MM-GG")
			}
		}
	}
	if d.EstimateLowCents != nil && *d.EstimateLowCents < 0 {
		return engine.InvalidCode("validation.not_negative", "estimate_low_cents", "non può essere negativa")
	}
	if d.EstimateLowCents != nil && d.EstimateHighCents != nil && *d.EstimateHighCents < *d.EstimateLowCents {
		return engine.InvalidCode("validation.demand.estimate_range", "estimate_high_cents", "il massimo è minore del minimo")
	}
	if d.Currency != nil && (len(*d.Currency) != 3 || strings.ToUpper(*d.Currency) != *d.Currency) {
		return engine.InvalidCode("validation.demand.currency_code", "currency", "codice ISO 4217 di tre lettere maiuscole")
	}
	return nil
}

// Create apre una business request in bozza. Il richiedente è chi la crea; il demand
// manager e il PMO sono i gruppi omonimi del tenant.
func (s *Service) Create(ctx context.Context, tx pgx.Tx, actor engine.Actor, in NewRequest) (*engine.Element, error) {
	if !actor.Has("element.write") && !actor.Has("portal.submit") {
		return nil, engine.ErrForbidden
	}
	if err := in.Data.validate(); err != nil {
		return nil, err
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{
		SpaceKey: SpaceKey, TypeKey: TypeKey, Title: in.Title, BodyText: in.Description, Fields: in.Fields, DeferStart: true,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_demand (tenant_id, element_id) values (app_tenant(), $1)`, el.ID); err != nil {
		return nil, err
	}
	if err := s.writeData(ctx, tx, el.ID, in.Data); err != nil {
		return nil, err
	}
	set := func(role, member, group string) error {
		if member == "" && group == "" {
			return nil
		}
		return engine.SetStakeholder(ctx, tx, actor, el.ID, role, member, group, false)
	}
	if err := set("requester", actor.MemberID, ""); err != nil {
		return nil, err
	}
	if err := set("sponsor", in.SponsorID, ""); err != nil {
		return nil, err
	}
	if err := set("business_owner", in.BusinessOwnerID, ""); err != nil {
		return nil, err
	}
	// Gruppi di presa in carico, fase iniziale e regole: li decide il processo.
	return s.eng.StartProcess(ctx, tx, actor, el)
}

// UpdateData modifica i dati della domanda: chi può scrivere l'elemento, oppure demand manager e PMO.
func (s *Service) UpdateData(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string, d Data) error {
	el, err := s.eng.Get(ctx, tx, actor, elementID)
	if err != nil {
		return err
	}
	if el.TypeKey != TypeKey {
		return engine.ErrNotFound
	}
	allowed := actor.Has("element.write")
	if !allowed {
		if allowed, err = engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{"demand_manager", "pmo"}); err != nil {
			return err
		}
	}
	if !allowed {
		return engine.ErrForbidden
	}
	if err := d.validate(); err != nil {
		return err
	}
	if err := s.writeData(ctx, tx, el.ID, d); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "field", "demand", d, nil); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "demand.update", EntityType: TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

func (s *Service) writeData(ctx context.Context, tx pgx.Tx, elementID string, d Data) error {
	_, err := tx.Exec(ctx, `update ext_demand set
		requesting_unit_id   = case when $2::text = '' then null else coalesce($2::text::uuid, requesting_unit_id) end,
		driver               = coalesce($3, driver),
		is_mandatory         = coalesce($4, is_mandatory),
		regulatory_deadline  = coalesce($5::date, regulatory_deadline),
		desired_date         = coalesce($6::date, desired_date),
		estimate_low_cents   = coalesce($7, estimate_low_cents),
		estimate_high_cents  = coalesce($8, estimate_high_cents),
		estimate_effort_days = coalesce($9, estimate_effort_days),
		currency             = coalesce($10, currency),
		budget_period_id     = case when $11::text = '' then null else coalesce($11::text::uuid, budget_period_id) end
		where element_id = $1`,
		elementID, d.RequestingUnitID, d.Driver, d.IsMandatory, d.RegulatoryDeadline, d.DesiredDate,
		d.EstimateLowCents, d.EstimateHighCents, d.EstimateEffortDays, d.Currency, d.BudgetPeriodID)
	if err != nil && strings.Contains(err.Error(), "ext_demand_budget_period_fk") {
		return engine.NotFound("budget_period_id", "period", "periodo di budget inesistente")
	}
	if err != nil && strings.Contains(err.Error(), "foreign key") {
		return engine.InvalidCode("validation.org_unit_not_found", "requesting_unit_id", "unità organizzativa inesistente")
	}
	return err
}

// View sono i dati della domanda da mostrare nel dettaglio.
type View struct {
	RequestingUnitID   *string    `json:"requesting_unit_id"`
	RequestingUnit     *string    `json:"requesting_unit"`
	Driver             *string    `json:"driver"`
	IsMandatory        bool       `json:"is_mandatory"`
	RegulatoryDeadline *time.Time `json:"regulatory_deadline"`
	DesiredDate        *time.Time `json:"desired_date"`
	EstimateLowCents   *int64     `json:"estimate_low_cents"`
	EstimateHighCents  *int64     `json:"estimate_high_cents"`
	EstimateEffortDays *float64   `json:"estimate_effort_days"`
	Currency           string     `json:"currency"`
	Score              *float64   `json:"score"`
	BudgetPeriodID     *string    `json:"budget_period_id"`
	BudgetPeriod       *string    `json:"budget_period"`
}

func (s *Service) View(ctx context.Context, tx pgx.Tx, elementID string) (*View, error) {
	v := &View{}
	err := tx.QueryRow(ctx, `select d.requesting_unit_id, u.name, d.driver, d.is_mandatory, d.regulatory_deadline, d.desired_date,
		d.estimate_low_cents, d.estimate_high_cents, d.estimate_effort_days, d.currency, d.score,
		d.budget_period_id, bp.name
		from ext_demand d left join org_units u on u.tenant_id = d.tenant_id and u.id = d.requesting_unit_id
		left join budget_periods bp on bp.tenant_id = d.tenant_id and bp.id = d.budget_period_id
		where d.element_id = $1`, elementID).Scan(&v.RequestingUnitID, &v.RequestingUnit, &v.Driver, &v.IsMandatory,
		&v.RegulatoryDeadline, &v.DesiredDate, &v.EstimateLowCents, &v.EstimateHighCents, &v.EstimateEffortDays, &v.Currency, &v.Score,
		&v.BudgetPeriodID, &v.BudgetPeriod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// OrgUnit è un'unità organizzativa (unità di business, centro di costo...).
type OrgUnit struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Code     *string `json:"code"`
	ParentID *string `json:"parent_id"`
}

func CreateOrgUnit(ctx context.Context, tx pgx.Tx, actor engine.Actor, u OrgUnit) (OrgUnit, error) {
	if !actor.Has("tenant.admin") {
		return u, engine.ErrForbidden
	}
	u.Name = strings.TrimSpace(u.Name)
	if u.Name == "" {
		return u, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if u.Kind == "" {
		u.Kind = "business_unit"
	}
	switch u.Kind {
	case "legal_entity", "business_unit", "department", "cost_center":
	default:
		return u, engine.InvalidValue("kind", "tipo non ammesso")
	}
	u.ID = db.NewID()
	if _, err := tx.Exec(ctx, `insert into org_units (tenant_id, id, name, kind, code, parent_id) values (app_tenant(), $1, $2, $3, $4, $5)`,
		u.ID, u.Name, u.Kind, u.Code, u.ParentID); err != nil {
		return u, err
	}
	return u, audit.Append(ctx, tx, audit.Event{Action: "org_unit.create", EntityType: "org_unit", EntityID: u.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

func ListOrgUnits(ctx context.Context, tx pgx.Tx) ([]OrgUnit, error) {
	return SearchOrgUnits(ctx, tx, "", 0, []string{})
}

// SearchOrgUnits: come ListOrgUnits, con ricerca nel nome o nel codice (q già resa letterale per
// ilike), massimo di righe (0 = tutte) e restrizione a certi identificativi.
func SearchOrgUnits(ctx context.Context, tx pgx.Tx, q string, limit int, ids []string) ([]OrgUnit, error) {
	if ids == nil {
		ids = []string{} // NULL escluderebbe ogni riga
	}
	rows, err := tx.Query(ctx, `select id, name, kind, code, parent_id from org_units
		where not archived and ($1 = '' or name ilike '%' || $1 || '%' or code ilike '%' || $1 || '%')
		  and (cardinality($2::text[]) = 0 or id::text = any($2))
		order by name, id limit nullif($3, 0)`, q, ids, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[OrgUnit])
}

// driverValues sono i nomi dei driver, per i valori suggeriti nell'editor dei processi.
func driverValues() []string {
	out := make([]string, 0, len(driverNames))
	for _, k := range []string{"strategic", "regulatory", "operational", "efficiency", "risk"} {
		if n, ok := driverNames[k]; ok {
			out = append(out, n)
		}
	}
	return out
}
