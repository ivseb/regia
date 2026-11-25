package itest

import (
	"net/http"
	"testing"
)

// Opzioni di collegamento di un budget di progetto a un nodo: percorso, disponibile, quanto ne usa il budget,
// visibilità per area (RLS) e nodo collegato non visibile.
func TestBudgetOpzioniCollegamento(t *testing.T) {
	e := newBudgetEnv(t, "bopz")
	period, ids := e.baseTree()
	cat := e.categories()["CONS"]
	key, elID := e.request("Portale clienti")
	proj := e.alloc(e.dev, period, ids["dev"], map[string]any{"element_id": elID, "amount_cents": 3_000_000}, http.StatusCreated)["id"].(string)

	opts := func(u *user, q string) map[string]any {
		return u.do("GET", "/api/v1/budget/element-allocations?"+q, nil, http.StatusOK)
	}
	items := func(r map[string]any) []any { return r["items"].([]any) }

	// La finanza (anche per chiave) e il responsabile di Sviluppo vedono il nodo, con percorso e disponibile.
	for _, u := range []*user{e.fin, e.dev} {
		r := opts(u, "element_id="+key+"&fiscal_year_id="+e.fy)
		if len(items(r)) != 1 {
			t.Fatalf("opzioni: %v", r)
		}
		o := items(r)[0].(map[string]any)
		path := o["path"].([]any)
		if o["id"] != proj || i64(o["allocated_cents"]) != 3_000_000 || i64(o["available_cents"]) != 3_000_000 || o["linked"] != false ||
			o["period_status"] != "open" {
			t.Fatalf("opzione: %v", o)
		}
		// La finanza vede tutto il percorso; il responsabile di Sviluppo parte dal suo nodo (la RLS nasconde i padri).
		if u == e.fin && (len(path) != 3 || path[0] != "Plafond" || path[1] != "Direzione IT" || path[2] != "Sviluppo") {
			t.Fatalf("percorso della finanza: %v", path)
		}
		if u == e.dev && (len(path) != 1 || path[0] != "Sviluppo") {
			t.Fatalf("percorso del responsabile: %v", path)
		}
	}
	// Chi non è dell'area non vede il nodo: nessuna opzione.
	for _, u := range []*user{e.infra, e.mk} {
		if r := opts(u, "element_id="+elID); len(items(r)) != 0 {
			t.Fatalf("il nodo è visibile a chi non è dell'area: %v", r)
		}
	}
	// Un esercizio diverso non ha opzioni.
	if r := opts(e.fin, "element_id="+key+"&fiscal_year_id=00000000-0000-0000-0000-000000000000"); len(items(r)) != 0 {
		t.Fatalf("esercizio estraneo: %v", r)
	}

	// Con il budget: il nodo collegato, il suo totale nel periodo, il disponibile.
	b := e.fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": key, "fiscal_year_id": e.fy, "allocation_id": proj,
		"lines": []map[string]any{{"cost_category_id": cat, "amount_cents": 1_200_000}}}, http.StatusCreated)
	r := opts(e.fin, "budget_id="+b["id"].(string))
	o := items(r)[0].(map[string]any)
	if r["linked_id"] != proj || r["linked_hidden"] != false || o["linked"] != true || i64(o["mine_cents"]) != 1_200_000 || i64(o["available_cents"]) != 3_000_000 {
		t.Fatalf("opzioni con il budget: %v", r)
	}
	e.fin.do("POST", "/api/v1/finance/budgets/"+b["id"].(string)+"/submit", nil, http.StatusOK)
	e.decideInbox(e.sponsor, "Budget 2026 · v1", "approved")
	// Il budget approvato non conta come «altri» per se stesso.
	o = items(opts(e.fin, "budget_id="+b["id"].(string)))[0].(map[string]any)
	if i64(o["others_cents"]) != 0 || i64(o["available_cents"]) != 3_000_000 {
		t.Fatalf("disponibile con il budget approvato: %v", o)
	}
	// Scollegato: il nodo si propone ancora, ma non è più «collegato».
	e.fin.do("PUT", "/api/v1/finance/budgets/"+b["id"].(string)+"/allocation", map[string]any{"allocation_id": nil}, http.StatusOK)
	r = opts(e.fin, "budget_id="+b["id"].(string))
	if r["linked_id"] != nil || items(r)[0].(map[string]any)["linked"] != false {
		t.Fatalf("dopo lo scollegamento: %v", r)
	}
	// Il nodo del progetto elenca il budget (chiave, impegnato) a chi lo vede.
	e.fin.do("PUT", "/api/v1/finance/budgets/"+b["id"].(string)+"/allocation", map[string]any{"allocation_id": proj}, http.StatusOK)
	n := e.tree(e.dev, period)["Portale clienti"]
	bs := n["budgets"].([]any)
	if len(bs) != 1 || bs[0].(map[string]any)["element_key"] != key || i64(bs[0].(map[string]any)["committed_cents"]) != 1_200_000 {
		t.Fatalf("budget del nodo: %v", n["budgets"])
	}
}
