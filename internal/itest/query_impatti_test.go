package itest

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// Relazioni dagli impatti nel linguaggio di query (seguito dell'ondata 3, replica/ai.md): un
// impatto lega una richiesta a un'applicazione senza una riga in relations; «impatta»,
// «impattata_da» e «impatti» lo rendono una relazione, con la visibilità della persona.
func TestQueryImpatti(t *testing.T) {
	e := newOndata2(t, "qimp")
	appA := e.admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Anagrafe quimpa", "code": "QIMPA"}, http.StatusCreated)
	appB := e.admin.do("POST", "/api/v1/architecture/applications", map[string]any{"name": "Bilancio quimpa", "code": "QIMPB"}, http.StatusCreated)
	r1 := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Prima quimpa", "description": "Con impatto."}, http.StatusCreated)
	r2 := e.admin.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Seconda quimpa", "description": "Senza impatto."}, http.StatusCreated)
	imp := e.admin.do("POST", "/api/v1/demand/requests/"+r1["key"].(string)+"/impacts", map[string]any{"application_id": appA["id"], "change_kind": "change"}, http.StatusCreated)
	keys := func(u *user, q string) []string {
		t.Helper()
		page := u.do("GET", "/api/v1/elements?q="+url.QueryEscape(q)+"&limit=50", nil, http.StatusOK)
		var out []string
		for _, it := range e.list(page["items"]) {
			out = append(out, e.obj(it)["key"].(string))
		}
		return out
	}
	aKey, bKey := appA["key"].(string), appB["key"].(string)
	if got := keys(e.admin, `tipo: application impattata_da: (tipo: business_request "quimpa")`); !slices.Equal(got, []string{aKey}) {
		t.Fatalf("applicazioni impattate: %v (attesa %s, non %s)", got, aKey, bKey)
	}
	if got := keys(e.admin, `tipo: business_request impatta: QIMPA`); !slices.Equal(got, []string{r1["key"].(string)}) {
		t.Fatalf("richieste che impattano QIMPA: %v", got)
	}
	if got := keys(e.admin, `tipo: business_request -impatta: presente "quimpa"`); !slices.Equal(got, []string{r2["key"].(string)}) {
		t.Fatalf("richieste senza impatti: %v", got)
	}
	if got := keys(e.admin, `tipo: application impatti: (tipo: impact) "quimpa"`); !slices.Equal(got, []string{aKey}) {
		t.Fatalf("applicazioni con elementi impatto: %v (impatto %v)", got, imp["key"])
	}
	// Il richiedente del portale non vede l'applicazione: la chiave vale come inesistente.
	e.cur.do("GET", "/api/v1/elements?q="+url.QueryEscape(`impatta: `+aKey), nil, http.StatusUnprocessableEntity)
	if got := keys(e.cur, `impattata_da: (tipo: business_request "quimpa")`); len(got) != 0 {
		t.Fatalf("il richiedente vede applicazioni impattate da richieste altrui: %v", got)
	}
}
