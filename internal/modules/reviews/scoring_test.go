package reviews

import (
	"strings"
	"testing"

	"regia/internal/engine"
)

func f(v float64) *float64 { return &v }

func scoredDef() *Definition {
	return &Definition{Name: "Punteggio", Sections: []Section{
		{Key: "a", Title: "A", Questions: []Question{
			{Key: "mfa", Label: "MFA?", Type: QYesNo, YesScore: f(10), NoScore: f(0)},
			{Key: "cifratura", Label: "Cifratura", Type: QSingle, Weight: f(2), Options: []Option{
				{Value: "nessuna", Label: "Nessuna", Score: f(0)},
				{Value: "riposo", Label: "A riposo", Score: f(5)},
				{Value: "totale", Label: "Totale", Score: f(10)}}},
			{Key: "maturita", Label: "Maturità", Type: QScale, Reverse: true},
			{Key: "controlli", Label: "Controlli", Type: QMatrix,
				Rows:    []Option{{Value: "log", Label: "Log"}, {Value: "backup", Label: "Backup"}},
				Options: []Option{{Value: "no", Label: "No", Score: f(0)}, {Value: "si", Label: "Sì", Score: f(4)}}},
			{Key: "note", Label: "Note", Type: QText},
			{Key: "extra", Label: "Extra", Type: QYesNo, YesScore: f(10), VisibleWhen: "mfa: no"},
		}},
	}, Scoring: &Scoring{Thresholds: []Threshold{
		{Min: 0, Outcome: "non_favorevole"}, {Min: 50, Outcome: "favorevole_con_prescrizioni"}, {Min: 80, Outcome: "favorevole"}}}}
}

func TestScoring(t *testing.T) {
	d := scoredDef()
	if ps := d.Validate(); hasErrors(ps) {
		t.Fatalf("definizione non valida: %v", ps)
	}
	// Vuoto: massimo = 10 + 2*10 + 4 (scala 1-5) + 8 (matrice); "extra" è nascosta.
	ev := Evaluate(d, engine.Facts{}, map[string]any{})
	if !ev.Scored || ev.MaxScore != 10+20+4+8 || ev.Score != 0 || ev.Proposed != "non_favorevole" {
		t.Fatalf("vuoto: %+v", ev)
	}
	// Tutto al meglio. Scala invertita: 1 vale 4 punti.
	ev = Evaluate(d, engine.Facts{}, map[string]any{"mfa": true, "cifratura": "totale", "maturita": 1.0,
		"controlli": map[string]any{"log": "si", "backup": "si"}})
	if ev.Percent != 100 || ev.Proposed != "favorevole" || ev.Score != ev.MaxScore {
		t.Fatalf("pieno: %+v", ev)
	}
	// Parziale: cifratura a riposo (peso 2: 10), mfa no (0), maturità 5 (0), una riga su due (4) = 14.
	ev = Evaluate(d, engine.Facts{}, map[string]any{"mfa": false, "cifratura": "riposo", "maturita": 5.0,
		"controlli": map[string]any{"log": "si"}})
	if ev.Score != 14 || ev.MaxScore != 42+10 || ev.Proposed != "non_favorevole" {
		// con mfa: no la domanda "extra" è visibile e vale 10 in più al massimo
		t.Fatalf("parziale: %+v", ev)
	}
	// La matrice a metà non conta come risposta, ma i suoi punti sì.
	if ev.Answered != 3 {
		t.Fatalf("risposte: %d", ev.Answered)
	}
	// Peso 0: la domanda esce dal punteggio.
	d.Sections[0].Questions[1].Weight = f(0)
	ev = Evaluate(d, engine.Facts{}, map[string]any{})
	if ev.MaxScore != 10+4+8 {
		t.Fatalf("peso zero: %+v", ev)
	}
	// Nessuna domanda con punteggio.
	plain := &Definition{Name: "x", Sections: []Section{{Key: "a", Title: "A", Questions: []Question{{Key: "q", Label: "Q", Type: QText}}}}}
	if ev := Evaluate(plain, engine.Facts{}, nil); ev.Scored || ev.Proposed != "" {
		t.Fatalf("senza punteggio: %+v", ev)
	}
}

