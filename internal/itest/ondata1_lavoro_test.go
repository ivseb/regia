package itest

import (
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

// L'organo di governo decide se si può decidere «in linea»: predefinito sì, modificabile, e il dato
// arriva nei passi di approvazione e nell'inbox (serve alla futura pagina «Da fare»).
func TestOrganoDecisioneInLinea(t *testing.T) {
	e := newSGEnv(t, "inl")
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	pmo := e.member("pmo", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	spo := e.member("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})

	bodies := admin.do("GET", "/api/v1/governance/bodies", nil, http.StatusOK)["items"].([]any)
	body := bodies[0].(map[string]any)
	bodyID := body["id"].(string)
	if body["inline_decision"] != true {
		t.Fatalf("predefinito: si decide in linea: %v", body["inline_decision"])
	}

	// Una richiesta da 300 k€ arriva al comitato dopo sponsor e PMO.
	r := dmU.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Migrazione del data warehouse", "sponsor_id": spo.member,
		"estimate_low_cents": 300 * 80_000, "estimate_high_cents": 300 * 100_000}, http.StatusCreated)
	id, key := r["id"].(string), r["key"].(string)
	dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	for _, to := range []string{"Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
		dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	steps := func() []any {
		return admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["approvals"].([]any)[0].(map[string]any)["steps"].([]any)
	}
	sp := steps()
	for _, s := range sp {
		if s.(map[string]any)["inline_decision"] != true {
			t.Fatalf("passo con decisione in linea per default: %v", s)
		}
	}
	spo.do("POST", "/api/v1/approvals/steps/"+sp[0].(map[string]any)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	pmo.do("POST", "/api/v1/approvals/steps/"+sp[1].(map[string]any)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)

	// Si spegne «in linea» per il comitato: la modifica senza il campo non lo riaccende.
	in := map[string]any{"name": body["name"], "kind": body["kind"], "chair_id": body["chair_id"], "inline_decision": false}
	if upd := admin.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusOK); upd["inline_decision"] != false {
		t.Fatalf("organo aggiornato: %v", upd["inline_decision"])
	}
	delete(in, "inline_decision")
	in["name"] = "Comitato investimenti (rinominato)"
	if upd := admin.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusOK); upd["inline_decision"] != false {
		t.Fatalf("senza il campo l'impostazione resta: %v", upd["inline_decision"])
	}
	if got := steps()[2].(map[string]any)["inline_decision"]; got != false {
		t.Fatalf("il passo del comitato non rispetta l'organo: %v", got)
	}
	if got := steps()[0].(map[string]any)["inline_decision"]; got != true {
		t.Fatalf("il passo dello sponsor resta in linea: %v", got)
	}
	items := admin.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["inline_decision"] != false {
		t.Fatalf("inbox del presidente: %v", items)
	}
	// Una creazione senza il campo parte da «sì».
	nuovo := admin.do("POST", "/api/v1/governance/bodies", map[string]any{"name": "Steering", "kind": "steering", "chair_id": pmo.member}, http.StatusCreated)
	if nuovo["inline_decision"] != true {
		t.Fatalf("nuovo organo: %v", nuovo["inline_decision"])
	}
}
