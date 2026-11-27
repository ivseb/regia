package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Budget è una versione del budget di un elemento (richiesta, progetto, programma,
// portafoglio) per un esercizio.
type Budget struct {
	ID              string     `json:"id"`
	ElementID       string     `json:"element_id"`
	ElementKey      string     `json:"element_key"`
	ElementTitle    string     `json:"element_title"`
	FiscalYearID    string     `json:"fiscal_year_id"`
	FiscalYear      string     `json:"fiscal_year"`
	Kind            string     `json:"kind"` // requested | revised | forecast
	Version         int        `json:"version"`
	Status          string     `json:"status"` // draft | submitted | approved | superseded
	Note            string     `json:"note"`
	DecisionNote    *string    `json:"decision_note"`
	BasedOnID       *string    `json:"based_on_id"`
	TotalCents      int64      `json:"total_cents"`
	EffectiveCents  int64      `json:"effective_cents"` // con i trasferimenti approvati
	ApprovalID      *string    `json:"approval_request_id"`
	ApprovalStatus  *string    `json:"approval_status"`
	ApprovedBy      *string    `json:"approved_by"`
	ApprovedAt      *time.Time `json:"approved_at"`
	SubmittedAt     *time.Time `json:"submitted_at"`
	CreatedAt       time.Time  `json:"created_at"`
	Lines           []Line     `json:"lines,omitempty"`
	CanEdit         bool       `json:"can_edit"`
	PendingTransfer bool       `json:"pending_transfers"`
	// AllocationID: lo stanziamento (budget gerarchico) da cui il budget attinge; l'approvazione lo impegna.
	AllocationID *string `json:"allocation_id"`
}

// Line è una riga del budget: categoria, centro di costo, fonte, periodo (mese) e importo
// nella valuta base. EffectiveCents tiene conto dei trasferimenti approvati.
type Line struct {
	ID              string  `json:"id"`
	CategoryID      string  `json:"cost_category_id"`
	Category        string  `json:"category"`
	Nature          string  `json:"nature"`
	OrgUnitID       *string `json:"org_unit_id"`
	OrgUnit         *string `json:"org_unit"`
	FundingSourceID *string `json:"funding_source_id"`
	FundingSource   *string `json:"funding_source"`
	Period          *string `json:"period"`
	AmountCents     int64   `json:"amount_cents"`
	EffectiveCents  int64   `json:"effective_cents"`
	PendingOutCents int64   `json:"pending_out_cents"`
	Note            *string `json:"note"`
}

const budgetSelect = `
	select b.id, b.element_id, coalesce(s.key || '-' || e.number, ''), e.title, b.fiscal_year_id, f.name, b.kind, b.version, b.status,
	       b.note, b.decision_note, b.based_on_id,
	       coalesce((select sum(l.amount_cents) from budget_lines l where l.tenant_id = b.tenant_id and l.budget_id = b.id), 0),
	       coalesce((select sum(l.amount_cents) from budget_lines l where l.tenant_id = b.tenant_id and l.budget_id = b.id), 0)
	         + coalesce((select sum(t.amount_cents) from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id and l.id = t.to_line_id
	                     where l.budget_id = b.id and t.status = 'approved'), 0)
	         - coalesce((select sum(t.amount_cents) from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id and l.id = t.from_line_id
	                     where l.budget_id = b.id and t.status = 'approved'), 0),
	       b.approval_request_id, ar.status, m.display_name, b.approved_at, b.submitted_at, b.created_at,
	       exists (select 1 from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id and (l.id = t.from_line_id or l.id = t.to_line_id)
	               where l.budget_id = b.id and t.status = 'pending'),
	       b.allocation_id
	from budgets b
	join elements e on e.tenant_id = b.tenant_id and e.id = b.element_id
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	join fiscal_years f on f.tenant_id = b.tenant_id and f.id = b.fiscal_year_id
	left join approval_requests ar on ar.tenant_id = b.tenant_id and ar.id = b.approval_request_id
	left join members m on m.tenant_id = b.tenant_id and m.id = b.approved_by`

func scanBudgets(rows pgx.Rows) ([]Budget, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Budget, error) {
		var b Budget
		err := r.Scan(&b.ID, &b.ElementID, &b.ElementKey, &b.ElementTitle, &b.FiscalYearID, &b.FiscalYear, &b.Kind, &b.Version, &b.Status,
			&b.Note, &b.DecisionNote, &b.BasedOnID, &b.TotalCents, &b.EffectiveCents, &b.ApprovalID, &b.ApprovalStatus, &b.ApprovedBy,
			&b.ApprovedAt, &b.SubmittedAt, &b.CreatedAt, &b.PendingTransfer, &b.AllocationID)
		return b, err
	})
	if out == nil {
		out = []Budget{}
	}
	return out, err
}

