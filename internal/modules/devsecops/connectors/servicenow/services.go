package servicenow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/servicenow/snapi"
	"regia/internal/platform/db"
)

// I servizi della CMDB stanno in connector_repositories, la tabella generica dei
// connettori: external_id = sys_id, full_name e name = nome del CI, description = classe
// (sys_class_name), html_url = link al CI, cursors = codice letto dal campo configurato e
// cursori della sincronizzazione (sys_updated_on già letto di incidenti e change).
// service_name ha il prefisso «servicenow:»: un servizio CMDB non è un servizio di
// rilascio e non deve scontrarsi con i nomi dei repository.

// CMDBService è un servizio scoperto, con la sua associazione.
type CMDBService struct {
	ID           string                 `json:"id"`
	SysID        string                 `json:"sys_id"`
	Name         string                 `json:"name"`
	Class        string                 `json:"class"`
	Code         string                 `json:"code,omitempty"`
	URL          string                 `json:"url"`
	Missing      bool                   `json:"missing"`
	Status       string                 `json:"status"` // none | suggested | confirmed | ignored
	Application  *ghconn.AppRef         `json:"application"`
	Suggestion   *ghconn.SuggestionView `json:"suggestion"`
	Incidents    int                    `json:"incidents"`
	Changes      int                    `json:"changes"`
	LastSyncedAt *time.Time             `json:"last_synced_at"`
	LastError    *string                `json:"last_error"`
	ConfirmedAt  *time.Time             `json:"confirmed_at"`
}

type svcCursors struct {
	Code      string `json:"code,omitempty"`
	Incidents string `json:"incidents,omitempty"` // sys_updated_on (UTC, formato della Table API)
	Changes   string `json:"changes,omitempty"`
}

type svcRow struct {
	CMDBService
	cur svcCursors
}

const selectService = `
	select r.id, r.external_id, r.name, r.description, r.html_url, r.missing_since is not null, r.mapping_status,
	  a.id, sa.key || '-' || a.number, a.title, coalesce(xa.code, ''),
	  g.id, sg.key || '-' || g.number, g.title, coalesce(xg.code, ''), r.suggestion_score, r.suggestion_reason,
	  (select count(*) from servicenow_incidents x where x.repository_id = r.id),
	  (select count(*) from servicenow_changes x where x.repository_id = r.id),
	  r.last_synced_at, r.last_error, r.confirmed_at, r.cursors
	from connector_repositories r
	left join elements a on a.tenant_id = r.tenant_id and a.id = r.application_element_id and a.deleted_at is null
	left join spaces sa on sa.tenant_id = a.tenant_id and sa.id = a.space_id
	left join ext_application xa on xa.tenant_id = a.tenant_id and xa.element_id = a.id
	left join elements g on g.tenant_id = r.tenant_id and g.id = r.suggested_element_id and g.deleted_at is null
	left join spaces sg on sg.tenant_id = g.tenant_id and sg.id = g.space_id
	left join ext_application xg on xg.tenant_id = g.tenant_id and xg.element_id = g.id
	where true`

func scanServices(rows pgx.Rows) ([]svcRow, error) {
	defer rows.Close()
	var out []svcRow
	for rows.Next() {
		var r svcRow
		var aID, aKey, aName, aCode, gID, gKey, gName, gCode, reason *string
		var score *float32
		var cur []byte
		if err := rows.Scan(&r.ID, &r.SysID, &r.Name, &r.Class, &r.URL, &r.Missing, &r.Status, &aID, &aKey, &aName, &aCode,
			&gID, &gKey, &gName, &gCode, &score, &reason, &r.Incidents, &r.Changes, &r.LastSyncedAt, &r.LastError, &r.ConfirmedAt, &cur); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cur, &r.cur)
		r.Code = r.cur.Code
		if aID != nil {
			r.Application = &ghconn.AppRef{ID: *aID, Key: deref(aKey), Name: deref(aName), Code: deref(aCode)}
		}
		if gID != nil && r.Status == "suggested" {
			r.Suggestion = &ghconn.SuggestionView{App: ghconn.AppRef{ID: *gID, Key: deref(gKey), Name: deref(gName), Code: deref(gCode)}, Reason: deref(reason)}
			if score != nil {
				r.Suggestion.Score = float64(*score)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Services elenca i servizi scoperti di un'integrazione.
func (s *Service) Services(ctx context.Context, tenantID, integrationID string) ([]CMDBService, error) {
	out := []CMDBService{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectService+` and r.integration_id = $1
			order by r.missing_since is not null, case r.mapping_status when 'suggested' then 0 when 'none' then 1 when 'confirmed' then 2 else 3 end, lower(r.name)`, integrationID)
		if err != nil {
			return err
		}
		list, err := scanServices(rows)
		for _, r := range list {
			out = append(out, r.CMDBService)
		}
		return err
	})
	return out, err
}

func loadApps(ctx context.Context, tx pgx.Tx) ([]ghconn.App, error) {
	rows, err := tx.Query(ctx, `
		select e.id, s.key || '-' || e.number, e.title, coalesce(a.code, '')
		from elements e
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.deleted_at is null`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ghconn.App, error) {
		var a ghconn.App
		err := r.Scan(&a.ID, &a.Key, &a.Name, &a.Code)
		return a, err
	})
}

