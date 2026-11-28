package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/grc"
	"regia/internal/platform/audit"
	"regia/internal/platform/i18n"
)

// TestBrief è un test in un elenco.
type TestBrief struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Title         string  `json:"title"`
	State         string  `json:"state"`
	StateCategory string  `json:"state_category"`
	Type          string  `json:"type"`
	PlannedOn     *string `json:"planned_on"`
	ExecutedOn    *string `json:"executed_on"`
	Outcome       *string `json:"outcome"`
}

func day(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func (t *testN) brief() TestBrief {
	return TestBrief{ID: t.ID, Key: t.Key, Title: t.Title, State: t.State, StateCategory: t.StateCat, Type: t.Type,
		PlannedOn: day(t.Planned), ExecutedOn: day(t.Executed), Outcome: t.Outcome}
}

// SubjectRef è l'oggetto di un test.
type SubjectRef struct {
	Ref
	Kind string `json:"kind"` // application | capability
}

// Finding è un rilievo del test, con il lavoro nato da lì.
type Finding struct {
	Text string    `json:"text"`
	Ref  string    `json:"ref"`
	Work *WorkLink `json:"work"`
}

// WorkLink è l'elemento di lavoro creato da un rilievo.
type WorkLink struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Pair è una coppia RTO/RPO.
type Pair struct {
	RTO *int `json:"rto"`
	RPO *int `json:"rpo"`
}

// TestView è il dettaglio del test.
type TestView struct {
	TestBrief
	Subject     SubjectRef `json:"subject"`
	TypeName    string     `json:"type_name"`
	RTOMeasured *int       `json:"rto_measured_minutes"`
	RPOMeasured *int       `json:"rpo_measured_minutes"`
	Notes       string     `json:"notes"`
	Findings    []Finding  `json:"findings"`
	Required    Pair       `json:"required"`
	Declared    Pair       `json:"declared"`
	RTOOK       *bool      `json:"rto_ok"`
	RPOOK       *bool      `json:"rpo_ok"`
	Evidence    *struct {
		Key string `json:"key"`
	} `json:"evidence"`
	EvidenceNote string `json:"evidence_note"`
	Can          struct {
		Edit bool `json:"edit"`
	} `json:"can"`
}

func findingRef(testID string, i int) string { return "finding:" + testID + ":" + strconv.Itoa(i) }

func (s *Service) testElement(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != ResTestType {
		return nil, engine.NotFound("ref", "", "test %q inesistente", ref)
	}
	return el, nil
}

// subjectOf risolve l'oggetto di un test: applicazione o capability.
func (s *Service) subjectOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, string, error) {
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, "", engine.NotFound("subject", "", "oggetto %q inesistente", ref)
	}
	switch el.TypeKey {
	case AppType:
		return el, "application", nil
	case CapabilityType:
		return el, "capability", nil
	}
	return nil, "", engine.InvalidCode("validation.continuity.subject_invalid", "subject", "un test riguarda un'applicazione o una capability")
}

// Test restituisce il dettaglio del test.
func (s *Service) Test(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*TestView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.testElement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	cfg, _, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	v := &TestView{TestBrief: TestBrief{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, StateCategory: el.StateCategory}, Findings: []Finding{}}
	var planned, executed *time.Time
	var raw []byte
	var evKey *string
	var subjType string
	err = tx.QueryRow(ctx, `select x.test_type, x.planned_on, x.executed_on, x.outcome, x.rto_measured_minutes, x.rpo_measured_minutes, x.notes, x.findings,
			x.evidence_note, x.subject_id::text, ss.key || '-' || se.number, se.title, st.key,
			(select es.key || '-' || ev.number from elements ev join spaces es on es.tenant_id = ev.tenant_id and es.id = ev.space_id where ev.id = x.evidence_id)
		from ext_resilience_test x join elements se on se.tenant_id = x.tenant_id and se.id = x.subject_id
		join element_types st on st.tenant_id = se.tenant_id and st.id = se.type_id
		join spaces ss on ss.tenant_id = se.tenant_id and ss.id = se.space_id where x.element_id = $1`, el.ID).
		Scan(&v.Type, &planned, &executed, &v.Outcome, &v.RTOMeasured, &v.RPOMeasured, &v.Notes, &raw, &v.EvidenceNote,
			&v.Subject.ID, &v.Subject.Key, &v.Subject.Title, &subjType, &evKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.NotFound("ref", "", "test %q senza dati", ref)
	}
	if err != nil {
		return nil, err
	}
	v.PlannedOn, v.ExecutedOn = day(planned), day(executed)
	v.Subject.Kind = "application"
	if subjType == CapabilityType {
		v.Subject.Kind = "capability"
	}
	if t, ok := cfg.testType(v.Type); ok {
		v.TypeName = t.Name
	} else {
		v.TypeName = v.Type
	}
	if evKey != nil {
		v.Evidence = &struct {
			Key string `json:"key"`
		}{*evKey}
	}
	var texts []string
	if err := json.Unmarshal(raw, &texts); err != nil {
		return nil, err
	}
	for i, t := range texts {
		f := Finding{Text: t, Ref: findingRef(el.ID, i)}
		var w WorkLink
		err := tx.QueryRow(ctx, `select pl.target_ref, coalesce(e.title, pl.target_label), coalesce(o.name, '') from problem_links pl
			left join elements e on e.tenant_id = pl.tenant_id and e.id = pl.target_element_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where pl.source_kind = 'continuity' and pl.source_ref = $1 and pl.action = 'work_item' order by pl.created_at desc limit 1`, f.Ref).Scan(&w.Key, &w.Title, &w.State)
		if err == nil {
			f.Work = &w
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		v.Findings = append(v.Findings, f)
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	if a, ok := m.apps[v.Subject.ID]; ok {
		r := m.required(a)
		v.Required, v.Declared = Pair{r.RTO, r.RPO}, Pair{a.RTO, a.RPO}
	} else if bn, ok := m.bias[v.Subject.ID]; ok && bn.Approved != nil {
		v.Required = Pair{bn.Approved.RTO, bn.Approved.RPO}
	}
	target := v.Required
	if target.RTO == nil {
		target.RTO = v.Declared.RTO
	}
	if target.RPO == nil {
		target.RPO = v.Declared.RPO
	}
	v.RTOOK, v.RPOOK = cmpOK(v.RTOMeasured, target.RTO), cmpOK(v.RPOMeasured, target.RPO)
	v.Can.Edit = actor.Has(PermWrite) && el.StateCategory != "done" && el.StateCategory != "cancelled"
	return v, nil
}

// TestInput pianifica un test.
type TestInput struct {
	Subject   string `json:"subject"`
	Type      string `json:"type"`
	PlannedOn string `json:"planned_on"`
	Title     string `json:"title"`
}

func parseDate(field, s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, engine.DateInvalid(field, "data non valida (AAAA-MM-GG)")
	}
	return &t, nil
}

// CreateTest pianifica un test di resilienza (continuity.write).
func (s *Service) CreateTest(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TestInput) (*TestView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := requireReady(ctx, tx); err != nil {
		return nil, err
	}
	subj, _, err := s.subjectOf(ctx, tx, actor, in.Subject)
	if err != nil {
		return nil, err
	}
	cfg, _, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	tt, ok := cfg.testType(in.Type)
	if !ok {
		return nil, engine.InvalidCode("validation.continuity.test_type", "type", "tipo di test non configurato")
	}
	planned, err := parseDate("planned_on", in.PlannedOn)
	if err != nil {
		return nil, err
	}
	if planned == nil {
		return nil, engine.Required("planned_on", "indica la data pianificata")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = i18n.T(i18n.Lang(ctx), "continuity.test.title", i18n.P{"type": tt.Name, "subject": subj.Title, "year": strconv.Itoa(planned.Year())})
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: ResTestType, Title: title, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_resilience_test (tenant_id, element_id, subject_id, test_type, planned_on) values (app_tenant(), $1, $2, $3, $4)`,
		el.ID, subj.ID, tt.Key, planned); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update elements set due_at = $2 where id = $1`, el.ID, planned); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.test.create", EntityType: ResTestType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"subject": subj.Key, "type": tt.Key}}); err != nil {
		return nil, err
	}
	return s.Test(ctx, tx, actor, el.ID)
}

