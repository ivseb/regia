package devsecops

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
)

// Configurazione → DevSecOps: impostazioni, ambienti, associazione dei servizi alle
// applicazioni, change (richiesta collegata e numero definitivo).

// ConfigService è un servizio nella pagina di configurazione.
type ConfigService struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Repository     *string    `json:"repository"`
	Provider       *string    `json:"provider"`
	Declared       *string    `json:"declared_application"`
	ApplicationID  *string    `json:"application_id"`
	ApplicationKey *string    `json:"application_key"`
	Application    *string    `json:"application"`
	Mapping        *string    `json:"mapping_source"`
	FirstSeen      *time.Time `json:"first_seen_at"`
	LastSeen       *time.Time `json:"last_seen_at"`
}

// ConfigApp è un'applicazione proposta per l'associazione.
type ConfigApp struct {
	ID   string  `json:"id"`
	Key  string  `json:"key"`
	Name string  `json:"name"`
	Code *string `json:"code"`
}

// FieldOption è un campo personalizzato della richiesta (per il numero di change).
type FieldOption struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Config struct {
	Settings     Settings        `json:"settings"`
	Environments []EnvView       `json:"environments"`
	Services     []ConfigService `json:"services"`
	Applications []ConfigApp     `json:"applications"`
	Fields       []FieldOption   `json:"request_fields"`
	Changes      []ChangeRef     `json:"changes"`
	CanEdit      bool            `json:"can_edit"`
}

