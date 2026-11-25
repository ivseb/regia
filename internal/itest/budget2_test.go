package itest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/finance"
	"regia/migrations"
)

// decideInbox decide il primo passo in attesa il cui stage è quello indicato.
func (e *budgetEnv) decideInbox(u *user, stage, decision string) {
	e.t.Helper()
	for _, it := range u.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
		m := it.(map[string]any)
		if m["stage"] == stage {
			u.do("POST", "/api/v1/approvals/steps/"+m["step_id"].(string)+"/decision", map[string]any{"decision": decision, "comment": "ok"}, http.StatusNoContent)
			return
		}
	}
	e.t.Fatalf("nessun passo %q nella posta di %s", stage, u.member)
}

// openPeriods apre i periodi in pianificazione (gli esercizi futuri nascono in pianificazione).
func (e *budgetEnv) openPeriods(ps []map[string]any) {
	e.t.Helper()
	for _, p := range ps {
		if p["status"] == "planning" {
			e.fin.do("POST", "/api/v1/budget/periods/"+p["id"].(string)+"/open", nil, http.StatusOK)
		}
	}
}

func (e *budgetEnv) categories() map[string]string {
	cats := map[string]string{}
	for _, c := range e.fin.do("GET", "/api/v1/finance/overview", nil, http.StatusOK)["categories"].([]any) {
		m := c.(map[string]any)
		cats[m["code"].(string)] = m["id"].(string)
	}
	return cats
}

// allocated restituisce lo stanziato del primo nodo del periodo la cui etichetta contiene label (-1 se manca).
func (e *budgetEnv) allocated(period, label string) int64 {
	for _, n := range e.fin.do("GET", "/api/v1/budget/tree?period_id="+period, nil, http.StatusOK)["items"].([]any) {
		if m := n.(map[string]any); strings.Contains(m["label"].(string), label) {
			return i64(m["allocated_cents"])
		}
	}
	return -1
}

// ---------------------------------------------------------------------
// Collegamento con i budget di progetto: l'approvazione impegna lo stanziamento
// ---------------------------------------------------------------------

