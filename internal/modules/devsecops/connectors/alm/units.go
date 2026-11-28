package alm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/modules/devsecops/connectors/alm/almapi"
	"regia/internal/platform/db"
)

// Le unità ALM stanno in connector_repositories, la tabella generica dei connettori. Un'unità
// è ciò che corrisponde a un'applicazione del catalogo, secondo la configurazione:
//
//	progetto  external_id «p:<dominio>/<progetto>», nome = progetto
//	cartella  external_id «f:<dominio>/<progetto>/<id>», nome = cartella di primo livello del Test Lab
//	campo     external_id «v:<valore in minuscolo>», nome = valore del campo del test set
//
// description dice il tipo (progetto, cartella, campo), full_name il percorso leggibile.
// service_name ha il prefisso «alm:»: un'unità ALM non è un servizio di rilascio.

// Unit è un'unità scoperta, con la sua associazione.
type Unit struct {
	ID           string                 `json:"id"`
	Kind         string                 `json:"kind"` // project | folder | value
	Name         string                 `json:"name"`
	Path         string                 `json:"path"`
	Missing      bool                   `json:"missing"`
	Status       string                 `json:"status"` // none | suggested | confirmed | ignored
	Application  *ghconn.AppRef         `json:"application"`
	Suggestion   *ghconn.SuggestionView `json:"suggestion"`
	TestSets     int                    `json:"test_sets"`
	LastSyncedAt *time.Time             `json:"last_synced_at"`
	ConfirmedAt  *time.Time             `json:"confirmed_at"`
}

const selectUnit = `
	select r.id, r.description, r.name, r.full_name, r.missing_since is not null, r.mapping_status,
	  a.id, sa.key || '-' || a.number, a.title, coalesce(xa.code, ''),
	  g.id, sg.key || '-' || g.number, g.title, coalesce(xg.code, ''), r.suggestion_score, r.suggestion_reason,
	  (select count(*) from test_sets x where x.repository_id = r.id),
	  r.last_synced_at, r.confirmed_at
	from connector_repositories r
	left join elements a on a.tenant_id = r.tenant_id and a.id = r.application_element_id and a.deleted_at is null
	left join spaces sa on sa.tenant_id = a.tenant_id and sa.id = a.space_id
	left join ext_application xa on xa.tenant_id = a.tenant_id and xa.element_id = a.id
	left join elements g on g.tenant_id = r.tenant_id and g.id = r.suggested_element_id and g.deleted_at is null
	left join spaces sg on sg.tenant_id = g.tenant_id and sg.id = g.space_id
	left join ext_application xg on xg.tenant_id = g.tenant_id and xg.element_id = g.id
	where true`

