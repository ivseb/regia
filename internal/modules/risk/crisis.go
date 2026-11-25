package risk

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Vista di crisi dell'incidente (replica/usabilita.md, proposta 3): lo stesso incidente della
// pagina, ma con ciò che serve a chi lo gestisce a colpo d'occhio: la prossima azione, l'impatto
// sulle applicazioni (ultimo rilascio, vulnerabilità aperte, rischi), i fornitori coinvolti, una
// cronologia unica con le scadenze future e la bozza della notifica con i campi che mancano.

// StaleUpdateHours: oltre queste ore senza un aggiornamento, un incidente aperto chiede il prossimo.
const StaleUpdateHours = 4

// suspectHoursBefore: un rilascio in produzione fino a queste ore prima della rilevazione è sospetto.
const suspectHoursBefore = 24

// CrisisRelease è l'ultimo rilascio di un'applicazione.
type CrisisRelease struct {
	Version     string    `json:"version"`
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	Production  bool      `json:"production"`
	At          time.Time `json:"at"`
	// Suspect: in produzione nelle ore che precedono la rilevazione.
	Suspect bool `json:"suspect"`
}

// CrisisVulns conta le vulnerabilità aperte dell'applicazione.
type CrisisVulns struct {
	Open     int `json:"open"`
	Overdue  int `json:"overdue"`
	Critical int `json:"critical"`
}

// CrisisApp è un'applicazione colpita.
type CrisisApp struct {
	Ref            Ref            `json:"ref"`
	Criticality    *string        `json:"criticality"`
	RTOMinutes     *int           `json:"rto_minutes"`
	Team           *string        `json:"team"`
	LastRelease    *CrisisRelease `json:"last_release"`
	Vulns          CrisisVulns    `json:"vulns"`
	Risks          []Ref          `json:"risks"`
	OtherIncidents []Ref          `json:"other_incidents"`
	Runbooks       []Ref          `json:"runbooks"`
	Dependents     []Ref          `json:"dependents"`
}

// CrisisProvider è un fornitore coinvolto: causa dell'incidente o fornitore di un'applicazione colpita.
type CrisisProvider struct {
	Ref          Ref      `json:"ref"`
	Cause        bool     `json:"cause"`
	Critical     bool     `json:"critical"`
	NextEnd      *string  `json:"next_end"`
	Applications []string `json:"applications"`
}

// NextAction è il prossimo passo suggerito. Kind: classify, notify, contain, update, resolve,
// close, postmortem, none.
type NextAction struct {
	Kind         string     `json:"kind"`
	DueAt        *time.Time `json:"due_at"`
	Notification string     `json:"notification,omitempty"`
}

// TimelineItem è una riga della cronologia unica. Kind: un tipo di evento dell'incidente, oppure
// release, cve, deadline.
type TimelineItem struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
	Ref    string    `json:"ref,omitempty"`
	Future bool      `json:"future"`
	System bool      `json:"system"`
	By     *string   `json:"by"`
}

// DraftField è un campo della bozza della notifica. Kind: text, date, number, minutes, money, list.
type DraftField struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Missing  bool   `json:"missing"`
	Required bool   `json:"required"`
}

// CrisisDraft è la bozza precompilata della prossima notifica normativa.
type CrisisDraft struct {
	Notification *Notification `json:"notification"`
	Fields       []DraftField  `json:"fields"`
	// Missing: quanti campi obbligatori mancano.
	Missing int `json:"missing"`
}

// CrisisView è la risposta della vista di crisi.
type CrisisView struct {
	Incident       *IncidentView    `json:"incident"`
	Next           NextAction       `json:"next"`
	Applications   []CrisisApp      `json:"applications"`
	Providers      []CrisisProvider `json:"providers"`
	Timeline       []TimelineItem   `json:"timeline"`
	Decisions      []Event          `json:"decisions"`
	Communications []Event          `json:"communications"`
	Draft          CrisisDraft      `json:"draft"`
	// StaleHours: ore dall'ultimo aggiornamento (0 se non c'è stato nessuno).
	StaleHours int `json:"stale_hours"`
}

func isClosed(cat string) bool { return cat == "done" || cat == "cancelled" }

// Crisis compone la vista di crisi di un incidente.
func (s *Service) Crisis(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*CrisisView, error) {
	iv, err := s.IncidentView(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	now := s.now()
	v := &CrisisView{Incident: iv, Applications: []CrisisApp{}, Providers: []CrisisProvider{}, Timeline: []TimelineItem{}, Decisions: []Event{}, Communications: []Event{}}
	providers := map[string]*CrisisProvider{}
	var order []string
	addProvider := func(p Ref, cause bool, app string) {
		cp, ok := providers[p.ID]
		if !ok {
			cp = &CrisisProvider{Ref: p, Applications: []string{}}
			providers[p.ID] = cp
			order = append(order, p.ID)
		}
		cp.Cause = cp.Cause || cause
		if app != "" && !slices.Contains(cp.Applications, app) {
			cp.Applications = append(cp.Applications, app)
		}
	}
	if iv.CauseProvider != nil {
		addProvider(*iv.CauseProvider, true, "")
	}
	for _, imp := range iv.Impacted {
		if imp.Type != AppType {
			continue
		}
		a := CrisisApp{Ref: imp, Risks: []Ref{}, OtherIncidents: []Ref{}, Runbooks: []Ref{}, Dependents: []Ref{}}
		err := tx.QueryRow(ctx, `select a.criticality, a.rto_minutes, t.name from ext_application a
			left join teams t on t.tenant_id = a.tenant_id and t.id = a.owner_team_id where a.element_id = $1`, imp.ID).Scan(&a.Criticality, &a.RTOMinutes, &a.Team)
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		var rel CrisisRelease
		err = tx.QueryRow(ctx, `select r.version, sv.name, en.name, en.is_production, r.released_at
			from releases r join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
			join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id
			where sv.application_element_id = $1 and r.released_at <= $2
			order by en.is_production desc, r.released_at desc limit 1`, imp.ID, now).Scan(&rel.Version, &rel.Service, &rel.Environment, &rel.Production, &rel.At)
		if err == nil {
			rel.Suspect = rel.Production && !rel.At.After(iv.DetectedAt.Add(time.Hour)) && rel.At.After(iv.DetectedAt.Add(-suspectHoursBefore*time.Hour))
			a.LastRelease = &rel
		} else if err != pgx.ErrNoRows {
			return nil, err
		}
		if err := tx.QueryRow(ctx, `select count(*), count(*) filter (where due_at < $2), count(*) filter (where severity = 'critical')
			from findings where application_element_id = $1 and status = 'open' and stage in ('open', 'taken', 'in_progress')`, imp.ID, now).
			Scan(&a.Vulns.Open, &a.Vulns.Overdue, &a.Vulns.Critical); err != nil {
			return nil, err
		}
		risks, err := targetsOf(ctx, tx, RelRiskOf, imp.ID, false)
		if err != nil {
			return nil, err
		}
		for _, r := range risks {
			if r.Type == RiskType && !isClosed(r.StateCat) {
				a.Risks = append(a.Risks, r)
			}
		}
		others, err := targetsOf(ctx, tx, RelImpacts, imp.ID, false)
		if err != nil {
			return nil, err
		}
		for _, o := range others {
			if o.ID != iv.Element.ID && !isClosed(o.StateCat) {
				a.OtherIncidents = append(a.OtherIncidents, o)
			}
		}
		if a.Runbooks, err = documentsOf(ctx, tx, imp.ID); err != nil {
			return nil, err
		}
		if a.Dependents, err = dependentsOf(ctx, tx, imp.ID); err != nil {
			return nil, err
		}
		provs, err := targetsOf(ctx, tx, RelProvidedBy, imp.ID, true)
		if err != nil {
			return nil, err
		}
		for _, p := range provs {
			addProvider(p, iv.CauseProvider != nil && iv.CauseProvider.ID == p.ID, imp.Key)
		}
		v.Applications = append(v.Applications, a)
	}
	for _, id := range order {
		cp := providers[id]
		st, err := statsOf(ctx, tx, cp.Ref.ID)
		if err != nil && err != engine.ErrNotFound {
			return nil, err
		}
		cp.Critical = st.critical()
		cp.NextEnd = dateStr(st.NextEnd)
		v.Providers = append(v.Providers, *cp)
	}

	for _, e := range iv.Events {
		switch e.Kind {
		case "decision":
			v.Decisions = append(v.Decisions, e)
		case "communication":
			v.Communications = append(v.Communications, e)
		}
	}
	slices.Reverse(v.Decisions)
	slices.Reverse(v.Communications)
	if last := lastUpdate(iv.Events); last != nil {
		v.StaleHours = int(now.Sub(*last).Hours())
	}
	if v.Timeline, err = s.crisisTimeline(ctx, tx, iv, v.Applications, now); err != nil {
		return nil, err
	}
	v.Next = nextAction(iv, v.StaleHours, now)
	v.Draft = buildDraft(iv, v.Applications, v.Next)
	return v, nil
}

func lastUpdate(events []Event) *time.Time {
	var last *time.Time
	for _, e := range events {
		if e.System || e.Kind == "detected" {
			continue
		}
		t := e.At
		if last == nil || t.After(*last) {
			last = &t
		}
	}
	return last
}

func documentsOf(ctx context.Context, tx pgx.Tx, appID string) ([]Ref, error) {
	rows, err := tx.Query(ctx, `select `+refColumns+refFrom+`
		join relations r on r.tenant_id = e.tenant_id and r.source_id = e.id
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'documents'
		where r.target_id = $1 and e.deleted_at is null and t.key = 'page'
		order by (lower(e.title) ~ 'runbook|ripristino|disaster|continuit|recovery') desc, e.title limit 4`, appID)
	if err != nil {
		return nil, err
	}
	return scanRefs(rows)
}

// dependentsOf: applicazioni che dipendono direttamente da questa, secondo il verso dei tipi di dipendenza.
func dependentsOf(ctx context.Context, tx pgx.Tx, appID string) ([]Ref, error) {
	rows, err := tx.Query(ctx, `select `+refColumns+refFrom+`
		join relations r on r.tenant_id = e.tenant_id
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.ea_category = 'dependency' and rt.impact_direction <> 'none' and not rt.archived
		where e.deleted_at is null and t.key = 'application'
		  and ((rt.impact_direction = 'forward' and r.target_id = $1 and r.source_id = e.id)
		    or (rt.impact_direction = 'reverse' and r.source_id = $1 and r.target_id = e.id))
		order by e.title limit 12`, appID)
	if err != nil {
		return nil, err
	}
	return scanRefs(rows)
}

func (s *Service) crisisTimeline(ctx context.Context, tx pgx.Tx, iv *IncidentView, apps []CrisisApp, now time.Time) ([]TimelineItem, error) {
	out := []TimelineItem{}
	for _, e := range iv.Events {
		out = append(out, TimelineItem{At: e.At, Kind: e.Kind, Detail: e.Text, System: e.System, By: e.CreatedBy})
	}
	ids := make([]string, 0, len(apps))
	for _, a := range apps {
		ids = append(ids, a.Ref.ID)
	}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `select r.released_at, e.title, coalesce(s.key || '-' || e.number, ''), r.version, en.name
			from releases r join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
			join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id
			join elements e on e.tenant_id = sv.tenant_id and e.id = sv.application_element_id
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where sv.application_element_id = any($1::uuid[]) and en.is_production
			  and r.released_at between $2 and $3 order by r.released_at`, ids, iv.DetectedAt.Add(-72*time.Hour), now)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var it TimelineItem
			var version, env string
			if err := rows.Scan(&it.At, &it.Title, &it.Ref, &version, &env); err != nil {
				rows.Close()
				return nil, err
			}
			it.Kind, it.Detail, it.System = "release", version+" · "+env, true
			out = append(out, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows, err = tx.Query(ctx, `select f.first_seen, coalesce(f.cve, f.rule_ref, ''), f.severity, e.title, coalesce(s.key || '-' || e.number, '')
			from findings f join elements e on e.tenant_id = f.tenant_id and e.id = f.application_element_id
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where f.application_element_id = any($1::uuid[]) and f.severity in ('critical', 'high')
			  and f.first_seen between $2 and $3 order by f.first_seen limit 12`, ids, iv.DetectedAt.Add(-7*24*time.Hour), now)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var it TimelineItem
			var sev, app string
			if err := rows.Scan(&it.At, &it.Title, &sev, &app, &it.Ref); err != nil {
				rows.Close()
				return nil, err
			}
			it.Kind, it.Detail, it.System = "cve", sev+" · "+app, true
			out = append(out, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, n := range iv.Notifications {
		if (n.Status == "pending" || n.Status == "draft") && n.DueAt != nil {
			out = append(out, TimelineItem{At: *n.DueAt, Kind: "deadline", Title: n.StageName, Detail: n.SchemeName, Ref: n.ID, Future: n.DueAt.After(now)})
		}
	}
	slices.SortStableFunc(out, func(a, b TimelineItem) int { return a.At.Compare(b.At) })
	return out, nil
}

// nextAction sceglie il passo successivo: prima classificare, poi la notifica in scadenza, poi
// contenere, aggiornare, risolvere, chiudere e il postmortem.
func nextAction(iv *IncidentView, staleHours int, now time.Time) NextAction {
	closed := isClosed(iv.Element.StateCategory)
	if closed {
		if iv.Postmortem == nil {
			return NextAction{Kind: "postmortem"}
		}
		return NextAction{Kind: "none"}
	}
	if iv.ClassifiedAt == nil {
		return NextAction{Kind: "classify"}
	}
	var next *Notification
	for i := range iv.Notifications {
		n := &iv.Notifications[i]
		if (n.Status == "pending" || n.Status == "draft") && n.DueAt != nil && (next == nil || n.DueAt.Before(*next.DueAt)) {
			next = n
		}
	}
	if next != nil {
		return NextAction{Kind: "notify", DueAt: next.DueAt, Notification: next.ID}
	}
	if iv.ResolvedAt != nil {
		return NextAction{Kind: "close"}
	}
	if iv.ContainedAt == nil {
		return NextAction{Kind: "contain"}
	}
	if staleHours >= StaleUpdateHours {
		return NextAction{Kind: "update"}
	}
	return NextAction{Kind: "resolve"}
}

func buildDraft(iv *IncidentView, apps []CrisisApp, next NextAction) CrisisDraft {
	d := CrisisDraft{Fields: []DraftField{}}
	for i := range iv.Notifications {
		if iv.Notifications[i].ID == next.Notification && next.Notification != "" {
			n := iv.Notifications[i]
			d.Notification = &n
		}
	}
	add := func(key, kind, value string, required bool) {
		f := DraftField{Key: key, Kind: kind, Value: value, Missing: strings.TrimSpace(value) == "", Required: required}
		if f.Missing && required {
			d.Missing++
		}
		d.Fields = append(d.Fields, f)
	}
	num := func(n *int) string {
		if n == nil {
			return ""
		}
		return fmt.Sprint(*n)
	}
	add("incident", "text", iv.Key+" · "+iv.Element.Title, true)
	add("detected_at", "date", iv.DetectedAt.UTC().Format(time.RFC3339), true)
	outcomes := []string{}
	refs := []string{}
	for _, c := range iv.Classification {
		if c.Positive {
			outcomes = append(outcomes, c.Name+": "+c.Outcome)
			if c.Reference != "" {
				refs = append(refs, c.Reference)
			}
		}
	}
	add("classification", "list", strings.Join(outcomes, "\n"), true)
	add("legal_reference", "list", strings.Join(refs, "\n"), false)
	add("description", "text", strings.TrimSpace(iv.Element.BodyText), true)
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Ref.Key+" "+a.Ref.Title)
	}
	add("applications", "list", strings.Join(names, "\n"), true)
	add("clients", "number", num(iv.ClientsAffected), true)
	pct := ""
	if iv.ClientsPct != nil {
		pct = fmt.Sprint(*iv.ClientsPct)
	}
	add("clients_pct", "number", pct, false)
	add("downtime", "minutes", num(iv.DowntimeMinutes), true)
	add("data_loss", "list", strings.Join(iv.DataLoss, "\n"), false)
	money := ""
	if iv.EconomicCents != nil {
		money = fmt.Sprint(*iv.EconomicCents)
	}
	add("economic", "money", money, false)
	add("member_states", "number", num(iv.MemberStates), false)
	cause := ""
	if iv.Cause != nil {
		cause = iv.CauseName
		if iv.CauseProvider != nil {
			cause += " · " + iv.CauseProvider.Title
		}
	}
	add("cause", "text", cause, true)
	lead := ""
	if iv.Lead != nil {
		lead = *iv.Lead
	}
	add("lead", "text", lead, true)
	return d
}
