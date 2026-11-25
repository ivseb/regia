package api

import "testing"

// La mappa aggancia alle fasi valutazioni, azioni, gate e approvazioni e raccoglie i team coinvolti.
func TestBuildMap(t *testing.T) {
	def := `{
	  "phases": [
	    {"name": "Bozza", "category": "todo"},
	    {"name": "Valutazione", "category": "in_progress"},
	    {"name": "Approvata", "category": "done"},
	    {"name": "Sospesa", "category": "in_progress", "off_path": true}
	  ],
	  "initial": "Bozza",
	  "transitions": [
	    {"from": ["Bozza"], "to": "Valutazione", "gates": ["g1"]},
	    {"from": ["Valutazione"], "to": "Approvata", "approval": "Approvata", "when": "importo > 1000"}
	  ],
	  "gates": [{"key": "g1", "name": "Titolo compilato", "condition": "titolo"}],
	  "rules": [
	    {"key": "r1", "name": "Sicurezza", "on": "enter:Valutazione", "when": "driver = normativo",
	     "actions": [{"kind": "open_review", "ref": "sec"}, {"kind": "create_epic"}]},
	    {"key": "r2", "name": "Vecchia", "on": "create", "actions": [{"kind": "open_review", "ref": "sparita"}]}
	  ]
	}`
	reviews := map[string]mapReviewType{"sec": {Name: "Sicurezza", Team: "Team sicurezza", Scope: "impact", Mandatory: true}}
	policies := map[string]mapPolicy{"|approvata": {Steps: [][]string{{"sponsor"}, {"comitato"}}}}
	got := buildMap([]mapProcRow{{Key: "p", Name: "P", TypeKey: "business_request", TypeName: "Richiesta", Version: 2, Definition: []byte(def)}}, reviews, policies)
	if len(got) != 1 {
		t.Fatalf("attesi 1 processo, trovati %d", len(got))
	}
	p := got[0]
	if len(p.Phases) != 4 || !p.Phases[0].Initial || !p.Phases[3].OffPath {
		t.Fatalf("fasi inattese: %+v", p.Phases)
	}
	// Bozza: la regola «create» con la valutazione scomparsa, segnalata come mancante.
	if it := p.Phases[0].Items; len(it) != 1 || !it[0].Missing || it[0].Kind != "review" {
		t.Errorf("Bozza: %+v", it)
	}
	// Valutazione: gate, valutazione con team e ambito, azione epic.
	kinds := map[string]mapItem{}
	for _, it := range p.Phases[1].Items {
		kinds[it.Kind] = it
	}
	if kinds["gate"].Name != "Titolo compilato" || kinds["review"].Team != "Team sicurezza" || kinds["review"].When != "driver = normativo" || kinds["action"].Key != "create_epic" {
		t.Errorf("Valutazione: %+v", p.Phases[1].Items)
	}
	// Approvata: l'approvazione con i passi della politica e la condizione del passaggio.
	if it := p.Phases[2].Items; len(it) != 1 || it[0].Kind != "approval" || len(it[0].Steps) != 2 || it[0].When != "importo > 1000" || it[0].Missing {
		t.Errorf("Approvata: %+v", it)
	}
	if len(p.Teams) != 1 || p.Teams[0] != "Team sicurezza" {
		t.Errorf("team: %v", p.Teams)
	}
	var epic, rev bool
	for _, l := range p.Links {
		epic = epic || l.Kind == "epic"
		rev = rev || (l.Kind == "review" && l.Scope == "impact")
	}
	if !epic || !rev {
		t.Errorf("collegamenti: %+v", p.Links)
	}
	// Senza politica l'approvazione è segnalata.
	got = buildMap([]mapProcRow{{Key: "p", Definition: []byte(def)}}, reviews, nil)
	if !got[0].Phases[2].Items[0].Missing {
		t.Errorf("approvazione senza politica non segnalata")
	}
}
