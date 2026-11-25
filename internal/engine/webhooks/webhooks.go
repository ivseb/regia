// Package webhooks consegna gli eventi del tenant a sistemi esterni (piano, passo 15).
//
//   - Configurazione (tenant.admin): URL, eventi scelti, filtro opzionale nel linguaggio
//     delle condizioni sull'elemento, segreto per la firma (mostrato una volta, cifrato
//     a riposo con la chiave applicativa).
//   - Gli eventi nascono dalla storia degli elementi: un trigger su activities scrive in
//     outbox_events, nella stessa transazione della modifica (migrazione 0022).
//   - Il worker (deliver.go) trasforma gli eventi dell'outbox in consegne, una per webhook
//     interessato, e le invia con firma HMAC-SHA256, retry ad attesa crescente e stato
//     visibile. "Reinvia" crea una nuova consegna con lo stesso corpo e lo stesso id evento.
//   - Protezione SSRF in ssrf.go.
package webhooks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Version è la versione del formato del payload (campo "version"). Cambia solo con
// modifiche incompatibili; i campi nuovi si aggiungono senza cambiarla.
const Version = 1

// EventInfo descrive un evento a cui ci si può iscrivere.
type EventInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Events è il catalogo degli eventi (le chiavi sono anche nel trigger della migrazione 0022).
var Events = []EventInfo{
	{"element.created", "Creazione", "Un elemento (richiesta, impatto, valutazione...) è stato creato."},
	{"element.phase_changed", "Cambio di fase", "Un elemento è passato a un'altra fase del processo."},
	{"comment.created", "Commento", "Un nuovo commento su un elemento."},
	{"attachment.added", "Allegato", "Un file allegato a un elemento."},
	{"approval.decided", "Approvazione decisa", "Una persona ha deciso un passo di approvazione."},
	{"review.completed", "Valutazione conclusa", "Un team valutatore ha dato l'esito di una valutazione (evento sulla richiesta)."},
	{"governance.decision", "Decisione di un organo", "Un organo di governance ha deciso su un elemento in seduta."},
}

// PingEvent è l'evento di prova inviato dal pulsante "Invia una prova".
const PingEvent = "ping"

func knownEvent(k string) bool {
	return slices.ContainsFunc(Events, func(e EventInfo) bool { return e.Key == k })
}

// Error è un errore da mostrare così com'è.
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

var ErrNotFound = &Error{Status: 404, Code: "webhook.not_found", Msg: "webhook inesistente"}

// Actor è l'amministratore che configura.
type Actor struct {
	TenantID  string
	AccountID string
	MemberID  string
	IP        netip.Addr
}

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Eng *engine.Service
	Box *secretbox.Box
	// AllowPrivate toglie la protezione SSRF (modalità sviluppo o REGIA_WEBHOOK_ALLOW_PRIVATE).
	AllowPrivate bool
	// BaseURL: indirizzo pubblico dell'app, per il link all'elemento nel payload.
	BaseURL string
	Log     *slog.Logger
	// Timeout di una consegna (default 10 secondi).
	Timeout time.Duration
	// Client sostituisce il client HTTP (test); nil = client con protezione SSRF.
	Client *http.Client
}

// Service configura e consegna i webhook.
type Service struct {
	o      Options
	client *http.Client
}

func New(o Options) *Service {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Box == nil {
		o.Box = secretbox.Random()
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	c := o.Client
	if c == nil {
		c = newClient(o.AllowPrivate, o.Timeout)
	}
	return &Service{o: o, client: c}
}

// AllowPrivate dice se la protezione SSRF è disattivata.
func (s *Service) AllowPrivate() bool { return s.o.AllowPrivate }

// Webhook è un webhook come appare nella configurazione (mai il segreto).
type Webhook struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	URL        string     `json:"url"`
	Events     []string   `json:"events"`
	Filter     string     `json:"filter"`
	Active     bool       `json:"active"`
	SecretHint string     `json:"secret_hint"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastStatus *string    `json:"last_status"`
	LastAt     *time.Time `json:"last_delivery_at"`
	Failing    int        `json:"failing"` // consegne fallite definitivamente nelle ultime 24 ore
	Pending    int        `json:"pending"`
}

// Input crea o modifica un webhook.
type Input struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Filter string   `json:"filter"`
	Active *bool    `json:"active"`
}

func (s *Service) clean(in *Input) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return invalid("name", "serve un nome (al massimo 80 caratteri)")
	}
	u, err := checkURL(in.URL, s.o.AllowPrivate)
	if err != nil {
		return err
	}
	in.URL = u
	evs := []string{}
	for _, e := range in.Events {
		if !knownEvent(e) {
			return invalid("events", "evento sconosciuto: "+e)
		}
		if !slices.Contains(evs, e) {
			evs = append(evs, e)
		}
	}
	if len(evs) == 0 {
		return invalid("events", "scegli almeno un evento")
	}
	in.Events = evs
	in.Filter = strings.TrimSpace(in.Filter)
	if len(in.Filter) > 2000 {
		return invalid("filter", "filtro troppo lungo")
	}
	if in.Filter != "" {
		if _, err := engine.ParseCondition(in.Filter); err != nil {
			return invalid("filter", "filtro non valido: "+err.Error())
		}
	}
	return nil
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b)
}

func secretContext(id string) string { return "webhook:" + id }

func audited(ctx context.Context, tx pgx.Tx, a Actor, action, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "webhook", EntityID: id, Payload: payload,
		ActorAccountID: a.AccountID, ActorMemberID: a.MemberID, IP: a.IP})
}

// List elenca i webhook del tenant con lo stato delle ultime consegne.
func (s *Service) List(ctx context.Context, tenantID string) ([]Webhook, error) {
	out := []Webhook{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select w.id, w.name, w.url, w.events, w.filter, w.active, w.secret_hint, w.created_at, w.updated_at,
			  l.status, l.at,
			  (select count(*) from webhook_deliveries d where d.webhook_id = w.id and d.status = 'failed' and d.created_at > now() - interval '24 hours'),
			  (select count(*) from webhook_deliveries d where d.webhook_id = w.id and d.status = 'pending')
			from webhooks w
			left join lateral (select d.status, coalesce(d.last_attempt_at, d.created_at) as at from webhook_deliveries d
			                   where d.webhook_id = w.id order by d.created_at desc limit 1) l on true
			order by lower(w.name), w.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w Webhook
			if err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.Events, &w.Filter, &w.Active, &w.SecretHint, &w.CreatedAt, &w.UpdatedAt,
				&w.LastStatus, &w.LastAt, &w.Failing, &w.Pending); err != nil {
				return err
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, err
}

// Create crea un webhook e restituisce il segreto (mostrato una volta sola).
func (s *Service) Create(ctx context.Context, a Actor, in Input) (string, string, error) {
	if err := s.clean(&in); err != nil {
		return "", "", err
	}
	id := db.NewID()
	secret := newSecret()
	sealed, err := s.o.Box.Seal([]byte(secret), secretContext(id))
	if err != nil {
		return "", "", err
	}
	active := in.Active == nil || *in.Active
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "select count(*) from webhooks").Scan(&n); err != nil {
			return err
		}
		if n >= 50 {
			return invalid("name", "al massimo 50 webhook per organizzazione")
		}
		if _, err := tx.Exec(ctx, `insert into webhooks (tenant_id, id, name, url, secret_enc, secret_hint, events, filter, active, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, in.Name, in.URL, sealed, secret[:10], in.Events, in.Filter, active, nullable(a.MemberID)); err != nil {
			return err
		}
		return audited(ctx, tx, a, "webhook.create", id, map[string]any{"events": in.Events, "active": active, "filter": in.Filter != ""})
	})
	if err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// Update modifica un webhook (il segreto resta).
func (s *Service) Update(ctx context.Context, a Actor, id string, in Input) error {
	if !validID(id) {
		return ErrNotFound
	}
	if err := s.clean(&in); err != nil {
		return err
	}
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		q := `update webhooks set name = $2, url = $3, events = $4, filter = $5, updated_at = now()`
		args := []any{id, in.Name, in.URL, in.Events, in.Filter}
		if in.Active != nil {
			q += ", active = $6"
			args = append(args, *in.Active)
		}
		tag, err := tx.Exec(ctx, q+" where id = $1", args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "webhook.update", id, map[string]any{"events": in.Events, "active": in.Active, "filter": in.Filter != ""})
	})
}

// Delete cancella un webhook e le sue consegne.
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "delete from webhooks where id = $1", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "webhook.delete", id, nil)
	})
}

// RotateSecret genera un nuovo segreto; il vecchio smette di valere subito.
func (s *Service) RotateSecret(ctx context.Context, a Actor, id string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	secret := newSecret()
	sealed, err := s.o.Box.Seal([]byte(secret), secretContext(id))
	if err != nil {
		return "", err
	}
	err = s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "update webhooks set secret_enc = $2, secret_hint = $3, updated_at = now() where id = $1", id, sealed, secret[:10])
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audited(ctx, tx, a, "webhook.secret_rotate", id, nil)
	})
	return secret, err
}

// Delivery è una consegna, per lo stato visibile nella configurazione.
type Delivery struct {
	ID             string     `json:"id"`
	EventID        string     `json:"event_id"`
	Event          string     `json:"event"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	ResponseStatus *int       `json:"response_status"`
	LastError      *string    `json:"last_error"`
	DurationMS     *int       `json:"duration_ms"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	LastAttemptAt  *time.Time `json:"last_attempt_at"`
	CreatedAt      time.Time  `json:"created_at"`
	RedeliveryOf   *string    `json:"redelivery_of"`
	Body           string     `json:"body,omitempty"`
}

// Deliveries elenca le ultime consegne di un webhook (al massimo 100).
func (s *Service) Deliveries(ctx context.Context, tenantID, id string) ([]Delivery, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	out := []Delivery{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from webhooks where id = $1)", id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `select id, event_id, event, status, attempts, response_status, last_error, duration_ms,
			case when status = 'pending' then next_attempt_at end, last_attempt_at, created_at, redelivery_of, body
			from webhook_deliveries where webhook_id = $1 order by created_at desc limit 100`, id)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Delivery])
		return err
	})
	if out == nil {
		out = []Delivery{}
	}
	return out, err
}

// Redeliver crea una nuova consegna con lo stesso corpo (stesso id evento) di una precedente.
func (s *Service) Redeliver(ctx context.Context, a Actor, deliveryID string) (string, error) {
	if !validID(deliveryID) {
		return "", ErrNotFound
	}
	id := db.NewID()
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var webhookID string
		err := tx.QueryRow(ctx, `insert into webhook_deliveries (tenant_id, id, webhook_id, event_id, event, body, redelivery_of)
			select tenant_id, $1, webhook_id, event_id, event, body, id from webhook_deliveries where id = $2
			returning webhook_id`, id, deliveryID).Scan(&webhookID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &Error{Status: 404, Code: "webhook.delivery_not_found", Msg: "consegna inesistente"}
		}
		if err != nil {
			return err
		}
		return audited(ctx, tx, a, "webhook.redeliver", webhookID, map[string]any{"delivery": deliveryID, "new_delivery": id})
	})
	return id, err
}

// Ping accoda un evento di prova.
func (s *Service) Ping(ctx context.Context, a Actor, id string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	deliveryID := db.NewID()
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from webhooks where id = $1)", id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		ev := s.envelope(ctx, tx, PingEvent, time.Now(), nil, nil, map[string]any{"webhook_id": id, "message": "Evento di prova da Regia"})
		body, err := ev.marshal()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into webhook_deliveries (tenant_id, id, webhook_id, event_id, event, body)
			values (app_tenant(), $1, $2, $3, $4, $5)`, deliveryID, id, ev.ID, PingEvent, body); err != nil {
			return err
		}
		return audited(ctx, tx, a, "webhook.ping", id, nil)
	})
	return deliveryID, err
}

func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isFK(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}
