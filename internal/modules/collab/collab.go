// Package collab gestisce la collaborazione sugli elementi: commenti in markdown leggero
// con menzioni, allegati su disco e osservatori (piano, passo 3). Le notifiche le genera
// il pacchetto notify, nella stessa transazione.
//
// Permessi: chi legge l'elemento ne legge commenti e allegati; per scrivere serve
// comment.write (per gli allegati anche element.write); ognuno modifica ed elimina i
// propri commenti e allegati, l'amministratore del tenant può eliminare anche quelli altrui.
package collab

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

const (
	permCommentWrite = "comment.write"
	permElementWrite = "element.write"
	maxCommentRunes  = 20000
)

type Service struct {
	eng       *engine.Service
	Files     *FileStore // nil = allegati non disponibili
	MaxUpload int64      // byte
}

func New(eng *engine.Service, files *FileStore, maxUpload int64) *Service {
	return &Service{eng: eng, Files: files, MaxUpload: maxUpload}
}

// Mention è una persona citata con @nome.
type Mention struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Comment è un commento da mostrare.
type Comment struct {
	ID          string       `json:"id"`
	ElementID   string       `json:"element_id"`
	AuthorID    *string      `json:"author_id"`
	AuthorName  string       `json:"author_name"`
	Body        string       `json:"body"` // markdown leggero, reso in sicurezza dal client
	Mentions    []Mention    `json:"mentions"`
	CreatedAt   time.Time    `json:"created_at"`
	EditedAt    *time.Time   `json:"edited_at"`
	CanEdit     bool         `json:"can_edit"`
	CanDelete   bool         `json:"can_delete"`
	Attachments []Attachment `json:"attachments"`
}

type commentBody struct {
	Format   string    `json:"format"`
	Mentions []Mention `json:"mentions"`
}

// Comments elenca i commenti dell'elemento, dal più vecchio, con i loro allegati.
func (s *Service) Comments(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]Comment, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select c.id, c.element_id, c.author_id, coalesce(m.display_name, 'Utente rimosso'), c.body_text, c.body, c.created_at, c.edited_at
		from comments c left join members m on m.tenant_id = c.tenant_id and m.id = c.author_id
		where c.element_id = $1 and c.deleted_at is null
		order by c.created_at, c.id limit 1000`, el.ID)
	if err != nil {
		return nil, err
	}
	out := []Comment{}
	idx := map[string]int{}
	for rows.Next() {
		var c Comment
		var raw []byte
		if err := rows.Scan(&c.ID, &c.ElementID, &c.AuthorID, &c.AuthorName, &c.Body, &raw, &c.CreatedAt, &c.EditedAt); err != nil {
			rows.Close()
			return nil, err
		}
		var b commentBody
		_ = json.Unmarshal(raw, &b)
		c.Mentions = b.Mentions
		if c.Mentions == nil {
			c.Mentions = []Mention{}
		}
		own := c.AuthorID != nil && *c.AuthorID == actor.MemberID
		c.CanEdit = own && actor.Has(permCommentWrite)
		c.CanDelete = own || actor.Has("*")
		c.Attachments = []Attachment{}
		idx[c.ID] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	atts, err := s.listAttachments(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range atts {
		if a.CommentID != nil {
			if i, ok := idx[*a.CommentID]; ok {
				out[i].Attachments = append(out[i].Attachments, a)
			}
		}
	}
	return out, nil
}

func cleanBody(body string) (string, error) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return "", engine.Required("body", "il commento è vuoto")
	}
	if utf8.RuneCountInString(body) > maxCommentRunes {
		return "", engine.TooLong("body", maxCommentRunes, "commento troppo lungo (massimo %d caratteri)", maxCommentRunes)
	}
	if !utf8.ValidString(body) {
		return "", engine.InvalidValue("body", "testo non valido")
	}
	return body, nil
}

// AddComment aggiunge un commento: storia, audit, notifiche ai menzionati e agli osservatori.
func (s *Service) AddComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, body string) (*Comment, error) {
	if !actor.Has(permCommentWrite) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if body, err = cleanBody(body); err != nil {
		return nil, err
	}
	mentions, err := ResolveMentions(ctx, tx, body)
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into comments (tenant_id, id, element_id, author_id, body, body_text)
		values (app_tenant(), $1, $2, $3, $4, $5)`, id, el.ID, actor.MemberID, commentBody{Format: "markdown", Mentions: mentions}, body); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "comment", "added", map[string]any{"comment_id": id}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "comment.create", EntityType: "comment", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": el.ID, "mentions": len(mentions)}}); err != nil {
		return nil, err
	}
	ids := mentionIDs(mentions)
	payload := map[string]any{"comment_id": id, "excerpt": Excerpt(body, 160)}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindMention, ElementID: el.ID, ActorID: actor.MemberID,
		Members: ids, Payload: payload}); err != nil {
		return nil, err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindComment, ElementID: el.ID, ActorID: actor.MemberID,
		Followers: true, Exclude: ids, Payload: payload}); err != nil {
		return nil, err
	}
	return s.comment(ctx, tx, actor, id)
}

