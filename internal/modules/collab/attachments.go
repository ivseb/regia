package collab

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Attachment è un allegato da mostrare.
type Attachment struct {
	ID         string    `json:"id"`
	ElementID  string    `json:"element_id"`
	CommentID  *string   `json:"comment_id"`
	AuthorName string    `json:"author_name"`
	Name       string    `json:"name"`
	MimeType   string    `json:"mime_type"`
	Size       int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	Width      *int      `json:"width"`
	Height     *int      `json:"height"`
	Preview    bool      `json:"preview"` // immagine mostrabile in linea
	CreatedAt  time.Time `json:"created_at"`
	CanDelete  bool      `json:"can_delete"`
	authorID   *string
}

// Attachments elenca gli allegati non eliminati dell'elemento (anche quelli dei commenti).
func (s *Service) Attachments(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]Attachment, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	return s.listAttachments(ctx, tx, actor, el.ID)
}

func (s *Service) listAttachments(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) ([]Attachment, error) {
	return s.listAttachmentsWhere(ctx, tx, actor, "a.element_id = $1", elementID)
}

func (s *Service) listAttachmentsWhere(ctx context.Context, tx pgx.Tx, actor engine.Actor, where string, arg string) ([]Attachment, error) {
	rows, err := tx.Query(ctx, `
		select a.id, a.element_id, a.comment_id, a.author_id, coalesce(m.display_name, 'Utente rimosso'), a.name, a.mime_type,
		       a.size_bytes, a.sha256, a.width, a.height, a.created_at
		from attachments a left join members m on m.tenant_id = a.tenant_id and m.id = a.author_id
		where `+where+` and a.removed_at is null
		order by a.created_at, a.id limit 1000`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		var sum []byte
		if err := rows.Scan(&a.ID, &a.ElementID, &a.CommentID, &a.authorID, &a.AuthorName, &a.Name, &a.MimeType,
			&a.Size, &sum, &a.Width, &a.Height, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.SHA256 = hex.EncodeToString(sum)
		a.Preview = InlineImage(a.MimeType)
		a.CanDelete = (a.authorID != nil && *a.authorID == actor.MemberID) || actor.Has("*")
		out = append(out, a)
	}
	return out, rows.Err()
}

// CanUpload: serve comment.write o element.write, oltre alla lettura dell'elemento.
func CanUpload(actor engine.Actor) bool {
	return actor.Has(permCommentWrite) || actor.Has(permElementWrite)
}

// PrepareUpload verifica, prima di ricevere il file, che l'attore possa allegare
// all'elemento; restituisce l'id dell'elemento.
func (s *Service) PrepareUpload(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (string, error) {
	if !CanUpload(actor) {
		return "", engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return "", err
	}
	return el.ID, nil
}

// SaveAttachment registra i metadati di un file già salvato su disco.
// commentID (facoltativo) lega l'allegato a un proprio commento dello stesso elemento.
func (s *Service) SaveAttachment(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, commentID, name string, b Blob) (*Attachment, error) {
	if !CanUpload(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.eng.Get(ctx, tx, actor, elementID)
	if err != nil {
		return nil, err
	}
	if commentID != "" {
		var ok bool
		if isUUID(commentID) {
			if err := tx.QueryRow(ctx, `select exists (select 1 from comments where id = $1 and element_id = $2
				and author_id = $3 and deleted_at is null)`, commentID, el.ID, actor.MemberID).Scan(&ok); err != nil {
				return nil, err
			}
		}
		if !ok {
			return nil, engine.NotFound("comment_id", "", "commento inesistente o non tuo")
		}
	}
	sum, err := hex.DecodeString(b.SHA256)
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	name = CleanName(name)
	if _, err := tx.Exec(ctx, `insert into attachments (tenant_id, id, element_id, comment_id, author_id, name, mime_type,
		size_bytes, sha256, storage_key, width, height)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, el.ID, nullable(commentID), actor.MemberID, name, b.Mime, b.Size, sum, "sha256/"+b.SHA256, b.Width, b.Height); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "attachment", "added",
		map[string]any{"attachment_id": id, "name": name}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "attachment.create", EntityType: "attachment", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": el.ID, "sha256": b.SHA256, "size": b.Size, "mime": b.Mime}}); err != nil {
		return nil, err
	}
	atts, err := s.listAttachmentsWhere(ctx, tx, actor, "a.id = $1", id)
	if err != nil || len(atts) == 0 {
		return nil, errors.Join(err, engine.ErrNotFound)
	}
	return &atts[0], nil
}

// Download restituisce i metadati di un allegato leggibile dall'attore.
func (s *Service) Download(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Attachment, error) {
	if !isUUID(id) {
		return nil, engine.ErrNotFound
	}
	atts, err := s.listAttachmentsWhere(ctx, tx, actor, "a.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(atts) == 0 {
		return nil, engine.ErrNotFound
	}
	if _, err := s.eng.Get(ctx, tx, actor, atts[0].ElementID); err != nil {
		return nil, err
	}
	return &atts[0], nil
}

// RemoveAttachment elimina logicamente l'allegato (il file resta: può essere un'evidenza).
func (s *Service) RemoveAttachment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	a, err := s.Download(ctx, tx, actor, id)
	if err != nil {
		return err
	}
	if !a.CanDelete {
		return engine.ErrForbidden
	}
	tag, err := tx.Exec(ctx, `update attachments set removed_at = now() where id = $1 and removed_at is null`, a.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	if err := engine.AddActivity(ctx, tx, a.ElementID, actor.MemberID, "attachment", "removed",
		map[string]any{"attachment_id": a.ID, "name": a.Name}, nil); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "attachment.remove", EntityType: "attachment", EntityID: a.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"element_id": a.ElementID, "sha256": a.SHA256}})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