type optStr struct {
	Set bool
	V   *string
}

func (o *optStr) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.V)
}

// TestUpdate sono i campi modificabili del test.
type TestUpdate struct {
	Type        *string   `json:"type"`
	PlannedOn   optStr    `json:"planned_on"`
	ExecutedOn  optStr    `json:"executed_on"`
	RTOMeasured optInt    `json:"rto_measured_minutes"`
	RPOMeasured optInt    `json:"rpo_measured_minutes"`
	Outcome     optStr    `json:"outcome"`
	Findings    *[]string `json:"findings"`
	Notes       *string   `json:"notes"`
}

func optDate(field string, o optStr) (*time.Time, error) {
	if !o.Set || o.V == nil {
		return nil, nil
	}
	return parseDate(field, *o.V)
}

// UpdateTest modifica il test (continuity.write); un test chiuso o annullato non cambia più.
func (s *Service) UpdateTest(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in TestUpdate) (*TestView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	el, err := s.testElement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.StateCategory == "done" || el.StateCategory == "cancelled" {
		return nil, engine.InvalidCode("validation.continuity.test_locked", "state", "il test è chiuso o annullato")
	}
	cfg, _, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	if in.Type != nil {
		if _, ok := cfg.testType(*in.Type); !ok {
			return nil, engine.InvalidCode("validation.continuity.test_type", "type", "tipo di test non configurato")
		}
	}
	planned, err := optDate("planned_on", in.PlannedOn)
	if err != nil {
		return nil, err
	}
	if in.PlannedOn.Set && planned == nil {
		return nil, engine.Required("planned_on", "indica la data pianificata")
	}
	executed, err := optDate("executed_on", in.ExecutedOn)
	if err != nil {
		return nil, err
	}
	if executed != nil && executed.After(today(s.Now())) {
		return nil, engine.DateInvalid("executed_on", "la data di esecuzione non può essere nel futuro")
	}
	for f, o := range map[string]optInt{"rto_measured_minutes": in.RTOMeasured, "rpo_measured_minutes": in.RPOMeasured} {
		if err := checkMinutes(f, o); err != nil {
			return nil, err
		}
	}
	if in.Outcome.Set && in.Outcome.V != nil && !slices.Contains(outcomes, *in.Outcome.V) {
		return nil, engine.InvalidCode("validation.continuity.outcome", "outcome", "esito non valido")
	}
	var findings []byte
	if in.Findings != nil {
		clean := []string{}
		for _, f := range *in.Findings {
			if f = strings.TrimSpace(f); f != "" {
				if len(f) > 2000 {
					return nil, engine.TooLong("findings", 2000, "rilievo troppo lungo")
				}
				clean = append(clean, f)
			}
		}
		if len(clean) > 50 {
			return nil, engine.InvalidCode("validation.continuity.outcome", "findings", "al massimo 50 rilievi")
		}
		if findings, err = json.Marshal(clean); err != nil {
			return nil, err
		}
	}
	if in.Notes != nil && len(*in.Notes) > 20000 {
		return nil, engine.TooLong("notes", 20000, "note troppo lunghe")
	}
	if _, err := tx.Exec(ctx, `update ext_resilience_test set
			test_type = coalesce($2, test_type),
			planned_on = case when $3 then $4 else planned_on end,
			executed_on = case when $5 then $6 else executed_on end,
			rto_measured_minutes = case when $7 then $8 else rto_measured_minutes end,
			rpo_measured_minutes = case when $9 then $10 else rpo_measured_minutes end,
			outcome = case when $11 then $12 else outcome end,
			findings = coalesce($13::jsonb, findings),
			notes = coalesce($14, notes)
		where element_id = $1`, el.ID, in.Type, in.PlannedOn.Set, planned, in.ExecutedOn.Set, executed, in.RTOMeasured.Set, in.RTOMeasured.V,
		in.RPOMeasured.Set, in.RPOMeasured.V, in.Outcome.Set, in.Outcome.V, findings, in.Notes); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update elements set version = version + 1, updated_at = now(), updated_by = $2,
		due_at = coalesce((select planned_on from ext_resilience_test where element_id = $1), due_at) where id = $1`, el.ID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.test.update", EntityType: ResTestType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.Test(ctx, tx, actor, el.ID)
}

// guardTest: esito, data di esecuzione e, se non è un successo pieno, almeno un rilievo.
func (s *Service) guardTest(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
	var outcome *string
	var executed *time.Time
	var n int
	err := tx.QueryRow(ctx, `select outcome, executed_on, jsonb_array_length(findings) from ext_resilience_test where element_id = $1`, el.ID).Scan(&outcome, &executed, &n)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if outcome == nil || executed == nil || (*outcome != OutcomePassed && n == 0) {
		return engine.InvalidCode("validation.continuity.outcome", "outcome", "registra l'esito, la data di esecuzione e, per un esito con rilievi o non superato, almeno un rilievo")
	}
	return nil
}

// testTransition: con l'esito registrato nasce l'evidenza automatica (una volta sola).
func (s *Service) testTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	el := ev.Element
	if el.StateCategory == "cancelled" || (!slices.Contains(ev.Transition.Gates, "esito") && el.StateCategory != "done") {
		return nil
	}
	var outcome *string
	var executed *time.Time
	var evidence *string
	var subjKey, subjType, title, typ string
	var rto, rpo *int
	err := tx.QueryRow(ctx, `select x.outcome, x.executed_on, x.evidence_id::text, ss.key || '-' || se.number, st.key, e.title, x.test_type,
			x.rto_measured_minutes, x.rpo_measured_minutes
		from ext_resilience_test x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join elements se on se.tenant_id = x.tenant_id and se.id = x.subject_id
		join element_types st on st.tenant_id = se.tenant_id and st.id = se.type_id
		join spaces ss on ss.tenant_id = se.tenant_id and ss.id = se.space_id where x.element_id = $1`, el.ID).
		Scan(&outcome, &executed, &evidence, &subjKey, &subjType, &title, &typ, &rto, &rpo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if outcome == nil || executed == nil || evidence != nil {
		return nil
	}
	lang := i18n.Lang(ctx)
	note := ""
	if s.grc == nil {
		note = i18n.T(lang, "continuity.evidence.no_grc")
	} else {
		cfg, _, err := loadConfig(ctx, tx)
		if err != nil {
			return err
		}
		maxAge := cfg.TestMaxAgeDays
		valid := executed.AddDate(0, 0, maxAge).Format(time.DateOnly)
		occurred := executed.UTC()
		in := grc.AutoEvidence{Collector: Collector, ExternalRef: el.Key, Title: title, OccurredAt: &occurred, ValidUntil: valid,
			Summary: i18n.T(lang, "continuity.evidence.summary", i18n.P{"outcome": i18n.T(lang, "continuity.outcome."+*outcome),
				"subject": subjKey, "type": typ, "rto": minText(rto), "rpo": minText(rpo)})}
		if subjType == AppType {
			in.Application = subjKey
		}
		// In un punto di salvataggio: se la conformità non è configurata il test va avanti lo stesso.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		res, err := s.grc.RecordAutomatic(ctx, sp, engine.Actor{AccountID: ev.Actor.AccountID, MemberID: ev.Actor.MemberID, IP: ev.Actor.IP, Permissions: []string{"*"}}, in)
		if err != nil {
			_ = sp.Rollback(ctx)
			var ve *engine.ValidationError
			if !errors.As(err, &ve) {
				return err
			}
			note = i18n.T(lang, "continuity.evidence.failed", i18n.P{"reason": ve.Msg})
		} else {
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `update ext_resilience_test set evidence_id = (select e.id from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
				where s.key || '-' || e.number = $2), evidence_note = '' where element_id = $1`, el.ID, res.Key); err != nil {
				return err
			}
		}
	}
	if note != "" {
		if _, err := tx.Exec(ctx, `update ext_resilience_test set evidence_note = $2 where element_id = $1`, el.ID, note); err != nil {
			return err
		}
	}
	return audit.Append(ctx, tx, audit.Event{Action: "continuity.test.outcome", EntityType: ResTestType, EntityID: el.ID,
		ActorAccountID: ev.Actor.AccountID, ActorMemberID: ev.Actor.MemberID, IP: ev.Actor.IP,
		Payload: map[string]any{"outcome": *outcome, "rto_measured_minutes": rto, "rpo_measured_minutes": rpo}})
}

func minText(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d min", *p)
}

// PlanRow è una riga del piano annuale dei test.
type PlanRow struct {
	Subject       SubjectRef  `json:"subject"`
	Critical      bool        `json:"critical"`
	RequiredRTO   *int        `json:"required_rto"`
	Tests         []TestBrief `json:"tests"`
	LastDone      *TestBrief  `json:"last_done"`
	PlannedInYear bool        `json:"planned_in_year"`
}

// TestPlan è il piano annuale.
type TestPlan struct {
	Year  int        `json:"year"`
	Rows  []PlanRow  `json:"rows"`
	Types []TestType `json:"types"`
	Can   Can        `json:"can"`
}

// TestsPlan: funzioni critiche (capability con BIA critica, applicazioni critiche) e ogni oggetto
// con un test nell'anno, con i test dell'anno e l'ultimo eseguito.
func (s *Service) TestsPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, year int) (*TestPlan, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	now := s.Now()
	if year < 2000 || year > 2100 {
		year = now.In(rome()).Year()
	}
	m, err := loadModel(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	out := &TestPlan{Year: year, Rows: []PlanRow{}, Types: m.cfg.TestTypes, Can: can(actor)}
	inYear := func(t *testN) bool {
		d := t.Executed
		if d == nil {
			d = t.Planned
		}
		return d != nil && d.Year() == year && t.StateCat != "cancelled"
	}
	row := func(sub SubjectRef, critical bool, req *int) {
		r := PlanRow{Subject: sub, Critical: critical, RequiredRTO: req, Tests: []TestBrief{}}
		for _, t := range m.tests[sub.ID] {
			if inYear(t) {
				r.Tests = append(r.Tests, t.brief())
			}
		}
		slices.SortFunc(r.Tests, func(a, b TestBrief) int {
			return strings.Compare(deref(firstNonNil(a.ExecutedOn, a.PlannedOn)), deref(firstNonNil(b.ExecutedOn, b.PlannedOn)))
		})
		r.PlannedInYear = len(r.Tests) > 0
		if last := m.lastDone(sub.ID); last != nil {
			b := last.brief()
			r.LastDone = &b
		}
		if critical || r.PlannedInYear {
			out.Rows = append(out.Rows, r)
		}
	}
	for _, id := range m.capOrder {
		c := m.caps[id]
		bn := m.bias[id]
		crit := bn != nil && bn.Approved != nil && bn.Approved.Critical
		var req *int
		if bn != nil && bn.Approved != nil {
			req = bn.Approved.RTO
		}
		row(SubjectRef{Ref: c.Ref, Kind: "capability"}, crit, req)
	}
	for _, id := range m.appOrder {
		a := m.apps[id]
		r := m.required(a)
		row(SubjectRef{Ref: a.Ref, Kind: "application"}, a.Critical || r.Critical, r.RTO)
	}
	return out, nil
}

func firstNonNil(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}
