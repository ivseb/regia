package github

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// AppRef è un'applicazione del catalogo come appare accanto a un repository.
type AppRef struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	Code string `json:"code"`
}

// SuggestionView è la proposta di associazione.
type SuggestionView struct {
	App    AppRef  `json:"application"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Repository è un repository scoperto.
type Repository struct {
	ID            string          `json:"id"`
	ExternalID    string          `json:"external_id"`
	FullName      string          `json:"full_name"`
	Name          string          `json:"name"`
	DefaultBranch string          `json:"default_branch"`
	HTMLURL       string          `json:"html_url"`
	Description   string          `json:"description"`
	Archived      bool            `json:"archived"`
	Private       bool            `json:"private"`
	Missing       bool            `json:"missing"`
	Status        string          `json:"status"` // none | suggested | confirmed | ignored
	Application   *AppRef         `json:"application"`
	Suggestion    *SuggestionView `json:"suggestion"`
	ServiceName   string          `json:"service_name"`
	LastSyncedAt  *time.Time      `json:"last_synced_at"`
	LastError     *string         `json:"last_error"`
	ConfirmedAt   *time.Time      `json:"confirmed_at"`
}

const selectRepo = `
	select r.id, r.external_id, r.full_name, r.name, r.default_branch, r.html_url, r.description, r.archived, r.private,
	  r.missing_since is not null, r.mapping_status,
	  a.id, sa.key || '-' || a.number, a.title, coalesce(xa.code, ''),
	  g.id, sg.key || '-' || g.number, g.title, coalesce(xg.code, ''), r.suggestion_score, r.suggestion_reason,
	  r.service_name, r.last_synced_at, r.last_error, r.confirmed_at, r.cursors
	from connector_repositories r
	left join elements a on a.tenant_id = r.tenant_id and a.id = r.application_element_id and a.deleted_at is null
	left join spaces sa on sa.tenant_id = a.tenant_id and sa.id = a.space_id
	left join ext_application xa on xa.tenant_id = a.tenant_id and xa.element_id = a.id
	left join elements g on g.tenant_id = r.tenant_id and g.id = r.suggested_element_id and g.deleted_at is null
	left join spaces sg on sg.tenant_id = g.tenant_id and sg.id = g.space_id
	left join ext_application xg on xg.tenant_id = g.tenant_id and xg.element_id = g.id
	where true`

// repoRow è un repository con i cursori (uso interno).
type repoRow struct {
	Repository
	cursors []byte
}

func scanRepos(rows pgx.Rows) ([]repoRow, error) {
	defer rows.Close()
	var out []repoRow
	for rows.Next() {
		var r repoRow
		var aID, aKey, aName, aCode, gID, gKey, gName, gCode *string
		var score *float32
		var reason *string
		if err := rows.Scan(&r.ID, &r.ExternalID, &r.FullName, &r.Name, &r.DefaultBranch, &r.HTMLURL, &r.Description, &r.Archived, &r.Private,
			&r.Missing, &r.Status, &aID, &aKey, &aName, &aCode, &gID, &gKey, &gName, &gCode, &score, &reason,
			&r.ServiceName, &r.LastSyncedAt, &r.LastError, &r.ConfirmedAt, &r.cursors); err != nil {
			return nil, err
		}
		if aID != nil {
			r.Application = &AppRef{ID: *aID, Key: deref(aKey), Name: deref(aName), Code: deref(aCode)}
		}
		if gID != nil && r.Status == "suggested" {
			r.Suggestion = &SuggestionView{App: AppRef{ID: *gID, Key: deref(gKey), Name: deref(gName), Code: deref(gCode)}, Reason: deref(reason)}
			if score != nil {
				r.Suggestion.Score = float64(*score)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// service è il servizio del contratto per un repository associato.
func (r *Repository) service() contract.Service {
	s := contract.Service{Name: r.ServiceName, Repository: r.FullName, Provider: "github", URL: r.HTMLURL}
	if r.Application != nil {
		s.Application = App{Key: r.Application.Key, Code: r.Application.Code}.Ref()
	}
	return s
}

// Repositories elenca i repository scoperti di un'integrazione.
func (s *Service) Repositories(ctx context.Context, tenantID, integrationID string) ([]Repository, error) {
	out := []Repository{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectRepo+` and r.integration_id = $1
			order by r.missing_since is not null, case r.mapping_status when 'suggested' then 0 when 'none' then 1 when 'confirmed' then 2 else 3 end, lower(r.full_name)`, integrationID)
		if err != nil {
			return err
		}
		list, err := scanRepos(rows)
		for _, r := range list {
			out = append(out, r.Repository)
		}
		return err
	})
	return out, err
}

func loadApps(ctx context.Context, tx pgx.Tx) ([]App, error) {
	rows, err := tx.Query(ctx, `
		select e.id, s.key || '-' || e.number, e.title, coalesce(a.code, '')
		from elements e
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.deleted_at is null`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (App, error) {
		var a App
		err := r.Scan(&a.ID, &a.Key, &a.Name, &a.Code)
		return a, err
	})
}

// DiscoverResult riassume una scoperta dei repository.
type DiscoverResult struct {
	Found     int `json:"found"`
	New       int `json:"new"`
	Missing   int `json:"missing"`
	Suggested int `json:"suggested"`
}

// Discover legge i repository delle organizzazioni e aggiorna le proposte di associazione.
func (s *Service) Discover(ctx context.Context, a Actor, integrationID string) (*DiscoverResult, error) {
	var it *Integration
	var sec secrets
	if err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		it, sec, err = s.load(ctx, tx, integrationID)
		return err
	}); err != nil {
		return nil, err
	}
	cl, err := s.client(integrationID, it.Config, sec)
	if err != nil {
		return nil, &Error{Status: 422, Code: "integration.config", Msg: err.Error()}
	}
	res, err := s.discover(ctx, a.TenantID, it, cl)
	if err != nil {
		return nil, err
	}
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.github.discover", "integration", integrationID, map[string]any{"found": res.Found, "new": res.New, "missing": res.Missing})
	})
	return res, err
}

func (s *Service) discover(ctx context.Context, tenantID string, it *Integration, cl *ghapi.Client) (*DiscoverResult, error) {
	var found []ghapi.Repo
	for _, owner := range it.Config.Owners {
		if err := cl.OwnerRepos(ctx, owner, func(r ghapi.Repo) error {
			if r.Fork || (r.Archived && !it.Config.IncludeArchived) {
				return nil
			}
			found = append(found, r)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	res := &DiscoverResult{Found: len(found)}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(found))
		for _, r := range found {
			ext := strconv.FormatInt(r.ID, 10)
			ids = append(ids, ext)
			branch := r.DefaultBranch
			if branch == "" {
				branch = "main"
			}
			var inserted bool
			if err := tx.QueryRow(ctx, `
				insert into connector_repositories (tenant_id, id, integration_id, external_id, full_name, name, default_branch, html_url,
				  description, archived, private, service_name)
				values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				on conflict (tenant_id, integration_id, external_id) do update set full_name = excluded.full_name, name = excluded.name,
				  default_branch = excluded.default_branch, html_url = excluded.html_url, description = excluded.description,
				  archived = excluded.archived, private = excluded.private, missing_since = null, updated_at = now()
				returning (xmax = 0)`,
				db.NewID(), it.ID, ext, r.FullName, r.Name, branch, r.HTMLURL, truncate(r.Description, 500), r.Archived, r.Private,
				strings.ToLower(r.Name)).Scan(&inserted); err != nil {
				return err
			}
			if inserted {
				res.New++
			}
		}
		tag, err := tx.Exec(ctx, `update connector_repositories set missing_since = now(), updated_at = now()
			where integration_id = $1 and missing_since is null and not (external_id = any($2))`, it.ID, ids)
		if err != nil {
			return err
		}
		res.Missing = int(tag.RowsAffected())
		n, err := s.suggest(ctx, tx, it.ID)
		res.Suggested = n
		return err
	})
	return res, err
}

// suggest ricalcola le proposte dei repository non ancora associati né ignorati.
func (s *Service) suggest(ctx context.Context, tx pgx.Tx, integrationID string) (int, error) {
	apps, err := loadApps(ctx, tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `select id, name from connector_repositories
		where integration_id = $1 and mapping_status in ('none', 'suggested') and missing_since is null`, integrationID)
	if err != nil {
		return 0, err
	}
	type pending struct{ id, name string }
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, p)
	}
	rows.Close()
	n := 0
	for _, p := range list {
		sg := Suggest(p.name, apps)
		if sg == nil {
			if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'none', suggested_element_id = null,
				suggestion_score = null, suggestion_reason = null where id = $1`, p.id); err != nil {
				return n, err
			}
			continue
		}
		n++
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'suggested', suggested_element_id = $2,
			suggestion_score = $3, suggestion_reason = $4 where id = $1`, p.id, sg.App.ID, sg.Score, sg.Reason); err != nil {
			return n, err
		}
	}
	return n, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// MapInput conferma, ignora o annulla l'associazione di un repository.
type MapInput struct {
	Status        string  `json:"status"` // confirmed | ignored | none
	ApplicationID *string `json:"application_id"`
	ServiceName   *string `json:"service_name"`
}

// MapRepository applica una decisione sull'associazione.
func (s *Service) MapRepository(ctx context.Context, a Actor, integrationID, repoID string, in MapInput) (*Repository, error) {
	if !validID(repoID) {
		return nil, ErrRepoNotFound
	}
	var out *Repository
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		r, err := s.mapOne(ctx, tx, a, integrationID, repoID, in)
		out = r
		return err
	})
	return out, err
}

func (s *Service) mapOne(ctx context.Context, tx pgx.Tx, a Actor, integrationID, repoID string, in MapInput) (*Repository, error) {
	rows, err := tx.Query(ctx, selectRepo+" and r.integration_id = $1 and r.id = $2 for update of r", integrationID, repoID)
	if err != nil {
		return nil, err
	}
	list, err := scanRepos(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrRepoNotFound
	}
	cur := list[0]
	svcName := cur.ServiceName
	if in.ServiceName != nil {
		svcName = strings.ToLower(strings.TrimSpace(*in.ServiceName))
		if svcName == "" || len(svcName) > 100 || strings.ContainsAny(svcName, " /\\") {
			return nil, invalid("service_name", "nome del servizio non valido (minuscole, senza spazi né barre, al massimo 100 caratteri)")
		}
	}
	switch in.Status {
	case "confirmed":
		appID := ""
		if in.ApplicationID != nil {
			appID = strings.TrimSpace(*in.ApplicationID)
		} else if cur.Application != nil {
			appID = cur.Application.ID
		} else if cur.Suggestion != nil {
			appID = cur.Suggestion.App.ID
		}
		if !validID(appID) {
			return nil, invalid("application_id", "scegli l'applicazione del catalogo")
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from ext_application a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			where a.element_id = $1 and e.deleted_at is null)`, appID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, invalid("application_id", "applicazione inesistente")
		}
		var dup string
		err := tx.QueryRow(ctx, `select full_name from connector_repositories where mapping_status = 'confirmed' and lower(service_name) = $1 and id <> $2 limit 1`,
			svcName, repoID).Scan(&dup)
		if err == nil {
			return nil, invalid("service_name", "il nome del servizio «"+svcName+"» è già usato da "+dup+": scegline un altro")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'confirmed', application_element_id = $2, service_name = $3,
			confirmed_by = $4, confirmed_at = now(), updated_at = now() where id = $1`, repoID, appID, svcName, nullable(a.MemberID)); err != nil {
			return nil, err
		}
	case "ignored":
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'ignored', application_element_id = null,
			updated_at = now() where id = $1`, repoID); err != nil {
			return nil, err
		}
	case "none":
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'none', application_element_id = null, service_name = $2,
			confirmed_by = null, confirmed_at = null, updated_at = now() where id = $1`, repoID, svcName); err != nil {
			return nil, err
		}
		if _, err := s.suggest(ctx, tx, integrationID); err != nil {
			return nil, err
		}
	default:
		return nil, invalid("status", "ammessi confirmed, ignored e none")
	}
	if err := audited(ctx, tx, a, "devsecops.github.repository_map", "integration", integrationID,
		map[string]any{"repository": cur.FullName, "status": in.Status, "service": svcName, "application": in.ApplicationID}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, selectRepo+" and r.id = $1", repoID)
	if err != nil {
		return nil, err
	}
	list, err = scanRepos(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	out := list[0].Repository
	// Il servizio appena associato si annuncia al modulo devsecops, se lo vuole sapere.
	if out.Status == "confirmed" {
		if si, ok := s.o.Ingestor.(contract.ServiceIngestor); ok {
			if err := si.IngestService(ctx, tx, systemActor(a), out.service()); err != nil {
				return nil, err
			}
		}
	}
	return &out, nil
}

// AcceptSuggestions conferma in blocco le proposte dei repository indicati.
func (s *Service) AcceptSuggestions(ctx context.Context, a Actor, integrationID string, repoIDs []string) (int, error) {
	if len(repoIDs) > 500 {
		return 0, invalid("ids", "al massimo 500 repository alla volta")
	}
	n := 0
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		for _, id := range repoIDs {
			if !validID(id) {
				return ErrRepoNotFound
			}
			var suggested *string
			if err := tx.QueryRow(ctx, "select suggested_element_id from connector_repositories where id = $1 and integration_id = $2 and mapping_status = 'suggested'",
				id, integrationID).Scan(&suggested); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return err
			}
			if suggested == nil {
				continue
			}
			if _, err := s.mapOne(ctx, tx, a, integrationID, id, MapInput{Status: "confirmed", ApplicationID: suggested}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
