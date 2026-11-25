package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LoadOptions descrive una misura di carico.
type LoadOptions struct {
	BaseURL     string
	Email       string
	Password    string
	Duration    time.Duration
	Concurrency int
	Elements    int // numero di elementi del tenant (per scegliere chiavi a caso)
	PID         int // processo del server di cui campionare la memoria (0 = no)
	// OnlyRequests: il tenant ha solo business request (dataset delle prime misure).
	OnlyRequests bool
	// Only: se non vuoto, solo gli scenari il cui nome contiene una di queste parole.
	Only []string
}

type scenario struct {
	name   string
	weight int
	budget time.Duration // sul p95
	run    func(c *client, r *rand.Rand) error
}

// warmers: per alcuni scenari, una visita a ciascuna variante della vista (es. le unità del
// cruscotto) prima della misura, come chi la usa già; il costo a freddo più alto va nella colonna
// «primo». Si riempie in modules, prima del riscaldamento.
var warmers = map[string]func(c *client) time.Duration{}

type client struct {
	base string
	http *http.Client
}

func (c *client) call(method, path string, body any) (map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return out, nil
}

func q(s string) string { return "/api/v1/elements?q=" + url.QueryEscape(s) }

// world: riferimenti del tenant di prova letti all'avvio della misura (spazi dei team,
// richiesta grande, unità organizzative). Vuoti sul dataset delle sole richieste.
type world struct {
	spaces []string
	big    string
	units  []string
}

func discover(c *client) world {
	var w world
	if sp, err := c.call("GET", "/api/v1/work/spaces", nil); err == nil {
		items, _ := sp["items"].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				if k, _ := m["key"].(string); k != "" {
					w.spaces = append(w.spaces, k)
				}
			}
		}
	}
	if res, err := c.call("GET", q(`tipo: business_request "`+BigRequestTitle+`"`), nil); err == nil {
		if items, _ := res["items"].([]any); len(items) > 0 {
			w.big, _ = items[0].(map[string]any)["key"].(string)
		}
	}
	if res, err := c.call("GET", "/api/v1/org-units", nil); err == nil {
		items, _ := res["items"].([]any)
		for _, it := range items {
			if n, _ := it.(map[string]any)["name"].(string); n != "" {
				w.units = append(w.units, n)
			}
		}
	}
	return w
}

