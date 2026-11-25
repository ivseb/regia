package devsecops

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// RecomputeReport dice quanto è stato calcolato.
type RecomputeReport struct {
	Points  int       `json:"points"`
	Deploys int       `json:"deploys"`
	Incidnt int       `json:"incidents"`
	At      time.Time `json:"at"`
}

func (s *Service) tenantID(ctx context.Context, tx pgx.Tx) (string, error) {
	var t string
	err := tx.QueryRow(ctx, "select app_tenant()::text").Scan(&t)
	return t, err
}

// recompute ricalcola e salva i punti dei periodi (tutta la finestra se full, altrimenti il
// periodo in corso e il precedente). Sostituisce i punti dora.* della finestra.
func (s *Service) recompute(ctx context.Context, tx pgx.Tx, full bool) (RecomputeReport, error) {
	now := s.now()
	rep := RecomputeReport{At: now}
	st, err := loadDoraSettings(ctx, tx)
	if err != nil {
		return rep, err
	}
	tenant, err := s.tenantID(ctx, tx)
	if err != nil {
		return rep, err
	}
	// Dati: dall'inizio della finestra più lunga, con un margine per i rollback.
	from := now
	for _, p := range periods {
		if f := windowFrom(now, p, full); f.Before(from) {
			from = f
		}
	}
	data, err := loadDora(ctx, tx, from.Add(-90*24*time.Hour), now.Add(time.Minute), "")
	if err != nil {
		return rep, err
	}
	data.analyze(st)
	rep.Deploys, rep.Incidnt = len(data.deploys), len(data.incidents)
	pts := data.computePoints(tenant, st, now, full)
	for _, p := range periods {
		if _, err := tx.Exec(ctx, `delete from metric_points where metric_key like 'dora.%' and period = $1 and at >= $2`, p, windowFrom(now, p, full)); err != nil {
			return rep, err
		}
	}
	for len(pts) > 0 {
		n := min(len(pts), 4000)
		if err := insertPoints(ctx, tx, pts[:n]); err != nil {
			return rep, err
		}
		pts = pts[n:]
	}
	rep.Points = 0
	_ = tx.QueryRow(ctx, `select count(*) from metric_points where metric_key like 'dora.%'`).Scan(&rep.Points)
	return rep, nil
}

func insertPoints(ctx context.Context, tx pgx.Tx, pts []Point) error {
	n := len(pts)
	scope, subj, period, key, dims := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	at, val := make([]time.Time, n), make([]float64, n)
	for i, p := range pts {
		scope[i], subj[i], period[i], key[i], at[i], val[i] = p.Subject.Scope, p.Subject.ID, p.Period, p.Key, p.At, p.Value
		raw, _ := json.Marshal(p.Dims)
		dims[i] = string(raw)
	}
	_, err := tx.Exec(ctx, `insert into metric_points (tenant_id, metric_key, scope, subject_id, subject_element_id, period, at, value, dims)
		select app_tenant(), k, sc, sid::uuid, case when sc in ('application', 'asset') then sid::uuid end, pe, t, v, dm::jsonb
		from unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::timestamptz[], $6::float8[], $7::text[]) as x(k, sc, sid, pe, t, v, dm)
		on conflict (tenant_id, metric_key, scope, period, subject_id, at) do update set value = excluded.value, dims = excluded.dims`,
		key, scope, subj, period, at, val, dims)
	return err
}

// Recompute ricalcola i punti (ricalcolo a richiesta: tutta la finestra), con
// architecture.write o tenant.admin.
func (s *Service) Recompute(ctx context.Context, tx pgx.Tx, actor engine.Actor) (RecomputeReport, error) {
	if !CanConfigure(actor) {
		return RecomputeReport{}, engine.ErrForbidden
	}
	rep, err := s.recompute(ctx, tx, true)
	if err != nil {
		return rep, err
	}
	return rep, auditEvent(ctx, tx, actor, "devsecops.dora_recompute", "metric_points", "", map[string]any{"points": rep.Points, "deploys": rep.Deploys, "incidents": rep.Incidnt})
}

// RecomputeTenant è il ricalcolo del job per un tenant già in transazione.
func (s *Service) RecomputeTenant(ctx context.Context, tx pgx.Tx, full bool) (RecomputeReport, error) {
	return s.recompute(ctx, tx, full)
}

// RunDora ricalcola ogni ora i punti del periodo in corso e del precedente per i tenant con
// servizi; una volta al giorno (e al primo avvio) tutta la finestra.
func (s *Service) RunDora(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	lastFull := time.Time{}
	run := func() {
		full := s.now().Sub(lastFull) >= 23*time.Hour
		rows, err := d.Owner.Query(ctx, `select distinct tenant_id::text from services`)
		if err != nil {
			log.Warn("metriche DORA: elenco dei tenant", "errore", err)
			return
		}
		tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			log.Warn("metriche DORA: elenco dei tenant", "errore", err)
			return
		}
		for _, t := range tenants {
			err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
				rep, err := s.recompute(ctx, tx, full)
				if err == nil {
					log.Debug("metriche DORA ricalcolate", "tenant", t, "punti", rep.Points, "completo", full)
				}
				return err
			})
			if err != nil {
				log.Warn("metriche DORA: ricalcolo", "tenant", t, "errore", err)
			}
		}
		if full {
			lastFull = s.now()
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

// ---------------------------------------------------------------------------
// Lettura

// MetricCard è una delle quattro metriche di un soggetto.
type MetricCard struct {
	MetricDef
	Value     *float64   `json:"value"`
	Prev      *float64   `json:"prev"`
	Level     string     `json:"level"`
	PrevLevel string     `json:"prev_level"`
	N         int        `json:"n"`
	Series    []SeriesPt `json:"series"`
	Ladder    Ladder     `json:"ladder"`
}

// SeriesPt è un punto della serie di un grafico.
type SeriesPt struct {
	At      time.Time `json:"at"`
	Value   *float64  `json:"value"`
	Level   string    `json:"level,omitempty"`
	Partial bool      `json:"partial,omitempty"`
}

// CompareRow è una riga del confronto tra team (o applicazioni, asset, servizi).
type CompareRow struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Values   map[string]*float64 `json:"values"`
	Levels   map[string]string   `json:"levels"`
	Level    string              `json:"level"`
	Deploys  int                 `json:"deploys"`
	Attempts int                 `json:"attempts"`
}

// SubjectRef è il soggetto mostrato.
type SubjectRef struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
	Name  string `json:"name"`
}

// Dashboard è la risposta della pagina «Metriche di consegna».
type Dashboard struct {
	Period       string       `json:"period"`
	Subject      SubjectRef   `json:"subject"`
	Bucket       time.Time    `json:"bucket"` // ultimo periodo concluso, quello dei numeri grandi
	BucketEnd    time.Time    `json:"bucket_end"`
	Current      time.Time    `json:"current"` // periodo in corso
	Metrics      []MetricCard `json:"metrics"`
	Level        string       `json:"level"`
	PrevLevel    string       `json:"prev_level"`
	Compare      []CompareRow `json:"compare"`
	CompareScope string       `json:"compare_scope"`
	Subjects     []SubjectRef `json:"subjects"` // per il selettore dell'ambito scelto
	Computed     bool         `json:"computed"`
	ComputedAt   *time.Time   `json:"computed_at"`
	// OpenIncidents: incidenti ancora aperti dell'ambito, per spiegare un tempo di ripristino
	// assente (nessun incidente risolto nel periodo, ma uno aperto).
	OpenIncidents int          `json:"open_incidents"`
	Settings      DoraSettings `json:"settings"`
	CanConfigure  bool         `json:"can_configure"`
}

