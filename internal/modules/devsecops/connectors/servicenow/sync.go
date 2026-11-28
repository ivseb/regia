package servicenow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/commitsync"
	"regia/internal/modules/devsecops/connectors/servicenow/snapi"
	"regia/internal/platform/db"
)

// Sincronizzazione: stesso schema degli altri connettori. Il job in-process (Run) è il
// meccanismo principale; «Sincronizza ora» e il webhook avviano la stessa procedura,
// eventualmente limitata (Scope).
//
//   - Incrementale: per ogni servizio associato, un cursore su sys_updated_on per gli
//     incidenti e uno per le change (in connector_repositories.cursors). Un servizio
//     associato dopo parte dalla sua prima lettura (lookback_days), non dal cursore degli
//     altri. Record e cursore si salvano nella stessa transazione.
//   - Idempotente: i record si aggiornano per sys_id; rileggere non duplica nulla.
//   - Change già note a Regia (tabella changes, numero o alias) si leggono anche se non
//     sono di un servizio associato: servono al gate.
//   - Una sola sincronizzazione per integrazione alla volta (lease nel database).

// Scope limita una sincronizzazione; tutto falso = tutto.
type Scope struct {
	Incidents bool `json:"incidents,omitempty"`
	Changes   bool `json:"changes,omitempty"`
	Discover  bool `json:"discover,omitempty"`
	// Full ignora i cursori e rilegge dall'inizio della finestra (lookback_days).
	Full bool `json:"full,omitempty"`
}

func (sc Scope) all() bool { return !sc.Incidents && !sc.Changes && !sc.Discover }

func (sc Scope) normalized() Scope {
	if sc.all() {
		sc.Incidents, sc.Changes, sc.Discover = true, true, true
	}
	return sc
}

func (sc Scope) label() string {
	if sc.all() {
		if sc.Full {
			return "completa"
		}
		return "tutto"
	}
	var parts []string
	if sc.Discover {
		parts = append(parts, "servizi")
	}
	if sc.Incidents {
		parts = append(parts, "incidenti")
	}
	if sc.Changes {
		parts = append(parts, "change")
	}
	return strings.Join(parts, ", ")
}

// Stats sono i numeri di una sincronizzazione.
type Stats struct {
	Services     int      `json:"services"`
	Discovered   int      `json:"discovered,omitempty"`
	NewServices  int      `json:"new_services,omitempty"`
	Incidents    int      `json:"incidents"`
	NewIncidents int      `json:"new_incidents"`
	Changes      int      `json:"changes"`
	NewChanges   int      `json:"new_changes"`
	KnownChanges int      `json:"known_changes,omitempty"`
	Requests     int      `json:"requests"`
	DurationMS   int64    `json:"duration_ms"`
	Errors       []string `json:"errors,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
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
	scope                                   Scope
}

const leaseMinutes = 15

func (s *Service) claim(ctx context.Context, tenantID, integrationID, trigger string, sc Scope) (*claim, error) {
	c := &claim{runID: db.NewID(), tenantID: tenantID, integrationID: integrationID, trigger: trigger, scope: sc}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var active bool
		err := tx.QueryRow(ctx, "select active from integrations where id = $1 and kind = 'servicenow'", integrationID).Scan(&active)
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
			values (app_tenant(), $1, $2, $3, $4, 'running')`, c.runID, integrationID, trigger, sc.label())
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
	c, err := s.claim(ctx, a.TenantID, integrationID, "manual", Scope{Full: full})
	if err != nil {
		return "", err
	}
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.servicenow.sync", "integration", integrationID, map[string]any{"full": full, "run": c.runID})
	}); err != nil {
		return "", err
	}
	s.background(c)
	return c.runID, nil
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
				s.o.Log.Warn("connettore ServiceNow", "errore", err)
			} else if n > 0 {
				s.o.Log.Debug("connettore ServiceNow: sincronizzazioni eseguite", "numero", n)
			}
		}
	}
}

// RunDue esegue, una dopo l'altra, le sincronizzazioni dovute di tutti i tenant.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	rows, err := s.o.DB.Owner.Query(ctx, `
		select i.tenant_id::text, i.id::text from integrations i
		left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
		where i.kind = 'servicenow' and i.active
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
		c, err := s.claim(ctx, d.tenant, d.id, "schedule", Scope{})
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

// SyncScoped esegue subito una sincronizzazione limitata (webhook); se ce n'è già una in
// corso, anticipa la prossima pianificata.
func (s *Service) SyncScoped(ctx context.Context, tenantID, integrationID string, sc Scope, background bool) error {
	c, err := s.claim(ctx, tenantID, integrationID, "webhook", sc)
	if errors.Is(err, ErrBusy) {
		return s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "update connector_sync_state set next_run_at = now() where integration_id = $1", integrationID)
			return err
		})
	}
	if err != nil {
		return err
	}
	if background {
		s.background(c)
		return nil
	}
	s.execute(ctx, c)
	return nil
}

type syncInput struct {
	it       *Integration
	sec      secrets
	services []svcRow
	bySysID  map[string]*svcRow
}

func (s *Service) loadInput(ctx context.Context, tenantID, integrationID string) (*syncInput, error) {
	in := &syncInput{bySysID: map[string]*svcRow{}}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if in.it, in.sec, err = s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectService+` and r.integration_id = $1 and r.mapping_status = 'confirmed' and r.missing_since is null
			and r.application_element_id is not null order by lower(r.name)`, integrationID)
		if err != nil {
			return err
		}
		in.services, err = scanServices(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	for i := range in.services {
		in.bySysID[in.services[i].SysID] = &in.services[i]
	}
	return in, nil
}

func (s *Service) execute(ctx context.Context, c *claim) {
	start := s.o.Now()
	st := Stats{}
	status := "ok"
	var errMsg string
	var rateReset time.Time
	sc := c.scope.normalized()

	func() {
		in, err := s.loadInput(ctx, c.tenantID, c.integrationID)
		if err != nil {
			status, errMsg = "error", err.Error()
			return
		}
		cl, err := s.client(in.it.Config, in.sec)
		if err != nil {
			status, errMsg = "error", "configurazione: "+err.Error()
			return
		}
		defer func() { st.Requests = cl.Requests() }()
		stop := func(err error) bool {
			if reset, ok := snapi.IsRateLimited(err); ok {
				status, errMsg, rateReset = "rate_limited", err.Error(), reset
				return true
			}
			if st := snapi.StatusOf(err); st == 401 {
				status, errMsg = "error", err.Error() // credenziali: inutile continuare
				return true
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				status, errMsg = "error", "sincronizzazione interrotta: "+err.Error()
				return true
			}
			return false
		}
		if sc.Discover {
			res, err := s.discover(ctx, c.tenantID, in.it, cl)
			if err != nil {
				if stop(err) {
					return
				}
				st.fail("scoperta dei servizi: " + err.Error())
			} else {
				st.Discovered, st.NewServices = res.Found, res.New
				if in2, err := s.loadInput(ctx, c.tenantID, c.integrationID); err == nil {
					in = in2 // i servizi scomparsi non si sincronizzano più
				}
			}
		}
		for i := range in.services {
			if ctx.Err() != nil {
				stop(ctx.Err())
				return
			}
			svc := &in.services[i]
			st.Services++
			if sc.Incidents {
				n, created, err := s.syncIncidents(ctx, c.tenantID, cl, in, svc, sc.Full)
				st.Incidents += n
				st.NewIncidents += created
				if err != nil {
					s.serviceError(ctx, c.tenantID, svc.ID, err)
					if stop(err) {
						return
					}
					st.fail(svc.Name + ": incidenti: " + err.Error())
					continue
				}
			}
			if sc.Changes {
				n, created, err := s.syncChanges(ctx, c.tenantID, cl, in, svc, sc.Full)
				st.Changes += n
				st.NewChanges += created
				if err != nil {
					s.serviceError(ctx, c.tenantID, svc.ID, err)
					if stop(err) {
						return
					}
					st.fail(svc.Name + ": change: " + err.Error())
				}
			}
		}
		if sc.Changes {
			n, err := s.syncKnownChanges(ctx, c.tenantID, cl, in)
			st.KnownChanges = n
			if err != nil {
				if stop(err) {
					return
				}
				st.fail("change già note a Regia: " + err.Error())
			}
		}
		if len(in.services) == 0 {
			st.warn("nessun servizio associato: conferma le associazioni nella scheda Servizi, poi sincronizza")
		}
	}()

	if (status == "ok" || status == "partial") && s.o.Dora != nil {
		if err := commitsync.RecomputeFirst(context.WithoutCancel(ctx), s.o.DB, c.tenantID, s.o.Dora); err != nil {
			st.warn("metriche DORA non ricalcolate: " + err.Error())
		}
	}
	if status == "ok" && len(st.Errors) > 0 {
		status = "partial"
		errMsg = st.Errors[0]
	}
	st.DurationMS = s.o.Now().Sub(start).Milliseconds()
	s.finish(context.WithoutCancel(ctx), c, st, status, errMsg, rateReset)
}

