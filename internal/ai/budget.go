package ai

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Prezzi di listino noti, in dollari per milione di token (input, output). Il costo nel
// registro è una stima: il fornitore fattura con le sue regole (cache, sconti, valuta).
var listPrices = map[string][2]float64{
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-sonnet-5-5": {2, 10},
	"claude-opus-5-5":   {4, 20},
	"claude-opus-5":     {5, 25},
	"claude-sonnet-5":   {2, 10},
	"claude-fable-5-2":  {8, 40},
	"claude-haiku-4-5":  {1, 5},
}

// Moltiplicatori della cache del prompt sul prezzo dell'input (listino Anthropic: lettura al
// 10%, scrittura con durata di 5 minuti al 125%).
const (
	cacheReadFactor  = 0.1
	cacheWriteFactor = 1.25
)

// Cost stima il costo di una chiamata: prezzi del fornitore se impostati, altrimenti il
// listino noto del modello, altrimenti zero (modelli locali).
func Cost(p *ProviderRow, model string, in, out int64) float64 {
	return CostCached(p, model, in, 0, 0, out)
}

// CostCached è Cost con la cache del prompt: in è tutto l'input, di cui cacheRead letto dalla
// cache e cacheWrite scritto nella cache.
func CostCached(p *ProviderRow, model string, in, cacheRead, cacheWrite, out int64) float64 {
	pin, pout := 0.0, 0.0
	if lp, ok := listPrices[strings.TrimPrefix(model, "anthropic.")]; ok {
		pin, pout = lp[0], lp[1]
	}
	if p != nil && p.PriceInput != nil {
		pin = *p.PriceInput
	}
	if p != nil && p.PriceOutput != nil {
		pout = *p.PriceOutput
	}
	plain := max(in-cacheRead-cacheWrite, 0)
	return (float64(plain)*pin + float64(cacheRead)*pin*cacheReadFactor + float64(cacheWrite)*pin*cacheWriteFactor + float64(out)*pout) / 1e6
}

// Stati del budget.
const (
	BudgetNone    = "none"    // nessun budget impostato
	BudgetOK      = "ok"      // sotto l'80%
	BudgetWarn    = "warn"    // dall'80%
	BudgetBlocked = "blocked" // dal 100%: le chiamate non partono
)

// Budget è il consumo del mese in corso rispetto ai limiti del tenant.
type Budget struct {
	Month       string   `json:"month"` // AAAA-MM nel fuso del tenant
	MonthStart  string   `json:"-"`
	SpentUSD    float64  `json:"spent_usd"`
	Tokens      int64    `json:"tokens"`
	LimitUSD    *float64 `json:"limit_usd"`
	LimitTokens *int64   `json:"limit_tokens"`
	// Percent è il più alto dei due rapporti (dollari e token).
	Percent float64 `json:"percent"`
	State   string  `json:"state"`
}

// budget calcola il consumo del mese dal registro delle chiamate (fuso del tenant).
func (s *Service) budget(ctx context.Context, tx pgx.Tx) (Budget, error) {
	var b Budget
	var start time.Time
	err := tx.QueryRow(ctx, `with m as (
			select date_trunc('month', now() at time zone t.default_timezone) as local_start, t.default_timezone as tz
			from tenants t where t.id = app_tenant())
		select to_char(m.local_start, 'YYYY-MM'), m.local_start,
			coalesce((select sum(c.cost_usd)::float8 from ai_calls c where c.created_at >= (m.local_start at time zone m.tz)), 0),
			coalesce((select sum(coalesce(c.input_tokens, 0) + coalesce(c.output_tokens, 0)) from ai_calls c
				where c.created_at >= (m.local_start at time zone m.tz)), 0),
			(select monthly_budget_usd::float8 from ai_settings where tenant_id = app_tenant()),
			(select monthly_token_budget from ai_settings where tenant_id = app_tenant())
		from m`).Scan(&b.Month, &start, &b.SpentUSD, &b.Tokens, &b.LimitUSD, &b.LimitTokens)
	if err != nil {
		return b, err
	}
	b.MonthStart = start.Format(time.DateOnly)
	b.State = BudgetNone
	if b.LimitUSD != nil && *b.LimitUSD > 0 {
		b.Percent = max(b.Percent, 100*b.SpentUSD / *b.LimitUSD)
	}
	if b.LimitTokens != nil && *b.LimitTokens > 0 {
		b.Percent = max(b.Percent, 100*float64(b.Tokens)/float64(*b.LimitTokens))
	}
	if b.LimitUSD != nil || b.LimitTokens != nil {
		switch {
		case b.Percent >= 100:
			b.State = BudgetBlocked
		case b.Percent >= 80:
			b.State = BudgetWarn
		default:
			b.State = BudgetOK
		}
	}
	return b, nil
}

