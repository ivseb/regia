package itest

import (
	"context"
	"io"
	"log/slog"
	"math"
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
	"regia/internal/modules/portfolio"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// sgEnv è un tenant con demand, architettura e portafoglio, servito via HTTP.
type sgEnv struct {
	t        *testing.T
	slug     string
	tenantID string
	srv      *httptest.Server
}

func newSGEnv(t *testing.T, prefix string) *sgEnv {
	t.Helper()
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{
		Slug: slug, Name: "Portafoglio " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
		Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Il provisioning è idempotente: ripeterlo non crea una seconda scorecard.
	if err := tenant.Provision(ctx, d, slug, []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision}); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() { srv.Close(); apiSrv.Close() })
	return &sgEnv{t: t, slug: slug, tenantID: ten.TenantID, srv: srv}
}

func (e *sgEnv) member(name string, in tenant.MemberInput) *user {
	e.t.Helper()
	in.TenantSlug, in.Email, in.Name, in.Password = e.slug, name+"@"+e.slug+".test", name, testPassword
	if _, err := tenant.AddMember(context.Background(), testDB, in); err != nil {
		e.t.Fatal(err)
	}
	return login(e.t, e.srv.URL, in.Email, testPassword)
}

// raw esegue una richiesta e restituisce il corpo così com'è.
func (u *user) raw(path string, wantStatus int) string {
	u.t.Helper()
	resp, err := u.c.Get(u.base + path)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		u.t.Fatalf("GET %s: stato %d, atteso %d: %s", path, resp.StatusCode, wantStatus, b)
	}
	return string(b)
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// votes costruisce una valutazione completa con lo stesso voto per ogni criterio, salvo eccezioni.
func votes(all float64, except map[string]float64) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"valore_strategico", "beneficio_economico", "urgenza_normativa", "rischio_esecuzione", "allineamento_architetturale"} {
		v := all
		if x, ok := except[k]; ok {
			v = x
		}
		out[k] = map[string]any{"value": v, "note": "nota su " + k}
	}
	return out
}

