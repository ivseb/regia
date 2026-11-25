package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/gitlab/glapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Sincronizzazione: stesso schema del connettore GitHub. Il job in-process (Run) è il
// meccanismo principale perché nelle reti aziendali i webhook spesso non arrivano;
// «Sincronizza ora» e i webhook avviano la stessa procedura, eventualmente limitata (Scope).
//
//   - Incrementale: cursori per progetto (Release già lette con la loro impronta, stato
//     già visto di ogni deployment, ultima pipeline del branch predefinito già riportata).
//     Gli oggetti di un progetto e i suoi cursori si salvano nella stessa transazione.
//   - Idempotente: rileggere lo stesso dato non produce oggetti nuovi (cursori) e, se
//     capita (sincronizzazione completa, webhook ripetuti), l'Ingestor lo riconosce.
//   - Limiti di velocità: il client aspetta fino a MaxWait; oltre, la sincronizzazione si
//     ferma con lo stato rate_limited e riprende dopo il reset.
//   - Una sola sincronizzazione per integrazione alla volta (lease nel database).

// Scope limita una sincronizzazione; tutto falso = tutto.
type Scope struct {
	ProjectIDs  []string `json:"projects,omitempty"`
	Releases    bool     `json:"releases,omitempty"`
	Deployments bool     `json:"deployments,omitempty"`
	Checks      bool     `json:"checks,omitempty"`
	Discover    bool     `json:"discover,omitempty"`
	// Full ignora i cursori e rilegge tutto (l'Ingestor scarta i doppioni).
	Full bool `json:"full,omitempty"`
}

func (sc Scope) all() bool { return !sc.Releases && !sc.Deployments && !sc.Checks && !sc.Discover }

func (sc Scope) normalized() Scope {
	if sc.all() {
		sc.Releases, sc.Deployments, sc.Checks, sc.Discover = true, true, true, len(sc.ProjectIDs) == 0
	}
	return sc
}

func (sc Scope) label() string {
	if sc.all() || (sc.Releases && sc.Deployments && sc.Checks && sc.Discover) {
		if sc.Full {
			return "completa"
		}
		return "tutto"
	}
	var parts []string
	for k, on := range map[string]bool{"release": sc.Releases, "deployment": sc.Deployments, "pipeline": sc.Checks, "progetti": sc.Discover} {
		if on {
			parts = append(parts, k)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// Stats sono i numeri di una sincronizzazione.
type Stats struct {
	Projects       int      `json:"projects"`
	Discovered     int      `json:"discovered,omitempty"`
	NewProjects    int      `json:"new_projects,omitempty"`
	Releases       int      `json:"releases"`
	SBOMs          int      `json:"sboms"`
	VulnReports    int      `json:"vuln_reports"`
	Deployments    int      `json:"deployments"`
	Environments   int      `json:"environments"`
	CheckRuns      int      `json:"check_runs"`
	Pipelines      int      `json:"pipelines"`
	Artifacts      int      `json:"artifacts"`
	Requests       int      `json:"requests"`
	ProfileVersion int      `json:"profile_version,omitempty"`
	DurationMS     int64    `json:"duration_ms"`
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
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

type projectCursors struct {
	Releases    map[string]string `json:"releases,omitempty"`
	Deployments map[string]string `json:"deployments,omitempty"`
	ChecksMark  string            `json:"checks_mark,omitempty"`
}

type claim struct {
	runID, tenantID, integrationID, trigger string
	scope                                   Scope
	actor                                   Actor
}

const leaseMinutes = 15

func (s *Service) claim(ctx context.Context, tenantID, integrationID, trigger string, sc Scope, a Actor) (*claim, error) {
	c := &claim{runID: db.NewID(), tenantID: tenantID, integrationID: integrationID, trigger: trigger, scope: sc, actor: a}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var active bool
		err := tx.QueryRow(ctx, "select active from integrations where id = $1 and kind = 'gitlab'", integrationID).Scan(&active)
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
	c, err := s.claim(ctx, a.TenantID, integrationID, "manual", Scope{Full: full}, a)
	if err != nil {
		return "", err
	}
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.gitlab.sync", "integration", integrationID, map[string]any{"full": full, "run": c.runID})
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
				s.o.Log.Warn("connettore GitLab", "errore", err)
			} else if n > 0 {
				s.o.Log.Debug("connettore GitLab: sincronizzazioni eseguite", "numero", n)
			}
		}
	}
}

// RunDue esegue, una dopo l'altra, le sincronizzazioni dovute di tutti i tenant.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	rows, err := s.o.DB.Owner.Query(ctx, `
		select i.tenant_id::text, i.id::text from integrations i
		left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
		where i.kind = 'gitlab' and i.active
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
		c, err := s.claim(ctx, d.tenant, d.id, "schedule", Scope{}, Actor{TenantID: d.tenant})
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
	c, err := s.claim(ctx, tenantID, integrationID, "webhook", sc, Actor{TenantID: tenantID})
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
	def      *profile.Definition
	version  int
	projects []projectRow
	verdict  string
	env      string
	jobs     []string
}

func (s *Service) loadInput(ctx context.Context, tenantID, integrationID string, ids []string) (*syncInput, error) {
	in := &syncInput{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if in.it, in.sec, err = s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		if in.def, in.version, err = definitionFor(ctx, tx, in.it.Config.ProfileID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectProject+` and r.integration_id = $1 and r.mapping_status = 'confirmed' and r.missing_since is null
			order by lower(r.full_name)`, integrationID)
		if err != nil {
			return err
		}
		all, err := scanProjects(rows)
		if err != nil {
			return err
		}
		for _, r := range all {
			if len(ids) == 0 || slices.Contains(ids, r.ID) {
				in.projects = append(in.projects, r)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	in.verdict, in.env, in.jobs = in.it.Config.VerdictCheck, in.it.Config.ReleaseEnvironment, in.it.Config.ArtifactJobs
	return in, nil
}

func (s *Service) execute(ctx context.Context, c *claim) {
	start := s.o.Now()
	st := Stats{}
	status := "ok"
	var errMsg string
	var rate *ghapi.RateLimit
	var rateReset time.Time
	sc := c.scope.normalized()

	func() {
		in, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.ProjectIDs)
		if err != nil {
			status, errMsg = "error", err.Error()
			return
		}
		st.ProfileVersion = in.version
		cl, err := s.client(in.it.Config, in.sec)
		if err != nil {
			status, errMsg = "error", "configurazione: "+err.Error()
			return
		}
		defer func() {
			r := cl.Rate()
			if r.Limit > 0 {
				rate = &r
			}
			st.Requests = cl.Requests()
		}()
		stop := func(err error) bool {
			if reset, ok := glapi.IsRateLimited(err); ok {
				status, errMsg, rateReset = "rate_limited", err.Error(), reset
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
				st.fail("scoperta dei progetti: " + err.Error())
			} else {
				st.Discovered, st.NewProjects = res.Found, res.New
				if in2, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.ProjectIDs); err == nil {
					in.projects = in2.projects // i progetti scomparsi non si sincronizzano più
				}
			}
		}
		for _, r := range in.projects {
			if ctx.Err() != nil {
				stop(ctx.Err())
				return
			}
			st.Projects++
			objs, cur, err := s.syncProject(ctx, cl, in, r, sc)
			for _, w := range objs.warnings {
				st.warn(r.FullName + ": " + w)
			}
			if err != nil {
				s.projectError(ctx, c.tenantID, r.ID, err)
				if stop(err) {
					return
				}
				st.fail(r.FullName + ": " + err.Error())
				continue
			}
			if err := s.ingestProject(ctx, c, r, objs, cur); err != nil {
				s.projectError(ctx, c.tenantID, r.ID, err)
				st.fail(r.FullName + ": archiviazione: " + err.Error())
				continue
			}
			st.Releases += len(objs.releases)
			for _, ro := range objs.releases {
				if ro.SBOM != nil {
					st.SBOMs++
				}
				if ro.Vulns != nil {
					st.VulnReports++
				}
			}
			st.Deployments += len(objs.deployments)
			st.Environments += objs.environments
			st.Pipelines += objs.pipelines
			st.Artifacts += objs.archives
			if objs.checkRun != nil {
				st.CheckRuns++
			}
		}
	}()

	if status == "ok" && len(st.Errors) > 0 {
		status = "partial"
		errMsg = st.Errors[0]
	}
	st.DurationMS = s.o.Now().Sub(start).Milliseconds()
	s.finish(context.WithoutCancel(ctx), c, st, status, errMsg, rate, rateReset)
}

func (s *Service) projectError(ctx context.Context, tenantID, id string, err error) {
	_ = s.o.DB.InTenant(context.WithoutCancel(ctx), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "update connector_repositories set last_error = $2, updated_at = now() where id = $1", id, truncate(err.Error(), 1000))
		return e
	})
}

