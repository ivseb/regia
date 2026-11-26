package gitea

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

	"regia/internal/modules/devsecops/connectors/commitsync"
	"regia/internal/modules/devsecops/connectors/gitea/giteaapi"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Sincronizzazione: stesso schema del connettore GitHub. Il job in-process (Run) è il
// meccanismo principale perché nelle reti aziendali i webhook spesso non arrivano;
// «Sincronizza ora» e i webhook avviano la stessa procedura, eventualmente limitata (Scope).
//
//   - Incrementale: cursori per repository (Release già lette con la loro impronta, stato
//     già visto di ogni deployment, ultima pipeline del branch predefinito già riportata).
//     Gli oggetti di un repository e i suoi cursori si salvano nella stessa transazione.
//   - Idempotente: rileggere lo stesso dato non produce oggetti nuovi (cursori) e, se
//     capita (sincronizzazione completa, webhook ripetuti), l'Ingestor lo riconosce.
//   - Limiti di velocità: il client aspetta fino a MaxWait; oltre, la sincronizzazione si
//     ferma con lo stato rate_limited e riprende dopo il reset.
//   - Una sola sincronizzazione per integrazione alla volta (lease nel database).

// Scope limita una sincronizzazione; tutto falso = tutto.
type Scope struct {
	RepoIDs  []string `json:"repos,omitempty"`
	Releases bool     `json:"releases,omitempty"`
	Checks   bool     `json:"checks,omitempty"`
	Commits  bool     `json:"commits,omitempty"`
	Discover bool     `json:"discover,omitempty"`
	// Full ignora i cursori e rilegge tutto (l'Ingestor scarta i doppioni).
	Full bool `json:"full,omitempty"`
}

func (sc Scope) all() bool { return !sc.Releases && !sc.Checks && !sc.Commits && !sc.Discover }

func (sc Scope) normalized() Scope {
	if sc.all() {
		sc.Releases, sc.Checks, sc.Commits, sc.Discover = true, true, true, len(sc.RepoIDs) == 0
	}
	return sc
}

func (sc Scope) label() string {
	if sc.all() || (sc.Releases && sc.Checks && sc.Commits && sc.Discover) {
		if sc.Full {
			return "completa"
		}
		return "tutto"
	}
	var parts []string
	for k, on := range map[string]bool{"release": sc.Releases, "controlli": sc.Checks, "commit": sc.Commits, "repository": sc.Discover} {
		if on {
			parts = append(parts, k)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// Stats sono i numeri di una sincronizzazione.
type Stats struct {
	Repos          int      `json:"repos"`
	Discovered     int      `json:"discovered,omitempty"`
	NewRepos       int      `json:"new_repos,omitempty"`
	Releases       int      `json:"releases"`
	SBOMs          int      `json:"sboms"`
	VulnReports    int      `json:"vuln_reports"`
	CheckRuns      int      `json:"check_runs"`
	Commits        int      `json:"commits"`
	Attachments    int      `json:"attachments"`
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

type repoCursors struct {
	Releases   map[string]string `json:"releases,omitempty"`
	ChecksMark string            `json:"checks_mark,omitempty"`
	Commits    commitsync.Cursor `json:"commits,omitzero"`
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
		err := tx.QueryRow(ctx, "select active from integrations where id = $1 and kind = 'gitea'", integrationID).Scan(&active)
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
		return audited(ctx, tx, a, "devsecops.gitea.sync", "integration", integrationID, map[string]any{"full": full, "run": c.runID})
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
				s.o.Log.Warn("connettore Gitea", "errore", err)
			} else if n > 0 {
				s.o.Log.Debug("connettore Gitea: sincronizzazioni eseguite", "numero", n)
			}
		}
	}
}

// RunDue esegue, una dopo l'altra, le sincronizzazioni dovute di tutti i tenant.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	rows, err := s.o.DB.Owner.Query(ctx, `
		select i.tenant_id::text, i.id::text from integrations i
		left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
		where i.kind = 'gitea' and i.active
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
	it      *Integration
	sec     secrets
	def     *profile.Definition
	version int
	repos   []repoRow
	verdict string
	env     string
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
		rows, err := tx.Query(ctx, selectRepo+` and r.integration_id = $1 and r.mapping_status = 'confirmed' and r.missing_since is null
			order by lower(r.full_name)`, integrationID)
		if err != nil {
			return err
		}
		all, err := scanRepos(rows)
		if err != nil {
			return err
		}
		for _, r := range all {
			if len(ids) == 0 || slices.Contains(ids, r.ID) {
				in.repos = append(in.repos, r)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	in.verdict, in.env = in.it.Config.VerdictCheck, in.it.Config.ReleaseEnvironment
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
		in, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.RepoIDs)
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
			if reset, ok := giteaapi.IsRateLimited(err); ok {
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
				st.fail("scoperta dei repository: " + err.Error())
			} else {
				st.Discovered, st.NewRepos = res.Found, res.New
				if in2, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.RepoIDs); err == nil {
					in.repos = in2.repos // i repository scomparsi non si sincronizzano più
				}
			}
		}
		for _, r := range in.repos {
			if ctx.Err() != nil {
				stop(ctx.Err())
				return
			}
			st.Repos++
			objs, cur, err := s.syncRepo(ctx, cl, in, r, sc)
			for _, w := range objs.warnings {
				st.warn(r.FullName + ": " + w)
			}
			if err != nil {
				s.repoError(ctx, c.tenantID, r.ID, err)
				if stop(err) {
					return
				}
				st.fail(r.FullName + ": " + err.Error())
				continue
			}
			linked, err := s.ingestRepo(ctx, c, r, objs, cur)
			if err != nil {
				s.repoError(ctx, c.tenantID, r.ID, err)
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
			st.Attachments += objs.attachments
			if objs.checkRun != nil {
				st.CheckRuns++
			}
			st.Commits += linked
		}
	}()

	// Prima sincronizzazione riuscita: le metriche DORA si calcolano subito (poi ogni ora).
	if status == "ok" || status == "partial" {
		if err := commitsync.RecomputeFirst(context.WithoutCancel(ctx), s.o.DB, c.tenantID, s.o.Ingestor); err != nil {
			st.warn("metriche DORA non ricalcolate: " + err.Error())
		}
	}
	if status == "ok" && len(st.Errors) > 0 {
		status = "partial"
		errMsg = st.Errors[0]
	}
	st.DurationMS = s.o.Now().Sub(start).Milliseconds()
	s.finish(context.WithoutCancel(ctx), c, st, status, errMsg, rate, rateReset)
}