// modules: scenari dei moduli (benchmark esteso del 2026-11-24).
func modules(o LoadOptions, w world) []scenario {
	if len(w.spaces) == 0 {
		return nil
	}
	key := func(r *rand.Rand) string {
		return "DEM-" + strconv.Itoa(r.IntN(int(float64(o.Elements)*RequestShare))+1)
	}
	get := func(path string) func(c *client, r *rand.Rand) error {
		return func(c *client, _ *rand.Rand) error { _, err := c.call("GET", path, nil); return err }
	}
	space := func(r *rand.Rand) string { return w.spaces[r.IntN(len(w.spaces))] }
	palette := "tipo: business_request, impact, application, logical_asset "
	out := []scenario{
		{"board dello sprint attivo", 3, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get("/api/v1/work/spaces/"+space(r)+"/board")(c, r)
		}},
		{"board con filtro", 4, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			f := []string{"priorita: Alta", "priorita: Bloccante, Alta", "-assegnatario: vuoto", "tipo: story"}
			return get("/api/v1/work/spaces/"+space(r)+"/board?sprint=tutti&q="+url.QueryEscape(f[r.IntN(len(f))]))(c, r)
		}},
		{"backlog", 4, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			// Come l'interfaccia: sprint interi e le prime 200 voci del backlog (le altre a richiesta).
			return get("/api/v1/work/spaces/"+space(r)+"/backlog?limit=200")(c, r)
		}},
		{"cruscotto", 3, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			path := "/api/v1/dashboard/portfolio"
			if len(w.units) > 0 && r.IntN(2) == 0 {
				path += "?unit=" + url.QueryEscape(w.units[r.IntN(len(w.units))])
			}
			return get(path)(c, r)
		}},
		{"inbox valutazioni", 3, 100 * time.Millisecond, get("/api/v1/reviews/inbox")},
		{"inbox requisiti", 3, 100 * time.Millisecond, get("/api/v1/requirements/inbox")},
		{"notifiche", 3, 100 * time.Millisecond, get("/api/v1/notifications")},
		{"contatore notifiche", 4, 100 * time.Millisecond, get("/api/v1/notifications/count")},
		// La pagina di dettaglio chiede in parallelo dettaglio, commenti, collegamenti,
		// osservatori, allegati e consegna; poi la storia (con l'id). Si misura il tempo totale.
		{"pagina di dettaglio completa", 6, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			k := key(r)
			paths := []string{"/api/v1/elements/" + k, "/api/v1/elements/" + k + "/comments", "/api/v1/elements/" + k + "/links",
				"/api/v1/elements/" + k + "/watch", "/api/v1/elements/" + k + "/attachments", "/api/v1/work/elements/" + k + "/delivery"}
			errs := make([]error, len(paths))
			var id string
			var wg sync.WaitGroup
			for i, p := range paths {
				wg.Add(1)
				go func() {
					defer wg.Done()
					res, err := c.call("GET", p, nil)
					errs[i] = err
					if i == 0 && err == nil {
						id, _ = res["element"].(map[string]any)["id"].(string)
					}
				}()
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					return err
				}
			}
			_, err := c.call("GET", "/api/v1/elements/"+id+"/activity", nil)
			return err
		}},
		{"roadmap", 2, 100 * time.Millisecond, get("/api/v1/demand/roadmap")},
		{"campo personalizzato numerico", 3, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			f := []string{"tipo: business_request utenti: >990 stato: aperta", "utenti: 100..120 ordina per: utenti desc", "tipo: business_request utenti: <5"}
			return get(q(f[r.IntN(len(f))]))(c, r)
		}},
		{"⌘K per parola", 4, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			words := []string{"regis*", "porta*", "applicazione*", "fornit*", "data wareh*", "contr*"}
			return get(q(palette+words[r.IntN(len(words))])+"&limit=8")(c, r)
		}},
		{"⌘K per chiave", 2, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get(q(palette+key(r))+"&limit=8")(c, r)
		}},
	}
	warmers["cruscotto"] = func(c *client) (worst time.Duration) {
		for _, u := range append([]string{""}, w.units...) {
			t0 := time.Now()
			path := "/api/v1/dashboard/portfolio"
			if u != "" {
				path += "?unit=" + url.QueryEscape(u)
			}
			if _, err := c.call("GET", path, nil); err == nil {
				worst = max(worst, time.Since(t0))
			}
		}
		return worst
	}
	if w.big != "" {
		out = append(out, scenario{"matrice requisiti 300×30", 2, 100 * time.Millisecond, // Come l'interfaccia: le prime 50 righe, il riepilogo è di tutta la matrice.
			get("/api/v1/elements/" + w.big + "/requirements?limit=50")})
	}
	return out
}