func (s *Service) Config(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Config, error) {
	if !actor.Has(PermRead) && !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	c := &Config{CanEdit: CanConfigure(actor)}
	var err error
	if c.Settings, err = loadSettings(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "select id::text, name, is_production, ordinal from environments order by ordinal, name")
	if err != nil {
		return nil, err
	}
	if c.Environments, err = pgx.CollectRows(rows, pgx.RowToStructByPos[EnvView]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select s.id::text, s.name, s.repository, s.provider, s.declared_application, s.application_element_id::text,
		sp.key || '-' || e.number, e.title, s.mapping_source, s.first_seen_at, s.last_seen_at
		from services s left join elements e on e.tenant_id = s.tenant_id and e.id = s.application_element_id
		left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		order by (s.application_element_id is not null), s.name limit 2000`)
	if err != nil {
		return nil, err
	}
	if c.Services, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ConfigService]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title, a.code from elements e
		join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.deleted_at is null order by e.title limit 2000`)
	if err != nil {
		return nil, err
	}
	if c.Applications, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ConfigApp]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select f.key, f.name from field_defs f
		join type_fields tf on tf.tenant_id = f.tenant_id and tf.field_id = f.id
		join element_types t on t.tenant_id = tf.tenant_id and t.id = tf.type_id and t.key = 'business_request'
		where not f.archived and f.data_type in ('text', 'string') group by f.key, f.name order by f.name`)
	if err != nil {
		return nil, err
	}
	if c.Fields, err = pgx.CollectRows(rows, pgx.RowToStructByPos[FieldOption]); err != nil {
		return nil, err
	}
	if c.Changes, err = s.changes(ctx, tx, "true order by c.updated_at desc limit 50"); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) changes(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]ChangeRef, error) {
	rows, err := tx.Query(ctx, `select c.id::text, c.number, c.aliases, c.title, c.request_element_id::text, rs.key || '-' || re.number, re.title, ro.name, c.request_source
		from changes c
		left join elements re on re.tenant_id = c.tenant_id and re.id = c.request_element_id and re.deleted_at is null
		left join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		left join options ro on ro.tenant_id = re.tenant_id and ro.id = re.state_id where `+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ChangeRef])
}

// EnvInput modifica un ambiente.
type EnvInput struct {
	Production *bool `json:"production"`
	Ordinal    *int  `json:"ordinal"`
}

func (s *Service) UpdateEnvironment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in EnvInput) error {
	if !CanConfigure(actor) {
		return engine.ErrForbidden
	}
	if in.Ordinal != nil && (*in.Ordinal < 0 || *in.Ordinal > 1000) {
		return engine.OutOfRange("ordinal", 0, 1000, "ordine fuori scala (0-1000)")
	}
	tag, err := tx.Exec(ctx, "update environments set is_production = coalesce($2, is_production), ordinal = coalesce($3, ordinal) where id::text = $1",
		id, in.Production, in.Ordinal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	// Cambia cosa è "in produzione": si riallinea lo stato delle vulnerabilità.
	rows, err := tx.Query(ctx, "select distinct service_id::text from deployments where environment_id::text = $1 and service_id is not null", id)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, sid := range ids {
		if err := s.refreshFindings(ctx, tx, sid); err != nil {
			return err
		}
	}
	return auditEvent(ctx, tx, actor, "devsecops.environment.update", "environment", id, map[string]any{"production": in.Production, "ordinal": in.Ordinal})
}

// ServiceInput associa un servizio a un'applicazione (vuoto = nessuna).
type ServiceInput struct {
	Application *string `json:"application"` // codice, chiave o id; "" toglie l'associazione
}

func (s *Service) UpdateService(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ServiceInput) (*ServiceView, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	var cur *string
	if err := tx.QueryRow(ctx, "select application_element_id::text from services where id::text = $1 for update", id).Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if in.Application != nil {
		app := ""
		if ref := strings.TrimSpace(*in.Application); ref != "" {
			var err error
			if app, err = appByRef(ctx, tx, ref); err != nil {
				return nil, err
			}
			if app == "" {
				return nil, engine.NotFound("application", "application", "applicazione inesistente: %s", ref)
			}
		}
		if app != deref(cur) {
			var src any = "manual"
			if app == "" {
				src = nil
			}
			if _, err := tx.Exec(ctx, "update services set application_element_id = $2, mapping_source = $3 where id::text = $1", id, nullable(app), src); err != nil {
				return nil, err
			}
			if err := s.remap(ctx, tx, actor, id, deref(cur), app, "manual"); err != nil {
				return nil, err
			}
		}
	}
	return s.serviceView(ctx, tx, id)
}

// ChangeInput collega un change a una richiesta o gli dà il numero definitivo (il numero
// precedente resta come alias).
type ChangeInput struct {
	Request *string `json:"request"` // chiave della richiesta (DEM-12); "" scollega
	Number  *string `json:"number"`
	Title   *string `json:"title"`
}

func (s *Service) UpdateChange(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in ChangeInput) (*ChangeRef, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if err := lock(ctx, tx, "dso/change"); err != nil {
		return nil, err
	}
	var number string
	var aliases []string
	if err := tx.QueryRow(ctx, "select number, aliases from changes where id::text = $1 for update", id).Scan(&number, &aliases); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if in.Number != nil {
		n := normChange(*in.Number)
		if n == "" {
			return nil, engine.Required("number", "serve il numero del change")
		}
		if n != number {
			var clash bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from changes where id::text <> $1 and (number = $2 or $2 = any(aliases)))", id, n).Scan(&clash); err != nil {
				return nil, err
			}
			if clash {
				return nil, engine.InvalidParams("validation.devsecops.change_number_taken", "number", map[string]any{"number": n}, "il numero %s è già di un altro change", n)
			}
			aliases = slices.DeleteFunc(append(aliases, number), func(a string) bool { return a == n })
			if _, err := tx.Exec(ctx, "update changes set number = $2, aliases = $3, updated_at = now() where id::text = $1", id, n, aliases); err != nil {
				return nil, err
			}
			if err := auditEvent(ctx, tx, actor, "devsecops.change.renumber", "change", id, map[string]any{"number": n, "previous": number}); err != nil {
				return nil, err
			}
		}
	}
	if in.Title != nil {
		if _, err := tx.Exec(ctx, "update changes set title = $2, updated_at = now() where id::text = $1", id, clip(*in.Title, 300)); err != nil {
			return nil, err
		}
	}
	if in.Request != nil {
		req := ""
		if ref := strings.TrimSpace(*in.Request); ref != "" {
			rid, _, _, typ, err := elementRef(ctx, tx, ref)
			if err != nil || typ != "business_request" {
				return nil, engine.NotFound("request", "request", "richiesta inesistente: %s", ref)
			}
			req = rid
		}
		var src any = "manual"
		if req == "" {
			src = nil
		}
		if _, err := tx.Exec(ctx, "update changes set request_element_id = $2, request_source = $3, updated_at = now() where id::text = $1", id, nullable(req), src); err != nil {
			return nil, err
		}
		if err := auditEvent(ctx, tx, actor, "devsecops.change.link", "change", id, map[string]any{"request": req, "by": "manual"}); err != nil {
			return nil, err
		}
	}
	list, err := s.changes(ctx, tx, "c.id::text = $1", id)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// CreatePlannedService censisce dall'interfaccia un servizio non ancora sulla pipeline.
func (s *Service) CreatePlannedService(ctx context.Context, tx pgx.Tx, actor engine.Actor, in contract.Service) (*ServiceView, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if strings.TrimSpace(in.Application) == "" {
		return nil, engine.Required("application", "indica l'applicazione del servizio")
	}
	app, err := appByRef(ctx, tx, in.Application)
	if err != nil {
		return nil, err
	}
	if app == "" {
		return nil, engine.NotFound("application", "application", "applicazione inesistente: %s", in.Application)
	}
	return s.RegisterService(ctx, tx, actor, in, "manual")
}
