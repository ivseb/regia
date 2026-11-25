package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Formato del payload, versione 1 (documentato in docs/sviluppo/accessi.md, "Webhook"). L'ordine
// dei campi è fisso: il corpo salvato è esattamente quello firmato e inviato.
type envelope struct {
	Version    int            `json:"version"`
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	Tenant     tenantRef      `json:"tenant"`
	Actor      *actorRef      `json:"actor"`
	Element    *elementRef    `json:"element"`
	Data       map[string]any `json:"data"`
}

type tenantRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type actorRef struct {
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
}

type elementRef struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Type          string `json:"type"`
	TypeName      string `json:"type_name"`
	Title         string `json:"title"`
	Phase         string `json:"phase"`
	PhaseCategory string `json:"phase_category"`
	URL           string `json:"url,omitempty"`
}

func (e envelope) marshal() (string, error) {
	b, err := json.Marshal(e)
	return string(b), err
}

func (s *Service) envelope(ctx context.Context, tx pgx.Tx, event string, at time.Time, actorID *string, el *engine.Element, data map[string]any) envelope {
	ev := envelope{Version: Version, ID: db.NewID(), Type: event, OccurredAt: at.UTC().Truncate(time.Millisecond), Data: data}
	if ev.Data == nil {
		ev.Data = map[string]any{}
	}
	_ = tx.QueryRow(ctx, "select id::text, slug, name from tenants where id = app_tenant()").Scan(&ev.Tenant.ID, &ev.Tenant.Slug, &ev.Tenant.Name)
	if actorID != nil && *actorID != "" {
		a := &actorRef{MemberID: *actorID}
		if tx.QueryRow(ctx, "select display_name from members where id = $1", *actorID).Scan(&a.Name) == nil {
			ev.Actor = a
		}
	}
	if el != nil {
		ev.Element = &elementRef{ID: el.ID, Key: el.Key, Type: el.TypeKey, TypeName: el.TypeName, Title: el.Title, Phase: el.State, PhaseCategory: el.StateCategory}
		if s.o.BaseURL != "" && el.Key != "" {
			ev.Element.URL = s.o.BaseURL + "/e/" + el.Key
		}
	}
	return ev
}

// Tempi delle consegne.
const (
	maxAttempts = 10
	claimLease  = 2 * time.Minute // una consegna interrotta (riavvio) si riprende dopo
	firstRetry  = 30 * time.Second
	maxRetry    = 6 * time.Hour
	batch       = 50
)

// Backoff è l'attesa prima del tentativo successivo al numero attempts (1 = primo fallito):
// 30 s, 1 min, 2 min, 4 min... fino a 6 ore.
func Backoff(attempts int) time.Duration {
	d := firstRetry << min(max(attempts-1, 0), 20)
	if d > maxRetry || d <= 0 {
		return maxRetry
	}
	return d
}

// Run elabora outbox e consegne a intervalli regolari, finché ctx non è chiuso.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.RunOnce(ctx); err != nil {
				s.o.Log.Warn("webhook", "errore", err)
			}
		}
	}
}

// RunOnce trasforma gli eventi dell'outbox in consegne e invia quelle dovute.
// Restituisce il numero di tentativi di invio fatti.
func (s *Service) RunOnce(ctx context.Context) (int, error) {
	errDispatch := s.dispatch(ctx)
	n, errDeliver := s.deliverDue(ctx)
	return n, errors.Join(errDispatch, errDeliver)
}

// tenantsWith elenca i tenant con lavoro (solo questo passa dal ruolo di piattaforma).
func (s *Service) tenantsWith(ctx context.Context, q string) ([]string, error) {
	rows, err := s.o.DB.Owner.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type outboxEvent struct {
	Event      string          `json:"event"`
	ElementID  string          `json:"element_id"`
	ActorID    *string         `json:"actor_id"`
	ActivityID int64           `json:"activity_id"`
	Added      json.RawMessage `json:"added"`
	Removed    json.RawMessage `json:"removed"`
	At         time.Time       `json:"at"`
}

func (s *Service) dispatch(ctx context.Context) error {
	tenants, err := s.tenantsWith(ctx, `select distinct tenant_id::text from outbox_events where kind = 'webhook' and dispatched_at is null`)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		errs = append(errs, s.dispatchTenant(ctx, t))
	}
	return errors.Join(errs...)
}

type hook struct {
	id     string
	events []string
	filter string
}

