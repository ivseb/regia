package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Budget gerarchico (replica/budget.md, passi 1-3): organizzazione, stanziamenti a cascata,
// registro dei movimenti, visibilità per area con la RLS, ripartizione, fine periodo e
// trasferimenti con approvazione.

type budgetEnv struct {
	t        *testing.T
	d        *db.DB
	slug     string
	tenantID string
	url      string
	// persone: finanza, amministratore, responsabile dell'area IT, di Sviluppo e di Infrastruttura,
	// responsabile di Marketing, PM di un progetto, sponsor, un membro del team senza ruoli di budget.
	admin, fin, area, dev, infra, mk, pm, sponsor, team *user
	// unità
	it, dsv, dinf, marketing string
	fy                       string
}

func newBudgetEnv(t *testing.T, prefix string) *budgetEnv {
	t.Helper()
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Budget " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{
		{"fin", "editor,finance"}, {"area", "viewer"}, {"dev", "viewer"}, {"infra", "viewer"}, {"mk", "viewer"},
		{"pm", "editor"}, {"sponsor", "viewer"}, {"team", "editor"},
	} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.email,
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
	t.Cleanup(func() { srv.Close(); apiSrv.Close() })
	e := &budgetEnv{t: t, d: d, slug: slug, tenantID: ten.TenantID, url: srv.URL}
	l := func(n string) *user { return login(t, srv.URL, n+"@"+slug+".test", testPassword) }
	e.admin, e.fin, e.area, e.dev, e.infra, e.mk = l("admin"), l("fin"), l("area"), l("dev"), l("infra"), l("mk")
	e.pm, e.sponsor, e.team = l("pm"), l("sponsor"), l("team")

	// Organizzazione: Direzione IT (area) con Sviluppo e Infrastruttura; Marketing a parte.
	unit := func(name, code, kind, parent, manager string) string {
		body := map[string]any{"name": name, "code": code, "kind": kind}
		if parent != "" {
			body["parent_id"] = parent
		}
		if manager != "" {
			body["manager_id"] = manager
		}
		return e.admin.do("POST", "/api/v1/org/units", body, http.StatusCreated)["id"].(string)
	}
	e.it = unit("Direzione IT", "IT", "business_unit", "", e.area.member)
	e.dsv = unit("Sviluppo", "IT-DEV", "department", e.it, e.dev.member)
	e.dinf = unit("Infrastruttura", "IT-INF", "department", e.it, e.infra.member)
	e.marketing = unit("Marketing", "MKT", "business_unit", "", e.mk.member)
	return e
}

// asTx esegue fn in una transazione del tenant con il profilo indicato (membro e, se serve, finanza).
func (e *budgetEnv) asTx(member string, finance bool, fn func(tx pgx.Tx)) {
	e.t.Helper()
	if err := e.d.InTenant(context.Background(), e.tenantID, db.TenantOpts{Member: member, Finance: finance}, func(tx pgx.Tx) error {
		fn(tx)
		return nil
	}); err != nil {
		e.t.Fatal(err)
	}
}

// fiscalYear crea un esercizio con plafond e restituisce il suo id.
func (e *budgetEnv) fiscalYear(name, start, end string, envelope int64) string {
	return e.fin.do("POST", "/api/v1/finance/fiscal-years", map[string]any{"name": name, "start_date": start, "end_date": end,
		"envelope_cents": envelope}, http.StatusCreated)["id"].(string)
}

func (e *budgetEnv) periods(fy string) []map[string]any {
	e.t.Helper()
	var out []map[string]any
	for _, p := range e.fin.do("GET", "/api/v1/budget/periods?fiscal_year_id="+fy, nil, http.StatusOK)["items"].([]any) {
		out = append(out, p.(map[string]any))
	}
	return out
}

