package github

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/platform/db"
)

// Adozione guidata tramite pull request (replica/devsecops.md §3.4, fase 3).
//
// Regia genera due file generici a partire dalla politica centrale e dalla sezione
// «adoption» del profilo di pipeline: un workflow che richiama i template configurati dal
// tenant (le righe «uses:») e il file di configurazione dei controlli. Ne mostra la
// differenza con il repository e, se la GitHub App ha i permessi facoltativi «Contents» e
// «Pull requests» in scrittura, apre la pull request; altrimenti i file si scaricano. Lo
// stato della pull request (aperta, unita, chiusa) si sincronizza da GitHub.

var (
	ErrNoPolicy     = &Error{Status: 409, Code: "adoption.no_policy", Msg: "definisci prima la politica della pipeline: senza, non si sa quali controlli richiedere"}
	ErrNoAdoption   = &Error{Status: 409, Code: "adoption.no_templates", Msg: "nessun profilo di pipeline ha una sezione di adozione con template: configurala da Configurazione → DevSecOps → Politica della pipeline → Adozione"}
	ErrNeedRepo     = &Error{Status: 422, Code: "adoption.repository", Field: "repository", Msg: "scegli il repository a cui proporre la pipeline"}
	ErrAligned      = &Error{Status: 409, Code: "adoption.aligned", Msg: "il repository ha già questi file: non c'è niente da proporre"}
	ErrAdoptionOpen = &Error{Status: 409, Code: "adoption.open", Msg: "c'è già una pull request di Regia aperta per questo repository: uniscila o chiudila prima di proporne un'altra"}
	ErrNoWrite      = &Error{Status: 403, Code: "adoption.no_write", Msg: ghapi.ErrNoWrite.Error() + ". In alternativa scarica i file e aprila a mano"}
)

// TemplateView è un template dell'adozione come lo vede la procedura guidata.
type TemplateView struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Uses        string   `json:"uses"`
	Needs       []string `json:"needs"`
	Controls    []string `json:"controls"`
	Stack       []string `json:"stack"`
	Default     bool     `json:"default"`
}

// AdoptionSource è un profilo con la sezione di adozione, con l'integrazione che lo usa.
type AdoptionSource struct {
	IntegrationID   *string                 `json:"integration_id"`
	Integration     string                  `json:"integration"`
	ProfileID       string                  `json:"profile_id"`
	Profile         string                  `json:"profile"`
	Templates       []TemplateView          `json:"templates"`
	Params          []profile.AdoptionParam `json:"params"`
	WorkflowPath    string                  `json:"workflow_path"`
	ConfigPath      string                  `json:"config_path"`
	CanOpenPR       *bool                   `json:"can_open_pr"` // nil: non lo si sa (token)
	CanOpenPRReason string                  `json:"can_open_pr_reason"`
}

