// Package fakealm è un finto OpenText ALM/Quality Center in memoria, con dati inventati, per
// provare il connettore DevSecOps senza un server vero (regia dev fake-alm) e per i test.
//
// Copre il poco che il connettore usa, sotto /qcbin:
//   - accesso con alm-authenticate (utente e password, oppure API key come da ALM 15.00) e con
//     rest/oauth2/login (API key come da ALM 12.60); OnlyOAuthAPIKeys simula le versioni in cui
//     alm-authenticate non accetta l'API key;
//   - rest/site-session (apre, chiude), authentication-point/logout, rest/domains;
//   - lettura delle collezioni di un progetto in JSON, con query (condizioni unite da «;»:
//     campo[valore], campo[>=valore], campo[a or b], valori tra virgolette o no), fields,
//     order-by, page-size e start-index;
//   - un campo sconosciuto in fields o in query risponde 400, come ALM;
//   - RequireXSRF simula ALM 16.00 e successivi: le chiamate non GET senza X-XSRF-TOKEN = 401.
//
// Le collezioni negate rispondono 403. Non accetta scritture sulle entità: come il connettore,
// legge soltanto.
package fakealm

import (
	"cmp"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Layout è il formato delle date di ALM (ora del server).
const Layout = "2006-01-02 15:04:05"

// Server è il finto ALM.
type Server struct {
	mu       sync.Mutex
	users    map[string]string // utente → password
	keys     map[string]string // client id → segreto
	auth     map[string]bool   // LWSSO_COOKIE_KEY validi
	sessions map[string]bool   // QCSession validi
	xsrf     map[string]bool
	// projects: "DOMINIO/PROGETTO" → collezione → entità.
	projects map[string]map[string][]map[string]string
	denied   map[string]bool
	seq      int
	// Loc è il fuso del server finto (le date si scrivono in quell'ora).
	Loc *time.Location
	Now func() time.Time
	// OnlyOAuthAPIKeys: l'API key entra solo da rest/oauth2/login (ALM 12.60 - 14.x).
	OnlyOAuthAPIKeys bool
	// RequireXSRF: le chiamate non GET vogliono X-XSRF-TOKEN (ALM 16.00 e successivi).
	RequireXSRF bool
	Requests    int
	Logins      int
	Logouts     int
	// Queries sono i filtri ricevuti, per collezione (per i test).
	Queries map[string][]string
}

// New crea un finto ALM vuoto.
func New() *Server {
	return &Server{users: map[string]string{}, keys: map[string]string{}, auth: map[string]bool{}, sessions: map[string]bool{},
		xsrf: map[string]bool{}, projects: map[string]map[string][]map[string]string{}, denied: map[string]bool{},
		Loc: time.UTC, Now: time.Now, Queries: map[string][]string{}}
}

// AddUser aggiunge un utente di servizio.
func (s *Server) AddUser(user, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user] = password
}

// AddAPIKey aggiunge un'API key (client id e segreto).
func (s *Server) AddAPIKey(clientID, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[clientID] = secret
}

// AddProject crea un progetto vuoto.
func (s *Server) AddProject(domain, project string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := domain + "/" + project
	if s.projects[k] == nil {
		s.projects[k] = map[string][]map[string]string{}
	}
}

// Deny fa rispondere 403 a una collezione di tutti i progetti.
func (s *Server) Deny(collection string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.denied[collection] = true
}

// Time scrive una data nell'ora del server finto.
func (s *Server) Time(t time.Time) string { return t.In(s.Loc).Format(Layout) }

// Put inserisce o aggiorna (per id) un'entità di una collezione e ne restituisce l'id.
// last-modified prende l'ora corrente, se non indicato.
func (s *Server) Put(domain, project, collection string, ent map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := domain + "/" + project
	if s.projects[k] == nil {
		s.projects[k] = map[string][]map[string]string{}
	}
	e := map[string]string{}
	for f, v := range ent {
		e[f] = v
	}
	if e["last-modified"] == "" {
		e["last-modified"] = s.Now().In(s.Loc).Format(Layout)
	}
	if e["id"] == "" {
		s.seq++
		e["id"] = strconv.Itoa(1000 + s.seq)
	}
	list := s.projects[k][collection]
	for i, old := range list {
		if old["id"] == e["id"] {
			for f, v := range e {
				old[f] = v
			}
			list[i] = old
			return e["id"]
		}
	}
	s.projects[k][collection] = append(list, e)
	return e["id"]
}

