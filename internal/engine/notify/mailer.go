package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
)

// Email delle notifiche (facoltative). Le notifiche finiscono in outbox_events nella
// transazione dell'evento; il worker le raccoglie a intervalli regolari, ne fa un
// riepilogo per persona e lo invia via SMTP. Se l'invio fallisce riprova con attese
// crescenti; dopo maxAttempts tentativi rinuncia e lascia l'errore nella riga.
// Le notifiche già lette in app prima dell'invio non si mandano più.

const (
	outboxKind  = "notification.email"
	maxAttempts = 8
	claimLease  = 10 * time.Minute // un invio interrotto si riprende dopo questo tempo
)

// SMTPConfig è la configurazione del server di posta (REGIA_SMTP_*).
type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Enabled: senza host e mittente le email sono spente.
func (c SMTPConfig) Enabled() bool { return c.Host != "" && c.From != "" }

// Sender invia un messaggio di testo semplice.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Mailer è il worker in-process delle email.
type Mailer struct {
	db       *db.DB
	send     Sender
	interval time.Duration
	baseURL  string
	log      *slog.Logger
}

// NewMailer: interval è la finestra di raggruppamento; baseURL (facoltativo) serve per i link.
func NewMailer(d *db.DB, s Sender, interval time.Duration, baseURL string, log *slog.Logger) *Mailer {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	return &Mailer{db: d, send: s, interval: interval, baseURL: strings.TrimRight(baseURL, "/"), log: log}
}

// Run invia i riepiloghi a ogni intervallo, finché ctx non è chiuso.
func (m *Mailer) Run(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := m.RunOnce(ctx); err != nil {
				m.log.Warn("invio email delle notifiche", "errore", err)
			} else if n > 0 {
				m.log.Info("email delle notifiche inviate", "messaggi", n)
			}
		}
	}
}