// AdoptionRepo è un repository proponibile.
type AdoptionRepo struct {
	ID            string `json:"id"`
	IntegrationID string `json:"integration_id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Mapped        bool   `json:"mapped"`    // associato a questa applicazione
	Suggested     bool   `json:"suggested"` // proposto per questa applicazione
	Archived      bool   `json:"archived"`
}

// AdoptedFile è un file di una pull request.
type AdoptedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// AdoptionPR è una pull request aperta da Regia.
type AdoptionPR struct {
	ID            string        `json:"id"`
	ApplicationID *string       `json:"application_id"`
	IntegrationID *string       `json:"integration_id"`
	Repository    string        `json:"repository"`
	BaseBranch    string        `json:"base_branch"`
	Branch        string        `json:"branch"`
	Number        int           `json:"number"`
	URL           string        `json:"url"`
	Title         string        `json:"title"`
	State         string        `json:"state"` // open | merged | closed
	PolicyVersion *int          `json:"policy_version"`
	Class         *string       `json:"class"`
	Files         []AdoptedFile `json:"files"`
	LastError     *string       `json:"last_error"`
	CreatedAt     time.Time     `json:"created_at"`
	SyncedAt      *time.Time    `json:"synced_at"`
	ClosedAt      *time.Time    `json:"closed_at"`
}

// AdoptionOptions è tutto ciò che serve alla procedura guidata di un'applicazione.
type AdoptionOptions struct {
	Sources      []AdoptionSource `json:"sources"`
	Repositories []AdoptionRepo   `json:"repositories"`
	Adoptions    []AdoptionPR     `json:"adoptions"`
}

func templateViews(a *profile.AdoptionSpec) []TemplateView {
	out := []TemplateView{}
	for _, t := range a.Templates {
		out = append(out, TemplateView{Key: t.Key, Name: t.Name, Description: t.Description, Uses: t.Uses, Needs: nz(t.Needs), Controls: nz(t.Controls), Stack: nz(t.Stack), Default: t.Default})
	}
	return out
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// sources elenca i profili con l'adozione: uno per integrazione attiva che li usa, e uno per
// i profili non usati da nessuna integrazione (solo download).
func (s *Service) sources(ctx context.Context, tx pgx.Tx) ([]AdoptionSource, error) {
	rows, err := tx.Query(ctx, `select p.id::text, p.name, v.definition from pipeline_profiles p
		join pipeline_profile_versions v on v.tenant_id = p.tenant_id and v.profile_id = p.id and v.version = p.current_version
		where v.definition ? 'adoption' order by lower(p.name)`)
	if err != nil {
		return nil, err
	}
	type prof struct {
		id, name string
		spec     *profile.AdoptionSpec
	}
	var profs []prof
	for rows.Next() {
		var p prof
		var raw []byte
		if err := rows.Scan(&p.id, &p.name, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		def, err := profile.Parse(raw)
		if err != nil || def.Adoption == nil || len(def.Adoption.Templates) == 0 {
			continue
		}
		p.spec = def.Adoption
		profs = append(profs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	irows, err := tx.Query(ctx, `select id::text, name, config ->> 'profile_id' from integrations where kind = 'github' and active order by lower(name)`)
	if err != nil {
		return nil, err
	}
	type ig struct{ id, name, profile string }
	var igs []ig
	for irows.Next() {
		var i ig
		var pid *string
		if err := irows.Scan(&i.id, &i.name, &pid); err != nil {
			irows.Close()
			return nil, err
		}
		if pid != nil {
			i.profile = *pid
		}
		igs = append(igs, i)
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}
	out := []AdoptionSource{}
	used := map[string]bool{}
	mk := func(p prof) AdoptionSource {
		wf, cf := p.spec.Paths()
		return AdoptionSource{ProfileID: p.id, Profile: p.name, Templates: templateViews(p.spec), Params: append([]profile.AdoptionParam{}, p.spec.Params...), WorkflowPath: wf, ConfigPath: cf}
	}
	for _, i := range igs {
		for _, p := range profs {
			if p.id == i.profile {
				src := mk(p)
				id := i.id
				src.IntegrationID, src.Integration = &id, i.name
				out = append(out, src)
				used[p.id] = true
			}
		}
	}
	for _, p := range profs {
		if !used[p.id] {
			out = append(out, mk(p))
		}
	}
	return out, nil
}

// AdoptionOptions raccoglie sorgenti, repository candidati e pull request di un'applicazione.
func (s *Service) AdoptionOptions(ctx context.Context, tenantID, appID string) (*AdoptionOptions, error) {
	o := &AdoptionOptions{Sources: []AdoptionSource{}, Repositories: []AdoptionRepo{}, Adoptions: []AdoptionPR{}}
	if !validID(appID) {
		return o, nil
	}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if o.Sources, err = s.sources(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select r.id::text, r.integration_id::text, r.full_name, r.default_branch,
			r.application_element_id = $1 and r.mapping_status = 'confirmed', r.suggested_element_id = $1 and r.mapping_status = 'suggested', r.archived
			from connector_repositories r join integrations i on i.tenant_id = r.tenant_id and i.id = r.integration_id and i.active
			where r.missing_since is null and r.mapping_status <> 'ignored'
			  and ((r.application_element_id = $1 and r.mapping_status = 'confirmed') or (r.suggested_element_id = $1 and r.mapping_status = 'suggested'))
			order by 5 desc, 6 desc, lower(r.full_name) limit 50`, appID)
		if err != nil {
			return err
		}
		if o.Repositories, err = pgx.CollectRows(rows, pgx.RowToStructByPos[AdoptionRepo]); err != nil {
			return err
		}
		o.Adoptions, err = adoptionsOf(ctx, tx, "application_element_id = $1", appID)
		return err
	})
	if err != nil {
		return nil, err
	}
	for i := range o.Sources {
		if o.Sources[i].IntegrationID != nil {
			o.Sources[i].CanOpenPR, o.Sources[i].CanOpenPRReason = s.canOpenPR(ctx, tenantID, *o.Sources[i].IntegrationID)
		} else {
			o.Sources[i].CanOpenPRReason = "Il profilo non è usato da nessuna integrazione GitHub: i file si scaricano."
		}
	}
	return o, nil
}