func (s *Service) repoError(ctx context.Context, tenantID, id string, err error) {
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
		s.o.Log.Warn("connettore Gitea: esito della sincronizzazione non salvato", "integrazione", c.integrationID, "errore", err)
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
// Un repository
// ---------------------------------------------------------------------

type repoObjects struct {
	releases    []profile.ReleaseOutput
	checkRun    *contract.CheckRun
	commits     []contract.Commit
	warnings    []string
	attachments int
	// per l'anteprima
	present  []string
	fromFile []string
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

// releaseFingerprint cambia quando cambia la release o i suoi allegati (un allegato aggiunto
// dopo la pubblicazione): la release si rilegge.
func releaseFingerprint(r giteaapi.Release) string {
	var b strings.Builder
	b.WriteString(r.TagName)
	fmt.Fprintf(&b, "@%d#%d", r.At().Unix(), r.ID)
	var ls []string
	for _, l := range r.Assets {
		ls = append(ls, fmt.Sprintf("%s=%d", l.Name, l.Size))
	}
	slices.Sort(ls)
	for _, l := range ls {
		b.WriteString("|" + l)
	}
	return b.String()
}

// readRelease traduce una release con i suoi allegati. complete è falso se un allegato
// presente non si è scaricato: la release si riproverà.
func (s *Service) readRelease(ctx context.Context, cl *giteaapi.Client, def *profile.Definition, svc contract.Service, repo string,
	rel giteaapi.Release, env string) (profile.ReleaseOutput, resolved, bool, error) {
	res, err := s.resolveAssets(ctx, cl, rel, def)
	if err != nil {
		return profile.ReleaseOutput{}, res, false, err
	}
	in := profile.ReleaseInput{Service: svc, Tag: rel.TagName, Name: rel.Name, Author: rel.Author.Login, URL: rel.HTMLURL,
		PublishedAt: rel.At(), Assets: res.Assets, Present: res.Present}
	if sha, err := cl.TagCommit(ctx, repo, rel.TagName); err == nil && isSHA(sha) {
		in.Commit = sha
	} else if err != nil {
		if _, ok := giteaapi.IsRateLimited(err); ok || ctx.Err() != nil {
			return profile.ReleaseOutput{}, res, false, err
		}
	}
	out := def.ExtractRelease(in, env)
	out.Warnings = append(append([]string{}, res.Warnings...), out.Warnings...)
	return out, res, !res.Incomplete, nil
}

// statusInputs traduce gli stati di un commit nei check run del profilo.
func statusInputs(sts []giteaapi.Status) []profile.CheckRunInput {
	out := make([]profile.CheckRunInput, 0, len(sts))
	for _, st := range sts {
		ci := profile.CheckRunInput{ID: strconv.FormatInt(st.ID, 10), Name: st.Context, URL: st.TargetURL, Summary: truncate(st.Description, 300)}
		switch st.State {
		case "success":
			ci.Status, ci.Conclusion = "completed", "success"
		case "failure", "error":
			ci.Status, ci.Conclusion = "completed", "failure"
		case "warning":
			ci.Status, ci.Conclusion = "completed", "neutral"
		default: // pending
			ci.Status = "in_progress"
		}
		if ci.Status == "completed" {
			ci.CompletedAt = st.UpdatedAt.UTC()
			if ci.CompletedAt.IsZero() {
				ci.CompletedAt = st.CreatedAt.UTC()
			}
		}
		out = append(out, ci)
	}
	return out
}

// syncRepo legge da Gitea ciò che è cambiato in un repository (nessuna scrittura).
func (s *Service) syncRepo(ctx context.Context, cl *giteaapi.Client, in *syncInput, r repoRow, sc Scope) (repoObjects, repoCursors, error) {
	var objs repoObjects
	var cur repoCursors
	_ = json.Unmarshal(r.cursors, &cur)
	next := repoCursors{Releases: map[string]string{}, ChecksMark: cur.ChecksMark, Commits: cur.Commits}
	for k, v := range cur.Releases {
		next.Releases[k] = v
	}
	svc := serviceOf(r.Repo)
	def := in.def

	if sc.Releases {
		var todo []giteaapi.Release
		seenRun := 0
		err := cl.Releases(ctx, r.FullName, 5, func(rel giteaapi.Release) (bool, error) {
			if rel.Draft || !def.AcceptsTag(rel.TagName, rel.Prerelease) {
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
			return objs, cur, fmt.Errorf("release: %w", err)
		}
		for i := len(todo) - 1; i >= 0; i-- { // dalla più vecchia: l'ordine dei rilasci conta
			rel := todo[i]
			out, res, complete, err := s.readRelease(ctx, cl, def, svc, r.FullName, rel, in.env)
			if err != nil {
				return objs, cur, err
			}
			objs.warnings = append(objs.warnings, out.Warnings...)
			objs.attachments += res.Downloaded
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

	if sc.Checks && r.DefaultBranch != "" {
		cs, err := cl.CombinedStatus(ctx, r.FullName, r.DefaultBranch)
		if err != nil {
			return objs, cur, fmt.Errorf("stati del branch %s: %w", r.DefaultBranch, err)
		}
		if cs.SHA != "" && len(cs.Statuses) > 0 {
			runs := statusInputs(cs.Statuses)
			cr, ready := def.BuildCheckRun(svc, cs.SHA, r.DefaultBranch, runs, in.verdict)
			mark := cs.SHA + ":" + cs.State
			if ready && (sc.Full || mark != cur.ChecksMark) {
				if cr.RunID == "" {
					cr.RunID = cs.SHA[:min(len(cs.SHA), 12)]
				}
				if cr.At.IsZero() {
					cr.At = s.o.Now().UTC()
				}
				objs.checkRun = &cr
				next.ChecksMark = mark
			}
		}
	}

	if sc.Commits && r.DefaultBranch != "" {
		res, err := commitsync.Read(ctx, commitSource{cl: cl, repo: r.FullName, branch: r.DefaultBranch}, svc, cur.Commits, sc.Full, s.o.Now())
		if err != nil {
			// I commit sono un di più: un errore (repository vuoto, Gitea vecchio senza `since`) non ferma
			// release e controlli; il cursore dei commit non avanza e la volta dopo si riprova.
			if _, ok := giteaapi.IsRateLimited(err); ok || ctx.Err() != nil {
				return objs, cur, err
			}
			objs.warnings = append(objs.warnings, err.Error())
		} else {
			objs.commits = res.Commits
			next.Commits = res.Next
			objs.warnings = append(objs.warnings, res.Warnings...)
		}
	}
	return objs, next, nil
}

// ingestRepo consegna gli oggetti di un repository e ne salva i cursori, insieme.
// ingestRepo restituisce quanti collegamenti commit → elemento nuovi ha creato.
func (s *Service) ingestRepo(ctx context.Context, c *claim, r repoRow, objs repoObjects, cur repoCursors) (int, error) {
	linked := 0
	curJSON, _ := json.Marshal(cur)
	ing := s.o.Ingestor
	act := systemActor(c.actor)
	err := s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		linked = 0
		if si, ok := ing.(contract.ServiceIngestor); ok {
			if err := si.IngestService(ctx, tx, act, serviceOf(r.Repo)); err != nil {
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
		if objs.checkRun != nil {
			if err := ing.IngestCheckRun(ctx, tx, act, *objs.checkRun); err != nil {
				return err
			}
		}
		if ci, ok := ing.(contract.CommitIngestor); ok && len(objs.commits) > 0 {
			n, err := ci.IngestCommits(ctx, tx, act, serviceOf(r.Repo), objs.commits)
			if err != nil {
				return err
			}
			linked = n
		}
		if _, err := tx.Exec(ctx, `update connector_repositories set cursors = $2, last_synced_at = now(), last_error = null, updated_at = now()
			where id = $1`, r.ID, curJSON); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "update connector_sync_state set lease_until = now() + make_interval(mins => $2) where integration_id = $1",
			c.integrationID, leaseMinutes)
		return err
	})
	return linked, err
}
