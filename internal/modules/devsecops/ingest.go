package devsecops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/requirements"
	"regia/internal/platform/db"
)

// Ingestor: ogni metodo è idempotente (lo stesso oggetto ricevuto due volte non crea
// duplicati) e lavora nella transazione del tenant. I servizi si creano al primo dato,
// gli ambienti al primo utilizzo, i change al primo rilascio che li cita.

// svcRef è un servizio risolto: id e applicazione associata (se c'è).
type svcRef struct {
	ID    string
	AppID string
	Name  string
}

var providers = map[string]bool{"github": true, "gitlab": true, "gitea": true, "azure_devops": true, "bitbucket": true, "other": true}

func cleanURL(s string) string {
	s = clip(s, 2000)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "" // solo link http(s): l'interfaccia li mostra come collegamenti
	}
	return s
}

// appByRef trova un'applicazione per codice del catalogo o chiave (ARC-5), o per id.
func appByRef(ctx context.Context, tx pgx.Tx, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	var id string
	err := tx.QueryRow(ctx, `select e.id::text from elements e
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.deleted_at is null and (lower(a.code) = lower($1) or s.key || '-' || e.number = upper($1) or e.id::text = $1)
		order by (lower(a.code) = lower($1)) desc nulls last limit 1`, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ensureService crea o aggiorna il servizio. source: "pipeline" (dati della pipeline o di un
// connettore) o "manual" (interfaccia). touch: il servizio è sulla pipeline (ha mandato dati).
// L'associazione fatta dall'interfaccia prevale su quella dichiarata dalla pipeline.
func (s *Service) ensureService(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.Service, source string, touch bool) (svcRef, error) {
	name := clip(in.Name, 200)
	if name == "" {
		name = clip(in.Repository, 200)
	}
	if name == "" {
		return svcRef{}, engine.Required("service.name", "serve il nome del servizio")
	}
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider != "" && !providers[provider] {
		provider = "other"
	}
	if err := lock(ctx, tx, "dso/svc/"+strings.ToLower(name)); err != nil {
		return svcRef{}, err
	}
	declared := clip(in.Application, 120)
	appID, err := appByRef(ctx, tx, declared)
	if err != nil {
		return svcRef{}, err
	}
	var ref svcRef
	var curApp, curSource *string
	err = tx.QueryRow(ctx, "select id::text, application_element_id::text, mapping_source from services where lower(name) = lower($1)", name).
		Scan(&ref.ID, &curApp, &curSource)
	ref.Name = name
	now := s.now()
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ref.ID = db.NewID()
		var src any
		if appID != "" {
			src = source
		}
		var seen any
		if touch {
			seen = now
		}
		if _, err := tx.Exec(ctx, `insert into services (tenant_id, id, name, repository, provider, url, application_element_id,
			declared_application, mapping_source, first_seen_at, last_seen_at, created_at)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10)`,
			ref.ID, name, nullable(clip(in.Repository, 300)), nullable(provider), nullable(cleanURL(in.URL)), nullable(appID),
			nullable(declared), src, seen, now); err != nil {
			return svcRef{}, err
		}
		ref.AppID = appID
		return ref, auditEvent(ctx, tx, actor, "devsecops.service.create", "service", ref.ID,
			map[string]any{"application": appID, "source": source})
	case err != nil:
		return svcRef{}, err
	}
	if curApp != nil {
		ref.AppID = *curApp
	}
	// Associazione: la pipeline non scavalca quella manuale; dall'interfaccia vale sempre.
	newApp := ref.AppID
	if appID != "" && (source == "manual" || curSource == nil || *curSource != "manual") {
		newApp = appID
	}
	if _, err := tx.Exec(ctx, `update services set
		repository = coalesce($2, repository), provider = coalesce($3, provider), url = coalesce($4, url),
		declared_application = coalesce($5, declared_application),
		application_element_id = $6, mapping_source = case when $6::uuid is distinct from application_element_id then $7 else mapping_source end,
		first_seen_at = case when $8 then coalesce(first_seen_at, $9) else first_seen_at end,
		last_seen_at = case when $8 then $9 else last_seen_at end
		where id = $1`,
		ref.ID, nullable(clip(in.Repository, 300)), nullable(provider), nullable(cleanURL(in.URL)), nullable(declared),
		nullable(newApp), source, touch, now); err != nil {
		return svcRef{}, err
	}
	if newApp != ref.AppID {
		if err := s.remap(ctx, tx, actor, ref.ID, ref.AppID, newApp, source); err != nil {
			return svcRef{}, err
		}
		ref.AppID = newApp
	}
	return ref, nil
}

// remap allinea all'associazione nuova i deploy del servizio e l'uso dei componenti.
func (s *Service) remap(ctx context.Context, tx pgx.Tx, actor engine.Actor, serviceID, oldApp, newApp, source string) error {
	if _, err := tx.Exec(ctx, "update deployments set application_element_id = $2 where service_id = $1", serviceID, nullable(newApp)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update findings set application_element_id = $2 where service_id = $1", serviceID, nullable(newApp)); err != nil {
		return err
	}
	if newApp != "" {
		if _, err := tx.Exec(ctx, `insert into component_usages (tenant_id, application_element_id, component_id)
			select distinct app_tenant(), $2::uuid, sc.component_id from sbom_components sc join sboms b on b.tenant_id = sc.tenant_id and b.id = sc.sbom_id
			where b.service_id = $1 on conflict do nothing`, serviceID, newApp); err != nil {
			return err
		}
	}
	return auditEvent(ctx, tx, actor, "devsecops.service.map", "service", serviceID,
		map[string]any{"from": oldApp, "to": newApp, "source": source})
}

// IngestService registra un servizio (repository scoperto, servizio censito) senza segnarlo
// come già sulla pipeline.
func (s *Service) IngestService(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.Service) error {
	_, err := s.ensureService(ctx, tx, actor, in, "pipeline", false)
	return err
}

// RegisterService è IngestService con la fonte esplicita e il risultato (API di push e
// interfaccia).
func (s *Service) RegisterService(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.Service, source string) (*ServiceView, error) {
	ref, err := s.ensureService(ctx, tx, actor, in, source, false)
	if err != nil {
		return nil, err
	}
	return s.serviceView(ctx, tx, ref.ID)
}

// ---------------------------------------------------------------------------
// Ambienti

var envOrdinals = map[string]int{
	"dev": 10, "development": 10, "sviluppo": 10, "int": 15, "integration": 15, "test": 20, "qa": 25, "collaudo": 30, "uat": 30,
	"staging": 30, "stage": 30, "preprod": 40, "pre-prod": 40, "preproduzione": 40, "prod": 50, "production": 50, "produzione": 50,
}

// ensureEnv trova o crea l'ambiente; è di produzione se il nome è tra quelli delle impostazioni.
func ensureEnv(ctx context.Context, tx pgx.Tx, name string, st Settings) (string, bool, error) {
	name = normEnv(name)
	if name == "" {
		name = st.ProductionEnvs[0]
	}
	var id string
	var prod bool
	err := tx.QueryRow(ctx, "select id::text, is_production from environments where lower(name) = $1", name).Scan(&id, &prod)
	if err == nil {
		return id, prod, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	prod = st.isProduction(name)
	ord, ok := envOrdinals[name]
	if !ok {
		ord = 35
	}
	if prod {
		ord = 50
	}
	if _, err := tx.Exec(ctx, `insert into environments (tenant_id, id, name, is_production, ordinal) values (app_tenant(), $1, $2, $3, $4)
		on conflict do nothing`, db.NewID(), name, prod, ord); err != nil {
		return "", false, err
	}
	err = tx.QueryRow(ctx, "select id::text, is_production from environments where lower(name) = $1", name).Scan(&id, &prod)
	return id, prod, err
}

// ---------------------------------------------------------------------------
// Change

func normChange(s string) string { return strings.ToUpper(clip(s, 120)) }

// ensureChange trova o crea il change del numero (o di un suo alias) e prova a collegarlo
// alla richiesta. Gli alias sono numeri provvisori: se esiste già un change con un alias come
// numero, quel change prende il numero definitivo; se ne esistono due si uniscono.
func (s *Service) ensureChange(ctx context.Context, tx pgx.Tx, actor engine.Actor, number, title string, aliases []string, st Settings) (string, string, error) {
	number = normChange(number)
	if number == "" {
		return "", "", nil
	}
	als := []string{}
	for _, a := range aliases {
		if a = normChange(a); a != "" && a != number && !slices.Contains(als, a) && len(als) < 20 {
			als = append(als, a)
		}
	}
	if err := lock(ctx, tx, "dso/change"); err != nil { // i change sono pochi: un solo lucchetto per tenant
		return "", "", err
	}
	all := append([]string{number}, als...)
	rows, err := tx.Query(ctx, `select id::text, number, aliases, coalesce(request_element_id::text, '') from changes
		where number = any($1) or aliases && $1 order by (number = $2) desc, created_at`, all, number)
	if err != nil {
		return "", "", err
	}
	type chg struct {
		id, number string
		aliases    []string
		request    string
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (chg, error) {
		var c chg
		err := r.Scan(&c.id, &c.number, &c.aliases, &c.request)
		return c, err
	})
	if err != nil {
		return "", "", err
	}
	var id, request string
	if len(found) == 0 {
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into changes (tenant_id, id, number, aliases, title) values (app_tenant(), $1, $2, $3, $4)`,
			id, number, als, clip(title, 300)); err != nil {
			return "", "", err
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.change.create", "change", id, map[string]any{"number": number, "aliases": als}); err != nil {
			return "", "", err
		}
	} else {
		keep := found[0]
		id, request = keep.id, keep.request
		merged := slices.Clone(keep.aliases)
		for _, c := range found[1:] {
			merged = append(merged, c.number)
			merged = append(merged, c.aliases...)
			if request == "" {
				request = c.request
			}
			if _, err := tx.Exec(ctx, "update releases set change_id = $1 where change_id = $2", id, c.id); err != nil {
				return "", "", err
			}
			if _, err := tx.Exec(ctx, "delete from changes where id = $1", c.id); err != nil {
				return "", "", err
			}
		}
		if keep.number != number {
			merged = append(merged, keep.number) // il numero provvisorio diventa un alias
		}
		merged = append(merged, als...)
		clean := []string{}
		for _, a := range merged {
			if a != number && !slices.Contains(clean, a) {
				clean = append(clean, a)
			}
		}
		if _, err := tx.Exec(ctx, `update changes set number = $2, aliases = $3, title = coalesce(nullif($4, ''), title),
			request_element_id = coalesce(request_element_id, $5), updated_at = now() where id = $1`,
			id, number, clean, clip(title, 300), nullable(request)); err != nil {
			return "", "", err
		}
		if keep.number != number || len(found) > 1 {
			if err := auditEvent(ctx, tx, actor, "devsecops.change.renumber", "change", id,
				map[string]any{"number": number, "previous": keep.number, "merged": len(found) - 1}); err != nil {
				return "", "", err
			}
		}
		all = append([]string{number}, clean...)
	}
	if request == "" {
		req, how, err := matchRequest(ctx, tx, all, st.ChangeField)
		if err != nil {
			return "", "", err
		}
		if req != "" {
			request = req
			if _, err := tx.Exec(ctx, "update changes set request_element_id = $2, request_source = $3, updated_at = now() where id = $1", id, req, how); err != nil {
				return "", "", err
			}
			if err := auditEvent(ctx, tx, actor, "devsecops.change.link", "change", id, map[string]any{"request": req, "by": how}); err != nil {
				return "", "", err
			}
		}
	}
	return id, request, nil
}

// matchRequest cerca la richiesta del change: per chiave (DEM-12) o per il campo configurato.
func matchRequest(ctx context.Context, tx pgx.Tx, numbers []string, field string) (string, string, error) {
	var id string
	err := tx.QueryRow(ctx, `select e.id::text from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'business_request'
		where e.deleted_at is null and e.number is not null and s.key || '-' || e.number = any($1) limit 1`, numbers).Scan(&id)
	if err == nil {
		return id, "key", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	if field == "" {
		return "", "", nil
	}
	for _, n := range numbers {
		for _, v := range []string{n, strings.ToLower(n)} {
			err := tx.QueryRow(ctx, `select e.id::text from elements e
				join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'business_request'
				where e.deleted_at is null and e.fields @> jsonb_build_object($1::text, $2::text) limit 1`, field, v).Scan(&id)
			if err == nil {
				return id, "field", nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return "", "", err
			}
		}
	}
	return "", "", nil
}

// ---------------------------------------------------------------------------
// Esiti dei controlli

var (
	modes    = map[string]bool{"off": true, "warn": true, "block": true}
	outcomes = map[string]string{"passed": "passed", "pass": "passed", "success": "passed", "ok": "passed",
		"failed": "failed", "fail": "failed", "failure": "failed", "error": "failed",
		"skipped": "skipped", "skip": "skipped", "not_run": "not_run", "neutral": "not_run", "cancelled": "not_run"}
)

// normChecks pulisce gli esiti: nomi in minuscolo, modalità e esiti nei valori del contratto.
func normChecks(in []contract.CheckResult) ([]contract.CheckResult, error) {
	if len(in) > 200 {
		return nil, engine.InvalidCode("validation.devsecops.checks_too_many", "checks", "al massimo 200 controlli")
	}
	out := make([]contract.CheckResult, 0, len(in))
	for i, c := range in {
		c.Name = strings.ToLower(clip(c.Name, 80))
		if c.Name == "" {
			return nil, engine.InvalidParams("validation.devsecops.check_unnamed", "checks", map[string]any{"row": i + 1}, "controllo %d senza nome", i+1)
		}
		c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
		if c.Mode == "" {
			c.Mode = "warn"
		}
		if !modes[c.Mode] {
			return nil, engine.InvalidValue("checks", "controllo %s: modalità non ammessa (off, warn, block)", c.Name)
		}
		o, ok := outcomes[strings.ToLower(strings.TrimSpace(c.Outcome))]
		if !ok {
			return nil, engine.InvalidValue("checks", "controllo %s: esito non ammesso (passed, failed, skipped, not_run)", c.Name)
		}
		c.Outcome = o
		c.Summary, c.URL = clip(c.Summary, 500), cleanURL(c.URL)
		if c.Skip != nil {
			c.Skip.By, c.Skip.Reason = clip(c.Skip.By, 120), clip(c.Skip.Reason, 1000)
			if c.Skip.By == "" {
				c.Skip.By = "?"
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// IngestCheckRun salva l'esito dei controlli di un commit (uno per commit ed esecuzione).
func (s *Service) IngestCheckRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, c contract.CheckRun) error {
	_, err := s.ingestCheckRun(ctx, tx, actor, c)
	return err
}

func (s *Service) ingestCheckRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, c contract.CheckRun) (*Receipt, error) {
	commit := clip(c.Commit, 80)
	if commit == "" {
		return nil, engine.Required("commit", "serve il commit")
	}
	checks, err := normChecks(c.Checks)
	if err != nil {
		return nil, err
	}
	verdict := strings.ToLower(strings.TrimSpace(c.Verdict))
	if verdict == "" {
		verdict = "passed"
		for _, x := range checks {
			if x.Mode == "block" && x.Outcome == "failed" && x.Skip == nil {
				verdict = "failed"
			}
		}
	}
	if o := outcomes[verdict]; o == "passed" || o == "failed" {
		verdict = o
	} else {
		return nil, engine.InvalidValue("verdict", "esito complessivo non ammesso (passed, failed)")
	}
	ref, err := s.ensureService(ctx, tx, actor, c.Service, "pipeline", true)
	if err != nil {
		return nil, err
	}
	at := c.At
	if at.IsZero() {
		at = s.now()
	}
	raw, _ := json.Marshal(checks)
	var id string
	var inserted bool
	if err := tx.QueryRow(ctx, `insert into check_runs (tenant_id, id, service_id, commit, ref, run_id, url, verdict, checks, at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (tenant_id, service_id, commit, run_id) do update set ref = excluded.ref, url = excluded.url, verdict = excluded.verdict,
		checks = excluded.checks, at = excluded.at, received_at = now()
		returning id::text, (xmax = 0)`,
		db.NewID(), ref.ID, commit, nullable(clip(c.Ref, 200)), clip(c.RunID, 120), nullable(cleanURL(c.URL)), verdict, raw, at).Scan(&id, &inserted); err != nil {
		return nil, err
	}
	if inserted {
		if err := auditEvent(ctx, tx, actor, "devsecops.check_run", "check_run", id, map[string]any{"service": ref.ID, "verdict": verdict, "checks": len(checks)}); err != nil {
			return nil, err
		}
	}
	return &Receipt{Kind: "check_run", ID: id, Service: ref.Name, ServiceID: ref.ID, Application: ref.AppID, Created: inserted, Changed: true,
		Summary: fmt.Sprintf("%s: %d controlli, esito %s", short(commit), len(checks), verdict)}, nil
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// ---------------------------------------------------------------------------
// Rilasci

// Receipt è il riepilogo di un oggetto ricevuto (risposta dell'API di push).
type Receipt struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Service     string `json:"service"`
	ServiceID   string `json:"service_id"`
	Application string `json:"application_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	Production  bool   `json:"production,omitempty"`
	// Created: oggetto nuovo; Changed: false = identico a quello già ricevuto (nessun effetto).
	Created bool   `json:"created"`
	Changed bool   `json:"changed"`
	Summary string `json:"summary"`
	// Change collegato e richiesta di Regia trovata dal numero.
	Change  string `json:"change,omitempty"`
	Request string `json:"request,omitempty"`
	// Requisiti verificati automaticamente da questo rilascio.
	Verified []requirements.AutoVerified `json:"verified_requirements,omitempty"`
	// Conteggi di SBOM e report.
	Components int            `json:"components,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}

func normSummary(v *contract.VulnSummary) *contract.VulnSummary {
	if v == nil {
		return nil
	}
	c := *v
	for _, p := range []*int{&c.Critical, &c.High, &c.Medium, &c.Low, &c.FixableCritical, &c.FixableHigh} {
		if *p < 0 {
			*p = 0
		}
	}
	return &c
}

// IngestRelease salva un rilascio (uno per servizio, ambiente e versione), crea il suo
// deploy, collega il change alla richiesta e verifica i requisiti da evidenza.
func (s *Service) IngestRelease(ctx context.Context, tx pgx.Tx, actor engine.Actor, r contract.Release) error {
	_, err := s.ingestRelease(ctx, tx, actor, r)
	return err
}

func (s *Service) ingestRelease(ctx context.Context, tx pgx.Tx, actor engine.Actor, r contract.Release) (*Receipt, error) {
	version := clip(r.Version, 200)
	if version == "" {
		return nil, engine.Required("version", "serve la versione rilasciata")
	}
	checks, err := normChecks(r.Checks)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	ref, err := s.ensureService(ctx, tx, actor, r.Service, "pipeline", true)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/rel/"+ref.ID); err != nil {
		return nil, err
	}
	envID, prod, err := ensureEnv(ctx, tx, r.Environment, st)
	if err != nil {
		return nil, err
	}
	at := r.At
	if at.IsZero() {
		at = s.now()
	}
	r.Version, r.Checks, r.At, r.CVEsAtRelease = version, checks, at.UTC(), normSummary(r.CVEsAtRelease)
	r.Environment = normEnv(r.Environment)
	r.Service = contract.Service{Name: ref.Name}
	norm, _ := json.Marshal(r)
	sum := sha256.Sum256(norm)
	hash := hex.EncodeToString(sum[:])

	rc := &Receipt{Kind: "release", Service: ref.Name, ServiceID: ref.ID, Application: ref.AppID, Production: prod}
	_ = tx.QueryRow(ctx, "select name from environments where id = $1", envID).Scan(&rc.Environment)
	var existing, oldHash string
	err = tx.QueryRow(ctx, "select id::text, payload_sha256 from releases where service_id = $1 and environment_id = $2 and lower(version) = lower($3)",
		ref.ID, envID, version).Scan(&existing, &oldHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if existing != "" && oldHash == hash {
		rc.ID, rc.Summary = existing, fmt.Sprintf("%s %s in %s: già ricevuto, nessuna modifica", ref.Name, version, rc.Environment)
		_ = tx.QueryRow(ctx, `select coalesce(c.number, ''), coalesce(s.key || '-' || e.number, '') from releases r
			left join changes c on c.tenant_id = r.tenant_id and c.id = r.change_id
			left join elements e on e.tenant_id = c.tenant_id and e.id = c.request_element_id
			left join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where r.id = $1`, existing).Scan(&rc.Change, &rc.Request)
		return rc, nil
	}
	changeID, requestID, err := s.ensureChange(ctx, tx, actor, r.ChangeNumber, r.ChangeTitle, r.ChangeAliases, st)
	if err != nil {
		return nil, err
	}
	// Controlli del commit rilasciato: dal rilascio, altrimenti dall'ultimo esito del commit.
	checksSource := "release"
	if len(checks) == 0 {
		checksSource = ""
		if c := clip(r.Commit, 80); c != "" {
			var raw []byte
			err := tx.QueryRow(ctx, "select checks from check_runs where service_id = $1 and commit = $2 order by at desc limit 1", ref.ID, c).Scan(&raw)
			if err == nil {
				_ = json.Unmarshal(raw, &checks)
				checksSource = "check_run"
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
	}
	rawChecks, _ := json.Marshal(checks)
	var cves any
	if r.CVEsAtRelease != nil {
		cves, _ = json.Marshal(r.CVEsAtRelease)
	}
	args := []any{ref.ID, envID, version, nullable(clip(r.Commit, 80)), nullable(clip(r.Image, 500)), nullable(clip(r.Digest, 200)),
		nullable(changeID), nullable(normChange(r.ChangeNumber)), nullable(clip(r.RequestedBy, 200)), nullable(clip(r.ApprovedBy, 200)), r.ApprovedAt,
		nullable(cleanURL(r.PullRequest)), nullable(cleanURL(r.ReleaseURL)), r.At, cves, rawChecks, nullable(checksSource), hash}
	if existing == "" {
		existing = db.NewID()
		rc.Created = true
		_, err = tx.Exec(ctx, `insert into releases (tenant_id, id, service_id, environment_id, version, commit, image, digest, change_id, change_number,
			requested_by, approved_by, approved_at, pull_request, release_url, released_at, cves_at_release, checks, checks_source, payload_sha256)
			values (app_tenant(), $19, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`, append(args, existing)...)
	} else {
		_, err = tx.Exec(ctx, `update releases set version = $3, commit = $4, image = $5, digest = $6, change_id = $7, change_number = $8,
			requested_by = $9, approved_by = $10, approved_at = $11, pull_request = $12, release_url = $13, released_at = $14,
			cves_at_release = $15, checks = $16, checks_source = $17, payload_sha256 = $18, updated_at = now()
			where id = $19 and service_id = $1 and environment_id = $2`, append(args, existing)...)
	}
	if err != nil {
		return nil, err
	}
	rc.ID, rc.Changed = existing, true
	// Il rilascio è anche un deploy riuscito nel suo ambiente.
	if _, err := tx.Exec(ctx, `insert into deployments (tenant_id, id, application_element_id, environment_id, version, status, started_at, finished_at,
		external_ref, service_id, digest, source, release_id)
		values (app_tenant(), $1, $2, $3, $4, 'succeeded', $5, $5, $6, $7, $8, 'release', $9)
		on conflict (tenant_id, service_id, external_ref) where service_id is not null and external_ref is not null
		do update set application_element_id = excluded.application_element_id, version = excluded.version, started_at = excluded.started_at,
		finished_at = excluded.finished_at, digest = excluded.digest`,
		db.NewID(), nullable(ref.AppID), envID, version, r.At, "release:"+existing, ref.ID, nullable(clip(r.Digest, 200)), existing); err != nil {
		return nil, err
	}
	if err := s.refreshFindings(ctx, tx, ref.ID); err != nil {
		return nil, err
	}
	if changeID != "" {
		_ = tx.QueryRow(ctx, `select c.number, coalesce(s.key || '-' || e.number, '') from changes c
			left join elements e on e.tenant_id = c.tenant_id and e.id = c.request_element_id
			left join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where c.id = $1`, changeID).Scan(&rc.Change, &rc.Request)
		if requestID != "" {
			if err := engine.AddActivity(ctx, tx, requestID, actor.MemberID, "release", "devsecops",
				map[string]any{"service": ref.Name, "version": version, "environment": rc.Environment, "change": rc.Change}, nil); err != nil {
				return nil, err
			}
		}
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.release", "release", existing, map[string]any{"service": ref.ID, "environment": envID,
		"change": changeID, "request": requestID, "created": rc.Created, "checks": len(checks)}); err != nil {
		return nil, err
	}
	// Requisiti da evidenza: i controlli in block superati senza eccezioni.
	if s.req != nil && ref.AppID != "" {
		passed := evidenceKeys(checks, r.CVEsAtRelease)
		if len(passed) > 0 {
			note := fmt.Sprintf("Rilascio %s %s in %s del %s: controlli in modalità block superati (%s).",
				ref.Name, version, rc.Environment, r.At.Format("02/01/2006"), strings.Join(blockPassed(checks), ", "))
			if rc.Change != "" {
				note += " Change " + rc.Change + "."
			}
			v, err := s.req.VerifyFromEvidence(ctx, tx, actor, requirements.PipelineEvidence{ApplicationID: ref.AppID, RequestID: requestID,
				Checks: passed, Collector: "devsecops", Note: note, Links: []string{cleanURL(r.ReleaseURL), cleanURL(r.PullRequest)}, Ref: existing})
			if err != nil {
				return nil, err
			}
			rc.Verified = v
		}
	}
	// Evidenze automatiche dei controlli GRC mappati (gate_evidence.go).
	if ref.AppID != "" {
		if _, err := s.releaseEvidence(ctx, tx, actor, ref.AppID, existing, ref.Name, version, rc.Environment, prod, checks, r.At, r.ReleaseURL, rc.Change); err != nil {
			return nil, err
		}
	}
	rc.Summary = fmt.Sprintf("%s %s in %s", ref.Name, version, rc.Environment)
	if ref.AppID == "" {
		rc.Warnings = append(rc.Warnings, "servizio non associato a un'applicazione del catalogo: indicare service.application o associarlo da Configurazione → DevSecOps")
	}
	return rc, nil
}

// cveChecks sono i controlli delle dipendenze e dell'immagine: per l'evidenza vogliono anche
// zero CVE critiche o alte correggibili al rilascio, se il rilascio le dichiara.
var cveChecks = map[string]bool{"cves": true, "cve": true, "sca": true, "dependencies": true, "vulnerabilities": true, "image": true}

// checkAliases: chiavi dei cataloghi di requisiti che corrispondono a un controllo della
// pipeline (il catalogo di partenza usa secret_scan e sca_block).
var checkAliases = map[string][]string{
	"secrets": {"secret_scan", "secret-scan", "gitleaks"},
	"cves":    {"sca_block", "sca"}, "sca": {"sca_block"}, "dependencies": {"sca_block"},
}

func blockPassed(checks []contract.CheckResult) []string {
	out := []string{}
	for _, c := range checks {
		if c.Mode == "block" && c.Outcome == "passed" && c.Skip == nil {
			out = append(out, c.Name)
		}
	}
	return out
}

// evidenceKeys sono le chiavi di controllo che il rilascio soddisfa: nome, "release.<nome>"
// e gli alias del catalogo.
func evidenceKeys(checks []contract.CheckResult, cves *contract.VulnSummary) []string {
	keys := []string{}
	add := func(k string) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, name := range blockPassed(checks) {
		if cveChecks[name] && cves != nil && cves.FixableCritical+cves.FixableHigh > 0 {
			continue
		}
		add(name)
		add("release." + name)
		for _, a := range checkAliases[name] {
			add(a)
		}
	}
	return keys
}

// ---------------------------------------------------------------------------
// Deploy

var deployStatus = map[string]string{"running": "running", "in_progress": "running", "succeeded": "succeeded", "success": "succeeded",
	"synced": "succeeded", "healthy": "succeeded", "failed": "failed", "failure": "failed", "error": "failed",
	"rolled_back": "rolled_back", "rollback": "rolled_back"}

var deploySources = map[string]bool{"gitops": true, "pipeline": true, "manual": true}

// IngestDeployment salva un deploy: per riferimento esterno (aggiornamenti di stato dello
// stesso deploy) o per servizio, ambiente, versione e istante.
func (s *Service) IngestDeployment(ctx context.Context, tx pgx.Tx, actor engine.Actor, d contract.Deployment) error {
	_, err := s.ingestDeployment(ctx, tx, actor, d)
	return err
}

func (s *Service) ingestDeployment(ctx context.Context, tx pgx.Tx, actor engine.Actor, d contract.Deployment) (*Receipt, error) {
	version := clip(d.Version, 200)
	if version == "" {
		return nil, engine.Required("version", "serve la versione")
	}
	status, ok := deployStatus[strings.ToLower(strings.TrimSpace(d.Status))]
	if !ok {
		return nil, engine.InvalidValue("status", "stato non ammesso (running, succeeded, failed, rolled_back)")
	}
	source := strings.ToLower(strings.TrimSpace(d.Source))
	if !deploySources[source] {
		source = "pipeline"
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	ref, err := s.ensureService(ctx, tx, actor, d.Service, "pipeline", true)
	if err != nil {
		return nil, err
	}
	if err := lock(ctx, tx, "dso/rel/"+ref.ID); err != nil {
		return nil, err
	}
	envID, prod, err := ensureEnv(ctx, tx, d.Environment, st)
	if err != nil {
		return nil, err
	}
	at := d.At
	if at.IsZero() {
		at = s.now()
	}
	var finished any
	if status != "running" {
		finished = at
	}
	extRef := clip(d.ExternalRef, 300)
	if strings.HasPrefix(extRef, "release:") {
		extRef = "x-" + extRef // riservato ai deploy dei rilasci
	}
	var id string
	var inserted bool
	if extRef != "" {
		err = tx.QueryRow(ctx, `insert into deployments (tenant_id, id, application_element_id, environment_id, version, status, started_at, finished_at,
			external_ref, service_id, digest, source)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			on conflict (tenant_id, service_id, external_ref) where service_id is not null and external_ref is not null
			do update set status = excluded.status, environment_id = excluded.environment_id, version = excluded.version,
			finished_at = coalesce(excluded.finished_at, deployments.finished_at), digest = coalesce(excluded.digest, deployments.digest)
			returning id::text, (xmax = 0)`,
			db.NewID(), nullable(ref.AppID), envID, version, status, at, finished, extRef, ref.ID, nullable(clip(d.Digest, 200)), source).Scan(&id, &inserted)
	} else {
		err = tx.QueryRow(ctx, `insert into deployments (tenant_id, id, application_element_id, environment_id, version, status, started_at, finished_at,
			service_id, digest, source)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			on conflict (tenant_id, service_id, environment_id, version, started_at) where service_id is not null and external_ref is null
			do update set status = excluded.status, finished_at = coalesce(excluded.finished_at, deployments.finished_at),
			digest = coalesce(excluded.digest, deployments.digest)
			returning id::text, (xmax = 0)`,
			db.NewID(), nullable(ref.AppID), envID, version, status, at, finished, ref.ID, nullable(clip(d.Digest, 200)), source).Scan(&id, &inserted)
	}
	if err != nil {
		return nil, err
	}
	if prod {
		if err := s.refreshFindings(ctx, tx, ref.ID); err != nil {
			return nil, err
		}
	}
	if inserted {
		if err := auditEvent(ctx, tx, actor, "devsecops.deployment", "deployment", id, map[string]any{"service": ref.ID, "environment": envID, "status": status}); err != nil {
			return nil, err
		}
	}
	rc := &Receipt{Kind: "deployment", ID: id, Service: ref.Name, ServiceID: ref.ID, Application: ref.AppID, Production: prod, Created: inserted, Changed: true}
	_ = tx.QueryRow(ctx, "select name from environments where id = $1", envID).Scan(&rc.Environment)
	rc.Summary = fmt.Sprintf("%s %s in %s: %s", ref.Name, version, rc.Environment, status)
	return rc, nil
}

// prodVersion è la versione in produzione del servizio all'istante indicato: l'ultimo deploy
// riuscito (o in corso) in un ambiente di produzione.
func prodVersion(ctx context.Context, tx pgx.Tx, serviceID string, at time.Time) (string, error) {
	var v string
	err := tx.QueryRow(ctx, `select d.version from deployments d join environments e on e.tenant_id = d.tenant_id and e.id = d.environment_id
		where d.service_id = $1 and e.is_production and d.status in ('succeeded', 'running') and d.started_at <= $2
		order by d.started_at desc limit 1`, serviceID, at).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// refreshFindings allinea lo stato dei finding del servizio alla versione in produzione:
// aperti quelli dell'ultimo report (per scanner) di quella versione, corretti gli altri. Le
// decisioni di una persona (accettato, falso positivo) non si toccano.
func (s *Service) refreshFindings(ctx context.Context, tx pgx.Tx, serviceID string) error {
	if err := s.refreshStatus(ctx, tx, serviceID); err != nil {
		return err
	}
	// Ciclo di vita, scadenze e team (vulnlife.go).
	return s.syncVulnLifecycle(ctx, tx, serviceID)
}

func (s *Service) refreshStatus(ctx context.Context, tx pgx.Tx, serviceID string) error {
	v, err := prodVersion(ctx, tx, serviceID, s.now().Add(time.Minute))
	if err != nil || v == "" {
		return err
	}
	var reports int
	if err := tx.QueryRow(ctx, "select count(*) from vuln_reports where service_id = $1 and lower(version) = lower($2)", serviceID, v).Scan(&reports); err != nil || reports == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `
		with rep as (
			select distinct on (scanner) id from vuln_reports where service_id = $1 and lower(version) = lower($2)
			order by scanner, scanned_at desc
		), cur as (
			select distinct l.finding_id from vuln_report_findings l join rep on rep.id = l.report_id
		)
		update findings f set status = case when exists (select 1 from cur where cur.finding_id = f.id) then 'open' else 'fixed' end
		where f.service_id = $1 and f.status in ('open', 'fixed')
		  and (f.status = 'open') is distinct from exists (select 1 from cur where cur.finding_id = f.id)`, serviceID, v)
	return err
}

func isoDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }
