package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Lettura dall'API REST di Jira (versione 2, documentata pubblicamente):
//   - Cloud: GET /rest/api/2/search/jql con nextPageToken, autenticazione Basic con email e
//     token dell'account;
//   - Data Center / Server: GET /rest/api/2/search con startAt, token personale (Bearer).
//
// Per ogni issue: campi di sistema, campi personalizzati con i nomi (expand=names),
// commenti e ore (pagine successive quando sono troncati), collegamenti, allegati e sprint
// con le date (campo Sprint del software).

type jiraUser struct {
	AccountID    string `json:"accountId"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

func (u *jiraUser) ref() *UserRef {
	if u == nil {
		return nil
	}
	r := u.AccountID
	if r == "" {
		r = u.Name
	}
	if r == "" {
		r = u.Key
	}
	if r == "" {
		r = u.DisplayName
	}
	if r == "" {
		return nil
	}
	name := u.DisplayName
	if name == "" {
		name = r
	}
	return &UserRef{Ref: r, Name: name, Email: strings.ToLower(u.EmailAddress)}
}

type jiraIssue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type jiraSearch struct {
	Issues        []jiraIssue       `json:"issues"`
	Names         map[string]string `json:"names"`
	Total         int               `json:"total"`
	NextPageToken string            `json:"nextPageToken"`
	IsLast        *bool             `json:"isLast"`
}

type jiraComment struct {
	ID      string    `json:"id"`
	Author  *jiraUser `json:"author"`
	Body    any       `json:"body"`
	Created string    `json:"created"`
}

type jiraWorklog struct {
	ID               string    `json:"id"`
	Author           *jiraUser `json:"author"`
	Started          string    `json:"started"`
	TimeSpentSeconds float64   `json:"timeSpentSeconds"`
	Comment          any       `json:"comment"`
}

// Campi personalizzati con un ruolo proprio o da non importare.
var jiraSpecial = map[string]string{"sprint": "sprint", "epic link": "epic", "rank": "", "epic name": "", "epic status": "",
	"epic colour": "", "epic color": "", "development": "", "team": "custom", "parent link": "epic", "start date": "start",
	"story points": "custom", "story point estimate": "custom", "flagged": "custom", "issue color": ""}

func fetchJira(ctx context.Context, a *api, progress func(int)) (*Dataset, error) {
	jql := strings.TrimSpace(a.c.Query)
	if a.c.Project != "" {
		p := `project = "` + strings.ReplaceAll(a.c.Project, `"`, ``) + `"`
		if jql != "" {
			jql = p + " AND (" + jql + ")"
		} else {
			jql = p
		}
	}
	jql += " ORDER BY key ASC"
	ds := &Dataset{System: "jira", Format: "api", Instance: a.base.Host, Users: map[string]UserRef{}}
	sprints := map[string]SprintInfo{}
	limit := a.c.Limit
	if limit <= 0 || limit > maxAPIIssues {
		limit = maxAPIIssues
	}
	token, startAt := "", 0
	for len(ds.Issues) < limit {
		q := url.Values{"jql": {jql}, "fields": {"*all"}, "expand": {"names"}, "maxResults": {"100"}}
		var page jiraSearch
		var err error
		if a.c.Deployment == "server" {
			q.Set("startAt", strconv.Itoa(startAt))
			err = a.get(ctx, "/rest/api/2/search", q, &page)
		} else {
			if token != "" {
				q.Set("nextPageToken", token)
			}
			err = a.get(ctx, "/rest/api/2/search/jql", q, &page)
		}
		if err != nil {
			return nil, err
		}
		for _, ji := range page.Issues {
			is, err := jiraToIssue(ctx, a, ji, page.Names, sprints, ds.Users)
			if err != nil {
				return nil, err
			}
			ds.Issues = append(ds.Issues, is)
			if len(ds.Issues) >= limit {
				break
			}
		}
		progress(len(ds.Issues))
		if len(page.Issues) == 0 {
			break
		}
		if a.c.Deployment == "server" {
			startAt += len(page.Issues)
			if startAt >= page.Total {
				break
			}
		} else {
			if page.NextPageToken == "" || (page.IsLast != nil && *page.IsLast) {
				break
			}
			token = page.NextPageToken
		}
	}
	for _, sp := range sprints {
		ds.Sprints = append(ds.Sprints, sp)
	}
	return ds, nil
}

