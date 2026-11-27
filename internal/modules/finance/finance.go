// Package finance è il modulo budget e costi del demand (demand.md §9-10; piano, passi
// 11 e 12): esercizi, categorie e fonti di finanziamento, versioni del budget con
// approvazione, trasferimenti motivati, voci di costo manuali e importate da CSV, ore per
// tariffe, valute con cambi storici, totali per richiesta ed earned value opzionale.
//
// Riservatezza (decisione 2026-11-24): tariffe e singole voci di costo stanno in tabelle
// con la policy RLS restrittiva finance_only. Le funzioni che le toccano lavorano in una
// transazione aperta con db.TenantOpts{Finance: true}, che l'API apre solo dopo il
// controllo di finance.read; senza, la RLS non restituisce nulla. Chi non ha il permesso
// vede soltanto gli aggregati (cost_rollups, totali del budget).
package finance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/rbac"
)

// Chiavi delle politiche di approvazione (approval_policies.stage) usate dal modulo.
const (
	PolicyBudget   = "@budget"
	PolicyTransfer = "@budget_transfer"
	subjectBudget  = "budget"
	subjectTrans   = "budget_transfer"
	requestType    = "business_request"
)

type Service struct {
	eng *engine.Service
	ap  *approval.Service
	// Changed, se impostata, riceve l'elemento il cui budget in vigore è cambiato (versione
	// approvata, previsione pubblicata, trasferimento approvato): il modulo dei progetti la usa per
	// confrontare il budget con la baseline e aprire la change request oltre la tolleranza.
	Changed func(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) error
}

// changed avvisa chi segue i budget (se c'è qualcuno).
func (s *Service) changed(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) error {
	if s.Changed == nil {
		return nil
	}
	return s.Changed(ctx, tx, actor, elementID)
}

// New crea il servizio senza registrare reazioni (per il job giornaliero).
func New(eng *engine.Service, ap *approval.Service) *Service { return &Service{eng: eng, ap: ap} }

// Register crea il servizio e lo collega ai motori: esito delle approvazioni di budget e
// trasferimenti, fatti per le condizioni di processo.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := New(eng, ap)
	ap.OnClose(subjectBudget, s.closeBudget)
	ap.OnClose(subjectTrans, s.closeTransfer)
	eng.Facts(requestType, s.facts)
	return s
}

// facts espone alle condizioni (gate, regole) il budget approvato, il consuntivo e il
// consumo in percentuale. Sono aggregati: nessun dato riservato.
func (s *Service) facts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var budget, actual int64
	if err := tx.QueryRow(ctx, `select
		coalesce((select sum(l.amount_cents) from budgets b join budget_lines l on l.tenant_id = b.tenant_id and l.budget_id = b.id
		          where b.element_id = $1 and b.status = 'approved' and b.kind <> 'forecast'), 0),
		coalesce((select sum(r.amount_base_cents) from cost_rollups r where r.element_id in (select id from finance_family($1)) and r.kind = 'actual'), 0)`,
		el.ID).Scan(&budget, &actual); err != nil {
		return err
	}
	f.Set("budget_approvato", float64(budget)/100)
	f.Set("consuntivo", float64(actual)/100)
	if budget > 0 {
		f.Set("consumo_budget", float64(actual)*100/float64(budget))
	}
	return nil
}

// CanRead dice se l'attore vede tariffe e singole voci (e quindi se aprire app.finance).
func CanRead(a engine.Actor) bool { return a.Has(rbac.FinanceRead) }

func requireRead(a engine.Actor) error {
	if !a.Has(rbac.FinanceRead) {
		return engine.ErrForbidden
	}
	return nil
}

func requireWrite(a engine.Actor) error {
	if !a.Has(rbac.FinanceWrite) {
		return engine.ErrForbidden
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullablePtr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Format(time.DateOnly)
	return &v
}

func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, engine.DateInvalid(field, "data non valida (AAAA-MM-GG)")
	}
	return t, nil
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func monthEnd(t time.Time) time.Time { return monthStart(t).AddDate(0, 1, -1) }

// BaseCurrency è la valuta base del tenant (tenants.base_currency, scelta alla creazione).
func BaseCurrency(ctx context.Context, tx pgx.Tx) (string, error) {
	var c string
	err := tx.QueryRow(ctx, "select base_currency from tenants where id = app_tenant()").Scan(&c)
	return c, err
}

func normCurrency(c, base string) (string, error) {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return base, nil
	}
	if len(c) != 3 || strings.Trim(c, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return "", engine.InvalidValue("currency", "valuta non valida (codice ISO a tre lettere)")
	}
	return c, nil
}

// ErrNoRate: manca il cambio di una valuta per la data.
var ErrNoRate = errors.New("cambio mancante")

// convert porta un importo nella valuta base con il cambio valido alla data (l'ultimo
// con valid_on non successivo). Restituisce l'importo convertito e il cambio usato.
func convert(ctx context.Context, tx pgx.Tx, amount int64, currency, base string, on time.Time) (int64, string, error) {
	if currency == base {
		return amount, "1", nil
	}
	var out int64
	var rate string
	err := tx.QueryRow(ctx, `select round($1::numeric * rate_to_base)::bigint, rate_to_base::text from exchange_rates
		where currency = $2 and valid_on <= $3 order by valid_on desc limit 1`, amount, currency, on).Scan(&out, &rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", fmt.Errorf("%w: %s al %s", ErrNoRate, currency, on.Format("02/01/2006"))
	}
	return out, rate, err
}
