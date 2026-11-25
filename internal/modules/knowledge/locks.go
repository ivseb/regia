package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Blocco di modifica leggero (page_locks): chi apre l'editor prende il blocco per LockTTL e
// lo rinnova finché scrive; gli altri vedono «X sta modificando» e possono subentrare
// esplicitamente (il subentro resta nell'audit). Un blocco scaduto non conta più.

// Lock è il blocco attivo su una pagina.
type Lock struct {
	MemberID   string    `json:"member_id"`
	Name       string    `json:"name"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Mine       bool      `json:"mine"`
}

// activeLock restituisce il blocco non scaduto della pagina (nil se libera).
func activeLock(ctx context.Context, tx pgx.Tx, pageID, memberID string) (*Lock, error) {
	var l Lock
	err := tx.QueryRow(ctx, `select l.member_id::text, coalesce(m.display_name, ''), l.acquired_at, l.expires_at
		from page_locks l left join members m on m.tenant_id = l.tenant_id and m.id = l.member_id
		where l.element_id = $1 and l.expires_at > now()`, pageID).Scan(&l.MemberID, &l.Name, &l.AcquiredAt, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.Mine = l.MemberID == memberID
	return &l, nil
}

// LockResult: il blocco dopo la richiesta. Taken = false se lo tiene un'altra persona.
type LockResult struct {
	Lock  *Lock `json:"lock"`
	Taken bool  `json:"taken"`
}

// AcquireLock prende o rinnova il blocco. Con force subentra a chi lo tiene.
func (s *Service) AcquireLock(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, force bool) (*LockResult, error) {
	el, sp, _, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write || actor.MemberID == "" {
		return nil, engine.ErrForbidden
	}
	cur, err := activeLock(ctx, tx, el.ID, actor.MemberID)
	if err != nil {
		return nil, err
	}
	if cur != nil && !cur.Mine && !force {
		return &LockResult{Lock: cur}, nil
	}
	if _, err := tx.Exec(ctx, `insert into page_locks (tenant_id, element_id, member_id, expires_at, acquired_at)
		values (app_tenant(), $1, $2, now() + make_interval(secs => $3), now())
		on conflict (tenant_id, element_id) do update set member_id = excluded.member_id, expires_at = excluded.expires_at,
			acquired_at = case when page_locks.member_id = excluded.member_id and page_locks.expires_at > now()
			                   then page_locks.acquired_at else now() end`,
		el.ID, actor.MemberID, LockTTL.Seconds()); err != nil {
		return nil, err
	}
	if cur != nil && !cur.Mine {
		if err := audit.Append(ctx, tx, audit.Event{Action: "page.lock.takeover", EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"previous_member_id": cur.MemberID}}); err != nil {
			return nil, err
		}
	}
	l, err := activeLock(ctx, tx, el.ID, actor.MemberID)
	return &LockResult{Lock: l, Taken: true}, err
}

// ReleaseLock lascia il blocco (solo il proprio).
func (s *Service) ReleaseLock(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) error {
	el, _, _, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `delete from page_locks where element_id = $1 and member_id::text = $2`, el.ID, actor.MemberID)
	return err
}
