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
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Timesheet nel motore delle approvazioni (migrazione 0074): l'invio apre una richiesta, la
// decisione dall'inbox e dal timesheet ha lo stesso effetto, le notifiche arrivano a chi approva
// e alla persona, la delega a tempo vale, il richiamo annulla, una modifica delle regole non
// sposta le settimane già inviate.
func TestTimesheetApprovazioni(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("tsa-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	c, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Timesheet", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"lead", "editor"}, {"dev", "editor"}, {"dev2", "editor"}, {"deleg", "editor"}, {"fin", "editor,finance"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: strings.ToUpper(m.email[:1]) + m.email[1:],
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
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	dev := login(t, srv.URL, "dev@"+slug+".test", testPassword)
	dev2 := login(t, srv.URL, "dev2@"+slug+".test", testPassword)
	deleg := login(t, srv.URL, "deleg@"+slug+".test", testPassword)
	fin := login(t, srv.URL, "fin@"+slug+".test", testPassword)

	// Il team: lead è il responsabile, dev ne fa parte.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	teamID := admin.do("GET", "/api/v1/work/teams", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)["id"].(string)
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": lead.member, "lead": true}, http.StatusOK)
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": dev.member}, http.StatusOK)

	item := dev.do("POST", "/api/v1/work/spaces/WEB/items", map[string]any{"type": "story", "title": "Lavoro"}, http.StatusCreated)["key"].(string)
	mon := time.Now().AddDate(0, 0, -((int(time.Now().Weekday()) + 6) % 7))
	week := func(back int) string { return mon.AddDate(0, 0, -7*back).Format(time.DateOnly) }
	logHours := func(u *user, w string) {
		t.Helper()
		u.do("POST", "/api/v1/work/elements/"+item+"/time", map[string]any{"duration": "2h", "date": w}, http.StatusCreated)
	}
	w1, w2, w3, w4 := week(1), week(2), week(3), week(4)
	for _, w := range []string{w1, w2, w3, w4} {
		logHours(dev, w)
	}
	logHours(fin, w1)
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"require_approval": true, "approver_mode": "team_lead"}, http.StatusOK)

	submit := func(u *user, w string) map[string]any {
		return u.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": w}, http.StatusOK)
	}
	inbox := func(u *user) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, x := range u.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
			if m := x.(map[string]any); m["approval_subject"] == "timesheet" {
				out = append(out, m)
			}
		}
		return out
	}
	notes := func(u *user, event string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, x := range u.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"].([]any) {
			if m := x.(map[string]any); m["kind"] == "timesheet" && m["payload"].(map[string]any)["event"] == event {
				out = append(out, m)
			}
		}
		return out
	}
	forWeek := func(items []map[string]any, member, w string) map[string]any {
		for _, it := range items {
			if it["timesheet_member"] == member && it["timesheet_week"] == w {
				return it
			}
		}
		return nil
	}

	// --- Invio: richiesta nel motore, notifica a chi approva ---------------------------------------
	submit(dev, w1)
	it := forWeek(inbox(lead), dev.member, w1)
	if it == nil || it["title"] != "Dev" || it["element_key"] != "" || it["stage"] != "timesheet" {
		t.Fatalf("la settimana non è tra le approvazioni del responsabile: %v", inbox(lead))
	}
	if len(inbox(dev2)) != 0 || len(inbox(dev)) != 0 || len(inbox(deleg)) != 0 {
		t.Fatal("un estraneo (o la persona stessa) vede la settimana tra le approvazioni")
	}
	if forWeek(inbox(admin), dev.member, w1) == nil {
		t.Fatal("l'amministratore del tempo non la vede tra le approvazioni")
	}
	if n := notes(lead, "submitted"); len(n) != 1 || n[0]["element_key"] != "" {
		t.Fatalf("notifica di invio: %v", n)
	}
	if len(notes(dev2, "submitted"))+len(notes(dev, "submitted")) != 0 {
		t.Fatal("la notifica di invio è arrivata a chi non approva")
	}
	// Più settimane inviate insieme: una sola notifica non letta, con il numero.
	submit(dev, w2)
	if n := notes(lead, "submitted"); len(n) != 1 || n[0]["payload"].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("raggruppamento delle notifiche: %v", n)
	}
	if len(inbox(lead)) != 2 {
		t.Fatalf("approvazioni del responsabile: %v", inbox(lead))
	}
	if p := lead.do("GET", "/api/v1/work/timesheet/pending", nil, http.StatusOK)["items"].([]any); len(p) != 2 {
		t.Fatalf("contatore del menu: %v", p)
	}
	if p := dev2.do("GET", "/api/v1/work/timesheet/pending", nil, http.StatusOK)["items"].([]any); len(p) != 0 {
		t.Fatalf("contatore di un estraneo: %v", p)
	}
	// La persona non decide la propria settimana, né dal timesheet né dal passo del motore.
	dev.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w1, "approve": true}, http.StatusUnprocessableEntity)
	step1 := it["step_id"].(string)
	dev.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	dev2.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)

	// --- Decisione dall'inbox: respinta senza motivo rifiutata -----------------------------------
	lead.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "rejected"}, http.StatusUnprocessableEntity)
	if forWeek(inbox(lead), dev.member, w1) == nil {
		t.Fatal("la decisione rifiutata ha chiuso la richiesta")
	}
	lead.do("POST", "/api/v1/approvals/steps/"+step1+"/decision", map[string]any{"decision": "rejected", "comment": "Manca il giovedì"}, http.StatusNoContent)
	ts := dev.do("GET", "/api/v1/work/timesheet?week="+w1, nil, http.StatusOK)
	if ts["status"] != "rejected" || ts["comment"] != "Manca il giovedì" || ts["editable"] != true {
		t.Fatalf("settimana respinta dall'inbox: %v", ts)
	}
	if forWeek(inbox(lead), dev.member, w1) != nil || len(inbox(lead)) != 1 {
		t.Fatalf("la richiesta decisa è ancora in inbox: %v", inbox(lead))
	}
	if n := notes(dev, "rejected"); len(n) != 1 || n[0]["payload"].(map[string]any)["comment"] != "Manca il giovedì" {
		t.Fatalf("notifica dell'esito alla persona: %v", n)
	}

	// --- Richiamo: annulla la richiesta -----------------------------------------------------------
	submit(dev, w1)
	if forWeek(inbox(lead), dev.member, w1) == nil {
		t.Fatal("la settimana reinviata non è in inbox")
	}
	dev.do("POST", "/api/v1/work/timesheet/withdraw", map[string]any{"week": w1}, http.StatusOK)
	if forWeek(inbox(lead), dev.member, w1) != nil {
		t.Fatal("il richiamo non ha annullato la richiesta")
	}
	assertRequests(t, ctx, d, c.TenantID, dev.member, w1, "cancelled", 1)

	// --- Decisione dal timesheet: stesso effetto --------------------------------------------------
	submit(dev, w1)
	lead.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w1, "approve": false}, http.StatusUnprocessableEntity)
	dev2.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w1, "approve": true}, http.StatusForbidden)
	if r := lead.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w1, "approve": true}, http.StatusOK); r["status"] != "approved" {
		t.Fatalf("approvazione dal timesheet: %v", r)
	}
	if forWeek(inbox(lead), dev.member, w1) != nil {
		t.Fatal("la settimana approvata è ancora in inbox")
	}
	assertRequests(t, ctx, d, c.TenantID, dev.member, w1, "approved", 1)
	if n := notes(dev, "approved"); len(n) != 1 {
		t.Fatalf("notifica di approvazione: %v", n)
	}
	// Riaprire una settimana approvata: solo un amministratore del tempo; la persona è avvisata.
	lead.do("POST", "/api/v1/work/timesheet/reopen", map[string]any{"member": dev.member, "week": w1}, http.StatusForbidden)
	admin.do("POST", "/api/v1/work/timesheet/reopen", map[string]any{"member": dev.member, "week": w1}, http.StatusNoContent)
	if n := notes(dev, "reopened"); len(n) != 1 {
		t.Fatalf("notifica di riapertura: %v", n)
	}

	// --- Delega a tempo ------------------------------------------------------------------------------
	run := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := d.InTenant(ctx, c.TenantID, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
	}
	run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into delegations (tenant_id, from_member_id, to_member_id, valid_from, valid_to)
			values (app_tenant(), $1, $2, now() - interval '1 day', now() + interval '1 day')`, lead.member, deleg.member)
		return err
	})
	dd := forWeek(inbox(deleg), dev.member, w2)
	if dd == nil {
		t.Fatalf("la delega non porta la settimana tra le approvazioni del delegato: %v", inbox(deleg))
	}
	if p := deleg.do("GET", "/api/v1/work/timesheet/pending", nil, http.StatusOK)["items"].([]any); len(p) != 1 {
		t.Fatalf("contatore del delegato: %v", p)
	}
	deleg.do("GET", "/api/v1/work/timesheet?week="+w2+"&member="+dev.member, nil, http.StatusOK)
	deleg.do("POST", "/api/v1/approvals/steps/"+dd["step_id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if ts := dev.do("GET", "/api/v1/work/timesheet?week="+w2, nil, http.StatusOK); ts["status"] != "approved" {
		t.Fatalf("approvazione per delega: %v", ts["status"])
	}
	run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update delegations set valid_from = now() - interval '2 days', valid_to = now() - interval '1 hour'")
		return err
	})
	submit(dev, w3)
	if len(inbox(deleg)) != 0 {
		t.Fatal("la delega scaduta porta ancora le settimane")
	}

	// --- Cambio delle regole: le settimane già inviate restano sugli approvatori di quando sono partite -----
	// w3 è inviata con il responsabile; ora approva dev2.
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"approver_mode": "member", "approver_id": dev2.member}, http.StatusOK)
	if forWeek(inbox(lead), dev.member, w3) == nil || forWeek(inbox(dev2), dev.member, w3) != nil {
		t.Fatal("una settimana già inviata si è spostata sul nuovo approvatore")
	}
	submit(dev, w4)
	if forWeek(inbox(dev2), dev.member, w4) == nil || forWeek(inbox(lead), dev.member, w4) != nil {
		t.Fatalf("la nuova regola non vale per la nuova settimana: lead=%v dev2=%v", inbox(lead), inbox(dev2))
	}
	lead.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w4, "approve": true}, http.StatusForbidden)
	lead.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w3, "approve": true}, http.StatusOK)

	// --- Settimana inviata senza richiesta (caso residuo): si decide comunque, la richiesta si apre al volo ---
	run(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "delete from approval_requests where subject = 'timesheet' and timesheet_week = $1::date", w4)
		return err
	})
	dev2.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w4, "approve": true}, http.StatusOK)

	// --- Chi amministra il tempo approva sempre, mai la propria ---------------------------------------------
	submit(fin, w1)
	fin.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": fin.member, "week": w1, "approve": true}, http.StatusUnprocessableEntity)
	if len(inbox(fin)) != 0 {
		t.Fatal("chi ha inviato vede la propria settimana tra le approvazioni")
	}
	if r := admin.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": fin.member, "week": w1, "approve": true}, http.StatusOK); r["status"] != "approved" {
		t.Fatalf("approvazione dell'amministratore: %v", r)
	}
}

// assertRequests: le richieste di approvazione della settimana, per stato.
func assertRequests(t *testing.T, ctx context.Context, d *db.DB, tenantID, member, week, status string, want int) {
	t.Helper()
	var n int
	err := d.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from approval_requests where subject = 'timesheet' and timesheet_member_id::text = $1
			and timesheet_week = $2::date and status = $3`, member, week, status).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("richieste %s della settimana %s: %d, attese %d", status, week, n, want)
	}
}
