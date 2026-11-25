package importer

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lettura dall'API REST di YouTrack (documentata pubblicamente), con un token permanente:
//   - GET /api/issues?query=project: X con $top e $skip, campi scelti con «fields»;
//   - GET /api/workItems per le ore del progetto;
//   - GET /api/agiles per gli sprint con le date e gli elementi pianificati.
// Il tipo «Subtask» dei collegamenti diventa la gerarchia (genitore e figli).

const ytIssueFields = "id,idReadable,summary,description,created,updated,resolved,project(shortName)," +
	"reporter(login,fullName,email),tags(name)," +
	"customFields(name,$type,value(name,login,fullName,email,minutes,presentation,text,id))," +
	"links(direction,linkType(name),issues(idReadable))," +
	"comments(id,text,created,author(login,fullName,email))," +
	"attachments(id,name,url,created,size,author(login,fullName,email))"

type ytUser struct {
	Login    string `json:"login"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
}

func (u *ytUser) ref() *UserRef {
	if u == nil || (u.Login == "" && u.FullName == "") {
		return nil
	}
	r := u.Login
	if r == "" {
		r = u.FullName
	}
	name := u.FullName
	if name == "" {
		name = r
	}
	return &UserRef{Ref: r, Name: name, Email: strings.ToLower(u.Email)}
}

type ytIssue struct {
	IDReadable  string `json:"idReadable"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Created     int64  `json:"created"`
	Updated     int64  `json:"updated"`
	Resolved    *int64 `json:"resolved"`
	Project     struct {
		ShortName string `json:"shortName"`
	} `json:"project"`
	Reporter *ytUser `json:"reporter"`
	Tags     []struct {
		Name string `json:"name"`
	} `json:"tags"`
	CustomFields []struct {
		Name  string          `json:"name"`
		Type  string          `json:"$type"`
		Value json.RawMessage `json:"value"`
	} `json:"customFields"`
	Links []struct {
		Direction string `json:"direction"`
		LinkType  struct {
			Name string `json:"name"`
		} `json:"linkType"`
		Issues []struct {
			IDReadable string `json:"idReadable"`
		} `json:"issues"`
	} `json:"links"`
	Comments []struct {
		ID      string  `json:"id"`
		Text    string  `json:"text"`
		Created int64   `json:"created"`
		Author  *ytUser `json:"author"`
	} `json:"comments"`
	Attachments []struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		URL     string  `json:"url"`
		Created int64   `json:"created"`
		Size    int64   `json:"size"`
		Author  *ytUser `json:"author"`
	} `json:"attachments"`
}

func ms(v int64) string {
	if v <= 0 {
		return ""
	}
	return time.UnixMilli(v).UTC().Format(time.RFC3339)
}

func fetchYouTrack(ctx context.Context, a *api, progress func(int)) (*Dataset, error) {
	query := strings.TrimSpace(a.c.Query)
	if a.c.Project != "" {
		query = strings.TrimSpace("project: {" + strings.ReplaceAll(a.c.Project, "}", "") + "} " + query)
	}
	ds := &Dataset{System: "youtrack", Format: "api", Instance: a.base.Host, Users: map[string]UserRef{}}
	limit := a.c.Limit
	if limit <= 0 || limit > maxAPIIssues {
		limit = maxAPIIssues
	}
	index := map[string]int{}
	for skip := 0; len(ds.Issues) < limit; {
		var page []ytIssue
		if err := a.get(ctx, "/api/issues", url.Values{"query": {query + " sort by: created asc"}, "fields": {ytIssueFields},
			"$top": {"100"}, "$skip": {strconv.Itoa(skip)}}, &page); err != nil {
			return nil, err
		}
		for _, yi := range page {
			is := ytToIssue(yi, a, ds.Users)
			index[strings.ToUpper(is.Key)] = len(ds.Issues)
			ds.Issues = append(ds.Issues, is)
			if len(ds.Issues) >= limit {
				break
			}
		}
		progress(len(ds.Issues))
		if len(page) < 100 {
			break
		}
		skip += len(page)
	}
	// Ore registrate del progetto.
	for skip := 0; ; {
		var page []struct {
			ID       string  `json:"id"`
			Date     int64   `json:"date"`
			Text     string  `json:"text"`
			Author   *ytUser `json:"author"`
			Duration struct {
				Minutes float64 `json:"minutes"`
			} `json:"duration"`
			Issue struct {
				IDReadable string `json:"idReadable"`
			} `json:"issue"`
		}
		if err := a.get(ctx, "/api/workItems", url.Values{"query": {query}, "fields": {"id,date,text,author(login,fullName,email),duration(minutes),issue(idReadable)"},
			"$top": {"500"}, "$skip": {strconv.Itoa(skip)}}, &page); err != nil {
			return nil, err
		}
		for _, w := range page {
			i, ok := index[strings.ToUpper(w.Issue.IDReadable)]
			if !ok {
				continue
			}
			u := w.Author.ref()
			if u != nil {
				ds.Users[u.Ref] = *u
			}
			ds.Issues[i].Worklogs = append(ds.Issues[i].Worklogs, Worklog{ID: w.ID, Author: u, Started: ms(w.Date), Minutes: w.Duration.Minutes, Comment: w.Text})
		}
		if len(page) < 500 {
			break
		}
		skip += len(page)
	}
	// Sprint delle board agili che contengono il progetto.
	var agiles []struct {
		Name     string `json:"name"`
		Projects []struct {
			ShortName string `json:"shortName"`
		} `json:"projects"`
		Sprints []struct {
			Name     string `json:"name"`
			Start    int64  `json:"start"`
			Finish   int64  `json:"finish"`
			Archived bool   `json:"archived"`
			Goal     string `json:"goal"`
			Issues   []struct {
				IDReadable string `json:"idReadable"`
			} `json:"issues"`
		} `json:"sprints"`
	}
	if err := a.get(ctx, "/api/agiles", url.Values{"fields": {"name,projects(shortName),sprints(name,start,finish,archived,goal,issues(idReadable))"}, "$top": {"200"}}, &agiles); err == nil {
		now := time.Now().UnixMilli()
		for _, ag := range agiles {
			mine := a.c.Project == ""
			for _, p := range ag.Projects {
				mine = mine || strings.EqualFold(p.ShortName, a.c.Project)
			}
			if !mine {
				continue
			}
			for _, sp := range ag.Sprints {
				used := false
				for _, x := range sp.Issues {
					if i, ok := index[strings.ToUpper(x.IDReadable)]; ok {
						ds.Issues[i].Sprints = appendUnique(ds.Issues[i].Sprints, sp.Name)
						used = true
					}
				}
				if !used {
					continue
				}
				state := "future"
				switch {
				case sp.Archived || (sp.Finish > 0 && sp.Finish < now):
					state = "closed"
				case sp.Start > 0 && sp.Start <= now:
					state = "active"
				}
				ds.Sprints = append(ds.Sprints, SprintInfo{Name: sp.Name, State: state, Start: ms(sp.Start), End: ms(sp.Finish), Goal: sp.Goal})
			}
		}
	}
	return ds, nil
}

