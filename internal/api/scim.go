package api

// SCIM 2.0 (RFC 7643/7644) per utenti e gruppi, sotto /scim/v2: il sottoinsieme che usano
// Entra ID, Okta e Kanidm. Autenticazione con il token SCIM dell'organizzazione
// ("Authorization: Bearer scim_…"); le regole stanno in internal/platform/admin/scim.go.
// Le richieste non passano da withPrincipal: non c'è una sessione né un membro che agisce.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"regia/internal/platform/admin"
)

const (
	scimPrefix      = "/scim/v2"
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimMaxBody     = 2 << 20
	scimMaxCount    = 200
)

// scimHandler instrada le richieste SCIM (fuori dal mux principale, senza withPrincipal).
func (s *Server) scimHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+scimPrefix+"/ServiceProviderConfig", s.scimAuth(s.scimConfig))
	mux.HandleFunc("GET "+scimPrefix+"/ResourceTypes", s.scimAuth(s.scimResourceTypes))
	mux.HandleFunc("GET "+scimPrefix+"/Schemas", s.scimAuth(s.scimSchemas))
	mux.HandleFunc("GET "+scimPrefix+"/Users", s.scimAuth(s.scimListUsers))
	mux.HandleFunc("POST "+scimPrefix+"/Users", s.scimAuth(s.scimCreateUser))
	mux.HandleFunc("GET "+scimPrefix+"/Users/{id}", s.scimAuth(s.scimGetUser))
	mux.HandleFunc("PUT "+scimPrefix+"/Users/{id}", s.scimAuth(s.scimPutUser))
	mux.HandleFunc("PATCH "+scimPrefix+"/Users/{id}", s.scimAuth(s.scimPatchUser))
	mux.HandleFunc("DELETE "+scimPrefix+"/Users/{id}", s.scimAuth(s.scimDeleteUser))
	mux.HandleFunc("GET "+scimPrefix+"/Groups", s.scimAuth(s.scimListGroups))
	mux.HandleFunc("POST "+scimPrefix+"/Groups", s.scimAuth(s.scimCreateGroup))
	mux.HandleFunc("GET "+scimPrefix+"/Groups/{id}", s.scimAuth(s.scimGetGroup))
	mux.HandleFunc("PUT "+scimPrefix+"/Groups/{id}", s.scimAuth(s.scimPutGroup))
	mux.HandleFunc("PATCH "+scimPrefix+"/Groups/{id}", s.scimAuth(s.scimPatchGroup))
	mux.HandleFunc("DELETE "+scimPrefix+"/Groups/{id}", s.scimAuth(s.scimDeleteGroup))
	mux.HandleFunc(scimPrefix+"/", func(w http.ResponseWriter, r *http.Request) {
		scimFail(w, http.StatusNotFound, "", "risorsa SCIM inesistente")
	})
	return mux
}

func (s *Server) scimAuth(h func(http.ResponseWriter, *http.Request, admin.SCIMActor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="regia-scim"`)
			scimFail(w, http.StatusUnauthorized, "", "serve Authorization: Bearer scim_…")
			return
		}
		tenantID, tokenID, err := s.adm.admin.SCIMAuth(r.Context(), strings.TrimSpace(raw))
		if errors.Is(err, admin.ErrSCIMToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="regia-scim", error="invalid_token"`)
			scimFail(w, http.StatusUnauthorized, "", err.Error())
			return
		}
		if err != nil {
			s.log.Error("scim: autenticazione", "errore", err, "request_id", requestID(r.Context()))
			scimFail(w, http.StatusServiceUnavailable, "", "servizio temporaneamente non disponibile")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, admin.SCIMActor{TenantID: tenantID, TokenID: tokenID})
	}
}

func scimWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func scimFail(w http.ResponseWriter, status int, scimType, detail string) {
	body := map[string]any{"schemas": []string{scimErrorSchema}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	scimWrite(w, status, body)
}

func (s *Server) scimError(w http.ResponseWriter, r *http.Request, err error) {
	var se *admin.SCIMError
	var ae *admin.Error
	switch {
	case errors.As(err, &se):
		scimFail(w, se.Status, se.SCIMType, se.Detail)
	case errors.As(err, &ae):
		scimFail(w, ae.Status, "", ae.Msg)
	default:
		s.log.Error("scim", "errore", err, "request_id", requestID(r.Context()))
		scimFail(w, http.StatusInternalServerError, "", "errore interno")
	}
}

func scimBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	b, err := io.ReadAll(io.LimitReader(r.Body, scimMaxBody+1))
	if err != nil || len(b) > scimMaxBody {
		scimFail(w, http.StatusRequestEntityTooLarge, "", "corpo troppo grande")
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		scimFail(w, http.StatusBadRequest, "invalidSyntax", "JSON non valido")
		return nil, false
	}
	return m, true
}

