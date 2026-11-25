package importer

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDates(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	cases := []struct {
		in, format string
		want       string
	}{
		{"24/Nov/26 10:30 AM", "jira", "2026-11-24T09:30:00Z"},
		{"24/nov/26 3:05 pm", "auto", "2026-11-24T14:05:00Z"},
		{"24/dic/26 10:30 AM", "jira", "2026-12-24T09:30:00Z"},
		{"2026-11-24 10:30", "auto", "2026-11-24T09:30:00Z"},
		{"2026-11-24T10:30:00.000+0100", "jira", "2026-11-24T09:30:00Z"},
		{"24/11/2026 10:30", "dmy", "2026-11-24T09:30:00Z"},
		{"11/24/2026 10:30 AM", "mdy", "2026-11-24T09:30:00Z"},
		{"1795253400000", "auto", "2026-11-21T09:30:00Z"},
	}
	for _, c := range cases {
		got, err := parseDate(c.in, c.format, rome)
		if err != nil || got.Format(time.RFC3339) != c.want {
			t.Errorf("parseDate(%q, %s) = %v, %v; atteso %s", c.in, c.format, got.Format(time.RFC3339), err, c.want)
		}
	}
	if f := detectDateFormat([]string{"03/Mar/26 9:15 AM", "20/Oct/26 4:40 PM", "30/Nov/26"}); f != "jira" {
		t.Errorf("formato Jira riconosciuto come %s", f)
	}
	if f := detectDateFormat([]string{"05/03/2026", "25/12/2026 10:00"}); f != "dmy" {
		t.Errorf("formato europeo riconosciuto come %s", f)
	}
	if f := detectDateFormat([]string{"12/25/2026 10:00 AM"}); f != "mdy" {
		t.Errorf("formato americano riconosciuto come %s", f)
	}
}

func TestJiraCSV(t *testing.T) {
	f, err := os.Open("../../itest/testdata/import/jira.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header, rows, lines, err := readCSV(f, "")
	if err != nil {
		t.Fatal(err)
	}
	ds := &Dataset{System: "jira", Format: "csv", Header: header, Rows: rows, Lines: lines}
	issues, roles, _ := issuesOf(ds, nil)
	if len(issues) != 8 {
		t.Fatalf("issue: %d", len(issues))
	}
	if roles[21] != RoleLabels || roles[26] != RoleCustom+"Team" || roles[32] != RoleLinkOut+"Blocks" || roles[36] != RoleIgnore {
		t.Fatalf("ruoli: %v", roles)
	}
	s := issues[1]
	if s.Key != "ACME-2" || s.Parent != "10001" || len(s.Sprints) != 2 || len(s.Labels) != 1 || s.Custom["Story Points"][0] != "5" {
		t.Fatalf("story: %+v", s)
	}
	if len(s.Comments) != 2 || s.Comments[1].Body != "Serve anche la variante email; grazie" || s.Comments[0].Author.Name != "Anna Bianchi" || s.Comments[1].Author.Name != "u-ext" {
		t.Fatalf("commenti: %+v", s.Comments)
	}
	if len(s.Worklogs) != 1 || s.Worklogs[0].Minutes != 120 || s.Worklogs[0].Comment != "Analisi iniziale" {
		t.Fatalf("ore: %+v", s.Worklogs)
	}
	if len(s.Attachments) != 1 || s.Attachments[0].Name != "bozza.png" || !strings.HasPrefix(s.Attachments[0].URL, "https://") {
		t.Fatalf("allegati: %+v", s.Attachments)
	}
	if len(s.Links) != 1 || s.Links[0] != (Link{Type: "Blocks", Out: true, Target: "ACME-3"}) {
		t.Fatalf("collegamenti: %+v", s.Links)
	}
	if issues[5].Estimate == nil || *issues[5].Estimate != 240 {
		t.Fatalf("stima: %v", issues[5].Estimate)
	}
	a := analyze(ds, issues, roles, nil)
	if a.DateFormat != "jira" || a.KeyPrefix != "ACME" || len(a.Sprints) != 2 || a.Comments != 2 || a.Worklogs != 1 {
		t.Fatalf("analisi: %+v", a)
	}
	for _, fi := range a.Fields {
		if fi.Name == "Story Points" && fi.DataType != "integer" {
			t.Errorf("Story Points dedotto come %s", fi.DataType)
		}
		if fi.Name == "Team" && fi.DataType != "enum" {
			t.Errorf("Team dedotto come %s", fi.DataType)
		}
	}
}

func TestYouTrackCSV(t *testing.T) {
	f, err := os.Open("../../itest/testdata/import/youtrack.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header, rows, lines, err := readCSV(f, "")
	if err != nil {
		t.Fatal(err)
	}
	ds := &Dataset{System: "youtrack", Format: "csv", Header: header, Rows: rows, Lines: lines}
	issues, _, _ := issuesOf(ds, nil)
	if len(issues) != 5 || issues[1].Parent != "DEMO-1" || len(issues[0].Labels) != 2 {
		t.Fatalf("issue: %+v", issues)
	}
	if issues[0].Estimate == nil || *issues[0].Estimate != 720 {
		t.Fatalf("stima 1d 4h: %v", issues[0].Estimate)
	}
	if l := issues[2].Links; len(l) != 1 || l[0] != (Link{Type: "Depend", Out: false, Target: "DEMO-2"}) {
		t.Fatalf("depends on: %+v", l)
	}
	if issues[0].Custom["Subsystem"][0] != "Backend" {
		t.Fatalf("campi: %+v", issues[0].Custom)
	}
}

func TestWikiToMarkdown(t *testing.T) {
	in := "h2. Titolo\n*grassetto* e _corsivo_ con {{codice}}\n* uno\n** due\n# primo\n[link|https://x.example/a] e [~accountid:abc]\n{code:go}\nx := *p\n{code}"
	want := "## Titolo\n**grassetto** e *corsivo* con `codice`\n- uno\n  - due\n1. primo\n[link](https://x.example/a) e @abc\n```go\nx := *p\n```"
	if got := wikiToMarkdown(in); got != want {
		t.Fatalf("conversione:\n%s\natteso:\n%s", got, want)
	}
}

func TestGuessCategory(t *testing.T) {
	for in, want := range map[string]string{"To Do": "todo", "Selected for Development": "todo", "In Progress": "in_progress",
		"In Review": "review", "QA": "verify", "Done": "done", "Won't Do": "cancelled", "Fixed": "done", "Submitted": "todo",
		"Verified": "done", "Reopened": "todo", "Bloccato": ""} {
		if got := guessCategory(in); got != want {
			t.Errorf("%s: %s, atteso %s", in, got, want)
		}
	}
}

func TestDurationAndSlug(t *testing.T) {
	for in, want := range map[string]float64{"1w 2d 3h 30m": 2400 + 960 + 180 + 30, "2g 4h": 1200, "90": 90, "1,5h": 90} {
		if got, err := parseDurationText(in); err != nil || got != want {
			t.Errorf("%s: %v %v, atteso %v", in, got, err, want)
		}
	}
	if s := slug("Story Points (Σ)"); s != "story_points_s" {
		t.Errorf("slug: %s", s)
	}
	if s := slug("Priorità cliente"); s != "priorita_cliente" {
		t.Errorf("slug: %s", s)
	}
}