func TestBudgetImpegno(t *testing.T) {
	e := newBudgetEnv(t, "bimp")
	period, ids := e.baseTree()
	cat := e.categories()["CONS"]
	key, elID := e.request("Portale fornitori")
	_, otherID := e.request("Altro progetto")
	proj := e.alloc(e.dev, period, ids["dev"], map[string]any{"element_id": elID, "amount_cents": 3_000_000}, http.StatusCreated)["id"].(string)
	other := e.alloc(e.dev, period, ids["dev"], map[string]any{"element_id": otherID, "amount_cents": 1_000_000}, http.StatusCreated)["id"].(string)

	mk := func(amount int64, alloc string, want int) map[string]any {
		return e.fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": key, "fiscal_year_id": e.fy, "allocation_id": alloc,
			"lines": []map[string]any{{"cost_category_id": cat, "amount_cents": amount}}}, want)
	}
	// Il nodo deve essere dell'elemento del budget.
	mk(1_000_000, other, http.StatusUnprocessableEntity)
	// Oltre lo stanziamento non si invia.
	big := mk(4_000_000, proj, http.StatusCreated)
	e.fin.do("POST", "/api/v1/finance/budgets/"+big["id"].(string)+"/submit", nil, http.StatusUnprocessableEntity)
	e.fin.do("PUT", "/api/v1/finance/budgets/"+big["id"].(string)+"/lines", map[string]any{"lines": []map[string]any{{"cost_category_id": cat, "amount_cents": 2_000_000}}}, http.StatusOK)
	b := e.fin.do("POST", "/api/v1/finance/budgets/"+big["id"].(string)+"/submit", nil, http.StatusOK)
	if b["status"] != "submitted" || b["allocation_id"] != proj {
		t.Fatalf("invio: %v", b)
	}
	if n := e.tree(e.fin, period)["Portale fornitori"]; i64(n["committed_cents"]) != 0 {
		t.Fatalf("un budget solo inviato non impegna: %v", n)
	}
	e.decideInbox(e.sponsor, "Budget 2026 · v1", "approved")
	n := e.tree(e.dev, period)["Portale fornitori"]
	if i64(n["committed_cents"]) != 2_000_000 || i64(n["residual_cents"]) != 1_000_000 || i64(n["allocated_cents"]) != 3_000_000 {
		t.Fatalf("impegno dopo l'approvazione: %v", n)
	}
	if bs := n["budgets"].([]any); len(bs) != 1 || bs[0].(map[string]any)["status"] != "approved" {
		t.Fatalf("budget del nodo: %v", n["budgets"])
	}
	// Il nodo dell'area somma l'impegno dei figli.
	if d := e.tree(e.area, period)["Sviluppo"]; i64(d["committed_cents"]) != 2_000_000 {
		t.Fatalf("impegnato di Sviluppo: %v", d)
	}
	// Non si ritira ciò che è impegnato.
	e.dev.do("PUT", "/api/v1/budget/allocations/"+proj+"/amount", map[string]any{"amount_cents": 1_500_000}, http.StatusUnprocessableEntity)
	// Una revisione oltre lo stanziamento si ferma all'invio; alzato lo stanziamento passa e sostituisce l'impegno.
	rev := e.fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": key, "fiscal_year_id": e.fy, "kind": "revised",
		"lines": []map[string]any{{"cost_category_id": cat, "amount_cents": 3_500_000}}}, http.StatusCreated)
	if rev["allocation_id"] != proj {
		t.Fatalf("la revisione eredita il nodo: %v", rev)
	}
	e.fin.do("POST", "/api/v1/finance/budgets/"+rev["id"].(string)+"/submit", nil, http.StatusUnprocessableEntity)
	e.dev.do("PUT", "/api/v1/budget/allocations/"+proj+"/amount", map[string]any{"amount_cents": 4_500_000}, http.StatusOK)
	e.fin.do("POST", "/api/v1/finance/budgets/"+rev["id"].(string)+"/submit", nil, http.StatusOK)
	e.decideInbox(e.sponsor, "Budget 2026 · v2", "approved")
	if n := e.tree(e.dev, period)["Portale fornitori"]; i64(n["committed_cents"]) != 3_500_000 || i64(n["residual_cents"]) != 1_000_000 {
		t.Fatalf("impegno dopo la revisione: %v", n)
	}
	// Speso (consuntivo dai rollup): l'impegnato non scende sotto lo speso, il forecast non sotto l'impegnato.
	if _, err := e.d.Owner.Exec(context.Background(), `insert into cost_rollups (tenant_id, element_id, period, cost_category_id, kind, amount_base_cents)
		values ($1, $2, '2026-03-01', $3, 'actual', 4000000)`, e.tenantID, elID, e.categories()["PERS"]); err != nil {
		t.Fatal(err)
	}
	n = e.tree(e.dev, period)["Portale fornitori"]
	if i64(n["spent_cents"]) != 4_000_000 || i64(n["committed_cents"]) != 4_000_000 || i64(n["residual_cents"]) != 500_000 || i64(n["forecast_cents"]) < 4_000_000 {
		t.Fatalf("speso: %v", n)
	}
	// Scollegare è un'azione della finanza; senza nodo il budget non impegna nulla (come prima).
	e.dev.do("PUT", "/api/v1/finance/budgets/"+rev["id"].(string)+"/allocation", map[string]any{"allocation_id": nil}, http.StatusForbidden)
	e.fin.do("PUT", "/api/v1/finance/budgets/"+rev["id"].(string)+"/allocation", map[string]any{"allocation_id": nil}, http.StatusOK)
	if n := e.tree(e.dev, period)["Portale fornitori"]; i64(n["committed_cents"]) != 4_000_000 { // resta lo speso
		t.Fatalf("dopo lo scollegamento: %v", n)
	}
	// Elenco dei nodi di un elemento, per proporli nel collegamento.
	items := e.fin.do("GET", "/api/v1/budget/element-allocations?element_id="+elID, nil, http.StatusOK)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != proj {
		t.Fatalf("nodi dell'elemento: %v", items)
	}
}

// ---------------------------------------------------------------------
// «Ripartisci»: anteprima e conferma, ogni modo
// ---------------------------------------------------------------------

