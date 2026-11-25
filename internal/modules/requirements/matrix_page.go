package requirements

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Matrice a pagine (benchmark 2026-11-24, 300 requisiti × 30 impatti = 9.000 celle: 681 ms e
// 1,3 MB). La matrice intera si calcola in SQL a livello di requisito (300 righe invece di
// 9.000), con le stesse regole di cellRow.status e di summarize; il dettaglio delle celle si
// legge solo per i requisiti della pagina. Il riepilogo riguarda sempre tutta la matrice.

// reqAgg è un requisito con lo stato aggregato delle sue celle.
type reqAgg struct {
	ID, Key, Title, Scope, Category, Stage, Phase, Origin, Method string
	// Celle visibili (impatto tra le colonne) non «non applicabili»: quante e peggior grado.
	Visible, Worst int
	// Stati presenti tra le celle visibili (anche «non applicabili»), per il filtro per stato.
	Present []string
	// Celle pertinenti al riepilogo: quante, peggior grado, se una ha la deroga scaduta.
	Cells, WorstCell int
	Expired          bool
}

// MatrixMissing: requisiti mancanti elencati nel riepilogo della matrice a pagine.
const MatrixMissing = 25

// statusByRank: i gradi di stato (statusRank) in ordine.
var statusByRank = []string{"proposed", "open", "to_verify", "waived", "verified"}

// Le espressioni ripetono cellRow.status, hasCell, relevant ed exceptionExpired. $2 è il giorno.
// Le celle si leggono solo sugli impatti vivi della richiesta ($3: gli id delle colonne senza il
// filtro per team, già letti da Matrix): non annullati e non cancellati, quindi senza un join
// per cella su impatto, stato, ext_impact e team (9.000 celle: da 40 a 20 ms).
const (
	aggStatus = `case when c.status = 'not_applicable' or x.stage = 'not_applicable' then 'not_applicable'
		when c.progress = 'verified' then 'verified'
		when c.progress = 'waived' and ex.stage = 'approved' and ex.expires_on >= $2::date then 'waived'
		when c.progress = 'to_verify' then 'to_verify'
		when x.stage = 'proposed' then 'proposed' else 'open' end`
	aggRelevant = `(c.id is not null and coalesce(c.status, '') <> 'not_applicable' and x.stage <> 'not_applicable')`
	// Visibile: l'impatto è una colonna della matrice dopo il filtro per team ($4).
	aggVisible = `(c.id is not null and c.target_element_id = any($4::uuid[]))`
	aggExpired = `(c.exception_id is not null and coalesce(c.progress, '') <> 'verified' and (ex.stage = 'expired'
		or (ex.stage = 'approved' and ex.expires_on is not null and ex.expires_on < $2::date)))`
)

func (s *Service) loadAggs(ctx context.Context, tx pgx.Tx, requestID string, today time.Time, live, visible []string) ([]reqAgg, error) {
	// L'aggregazione sulle celle gira su poche colonne (ordinare 9.000 righe larghe costava 16 ms);
	// requisito, chiave e fase si uniscono dopo, sulle 300 righe già aggregate.
	rows, err := tx.Query(ctx, `
		select g.rid::text, rs.key || '-' || re.number, re.title, g.scope, g.category, g.stage, coalesce(ro.name, ''), g.origin, g.method,
		       g.visible, g.worst, g.present, g.cells, g.worst_cell, g.expired
		from (
			select x.tenant_id, x.element_id as rid, x.scope, x.category, x.stage, x.origin, x.verification_method as method,
			       count(*) filter (where `+aggVisible+` and a.st <> 'not_applicable')::int as visible,
			       coalesce(min(b.rk) filter (where `+aggVisible+` and a.st <> 'not_applicable'), -1)::int as worst,
			       coalesce(array_agg(distinct a.st) filter (where `+aggVisible+`), '{}') as present,
			       count(*) filter (where `+aggRelevant+`)::int as cells,
			       coalesce(min(b.rk) filter (where `+aggRelevant+`), -1)::int as worst_cell,
			       coalesce(bool_or(`+aggExpired+`) filter (where `+aggRelevant+`), false) as expired
			from ext_requirement x
			left join applicability c on c.tenant_id = x.tenant_id and c.requirement_element_id = x.element_id
			                         and c.target_element_id = any($3::uuid[])
			left join ext_exception ex on ex.tenant_id = c.tenant_id and ex.element_id = c.exception_id
			cross join lateral (select `+aggStatus+` as st) a
			cross join lateral (select case a.st when 'proposed' then 0 when 'open' then 1 when 'to_verify' then 2 when 'waived' then 3 when 'verified' then 4 else 5 end as rk) b
			where x.request_id = $1
			group by x.tenant_id, x.element_id
		) g
		join elements re on re.tenant_id = g.tenant_id and re.id = g.rid and re.deleted_at is null
		join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id
		order by g.scope desc, re.number`, requestID, today.Format(time.DateOnly), live, visible)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[reqAgg])
}