func (s *Service) dispatchTenant(ctx context.Context, tenantID string) error {
	return s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `update outbox_events o set dispatched_at = now()
			where o.id in (select id from outbox_events where kind = 'webhook' and dispatched_at is null
			               order by id limit 200 for update skip locked)
			returning o.id, o.payload`)
		if err != nil {
			return err
		}
		type item struct {
			id int64
			ev outboxEvent
		}
		var items []item
		for rows.Next() {
			var it item
			var raw []byte
			if err := rows.Scan(&it.id, &raw); err != nil {
				rows.Close()
				return err
			}
			if json.Unmarshal(raw, &it.ev) == nil {
				items = append(items, it)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		hrows, err := tx.Query(ctx, "select id, events, filter from webhooks where active")
		if err != nil {
			return err
		}
		var hooks []hook
		for hrows.Next() {
			var h hook
			if err := hrows.Scan(&h.id, &h.events, &h.filter); err != nil {
				hrows.Close()
				return err
			}
			hooks = append(hooks, h)
		}
		hrows.Close()
		// Il worker legge come sistema: vede ogni elemento del tenant (non le bozze, che
		// hanno un proprietario: l'id nullo non lo è di nessuna).
		system := engine.Actor{MemberID: "00000000-0000-0000-0000-000000000000", Permissions: []string{"*"}}
		for _, it := range items {
			var targets []hook
			for _, h := range hooks {
				for _, e := range h.events {
					if e == it.ev.Event {
						targets = append(targets, h)
						break
					}
				}
			}
			if len(targets) == 0 {
				continue
			}
			el, err := s.o.Eng.Get(ctx, tx, system, it.ev.ElementID)
			if errors.Is(err, engine.ErrNotFound) {
				continue // elemento cancellato o bozza: niente da consegnare
			}
			if err != nil {
				return err
			}
			var facts engine.Facts
			data := s.eventData(ctx, tx, it.ev)
			body := ""
			eventID := ""
			for _, h := range targets {
				if h.filter != "" {
					c, err := engine.ParseCondition(h.filter)
					if err != nil {
						continue // filtro diventato non valido: si salta, la configurazione lo mostrerà
					}
					if facts == nil {
						if facts, err = s.o.Eng.ElementFacts(ctx, tx, el); err != nil {
							return err
						}
					}
					if !c.Eval(facts) {
						continue
					}
				}
				if body == "" {
					ev := s.envelope(ctx, tx, it.ev.Event, it.ev.At, it.ev.ActorID, el, data)
					if body, err = ev.marshal(); err != nil {
						return err
					}
					eventID = ev.ID
				}
				if _, err := tx.Exec(ctx, `insert into webhook_deliveries (tenant_id, id, webhook_id, event_id, event, body)
					values (app_tenant(), $1, $2, $3, $4, $5)`, db.NewID(), h.id, eventID, it.ev.Event, body); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// eventData costruisce la parte "data" dell'evento dalla voce della storia.
func (s *Service) eventData(ctx context.Context, tx pgx.Tx, ev outboxEvent) map[string]any {
	added := map[string]any{}
	removed := map[string]any{}
	_ = json.Unmarshal(ev.Added, &added)
	_ = json.Unmarshal(ev.Removed, &removed)
	str := func(m map[string]any, k string) any {
		if v, ok := m[k]; ok {
			return v
		}
		return nil
	}
	switch ev.Event {
	case "element.created":
		return map[string]any{"phase": str(added, "state")}
	case "element.phase_changed":
		return map[string]any{"from": str(removed, "name"), "to": str(added, "name"), "comment": str(added, "comment")}
	case "comment.created":
		out := map[string]any{"comment_id": str(added, "comment_id")}
		var text string
		var limited bool
		if id, ok := added["comment_id"].(string); ok &&
			tx.QueryRow(ctx, "select body_text, visibility_limited from comments where id = $1 and deleted_at is null", id).Scan(&text, &limited) == nil {
			// I commenti a visibilità ristretta non escono dal sistema: solo l'id.
			if limited {
				out["restricted"] = true
			} else {
				if r := []rune(text); len(r) > 4000 {
					text = string(r[:4000])
				}
				out["text"] = text
			}
		}
		return out
	case "approval.decided":
		return map[string]any{"decision": str(added, "decision"), "comment": str(added, "comment"), "conditions": str(added, "conditions")}
	case "review.completed":
		return map[string]any{"review": str(added, "review"), "review_type": str(added, "type"), "team": str(added, "team"), "outcome": str(added, "outcome")}
	}
	return added
}

type due struct {
	id, webhookID, eventID, event, body, url string
	attempts                                 int
	secret                                   []byte
	active                                   bool
}

func (s *Service) deliverDue(ctx context.Context) (int, error) {
	tenants, err := s.tenantsWith(ctx, `select distinct tenant_id::text from webhook_deliveries where status = 'pending' and next_attempt_at <= now()`)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, t := range tenants {
		k, err := s.deliverTenant(ctx, t)
		n += k
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

func (s *Service) deliverTenant(ctx context.Context, tenantID string) (int, error) {
	var list []due
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		// Presa in carico: la consegna resta "in volo" per claimLease, così un altro worker
		// (o un riavvio) non la manda due volte mentre si aspetta la risposta.
		rows, err := tx.Query(ctx, `
			update webhook_deliveries d set attempts = d.attempts + 1, last_attempt_at = now(),
			  next_attempt_at = now() + $1::float8 * interval '1 second'
			from webhooks w
			where w.tenant_id = d.tenant_id and w.id = d.webhook_id
			  and d.id in (select id from webhook_deliveries where status = 'pending' and next_attempt_at <= now()
			               order by next_attempt_at limit $2 for update skip locked)
			returning d.id, d.webhook_id, d.event_id, d.event, d.body, d.attempts, w.url, w.secret_enc, w.active`,
			claimLease.Seconds(), batch)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.id, &d.webhookID, &d.eventID, &d.event, &d.body, &d.attempts, &d.url, &d.secret, &d.active); err != nil {
				return err
			}
			list = append(list, d)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, d := range list {
		res := s.send(ctx, d)
		errs = append(errs, s.record(ctx, tenantID, d, res))
	}
	return len(list), errors.Join(errs...)
}

type result struct {
	status   int
	err      string
	duration time.Duration
	final    bool // errore che un nuovo tentativo non risolve
}

// Sign calcola la firma: HMAC-SHA256 del testo "<timestamp>.<corpo>" con il segreto,
// in esadecimale. L'intestazione è X-Regia-Signature: t=<timestamp>,v1=<firma>.
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Service) send(ctx context.Context, d due) result {
	if !d.active {
		return result{err: "webhook disattivato", final: true}
	}
	secret, err := s.o.Box.Open(d.secret, secretContext(d.webhookID))
	if err != nil {
		return result{err: "segreto illeggibile: la chiave applicativa è cambiata", final: true}
	}
	body := []byte(d.body)
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return result{err: "URL non valido", final: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Regia-Webhooks/1")
	req.Header.Set("X-Regia-Event", d.event)
	req.Header.Set("X-Regia-Event-Id", d.eventID)
	req.Header.Set("X-Regia-Delivery", d.id)
	req.Header.Set("X-Regia-Signature", fmt.Sprintf("t=%d,v1=%s", ts, Sign(string(secret), ts, body)))
	start := time.Now()
	resp, err := s.client.Do(req)
	dur := time.Since(start)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, ErrBlockedAddress) {
			msg = ErrBlockedAddress.Error()
		}
		return result{err: truncate(msg, 500), duration: dur}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	r := result{status: resp.StatusCode, duration: dur}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		r.err = "risposta " + resp.Status
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			r.err += ": i redirect non si seguono, aggiorna l'URL"
		}
	}
	return r
}

func (s *Service) record(ctx context.Context, tenantID string, d due, r result) error {
	return s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var status *int
		if r.status != 0 {
			status = &r.status
		}
		ms := int(r.duration.Milliseconds())
		if r.err == "" {
			_, err := tx.Exec(ctx, `update webhook_deliveries set status = 'delivered', response_status = $2, last_error = null,
				duration_ms = $3 where id = $1`, d.id, status, ms)
			return err
		}
		if r.final || d.attempts >= maxAttempts {
			_, err := tx.Exec(ctx, `update webhook_deliveries set status = 'failed', response_status = $2, last_error = $3,
				duration_ms = $4 where id = $1`, d.id, status, r.err, ms)
			return err
		}
		_, err := tx.Exec(ctx, `update webhook_deliveries set response_status = $2, last_error = $3, duration_ms = $4,
			next_attempt_at = now() + $5::float8 * interval '1 second' where id = $1`, d.id, status, r.err, ms, Backoff(d.attempts).Seconds())
		return err
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