// tree restituisce i nodi del periodo come li vede l'utente, per etichetta.
func (e *budgetEnv) tree(u *user, period string) map[string]map[string]any {
	e.t.Helper()
	out := map[string]map[string]any{}
	for _, n := range u.do("GET", "/api/v1/budget/tree?period_id="+period, nil, http.StatusOK)["items"].([]any) {
		m := n.(map[string]any)
		out[m["label"].(string)] = m
		if m["kind"] == "element" { // anche per titolo, senza la chiave («DEM-1 Titolo»)
			_, title, _ := strings.Cut(m["label"].(string), " ")
			out[title] = m
		}
	}
	return out
}

func (e *budgetEnv) alloc(u *user, period, parent string, body map[string]any, want int) map[string]any {
	e.t.Helper()
	body["period_id"], body["parent_id"] = period, parent
	return u.do("POST", "/api/v1/budget/allocations", body, want)
}

func i64(v any) int64 { f, _ := v.(float64); return int64(f) }

// Un albero base: plafond 1.000.000 → IT 400.000 (Sviluppo 150.000, Infrastruttura 100.000), Marketing 200.000.
func (e *budgetEnv) baseTree() (period string, ids map[string]string) {
	e.t.Helper()
	fy := e.fiscalYear("2026", "2026-01-01", "2026-12-31", 100_000_000)
	ps := e.periods(fy)
	if len(ps) != 1 {
		e.t.Fatalf("periodi: %v", ps)
	}
	e.fy = fy
	period = ps[0]["id"].(string)
	root := ps[0]["root_id"].(string)
	ids = map[string]string{"root": root}
	ids["it"] = e.alloc(e.fin, period, root, map[string]any{"org_unit_id": e.it, "amount_cents": 40_000_000}, http.StatusCreated)["id"].(string)
	ids["mkt"] = e.alloc(e.fin, period, root, map[string]any{"org_unit_id": e.marketing, "amount_cents": 20_000_000}, http.StatusCreated)["id"].(string)
	ids["dev"] = e.alloc(e.area, period, ids["it"], map[string]any{"org_unit_id": e.dsv, "amount_cents": 15_000_000}, http.StatusCreated)["id"].(string)
	ids["inf"] = e.alloc(e.area, period, ids["it"], map[string]any{"org_unit_id": e.dinf, "amount_cents": 10_000_000}, http.StatusCreated)["id"].(string)
	return period, ids
}

func (e *budgetEnv) request(title string) (key, id string) {
	e.t.Helper()
	r := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": title, "sponsor_id": e.sponsor.member}, http.StatusCreated)
	return r["key"].(string), r["id"].(string)
}

// ---------------------------------------------------------------------
// Organizzazione
// ---------------------------------------------------------------------

func TestBudgetOrganizzazione(t *testing.T) {
	e := newBudgetEnv(t, "borg")
	list := func(u *user, q string) []map[string]any {
		var out []map[string]any
		for _, x := range u.do("GET", "/api/v1/org/units"+q, nil, http.StatusOK)["items"].([]any) {
			out = append(out, x.(map[string]any))
		}
		return out
	}
	// Albero in ordine di visita: padre prima dei figli, con profondità e percorso.
	all := list(e.team, "")
	if len(all) != 4 || all[0]["name"] != "Direzione IT" || all[1]["name"] != "Infrastruttura" || num(all[1]["depth"]) != 1 ||
		all[1]["path"] != "Direzione IT › Infrastruttura" || all[0]["manager_name"] != "area" || num(all[0]["children"]) != 2 {
		t.Fatalf("albero: %v", all)
	}
	// Ricerca per nome e per codice, con limite.
	if r := list(e.team, "?q=mkt"); len(r) != 1 || r[0]["name"] != "Marketing" {
		t.Fatalf("ricerca per codice: %v", r)
	}
	if r := list(e.team, "?q=ra&limit=1"); len(r) != 1 {
		t.Fatalf("limite: %v", r)
	}
	// Solo amministratori e finanza modificano la struttura.
	e.team.do("POST", "/api/v1/org/units", map[string]any{"name": "Abusiva"}, http.StatusForbidden)
	e.area.do("PATCH", "/api/v1/org/units/"+e.dsv, map[string]any{"name": "X"}, http.StatusForbidden)
	e.admin.do("POST", "/api/v1/org/units", map[string]any{"name": "Dup", "code": "IT"}, http.StatusUnprocessableEntity) // codice già usato
	e.admin.do("POST", "/api/v1/org/units", map[string]any{"name": " "}, http.StatusUnprocessableEntity)

	// Spostamento con controllo dei cicli.
	e.admin.do("POST", "/api/v1/org/units/"+e.it+"/move", map[string]any{"parent_id": e.dsv}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/org/units/"+e.it+"/move", map[string]any{"parent_id": e.it}, http.StatusUnprocessableEntity)
	e.admin.do("POST", "/api/v1/org/units/"+e.dinf+"/move", map[string]any{"parent_id": e.marketing}, http.StatusOK)
	if r := list(e.team, "?q=infra"); r[0]["path"] != "Marketing › Infrastruttura" {
		t.Fatalf("dopo lo spostamento: %v", r)
	}
	// Il database rifiuta il ciclo anche se si aggira il servizio.
	if err := e.d.InTenant(context.Background(), e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "update org_units set parent_id = $1 where id = $2", e.dinf, e.marketing)
		return err
	}); err == nil {
		t.Fatal("il trigger doveva rifiutare il ciclo")
	}
	e.admin.do("POST", "/api/v1/org/units/"+e.dinf+"/move", map[string]any{"parent_id": e.it}, http.StatusOK)

	// Archiviazione: non con unità attive sotto.
	e.admin.do("PATCH", "/api/v1/org/units/"+e.it, map[string]any{"archived": true}, http.StatusUnprocessableEntity)
	e.admin.do("PATCH", "/api/v1/org/units/"+e.dinf, map[string]any{"archived": true}, http.StatusOK)
	if r := list(e.team, ""); len(r) != 3 {
		t.Fatalf("archiviata ancora visibile: %v", r)
	}
	if r := list(e.team, "?archived=1"); len(r) != 4 {
		t.Fatalf("con archiviate: %v", r)
	}
	e.admin.do("PATCH", "/api/v1/org/units/"+e.dinf, map[string]any{"archived": false}, http.StatusOK)

	// Persone nelle unità con il ruolo.
	e.admin.do("PUT", "/api/v1/org/units/"+e.dsv+"/members/"+e.pm.member, map[string]any{"role": "finance_ref"}, http.StatusOK)
	e.admin.do("PUT", "/api/v1/org/units/"+e.dsv+"/members/"+e.team.member, map[string]any{"role": "member"}, http.StatusOK)
	e.admin.do("PUT", "/api/v1/org/units/"+e.dsv+"/members/"+e.team.member, map[string]any{"role": "boss"}, http.StatusUnprocessableEntity)
	e.admin.do("PUT", "/api/v1/org/units/"+e.dinf+"/members/"+e.team.member, map[string]any{"role": "member"}, http.StatusOK)
	ms := e.team.do("GET", "/api/v1/org/units/"+e.dsv+"/members", nil, http.StatusOK)["items"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["role"] != "finance_ref" || ms[1].(map[string]any)["role"] != "member" {
		t.Fatalf("persone dell'unità: %v", ms)
	}
	// Cercare una persona mostra le sue unità (anche quella dove è responsabile indicato).
	people := e.team.do("GET", "/api/v1/org/people?q=team", nil, http.StatusOK)["items"].([]any)
	if len(people) != 1 || len(people[0].(map[string]any)["units"].([]any)) != 2 {
		t.Fatalf("persona: %v", people)
	}
	people = e.team.do("GET", "/api/v1/org/people?q=area", nil, http.StatusOK)["items"].([]any)
	if len(people) != 1 || people[0].(map[string]any)["units"].([]any)[0].(map[string]any)["role"] != "manager" {
		t.Fatalf("responsabile indicato: %v", people)
	}
	e.admin.do("DELETE", "/api/v1/org/units/"+e.dinf+"/members/"+e.team.member, nil, http.StatusNoContent)
	e.admin.do("DELETE", "/api/v1/org/units/"+e.dinf+"/members/"+e.team.member, nil, http.StatusNotFound)

	// Funzione SQL del sottoalbero visibile: responsabile dell'area vede area e reparti; il membro semplice niente; il referente il suo.
	count := func(member string) (n int) {
		e.asTx(member, false, func(tx pgx.Tx) {
			if err := tx.QueryRow(context.Background(), "select count(*) from visible_org_units($1::uuid)", member).Scan(&n); err != nil {
				t.Fatal(err)
			}
		})
		return n
	}
	if n := count(e.area.member); n != 3 {
		t.Fatalf("sottoalbero del responsabile dell'area: %d", n)
	}
	if n := count(e.dev.member); n != 1 {
		t.Fatalf("sottoalbero del responsabile di reparto: %d", n)
	}
	if n := count(e.pm.member); n != 1 { // referente finanziario di Sviluppo
		t.Fatalf("sottoalbero del referente: %d", n)
	}
	if n := count(e.team.member); n != 0 { // solo membro: nessun importo
		t.Fatalf("sottoalbero del semplice membro: %d", n)
	}

	// Import CSV: anteprima, errori riga per riga, conferma con la stessa impronta.
	csvBad := "codice;nome;padre;tipo;responsabile\nIT-QA;Qualità;IT;reparto;\nIT-X;Orfano;NON-C-E;reparto;\nIT-Y;Responsabile ignoto;IT;reparto;nessuno@x.test\n"
	pre := e.admin.do("POST", "/api/v1/org/import/preview", map[string]any{"csv": csvBad}, http.StatusOK)
	if num(pre["create"]) != 1 || num(pre["errors"]) != 2 {
		t.Fatalf("anteprima con errori: %v", pre)
	}
	e.admin.do("POST", "/api/v1/org/import/confirm", map[string]any{"csv": csvBad, "sha256": pre["sha256"]}, http.StatusUnprocessableEntity)
	csv := "codice;nome;padre;tipo;responsabile;referente\nIT-QA;Qualità;IT;reparto;dev@" + e.slug + ".test;pm@" + e.slug + ".test\n" +
		"IT-QA-1;Collaudo;IT-QA;reparto;;\nIT-DEV;Sviluppo software;IT;reparto;;\nCC-1;Centro costi;;centro di costo;;\n"
	pre = e.admin.do("POST", "/api/v1/org/import/preview", map[string]any{"csv": csv}, http.StatusOK)
	if num(pre["create"]) != 3 || num(pre["update"]) != 1 || num(pre["errors"]) != 0 || pre["applied"] != false {
		t.Fatalf("anteprima: %v", pre)
	}
	if n := len(list(e.team, "")); n != 4 {
		t.Fatal("l'anteprima non deve scrivere")
	}
	e.admin.do("POST", "/api/v1/org/import/confirm", map[string]any{"csv": csv + "\n", "sha256": pre["sha256"]}, http.StatusUnprocessableEntity) // file diverso
	done := e.admin.do("POST", "/api/v1/org/import/confirm", map[string]any{"csv": csv, "sha256": pre["sha256"]}, http.StatusOK)
	if done["applied"] != true {
		t.Fatalf("conferma: %v", done)
	}
	byName := map[string]map[string]any{}
	for _, u := range list(e.team, "") {
		byName[u["name"].(string)] = u
	}
	if len(byName) != 7 || byName["Collaudo"]["path"] != "Direzione IT › Qualità › Collaudo" || byName["Sviluppo software"]["code"] != "IT-DEV" {
		t.Fatalf("dopo l'import: %v", byName)
	}
	again := e.admin.do("POST", "/api/v1/org/import/preview", map[string]any{"csv": csv}, http.StatusOK)
	if num(again["create"]) != 0 || num(again["errors"]) != 0 {
		t.Fatalf("reimportare non deve duplicare: %v", again)
	}
}

// ---------------------------------------------------------------------
// Stanziamenti, visibilità per area (RLS) e movimenti
// ---------------------------------------------------------------------

func TestBudgetStanziamentiVisibilita(t *testing.T) {
	e := newBudgetEnv(t, "bvis")
	other := newBudgetEnv(t, "bvo")
	period, ids := e.baseTree()

	// Il plafond è la radice: il vecchio envelope resta allineato.
	tree := e.tree(e.fin, period)
	if len(tree) != 5 || i64(tree["Plafond 2026"]["allocated_cents"]) != 100_000_000 || i64(tree["Plafond 2026"]["distributed_cents"]) != 60_000_000 ||
		i64(tree["Plafond 2026"]["reserve_cents"]) != 40_000_000 || i64(tree["Direzione IT"]["reserve_cents"]) != 15_000_000 {
		t.Fatalf("numeri dell'albero: %v", tree)
	}
	var env int64
	e.asTx(e.fin.member, true, func(tx pgx.Tx) {
		_ = tx.QueryRow(context.Background(), "select envelope_cents from fiscal_years limit 1").Scan(&env)
	})
	if env != 100_000_000 {
		t.Fatalf("envelope_cents: %d", env)
	}

	// Un elemento riceve il suo stanziamento sotto Sviluppo; il PM è il suo responsabile.
	key, elID := e.request("Nuovo CRM")
	proj := e.alloc(e.dev, period, ids["dev"], map[string]any{"element_id": elID, "amount_cents": 5_000_000, "nature": "opex"}, http.StatusCreated)["id"].(string)
	if err := e.d.InTenant(context.Background(), e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "update elements set assignee_id = $1 where id = $2", e.pm.member, elID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = key

	labels := func(u *user) []string {
		var out []string
		for _, n := range u.do("GET", "/api/v1/budget/tree?period_id="+period, nil, http.StatusOK)["items"].([]any) {
			out = append(out, n.(map[string]any)["label"].(string))
		}
		return out
	}
	has := func(ls []string, s string) bool {
		for _, l := range ls {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	// finanza: tutto (6 nodi); responsabile dell'area: la sua unità e quelle sotto, e il progetto che ne attinge;
	// responsabile di reparto: il suo ramo; PM: solo il suo elemento; team: nulla.
	if l := labels(e.fin); len(l) != 6 {
		t.Fatalf("finanza: %v", l)
	}
	if l := labels(e.area); len(l) != 4 || has(l, "Marketing") || has(l, "Plafond") || !has(l, "Nuovo CRM") {
		t.Fatalf("responsabile dell'area: %v", l)
	}
	if l := labels(e.dev); len(l) != 2 || !has(l, "Sviluppo") || !has(l, "Nuovo CRM") || has(l, "Infrastruttura") {
		t.Fatalf("responsabile di reparto: %v", l)
	}
	if l := labels(e.infra); len(l) != 1 || l[0] != "Infrastruttura" {
		t.Fatalf("responsabile di Infrastruttura: %v", l)
	}
	if l := labels(e.mk); len(l) != 1 || l[0] != "Marketing" {
		t.Fatalf("responsabile di Marketing: %v", l)
	}
	if l := labels(e.pm); len(l) != 1 || !has(l, "Nuovo CRM") {
		t.Fatalf("PM: %v", l)
	}
	if l := labels(e.team); len(l) != 0 {
		t.Fatalf("team: %v", l)
	}
	// Chi non è finanza non scrive dove non vede o non gestisce.
	e.mk.do("GET", "/api/v1/budget/allocations/"+ids["it"], nil, http.StatusNotFound)
	e.pm.do("GET", "/api/v1/budget/allocations/"+ids["dev"], nil, http.StatusNotFound)
	e.pm.do("PUT", "/api/v1/budget/allocations/"+proj+"/amount", map[string]any{"amount_cents": 9_000_000}, http.StatusForbidden)
	e.team.do("PUT", "/api/v1/budget/allocations/"+ids["dev"]+"/amount", map[string]any{"amount_cents": 1}, http.StatusNotFound)
	e.dev.do("PUT", "/api/v1/budget/allocations/"+ids["dev"]+"/amount", map[string]any{"amount_cents": 20_000_000}, http.StatusForbidden) // il proprio importo lo cambia il padre
	e.mk.do("POST", "/api/v1/budget/allocations", map[string]any{"period_id": period, "parent_id": ids["it"], "org_unit_id": e.dsv, "amount_cents": 1}, http.StatusForbidden)
	e.mk.do("GET", "/api/v1/budget/allocations/"+ids["mkt"]+"/movements", nil, http.StatusOK)
	e.mk.do("GET", "/api/v1/budget/allocations/"+ids["it"]+"/movements", nil, http.StatusNotFound)
	// I movimenti sono visibili come i nodi.
	mv := e.dev.do("GET", "/api/v1/budget/allocations/"+ids["dev"]+"/movements", nil, http.StatusOK)["items"].([]any)
	if len(mv) != 1 || mv[0].(map[string]any)["kind"] != "allocate" || i64(mv[0].(map[string]any)["balance_cents"]) != 15_000_000 {
		t.Fatalf("movimenti: %v", mv)
	}

	// RLS direttamente nel database, senza passare dall'API: i quattro profili.
	count := func(member string, fin bool, table string) (n int) {
		e.asTx(member, fin, func(tx pgx.Tx) {
			if err := tx.QueryRow(context.Background(), "select count(*) from "+table).Scan(&n); err != nil {
				t.Fatal(err)
			}
		})
		return n
	}
	for _, tc := range []struct {
		name   string
		member string
		fin    bool
		nodes  int
		moves  int
	}{
		{"finanza", e.fin.member, true, 6, 6}, {"responsabile area", e.area.member, false, 4, 4}, {"responsabile reparto", e.dev.member, false, 2, 2},
		{"PM", e.pm.member, false, 1, 1}, {"team", e.team.member, false, 0, 0}, {"nessun membro", "", false, 0, 0},
	} {
		if n := count(tc.member, tc.fin, "budget_allocations"); n != tc.nodes {
			t.Errorf("RLS %s: %d nodi, attesi %d", tc.name, n, tc.nodes)
		}
		if n := count(tc.member, tc.fin, "budget_movements"); n != tc.moves {
			t.Errorf("RLS %s: %d movimenti, attesi %d", tc.name, n, tc.moves)
		}
	}
	// Nemmeno la finanza di un altro tenant vede questi nodi.
	if err := other.d.InTenant(context.Background(), other.tenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(context.Background(), "select count(*) from budget_allocations where id = $1", ids["it"]).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("nodo visibile da un altro tenant")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Scrivere un nodo fuori dalla propria area è rifiutato dal database (WITH CHECK).
	e.asTx(e.dev.member, false, func(tx pgx.Tx) {
		ctx := context.Background()
		if _, err := tx.Exec(ctx, "savepoint s"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, `insert into budget_allocations (tenant_id, id, period_id, parent_id, kind, org_unit_id) values (app_tenant(), gen_random_uuid(), $1, $2, 'unit', $3)`,
			period, ids["it"], e.marketing)
		if err == nil {
			t.Error("inserimento fuori area riuscito")
		}
		_, _ = tx.Exec(ctx, "rollback to savepoint s")
	})
	// Le tariffe e i costi da ore restano della sola finanza (decisione del 2026-11-24).
	if n := count(e.area.member, false, "rates"); n != 0 {
		t.Fatalf("tariffe visibili al responsabile: %d", n)
	}
	// Le chiusure sono della finanza.
	if n := count(e.area.member, false, "budget_closures"); n != 0 {
		t.Fatalf("chiusure visibili: %d", n)
	}

	// Il responsabile dell'area ripartisce nel suo sottoalbero; oltre la riserva no.
	e.alloc(e.area, period, ids["it"], map[string]any{"org_unit_id": e.dinf, "amount_cents": 1}, http.StatusUnprocessableEntity) // già presente
	e.area.do("PUT", "/api/v1/budget/allocations/"+ids["inf"]+"/amount", map[string]any{"amount_cents": 25_000_000}, http.StatusOK)
	r := e.area.do("PUT", "/api/v1/budget/allocations/"+ids["inf"]+"/amount", map[string]any{"amount_cents": 25_000_001}, http.StatusUnprocessableEntity)
	_ = r
	// Modifica in linea: la risposta porta il padre con la nuova riserva.
	up := e.area.do("PUT", "/api/v1/budget/allocations/"+ids["inf"]+"/amount", map[string]any{"amount_cents": 20_000_000, "reason": "Rientro"}, http.StatusOK)
	if i64(up["parent"].(map[string]any)["reserve_cents"]) != 5_000_000 || i64(up["node"].(map[string]any)["allocated_cents"]) != 20_000_000 {
		t.Fatalf("riserva del padre dopo la modifica: %v", up)
	}
	// Ripartizione libera disattivata: il responsabile non scrive più, la finanza sì.
	e.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"free_allocation": false}, http.StatusOK)
	e.area.do("PUT", "/api/v1/budget/allocations/"+ids["inf"]+"/amount", map[string]any{"amount_cents": 19_000_000}, http.StatusForbidden)
	e.fin.do("PUT", "/api/v1/budget/allocations/"+ids["inf"]+"/amount", map[string]any{"amount_cents": 19_000_000}, http.StatusOK)
	e.fin.do("PUT", "/api/v1/budget/settings", map[string]any{"free_allocation": true}, http.StatusOK)
	// Il budget del progetto oltre lo stanziamento del nodo non si può ritirare sotto l'impegnato (vedi TestBudgetImpegno).
	if !has(labels(e.fin), "Nuovo CRM") {
		t.Fatal("nodo del progetto assente")
	}
}

func TestBudgetMovimentiImmutabili(t *testing.T) {
	e := newBudgetEnv(t, "bmov")
	period, ids := e.baseTree()
	ctx := context.Background()
	// L'importo corrente è la somma dei movimenti, e ogni modifica ne aggiunge uno.
	e.fin.do("PUT", "/api/v1/budget/allocations/"+ids["mkt"]+"/amount", map[string]any{"amount_cents": 25_000_000, "reason": "Più marketing"}, http.StatusOK)
	e.fin.do("PUT", "/api/v1/budget/allocations/"+ids["mkt"]+"/amount", map[string]any{"amount_cents": 18_000_000, "reason": "Meno marketing"}, http.StatusOK)
	mv := e.fin.do("GET", "/api/v1/budget/allocations/"+ids["mkt"]+"/movements", nil, http.StatusOK)["items"].([]any)
	var sum int64
	for _, m := range mv {
		sum += i64(m.(map[string]any)["amount_cents"])
	}
	if len(mv) != 3 || sum != 18_000_000 || mv[2].(map[string]any)["kind"] != "adjust" || mv[2].(map[string]any)["reason"] != "Meno marketing" ||
		i64(mv[2].(map[string]any)["amount_cents"]) != -7_000_000 {
		t.Fatalf("registro: %v", mv)
	}
	if n := e.tree(e.fin, period)["Marketing"]["allocated_cents"]; i64(n) != 18_000_000 {
		t.Fatalf("stanziato: %v", n)
	}
	// Nessun UPDATE né DELETE: il ruolo applicativo non ha il privilegio e il trigger blocca anche il proprietario.
	e.asTx(e.fin.member, true, func(tx pgx.Tx) {
		for _, q := range []string{"update budget_movements set amount_cents = 1", "delete from budget_movements"} {
			_, _ = tx.Exec(ctx, "savepoint s")
			if _, err := tx.Exec(ctx, q); err == nil {
				t.Errorf("%q riuscito", q)
			}
			_, _ = tx.Exec(ctx, "rollback to savepoint s")
		}
	})
	if _, err := e.d.Owner.Exec(ctx, "update budget_movements set reason = 'x' where tenant_id = $1", e.tenantID); err == nil {
		t.Error("il proprietario ha modificato un movimento")
	}
	// La rete di sicurezza del database: un movimento che farebbe superare il padre fallisce al commit.
	err := e.d.InTenant(ctx, e.tenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into budget_movements (tenant_id, id, period_id, allocation_id, kind, amount_cents, batch_id)
			values (app_tenant(), gen_random_uuid(), $1, $2, 'adjust', 90000000, gen_random_uuid())`, period, ids["dev"])
		return err
	})
	if err == nil {
		t.Fatal("il database doveva rifiutare il superamento del padre")
	}
	err = e.d.InTenant(ctx, e.tenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into budget_movements (tenant_id, id, period_id, allocation_id, kind, amount_cents, batch_id)
			values (app_tenant(), gen_random_uuid(), $1, $2, 'adjust', -20000000, gen_random_uuid())`, period, ids["dev"])
		return err
	})
	if err == nil {
		t.Fatal("il database doveva rifiutare l'importo negativo")
	}
}

