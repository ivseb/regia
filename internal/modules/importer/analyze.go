package importer

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Analisi: cosa c'è nell'origine (tipi, stati, persone, campi, sprint) con i conteggi.
// È la base della mappatura e si ricalcola quando cambiano i ruoli delle colonne.

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type StateInfo struct {
	Name     string   `json:"name"`
	Count    int      `json:"count"`
	Category string   `json:"category,omitempty"` // dalla categoria dello stato di Jira, se c'è
	Types    []string `json:"types"`              // tipi d'origine in cui compare
}

type UserInfo struct {
	Ref   string `json:"ref"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Count int    `json:"count"` // elementi, commenti e ore in cui compare
}

type FieldInfo struct {
	Name     string   `json:"name"`
	Count    int      `json:"count"`    // elementi con un valore
	Distinct int      `json:"distinct"` // valori diversi
	Samples  []string `json:"samples"`
	DataType string   `json:"data_type"` // dedotto dai valori
	Multi    bool     `json:"multi"`
}

type SprintRow struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Open  int    `json:"open"` // elementi non risolti
	State string `json:"state,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type Analysis struct {
	System      string      `json:"system"`
	Format      string      `json:"format"`
	Instance    string      `json:"instance,omitempty"`
	Issues      int         `json:"issues"`
	Projects    []NameCount `json:"projects"`
	Types       []NameCount `json:"types"`
	States      []StateInfo `json:"states"`
	Priorities  []NameCount `json:"priorities"`
	Labels      []NameCount `json:"labels"`
	LabelsTotal int         `json:"labels_total"`
	Users       []UserInfo  `json:"users"`
	Fields      []FieldInfo `json:"fields"`
	Sprints     []SprintRow `json:"sprints"`
	LinkTypes   []NameCount `json:"link_types"`
	Comments    int         `json:"comments"`
	Worklogs    int         `json:"worklogs"`
	Attachments int         `json:"attachments"`
	Links       int         `json:"links"`
	WithParent  int         `json:"with_parent"`
	DateFormat  string      `json:"date_format"`
	// Header e Columns: intestazione del CSV e ruoli proposti (o scelti) delle colonne.
	Header   []string `json:"header,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	Warnings []string `json:"warnings"`
	// KeyPrefix: prefisso comune delle chiavi (PROJ), per conservare la numerazione.
	KeyPrefix string `json:"key_prefix,omitempty"`
}

// issuesOf restituisce le issue dell'origine: dal CSV con i ruoli delle colonne (quelli
// della mappatura o, se mancano, quelli proposti per lo strumento), oppure dall'API.
func issuesOf(ds *Dataset, columns []string) ([]Issue, []string, []string) {
	if ds.Format != "csv" {
		return ds.Issues, nil, nil
	}
	if len(columns) != len(ds.Header) {
		columns = DetectRoles(ds.System, ds.Header)
	}
	issues, warnings := interpretCSV(ds, columns)
	return issues, columns, warnings
}

func analyze(ds *Dataset, issues []Issue, columns []string, warnings []string) *Analysis {
	a := &Analysis{System: ds.System, Format: ds.Format, Instance: ds.Instance, Issues: len(issues), Header: ds.Header, Columns: columns,
		Warnings: append([]string{}, warnings...)}
	projects, types, prios, labels, links := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	states := map[string]*StateInfo{}
	users := map[string]*UserInfo{}
	fields := map[string]*fieldStats{}
	sprints := map[string]*SprintRow{}
	var dates []string
	keys := map[string]bool{}
	dup := 0
	addUser := func(u *UserRef) {
		if u == nil || u.Ref == "" {
			return
		}
		x := users[u.Ref]
		if x == nil {
			x = &UserInfo{Ref: u.Ref, Name: u.Name, Email: u.Email}
			if ds.Users != nil {
				if k, ok := ds.Users[u.Ref]; ok {
					if x.Name == "" || x.Name == x.Ref {
						x.Name = k.Name
					}
					if x.Email == "" {
						x.Email = k.Email
					}
				}
			}
			users[u.Ref] = x
		}
		if (x.Name == "" || x.Name == x.Ref) && u.Name != "" {
			x.Name = u.Name
		}
		if x.Email == "" && u.Email != "" {
			x.Email = u.Email
		}
		x.Count++
	}
	for _, is := range issues {
		if keys[strings.ToUpper(is.Key)] {
			dup++
		}
		keys[strings.ToUpper(is.Key)] = true
		if is.Project != "" {
			projects[is.Project]++
		}
		t := is.Type
		if t == "" {
			t = "(senza tipo)"
		}
		types[t]++
		st := is.Status
		if st == "" {
			st = "(senza stato)"
		}
		s := states[st]
		if s == nil {
			s = &StateInfo{Name: st}
			states[st] = s
		}
		s.Count++
		if s.Category == "" && is.StatusCat != "" {
			s.Category = is.StatusCat
		}
		if !containsFold(s.Types, t) {
			s.Types = append(s.Types, t)
		}
		if is.Priority != "" {
			prios[is.Priority]++
		}
		for _, l := range is.Labels {
			labels[l]++
		}
		addUser(is.Assignee)
		addUser(is.Reporter)
		for _, c := range is.Comments {
			a.Comments++
			addUser(c.Author)
			dates = append(dates, c.Created)
		}
		for _, w := range is.Worklogs {
			a.Worklogs++
			addUser(w.Author)
			dates = append(dates, w.Started)
		}
		a.Attachments += len(is.Attachments)
		for _, l := range is.Links {
			a.Links++
			links[l.Type]++
		}
		if is.Parent != "" {
			a.WithParent++
		}
		for _, sp := range is.Sprints {
			r := sprints[sp]
			if r == nil {
				r = &SprintRow{Name: sp}
				sprints[sp] = r
			}
			r.Count++
			if is.Resolved == "" && !doneLike(is.Status, is.StatusCat) {
				r.Open++
			}
		}
		for name, vals := range is.Custom {
			f := fields[name]
			if f == nil {
				f = &fieldStats{distinct: map[string]bool{}}
				fields[name] = f
			}
			f.count++
			if len(vals) > 1 {
				f.multi = true
			}
			for _, v := range vals {
				f.add(v)
			}
		}
		dates = append(dates, is.Created, is.Updated, is.Resolved, is.Due, is.Start)
	}
	if dup > 0 {
		a.Warnings = append(a.Warnings, strconv.Itoa(dup)+" righe hanno una chiave già vista: si importa l'ultima")
	}
	for _, sp := range ds.Sprints {
		r := sprints[sp.Name]
		if r == nil {
			r = &SprintRow{Name: sp.Name}
			sprints[sp.Name] = r
		}
		r.State, r.Start, r.End = sp.State, sp.Start, sp.End
	}
	var nonEmpty []string
	for _, d := range dates {
		if strings.TrimSpace(d) != "" {
			nonEmpty = append(nonEmpty, d)
		}
	}
	a.DateFormat = detectDateFormat(nonEmpty)
	a.Projects = sortedCounts(projects)
	a.Types = sortedCounts(types)
	a.Priorities = sortedCounts(prios)
	a.LinkTypes = sortedCounts(links)
	a.LabelsTotal = len(labels)
	a.Labels = sortedCounts(labels)
	if len(a.Labels) > 300 {
		a.Labels = a.Labels[:300]
	}
	for _, s := range states {
		a.States = append(a.States, *s)
	}
	sort.Slice(a.States, func(i, j int) bool {
		return a.States[i].Count > a.States[j].Count || (a.States[i].Count == a.States[j].Count && a.States[i].Name < a.States[j].Name)
	})
	for _, u := range users {
		a.Users = append(a.Users, *u)
	}
	sort.Slice(a.Users, func(i, j int) bool { return strings.ToLower(a.Users[i].Name) < strings.ToLower(a.Users[j].Name) })
	for name, f := range fields {
		a.Fields = append(a.Fields, f.info(name))
	}
	sort.Slice(a.Fields, func(i, j int) bool { return a.Fields[i].Name < a.Fields[j].Name })
	for _, r := range sprints {
		a.Sprints = append(a.Sprints, *r)
	}
	sort.Slice(a.Sprints, func(i, j int) bool { return naturalLess(a.Sprints[i].Name, a.Sprints[j].Name) })
	if len(a.Projects) == 1 {
		a.KeyPrefix = strings.ToUpper(a.Projects[0].Name)
		if !spaceKeyRe.MatchString(a.KeyPrefix) {
			a.KeyPrefix = ""
		}
	}
	if a.Users == nil {
		a.Users = []UserInfo{}
	}
	for _, p := range []*[]NameCount{&a.Projects, &a.Types, &a.Priorities, &a.Labels, &a.LinkTypes} {
		if *p == nil {
			*p = []NameCount{}
		}
	}
	if a.States == nil {
		a.States = []StateInfo{}
	}
	if a.Fields == nil {
		a.Fields = []FieldInfo{}
	}
	if a.Sprints == nil {
		a.Sprints = []SprintRow{}
	}
	return a
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func sortedCounts(m map[string]int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for k, v := range m {
		out = append(out, NameCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count || (out[i].Count == out[j].Count && out[i].Name < out[j].Name)
	})
	return out
}

var numPartRe = regexp.MustCompile(`\d+`)

// naturalLess: «Sprint 2» prima di «Sprint 10».
func naturalLess(a, b string) bool {
	pa, pb := numPartRe.ReplaceAllStringFunc(a, pad), numPartRe.ReplaceAllStringFunc(b, pad)
	return strings.ToLower(pa) < strings.ToLower(pb)
}

func pad(s string) string {
	if len(s) >= 12 {
		return s
	}
	return strings.Repeat("0", 12-len(s)) + s
}

// fieldStats raccoglie i valori di un campo per dedurne il tipo.
type fieldStats struct {
	count    int
	multi    bool
	distinct map[string]bool
	samples  []string
	values   int
	nums     int
	ints     int
	dates    int
	times    int
	urls     int
	bools    int
	long     int
	maxLen   int
}

var boolWords = map[string]bool{"sì": true, "si": true, "no": true, "yes": true, "true": true, "false": true, "vero": true, "falso": true}

func (f *fieldStats) add(v string) {
	f.values++
	if !f.distinct[v] {
		if len(f.distinct) < 10_000 {
			f.distinct[v] = true
		}
		if len(f.samples) < 5 {
			f.samples = append(f.samples, short(v))
		}
	}
	if len(v) > f.maxLen {
		f.maxLen = len(v)
	}
	if len(v) > 250 || strings.Contains(v, "\n") {
		f.long++
	}
	t := strings.TrimSpace(v)
	if n, err := strconv.ParseFloat(strings.ReplaceAll(t, ",", "."), 64); err == nil {
		f.nums++
		if n == float64(int64(n)) && !strings.ContainsAny(t, ".,") {
			f.ints++
		}
	}
	if boolWords[strings.ToLower(t)] {
		f.bools++
	}
	if u, err := url.Parse(t); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !strings.Contains(t, " ") {
		f.urls++
	}
	if d, err := parseDate(t, "auto", time.UTC); err == nil {
		f.dates++
		if d.Hour() != 0 || d.Minute() != 0 {
			f.times++
		}
	}
}

func (f *fieldStats) info(name string) FieldInfo {
	fi := FieldInfo{Name: name, Count: f.count, Distinct: len(f.distinct), Samples: f.samples, Multi: f.multi}
	all := func(n int) bool { return f.values > 0 && n == f.values }
	switch {
	case f.long > 0:
		fi.DataType = "text"
	case all(f.ints):
		fi.DataType = "integer"
	case all(f.nums):
		fi.DataType = "decimal"
	case all(f.dates) && f.times > 0:
		fi.DataType = "datetime"
	case all(f.dates):
		fi.DataType = "date"
	case all(f.urls):
		fi.DataType = "url"
	case all(f.bools) && len(f.distinct) <= 2:
		fi.DataType = "boolean"
	case len(f.distinct) <= 50 && (f.multi || f.values >= 2*len(f.distinct)) && f.maxLen <= 100:
		fi.DataType = "enum"
	default:
		fi.DataType = "string"
	}
	if fi.Multi && fi.DataType != "enum" && fi.DataType != "url" {
		fi.DataType = "string"
	}
	if fi.Samples == nil {
		fi.Samples = []string{}
	}
	return fi
}

// doneLike: lo stato sembra chiuso (per contare gli elementi aperti degli sprint).
func doneLike(status, cat string) bool {
	if strings.EqualFold(cat, "done") || strings.EqualFold(cat, "completata") || strings.EqualFold(cat, "fatto") {
		return true
	}
	c := guessCategory(status)
	return c == "done" || c == "cancelled"
}

// guessCategory deduce la categoria da un nome di stato comune (Jira, YouTrack, italiano).
func guessCategory(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "selected for"), strings.HasPrefix(s, "ready for dev"):
		return "todo"
	case containsAny(s, "won't", "wont", "cancel", "annull", "reject", "respint", "duplicate", "duplicat", "obsolete", "invalid", "can't reproduce", "non riproduc", "non si fa", "incomplete"):
		return "cancelled"
	case containsAny(s, "done", "closed", "resolved", "fixed", "verified", "released", "complete", "fatto", "chius", "risolt", "rilasciat", "completat"):
		return "done"
	case containsAny(s, "review", "revision", "revisione", "code review"):
		return "review"
	case containsAny(s, "qa", "test", "verif", "to verify", "da verificare", "collaud"):
		return "verify"
	case containsAny(s, "progress", "doing", "development", "sviluppo", "in corso", "lavorazione", "started", "avviat", "fixing"):
		return "in_progress"
	case containsAny(s, "to do", "todo", "open", "backlog", "new", "nuov", "aperto", "da fare", "selected", "submitted", "reopened", "riapert", "pianific", "planned"):
		return "todo"
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
