package requirements

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Copertura dei requisiti: le celle della matrice (requisito × impatto) con lo stato
// calcolato. Servono ai fatti delle condizioni, al gate di rilascio, alla matrice e
// all'API delle pipeline: una sola lettura, piccola (decine di righe per richiesta).

// cellRow è un requisito con una sua cella (o senza celle: requisito di progetto su una
// richiesta ancora senza impatti).
type cellRow struct {
	ReqID, ReqKey, Title     string
	Scope, Category, Stage   string
	Phase, Origin, Method    string
	ImpactID, ImpactKey      string
	Application, AppID       string
	TeamID, Team             string
	ImpactCat, ImpactDeleted string
	Status, Progress         string
	ExceptionID              *string
	ExceptionKey             string
	ExceptionStage           string
	ExpiresOn                *time.Time
	ImplementedByID          *string
	ImplementedBy            *string
	ImplementedAt            *time.Time
	VerifiedByID             *string
	VerifiedBy               *string
	VerifiedAt               *time.Time
	CellID                   string
}

const selectCells = `
	select x.element_id::text, rs.key || '-' || re.number, re.title, x.scope, x.category, x.stage, coalesce(ro.name, ''),
	       x.origin, x.verification_method,
	       coalesce(c.target_element_id::text, ''), coalesce(isp.key || '-' || ie.number, ''), coalesce(ap.title, ''), coalesce(ap.id::text, ''),
	       coalesce(t.id::text, ''), coalesce(t.name, ''), coalesce(io.category, ''), case when ie.deleted_at is null then '' else 'x' end,
	       coalesce(c.status, ''), coalesce(c.progress, ''), c.exception_id::text,
	       coalesce(es.key || '-' || ee.number, ''), coalesce(ex.stage, ''), ex.expires_on,
	       c.implemented_by::text, im.display_name, c.implemented_at, c.verified_by::text, vm.display_name, c.verified_at, coalesce(c.id::text, '')
	from ext_requirement x
	join elements re on re.tenant_id = x.tenant_id and re.id = x.element_id and re.deleted_at is null
	join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id
	left join applicability c on c.tenant_id = x.tenant_id and c.requirement_element_id = x.element_id
	left join elements ie on ie.tenant_id = c.tenant_id and ie.id = c.target_element_id
	left join spaces isp on isp.tenant_id = ie.tenant_id and isp.id = ie.space_id
	left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
	left join ext_impact xi on xi.tenant_id = c.tenant_id and xi.element_id = c.target_element_id
	left join elements ap on ap.tenant_id = xi.tenant_id and ap.id = xi.application_id
	left join teams t on t.tenant_id = xi.tenant_id and t.id = xi.team_id
	left join elements ee on ee.tenant_id = c.tenant_id and ee.id = c.exception_id
	left join spaces es on es.tenant_id = ee.tenant_id and es.id = ee.space_id
	left join ext_exception ex on ex.tenant_id = c.tenant_id and ex.element_id = c.exception_id
	left join members im on im.tenant_id = c.tenant_id and im.id = c.implemented_by
	left join members vm on vm.tenant_id = c.tenant_id and vm.id = c.verified_by`

func (s *Service) loadCells(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]cellRow, error) {
	rows, err := tx.Query(ctx, selectCells+" where "+where+" order by x.scope desc, re.number, ie.number", args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[cellRow])
}

// hasCell: la riga ha una cella su un impatto esistente.
func (r cellRow) hasCell() bool { return r.CellID != "" && r.ImpactID != "" && r.ImpactDeleted == "" }

// relevant: la cella conta (impatto non annullato, requisito e cella applicabili).
func (r cellRow) relevant() bool {
	return r.hasCell() && r.ImpactCat != "cancelled" && r.Status != "not_applicable" && r.Stage != StageNotApplicable
}

// exceptionValid: la deroga della cella è in vigore e non scaduta.
func (r cellRow) exceptionValid(today time.Time) bool {
	return r.ExceptionStage == "approved" && r.ExpiresOn != nil && !r.ExpiresOn.Before(today)
}