// EditComment modifica il proprio commento; i nuovi menzionati ricevono la notifica.
func (s *Service) EditComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, commentID, body string) (*Comment, error) {
	if !actor.Has(permCommentWrite) {
		return nil, engine.ErrForbidden
	}
	c, err := s.lockComment(ctx, tx, actor, commentID)
	if err != nil {
		return nil, err
	}
	if c.AuthorID == nil || *c.AuthorID != actor.MemberID {
		return nil, engine.ErrForbidden
	}
	if body, err = cleanBody(body); err != nil {
		return nil, err
	}
	if body == c.Body {
		return s.comment(ctx, tx, actor, c.ID)
	}
	mentions, err := ResolveMentions(ctx, tx, body)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update comments set body = $2, body_text = $3, edited_at = now() where id = $1`,
		c.ID, commentBody{Format: "markdown", Mentions: mentions}, body); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, c.ElementID, actor.MemberID, "comment", "edited", map[string]any{"comment_id": c.ID}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "comment.update", EntityType: "comment", EntityID: c.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": c.ElementID, "mentions": len(mentions)}}); err != nil {
		return nil, err
	}
	old := map[string]bool{}
	for _, m := range c.Mentions {
		old[m.ID] = true
	}
	var added []string
	for _, m := range mentions {
		if !old[m.ID] {
			added = append(added, m.ID)
		}
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindMention, ElementID: c.ElementID, ActorID: actor.MemberID,
		Members: added, Payload: map[string]any{"comment_id": c.ID, "excerpt": Excerpt(body, 160)}}); err != nil {
		return nil, err
	}
	return s.comment(ctx, tx, actor, c.ID)
}

// DeleteComment elimina (logicamente) il commento e i suoi allegati.
func (s *Service) DeleteComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, commentID string) error {
	c, err := s.lockComment(ctx, tx, actor, commentID)
	if err != nil {
		return err
	}
	if !c.CanDelete {
		return engine.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `update comments set deleted_at = now() where id = $1`, c.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update attachments set removed_at = now() where comment_id = $1 and removed_at is null`, c.ID); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, c.ElementID, actor.MemberID, "comment", "deleted", map[string]any{"comment_id": c.ID}, nil); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "comment.delete", EntityType: "comment", EntityID: c.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": c.ElementID}})
}

