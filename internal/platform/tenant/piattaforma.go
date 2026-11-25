package tenant

import (
	"context"
	"fmt"
	"strings"
	"time"

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
}

// List elenca tutte le organizzazioni dell'istanza con il numero di membri.
func List(ctx context.Context, d *db.DB) ([]Summary, error) {
	rows, err := d.Owner.Query(ctx, `select t.id, t.slug, t.name, t.base_currency, t.created_at,
		coalesce((select l.locale from tenant_language l where l.tenant_id = t.id), ''),
		(select count(*) from members m where m.tenant_id = t.id)
		from tenants t order by t.created_at desc, t.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.BaseCurrency, &s.CreatedAt, &s.Language, &s.Members); err != nil {
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
