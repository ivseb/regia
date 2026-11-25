package itest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
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
	"regia/internal/modules/knowledge"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Knowledge, rifiniture: commenti in linea con ancore che seguono il testo (e diventano
// orfane se la frase sparisce), thread con risposte, menzioni e stato; conferma di lettura dei
// documenti controllati con campagne, promemoria, riapertura alla nuova versione approvata e
// CSV; indicatori della home; fatto «letture_mancanti»; export e import in zip di Markdown
// con allegati e collegamenti relativi.
func TestKnowledgePlus(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, knowledge.Provision}
	slug := strings.ReplaceAll("kbp-"+time.Now().Format("150405.000"), ".", "")
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Documenti plus", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("kbq-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ name, roles string }{{"Autore", "editor"}, {"Anna", "assessor"}, {"Bruno", "assessor"},
		{"Revisore", "viewer"}, {"Approvatore", "viewer"}, {"Sola", "viewer"}} {
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
		FilesDir: t.TempDir(), MaxUploadBytes: 1 << 20,
	})
	srv := httptest.NewServer(apiSrv.Handler())
	defer srv.Close()
	defer apiSrv.Close()
	in := func(name string) *user {
		return login(t, srv.URL, strings.ToLower(name)+"@"+slug+".test", testPassword)
	}
	adminA, autore, anna, bruno, revisore, approvatore, sola := in("admin"), in("Autore"), in("Anna"), in("Bruno"), in("Revisore"), in("Approvatore"), in("Sola")
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	autore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "DOC", "name": "Documentazione"}, http.StatusCreated)
	body0 := "# Procedura di backup\n\nIl backup parte alle 02:00 ogni notte.\n\nVerificare ogni mattina l'esito del backup e segnalare gli errori.\n"
	pg := autore.do("POST", "/api/v1/kb/spaces/DOC/pages", map[string]any{"title": "Procedura di backup", "body": body0}, http.StatusCreated)
	key := pg["page"].(map[string]any)["key"].(string)
	pageID := pg["page"].(map[string]any)["id"].(string)
	save := func(u *user, body, note string, base int) map[string]any {
		return u.do("PUT", "/api/v1/kb/pages/"+key, map[string]any{"body": body, "note": note, "base_version": base}, http.StatusOK)
	}
	version := func() int {
		return int(autore.do("GET", "/api/v1/kb/pages/"+key, nil, http.StatusOK)["page"].(map[string]any)["version"].(float64))
	}
	threads := func(u *user) map[string]any {
		return u.do("GET", "/api/v1/kb/pages/"+key+"/threads", nil, http.StatusOK)
	}
	thread0 := func(u *user) map[string]any { return threads(u)["items"].([]any)[0].(map[string]any) }

	// --- Commenti in linea -------------------------------------------------------------------
	sola.do("POST", "/api/v1/kb/pages/"+key+"/threads", map[string]any{"quote": "backup parte", "body": "no"}, http.StatusForbidden)
	if l := threads(sola); l["can_comment"] != false || len(l["items"].([]any)) != 0 {
		t.Fatalf("chi non commenta: %v", l)
	}
	anna.do("POST", "/api/v1/kb/pages/"+key+"/threads", map[string]any{"quote": "", "body": "vuota"}, http.StatusUnprocessableEntity)
	anna.do("POST", "/api/v1/kb/pages/"+key+"/threads", map[string]any{"quote": "backup parte", "body": "  "}, http.StatusUnprocessableEntity)
	l := anna.do("POST", "/api/v1/kb/pages/"+key+"/threads", map[string]any{"quote": "Verificare ogni mattina", "prefix": "ogni notte. ", "suffix": " l'esito del backup",
		"body": "Chi lo verifica nel weekend?"}, http.StatusCreated)
	th := l["items"].([]any)[0].(map[string]any)
	thID := th["id"].(string)
	if th["orphan"] != false || th["pos"] == nil || th["status"] != "open" || len(th["comments"].([]any)) != 1 {
		t.Fatalf("thread creato: %v", th)
	}
	// L'autore della pagina (segnalatore) riceve la notifica del commento in linea.
	if n := kinds(autore.notes(), key)["comment"]; n != 1 {
		t.Fatalf("notifiche all'autore: %v", autore.notes())
	}
	bruno.do("POST", "/api/v1/kb/threads/"+thID+"/comments", map[string]any{"body": "@Anna lo faccio io il sabato"}, http.StatusCreated)
	if kinds(anna.notes(), key)["mention"] != 1 || kinds(anna.notes(), key)["comment"] != 0 {
		t.Fatalf("la menzionata riceve la menzione, non anche il commento: %v", kinds(anna.notes(), key))
	}
	autore.do("POST", "/api/v1/kb/threads/"+thID+"/comments", map[string]any{"body": "Ok, aggiungo il turno"}, http.StatusCreated)
	if kinds(anna.notes(), key)["comment"] != 1 || kinds(bruno.notes(), key)["comment"] != 1 {
		t.Fatalf("le risposte vanno agli autori del thread: anna=%v bruno=%v", kinds(anna.notes(), key), kinds(bruno.notes(), key))
	}
	if cs := thread0(autore)["comments"].([]any); len(cs) != 3 || cs[1].(map[string]any)["mentions"].([]any)[0].(map[string]any)["name"] != "Anna" {
		t.Fatalf("risposte e menzioni: %v", cs)
	}
	// Risolvere avvisa chi ha aperto il thread; una risposta lo riapre.
	bruno.do("POST", "/api/v1/kb/threads/"+thID+"/resolve", nil, http.StatusOK)
	if th := thread0(anna); th["status"] != "resolved" || th["resolved_by"] != "Bruno" {
		t.Fatalf("thread risolto: %v", th)
	}
	if kinds(anna.notes(), key)["comment"] != 2 {
		t.Fatalf("notifica di risoluzione: %v", anna.notes())
	}
	anna.do("POST", "/api/v1/kb/threads/"+thID+"/reopen", nil, http.StatusOK)
	// Eliminare: solo il proprio commento, salvo chi gestisce lo spazio.
	cid := thread0(anna)["comments"].([]any)[1].(map[string]any)["id"].(string)
	anna.do("DELETE", "/api/v1/kb/threads/"+thID+"/comments/"+cid, nil, http.StatusForbidden)
	// Il testo cambia davanti alla frase: l'ancora la segue.
	before := int(thread0(anna)["pos"].(float64))
	save(autore, "# Procedura di backup\n\nNota nuova all'inizio della procedura.\n\n"+strings.SplitN(body0, "\n\n", 2)[1], "Nota", version())
	if th := thread0(anna); th["orphan"] != false || int(th["pos"].(float64)) <= before {
		t.Fatalf("ancora non spostata: prima %d, ora %v", before, th)
	}
	// La frase sparisce: il thread diventa orfano ma resta visibile con il suo testo citato.
	withQuote := autore.do("GET", "/api/v1/kb/pages/"+key, nil, http.StatusOK)["page"].(map[string]any)
	save(autore, "# Procedura di backup\n\nTutto riscritto: il controllo è automatico.", "Riscritto", version())
	if th := thread0(anna); th["orphan"] != true || th["quote"] != "Verificare ogni mattina" || len(th["comments"].([]any)) != 3 {
		t.Fatalf("thread orfano: %v", th)
	}
	// Il ripristino riporta la frase: l'ancora torna.
	autore.do("POST", "/api/v1/kb/pages/"+key+"/versions/"+itoa(int(withQuote["version"].(float64)))+"/restore", map[string]any{}, http.StatusOK)
	if th := thread0(anna); th["orphan"] != false {
		t.Fatalf("thread ancorato di nuovo: %v", th)
	}
	// Un thread risolto da chi gestisce lo spazio; il commento si elimina.
	autore.do("DELETE", "/api/v1/kb/threads/"+thID+"/comments/"+cid, nil, http.StatusOK)
	if cs := thread0(anna)["comments"].([]any); len(cs) != 2 {
		t.Fatalf("commento eliminato: %v", cs)
	}
	// Isolamento tra tenant.
	adminB.do("GET", "/api/v1/kb/pages/"+key+"/threads", nil, http.StatusNotFound)
	adminB.do("POST", "/api/v1/kb/threads/"+thID+"/resolve", nil, http.StatusNotFound)

	// --- Documento controllato e conferma di lettura -----------------------------------------
	ctl := autore.do("PUT", "/api/v1/kb/pages/"+key+"/control", map[string]any{"controlled": true, "review_months": 12,
		"reviewers": []string{revisore.member}, "approvers": []string{approvatore.member}}, http.StatusOK)
	// Nel pubblico può esserci chi legge lo spazio: Sola (viewer) sì, ma l'amministratore del
	// tenant e i ruoli si risolvono comunque in persone.
	autore.do("GET", "/api/v1/kb/audience", nil, http.StatusOK)
	sola.do("GET", "/api/v1/kb/audience", nil, http.StatusForbidden)
	aud := []map[string]any{{"kind": "member", "id": anna.member}, {"kind": "member", "id": bruno.member}}
	prev := autore.do("POST", "/api/v1/kb/pages/"+key+"/readings/preview", map[string]any{"audience": aud}, http.StatusOK)
	if len(prev["people"].([]any)) != 2 {
		t.Fatalf("anteprima del pubblico: %v", prev)
	}
	anna.do("POST", "/api/v1/kb/pages/"+key+"/readings", map[string]any{"audience": aud, "version": 1}, http.StatusForbidden)
	autore.do("POST", "/api/v1/kb/pages/"+key+"/readings", map[string]any{"audience": []any{}, "version": 1}, http.StatusUnprocessableEntity)
	autore.do("POST", "/api/v1/kb/pages/"+key+"/readings", map[string]any{"audience": aud, "version": 99}, http.StatusUnprocessableEntity)
	autore.do("POST", "/api/v1/kb/pages/"+key+"/readings", map[string]any{"audience": aud, "version": 1, "due_date": "2020-01-01"}, http.StatusUnprocessableEntity)
	curV := int(ctl["page"].(map[string]any)["version"].(float64))
	due := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	rd := autore.do("POST", "/api/v1/kb/pages/"+key+"/readings", map[string]any{"name": "Presa visione backup", "audience": aud, "version": curV, "due_date": due}, http.StatusCreated)
	camp := rd["items"].([]any)[0].(map[string]any)
	cID := camp["id"].(string)
	if camp["total"].(float64) != 2 || camp["confirmed"].(float64) != 0 || camp["status"] != "active" || len(camp["people"].([]any)) != 2 {
		t.Fatalf("campagna: %v", camp)
	}
	if kinds(anna.notes(), key)["document"] != 1 {
		t.Fatalf("richiesta di lettura: %v", anna.notes())
	}
	// Chi non è nel pubblico non conferma; la versione sbagliata nemmeno.
	sola.do("POST", "/api/v1/kb/readings/"+cID+"/confirm", map[string]any{"version": curV}, http.StatusForbidden)
	anna.do("POST", "/api/v1/kb/readings/"+cID+"/confirm", map[string]any{"version": curV - 1}, http.StatusUnprocessableEntity)
	ar := anna.do("POST", "/api/v1/kb/readings/"+cID+"/confirm", map[string]any{"version": curV}, http.StatusOK)
	mine := ar["items"].([]any)[0].(map[string]any)
	if mine["my_state"] != "confirmed" || mine["confirmed"].(float64) != 1 || len(mine["people"].([]any)) != 0 {
		t.Fatalf("dopo la conferma: %v", mine)
	}
	anna.do("POST", "/api/v1/kb/readings/"+cID+"/confirm", map[string]any{"version": curV}, http.StatusOK) // idempotente
	if c := autore.do("GET", "/api/v1/kb/pages/"+key+"/readings", nil, http.StatusOK)["items"].([]any)[0].(map[string]any); c["confirmed"].(float64) != 1 {
		t.Fatalf("una sola conferma per persona e versione: %v", c)
	}
	// Indicatori della home: campagna attiva, una conferma mancante, quella di Bruno a me.
	h := bruno.do("GET", "/api/v1/kb/home", nil, http.StatusOK)
	ind := h["indicators"].(map[string]any)
	if ind["campaigns_active"].(float64) != 1 || ind["readings_missing"].(float64) != 1 || ind["my_readings"].(float64) != 1 || ind["open_threads"].(float64) != 1 {
		t.Fatalf("indicatori: %v", ind)
	}
	if mr := h["my_readings"].([]any); len(mr) != 1 || mr[0].(map[string]any)["key"] != key {
		t.Fatalf("le mie letture: %v", mr)
	}
	if ot := h["open_threads"].([]any); len(ot) != 1 || ot[0].(map[string]any)["key"] != key {
		t.Fatalf("discussioni aperte: %v", ot)
	}
	// Fatto per le condizioni di processo.
	fact := func() float64 {
		var n float64
		if err := d.InTenant(ctx, ten.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			el, err := eng.Get(ctx, tx, engine.Actor{Permissions: []string{"*"}}, key)
			if err != nil {
				return err
			}
			f, err := eng.ElementFacts(ctx, tx, el)
			if err != nil {
				return err
			}
			n = f["letture_mancanti"][0].(float64)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := fact(); n != 1 {
		t.Fatalf("letture_mancanti = %v", n)
	}
	// Sollecito manuale: parte una volta, poi bisogna aspettare.
	autore.do("POST", "/api/v1/kb/readings/"+cID+"/remind", nil, http.StatusOK)
	autore.do("POST", "/api/v1/kb/readings/"+cID+"/remind", nil, http.StatusUnprocessableEntity)
	bruno.do("POST", "/api/v1/kb/readings/"+cID+"/remind", nil, http.StatusForbidden)
	if kinds(bruno.notes(), key)["document"] != 2 || kinds(anna.notes(), key)["document"] != 1 {
		t.Fatalf("il sollecito va solo a chi manca: bruno=%v anna=%v", kinds(bruno.notes(), key), kinds(anna.notes(), key))
	}
	// Promemoria automatico: la scadenza è tra 10 giorni, avviso a 3: ancora niente; con 14 sì.
	if n, err := kb.CheckReminders(ctx, d); err != nil || n != 0 {
		t.Fatalf("promemoria prematuro: %d %v", n, err)
	}
	autore.do("PATCH", "/api/v1/kb/readings/"+cID, map[string]any{"remind_days": 14}, http.StatusOK)
	if n, err := kb.CheckReminders(ctx, d); err != nil || n != 1 {
		t.Fatalf("promemoria: %d %v", n, err)
	}
	if n, err := kb.CheckReminders(ctx, d); err != nil || n != 0 {
		t.Fatalf("una volta sola per scadenza: %d %v", n, err)
	}
	// CSV per l'audit.
	code, hdr, raw := autore.get("/api/v1/kb/readings/" + cID + "/export")
	if code != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !strings.Contains(string(raw), "Anna;") ||
		!strings.Contains(string(raw), "confermata") || !strings.Contains(string(raw), "da confermare") {
		t.Fatalf("csv: %d %s", code, raw)
	}
	if code, _, _ := bruno.get("/api/v1/kb/readings/" + cID + "/export"); code != http.StatusForbidden {
		t.Fatalf("csv a chi non gestisce: %d", code)
	}
	// Una versione nuova approvata riapre la campagna: chi aveva confermato deve rileggere.
	tr := func(u *user, to string) {
		u.do("POST", "/api/v1/elements/"+pageID+"/transitions", map[string]any{"to": to}, http.StatusOK)
	}
	stepFor := func(u *user) string {
		for _, it := range u.do("GET", "/api/v1/approvals/inbox", nil, http.StatusOK)["items"].([]any) {
			if it.(map[string]any)["element_key"] == key {
				return it.(map[string]any)["step_id"].(string)
			}
		}
		return ""
	}
	save(autore, "# Procedura di backup\n\nIl backup parte alle 03:00. Verificare ogni mattina l'esito.", "Nuovo orario", version())
	tr(autore, "In revisione")
	revisore.do("POST", "/api/v1/approvals/steps/"+stepFor(revisore)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	approvatore.do("POST", "/api/v1/approvals/steps/"+stepFor(approvatore)+"/decision", map[string]any{"decision": "approved"}, http.StatusNoContent)
	appV := int(autore.do("GET", "/api/v1/kb/pages/"+key, nil, http.StatusOK)["page"].(map[string]any)["approved_version"].(float64))
	re := autore.do("GET", "/api/v1/kb/pages/"+key+"/readings", nil, http.StatusOK)
	c2 := re["items"].([]any)[0].(map[string]any)
	if int(c2["version"].(float64)) != appV || c2["confirmed"].(float64) != 0 || c2["status"] != "active" {
		t.Fatalf("campagna riaperta alla v%d: %v", appV, c2)
	}
	for _, p := range c2["people"].([]any) {
		if p.(map[string]any)["name"] == "Anna" && p.(map[string]any)["state"] != "outdated" {
			t.Fatalf("Anna ha letto una versione precedente: %v", p)
		}
	}
	if n := fact(); n != 2 {
		t.Fatalf("letture_mancanti dopo la riapertura = %v", n)
	}
	if kinds(anna.notes(), key)["document"] < 2 {
		t.Fatalf("avviso di riapertura: %v", kinds(anna.notes(), key))
	}
	// Chiusa la campagna non si conferma più e non conta più nel fatto.
	autore.do("PATCH", "/api/v1/kb/readings/"+cID, map[string]any{"status": "closed"}, http.StatusOK)
	anna.do("POST", "/api/v1/kb/readings/"+cID+"/confirm", map[string]any{"version": appV}, http.StatusUnprocessableEntity)
	if n := fact(); n != 0 {
		t.Fatalf("letture_mancanti a campagna chiusa = %v", n)
	}

	// --- Export e import in Markdown ---------------------------------------------------------
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	att := autore.upload("/api/v1/elements/"+pageID+"/attachments", "schema backup.png", pngBuf.Bytes(), "", http.StatusCreated)
	attURL := "/api/v1/attachments/" + att["id"].(string) + "/content"
	child := autore.do("POST", "/api/v1/kb/spaces/DOC/pages", map[string]any{"title": "Ripristino dei dati", "parent": key,
		"body": "Torna al [backup](" + key + ").\n"}, http.StatusCreated)["page"].(map[string]any)["key"].(string)
	// La pagina madre è un documento controllato approvato: per scriverla si torna in bozza.
	save(autore, "# Procedura di backup\n\n![schema]("+attURL+")\n\nVedi [il ripristino]("+child+") e l'[allegato]("+attURL+"?download=1).", "Con schema", version())
	code, hdr, zipRaw := autore.get("/api/v1/kb/spaces/DOC/export")
	if code != http.StatusOK || hdr.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %s", code, hdr.Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(zipRaw), int64(len(zipRaw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		entries[f.Name] = string(b)
	}
	root := "procedura-di-backup.md"
	if !strings.Contains(entries[root], "titolo: \"Procedura di backup\"") || !strings.Contains(entries[root], "chiave: "+key) ||
		!strings.Contains(entries[root], "![schema](_allegati/"+key+"/schema_backup.png)") ||
		!strings.Contains(entries[root], "(procedura-di-backup/ripristino-dei-dati.md)") {
		t.Fatalf("pagina esportata:\n%s\nfile: %v", entries[root], keysOfMap(entries))
	}
	if _, ok := entries["_allegati/"+key+"/schema_backup.png"]; !ok {
		t.Fatalf("allegato mancante: %v", keysOfMap(entries))
	}
	if c := entries["procedura-di-backup/ripristino-dei-dati.md"]; !strings.Contains(c, "(../procedura-di-backup.md)") {
		t.Fatalf("sottopagina esportata:\n%s", c)
	}
	if !strings.Contains(entries[knowledge.ManifestName], "regia-markdown/1") {
		t.Fatalf("manifest: %s", entries[knowledge.ManifestName])
	}
	// Chi non legge lo spazio non esporta; solo la sottopagina con ?pagina=.
	if code, _, _ := adminB.get("/api/v1/kb/spaces/DOC/export"); code != http.StatusNotFound {
		t.Fatalf("export di un altro tenant: %d", code)
	}
	_, _, sub := autore.get("/api/v1/kb/spaces/DOC/export?pagina=" + child)
	if zs, _ := zip.NewReader(bytes.NewReader(sub), int64(len(sub))); len(zs.File) != 2 { // la pagina e il manifest
		t.Fatalf("sottoalbero: %d file", len(zs.File))
	}
	// Import dello stesso zip in un nuovo spazio: anteprima, poi esecuzione.
	autore.do("POST", "/api/v1/kb/spaces", map[string]any{"key": "IMP", "name": "Importate"}, http.StatusCreated)
	anna.importZip("/api/v1/kb/spaces/IMP/import", zipRaw, false, "", http.StatusForbidden)
	dry := autore.importZip("/api/v1/kb/spaces/IMP/import", zipRaw, true, "", http.StatusOK)
	if dry["dry"] != true || dry["pages"].(float64) != 2 || dry["attachments"].(float64) != 1 {
		t.Fatalf("anteprima dell'import: %v", dry)
	}
	if pages := autore.do("GET", "/api/v1/kb/spaces/IMP", nil, http.StatusOK)["pages"].([]any); len(pages) != 0 {
		t.Fatalf("l'anteprima non scrive: %v", pages)
	}
	done := autore.importZip("/api/v1/kb/spaces/IMP/import", zipRaw, false, "", http.StatusOK)
	items := done["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["key"] != "IMP-1" || items[1].(map[string]any)["level"].(float64) != 1 {
		t.Fatalf("import: %v", done)
	}
	imp := autore.do("GET", "/api/v1/kb/pages/IMP-1", nil, http.StatusOK)
	body := imp["page"].(map[string]any)["body"].(string)
	if imp["page"].(map[string]any)["title"] != "Procedura di backup" || !strings.Contains(body, "(IMP-2)") || strings.Contains(body, "_allegati") ||
		!strings.Contains(body, "![schema](/api/v1/attachments/") {
		t.Fatalf("pagina importata:\n%s", body)
	}
	if v := imp["page"].(map[string]any)["version"].(float64); v != 1 {
		t.Fatalf("l'import non crea versioni extra: %v", v)
	}
	if kids := imp["children"].([]any); len(kids) != 1 || kids[0].(map[string]any)["title"] != "Ripristino dei dati" {
		t.Fatalf("figli importati: %v", imp["children"])
	}
	// L'immagine importata è un allegato scaricabile della pagina nuova.
	imgPath := body[strings.Index(body, "/api/v1/attachments/"):]
	imgPath = imgPath[:strings.Index(imgPath, ")")]
	if code, h2, b := autore.get(imgPath); code != http.StatusOK || h2.Get("Content-Type") != "image/png" || !bytes.Equal(b, pngBuf.Bytes()) {
		t.Fatalf("allegato importato: %d %s", code, h2.Get("Content-Type"))
	}
	// Zip scritto altrove: cartelle senza pagina, titolo dal primo «# », indice di cartella,
	// collegamento relativo, file sospetti ignorati.
	var foreign bytes.Buffer
	zw := zip.NewWriter(&foreign)
	add := func(name, content string) {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	add("manuale/index.md", "# Manuale operativo\n\nBenvenuti. Vedi [avvio](avvio.md#passi).\n")
	add("manuale/avvio.md", "# Avvio del sistema\n\nPremere il pulsante.\n")
	add("runbook/db/ripristino.md", "Ripartire dal backup.\n")
	add("../../etc/passwd.md", "non deve entrare")
	add(".DS_Store", "x")
	_ = zw.Close()
	fr := autore.importZip("/api/v1/kb/spaces/IMP/import", foreign.Bytes(), false, "IMP-1", http.StatusOK)
	titles := map[string]float64{}
	for _, it := range fr["items"].([]any) {
		titles[it.(map[string]any)["title"].(string)] = it.(map[string]any)["level"].(float64)
	}
	// Manuale operativo (indice) → Avvio; Runbook (cartella) → Db (cartella) → Ripristino.
	if len(titles) != 5 || titles["Manuale operativo"] != 0 || titles["Avvio del sistema"] != 1 || titles["Runbook"] != 0 || titles["Db"] != 1 || titles["Ripristino"] != 2 {
		t.Fatalf("struttura importata: %v", titles)
	}
	man := autore.do("GET", "/api/v1/kb/pages/"+fr["items"].([]any)[0].(map[string]any)["key"].(string), nil, http.StatusOK)
	if p := man["page"].(map[string]any); !strings.Contains(p["body"].(string), "[avvio](IMP-") {
		t.Fatalf("collegamento relativo: %v", p["body"])
	}
	if path := man["path"].([]any); len(path) != 1 || path[0].(map[string]any)["key"] != "IMP-1" {
		t.Fatalf("pagine importate sotto IMP-1: %v", man["path"])
	}
	autore.importZip("/api/v1/kb/spaces/IMP/import", []byte("non è uno zip"), false, "", http.StatusBadRequest)
	// Audit: restano le tracce delle operazioni sensibili.
	seen := map[string]bool{}
	for _, it := range adminA.do("GET", "/api/v1/audit?limit=500", nil, http.StatusOK)["items"].([]any) {
		seen[it.(map[string]any)["action"].(string)] = true
	}
	for _, a := range []string{"page.thread.create", "page.thread.reply", "page.reading.create", "page.reading.confirm", "page.reading.export", "space.export", "space.import"} {
		if !seen[a] {
			t.Fatalf("manca l'audit %s: %v", a, seen)
		}
	}
}

// importZip invia lo zip in multipart (campi file, parent, dry).
func (u *user) importZip(path string, data []byte, dry bool, parent string, wantStatus int) map[string]any {
	u.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if dry {
		_ = mw.WriteField("dry", "1")
	}
	if parent != "" {
		_ = mw.WriteField("parent", parent)
	}
	fw, _ := mw.CreateFormFile("file", "import.zip")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", u.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		u.t.Fatalf("import %s: stato %d, atteso %d: %s", path, resp.StatusCode, wantStatus, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func keysOfMap(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