// lockComment carica e blocca il commento, verificando che l'elemento sia leggibile.
func (s *Service) lockComment(ctx context.Context, tx pgx.Tx, actor engine.Actor, commentID string) (*Comment, error) {
	if !isUUID(commentID) {
		return nil, engine.ErrNotFound
	}
	var elementID string
	err := tx.QueryRow(ctx, `select element_id from comments where id = $1 and deleted_at is null for update`, commentID).Scan(&elementID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.eng.Get(ctx, tx, actor, elementID); err != nil {
		return nil, err
	}
	return s.comment(ctx, tx, actor, commentID)
}

func (s *Service) comment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Comment, error) {
	var c Comment
	var raw []byte
	err := tx.QueryRow(ctx, `
		select c.id, c.element_id, c.author_id, coalesce(m.display_name, 'Utente rimosso'), c.body_text, c.body, c.created_at, c.edited_at
		from comments c left join members m on m.tenant_id = c.tenant_id and m.id = c.author_id
		where c.id = $1 and c.deleted_at is null`, id).Scan(&c.ID, &c.ElementID, &c.AuthorID, &c.AuthorName, &c.Body, &raw, &c.CreatedAt, &c.EditedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var b commentBody
	_ = json.Unmarshal(raw, &b)
	c.Mentions = b.Mentions
	if c.Mentions == nil {
		c.Mentions = []Mention{}
	}
	own := c.AuthorID != nil && *c.AuthorID == actor.MemberID
	c.CanEdit = own && actor.Has(permCommentWrite)
	c.CanDelete = own || actor.Has("*")
	c.Attachments = []Attachment{}
	atts, err := s.listAttachmentsWhere(ctx, tx, actor, "a.comment_id = $1", id)
	if err != nil {
		return nil, err
	}
	c.Attachments = atts
	return &c, nil
}

// mentionRe trova "@" seguito da un massimo di quattro parole (i nomi hanno spazi:
// "@Sara Villa"). Il carattere prima di @ non deve essere una lettera (email, handle).
var mentionRe = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_@.])@(\p{L}[\p{L}\p{M}\p{N}'’\-]*(?:[ \t]+\p{L}[\p{L}\p{M}\p{N}'’\-]*){0,3})`)

// ResolveMentions riconosce le menzioni: per ogni @ vince il nome di membro attivo più
// lungo che corrisponde alle parole seguenti (senza distinguere maiuscole).
func ResolveMentions(ctx context.Context, tx pgx.Tx, body string) ([]Mention, error) {
	matches := mentionRe.FindAllStringSubmatch(body, 50)
	if len(matches) == 0 {
		return []Mention{}, nil
	}
	var occ [][]string
	seen := map[string]bool{}
	var cands []string
	for _, m := range matches {
		words := strings.Fields(m[1])
		var prefixes []string
		for i := len(words); i >= 1; i-- {
			p := strings.ToLower(strings.Join(words[:i], " "))
			prefixes = append(prefixes, p)
			if !seen[p] {
				seen[p] = true
				cands = append(cands, p)
			}
		}
		occ = append(occ, prefixes)
	}
	rows, err := tx.Query(ctx, `select id::text, display_name, lower(display_name) from members
		where status = 'active' and lower(display_name) = any($1) order by display_name, id`, cands)
	if err != nil {
		return nil, err
	}
	byName := map[string]Mention{}
	for rows.Next() {
		var mm Mention
		var low string
		if err := rows.Scan(&mm.ID, &mm.Name, &low); err != nil {
			rows.Close()
			return nil, err
		}
		if _, dup := byName[low]; !dup { // omonimi: vince il primo, in ordine stabile
			byName[low] = mm
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Mention{}
	got := map[string]bool{}
	for _, prefixes := range occ {
		for _, p := range prefixes { // dal più lungo
			if mm, ok := byName[p]; ok {
				if !got[mm.ID] {
					got[mm.ID] = true
					out = append(out, mm)
				}
				break
			}
		}
	}
	return out, nil
}

func mentionIDs(ms []Mention) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

var mdMarks = strings.NewReplacer("**", "", "__", "", "`", "", "*", "", "#", "", ">", "")
var linkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// Excerpt è un estratto in testo semplice per notifiche ed email.
func Excerpt(body string, n int) string {
	t := linkRe.ReplaceAllString(body, "$1")
	t = mdMarks.Replace(t)
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return t
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }
