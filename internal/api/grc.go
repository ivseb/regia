package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/collab"
	"regia/internal/modules/grc"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Conformità (piano, passo 20; replica/grc.md): cataloghi normativi, controlli,
// applicabilità, evidenze, cruscotto e pacchetto per l'audit. I permessi li verifica il
// modulo (grc.read, grc.admin, grc.collect).

func (s *Server) grcInit(o Options) {
	s.grc = o.GRC
	if s.grc == nil && o.Engine != nil {
		s.grc = grc.Register(o.Engine)
	}
	if o.FilesDir != "" {
		if fs, err := collab.NewFileStore(o.FilesDir); err == nil {
			s.grcFiles = fs
		}
	}
}

func (s *Server) grcRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/grc/dashboard", inTenant(s.grcDashboard))
	mux.HandleFunc("GET /api/v1/grc/library", inTenant(s.grcLibrary))
	mux.HandleFunc("PUT /api/v1/grc/settings", inTenant(s.grcSettings))
	mux.HandleFunc("GET /api/v1/grc/export", inTenant(s.grcExportPackage))
	mux.HandleFunc("POST /api/v1/grc/import", inTenant(s.grcImportPackage))
	mux.HandleFunc("GET /api/v1/grc/audit-pack", inTenant(s.grcAuditPack))
	// Cataloghi.
	mux.HandleFunc("POST /api/v1/grc/starters/{key}", inTenant(s.grcImportStarter))
	mux.HandleFunc("POST /api/v1/grc/frameworks/import", inTenant(s.grcImportCatalog))
	mux.HandleFunc("GET /api/v1/grc/frameworks/{key}", inTenant(s.grcFramework))
	mux.HandleFunc("GET /api/v1/grc/frameworks/{key}/coverage", inTenant(s.grcCoverage))
	mux.HandleFunc("GET /api/v1/grc/frameworks/{key}/export", inTenant(s.grcExportCatalog))
	mux.HandleFunc("POST /api/v1/grc/frameworks/{key}/draft", inTenant(s.grcNewDraft))
	mux.HandleFunc("DELETE /api/v1/grc/frameworks/{key}/draft", inTenant(s.grcDiscardDraft))
	mux.HandleFunc("PUT /api/v1/grc/frameworks/{key}/draft/items", inTenant(s.grcDraftItem))
	mux.HandleFunc("DELETE /api/v1/grc/frameworks/{key}/draft/items", inTenant(s.grcRemoveDraftItem))
	mux.HandleFunc("POST /api/v1/grc/frameworks/{key}/publish", inTenant(s.grcPublish))
	mux.HandleFunc("PUT /api/v1/grc/frameworks/{key}/status", inTenant(s.grcStatus))
	// Corrispondenze tra normative.
	mux.HandleFunc("GET /api/v1/grc/mappings", inTenant(s.grcMappings))
	mux.HandleFunc("GET /api/v1/grc/mappings.csv", inTenant(s.grcMappingsCSV))
	mux.HandleFunc("POST /api/v1/grc/mappings", inTenant(s.grcAddMapping))
	mux.HandleFunc("POST /api/v1/grc/mappings/import", inTenant(s.grcImportMappings))
	mux.HandleFunc("DELETE /api/v1/grc/mappings/{id}", inTenant(s.grcDeleteMapping))
	// Requisiti normativi e applicabilità.
	mux.HandleFunc("GET /api/v1/grc/requirements/{id}", inTenant(s.grcRequirement))
	mux.HandleFunc("PUT /api/v1/grc/requirements/{id}/applicability", inTenant(s.grcRequirementApplicability))
	mux.HandleFunc("GET /api/v1/grc/rules", inTenant(s.grcRules))
	mux.HandleFunc("PUT /api/v1/grc/rules", inTenant(s.grcSaveRules))
	mux.HandleFunc("POST /api/v1/grc/rules/preview", inTenant(s.grcPreview))
	mux.HandleFunc("POST /api/v1/grc/recompute", inTenant(s.grcRecompute))
	mux.HandleFunc("GET /api/v1/grc/scopes/{kind}/{target}", inTenant(s.grcScope))
	// Controlli.
	mux.HandleFunc("GET /api/v1/grc/controls", inTenant(s.grcControls))
	mux.HandleFunc("POST /api/v1/grc/controls", inTenant(s.grcCreateControl))
	mux.HandleFunc("GET /api/v1/grc/controls/{ref}", inTenant(s.grcControl))
	mux.HandleFunc("PATCH /api/v1/grc/controls/{ref}", inTenant(s.grcUpdateControl))
	mux.HandleFunc("POST /api/v1/grc/controls/{ref}/assessment", inTenant(s.grcAssess))
	mux.HandleFunc("PUT /api/v1/grc/controls/{ref}/requirements", inTenant(s.grcControlRequirements))
	mux.HandleFunc("PUT /api/v1/grc/controls/{ref}/scopes", inTenant(s.grcControlScopes))
	// Evidenze.
	mux.HandleFunc("GET /api/v1/grc/evidence", inTenant(s.grcEvidenceList))
	mux.HandleFunc("POST /api/v1/grc/evidence", inTenant(s.grcCreateEvidence))
	mux.HandleFunc("POST /api/v1/grc/evidence/automatic", inTenant(s.grcAutomatic))
	mux.HandleFunc("GET /api/v1/grc/evidence/{ref}", inTenant(s.grcEvidence))
	mux.HandleFunc("POST /api/v1/grc/evidence/{ref}/validate", inTenant(s.grcValidate))
	mux.HandleFunc("POST /api/v1/grc/evidence/{ref}/reject", inTenant(s.grcEvidenceStatus("rejected")))
	mux.HandleFunc("POST /api/v1/grc/evidence/{ref}/revoke", inTenant(s.grcEvidenceStatus("revoked")))
	// Requisiti di progetto → requisiti normativi.
	mux.HandleFunc("GET /api/v1/grc/project-requirements/{ref}/links", inTenant(s.grcLinks))
	mux.HandleFunc("PUT /api/v1/grc/project-requirements/{ref}/links", inTenant(s.grcSetLinks))
	s.grcLivingRoutes(mux)
	s.grcAuditRoutes(mux)
}

