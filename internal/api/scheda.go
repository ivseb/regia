package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Ogni scheda si apre sulla sintesi giusta (replica/usabilita.md, proposta 2; migrazione: nessuna).
//
//   - GET /api/v1/elements/{ref}/summary: sintesi «decisione prima» di una richiesta;
//   - GET /api/v1/architecture/applications/{ref}/360: la vista unica di un'applicazione.
//
// Sono letture aggregate in una sola transazione con i permessi e la RLS di chi chiede: ogni numero
// viene dalla stessa funzione della pagina che lo mostra per esteso (graduatoria, valutazioni,
// requisiti, vulnerabilità, gate, aderenza, conformità, rischi). Un blocco che la persona non può
// leggere, o il cui modulo è spento, manca dalla risposta (null) invece di far fallire tutto.
// I testi sono del client: qui solo codici (kind), conteggi e nomi che sono dati del tenant.

func (s *Server) schedaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/elements/{ref}/summary", inTenant(s.requestSummary))
	mux.HandleFunc("GET /api/v1/architecture/applications/{ref}/360", inTenant(s.application360))
}

// soft dice se un errore va tollerato (il blocco manca) o propagato.
func soft(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, engine.ErrForbidden) || errors.Is(err, engine.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// ---------------------------------------------------------------------
// Richiesta «decisione prima»

// SummaryAction è una cosa che manca o che richiede un'azione, con chi deve agire e la scheda dove farla.
type SummaryAction struct {
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
	// Detail: un nome o un testo che sono dati (un requisito, un controllo, un motivo del motore).
	Detail string `json:"detail,omitempty"`
	// Who: chi deve agire (nomi, ruoli o gruppi, come li mostra l'approvazione).
	Who string `json:"who,omitempty"`
	// Tab: la scheda della richiesta in cui si risolve ("" = nessuna).
	Tab string `json:"tab,omitempty"`
	// Mine: l'azione tocca a chi guarda.
	Mine bool `json:"mine,omitempty"`
}

type sumDecision struct {
	RequestID string   `json:"request_id"`
	Stage     string   `json:"stage"`
	Amount    *int64   `json:"amount_cents"`
	Waiting   []string `json:"waiting"` // chi deve ancora decidere
	StepsDone int      `json:"steps_done"`
	StepsAll  int      `json:"steps_all"`
	CanDecide bool     `json:"can_decide"`
	Inline    bool     `json:"inline"`
}

type sumScore struct {
	Value     *float64 `json:"value"`
	Version   *int     `json:"version"`
	Stale     bool     `json:"stale"`
	Scorecard string   `json:"scorecard,omitempty"`
}

type sumRank struct {
	InRanking     bool     `json:"in_ranking"`
	Position      int      `json:"position"`
	Total         int      `json:"total"`
	Cut           *int     `json:"cut"`
	Funded        *bool    `json:"funded"`
	Pinned        bool     `json:"pinned"`
	EstimateCents *int64   `json:"estimate_cents"`
	Cumulative    int64    `json:"cumulative_cents"`
	BudgetCents   *int64   `json:"budget_cents"`
	ValuePerCost  *float64 `json:"value_per_cost"`
	Mandatory     bool     `json:"mandatory"`
	// Area: la graduatoria ristretta all'area richiedente (e al periodo), quando c'è.
	Area *sumRankArea `json:"area"`
}

type sumRankArea struct {
	UnitID        string `json:"unit_id"`
	Unit          string `json:"unit"`
	PeriodID      string `json:"period_id,omitempty"`
	Period        string `json:"period,omitempty"`
	Position      int    `json:"position"`
	Total         int    `json:"total"`
	Cut           *int   `json:"cut"`
	Funded        *bool  `json:"funded"`
	HasAllocation bool   `json:"has_allocation"`
	Allocated     int64  `json:"allocated_cents"`
	Residual      int64  `json:"residual_cents"`
	// FitsResidual: la stima della richiesta sta nel residuo (nil senza stima o senza stanziamento).
	FitsResidual *bool `json:"fits_residual"`
}

type sumReview struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Team      string `json:"team"`
	Stage     string `json:"stage"`
	Mandatory bool   `json:"mandatory"`
	Missing   int    `json:"required_missing"`
	LateDays  int    `json:"late_days"`
	Impact    string `json:"impact,omitempty"`
}

type sumReviews struct {
	Total     int         `json:"total"`
	Open      int         `json:"open"`
	Mandatory int         `json:"mandatory_open"`
	Items     []sumReview `json:"items"`
}

type sumRequirements struct {
	Total    int      `json:"total"`
	Accept   int      `json:"to_accept"`
	Open     int      `json:"open"`
	ToVerify int      `json:"to_verify"`
	Verified int      `json:"verified"`
	Waived   int      `json:"waived"`
	Expired  int      `json:"expired_exceptions"`
	Ready    bool     `json:"ready"`
	Top      []sumReq `json:"top"`
}