// Scorecard versionata, punteggio pesato con direzione dei criteri, permessi di voto,
// graduatoria con obblighi in testa, linea di budget e ordine manuale motivato.
func TestScorecardAndRanking(t *testing.T) {
	e := newSGEnv(t, "score")
	d := needDB(t)
	ctx := context.Background()
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	pmo := e.member("pmo", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	spo := e.member("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	req := e.member("richiedente", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}})

	// Pacchetto di partenza: versione 1 pubblicata con cinque criteri.
	sc := admin.do("GET", "/api/v1/scorecards/portafoglio", nil, http.StatusOK)
	cur := sc["current"].(map[string]any)
	if cur["version"].(float64) != 1 || len(cur["definition"].(map[string]any)["criteria"].([]any)) != 5 {
		t.Fatalf("scorecard di partenza: %v", cur)
	}
	if n := len(admin.do("GET", "/api/v1/scorecards", nil, http.StatusOK)["items"].([]any)); n != 1 {
		t.Fatalf("scorecard dopo due provisioning: %d", n)
	}
	v1 := cur["id"].(string)

	newReq := func(title string, mandatory bool, lowK, highK int64) (string, string) {
		body := map[string]any{"title": title, "sponsor_id": spo.member, "estimate_low_cents": lowK * 100_000, "estimate_high_cents": highK * 100_000}
		if mandatory {
			body["is_mandatory"] = true
			body["driver"] = "regulatory"
			body["regulatory_deadline"] = "2027-03-31"
		}
		r := req.do("POST", "/api/v1/demand/requests", body, http.StatusCreated)
		req.do("POST", "/api/v1/elements/"+r["id"].(string)+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
		dmU.do("POST", "/api/v1/elements/"+r["id"].(string)+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)
		dmU.do("POST", "/api/v1/elements/"+r["id"].(string)+"/transitions", map[string]any{"to": "Valutazione"}, http.StatusOK)
		return r["id"].(string), r["key"].(string)
	}
	aID, aKey := newReq("Obbligo DORA", true, 80, 100)
	_, bKey := newReq("Portale B2B", false, 150, 200)
	_, cKey := newReq("Data warehouse", false, 250, 300)
	dID, dKey := newReq("Firma digitale", false, 30, 40)
	draft := req.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Bozza esclusa", "estimate_high_cents": 1}, http.StatusCreated)

	// Permessi: votano solo i ruoli configurati (demand manager e PMO); il richiedente non vede.
	ev := dmU.do("GET", "/api/v1/demand/requests/"+aKey+"/evaluation", nil, http.StatusOK)
	if ev["can_score"] != true || ev["scorecard"].(map[string]any)["id"] != v1 {
		t.Fatalf("valutazione per il demand manager: %v", ev)
	}
	if p := ev["proposals"].(map[string]any)["urgenza_normativa"]; p == nil || num(p.(map[string]any)["value"]) != 4 {
		t.Fatalf("valore proposto dalla condizione per l'urgenza normativa: %v", ev["proposals"])
	}
	if spo.do("GET", "/api/v1/demand/requests/"+aKey+"/evaluation", nil, http.StatusOK)["can_score"] != false {
		t.Fatal("lo sponsor risulta abilitato al voto")
	}
	spo.do("POST", "/api/v1/demand/requests/"+aKey+"/evaluation", map[string]any{"values": votes(3, nil)}, http.StatusForbidden)
	req.do("POST", "/api/v1/demand/requests/"+aKey+"/evaluation", map[string]any{"values": votes(3, nil)}, http.StatusForbidden)
	req.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusForbidden)

	// Punteggio pesato 0-100 e direzione: il rischio alto pesa contro.
	dmU.do("POST", "/api/v1/demand/requests/"+aKey+"/evaluation", map[string]any{"values": votes(3, map[string]float64{"urgenza_normativa": 0})}, http.StatusUnprocessableEntity)
	partial := votes(3, nil)
	delete(partial, "allineamento_architetturale")
	dmU.do("POST", "/api/v1/demand/requests/"+aKey+"/evaluation", map[string]any{"values": partial}, http.StatusUnprocessableEntity)
	best := dmU.do("POST", "/api/v1/demand/requests/"+bKey+"/evaluation", map[string]any{"version_id": v1, "values": votes(5, map[string]float64{"rischio_esecuzione": 1})}, http.StatusCreated)
	worst := pmo.do("POST", "/api/v1/demand/requests/"+cKey+"/evaluation", map[string]any{"values": votes(1, map[string]float64{"rischio_esecuzione": 5})}, http.StatusCreated)
	if num(best["total"]) != 100 || num(worst["total"]) != 0 {
		t.Fatalf("estremi del punteggio: %v e %v, attesi 100 e 0", best["total"], worst["total"])
	}
	// 30×0,75 + 25×0,5 + 20×0,25 + 15×(1−0,75) + 10×1 = 53,75 su 100 di peso.
	mixed := dmU.do("POST", "/api/v1/demand/requests/"+dKey+"/evaluation", map[string]any{"values": map[string]any{
		"valore_strategico": map[string]any{"value": 4}, "beneficio_economico": map[string]any{"value": 3},
		"urgenza_normativa": map[string]any{"value": 2}, "rischio_esecuzione": map[string]any{"value": 4},
		"allineamento_architetturale": map[string]any{"value": 5, "note": "riusa la piattaforma di firma"},
	}, "comment": "valutazione iniziale"}, http.StatusCreated)
	if math.Abs(num(mixed["total"])-53.8) > 1e-9 {
		t.Fatalf("punteggio pesato: %v, atteso 53,8", mixed["total"])
	}
	dmU.do("POST", "/api/v1/demand/requests/"+aKey+"/evaluation", map[string]any{"values": votes(2, nil)}, http.StatusCreated) // 37,5 circa
	// Il punteggio diventa il fatto "punteggio" delle condizioni.
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "punteggio: >50", "element": dKey}, http.StatusOK)
	if chk["result"] != true {
		t.Fatalf("fatto punteggio: %v", chk)
	}

	// Versioni: solo il governo del portafoglio modifica; la nuova versione non tocca i punteggi dati.
	def := cur["definition"].(map[string]any)
	def["criteria"].([]any)[3].(map[string]any)["weight"] = 35
	dmU.do("PUT", "/api/v1/scorecards/portafoglio/draft", map[string]any{"definition": def}, http.StatusForbidden)
	bad := map[string]any{"name": "x", "scorer_roles": []string{"inesistente"}, "criteria": []any{}}
	admin.do("PUT", "/api/v1/scorecards/portafoglio/draft", map[string]any{"definition": bad}, http.StatusUnprocessableEntity)
	dr := admin.do("PUT", "/api/v1/scorecards/portafoglio/draft", map[string]any{"definition": def, "notes": "il rischio pesa di più"}, http.StatusOK)
	if dr["draft"].(map[string]any)["version"].(float64) != 2 || dr["current"].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("bozza: %v", dr)
	}
	pub := admin.do("POST", "/api/v1/scorecards/portafoglio/publish", map[string]any{"notes": ""}, http.StatusOK)
	v2 := pub["current"].(map[string]any)["id"].(string)
	if pub["current"].(map[string]any)["version"].(float64) != 2 || pub["draft"] != nil {
		t.Fatalf("pubblicazione: %v", pub)
	}
	evD := dmU.do("GET", "/api/v1/demand/requests/"+dKey+"/evaluation", nil, http.StatusOK)
	curD := evD["current"].(map[string]any)
	if evD["stale"] != true || curD["version"].(float64) != 1 || num(curD["total"]) != 53.8 || evD["current_definition"] == nil {
		t.Fatalf("il punteggio dato con la v1 deve restare legato alla v1: %v", evD)
	}
	// Chi vota con la versione vecchia è fermato; con la nuova il peso diverso cambia il totale.
	dmU.do("POST", "/api/v1/demand/requests/"+dKey+"/evaluation", map[string]any{"version_id": v1, "values": votes(3, nil)}, http.StatusUnprocessableEntity)
	again := dmU.do("POST", "/api/v1/demand/requests/"+dKey+"/evaluation", map[string]any{"version_id": v2, "values": map[string]any{
		"valore_strategico": map[string]any{"value": 4}, "beneficio_economico": map[string]any{"value": 3},
		"urgenza_normativa": map[string]any{"value": 2}, "rischio_esecuzione": map[string]any{"value": 4},
		"allineamento_architetturale": map[string]any{"value": 5},
	}}, http.StatusCreated)
	// 22,5 + 12,5 + 5 + 35×0,25 + 10 = 58,75 su 120 → 49,0
	if num(again["total"]) != 49 || again["version"].(float64) != 2 {
		t.Fatalf("punteggio con la v2: %v", again)
	}
	if h := dmU.do("GET", "/api/v1/demand/requests/"+dKey+"/evaluation", nil, http.StatusOK)["history"].([]any); len(h) != 2 {
		t.Fatalf("storia delle valutazioni: %d voci", len(h))
	}
	// Immutabilità anche nel database: versione pubblicata e punteggi non si toccano.
	err := d.InTenant(ctx, e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update scorecards set criteria = '[]' where id = $1", v1)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "non si può modificare") {
		t.Fatalf("versione pubblicata modificabile: %v", err)
	}
	err = d.InTenant(ctx, e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "update scorecard_results set total = 99")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "non si modificano") {
		t.Fatalf("punteggi modificabili: %v", err)
	}

	// Graduatoria: l'obbligo normativo sta in testa anche con il punteggio più basso; la bozza no.
	rk := pmo.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)
	order := func(rk map[string]any) []string {
		var out []string
		for _, it := range rk["items"].([]any) {
			out = append(out, it.(map[string]any)["key"].(string))
		}
		return out
	}
	got := strings.Join(order(rk), ",")
	if want := strings.Join([]string{aKey, bKey, dKey, cKey}, ","); got != want {
		t.Fatalf("ordine della graduatoria: %s, atteso %s", got, want)
	}
	if strings.Contains(got, draft["key"].(string)) {
		t.Fatal("la bozza è in graduatoria")
	}
	// Linea di budget sulla stima massima cumulata: 100 + 200 = 300 k€ entra, 340 no.
	dmU.do("PUT", "/api/v1/portfolio/settings", map[string]any{"budget_cents": 30_000_000}, http.StatusForbidden)
	st := admin.do("PUT", "/api/v1/portfolio/settings", map[string]any{"budget_cents": 30_000_000}, http.StatusOK)
	if num(st["budget_cents"]) != 30_000_000 {
		t.Fatalf("impostazioni: %v", st)
	}
	rk = pmo.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)
	items := rk["items"].([]any)
	if num(rk["cut"]) != 2 || items[1].(map[string]any)["funded"] != true || items[2].(map[string]any)["funded"] != false {
		t.Fatalf("linea di taglio: cut %v, %v", rk["cut"], items)
	}
	if num(items[2].(map[string]any)["cumulative_cents"]) != 34_000_000 {
		t.Fatalf("cumulata: %v", items[2])
	}
	// Simulazione con un budget diverso, senza salvarlo.
	if sim := pmo.do("GET", "/api/v1/portfolio/ranking?budget_cents=10000000", nil, http.StatusOK); num(sim["cut"]) != 1 {
		t.Fatalf("simulazione di budget: %v", sim["cut"])
	}
	// Gli obblighi in testa sono configurabili.
	admin.do("PUT", "/api/v1/portfolio/settings", map[string]any{"mandatory_first": false}, http.StatusOK)
	if first := order(pmo.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK))[0]; first != bKey {
		t.Fatalf("senza obblighi in testa la prima è %s, attesa %s", first, bKey)
	}
	admin.do("PUT", "/api/v1/portfolio/settings", map[string]any{"mandatory_first": true}, http.StatusOK)

	// Ordine manuale: motivazione obbligatoria, solo i ruoli configurati, obblighi sempre in testa.
	pmo.do("PUT", "/api/v1/portfolio/ranking/"+dKey, map[string]any{"position": 2, "reason": "  "}, http.StatusUnprocessableEntity)
	dmU.do("PUT", "/api/v1/portfolio/ranking/"+dKey, map[string]any{"position": 2, "reason": "urgente"}, http.StatusForbidden)
	pmo.do("PUT", "/api/v1/portfolio/ranking/"+dKey, map[string]any{"position": 1, "reason": "urgente"}, http.StatusUnprocessableEntity)
	moved := pmo.do("PUT", "/api/v1/portfolio/ranking/"+dKey, map[string]any{"position": 2, "reason": "Contratto in scadenza a marzo"}, http.StatusOK)
	if got := strings.Join(order(moved), ","); got != strings.Join([]string{aKey, dKey, bKey, cKey}, ",") {
		t.Fatalf("dopo lo spostamento: %s", got)
	}
	pinned := moved["items"].([]any)[1].(map[string]any)
	if pinned["pinned"] != true || pinned["pin_reason"] != "Contratto in scadenza a marzo" {
		t.Fatalf("posizione forzata: %v", pinned)
	}
	// La posizione forzata resta anche se il punteggio cambia.
	pmo.do("POST", "/api/v1/demand/requests/"+bKey+"/evaluation", map[string]any{"values": votes(5, map[string]float64{"rischio_esecuzione": 1})}, http.StatusCreated)
	if order(pmo.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK))[1] != dKey {
		t.Fatal("la posizione forzata non resta")
	}
	csv := pmo.raw("/api/v1/portfolio/ranking.csv", http.StatusOK)
	if !strings.Contains(csv, "posizione;chiave;titolo") || !strings.Contains(csv, "2;"+dKey+";Firma digitale") || !strings.Contains(csv, "Contratto in scadenza a marzo") {
		t.Fatalf("CSV della graduatoria:\n%s", csv)
	}
	// Ritorno all'ordine automatico, sempre motivato.
	back := pmo.do("PUT", "/api/v1/portfolio/ranking/"+dKey, map[string]any{"position": nil, "reason": "Contratto rinnovato"}, http.StatusOK)
	if order(back)[1] != bKey {
		t.Fatalf("ordine automatico: %v", order(back))
	}
	hist := admin.do("GET", "/api/v1/elements/"+dID+"/activity", nil, http.StatusOK)["items"].([]any)
	kinds := map[string]int{}
	for _, h := range hist {
		kinds[h.(map[string]any)["kind"].(string)]++
	}
	if kinds["rank"] != 2 || kinds["score"] != 2 {
		t.Fatalf("storia della richiesta: %v", kinds)
	}
	_ = aID

	var broken *int64
	if err := d.Owner.QueryRow(ctx, "select audit_verify($1)", e.tenantID).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != nil {
		t.Fatalf("catena di audit rotta al seq %d", *broken)
	}
	var audits int
	if err := d.Owner.QueryRow(ctx, `select count(*) from audit_log where tenant_id = $1 and action in ('scorecard.score','scorecard.publish','portfolio.rank','portfolio.settings')`, e.tenantID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits < 10 {
		t.Fatalf("eventi di audit della valutazione: %d", audits)
	}
}

// Organi e sedute: ordine del giorno proposto dai passi di approvazione dell'organo,
// quorum, decisione in seduta che fa avanzare la richiesta, verbale immutabile,
// isolamento tra tenant.
func TestGovernanceMeetings(t *testing.T) {
	e := newSGEnv(t, "gov")
	d := needDB(t)
	ctx := context.Background()
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	dmU := e.member("demandmanager", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	pmo := e.member("pmo", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"PMO"}})
	spo := e.member("sponsor", tenant.MemberInput{Roles: []string{"viewer"}})
	seg := e.member("segretario", tenant.MemberInput{Roles: []string{"viewer"}})
	mem := e.member("consigliere", tenant.MemberInput{Roles: []string{"viewer"}})

	// L'organo del pacchetto di partenza: il presidente è l'amministratore.
	bodies := admin.do("GET", "/api/v1/governance/bodies", nil, http.StatusOK)["items"].([]any)
	if len(bodies) != 1 {
		t.Fatalf("organi: %v", bodies)
	}
	body := bodies[0].(map[string]any)
	bodyID := body["id"].(string)
	chair := body["chair_id"].(string)
	in := map[string]any{"name": "Comitato investimenti", "kind": "investment_committee", "chair_id": chair, "secretary_id": seg.member,
		"quorum": 2, "members": []any{map[string]any{"member_id": mem.member}, map[string]any{"member_id": seg.member, "voting": false}}}
	dmU.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusForbidden)
	in["quorum"] = 3
	admin.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusUnprocessableEntity) // solo due votanti
	in["quorum"] = 2
	upd := admin.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusOK)
	if len(upd["members"].([]any)) != 3 || num(upd["quorum_required"]) != 2 {
		t.Fatalf("organo aggiornato: %v", upd)
	}
	// Un secondo organo (steering) con tipo e quorum di default (maggioranza).
	st := admin.do("POST", "/api/v1/governance/bodies", map[string]any{"name": "Steering trasformazione", "kind": "steering", "chair_id": pmo.member,
		"members": []any{map[string]any{"member_id": spo.member}, map[string]any{"member_id": dmU.member}}}, http.StatusCreated)
	if num(st["quorum_required"]) != 2 {
		t.Fatalf("quorum di maggioranza su 3 votanti: %v", st["quorum_required"])
	}
	dmU.do("POST", "/api/v1/governance/bodies", map[string]any{"name": "Abusivo", "chair_id": dmU.member}, http.StatusForbidden)

	// Una richiesta da 300 k€ arriva al comitato dopo sponsor e PMO; una da 30 k€ no.
	toApproval := func(title string, highK int64) (string, string) {
		r := dmU.do("POST", "/api/v1/demand/requests", map[string]any{"title": title, "sponsor_id": spo.member,
			"estimate_low_cents": highK * 80_000, "estimate_high_cents": highK * 100_000}, http.StatusCreated)
		id := r["id"].(string)
		dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
		for _, to := range []string{"Triage", "Valutazione", "Prioritizzazione", "Approvazione"} {
			dmU.do("POST", "/api/v1/elements/"+id+"/transitions", map[string]any{"to": to}, http.StatusOK)
		}
		return id, r["key"].(string)
	}
	bigID, bigKey := toApproval("Migrazione del data warehouse", 300)
	_, smallKey := toApproval("Firma digitale", 30)
	steps := func(key string) []any {
		return admin.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["approvals"].([]any)[0].(map[string]any)["steps"].([]any)
	}
	sp := steps(bigKey)
	if len(sp) != 3 {
		t.Fatalf("passi per 300 k€: %d", len(sp))
	}
	// Seduta pianificata quando il comitato non è ancora di turno: niente proposte.
	early := seg.do("POST", "/api/v1/governance/meetings", map[string]any{"body_id": bodyID, "scheduled_at": "2027-01-12T15:00:00+01:00",
		"location": "Sala del consiglio"}, http.StatusCreated)
	if len(early["items"].([]any)) != 0 {
		t.Fatalf("proposte prima del turno del comitato: %v", early["items"])
	}
	spo.do("POST", "/api/v1/approvals/steps/"+sp[0].(map[string]any)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	pmo.do("POST", "/api/v1/approvals/steps/"+sp[1].(map[string]any)["id"].(string)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if bodies := admin.do("GET", "/api/v1/governance/bodies", nil, http.StatusOK)["items"].([]any); num(bodies[0].(map[string]any)["pending"]) != 1 {
		t.Fatalf("richieste in attesa del comitato: %v", bodies[0])
	}
	// Ora la richiesta compare tra le proposte della seduta già pianificata.
	props := seg.do("GET", "/api/v1/governance/meetings/"+early["id"].(string), nil, http.StatusOK)["proposals"].([]any)
	if len(props) != 1 || props[0].(map[string]any)["key"] != bigKey {
		t.Fatalf("proposte: %v", props)
	}

	// Permessi: solo presidente, segretario o governo del portafoglio pianificano sedute.
	mem.do("POST", "/api/v1/governance/meetings", map[string]any{"body_id": bodyID, "scheduled_at": "2027-01-19T15:00:00+01:00"}, http.StatusForbidden)
	seg.do("POST", "/api/v1/governance/meetings/"+early["id"].(string)+"/status", map[string]any{"status": "cancelled"}, http.StatusOK)
	mt := seg.do("POST", "/api/v1/governance/meetings", map[string]any{"body_id": bodyID, "title": "Comitato di gennaio",
		"scheduled_at": "2027-01-19T15:00:00+01:00", "link": "https://meet.example.test/comitato"}, http.StatusCreated)
	mid := mt["id"].(string)
	items := mt["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != bigKey || items[0].(map[string]any)["proposed"] != true || items[0].(map[string]any)["step_active"] != true {
		t.Fatalf("ordine del giorno proposto: %v", items)
	}
	// Un punto aggiunto a mano (senza passo del comitato) e tolto.
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/items", map[string]any{"element": smallKey}, http.StatusOK)
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/items", map[string]any{"element": smallKey}, http.StatusUnprocessableEntity)
	seg.do("DELETE", "/api/v1/governance/meetings/"+mid+"/items/"+smallKey, nil, http.StatusOK)

	dec := "/api/v1/governance/meetings/" + mid + "/items/" + bigKey + "/decision"
	seg.do("POST", dec, map[string]any{"decision": "approved"}, http.StatusUnprocessableEntity) // seduta non ancora svolta
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/status", map[string]any{"status": "convened"}, http.StatusOK)
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/status", map[string]any{"status": "held"}, http.StatusOK)
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/status", map[string]any{"status": "planned"}, http.StatusUnprocessableEntity)

	// Quorum: 2 votanti su 2 richiesti; il segretario non vota e non conta.
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/attendance", map[string]any{"attendance": []any{
		map[string]any{"member_id": seg.member, "present": true}, map[string]any{"member_id": chair, "present": true}}}, http.StatusOK)
	q := seg.do("GET", "/api/v1/governance/meetings/"+mid, nil, http.StatusOK)["quorum"].(map[string]any)
	if q["reached"] != false || num(q["present"]) != 1 || num(q["required"]) != 2 {
		t.Fatalf("quorum con un solo votante: %v", q)
	}
	seg.do("POST", dec, map[string]any{"decision": "approved"}, http.StatusUnprocessableEntity)
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/attendance", map[string]any{"attendance": []any{
		map[string]any{"member_id": mem.member, "present": true}}}, http.StatusOK)
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/attendance", map[string]any{"attendance": []any{
		map[string]any{"member_id": spo.member, "present": true}}}, http.StatusUnprocessableEntity) // non è membro

	// Decisione: la registra il presidente o il segretario, non un consigliere.
	mem.do("POST", dec, map[string]any{"decision": "approved"}, http.StatusForbidden)
	seg.do("POST", dec, map[string]any{"decision": "approved_with_conditions"}, http.StatusUnprocessableEntity)
	after := seg.do("POST", dec, map[string]any{"decision": "approved_with_conditions", "conditions": "Rivedere i costi di licenza", "note": "Unanimità"}, http.StatusOK)
	it := after["items"].([]any)[0].(map[string]any)
	if it["outcome"] != "approved_with_conditions" || it["decided_by"] != "segretario" {
		t.Fatalf("punto deciso: %v", it)
	}
	seg.do("POST", dec, map[string]any{"decision": "rejected", "note": "ripensamento"}, http.StatusConflict)
	// La richiesta avanza da sola: il passo del comitato è chiuso dalla seduta.
	if s := state(t, admin, bigKey); s != "Pianificazione" {
		t.Fatalf("dopo la decisione del comitato lo stato è %q", s)
	}
	last := steps(bigKey)[2].(map[string]any)
	if last["decision"] != "approved_with_conditions" || last["decided_by"] != "segretario" {
		t.Fatalf("passo del comitato: %v", last)
	}
	var stepMeeting *string
	if err := d.InTenant(ctx, e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select meeting_id::text from approval_steps where id = $1", last["id"]).Scan(&stepMeeting)
	}); err != nil {
		t.Fatal(err)
	}
	if stepMeeting == nil || *stepMeeting != mid {
		t.Fatalf("il passo non ricorda la seduta: %v", stepMeeting)
	}

	// Verbale: bozza dalle decisioni, la scrive il segretario, la approva solo il presidente.
	mtg := seg.do("GET", "/api/v1/governance/meetings/"+mid, nil, http.StatusOK)
	draftText := mtg["minutes_draft"].(string)
	for _, want := range []string{"19 gennaio 2027, ore 15:00", "Presenti:", "consigliere", "Assenti:", "Quorum: 2 votanti presenti su 2, richiesti 2", bigKey, "approvata con condizioni", "Rivedere i costi di licenza"} {
		if !strings.Contains(draftText, want) {
			t.Fatalf("bozza del verbale senza %q:\n%s", want, draftText)
		}
	}
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/minutes", map[string]any{"text": draftText + "\nLa seduta termina alle 16:10."}, http.StatusOK)
	seg.do("POST", "/api/v1/governance/meetings/"+mid+"/minutes/approve", nil, http.StatusForbidden)
	ok := admin.do("POST", "/api/v1/governance/meetings/"+mid+"/minutes/approve", nil, http.StatusOK)
	if ok["status"] != "minuted" || ok["minutes_hash"] == nil || !strings.Contains(ok["minutes"].(string), "16:10") {
		t.Fatalf("verbale approvato: %v", ok)
	}
	// Immutabile: dall'API e anche scrivendo direttamente nel database.
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/minutes", map[string]any{"text": "riscritto"}, http.StatusUnprocessableEntity)
	seg.do("PUT", "/api/v1/governance/meetings/"+mid+"/attendance", map[string]any{"attendance": []any{}}, http.StatusUnprocessableEntity)
	admin.do("POST", "/api/v1/governance/meetings/"+mid+"/minutes/approve", nil, http.StatusUnprocessableEntity)
	for _, sql := range []string{
		"update meetings set minutes_text = 'manomesso' where id = '" + mid + "'",
		"update meeting_items set note = 'manomesso' where meeting_id = '" + mid + "'",
		"update meeting_attendance set present = false where meeting_id = '" + mid + "'",
		"delete from meetings where id = '" + mid + "'",
	} {
		err := d.InTenant(ctx, e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "verbalizzata") {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// Storia della richiesta: iscrizione all'ordine del giorno, decisione, verbale.
	kinds := map[string]int{}
	for _, h := range admin.do("GET", "/api/v1/elements/"+bigID+"/activity", nil, http.StatusOK)["items"].([]any) {
		m := h.(map[string]any)
		if m["kind"] == "meeting" {
			kinds[m["field_key"].(string)]++
		}
	}
	if kinds["agenda"] != 1 || kinds["decision"] != 1 || kinds["minutes"] != 1 {
		t.Fatalf("storia della seduta sulla richiesta: %v", kinds)
	}
	cal := seg.do("GET", "/api/v1/governance/meetings?from=2027-01-01&to=2027-02-01", nil, http.StatusOK)["items"].([]any)
	if len(cal) != 2 {
		t.Fatalf("calendario di gennaio: %d sedute", len(cal))
	}

	// Isolamento: un altro tenant non vede sedute, organi, graduatoria né punteggi di questo.
	other := newSGEnv(t, "altro")
	oadmin := login(t, other.srv.URL, "admin@"+other.slug+".test", testPassword)
	oadmin.do("GET", "/api/v1/governance/meetings/"+mid, nil, http.StatusNotFound)
	oadmin.do("PATCH", "/api/v1/governance/bodies/"+bodyID, in, http.StatusNotFound)
	oadmin.do("POST", "/api/v1/governance/meetings", map[string]any{"body_id": bodyID, "scheduled_at": "2027-01-19T15:00:00+01:00"}, http.StatusUnprocessableEntity)
	if n := len(oadmin.do("GET", "/api/v1/governance/meetings", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("sedute visibili da un altro tenant: %d", n)
	}
	if n := len(oadmin.do("GET", "/api/v1/portfolio/ranking", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("graduatoria di un altro tenant: %d righe", n)
	}
	if v := oadmin.do("GET", "/api/v1/scorecards/portafoglio", nil, http.StatusOK)["versions"].([]any); len(v) != 1 {
		t.Fatalf("versioni della scorecard nell'altro tenant: %d", len(v))
	}
	err := d.InTenant(ctx, other.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select (select count(*) from meetings) + (select count(*) from meeting_items) + (select count(*) from meeting_attendance)
			+ (select count(*) from scorecard_results) + (select count(*) from governance_bodies where id = $1)`, bodyID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("righe di un altro tenant visibili: %d", n)
		}
		_, err := tx.Exec(ctx, "insert into meeting_attendance (tenant_id, meeting_id, member_id) values ($1, $2, $3)", e.tenantID, mid, chair)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("scrittura tra tenant non bloccata: %v", err)
	}

	var broken *int64
	if err := d.Owner.QueryRow(ctx, "select audit_verify($1)", e.tenantID).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != nil {
		t.Fatalf("catena di audit rotta al seq %d", *broken)
	}
}
