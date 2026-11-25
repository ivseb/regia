package grc

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Spazio dell'auditor (ondata 3, C6; docs/sviluppo/spazio-auditor.md): incarichi con perimetro e
// periodo, esame dei controlli, campioni ripetibili, richieste di evidenza, commenti e rilievi
// (audit_work.go). L'auditor (grc.audit) scrive solo queste tabelle: il resto è in sola lettura.

// PermAudit è il permesso dell'auditor.
const PermAudit = "grc.audit"

func requireAudit(a engine.Actor) error {
	if !a.Has(PermAudit) {
		return engine.ErrForbidden
	}
	return nil
}

// brief è un elemento citato: id, chiave e titolo.
type Brief struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Engagement è un incarico di audit con i suoi numeri.
type Engagement struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Framework       string     `json:"framework"`
	PeriodFrom      string     `json:"period_from"`
	PeriodTo        string     `json:"period_to"`
	Status          string     `json:"status"`
	Note            string     `json:"note"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	ClosedAt        *time.Time `json:"closed_at"`
	Apps            []Brief    `json:"apps"`
	Controls        int        `json:"controls"`
	Reviewed        int        `json:"reviewed"`
	OpenRequests    int        `json:"open_requests"`
	OverdueRequests int        `json:"overdue_requests"`
	OpenFindings    int        `json:"open_findings"`
	Samples         int        `json:"samples"`
}

// EngControl è un controllo del perimetro con lo stato dell'esame.
type EngControl struct {
	Brief
	Owner         string `json:"owner"`
	Effectiveness string `json:"effectiveness"`
	Conclusion    string `json:"conclusion"` // vuoto = da esaminare
	Samples       int    `json:"samples"`
	SamplePending int    `json:"sample_pending"`
	OpenRequests  int    `json:"open_requests"`
	OpenFindings  int    `json:"open_findings"`
}

// EngagementDetail è l'incarico con i controlli del perimetro.
type EngagementDetail struct {
	Engagement
	ControlList []EngControl `json:"control_list"`
	Can         struct {
		Audit bool `json:"audit"`
	} `json:"can"`
}

// EngagementInput apre un incarico. Controls e Apps sono chiavi; vuoti = tutto (o la norma).
type EngagementInput struct {
	Title     string   `json:"title"`
	Framework string   `json:"framework"`
	From      string   `json:"period_from"`
	To        string   `json:"period_to"`
	Note      string   `json:"note"`
	Controls  []string `json:"controls"`
	Apps      []string `json:"apps"`
}

func auditDay(field, s string) (time.Time, error) {
	t, err := parseDay(field, s)
	if err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, engine.InvalidCode("validation.required", field, "campo obbligatorio")
	}
	return *t, nil
}

// CreateEngagement apre un incarico.
func (s *Service) CreateEngagement(ctx context.Context, tx pgx.Tx, actor engine.Actor, in EngagementInput) (*EngagementDetail, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	if len(in.Title) > 200 || len(in.Note) > 10000 {
		return nil, engine.InvalidCode("validation.too_long", "title", "testo troppo lungo")
	}
	from, err := auditDay("period_from", in.From)
	if err != nil {
		return nil, err
	}
	to, err := auditDay("period_to", in.To)
	if err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, engine.InvalidCode("validation.audit.period_invalid", "period_to", "la fine del periodo precede l'inizio")
	}
	if in.Framework != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from frameworks where key = $1)", in.Framework).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.InvalidCode("validation.audit.framework_unknown", "framework", "normativa sconosciuta")
		}
	}
	var id string
	if err := tx.QueryRow(ctx, `insert into grc_audit_engagements (tenant_id, title, framework_key, period_from, period_to, note, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6) returning id::text`,
		in.Title, nullable(in.Framework), from, to, strings.TrimSpace(in.Note), nullable(actor.MemberID)).Scan(&id); err != nil {
		return nil, err
	}
	for _, ref := range in.Controls {
		cid, err := s.controlRef(ctx, tx, ref)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into grc_audit_scope (tenant_id, engagement_id, element_id, kind) values (app_tenant(), $1, $2, 'control') on conflict do nothing`, id, cid); err != nil {
			return nil, err
		}
	}
	for _, ref := range in.Apps {
		el, err := s.eng.Get(ctx, tx, sysActor, ref)
		if err != nil {
			return nil, err
		}
		if el.TypeKey != AppType {
			return nil, engine.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `insert into grc_audit_scope (tenant_id, engagement_id, element_id, kind) values (app_tenant(), $1, $2, 'application') on conflict do nothing`, id, el.ID); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "grc.audit.engagement.create", "grc_audit_engagement", id, map[string]any{
		"title": in.Title, "framework": in.Framework, "from": in.From, "to": in.To, "controls": len(in.Controls), "apps": len(in.Apps)}); err != nil {
		return nil, err
	}
	return s.EngagementDetail(ctx, tx, actor, id)
}

// scopeControls sono gli id dei controlli del perimetro dell'incarico.
func scopeControls(ctx context.Context, tx pgx.Tx, id, framework string) ([]string, error) {
	var q string
	args := []any{id}
	var explicit int
	if err := tx.QueryRow(ctx, "select count(*) from grc_audit_scope where engagement_id = $1 and kind = 'control'", id).Scan(&explicit); err != nil {
		return nil, err
	}
	switch {
	case explicit > 0:
		q = "select element_id::text from grc_audit_scope where engagement_id = $1 and kind = 'control'"
	case framework != "":
		q = `select distinct cr.control_element_id::text from control_requirements cr join requirements r on r.id = cr.requirement_id
			join framework_versions v on v.id = r.version_id join frameworks f on f.id = v.framework_id where f.key = $2 and $1::uuid is not null`
		args = append(args, framework)
	default:
		q = "select element_id::text from ext_control where $1::uuid is not null"
	}
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	var x string
	_, err = pgx.ForEachRow(rows, []any{&x}, func() error { ids = append(ids, x); return nil })
	return ids, err
}

func scopeApps(ctx context.Context, tx pgx.Tx, id string) ([]Brief, error) {
	rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title from grc_audit_scope g
		join elements e on e.tenant_id = g.tenant_id and e.id = g.element_id join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where g.engagement_id = $1 and g.kind = 'application' order by e.title`, id)
	if err != nil {
		return nil, err
	}
	out := []Brief{}
	var b Brief
	_, err = pgx.ForEachRow(rows, []any{&b.ID, &b.Key, &b.Title}, func() error { out = append(out, b); return nil })
	return out, err
}

const engagementSelect = `select g.id::text, g.title, coalesce(g.framework_key, ''), g.period_from, g.period_to, g.status, g.note,
	coalesce(m.display_name, ''), g.created_at, g.closed_at
	from grc_audit_engagements g left join members m on m.tenant_id = g.tenant_id and m.id = g.created_by`

func (s *Service) scanEngagement(ctx context.Context, tx pgx.Tx, row pgx.Row, withCounts bool) (*Engagement, error) {
	e := &Engagement{}
	var from, to time.Time
	if err := row.Scan(&e.ID, &e.Title, &e.Framework, &from, &to, &e.Status, &e.Note, &e.CreatedBy, &e.CreatedAt, &e.ClosedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	e.PeriodFrom, e.PeriodTo = from.Format(time.DateOnly), to.Format(time.DateOnly)
	e.Apps = []Brief{}
	if !withCounts {
		return e, nil
	}
	var err error
	if e.Apps, err = scopeApps(ctx, tx, e.ID); err != nil {
		return nil, err
	}
	ids, err := scopeControls(ctx, tx, e.ID, e.Framework)
	if err != nil {
		return nil, err
	}
	e.Controls = len(ids)
	err = tx.QueryRow(ctx, `select
		(select count(*) from grc_audit_reviews where engagement_id = $1 and control_element_id = any($2)),
		(select count(*) from grc_audit_requests where engagement_id = $1 and status = 'open'),
		(select count(*) from grc_audit_requests where engagement_id = $1 and status = 'open' and due_date < current_date),
		(select count(*) from grc_audit_findings where engagement_id = $1 and status <> 'closed'),
		(select count(*) from grc_audit_samples where engagement_id = $1)`, e.ID, ids).
		Scan(&e.Reviewed, &e.OpenRequests, &e.OverdueRequests, &e.OpenFindings, &e.Samples)
	return e, err
}

func (s *Service) requireEngagement(ctx context.Context, tx pgx.Tx, id string, open bool) (*Engagement, error) {
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	e, err := s.scanEngagement(ctx, tx, tx.QueryRow(ctx, engagementSelect+" where g.id = $1", id), false)
	if err != nil {
		return nil, err
	}
	if open && e.Status != "open" {
		return nil, engine.InvalidCode("validation.audit.engagement_closed", "engagement", "l'incarico è chiuso")
	}
	return e, nil
}

// Engagements elenca gli incarichi (aperti per primi).
func (s *Service) Engagements(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Engagement, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, engagementSelect+" order by (g.status = 'open') desc, g.created_at desc")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var raw []Engagement
	for rows.Next() {
		e := Engagement{}
		var from, to time.Time
		if err := rows.Scan(&e.ID, &e.Title, &e.Framework, &from, &to, &e.Status, &e.Note, &e.CreatedBy, &e.CreatedAt, &e.ClosedAt); err != nil {
			return nil, err
		}
		e.PeriodFrom, e.PeriodTo = from.Format(time.DateOnly), to.Format(time.DateOnly)
		raw = append(raw, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Engagement, 0, len(raw))
	for _, e := range raw {
		full, err := s.scanEngagement(ctx, tx, tx.QueryRow(ctx, engagementSelect+" where g.id = $1", e.ID), true)
		if err != nil {
			return nil, err
		}
		out = append(out, *full)
	}
	return out, nil
}

// EngagementDetail restituisce l'incarico con i controlli del perimetro e lo stato dell'esame.
func (s *Service) EngagementDetail(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*EngagementDetail, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(id) {
		return nil, engine.ErrNotFound
	}
	e, err := s.scanEngagement(ctx, tx, tx.QueryRow(ctx, engagementSelect+" where g.id = $1", id), true)
	if err != nil {
		return nil, err
	}
	ids, err := scopeControls(ctx, tx, id, e.Framework)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, coalesce(m.display_name, ''), x.effectiveness,
		coalesce(r.conclusion, ''),
		(select count(*) from grc_audit_samples sa where sa.engagement_id = $1 and sa.control_element_id = e.id),
		(select count(*) from grc_audit_sample_items si join grc_audit_samples sa on sa.tenant_id = si.tenant_id and sa.id = si.sample_id
			where sa.engagement_id = $1 and sa.control_element_id = e.id and si.outcome = 'pending'),
		(select count(*) from grc_audit_requests q where q.engagement_id = $1 and q.control_element_id = e.id and q.status = 'open'),
		(select count(*) from grc_audit_findings f where f.engagement_id = $1 and f.control_element_id = e.id and f.status <> 'closed')
		from ext_control x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join members m on m.tenant_id = x.tenant_id and m.id = x.owner_id
		left join grc_audit_reviews r on r.tenant_id = x.tenant_id and r.engagement_id = $1 and r.control_element_id = x.element_id
		where e.id = any($2) order by e.number`, id, ids)
	if err != nil {
		return nil, err
	}
	d := &EngagementDetail{Engagement: *e, ControlList: []EngControl{}}
	var c EngControl
	if _, err := pgx.ForEachRow(rows, []any{&c.ID, &c.Key, &c.Title, &c.Owner, &c.Effectiveness, &c.Conclusion, &c.Samples, &c.SamplePending, &c.OpenRequests, &c.OpenFindings},
		func() error { d.ControlList = append(d.ControlList, c); return nil }); err != nil {
		return nil, err
	}
	d.Can.Audit = actor.Has(PermAudit) && e.Status == "open"
	return d, nil
}

// CloseEngagement chiude l'incarico (non si scrivono più campioni, richieste e rilievi nuovi).
func (s *Service) CloseEngagement(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*EngagementDetail, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	if _, err := s.requireEngagement(ctx, tx, id, true); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update grc_audit_engagements set status = 'closed', closed_at = now() where id = $1", id); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.engagement.close", "grc_audit_engagement", id, nil); err != nil {
		return nil, err
	}
	return s.EngagementDetail(ctx, tx, actor, id)
}

// inScope trova il controllo e verifica che stia nel perimetro dell'incarico.
func (s *Service) inScope(ctx context.Context, tx pgx.Tx, e *Engagement, ref string) (string, error) {
	cid, err := s.controlRef(ctx, tx, ref)
	if err != nil {
		return "", err
	}
	ids, err := scopeControls(ctx, tx, e.ID, e.Framework)
	if err != nil {
		return "", err
	}
	if !slices.Contains(ids, cid) {
		return "", engine.InvalidCode("validation.audit.control_not_in_scope", "control", "il controllo non è nel perimetro dell'incarico")
	}
	return cid, nil
}

// ReviewControl segna un controllo come esaminato (o da chiarire) nell'incarico.
func (s *Service) ReviewControl(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, ref, conclusion, note string) (*EngagementDetail, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	e, err := s.requireEngagement(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if conclusion == "" {
		conclusion = "reviewed"
	}
	if conclusion != "reviewed" && conclusion != "to_clarify" {
		return nil, engine.InvalidCode("validation.audit.conclusion_invalid", "conclusion", "conclusione non ammessa (reviewed, to_clarify)")
	}
	note = strings.TrimSpace(note)
	if len(note) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	cid, err := s.inScope(ctx, tx, e, ref)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into grc_audit_reviews (tenant_id, engagement_id, control_element_id, conclusion, note, reviewed_by)
		values (app_tenant(), $1, $2, $3, $4, $5)
		on conflict (tenant_id, engagement_id, control_element_id) do update set conclusion = excluded.conclusion, note = excluded.note,
		  reviewed_by = excluded.reviewed_by, reviewed_at = now()`, id, cid, conclusion, note, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.audit.control.review", "grc_audit_engagement", id, map[string]any{"control": ref, "conclusion": conclusion}); err != nil {
		return nil, err
	}
	return s.EngagementDetail(ctx, tx, actor, id)
}

// ---------------------------------------------------------------------------
// Campionamento

// SampleItem è un elemento del campione con l'esito dell'auditor.
type SampleItem struct {
	Position   int        `json:"position"`
	Evidence   Brief      `json:"evidence"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	ValidFrom  string     `json:"valid_from"`
	ValidUntil string     `json:"valid_until"`
	URL        string     `json:"url"`
	Outcome    string     `json:"outcome"`
	Note       string     `json:"note"`
	AssessedBy string     `json:"assessed_by"`
	AssessedAt *time.Time `json:"assessed_at"`
}

// Sample è un campione di evidenze di un controllo.
type Sample struct {
	ID         string       `json:"id"`
	Engagement string       `json:"engagement"`
	Control    Brief        `json:"control"`
	Seed       int64        `json:"seed"`
	Size       int          `json:"size"`
	Population int          `json:"population"`
	DrawnBy    string       `json:"drawn_by"`
	DrawnAt    time.Time    `json:"drawn_at"`
	PeriodFrom string       `json:"period_from"`
	PeriodTo   string       `json:"period_to"`
	Items      []SampleItem `json:"items"`
}

// DrawInput chiede un campione di Size evidenze del controllo; Seed vuoto = a caso, registrato.
type DrawInput struct {
	Control string `json:"control"`
	Size    int    `json:"size"`
	Seed    *int64 `json:"seed"`
}

// populationIDs sono le evidenze del controllo con validità nel periodo (e, se l'incarico ha
// applicazioni, di quelle applicazioni o dell'intero tenant), in ordine stabile.
func populationIDs(ctx context.Context, tx pgx.Tx, e *Engagement, controlID string) ([]string, error) {
	apps := make([]string, 0, len(e.Apps))
	for _, a := range e.Apps {
		apps = append(apps, a.ID)
	}
	rows, err := tx.Query(ctx, `select ev.element_id::text from grc_evidence_controls gc
		join ext_evidence ev on ev.tenant_id = gc.tenant_id and ev.element_id = gc.evidence_element_id
		where gc.control_element_id = $1 and ev.status in ('valid','revoked')
		  and ev.valid_from::date <= $3::date and (ev.valid_until is null or ev.valid_until::date >= $2::date)
		  and (cardinality($4::uuid[]) = 0 or ev.scope_kind <> 'application' or ev.target_element_id = any($4::uuid[]))
		order by ev.element_id`, controlID, e.PeriodFrom, e.PeriodTo, apps)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	var x string
	_, err = pgx.ForEachRow(rows, []any{&x}, func() error { ids = append(ids, x); return nil })
	return ids, err
}

// pick estrae size elementi con il seme: stesso seme e stessa popolazione, stesso campione.
func pick(ids []string, size int, seed int64) []string {
	r := mrand.New(mrand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	perm := r.Perm(len(ids))
	if size > len(ids) {
		size = len(ids)
	}
	out := make([]string, 0, size)
	for _, i := range perm[:size] {
		out = append(out, ids[i])
	}
	return out
}

// DrawSample estrae e registra un campione di evidenze del controllo nel periodo dell'incarico.
func (s *Service) DrawSample(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in DrawInput) (*Sample, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	e, err := s.requireEngagement(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if in.Size < 1 || in.Size > 200 {
		return nil, engine.InvalidCode("validation.audit.sample_size_invalid", "size", "la dimensione del campione va da 1 a 200")
	}
	cid, err := s.inScope(ctx, tx, e, in.Control)
	if err != nil {
		return nil, err
	}
	pop, err := populationIDs(ctx, tx, e, cid)
	if err != nil {
		return nil, err
	}
	if len(pop) == 0 {
		return nil, engine.InvalidCode("validation.audit.sample_empty", "control", "il controllo non ha evidenze nel periodo da campionare")
	}
	var seed int64
	if in.Seed != nil {
		seed = *in.Seed
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		seed = int64(binary.BigEndian.Uint64(b[:]) >> 12) // intero modesto, facile da riscrivere
	}
	chosen := pick(pop, in.Size, seed)
	var sid string
	if err := tx.QueryRow(ctx, `insert into grc_audit_samples (tenant_id, engagement_id, control_element_id, seed, size, population, drawn_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6) returning id::text`, id, cid, seed, len(chosen), len(pop), nullable(actor.MemberID)).Scan(&sid); err != nil {
		return nil, err
	}
	for i, ev := range chosen {
		if _, err := tx.Exec(ctx, `insert into grc_audit_sample_items (tenant_id, sample_id, position, evidence_element_id) values (app_tenant(), $1, $2, $3)`, sid, i+1, ev); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "grc.audit.sample.draw", "grc_audit_sample", sid, map[string]any{
		"engagement": id, "control": in.Control, "seed": seed, "size": len(chosen), "population": len(pop)}); err != nil {
		return nil, err
	}
	return s.sampleView(ctx, tx, sid)
}

func (s *Service) sampleView(ctx context.Context, tx pgx.Tx, sid string) (*Sample, error) {
	sm := &Sample{}
	var from, to time.Time
	err := tx.QueryRow(ctx, `select sa.id::text, sa.engagement_id::text, c.id::text, sp.key || '-' || c.number, c.title, sa.seed, sa.size, sa.population,
		coalesce(m.display_name, ''), sa.drawn_at, g.period_from, g.period_to
		from grc_audit_samples sa join grc_audit_engagements g on g.tenant_id = sa.tenant_id and g.id = sa.engagement_id
		join elements c on c.tenant_id = sa.tenant_id and c.id = sa.control_element_id
		join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id
		left join members m on m.tenant_id = sa.tenant_id and m.id = sa.drawn_by where sa.id = $1`, sid).
		Scan(&sm.ID, &sm.Engagement, &sm.Control.ID, &sm.Control.Key, &sm.Control.Title, &sm.Seed, &sm.Size, &sm.Population, &sm.DrawnBy, &sm.DrawnAt, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sm.PeriodFrom, sm.PeriodTo = from.Format(time.DateOnly), to.Format(time.DateOnly)
	rows, err := tx.Query(ctx, `select si.position, e.id::text, sp.key || '-' || e.number, e.title, ev.kind, ev.status, ev.valid_from::date, ev.valid_until::date,
		coalesce(ev.url, ''), si.outcome, si.note, coalesce(m.display_name, ''), si.assessed_at
		from grc_audit_sample_items si join elements e on e.tenant_id = si.tenant_id and e.id = si.evidence_element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join ext_evidence ev on ev.tenant_id = e.tenant_id and ev.element_id = e.id
		left join members m on m.tenant_id = si.tenant_id and m.id = si.assessed_by
		where si.sample_id = $1 order by si.position`, sid)
	if err != nil {
		return nil, err
	}
	sm.Items = []SampleItem{}
	var it SampleItem
	var vf time.Time
	var vu *time.Time
	if _, err := pgx.ForEachRow(rows, []any{&it.Position, &it.Evidence.ID, &it.Evidence.Key, &it.Evidence.Title, &it.Kind, &it.Status, &vf, &vu, &it.URL, &it.Outcome, &it.Note, &it.AssessedBy, &it.AssessedAt},
		func() error {
			x := it
			x.ValidFrom = vf.Format(time.DateOnly)
			x.ValidUntil = ""
			if vu != nil {
				x.ValidUntil = vu.Format(time.DateOnly)
			}
			sm.Items = append(sm.Items, x)
			return nil
		}); err != nil {
		return nil, err
	}
	return sm, nil
}

// Sample restituisce un campione.
func (s *Service) Sample(ctx context.Context, tx pgx.Tx, actor engine.Actor, sid string) (*Sample, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(sid) {
		return nil, engine.ErrNotFound
	}
	return s.sampleView(ctx, tx, sid)
}

// AssessSampleItem segna un elemento del campione: conforme, non conforme o da chiarire.
func (s *Service) AssessSampleItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, sid string, pos int, outcome, note string) (*Sample, error) {
	if err := requireAudit(actor); err != nil {
		return nil, err
	}
	if outcome != "compliant" && outcome != "non_compliant" && outcome != "to_clarify" && outcome != "pending" {
		return nil, engine.InvalidCode("validation.audit.outcome_invalid", "outcome", "esito non ammesso (compliant, non_compliant, to_clarify)")
	}
	note = strings.TrimSpace(note)
	if len(note) > 4000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	if outcome != "compliant" && outcome != "pending" && note == "" {
		return nil, engine.InvalidCode("validation.audit.note_required", "note", "scrivi perché non è conforme o cosa va chiarito")
	}
	if !uuidRe.MatchString(sid) {
		return nil, engine.ErrNotFound
	}
	var eng string
	if err := tx.QueryRow(ctx, "select engagement_id::text from grc_audit_samples where id = $1", sid).Scan(&eng); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if _, err := s.requireEngagement(ctx, tx, eng, true); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `update grc_audit_sample_items set outcome = $3, note = $4, assessed_by = $5, assessed_at = now()
		where sample_id = $1 and position = $2`, sid, pos, outcome, note, nullable(actor.MemberID))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.ErrNotFound
	}
	if err := audited(ctx, tx, actor, "grc.audit.sample.assess", "grc_audit_sample", sid, map[string]any{"position": pos, "outcome": outcome}); err != nil {
		return nil, err
	}
	return s.sampleView(ctx, tx, sid)
}

// Samples elenca i campioni di un controllo (di un incarico, se indicato).
func (s *Service) samplesOf(ctx context.Context, tx pgx.Tx, controlID string) ([]Sample, error) {
	rows, err := tx.Query(ctx, "select id::text from grc_audit_samples where control_element_id = $1 order by drawn_at desc", controlID)
	if err != nil {
		return nil, err
	}
	var ids []string
	var x string
	if _, err := pgx.ForEachRow(rows, []any{&x}, func() error { ids = append(ids, x); return nil }); err != nil {
		return nil, err
	}
	out := []Sample{}
	for _, id := range ids {
		sm, err := s.sampleView(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *sm)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Pacchetto dell'incarico

// WriteEngagementPack scrive il pacchetto per l'audit sul perimetro e sul periodo dell'incarico, con
// campioni, richieste e rilievi.
func (s *Service) WriteEngagementPack(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, w io.Writer, files FileSource) (string, error) {
	if err := requireRead(actor); err != nil {
		return "", err
	}
	e, err := s.requireEngagement(ctx, tx, id, false)
	if err != nil {
		return "", err
	}
	ids, err := scopeControls(ctx, tx, id, e.Framework)
	if err != nil {
		return "", err
	}
	ctrl := map[string]bool{}
	for _, c := range ids {
		ctrl[c] = true
	}
	from, _ := time.Parse(time.DateOnly, e.PeriodFrom)
	to, _ := time.Parse(time.DateOnly, e.PeriodTo)
	extra := map[string][]byte{}
	var b strings.Builder
	b.WriteString(bom + "controllo;campione;seme;popolazione;posizione;evidenza;titolo;esito;nota;valutata da\n")
	samples, err := tx.Query(ctx, "select id::text from grc_audit_samples where engagement_id = $1 order by drawn_at", id)
	if err != nil {
		return "", err
	}
	var sids []string
	var sid string
	if _, err := pgx.ForEachRow(samples, []any{&sid}, func() error { sids = append(sids, sid); return nil }); err != nil {
		return "", err
	}
	for _, x := range sids {
		sm, err := s.sampleView(ctx, tx, x)
		if err != nil {
			return "", err
		}
		for _, it := range sm.Items {
			fmt.Fprintf(&b, "%s;%s;%d;%d;%d;%s;%s;%s;%s;%s\n", sm.Control.Key, sm.ID[:8], sm.Seed, sm.Population, it.Position,
				it.Evidence.Key, csvText(it.Evidence.Title), it.Outcome, csvText(it.Note), csvText(it.AssessedBy))
		}
	}
	extra["campioni.csv"] = []byte(b.String())
	b.Reset()
	b.WriteString(bom + "controllo;richiesta;scadenza;stato;a;risposta;link\n")
	rr, err := tx.Query(ctx, `select c.number::text, q.title, coalesce(q.due_date::text, ''), q.status, coalesce(m.display_name, ''), q.response, coalesce(q.response_url, ''), sp.key
		from grc_audit_requests q join elements c on c.tenant_id = q.tenant_id and c.id = q.control_element_id
		join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id
		left join members m on m.tenant_id = q.tenant_id and m.id = q.assignee_id where q.engagement_id = $1 order by q.requested_at`, id)
	if err != nil {
		return "", err
	}
	var num, title, due, st, to2, resp, url, sk string
	if _, err := pgx.ForEachRow(rr, []any{&num, &title, &due, &st, &to2, &resp, &url, &sk}, func() error {
		fmt.Fprintf(&b, "%s-%s;%s;%s;%s;%s;%s;%s\n", sk, num, csvText(title), due, st, csvText(to2), csvText(resp), url)
		return nil
	}); err != nil {
		return "", err
	}
	extra["richieste.csv"] = []byte(b.String())
	b.Reset()
	b.WriteString(bom + "controllo;gravità;rilievo;stato;piano d'azione;scadenza del piano;nota di chiusura\n")
	fr, err := tx.Query(ctx, `select sp.key || '-' || c.number, f.severity, f.title, f.status, f.action_plan, coalesce(f.action_due::text, ''), f.closure_note
		from grc_audit_findings f join elements c on c.tenant_id = f.tenant_id and c.id = f.control_element_id
		join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id where f.engagement_id = $1 order by f.opened_at`, id)
	if err != nil {
		return "", err
	}
	var ck, sev, ft, fs, plan, pdue, note string
	if _, err := pgx.ForEachRow(fr, []any{&ck, &sev, &ft, &fs, &plan, &pdue, &note}, func() error {
		fmt.Fprintf(&b, "%s;%s;%s;%s;%s;%s;%s\n", ck, sev, csvText(ft), fs, csvText(plan), pdue, csvText(note))
		return nil
	}); err != nil {
		return "", err
	}
	extra["rilievi.csv"] = []byte(b.String())
	header := fmt.Sprintf("Incarico: %s\nPeriodo: dal %s al %s\nControlli nel perimetro: %d\n", e.Title, e.PeriodFrom, e.PeriodTo, len(ids))
	flt := &packFilter{Controls: ctrl, From: &from, To: &to, Extra: extra, Header: header}
	fw := e.Framework
	if err := s.writeAuditPack(ctx, tx, actor, w, fw, files, flt); err != nil {
		return "", err
	}
	if err := audited(ctx, tx, actor, "grc.audit.engagement.pack", "grc_audit_engagement", id, map[string]any{"controls": len(ids)}); err != nil {
		return "", err
	}
	return fw, nil
}

func csvText(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", ";", ",").Replace(s)
	return s
}
