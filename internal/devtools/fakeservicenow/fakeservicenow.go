// Package fakeservicenow è un finto ServiceNow in memoria, con dati inventati, per provare
// il connettore DevSecOps senza un'istanza vera (regia dev fake-servicenow) e per i test.
//
// Copre il poco che il connettore usa: la Table API in lettura (/api/now/table/{tabella})
// con un sottoinsieme delle encoded query (=, !=, IN, >=, >, <=, <, ^, ^OR, ORDERBY,
// ORDERBYDESC), sysparm_fields, sysparm_limit e sysparm_offset; l'autenticazione Basic e
// OAuth client credentials (/oauth_token.do). Le tabelle negate rispondono 403 come una
// ACL mancante. Non accetta scritture: come il connettore, legge soltanto.
package fakeservicenow

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Layout è il formato delle date della Table API (UTC).
const Layout = "2006-01-02 15:04:05"

// Server è il finto ServiceNow.
type Server struct {
	mu       sync.Mutex
	users    map[string]string // utente → password
	clients  map[string]string // client_id → segreto
	tokens   map[string]time.Time
	tables   map[string][]map[string]string
	denied   map[string]bool
	seq      int
	Now      func() time.Time
	Requests int
	// Queries sono le encoded query ricevute, per tabella (per i test).
	Queries map[string][]string
}

// New crea un finto ServiceNow vuoto.
func New() *Server {
	return &Server{users: map[string]string{}, clients: map[string]string{}, tokens: map[string]time.Time{},
		tables: map[string][]map[string]string{}, denied: map[string]bool{}, Now: time.Now, Queries: map[string][]string{}}
}

// AddUser aggiunge un utente per l'autenticazione Basic.
func (s *Server) AddUser(user, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user] = password
}

// AddClient aggiunge un client OAuth (client credentials).
func (s *Server) AddClient(id, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[id] = secret
}

// Deny fa rispondere 403 a una tabella, come un'ACL di lettura mancante.
func (s *Server) Deny(table string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.denied[table] = true
}

// SysID genera un sys_id finto di 32 caratteri esadecimali.
func (s *Server) sysID() string {
	s.seq++
	return fmt.Sprintf("%032x", s.seq)
}

// Put inserisce o aggiorna un record (per sys_id, se c'è) e restituisce il sys_id.
// sys_updated_on prende l'ora corrente, se non indicato.
func (s *Server) Put(table string, rec map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := map[string]string{}
	for k, v := range rec {
		r[k] = v
	}
	if r["sys_updated_on"] == "" {
		r["sys_updated_on"] = s.Now().UTC().Format(Layout)
	}
	if r["sys_id"] == "" {
		r["sys_id"] = s.sysID()
	}
	for i, old := range s.tables[table] {
		if old["sys_id"] == r["sys_id"] {
			for k, v := range r {
				old[k] = v
			}
			s.tables[table][i] = old
			return r["sys_id"]
		}
	}
	if r["sys_created_on"] == "" {
		r["sys_created_on"] = r["sys_updated_on"]
	}
	s.tables[table] = append(s.tables[table], r)
	return r["sys_id"]
}