// subjectName: nome leggibile del soggetto (vuoto se non esiste).
func subjectName(ctx context.Context, tx pgx.Tx, scope, id string) (string, error) {
	var name string
	var q string
	switch scope {
	case ScopeTenant:
		return "Tutta l'organizzazione", nil
	case ScopeTeam:
		q = `select name from teams where id::text = $1`
	case ScopeService:
		q = `select name from services where id::text = $1`
	case ScopeApplication, ScopeAsset:
		q = `select s.key || '-' || e.number || ' ' || e.title from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id::text = $1 and e.deleted_at is null`
	default:
		return "", engine.InvalidValue("scope", "ambito non valido")
	}
	err := tx.QueryRow(ctx, q, id).Scan(&name)
	if err == pgx.ErrNoRows {
		return "", engine.ErrNotFound
	}
	return name, err
}

// ResolveSubject trasforma un riferimento (chiave ARC-3 o id) nell'id del soggetto.
func resolveSubject(ctx context.Context, tx pgx.Tx, scope, ref, tenant string) (string, error) {
	ref = strings.TrimSpace(ref)
	if scope == ScopeTenant || ref == "" {
		return tenant, nil
	}
	if scope == ScopeApplication || scope == ScopeAsset {
		id, _, _, _, err := elementRef(ctx, tx, ref)
		return id, err
	}
	return ref, nil
}

func subjectsOf(ctx context.Context, tx pgx.Tx, scope, period string, since time.Time) ([]SubjectRef, error) {
	var q string
	switch scope {
	case ScopeTeam:
		q = `select m.subject_id::text, t.name from (select distinct subject_id from metric_points where scope = $1 and period = $2 and at >= $3 and metric_key = 'dora.deploy_frequency') m
			join teams t on t.tenant_id = app_tenant() and t.id = m.subject_id order by t.name`
	case ScopeService:
		q = `select m.subject_id::text, t.name from (select distinct subject_id from metric_points where scope = $1 and period = $2 and at >= $3 and metric_key = 'dora.deploy_frequency') m
			join services t on t.tenant_id = app_tenant() and t.id = m.subject_id order by t.name`
	default:
		q = `select m.subject_id::text, s.key || '-' || e.number || ' ' || e.title from (select distinct subject_id from metric_points where scope = $1 and period = $2 and at >= $3 and metric_key = 'dora.deploy_frequency') m
			join elements e on e.tenant_id = app_tenant() and e.id = m.subject_id join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id order by e.title`
	}
	rows, err := tx.Query(ctx, q, scope, period, since)
	if err != nil {
		return nil, err
	}
	var out []SubjectRef
	var r SubjectRef
	_, err = pgx.ForEachRow(rows, []any{&r.ID, &r.Name}, func() error {
		out = append(out, SubjectRef{Scope: scope, ID: r.ID, Name: r.Name})
		return nil
	})
	if out == nil {
		out = []SubjectRef{}
	}
	return out, err
}

type storedPoint struct {
	key   string
	at    time.Time
	value float64
	dims  map[string]any
}

func readSeries(ctx context.Context, tx pgx.Tx, scope, subject, period string, since time.Time) (map[time.Time]map[string]storedPoint, error) {
	rows, err := tx.Query(ctx, `select metric_key, at, value, dims from metric_points
		where scope = $1 and subject_id::text = $2 and period = $3 and at >= $4 and metric_key like 'dora.%'`, scope, subject, period, since)
	if err != nil {
		return nil, err
	}
	out := map[time.Time]map[string]storedPoint{}
	var p storedPoint
	var raw []byte
	_, err = pgx.ForEachRow(rows, []any{&p.key, &p.at, &p.value, &raw}, func() error {
		q := p
		q.at = q.at.UTC()
		q.key = strings.TrimPrefix(q.key, pointPrefix)
		_ = json.Unmarshal(raw, &q.dims)
		if out[q.at] == nil {
			out[q.at] = map[string]storedPoint{}
		}
		out[q.at][q.key] = q
		return nil
	})
	return out, err
}