func TestBudgetRipartisci(t *testing.T) {
	e := newBudgetEnv(t, "brip")
	// Esercizio precedente, per il modo «come il periodo precedente».
	fy25 := e.fiscalYear("2025", "2025-01-01", "2025-12-31", 90_000_000)
	p25 := e.periods(fy25)[0]
	it25 := e.alloc(e.fin, p25["id"].(string), p25["root_id"].(string), map[string]any{"org_unit_id": e.it, "amount_cents": 30_000_000}, http.StatusCreated)["id"].(string)
	e.alloc(e.fin, p25["id"].(string), it25, map[string]any{"org_unit_id": e.dsv, "amount_cents": 12_000_000}, http.StatusCreated)
	e.alloc(e.fin, p25["id"].(string), it25, map[string]any{"org_unit_id": e.dinf, "amount_cents": 8_000_000}, http.StatusCreated)

	period, ids := e.baseTree() // IT 40M: Sviluppo 15M, Infrastruttura 10M
	prev := func(u *user, mode string, body map[string]any, want int) map[string]any {
		body["mode"] = mode
		return u.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute/preview", body, want)
	}
	line := func(pv map[string]any, label string) map[string]any {
		for _, l := range pv["lines"].([]any) {
			if m := l.(map[string]any); m["label"] == label {
				return m
			}
		}
		t.Fatalf("riga %q assente: %v", label, pv)
		return nil
	}
	// Parti uguali: l'anteprima non scrive.
	pv := prev(e.area, "equal", map[string]any{}, http.StatusOK)
	if pv["valid"] != true || i64(line(pv, "Sviluppo")["proposed_cents"]) != 20_000_000 || i64(line(pv, "Infrastruttura")["delta_cents"]) != 10_000_000 ||
		i64(pv["reserve_after"]) != 0 || i64(pv["distributed_after"]) != 40_000_000 {
		t.Fatalf("parti uguali: %v", pv)
	}
	if i64(e.tree(e.fin, period)["Sviluppo"]["allocated_cents"]) != 15_000_000 {
		t.Fatal("l'anteprima ha scritto")
	}
	// Percentuali: il resto resta di riserva; oltre il 100% no.
	pv = prev(e.area, "percent", map[string]any{"targets": []map[string]any{{"allocation_id": ids["dev"], "percent": 60}, {"allocation_id": ids["inf"], "percent": 30}}}, http.StatusOK)
	if i64(line(pv, "Sviluppo")["proposed_cents"]) != 24_000_000 || i64(line(pv, "Infrastruttura")["proposed_cents"]) != 12_000_000 || i64(pv["reserve_after"]) != 4_000_000 {
		t.Fatalf("percentuali: %v", pv)
	}
	prev(e.area, "percent", map[string]any{"targets": []map[string]any{{"allocation_id": ids["dev"], "percent": 70}, {"allocation_id": ids["inf"], "percent": 40}}}, http.StatusUnprocessableEntity)
	prev(e.area, "percent", map[string]any{"targets": []map[string]any{{"allocation_id": ids["dev"]}}}, http.StatusUnprocessableEntity)
	// Superamento: evidenziato nell'anteprima, rifiutato alla conferma.
	pv = prev(e.area, "equal", map[string]any{"total_cents": 50_000_000}, http.StatusOK)
	if pv["valid"] != false || i64(pv["exceeds_cents"]) != 10_000_000 {
		t.Fatalf("superamento: %v", pv)
	}
	e.area.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", map[string]any{"mode": "equal", "total_cents": 50_000_000}, http.StatusUnprocessableEntity)
	// Come il periodo precedente.
	pv = prev(e.area, "previous", map[string]any{}, http.StatusOK)
	if i64(line(pv, "Sviluppo")["proposed_cents"]) != 12_000_000 || i64(line(pv, "Infrastruttura")["proposed_cents"]) != 8_000_000 {
		t.Fatalf("periodo precedente: %v", pv)
	}
	// In proporzione alle richieste in graduatoria dell'area.
	for _, rq := range []struct {
		unit  string
		cents int64
	}{{e.dsv, 3_000_000}, {e.dsv, 3_000_000}, {e.dinf, 2_000_000}} {
		_, id := e.request("Richiesta " + rq.unit[:8])
		if _, err := e.d.Owner.Exec(context.Background(), `update ext_demand set requesting_unit_id = $2, rank = 1, estimate_high_cents = $3 where element_id = $1`, id, rq.unit, rq.cents); err != nil {
			t.Fatal(err)
		}
	}
	pv = prev(e.area, "requests", map[string]any{}, http.StatusOK)
	if i64(line(pv, "Sviluppo")["weight_cents"]) != 6_000_000 || i64(line(pv, "Sviluppo")["proposed_cents"]) != 30_000_000 ||
		i64(line(pv, "Infrastruttura")["proposed_cents"]) != 10_000_000 || line(pv, "Sviluppo")["percent"].(float64) != 75 {
		t.Fatalf("richieste in graduatoria: %v", pv)
	}
	// Conferma: chi non gestisce il nodo non ripartisce; il responsabile sì, e ci sono i movimenti.
	e.mk.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", map[string]any{"mode": "equal"}, http.StatusNotFound)
	e.dev.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", map[string]any{"mode": "equal"}, http.StatusNotFound)
	done := e.area.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", map[string]any{"mode": "requests", "reason": "Piano 2026"}, http.StatusOK)
	if i64(done["distributed_after"]) != 40_000_000 {
		t.Fatalf("conferma: %v", done)
	}
	tr := e.tree(e.fin, period)
	if i64(tr["Sviluppo"]["allocated_cents"]) != 30_000_000 || i64(tr["Infrastruttura"]["allocated_cents"]) != 10_000_000 || i64(tr["Direzione IT"]["reserve_cents"]) != 0 {
		t.Fatalf("dopo la conferma: %v", tr)
	}
	mv := e.fin.do("GET", "/api/v1/budget/allocations/"+ids["dev"]+"/movements", nil, http.StatusOK)["items"].([]any)
	if len(mv) != 2 || mv[1].(map[string]any)["reason"] != "Piano 2026" || i64(mv[1].(map[string]any)["amount_cents"]) != 15_000_000 {
		t.Fatalf("movimenti della ripartizione: %v", mv)
	}
	// Un destinatario nuovo (unità sotto l'area) nasce con la ripartizione; l'uguale divide fra tre.
	qa := e.admin.do("POST", "/api/v1/org/units", map[string]any{"name": "Qualità", "parent_id": e.it}, http.StatusCreated)["id"].(string)
	body := map[string]any{"mode": "equal", "targets": []map[string]any{{"allocation_id": ids["dev"]}, {"allocation_id": ids["inf"]}, {"org_unit_id": qa}}}
	pv = e.area.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute/preview", body, http.StatusOK)
	nq := line(pv, "Qualità")
	if nq["new"] != true || i64(nq["proposed_cents"]) != 13_333_333 || i64(line(pv, "Sviluppo")["proposed_cents"]) != 13_333_334 {
		t.Fatalf("destinatario nuovo: %v", pv)
	}
	e.area.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", body, http.StatusOK)
	if n := e.tree(e.area, period)["Qualità"]; n == nil || i64(n["allocated_cents"]) != 13_333_333 {
		t.Fatalf("nodo creato dalla ripartizione: %v", n)
	}
	// Un'unità fuori dall'area non è un destinatario ammesso.
	e.area.do("POST", "/api/v1/budget/allocations/"+ids["it"]+"/distribute", map[string]any{"mode": "equal", "total_cents": 1,
		"targets": []map[string]any{{"org_unit_id": e.marketing}}}, http.StatusUnprocessableEntity)
}

