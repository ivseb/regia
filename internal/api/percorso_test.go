package api

import (
	"testing"

	"regia/internal/engine"
)

func pcStates() []engine.State {
	return []engine.State{
		{ID: "1", Name: "Bozza", Category: "todo"},
		{ID: "2", Name: "Valutazione", Category: "in_progress"},
		{ID: "3", Name: "Approvata", Category: "done"},
		{ID: "4", Name: "In pausa", Category: "in_progress", OffPath: true},
	}
}

func TestPhaseLine(t *testing.T) {
	got := phaseLine(pcStates(), "valutazione")
	want := []string{"done", "current", "next", "next"}
	for i, w := range want {
		if got[i].Status != w {
			t.Errorf("fase %s: %s, atteso %s", got[i].Name, got[i].Status, w)
		}
	}
	// Una fase fuori percorso come attuale: nessuna fase risulta «fatta».
	for _, p := range phaseLine(pcStates(), "In pausa") {
		if p.Status == "done" {
			t.Errorf("fase %s fatta con il processo in pausa", p.Name)
		}
	}
}

func TestNextStepsAndTriggers(t *testing.T) {
	st := pcStates()
	def := &engine.Workflow{
		Transitions: []engine.Transition{
			{From: []string{"Bozza"}, To: "Valutazione", Label: "Avvia", Roles: []string{"demand_manager"}},
			{From: []string{"Valutazione"}, To: "Approvata", Label: "Approva"},
			{From: []string{"*"}, To: "In pausa"},
			{From: []string{"Valutazione"}, To: "Bozza", System: true},
		},
		Rules: []engine.Rule{
			{Key: "r1", Name: "Parere sicurezza", On: "enter:Valutazione", Actions: []engine.Action{{Kind: "open_review", Ref: "sicurezza"}}},
			{Key: "r2", Name: "Altra fase", On: "enter:Approvata", Actions: []engine.Action{{Kind: "open_review", Ref: "x"}}},
			{Key: "r3", Name: "Solo se cloud", On: "enter:Valutazione", When: "ospitato: cloud", Actions: []engine.Action{{Kind: "open_review", Ref: "cloud"}}},
		},
	}
	next := nextSteps(def, st, st[0], nil, nil, nil)
	if len(next) != 2 || next[0].To != "Valutazione" || !next[0].Forward || next[1].To != "In pausa" || next[1].Forward {
		t.Fatalf("passaggi inattesi: %+v", next)
	}
	facts := engine.Facts{}
	facts.Set("ospitato", "cloud")
	tr := triggersFor(def, next, facts)
	if len(tr) != 2 || tr[0].Ref != "sicurezza" || tr[1].Ref != "cloud" || tr[1].WhenMet == nil || !*tr[1].WhenMet {
		t.Fatalf("regole inattese: %+v", tr)
	}
	facts = engine.Facts{}
	facts.Set("ospitato", "onprem")
	if tr = triggersFor(def, next, facts); tr[1].WhenMet == nil || *tr[1].WhenMet {
		t.Fatalf("la condizione non doveva essere soddisfatta: %+v", tr[1])
	}
}

func TestBuildTodoOrder(t *testing.T) {
	o := &percorsoOut{
		Approvals: []pcApproval{{Stage: "Sponsor", Waiting: []string{"Rossi"}, CanDecide: true}},
		Reviews:   []pcReview{{Key: "RV-1", Type: "Sicurezza", Team: "Sec", Stage: "compile", Open: true, Mine: true}, {Key: "RV-2", Team: "Arch", Open: true}},
	}
	td := buildTodo("DEM-1", "business_request", o)
	if td[0].Kind != "decide" || !td[0].Mine || td[1].Kind != "compile_review" || td[1].Link != "/valutazioni/RV-1" || td[2].Kind != "wait_review" {
		t.Fatalf("ordine inatteso: %+v", td)
	}
	if td = buildTodo("DEM-1", "business_request", &percorsoOut{}); len(td) != 1 || td[0].Kind != "nothing" {
		t.Fatalf("senza nulla: %+v", td)
	}
}