// exceptionExpired: la cella aveva una deroga che è scaduta (e non è stata verificata).
func (r cellRow) exceptionExpired(today time.Time) bool {
	if r.ExceptionID == nil || r.Progress == "verified" {
		return false
	}
	return r.ExceptionStage == "expired" || (r.ExceptionStage == "approved" && r.ExpiresOn != nil && r.ExpiresOn.Before(today))
}

// status è lo stato della cella: proposed, open, to_verify, verified, waived, not_applicable.
// Una deroga scaduta conta già come aperta, anche prima che il job la chiuda.
func (r cellRow) status(today time.Time) string {
	switch {
	case r.Status == "not_applicable" || r.Stage == StageNotApplicable:
		return "not_applicable"
	case r.Progress == "verified":
		return "verified"
	case r.Progress == "waived" && r.exceptionValid(today):
		return "waived"
	case r.Progress == "to_verify":
		return "to_verify"
	case r.Stage == StageProposed:
		return "proposed"
	}
	return "open"
}

// stageStatus: lo stato di un requisito senza celle, dal suo stadio.
func stageStatus(stage string) string {
	switch stage {
	case StageProposed:
		return "proposed"
	case StageToVerify:
		return "to_verify"
	case StageVerified:
		return "verified"
	case StageWaived:
		return "waived"
	case StageNotApplicable:
		return "not_applicable"
	}
	return "open"
}

var statusRank = map[string]int{"proposed": 0, "open": 1, "to_verify": 2, "waived": 3, "verified": 4}

// StatusLabels sono le etichette degli stati delle celle (anche per il CSV).
var StatusLabels = map[string]string{
	"proposed": "Da accettare", "open": "Da realizzare", "to_verify": "Da verificare", "verified": "Verificato",
	"waived": "In deroga", "not_applicable": "Non applicabile",
}

// Summary sono i conteggi della copertura (fatti delle condizioni e API di gate).
type Summary struct {
	Total             int           `json:"requisiti"`
	Open              int           `json:"requisiti_aperti"`
	ToVerify          int           `json:"requisiti_da_verificare"`
	Verified          int           `json:"requisiti_verificati"`
	Waived            int           `json:"requisiti_in_deroga"`
	ExpiredExceptions int           `json:"deroghe_scadute"`
	Ready             bool          `json:"pronto_al_rilascio"`
	Missing           []MissingItem `json:"mancanti"`
	// MissingTotal: solo nella matrice a pagine, dove Missing si ferma ai primi MatrixMissing:
	// quanti sono in tutto (la pagina mostra solo il numero).
	MissingTotal int `json:"mancanti_totale,omitempty"`
}

// MissingItem è un requisito che ferma il rilascio.
type MissingItem struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
	Impact      string `json:"impact,omitempty"`
	Application string `json:"application,omitempty"`
}

// summarize conta per cella (perCell, l'impatto) o per requisito (la richiesta: lo stato
// del requisito è quello della sua cella peggiore).
func summarize(rows []cellRow, today time.Time, perCell bool) Summary {
	sum := Summary{Missing: []MissingItem{}}
	add := func(st string, r cellRow, expired bool) {
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
			m := MissingItem{Key: r.ReqKey, Title: r.Title, Status: st, StatusLabel: StatusLabels[st]}
			if perCell {
				m.Impact, m.Application = r.ImpactKey, r.Application
			}
			sum.Missing = append(sum.Missing, m)
		}
	}
	if perCell {
		for _, r := range rows {
			if r.relevant() {
				add(r.status(today), r, r.exceptionExpired(today))
			}
		}
	} else {
		type agg struct {
			row     cellRow
			status  string
			cells   int
			expired bool
		}
		var order []string
		byReq := map[string]*agg{}
		for _, r := range rows {
			a, ok := byReq[r.ReqID]
			if !ok {
				a = &agg{row: r, status: ""}
				byReq[r.ReqID] = a
				order = append(order, r.ReqID)
			}
			if !r.relevant() {
				continue
			}
			st := r.status(today)
			if a.cells == 0 || statusRank[st] < statusRank[a.status] {
				a.status = st
			}
			a.cells++
			a.expired = a.expired || r.exceptionExpired(today)
		}
		for _, id := range order {
			a := byReq[id]
			if a.row.Stage == StageNotApplicable {
				continue
			}
			if a.cells == 0 {
				// Senza celle conta solo un requisito di progetto (non ci sono ancora impatti).
				if a.row.Scope != "project" {
					continue
				}
				a.status = stageStatus(a.row.Stage)
			}
			add(a.status, a.row, a.expired)
		}
	}
	sum.Ready = sum.Open == 0 && sum.ToVerify == 0
	return sum
}