func (s *Service) finish(ctx context.Context, c *claim, st Stats, status, errMsg string, rate *ghapi.RateLimit, reset time.Time) {
	statsJSON, _ := json.Marshal(st)
	var rateJSON []byte
	if rate != nil {
		rateJSON, _ = json.Marshal(rate)
	}
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
		// Una sincronizzazione mirata (webhook) non sposta quella pianificata.
		keepNext := c.trigger == "webhook" && reset.IsZero()
		if _, err := tx.Exec(ctx, `update connector_sync_state set lease_until = null, last_finished_at = now(), last_status = $2, last_error = $3,
			rate_limit = coalesce($4::jsonb, rate_limit), next_run_at = case when $6 then next_run_at else $5 end
			where integration_id = $1`, c.integrationID, status, errp, rateJSON, next, keepNext); err != nil {
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
		s.o.Log.Warn("connettore GitLab: esito della sincronizzazione non salvato", "integrazione", c.integrationID, "errore", err)
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

// ---------------------------------------------------------------------
// Un progetto
// ---------------------------------------------------------------------

type projectObjects struct {
	releases     []profile.ReleaseOutput
	deployments  []contract.Deployment
	checkRun     *contract.CheckRun
	warnings     []string
	environments int
	pipelines    int
	archives     int
	// per l'anteprima
	assets   []string
	fromJobs []string
	fromLink []string
}

const maxReleasesPerSync = 50

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// releaseFingerprint cambia quando cambia la Release o i suoi link (un link aggiunto dopo la
// pubblicazione): la Release si rilegge.
func releaseFingerprint(r glapi.Release) string {
	var b strings.Builder
	b.WriteString(r.TagName)
	fmt.Fprintf(&b, "@%d#%s", r.At().Unix(), r.Commit.ID)
	var ls []string
	for _, l := range r.Assets.Links {
		ls = append(ls, l.Name+"="+l.Download())
	}
	slices.Sort(ls)
	for _, l := range ls {
		b.WriteString("|" + l)
	}
	return b.String()
}

// readRelease traduce una Release con i suoi allegati (link e artefatti dei job). complete è
// falso se la pipeline del tag è ancora in corso o un file presente non si è scaricato:
// la Release si riproverà.
func (s *Service) readRelease(ctx context.Context, cl *glapi.Client, def *profile.Definition, svc contract.Service, project int64,
	rel glapi.Release, env string, jobGlobs []string) (profile.ReleaseOutput, resolved, bool, error) {
	res, err := s.resolveAssets(ctx, cl, project, rel, def, jobGlobs)
	if err != nil {
		return profile.ReleaseOutput{}, res, false, err
	}
	in := profile.ReleaseInput{Service: svc, Tag: rel.TagName, Name: rel.Name, Author: rel.Author.Username, URL: rel.Links.Self,
		PublishedAt: rel.At(), Assets: res.Assets, Present: res.Present}
	if isSHA(rel.Commit.ID) {
		in.Commit = rel.Commit.ID
	}
	var warns []string
	d2 := withGitLabReports(def, in.Assets, func(f string, a ...any) { warns = append(warns, fmt.Sprintf(f, a...)) })
	out := d2.ExtractRelease(in, env)
	out.Warnings = append(append(warns, res.Warnings...), out.Warnings...)
	return out, res, !res.Incomplete, nil
}

func glDeployState(s string) string {
	switch s {
	case "success":
		return "success"
	case "failed", "canceled":
		return "failure"
	}
	return "pending" // created, running, blocked
}

func deployMark(d glapi.Deployment) string {
	return d.Status + "@" + strconv.FormatInt(d.UpdatedAt.Unix(), 10)
}

func terminalDeploy(s string) bool { return s == "success" || s == "failed" || s == "canceled" }

// jobInputs traduce i job di una pipeline nei check run del profilo.
func jobInputs(jobs []glapi.Job) []profile.CheckRunInput {
	out := make([]profile.CheckRunInput, 0, len(jobs))
	for _, j := range jobs {
		ci := profile.CheckRunInput{ID: strconv.FormatInt(j.ID, 10), Name: j.Name, URL: j.WebURL}
		if j.Stage != "" {
			ci.Summary = "stage " + j.Stage
		}
		switch j.Status {
		case "success":
			ci.Status, ci.Conclusion = "completed", "success"
		case "failed":
			ci.Status, ci.Conclusion = "completed", "failure"
			if j.AllowFailure {
				ci.Conclusion, ci.Summary = "neutral", strings.TrimSpace(ci.Summary+" · fallito, ma consentito")
			}
		case "canceled":
			ci.Status, ci.Conclusion = "completed", "cancelled"
		case "skipped", "manual":
			// Un job manuale mai avviato non blocca il verdetto: non è stato eseguito.
			ci.Status, ci.Conclusion = "completed", "skipped"
		default:
			ci.Status = "in_progress"
		}
		if j.FinishedAt != nil {
			ci.CompletedAt = j.FinishedAt.UTC()
		}
		out = append(out, ci)
	}
	return out
}

// syncProject legge da GitLab ciò che è cambiato in un progetto (nessuna scrittura).
func (s *Service) syncProject(ctx context.Context, cl *glapi.Client, in *syncInput, r projectRow, sc Scope) (projectObjects, projectCursors, error) {
	var objs projectObjects
	var cur projectCursors
	_ = json.Unmarshal(r.cursors, &cur)
	next := projectCursors{Releases: map[string]string{}, Deployments: map[string]string{}, ChecksMark: cur.ChecksMark}
	for k, v := range cur.Releases {
		next.Releases[k] = v
	}
	for k, v := range cur.Deployments {
		next.Deployments[k] = v
	}
	svc := serviceOf(r.Project)
	def := in.def
	pid := r.pid()

	if sc.Releases {
		var todo []glapi.Release
		seenRun := 0
		err := cl.Releases(ctx, pid, 5, func(rel glapi.Release) (bool, error) {
			if rel.UpcomingRelease || !def.AcceptsTag(rel.TagName, false) {
				return false, nil
			}
			if !sc.Full && cur.Releases[rel.TagName] == releaseFingerprint(rel) {
				seenRun++
				return seenRun >= 10, nil // dieci già lette di fila: il resto è storia nota
			}
			seenRun = 0
			todo = append(todo, rel)
			return len(todo) >= maxReleasesPerSync, nil
		})
		if err != nil {
			return objs, cur, fmt.Errorf("Release: %w", err)
		}
		for i := len(todo) - 1; i >= 0; i-- { // dalla più vecchia: l'ordine dei rilasci conta
			rel := todo[i]
			out, res, complete, err := s.readRelease(ctx, cl, def, svc, pid, rel, in.env, in.jobs)
			if err != nil {
				return objs, cur, err
			}
			objs.warnings = append(objs.warnings, out.Warnings...)
			objs.archives += res.Archives
			objs.releases = append(objs.releases, out)
			if complete {
				next.Releases[rel.TagName] = releaseFingerprint(rel)
			}
		}
		if len(next.Releases) > 300 { // i cursori non crescono senza limite
			for k := range next.Releases {
				if len(next.Releases) <= 200 {
					break
				}
				delete(next.Releases, k)
			}
		}
	}

	if sc.Deployments && def.DeploymentsEnabled() {
		deps, err := cl.Deployments(ctx, pid, "", def.DeploymentsMax())
		if err != nil {
			return objs, cur, fmt.Errorf("Deployments: %w", err)
		}
		// Gli ambienti senza deployment tra i recenti: l'ultimo di ciascuno.
		envs, err := cl.Environments(ctx, pid)
		if err != nil {
			objs.warnings = append(objs.warnings, "Environments non letti: "+err.Error())
		}
		objs.environments = len(envs)
		have := map[string]bool{}
		for _, d := range deps {
			have[d.Environment.Name] = true
		}
		for _, e := range envs {
			if have[e.Name] {
				continue
			}
			last, err := cl.Deployments(ctx, pid, e.Name, 1)
			if err != nil {
				return objs, cur, fmt.Errorf("ultimo deployment di %s: %w", e.Name, err)
			}
			deps = append(deps, last...)
		}
		listed := map[string]bool{}
		slices.SortStableFunc(deps, func(a, b glapi.Deployment) int { return a.UpdatedAt.Compare(b.UpdatedAt) }) // dal più vecchio
		for _, d := range deps {
			id := strconv.FormatInt(d.ID, 10)
			listed[id] = true
			mark := deployMark(d)
			prev := cur.Deployments[id]
			if !sc.Full && prev == mark && terminalDeploy(d.Status) {
				continue
			}
			version, digest := def.DeploymentVersion(d.Raw)
			short := d.SHA
			if len(short) > 12 {
				short = short[:12]
			}
			if d.Deployable.Tag != nil && !*d.Deployable.Tag && (version == d.Ref || "v"+version == d.Ref) {
				version = short // un branch non è una versione
			}
			if version == "" {
				version = short
			}
			at := d.UpdatedAt
			if at.IsZero() {
				at = d.CreatedAt
			}
			objs.deployments = append(objs.deployments, contract.Deployment{
				Service: svc, Environment: def.DeploymentEnv(d.Environment.Name), Version: version, Digest: digest,
				Status: profile.DeploymentStatus(glDeployState(d.Status)), At: at.UTC(), Source: "pipeline", ExternalRef: "gitlab:deployment:" + id,
			})
			next.Deployments[id] = mark
		}
		for k := range next.Deployments {
			if !listed[k] {
				delete(next.Deployments, k)
			}
		}
	}

	if sc.Checks {
		pls, err := cl.PipelinesForRef(ctx, pid, r.DefaultBranch, 1)
		if err != nil {
			return objs, cur, fmt.Errorf("pipeline di %s: %w", r.DefaultBranch, err)
		}
		if len(pls) > 0 {
			pl := pls[0]
			mark := fmt.Sprintf("%d:%s", pl.ID, pl.Status)
			if pl.Finished() && (sc.Full || mark != cur.ChecksMark) {
				jobs, err := cl.Jobs(ctx, pid, pl.ID)
				if err != nil {
					return objs, cur, fmt.Errorf("job della pipeline %d: %w", pl.ID, err)
				}
				objs.pipelines++
				cr, ready := def.BuildCheckRun(svc, pl.SHA, r.DefaultBranch, jobInputs(jobs), in.verdict)
				if ready {
					cr.RunID, cr.URL = strconv.FormatInt(pl.ID, 10), pl.WebURL
					if cr.At.IsZero() {
						cr.At = pl.UpdatedAt.UTC()
					}
					objs.checkRun = &cr
					next.ChecksMark = mark
				}
			}
		}
	}
	return objs, next, nil
}

// ingestProject consegna gli oggetti di un progetto e ne salva i cursori, insieme.
func (s *Service) ingestProject(ctx context.Context, c *claim, r projectRow, objs projectObjects, cur projectCursors) error {
	curJSON, _ := json.Marshal(cur)
	ing := s.o.Ingestor
	act := systemActor(c.actor)
	return s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if si, ok := ing.(contract.ServiceIngestor); ok {
			if err := si.IngestService(ctx, tx, act, serviceOf(r.Project)); err != nil {
				return err
			}
		}
		for _, ro := range objs.releases {
			if err := ing.IngestRelease(ctx, tx, act, ro.Release); err != nil {
				return err
			}
			if ro.SBOM != nil {
				if err := ing.IngestSBOM(ctx, tx, act, *ro.SBOM); err != nil {
					return err
				}
			}
			if ro.Vulns != nil {
				if err := ing.IngestVulnReport(ctx, tx, act, *ro.Vulns); err != nil {
					return err
				}
			}
		}
		for _, d := range objs.deployments {
			if err := ing.IngestDeployment(ctx, tx, act, d); err != nil {
				return err
			}
		}
		if objs.checkRun != nil {
			if err := ing.IngestCheckRun(ctx, tx, act, *objs.checkRun); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update connector_repositories set cursors = $2, last_synced_at = now(), last_error = null, updated_at = now()
			where id = $1`, r.ID, curJSON); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "update connector_sync_state set lease_until = now() + make_interval(mins => $2) where integration_id = $1",
			c.integrationID, leaseMinutes)
		return err
	})
}
