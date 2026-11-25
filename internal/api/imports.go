package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/importer"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Import di progetti da altri strumenti (Configurazione → Import; docs/import.md):
// caricamento del CSV o connessione all'API, analisi, mappatura (salvabile come profilo),
// anteprima senza salvare, esecuzione in background, report e annullamento.

// maxImportUpload: un export di Jira con 50.000 issue e i commenti supera di molto i 16 MB
// dei CSV dei costi.
const maxImportUpload = 512 << 20

func newImporter(o Options, s *Server) *importer.Service {
	if o.Importer != nil {
		return o.Importer
	}
	return importer.New(importer.Options{DB: o.DB, Engine: o.Engine, Files: s.collab.Files, MaxAttachment: s.collab.MaxUpload,
		AllowPrivate: o.Dev || o.ImportAllowPrivate, Log: o.Log})
}

// CloseImports ferma le esecuzioni in background (restano interrotte e si riprendono).
func (s *Server) CloseImports() {
	if s.imp != nil {
		s.imp.Close()
	}
}

func (s *Server) importRoutes(mux *http.ServeMux) {
	ad := func(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
		return can(rbac.TenantAdmin, h)
	}
	mux.HandleFunc("GET /api/v1/imports", ad(s.listImportRuns))
	mux.HandleFunc("GET /api/v1/imports/catalog", ad(s.importCatalog))
	mux.HandleFunc("POST /api/v1/imports/upload", ad(s.uploadImport))
	mux.HandleFunc("POST /api/v1/imports/connect", ad(s.connectImport))
	mux.HandleFunc("GET /api/v1/import-profiles", ad(s.listWorkImportProfiles))
	mux.HandleFunc("POST /api/v1/import-profiles", ad(s.saveWorkImportProfile))
	mux.HandleFunc("PUT /api/v1/import-profiles/{pid}", ad(s.saveWorkImportProfile))
	mux.HandleFunc("DELETE /api/v1/import-profiles/{pid}", ad(s.deleteWorkImportProfile))
	mux.HandleFunc("GET /api/v1/imports/{id}", ad(s.getImportRun))
	mux.HandleFunc("PUT /api/v1/imports/{id}/mapping", ad(s.saveImportMapping))
	mux.HandleFunc("GET /api/v1/imports/{id}/suggest", ad(s.suggestImportMapping))
	mux.HandleFunc("POST /api/v1/imports/{id}/profile/{pid}", ad(s.applyImportProfile))
	mux.HandleFunc("POST /api/v1/imports/{id}/preview", ad(s.previewImportRun))
	mux.HandleFunc("POST /api/v1/imports/{id}/start", ad(s.startImportRun))
	mux.HandleFunc("POST /api/v1/imports/{id}/rollback", ad(s.rollbackImportRun))
}

func (s *Server) listImportRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.imp.Runs(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) importCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*importer.Catalog, error) {
		return s.imp.GetCatalog(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) getImportRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*importer.Run, error) {
		return s.imp.GetRun(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// uploadImport riceve il CSV in multipart/form-data: campi "source" e "delimiter" (facoltativo)
// prima del campo "file". Il file si legge in streaming.
func (s *Server) uploadImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: multipart/form-data")
		return
	}
	if r.ContentLength > maxImportUpload {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "import.too_large", "file troppo grande (massimo 512 MB): dividi l'export")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(15 * time.Minute))
	_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_multipart", "corpo multipart non valido")
		return
	}
	in := importer.UploadInput{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			writeProblem(w, r, http.StatusBadRequest, "import.no_file", "manca il campo «file»")
			return
		}
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeProblem(w, r, http.StatusRequestEntityTooLarge, "import.too_large", "file troppo grande (massimo 512 MB)")
				return
			}
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_multipart", "corpo multipart non valido")
			return
		}
		switch part.FormName() {
		case "source", "delimiter":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			if part.FormName() == "source" {
				in.Source = strings.TrimSpace(string(b))
			} else {
				in.Delimiter = string(b)
			}
			continue
		case "file":
			in.FileName = part.FileName()
			run, err := s.imp.Upload(r.Context(), p.TenantID, actorOf(r, p), in, part)
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) || strings.Contains(err.Error(), "request body too large") {
					writeProblem(w, r, http.StatusRequestEntityTooLarge, "import.too_large", "file troppo grande (massimo 512 MB)")
					return
				}
				s.engineError(w, r, err)
				return
			}
			writeJSON(w, http.StatusCreated, run)
			return
		default:
			_, _ = io.Copy(io.Discard, part)
		}
	}
}

func (s *Server) connectImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in importer.Connection
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := s.imp.Connect(r.Context(), p.TenantID, actorOf(r, p), in)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) saveImportMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in importer.Mapping
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*importer.MappingResult, error) {
		return s.imp.SaveMapping(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) suggestImportMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*importer.Mapping, error) {
		return s.imp.Suggest(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) applyImportProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*importer.MappingResult, error) {
		return s.imp.ApplyProfile(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("pid"))
	})
}

func (s *Server) previewImportRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
	run, err := s.imp.Preview(r.Context(), p.TenantID, actorOf(r, p), r.PathValue("id"))
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) startImportRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Token string `json:"token"`
		Email string `json:"email"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	var conn *importer.Connection
	if strings.TrimSpace(in.Token) != "" {
		// Il token si inserisce di nuovo dopo un riavvio: il resto della connessione è nell'esecuzione.
		c, err := s.reconnect(r, p, in.Token, in.Email)
		if err != nil {
			s.engineError(w, r, err)
			return
		}
		conn = c
	}
	run, err := s.imp.Start(r.Context(), p.TenantID, actorOf(r, p), r.PathValue("id"), conn)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) reconnect(r *http.Request, p *auth.Principal, token, email string) (*importer.Connection, error) {
	var c *importer.Connection
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		c, err = s.imp.Reconnect(r.Context(), tx, actorOf(r, p), r.PathValue("id"), token, email)
		return err
	})
	return c, err
}

func (s *Server) rollbackImportRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	run, err := s.imp.Rollback(r.Context(), p.TenantID, actorOf(r, p), r.PathValue("id"))
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) listWorkImportProfiles(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.imp.Profiles(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) saveWorkImportProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in importer.ProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.PathValue("pid") == "" {
		status = http.StatusCreated
	}
	tenantJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*importer.Profile, error) {
		return s.imp.SaveProfile(r.Context(), tx, actorOf(r, p), r.PathValue("pid"), in)
	})
}

func (s *Server) deleteWorkImportProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.imp.DeleteProfile(r.Context(), tx, actorOf(r, p), r.PathValue("pid"))
	})
}
