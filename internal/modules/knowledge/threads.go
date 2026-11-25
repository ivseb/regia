package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/modules/collab"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Commenti in linea: si seleziona una frase della pagina e si apre un thread. Il thread
// resta ancorato alla frase citata (anchor.go); a ogni salvataggio la posizione si ricalcola
// e, se la frase non c'è più, il thread diventa orfano ma resta visibile. Ogni thread ha
// risposte con menzioni e uno stato (aperto, risolto) che si può riaprire. Autori e
// menzionati ricevono la notifica.

const (
	permCommentWrite = "comment.write"
	// maxThreadsPerPage limita i thread di una pagina (i risolti contano).
	maxThreadsPerPage = 300
	maxThreadBody     = 5000
)

// ThreadComment è un commento di un thread.
type ThreadComment struct {
	ID        string           `json:"id"`
	AuthorID  *string          `json:"author_id"`
	Author    string           `json:"author"`
	Body      string           `json:"body"`
	Mentions  []collab.Mention `json:"mentions"`
	CreatedAt time.Time        `json:"created_at"`
	EditedAt  *time.Time       `json:"edited_at"`
	CanDelete bool             `json:"can_delete"`
}

// Thread è una discussione ancorata a una frase.
type Thread struct {
	ID         string          `json:"id"`
	Quote      string          `json:"quote"`
	Prefix     string          `json:"prefix"`
	Suffix     string          `json:"suffix"`
	Pos        *int            `json:"pos"`
	Nth        int             `json:"nth"`
	Orphan     bool            `json:"orphan"`
	Status     string          `json:"status"`  // open | resolved
	Version    int             `json:"version"` // versione della pagina in cui l'ancora è stata calcolata
	ResolvedBy string          `json:"resolved_by"`
	ResolvedAt *time.Time      `json:"resolved_at"`
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
	Comments   []ThreadComment `json:"comments"`
}

// ThreadList sono i thread della pagina e ciò che l'attore può farci.
type ThreadList struct {
	Items       []Thread `json:"items"`
	CanComment  bool     `json:"can_comment"`
	CanResolve  bool     `json:"can_resolve"`
	PageVersion int      `json:"page_version"`
}

// Threads elenca i thread della pagina con i loro commenti: prima gli aperti.
func (s *Service) Threads(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*ThreadList, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	return s.threadList(ctx, tx, actor, el.ID, sp, k)
}

func (s *Service) threadList(ctx context.Context, tx pgx.Tx, actor engine.Actor, pageID string, sp *Space, k *kbRow) (*ThreadList, error) {
	out := &ThreadList{Items: []Thread{}, PageVersion: k.version,
		CanComment: actor.Has(permCommentWrite), CanResolve: actor.Has(permCommentWrite) || sp.Rights.Write}
	rows, err := tx.Query(ctx, `
		select t.id::text, t.quote, t.prefix, t.suffix, t.pos, t.nth, t.orphan, t.status, t.anchored_version,
		       coalesce(r.display_name, ''), t.resolved_at, coalesce(c.display_name, ''), t.created_at
		from kb_threads t
		left join members c on c.tenant_id = t.tenant_id and c.id = t.created_by
		left join members r on r.tenant_id = t.tenant_id and r.id = t.resolved_by
		where t.page_id = $1
		order by (t.status = 'open') desc, t.created_at desc
		limit $2`, pageID, maxThreadsPerPage)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.Quote, &t.Prefix, &t.Suffix, &t.Pos, &t.Nth, &t.Orphan, &t.Status, &t.Version,
			&t.ResolvedBy, &t.ResolvedAt, &t.CreatedBy, &t.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		t.Comments = []ThreadComment{}
		idx[t.ID] = len(out.Items)
		out.Items = append(out.Items, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(idx) == 0 {
		return out, nil
	}
	crows, err := tx.Query(ctx, `
		select c.id::text, c.thread_id::text, c.author_id::text, coalesce(m.display_name, 'Utente rimosso'), c.body, c.mentions, c.created_at, c.edited_at
		from kb_thread_comments c
		join kb_threads t on t.tenant_id = c.tenant_id and t.id = c.thread_id
		left join members m on m.tenant_id = c.tenant_id and m.id = c.author_id
		where t.page_id = $1 and c.deleted_at is null
		order by c.created_at, c.id`, pageID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c ThreadComment
		var threadID string
		var raw []byte
		if err := crows.Scan(&c.ID, &threadID, &c.AuthorID, &c.Author, &c.Body, &raw, &c.CreatedAt, &c.EditedAt); err != nil {
			return nil, err
		}
		c.Mentions = []collab.Mention{}
		_ = json.Unmarshal(raw, &c.Mentions)
		own := c.AuthorID != nil && *c.AuthorID == actor.MemberID
		c.CanDelete = own || actor.Has("*") || sp.Rights.Manage
		if i, ok := idx[threadID]; ok {
			out.Items[i].Comments = append(out.Items[i].Comments, c)
		}
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	// Un thread senza commenti (tutti eliminati) non si mostra.
	kept := out.Items[:0]
	for _, t := range out.Items {
		if len(t.Comments) > 0 {
			kept = append(kept, t)
		}
	}
	out.Items = kept
	return out, nil
}

// ThreadInput apre un thread su una frase.
type ThreadInput struct {
	Quote  string `json:"quote"`
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
	Nth    int    `json:"nth"`
	Body   string `json:"body"`
}

func cleanThreadBody(body string) (string, error) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return "", engine.Required("body", "il commento è vuoto")
	}
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) > maxThreadBody {
		return "", engine.InvalidValue("body", "commento non valido o troppo lungo (massimo %d caratteri)", maxThreadBody)
	}
	return body, nil
}

