package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/commitsync"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Sincronizzazione: il job in-process (Run) è il meccanismo principale, perché nelle reti
// aziendali i webhook spesso non arrivano; "Sincronizza ora" e i webhook avviano la stessa
// procedura, eventualmente limitata (Scope).
//
//   - Incrementale: cursori per repository (Release già lette con la loro impronta,
//     ultimo stato visto di ogni deployment, commit dei check run già riportato) e per
//     repository GitOps (commit del branch, sha di ogni file degli ambienti). Gli oggetti di
//     un repository e i suoi cursori si salvano nella stessa transazione: un errore non fa
//     avanzare i cursori, e la volta dopo si riprova.
//   - Idempotente: rileggere lo stesso dato non produce oggetti nuovi (cursori) e, se
//     capita (sincronizzazione completa, webhook ripetuti), l'Ingestor lo riconosce.
//   - Limiti di velocità: il client aspetta fino a MaxWait; oltre, la sincronizzazione si
//     ferma con lo stato rate_limited e riprende dopo il reset.
//   - Una sola sincronizzazione per integrazione alla volta (lease nel database, valido
//     anche con più processi).

// Scope limita una sincronizzazione; tutto falso = tutto.
type Scope struct {
	RepoIDs     []string `json:"repos,omitempty"`
	Releases    bool     `json:"releases,omitempty"`
	Deployments bool     `json:"deployments,omitempty"`
	Checks      bool     `json:"checks,omitempty"`
	Commits     bool     `json:"commits,omitempty"`
	GitOps      bool     `json:"gitops,omitempty"`
	Discover    bool     `json:"discover,omitempty"`
	// Full ignora i cursori e rilegge tutto (l'Ingestor scarta i doppioni).
	Full bool `json:"full,omitempty"`
}

func (sc Scope) all() bool {
	return !sc.Releases && !sc.Deployments && !sc.Checks && !sc.Commits && !sc.GitOps && !sc.Discover
}

func (sc Scope) normalized() Scope {
	if sc.all() {
		sc.Releases, sc.Deployments, sc.Checks, sc.Commits, sc.GitOps, sc.Discover = true, true, true, true, true, len(sc.RepoIDs) == 0
	}
	return sc
}

func (sc Scope) label() string {
	if sc.all() || (sc.Releases && sc.Deployments && sc.Checks && sc.Commits && sc.GitOps && sc.Discover) {
		if sc.Full {
			return "completa"
		}
		return "tutto"
	}
	var parts []string
	for k, on := range map[string]bool{"release": sc.Releases, "deployment": sc.Deployments, "controlli": sc.Checks, "commit": sc.Commits, "gitops": sc.GitOps, "repository": sc.Discover} {
		if on {
			parts = append(parts, k)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// Stats sono i numeri di una sincronizzazione.
type Stats struct {
	Repositories   int      `json:"repositories"`
	Discovered     int      `json:"discovered,omitempty"`
	NewRepos       int      `json:"new_repositories,omitempty"`
	Releases       int      `json:"releases"`
	SBOMs          int      `json:"sboms"`
	VulnReports    int      `json:"vuln_reports"`
	Deployments    int      `json:"deployments"`
	GitOpsDeploys  int      `json:"gitops_deployments"`
	CheckRuns      int      `json:"check_runs"`
	Commits        int      `json:"commits"`
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
	Releases    map[string]string `json:"releases,omitempty"`
	Deployments map[string]string `json:"deployments,omitempty"`
	ChecksSHA   string            `json:"checks_sha,omitempty"`
	Commits     commitsync.Cursor `json:"commits,omitzero"`
}

type gitopsCursor struct {
	Branch string            `json:"branch,omitempty"`
	Head   string            `json:"head,omitempty"`
	Files  map[string]string `json:"files,omitempty"`
	// Index è l'impronta dei servizi associati: se cambia (un repository appena
	// confermato) i file si rileggono tutti, anche se non sono cambiati.
	Index string `json:"index,omitempty"`
}

type stateCursors struct {
	GitOps map[string]gitopsCursor `json:"gitops,omitempty"`
}

// claim è una sincronizzazione che ha ottenuto il lease.
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
		err := tx.QueryRow(ctx, "select active from integrations where id = $1 and kind = 'github'", integrationID).Scan(&active)
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
		return audited(ctx, tx, a, "devsecops.github.sync", "integration", integrationID, map[string]any{"full": full, "run": c.runID})
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
				s.o.Log.Warn("connettore GitHub", "errore", err)
			} else if n > 0 {
				s.o.Log.Debug("connettore GitHub: sincronizzazioni eseguite", "numero", n)
			}
			// Stato delle pull request di adozione aperte da Regia (adopt.go).
			if _, err := s.SyncAdoptionsDue(ctx); err != nil {
				s.o.Log.Warn("connettore GitHub: pull request di adozione", "errore", err)
			}
		}
	}
}