func (s *Service) serviceError(ctx context.Context, tenantID, serviceID string, err error) {
	_ = s.o.DB.InTenant(context.WithoutCancel(ctx), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "update connector_repositories set last_error = $2, updated_at = now() where id = $1", serviceID, truncate(err.Error(), 1000))
		return e
	})
}

// since è il punto di partenza di una lettura: il cursore, oppure l'inizio della finestra.
func (s *Service) since(cursor string, full bool, days int) string {
	if !full && cursor != "" {
		return cursor
	}
	return snapi.FormatTime(s.o.Now().AddDate(0, 0, -days))
}

// MaxPerRun è il numero massimo di record per tabella e servizio in una sincronizzazione:
// oltre, il cursore si ferma all'ultimo letto e si riprende alla successiva.
const MaxPerRun = 2000

var incidentFields = []string{"sys_id", "number", "priority", "state", "opened_at", "resolved_at", "closed_at",
	"short_description", "cmdb_ci", "business_service", "sys_updated_on", "sys_created_on"}

var changeFields = []string{"sys_id", "number", "state", "approval", "type", "start_date", "end_date", "work_start", "work_end",
	"short_description", "cmdb_ci", "business_service", "sys_updated_on"}

// serviceFilter è la condizione «del servizio»: il campo Servizio o il CI dell'incidente.
func serviceFilter(sysID string) string {
	return "business_service=" + sysID + "^ORcmdb_ci=" + sysID
}

// owner sceglie il servizio associato a cui appartiene un record: quello del campo
// Servizio se è associato, altrimenti quello del CI, altrimenti quello che l'ha letto.
func (in *syncInput) owner(rec map[string]string, fallback *svcRow) *svcRow {
	if o := in.bySysID[rec["business_service"]]; o != nil {
		return o
	}
	if o := in.bySysID[rec["cmdb_ci"]]; o != nil {
		return o
	}
	return fallback
}

func appOf(r *svcRow) any {
	if r == nil || r.Application == nil {
		return nil
	}
	return r.Application.ID
}

func idOf(r *svcRow) any {
	if r == nil {
		return nil
	}
	return r.ID
}

func (s *Service) syncIncidents(ctx context.Context, tenantID string, cl *snapi.Client, in *syncInput, svc *svcRow, full bool) (int, int, error) {
	from := s.since(svc.cur.Incidents, full, in.it.Config.LookbackDays)
	q := "priorityIN" + joinInts(in.it.Config.Priorities) + "^" + serviceFilter(svc.SysID) + "^sys_updated_on>=" + from + "^ORDERBYsys_updated_on"
	var recs []map[string]string
	err := cl.Table(ctx, snapi.Query{Table: "incident", Query: q, Fields: incidentFields, Limit: MaxPerRun}, func(r map[string]string) (bool, error) {
		recs = append(recs, r)
		return false, nil
	})
	if err != nil {
		return 0, 0, err
	}
	n, created := 0, 0
	err = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cursor := svc.cur.Incidents
		for _, r := range recs {
			sysID, number := strings.TrimSpace(r["sys_id"]), strings.ToUpper(strings.TrimSpace(r["number"]))
			prio, _ := strconv.Atoi(strings.TrimSpace(r["priority"]))
			updated := snapi.ParseTime(r["sys_updated_on"])
			if sysID == "" || number == "" || prio < 1 || prio > 5 || updated == nil {
				continue
			}
			opened := snapi.ParseTime(r["opened_at"])
			if opened == nil {
				opened = snapi.ParseTime(r["sys_created_on"])
			}
			if opened == nil {
				opened = updated
			}
			resolved, closed := snapi.ParseTime(r["resolved_at"]), snapi.ParseTime(r["closed_at"])
			if resolved == nil && closed != nil {
				resolved = closed // chiuso senza la data di risoluzione
			}
			o := in.owner(r, svc)
			var inserted bool
			if err := tx.QueryRow(ctx, `
				insert into servicenow_incidents (tenant_id, id, integration_id, sys_id, number, priority, state, opened_at, resolved_at, closed_at,
				  short_description, ci_sys_id, service_sys_id, url, repository_id, application_element_id, sys_updated_on)
				values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
				on conflict (tenant_id, integration_id, sys_id) do update set number = excluded.number, priority = excluded.priority,
				  state = excluded.state, opened_at = excluded.opened_at, resolved_at = excluded.resolved_at, closed_at = excluded.closed_at,
				  short_description = excluded.short_description, ci_sys_id = excluded.ci_sys_id, service_sys_id = excluded.service_sys_id,
				  url = excluded.url, repository_id = excluded.repository_id, application_element_id = excluded.application_element_id,
				  sys_updated_on = excluded.sys_updated_on, updated_at = now()
				returning (xmax = 0)`,
				db.NewID(), in.it.ID, truncate(sysID, 64), truncate(number, 40), prio, truncate(r["state"], 20), *opened, resolved, closed,
				truncate(strings.TrimSpace(r["short_description"]), 300), truncate(r["cmdb_ci"], 64), truncate(r["business_service"], 64),
				cl.RecordURL("incident", sysID), idOf(o), appOf(o), *updated).Scan(&inserted); err != nil {
				return err
			}
			n++
			if inserted {
				created++
			}
			if c := snapi.FormatTime(*updated); c > cursor {
				cursor = c
			}
		}
		_, err := tx.Exec(ctx, `update connector_repositories set cursors = cursors || jsonb_build_object('incidents', $2::text),
			last_synced_at = now(), last_error = null where id = $1`, svc.ID, cursorOr(cursor, from))
		return err
	})
	return n, created, err
}

// cursorOr: senza record letti il cursore resta all'inizio della finestra, così una
// sincronizzazione completa non si ripete all'infinito.
func cursorOr(cursor, from string) string {
	if cursor == "" {
		return from
	}
	return cursor
}

func (s *Service) syncChanges(ctx context.Context, tenantID string, cl *snapi.Client, in *syncInput, svc *svcRow, full bool) (int, int, error) {
	from := s.since(svc.cur.Changes, full, in.it.Config.LookbackDays)
	q := serviceFilter(svc.SysID) + "^sys_updated_on>=" + from + "^ORDERBYsys_updated_on"
	var recs []map[string]string
	err := cl.Table(ctx, snapi.Query{Table: "change_request", Query: q, Fields: changeFields, Limit: MaxPerRun}, func(r map[string]string) (bool, error) {
		recs = append(recs, r)
		return false, nil
	})
	if err != nil {
		return 0, 0, err
	}
	n, created := 0, 0
	err = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cursor := svc.cur.Changes
		for _, r := range recs {
			ok, inserted, err := s.upsertChange(ctx, tx, cl, in, r, in.owner(r, svc))
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			n++
			if inserted {
				created++
			}
			if u := snapi.ParseTime(r["sys_updated_on"]); u != nil {
				if c := snapi.FormatTime(*u); c > cursor {
					cursor = c
				}
			}
		}
		_, err := tx.Exec(ctx, `update connector_repositories set cursors = cursors || jsonb_build_object('changes', $2::text),
			last_synced_at = now(), last_error = null where id = $1`, svc.ID, cursorOr(cursor, from))
		return err
	})
	return n, created, err
}

