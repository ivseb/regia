package alm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/alm/almapi"
	"regia/internal/platform/db"
)

// Sincronizzazione: stesso schema degli altri connettori. Il job in-process (Run) è il
// meccanismo principale; «Sincronizza ora» avvia la stessa procedura.
//
//   - Per ogni progetto configurato: release e cicli (tutti: sono pochi), le cartelle del Test
//     Lab se l'applicazione si riconosce dalla cartella, poi test set, istanze di test,
//     esecuzioni e difetti modificati dopo il cursore (last-modified, ora del server ALM),
//     più i link dei difetti letti.
//   - I cursori stanno in connector_sync_state.cursors, per progetto ed entità; dati e cursore
//     si salvano nella stessa transazione. Rileggere non duplica nulla (source_key).
//   - Un test set citato da istanze o esecuzioni ma fuori dalla finestra si legge per id.
//   - Il numero di change e l'unità (quindi l'applicazione) si ricalcolano a ogni lettura del
//     test set; con il numero sul ciclo o sulla release, da ciclo e release del set (il ciclo
//     di un set è quello delle sue istanze, campo assign-rcyc).
//   - Una sessione ALM per sincronizzazione, chiusa alla fine (occupa una licenza).
//   - Una sola sincronizzazione per integrazione alla volta (lease nel database).

// Stats sono i numeri di una sincronizzazione.
type Stats struct {
	Projects    int      `json:"projects"`
	TestSets    int      `json:"test_sets"`
	NewTestSets int      `json:"new_test_sets"`
	Linked      int      `json:"linked"` // test set letti con un numero di change
	Items       int      `json:"items"`
	Runs        int      `json:"runs"`
	NewRuns     int      `json:"new_runs"`
	Defects     int      `json:"defects"`
	NewUnits    int      `json:"new_units,omitempty"`
	Requests    int      `json:"requests"`
	DurationMS  int64    `json:"duration_ms"`
	Errors      []string `json:"errors,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

func (st *Stats) warn(msg string) {
	if len(st.Warnings) < 30 && !slices.Contains(st.Warnings, msg) {
		st.Warnings = append(st.Warnings, msg)
	}
}

func (st *Stats) fail(msg string) {
	if len(st.Errors) < 30 {
		st.Errors = append(st.Errors, msg)
	}
}

// Run è una sincronizzazione nello storico.
type Run struct {
	ID         string     `json:"id"`
	Trigger    string     `json:"trigger"`
	Scope      string     `json:"scope"`
	Status     string     `json:"status"`
	Stats      Stats      `json:"stats"`
	Error      *string    `json:"error"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type claim struct {
	runID, tenantID, integrationID, trigger string
	full                                    bool
}

const leaseMinutes = 20

func (s *Service) claim(ctx context.Context, tenantID, integrationID, trigger string, full bool) (*claim, error) {
	c := &claim{runID: db.NewID(), tenantID: tenantID, integrationID: integrationID, trigger: trigger, full: full}
	scope := "tutto"
	if full {
		scope = "completa"
	}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var active bool
		err := tx.QueryRow(ctx, "select active from integrations where id = $1 and kind = 'alm'", integrationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !active {
			return &Error{Status: 409, Code: "integration.inactive", Msg: "integrazione disattivata: riattivala per sincronizzare"}
		}
		tag, err := tx.Exec(ctx, `insert into connector_sync_state (tenant_id, integration_id, lease_until, last_started_at, last_status)
			values (app_tenant(), $1, now() + make_interval(mins => $2), now(), 'running')
			on conflict (tenant_id, integration_id) do update set lease_until = excluded.lease_until, last_started_at = now(), last_status = 'running'
			where connector_sync_state.lease_until is null or connector_sync_state.lease_until < now()`, integrationID, leaseMinutes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrBusy
		}
		_, err = tx.Exec(ctx, `insert into connector_sync_runs (tenant_id, id, integration_id, trigger, scope, status)
			values (app_tenant(), $1, $2, $3, $4, 'running')`, c.runID, integrationID, trigger, scope)
		return err
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// SyncNow avvia una sincronizzazione in background e restituisce l'id dell'esecuzione.
func (s *Service) SyncNow(ctx context.Context, a Actor, integrationID string, full bool) (string, error) {
	if !validID(integrationID) {
		return "", ErrNotFound
	}
	c, err := s.claim(ctx, a.TenantID, integrationID, "manual", full)
	if err != nil {
		return "", err
	}
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.alm.sync", "integration", integrationID, map[string]any{"full": full, "run": c.runID})
	}); err != nil {
		return "", err
	}
	s.background(c)
	return c.runID, nil
}

// SyncWait esegue subito una sincronizzazione e aspetta la fine (test e strumenti).
func (s *Service) SyncWait(ctx context.Context, tenantID, integrationID string, full bool) error {
	c, err := s.claim(ctx, tenantID, integrationID, "manual", full)
	if err != nil {
		return err
	}
	s.execute(ctx, c)
	return nil
}

func (s *Service) background(c *claim) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*leaseMinutes*time.Minute)
		defer cancel()
		s.execute(ctx, c)
	}()
}

// Run esegue le sincronizzazioni dovute a intervalli regolari, finché ctx non è chiuso.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.WaitIdle()
			return
		case <-t.C:
			if n, err := s.RunDue(ctx); err != nil {
				s.o.Log.Warn("connettore ALM", "errore", err)
			} else if n > 0 {
				s.o.Log.Debug("connettore ALM: sincronizzazioni eseguite", "numero", n)
			}
		}
	}
}

// RunDue esegue, una dopo l'altra, le sincronizzazioni dovute di tutti i tenant.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	rows, err := s.o.DB.Owner.Query(ctx, `
		select i.tenant_id::text, i.id::text from integrations i
		left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
		where i.kind = 'alm' and i.active
		  and (st.next_run_at is null or st.next_run_at <= now())
		  and (st.lease_until is null or st.lease_until < now())
		order by st.next_run_at nulls first limit 20`)
	if err != nil {
		return 0, err
	}
	type due struct{ tenant, id string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		err := r.Scan(&d.tenant, &d.id)
		return d, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, d := range list {
		if ctx.Err() != nil {
			break
		}
		c, err := s.claim(ctx, d.tenant, d.id, "schedule", false)
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.execute(ctx, c)
		n++
	}
	return n, errors.Join(errs...)
}

func (s *Service) execute(ctx context.Context, c *claim) {
	start := s.o.Now()
	st := Stats{}
	status := "ok"
	var errMsg string
	var rateReset time.Time

	func() {
		var it *Integration
		var sec secrets
		var cursors map[string]map[string]string
		err := s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			var err error
			if it, sec, err = s.load(ctx, tx, c.integrationID); err != nil {
				return err
			}
			var raw []byte
			if err := tx.QueryRow(ctx, "select cursors from connector_sync_state where integration_id = $1", c.integrationID).Scan(&raw); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			_ = json.Unmarshal(raw, &cursors)
			return nil
		})
		if err != nil {
			status, errMsg = "error", err.Error()
			return
		}
		if cursors == nil {
			cursors = map[string]map[string]string{}
		}
		cl, err := s.client(it.Config, sec)
		if err != nil {
			status, errMsg = "error", "configurazione: "+err.Error()
			return
		}
		defer func() { st.Requests = cl.Requests() }()
		defer cl.Logout(ctx)
		stop := func(err error) bool {
			if reset, ok := almapi.IsRateLimited(err); ok {
				status, errMsg, rateReset = "rate_limited", err.Error(), reset
				return true
			}
			if code := almapi.StatusOf(err); code == 401 || code == 0 {
				status, errMsg = "error", err.Error() // credenziali, licenze o rete: inutile continuare
				return true
			}
			return false
		}
		pattern, err := regexp.Compile(it.Config.ChangePattern)
		if err != nil {
			status, errMsg = "error", "espressione del numero di change non valida: "+err.Error()
			return
		}
		for _, p := range it.Config.Projects {
			if ctx.Err() != nil {
				status, errMsg = "error", "sincronizzazione interrotta: "+ctx.Err().Error()
				return
			}
			st.Projects++
			pc := &projCtx{s: s, it: it, cl: cl, p: p, pattern: pattern, st: &st, full: c.full, tenantID: c.tenantID,
				prefix: it.ID + "/" + p.key() + "/", cur: cursors[p.key()]}
			if pc.cur == nil {
				pc.cur = map[string]string{}
			}
			if err := pc.run(ctx); err != nil {
				if stop(err) {
					return
				}
				st.fail(p.key() + ": " + err.Error())
			}
		}
		if err := s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			return linkDefects(ctx, tx, it)
		}); err != nil {
			st.fail("collegamento dei difetti ai change: " + err.Error())
		}
		if st.TestSets > 0 && st.Linked == 0 {
			where := "nel campo " + it.Config.ChangeField
			if it.Config.ChangeSource == ChangeTestSetName {
				where = "nel nome"
			}
			st.warn(fmt.Sprintf("nessun test set letto ha un numero di change riconoscibile %s: controlla la configurazione «Numero di change»", where))
		}
	}()

	if status == "ok" && len(st.Errors) > 0 {
		status = "partial"
		errMsg = st.Errors[0]
	}
	st.DurationMS = s.o.Now().Sub(start).Milliseconds()
	s.finish(context.WithoutCancel(ctx), c, st, status, errMsg, rateReset)
}

// MaxPerRun è il numero massimo di entità per collezione e progetto in una sincronizzazione:
// oltre, il cursore si ferma all'ultima letta e si riprende alla successiva.
const MaxPerRun = 5000

// projCtx è la lettura di un progetto.
type projCtx struct {
	s        *Service
	it       *Integration
	cl       *almapi.Client
	p        Project
	pattern  *regexp.Regexp
	st       *Stats
	full     bool
	tenantID string
	prefix   string            // chiave delle righe: <integrazione>/<dominio>/<progetto>/
	cur      map[string]string // cursori del progetto, per collezione

	releases map[string]named // id → release
	cycles   map[string]named // id → ciclo (parent = release)
	folders  map[string]named // id → cartella (parent = cartella madre)
}

type named struct{ name, parent, change string }

func (pc *projCtx) key(kind, id string) string { return pc.prefix + kind + "/" + id }

func (pc *projCtx) since(coll string) string {
	if c := pc.cur[coll]; !pc.full && c != "" {
		return c
	}
	return pc.cl.FormatTime(pc.s.o.Now().AddDate(0, 0, -pc.it.Config.LookbackDays))
}

// changeOf estrae il numero di change da un valore («CHG0045120», «CHG0045120 - note»).
func (pc *projCtx) changeOf(v string) string {
	if v = strings.TrimSpace(v); v == "" {
		return ""
	}
	return strings.ToUpper(truncate(pc.pattern.FindString(v), 120))
}

// read legge una collezione modificata dopo il cursore, in ordine di last-modified.
func (pc *projCtx) read(ctx context.Context, coll string, fields []string) ([]almapi.Entity, string, error) {
	from := pc.since(coll)
	var out []almapi.Entity
	err := pc.cl.Entities(ctx, almapi.Query{Domain: pc.p.Domain, Project: pc.p.Project, Collection: coll,
		Filter: "last-modified[>=" + almapi.Quote(from) + "]", Fields: fields, OrderBy: "last-modified[ASC];id[ASC]", Limit: MaxPerRun},
		func(e almapi.Entity) (bool, error) {
			out = append(out, e)
			return false, nil
		})
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", coll, err)
	}
	if len(out) >= MaxPerRun {
		pc.st.warn(fmt.Sprintf("%s: più di %d %s modificati: letti i primi, il resto alla prossima sincronizzazione", pc.p.key(), MaxPerRun, coll))
	}
	cursor := pc.cur[coll]
	if pc.full {
		cursor = ""
	}
	for _, e := range out {
		if lm := strings.TrimSpace(e["last-modified"]); lm > cursor {
			cursor = lm
		}
	}
	if cursor == "" {
		cursor = from
	}
	return out, cursor, nil
}

// byIDs legge le entità con gli id indicati (a blocchi di 100).
func (pc *projCtx) byIDs(ctx context.Context, coll string, ids []string, fields []string) ([]almapi.Entity, error) {
	var out []almapi.Entity
	for chunk := range slices.Chunk(ids, 100) {
		err := pc.cl.Entities(ctx, almapi.Query{Domain: pc.p.Domain, Project: pc.p.Project, Collection: coll,
			Filter: almapi.IDs("id", chunk), Fields: fields, Limit: len(chunk)}, func(e almapi.Entity) (bool, error) {
			out = append(out, e)
			return false, nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", coll, err)
		}
	}
	return out, nil
}

// all legge tutta una collezione piccola (release, cicli, cartelle).
func (pc *projCtx) all(ctx context.Context, coll string, fields []string) (map[string]named, error) {
	m := map[string]named{}
	cf := ""
	if (coll == "releases" && pc.it.Config.ChangeSource == ChangeReleaseField) || (coll == "release-cycles" && pc.it.Config.ChangeSource == ChangeCycleField) {
		cf = pc.it.Config.ChangeField
		fields = append(fields, cf)
	}
	err := pc.cl.Entities(ctx, almapi.Query{Domain: pc.p.Domain, Project: pc.p.Project, Collection: coll, Fields: fields, OrderBy: "id[ASC]", Limit: 20000},
		func(e almapi.Entity) (bool, error) {
			n := named{name: truncate(strings.TrimSpace(e["name"]), 200), parent: e["parent-id"]}
			if cf != "" {
				n.change = pc.changeOf(e[cf])
			}
			m[e["id"]] = n
			return false, nil
		})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", coll, err)
	}
	return m, nil
}

// topFolder risale alla cartella di primo livello del Test Lab (figlia della radice, id 0) e
// restituisce id, nome e percorso.
func (pc *projCtx) topFolder(id string) (string, string, string) {
	var path []string
	top, topName := "", ""
	for i := 0; i < 50 && id != "" && id != "0"; i++ {
		f, ok := pc.folders[id]
		if !ok {
			break
		}
		path = append(path, f.name)
		top, topName = id, f.name
		id = f.parent
	}
	slices.Reverse(path)
	return top, topName, strings.Join(path, "/")
}

var setFieldsBase = []string{"id", "name", "parent-id", "status", "last-modified"}

func (pc *projCtx) setFields() []string {
	f := slices.Clone(setFieldsBase)
	if pc.it.Config.ChangeSource == ChangeTestSetField {
		f = append(f, pc.it.Config.ChangeField)
	}
	if pc.it.Config.AppSource == AppTestSetField && pc.it.Config.AppField != pc.it.Config.ChangeField {
		f = append(f, pc.it.Config.AppField)
	}
	return f
}

var (
	instanceFields = []string{"id", "cycle-id", "test-id", "status", "exec-date", "exec-time", "assign-rcyc", "last-modified"}
	runFieldsBase  = []string{"id", "name", "cycle-id", "testcycl-id", "test-id", "status", "execution-date", "execution-time", "duration", "last-modified"}
	defectFields   = []string{"id", "name", "status", "severity", "detected-in-rel", "detected-in-rcyc", "last-modified"}
	linkFields     = []string{"id", "first-endpoint-id", "second-endpoint-id", "second-endpoint-type"}
)

func (pc *projCtx) run(ctx context.Context) error {
	var err error
	if pc.releases, err = pc.all(ctx, "releases", []string{"id", "name", "parent-id"}); err != nil {
		return err
	}
	if pc.cycles, err = pc.all(ctx, "release-cycles", []string{"id", "name", "parent-id"}); err != nil {
		return err
	}
	// Le cartelle servono all'unità (applicazione dalla cartella) e al percorso del test set.
	if pc.folders, err = pc.all(ctx, "test-set-folders", []string{"id", "name", "parent-id"}); err != nil {
		return err
	}
	sets, setCur, err := pc.read(ctx, "test-sets", pc.setFields())
	if err != nil {
		return err
	}
	insts, instCur, err := pc.read(ctx, "test-instances", instanceFields)
	if err != nil {
		return err
	}
	runFields := runFieldsBase
	if pc.it.Config.KeepTester {
		runFields = append(slices.Clone(runFields), "owner")
	}
	runs, runCur, err := pc.read(ctx, "runs", runFields)
	if err != nil {
		return err
	}
	defects, defCur, err := pc.read(ctx, "defects", defectFields)
	if err != nil {
		return err
	}
	// Test set citati da istanze ed esecuzioni ma non letti ora: se Regia non li ha, si leggono per id.
	have := map[string]bool{}
	for _, e := range sets {
		have[e["id"]] = true
	}
	var cited []string
	for _, list := range [][]almapi.Entity{insts, runs} {
		for _, e := range list {
			if id := e["cycle-id"]; id != "" && !have[id] {
				have[id] = true
				cited = append(cited, id)
			}
		}
	}
	if len(cited) > 0 {
		known := map[string]bool{}
		keys := make([]string, len(cited))
		for i, id := range cited {
			keys[i] = pc.key("set", id)
		}
		if err := pc.s.o.DB.InTenant(ctx, pc.tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "select source_key from test_sets where origin = 'alm' and source_key = any($1)", keys)
			if err != nil {
				return err
			}
			list, err := pgx.CollectRows(rows, pgx.RowTo[string])
			for _, k := range list {
				known[k] = true
			}
			return err
		}); err != nil {
			return err
		}
		var missing []string
		for _, id := range cited {
			if !known[pc.key("set", id)] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			more, err := pc.byIDs(ctx, "test-sets", missing, pc.setFields())
			if err != nil {
				return err
			}
			sets = append(sets, more...)
		}
	}
	// Nomi dei test delle istanze (solo quelli che Regia non conosce già).
	testNames, err := pc.testNames(ctx, insts)
	if err != nil {
		return err
	}
	// Link dei difetti letti.
	links := map[string][]almapi.Entity{}
	if len(defects) > 0 {
		ids := make([]string, len(defects))
		for i, d := range defects {
			ids[i] = d["id"]
		}
		for chunk := range slices.Chunk(ids, 100) {
			err := pc.cl.Entities(ctx, almapi.Query{Domain: pc.p.Domain, Project: pc.p.Project, Collection: "defect-links",
				Filter: almapi.IDs("first-endpoint-id", chunk), Fields: linkFields, Limit: 5000}, func(e almapi.Entity) (bool, error) {
				links[e["first-endpoint-id"]] = append(links[e["first-endpoint-id"]], e)
				return false, nil
			})
			if err != nil {
				if s := almapi.StatusOf(err); s == 400 || s == 403 || s == 404 {
					pc.st.warn(pc.p.key() + ": i link dei difetti non si leggono (" + err.Error() + "): i difetti contano solo dal ciclo o dalla release di rilevazione")
					break
				}
				return err
			}
		}
	}
	return pc.s.o.DB.InTenant(ctx, pc.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if err := pc.saveSets(ctx, tx, sets); err != nil {
			return err
		}
		if err := pc.saveItems(ctx, tx, insts, testNames); err != nil {
			return err
		}
		if err := pc.deriveSets(ctx, tx); err != nil {
			return err
		}
		if err := pc.saveRuns(ctx, tx, runs); err != nil {
			return err
		}
		if err := pc.saveDefects(ctx, tx, defects, links); err != nil {
			return err
		}
		cur, _ := json.Marshal(map[string]string{"test-sets": setCur, "test-instances": instCur, "runs": runCur, "defects": defCur})
		_, err := tx.Exec(ctx, `update connector_sync_state set cursors = cursors || jsonb_build_object($2::text, $3::jsonb) where integration_id = $1`,
			pc.it.ID, pc.p.key(), cur)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update connector_repositories set last_synced_at = now() where integration_id = $1 and missing_since is null`, pc.it.ID)
		return err
	})
}

func (pc *projCtx) testNames(ctx context.Context, insts []almapi.Entity) (map[string]string, error) {
	names := map[string]string{}
	var ids []string
	seen := map[string]bool{}
	for _, e := range insts {
		if id := e["test-id"]; id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return names, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = pc.key("test", id)
	}
	if err := pc.s.o.DB.InTenant(ctx, pc.tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select distinct on (test_key) test_key, test_name from test_set_items where test_key = any($1) and test_name <> ''`, keys)
		if err != nil {
			return err
		}
		var k, n string
		_, err = pgx.ForEachRow(rows, []any{&k, &n}, func() error {
			names[k] = n
			return nil
		})
		return err
	}); err != nil {
		return nil, err
	}
	var missing []string
	for _, id := range ids {
		if names[pc.key("test", id)] == "" {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		list, err := pc.byIDs(ctx, "tests", missing, []string{"id", "name"})
		if err != nil {
			if s := almapi.StatusOf(err); s == 403 {
				pc.st.warn(pc.p.key() + ": il Test Plan non si legge (403): i test compaiono con il loro id")
				return names, nil
			}
			return nil, err
		}
		for _, e := range list {
			names[pc.key("test", e["id"])] = truncate(strings.TrimSpace(e["name"]), 300)
		}
	}
	return names, nil
}

// ---------------------------------------------------------------------
// Salvataggio
// ---------------------------------------------------------------------

// Link all'interfaccia di ALM: il formato «td://» che ALM copia con «Copia URL» e che apre il
// client di ALM (o ALM Client Launcher).
func (pc *projCtx) link(module, entityType, id string) string {
	u, err := url.Parse(pc.it.Config.BaseURL)
	if err != nil {
		return ""
	}
	return "td://" + pc.p.Project + "." + pc.p.Domain + "." + u.Host + u.Path + "/" + module + "?EntityType=" + entityType + "&EntityID=" + id
}

const (
	testLabModule = "TestLabModule-000000004243046514"
	defectsModule = "DefectsModule-000000003649890581"
)

func (pc *projCtx) unitOf(e almapi.Entity) (unitRef, bool) {
	switch pc.it.Config.AppSource {
	case AppProject:
		return projectUnit(pc.p), true
	case AppFolder:
		if id, name, _ := pc.topFolder(e["parent-id"]); id != "" {
			return folderUnit(pc.p, id, name), true
		}
	case AppTestSetField:
		if v := strings.TrimSpace(e[pc.it.Config.AppField]); v != "" {
			return valueUnit(v), true
		}
	}
	return unitRef{}, false
}

func (pc *projCtx) saveSets(ctx context.Context, tx pgx.Tx, sets []almapi.Entity) error {
	var units []unitRef
	unitOf := make([]string, len(sets))
	for i, e := range sets {
		if u, ok := pc.unitOf(e); ok {
			units = append(units, u)
			unitOf[i] = u.external
		}
	}
	if pc.it.Config.AppSource == AppProject {
		units = append(units, projectUnit(pc.p))
	}
	m, created, err := pc.s.ensureUnits(ctx, tx, pc.it.ID, units)
	if err != nil {
		return err
	}
	pc.st.NewUnits += created
	for i, e := range sets {
		id, name := strings.TrimSpace(e["id"]), strings.Join(strings.Fields(e["name"]), " ")
		if id == "" || name == "" {
			continue
		}
		change := ""
		switch pc.it.Config.ChangeSource {
		case ChangeTestSetField:
			if v := e[pc.it.Config.ChangeField]; strings.TrimSpace(v) != "" {
				if change = pc.changeOf(v); change == "" {
					pc.st.warn(fmt.Sprintf("%s: il campo %s di alcuni test set ha un valore senza un numero di change riconoscibile (per esempio «%s»)",
						pc.p.key(), pc.it.Config.ChangeField, truncate(strings.TrimSpace(v), 40)))
				}
			}
		case ChangeTestSetName:
			change = pc.changeOf(name)
		}
		_, _, path := pc.topFolder(e["parent-id"])
		var repo, app any
		if u, ok := m[unitOf[i]]; ok && unitOf[i] != "" {
			repo = u.id
			if u.app != nil {
				app = *u.app
			}
		}
		keepChange := pc.it.Config.ChangeSource == ChangeCycleField || pc.it.Config.ChangeSource == ChangeReleaseField
		var inserted bool
		if err := tx.QueryRow(ctx, `
			insert into test_sets (tenant_id, id, origin, integration_id, source_key, external_id, project, name, folder, status, change_number,
			  repository_id, application_element_id, url, source_updated_at)
			values (app_tenant(), $1, 'alm', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			on conflict (tenant_id, origin, source_key) do update set name = excluded.name, folder = excluded.folder, status = excluded.status,
			  change_number = case when $14 then test_sets.change_number else excluded.change_number end,
			  repository_id = excluded.repository_id, application_element_id = excluded.application_element_id, url = excluded.url,
			  source_updated_at = excluded.source_updated_at, updated_at = now()
			returning (xmax = 0)`,
			db.NewID(), pc.it.ID, pc.key("set", id), truncate(id, 64), truncate(pc.p.key(), 200), truncate(name, 300), truncate(path, 500),
			truncate(e["status"], 40), change, repo, app, pc.link(testLabModule, "ITestSet", id), pc.cl.ParseTime(e["last-modified"]), keepChange).Scan(&inserted); err != nil {
			return err
		}
		pc.st.TestSets++
		if inserted {
			pc.st.NewTestSets++
		}
		if change != "" {
			pc.st.Linked++
		}
	}
	return nil
}

// setIDs restituisce source_key → id dei test set del progetto citati.
func (pc *projCtx) setIDs(ctx context.Context, tx pgx.Tx, almIDs []string) (map[string]string, error) {
	keys := make([]string, 0, len(almIDs))
	for _, id := range almIDs {
		keys = append(keys, pc.key("set", id))
	}
	rows, err := tx.Query(ctx, "select source_key, id::text from test_sets where origin = 'alm' and source_key = any($1)", keys)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	var k, id string
	_, err = pgx.ForEachRow(rows, []any{&k, &id}, func() error {
		m[k] = id
		return nil
	})
	return m, err
}

// NormStatus porta lo stato di ALM a quello di Regia: passed, failed, blocked, not_completed,
// no_run, na. Gli stati personalizzati che non si riconoscono valgono na.
func NormStatus(s string) string {
	v := strings.ToLower(strings.Join(strings.Fields(s), " "))
	switch v {
	case "passed", "pass", "superato", "passato":
		return "passed"
	case "failed", "fail", "fallito":
		return "failed"
	case "blocked", "bloccato":
		return "blocked"
	case "not completed", "non completato", "in progress":
		return "not_completed"
	case "no run", "", "non eseguito":
		return "no_run"
	case "n/a", "na", "not applicable":
		return "na"
	}
	switch {
	case strings.Contains(v, "pass"):
		return "passed"
	case strings.Contains(v, "fail"):
		return "failed"
	case strings.Contains(v, "block"):
		return "blocked"
	}
	return "na"
}

// NormSeverity porta la gravità di ALM (di solito «1-Low» … «5-Urgent») a critical, high,
// medium o low; "" se non si riconosce.
func NormSeverity(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ""
	}
	if n, err := strconv.Atoi(strings.SplitN(v, "-", 2)[0]); err == nil {
		switch {
		case n >= 5:
			return "critical"
		case n >= 3:
			return "high"
		case n == 2:
			return "medium"
		default:
			return "low"
		}
	}
	switch {
	case strings.Contains(v, "urgent") || strings.Contains(v, "critic") || strings.Contains(v, "blocc") || strings.Contains(v, "block"):
		return "critical"
	case strings.Contains(v, "high") || strings.Contains(v, "alta") || strings.Contains(v, "major"):
		return "high"
	case strings.Contains(v, "medi"):
		return "medium"
	case strings.Contains(v, "low") || strings.Contains(v, "bass") || strings.Contains(v, "minor"):
		return "low"
	}
	return ""
}

func (pc *projCtx) execTime(date, clock string) *time.Time {
	if strings.TrimSpace(date) == "" {
		return nil
	}
	if clock = strings.TrimSpace(clock); clock != "" {
		if t := pc.cl.ParseTime(strings.TrimSpace(date) + " " + clock); t != nil {
			return t
		}
	}
	return pc.cl.ParseTime(date)
}

func (pc *projCtx) saveItems(ctx context.Context, tx pgx.Tx, insts []almapi.Entity, names map[string]string) error {
	if len(insts) == 0 {
		return nil
	}
	var setRefs []string
	for _, e := range insts {
		setRefs = append(setRefs, e["cycle-id"])
	}
	sets, err := pc.setIDs(ctx, tx, setRefs)
	if err != nil {
		return err
	}
	for _, e := range insts {
		setID := sets[pc.key("set", e["cycle-id"])]
		if setID == "" || e["id"] == "" {
			continue
		}
		testKey := ""
		if e["test-id"] != "" {
			testKey = pc.key("test", e["test-id"])
		}
		name := names[testKey]
		if name == "" && e["test-id"] != "" {
			name = "Test " + e["test-id"]
		}
		if _, err := tx.Exec(ctx, `
			insert into test_set_items (tenant_id, id, test_set_id, source_key, test_key, test_name, status, raw_status, last_run_at, source_updated_at)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (tenant_id, test_set_id, source_key) do update set test_key = excluded.test_key,
			  test_name = coalesce(nullif(excluded.test_name, ''), test_set_items.test_name), status = excluded.status, raw_status = excluded.raw_status,
			  last_run_at = excluded.last_run_at, source_updated_at = excluded.source_updated_at, updated_at = now()`,
			db.NewID(), setID, pc.key("inst", e["id"]), testKey, truncate(name, 300), NormStatus(e["status"]), truncate(e["status"], 60),
			pc.execTime(e["exec-date"], e["exec-time"]), pc.cl.ParseTime(e["last-modified"])); err != nil {
			return err
		}
		pc.st.Items++
		// Il ciclo del test set è quello delle sue istanze.
		if c := e["assign-rcyc"]; c != "" {
			if _, err := tx.Exec(ctx, `update test_sets set cycle_key = $2 where id = $1 and cycle_key <> $2`, setID, pc.key("cycle", c)); err != nil {
				return err
			}
		}
	}
	return nil
}

// deriveSets aggiorna nomi di ciclo e release dei test set del progetto e, se il numero di
// change sta nel ciclo o nella release, il numero.
func (pc *projCtx) deriveSets(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `select id::text, cycle_key, release_key, cycle, release, change_number from test_sets
		where origin = 'alm' and integration_id = $1 and project = $2`, pc.it.ID, pc.p.key())
	if err != nil {
		return err
	}
	type row struct{ id, cycleKey, releaseKey, cycle, release, change string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.cycleKey, &x.releaseKey, &x.cycle, &x.release, &x.change)
		return x, err
	})
	if err != nil {
		return err
	}
	cycPrefix, relPrefix := pc.key("cycle", ""), pc.key("release", "")
	for _, x := range list {
		cyc, rel := "", ""
		var c named
		if id, ok := strings.CutPrefix(x.cycleKey, cycPrefix); ok {
			c = pc.cycles[id]
			cyc = c.name
			if c.parent != "" {
				rel = c.parent
			}
		}
		relKey, relName := "", ""
		var r named
		if rel != "" {
			r = pc.releases[rel]
			relKey, relName = pc.key("release", rel), r.name
		} else if id, ok := strings.CutPrefix(x.releaseKey, relPrefix); ok {
			r = pc.releases[id]
			relKey, relName = x.releaseKey, r.name
		}
		change := x.change
		switch pc.it.Config.ChangeSource {
		case ChangeCycleField:
			change = c.change
		case ChangeReleaseField:
			change = r.change
		}
		if cyc == x.cycle && relKey == x.releaseKey && relName == x.release && change == x.change {
			continue
		}
		if _, err := tx.Exec(ctx, `update test_sets set cycle = $2, release_key = $3, release = $4, change_number = $5, updated_at = now() where id = $1`,
			x.id, truncate(cyc, 200), relKey, truncate(relName, 200), change); err != nil {
			return err
		}
		if change != "" && x.change == "" && (pc.it.Config.ChangeSource == ChangeCycleField || pc.it.Config.ChangeSource == ChangeReleaseField) {
			pc.st.Linked++
		}
	}
	return nil
}

func (pc *projCtx) saveRuns(ctx context.Context, tx pgx.Tx, runs []almapi.Entity) error {
	if len(runs) == 0 {
		return nil
	}
	var setRefs, instKeys []string
	for _, e := range runs {
		setRefs = append(setRefs, e["cycle-id"])
		if e["testcycl-id"] != "" {
			instKeys = append(instKeys, pc.key("inst", e["testcycl-id"]))
		}
	}
	sets, err := pc.setIDs(ctx, tx, setRefs)
	if err != nil {
		return err
	}
	items := map[string]string{}
	rows, err := tx.Query(ctx, "select source_key, id::text from test_set_items where source_key = any($1)", instKeys)
	if err != nil {
		return err
	}
	var k, id string
	if _, err := pgx.ForEachRow(rows, []any{&k, &id}, func() error {
		items[k] = id
		return nil
	}); err != nil {
		return err
	}
	for _, e := range runs {
		if e["id"] == "" {
			continue
		}
		var tester any
		if pc.it.Config.KeepTester {
			if t := strings.TrimSpace(e["owner"]); t != "" {
				tester = truncate(t, 120)
			}
		}
		var dur any
		if d, err := strconv.Atoi(strings.TrimSpace(e["duration"])); err == nil && d >= 0 {
			dur = d
		}
		testKey := ""
		if e["test-id"] != "" {
			testKey = pc.key("test", e["test-id"])
		}
		var inserted bool
		if err := tx.QueryRow(ctx, `
			insert into test_runs (tenant_id, id, origin, integration_id, source_key, test_set_id, item_id, test_key, name, status, raw_status,
			  executed_at, duration_seconds, tester, url, source_updated_at)
			values (app_tenant(), $1, 'alm', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			on conflict (tenant_id, origin, source_key) do update set test_set_id = excluded.test_set_id, item_id = excluded.item_id,
			  test_key = excluded.test_key, name = excluded.name, status = excluded.status, raw_status = excluded.raw_status,
			  executed_at = excluded.executed_at, duration_seconds = excluded.duration_seconds, tester = excluded.tester, url = excluded.url,
			  source_updated_at = excluded.source_updated_at, updated_at = now()
			returning (xmax = 0)`,
			db.NewID(), pc.it.ID, pc.key("run", e["id"]), nullable(sets[pc.key("set", e["cycle-id"])]), nullable(items[pc.key("inst", e["testcycl-id"])]),
			testKey, truncate(strings.TrimSpace(e["name"]), 300), NormStatus(e["status"]), truncate(e["status"], 60),
			pc.execTime(e["execution-date"], e["execution-time"]), dur, tester, pc.link(testLabModule, "IRun", e["id"]),
			pc.cl.ParseTime(e["last-modified"])).Scan(&inserted); err != nil {
			return err
		}
		pc.st.Runs++
		if inserted {
			pc.st.NewRuns++
		}
	}
	return nil
}

func lowerAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

func (pc *projCtx) saveDefects(ctx context.Context, tx pgx.Tx, defects []almapi.Entity, links map[string][]almapi.Entity) error {
	closed := lowerAll(pc.it.Config.DefectClosed)
	for _, e := range defects {
		id := strings.TrimSpace(e["id"])
		if id == "" {
			continue
		}
		// Test set collegati: direttamente, da un'istanza o da un'esecuzione.
		var setKeys, instKeys, runKeys []string
		for _, l := range links[id] {
			other := l["second-endpoint-id"]
			switch strings.ToLower(strings.ReplaceAll(l["second-endpoint-type"], "_", "-")) {
			case "test-set", "cycle":
				setKeys = append(setKeys, pc.key("set", other))
			case "test-instance", "testcycl":
				instKeys = append(instKeys, pc.key("inst", other))
			case "run":
				runKeys = append(runKeys, pc.key("run", other))
			}
		}
		var setIDs []string
		if len(setKeys)+len(instKeys)+len(runKeys) > 0 {
			rows, err := tx.Query(ctx, `select distinct id from (
				  select id::text from test_sets where origin = 'alm' and source_key = any($1)
				  union select test_set_id::text from test_set_items where source_key = any($2)
				  union select test_set_id::text from test_runs where origin = 'alm' and source_key = any($3) and test_set_id is not null) x`,
				setKeys, instKeys, runKeys)
			if err != nil {
				return err
			}
			if setIDs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
				return err
			}
		}
		if setIDs == nil {
			setIDs = []string{}
		}
		status := strings.TrimSpace(e["status"])
		cyc, rel := "", ""
		if v := e["detected-in-rcyc"]; v != "" {
			cyc = pc.key("cycle", v)
		}
		if v := e["detected-in-rel"]; v != "" {
			rel = pc.key("release", v)
		}
		if _, err := tx.Exec(ctx, `
			insert into test_defects (tenant_id, id, origin, integration_id, source_key, external_id, project, summary, status, open, severity, raw_severity,
			  detected_cycle_key, detected_release_key, test_set_ids, url, source_updated_at)
			values (app_tenant(), $1, 'alm', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::uuid[], $14, $15)
			on conflict (tenant_id, origin, source_key) do update set summary = excluded.summary, status = excluded.status, open = excluded.open,
			  severity = excluded.severity, raw_severity = excluded.raw_severity, detected_cycle_key = excluded.detected_cycle_key,
			  detected_release_key = excluded.detected_release_key, test_set_ids = excluded.test_set_ids, url = excluded.url,
			  source_updated_at = excluded.source_updated_at, updated_at = now()`,
			db.NewID(), pc.it.ID, pc.key("defect", id), truncate(id, 64), truncate(pc.p.key(), 200), truncate(strings.Join(strings.Fields(e["name"]), " "), 300),
			truncate(status, 60), !slices.Contains(closed, strings.ToLower(status)), NormSeverity(e["severity"]), truncate(e["severity"], 60),
			cyc, rel, setIDs, pc.link(defectsModule, "IBug", id), pc.cl.ParseTime(e["last-modified"])); err != nil {
			return err
		}
		pc.st.Defects++
	}
	return nil
}

// linkDefects ricalcola i change dei difetti dell'integrazione: quelli dei test set collegati
// e, se il numero di change sta nel ciclo o nella release, quelli dei test set del ciclo o
// della release di rilevazione.
func linkDefects(ctx context.Context, tx pgx.Tx, it *Integration) error {
	byCycle := it.Config.ChangeSource == ChangeCycleField
	byRelease := it.Config.ChangeSource == ChangeReleaseField
	_, err := tx.Exec(ctx, `update test_defects d set change_numbers = coalesce((
		  select array_agg(distinct s.change_number order by s.change_number) from test_sets s
		  where s.integration_id = d.integration_id and s.change_number <> '' and (s.id = any(d.test_set_ids)
		    or ($2 and d.detected_cycle_key <> '' and s.cycle_key = d.detected_cycle_key)
		    or ($3 and d.detected_release_key <> '' and s.release_key = d.detected_release_key))), '{}')
		where d.integration_id = $1`, it.ID, byCycle, byRelease)
	return err
}

func (s *Service) finish(ctx context.Context, c *claim, st Stats, status, errMsg string, reset time.Time) {
	statsJSON, _ := json.Marshal(st)
	var errp *string
	if errMsg != "" {
		e := truncate(errMsg, 2000)
		errp = &e
	}
	err := s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var interval int
		if err := tx.QueryRow(ctx, "select coalesce((config->>'interval_minutes')::int, 60) from integrations where id = $1", c.integrationID).Scan(&interval); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // integrazione cancellata durante la sincronizzazione
			}
			return err
		}
		next := s.o.Now().Add(time.Duration(interval) * time.Minute)
		if !reset.IsZero() && reset.Add(time.Minute).After(next) {
			next = reset.Add(time.Minute)
		}
		if _, err := tx.Exec(ctx, `update connector_sync_state set lease_until = null, last_finished_at = now(), last_status = $2, last_error = $3,
			next_run_at = $4 where integration_id = $1`, c.integrationID, status, errp, next); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update connector_sync_runs set status = $2, stats = $3, error = $4, finished_at = now() where id = $1`,
			c.runID, status, statsJSON, errp); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `delete from connector_sync_runs where integration_id = $1 and id not in (
			select id from connector_sync_runs where integration_id = $1 order by started_at desc limit 50)`, c.integrationID)
		return err
	})
	if err != nil {
		s.o.Log.Warn("connettore ALM: esito della sincronizzazione non salvato", "integrazione", c.integrationID, "errore", err)
	}
}

// Runs elenca le ultime sincronizzazioni di un'integrazione.
func (s *Service) Runs(ctx context.Context, tenantID, integrationID string) ([]Run, error) {
	out := []Run{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select id, trigger, scope, status, stats, error, started_at, finished_at from connector_sync_runs
			where integration_id = $1 order by started_at desc limit 30`, integrationID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) {
			var run Run
			var stats []byte
			err := r.Scan(&run.ID, &run.Trigger, &run.Scope, &run.Status, &stats, &run.Error, &run.StartedAt, &run.FinishedAt)
			_ = json.Unmarshal(stats, &run.Stats)
			return run, err
		})
		return err
	})
	if out == nil {
		out = []Run{}
	}
	return out, err
}
