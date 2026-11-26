package itest

import (
	"context"
	"log/slog"
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
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Ore dal timesheet via CSV (target work_logs): errori riga per riga in prova, conferma,
// doppioni, periodo chiuso, annullamento, elenchi filtrati per target e isolamento tra tenant.
func TestImportOre(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("ore-"+time.Now().Format("150405.000000"), ".", "")
	other := slug + "-b"
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision, finance.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Ore", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test", AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, name, roles string }{{"sponsor", "Sponsor", "viewer"}, {"fin", "Finanza", "editor,finance"}, {"dev", "Sviluppatrice", "editor"}} {
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
	dev := login(t, srv.URL, "dev@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)
	sponsor := login(t, srv.URL, "sponsor@"+slug+".test", testPassword)

	req := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuovo CRM", "sponsor_id": sponsor.member}, http.StatusCreated)["key"].(string)
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, sql, args...).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Profilo: validazioni e target.
	cols := map[string]string{"Attività": "element", "Chi": "member", "Giorno": "work_date", "Ore": "hours", "Tipo": "activity_type", "Nota": "text", "Cap": "capitalizable", "Riga": "source_ref"}
	mapping := func(c map[string]string) map[string]any {
		return map[string]any{"columns": c, "date_format": "GG/MM/AAAA", "decimal": ","}
	}
	fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(map[string]string{"Giorno": "work_date", "Chi": "member", "Attività": "element"})}, http.StatusUnprocessableEntity) // né ore né minuti
	both := map[string]string{"Attività": "element", "Chi": "member", "Giorno": "work_date", "Ore": "hours", "Min": "minutes"}
	fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(both)}, http.StatusUnprocessableEntity)
	fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(map[string]string{"Giorno": "work_date", "Ore": "hours", "Attività": "element"})}, http.StatusUnprocessableEntity) // manca la persona
	fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "xx", "mapping": mapping(cols)}, http.StatusUnprocessableEntity)
	dev.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(cols)}, http.StatusForbidden)
	profile := fin.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(cols)}, http.StatusCreated)
	if profile["target"] != "work_logs" {
		t.Fatalf("profilo: %v", profile)
	}
	hoursProfiles := fin.do("GET", "/api/v1/finance/import-profiles?target=work_logs", nil, http.StatusOK)
	if len(hoursProfiles["items"].([]any)) != 1 || hoursProfiles["fields"].(map[string]any)["hours"] == nil {
		t.Fatalf("profili delle ore: %v", hoursProfiles)
	}
	if costP := fin.do("GET", "/api/v1/finance/import-profiles", nil, http.StatusOK); len(costP["items"].([]any)) != 0 || costP["fields"].(map[string]any)["amount"] == nil {
		t.Fatalf("i profili dei costi non devono includere le ore: %v", costP)
	}
	if n := adminB.do("GET", "/api/v1/finance/import-profiles?target=work_logs", nil, http.StatusOK)["items"].([]any); len(n) != 0 {
		t.Fatalf("un altro tenant vede il profilo: %v", n)
	}

	// Prova: una riga buona, una con ore decimali con la virgola, e gli errori attesi.
	devMail := "dev@" + slug + ".test"
	csv := "Attività;Chi;Giorno;Ore;Tipo;Nota;Cap;Riga\n" +
		req + ";" + devMail + ";16/03/2026;1,5;design;Analisi;sì;T-1\n" + // buona: 90 minuti
		req + ";" + devMail + ";17/03/2026;0,25;;;no;T-2\n" + // buona: 15 minuti
		"ZZZ-999;" + devMail + ";16/03/2026;2;;;;T-3\n" + // chiave inesistente
		req + ";nessuno@" + slug + ".test;16/03/2026;2;;;;T-4\n" + // non è un membro
		req + ";" + devMail + ";31/02/2026;2;;;;T-5\n" + // data non valida
		req + ";" + devMail + ";18/03/2026;0;;;;T-6\n" + // ore zero
		req + ";" + devMail + ";18/03/2026;-1;;;;T-7\n" + // ore negative
		req + ";" + devMail + ";18/03/2026;abc;;;;T-8\n" + // ore illeggibili
		req + ";" + devMail + ";18/03/2026;25;;;;T-9\n" // più di 24 ore
	before := count("select count(*) from work_logs")
	run := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore.csv", "csv": csv}, http.StatusCreated)
	if run["target"] != "work_logs" || run["status"] != "preview" || run["rows_total"].(float64) != 9 || run["rows_ok"].(float64) != 2 || run["rows_error"].(float64) != 7 {
		t.Fatalf("prova: %v", run)
	}
	errs := map[float64]string{}
	for _, e := range run["errors"].([]any) {
		m := e.(map[string]any)
		errs[m["row"].(float64)] = m["field"].(string)
	}
	want := map[float64]string{4: "element", 5: "member", 6: "work_date", 7: "hours", 8: "hours", 9: "hours", 10: "hours"}
	for row, f := range want {
		if errs[row] != f {
			t.Fatalf("errore della riga %v: %q, atteso %q (%v)", row, errs[row], f, run["errors"])
		}
	}
	prev := run["preview"].([]any)
	if p := prev[0].(map[string]any); p["minutes"].(float64) != 90 || p["person"] != "Sviluppatrice" || p["activity_type"] != "design" {
		t.Fatalf("anteprima: %v", prev)
	}
	if prev[1].(map[string]any)["minutes"].(float64) != 15 {
		t.Fatalf("anteprima: %v", prev)
	}
	if count("select count(*) from work_logs") != before {
		t.Fatal("la prova non deve scrivere ore")
	}
	runID := run["id"].(string)
	// Le prove delle ore non compaiono tra i costi, e viceversa.
	if n := fin.do("GET", "/api/v1/finance/imports", nil, http.StatusOK)["items"].([]any); len(n) != 0 {
		t.Fatalf("elenco dei costi con le ore dentro: %v", n)
	}
	if n := fin.do("GET", "/api/v1/finance/imports?target=work_logs", nil, http.StatusOK)["items"].([]any); len(n) != 1 {
		t.Fatalf("elenco delle ore: %v", n)
	}
	fin.do("GET", "/api/v1/finance/imports?target=boh", nil, http.StatusUnprocessableEntity)
	dev.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore.csv", "csv": csv}, http.StatusForbidden)

	fin.do("POST", "/api/v1/finance/rates", map[string]any{"member_id": dev.member, "hourly_cents": 4000, "valid_from": "2026-01-01"}, http.StatusCreated)
	// Conferma: serve ignorare gli errori; il file deve essere lo stesso.
	fin.do("POST", "/api/v1/finance/imports/"+runID+"/confirm", map[string]any{"csv": csv}, http.StatusUnprocessableEntity)
	fin.do("POST", "/api/v1/finance/imports/"+runID+"/confirm", map[string]any{"csv": csv + "x", "skip_errors": true}, http.StatusUnprocessableEntity)
	done := fin.do("POST", "/api/v1/finance/imports/"+runID+"/confirm", map[string]any{"csv": csv, "skip_errors": true}, http.StatusOK)
	if done["status"] != "done" || done["rows_ok"].(float64) != 2 || done["rows_duplicate"].(float64) != 0 {
		t.Fatalf("conferma: %v", done)
	}
	if n := count("select coalesce(sum(minutes), 0)::int from work_logs where import_run_id::text = $1 and member_id::text = $2 and created_by::text = $3", runID, dev.member, fin.member); n != 105 {
		t.Fatalf("minuti importati: %d", n)
	}
	if n := count("select count(*) from work_logs where import_run_id::text = $1 and capitalizable and text = 'Analisi' and activity_type = 'design'", runID); n != 1 {
		t.Fatalf("campi facoltativi non importati: %d", n)
	}
	if n := count("select count(*) from work_logs where import_run_id::text = $1 and activity_type = 'work' and text is null", runID); n != 1 {
		t.Fatalf("valori di default non applicati: %d", n)
	}
	// Con la tariffa della persona le ore importate hanno il loro costo: 105 min a 40 €/h = 70 €.
	if n := count("select coalesce(sum(amount_base_cents), 0)::int from cost_entries where source = 'work_log' and work_log_id in (select id from work_logs where import_run_id::text = $1)", runID); n != 7000 {
		t.Fatalf("costo delle ore importate: %d", n)
	}
	logs := fin.do("GET", "/api/v1/finance/work-logs?element="+req, nil, http.StatusOK)["items"].([]any)
	if len(logs) != 2 {
		t.Fatalf("ore visibili: %v", logs)
	}

	// Doppione: lo stesso file non duplica.
	again := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore.csv", "csv": csv}, http.StatusCreated)
	if again["rows_ok"].(float64) != 0 || again["rows_duplicate"].(float64) != 2 {
		t.Fatalf("doppioni in prova: %v", again)
	}
	again = fin.do("POST", "/api/v1/finance/imports/"+again["id"].(string)+"/confirm", map[string]any{"csv": csv, "skip_errors": true}, http.StatusOK)
	if again["rows_ok"].(float64) != 0 || again["rows_duplicate"].(float64) != 2 {
		t.Fatalf("doppioni in conferma: %v", again)
	}
	if n := count("select count(*) from work_logs where source_ref is not null"); n != 2 {
		t.Fatalf("ore dopo il doppione: %d", n)
	}

	// Periodo chiuso: la riga è un errore, come per le ore normali.
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"locked_until": "2026-03-31"}, http.StatusOK)
	locked := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore2.csv",
		"csv": "Attività;Chi;Giorno;Ore;Tipo;Nota;Cap;Riga\n" + req + ";" + devMail + ";20/03/2026;1;;;;T-20\n" + req + ";" + devMail + ";06/04/2026;1;;;;T-21\n"}, http.StatusCreated)
	if locked["rows_ok"].(float64) != 1 || locked["rows_error"].(float64) != 1 || locked["errors"].([]any)[0].(map[string]any)["field"] != "work_date" {
		t.Fatalf("periodo chiuso: %v", locked)
	}
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"locked_until": ""}, http.StatusOK)

	// Un altro tenant non vede l'esecuzione e non raggiunge gli elementi di questo.
	adminB.do("GET", "/api/v1/finance/imports/"+runID, nil, http.StatusNotFound)
	adminB.do("POST", "/api/v1/finance/imports/"+runID+"/rollback", nil, http.StatusNotFound)
	if n := adminB.do("GET", "/api/v1/finance/imports?target=work_logs", nil, http.StatusOK)["items"].([]any); len(n) != 0 {
		t.Fatalf("un altro tenant vede le esecuzioni: %v", n)
	}
	profB := adminB.do("POST", "/api/v1/finance/import-profiles", map[string]any{"name": "Ore", "target": "work_logs", "mapping": mapping(cols)}, http.StatusCreated)
	crossed := adminB.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profB["id"], "file_name": "ore.csv", "csv": csv}, http.StatusCreated)
	if crossed["rows_ok"].(float64) != 0 || crossed["rows_error"].(float64) != 9 {
		t.Fatalf("un altro tenant importa ore su elementi e persone altrui: %v", crossed)
	}
	adminB.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore.csv", "csv": csv}, http.StatusUnprocessableEntity) // profilo di un altro tenant: inesistente

	// Annullamento: le ore di quell'esecuzione se ne vanno, le altre restano.
	fin.do("POST", "/api/v1/finance/imports/"+locked["id"].(string)+"/rollback", nil, http.StatusUnprocessableEntity) // solo un import completato
	rb := fin.do("POST", "/api/v1/finance/imports/"+runID+"/rollback", nil, http.StatusOK)
	if rb["status"] != "rolled_back" {
		t.Fatalf("annullamento: %v", rb)
	}
	if n := count("select count(*) from work_logs where source_ref is not null"); n != 0 {
		t.Fatalf("ore rimaste dopo l'annullamento: %d", n)
	}
	if n := count("select count(*) from cost_entries where source = 'work_log'"); n != 0 {
		t.Fatalf("costi rimasti dopo l'annullamento: %d", n)
	}
	fin.do("POST", "/api/v1/finance/imports/"+runID+"/rollback", nil, http.StatusUnprocessableEntity)
	// Dopo l'annullamento lo stesso file si può reimportare.
	re := fin.do("POST", "/api/v1/finance/imports", map[string]any{"profile_id": profile["id"], "file_name": "ore.csv", "csv": csv}, http.StatusCreated)
	if re["rows_ok"].(float64) != 2 || re["rows_duplicate"].(float64) != 0 {
		t.Fatalf("reimport dopo l'annullamento: %v", re)
	}
}