func TestScoringValidation(t *testing.T) {
	d := scoredDef()
	d.Scoring.Thresholds = []Threshold{{Min: 50, Outcome: "favorevole"}, {Min: 50, Outcome: "x"}, {Min: 120, Outcome: "Bad Key"}}
	ps := d.Validate()
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.Path+": "+p.Msg)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"ripetuta", "tra 0 e 100", "non valida", "soglia da 0"} {
		if !strings.Contains(all, want) {
			t.Errorf("manca %q in:\n%s", want, all)
		}
	}
	d = scoredDef()
	d.Sections[0].FilledBy = "tutti"
	d.Sections[0].Questions[2].Min, d.Sections[0].Questions[2].Max = f(5), f(5)
	d.Sections[0].Questions[3].Rows = nil
	ps = d.Validate()
	if n := len(ps); n < 3 {
		t.Fatalf("attesi almeno 3 problemi: %v", ps)
	}
}

func TestMatrixAndScale(t *testing.T) {
	q := &Question{Key: "m", Type: QMatrix, Rows: []Option{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}},
		Options: []Option{{Value: "x", Label: "X"}, {Value: "y", Label: "Y"}}}
	v, err := q.NormalizeValue(map[string]any{"A": "X", "b": "", "a2": ""})
	if err != nil || len(v.(map[string]any)) != 1 || v.(map[string]any)["a"] != "x" {
		t.Fatalf("matrice: %v %v", v, err)
	}
	if _, err := q.NormalizeValue(map[string]any{"a": "zzz"}); err == nil {
		t.Fatal("colonna sconosciuta accettata")
	}
	facts := q.answerFacts(v)
	if len(facts) != 1 || facts[0] != "a.x" {
		t.Fatalf("fatti della matrice: %v", facts)
	}
	s := &Question{Key: "s", Type: QScale, MinLabel: "poco"}
	if _, err := s.NormalizeValue(2.5); err == nil {
		t.Fatal("scala non intera accettata")
	}
	if _, err := s.NormalizeValue(6.0); err == nil {
		t.Fatal("scala fuori misura accettata")
	}
	if v, err := s.NormalizeValue(3.0); err != nil || v != 3.0 {
		t.Fatalf("scala: %v %v", v, err)
	}
	// Le condizioni leggono la riga e la colonna della matrice e il numero della scala.
	d := &Definition{Name: "c", Sections: []Section{{Key: "a", Title: "A", Questions: []Question{
		*q, *s, {Key: "dopo", Label: "D", Type: QText, VisibleWhen: "m: a.x s: >=3"}}}}}
	if ev := Evaluate(d, nil, map[string]any{"m": map[string]any{"a": "x", "b": "y"}, "s": 4.0}); !ev.Visible["dopo"] {
		t.Fatalf("condizione su matrice e scala: %+v", ev.Visible)
	}
	if ev := Evaluate(d, nil, map[string]any{"m": map[string]any{"a": "y", "b": "y"}, "s": 4.0}); ev.Visible["dopo"] {
		t.Fatal("la condizione doveva essere falsa")
	}
}

func TestSectionAudience(t *testing.T) {
	p := &perms{owner: true, sections: []string{"b"}}
	if !p.canAnswer(StageCompile, "a", false) || p.canAnswer(StageCompile, "a", true) || !p.canAnswer(StageCompile, "b", true) {
		t.Fatal("sezioni del team")
	}
	team := &perms{team: true}
	if !team.canAnswer(StageClarification, "a", true) {
		t.Fatal("il team compila le sue sezioni")
	}
}
