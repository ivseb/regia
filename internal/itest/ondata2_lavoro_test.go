package itest

import (
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

// Quasi vittorie dell'area lavoro (ondata 2): responsabile persona e comandi in blocco sulle richieste,
// portale con unità del profilo, una sola bozza e richieste simili.
func TestRichiesteResponsabileEBlocco(t *testing.T) {
	e := newSGEnv(t, "resp")
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	viewer := e.member("viewer", tenant.MemberInput{Roles: []string{"viewer"}})

	mk := func(title string) (string, string) {
		r := dmU.do("POST", "/api/v1/demand/requests", map[string]any{"title": title}, http.StatusCreated)
		return r["id"].(string), r["key"].(string)
	}
	_, k1 := mk("Cassetto fiscale")
	_, k2 := mk("Archivio polizze")

	// «Prendi in carico»: la richiesta ha un responsabile persona.
	el := dmU.do("PUT", "/api/v1/work/elements/"+k1+"/assignee", map[string]any{"member_id": dmU.member}, http.StatusOK)
	if el["assignee_id"] != dmU.member {
		t.Fatalf("responsabile: %v", el["assignee_id"])
	}
	viewer.do("PUT", "/api/v1/work/elements/"+k1+"/assignee", map[string]any{"member_id": viewer.member}, http.StatusForbidden)
	el = dmU.do("PUT", "/api/v1/work/elements/"+k1+"/assignee", map[string]any{"member_id": ""}, http.StatusOK)
	if el["assignee_id"] != nil {
		t.Fatalf("responsabile tolto: %v", el["assignee_id"])
	}

	// Il contesto dei comandi in blocco delle richieste ha le loro fasi e nessuno sprint.
	ctx := dmU.do("GET", "/api/v1/work/bulk/context?type=business_request", nil, http.StatusOK)
	phases := map[string]bool{}
	for _, p := range ctx["phases"].([]any) {
		phases[p.(string)] = true
	}
	if !phases["Triage"] || phases["In sviluppo"] || len(ctx["sprints"].([]any)) != 0 {
		t.Fatalf("contesto delle richieste: %v", ctx)
	}

	// Comando in blocco: assegna due richieste; l'anteprima non cambia niente.
	in := map[string]any{"ids": []string{k1, k2}, "commands": []any{map[string]any{"kind": "assign", "value": dmU.member}}, "dry_run": true}
	if r := dmU.do("POST", "/api/v1/work/bulk", in, http.StatusOK); r["applied"].(float64) != 2 {
		t.Fatalf("anteprima: %v", r)
	}
	if d := dmU.do("GET", "/api/v1/elements/"+k2, nil, http.StatusOK); d["element"].(map[string]any)["assignee_id"] != nil {
		t.Fatal("l'anteprima ha cambiato il responsabile")
	}
	in["dry_run"] = false
	if r := dmU.do("POST", "/api/v1/work/bulk", in, http.StatusOK); r["applied"].(float64) != 2 || r["blocked"].(float64) != 0 {
		t.Fatalf("assegnazione in blocco: %v", r)
	}
	for _, k := range []string{k1, k2} {
		if d := dmU.do("GET", "/api/v1/elements/"+k, nil, http.StatusOK); d["element"].(map[string]any)["assignee_id"] != dmU.member {
			t.Fatalf("%s: responsabile %v", k, d["element"].(map[string]any)["assignee_id"])
		}
	}
	// Le richieste non si eliminano né si mettono in uno sprint.
	for _, cmd := range []map[string]any{{"kind": "delete"}, {"kind": "sprint", "value": "backlog"}} {
		r := dmU.do("POST", "/api/v1/work/bulk", map[string]any{"ids": []string{k1}, "commands": []any{cmd}}, http.StatusOK)
		if r["blocked"].(float64) != 1 {
			t.Fatalf("comando %v su una richiesta: %v", cmd["kind"], r)
		}
	}
	// L'ultimo comando si annulla anche sulle richieste.
	if r := dmU.do("POST", "/api/v1/work/bulk/undo", nil, http.StatusOK); r["restored"].(float64) != 2 {
		t.Fatalf("annulla: %v", r)
	}
	if d := dmU.do("GET", "/api/v1/elements/"+k1, nil, http.StatusOK); d["element"].(map[string]any)["assignee_id"] != nil {
		t.Fatal("l'annullamento non ha tolto il responsabile")
	}
}

func TestPortaleUnitaBozzaSimili(t *testing.T) {
	e := newSGEnv(t, "portale")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	other := e.member("altra", tenant.MemberInput{Roles: []string{"editor"}})

	// Un albero con la radice e un'area; la persona è nell'area.
	root := admin.do("POST", "/api/v1/org/units", map[string]any{"name": "Azienda", "kind": "legal_entity"}, http.StatusCreated)
	area := admin.do("POST", "/api/v1/org/units", map[string]any{"name": "Area sinistri", "kind": "business_unit", "parent_id": root["id"]}, http.StatusCreated)
	admin.do("PUT", "/api/v1/org/units/"+area["id"].(string)+"/members/"+dmU.member, map[string]any{"role": "member"}, http.StatusOK)

	p := dmU.do("GET", "/api/v1/portal", nil, http.StatusOK)
	if p["my_unit"] != area["id"] {
		t.Fatalf("unità della persona: %v", p["my_unit"])
	}
	for _, u := range p["units"].([]any) {
		if u.(map[string]any)["key"] == root["id"] {
			t.Fatalf("la radice è tra le unità richiedenti: %v", p["units"])
		}
	}
	if p := other.do("GET", "/api/v1/portal", nil, http.StatusOK); p["my_unit"] != "" {
		t.Fatalf("chi non è in un'unità non ne ha una precompilata: %v", p["my_unit"])
	}

	// Una sola bozza per persona e tipo.
	d1 := dmU.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "business_request", "title": "Prima", "step": 0}, http.StatusCreated)
	d2 := dmU.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "business_request", "title": "Seconda", "step": 2}, http.StatusCreated)
	if d1["id"] != d2["id"] || d2["title"] != "Seconda" {
		t.Fatalf("la seconda bozza non riprende la prima: %v %v", d1, d2)
	}
	if n := len(dmU.do("GET", "/api/v1/portal", nil, http.StatusOK)["drafts"].([]any)); n != 1 {
		t.Fatalf("bozze: %d", n)
	}
	if o := other.do("POST", "/api/v1/portal/drafts", map[string]any{"type": "business_request", "title": "Altra"}, http.StatusCreated); o["id"] == d1["id"] {
		t.Fatal("la bozza è di un'altra persona")
	}

	// Richieste simili: parole in comune col titolo, solo tra quelle leggibili.
	r := dmU.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Modulo reclami clienti online"}, http.StatusCreated)
	dmU.do("POST", "/api/v1/elements/"+r["id"].(string)+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	sim := dmU.do("GET", "/api/v1/portal/similar?q=Nuovo+modulo+per+i+reclami+dei+clienti", nil, http.StatusOK)["items"].([]any)
	if len(sim) != 1 || sim[0].(map[string]any)["key"] != r["key"] {
		t.Fatalf("richieste simili: %v", sim)
	}
	if sim := dmU.do("GET", "/api/v1/portal/similar?q=Qualcosa+di+completamente+diverso", nil, http.StatusOK)["items"].([]any); len(sim) != 0 {
		t.Fatalf("simili inattese: %v", sim)
	}
	if sim := dmU.do("GET", "/api/v1/portal/similar?q=ab", nil, http.StatusOK)["items"].([]any); len(sim) != 0 {
		t.Fatalf("un testo troppo corto non cerca: %v", sim)
	}
}
