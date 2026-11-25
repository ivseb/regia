package itest

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Tempo e team (piano, passo 18): timesheet settimanale con celle, copia, invio e approvazione,
// periodo chiuso; report per persona e spazio con riservatezza dei costi; comandi in blocco con
// anteprima, blocchi motivati e annullamento; membri dei team con capacità e assenze.
func TestTempoETeam(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("tmp-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Tempo", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("tmo-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"lead", "editor"}, {"dev", "editor"}, {"dev2", "editor"}, {"fin", "editor,finance"}} {
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
	fin := login(t, srv.URL, "fin@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	// --- Team: persone, ruolo, capacità e assenze ---------------------------------------------
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	teams := admin.do("GET", "/api/v1/work/teams", nil, http.StatusOK)["items"].([]any)
	if len(teams) != 1 {
		t.Fatalf("team: %v", teams)
	}
	teamID := teams[0].(map[string]any)["id"].(string)
	dev.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": dev.member}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": lead.member, "lead": true, "role": "analista"}, http.StatusOK)
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": dev.member, "role": "backend", "allocation_pct": 50}, http.StatusOK)
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": dev2.member, "weekly_hours": 20}, http.StatusOK)
	// Il responsabile gestisce il proprio team.
	lead.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": dev2.member, "weekly_hours": 30}, http.StatusOK)
	teams = dev.do("GET", "/api/v1/work/teams", nil, http.StatusOK)["items"].([]any)
	tv := teams[0].(map[string]any)
	if len(tv["members"].([]any)) != 3 || tv["lead_id"] != lead.member {
		t.Fatalf("membri: %v", tv)
	}
	// 40 (responsabile, 100%) + 20 (dev, 50% di 40) + 30 (dev2) = 90 ore a settimana.
	if tv["hours"].(float64) != 90 {
		t.Fatalf("capacità del team: %v", tv["hours"])
	}
	admin.do("PUT", "/api/v1/work/teams/"+teamID+"/members", map[string]any{"member_id": "00000000-0000-0000-0000-000000000000"}, http.StatusUnprocessableEntity)
	if b := adminB.do("GET", "/api/v1/work/teams", nil, http.StatusOK)["items"]; len(b.([]any)) != 0 {
		t.Fatalf("team di un altro tenant: %v", b)
	}
	// La scheda Team in Architettura vede la capacità aggiornata (giorni di otto ore: 90 / 8 = 11,25).
	found := false
	for _, x := range admin.do("GET", "/api/v1/teams", nil, http.StatusOK)["items"].([]any) {
		if m := x.(map[string]any); m["name"] == "Web" {
			found = m["capacity_days_week"].(float64) > 11.2 && m["capacity_days_week"].(float64) < 11.3
		}
	}
	if !found {
		t.Fatal("capacità del team non riflessa nella scheda Team")
	}

	// --- Elementi di lavoro ---------------------------------------------------------------------
	story := func(title string, extra map[string]any) map[string]any {
		body := map[string]any{"type": "story", "title": title}
		for k, v := range extra {
			body[k] = v
		}
		return dev.do("POST", "/api/v1/work/spaces/WEB/items", body, http.StatusCreated)
	}
	s1 := story("Login con SPID", map[string]any{"fields": map[string]any{"punti": 3, "stima": 480}})
	s2 := story("Elenco contratti", nil)
	s3 := story("Download fatture", nil)
	k1, k2, k3 := s1["key"].(string), s2["key"].(string), s3["key"].(string)

	// --- Timesheet -------------------------------------------------------------------------------
	today := time.Now()
	mon := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	w1 := mon.AddDate(0, 0, -7).Format(time.DateOnly) // settimana scorsa
	w2 := mon.AddDate(0, 0, -14).Format(time.DateOnly)
	day := func(week string, i int) string {
		t0, _ := time.Parse(time.DateOnly, week)
		return t0.AddDate(0, 0, i).Format(time.DateOnly)
	}
	cell := func(el, date, dur string) map[string]any {
		return map[string]any{"element": el, "date": date, "duration": dur}
	}
	cells := func(u *user, week string, status int, cs ...map[string]any) map[string]any {
		return u.do("PUT", "/api/v1/work/timesheet/cells", map[string]any{"week": week, "cells": cs}, status)
	}
	ts := cells(dev, w2, http.StatusOK, cell(k1, day(w2, 0), "1h30"), cell(k1, day(w2, 1), "2"), cell(k2, day(w2, 0), "1,5"), cell(k2, day(w2, 2), "45m"))
	// 1h30 + 2h + 1,5h + 45m = 5h45 = 345 minuti, su due righe.
	if ts["total"].(float64) != 345 || len(ts["rows"].([]any)) != 2 {
		t.Fatalf("timesheet: %v", ts)
	}
	// Riscrivere la cella la aggiorna (non la duplica); «0» la svuota.
	ts = cells(dev, w2, http.StatusOK, cell(k1, day(w2, 0), "2h"), cell(k2, day(w2, 2), "0"))
	if ts["total"].(float64) != 120+120+90 || len(ts["rows"].([]any)) != 2 {
		t.Fatalf("timesheet dopo la modifica: %v", ts)
	}
	cells(dev, w2, http.StatusUnprocessableEntity, cell(k1, day(w2, 0), "tanto"))
	cells(dev, w2, http.StatusUnprocessableEntity, cell(k1, day(w2, 0), "25"))
	cells(dev, w2, http.StatusUnprocessableEntity, cell(k1, mon.AddDate(0, 0, 5).Format(time.DateOnly), "1"))
	// Tetto giornaliero (16 ore di default): la somma di tutti gli elementi del giorno.
	cells(dev, w2, http.StatusUnprocessableEntity, cell(k3, day(w2, 0), "15"))
	if ts = dev.do("GET", "/api/v1/work/timesheet?week="+w2, nil, http.StatusOK); len(ts["rows"].([]any)) != 2 {
		t.Fatalf("la richiesta fallita ha lasciato tracce: %v", ts["rows"])
	}
	// Le ore passano dal dettaglio dell'elemento come le altre.
	if sum := dev.do("GET", "/api/v1/work/elements/"+k1+"/time", nil, http.StatusOK); sum["minutes"].(float64) != 240 {
		t.Fatalf("ore dell'elemento: %v", sum)
	}
	// Un collega non legge il mio timesheet; il responsabile del team sì.
	dev2.do("GET", "/api/v1/work/timesheet?week="+w2+"&member="+dev.member, nil, http.StatusForbidden)
	lead.do("GET", "/api/v1/work/timesheet?week="+w2+"&member="+dev.member, nil, http.StatusOK)

	// Copia: nella settimana dopo, solo nelle celle vuote.
	cp := dev.do("POST", "/api/v1/work/timesheet/copy", map[string]any{"week": w1}, http.StatusOK)
	if cp["copied"].(float64) != 3 {
		t.Fatalf("copia: %v", cp["copied"])
	}
	if ts = cp["timesheet"].(map[string]any); ts["total"].(float64) != 120+120+90 {
		t.Fatalf("settimana copiata: %v", ts["total"])
	}
	if again := dev.do("POST", "/api/v1/work/timesheet/copy", map[string]any{"week": w1}, http.StatusOK); again["copied"].(float64) != 0 {
		t.Fatalf("la seconda copia non deve duplicare: %v", again["copied"])
	}
	// Il lavoro assegnato a me compare tra i suggerimenti.
	dev.do("PUT", "/api/v1/work/elements/"+k3+"/assignee", map[string]any{"member_id": dev.member}, http.StatusOK)
	ts = dev.do("GET", "/api/v1/work/timesheet?week="+w1, nil, http.StatusOK)
	if sug := ts["assigned"].([]any); len(sug) != 1 || sug[0].(map[string]any)["key"] != k3 {
		t.Fatalf("assegnati: %v", sug)
	}

	// --- Invio e approvazione (regola del tenant) -------------------------------------------------
	dev.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": w1}, http.StatusUnprocessableEntity) // non richiesta
	dev.do("PUT", "/api/v1/work/time/settings", map[string]any{"require_approval": true}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"require_approval": true, "approver_mode": "team_lead"}, http.StatusOK)
	ts = dev.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": w1}, http.StatusOK)
	if ts["status"] != "submitted" || ts["editable"] != false {
		t.Fatalf("invio: %v", ts)
	}
	dev.do("PUT", "/api/v1/work/timesheet/cells", map[string]any{"week": w1, "cells": []any{cell(k1, day(w1, 3), "1")}}, http.StatusUnprocessableEntity)
	dev.do("POST", "/api/v1/work/elements/"+k1+"/time", map[string]any{"duration": "1h", "date": day(w1, 3)}, http.StatusUnprocessableEntity)
	pend := lead.do("GET", "/api/v1/work/timesheet/pending", nil, http.StatusOK)["items"].([]any)
	if len(pend) != 1 || pend[0].(map[string]any)["member_id"] != dev.member {
		t.Fatalf("da approvare: %v", pend)
	}
	if p := dev2.do("GET", "/api/v1/work/timesheet/pending", nil, http.StatusOK)["items"].([]any); len(p) != 0 {
		t.Fatalf("un estraneo vede le approvazioni: %v", p)
	}
	decide := func(u *user, approve bool, comment string, status int) map[string]any {
		return u.do("POST", "/api/v1/work/timesheet/decide", map[string]any{"member": dev.member, "week": w1, "approve": approve, "comment": comment}, status)
	}
	decide(dev2, true, "", http.StatusForbidden)
	decide(dev, true, "", http.StatusUnprocessableEntity) // non la propria
	decide(lead, false, "", http.StatusUnprocessableEntity)
	if r := decide(lead, false, "Manca il giovedì", http.StatusOK); r["status"] != "rejected" || r["comment"] != "Manca il giovedì" {
		t.Fatalf("rifiuto: %v", r)
	}
	// Respinta: torna modificabile e si reinvia; un richiamo la riporta in bozza.
	cells(dev, w1, http.StatusOK, cell(k1, day(w1, 3), "1"))
	dev.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": w1}, http.StatusOK)
	if r := dev.do("POST", "/api/v1/work/timesheet/withdraw", map[string]any{"week": w1}, http.StatusOK); r["status"] != "draft" {
		t.Fatalf("richiamo: %v", r)
	}
	dev.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": w1}, http.StatusOK)
	if r := decide(lead, true, "", http.StatusOK); r["status"] != "approved" {
		t.Fatalf("approvazione: %v", r)
	}
	dev.do("POST", "/api/v1/work/timesheet/withdraw", map[string]any{"week": w1}, http.StatusUnprocessableEntity)
	cells(dev, w1, http.StatusUnprocessableEntity, cell(k1, day(w1, 3), "3"))
	// Solo chi amministra il tempo riapre una settimana approvata.
	lead.do("POST", "/api/v1/work/timesheet/reopen", map[string]any{"member": dev.member, "week": w1}, http.StatusForbidden)
	admin.do("POST", "/api/v1/work/timesheet/reopen", map[string]any{"member": dev.member, "week": w1}, http.StatusNoContent)
	cells(dev, w1, http.StatusOK, cell(k1, day(w1, 3), "3"))

	// --- Periodo chiuso ------------------------------------------------------------------------------
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"locked_until": day(w2, 6)}, http.StatusOK)
	cells(dev, w2, http.StatusUnprocessableEntity, cell(k1, day(w2, 3), "1"))
	dev.do("POST", "/api/v1/work/elements/"+k1+"/time", map[string]any{"duration": "1h", "date": day(w2, 4)}, http.StatusUnprocessableEntity)
	ts = dev.do("GET", "/api/v1/work/timesheet?week="+w2, nil, http.StatusOK)
	if !ts["days"].([]any)[0].(map[string]any)["locked"].(bool) {
		t.Fatalf("giorno chiuso non segnalato: %v", ts["days"])
	}
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"locked_until": ""}, http.StatusOK)

	// --- Report ----------------------------------------------------------------------------------------
	// Ore di dev: settimana w2 = 330 (k1 2h + 2h, k2 1,5h); w1 = 330 copiati + 180 (k1, giovedì) = 510.
	q := "?from=" + w2 + "&to=" + day(w1, 6)
	rep := admin.do("GET", "/api/v1/work/time/report"+q+"&group=person,space", nil, http.StatusOK)
	g := rep["groups"].([]any)
	if len(g) != 1 || g[0].(map[string]any)["minutes"].(float64) != 840 {
		t.Fatalf("report per persona: %v", rep)
	}
	first := g[0].(map[string]any)
	if first["label"] != "Dev" || len(first["children"].([]any)) != 1 {
		t.Fatalf("raggruppamento a due livelli: %v", first)
	}
	if first["capacity"] == nil || first["capacity"].(float64) <= 0 {
		t.Fatalf("capacità della persona: %v", first)
	}
	if first["estimate"].(float64) != 480 { // la stima di «Login con SPID»
		t.Fatalf("stima: %v", first["estimate"])
	}
	if rep["can_cost"] != true || first["cost_cents"] == nil {
		t.Fatalf("i costi li vede chi legge la finanza: %v", rep["can_cost"])
	}
	// Chi non ha finance.read non vede costi; vede solo le proprie ore.
	devRep := dev.do("GET", "/api/v1/work/time/report"+q+"&group=person", nil, http.StatusOK)
	if devRep["can_cost"] == true || devRep["scope"] != "own" || devRep["groups"].([]any)[0].(map[string]any)["cost_cents"] != nil {
		t.Fatalf("report di chi non legge i costi: %v", devRep)
	}
	if r := dev2.do("GET", "/api/v1/work/time/report"+q+"&group=person", nil, http.StatusOK); len(r["groups"].([]any)) != 0 {
		t.Fatalf("un collega vede le ore altrui: %v", r["groups"])
	}
	if r := lead.do("GET", "/api/v1/work/time/report"+q+"&group=person", nil, http.StatusOK); r["scope"] != "team" || len(r["groups"].([]any)) != 1 {
		t.Fatalf("il responsabile vede il suo team: %v", r)
	}
	if r := fin.do("GET", "/api/v1/work/time/report"+q+"&group=team,person", nil, http.StatusOK); len(r["groups"].([]any)) != 1 || r["groups"].([]any)[0].(map[string]any)["label"] != "Web" {
		t.Fatalf("raggruppamento per team: %v", r)
	}
	dev.do("GET", "/api/v1/work/time/report"+q+"&group=persona", nil, http.StatusUnprocessableEntity)
	// Ogni numero porta alle registrazioni: stesso filtro.
	recs := admin.do("GET", "/api/v1/work/time/records"+q+"&person="+dev.member+"&element="+s1["id"].(string), nil, http.StatusOK)
	if recs["count"].(float64) != 5 || recs["total_minutes"].(float64) != 660 {
		t.Fatalf("registrazioni dell'elemento: %v", recs)
	}
	// CSV con intestazione e totale.
	resp, err := admin.c.Get(admin.base + "/api/v1/work/time/report" + q + "&group=person,space&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(buf.String(), "Persona;Spazio;Ore") || !strings.Contains(buf.String(), "Totale") {
		t.Fatalf("csv: %d %q", resp.StatusCode, buf.String())
	}

	// --- Assenze e capacità ------------------------------------------------------------------------------
	dev.do("POST", "/api/v1/work/absences", map[string]any{"member_id": dev2.member, "from": day(w1, 0), "to": day(w1, 0)}, http.StatusForbidden)
	dev.do("POST", "/api/v1/work/absences", map[string]any{"from": day(w1, 1), "to": day(w1, 2), "kind": "ferie"}, http.StatusCreated)
	lead.do("POST", "/api/v1/work/absences", map[string]any{"member_id": dev.member, "from": day(w1, 3), "hours_per_day": 4, "kind": "permesso"}, http.StatusCreated)
	dev.do("POST", "/api/v1/work/absences", map[string]any{"from": day(w1, 3), "to": day(w1, 1)}, http.StatusUnprocessableEntity)
	ts = dev.do("GET", "/api/v1/work/timesheet?week="+w1, nil, http.StatusOK)
	days := ts["days"].([]any)
	if days[1].(map[string]any)["capacity"].(float64) != 0 || days[3].(map[string]any)["absence"].(float64) != 240 {
		t.Fatalf("assenze nella capacità: %v", days)
	}
	// Il carico dei team usa la capacità (90 ore = 11,25 giorni a settimana) meno le assenze.
	var week map[string]any
	for _, x := range admin.do("GET", "/api/v1/teams/load?from="+w1+"&to="+day(w1, 6), nil, http.StatusOK)["items"].([]any) {
		if x.(map[string]any)["name"] == "Web" {
			week = x.(map[string]any)["weeks"].([]any)[0].(map[string]any)
		}
	}
	if week == nil || week["capacity_days"].(float64) >= 11.25 || week["capacity_days"].(float64) <= 8 {
		t.Fatalf("carico dei team: %v", week)
	}

	// --- Comandi in blocco ---------------------------------------------------------------------------------
	sprint := admin.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Sprint 1", "start": day(w1, 0), "end": day(w1, 13)}, http.StatusCreated)
	sprintKey := sprint["key"].(string)
	cx := dev.do("GET", "/api/v1/work/bulk/context?space=WEB", nil, http.StatusOK)
	if len(cx["members"].([]any)) < 4 || len(cx["phases"].([]any)) < 4 || len(cx["sprints"].([]any)) != 1 || len(cx["fields"].([]any)) < 3 {
		t.Fatalf("contesto dei comandi: %v", cx)
	}
	ids := []any{k1, k2, k3}
	bulk := func(u *user, body map[string]any, status int) map[string]any {
		body["ids"] = ids
		return u.do("POST", "/api/v1/work/bulk", body, status)
	}
	// Anteprima: senza assegnatario la fase è bloccata, con il motivo; non cambia niente.
	pv := bulk(dev, map[string]any{"dry_run": true, "commands": []any{map[string]any{"kind": "phase", "value": "In corso"}}}, http.StatusOK)
	if pv["blocked"].(float64) != 2 || pv["applied"].(float64) != 1 || pv["dry_run"] != true {
		t.Fatalf("anteprima: %v", pv)
	}
	for _, it := range pv["items"].([]any) {
		m := it.(map[string]any)
		if m["key"] == k3 {
			if m["status"] != "ok" {
				t.Fatalf("%s è già assegnato a dev: %v", k3, m)
			}
			continue
		}
		if m["status"] != "blocked" || !strings.Contains(m["reason"].(string), "assegnatario") {
			t.Fatalf("motivo del blocco: %v", m)
		}
	}
	if st := state(t, dev, k3); st != "Da fare" {
		t.Fatalf("l'anteprima ha cambiato la fase: %s", st)
	}
	// Comando reale: assegna (applicato per primo, soddisfa il gate), fase, etichette, sprint, campo.
	run := bulk(dev, map[string]any{"commands": []any{
		map[string]any{"kind": "phase", "value": "In corso"},
		map[string]any{"kind": "assign", "value": dev.member, "label": "Dev"},
		map[string]any{"kind": "labels", "add": []string{"urgente", "web"}},
		map[string]any{"kind": "sprint", "value": sprintKey},
		map[string]any{"kind": "field", "field": "priorita", "raw": "Alta"},
	}}, http.StatusOK)
	if run["applied"].(float64) != 3 || run["blocked"].(float64) != 0 || run["command_id"] == nil {
		t.Fatalf("comando in blocco: %v", run)
	}
	for _, k := range []string{k1, k2, k3} {
		el := dev.do("GET", "/api/v1/elements/"+k, nil, http.StatusOK)["element"].(map[string]any)
		if el["state"] != "In corso" || el["assignee_id"] != dev.member || el["fields"].(map[string]any)["priorita"] == nil {
			t.Fatalf("%s dopo il comando: %v", k, el)
		}
	}
	planned := 0
	for _, it := range dev.do("GET", "/api/v1/work/spaces/WEB/backlog", nil, http.StatusOK)["items"].([]any) {
		if it.(map[string]any)["sprint"] == sprint["id"] {
			planned++
		}
	}
	if planned != 3 {
		t.Fatalf("elementi nello sprint: %d", planned)
	}
	// Elementi di un altro tenant: non si vedono, restano fuori con il motivo.
	if r := bulk(adminB, map[string]any{"commands": []any{map[string]any{"kind": "assign", "value": ""}}}, http.StatusOK); r["applied"].(float64) != 0 || r["blocked"].(float64) != 3 {
		t.Fatalf("un altro tenant non deve vedere gli elementi: %v", r)
	}
	bulk(dev, map[string]any{"commands": []any{map[string]any{"kind": "boh"}}}, http.StatusUnprocessableEntity)
	bulk(dev, map[string]any{"commands": []any{map[string]any{"kind": "delete"}, map[string]any{"kind": "assign"}}}, http.StatusUnprocessableEntity)
	// Annullamento dell'ultimo comando.
	last := dev.do("GET", "/api/v1/work/bulk/last", nil, http.StatusOK)["last"].(map[string]any)
	if last["elements"].(float64) != 3 || !strings.Contains(last["summary"].(string), "Dev") {
		t.Fatalf("ultimo comando: %v", last)
	}
	// Chi ha cambiato un valore nel frattempo non viene calpestato.
	dev.do("PUT", "/api/v1/work/elements/"+k2+"/assignee", map[string]any{"member_id": dev2.member}, http.StatusOK)
	undo := dev.do("POST", "/api/v1/work/bulk/undo", nil, http.StatusOK)
	if undo["restored"].(float64) != 13 || len(undo["skipped"].([]any)) != 1 {
		t.Fatalf("annullamento: %v", undo)
	}
	if st := state(t, dev, k1); st != "Da fare" {
		t.Fatalf("fase dopo l'annullamento: %s", st)
	}
	if el := dev.do("GET", "/api/v1/elements/"+k1, nil, http.StatusOK)["element"].(map[string]any); el["assignee_id"] != nil {
		t.Fatalf("assegnatario dopo l'annullamento: %v", el["assignee_id"])
	}
	if l := dev.do("GET", "/api/v1/work/bulk/last", nil, http.StatusOK)["last"]; l != nil {
		t.Fatalf("un comando annullato non si annulla di nuovo: %v", l)
	}
	dev.do("POST", "/api/v1/work/bulk/undo", nil, http.StatusUnprocessableEntity)
	// Elimina: con ore registrate no (i costi non si perdono); senza sì, e si ripristina.
	del := dev.do("POST", "/api/v1/work/bulk", map[string]any{"ids": []any{k1, k3}, "commands": []any{map[string]any{"kind": "delete"}}}, http.StatusOK)
	if del["applied"].(float64) != 1 || del["blocked"].(float64) != 1 {
		t.Fatalf("elimina: %v", del)
	}
	dev.do("GET", "/api/v1/elements/"+k3, nil, http.StatusNotFound)
	dev.do("POST", "/api/v1/work/bulk/undo", nil, http.StatusOK)
	dev.do("GET", "/api/v1/elements/"+k3, nil, http.StatusOK)
	// Tutto o niente: con un elemento bloccato non cambia niente.
	dev.do("POST", "/api/v1/work/bulk", map[string]any{"ids": []any{k1, k2}, "all_or_nothing": true,
		"commands": []any{map[string]any{"kind": "phase", "value": "In revisione"}}}, http.StatusUnprocessableEntity)
	if st := state(t, dev, k2); st != "Da fare" {
		t.Fatalf("tutto o niente: %s", st)
	}
}