func (s *Service) upsertChange(ctx context.Context, tx pgx.Tx, cl *snapi.Client, in *syncInput, r map[string]string, o *svcRow) (bool, bool, error) {
	sysID, number := strings.TrimSpace(r["sys_id"]), strings.ToUpper(strings.TrimSpace(r["number"]))
	updated := snapi.ParseTime(r["sys_updated_on"])
	if sysID == "" || number == "" || updated == nil {
		return false, false, nil
	}
	var inserted bool
	err := tx.QueryRow(ctx, `
		insert into servicenow_changes (tenant_id, id, integration_id, sys_id, number, state, approval, type, planned_start, planned_end,
		  actual_start, actual_end, short_description, ci_sys_id, service_sys_id, url, repository_id, application_element_id, sys_updated_on)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		on conflict (tenant_id, integration_id, sys_id) do update set number = excluded.number, state = excluded.state,
		  approval = excluded.approval, type = excluded.type, planned_start = excluded.planned_start, planned_end = excluded.planned_end,
		  actual_start = excluded.actual_start, actual_end = excluded.actual_end, short_description = excluded.short_description,
		  ci_sys_id = excluded.ci_sys_id, service_sys_id = excluded.service_sys_id, url = excluded.url,
		  repository_id = coalesce(excluded.repository_id, servicenow_changes.repository_id),
		  application_element_id = coalesce(excluded.application_element_id, servicenow_changes.application_element_id),
		  sys_updated_on = excluded.sys_updated_on, updated_at = now()
		returning (xmax = 0)`,
		db.NewID(), in.it.ID, truncate(sysID, 64), truncate(number, 40), truncate(r["state"], 20), truncate(strings.ToLower(strings.TrimSpace(r["approval"])), 40),
		truncate(r["type"], 40), snapi.ParseTime(r["start_date"]), snapi.ParseTime(r["end_date"]), snapi.ParseTime(r["work_start"]), snapi.ParseTime(r["work_end"]),
		truncate(strings.TrimSpace(r["short_description"]), 300), truncate(r["cmdb_ci"], 64), truncate(r["business_service"], 64),
		cl.RecordURL("change_request", sysID), idOf(o), appOf(o), *updated).Scan(&inserted)
	return err == nil, inserted, err
}

// MaxKnownChanges è il numero massimo di change di Regia controllate per numero a ogni
// sincronizzazione (le più recenti, non ancora chiuse in ServiceNow).
const MaxKnownChanges = 300

// syncKnownChanges legge per numero le change che Regia conosce già (dai rilasci o dalle
// richieste) e che non sono ancora chiuse: il gate le trova anche se il servizio della
// change non è tra quelli associati.
func (s *Service) syncKnownChanges(ctx context.Context, tenantID string, cl *snapi.Client, in *syncInput) (int, error) {
	var numbers []string
	if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select n from (
			  select distinct on (n) n, c.updated_at from changes c, unnest(array_prepend(c.number, c.aliases)) n
			  where c.updated_at > now() - interval '180 days' order by n, c.updated_at desc) x
			where n ~ '^[A-Z]{2,10}[0-9]{4,12}$'
			  and not exists (select 1 from servicenow_changes sc where sc.integration_id = $1 and upper(sc.number) = x.n and sc.state in ('3', '4', '7'))
			order by x.updated_at desc limit $2`, in.it.ID, MaxKnownChanges)
		if err != nil {
			return err
		}
		numbers, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil {
		return 0, err
	}
	n := 0
	for chunk := range slices.Chunk(numbers, 50) {
		var recs []map[string]string
		err := cl.Table(ctx, snapi.Query{Table: "change_request", Query: "numberIN" + strings.Join(chunk, ","), Fields: changeFields, Limit: len(chunk)},
			func(r map[string]string) (bool, error) {
				recs = append(recs, r)
				return false, nil
			})
		if err != nil {
			return n, err
		}
		if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			for _, r := range recs {
				ok, _, err := s.upsertChange(ctx, tx, cl, in, r, in.owner(r, nil))
				if err != nil {
					return err
				}
				if ok {
					n++
				}
			}
			return nil
		}); err != nil {
			return n, err
		}
	}
	return n, nil
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
		if err := tx.QueryRow(ctx, "select coalesce((config->>'interval_minutes')::int, 30) from integrations where id = $1", c.integrationID).Scan(&interval); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // integrazione cancellata durante la sincronizzazione
			}
			return err
		}
		next := s.o.Now().Add(time.Duration(interval) * time.Minute)
		if !reset.IsZero() && reset.Add(time.Minute).After(next) {
			next = reset.Add(time.Minute)
		}
		keepNext := c.trigger == "webhook" && reset.IsZero()
		if _, err := tx.Exec(ctx, `update connector_sync_state set lease_until = null, last_finished_at = now(), last_status = $2, last_error = $3,
			next_run_at = case when $5 then next_run_at else $4 end
			where integration_id = $1`, c.integrationID, status, errp, next, keepNext); err != nil {
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
		s.o.Log.Warn("connettore ServiceNow: esito della sincronizzazione non salvato", "integrazione", c.integrationID, "errore", err)
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
