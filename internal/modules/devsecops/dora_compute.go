package devsecops

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// analyze applica le impostazioni ai dati letti: inizio del lead time di ogni deploy e deploy
// falliti con il motivo. Si può rifare con impostazioni diverse sugli stessi dati (anteprima).
func (d *doraData) analyze(st DoraSettings) {
	order := map[string][]int{"auto": {0, 1}, "check_run": {0}, "change": {1}, "request": {2}}[st.LeadTimeStart]
	sources := [3]string{"check_run", "change", "request"}
	for _, y := range d.deploys {
		y.LeadStart, y.LeadSource, y.LeadDays, y.Failed, y.Reasons = nil, "", nil, false, []FailReason{}
		for _, i := range order {
			if t := y.cand[i]; t != nil && t.Before(y.At) {
				// Il change nasce con il primo rilascio: se è dello stesso istante non dice nulla.
				if i == 1 && y.At.Sub(*t) < time.Minute {
					continue
				}
				y.LeadStart, y.LeadSource = t, sources[i]
				days := y.At.Sub(*t).Hours() / 24
				y.LeadDays = &days
				break
			}
		}
	}
	window := time.Duration(st.FailureWindowHours) * time.Hour
	fail := func(y *DeployRow, r FailReason) {
		y.Failed = true
		for _, e := range y.Reasons {
			if e.Kind == r.Kind && e.Ref == r.Ref {
				return
			}
		}
		y.Reasons = append(y.Reasons, r)
	}
	bySvc := map[string][]*DeployRow{}
	for _, y := range d.deploys {
		bySvc[y.ServiceID] = append(bySvc[y.ServiceID], y)
	}
	for _, list := range bySvc {
		var prev *DeployRow // ultimo deploy riuscito
		for i, y := range list {
			if y.Status == "failed" || y.Status == "rolled_back" {
				fail(y, FailReason{Kind: "status", Text: map[string]string{"failed": "deploy fallito", "rolled_back": "deploy annullato (rollback)"}[y.Status]})
			}
			if y.Status == "succeeded" {
				for _, n := range list[i+1:] {
					if n.At.Sub(y.At) > window {
						break
					}
					if n.Status != "succeeded" || n.ID == y.ID {
						continue
					}
					if prev != nil && !strings.EqualFold(prev.Version, y.Version) && strings.EqualFold(n.Version, prev.Version) {
						fail(y, FailReason{Kind: "rollback", Text: fmt.Sprintf("si è tornati alla versione %s dopo %s", n.Version, humanGap(n.At.Sub(y.At))), Ref: n.Version})
						continue
					}
					if w := hotfixWord(n, st.HotfixWords); w != "" {
						kind := "hotfix"
						if w == "rollback" || w == "revert" {
							kind = "rollback"
						}
						fail(y, FailReason{Kind: kind, Text: fmt.Sprintf("seguito da %s (%s) dopo %s", n.Version, w, humanGap(n.At.Sub(y.At))), Ref: n.Version})
					}
				}
				prev = y
			}
		}
	}
	// Collegamento a mano e incidenti dell'applicazione entro la finestra.
	keys := map[string]*IncidentRow{}
	for _, in := range d.incidents {
		keys[in.ID] = in
	}
	for _, y := range d.deploys {
		if in := keys[y.linkedIncID]; in != nil {
			fail(y, FailReason{Kind: "linked", Text: "collegato a mano all'incidente " + in.Key, Ref: in.Key})
		} else if y.linkedIncID != "" {
			fail(y, FailReason{Kind: "linked", Text: "collegato a mano a un incidente"})
		}
	}
	for _, in := range d.incidents {
		for _, app := range in.apps {
			var last *DeployRow
			for _, y := range d.deploys {
				if y.At.After(in.DetectedAt) {
					break
				}
				if y.Status == "succeeded" && y.AppID != nil && *y.AppID == app {
					last = y
				}
			}
			if last != nil && in.DetectedAt.Sub(last.At) <= window {
				fail(last, FailReason{Kind: "incident", Text: fmt.Sprintf("incidente %s dopo %s", in.Key, humanGap(in.DetectedAt.Sub(last.At))), Ref: in.Key})
			}
		}
	}
}

