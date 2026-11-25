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
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Requisiti veri dalle regole dei questionari: una derivazione «requirement» non produce una
// prescrizione da accettare ma un «requisito proposto» che, alla chiusura della valutazione
// (esito favorevole o con prescrizioni), diventa un requisito del modulo requisiti: di
// applicazione per la valutazione di un impatto, di progetto per quella della richiesta.
func TestRequisitiDaQuestionari(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("reqq-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision, requirements.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Requisiti dai questionari", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add("req", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})
	add("dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	add("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	add("lead", tenant.MemberInput{Roles: []string{"viewer", "assessor"}})
	add("arch", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Architetture"}})
	add("comp", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Compliance"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	rq := requirements.Register(eng, ap)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Reviews: rev, Requirements: rq,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, req, man, sponsor, lead, arch, comp := u("admin"), u("req"), u("dm"), u("sponsor"), u("lead"), u("arch"), u("comp")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Un modello con due regole «requirement» e una «prescription», sui tipi di valutazione
	// di un impatto (design review) e della richiesta (parere compliance).
	tpl := map[string]any{
		"name": "Requisiti dal questionario",
		"sections": []any{map[string]any{"key": "generale", "title": "Generale", "questions": []any{
			map[string]any{"key": "mfa", "label": "MFA attiva?", "type": "yes_no", "required": true},
			map[string]any{"key": "cifr", "label": "Dati cifrati?", "type": "yes_no", "required": true},
		}}},
		"derivations": []any{
			map[string]any{"key": "r_mfa", "when": "mfa: no", "kind": "requirement", "title": "MFA obbligatoria", "text": "Attivare la MFA per tutti gli accessi.", "category": "Identità"},
			map[string]any{"key": "r_cifr", "when": "cifr: no", "kind": "requirement", "text": "Cifrare i dati a riposo."},
			map[string]any{"key": "p_mfa", "when": "mfa: no", "text": "Valutare un token hardware."},
		},
	}
	// Titolo e metodo valgono solo per i requisiti; l'evidenza vuole il controllo.
	bad := map[string]any{"name": "x", "sections": tpl["sections"], "derivations": []any{
		map[string]any{"key": "a", "when": "mfa: no", "text": "t", "title": "solo per i requisiti"},
		map[string]any{"key": "b", "when": "mfa: no", "text": "t", "kind": "requirement", "method": "evidence"},
	}}
	if p := jsonString(admin.do("POST", "/api/v1/review-config/templates/validate", map[string]any{"definition": bad}, http.StatusOK)["problems"]); !strings.Contains(p, "derivations[0].kind") || !strings.Contains(p, "derivations[1].check") {
		t.Fatalf("validazione delle regole: %s", p)
	}
	admin.do("PUT", "/api/v1/review-config/templates/quest_req/draft", map[string]any{"definition": tpl, "notes": "prima"}, http.StatusOK)
	admin.do("POST", "/api/v1/review-config/templates/quest_req/publish", map[string]any{"notes": "v1"}, http.StatusOK)
	for _, x := range list(admin.do("GET", "/api/v1/review-config", nil, http.StatusOK)["types"]) {
		if k := obj(x)["key"]; k == "design_review" || k == "parere_compliance" {
			admin.do("PATCH", "/api/v1/review-config/types/"+obj(x)["id"].(string), map[string]any{"template_key": "quest_req"}, http.StatusOK)
		}
	}

	// --- Richiesta con un impatto, fino alla Valutazione.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\nFE;Frontend;alta;sì;personali;Web;Area clienti\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	var appID string
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		appID = obj(a)["id"].(string)
	}
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Area clienti", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	imp := man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": appID, "estimate_effort_days": 5}, http.StatusCreated)
	feKey := imp["key"].(string)
	move := func(x *user, id, to string, status int) map[string]any {
		return x.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)
	// Requisiti con origine questionario nella matrice di un oggetto (richiesta o impatto).
	fromQ := func(ref string) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, r := range list(man.do("GET", "/api/v1/elements/"+ref+"/requirements", nil, http.StatusOK)["rows"]) {
			if x := obj(r); x["origin"] == "questionnaire" && (ref != brKey || x["scope"] == "project") {
				out[x["title"].(string)+"/"+jsonString(x["impact_key"])] = x
			}
		}
		return out
	}
	var designKey string
	for _, it := range list(man.do("GET", "/api/v1/elements/"+brKey+"/reviews", nil, http.StatusOK)["items"]) {
		if obj(it)["type_key"] == "design_review" && obj(it)["impact_key"] == feKey {
			designKey = obj(it)["key"].(string)
		}
	}
	if designKey == "" {
		t.Fatal("design review non aperta")
	}

	// --- Valutazione dell'impatto: i requisiti sono proposti, non prescrizioni.
	save := func(x *user, key string, answers map[string]any) map[string]any {
		return x.do("PUT", "/api/v1/reviews/"+key+"/answers", map[string]any{"answers": answers}, http.StatusOK)
	}
	v := save(lead, designKey, map[string]any{"mfa": false, "cifr": false})
	props := jsonString(v["requirement_proposals"])
	if len(list(v["requirement_proposals"])) != 2 || !strings.Contains(props, "MFA obbligatoria") || !strings.Contains(props, "Cifrare i dati a riposo") || !strings.Contains(props, `"category":"Requisito"`) ||
		len(list(obj(v["evaluation"])["derived_requirements"])) != 2 || len(list(obj(v["evaluation"])["derived"])) != 1 {
		t.Fatalf("requisiti proposti: %s %v", props, v["evaluation"])
	}
	if p := jsonString(v["prescriptions"]); !strings.Contains(p, "token hardware") || strings.Contains(p, "Attivare la MFA") || strings.Contains(p, "Cifrare") {
		t.Fatalf("le prescrizioni restano solo quelle di kind prescription: %s", p)
	}
	// La proposta non più giustificata dalle risposte sparisce da sola.
	v = save(lead, designKey, map[string]any{"cifr": true})
	if len(list(v["requirement_proposals"])) != 1 {
		t.Fatalf("proposta da togliere: %v", v["requirement_proposals"])
	}
	v = save(lead, designKey, map[string]any{"cifr": false})
	if len(list(v["requirement_proposals"])) != 2 || len(fromQ(feKey)) != 0 {
		t.Fatalf("prima della chiusura non ci sono requisiti: %v", fromQ(feKey))
	}
	id := obj(v["element"])["id"].(string)
	move(lead, id, "Inviata al team", http.StatusOK)
	move(arch, id, "In esame", http.StatusOK)
	for _, p := range list(arch.do("GET", "/api/v1/reviews/"+designKey, nil, http.StatusOK)["prescriptions"]) {
		arch.do("PATCH", "/api/v1/reviews/"+designKey+"/prescriptions/"+obj(p)["id"].(string), map[string]any{"status": "accepted"}, http.StatusOK)
	}
	arch.do("POST", "/api/v1/reviews/"+designKey+"/decision", map[string]any{"outcome": "favorevole_con_prescrizioni", "note": "Ok"}, http.StatusOK)
	got := fromQ(feKey)
	mfa, cifr := got["MFA obbligatoria/\""+feKey+"\""], got["Cifrare i dati a riposo/\""+feKey+"\""]
	if len(got) != 2 || mfa == nil || cifr == nil || mfa["scope"] != "application" || mfa["category"] != "Identità" || cifr["category"] != "Requisito" {
		t.Fatalf("requisiti di applicazione dal questionario: %v", got)
	}
	info := obj(arch.do("GET", "/api/v1/requirements/"+mfa["key"].(string), nil, http.StatusOK)["requirement"])
	if info["review_key"] != designKey || info["verifier_team"] != "Architetture" || info["verification_method"] != "manual" || info["origin"] != "questionnaire" {
		t.Fatalf("requisito dal questionario: %v", info)
	}
	if l := jsonString(man.do("GET", "/api/v1/elements/"+mfa["key"].(string)+"/links", nil, http.StatusOK)); !strings.Contains(l, designKey) || !strings.Contains(l, "nasce da") {
		t.Fatalf("relazione nasce da: %s", l)
	}
	// Anche la prescrizione accettata diventa un requisito, con la sua origine.
	prescOrigin := 0
	for _, r := range list(man.do("GET", "/api/v1/elements/"+feKey+"/requirements", nil, http.StatusOK)["rows"]) {
		if obj(r)["origin"] == "prescription" && strings.Contains(obj(r)["title"].(string), "token hardware") {
			prescOrigin++
		}
	}
	if prescOrigin != 1 {
		t.Fatalf("la prescrizione accettata resta un requisito con origine prescription: %d", prescOrigin)
	}
	// La valutazione dopo la chiusura mostra a quali requisiti hanno portato le regole.
	if p := jsonString(arch.do("GET", "/api/v1/reviews/"+designKey, nil, http.StatusOK)["requirement_proposals"]); !strings.Contains(p, mfa["key"].(string)) {
		t.Fatalf("proposte dopo la chiusura: %s", p)
	}

	// --- Idempotenza: rieseguire la generazione non duplica niente.
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		keys, err := rq.FromReview(ctx, tx, engine.Actor{MemberID: admin.member, Permissions: []string{"*"}}, id)
		if err == nil && len(keys) != 0 {
			t.Errorf("requisiti duplicati: %v", keys)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fromQ(feKey)) != 2 {
		t.Fatalf("requisiti dopo la rigenerazione: %v", fromQ(feKey))
	}

	// --- Valutazione della richiesta: non favorevole non crea nulla, poi favorevole crea un requisito di progetto.
	openCompliance := func() (string, string) {
		// Il parere è già aperto dalla regola (funzione critica); dopo l'esito non favorevole se ne apre uno nuovo.
		for _, it := range list(man.do("GET", "/api/v1/elements/"+brKey+"/reviews", nil, http.StatusOK)["items"]) {
			if x := obj(it); x["type_key"] == "parere_compliance" && x["stage"] != "concluded" && x["stage"] != "cancelled" {
				return x["key"].(string), obj(man.do("GET", "/api/v1/reviews/"+x["key"].(string), nil, http.StatusOK)["element"])["id"].(string)
			}
		}
		r := man.do("POST", "/api/v1/elements/"+brKey+"/reviews", map[string]any{"type": "parere_compliance"}, http.StatusCreated)
		return obj(r["element"])["key"].(string), obj(r["element"])["id"].(string)
	}
	conclude := func(key, rid, outcome string) {
		move(man, rid, "Inviata al team", http.StatusOK)
		move(comp, rid, "In esame", http.StatusOK)
		for _, p := range list(comp.do("GET", "/api/v1/reviews/"+key, nil, http.StatusOK)["prescriptions"]) {
			comp.do("PATCH", "/api/v1/reviews/"+key+"/prescriptions/"+obj(p)["id"].(string), map[string]any{"status": "rejected"}, http.StatusOK)
		}
		comp.do("POST", "/api/v1/reviews/"+key+"/decision", map[string]any{"outcome": outcome, "note": "Esito"}, http.StatusOK)
	}
	key1, rid1 := openCompliance()
	save(man, key1, map[string]any{"mfa": false, "cifr": false})
	conclude(key1, rid1, "non_favorevole")
	if got := fromQ(brKey); len(got) != 0 {
		t.Fatalf("un esito non favorevole non crea requisiti: %v", got)
	}
	key2, rid2 := openCompliance()
	v = save(man, key2, map[string]any{"mfa": false, "cifr": true})
	if len(list(v["requirement_proposals"])) != 1 {
		t.Fatalf("proposte della richiesta: %v", v["requirement_proposals"])
	}
	conclude(key2, rid2, "favorevole")
	got = fromQ(brKey)
	proj := got["MFA obbligatoria/null"]
	if len(got) != 1 || proj == nil || proj["scope"] != "project" {
		t.Fatalf("requisito di progetto dal questionario: %v", got)
	}
	info = obj(comp.do("GET", "/api/v1/requirements/"+proj["key"].(string), nil, http.StatusOK)["requirement"])
	if info["review_key"] != key2 || info["verifier_team"] != "Compliance" {
		t.Fatalf("requisito di progetto: %v", info)
	}
	// Le prescrizioni già create dalle regole non si toccano: quelle della v1 non esistono qui,
	// e la valutazione non favorevole conserva le sue proposte senza requisiti.
	if p := jsonString(comp.do("GET", "/api/v1/reviews/"+key1, nil, http.StatusOK)["requirement_proposals"]); !strings.Contains(p, `"requirement":null`) {
		t.Fatalf("proposte della valutazione non favorevole: %s", p)
	}
}
