package finance

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Tutte le funzioni di questo file lavorano su tabelle riservate (rates, cost_entries):
// la transazione deve avere app.finance = 'on' (db.TenantOpts{Finance: true}).

// ---------------------------------------------------------------------
// Voci di costo
// ---------------------------------------------------------------------

type CostEntry struct {
	ID              string    `json:"id"`
	ElementID       string    `json:"element_id"`
	ElementKey      string    `json:"element_key"`
	ElementTitle    string    `json:"element_title"`
	CategoryID      string    `json:"cost_category_id"`
	Category        string    `json:"category"`
	Nature          string    `json:"nature"`
	OrgUnit         *string   `json:"org_unit"`
	Source          string    `json:"source"`
	SourceRef       *string   `json:"source_ref"`
	EntryDate       string    `json:"entry_date"`
	Kind            string    `json:"kind"` // committed | actual
	AmountCents     int64     `json:"amount_cents"`
	Currency        string    `json:"currency"`
	AmountBaseCents int64     `json:"amount_base_cents"`
	FxRate          float64   `json:"fx_rate"`
	Capitalizable   bool      `json:"capitalizable"`
	Description     string    `json:"description"`
	Supplier        *string   `json:"supplier"`
	Member          *string   `json:"member"`       // persona delle ore (solo voci da work_log)
	Hours           *float64  `json:"hours"`        // ore della voce da work_log
	HourlyCents     *int64    `json:"hourly_cents"` // tariffa applicata
	ImportRunID     *string   `json:"import_run_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// CostFilter seleziona le voci; Element include impatti e figli.
type CostFilter struct {
	Element    string
	CategoryID string
	Kind       string
	Source     string
	From, To   string
	Text       string
	Cursor     string
	Limit      int
	id         string // una sola voce (uso interno)
}

type CostPage struct {
	Items     []CostEntry `json:"items"`
	Next      string      `json:"next,omitempty"`
	TotalBase int64       `json:"total_base_cents"` // somma della selezione (tutte le pagine)
	Count     int64       `json:"count"`
	Actual    int64       `json:"actual_base_cents"`
	Committed int64       `json:"committed_base_cents"`
}

func (s *Service) Costs(ctx context.Context, tx pgx.Tx, actor engine.Actor, f CostFilter) (*CostPage, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	var elementID string
	if f.Element != "" {
		el, err := s.eng.Get(ctx, tx, actor, f.Element)
		if err != nil {
			return nil, err
		}
		elementID = el.ID
	}
	var from, to *time.Time
	for _, p := range []struct {
		v   string
		dst **time.Time
		k   string
	}{{f.From, &from, "from"}, {f.To, &to, "to"}} {
		if p.v != "" {
			d, err := parseDate(p.k, p.v)
			if err != nil {
				return nil, err
			}
			*p.dst = &d
		}
	}
	var curDate *time.Time
	var curID *string
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		parts := strings.SplitN(string(raw), "|", 2)
		if err != nil || len(parts) != 2 {
			return nil, engine.InvalidValue("cursor", "cursore non valido")
		}
		d, err := time.Parse(time.DateOnly, parts[0])
		if err != nil {
			return nil, engine.InvalidValue("cursor", "cursore non valido")
		}
		curDate, curID = &d, &parts[1]
	}
	where := `($1::uuid is null or c.element_id in (select id from finance_family($1::uuid)))
		and ($2 = '' or c.cost_category_id::text = $2) and ($3 = '' or c.kind = $3) and ($4 = '' or c.source = $4)
		and ($5::date is null or c.entry_date >= $5) and ($6::date is null or c.entry_date <= $6)
		and ($7 = '' or c.description ilike '%' || $7 || '%' or c.supplier ilike '%' || $7 || '%' or c.source_ref ilike '%' || $7 || '%')
		and ($8 = '' or c.id::text = $8)`
	args := []any{nullable(elementID), f.CategoryID, f.Kind, f.Source, from, to, f.Text, f.id}
	page := &CostPage{}
	if err := tx.QueryRow(ctx, `select count(*), coalesce(sum(amount_base_cents), 0),
		coalesce(sum(amount_base_cents) filter (where kind = 'actual'), 0), coalesce(sum(amount_base_cents) filter (where kind = 'committed'), 0)
		from cost_entries c where `+where, args...).Scan(&page.Count, &page.TotalBase, &page.Actual, &page.Committed); err != nil {
		return nil, err
	}
	args = append(args, curDate, curID, f.Limit+1)
	rows, err := tx.Query(ctx, `
		select c.id, c.element_id, coalesce(s.key || '-' || e.number, ''), e.title, c.cost_category_id, cc.name, cc.nature, u.name,
		       c.source, c.source_ref, c.entry_date, c.kind, c.amount_cents, c.currency, c.amount_base_cents, c.fx_rate::float8,
		       c.capitalizable, c.description, c.supplier, m.display_name, round(w.minutes / 60.0, 2)::float8, r.hourly_cents,
		       c.import_run_id, c.created_at
		from cost_entries c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join cost_categories cc on cc.tenant_id = c.tenant_id and cc.id = c.cost_category_id
		left join org_units u on u.tenant_id = c.tenant_id and u.id = c.org_unit_id
		left join work_logs w on w.tenant_id = c.tenant_id and w.id = c.work_log_id
		left join members m on m.tenant_id = w.tenant_id and m.id = w.member_id
		left join rates r on r.tenant_id = c.tenant_id and r.id = c.rate_id
		where `+where+` and ($9::date is null or (c.entry_date, c.id) < ($9, $10::uuid))
		order by c.entry_date desc, c.id desc limit $11`, args...)
	if err != nil {
		return nil, err
	}
	page.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CostEntry, error) {
		var c CostEntry
		var d time.Time
		err := r.Scan(&c.ID, &c.ElementID, &c.ElementKey, &c.ElementTitle, &c.CategoryID, &c.Category, &c.Nature, &c.OrgUnit,
			&c.Source, &c.SourceRef, &d, &c.Kind, &c.AmountCents, &c.Currency, &c.AmountBaseCents, &c.FxRate,
			&c.Capitalizable, &c.Description, &c.Supplier, &c.Member, &c.Hours, &c.HourlyCents, &c.ImportRunID, &c.CreatedAt)
		c.EntryDate = d.Format(time.DateOnly)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	if len(page.Items) > f.Limit {
		last := page.Items[f.Limit-1]
		page.Items = page.Items[:f.Limit]
		page.Next = base64.RawURLEncoding.EncodeToString([]byte(last.EntryDate + "|" + last.ID))
	}
	if page.Items == nil {
		page.Items = []CostEntry{}
	}
	return page, nil
}

// CostInput è una voce inserita a mano (fattura, ordine, costo ricorrente, altro).
type CostInput struct {
	Element       string  `json:"element"`
	CategoryID    string  `json:"cost_category_id"`
	OrgUnitID     *string `json:"org_unit_id"`
	Source        string  `json:"source"` // manual (default) | invoice | purchase_order | recurring
	SourceRef     *string `json:"source_ref"`
	EntryDate     string  `json:"entry_date"`
	Kind          string  `json:"kind"` // actual (default) | committed
	AmountCents   int64   `json:"amount_cents"`
	Currency      string  `json:"currency"`
	Capitalizable bool    `json:"capitalizable"`
	Description   string  `json:"description"`
	Supplier      *string `json:"supplier"`
}

func (s *Service) CreateCost(ctx context.Context, tx pgx.Tx, actor engine.Actor, in CostInput) (*CostEntry, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	el, err := s.eng.Get(ctx, tx, actor, in.Element)
	if err != nil {
		return nil, err
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	switch in.Source {
	case "manual", "invoice", "purchase_order", "recurring":
	default:
		return nil, engine.InvalidValue("source", "origine non ammessa (manual, invoice, purchase_order, recurring)")
	}
	if in.Kind == "" {
		in.Kind = "actual"
	}
	if in.Kind != "actual" && in.Kind != "committed" {
		return nil, engine.InvalidValue("kind", "tipo non ammesso (actual, committed)")
	}
	if in.AmountCents == 0 {
		return nil, engine.Required("amount_cents", "serve un importo")
	}
	if err := exists(ctx, tx, "cost_categories", in.CategoryID, "cost_category_id"); err != nil {
		return nil, err
	}
	d, err := parseDate("entry_date", in.EntryDate)
	if err != nil {
		return nil, err
	}
	base, err := BaseCurrency(ctx, tx)
	if err != nil {
		return nil, err
	}
	cur, err := normCurrency(in.Currency, base)
	if err != nil {
		return nil, err
	}
	amountBase, rate, err := convert(ctx, tx, in.AmountCents, cur, base, d)
	if errors.Is(err, ErrNoRate) {
		return nil, engine.InvalidParams("validation.finance.exchange_rate_missing", "currency", map[string]any{"detail": err.Error()}, "%v: inserisci il cambio", err)
	}
	if err != nil {
		return nil, err
	}
	ref := nullablePtr(in.SourceRef)
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into cost_entries (tenant_id, id, element_id, cost_category_id, org_unit_id, source, source_ref, entry_date, kind,
		amount_cents, currency, amount_base_cents, fx_rate, capitalizable, description, supplier, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::numeric, $13, $14, $15, $16)`,
		id, el.ID, in.CategoryID, nullablePtr(in.OrgUnitID), in.Source, ref, d, in.Kind, in.AmountCents, cur, amountBase, rate,
		in.Capitalizable, strings.TrimSpace(in.Description), nullablePtr(in.Supplier), nullable(actor.MemberID)); err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("source_ref", "esiste già una voce con questo riferimento")
		}
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "cost.create", EntityType: "cost_entry", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": el.ID, "source": in.Source, "kind": in.Kind,
			"amount_cents": in.AmountCents, "currency": cur, "amount_base_cents": amountBase}}); err != nil {
		return nil, err
	}
	return s.cost(ctx, tx, actor, id)
}