// ---------------------------------------------------------------------
// Fine periodo: le tre politiche, l'eccezione per nodo e l'annullamento
// ---------------------------------------------------------------------

func TestBudgetChiusura(t *testing.T) {
	e := newBudgetEnv(t, "bchi")
	e.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"period_granularity": "semester"}, http.StatusOK)
	fy := e.fiscalYear("2027", "2027-01-01", "2027-12-31", 90_000_000)
	ps := e.periods(fy)
	if len(ps) != 2 || ps[0]["name"] != "S1 2027" || ps[0]["end_date"] != "2027-06-30" || ps[1]["start_date"] != "2027-07-01" {
		t.Fatalf("periodi semestrali: %v", ps)
	}
	s1, s2 := ps[0]["id"].(string), ps[1]["id"].(string)
	e.openPeriods(ps)
	r1, r2 := ps[0]["root_id"].(string), ps[1]["root_id"].(string)
	it := e.alloc(e.fin, s1, r1, map[string]any{"org_unit_id": e.it, "amount_cents": 30_000_000}, http.StatusCreated)["id"].(string)
	dev := e.alloc(e.fin, s1, it, map[string]any{"org_unit_id": e.dsv, "amount_cents": 10_000_000}, http.StatusCreated)["id"].(string)
	get := e.allocated
	if get(s1, "Plafond") != 45_000_000 || get(s2, "Plafond") != 45_000_000 {
		t.Fatalf("plafond diviso fra i semestri: %d %d", get(s1, "Plafond"), get(s2, "Plafond"))
	}
	e.area.do("POST", "/api/v1/budget/periods/"+s1+"/close", nil, http.StatusForbidden) // solo la finanza chiude

	// 1. Restituzione (predefinita): il non impegnato torna al padre, risalendo fino alla radice.
	pv := e.fin.do("GET", "/api/v1/budget/periods/"+s1+"/close-preview", nil, http.StatusOK)
	if pv["default_policy"] != "return" || i64(pv["return_cents"]) != 40_000_000 || i64(pv["carry_cents"]) != 0 || pv["can_confirm"] != true {
		t.Fatalf("anteprima restituzione: %v", pv)
	}
	if get(s1, "Sviluppo") != 10_000_000 {
		t.Fatal("l'anteprima non deve scrivere")
	}
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/close", map[string]any{}, http.StatusOK)
	if get(s1, "Sviluppo") != 0 || get(s1, "Direzione IT") != 0 || get(s1, "Plafond") != 45_000_000 {
		t.Fatalf("dopo la restituzione: dev %d it %d root %d", get(s1, "Sviluppo"), get(s1, "Direzione IT"), get(s1, "Plafond"))
	}
	e.fin.do("PUT", "/api/v1/budget/allocations/"+dev+"/amount", map[string]any{"amount_cents": 1}, http.StatusUnprocessableEntity) // periodo chiuso
	mv := e.fin.do("GET", "/api/v1/budget/allocations/"+dev+"/movements", nil, http.StatusOK)["items"].([]any)
	if len(mv) != 2 || mv[1].(map[string]any)["kind"] != "return" || i64(mv[1].(map[string]any)["amount_cents"]) != -10_000_000 {
		t.Fatalf("movimenti della restituzione: %v", mv)
	}
	// Annullamento: ripristina tutto con movimenti opposti (il registro non si riscrive).
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/undo-close", nil, http.StatusOK)
	if get(s1, "Sviluppo") != 10_000_000 || get(s1, "Direzione IT") != 30_000_000 {
		t.Fatalf("dopo l'annullamento: dev %d it %d", get(s1, "Sviluppo"), get(s1, "Direzione IT"))
	}
	if mv = e.fin.do("GET", "/api/v1/budget/allocations/"+dev+"/movements", nil, http.StatusOK)["items"].([]any); len(mv) != 3 {
		t.Fatalf("il registro conserva la chiusura e l'annullamento: %v", mv)
	}
	if cl := e.fin.do("GET", "/api/v1/budget/periods/"+s1+"/closures", nil, http.StatusOK)["items"].([]any); len(cl) != 1 || cl[0].(map[string]any)["status"] != "undone" {
		t.Fatalf("chiusure: %v", cl)
	}

	// 2. Riporto con approvazione esplicita: passa allo stesso nodo del periodo successivo.
	e.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"end_policy": "carry"}, http.StatusOK)
	pv = e.fin.do("GET", "/api/v1/budget/periods/"+s1+"/close-preview", nil, http.StatusOK)
	if i64(pv["carry_cents"]) != 45_000_000 || pv["needs_approval"] != true || pv["next_period"] != "S2 2027" {
		t.Fatalf("anteprima riporto: %v", pv)
	}
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/close", map[string]any{}, http.StatusUnprocessableEntity) // manca l'approvazione del riporto
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/close", map[string]any{"approve_carry": true}, http.StatusOK)
	if get(s1, "Plafond") != 0 || get(s1, "Sviluppo") != 0 || get(s2, "Plafond") != 90_000_000 || get(s2, "Direzione IT") != 30_000_000 || get(s2, "Sviluppo") != 10_000_000 {
		t.Fatalf("dopo il riporto: S1 root %d, S2 root %d it %d dev %d", get(s1, "Plafond"), get(s2, "Plafond"), get(s2, "Direzione IT"), get(s2, "Sviluppo"))
	}
	// Annullare riporta il periodo successivo com'era, finché non ha ripartizioni.
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/undo-close", nil, http.StatusOK)
	if get(s2, "Plafond") != 45_000_000 || get(s2, "Sviluppo") != 0 || get(s1, "Plafond") != 45_000_000 || get(s1, "Sviluppo") != 10_000_000 {
		t.Fatalf("dopo l'annullamento del riporto: S2 root %d dev %d", get(s2, "Plafond"), get(s2, "Sviluppo"))
	}
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/close", map[string]any{"approve_carry": true}, http.StatusOK)
	// Con una ripartizione nel periodo successivo non si annulla più.
	e.fin.do("PUT", "/api/v1/budget/allocations/"+r2+"/amount", map[string]any{"amount_cents": 95_000_000}, http.StatusOK) // rettifica del plafond
	e.fin.do("POST", "/api/v1/budget/periods/"+s1+"/undo-close", nil, http.StatusUnprocessableEntity)

	// 3. Decadenza per un nodo con eccezione: il residuo si perde e resta registrato.
	e2 := newBudgetEnv(t, "bchl")
	e2.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"period_granularity": "semester"}, http.StatusOK)
	fyb := e2.fiscalYear("2027", "2027-01-01", "2027-12-31", 90_000_000)
	pb := e2.periods(fyb)
	b1 := pb[0]["id"].(string)
	e2.openPeriods(pb)
	itb := e2.alloc(e2.fin, b1, pb[0]["root_id"].(string), map[string]any{"org_unit_id": e2.it, "amount_cents": 30_000_000}, http.StatusCreated)["id"].(string)
	devb := e2.alloc(e2.fin, b1, itb, map[string]any{"org_unit_id": e2.dsv, "amount_cents": 10_000_000}, http.StatusCreated)["id"].(string)
	e2.area.do("PATCH", "/api/v1/budget/allocations/"+devb, map[string]any{"end_policy": "lapse"}, http.StatusForbidden) // la politica la decide la finanza
	e2.fin.do("PATCH", "/api/v1/budget/allocations/"+devb, map[string]any{"end_policy": "lapse"}, http.StatusOK)
	pv = e2.fin.do("GET", "/api/v1/budget/periods/"+b1+"/close-preview", nil, http.StatusOK)
	if i64(pv["lapse_cents"]) != 10_000_000 || i64(pv["return_cents"]) != 20_000_000 {
		t.Fatalf("anteprima decadenza: %v", pv)
	}
	var devLine map[string]any
	for _, l := range pv["lines"].([]any) {
		if m := l.(map[string]any); m["label"] == "Sviluppo" {
			devLine = m
		}
	}
	if devLine["policy"] != "lapse" || devLine["policy_source"] != "node" || i64(devLine["lapse_cents"]) != 10_000_000 {
		t.Fatalf("riga con eccezione: %v", devLine)
	}
	e2.fin.do("POST", "/api/v1/budget/periods/"+b1+"/start-closing", nil, http.StatusOK)
	e2.area.do("PUT", "/api/v1/budget/allocations/"+devb+"/amount", map[string]any{"amount_cents": 1}, http.StatusUnprocessableEntity) // in chiusura solo la finanza
	e2.fin.do("POST", "/api/v1/budget/periods/"+b1+"/close", map[string]any{}, http.StatusOK)
	if root := e2.allocated(b1, "Plafond"); root != 35_000_000 { // 45 M meno i 10 M decaduti
		t.Fatalf("plafond dopo la decadenza: %d", root)
	}
	lapse := e2.fin.do("GET", "/api/v1/budget/allocations/"+devb+"/movements", nil, http.StatusOK)["items"].([]any)
	if last := lapse[len(lapse)-1].(map[string]any); last["kind"] != "lapse" || i64(last["amount_cents"]) != -10_000_000 {
		t.Fatalf("movimento di decadenza: %v", last)
	}
	// Il vecchio plafond segue le radici.
	e2.asTx(e2.fin.member, true, func(tx pgx.Tx) {
		var env int64
		if err := tx.QueryRow(context.Background(), "select envelope_cents from fiscal_years where id = $1", fyb).Scan(&env); err != nil {
			t.Fatal(err)
		}
		if env != 80_000_000 { // 35 M (S1) + 45 M (S2)
			t.Errorf("envelope dopo la decadenza: %d", env)
		}
	})
}