func scanUnits(rows pgx.Rows) ([]Unit, error) {
	defer rows.Close()
	out := []Unit{}
	for rows.Next() {
		var u Unit
		var aID, aKey, aName, aCode, gID, gKey, gName, gCode, reason *string
		var score *float32
		if err := rows.Scan(&u.ID, &u.Kind, &u.Name, &u.Path, &u.Missing, &u.Status, &aID, &aKey, &aName, &aCode,
			&gID, &gKey, &gName, &gCode, &score, &reason, &u.TestSets, &u.LastSyncedAt, &u.ConfirmedAt); err != nil {
			return nil, err
		}
		if aID != nil {
			u.Application = &ghconn.AppRef{ID: *aID, Key: deref(aKey), Name: deref(aName), Code: deref(aCode)}
		}
		if gID != nil && u.Status == "suggested" {
			u.Suggestion = &ghconn.SuggestionView{App: ghconn.AppRef{ID: *gID, Key: deref(gKey), Name: deref(gName), Code: deref(gCode)}, Reason: deref(reason)}
			if score != nil {
				u.Suggestion.Score = float64(*score)
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Units elenca le unità di un'integrazione.
func (s *Service) Units(ctx context.Context, tenantID, integrationID string) ([]Unit, error) {
	out := []Unit{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectUnit+` and r.integration_id = $1
			order by r.missing_since is not null, case r.mapping_status when 'suggested' then 0 when 'none' then 1 when 'confirmed' then 2 else 3 end, lower(r.full_name)`, integrationID)
		if err != nil {
			return err
		}
		out, err = scanUnits(rows)
		return err
	})
	return out, err
}

// unitRef è un'unità come la calcola la sincronizzazione.
type unitRef struct {
	external, kind, name, path string
}

func projectUnit(p Project) unitRef {
	return unitRef{external: "p:" + p.key(), kind: "project", name: p.Project, path: p.key()}
}

func folderUnit(p Project, id, name string) unitRef {
	return unitRef{external: "f:" + p.key() + "/" + id, kind: "folder", name: name, path: p.key() + " · " + name}
}

func valueUnit(v string) unitRef {
	v = strings.Join(strings.Fields(v), " ")
	return unitRef{external: "v:" + strings.ToLower(truncate(v, 200)), kind: "value", name: truncate(v, 200), path: v}
}

// ensureUnits crea le unità che mancano (e toglie il segno «scomparsa» a quelle viste) e
// restituisce external_id → (id della riga, applicazione confermata).
func (s *Service) ensureUnits(ctx context.Context, tx pgx.Tx, integrationID string, units []unitRef) (map[string]unitRow, int, error) {
	created := 0
	for _, u := range units {
		var inserted bool
		if err := tx.QueryRow(ctx, `
			insert into connector_repositories (tenant_id, id, integration_id, external_id, full_name, name, default_branch, html_url, description, service_name)
			values (app_tenant(), $1, $2, $3, $4, $5, '', '', $6, $7)
			on conflict (tenant_id, integration_id, external_id) do update set full_name = excluded.full_name, name = excluded.name,
			  missing_since = null, updated_at = now()
			returning (xmax = 0)`, db.NewID(), integrationID, truncate(u.external, 400), truncate(u.path, 400), truncate(u.name, 200), u.kind,
			"alm:"+truncate(u.external, 300)).Scan(&inserted); err != nil {
			return nil, 0, err
		}
		if inserted {
			created++
		}
	}
	if created > 0 {
		if _, err := s.suggest(ctx, tx, integrationID); err != nil {
			return nil, 0, err
		}
	}
	m, err := loadUnitRows(ctx, tx, integrationID)
	return m, created, err
}

type unitRow struct {
	id  string
	app *string
}

func loadUnitRows(ctx context.Context, tx pgx.Tx, integrationID string) (map[string]unitRow, error) {
	rows, err := tx.Query(ctx, `select external_id, id::text, case when mapping_status = 'confirmed' then application_element_id::text end
		from connector_repositories where integration_id = $1`, integrationID)
	if err != nil {
		return nil, err
	}
	m := map[string]unitRow{}
	var ext, id string
	var app *string
	_, err = pgx.ForEachRow(rows, []any{&ext, &id, &app}, func() error {
		m[ext] = unitRow{id: id, app: app}
		return nil
	})
	return m, err
}

// DiscoverResult riassume una scoperta delle unità.
type DiscoverResult struct {
	Found     int `json:"found"`
	New       int `json:"new"`
	Missing   int `json:"missing"`
	Suggested int `json:"suggested"`
}

// Discover legge le unità da ALM (progetti, cartelle di primo livello del Test Lab o valori del
// campo nei test set recenti) e aggiorna le proposte di associazione.
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
	defer cl.Logout(ctx)
	res, err := s.discover(ctx, a.TenantID, it, cl)
	if err != nil {
		return nil, err
	}
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return audited(ctx, tx, a, "devsecops.alm.discover", "integration", integrationID, map[string]any{"found": res.Found, "new": res.New, "missing": res.Missing})
	})
	return res, err
}

// MaxValues è il numero massimo di test set letti per scoprire i valori del campo applicazione.
const MaxValues = 5000

