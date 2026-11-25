package itest

import (
	"context"
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

// «Da fare» (ondata 2, proposta 1): una sola pagina con decisioni (con il contesto della graduatoria),
// impatti del team (con i passaggi disponibili), menzioni, e un campanello che conta solo ciò che chiede
// un'azione. Gli avvisi informativi si silenziano per elemento.
func TestDaFare(t *testing.T) {
	e := newSGEnv(t, "todo")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	pmo := e.member("pmo", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	spo := e.member("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	lead := e.member("lead", tenant.MemberInput{Roles: []string{"viewer"}})
	archi := e.member("arch", tenant.MemberInput{Roles: []string{"architect"}})

	todo := func(u *user) map[string]any { return u.do("GET", "/api/v1/todo", nil, http.StatusOK) }
	count := func(u *user, k string) int {
		return int(u.do("GET", "/api/v1/todo/count", nil, http.StatusOK)[k].(float64))
	}
	list := func(m map[string]any, k string) []any { return m[k].([]any) }

	// Chi non ha niente da fare trova liste vuote e totale zero.
	if n := count(spo, "total"); n != 0 {
		t.Fatalf("totale iniziale dello sponsor: %d", n)
	}
	if td := todo(spo); len(list(td, "decisions")) != 0 || len(list(td, "impacts")) != 0 {
		t.Fatalf("pagina vuota: %v", td)
	}

	// Una richiesta da 30 k€ arriva all'approvazione dello sponsor.
	r := dmU.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Archivio digitale", "sponsor_id": spo.member,
		"estimate_low_cents": 2_000_000, "estimate_high_cents": 3_000_000}, http.StatusCreated)
	id, key := r["id"].(string), r["key"].(string)
	dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	for _, to := range []string{"Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
		dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	td := todo(spo)
	ds := list(td, "decisions")
	if len(ds) != 1 {
		t.Fatalf("decisioni dello sponsor: %v", ds)
	}
	d := ds[0].(map[string]any)
	if d["element_key"] != key || d["is_request"] != true || d["inline_decision"] != true || d["step_id"] == "" {
		t.Fatalf("decisione: %v", d)
	}
	if d["amount_cents"] == nil || d["position"] == nil || d["rank_total"].(float64) < 1 {
		t.Fatalf("la decisione non porta importo e posizione in graduatoria: %v", d)
	}
	if n := count(spo, "decisions"); n != 1 {
		t.Fatalf("conteggio decisioni: %d", n)
	}
	if n := count(spo, "total"); n != 1 {
		t.Fatalf("totale: %d", n)
	}
	// Un altro non vede la decisione dello sponsor.
	if n := count(pmo, "decisions"); n != 0 {
		t.Fatalf("il PMO vede %d decisioni non sue", n)
	}
	// Decide dalla stessa pagina (la stessa API della scheda) e la riga sparisce.
	spo.do("POST", "/api/v1/approvals/steps/"+d["step_id"].(string)+"/decision", map[string]any{"decision": "approved", "comment": "Va bene"}, http.StatusNoContent)
	if n := count(spo, "total"); n != 0 {
		t.Fatalf("dopo la decisione restano %d voci", n)
	}

	// Impatti del team: il referente vede quelli del suo team, con i passaggi disponibili.
	archi.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\nFE-AR;Frontend area riservata;alta;sì;personali;Web;Area riservata\n"
	archi.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	var appID string
	for _, a := range archi.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		appID = a.(map[string]any)["id"].(string)
	}
	imp := dmU.do("POST", "/api/v1/demand/requests/"+key+"/impacts", map[string]any{"application_id": appID, "change_kind": "change"}, http.StatusCreated)
	impID := imp["id"].(string)
	is := list(todo(lead), "impacts")
	if len(is) != 1 {
		t.Fatalf("impatti del team: %v", is)
	}
	i := is[0].(map[string]any)
	if i["state"] != "Da analizzare" || i["team"] != "Web" || i["can_estimate"] != true || i["request_key"] != key {
		t.Fatalf("impatto: %v", i)
	}
	if len(i["transitions"].([]any)) != 0 {
		t.Fatalf("il referente non ha passaggi prima della stima: %v", i["transitions"])
	}
	// Chi non è del team non lo vede.
	if n := count(spo, "impacts"); n != 0 {
		t.Fatalf("lo sponsor vede %d impatti", n)
	}
	dmU.do("POST", "/api/v1/elements/"+impID+"/transitions", map[string]any{"to": "In analisi"}, http.StatusOK)
	lead.do("PATCH", "/api/v1/impacts/"+impID, map[string]any{"estimate_effort_days": 12}, http.StatusOK)
	dmU.do("POST", "/api/v1/elements/"+impID+"/transitions", map[string]any{"to": "Stimato"}, http.StatusOK)
	i = list(todo(lead), "impacts")[0].(map[string]any)
	var forward, back bool
	for _, tr := range i["transitions"].([]any) {
		m := tr.(map[string]any)
		if m["to"] == "Accettato" && m["forward"] == true {
			forward = true
		}
		if m["to"] == "In analisi" && m["comment_required"] == true {
			back = true
		}
	}
	if !forward || !back || i["effort_days"].(float64) != 12 {
		t.Fatalf("passaggi dell'impatto stimato: %v", i)
	}
	if i["free_days"] == nil || i["weeks"].(float64) < 1 {
		t.Fatalf("manca la capacità libera del team: %v", i)
	}
	lead.do("POST", "/api/v1/elements/"+impID+"/transitions", map[string]any{"to": "Accettato"}, http.StatusOK)
	if n := count(lead, "impacts"); n != 0 {
		t.Fatalf("l'impatto accettato è ancora in lista: %d", n)
	}

	// Menzioni: contano nel totale; avvisi informativi no.
	dmU.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "Ciao @sponsor, serve il tuo parere."}, http.StatusCreated)
	if n := count(spo, "mentions"); n != 1 {
		t.Fatalf("menzioni dello sponsor: %d", n)
	}
	if n := count(spo, "total"); n != 1 {
		t.Fatalf("totale con la menzione: %d", n)
	}
	m := list(todo(spo), "mentions")[0].(map[string]any)
	if m["element_key"] != key || m["kind"] != "mention" {
		t.Fatalf("menzione: %v", m)
	}
	// Un commento senza menzione è un avviso: non cambia il totale.
	dmU.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "Aggiornamento senza menzioni."}, http.StatusCreated)
	if n := count(spo, "total"); n != 1 {
		t.Fatalf("un avviso ha cambiato il totale: %d", n)
	}
	unread := func() int {
		return int(spo.do("GET", "/api/v1/notifications/count", nil, http.StatusOK)["unread"].(float64))
	}
	before := unread()
	// Silenzia l'elemento: gli avvisi informativi già arrivati sono letti e i nuovi non arrivano; la
	// menzione resta.
	spo.do("PUT", "/api/v1/me/silenced/"+key, nil, http.StatusNoContent)
	if s := list(todo(spo), "silenced"); len(s) != 1 {
		t.Fatalf("elementi silenziati: %v", s)
	}
	dmU.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "Un altro aggiornamento."}, http.StatusCreated)
	after := unread()
	if after >= before {
		t.Fatalf("gli avvisi non sono stati silenziati: prima %d dopo %d", before, after)
	}
	if n := count(spo, "mentions"); n != 1 {
		t.Fatalf("la menzione non si silenzia: %d", n)
	}
	spo.do("DELETE", "/api/v1/me/silenced/"+key, nil, http.StatusNoContent)
	if s := list(todo(spo), "silenced"); len(s) != 0 {
		t.Fatalf("elementi ancora silenziati: %v", s)
	}
	// Tempo: con l'approvazione richiesta, la settimana scorsa con ore e non inviata è da inviare; chi
	// approva vede quella inviata da altri.
	admin.do("PUT", "/api/v1/work/time/settings", map[string]any{"require_approval": true, "approver_mode": "admin"}, http.StatusOK)
	if n := count(dmU, "time"); n != 0 {
		t.Fatalf("settimane da inviare senza ore: %d", n)
	}
	if _, err := testDB.Owner.Exec(context.Background(), `insert into work_logs (tenant_id, element_id, member_id, work_date, minutes)
		values ($1, $2, $3, current_date - 7, 240)`, e.tenantID, id, dmU.member); err != nil {
		t.Fatal(err)
	}
	w := list(todo(dmU), "time")
	if len(w) != 1 || w[0].(map[string]any)["kind"] != "submit" || w[0].(map[string]any)["minutes"].(float64) != 240 {
		t.Fatalf("settimana da inviare: %v", w)
	}
	week := w[0].(map[string]any)["week"].(string)
	dmU.do("POST", "/api/v1/work/timesheet/submit", map[string]any{"week": week}, http.StatusOK)
	if n := count(dmU, "time"); n != 0 {
		t.Fatalf("dopo l'invio restano %d settimane", n)
	}
	aw := list(todo(admin), "time")
	if len(aw) != 1 || aw[0].(map[string]any)["kind"] != "approve" || aw[0].(map[string]any)["member_id"] != dmU.member {
		t.Fatalf("settimana da approvare: %v", aw)
	}

	// Un elemento che non esiste non si silenzia.
	spo.do("PUT", "/api/v1/me/silenced/NOPE-1", nil, http.StatusNotFound)
}