// Coverage legge la copertura di una richiesta o di un impatto.
func (s *Service) Coverage(ctx context.Context, tx pgx.Tx, el *engine.Element) (Summary, []cellRow, error) {
	var rows []cellRow
	var err error
	perCell := false
	switch el.TypeKey {
	case RequestType:
		rows, err = s.loadCells(ctx, tx, "x.request_id = $1", el.ID)
	case ImpactType:
		rows, err = s.loadCells(ctx, tx, "c.target_element_id = $1", el.ID)
		perCell = true
	default:
		return Summary{Missing: []MissingItem{}, Ready: true}, nil, nil
	}
	if err != nil {
		return Summary{}, nil, err
	}
	return summarize(rows, s.today(), perCell), rows, nil
}

func setCoverageFacts(f engine.Facts, sum Summary) {
	f.Set("requisiti", sum.Total)
	f.Set("requisiti_aperti", sum.Open)
	f.Set("requisiti_da_verificare", sum.ToVerify)
	f.Set("requisiti_verificati", sum.Verified)
	f.Set("requisiti_in_deroga", sum.Waived)
	f.Set("deroghe_scadute", sum.ExpiredExceptions)
	f.Set("pronto_al_rilascio", sum.Ready)
}

func (s *Service) requestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	sum, _, err := s.Coverage(ctx, tx, el)
	if err != nil {
		return err
	}
	setCoverageFacts(f, sum)
	return nil
}

func (s *Service) impactFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	return s.requestFacts(ctx, tx, el, f)
}

var originNames = map[string]string{"prescription": "prescrizione", "catalog": "catalogo", "manual": "manuale", "questionnaire": "questionario"}

func (s *Service) requirementFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var stage, scope, origin, category, method string
	err := tx.QueryRow(ctx, `select stage, scope, origin, category, verification_method from ext_requirement where element_id = $1`, el.ID).
		Scan(&stage, &scope, &origin, &category, &method)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	f.Set("stadio", stage)
	f.Set("ambito", scope, map[string]string{"project": "progetto", "application": "applicazione"}[scope])
	f.Set("origine", origin, originNames[origin])
	f.Set("categoria", category)
	f.Set("verifica", method, map[string]string{"manual": "manuale", "evidence": "evidenza"}[method])
	return nil
}

// releaseGuard è il controllo del gate di rilascio sull'impatto: tutti i requisiti
// applicabili verificati o coperti da una deroga valida. Il messaggio dice quali mancano.
func (s *Service) releaseGuard(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
	sum, _, err := s.Coverage(ctx, tx, el)
	if err != nil {
		return err
	}
	if sum.Ready {
		return nil
	}
	return engine.InvalidParams("validation.requirements.missing", "requisiti", map[string]any{"detail": missingMessage(sum.Missing)}, "%s", missingMessage(sum.Missing))
}

func missingMessage(ms []MissingItem) string {
	const max = 5
	parts := make([]string, 0, max)
	for i, m := range ms {
		if i == max {
			break
		}
		parts = append(parts, fmt.Sprintf("%s «%s» (%s)", m.Key, m.Title, strings.ToLower(m.StatusLabel)))
	}
	msg := fmt.Sprintf("requisiti non verificati né in deroga: %s", strings.Join(parts, ", "))
	if len(ms) > max {
		msg += fmt.Sprintf(" e altri %d", len(ms)-max)
	}
	return msg
}

// ---------------------------------------------------------------------------
// Matrice di tracciabilità

// MatrixColumn è un impatto (applicazione) della richiesta.
type MatrixColumn struct {
	ImpactID    string `json:"impact_id"`
	ImpactKey   string `json:"impact_key"`
	Application string `json:"application"`
	Team        string `json:"team"`
	Phase       string `json:"phase"`
}

// MatrixCell è lo stato di un requisito su un impatto.
type MatrixCell struct {
	Status           string  `json:"status"`
	ExceptionKey     string  `json:"exception_key,omitempty"`
	ExpiresOn        *string `json:"expires_on,omitempty"`
	ExceptionExpired bool    `json:"exception_expired,omitempty"`
	VerifiedBy       *string `json:"verified_by,omitempty"`
	VerifiedAt       *string `json:"verified_at,omitempty"`
}

