// Package report è l'avanzamento del demand (demand.md §11, §14 D01, D11, D12; piano,
// passi 13 e 14): status report periodici con semafori precompilati dai dati, dashboard
// di portafoglio con numeri che portano alla query corrispondente, portale del richiedente.
//
// Riservatezza: il modulo legge solo aggregati del budget e dei costi (budget approvati,
// cost_rollups, fotografie dell'earned value). Le transazioni non aprono mai app.finance:
// tariffe e singole voci restano invisibili anche se il codice sbagliasse una query.
// Il dettaglio per categoria e per richiesta del cruscotto si mostra solo a chi ha
// finance.read; i totali a tutti.
package report

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/finance"
	"regia/internal/modules/planning"
	"regia/internal/platform/audit"
	"regia/internal/platform/rbac"
)

const (
	requestType = "business_request"
	impactType  = "impact"
	reviewType  = "review"
)

type Service struct {
	eng  *engine.Service
	plan *planning.Service
	fin  *finance.Service
	// Now è l'orologio (sostituibile nei test).
	Now func() time.Time
}

// Register crea il servizio e registra i campi di query del cruscotto sul motore.
// Va chiamato una sola volta per motore.
func Register(eng *engine.Service) *Service {
	s := New(eng)
	registerQueryFields(eng)
	return s
}

// New crea il servizio senza registrare nulla (per i job).
func New(eng *engine.Service) *Service {
	return &Service{eng: eng, plan: planning.New(eng), fin: finance.New(eng, nil), Now: time.Now}
}

// ---------------------------------------------------------------------------
// Data di oggi nel fuso del tenant (lo stesso del linguaggio di query: «oggi»).

type clock struct {
	today time.Time // mezzanotte UTC del giorno locale: si confronta con le colonne date
	loc   *time.Location
}

func (s *Service) clock(ctx context.Context, tx pgx.Tx) (clock, error) {
	var tz string
	if err := tx.QueryRow(ctx, `select coalesce((select default_timezone from tenants where id = app_tenant()), 'Europe/Rome')`).Scan(&tz); err != nil {
		return clock{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation("Europe/Rome")
	}
	n := s.Now().In(loc)
	return clock{today: time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC), loc: loc}, nil
}

// startOf è l'inizio del giorno d (AAAA-MM-GG) nel fuso del tenant, come «creata: d..» della query.
func (c clock) startOf(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, c.loc)
}

func day(t time.Time) string { return t.Format(time.DateOnly) }

func dayPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.DateOnly)
	return &s
}

func parseDay(field, v string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, engine.InvalidCode("validation.date_invalid", field, "data non valida (AAAA-MM-GG)")
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Periodicità

var frequencies = map[string]string{"weekly": "settimanale", "biweekly": "ogni due settimane", "monthly": "mensile", "off": "nessuna"}

// addPeriod è la data del report successivo.
func addPeriod(t time.Time, freq string) time.Time {
	switch freq {
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 14)
	}
}

// ---------------------------------------------------------------------------
// Impostazioni del tenant

// Indicator è un indicatore normativo del cruscotto: una query con un'etichetta.
type Indicator struct {
	Label string `json:"label"`
	Query string `json:"query"`
}

type Settings struct {
	Frequency    string      `json:"frequency"`
	ReminderDays int         `json:"reminder_days"`
	Phases       []string    `json:"phases"`
	Regulatory   []Indicator `json:"regulatory"`
	UpdatedAt    *time.Time  `json:"updated_at"`
	CanEdit      bool        `json:"can_edit"`
}

func defaultSettings() Settings {
	return Settings{Frequency: "biweekly", ReminderDays: 2, Phases: []string{"Pianificazione", "In esecuzione"},
		Regulatory: []Indicator{{"DORA", "DORA"}, {"NIS2", "NIS2"}}}
}

// GetSettings legge le impostazioni del tenant (quelle di default se mancano).
func GetSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	st := defaultSettings()
	var reg []byte
	var at time.Time
	err := tx.QueryRow(ctx, `select frequency, reminder_days, phases, regulatory, updated_at from report_settings`).
		Scan(&st.Frequency, &st.ReminderDays, &st.Phases, &reg, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.UpdatedAt = &at
	st.Regulatory = nil
	if err := json.Unmarshal(reg, &st.Regulatory); err != nil {
		return st, err
	}
	if st.Phases == nil {
		st.Phases = []string{}
	}
	if st.Regulatory == nil {
		st.Regulatory = []Indicator{}
	}
	return st, nil
}

func canConfigure(a engine.Actor) bool {
	return a.Has(rbac.PortfolioAdmin) || a.Has(rbac.TenantAdmin)
}

// Settings restituisce le impostazioni con il permesso di modificarle.
func (s *Service) Settings(ctx context.Context, tx pgx.Tx, actor engine.Actor) (Settings, error) {
	if !actor.Has(rbac.ElementRead) {
		return Settings{}, engine.ErrForbidden
	}
	st, err := GetSettings(ctx, tx)
	st.CanEdit = canConfigure(actor)
	return st, err
}