type sumReq struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Application string `json:"application,omitempty"`
}

type sumRisk struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Phase string `json:"phase"`
	Band  string `json:"band,omitempty"`
	Tone  string `json:"tone,omitempty"`
	Over  bool   `json:"over_appetite"`
	Via   string `json:"via,omitempty"` // la chiave dell'applicazione o della richiesta a cui è legato
}

type requestSummaryOut struct {
	Key          string           `json:"key"`
	Decision     *sumDecision     `json:"decision"`
	Score        *sumScore        `json:"score"`
	Rank         *sumRank         `json:"rank"`
	Reviews      *sumReviews      `json:"reviews"`
	Requirements *sumRequirements `json:"requirements"`
	Risks        []sumRisk        `json:"risks"`
	Impacts      int              `json:"impacts"`
	DaysInStage  *int             `json:"days_in_stage"`
	Missing      []SummaryAction  `json:"missing"`
}

func (s *Server) requestSummary(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	budgetJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*requestSummaryOut, error) {
		return s.buildRequestSummary(r, p, tx)
	})
}

func (s *Server) buildRequestSummary(r *http.Request, p *auth.Principal, tx pgx.Tx) (*requestSummaryOut, error) {
	ctx := r.Context()
	actor := actorOf(r, p)
	el, err := s.eng.Get(ctx, tx, actor, r.PathValue("ref"))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != demand.TypeKey {
		return nil, engine.InvalidCode("validation.scheda.summary_of_request", "ref", "la sintesi è di una richiesta")
	}
	off, err := s.modulesOff(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	out := &requestSummaryOut{Key: el.Key, Risks: []sumRisk{}, Missing: []SummaryAction{}}
	closed := el.StateCategory == "done" || el.StateCategory == "cancelled"

	// Giorni nella fase: dall'ultimo passaggio di fase.
	since := el.CreatedAt
	var last *time.Time
	if err := tx.QueryRow(ctx, `select max(created_at) from activities where element_id = $1 and kind = 'state'`, el.ID).Scan(&last); err == nil && last != nil {
		since = *last
	}
	days := int(time.Since(since).Hours() / 24)
	out.DaysInStage = &days

	// Decisione in attesa.
	if apr, err := s.approvals.ForElement(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else {
		for _, ar := range apr {
			if ar.Status != "pending" {
				continue
			}
			d := &sumDecision{RequestID: ar.ID, Stage: ar.Stage, Amount: ar.AmountCents, Waiting: []string{}, StepsAll: len(ar.Steps), Inline: true}
			for _, st := range ar.Steps {
				if st.Decision != "pending" && st.Decision != "" {
					d.StepsDone++
					continue
				}
				d.Waiting = append(d.Waiting, st.Approver)
				if st.CanDecide {
					d.CanDecide = true
					d.Inline = d.Inline && st.InlineDecision
				}
			}
			out.Decision = d
			who := strings.Join(d.Waiting, ", ")
			out.Missing = append(out.Missing, SummaryAction{Kind: "decision", Detail: ar.Stage, Who: who, Tab: "approvazioni", Mine: d.CanDecide})
			break
		}
	}

	dv, err := s.demand.View(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}

	// Graduatoria, linea di taglio, budget e stanziamento dell'area: le stesse funzioni della pagina Graduatoria.
	if !closed {
		rk, err := s.rankingScoped(r, p, tx, nil)
		if ok, err2 := soft(err); err2 != nil {
			return nil, err2
		} else if ok {
			out.Rank = &sumRank{Total: len(rk.Items), Cut: rk.Cut, BudgetCents: rk.BudgetCents}
			for _, it := range rk.Items {
				if it.ID != el.ID {
					continue
				}
				out.Rank.InRanking, out.Rank.Position, out.Rank.Funded, out.Rank.Pinned = true, it.Position, it.Funded, it.Pinned
				out.Rank.EstimateCents, out.Rank.Cumulative, out.Rank.ValuePerCost, out.Rank.Mandatory = it.EstimateCents, it.CumulativeCents, it.ValuePerCost, it.IsMandatory
				out.Score = &sumScore{Value: it.Score, Version: it.ScoreVersion, Stale: it.ScoreStale}
			}
			if rk.Scorecard != nil && out.Score != nil {
				out.Score.Scorecard = rk.Scorecard.Name
			}
			if out.Rank.InRanking && dv.RequestingUnitID != nil {
				area, err := s.requestArea(r, p, tx, el.ID, *dv.RequestingUnitID, dv.BudgetPeriodID, out.Rank.EstimateCents)
				if err != nil {
					return nil, err
				}
				out.Rank.Area = area
			}
		}
	}
	if out.Score == nil && dv.Score != nil {
		out.Score = &sumScore{Value: dv.Score}
	}
	if out.Score == nil || out.Score.Value == nil {
		if !closed {
			out.Missing = append(out.Missing, SummaryAction{Kind: "score", Tab: "dettagli"})
		}
	}
	if !closed && dv.EstimateLowCents == nil && dv.EstimateHighCents == nil {
		out.Missing = append(out.Missing, SummaryAction{Kind: "estimate", Tab: "dettagli"})
	}

	// Valutazioni (questionari).
	if !off["demand"] {
		er, err := s.reviews.ForElement(ctx, tx, actor, el.ID)
		if ok, err2 := soft(err); err2 != nil {
			return nil, err2
		} else if ok {
			rv := &sumReviews{Items: []sumReview{}}
			for _, it := range er.Items {
				if it.Superseded {
					continue
				}
				rv.Total++
				if it.StateCategory == "done" || it.StateCategory == "cancelled" || it.Stage == "concluded" || it.Stage == "cancelled" {
					continue
				}
				rv.Open++
				if it.Mandatory {
					rv.Mandatory++
				}
				if len(rv.Items) < 5 {
					x := sumReview{Key: it.Key, Title: it.Title, Type: it.TypeName, Team: it.Team, Stage: it.Stage, Mandatory: it.Mandatory, Missing: it.RequiredMissing, LateDays: it.LateDays}
					if it.Application != nil {
						x.Impact = *it.Application
					}
					rv.Items = append(rv.Items, x)
				}
			}
			out.Reviews = rv
			if rv.Open > 0 && !closed {
				teams := []string{}
				for _, it := range rv.Items {
					if it.Team != "" && !slices.Contains(teams, it.Team) {
						teams = append(teams, it.Team)
					}
				}
				out.Missing = append(out.Missing, SummaryAction{Kind: "reviews", Count: rv.Open, Who: strings.Join(teams, ", "), Tab: "valutazioni"})
			}
		}
	}

	// Requisiti: la stessa copertura che pilota i gate.
	if sum, _, err := s.req.Coverage(ctx, tx, el); err == nil {
		rq := &sumRequirements{Total: sum.Total, Open: sum.Open, ToVerify: sum.ToVerify, Verified: sum.Verified, Waived: sum.Waived,
			Expired: sum.ExpiredExceptions, Ready: sum.Ready, Top: []sumReq{}}
		for _, m := range sum.Missing {
			if m.Status == "proposed" {
				rq.Accept++
			}
			if len(rq.Top) < 5 {
				rq.Top = append(rq.Top, sumReq{Key: m.Key, Title: m.Title, Status: m.Status, Application: m.Application})
			}
		}
		// «Aperti» comprende quelli da accettare: si mostrano separati.
		rq.Open -= rq.Accept
		out.Requirements = rq
		if rq.Accept > 0 {
			out.Missing = append(out.Missing, SummaryAction{Kind: "req_accept", Count: rq.Accept, Tab: "requisiti"})
		}
		if rq.Open > 0 {
			out.Missing = append(out.Missing, SummaryAction{Kind: "req_open", Count: rq.Open, Tab: "requisiti"})
		}
		if rq.ToVerify > 0 {
			out.Missing = append(out.Missing, SummaryAction{Kind: "req_verify", Count: rq.ToVerify, Tab: "requisiti"})
		}
	} else if _, e := soft(err); e != nil {
		return nil, e
	}

	// Impatti e rischi: quelli della richiesta e delle applicazioni toccate dai suoi impatti.
	apps, err := s.requestApps(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	out.Impacts = len(apps)
	if !off["grc"] {
		if out.Risks, err = s.risksLinked(ctx, tx, actor, append([]string{el.ID}, appIDs(apps)...), apps); err != nil {
			return nil, err
		}
		for _, rk := range out.Risks {
			if rk.Over {
				out.Missing = append(out.Missing, SummaryAction{Kind: "risk_over", Detail: rk.Key + " " + rk.Title, Tab: ""})
				break
			}
		}
	}

	// I blocchi del motore sul passaggio in avanti non già coperti sopra.
	if tr, err := s.eng.Available(ctx, tx, actor, el); err == nil {
		for _, t := range tr {
			if !t.Forward {
				continue
			}
			for _, b := range t.Blocked {
				out.Missing = append(out.Missing, SummaryAction{Kind: "blocked", Detail: b, Who: t.Label})
			}
			break
		}
	}
	return out, nil
}

type reqApp struct {
	ID  string
	Key string
}

func appIDs(a []reqApp) []string {
	ids := make([]string, len(a))
	for i, x := range a {
		ids[i] = x.ID
	}
	return ids
}

// requestApps: le applicazioni degli impatti della richiesta (quelle che l'attore può leggere).
func (s *Server) requestApps(ctx context.Context, tx pgx.Tx, requestID string) ([]reqApp, error) {
	rows, err := tx.Query(ctx, `select distinct a.id::text, sp.key || '-' || a.number
		from ext_impact x
		join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.element_id and ie.deleted_at is null
		left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
		join elements a on a.tenant_id = x.tenant_id and a.id = x.application_id and a.deleted_at is null
		join spaces sp on sp.tenant_id = a.tenant_id and sp.id = a.space_id
		where x.request_id = $1 and coalesce(io.category, '') <> 'cancelled'
		order by 2 limit 12`, requestID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (reqApp, error) {
		var a reqApp
		return a, r.Scan(&a.ID, &a.Key)
	})
}

// risksLinked: i rischi legati agli elementi indicati (con fascia del residuo), da una lettura sola del registro.
func (s *Server) risksLinked(ctx context.Context, tx pgx.Tx, actor engine.Actor, ids []string, apps []reqApp) ([]sumRisk, error) {
	out := []sumRisk{}
	via := map[string]string{} // chiave del rischio -> elemento a cui è legato
	for _, id := range ids {
		el, err := s.eng.Get(ctx, tx, actor, id)
		if err != nil {
			if ok, e := soft(err); e != nil {
				return nil, e
			} else if !ok {
				continue
			}
		}
		links, err := s.eng.ElementLinks(ctx, tx, actor, el)
		if err != nil {
			return nil, err
		}
		for _, l := range links.Relations {
			if l.Type == risk.RiskType {
				if _, dup := via[l.Key]; !dup {
					via[l.Key] = el.Key
				}
			}
		}
	}
	if len(via) == 0 || s.risk == nil {
		return out, nil
	}
	reg, err := s.risk.Risks(ctx, tx, actor, risk.RiskFilter{})
	if ok, e := soft(err); e != nil {
		return nil, e
	} else if !ok {
		return out, nil
	}
	for _, it := range reg.Items {
		v, ok := via[it.Key]
		if !ok || it.PhaseCategory == "done" || it.PhaseCategory == "cancelled" {
			continue
		}
		x := sumRisk{Key: it.Key, Title: it.Title, Phase: it.Phase, Via: v}
		sc := it.Residual
		if sc == nil {
			sc = it.Inherent
		}
		if sc != nil {
			x.Band, x.Tone, x.Over = sc.BandName, sc.Tone, sc.Over
		}
		out = append(out, x)
	}
	return out, nil
}

// requestArea restringe la graduatoria all'area richiedente e al periodo (quello della richiesta oppure quello
// in corso) e legge lo stanziamento: è la stessa funzione della pagina Graduatoria con area e periodo scelti.
func (s *Server) requestArea(r *http.Request, p *auth.Principal, tx pgx.Tx, requestID, unitID string, periodID *string, estimate *int64) (*sumRankArea, error) {
	ctx := r.Context()
	period := ""
	if periodID != nil {
		period = *periodID
	} else if per, err := s.fin.Periods(ctx, tx, ""); err == nil {
		today := time.Now().Format(time.DateOnly)
		var open, last string
		for _, pr := range per {
			if pr.Status != "closed" && pr.Start <= today && pr.End >= today {
				period = pr.ID
				break
			}
			if open == "" && (pr.Status == "open" || pr.Status == "closing") {
				open = pr.ID
			}
			last = pr.ID
		}
		if period == "" {
			period = open
		}
		if period == "" {
			period = last
		}
	}
	r2 := r.Clone(ctx)
	r2.URL = &url.URL{Path: r.URL.Path, RawQuery: url.Values{"unit": {unitID}, "period": {period}}.Encode()}
	rk, err := s.rankingScoped(r2, p, tx, nil)
	if ok, e := soft(err); e != nil {
		return nil, e
	} else if !ok || rk.Scope == nil {
		return nil, nil
	}
	a := &sumRankArea{UnitID: unitID, Unit: rk.Scope.Unit, PeriodID: rk.Scope.PeriodID, Period: rk.Scope.Period, Total: len(rk.Items), Cut: rk.Cut,
		HasAllocation: rk.Scope.HasAllocation, Allocated: rk.Scope.AllocatedCents, Residual: rk.Scope.ResidualCents}
	for _, it := range rk.Items {
		if it.ID == requestID {
			a.Position, a.Funded = it.Position, it.Funded
		}
	}
	if a.Position == 0 {
		return nil, nil
	}
	if a.HasAllocation && estimate != nil {
		f := *estimate <= a.Residual
		a.FitsResidual = &f
	}
	return a, nil
}

// ---------------------------------------------------------------------
// Applicazione 360

type a360Owner struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type a360Node struct {
	Key         string  `json:"key"`
	Code        *string `json:"code"`
	Name        string  `json:"name"`
	Criticality *string `json:"criticality"`
	Via         string  `json:"via"`
}

type a360Deps struct {
	Dependencies []a360Node `json:"dependencies"`
	Dependents   []a360Node `json:"dependents"`
}

type a360Impact struct {
	Key             string `json:"key"`
	Title           string `json:"title"`
	State           string `json:"state"`
	StateCategory   string `json:"state_category"`
	RequestKey      string `json:"request_key"`
	RequestTitle    string `json:"request_title"`
	RequestState    string `json:"request_state"`
	RequestCategory string `json:"request_category"`
}

type a360Vuln struct {
	ID       string     `json:"id"`
	CVE      string     `json:"cve"`
	Severity string     `json:"severity"`
	Package  *string    `json:"package"`
	DueAt    *time.Time `json:"due_at"`
	DaysLeft *int       `json:"days_left"`
	Overdue  bool       `json:"overdue"`
}

type a360Vulns struct {
	Open         int        `json:"open"`
	Overdue      int        `json:"overdue"`
	Soon         int        `json:"soon"`
	CriticalOpen int        `json:"critical_open"`
	CriticalProd int        `json:"critical_prod"`
	Exceptions   int        `json:"exceptions"`
	Items        []a360Vuln `json:"items"`
}

type a360Release struct {
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	Production  bool      `json:"production"`
	Version     string    `json:"version"`
	At          time.Time `json:"at"`
	Status      string    `json:"checks_status"`
	Request     *string   `json:"request_key"`
}

type a360Releases struct {
	Services   int           `json:"services"`
	OnPipeline int           `json:"on_pipeline"`
	Stale      int           `json:"stale"`
	Unscanned  int           `json:"unscanned"`
	Latest     []a360Release `json:"latest"`
}

type a360GateCell struct {
	Environment string `json:"environment"`
	Production  bool   `json:"production"`
	Result      string `json:"result"` // pass | warn | fail | "" (nessuna decisione)
	Mode        string `json:"mode"`
	Version     string `json:"version,omitempty"`
	Reasons     int    `json:"failing_reasons"`
}

type a360Gate struct {
	Configured bool           `json:"configured"`
	Cells      []a360GateCell `json:"cells"`
	Failing    int            `json:"failing"`
	Overrides  int            `json:"overrides_pending"`
}

type a360Pipeline struct {
	Configured bool     `json:"configured"`
	Status     string   `json:"status"`
	Class      string   `json:"class"`
	Missing    []string `json:"missing"`
	Services   int      `json:"services"`
}

type a360Control struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Effectiveness string `json:"effectiveness"`
	Valid         bool   `json:"valid"`
}

type a360Framework struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Worst   string `json:"worst"`
	Percent int    `json:"percent"`
	Gaps    int    `json:"gaps"`
}

type a360Compliance struct {
	Applicable int             `json:"applicable"`
	Covered    int             `json:"covered"`
	Percent    int             `json:"percent"`
	Gaps       int             `json:"gaps"` // requisiti senza controllo o senza evidenza valida
	Frameworks []a360Framework `json:"frameworks"`
	Controls   []a360Control   `json:"controls"`
	Invalid    int             `json:"controls_invalid"`
}

type a360Incident struct {
	Key           string    `json:"key"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	StateCategory string    `json:"state_category"`
	DetectedAt    time.Time `json:"detected_at"`
	Major         bool      `json:"major"`
}

type a360Doc struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Space      string `json:"space"`
	Controlled bool   `json:"controlled"`
}

type a360Out struct {
	Key             string          `json:"key"`
	ID              string          `json:"id"`
	Owners          []a360Owner     `json:"owners"`
	Team            *string         `json:"team"`
	TeamLead        *string         `json:"team_lead"`
	Actions         []SummaryAction `json:"actions"`
	Deps            *a360Deps       `json:"deps"`
	Requests        []a360Impact    `json:"requests"`
	RequestsOpen    int             `json:"requests_open"`
	Vulns           *a360Vulns      `json:"vulns"`
	Releases        *a360Releases   `json:"releases"`
	Gate            *a360Gate       `json:"gate"`
	Pipeline        *a360Pipeline   `json:"pipeline"`
	Compliance      *a360Compliance `json:"compliance"`
	Risks           []sumRisk       `json:"risks"`
	RisksOpen       int             `json:"risks_open"`
	Incidents       []a360Incident  `json:"incidents"`
	IncidentsOpen   int             `json:"incidents_open"`
	Documents       []a360Doc       `json:"documents"`
	HasRisks        bool            `json:"has_risks"`
	HasIncidents    bool            `json:"has_incidents"`
	HasDocuments    bool            `json:"has_documents"`
	HasCompliance   bool            `json:"has_compliance"`
	HasDevsecops    bool            `json:"has_devsecops"`
	HasArchitecture bool            `json:"has_architecture"`
}

func (s *Server) application360(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Finance: p.Can(rbac.FinanceRead), Member: p.MemberID}, func(tx pgx.Tx) error {
		out, err := s.buildApplication360(r.Context(), p, actorOf(r, p), tx, r.PathValue("ref"))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	})
	if err != nil {
		s.engineError(w, r, err)
	}
}

func isOpenCat(c string) bool { return c != "done" && c != "cancelled" }

func (s *Server) buildApplication360(ctx context.Context, p *auth.Principal, actor engine.Actor, tx pgx.Tx, ref string) (*a360Out, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != architecture.AppType {
		return nil, engine.InvalidCode("validation.scheda.view360_application", "ref", "la vista 360 è di un'applicazione")
	}
	off, err := s.modulesOff(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	out := &a360Out{Key: el.Key, ID: el.ID, Owners: []a360Owner{}, Actions: []SummaryAction{}, Requests: []a360Impact{}, Risks: []sumRisk{},
		Incidents: []a360Incident{}, Documents: []a360Doc{}}
	add := func(a SummaryAction) { out.Actions = append(out.Actions, a) }

	// Responsabili: titolari dei ruoli sull'elemento, e il team con il suo responsabile.
	if sh, err := engine.Stakeholders(ctx, tx, el.ID); err == nil {
		for _, x := range sh {
			out.Owners = append(out.Owners, a360Owner{Role: x.RoleName, Name: x.Name})
		}
	}
	if app, err := s.arch.Application(ctx, tx, el.ID); err == nil && app != nil {
		out.Team = app.Team
		if app.TeamID != nil {
			var lead *string
			if err := tx.QueryRow(ctx, `select m.display_name from teams t left join members m on m.tenant_id = t.tenant_id and m.id = t.lead_id where t.id = $1`, *app.TeamID).Scan(&lead); err == nil {
				out.TeamLead = lead
			}
		}
	}
	if out.Team == nil && len(out.Owners) == 0 {
		add(SummaryAction{Kind: "no_owner", Tab: "dettagli"})
	}

	// Dipendenze: un livello in ciascun verso, la mappa piccola.
	if !off["architecture"] {
		deps := &a360Deps{Dependencies: []a360Node{}, Dependents: []a360Node{}}
		for _, dir := range []string{"dependencies", "dependents"} {
			res, err := s.arch.Impact(ctx, tx, actor, el.ID, dir, 1)
			if ok, e := soft(err); e != nil {
				return nil, e
			} else if !ok {
				continue
			}
			for _, it := range res.Items {
				if it.Level != 1 || it.Type != architecture.AppType {
					continue
				}
				n := a360Node{Key: it.Key, Code: it.Code, Name: it.Name, Criticality: it.Criticality, Via: it.Via}
				if dir == "dependencies" {
					deps.Dependencies = append(deps.Dependencies, n)
				} else {
					deps.Dependents = append(deps.Dependents, n)
				}
			}
		}
		out.Deps, out.HasArchitecture = deps, true
	}

	// Richieste e impatti aperti sull'applicazione.
	rows, err := tx.Query(ctx, `select ie.id::text, isp.key || '-' || ie.number, ie.title, coalesce(io.name, ''), coalesce(io.category, ''),
		rs.key || '-' || rq.number, rq.title, coalesce(ro.name, ''), coalesce(ro.category, '')
		from ext_impact x
		join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.element_id and ie.deleted_at is null and not ie.is_draft
		join spaces isp on isp.tenant_id = ie.tenant_id and isp.id = ie.space_id
		left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
		join elements rq on rq.tenant_id = x.tenant_id and rq.id = x.request_id and rq.deleted_at is null
		join spaces rs on rs.tenant_id = rq.tenant_id and rs.id = rq.space_id
		left join options ro on ro.tenant_id = rq.tenant_id and ro.id = rq.state_id
		where x.application_id = $1 and coalesce(io.category, '') not in ('done', 'cancelled') and coalesce(ro.category, '') not in ('done', 'cancelled')
		order by ie.created_at desc limit 40`, el.ID)
	if err != nil {
		return nil, err
	}
	type impRow struct {
		id string
		x  a360Impact
	}
	var imps []impRow
	for rows.Next() {
		var ir impRow
		if err := rows.Scan(&ir.id, &ir.x.Key, &ir.x.Title, &ir.x.State, &ir.x.StateCategory, &ir.x.RequestKey, &ir.x.RequestTitle, &ir.x.RequestState, &ir.x.RequestCategory); err != nil {
			rows.Close()
			return nil, err
		}
		imps = append(imps, ir)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, ir := range imps {
		if ok, err := s.eng.CanRead(ctx, tx, actor, ir.id); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		out.RequestsOpen++
		if len(out.Requests) < 5 {
			out.Requests = append(out.Requests, ir.x)
		}
	}

	// DevSecOps: vulnerabilità e SLA, rilasci, gate, aderenza.
	if !off["devsecops"] && s.dso != nil {
		out.HasDevsecops = true
		dash, err := s.dso.VulnDashboard(ctx, tx, actor, devsecops.VulnFilter{Application: el.ID})
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok {
			v := &a360Vulns{Open: dash.KPIs.Open, Overdue: dash.KPIs.Overdue, Soon: dash.KPIs.Soon, CriticalOpen: dash.KPIs.CriticalOpen,
				CriticalProd: dash.KPIs.CriticalProd, Exceptions: dash.KPIs.Exceptions, Items: []a360Vuln{}}
			list, err := s.dso.ListVulns(ctx, tx, actor, devsecops.VulnFilter{Application: el.ID, Stage: "open", Limit: 5})
			if err != nil {
				return nil, err
			}
			for _, it := range list.Items {
				v.Items = append(v.Items, a360Vuln{ID: it.ID, CVE: it.CVE, Severity: it.Severity, Package: it.Package, DueAt: it.DueAt, DaysLeft: it.DaysLeft, Overdue: it.Overdue})
			}
			out.Vulns = v
			if v.Overdue > 0 {
				add(SummaryAction{Kind: "vuln_overdue", Count: v.Overdue, Tab: "produzione"})
			}
			if v.CriticalOpen > 0 {
				add(SummaryAction{Kind: "vuln_critical", Count: v.CriticalOpen, Tab: "produzione"})
			}
		}
		panel, err := s.dso.Panel(ctx, tx, actor, el.ID)
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok {
			rl := &a360Releases{Services: panel.KPIs.Services, OnPipeline: panel.KPIs.OnPipeline, Stale: panel.KPIs.Stale.Now, Unscanned: panel.KPIs.Unscanned, Latest: []a360Release{}}
			for _, g := range panel.Changes {
				for _, x := range g.Releases {
					a := a360Release{Service: x.Service, Environment: x.Environment, Production: x.Production, Version: x.Version, At: x.At, Status: x.ChecksStatus}
					if x.Change != nil {
						a.Request = x.Change.RequestKey
					}
					rl.Latest = append(rl.Latest, a)
				}
			}
			slices.SortFunc(rl.Latest, func(a, b a360Release) int { return b.At.Compare(a.At) })
			if len(rl.Latest) > 4 {
				rl.Latest = rl.Latest[:4]
			}
			out.Releases = rl
			if rl.Services == 0 {
				add(SummaryAction{Kind: "no_service", Tab: "produzione"})
			}
		}
		gp, err := s.dso.GatePanel(ctx, tx, actor, el.ID)
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok {
			g := &a360Gate{Configured: gp.Configured, Cells: []a360GateCell{}}
			for _, c := range gp.Cells {
				cell := a360GateCell{Environment: c.Environment, Production: c.Production}
				if c.Outcome != nil {
					cell.Result, cell.Mode, cell.Version = c.Outcome.Result, c.Outcome.Mode, c.Outcome.Version
					for _, rs := range c.Outcome.Reasons {
						if rs.Status == "fail" {
							cell.Reasons++
						}
					}
				}
				if cell.Result == "fail" {
					g.Failing++
				}
				g.Cells = append(g.Cells, cell)
			}
			for _, o := range gp.Overrides {
				if o.Status == "pending" {
					g.Overrides++
				}
			}
			out.Gate = g
			if g.Failing > 0 {
				add(SummaryAction{Kind: "gate_fail", Count: g.Failing, Tab: "gate"})
			}
			if g.Overrides > 0 {
				add(SummaryAction{Kind: "gate_override", Count: g.Overrides, Tab: "gate"})
			}
		}
		pv, err := s.dso.PipelineOf(ctx, tx, actor, el.ID)
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok && len(pv.Applications) > 0 {
			a := pv.Applications[0].Application
			pl := &a360Pipeline{Configured: pv.Configured, Status: a.Status, Class: a.Class, Missing: []string{}, Services: len(a.Services)}
			for _, c := range a.Required {
				if c.Status == "missing" || c.Status == "weaker" {
					pl.Missing = append(pl.Missing, c.Name)
				}
			}
			out.Pipeline = pl
			if pl.Configured && (a.Status == devsecops.StOff || a.Status == devsecops.StPartial) {
				add(SummaryAction{Kind: "pipeline_gap", Count: len(pl.Missing), Detail: strings.Join(pl.Missing, ", "), Tab: "produzione"})
			}
		}
	}

	// Conformità: controlli e normative del perimetro.
	if !off["grc"] && s.grc != nil {
		out.HasCompliance = true
		sc, err := s.grc.ScopeCompliance(ctx, tx, actor, "application", el.Key)
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok {
			c := &a360Compliance{Applicable: sc.Counts.Applicable, Covered: sc.Counts.Covered, Percent: sc.Counts.Percent,
				Gaps: sc.Counts.NoControl + sc.Counts.NoEvidence + sc.Counts.Partial, Frameworks: []a360Framework{}, Controls: []a360Control{}}
			seen := map[string]int{}
			for _, f := range sc.Frameworks {
				c.Frameworks = append(c.Frameworks, a360Framework{Key: f.Key, Name: f.Name, Worst: f.Worst, Percent: f.Percent, Gaps: f.NoControl + f.NoEvidence + f.Partial})
				for _, it := range f.Items {
					for _, ct := range it.Controls {
						valid := false
						for _, ev := range ct.Evidence {
							if ev.Status == "valid" || ev.Status == "expiring" {
								valid = true
							}
						}
						if i, dup := seen[ct.Key]; dup {
							c.Controls[i].Valid = c.Controls[i].Valid || valid
							continue
						}
						seen[ct.Key] = len(c.Controls)
						c.Controls = append(c.Controls, a360Control{Key: ct.Key, Title: ct.Title, Effectiveness: ct.Effectiveness, Valid: valid})
					}
				}
			}
			for _, ct := range c.Controls {
				if !ct.Valid {
					c.Invalid++
				}
			}
			slices.SortStableFunc(c.Controls, func(a, b a360Control) int {
				if a.Valid != b.Valid {
					if !a.Valid {
						return -1
					}
					return 1
				}
				return 0
			})
			if len(c.Controls) > 6 {
				c.Controls = c.Controls[:6]
			}
			out.Compliance = c
			if c.Invalid > 0 {
				add(SummaryAction{Kind: "controls_invalid", Count: c.Invalid, Tab: "dettagli"})
			}
			if c.Gaps > 0 && c.Invalid == 0 {
				add(SummaryAction{Kind: "compliance_gap", Count: c.Gaps, Tab: "dettagli"})
			}
		}
		// Rischi e incidenti legati all'applicazione.
		out.HasRisks, out.HasIncidents = true, true
		if s.risk != nil {
			reg, err := s.risk.Risks(ctx, tx, actor, risk.RiskFilter{Target: el.Key})
			if ok, e := soft(err); e != nil {
				return nil, e
			} else if ok {
				for _, it := range reg.Items {
					if !isOpenCat(it.PhaseCategory) {
						continue
					}
					out.RisksOpen++
					x := sumRisk{Key: it.Key, Title: it.Title, Phase: it.Phase, Via: el.Key}
					sc := it.Residual
					if sc == nil {
						sc = it.Inherent
					}
					if sc != nil {
						x.Band, x.Tone, x.Over = sc.BandName, sc.Tone, sc.Over
					}
					out.Risks = append(out.Risks, x)
				}
				slices.SortStableFunc(out.Risks, func(a, b sumRisk) int {
					if a.Over != b.Over {
						if a.Over {
							return -1
						}
						return 1
					}
					return 0
				})
				if len(out.Risks) > 5 {
					out.Risks = out.Risks[:5]
				}
				for _, x := range out.Risks {
					if x.Over {
						add(SummaryAction{Kind: "risk_over", Detail: x.Key + " " + x.Title})
						break
					}
				}
			}
			irows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''), x.detected_at, x.is_major
				from relations r
				join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
				join ext_incident x on x.tenant_id = r.tenant_id and x.element_id = r.source_id
				join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
				join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
				left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
				where r.target_id = $1 order by x.detected_at desc limit 40`, el.ID, risk.RelImpacts)
			if err != nil {
				return nil, err
			}
			type incRow struct {
				id string
				x  a360Incident
			}
			var incs []incRow
			for irows.Next() {
				var ir incRow
				if err := irows.Scan(&ir.id, &ir.x.Key, &ir.x.Title, &ir.x.State, &ir.x.StateCategory, &ir.x.DetectedAt, &ir.x.Major); err != nil {
					irows.Close()
					return nil, err
				}
				incs = append(incs, ir)
			}
			irows.Close()
			if err := irows.Err(); err != nil {
				return nil, err
			}
			for _, ir := range incs {
				if !isOpenCat(ir.x.StateCategory) {
					continue
				}
				if ok, err := s.eng.CanRead(ctx, tx, actor, ir.id); err != nil {
					return nil, err
				} else if !ok {
					continue
				}
				out.IncidentsOpen++
				if len(out.Incidents) < 4 {
					out.Incidents = append(out.Incidents, ir.x)
				}
			}
			if out.IncidentsOpen > 0 {
				add(SummaryAction{Kind: "incident_open", Count: out.IncidentsOpen, Detail: out.Incidents[0].Key + " " + out.Incidents[0].Title})
			}
		}
	}

	// Documenti collegati.
	if !off["knowledge"] && s.kb != nil {
		out.HasDocuments = true
		docs, err := s.kb.Documents(ctx, tx, actor, el.ID)
		if ok, e := soft(err); e != nil {
			return nil, e
		} else if ok {
			for _, d := range docs {
				if len(out.Documents) < 5 {
					out.Documents = append(out.Documents, a360Doc{Key: d.Key, Title: d.Title, Space: d.SpaceName, Controlled: d.Controlled})
				}
			}
		}
	}
	return out, nil
}
