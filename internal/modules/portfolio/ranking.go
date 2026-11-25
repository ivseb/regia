package portfolio

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/rbac"
)

// Settings sono le impostazioni della graduatoria del tenant.
type Settings struct {
	MandatoryFirst bool       `json:"mandatory_first"` // obblighi normativi in testa
	BudgetCents    *int64     `json:"budget_cents"`    // budget disponibile: traccia la linea di taglio
	EstimateBasis  string     `json:"estimate_basis"`  // low | mid | high: quale stima si somma
	ExcludedPhases []string   `json:"excluded_phases"` // fasi aperte che non entrano in graduatoria
	RankRoles      []string   `json:"rank_roles"`      // ruoli sulla richiesta che possono forzare l'ordine
	UpdatedAt      *time.Time `json:"updated_at"`
	UpdatedBy      *string    `json:"updated_by"`
}

func defaultSettings() Settings {
	return Settings{MandatoryFirst: true, EstimateBasis: "high", ExcludedPhases: []string{"Bozza"}, RankRoles: []string{"pmo"}}
}

// GetSettings legge le impostazioni (i default se il tenant non le ha ancora salvate).
func GetSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	st := defaultSettings()
	err := tx.QueryRow(ctx, `select p.mandatory_first, p.budget_cents, p.estimate_basis, p.excluded_phases, p.rank_roles, p.updated_at, m.display_name
		from portfolio_settings p left join members m on m.tenant_id = p.tenant_id and m.id = p.updated_by`).
		Scan(&st.MandatoryFirst, &st.BudgetCents, &st.EstimateBasis, &st.ExcludedPhases, &st.RankRoles, &st.UpdatedAt, &st.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSettings(), nil
	}
	return st, err
}

// SettingsInput modifica le impostazioni; i campi assenti restano come sono.
type SettingsInput struct {
	MandatoryFirst *bool     `json:"mandatory_first"`
	BudgetCents    *int64    `json:"budget_cents"`
	ClearBudget    bool      `json:"clear_budget"`
	EstimateBasis  *string   `json:"estimate_basis"`
	ExcludedPhases *[]string `json:"excluded_phases"`
	RankRoles      *[]string `json:"rank_roles"`
}

// UpdateSettings salva le impostazioni della graduatoria (permesso portfolio.admin).
func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SettingsInput) (Settings, error) {
	if err := requireAdmin(actor); err != nil {
		return Settings{}, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return st, err
	}
	if in.MandatoryFirst != nil {
		st.MandatoryFirst = *in.MandatoryFirst
	}
	if in.BudgetCents != nil {
		if *in.BudgetCents < 0 {
			return st, engine.InvalidCode("validation.not_negative", "budget_cents", "il budget non può essere negativo")
		}
		st.BudgetCents = in.BudgetCents
	}
	if in.ClearBudget {
		st.BudgetCents = nil
	}
	if in.EstimateBasis != nil {
		switch *in.EstimateBasis {
		case "low", "mid", "high":
			st.EstimateBasis = *in.EstimateBasis
		default:
			return st, engine.InvalidValue("estimate_basis", "valore non ammesso (low, mid, high)")
		}
	}
	if in.ExcludedPhases != nil {
		st.ExcludedPhases = *in.ExcludedPhases
	}
	if in.RankRoles != nil {
		for _, r := range *in.RankRoles {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", r).Scan(&ok); err != nil {
				return st, err
			}
			if !ok {
				return st, engine.NotFound("rank_roles", "role", "ruolo %q inesistente", r)
			}
		}
		st.RankRoles = *in.RankRoles
	}
	if st.ExcludedPhases == nil {
		st.ExcludedPhases = []string{}
	}
	if st.RankRoles == nil {
		st.RankRoles = []string{}
	}
	if _, err := tx.Exec(ctx, `insert into portfolio_settings (tenant_id, mandatory_first, budget_cents, estimate_basis, excluded_phases, rank_roles, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, now())
		on conflict (tenant_id) do update set mandatory_first = excluded.mandatory_first, budget_cents = excluded.budget_cents,
			estimate_basis = excluded.estimate_basis, excluded_phases = excluded.excluded_phases, rank_roles = excluded.rank_roles,
			updated_by = excluded.updated_by, updated_at = now()`,
		st.MandatoryFirst, st.BudgetCents, st.EstimateBasis, st.ExcludedPhases, st.RankRoles, nullable(actor.MemberID)); err != nil {
		return st, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "portfolio.settings", EntityType: "portfolio_settings", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"mandatory_first": st.MandatoryFirst, "budget_cents": st.BudgetCents,
			"estimate_basis": st.EstimateBasis, "excluded_phases": st.ExcludedPhases, "rank_roles": st.RankRoles}}); err != nil {
		return st, err
	}
	return GetSettings(ctx, tx)
}