// BudgetFilter: esercizio, elemento (chiave o id), solo le versioni correnti.
type BudgetFilter struct {
	FiscalYearID string
	ElementID    string
	CurrentOnly  bool
}

// Budgets elenca le versioni del budget (le più recenti prima). Non contiene dati
// riservati, ma è una vista di dettaglio: l'API la espone a chi ha finance.read.
func (s *Service) Budgets(ctx context.Context, tx pgx.Tx, f BudgetFilter) ([]Budget, error) {
	q := budgetSelect + ` where e.deleted_at is null and ($1 = '' or b.fiscal_year_id::text = $1) and ($2 = '' or b.element_id::text = $2)
		and (not $3 or b.status <> 'superseded')
		order by f.start_date desc, e.number, b.version desc limit 1000`
	rows, err := tx.Query(ctx, q, f.FiscalYearID, f.ElementID, f.CurrentOnly)
	if err != nil {
		return nil, err
	}
	return scanBudgets(rows)
}

// Budget restituisce una versione con le sue righe.
func (s *Service) Budget(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Budget, error) {
	rows, err := tx.Query(ctx, budgetSelect+` where b.id = $1`, id)
	if err != nil {
		return nil, err
	}
	bs, err := scanBudgets(rows)
	if err != nil {
		return nil, err
	}
	if len(bs) == 0 {
		return nil, engine.ErrNotFound
	}
	b := &bs[0]
	if ok, err := s.eng.CanRead(ctx, tx, actor, b.ElementID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrNotFound)
	}
	if b.Lines, err = lines(ctx, tx, b.ID); err != nil {
		return nil, err
	}
	b.CanEdit = b.Status == "draft" && actor.Has("finance.write")
	return b, nil
}

func firstErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func lines(ctx context.Context, tx pgx.Tx, budgetID string) ([]Line, error) {
	rows, err := tx.Query(ctx, `
		select l.id, l.cost_category_id, c.name, c.nature, l.org_unit_id, u.name, l.funding_source_id, fs.name, l.period, l.amount_cents,
		       l.amount_cents + coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = l.id and t.status = 'approved'), 0)
		                      - coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status = 'approved'), 0),
		       coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status = 'pending'), 0),
		       l.note
		from budget_lines l
		join cost_categories c on c.tenant_id = l.tenant_id and c.id = l.cost_category_id
		left join org_units u on u.tenant_id = l.tenant_id and u.id = l.org_unit_id
		left join funding_sources fs on fs.tenant_id = l.tenant_id and fs.id = l.funding_source_id
		where l.budget_id = $1
		order by c.nature, c.name, l.period nulls first, l.id`, budgetID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Line, error) {
		var l Line
		var period *time.Time
		err := r.Scan(&l.ID, &l.CategoryID, &l.Category, &l.Nature, &l.OrgUnitID, &l.OrgUnit, &l.FundingSourceID, &l.FundingSource,
			&period, &l.AmountCents, &l.EffectiveCents, &l.PendingOutCents, &l.Note)
		l.Period = dateStr(period)
		return l, err
	})
	if out == nil {
		out = []Line{}
	}
	return out, err
}

// LineInput è una riga da salvare in una versione in bozza.
type LineInput struct {
	CategoryID      string  `json:"cost_category_id"`
	OrgUnitID       *string `json:"org_unit_id"`
	FundingSourceID *string `json:"funding_source_id"`
	Period          *string `json:"period"` // AAAA-MM (o AAAA-MM-GG): mese dell'esercizio; vuoto = intero esercizio
	AmountCents     int64   `json:"amount_cents"`
	Note            *string `json:"note"`
}