// Handler è l'handler HTTP del finto.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth_token.do", s.token)
	mux.HandleFunc("GET /api/now/table/{table}", s.table)
	return mux
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Form.Get("grant_type") != "client_credentials" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type","error_description":"solo client_credentials"}`))
		return
	}
	sec, ok := s.clients[r.Form.Get("client_id")]
	if !ok || sec != r.Form.Get("client_secret") {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"client sconosciuto o segreto errato"}`))
		return
	}
	s.seq++
	tok := fmt.Sprintf("finto-token-%d", s.seq)
	s.tokens[tok] = s.Now().Add(30 * time.Minute)
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": 1800, "scope": "useraccount"})
}

func (s *Server) authorized(r *http.Request) bool {
	if u, p, ok := r.BasicAuth(); ok {
		want, known := s.users[u]
		return known && want == p
	}
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		exp, known := s.tokens[tok]
		return known && s.Now().Before(exp)
	}
	return false
}

func fail(w http.ResponseWriter, status int, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "detail": detail}, "status": "failure"})
}

func (s *Server) table(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	if !s.authorized(r) {
		fail(w, http.StatusUnauthorized, "User Not Authenticated", "Required to provide Auth information")
		return
	}
	name := r.PathValue("table")
	if s.denied[name] {
		fail(w, http.StatusForbidden, "User Not Authorized", "Insufficient rights to query records")
		return
	}
	rows, ok := s.tables[name]
	if !ok && !knownTable(name) {
		fail(w, http.StatusBadRequest, "Invalid table "+name, "")
		return
	}
	q := r.URL.Query()
	query := q.Get("sysparm_query")
	s.Queries[name] = append(s.Queries[name], query)
	filter, order, err := parse(query)
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid query", err.Error())
		return
	}
	var out []map[string]string
	for _, row := range rows {
		if filter(row) {
			out = append(out, row)
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		o := order[i]
		slices.SortStableFunc(out, func(a, b map[string]string) int {
			c := cmp.Compare(a[o.field], b[o.field])
			if o.desc {
				return -c
			}
			return c
		})
	}
	offset, _ := strconv.Atoi(q.Get("sysparm_offset"))
	limit, _ := strconv.Atoi(q.Get("sysparm_limit"))
	if limit <= 0 {
		limit = 10000
	}
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:min(len(out), offset+limit)]
	var fields []string
	if f := q.Get("sysparm_fields"); f != "" {
		fields = strings.Split(f, ",")
	}
	result := make([]map[string]string, 0, len(out))
	for _, row := range out {
		rec := map[string]string{}
		if fields == nil {
			for k, v := range row {
				rec[k] = v
			}
		} else {
			for _, f := range fields {
				rec[f] = row[f] // i campi senza valore tornano vuoti, come in ServiceNow
			}
		}
		result = append(result, rec)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
}

func knownTable(name string) bool {
	return name == "incident" || name == "change_request" || strings.HasPrefix(name, "cmdb_ci")
}

type orderBy struct {
	field string
	desc  bool
}

var condRe = regexp.MustCompile(`^([a-z0-9_.]+)(>=|<=|!=|IN|=|>|<)(.*)$`)

// parse traduce un sottoinsieme delle encoded query: condizioni unite da ^ (e), ^OR (o con
// la condizione precedente), ORDERBY e ORDERBYDESC.
func parse(q string) (func(map[string]string) bool, []orderBy, error) {
	type cond func(map[string]string) bool
	var groups [][]cond // and di or
	var order []orderBy
	if strings.TrimSpace(q) == "" {
		return func(map[string]string) bool { return true }, nil, nil
	}
	for _, part := range strings.Split(q, "^") {
		if part == "" {
			continue
		}
		if f, ok := strings.CutPrefix(part, "ORDERBYDESC"); ok {
			order = append(order, orderBy{f, true})
			continue
		}
		if f, ok := strings.CutPrefix(part, "ORDERBY"); ok {
			order = append(order, orderBy{f, false})
			continue
		}
		or := false
		if p, ok := strings.CutPrefix(part, "OR"); ok && len(groups) > 0 {
			part, or = p, true
		}
		m := condRe.FindStringSubmatch(part)
		if m == nil {
			return nil, nil, fmt.Errorf("condizione non capita: %s", part)
		}
		field, op, val := m[1], m[2], m[3]
		var c cond
		switch op {
		case "=":
			c = func(r map[string]string) bool { return r[field] == val }
		case "!=":
			c = func(r map[string]string) bool { return r[field] != val }
		case "IN":
			set := strings.Split(val, ",")
			c = func(r map[string]string) bool { return slices.Contains(set, r[field]) }
		case ">=":
			c = func(r map[string]string) bool { return r[field] >= val }
		case ">":
			c = func(r map[string]string) bool { return r[field] > val }
		case "<=":
			c = func(r map[string]string) bool { return r[field] != "" && r[field] <= val }
		case "<":
			c = func(r map[string]string) bool { return r[field] != "" && r[field] < val }
		}
		if or {
			groups[len(groups)-1] = append(groups[len(groups)-1], c)
		} else {
			groups = append(groups, []cond{c})
		}
	}
	return func(r map[string]string) bool {
		for _, g := range groups {
			ok := false
			for _, c := range g {
				if c(r) {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		return true
	}, order, nil
}

// Demo carica dati inventati: tre servizi, alcuni incidenti P1-P3 e qualche change.
func Demo(s *Server, now time.Time) {
	at := func(d time.Duration) string { return now.Add(-d).UTC().Format(Layout) }
	svc := func(name, class, code string) string {
		return s.Put("cmdb_ci_service", map[string]string{"name": name, "sys_class_name": class, "u_codice": code, "operational_status": "1",
			"sys_updated_on": at(30 * 24 * time.Hour)})
	}
	ordini := svc("Ordini online", "cmdb_ci_service_auto", "ORD")
	pagamenti := svc("Pagamenti", "cmdb_ci_service_auto", "PAG")
	_ = svc("Posta interna", "cmdb_ci_service", "")
	inc := func(number, prio, state, title, service string, opened, resolved time.Duration) {
		rec := map[string]string{"number": number, "priority": prio, "state": state, "short_description": title,
			"business_service": service, "cmdb_ci": "", "opened_at": at(opened), "sys_updated_on": at(resolved)}
		if resolved > 0 && (state == "6" || state == "7") {
			rec["resolved_at"] = at(resolved)
		}
		if state == "7" {
			rec["closed_at"] = at(resolved)
		}
		s.Put("incident", rec)
	}
	inc("INC0010001", "1", "7", "Ordini online non raggiungibili", ordini, 20*24*time.Hour, 20*24*time.Hour-3*time.Hour)
	inc("INC0010002", "2", "6", "Pagamenti lenti nelle ore di punta", pagamenti, 6*24*time.Hour, 6*24*time.Hour-90*time.Minute)
	inc("INC0010003", "3", "2", "Errore di stampa delle fatture", ordini, 2*24*time.Hour, time.Hour)
	inc("INC0010004", "1", "2", "Carrello vuoto dopo il login", ordini, 5*time.Hour, 10*time.Minute)
	chg := func(number, state, approval, title, service string, start time.Duration) {
		s.Put("change_request", map[string]string{"number": number, "state": state, "approval": approval, "type": "normal",
			"short_description": title, "business_service": service, "start_date": at(start), "end_date": at(start - 2*time.Hour),
			"sys_updated_on": at(start + time.Hour)})
	}
	chg("CHG0030001", "3", "approved", "Rilascio ordini 2.4.0", ordini, 21*24*time.Hour)
	chg("CHG0030002", "-2", "approved", "Rilascio ordini 2.5.0", ordini, -24*time.Hour)
	chg("CHG0030003", "-3", "requested", "Nuovo fornitore di pagamento", pagamenti, -48*time.Hour)
}
