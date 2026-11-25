// Package admin è l'amministrazione di persone e accessi di un tenant da interfaccia
// (Configurazione → Persone e accessi): membri, inviti, sospensioni, reimpostazione di
// password e MFA, ruoli personalizzati, gruppi, ruoli sugli oggetti e politica di
// sicurezza. Serve tenant.admin (lo controlla l'API).
//
// Regole comuni:
//   - tutto gira in db.InTenant, sotto RLS, e ogni modifica scrive nell'audit nella
//     stessa transazione (solo identificativi, mai dati personali in chiaro);
//   - l'ultimo amministratore non si può togliere: ogni modifica che può ridurre gli
//     amministratori ricontrolla, prima del commit, che ne resti almeno uno attivo;
//   - password e MFA sono dell'account, che può valere anche in altri tenant: un
//     amministratore di tenant li reimposta solo se l'account appartiene solo al suo tenant.
package admin

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Actor è l'amministratore che compie l'operazione.
type Actor struct {
	TenantID  string
	AccountID string
	MemberID  string
	SessionID string
	IP        netip.Addr
}

// Error è un errore da mostrare così com'è, con lo stato HTTP e un codice stabile.
type Error struct {
	Status int
	Code   string
	Field  string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func invalid(field, msg string) error {
	return &Error{Status: 422, Code: "validation", Field: field, Msg: msg}
}
func conflict(code, msg string) error { return &Error{Status: 409, Code: code, Msg: msg} }

var (
	ErrNotFound  = &Error{Status: 404, Code: "admin.not_found", Msg: "elemento inesistente"}
	ErrLastAdmin = &Error{Status: 409, Code: "admin.last_admin",
		Msg: "operazione non consentita: l'organizzazione resterebbe senza amministratori attivi"}
	ErrSharedAccount = &Error{Status: 409, Code: "admin.shared_account",
		Msg: "l'account vale anche in altre organizzazioni (o è un amministratore di piattaforma): password e MFA li può reimpostare solo un amministratore di piattaforma"}
	ErrSelf = &Error{Status: 409, Code: "admin.self", Msg: "non puoi sospendere te stesso"}
)

// Options configura il servizio.
type Options struct {
	// PasswordOff: modalità solo SSO (niente password per i nuovi account).
	PasswordOff func() bool
	InviteTTL   time.Duration // default 7 giorni
	ResetTTL    time.Duration // default 24 ore
}

// Service è l'amministrazione di persone e accessi.
type Service struct {
	db   *db.DB
	opts Options
}

func New(d *db.DB, o Options) *Service {
	if o.InviteTTL == 0 {
		o.InviteTTL = 7 * 24 * time.Hour
	}
	if o.ResetTTL == 0 {
		o.ResetTTL = 24 * time.Hour
	}
	if o.PasswordOff == nil {
		o.PasswordOff = func() bool { return false }
	}
	return &Service{db: d, opts: o}
}

func (s *Service) tx(ctx context.Context, a Actor, fn func(pgx.Tx) error) error {
	return s.db.InTenant(ctx, a.TenantID, db.TenantOpts{}, fn)
}

func (s *Service) read(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	return s.db.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, fn)
}

func (a Actor) audit(ctx context.Context, tx pgx.Tx, action, entityType, entityID string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: entityID, Payload: payload,
		ActorAccountID: a.AccountID, ActorMemberID: a.MemberID, IP: a.IP})
}

// adminHolderSQL: il membro m ha un ruolo di amministrazione (permesso * o tenant.admin),
// direttamente o tramite gruppo, a livello di tenant.
const adminHolderSQL = `exists (
	select 1 from role_assignments ra
	join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
	where ra.space_id is null and r.permissions && array['*', 'tenant.admin']
	  and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id))))`

// ensureAdmin si chiama prima del commit di ogni modifica che può ridurre gli
// amministratori: ne deve restare almeno uno attivo, con un account.
func ensureAdmin(ctx context.Context, tx pgx.Tx) error {
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from members m
		where m.status = 'active' and m.account_id is not null
		  and (m.access_until is null or m.access_until > now()) and `+adminHolderSQL).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// closeSessions chiude le sessioni dell'account aperte su questo tenant (o senza tenant
// attivo): quelle su altri tenant restano, lì l'appartenenza è ancora valida.
func closeSessions(ctx context.Context, tx pgx.Tx, accountID string, all bool) (int64, error) {
	q := "delete from sessions where account_id = $1 and (active_tenant_id = app_tenant() or active_tenant_id is null)"
	if all {
		q = "delete from sessions where account_id = $1"
	}
	tag, err := tx.Exec(ctx, q, accountID)
	return tag.RowsAffected(), err
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isFK(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

func cleanName(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