func (s *Server) grcDashboard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.Dashboard, error) {
		return s.grc.Dashboard(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) grcLibrary(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.Library, error) {
		return s.grc.Library(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) grcSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.Settings, error) {
		return s.grc.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

// download scrive un file da scaricare con il nome indicato.
func download(w http.ResponseWriter, contentType, name string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(body)
}

func (s *Server) grcExportPackage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *grc.Package
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.grc.ExportPackage(r.Context(), tx, actorOf(r, p))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	download(w, "application/json", "conformita-"+time.Now().Format(time.DateOnly)+".json", body)
}

func (s *Server) grcImportPackage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "pacchetto troppo grande (massimo 8 MB)")
		return
	}
	pkg, err := grc.ParsePackage(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	publish := r.URL.Query().Get("publish") != "false"
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ImportResult, error) {
		return s.grc.ImportPackage(r.Context(), tx, actorOf(r, p), pkg, publish)
	})
}

func (s *Server) grcAuditPack(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	f, err := os.CreateTemp("", "regia-audit-*.zip")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	framework := r.URL.Query().Get("framework")
	var files grc.FileSource
	if s.grcFiles != nil {
		files = s.grcFiles
	}
	// Non in sola lettura: l'export resta nell'audit.
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		return s.grc.WriteAuditPack(r.Context(), tx, actorOf(r, p), f, framework, files)
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.internal(w, r, err)
		return
	}
	name := "audit-conformita"
	if framework != "" {
		name += "-" + framework
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+"-"+time.Now().Format(time.DateOnly)+`.zip"`)
	_, _ = io.Copy(w, f)
}

// --- Cataloghi ---------------------------------------------------------

