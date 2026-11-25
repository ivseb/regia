package itest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Valutazioni con questionari (piano, passo 4): regole che aprono le valutazioni per la
// richiesta e per ogni impatto, modelli versionati e immutabili, condizioni di visibilità,
// precompilazioni, derivazione delle prescrizioni, permessi (compilano i compilatori,
// decide solo il team), SLA in giorni lavorativi con sospensione ed escalation, fatti e
// gate sulla richiesta, storia delle risposte, isolamento tra tenant.
func TestReviews(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("rev-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, reviews.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Valutazioni", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
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
	add("lead", tenant.MemberInput{Roles: []string{"viewer"}})
	add("curioso", tenant.MemberInput{Roles: []string{"viewer"}})
	add("sec1", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Sicurezza"}})
	add("sec2", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Sicurezza"}})
	add("dpo", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Privacy (DPO)"}})
	add("priv", tenant.MemberInput{Roles: []string{"assessor"}, Groups: []string{"Privacy (DPO)"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	rev := reviews.Register(eng)
	var clock atomic.Int64
	setDay := func(s string) {
		day, _ := time.Parse(time.DateOnly, s)
		clock.Store(day.Add(9 * time.Hour).Unix())
	}
	setDay("2026-12-04") // venerdì; l'8 dicembre è festivo
	rev.Now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Reviews: rev,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, req, man, sponsor, lead, curioso := u("admin"), u("req"), u("dm"), u("sponsor"), u("lead"), u("curioso")
	sec1, sec2, dpo, priv := u("sec1"), u("sec2"), u("dpo"), u("priv")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- Pacchetto di partenza: team, tipi, modelli, regole e gate nei processi.
	cfg := admin.do("GET", "/api/v1/review-config", nil, http.StatusOK)
	if len(list(cfg["teams"])) != 4 || len(list(cfg["types"])) != 4 || len(list(cfg["templates"])) != 4 {
		t.Fatalf("configurazione di partenza: %d team, %d tipi, %d modelli", len(list(cfg["teams"])), len(list(cfg["types"])), len(list(cfg["templates"])))
	}
	// «Quando si apre»: le regole dei processi che aprono ogni tipo di valutazione.
	trig := jsonString(cfg["triggers"])
	for _, want := range []string{"dpia_preliminare", "parere_compliance", "security_review", "demand_standard"} {
		if !strings.Contains(trig, want) {
			t.Fatalf("regole che aprono le valutazioni senza %s: %s", want, trig)
		}
	}
	proc := admin.do("GET", "/api/v1/processes/demand_standard", nil, http.StatusOK)
	cur := obj(proc["current"])
	hasGate := false
	for _, tr := range list(cur["transitions"]) {
		m := obj(tr)
		if m["to"] == "Approvazione" && strings.Contains(strings.Join(toStrings(m["gates"]), ","), "valutazioni") {
			hasGate = true
		}
	}
	if !hasGate || len(list(cur["rules"])) < 7 || len(list(proc["versions"])) != 2 {
		t.Fatalf("processo della domanda senza gate o regole delle valutazioni: %v", cur["rules"])
	}
	// Un'azione open_review con un tipo inesistente non passa la validazione.
	bad := cur
	bad["rules"] = append(list(cur["rules"]), map[string]any{"key": "prova", "name": "Prova", "on": "enter:Triage",
		"actions": []any{map[string]any{"kind": "open_review", "ref": "inesistente"}}})
	dr := admin.do("PUT", "/api/v1/processes/demand_standard/draft", map[string]any{"definition": bad}, http.StatusOK)
	if !strings.Contains(jsonString(dr["draft_problems"]), "inesistente") {
		t.Fatalf("azione non validata: %v", dr["draft_problems"])
	}
	admin.do("DELETE", "/api/v1/processes/demand_standard/draft", nil, http.StatusNoContent)
	if acts := admin.do("GET", "/api/v1/processes", nil, http.StatusOK)["actions"]; !strings.Contains(jsonString(acts), "open_review") {
		t.Fatalf("azioni disponibili: %v", acts)
	}

	// Responsabili dei team: sec2 per la Sicurezza, il DPO per la Privacy.
	admin.do("PATCH", "/api/v1/review-config/teams/sicurezza", map[string]any{"lead_id": sec2.member}, http.StatusOK)
	admin.do("PATCH", "/api/v1/review-config/teams/privacy", map[string]any{"lead_id": dpo.member}, http.StatusOK)
	man.do("PATCH", "/api/v1/review-config/teams/privacy", map[string]any{"sla_days": 1}, http.StatusForbidden)

	// --- Catalogo e richiesta con due impatti.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"FE;Frontend area clienti;alta;sì;personali;Web;Area clienti\n" +
		"REP;Reportistica;bassa;no;aggregati;Dati;Area clienti\n" +
		"PAY;Pagamenti;critica;no;finanziari;Payments;Area clienti\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)["id"].(string)
	}
	br := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Area clienti rinnovata", "sponsor_id": sponsor.member}, http.StatusCreated)
	brKey, brID := br["key"].(string), br["id"].(string)
	impact := func(code string) map[string]any {
		return man.do("POST", "/api/v1/demand/requests/"+brKey+"/impacts", map[string]any{"application_id": apps[code], "estimate_effort_days": 10}, http.StatusCreated)
	}
	fe, rep := impact("FE"), impact("REP")
	move := func(u *user, id, to string, status int, comment ...string) map[string]any {
		body := map[string]any{"to": to}
		if len(comment) > 0 {
			body["comment"] = comment[0]
		}
		return u.do("POST", "/api/v1/elements/"+id+"/transitions", body, status)
	}
	if n := len(list(man.do("GET", "/api/v1/elements/"+brKey+"/reviews", nil, http.StatusOK)["items"])); n != 0 {
		t.Fatalf("valutazioni prima della fase: %d", n)
	}
	move(req, brID, "Inviata", http.StatusOK)
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)

	// --- Le regole aprono: DPIA (dati personali), parere compliance (funzione critica),
	// security review solo per l'applicazione a criticità alta, design review per ognuna.
	type rv struct{ key, typ, impact string }
	reviewsOf := func(ref string) (map[string]rv, map[string]any) {
		out := man.do("GET", "/api/v1/elements/"+ref+"/reviews", nil, http.StatusOK)
		m := map[string]rv{}
		for _, it := range list(out["items"]) {
			x := obj(it)
			imp, _ := x["impact_key"].(string)
			if x["superseded"] == true {
				continue
			}
			m[x["type_key"].(string)+"/"+imp] = rv{x["key"].(string), x["type_key"].(string), imp}
		}
		return m, obj(out["facts"])
	}
	got, facts := reviewsOf(brKey)
	feKey, repKey := fe["key"].(string), rep["key"].(string)
	for _, want := range []string{"dpia_preliminare/", "parere_compliance/", "security_review/" + feKey, "design_review/" + feKey, "design_review/" + repKey} {
		if _, ok := got[want]; !ok {
			t.Fatalf("manca la valutazione %s: %v", want, got)
		}
	}
	if len(got) != 5 || facts["valutazioni"] != 5.0 || facts["valutazioni_obbligatorie_aperte"] != 4.0 {
		t.Fatalf("valutazioni aperte dalle regole: %v, fatti %v", got, facts)
	}
	if g, _ := reviewsOf(repKey); len(g) != 1 {
		t.Fatalf("valutazioni dell'impatto REP: %v", g)
	}
	// Un impatto aggiunto durante la fase riceve le valutazioni delle regole della fase.
	pay := impact("PAY")
	got, _ = reviewsOf(brKey)
	if _, ok := got["security_review/"+pay["key"].(string)]; !ok || len(got) != 7 {
		t.Fatalf("valutazioni dopo il nuovo impatto: %v", got)
	}
	// Rientrando nella fase le regole non riaprono le valutazioni già aperte.
	move(man, brID, "Sospesa", http.StatusOK, "In attesa del budget")
	move(man, brID, "Triage", http.StatusOK)
	move(man, brID, "Valutazione", http.StatusOK)
	if again, _ := reviewsOf(brKey); len(again) != 7 {
		t.Fatalf("valutazioni riaperte rientrando in fase: %d", len(again))
	}

	// --- Precompilazione e condizioni di visibilità sulla security review del frontend.
	secKey := got["security_review/"+feKey].key
	v := lead.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusOK)
	ans := obj(v["answers"])
	if obj(ans["criticita"])["value"] != "alta" || obj(ans["criticita"])["source"] != "prefill" || obj(ans["classificazione_dati"])["value"] != "personali" {
		t.Fatalf("precompilazione: %v", ans)
	}
	if obj(obj(v["review"]))["template_version"] != 1.0 || obj(v["can"])["is_owner"] != true {
		t.Fatalf("valutazione: %v %v", v["review"], v["can"])
	}
	visible := func(v map[string]any, q string) bool { return obj(obj(v["evaluation"])["visible"])[q] == true }
	if visible(v, "autenticazione") || visible(v, "mfa") || visible(v, "elenco_terze_parti") {
		t.Fatalf("domande condizionali visibili troppo presto: %v", obj(v["evaluation"])["visible"])
	}
	save := func(u *user, key string, answers map[string]any, status int) map[string]any {
		return u.do("PUT", "/api/v1/reviews/"+key+"/answers", map[string]any{"answers": answers}, status)
	}
	// Chi non compila non salva; il team valutatore può correggere.
	save(curioso, secKey, map[string]any{"esposto_internet": true}, http.StatusForbidden)
	save(man, secKey, map[string]any{"esposto_internet": true}, http.StatusForbidden)
	v = save(lead, secKey, map[string]any{"esposto_internet": true, "autenticazione": "locale"}, http.StatusOK)
	if !visible(v, "autenticazione") || !visible(v, "mfa") || obj(v["element"])["state"] != "In compilazione" {
		t.Fatalf("dopo «esposto su internet»: %v, fase %v", obj(v["evaluation"])["visible"], obj(v["element"])["state"])
	}
	derived := func(v map[string]any) map[string]string {
		m := map[string]string{}
		for _, p := range list(v["prescriptions"]) {
			if k, ok := obj(p)["rule_key"].(string); ok {
				m[k] = obj(p)["id"].(string)
			}
		}
		return m
	}
	if p := derived(v); p["waf"] == "" || p["sso"] == "" || p["pentest"] == "" {
		t.Fatalf("prescrizioni proposte: %v", v["prescriptions"])
	}
	v = save(lead, secKey, map[string]any{"autenticazione": "nessuna"}, http.StatusOK)
	if visible(v, "mfa") || derived(v)["sso"] != "" {
		t.Fatalf("mfa nascosta e sso tolta senza credenziali locali: %v %v", obj(v["evaluation"])["visible"], derived(v))
	}
	save(lead, secKey, map[string]any{"autenticazione": "boh"}, http.StatusUnprocessableEntity)
	save(lead, secKey, map[string]any{"inesistente": "x"}, http.StatusUnprocessableEntity)

	// Sezione assegnata: il compilatore risponde solo alla sua.
	lead.do("PUT", "/api/v1/reviews/"+secKey+"/assignments/dati", map[string]any{"member_id": curioso.member}, http.StatusOK)
	save(curioso, secKey, map[string]any{"esposto_internet": false}, http.StatusForbidden)
	save(curioso, secKey, map[string]any{"cifratura_riposo": false, "segreti": "config", "terze_parti": true}, http.StatusOK)
	if n := len(list(curioso.do("GET", "/api/v1/reviews/inbox", nil, http.StatusOK)["compile"])); n != 1 {
		t.Fatalf("inbox del compilatore: %d", n)
	}
	// Invio con domande obbligatorie mancanti: il gate lo ferma.
	sec := v["element"].(map[string]any)["id"].(string)
	if r := move(lead, sec, "Inviata al team", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "obbligatorie") {
		t.Fatalf("gate del questionario: %v", r)
	}
	v = save(curioso, secKey, map[string]any{"elenco_terze_parti": "Servizio di invio SMS, solo numero di telefono"}, http.StatusOK)
	if m := list(obj(v["evaluation"])["missing"]); len(m) != 0 {
		t.Fatalf("domande mancanti: %v", m)
	}
	p := derived(v)
	if p["cifratura"] == "" || p["segreti"] == "" {
		t.Fatalf("derivazioni sui dati: %v", p)
	}
	move(lead, sec, "Inviata al team", http.StatusOK)
	v = lead.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusOK)
	if obj(v["review"])["due_on"] != "2026-12-14" || obj(v["review"])["stage"] != "submitted" {
		t.Fatalf("scadenza SLA (5 giorni lavorativi, l'8 è festivo): %v", v["review"])
	}
	save(lead, secKey, map[string]any{"mfa": true}, http.StatusUnprocessableEntity)
	if n := len(list(sec1.do("GET", "/api/v1/reviews/inbox", nil, http.StatusOK)["team"])); n != 1 {
		t.Fatalf("inbox del team: %d", n)
	}

	// --- Esame: solo il team prende in carico, chiede chiarimenti (SLA sospeso) e decide.
	move(lead, sec, "In esame", http.StatusForbidden)
	move(sec1, sec, "In esame", http.StatusOK)
	setDay("2026-12-07")
	move(sec1, sec, "Chiarimenti", http.StatusUnprocessableEntity)
	move(sec1, sec, "Chiarimenti", http.StatusOK, "Che dati passano al servizio SMS?")
	save(lead, secKey, map[string]any{"elenco_terze_parti": "Servizio SMS: solo il numero di telefono, nessun altro dato"}, http.StatusOK)
	setDay("2026-12-10")
	move(lead, sec, "In esame", http.StatusOK)
	v = sec1.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusOK)
	if obj(v["review"])["due_on"] != "2026-12-16" {
		t.Fatalf("scadenza dopo i chiarimenti (sospensione di 2 giorni lavorativi): %v", obj(v["review"])["due_on"])
	}
	decide := func(u *user, key, outcome, note string, status int) map[string]any {
		return u.do("POST", "/api/v1/reviews/"+key+"/decision", map[string]any{"outcome": outcome, "note": note}, status)
	}
	decide(lead, secKey, "favorevole", "", http.StatusForbidden)
	decide(sec1, secKey, "favorevole", "", http.StatusUnprocessableEntity) // proposte da confermare
	p = derived(v)
	for k, id := range p {
		status := "rejected"
		if k == "waf" || k == "segreti" {
			status = "accepted"
		}
		lead.do("PATCH", "/api/v1/reviews/"+secKey+"/prescriptions/"+id, map[string]any{"status": status}, http.StatusForbidden)
		sec1.do("PATCH", "/api/v1/reviews/"+secKey+"/prescriptions/"+id, map[string]any{"status": status}, http.StatusOK)
	}
	sec1.do("POST", "/api/v1/reviews/"+secKey+"/prescriptions", map[string]any{"text": "Log degli accessi al SIEM", "category": "Monitoraggio"}, http.StatusCreated)
	decide(sec1, secKey, "favorevole", "", http.StatusUnprocessableEntity) // ci sono prescrizioni accettate
	v = decide(sec1, secKey, "favorevole_con_prescrizioni", "Ok con le prescrizioni", http.StatusOK)
	if obj(v["element"])["state"] != "Conclusa" || obj(v["review"])["outcome_label"] != "Favorevole con prescrizioni" {
		t.Fatalf("esito: %v %v", obj(v["element"])["state"], v["review"])
	}
	save(sec1, secKey, map[string]any{"mfa": true}, http.StatusUnprocessableEntity)

	// --- Storia delle risposte: chi ha risposto cosa e quando, precompilazioni comprese.
	h := list(lead.do("GET", "/api/v1/reviews/"+secKey+"/history?question=elenco_terze_parti", nil, http.StatusOK)["items"])
	if len(h) != 2 || obj(h[0])["by"] != "lead" || obj(h[1])["by"] != "curioso" {
		t.Fatalf("storia della risposta: %v", h)
	}
	h = list(lead.do("GET", "/api/v1/reviews/"+secKey+"/history?question=criticita", nil, http.StatusOK)["items"])
	if len(h) != 1 || obj(h[0])["source"] != "prefill" || obj(h[0])["by"] != nil {
		t.Fatalf("storia della precompilazione: %v", h)
	}
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update questionnaire_answers set value = '"bassa"' where question_key = 'criticita'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "storia delle risposte") {
		t.Fatalf("la storia delle risposte si è potuta modificare: %v", err)
	}

	// --- DPIA: decide solo il responsabile (DPO); un esito non favorevole blocca il gate.
	dpiaKey := got["dpia_preliminare/"].key
	dv := man.do("GET", "/api/v1/reviews/"+dpiaKey, nil, http.StatusOK)
	if obj(dv["can"])["is_owner"] != true {
		t.Fatalf("il demand manager compila la DPIA: %v", dv["can"])
	}
	save(man, dpiaKey, map[string]any{"finalita": "Area clienti con profilo e contratti", "interessati": []any{"clienti", "minori"},
		"dati_particolari": false, "profilazione": true, "larga_scala": true, "base_giuridica": "contratto",
		"conservazione_mesi": 180, "extra_ue": false}, http.StatusOK)
	dpia := obj(dv["element"])["id"].(string)
	move(man, dpia, "Inviata al team", http.StatusOK)
	move(dpo, dpia, "In esame", http.StatusOK)
	dv = dpo.do("GET", "/api/v1/reviews/"+dpiaKey, nil, http.StatusOK)
	if p := derived(dv); p["dpia_completa"] == "" || p["minori"] == "" || p["conservazione"] == "" || p["registro"] == "" || p["trasferimenti"] != "" {
		t.Fatalf("derivazioni della DPIA: %v", p)
	}
	for _, id := range derived(dv) {
		priv.do("PATCH", "/api/v1/reviews/"+dpiaKey+"/prescriptions/"+id, map[string]any{"status": "accepted"}, http.StatusOK)
	}
	decide(priv, dpiaKey, "non_favorevole", "Serve la DPIA completa", http.StatusForbidden)
	decide(dpo, dpiaKey, "non_favorevole", "", http.StatusUnprocessableEntity)
	decide(dpo, dpiaKey, "non_favorevole", "Profilazione su larga scala anche di minori: serve la DPIA completa", http.StatusOK)
	_, facts = reviewsOf(brKey)
	if facts["valutazioni_non_favorevoli"] != 1.0 || facts["prescrizioni"].(float64) < 3 {
		t.Fatalf("fatti dopo le decisioni: %v", facts)
	}

	// --- SLA: sollecito, ritardo ed escalation sulla design review di REP (Architetture:
	// SLA 5 giorni, escalation dopo 3 di ritardo, responsabile l'amministratore).
	setDay("2026-12-04")
	desKey := got["design_review/"+repKey].key
	save(man, desKey, map[string]any{"tipo_intervento": "modifica", "descrizione": "Nuovi report", "hosting": "cloud_ue",
		"nuove_tecnologie": false, "nuove_api": false}, http.StatusOK)
	des := obj(man.do("GET", "/api/v1/reviews/"+desKey, nil, http.StatusOK)["element"])["id"].(string)
	move(man, des, "Inviata al team", http.StatusOK)
	sla := func(day string) reviews.SLAReport {
		setDay(day)
		var rep reviews.SLAReport
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			rep, err = rev.CheckSLATenant(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	if r := sla("2026-12-09"); r.Reminders != 0 {
		t.Fatalf("sollecito troppo presto: %+v", r)
	}
	if r := sla("2026-12-11"); r.Reminders != 1 {
		t.Fatalf("sollecito il giorno lavorativo prima della scadenza: %+v", r)
	}
	if r := sla("2026-12-11"); r.Reminders != 0 {
		t.Fatalf("sollecito ripetuto: %+v", r)
	}
	if r := sla("2026-12-15"); r.Overdue != 1 || r.Escalations != 0 {
		t.Fatalf("ritardo: %+v", r)
	}
	if lv := man.do("GET", "/api/v1/reviews/"+desKey, nil, http.StatusOK); obj(lv["review"])["late_days"] != 1.0 {
		t.Fatalf("giorni di ritardo: %v", obj(lv["review"])["late_days"])
	}
	if r := sla("2026-12-17"); r.Escalations != 1 {
		t.Fatalf("escalation dopo 3 giorni lavorativi: %+v", r)
	}
	if r := sla("2026-12-18"); r.Escalations+r.Overdue+r.Reminders != 0 {
		t.Fatalf("avvisi ripetuti: %+v", r)
	}
	esc := false
	for _, n := range list(admin.do("GET", "/api/v1/notifications", nil, http.StatusOK)["items"]) {
		if obj(n)["kind"] == "sla" && obj(obj(n)["payload"])["event"] == "escalation" {
			esc = true
		}
	}
	if !esc {
		t.Fatal("il responsabile non ha ricevuto l'escalation")
	}
	setDay("2026-12-04")

	// --- Gate prima dell'approvazione: valutazioni obbligatorie chiuse e nessun esito non favorevole.
	man.do("PATCH", "/api/v1/demand/requests/"+brID, map[string]any{"estimate_high_cents": 9_000_000}, http.StatusNoContent)
	move(man, brID, "Prioritizzazione", http.StatusOK)
	if r := move(man, brID, "Approvazione", http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "valutazioni") {
		t.Fatalf("gate delle valutazioni: %v", r)
	}
	// La DPIA si riapre a mano: la nuova sostituisce quella non favorevole.
	nv := man.do("POST", "/api/v1/elements/"+brKey+"/reviews", map[string]any{"type": "dpia_preliminare"}, http.StatusCreated)
	man.do("POST", "/api/v1/elements/"+brKey+"/reviews", map[string]any{"type": "dpia_preliminare"}, http.StatusUnprocessableEntity)
	curioso.do("POST", "/api/v1/elements/"+brKey+"/reviews", map[string]any{"type": "dpia_preliminare"}, http.StatusForbidden)
	if _, facts = reviewsOf(brKey); facts["valutazioni_non_favorevoli"] != 0.0 {
		t.Fatalf("la DPIA sostituita conta ancora: %v", facts)
	}
	// Le valutazioni non necessarie le annulla il responsabile del team, con motivazione.
	cancel := func(u *user, key string) {
		id := obj(u.do("GET", "/api/v1/reviews/"+key, nil, http.StatusOK)["element"])["id"].(string)
		move(u, id, "Annullata", http.StatusOK, "Non necessaria per questo intervento")
	}
	move(man, obj(nv["element"])["id"].(string), "Annullata", http.StatusForbidden, "prova")
	cancel(dpo, obj(nv["element"])["key"].(string))
	got, _ = reviewsOf(brKey)
	cancel(admin, got["design_review/"+feKey].key)
	cancel(admin, got["design_review/"+repKey].key)
	cancel(admin, got["design_review/"+pay["key"].(string)].key)
	cancel(sec2, got["security_review/"+pay["key"].(string)].key)
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{
		"condition": "valutazioni_obbligatorie_aperte: 0 valutazioni_non_favorevoli: 0 valutazioni_aperte: 1 prescrizioni: >=3", "element": brKey}, http.StatusOK)
	if chk["result"] != true {
		t.Fatalf("fatti della richiesta: %v", chk["facts"])
	}
	move(man, brID, "Approvazione", http.StatusOK)

	// --- Versioni del modello: le pubblicate sono immutabili, le valutazioni restano sulla loro.
	tpl := admin.do("GET", "/api/v1/review-config/templates/security_applicativa", nil, http.StatusOK)
	def := obj(obj(tpl["current"])["definition"])
	secs := list(def["sections"])
	first := obj(secs[0])
	// Una condizione che usa una domanda successiva è un errore.
	first["questions"] = append(list(first["questions"]), map[string]any{"key": "nota", "label": "Nota", "type": "text", "visible_when": "segreti: vault"})
	dt := admin.do("PUT", "/api/v1/review-config/templates/security_applicativa/draft", map[string]any{"definition": def}, http.StatusOK)
	if !strings.Contains(jsonString(dt["draft_problems"]), "non viene prima") {
		t.Fatalf("problemi della bozza: %v", dt["draft_problems"])
	}
	admin.do("POST", "/api/v1/review-config/templates/security_applicativa/publish", map[string]any{}, http.StatusUnprocessableEntity)
	q := list(first["questions"])
	obj(q[len(q)-1])["visible_when"] = "criticita: alta, critica"
	first["questions"] = q
	lead.do("PUT", "/api/v1/review-config/templates/security_applicativa/draft", map[string]any{"definition": def}, http.StatusForbidden)
	admin.do("PUT", "/api/v1/review-config/templates/security_applicativa/draft", map[string]any{"definition": def, "notes": "Nota libera"}, http.StatusOK)
	pub := admin.do("POST", "/api/v1/review-config/templates/security_applicativa/publish", map[string]any{}, http.StatusOK)
	if obj(pub["current"])["version"] != 2.0 {
		t.Fatalf("versione pubblicata: %v", pub["current"])
	}
	if v := sec1.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusOK); obj(v["review"])["template_version"] != 1.0 || obj(v["review"])["template_current_version"] != 2.0 {
		t.Fatalf("la valutazione conclusa deve restare sulla versione 1: %v", v["review"])
	}
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update questionnaire_templates set definition = '{}' where key = 'security_applicativa' and version = 1")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "non si può modificare") {
		t.Fatalf("versione pubblicata modificata: %v", err)
	}

	// --- Isolamento tra tenant.
	slugB := slug + "b"
	tenB, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slugB, Name: "Altro", AdminEmail: "admin@" + slugB + ".test", AdminPassword: testPassword, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}
	other := login(t, srv.URL, "admin@"+slugB+".test", testPassword)
	other.do("GET", "/api/v1/reviews/"+secKey, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/reviews/"+obj(v["element"])["id"].(string), nil, http.StatusNotFound)
	other.do("PUT", "/api/v1/reviews/"+secKey+"/answers", map[string]any{"answers": map[string]any{"mfa": true}}, http.StatusNotFound)
	if n := len(list(other.do("GET", "/api/v1/reviews/inbox", nil, http.StatusOK)["team"])); n != 0 {
		t.Fatalf("inbox dell'altro tenant: %d", n)
	}
	if err := d.InTenant(ctx, tenB.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select (select count(*) from ext_review) + (select count(*) from questionnaire_answers) + (select count(*) from review_prescriptions)").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("dati delle valutazioni visibili da un altro tenant: %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := len(list(other.do("GET", "/api/v1/review-config", nil, http.StatusOK)["teams"])); n != 4 {
		t.Fatalf("team dell'altro tenant: %d", n)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range func() []any { l, _ := v.([]any); return l }() {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