// matrixPage è Matrix con le righe a pagine: m ha già colonne e team, colIdx le colonne per impatto.
func (s *Service) matrixPage(ctx context.Context, tx pgx.Tx, actor engine.Actor, req, el *engine.Element, m *Matrix,
	colIdx map[string]int, live []MatrixColumn, flt MatrixFilter, today time.Time) (*Matrix, error) {
	liveIDs, visibleIDs := make([]string, len(live)), make([]string, 0, len(colIdx))
	for i, c := range live {
		liveIDs[i] = c.ImpactID
		if _, ok := colIdx[c.ImpactID]; ok {
			visibleIDs = append(visibleIDs, c.ImpactID)
		}
	}
	aggs, err := s.loadAggs(ctx, tx, req.ID, today, liveIDs, visibleIDs)
	if err != nil {
		return nil, err
	}
	sum := Summary{Missing: []MissingItem{}}
	add := func(a reqAgg, st string, expired bool) {
		if st == "not_applicable" {
			return
		}
		sum.Total++
		switch st {
		case "proposed", "open":
			sum.Open++
		case "to_verify":
			sum.ToVerify++
		case "verified":
			sum.Verified++
		case "waived":
			sum.Waived++
		}
		if expired {
			sum.ExpiredExceptions++
		}
		if st != "verified" && st != "waived" {
			sum.Missing = append(sum.Missing, MissingItem{Key: a.Key, Title: a.Title, Status: st, StatusLabel: StatusLabels[st]})
		}
	}
	all := []MatrixRow{}
	for _, a := range aggs {
		if a.Category != "" && !slices.Contains(m.Categories, a.Category) {
			m.Categories = append(m.Categories, a.Category)
		}
		// Riepilogo: lo stato del requisito è quello della sua cella peggiore; senza celle conta solo
		// un requisito di progetto (non ci sono ancora impatti), con lo stato del suo stadio.
		if a.Stage != StageNotApplicable {
			switch {
			case a.Cells > 0:
				add(a, statusByRank[a.WorstCell], a.Expired)
			case a.Scope == "project":
				add(a, stageStatus(a.Stage), false)
			}
		}
		status := ""
		switch {
		case a.Stage == StageNotApplicable:
			status = "not_applicable"
		case a.Visible == 0:
			// Un requisito di applicazione di un impatto escluso non si mostra; uno di progetto
			// senza celle sì, con lo stato del suo stadio.
			if a.Scope == "application" {
				continue
			}
			status = stageStatus(a.Stage)
		default:
			status = statusByRank[a.Worst]
		}
		if flt.Category != "" && !strings.EqualFold(a.Category, flt.Category) {
			continue
		}
		if flt.Status != "" && status != flt.Status && !slices.Contains(a.Present, flt.Status) {
			continue
		}
		all = append(all, MatrixRow{ID: a.ID, Key: a.Key, Title: a.Title, Scope: a.Scope, Category: a.Category, Stage: a.Stage,
			Phase: a.Phase, Origin: a.Origin, Method: a.Method, Status: status, Cells: make([]*MatrixCell, len(m.Columns))})
	}
	slices.Sort(m.Categories)
	sum.Ready = sum.Open == 0 && sum.ToVerify == 0
	// L'elenco dei mancanti in una matrice da 300 requisiti pesa quanto la metà della risposta e la
	// pagina ne mostra solo il numero: si manda l'inizio e il totale.
	if len(sum.Missing) > MatrixMissing {
		sum.MissingTotal, sum.Missing = len(sum.Missing), sum.Missing[:MatrixMissing]
	}
	m.Summary = sum
	m.TotalRows = len(all)
	lo := min(max(flt.Offset, 0), len(all))
	hi := min(lo+flt.Limit, len(all))
	m.Offset, m.More = lo, hi < len(all)
	m.Rows = all[lo:hi]
	if err := s.matrixCells(ctx, tx, m, colIdx, today); err != nil {
		return nil, err
	}
	m.Can.Create, err = s.canCreate(ctx, tx, actor, req, el)
	return m, err
}

// matrixCells completa le righe della pagina con le celle (stato, deroga, chi ha verificato).
func (s *Service) matrixCells(ctx context.Context, tx pgx.Tx, m *Matrix, colIdx map[string]int, today time.Time) error {
	if len(m.Rows) == 0 {
		return nil
	}
	ids := make([]string, len(m.Rows))
	byID := make(map[string]*MatrixRow, len(m.Rows))
	for i := range m.Rows {
		ids[i] = m.Rows[i].ID
		byID[m.Rows[i].ID] = &m.Rows[i]
	}
	full, err := s.loadCells(ctx, tx, "x.element_id = any($1::uuid[])", ids)
	if err != nil {
		return err
	}
	for _, r := range full {
		row := byID[r.ReqID]
		if row == nil {
			continue
		}
		if r.Scope == "application" && r.hasCell() {
			row.ImpactKey = r.ImpactKey
		}
		i, ok := colIdx[r.ImpactID]
		if !ok || !r.hasCell() {
			continue
		}
		c := &MatrixCell{Status: r.status(today), ExceptionKey: r.ExceptionKey, ExceptionExpired: r.exceptionExpired(today), VerifiedBy: r.VerifiedBy}
		if r.ExpiresOn != nil {
			d := r.ExpiresOn.Format(time.DateOnly)
			c.ExpiresOn = &d
		}
		if r.VerifiedAt != nil {
			d := r.VerifiedAt.UTC().Format(time.RFC3339)
			c.VerifiedAt = &d
		}
		row.Cells[i] = c
	}
	return nil
}