// Budget è il consumo del mese in corso (per l'API).
func (s *Service) Budget(ctx context.Context, tx pgx.Tx) (Budget, error) { return s.budget(ctx, tx) }

// UsageRow è una riga di riepilogo del consumo.
type UsageRow struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Errors       int64   `json:"errors"`
}

// CallView è una chiamata del registro.
type CallView struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	Member       string    `json:"member"`
	Feature      string    `json:"feature"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Tier         string    `json:"tier"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	Outcome      string    `json:"outcome"`
	ErrorCode    string    `json:"error_code"`
	DurationMS   int64     `json:"duration_ms"`
	PromptHash   string    `json:"prompt_sha256"`
}

// Usage è il consumo di un mese per l'amministratore.
type Usage struct {
	Month     string     `json:"month"`
	Budget    Budget     `json:"budget"`
	Total     UsageRow   `json:"total"`
	ByFeature []UsageRow `json:"by_feature"`
	ByMember  []UsageRow `json:"by_member"`
	ByModel   []UsageRow `json:"by_model"`
	Recent    []CallView `json:"recent"`
}

// Usage riassume il registro di un mese (AAAA-MM; vuoto = il mese in corso).
func (s *Service) Usage(ctx context.Context, tx pgx.Tx, month string) (*Usage, error) {
	b, err := s.budget(ctx, tx)
	if err != nil {
		return nil, err
	}
	if month == "" {
		month = b.Month
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, invalid("month", "ai.month_invalid", "mese non valido (AAAA-MM)")
	}
	out := &Usage{Month: month, Budget: b}
	const window = `with w as (select (to_date($1, 'YYYY-MM')::timestamp at time zone t.default_timezone) as a,
			((to_date($1, 'YYYY-MM') + interval '1 month')::timestamp at time zone t.default_timezone) as z
			from tenants t where t.id = app_tenant())`
	group := func(key, label, join string) ([]UsageRow, error) {
		rows, err := tx.Query(ctx, window+`
			select coalesce(`+key+`, ''), coalesce(`+label+`, ''), count(*), coalesce(sum(c.input_tokens), 0), coalesce(sum(c.output_tokens), 0),
				coalesce(sum(c.cost_usd), 0)::float8, count(*) filter (where c.outcome <> 'ok')
			from ai_calls c `+join+`, w where c.created_at >= w.a and c.created_at < w.z
			group by 1, 2 order by 6 desc, 3 desc limit 50`, month)
		if err != nil {
			return nil, err
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[UsageRow])
		if list == nil {
			list = []UsageRow{}
		}
		return list, err
	}
	if out.ByFeature, err = group("c.feature", "c.feature", ""); err != nil {
		return nil, err
	}
	if out.ByModel, err = group("c.provider || ':' || c.model", "c.model", ""); err != nil {
		return nil, err
	}
	if out.ByMember, err = group("c.member_id::text", "m.display_name",
		"left join members m on m.tenant_id = c.tenant_id and m.id = c.member_id"); err != nil {
		return nil, err
	}
	for _, r := range out.ByFeature {
		out.Total.Calls += r.Calls
		out.Total.InputTokens += r.InputTokens
		out.Total.OutputTokens += r.OutputTokens
		out.Total.CostUSD += r.CostUSD
		out.Total.Errors += r.Errors
	}
	rows, err := tx.Query(ctx, window+`
		select c.id::text, c.created_at, coalesce(m.display_name, ''), c.feature, c.provider, c.model, coalesce(c.tier, ''),
			coalesce(c.input_tokens, 0)::int8, coalesce(c.output_tokens, 0)::int8, c.cost_usd::float8, coalesce(c.outcome, ''),
			coalesce(c.error_code, ''), coalesce(c.duration_ms, 0)::int8, coalesce(encode(c.prompt_sha256, 'hex'), '')
		from ai_calls c left join members m on m.tenant_id = c.tenant_id and m.id = c.member_id, w
		where c.created_at >= w.a and c.created_at < w.z order by c.created_at desc limit 100`, month)
	if err != nil {
		return nil, err
	}
	if out.Recent, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CallView]); err != nil {
		return nil, err
	}
	if out.Recent == nil {
		out.Recent = []CallView{}
	}
	return out, nil
}
