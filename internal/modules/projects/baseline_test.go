package projects

import (
	"testing"
	"time"
)

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }

func TestBudgetVariance(t *testing.T) {
	base := &Budget{Currency: "EUR", ApprovedCents: i64(100_000)}
	cases := []struct {
		name string
		cur  *Budget
		want *float64
	}{
		{"forecast sopra l'approvato della baseline", &Budget{Currency: "EUR", ApprovedCents: i64(100_000), ForecastCents: i64(106_000)}, ptr(6.0)},
		{"solo approvato, invariato", &Budget{Currency: "EUR", ApprovedCents: i64(100_000)}, ptr(0.0)},
		{"sotto la baseline", &Budget{Currency: "EUR", ForecastCents: i64(90_000)}, ptr(-10.0)},
		{"valuta diversa", &Budget{Currency: "USD", ForecastCents: i64(200_000)}, nil},
		{"senza importi", &Budget{Currency: "EUR"}, nil},
	}
	for _, c := range cases {
		got := budgetVariance(base, c.cur)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: %v, atteso %v", c.name, deref64(got), deref64(c.want))
		}
	}
	if budgetVariance(&Budget{Currency: "EUR", ApprovedCents: i64(0)}, &Budget{Currency: "EUR", ApprovedCents: i64(5)}) != nil {
		t.Error("baseline a zero: lo scostamento non ha senso")
	}
}

func ptr(v float64) *float64 { return &v }
func deref64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// La milestone oltre la tolleranza e la fine del progetto finiscono nei numeri e nel semaforo;
// la milestone raggiunta non conta più.
func TestNumbersAgainstBaseline(t *testing.T) {
	p := &Plan{
		Project:   Head{ID: "p"},
		Tolerance: Tolerance{Days: 5, BudgetPct: 5},
		Baseline:  &BaselineHead{ID: "b", Number: 1, Start: str("2027-01-04"), End: str("2027-01-29")},
		Items: []Item{
			{ID: "wp", Kind: TypeWorkPackage, Start: str("2027-01-04"), End: str("2027-01-15"), BaselineEnd: str("2027-01-15"), StateCategory: "in_progress"},
			{ID: "m1", Kind: TypeMilestone, Start: str("2027-02-12"), End: str("2027-02-12"), BaselineEnd: str("2027-01-29"), StateCategory: "todo"},
			{ID: "m2", Kind: TypeMilestone, Start: str("2027-02-12"), End: str("2027-02-12"), BaselineEnd: str("2027-01-15"), StateCategory: "done"},
		},
	}
	computeItemVariance(p)
	if v := p.Items[1].VarianceDays; v == nil || *v != 10 {
		t.Fatalf("scostamento della milestone: %v", v)
	}
	n := computeNumbers(p, time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC))
	if len(n.OverTolerance) != 1 || n.OverTolerance[0].ID != "m1" || n.OverTolerance[0].DaysLate != 10 {
		t.Fatalf("milestone oltre la tolleranza: %+v", n.OverTolerance)
	}
	if !n.EndOverTolerance || n.VarianceDays == nil || *n.VarianceDays != 10 || n.VariancePct == nil || *n.VariancePct != 52.6 { // 19 giorni lavorativi: il 6 gennaio è festa
		t.Fatalf("fine del progetto: %v %v %v", n.EndOverTolerance, deref64i(n.VarianceDays), deref64(n.VariancePct))
	}
	if n.Signal != "red" || n.Reasons[0] != "over_tolerance" {
		t.Fatalf("semaforo: %s %v", n.Signal, n.Reasons)
	}
	devs := deviationsOf(p, &n)
	if len(devs) != 2 || devs[0].ItemID != "m1" || devs[1].Kind != "project" || devs[1].Days != 10 {
		t.Fatalf("scostamenti per la change request: %+v", devs)
	}
}

func deref64i(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