func levelName(dims map[string]any) string {
	l, _ := dims["level"].(string)
	return l
}

// Dashboard compone la pagina: quattro numeri dell'ultimo periodo concluso con il confronto
// con il precedente, le serie dei grafici e il confronto tra i soggetti dell'ambito
// compareScope (vuoto: nessun confronto). I punti sono quelli salvati.
func (s *Service) Dashboard(ctx context.Context, tx pgx.Tx, actor engine.Actor, period, scope, ref, compareScope string) (*Dashboard, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	if !validPeriod(period) {
		return nil, engine.InvalidValue("period", "periodo non valido (week, month, quarter)")
	}
	if !validScope(scope) {
		return nil, engine.InvalidValue("scope", "ambito non valido")
	}
	if compareScope != "" && (!validScope(compareScope) || compareScope == ScopeTenant) {
		return nil, engine.InvalidValue("compare", "ambito del confronto non valido")
	}
	tenant, err := s.tenantID(ctx, tx)
	if err != nil {
		return nil, err
	}
	subject, err := resolveSubject(ctx, tx, scope, ref, tenant)
	if err != nil {
		return nil, err
	}
	name, err := subjectName(ctx, tx, scope, subject)
	if err != nil {
		return nil, err
	}
	st, err := loadDoraSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	cur := bucketStart(now, period)
	last := prevBucket(cur, period)
	prev := prevBucket(last, period)
	from := windowFrom(now, period, true)
	dash := &Dashboard{Period: period, Subject: SubjectRef{scope, subject, name}, Bucket: last, BucketEnd: cur, Current: cur,
		Metrics: []MetricCard{}, Compare: []CompareRow{}, CompareScope: compareScope, Settings: st, CanConfigure: CanConfigure(actor), Subjects: []SubjectRef{}}
	series, err := readSeries(ctx, tx, scope, subject, period, from)
	if err != nil {
		return nil, err
	}
	var computed *time.Time
	if err := tx.QueryRow(ctx, `select max(at) from metric_points where metric_key = 'dora.deploy_frequency' and period = $1`, period).Scan(&computed); err != nil {
		return nil, err
	}
	dash.Computed = computed != nil
	dash.ComputedAt = computed
	for _, m := range Metrics {
		c := MetricCard{MetricDef: m, Series: []SeriesPt{}, Ladder: st.Thresholds[m.Key]}
		for b := from; !b.After(cur); b = bucketEnd(b, period) {
			pt := SeriesPt{At: b, Partial: b.Equal(cur)}
			if p, ok := series[b][m.Key]; ok {
				v := p.value
				pt.Value, pt.Level = &v, levelName(p.dims)
			}
			c.Series = append(c.Series, pt)
		}
		if p, ok := series[last][m.Key]; ok {
			v := p.value
			c.Value, c.Level = &v, levelName(p.dims)
			if n, ok := p.dims["attempts"].(float64); ok && m.Key == MetricFailure {
				c.N = int(n)
			} else if n, ok := p.dims["n"].(float64); ok {
				c.N = int(n)
			} else if n, ok := p.dims["deploys"].(float64); ok {
				c.N = int(n)
			}
		}
		if p, ok := series[prev][m.Key]; ok {
			v := p.value
			c.Prev, c.PrevLevel = &v, levelName(p.dims)
		}
		dash.Metrics = append(dash.Metrics, c)
	}
	if restore := dash.Metrics[len(dash.Metrics)-1]; restore.Key == MetricRestore && restore.Value == nil {
		win := time.Duration(st.FailureWindowHours) * time.Hour
		if data, err := loadDora(ctx, tx, last.Add(-90*24*time.Hour), bucketEnd(last, period).Add(win), ""); err == nil {
			data.analyze(st)
			if ev := data.bySubject(tenant)[Subject{scope, subject}]; ev != nil {
				for _, in := range ev.incidents {
					if in.ResolvedAt == nil {
						dash.OpenIncidents++
					}
				}
			}
		}
	}
	dash.Level = levelName(series[last][metricLevel].dims)
	dash.PrevLevel = levelName(series[prev][metricLevel].dims)

	scopeSel := scope
	if compareScope != "" {
		scopeSel = compareScope
	}
	if scopeSel != ScopeTenant {
		if dash.Subjects, err = subjectsOf(ctx, tx, scopeSel, period, from); err != nil {
			return nil, err
		}
	}
	if compareScope != "" {
		if dash.Compare, err = s.compare(ctx, tx, compareScope, period, last, st); err != nil {
			return nil, err
		}
	}
	return dash, nil
}

