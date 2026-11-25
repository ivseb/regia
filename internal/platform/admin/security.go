package admin

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// ---------------------------------------------------------------------
// Ruoli sugli oggetti (stakeholder_roles): sponsor, PMO, demand manager... I processi li
// citano per chiave (passaggi, regole, approvazioni): la chiave non cambia, il nome sì.
// ---------------------------------------------------------------------

var stakeholderKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// StakeholderRole è un ruolo sugli oggetti con i suoi utilizzi.
type StakeholderRole struct {
	ID        string   `json:"id"`
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Elements  int      `json:"elements"`
	Processes []string `json:"processes"`
}

// ListStakeholderRoles elenca i ruoli sugli oggetti.
func (s *Service) ListStakeholderRoles(ctx context.Context, tenantID string) ([]StakeholderRole, error) {
	out := []StakeholderRole{}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select r.id, r.key, r.name,
			  (select count(distinct sh.element_id) from stakeholders sh where sh.role_id = r.id),
			  coalesce((select jsonb_agg(distinct p.name) from process_definitions p
			            join process_versions v on v.tenant_id = p.tenant_id and v.process_id = p.id and v.status in ('published', 'draft')
			            where not p.archived and exists (select 1 from jsonb_path_query(v.definition, 'lax $.**') x
			                                             where jsonb_typeof(x) = 'string' and x #>> '{}' = r.key)), '[]')
			from stakeholder_roles r order by lower(r.name)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r StakeholderRole
			var procs []byte
			if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.Elements, &procs); err != nil {
				return err
			}
			_ = json.Unmarshal(procs, &r.Processes)
			if r.Processes == nil {
				r.Processes = []string{}
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// CreateStakeholderRole crea un ruolo sugli oggetti.
func (s *Service) CreateStakeholderRole(ctx context.Context, a Actor, key, name string) (string, error) {
	name = cleanName(name, 80)
	if !stakeholderKeyRe.MatchString(key) {
		return "", invalid("key", "chiave non valida: minuscole, cifre e trattini bassi, iniziale minuscola")
	}
	if name == "" {
		return "", invalid("name", "serve un nome")
	}
	id := db.NewID()
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3)", id, key, name); err != nil {
			if isUnique(err) {
				return invalid("key", "esiste già un ruolo con questa chiave")
			}
			return err
		}
		return a.audit(ctx, tx, "stakeholder_role.create", "stakeholder_role", id, map[string]any{"key": key})
	})
	return id, err
}

// RenameStakeholderRole cambia il nome (la chiave resta: i processi la usano).
func (s *Service) RenameStakeholderRole(ctx context.Context, a Actor, id, name string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	name = cleanName(name, 80)
	if name == "" {
		return invalid("name", "serve un nome")
	}
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update stakeholder_roles set name = $2 where id = $1", id, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return a.audit(ctx, tx, "stakeholder_role.rename", "stakeholder_role", id, nil)
	})
}

// ---------------------------------------------------------------------
// Politica di sicurezza del tenant
// ---------------------------------------------------------------------

// Security è la politica del tenant.
type Security struct {
	MFAPolicy string     `json:"mfa_policy"` // off | admins | all
	UpdatedAt *time.Time `json:"updated_at"`
	// GroupInheritance: i ruoli di un gruppo valgono anche per i membri dei sottogruppi.
	GroupInheritance bool `json:"group_inheritance"`
	// Quanti membri attivi con password non hanno ancora l'MFA (tra quelli a cui si applica).
	PendingMFA int `json:"pending_mfa"`
}

func (s *Service) GetSecurity(ctx context.Context, tenantID string) (*Security, error) {
	out := &Security{MFAPolicy: "off", GroupInheritance: true}
	err := s.read(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "select mfa_policy, updated_at, group_inheritance from tenant_security").Scan(&out.MFAPolicy, &out.UpdatedAt, &out.GroupInheritance)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from members m join accounts a on a.id = m.account_id
			where m.status = 'active' and a.password_hash is not null and a.mfa_enabled_at is null
			  and ($1 = 'all' or ($1 = 'admins' and `+adminHolderSQL+`))`, out.MFAPolicy).Scan(&out.PendingMFA)
	})
	return out, err
}

// SetSecurity cambia la politica MFA. Rendendola più severa si chiudono le sessioni con
// password senza secondo fattore delle persone a cui ora si applica (tranne quella di chi
// fa la modifica): al prossimo accesso attivano o usano l'MFA.
func (s *Service) SetSecurity(ctx context.Context, a Actor, policy string) (int64, error) {
	if policy != "off" && policy != "admins" && policy != "all" {
		return 0, invalid("mfa_policy", "politica non valida: off, admins o all")
	}
	var closed int64
	err := s.tx(ctx, a, func(tx pgx.Tx) error {
		before := "off"
		if err := tx.QueryRow(ctx, "select mfa_policy from tenant_security for update").Scan(&before); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if before == policy {
			return nil
		}
		if _, err := tx.Exec(ctx, `insert into tenant_security (tenant_id, mfa_policy, updated_at, updated_by) values (app_tenant(), $1, now(), $2)
			on conflict (tenant_id) do update set mfa_policy = excluded.mfa_policy, updated_at = now(), updated_by = excluded.updated_by`,
			policy, nullable(a.MemberID)); err != nil {
			return err
		}
		if policy != "off" {
			tag, err := tx.Exec(ctx, `delete from sessions s using members m
				where m.account_id = s.account_id and m.status = 'active'
				  and s.auth_method = 'password' and not s.mfa and s.id is distinct from $2::uuid
				  and ($1 = 'all' or `+adminHolderSQL+`)`, policy, nullable(a.SessionID))
			if err != nil {
				return err
			}
			closed = tag.RowsAffected()
		}
		return a.audit(ctx, tx, "tenant.security.update", "tenant", a.TenantID, map[string]any{"mfa_policy": policy, "from": before, "sessions_closed": closed})
	})
	return closed, err
}

// SetGroupInheritance attiva o spegne la propagazione dei ruoli lungo la gerarchia dei gruppi.
// I permessi si leggono a ogni richiesta, quindi l'effetto è immediato.
func (s *Service) SetGroupInheritance(ctx context.Context, a Actor, on bool) error {
	return s.tx(ctx, a, func(tx pgx.Tx) error {
		before := true
		if err := tx.QueryRow(ctx, "select group_inheritance from tenant_security for update").Scan(&before); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if before == on {
			return nil
		}
		if _, err := tx.Exec(ctx, `insert into tenant_security (tenant_id, group_inheritance, updated_at, updated_by) values (app_tenant(), $1, now(), $2)
			on conflict (tenant_id) do update set group_inheritance = excluded.group_inheritance, updated_at = now(), updated_by = excluded.updated_by`,
			on, nullable(a.MemberID)); err != nil {
			return err
		}
		if !on {
			// Spegnendola un amministratore potrebbe restare senza ruolo: ne deve restare uno.
			if err := ensureAdmin(ctx, tx); err != nil {
				return err
			}
		}
		return a.audit(ctx, tx, "tenant.security.update", "tenant", a.TenantID, map[string]any{"group_inheritance": on, "from": before})
	})
}
