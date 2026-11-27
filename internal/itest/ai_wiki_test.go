package itest

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"regia/internal/ai"
	"regia/internal/devtools/fakeai"
	"regia/internal/modules/knowledge"
	"regia/internal/platform/db"
)

// Wiki personale (replica/ai.md §2 punto 1 e §7, ondata 4): pagine private della persona (RLS sul
// membro), storia con autore, differenze e annullamento, condivisione in sola lettura, promozione
// a pagina Knowledge, «aggiungi alla wiki» senza AI; aggiornamento con il fornitore finto dalle
// sole fonti che la persona vede, con registro, mascheramento e budget; contesto per l'assistente,
// la scheda e le domande sul grafo; strumento «wiki» anche dal server MCP; conservazione,
// esportazione e cancellazione con il membro.
func TestAIWiki(t *testing.T) {
	e := newOndata2(t, "wiki")
	ownerCount := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := e.d.Owner.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// --- Senza AI la wiki si usa a mano.
	home := e.ed.do("GET", "/api/v1/wiki", nil, http.StatusOK)
	if len(e.list(home["pages"])) != 0 || e.obj(home["ai"])["ready"] != false {
		t.Fatalf("wiki vuota senza AI: %v", home)
	}
	dem := e.ed.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Archivio dei verbali del comitato", "description": "Serve per il comitato."}, http.StatusCreated)
	demKey := dem["key"].(string)
	pg := e.obj(e.ed.do("POST", "/api/v1/wiki/pages", map[string]any{"title": "Comitato IT", "body": "Segue " + demKey + ".\nCodice inesistente APP-NIENTE."}, http.StatusCreated)["page"])
	pid := pg["id"].(string)
	if pg["version"] != float64(1) || pg["updated_by"] != "persona" || pg["mine"] != true {
		t.Fatalf("pagina creata: %v", pg)
	}
	view := e.ed.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusOK)
	if refs := e.list(view["refs"]); len(refs) != 1 || e.obj(refs[0])["key"] != demKey {
		t.Fatalf("collegamenti della pagina: %v", view["refs"])
	}
	e.ed.do("POST", "/api/v1/wiki/pages", map[string]any{"title": "comitato it", "body": "doppia"}, http.StatusConflict)
	e.ed.do("PUT", "/api/v1/wiki/pages/"+pid, map[string]any{"title": "Comitato IT", "body": "Segue " + demKey + ".\nDecisione: avanti.", "base_version": 1}, http.StatusOK)
	// Versione di partenza vecchia: conflitto, niente si perde.
	e.ed.do("PUT", "/api/v1/wiki/pages/"+pid, map[string]any{"title": "Comitato IT", "body": "altro", "base_version": 1}, http.StatusConflict)
	hist := e.list(e.ed.do("GET", "/api/v1/wiki/pages/"+pid+"/history", nil, http.StatusOK)["items"])
	if len(hist) != 2 || e.obj(hist[0])["op"] != "modifica" || e.obj(hist[0])["added"] != float64(1) || e.obj(hist[0])["latest"] != true {
		t.Fatalf("storia: %v", hist)
	}
	diff := e.ed.do("GET", "/api/v1/wiki/pages/"+pid+"/versions/2", nil, http.StatusOK)
	if !strings.Contains(fmtJSON(diff["lines"]), "Decisione: avanti.") {
		t.Fatalf("differenze: %v", diff)
	}
	// Annulla l'ultima modifica: si torna alla versione 1 con una revisione nuova; si annulla anche l'annullamento.
	undo := e.obj(e.ed.do("POST", "/api/v1/wiki/revisions/"+e.obj(hist[0])["id"].(string)+"/undo", nil, http.StatusOK)["page"])
	if undo["version"] != float64(3) || strings.Contains(undo["body"].(string), "Decisione") {
		t.Fatalf("annullamento: %v", undo)
	}
	e.ed.do("POST", "/api/v1/wiki/revisions/"+e.obj(hist[1])["id"].(string)+"/undo", nil, http.StatusConflict) // non è l'ultima
	e.ed.do("POST", "/api/v1/wiki/pages/"+pid+"/restore", map[string]any{"version": 2}, http.StatusOK)
	if b := e.obj(e.ed.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusOK)["page"])["body"].(string); !strings.Contains(b, "Decisione: avanti.") {
		t.Fatalf("ripristino: %q", b)
	}

	// --- Privata: gli altri non la vedono, nemmeno l'amministratore e nemmeno nel database (RLS).
	e.admin.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusNotFound)
	e.cur.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusNotFound)
	var adminMember, edMember, curMember string
	memberOf := func(email string) string {
		var id string
		if err := e.d.Owner.QueryRow(e.ctx, `select m.id::text from members m join accounts a on a.id = m.account_id
			where m.tenant_id = $1 and a.email = $2`, e.tid, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	adminMember, edMember, curMember = memberOf("admin@"+e.slug+".test"), memberOf("ed@"+e.slug+".test"), memberOf("curioso@"+e.slug+".test")
	seen := -1
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{Member: adminMember}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(e.ctx, `select count(*) from wiki_pages`).Scan(&seen); err != nil {
			return err
		}
		tag, err := tx.Exec(e.ctx, `update wiki_pages set body = 'manomesso' where id = $1`, pid)
		if err == nil && tag.RowsAffected() != 0 {
			t.Fatalf("un altro membro modifica la pagina")
		}
		return err
	}); err != nil || seen != 0 {
		t.Fatalf("RLS della wiki: %d pagine viste da un altro membro, %v", seen, err)
	}
	// Condivisa: si legge, ma non si modifica; il richiedente del portale non la vede.
	e.ed.do("POST", "/api/v1/wiki/pages/"+pid+"/share", map[string]any{"shared": true}, http.StatusOK)
	if p := e.obj(e.admin.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusOK)["page"]); p["mine"] != false || p["owner"] != "ed" {
		t.Fatalf("pagina condivisa: %v", p)
	}
	if sh := e.list(e.admin.do("GET", "/api/v1/wiki", nil, http.StatusOK)["shared"]); len(sh) != 1 {
		t.Fatalf("condivise con me: %v", sh)
	}
	e.admin.do("PUT", "/api/v1/wiki/pages/"+pid, map[string]any{"title": "Comitato IT", "body": "intruso"}, http.StatusForbidden)
	e.admin.do("GET", "/api/v1/wiki/pages/"+pid+"/history", nil, http.StatusNotFound)
	e.cur.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusNotFound)

	// --- Promuovi a pagina Knowledge (con i permessi sullo spazio).
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error { return knowledge.Provision(e.ctx, tx, adminMember) }); err != nil {
		t.Fatal(err)
	}
	e.ed.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "NOTE", "name": "Note del team"}, http.StatusCreated)
	prom := e.ed.do("POST", "/api/v1/wiki/pages/"+pid+"/promote", map[string]any{"space": "NOTE"}, http.StatusCreated)
	kbKey := prom["key"].(string)
	if !strings.HasPrefix(kbKey, "NOTE-") || e.obj(e.ed.do("GET", "/api/v1/wiki/pages/"+pid, nil, http.StatusOK)["page"])["promoted_key"] != kbKey {
		t.Fatalf("promozione: %v", prom)
	}
	e.cur.do("POST", "/api/v1/wiki/pages", map[string]any{"title": "Mia", "body": "x"}, http.StatusCreated)

	// --- Aggiungi alla wiki da una scheda, senza AI: riga in «Appunti» e fonte per l'assistente.
	add := e.obj(e.ed.do("POST", "/api/v1/wiki/add", map[string]any{"element": demKey, "note": "da seguire"}, http.StatusOK)["page"])
	if add["title"] != "Appunti" {
		t.Fatalf("aggiungi alla wiki: %v", add)
	}
	e.cur.do("POST", "/api/v1/wiki/add", map[string]any{"element": demKey}, http.StatusNotFound) // non la vede
	if n := ownerCount(`select count(*) from wiki_sources where tenant_id = $1 and used_at is null`, e.tid); n != 1 {
		t.Fatalf("fonti a mano: %d", n)
	}
	e.ed.do("POST", "/api/v1/wiki/refresh", nil, http.StatusConflict) // AI spenta

	// --- AI accesa (fornitore finto, solo il livello approfondito: la wiki usa l'altro livello).
	fake := e.admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "mask_personal_data": true, "deep": map[string]any{"provider_id": fake["id"], "model": fakeai.ModelDeep}}, http.StatusOK)
	if st := e.ed.do("GET", "/api/v1/ai/status", nil, http.StatusOK); e.obj(st["features"])["wiki"] != true {
		t.Fatalf("funzione wiki non pronta: %v", st["features"])
	}
	// Fonti: una richiesta riservata che il richiedente segue ma non può leggere, e una sua.
	secret := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Fusione segreta omega", "description": "Riservata."}, http.StatusCreated)
	mine := e.cur.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Stampante del piano di curioso", "description": "Non stampa."}, http.StatusCreated)
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctx, `insert into watchers (tenant_id, element_id, member_id) values (app_tenant(), $1, $2) on conflict do nothing`, secret["id"], curMember)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	run, err := e.api.AI().UpdateWiki(e.ctx, e.tid, curMember, false)
	if err != nil || run.Outcome != "ok" || len(run.Pages) != 1 || run.Pages[0] != "Le mie priorità" {
		t.Fatalf("aggiornamento della wiki: %+v %v", run, err)
	}
	var wreq *ai.Request
	for _, r := range fakeai.Requests(fakeai.ModelDeep) {
		if strings.Contains(r.System, ai.WikiMarker) {
			wreq = &r
		}
	}
	if wreq == nil {
		t.Fatal("nessuna richiesta della wiki al fornitore")
	}
	all := fmtJSON(wreq.Messages)
	if strings.Contains(all, "Fusione segreta") || strings.Contains(all, secret["key"].(string)) {
		t.Fatalf("la wiki ha letto un elemento non visibile alla persona")
	}
	if !strings.Contains(all, mine["key"].(string)) || strings.Contains(all, "curioso") || !strings.Contains(all, "[persona-") {
		t.Fatalf("fonti della wiki (con mascheramento): %.600s", all)
	}
	if strings.Contains(wreq.System, "Stampante") || !strings.Contains(wreq.Messages[0].Text, `<dati origine="elementi seguiti o creati`) {
		t.Fatalf("il contenuto non è delimitato come dato: %.300s", wreq.Messages[0].Text)
	}
	hasTool := map[string]bool{}
	for _, tl := range wreq.Tools {
		hasTool[tl.Name] = true
	}
	if !hasTool["wiki_scrivi"] || !hasTool["leggi"] || !hasTool["wiki"] || hasTool["commenta"] || hasTool["crea"] {
		t.Fatalf("strumenti dell'aggiornamento: %v", hasTool)
	}
	var prio map[string]any
	for _, p := range e.list(e.cur.do("GET", "/api/v1/wiki", nil, http.StatusOK)["pages"]) {
		if e.obj(p)["title"] == "Le mie priorità" {
			prio = e.obj(p)
		}
	}
	if prio == nil || prio["updated_by"] != "assistente" {
		t.Fatalf("pagina scritta dall'assistente: %v", prio)
	}
	body := e.obj(e.cur.do("GET", "/api/v1/wiki/pages/"+prio["id"].(string), nil, http.StatusOK)["page"])["body"].(string)
	if !strings.Contains(body, "Stampante del piano di curioso") { // smascherato prima di salvare
		t.Fatalf("testo della pagina: %q", body)
	}
	if n := ownerCount(`select count(*) from ai_calls where tenant_id = $1 and feature = 'wiki' and tier = 'rapido' and outcome = 'ok' and member_id = $2`, e.tid, curMember); n < 2 {
		t.Fatalf("registro delle chiamate della wiki: %d", n)
	}
	// Registro della wiki: la modifica dell'assistente si annulla (la pagina nuova si toglie).
	logItems := e.list(e.cur.do("GET", "/api/v1/wiki/log", nil, http.StatusOK)["items"])
	if r0 := e.obj(logItems[0]); r0["author"] != "assistente" || r0["page_title"] != "Le mie priorità" || r0["op"] != "crea" {
		t.Fatalf("registro: %v", logItems)
	}
	e.cur.do("POST", "/api/v1/wiki/revisions/"+e.obj(logItems[0])["id"].(string)+"/undo", nil, http.StatusOK)
	e.cur.do("GET", "/api/v1/wiki/pages/"+prio["id"].(string), nil, http.StatusNotFound)
	logItems = e.list(e.cur.do("GET", "/api/v1/wiki/log", nil, http.StatusOK)["items"])
	if r0 := e.obj(logItems[0]); r0["op"] != "annulla" || r0["author"] != "persona" || r0["deleted"] != true {
		t.Fatalf("annullamento nel registro: %v", logItems[0])
	}
	// Niente di nuovo: l'aggiornamento automatico non chiama il modello.
	before := ownerCount(`select count(*) from ai_calls where tenant_id = $1 and feature = 'wiki'`, e.tid)
	if run, err := e.api.AI().UpdateWiki(e.ctx, e.tid, curMember, false); err != nil || run.Outcome != "nulla" {
		t.Fatalf("aggiornamento senza novità: %+v %v", run, err)
	}
	if after := ownerCount(`select count(*) from ai_calls where tenant_id = $1 and feature = 'wiki'`, e.tid); after != before {
		t.Fatalf("chiamate senza novità: %d → %d", before, after)
	}

	// --- «Aggiorna adesso» dall'API (in background) e lavoro periodico per chi l'ha acceso.
	e.ed.do("PUT", "/api/v1/wiki/settings", map[string]any{"auto": true}, http.StatusOK)
	e.ed.do("POST", "/api/v1/wiki/refresh", nil, http.StatusAccepted)
	deadline := time.Now().Add(20 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		st = e.obj(e.ed.do("GET", "/api/v1/wiki", nil, http.StatusOK)["state"])
		if st["running"] != true && st["last_run_at"] != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st["last_outcome"] != "ok" || st["auto"] != true {
		t.Fatalf("aggiorna adesso: %v", st)
	}
	if n := ownerCount(`select count(*) from wiki_sources where tenant_id = $1 and used_at is null`, e.tid); n != 0 {
		t.Fatalf("fonti a mano non usate: %d", n)
	}

	// --- Contesto per l'assistente: le pagine pertinenti entrano come dato, con un limite.
	e.ed.do("POST", "/api/v1/ai/ask", map[string]any{"question": "Cosa so di " + demKey + "?"}, http.StatusOK)
	reqs := fakeai.Requests(fakeai.ModelDeep)
	last := reqs[len(reqs)-1]
	q := last.Messages[0].Text
	if !strings.Contains(q, `<dati origine="wiki personale">`) || !strings.Contains(q, "## Comitato IT") || len(q) > ai.WikiContextChars+2000 {
		t.Fatalf("contesto della wiki nel pannello: %.500s", q)
	}
	e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": demKey, "action": "riassunto"}, http.StatusOK)
	reqs = fakeai.Requests(fakeai.ModelDeep)
	if c := reqs[len(reqs)-1].Messages[0].Text; !strings.Contains(c, `<dati origine="wiki personale">`) {
		t.Fatalf("contesto della wiki nella scheda: %.500s", c)
	}
	e.ed.do("POST", "/api/v1/ai/graph", map[string]any{"question": "richieste del comitato"}, http.StatusOK)
	reqs = fakeai.Requests(fakeai.ModelDeep)
	if c := reqs[len(reqs)-1].Messages[0].Text; !strings.Contains(c, `<dati origine="wiki personale">`) {
		t.Fatalf("contesto della wiki nelle domande sul grafo: %.500s", c)
	}
	// Senza pagine pertinenti niente contesto (l'altra persona non ha pagine sul comitato).
	e.admin.do("POST", "/api/v1/ai/ask", map[string]any{"question": "Cosa so di " + demKey + "?"}, http.StatusOK)
	reqs = fakeai.Requests(fakeai.ModelDeep)
	if c := reqs[len(reqs)-1].Messages[0].Text; strings.Contains(c, "wiki personale") {
		t.Fatalf("contesto della wiki di un'altra persona: %.300s", c)
	}

	// --- Server MCP: lo strumento «wiki» legge solo la wiki di chi chiama, anche in sola lettura.
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "mask_personal_data": true, "mcp_enabled": true,
		"deep": map[string]any{"provider_id": fake["id"], "model": fakeai.ModelDeep}}, http.StatusOK)
	roTok := e.ed.do("POST", "/api/v1/me/tokens", map[string]any{"name": "sola lettura", "scopes": []string{"element.read"}}, http.StatusCreated)["token"].(string)
	s := e.mcpSession(t, roTok)
	r, err := s.CallTool(e.ctx, &mcp.CallToolParams{Name: "wiki", Arguments: map[string]any{"pagina": "Comitato IT"}})
	if err != nil || r.IsError || !strings.Contains(toolText(r), "Decisione: avanti.") || !strings.Contains(toolText(r), `<dati origine="wiki: Comitato IT">`) {
		t.Fatalf("strumento wiki via MCP: %v %v", toolText(r), err)
	}
	r, _ = s.CallTool(e.ctx, &mcp.CallToolParams{Name: "wiki", Arguments: map[string]any{"pagina": "Mia"}})
	if !r.IsError { // è del richiedente: non si legge
		t.Fatalf("wiki di un'altra persona via MCP: %v", toolText(r))
	}
	if n := ownerCount(`select count(*) from ai_calls where tenant_id = $1 and feature = 'mcp' and model = 'strumento:wiki'`, e.tid); n != 2 {
		t.Fatalf("registro MCP della wiki: %d", n)
	}

	// --- Budget esaurito: l'aggiornamento non parte e lo stato lo dice.
	tiny := int64(1)
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "monthly_token_budget": tiny, "deep": map[string]any{"provider_id": fake["id"], "model": fakeai.ModelDeep}}, http.StatusOK)
	if _, err := e.api.AI().UpdateWiki(e.ctx, e.tid, edMember, true); err == nil {
		t.Fatal("aggiornamento oltre il budget")
	}
	if st := e.obj(e.ed.do("GET", "/api/v1/wiki", nil, http.StatusOK)["state"]); st["last_outcome"] != "errore" || !strings.HasPrefix(st["last_error"].(string), "ai.budget") {
		t.Fatalf("stato dopo il budget: %v", st)
	}

	// --- Esportazione in Markdown.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/wiki/export", nil)
	resp, err := e.ed.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "# Comitato IT") || !strings.Contains(resp.Header.Get("Content-Disposition"), "wiki-") {
		t.Fatalf("esportazione: %d %.200s", resp.StatusCode, raw)
	}

	// --- Conservazione: la storia vecchia si cancella (resta l'ultima versione), le pagine restano.
	for _, q := range []string{`update wiki_revisions set created_at = now() - interval '400 days' where tenant_id = $1`,
		`update wiki_pages set deleted_at = now() - interval '400 days' where tenant_id = $1 and deleted_at is not null`} {
		if _, err := e.d.Owner.Exec(e.ctx, q, e.tid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.api.AI().PurgeWiki(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := ownerCount(`select count(*) from wiki_revisions where tenant_id = $1 and page_id = $2`, e.tid, pid); n != 1 {
		t.Fatalf("storia dopo la conservazione: %d", n)
	}
	if n := ownerCount(`select count(*) from wiki_pages where tenant_id = $1 and deleted_at is not null`, e.tid); n != 0 {
		t.Fatalf("pagine tolte dopo la conservazione: %d", n)
	}

	// --- Il membro anonimizzato perde la wiki.
	if _, err := e.d.Owner.Exec(e.ctx, `update members set status = 'anonymized' where id = $1`, curMember); err != nil {
		t.Fatal(err)
	}
	if n := ownerCount(`select count(*) from wiki_pages where tenant_id = $1 and member_id = $2`, e.tid, curMember); n != 0 {
		t.Fatalf("wiki del membro anonimizzato: %d", n)
	}
}
