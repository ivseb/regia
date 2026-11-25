// Package tenant crea i tenant con la loro configurazione iniziale.
// Usa il ruolo di piattaforma: è un'operazione di amministrazione, non di un membro.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type CreateInput struct {
	Slug       string
	Name       string
	AdminEmail string
	AdminName  string
	// AdminPassword serve solo se l'account non esiste ancora.
	AdminPassword string
	BaseCurrency  string
	// Language è la lingua del tenant: i nomi dei dati di partenza (tipi, ruoli, spazi, campi,
	// processi…) nascono in quella lingua e diventa la predefinita del tenant (tenant_language).
	// Vuoto = italiano, senza una scelta registrata.
	Language string
	// Provision sono le configurazioni iniziali dei moduli, eseguite nella stessa
	// transazione (es. spazio e ciclo di vita del demand).
	Provision []Provisioner
}

// Provisioner configura un modulo in un tenant appena creato; tx è già legata al tenant.
type Provisioner func(ctx context.Context, tx pgx.Tx, adminMemberID string) error

type Created struct {
	TenantID       string
	AdminAccountID string
	AdminMemberID  string
	NewAccount     bool
}

func init() {
	i18n.Register(map[string]i18n.Msg{"tenant.all_members": {It: "Tutti i membri", En: "All members"}})
}

var ErrSlugTaken = errors.New("esiste già un tenant con questo identificativo")

func Create(ctx context.Context, d *db.DB, in CreateInput) (Created, error) {
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.AdminEmail = strings.ToLower(strings.TrimSpace(in.AdminEmail))
	if !slugRe.MatchString(in.Slug) {
		return Created{}, fmt.Errorf("identificativo non valido %q: minuscole, cifre e trattini, 2-63 caratteri", in.Slug)
	}
	if strings.TrimSpace(in.Name) == "" || in.AdminEmail == "" || !strings.Contains(in.AdminEmail, "@") {
		return Created{}, errors.New("servono nome del tenant ed email dell'amministratore")
	}
	if in.AdminName == "" {
		in.AdminName = in.AdminEmail
	}
	if in.BaseCurrency == "" {
		in.BaseCurrency = "EUR"
	}
	if in.Language != "" && !i18n.OK(in.Language) {
		return Created{}, fmt.Errorf("lingua non supportata %q", in.Language)
	}
	lang := i18n.Norm(in.Language)
	out := Created{TenantID: db.NewID(), AdminMemberID: db.NewID()}

	err := pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "insert into tenants (id, slug, name, base_currency) values ($1, $2, $3, $4)",
			out.TenantID, in.Slug, in.Name, in.BaseCurrency); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrSlugTaken
			}
			return err
		}
		// Da qui in poi la transazione è legata al nuovo tenant e soggetta alla RLS (audit compreso).
		if err := bindTenant(ctx, tx, out.TenantID); err != nil {
			return err
		}

		roleIDs := map[string]string{}
		for _, r := range rbac.SystemRoles {
			id := db.NewID()
			roleIDs[r.Key] = id
			if _, err := tx.Exec(ctx, "insert into roles (tenant_id, id, key, name, permissions) values ($1, $2, $3, $4, $5)",
				out.TenantID, id, r.Key, i18n.Name(lang, r.Name), r.Permissions); err != nil {
				return err
			}
		}
		allMembers := db.NewID()
		if _, err := tx.Exec(ctx, "insert into groups (tenant_id, id, name, system_kind) values ($1, $2, $3, 'all_members')",
			out.TenantID, allMembers, i18n.T(lang, "tenant.all_members")); err != nil {
			return err
		}
		if in.Language != "" {
			if _, err := tx.Exec(ctx, "insert into tenant_language (tenant_id, locale) values ($1, $2)", out.TenantID, lang); err != nil {
				return err
			}
		}

		// Account: si riusa se esiste (un consulente può stare su più tenant).
		err := tx.QueryRow(ctx, "select id from accounts where lower(email) = $1", in.AdminEmail).Scan(&out.AdminAccountID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if in.AdminPassword == "" {
				return errors.New("l'account non esiste: serve una password iniziale")
			}
			hash, err := auth.HashPassword(in.AdminPassword)
			if err != nil {
				return err
			}
			out.AdminAccountID, out.NewAccount = db.NewID(), true
			if _, err := tx.Exec(ctx, "insert into accounts (id, email, password_hash, display_name) values ($1, $2, $3, $4)",
				out.AdminAccountID, in.AdminEmail, hash, in.AdminName); err != nil {
				return err
			}
		case err != nil:
			return err
		}

		if _, err := tx.Exec(ctx, "insert into members (tenant_id, id, account_id, display_name, email) values ($1, $2, $3, $4, $5)",
			out.TenantID, out.AdminMemberID, out.AdminAccountID, in.AdminName, in.AdminEmail); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into group_members (tenant_id, group_id, member_id) values ($1, $2, $3)",
			out.TenantID, allMembers, out.AdminMemberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "insert into role_assignments (tenant_id, id, role_id, member_id) values ($1, $2, $3, $4)",
			out.TenantID, db.NewID(), roleIDs["tenant_admin"], out.AdminMemberID); err != nil {
			return err
		}
		for _, p := range in.Provision {
			if err := p(ctx, tx, out.AdminMemberID); err != nil {
				return fmt.Errorf("configurazione iniziale: %w", err)
			}
		}
		if err := Localize(ctx, tx, lang); err != nil {
			return fmt.Errorf("lingua dei dati di partenza: %w", err)
		}
		return audit.Append(ctx, tx, audit.Event{
			Action: "tenant.create", EntityType: "tenant", EntityID: out.TenantID,
			ActorAccountID: out.AdminAccountID, ActorMemberID: out.AdminMemberID,
			Payload: map[string]any{"slug": in.Slug, "base_currency": in.BaseCurrency},
		})
	})
	return out, err
}

