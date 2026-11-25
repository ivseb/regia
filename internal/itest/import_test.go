package itest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/collab"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/importer"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Import di progetti da altri strumenti (docs/import.md): CSV di Jira, YouTrack e generico,
// API REST con un server finto, mappatura, prova senza salvare, esecuzione in background a
// lotti, idempotenza, gerarchia, collegamenti, commenti, ore, sprint, annullamento,
// isolamento tra tenant e un test di volume.

type importEnv struct {
	t        *testing.T
	d        *db.DB
	slug     string
	tenantID string
	url      string
	admin    *user
	dev      *user
	other    *user
	imp      *importer.Service
}

func newImportEnv(t *testing.T, prefix string, batch int, filesDir string) *importEnv {
	t.Helper()
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Import", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	other := slug + "-b"
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, name, roles string }{
		{"anna.bianchi", "Anna Bianchi", "editor"}, {"marco.neri", "Marco Neri", "editor"}, {"dev", "Sviluppatore", "editor"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: m.name,
			Password: testPassword, Roles: strings.Split(m.roles, ",")}); err != nil {
			t.Fatal(err)
		}
	}
	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	imp := importer.New(importer.Options{DB: d, Engine: eng, AllowPrivate: true, Log: log, Files: filesStore(t, filesDir)})
	imp.SetBatchSize(batch)
	apiSrv := api.New(api.Options{DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: log, Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour, Importer: imp, FilesDir: filesDir})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(func() {
		imp.Close()
		srv.Close()
		apiSrv.Close()
	})
	e := &importEnv{t: t, d: d, slug: slug, tenantID: ten.TenantID, url: srv.URL, imp: imp}
	e.admin = login(t, srv.URL, "admin@"+slug+".test", testPassword)
	e.dev = login(t, srv.URL, "dev@"+slug+".test", testPassword)
	e.other = login(t, srv.URL, "admin@"+other+".test", testPassword)
	return e
}