// BudgetInput crea una nuova versione in bozza.
type BudgetInput struct {
	Element      string      `json:"element"` // chiave (DEM-12) o id
	FiscalYearID string      `json:"fiscal_year_id"`
	Kind         string      `json:"kind"` // requested (default) | revised | forecast
	Note         string      `json:"note"`
	Lines        []LineInput `json:"lines"` // per revised e forecast, vuoto = copia della versione corrente
	// AllocationID: stanziamento da cui attinge (nodo di questo elemento in un periodo dell'esercizio).
	// Una revisione o una previsione senza indicazione eredita quello della versione di partenza.
	AllocationID *string `json:"allocation_id"`
}

// CreateBudget crea una versione in bozza:
//   - requested: la proposta, finché non c'è una versione approvata;
//   - revised: la revisione della versione approvata (se non si danno righe, le copia con i
//     trasferimenti già applicati);
//   - forecast: la previsione a finire (si pubblica senza approvazione).
func (s *Service) CreateBudget(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BudgetInput) (*Budget, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	el, err := s.eng.Get(ctx, tx, actor, in.Element)
	if err != nil {
		return nil, err
	}
	var fyStatus string
	var fyStart, fyEnd time.Time
	if err := tx.QueryRow(ctx, "select status, start_date, end_date from fiscal_years where id::text = $1", in.FiscalYearID).Scan(&fyStatus, &fyStart, &fyEnd); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("fiscal_year_id", "fiscal_year", "esercizio inesistente")
		}
		return nil, err
	}
	if fyStatus == "closed" {
		return nil, engine.InvalidCode("validation.finance.fiscal_year_closed", "fiscal_year_id", "l'esercizio è chiuso")
	}
	if in.Kind == "" {
		in.Kind = "requested"
	}
	forecast := in.Kind == "forecast"
	var approvedID, forecastID *string
	if err := tx.QueryRow(ctx, `select
		(select id::text from budgets where element_id = $1 and fiscal_year_id = $2 and status = 'approved' and kind <> 'forecast'),
		(select id::text from budgets where element_id = $1 and fiscal_year_id = $2 and status = 'approved' and kind = 'forecast')`,
		el.ID, in.FiscalYearID).Scan(&approvedID, &forecastID); err != nil {
		return nil, err
	}
	var open bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from budgets where element_id = $1 and fiscal_year_id = $2 and status in ('draft','submitted')
		and (kind = 'forecast') = $3)`, el.ID, in.FiscalYearID, forecast).Scan(&open); err != nil {
		return nil, err
	}
	if open {
		return nil, engine.Duplicate("kind", "c'è già una versione in lavorazione per questo esercizio: completala o eliminala")
	}
	var basedOn *string
	switch in.Kind {
	case "requested":
		if approvedID != nil {
			return nil, engine.InvalidCode("validation.finance.budget_already_approved", "kind", "il budget è già approvato: crea una revisione")
		}
	case "revised":
		if approvedID == nil {
			return nil, engine.InvalidCode("validation.finance.budget_no_approved", "kind", "non c'è un budget approvato da rivedere")
		}
		basedOn = approvedID
	case "forecast":
		basedOn = forecastID
		if basedOn == nil {
			basedOn = approvedID
		}
	default:
		return nil, engine.InvalidValue("kind", "tipo non ammesso (requested, revised, forecast)")
	}
	var version int
	if err := tx.QueryRow(ctx, "select coalesce(max(version), 0) + 1 from budgets where element_id = $1 and fiscal_year_id = $2",
		el.ID, in.FiscalYearID).Scan(&version); err != nil {
		return nil, err
	}
	id := db.NewID()
	var allocID any
	if in.AllocationID != nil && *in.AllocationID != "" {
		if err := s.checkAllocationLink(ctx, tx, *in.AllocationID, el.ID, in.FiscalYearID); err != nil {
			return nil, err
		}
		allocID = *in.AllocationID
	} else if basedOn != nil {
		var inherited *string
		if err := tx.QueryRow(ctx, "select allocation_id from budgets where id = $1", *basedOn).Scan(&inherited); err != nil {
			return nil, err
		}
		if inherited != nil {
			allocID = *inherited
		}
	}
	if _, err := tx.Exec(ctx, `insert into budgets (tenant_id, id, element_id, fiscal_year_id, kind, version, status, note, based_on_id, created_by, allocation_id)
		values (app_tenant(), $1, $2, $3, $4, $5, 'draft', $6, $7, $8, $9)`,
		id, el.ID, in.FiscalYearID, in.Kind, version, strings.TrimSpace(in.Note), basedOn, nullable(actor.MemberID), allocID); err != nil {
		return nil, err
	}
	if len(in.Lines) == 0 && basedOn != nil {
		// Copia con i trasferimenti approvati già applicati.
		if _, err := tx.Exec(ctx, `insert into budget_lines (tenant_id, id, budget_id, cost_category_id, org_unit_id, funding_source_id, period, amount_cents, currency, note)
			select l.tenant_id, gen_random_uuid(), $2, l.cost_category_id, l.org_unit_id, l.funding_source_id, l.period,
			       greatest(l.amount_cents
			         + coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = l.id and t.status = 'approved'), 0)
			         - coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status = 'approved'), 0), 0),
			       l.currency, l.note
			from budget_lines l where l.budget_id = $1`, *basedOn, id); err != nil {
			return nil, err
		}
	} else if err := s.writeLines(ctx, tx, id, fyStart, fyEnd, in.Lines); err != nil {
		return nil, err
	}
	b, err := s.Budget(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	return b, audit.Append(ctx, tx, audit.Event{Action: "budget.create", EntityType: "budget", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": el.ID, "fiscal_year_id": in.FiscalYearID,
			"kind": in.Kind, "version": version, "based_on": basedOn, "total_cents": b.TotalCents}})
}

func (s *Service) writeLines(ctx context.Context, tx pgx.Tx, budgetID string, fyStart, fyEnd time.Time, in []LineInput) error {
	if len(in) > 500 {
		return engine.InvalidCode("validation.finance.budget_too_many_lines", "lines", "al massimo 500 righe per versione")
	}
	base, err := BaseCurrency(ctx, tx)
	if err != nil {
		return err
	}
	for i, l := range in {
		field := fmt.Sprintf("lines[%d]", i)
		if l.AmountCents < 0 {
			return engine.InvalidCode("validation.not_negative", field, "l'importo non può essere negativo")
		}
		var active bool
		if err := tx.QueryRow(ctx, "select active from cost_categories where id::text = $1", l.CategoryID).Scan(&active); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return engine.NotFound(field, "", "categoria di costo inesistente")
			}
			return err
		}
		if !active {
			return engine.InvalidCode("validation.finance.cost_category_inactive", field, "categoria di costo non più attiva")
		}
		var period *time.Time
		if l.Period != nil && *l.Period != "" {
			v := *l.Period
			if len(v) == 7 {
				v += "-01"
			}
			p, err := parseDate(field, v)
			if err != nil {
				return err
			}
			p = monthStart(p)
			if p.Before(monthStart(fyStart)) || p.After(fyEnd) {
				return engine.InvalidCode("validation.finance.period_outside_year", field, "il periodo è fuori dall'esercizio")
			}
			period = &p
		}
		if _, err := tx.Exec(ctx, `insert into budget_lines (tenant_id, id, budget_id, cost_category_id, org_unit_id, funding_source_id, period, amount_cents, currency, note)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			db.NewID(), budgetID, l.CategoryID, nullablePtr(l.OrgUnitID), nullablePtr(l.FundingSourceID), period, l.AmountCents, base, nullablePtr(l.Note)); err != nil {
			if strings.Contains(err.Error(), "23503") {
				return engine.NotFound(field, "", "centro di costo o fonte di finanziamento inesistente")
			}
			return err
		}
	}
	return nil
}

