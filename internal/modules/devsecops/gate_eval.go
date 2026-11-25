package devsecops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Valutazione del gate di rilascio. Una chiamata della pipeline (o l'anteprima dell'editor)
// dà un esito pass, warn o fail con i motivi, uno per regola.

// GateInput è la domanda della pipeline: dove si vuole rilasciare e che cosa.
type GateInput struct {
	Application string `json:"application"` // codice, chiave o id; si può omettere se c'è il servizio associato
	Service     string `json:"service"`     // nome del servizio (facoltativo)
	Environment string `json:"environment"`
	Change      string `json:"change"`  // numero di change (CHG0045120) o chiave della richiesta
	Request     string `json:"request"` // chiave della richiesta di Regia (DEM-12)
	Version     string `json:"version"`
	Commit      string `json:"commit"`
}

// GateRef è un elemento citato nell'esito, con il suo indirizzo.
type GateRef struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Code  string `json:"code,omitempty"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// GateReason è il motivo di una regola.
type GateReason struct {
	Rule       string `json:"rule"` // id della regola
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Status     string `json:"status"` // pass | warn | fail
	Message    string `json:"message"`
	URL        string `json:"url,omitempty"`
	Overridden bool   `json:"overridden,omitempty"`
}

// GateOutcome è la risposta del gate.
type GateOutcome struct {
	Result      string       `json:"result"`     // pass | warn | fail
	RawResult   string       `json:"raw_result"` // prima della modalità dell'ambiente e dell'override
	Mode        string       `json:"mode"`       // off | warn | block | none (nessuna regola)
	Environment string       `json:"environment"`
	Application GateRef      `json:"application"`
	Request     *GateRef     `json:"request,omitempty"`
	Service     string       `json:"service,omitempty"`
	Version     string       `json:"version,omitempty"`
	Reasons     []GateReason `json:"reasons"`
	Summary     string       `json:"summary"`
	Overridden  bool         `json:"overridden,omitempty"`
	Override    *OverrideRef `json:"override,omitempty"`
	DecisionID  string       `json:"decision_id,omitempty"`
	URL         string       `json:"url"`
	CheckedAt   time.Time    `json:"checked_at"`
}

// OverrideRef è l'override che ha superato (o che si può chiedere per) la decisione.
type OverrideRef struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (s *Service) link(path string) string {
	if s.PublicURL == "" {
		return path
	}
	return strings.TrimRight(s.PublicURL, "/") + path
}

func (s *Service) elementLink(key, tab string) string {
	p := "/e/" + key
	if tab != "" {
		p += "?scheda=" + tab
	}
	return s.link(p)
}

type gateCtx struct {
	in      GateInput
	appID   string
	app     GateRef
	reqID   string
	req     *GateRef
	reqEl   *engine.Element
	appEl   *engine.Element
	svcIDs  []string
	svcName map[string]string
	policy  *GatePolicy
	env     string
	now     time.Time
}

func (s *Service) elementRefByID(ctx context.Context, tx pgx.Tx, id string) (GateRef, error) {
	var g GateRef
	var code *string
	err := tx.QueryRow(ctx, `select e.id::text, s.key || '-' || e.number, a.code, e.title
		from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id where e.id = $1`, id).Scan(&g.ID, &g.Key, &code, &g.Title)
	if err != nil {
		return g, err
	}
	if code != nil {
		g.Code = *code
	}
	g.URL = s.elementLink(g.Key, "gate")
	return g, nil
}

// resolve risolve applicazione, richiesta e servizi della domanda.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, actor engine.Actor, in GateInput, policies []GatePolicy) (*gateCtx, error) {
	g := &gateCtx{in: in, now: s.now(), svcName: map[string]string{}}
	g.in.Environment = normEnv(in.Environment)
	g.env = g.in.Environment
	if g.env == "" {
		return nil, engine.Required("environment", "indica l'ambiente di destinazione")
	}
	g.in.Service, g.in.Version, g.in.Commit = clip(in.Service, 200), clip(in.Version, 200), clip(in.Commit, 80)
	g.in.Application, g.in.Change, g.in.Request = clip(in.Application, 200), clip(in.Change, 120), clip(in.Request, 120)
	var svcApp string
	if g.in.Service != "" {
		var sid string
		var app *string
		err := tx.QueryRow(ctx, "select id::text, application_element_id::text from services where lower(name) = lower($1)", g.in.Service).Scan(&sid, &app)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidValue("service", "servizio sconosciuto: %s", g.in.Service)
		}
		if err != nil {
			return nil, err
		}
		g.svcIDs, g.svcName[sid] = []string{sid}, g.in.Service
		if app != nil {
			svcApp = *app
		}
	}
	if g.in.Application != "" {
		id, err := appByRef(ctx, tx, g.in.Application)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, engine.InvalidValue("application", "applicazione sconosciuta: %s", g.in.Application)
		}
		g.appID = id
		if svcApp != "" && svcApp != id {
			return nil, engine.InvalidParams("validation.devsecops.service_other_app", "service", map[string]any{"service": g.in.Service}, "il servizio %s è associato a un'altra applicazione", g.in.Service)
		}
	} else if svcApp != "" {
		g.appID = svcApp
	}
	if g.appID == "" {
		return nil, engine.Required("application", "indica l'applicazione (codice del catalogo o chiave), oppure un servizio già associato")
	}
	sys := engine.Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}
	var err error
	if g.appEl, err = s.eng.Get(ctx, tx, sys, g.appID); err != nil {
		return nil, err
	}
	if g.app, err = s.elementRefByID(ctx, tx, g.appID); err != nil {
		return nil, err
	}
	// Servizi dell'applicazione sulla pipeline, se non indicato.
	if len(g.svcIDs) == 0 {
		rows, err := tx.Query(ctx, "select id::text, name from services where application_element_id = $1 and first_seen_at is not null order by name", g.appID)
		if err != nil {
			return nil, err
		}
		var id, name string
		if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error {
			g.svcIDs = append(g.svcIDs, id)
			g.svcName[id] = name
			return nil
		}); err != nil {
			return nil, err
		}
	}
	// Richiesta: dalla chiave, dal numero di change o dal campo configurato.
	switch {
	case g.in.Request != "":
		id, err := requestByRef(ctx, tx, g.in.Request)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, engine.InvalidValue("request", "richiesta sconosciuta: %s", g.in.Request)
		}
		g.reqID = id
	case g.in.Change != "":
		id, err := s.requestOfChange(ctx, tx, g.in.Change)
		if err != nil {
			return nil, err
		}
		g.reqID = id
	}
	if g.reqID != "" {
		ref, err := s.elementRefByID(ctx, tx, g.reqID)
		if err != nil {
			return nil, err
		}
		g.req = &ref
		if g.reqEl, err = s.eng.Get(ctx, tx, sys, g.reqID); err != nil {
			return nil, err
		}
	}
	g.policy = policyFor(policies, g.env)
	return g, nil
}

func requestByRef(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `select e.id::text from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'business_request'
		where e.deleted_at is null and (e.id::text = $1 or s.key || '-' || e.number = upper($1)) limit 1`, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// requestOfChange trova la richiesta di un numero di change: un change già noto, la chiave di
// una richiesta o il campo configurato nelle impostazioni.
func (s *Service) requestOfChange(ctx context.Context, tx pgx.Tx, number string) (string, error) {
	n := normChange(number)
	var id *string
	err := tx.QueryRow(ctx, "select request_element_id::text from changes where number = $1 or $1 = any(aliases) limit 1", n).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if id != nil {
		return *id, nil
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return "", err
	}
	rid, _, err := matchRequest(ctx, tx, []string{n, number}, st.ChangeField)
	return rid, err
}

func rank(st string) int {
	switch st {
	case "fail":
		return 2
	case "warn":
		return 1
	}
	return 0
}

func (g *gateCtx) reason(r GateRule, status, msg, link string) GateReason {
	if status == "fail" && r.Level == "warn" {
		status = "warn"
	}
	label := r.Label
	if label == "" {
		label = RuleLabel(r.Kind)
	}
	return GateReason{Rule: r.ID, Kind: r.Kind, Label: label, Status: status, Message: msg, URL: link}
}

// RuleLabel è il nome leggibile del tipo di regola.
func RuleLabel(kind string) string {
	switch kind {
	case RuleRequirements:
		return "Requisiti di progetto"
	case RuleVulnerabilities:
		return "Vulnerabilità"
	case RuleChangeApproved:
		return "Change approvato"
	case RuleWindow:
		return "Finestra di rilascio"
	case RuleChecks:
		return "Controlli della pipeline"
	case RuleCondition:
		return "Condizione"
	}
	return kind
}

// Evaluate valuta il gate. policy != nil sostituisce quella salvata (anteprima di una bozza).
// Non scrive nulla: la registrazione è di Decide.
func (s *Service) Evaluate(ctx context.Context, tx pgx.Tx, actor engine.Actor, in GateInput, draft *GatePolicy) (*GateOutcome, error) {
	policies, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	g, err := s.resolve(ctx, tx, actor, in, policies)
	if err != nil {
		return nil, err
	}
	if draft != nil {
		d := *draft
		g.policy = &d
	}
	return s.evaluate(ctx, tx, actor, g)
}

func (s *Service) evaluate(ctx context.Context, tx pgx.Tx, actor engine.Actor, g *gateCtx) (*GateOutcome, error) {
	out := &GateOutcome{Environment: g.env, Application: g.app, Request: g.req, Version: g.in.Version, Reasons: []GateReason{},
		CheckedAt: g.now.Truncate(time.Second), URL: s.elementLink(g.app.Key, "gate"), Mode: "none", Result: "pass", RawResult: "pass"}
	if g.in.Service != "" {
		out.Service = g.in.Service
	}
	if g.policy == nil {
		out.Reasons = append(out.Reasons, GateReason{Kind: "policy", Label: "Regole dell'ambiente", Status: "pass",
			Message: fmt.Sprintf("Nessuna regola configurata per l'ambiente %s: il gate lascia passare.", g.env), URL: s.link("/configurazione/gate")})
		out.Summary = out.Reasons[0].Message
		return out, nil
	}
	out.Mode = g.policy.Mode
	if g.policy.Mode == "off" {
		out.Reasons = append(out.Reasons, GateReason{Kind: "policy", Label: "Regole dell'ambiente", Status: "pass",
			Message: fmt.Sprintf("Il gate è spento per l'ambiente %s.", g.env), URL: s.link("/configurazione/gate")})
		out.Summary = out.Reasons[0].Message
		return out, nil
	}
	sys := engine.Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}
	for _, r := range g.policy.Rules {
		if !r.Enabled {
			continue
		}
		var rs []GateReason
		var err error
		switch r.Kind {
		case RuleRequirements:
			rs, err = s.ruleRequirements(ctx, tx, sys, g, r)
		case RuleVulnerabilities:
			rs, err = s.ruleVulnerabilities(ctx, tx, g, r)
		case RuleChangeApproved:
			rs, err = s.ruleChange(g, r)
		case RuleWindow:
			rs, err = s.ruleWindow(g, r)
		case RuleChecks:
			rs, err = s.ruleChecks(ctx, tx, g, r)
		case RuleCondition:
			rs, err = s.ruleCondition(ctx, tx, g, r)
		}
		if err != nil {
			return nil, err
		}
		out.Reasons = append(out.Reasons, rs...)
	}
	raw := 0
	for _, r := range out.Reasons {
		raw = max(raw, rank(r.Status))
	}
	out.RawResult = []string{"pass", "warn", "fail"}[raw]
	out.Result = out.RawResult
	if out.Result == "fail" && g.policy.Mode == "warn" {
		out.Result = "warn"
	}
	if out.Result == "fail" {
		ov, err := s.activeOverride(ctx, tx, g)
		if err != nil {
			return nil, err
		}
		if ov != nil {
			out.Result, out.Overridden, out.Override = "pass", true, ov
			for i := range out.Reasons {
				if out.Reasons[i].Status == "fail" {
					out.Reasons[i].Overridden = true
				}
			}
		}
	}
	out.Summary = summarize(out, g.policy.Mode)
	return out, nil
}

func summarize(o *GateOutcome, mode string) string {
	bad := 0
	for _, r := range o.Reasons {
		if r.Status != "pass" {
			bad++
		}
	}
	switch {
	case o.Overridden:
		return fmt.Sprintf("Superato con un override approvato (%d regole non soddisfatte).", bad)
	case o.Result == "pass":
		return fmt.Sprintf("Tutte le regole dell'ambiente %s sono soddisfatte.", o.Environment)
	case o.Result == "warn" && mode == "warn" && o.RawResult == "fail":
		return fmt.Sprintf("%d regole non soddisfatte: il gate è in modalità avviso e non blocca.", bad)
	case o.Result == "warn":
		return fmt.Sprintf("%d regole con un avviso: il rilascio può procedere.", bad)
	}
	return fmt.Sprintf("Il rilascio è bloccato: %d regole non soddisfatte.", bad)
}

// ---------------------------------------------------------------------------
// Le regole

func (s *Service) ruleRequirements(ctx context.Context, tx pgx.Tx, sys engine.Actor, g *gateCtx, r GateRule) ([]GateReason, error) {
	if s.req == nil {
		return []GateReason{g.reason(r, "pass", "Il modulo dei requisiti non è attivo.", "")}, nil
	}
	reqKey := ""
	if g.req != nil {
		reqKey = g.req.Key
	}
	res, err := s.req.GateCheck(ctx, tx, sys, g.appID, reqKey)
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) && ve.Field == "request" {
			return []GateReason{g.reason(r, "pass", "L'applicazione non ha impatti in "+reqKey+": nessun requisito da verificare.", "")}, nil
		}
		return nil, err
	}
	if res.Allowed {
		msg := "Tutti i requisiti applicabili sono verificati o coperti da una deroga valida."
		if len(res.Impacts) == 0 {
			msg = "Nessun impatto in corso per l'applicazione: nessun requisito da verificare."
		}
		return []GateReason{g.reason(r, "pass", msg, "")}, nil
	}
	out := []GateReason{}
	for i, text := range res.Reasons {
		if i == 4 && len(res.Reasons) > 5 {
			out = append(out, g.reason(r, "fail", fmt.Sprintf("… e altri %d impatti con requisiti da completare.", len(res.Reasons)-4), s.elementLink(g.app.Key, "requisiti")))
			break
		}
		link := ""
		if k, _, ok := strings.Cut(text, " ("); ok {
			link = s.elementLink(k, "requisiti")
		}
		out = append(out, g.reason(r, "fail", clip(text, 260)+ellipsis(text, 260), link))
	}
	return out, nil
}

func (s *Service) ruleVulnerabilities(ctx context.Context, tx pgx.Tx, g *gateCtx, r GateRule) ([]GateReason, error) {
	var p vulnParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	what := "critiche o alte"
	if p.Severity == "critical" {
		what = "critiche"
	}
	if p.OnlyFixable {
		what += " con correzione disponibile"
	}
	link := s.elementLink(g.app.Key, "produzione")
	if len(g.svcIDs) == 0 {
		if p.RequireReport {
			return []GateReason{g.reason(r, "fail", "Nessun servizio dell'applicazione è sulla pipeline: non c'è una scansione delle vulnerabilità.", link)}, nil
		}
		return []GateReason{g.reason(r, "pass", "Nessun servizio sulla pipeline: nessuna vulnerabilità nota.", link)}, nil
	}
	versions := make([]string, len(g.svcIDs))
	if g.in.Version != "" && len(g.svcIDs) == 1 {
		versions[0] = g.in.Version
	} else {
		rows, err := tx.Query(ctx, `select distinct on (service_id) service_id::text, version from vuln_reports
			where service_id = any($1) order by service_id, scanned_at desc`, g.svcIDs)
		if err != nil {
			return nil, err
		}
		latest := map[string]string{}
		var sid, ver string
		if _, err := pgx.ForEachRow(rows, []any{&sid, &ver}, func() error { latest[sid] = ver; return nil }); err != nil {
			return nil, err
		}
		for i, id := range g.svcIDs {
			versions[i] = latest[id]
		}
	}
	// Le versioni senza nome non hanno report: cveCounts non le trova e il servizio risulta senza scansione.
	counts, err := cveCounts(ctx, tx, g.svcIDs, versions, g.now.Add(time.Minute), false, true)
	if err != nil {
		return nil, err
	}
	out := []GateReason{}
	for i, id := range g.svcIDs {
		name := g.svcName[id] + " " + versions[i]
		c := counts[id]
		if c == nil {
			if p.RequireReport {
				msg := "Nessun report delle vulnerabilità per " + strings.TrimSpace(name) + "."
				if versions[i] == "" {
					msg = "Nessun report delle vulnerabilità per " + g.svcName[id] + "."
				}
				out = append(out, g.reason(r, "fail", msg, link))
			}
			continue
		}
		n := c.Critical
		if p.Severity == "high" {
			n += c.High
		}
		if p.OnlyFixable {
			n = c.FixableCritical
			if p.Severity == "high" {
				n += c.FixableHigh
			}
		}
		if n > p.Max {
			msg := fmt.Sprintf("%s: %d CVE %s", name, n, what)
			if p.Max > 0 {
				msg += fmt.Sprintf(" (se ne tollerano %d)", p.Max)
			}
			out = append(out, g.reason(r, "fail", msg+".", link))
		} else {
			out = append(out, g.reason(r, "pass", fmt.Sprintf("%s: nessuna CVE %s oltre la soglia.", name, what), link))
		}
	}
	if len(out) == 0 {
		out = append(out, g.reason(r, "pass", "Nessuna scansione da valutare.", link))
	}
	return collapse(g, r, out), nil
}

// collapse riunisce i motivi positivi di più servizi in uno, per non riempire l'elenco.
func collapse(g *gateCtx, r GateRule, rs []GateReason) []GateReason {
	passes, out := []GateReason{}, []GateReason{}
	for _, x := range rs {
		if x.Status == "pass" {
			passes = append(passes, x)
		} else {
			out = append(out, x)
		}
	}
	switch {
	case len(passes) == 0:
	case len(out) == 0 && len(passes) > 1:
		out = append(out, g.reason(r, "pass", fmt.Sprintf("%d servizi senza CVE oltre la soglia.", len(passes)), passes[0].URL))
	default:
		out = append(out, passes...)
	}
	return out
}

func (s *Service) ruleChange(g *gateCtx, r GateRule) ([]GateReason, error) {
	var p changeParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	if g.req == nil {
		if !p.RequireChange {
			return []GateReason{g.reason(r, "pass", "Nessuna richiesta di change collegata: la regola non si applica.", "")}, nil
		}
		msg := "Nessuna richiesta di change collegata: indica il numero di change o la richiesta del rilascio."
		if g.in.Change != "" {
			msg = fmt.Sprintf("Il change %s non è collegato a nessuna richiesta di Regia.", g.in.Change)
		}
		return []GateReason{g.reason(r, "fail", msg, "")}, nil
	}
	state := g.reqEl.State
	for _, ph := range p.Phases {
		if strings.EqualFold(ph, state) {
			return []GateReason{g.reason(r, "pass", fmt.Sprintf("La richiesta %s è in fase «%s».", g.req.Key, state), g.req.URL)}, nil
		}
	}
	return []GateReason{g.reason(r, "fail", fmt.Sprintf("La richiesta %s è in fase «%s»: serve una tra %s.", g.req.Key, state, quoted(p.Phases)), g.req.URL)}, nil
}

// ellipsis aggiunge i puntini se clip ha tagliato il testo.
func ellipsis(s string, n int) string {
	if len(strings.TrimSpace(s)) > n {
		return "…"
	}
	return ""
}

func quoted(ss []string) string {
	q := make([]string, len(ss))
	for i, x := range ss {
		q[i] = "«" + x + "»"
	}
	return strings.Join(q, ", ")
}

var dayNames = []string{"", "lun", "mar", "mer", "gio", "ven", "sab", "dom"}

func (s *Service) ruleWindow(g *gateCtx, r GateRule) ([]GateReason, error) {
	var p windowParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := g.now.In(loc)
	today := now.Format(time.DateOnly)
	for _, b := range p.Blackouts {
		if today >= b.From && today <= b.To {
			l := b.Label
			if l == "" {
				l = "blocco dei rilasci"
			}
			return []GateReason{g.reason(r, "fail", fmt.Sprintf("Oggi c'è un blocco dei rilasci: %s (dal %s al %s).", l, dayIT(b.From), dayIT(b.To)), "")}, nil
		}
	}
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	desc := windowText(p)
	if len(p.Days) > 0 && !slices.Contains(p.Days, wd) {
		return []GateReason{g.reason(r, "fail", fmt.Sprintf("Fuori dalla finestra di rilascio (%s): oggi è %s.", desc, dayNames[wd]), "")}, nil
	}
	from, _ := time.Parse("15:04", p.From)
	to, _ := time.Parse("15:04", p.To)
	cur := now.Hour()*60 + now.Minute()
	a, b := from.Hour()*60+from.Minute(), to.Hour()*60+to.Minute()
	ok := cur >= a && cur < b
	if a > b { // finestra a cavallo della mezzanotte
		ok = cur >= a || cur < b
	}
	if a == b { // dalle 00:00 alle 00:00: tutto il giorno
		ok = true
	}
	if !ok {
		return []GateReason{g.reason(r, "fail", fmt.Sprintf("Fuori dalla finestra di rilascio (%s): sono le %s.", desc, now.Format("15:04")), "")}, nil
	}
	return []GateReason{g.reason(r, "pass", fmt.Sprintf("Dentro la finestra di rilascio (%s).", desc), "")}, nil
}

func dayIT(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02/01/2006")
}

func windowText(p windowParams) string {
	days := "tutti i giorni"
	if len(p.Days) > 0 && len(p.Days) < 7 {
		names := make([]string, len(p.Days))
		for i, d := range p.Days {
			names[i] = dayNames[d]
		}
		days = strings.Join(names, ", ")
		if len(p.Days) > 2 {
			contiguous := true
			for i := 1; i < len(p.Days); i++ {
				contiguous = contiguous && p.Days[i] == p.Days[i-1]+1
			}
			if contiguous {
				days = dayNames[p.Days[0]] + "–" + dayNames[p.Days[len(p.Days)-1]]
			}
		}
	}
	return fmt.Sprintf("%s %s–%s, %s", days, p.From, p.To, p.Timezone)
}

func (s *Service) ruleCondition(ctx context.Context, tx pgx.Tx, g *gateCtx, r GateRule) ([]GateReason, error) {
	var p conditionParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	cond, err := engine.ParseCondition(p.When)
	if err != nil {
		return nil, err
	}
	el, link, what := g.appEl, g.app.URL, "l'applicazione "+g.app.Key
	if p.On == "request" {
		if g.reqEl == nil {
			return []GateReason{g.reason(r, "fail", "La condizione riguarda la richiesta, ma nessuna richiesta è collegata al rilascio.", "")}, nil
		}
		el, link, what = g.reqEl, g.req.URL, "la richiesta "+g.req.Key
	}
	facts, err := s.eng.ElementFacts(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	label := r.Label
	if cond.Eval(facts) {
		if label == "" {
			label = "La condizione è vera per " + what
		}
		return []GateReason{g.reason(r, "pass", label+".", link)}, nil
	}
	msg := fmt.Sprintf("La condizione «%s» non è vera per %s.", p.When, what)
	if label != "" {
		msg = fmt.Sprintf("%s: la condizione «%s» non è vera per %s.", label, p.When, what)
	}
	return []GateReason{g.reason(r, "fail", msg, link)}, nil
}

func (s *Service) ruleChecks(ctx context.Context, tx pgx.Tx, g *gateCtx, r GateRule) ([]GateReason, error) {
	var p checksParams
	if err := strictDecode(r.Params, &p); err != nil {
		return nil, err
	}
	link := s.elementLink(g.app.Key, "produzione")
	if len(g.svcIDs) == 0 {
		return []GateReason{g.reason(r, "fail", "Nessun servizio dell'applicazione è sulla pipeline: nessun esito dei controlli.", link)}, nil
	}
	out := []GateReason{}
	for _, id := range g.svcIDs {
		var raw []byte
		var commit string
		q := "select checks, commit from check_runs where service_id = $1 order by at desc limit 1"
		args := []any{id}
		if g.in.Commit != "" {
			q = "select checks, commit from check_runs where service_id = $1 and commit = $2 order by at desc limit 1"
			args = append(args, g.in.Commit)
		}
		err := tx.QueryRow(ctx, q, args...).Scan(&raw, &commit)
		if errors.Is(err, pgx.ErrNoRows) {
			out = append(out, g.reason(r, "fail", "Nessun esito dei controlli per "+g.svcName[id]+commitText(g.in.Commit)+".", link))
			continue
		}
		if err != nil {
			return nil, err
		}
		var checks []contract.CheckResult
		_ = json.Unmarshal(raw, &checks)
		missing := []string{}
		for _, name := range p.Names {
			ok := false
			for _, c := range checks {
				if strings.EqualFold(c.Name, name) && c.Outcome == "passed" && c.Skip == nil && c.Mode != "off" {
					ok = true
				}
			}
			if !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			out = append(out, g.reason(r, "fail", fmt.Sprintf("%s (%s): controlli non superati o non eseguiti: %s.", g.svcName[id], short(commit), strings.Join(missing, ", ")), link))
		} else {
			out = append(out, g.reason(r, "pass", fmt.Sprintf("%s (%s): superati %s.", g.svcName[id], short(commit), strings.Join(p.Names, ", ")), link))
		}
	}
	return collapse(g, r, out), nil
}

func commitText(c string) string {
	if c == "" {
		return ""
	}
	return " (commit " + short(c) + ")"
}

// ---------------------------------------------------------------------------
// Registrazione della decisione

// Decide valuta il gate e registra la decisione: esito, motivi, audit e storia dell'applicazione
// e della richiesta. Le chiamate identiche entro cinque minuti valgono una sola decisione.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, actor engine.Actor, in GateInput) (*GateOutcome, error) {
	policies, err := loadPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	g, err := s.resolve(ctx, tx, actor, in, policies)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/gate/"+g.appID); err != nil {
		return nil, err
	}
	out, err := s.evaluate(ctx, tx, actor, g)
	if err != nil {
		return nil, err
	}
	reasons, _ := json.Marshal(out.Reasons)
	reqID := ""
	if g.req != nil {
		reqID = g.req.ID
	}
	h := sha256.Sum256([]byte(strings.Join([]string{g.appID, g.env, g.in.Version, g.in.Commit, normChange(g.in.Change), reqID, out.Result, out.RawResult, string(reasons)}, "\x00")))
	digest := hex.EncodeToString(h[:])
	var existing string
	err = tx.QueryRow(ctx, `select id::text from gate_decisions where application_element_id = $1 and digest = $2 and created_at > now() - interval '5 minutes'
		order by created_at desc limit 1`, g.appID, digest).Scan(&existing)
	if err == nil {
		out.DecisionID = existing
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	id := db.NewID()
	var svc any
	if len(g.svcIDs) == 1 {
		svc = g.svcIDs[0]
	}
	var overrideID any
	if out.Override != nil {
		overrideID = out.Override.ID
	}
	if _, err := tx.Exec(ctx, `insert into gate_decisions (tenant_id, id, application_element_id, request_element_id, service_id, environment, version, commit,
		change_number, outcome, raw_outcome, mode, overridden, override_id, reasons, digest, actor_member_id)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		id, g.appID, nullable(reqID), svc, g.env, nullable(g.in.Version), nullable(g.in.Commit), nullable(normChange(g.in.Change)),
		out.Result, out.RawResult, out.Mode, out.Overridden, overrideID, reasons, digest, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	out.DecisionID = id
	payload := map[string]any{"environment": g.env, "result": out.Result, "raw_result": out.RawResult, "version": g.in.Version, "decision": id, "mode": out.Mode}
	if out.Overridden {
		payload["override"] = out.Override.ID
	}
	for _, el := range []string{g.appID, reqID} {
		if el == "" {
			continue
		}
		if err := engine.AddActivity(ctx, tx, el, actor.MemberID, "gate", out.Result, payload, nil); err != nil {
			return nil, err
		}
	}
	auditPayload := map[string]any{"application": g.appID, "request": reqID, "environment": g.env, "version": g.in.Version, "commit": g.in.Commit,
		"change": g.in.Change, "result": out.Result, "raw_result": out.RawResult, "mode": out.Mode, "overridden": out.Overridden, "reasons": len(out.Reasons)}
	if err := auditEvent(ctx, tx, actor, "devsecops.gate", "gate_decision", id, auditPayload); err != nil {
		return nil, err
	}
	// Una decisione superata davvero (non per override) è un'evidenza dei controlli mappati.
	if out.Result == "pass" && !out.Overridden && (out.Mode == "block" || out.Mode == "warn") {
		if err := s.gateEvidence(ctx, tx, actor, g, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