// CreateThread apre un thread sulla frase citata. Il server verifica la frase nel testo
// corrente: se la trova, calcola posizione e contesto; se non può verificarla (selezione che
// attraversa elementi particolari) la ancora solo con il testo e lascia la ricerca al browser.
func (s *Service) CreateThread(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ThreadInput) (*ThreadList, error) {
	if !actor.Has(permCommentWrite) {
		return nil, engine.ErrForbidden
	}
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	quote := normQuote(in.Quote)
	if quote == "" || utf8.RuneCountInString(quote) > maxQuoteRunes || !utf8.ValidString(quote) {
		return nil, engine.InvalidParams("validation.knowledge.quote_too_long", "quote", map[string]any{"max": maxQuoteRunes}, "seleziona una frase di al massimo %d caratteri", maxQuoteRunes)
	}
	body, err := cleanThreadBody(in.Body)
	if err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from kb_threads where page_id = $1`, el.ID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= maxThreadsPerPage {
		return nil, engine.InvalidParams("validation.knowledge.threads_limit", "quote", map[string]any{"max": maxThreadsPerPage}, "la pagina ha già %d discussioni: risolvile o eliminane qualcuna", maxThreadsPerPage)
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `select body from elements where id = $1`, el.ID).Scan(&raw); err != nil {
		return nil, err
	}
	text := AnchorText(readBody(raw).MD)
	prefix := lastRunes(normSpace(in.Prefix), contextRunes)
	suffix := firstRunes(normSpace(in.Suffix), contextRunes)
	var pos *int
	nth := max(in.Nth, 0)
	if loc, ok := Locate(text, Anchor{Quote: quote, Prefix: prefix, Suffix: suffix, Pos: -1, Nth: nth}); ok {
		pos, nth, prefix, suffix = &loc.Pos, loc.Nth, loc.Prefix, loc.Suffix
	}
	mentions, err := collab.ResolveMentions(ctx, tx, body)
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into kb_threads (tenant_id, id, page_id, quote, prefix, suffix, pos, nth, anchored_version, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, el.ID, quote, prefix, suffix, pos, nth, k.version, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	cid := db.NewID()
	if err := insertThreadComment(ctx, tx, cid, id, actor, body, mentions); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.thread.create", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"thread_id": id, "mentions": len(mentions)}}); err != nil {
		return nil, err
	}
	payload := map[string]any{"event": "inline_comment", "thread_id": id, "excerpt": collab.Excerpt(body, 160), "quote": collab.Excerpt(quote, 100), "inline": true}
	ids := mentionIDs(mentions)
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindMention, ElementID: el.ID, ActorID: actor.MemberID,
		Members: ids, Payload: payload}); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindComment, ElementID: el.ID, ActorID: actor.MemberID,
		Followers: true, Exclude: ids, Payload: payload}); err != nil {
		return nil, err
	}
	return s.threadList(ctx, tx, actor, el.ID, sp, k)
}

func mentionIDs(ms []collab.Mention) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func insertThreadComment(ctx context.Context, tx pgx.Tx, id, threadID string, actor engine.Actor, body string, mentions []collab.Mention) error {
	raw, _ := json.Marshal(mentions)
	_, err := tx.Exec(ctx, `insert into kb_thread_comments (tenant_id, id, thread_id, author_id, body, mentions)
		values (app_tenant(), $1, $2, $3, $4, $5)`, id, threadID, nullable(actor.MemberID), body, raw)
	return err
}

// threadOf carica e blocca il thread, verificando che la pagina sia leggibile.
type threadRow struct {
	id, pageID, quote, status, createdBy string
}

func (s *Service) threadOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, threadID string) (*threadRow, *engine.Element, *Space, *kbRow, error) {
	if !uuidRe.MatchString(threadID) {
		return nil, nil, nil, nil, engine.ErrNotFound
	}
	var t threadRow
	err := tx.QueryRow(ctx, `select id::text, page_id::text, quote, status, coalesce(created_by::text, '') from kb_threads where id = $1 for update`,
		threadID).Scan(&t.id, &t.pageID, &t.quote, &t.status, &t.createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	el, sp, k, err := s.loadPage(ctx, tx, actor, t.pageID, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return &t, el, sp, k, nil
}

// ReplyThread aggiunge una risposta al thread: la ricevono gli altri autori del thread; i
// menzionati hanno la loro notifica.
func (s *Service) ReplyThread(ctx context.Context, tx pgx.Tx, actor engine.Actor, threadID, body string) (*ThreadList, error) {
	if !actor.Has(permCommentWrite) {
		return nil, engine.ErrForbidden
	}
	t, el, sp, k, err := s.threadOf(ctx, tx, actor, threadID)
	if err != nil {
		return nil, err
	}
	if body, err = cleanThreadBody(body); err != nil {
		return nil, err
	}
	mentions, err := collab.ResolveMentions(ctx, tx, body)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select distinct author_id::text from kb_thread_comments where thread_id = $1 and deleted_at is null and author_id is not null`, t.id)
	if err != nil {
		return nil, err
	}
	authors, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if t.createdBy != "" {
		authors = append(authors, t.createdBy)
	}
	if err := insertThreadComment(ctx, tx, db.NewID(), t.id, actor, body, mentions); err != nil {
		return nil, err
	}
	// Una risposta a un thread risolto lo riapre: la discussione è di nuovo viva.
	if t.status == "resolved" {
		if _, err := tx.Exec(ctx, `update kb_threads set status = 'open', resolved_by = null, resolved_at = null where id = $1`, t.id); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.thread.reply", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"thread_id": t.id, "mentions": len(mentions)}}); err != nil {
		return nil, err
	}
	payload := map[string]any{"event": "inline_reply", "thread_id": t.id, "excerpt": collab.Excerpt(body, 160), "quote": collab.Excerpt(t.quote, 100), "inline": true}
	ids := mentionIDs(mentions)
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindMention, ElementID: el.ID, ActorID: actor.MemberID,
		Members: ids, Payload: payload}); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindComment, ElementID: el.ID, ActorID: actor.MemberID,
		Members: authors, Exclude: ids, Payload: payload}); err != nil {
		return nil, err
	}
	return s.threadList(ctx, tx, actor, el.ID, sp, k)
}