// RepositorySearch cerca tra tutti i repository scoperti (per sceglierne uno non ancora
// associato all'applicazione).
func (s *Service) RepositorySearch(ctx context.Context, tenantID, q string) ([]AdoptionRepo, error) {
	out := []AdoptionRepo{}
	q = strings.TrimSpace(q)
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select r.id::text, r.integration_id::text, r.full_name, r.default_branch, false, false, r.archived
			from connector_repositories r join integrations i on i.tenant_id = r.tenant_id and i.id = r.integration_id and i.active
			where r.missing_since is null and r.mapping_status <> 'ignored' and r.full_name ilike '%' || $1 || '%'
			order by lower(r.full_name) limit 20`, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[AdoptionRepo])
		return err
	})
	return out, err
}

// canOpenPR dice se l'integrazione può aprire pull request: con una GitHub App lo dicono i
// permessi dell'installazione; con un token non lo si sa (si prova). Il risultato si tiene
// per qualche minuto.
func (s *Service) canOpenPR(ctx context.Context, tenantID, integrationID string) (*bool, string) {
	s.mu.Lock()
	if c, ok := s.caps[integrationID]; ok && s.o.Now().Before(c.until) {
		s.mu.Unlock()
		return c.can, c.reason
	}
	s.mu.Unlock()
	var it *Integration
	var sec secrets
	if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		it, sec, err = s.load(ctx, tx, integrationID)
		return err
	}); err != nil {
		return nil, ""
	}
	var can *bool
	reason := ""
	if it.Config.AuthMode != "app" {
		reason = "Con un token non si può sapere prima se la scrittura è permessa: si prova al momento di aprire la pull request."
	} else if cl, err := s.client(integrationID, it.Config, sec); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if inst, err := cl.Installation(cctx); err == nil {
			ok := ghapi.HasWrite(inst.Permissions)
			can = &ok
			if !ok {
				reason = "La GitHub App non ha i permessi «Contents» e «Pull requests» in scrittura (facoltativi): i file si scaricano."
			}
		} else {
			reason = "Permessi della GitHub App non leggibili: " + err.Error()
		}
	}
	s.mu.Lock()
	s.caps[integrationID] = capEntry{can: can, reason: reason, until: s.o.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	return can, reason
}

type capEntry struct {
	can    *bool
	reason string
	until  time.Time
}

// ---------------------------------------------------------------------
// Anteprima

// AdoptionInput è la scelta fatta nella procedura guidata.
type AdoptionInput struct {
	ApplicationID string            `json:"application_id"`
	IntegrationID string            `json:"integration_id"`
	ProfileID     string            `json:"profile_id"`
	RepositoryID  string            `json:"repository_id"`
	Templates     []string          `json:"templates"`
	Params        map[string]string `json:"params"`
}

// PreviewFile è un file proposto con la differenza rispetto al repository.
type PreviewFile struct {
	Path    string             `json:"path"`
	Status  string             `json:"status"` // new | changed | unchanged
	Content string             `json:"content"`
	Diff    []profile.DiffLine `json:"diff"`
	Added   int                `json:"added"`
	Removed int                `json:"removed"`
}

// AdoptionPreview è l'anteprima di ciò che Regia proporrebbe.
type AdoptionPreview struct {
	Application   string                    `json:"application"`
	Class         string                    `json:"class"`
	PolicyVersion int                       `json:"policy_version"`
	Controls      []devsecops.PolicyControl `json:"controls"`
	Uncovered     []string                  `json:"uncovered"` // controlli richiesti che nessun template scelto dichiara
	Profile       string                    `json:"profile"`
	Repository    string                    `json:"repository"`
	DefaultBranch string                    `json:"default_branch"`
	Files         []PreviewFile             `json:"files"`
	Warnings      []string                  `json:"warnings"`
	CanOpenPR     bool                      `json:"can_open_pr"`
	CanOpenReason string                    `json:"can_open_reason"`
	// Interni: servono ad aprire la pull request senza rileggere tutto.
	repoID, integrationID, profileName, service string
}

type prepared struct {
	pv      AdoptionPreview
	pol     *devsecops.AppPolicy
	it      *Integration
	sec     secrets
	appUUID string
}

// prepare risolve profilo, repository e politica, genera i file e li confronta col repository.
func (s *Service) prepare(ctx context.Context, tenantID string, in AdoptionInput, needRepo bool) (*prepared, error) {
	if !validID(in.ApplicationID) {
		return nil, invalid("application_id", "applicazione non valida")
	}
	var spec *profile.AdoptionSpec
	var pol *devsecops.AppPolicy
	var it *Integration
	var sec secrets
	var repo *AdoptionRepo
	var profName, service string
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if pol, err = devsecops.PolicyFor(ctx, tx, in.ApplicationID); err != nil {
			if errors.Is(err, engine.ErrNotFound) {
				return invalid("application_id", "applicazione inesistente")
			}
			return err
		}
		integrationID := in.IntegrationID
		if in.RepositoryID != "" {
			if !validID(in.RepositoryID) {
				return invalid("repository_id", "repository non valido")
			}
			var r AdoptionRepo
			if err := tx.QueryRow(ctx, `select r.id::text, r.integration_id::text, r.full_name, r.default_branch, false, false, r.archived, r.service_name
				from connector_repositories r join integrations i on i.tenant_id = r.tenant_id and i.id = r.integration_id and i.active
				where r.id = $1 and r.missing_since is null`, in.RepositoryID).Scan(&r.ID, &r.IntegrationID, &r.FullName, &r.DefaultBranch, &r.Mapped, &r.Suggested, &r.Archived, &service); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrRepoNotFound
				}
				return err
			}
			repo = &r
			integrationID = r.IntegrationID
		}
		profileID := in.ProfileID
		if integrationID != "" {
			if it, sec, err = s.load(ctx, tx, integrationID); err != nil {
				return err
			}
			if profileID == "" {
				profileID = it.Config.ProfileID
			}
		}
		if profileID == "" {
			srcs, err := s.sources(ctx, tx)
			if err != nil {
				return err
			}
			if len(srcs) > 0 {
				profileID = srcs[0].ProfileID
			}
		}
		def, _, err := definitionFor(ctx, tx, profileID)
		if err != nil {
			return err
		}
		if def == nil || def.Adoption == nil || len(def.Adoption.Templates) == 0 {
			return ErrNoAdoption
		}
		spec = def.Adoption
		return tx.QueryRow(ctx, "select name from pipeline_profiles where id = $1", profileID).Scan(&profName)
	})
	if err != nil {
		return nil, err
	}
	if !pol.Configured {
		return nil, ErrNoPolicy
	}
	if needRepo && repo == nil {
		return nil, ErrNeedRepo
	}
	tpls, err := spec.SelectTemplates(in.Templates)
	if err != nil {
		return nil, invalid("templates", err.Error())
	}
	keys := make([]string, len(tpls))
	for i, t := range tpls {
		keys[i] = t.Key
	}
	ri := profile.RenderInput{Application: pol.AppKey, Service: pol.AppKey, Class: pol.Class, PolicyVersion: pol.Version, Templates: keys, Params: in.Params, DefaultBranch: "main"}
	if repo != nil {
		ri.Repository, ri.DefaultBranch = repo.FullName, repo.DefaultBranch
		ri.Service = service
	}
	for _, c := range pol.Controls {
		ri.Controls = append(ri.Controls, profile.ControlMode{Key: c.Key, Name: c.Name, Mode: c.Mode})
	}
	files, err := spec.Render(ri)
	if err != nil {
		return nil, invalid("params", err.Error())
	}
	p := &prepared{pol: pol, it: it, sec: sec, appUUID: in.ApplicationID}
	p.pv = AdoptionPreview{Application: pol.AppKey + " · " + pol.AppName, Class: pol.Class, PolicyVersion: pol.Version, Controls: pol.Controls,
		Profile: profName, Files: []PreviewFile{}, Warnings: []string{}, Uncovered: []string{}, service: service, profileName: profName}
	var required []string
	for _, c := range pol.Controls {
		if c.Mode != "off" {
			required = append(required, c.Key)
		}
	}
	for _, k := range spec.Uncovered(tpls, required) {
		name := k
		for _, c := range pol.Controls {
			if c.Key == k {
				name = c.Name
			}
		}
		p.pv.Uncovered = append(p.pv.Uncovered, name)
	}
	if repo != nil {
		p.pv.Repository, p.pv.DefaultBranch, p.pv.repoID, p.pv.integrationID = repo.FullName, repo.DefaultBranch, repo.ID, repo.IntegrationID
	}
	// Differenza col repository (se lo si può leggere).
	var cl *ghapi.Client
	if repo != nil && it != nil {
		if cl, err = s.client(it.ID, it.Config, sec); err != nil {
			p.pv.Warnings = append(p.pv.Warnings, "Il repository non si può leggere: "+err.Error())
		}
	}
	for _, f := range files {
		pf := PreviewFile{Path: f.Path, Status: "new", Content: f.Content}
		old := ""
		if cl != nil {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			b, _, err := cl.FileOrNil(cctx, repo.FullName, f.Path, repo.DefaultBranch)
			cancel()
			if err != nil {
				p.pv.Warnings = append(p.pv.Warnings, fmt.Sprintf("Non riesco a leggere %s dal repository: %v", f.Path, err))
				cl = nil
			} else if b != nil {
				old = string(b)
				pf.Status = "changed"
				if old == f.Content {
					pf.Status = "unchanged"
				}
			}
		}
		pf.Diff, pf.Added, pf.Removed = profile.Diff(old, f.Content)
		p.pv.Files = append(p.pv.Files, pf)
	}
	switch {
	case it == nil || repo == nil:
		p.pv.CanOpenReason = "Scegli un repository collegato a un'integrazione GitHub per aprire la pull request; altrimenti scarica i file."
	default:
		can, reason := s.canOpenPR(ctx, tenantID, it.ID)
		p.pv.CanOpenPR = can == nil || *can
		p.pv.CanOpenReason = reason
	}
	return p, nil
}

// PreviewAdoption genera i file e ne mostra la differenza con il repository, senza scrivere.
func (s *Service) PreviewAdoption(ctx context.Context, tenantID string, in AdoptionInput) (*AdoptionPreview, error) {
	p, err := s.prepare(ctx, tenantID, in, false)
	if err != nil {
		return nil, err
	}
	return &p.pv, nil
}

// AdoptionFiles restituisce i file da scaricare.
func (s *Service) AdoptionFiles(ctx context.Context, tenantID string, in AdoptionInput) ([]profile.File, error) {
	p, err := s.prepare(ctx, tenantID, in, false)
	if err != nil {
		return nil, err
	}
	out := make([]profile.File, len(p.pv.Files))
	for i, f := range p.pv.Files {
		out[i] = profile.File{Path: f.Path, Content: f.Content}
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Pull request

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func prBody(p *prepared) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Questa pull request è stata aperta da Regia per allineare la pipeline di **%s** alla politica centrale.\n\n", p.pv.Repository)
	fmt.Fprintf(&b, "- **Applicazione:** %s\n- **Classe:** %s (versione %d della politica)\n- **Profilo dei template:** %s\n\n", p.pv.Application, p.pv.Class, p.pv.PolicyVersion, p.pv.profileName)
	b.WriteString("### Controlli richiesti\n\n| Controllo | Modalità |\n|---|---|\n")
	for _, c := range p.pol.Controls {
		mode := map[string]string{"off": "spento", "warn": "avviso", "block": "bloccante"}[c.Mode]
		fmt.Fprintf(&b, "| %s | %s |\n", c.Name, mode)
	}
	b.WriteString("\n### File\n\n")
	for _, f := range p.pv.Files {
		if f.Status == "unchanged" {
			continue
		}
		fmt.Fprintf(&b, "- `%s` (%s)\n", f.Path, map[string]string{"new": "nuovo", "changed": "aggiornato"}[f.Status])
	}
	b.WriteString("\nIl workflow non contiene la logica della pipeline: richiama i workflow riusabili configurati nel profilo. ")
	b.WriteString("Dopo l'unione, gli esiti dei controlli arrivano a Regia con la pipeline e l'aderenza alla politica si aggiorna da sola.\n")
	return b.String()
}

// OpenAdoptionPR crea il branch, scrive i file e apre la pull request. Non fa commit sul
// branch principale e non unisce niente.
func (s *Service) OpenAdoptionPR(ctx context.Context, a Actor, in AdoptionInput) (*AdoptionPR, error) {
	p, err := s.prepare(ctx, a.TenantID, in, true)
	if err != nil {
		return nil, err
	}
	if p.it == nil {
		return nil, ErrNeedRepo
	}
	var changed []PreviewFile
	for _, f := range p.pv.Files {
		if f.Status != "unchanged" {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		return nil, ErrAligned
	}
	// Una sola pull request aperta per repository.
	var open int
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from pipeline_adoptions where integration_id = $1 and lower(repository) = lower($2) and state = 'open'`,
			p.it.ID, p.pv.Repository).Scan(&open)
	}); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, ErrAdoptionOpen
	}
	cl, err := s.client(p.it.ID, p.it.Config, p.sec)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	wrap := func(err error) error {
		if ghapi.IsNoWrite(err) {
			return ErrNoWrite
		}
		return &Error{Status: 502, Code: "adoption.github", Msg: err.Error()}
	}
	head, err := cl.BranchHead(cctx, p.pv.Repository, p.pv.DefaultBranch)
	if err != nil {
		return nil, wrap(err)
	}
	branch := "regia/pipeline-" + s.o.Now().UTC().Format("20060102") + "-" + randHex(2)
	if err := cl.CreateBranch(cctx, p.pv.Repository, branch, head); err != nil {
		return nil, wrap(err)
	}
	for _, f := range changed {
		var sha string
		if f.Status == "changed" {
			if _, sha, err = cl.File(cctx, p.pv.Repository, f.Path, p.pv.DefaultBranch); err != nil {
				return nil, wrap(err)
			}
		}
		msg := "Regia: aggiunge " + f.Path
		if f.Status == "changed" {
			msg = "Regia: aggiorna " + f.Path
		}
		if err := cl.PutFile(cctx, p.pv.Repository, branch, f.Path, msg, []byte(f.Content), sha); err != nil {
			return nil, wrap(err)
		}
	}
	title := fmt.Sprintf("Pipeline: allinea %s alla politica (classe %s)", p.pol.AppKey, p.pv.Class)
	pr, err := cl.CreatePull(cctx, p.pv.Repository, title, branch, p.pv.DefaultBranch, prBody(p))
	if err != nil {
		return nil, wrap(err)
	}
	files := make([]AdoptedFile, len(changed))
	for i, f := range changed {
		files[i] = AdoptedFile{Path: f.Path, Status: f.Status}
	}
	rawFiles, _ := json.Marshal(files)
	var out *AdoptionPR
	err = s.o.DB.InTenant(context.WithoutCancel(ctx), a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `insert into pipeline_adoptions (tenant_id, application_element_id, integration_id, repository, base_branch, branch,
			pr_number, pr_url, title, state, policy_version, class_key, files, created_by, synced_at)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, 'open', $9, $10, $11, $12, now()) returning id::text`,
			p.appUUID, p.it.ID, p.pv.Repository, p.pv.DefaultBranch, branch, pr.Number, pr.HTMLURL, title, p.pol.Version, p.pol.ClassKey, rawFiles,
			nullable(a.MemberID)).Scan(&id); err != nil {
			return err
		}
		if err := audited(ctx, tx, a, "devsecops.adoption.pr", "pipeline_adoption", id, map[string]any{"repository": p.pv.Repository, "pull_request": pr.Number,
			"application": p.pol.AppKey, "policy_version": p.pol.Version, "files": len(changed)}); err != nil {
			return err
		}
		list, err := adoptionsOf(ctx, tx, "id = $1", id)
		if err != nil {
			return err
		}
		if len(list) > 0 {
			out = &list[0]
		}
		return nil
	})
	return out, err
}

