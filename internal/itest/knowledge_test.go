package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"regia/internal/modules/knowledge"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Knowledge (piano, passo 19): spazi aperti e riservati con ruoli sullo spazio (persone e
// gruppi), albero e ordinamento manuale, versioni con confronto e ripristino, blocco di
// modifica, documento controllato con revisori, approvatori e revisione periodica, ricerca
// nello spazio e nella query, collegamenti e citazioni degli elementi, isolamento tra tenant.
func TestKnowledge(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, knowledge.Provision}
	slug := strings.ReplaceAll("kb-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Documenti", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("kbo-"+time.Now().Format("150405.000"), ".", "")
	tenB, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	// Idempotente: tipo, ruoli, politica, processo e template non si duplicano.
	if err := tenant.Provision(ctx, d, slug, prov); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ name, roles string }{{"Autore", "editor"}, {"Lettore", "viewer"}, {"Revisore", "viewer"},
		{"Approvatore", "viewer"}, {"Esterno", "viewer"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: strings.ToLower(m.name) + "@" + slug + ".test",
			Name: m.name, Password: testPassword, Roles: strings.Split(m.roles, ",")}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	kb := knowledge.Register(eng, ap)
	apiSrv := api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc, Knowledge: kb,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	in := func(name string) *user {
		return login(t, srv.URL, strings.ToLower(name)+"@"+slug+".test", testPassword)
	}
	admin, autore, lettore, revisore, approvatore, esterno := in("admin"), in("Autore"), in("Lettore"), in("Revisore"), in("Approvatore"), in("Esterno")
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)
	q := url.QueryEscape
	keysOf := func(items []any, field string) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.(map[string]any)[field].(string))
		}
		return out
	}

	// --- Spazi e permessi --------------------------------------------------------------------
	lettore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "DOC", "name": "Documentazione IT"}, http.StatusForbidden)
	sp := autore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "DOC", "name": "Documentazione IT", "description": "Runbook, ADR e policy"}, http.StatusCreated)
	if r := sp["rights"].(map[string]any); r["write"] != true || r["manage"] != true || sp["access"] != "open" {
		t.Fatalf("spazio DOC: %v", sp)
	}
	autore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "DOC", "name": "Doppione"}, http.StatusUnprocessableEntity)
	autore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "SEC", "name": "Sicurezza", "access": "restricted"}, http.StatusCreated)
	lsp := lettore.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)["items"].([]any)
	if k := keysOf(lsp, "key"); len(k) != 1 || k[0] != "DOC" {
		t.Fatalf("spazi del lettore: %v", k)
	}
	if r := lsp[0].(map[string]any)["rights"].(map[string]any); r["read"] != true || r["write"] != false {
		t.Fatalf("diritti del lettore su DOC: %v", r)
	}
	lettore.do("POST", "/api/v1/kb/spaces/DOC/pages", map[string]any{"title": "Non posso"}, http.StatusForbidden)
	esterno.do("GET", "/api/v1/kb/spaces/SEC", nil, http.StatusNotFound)
	// Pagina nello spazio riservato: chi non ha un ruolo sullo spazio non la vede da nessuna parte.
	secPage := autore.do("POST", "/api/v1/kb/spaces/SEC/pages", map[string]any{"title": "Piano di risposta agli incidenti", "body": "Riservato: chiamare il CISO."}, http.StatusCreated)
	secKey := secPage["page"].(map[string]any)["key"].(string)
	esterno.do("GET", "/api/v1/elements/"+secKey, nil, http.StatusNotFound)
	esterno.do("GET", "/api/v1/kb/pages/"+secKey, nil, http.StatusNotFound)
	if items := esterno.do("GET", "/api/v1/elements?q="+q("tipo: page"), nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("pagine riservate nella query: %v", keysOf(items, "key"))
	}
	// L'amministratore del tenant legge anche gli spazi riservati.
	admin.do("GET", "/api/v1/kb/pages/"+secKey, nil, http.StatusOK)
	// Ruolo Lettore sullo spazio a una persona, ruolo Editor a un gruppo.
	autore.do("POST", "/api/v1/kb/spaces/SEC/access", map[string]any{"role": "viewer", "member_id": revisore.member}, http.StatusOK)
	revisore.do("GET", "/api/v1/kb/pages/"+secKey, nil, http.StatusOK)
	revisore.do("PUT", "/api/v1/kb/pages/"+secKey, map[string]any{"body": "no", "base_version": 1}, http.StatusForbidden)
	lettore.do("POST", "/api/v1/kb/spaces/SEC/access", map[string]any{"role": "editor", "member_id": lettore.member}, http.StatusNotFound)
	autore.do("POST", "/api/v1/kb/spaces/SEC/access", map[string]any{"role": "tenant_admin", "member_id": lettore.member}, http.StatusUnprocessableEntity)
	grp := admin.do("POST", "/api/v1/admin/groups", map[string]any{"name": "Team sicurezza"}, http.StatusCreated)["id"].(string)
	admin.do("POST", "/api/v1/admin/groups/"+grp+"/members", map[string]any{"member_id": esterno.member}, http.StatusNoContent)
	acc := autore.do("POST", "/api/v1/kb/spaces/SEC/access", map[string]any{"role": "editor", "group_id": grp}, http.StatusOK)["items"].([]any)
	if len(acc) != 3 { // autore (creatore), revisore, gruppo
		t.Fatalf("ruoli su SEC: %v", acc)
	}
	esterno.do("PUT", "/api/v1/kb/pages/"+secKey, map[string]any{"body": "Riservato: chiamare il CISO e il DPO.", "base_version": 1}, http.StatusOK)
	if items := esterno.do("GET", "/api/v1/elements?q="+q("tipo: page"), nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("pagine del gruppo nella query: %v", items)
	}
	var revoke string
	for _, a := range acc {
		if a.(map[string]any)["group_id"] == grp {
			revoke = a.(map[string]any)["id"].(string)
		}
	}
	autore.do("DELETE", "/api/v1/kb/spaces/SEC/access/"+revoke, nil, http.StatusOK)
	esterno.do("GET", "/api/v1/kb/pages/"+secKey, nil, http.StatusNotFound)

	// --- Gerarchia e ordinamento ---------------------------------------------------------------
	mk := func(u *user, title string, extra map[string]any) string {
		body := map[string]any{"title": title}
		for k, v := range extra {
			body[k] = v
		}
		return u.do("POST", "/api/v1/kb/spaces/DOC/pages", body, http.StatusCreated)["page"].(map[string]any)["key"].(string)
	}
	runbook := mk(autore, "Runbook del Portale B2B", map[string]any{"template": "runbook"})
	archi := mk(autore, "Architettura", nil)
	adr := mk(autore, "ADR-001 Autenticazione federata", map[string]any{"parent": archi, "template": "adr"})
	guide := mk(autore, "Guide", nil)
	rb := autore.do("GET", "/api/v1/kb/pages/"+runbook, nil, http.StatusOK)["page"].(map[string]any)
	if body := rb["body"].(string); !strings.Contains(body, "## Panoramica") || strings.Contains(body, "{{") || rb["template_key"] != "runbook" || rb["state"] != "" {
		t.Fatalf("template del runbook: %q", body)
	}
	roots := func() []string {
		var out []string
		for _, p := range lettore.do("GET", "/api/v1/kb/spaces/DOC", nil, http.StatusOK)["pages"].([]any) {
			if m := p.(map[string]any); m["parent_id"] == nil {
				out = append(out, m["key"].(string))
			}
		}
		return out
	}
	if r := roots(); strings.Join(r, ",") != strings.Join([]string{runbook, archi, guide}, ",") {
		t.Fatalf("ordine iniziale: %v", r)
	}
	autore.do("POST", "/api/v1/kb/pages/"+guide+"/move", map[string]any{"before": runbook}, http.StatusOK)
	if r := roots(); strings.Join(r, ",") != strings.Join([]string{guide, runbook, archi}, ",") {
		t.Fatalf("dopo lo spostamento: %v", r)
	}
	autore.do("POST", "/api/v1/kb/pages/"+archi+"/move", map[string]any{"parent": adr}, http.StatusUnprocessableEntity)
	lettore.do("POST", "/api/v1/kb/pages/"+guide+"/move", map[string]any{"after": archi}, http.StatusForbidden)
	moved := autore.do("POST", "/api/v1/kb/pages/"+runbook+"/move", map[string]any{"parent": archi, "after": adr}, http.StatusOK)
	if path := moved["path"].([]any); len(path) != 1 || path[0].(map[string]any)["key"] != archi {
		t.Fatalf("percorso dopo lo spostamento: %v", path)
	}
	if ch := keysOf(autore.do("GET", "/api/v1/kb/pages/"+archi, nil, http.StatusOK)["children"].([]any), "key"); strings.Join(ch, ",") != adr+","+runbook {
		t.Fatalf("figli di Architettura: %v", ch)
	}

	// --- Versioni, confronto e ripristino -------------------------------------------------------
	save := func(u *user, key, body, note string, base int, status int) map[string]any {
		return u.do("PUT", "/api/v1/kb/pages/"+key, map[string]any{"body": body, "note": note, "base_version": base}, status)
	}
	v2 := save(autore, guide, "# Guide\nPrima riga\nSeconda riga da rivedere", "Prima stesura", 1, http.StatusOK)
	if v2["page"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("versione dopo il salvataggio: %v", v2["page"])
	}
	stale := save(admin, guide, "# Guide\naltro", "", 1, http.StatusConflict)
	_ = stale
	save(autore, guide, "# Guide\nPrima riga\nSeconda riga rivista e corretta\nTerza riga", "Revisione", 2, http.StatusOK)
	// Stesso testo: nessuna versione nuova.
	if v := save(autore, guide, "# Guide\nPrima riga\nSeconda riga rivista e corretta\nTerza riga", "", 3, http.StatusOK); v["page"].(map[string]any)["version"].(float64) != 3 {
		t.Fatalf("versione vuota: %v", v["page"])
	}
	vers := lettore.do("GET", "/api/v1/kb/pages/"+guide+"/versions", nil, http.StatusOK)["items"].([]any)
	if len(vers) != 3 || vers[0].(map[string]any)["author"] != "Autore" || vers[0].(map[string]any)["note"] != "Revisione" || vers[0].(map[string]any)["current"] != true {
		t.Fatalf("storico: %v", vers)
	}
	cmp := lettore.do("GET", "/api/v1/kb/pages/"+guide+"/diff?from=2&to=3", nil, http.StatusOK)
	if st := cmp["stats"].(map[string]any); st["changed"].(float64) != 1 || st["added"].(float64) != 1 {
		t.Fatalf("confronto: %v", cmp)
	}
	restored := autore.do("POST", "/api/v1/kb/pages/"+guide+"/versions/2/restore", map[string]any{"note": "testo più chiaro"}, http.StatusOK)["page"].(map[string]any)
	if restored["version"].(float64) != 4 || restored["body"] != "# Guide\nPrima riga\nSeconda riga da rivedere" {
		t.Fatalf("ripristino: %v", restored)
	}
	if v := lettore.do("GET", "/api/v1/kb/pages/"+guide+"/versions/4", nil, http.StatusOK); !strings.HasPrefix(v["note"].(string), "Ripristino della versione 2") {
		t.Fatalf("nota del ripristino: %v", v["note"])
	}
	lettore.do("POST", "/api/v1/kb/pages/"+guide+"/versions/1/restore", nil, http.StatusForbidden)

	// --- Blocco di modifica -----------------------------------------------------------------
	l := autore.do("POST", "/api/v1/kb/pages/"+guide+"/lock", nil, http.StatusOK)
	if l["taken"] != true || l["lock"].(map[string]any)["mine"] != true {
		t.Fatalf("blocco: %v", l)
	}
	l = admin.do("POST", "/api/v1/kb/pages/"+guide+"/lock", nil, http.StatusOK)
	if l["taken"] != false || l["lock"].(map[string]any)["name"] != "Autore" {
		t.Fatalf("blocco di un altro: %v", l)
	}
	if pv := admin.do("GET", "/api/v1/kb/pages/"+guide, nil, http.StatusOK); pv["lock"].(map[string]any)["name"] != "Autore" {
		t.Fatalf("avviso «sta modificando»: %v", pv["lock"])
	}
	if e := save(admin, guide, "# Guide\nsovrascritto", "", 4, http.StatusConflict); e["code"] != "page.locked" {
		t.Fatalf("salvataggio con il blocco di un altro: %v", e)
	}
	admin.do("POST", "/api/v1/kb/pages/"+guide+"/lock?force=true", nil, http.StatusOK)
	save(autore, guide, "# Guide\nmio", "", 4, http.StatusConflict)
	admin.do("DELETE", "/api/v1/kb/pages/"+guide+"/lock", nil, http.StatusNoContent)
	// Blocco scaduto: non conta più.
	autore.do("POST", "/api/v1/kb/pages/"+guide+"/lock", nil, http.StatusOK)
	inTen := func(id string, fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := d.InTenant(ctx, id, db.TenantOpts{}, fn); err != nil {
			t.Fatal(err)
		}
	}
	inTen(ten.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update page_locks set expires_at = now() - interval '1 minute'`)
		return err
	})
	save(admin, guide, "# Guide\nPrima riga\nSeconda riga da rivedere\nAggiunta dell'amministratore", "", 4, http.StatusOK)

	// --- Menzioni, citazioni e collegamenti nel grafo -----------------------------------------
	req := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Portale clienti B2B"}, http.StatusCreated)
	reqKey := req["key"].(string)
	before := lettore.unread()
	adrBody := "# ADR-001\nDecisione presa con @Lettore per " + reqKey + ".\n\n> [!NOTA] Vale anche per ZZZ-99."
	save(autore, adr, adrBody, "Decisione", 1, http.StatusOK)
	if lettore.unread() != before+1 || kinds(lettore.notes(), adr)["mention"] != 1 {
		t.Fatalf("menzione: %v", lettore.notes())
	}
	// Salvare di nuovo con la stessa menzione non notifica ancora.
	save(autore, adr, adrBody+"\nAggiornata.", "", 2, http.StatusOK)
	if kinds(lettore.notes(), adr)["mention"] != 1 {
		t.Fatal("menzione ripetuta")
	}
	refs := lettore.do("GET", "/api/v1/kb/refs?keys="+reqKey+",ZZZ-99,"+secKey, nil, http.StatusOK)["items"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["key"] != reqKey || refs[0].(map[string]any)["state"] == "" {
		t.Fatalf("riferimenti: %v", refs)
	}
	autore.do("POST", "/api/v1/kb/pages/"+runbook+"/links", map[string]any{"element": reqKey}, http.StatusOK)
	lettore.do("POST", "/api/v1/kb/pages/"+runbook+"/links", map[string]any{"element": reqKey}, http.StatusForbidden)
	pv := lettore.do("GET", "/api/v1/kb/pages/"+runbook, nil, http.StatusOK)
	if links := pv["links"].([]any); len(links) != 1 || links[0].(map[string]any)["key"] != reqKey {
		t.Fatalf("elementi documentati: %v", pv["links"])
	}
	// Una pagina riservata cita la richiesta: chi non vede lo spazio non la trova tra i documenti.
	autore.do("PUT", "/api/v1/kb/pages/"+secKey, map[string]any{"body": "Vedi " + reqKey, "base_version": 2}, http.StatusOK)
	docs := lettore.do("GET", "/api/v1/kb/elements/"+reqKey+"/documents", nil, http.StatusOK)["items"].([]any)
	via := map[string]string{}
	for _, dd := range docs {
		via[dd.(map[string]any)["key"].(string)] = dd.(map[string]any)["via"].(string)
	}
	if len(via) != 2 || via[runbook] != "link" || via[adr] != "ref" {
		t.Fatalf("documenti collegati: %v", via)
	}
	if docs := admin.do("GET", "/api/v1/kb/elements/"+reqKey+"/documents", nil, http.StatusOK)["items"].([]any); len(docs) != 3 {
		t.Fatalf("documenti collegati per l'amministratore: %v", docs)
	}
	links := lettore.do("GET", "/api/v1/elements/"+reqKey+"/links", nil, http.StatusOK)
	if rel := links["relations"].([]any); len(rel) != 1 || rel[0].(map[string]any)["relation"] != "documentato da" {
		t.Fatalf("relazioni della richiesta: %v", links)
	}
	autore.do("DELETE", "/api/v1/kb/pages/"+runbook+"/links/"+reqKey, nil, http.StatusOK)
	if docs := lettore.do("GET", "/api/v1/kb/elements/"+reqKey+"/documents", nil, http.StatusOK)["items"].([]any); len(docs) != 1 {
		t.Fatalf("documenti dopo lo scollegamento: %v", docs)
	}

	// --- Documento controllato con approvazione e revisione periodica -----------------------
	policy := autore.do("POST", "/api/v1/kb/spaces/DOC/pages", map[string]any{"title": "Policy di sicurezza delle informazioni", "template": "policy"}, http.StatusCreated)
	pol := policy["page"].(map[string]any)
	polKey := pol["key"].(string)
	if pol["controlled"] != true || pol["state"] != "Bozza" || pol["review_months"].(float64) != 12 {
		t.Fatalf("policy dal template: %v", pol)
	}
	tr := func(u *user, to, comment string, status int) map[string]any {
		return u.do("POST", "/api/v1/elements/"+pol["id"].(string)+"/transitions", map[string]any{"to": to, "comment": comment}, status)
	}
	tr(autore, "In revisione", "", http.StatusUnprocessableEntity) // mancano revisori e approvatori
	autore.do("PUT", "/api/v1/kb/pages/"+polKey+"/control", map[string]any{"controlled": true, "review_months": 12,
		"reviewers": []string{esterno.member}, "approvers": []string{approvatore.member}}, http.StatusOK)
	ctl := autore.do("PUT", "/api/v1/kb/pages/"+polKey+"/control", map[string]any{"controlled": true, "review_months": 12,
		"reviewers": []string{revisore.member}, "approvers": []string{approvatore.member}}, http.StatusOK)
	if rv := ctl["reviewers"].([]any); len(rv) != 1 || rv[0].(map[string]any)["name"] != "Revisore" {
		t.Fatalf("revisori: %v", ctl["reviewers"])
	}
	// In SEC il lettore non vede lo spazio: non può essere revisore di una pagina di SEC.
	autore.do("PUT", "/api/v1/kb/pages/"+secKey+"/control", map[string]any{"controlled": true, "reviewers": []string{lettore.member},
		"approvers": []string{approvatore.member}}, http.StatusUnprocessableEntity)
	polV := int(ctl["page"].(map[string]any)["version"].(float64))
	save(autore, polKey, ctl["page"].(map[string]any)["body"].(string)+"\n\n## Ambito\nTutti i sistemi del Portale B2B.", "Bozza completa", polV, http.StatusOK)
	tr(autore, "In revisione", "", http.StatusOK)
	save(autore, polKey, "durante la revisione", "", polV+1, http.StatusUnprocessableEntity)
	stepFor := func(u *user) string {
		for _, it := range u.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
			if it.(map[string]any)["element_key"] == polKey {
				return it.(map[string]any)["step_id"].(string)
			}
		}
		return ""
	}
	if stepFor(approvatore) != "" {
		t.Fatal("l'approvatore decide dopo il revisore")
	}
	step := stepFor(revisore)
	if step == "" {
		t.Fatal("passo del revisore assente")
	}
	autore.do("POST", "/api/v1/approvals/steps/"+step+"/decision", map[string]any{"decision": "approved"}, http.StatusForbidden)
	revisore.do("POST", "/api/v1/approvals/steps/"+step+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	approvatore.do("POST", "/api/v1/approvals/steps/"+stepFor(approvatore)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	ap1 := lettore.do("GET", "/api/v1/kb/pages/"+polKey, nil, http.StatusOK)["page"].(map[string]any)
	// Il database lavora in UTC (embeddedpg: timezone=UTC): current_date è la data UTC, quindi anche l'attesa.
	wantNext := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	if ap1["state"] != "Approvata" || ap1["approved_version"].(float64) != float64(polV+1) || ap1["next_review"] != wantNext {
		t.Fatalf("policy approvata: %v", ap1)
	}
	vers = lettore.do("GET", "/api/v1/kb/pages/"+polKey+"/versions", nil, http.StatusOK)["items"].([]any)
	if v := vers[0].(map[string]any); v["in_force"] != true || v["approved_by"] != "Approvatore" {
		t.Fatalf("versione approvata nello storico: %v", v)
	}
	// Si lavora alla versione successiva: torna in bozza, l'approvata resta leggibile.
	approvedBody := ap1["body"].(string)
	next := save(autore, polKey, approvedBody+"\n\n## Eccezioni\nNuova regola.", "Versione 2", polV+1, http.StatusOK)
	if p := next["page"].(map[string]any); p["state"] != "Bozza" || next["approved"].(map[string]any)["body"] != approvedBody {
		t.Fatalf("nuova versione di un documento approvato: %v / %v", p, next["approved"])
	}
	autore.do("POST", "/api/v1/kb/pages/"+polKey+"/reviewed", nil, http.StatusUnprocessableEntity)
	tr(autore, "In revisione", "", http.StatusOK)
	revisore.do("POST", "/api/v1/approvals/steps/"+stepFor(revisore)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	approvatore.do("POST", "/api/v1/approvals/steps/"+stepFor(approvatore)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	if p := lettore.do("GET", "/api/v1/kb/pages/"+polKey, nil, http.StatusOK); p["approved"] != nil || p["page"].(map[string]any)["approved_version"].(float64) != float64(polV+2) {
		t.Fatalf("seconda approvazione: %v", p["page"])
	}
	// Promemoria della revisione periodica: una volta per scadenza, agli approvatori.
	inTen(ten.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update kb_pages set next_review = current_date + 5 where element_id = $1`, pol["id"]); err != nil {
			return err
		}
		n, err := kb.CheckRemindersTenant(ctx, tx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("promemoria inviati: %d", n)
		}
		if n, err = kb.CheckRemindersTenant(ctx, tx); err != nil || n != 0 {
			return fmt.Errorf("promemoria ripetuto: %d %v", n, err)
		}
		return nil
	})
	if kinds(approvatore.notes(), polKey)["document"] != 1 {
		t.Fatalf("promemoria all'approvatore: %v", approvatore.notes())
	}
	if hits := lettore.do("GET", "/api/v1/elements?q="+q("tipo: page revisione: +10g"), nil, http.StatusOK)["items"].([]any); len(hits) != 1 {
		t.Fatalf("query revisione: %v", keysOf(hits, "key"))
	}
	lettore.do("POST", "/api/v1/kb/pages/"+polKey+"/reviewed", nil, http.StatusForbidden)
	if p := approvatore.do("POST", "/api/v1/kb/pages/"+polKey+"/reviewed", map[string]any{"note": "nessuna modifica"}, http.StatusOK)["page"].(map[string]any); p["next_review"] != wantNext {
		t.Fatalf("revisione confermata: %v", p["next_review"])
	}
	autore.do("DELETE", "/api/v1/kb/pages/"+polKey, nil, http.StatusUnprocessableEntity)
	if hits := lettore.do("GET", "/api/v1/elements?q="+q("tipo: page spazio: DOC fase: Approvata"), nil, http.StatusOK)["items"].([]any); len(hits) != 1 {
		t.Fatalf("query delle policy approvate: %v", hits)
	}
	tr(autore, "Obsoleta", "", http.StatusUnprocessableEntity)
	tr(autore, "Obsoleta", "Sostituita dalla policy di gruppo", http.StatusOK)
	save(autore, polKey, "no", "", polV+2, http.StatusUnprocessableEntity)

	// --- Ricerca -------------------------------------------------------------------------------
	rbv := autore.do("GET", "/api/v1/kb/pages/"+runbook, nil, http.StatusOK)["page"].(map[string]any)
	save(autore, runbook, rbv["body"].(string)+"\n\n## Riavvio\nProcedura di riavvio del Portale B2B dopo un rilascio.", "Riavvio", int(rbv["version"].(float64)), http.StatusOK)
	hits := lettore.do("GET", "/api/v1/kb/spaces/DOC/search?q="+q("riavv"), nil, http.StatusOK)["items"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["key"] != runbook {
		t.Fatalf("ricerca per prefisso: %v", hits)
	}
	marked := false
	for _, sg := range hits[0].(map[string]any)["excerpt"].([]any) {
		if m := sg.(map[string]any); m["hit"] == true && strings.HasPrefix(strings.ToLower(m["t"].(string)), "riavv") {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("estratto senza evidenza: %v", hits[0])
	}
	if hits := lettore.do("GET", "/api/v1/kb/spaces/DOC/search?q="+q("rilascio portale"), nil, http.StatusOK)["items"].([]any); len(hits) != 1 {
		t.Fatalf("ricerca a parole: %v", hits)
	}
	if hits := lettore.do("GET", "/api/v1/elements?q="+q("tipo: page spazio: DOC riavvio"), nil, http.StatusOK)["items"].([]any); len(hits) != 1 || hits[0].(map[string]any)["key"] != runbook {
		t.Fatalf("query delle pagine: %v", hits)
	}
	lettore.do("GET", "/api/v1/kb/spaces/SEC/search?q=ciso", nil, http.StatusNotFound)
	if hits := revisore.do("GET", "/api/v1/kb/spaces/SEC/search?q=ciso", nil, http.StatusOK)["items"].([]any); len(hits) != 0 {
		t.Fatalf("SEC non cita più il CISO: %v", hits)
	}

	// --- Template dello spazio, home ----------------------------------------------------------
	tpls := lettore.do("GET", "/api/v1/kb/spaces/DOC/templates", nil, http.StatusOK)["items"].([]any)
	if k := keysOf(tpls, "key"); strings.Join(k, ",") != "runbook,adr,policy,verbale,guida" {
		t.Fatalf("template di partenza: %v", k)
	}
	lettore.do("POST", "/api/v1/kb/spaces/DOC/templates", map[string]any{"key": "postmortem", "name": "Postmortem"}, http.StatusForbidden)
	autore.do("POST", "/api/v1/kb/spaces/DOC/templates", map[string]any{"key": "postmortem", "name": "Postmortem", "body": "# {{titolo}}\n## Cronologia"}, http.StatusCreated)
	pm := mk(autore, "Postmortem del 12 novembre", map[string]any{"template": "postmortem"})
	if b := autore.do("GET", "/api/v1/kb/pages/"+pm, nil, http.StatusOK)["page"].(map[string]any)["body"]; b != "# Postmortem del 12 novembre\n## Cronologia" {
		t.Fatalf("template dello spazio: %q", b)
	}
	home := lettore.do("GET", "/api/v1/kb/home", nil, http.StatusOK)
	if r := home["recent"].([]any); len(r) == 0 || r[0].(map[string]any)["space_key"] != "DOC" {
		t.Fatalf("pagine recenti: %v", home)
	}
	if h := revisore.do("GET", "/api/v1/kb/home", nil, http.StatusOK); len(h["spaces"].([]any)) != 2 {
		t.Fatalf("spazi del revisore: %v", h["spaces"])
	}
	// Eliminazione: le sottopagine salgono al genitore.
	autore.do("DELETE", "/api/v1/kb/pages/"+archi, nil, http.StatusNoContent)
	if r := roots(); !strings.Contains(strings.Join(r, ","), adr) || strings.Contains(strings.Join(r, ","), archi) {
		t.Fatalf("radici dopo l'eliminazione: %v", r)
	}

	// --- Isolamento tra tenant -----------------------------------------------------------------
	adminB.do("GET", "/api/v1/kb/pages/"+guide, nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/kb/spaces/DOC", nil, http.StatusNotFound)
	if items := adminB.do("GET", "/api/v1/kb/spaces", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("spazi dell'altro tenant: %v", items)
	}
	adminB.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "DOC", "name": "Documenti di B"}, http.StatusCreated)
	if hits := adminB.do("GET", "/api/v1/kb/spaces/DOC/search?q=riavvio", nil, http.StatusOK)["items"].([]any); len(hits) != 0 {
		t.Fatalf("ricerca nell'altro tenant: %v", hits)
	}
	inTen(tenB.TenantID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `select (select count(*) from kb_pages) + (select count(*) from page_versions) + (select count(*) from kb_refs)`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("righe del knowledge visibili dall'altro tenant: %d", n)
		}
		return nil
	})
}
