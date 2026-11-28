package engine

import (
	"context"
	"testing"
	"time"
)

func TestParseRuleEvent(t *testing.T) {
	ok := map[string]RuleEvent{
		"create":                     {Kind: "create"},
		"enter:Valutazione":          {Kind: "enter", Phase: "Valutazione"},
		"exit:In corso":              {Kind: "exit", Phase: "In corso"},
		"change:priorita":            {Kind: "change", Field: "priorita"},
		"change:titolo":              {Kind: "change", Field: "titolo"},
		"at:scadenza:-30":            {Kind: "at", Date: "scadenza", Offset: -30},
		"at:campo.fine_contratto:+7": {Kind: "at", Date: "campo.fine_contratto", Offset: 7},
		"at:evidenza.scadenza:0":     {Kind: "at", Date: "evidenza.scadenza"},
		"every:day":                  {Kind: "every"},
		"every:week:1":               {Kind: "every", Weekday: 1},
		"every:week:7":               {Kind: "every", Weekday: 7},
	}
	for on, want := range ok {
		got, err := ParseRuleEvent(on)
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v (atteso %+v)", on, got, err, want)
		}
	}
	for _, on := range []string{"", "create:x", "change:", "change:Priorità", "at:scadenza", "at::3", "at:scadenza:mai",
		"at:scadenza:99999", "every:week:0", "every:week:8", "every:month", "quando"} {
		if _, err := ParseRuleEvent(on); err == nil {
			t.Errorf("%q: atteso un errore", on)
		}
	}
	if !(RuleEvent{Kind: "at"}).Timed() || !(RuleEvent{Kind: "every"}).Timed() || (RuleEvent{Kind: "change"}).Timed() {
		t.Fatal("Timed")
	}
}

func TestRuleChainDepth(t *testing.T) {
	ctx := context.Background()
	if !rulesAllowed(ctx) {
		t.Fatal("un evento dell'utente fa scattare le regole")
	}
	mod := withRule(ctx, "p/m/e1", false)
	if !rulesAllowed(withRule(withRule(mod, "p/n/e2", false), "p/o/e3", false)) {
		t.Fatal("le azioni dei moduli non alzano il livello")
	}
	one := withRule(ctx, "p/a", true)
	if !rulesAllowed(one) || !chainOf(one).has("p/a") {
		t.Fatal("primo livello: le regole scattano e la catena ricorda la regola")
	}
	two := withRule(one, "p/b", true)
	if rulesAllowed(two) {
		t.Fatal("secondo livello: le regole non scattano più")
	}
	if chainOf(one).has("p/b") {
		t.Fatal("la catena del livello sopra non cambia")
	}
}

func TestRenderTemplate(t *testing.T) {
	el := &Element{Key: "DEM-7", Title: "Nuovo portale", State: "Triage", TypeName: "Richiesta", SpaceKey: "DEM"}
	f := Facts{}
	f.Set("priorita", "alta")
	f.Set("prima.priorita", "bassa")
	f.Set("ruolo.sponsor", "Anna", "Bruno")
	run := &RuleRun{Rule: Rule{Name: "Avviso"}, Today: "2026-11-28"}
	got := Render("{{chiave}} «{{ titolo }}» in {{fase}}: {{prima.priorita}} → {{campo.priorita}}, {{ruolo.sponsor}}, {{data}}, {{regola}}, {{link}}, {{ignoto}}.", el, f, run)
	want := "DEM-7 «Nuovo portale» in Triage: bassa → alta, Anna, Bruno, 2026-11-28, Avviso, /e/DEM-7, ."
	if got != want {
		t.Fatalf("\n%s\n%s", got, want)
	}
}