type SettingsInput struct {
	Frequency    *string      `json:"frequency"`
	ReminderDays *int         `json:"reminder_days"`
	Phases       *[]string    `json:"phases"`
	Regulatory   *[]Indicator `json:"regulatory"`
}

// UpdateSettings: governo del portafoglio o amministratore del tenant.
func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (Settings, error) {
	if !canConfigure(actor) {
		return Settings{}, engine.ErrForbidden
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return st, err
	}
	if in.Frequency != nil {
		if _, ok := frequencies[*in.Frequency]; !ok {
			return st, engine.InvalidValue("frequency", "periodicità non ammessa (weekly, biweekly, monthly, off)")
		}
		st.Frequency = *in.Frequency
	}
	if in.ReminderDays != nil {
		if *in.ReminderDays < 0 || *in.ReminderDays > 30 {
			return st, engine.OutOfRange("reminder_days", 0, 30, "tra 0 e 30 giorni")
		}
		st.ReminderDays = *in.ReminderDays
	}
	if in.Phases != nil {
		t, err := engine.LoadType(ctx, tx, requestType)
		if err != nil {
			return st, err
		}
		known := map[string]bool{}
		for _, p := range t.States {
			known[p.Name] = true
		}
		out := []string{}
		for _, p := range *in.Phases {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !known[p] {
				return st, engine.NotFound("phases", "", "fase «%s» inesistente nel processo delle richieste", p)
			}
			out = append(out, p)
		}
		st.Phases = out
	}
	if in.Regulatory != nil {
		out := []Indicator{}
		for i, r := range *in.Regulatory {
			r.Label, r.Query = strings.TrimSpace(r.Label), strings.TrimSpace(r.Query)
			if r.Label == "" || r.Query == "" {
				return st, engine.Required("regulatory", "indicatore %d: servono etichetta e query", i+1)
			}
			if len(r.Label) > 40 || len(r.Query) > 500 {
				return st, engine.Required("regulatory", "indicatore %d: etichetta fino a 40 caratteri, query fino a 500", i+1)
			}
			// La query deve essere valida nel tenant (campi, valori), non solo nella sintassi.
			if _, err := s.eng.Search(ctx, tx, actor, "tipo: "+requestType+" "+r.Query, 1, ""); err != nil {
				var qe *engine.QueryError
				if errors.As(err, &qe) {
					return st, engine.Required("regulatory", "indicatore «%s»: %s", r.Label, qe.Msg)
				}
				return st, err
			}
			out = append(out, r)
		}
		if len(out) > 8 {
			return st, engine.InvalidCode("validation.report.regulatory_too_many", "regulatory", "al massimo 8 indicatori")
		}
		st.Regulatory = out
	}
	reg, _ := json.Marshal(st.Regulatory)
	if _, err := tx.Exec(ctx, `insert into report_settings (tenant_id, frequency, reminder_days, phases, regulatory, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, now())
		on conflict (tenant_id) do update set frequency = excluded.frequency, reminder_days = excluded.reminder_days,
			phases = excluded.phases, regulatory = excluded.regulatory, updated_by = excluded.updated_by, updated_at = now()`,
		st.Frequency, st.ReminderDays, st.Phases, reg, nullable(actor.MemberID)); err != nil {
		return st, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "report.settings.update", EntityType: "report_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"frequency": st.Frequency, "reminder_days": st.ReminderDays, "phases": len(st.Phases), "regulatory": len(st.Regulatory)}}); err != nil {
		return st, err
	}
	return s.Settings(ctx, tx, actor)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------------------
// Campi di query del cruscotto (piano, passo 16): ogni numero porta alla lista con la
// query che lo riproduce. I valori senza un id (driver, semaforo, obbligo) si
// confrontano su un uuid derivato dal testo (md5), con un indice dove serve.

func registerQueryFields(eng *engine.Service) {
	eng.QueryField(engine.QueryField{
		Key: "unit", Names: []string{"unita", "unit"},
		Label:   "Unità richiedente della richiesta (nome)",
		Kind:    "ref",
		Types:   []string{"business_request"},
		Source:  `select element_id, requesting_unit_id as v from ext_demand where requesting_unit_id is not null`,
		Lookup:  `select id::text, lower(name) from org_units where lower(name) = any($1)`,
		Suggest: `select name, case when archived then 'archiviata' else 'unità' end from org_units where name ilike $1 order by archived, name limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "driver", Names: []string{"driver", "motivazione"},
		Label:  "Driver della richiesta: strategico, normativo, operativo, efficienza, rischio",
		Kind:   "ref",
		Types:  []string{"business_request"},
		Source: `select element_id, md5('driver:' || driver)::uuid as v from ext_demand where driver is not null`,
		Lookup: `select md5('driver:' || k)::uuid::text, n from (values ` + driverValues + `) x(k, n) where n = any($1)`,
		Suggest: `select n, 'driver' from (values ('strategico'), ('normativo'), ('operativo'), ('efficienza'), ('rischio')) x(n)
			where n ilike $1 limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "mandatory", Names: []string{"obbligatoria", "obbligo", "mandatory"},
		Label:   "Richiesta con obbligo normativo (sì)",
		Kind:    "ref",
		Types:   []string{"business_request"},
		Source:  `select element_id, md5('mandatory')::uuid as v from ext_demand where is_mandatory`,
		Lookup:  `select md5('mandatory')::uuid::text, n from (values ('si'), ('sì'), ('yes'), ('true')) x(n) where n = any($1)`,
		Suggest: `select n, 'obbligo normativo' from (values ('sì')) x(n) where n ilike $1 limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "approved_budget", Names: []string{"budget_approvato"},
		Label:  "Budget approvato (presente o vuoto)",
		Kind:   "ref",
		Types:  []string{"business_request"},
		Source: `select element_id, id as v from budgets where status = 'approved' and kind <> 'forecast'`,
	})
	eng.QueryField(engine.QueryField{
		Key: "costs", Names: []string{"con_costi"},
		Label: "Costi registrati sulla richiesta, sui suoi impatti o sui figli (presente o vuoto)",
		Kind:  "ref",
		Types: []string{"business_request"},
		Source: `select element_id, element_id as v from cost_rollups
			union all select x.request_id, x.request_id from cost_rollups r join ext_impact x on x.tenant_id = r.tenant_id and x.element_id = r.element_id
			union all select e.parent_id, e.parent_id from cost_rollups r join elements e on e.tenant_id = r.tenant_id and e.id = r.element_id
				where e.parent_id is not null and e.deleted_at is null`,
	})
	eng.QueryField(engine.QueryField{
		Key: "rag", Names: []string{"semaforo", "rag"},
		Label:   "Semaforo dell'ultimo status report: verde, giallo, rosso",
		Kind:    "ref",
		Types:   []string{"business_request"},
		Source:  `select element_id, md5('rag:' || last_overall)::uuid as v from status_report_schedules where last_overall is not null`,
		Lookup:  `select md5('rag:' || k)::uuid::text, n from (values ` + ragValues + `) x(k, n) where n = any($1)`,
		Suggest: `select n, 'semaforo' from (values ('verde'), ('giallo'), ('rosso')) x(n) where n ilike $1 limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "next_report", Names: []string{"prossimo_report", "next_report"},
		Label: "Data del prossimo status report atteso (<oggi = in ritardo)",
		Kind:  "date", DateOnly: true, Types: []string{"business_request"},
		Source: `select element_id, next_due as v from status_report_schedules where next_due is not null`,
	})
	eng.QueryField(engine.QueryField{
		Key: "review_team", Names: []string{"valutatore", "team_valutatore", "review_team"},
		Label:   "Team valutatore di una valutazione (nome o chiave)",
		Kind:    "ref",
		Types:   []string{"business_request"},
		Source:  `select element_id, team_id as v from ext_review`,
		Lookup:  `select id::text, k from review_teams, unnest(array[key, lower(name)]) k where k = any($1)`,
		Suggest: `select name, 'team valutatore' from review_teams where name ilike $1 and not archived order by name limit $2`,
	})
	eng.QueryField(engine.QueryField{
		Key: "sla_due", Names: []string{"scadenza_sla", "sla"},
		Label: "Scadenza dello SLA delle valutazioni in corso (<oggi = in ritardo)",
		Kind:  "date", DateOnly: true, Types: []string{"business_request"},
		Source: `select element_id, due_on as v from ext_review where stage in ('submitted', 'exam') and due_on is not null`,
	})
}

const driverValues = `('strategic', 'strategico'), ('strategic', 'strategic'), ('regulatory', 'normativo'), ('regulatory', 'regulatory'),
	('operational', 'operativo'), ('operational', 'operational'), ('efficiency', 'efficienza'), ('efficiency', 'efficiency'),
	('risk', 'rischio'), ('risk', 'risk')`

const ragValues = `('green', 'verde'), ('green', 'green'), ('amber', 'giallo'), ('amber', 'ambra'), ('amber', 'amber'), ('red', 'rosso'), ('red', 'red')`

// driverWord è il nome italiano del driver nella query.
var driverWord = map[string]string{"strategic": "strategico", "regulatory": "normativo", "operational": "operativo", "efficiency": "efficienza", "risk": "rischio"}

var driverLabel = map[string]string{"strategic": "Strategico", "regulatory": "Normativo", "operational": "Operativo", "efficiency": "Efficienza", "risk": "Riduzione del rischio"}

var ragWord = map[string]string{"green": "verde", "amber": "giallo", "red": "rosso"}

// quote mette un valore tra virgolette per la query (le virgolette interne si tolgono).
func quote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, "") + `"`
}
