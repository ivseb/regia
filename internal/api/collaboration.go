package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/modules/collab"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Collaborazione (piano, passo 3): commenti, allegati, osservatori e notifiche in-app.

const defaultMaxUpload = 25 << 20

func newCollab(o Options) *collab.Service {
	limit := o.MaxUploadBytes
	if limit <= 0 {
		limit = defaultMaxUpload
	}
	var files *collab.FileStore
	if o.FilesDir != "" {
		fs, err := collab.NewFileStore(o.FilesDir)
		if err != nil {
			if o.Log != nil {
				o.Log.Error("cartella degli allegati non utilizzabile: allegati disattivati", "cartella", o.FilesDir, "errore", err)
			}
		} else {
			files = fs
		}
	}
	return collab.New(o.Engine, files, limit)
}

// --- Commenti ---------------------------------------------------------

func (s *Server) listComments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.collab.Comments(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return map[string]any{"items": items}, err
	})
}

type commentInput struct {
	Body string `json:"body"`
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in commentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*collab.Comment, error) {
		return s.collab.AddComment(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Body)
	})
}

func (s *Server) editComment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in commentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*collab.Comment, error) {
		return s.collab.EditComment(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Body)
	})
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.collab.DeleteComment(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// --- Allegati ---------------------------------------------------------

func (s *Server) listAttachments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.collab.Attachments(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return map[string]any{"items": items, "max_bytes": s.collab.MaxUpload}, err
	})
}

// uploadAttachment riceve un file in multipart/form-data (campo "file", facoltativo
// "comment_id"). Il corpo si legge in streaming: niente file temporanei di net/http,
// niente limite di 1 MiB del JSON. I permessi si verificano prima di leggere il file.
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if s.collab.Files == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "attachments.unavailable", "allegati non disponibili su questo server")
		return
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: multipart/form-data")
		return
	}
	limit := s.collab.MaxUpload
	if r.ContentLength > limit+(1<<20) {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "attachment.too_large", tooLarge(limit))
		return
	}
	actor := actorOf(r, p)
	var elementID string
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		elementID, err = s.collab.PrepareUpload(r.Context(), tx, actor, r.PathValue("id"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	// Dal telefono un file grande può richiedere più del ReadTimeout del server.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(15 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20)) // margine per le intestazioni multipart
	mr, err := r.MultipartReader()
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_multipart", "corpo multipart non valido")
		return
	}
	var commentID, name string
	var blob *collab.Blob
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.uploadError(w, r, err, limit)
			return
		}
		switch part.FormName() {
		case "comment_id":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			commentID = strings.TrimSpace(string(b))
		case "file":
			if blob != nil {
				writeProblem(w, r, http.StatusBadRequest, "attachment.one_file", "un file per richiesta")
				return
			}
			name = part.FileName()
			b, err := s.collab.Files.Put(part, limit, name)
			if err != nil {
				s.uploadError(w, r, err, limit)
				return
			}
			blob = &b
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		part.Close()
	}
	if blob == nil {
		writeProblem(w, r, http.StatusBadRequest, "attachment.missing", "manca il file (campo \"file\")")
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*collab.Attachment, error) {
		return s.collab.SaveAttachment(r.Context(), tx, actor, elementID, commentID, name, *blob)
	})
}

func tooLarge(limit int64) string {
	return fmt.Sprintf("file troppo grande: il limite è %d MB", limit>>20)
}

func (s *Server) uploadError(w http.ResponseWriter, r *http.Request, err error, limit int64) {
	var mbe *http.MaxBytesError
	switch {
	case errors.Is(err, collab.ErrTooLarge), errors.As(err, &mbe):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "attachment.too_large", tooLarge(limit))
	case errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "multipart"):
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_multipart", "caricamento interrotto o corpo multipart non valido")
	default:
		s.internal(w, r, err)
	}
}

// downloadAttachment serve il file in modo sicuro: Content-Disposition attachment per
// tutto tranne le immagini sicure, nosniff, CSP chiusa e sandbox, tipo mai "attivo".
func (s *Server) downloadAttachment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if s.collab.Files == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "attachments.unavailable", "allegati non disponibili su questo server")
		return
	}
	var a *collab.Attachment
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		a, err = s.collab.Download(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	f, err := s.collab.Files.Open(a.SHA256)
	if err != nil {
		s.log.Error("file di un allegato mancante", "allegato", a.ID, "errore", err)
		writeProblem(w, r, http.StatusNotFound, "attachment.missing_file", "file non disponibile")
		return
	}
	defer f.Close()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Minute))
	h := w.Header()
	inline := collab.InlineImage(a.MimeType) && r.URL.Query().Get("download") == ""
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	h.Set("Content-Type", collab.ServedType(a.MimeType))
	h.Set("Content-Disposition", disp+"; filename=\""+asciiName(a.Name)+"\"; filename*=UTF-8''"+url.PathEscape(a.Name))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("ETag", `"`+a.SHA256+`"`)
	http.ServeContent(w, r, "", a.CreatedAt, f)
}

// asciiName è il nome di riserva per i client che non capiscono filename*.
func asciiName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
}

func (s *Server) removeAttachment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.collab.RemoveAttachment(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// --- Osservatori ------------------------------------------------------

func (s *Server) watchStatus(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (notify.WatchStatus, error) {
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		if err != nil {
			return notify.WatchStatus{}, err
		}
		return notify.Status(r.Context(), tx, el.ID, p.MemberID)
	})
}

func (s *Server) setWatch(w http.ResponseWriter, r *http.Request, p *auth.Principal, follow bool) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (notify.WatchStatus, error) {
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		if err != nil {
			return notify.WatchStatus{}, err
		}
		if err := notify.Watch(r.Context(), tx, el.ID, p.MemberID, follow); err != nil {
			return notify.WatchStatus{}, err
		}
		return notify.Status(r.Context(), tx, el.ID, p.MemberID)
	})
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.setWatch(w, r, p, true)
}
func (s *Server) unwatch(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.setWatch(w, r, p, false)
}

// --- Notifiche --------------------------------------------------------

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	var page notify.Page
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		page, err = notify.List(r.Context(), tx, p.MemberID, q.Get("unread") == "true", limit, q.Get("cursor"))
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "cursore") {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", err.Error())
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) countNotifications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		n, err := notify.Unread(r.Context(), tx, p.MemberID)
		return map[string]int{"unread": n}, err
	})
}

func (s *Server) readNotification(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var found bool
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		found, err = notify.MarkRead(r.Context(), tx, p.MemberID, r.PathValue("id"))
		return err
	})
	switch {
	case err != nil:
		s.internal(w, r, err)
	case !found:
		writeProblem(w, r, http.StatusNotFound, "notification.not_found", "notifica non trovata")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) readAllNotifications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		n, err := notify.MarkAllRead(r.Context(), tx, p.MemberID)
		return map[string]int64{"updated": n}, err
	})
}

func (s *Server) getNotificationPrefs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (notify.Prefs, error) {
		return notify.GetPrefs(r.Context(), tx, p.MemberID)
	})
}

func (s *Server) setNotificationPrefs(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Kinds []notify.KindPref `json:"kinds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var prefs notify.Prefs
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if err := notify.SetPrefs(r.Context(), tx, p.MemberID, in.Kinds); err != nil {
			return err
		}
		var err error
		prefs, err = notify.GetPrefs(r.Context(), tx, p.MemberID)
		return err
	})
	if errors.Is(err, notify.ErrUnknownKind) {
		writeProblem(w, r, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