// Item è una riga della graduatoria.
type Item struct {
	Position           int        `json:"position"`
	ID                 string     `json:"id"`
	Key                string     `json:"key"`
	Title              string     `json:"title"`
	State              string     `json:"state"`
	StateCategory      string     `json:"state_category"`
	IsMandatory        bool       `json:"is_mandatory"`
	Driver             *string    `json:"driver"`
	RegulatoryDeadline *time.Time `json:"regulatory_deadline"`
	DesiredDate        *time.Time `json:"desired_date"`
	EstimateLowCents   *int64     `json:"estimate_low_cents"`
	EstimateHighCents  *int64     `json:"estimate_high_cents"`
	// EstimateCents: la stima che si somma (secondo estimate_basis); nil se manca.
	EstimateCents *int64   `json:"estimate_cents"`
	Score         *float64 `json:"score"`
	ScoreVersion  *int     `json:"score_version"`
	ScoreStale    bool     `json:"score_stale"` // dato con una versione non più corrente
	// ValuePerCost: punti di punteggio per 100 k€ di stima.
	ValuePerCost    *float64   `json:"value_per_cost"`
	CumulativeCents int64      `json:"cumulative_cents"`
	Funded          *bool      `json:"funded"` // nil se non c'è un budget
	Pinned          bool       `json:"pinned"` // posizione forzata a mano
	PinReason       *string    `json:"pin_reason"`
	PinnedBy        *string    `json:"pinned_by"`
	PinnedAt        *time.Time `json:"pinned_at"`
	CanMove         bool       `json:"can_move"`
	pin             int
	createdAt       time.Time
}

// Ranking è la graduatoria del portafoglio.
type Ranking struct {
	Items       []*Item  `json:"items"`
	Settings    Settings `json:"settings"`
	BudgetCents *int64   `json:"budget_cents"` // budget usato (impostazioni o simulazione)
	TotalCents  int64    `json:"total_cents"`
	FundedCents int64    `json:"funded_cents"`
	FundedCount int      `json:"funded_count"`
	// Cut: numero di richieste sopra la linea di taglio (nil senza budget).
	Cut       *int `json:"cut"`
	Mandatory int  `json:"mandatory"` // obblighi normativi in graduatoria
	CanAdmin  bool `json:"can_admin"`
	Scorecard *Ref `json:"scorecard"`
	// BudgetSource dice da dove viene il budget: dal plafond di un esercizio, impostato a mano o assente.
	BudgetSource BudgetSource `json:"budget_source"`
	// Scope c'è quando la graduatoria è ristretta a un'area e a un periodo (budget.md §6).
	Scope *Scope `json:"scope,omitempty"`
}

// RankFilter restringe la graduatoria: richieste dell'unità (e di quelle sotto) e del periodo di competenza
// indicato, oppure senza un periodo assegnato.
type RankFilter struct {
	UnitID   string
	PeriodID string
}

// Scope descrive l'area e il periodo scelti e lo stanziamento da cui viene il budget (compilato dall'API).
type Scope struct {
	UnitID         string `json:"unit_id"`
	Unit           string `json:"unit"`
	PeriodID       string `json:"period_id,omitempty"`
	Period         string `json:"period,omitempty"`
	AllocationID   string `json:"allocation_id,omitempty"`
	AllocatedCents int64  `json:"allocated_cents"`
	ResidualCents  int64  `json:"residual_cents"`
	// HasAllocation è falso quando l'area non ha uno stanziamento nel periodo (o non si vede).
	HasAllocation bool `json:"has_allocation"`
}

// BudgetSource è l'origine del budget della graduatoria. Suggested è l'esercizio in corso con un plafond,
// quando il budget non viene da un esercizio: la pagina propone di usarlo.
type BudgetSource struct {
	Kind       string         `json:"kind"` // fiscal_year | manual | none
	FiscalYear string         `json:"fiscal_year,omitempty"`
	Basis      string         `json:"basis,omitempty"` // envelope (plafond) | residual (residuo)
	Suggested  *SuggestedYear `json:"suggested"`
}

type SuggestedYear struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	EnvelopeCents int64  `json:"envelope_cents"`
}

// budgetSource legge il collegamento con la Finanza (impostazioni dell'esercizio che alimenta la graduatoria).
func budgetSource(ctx context.Context, tx pgx.Tx, budget *int64) (BudgetSource, error) {
	src := BudgetSource{Kind: "none"}
	if budget != nil {
		src.Kind = "manual"
	}
	var name, basis *string
	err := tx.QueryRow(ctx, `select f.name, s.portfolio_basis from finance_settings s
		join fiscal_years f on f.tenant_id = s.tenant_id and f.id = s.portfolio_fiscal_year_id`).Scan(&name, &basis)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return src, err
	}
	if err == nil && name != nil {
		src.Kind, src.FiscalYear, src.Basis = "fiscal_year", *name, *basis
		return src, nil
	}
	var s SuggestedYear
	err = tx.QueryRow(ctx, `select id, name, envelope_cents from fiscal_years
		where start_date <= current_date and end_date >= current_date and envelope_cents is not null and status <> 'closed'
		order by start_date desc limit 1`).Scan(&s.ID, &s.Name, &s.EnvelopeCents)
	if err == nil {
		src.Suggested = &s
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return src, err
	}
	return src, nil
}

// Ref identifica la versione corrente della scorecard.
type Ref struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

const rankLimit = 2000

// Ranking calcola la graduatoria: prima gli obblighi normativi (se configurato), poi il
// punteggio; le posizioni forzate a mano si inseriscono al loro posto nel proprio gruppo.
// budget, se non nil, simula un budget diverso da quello salvato.
func (s *Service) Ranking(ctx context.Context, tx pgx.Tx, actor engine.Actor, budget *int64) (*Ranking, error) {
	return s.RankingFor(ctx, tx, actor, budget, RankFilter{})
}