func (s *Server) grcImportStarter(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	in := struct {
		Enable *bool `json:"enable"`
	}{}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	enable := in.Enable == nil || *in.Enable
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.StarterResult, error) {
		return s.grc.ImportStarter(r.Context(), tx, actorOf(r, p), r.PathValue("key"), enable)
	})
}

type grcImportInput struct {
	Format    string            `json:"format"` // json | csv | oscal
	Content   string            `json:"content"`
	Framework grc.FrameworkMeta `json:"framework"`
	Version   grc.VersionMeta   `json:"version"`
	Publish   bool              `json:"publish"`
	Enable    bool              `json:"enable"`
}

func (s *Server) grcImportCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grcImportInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var c *grc.CatalogFile
	var err error
	switch in.Format {
	case "json", "":
		c, err = grc.DecodeCatalog([]byte(in.Content))
	case "csv":
		c, err = grc.CatalogFromCSV(in.Content, in.Framework, in.Version)
	case "oscal":
		c, err = grc.FromOSCAL([]byte(in.Content), in.Framework, in.Version.Version)
	default:
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "formato non ammesso (json, csv, oscal)")
		return
	}
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkInfo, error) {
		return s.grc.ImportCatalog(r.Context(), tx, actorOf(r, p), c, grc.ImportOptions{Publish: in.Publish, Enable: in.Enable})
	})
}

func (s *Server) grcFramework(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkDetail, error) {
		return s.grc.Framework(r.Context(), tx, actorOf(r, p), r.PathValue("key"), r.URL.Query().Get("draft") != "")
	})
}

func (s *Server) grcCoverage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkCoverage, error) {
		return s.grc.FrameworkCoverage(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) grcExportCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	format := r.URL.Query().Get("format")
	var c *grc.CatalogFile
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		c, err = s.grc.Export(r.Context(), tx, actorOf(r, p), r.PathValue("key"), r.URL.Query().Get("version"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	base := c.Framework.Key + "-" + strings.NewReplacer("/", "-", " ", "-").Replace(c.Version.Version)
	switch format {
	case "oscal":
		body, err := grc.ToOSCAL(c, time.Now())
		if err != nil {
			s.internal(w, r, err)
			return
		}
		download(w, "application/json", base+".oscal.json", body)
	case "csv":
		var b bytes.Buffer
		if err := grc.WriteCatalogCSV(&b, c); err != nil {
			s.internal(w, r, err)
			return
		}
		download(w, "text/csv; charset=utf-8", base+".csv", b.Bytes())
	case "json", "":
		body, _ := json.MarshalIndent(c, "", "  ")
		download(w, "application/json", base+".json", body)
	default:
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "formato non ammesso (json, csv, oscal)")
	}
}

func (s *Server) grcNewDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.FrameworkInfo, error) {
		return s.grc.NewDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Version)
	})
}

func (s *Server) grcDiscardDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.grc.DiscardDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) grcDraftItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.CatalogItem
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkDetail, error) {
		return s.grc.DraftItem(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in, false)
	})
}

func (s *Server) grcRemoveDraftItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkDetail, error) {
		return s.grc.DraftItem(r.Context(), tx, actorOf(r, p), r.PathValue("key"), grc.CatalogItem{Ref: r.URL.Query().Get("ref")}, true)
	})
}

func (s *Server) grcPublish(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkInfo, error) {
		return s.grc.Publish(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) grcStatus(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.FrameworkInfo, error) {
		return s.grc.SetStatus(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Status)
	})
}

// --- Corrispondenze ----------------------------------------------------

func (s *Server) grcMappings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Mappings(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("framework"))
		return map[string]any{"items": items, "relations": grc.RelationNames}, err
	})
}