// List restituisce una copia delle entità di una collezione (per i test).
func (s *Server) List(domain, project, collection string) []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]string
	for _, e := range s.projects[domain+"/"+project][collection] {
		c := map[string]string{}
		for k, v := range e {
			c[k] = v
		}
		out = append(out, c)
	}
	return out
}

// Handler è l'handler HTTP del finto (tutto sotto /qcbin).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /qcbin/authentication-point/alm-authenticate", s.almAuthenticate)
	mux.HandleFunc("POST /qcbin/rest/oauth2/login", s.oauthLogin)
	mux.HandleFunc("GET /qcbin/authentication-point/logout", s.logout)
	mux.HandleFunc("POST /qcbin/rest/site-session", s.openSession)
	mux.HandleFunc("DELETE /qcbin/rest/site-session", s.closeSession)
	mux.HandleFunc("GET /qcbin/rest/domains", s.domains)
	mux.HandleFunc("GET /qcbin/rest/domains/{domain}/projects/{project}/{collection}", s.collection)
	return mux
}

func fail(w http.ResponseWriter, status int, id, title string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"Id": id, "Title": title})
}

func (s *Server) newToken(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%d", prefix, s.seq)
}

func (s *Server) grant(w http.ResponseWriter) {
	tok := s.newToken("lwsso")
	s.auth[tok] = true
	s.Logins++
	http.SetCookie(w, &http.Cookie{Name: "LWSSO_COOKIE_KEY", Value: tok, Path: "/", HttpOnly: true})
	w.WriteHeader(http.StatusOK)
}

func (s *Server) almAuthenticate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	var in struct {
		User     string `xml:"user"`
		Password string `xml:"password"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err := xml.Unmarshal(raw, &in); err != nil {
		fail(w, http.StatusBadRequest, "qccore.general-error", "XML non valido")
		return
	}
	if p, ok := s.users[in.User]; ok && p == in.Password {
		s.grant(w)
		return
	}
	if p, ok := s.keys[in.User]; ok && p == in.Password && !s.OnlyOAuthAPIKeys {
		s.grant(w)
		return
	}
	fail(w, http.StatusUnauthorized, "qccore.authentication-failed", "Authentication failed")
}

func (s *Server) oauthLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	var in struct {
		ClientID string `json:"clientId"`
		Secret   string `json:"secret"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
	if p, ok := s.keys[in.ClientID]; ok && p == in.Secret {
		s.grant(w)
		return
	}
	fail(w, http.StatusUnauthorized, "qccore.authentication-failed", "Invalid client id or secret")
}

func (s *Server) cookie(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) checkXSRF(w http.ResponseWriter, r *http.Request) bool {
	if !s.RequireXSRF {
		return true
	}
	if t := r.Header.Get("X-XSRF-TOKEN"); t != "" && s.xsrf[t] && t == s.cookie(r, "XSRF-TOKEN") {
		return true
	}
	fail(w, http.StatusUnauthorized, "qccore.xsrf-token-missing", "XSRF token missing or invalid")
	return false
}

func (s *Server) openSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	if !s.auth[s.cookie(r, "LWSSO_COOKIE_KEY")] {
		fail(w, http.StatusUnauthorized, "qccore.session-has-expired", "Authentication required")
		return
	}
	sess, x := s.newToken("qcsession"), s.newToken("xsrf")
	s.sessions[sess], s.xsrf[x] = true, true
	http.SetCookie(w, &http.Cookie{Name: "QCSession", Value: sess, Path: "/", HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "ALM_USER", Value: "finto", Path: "/"})
	http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: x, Path: "/"})
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	if !s.checkXSRF(w, r) {
		return
	}
	delete(s.sessions, s.cookie(r, "QCSession"))
	s.Logouts++
	w.WriteHeader(http.StatusOK)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	delete(s.auth, s.cookie(r, "LWSSO_COOKIE_KEY"))
	w.WriteHeader(http.StatusOK)
}

// Expire chiude tutte le sessioni, come la scadenza per inattività (per i test).
func (s *Server) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]bool{}
}