// RankingFor è la graduatoria ristretta a un'area e a un periodo (filtro vuoto = tutto il portafoglio).
func (s *Service) RankingFor(ctx context.Context, tx pgx.Tx, actor engine.Actor, budget *int64, f RankFilter) (*Ranking, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &Ranking{Settings: st, BudgetCents: st.BudgetCents, Items: []*Item{}, CanAdmin: actor.Has(rbac.PortfolioAdmin)}
	if budget != nil {
		out.BudgetCents = budget
	}
	if out.BudgetSource, err = budgetSource(ctx, tx, st.BudgetCents); err != nil {
		return nil, err
	}
	var sc Ref
	err = tx.QueryRow(ctx, `select s.key, s.name, s.version from scorecards s join element_types t on t.tenant_id = s.tenant_id and t.id = s.type_id
		where s.status = 'published' and t.key = 'business_request' order by s.key limit 1`).Scan(&sc.Key, &sc.Name, &sc.Version)
	if err == nil {
		out.Scorecard = &sc
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select e.id, coalesce(sp.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		       d.is_mandatory, d.driver, d.regulatory_deadline, d.desired_date, d.estimate_low_cents, d.estimate_high_cents,
		       d.score, sc.version, coalesce(sc.status <> 'published', false),
		       coalesce(d.rank, 0), d.rank_reason, rm.display_name, d.rank_set_at, e.created_at
		from ext_demand d
		join elements e on e.tenant_id = d.tenant_id and e.id = d.element_id and e.deleted_at is null and not e.is_draft
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join scorecards sc on sc.tenant_id = d.tenant_id and sc.id = d.score_scorecard_id
		left join members rm on rm.tenant_id = d.tenant_id and rm.id = d.rank_set_by
		where d.merged_into_element_id is null
		  and coalesce(o.category, 'todo') in ('todo', 'in_progress')
		  and not (coalesce(o.name, '') = any($1))
		  and ($4::text = '' or d.requesting_unit_id in (select org_subtree($4::text::uuid)))
		  and ($5::text = '' or d.budget_period_id is null or d.budget_period_id = $5::text::uuid)
		order by (case when $2 then not d.is_mandatory else false end), d.score desc nulls last,
		         d.regulatory_deadline nulls last, e.created_at, e.id
		limit $3`, st.ExcludedPhases, st.MandatoryFirst, rankLimit, f.UnitID, f.PeriodID)
	if err != nil {
		return nil, err
	}
	var all []*Item
	for rows.Next() {
		it := &Item{}
		if err := rows.Scan(&it.ID, &it.Key, &it.Title, &it.State, &it.StateCategory, &it.IsMandatory, &it.Driver, &it.RegulatoryDeadline,
			&it.DesiredDate, &it.EstimateLowCents, &it.EstimateHighCents, &it.Score, &it.ScoreVersion, &it.ScoreStale,
			&it.pin, &it.PinReason, &it.PinnedBy, &it.PinnedAt, &it.createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		it.Pinned = it.pin > 0
		if !it.Pinned {
			it.PinReason, it.PinnedBy, it.PinnedAt = nil, nil, nil
		}
		all = append(all, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Gruppi: obblighi normativi e altre richieste (oppure un gruppo solo).
	var groups [][]*Item
	if st.MandatoryFirst {
		var m, o []*Item
		for _, it := range all {
			if it.IsMandatory {
				m = append(m, it)
			} else {
				o = append(o, it)
			}
		}
		groups = [][]*Item{m, o}
		out.Mandatory = len(m)
	} else {
		groups = [][]*Item{all}
		for _, it := range all {
			if it.IsMandatory {
				out.Mandatory++
			}
		}
	}
	for _, g := range groups {
		out.Items = append(out.Items, placePins(g)...)
	}

	// Posizioni, stime cumulate e linea di taglio.
	var cum int64
	over := false
	for i, it := range out.Items {
		it.Position = i + 1
		it.EstimateCents = basis(st.EstimateBasis, it.EstimateLowCents, it.EstimateHighCents)
		if it.EstimateCents != nil {
			cum += *it.EstimateCents
			if it.Score != nil && *it.EstimateCents > 0 {
				v := math.Round(*it.Score/(float64(*it.EstimateCents)/10_000_000)*10) / 10
				it.ValuePerCost = &v
			}
		}
		it.CumulativeCents = cum
		if out.BudgetCents != nil {
			funded := !over && cum <= *out.BudgetCents
			if !funded {
				over = true
			}
			it.Funded = &funded
			if funded {
				out.FundedCount++
				out.FundedCents = cum
			}
		}
	}
	out.TotalCents = cum
	if out.BudgetCents != nil {
		c := out.FundedCount
		out.Cut = &c
	}
	if err := s.markMovable(ctx, tx, actor, st, out.Items); err != nil {
		return nil, err
	}
	return out, nil
}

// placePins inserisce le richieste con posizione forzata al loro posto (1 = prima del
// gruppo); a parità di posizione vince lo spostamento più recente.
func placePins(g []*Item) []*Item {
	var auto, pinned []*Item
	for _, it := range g {
		if it.Pinned {
			pinned = append(pinned, it)
		} else {
			auto = append(auto, it)
		}
	}
	slices.SortStableFunc(pinned, func(a, b *Item) int {
		if a.pin != b.pin {
			return a.pin - b.pin
		}
		return a.PinnedAt.Compare(*b.PinnedAt)
	})
	out := append([]*Item{}, auto...)
	for _, p := range pinned {
		i := min(p.pin-1, len(out))
		out = slices.Insert(out, i, p)
	}
	return out
}

func basis(b string, low, high *int64) *int64 {
	switch {
	case low == nil && high == nil:
		return nil
	case low == nil:
		return high
	case high == nil:
		return low
	}
	switch b {
	case "low":
		return low
	case "mid":
		v := (*low + *high) / 2
		return &v
	}
	return high
}

// markMovable: chi ha portfolio.admin sposta tutto; gli altri le richieste su cui hanno
// uno dei ruoli configurati (default PMO).
func (s *Service) markMovable(ctx context.Context, tx pgx.Tx, actor engine.Actor, st Settings, items []*Item) error {
	if actor.Has(rbac.PortfolioAdmin) {
		for _, it := range items {
			it.CanMove = true
		}
		return nil
	}
	if len(st.RankRoles) == 0 || len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	rows, err := tx.Query(ctx, `select distinct sh.element_id::text from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		where sh.element_id = any($1::uuid[]) and r.key = any($2)
		  and (sh.member_id = $3 or sh.group_id in (select member_groups($3)))`,
		ids, st.RankRoles, actor.MemberID)
	if err != nil {
		return err
	}
	ok, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, it := range items {
		it.CanMove = slices.Contains(ok, it.ID)
	}
	return nil
}

// MoveInput forza la posizione di una richiesta (1 = prima); nil torna all'ordine automatico.
type MoveInput struct {
	Position *int   `json:"position"`
	Reason   string `json:"reason"`
}

// Move forza a mano la posizione di una richiesta in graduatoria, con motivazione
// obbligatoria, voce nella storia e audit. Se gli obblighi normativi stanno in testa,
// la richiesta si sposta solo dentro il proprio gruppo.
func (s *Service) Move(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in MoveInput) (*Ranking, error) {
	el, err := s.loadRequest(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	st, err := GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	allowed := actor.Has(rbac.PortfolioAdmin)
	if !allowed && len(st.RankRoles) > 0 {
		if allowed, err = engine.HasRole(ctx, tx, el.ID, actor.MemberID, st.RankRoles); err != nil {
			return nil, err
		}
	}
	if !allowed {
		return nil, engine.ErrForbidden
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, engine.Required("reason", "serve una motivazione per cambiare l'ordine della graduatoria")
	}
	if len(in.Reason) > 1000 {
		return nil, engine.TooLong("reason", 1000, "motivazione troppo lunga (massimo 1000 caratteri)")
	}
	before, err := s.Ranking(ctx, tx, actor, nil)
	if err != nil {
		return nil, err
	}
	idx := slices.IndexFunc(before.Items, func(it *Item) bool { return it.ID == el.ID })
	if idx < 0 {
		return nil, engine.InvalidParams("validation.portfolio.not_ranked", "position", map[string]any{"stage": el.State}, "la richiesta non è in graduatoria (fase %s)", el.State)
	}
	cur := before.Items[idx]
	var stored *int
	if in.Position != nil {
		p := *in.Position
		if p < 1 || p > len(before.Items) {
			return nil, engine.OutOfRange("position", 1, len(before.Items), "posizione fuori dalla graduatoria (1-%d)", len(before.Items))
		}
		if st.MandatoryFirst {
			m := before.Mandatory
			switch {
			case cur.IsMandatory && p > m:
				return nil, engine.InvalidParams("validation.portfolio.position_mandatory_top", "position", map[string]any{"max": m}, "gli obblighi normativi restano in testa: posizione tra 1 e %d", m)
			case !cur.IsMandatory && p <= m:
				return nil, engine.InvalidParams("validation.portfolio.position_first_available", "position", map[string]any{"first": m + 1}, "gli obblighi normativi restano in testa: la prima posizione disponibile è %d", m+1)
			case !cur.IsMandatory:
				p -= m
			}
		}
		stored = &p
	} else if !cur.Pinned {
		return before, nil
	}
	if _, err := tx.Exec(ctx, `update ext_demand set rank = $2, rank_reason = $3, rank_set_by = $4, rank_set_at = clock_timestamp() where element_id = $1`,
		el.ID, stored, in.Reason, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	after, err := s.Ranking(ctx, tx, actor, nil)
	if err != nil {
		return nil, err
	}
	to := 0
	for _, it := range after.Items {
		if it.ID == el.ID {
			to = it.Position
		}
	}
	field := "pinned"
	if stored == nil {
		field = "automatic"
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "rank", field,
		map[string]any{"position": to, "reason": in.Reason}, map[string]any{"position": cur.Position}); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "portfolio.rank", EntityType: "business_request", EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"from": cur.Position, "to": to, "manual": stored != nil, "reason_length": len(in.Reason)}}); err != nil {
		return nil, err
	}
	return after, nil
}

// WriteCSV esporta la graduatoria (separatore ";" per Excel in italiano, importi in euro).
func (r *Ranking) WriteCSV(w io.Writer) error {
	if _, err := w.Write([]byte("\xef\xbb\xbf")); err != nil { // BOM: Excel riconosce l'UTF-8
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	euros := func(c *int64) string {
		if c == nil {
			return ""
		}
		return strings.ReplaceAll(fmt.Sprintf("%.2f", float64(*c)/100), ".", ",")
	}
	num := func(f *float64) string {
		if f == nil {
			return ""
		}
		return strings.ReplaceAll(fmt.Sprintf("%.1f", *f), ".", ",")
	}
	day := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format(time.DateOnly)
	}
	_ = cw.Write([]string{"posizione", "chiave", "titolo", "fase", "obbligo_normativo", "scadenza_normativa", "punteggio",
		"versione_scorecard", "stima_min_eur", "stima_max_eur", "stima_sommata_eur", "valore_per_100k", "cumulata_eur", "finanziata",
		"posizione_forzata", "motivazione"})
	for _, it := range r.Items {
		funded := ""
		if it.Funded != nil {
			funded = map[bool]string{true: "sì", false: "no"}[*it.Funded]
		}
		version := ""
		if it.ScoreVersion != nil {
			version = fmt.Sprint(*it.ScoreVersion)
		}
		reason := ""
		if it.Pinned && it.PinReason != nil {
			reason = *it.PinReason
		}
		cum := it.CumulativeCents
		_ = cw.Write([]string{fmt.Sprint(it.Position), it.Key, it.Title, it.State, map[bool]string{true: "sì", false: "no"}[it.IsMandatory],
			day(it.RegulatoryDeadline), num(it.Score), version, euros(it.EstimateLowCents), euros(it.EstimateHighCents), euros(it.EstimateCents),
			num(it.ValuePerCost), euros(&cum), funded, map[bool]string{true: "sì", false: "no"}[it.Pinned], reason})
	}
	cw.Flush()
	return cw.Error()
}