// ---------------------------------------------------------------------
// Avvisi prima della chiusura
// ---------------------------------------------------------------------

func TestBudgetAvvisiFinePeriodo(t *testing.T) {
	e := newBudgetEnv(t, "bavv")
	fy := e.fiscalYear("2027", "2027-01-01", "2027-12-31", 90_000_000)
	p := e.periods(fy)[0]
	e.openPeriods([]map[string]any{p})
	it := e.alloc(e.fin, p["id"].(string), p["root_id"].(string), map[string]any{"org_unit_id": e.it, "amount_cents": 40_000_000}, http.StatusCreated)["id"].(string)
	e.alloc(e.area, p["id"].(string), it, map[string]any{"org_unit_id": e.dsv, "amount_cents": 10_000_000}, http.StatusCreated)
	ctx := context.Background()
	mine := func() (n int) { // le notifiche di budget di questo tenant (il job gira su tutti i tenant del database di prova)
		e.asTx(e.fin.member, true, func(tx pgx.Tx) {
			_ = tx.QueryRow(ctx, "select count(*) from notifications where kind = 'budget'").Scan(&n)
		})
		return n
	}
	// Troppo presto: nessun avviso (30 giorni di preavviso).
	if _, err := finance.RunBudgetAlerts(ctx, e.d, time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil || mine() != 0 {
		t.Fatalf("avvisi in anticipo: %d %v", mine(), err)
	}
	now := time.Date(2027, 12, 11, 0, 0, 0, 0, time.UTC)                             // 20 giorni alla fine
	if _, err := finance.RunBudgetAlerts(ctx, e.d, now); err != nil || mine() != 2 { // responsabile dell'area e responsabile di Sviluppo
		t.Fatalf("avvisi: %d %v", mine(), err)
	}
	if _, err := finance.RunBudgetAlerts(ctx, e.d, now.AddDate(0, 0, 1)); err != nil || mine() != 2 {
		t.Fatalf("avviso ripetuto: %d %v", mine(), err)
	}
	list := e.area.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"].([]any)
	var found map[string]any
	for _, it := range list {
		if m := it.(map[string]any); m["kind"] == "budget" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("notifica assente: %v", list)
	}
	pl := found["payload"].(map[string]any)
	if i64(pl["residual_cents"]) != 40_000_000 || pl["days"].(float64) != 20 || pl["node"] != "Direzione IT" {
		t.Fatalf("notifica: %v", found)
	}
	// Chi non è responsabile di nulla non riceve avvisi.
	for _, it := range e.team.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"].([]any) {
		if it.(map[string]any)["kind"] == "budget" {
			t.Fatal("avviso a chi non è responsabile")
		}
	}
}

