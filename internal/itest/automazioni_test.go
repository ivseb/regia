package itest

import (
	"context"
	"encoding/json"
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
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Automazioni (docs/sviluppo/automazioni.md): regole su cambio di campo, a tempo e periodiche,
// con le azioni nuove, l'idempotenza, il tetto per giro e niente cicli.
func TestAutomazioni(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("auto-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Automazioni", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	inTenant := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
	}
	auditCount := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and `+q, append([]any{ten.TenantID}, args...)...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		inTenant(func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) })
		return n
	}

	// Dati del tenant: tre campi della richiesta, un webhook, un tipo semplice e una relazione.
	for _, f := range []map[string]any{
		{"key": "nota_auto", "name": "Nota", "data_type": "string"},
		{"key": "esito_auto", "name": "Esito", "data_type": "string"},
		{"key": "fine_contratto", "name": "Fine del contratto", "data_type": "date"},
	} {
		admin.do("POST", "/api/v1/fields", f, http.StatusCreated)
		admin.do("PUT", "/api/v1/fields/"+f["key"].(string)+"/types/business_request", map[string]any{}, http.StatusOK)
	}
	inTenant(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into webhooks (tenant_id, url, secret_enc, events, name)
			values (app_tenant(), 'https://esempio.test/regia', '\x00', '{}', 'Integrazione')`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into relation_types (tenant_id, key, outward_name, inward_name)
			values (app_tenant(), 'segue', 'segue', 'seguito da')`); err != nil {
			return err
		}
		var typeID string
		if err := tx.QueryRow(ctx, `insert into element_types (tenant_id, key, name, module) values (app_tenant(), 'promemoria', 'Promemoria', 'core')
			returning id`).Scan(&typeID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into space_types (tenant_id, space_id, type_id) select app_tenant(), id, $1 from spaces where key = 'DEM'`, typeID)
		return err
	})

	// Le regole nuove nel processo della richiesta.
	proc := admin.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	def := proc["current"].(map[string]any)
	rules, _ := def["rules"].([]any)
	check := func(extra map[string]any) []string {
		t.Helper()
		dd := map[string]any{}
		for k, v := range def {
			dd[k] = v
		}
		dd["rules"] = append(append([]any{}, rules...), extra)
		out := admin.do("POST", "/api/v1/processes/demand_standard/check", map[string]any{"definition": dd}, http.StatusOK)
		var codes []string
		ps, _ := out["problems"].([]any)
		for _, p := range ps {
			if c, _ := p.(map[string]any)["code"].(string); c != "" {
				codes = append(codes, c)
			}
		}
		return codes
	}
	act := func(kind string, params map[string]any) map[string]any {
		return map[string]any{"kind": kind, "params": params}
	}
	for want, r := range map[string]map[string]any{
		"validation.rule.date_unknown":   {"key": "x", "name": "x", "on": "at:campo.inventato:-3", "actions": []any{act("add_comment", map[string]any{"text": "x"})}},
		"validation.rule.field_unknown":  {"key": "x", "name": "x", "on": "change:inventato", "actions": []any{act("add_comment", map[string]any{"text": "x"})}},
		"validation.rule.event_invalid":  {"key": "x", "name": "x", "on": "every:month", "actions": []any{act("add_comment", map[string]any{"text": "x"})}},
		"validation.rule.param_invalid":  {"key": "x", "name": "x", "on": "create", "actions": []any{act("create_linked", map[string]any{"type": "business_request", "title": "x", "relation": "segue"})}},
		"validation.rule.param_required": {"key": "x", "name": "x", "on": "create", "actions": []any{act("add_comment", map[string]any{})}},
	} {
		if codes := check(r); !strings.Contains(strings.Join(codes, " "), want) {
			t.Errorf("%s: codici %v", want, codes)
		}
	}
	added := []any{
		map[string]any{"key": "su_cambio", "name": "Nota cambiata", "on": "change:nota_auto", "when": "nota_auto: presente", "actions": []any{
			act("set_field", map[string]any{"field": "esito_auto", "value": "visto {{prima.nota_auto}}→{{campo.nota_auto}}"}),
			act("add_comment", map[string]any{"text": "Nota cambiata su {{chiave}}: {{campo.nota_auto}}"}),
			act("webhook", map[string]any{"webhook": "Integrazione"}),
			act("create_linked", map[string]any{"type": "promemoria", "title": "Richiamare per {{chiave}}", "relation": "segue"}),
		}},
		// Catena: l'esito cambiato da su_cambio rimette la nota; quella modifica non fa più scattare nulla.
		map[string]any{"key": "catena", "name": "Esito cambiato", "on": "change:esito_auto", "actions": []any{
			act("set_field", map[string]any{"field": "nota_auto", "value": "rimbalzo"}),
		}},
		map[string]any{"key": "promemoria", "name": "Contratto in scadenza", "on": "at:campo.fine_contratto:-30", "actions": []any{
			act("add_comment", map[string]any{"text": "Il contratto di {{titolo}} scade il {{campo.fine_contratto}}"}),
			act("notify", map[string]any{"message": "Contratto in scadenza: {{titolo}}", "followers": "true"}),
		}},
		map[string]any{"key": "settimanale", "name": "Giro del lunedì", "on": "every:week:1", "when": "nota_auto: presente", "actions": []any{
			act("add_comment", map[string]any{"text": "Giro del {{data}}"}),
		}},
	}
	def["rules"] = append(rules, added...)
	admin.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": def, "notes": "Automazioni"}, http.StatusOK)
	pub := admin.do("POST", "/api/v1/processes/demand_standard/publish", map[string]any{"notes": "Automazioni"}, http.StatusOK)
	_ = pub

	newRequest := func(title string) (string, string) {
		r := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": title, "driver": "regulatory"}, http.StatusCreated)
		return r["id"].(string), r["key"].(string)
	}
	element := func(key string) map[string]any {
		return admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"].(map[string]any)
	}
	patch := func(id string, fields map[string]any) {
		t.Helper()
		el := admin.do("GET", "/api/v1/elements/"+id, nil, http.StatusOK)["element"].(map[string]any)
		admin.do("PATCH", "/api/v1/elements/"+id, map[string]any{"version": el["version"], "fields": fields}, http.StatusOK)
	}
	comments := func(id string) int { return count("select count(*) from comments where element_id = $1", id) }
	ruleEntries := func(id, rule string) int {
		return count("select count(*) from activities where element_id = $1 and kind = 'rule' and field_key = $2", id, rule)
	}

	// --- Cambio di campo, azioni e catena.
	aID, aKey := newRequest("Contratto del fornitore")
	patch(aID, map[string]any{"nota_auto": "nuova"})
	a := element(aKey)
	fields := a["fields"].(map[string]any)
	if fields["esito_auto"] != "visto →nuova" || fields["nota_auto"] != "rimbalzo" {
		t.Fatalf("campi dopo la regola: %v", fields)
	}
	if ruleEntries(aID, "su_cambio") != 4 || ruleEntries(aID, "catena") != 1 {
		t.Fatalf("voci della storia: su_cambio %d, catena %d", ruleEntries(aID, "su_cambio"), ruleEntries(aID, "catena"))
	}
	if comments(aID) != 1 {
		t.Fatalf("commenti: %d", comments(aID))
	}
	// La voce della storia dice regola e versione; l'audit della modifica dice la regola.
	var hist []byte
	inTenant(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select added from activities where element_id = $1 and kind = 'rule' and field_key = 'su_cambio' order by id limit 1`, aID).Scan(&hist)
	})
	var entry map[string]any
	_ = json.Unmarshal(hist, &entry)
	if entry["key"] != "su_cambio" || entry["version"].(float64) < 2 || entry["event"] != "change:nota_auto" || entry["action"] != "set_field" {
		t.Fatalf("voce della storia: %v", entry)
	}
	if n := auditCount(`action = 'element.update' and entity_id = $2 and payload->>'rule' = 'su_cambio'`, aID); n != 1 {
		t.Fatalf("audit della modifica fatta dalla regola: %d", n)
	}
	if n := count(`select count(*) from outbox_events where payload->>'event' = 'automation.rule' and payload->>'element_id' = $1 and payload ? 'webhook_id'`, aID); n != 1 {
		t.Fatalf("evento del webhook: %d", n)
	}
	if n := count(`select count(*) from relations r join elements e on e.id = r.target_id join element_types t on t.id = e.type_id
		where r.source_id = $1 and t.key = 'promemoria' and e.title = $2`, aID, "Richiamare per "+aKey); n != 1 {
		t.Fatalf("elemento collegato: %d", n)
	}
	// Una modifica che non cambia il valore non fa scattare nulla.
	patch(aID, map[string]any{"nota_auto": "rimbalzo"})
	if ruleEntries(aID, "su_cambio") != 4 {
		t.Fatal("stesso valore: la regola non riscatta")
	}

	// --- A tempo e periodiche: il lavoratore, con un «adesso» controllato (il prossimo lunedì).
	rome, _ := time.LoadLocation("Europe/Rome")
	monday := time.Now().In(rome)
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, 1)
	}
	at := func(h int) time.Time { return time.Date(monday.Year(), monday.Month(), monday.Day(), h, 0, 0, 0, rome) }
	fine := monday.AddDate(0, 0, 30).Format(time.DateOnly)
	patch(aID, map[string]any{"fine_contratto": fine})
	bID, bKey := newRequest("Seconda richiesta")
	patch(bID, map[string]any{"nota_auto": "anche qui", "fine_contratto": monday.AddDate(0, 0, 90).Format(time.DateOnly)})
	before, beforeB := comments(aID), comments(bID)

	// Anteprima: nei prossimi 30 giorni la regola «promemoria» scatterebbe su A (se oggi è lunedì,
	// l'occorrenza è oggi; altrimenti cade entro la finestra).
	prev := admin.do("POST", "/api/v1/rules/preview", map[string]any{"type": "business_request", "on": "at:campo.fine_contratto:-30", "days": 30}, http.StatusOK)
	if prev["ok"] != true || prev["matched"].(float64) != 1 || prev["items"].([]any)[0].(map[string]any)["key"] != aKey {
		t.Fatalf("anteprima: %v", prev)
	}
	if p := admin.do("POST", "/api/v1/rules/preview", map[string]any{"type": "business_request", "on": "every:week:1", "condition": "nota_auto: presente"}, http.StatusOK); p["matched"].(float64) != 2 || p["from"] == "" {
		t.Fatalf("anteprima periodica: %v", p)
	}

	// Prima dell'ora del tenant (8:00) non scatta nulla.
	if n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(6)); err != nil || n != 0 {
		t.Fatalf("prima delle 8: %d %v", n, err)
	}
	// Tetto di una esecuzione per giro: tre occorrenze dovute (promemoria su A, settimanale su A e B).
	admin.do("PUT", "/api/v1/automation/settings", map[string]any{"enabled": true, "run_hour": 8, "max_per_run": 1}, http.StatusOK)
	total := 0
	for i := 0; i < 3; i++ {
		n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(12))
		if err != nil || n != 1 {
			t.Fatalf("giro %d: %d %v", i, n, err)
		}
		total += n
	}
	if n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(13)); err != nil || n != 0 {
		t.Fatalf("idempotenza: %d %v", n, err)
	}
	if comments(aID) != before+2 || comments(bID) != beforeB+1 {
		t.Fatalf("commenti dopo i giri: A %d (prima %d), B %d", comments(aID), before, comments(bID))
	}
	if n := count(`select count(*) from rule_runs where status = 'ok'`); n != 3 {
		t.Fatalf("esecuzioni: %d", n)
	}
	if n := count(`select count(*) from notifications where kind = 'automation' and element_id = $1`, aID); n == 0 {
		t.Fatal("la notifica della regola a tempo non è arrivata")
	}
	if n := auditCount(`action = 'automation.run' and payload->>'key' = 'promemoria' and payload->>'occurrence' = $2`, monday.Format(time.DateOnly)); n != 1 {
		t.Fatalf("audit dell'esecuzione a tempo: %d", n)
	}
	runs := admin.do("GET", "/api/v1/processes/demand_standard/rules/promemoria/runs", nil, http.StatusOK)["items"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["element_key"] != aKey || runs[0].(map[string]any)["status"] != "ok" {
		t.Fatalf("ultime esecuzioni: %v", runs)
	}
	// Lunedì dopo: il giro settimanale scatta di nuovo, il promemoria no (occorrenza già fatta).
	if n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(12).AddDate(0, 0, 7)); err != nil || n != 1 {
		t.Fatalf("lunedì dopo: %d %v", n, err)
	}
	_ = bKey
	// Spostando la data, l'occorrenza è nuova e la regola scatta di nuovo.
	patch(aID, map[string]any{"fine_contratto": monday.AddDate(0, 0, 31).Format(time.DateOnly)})
	admin.do("PUT", "/api/v1/automation/settings", map[string]any{"enabled": true, "run_hour": 8, "max_per_run": 50}, http.StatusOK)
	if n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(12).AddDate(0, 0, 8)); err != nil || n < 1 {
		t.Fatalf("data spostata: %d %v", n, err)
	}
	if n := count(`select count(*) from rule_runs where rule_key = 'promemoria' and element_id = $1`, aID); n != 2 {
		t.Fatalf("occorrenze del promemoria: %d", n)
	}
	// Disattivate, non girano.
	admin.do("PUT", "/api/v1/automation/settings", map[string]any{"enabled": false, "run_hour": 8, "max_per_run": 50}, http.StatusOK)
	if n, err := eng.CheckAutomationsTenant(ctx, d, ten.TenantID, at(12).AddDate(0, 0, 14)); err != nil || n != 0 {
		t.Fatalf("disattivate: %d %v", n, err)
	}
}
