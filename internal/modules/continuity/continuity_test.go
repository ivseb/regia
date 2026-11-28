package continuity

import (
	"testing"
	"time"
)

func grid(cfg Config, level func(cat, hor string) int) Impacts {
	out := Impacts{}
	for _, c := range cfg.Categories {
		out[c.Key] = map[string]int{}
		for _, h := range cfg.Horizons {
			out[c.Key][h.Key] = level(c.Key, h.Key)
		}
	}
	return out
}

func TestProposta(t *testing.T) {
	cfg := DefaultConfig("it")
	if err := cfg.validate(); err != nil {
		t.Fatalf("pacchetto di partenza non valido: %v", err)
	}
	// Intollerabile (4) sull'economico a 4 ore: MTPD 4 h, RTO 1 h, critica.
	p := propose(&cfg, grid(cfg, func(c, h string) int {
		if c == "economico" && (h == "4h" || h == "1g") {
			return 4
		}
		return 1
	}))
	if *p.MTPD != 240 || *p.RTO != 60 || !p.Critical || *p.Category != "economico" || *p.Horizon != "4h" {
		t.Fatalf("proposta: %+v", p)
	}
	// Già al primo orizzonte: RTO alla metà.
	p = propose(&cfg, grid(cfg, func(c, h string) int { return 5 }))
	if *p.MTPD != 60 || *p.RTO != 30 {
		t.Fatalf("primo orizzonte: %+v", p)
	}
	// Mai intollerabile: niente MTPD, RTO all'ultimo orizzonte, non critica.
	p = propose(&cfg, grid(cfg, func(c, h string) int { return 2 }))
	if p.MTPD != nil || *p.RTO != 10080 || p.Critical {
		t.Fatalf("mai intollerabile: %+v", p)
	}
	// Oltre la soglia della funzione critica (3 giorni): MTPD a 1 settimana non è critica.
	p = propose(&cfg, grid(cfg, func(c, h string) int {
		if h == "1s" {
			return 4
		}
		return 1
	}))
	if *p.MTPD != 10080 || *p.RTO != 4320 || p.Critical {
		t.Fatalf("oltre la soglia: %+v", p)
	}
	if propose(&cfg, Impacts{}) != nil || complete(&cfg, Impacts{"economico": {"1h": 1}}) {
		t.Fatal("griglia vuota o incompleta")
	}
}

func TestConfigurazione(t *testing.T) {
	cfg := DefaultConfig("en")
	if cfg.Categories[0].Name != "Financial" || cfg.Horizons[0].Name != "1 hour" {
		t.Fatalf("pacchetto in inglese: %+v", cfg.Categories[0])
	}
	cfg = DefaultConfig("it")
	cfg.Horizons[0], cfg.Horizons[4] = cfg.Horizons[4], cfg.Horizons[0]
	if err := cfg.validate(); err != nil || cfg.Horizons[0].Minutes != 60 {
		t.Fatalf("gli orizzonti si ordinano per durata: %v %+v", err, cfg.Horizons)
	}
	for name, broken := range map[string]func(c *Config){
		"intollerabile fuori scala": func(c *Config) { c.Categories[0].Intolerable = 9 },
		"orizzonti uguali":          func(c *Config) { c.Horizons[1].Minutes = 60 },
		"chiave doppia":             func(c *Config) { c.TestTypes[1].Key = c.TestTypes[0].Key },
		"età dei test":              func(c *Config) { c.TestMaxAgeDays = 5 },
		"nessuna categoria":         func(c *Config) { c.Categories = nil },
	} {
		c := DefaultConfig("it")
		broken(&c)
		if c.validate() == nil {
			t.Errorf("%s: doveva essere rifiutata", name)
		}
	}
}

func TestModello(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	cfg := DefaultConfig("it")
	rto := func(n int) *int { return &n }
	m := &model{cfg: &cfg, now: now, apps: map[string]*appN{}, caps: map[string]*capN{}, bias: map[string]*biaN{}, provs: map[string]*provN{},
		deps: map[string][]string{}, provided: map[string][]string{}, plans: map[string][]planN{}, tests: map[string][]*testN{}}
	parent := "P"
	m.caps["P"] = &capN{Ref: Ref{ID: "P", Key: "ARC-1"}}
	m.caps["C"] = &capN{Ref: Ref{ID: "C", Key: "ARC-2"}, Parent: &parent}
	m.capOrder = []string{"P", "C"}
	// La BIA approvata sulla capability padre vale anche per chi supporta la figlia.
	m.bias["P"] = &biaN{Ref: Ref{ID: "B", Key: "ARC-3"}, CapID: "P", Approved: &approvedV{Version: 1, At: now.AddDate(-2, 0, 0), Critical: true, RTO: rto(240), RPO: rto(60)}}
	m.apps["A"] = &appN{Ref: Ref{ID: "A", Key: "ARC-4", Title: "A"}, RTO: rto(480), Caps: []string{"C"}}
	m.apps["D"] = &appN{Ref: Ref{ID: "D", Key: "ARC-5", Title: "D"}}
	m.appOrder = []string{"A", "D"}
	m.deps["A"] = []string{"D"}
	got := map[string]bool{}
	for _, i := range m.allIssues() {
		got[i.Ref] = true
	}
	for _, want := range []string{"rto_over_required:A", "dependency_rto_missing:A:D", "critical_not_declared:A", "plan_missing:A", "test_missing:A",
		"bia_review_due:P", "plan_missing:P", "test_missing:P"} {
		if !got[want] {
			t.Errorf("manca %s in %v", want, got)
		}
	}
	if is := m.findIssue("rto_over_required:A"); is == nil || *is.Required != 240 || is.Source.Key != "ARC-3" {
		t.Fatalf("findIssue: %+v", is)
	}
	// Un test riuscito sulla capability copre la funzione critica e le sue applicazioni.
	ok, day := OutcomePassed, now.AddDate(0, -1, 0)
	m.tests["P"] = []*testN{{Ref: Ref{ID: "T", Key: "ARC-6"}, Subject: "P", Executed: &day, Outcome: &ok, StateCat: "in_progress"}}
	for _, i := range m.allIssues() {
		if i.Code == IssueTestMissing {
			t.Fatalf("test ancora mancante: %+v", i)
		}
	}
}