func scenarios(o LoadOptions, sponsor string) []scenario {
	nreq := o.Elements
	if !o.OnlyRequests {
		nreq = int(float64(o.Elements) * RequestShare)
	}
	key := func(r *rand.Rand) string { return "DEM-" + strconv.Itoa(r.IntN(nreq)+1) }
	get := func(path string) func(c *client, r *rand.Rand) error {
		return func(c *client, r *rand.Rand) error { _, err := c.call("GET", path, nil); return err }
	}
	words := []string{"registro fornitori", "data warehouse", "firma digitale", "portale clienti", "conservazione norma", "contact center"}
	states := []string{"Triage", "Valutazione", "Prioritizzazione", "Pianificazione", "In esecuzione"}
	return []scenario{
		{"dettaglio per chiave", 20, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			_, err := c.call("GET", "/api/v1/elements/"+key(r), nil)
			return err
		}},
		{"lista per tipo e stato", 15, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get(q("tipo: business_request stato: \""+states[r.IntN(len(states))]+"\""))(c, r)
		}},
		{"lista aperte per creazione", 10, 100 * time.Millisecond, get(q("stato: aperta ordina per: creazione"))},
		// run nil: gestito a parte, misura solo la decima pagina (le prime 9 danno il cursore).
		{"pagina 10 col cursore", 5, 100 * time.Millisecond, nil},
		{"ricerca full-text", 15, 200 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get(q(words[r.IntN(len(words))]))(c, r)
		}},
		{"ricerca + filtro stato", 10, 200 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get(q(words[r.IntN(len(words))]+" stato: Valutazione"))(c, r)
		}},
		{"ricerca termine raro", 5, 200 * time.Millisecond, func(c *client, r *rand.Rand) error {
			rare := []string{"zanzibar", "sanzioni", "blockchain", "ornitorinco"}
			return get(q(rare[r.IntN(len(rare))]))(c, r)
		}},
		{"ricerca + filtro selettivo", 5, 200 * time.Millisecond, func(c *client, r *rand.Rand) error {
			rare := []string{"Sospesa", "Ritirata", "Approvazione"}
			return get(q(words[r.IntN(len(words))]+" stato: "+rare[r.IntN(len(rare))]))(c, r)
		}},
		{"storia di un elemento", 10, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			d, err := c.call("GET", "/api/v1/elements/"+key(r), nil)
			if err != nil {
				return err
			}
			_, err = c.call("GET", "/api/v1/elements/"+d["element"].(map[string]any)["id"].(string)+"/activity", nil)
			return err
		}},
		{"inbox approvazioni", 5, 100 * time.Millisecond, get("/api/v1/approvals/inbox")},
		// Linguaggio di query completo e autocompletamento (piano, passo 16).
		{"campo personalizzato", 4, 100 * time.Millisecond, get(q("tipo: business_request urgenza: Alta stato: aperta"))},
		{"negazione + data relativa", 3, 100 * time.Millisecond, get(q("-urgenza: Bassa aggiornata: -30g"))},
		{"persona (0,3% degli elementi)", 3, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			return get(q(fmt.Sprintf(`segnalatore: "Persona %03d"`, r.IntN(300))))(c, r)
		}},
		{"osservati: me", 2, 100 * time.Millisecond, get(q("osservati: me stato: aperta"))},
		{"ordina per fase", 3, 100 * time.Millisecond, get(q("tipo: business_request stato: aperta ordina per: fase"))},
		{"ordina per campo a elenco", 3, 100 * time.Millisecond, get(q("tipo: business_request ordina per: urgenza desc"))},
		{"ordina per titolo", 2, 100 * time.Millisecond, get(q("stato: aperta ordina per: titolo"))},
		{"ricerca per prefisso", 3, 200 * time.Millisecond, func(c *client, r *rand.Rand) error {
			pre := []string{"regis*", "fornit*", "porta* clienti", "wareh*"}
			return get(q(pre[r.IntN(len(pre))]))(c, r)
		}},
		{"autocompletamento", 5, 30 * time.Millisecond, func(c *client, r *rand.Rand) error {
			qs := []string{"tipo: business_request fase: ", "urg", "urgenza: A", "segnalatore: Persona 01", "ordina per: "}
			s := qs[r.IntN(len(qs))]
			return get("/api/v1/query/suggest?q="+url.QueryEscape(s)+"&pos="+strconv.Itoa(len(s)))(c, r)
		}},
		{"nuova richiesta + invio", 10, 100 * time.Millisecond, func(c *client, r *rand.Rand) error {
			d, err := c.call("POST", "/api/v1/demand/requests", map[string]any{
				"title": "Richiesta di carico " + strconv.Itoa(r.IntN(1_000_000)), "sponsor_id": sponsor,
				"description": "Creata dal benchmark.", "fields": map[string]any{"urgenza": "Media"},
			})
			if err != nil {
				return err
			}
			_, err = c.call("POST", "/api/v1/elements/"+d["id"].(string)+"/transitions", map[string]any{"to": "Inviata"})
			return err
		}},
	}
}

// Result è l'esito di uno scenario.
type Result struct {
	Name     string
	Count    int
	Errors   int
	P50, P95 time.Duration
	P99, Max time.Duration
	First    time.Duration // prima chiamata, a freddo
	Budget   time.Duration
	FirstErr string
}

func (r Result) OK() bool { return r.Errors == 0 && r.P95 <= r.Budget }

// Report è l'esito della misura.
type Report struct {
	Results    []Result
	Throughput float64
	MaxRSSMB   float64
	Duration   time.Duration
}

