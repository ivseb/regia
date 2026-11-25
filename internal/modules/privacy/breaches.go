package privacy

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Violazioni dei dati personali (art. 33 GDPR, da verificare): non una tabella, ma gli incidenti
// esistenti che toccano dati personali. Un incidente conta come violazione se il DPO l'ha segnato,
// oppure se è collegato a un trattamento o colpisce un'applicazione con dati personali nel catalogo.
// La notifica all'autorità è dovuta senza ingiustificato ritardo e, ove possibile, entro 72 ore da
// quando se ne è venuti a conoscenza: qui si conta dalla rilevazione dell'incidente.

// TreatmentRef è un trattamento citato da una violazione o da un'applicazione.
type TreatmentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Breach è un incidente visto dal DPO.
type Breach struct {
	ID            string     `json:"id"`
	Key           string     `json:"key"`
	Title         string     `json:"title"`
	Phase         string     `json:"phase"`
	PhaseCategory string     `json:"phase_category"`
	Major         bool       `json:"major"`
	DetectedAt    time.Time  `json:"detected_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	// Personal: tocca dati personali. Source: manual (segnato dal DPO), treatment, application, none.
	Personal bool   `json:"personal"`
	Source   string `json:"source"`
	Note     string `json:"note"`
	// DeadlineAt: rilevazione + 72 ore. HoursLeft è negativo a scadenza passata.
	DeadlineAt time.Time `json:"deadline_at"`
	HoursLeft  float64   `json:"hours_left"`
	// Status: open (da notificare), overdue (scaduta e non notificata), notified, notified_late, not_required.
	Status      string         `json:"status"`
	NotifiedAt  *time.Time     `json:"notified_at"`
	NotRequired bool           `json:"not_required"`
	Reason      string         `json:"not_required_reason"`
	Closed      bool           `json:"closed"`
	Application []AppRef       `json:"applications"`
	Treatments  []TreatmentRef `json:"treatments"`
}

// BreachInput segna un incidente come violazione (o toglie il segno) e indica i trattamenti coinvolti.
type BreachInput struct {
	Personal     bool     `json:"personal"`
	Note         string   `json:"note"`
	TreatmentIDs []string `json:"treatment_ids"`
}

// NotifyInput registra la notifica all'autorità: inviata (NotifiedAt vuoto = adesso), non dovuta con il motivo, oppure da azzerare.
type NotifyInput struct {
	NotifiedAt  string `json:"notified_at"`
	NotRequired bool   `json:"not_required"`
	Reason      string `json:"reason"`
	Clear       bool   `json:"clear"`
}

// Breaches elenca gli incidenti che toccano dati personali (privacy.read); all = tutti gli incidenti,
// per segnarne uno che i criteri non hanno riconosciuto. Prima quelli da notificare, per scadenza.
func (s *Service) Breaches(ctx context.Context, tx pgx.Tx, actor engine.Actor, all bool) ([]Breach, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	return s.breaches(ctx, tx, all, "")
}

func (s *Service) breaches(ctx context.Context, tx pgx.Tx, all bool, only string) ([]Breach, error) {
	rows, err := tx.Query(ctx, `select e.id::text, coalesce(sp.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), coalesce(o.category, ''),
		x.is_major, x.detected_at, x.resolved_at, mk.personal, coalesce(mk.note, ''), mk.notified_at, coalesce(mk.not_required, false),
		coalesce(mk.not_required_reason, '')
		from ext_incident x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join privacy_incident_marks mk on mk.tenant_id = x.tenant_id and mk.incident_id = x.element_id
		where ($1 = '' or e.id::text = $1)
		order by x.detected_at desc limit 500`, only)
	if err != nil {
		return nil, err
	}
	var list []Breach
	var marks []*bool
	for rows.Next() {
		var b Breach
		var mark *bool
		if err := rows.Scan(&b.ID, &b.Key, &b.Title, &b.Phase, &b.PhaseCategory, &b.Major, &b.DetectedAt, &b.ResolvedAt, &mark, &b.Note,
			&b.NotifiedAt, &b.NotRequired, &b.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		b.Closed = b.PhaseCategory == "done" || b.PhaseCategory == "cancelled"
		b.Application, b.Treatments = []AppRef{}, []TreatmentRef{}
		list, marks = append(list, b), append(marks, mark)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return []Breach{}, nil
	}
	idx := map[string]int{}
	ids := make([]string, len(list))
	for i, b := range list {
		idx[b.ID], ids[i] = i, b.ID
	}
	// Applicazioni colpite.
	ar, err := tx.Query(ctx, `select r.source_id::text, e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, coalesce(a.data_classification, '')
		from relations r
		join elements e on e.tenant_id = r.tenant_id and e.id = r.target_id and e.deleted_at is null
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where r.source_id = any($1::uuid[]) and r.type_id = (select id from relation_types where key = 'incident_impacts')
		order by e.title`, ids)
	if err != nil {
		return nil, err
	}
	for ar.Next() {
		var iid string
		var a AppRef
		if err := ar.Scan(&iid, &a.ID, &a.Key, &a.Name, &a.Classification); err != nil {
			ar.Close()
			return nil, err
		}
		a.Personal, a.Credentials, a.Financial = dataKinds(a.Classification)
		list[idx[iid]].Application = append(list[idx[iid]].Application, a)
	}
	ar.Close()
	if err := ar.Err(); err != nil {
		return nil, err
	}
	// Trattamenti coinvolti.
	tr, err := tx.Query(ctx, `select l.incident_id::text, t.id::text, t.name from privacy_incident_treatments l
		join privacy_treatments t on t.tenant_id = l.tenant_id and t.id = l.treatment_id
		where l.incident_id = any($1::uuid[]) order by lower(t.name)`, ids)
	if err != nil {
		return nil, err
	}
	for tr.Next() {
		var iid string
		var t TreatmentRef
		if err := tr.Scan(&iid, &t.ID, &t.Name); err != nil {
			tr.Close()
			return nil, err
		}
		list[idx[iid]].Treatments = append(list[idx[iid]].Treatments, t)
	}
	tr.Close()
	if err := tr.Err(); err != nil {
		return nil, err
	}
	now := s.now()
	out := []Breach{}
	for i := range list {
		b := list[i]
		appPersonal := slices.ContainsFunc(b.Application, func(a AppRef) bool { return a.Personal })
		switch {
		case marks[i] != nil:
			b.Personal, b.Source = *marks[i], "manual"
		case len(b.Treatments) > 0:
			b.Personal, b.Source = true, "treatment"
		case appPersonal:
			b.Personal, b.Source = true, "application"
		default:
			b.Source = "none"
		}
		if !b.Personal && !all && only == "" {
			continue
		}
		b.DeadlineAt = b.DetectedAt.Add(BreachHours * time.Hour)
		b.HoursLeft = b.DeadlineAt.Sub(now).Hours()
		switch {
		case b.NotRequired:
			b.Status = "not_required"
		case b.NotifiedAt != nil && b.NotifiedAt.After(b.DeadlineAt):
			b.Status = "notified_late"
		case b.NotifiedAt != nil:
			b.Status = "notified"
		case b.HoursLeft < 0:
			b.Status = "overdue"
		default:
			b.Status = "open"
		}
		out = append(out, b)
	}
	rank := map[string]int{"overdue": 0, "open": 1, "notified_late": 2, "notified": 3, "not_required": 4}
	slices.SortStableFunc(out, func(a, b Breach) int {
		if a.Personal != b.Personal {
			if a.Personal {
				return -1
			}
			return 1
		}
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] - rank[b.Status]
		}
		if a.Status == "open" || a.Status == "overdue" {
			return a.DeadlineAt.Compare(b.DeadlineAt)
		}
		return b.DetectedAt.Compare(a.DetectedAt)
	})
	return out, nil
}

// Breach legge una violazione (privacy.read), anche se non è ancora segnata come personale.
func (s *Service) Breach(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Breach, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if !isUUID(id) {
		return nil, engine.ErrNotFound
	}
	list, err := s.breaches(ctx, tx, true, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	return &list[0], nil
}

// SetBreach segna un incidente come violazione di dati personali (o ne toglie il segno) e ne collega i
// trattamenti (privacy.write).
func (s *Service) SetBreach(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in BreachInput) (*Breach, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if !isUUID(id) {
		return nil, engine.ErrNotFound
	}
	if err := checkElements(ctx, tx, "id", "element", []string{id}, "incident"); err != nil {
		return nil, engine.ErrNotFound
	}
	in.Note = strings.TrimSpace(in.Note)
	if err := checkLen("note", in.Note, 2000); err != nil {
		return nil, err
	}
	ids := uniq(in.TreatmentIDs)
	for _, t := range ids {
		if !isUUID(t) {
			return nil, engine.NotFound("treatment_ids", "", "trattamento non trovato")
		}
	}
	if len(ids) > 0 {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from privacy_treatments where id = any($1::uuid[])`, ids).Scan(&n); err != nil {
			return nil, err
		}
		if n != len(ids) {
			return nil, engine.NotFound("treatment_ids", "", "trattamento non trovato")
		}
	}
	if _, err := tx.Exec(ctx, `insert into privacy_incident_marks (tenant_id, incident_id, personal, note, marked_by)
		values (app_tenant(), $1, $2, $3, $4)
		on conflict (tenant_id, incident_id) do update set personal = excluded.personal, note = excluded.note, marked_by = excluded.marked_by, marked_at = now()`,
		id, in.Personal, in.Note, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from privacy_incident_treatments where incident_id = $1`, id); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `insert into privacy_incident_treatments (tenant_id, incident_id, treatment_id)
			select app_tenant(), $1, t from unnest($2::uuid[]) t`, id, ids); err != nil {
			return nil, err
		}
	}
	if err := auditEv(ctx, tx, actor, "privacy.breach.mark", "incident", id, map[string]any{"personal": in.Personal, "treatments": len(ids)}); err != nil {
		return nil, err
	}
	return s.Breach(ctx, tx, actor, id)
}

// NotifyBreach registra la notifica all'autorità di controllo, oppure che non è dovuta (con il motivo),
// oppure la azzera (privacy.write). Segna anche l'incidente come violazione di dati personali.
func (s *Service) NotifyBreach(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in NotifyInput) (*Breach, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if !isUUID(id) {
		return nil, engine.ErrNotFound
	}
	if err := checkElements(ctx, tx, "id", "element", []string{id}, "incident"); err != nil {
		return nil, engine.ErrNotFound
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if err := checkLen("reason", in.Reason, 2000); err != nil {
		return nil, err
	}
	var at *time.Time
	switch {
	case in.Clear:
	case in.NotRequired:
		if in.Reason == "" {
			return nil, engine.InvalidCode("validation.privacy.reason_required", "reason", "indica perché la notifica non è dovuta")
		}
	default:
		t := s.now()
		if strings.TrimSpace(in.NotifiedAt) != "" {
			p, err := time.Parse(time.RFC3339, strings.TrimSpace(in.NotifiedAt))
			if err != nil {
				return nil, engine.DateInvalid("notified_at", "data e ora non valide (formato RFC 3339)")
			}
			t = p
		}
		if t.After(s.now().Add(5 * time.Minute)) {
			return nil, engine.InvalidCode("validation.privacy.notified_future", "notified_at", "la notifica non può essere nel futuro")
		}
		at = &t
	}
	reason := in.Reason
	if !in.NotRequired {
		reason = ""
	}
	if _, err := tx.Exec(ctx, `insert into privacy_incident_marks (tenant_id, incident_id, personal, notified_at, notified_by, not_required, not_required_reason, marked_by)
		values (app_tenant(), $1, true, $2, $3, $4, $5, $3)
		on conflict (tenant_id, incident_id) do update set notified_at = excluded.notified_at, notified_by = excluded.notified_by,
			not_required = excluded.not_required, not_required_reason = excluded.not_required_reason`,
		id, at, nullable(actor.MemberID), in.NotRequired && !in.Clear, reason); err != nil {
		return nil, err
	}
	if err := auditEv(ctx, tx, actor, "privacy.breach.notify", "incident", id, map[string]any{"not_required": in.NotRequired && !in.Clear, "cleared": in.Clear}); err != nil {
		return nil, err
	}
	return s.Breach(ctx, tx, actor, id)
}