// MatrixRow è un requisito con le sue celle, allineate alle colonne (nil = non si applica).
type MatrixRow struct {
	ID        string        `json:"id"`
	Key       string        `json:"key"`
	Title     string        `json:"title"`
	Scope     string        `json:"scope"`
	Category  string        `json:"category"`
	Stage     string        `json:"stage"`
	Phase     string        `json:"phase"`
	Origin    string        `json:"origin"`
	Method    string        `json:"method"`
	ImpactKey string        `json:"impact_key,omitempty"`
	Status    string        `json:"status"` // il peggiore tra le celle visibili
	Cells     []*MatrixCell `json:"cells"`
}

// Matrix è la matrice di tracciabilità di una richiesta (o di un solo impatto).
type Matrix struct {
	Request    engine.LinkedElement `json:"request"`
	Impact     string               `json:"impact,omitempty"`
	Columns    []MatrixColumn       `json:"columns"`
	Rows       []MatrixRow          `json:"rows"`
	Summary    Summary              `json:"summary"`
	Teams      []string             `json:"teams"`
	Categories []string             `json:"categories"`
	Statuses   map[string]string    `json:"statuses"`
	// Righe che rispettano i filtri (tutte, anche quelle fuori pagina), da dove parte la pagina e se
	// ce ne sono altre dopo: il riepilogo (Summary) riguarda sempre tutta la matrice.
	TotalRows int  `json:"total_rows"`
	Offset    int  `json:"offset"`
	More      bool `json:"more"`
	Can       struct {
		Create bool `json:"create"`
	} `json:"can"`
}

// MatrixFilter filtra la matrice: team delle colonne, categoria e stato delle righe.
type MatrixFilter struct {
	Team, Category, Status string
	// Limit > 0 pagina le righe (per requisito) dall'Offset; 0 = tutte. Solo per la matrice di una richiesta.
	Limit, Offset int
}

