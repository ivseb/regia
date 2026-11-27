package itest

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/requirements"
	"regia/internal/platform/db"
)

// Clausola di relazione e sottoquery del linguaggio di query (ondata 3 dell'AI): relazione
// generica e per tipo nei due versi, chiavi, «presente», negazione, sottoquery con la
// visibilità della persona (un richiedente del portale non scopre i collegamenti verso ciò che
// non vede), limiti ed errori con la posizione.
func TestQueryRelazioni(t *testing.T) {
	e := newOndata2(t, "qrel")
	newReq := func(u *user, title string) (string, string) {
		r := u.do("POST", "/api/v1/demand/requests", map[string]any{"title": title, "description": "Richiesta per le relazioni."}, http.StatusCreated)
		return r["key"].(string), r["id"].(string)
	}
	r1, r1id := newReq(e.admin, "Alfa relazioni")
	r2, r2id := newReq(e.admin, "Beta relazioni")
	r3, _ := newReq(e.admin, "Epsilon relazioni")
	c1, c1id := newReq(e.cur, "Gamma relazioni")
	c2, c2id := newReq(e.cur, "Delta relazioni")
	c3, c3id := newReq(e.cur, "Zeta relazioni")
	// Relazioni scritte direttamente: qui conta la query, non le regole dell'architettura.
	rel := func(key, src, dst string) [3]string { return [3]string{key, src, dst} }
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{}, func(tx pgx.Tx) error {
		for _, r := range [][3]string{rel("depends_on", c1id, r1id), rel("concerns", c1id, c2id), rel("depends_on", r2id, r1id), rel("uses", c3id, r2id)} {
			if _, err := tx.Exec(e.ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id)
				values (app_tenant(), $1, (select id from relation_types where key = $2), $3, $4)`, db.NewID(), r[0], r[1], r[2]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	keys := func(u *user, q string) []string {
		t.Helper()
		page := u.do("GET", "/api/v1/elements?q="+url.QueryEscape(q+` "relazioni"`)+"&limit=50", nil, http.StatusOK)
		var out []string
		for _, it := range e.list(page["items"]) {
			out = append(out, e.obj(it)["key"].(string))
		}
		slices.Sort(out)
		return out
	}
	want := func(u *user, q string, exp ...string) {
		t.Helper()
		slices.Sort(exp)
		if got := keys(u, q); !slices.Equal(got, exp) {
			t.Errorf("%s: %v, attesi %v", q, got, exp)
		}
	}

	// Chi vede tutto (editor).
	want(e.ed, `collegata: (tipo: business_request Alfa)`, c1, r2)
	want(e.ed, `"dipende da": (Alfa)`, c1, r2)
	want(e.ed, `dipende_da: `+r1, c1, r2)
	want(e.ed, `"è prerequisito di": `+c1, r1)
	want(e.ed, `depends_on: presente`, c1, r2)
	want(e.ed, `collegata: `+c2+`, `+r1, c1, r2)
	want(e.ed, `tipo: business_request -collegata: presente`, r3)
	want(e.ed, `collegata: (collegata: (Gamma))`, c1, r2)
	want(e.ed, `"è usata da": (Zeta)`, r2)
	want(e.ed, `collegata: (Inesistentissimo)`)

	// Il richiedente vede solo le sue richieste: i collegamenti verso le altre non contano e una
	// chiave che non vede vale come inesistente.
	want(e.cur, `collegata: presente`, c1, c2)
	want(e.cur, `-collegata: presente`, c3)
	want(e.cur, `collegata: (Alfa)`)
	want(e.cur, `collegata: (Delta)`, c1)
	r := e.cur.do("GET", "/api/v1/elements?q="+url.QueryEscape("collegata: "+r1), nil, http.StatusUnprocessableEntity)
	if !strings.Contains(r["detail"].(string), "inesistente") {
		t.Fatalf("chiave non visibile: %v", r)
	}

	// Errori con la posizione del frammento.
	for _, c := range []struct{ q, frag, msg string }{
		{`fase: (tipo: business_request)`, `(tipo: business_request)`, "non accetta una sottoquery"},
		{`zzz_rel: (Alfa)`, `zzz_rel`, "né una relazione"},
		{`collegata: (fase: Inesistente)`, `Inesistente`, "inesistente"},
		{`collegata: (tipo: business_request`, `(tipo: business_request`, "parentesi non chiusa"},
		{`collegata: >3`, `>3`, "indica chiavi"},
	} {
		r := e.ed.do("GET", "/api/v1/elements?q="+url.QueryEscape(c.q), nil, http.StatusUnprocessableEntity)
		if r["code"] != "query.invalid" || !strings.Contains(r["detail"].(string), c.msg) || int(r["pos"].(float64)) != strings.Index(c.q, c.frag) {
			t.Errorf("%q: %v (atteso %q in %q)", c.q, r, c.msg, c.frag)
		}
	}

	// Una sottoquery che trova troppi elementi si restringe.
	old := engine.SubqueryMaxIDs
	engine.SubqueryMaxIDs = 2
	r = e.ed.do("GET", "/api/v1/elements?q="+url.QueryEscape("collegata: (tipo: business_request)"), nil, http.StatusUnprocessableEntity)
	engine.SubqueryMaxIDs = old
	if !strings.Contains(r["detail"].(string), "più di 2") {
		t.Fatalf("sottoquery troppo larga: %v", r)
	}

	// Autocompletamento dentro la sottoquery: si propongono i campi come in una query a sé.
	q := "collegata: (fa"
	s := e.ed.do("GET", "/api/v1/query/suggest?q="+url.QueryEscape(q)+"&pos=14", nil, http.StatusOK)
	if s["context"] != "field" || !strings.Contains(fmtJSON(s["items"]), `"fase"`) || s["from"].(float64) != 12 {
		t.Fatalf("suggerimenti nella sottoquery: %v", s)
	}

	// Vulnerabilità scadute delle applicazioni (campo di DevSecOps): la query è valida e usa gli indici.
	eng := engine.New()
	ap := approval.Register(eng)
	devsecops.Register(eng, requirements.Register(eng, ap))
	if err := e.d.InTenant(e.ctx, e.tid, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		_, err := eng.Search(e.ctx, tx, engine.Actor{MemberID: "00000000-0000-0000-0000-000000000000", Permissions: []string{"*"}},
			"tipo: application scadenza_vulnerabilità: <oggi collegata: (tipo: business_request)", 10, "")
		return err
	}); err != nil {
		t.Fatalf("applicazioni con vulnerabilità scadute: %v", err)
	}
}