func (s *Service) compare(ctx context.Context, tx pgx.Tx, scope, period string, at time.Time, st DoraSettings) ([]CompareRow, error) {
	subs, err := subjectsOf(ctx, tx, scope, period, at)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, x := range subs {
		names[x.ID] = x.Name
	}
	rows, err := tx.Query(ctx, `select subject_id::text, metric_key, value, dims from metric_points
		where scope = $1 and period = $2 and at = $3 and metric_key like 'dora.%'`, scope, period, at)
	if err != nil {
		return nil, err
	}
	byID := map[string]*CompareRow{}
	var id, key string
	var val float64
	var raw []byte
	if _, err := pgx.ForEachRow(rows, []any{&id, &key, &val, &raw}, func() error {
		if names[id] == "" {
			return nil
		}
		r := byID[id]
		if r == nil {
			r = &CompareRow{ID: id, Name: names[id], Values: map[string]*float64{}, Levels: map[string]string{}}
			byID[id] = r
		}
		var dims map[string]any
		_ = json.Unmarshal(raw, &dims)
		key = strings.TrimPrefix(key, pointPrefix)
		if key == metricLevel {
			r.Level = levelName(dims)
			return nil
		}
		v := val
		r.Values[key], r.Levels[key] = &v, levelName(dims)
		if n, ok := dims["deploys"].(float64); ok {
			r.Deploys = int(n)
		}
		if n, ok := dims["attempts"].(float64); ok {
			r.Attempts = int(n)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	out := make([]CompareRow, 0, len(byID))
	for _, r := range byID {
		out = append(out, *r)
	}
	sortCompare(out)
	return out, nil
}

func sortCompare(rows []CompareRow) {
	rank := func(l string) int {
		for i, n := range Levels {
			if n == l {
				return i
			}
		}
		return -1
	}
	// Livello migliore prima, poi per numero di deploy e nome.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if rank(a.Level) < rank(b.Level) || (rank(a.Level) == rank(b.Level) && (a.Deploys < b.Deploys || (a.Deploys == b.Deploys && a.Name > b.Name))) {
				rows[j-1], rows[j] = rows[j], rows[j-1]
			} else {
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Approfondimento: i rilasci e gli incidenti che compongono un numero

// Drill è l'elenco che sta dietro ai numeri di un soggetto in un periodo.
type Drill struct {
	Subject   SubjectRef        `json:"subject"`
	Period    string            `json:"period"`
	Start     time.Time         `json:"start"`
	End       time.Time         `json:"end"`
	Values    Values            `json:"values"`
	Levels    map[string]string `json:"levels"`
	Deploys   []*DeployRow      `json:"deploys"`
	Incidents []*IncidentRow    `json:"incidents"`
}

// Drilldown ricalcola dal dato grezzo un periodo di un soggetto e ne elenca i deploy (con il
// motivo del fallimento e l'inizio del lead time) e gli incidenti.
func (s *Service) Drilldown(ctx context.Context, tx pgx.Tx, actor engine.Actor, period, scope, ref string, at time.Time) (*Drill, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	if !validPeriod(period) || !validScope(scope) {
		return nil, engine.InvalidCode("validation.devsecops.dora_period_invalid", "period", "periodo o ambito non validi")
	}
	tenant, err := s.tenantID(ctx, tx)
	if err != nil {
		return nil, err
	}
	subject, err := resolveSubject(ctx, tx, scope, ref, tenant)
	if err != nil {
		return nil, err
	}
	name, err := subjectName(ctx, tx, scope, subject)
	if err != nil {
		return nil, err
	}
	st, err := loadDoraSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	start := bucketStart(at, period)
	end := bucketEnd(start, period)
	win := time.Duration(st.FailureWindowHours) * time.Hour
	data, err := loadDora(ctx, tx, start.Add(-90*24*time.Hour), end.Add(win), "")
	if err != nil {
		return nil, err
	}
	data.analyze(st)
	ev := data.bySubject(tenant)[Subject{scope, subject}]
	if ev == nil {
		ev = &events{}
	}
	d := &Drill{Subject: SubjectRef{scope, subject, name}, Period: period, Start: start, End: end, Deploys: []*DeployRow{}, Incidents: []*IncidentRow{}}
	d.Values = calcValues(ev.deploys, ev.incidents, start, end, s.now())
	lv, _ := d.Values.Levels(st.Thresholds)
	d.Levels = map[string]string{}
	for k, l := range lv {
		d.Levels[k] = Levels[l]
	}
	for _, y := range ev.deploys {
		if !y.At.Before(start) && y.At.Before(end) {
			d.Deploys = append(d.Deploys, y)
		}
	}
	for _, in := range ev.incidents {
		if !in.DetectedAt.Before(start) && in.DetectedAt.Before(end) {
			d.Incidents = append(d.Incidents, in)
		}
	}
	// Più recenti prima.
	for i, j := 0, len(d.Deploys)-1; i < j; i, j = i+1, j-1 {
		d.Deploys[i], d.Deploys[j] = d.Deploys[j], d.Deploys[i]
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Anteprima delle impostazioni

// PreviewRow confronta i livelli di un soggetto con le impostazioni attuali e con quelle proposte.
type PreviewRow struct {
	Scope    string              `json:"scope"`
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Current  map[string]*float64 `json:"current"`
	Proposed map[string]*float64 `json:"proposed"`
	CurLevel map[string]string   `json:"current_levels"`
	NewLevel map[string]string   `json:"proposed_levels"`
	CurAll   string              `json:"current_level"`
	NewAll   string              `json:"proposed_level"`
	Changed  bool                `json:"changed"`
}

// Preview mostra, sull'ultimo periodo concluso, che cosa cambierebbe nelle soglie e nei criteri
// di fallimento proposti per il tenant, i team e le applicazioni. Non salva nulla.
func (s *Service) Preview(ctx context.Context, tx pgx.Tx, actor engine.Actor, period string, proposed DoraSettings) ([]PreviewRow, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if !validPeriod(period) {
		return nil, engine.InvalidValue("period", "periodo non valido")
	}
	proposed, err := proposed.normalize()
	if err != nil {
		return nil, err
	}
	cur, err := loadDoraSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	tenant, err := s.tenantID(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	end := bucketStart(now, period)
	start := prevBucket(end, period)
	maxWin := max(cur.FailureWindowHours, proposed.FailureWindowHours)
	data, err := loadDora(ctx, tx, start.Add(-90*24*time.Hour), end.Add(time.Duration(maxWin)*time.Hour), "")
	if err != nil {
		return nil, err
	}
	run := func(st DoraSettings) map[Subject]Values {
		data.analyze(st)
		out := map[Subject]Values{}
		for sub, ev := range data.bySubject(tenant) {
			if sub.Scope == ScopeService || sub.Scope == ScopeAsset {
				continue
			}
			out[sub] = calcValues(ev.deploys, ev.incidents, start, end, now)
		}
		return out
	}
	a := run(cur)
	b := run(proposed)
	rows := []PreviewRow{}
	for sub, va := range a {
		vb := b[sub]
		if va.Attempts == 0 && va.MTTRN == 0 && vb.Attempts == 0 && vb.MTTRN == 0 {
			continue
		}
		r := PreviewRow{Scope: sub.Scope, ID: sub.ID, Current: map[string]*float64{}, Proposed: map[string]*float64{},
			CurLevel: map[string]string{}, NewLevel: map[string]string{}}
		la, oa := va.Levels(cur.Thresholds)
		lb, ob := vb.Levels(proposed.Thresholds)
		for _, m := range Metrics {
			r.Current[m.Key], r.Proposed[m.Key] = va.Get(m.Key), vb.Get(m.Key)
			if l, ok := la[m.Key]; ok {
				r.CurLevel[m.Key] = Levels[l]
			}
			if l, ok := lb[m.Key]; ok {
				r.NewLevel[m.Key] = Levels[l]
			}
		}
		if oa >= 0 {
			r.CurAll = Levels[oa]
		}
		if ob >= 0 {
			r.NewAll = Levels[ob]
		}
		r.Changed = r.CurAll != r.NewAll
		if r.Name, err = subjectName(ctx, tx, sub.Scope, sub.ID); err != nil {
			continue
		}
		rows = append(rows, r)
	}
	// Prima il tenant, poi i team, poi le applicazioni; i cambiati davanti.
	order := map[string]int{ScopeTenant: 0, ScopeTeam: 1, ScopeApplication: 2}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			x, y := rows[j-1], rows[j]
			if order[x.Scope] > order[y.Scope] || (x.Scope == y.Scope && ((!x.Changed && y.Changed) || (x.Changed == y.Changed && x.Name > y.Name))) {
				rows[j-1], rows[j] = rows[j], rows[j-1]
			} else {
				break
			}
		}
	}
	if len(rows) > 80 {
		rows = rows[:80]
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// Fatti

// doraFacts: i quattro numeri dell'applicazione sugli ultimi fact_days giorni.
func (s *Service) doraFacts(ctx context.Context, tx pgx.Tx, appID string, f engine.Facts) error {
	st, err := loadDoraSettings(ctx, tx)
	if err != nil {
		return err
	}
	now := s.now()
	from := now.AddDate(0, 0, -st.FactDays)
	data, err := loadDora(ctx, tx, from.Add(-90*24*time.Hour), now.Add(time.Minute), appID)
	if err != nil {
		return err
	}
	data.analyze(st)
	v := calcValues(data.deploys, data.incidents, from, now.Add(time.Minute), now.Add(time.Minute))
	if v.Attempts == 0 && v.MTTRN == 0 {
		return nil
	}
	f.Set("frequenza_rilascio", v.Freq)
	if v.Lead != nil {
		f.Set("lead_time_giorni", *v.Lead)
	}
	if v.CFR != nil {
		f.Set("change_failure_rate", *v.CFR)
	}
	if v.MTTR != nil {
		f.Set("mttr_ore", *v.MTTR)
	}
	return nil
}

func (s *Service) registerDoraFacts(eng *engine.Service, appType string) {
	eng.DescribeFacts(appType,
		engine.FactInfo{Key: "frequenza_rilascio", Name: "Rilasci in produzione a settimana (ultimi giorni configurati)", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "lead_time_giorni", Name: "Lead time delle modifiche, mediana in giorni", Kind: "number", Example: "3"},
		engine.FactInfo{Key: "change_failure_rate", Name: "Tasso di fallimento dei rilasci in produzione (%)", Kind: "number", Example: "12"},
		engine.FactInfo{Key: "mttr_ore", Name: "Tempo di ripristino degli incidenti, mediana in ore", Kind: "number", Example: "4"},
	)
}