func filesStore(t *testing.T, dir string) *collab.FileStore {
	if dir == "" {
		return nil
	}
	fs, err := collab.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// uploadCSV carica un CSV per l'import.
func (e *importEnv) uploadCSV(u *user, source, name string, content []byte, want int) map[string]any {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("source", source)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.url+"/api/v1/imports/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := u.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("upload %s: stato %d, atteso %d: %s", name, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// wait aspetta che l'esecuzione esca da validating/running.
func (e *importEnv) wait(id string, timeout time.Duration) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r := e.admin.do("GET", "/api/v1/imports/"+id, nil, http.StatusOK)
		if st := r["status"]; st != "running" && st != "validating" {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("esecuzione %s ancora in corso dopo %s: %v", id, timeout, r["progress"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *importEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.InTenant(context.Background(), e.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(&n)
	}); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func element(u *user, key string) map[string]any {
	return u.do("GET", "/api/v1/elements/"+key, nil, http.StatusOK)["element"].(map[string]any)
}

func readFixture(t *testing.T, name string) []byte {
	b, err := os.ReadFile("testdata/import/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestImportJiraCSV(t *testing.T) {
	e := newImportEnv(t, "impj", 3, "")
	admin := e.admin
	data := readFixture(t, "jira.csv")

	// Solo l'amministratore importa.
	e.dev.do("GET", "/api/v1/imports", nil, http.StatusForbidden)
	e.uploadCSV(e.dev, "jira_csv", "jira.csv", data, http.StatusForbidden)

	// 1-2. Caricamento e analisi.
	run := e.uploadCSV(admin, "jira_csv", "jira.csv", data, http.StatusCreated)
	id := run["id"].(string)
	a := run["analysis"].(map[string]any)
	if a["issues"].(float64) != 8 || a["date_format"] != "jira" || a["key_prefix"] != "ACME" || a["comments"].(float64) != 2 || a["worklogs"].(float64) != 1 {
		t.Fatalf("analisi: %v", a)
	}
	names := func(list any) []string {
		var out []string
		for _, x := range list.([]any) {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	if ts := strings.Join(names(a["types"]), ","); !strings.Contains(ts, "Epic") || !strings.Contains(ts, "Sub-task") || !strings.Contains(ts, "Spike") {
		t.Fatalf("tipi: %s", ts)
	}
	if len(a["sprints"].([]any)) != 2 || len(a["users"].([]any)) != 4 {
		t.Fatalf("sprint o persone: %v %v", a["sprints"], a["users"])
	}
	// 3. Mappatura proposta: spazio nuovo ACME con la numerazione d'origine, persone per nome,
	// Story Points nel campo Punti, priorità nelle opzioni di Priorità.
	m := run["mapping"].(map[string]any)
	sp := m["space"].(map[string]any)
	if sp["mode"] != "new" || sp["key"] != "ACME" || sp["keep_numbers"] != true {
		t.Fatalf("spazio proposto: %v", sp)
	}
	users := m["users"].(map[string]any)
	if users["u-anna"].(map[string]any)["mode"] != "member" || users["u-paolo"].(map[string]any)["mode"] != "placeholder" {
		t.Fatalf("persone: %v", users)
	}
	fields := m["fields"].(map[string]any)
	if f := fields["Story Points"].(map[string]any); f["mode"] != "existing" || f["key"] != "punti" {
		t.Fatalf("Story Points: %v", f)
	}
	if f := fields["Team"].(map[string]any); f["mode"] != "new" || f["data_type"] != "enum" || f["key"] != "jira_team" {
		t.Fatalf("Team: %v", f)
	}
	if pv := m["priority"].(map[string]any)["values"].(map[string]any); pv["Highest"] != "Bloccante" || pv["High"] != "Alta" || pv["Medium"] != "Normale" {
		t.Fatalf("priorità: %v", pv)
	}
	if st := m["states"].(map[string]any)["story"].(map[string]any); st["Done"] != "Fatto" || st["In Review"] != "In revisione" || st["Bloccato"] != "" {
		t.Fatalf("stati: %v", st)
	}
	// Date degli sprint (nel CSV non ci sono) e la mappatura salvata.
	m["sprints"] = map[string]any{
		"ACME Sprint 1": map[string]any{"state": "closed", "start": "2026-03-02", "end": "2026-03-13"},
		"ACME Sprint 2": map[string]any{"state": "active", "start": "2026-03-16", "end": "2026-03-27"},
	}
	fields["Ambiente"] = map[string]any{"mode": "ignore"}
	res := admin.do("PUT", "/api/v1/imports/"+id+"/mapping", m, http.StatusOK)
	if len(res["problems"].([]any)) != 0 {
		t.Fatalf("problemi: %v", res["problems"])
	}
	// Una mappatura sbagliata si salva con i problemi e l'anteprima la rifiuta.
	bad := cloneMap(m)
	bad["space"] = map[string]any{"mode": "new", "key": "acme minuscolo", "team_name": ""}
	if res := admin.do("PUT", "/api/v1/imports/"+id+"/mapping", bad, http.StatusOK); len(res["problems"].([]any)) == 0 {
		t.Fatal("mappatura sbagliata senza problemi")
	}
	admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/imports/"+id+"/mapping", m, http.StatusOK)
	// Profilo dalla mappatura.
	prof := admin.do("POST", "/api/v1/import-profiles", map[string]any{"name": "Jira Acme", "run_id": id}, http.StatusCreated)

	// 4. Anteprima: conteggi ed errori riga per riga, niente salvato.
	before := e.count(`select count(*) from elements`)
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusUnprocessableEntity) // prima l'anteprima
	pv := admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)
	rep := pv["report"].(map[string]any)
	if pv["status"] != "preview" || rep["created"].(float64) != 7 || rep["errors"].(float64) != 1 || rep["trial"] != "ok" || rep["comments"].(float64) != 2 {
		t.Fatalf("anteprima: %v", pv)
	}
	if bt := rep["by_type"].(map[string]any); bt["epic"].(float64) != 1 || bt["story"].(float64) != 3 || bt["task"].(float64) != 2 || bt["bug"].(float64) != 1 {
		t.Fatalf("per tipo: %v", bt)
	}
	errs := pv["errors"].([]any)
	foundEmpty, foundTask := false, false
	for _, x := range errs {
		er := x.(map[string]any)
		if er["key"] == "ACME-7" && er["level"] == "error" && er["line"].(float64) == 11 {
			foundEmpty = true
		}
		if er["key"] == "ACME-6" && er["level"] == "warning" && er["field"] == "parent" {
			foundTask = true
		}
	}
	if !foundEmpty || !foundTask {
		t.Fatalf("errori riga per riga: %v", errs)
	}
	if msgs := fmt.Sprint(rep["messages"]); !strings.Contains(msgs, "Bloccato") {
		t.Fatalf("avviso dello stato senza fase: %v", msgs)
	}
	if after := e.count(`select count(*) from elements`); after != before {
		t.Fatalf("l'anteprima ha salvato %d elementi", after-before)
	}
	if n := e.count(`select count(*) from spaces where key = 'ACME'`) + e.count(`select count(*) from field_defs where key = 'origine'`); n != 0 {
		t.Fatal("l'anteprima ha creato lo spazio o il campo origine")
	}

	// 5. Esecuzione in background, a lotti di 3.
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	done := e.wait(id, 60*time.Second)
	if done["status"] != "done" {
		t.Fatalf("esecuzione: %v", done)
	}
	rep = done["report"].(map[string]any)
	if rep["created"].(float64) != 7 || rep["relations"].(float64) < 2 || rep["space_new"] != true || rep["placeholder"] != "Importato da Jira" {
		t.Fatalf("report: %v", rep)
	}
	// Numerazione d'origine, fase, date, campi, chiave d'origine.
	s2 := element(admin, "ACME-2")
	if s2["title"] != "Accesso con codice monouso" || s2["type"] != "story" || s2["state"] != "Fatto" || s2["resolved_at"] == nil {
		t.Fatalf("ACME-2: %v", s2)
	}
	if !instant(s2["created_at"], "2026-03-05T09:00:00Z") {
		t.Fatalf("data di creazione originale: %v", s2["created_at"])
	}
	if !instant(s2["updated_at"], "2026-04-18T13:20:00Z") {
		t.Fatalf("data di aggiornamento originale: %v", s2["updated_at"])
	}
	f2 := s2["fields"].(map[string]any)
	if f2["origine"] != "ACME-2" || f2["punti"].(float64) != 5 || f2["jira_team"] == nil || f2["priorita"] == nil {
		t.Fatalf("campi di ACME-2: %v", f2)
	}
	if s2["assignee_id"] == nil {
		t.Fatal("ACME-2 senza assegnatario (Anna Bianchi)")
	}
	if !strings.Contains(s2["body_text"].(string), "Allegati nell'origine") || !strings.Contains(s2["body_text"].(string), "bozza.png") {
		t.Fatalf("allegato come link: %v", s2["body_text"])
	}
	if e1 := element(admin, "ACME-1"); !strings.Contains(e1["body_text"].(string), "## Obiettivo") || !strings.Contains(e1["body_text"].(string), "**Ridurre**") ||
		e1["state"] != "In corso" || e1["due_at"] == nil {
		t.Fatalf("epic: %v", e1)
	}
	if e8 := element(admin, "ACME-8"); e8["state"] != "Da fare" || e8["type"] != "story" {
		t.Fatalf("stato senza fase → fase iniziale: %v", e8)
	}
	if e5 := element(admin, "ACME-5"); e5["state"] != "Annullato" || e5["type"] != "bug" {
		t.Fatalf("Won't Do: %v", e5)
	}
	if e6 := element(admin, "ACME-6"); e6["fields"].(map[string]any)["stima"].(float64) != 240 || e6["state"] != "Da fare" {
		t.Fatalf("stima e Selected for Development: %v", e6)
	}
	// ACME-7 (senza titolo) non c'è; il numero successivo libero viene dopo quelli d'origine.
	admin.do("GET", "/api/v1/elements/ACME-7", nil, http.StatusNotFound)
	// Gerarchia: epic > story > sottotask.
	l2 := admin.do("GET", "/api/v1/elements/ACME-2/links", nil, http.StatusOK)
	if l2["parent"].(map[string]any)["key"] != "ACME-1" || len(l2["children"].([]any)) != 1 || l2["children"].([]any)[0].(map[string]any)["key"] != "ACME-4" {
		t.Fatalf("gerarchia di ACME-2: %v", l2)
	}
	rels := fmt.Sprint(l2["relations"])
	if !strings.Contains(rels, "ACME-3") || !strings.Contains(rels, "blocca") {
		t.Fatalf("relazione blocca: %v", rels)
	}
	l5 := admin.do("GET", "/api/v1/elements/ACME-5/links", nil, http.StatusOK)
	if !strings.Contains(fmt.Sprint(l5["relations"]), "ACME-3") {
		t.Fatalf("relazione collegato a: %v", l5)
	}
	if n := e.count(`select count(*) from relations r join relation_types t on t.id = r.type_id where t.key = 'blocks'`); n != 1 {
		t.Fatalf("relazioni blocca (dai due versi, senza doppioni): %d", n)
	}
	// Commenti con autore e data originali; l'autore senza corrispondenza è «importato da».
	cm := admin.do("GET", "/api/v1/elements/ACME-2/comments", nil, http.StatusOK)["items"].([]any)
	if len(cm) != 2 {
		t.Fatalf("commenti: %v", cm)
	}
	c0, c1 := cm[0].(map[string]any), cm[1].(map[string]any)
	if c0["author_name"] != "Anna Bianchi" || !instant(c0["created_at"], "2026-03-10T08:30:00Z") {
		t.Fatalf("primo commento: %v", c0)
	}
	if c1["author_name"] != "Importato da Jira" || !strings.HasPrefix(c1["body"].(string), "_Importato da u-ext_") {
		t.Fatalf("commento di un autore sconosciuto: %v", c1)
	}
	// Ore registrate.
	if n := e.count(`select coalesce(sum(minutes), 0)::int from work_logs w join elements e on e.id = w.element_id where e.fields->>'origine' = 'ACME-2'`); n != 120 {
		t.Fatalf("ore: %d minuti", n)
	}
	// Sprint con le date, l'elemento pianificato nell'ultimo.
	bl := admin.do("GET", "/api/v1/work/spaces/ACME/backlog", nil, http.StatusOK)
	sprints := fmt.Sprint(bl["sprints"])
	if !strings.Contains(sprints, "ACME Sprint 2") || !strings.Contains(sprints, "2026-03-16") {
		t.Fatalf("sprint: %v", sprints)
	}
	if n := e.count(`select count(*) from relations r join relation_types t on t.id = r.type_id and t.key = 'planned_in'
		join elements s on s.id = r.target_id where s.title = 'ACME Sprint 2'`); n != 3 {
		t.Fatalf("elementi pianificati nello Sprint 2: %d", n)
	}
	if n := e.count(`select count(*) from elements e join element_types t on t.id = e.type_id and t.key = 'sprint'
		join options o on o.id = e.state_id where e.title = 'ACME Sprint 1' and o.category = 'done'`); n != 1 {
		t.Fatal("ACME Sprint 1 non chiuso")
	}
	// La chiave d'origine si cerca con la query.
	q := admin.do("GET", "/api/v1/elements?q="+urlq("origine: ACME-3"), nil, http.StatusOK)["items"].([]any)
	if len(q) != 1 || q[0].(map[string]any)["key"] != "ACME-3" {
		t.Fatalf("ricerca per origine: %v", q)
	}
	// La storia dice da dove viene l'elemento.
	hist := admin.do("GET", "/api/v1/elements/"+s2["id"].(string)+"/activity", nil, http.StatusOK)
	if !strings.Contains(fmt.Sprint(hist), "import") || !strings.Contains(fmt.Sprint(hist), "ACME-2") {
		t.Fatalf("storia: %v", hist)
	}
	// La board dello spazio importato funziona.
	admin.do("GET", "/api/v1/work/spaces/ACME/board?sprint=tutti", nil, http.StatusOK)

	// 6. Secondo import dello stesso export (un titolo cambiato) con il profilo: aggiorna, non duplica.
	data2 := bytes.Replace(data, []byte("Profilo del cliente"), []byte("Profilo del cliente (rivisto)"), 1)
	run2 := e.uploadCSV(admin, "jira_csv", "jira-2.csv", data2, http.StatusCreated)
	id2 := run2["id"].(string)
	applied := admin.do("POST", "/api/v1/imports/"+id2+"/profile/"+prof["id"].(string), nil, http.StatusOK)
	if len(applied["problems"].([]any)) != 0 {
		t.Fatalf("profilo applicato: %v", applied)
	}
	if sp := applied["run"].(map[string]any)["mapping"].(map[string]any)["space"].(map[string]any); sp["key"] != "ACME" {
		t.Fatalf("spazio dal profilo: %v", sp)
	}
	pv2 := admin.do("POST", "/api/v1/imports/"+id2+"/preview", nil, http.StatusOK)["report"].(map[string]any)
	if pv2["created"].(float64) != 0 || pv2["updated"].(float64) != 7 || pv2["comments"].(float64) != 0 || pv2["trial"] != "ok" {
		t.Fatalf("anteprima del secondo import: %v", pv2)
	}
	elems := e.count(`select count(*) from elements`)
	admin.do("POST", "/api/v1/imports/"+id2+"/start", nil, http.StatusAccepted)
	if r := e.wait(id2, 60*time.Second); r["status"] != "done" {
		t.Fatalf("secondo import: %v", r)
	}
	if n := e.count(`select count(*) from elements`); n != elems {
		t.Fatalf("il secondo import ha creato %d elementi", n-elems)
	}
	if e3 := element(admin, "ACME-3"); e3["title"] != "Profilo del cliente (rivisto)" {
		t.Fatalf("titolo aggiornato: %v", e3["title"])
	}
	if n := e.count(`select count(*) from comments c join elements e on e.id = c.element_id where e.fields->>'origine' = 'ACME-2'`); n != 2 {
		t.Fatalf("commenti duplicati: %d", n)
	}
	if n := e.count(`select count(*) from work_logs w join elements e on e.id = w.element_id where e.fields->>'origine' = 'ACME-2'`); n != 1 {
		t.Fatalf("ore duplicate: %d", n)
	}
	// Annullare il primo import: tutti gli elementi li ha riscritti il secondo, quindi restano.
	admin.do("POST", "/api/v1/imports/"+id+"/rollback", nil, http.StatusAccepted)
	rb := e.wait(id, 60*time.Second)
	if rb["status"] != "rolled_back" {
		t.Fatalf("annullamento: %v", rb)
	}
	rr := rb["report"].(map[string]any)["rollback"].(map[string]any)
	if rr["deleted"].(float64) != 0 || rr["kept_updated"].(float64) != 9 || rr["comments"].(float64) != 0 || rr["relations"].(float64) != 0 {
		// 9 = 7 elementi e 2 sprint, tutti riscritti dal secondo import: restano, con i loro commenti.
		t.Fatalf("report dell'annullamento: %v", rr)
	}

	// 7. Isolamento: l'altro tenant non vede esecuzioni, profili né elementi importati.
	e.other.do("GET", "/api/v1/imports/"+id, nil, http.StatusNotFound)
	if l := e.other.do("GET", "/api/v1/imports", nil, http.StatusOK)["items"].([]any); len(l) != 0 {
		t.Fatalf("esecuzioni visibili dall'altro tenant: %v", l)
	}
	if l := e.other.do("GET", "/api/v1/import-profiles", nil, http.StatusOK)["items"].([]any); len(l) != 0 {
		t.Fatalf("profili visibili dall'altro tenant: %v", l)
	}
	e.other.do("PUT", "/api/v1/imports/"+id2+"/mapping", m, http.StatusNotFound)
	e.other.do("GET", "/api/v1/elements/ACME-2", nil, http.StatusNotFound)
}

// instant confronta un istante del JSON (in qualunque fuso) con uno atteso in UTC.
func instant(v any, want string) bool {
	s, _ := v.(string)
	a, err1 := time.Parse(time.RFC3339Nano, s)
	b, err2 := time.Parse(time.RFC3339, want)
	return err1 == nil && err2 == nil && a.Equal(b)
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func TestImportYouTrackCSVAndRollback(t *testing.T) {
	e := newImportEnv(t, "impy", 2, "")
	admin := e.admin
	run := e.uploadCSV(admin, "youtrack_csv", "youtrack.csv", readFixture(t, "youtrack.csv"), http.StatusCreated)
	id := run["id"].(string)
	m := run["mapping"].(map[string]any)
	if m["date_format"] != "iso" {
		t.Fatalf("formato delle date: %v", m["date_format"])
	}
	users := m["users"].(map[string]any)
	if users["anna.bianchi"].(map[string]any)["mode"] != "member" || users["marco.neri"].(map[string]any)["mode"] != "member" {
		t.Fatalf("persone per nome utente: %v", users)
	}
	// Nessuna corrispondenza per paolo.verdi: resta senza assegnatario.
	users["paolo.verdi"] = map[string]any{"mode": "none"}
	if st := m["states"].(map[string]any)["bug"].(map[string]any); st["Submitted"] != "Da fare" {
		t.Fatalf("stati del bug: %v", st)
	}
	if f := m["fields"].(map[string]any)["Subsystem"].(map[string]any); f["mode"] != "new" || f["key"] != "yt_subsystem" {
		t.Fatalf("campo Subsystem: %v", f)
	}
	if lk := m["links"].(map[string]any); lk["Depend"] != "blocks" || lk["Relates"] != "relates" {
		t.Fatalf("collegamenti: %v", lk)
	}
	m["space"] = map[string]any{"mode": "new", "key": "DEMO", "team_name": "Team Demo", "keep_numbers": true}
	admin.do("PUT", "/api/v1/imports/"+id+"/mapping", m, http.StatusOK)
	pv := admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)["report"].(map[string]any)
	if pv["created"].(float64) != 5 || pv["errors"].(float64) != 0 || pv["trial"] != "ok" {
		t.Fatalf("anteprima: %v", pv)
	}
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	if r := e.wait(id, 60*time.Second); r["status"] != "done" {
		t.Fatalf("esecuzione: %v", r)
	}
	d2 := element(admin, "DEMO-2")
	l := admin.do("GET", "/api/v1/elements/DEMO-2/links", nil, http.StatusOK)
	if d2["state"] != "Fatto" || d2["assignee_id"] != nil || l["parent"].(map[string]any)["key"] != "DEMO-1" {
		t.Fatalf("DEMO-2: %v %v", d2, l)
	}
	// «DEMO-3 depends on DEMO-2» = DEMO-2 blocca DEMO-3.
	if n := e.count(`select count(*) from relations r join relation_types t on t.id = r.type_id and t.key = 'blocks'
		join elements s on s.id = r.source_id join elements x on x.id = r.target_id
		where s.fields->>'origine' = 'DEMO-2' and x.fields->>'origine' = 'DEMO-3'`); n != 1 {
		t.Fatal("DEMO-2 non blocca DEMO-3")
	}
	if d1 := element(admin, "DEMO-1"); d1["fields"].(map[string]any)["stima"].(float64) != 720 || len(d1["fields"].(map[string]any)["etichette"].([]any)) != 2 {
		t.Fatalf("stima ed etichette: %v", d1["fields"])
	}
	// Una modifica fatta in Regia dopo l'import: quell'elemento resta all'annullamento.
	d4 := element(admin, "DEMO-4")
	admin.do("PATCH", "/api/v1/elements/"+d4["id"].(string), map[string]any{"version": d4["version"], "title": "Piattaforma dati (rivista)"}, http.StatusOK)
	admin.do("POST", "/api/v1/imports/"+id+"/rollback", nil, http.StatusAccepted)
	rb := e.wait(id, 60*time.Second)
	rr := rb["report"].(map[string]any)["rollback"].(map[string]any)
	if rb["status"] != "rolled_back" || rr["deleted"].(float64) != 5 || rr["kept_modified"].(float64) != 1 || rr["space_deleted"] == true {
		// 5 = 4 elementi + lo sprint «Sprint 7».
		t.Fatalf("annullamento: %v", rr)
	}
	admin.do("GET", "/api/v1/elements/DEMO-1", nil, http.StatusNotFound)
	if e4 := element(admin, "DEMO-4"); e4["title"] != "Piattaforma dati (rivista)" {
		t.Fatalf("elemento modificato: %v", e4)
	}
	// Campi nuovi senza più valori: eliminati; Etichette ha ancora i valori di DEMO-4? No: DEMO-4 non ne ha.
	if n := e.count(`select count(*) from field_defs where key = 'yt_subsystem'`); n != 0 {
		t.Fatal("campo creato dall'import non eliminato")
	}
	if n := e.count(`select count(*) from import_origins where kind = 'element'`); n != 1 {
		t.Fatalf("chiavi d'origine rimaste: %d", n)
	}
	// Un'esecuzione annullata non si annulla di nuovo.
	admin.do("POST", "/api/v1/imports/"+id+"/rollback", nil, http.StatusUnprocessableEntity)
}

func TestImportGenericCSV(t *testing.T) {
	e := newImportEnv(t, "impg", 1000, "")
	admin := e.admin
	run := e.uploadCSV(admin, "csv", "attivita.csv", readFixture(t, "generico.csv"), http.StatusCreated)
	id := run["id"].(string)
	a := run["analysis"].(map[string]any)
	cols := a["columns"].([]any)
	if cols[0] != "key" || cols[1] != "summary" || cols[3] != "status" || cols[9] != "custom:Note interne" {
		t.Fatalf("ruoli delle colonne: %v", cols)
	}
	if a["date_format"] != "dmy" {
		t.Fatalf("date: %v", a["date_format"])
	}
	m := run["mapping"].(map[string]any)
	// Le note interne non si importano: ruolo cambiato, l'analisi si rifà.
	cols[9] = "ignore"
	m["columns"] = cols
	m["types"].(map[string]any)["Attività"] = "task"
	m["types"].(map[string]any)["Storia"] = "story"
	m["space"] = map[string]any{"mode": "new", "key": "OPS", "team_name": "Operazioni", "keep_numbers": true}
	if f := m["fields"].(map[string]any)["Costo stimato"].(map[string]any); f["data_type"] != "decimal" {
		t.Fatalf("tipo dedotto: %v", f)
	}
	res := admin.do("PUT", "/api/v1/imports/"+id+"/mapping", m, http.StatusOK)
	an := res["run"].(map[string]any)["analysis"].(map[string]any)
	for _, f := range an["fields"].([]any) {
		if f.(map[string]any)["name"] == "Note interne" {
			t.Fatal("l'analisi non si è rifatta dopo il cambio dei ruoli")
		}
	}
	m = res["run"].(map[string]any)["mapping"].(map[string]any)
	if _, ok := m["fields"].(map[string]any)["Note interne"]; ok {
		delete(m["fields"].(map[string]any), "Note interne")
	}
	admin.do("PUT", "/api/v1/imports/"+id+"/mapping", m, http.StatusOK)
	admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	if r := e.wait(id, 60*time.Second); r["status"] != "done" {
		t.Fatalf("esecuzione: %v", r)
	}
	o10 := element(admin, "OPS-10")
	if o10["type"] != "task" || o10["fields"].(map[string]any)["csv_costo_stimato"].(float64) != 1200.5 || o10["due_at"] == nil {
		t.Fatalf("OPS-10: %v", o10)
	}
	if o12 := element(admin, "OPS-12"); o12["type"] != "bug" || o12["state"] != "Fatto" {
		t.Fatalf("OPS-12: %v", o12)
	}
}

// --- API REST con server finti -----------------------------------------------------------

func TestImportJiraAPI(t *testing.T) {
	var fake *httptest.Server
	attachmentHits := 0
	fake = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot@acme.example:segreto-jira"))
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/2/myself":
			fmt.Fprint(w, `{"accountId":"bot","displayName":"Bot"}`)
		case r.URL.Path == "/rest/api/2/search/jql":
			if !strings.Contains(r.URL.Query().Get("jql"), `project = "API"`) {
				t.Errorf("JQL: %s", r.URL.Query().Get("jql"))
			}
			if r.URL.Query().Get("nextPageToken") == "" {
				fmt.Fprintf(w, `{"names":{"customfield_10020":"Sprint","customfield_10016":"Story Points","customfield_10050":"Canale"},
				"nextPageToken":"p2","isLast":false,"issues":[
				{"id":"1","key":"API-1","fields":{"summary":"Epic delle API","issuetype":{"name":"Epic"},"status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},
				 "project":{"key":"API"},"created":"2026-02-01T09:00:00.000+0100","updated":"2026-02-02T09:00:00.000+0100",
				 "assignee":{"accountId":"a1","displayName":"Anna Bianchi","emailAddress":"anna.bianchi@%s.test"},
				 "description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Documento ADF"}]}]},
				 "comment":{"total":3,"comments":[{"id":"c1","body":"troncato","created":"2026-02-01T10:00:00.000+0100"}]},
				 "worklog":{"total":1,"worklogs":[{"id":"w1","author":{"accountId":"a1","displayName":"Anna Bianchi"},"started":"2026-02-03T09:00:00.000+0100","timeSpentSeconds":5400}]},
				 "customfield_10050":{"value":"Web"}}}]}`, slugOf(r))
			} else {
				fmt.Fprintf(w, `{"names":{"customfield_10020":"Sprint","customfield_10016":"Story Points"},"isLast":true,"issues":[
				{"id":"2","key":"API-2","fields":{"summary":"Endpoint degli ordini","issuetype":{"name":"Story"},"status":{"name":"Done","statusCategory":{"key":"done"}},
				 "project":{"key":"API"},"parent":{"key":"API-1"},"created":"2026-02-05T09:00:00.000+0100","updated":"2026-02-06T09:00:00.000+0100","resolutiondate":"2026-02-06T09:00:00.000+0100",
				 "priority":{"name":"High"},"labels":["backend"],"customfield_10016":8,
				 "customfield_10020":[{"id":5,"name":"API Sprint 3","state":"closed","startDate":"2026-02-02T09:00:00.000Z","endDate":"2026-02-13T17:00:00.000Z"}],
				 "issuelinks":[{"type":{"name":"Duplicate"},"outwardIssue":{"key":"API-1"}}],
				 "attachment":[{"id":"9","filename":"schema.txt","content":"%s/secure/attachment/9/schema.txt","created":"2026-02-05T10:00:00.000+0100","size":12},
				               {"id":"10","filename":"altro.txt","content":"https://altro.example/file.txt","size":5}]}}]}`, fake.URL)
			}
		case r.URL.Path == "/rest/api/2/issue/API-1/comment":
			fmt.Fprint(w, `{"total":3,"comments":[
				{"id":"c1","author":{"accountId":"a1","displayName":"Anna Bianchi"},"body":"Primo","created":"2026-02-01T10:00:00.000+0100"},
				{"id":"c2","author":{"accountId":"zz","displayName":"Ospite"},"body":"Secondo","created":"2026-02-01T11:00:00.000+0100"},
				{"id":"c3","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Terzo in ADF"}]}]},"created":"2026-02-01T12:00:00.000+0100"}]}`)
		case r.URL.Path == "/secure/attachment/9/schema.txt":
			attachmentHits++
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "id;nome;data")
		default:
			t.Errorf("percorso inatteso: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	e := newImportEnv(t, "impa", 1000, t.TempDir())
	jiraSlug = e.slug
	admin := e.admin
	conn := map[string]any{"source": "jira_api", "base_url": fake.URL, "deployment": "cloud", "email": "bot@acme.example", "token": "sbagliato", "project": "API"}
	admin.do("POST", "/api/v1/imports/connect", conn, http.StatusUnprocessableEntity) // credenziali rifiutate
	conn["token"] = "segreto-jira"
	run := admin.do("POST", "/api/v1/imports/connect", conn, http.StatusCreated)
	id := run["id"].(string)
	run = e.wait(id, 30*time.Second)
	if run["status"] != "pending" {
		t.Fatalf("lettura dall'API: %v", run)
	}
	// Il token non è salvato nel database.
	if n := e.count(`select count(*) from import_runs where id = $1 and (connection::text like '%segreto%' or analysis::text like '%segreto%')`, id); n != 0 {
		t.Fatal("token salvato nel database")
	}
	a := run["analysis"].(map[string]any)
	if a["issues"].(float64) != 2 || a["comments"].(float64) != 3 || a["attachments"].(float64) != 2 {
		t.Fatalf("analisi dell'API: %v", a)
	}
	m := run["mapping"].(map[string]any)
	if m["users"].(map[string]any)["a1"].(map[string]any)["mode"] != "member" {
		t.Fatalf("persona per email: %v", m["users"])
	}
	if sp := m["sprints"].(map[string]any)["API Sprint 3"].(map[string]any); sp["state"] != "closed" || sp["start"] != "2026-02-02" {
		t.Fatalf("sprint dall'API con le date: %v", sp)
	}
	admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	done := e.wait(id, 60*time.Second)
	if done["status"] != "done" {
		t.Fatalf("esecuzione: %v", done)
	}
	if rep := done["report"].(map[string]any); rep["attachments"].(float64) != 1 {
		t.Fatalf("allegati scaricati: %v (messaggi %v)", rep["attachments"], rep["messages"])
	}
	if attachmentHits != 1 {
		t.Fatalf("download dell'allegato: %d (quello su un altro host non si scarica)", attachmentHits)
	}
	e1 := element(admin, "API-1")
	if !strings.Contains(e1["body_text"].(string), "Documento ADF") {
		t.Fatalf("descrizione ADF: %v", e1["body_text"])
	}
	cm := admin.do("GET", "/api/v1/elements/API-1/comments", nil, http.StatusOK)["items"].([]any)
	if len(cm) != 3 || cm[2].(map[string]any)["body"] != "_Importato da sconosciuto_\n\nTerzo in ADF" {
		t.Fatalf("commenti letti anche dalle pagine successive: %v", cm)
	}
	atts := admin.do("GET", "/api/v1/elements/API-2/attachments", nil, http.StatusOK)["items"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["name"] != "schema.txt" {
		t.Fatalf("allegati: %v", atts)
	}
	if e2 := element(admin, "API-2"); e2["fields"].(map[string]any)["punti"].(float64) != 8 {
		t.Fatalf("Story Points dall'API: %v", e2["fields"])
	}
	if n := e.count(`select count(*) from work_logs where minutes = 90`); n != 1 {
		t.Fatal("ore dall'API")
	}
}

var jiraSlug string

func slugOf(*http.Request) string { return jiraSlug }

func TestImportYouTrackAPI(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer perm:prova" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/users/me":
			fmt.Fprint(w, `{"login":"bot"}`)
		case "/api/issues":
			if r.URL.Query().Get("$skip") != "0" {
				fmt.Fprint(w, `[]`)
				return
			}
			if !strings.Contains(r.URL.Query().Get("query"), "project: {YT}") {
				t.Errorf("query: %s", r.URL.Query().Get("query"))
			}
			fmt.Fprint(w, `[
			 {"idReadable":"YT-1","summary":"Catalogo","created":1772352000000,"updated":1772438400000,"project":{"shortName":"YT"},
			  "reporter":{"login":"marco.neri","fullName":"Marco Neri"},
			  "customFields":[{"name":"Type","value":{"name":"Epic"}},{"name":"State","value":{"name":"Open"}},{"name":"Priority","value":{"name":"Major"}}]},
			 {"idReadable":"YT-2","summary":"Filtri","created":1772524800000,"updated":1772611200000,"resolved":1772611200000,"project":{"shortName":"YT"},
			  "customFields":[{"name":"Type","value":{"name":"Feature"}},{"name":"State","value":{"name":"Fixed"}},
			    {"name":"Assignee","value":{"login":"anna.bianchi","fullName":"Anna Bianchi"}},{"name":"Estimation","value":{"minutes":480}},
			    {"name":"Componente","$type":"MultiEnumIssueCustomField","value":[{"name":"UI"},{"name":"API"}]}],
			  "tags":[{"name":"ux"}],
			  "links":[{"direction":"INWARD","linkType":{"name":"Subtask"},"issues":[{"idReadable":"YT-1"}]}],
			  "comments":[{"id":"1-1","text":"Fatto **bene**","created":1772611200000,"author":{"login":"anna.bianchi","fullName":"Anna Bianchi"}}]}]`)
		case "/api/workItems":
			fmt.Fprint(w, `[{"id":"w-1","date":1772611200000,"text":"sviluppo","author":{"login":"anna.bianchi"},"duration":{"minutes":150},"issue":{"idReadable":"YT-2"}}]`)
		case "/api/agiles":
			fmt.Fprint(w, `[{"name":"Board","projects":[{"shortName":"YT"}],"sprints":[{"name":"YT Sprint 1","start":1772352000000,"finish":1773561600000,"archived":true,"issues":[{"idReadable":"YT-2"}]}]}]`)
		default:
			t.Errorf("percorso inatteso: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	e := newImportEnv(t, "impt", 1000, "")
	admin := e.admin
	run := admin.do("POST", "/api/v1/imports/connect", map[string]any{"source": "youtrack_api", "base_url": fake.URL, "token": "perm:prova", "project": "YT"}, http.StatusCreated)
	id := run["id"].(string)
	run = e.wait(id, 30*time.Second)
	if run["status"] != "pending" {
		t.Fatalf("lettura: %v", run)
	}
	m := run["mapping"].(map[string]any)
	if sp := m["sprints"].(map[string]any)["YT Sprint 1"].(map[string]any); sp["state"] != "closed" || sp["start"] != "2026-03-01" {
		t.Fatalf("sprint: %v", sp)
	}
	if f := m["fields"].(map[string]any)["Componente"].(map[string]any); f["multi"] != true || f["data_type"] != "enum" {
		t.Fatalf("campo multiplo: %v", f)
	}
	admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	if r := e.wait(id, 60*time.Second); r["status"] != "done" {
		t.Fatalf("esecuzione: %v", r)
	}
	y2 := element(admin, "YT-2")
	if y2["state"] != "Fatto" || y2["fields"].(map[string]any)["stima"].(float64) != 480 || len(y2["fields"].(map[string]any)["yt_componente"].([]any)) != 2 {
		t.Fatalf("YT-2: %v", y2)
	}
	if l := admin.do("GET", "/api/v1/elements/YT-2/links", nil, http.StatusOK); l["parent"].(map[string]any)["key"] != "YT-1" {
		t.Fatalf("gerarchia da Subtask: %v", l)
	}
	if n := e.count(`select count(*) from work_logs where minutes = 150`); n != 1 {
		t.Fatal("ore dai work item")
	}
}

// --- Volume --------------------------------------------------------------------------------

// 5.000 righe (con commenti, ore, gerarchia e sprint) in un import: si misura il tempo.
func TestImportVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("test di volume")
	}
	// REGIA_IMPORT_VOLUME cambia il numero di righe (es. 50000 per la misura completa).
	n := 5000
	if v, err := strconv.Atoi(os.Getenv("REGIA_IMPORT_VOLUME")); err == nil && v >= 100 {
		n = v
	}
	var b strings.Builder
	b.WriteString("Summary,Issue key,Issue id,Parent,Issue Type,Status,Priority,Assignee,Assignee Id,Reporter,Reporter Id,Created,Updated,Labels,Sprint,Custom field (Story Points),Custom field (Componente),Comment,Log Work,Outward issue link (Blocks),Description\n")
	for i := 1; i <= n; i++ {
		typ, parent := "Story", fmt.Sprint(10000+((i-1)/50)*50+1)
		if (i-1)%50 == 0 {
			typ, parent = "Epic", ""
		}
		status := []string{"To Do", "In Progress", "In Review", "Done"}[i%4]
		link := ""
		if i%7 == 0 && i > 1 {
			link = fmt.Sprintf("VOL-%d", i-1)
		}
		fmt.Fprintf(&b, "Elemento numero %d del test di volume,VOL-%d,%d,%s,%s,%s,%s,Anna Bianchi,u-anna,Marco Neri,u-marco,%02d/Mar/26 9:%02d AM,%02d/Apr/26 3:00 PM,vol etichetta%d,VOL Sprint %d,%d,Comp %d,"+
			"12/Apr/26 10:00 AM;u-anna;Commento sull'elemento %d,Lavoro;13/Apr/26 9:00 AM;u-anna;3600,%s,\"Descrizione dell'elemento %d con un po' di testo per la ricerca.\"\n",
			i, i, 10000+i, parent, typ, status, []string{"High", "Medium", "Low"}[i%3], 1+i%28, i%60, 1+i%28, i%10, 1+i%12, []int{1, 2, 3, 5, 8}[i%5], i%5, i, link, i)
	}
	e := newImportEnv(t, "impv", 1000, "")
	admin := e.admin
	start := time.Now()
	run := e.uploadCSV(admin, "jira_csv", "volume.csv", []byte(b.String()), http.StatusCreated)
	uploaded := time.Since(start)
	id := run["id"].(string)
	t0 := time.Now()
	pv := admin.do("POST", "/api/v1/imports/"+id+"/preview", nil, http.StatusOK)["report"].(map[string]any)
	previewed := time.Since(t0)
	if int(pv["created"].(float64)) != n || pv["trial"] != "ok" {
		t.Fatalf("anteprima: %v", pv)
	}
	t1 := time.Now()
	admin.do("POST", "/api/v1/imports/"+id+"/start", nil, http.StatusAccepted)
	// Mentre l'import gira il server risponde.
	time.Sleep(200 * time.Millisecond)
	q0 := time.Now()
	admin.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)
	queryDuring := time.Since(q0)
	done := e.wait(id, 5*time.Minute)
	executed := time.Since(t1)
	if done["status"] != "done" {
		t.Fatalf("esecuzione: %v", done)
	}
	rep := done["report"].(map[string]any)
	t.Logf("volume %d righe: caricamento e analisi %s, anteprima %s, esecuzione %s (%.0f elementi/s), richiesta durante l'import %s; commenti %v, ore %v, relazioni %v",
		n, uploaded.Round(time.Millisecond), previewed.Round(time.Millisecond), executed.Round(time.Millisecond), float64(n)/executed.Seconds(),
		queryDuring.Round(time.Millisecond), rep["comments"], rep["worklogs"], rep["relations"])
	if c := e.count(`select count(*) from elements e join spaces s on s.id = e.space_id where s.key = 'VOL'`); c != n+12 {
		t.Fatalf("elementi nello spazio: %d", c)
	}
	if c := e.count(`select count(*) from elements e join spaces s on s.id = e.space_id where s.key = 'VOL' and e.parent_id is not null`); c != n-n/50 {
		t.Fatalf("elementi con genitore: %d", c)
	}
	if executed > time.Duration(max(1, n/5000))*2*time.Minute {
		t.Fatalf("troppo lento: %s per %d righe", executed, n)
	}
	if queryDuring > 5*time.Second {
		t.Fatalf("il server non risponde durante l'import: %s", queryDuring)
	}
}