func (s *Server) scimLocation(kind, id string) string {
	return s.adm.baseURL + scimPrefix + "/" + kind + "/" + id
}

// ---------------------------------------------------------------------
// Filtri e paginazione
// ---------------------------------------------------------------------

var scimFilterRe = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9.]*)\s+eq\s+"((?:[^"\\]|\\.)*)"\s*$`)

func scimParseFilter(raw string) (*admin.SCIMFilter, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	m := scimFilterRe.FindStringSubmatch(raw)
	if m == nil {
		return nil, &admin.SCIMError{Status: 400, SCIMType: "invalidFilter", Detail: "filtro non supportato: solo «attributo eq \"valore\"»"}
	}
	v := strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(m[2])
	return &admin.SCIMFilter{Attr: strings.ToLower(m[1]), Value: v}, nil
}

func scimPage(r *http.Request) (start, count int) {
	start, count = 1, 100
	if v, err := strconv.Atoi(r.URL.Query().Get("startIndex")); err == nil && v > 1 {
		start = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && v >= 0 {
		count = min(v, scimMaxCount)
	}
	return start, count
}

func scimList(w http.ResponseWriter, total, start int, items []any) {
	scimWrite(w, http.StatusOK, map[string]any{"schemas": []string{scimListSchema}, "totalResults": total,
		"startIndex": start, "itemsPerPage": len(items), "Resources": items})
}

// ---------------------------------------------------------------------
// Configurazione del servizio
// ---------------------------------------------------------------------

func (s *Server) scimConfig(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	scimWrite(w, http.StatusOK, map[string]any{
		"schemas":          []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"documentationUri": "https://github.com/ivseb/regia/blob/main/docs/identita-aziendale.md",
		"patch":            map[string]any{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":           map[string]any{"supported": true, "maxResults": scimMaxCount},
		"changePassword":   map[string]any{"supported": false},
		"sort":             map[string]any{"supported": false},
		"etag":             map[string]any{"supported": false},
		"authenticationSchemes": []any{map[string]any{"type": "oauthbearertoken", "name": "Token SCIM",
			"description": "Authorization: Bearer scim_… creato in Configurazione → Persone e accessi → Identità aziendale", "primary": true}},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig", "location": s.adm.baseURL + scimPrefix + "/ServiceProviderConfig"},
	})
}

func (s *Server) scimResourceTypes(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	rt := func(name, endpoint, schema string) any {
		return map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": name, "name": name,
			"endpoint": endpoint, "schema": schema, "meta": map[string]any{"resourceType": "ResourceType", "location": s.adm.baseURL + scimPrefix + "/ResourceTypes/" + name}}
	}
	scimList(w, 2, 1, []any{rt("User", "/Users", scimUserSchema), rt("Group", "/Groups", scimGroupSchema)})
}

func (s *Server) scimSchemas(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	attr := func(name, typ string, multi, required bool) map[string]any {
		return map[string]any{"name": name, "type": typ, "multiValued": multi, "required": required, "mutability": "readWrite",
			"returned": "default", "uniqueness": "none", "caseExact": false}
	}
	user := map[string]any{"id": scimUserSchema, "name": "User", "attributes": []any{
		attr("userName", "string", false, true), attr("displayName", "string", false, false), attr("externalId", "string", false, false),
		attr("active", "boolean", false, false), attr("emails", "complex", true, false), attr("name", "complex", false, false)}}
	group := map[string]any{"id": scimGroupSchema, "name": "Group", "attributes": []any{
		attr("displayName", "string", false, true), attr("externalId", "string", false, false), attr("members", "complex", true, false)}}
	scimList(w, 2, 1, []any{user, group})
}

// ---------------------------------------------------------------------
// Utenti
// ---------------------------------------------------------------------

func (s *Server) scimUserJSON(u *admin.SCIMUser) map[string]any {
	out := map[string]any{
		"schemas": []string{scimUserSchema}, "id": u.ID, "userName": u.UserName, "displayName": u.DisplayName,
		"name":   map[string]any{"formatted": u.DisplayName},
		"emails": []any{map[string]any{"value": u.Email, "type": "work", "primary": true}},
		"active": u.Active,
		"meta": map[string]any{"resourceType": "User", "created": u.Created.UTC().Format(time.RFC3339),
			"lastModified": u.Modified.UTC().Format(time.RFC3339), "location": s.scimLocation("Users", u.ID)},
	}
	if u.ExternalID != "" {
		out["externalId"] = u.ExternalID
	}
	return out
}

// scimBool accetta true/false e le stringhe "True"/"False" che manda Entra ID.
func scimBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// applyUserAttr applica un attributo (nome già minuscolo) all'utente; remove lo svuota.
func applyUserAttr(u *admin.SCIMUser, path string, v any, remove bool) error {
	switch path {
	case "username":
		if !remove {
			u.UserName = str(v)
		}
	case "externalid":
		u.ExternalID = ""
		if !remove {
			u.ExternalID = str(v)
		}
	case "displayname":
		if !remove {
			u.DisplayName = str(v)
		}
	case "name.formatted":
		if !remove && str(v) != "" {
			u.DisplayName = str(v)
		}
	case "name":
		if m, ok := v.(map[string]any); ok && !remove {
			if f := str(m["formatted"]); f != "" {
				u.DisplayName = f
			} else if gn := strings.TrimSpace(str(m["givenName"]) + " " + str(m["familyName"])); gn != "" && u.DisplayName == "" {
				u.DisplayName = gn
			}
		}
	case "active":
		if remove {
			return nil
		}
		b, ok := scimBool(v)
		if !ok {
			return &admin.SCIMError{Status: 400, SCIMType: "invalidValue", Detail: "active deve essere vero o falso"}
		}
		u.Active = b
	case "emails":
		if list, ok := v.([]any); ok && !remove {
			var first, primary string
			for _, x := range list {
				m, _ := x.(map[string]any)
				val := str(m["value"])
				if val == "" {
					continue
				}
				if first == "" {
					first = val
				}
				if p, _ := scimBool(m["primary"]); p && primary == "" {
					primary = val
				}
			}
			if primary != "" {
				u.Email = primary
			} else if first != "" {
				u.Email = first
			}
		}
	default:
		// emails[type eq "work"].value e simili
		if strings.HasPrefix(path, "emails[") && strings.HasSuffix(path, "].value") && !remove && str(v) != "" {
			u.Email = str(v)
		}
		// Altri attributi (title, phoneNumbers, estensione enterprise...) non si usano.
	}
	return nil
}

func applyUserObject(u *admin.SCIMUser, m map[string]any) error {
	for k, v := range m {
		key := strings.ToLower(k)
		if key == "schemas" || key == "id" || key == "meta" || strings.HasPrefix(key, "urn:") {
			continue
		}
		if err := applyUserAttr(u, key, v, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) scimListUsers(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	f, err := scimParseFilter(r.URL.Query().Get("filter"))
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	start, count := scimPage(r)
	users, total, err := s.adm.admin.SCIMListUsers(r.Context(), a, f, start, count)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	items := make([]any, len(users))
	for i := range users {
		items[i] = s.scimUserJSON(&users[i])
	}
	scimList(w, total, start, items)
}

func (s *Server) scimGetUser(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	u, err := s.adm.admin.SCIMGetUser(r.Context(), a, r.PathValue("id"))
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimUserJSON(u))
}

func (s *Server) scimCreateUser(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	u := admin.SCIMUser{Active: true}
	if err := applyUserObject(&u, m); err != nil {
		s.scimError(w, r, err)
		return
	}
	out, err := s.adm.admin.SCIMCreateUser(r.Context(), a, u)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	w.Header().Set("Location", s.scimLocation("Users", out.ID))
	scimWrite(w, http.StatusCreated, s.scimUserJSON(out))
}

func (s *Server) scimPutUser(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	cur, err := s.adm.admin.SCIMGetUser(r.Context(), a, r.PathValue("id"))
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	u := *cur
	if err := applyUserObject(&u, m); err != nil {
		s.scimError(w, r, err)
		return
	}
	out, err := s.adm.admin.SCIMReplaceUser(r.Context(), a, cur.ID, u)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimUserJSON(out))
}

type scimOp struct {
	Op    string
	Path  string
	Value any
}

func scimOps(m map[string]any) ([]scimOp, error) {
	raw, _ := m["Operations"].([]any)
	if raw == nil {
		raw, _ = m["operations"].([]any)
	}
	if len(raw) == 0 {
		return nil, &admin.SCIMError{Status: 400, SCIMType: "invalidSyntax", Detail: "servono le Operations della PatchOp"}
	}
	out := make([]scimOp, 0, len(raw))
	for _, x := range raw {
		o, _ := x.(map[string]any)
		op := strings.ToLower(str(o["op"]))
		if op != "add" && op != "replace" && op != "remove" {
			return nil, &admin.SCIMError{Status: 400, SCIMType: "invalidSyntax", Detail: "operazione non valida: " + str(o["op"])}
		}
		out = append(out, scimOp{Op: op, Path: strings.TrimSpace(str(o["path"])), Value: o["value"]})
	}
	return out, nil
}

func (s *Server) scimPatchUser(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	ops, err := scimOps(m)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	cur, err := s.adm.admin.SCIMGetUser(r.Context(), a, r.PathValue("id"))
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	u := *cur
	for _, o := range ops {
		if o.Path == "" {
			obj, ok := o.Value.(map[string]any)
			if !ok {
				s.scimError(w, r, &admin.SCIMError{Status: 400, SCIMType: "invalidValue", Detail: "senza path il valore deve essere un oggetto"})
				return
			}
			if err := applyUserObject(&u, obj); err != nil {
				s.scimError(w, r, err)
				return
			}
			continue
		}
		path := strings.ToLower(strings.TrimPrefix(o.Path, scimUserSchema+":"))
		if err := applyUserAttr(&u, path, o.Value, o.Op == "remove"); err != nil {
			s.scimError(w, r, err)
			return
		}
	}
	out, err := s.adm.admin.SCIMReplaceUser(r.Context(), a, cur.ID, u)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimUserJSON(out))
}