// Matrix costruisce la matrice per una richiesta o per un impatto (solo la sua colonna).
func (s *Service) Matrix(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, flt MatrixFilter) (*Matrix, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var req *engine.Element
	switch el.TypeKey {
	case RequestType:
		req = el
	case ImpactType:
		if req, err = s.eng.Get(ctx, tx, reader(actor.MemberID), deref(el.ParentID)); err != nil {
			return nil, err
		}
	default:
		return nil, engine.InvalidCode("validation.requirements.matrix_element", "element", "la matrice dei requisiti è della richiesta o dell'impatto")
	}
	today := s.today()
	m := &Matrix{Request: engine.LinkedElement{ID: req.ID, Key: req.Key, Title: req.Title, Type: req.TypeKey, TypeName: req.TypeName,
		State: req.State, StateCategory: req.StateCategory}, Columns: []MatrixColumn{}, Rows: []MatrixRow{},
		Teams: []string{}, Categories: []string{}, Statuses: StatusLabels}
	if el.TypeKey == ImpactType {
		m.Impact = el.Key
	}
	// Colonne: gli impatti attivi della richiesta (anche senza requisiti), nell'ordine.
	crows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, ap.title, coalesce(t.name, ''), coalesce(o.name, '')
		from ext_impact x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		where x.request_id = $1 and coalesce(o.category, '') <> 'cancelled' order by e.number`, req.ID)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(crows, pgx.RowToStructByPos[MatrixColumn])
	if err != nil {
		return nil, err
	}
	colIdx := map[string]int{}
	for _, c := range all {
		if c.Team != "" && !slices.Contains(m.Teams, c.Team) {
			m.Teams = append(m.Teams, c.Team)
		}
		if el.TypeKey == ImpactType && c.ImpactID != el.ID {
			continue
		}
		if flt.Team != "" && !strings.EqualFold(c.Team, flt.Team) {
			continue
		}
		colIdx[c.ImpactID] = len(m.Columns)
		m.Columns = append(m.Columns, c)
	}
	slices.Sort(m.Teams)
	if flt.Limit > 0 && el.TypeKey == RequestType {
		return s.matrixPage(ctx, tx, actor, req, el, m, colIdx, all, flt, today)
	}
	rows, err := s.loadCells(ctx, tx, "x.request_id = $1", req.ID)
	if err != nil {
		return nil, err
	}
	byReq := map[string]*MatrixRow{}
	var order []string
	inScope := []cellRow{}
	for _, r := range rows {
		if r.Category != "" && !slices.Contains(m.Categories, r.Category) {
			m.Categories = append(m.Categories, r.Category)
		}
		row, ok := byReq[r.ReqID]
		if !ok {
			row = &MatrixRow{ID: r.ReqID, Key: r.ReqKey, Title: r.Title, Scope: r.Scope, Category: r.Category, Stage: r.Stage,
				Phase: r.Phase, Origin: r.Origin, Method: r.Method, Cells: make([]*MatrixCell, len(m.Columns))}
			byReq[r.ReqID] = row
			order = append(order, r.ReqID)
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
		inScope = append(inScope, r)
	}
	slices.Sort(m.Categories)
	for _, id := range order {
		row := byReq[id]
		visible := 0
		row.Status = ""
		for _, c := range row.Cells {
			if c == nil || c.Status == "not_applicable" {
				continue
			}
			if visible == 0 || statusRank[c.Status] < statusRank[row.Status] {
				row.Status = c.Status
			}
			visible++
		}
		if row.Stage == StageNotApplicable {
			row.Status = "not_applicable"
		} else if visible == 0 {
			// Un requisito di applicazione di un impatto escluso (filtri, impatto annullato) non
			// si mostra; uno di progetto senza celle sì, con lo stato del suo stadio.
			if row.Scope == "application" || (el.TypeKey == ImpactType && len(m.Columns) > 0) {
				continue
			}
			row.Status = stageStatus(row.Stage)
		}
		if flt.Category != "" && !strings.EqualFold(row.Category, flt.Category) {
			continue
		}
		if flt.Status != "" && !rowHasStatus(row, flt.Status) {
			continue
		}
		m.Rows = append(m.Rows, *row)
	}
	if el.TypeKey == ImpactType {
		m.Summary = summarize(inScope, today, true)
	} else {
		m.Summary = summarize(rows, today, false)
	}
	m.TotalRows = len(m.Rows)
	m.Can.Create, err = s.canCreate(ctx, tx, actor, req, el)
	return m, err
}

func rowHasStatus(r *MatrixRow, status string) bool {
	if r.Status == status {
		return true
	}
	for _, c := range r.Cells {
		if c != nil && c.Status == status {
			return true
		}
	}
	return false
}

var scopeLabels = map[string]string{"project": "Progetto", "application": "Applicazione"}

// WriteCSV scrive la matrice con separatore ";" e BOM per Excel.
func (m *Matrix) WriteCSV(w io.Writer) error {
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	head := []string{"Requisito", "Titolo", "Ambito", "Categoria", "Origine", "Fase", "Stato"}
	for _, c := range m.Columns {
		head = append(head, c.ImpactKey+" "+c.Application)
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, r := range m.Rows {
		line := []string{r.Key, r.Title, scopeLabels[r.Scope], r.Category, originNames[r.Origin], r.Phase, StatusLabels[r.Status]}
		for _, c := range r.Cells {
			v := ""
			if c != nil {
				v = StatusLabels[c.Status]
				if c.Status == "waived" && c.ExpiresOn != nil {
					v += " fino al " + *c.ExpiresOn
				}
			}
			line = append(line, v)
		}
		if err := cw.Write(line); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ---------------------------------------------------------------------------
// Gate di rilascio per le pipeline (devsecops.md §3.5)

// GateImpact è un impatto valutato dal gate di rilascio.
type GateImpact struct {
	Impact     string  `json:"impact"`
	Request    string  `json:"request"`
	RequestTtl string  `json:"request_title"`
	Phase      string  `json:"phase"`
	Summary    Summary `json:"summary"`
}

// GateResult è la risposta del gate di rilascio.
type GateResult struct {
	Allowed     bool `json:"allowed"`
	Application struct {
		ID    string `json:"id"`
		Key   string `json:"key"`
		Code  string `json:"code"`
		Title string `json:"title"`
	} `json:"application"`
	Impacts   []GateImpact `json:"impacts"`
	Reasons   []string     `json:"reasons"`
	CheckedAt time.Time    `json:"checked_at"`
}

// ReleaseGate dice se un'applicazione può andare in produzione: per la richiesta indicata,
// oppure per tutte le richieste in cui l'applicazione ha un impatto in corso. Sì solo se
// ogni requisito applicabile è verificato o coperto da una deroga valida. La decisione
// finisce nell'audit, con i motivi.
func (s *Service) ReleaseGate(ctx context.Context, tx pgx.Tx, actor engine.Actor, application, request string) (*GateResult, error) {
	res, err := s.GateCheck(ctx, tx, actor, application, request)
	if err != nil {
		return nil, err
	}
	impacts := make([]string, len(res.Impacts))
	missing := 0
	for i, g := range res.Impacts {
		impacts[i] = g.Impact
		missing += len(g.Summary.Missing)
	}
	if err := audited(ctx, tx, actor, "release_gate.check", "application", res.Application.ID, map[string]any{
		"allowed": res.Allowed, "request": request, "impacts": impacts, "missing": missing}); err != nil {
		return nil, err
	}
	return res, nil
}

// GateCheck è la valutazione del gate dei requisiti senza scrivere nell'audit: la usa il gate
// di rilascio del modulo DevSecOps, che registra la propria decisione, e l'anteprima.
func (s *Service) GateCheck(ctx context.Context, tx pgx.Tx, actor engine.Actor, application, request string) (*GateResult, error) {
	application, request = strings.TrimSpace(application), strings.TrimSpace(request)
	if application == "" {
		return nil, engine.Required("application", "indica l'applicazione (codice del catalogo, chiave o id)")
	}
	res := &GateResult{Impacts: []GateImpact{}, Reasons: []string{}, CheckedAt: s.Now().UTC().Truncate(time.Second)}
	err := tx.QueryRow(ctx, `select e.id::text, s.key || '-' || e.number, coalesce(a.code, ''), e.title
		from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'application'
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		where e.deleted_at is null and (lower(a.code) = lower($1) or e.id::text = $1 or s.key || '-' || e.number = upper($1))
		limit 1`, application).Scan(&res.Application.ID, &res.Application.Key, &res.Application.Code, &res.Application.Title)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	q := `select ie.id::text from ext_impact x
		join elements ie on ie.tenant_id = x.tenant_id and ie.id = x.element_id and ie.deleted_at is null
		left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
		join elements re on re.tenant_id = x.tenant_id and re.id = x.request_id and re.deleted_at is null
		join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id
		where x.application_id = $1 and coalesce(io.category, '') <> 'cancelled'`
	args := []any{res.Application.ID}
	if request != "" {
		q += ` and (re.id::text = $2 or rs.key || '-' || re.number = upper($2))`
		args = append(args, request)
	} else {
		q += ` and coalesce(io.category, '') <> 'done' and coalesce(ro.category, '') not in ('done', 'cancelled')`
	}
	q += ` order by re.number, ie.number`
	irows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(irows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if request != "" && len(ids) == 0 {
		return nil, engine.InvalidParams("validation.requirements.no_impact_in_request", "request", map[string]any{"request": request}, "l'applicazione non ha un impatto nella richiesta %s", request)
	}
	sys := reader(actor.MemberID)
	res.Allowed = true
	for _, id := range ids {
		imp, err := s.eng.Get(ctx, tx, sys, id)
		if err != nil {
			return nil, err
		}
		req, err := s.eng.Get(ctx, tx, sys, deref(imp.ParentID))
		if err != nil {
			return nil, err
		}
		sum, _, err := s.Coverage(ctx, tx, imp)
		if err != nil {
			return nil, err
		}
		res.Impacts = append(res.Impacts, GateImpact{Impact: imp.Key, Request: req.Key, RequestTtl: req.Title, Phase: imp.State, Summary: sum})
		if !sum.Ready {
			res.Allowed = false
			res.Reasons = append(res.Reasons, req.Key+" ("+imp.Key+"): "+missingMessage(sum.Missing))
		}
	}
	if len(ids) == 0 {
		res.Reasons = append(res.Reasons, "nessun impatto in corso per l'applicazione: nessun requisito da verificare")
	}
	return res, nil
}