// RunDue esegue, una dopo l'altra, le sincronizzazioni dovute di tutti i tenant.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	// Solo l'elenco passa dal ruolo di piattaforma; il resto gira sotto RLS.
	rows, err := s.o.DB.Owner.Query(ctx, `
		select i.tenant_id::text, i.id::text from integrations i
		left join connector_sync_state st on st.tenant_id = i.tenant_id and st.integration_id = i.id
		where i.kind = 'github' and i.active
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

// syncInput è ciò che serve a una sincronizzazione, letto all'inizio.
type syncInput struct {
	it       *Integration
	sec      secrets
	def      *profile.Definition
	version  int
	repos    []repoRow
	all      []repoRow // tutti i confermati (per associare i servizi GitOps)
	cursors  stateCursors
	verdict  string
	env      string
	gitRepos []string
}

func (s *Service) loadInput(ctx context.Context, tenantID, integrationID string, repoIDs []string) (*syncInput, error) {
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
		if in.all, err = scanRepos(rows); err != nil {
			return err
		}
		for _, r := range in.all {
			if len(repoIDs) == 0 || slices.Contains(repoIDs, r.ID) {
				in.repos = append(in.repos, r)
			}
		}
		var raw []byte
		if err := tx.QueryRow(ctx, "select cursors from connector_sync_state where integration_id = $1", integrationID).Scan(&raw); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_ = json.Unmarshal(raw, &in.cursors)
		return nil
	})
	if err != nil {
		return nil, err
	}
	in.verdict = in.it.Config.VerdictCheck
	in.env = in.it.Config.ReleaseEnvironment
	in.gitRepos = in.it.Config.GitOpsRepositories
	if len(in.gitRepos) == 0 && in.def != nil && in.def.GitOps != nil {
		in.gitRepos = in.def.GitOps.Repositories
	}
	return in, nil
}

// execute esegue una sincronizzazione già reclamata e ne registra l'esito.
func (s *Service) execute(ctx context.Context, c *claim) {
	start := s.o.Now()
	st := Stats{}
	status := "ok"
	var errMsg string
	var rate *ghapi.RateLimit
	var newCursors *stateCursors
	var rateReset time.Time
	sc := c.scope.normalized()

	func() {
		in, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.RepoIDs)
		if err != nil {
			status, errMsg = "error", err.Error()
			return
		}
		st.ProfileVersion = in.version
		cl, err := s.client(c.integrationID, in.it.Config, in.sec)
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
			if reset, ok := ghapi.IsRateLimited(err); ok {
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
				// Repository scomparsi: non si sincronizzano più.
				if in2, err := s.loadInput(ctx, c.tenantID, c.integrationID, sc.RepoIDs); err == nil {
					in.repos, in.all = in2.repos, in2.all
				}
			}
		}
		for _, r := range in.repos {
			if ctx.Err() != nil {
				stop(ctx.Err())
				return
			}
			st.Repositories++
			objs, cur, warns, err := s.syncRepo(ctx, cl, in, r, sc)
			for _, w := range warns {
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
			st.Deployments += len(objs.deployments)
			if objs.checkRun != nil {
				st.CheckRuns++
			}
			st.Commits += linked
		}
		if sc.GitOps && len(in.gitRepos) > 0 {
			if in.def == nil || in.def.GitOps == nil {
				st.warn("per leggere i repository GitOps serve un profilo con la sezione gitops")
				return
			}
			cursors := in.cursors
			if cursors.GitOps == nil {
				cursors.GitOps = map[string]gitopsCursor{}
			}
			idx := newServiceIndex(in.all)
			for _, g := range in.gitRepos {
				deps, cur, warns, err := s.syncGitOps(ctx, cl, in.def.GitOps, g, cursors.GitOps[g], sc.Full, idx)
				for _, w := range warns {
					st.warn(g + ": " + w)
				}
				if err != nil {
					if stop(err) {
						break
					}
					st.fail(g + ": " + err.Error())
					continue
				}
				if err := s.ingestGitOps(ctx, c, deps); err != nil {
					st.fail(g + ": archiviazione: " + err.Error())
					continue
				}
				cursors.GitOps[g] = cur
				st.GitOpsDeploys += len(deps)
			}
			newCursors = &cursors
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
	s.finish(context.WithoutCancel(ctx), c, st, status, errMsg, rate, rateReset, newCursors)
}

func (s *Service) repoError(ctx context.Context, tenantID, repoID string, err error) {
	_ = s.o.DB.InTenant(context.WithoutCancel(ctx), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "update connector_repositories set last_error = $2, updated_at = now() where id = $1", repoID, truncate(err.Error(), 1000))
		return e
	})
}

func (s *Service) finish(ctx context.Context, c *claim, st Stats, status, errMsg string, rate *ghapi.RateLimit, reset time.Time, cursors *stateCursors) {
	statsJSON, _ := json.Marshal(st)
	var rateJSON []byte
	if rate != nil {
		rateJSON, _ = json.Marshal(rate)
	}
	var curJSON []byte
	if cursors != nil {
		curJSON, _ = json.Marshal(cursors)
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
			rate_limit = coalesce($4::jsonb, rate_limit), cursors = coalesce($5::jsonb, cursors),
			next_run_at = case when $7 then next_run_at else $6 end
			where integration_id = $1`, c.integrationID, status, errp, rateJSON, curJSON, next, keepNext); err != nil {
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
		s.o.Log.Warn("connettore GitHub: esito della sincronizzazione non salvato", "integrazione", c.integrationID, "errore", err)
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
	deployments []contract.Deployment
	checkRun    *contract.CheckRun
	commits     []contract.Commit
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

// releaseFingerprint cambia quando cambiano gli allegati letti dal profilo (un finalize che
// completa il registro dopo la pubblicazione): la Release si rilegge.
func releaseFingerprint(r ghapi.Release, def *profile.Definition) string {
	var b strings.Builder
	b.WriteString(r.TagName)
	names := def.AssetNames()
	for _, a := range r.Assets {
		if slices.Contains(names, a.Name) {
			fmt.Fprintf(&b, "|%s@%d:%d", a.Name, a.UpdatedAt.Unix(), a.Size)
		}
	}
	return b.String()
}

// readRelease scarica gli allegati del profilo e traduce la Release. complete è falso se
// un allegato presente non si è potuto scaricare (la Release si riproverà).
func (s *Service) readRelease(ctx context.Context, cl *ghapi.Client, def *profile.Definition, svc contract.Service, full string, r ghapi.Release, env string) (profile.ReleaseOutput, bool, error) {
	in := profile.ReleaseInput{Service: svc, Tag: r.TagName, Name: r.Name, Author: r.Author.Login, URL: r.HTMLURL, Assets: map[string][]byte{}}
	if r.PublishedAt != nil {
		in.PublishedAt = *r.PublishedAt
	} else {
		in.PublishedAt = r.CreatedAt
	}
	if isSHA(r.TargetCommitish) {
		in.Commit = r.TargetCommitish
	}
	names := def.AssetNames()
	complete := true
	var warns []string
	for _, a := range r.Assets {
		in.Present = append(in.Present, a.Name)
		if !slices.Contains(names, a.Name) {
			continue
		}
		if a.Size > def.MaxAssetBytes() {
			warns = append(warns, fmt.Sprintf("allegato %s di %s troppo grande (%d MB): ignorato", a.Name, r.TagName, a.Size>>20))
			continue
		}
		b, err := cl.DownloadAsset(ctx, full, a.ID, def.MaxAssetBytes())
		if err != nil {
			if _, ok := ghapi.IsRateLimited(err); ok || ctx.Err() != nil {
				return profile.ReleaseOutput{}, false, err
			}
			warns = append(warns, fmt.Sprintf("allegato %s di %s non scaricato: %v", a.Name, r.TagName, err))
			complete = false
			continue
		}
		in.Assets[a.Name] = b
	}
	out := def.ExtractRelease(in, env)
	out.Warnings = append(warns, out.Warnings...)
	return out, complete, nil
}

// syncRepo legge da GitHub ciò che è cambiato in un repository (nessuna scrittura).
func (s *Service) syncRepo(ctx context.Context, cl *ghapi.Client, in *syncInput, r repoRow, sc Scope) (repoObjects, repoCursors, []string, error) {
	var objs repoObjects
	var warns []string
	var cur repoCursors
	_ = json.Unmarshal(r.cursors, &cur)
	next := repoCursors{Releases: map[string]string{}, Deployments: map[string]string{}, ChecksSHA: cur.ChecksSHA, Commits: cur.Commits}
	for k, v := range cur.Releases {
		next.Releases[k] = v
	}
	for k, v := range cur.Deployments {
		next.Deployments[k] = v
	}
	svc := r.service()
	def := in.def

	if sc.Releases {
		var todo []ghapi.Release
		seenRun := 0
		err := cl.Releases(ctx, r.FullName, 5, func(rel ghapi.Release) (bool, error) {
			if rel.Draft || !def.AcceptsTag(rel.TagName, rel.Prerelease) {
				return false, nil
			}
			id := strconv.FormatInt(rel.ID, 10)
			if !sc.Full && cur.Releases[id] == releaseFingerprint(rel, def) {
				seenRun++
				return seenRun >= 10, nil // dieci già lette di fila: il resto è storia nota
			}
			seenRun = 0
			todo = append(todo, rel)
			return len(todo) >= maxReleasesPerSync, nil
		})
		if err != nil {
			return objs, cur, warns, fmt.Errorf("Release: %w", err)
		}
		for i := len(todo) - 1; i >= 0; i-- { // dalla più vecchia: l'ordine dei rilasci conta
			rel := todo[i]
			out, complete, err := s.readRelease(ctx, cl, def, svc, r.FullName, rel, in.env)
			if err != nil {
				return objs, cur, warns, err
			}
			warns = append(warns, out.Warnings...)
			objs.releases = append(objs.releases, out)
			if complete {
				next.Releases[strconv.FormatInt(rel.ID, 10)] = releaseFingerprint(rel, def)
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
		deps, err := cl.Deployments(ctx, r.FullName, def.DeploymentsMax())
		if err != nil {
			return objs, cur, warns, fmt.Errorf("Deployments: %w", err)
		}
		listed := map[string]bool{}
		for i := len(deps) - 1; i >= 0; i-- {
			d := deps[i]
			id := strconv.FormatInt(d.ID, 10)
			listed[id] = true
			prev := cur.Deployments[id]
			if !sc.Full && terminalMark(prev) {
				continue
			}
			stt, err := cl.LatestDeploymentStatus(ctx, r.FullName, d.ID)
			if err != nil {
				return objs, cur, warns, fmt.Errorf("stato del deployment %d: %w", d.ID, err)
			}
			state, at, mark := "pending", d.CreatedAt, "0:pending"
			if stt != nil {
				state, at, mark = stt.State, stt.CreatedAt, strconv.FormatInt(stt.ID, 10)+":"+stt.State
			}
			if !sc.Full && prev == mark {
				continue
			}
			version, digest := def.DeploymentVersion(d.Raw)
			if version == "" && len(d.SHA) >= 12 {
				version = d.SHA[:12]
			}
			objs.deployments = append(objs.deployments, contract.Deployment{
				Service: svc, Environment: def.DeploymentEnv(d.Environment), Version: version, Digest: digest,
				Status: profile.DeploymentStatus(state), At: at.UTC(), Source: "pipeline", ExternalRef: "github:deployment:" + id,
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
		sha, err := cl.BranchHead(ctx, r.FullName, r.DefaultBranch)
		if err != nil {
			return objs, cur, warns, fmt.Errorf("branch %s: %w", r.DefaultBranch, err)
		}
		if sc.Full || sha != cur.ChecksSHA {
			runs, err := cl.CheckRuns(ctx, r.FullName, sha)
			if err != nil {
				return objs, cur, warns, fmt.Errorf("check run: %w", err)
			}
			inputs := make([]profile.CheckRunInput, 0, len(runs))
			for _, x := range runs {
				ci := profile.CheckRunInput{ID: strconv.FormatInt(x.ID, 10), Name: x.Name, Status: x.Status, Conclusion: x.Conclusion,
					URL: x.HTMLURL, Summary: x.Output.Title}
				if x.CompletedAt != nil {
					ci.CompletedAt = x.CompletedAt.UTC()
				}
				inputs = append(inputs, ci)
			}
			cr, ready := def.BuildCheckRun(svc, sha, r.DefaultBranch, inputs, in.verdict)
			if ready {
				if cr.At.IsZero() {
					cr.At = s.o.Now().UTC()
				}
				objs.checkRun = &cr
				next.ChecksSHA = sha
			}
		}
	}

	if sc.Commits {
		res, err := s.readCommits(ctx, cl, r, svc, cur.Commits, sc.Full)
		if err != nil {
			// I commit sono un di più: un errore (repository vuoto, ecc.) non ferma release e controlli;
			// il cursore dei commit non avanza e la volta dopo si riprova.
			if _, ok := ghapi.IsRateLimited(err); ok || ctx.Err() != nil {
				return objs, cur, warns, err
			}
			warns = append(warns, err.Error())
		} else {
			objs.commits = res.Commits
			next.Commits = res.Next
			warns = append(warns, res.Warnings...)
		}
	}
	return objs, next, warns, nil
}

func terminalMark(m string) bool {
	_, state, ok := strings.Cut(m, ":")
	return ok && (state == "success" || state == "failure" || state == "error" || state == "inactive")
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
			if err := si.IngestService(ctx, tx, act, r.service()); err != nil {
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
		if ci, ok := ing.(contract.CommitIngestor); ok && len(objs.commits) > 0 {
			n, err := ci.IngestCommits(ctx, tx, act, r.service(), objs.commits)
			if err != nil {
				return err
			}
			linked = n
		}
		if _, err := tx.Exec(ctx, `update connector_repositories set cursors = $2, last_synced_at = now(), last_error = null, updated_at = now()
			where id = $1`, r.ID, curJSON); err != nil {
			return err
		}
		// La sincronizzazione è viva: il lease si allunga.
		_, err := tx.Exec(ctx, "update connector_sync_state set lease_until = now() + make_interval(mins => $2) where integration_id = $1",
			c.integrationID, leaseMinutes)
		return err
	})
	return linked, err
}

func (s *Service) ingestGitOps(ctx context.Context, c *claim, deps []contract.Deployment) error {
	if len(deps) == 0 {
		return nil
	}
	act := systemActor(c.actor)
	return s.o.DB.InTenant(ctx, c.tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		for _, d := range deps {
			if err := s.o.Ingestor.IngestDeployment(ctx, tx, act, d); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------
// Repository GitOps
// ---------------------------------------------------------------------

// serviceIndex associa i servizi letti dai file degli ambienti ai repository confermati.
type serviceIndex map[string]contract.Service

func newServiceIndex(repos []repoRow) serviceIndex {
	idx := serviceIndex{}
	for _, r := range repos {
		svc := r.service()
		for _, k := range []string{strings.ToLower(r.Name), strings.ToLower(r.ServiceName)} {
			if _, ok := idx[k]; !ok || k == strings.ToLower(r.ServiceName) {
				idx[k] = svc
			}
		}
	}
	return idx
}

func (idx serviceIndex) fingerprint() string {
	keys := make([]string, 0, len(idx))
	for k, v := range idx {
		keys = append(keys, k+"="+v.Name+"/"+v.Application)
	}
	slices.Sort(keys)
	h := sha256.Sum256([]byte(strings.Join(keys, ",")))
	return hex.EncodeToString(h[:8])
}

func (idx serviceIndex) match(es profile.EnvService) (contract.Service, bool) {
	for _, k := range []string{es.Name, es.Key, profile.ImageName(es.Image)} {
		if svc, ok := idx[strings.ToLower(strings.TrimSpace(k))]; ok && k != "" {
			return svc, true
		}
	}
	return contract.Service{}, false
}

// syncGitOps legge lo stato degli ambienti di un repository GitOps.
func (s *Service) syncGitOps(ctx context.Context, cl *ghapi.Client, spec *profile.GitOpsSpec, repo string, cur gitopsCursor, full bool, idx serviceIndex) ([]contract.Deployment, gitopsCursor, []string, error) {
	var warns []string
	branch := spec.Branch
	if branch == "" {
		branch = cur.Branch
	}
	if branch == "" {
		rp, err := cl.Repo(ctx, repo)
		if err != nil {
			return nil, cur, warns, err
		}
		branch = rp.DefaultBranch
	}
	head, err := cl.BranchHead(ctx, repo, branch)
	if err != nil {
		return nil, cur, warns, err
	}
	if cur.Index != idx.fingerprint() {
		full = true
	}
	if !full && head == cur.Head && branch == cur.Branch {
		return nil, cur, warns, nil
	}
	envs := spec.Environments
	if len(envs) == 0 {
		base, ok := spec.EnvBase()
		if !ok {
			return nil, cur, warns, errors.New("il percorso degli ambienti non finisce con {environment}: elenca gli ambienti nel profilo")
		}
		entries, err := cl.Dir(ctx, repo, base, head)
		if err != nil {
			return nil, cur, warns, fmt.Errorf("cartella %s: %w", base, err)
		}
		for _, e := range entries {
			if e.Type == "dir" {
				envs = append(envs, e.Name)
			}
		}
	}
	next := gitopsCursor{Branch: branch, Head: head, Files: map[string]string{}, Index: idx.fingerprint()}
	var out []contract.Deployment
	for _, env := range envs {
		dir := spec.EnvPath(env)
		entries, err := cl.Dir(ctx, repo, dir, head)
		if ghapi.IsNotFound(err) {
			warns = append(warns, "cartella "+dir+" assente")
			continue
		}
		if err != nil {
			return nil, cur, warns, fmt.Errorf("cartella %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.Type != "file" || !spec.MatchFile(e.Name) {
				continue
			}
			next.Files[e.Path] = e.SHA
			if !full && cur.Files[e.Path] == e.SHA {
				continue
			}
			raw, _, err := cl.File(ctx, repo, e.Path, head)
			if err != nil {
				return nil, cur, warns, fmt.Errorf("file %s: %w", e.Path, err)
			}
			services, err := spec.ParseEnvFile(e.Name, raw)
			if err != nil {
				warns = append(warns, err.Error())
				continue
			}
			commit, at, err := cl.LastCommitTime(ctx, repo, e.Path, head)
			if err != nil {
				return nil, cur, warns, err
			}
			if at.IsZero() {
				at, commit = s.o.Now().UTC(), head
			}
			if len(commit) > 12 {
				commit = commit[:12]
			}
			for _, es := range services {
				svc, ok := idx.match(es)
				if !ok {
					warns = append(warns, fmt.Sprintf("servizio %s in %s senza repository associato", es.Key, env))
					continue
				}
				out = append(out, contract.Deployment{Service: svc, Environment: spec.EnvName(env), Version: es.Version, Digest: es.Digest,
					Status: "running", At: at, Source: "gitops", ExternalRef: repo + "@" + commit + ":" + e.Path})
			}
		}
	}
	return out, next, warns, nil
}