func (s *Service) discover(ctx context.Context, tenantID string, it *Integration, cl *almapi.Client) (*DiscoverResult, error) {
	var found []unitRef
	seen := map[string]bool{}
	add := func(u unitRef) {
		if !seen[u.external] {
			seen[u.external] = true
			found = append(found, u)
		}
	}
	for _, p := range it.Config.Projects {
		switch it.Config.AppSource {
		case AppProject:
			add(projectUnit(p))
		case AppFolder:
			err := cl.Entities(ctx, almapi.Query{Domain: p.Domain, Project: p.Project, Collection: "test-set-folders", Filter: "parent-id[0]",
				Fields: []string{"id", "name", "parent-id"}, OrderBy: "name[ASC]", Limit: 2000}, func(e almapi.Entity) (bool, error) {
				if name := strings.TrimSpace(e["name"]); name != "" && e["id"] != "" {
					add(folderUnit(p, e["id"], name))
				}
				return false, nil
			})
			if err != nil {
				return nil, err
			}
		case AppTestSetField:
			err := cl.Entities(ctx, almapi.Query{Domain: p.Domain, Project: p.Project, Collection: "test-sets",
				Fields: []string{"id", it.Config.AppField}, OrderBy: "last-modified[DESC]", Limit: MaxValues}, func(e almapi.Entity) (bool, error) {
				if v := strings.TrimSpace(e[it.Config.AppField]); v != "" {
					add(valueUnit(v))
				}
				return false, nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	res := &DiscoverResult{Found: len(found)}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		if _, res.New, err = s.ensureUnits(ctx, tx, it.ID, found); err != nil {
			return err
		}
		ids := make([]string, 0, len(found))
		for _, u := range found {
			ids = append(ids, u.external)
		}
		// Le unità di un altro tipo (la configurazione è cambiata) o non più trovate restano,
		// segnate come scomparse: le associazioni confermate non si perdono.
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

// suggest ricalcola le proposte delle unità non ancora associate né ignorate, dal nome
// (i trattini bassi dei progetti valgono come spazi).
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
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		err := r.Scan(&p.id, &p.name)
		return p, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range list {
		sg := ghconn.Suggest(strings.ReplaceAll(p.name, "_", " "), apps)
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

// MapInput conferma, ignora o annulla l'associazione di un'unità.
type MapInput struct {
	Status        string  `json:"status"` // confirmed | ignored | none
	ApplicationID *string `json:"application_id"`
}

// MapUnit applica una decisione sull'associazione.
func (s *Service) MapUnit(ctx context.Context, a Actor, integrationID, unitID string, in MapInput) (*Unit, error) {
	if !validID(unitID) {
		return nil, ErrUnitNotFound
	}
	var out *Unit
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		u, err := s.mapOne(ctx, tx, a, integrationID, unitID, in)
		out = u
		return err
	})
	return out, err
}

func (s *Service) mapOne(ctx context.Context, tx pgx.Tx, a Actor, integrationID, unitID string, in MapInput) (*Unit, error) {
	rows, err := tx.Query(ctx, selectUnit+" and r.integration_id = $1 and r.id = $2 for update of r", integrationID, unitID)
	if err != nil {
		return nil, err
	}
	list, err := scanUnits(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrUnitNotFound
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
			confirmed_by = $3, confirmed_at = now(), updated_at = now() where id = $1`, unitID, appID, nullable(a.MemberID)); err != nil {
			return nil, err
		}
	case "ignored", "none":
		if _, err := tx.Exec(ctx, `update connector_repositories set mapping_status = $2, application_element_id = null,
			confirmed_by = null, confirmed_at = null, updated_at = now() where id = $1`, unitID, in.Status); err != nil {
			return nil, err
		}
		if in.Status == "none" {
			if _, err := s.suggest(ctx, tx, integrationID); err != nil {
				return nil, err
			}
		}
	default:
		return nil, invalid("status", "ammessi confirmed, ignored e none")
	}
	// I test set già letti seguono l'associazione.
	if _, err := tx.Exec(ctx, "update test_sets set application_element_id = $2, updated_at = now() where repository_id = $1", unitID, nullable(appID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, a, "devsecops.alm.unit_map", "integration", integrationID,
		map[string]any{"unit": cur.Path, "kind": cur.Kind, "status": in.Status, "application": nullable(appID)}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, selectUnit+" and r.id = $1", unitID)
	if err != nil {
		return nil, err
	}
	list, err = scanUnits(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// AcceptSuggestions conferma in blocco le proposte delle unità indicate.
func (s *Service) AcceptSuggestions(ctx context.Context, a Actor, integrationID string, ids []string) (int, error) {
	if len(ids) > 500 {
		return 0, invalid("ids", "al massimo 500 unità alla volta")
	}
	n := 0
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, _, err := s.load(ctx, tx, integrationID); err != nil {
			return err
		}
		for _, id := range ids {
			if !validID(id) {
				return ErrUnitNotFound
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
