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

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Controlli vivi (ondata 3, proposta 4): la proposta di efficacia la calcola il sistema da una
// regola sui fatti, la conferma una persona con la nota (e solo allora cambia lo stato
// ufficiale); le evidenze automatiche si rinnovano senza duplicarsi; la mappatura guidata
// suggerisce e associa in blocco senza togliere ciò che c'è già.
func TestGRCControlliVivi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("vivi-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Vivi", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"co", "aud"} {
		in := tenant.MemberInput{TenantSlug: slug, Email: n + "@" + slug + ".test", Name: n, Password: testPassword, Roles: []string{"compliance_officer"}, Groups: []string{"Compliance"}}
		if n == "aud" {
			in.MemberType, in.Roles, in.Groups = "auditor", []string{"auditor"}, nil
		}
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	gs := grc.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq, GRC: gs,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	co, aud := login(t, srv.URL, "co@"+slug+".test", testPassword), login(t, srv.URL, "aud@"+slug+".test", testPassword)
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	co.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusCreated)
	ctl := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Vulnerabilità critiche entro lo SLA", "text": "Le vulnerabilità critiche si correggono entro lo SLA della politica.",
		"requirements": []any{map[string]any{"ref": "dora:Art. 9"}}}, http.StatusCreated)
	key := ctl["key"].(string)
	base := "/api/v1/grc/controls/" + key + "/proposal"

	// Senza regola non c'è proposta.
	if p := co.do("GET", base, nil, http.StatusOK); p["state"] != "none" || p["pending"] != false {
		t.Fatalf("senza regola: %v", p)
	}
	// Una regola vuota, con un dato sconosciuto o con sorgenti strane si rifiuta; l'auditor non scrive.
	co.do("PUT", base+"/rule", map[string]any{}, http.StatusUnprocessableEntity)
	co.do("PUT", base+"/rule", map[string]any{"effective_when": "dato_inventato: 0"}, http.StatusUnprocessableEntity)
	co.do("PUT", base+"/rule", map[string]any{"effective_when": "vuln_critiche_scadute: 0", "sources": []any{"altro"}}, http.StatusUnprocessableEntity)
	aud.do("PUT", base+"/rule", map[string]any{"effective_when": "vuln_critiche_scadute: 0"}, http.StatusForbidden)

	// Regola GRC-2: efficace se nessuna critica oltre lo SLA; a rischio se ce ne sono.
	p := co.do("PUT", base+"/rule", map[string]any{"name": "Nessuna critica oltre lo SLA", "effective_when": "vuln_critiche_scadute: 0",
		"at_risk_when": "vuln_critiche_scadute: >0", "sources": []any{"rule"}}, http.StatusOK)
	if p["state"] != "effective" || p["proposed"] != "effective" || p["pending"] != true || p["official"] != "not_assessed" || len(list(p["facts"])) != 1 {
		t.Fatalf("proposta: %v", p)
	}
	// Lo stato ufficiale non è cambiato da solo.
	if c := co.do("GET", "/api/v1/grc/controls/"+key, nil, http.StatusOK); c["effectiveness"] != "not_assessed" {
		t.Fatalf("lo stato ufficiale non deve cambiare: %v", c["effectiveness"])
	}
	// L'auditor vede la proposta ma non decide.
	aud.do("GET", base, nil, http.StatusOK)
	aud.do("POST", base+"/decision", map[string]any{"decision": "confirm"}, http.StatusForbidden)
	// Non confermare vuole la nota; con la nota lo stato ufficiale resta e la proposta non si ripropone.
	co.do("POST", base+"/decision", map[string]any{"decision": "not_confirm"}, http.StatusUnprocessableEntity)
	co.do("POST", base+"/decision", map[string]any{"decision": "boh"}, http.StatusUnprocessableEntity)
	p = co.do("POST", base+"/decision", map[string]any{"decision": "not_confirm", "note": "Aspetto la scansione di fine mese"}, http.StatusOK)
	if p["pending"] != false || p["dismissed"] != true || len(list(p["decisions"])) != 1 {
		t.Fatalf("dopo il rifiuto: %v", p)
	}
	if c := co.do("GET", "/api/v1/grc/controls/"+key, nil, http.StatusOK); c["effectiveness"] != "not_assessed" {
		t.Fatalf("il rifiuto non cambia lo stato: %v", c["effectiveness"])
	}

	// Evidenza automatica dalla regola: una sola, anche se si sincronizza due volte.
	for i := 0; i < 2; i++ {
		co.do("POST", "/api/v1/grc/living/sync", map[string]any{"control": key}, http.StatusOK)
	}
	c := co.do("GET", "/api/v1/grc/controls/"+key, nil, http.StatusOK)
	auto := 0
	for _, e := range list(c["evidence"]) {
		if strings.HasPrefix(obj(e)["title"].(string), "Verifica automatica") {
			auto++
		}
	}
	if auto != 1 {
		t.Fatalf("evidenze automatiche: %d (%v)", auto, c["evidence"])
	}

	// Cambiata la regola (ora a rischio sempre) la proposta cambia e si può confermare: stato ufficiale «parziale».
	co.do("PUT", base+"/rule", map[string]any{"effective_when": "vuln_critiche_scadute: >5", "at_risk_when": "vuln_critiche_scadute: >=0"}, http.StatusOK)
	p = co.do("GET", base, nil, http.StatusOK)
	if p["state"] != "at_risk" || p["proposed"] != "partial" || p["pending"] != true {
		t.Fatalf("a rischio: %v", p)
	}
	p = co.do("POST", base+"/decision", map[string]any{"decision": "confirm", "note": "Confermo"}, http.StatusOK)
	if p["official"] != "partial" || p["pending"] != false {
		t.Fatalf("dopo la conferma: %v", p)
	}
	if got := co.do("GET", "/api/v1/grc/controls/"+key, nil, http.StatusOK); got["effectiveness"] != "partial" {
		t.Fatalf("efficacia ufficiale: %v", got["effectiveness"])
	}
	props := co.do("GET", "/api/v1/grc/proposals", nil, http.StatusOK)
	if len(list(props["items"])) != 1 {
		t.Fatalf("proposte: %v", props)
	}

	// Mappatura guidata: i requisiti senza controllo, i suggerimenti per somiglianza, l'associazione in blocco.
	wb := co.do("GET", "/api/v1/grc/mapping/workbench", nil, http.StatusOK)
	var missing []any
	for _, f := range list(wb["frameworks"]) {
		if obj(f)["key"] == "dora" {
			missing = list(obj(f)["requirements"])
		}
	}
	if len(missing) < 3 || len(list(wb["controls"])) != 1 {
		t.Fatalf("workbench: %d requisiti senza controllo, controlli %v", len(missing), wb["controls"])
	}
	ids, refs := []any{}, []any{}
	for _, r := range missing[:3] {
		ids, refs = append(ids, obj(r)["id"]), append(refs, obj(r)["ref"])
	}
	if s := co.do("POST", "/api/v1/grc/mapping/suggestions", map[string]any{"requirements": ids}, http.StatusOK); s["items"] == nil {
		t.Fatalf("suggerimenti: %v", s)
	}
	aud.do("POST", "/api/v1/grc/mapping/bulk", map[string]any{"control": key, "requirements": refs}, http.StatusForbidden)
	co.do("POST", "/api/v1/grc/mapping/bulk", map[string]any{"control": key}, http.StatusUnprocessableEntity)
	v := co.do("POST", "/api/v1/grc/mapping/bulk", map[string]any{"control": key, "requirements": refs}, http.StatusOK)
	if n := len(list(v["requirements"])); n != 4 {
		t.Fatalf("requisiti del controllo dopo il blocco: %d (deve tenere quello di prima)", n)
	}
	// Ripetere non duplica.
	if v = co.do("POST", "/api/v1/grc/mapping/bulk", map[string]any{"control": key, "requirements": refs}, http.StatusOK); len(list(v["requirements"])) != 4 {
		t.Fatalf("ripetuto: %v", v["requirements"])
	}
}
