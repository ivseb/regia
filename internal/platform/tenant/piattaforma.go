package tenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
)

// Operazioni di piattaforma: riguardano l'istanza, non un'organizzazione. Usano il ruolo
// proprietario (nessun tenant attivo) e le chiama solo chi è superamministratore.

// ValidSlug dice se l'identificativo è ammesso (minuscole, cifre e trattini, 2-63 caratteri).
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// Summary è una riga dell'elenco delle organizzazioni dell'istanza.
type Summary struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Language     string    `json:"language"`
	BaseCurrency string    `json:"base_currency"`
	Members      int       `json:"members"`
	CreatedAt    time.Time `json:"created_at"`
	// Status: «active», «suspended» (nessuno entra, i dati restano) o «offboarding».
	Status string `json:"status"`
}

// List elenca tutte le organizzazioni dell'istanza con il numero di membri.
func List(ctx context.Context, d *db.DB) ([]Summary, error) {
	rows, err := d.Owner.Query(ctx, `select t.id, t.slug, t.name, t.base_currency, t.created_at,
		coalesce((select l.locale from tenant_language l where l.tenant_id = t.id), ''),
		(select count(*) from members m where m.tenant_id = t.id), t.status
		from tenants t order by t.created_at desc, t.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.BaseCurrency, &s.CreatedAt, &s.Language, &s.Members, &s.Status); err != nil {
			return nil, err
		}
		if s.Language == "" {
			s.Language = i18n.Default
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetPlatformAdmin dà o toglie il ruolo di superamministratore dell'istanza a un account esistente.
func SetPlatformAdmin(ctx context.Context, d *db.DB, email string, on bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	tag, err := d.Owner.Exec(ctx, "update accounts set is_platform_admin = $2 where lower(email) = $1", email, on)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("account %q inesistente", email)
	}
	return nil
}

// ErrNotFound: l'organizzazione non esiste.
var ErrNotFound = errors.New("organizzazione inesistente")

// Actor è chi compie un'operazione di piattaforma su un tenant: l'account del superamministratore
// e, se ne fa parte, il suo membro in quel tenant.
type Actor struct {
	AccountID string
	MemberID  string
}

// Rename cambia il nome di un'organizzazione (l'identificativo non cambia). Evento tenant.update
// nell'audit di quel tenant.
func Rename(ctx context.Context, d *db.DB, tenantID, name string, by Actor) error {
	return updateTenant(ctx, d, tenantID, by, "tenant.update", func(tx pgx.Tx, old map[string]any) (map[string]any, error) {
		if _, err := tx.Exec(ctx, "update tenants set name = $2, updated_at = now() where id = $1", tenantID, name); err != nil {
			return nil, err
		}
		return map[string]any{"field": "name"}, nil
	})
}

// SetStatus sospende («suspended») o riattiva («active») un'organizzazione. Eventi tenant.suspend e
// tenant.reactivate nell'audit del tenant. Non cambia i dati: solo chi può entrare.
func SetStatus(ctx context.Context, d *db.DB, tenantID, status string, by Actor) error {
	action := "tenant.reactivate"
	if status == "suspended" {
		action = "tenant.suspend"
	} else if status != "active" {
		return fmt.Errorf("stato non valido %q", status)
	}
	return updateTenant(ctx, d, tenantID, by, action, func(tx pgx.Tx, old map[string]any) (map[string]any, error) {
		if _, err := tx.Exec(ctx, "update tenants set status = $2, updated_at = now() where id = $1", tenantID, status); err != nil {
			return nil, err
		}
		return map[string]any{"from": old["status"], "to": status}, nil
	})
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// updateTenant legge la riga con il ruolo proprietario, applica la modifica e scrive l'audit
// nella stessa transazione, legata al tenant (RLS e catena di audit del tenant).
func updateTenant(ctx context.Context, d *db.DB, tenantID string, by Actor, action string,
	apply func(tx pgx.Tx, old map[string]any) (map[string]any, error)) error {
	if !uuidRe.MatchString(tenantID) {
		return ErrNotFound
	}
	return pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		var name, status string
		err := tx.QueryRow(ctx, "select name, status from tenants where id = $1 for update", tenantID).Scan(&name, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		payload, err := apply(tx, map[string]any{"name": name, "status": status})
		if err != nil {
			return err
		}
		if err := bindTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "tenant", EntityID: tenantID,
			ActorAccountID: by.AccountID, ActorMemberID: by.MemberID, Payload: payload})
	})
}