// SetThreadStatus risolve o riapre il thread; chi l'ha aperto lo viene a sapere.
func (s *Service) SetThreadStatus(ctx context.Context, tx pgx.Tx, actor engine.Actor, threadID string, resolve bool) (*ThreadList, error) {
	t, el, sp, k, err := s.threadOf(ctx, tx, actor, threadID)
	if err != nil {
		return nil, err
	}
	if !actor.Has(permCommentWrite) && !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	want := "open"
	if resolve {
		want = "resolved"
	}
	if t.status != want {
		if resolve {
			_, err = tx.Exec(ctx, `update kb_threads set status = 'resolved', resolved_by = $2, resolved_at = now() where id = $1`, t.id, nullable(actor.MemberID))
		} else {
			_, err = tx.Exec(ctx, `update kb_threads set status = 'open', resolved_by = null, resolved_at = null where id = $1`, t.id)
		}
		if err != nil {
			return nil, err
		}
		action, event := "page.thread.reopen", "inline_reopened"
		if resolve {
			action, event = "page.thread.resolve", "inline_resolved"
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: action, EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"thread_id": t.id}}); err != nil {
			return nil, err
		}
		if t.createdBy != "" {
			if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindComment, ElementID: el.ID, ActorID: actor.MemberID,
				Members: []string{t.createdBy}, Payload: map[string]any{"event": event, "thread_id": t.id, "quote": collab.Excerpt(t.quote, 100), "inline": true}}); err != nil {
				return nil, err
			}
		}
	}
	return s.threadList(ctx, tx, actor, el.ID, sp, k)
}