func (s *Service) cost(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*CostEntry, error) {
	page, err := s.Costs(ctx, tx, actor, CostFilter{id: id, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, engine.ErrNotFound
	}
	return &page.Items[0], nil
}

// DeleteCost elimina una voce inserita a mano o importata (quelle da ore si tolgono
// eliminando le ore).
func (s *Service) DeleteCost(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireWrite(actor); err != nil {
		return err
	}
	var source, elementID string
	var amount int64
	err := tx.QueryRow(ctx, "delete from cost_entries where id::text = $1 and source <> 'work_log' returning source, element_id, amount_base_cents", id).
		Scan(&source, &elementID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "cost.delete", EntityType: "cost_entry", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": elementID, "source": source, "amount_base_cents": amount}})
}

// ---------------------------------------------------------------------
// Tariffe
// ---------------------------------------------------------------------

type Rate struct {
	ID          string  `json:"id"`
	MemberID    *string `json:"member_id"`
	Member      *string `json:"member"`
	RoleKey     *string `json:"role_key"`
	HourlyCents int64   `json:"hourly_cents"`
	Currency    string  `json:"currency"`
	ValidFrom   string  `json:"valid_from"`
	ValidTo     *string `json:"valid_to"`
	Note        string  `json:"note"`
	Uses        int64   `json:"uses"` // voci di costo calcolate con questa tariffa
}

func (s *Service) Rates(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Rate, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select r.id, r.member_id, m.display_name, r.role_key, r.hourly_cents, r.currency, r.valid_from, r.valid_to, r.note,
		(select count(*) from cost_entries c where c.rate_id = r.id)
		from rates r left join members m on m.tenant_id = r.tenant_id and m.id = r.member_id
		where r.provider_element_id is null
		order by r.member_id is not null, coalesce(r.role_key, m.display_name), r.valid_from desc`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rate, error) {
		var x Rate
		var from time.Time
		var to *time.Time
		err := r.Scan(&x.ID, &x.MemberID, &x.Member, &x.RoleKey, &x.HourlyCents, &x.Currency, &from, &to, &x.Note, &x.Uses)
		x.ValidFrom, x.ValidTo = from.Format(time.DateOnly), dateStr(to)
		return x, err
	})
	if out == nil {
		out = []Rate{}
	}
	return out, err
}

type RateInput struct {
	MemberID    *string `json:"member_id"`
	RoleKey     *string `json:"role_key"`
	HourlyCents int64   `json:"hourly_cents"`
	Currency    string  `json:"currency"`
	ValidFrom   string  `json:"valid_from"`
	ValidTo     *string `json:"valid_to"`
	Note        string  `json:"note"`
}

func normRole(r *string) *string {
	if r == nil {
		return nil
	}
	v := strings.ToLower(strings.TrimSpace(*r))
	if v == "" {
		return nil
	}
	return &v
}

// SaveRate crea (id vuoto) o modifica una tariffa per persona o per ruolo. Le validità
// della stessa persona (o dello stesso ruolo) non si sovrappongono. Dopo la modifica le
// ore interessate si ricalcolano.
func (s *Service) SaveRate(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in RateInput) (*Rate, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	in.RoleKey = normRole(in.RoleKey)
	if in.MemberID != nil && *in.MemberID == "" {
		in.MemberID = nil
	}
	if (in.MemberID == nil) == (in.RoleKey == nil) {
		return nil, engine.Required("member_id", "indica una persona oppure un ruolo")
	}
	if in.HourlyCents <= 0 {
		return nil, engine.InvalidCode("validation.finance.hourly_not_positive", "hourly_cents", "la tariffa oraria deve essere positiva")
	}
	base, err := BaseCurrency(ctx, tx)
	if err != nil {
		return nil, err
	}
	cur, err := normCurrency(in.Currency, base)
	if err != nil {
		return nil, err
	}
	from, err := parseDate("valid_from", in.ValidFrom)
	if err != nil {
		return nil, err
	}
	var to *time.Time
	if in.ValidTo != nil && *in.ValidTo != "" {
		t, err := parseDate("valid_to", *in.ValidTo)
		if err != nil {
			return nil, err
		}
		if t.Before(from) {
			return nil, engine.InvalidCode("validation.finance.valid_range_inverted", "valid_to", "la fine della validità precede l'inizio")
		}
		to = &t
	}
	if in.MemberID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1)", *in.MemberID).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.NotFound("member_id", "member", "persona inesistente")
		}
	}
	var overlap bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from rates where id::text <> $1
		and member_id is not distinct from $2::uuid and role_key is not distinct from $3 and provider_element_id is null
		and valid_from <= coalesce($5::date, 'infinity') and coalesce(valid_to, 'infinity') >= $4)`,
		id, in.MemberID, in.RoleKey, from, to).Scan(&overlap); err != nil {
		return nil, err
	}
	if overlap {
		return nil, engine.InvalidCode("validation.finance.rate_overlap", "valid_from", "la validità si sovrappone a un'altra tariffa dello stesso soggetto: chiudi prima quella")
	}
	action := "rate.update"
	var prevMember, prevRole *string
	if id == "" {
		id, action = db.NewID(), "rate.create"
		_, err = tx.Exec(ctx, `insert into rates (tenant_id, id, member_id, role_key, hourly_cents, currency, valid_from, valid_to, note, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, in.MemberID, in.RoleKey, in.HourlyCents, cur, from, to, strings.TrimSpace(in.Note), nullable(actor.MemberID))
	} else {
		err = tx.QueryRow(ctx, `update rates r set member_id = $2, role_key = $3, hourly_cents = $4, currency = $5, valid_from = $6, valid_to = $7, note = $8
			from rates old where r.id = $1 and old.tenant_id = r.tenant_id and old.id = r.id returning old.member_id::text, old.role_key`,
			id, in.MemberID, in.RoleKey, in.HourlyCents, cur, from, to, strings.TrimSpace(in.Note)).Scan(&prevMember, &prevRole)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	// Nell'audit niente importi delle tariffe per persona: solo identificativi e validità.
	if err := audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "rate", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"member_id": in.MemberID, "role_key": in.RoleKey,
			"valid_from": from.Format(time.DateOnly), "valid_to": dateStr(to)}}); err != nil {
		return nil, err
	}
	if _, err := s.recompute(ctx, tx, actor, workLogScope{memberIDs: compact(in.MemberID, prevMember), roles: compact(in.RoleKey, prevRole)}); err != nil {
		return nil, err
	}
	all, err := s.Rates(ctx, tx, actor)
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

func compact(vs ...*string) []string {
	var out []string
	for _, v := range vs {
		if v != nil && *v != "" {
			out = append(out, *v)
		}
	}
	return out
}

// DeleteRate elimina una tariffa e ricalcola le ore che la usavano (con un'altra tariffa
// valida, oppure senza costo).
func (s *Service) DeleteRate(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireWrite(actor); err != nil {
		return err
	}
	var member, role *string
	err := tx.QueryRow(ctx, "select member_id::text, role_key from rates where id::text = $1", id).Scan(&member, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from cost_entries where rate_id = $1 and source = 'work_log'", id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from rates where id = $1", id); err != nil {
		if strings.Contains(err.Error(), "23503") {
			return engine.InvalidCode("validation.finance.rate_in_use", "id", "la tariffa è usata da voci non calcolate dalle ore")
		}
		return err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "rate.delete", EntityType: "rate", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"member_id": member, "role_key": role}}); err != nil {
		return err
	}
	_, err = s.recompute(ctx, tx, actor, workLogScope{memberIDs: compact(member), roles: compact(role)})
	return err
}

// ---------------------------------------------------------------------
// Ore registrate e costi calcolati
// ---------------------------------------------------------------------

type WorkLog struct {
	ID            string   `json:"id"`
	ElementID     string   `json:"element_id"`
	ElementKey    string   `json:"element_key"`
	MemberID      string   `json:"member_id"`
	Member        string   `json:"member"`
	WorkDate      string   `json:"work_date"`
	Hours         float64  `json:"hours"`
	RoleKey       *string  `json:"role_key"`
	Capitalizable bool     `json:"capitalizable"`
	Text          *string  `json:"text"`
	CostCents     *int64   `json:"cost_base_cents"` // nil = nessuna tariffa valida
	RateKind      *string  `json:"rate_kind"`       // member | role
	HourlyCents   *int64   `json:"hourly_cents"`
	RateCurrency  *string  `json:"rate_currency"`
	FxRate        *float64 `json:"fx_rate"`
}

type WorkLogFilter struct {
	Element  string
	MemberID string
	From, To string
}

func (s *Service) WorkLogs(ctx context.Context, tx pgx.Tx, actor engine.Actor, f WorkLogFilter) ([]WorkLog, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	var elementID string
	if f.Element != "" {
		el, err := s.eng.Get(ctx, tx, actor, f.Element)
		if err != nil {
			return nil, err
		}
		elementID = el.ID
	}
	var from, to *time.Time
	if f.From != "" {
		d, err := parseDate("from", f.From)
		if err != nil {
			return nil, err
		}
		from = &d
	}
	if f.To != "" {
		d, err := parseDate("to", f.To)
		if err != nil {
			return nil, err
		}
		to = &d
	}
	rows, err := tx.Query(ctx, `
		select w.id, w.element_id, coalesce(s.key || '-' || e.number, ''), w.member_id, m.display_name, w.work_date,
		       round(w.minutes / 60.0, 2)::float8, w.role_key, w.capitalizable, w.text, c.amount_base_cents,
		       case when r.member_id is not null then 'member' when r.id is not null then 'role' end, r.hourly_cents, r.currency, c.fx_rate::float8
		from work_logs w
		join elements e on e.tenant_id = w.tenant_id and e.id = w.element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join members m on m.tenant_id = w.tenant_id and m.id = w.member_id
		left join cost_entries c on c.tenant_id = w.tenant_id and c.work_log_id = w.id
		left join rates r on r.tenant_id = c.tenant_id and r.id = c.rate_id
		where ($1::uuid is null or w.element_id in (select id from finance_family($1::uuid))) and ($2 = '' or w.member_id::text = $2)
		  and ($3::date is null or w.work_date >= $3) and ($4::date is null or w.work_date <= $4)
		order by w.work_date desc, w.created_at desc limit 500`, nullable(elementID), f.MemberID, from, to)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WorkLog, error) {
		var w WorkLog
		var d time.Time
		err := r.Scan(&w.ID, &w.ElementID, &w.ElementKey, &w.MemberID, &w.Member, &d, &w.Hours, &w.RoleKey, &w.Capitalizable, &w.Text,
			&w.CostCents, &w.RateKind, &w.HourlyCents, &w.RateCurrency, &w.FxRate)
		w.WorkDate = d.Format(time.DateOnly)
		return w, err
	})
	if out == nil {
		out = []WorkLog{}
	}
	return out, err
}

// WorkLogInput: ore inserite a mano (il time tracking arriverà con il work management).
type WorkLogInput struct {
	Element       string  `json:"element"`
	MemberID      string  `json:"member_id"`
	WorkDate      string  `json:"work_date"`
	Hours         float64 `json:"hours"`
	RoleKey       *string `json:"role_key"`
	Capitalizable bool    `json:"capitalizable"`
	Text          *string `json:"text"`
}

func (s *Service) CreateWorkLog(ctx context.Context, tx pgx.Tx, actor engine.Actor, in WorkLogInput) (*WorkLog, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	el, err := s.eng.Get(ctx, tx, actor, in.Element)
	if err != nil {
		return nil, err
	}
	d, err := parseDate("work_date", in.WorkDate)
	if err != nil {
		return nil, err
	}
	minutes := int(math.Round(in.Hours * 60))
	if minutes <= 0 || minutes > 24*60 {
		return nil, engine.OutOfRange("hours", 0, 24, "ore tra 0 e 24")
	}
	if in.MemberID == "" {
		in.MemberID = actor.MemberID
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1)", in.MemberID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.NotFound("member_id", "member", "persona inesistente")
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into work_logs (tenant_id, id, element_id, member_id, work_date, minutes, role_key, capitalizable, text, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, el.ID, in.MemberID, d, minutes, normRole(in.RoleKey), in.Capitalizable, nullablePtr(in.Text), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "work_log.create", EntityType: "work_log", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": el.ID, "member_id": in.MemberID, "minutes": minutes}}); err != nil {
		return nil, err
	}
	if _, err := s.recompute(ctx, tx, actor, workLogScope{ids: []string{id}}); err != nil {
		return nil, err
	}
	logs, err := s.WorkLogs(ctx, tx, actor, WorkLogFilter{Element: el.ID, From: in.WorkDate, To: in.WorkDate})
	if err != nil {
		return nil, err
	}
	for i := range logs {
		if logs[i].ID == id {
			return &logs[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

func (s *Service) DeleteWorkLog(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireWrite(actor); err != nil {
		return err
	}
	var elementID string
	err := tx.QueryRow(ctx, "delete from work_logs where id::text = $1 returning element_id", id).Scan(&elementID)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.ErrNotFound
	}
	if err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "work_log.delete", EntityType: "work_log", EntityID: id, ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"element_id": elementID}})
}

// workLogScope seleziona le ore da ricalcolare; vuoto = tutte.
type workLogScope struct {
	ids       []string
	memberIDs []string
	roles     []string
	all       bool
}

// Recompute ricalcola i costi di tutte le ore (dopo modifiche ai cambi o alle impostazioni).
func (s *Service) Recompute(ctx context.Context, tx pgx.Tx, actor engine.Actor) (RecomputeResult, error) {
	if err := requireWrite(actor); err != nil {
		return RecomputeResult{}, err
	}
	res, err := s.recompute(ctx, tx, actor, workLogScope{all: true})
	if err != nil {
		return res, err
	}
	return res, audit.Append(ctx, tx, audit.Event{Action: "work_log.recompute", EntityType: "work_log", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"costed": res.Costed, "without_rate": res.WithoutRate}})
}

type RecomputeResult struct {
	Costed      int64 `json:"costed"`       // ore con un costo
	WithoutRate int64 `json:"without_rate"` // ore senza tariffa valida (o senza cambio)
}

// recompute calcola le voci di costo delle ore, in un'unica istruzione per tutte:
// tariffa della persona valida alla data se c'è, altrimenti quella del ruolo con cui si è
// lavorato; importo = minuti × tariffa / 60, convertito con il cambio del giorno. Le ore
// senza tariffa (o senza cambio) restano senza costo e la voce eventuale si toglie.
func (s *Service) recompute(ctx context.Context, tx pgx.Tx, actor engine.Actor, sc workLogScope) (RecomputeResult, error) {
	var res RecomputeResult
	if !sc.all && len(sc.ids) == 0 && len(sc.memberIDs) == 0 && len(sc.roles) == 0 {
		return res, nil
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return res, err
	}
	if st.LaborCategoryID == nil {
		return res, engine.Required("labor_category_id", "manca la categoria dei costi delle ore nelle impostazioni")
	}
	scope := `(w.id::text = any($1) or w.member_id::text = any($2) or w.role_key = any($3) or $4)`
	args := []any{sc.ids, sc.memberIDs, sc.roles, sc.all, st.BaseCurrency, *st.LaborCategoryID, nullable(actor.MemberID)}
	calc := `
		with wl as (select w.* from work_logs w where ` + scope + `),
		pick as (
			select wl.id as work_log_id, wl.tenant_id, wl.element_id, wl.work_date, wl.minutes, wl.capitalizable,
			       coalesce(pr.id, rr.id) as rate_id, coalesce(pr.hourly_cents, rr.hourly_cents) as hourly, coalesce(pr.currency, rr.currency) as currency
			from wl
			left join lateral (select id, hourly_cents, currency from rates r where r.member_id = wl.member_id and r.provider_element_id is null
			                   and r.valid_from <= wl.work_date and (r.valid_to is null or r.valid_to >= wl.work_date)
			                   order by r.valid_from desc limit 1) pr on true
			left join lateral (select id, hourly_cents, currency from rates r where r.member_id is null and r.role_key = wl.role_key
			                   and r.provider_element_id is null and r.valid_from <= wl.work_date and (r.valid_to is null or r.valid_to >= wl.work_date)
			                   order by r.valid_from desc limit 1) rr on pr.id is null
		),
		calc as (
			select p.*, round(p.minutes * p.hourly / 60.0)::bigint as amount,
			       case when p.currency = $5 then 1::numeric else fx.rate_to_base end as fx
			from pick p
			left join lateral (select rate_to_base from exchange_rates x where x.currency = p.currency and x.valid_on <= p.work_date
			                   order by x.valid_on desc limit 1) fx on p.currency <> $5
			where p.rate_id is not null
		)`
	_, err = tx.Exec(ctx, calc+`
		insert into cost_entries (tenant_id, id, element_id, cost_category_id, source, source_ref, work_log_id, entry_date, kind,
			amount_cents, currency, amount_base_cents, fx_rate, capitalizable, rate_id, description, created_by)
		select c.tenant_id, gen_random_uuid(), c.element_id, $6::uuid, 'work_log', c.work_log_id::text, c.work_log_id, c.work_date, 'actual',
		       c.amount, c.currency, round(c.amount * c.fx)::bigint, c.fx, c.capitalizable, c.rate_id, 'Ore registrate', $7::uuid
		from calc c where c.fx is not null
		on conflict (tenant_id, work_log_id) where work_log_id is not null do update set
			element_id = excluded.element_id, cost_category_id = excluded.cost_category_id, entry_date = excluded.entry_date,
			amount_cents = excluded.amount_cents, currency = excluded.currency, amount_base_cents = excluded.amount_base_cents,
			fx_rate = excluded.fx_rate, capitalizable = excluded.capitalizable, rate_id = excluded.rate_id, updated_at = now()
		where (cost_entries.element_id, cost_entries.cost_category_id, cost_entries.entry_date, cost_entries.amount_cents, cost_entries.currency,
		       cost_entries.amount_base_cents, cost_entries.capitalizable, cost_entries.rate_id)
		      is distinct from (excluded.element_id, excluded.cost_category_id, excluded.entry_date, excluded.amount_cents, excluded.currency,
		       excluded.amount_base_cents, excluded.capitalizable, excluded.rate_id)`, args...)
	if err != nil {
		return res, fmt.Errorf("calcolo dei costi delle ore: %w", err)
	}
	// Ore che non hanno più una tariffa (o un cambio): la voce si toglie.
	if _, err := tx.Exec(ctx, calc+`
		delete from cost_entries ce using wl
		where ce.work_log_id = wl.id and not exists (select 1 from calc c where c.work_log_id = wl.id and c.fx is not null)`, args[:5]...); err != nil {
		return res, err
	}
	err = tx.QueryRow(ctx, `select count(c.id), count(*) - count(c.id) from work_logs w
		left join cost_entries c on c.tenant_id = w.tenant_id and c.work_log_id = w.id where `+scope, sc.ids, sc.memberIDs, sc.roles, sc.all).
		Scan(&res.Costed, &res.WithoutRate)
	return res, err
}
