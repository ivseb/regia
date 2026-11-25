package itest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/api"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Linguaggio di query completo e autocompletamento (piano, passo 16): campi
// personalizzati, operatori, date relative, negazioni, campi dei moduli, ordinamenti
// con cursore, errori con la posizione, isolamento tra tenant.
func TestQueryLanguage(t *testing.T) {
	d := needDB(t)
	ctx := context.Background()

	eng := engine.New()
	ap := approval.Register(eng)
	dm := demand.Register(eng, ap)
	arc := architecture.Register(eng)
	srv := httptest.NewServer(api.New(api.Options{
		DB: d, Auth: auth.NewService(d, time.Hour), Engine: eng, Approvals: ap, Demand: dm, Arch: arc,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Web: os.DirFS(t.TempDir()), Version: "test", Dev: true, SessionTTL: time.Hour,
	}).Handler())
	defer srv.Close()

	// Due tenant con gli stessi nomi di campi, team e chiavi: nulla deve attraversare il confine.
	setup := func(prefix string) (string, string) {
		slug := strings.ReplaceAll(prefix+"-"+time.Now().Format("150405.000"), ".", "")
		ten, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: slug, Name: "Query " + prefix, AdminEmail: "admin@" + slug + ".test", AdminPassword: testPassword,
			Provision: []tenant.Provisioner{demand.Provision, architecture.Provision},
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range []struct {
			name string
			in   tenant.MemberInput
		}{
			{"richiedente", tenant.MemberInput{MemberType: "requester", Roles: []string{"requester"}}},
			{"dm", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}}},
			{"lettore", tenant.MemberInput{Roles: []string{"viewer"}}},
		} {
			in := m.in
			in.TenantSlug, in.Email, in.Name, in.Password = slug, m.name+"@"+slug+".test", "Persona "+m.name, testPassword
			if _, err := tenant.AddMember(ctx, d, in); err != nil {
				t.Fatal(err)
			}
		}
		// Campi personalizzati della business request: importo (numero), collaudo (data),
		// fornitore (testo, alias "vendor").
		err = d.InTenant(ctx, ten.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			var typeID string
			if err := tx.QueryRow(ctx, "select id from element_types where key = 'business_request'").Scan(&typeID); err != nil {
				return err
			}
			for i, f := range [][3]string{{"importo", "Importo", "decimal"}, {"collaudo", "Data di collaudo", "date"}, {"fornitore", "Fornitore", "string"}} {
				aliases := []string{}
				if f[0] == "fornitore" {
					aliases = []string{"vendor"}
				}
				fid := db.NewID()
				if _, err := tx.Exec(ctx, `insert into field_defs (tenant_id, id, key, name, data_type, aliases) values (app_tenant(), $1, $2, $3, $4, $5)`, fid, f[0], f[1], f[2], aliases); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `insert into type_fields (tenant_id, id, type_id, field_id, ordinal) values (app_tenant(), $1, $2, $3, $4)`, db.NewID(), typeID, fid, 10+i); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return slug, ten.TenantID
	}
	slugA, _ := setup("qa")
	slugB, _ := setup("qb")
	admin := login(t, srv.URL, "admin@"+slugA+".test", testPassword)
	req := login(t, srv.URL, "richiedente@"+slugA+".test", testPassword)
	man := login(t, srv.URL, "dm@"+slugA+".test", testPassword)
	reader := login(t, srv.URL, "lettore@"+slugA+".test", testPassword)
	adminB := login(t, srv.URL, "admin@"+slugB+".test", testPassword)

	rome, _ := time.LoadLocation("Europe/Rome")
	in10 := time.Now().In(rome).AddDate(0, 0, 10).Format(time.DateOnly)
	newReq := func(u *user, title string, fields map[string]any, extra map[string]any) (string, string) {
		body := map[string]any{"title": title, "description": "Richiesta di prova per il linguaggio di query.", "fields": fields}
		for k, v := range extra {
			body[k] = v
		}
		r := u.do("POST", "/api/v1/demand/requests", body, http.StatusCreated)
		return r["key"].(string), r["id"].(string)
	}
	k1, id1 := newReq(req, "Registro dei fornitori ICT", map[string]any{"urgenza": "Alta", "importo": 1500, "collaudo": "2026-12-01", "fornitore": "Acme"},
		map[string]any{"regulatory_deadline": in10, "is_mandatory": true})
	k2, _ := newReq(req, "Portale clienti B2B", map[string]any{"urgenza": "Media", "importo": 50000, "fornitore": "Beta srl"}, nil)
	k3, _ := newReq(admin, "Firma digitale dei contratti", map[string]any{"urgenza": "Bassa"}, nil)
	newReq(adminB, "Registro dei fornitori del tenant B", map[string]any{"urgenza": "Alta", "importo": 999999}, nil)

	// R1 avanza fino al triage; le altre restano in bozza.
	req.do("POST", "/api/v1/elements/"+id1+"/transitions", map[string]any{"to": "Inviata"}, http.StatusOK)
	man.do("POST", "/api/v1/elements/"+id1+"/transitions", map[string]any{"to": "Triage"}, http.StatusOK)

	// Architettura: team, applicazione con codice, asset citato da R2, impatto di R1.
	teamA := admin.do("POST", "/api/v1/teams", map[string]any{"name": "Pagamenti"}, http.StatusCreated)["id"].(string)
	adminB.do("POST", "/api/v1/teams", map[string]any{"name": "Pagamenti"}, http.StatusCreated)
	adminB.do("POST", "/api/v1/teams", map[string]any{"name": "Solo nel tenant B"}, http.StatusCreated)
	admin.do("POST", "/api/v1/teams", map[string]any{"name": "Solo nel tenant A"}, http.StatusCreated)
	admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Modulo pagamenti", "code": "PAY", "team_id": teamA}, http.StatusCreated)
	var app map[string]any
	for _, a := range admin.do("GET", "/api/v1/architecture/applications", nil, http.StatusOK)["items"].([]any) {
		app = a.(map[string]any)
	}
	appKey := app["key"].(string)
	asset := admin.do("POST", "/api/v1/architecture/assets", map[string]any{"name": "Area riservata"}, http.StatusCreated)["id"].(string)
	assetKey := admin.do("GET", "/api/v1/elements/"+asset, nil, http.StatusOK)["element"].(map[string]any)["key"].(string)
	admin.do("POST", "/api/v1/architecture/assets/"+asset+"/components", map[string]any{"application_id": app["id"], "relation": "composed_of"}, http.StatusNoContent)
	req.do("POST", "/api/v1/demand/requests/"+k2+"/assets", map[string]any{"asset_id": asset}, http.StatusNoContent)
	imp := man.do("POST", "/api/v1/demand/requests/"+k1+"/impacts", map[string]any{"application_id": app["id"]}, http.StatusCreated)
	impKey := imp["key"].(string)

	// Il lettore segue R2 (non è segnalatore né stakeholder); il richiedente smette di seguire R1.
	reader.do("PUT", "/api/v1/elements/"+k2+"/watch", nil, http.StatusOK)
	req.do("DELETE", "/api/v1/elements/"+k1+"/watch", nil, http.StatusOK)

	search := func(u *user, q string) []string {
		t.Helper()
		items := u.do("GET", "/api/v1/elements?limit=200&q="+url.QueryEscape(q), nil, http.StatusOK)["items"].([]any)
		out := []string{}
		for _, it := range items {
			out = append(out, it.(map[string]any)["key"].(string))
		}
		return out
	}
	set := func(xs ...string) []string {
		out := append([]string{}, xs...)
		sort.Strings(out)
		return out
	}
	br := "tipo: business_request "
	cases := []struct {
		q    string
		want []string
	}{
		// Campi personalizzati: opzione, numero, testo (anche per alias), data.
		{"urgenza: Alta", set(k1)},
		{"urgenza: Alta, Media", set(k1, k2)},
		{"urgenza: Alta oppure urgenza: Bassa", set(k1, k3)},
		{br + "-urgenza: Alta", set(k2, k3)},
		{"importo: >1000", set(k1, k2)},
		{"importo: > 10000", set(k2)},
		{"importo: 1000..2000", set(k1)},
		{"importo: ..1500", set(k1)},
		{br + "importo: vuoto", set(k3)},
		{"importo: presente", set(k1, k2)},
		{"vendor: Acme", set(k1)},
		{`fornitore: "Beta srl"`, set(k2)},
		{"collaudo: 2026-12-01", set(k1)},
		{"collaudo: >2026-11-30", set(k1)},
		{"collaudo: <2026-12-01", set()},
		{"collaudo: 2026-12", set(k1)},
		// Date relative sui campi di sistema e sulla scadenza del demand.
		{br + "creata: oggi", set(k1, k2, k3)},
		{br + "creata: ieri", set()},
		{br + "aggiornata: -7g", set(k1, k2, k3)},
		{br + "creata: <-1g", set()},
		{br + "aggiornata: settimana", set(k1, k2, k3)},
		{"scadenza: +30g", set(k1)},
		{"scadenza: -30g", set()},
		{br + "scadenza: vuoto", set(k2, k3)},
		// Campi di sistema e negazioni.
		{"chiave: " + k1, set(k1)},
		{"chiave: " + k1 + ", " + k3, set(k1, k3)},
		{"fase: Triage", set(k1)},
		{"stato: Triage", set(k1)},
		{br + "-fase: Bozza", set(k1)},
		{br + "fase: Bozza", set(k2, k3)},
		{br + "fase: aperta", set(k1, k2, k3)},
		{br + "fase: chiusa", set()},
		{br + "segnalatore: me", set(k3)},
		{br + "-segnalatore: me", set(k1, k2)},
		{br + "assegnatario: vuoto", set(k1, k2, k3)},
		{br + "-assegnatario: me", set(k1, k2, k3)},
		{"team: nessuno", set(k2, k3, assetKey)},
		{"genitore: " + k1, set(impKey)},
		{"tipo: impact genitore: vuoto", set()},
		{br + "inizio: vuoto", set(k1, k2, k3)},
		// Campi dei moduli: team, applicazione (per codice e per chiave), asset, richiesta.
		{"team: Pagamenti", set(k1, impKey, appKey)},
		{br + "team: Pagamenti", set(k1)},
		{"applicazione: PAY", set(k1, impKey)},
		{"applicazione: " + appKey, set(k1, impKey)},
		{`applicazione: "Modulo pagamenti" tipo: impact`, set(impKey)},
		{`asset: "Area riservata"`, set(k2, appKey)},
		{"richiesta: " + k1, set(impKey)},
		{br + "-team: Pagamenti", set(k2, k3)},
		// Testo libero, prefissi e combinazioni.
		{"regis*", set(k1, impKey)},
		{br + "regis*", set(k1)},
		{br + "fi*", set(k3)}, // prefisso corto: scorrimento dell'indice ordinato
		{"fornitori urgenza: Alta", set(k1)},
		{"porta* importo: >100", set(k2)},
	}
	for _, strat := range []int{2000, 0} { // 2000: filtri risolti in id; 0: sempre predicati
		old := engine.QueryMaxIDs
		engine.QueryMaxIDs = strat
		for _, c := range cases {
			if got := set(search(admin, c.q)...); !reflect.DeepEqual(got, c.want) {
				t.Errorf("[id max %d] %q: %v, attesi %v", strat, c.q, got, c.want)
			}
		}
		engine.QueryMaxIDs = old
	}

	// Osservati: chi segue esplicitamente, e chi ha smesso di seguire.
	if got := set(search(reader, "osservati: me")...); !reflect.DeepEqual(got, set(k2)) {
		t.Errorf("osservati del lettore: %v", got)
	}
	if got := set(search(req, br+"osservati: me")...); !reflect.DeepEqual(got, set(k2)) {
		t.Errorf("osservati del richiedente (ha smesso di seguire %s): %v", k1, got)
	}
	// Il richiedente vede solo le proprie richieste anche con i filtri risolti in id.
	if got := set(search(req, "urgenza: Alta, Bassa")...); !reflect.DeepEqual(got, set(k1)) {
		t.Errorf("visibilità del richiedente: %v", got)
	}

	// Ordinamenti: campo personalizzato, opzione nell'ordine dell'elenco, fase, titolo;
	// la paginazione a cursore attraversa i gruppi senza duplicati né salti.
	ordered := func(u *user, q string, limit int) []string {
		t.Helper()
		var out []string
		path := "/api/v1/elements?limit=" + itoa(limit) + "&q=" + url.QueryEscape(q)
		for page := 0; page < 20; page++ {
			res := u.do("GET", path, nil, http.StatusOK)
			for _, it := range res["items"].([]any) {
				out = append(out, it.(map[string]any)["key"].(string))
			}
			next, _ := res["next"].(string)
			if next == "" {
				return out
			}
			path = "/api/v1/elements?limit=" + itoa(limit) + "&q=" + url.QueryEscape(q) + "&cursor=" + url.QueryEscape(next)
		}
		t.Fatalf("%q: troppe pagine", q)
		return nil
	}
	sorts := []struct {
		q    string
		want []string
	}{
		{br + "ordina per: importo", []string{k2, k1, k3}},
		{br + "ordina per: importo asc", []string{k1, k2, k3}},
		{br + "ordina per: importo desc", []string{k2, k1, k3}},
		{br + "ordina per: urgenza", []string{k1, k2, k3}},
		{br + "ordina per: urgenza desc", []string{k3, k2, k1}},
		{br + "ordina per: titolo", []string{k3, k2, k1}},
		{br + "ordina per: fase desc", []string{k1, k3, k2}},
		{br + "ordina per: chiave asc", []string{k1, k2, k3}},
		{br + "ordina per: collaudo", []string{k1, k3, k2}},
		{br + "ordina per: fine", []string{k1, k3, k2}}, // tutte senza fine: per aggiornamento
	}
	for _, strat := range []int{2000, 0} {
		old := engine.QueryMaxIDs
		engine.QueryMaxIDs = strat
		for _, c := range sorts {
			for _, limit := range []int{1, 2, 50} {
				if got := ordered(admin, c.q, limit); !reflect.DeepEqual(got, c.want) {
					t.Errorf("[id max %d, pagine da %d] %q: %v, attesi %v", strat, limit, c.q, got, c.want)
				}
			}
		}
		engine.QueryMaxIDs = old
	}
	// "ordina per: fase" mette le fasi nell'ordine del processo: Bozza prima di Triage.
	if got := ordered(admin, br+"ordina per: fase", 50); got[len(got)-1] != k1 {
		t.Errorf("ordine per fase: %v", got)
	}

	// Errori chiari con la posizione del frammento sbagliato.
	errs := []struct{ q, frag, msg string }{
		{"colore: rosso", "colore", "non è un campo"},
		{"urgenza: Altissima", "Altissima", "non è un'opzione"},
		{"importo: abc", "abc", "non è un numero"},
		{"creata: domenica", "domenica", "data «domenica» non valida"},
		{"ordina per: colore", "colore", "non si può ordinare"},
		{"ordina per: fornitore asc fase: Inesistente", "Inesistente", "fase «Inesistente» inesistente"},
		{"team: Inesistente", "Inesistente", "non trovato"},
		{"titolo: x", "titolo", "serve solo per ordinare"},
		{"tipo: business_request fase:", "fase:", "manca il valore"},
		{"genitore: XYZ-1", "XYZ-1", "inesistente"},
		{"tipo: >3", ">3", "senza operatori"},
	}
	for _, e := range errs {
		r := admin.do("GET", "/api/v1/elements?q="+url.QueryEscape(e.q), nil, http.StatusUnprocessableEntity)
		at := strings.Index(e.q, e.frag)
		if r["code"] != "query.invalid" || !strings.Contains(r["detail"].(string), e.msg) || int(r["pos"].(float64)) != at {
			t.Errorf("%q: %v (atteso %q alla posizione %d)", e.q, r, e.msg, at)
		}
	}

	// Autocompletamento.
	suggest := func(u *user, q string, pos int) map[string]any {
		t.Helper()
		return u.do("GET", "/api/v1/query/suggest?q="+url.QueryEscape(q)+"&pos="+itoa(pos), nil, http.StatusOK)
	}
	labels := func(r map[string]any) []string {
		var out []string
		for _, it := range r["items"].([]any) {
			out = append(out, it.(map[string]any)["label"].(string))
		}
		return out
	}
	has := func(xs []string, x string) bool {
		for _, y := range xs {
			if y == x {
				return true
			}
		}
		return false
	}
	var total time.Duration
	n := 0
	check := func(u *user, q string, pos int, want ...string) map[string]any {
		t.Helper()
		t0 := time.Now()
		r := suggest(u, q, pos)
		total += time.Since(t0)
		n++
		got := labels(r)
		for _, w := range want {
			if !has(got, w) {
				t.Errorf("suggerimenti per %q (pos %d): %v, manca %q", q, pos, got, w)
			}
		}
		return r
	}
	r := check(admin, "fa", 2, "fase")
	if it := r["items"].([]any)[0].(map[string]any); it["insert"] != "fase: " || r["from"].(float64) != 0 || r["to"].(float64) != 2 {
		t.Errorf("inserimento del campo: %v", r)
	}
	check(admin, "", 0, "tipo", "fase", "urgenza", "importo", "team", "ordina per")
	check(admin, "-ass", 4, "assegnatario")
	check(admin, "imp", 3, "importo")
	check(admin, "ven", 3, "vendor")
	r = check(admin, br+"fase: ", len(br)+6, "Triage", "Bozza", "aperta", "chiusa")
	if has(labels(r), "Da analizzare") {
		t.Errorf("fasi di altri tipi tra i suggerimenti con tipo: business_request: %v", labels(r))
	}
	check(admin, "urgenza: M", 10, "Media")
	r = check(admin, "team: Pa", 8, "Pagamenti")
	if it := r["items"].([]any)[0].(map[string]any); it["insert"] != "Pagamenti " {
		t.Errorf("inserimento del valore: %v", it)
	}
	if has(labels(check(admin, "team: ", 6)), "Solo nel tenant B") {
		t.Error("l'autocompletamento propone un team dell'altro tenant")
	}
	check(admin, "applicazione: ", 14, appKey)
	check(admin, "assegnatario: ", 14, "me", "Persona dm")
	check(admin, "aggiornata: ", 12, "oggi", "-7g", ">")
	check(admin, "importo: ", 9, ">", "<=", "vuoto")
	check(admin, "ordina per: ", 12, "fase", "importo", "urgenza", "aggiornata")
	check(admin, "ordina per: fase ", 17, "asc", "desc")
	check(admin, "fase: Triage, ", 14, "Valutazione")
	if r := check(admin, "colore: rosso", 13); r["error"] == nil || r["error"].(map[string]any)["pos"].(float64) != 0 {
		t.Errorf("errore nell'autocompletamento: %v", r["error"])
	}
	if r := check(admin, "fase: Triage", 12); r["error"] != nil {
		t.Errorf("errore inatteso: %v", r["error"])
	}
	t.Logf("autocompletamento: %d richieste, media %s", n, (total / time.Duration(n)).Round(time.Microsecond))
	if avg := total / time.Duration(n); avg > 100*time.Millisecond {
		t.Errorf("autocompletamento lento: media %s", avg)
	}

	// Isolamento: il tenant B ha gli stessi nomi (campo importo, team Pagamenti, chiave DEM-1)
	// ma non vede nulla di A, e viceversa.
	if got := search(adminB, "importo: >0"); len(got) != 1 {
		t.Errorf("tenant B, importo: %v", got)
	}
	if got := search(adminB, "team: Pagamenti"); len(got) != 0 {
		t.Errorf("tenant B, team: %v", got)
	}
	if got := search(admin, br+"fornitori"); !reflect.DeepEqual(got, []string{k1}) {
		t.Errorf("tenant A, testo: %v", got)
	}
	adminB.do("GET", "/api/v1/elements?q="+url.QueryEscape("applicazione: "+appKey), nil, http.StatusUnprocessableEntity)
	if got := search(adminB, "vendor: Acme"); len(got) != 0 {
		t.Errorf("tenant B, campo con lo stesso alias: %v", got)
	}
	if has(labels(suggest(adminB, "team: ", 6)), "Solo nel tenant A") {
		t.Error("l'autocompletamento del tenant B propone un team del tenant A")
	}

	// Catalogo dei campi per le colonne della tabella.
	fields := admin.do("GET", "/api/v1/query/fields", nil, http.StatusOK)["items"].([]any)
	byKey := map[string]map[string]any{}
	for _, f := range fields {
		m := f.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	if byKey["urgenza"]["source"] != "custom" || len(byKey["urgenza"]["options"].([]any)) != 3 || byKey["state"]["sortable"] != true || byKey["team"]["sortable"] != false {
		t.Errorf("catalogo dei campi: %v %v %v", byKey["urgenza"], byKey["state"], byKey["team"])
	}
	// Tipi a cui si applica ciascun campo: i campi dei moduli dichiarano i propri, quelli di sistema valgono per tutti.
	if types, _ := byKey["deadline"]["types"].([]any); len(types) != 1 || types[0] != "business_request" {
		t.Errorf("tipi del campo «scadenza»: %v", byKey["deadline"]["types"])
	}
	if _, ok := byKey["assignee"]["types"]; ok {
		t.Errorf("un campo di sistema non dichiara tipi: %v", byKey["assignee"])
	}

	// Colonne della domanda: solo con demand=1, solo per le richieste della pagina.
	page := admin.do("GET", "/api/v1/elements?demand=1&limit=5&q="+url.QueryEscape("tipo: business_request"), nil, http.StatusOK)
	if items, _ := page["items"].([]any); len(items) > 0 {
		cols, _ := page["demand"].(map[string]any)
		id := items[0].(map[string]any)["id"].(string)
		col, ok := cols[id].(map[string]any)
		if !ok || col["days_in_state"] == nil {
			t.Errorf("colonne della domanda assenti per %s: %v", id, cols)
		}
	}
	plain := admin.do("GET", "/api/v1/elements?limit=5&q="+url.QueryEscape("tipo: business_request"), nil, http.StatusOK)
	if _, ok := plain["demand"]; ok {
		t.Error("senza demand=1 la risposta non porta le colonne della domanda")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
