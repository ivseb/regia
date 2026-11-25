package devsecops

import (
	"strings"
	"testing"

	"regia/internal/modules/devsecops/contract"
)

func norm(t *testing.T, d PolicyDef) *PolicyDef {
	t.Helper()
	if ps := d.Normalize(); len(ps) != 0 {
		t.Fatalf("politica non valida: %v", ps)
	}
	return &d
}

func TestPolicyClassOf(t *testing.T) {
	d := norm(t, DefaultPolicy())
	d.Classes = append(d.Classes, Class{Key: "dati", Name: "Dati personali esposti", Data: []string{"personali"}, Exposure: []string{"internet"}, Modes: map[string]string{"test": "block"}})
	cases := []struct {
		a    AppAttrs
		want string
	}{
		{AppAttrs{Criticality: "critical", CriticalFunction: true}, "funzione_critica"},
		{AppAttrs{Criticality: "high"}, "criticita_alta"},
		{AppAttrs{Criticality: "low", Data: "Personali", Exposure: "Internet"}, "dati"},
		{AppAttrs{Criticality: "low", Data: "personali", Exposure: "interna"}, "altre"},
		{AppAttrs{}, "altre"},
	}
	for _, c := range cases {
		if got := d.ClassOf(c.a).Key; got != c.want {
			t.Errorf("%+v: classe %s, attesa %s", c.a, got, c.want)
		}
	}
}

func TestPolicyNormalize(t *testing.T) {
	d := DefaultPolicy()
	d.Controls[0].Aliases = []string{"Semgrep"} // già alias di «sast»
	d.Classes[0].Modes["inesistente"] = "block"
	d.Classes[1].Modes["lint"] = "forte"
	d.Classes[1].Criticality = []string{"enorme"}
	ps := d.Normalize()
	got := ""
	for _, p := range ps {
		got += p.Path + " "
	}
	for _, w := range []string{"controls[", "classes[0].modes.inesistente", "classes[1].modes.lint", "classes[1].criticality"} {
		if !strings.Contains(got, w) {
			t.Errorf("manca il problema %s in: %s", w, got)
		}
	}
	// Le modalità assenti valgono off.
	ok := DefaultPolicy()
	delete(ok.Fallback.Modes, "sast")
	if ps := ok.Normalize(); len(ps) != 0 || ok.Fallback.Modes["sast"] != "off" {
		t.Errorf("modalità assente: %v %v", ps, ok.Fallback.Modes)
	}
}

func checks(cs ...string) []contract.CheckResult {
	var out []contract.CheckResult
	for _, c := range cs {
		name, mode, _ := strings.Cut(c, ":")
		o := "passed"
		if strings.HasSuffix(mode, "!") {
			o, mode = "not_run", strings.TrimSuffix(mode, "!")
		}
		out = append(out, contract.CheckResult{Name: name, Mode: mode, Outcome: o})
	}
	return out
}

func TestEvaluate(t *testing.T) {
	d := norm(t, DefaultPolicy())
	crit := d.ClassOf(AppAttrs{CriticalFunction: true}) // test, secrets, dependencies, sast, image in block; lint warn
	cases := []struct {
		name string
		cs   []contract.CheckResult
		run  bool
		want string
	}{
		{"tutto in regola, con alias", checks("tests:block", "lint:warn", "gitleaks:block", "grype:block", "semgrep:block", "trivy:block"), true, StAdherent},
		{"più forte del richiesto", checks("test:block", "lint:block", "secrets:block", "dependencies:block", "sast:block", "image:block"), true, StAdherent},
		{"mancano alcuni controlli", checks("test:block", "lint:warn", "secrets:block"), true, StPartial},
		{"uno è più debole", checks("test:block", "lint:warn", "secrets:warn", "dependencies:block", "sast:block", "image:block"), true, StOff},
		{"uno è spento", checks("test:block", "lint:warn", "secrets:off", "dependencies:block", "sast:block", "image:block"), true, StOff},
		{"un controllo che non è girato conta come mancante", checks("test:block!", "lint:warn", "secrets:block", "dependencies:block", "sast:block", "image:block"), true, StPartial},
		{"nessuno dei richiesti gira", checks("altro:block"), true, StOff},
		{"nessun esito ricevuto", nil, false, StNone},
	}
	for _, c := range cases {
		if got, _ := evaluate(d, crit, c.cs, c.run); got != c.want {
			t.Errorf("%s: %s, atteso %s", c.name, got, c.want)
		}
	}
	// Una classe che non richiede niente è sempre in regola, anche senza controlli.
	none := &Class{Key: "x", Name: "x", Modes: map[string]string{}}
	if got, _ := evaluate(d, none, nil, true); got != StAdherent {
		t.Errorf("classe senza richieste: %s", got)
	}
}

func TestAggregate(t *testing.T) {
	sv := func(s ...string) []ServiceAdherence {
		var out []ServiceAdherence
		for _, x := range s {
			out = append(out, ServiceAdherence{Status: x})
		}
		return out
	}
	cases := []struct {
		in   []ServiceAdherence
		want string
	}{
		{nil, StNone},
		{sv(StNone, StNone), StNone},
		{sv(StAdherent, StAdherent), StAdherent},
		{sv(StAdherent, StNone), StPartial},
		{sv(StAdherent, StPartial), StPartial},
		{sv(StPartial, StOff, StAdherent), StOff},
	}
	for _, c := range cases {
		if got := aggregate(c.in); got != c.want {
			t.Errorf("aggregate: %s, atteso %s", got, c.want)
		}
	}
}

func TestDiffPolicy(t *testing.T) {
	a := norm(t, DefaultPolicy())
	b := norm(t, DefaultPolicy())
	if len(DiffPolicy(a, b)) != 0 {
		t.Fatal("definizioni uguali")
	}
	b.Fallback.Modes["sast"] = "block"
	b.Classes[1].Criticality = []string{"critical"}
	b.Controls = append(b.Controls, Control{Key: "iac", Name: "Infrastruttura come codice"})
	b.Classes = b.Classes[:1]
	b = norm(t, *b)
	text := ""
	for _, c := range DiffPolicy(a, b) {
		text += c.Text + "\n"
	}
	for _, w := range []string{"Nuovo controllo: Infrastruttura come codice", "Classe tolta: «Criticità alta»", "Tutte le altre», Analisi statica della sicurezza: da spento a bloccante"} {
		if !strings.Contains(text, w) {
			t.Errorf("manca %q in:\n%s", w, text)
		}
	}
}
