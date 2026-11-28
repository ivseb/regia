package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Lavoratore delle automazioni: esegue le regole a tempo («at:») e periodiche («every:»).
//
//   - Fuso: tenants.default_timezone (predefinito Europe/Rome). «Oggi» è il giorno in quel fuso;
//     una data senza ora (campo data, scadenza, inizio) vale a mezzanotte di quel giorno.
//   - Ora: le occorrenze di oggi scattano dal primo giro dopo run_hour (automation_settings).
//   - Idempotenza: prima di eseguire si «prenota» la riga di rule_runs (unica per processo,
//     regola, elemento e occorrenza). Una riga già presente vuol dire: già fatto.
//   - Recupero: un'occorrenza scatta se è caduta negli ultimi recoveryDays giorni e non prima
//     della pubblicazione della versione (una regola nuova non scatta su date passate).
//   - Condizione: si valuta una volta, all'occorrenza; se è falsa la riga resta «skipped».
//   - Tetto: al più max_per_run prenotazioni per giro e per tenant; le altre al giro dopo.
//   - Ogni esecuzione è una transazione a sé, con le azioni in un savepoint: un'azione che
//     fallisce lascia la riga in «error» con il motivo, senza fermare le altre.
//   - Elementi: quelli non cancellati, non in bozza e non chiusi (categoria done o cancelled).

const (
	recoveryDays = 7
	// skippedKeepDays: le righe «skipped» servono solo all'idempotenza della finestra di recupero.
	skippedKeepDays = 30
)

// AutomationSettings sono le impostazioni del tenant (automation_settings).
type AutomationSettings struct {
	Enabled   bool `json:"enabled"`
	RunHour   int  `json:"run_hour"`
	MaxPerRun int  `json:"max_per_run"`
}

// GetAutomationSettings legge le impostazioni (i predefiniti se il tenant non le ha cambiate).
func GetAutomationSettings(ctx context.Context, tx pgx.Tx) (AutomationSettings, error) {
	st := AutomationSettings{Enabled: true, RunHour: 8, MaxPerRun: 200}
	err := tx.QueryRow(ctx, "select enabled, run_hour, max_per_run from automation_settings").Scan(&st.Enabled, &st.RunHour, &st.MaxPerRun)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return st, err
}

// SetAutomationSettings salva le impostazioni (process.admin).
func SetAutomationSettings(ctx context.Context, tx pgx.Tx, actor Actor, st AutomationSettings) (AutomationSettings, error) {
	if !actor.Has("process.admin") {
		return st, ErrForbidden
	}
	if st.RunHour < 0 || st.RunHour > 23 {
		return st, invalidCode("validation.out_of_range", "run_hour", "ora non valida (0-23)")
	}
	if st.MaxPerRun < 1 || st.MaxPerRun > 5000 {
		return st, invalidCode("validation.out_of_range", "max_per_run", "tetto non valido (1-5000)")
	}
	if _, err := tx.Exec(ctx, `insert into automation_settings (tenant_id, enabled, run_hour, max_per_run)
		values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id) do update set enabled = excluded.enabled, run_hour = excluded.run_hour,
			max_per_run = excluded.max_per_run, updated_at = now()`, st.Enabled, st.RunHour, st.MaxPerRun); err != nil {
		return st, err
	}
	err := audit.Append(ctx, tx, audit.Event{Action: "automation.settings", EntityType: "automation", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"enabled": st.Enabled, "run_hour": st.RunHour, "max_per_run": st.MaxPerRun}})
	return st, err
}

// --- Date dell'elemento ---

// DateSource è una data dell'elemento a cui si può legare un evento «at:».
type DateSource struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Source: base (scadenza, inizio) | campo (campo data del tipo) | modulo.
	Source string `json:"source"`
	sql    string // select element_id, at …
}

// DescribeDate registra una data offerta da un modulo per un tipo di elemento. sql è una query
// che restituisce le colonne element_id (uuid) e at (timestamptz) per gli elementi del tipo.
func (s *Service) DescribeDate(typeKey, key, name, sql string) {
	r := &s.editor
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dates == nil {
		r.dates = map[string][]DateSource{}
	}
	r.dates[typeKey] = append(slices.DeleteFunc(r.dates[typeKey], func(d DateSource) bool { return d.Key == key }),
		DateSource{Key: key, Name: name, Source: "modulo", sql: sql})
}