// draft blocca una versione in bozza per modificarla.
func (s *Service) draft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Budget, time.Time, time.Time, error) {
	if err := requireWrite(actor); err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	var status string
	var fyStart, fyEnd time.Time
	err := tx.QueryRow(ctx, `select b.status, f.start_date, f.end_date from budgets b join fiscal_years f on f.tenant_id = b.tenant_id and f.id = b.fiscal_year_id
		where b.id::text = $1 for update of b`, id).Scan(&status, &fyStart, &fyEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fyStart, fyEnd, engine.ErrNotFound
	}
	if err != nil {
		return nil, fyStart, fyEnd, err
	}
	b, err := s.Budget(ctx, tx, actor, id)
	if err != nil {
		return nil, fyStart, fyEnd, err
	}
	if status != "draft" {
		return nil, fyStart, fyEnd, engine.InvalidCode("validation.finance.budget_only_draft_editable", "status", "si modificano solo le versioni in bozza: le approvate si rivedono con una nuova versione")
	}
	return b, fyStart, fyEnd, nil
}

// SaveLines sostituisce le righe (e la nota) di una versione in bozza.
func (s *Service) SaveLines(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, note *string, in []LineInput) (*Budget, error) {
	b, fyStart, fyEnd, err := s.draft(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "delete from budget_lines where budget_id = $1", b.ID); err != nil {
		return nil, err
	}
	if err := s.writeLines(ctx, tx, b.ID, fyStart, fyEnd, in); err != nil {
		return nil, err
	}
	if note != nil {
		if _, err := tx.Exec(ctx, "update budgets set note = $2 where id = $1", b.ID, strings.TrimSpace(*note)); err != nil {
			return nil, err
		}
	}
	out, err := s.Budget(ctx, tx, actor, b.ID)
	if err != nil {
		return nil, err
	}
	return out, audit.Append(ctx, tx, audit.Event{Action: "budget.lines", EntityType: "budget", EntityID: b.ID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"lines": len(in), "total_cents": out.TotalCents}})
}

