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
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/tenant"
)

// Editor dei questionari (piano, passo 7): scala e matrice, punteggio e soglie, sezioni solo
// del team, fatti per il costruttore di condizioni, versioni (le valutazioni aperte restano
// sulla loro) e validazione dal vivo.
func TestQuestionari(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("quest-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision}
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Questionari", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	}); err != nil {
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
	add("lead", tenant.MemberInput{Roles: []string{"viewer"}})
	add("arch", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Architetture"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Reviews: rev,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, req, man, sponsor, lead, arch := u("admin"), u("req"), u("dm"), u("sponsor"), u("lead"), u("arch")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Un modello con scala, matrice, punteggio, soglie e una sezione solo del team.
	opt := func(v, l string, score any) map[string]any {
		o := map[string]any{"value": v, "label": l}
		if score != nil {
			o["score"] = score
		}
		return o
	}
	tpl := func(name string, extra ...map[string]any) map[string]any {
		d := map[string]any{
			"name": name,
			"sections": []any{
				map[string]any{"key": "generale", "title": "Generale", "questions": []any{
					map[string]any{"key": "mfa", "label": "MFA attiva?", "type": "yes_no", "required": true, "yes_score": 10, "no_score": 0},
					map[string]any{"key": "maturita", "label": "Maturità", "type": "scale", "min": 1, "max": 5, "min_label": "bassa", "max_label": "alta", "weight": 2},
					map[string]any{"key": "senza_mfa", "label": "Perché no?", "type": "text", "visible_when": "mfa: no"},
				}},
				map[string]any{"key": "tecnica", "title": "Verifica tecnica", "filled_by": "team", "questions": []any{
					map[string]any{"key": "controlli", "label": "Controlli", "type": "matrix",
						"rows":    []any{opt("log", "Log", nil), opt("backup", "Backup", nil)},
						"options": []any{opt("no", "No", 0), opt("si", "Sì", 5)}},
				}},
			},
			"derivations": []any{
				map[string]any{"key": "mfa_obbligatoria", "when": "mfa: no", "text": "Attivare la MFA.", "kind": "requirement"},
			},
			"scoring": map[string]any{"thresholds": []any{
				map[string]any{"min": 0, "outcome": "non_favorevole"},
				map[string]any{"min": 50, "outcome": "favorevole_con_prescrizioni"},
				map[string]any{"min": 80, "outcome": "favorevole"},
			}},
		}
		for _, e := range extra {
			for k, v := range e {
				d[k] = v
			}
		}
		return d
	}
	probs := func(def map[string]any) string {
		return jsonString(admin.do("POST", "/api/v1/review-config/templates/validate", map[string]any{"definition": def}, http.StatusOK)["problems"])
	}
	if p := probs(tpl("Prova")); strings.Contains(p, `"error"`) {
		t.Fatalf("modello valido con errori: %s", p)
	}
	// Dati sbagliati: sezione per «tutti», scala invertita, soglia fuori misura, tipo sconosciuto, esito non valido.
	bad := tpl("Prova", map[string]any{"scoring": map[string]any{"thresholds": []any{map[string]any{"min": 150, "outcome": "Bad Key"}}}})
	bad["sections"].([]any)[0].(map[string]any)["filled_by"] = "tutti"
	bad["sections"].([]any)[0].(map[string]any)["questions"].([]any)[1].(map[string]any)["min"] = 5
	p := probs(bad)
	for _, want := range []string{"filled_by", "scala", "tra 0 e 100", "non valida"} {
		if !strings.Contains(p, want) {
			t.Errorf("la validazione non segnala %q: %s", want, p)
		}
	}

	// --- Fatti per il costruttore e permessi dell'editor.
	facts := admin.do("GET", "/api/v1/review-config/template-facts", nil, http.StatusOK)
	if !strings.Contains(jsonString(facts["request"]), `"key":"stato"`) || !strings.Contains(jsonString(facts["impact"]), `"key":"richiesta.stato"`) {
		t.Fatalf("fatti per le condizioni: %s", jsonString(facts))
	}
	man.do("GET", "/api/v1/review-config/template-facts", nil, http.StatusForbidden)
	man.do("PUT", "/api/v1/review-config/templates/quest_prova/draft", map[string]any{"definition": tpl("X")}, http.StatusForbidden)

	// --- Bozza, pubblicazione e versione 2.
	admin.do("PUT", "/api/v1/review-config/templates/quest_prova/draft", map[string]any{"definition": tpl("Modello di prova"), "notes": "prima"}, http.StatusOK)
	admin.do("POST", "/api/v1/review-config/templates/quest_prova/publish", map[string]any{"notes": "v1"}, http.StatusOK)
	// Il modello torna con scala, matrice e soglie: nessun campo perso nel giro.
	got := admin.do("GET", "/api/v1/review-config/templates/quest_prova/versions/1", nil, http.StatusOK)
	if s := jsonString(got["definition"]); !strings.Contains(s, `"filled_by":"team"`) || !strings.Contains(s, `"type":"matrix"`) || !strings.Contains(s, `"thresholds"`) || !strings.Contains(s, `"kind":"requirement"`) {
		t.Fatalf("definizione salvata: %s", s)
	}
	// Un errore non impedisce di salvare la bozza ma blocca la pubblicazione.
	admin.do("PUT", "/api/v1/review-config/templates/quest_prova/draft", map[string]any{"definition": bad}, http.StatusOK)
	admin.do("POST", "/api/v1/review-config/templates/quest_prova/publish", map[string]any{}, http.StatusUnprocessableEntity)
	admin.do("DELETE", "/api/v1/review-config/templates/quest_prova/draft", nil, http.StatusNoContent)

	// Il tipo «design_review» usa il nuovo modello.
	var typeID string
	for _, x := range list(admin.do("GET", "/api/v1/review-config", nil, http.StatusOK)["types"]) {
		if obj(x)["key"] == "design_review" {
			typeID = obj(x)["id"].(string)
		}
	}
	admin.do("PATCH", "/api/v1/review-config/types/"+typeID, map[string]any{"template_key": "quest_prova"}, http.StatusOK)

	// --- Una valutazione apre il modello v1.
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
	move := func(u *user, id, to string, status int) map[string]any {
		return u.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, status)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)
	var key string
	for _, it := range list(man.do("GET", "/api/v1/elements/"+brKey+"/reviews", nil, http.StatusOK)["items"]) {
		if obj(it)["type_key"] == "design_review" && obj(it)["impact_key"] == imp["key"] {
			key = obj(it)["key"].(string)
		}
	}
	if key == "" {
		t.Fatal("design review non aperta")
	}
	save := func(u *user, answers map[string]any, status int) map[string]any {
		return u.do("PUT", "/api/v1/reviews/"+key+"/answers", map[string]any{"answers": answers}, status)
	}

	// --- Sezione solo del team: chi ha avviato il questionario compila le altre, non quella.
	v := lead.do("GET", "/api/v1/reviews/"+key, nil, http.StatusOK)
	if can := obj(v["can"]); can["answer_all"] != false || !strings.Contains(jsonString(can["sections"]), "generale") || strings.Contains(jsonString(can["sections"]), "tecnica") {
		t.Fatalf("permessi con una sezione del team: %v", can)
	}
	save(lead, map[string]any{"controlli": map[string]any{"log": "si"}}, http.StatusForbidden)
	v = save(lead, map[string]any{"mfa": false, "maturita": 3.0}, http.StatusOK)
	ev := obj(v["evaluation"])
	// MFA no: 0/10; maturità 3 → 2 punti su 4, peso 2 → 4/8; matrice: 0/10. Totale 4/28.
	if ev["scored"] != true || ev["score"] != 4.0 || ev["max_score"] != 28.0 || ev["proposed_outcome"] != "non_favorevole" {
		t.Fatalf("punteggio: %v", ev)
	}
	// Una regola «requirement» propone un requisito, non una prescrizione da accettare.
	if strings.Contains(jsonString(v["prescriptions"]), "Attivare la MFA") ||
		!strings.Contains(jsonString(v["requirement_proposals"]), "Attivare la MFA") || !strings.Contains(jsonString(v["requirement_proposals"]), "Requisito") {
		t.Fatalf("requisito proposto dalla risposta: %v %v", v["prescriptions"], v["requirement_proposals"])
	}
	save(lead, map[string]any{"maturita": 2.5}, http.StatusUnprocessableEntity) // la scala è intera
	save(lead, map[string]any{"maturita": 9.0}, http.StatusUnprocessableEntity) // fuori scala
	// Il team compila la matrice: risposta incompleta non conta come risposta ma dà punti.
	v = save(arch, map[string]any{"controlli": map[string]any{"log": "si"}}, http.StatusOK)
	if ev = obj(v["evaluation"]); ev["score"] != 9.0 {
		t.Fatalf("punteggio con la matrice a metà: %v", ev)
	}
	save(arch, map[string]any{"controlli": map[string]any{"log": "boh"}}, http.StatusUnprocessableEntity)
	v = save(arch, map[string]any{"mfa": true, "controlli": map[string]any{"log": "si", "backup": "si"}}, http.StatusOK)
	ev = obj(v["evaluation"])
	if ev["score"] != 24.0 || ev["max_score"] != 28.0 || ev["percent"] != 85.7 || ev["proposed_outcome"] != "favorevole" {
		t.Fatalf("punteggio pieno: %v", ev)
	}
	if strings.Contains(jsonString(v["requirement_proposals"]), "Attivare la MFA") {
		t.Fatalf("la proposta doveva sparire con la risposta: %v", v["requirement_proposals"])
	}

	// --- Versione 2: le valutazioni già aperte restano sulla loro.
	v2 := tpl("Modello di prova v2")
	v2["sections"].([]any)[0].(map[string]any)["questions"].([]any)[1].(map[string]any)["weight"] = 0
	admin.do("PUT", "/api/v1/review-config/templates/quest_prova/draft", map[string]any{"definition": v2, "notes": "peso a zero"}, http.StatusOK)
	pub := admin.do("POST", "/api/v1/review-config/templates/quest_prova/publish", map[string]any{}, http.StatusOK)
	if obj(pub["current"])["version"] != 2.0 || len(list(pub["versions"])) != 2 {
		t.Fatalf("pubblicazione della v2: %v", pub["current"])
	}
	v = arch.do("GET", "/api/v1/reviews/"+key, nil, http.StatusOK)
	if obj(v["review"])["template_version"] != 1.0 || obj(v["evaluation"])["max_score"] != 28.0 {
		t.Fatalf("la valutazione aperta deve restare sulla v1: %v %v", v["review"], v["evaluation"])
	}
	if obj(admin.do("GET", "/api/v1/review-config/templates/quest_prova/versions/1", nil, http.StatusOK)["definition"])["name"] != "Modello di prova" {
		t.Fatal("la v1 è cambiata")
	}
}