// Open è il numero di sessioni aperte (per i test: il connettore le deve chiudere).
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) domains(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	if !s.sessions[s.cookie(r, "QCSession")] {
		fail(w, http.StatusUnauthorized, "qccore.session-has-expired", "Session has expired")
		return
	}
	type proj struct {
		Name string `json:"Name"`
	}
	type dom struct {
		Name     string `json:"Name"`
		Projects struct {
			Project []proj `json:"Project"`
		} `json:"Projects"`
	}
	byDomain := map[string]*dom{}
	var names []string
	for k := range s.projects {
		d, p, _ := strings.Cut(k, "/")
		if byDomain[d] == nil {
			byDomain[d] = &dom{Name: d}
			names = append(names, d)
		}
		byDomain[d].Projects.Project = append(byDomain[d].Projects.Project, proj{Name: p})
	}
	slices.Sort(names)
	out := []dom{}
	for _, n := range names {
		d := byDomain[n]
		slices.SortFunc(d.Projects.Project, func(a, b proj) int { return cmp.Compare(a.Name, b.Name) })
		out = append(out, *d)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"Domains": map[string]any{"Domain": out}})
}

// Campi che ogni collezione ha anche se nessuna entità li valorizza.
var baseFields = map[string][]string{
	"releases":         {"id", "name", "parent-id", "start-date", "end-date", "last-modified"},
	"release-cycles":   {"id", "name", "parent-id", "start-date", "end-date", "last-modified"},
	"test-set-folders": {"id", "name", "parent-id", "last-modified"},
	"test-sets":        {"id", "name", "parent-id", "status", "last-modified"},
	"test-instances":   {"id", "cycle-id", "test-id", "status", "exec-date", "exec-time", "assign-rcyc", "last-modified"},
	"runs":             {"id", "name", "cycle-id", "testcycl-id", "test-id", "status", "execution-date", "execution-time", "duration", "owner", "last-modified"},
	"tests":            {"id", "name", "last-modified"},
	"defects":          {"id", "name", "status", "severity", "detected-in-rel", "detected-in-rcyc", "last-modified"},
	"defect-links":     {"id", "first-endpoint-id", "second-endpoint-id", "second-endpoint-type", "last-modified"},
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	if !s.sessions[s.cookie(r, "QCSession")] {
		fail(w, http.StatusUnauthorized, "qccore.session-has-expired", "Session has expired")
		return
	}
	key := r.PathValue("domain") + "/" + r.PathValue("project")
	proj, ok := s.projects[key]
	if !ok {
		fail(w, http.StatusNotFound, "qccore.project-not-found", "Project "+key+" does not exist")
		return
	}
	coll := r.PathValue("collection")
	base, known := baseFields[coll]
	if !known {
		fail(w, http.StatusNotFound, "qccore.entity-not-found", "Unknown entity type "+coll)
		return
	}
	if s.denied[coll] {
		fail(w, http.StatusForbidden, "qccore.operation-forbidden", "Permission denied")
		return
	}
	schema := map[string]bool{}
	for _, f := range base {
		schema[f] = true
	}
	for _, e := range proj[coll] {
		for f := range e {
			schema[f] = true
		}
	}
	q := r.URL.Query()
	query := strings.TrimSuffix(strings.TrimPrefix(q.Get("query"), "{"), "}")
	s.Queries[coll] = append(s.Queries[coll], query)
	filter, err := parseQuery(query, schema)
	if err != nil {
		fail(w, http.StatusBadRequest, "qccore.general-error", err.Error())
		return
	}
	var fields []string
	if f := q.Get("fields"); f != "" {
		fields = strings.Split(f, ",")
		for _, f := range fields {
			if !schema[f] {
				fail(w, http.StatusBadRequest, "qccore.general-error", "Field "+f+" not found for entity "+coll)
				return
			}
		}
	}
	var out []map[string]string
	for _, e := range proj[coll] {
		if filter(e) {
			out = append(out, e)
		}
	}
	order := strings.TrimSuffix(strings.TrimPrefix(q.Get("order-by"), "{"), "}")
	if order == "" {
		order = "id[ASC]"
	}
	parts := strings.Split(order, ";")
	for i := len(parts) - 1; i >= 0; i-- {
		f, dir, _ := strings.Cut(strings.TrimSuffix(parts[i], "]"), "[")
		desc := strings.EqualFold(dir, "DESC")
		slices.SortStableFunc(out, func(a, b map[string]string) int {
			c := compare(a[f], b[f])
			if desc {
				return -c
			}
			return c
		})
	}
	start, _ := strconv.Atoi(q.Get("start-index"))
	size, _ := strconv.Atoi(q.Get("page-size"))
	if start < 1 {
		start = 1
	}
	if size <= 0 {
		size = 100
	}
	total := len(out)
	from := min(start-1, total)
	page := out[from:min(total, from+size)]
	type value struct {
		Value string `json:"value,omitempty"`
	}
	type field struct {
		Name   string  `json:"Name"`
		Values []value `json:"values"`
	}
	type entity struct {
		Fields []field `json:"Fields"`
		Type   string  `json:"Type"`
	}
	res := make([]entity, 0, len(page))
	for _, e := range page {
		var ent entity
		ent.Type = strings.TrimSuffix(coll, "s")
		names := fields
		if names == nil {
			for f := range e {
				names = append(names, f)
			}
			slices.Sort(names)
		}
		for _, f := range names {
			fl := field{Name: f, Values: []value{}}
			if v := e[f]; v != "" {
				fl.Values = append(fl.Values, value{Value: v})
			} else {
				fl.Values = append(fl.Values, value{}) // come ALM: un valore vuoto
			}
			ent.Fields = append(ent.Fields, fl)
		}
		res = append(res, ent)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entities": res, "TotalResults": total})
}

func compare(a, b string) int {
	x, e1 := strconv.ParseFloat(a, 64)
	y, e2 := strconv.ParseFloat(b, 64)
	if e1 == nil && e2 == nil {
		return cmp.Compare(x, y)
	}
	return cmp.Compare(a, b)
}

// parseQuery traduce un sottoinsieme della sintassi delle query di ALM: condizioni
// campo[espressione] unite da «;» (e). Un'espressione è un elenco di termini uniti da « or »;
// un termine è un valore (uguale), oppure un operatore (>=, <=, >, <, <>, =) e un valore;
// i valori possono stare tra virgolette doppie o semplici.
func parseQuery(q string, schema map[string]bool) (func(map[string]string) bool, error) {
	type cond func(map[string]string) bool
	var conds []cond
	q = strings.TrimSpace(q)
	for q != "" {
		open := strings.Index(q, "[")
		if open < 0 {
			return nil, fmt.Errorf("query non capita: %s", q)
		}
		field := strings.TrimSpace(strings.TrimPrefix(q[:open], ";"))
		end := strings.Index(q[open:], "]")
		if end < 0 {
			return nil, fmt.Errorf("query non chiusa: %s", q)
		}
		expr := q[open+1 : open+end]
		q = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q[open+end+1:]), ";"))
		if !schema[field] {
			return nil, fmt.Errorf("Field %s not found", field)
		}
		var terms []cond
		for _, t := range strings.Split(expr, " or ") {
			t = strings.TrimSpace(t)
			op := "="
			for _, o := range []string{">=", "<=", "<>", ">", "<", "="} {
				if strings.HasPrefix(t, o) {
					op, t = o, strings.TrimSpace(t[len(o):])
					break
				}
			}
			v := strings.Trim(t, `"'`)
			f := field
			switch op {
			case "=":
				terms = append(terms, func(e map[string]string) bool { return e[f] == v })
			case "<>":
				terms = append(terms, func(e map[string]string) bool { return e[f] != v })
			case ">=":
				terms = append(terms, func(e map[string]string) bool { return e[f] != "" && compare(e[f], v) >= 0 })
			case ">":
				terms = append(terms, func(e map[string]string) bool { return e[f] != "" && compare(e[f], v) > 0 })
			case "<=":
				terms = append(terms, func(e map[string]string) bool { return e[f] != "" && compare(e[f], v) <= 0 })
			case "<":
				terms = append(terms, func(e map[string]string) bool { return e[f] != "" && compare(e[f], v) < 0 })
			}
		}
		conds = append(conds, func(e map[string]string) bool {
			for _, t := range terms {
				if t(e) {
					return true
				}
			}
			return false
		})
	}
	return func(e map[string]string) bool {
		for _, c := range conds {
			if !c(e) {
				return false
			}
		}
		return true
	}, nil
}