// Load esegue gli scenari in parallelo per la durata indicata.
func Load(ctx context.Context, o LoadOptions) (*Report, error) {
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{MaxIdleConnsPerHost: o.Concurrency * 8}
	c := &client{base: o.BaseURL, http: &http.Client{Jar: jar, Transport: tr, Timeout: 30 * time.Second}}
	me, err := c.call("POST", "/api/v1/auth/login", map[string]string{"email": o.Email, "password": o.Password})
	if err != nil {
		return nil, err
	}
	sponsor := me["active_tenant"].(map[string]any)["member_id"].(string)
	scs := scenarios(o, sponsor)
	if !o.OnlyRequests {
		scs = append(scs, modules(o, discover(c))...)
	}
	if len(o.Only) > 0 {
		var keep []scenario
		for _, s := range scs {
			for _, w := range o.Only {
				if strings.Contains(s.name, w) {
					keep = append(keep, s)
					break
				}
			}
		}
		scs = keep
	}
	if len(scs) == 0 {
		return nil, fmt.Errorf("nessuno scenario da eseguire")
	}
	total := 0
	for _, s := range scs {
		total += s.weight
	}

	var mu sync.Mutex
	lat := map[string][]time.Duration{}
	errs := map[string]int{}
	firstErr := map[string]string{}
	record := func(name string, d time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs[name]++
			if firstErr[name] == "" {
				firstErr[name] = err.Error()
			}
			return
		}
		lat[name] = append(lat[name], d)
	}

	rss := 0.0
	stopRSS := make(chan struct{})
	if o.PID > 0 {
		go func() {
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stopRSS:
					return
				case <-t.C:
					out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(o.PID)).Output()
					if err == nil {
						if kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil && kb/1024 > rss {
							mu.Lock()
							rss = kb / 1024
							mu.Unlock()
						}
					}
				}
			}
		}()
	}

	// Riscaldamento: ogni scenario gira due volte prima della misura, da solo. La prima chiamata
	// (cache fredda: piani, buffer di Postgres, viste in cache) si riporta a parte, nella colonna
	// «primo»; p50 e p95 sono quelli a regime.
	cold := map[string]time.Duration{}
	wr := rand.New(rand.NewPCG(99, 7))
	for _, s := range scs {
		if s.run == nil {
			continue
		}
		for i := 0; i < 2 && ctx.Err() == nil; i++ {
			t0 := time.Now()
			if err := s.run(c, wr); err != nil {
				break
			}
			if i == 0 {
				cold[s.name] = time.Since(t0)
			}
		}
		if warm := warmers[s.name]; warm != nil && ctx.Err() == nil {
			cold[s.name] = max(cold[s.name], warm(c))
		}
	}

	deadline := time.Now().Add(o.Duration)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < o.Concurrency; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(seed, 7))
			for time.Now().Before(deadline) && ctx.Err() == nil {
				pick := r.IntN(total)
				var s scenario
				for _, s = range scs {
					if pick < s.weight {
						break
					}
					pick -= s.weight
				}
				if s.run == nil { // decima pagina col cursore
					path := q("tipo: business_request")
					var err error
					var d time.Duration
					for p := 0; p < 10 && err == nil; p++ {
						t0 := time.Now()
						var res map[string]any
						res, err = c.call("GET", path, nil)
						d = time.Since(t0)
						if err == nil {
							next, _ := res["next"].(string)
							if next == "" {
								err = fmt.Errorf("pagine finite prima della decima")
							}
							path = q("tipo: business_request") + "&cursor=" + url.QueryEscape(next)
						}
					}
					record(s.name, d, err)
					continue
				}
				t0 := time.Now()
				err := s.run(c, r)
				record(s.name, time.Since(t0), err)
			}
		}(uint64(w + 1))
	}
	wg.Wait()
	close(stopRSS)
	elapsed := time.Since(start)

	rep := &Report{Duration: elapsed, MaxRSSMB: rss}
	n := 0
	for _, s := range scs {
		d := lat[s.name]
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		pct := func(p float64) time.Duration {
			if len(d) == 0 {
				return 0
			}
			return d[min(len(d)-1, int(float64(len(d))*p))]
		}
		res := Result{Name: s.name, Count: len(d), Errors: errs[s.name], Budget: s.budget, FirstErr: firstErr[s.name], First: cold[s.name],
			P50: pct(0.50), P95: pct(0.95), P99: pct(0.99)}
		if len(d) > 0 {
			res.Max = d[len(d)-1]
		}
		n += len(d)
		rep.Results = append(rep.Results, res)
	}
	rep.Throughput = float64(n) / elapsed.Seconds()
	return rep, nil
}

// Print scrive il report in forma di tabella.
func (r *Report) Print(w io.Writer) bool {
	ok := true
	fmt.Fprintf(w, "%-32s %7s %6s %9s %9s %9s %9s %9s %8s  %s\n", "scenario", "n", "errori", "p50", "p95", "p99", "max", "primo", "budget", "esito")
	for _, x := range r.Results {
		esito := "OK"
		if !x.OK() {
			esito, ok = "FUORI BUDGET", false
		}
		fmt.Fprintf(w, "%-32s %7d %6d %9s %9s %9s %9s %9s %8s  %s\n", x.Name, x.Count, x.Errors,
			ms(x.P50), ms(x.P95), ms(x.P99), ms(x.Max), ms(x.First), ms(x.Budget), esito)
		if x.FirstErr != "" {
			fmt.Fprintf(w, "    primo errore: %.200s\n", x.FirstErr)
		}
	}
	fmt.Fprintf(w, "\nrichieste al secondo: %.0f in %s", r.Throughput, r.Duration.Round(time.Second))
	if r.MaxRSSMB > 0 {
		fmt.Fprintf(w, " · memoria massima del server: %.1f MB (budget 50 MB)", r.MaxRSSMB)
		if r.MaxRSSMB > 50 {
			ok = false
		}
	}
	fmt.Fprintln(w)
	return ok
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }
