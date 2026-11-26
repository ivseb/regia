package planning

import (
	"testing"

	"regia/internal/platform/calendar"
)

func ptr(s string) *string { return &s }

// Dipendenze tipizzate (progetti.md §3): fine→inizio, inizio→inizio e fine→fine con ritardo in
// giorni lavorativi; la milestone (durata zero) come successore fine→inizio cade il giorno stesso.
func TestSchedulerDependencies(t *testing.T) {
	tasks := []Task{
		{ID: "a", Key: "A", Start: ptr("2027-01-04"), End: ptr("2027-01-08")},                                                       // lun → ven
		{ID: "m", Key: "M", Start: ptr("2027-01-08"), End: ptr("2027-01-08"), Milestone: true, Deps: []Dep{{ID: "a", Kind: DepFS}}}, // stesso giorno
		{ID: "b", Key: "B", Start: ptr("2027-01-11"), End: ptr("2027-01-15"), Deps: []Dep{{ID: "m", Kind: DepFS}}},
		{ID: "c", Key: "C", Start: ptr("2027-01-04"), End: ptr("2027-01-07"), Deps: []Dep{{ID: "a", Kind: DepSS, Lag: 2}}},
		{ID: "e", Key: "E", Start: ptr("2027-01-11"), End: ptr("2027-01-12"), Deps: []Dep{{ID: "b", Kind: DepFF, Lag: 1}}},
		{ID: "x", Key: "X", Start: ptr("2027-01-04"), End: ptr("2027-01-05"), StateCategory: "cancelled", Deps: []Dep{{ID: "a", Kind: DepFS}}},
	}
	s := NewScheduler(tasks)
	if err := s.CheckCycle(); err != nil {
		t.Fatal(err)
	}
	// C (ss +2) parte due giorni lavorativi dopo l'inizio di A (il 6 gennaio è festa), mantenendo i suoi tre giorni.
	if v := s.Violated("c"); len(v) != 1 || v[0] != "a" {
		t.Fatalf("violazioni di C: %v", v)
	}
	start, _ := calendar.Parse("2027-01-11")
	end, _ := calendar.Parse("2027-01-15")
	got := map[string][2]string{}
	for _, c := range s.Move("a", start, end, true) {
		got[c.ID] = [2]string{c.Start, c.End}
	}
	want := map[string][2]string{
		"a": {"2027-01-11", "2027-01-15"},
		"m": {"2027-01-15", "2027-01-15"}, // milestone: il giorno della fine di A
		"b": {"2027-01-18", "2027-01-22"}, // il giorno lavorativo dopo la milestone
		"c": {"2027-01-13", "2027-01-15"}, // ss +2
		"e": {"2027-01-22", "2027-01-25"}, // ff +1: finisce un giorno lavorativo dopo B, durata 2
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %v, atteso %v (tutte: %v)", id, got[id], w, got)
		}
	}
	if _, ok := got["x"]; ok {
		t.Errorf("la voce annullata non si sposta: %v", got["x"])
	}
	// Spostare B indietro non anticipa E: lo slittamento è solo in avanti.
	bs, _ := calendar.Parse("2027-01-04")
	be, _ := calendar.Parse("2027-01-08")
	for _, c := range s.Move("b", bs, be, true) {
		if c.ID == "e" {
			t.Errorf("E anticipata: %v", c)
		}
	}
	// Un ciclo si riconosce.
	cyc := NewScheduler([]Task{
		{ID: "p", Key: "P", Deps: []Dep{{ID: "q", Kind: DepSS}}},
		{ID: "q", Key: "Q", Deps: []Dep{{ID: "p", Kind: DepFF}}},
	})
	if err := cyc.CheckCycle(); err == nil {
		t.Fatal("ciclo non riconosciuto")
	}
}

// Senza Deps valgono le DependsOn degli impatti come fine→inizio, come prima.
func TestSchedulerImpactsUnchanged(t *testing.T) {
	s := NewScheduler([]Task{
		{ID: "be", Key: "BE", Start: ptr("2026-12-01"), End: ptr("2026-12-07")},
		{ID: "fe", Key: "FE", Start: ptr("2026-12-09"), End: ptr("2026-12-22"), DependsOn: []string{"be"}},
	})
	start, _ := calendar.Parse("2026-12-14")
	end, _ := calendar.Parse("2026-12-18")
	ch := s.Move("be", start, end, true)
	if len(ch) != 2 || ch[1].Start != "2026-12-21" {
		t.Fatalf("cascata degli impatti: %v", ch)
	}
}
