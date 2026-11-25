package itest

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
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

// Spazio dell'auditor (ondata 3, C6): l'auditor apre un incarico, estrae un campione ripetibile,
// chiede un'evidenza, apre un rilievo e lo chiude dopo il piano del responsabile; non può
// modificare un controllo; il responsabile risponde e riceve le notifiche.
func TestGRCSpazioAuditor(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("audit-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision, grc.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Audit", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"co", "co2", "own", "aud"} {
		in := tenant.MemberInput{TenantSlug: slug, Email: n + "@" + slug + ".test", Name: n, Password: testPassword, Roles: []string{"compliance_officer"}, Groups: []string{"Compliance"}}
		switch n {
		case "own":
			in.Roles, in.Groups = []string{"editor"}, nil
		case "aud":
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
	login2 := func(n string) *user { return login(t, srv.URL, n+"@"+slug+".test", testPassword) }
	co, co2, own, aud := login2("co"), login2("co2"), login2("own"), login2("aud")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	const B = "/api/v1/grc/audit"

	co.do("POST", "/api/v1/grc/starters/dora", map[string]any{"enable": true}, http.StatusCreated)
	ctl := co.do("POST", "/api/v1/grc/controls", map[string]any{"title": "Prove di ripristino", "owner_id": own.member,
		"requirements": []any{map[string]any{"ref": "dora:Art. 9"}}}, http.StatusCreated)
	key := ctl["key"].(string)
	// Sei evidenze valide del controllo (collegamenti, validate da un'altra persona).
	var evKeys []string
	for i := 0; i < 6; i++ {
		ev := own.do("POST", "/api/v1/grc/evidence", map[string]any{"controls": []string{key}, "kind": "link", "title": "Verbale " + string(rune('A'+i)),
			"url": "https://ci.example.test/v/" + string(rune('a'+i))}, http.StatusCreated)
		co.do("POST", "/api/v1/grc/evidence/"+ev["key"].(string)+"/validate", map[string]any{}, http.StatusOK)
		evKeys = append(evKeys, ev["key"].(string))
	}
	year := time.Now().Year()
	period := map[string]any{"period_from": time.Now().AddDate(0, -3, 0).Format(time.DateOnly), "period_to": time.Now().AddDate(0, 3, 0).Format(time.DateOnly)}
	mk := func(extra map[string]any) map[string]any {
		in := map[string]any{"title": "Revisione DORA " + string(rune('0'+year%10))}
		for k, v := range period {
			in[k] = v
		}
		for k, v := range extra {
			in[k] = v
		}
		return in
	}

	// Chi non ha grc.audit non scrive; periodo e norma si controllano.
	co.do("POST", B+"/engagements", mk(nil), http.StatusForbidden)
	own.do("GET", B+"/home", nil, http.StatusForbidden)
	aud.do("POST", B+"/engagements", mk(map[string]any{"period_to": "2020-01-01"}), http.StatusUnprocessableEntity)
	aud.do("POST", B+"/engagements", mk(map[string]any{"framework": "inesistente"}), http.StatusUnprocessableEntity)
	inc := aud.do("POST", B+"/engagements", mk(map[string]any{"framework": "dora", "controls": []string{key}}), http.StatusCreated)
	id := inc["id"].(string)
	if inc["controls"].(float64) != 1 || inc["reviewed"].(float64) != 0 || inc["status"] != "open" {
		t.Fatalf("incarico: %v", inc)
	}
	home := aud.do("GET", B+"/home", nil, http.StatusOK)
	if obj(home["current"])["id"] != id || home["can_audit"] != true {
		t.Fatalf("home: %v", home)
	}

	// Campione ripetibile: stesso seme, stesse evidenze; seme registrato.
	aud.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 0}, http.StatusUnprocessableEntity)
	co.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 3}, http.StatusForbidden)
	s1 := aud.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 3, "seed": 42}, http.StatusCreated)
	s2 := aud.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 3, "seed": 42}, http.StatusCreated)
	s3 := aud.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 3}, http.StatusCreated)
	keysOf := func(s map[string]any) string {
		var out []string
		for _, it := range list(s["items"]) {
			out = append(out, obj(obj(it)["evidence"])["key"].(string))
		}
		return strings.Join(out, ",")
	}
	if len(list(s1["items"])) != 3 || s1["population"].(float64) != 6 || s1["seed"].(float64) != 42 || keysOf(s1) != keysOf(s2) || s3["seed"].(float64) == 0 {
		t.Fatalf("campioni: %v %v %v", keysOf(s1), keysOf(s2), s3["seed"])
	}
	sid := s1["id"].(string)
	aud.do("PUT", B+"/samples/"+sid+"/items/1", map[string]any{"outcome": "non_compliant"}, http.StatusUnprocessableEntity) // serve la nota
	aud.do("PUT", B+"/samples/"+sid+"/items/1", map[string]any{"outcome": "compliant"}, http.StatusOK)
	smp := aud.do("PUT", B+"/samples/"+sid+"/items/2", map[string]any{"outcome": "non_compliant", "note": "Verbale senza firma"}, http.StatusOK)
	if obj(list(smp["items"])[1])["outcome"] != "non_compliant" {
		t.Fatalf("esito: %v", smp)
	}
	co.do("PUT", B+"/samples/"+sid+"/items/3", map[string]any{"outcome": "compliant"}, http.StatusForbidden)

	// Richiesta di evidenza al responsabile, con scadenza; lui risponde con un link.
	due := time.Now().AddDate(0, 0, 7).Format(time.DateOnly)
	rq1 := aud.do("POST", B+"/engagements/"+id+"/requests", map[string]any{"control": key, "evidence": evKeys[1], "title": "Verbale firmato", "body": "Serve la versione firmata.", "due": due}, http.StatusCreated)
	rid := rq1["id"].(string)
	if rq1["assignee"] != "own" || rq1["status"] != "open" || rq1["due"] != due {
		t.Fatalf("richiesta: %v", rq1)
	}
	found := false
	for _, n := range list(own.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"]) {
		if strings.Contains(jsonString(n), "Verbale firmato") {
			found = true
		}
	}
	if !found {
		t.Fatal("il responsabile non è stato avvisato della richiesta")
	}
	if l := list(own.do("GET", B+"/requests/mine", nil, http.StatusOK)["items"]); len(l) != 1 {
		t.Fatalf("richieste mie: %v", l)
	}
	co2.do("POST", B+"/requests/"+rid+"/answer", map[string]any{"response": "ok"}, http.StatusOK) // la compliance può rispondere
	rq2 := aud.do("POST", B+"/engagements/"+id+"/requests", map[string]any{"control": key, "title": "Piano di ripristino"}, http.StatusCreated)
	aud.do("POST", B+"/requests/"+rq2["id"].(string)+"/answer", map[string]any{"response": "x"}, http.StatusForbidden)
	own.do("POST", B+"/requests/"+rq2["id"].(string)+"/answer", map[string]any{}, http.StatusUnprocessableEntity)
	own.do("POST", B+"/requests/"+rq2["id"].(string)+"/answer", map[string]any{"url": "ftp://x"}, http.StatusUnprocessableEntity)
	ans := own.do("POST", B+"/requests/"+rq2["id"].(string)+"/answer", map[string]any{"response": "Allegato nel verbale", "url": "https://wiki.example.test/ripristino", "evidence": evKeys[0]}, http.StatusOK)
	if ans["status"] != "answered" || obj(ans["response_evidence"])["key"] != evKeys[0] {
		t.Fatalf("risposta: %v", ans)
	}
	own.do("POST", B+"/requests/"+rq2["id"].(string)+"/answer", map[string]any{"response": "ancora"}, http.StatusUnprocessableEntity) // non più aperta
	aud.do("POST", B+"/requests/"+rq2["id"].(string)+"/close", nil, http.StatusOK)

	// Commento in linea sul controllo e sull'evidenza.
	aud.do("POST", "/api/v1/grc/controls/"+key+"/audit/comments", map[string]any{"body": "Il verbale B non è firmato.", "evidence": evKeys[1]}, http.StatusCreated)
	aud.do("POST", "/api/v1/grc/controls/"+key+"/audit/comments", map[string]any{"body": ""}, http.StatusUnprocessableEntity)
	cm := own.do("POST", "/api/v1/grc/controls/"+key+"/audit/comments", map[string]any{"body": "Lo firmiamo domani."}, http.StatusCreated)
	if len(list(cm["items"])) != 2 {
		t.Fatalf("commenti: %v", cm)
	}

	// Rilievo: aperto dall'auditor, piano del responsabile, chiusura dell'auditor.
	aud.do("POST", B+"/engagements/"+id+"/findings", map[string]any{"control": key, "severity": "grave", "title": "x"}, http.StatusUnprocessableEntity)
	own.do("POST", B+"/engagements/"+id+"/findings", map[string]any{"control": key, "severity": "high", "title": "x"}, http.StatusForbidden)
	fd := aud.do("POST", B+"/engagements/"+id+"/findings", map[string]any{"control": key, "severity": "high", "title": "Verbali non firmati", "description": "Due verbali su sei senza firma."}, http.StatusCreated)
	fid := fd["id"].(string)
	aud.do("POST", B+"/findings/"+fid+"/plan", map[string]any{"action_plan": "x"}, http.StatusForbidden)
	own.do("POST", B+"/findings/"+fid+"/plan", map[string]any{}, http.StatusUnprocessableEntity)
	pl := own.do("POST", B+"/findings/"+fid+"/plan", map[string]any{"action_plan": "Firma obbligatoria nel modello.", "action_due": due}, http.StatusOK)
	if pl["status"] != "plan_proposed" {
		t.Fatalf("piano: %v", pl)
	}
	own.do("POST", B+"/findings/"+fid+"/close", map[string]any{"note": "fatto"}, http.StatusForbidden)
	aud.do("POST", B+"/findings/"+fid+"/close", map[string]any{}, http.StatusUnprocessableEntity)
	cl := aud.do("POST", B+"/findings/"+fid+"/close", map[string]any{"note": "Piano attuato, verificato a campione."}, http.StatusOK)
	if cl["status"] != "closed" {
		t.Fatalf("chiusura: %v", cl)
	}
	aud.do("POST", B+"/findings/"+fid+"/close", map[string]any{"note": "ancora"}, http.StatusUnprocessableEntity)

	// La scheda del controllo mostra tutto; l'avanzamento sale con l'esame.
	ca := own.do("GET", "/api/v1/grc/controls/"+key+"/audit", nil, http.StatusOK)
	if len(list(ca["findings"])) != 1 || len(list(ca["requests"])) != 2 || len(list(ca["samples"])) != 3 || obj(ca["can"])["respond"] != true {
		t.Fatalf("scheda audit: %v", ca)
	}
	aud.do("PUT", B+"/engagements/"+id+"/controls/"+key+"/review", map[string]any{"conclusion": "bad"}, http.StatusUnprocessableEntity)
	rv := aud.do("PUT", B+"/engagements/"+id+"/controls/"+key+"/review", map[string]any{"conclusion": "reviewed", "note": "Esaminato."}, http.StatusOK)
	if rv["reviewed"].(float64) != 1 || obj(list(rv["control_list"])[0])["conclusion"] != "reviewed" {
		t.Fatalf("esame: %v", rv)
	}

	// L'auditor non modifica i controlli né le evidenze.
	aud.do("PATCH", "/api/v1/grc/controls/"+key, map[string]any{"frequency": "monthly"}, http.StatusForbidden)
	aud.do("POST", "/api/v1/grc/controls/"+key+"/assessment", map[string]any{"effectiveness": "effective"}, http.StatusForbidden)
	aud.do("POST", "/api/v1/grc/evidence/"+evKeys[0]+"/revoke", map[string]any{"note": "x"}, http.StatusForbidden)

	// Pacchetto dell'incarico: zip con campioni, richieste e rilievi.
	resp, err := aud.c.Get(srv.URL + B + "/engagements/" + id + "/pack")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pacchetto: %d %s", resp.StatusCode, raw)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, n := range []string{"campioni.csv", "richieste.csv", "rilievi.csv", "evidenze.csv", "LEGGIMI.txt"} {
		if !names[n] {
			t.Fatalf("manca %s nel pacchetto: %v", n, names)
		}
	}

	// Incarico chiuso: niente scrittura nuova.
	aud.do("POST", B+"/engagements/"+id+"/close", nil, http.StatusOK)
	aud.do("POST", B+"/engagements/"+id+"/samples", map[string]any{"control": key, "size": 2}, http.StatusUnprocessableEntity)
	aud.do("POST", B+"/engagements/"+id+"/findings", map[string]any{"control": key, "severity": "low", "title": "y"}, http.StatusUnprocessableEntity)
}