// DeleteThreadComment elimina (logicamente) un commento: il proprio, o di chiunque per
// chi amministra lo spazio. Senza più commenti il thread sparisce.
func (s *Service) DeleteThreadComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, threadID, commentID string) (*ThreadList, error) {
	t, el, sp, k, err := s.threadOf(ctx, tx, actor, threadID)
	if err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(commentID) {
		return nil, engine.ErrNotFound
	}
	var author string
	err = tx.QueryRow(ctx, `select coalesce(author_id::text, '') from kb_thread_comments where id = $1 and thread_id = $2 and deleted_at is null for update`,
		commentID, t.id).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !(author != "" && author == actor.MemberID) && !actor.Has("*") && !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `update kb_thread_comments set deleted_at = now() where id = $1`, commentID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.thread.delete_comment", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"thread_id": t.id, "comment_id": commentID}}); err != nil {
		return nil, err
	}
	return s.threadList(ctx, tx, actor, el.ID, sp, k)
}

// reanchor ricalcola le ancore dei thread dopo un nuovo testo: ogni frase ritrovata segue la
// sua nuova posizione, quella che non c'è più rende il thread orfano (e lo riporta in vita se
// la frase torna, per esempio con un ripristino).
func reanchor(ctx context.Context, tx pgx.Tx, pageID, md string, version int) error {
	rows, err := tx.Query(ctx, `select id::text, quote, prefix, suffix, pos, nth, orphan from kb_threads where page_id = $1 and pos is not null`, pageID)
	if err != nil {
		return err
	}
	type row struct {
		id, quote, prefix, suffix string
		pos                       *int
		nth                       int
		orphan                    bool
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.quote, &r.prefix, &r.suffix, &r.pos, &r.nth, &r.orphan); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return err
	}
	text := AnchorText(md)
	for _, r := range list {
		loc, ok := Locate(text, Anchor{Quote: r.quote, Prefix: r.prefix, Suffix: r.suffix, Pos: *r.pos, Nth: r.nth})
		if !ok {
			if !r.orphan {
				if _, err := tx.Exec(ctx, `update kb_threads set orphan = true where id = $1`, r.id); err != nil {
					return err
				}
			}
			continue
		}
		if r.orphan || loc.Pos != *r.pos || loc.Nth != r.nth || loc.Prefix != r.prefix || loc.Suffix != r.suffix {
			if _, err := tx.Exec(ctx, `update kb_threads set orphan = false, pos = $2, nth = $3, prefix = $4, suffix = $5, anchored_version = $6 where id = $1`,
				r.id, loc.Pos, loc.Nth, loc.Prefix, loc.Suffix, version); err != nil {
				return err
			}
		}
	}
	return nil
}