// RunOnce elabora le email in attesa di tutti i tenant; restituisce i messaggi inviati.
func (m *Mailer) RunOnce(ctx context.Context) (int, error) {
	// Solo l'elenco dei tenant con lavoro passa dal ruolo di piattaforma; il resto gira
	// nella transazione del tenant, sotto RLS.
	rows, err := m.db.Owner.Query(ctx, `select distinct tenant_id::text from outbox_events
		where dispatched_at is null and kind = $1 and coalesce(next_attempt_at, created_at) <= now()`, outboxKind)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, t := range tenants {
		n, err := m.tenant(ctx, t)
		sent += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sent, errors.Join(errs...)
}

type pending struct {
	eventID  int64
	attempts int
	item     Item
	read     bool
}

type recipient struct {
	email, name, lang string
	events            []pending
}

func (m *Mailer) tenant(ctx context.Context, tenantID string) (int, error) {
	byMember := map[string]*recipient{}
	var order []string
	err := m.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		// Presa in carico: le righe restano "in volo" per claimLease, così un altro worker
		// (o un riavvio) non le manda due volte mentre si parla con il server SMTP.
		rows, err := tx.Query(ctx, `
			update outbox_events o set attempts = o.attempts + 1, next_attempt_at = now() + $2::float8 * interval '1 second'
			where o.id in (select id from outbox_events where dispatched_at is null and kind = $1
				and coalesce(next_attempt_at, created_at) <= now() order by id limit 500 for update skip locked)
			returning o.id, o.attempts, o.payload->>'notification_id'`, outboxKind, claimLease.Seconds())
		if err != nil {
			return err
		}
		type claim struct {
			id       int64
			attempts int
			nid      *string
		}
		var claims []claim
		for rows.Next() {
			var c claim
			if err := rows.Scan(&c.id, &c.attempts, &c.nid); err != nil {
				rows.Close()
				return err
			}
			claims = append(claims, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, c := range claims {
			if c.nid == nil {
				continue
			}
			var p pending
			var payload []byte
			var memberID, email, name, lang string
			err := tx.QueryRow(ctx, `
				select n.id, n.kind, n.payload, n.element_id, coalesce(s.key || '-' || e.number, ''), coalesce(e.title, ''),
				       coalesce(t.key, ''), coalesce(a.display_name, ''), n.read_at, n.created_at,
				       n.member_id, coalesce(nullif(mb.email, ''), ac.email, ''), mb.display_name,
				       coalesce(mb.locale, (select tl.locale from tenant_language tl), 'it')
				from notifications n
				join members mb on mb.tenant_id = n.tenant_id and mb.id = n.member_id and mb.status = 'active'
				left join accounts ac on ac.id = mb.account_id
				left join elements e on e.tenant_id = n.tenant_id and e.id = n.element_id
				left join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
				left join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
				left join members a on a.tenant_id = n.tenant_id and a.id = n.actor_id
				where n.id::text = $1`, *c.nid).Scan(&p.item.ID, &p.item.Kind, &payload, &p.item.ElementID, &p.item.ElementKey,
				&p.item.Title, &p.item.Type, &p.item.ActorName, &p.item.ReadAt, &p.item.CreatedAt, &memberID, &email, &name, &lang)
			p.eventID, p.attempts = c.id, c.attempts
			if errors.Is(err, pgx.ErrNoRows) {
				memberID = "" // notifica o persona non più presenti: si chiude l'evento
			} else if err != nil {
				return err
			}
			p.item.Payload = map[string]any{}
			_ = json.Unmarshal(payload, &p.item.Payload)
			p.read = p.item.ReadAt != nil || memberID == "" || email == ""
			r := byMember[memberID]
			if r == nil {
				r = &recipient{email: email, name: name, lang: i18n.Norm(lang)}
				byMember[memberID] = r
				order = append(order, memberID)
			}
			r.events = append(r.events, p)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, mid := range order {
		r := byMember[mid]
		var items []Item
		var done, all []int64
		attempts := 0
		for _, p := range r.events {
			all = append(all, p.eventID)
			attempts = max(attempts, p.attempts)
			if p.read {
				done = append(done, p.eventID)
				continue
			}
			items = append(items, p.item)
		}
		if len(items) == 0 {
			errs = append(errs, m.close(ctx, tenantID, done, nil, ""))
			continue
		}
		subject, body := m.compose(r.lang, items)
		if err := m.send.Send(ctx, r.email, subject, body); err != nil {
			errs = append(errs, m.retry(ctx, tenantID, all, attempts, err))
			continue
		}
		sent++
		ids := make([]string, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		errs = append(errs, m.close(ctx, tenantID, all, ids, ""))
	}
	return sent, errors.Join(errs...)
}

// close segna gli eventi come spediti (e le notifiche come inviate per email).
func (m *Mailer) close(ctx context.Context, tenantID string, events []int64, notifications []string, lastError string) error {
	if len(events) == 0 {
		return nil
	}
	return m.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update outbox_events set dispatched_at = now(), last_error = nullif($2, '') where id = any($1)`,
			events, lastError); err != nil {
			return err
		}
		if len(notifications) > 0 {
			_, err := tx.Exec(ctx, `update notifications set emailed_at = now() where id::text = any($1)`, notifications)
			return err
		}
		return nil
	})
}

// retry riprogramma l'invio con attesa crescente; oltre maxAttempts rinuncia.
func (m *Mailer) retry(ctx context.Context, tenantID string, events []int64, attempts int, cause error) error {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if attempts >= maxAttempts {
		m.log.Warn("email di notifica abbandonata dopo troppi tentativi", "tenant", tenantID, "errore", msg)
		return m.close(ctx, tenantID, events, nil, msg)
	}
	wait := m.interval * time.Duration(1<<min(attempts, 6))
	if wait > 6*time.Hour {
		wait = 6 * time.Hour
	}
	return m.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update outbox_events set next_attempt_at = now() + $2::float8 * interval '1 second', last_error = $3 where id = any($1)`,
			events, wait.Seconds(), msg)
		return err
	})
}

// compose scrive l'email nella lingua di chi la riceve: oggetto, corpo e testi di servizio.
func (m *Mailer) compose(lang string, items []Item) (subject, body string) {
	var b strings.Builder
	if len(items) == 1 {
		subject = i18n.T(lang, "mail.subject.one", i18n.P{"text": DescribeLang(lang, items[0])})
	} else {
		subject = i18n.T(lang, "mail.subject.many", i18n.P{"count": len(items)})
	}
	b.WriteString(i18n.T(lang, "mail.intro") + "\n\n")
	for _, it := range items {
		b.WriteString("- " + DescribeLang(lang, it))
		if it.Title != "" {
			b.WriteString("\n  «" + it.Title + "»")
		}
		if ex, _ := it.Payload["excerpt"].(string); ex != "" {
			b.WriteString("\n  " + ex)
		}
		if m.baseURL != "" && it.ElementKey != "" {
			b.WriteString("\n  " + m.baseURL + "/richieste?sel=" + it.ElementKey)
		} else if m.baseURL != "" && it.Kind == KindTimesheet {
			mem, _ := it.Payload["member_id"].(string)
			wk, _ := it.Payload["week"].(string)
			b.WriteString("\n  " + m.baseURL + TimesheetLink(mem, wk))
		}
		b.WriteString("\n\n")
	}
	if m.baseURL != "" {
		b.WriteString(i18n.T(lang, "mail.all", i18n.P{"url": m.baseURL + "/notifiche"}) + "\n")
	}
	b.WriteString(i18n.T(lang, "mail.prefs") + "\n")
	return subject, b.String()
}

// SMTPSender invia con net/smtp: TLS implicito sulla porta 465, altrimenti STARTTLS
// se il server lo offre. L'autenticazione PLAIN avviene solo su connessione cifrata
// (o verso localhost), come impone net/smtp.
type SMTPSender struct {
	Cfg SMTPConfig
}

func (s SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	from, err := mail.ParseAddress(s.Cfg.From)
	if err != nil {
		return fmt.Errorf("REGIA_SMTP_FROM non valido: %w", err)
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("destinatario non valido: %w", err)
	}
	port := s.Cfg.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(s.Cfg.Host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	tlsCfg := &tls.Config{ServerName: s.Cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
		}
	}
	if s.Cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Cfg.User, s.Cfg.Password, s.Cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(rcpt.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(Message(s.Cfg.From, rcpt.Address, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Message compone un messaggio di testo UTF-8 in quoted-printable. I valori delle
// intestazioni sono ripuliti dagli a capo (niente iniezione di intestazioni).
func Message(from, to, subject, body string) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	var b bytes.Buffer
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	host := "regia"
	if a, err := mail.ParseAddress(from); err == nil {
		if _, d, ok := strings.Cut(a.Address, "@"); ok {
			host = d
		}
	}
	fmt.Fprintf(&b, "From: %s\r\n", clean(from))
	fmt.Fprintf(&b, "To: %s\r\n", clean(to))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), clean(host))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = qp.Close()
	return b.Bytes()
}
