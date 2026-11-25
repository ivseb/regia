package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/collab"
	"regia/internal/modules/knowledge"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Knowledge, rifiniture (replica/knowledge.md): commenti in linea, conferma di lettura dei
// documenti controllati, export e import in Markdown (zip).

func (s *Server) knowledgePlusRoutes(mux *http.ServeMux) {
	if s.kb != nil && s.collab != nil && s.collab.Files != nil {
		s.kb.SetFiles(kbFiles{s: s})
	}
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}/threads", inTenant(s.kbThreads))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/threads", inTenant(s.kbCreateThread))
	mux.HandleFunc("POST /api/v1/kb/threads/{id}/comments", inTenant(s.kbReplyThread))
	mux.HandleFunc("DELETE /api/v1/kb/threads/{id}/comments/{cid}", inTenant(s.kbDeleteThreadComment))
	mux.HandleFunc("POST /api/v1/kb/threads/{id}/resolve", inTenant(s.kbResolveThread))
	mux.HandleFunc("POST /api/v1/kb/threads/{id}/reopen", inTenant(s.kbReopenThread))

	mux.HandleFunc("GET /api/v1/kb/audience", inTenant(s.kbAudience))
	mux.HandleFunc("GET /api/v1/kb/pages/{ref}/readings", inTenant(s.kbReadings))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/readings", inTenant(s.kbCreateReading))
	mux.HandleFunc("POST /api/v1/kb/pages/{ref}/readings/preview", inTenant(s.kbPreviewReading))
	mux.HandleFunc("PATCH /api/v1/kb/readings/{id}", inTenant(s.kbUpdateReading))
	mux.HandleFunc("POST /api/v1/kb/readings/{id}/confirm", inTenant(s.kbConfirmReading))
	mux.HandleFunc("POST /api/v1/kb/readings/{id}/remind", inTenant(s.kbRemindReading))
	mux.HandleFunc("GET /api/v1/kb/readings/{id}/export", inTenant(s.kbExportReading))

	mux.HandleFunc("GET /api/v1/kb/spaces/{key}/export", inTenant(s.kbExport))
	mux.HandleFunc("POST /api/v1/kb/spaces/{key}/import", inTenant(s.kbImport))
}

// kbFiles collega gli allegati di collaborazione al modulo Knowledge (export e import).
type kbFiles struct{ s *Server }

func (f kbFiles) Open(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (string, io.ReadCloser, error) {
	a, err := f.s.collab.Download(ctx, tx, actor, id)
	if err != nil {
		return "", nil, err
	}
	file, err := f.s.collab.Files.Open(a.SHA256)
	if err != nil {
		return "", nil, err
	}
	return a.Name, file, nil
}

func (f kbFiles) Store(ctx context.Context, tx pgx.Tx, actor engine.Actor, pageID, name string, r io.Reader) (string, error) {
	if !collab.CanUpload(actor) {
		return "", engine.ErrForbidden
	}
	blob, err := f.s.collab.Files.Put(r, f.s.collab.MaxUpload, name)
	if err != nil {
		return "", err
	}
	att, err := f.s.collab.SaveAttachment(ctx, tx, actor, pageID, "", name, blob)
	if err != nil {
		return "", err
	}
	return att.ID, nil
}

// --- Commenti in linea ------------------------------------------------

func (s *Server) kbThreads(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.Threads(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) kbCreateThread(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.ThreadInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.CreateThread(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) kbReplyThread(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.ReplyThread(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Body)
	})
}

func (s *Server) kbDeleteThreadComment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.DeleteThreadComment(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("cid"))
	})
}

func (s *Server) kbResolveThread(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.SetThreadStatus(r.Context(), tx, actorOf(r, p), r.PathValue("id"), true)
	})
}

func (s *Server) kbReopenThread(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.ThreadList, error) {
		return s.kb.SetThreadStatus(r.Context(), tx, actorOf(r, p), r.PathValue("id"), false)
	})
}

// --- Conferma di lettura ----------------------------------------------

