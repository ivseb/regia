package itest

import (
	"context"
	"net/http"
	"testing"

	"regia/internal/platform/tenant"
)

func viewItems(t *testing.T, u *user, page string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, it := range u.do("GET", "/api/v1/views?page="+page, nil, http.StatusOK)["items"].([]any) {
		m := it.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

// Viste salvate (piano, passo 16): visibilità personale, di gruppo e del tenant, permessi di
// modifica, duplicazione, preferiti con contatore, vista predefinita per utente e per tenant,
// isolamento tra tenant.
func TestSavedViews(t *testing.T) {
	e := newSGEnv(t, "viste")
	ctx := context.Background()
	admin := login(t, e.srv.URL, "admin@"+e.slug+".test", testPassword)
	anna := e.member("anna", tenant.MemberInput{Roles: []string{"editor"}, Groups: []string{"Demand management"}})
	luca := e.member("luca", tenant.MemberInput{Roles: []string{"editor"}})

	var dm string
	if err := testDB.Owner.QueryRow(ctx, "select id::text from groups where tenant_id = $1 and name = 'Demand management'", e.tenantID).Scan(&dm); err != nil {
		t.Fatal(err)
	}

	// I gruppi con cui condividere sono quelli di cui si fa parte (tutti, per l'amministratore).
	found := false
	for _, g := range anna.do("GET", "/api/v1/views/groups", nil, http.StatusOK)["items"].([]any) {
		found = found || g.(map[string]any)["id"] == dm
	}
	for _, g := range luca.do("GET", "/api/v1/views/groups", nil, http.StatusOK)["items"].([]any) {
		if g.(map[string]any)["id"] == dm {
			t.Fatalf("Luca non è nel gruppo Demand management")
		}
	}
	if !found {
		t.Fatalf("Anna non vede il suo gruppo tra quelli con cui condividere")
	}

	// Una vista personale è visibile solo a chi l'ha creata; le impostazioni sono salvate.
	mine := anna.do("POST", "/api/v1/views", map[string]any{
		"page": "richieste", "name": "Da triare", "kind": "table", "query": "tipo: business_request fase: Triage",
		"settings": map[string]any{"cols": []string{"key", "title", "state"}, "group": "state"},
	}, http.StatusCreated)
	if mine["scope"] != "private" || mine["mine"] != true || mine["can_edit"] != true {
		t.Fatalf("vista personale: %v", mine)
	}
	if got := mine["settings"].(map[string]any)["group"]; got != "state" {
		t.Fatalf("impostazioni perse: %v", mine["settings"])
	}
	id := mine["id"].(string)
	if len(viewItems(t, luca, "richieste")) != 0 {
		t.Fatalf("la vista personale è visibile ad altri")
	}
	luca.do("GET", "/api/v1/views/"+id, nil, http.StatusNotFound)
	luca.do("PATCH", "/api/v1/views/"+id, map[string]any{"name": "mia"}, http.StatusNotFound)
	// Nemmeno l'amministratore vede e gestisce le viste personali altrui.
	admin.do("GET", "/api/v1/views/"+id, nil, http.StatusNotFound)

	// Validazioni: nome, modalità, query lunga, pagina.
	anna.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": " ", "kind": "table"}, http.StatusUnprocessableEntity)
	anna.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": "x", "kind": "gantt"}, http.StatusUnprocessableEntity)
	anna.do("POST", "/api/v1/views", map[string]any{"page": "Pagina storta!", "name": "x", "kind": "table"}, http.StatusUnprocessableEntity)
	anna.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": "x", "kind": "table", "settings": []int{1}}, http.StatusUnprocessableEntity)

	// Condivisione con un gruppo: solo chi ne fa parte; Luca (fuori dal gruppo) non può.
	luca.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": "g", "scope": "group", "group_id": dm}, http.StatusUnprocessableEntity)
	anna.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": "g", "scope": "group"}, http.StatusUnprocessableEntity)
	shared := anna.do("PATCH", "/api/v1/views/"+id, map[string]any{"scope": "group", "group_id": dm, "name": "Da triare (DM)"}, http.StatusOK)
	if shared["scope"] != "group" || shared["group_name"] != "Demand management" {
		t.Fatalf("condivisione con il gruppo: %v", shared)
	}
	// Luca non è nel gruppo: non la vede. L'amministratore sì (è nel gruppo di sistema? no: serve appartenenza).
	if len(viewItems(t, luca, "richieste")) != 0 {
		t.Fatalf("vista di gruppo visibile a chi non ne fa parte")
	}

	// Con tutto il tenant: solo l'amministratore. Poi tutti la vedono, ma la cambia solo chi può.
	anna.do("PATCH", "/api/v1/views/"+id, map[string]any{"scope": "tenant"}, http.StatusUnprocessableEntity)
	all := admin.do("POST", "/api/v1/views", map[string]any{"page": "richieste", "name": "Tutte le aperte", "kind": "list", "query": "stato: aperta", "scope": "tenant"}, http.StatusCreated)
	allID := all["id"].(string)
	got := viewItems(t, luca, "richieste")
	if len(got) != 1 || got["Tutte le aperte"]["can_edit"] != false || got["Tutte le aperte"]["mine"] != false {
		t.Fatalf("vista del tenant vista da Luca: %v", got)
	}
	luca.do("PATCH", "/api/v1/views/"+allID, map[string]any{"name": "mia"}, http.StatusForbidden)
	luca.do("DELETE", "/api/v1/views/"+allID, nil, http.StatusForbidden)
	// Pagine diverse non si mescolano.
	if len(viewItems(t, luca, "applicazioni")) != 0 {
		t.Fatalf("viste di un'altra pagina")
	}

	// Duplicare crea una copia personale, modificabile.
	dup := luca.do("POST", "/api/v1/views/"+allID+"/duplicate", map[string]any{}, http.StatusCreated)
	if dup["name"] != "Tutte le aperte (copia)" || dup["scope"] != "private" || dup["can_edit"] != true || dup["query"] != "stato: aperta" {
		t.Fatalf("duplicato: %v", dup)
	}
	luca.do("PATCH", "/api/v1/views/"+dup["id"].(string), map[string]any{"name": "Le mie aperte", "query": "stato: aperta assegnatario: me"}, http.StatusOK)

	// Preferiti con contatore: una richiesta aperta, una vista con query rotta dà "nessun contatore".
	anna.do("POST", "/api/v1/demand/requests", map[string]any{"title": "Richiesta per il contatore"}, http.StatusCreated)
	luca.do("PUT", "/api/v1/views/"+allID+"/favorite", map[string]any{"favorite": true}, http.StatusNoContent)
	luca.do("PUT", "/api/v1/views/"+allID+"/favorite", map[string]any{"favorite": true}, http.StatusNoContent) // idempotente
	favs := luca.do("GET", "/api/v1/views/favorites", nil, http.StatusOK)["items"].([]any)
	if len(favs) != 1 {
		t.Fatalf("preferiti: %v", favs)
	}
	f := favs[0].(map[string]any)
	if f["name"] != "Tutte le aperte" || f["page"] != "richieste" || f["count"] == nil || num(f["count"]) < 1 {
		t.Fatalf("contatore del preferito: %v", f)
	}
	// Un preferito è personale: Anna non lo ha.
	if n := len(anna.do("GET", "/api/v1/views/favorites", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("preferiti di Anna: %d", n)
	}
	admin.do("PATCH", "/api/v1/views/"+allID, map[string]any{"query": "fase: ("}, http.StatusOK)
	favs = luca.do("GET", "/api/v1/views/favorites", nil, http.StatusOK)["items"].([]any)
	if len(favs) != 1 || favs[0].(map[string]any)["count"] != nil {
		t.Fatalf("query rotta: il contatore deve mancare, non far fallire il menu: %v", favs)
	}
	admin.do("PATCH", "/api/v1/views/"+allID, map[string]any{"query": "stato: aperta"}, http.StatusOK)
	if items := viewItems(t, luca, "richieste"); items["Tutte le aperte"]["favorite"] != true {
		t.Fatalf("favorite non segnalato nell'elenco")
	}
	luca.do("PUT", "/api/v1/views/"+allID+"/favorite", map[string]any{"favorite": false}, http.StatusNoContent)
	if n := len(luca.do("GET", "/api/v1/views/favorites", nil, http.StatusOK)["items"].([]any)); n != 0 {
		t.Fatalf("preferito non tolto")
	}

	// Vista predefinita: per sé, e per il tenant solo l'amministratore e solo con una vista condivisa con tutti.
	luca.do("PUT", "/api/v1/views/default", map[string]any{"page": "richieste", "view_id": dup["id"], "for": "me"}, http.StatusNoContent)
	luca.do("PUT", "/api/v1/views/default", map[string]any{"page": "richieste", "view_id": allID, "for": "tenant"}, http.StatusUnprocessableEntity)
	admin.do("PUT", "/api/v1/views/default", map[string]any{"page": "richieste", "view_id": allID, "for": "tenant"}, http.StatusNoContent)
	admin.do("PUT", "/api/v1/views/default", map[string]any{"page": "applicazioni", "view_id": allID, "for": "me"}, http.StatusUnprocessableEntity) // pagina sbagliata
	items := viewItems(t, luca, "richieste")
	if items["Le mie aperte"]["default_me"] != true || items["Tutte le aperte"]["default_tenant"] != true || items["Tutte le aperte"]["default_me"] != false {
		t.Fatalf("predefinite: %v", items)
	}
	// Cambiare la predefinita sostituisce la precedente; "nessuna" la toglie.
	luca.do("PUT", "/api/v1/views/default", map[string]any{"page": "richieste", "view_id": allID, "for": "me"}, http.StatusNoContent)
	items = viewItems(t, luca, "richieste")
	if items["Le mie aperte"]["default_me"] != false || items["Tutte le aperte"]["default_me"] != true {
		t.Fatalf("predefinita sostituita: %v", items)
	}
	luca.do("PUT", "/api/v1/views/default", map[string]any{"page": "richieste", "view_id": "", "for": "me"}, http.StatusNoContent)
	// Una vista del tenant resa personale perde la predefinita del tenant.
	admin.do("PATCH", "/api/v1/views/"+allID, map[string]any{"scope": "private"}, http.StatusOK)
	var n int
	if err := testDB.Owner.QueryRow(ctx, "select count(*) from view_defaults where tenant_id = $1 and view_id = $2", e.tenantID, allID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("predefinite rimaste su una vista personale: %d (%v)", n, err)
	}

	// Eliminare: solo chi può; sparisce con i suoi preferiti.
	luca.do("DELETE", "/api/v1/views/"+dup["id"].(string), nil, http.StatusNoContent)
	luca.do("GET", "/api/v1/views/"+dup["id"].(string), nil, http.StatusNotFound)
	anna.do("DELETE", "/api/v1/views/"+id, nil, http.StatusNoContent)

	// Il registro di audit ricorda creazione, modifica ed eliminazione.
	if err := testDB.Owner.QueryRow(ctx, "select count(*) from audit_log where tenant_id = $1 and entity_type = 'view'", e.tenantID).Scan(&n); err != nil || n < 6 {
		t.Fatalf("eventi di audit delle viste: %d (%v)", n, err)
	}

	// Isolamento: un altro tenant non vede nulla, nemmeno conoscendo l'id.
	other := newSGEnv(t, "viste-altro")
	stranger := login(t, other.srv.URL, "admin@"+other.slug+".test", testPassword)
	if len(viewItems(t, stranger, "richieste")) != 0 {
		t.Fatalf("viste di un altro tenant")
	}
	stranger.do("GET", "/api/v1/views/"+allID, nil, http.StatusNotFound)
}