// DeleteDraft elimina una versione in bozza.
func (s *Service) DeleteDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	b, _, _, err := s.draft(ctx, tx, actor, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from budgets where id = $1", b.ID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "budget.delete", EntityType: "budget", EntityID: b.ID, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": b.ElementID, "version": b.Version}})
}

// Submit manda in approvazione una versione in bozza con il motore di approvazione
// (politica "@budget", soglie sul totale). Senza politica, o con una regola senza passi,
// la versione è approvata subito da chi la invia (che ha finance.write). La previsione a
// finire non si approva: si pubblica.
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Budget, error) {
	b, _, _, err := s.draft(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if len(b.Lines) == 0 {
		return nil, engine.InvalidCode("validation.finance.budget_no_lines", "lines", "la versione non ha righe")
	}
	if b.Kind == "revised" && b.BasedOnID != nil {
		var pending bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from budget_transfers t join budget_lines l on l.tenant_id = t.tenant_id
			and (l.id = t.from_line_id or l.id = t.to_line_id) where l.budget_id = $1 and t.status = 'pending')`, *b.BasedOnID).Scan(&pending); err != nil {
			return nil, err
		}
		if pending {
			return nil, engine.InvalidCode("validation.finance.budget_pending_transfers", "status", "la versione approvata ha trasferimenti in attesa: decidili prima della revisione")
		}
	}
	if b.AllocationID != nil && b.Kind != "forecast" {
		// Meglio saperlo all'invio che all'approvazione: il budget deve stare nello stanziamento.
		if err := s.commitCheckWithLines(ctx, tx, b); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, "update budgets set submitted_at = now(), decision_note = null where id = $1", b.ID); err != nil {
		return nil, err
	}
	if b.Kind != "forecast" {
		el, err := s.eng.Get(ctx, tx, actor, b.ElementID)
		if err != nil {
			return nil, err
		}
		label := fmt.Sprintf("Budget %s · v%d", b.FiscalYear, b.Version)
		reqID, err := s.ap.OpenSubject(ctx, tx, actor, el, subjectBudget, PolicyBudget, label, b.TotalCents)
		if err != nil {
			return nil, err
		}
		if reqID != "" {
			if _, err := tx.Exec(ctx, "update budgets set status = 'submitted', approval_request_id = $2 where id = $1", b.ID, reqID); err != nil {
				return nil, err
			}
			if err := audit.Append(ctx, tx, audit.Event{Action: "budget.submit", EntityType: "budget", EntityID: b.ID, ActorAccountID: actor.AccountID,
				ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"approval_request_id": reqID, "total_cents": b.TotalCents}}); err != nil {
				return nil, err
			}
			return s.Budget(ctx, tx, actor, b.ID)
		}
	}
	if err := s.approve(ctx, tx, actor, b.ID); err != nil {
		return nil, err
	}
	return s.Budget(ctx, tx, actor, b.ID)
}

// approve rende corrente la versione: la precedente approvata (dello stesso genere)
// diventa superseded. Poi aggiorna la graduatoria se l'esercizio la alimenta.
func (s *Service) approve(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	var elementID, fyID, kind string
	var total int64
	if err := tx.QueryRow(ctx, `select element_id, fiscal_year_id, kind, coalesce((select sum(amount_cents) from budget_lines where budget_id = b.id), 0)
		from budgets b where id = $1`, id).Scan(&elementID, &fyID, &kind, &total); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update budgets set status = 'superseded' where element_id = $1 and fiscal_year_id = $2 and status = 'approved'
		and (kind = 'forecast') = ($3 = 'forecast') and id <> $4`, elementID, fyID, kind, id); err != nil {
		return err
	}
	// L'approvazione impegna lo stanziamento da cui il budget attinge.
	var allocID *string
	if err := tx.QueryRow(ctx, "select allocation_id from budgets where id = $1", id).Scan(&allocID); err != nil {
		return err
	}
	if allocID != nil && kind != "forecast" {
		if err := s.commitCheck(ctx, tx, id, *allocID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update budgets set status = 'approved', approved_by = $2, approved_at = now() where id = $1`, id, nullable(actor.MemberID)); err != nil {
		return err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "budget.approve", EntityType: "budget", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": elementID, "kind": kind, "total_cents": total}}); err != nil {
		return err
	}
	if kind != "forecast" {
		if err := s.syncPortfolio(ctx, tx, actor, fyID); err != nil {
			return err
		}
	}
	return s.changed(ctx, tx, actor, elementID)
}

// closeBudget applica l'esito dell'approvazione: approvata → versione corrente;
// respinta o rinviata → torna in bozza con la motivazione.
func (s *Service) closeBudget(ctx context.Context, tx pgx.Tx, actor engine.Actor, reqID, outcome, comment string) error {
	var id string
	err := tx.QueryRow(ctx, "select id from budgets where approval_request_id = $1 and status = 'submitted' for update", reqID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // la versione è stata eliminata o già decisa
	}
	if err != nil {
		return err
	}
	switch outcome {
	case "approved", "approved_with_conditions":
		return s.approve(ctx, tx, actor, id)
	}
	if _, err := tx.Exec(ctx, "update budgets set status = 'draft', decision_note = $2 where id = $1", id, nullable(comment)); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "budget.returned", EntityType: "budget", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"outcome": outcome, "approval_request_id": reqID}})
}

// ---------------------------------------------------------------------
// Trasferimenti
// ---------------------------------------------------------------------

type Transfer struct {
	ID           string     `json:"id"`
	FromLineID   string     `json:"from_line_id"`
	ToLineID     string     `json:"to_line_id"`
	FromElement  string     `json:"from_element"`
	ToElement    string     `json:"to_element"`
	FromCategory string     `json:"from_category"`
	ToCategory   string     `json:"to_category"`
	FiscalYear   string     `json:"fiscal_year"`
	AmountCents  int64      `json:"amount_cents"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"` // pending | approved | rejected
	ApprovalID   *string    `json:"approval_request_id"`
	CreatedBy    *string    `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedBy    *string    `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote *string    `json:"decision_note"`
}

func (s *Service) Transfers(ctx context.Context, tx pgx.Tx, fyID string) ([]Transfer, error) {
	rows, err := tx.Query(ctx, `
		select t.id, t.from_line_id, t.to_line_id, coalesce(fs.key || '-' || fe.number, ''), coalesce(ts.key || '-' || te.number, ''),
		       fc.name, tc.name, fy.name, t.amount_cents, t.reason, t.status, t.approval_request_id, cm.display_name, t.created_at,
		       dm.display_name, t.decided_at, t.decision_note
		from budget_transfers t
		join budget_lines fl on fl.tenant_id = t.tenant_id and fl.id = t.from_line_id
		join budget_lines tl on tl.tenant_id = t.tenant_id and tl.id = t.to_line_id
		join budgets fb on fb.tenant_id = t.tenant_id and fb.id = fl.budget_id
		join budgets tb on tb.tenant_id = t.tenant_id and tb.id = tl.budget_id
		join elements fe on fe.tenant_id = t.tenant_id and fe.id = fb.element_id
		join spaces fs on fs.tenant_id = t.tenant_id and fs.id = fe.space_id
		join elements te on te.tenant_id = t.tenant_id and te.id = tb.element_id
		join spaces ts on ts.tenant_id = t.tenant_id and ts.id = te.space_id
		join cost_categories fc on fc.tenant_id = t.tenant_id and fc.id = fl.cost_category_id
		join cost_categories tc on tc.tenant_id = t.tenant_id and tc.id = tl.cost_category_id
		join fiscal_years fy on fy.tenant_id = t.tenant_id and fy.id = fb.fiscal_year_id
		left join members cm on cm.tenant_id = t.tenant_id and cm.id = t.created_by
		left join members dm on dm.tenant_id = t.tenant_id and dm.id = t.decided_by
		where $1 = '' or fb.fiscal_year_id::text = $1
		order by t.created_at desc limit 500`, fyID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Transfer])
	if out == nil {
		out = []Transfer{}
	}
	return out, err
}

type TransferInput struct {
	FromLineID  string `json:"from_line_id"`
	ToLineID    string `json:"to_line_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
}

// lineInfo: la riga deve stare in una versione approvata corrente (non una previsione).
type lineInfo struct {
	elementID, fyID, status, kind string
	available                     int64 // effettivo meno i trasferimenti in uscita in attesa
}

func lineOf(ctx context.Context, tx pgx.Tx, id string) (*lineInfo, error) {
	var li lineInfo
	err := tx.QueryRow(ctx, `select b.element_id, b.fiscal_year_id, b.status, b.kind,
		l.amount_cents
		  + coalesce((select sum(t.amount_cents) from budget_transfers t where t.to_line_id = l.id and t.status = 'approved'), 0)
		  - coalesce((select sum(t.amount_cents) from budget_transfers t where t.from_line_id = l.id and t.status in ('approved','pending')), 0)
		from budget_lines l join budgets b on b.tenant_id = l.tenant_id and b.id = l.budget_id where l.id::text = $1 for update of l`, id).
		Scan(&li.elementID, &li.fyID, &li.status, &li.kind, &li.available)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &li, err
}

// CreateTransfer sposta budget da una riga a un'altra (anche di un altro progetto) dello
// stesso esercizio, con motivazione. Secondo la politica "@budget_transfer" il
// trasferimento è applicato subito o va in approvazione (PMO oltre i 10 k€ di default).
func (s *Service) CreateTransfer(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TransferInput) (*Transfer, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, engine.Required("reason", "serve una motivazione")
	}
	if in.AmountCents <= 0 {
		return nil, engine.InvalidCode("validation.finance.amount_not_positive", "amount_cents", "l'importo deve essere positivo")
	}
	if in.FromLineID == in.ToLineID {
		return nil, engine.InvalidCode("validation.finance.transfer_line_same", "to_line_id", "la riga di destinazione deve essere diversa")
	}
	from, err := lineOf(ctx, tx, in.FromLineID)
	if err != nil {
		return nil, err
	}
	to, err := lineOf(ctx, tx, in.ToLineID)
	if err != nil {
		return nil, err
	}
	if from == nil || to == nil {
		return nil, engine.NotFound("from_line_id", "", "riga di budget inesistente")
	}
	if from.status != "approved" || to.status != "approved" || from.kind == "forecast" || to.kind == "forecast" {
		return nil, engine.InvalidCode("validation.finance.transfer_only_approved_lines", "from_line_id", "si trasferisce solo tra righe di budget approvati correnti")
	}
	if from.fyID != to.fyID {
		return nil, engine.InvalidCode("validation.finance.transfer_lines_same_year", "to_line_id", "le righe devono essere dello stesso esercizio")
	}
	if in.AmountCents > from.available {
		return nil, engine.InvalidParams("validation.finance.transfer_insufficient", "amount_cents", map[string]any{"available": money(from.available)}, "disponibilità insufficiente sulla riga di partenza (%s)", money(from.available))
	}
	el, err := s.eng.Get(ctx, tx, actor, from.elementID)
	if err != nil {
		return nil, err
	}
	if _, err := s.eng.Get(ctx, tx, actor, to.elementID); err != nil {
		return nil, err
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into budget_transfers (tenant_id, id, from_line_id, to_line_id, amount_cents, reason, status, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, 'pending', $6)`, id, in.FromLineID, in.ToLineID, in.AmountCents, in.Reason, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	reqID, err := s.ap.OpenSubject(ctx, tx, actor, el, subjectTrans, PolicyTransfer, "Trasferimento di budget", in.AmountCents)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(in.Reason))
	payload := map[string]any{"from_line_id": in.FromLineID, "to_line_id": in.ToLineID, "amount_cents": in.AmountCents,
		"from_element_id": from.elementID, "to_element_id": to.elementID, "reason_sha256": hex.EncodeToString(sum[:])}
	if reqID != "" {
		payload["approval_request_id"] = reqID
		if _, err := tx.Exec(ctx, "update budget_transfers set approval_request_id = $2 where id = $1", id, reqID); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "budget.transfer", EntityType: "budget_transfer", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload}); err != nil {
		return nil, err
	}
	if reqID == "" {
		if err := s.decideTransfer(ctx, tx, actor, id, "approved", ""); err != nil {
			return nil, err
		}
	}
	return s.transfer(ctx, tx, id)
}