// DateSources elenca le date del tipo: scadenza e inizio, i campi data, quelle dei moduli.
func (s *Service) DateSources(ctx context.Context, tx pgx.Tx, typeID string) ([]DateSource, error) {
	out := []DateSource{
		{Key: "scadenza", Name: "Scadenza", Source: "base", sql: "select id as element_id, due_at as at from elements where due_at is not null"},
		{Key: "inizio", Name: "Inizio", Source: "base", sql: "select id as element_id, start_at as at from elements where start_at is not null"},
	}
	rows, err := tx.Query(ctx, `select f.id::text, f.key, f.name, t.key from type_fields tf
		join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
		join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id
		where tf.type_id = $1 and tf.space_id is null and f.data_type in ('date', 'datetime') and not f.archived
		order by tf.ordinal, f.name`, typeID)
	if err != nil {
		return nil, err
	}
	var id, key, name, typeKey string
	if _, err := pgx.ForEachRow(rows, []any{&id, &key, &name, &typeKey}, func() error {
		out = append(out, DateSource{Key: "campo." + key, Name: name, Source: "campo",
			sql: fmt.Sprintf("select element_id, min(v_ts) as at from element_values where field_id = '%s' and v_ts is not null group by element_id", id)})
		return nil
	}); err != nil {
		return nil, err
	}
	if typeKey == "" {
		if err := tx.QueryRow(ctx, "select key from element_types where id = $1", typeID).Scan(&typeKey); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	s.editor.mu.RLock()
	out = append(out, s.editor.dates[typeKey]...)
	s.editor.mu.RUnlock()
	return out, nil
}

func findDate(ds []DateSource, key string) (DateSource, bool) {
	for _, d := range ds {
		if d.Key == key {
			return d, true
		}
	}
	return DateSource{}, false
}

// tenantDay è il giorno di una data nel fuso del tenant. Le date senza ora sono salvate a
// mezzanotte UTC: valgono quel giorno (cioè mezzanotte ora del tenant), senza conversioni.
func tenantDay(t time.Time, loc *time.Location) time.Time {
	u := t.UTC()
	if u.Hour() == 0 && u.Minute() == 0 && u.Second() == 0 {
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// isoWeekday: 1 = lunedì … 7 = domenica.
func isoWeekday(t time.Time) int {
	if w := int(t.Weekday()); w != 0 {
		return w
	}
	return 7
}

// --- Il lavoratore ---

// RunAutomations esegue i giri a intervalli finché ctx non è chiuso.
func (s *Service) RunAutomations(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	run := func() {
		n, err := s.CheckAutomations(ctx, d, time.Now())
		if err != nil {
			log.Warn("automazioni a tempo", "errore", err)
		} else if n > 0 {
			log.Info("automazioni a tempo eseguite", "numero", n)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// CheckAutomations fa un giro su tutti i tenant (solo l'elenco passa dal ruolo di piattaforma).
// Restituisce le esecuzioni andate a buon fine.
func (s *Service) CheckAutomations(ctx context.Context, d *db.DB, now time.Time) (int, error) {
	rows, err := d.Owner.Query(ctx, `select id::text from tenants`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, t := range tenants {
		n, err := s.CheckAutomationsTenant(ctx, d, t, now)
		total += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// timedRule è una regola a tempo o periodica di una versione in uso.
type timedRule struct {
	rule      Rule
	ev        RuleEvent
	w         *Workflow
	processID string
	versionID string
	version   int
	typeID    string
	published time.Time
}

// candidate è un'occorrenza da eseguire.
type candidate struct {
	r          *timedRule
	elementID  string
	occurrence string
}

// CheckAutomationsTenant fa un giro nel tenant.
func (s *Service) CheckAutomationsTenant(ctx context.Context, d *db.DB, tenantID string, now time.Time) (int, error) {
	var cands []candidate
	var loc *time.Location
	var today time.Time
	err := d.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		st, err := GetAutomationSettings(ctx, tx)
		if err != nil || !st.Enabled {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from rule_runs where status = 'skipped' and fired_at < $1`, now.AddDate(0, 0, -skippedKeepDays)); err != nil {
			return err
		}
		loc = TenantLocation(ctx, tx)
		local := now.In(loc)
		today = dateOnly(local)
		hourOK := local.Hour() >= st.RunHour
		rules, err := s.timedRules(ctx, tx, loc)
		if err != nil {
			return err
		}
		budget := st.MaxPerRun
		for i := range rules {
			if budget <= 0 {
				break
			}
			found, err := s.occurrences(ctx, tx, &rules[i], loc, today, hourOK, budget)
			if err != nil {
				return err
			}
			cands = append(cands, found...)
			budget -= len(found)
		}
		return nil
	})
	if err != nil || len(cands) == 0 {
		return 0, err
	}
	done := 0
	var errs []error
	for _, c := range cands {
		ok, err := s.execute(ctx, d, tenantID, c, today)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			done++
		}
	}
	return done, errors.Join(errs...)
}

// timedRules elenca le regole a tempo e periodiche delle versioni su cui c'è almeno un elemento.
func (s *Service) timedRules(ctx context.Context, tx pgx.Tx, loc *time.Location) ([]timedRule, error) {
	rows, err := tx.Query(ctx, `select v.id::text, v.process_id::text, v.number, v.definition, coalesce(v.published_at, v.created_at), p.type_id::text
		from process_versions v
		join process_definitions p on p.tenant_id = v.tenant_id and p.id = v.process_id
		where v.status <> 'draft'
		  and v.definition @? '$.rules[*] ? (@.on starts with "at:" || @.on starts with "every:")'
		  and exists (select 1 from elements e where e.process_version_id = v.id and e.deleted_at is null)
		order by p.key, v.number`)
	if err != nil {
		return nil, err
	}
	type ver struct {
		id, process, typeID string
		number              int
		def                 []byte
		published           time.Time
	}
	vers, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ver, error) {
		var v ver
		err := r.Scan(&v.id, &v.process, &v.number, &v.def, &v.published, &v.typeID)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	var out []timedRule
	for _, v := range vers {
		var w Workflow
		if json.Unmarshal(v.def, &w) != nil {
			continue
		}
		w.compile()
		wp := &w
		for _, r := range w.Rules {
			ev, err := ParseRuleEvent(r.On)
			if err != nil || !ev.Timed() {
				continue
			}
			out = append(out, timedRule{rule: r, ev: ev, w: wp, processID: v.process, versionID: v.id, version: v.number,
				typeID: v.typeID, published: tenantDay(v.published, loc)})
		}
	}
	return out, nil
}

const openElements = `e.deleted_at is null and not e.is_draft and e.number is not null
	and not exists (select 1 from options o where o.tenant_id = e.tenant_id and o.id = e.state_id and o.category in ('done', 'cancelled'))`

// occurrences trova le occorrenze dovute di una regola non ancora eseguite (al più limit).
func (s *Service) occurrences(ctx context.Context, tx pgx.Tx, r *timedRule, loc *time.Location, today time.Time, hourOK bool, limit int) ([]candidate, error) {
	low := today.AddDate(0, 0, -recoveryDays)
	if r.published.After(low) {
		low = r.published
	}
	if !hourOK {
		today = today.AddDate(0, 0, -1) // le occorrenze di oggi aspettano l'ora del tenant
	}
	if today.Before(low) {
		return nil, nil
	}
	var out []candidate
	if r.ev.Kind == "every" {
		day := today
		if r.ev.Weekday != 0 {
			// L'ultimo giorno della settimana scelto, non oltre oggi e non prima di low.
			for isoWeekday(day) != r.ev.Weekday {
				day = day.AddDate(0, 0, -1)
			}
		}
		if day.Before(low) {
			return nil, nil
		}
		occ := day.Format(time.DateOnly)
		rows, err := tx.Query(ctx, `select e.id::text from elements e
			where e.process_version_id = $1 and `+openElements+`
			  and not exists (select 1 from rule_runs rr where rr.process_id = $2 and rr.rule_key = $3 and rr.element_id = e.id and rr.occurrence = $4)
			order by e.number limit $5`, r.versionID, r.processID, r.rule.Key, occ, limit)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out = append(out, candidate{r: r, elementID: id, occurrence: occ})
		}
		return out, nil
	}
	ds, err := s.DateSources(ctx, tx, r.typeID)
	if err != nil {
		return nil, err
	}
	src, ok := findDate(ds, r.ev.Date)
	if !ok {
		return nil, nil // data non più disponibile (campo tolto dal tipo): la validazione lo segnala
	}
	// Le date che, spostate di offset giorni, cadono nella finestra [low, today] (con un giorno
	// di margine per il fuso; il controllo esatto è sotto).
	from := low.AddDate(0, 0, -r.ev.Offset-1)
	to := today.AddDate(0, 0, -r.ev.Offset+2)
	rows, err := tx.Query(ctx, `with d as (`+src.sql+`)
		select e.id::text, d.at from elements e join d on d.element_id = e.id
		where e.process_version_id = $1 and `+openElements+` and d.at >= $2 and d.at < $3
		order by d.at, e.number`, r.versionID, from, to)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id string
		at time.Time
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (hit, error) {
		var h hit
		err := row.Scan(&h.id, &h.at)
		return h, err
	})
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	done := map[string]bool{}
	drows, err := tx.Query(ctx, `select element_id::text || '|' || occurrence from rule_runs
		where process_id = $1 and rule_key = $2 and fired_at > now() - make_interval(days => $3)`, r.processID, r.rule.Key, recoveryDays+skippedKeepDays)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(drows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		done[k] = true
	}
	for _, h := range hits {
		occDay := tenantDay(h.at, loc).AddDate(0, 0, r.ev.Offset)
		if occDay.Before(low) || occDay.After(today) {
			continue
		}
		occ := occDay.Format(time.DateOnly)
		if done[h.id+"|"+occ] {
			continue
		}
		out = append(out, candidate{r: r, elementID: h.id, occurrence: occ})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// execute esegue un'occorrenza nella sua transazione. ok = azioni eseguite (anche senza effetto).
func (s *Service) execute(ctx context.Context, d *db.DB, tenantID string, c candidate, today time.Time) (bool, error) {
	ok := false
	err := d.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var runID string
		err := tx.QueryRow(ctx, `insert into rule_runs (tenant_id, process_id, version_id, rule_key, element_id, occurrence, event)
			values (app_tenant(), $1, $2, $3, $4, $5, $6)
			on conflict (tenant_id, process_id, rule_key, element_id, occurrence) do nothing
			returning id::text`, c.r.processID, c.r.versionID, c.r.rule.Key, c.elementID, c.occurrence, c.r.rule.On).Scan(&runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // l'ha già presa un altro giro
		}
		if err != nil {
			return err
		}
		finish := func(status, note string) error {
			_, err := tx.Exec(ctx, "update rule_runs set status = $2, note = left($3, 1000) where id = $1", runID, status, note)
			return err
		}
		el, err := s.Get(ctx, tx, SystemReader, c.elementID)
		if errors.Is(err, ErrNotFound) {
			return finish("skipped", "elemento non disponibile")
		}
		if err != nil {
			return err
		}
		f, err := s.ElementFacts(ctx, tx, el)
		if err != nil {
			return err
		}
		if c.r.rule.When != "" && !c.r.w.cond(c.r.rule.When).Eval(f) {
			return finish("skipped", "")
		}
		run := &RuleRun{Rule: c.r.rule, ProcessID: c.r.processID, VersionID: c.r.versionID, Version: c.r.version,
			Event: c.r.rule.On, Occurrence: c.occurrence, Facts: f, System: true, Today: today.Format(time.DateOnly)}
		id := c.r.processID + "/" + c.r.rule.Key + "/" + el.ID
		system := Actor{Permissions: []string{"*"}}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		var notes []string
		failed := ""
		for _, a := range c.r.rule.Actions {
			actx := context.WithValue(withRule(ctx, id, automationKinds[a.Kind]), runKey{}, run)
			note, err := s.runRuleAction(actx, sp, system, el, a)
			if errors.Is(err, ErrNoEffect) {
				continue
			}
			if err != nil {
				failed = a.Kind + ": " + err.Error()
				break
			}
			p := run.payload()
			p["action"], p["note"] = a.Kind, note
			if err := addActivity(ctx, sp, el.ID, "", "rule", c.r.rule.Key, p, nil); err != nil {
				failed = err.Error()
				break
			}
			if note != "" {
				notes = append(notes, note)
			}
		}
		if failed != "" {
			_ = sp.Rollback(ctx)
		} else if err := sp.Commit(ctx); err != nil {
			return err
		}
		status, note := "ok", strings.Join(notes, "; ")
		if failed != "" {
			status, note = "error", failed
		}
		p := run.payload()
		p["status"] = status
		if note != "" {
			p["note"] = note
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "automation.run", EntityType: el.TypeKey, EntityID: el.ID, Payload: p}); err != nil {
			return err
		}
		ok = failed == ""
		return finish(status, note)
	})
	return ok, err
}

// --- Esecuzioni di una regola (sola lettura) ---

// RuleRunItem è un'esecuzione di una regola a tempo o periodica.
type RuleRunItem struct {
	ElementKey   string    `json:"element_key"`
	ElementTitle string    `json:"element_title"`
	Occurrence   string    `json:"occurrence"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	Note         string    `json:"note,omitempty"`
	FiredAt      time.Time `json:"fired_at"`
}

// RuleRuns elenca le ultime esecuzioni di una regola di un processo (process.admin).
func (s *Service) RuleRuns(ctx context.Context, tx pgx.Tx, actor Actor, processKey, ruleKey string, limit int) ([]RuleRunItem, error) {
	if !actor.Has("process.admin") {
		return nil, ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `select coalesce(sp.key || '-' || e.number, ''), e.title, rr.occurrence, coalesce(v.number, 0), rr.status, rr.note, rr.fired_at
		from rule_runs rr
		join process_definitions p on p.tenant_id = rr.tenant_id and p.id = rr.process_id
		join elements e on e.tenant_id = rr.tenant_id and e.id = rr.element_id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join process_versions v on v.tenant_id = rr.tenant_id and v.id = rr.version_id
		where (p.key = $1 or p.id::text = $1) and rr.rule_key = $2 and rr.status <> 'skipped'
		order by rr.fired_at desc limit $3`, processKey, ruleKey, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RuleRunItem, error) {
		var it RuleRunItem
		err := r.Scan(&it.ElementKey, &it.ElementTitle, &it.Occurrence, &it.Version, &it.Status, &it.Note, &it.FiredAt)
		return it, err
	})
}

// --- Anteprima ---

// RulePreview è l'anteprima di una regola: quanti elementi del tipo la farebbero scattare.
type RulePreview struct {
	ConditionCount
	Event RuleEvent `json:"event"`
	// From e To: la finestra dell'anteprima (giorni nel fuso del tenant).
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// PreviewRule conta gli elementi del tipo (leggibili dalla persona) su cui la regola
// scatterebbe: per «at:» nei prossimi days giorni (occorrenze da oggi a oggi+days, con la
// condizione valutata sui dati di oggi), per «every:» al prossimo giro, per gli altri eventi
// oggi (come /conditions/count).
func (s *Service) PreviewRule(ctx context.Context, tx pgx.Tx, actor Actor, typeKey, on, when string, days int) (*RulePreview, error) {
	ev, err := ParseRuleEvent(on)
	if err != nil {
		return &RulePreview{ConditionCount: ConditionCount{Error: err.Error(), Items: []ConditionItem{}}}, nil
	}
	if days <= 0 || days > 366 {
		days = 30
	}
	if !ev.Timed() {
		cc, err := s.CountCondition(ctx, tx, actor, when, typeKey)
		if err != nil {
			return nil, err
		}
		return &RulePreview{ConditionCount: *cc, Event: ev}, nil
	}
	out := &RulePreview{ConditionCount: ConditionCount{Items: []ConditionItem{}}, Event: ev}
	c, err := ParseCondition(when)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.OK = true
	t, err := LoadType(ctx, tx, typeKey)
	if err != nil {
		return nil, err
	}
	loc := TenantLocation(ctx, tx)
	today := dateOnly(time.Now().In(loc))
	last := today.AddDate(0, 0, days)
	if ev.Kind == "every" {
		next := today
		for ev.Weekday != 0 && isoWeekday(next) != ev.Weekday {
			next = next.AddDate(0, 0, 1)
		}
		out.From, out.To = next.Format(time.DateOnly), next.Format(time.DateOnly)
	} else {
		out.From, out.To = today.Format(time.DateOnly), last.Format(time.DateOnly)
	}
	dates := map[string]time.Time{}
	if ev.Kind == "at" {
		ds, err := s.DateSources(ctx, tx, t.ID)
		if err != nil {
			return nil, err
		}
		src, ok := findDate(ds, ev.Date)
		if !ok {
			out.OK, out.Error = false, fmt.Sprintf("data %q non disponibile per il tipo", ev.Date)
			return out, nil
		}
		rows, err := tx.Query(ctx, `with d as (`+src.sql+`) select d.element_id::text, d.at from d
			join elements e on e.id = d.element_id where e.type_id = $1`, t.ID)
		if err != nil {
			return nil, err
		}
		var id string
		var at time.Time
		if _, err := pgx.ForEachRow(rows, []any{&id, &at}, func() error { dates[id] = at; return nil }); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `select e.id::text from elements e where e.type_id = $1 and `+openElements+`
		order by e.number desc limit $2`, t.ID, maxCountElements+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(ids) > maxCountElements {
		ids, out.Truncated = ids[:maxCountElements], true
	}
	for _, id := range ids {
		if ev.Kind == "at" {
			at, ok := dates[id]
			if !ok {
				continue
			}
			occ := tenantDay(at, loc).AddDate(0, 0, ev.Offset)
			if occ.Before(today) || occ.After(last) {
				continue
			}
		}
		el, err := s.Get(ctx, tx, actor, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Total++
		f, err := s.ElementFacts(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		if c.Eval(f) {
			out.Matched++
			if len(out.Items) < 5 {
				out.Items = append(out.Items, ConditionItem{Key: el.Key, Title: el.Title})
			}
		}
	}
	return out, nil
}