// MemberInput aggiunge una persona a un tenant (comando di amministrazione e test).
// Gli inviti via email con link arriveranno con il modulo di notifiche.
type MemberInput struct {
	TenantSlug string
	Email      string
	Name       string
	Password   string   // solo se l'account non esiste ancora
	MemberType string   // standard (default) | requester | guest | auditor
	Roles      []string // chiavi dei ruoli di tenant, es. editor, finance
	Groups     []string // nomi dei gruppi, es. "Demand management"
}

func AddMember(ctx context.Context, d *db.DB, in MemberInput) (memberID string, err error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Name == "" {
		in.Name = in.Email
	}
	if in.MemberType == "" {
		in.MemberType = "standard"
	}
	err = pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		var tenantID string
		if err := tx.QueryRow(ctx, "select id from tenants where slug = $1", in.TenantSlug).Scan(&tenantID); err != nil {
			return fmt.Errorf("tenant %q inesistente", in.TenantSlug)
		}
		if err := bindTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		var accountID string
		err := tx.QueryRow(ctx, "select id from accounts where lower(email) = $1", in.Email).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			hash, err := auth.HashPassword(in.Password)
			if err != nil {
				return err
			}
			accountID = db.NewID()
			if _, err := tx.Exec(ctx, "insert into accounts (id, email, password_hash, display_name) values ($1, $2, $3, $4)",
				accountID, in.Email, hash, in.Name); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		memberID = db.NewID()
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, account_id, display_name, email, member_type) values ($1, $2, $3, $4, $5, $6)`,
			tenantID, memberID, accountID, in.Name, in.Email, in.MemberType); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return errors.New("questa persona è già membro del tenant")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
			select $1, id, $2 from groups where system_kind = 'all_members' or name = any($3)`, tenantID, memberID, in.Groups); err != nil {
			return err
		}
		for _, r := range in.Roles {
			tag, err := tx.Exec(ctx, `insert into role_assignments (tenant_id, id, role_id, member_id)
				select $1, $2, id, $3 from roles where key = $4`, tenantID, db.NewID(), memberID, r)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("ruolo %q inesistente", r)
			}
		}
		return audit.Append(ctx, tx, audit.Event{Action: "member.add", EntityType: "member", EntityID: memberID,
			ActorAccountID: "", Payload: map[string]any{"roles": in.Roles, "groups": in.Groups, "member_type": in.MemberType}})
	})
	return memberID, err
}

// Provision applica le configurazioni iniziali dei moduli a un tenant esistente
// (i provisioner sono idempotenti). Serve dopo un aggiornamento che aggiunge un modulo.
func Provision(ctx context.Context, d *db.DB, slug string, ps []Provisioner) error {
	return pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		var tenantID, adminID string
		err := tx.QueryRow(ctx, `select t.id, ra.member_id from tenants t
			join roles r on r.tenant_id = t.id and r.key = 'tenant_admin'
			join role_assignments ra on ra.tenant_id = t.id and ra.role_id = r.id and ra.member_id is not null
			where t.slug = $1 order by ra.created_at limit 1`, slug).Scan(&tenantID, &adminID)
		if err != nil {
			return fmt.Errorf("tenant %q inesistente o senza amministratore", slug)
		}
		if err := bindTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		// I ruoli di sistema aggiunti dopo la creazione del tenant (es. architetto,
		// amministratore di processo); quelli esistenti non si toccano: il tenant può averli modificati.
		lang, err := i18n.TenantLang(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range rbac.SystemRoles {
			if _, err := tx.Exec(ctx, `insert into roles (tenant_id, id, key, name, permissions) values (app_tenant(), $1, $2, $3, $4)
				on conflict (tenant_id, key) where key is not null do nothing`, db.NewID(), r.Key, i18n.Name(lang, r.Name), r.Permissions); err != nil {
				return err
			}
		}
		for _, p := range ps {
			if err := p(ctx, tx, adminID); err != nil {
				return err
			}
		}
		// Un tenant in inglese: i tipi, i campi e i processi aggiunti ora ricevono i nomi inglesi.
		return Localize(ctx, tx, lang)
	})
}

// bindTenant lega la transazione del ruolo di piattaforma a un tenant e la fa
// proseguire con il ruolo applicativo: il proprietario dello schema è superuser e
// scavalcherebbe la RLS, vedendo i dati di tutti i tenant. Da qui in poi valgono le
// stesse regole delle richieste HTTP.
func bindTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if _, err := tx.Exec(ctx, "select set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "set local role mytrack_app")
	return err
}
