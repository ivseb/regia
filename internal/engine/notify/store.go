package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// Item è una notifica da mostrare, con chiave e titolo attuali dell'elemento.
type Item struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	ElementID  *string        `json:"element_id"`
	ElementKey string         `json:"element_key"`
	Title      string         `json:"title"`
	Type       string         `json:"type"`
	ActorName  string         `json:"actor_name"`
	ReadAt     *time.Time     `json:"read_at"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Page è una pagina di notifiche, dalla più recente; Next è il cursore della successiva.
type Page struct {
	Items []Item `json:"items"`
	Next  string `json:"next,omitempty"`
}

// List restituisce le notifiche del membro. cursor = valore Next della pagina precedente.
func List(ctx context.Context, tx pgx.Tx, memberID string, unreadOnly bool, limit int, cursor string) (Page, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var beforeAt *time.Time
	var beforeID *string
	if cursor != "" {
		at, id, ok := strings.Cut(cursor, "_")
		t, err := time.Parse(time.RFC3339Nano, at)
		if !ok || err != nil {
			return Page{}, errors.New("cursore non valido")
		}
		beforeAt, beforeID = &t, &id
	}
	rows, err := tx.Query(ctx, `
		select n.id, n.kind, n.payload, n.element_id, coalesce(s.key || '-' || e.number, ''), coalesce(e.title, ''),
		       coalesce(t.key, ''), coalesce(a.display_name, ''), n.read_at, n.created_at
		from notifications n
		left join elements e on e.tenant_id = n.tenant_id and e.id = n.element_id
		left join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join members a on a.tenant_id = n.tenant_id and a.id = n.actor_id
		where n.member_id = $1
		  and ($2::timestamptz is null or (n.created_at, n.id) < ($2, $3::uuid))
		  and (not $4 or n.read_at is null)
		  and (e.id is null or e.deleted_at is null)
		order by n.created_at desc, n.id desc
		limit $5`, memberID, beforeAt, beforeID, unreadOnly, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	out := Page{Items: []Item{}}
	for rows.Next() {
		var it Item
		var payload []byte
		if err := rows.Scan(&it.ID, &it.Kind, &payload, &it.ElementID, &it.ElementKey, &it.Title, &it.Type, &it.ActorName, &it.ReadAt, &it.CreatedAt); err != nil {
			return Page{}, err
		}
		it.Payload = map[string]any{}
		_ = json.Unmarshal(payload, &it.Payload)
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	rows.Close()
	// Una notifica senza autore è del sistema: si chiama «Sistema» o «System» a seconda della
	// lingua di chi legge (la si chiede una volta sola, e solo se serve).
	lang := ""
	for i := range out.Items {
		if out.Items[i].ActorName != "" {
			continue
		}
		if lang == "" {
			var err error
			if lang, err = i18n.MemberLang(ctx, tx, memberID); err != nil {
				return Page{}, err
			}
		}
		out.Items[i].ActorName = i18n.T(lang, "notify.system")
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		out.Next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + last.ID
	}
	return out, nil
}

// Unread conta le notifiche non lette (fino a 100: oltre basta "99+").
func Unread(ctx context.Context, tx pgx.Tx, memberID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `select count(*) from (select 1 from notifications where member_id = $1 and read_at is null limit 100) x`, memberID).Scan(&n)
	return n, err
}

// MarkRead segna come letta una notifica del membro; false se non esiste o non è sua.
func MarkRead(ctx context.Context, tx pgx.Tx, memberID, id string) (bool, error) {
	tag, err := tx.Exec(ctx, `update notifications set read_at = coalesce(read_at, now()) where id::text = $1 and member_id = $2`, id, memberID)
	return tag.RowsAffected() > 0, err
}

// MarkAllRead segna come lette tutte le notifiche del membro; restituisce quante.
func MarkAllRead(ctx context.Context, tx pgx.Tx, memberID string) (int64, error) {
	tag, err := tx.Exec(ctx, `update notifications set read_at = now() where member_id = $1 and read_at is null`, memberID)
	return tag.RowsAffected(), err
}

// Prefs sono le preferenze di una persona: per ogni tipo, in app e per email.
type Prefs struct {
	Kinds          []KindPref `json:"kinds"`
	EmailAvailable bool       `json:"email_available"` // il server ha un SMTP configurato
}

type KindPref struct {
	Kind  string `json:"kind"`
	InApp bool   `json:"in_app"`
	Email bool   `json:"email"`
}

// GetPrefs restituisce le preferenze (di default tutto attivo).
func GetPrefs(ctx context.Context, tx pgx.Tx, memberID string) (Prefs, error) {
	var muted, emailMuted []string
	err := tx.QueryRow(ctx, `select muted_kinds, email_muted_kinds from notification_prefs where member_id = $1`, memberID).Scan(&muted, &emailMuted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Prefs{}, err
	}
	out := Prefs{EmailAvailable: EmailEnabled()}
	for _, k := range Kinds {
		out.Kinds = append(out.Kinds, KindPref{Kind: k, InApp: !contains(muted, k), Email: !contains(emailMuted, k)})
	}
	return out, nil
}

// SetPrefs salva le preferenze; i tipi non indicati restano attivi.
func SetPrefs(ctx context.Context, tx pgx.Tx, memberID string, kinds []KindPref) error {
	muted, emailMuted := []string{}, []string{}
	for _, k := range kinds {
		if !ValidKind(k.Kind) {
			return fmt.Errorf("%w: %q", ErrUnknownKind, k.Kind)
		}
		if !k.InApp {
			muted = append(muted, k.Kind)
		}
		if !k.Email {
			emailMuted = append(emailMuted, k.Kind)
		}
	}
	_, err := tx.Exec(ctx, `insert into notification_prefs (tenant_id, member_id, muted_kinds, email_muted_kinds)
		values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, member_id) do update set muted_kinds = excluded.muted_kinds,
			email_muted_kinds = excluded.email_muted_kinds, updated_at = now()`, memberID, muted, emailMuted)
	return err
}

// ErrUnknownKind: tipo di evento inesistente nelle preferenze.
var ErrUnknownKind = errors.New("tipo di notifica sconosciuto")

// WatchStatus dice se il membro segue l'elemento e perché.
type WatchStatus struct {
	Following bool `json:"following"`
	Auto      bool `json:"auto"`      // lo segue perché è autore o stakeholder
	Followers int  `json:"followers"` // quante persone lo seguono
}

// Watch fa seguire (follow = true) o smettere di seguire l'elemento. Smettere vale anche
// per chi lo segue in automatico: resta una riga con muted = true.
func Watch(ctx context.Context, tx pgx.Tx, elementID, memberID string, follow bool) error {
	_, err := tx.Exec(ctx, `insert into watchers (tenant_id, element_id, member_id, muted) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, element_id, member_id) do update set muted = excluded.muted`, elementID, memberID, !follow)
	return err
}

// Status restituisce lo stato di osservazione dell'elemento per il membro.
func Status(ctx context.Context, tx pgx.Tx, elementID, memberID string) (WatchStatus, error) {
	var st WatchStatus
	err := tx.QueryRow(ctx, `
		with f as (`+followersSQL+`)
		select exists (select 1 from f where member_id = $2),
		       exists (select 1 from elements e where e.id = $1 and e.reporter_id = $2)
		       or exists (select 1 from stakeholders sh where sh.element_id = $1
		                  and (sh.member_id = $2 or sh.group_id in (select member_groups($2)))),
		       (select count(*) from f)`, elementID, memberID).Scan(&st.Following, &st.Auto, &st.Followers)
	return st, err
}

func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

// Silence silenzia (on = true) o riattiva gli avvisi informativi di un elemento per il membro.
// Silenziando, gli avvisi informativi già arrivati e non letti si segnano come letti. Le
// notifiche che chiedono un'azione non si toccano mai.
func Silence(ctx context.Context, tx pgx.Tx, memberID, elementID string, on bool) error {
	if !on {
		_, err := tx.Exec(ctx, `delete from notification_silences where member_id = $1 and element_id = $2`, memberID, elementID)
		return err
	}
	if _, err := tx.Exec(ctx, `insert into notification_silences (tenant_id, member_id, element_id) values (app_tenant(), $1, $2)
		on conflict do nothing`, memberID, elementID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `update notifications set read_at = now()
		where member_id = $1 and element_id = $2 and read_at is null and kind = any($3::text[])`, memberID, elementID, Informational)
	return err
}

// Silenced elenca gli elementi silenziati dal membro.
func Silenced(ctx context.Context, tx pgx.Tx, memberID string) ([]string, error) {
	rows, err := tx.Query(ctx, `select element_id::text from notification_silences where member_id = $1`, memberID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if out == nil {
		out = []string{}
	}
	return out, err
}