const selectAdoption = `select id::text, application_element_id::text, integration_id::text, repository, base_branch, branch, pr_number, pr_url, title, state,
	policy_version, class_key, files, last_error, created_at, synced_at, closed_at from pipeline_adoptions`

func adoptionsOf(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]AdoptionPR, error) {
	rows, err := tx.Query(ctx, selectAdoption+" where "+where+" order by created_at desc limit 30", args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdoptionPR, error) {
		var p AdoptionPR
		var raw []byte
		err := r.Scan(&p.ID, &p.ApplicationID, &p.IntegrationID, &p.Repository, &p.BaseBranch, &p.Branch, &p.Number, &p.URL, &p.Title, &p.State,
			&p.PolicyVersion, &p.Class, &raw, &p.LastError, &p.CreatedAt, &p.SyncedAt, &p.ClosedAt)
		p.Files = []AdoptedFile{}
		_ = json.Unmarshal(raw, &p.Files)
		return p, err
	})
}

// ---------------------------------------------------------------------
// Sincronizzazione dello stato delle pull request

func stateOf(p *ghapi.Pull) string {
	switch {
	case p.Merged:
		return "merged"
	case p.State == "closed":
		return "closed"
	}
	return "open"
}

// SyncAdoptions rilegge da GitHub lo stato delle pull request aperte di un'applicazione (o
// di tutte, con appID vuoto). Con force ignora l'intervallo minimo tra due letture.
func (s *Service) SyncAdoptions(ctx context.Context, tenantID, appID string, force bool) (int, error) {
	type row struct {
		id, integration, repo string
		number                int
	}
	var rows []row
	where := "state = 'open'"
	var args []any
	if appID != "" {
		where += " and application_element_id = $1"
		args = append(args, appID)
	}
	if !force {
		where += " and (synced_at is null or synced_at < now() - interval '1 minute')"
	}
	if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rs, err := tx.Query(ctx, "select id::text, integration_id::text, repository, pr_number from pipeline_adoptions where "+where+" order by synced_at nulls first limit 100", args...)
		if err != nil {
			return err
		}
		rows, err = pgx.CollectRows(rs, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.id, &x.integration, &x.repo, &x.number)
		})
		return err
	}); err != nil || len(rows) == 0 {
		return 0, err
	}
	clients := map[string]*ghapi.Client{}
	type result struct {
		id   string
		pr   *ghapi.Pull
		fail string
	}
	var results []result
	for _, r := range rows {
		cl, ok := clients[r.integration]
		if !ok {
			var it *Integration
			var sec secrets
			if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
				var err error
				it, sec, err = s.load(ctx, tx, r.integration)
				return err
			}); err == nil {
				cl, _ = s.client(it.ID, it.Config, sec)
			}
			clients[r.integration] = cl
		}
		if cl == nil {
			results = append(results, result{id: r.id, fail: "integrazione non disponibile"})
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		pr, err := cl.GetPull(cctx, r.repo, r.number)
		cancel()
		switch {
		case ghapi.IsNotFound(err):
			results = append(results, result{id: r.id, pr: &ghapi.Pull{State: "closed"}, fail: "la pull request non esiste più su GitHub"})
		case err != nil:
			results = append(results, result{id: r.id, fail: err.Error()})
		default:
			results = append(results, result{id: r.id, pr: pr})
		}
	}
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		for _, r := range results {
			if r.pr == nil {
				if _, err := tx.Exec(ctx, "update pipeline_adoptions set last_error = $2, synced_at = now() where id = $1", r.id, clipText(r.fail, 300)); err != nil {
					return err
				}
				continue
			}
			st := stateOf(r.pr)
			var closed any
			if st != "open" {
				switch {
				case r.pr.MergedAt != nil:
					closed = *r.pr.MergedAt
				case r.pr.ClosedAt != nil:
					closed = *r.pr.ClosedAt
				default:
					closed = s.o.Now().UTC()
				}
			}
			if _, err := tx.Exec(ctx, `update pipeline_adoptions set state = $2, closed_at = $3, last_error = $4, synced_at = now(), updated_at = now() where id = $1`,
				r.id, st, closed, nullable(r.fail)); err != nil {
				return err
			}
		}
		return nil
	})
	return len(results), err
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// SyncAdoptionsDue aggiorna le pull request aperte di tutti i tenant non lette da più di
// dieci minuti. Lo chiama il job periodico del connettore.
func (s *Service) SyncAdoptionsDue(ctx context.Context) (int, error) {
	rows, err := s.o.DB.Owner.Query(ctx, `select distinct tenant_id::text from pipeline_adoptions
		where state = 'open' and (synced_at is null or synced_at < now() - interval '10 minutes') limit 50`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, t := range tenants {
		if ctx.Err() != nil {
			break
		}
		c, err := s.SyncAdoptions(ctx, t, "", true)
		n += c
		if err != nil {
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
}

// adoptionEvent applica l'evento «pull_request» di un webhook alle pull request di Regia.
func (s *Service) adoptionEvent(ctx context.Context, tx pgx.Tx, it *Integration, body []byte) (string, error) {
	var p struct {
		Number      int `json:"number"`
		PullRequest struct {
			State    string     `json:"state"`
			Merged   bool       `json:"merged"`
			MergedAt *time.Time `json:"merged_at"`
			ClosedAt *time.Time `json:"closed_at"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Number == 0 {
		return "corpo non leggibile: ignorato", nil
	}
	pr := &ghapi.Pull{State: p.PullRequest.State, Merged: p.PullRequest.Merged, MergedAt: p.PullRequest.MergedAt, ClosedAt: p.PullRequest.ClosedAt}
	st := stateOf(pr)
	var closed any
	if st != "open" {
		closed = s.o.Now().UTC()
		if pr.MergedAt != nil {
			closed = *pr.MergedAt
		} else if pr.ClosedAt != nil {
			closed = *pr.ClosedAt
		}
	}
	tag, err := tx.Exec(ctx, `update pipeline_adoptions set state = $4, closed_at = $5, synced_at = now(), updated_at = now(), last_error = null
		where integration_id = $1 and lower(repository) = lower($2) and pr_number = $3`, it.ID, p.Repository.FullName, p.Number, st, closed)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "pull request non aperta da Regia: ignorata", nil
	}
	return "stato della pull request aggiornato (" + st + ")", nil
}