func (s *Server) scimDeleteUser(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	if err := s.adm.admin.SCIMDeleteUser(r.Context(), a, r.PathValue("id")); err != nil {
		s.scimError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// Gruppi
// ---------------------------------------------------------------------

func (s *Server) scimGroupJSON(g *admin.SCIMGroup, members bool) map[string]any {
	out := map[string]any{
		"schemas": []string{scimGroupSchema}, "id": g.ID, "displayName": g.DisplayName, "externalId": g.ExternalID,
		"meta": map[string]any{"resourceType": "Group", "created": g.Created.UTC().Format(time.RFC3339),
			"lastModified": g.Modified.UTC().Format(time.RFC3339), "location": s.scimLocation("Groups", g.ID)},
	}
	if members {
		ms := make([]any, len(g.Members))
		for i, m := range g.Members {
			ms[i] = map[string]any{"value": m.Value, "display": m.Display, "$ref": s.scimLocation("Users", m.Value)}
		}
		out["members"] = ms
	}
	return out
}

func wantMembers(r *http.Request) bool {
	ex := strings.ToLower(r.URL.Query().Get("excludedAttributes"))
	return !strings.Contains(ex, "members")
}

func memberRefs(v any) []admin.SCIMRef {
	list, _ := v.([]any)
	if m, ok := v.(map[string]any); ok {
		list = []any{m}
	}
	out := []admin.SCIMRef{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		if id := str(m["value"]); id != "" {
			out = append(out, admin.SCIMRef{Value: strings.ToLower(id)})
		}
	}
	return out
}

func applyGroupObject(g *admin.SCIMGroup, m map[string]any, add bool) {
	for k, v := range m {
		switch strings.ToLower(k) {
		case "displayname":
			g.DisplayName = str(v)
		case "externalid":
			g.ExternalID = str(v)
		case "members":
			if add {
				g.Members = mergeRefs(g.Members, memberRefs(v))
			} else {
				g.Members = memberRefs(v)
			}
		}
	}
}

func mergeRefs(cur, more []admin.SCIMRef) []admin.SCIMRef {
	seen := map[string]bool{}
	out := []admin.SCIMRef{}
	for _, r := range append(append([]admin.SCIMRef{}, cur...), more...) {
		k := strings.ToLower(r.Value)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func dropRefs(cur []admin.SCIMRef, ids map[string]bool) []admin.SCIMRef {
	out := []admin.SCIMRef{}
	for _, r := range cur {
		if !ids[strings.ToLower(r.Value)] {
			out = append(out, r)
		}
	}
	return out
}

var scimMemberPathRe = regexp.MustCompile(`(?i)^members\[\s*value\s+eq\s+"([^"]+)"\s*\]$`)

func (s *Server) scimListGroups(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	f, err := scimParseFilter(r.URL.Query().Get("filter"))
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	start, count := scimPage(r)
	members := wantMembers(r)
	groups, total, err := s.adm.admin.SCIMListGroups(r.Context(), a, f, start, count, members)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	items := make([]any, len(groups))
	for i := range groups {
		items[i] = s.scimGroupJSON(&groups[i], members)
	}
	scimList(w, total, start, items)
}

func (s *Server) scimGetGroup(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	members := wantMembers(r)
	g, err := s.adm.admin.SCIMGetGroup(r.Context(), a, r.PathValue("id"), members)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimGroupJSON(g, members))
}

func (s *Server) scimCreateGroup(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	var g admin.SCIMGroup
	applyGroupObject(&g, m, false)
	out, err := s.adm.admin.SCIMCreateGroup(r.Context(), a, g)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	w.Header().Set("Location", s.scimLocation("Groups", out.ID))
	scimWrite(w, http.StatusCreated, s.scimGroupJSON(out, true))
}

func (s *Server) scimPutGroup(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	cur, err := s.adm.admin.SCIMGetGroup(r.Context(), a, r.PathValue("id"), true)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	g := *cur
	g.Members = []admin.SCIMRef{}
	applyGroupObject(&g, m, false)
	out, err := s.adm.admin.SCIMReplaceGroup(r.Context(), a, cur.ID, g)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimGroupJSON(out, true))
}

func (s *Server) scimPatchGroup(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	m, ok := scimBody(w, r)
	if !ok {
		return
	}
	ops, err := scimOps(m)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	cur, err := s.adm.admin.SCIMGetGroup(r.Context(), a, r.PathValue("id"), true)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	g := *cur
	for _, o := range ops {
		path := strings.TrimPrefix(o.Path, scimGroupSchema+":")
		lp := strings.ToLower(path)
		switch {
		case path == "":
			obj, ok := o.Value.(map[string]any)
			if !ok {
				s.scimError(w, r, &admin.SCIMError{Status: 400, SCIMType: "invalidValue", Detail: "senza path il valore deve essere un oggetto"})
				return
			}
			applyGroupObject(&g, obj, o.Op == "add")
		case lp == "displayname":
			if o.Op != "remove" {
				g.DisplayName = str(o.Value)
			}
		case lp == "externalid":
			// L'identificativo esterno non cambia: le mappature lo usano.
		case lp == "members":
			switch o.Op {
			case "add":
				g.Members = mergeRefs(g.Members, memberRefs(o.Value))
			case "replace":
				g.Members = memberRefs(o.Value)
			case "remove":
				if o.Value == nil {
					g.Members = []admin.SCIMRef{}
				} else {
					ids := map[string]bool{}
					for _, ref := range memberRefs(o.Value) {
						ids[ref.Value] = true
					}
					g.Members = dropRefs(g.Members, ids)
				}
			}
		case scimMemberPathRe.MatchString(path):
			id := strings.ToLower(scimMemberPathRe.FindStringSubmatch(path)[1])
			if o.Op == "remove" {
				g.Members = dropRefs(g.Members, map[string]bool{id: true})
			} else {
				g.Members = mergeRefs(g.Members, []admin.SCIMRef{{Value: id}})
			}
		default:
			s.scimError(w, r, &admin.SCIMError{Status: 400, SCIMType: "invalidPath", Detail: "percorso non supportato: " + o.Path})
			return
		}
	}
	out, err := s.adm.admin.SCIMReplaceGroup(r.Context(), a, cur.ID, g)
	if err != nil {
		s.scimError(w, r, err)
		return
	}
	scimWrite(w, http.StatusOK, s.scimGroupJSON(out, true))
}

func (s *Server) scimDeleteGroup(w http.ResponseWriter, r *http.Request, a admin.SCIMActor) {
	if err := s.adm.admin.SCIMDeleteGroup(r.Context(), a, r.PathValue("id")); err != nil {
		s.scimError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