// ---------------------------------------------------------------------
// Vincolo «somma dei figli ≤ padre» con richieste concorrenti
// ---------------------------------------------------------------------

func TestBudgetVincoloFigliConcorrenza(t *testing.T) {
	e := newBudgetEnv(t, "bconc")
	period, ids := e.baseTree()
	// Sotto Direzione IT restano 15.000.000 di riserva. Dieci richieste simultanee da 3.000.000 su
	// otto elementi: ne passano al massimo cinque, e la somma dei figli non supera mai il padre.
	var els []string
	for i := 0; i < 10; i++ {
		_, id := e.request(fmt.Sprintf("Progetto %d", i))
		els = append(els, id)
	}
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for _, id := range els {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			u := e.fin
			if len(id)%2 == 0 {
				u = e.area // anche il responsabile scrive, sullo stesso albero
			}
			status, body := rawDo(u, "POST", "/api/v1/budget/allocations", map[string]any{"period_id": period, "parent_id": ids["it"], "element_id": id, "amount_cents": 3_000_000})
			switch status {
			case http.StatusCreated:
				ok.Add(1)
			case http.StatusUnprocessableEntity:
				refused.Add(1)
			default:
				t.Errorf("stato inatteso %d: %s", status, body)
			}
		}(id)
	}
	wg.Wait()
	if ok.Load() != 5 || refused.Load() != 5 {
		t.Fatalf("concorrenza: %d riusciti, %d rifiutati (attesi 5 e 5)", ok.Load(), refused.Load())
	}
	it := e.tree(e.fin, period)["Direzione IT"]
	if i64(it["distributed_cents"]) != 25_000_000+15_000_000-10_000_000+0 && i64(it["distributed_cents"]) > i64(it["allocated_cents"]) {
		t.Fatalf("figli oltre il padre: %v", it)
	}
	if i64(it["distributed_cents"]) != 25_000_000+15_000_000 || i64(it["reserve_cents"]) != 0 {
		t.Fatalf("riserva dopo la concorrenza: %v", it)
	}
	// Stessa prova sulla radice: 40.000.000 di riserva, sei unità nuove che chiedono 10.000.000 ciascuna.
	var units []string
	for i := 0; i < 6; i++ {
		units = append(units, e.admin.do("POST", "/api/v1/org/units", map[string]any{"name": fmt.Sprintf("Unità %d", i)}, http.StatusCreated)["id"].(string))
	}
	ok.Store(0)
	refused.Store(0)
	var wg2 sync.WaitGroup
	for _, u := range units {
		wg2.Add(1)
		go func(u string) {
			defer wg2.Done()
			status, body := rawDo(e.fin, "POST", "/api/v1/budget/allocations", map[string]any{"period_id": period, "parent_id": ids["root"], "org_unit_id": u, "amount_cents": 10_000_000})
			switch status {
			case http.StatusCreated:
				ok.Add(1)
			case http.StatusUnprocessableEntity:
				refused.Add(1)
			default:
				t.Errorf("stato inatteso %d: %s", status, body)
			}
		}(u)
	}
	wg2.Wait()
	root := e.tree(e.fin, period)["Plafond 2026"]
	if ok.Load() != 4 || refused.Load() != 2 || i64(root["reserve_cents"]) != 0 || i64(root["distributed_cents"]) != 100_000_000 {
		t.Fatalf("radice: %d riusciti, %d rifiutati: %v", ok.Load(), refused.Load(), root)
	}
}

// rawDo è user.do senza fermare il test: restituisce stato e corpo (per le prove concorrenti).
func rawDo(u *user, method, path string, body any) (int, string) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, u.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := u.c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}
