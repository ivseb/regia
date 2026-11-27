package itest

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"regia/internal/devtools/fakeai"
	"regia/internal/platform/db"
)

// Assistente nella scheda (replica/ai.md §2 punto 2, ondata 3): riassunto, cosa manca per
// decidere (con i link a campi, commenti ed elementi, e ciò che il modello inventa scartato),
// bozze riscritte dalla persona e inserite con i suoi permessi, duplicati e classificazione alla
// creazione, collegamenti accettati; funzioni e livelli nelle impostazioni, registro e permessi.
func TestAIScheda(t *testing.T) {
	e := newOndata2(t, "aisc")
	cur := e.cur.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Archivio documentale dei contratti", "description": "Va inventariato."}, http.StatusCreated)
	key, id := cur["key"].(string), cur["id"].(string)
	other := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Portale fornitori omega", "description": "Testo della richiesta riservata al team."}, http.StatusCreated)
	oKey, oID := other["key"].(string), other["id"].(string)
	comment := e.ed.do("POST", "/api/v1/elements/"+key+"/comments", map[string]any{"body": "Quale budget è previsto?"}, http.StatusCreated)
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id)
			values (app_tenant(), $1, (select id from relation_types where key = 'depends_on'), $2, $3)`, db.NewID(), id, oID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Spenta: nessuna funzione e la richiesta non parte.
	if st := e.ed.do("GET", "/api/v1/ai/status", nil, http.StatusOK); len(e.obj(st["features"])) != 0 {
		t.Fatalf("funzioni con l'AI spenta: %v", st)
	}
	e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "riassunto"}, http.StatusConflict)

	// Accesa con un solo livello: le funzioni rapide usano l'altro livello.
	fake := e.admin.do("POST", "/api/v1/ai/providers", map[string]any{"kind": "fake", "name": "Finto"}, http.StatusCreated)
	e.admin.do("PUT", "/api/v1/ai/settings", map[string]any{"enabled": true, "deep": map[string]any{"provider_id": fake["id"], "model": fakeai.ModelDeep}}, http.StatusOK)
	st := e.ed.do("GET", "/api/v1/ai/status", nil, http.StatusOK)
	for _, f := range []string{"riassunti", "mancanze", "bozze", "classificazione"} {
		if e.obj(st["features"])[f] != true {
			t.Fatalf("funzione %s non pronta: %v", f, st["features"])
		}
	}
	feats := e.list(e.admin.do("GET", "/api/v1/ai/settings", nil, http.StatusOK)["features"])
	if !slices.ContainsFunc(feats, func(f any) bool {
		m := e.obj(f)
		return m["key"] == "mancanze" && m["active"] == true && m["tier"] == "approfondito"
	}) {
		t.Fatalf("funzione «mancanze» nelle impostazioni: %v", feats)
	}

	// --- Riassunto (rapido): testo con le chiavi citate, registro con l'elemento.
	r := e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "riassunto"}, http.StatusOK)
	if txt := r["text"].(string); !strings.Contains(txt, key) || !strings.Contains(txt, oKey) {
		t.Fatalf("riassunto: %v", r)
	}
	if !strings.Contains(fmtJSON(r["refs"]), oKey) {
		t.Fatalf("link del riassunto: %v", r["refs"])
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'riassunti' and outcome = 'ok' and tier = 'rapido' and $1 = any(element_ids)`, id); n != 1 {
		t.Fatalf("registro del riassunto: %d", n)
	}
	// Il contenuto arriva come dato, mai nelle istruzioni.
	reqs := fakeai.Requests(fakeai.ModelDeep)
	last := reqs[len(reqs)-1]
	if strings.Contains(last.System, "Archivio documentale") || !strings.Contains(last.Messages[0].Text, `<dati origine="`+key+`">`) || len(last.Tools) != 0 {
		t.Fatalf("contesto della scheda: %.400s | %.400s", last.System, last.Messages[0].Text)
	}

	// --- Cosa manca per decidere: prima Regia (descrizione breve), poi il modello (domanda nel
	// commento, con il commento giusto); l'elemento inventato dal modello resta senza link.
	r = e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "mancanze"}, http.StatusOK)
	gaps := e.list(r["gaps"])
	var desc, question, invented map[string]any
	for _, g := range gaps {
		m := e.obj(g)
		switch {
		case m["kind"] == "descrizione" && m["source"] == "regia":
			desc = m
		case m["kind"] == "domanda":
			question = m
		case m["kind"] == "relazione" && m["source"] == "modello":
			invented = m
		}
	}
	if desc == nil || question == nil || question["comment_id"] != comment["id"] || question["comment_by"] != "ed" || invented == nil || invented["ref"] != nil {
		t.Fatalf("mancanze: %v", gaps)
	}
	if r["unverified"] != float64(1) {
		t.Fatalf("voci non verificate: %v", r["unverified"])
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'mancanze' and tier = 'approfondito'`); n != 1 {
		t.Fatalf("registro delle mancanze: %d", n)
	}

	// --- Bozza della risposta al richiedente: menziona chi l'ha aperta; la persona la cambia e
	// la inserisce come commento, con i suoi permessi; nel registro è una decisione della persona.
	r = e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "bozza", "draft": "risposta", "hint": "Ringrazia."}, http.StatusOK)
	draft := r["text"].(string)
	if !strings.HasPrefix(draft, "@curioso ") || !strings.Contains(draft, key) || !strings.Contains(draft, "Ringrazia.") {
		t.Fatalf("bozza della risposta: %q", draft)
	}
	before := e.count(t, `select count(*) from comments where element_id = $1`, id)
	p := e.ed.do("POST", "/api/v1/ai/card/insert", map[string]any{"element": key, "draft": "risposta", "text": draft + " A presto."}, http.StatusOK)
	if p["status"] != "applied" {
		t.Fatalf("inserimento della risposta: %v", p)
	}
	if n := e.count(t, `select count(*) from comments where element_id = $1 and body_text like '%A presto.'`, id); n != 1 || e.count(t, `select count(*) from comments where element_id = $1`, id) != before+1 {
		t.Fatalf("commento inserito: %d", n)
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'bozze' and outcome = 'accepted_by_user' and model = 'proposta:commenta'`); n != 1 {
		t.Fatalf("decisione nel registro: %d", n)
	}
	// Bozza della descrizione, inserita: sostituisce la descrizione.
	r = e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "bozza", "draft": "descrizione"}, http.StatusOK)
	e.ed.do("POST", "/api/v1/ai/card/insert", map[string]any{"element": key, "draft": "descrizione", "text": r["text"]}, http.StatusOK)
	if n := e.count(t, `select count(*) from elements where id = $1 and body_text like 'Contesto: Archivio documentale dei contratti.%'`, id); n != 1 {
		t.Fatalf("descrizione inserita: %d", n)
	}
	// Le bozze della scheda non sono proposte da confermare per il client MCP.
	if n := e.count(t, `select count(*) from ai_proposals where feature is not null and status <> 'applied'`); n != 0 {
		t.Fatalf("proposte della scheda non applicate: %d", n)
	}

	// --- Permessi della persona: il richiedente lavora solo su ciò che vede.
	e.cur.do("POST", "/api/v1/ai/card", map[string]any{"element": oKey, "action": "riassunto"}, http.StatusNotFound)
	r = e.cur.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "riassunto"}, http.StatusOK)
	if strings.Contains(r["text"].(string), oKey) {
		t.Fatalf("il richiedente vede un collegamento riservato: %v", r["text"])
	}
	e.cur.do("POST", "/api/v1/ai/card/insert", map[string]any{"element": oKey, "draft": "commento", "text": "intruso"}, http.StatusUnprocessableEntity)
	e.ed.do("POST", "/api/v1/ai/card", map[string]any{"element": key, "action": "bozza", "draft": "poesia"}, http.StatusUnprocessableEntity)

	// --- Alla creazione: duplicati probabili, campi proposti (urgenza, driver) e collegamenti.
	csv := "codice;nome;criticita;funzione_critica;team;asset\nGDOC;Protocollo elettronico;alta;no;Documenti;Gestione documentale\n"
	e.admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": csv}, http.StatusOK)
	var appKey string
	for _, a := range e.list(e.admin.do("GET", "/api/v1/architecture/assets", nil, http.StatusOK)["items"]) {
		if m := e.obj(a); m["name"] == "Gestione documentale" {
			appKey = m["key"].(string)
		}
	}
	if appKey == "" {
		t.Fatalf("asset logico non creato: %v", e.admin.do("GET", "/api/v1/architecture/assets", nil, http.StatusOK))
	}
	tr := e.ed.do("POST", "/api/v1/ai/card/triage", map[string]any{"type": "business_request", "title": "Nuovo archivio documentale per i contratti",
		"description": "Urgenza alta, motivo normativo. Va integrato con la Gestione documentale."}, http.StatusOK)
	dups := e.list(tr["duplicates"])
	if len(dups) != 1 || e.obj(dups[0])["key"] != key {
		t.Fatalf("duplicati: %v", tr)
	}
	byKey := map[string]map[string]any{}
	for _, f := range e.list(tr["fields"]) {
		byKey[e.obj(f)["key"].(string)] = e.obj(f)
	}
	if byKey["urgenza"]["display"] != "Alta" || byKey["driver"]["value"] != "regulatory" {
		t.Fatalf("campi proposti: %v", tr["fields"])
	}
	if links := e.list(tr["links"]); len(links) != 1 || e.obj(links[0])["key"] != appKey {
		t.Fatalf("collegamenti proposti: %v", tr["links"])
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'classificazione' and tier = 'rapido'`); n != 1 {
		t.Fatalf("registro della classificazione: %d", n)
	}
	// Il richiedente non vede la richiesta dell'altro né le applicazioni: niente duplicati da lì.
	tr = e.cur.do("POST", "/api/v1/ai/card/triage", map[string]any{"type": "business_request", "title": "Portale fornitori omega bis", "description": "Gestione documentale"}, http.StatusOK)
	if len(e.list(tr["duplicates"])) != 0 || len(e.list(tr["links"])) != 0 {
		t.Fatalf("triage del richiedente: %v", tr)
	}
	// Asset accettati dopo il salvataggio («riguarda»), con i permessi di chi li accetta.
	created := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Nuovo archivio documentale per i contratti", "description": "Testo."}, http.StatusCreated)
	res := e.list(e.admin.do("POST", "/api/v1/ai/card/links", map[string]any{"element": created["key"], "links": []string{appKey}}, http.StatusOK)["items"])
	if len(res) != 1 || e.obj(res[0])["ok"] != true {
		t.Fatalf("collegamento: %v", res)
	}
	if n := e.count(t, `select count(*) from relations r join relation_types rt on rt.id = r.type_id where rt.key = 'concerns' and r.source_id = $1`, created["id"]); n != 1 {
		t.Fatalf("relazione creata: %d", n)
	}
	res = e.list(e.cur.do("POST", "/api/v1/ai/card/links", map[string]any{"element": key, "links": []string{oKey}}, http.StatusOK)["items"])
	if len(res) != 1 || e.obj(res[0])["error"] == nil {
		t.Fatalf("collegamento del richiedente: %v", res)
	}
	if n := e.count(t, `select count(*) from ai_calls where feature = 'classificazione' and outcome = 'accepted_by_user'`); n != 1 {
		t.Fatalf("decisione sul collegamento nel registro: %d", n)
	}
}