// DiscoverResult riassume una scoperta dei servizi.
type DiscoverResult struct {
	Found     int `json:"found"`
	New       int `json:"new"`
	Missing   int `json:"missing"`
	Suggested int `json:"suggested"`
}

// MaxServices è il numero massimo di servizi letti dalla CMDB per integrazione: oltre,
// serve un filtro (per esempio operational_status=1 o un gruppo di supporto).
const MaxServices = 3000

// Discover legge i servizi della classe CMDB e aggiorna le proposte di associazione.
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
	cl, err := s.client(it.Config, sec)
	if err != nil {
		return nil, &Error{Status: 422, Code: "integration.config", Msg: err.Error()}
	}
	res, err := s.discover(ctx, a.TenantID, it, cl)
	if err != nil {
		return nil, err
	}
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.servicenow.discover", "integration", integrationID, map[string]any{"found": res.Found, "new": res.New, "missing": res.Missing})
	})
	return res, err
}

type cmdbItem struct{ sysID, name, class, code string }

func (s *Service) discover(ctx context.Context, tenantID string, it *Integration, cl *snapi.Client) (*DiscoverResult, error) {
	fields := []string{"sys_id", "name", "sys_class_name"}
	if it.Config.CodeField != "" {
		fields = append(fields, it.Config.CodeField)
	}
	q := it.Config.ServiceFilter
	if q != "" {
		q += "^"
	}
	q += "ORDERBYname"
	var found []cmdbItem
	seen := map[string]bool{}
	err := cl.Table(ctx, snapi.Query{Table: it.Config.ServiceClass, Query: q, Fields: fields, Limit: MaxServices + 1}, func(r map[string]string) (bool, error) {
		id := strings.TrimSpace(r["sys_id"])
		name := strings.Join(strings.Fields(r["name"]), " ")
		if id == "" || name == "" || seen[id] {
			return false, nil
		}
		seen[id] = true
		class := r["sys_class_name"]
		if class == "" {
			class = it.Config.ServiceClass
		}
		found = append(found, cmdbItem{sysID: truncate(id, 64), name: truncate(name, 200), class: truncate(class, 80), code: truncate(strings.TrimSpace(r[it.Config.CodeField]), 100)})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) > MaxServices {
		return nil, invalid("service_filter", "la classe ha più di 3000 servizi: aggiungi un filtro (per esempio operational_status=1)")
	}
	res := &DiscoverResult{Found: len(found)}
	err = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(found))
		for _, r := range found {
			ids = append(ids, r.sysID)
			var inserted bool
			if err := tx.QueryRow(ctx, `
				insert into connector_repositories (tenant_id, id, integration_id, external_id, full_name, name, default_branch, html_url,
				  description, service_name, cursors)
				values (app_tenant(), $1, $2, $3, $4, $4, '', $5, $6, $7, jsonb_build_object('code', $8::text))
				on conflict (tenant_id, integration_id, external_id) do update set full_name = excluded.full_name, name = excluded.name,
				  html_url = excluded.html_url, description = excluded.description, missing_since = null, updated_at = now(),
				  cursors = connector_repositories.cursors || jsonb_build_object('code', $8::text)
				returning (xmax = 0)`,
				db.NewID(), it.ID, r.sysID, r.name, snapi.RecordURL(it.Config.BaseURL, r.class, r.sysID), r.class,
				"servicenow:"+strings.ToLower(r.sysID), r.code).Scan(&inserted); err != nil {
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

// suggest ricalcola le proposte dei servizi non ancora associati né ignorati: vince il
// confronto migliore tra il nome del CI e, se c'è, il codice letto dal campo configurato.
func (s *Service) suggest(ctx context.Context, tx pgx.Tx, integrationID string) (int, error) {
	apps, err := loadApps(ctx, tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `select id, name, coalesce(cursors->>'code', '') from connector_repositories
		where integration_id = $1 and mapping_status in ('none', 'suggested') and missing_since is null`, integrationID)
	if err != nil {
		return 0, err
	}
	type pending struct{ id, name, code string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		err := r.Scan(&p.id, &p.name, &p.code)
		return p, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range list {
		sg := ghconn.Suggest(p.name, apps)
		if p.code != "" {
			if byCode := ghconn.Suggest(p.code, apps); byCode != nil && (sg == nil || byCode.Score > sg.Score) {
				byCode.Reason = "codice del CI «" + p.code + "» (" + byCode.Reason + ")"
				sg = byCode
			}
		}
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

// MapInput conferma, ignora o annulla l'associazione di un servizio.
type MapInput struct {
	Status        string  `json:"status"` // confirmed | ignored | none
	ApplicationID *string `json:"application_id"`
}

// MapService applica una decisione sull'associazione.
func (s *Service) MapService(ctx context.Context, a Actor, integrationID, serviceID string, in MapInput) (*CMDBService, error) {
	if !validID(serviceID) {
		return nil, ErrServiceNotFound
	}
	var out *CMDBService
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		r, err := s.mapOne(ctx, tx, a, integrationID, serviceID, in)
		out = r
		return err
	})
	return out, err
}

func (s *Service) mapOne(ctx context.Context, tx pgx.Tx, a Actor, integrationID, serviceID string, in MapInput) (*CMDBService, error) {
	rows, err := tx.Query(ctx, selectService+" and r.integration_id = $1 and r.id = $2 for update of r", integrationID, serviceID)
	if err != nil {
		return nil, err
	}
	list, err := scanServices(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrServiceNotFound
	}
	cur := list[0]
	var appID string
	switch in.Status {
	case "confirmed":
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
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'confirmed', application_element_id = $2,
			confirmed_by = $3, confirmed_at = now(), updated_at = now() where id = $1`, serviceID, appID, nullable(a.MemberID)); err != nil {
			return nil, err
		}
	case "ignored":
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'ignored', application_element_id = null,
			confirmed_by = null, confirmed_at = null, updated_at = now() where id = $1`, serviceID); err != nil {
			return nil, err
		}
	case "none":
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = 'none', application_element_id = null,
			confirmed_by = null, confirmed_at = null, updated_at = now() where id = $1`, serviceID); err != nil {
			return nil, err
		}
		if _, err := s.suggest(ctx, tx, integrationID); err != nil {
			return nil, err
		}
	default:
		return nil, invalid("status", "ammessi confirmed, ignored e none")
	}
	// Gli incidenti e le change già letti seguono l'associazione: senza applicazione escono
	// dalle metriche, con un'altra applicazione passano a quella.
	for _, t := range []string{"servicenow_incidents", "servicenow_changes"} {
		if _, err := tx.Exec(ctx, "update "+t+" set application_element_id = $2, updated_at = now() where repository_id = $1", serviceID, nullable(appID)); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, a, "devsecops.servicenow.service_map", "integration", integrationID,
		map[string]any{"service": cur.Name, "sys_id": cur.SysID, "status": in.Status, "application": nullable(appID)}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, selectService+" and r.id = $1", serviceID)
	if err != nil {
		return nil, err
	}
	list, err = scanServices(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	out := list[0].CMDBService
	return &out, nil
}

// AcceptSuggestions conferma in blocco le proposte dei servizi indicati.
func (s *Service) AcceptSuggestions(ctx context.Context, a Actor, integrationID string, ids []string) (int, error) {
	if len(ids) > 500 {
		return 0, invalid("ids", "al massimo 500 servizi alla volta")
	}
	n := 0
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		for _, id := range ids {
			if !validID(id) {
				return ErrServiceNotFound
			}
			var suggested *string
			if err := tx.QueryRow(ctx, "select suggested_element_id::text from connector_repositories where id = $1 and integration_id = $2 and mapping_status = 'suggested'",
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
