package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Incidenti: criteri di impatto, timeline, applicazioni impattate, classificazione con gli
// schemi del tenant, notifiche normative con scadenze, postmortem e azioni correttive.

var dataLoss = map[string]string{"availability": "disponibilità", "integrity": "integrità", "confidentiality": "riservatezza", "authenticity": "autenticità"}
var causes = map[string]string{"cyber": "attacco informatico", "process": "errore di processo", "system": "guasto di sistema", "external": "evento esterno", "human": "errore umano", "provider": "fornitore terzo"}
var eventKinds = []string{"detected", "classified", "contained", "resolved", "update", "communication", "decision", "notification", "action", "closed"}

func causeNames() []string {
	out := make([]string, 0, len(causes))
	for _, v := range causes {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

func normFrom(m map[string]string, s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	for en, it := range m {
		if s == en || s == it {
			return en, true
		}
	}
	return "", false
}

// incidentRow è la riga di ext_incident con i dati dell'elemento.
type incidentRow struct {
	ID, Key, Title, Body      string
	Phase, PhaseCat           string
	DetectedAt                time.Time
	ClassifiedAt, ContainedAt *time.Time
	ResolvedAt                *time.Time
	Major                     bool
	Classification            map[string]SchemeResult
	ClientsAffected           *int
	ClientsPct                *float64
	DowntimeMinutes           *int
	DataLoss                  []string
	EconomicCents             *int64
	MemberStates              *int
	Reputational, Recurring   bool
	Cause                     *string
	Postmortem                *Postmortem
	PostmortemAt              *time.Time
	PostmortemBy              *string
}

// Postmortem è l'analisi dopo la risoluzione.
type Postmortem struct {
	Summary   string `json:"summary"`
	RootCause string `json:"root_cause"`
	Lessons   string `json:"lessons"`
}

const selectIncident = `
	select x.element_id::text, s.key || '-' || e.number, e.title, e.body_text, coalesce(o.name, ''), coalesce(o.category, ''),
	       x.detected_at, x.classified_at, x.contained_at, x.resolved_at, x.is_major, x.classification,
	       x.clients_affected, x.clients_affected_pct::float8, x.downtime_minutes, x.data_loss, x.economic_impact_cents, x.member_states,
	       x.reputational, x.recurring, x.cause, x.postmortem, x.postmortem_at, m.display_name
	from ext_incident x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	left join members m on m.tenant_id = x.tenant_id and m.id = x.postmortem_by`

func scanIncident(row pgx.Row) (*incidentRow, error) {
	r := &incidentRow{}
	var class, pm []byte
	err := row.Scan(&r.ID, &r.Key, &r.Title, &r.Body, &r.Phase, &r.PhaseCat, &r.DetectedAt, &r.ClassifiedAt, &r.ContainedAt, &r.ResolvedAt,
		&r.Major, &class, &r.ClientsAffected, &r.ClientsPct, &r.DowntimeMinutes, &r.DataLoss, &r.EconomicCents, &r.MemberStates,
		&r.Reputational, &r.Recurring, &r.Cause, &pm, &r.PostmortemAt, &r.PostmortemBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Classification = map[string]SchemeResult{}
	if len(class) > 0 {
		if err := json.Unmarshal(class, &r.Classification); err != nil {
			return nil, err
		}
	}
	if len(pm) > 0 && string(pm) != "null" {
		r.Postmortem = &Postmortem{}
		if err := json.Unmarshal(pm, r.Postmortem); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func loadIncident(ctx context.Context, tx pgx.Tx, id string, lock bool) (*incidentRow, error) {
	q := selectIncident + " where x.element_id = $1"
	if lock {
		q += " for update of x"
	}
	return scanIncident(tx.QueryRow(ctx, q, id))
}

// IncidentInput crea o modifica un incidente. Nella modifica solo i campi valorizzati cambiano.
type IncidentInput struct {
	Title           *string  `json:"title"`
	Description     *string  `json:"description"`
	DetectedAt      *string  `json:"detected_at"`
	ContainedAt     *string  `json:"contained_at"`
	ResolvedAt      *string  `json:"resolved_at"`
	ClientsAffected *int     `json:"clients_affected"`
	ClientsPct      *float64 `json:"clients_affected_pct"`
	DowntimeMinutes *int     `json:"downtime_minutes"`
	DataLoss        []string `json:"data_loss"`
	EconomicCents   *int64   `json:"economic_impact_cents"`
	MemberStates    *int     `json:"member_states"`
	Reputational    *bool    `json:"reputational"`
	Recurring       *bool    `json:"recurring"`
	Cause           *string  `json:"cause"`
	// CauseProvider: il fornitore ICT (chiave o id) quando la causa è «fornitore terzo»; vuoto lo toglie.
	CauseProvider *string  `json:"cause_provider"`
	LeadID        *string  `json:"lead_id"`
	Impacted      []string `json:"impacted"` // solo alla creazione: applicazioni o asset (chiavi o id)
}

func parseTime(field, v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, engine.InvalidCode("validation.risk.datetime_invalid", field, "data e ora non valide (ISO 8601, es. 2026-11-24T09:30:00Z)")
}

func (in *IncidentInput) validate() error {
	for f, v := range map[string]*int{"clients_affected": in.ClientsAffected, "downtime_minutes": in.DowntimeMinutes, "member_states": in.MemberStates} {
		if v != nil && *v < 0 {
			return engine.InvalidCode("validation.not_negative", f, "non può essere negativo")
		}
	}
	if in.ClientsPct != nil && (*in.ClientsPct < 0 || *in.ClientsPct > 100) {
		return engine.InvalidCode("validation.percent_range", "clients_affected_pct", "percentuale tra 0 e 100")
	}
	if in.EconomicCents != nil && *in.EconomicCents < 0 {
		return engine.InvalidCode("validation.not_negative", "economic_impact_cents", "non può essere negativo")
	}
	if in.DataLoss != nil {
		out := []string{}
		for _, d := range in.DataLoss {
			k, ok := normFrom(dataLoss, d)
			if !ok {
				return engine.InvalidValue("data_loss", "perdita di dati %q non ammessa (disponibilità, integrità, riservatezza, autenticità)", d)
			}
			if k != "" && !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
		in.DataLoss = out
	}
	if in.Cause != nil {
		k, ok := normFrom(causes, *in.Cause)
		if !ok {
			return engine.InvalidValue("cause", "causa non ammessa")
		}
		in.Cause = &k
	}
	return nil
}

// canReport: chi crea elementi può segnalare un incidente; chi gestisce gli incidenti anche.
func canReport(actor engine.Actor) bool {
	return actor.Has(rbac.ElementWrite) || actor.Has(PermIncident)
}

// CreateIncident registra un incidente (element.write o incident.manage).
func (s *Service) CreateIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, in IncidentInput) (*IncidentView, error) {
	if !canReport(actor) {
		return nil, engine.ErrForbidden
	}
	if ok, err := typeExists(ctx, tx, IncidentType); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.InvalidCode("validation.risk.incidents_not_configured", "type", "gli incidenti non sono configurati in questo tenant")
	}
	if in.Title == nil {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	detected, err := parseTime("detected_at", deref(in.DetectedAt))
	if err != nil {
		return nil, err
	}
	if detected == nil {
		n := s.now()
		detected = &n
	}
	if detected.After(s.now().Add(5 * time.Minute)) {
		return nil, engine.InvalidCode("validation.risk.detected_future", "detected_at", "la rilevazione non può essere nel futuro")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: IncidentSpace, TypeKey: IncidentType, Title: *in.Title,
		BodyText: deref(in.Description), DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_incident (tenant_id, element_id, detected_at) values (app_tenant(), $1, $2)`, el.ID, detected); err != nil {
		return nil, err
	}
	in.DetectedAt = nil
	if err := s.writeIncident(ctx, tx, el.ID, in); err != nil {
		return nil, err
	}
	if err := s.writeCauseProvider(ctx, tx, actor, el.ID, in); err != nil {
		return nil, err
	}
	lead := deref(in.LeadID)
	if lead == "" && actor.Has(PermIncident) {
		lead = actor.MemberID
	}
	if err := setOwner(ctx, tx, actor, el.ID, RoleIncidentLead, lead); err != nil {
		return nil, err
	}
	for _, t := range in.Impacted {
		if err := s.linkImpacted(ctx, tx, actor, el.ID, t); err != nil {
			return nil, err
		}
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "incident.create", IncidentType, el.ID, map[string]any{"detected_at": detected.Format(time.RFC3339)}); err != nil {
		return nil, err
	}
	return s.IncidentView(ctx, tx, actor, el.ID)
}

func (s *Service) writeIncident(ctx context.Context, tx pgx.Tx, id string, in IncidentInput) error {
	detected, err := parseTime("detected_at", deref(in.DetectedAt))
	if err != nil {
		return err
	}
	contained, err := parseTime("contained_at", deref(in.ContainedAt))
	if err != nil {
		return err
	}
	resolved, err := parseTime("resolved_at", deref(in.ResolvedAt))
	if err != nil {
		return err
	}
	var dl []string
	if in.DataLoss != nil {
		dl = in.DataLoss
	}
	_, err = tx.Exec(ctx, `update ext_incident set
		detected_at = coalesce($2, detected_at),
		contained_at = case when $14 then $3::timestamptz else contained_at end,
		resolved_at = case when $15 then $4::timestamptz else resolved_at end,
		clients_affected = coalesce($5, clients_affected), clients_affected_pct = coalesce($6, clients_affected_pct),
		downtime_minutes = coalesce($7, downtime_minutes), data_loss = coalesce($8, data_loss),
		economic_impact_cents = coalesce($9, economic_impact_cents), member_states = coalesce($10, member_states),
		reputational = coalesce($11, reputational), recurring = coalesce($12, recurring),
		cause = case when $16 then nullif($13, '') else cause end
		where element_id = $1`, id, detected, contained, resolved, in.ClientsAffected, in.ClientsPct, in.DowntimeMinutes, dl,
		in.EconomicCents, in.MemberStates, in.Reputational, in.Recurring, deref(in.Cause), in.ContainedAt != nil, in.ResolvedAt != nil, in.Cause != nil)
	return err
}

// writeCauseProvider registra il fornitore ICT che ha causato l'incidente. Se la causa non è
// «fornitore terzo» il fornitore si toglie; se si indica un fornitore senza causa, la causa
// diventa «fornitore terzo».
func (s *Service) writeCauseProvider(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in IncidentInput) error {
	if in.Cause != nil && *in.Cause != "provider" {
		_, err := tx.Exec(ctx, "update ext_incident set cause_provider_id = null where element_id = $1", id)
		return err
	}
	if in.CauseProvider == nil {
		return nil
	}
	ref := strings.TrimSpace(*in.CauseProvider)
	if ref == "" {
		_, err := tx.Exec(ctx, "update ext_incident set cause_provider_id = null where element_id = $1", id)
		return err
	}
	p, err := s.resolveTyped(ctx, tx, actor, ref, ProviderType)
	if errors.Is(err, engine.ErrNotFound) {
		return engine.NotFound("cause_provider", "provider", "fornitore %q inesistente", ref)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "update ext_incident set cause_provider_id = $2, cause = 'provider' where element_id = $1", id, p.ID)
	return err
}

// UpdateIncident modifica criteri e momenti dell'incidente (incident.manage). Se è già
// classificato, la classificazione si ripete: un incidente può diventare grave dopo.
func (s *Service) UpdateIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in IncidentInput) (*IncidentView, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	cur, err := loadIncident(ctx, tx, el.ID, true)
	if err != nil {
		return nil, err
	}
	if in.Title != nil || in.Description != nil {
		if _, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: el.Version, Title: in.Title, BodyText: in.Description}); err != nil {
			return nil, err
		}
	}
	if err := s.writeIncident(ctx, tx, el.ID, in); err != nil {
		return nil, err
	}
	if err := s.writeCauseProvider(ctx, tx, actor, el.ID, in); err != nil {
		return nil, err
	}
	if in.LeadID != nil {
		if err := setOwner(ctx, tx, actor, el.ID, RoleIncidentLead, *in.LeadID); err != nil {
			return nil, err
		}
	}
	for _, t := range []struct {
		v    *string
		prev *time.Time
		kind string
		text string
	}{{in.ContainedAt, cur.ContainedAt, "contained", "Incidente contenuto"}, {in.ResolvedAt, cur.ResolvedAt, "resolved", "Servizio ripristinato"}} {
		if at, _ := parseTime("", deref(t.v)); at != nil && (t.prev == nil || !t.prev.Equal(*at)) {
			if err := addEvent(ctx, tx, actor, el.ID, *at, t.kind, t.text, true); err != nil {
				return nil, err
			}
		}
	}
	if err := audited(ctx, tx, actor, "incident.update", IncidentType, el.ID, nil); err != nil {
		return nil, err
	}
	if cur.ClassifiedAt != nil {
		if _, err := s.classify(ctx, tx, actor, el.ID, true); err != nil {
			return nil, err
		}
	}
	var move *PhaseMove
	if at, _ := parseTime("", deref(in.ResolvedAt)); at != nil && cur.ResolvedAt == nil {
		if move, err = s.advanceOnResolution(ctx, tx, actor, el.ID); err != nil {
			return nil, err
		}
	}
	v, err := s.IncidentView(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	v.PhaseMove = move
	return v, nil
}

// PhaseMove dice che cosa è successo alla fase dell'incidente quando si è registrata la
// risoluzione: «moved» se è passato alla fase di risoluzione, «choose» se il processo non lo
// consente da lì e deve scegliere la persona tra i passaggi disponibili.
type PhaseMove struct {
	Status  string            `json:"status"` // moved | choose
	Phase   string            `json:"phase,omitempty"`
	Target  string            `json:"target,omitempty"` // la fase di risoluzione del processo, se c'è
	Message string            `json:"message"`
	Options []PhaseMoveOption `json:"options"`
}

// PhaseMoveOption è un passaggio avanti disponibile all'attore.
type PhaseMoveOption struct {
	To    string `json:"to"`
	Label string `json:"label"`
}

// advanceOnResolution porta l'incidente alla fase di risoluzione del suo processo (quella la
// cui regola d'ingresso registra la risoluzione) se c'è un passaggio diretto e libero da lì;
// altrimenti elenca i passaggi avanti possibili perché la persona scelga. Nessun effetto se
// l'incidente è già in quella fase o oltre.
func (s *Service) advanceOnResolution(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*PhaseMove, error) {
	el, err := s.eng.Get(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	target, err := s.eng.EntryPhaseWithAction(ctx, tx, el, ActionIncident, "resolved")
	if err != nil {
		return nil, err
	}
	if target != "" {
		beyond, err := s.eng.AtOrBeyondPhase(ctx, tx, el, target)
		if err != nil || beyond {
			return nil, err
		}
	} else if el.StateCategory == "done" || el.StateCategory == "cancelled" {
		return nil, nil
	}
	avail, err := s.eng.Available(ctx, tx, actor, el)
	if err != nil {
		return nil, err
	}
	if target != "" {
		for _, t := range avail {
			if strings.EqualFold(t.To, target) && len(t.Blocked) == 0 && (!t.CommentRequired) {
				if _, err := s.eng.Transition(ctx, tx, actor, el.ID, t.To, "", engine.TransitionOpts{}); err != nil {
					return nil, err
				}
				return &PhaseMove{Status: "moved", Phase: t.To, Target: target, Options: []PhaseMoveOption{},
					Message: fmt.Sprintf("Risoluzione registrata: l'incidente è passato alla fase «%s».", t.To)}, nil
			}
		}
	}
	mv := &PhaseMove{Status: "choose", Target: target, Options: []PhaseMoveOption{}}
	for _, t := range avail {
		if t.Forward && len(t.Blocked) == 0 {
			mv.Options = append(mv.Options, PhaseMoveOption{To: t.To, Label: t.Label})
		}
	}
	switch {
	case len(mv.Options) == 0:
		mv.Message = fmt.Sprintf("Risoluzione registrata, ma l'incidente resta in fase «%s» e non hai un passaggio avanti disponibile: chiedi a chi gestisce l'incidente di portarlo avanti.", el.State)
	case target != "":
		mv.Message = fmt.Sprintf("Risoluzione registrata. Dalla fase «%s» il processo non porta direttamente a «%s»: scegli la fase.", el.State, target)
	default:
		mv.Message = "Risoluzione registrata. Il processo non ha una fase di risoluzione: scegli in quale fase portare l'incidente."
	}
	return mv, nil
}

func (s *Service) linkImpacted(ctx context.Context, tx pgx.Tx, actor engine.Actor, incidentID, target string) error {
	t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(target))
	if errors.Is(err, engine.ErrNotFound) {
		return engine.NotFound("target", "element", "elemento %q inesistente", target)
	}
	if err != nil {
		return err
	}
	switch t.TypeKey {
	case AppType, AssetType:
		_, err = relate(ctx, tx, actor, RelImpacts, incidentID, t.ID)
	case RiskType:
		_, err = relate(ctx, tx, actor, RelMaterializes, incidentID, t.ID)
	default:
		return engine.InvalidParams("validation.risk.incident_link_kind", "target", map[string]any{"kind": t.TypeName}, "un incidente si collega ad applicazioni, asset (servizi) o rischi (non a %s)", t.TypeName)
	}
	return err
}

// LinkIncident collega un'applicazione o un asset impattato, oppure un rischio che si è
// concretizzato (incident.manage). Se l'incidente è classificato si riclassifica.
func (s *Service) LinkIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, target string, remove bool) (*IncidentView, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	if remove {
		t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(target))
		if err != nil {
			return nil, err
		}
		rel := RelImpacts
		if t.TypeKey == RiskType {
			rel = RelMaterializes
		}
		if err := unrelate(ctx, tx, actor, rel, el.ID, t.ID); err != nil {
			return nil, err
		}
	} else if err := s.linkImpacted(ctx, tx, actor, el.ID, target); err != nil {
		return nil, err
	}
	cur, err := loadIncident(ctx, tx, el.ID, false)
	if err != nil {
		return nil, err
	}
	if cur.ClassifiedAt != nil {
		if _, err := s.classify(ctx, tx, actor, el.ID, true); err != nil {
			return nil, err
		}
	}
	return s.IncidentView(ctx, tx, actor, el.ID)
}

// ---------------------------------------------------------------------------
// Timeline

// Event è una voce della timeline.
type Event struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	System    bool      `json:"system"`
	CreatedBy *string   `json:"created_by"`
}

func addEvent(ctx context.Context, tx pgx.Tx, actor engine.Actor, incidentID string, at time.Time, kind, text string, system bool) error {
	_, err := tx.Exec(ctx, `insert into incident_events (tenant_id, id, incident_id, at, kind, text, system, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`, db.NewID(), incidentID, at, kind, text, system, nullable(actor.MemberID))
	return err
}

// AddEvent aggiunge una voce alla timeline (incident.manage).
func (s *Service) AddEvent(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, at, kind, text string) (*IncidentView, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind = "update"
	}
	if !slices.Contains(eventKinds, kind) {
		return nil, engine.InvalidValue("kind", "tipo di evento non ammesso (%s)", strings.Join(eventKinds, ", "))
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 4000 {
		return nil, engine.TooLong("text", 4000, "descrivi l'evento (massimo 4000 caratteri)")
	}
	t, err := parseTime("at", at)
	if err != nil {
		return nil, err
	}
	if t == nil {
		n := s.now()
		t = &n
	}
	if t.After(s.now().Add(5 * time.Minute)) {
		return nil, engine.InvalidCode("validation.risk.timeline_future", "at", "un evento della timeline non può essere nel futuro")
	}
	if err := addEvent(ctx, tx, actor, el.ID, *t, kind, text, false); err != nil {
		return nil, err
	}
	if kind == "contained" {
		if _, err := tx.Exec(ctx, "update ext_incident set contained_at = coalesce(contained_at, $2) where element_id = $1", el.ID, t); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "incident.event", IncidentType, el.ID, map[string]any{"kind": kind}); err != nil {
		return nil, err
	}
	return s.IncidentView(ctx, tx, actor, el.ID)
}

func events(ctx context.Context, tx pgx.Tx, incidentID string) ([]Event, error) {
	rows, err := tx.Query(ctx, `select v.id::text, v.at, v.kind, v.text, v.system, m.display_name from incident_events v
		left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by
		where v.incident_id = $1 order by v.at, v.created_at`, incidentID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
	if out == nil {
		out = []Event{}
	}
	return out, err
}

// ---------------------------------------------------------------------------
// Fatti

func (r *incidentRow) durationHours(now time.Time) float64 {
	end := now
	if r.ResolvedAt != nil {
		end = *r.ResolvedAt
	}
	h := end.Sub(r.DetectedAt).Hours()
	if h < 0 {
		return 0
	}
	return float64(int(h*10)) / 10
}

func (s *Service) incidentFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	r, err := loadIncident(ctx, tx, el.ID, false)
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.ClientsAffected != nil {
		f.Set("clienti_coinvolti", *r.ClientsAffected)
	}
	f.Set("clienti_coinvolti_pct", r.ClientsPct)
	f.Set("durata_ore", r.durationHours(s.now()))
	if r.DowntimeMinutes != nil {
		f.Set("fermo_minuti", *r.DowntimeMinutes)
	}
	var loss []any
	for _, d := range r.DataLoss {
		loss = append(loss, dataLoss[d], d)
	}
	f.Set("perdita_dati", loss...)
	if r.EconomicCents != nil {
		f.Set("impatto_economico", float64(*r.EconomicCents)/100)
	}
	if r.MemberStates != nil {
		f.Set("stati_membri", *r.MemberStates)
	}
	f.Set("impatto_reputazionale", r.Reputational)
	f.Set("ricorrente", r.Recurring)
	if r.Cause != nil {
		f.Set("causa", causes[*r.Cause], *r.Cause)
	}
	impacted, err := targetsOf(ctx, tx, RelImpacts, el.ID, true)
	if err != nil {
		return err
	}
	critical := false
	var apps []any
	for _, a := range impacted {
		critical = critical || a.Critical
		apps = append(apps, a.Title, a.Key)
	}
	// Anche un asset (servizio) è critico se lo compone un'applicazione con funzione critica.
	var assetCritical bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		join relations c on c.tenant_id = r.tenant_id and c.source_id = r.target_id
		join relation_types ct on ct.tenant_id = c.tenant_id and ct.id = c.type_id and ct.key in ('composed_of', 'uses')
		join ext_application a on a.tenant_id = c.tenant_id and a.element_id = c.target_id and a.supports_critical_function
		where r.source_id = $1)`, el.ID, RelImpacts).Scan(&assetCritical); err != nil {
		return err
	}
	f.Set("funzione_critica", critical || assetCritical)
	f.Set("applicazioni", apps...)
	var maxCrit *string
	if err := tx.QueryRow(ctx, `select a.criticality from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		join ext_application a on a.tenant_id = r.tenant_id and a.element_id = r.target_id
		where r.source_id = $1 and a.criticality is not null
		order by array_position(array['low','medium','high','critical'], a.criticality) desc limit 1`, el.ID, RelImpacts).Scan(&maxCrit); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if maxCrit != nil {
		f.Set("criticita_max", critNames[*maxCrit], *maxCrit)
	}
	var positive []any
	for k, res := range r.Classification {
		if res.Positive {
			positive = append(positive, k, res.Name)
		}
	}
	f.Set("grave", r.Major)
	f.Set("classificazione", positive...)
	var open, actions, actionsOpen int
	if err := tx.QueryRow(ctx, `select
		(select count(*) from regulatory_notifications where incident_element_id = $1 and status in ('pending', 'draft')),
		(select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		   join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null where r.target_id = $1),
		(select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		   join elements e on e.tenant_id = r.tenant_id and e.id = r.source_id and e.deleted_at is null
		   left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		   where r.target_id = $1 and coalesce(o.category, '') not in ('done', 'cancelled'))`, el.ID, RelCorrective).Scan(&open, &actions, &actionsOpen); err != nil {
		return err
	}
	f.Set("notifiche_aperte", open)
	f.Set("azioni_correttive", actions)
	f.Set("azioni_aperte", actionsOpen)
	if r.Postmortem != nil && strings.TrimSpace(r.Postmortem.Summary) != "" {
		f.Set("postmortem", true)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Classificazione

// CriterionResult è l'esito di un criterio.
type CriterionResult struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Met     bool   `json:"met"`
	Primary bool   `json:"primary,omitempty"`
}

// SchemeResult è l'esito di uno schema su un incidente.
type SchemeResult struct {
	Key       string            `json:"key"`
	Name      string            `json:"name"`
	Outcome   string            `json:"outcome"`
	Positive  bool              `json:"positive"`
	Criteria  []CriterionResult `json:"criteria"`
	Reference string            `json:"reference,omitempty"`
	At        time.Time         `json:"at"`
}

func evalScheme(sc Scheme, base engine.Facts) SchemeResult {
	f := engine.Facts{}
	for k, v := range base {
		f[k] = v
	}
	res := SchemeResult{Key: sc.Key, Name: sc.Name, Reference: sc.Reference, Criteria: []CriterionResult{}}
	met, prim, sec := 0, 0, 0
	for _, c := range sc.Criteria {
		cond, err := engine.ParseCondition(c.Condition)
		ok := err == nil && cond.Eval(base)
		f.Set("criterio."+c.Key, ok)
		res.Criteria = append(res.Criteria, CriterionResult{Key: c.Key, Name: c.Name, Met: ok, Primary: c.Primary})
		if ok {
			met++
			if c.Primary {
				prim++
			} else {
				sec++
			}
		}
	}
	f.Set("criteri_soddisfatti", met)
	f.Set("criteri_primari", prim)
	f.Set("criteri_secondari", sec)
	when, err := engine.ParseCondition(sc.When)
	res.Positive = err == nil && when.Eval(f)
	res.Outcome = "non " + sc.OutcomeName
	if res.Positive {
		res.Outcome = sc.OutcomeName
	}
	return res
}

// Classification è l'esito della classificazione: uno per schema attivo.
type Classification struct {
	Results []SchemeResult `json:"results"`
	Major   bool           `json:"major"`
	// Created: notifiche generate da questa classificazione.
	Created int `json:"created"`
}

// Classify applica gli schemi all'incidente. Con dryRun restituisce l'esito senza salvarlo
// (anteprima); altrimenti salva, segna la classificazione nella timeline e genera le
// notifiche degli schemi con esito positivo (incident.manage).
func (s *Service) Classify(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, dryRun bool) (*Classification, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return s.evaluate(ctx, tx, el)
	}
	return s.classify(ctx, tx, actor, el.ID, false)
}

func (s *Service) evaluate(ctx context.Context, tx pgx.Tx, el *engine.Element) (*Classification, error) {
	facts, err := s.eng.ElementFacts(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	schemes, err := s.Schemes(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	out := &Classification{Results: []SchemeResult{}}
	now := s.now()
	for _, sc := range schemes {
		res := evalScheme(sc, facts)
		res.At = now
		out.Results = append(out.Results, res)
		out.Major = out.Major || res.Positive
	}
	return out, nil
}

// classify salva la classificazione e allinea le notifiche. again = riclassificazione dopo
// una modifica (nella timeline solo se l'esito cambia).
func (s *Service) classify(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, again bool) (*Classification, error) {
	el, err := s.eng.Get(ctx, tx, reader(actor.MemberID), id)
	if err != nil {
		return nil, err
	}
	prev, err := loadIncident(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	c, err := s.evaluate(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	stored := map[string]SchemeResult{}
	for _, r := range c.Results {
		stored[r.Key] = r
	}
	raw, _ := json.Marshal(stored)
	now := s.now()
	if _, err := tx.Exec(ctx, `update ext_incident set classification = $2, is_major = $3, classified_at = coalesce(classified_at, $4) where element_id = $1`,
		id, raw, c.Major, now); err != nil {
		return nil, err
	}
	if prev.ClassifiedAt == nil {
		prev.ClassifiedAt = &now
	}
	changed := !again || prev.Major != c.Major
	for _, r := range c.Results {
		if p, ok := prev.Classification[r.Key]; !ok || p.Positive != r.Positive {
			changed = true
		}
	}
	schemes, err := s.Schemes(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	var summary []string
	for _, sc := range schemes {
		res := stored[sc.Key]
		summary = append(summary, sc.Name+": "+res.Outcome)
		if res.Positive {
			n, err := s.ensureNotifications(ctx, tx, actor, prev, sc)
			if err != nil {
				return nil, err
			}
			c.Created += n
			if n > 0 {
				if err := s.notifyIncident(ctx, tx, actor, id, map[string]any{"event": "incident_classified", "scheme": sc.Name, "outcome": sc.OutcomeName}); err != nil {
					return nil, err
				}
			}
		} else if _, err := tx.Exec(ctx, `update regulatory_notifications set status = 'not_required', note = 'Riclassificato: esito non più positivo', updated_at = now()
			where incident_element_id = $1 and scheme_key = $2 and status in ('pending', 'draft')`, id, sc.Key); err != nil {
			return nil, err
		}
	}
	if changed {
		if err := addEvent(ctx, tx, actor, id, now, "classified", strings.Join(summary, " · "), true); err != nil {
			return nil, err
		}
		if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "incident", "classified", map[string]any{"major": c.Major, "summary": strings.Join(summary, " · ")}, nil); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "incident.classify", IncidentType, id, map[string]any{"major": c.Major, "notifications": c.Created}); err != nil {
		return nil, err
	}
	return c, nil
}

// ensureNotifications crea le notifiche dello schema che mancano (o riattiva quelle chiuse
// da una riclassificazione) e ne calcola le scadenze. Restituisce quante sono nuove.
func (s *Service) ensureNotifications(ctx context.Context, tx pgx.Tx, actor engine.Actor, inc *incidentRow, sc Scheme) (int, error) {
	created := 0
	for i, st := range sc.Stages {
		rule, _ := json.Marshal(st)
		var inserted bool
		err := tx.QueryRow(ctx, `insert into regulatory_notifications (tenant_id, id, incident_element_id, scheme_key, scheme_name, stage_key, stage_name,
			ordinal, due_rule, payload) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (tenant_id, incident_element_id, scheme_key, stage_key) where scheme_key is not null do update
			set status = case when regulatory_notifications.status = 'not_required' and regulatory_notifications.note like 'Riclassificato%' then 'pending'
			                  else regulatory_notifications.status end,
			    note = case when regulatory_notifications.status = 'not_required' and regulatory_notifications.note like 'Riclassificato%' then null
			                else regulatory_notifications.note end
			returning (xmax = 0)`,
			db.NewID(), inc.ID, sc.Key, sc.Name, st.Key, st.Name, i+1, rule, map[string]any{"hint": st.Hint, "reference": sc.Reference}).Scan(&inserted)
		if err != nil {
			return 0, err
		}
		if inserted {
			created++
		}
	}
	if created > 0 {
		if err := addEvent(ctx, tx, actor, inc.ID, s.now(), "notification", fmt.Sprintf("%s: %d notifiche da preparare", sc.Name, created), true); err != nil {
			return 0, err
		}
	}
	return created, s.recomputeDue(ctx, tx, inc.ID)
}

// recomputeDue ricalcola le scadenze delle notifiche non inviate di un incidente: ogni
// termine parte dalla rilevazione, dalla classificazione o dall'invio di una tappa
// precedente; vale il primo. Se manca il punto di partenza (tappa precedente non inviata)
// la scadenza resta vuota.
func (s *Service) recomputeDue(ctx context.Context, tx pgx.Tx, incidentID string) error {
	inc, err := loadIncident(ctx, tx, incidentID, false)
	if err != nil {
		return err
	}
	list, err := notificationsOf(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	submitted := map[string]time.Time{}
	for _, n := range list {
		if n.SubmittedAt != nil {
			submitted[n.SchemeKey+"/"+n.StageKey] = *n.SubmittedAt
		}
	}
	for _, n := range list {
		if n.Status == "submitted" || n.Status == "not_required" || n.rule == nil {
			continue
		}
		var due *time.Time
		for _, d := range n.rule.Due {
			var from *time.Time
			switch {
			case d.From == "detected":
				from = &inc.DetectedAt
			case d.From == "classified":
				from = inc.ClassifiedAt
			case strings.HasPrefix(d.From, "stage:"):
				if t, ok := submitted[n.SchemeKey+"/"+strings.TrimPrefix(d.From, "stage:")]; ok {
					from = &t
				}
			}
			if from == nil {
				continue
			}
			t := from.Add(d.duration())
			if due == nil || t.Before(*due) {
				due = &t
			}
		}
		if _, err := tx.Exec(ctx, `update regulatory_notifications set due_at = $2, updated_at = now() where id = $1 and due_at is distinct from $2`,
			n.ID, due); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Notifiche normative

// Notification è una notifica normativa di un incidente.
type Notification struct {
	ID          string     `json:"id"`
	IncidentID  string     `json:"incident_id"`
	IncidentKey string     `json:"incident_key"`
	Incident    string     `json:"incident"`
	SchemeKey   string     `json:"scheme_key"`
	SchemeName  string     `json:"scheme_name"`
	StageKey    string     `json:"stage_key"`
	StageName   string     `json:"stage_name"`
	Ordinal     int        `json:"ordinal"`
	Status      string     `json:"status"`
	DueAt       *time.Time `json:"due_at"`
	Content     string     `json:"content"`
	Hint        string     `json:"hint"`
	Reference   *string    `json:"reference"`
	Channel     *string    `json:"channel"`
	Note        *string    `json:"note"`
	SubmittedAt *time.Time `json:"submitted_at"`
	SubmittedBy *string    `json:"submitted_by"`
	// Seconds: secondi alla scadenza (negativi = scaduta), al momento della lettura.
	Seconds *int64 `json:"seconds_left"`
	rule    *Stage
}

const selectNotification = `
	select n.id::text, n.incident_element_id::text, s.key || '-' || e.number, e.title, coalesce(n.scheme_key, ''), coalesce(n.scheme_name, ''),
	       coalesce(n.stage_key, ''), coalesce(n.stage_name, ''), n.ordinal, n.status, n.due_at, n.content, coalesce(n.payload->>'hint', ''),
	       n.reference, n.channel, n.note, n.submitted_at, m.display_name, n.due_rule
	from regulatory_notifications n
	join elements e on e.tenant_id = n.tenant_id and e.id = n.incident_element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join members m on m.tenant_id = n.tenant_id and m.id = n.submitted_by`

func scanNotifications(rows pgx.Rows, now time.Time) ([]Notification, error) {
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var rule []byte
		if err := rows.Scan(&n.ID, &n.IncidentID, &n.IncidentKey, &n.Incident, &n.SchemeKey, &n.SchemeName, &n.StageKey, &n.StageName, &n.Ordinal,
			&n.Status, &n.DueAt, &n.Content, &n.Hint, &n.Reference, &n.Channel, &n.Note, &n.SubmittedAt, &n.SubmittedBy, &rule); err != nil {
			return nil, err
		}
		if len(rule) > 0 {
			n.rule = &Stage{}
			if err := json.Unmarshal(rule, n.rule); err != nil {
				return nil, err
			}
		}
		if n.DueAt != nil && (n.Status == "pending" || n.Status == "draft") {
			sec := int64(n.DueAt.Sub(now).Seconds())
			n.Seconds = &sec
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func notificationsOf(ctx context.Context, tx pgx.Tx, incidentID string) ([]Notification, error) {
	rows, err := tx.Query(ctx, selectNotification+` where n.incident_element_id = $1 order by n.scheme_key, n.ordinal`, incidentID)
	if err != nil {
		return nil, err
	}
	return scanNotifications(rows, time.Now())
}

func (s *Service) notification(ctx context.Context, tx pgx.Tx, id string, lock bool) (*Notification, error) {
	q := selectNotification + ` where n.id::text = $1`
	if lock {
		q += " for update of n"
	}
	rows, err := tx.Query(ctx, q, id)
	if err != nil {
		return nil, err
	}
	list, err := scanNotifications(rows, s.now())
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

// NotificationInput modifica una notifica: contenuto (bozza), invio registrato, non dovuta.
type NotificationInput struct {
	Content     *string `json:"content"`
	SubmittedAt *string `json:"submitted_at"`
	Reference   *string `json:"reference"`
	Channel     *string `json:"channel"`
	Note        *string `json:"note"`
}

// SaveDraft salva il contenuto della notifica come bozza (incident.manage). Se la notifica ha
// già una storia di bozze (iniziata da una bozza dell'assistente, notification_drafts.go), anche
// questa diventa una versione, scritta dalla persona.
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in NotificationInput) (*Notification, error) {
	n, err := s.saveDraft(ctx, tx, actor, id, in)
	if err != nil {
		return nil, err
	}
	if ok, err := hasDraftVersions(ctx, tx, n.ID); err != nil {
		return nil, err
	} else if ok {
		if _, err := s.addDraftVersion(ctx, tx, actor, n, nil, DraftByPerson); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func (s *Service) saveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in NotificationInput) (*Notification, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	n, err := s.notification(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if n.Status == "submitted" {
		return nil, engine.InvalidCode("validation.risk.notification_sent_locked", "status", "la notifica è già stata inviata: il contenuto resta quello inviato")
	}
	content := strings.TrimSpace(deref(in.Content))
	if len(content) > 50000 {
		return nil, engine.InvalidCode("validation.risk.text_too_long", "content", "contenuto troppo lungo")
	}
	if _, err := tx.Exec(ctx, `update regulatory_notifications set content = $2, status = 'draft', note = null, updated_at = now() where id = $1`, n.ID, content); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "regulatory_notification.draft", "regulatory_notification", n.ID, map[string]any{"incident": n.IncidentID, "stage": n.StageKey}); err != nil {
		return nil, err
	}
	return s.notification(ctx, tx, n.ID, false)
}

// Submit registra l'invio fatto fuori da Regia (incident.manage): data e ora, riferimento
// (protocollo), canale. Le scadenze delle tappe successive partono da qui.
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in NotificationInput) (*Notification, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	n, err := s.notification(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if n.Status == "submitted" {
		return nil, engine.InvalidCode("validation.risk.send_already_recorded", "status", "invio già registrato")
	}
	at, err := parseTime("submitted_at", deref(in.SubmittedAt))
	if err != nil {
		return nil, err
	}
	if at == nil {
		t := s.now()
		at = &t
	}
	if at.After(s.now().Add(5 * time.Minute)) {
		return nil, engine.InvalidCode("validation.risk.sent_future", "submitted_at", "l'invio non può essere nel futuro")
	}
	content := n.Content
	if in.Content != nil {
		content = strings.TrimSpace(*in.Content)
	}
	if content == "" {
		return nil, engine.InvalidCode("validation.risk.content_before_send", "content", "scrivi il contenuto inviato prima di registrare l'invio")
	}
	ref, ch := strings.TrimSpace(deref(in.Reference)), strings.TrimSpace(deref(in.Channel))
	if len(ref) > 200 || len(ch) > 200 {
		return nil, engine.InvalidCode("validation.risk.reference_channel_too_long", "reference", "riferimento o canale troppo lunghi")
	}
	if _, err := tx.Exec(ctx, `update regulatory_notifications set status = 'submitted', content = $2, submitted_at = $3, submitted_by = $4,
		reference = nullif($5, ''), channel = nullif($6, ''), note = nullif($7, ''), updated_at = now() where id = $1`,
		n.ID, content, at, nullable(actor.MemberID), ref, ch, strings.TrimSpace(deref(in.Note))); err != nil {
		return nil, err
	}
	late := ""
	if n.DueAt != nil && at.After(*n.DueAt) {
		late = " (oltre la scadenza)"
	}
	text := fmt.Sprintf("%s · %s inviata%s", n.SchemeName, n.StageName, late)
	if ref != "" {
		text += ", riferimento " + ref
	}
	if err := addEvent(ctx, tx, actor, n.IncidentID, *at, "notification", text, true); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, n.IncidentID, actor.MemberID, "notification", "submitted", map[string]any{"scheme": n.SchemeName, "stage": n.StageName}, nil); err != nil {
		return nil, err
	}
	if err := s.recomputeDue(ctx, tx, n.IncidentID); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "regulatory_notification.submit", "regulatory_notification", n.ID, map[string]any{"incident": n.IncidentID,
		"stage": n.StageKey, "late": late != ""}); err != nil {
		return nil, err
	}
	return s.notification(ctx, tx, n.ID, false)
}

// NotRequired dichiara la notifica non dovuta, con la motivazione (incident.manage).
func (s *Service) NotRequired(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in NotificationInput) (*Notification, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	n, err := s.notification(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if n.Status == "submitted" {
		return nil, engine.InvalidCode("validation.risk.notification_already_sent", "status", "la notifica è già stata inviata")
	}
	note := strings.TrimSpace(deref(in.Note))
	if note == "" || len(note) > 2000 {
		return nil, engine.InvalidCode("validation.risk.waive_reason", "note", "spiega perché la notifica non è dovuta")
	}
	if _, err := tx.Exec(ctx, `update regulatory_notifications set status = 'not_required', note = $2, updated_at = now() where id = $1`, n.ID, note); err != nil {
		return nil, err
	}
	if err := addEvent(ctx, tx, actor, n.IncidentID, s.now(), "notification", n.SchemeName+" · "+n.StageName+": non dovuta. "+note, true); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "regulatory_notification.not_required", "regulatory_notification", n.ID, map[string]any{"incident": n.IncidentID, "stage": n.StageKey}); err != nil {
		return nil, err
	}
	return s.notification(ctx, tx, n.ID, false)
}

// OpenNotifications elenca le notifiche da inviare di tutti gli incidenti, per scadenza.
func (s *Service) OpenNotifications(ctx context.Context, tx pgx.Tx) ([]Notification, error) {
	rows, err := tx.Query(ctx, selectNotification+` where n.status in ('pending', 'draft') order by n.due_at nulls last, n.ordinal limit 200`)
	if err != nil {
		return nil, err
	}
	return scanNotifications(rows, s.now())
}

// ---------------------------------------------------------------------------
// Postmortem e azioni correttive

// SavePostmortem salva il postmortem (incident.manage).
func (s *Service) SavePostmortem(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, pm Postmortem) (*IncidentView, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	pm.Summary, pm.RootCause, pm.Lessons = strings.TrimSpace(pm.Summary), strings.TrimSpace(pm.RootCause), strings.TrimSpace(pm.Lessons)
	if pm.Summary == "" {
		return nil, engine.Required("summary", "il riepilogo del postmortem è obbligatorio")
	}
	if len(pm.Summary)+len(pm.RootCause)+len(pm.Lessons) > 50000 {
		return nil, engine.InvalidCode("validation.risk.text_too_long", "summary", "postmortem troppo lungo")
	}
	raw, _ := json.Marshal(pm)
	if _, err := tx.Exec(ctx, `update ext_incident set postmortem = $2, postmortem_at = now(), postmortem_by = $3 where element_id = $1`,
		el.ID, raw, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "incident", "postmortem", map[string]any{"saved": true}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "incident.postmortem", IncidentType, el.ID, nil); err != nil {
		return nil, err
	}
	return s.IncidentView(ctx, tx, actor, el.ID)
}

// ActionInput crea un'azione correttiva.
type ActionInput struct {
	Title      string `json:"title"`
	Body       string `json:"body_text"`
	AssigneeID string `json:"assignee_id"`
	TeamID     string `json:"team_id"` // vuoto = nello spazio degli incidenti
	Due        string `json:"due"`
}

// AddAction crea un task collegato all'incidente (incident.manage): nello spazio del team
// indicato (così compare nella sua board) oppure nello spazio degli incidenti.
func (s *Service) AddAction(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ActionInput) (*IncidentView, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	if ok, err := typeExists(ctx, tx, TaskType); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.Required("type", "le azioni correttive sono task: serve il work management")
	}
	space := IncidentSpace
	if strings.TrimSpace(in.TeamID) != "" {
		var sp *string
		err := tx.QueryRow(ctx, `select s.key from teams t join spaces s on s.tenant_id = t.tenant_id and s.id = t.delivery_space_id
			where t.id::text = $1`, strings.TrimSpace(in.TeamID)).Scan(&sp)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
		if err != nil {
			return nil, err
		}
		if sp == nil {
			return nil, engine.InvalidCode("validation.risk.team_no_workspace", "team_id", "il team non ha uno spazio di lavoro")
		}
		space = *sp
	}
	due, err := parseDate("due", in.Due)
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		body = "Azione correttiva dell'incidente " + el.Key + "."
	}
	sys := actor
	sys.Permissions = append(slices.Clone(actor.Permissions), rbac.ElementWrite)
	task, err := s.eng.Create(ctx, tx, sys, engine.CreateInput{SpaceKey: space, TypeKey: TaskType, Title: in.Title, BodyText: body, AssigneeID: strings.TrimSpace(in.AssigneeID)})
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return nil, engine.InvalidCode("validation.member_not_found", "assignee_id", "membro inesistente")
		}
		return nil, err
	}
	if due != nil {
		if _, err := tx.Exec(ctx, "update elements set due_at = $2 where id = $1", task.ID, due); err != nil {
			return nil, err
		}
	}
	if _, err := relate(ctx, tx, actor, RelCorrective, task.ID, el.ID); err != nil {
		return nil, err
	}
	if err := addEvent(ctx, tx, actor, el.ID, s.now(), "action", "Azione correttiva "+task.Key+": "+task.Title, true); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "incident.action", IncidentType, el.ID, map[string]any{"task": task.ID}); err != nil {
		return nil, err
	}
	return s.IncidentView(ctx, tx, actor, el.ID)
}

// ActionRef è un'azione correttiva con assegnatario e scadenza.
type ActionRef struct {
	Ref
	Assignee *string `json:"assignee"`
	Due      *string `json:"due"`
}

func correctiveActions(ctx context.Context, tx pgx.Tx, incidentID string) ([]ActionRef, error) {
	rows, err := tx.Query(ctx, `select `+refColumns+`, m.display_name, e.due_at`+refFrom+`
		join relations r on r.tenant_id = e.tenant_id and r.source_id = e.id
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		left join members m on m.tenant_id = e.tenant_id and m.id = e.assignee_id
		where r.target_id = $1 and e.deleted_at is null order by e.created_at`, incidentID, RelCorrective)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ActionRef, error) {
		var a ActionRef
		var due *time.Time
		err := r.Scan(&a.ID, &a.Key, &a.Title, &a.Type, &a.TypeName, &a.State, &a.StateCat, &a.Critical, &a.Assignee, &due)
		a.Due = dateStr(due)
		return a, err
	})
	if out == nil {
		out = []ActionRef{}
	}
	return out, err
}

// ---------------------------------------------------------------------------
// Azione incident_stage

func (s *Service) actionIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != IncidentType {
		return "incident_stage vale solo sugli incidenti", nil
	}
	r, err := loadIncident(ctx, tx, el.ID, true)
	if errors.Is(err, engine.ErrNotFound) {
		return "", engine.ErrNoEffect
	}
	if err != nil {
		return "", err
	}
	switch a.Ref {
	case "detected":
		var has bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from incident_events where incident_id = $1 and kind = 'detected')", el.ID).Scan(&has); err != nil || has {
			if err != nil {
				return "", err
			}
			return "", engine.ErrNoEffect
		}
		if err := addEvent(ctx, tx, actor, el.ID, r.DetectedAt, "detected", "Incidente rilevato", true); err != nil {
			return "", err
		}
		return "", engine.ErrNoEffect
	case "classified":
		c, err := s.classify(ctx, tx, actor, el.ID, false)
		if err != nil {
			return "", err
		}
		var pos []string
		for _, res := range c.Results {
			if res.Positive {
				pos = append(pos, res.Name)
			}
		}
		if len(pos) == 0 {
			return "nessuno schema con esito positivo", nil
		}
		return fmt.Sprintf("%s: %d notifiche da preparare", strings.Join(pos, ", "), c.Created), nil
	case "resolved":
		if _, err := tx.Exec(ctx, "update ext_incident set resolved_at = coalesce(resolved_at, $2) where element_id = $1", el.ID, s.now()); err != nil {
			return "", err
		}
		if r.ResolvedAt == nil {
			if err := addEvent(ctx, tx, actor, el.ID, s.now(), "resolved", "Servizio ripristinato", true); err != nil {
				return "", err
			}
		}
		return "", engine.ErrNoEffect
	case "closed":
		if err := addEvent(ctx, tx, actor, el.ID, s.now(), "closed", "Incidente chiuso", true); err != nil {
			return "", err
		}
		return "", engine.ErrNoEffect
	}
	return "stadio " + a.Ref + " non ammesso", nil
}

// notifyIncident avvisa il responsabile e la gestione incidenti.
func (s *Service) notifyIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, payload map[string]any) error {
	members, groups, err := roleHolders(ctx, tx, id, RoleIncidentLead, RoleIncidentTeam)
	if err != nil {
		return err
	}
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRisk, ElementID: id, ActorID: actor.MemberID, Members: members, Groups: groups, Payload: payload})
	return err
}

// ---------------------------------------------------------------------------
// Vista e registro

// IncidentView è la pagina dell'incidente.
type IncidentView struct {
	Element         *engine.Element `json:"element"`
	Key             string          `json:"key"`
	DetectedAt      time.Time       `json:"detected_at"`
	ClassifiedAt    *time.Time      `json:"classified_at"`
	ContainedAt     *time.Time      `json:"contained_at"`
	ResolvedAt      *time.Time      `json:"resolved_at"`
	DurationHours   float64         `json:"duration_hours"`
	Major           bool            `json:"major"`
	Classification  []SchemeResult  `json:"classification"`
	ClientsAffected *int            `json:"clients_affected"`
	ClientsPct      *float64        `json:"clients_affected_pct"`
	DowntimeMinutes *int            `json:"downtime_minutes"`
	DataLoss        []string        `json:"data_loss"`
	EconomicCents   *int64          `json:"economic_impact_cents"`
	MemberStates    *int            `json:"member_states"`
	Reputational    bool            `json:"reputational"`
	Recurring       bool            `json:"recurring"`
	Cause           *string         `json:"cause"`
	CauseName       string          `json:"cause_name"`
	CauseProvider   *Ref            `json:"cause_provider"`
	// PhaseMove: cosa è successo alla fase quando si è registrata la risoluzione (solo nella risposta di UpdateIncident).
	PhaseMove     *PhaseMove                   `json:"phase_move,omitempty"`
	LeadID        *string                      `json:"lead_id"`
	Lead          *string                      `json:"lead"`
	Impacted      []Ref                        `json:"impacted"`
	Risks         []Ref                        `json:"risks"`
	Events        []Event                      `json:"events"`
	Notifications []Notification               `json:"notifications"`
	Postmortem    *Postmortem                  `json:"postmortem"`
	PostmortemAt  *time.Time                   `json:"postmortem_at"`
	PostmortemBy  *string                      `json:"postmortem_by"`
	Actions       []ActionRef                  `json:"actions"`
	Options       map[string]map[string]string `json:"options"`
	Can           map[string]bool              `json:"can"`
}

// IncidentView restituisce l'incidente con timeline, notifiche, postmortem e azioni.
func (s *Service) IncidentView(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*IncidentView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, IncidentType)
	if err != nil {
		return nil, err
	}
	r, err := loadIncident(ctx, tx, el.ID, false)
	if err != nil {
		return nil, err
	}
	v := &IncidentView{Element: el, Key: r.Key, DetectedAt: r.DetectedAt, ClassifiedAt: r.ClassifiedAt, ContainedAt: r.ContainedAt,
		ResolvedAt: r.ResolvedAt, DurationHours: r.durationHours(s.now()), Major: r.Major, Classification: []SchemeResult{},
		ClientsAffected: r.ClientsAffected, ClientsPct: r.ClientsPct, DowntimeMinutes: r.DowntimeMinutes, DataLoss: r.DataLoss,
		EconomicCents: r.EconomicCents, MemberStates: r.MemberStates, Reputational: r.Reputational, Recurring: r.Recurring, Cause: r.Cause,
		Postmortem: r.Postmortem, PostmortemAt: r.PostmortemAt, PostmortemBy: r.PostmortemBy,
		Options: map[string]map[string]string{"data_loss": dataLoss, "cause": causes}}
	if r.Cause != nil {
		v.CauseName = causes[*r.Cause]
	}
	if v.CauseProvider, err = causeProviderRef(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	schemes, err := s.Schemes(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	for _, sc := range schemes {
		if res, ok := r.Classification[sc.Key]; ok {
			v.Classification = append(v.Classification, res)
		}
	}
	if v.LeadID, v.Lead, err = holderName(ctx, tx, el.ID, RoleIncidentLead); err != nil {
		return nil, err
	}
	if v.Impacted, err = targetsOf(ctx, tx, RelImpacts, el.ID, true); err != nil {
		return nil, err
	}
	if v.Risks, err = targetsOf(ctx, tx, RelMaterializes, el.ID, true); err != nil {
		return nil, err
	}
	if v.Events, err = events(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectNotification+` where n.incident_element_id = $1 order by n.scheme_key, n.ordinal`, el.ID)
	if err != nil {
		return nil, err
	}
	if v.Notifications, err = scanNotifications(rows, s.now()); err != nil {
		return nil, err
	}
	if v.Actions, err = correctiveActions(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	manage := actor.Has(PermIncident)
	v.Can = map[string]bool{"manage": manage}
	return v, nil
}

func causeProviderRef(ctx context.Context, tx pgx.Tx, incidentID string) (*Ref, error) {
	rows, err := tx.Query(ctx, `select `+refColumns+refFrom+`
		join ext_incident x on x.tenant_id = e.tenant_id and x.cause_provider_id = e.id
		where x.element_id = $1 and e.deleted_at is null`, incidentID)
	if err != nil {
		return nil, err
	}
	refs, err := scanRefs(rows)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	return &refs[0], nil
}

// IncidentItem è una riga dell'elenco degli incidenti.
type IncidentItem struct {
	ID            string     `json:"id"`
	Key           string     `json:"key"`
	Title         string     `json:"title"`
	Phase         string     `json:"phase"`
	PhaseCategory string     `json:"phase_category"`
	DetectedAt    time.Time  `json:"detected_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	Major         bool       `json:"major"`
	Outcomes      []string   `json:"outcomes"`
	OpenNotices   int        `json:"open_notifications"`
	NextDue       *time.Time `json:"next_due"`
	NextStage     string     `json:"next_stage"`
	Impacted      []string   `json:"impacted"`
}

// Incidents elenca gli incidenti (element.read); open = solo quelli non chiusi.
func (s *Service) Incidents(ctx context.Context, tx pgx.Tx, actor engine.Actor, q string, open, major bool) ([]IncidentItem, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectIncident+`
		where ($1 = '' or e.title ilike '%' || $1 || '%' or (s.key || '-' || e.number) ilike $1)
		  and (not $2 or coalesce(o.category, '') not in ('done', 'cancelled')) and (not $3 or x.is_major)
		order by x.detected_at desc limit 500`, strings.TrimSpace(q), open, major)
	if err != nil {
		return nil, err
	}
	var list []*incidentRow
	for rows.Next() {
		r, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	notices, err := s.OpenNotifications(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []IncidentItem{}
	for _, r := range list {
		it := IncidentItem{ID: r.ID, Key: r.Key, Title: r.Title, Phase: r.Phase, PhaseCategory: r.PhaseCat, DetectedAt: r.DetectedAt, ResolvedAt: r.ResolvedAt, Major: r.Major,
			Outcomes: []string{}, Impacted: []string{}}
		for _, res := range r.Classification {
			if res.Positive {
				it.Outcomes = append(it.Outcomes, res.Name)
			}
		}
		slices.Sort(it.Outcomes)
		for _, n := range notices {
			if n.IncidentID != r.ID {
				continue
			}
			it.OpenNotices++
			if n.DueAt != nil && (it.NextDue == nil || n.DueAt.Before(*it.NextDue)) {
				it.NextDue, it.NextStage = n.DueAt, n.SchemeName+" · "+n.StageName
			}
		}
		imp, err := targetsOf(ctx, tx, RelImpacts, r.ID, true)
		if err != nil {
			return nil, err
		}
		for _, x := range imp {
			it.Impacted = append(it.Impacted, x.Title)
		}
		out = append(out, it)
	}
	return out, nil
}

// PreviewScheme valida uno schema (anche non salvato) e, se c'è un incidente, ne mostra
// l'esito sui suoi fatti: serve all'anteprima nella configurazione.
func (s *Service) PreviewScheme(ctx context.Context, tx pgx.Tx, actor engine.Actor, sc Scheme, incidentRef string) (*SchemeResult, error) {
	if !canConfigureSchemes(actor) {
		return nil, engine.ErrForbidden
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(incidentRef) == "" {
		return nil, nil
	}
	el, err := s.resolveTyped(ctx, tx, actor, incidentRef, IncidentType)
	if err != nil {
		return nil, err
	}
	facts, err := s.eng.ElementFacts(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	res := evalScheme(sc, facts)
	res.At = s.now()
	return &res, nil
}