func hotfixWord(y *DeployRow, words []string) string {
	text := strings.ToLower(y.Version + " " + y.title + " " + y.pr)
	if y.Change != nil {
		text += " " + strings.ToLower(*y.Change)
	}
	for _, w := range words {
		if strings.Contains(text, w) {
			return w
		}
	}
	return ""
}

func humanGap(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0f h", d.Hours())
	}
	return fmt.Sprintf("%.0f giorni", d.Hours()/24)
}

// ---------------------------------------------------------------------------
// Valori di un periodo

// Values sono i quattro numeri di un soggetto in un periodo.
type Values struct {
	Deploys  int      `json:"deploys"`  // riusciti
	Attempts int      `json:"attempts"` // tutti i tentativi
	Failed   int      `json:"failed"`
	Freq     float64  `json:"frequency"` // a settimana
	Lead     *float64 `json:"lead_time_days"`
	LeadN    int      `json:"lead_time_n"`
	CFR      *float64 `json:"change_failure_rate"`
	MTTR     *float64 `json:"mttr_hours"`
	MTTRN    int      `json:"mttr_n"`
	Partial  bool     `json:"partial"`
}

// Get restituisce il valore di una metrica (nil se non ci sono dati).
func (v Values) Get(key string) *float64 {
	switch key {
	case MetricFrequency:
		f := v.Freq
		return &f
	case MetricLeadTime:
		return v.Lead
	case MetricFailure:
		return v.CFR
	case MetricRestore:
		return v.MTTR
	}
	return nil
}

// Levels classifica le metriche con dati e restituisce anche il livello complessivo (-1 se
// il soggetto non ha dati).
func (v Values) Levels(th map[string]Ladder) (map[string]int, int) {
	out := map[string]int{}
	if v.Attempts == 0 && v.MTTRN == 0 && v.Deploys == 0 {
		return out, -1
	}
	for _, m := range Metrics {
		if x := v.Get(m.Key); x != nil {
			out[m.Key] = LevelOf(m.Key, *x, th[m.Key])
		}
	}
	return out, OverallLevel(out)
}

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// calcValues calcola i quattro numeri su [start, end): i deploy e gli incidenti si
// filtrano per istante qui. now limita l'ultimo periodo, ancora in corso.
func calcValues(deploys []*DeployRow, incs []*IncidentRow, start, end, now time.Time) Values {
	var v Values
	var leads, mttrs []float64
	for _, y := range deploys {
		if y.At.Before(start) || !y.At.Before(end) {
			continue
		}
		v.Attempts++
		if y.Failed {
			v.Failed++
		}
		if y.Status == "succeeded" {
			v.Deploys++
			if y.LeadDays != nil {
				leads = append(leads, *y.LeadDays)
			}
		}
	}
	for _, in := range incs {
		if in.DetectedAt.Before(start) || !in.DetectedAt.Before(end) {
			continue
		}
		if in.Hours != nil {
			mttrs = append(mttrs, *in.Hours)
		}
	}
	span := end
	if now.Before(end) {
		span, v.Partial = now, true
	}
	days := span.Sub(start).Hours() / 24
	if days < 1 {
		days = 1
	}
	v.Freq = round2(float64(v.Deploys) / (days / 7))
	if len(leads) > 0 {
		m := round2(median(leads))
		v.Lead, v.LeadN = &m, len(leads)
	}
	if v.Attempts > 0 {
		c := round2(float64(v.Failed) * 100 / float64(v.Attempts))
		v.CFR = &c
	}
	if len(mttrs) > 0 {
		m := round2(median(mttrs))
		v.MTTR, v.MTTRN = &m, len(mttrs)
	}
	return v
}

func round2(x float64) float64 {
	return float64(int64(x*100+0.5*sign(x))) / 100
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

// ---------------------------------------------------------------------------
// Soggetti

// Subject è un soggetto delle metriche.
type Subject struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
}

func (d *doraData) subjectsOfDeploy(tenant string, y *DeployRow) []Subject {
	out := []Subject{{ScopeTenant, tenant}, {ScopeService, y.ServiceID}}
	if y.AppID != nil {
		out = append(out, Subject{ScopeApplication, *y.AppID})
		if y.app != nil {
			if y.app.team != "" {
				out = append(out, Subject{ScopeTeam, y.app.team})
			}
			for _, a := range y.app.assets {
				out = append(out, Subject{ScopeAsset, a})
			}
		}
	}
	return out
}