func TestChangedFieldsAndPrevious(t *testing.T) {
	before := map[string]any{"a": "x", "b": 1.0, "c": []any{"u"}, "d": ""}
	after := map[string]any{"a": "x", "b": 2.0, "c": []any{"u"}, "d": nil}
	got := changedFields(before, after, []string{"a", "b", "c", "d"})
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("cambiati: %v", got)
	}
	prev := Facts{}
	prev.Set("importo", 10.0)
	prev.Set("importo.valuta", "EUR")
	cur := Facts{}
	cur.Set("importo", 20.0)
	f := withPrevious(cur, prev, []string{"importo", "nuovo"})
	if f["prima.importo"][0] != 10.0 || f["prima.importo.valuta"][0] != "EUR" || f["importo"][0] != 20.0 {
		t.Fatalf("fatti: %v", f)
	}
	c, _ := ParseCondition("prima.importo: <15 importo: >15 prima.nuovo: assente")
	if !c.Eval(f) {
		t.Fatal("la condizione sui valori precedenti è vera")
	}
}

func TestTenantDay(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	// Data senza ora (mezzanotte UTC): vale quel giorno in qualunque fuso.
	d := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	for _, loc := range []*time.Location{rome, ny} {
		if got := tenantDay(d, loc).Format(time.DateOnly); got != "2026-12-01" {
			t.Fatalf("%s: %s", loc, got)
		}
	}
	// Data e ora: il giorno nel fuso del tenant (le 23:30 UTC sono già il giorno dopo a Roma).
	dt := time.Date(2026, 12, 1, 23, 30, 0, 0, time.UTC)
	if got := tenantDay(dt, rome).Format(time.DateOnly); got != "2026-12-02" {
		t.Fatalf("Roma: %s", got)
	}
	if got := tenantDay(dt, ny).Format(time.DateOnly); got != "2026-12-01" {
		t.Fatalf("New York: %s", got)
	}
	if isoWeekday(time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)) != 1 || isoWeekday(time.Date(2026, 11, 29, 0, 0, 0, 0, time.UTC)) != 7 {
		t.Fatal("giorni della settimana: lunedì = 1, domenica = 7")
	}
}

func TestFieldInput(t *testing.T) {
	if v, err := fieldInput(FieldDef{Key: "ok", DataType: "boolean"}, "sì"); err != nil || v != true {
		t.Fatalf("booleano: %v %v", v, err)
	}
	if v, err := fieldInput(FieldDef{Key: "n", DataType: "decimal"}, "3,5"); err != nil || v != 3.5 {
		t.Fatalf("numero: %v %v", v, err)
	}
	if _, err := fieldInput(FieldDef{Key: "n", DataType: "integer"}, "tre"); err == nil {
		t.Fatal("un numero non valido è un errore")
	}
	if v, _ := fieldInput(FieldDef{Key: "t", DataType: "enum", Multi: true}, "a, b,"); len(v.([]any)) != 2 {
		t.Fatalf("multi: %v", v)
	}
	if v, _ := fieldInput(FieldDef{Key: "t", DataType: "string"}, "  "); v != nil {
		t.Fatal("testo vuoto = svuota")
	}
}

func TestValidateNewEvents(t *testing.T) {
	w := &Workflow{
		Phases:      []Phase{{Name: "Aperta", Category: "todo"}, {Name: "Chiusa", Category: "done"}},
		Initial:     "Aperta",
		Transitions: []Transition{{From: []string{"Aperta"}, To: "Chiusa", Label: "Chiudi"}},
		Rules: []Rule{
			{Key: "a", Name: "a", On: "at:scadenza:-30", Actions: []Action{{Kind: ActionComment, Params: map[string]string{"text": "x"}}}},
			{Key: "b", Name: "b", On: "every:week:1", Actions: []Action{{Kind: ActionNotify, Params: map[string]string{"message": "x", "followers": "true"}}}},
			{Key: "c", Name: "c", On: "change:priorita", Actions: []Action{{Kind: ActionSetField, Params: map[string]string{"field": "titolo", "value": "x"}}}},
			{Key: "d", Name: "d", On: "every:month", Actions: []Action{{Kind: ActionWebhook, Params: map[string]string{"webhook": "x"}}}},
		},
	}
	var codes []string
	for _, p := range Validate(w) {
		if p.Level == "error" {
			codes = append(codes, p.Path+" "+p.Code)
		}
	}
	if len(codes) != 1 || codes[0] != "rules[3].on validation.rule.event_invalid" {
		t.Fatalf("problemi: %v", codes)
	}
}