func ytToIssue(yi ytIssue, a *api, users map[string]UserRef) Issue {
	is := Issue{Key: yi.IDReadable, Summary: yi.Summary, Description: yi.Description, Project: yi.Project.ShortName,
		Created: ms(yi.Created), Updated: ms(yi.Updated), Custom: map[string][]string{}}
	if yi.Resolved != nil {
		is.Resolved = ms(*yi.Resolved)
	}
	addUser := func(u *ytUser) *UserRef {
		r := u.ref()
		if r != nil {
			users[r.Ref] = *r
		}
		return r
	}
	is.Reporter = addUser(yi.Reporter)
	for _, t := range yi.Tags {
		is.Labels = appendUnique(is.Labels, t.Name)
	}
	for _, cf := range yi.CustomFields {
		if string(cf.Value) == "null" || len(cf.Value) == 0 {
			continue
		}
		switch strings.ToLower(cf.Name) {
		case "type":
			is.Type = ytName(cf.Value)
			continue
		case "state":
			is.Status = ytName(cf.Value)
			continue
		case "priority":
			is.Priority = ytName(cf.Value)
			continue
		case "assignee":
			var u ytUser
			if json.Unmarshal(cf.Value, &u) == nil {
				is.Assignee = addUser(&u)
			}
			continue
		case "estimation", "stima", "original estimation":
			var p struct {
				Minutes float64 `json:"minutes"`
			}
			if json.Unmarshal(cf.Value, &p) == nil && p.Minutes > 0 {
				m := p.Minutes
				is.Estimate = &m
			}
			continue
		case "spent time":
			continue
		case "due date", "scadenza":
			var n int64
			if json.Unmarshal(cf.Value, &n) == nil {
				is.Due = ms(n)
			}
			continue
		case "start date":
			var n int64
			if json.Unmarshal(cf.Value, &n) == nil {
				is.Start = ms(n)
			}
			continue
		}
		var vals []string
		if strings.Contains(cf.Type, "Date") {
			var n int64
			if json.Unmarshal(cf.Value, &n) == nil {
				vals = []string{ms(n)}
			}
		} else {
			vals = ytValues(cf.Value)
		}
		if len(vals) > 0 {
			is.Custom[cf.Name] = appendUnique(is.Custom[cf.Name], vals...)
		}
	}
	for _, l := range yi.Links {
		for _, t := range l.Issues {
			switch {
			case strings.EqualFold(l.LinkType.Name, "Subtask") && l.Direction == "INWARD":
				is.Parent = t.IDReadable // «subtask of»
			case strings.EqualFold(l.LinkType.Name, "Subtask"):
				// «parent for»: il figlio dice già chi è il genitore.
			default:
				is.Links = append(is.Links, Link{Type: l.LinkType.Name, Out: l.Direction != "INWARD", Target: t.IDReadable})
			}
		}
	}
	for _, c := range yi.Comments {
		is.Comments = append(is.Comments, Comment{ID: c.ID, Author: addUser(c.Author), Created: ms(c.Created), Body: c.Text})
	}
	for _, x := range yi.Attachments {
		u := x.URL
		if ref, err := url.Parse(u); err == nil && !ref.IsAbs() {
			u = a.base.ResolveReference(ref).String()
		}
		is.Attachments = append(is.Attachments, Attachment{ID: x.ID, Name: x.Name, URL: u, Created: ms(x.Created), Size: x.Size, Author: addUser(x.Author)})
	}
	if len(is.Custom) == 0 {
		is.Custom = nil
	}
	return is
}

func ytName(raw json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Name
}

func ytValues(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var add func(x any)
	add = func(x any) {
		switch t := x.(type) {
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
			for _, k := range []string{"name", "fullName", "login", "presentation", "text"} {
				if s, ok := t[k].(string); ok && s != "" {
					out = append(out, s)
					return
				}
			}
			if m, ok := t["minutes"].(float64); ok {
				out = append(out, strconv.FormatFloat(m, 'f', -1, 64))
			}
		}
	}
	add(v)
	return out
}
