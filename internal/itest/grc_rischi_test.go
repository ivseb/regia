package itest

import (
	"context"
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
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// GRC, rischi, terze parti e incidenti (piano, passo 20): matrice e livelli, rischio
// residuo e accettazione con l'approvazione (separazione dei compiti, due livelli secondo
// la soglia, scadenza), heatmap e registro con filtri ed export, fornitore critico calcolato,
// contratti in scadenza, registro delle informazioni con colonne configurabili,
// classificazione degli incidenti con le regole degli schemi, notifiche normative con le
// scadenze giuste, postmortem e azioni correttive, permessi, isolamento tra tenant.
func TestGRCRischi(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("grc-"+time.Now().Format("150405.000"), ".", "")
	provision := []tenant.Provisioner{demand.Provision, architecture.Provision, work.Provision, risk.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Rischi", AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword, Provision: provision,
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(slug, name string, in tenant.MemberInput) {
		in.TenantSlug, in.Email, in.Name, in.Password = slug, name+"@"+slug+".test", name, testPassword
		if _, err := tenant.AddMember(ctx, d, in); err != nil {
			t.Fatal(err)
		}
	}
	add(slug, "rm1", tenant.MemberInput{Roles: []string{"risk_manager"}, Groups: []string{"Risk management"}})
	add(slug, "rm2", tenant.MemberInput{Roles: []string{"risk_manager"}, Groups: []string{"Risk management"}})
	add(slug, "owner", tenant.MemberInput{Roles: []string{"viewer"}})
	add(slug, "im", tenant.MemberInput{Roles: []string{"incident_manager"}, Groups: []string{"Incident management"}})
	add(slug, "im2", tenant.MemberInput{Roles: []string{"incident_manager"}, Groups: []string{"Incident management"}})
	add(slug, "editor", tenant.MemberInput{Roles: []string{"editor"}})
	add(slug, "viewer", tenant.MemberInput{Roles: []string{"viewer"}})

	eng := engine.New()
	ap := approval.Register(eng)
	dmSvc := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	rk := risk.Register(eng, ap)
	var clock atomic.Int64
	setTime := func(t time.Time) { clock.Store(t.Unix()) }
	base := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	setTime(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	rk.Now = now
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dmSvc, Arch: arc, Work: wk, Risk: rk,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, FilesDir: t.TempDir(),
	}).Handler())
	defer srv.Close()
	u := func(name string) *user { return login(t, srv.URL, name+"@"+slug+".test", testPassword) }
	admin, rm1, rm2, owner, im, editor, viewer := u("admin"), u("rm1"), u("rm2"), u("owner"), u("im"), u("editor"), u("viewer")
	list := func(v any) []any { l, _ := v.([]any); return l }
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	runJob := func() risk.DeadlineReport {
		t.Helper()
		var rep risk.DeadlineReport
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var err error
			rep, err = rk.CheckDeadlinesTenant(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	day := func(n int) string { return base.AddDate(0, 0, n).Format(time.DateOnly) }
	// eid: id dell elemento dalla chiave (i passaggi di fase vogliono l id).
	eid := func(key string) string {
		return obj(admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"])["id"].(string)
	}

	// --- Ruoli di sistema e permessi nel catalogo.
	roles := admin.do("GET", "/api/v1/admin/roles", nil, http.StatusOK)
	if s := jsonString(roles); !strings.Contains(s, "risk_manager") || !strings.Contains(s, "incident_manager") ||
		!strings.Contains(s, "risk.admin") || !strings.Contains(s, "incident.manage") {
		t.Fatalf("ruoli di sistema Risk manager e Incident manager mancanti: %s", s)
	}

	// --- Configurazione di partenza: matrice 5×5 con quattro fasce e soglia 9, politica a due
	// regole, schemi DORA e NIS2.
	cfg := rm1.do("GET", "/api/v1/risk-config", nil, http.StatusOK)
	ms := list(cfg["matrices"])
	if len(ms) != 1 || len(list(obj(ms[0])["likelihood"])) != 5 || len(list(obj(ms[0])["bands"])) != 4 || obj(ms[0])["appetite_score"] != 9.0 ||
		obj(ms[0])["default"] != true || len(list(obj(cfg["policy"])["rules"])) != 2 || len(list(cfg["schemes"])) != 2 || cfg["can_edit"] != true {
		t.Fatalf("configurazione di partenza: %s", jsonString(cfg))
	}
	if c := im.do("GET", "/api/v1/risk-config", nil, http.StatusOK); c["can_edit"] != false || c["can_edit_schemes"] != true {
		t.Fatalf("permessi della configurazione per l'incident manager: %v %v", c["can_edit"], c["can_edit_schemes"])
	}
	matrix3 := map[string]any{"name": "Matrice 3×3", "appetite_score": 4,
		"likelihood": []any{map[string]any{"value": 1, "name": "Bassa"}, map[string]any{"value": 2, "name": "Media"}, map[string]any{"value": 3, "name": "Alta"}},
		"impact":     []any{map[string]any{"value": 1, "name": "Basso"}, map[string]any{"value": 2, "name": "Medio"}, map[string]any{"value": 3, "name": "Alto"}},
		"bands": []any{map[string]any{"key": "basso", "name": "Basso", "min": 1, "max": 2, "tone": "ok"},
			map[string]any{"key": "medio", "name": "Medio", "min": 3, "max": 4, "tone": "neutral"},
			map[string]any{"key": "alto", "name": "Alto", "min": 5, "max": 9, "tone": "danger"}}}
	// Fasce con un buco, tono non del tema (l'arancione è riservato), permessi.
	bad := map[string]any{}
	for k, v := range matrix3 {
		bad[k] = v
	}
	bad["bands"] = []any{map[string]any{"key": "basso", "name": "Basso", "min": 1, "max": 2, "tone": "ok"}, map[string]any{"key": "alto", "name": "Alto", "min": 4, "max": 9, "tone": "danger"}}
	if r := rm1.do("PUT", "/api/v1/risk-config/matrices/tre", bad, http.StatusUnprocessableEntity); !strings.Contains(jsonString(r), "senza buchi") {
		t.Fatalf("fasce con un buco accettate: %v", r)
	}
	bad["bands"] = []any{map[string]any{"key": "basso", "name": "Basso", "min": 1, "max": 4, "tone": "flag"}, map[string]any{"key": "alto", "name": "Alto", "min": 5, "max": 9, "tone": "danger"}}
	rm1.do("PUT", "/api/v1/risk-config/matrices/tre", bad, http.StatusUnprocessableEntity)
	im.do("PUT", "/api/v1/risk-config/matrices/tre", matrix3, http.StatusForbidden)
	editor.do("PUT", "/api/v1/risk-config/matrices/tre", matrix3, http.StatusForbidden)
	if m := rm1.do("PUT", "/api/v1/risk-config/matrices/tre", matrix3, http.StatusOK); m["default"] != false || m["key"] != "tre" {
		t.Fatalf("seconda matrice: %v", m)
	}

	// --- Catalogo: un'applicazione con funzione critica e una no, un asset.
	csv := "codice;nome;criticita;funzione_critica;classificazione_dati;team;asset\n" +
		"PAG;Pagamenti online;critica;sì;personali;;Canali digitali\n" +
		"REP;Reportistica;bassa;no;aggregati;;Canali digitali\n"
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	apps := map[string]string{}
	for _, a := range list(admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"]) {
		apps[obj(a)["code"].(string)] = obj(a)["key"].(string)
	}

	// --- Rischio: inerente 4×4 = 16 (alto, oltre la soglia), residuo che non supera
	// l'inerente, titolare che tratta ma non riscrive, permessi.
	editor.do("POST", "/api/v1/risks", map[string]any{"title": "x"}, http.StatusForbidden)
	im.do("POST", "/api/v1/risks", map[string]any{"title": "x"}, http.StatusForbidden)
	r1 := rm1.do("POST", "/api/v1/risks", map[string]any{"title": "Indisponibilità del gateway di pagamento", "category": "Terze parti",
		"inherent_likelihood": 4, "inherent_impact": 4, "owner_id": owner.member, "targets": []string{apps["PAG"]}}, http.StatusCreated)
	r1Key := r1["key"].(string)
	if inh := obj(r1["inherent"]); inh["score"] != 16.0 || inh["band"] != "alto" || inh["over_appetite"] != true || inh["likelihood_name"] != "Probabile" {
		t.Fatalf("valutazione inerente: %v", inh)
	}
	if len(list(r1["targets"])) != 1 || obj(r1["element"])["state"] != "Identificato" || r1["owner"] != "owner" {
		t.Fatalf("rischio creato: %s", jsonString(r1))
	}
	rm1.do("PATCH", "/api/v1/risks/"+r1Key, map[string]any{"residual_likelihood": 5, "residual_impact": 5}, http.StatusUnprocessableEntity)
	rm1.do("PATCH", "/api/v1/risks/"+r1Key, map[string]any{"residual_likelihood": 2}, http.StatusUnprocessableEntity)
	owner.do("PATCH", "/api/v1/risks/"+r1Key, map[string]any{"title": "Altro titolo"}, http.StatusForbidden)
	viewer.do("PATCH", "/api/v1/risks/"+r1Key, map[string]any{"treatment": "accettare"}, http.StatusForbidden)
	r1 = owner.do("PATCH", "/api/v1/risks/"+r1Key, map[string]any{"residual_likelihood": 2, "residual_impact": 3, "treatment": "accettare"}, http.StatusOK)
	if res := obj(r1["residual"]); res["score"] != 6.0 || res["band"] != "medio" || res["over_appetite"] != false || r1["treatment_name"] != "accettare" {
		t.Fatalf("valutazione residua: %v %v", res, r1["treatment_name"])
	}
	// Valutazione: il passaggio vuole la valutazione inerente (c'è) ed è del titolare o del comitato.
	editor.do("POST", "/api/v1/elements/"+eid(r1Key)+"/transitions", map[string]any{"to": "Valutato"}, http.StatusForbidden)
	owner.do("POST", "/api/v1/elements/"+eid(r1Key)+"/transitions", map[string]any{"to": "Valutato"}, http.StatusOK)

	// --- Accettazione (residuo 6 ≤ 9): decide il titolare; chiede il comitato (rm1).
	r1 = rm1.do("POST", "/api/v1/risks/"+r1Key+"/acceptance", map[string]any{"until": day(180), "comment": "Fornitore con piano di continuità"}, http.StatusOK)
	if r1["acceptance"] != "pending" || obj(r1["element"])["state"] != "In accettazione" {
		t.Fatalf("accettazione richiesta: %v %v", r1["acceptance"], obj(r1["element"])["state"])
	}
	pendingSteps := func(u *user, key string) []map[string]any {
		var out []map[string]any
		for _, a := range list(u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["approvals"]) {
			if obj(a)["status"] != "pending" {
				continue
			}
			for _, s := range list(obj(a)["steps"]) {
				if obj(s)["decision"] == "pending" {
					out = append(out, obj(s))
				}
			}
		}
		return out
	}
	steps := pendingSteps(owner, r1Key)
	if len(steps) != 1 {
		t.Fatalf("passi dell'accettazione sotto la soglia: %v", steps)
	}
	rm1.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	owner.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "approved", "comment": "Accetto"}, http.StatusNoContent)
	r1 = rm1.do("GET", "/api/v1/risks/"+r1Key, nil, http.StatusOK)
	if r1["acceptance"] != "accepted" || obj(r1["element"])["state"] != "Accettato" || r1["accepted_until"] != day(180) {
		t.Fatalf("accettazione approvata: %v %v %v", r1["acceptance"], obj(r1["element"])["state"], r1["accepted_until"])
	}

	// --- Residuo 12 (oltre la soglia): due passi, titolare e poi comitato; chi ha chiesto
	// (rm1, nel comitato) non decide il secondo passo, rm2 sì.
	r2 := rm1.do("POST", "/api/v1/risks", map[string]any{"title": "Perdita di dati per configurazione errata del cloud", "category": "Cloud",
		"inherent_likelihood": 4, "inherent_impact": 5, "residual_likelihood": 3, "residual_impact": 4, "treatment": "accettare",
		"owner_id": owner.member}, http.StatusCreated)
	r2Key := r2["key"].(string)
	rm1.do("POST", "/api/v1/elements/"+eid(r2Key)+"/transitions", map[string]any{"to": "Valutato"}, http.StatusOK)
	rm1.do("POST", "/api/v1/risks/"+r2Key+"/acceptance", map[string]any{"until": day(90)}, http.StatusOK)
	steps = pendingSteps(owner, r2Key)
	owner.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	steps = pendingSteps(rm2, r2Key)
	if len(steps) != 1 {
		t.Fatalf("secondo passo del comitato: %v", steps)
	}
	rm1.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	rm2.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if st := state(t, rm1, r2Key); st != "Accettato" {
		t.Fatalf("rischio oltre la soglia dopo due approvazioni: %s", st)
	}

	// --- Respinta: il rischio torna da valutare, l'accettazione non approvata.
	r3 := rm1.do("POST", "/api/v1/risks", map[string]any{"title": "Dipendenza da una sola persona per i backup", "category": "Persone",
		"inherent_likelihood": 3, "inherent_impact": 3, "residual_likelihood": 2, "residual_impact": 2, "owner_id": owner.member,
		"targets": []string{apps["REP"]}}, http.StatusCreated)
	r3Key := r3["key"].(string)
	rm1.do("POST", "/api/v1/elements/"+eid(r3Key)+"/transitions", map[string]any{"to": "Valutato"}, http.StatusOK)
	// Il trattamento con piano va in trattamento; senza piano il gate blocca.
	rm1.do("PATCH", "/api/v1/risks/"+r3Key, map[string]any{"treatment": "mitigare"}, http.StatusOK)
	if r := rm1.do("POST", "/api/v1/elements/"+eid(r3Key)+"/transitions", map[string]any{"to": "In trattamento"}, http.StatusUnprocessableEntity); !strings.Contains(jsonString(r), "piano") {
		t.Fatalf("gate del piano di trattamento: %v", r)
	}
	rm1.do("POST", "/api/v1/risks/"+r3Key+"/acceptance", map[string]any{"until": day(30)}, http.StatusOK)
	steps = pendingSteps(owner, r3Key)
	owner.do("POST", "/api/v1/approvals/steps/"+steps[0]["id"].(string)+"/decision", map[string]any{"decision": "rejected", "comment": "Va mitigato"}, http.StatusNoContent)
	r3 = rm1.do("GET", "/api/v1/risks/"+r3Key, nil, http.StatusOK)
	if r3["acceptance"] != "rejected" || obj(r3["element"])["state"] != "Valutato" {
		t.Fatalf("accettazione respinta: %v %v", r3["acceptance"], obj(r3["element"])["state"])
	}
	r3 = rm1.do("PATCH", "/api/v1/risks/"+r3Key, map[string]any{"treatment": "mitigare", "treatment_plan": "Seconda persona formata e procedura scritta",
		"treatment_due": day(20)}, http.StatusOK)
	rm1.do("POST", "/api/v1/elements/"+eid(r3Key)+"/transitions", map[string]any{"to": "In trattamento"}, http.StatusOK)

	// --- Heatmap e registro con filtri ed export.
	dash := viewer.do("GET", "/api/v1/risk/dashboard", nil, http.StatusOK)
	hm := obj(dash["heatmap"])
	cellCount := func(cells []any, l, i float64) float64 {
		for _, c := range cells {
			if obj(c)["likelihood"] == l && obj(c)["impact"] == i {
				return obj(c)["count"].(float64)
			}
		}
		return -1
	}
	if len(list(hm["inherent"])) != 25 || cellCount(list(hm["inherent"]), 4, 4) != 1 || cellCount(list(hm["inherent"]), 4, 5) != 1 ||
		cellCount(list(hm["residual"]), 3, 4) != 1 || cellCount(list(hm["residual"]), 2, 3) != 1 {
		t.Fatalf("heatmap: %s", jsonString(hm))
	}
	if over := list(dash["over_threshold"]); len(over) != 1 || obj(over[0])["key"] != r2Key {
		t.Fatalf("rischi oltre la soglia: %s", jsonString(over))
	}
	reg := func(q string) []any { return list(viewer.do("GET", "/api/v1/risks"+q, nil, http.StatusOK)["items"]) }
	if n := len(reg("")); n != 3 {
		t.Fatalf("registro: %d rischi", n)
	}
	if it := reg("?oltre_soglia=true"); len(it) != 1 || obj(it[0])["key"] != r2Key {
		t.Fatalf("filtro oltre la soglia: %v", it)
	}
	if it := reg("?trattamento=accettare"); len(it) != 2 {
		t.Fatalf("filtro trattamento: %d", len(it))
	}
	if it := reg("?livello=medio&categoria=Terze%20parti"); len(it) != 1 || obj(it[0])["key"] != r1Key {
		t.Fatalf("filtro livello e categoria: %v", it)
	}
	if it := reg("?collegato=" + apps["REP"]); len(it) != 1 || obj(it[0])["key"] != r3Key {
		t.Fatalf("filtro per applicazione collegata: %v", it)
	}
	if it := reg("?fase=In%20trattamento"); len(it) != 1 {
		t.Fatalf("filtro per fase: %v", it)
	}
	out := viewer.raw("/api/v1/risks.csv?oltre_soglia=true", http.StatusOK)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 || !strings.Contains(lines[0], "Punteggio residuo") || !strings.Contains(lines[1], r2Key) {
		t.Fatalf("export del registro dei rischi: %q", out)
	}
	// Fatti del rischio per le condizioni dei processi.
	if f := rm1.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "oltre_soglia: sì livello_residuo: alto funzione_critica: no", "element": r2Key}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti del rischio: %v", f)
	}

	// --- Scadenze dei rischi: trattamento in scadenza, accettazione scaduta che torna da valutare.
	setTime(base.AddDate(0, 0, 10))
	rep := runJob()
	if rep.Treatments != 1 {
		t.Fatalf("avvisi di trattamento: %+v", rep)
	}
	setTime(base.AddDate(0, 0, 181))
	rep = runJob()
	if rep.Expired != 2 { // anche il secondo, accettato per 90 giorni
		t.Fatalf("accettazioni scadute: %+v", rep)
	}
	r1 = rm1.do("GET", "/api/v1/risks/"+r1Key, nil, http.StatusOK)
	if r1["acceptance"] != "expired" || obj(r1["element"])["state"] != "Valutato" {
		t.Fatalf("accettazione scaduta: %v %v", r1["acceptance"], obj(r1["element"])["state"])
	}
	if !hasRiskNote(owner, "risk_accepted") || !hasRiskNote(rm1, "risk_rejected") || !hasRiskNote(owner, "risk_acceptance_expired") {
		t.Fatalf("notifiche dell'accettazione al titolare: %v", owner.notes())
	}
	setTime(base)

	// --- Fornitori: criticità calcolata dalle applicazioni con funzione critica o dal contratto.
	editor.do("POST", "/api/v1/ict/providers", map[string]any{"name": "x"}, http.StatusForbidden)
	rm1.do("POST", "/api/v1/ict/providers", map[string]any{"name": "x", "lei": "corto"}, http.StatusUnprocessableEntity)
	p1 := rm1.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Nuvola Calcolo", "legal_name": "Nuvola Calcolo S.r.l.",
		"lei": "815600ABCDEF12345678", "country": "it", "provider_type": "Servizi cloud", "substitutability": "difficile",
		"owner_id": owner.member, "applications": []string{apps["PAG"]}}, http.StatusCreated)
	p2 := rm1.do("POST", "/api/v1/ict/providers", map[string]any{"name": "Archivia Dati", "country": "DE", "applications": []string{apps["REP"]}}, http.StatusCreated)
	p1Key, p2Key := p1["key"].(string), p2["key"].(string)
	if p1["critical"] != true || !strings.Contains(p1["critical_reason"].(string), "funzione critica") || p1["country"] != "IT" ||
		p2["critical"] != false {
		t.Fatalf("criticità calcolata: %v %v / %v", p1["critical"], p1["critical_reason"], p2["critical"])
	}
	if crit := list(viewer.do("GET", "/api/v1/ict/providers?critici=true", nil, http.StatusOK)["items"]); len(crit) != 1 || obj(crit[0])["key"] != p1Key {
		t.Fatalf("fornitori critici: %v", crit)
	}
	if f := rm1.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "critico: sì funzioni_critiche: 1 paese: IT", "element": p1Key}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti del fornitore: %v", f)
	}
	if f := rm1.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "fornitori_ict: \"Nuvola Calcolo\"", "element": apps["PAG"]}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti dell'applicazione: %v", f)
	}
	// Un contratto che dichiara una funzione critica rende critico anche il secondo fornitore;
	// scade fra 40 giorni con 30 di preavviso: è in scadenza.
	rm1.do("POST", "/api/v1/ict/providers/"+p2Key+"/contracts", map[string]any{"reference": "C-2025-01", "start_date": day(60), "end_date": day(10)}, http.StatusUnprocessableEntity)
	c2 := rm1.do("POST", "/api/v1/ict/providers/"+p2Key+"/contracts", map[string]any{"reference": "C-2025-07", "service_type": "Archiviazione",
		"services": "Conservazione dei documenti", "functions": "Archivio contratti", "supports_critical_function": true, "criticality": "alta",
		"start_date": "2025-01-01", "end_date": day(40), "notice_days": 30, "data_locations": []string{"de", "IT"},
		"subcontractors": []any{map[string]any{"name": "Server Nord", "country": "AT", "service": "Data center"}}}, http.StatusCreated)
	if c2["expiring"] != true || c2["criticality_name"] != "alta" || len(list(c2["data_locations"])) != 2 {
		t.Fatalf("contratto: %s", jsonString(c2))
	}
	rm1.do("POST", "/api/v1/ict/providers/"+p2Key+"/contracts", map[string]any{"reference": "c-2025-07"}, http.StatusUnprocessableEntity)
	c1 := rm1.do("POST", "/api/v1/ict/providers/"+p1Key+"/contracts", map[string]any{"reference": "NC-001", "service_type": "Calcolo",
		"services": "Infrastruttura dei pagamenti", "end_date": day(400), "notice_days": 90, "applications": []string{apps["PAG"]},
		"exit_strategy": "Migrazione su un secondo fornitore in sei mesi"}, http.StatusCreated)
	if len(list(c1["applications"])) != 1 || c1["expiring"] != false {
		t.Fatalf("contratto con applicazioni: %s", jsonString(c1))
	}
	if p := viewer.do("GET", "/api/v1/ict/providers/"+p2Key, nil, http.StatusOK); p["critical"] != true || !strings.Contains(p["critical_reason"].(string), "contratto") {
		t.Fatalf("fornitore critico per contratto: %v %v", p["critical"], p["critical_reason"])
	}
	if exp := list(viewer.do("GET", "/api/v1/ict/contracts?in_scadenza=true", nil, http.StatusOK)["items"]); len(exp) != 1 || obj(exp[0])["reference"] != "C-2025-07" {
		t.Fatalf("contratti in scadenza: %v", exp)
	}
	// Il job avvisa una volta sola; il contratto lontano no.
	if rep := runJob(); rep.Contracts != 1 {
		t.Fatalf("avvisi dei contratti: %+v", rep)
	}
	if rep := runJob(); rep.Contracts != 0 {
		t.Fatalf("avviso del contratto ripetuto: %+v", rep)
	}
	if !hasRiskNote(rm2, "contract_expiring") {
		t.Fatalf("avviso del contratto al gruppo Risk management: %v", rm2.notes())
	}

	// --- Registro delle informazioni: colonne di default, scelte nella richiesta, configurate.
	out = viewer.raw("/api/v1/ict/register.csv", http.StatusOK)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "Codice LEI") || !strings.Contains(out, "C-2025-07") || !strings.Contains(out, "815600ABCDEF12345678") ||
		!strings.Contains(out, "DE, IT") || !strings.Contains(out, "Server Nord (AT)") {
		t.Fatalf("registro delle informazioni: %q", out)
	}
	out = viewer.raw("/api/v1/ict/register.csv?columns=fornitore.nome,fornitore.critico&grain=provider", http.StatusOK)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 3 || strings.TrimPrefix(lines[0], "\ufeff") != "Fornitore;Fornitore critico (calcolato)" ||
		!strings.Contains(out, "Archivia Dati;sì") {
		t.Fatalf("registro con colonne scelte: %q", out)
	}
	if code, _, _ := viewer.get("/api/v1/ict/register.csv?columns=fornitore.inventata"); code != http.StatusUnprocessableEntity {
		t.Fatalf("colonna sconosciuta: %d", code)
	}
	st := obj(cfg["settings"])
	st["register_columns"] = []any{map[string]any{"key": "contratto.riferimento", "label": "Contratto"}, map[string]any{"key": "fornitore.nome"}}
	rm1.do("PUT", "/api/v1/risk-config/settings", st, http.StatusOK)
	if out := viewer.raw("/api/v1/ict/register.csv", http.StatusOK); !strings.HasPrefix(strings.TrimPrefix(out, "\ufeff"), "Contratto;Fornitore\r\n") &&
		!strings.HasPrefix(strings.TrimPrefix(out, "\ufeff"), "Contratto;Fornitore\n") {
		t.Fatalf("colonne configurate: %q", out)
	}

	// --- Incidente: chi crea elementi segnala, solo l'incident manager classifica.
	viewer.do("POST", "/api/v1/incidents", map[string]any{"title": "x"}, http.StatusForbidden)
	detected := base.Add(-2 * time.Hour)
	inc := editor.do("POST", "/api/v1/incidents", map[string]any{"title": "Pagamenti online non disponibili", "detected_at": detected.Format(time.RFC3339),
		"clients_affected": 150000, "downtime_minutes": 180, "impacted": []string{apps["PAG"]}}, http.StatusCreated)
	incKey := inc["key"].(string)
	if len(list(inc["events"])) != 1 || obj(list(inc["events"])[0])["kind"] != "detected" || len(list(inc["impacted"])) != 1 {
		t.Fatalf("incidente creato: %s", jsonString(inc))
	}
	editor.do("POST", "/api/v1/incidents/"+incKey+"/classify?dry_run=true", nil, http.StatusForbidden)
	rm1.do("POST", "/api/v1/incidents/"+incKey+"/classify?dry_run=true", nil, http.StatusForbidden)
	prevw := im.do("POST", "/api/v1/incidents/"+incKey+"/classify?dry_run=true", nil, http.StatusOK)
	results := map[string]map[string]any{}
	for _, r := range list(prevw["results"]) {
		results[obj(r)["key"].(string)] = obj(r)
	}
	met := func(res map[string]any) []string {
		var out []string
		for _, c := range list(res["criteria"]) {
			if obj(c)["met"] == true {
				out = append(out, obj(c)["key"].(string))
			}
		}
		return out
	}
	if results["dora"]["positive"] != true || results["nis2"]["positive"] != false ||
		strings.Join(met(results["dora"]), ",") != "servizi_critici,clienti,durata" {
		t.Fatalf("classificazione in anteprima: %s", jsonString(prevw))
	}
	// L'anteprima non salva nulla.
	if n := len(list(im.do("GET", "/api/v1/incidents/"+incKey, nil, http.StatusOK)["notifications"])); n != 0 {
		t.Fatalf("notifiche dopo l'anteprima: %d", n)
	}
	editor.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Classificato"}, http.StatusForbidden)
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Classificato"}, http.StatusOK)
	inc = im.do("GET", "/api/v1/incidents/"+incKey, nil, http.StatusOK)
	type notice struct {
		id, status string
		due        *time.Time
	}
	notices := func(v map[string]any) map[string]notice {
		out := map[string]notice{}
		for _, n := range list(v["notifications"]) {
			x := obj(n)
			var due *time.Time
			if s, ok := x["due_at"].(string); ok {
				t, _ := time.Parse(time.RFC3339, s)
				due = &t
			}
			out[x["scheme_key"].(string)+"/"+x["stage_key"].(string)] = notice{x["id"].(string), x["status"].(string), due}
		}
		return out
	}
	ns := notices(inc)
	if inc["major"] != true || len(ns) != 3 || ns["dora/iniziale"].due == nil || !ns["dora/iniziale"].due.Equal(base.Add(4*time.Hour)) ||
		ns["dora/intermedia"].due != nil || ns["dora/finale"].due != nil {
		t.Fatalf("notifiche DORA alla classificazione: %s", jsonString(inc["notifications"]))
	}
	if !hasRiskNote(login(t, srv.URL, "im2@"+slug+".test", testPassword), "incident_classified") {
		t.Fatalf("avviso della classificazione: %v", im.notes())
	}
	// La notifica iniziale: bozza, poi invio registrato; la scadenza dell'intermedia parte da lì.
	editor.do("PUT", "/api/v1/regulatory-notifications/"+ns["dora/iniziale"].id+"/draft", map[string]any{"content": "x"}, http.StatusForbidden)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/iniziale"].id+"/submit", map[string]any{}, http.StatusUnprocessableEntity)
	if n := im.do("PUT", "/api/v1/regulatory-notifications/"+ns["dora/iniziale"].id+"/draft", map[string]any{"content": "Prima notifica"}, http.StatusOK); n["status"] != "draft" {
		t.Fatalf("bozza: %v", n)
	}
	sent := base.Add(3 * time.Hour)
	setTime(sent)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/iniziale"].id+"/submit", map[string]any{"reference": "PROT-1", "channel": "portale dell'autorità"}, http.StatusOK)
	ns = notices(im.do("GET", "/api/v1/incidents/"+incKey, nil, http.StatusOK))
	if ns["dora/iniziale"].status != "submitted" || ns["dora/intermedia"].due == nil || !ns["dora/intermedia"].due.Equal(sent.Add(72*time.Hour)) || ns["dora/finale"].due != nil {
		t.Fatalf("scadenza della relazione intermedia: %+v", ns)
	}
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/iniziale"].id+"/submit", map[string]any{"content": "di nuovo"}, http.StatusUnprocessableEntity)
	// Più clienti in percentuale: l'incidente diventa significativo anche per NIS2.
	inc = im.do("PATCH", "/api/v1/incidents/"+incKey, map[string]any{"clients_affected_pct": 12}, http.StatusOK)
	ns = notices(inc)
	if len(ns) != 6 || ns["nis2/preallarme"].due == nil || !ns["nis2/preallarme"].due.Equal(detected.Add(24*time.Hour)) ||
		!ns["nis2/notifica"].due.Equal(detected.Add(72*time.Hour)) || ns["nis2/finale"].due != nil {
		t.Fatalf("notifiche NIS2 dopo la riclassificazione: %s", jsonString(inc["notifications"]))
	}
	if f := im.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "grave: sì classificazione: nis2 notifiche_aperte: 5 funzione_critica: sì", "element": incKey}, http.StatusOK); f["result"] != true {
		t.Fatalf("fatti dell'incidente: %v", f)
	}
	// Preavviso (entro 4 ore dalla scadenza) e scadenza passata del preallarme NIS2.
	setTime(detected.Add(21 * time.Hour))
	if rep := runJob(); rep.Notifications != 1 || rep.Overdue != 0 {
		t.Fatalf("preavviso delle notifiche: %+v", rep)
	}
	setTime(detected.Add(25 * time.Hour))
	if rep := runJob(); rep.Overdue != 1 || rep.Notifications != 0 {
		t.Fatalf("notifica scaduta: %+v", rep)
	}
	if !hasRiskNote(im, "notification_overdue") {
		t.Fatalf("avviso della notifica scaduta: %v", im.notes())
	}
	if dash := viewer.do("GET", "/api/v1/risk/dashboard", nil, http.StatusOK); len(list(dash["open_incidents"])) != 1 || len(list(dash["open_notifications"])) != 5 ||
		obj(list(dash["open_incidents"])[0])["next_stage"] != "NIS2 · incidenti significativi · Preallarme" {
		t.Fatalf("cruscotto degli incidenti: %s", jsonString(dash["open_incidents"]))
	}
	setTime(detected.Add(26 * time.Hour))
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["nis2/preallarme"].id+"/submit", map[string]any{"content": "Preallarme", "reference": "CSIRT-9"}, http.StatusOK)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["nis2/notifica"].id+"/submit", map[string]any{"content": "Notifica"}, http.StatusOK)
	ns = notices(im.do("GET", "/api/v1/incidents/"+incKey, nil, http.StatusOK))
	if ns["nis2/finale"].due == nil || !ns["nis2/finale"].due.Equal(detected.Add(26*time.Hour).Add(30*24*time.Hour)) {
		t.Fatalf("scadenza della relazione finale NIS2: %+v", ns["nis2/finale"])
	}
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/intermedia"].id+"/not-required", map[string]any{}, http.StatusUnprocessableEntity)

	// --- Timeline, rischio concretizzato, risoluzione, postmortem e azioni correttive.
	im.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "update", "text": "Failover sul sito secondario"}, http.StatusOK)
	im.do("POST", "/api/v1/incidents/"+incKey+"/events", map[string]any{"kind": "update", "text": "Nel futuro", "at": detected.Add(48 * time.Hour).Format(time.RFC3339)}, http.StatusUnprocessableEntity)
	im.do("POST", "/api/v1/incidents/"+incKey+"/links", map[string]any{"target": r1Key}, http.StatusOK)
	if r := viewer.do("GET", "/api/v1/risks/"+r1Key, nil, http.StatusOK); len(list(r["incidents"])) != 1 {
		t.Fatalf("rischio concretizzato: %v", r["incidents"])
	}
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "In gestione"}, http.StatusOK)
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Risolto"}, http.StatusOK)
	// Un incidente grave non si chiude senza postmortem.
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Chiuso", "comment": "x"}, http.StatusUnprocessableEntity)
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Postmortem"}, http.StatusOK)
	if r := im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Chiuso"}, http.StatusUnprocessableEntity); !strings.Contains(jsonString(r), "postmortem") ||
		!strings.Contains(jsonString(r), "notifiche") {
		t.Fatalf("gate della chiusura: %v", r)
	}
	im.do("PUT", "/api/v1/incidents/"+incKey+"/postmortem", map[string]any{"summary": ""}, http.StatusUnprocessableEntity)
	im.do("PUT", "/api/v1/incidents/"+incKey+"/postmortem", map[string]any{"summary": "Certificato scaduto sul bilanciatore",
		"root_cause": "Rinnovo manuale non pianificato", "lessons": "Rinnovo automatico e allarme a 30 giorni"}, http.StatusOK)
	editor.do("POST", "/api/v1/incidents/"+incKey+"/actions", map[string]any{"title": "x"}, http.StatusForbidden)
	inc = im.do("POST", "/api/v1/incidents/"+incKey+"/actions", map[string]any{"title": "Rinnovo automatico dei certificati", "assignee_id": editor.member, "due": day(30)}, http.StatusCreated)
	acts := list(inc["actions"])
	if len(acts) != 1 || obj(acts[0])["type"] != "task" || obj(acts[0])["assignee"] != "editor" || obj(acts[0])["due"] != day(30) ||
		!strings.HasPrefix(obj(acts[0])["key"].(string), "INC-") || inc["postmortem"] == nil {
		t.Fatalf("azione correttiva: %s", jsonString(inc["actions"]))
	}
	kinds := map[string]int{}
	for _, e := range list(inc["events"]) {
		kinds[obj(e)["kind"].(string)]++
	}
	if kinds["detected"] != 1 || kinds["classified"] < 2 || kinds["notification"] < 4 || kinds["update"] != 1 || kinds["resolved"] != 1 || kinds["action"] != 1 {
		t.Fatalf("timeline: %v", kinds)
	}
	// Le notifiche rimaste: l'intermedia e le finali; una dichiarata non dovuta, le altre inviate.
	ns = notices(inc)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/intermedia"].id+"/submit", map[string]any{"content": "Relazione intermedia"}, http.StatusOK)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/finale"].id+"/submit", map[string]any{"content": "Relazione finale"}, http.StatusOK)
	im.do("POST", "/api/v1/regulatory-notifications/"+ns["nis2/finale"].id+"/not-required", map[string]any{"note": "Assorbita dalla relazione DORA, d'accordo con l'autorità"}, http.StatusOK)
	im.do("POST", "/api/v1/elements/"+eid(incKey)+"/transitions", map[string]any{"to": "Chiuso"}, http.StatusOK)
	if st := state(t, im, incKey); st != "Chiuso" {
		t.Fatalf("incidente chiuso: %s", st)
	}

	// --- Un incidente non grave si chiude senza postmortem.
	setTime(base.AddDate(0, 0, 3))
	minor := im.do("POST", "/api/v1/incidents", map[string]any{"title": "Rallentamento della reportistica", "impacted": []string{apps["REP"]},
		"downtime_minutes": 20}, http.StatusCreated)
	mKey := minor["key"].(string)
	im.do("POST", "/api/v1/elements/"+eid(mKey)+"/transitions", map[string]any{"to": "Classificato"}, http.StatusOK)
	if m := im.do("GET", "/api/v1/incidents/"+mKey, nil, http.StatusOK); m["major"] != false || len(list(m["notifications"])) != 0 || m["lead"] != "im" {
		t.Fatalf("incidente minore: %v %v %v", m["major"], len(list(m["notifications"])), m["lead"])
	}
	im.do("POST", "/api/v1/elements/"+eid(mKey)+"/transitions", map[string]any{"to": "Risolto"}, http.StatusOK)
	im.do("POST", "/api/v1/elements/"+eid(mKey)+"/transitions", map[string]any{"to": "Chiuso", "comment": "Nessun impatto sui clienti"}, http.StatusOK)

	// --- Schemi: validazione, anteprima, modifica da chi gestisce gli incidenti.
	var nis2 map[string]any
	for _, s := range list(cfg["schemes"]) {
		if obj(s)["key"] == "nis2" {
			nis2 = obj(s)
		}
	}
	broken := map[string]any{}
	for k, v := range nis2 {
		broken[k] = v
	}
	broken["when"] = "criteri_soddisfatti:"
	im.do("PUT", "/api/v1/risk-config/schemes/nis2", broken, http.StatusUnprocessableEntity)
	broken["when"] = "criteri_soddisfatti: >=1"
	broken["stages"] = []any{map[string]any{"key": "a", "name": "A", "due": []any{map[string]any{"from": "stage:b", "hours": 4}}}}
	im.do("PUT", "/api/v1/risk-config/schemes/nis2", broken, http.StatusUnprocessableEntity)
	pv := im.do("POST", "/api/v1/risk-config/schemes/preview", map[string]any{"scheme": nis2, "incident": incKey}, http.StatusOK)
	if obj(pv["result"])["positive"] != true {
		t.Fatalf("anteprima dello schema: %v", pv)
	}
	nis2["stages"] = []any{map[string]any{"key": "preallarme", "name": "Preallarme", "due": []any{map[string]any{"from": "detected", "hours": 12}}}}
	if sc := im.do("PUT", "/api/v1/risk-config/schemes/nis2", nis2, http.StatusOK); len(list(sc["stages"])) != 1 {
		t.Fatalf("schema salvato: %v", sc)
	}
	editor.do("PUT", "/api/v1/risk-config/schemes/nis2", nis2, http.StatusForbidden)
	// Export e import della configurazione.
	pkg := rm1.do("GET", "/api/v1/risk-config/export", nil, http.StatusOK)
	if pkg["format"] != "regia.risk/v1" || len(list(pkg["matrices"])) != 2 || len(list(pkg["schemes"])) != 2 {
		t.Fatalf("export della configurazione: %v", pkg)
	}
	im.do("GET", "/api/v1/risk-config/export", nil, http.StatusForbidden)

	// --- Isolamento tra tenant.
	slug2 := slug + "b"
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug2, Name: "Altro", AdminEmail: "admin@" + slug2 + ".test",
		AdminPassword: testPassword, Provision: provision}); err != nil {
		t.Fatal(err)
	}
	other := login(t, srv.URL, "admin@"+slug2+".test", testPassword)
	other.do("GET", "/api/v1/risks/"+r1Key, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/incidents/"+incKey, nil, http.StatusNotFound)
	other.do("GET", "/api/v1/ict/providers/"+p1Key, nil, http.StatusNotFound)
	other.do("PATCH", "/api/v1/ict/contracts/"+c1["id"].(string), map[string]any{"services": "x"}, http.StatusNotFound)
	other.do("POST", "/api/v1/regulatory-notifications/"+ns["dora/finale"].id+"/submit", map[string]any{"content": "x"}, http.StatusNotFound)
	if n := len(list(other.do("GET", "/api/v1/risks", nil, http.StatusOK)["items"])); n != 0 {
		t.Fatalf("rischi visibili dall'altro tenant: %d", n)
	}
	if out := other.raw("/api/v1/ict/register.csv", http.StatusOK); strings.Contains(out, "Nuvola") {
		t.Fatalf("registro dell'altro tenant con i fornitori del primo: %q", out)
	}
	if c := other.do("GET", "/api/v1/risk-config", nil, http.StatusOK); len(list(c["matrices"])) != 1 {
		t.Fatalf("matrici dell'altro tenant: %d", len(list(c["matrices"])))
	}
}

// hasRiskNote dice se la persona ha una notifica dei rischi con l'evento indicato.
func hasRiskNote(u *user, event string) bool {
	for _, n := range u.notes() {
		if n["kind"] == "risk" && obj0(n["payload"])["event"] == event {
			return true
		}
	}
	return false
}

func obj0(v any) map[string]any { m, _ := v.(map[string]any); return m }