// Demo carica dati inventati nel dominio QUALITA: il progetto Ordini_online (due test set del
// change CHG0045120, uno del CHG0045121) e il progetto Pagamenti (un test set del CHG0045120).
// Il numero di change sta nel campo user-01 del test set; le cartelle di primo livello del Test
// Lab e il campo user-02 portano il nome dell'applicazione.
func Demo(s *Server, now time.Time) {
	const dom = "QUALITA"
	at := func(d time.Duration) string { return s.Time(now.Add(-d)) }
	day := func(d time.Duration) string { return now.Add(-d).In(s.Loc).Format(time.DateOnly) }
	hm := func(d time.Duration) string { return now.Add(-d).In(s.Loc).Format("15:04:05") }
	old := at(20 * 24 * time.Hour)
	for _, p := range []string{"Ordini_online", "Pagamenti"} {
		s.AddProject(dom, p)
		rel := s.Put(dom, p, "releases", map[string]string{"name": "R2026.12", "parent-id": "1", "start-date": day(30 * 24 * time.Hour), "last-modified": old})
		c1 := s.Put(dom, p, "release-cycles", map[string]string{"name": "Ciclo 1 - Sistema", "parent-id": rel, "last-modified": old})
		c2 := s.Put(dom, p, "release-cycles", map[string]string{"name": "Ciclo 2 - Regressione", "parent-id": rel, "last-modified": old})
		app := map[string]string{"Ordini_online": "Ordini online", "Pagamenti": "Pagamenti"}[p]
		folder := s.Put(dom, p, "test-set-folders", map[string]string{"name": app, "parent-id": "0", "last-modified": old})
		sub := s.Put(dom, p, "test-set-folders", map[string]string{"name": "Rilascio dicembre", "parent-id": folder, "last-modified": old})
		tests := []string{}
		for i, n := range []string{"Login", "Carrello", "Pagamento con carta", "Reso", "Fattura PDF"} {
			tests = append(tests, s.Put(dom, p, "tests", map[string]string{"name": fmt.Sprintf("%s %02d", n, i+1), "last-modified": old}))
		}
		set := func(name, change string) string {
			return s.Put(dom, p, "test-sets", map[string]string{"name": name, "parent-id": sub, "status": "Open", "user-01": change,
				"user-02": app, "last-modified": at(3 * 24 * time.Hour)})
		}
		inst := func(setID, testID, cycle, status string, ago time.Duration) string {
			e := map[string]string{"cycle-id": setID, "test-id": testID, "status": status, "assign-rcyc": cycle, "last-modified": at(ago)}
			if status != "No Run" {
				e["exec-date"], e["exec-time"] = day(ago), hm(ago)
			}
			return s.Put(dom, p, "test-instances", e)
		}
		run := func(setID, instID, testID, status string, ago time.Duration) string {
			return s.Put(dom, p, "runs", map[string]string{"name": "Run_" + day(ago), "cycle-id": setID, "testcycl-id": instID, "test-id": testID,
				"status": status, "execution-date": day(ago), "execution-time": hm(ago), "duration": "240", "owner": "tester.finto", "last-modified": at(ago)})
		}
		if p == "Ordini_online" {
			sys := set("Sistema - ordini", "CHG0045120")
			for i, st := range []string{"Passed", "Passed", "Passed", "Failed", "Passed"} {
				id := inst(sys, tests[i], c1, st, time.Duration(48-i)*time.Hour)
				if st == "Failed" {
					run(sys, id, tests[i], "Failed", 50*time.Hour)
					r := run(sys, id, tests[i], "Failed", time.Duration(48-i)*time.Hour)
					d := s.Put(dom, p, "defects", map[string]string{"name": "Il reso non calcola lo sconto", "status": "Open", "severity": "3-High",
						"detected-in-rel": rel, "detected-in-rcyc": c1, "last-modified": at(40 * time.Hour)})
					s.Put(dom, p, "defect-links", map[string]string{"first-endpoint-id": d, "second-endpoint-id": r, "second-endpoint-type": "run", "last-modified": at(40 * time.Hour)})
					continue
				}
				run(sys, id, tests[i], st, time.Duration(48-i)*time.Hour)
			}
			reg := set("Regressione - ordini", "CHG0045120")
			for i, st := range []string{"Passed", "Passed", "Blocked", "No Run"} {
				id := inst(reg, tests[i], c2, st, time.Duration(24-i)*time.Hour)
				if st != "No Run" {
					run(reg, id, tests[i], st, time.Duration(24-i)*time.Hour)
				}
			}
			d := s.Put(dom, p, "defects", map[string]string{"name": "Etichetta del carrello tagliata", "status": "Closed", "severity": "1-Low",
				"detected-in-rcyc": c2, "last-modified": at(20 * time.Hour)})
			s.Put(dom, p, "defect-links", map[string]string{"first-endpoint-id": d, "second-endpoint-id": reg, "second-endpoint-type": "test-set", "last-modified": at(20 * time.Hour)})
			next := set("Sistema - ordini gennaio", "CHG0045121")
			for i := range 3 {
				inst(next, tests[i], c2, "No Run", 5*time.Hour)
			}
		} else {
			pay := set("Sistema - pagamenti", "CHG0045120")
			for i, st := range []string{"Passed", "Passed", "Passed"} {
				id := inst(pay, tests[i], c1, st, time.Duration(30-i)*time.Hour)
				run(pay, id, tests[i], st, time.Duration(30-i)*time.Hour)
			}
		}
	}
}
