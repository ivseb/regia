package reviews

import (
	"strings"
	"testing"

	"regia/internal/engine"
)

// I modelli del pacchetto di partenza sono validi e senza errori.
func TestStarterPackage(t *testing.T) {
	raw, err := starters.ReadFile("starter/valutazioni_standard.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Teams) != 4 || len(p.Types) != 4 || len(p.Templates) != 4 {
		t.Fatalf("pacchetto: %d team, %d tipi, %d modelli", len(p.Teams), len(p.Types), len(p.Templates))
	}
	for _, tpl := range p.Templates {
		d, err := DecodeDefinition(tpl.Definition)
		if err != nil {
			t.Fatalf("%s: %v", tpl.Key, err)
		}
		for _, pr := range d.Validate() {
			t.Errorf("%s: %s %s: %s", tpl.Key, pr.Level, pr.Path, pr.Msg)
		}
	}
}

func TestEvaluate(t *testing.T) {
	d := &Definition{Name: "Prova", Sections: []Section{
		{Key: "a", Title: "A", Questions: []Question{
			{Key: "esposto", Label: "Esposto?", Type: QYesNo, Required: true},
			{Key: "auth", Label: "Autenticazione", Type: QSingle, Required: true, VisibleWhen: "esposto: sì",
				Options: []Option{{Value: "sso", Label: "SSO"}, {Value: "locale", Label: "Locale"}}},
			{Key: "crit", Label: "Criticità", Type: QSingle, Prefill: "criticita",
				Options: []Option{{Value: "bassa", Label: "Bassa"}, {Value: "alta", Label: "Alta"}}},
		}},
		{Key: "b", Title: "B", VisibleWhen: "auth: locale", Questions: []Question{
			{Key: "mfa", Label: "MFA?", Type: QYesNo, Required: true},
		}},
	}, Derivations: []Derivation{
		{Key: "waf", When: "esposto: sì", Text: "WAF"},
		{Key: "mfa", When: "mfa: no", Text: "MFA"},
		{Key: "critica", When: "criticita: alta esposto: sì", Text: "Pentest"},
	}}
	if ps := d.Validate(); len(ps) != 0 {
		t.Fatalf("problemi: %v", ps)
	}
	facts := engine.Facts{}
	facts.Set("criticita", "high", "alta")
	if v := d.Sections[0].Questions[2].prefill(facts["criticita"]); v != "alta" {
		t.Fatalf("precompilazione: %v", v)
	}
	ev := Evaluate(d, facts, map[string]any{})
	if ev.Visible["auth"] || ev.Sections["b"].Visible || len(ev.Missing) != 1 || len(ev.Derived) != 0 {
		t.Fatalf("questionario vuoto: %+v", ev)
	}
	ev = Evaluate(d, facts, map[string]any{"esposto": true, "auth": "locale", "mfa": false})
	if !ev.Visible["auth"] || !ev.Visible["mfa"] || len(ev.Missing) != 0 || len(ev.Derived) != 3 {
		t.Fatalf("questionario completo: %+v", ev)
	}
	// Nascondendo la sezione B la risposta su MFA non conta più, nemmeno per le derivazioni.
	ev = Evaluate(d, facts, map[string]any{"esposto": true, "auth": "sso", "mfa": false})
	if ev.Visible["mfa"] || len(ev.Derived) != 2 {
		t.Fatalf("risposta nascosta: %+v", ev)
	}
	// Una condizione che usa una domanda successiva è un errore.
	d.Sections[0].Questions[0].VisibleWhen = "mfa: sì"
	if ps := d.Validate(); !hasErrors(ps) || !strings.Contains(ps[0].Msg, "non viene prima") {
		t.Fatalf("condizione in avanti: %v", ps)
	}
}

func TestNormalizeValue(t *testing.T) {
	q := &Question{Key: "n", Type: QNumber, Min: new(float64)}
	if _, err := q.NormalizeValue(-1.0); err == nil {
		t.Fatal("minimo non controllato")
	}
	m := &Question{Key: "m", Type: QMulti, Options: []Option{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}}
	if v, err := m.NormalizeValue([]any{"B", "a", "b"}); err != nil || len(v.([]string)) != 2 {
		t.Fatalf("scelta multipla: %v %v", v, err)
	}
	if _, err := m.NormalizeValue([]any{"c"}); err == nil {
		t.Fatal("scelta inesistente accettata")
	}
	dt := &Question{Key: "d", Type: QDate}
	if _, err := dt.NormalizeValue("2026-13-01"); err == nil {
		t.Fatal("data non valida accettata")
	}
}

// Le regole «requirement» non diventano prescrizioni: vanno nei requisiti proposti, con la
// categoria «Requisito» di default e il metodo manuale; titolo, metodo e controllo si validano.
func TestDerivazioniRequisiti(t *testing.T) {
	d := &Definition{Name: "Prova", Sections: []Section{{Key: "a", Title: "A", Questions: []Question{
		{Key: "mfa", Label: "MFA?", Type: QYesNo},
	}}}, Derivations: []Derivation{
		{Key: "p", When: "mfa: no", Text: "Token hardware"},
		{Key: "r1", When: "mfa: no", Text: "Attivare la MFA", Kind: "requirement"},
		{Key: "r2", When: "mfa: no", Text: "Log degli accessi", Kind: "requirement", Title: "Log", Category: "Tracciabilità", Method: "evidence", Check: "log_accessi"},
	}}
	if ps := d.Validate(); len(ps) != 0 {
		t.Fatalf("problemi: %v", ps)
	}
	ev := Evaluate(d, engine.Facts{}, map[string]any{"mfa": false})
	if len(ev.Derived) != 1 || ev.Derived[0].Key != "p" || len(ev.DerivedRequirements) != 2 {
		t.Fatalf("derivazioni: %+v %+v", ev.Derived, ev.DerivedRequirements)
	}
	if r := ev.DerivedRequirements[0]; r.Category != "Requisito" || r.Method != "manual" {
		t.Fatalf("valori di default: %+v", r)
	}
	if r := ev.DerivedRequirements[1]; r.Category != "Tracciabilità" || r.Method != "evidence" || r.Check != "log_accessi" {
		t.Fatalf("regola con evidenza: %+v", r)
	}
	if ev := Evaluate(d, engine.Facts{}, map[string]any{"mfa": true}); len(ev.Derived)+len(ev.DerivedRequirements) != 0 {
		t.Fatalf("senza la condizione niente: %+v", ev)
	}
	d.Derivations = []Derivation{
		{Key: "a", When: "mfa: no", Text: "t", Title: "solo requisiti"},
		{Key: "b", When: "mfa: no", Text: "t", Kind: "requirement", Method: "evidence"},
		{Key: "c", When: "mfa: no", Text: "t", Kind: "requirement", Method: "boh"},
	}
	var paths []string
	for _, p := range d.Validate() {
		paths = append(paths, p.Path)
	}
	if got := strings.Join(paths, " "); got != "derivations[0].kind derivations[1].check derivations[2].method" {
		t.Fatalf("validazione: %s", got)
	}
}