// ---------------------------------------------------------------------
// Trasferimenti tra nodi, con approvazione
// ---------------------------------------------------------------------

func TestBudgetTrasferimenti(t *testing.T) {
	e := newBudgetEnv(t, "btra")
	period, ids := e.baseTree()
	e.request("Aggancio per le approvazioni") // l'approvazione si aggancia a un elemento
	tr := func(u *user, from, to string, cents int64, want int) map[string]any {
		return u.do("POST", "/api/v1/budget/transfers", map[string]any{"from_allocation_id": ids[from], "to_allocation_id": ids[to], "amount_cents": cents, "reason": "Spostamento"}, want)
	}
	amount := func(label string) int64 { return e.allocated(period, label) }

	// Tra fratelli, chiesto dal responsabile del nodo comune: nel suo sottoalbero ripartisce senza approvazione.
	r := tr(e.area, "dev", "inf", 2_000_000, http.StatusCreated)
	if r["status"] != "approved" || r["approval_kind"] != "none" || amount("Sviluppo") != 13_000_000 || amount("Infrastruttura") != 12_000_000 {
		t.Fatalf("trasferimento libero: %v", r)
	}
	// Senza motivazione, oltre la disponibilità, verso sé stesso, con un discendente, chi non li vede: no.
	e.area.do("POST", "/api/v1/budget/transfers", map[string]any{"from_allocation_id": ids["dev"], "to_allocation_id": ids["inf"], "amount_cents": 1}, http.StatusUnprocessableEntity)
	tr(e.area, "dev", "inf", 99_000_000, http.StatusUnprocessableEntity)
	tr(e.area, "dev", "dev", 1, http.StatusUnprocessableEntity)
	tr(e.area, "it", "dev", 1, http.StatusUnprocessableEntity)
	tr(e.mk, "dev", "inf", 1, http.StatusUnprocessableEntity)
	tr(e.team, "dev", "inf", 1, http.StatusUnprocessableEntity)
	tr(e.infra, "inf", "dev", 1, http.StatusUnprocessableEntity) // Infrastruttura non vede Sviluppo

	// Chiesto dalla finanza: lo approva il responsabile del nodo comune più vicino (Direzione IT).
	r = tr(e.fin, "dev", "inf", 1_000_000, http.StatusCreated)
	if r["status"] != "pending" || r["approval_kind"] != "manager" || r["approver_member_id"] != e.area.member || r["approval_request_id"] == nil || amount("Sviluppo") != 13_000_000 {
		t.Fatalf("trasferimento da approvare: %v", r)
	}
	e.decideInbox(e.area, "Trasferimento di stanziamento", "approved")
	if amount("Sviluppo") != 12_000_000 || amount("Infrastruttura") != 13_000_000 {
		t.Fatalf("dopo l'approvazione: %d %d", amount("Sviluppo"), amount("Infrastruttura"))
	}
	mv := e.fin.do("GET", "/api/v1/budget/allocations/"+ids["dev"]+"/movements", nil, http.StatusOK)["items"].([]any)
	last := mv[len(mv)-1].(map[string]any)
	if last["kind"] != "transfer" || i64(last["amount_cents"]) != -1_000_000 || last["approval_request_id"] == nil || last["transfer_id"] == nil {
		t.Fatalf("movimento del trasferimento: %v", last)
	}
	// Respinto: nessun movimento.
	tr(e.fin, "dev", "inf", 500_000, http.StatusCreated)
	e.decideInbox(e.area, "Trasferimento di stanziamento", "rejected")
	if amount("Sviluppo") != 12_000_000 {
		t.Fatal("un trasferimento respinto non sposta nulla")
	}
	st := map[string]int{}
	for _, x := range e.fin.do("GET", "/api/v1/budget/transfers?period_id="+period, nil, http.StatusOK)["items"].([]any) {
		st[x.(map[string]any)["status"].(string)]++
	}
	if st["approved"] != 2 || st["rejected"] != 1 {
		t.Fatalf("elenco dei trasferimenti: %v", st)
	}
	// Chi non vede i nodi non vede nemmeno i trasferimenti.
	if l := e.mk.do("GET", "/api/v1/budget/transfers?period_id="+period, nil, http.StatusOK)["items"].([]any); len(l) != 0 {
		t.Fatalf("trasferimenti visibili a Marketing: %v", l)
	}

	// Tra aree diverse (nodo comune: il plafond, senza responsabile): lo sposta la finanza; il percorso scende da entrambe le parti.
	r = tr(e.fin, "dev", "mkt", 1_000_000, http.StatusCreated)
	if r["status"] != "approved" || amount("Sviluppo") != 11_000_000 || amount("Direzione IT") != 39_000_000 || amount("Marketing") != 21_000_000 || amount("Plafond 2026") != 100_000_000 {
		t.Fatalf("trasferimento fra aree: %v (dev %d it %d mkt %d)", r, amount("Sviluppo"), amount("Direzione IT"), amount("Marketing"))
	}

	// Sopra la soglia decide l'organo di governance indicato.
	e.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"transfer_threshold_cents": 1_000_000, "transfer_body_kind": "investment_committee"}, http.StatusOK)
	r = tr(e.area, "inf", "dev", 3_000_000, http.StatusCreated) // anche il responsabile del nodo comune: sopra soglia decide l'organo
	if r["status"] != "pending" || r["approval_kind"] != "body" || amount("Sviluppo") != 11_000_000 {
		t.Fatalf("sopra soglia: %v", r)
	}
	e.decideInbox(e.admin, "Trasferimento di stanziamento", "approved")
	if amount("Sviluppo") != 14_000_000 || amount("Infrastruttura") != 10_000_000 {
		t.Fatalf("dopo l'organo: %d %d", amount("Sviluppo"), amount("Infrastruttura"))
	}
	// Sotto soglia resta libero. Un trasferimento in attesa tiene ferma la disponibilità.
	tr(e.area, "dev", "inf", 500_000, http.StatusCreated)
	tr(e.fin, "dev", "inf", 13_000_000, http.StatusCreated)
	tr(e.fin, "dev", "inf", 1_000_000, http.StatusUnprocessableEntity)
}

