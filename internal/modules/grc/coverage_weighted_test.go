package grc

import (
	"testing"
	"time"
)

// La copertura pesata conta di più un perimetro critico: coprire l'applicazione critica
// alza la percentuale pesata più che coprire quella a bassa criticità, a parità di coppie.
func TestWeightedCoverage(t *testing.T) {
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	build := func(coveredScope string) Counts {
		m := &model{now: now, notice: now.AddDate(0, 0, 30), weights: map[string]int{
			"application:crit": criticalityWeight["critical"], "application:low": criticalityWeight["low"]}}
		ev := &evModel{ID: "e1", Status: "valid", ValidFrom: now.Add(-time.Hour), Scope: coveredScope}
		ctrl := &ctrlModel{ID: "c1", PhaseCat: "in_progress", Effectiveness: "effective", evidence: []*evModel{ev}}
		r := &reqModel{ID: "r1", Assessable: true, rows: []applRow{
			{scope: "application:crit", status: "applicable"},
			{scope: "application:low", status: "applicable"},
		}, maps: []ctrlMap{{c: ctrl, coverage: "full"}}}
		var c Counts
		for _, p := range m.pairs(r) {
			c.add(p)
		}
		c.finish()
		return c
	}
	onCrit, onLow := build("application:crit"), build("application:low")
	if onCrit.Applicable != 3 || onCrit.Covered != 1 || onLow.Covered != 1 {
		t.Fatalf("coppie: %+v %+v", onCrit, onLow)
	}
	// Tre coppie: il tenant (peso 2), l'applicazione critica (4) e quella bassa (1).
	if onCrit.Percent != 33 || onLow.Percent != 33 {
		t.Fatalf("copertura semplice uguale: %d %d", onCrit.Percent, onLow.Percent)
	}
	if onCrit.WeightedPercent != 4*100/7 || onLow.WeightedPercent != 1*100/7 {
		t.Fatalf("copertura pesata: critica %d, bassa %d", onCrit.WeightedPercent, onLow.WeightedPercent)
	}
}