// kbAudience elenca i gruppi e i ruoli da scegliere come pubblico di una campagna.
func (s *Server) kbAudience(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	actor := actorOf(r, p)
	if !actor.Has("element.write") && !actor.Has("tenant.admin") {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	type option struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Members int    `json:"members,omitempty"`
	}
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		out := map[string]any{}
		rows, err := tx.Query(r.Context(), `select g.id::text, g.name, (select count(*) from group_effective_members(g.id))::int
			from groups g order by g.name limit 500`)
		if err != nil {
			return nil, err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowToStructByPos[option])
		if err != nil {
			return nil, err
		}
		rows, err = tx.Query(r.Context(), `select r.id::text, r.name from roles r order by r.name limit 200`)
		if err != nil {
			return nil, err
		}
		roles := []option{}
		for rows.Next() {
			var o option
			if err := rows.Scan(&o.ID, &o.Name); err != nil {
				rows.Close()
				return nil, err
			}
			roles = append(roles, o)
		}
		rows.Close()
		if groups == nil {
			groups = []option{}
		}
		out["groups"], out["roles"] = groups, roles
		return out, rows.Err()
	})
}

func (s *Server) kbReadings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.Readings, error) {
		return s.kb.Readings(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) kbCreateReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.ReadingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*knowledge.Readings, error) {
		return s.kb.CreateReading(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) kbPreviewReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Audience []knowledge.AudienceRef `json:"audience"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*knowledge.AudiencePreview, error) {
		return s.kb.PreviewAudience(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Audience)
	})
}

func (s *Server) kbUpdateReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in knowledge.ReadingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.Readings, error) {
		return s.kb.UpdateReading(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) kbConfirmReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Version int `json:"version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.Readings, error) {
		return s.kb.ConfirmReading(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Version)
	})
}

func (s *Server) kbRemindReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.Readings, error) {
		return s.kb.RemindReading(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) kbExportReading(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var name string
	var data []byte
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		name, data, err = s.kb.ReadingCSV(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// --- Export e import in Markdown --------------------------------------

func (s *Server) kbExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	opt := knowledge.ExportOptions{Root: strings.TrimSpace(q.Get("pagina")), Approved: q.Get("approvate") == "1", Attachments: q.Get("allegati") != "0"}
	actor := actorOf(r, p)
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		plan, err := s.kb.PlanExport(r.Context(), tx, actor, r.PathValue("key"), opt)
		if err != nil {
			return err
		}
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Minute))
		h := w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", `attachment; filename="`+plan.Filename+`"`)
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		return plan.Write(r.Context(), tx, actor, w)
	})
	if err != nil {
		if w.Header().Get("Content-Type") == "application/zip" {
			s.log.Error("export dello spazio interrotto", "errore", err)
			return
		}
		s.engineError(w, r, err)
	}
}

func (s *Server) kbImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: multipart/form-data")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, knowledge.MaxImportBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_multipart", "corpo multipart non valido")
		return
	}
	var data []byte
	opt := knowledge.ImportOptions{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "import.too_large", "il file supera il limite di caricamento")
			return
		}
		switch part.FormName() {
		case "parent":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			opt.Parent = strings.TrimSpace(string(b))
		case "dry":
			b, _ := io.ReadAll(io.LimitReader(part, 8))
			opt.Dry = strings.TrimSpace(string(b)) == "1"
		case "file":
			data, err = io.ReadAll(io.LimitReader(part, knowledge.MaxImportBytes+1))
			if err != nil || len(data) > knowledge.MaxImportBytes {
				writeProblem(w, r, http.StatusRequestEntityTooLarge, "import.too_large", "lo zip supera il limite di 64 MB")
				return
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		part.Close()
	}
	if len(data) == 0 {
		writeProblem(w, r, http.StatusBadRequest, "import.missing", "manca il file zip (campo \"file\")")
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "import.invalid_zip", "il file non è uno zip valido")
		return
	}
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
	kbJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*knowledge.ImportReport, error) {
		return s.kb.ImportZip(r.Context(), tx, actorOf(r, p), r.PathValue("key"), zr, opt)
	})
}