func (d *doraData) subjectsOfIncident(tenant string, in *IncidentRow) []Subject {
	out := []Subject{{ScopeTenant, tenant}}
	teams := map[string]bool{}
	for _, a := range in.apps {
		out = append(out, Subject{ScopeApplication, a})
		if info := d.apps[a]; info != nil && info.team != "" && !teams[info.team] {
			teams[info.team] = true
			out = append(out, Subject{ScopeTeam, info.team})
		}
	}
	for _, a := range in.assets {
		out = append(out, Subject{ScopeAsset, a})
	}
	return out
}

type events struct {
	deploys   []*DeployRow
	incidents []*IncidentRow
}

// bySubject raggruppa gli eventi per soggetto.
func (d *doraData) bySubject(tenant string) map[Subject]*events {
	m := map[Subject]*events{}
	get := func(s Subject) *events {
		e := m[s]
		if e == nil {
			e = &events{}
			m[s] = e
		}
		return e
	}
	for _, y := range d.deploys {
		for _, s := range d.subjectsOfDeploy(tenant, y) {
			e := get(s)
			e.deploys = append(e.deploys, y)
		}
	}
	for _, in := range d.incidents {
		for _, s := range d.subjectsOfIncident(tenant, in) {
			e := get(s)
			e.incidents = append(e.incidents, in)
		}
	}
	return m
}

// Point è un valore salvato in metric_points.
type Point struct {
	Subject Subject
	Period  string
	At      time.Time
	Key     string
	Value   float64
	Dims    map[string]any
}

func buckets(period string, now time.Time, full bool) []time.Time {
	var out []time.Time
	last := bucketStart(now, period)
	for s := windowFrom(now, period, full); !s.After(last); s = bucketEnd(s, period) {
		out = append(out, s)
	}
	return out
}

// computePoints calcola i punti di tutti i soggetti per i periodi richiesti.
func (d *doraData) computePoints(tenant string, st DoraSettings, now time.Time, full bool) []Point {
	var out []Point
	for sub, ev := range d.bySubject(tenant) {
		for _, period := range periods {
			bs := buckets(period, now, full)
			from := bs[0]
			any := false
			for _, y := range ev.deploys {
				if !y.At.Before(from) {
					any = true
					break
				}
			}
			for _, in := range ev.incidents {
				if !in.DetectedAt.Before(from) {
					any = true
					break
				}
			}
			if !any {
				continue
			}
			for _, b := range bs {
				v := calcValues(ev.deploys, ev.incidents, b, bucketEnd(b, period), now)
				out = append(out, valuePoints(sub, period, b, v, st.Thresholds)...)
			}
		}
	}
	return out
}

// valuePoints: le righe di metric_points di un soggetto e un periodo.
func valuePoints(sub Subject, period string, at time.Time, v Values, th map[string]Ladder) []Point {
	levels, overall := v.Levels(th)
	mk := func(key string, val float64, dims map[string]any) Point {
		if l, ok := levels[key]; ok {
			dims["level"] = Levels[l]
		}
		if v.Partial {
			dims["partial"] = true
		}
		return Point{Subject: sub, Period: period, At: at, Key: pointPrefix + key, Value: val, Dims: dims}
	}
	pts := []Point{mk(MetricFrequency, v.Freq, map[string]any{"deploys": v.Deploys, "attempts": v.Attempts, "failed": v.Failed})}
	if v.Lead != nil {
		pts = append(pts, mk(MetricLeadTime, *v.Lead, map[string]any{"n": v.LeadN}))
	}
	if v.CFR != nil {
		pts = append(pts, mk(MetricFailure, *v.CFR, map[string]any{"attempts": v.Attempts, "failed": v.Failed}))
	}
	if v.MTTR != nil {
		pts = append(pts, mk(MetricRestore, *v.MTTR, map[string]any{"n": v.MTTRN}))
	}
	if overall >= 0 {
		dims := map[string]any{"level": Levels[overall]}
		if v.Partial {
			dims["partial"] = true
		}
		pts = append(pts, Point{Subject: sub, Period: period, At: at, Key: pointPrefix + metricLevel, Value: float64(overall), Dims: dims})
	}
	return pts
}