func (s *Server) grcMappingsCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var items []grc.MappingView
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		items, err = s.grc.Mappings(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("framework"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	var b bytes.Buffer
	if err := grc.WriteMappingsCSV(&b, items); err != nil {
		s.internal(w, r, err)
		return
	}
	download(w, "text/csv; charset=utf-8", "corrispondenze-"+time.Now().Format(time.DateOnly)+".csv", b.Bytes())
}

func (s *Server) grcAddMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.MappingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.MappingView, error) {
		return s.grc.AddMapping(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) grcImportMappings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		CSV string `json:"csv"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ms, err := grc.MappingsFromCSV(in.CSV)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		res, err := s.grc.ImportPackage(r.Context(), tx, actorOf(r, p), &grc.Package{Format: grc.PackageFormat, Mappings: ms}, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"created": res.Mappings, "rows": len(ms)}, nil
	})
}

func (s *Server) grcDeleteMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.grc.DeleteMapping(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// --- Requisiti normativi e applicabilità ------------------------------

func (s *Server) grcRequirement(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.RequirementDetail, error) {
		return s.grc.Requirement(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) grcRequirementApplicability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []grc.Decision `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.RequirementDetail, error) {
		a := actorOf(r, p)
		if err := s.grc.SetRequirementApplicability(r.Context(), tx, a, r.PathValue("id"), in.Items); err != nil {
			return nil, err
		}
		return s.grc.Requirement(r.Context(), tx, a, r.PathValue("id"))
	})
}

func (s *Server) grcRules(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.RulesView, error) {
		return s.grc.Rules(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) grcSaveRules(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []grc.RuleInput `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.RulesView, error) {
		return s.grc.SaveRules(r.Context(), tx, actorOf(r, p), in.Items)
	})
}

func (s *Server) grcPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Scope     string `json:"scope"`
		Condition string `json:"condition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Preview(r.Context(), tx, actorOf(r, p), in.Scope, in.Condition)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) grcRecompute(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.RecomputeResult, error) {
		a := actorOf(r, p)
		if !a.Has(grc.PermAdmin) {
			return nil, engine.ErrForbidden
		}
		return s.grc.Recompute(r.Context(), tx, a)
	})
}

func (s *Server) grcScope(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.ScopeCompliance, error) {
		return s.grc.ScopeCompliance(r.Context(), tx, actorOf(r, p), r.PathValue("kind"), r.PathValue("target"))
	})
}

// --- Controlli ---------------------------------------------------------

func (s *Server) grcControls(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.Controls(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) grcCreateControl(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.ControlInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.CreateControl(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) grcControl(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.Control(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcUpdateControl(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.ControlUpdate
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.UpdateControl(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) grcAssess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Effectiveness string `json:"effectiveness"`
		Note          string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.Assess(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Effectiveness, in.Note)
	})
}

func (s *Server) grcControlRequirements(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []grc.MappedInput `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.SetControlRequirements(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Items)
	})
}

func (s *Server) grcControlScopes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Items []grc.Decision `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.ControlView, error) {
		return s.grc.SetControlScopes(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Items)
	})
}

// --- Evidenze ----------------------------------------------------------

func (s *Server) grcEvidenceList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.grc.EvidenceList(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("status"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) grcCreateEvidence(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.EvidenceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*grc.EvidenceView, error) {
		return s.grc.CreateEvidence(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) grcAutomatic(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.AutoEvidence
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *grc.AutoResult
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		out, err = s.grc.RecordAutomatic(r.Context(), tx, actorOf(r, p), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

func (s *Server) grcEvidence(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.EvidenceView, error) {
		return s.grc.Evidence(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcValidate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in grc.ValidateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.EvidenceView, error) {
		return s.grc.Validate(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) grcEvidenceStatus(status string) func(http.ResponseWriter, *http.Request, *auth.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		var in struct {
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.EvidenceView, error) {
			return s.grc.SetEvidenceStatus(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), status, in.Note)
		})
	}
}

func (s *Server) grcLinks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*grc.LinksView, error) {
		return s.grc.RequirementLinks(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) grcSetLinks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Refs []string `json:"refs"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*grc.LinksView, error) {
		return s.grc.SetRequirementLinks(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Refs)
	})
}
