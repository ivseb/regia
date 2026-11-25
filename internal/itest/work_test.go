package itest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Work management di base e assegnazione ai team (piano, passi 10 e 18): spazi dei team e
// tipi, epic creato all'accettazione dell'impatto e avanzamento che risale, board con WIP e
// passaggi bloccati dai gate, ordinamento manuale, sprint con spostamento degli incompiuti,
// burndown, ore che diventano costi della richiesta, isolamento tra tenant.
func TestWorkManagement(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()
	slug := strings.ReplaceAll("wrk-"+time.Now().Format("150405.000"), ".", "")
	prov := []tenant.Provisioner{demand.Provision, architecture.Provision, finance.Provision, reviews.Provision, work.Provision}
	ten, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: slug, Name: "Lavoro", AdminEmail: "admin@" + slug + ".test",
		AdminPassword: testPassword, Provision: prov})
	if err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll("wro-"+time.Now().Format("150405.000"), ".", "")
	if _, err := tenant.Create(ctx, d, tenant.CreateInput{Slug: other, Name: "Altro", AdminEmail: "admin@" + other + ".test",
		AdminPassword: testPassword, Provision: prov}); err != nil {
		t.Fatal(err)
	}
	// Il provisioning è idempotente (anche la modifica al processo degli impatti).
	if err := tenant.Provision(ctx, d, slug, prov); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ email, roles string }{{"lead", "viewer"}, {"dev", "editor"}, {"fin", "editor,finance"}} {
		if _, err := tenant.AddMember(ctx, d, tenant.MemberInput{TenantSlug: slug, Email: m.email + "@" + slug + ".test", Name: strings.ToUpper(m.email[:1]) + m.email[1:],
			Password: testPassword, Roles: strings.Split(m.roles, ",")}); err != nil {
			t.Fatal(err)
		}
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
	defer srv.Close()
	defer apiSrv.Close()
	admin := login(t, srv.URL, "admin@"+slug+".test", testPassword)
	lead := login(t, srv.URL, "lead@"+slug+".test", testPassword)
	dev := login(t, srv.URL, "dev@"+slug+".test", testPassword)
	fin := login(t, srv.URL, "fin@"+slug+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+other+".test", testPassword)

	// --- Spazi dei team e tipi -------------------------------------------------------------
	// Un team nuovo riceve subito lo spazio (chiave dal nome); uno importato lo riceve dalla scheda Team.
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Web", "lead_id": lead.member}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications/import", map[string]any{"csv": "codice,nome,team\nWEB-AR,Frontend area riservata,Web\nAPI,Backend API,Integrazioni\n"}, http.StatusOK)
	spaces := admin.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)
	if items := spaces["items"].([]any); len(items) != 1 || items[0].(map[string]any)["key"] != "WEB" {
		t.Fatalf("spazi: %v", spaces)
	}
	without := spaces["teams_without_space"].([]any)
	if len(without) != 1 || without[0].(map[string]any)["proposed_key"] != "INT" {
		t.Fatalf("team senza spazio: %v", without)
	}
	dev.do("POST", "/api/v1/work/spaces", map[string]any{"team_id": without[0].(map[string]any)["id"]}, http.StatusForbidden)
	if sp := admin.do("POST", "/api/v1/work/spaces", map[string]any{"team_id": without[0].(map[string]any)["id"]}, http.StatusCreated); sp["key"] != "INT" {
		t.Fatalf("spazio Integrazioni: %v", sp)
	}
	for typ, phase := range map[string]string{"story": "In revisione", "task": "In corso", "bug": "Da verificare", "epic": "Fatto", "sprint": "Attivo"} {
		ti := admin.do("GET", "/api/v1/types/"+typ, nil, http.StatusOK)
		found := false
		for _, s := range ti["states"].([]any) {
			found = found || s.(map[string]any)["name"] == phase
		}
		if !found || ti["process"] == nil {
			t.Fatalf("tipo %s senza la fase %s o senza processo: %v", typ, phase, ti)
		}
	}
	if (work.ProposeKey("Customer Experience") != "CE") || work.ProposeKey("Città") != "CIT" || work.ProposeKey("Dati e analisi") != "DA" {
		t.Fatal("chiavi proposte")
	}

	// --- Assegnazione al team: l'epic nasce all'accettazione dell'impatto ----------------------
	apps := map[string]string{}
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		m := a.(map[string]any)
		apps[m["code"].(string)] = m["id"].(string)
	}
	req := admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Portale clienti B2B"}, http.StatusCreated)
	reqKey := req["key"].(string)
	imp := admin.do("POST", "/api/v1/demand/requests/"+reqKey+"/impacts", map[string]any{"application_id": apps["WEB-AR"], "estimate_effort_days": 20,
		"technical_note": "Nuove pagine dell'area riservata"}, http.StatusCreated)
	impKey := imp["key"].(string)
	move := func(u *user, ref, to string, status int) map[string]any {
		return u.do("POST", "/api/v1/elements/"+ref+"/transitions", map[string]any{"to": to}, status)
	}
	move(admin, imp["id"].(string), "In analisi", http.StatusOK)
	move(admin, imp["id"].(string), "Stimato", http.StatusOK)
	if del := admin.do("GET", "/api/v1/work/elements/"+impKey+"/delivery", nil, http.StatusOK); len(del["epics"].([]any)) != 0 {
		t.Fatalf("epic prima dell'accettazione: %v", del)
	}
	move(lead, imp["id"].(string), "Accettato", http.StatusOK)
	del := lead.do("GET", "/api/v1/work/elements/"+impKey+"/delivery", nil, http.StatusOK)
	epics := del["epics"].([]any)
	if len(epics) != 1 {
		t.Fatalf("epic dopo l'accettazione: %v", del)
	}
	epic := epics[0].(map[string]any)
	epicKey := epic["key"].(string)
	if !strings.HasPrefix(epicKey, "WEB-") || epic["impact_key"] != impKey || epic["team"] != "Web" {
		t.Fatalf("epic: %v", epic)
	}
	epicEl := admin.do("GET", "/api/v1/elements/"+epicKey, nil, http.StatusOK)["element"].(map[string]any)
	if epicEl["parent_id"] != imp["id"] || epicEl["assignee_id"] != lead.member {
		t.Fatalf("epic: genitore o assegnatario: %v", epicEl)
	}
	// L'azione è idempotente: la creazione manuale restituisce lo stesso epic.
	if again := lead.do("POST", "/api/v1/work/impacts/"+impKey+"/epic", nil, http.StatusCreated); again["key"] != epicKey {
		t.Fatalf("epic ricreato: %v", again)
	}
	err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var status, team string
		if err := tx.QueryRow(ctx, `select a.status, t.name from assignments a join teams t on t.id = a.team_id
			where a.element_id = $1 and a.delivery_element_id = $2`, imp["id"], epicEl["id"]).Scan(&status, &team); err != nil {
			return err
		}
		if status != "accepted" || team != "Web" {
			return fmt.Errorf("assegnazione %s %s", status, team)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// --- Story, task e bug nello spazio del team --------------------------------------------
	item := func(u *user, body map[string]any, status int) map[string]any {
		return u.do("POST", "/api/v1/work/spaces/WEB/items", body, status)
	}
	s1 := item(dev, map[string]any{"type": "story", "title": "Login con SPID", "parent": epicKey, "fields": map[string]any{"punti": 3, "priorita": "Alta"}}, http.StatusCreated)
	s2 := item(dev, map[string]any{"type": "story", "title": "Elenco contratti", "parent": epicKey, "fields": map[string]any{"punti": 5}}, http.StatusCreated)
	s3 := item(dev, map[string]any{"type": "story", "title": "Download fatture", "parent": epicKey, "fields": map[string]any{"punti": 2}}, http.StatusCreated)
	t1 := item(dev, map[string]any{"type": "task", "title": "Integrazione con l'IdP", "parent": s1["key"]}, http.StatusCreated)
	b1 := item(dev, map[string]any{"type": "bug", "title": "Errore 500 al logout"}, http.StatusCreated)
	item(dev, map[string]any{"type": "task", "title": "Task sotto un epic", "parent": epicKey}, http.StatusUnprocessableEntity)
	item(adminB, map[string]any{"type": "story", "title": "Abusiva"}, http.StatusNotFound)

	// --- Board: colonne dalle fasi, WIP, passaggi bloccati dai gate --------------------------
	board := func(u *user, query string) (map[string]any, []map[string]any) {
		b := u.do("GET", "/api/v1/work/spaces/WEB/board"+query, nil, http.StatusOK)
		var cards []map[string]any
		for _, c := range b["cards"].([]any) {
			cards = append(cards, c.(map[string]any))
		}
		return b, cards
	}
	b, _ := board(dev, "")
	var cols []string
	for _, c := range b["columns"].([]any) {
		cols = append(cols, c.(map[string]any)["name"].(string))
	}
	if strings.Join(cols, "|") != "Da fare|In corso|In revisione|Da verificare|Fatto" {
		t.Fatalf("colonne di default: %v", cols)
	}
	colIdx := func(name string) int { return slices.Index(cols, name) }
	settings := b["settings"].(map[string]any)
	cs := settings["columns"].([]any)
	cs[colIdx("In corso")].(map[string]any)["wip"] = 1
	settings["columns"] = cs
	adminB.do("PUT", "/api/v1/work/spaces/WEB/board/settings", settings, http.StatusNotFound)
	dev.do("PUT", "/api/v1/work/spaces/WEB/board/settings", settings, http.StatusOK)

	boardMove := func(u *user, body map[string]any, status int) map[string]any {
		return u.do("POST", "/api/v1/work/spaces/WEB/board/move", body, status)
	}
	// Senza assegnatario la story non parte (gate del processo): nessun cambiamento.
	r := boardMove(dev, map[string]any{"element": s1["id"], "column": colIdx("In corso")}, http.StatusUnprocessableEntity)
	if !strings.Contains(r["detail"].(string), "assegnatario") {
		t.Fatalf("gate dell'assegnatario: %v", r)
	}
	// Spostando nella swimlane della persona si assegna, e il passaggio riesce.
	lane := dev.member
	boardMove(dev, map[string]any{"element": s1["id"], "lane": lane, "swimlane": "assignee"}, http.StatusOK)
	boardMove(dev, map[string]any{"element": s1["id"], "column": colIdx("In corso")}, http.StatusOK)
	dev.do("PUT", "/api/v1/work/elements/"+s2["key"].(string)+"/assignee", map[string]any{"member_id": dev.member}, http.StatusOK)
	boardMove(dev, map[string]any{"element": s2["id"], "column": colIdx("In corso")}, http.StatusOK)
	b, _ = board(dev, "")
	inCorso := b["columns"].([]any)[colIdx("In corso")].(map[string]any)
	if inCorso["count"].(float64) != 2 || inCorso["over"] != true {
		t.Fatalf("limite WIP: %v", inCorso)
	}
	// Una story con task aperti non si chiude.
	r = boardMove(dev, map[string]any{"element": s1["id"], "column": colIdx("Fatto")}, http.StatusUnprocessableEntity)
	if !strings.Contains(r["detail"].(string), "task aperti") {
		t.Fatalf("gate dei task: %v", r)
	}
	if st := state(t, dev, s1["key"].(string)); st != "In corso" {
		t.Fatalf("la story bloccata è cambiata: %s", st)
	}
	// Il bug ha la fase in più; una story non ce l'ha.
	r = boardMove(dev, map[string]any{"element": s3["id"], "column": colIdx("Da verificare")}, http.StatusUnprocessableEntity)
	if !strings.Contains(r["detail"].(string), "Da verificare") {
		t.Fatalf("colonna senza la fase del tipo: %v", r)
	}
	// La query filtra la board.
	if _, cards := board(dev, "?q="+strings.ReplaceAll("tipo: bug", " ", "%20")); len(cards) != 1 || cards[0]["key"] != b1["key"] {
		t.Fatalf("filtro della board: %v", cards)
	}
	if _, cards := board(dev, "?swimlane=assignee&q=assegnatario:%20me"); len(cards) != 2 || cards[0]["lane"] != dev.member {
		t.Fatalf("filtro per assegnatario e swimlane: %v", cards)
	}

	// --- Ordinamento manuale (rank) ----------------------------------------------------------
	order := func() []string {
		_, cards := board(dev, "")
		var keys []string
		for _, c := range cards {
			if c["col"].(float64) == float64(colIdx("Da fare")) {
				keys = append(keys, c["key"].(string))
			}
		}
		return keys
	}
	before := order() // s3, t1, b1 nell'ordine di creazione
	if strings.Join(before, ",") != strings.Join([]string{s3["key"].(string), t1["key"].(string), b1["key"].(string)}, ",") {
		t.Fatalf("ordine iniziale: %v", before)
	}
	dev.do("PUT", "/api/v1/work/elements/"+b1["key"].(string)+"/rank", map[string]any{"before": s3["id"]}, http.StatusOK)
	boardMove(dev, map[string]any{"element": t1["id"], "after": b1["id"], "before": s3["id"]}, http.StatusOK)
	if got := order(); strings.Join(got, ",") != strings.Join([]string{b1["key"].(string), t1["key"].(string), s3["key"].(string)}, ",") {
		t.Fatalf("ordine dopo gli spostamenti: %v", got)
	}
	// Molti inserimenti nello stesso punto: le chiavi restano corte (ridistribuzione).
	for range 60 {
		dev.do("PUT", "/api/v1/work/elements/"+s3["key"].(string)+"/rank", map[string]any{"after": b1["id"], "before": t1["id"]}, http.StatusOK)
		dev.do("PUT", "/api/v1/work/elements/"+t1["key"].(string)+"/rank", map[string]any{"after": b1["id"], "before": s3["id"]}, http.StatusOK)
	}
	if got := order(); strings.Join(got, ",") != strings.Join([]string{b1["key"].(string), t1["key"].(string), s3["key"].(string)}, ",") {
		t.Fatalf("ordine dopo molti spostamenti: %v", got)
	}

	// --- Sprint, burndown e spostamento degli incompiuti ------------------------------------
	today := time.Now().Format(time.DateOnly)
	sp1 := dev.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Sprint 1", "goal": "Accesso al portale",
		"start": today, "end": time.Now().AddDate(0, 0, 9).Format(time.DateOnly)}, http.StatusCreated)
	sp2 := dev.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Sprint 2"}, http.StatusCreated)
	for _, it := range []map[string]any{s1, s2, s3} {
		dev.do("PUT", "/api/v1/work/elements/"+it["key"].(string)+"/sprint", map[string]any{"sprint": sp1["key"]}, http.StatusOK)
	}
	// Il referente del team ha il ruolo di sola lettura: non pianifica. La pagina non gli mostra i
	// pulsanti (can_manage falso) e il server rifiuta comunque ogni azione sugli sprint.
	if lb := lead.do("GET", "/api/v1/work/spaces/WEB/backlog", nil, http.StatusOK); lb["can_manage"] != false || lb["can_work"] != false {
		t.Fatalf("il referente in sola lettura non deve poter gestire gli sprint: %v %v", lb["can_manage"], lb["can_work"])
	}
	lead.do("POST", "/api/v1/work/spaces/WEB/sprints", map[string]any{"title": "Sprint del referente"}, http.StatusForbidden)
	lead.do("PATCH", "/api/v1/work/sprints/"+sp2["key"].(string), map[string]any{"title": "Altro nome"}, http.StatusForbidden)
	lead.do("POST", "/api/v1/work/sprints/"+sp1["key"].(string)+"/start", nil, http.StatusForbidden)
	// Lo sprint 2 non parte senza date; lo sprint 1 sì, e poi nessun altro finché è attivo.
	if r := dev.do("POST", "/api/v1/work/sprints/"+sp2["key"].(string)+"/start", nil, http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "data di inizio") {
		t.Fatalf("gate delle date: %v", r)
	}
	dev.do("POST", "/api/v1/work/sprints/"+sp1["key"].(string)+"/start", nil, http.StatusOK)
	dev.do("PATCH", "/api/v1/work/sprints/"+sp2["key"].(string), map[string]any{"start": today, "end": time.Now().AddDate(0, 0, 20).Format(time.DateOnly)}, http.StatusOK)
	if r := dev.do("POST", "/api/v1/work/sprints/"+sp2["key"].(string)+"/start", nil, http.StatusUnprocessableEntity); !strings.Contains(r["detail"].(string), "già uno sprint attivo") {
		t.Fatalf("gate dello sprint attivo: %v", r)
	}
	// La board mostra lo sprint attivo.
	if bb, cards := board(dev, ""); bb["sprint"].(map[string]any)["key"] != sp1["key"] || len(cards) != 3 {
		t.Fatalf("board dello sprint attivo: %v %v", bb["sprint"], len(cards))
	}
	// Si chiude il task e poi la story: avanzamento dell'epic e della richiesta.
	dev.do("PUT", "/api/v1/work/elements/"+t1["key"].(string)+"/assignee", map[string]any{"member_id": dev.member}, http.StatusOK)
	move(dev, t1["id"].(string), "In corso", http.StatusOK)
	move(dev, t1["id"].(string), "Fatto", http.StatusOK)
	boardMove(dev, map[string]any{"element": s1["id"], "column": colIdx("Fatto")}, http.StatusOK)
	bd := dev.do("GET", "/api/v1/work/sprints/"+sp1["key"].(string)+"/burndown", nil, http.StatusOK)
	actual := bd["actual"].([]any)
	if bd["unit"] != "points" || bd["committed"].(float64) != 10 || bd["remaining"].(float64) != 7 || len(actual) == 0 ||
		actual[len(actual)-1].(map[string]any)["value"].(float64) != 7 || len(bd["ideal"].([]any)) != 2 {
		t.Fatalf("burndown: %v", bd)
	}
	chk := admin.do("POST", "/api/v1/conditions/check", map[string]any{"condition": "avanzamento: >=33", "element": reqKey}, http.StatusOK)
	if chk["result"] != true || chk["facts"].(map[string]any)["avanzamento"].([]any)[0].(float64) != 33.3 {
		t.Fatalf("avanzamento della richiesta: %v", chk)
	}
	if p := admin.do("GET", "/api/v1/work/elements/"+impKey+"/delivery", nil, http.StatusOK)["progress"].(map[string]any); p["done"].(float64) != 1 || p["total"].(float64) != 3 {
		t.Fatalf("avanzamento dell'impatto: %v", p)
	}
	// Chiusura: gli incompiuti passano allo sprint successivo; il burndown resta.
	dev.do("POST", "/api/v1/work/sprints/"+sp1["key"].(string)+"/close", map[string]any{"move_to": "next"}, http.StatusOK)
	bl := dev.do("GET", "/api/v1/work/spaces/WEB/backlog", nil, http.StatusOK)
	inSprint := map[string]string{}
	for _, it := range bl["items"].([]any) {
		m := it.(map[string]any)
		inSprint[m["key"].(string)], _ = m["sprint"].(string)
	}
	if inSprint[s2["key"].(string)] != sp2["id"] || inSprint[s3["key"].(string)] != sp2["id"] || inSprint[b1["key"].(string)] != "" {
		t.Fatalf("incompiuti dopo la chiusura: %v", inSprint)
	}
	if len(bl["sprints"].([]any)) != 1 {
		t.Fatalf("sprint aperti: %v", bl["sprints"])
	}
	bd = dev.do("GET", "/api/v1/work/sprints/"+sp1["key"].(string)+"/burndown", nil, http.StatusOK)
	if bd["closed"] != true || bd["remaining"].(float64) != 7 || bd["committed"].(float64) != 10 {
		t.Fatalf("burndown dello sprint chiuso: %v", bd)
	}
	if dev.do("GET", "/api/v1/elements/"+sp1["key"].(string), nil, http.StatusOK)["element"].(map[string]any)["state"] != "Chiuso" {
		t.Fatal("sprint non chiuso")
	}

	// --- Ore → costi della richiesta ----------------------------------------------------------
	fin.do("POST", "/api/v1/finance/rates", map[string]any{"member_id": dev.member, "hourly_cents": 6000, "valid_from": "2026-01-01"}, http.StatusCreated)
	lg := dev.do("POST", "/api/v1/work/elements/"+t1["key"].(string)+"/time", map[string]any{"duration": "2h", "text": "Configurazione del client"}, http.StatusCreated)
	if lg["costed"] != true || lg["time"].(map[string]any)["minutes"].(float64) != 120 {
		t.Fatalf("ore registrate: %v", lg)
	}
	dev.do("POST", "/api/v1/work/elements/"+s1["key"].(string)+"/time", map[string]any{"duration": "1,5"}, http.StatusCreated)
	dev.do("POST", "/api/v1/work/elements/"+s1["key"].(string)+"/time", map[string]any{"duration": "25h"}, http.StatusUnprocessableEntity)
	adminB.do("POST", "/api/v1/work/elements/"+s1["key"].(string)+"/time", map[string]any{"hours": 1}, http.StatusNotFound)
	tm := dev.do("GET", "/api/v1/work/elements/"+s1["key"].(string)+"/time", nil, http.StatusOK)
	if tm["minutes"].(float64) != 90 || tm["total_minutes"].(float64) != 210 {
		t.Fatalf("ore della story: %v", tm)
	}
	// Chi registra non vede i costi; la richiesta li somma (impatto → epic → story → task).
	dev.do("GET", "/api/v1/finance/work-logs", nil, http.StatusForbidden)
	tot := admin.do("GET", "/api/v1/demand/requests/"+reqKey+"/finance", nil, http.StatusOK)
	if tot["actual_cents"].(float64) != 21000 {
		t.Fatalf("costo delle ore nella richiesta: %v", tot["actual_cents"])
	}
	logs := fin.do("GET", "/api/v1/finance/work-logs?element="+reqKey, nil, http.StatusOK)["items"].([]any)
	if len(logs) != 2 {
		t.Fatalf("ore nella finanza: %v", logs)
	}
	// Una propria registrazione si cancella (con la voce di costo).
	id := lg["id"].(string)
	lead.do("DELETE", "/api/v1/work/time/"+id, nil, http.StatusForbidden)
	dev.do("DELETE", "/api/v1/work/time/"+id, nil, http.StatusNoContent)
	if tot := admin.do("GET", "/api/v1/demand/requests/"+reqKey+"/finance", nil, http.StatusOK); tot["actual_cents"].(float64) != 9000 {
		t.Fatalf("costo dopo la cancellazione: %v", tot["actual_cents"])
	}

	// --- Il mio lavoro -----------------------------------------------------------------------
	mine := dev.do("GET", "/api/v1/work/me", nil, http.StatusOK)["items"].([]any)
	keys := map[string]bool{}
	for _, m := range mine {
		keys[m.(map[string]any)["key"].(string)] = true
	}
	if !keys[s1["key"].(string)] || !keys[s2["key"].(string)] || keys[s3["key"].(string)] {
		t.Fatalf("il mio lavoro: %v", keys)
	}

	// --- Isolamento tra tenant ---------------------------------------------------------------
	adminB.do("GET", "/api/v1/work/spaces/WEB/board", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/work/spaces/WEB/backlog", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/work/sprints/"+sp1["key"].(string)+"/burndown", nil, http.StatusNotFound)
	adminB.do("GET", "/api/v1/work/elements/"+impKey+"/delivery", nil, http.StatusNotFound)
	adminB.do("POST", "/api/v1/work/spaces/WEB/board/move", map[string]any{"element": s1["id"], "column": 0}, http.StatusNotFound)
	adminB.do("PUT", "/api/v1/work/elements/"+s1["id"].(string)+"/rank", map[string]any{"after": s2["id"]}, http.StatusNotFound)
	if n := len(adminB.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("spazi visibili all'altro tenant: %d", n)
	}
	// Anche l'altro tenant può avere uno spazio WEB: le chiavi sono per tenant.
	adminB.do("POST", "/api/v1/teams", map[string]any{"name": "Web"}, http.StatusCreated)
	if items := adminB.do("GET", "/api/v1/work/spaces", nil, http.StatusOK)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["key"] != "WEB" {
		t.Fatalf("spazio dell'altro tenant: %v", items)
	}
	if _, cards := board(adminB, ""); len(cards) != 0 {
		t.Fatalf("card dell'altro tenant: %v", cards)
	}
}