func (s *Service) transfer(ctx context.Context, tx pgx.Tx, id string) (*Transfer, error) {
	all, err := s.Transfers(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Service) decideTransfer(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, outcome, note string) error {
	status := "rejected"
	if outcome == "approved" || outcome == "approved_with_conditions" {
		status = "approved"
	}
	if _, err := tx.Exec(ctx, `update budget_transfers set status = $2, decided_by = $3, decided_at = now(), decision_note = $4 where id = $1`,
		id, status, nullable(actor.MemberID), nullable(note)); err != nil {
		return err
	}
	if status == "approved" {
		var elementID string
		if err := tx.QueryRow(ctx, `select b.element_id::text from budget_transfers t
			join budget_lines l on l.tenant_id = t.tenant_id and l.id = coalesce(t.to_line_id, t.from_line_id)
			join budgets b on b.tenant_id = l.tenant_id and b.id = l.budget_id where t.id = $1`, id).Scan(&elementID); err == nil {
			if err := s.changed(ctx, tx, actor, elementID); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return audit.Append(ctx, tx, audit.Event{Action: "budget.transfer." + status, EntityType: "budget_transfer", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"outcome": outcome}})
}

func (s *Service) closeTransfer(ctx context.Context, tx pgx.Tx, actor engine.Actor, reqID, outcome, comment string) error {
	var id string
	err := tx.QueryRow(ctx, "select id from budget_transfers where approval_request_id = $1 and status = 'pending' for update", reqID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Non è un trasferimento tra righe di budget: può esserlo tra stanziamenti.
		_, err := s.closeAllocTransfer(ctx, tx, actor, reqID, outcome, comment)
		return err
	}
	if err != nil {
		return err
	}
	return s.decideTransfer(ctx, tx, actor, id, outcome, comment)
}

// money formatta centesimi per i messaggi (1.234,56).
func money(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	units := fmt.Sprint(c / 100)
	var b strings.Builder
	for i, r := range units {
		if i > 0 && (len(units)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s,%02d", b.String(), c%100)
	if neg {
		return "-" + out
	}
	return out
}

// commitCheckWithLines è commitCheck per una versione già caricata.
func (s *Service) commitCheckWithLines(ctx context.Context, tx pgx.Tx, b *Budget) error {
	return s.commitCheck(ctx, tx, b.ID, *b.AllocationID)
}