func jiraToIssue(ctx context.Context, a *api, ji jiraIssue, names map[string]string, sprints map[string]SprintInfo, users map[string]UserRef) (Issue, error) {
	f := ji.Fields
	is := Issue{Key: ji.Key, ID: ji.ID, Custom: map[string][]string{}}
	str := func(k string) string {
		var s string
		if raw, ok := f[k]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		return s
	}
	named := func(k string) string {
		var v struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		}
		if raw, ok := f[k]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		if v.Name == "" {
			return v.Key
		}
		return v.Name
	}
	user := func(k string) *UserRef {
		var u *jiraUser
		if raw, ok := f[k]; ok {
			_ = json.Unmarshal(raw, &u)
		}
		r := u.ref()
		if r != nil {
			users[r.Ref] = *r
		}
		return r
	}
	is.Summary = str("summary")
	if raw, ok := f["description"]; ok {
		is.Description = textOf(raw)
	}
	is.Type = named("issuetype")
	is.Priority = named("priority")
	is.Resolution = named("resolution")
	is.Project = named("project")
	var proj struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(f["project"], &proj)
	if proj.Key != "" {
		is.Project = proj.Key
	}
	var status struct {
		Name     string `json:"name"`
		Category struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"statusCategory"`
	}
	_ = json.Unmarshal(f["status"], &status)
	is.Status = status.Name
	is.StatusCat = map[string]string{"new": "To Do", "indeterminate": "In Progress", "done": "Done"}[status.Category.Key]
	is.Assignee = user("assignee")
	is.Reporter = user("reporter")
	is.Created, is.Updated, is.Resolved, is.Due = str("created"), str("updated"), str("resolutiondate"), str("duedate")
	_ = json.Unmarshal(f["labels"], &is.Labels)
	var parent struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(f["parent"], &parent)
	is.Parent = parent.Key
	var est float64
	if json.Unmarshal(f["timeoriginalestimate"], &est) == nil && est > 0 {
		m := est / 60
		is.Estimate = &m
	}
	for _, sys := range []struct{ key, name string }{{"components", "Component/s"}, {"fixVersions", "Fix Version/s"}, {"versions", "Affects Version/s"}} {
		var list []struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(f[sys.key], &list)
		for _, x := range list {
			is.Custom[sys.name] = appendUnique(is.Custom[sys.name], x.Name)
		}
	}
	// Collegamenti.
	var links []struct {
		Type struct {
			Name string `json:"name"`
		} `json:"type"`
		Outward *struct {
			Key string `json:"key"`
		} `json:"outwardIssue"`
		Inward *struct {
			Key string `json:"key"`
		} `json:"inwardIssue"`
	}
	_ = json.Unmarshal(f["issuelinks"], &links)
	for _, l := range links {
		if l.Outward != nil {
			is.Links = append(is.Links, Link{Type: l.Type.Name, Out: true, Target: l.Outward.Key})
		}
		if l.Inward != nil {
			is.Links = append(is.Links, Link{Type: l.Type.Name, Out: false, Target: l.Inward.Key})
		}
	}
	// Allegati.
	var atts []struct {
		ID       string    `json:"id"`
		Filename string    `json:"filename"`
		Content  string    `json:"content"`
		Created  string    `json:"created"`
		Size     int64     `json:"size"`
		Author   *jiraUser `json:"author"`
	}
	_ = json.Unmarshal(f["attachment"], &atts)
	for _, x := range atts {
		is.Attachments = append(is.Attachments, Attachment{ID: x.ID, Name: x.Filename, URL: x.Content, Created: x.Created, Size: x.Size, Author: x.Author.ref()})
	}
	// Commenti e ore: l'elenco nella ricerca può essere troncato.
	var cm struct {
		Comments []jiraComment `json:"comments"`
		Total    int           `json:"total"`
	}
	_ = json.Unmarshal(f["comment"], &cm)
	if cm.Total > len(cm.Comments) {
		cm.Comments = nil
		for start := 0; ; {
			var page struct {
				Comments []jiraComment `json:"comments"`
				Total    int           `json:"total"`
			}
			if err := a.get(ctx, "/rest/api/2/issue/"+url.PathEscape(ji.Key)+"/comment", url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"100"}}, &page); err != nil {
				return is, err
			}
			cm.Comments = append(cm.Comments, page.Comments...)
			start += len(page.Comments)
			if len(page.Comments) == 0 || start >= page.Total {
				break
			}
		}
	}
	for _, c := range cm.Comments {
		is.Comments = append(is.Comments, Comment{ID: c.ID, Author: c.Author.ref(), Created: c.Created, Body: anyText(c.Body)})
	}
	var wl struct {
		Worklogs []jiraWorklog `json:"worklogs"`
		Total    int           `json:"total"`
	}
	_ = json.Unmarshal(f["worklog"], &wl)
	if wl.Total > len(wl.Worklogs) {
		wl.Worklogs = nil
		for start := 0; ; {
			var page struct {
				Worklogs []jiraWorklog `json:"worklogs"`
				Total    int           `json:"total"`
			}
			if err := a.get(ctx, "/rest/api/2/issue/"+url.PathEscape(ji.Key)+"/worklog", url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"1000"}}, &page); err != nil {
				return is, err
			}
			wl.Worklogs = append(wl.Worklogs, page.Worklogs...)
			start += len(page.Worklogs)
			if len(page.Worklogs) == 0 || start >= page.Total {
				break
			}
		}
	}
	for _, w := range wl.Worklogs {
		is.Worklogs = append(is.Worklogs, Worklog{ID: w.ID, Author: w.Author.ref(), Started: w.Started, Minutes: w.TimeSpentSeconds / 60, Comment: anyText(w.Comment)})
	}
	// Campi personalizzati.
	for k, raw := range f {
		if !strings.HasPrefix(k, "customfield_") || string(raw) == "null" {
			continue
		}
		name := names[k]
		if name == "" {
			name = k
		}
		role, special := jiraSpecial[strings.ToLower(name)]
		switch {
		case special && role == "sprint":
			for _, sp := range jiraSprints(raw) {
				is.Sprints = appendUnique(is.Sprints, sp.Name)
				if _, ok := sprints[sp.Name]; !ok {
					sprints[sp.Name] = sp
				}
			}
			continue
		case special && role == "epic":
			if is.Parent == "" {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					is.Parent = s
				} else {
					var o struct {
						Key string `json:"key"`
					}
					_ = json.Unmarshal(raw, &o)
					is.Parent = o.Key
				}
			}
			continue
		case special && role == "start":
			var s string
			_ = json.Unmarshal(raw, &s)
			is.Start = s
			continue
		case special && role == "":
			continue
		}
		if vals := customValues(raw); len(vals) > 0 {
			is.Custom[name] = appendUnique(is.Custom[name], vals...)
		}
	}
	if len(is.Custom) == 0 {
		is.Custom = nil
	}
	return is, nil
}

// textOf: descrizione come testo (v2) o documento ADF (v3).
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return adfText(v)
	}
	return ""
}

func anyText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		return adfText(x)
	}
	return ""
}

// customValues: i valori di un campo personalizzato come testo (opzioni, persone, numeri…).
func customValues(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var add func(x any)
	add = func(x any) {
		switch t := x.(type) {
		case nil:
		case string:
			if t != "" {
				out = append(out, t)
			}
		case float64:
			out = append(out, strconv.FormatFloat(t, 'f', -1, 64))
		case bool:
			out = append(out, strconv.FormatBool(t))
		case []any:
			for _, e := range t {
				add(e)
			}
		case map[string]any:
			for _, k := range []string{"value", "name", "displayName", "key"} {
				if s, ok := t[k].(string); ok && s != "" {
					out = append(out, s)
					if c, ok := t["child"].(map[string]any); ok {
						if cs, ok := c["value"].(string); ok {
							out[len(out)-1] += " / " + cs
						}
					}
					return
				}
			}
			if t["type"] == "doc" {
				if s := adfText(t); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	add(v)
	return out
}

var sprintLegacyRe = regexp.MustCompile(`(\w+)=([^,\]]*)`)

// jiraSprints legge il campo Sprint: oggetti (Cloud, DC recenti) o testi «…Sprint@…[id=1,name=…]».
func jiraSprints(raw json.RawMessage) []SprintInfo {
	var objs []struct {
		Name      string `json:"name"`
		State     string `json:"state"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
		Goal      string `json:"goal"`
	}
	var out []SprintInfo
	if json.Unmarshal(raw, &objs) == nil {
		for _, o := range objs {
			if o.Name != "" {
				out = append(out, SprintInfo{Name: o.Name, State: strings.ToLower(o.State), Start: o.StartDate, End: o.EndDate, Goal: o.Goal})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var strs []string
	if json.Unmarshal(raw, &strs) == nil {
		for _, s := range strs {
			sp := SprintInfo{}
			for _, m := range sprintLegacyRe.FindAllStringSubmatch(s, -1) {
				v := m[2]
				if v == "<null>" {
					v = ""
				}
				switch m[1] {
				case "name":
					sp.Name = v
				case "state":
					sp.State = strings.ToLower(v)
				case "startDate":
					sp.Start = v
				case "endDate":
					sp.End = v
				case "goal":
					sp.Goal = v
				}
			}
			if sp.Name != "" {
				out = append(out, sp)
			}
		}
	}
	return out
}

var _ = fmt.Sprintf
