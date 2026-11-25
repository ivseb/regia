package itest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/portfolio"
	"regia/internal/platform/auth"
	"regia/internal/platform/calendar"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Budget e costi (piano, passi 11 e 12): riservatezza con la RLS, ore × tariffa con
// validità, import CSV con prova e idempotenza, valute, versioni del budget con il motore
// di approvazione, trasferimenti con audit, earned value e isolamento tra tenant.
func TestFinance(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("fin-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Finanza", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("fio-"+time.Now().Format("150405.000"), ".", "")
	tenB, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, name, roles string }{
		{"sponsor", "Sponsor", "viewer"}, {"fin", "Finanza", "editor,finance"}, {"pmo", "PMO", "editor"}, {"dev", "Sviluppatrice", "editor"},
	} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.name,
			Password: testPassword, Roles: strings.Split(m.roles, ",")}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	fin := login(t, srv.URL, "fin@"+slug+".test", testPassword)
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)
	pmo := login(t, srv.URL, "pmo@"+slug+".test", testPassword)
	dev := login(t, srv.URL, "dev@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	newRequest := func(title string) string {
		r := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": title, "sponsor_id": sponsor.member}, http.StatusCreated)
		return r["key"].(string)
	}
	reqA, reqB, reqC := newRequest("Nuovo CRM"), newRequest("Portale fornitori"), newRequest("Migrazione cloud")

	ov := fin.do("GET", "/api/v1/finance/overview", nil, http.StatusOK)
	cats := map[string]string{}
	for _, c := range ov["categories"].([]any) {
		m := c.(map[string]any)
		cats[m["code"].(string)] = m["id"].(string)
	}
	if ov["settings"].(map[string]any)["base_currency"] != "EUR" || cats["PERS"] == "" || cats["CONS"] == "" {
		t.Fatalf("configurazione iniziale: %v", ov)
	}
	fy := fin.do("POST", "/api/v1/finance/fiscal-years", map[string]any{"name": "2026", "start_date": "2026-01-01", "end_date": "2026-12-31",
		"envelope_cents": 10_000_000}, http.StatusCreated)
	fyID := fy["id"].(string)
	fin.do("POST", "/api/v1/finance/fiscal-years", map[string]any{"name": "2026 bis", "start_date": "2026-06-01", "end_date": "2027-05-31"},
		http.StatusUnprocessableEntity) // si sovrappone

	// -----------------------------------------------------------------
	// Versioni del budget, con il motore di approvazione (fino a 50 k€ decide lo sponsor).
	// -----------------------------------------------------------------
	decide := func(u *user, key, decision, comment string) {
		t.Helper()
		det := u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)
		for _, rq := range det["approvals"].([]any) {
			m := rq.(map[string]any)
			if m["status"] != "pending" {
				continue
			}
			for _, st := range m["steps"].([]any) {
				s := st.(map[string]any)
				if s["can_decide"] == true {
					u.do("POST", "/api/v1/approvals/steps/"+s["id"].(string)+"/decision", map[string]any{"decision": decision, "comment": comment}, http.StatusNoContent)
					return
				}
			}
		}
		t.Fatalf("%s: nessun passo da decidere su %s: %v", u.member, key, det["approvals"])
	}
	approvedBudget := func(key string, lines []map[string]any) map[string]any {
		t.Helper()
		b := fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": key, "fiscal_year_id": fyID, "lines": lines}, http.StatusCreated)
		b = fin.do("POST", "/api/v1/finance/budgets/"+b["id"].(string)+"/submit", nil, http.StatusOK)
		if b["status"] != "submitted" || b["approval_request_id"] == nil {
			t.Fatalf("invio del budget: %v", b)
		}
		decide(sponsor, key, "approved", "")
		return fin.do("GET", "/api/v1/finance/budgets/"+b["id"].(string), nil, http.StatusOK)
	}
	v1 := approvedBudget(reqA, []map[string]any{
		{"cost_category_id": cats["PERS"], "amount_cents": 2_000_000}, {"cost_category_id": cats["CONS"], "amount_cents": 1_000_000, "period": "2026-03"}})
	if v1["status"] != "approved" || v1["total_cents"].(float64) != 3_000_000 || v1["version"].(float64) != 1 {
		t.Fatalf("budget v1: %v", v1)
	}
	fin.do("PUT", "/api/v1/finance/budgets/"+v1["id"].(string)+"/lines", map[string]any{"lines": []any{}}, http.StatusUnprocessableEntity) // approvato: immutabile
	fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": reqA, "fiscal_year_id": fyID}, http.StatusUnprocessableEntity)     // serve una revisione
	v2 := fin.do("POST", "/api/v1/finance/budgets", map[string]any{"element": reqA, "fiscal_year_id": fyID, "kind": "revised"}, http.StatusCreated)
	if v2["total_cents"].(float64) != 3_000_000 || len(v2["lines"].([]any)) != 2 || v2["based_on_id"] != v1["id"] {
		t.Fatalf("revisione copiata: %v", v2)
	}
	v2 = fin.do("PUT", "/api/v1/finance/budgets/"+v2["id"].(string)+"/lines", map[string]any{"lines": []map[string]any{
		{"cost_category_id": cats["PERS"], "amount_cents": 3_000_000}, {"cost_category_id": cats["CONS"], "amount_cents": 1_500_000}}}, http.StatusOK)
	fin.do("POST", "/api/v1/finance/budgets/"+v2["id"].(string)+"/submit", nil, http.StatusOK)
	decide(sponsor, reqA, "rejected", "Troppo alto per il primo semestre")
	v2 = fin.do("GET", "/api/v1/finance/budgets/"+v2["id"].(string), nil, http.StatusOK)
	if v2["status"] != "draft" || v2["decision_note"] != "Troppo alto per il primo semestre" {
		t.Fatalf("revisione respinta: %v", v2)
	}
	fin.do("POST", "/api/v1/finance/budgets/"+v2["id"].(string)+"/submit", nil, http.StatusOK)
	decide(sponsor, reqA, "approved", "")
	versions := fin.do("GET", "/api/v1/finance/budgets?all=true&element="+reqA, nil, http.StatusOK)["items"].([]any)
	got := map[float64]string{}
	for _, it := range versions {
		m := it.(map[string]any)
		got[m["version"].(float64)] = m["kind"].(string) + "/" + m["status"].(string)
	}
	if got[1] != "requested/superseded" || got[2] != "revised/approved" {
		t.Fatalf("versioni: %v", got)
	}
	// La graduatoria si alimenta dall'esercizio: plafond 100 k€ meno 45 k€ approvati.
	fin.do("PUT", "/api/v1/finance/settings", map[string]any{"portfolio_fiscal_year_id": fyID, "portfolio_basis": "residual"}, http.StatusOK)
	var portfolioBudget int64
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select budget_cents from portfolio_settings").Scan(&portfolioBudget)
	}); err != nil || portfolioBudget != 5_500_000 {
		t.Fatalf("budget della graduatoria: %d %v", portfolioBudget, err)
	}

	// -----------------------------------------------------------------
	// Trasferimenti: motivati, con audit; oltre 10 k€ decide il PMO.
	// -----------------------------------------------------------------
	approvedBudget(reqB, []map[string]any{{"cost_category_id": cats["CONS"], "amount_cents": 2_000_000}})
	lineOf := func(key, cat string) string {
		t.Helper()
		bs := fin.do("GET", "/api/v1/finance/budgets?element="+key, nil, http.StatusOK)["items"].([]any)
		for _, b := range bs {
			m := b.(map[string]any)
			if m["status"] != "approved" {
				continue
			}
			for _, l := range fin.do("GET", "/api/v1/finance/budgets/"+m["id"].(string), nil, http.StatusOK)["lines"].([]any) {
				if lm := l.(map[string]any); lm["cost_category_id"] == cats[cat] {
					return lm["id"].(string)
				}
			}
		}
		t.Fatalf("riga %s di %s non trovata", cat, key)
		return ""
	}
	aPers, bCons := lineOf(reqA, "PERS"), lineOf(reqB, "CONS")
	fin.do("POST", "/api/v1/finance/transfers", map[string]any{"from_line_id": aPers, "to_line_id": bCons, "amount_cents": 500_000}, http.StatusUnprocessableEntity)
	fin.do("POST", "/api/v1/finance/transfers", map[string]any{"from_line_id": aPers, "to_line_id": bCons, "amount_cents": 9_000_000, "reason": "Troppo"}, http.StatusUnprocessableEntity)
	tr := fin.do("POST", "/api/v1/finance/transfers", map[string]any{"from_line_id": aPers, "to_line_id": bCons, "amount_cents": 500_000,
		"reason": "Riallocazione"}, http.StatusCreated)
	if tr["status"] != "approved" {
		t.Fatalf("trasferimento sotto soglia: %v", tr)
	}
	// Oltre soglia: serve il ruolo PMO sulla richiesta di partenza.
	if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id)
			select app_tenant(), e.id, r.id, $2 from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join stakeholder_roles r on r.tenant_id = e.tenant_id and r.key = 'pmo' where s.key || '-' || e.number = $1`, reqA, pmo.member)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tr2 := fin.do("POST", "/api/v1/finance/transfers", map[string]any{"from_line_id": aPers, "to_line_id": bCons, "amount_cents": 1_200_000,
		"reason": "Rinforzo del portale"}, http.StatusCreated)
	if tr2["status"] != "pending" || tr2["approval_request_id"] == nil {
		t.Fatalf("trasferimento sopra soglia: %v", tr2)
	}
	decide(pmo, reqA, "approved", "")
	totA := fin.do("GET", "/api/v1/demand/requests/"+reqA+"/finance", nil, http.StatusOK)
	totB := fin.do("GET", "/api/v1/demand/requests/"+reqB+"/finance", nil, http.StatusOK)
	if totA["approved_cents"].(float64) != 2_800_000 || totB["approved_cents"].(float64) != 3_700_000 {
		t.Fatalf("budget dopo i trasferimenti: A %v B %v", totA["approved_cents"], totB["approved_cents"])
	}
	var actions []string
	var reasonHash string
	if err := d.Owner.QueryRow(ctx, `select array_agg(action order by seq), max(payload->>'reason_sha256') from audit_log
		where tenant_id = $1 and entity_id = $2`, ten.TenantID, tr["id"]).Scan(&actions, &reasonHash); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("Riallocazione"))
	if strings.Join(actions, ",") != "budget.transfer,budget.transfer.approved" || reasonHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("audit del trasferimento: %v %s", actions, reasonHash)
	}

	// -----------------------------------------------------------------
	// Ore × tariffa con validità: la tariffa della persona prevale su quella del ruolo.
	// -----------------------------------------------------------------
	fin.do("POST", "/api/v1/finance/rates", map[string]any{"role_key": "backend", "hourly_cents": 5000, "valid_from": "2026-01-01", "valid_to": "2026-06-30"}, http.StatusCreated)
	fin.do("POST", "/api/v1/finance/rates", map[string]any{"role_key": "Backend", "hourly_cents": 6000, "valid_from": "2026-07-01"}, http.StatusCreated)
	fin.do("POST", "/api/v1/finance/rates", map[string]any{"role_key": "backend", "hourly_cents": 7000, "valid_from": "2026-06-01"}, http.StatusUnprocessableEntity)
	personal := fin.do("POST", "/api/v1/finance/rates", map[string]any{"member_id": dev.member, "hourly_cents": 8000, "valid_from": "2026-09-01"}, http.StatusCreated)
	logCost := map[string]float64{}
	for _, wl := range []struct {
		date, role string
		hours      float64
		member     string
	}{{"2026-03-10", "backend", 8, dev.member}, {"2026-08-10", "backend", 8, dev.member}, {"2026-09-15", "backend", 8, dev.member}, {"2026-09-15", "pm", 4, pmo.member}} {
		w := fin.do("POST", "/api/v1/finance/work-logs", map[string]any{"element": reqA, "member_id": wl.member, "work_date": wl.date, "hours": wl.hours,
			"role_key": wl.role}, http.StatusCreated)
		c, _ := w["cost_base_cents"].(float64)
		logCost[wl.date+"/"+wl.role] = c
	}
	if logCost["2026-03-10/backend"] != 40_000 || logCost["2026-08-10/backend"] != 48_000 || logCost["2026-09-15/backend"] != 64_000 || logCost["2026-09-15/pm"] != 0 {
		t.Fatalf("costi delle ore: %v", logCost)
	}
	// La tariffa personale parte un giorno dopo: le ore del 15 settembre tornano alla tariffa di ruolo.
	fin.do("PATCH", "/api/v1/finance/rates/"+personal["id"].(string), map[string]any{"member_id": dev.member, "hourly_cents": 8000, "valid_from": "2026-09-16"}, http.StatusOK)
	var sept float64
	for _, w := range fin.do("GET", "/api/v1/finance/work-logs?element="+reqA, nil, http.StatusOK)["items"].([]any) {
		m := w.(map[string]any)
		if m["work_date"] == "2026-09-15" && m["role_key"] == "backend" {
			sept = m["cost_base_cents"].(float64)
		}
	}
	if sept != 48_000 {
		t.Fatalf("ricalcolo dopo la modifica della tariffa: %v", sept)
	}
	dev.do("GET", "/api/v1/finance/work-logs", nil, http.StatusForbidden)

	// -----------------------------------------------------------------
	// Valute: cambio valido alla data della voce; senza cambio la voce è rifiutata.
	// -----------------------------------------------------------------
	fin.do("PUT", "/api/v1/finance/exchange-rates", map[string]any{"currency": "usd", "valid_on": "2026-01-01", "rate_to_base": 0.9}, http.StatusOK)
	fin.do("PUT", "/api/v1/finance/exchange-rates", map[string]any{"currency": "USD", "valid_on": "2026-06-01", "rate_to_base": 0.92}, http.StatusOK)
	fin.do("PUT", "/api/v1/finance/exchange-rates", map[string]any{"currency": "EUR", "valid_on": "2026-06-01", "rate_to_base": 1}, http.StatusUnprocessableEntity)
	usd := func(date string) float64 {
		c := fin.do("POST", "/api/v1/finance/costs", map[string]any{"element": reqB, "cost_category_id": cats["LIC"], "entry_date": date,
			"amount_cents": 100_000, "currency": "USD", "source": "invoice", "description": "Licenze annuali"}, http.StatusCreated)
		return c["amount_base_cents"].(float64)
	}
	if a, b := usd("2026-03-01"), usd("2026-07-01"); a != 90_000 || b != 92_000 {
		t.Fatalf("conversione USD: %v %v", a, b)
	}
	fin.do("POST", "/api/v1/finance/costs", map[string]any{"element": reqB, "cost_category_id": cats["LIC"], "entry_date": "2026-03-01",
		"amount_cents": 100_000, "currency": "GBP"}, http.StatusUnprocessableEntity)
	totB = fin.do("GET", "/api/v1/demand/requests/"+reqB+"/finance", nil, http.StatusOK)
	if totB["actual_cents"].(float64) != 182_000 || totB["currency"] != "EUR" {
		t.Fatalf("consuntivo convertito: %v", totB)
	}

	// -----------------------------------------------------------------
	// Import CSV: prova senza salvare, errori riga per riga, conferma, idempotenza, annullamento.
	// -----------------------------------------------------------------
	csv := "Data;Elemento;Importo;Categoria;Fornitore;Numero\n" +
		"15/03/2026;" + reqA + ";1.234,56;CONS;Studio Alfa;F-001\n" +
		"16/03/2026;" + reqA + ";500,00;LIC;Beta srl;F-002\n" +
		"17/03/2026;" + reqA + ";abc;CONS;Gamma;F-003\n" +
		"18/03/2026;" + reqA + ";100,00;Consulenze esterne;Delta;\n"
	mapping := map[string]any{"columns": map[string]string{"Data": "entry_date", "Elemento": "element", "Importo": "amount",
		"Categoria": "category", "Fornitore": "supplier", "Numero": "source_ref"}, "date_format": "GG/MM/AAAA", "decimal": ","}
	profile := fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Fatture ERP", "mapping": mapping}, http.StatusCreated)
	before := fin.do("GET", "/api/v1/finance/costs?element="+reqA, nil, http.StatusOK)["count"].(float64)
	run := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "fatture.csv", "csv": csv}, http.StatusCreated)
	if run["status"] != "preview" || run["rows_total"].(float64) != 4 || run["rows_ok"].(float64) != 3 || run["rows_error"].(float64) != 1 {
		t.Fatalf("prova dell'import: %v", run)
	}
	if e := run["errors"].([]any)[0].(map[string]any); e["row"].(float64) != 4 || e["field"] != "amount" {
		t.Fatalf("errore della riga: %v", e)
	}
	if n := fin.do("GET", "/api/v1/finance/costs?element="+reqA, nil, http.StatusOK)["count"].(float64); n != before {
		t.Fatalf("la prova ha scritto voci: %v → %v", before, n)
	}
	fin.do("POST", "/api/v1/finance/imports/"+run["id"].(string)+"/confirm", map[string]any{"csv": csv}, http.StatusUnprocessableEntity) // errori
	fin.do("POST", "/api/v1/finance/imports/"+run["id"].(string)+"/confirm", map[string]any{"csv": csv + "x"}, http.StatusUnprocessableEntity)
	done := fin.do("POST", "/api/v1/finance/imports/"+run["id"].(string)+"/confirm", map[string]any{"csv": csv, "skip_errors": true}, http.StatusOK)
	if done["status"] != "done" || done["rows_ok"].(float64) != 3 {
		t.Fatalf("conferma dell'import: %v", done)
	}
	again := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "fatture.csv", "csv": csv}, http.StatusCreated)
	if again["rows_ok"].(float64) != 0 || again["rows_duplicate"].(float64) != 3 {
		t.Fatalf("seconda prova dello stesso file: %v", again)
	}
	again = fin.do("POST", "/api/v1/finance/imports/"+again["id"].(string)+"/confirm", map[string]any{"csv": csv, "skip_errors": true}, http.StatusOK)
	if again["rows_ok"].(float64) != 0 {
		t.Fatalf("reimport duplicato: %v", again)
	}
	if n := fin.do("GET", "/api/v1/finance/costs?element="+reqA+"&source=import", nil, http.StatusOK)["total_base_cents"].(float64); n != 183_456 {
		t.Fatalf("totale importato: %v", n)
	}
	fin.do("POST", "/api/v1/finance/imports/"+run["id"].(string)+"/rollback", nil, http.StatusOK)
	if n := fin.do("GET", "/api/v1/finance/costs?element="+reqA+"&source=import", nil, http.StatusOK)["count"].(float64); n != 0 {
		t.Fatalf("voci dopo l'annullamento: %v", n)
	}

	// -----------------------------------------------------------------
	// Riservatezza: senza finance.read solo aggregati; senza app.finance la RLS non mostra nulla.
	// -----------------------------------------------------------------
	tot := sponsor.do("GET", "/api/v1/demand/requests/"+reqA+"/finance", nil, http.StatusOK)
	if tot["can_see_details"] != false || tot["approved_cents"].(float64) != 2_800_000 || tot["actual_cents"].(float64) != 136_000 {
		t.Fatalf("totali per chi non è finanza: %v", tot)
	}
	for _, p := range []string{"/api/v1/finance/costs", "/api/v1/finance/rates", "/api/v1/finance/overview", "/api/v1/finance/budgets"} {
		sponsor.do("GET", p, nil, http.StatusForbidden)
		pmo.do("GET", p, nil, http.StatusForbidden)
	}
	count := func(tid string, o db.TenantOpts, table string) int {
		var n int
		if err := d.InTenant(ctx, tid, o, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "select count(*) from "+pgx.Identifier{table}.Sanitize()).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, table := range []string{"rates", "cost_entries"} {
		if n := count(ten.TenantID, db.TenantOpts{}, table); n != 0 {
			t.Fatalf("%s visibili senza app.finance: %d", table, n)
		}
		if n := count(ten.TenantID, db.TenantOpts{Finance: true}, table); n == 0 {
			t.Fatalf("%s non visibili con app.finance", table)
		}
	}
	if n := count(ten.TenantID, db.TenantOpts{}, "cost_rollups"); n == 0 {
		t.Fatal("aggregati non visibili senza app.finance")
	}

	// -----------------------------------------------------------------
	// Earned value: PV dalle date pianificate, EV dalla percentuale, AC dal consuntivo.
	// -----------------------------------------------------------------
	approvedBudget(reqC, []map[string]any{{"cost_category_id": cats["PERS"], "amount_cents": 1_000_000}})
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 5, -1)
	asOf := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, -1) // fine del mese di due mesi fa
	admin.do("PUT", "/api/v1/demand/requests/"+reqC+"/schedule", map[string]any{"start": start.Format(time.DateOnly), "end": end.Format(time.DateOnly)}, http.StatusOK)
	for _, c := range []struct {
		date   time.Time
		amount int
	}{{time.Date(asOf.Year(), asOf.Month(), 5, 0, 0, 0, 0, time.UTC), 300_000}, {calendar.Day(now), 100_000}} {
		fin.do("POST", "/api/v1/finance/costs", map[string]any{"element": reqC, "cost_category_id": cats["CLOUD"], "entry_date": c.date.Format(time.DateOnly),
			"amount_cents": c.amount}, http.StatusCreated)
	}
	dev.do("PUT", "/api/v1/demand/requests/"+reqC+"/ev", map[string]any{"ev_enabled": true}, http.StatusForbidden)
	pr := fin.do("PUT", "/api/v1/demand/requests/"+reqC+"/ev", map[string]any{"ev_enabled": true, "ev_method": "percent_complete", "percent_complete": 40}, http.StatusOK)
	if pr["bac_cents"].(float64) != 1_000_000 || len(pr["curve"].([]any)) < 3 {
		t.Fatalf("progetto con earned value: %v", pr)
	}
	pr = fin.do("POST", "/api/v1/demand/requests/"+reqC+"/ev/snapshots", map[string]any{"as_of": asOf.Format(time.DateOnly)}, http.StatusOK)
	var snap map[string]any
	for _, s := range pr["snapshots"].([]any) {
		if m := s.(map[string]any); m["period_end"] == asOf.Format(time.DateOnly) {
			snap = m
		}
	}
	wantPV := math.Round(1_000_000 * float64(calendar.Workdays(start, asOf)) / float64(calendar.Workdays(start, end)))
	if snap == nil || snap["pv_cents"].(float64) != wantPV || snap["ev_cents"].(float64) != 400_000 || snap["ac_cents"].(float64) != 300_000 {
		t.Fatalf("fotografia EV (PV atteso %v): %v", wantPV, snap)
	}
	if cpi := snap["cpi"].(float64); math.Abs(cpi-4.0/3) > 1e-9 || math.Abs(snap["spi"].(float64)-400_000/wantPV) > 1e-9 || snap["eac_cents"].(float64) != 750_000 {
		t.Fatalf("indicatori EV: %v", snap)
	}
	// Job giornaliero: fotografia del mese corrente con tutto il consuntivo.
	if n, err := finance.RefreshSnapshots(ctx, d, now); err != nil || n < 1 {
		t.Fatalf("job giornaliero: %d %v", n, err)
	}
	pr = fin.do("GET", "/api/v1/demand/requests/"+reqC+"/ev", nil, http.StatusOK)
	if l := pr["latest"].(map[string]any); l["ac_cents"].(float64) != 400_000 || l["period_end"] != time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, -1).Format(time.DateOnly) {
		t.Fatalf("fotografia del giorno: %v", l)
	}
	if tc := sponsor.do("GET", "/api/v1/demand/requests/"+reqC+"/finance", nil, http.StatusOK); tc["ev_enabled"] != true || tc["forecast_source"] != "eac" {
		t.Fatalf("previsione a finire dall'EAC: %v", tc)
	}

	// -----------------------------------------------------------------
	// Isolamento tra tenant.
	// -----------------------------------------------------------------
	adminB.do("GET", "/api/v1/finance/budgets/"+v1["id"].(string), nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/demand/requests/"+reqA+"/finance", nil, http.StatusNotFound)
	if c := adminB.do("GET", "/api/v1/finance/costs", nil, http.StatusOK); c["count"].(float64) != 0 {
		t.Fatalf("voci di un altro tenant: %v", c)
	}
	if n := len(adminB.do("GET", "/api/v1/finance/rates", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("tariffe di un altro tenant: %d", n)
	}
	var leaked int
	if err := d.InTenant(ctx, tenB.TenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select (select count(*) from rates where tenant_id = $1) + (select count(*) from cost_entries where tenant_id = $1)
			+ (select count(*) from budgets where tenant_id = $1) + (select count(*) from cost_rollups where tenant_id = $1)`, ten.TenantID).Scan(&leaked)
	}); err != nil || leaked != 0 {
		t.Fatalf("righe di un altro tenant visibili: %d %v", leaked, err)
	}
}