// ---------------------------------------------------------------------
// Migrazione del plafond
// ---------------------------------------------------------------------

func TestBudgetMigrazionePlafond(t *testing.T) {
	e := newBudgetEnv(t, "bmig")
	ctx := context.Background()
	// Un esercizio «vecchio»: plafond ma nessun periodo né radice, come prima della migrazione.
	var fy string
	if err := e.d.Owner.QueryRow(ctx, `insert into fiscal_years (tenant_id, id, name, start_date, end_date, status, envelope_cents)
		values ($1, gen_random_uuid(), 'Vecchio', '2020-01-01', '2020-12-31', 'closed', 55000000) returning id::text`, e.tenantID).Scan(&fy); err != nil {
		t.Fatal(err)
	}
	b, err := migrations.FS.ReadFile("0050_budget_gerarchico.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	i := strings.Index(sql, "insert into budget_periods")
	if i < 0 {
		t.Fatal("blocco di migrazione dei dati non trovato")
	}
	stmts := strings.Split(sql[i:], ";\n")
	for pass := 0; pass < 2; pass++ { // la seconda volta non deve duplicare nulla
		for _, st := range stmts {
			if strings.TrimSpace(st) == "" {
				continue
			}
			if _, err := e.d.Owner.Exec(ctx, st); err != nil {
				t.Fatalf("migrazione: %v\n%s", err, st)
			}
		}
	}
	var periods, roots, moves int
	var amount int64
	var status string
	if err := e.d.Owner.QueryRow(ctx, `select (select count(*) from budget_periods where fiscal_year_id = $1::uuid),
		(select count(*) from budget_allocations a join budget_periods p on p.id = a.period_id where p.fiscal_year_id = $1::uuid and a.kind = 'root'),
		(select count(*) from budget_movements m join budget_periods p on p.id = m.period_id where p.fiscal_year_id = $1::uuid and m.kind = 'initial'),
		(select coalesce(sum(m.amount_cents), 0) from budget_movements m join budget_periods p on p.id = m.period_id where p.fiscal_year_id = $1::uuid),
		(select status from budget_periods where fiscal_year_id = $1::uuid)`, fy).Scan(&periods, &roots, &moves, &amount, &status); err != nil {
		t.Fatal(err)
	}
	if periods != 1 || roots != 1 || moves != 1 || amount != 55_000_000 || status != "closed" {
		t.Fatalf("plafond migrato: periodi %d radici %d movimenti %d importo %d stato %s", periods, roots, moves, amount, status)
	}
	// La finanza lo vede come radice dell'albero e l'esercizio resta invariato.
	ps := e.periods(fy)
	tr := e.tree(e.fin, ps[0]["id"].(string))
	if i64(tr["Plafond Vecchio"]["allocated_cents"]) != 55_000_000 {
		t.Fatalf("radice migrata: %v", tr)
	}
}
